package assemble

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strconv"
	"sync"
)

const (
	DefaultFileIdentifierHeader = "x-assemble-file-id"
	DefaultFileMimeTypeHeader   = "x-assemble-content-type"
	DefaultChunkSequenceHeader  = "x-assemble-chunk-sequence"
	DefaultChunkTotalHeader     = "x-assemble-chunk-total"
)

type FileChunksAssembler struct {
	Config *AssemblerConfig
	data   map[string]*file
	// Locks FileChunksAssembler.data. Config should be constant.
	lock sync.Mutex
}

type AssemblerConfig struct {

	// Header name for ID of the file being uploaded.
	//
	// Default: x-assemble-file-id
	FileIdentifierHeader string

	// Header name for content type of original file.
	//
	// Default: x-assemble-content-type
	FileMimeTypeHeader string

	// Header name for chunk's sequence number.
	//
	// Default: x-assemble-chunk-sequence
	ChunkSequenceHeader string

	// Header name for total number of chunks.
	//
	// Default: x-assemble-chunk-total
	ChunkTotalHeader string

	// Path to directory where chunks will be saved.
	//
	// Default: $HOME/.go-assemble-data/chunks
	ChunksDir string

	// Path to directory where completed files will be saved.
	//
	// Default: $HOME/.go-assemble-data/completed
	CompletedDir string

	// Don't delete all chunks after combining them
	// (e.g. want to use cron job instead).
	//
	// Default: false
	KeepCompletedChunks bool
}

func NewFileChunksAssembler(config *AssemblerConfig) (*FileChunksAssembler, error) {
	if config == nil {
		config = &AssemblerConfig{}
	}
	if config.FileIdentifierHeader == "" {
		config.FileIdentifierHeader = DefaultFileIdentifierHeader
	}
	if config.FileMimeTypeHeader == "" {
		config.FileMimeTypeHeader = DefaultFileMimeTypeHeader
	}
	if config.ChunkSequenceHeader == "" {
		config.ChunkSequenceHeader = DefaultChunkSequenceHeader
	}
	if config.ChunkTotalHeader == "" {
		config.ChunkTotalHeader = DefaultChunkTotalHeader
	}
	if config.ChunksDir == "" {
		chunksDirBase, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		config.ChunksDir = path.Join(chunksDirBase, ".go-assemble-data", "chunks")
		if err := os.MkdirAll(config.ChunksDir, 0755); err != nil {
			return nil, err
		}
	}
	if config.CompletedDir == "" {
		completedDirBase, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		config.CompletedDir = path.Join(completedDirBase, ".go-assemble-data", "completed")
		if err := os.MkdirAll(config.CompletedDir, 0755); err != nil {
			return nil, err
		}
	}
	a := FileChunksAssembler{
		Config: config,
		data:   make(map[string]*file),
	}
	return &a, nil
}

// Middleware wraps an endpoint that expects a single file. It will collect
// chunks in files until it has determined all chunks have been received.
// For requests that don't have the correct headers, HTTP 400 is returned.
// In downstream handlers, the request body becomes nil and response cannot be
// written to (nil). To get the completed file, use assemble.GetFile(r).
func (a *FileChunksAssembler) Middleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fileID := r.Header.Get(a.Config.FileIdentifierHeader)
		if fileID == "" {
			badRequest(w, "file ID is required")
			return
		}
		// File ID should be cleansed as it becomes part of a file name.
		if containsInvalidCharacters(fileID) {
			badRequest(w, "file ID only supports alphanumeric, underscores and hyphens")
			return
		}
		chunkExpectedTotal, err := strconv.ParseInt(
			r.Header.Get(a.Config.ChunkTotalHeader),
			10,
			64,
		)
		if err != nil {
			badRequest(w, "expected chunks must be an integer")
			return
		}
		if chunkExpectedTotal <= 0 {
			badRequest(w, "expected chunks cannot be negative")
			return
		}
		chunkSequenceID, err := strconv.ParseInt(
			r.Header.Get(a.Config.ChunkSequenceHeader),
			10,
			64,
		)
		if err != nil {
			badRequest(w, "sequence number must be an integer")
			return
		}
		if chunkSequenceID < 0 || chunkSequenceID >= chunkExpectedTotal {
			badRequest(w, "sequence number must be between 0 and N")
			return
		}
		chunkData, err := ioutil.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if len(chunkData) == 0 {
			badRequest(w, "chunk cannot be empty")
			return
		}
		a.lock.Lock()
		if err := a.addFileIfNotExists(fileID, chunkSequenceID, chunkExpectedTotal); err != nil {
			badRequest(w, err.Error())
			a.lock.Unlock()
			return
		}
		if err := a.add(fileID, chunkSequenceID, chunkData); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			a.lock.Unlock()
			return
		}
		var response progressResponse
		if a.isComplete(fileID) {
			completedFilePath, err := a.combineChunks(fileID)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				a.lock.Unlock()
				return
			}
			response.CurrentChunks = a.countChunks(fileID)
			response.ExpectedChunks = a.totalChunks(fileID)
			a.lock.Unlock()

			contentType := r.Header.Get(a.Config.FileMimeTypeHeader)
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			r.Header.Set("Content-Type", contentType)

			contentLength, err := getFileSize(completedFilePath)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			r.Header.Set("Content-Length", strconv.FormatInt(contentLength, 10))

			// Remove chunk-specific headers from request.
			r.Header.Del(a.Config.FileIdentifierHeader)
			r.Header.Del(a.Config.FileMimeTypeHeader)
			r.Header.Del(a.Config.ChunkSequenceHeader)
			r.Header.Del(a.Config.ChunkTotalHeader)

			// Add the file stream as request body.
			f, err := os.Open(completedFilePath)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			defer func() { _ = f.Close() }()
			r.Body = f

			// Downstream requests should use assemble.GetFileID(r).
			ctx := context.WithValue(r.Context(), contextKey("id"), fileID)

			// Cannot send a response downstream as it's used for the final progress update.
			req := *r.WithContext(ctx)
			h.ServeHTTP(nil, &req)

			rejectedFileCode := req.Context().Value(contextKey("error-code"))
			if rejectedFileCode != nil {
				rejectedFileErr := req.Context().Value(contextKey("error-message")).(string)
				response.RejectedError = &rejectedFileErr
				w.WriteHeader(rejectedFileCode.(int))
			}
		} else {
			response.CurrentChunks = a.countChunks(fileID)
			response.ExpectedChunks = a.totalChunks(fileID)
			a.lock.Unlock()
		}
		w.Header().Add("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})
}
