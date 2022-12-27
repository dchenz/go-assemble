package assemble

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strconv"
	"sync"
)

const (
	DefaultUploadIdentifierHeader = "x-assemble-upload-id"
	DefaultChunkIdentifierHeader  = "x-assemble-chunk-id"
)

type FileChunksAssembler struct {
	Config *AssemblerConfig
	data   *tracker
}

type AssemblerConfig struct {

	// Header name for ID of the file being uploaded.
	//
	// Default: x-assemble-file-id
	UploadIdentifierHeader string

	// Header name for chunk's sequence number.
	//
	// Default: x-assemble-chunk-sequence
	ChunkIdentifierHeader string

	// Path to directory where chunks will be saved.
	//
	// Default: $HOME/.go-assemble-data/chunks
	ChunksDir string

	// Path to directory where completed files will be saved.
	//
	// Default: $HOME/.go-assemble-data/completed
	CompletedDir string
}

func NewFileChunksAssembler(config *AssemblerConfig) *FileChunksAssembler {
	if config == nil {
		config = &AssemblerConfig{}
	}
	if config.UploadIdentifierHeader == "" {
		config.UploadIdentifierHeader = DefaultUploadIdentifierHeader
	}
	if config.ChunkIdentifierHeader == "" {
		config.ChunkIdentifierHeader = DefaultChunkIdentifierHeader
	}
	if config.ChunksDir == "" {
		chunksDirBase, err := os.UserHomeDir()
		if err != nil {
			panic(err)
		}
		config.ChunksDir = path.Join(chunksDirBase, ".go-assemble-data", "chunks")
		if err := os.MkdirAll(config.ChunksDir, 0755); err != nil {
			panic(err)
		}
	}
	if config.CompletedDir == "" {
		completedDirBase, err := os.UserHomeDir()
		if err != nil {
			panic(err)
		}
		config.CompletedDir = path.Join(completedDirBase, ".go-assemble-data", "completed")
		if err := os.MkdirAll(config.CompletedDir, 0755); err != nil {
			panic(err)
		}
	}
	return &FileChunksAssembler{
		Config: config,
		data: &tracker{
			uploads:      sync.Map{},
			chunkDir:     config.ChunksDir,
			completedDir: config.CompletedDir,
		},
	}
}

func (a *FileChunksAssembler) getActiveUpload(r *http.Request) (*activeUpload, error) {
	headerVal := r.Header.Get(a.Config.UploadIdentifierHeader)
	uploadID, err := strconv.ParseInt(headerVal, 10, 64)
	if err != nil {
		return nil, err
	}
	f, exists := a.data.uploads.Load(uploadID)
	if !exists {
		return nil, fmt.Errorf("upload ID not found")
	}
	return f.(*activeUpload), nil
}

func (a *FileChunksAssembler) getChunkID(r *http.Request) (int64, error) {
	headerVal := r.Header.Get(a.Config.ChunkIdentifierHeader)
	chunkSequenceID, err := strconv.ParseInt(headerVal, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("must be an integer")
	}
	if chunkSequenceID < 0 {
		return 0, fmt.Errorf("cannot be negative")
	}
	return chunkSequenceID, nil
}

func (a *FileChunksAssembler) UploadStartHandler(w http.ResponseWriter, r *http.Request) {
	var info fileInfo
	if err := json.NewDecoder(r.Body).Decode(&info); err != nil {
		badRequest(w, err)
		return
	}
	if info.TotalChunks == 0 {
		badRequest(w, fmt.Errorf("invalid number of expected chunks"))
		return
	}
	uploadID := a.data.createUpload(info)
	json.NewEncoder(w).Encode(map[string]int64{
		"id": uploadID,
	})
}

// Middleware wraps an endpoint that expects a single file. It will collect
// chunks in files until it has determined all chunks have been received.
// For requests that don't have the correct headers, HTTP 400 is returned.
// In downstream handlers, the request body becomes the complete file and
// response cannot be written to (nil).
func (a *FileChunksAssembler) ChunksMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		currentUpload, err := a.getActiveUpload(r)
		if err != nil {
			badRequest(w, err)
			return
		}
		// For each file being uploaded, only one chunk can be processed at a time.
		currentUpload.lock.Lock()
		defer currentUpload.lock.Unlock()

		chunkSequenceID, err := a.getChunkID(r)
		if err != nil {
			badRequest(w, err)
			return
		}
		if chunkSequenceID >= currentUpload.info.TotalChunks {
			badRequest(w, fmt.Errorf("invalid chunk ID"))
			return
		}
		chunkData, err := ioutil.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if len(chunkData) == 0 {
			badRequest(w, fmt.Errorf("chunk cannot be empty"))
			return
		}
		if err := a.data.addChunk(currentUpload, chunkSequenceID, chunkData); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		response := progressResponse{
			CurrentChunks:  currentUpload.countChunks(),
			ExpectedChunks: currentUpload.totalChunks(),
		}
		if currentUpload.countChunks() == currentUpload.totalChunks() {
			completedFilePath, err := a.data.combineChunks(currentUpload)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			contentType := currentUpload.info.Metadata["type"]
			if contentType == nil {
				contentType = "application/octet-stream"
			}
			r.Header.Set("Content-Type", contentType.(string))

			contentLength, err := getFileSize(completedFilePath)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			r.Header.Set("Content-Length", strconv.FormatInt(contentLength, 10))

			// Remove chunk-specific headers from request.
			r.Header.Del(a.Config.UploadIdentifierHeader)
			r.Header.Del(a.Config.ChunkIdentifierHeader)

			// Add the file stream as request body.
			f, err := os.Open(completedFilePath)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			defer func() { _ = f.Close() }()
			r.Body = f

			ctx := context.WithValue(
				r.Context(),
				contextKey("metadata"),
				currentUpload.info.Metadata,
			)
			// Cannot send a response downstream as it's used for the final progress update.
			req := *r.WithContext(ctx)
			h.ServeHTTP(nil, &req)

			rejectedFileCode := req.Context().Value(contextKey("error-code"))
			if rejectedFileCode != nil {
				rejectedFileErr := req.Context().Value(contextKey("error-message")).(string)
				response.RejectedError = &rejectedFileErr
				w.WriteHeader(rejectedFileCode.(int))
			}
		}
		w.Header().Add("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})
}
