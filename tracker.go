package assemble

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"sync"
)

type fileInfo struct {
	TotalChunks int64                  `json:"total_chunks"`
	Metadata    map[string]interface{} `json:"metadata"`
}

type activeUpload struct {
	id     int64
	info   fileInfo
	chunks map[int64]interface{}
	lock   sync.Mutex
}

type tracker struct {
	uploads      sync.Map
	nextID       int64
	lock         sync.Mutex
	chunkDir     string
	completedDir string
}

func (a *tracker) createUpload(info fileInfo) int64 {
	a.lock.Lock()
	defer a.lock.Unlock()
	id := a.nextID
	a.uploads.Store(id, &activeUpload{
		id:     id,
		info:   info,
		chunks: make(map[int64]interface{}),
	})
	a.nextID++
	return id
}

func (a *tracker) addChunk(f *activeUpload, chunkID int64, chunkData []byte) error {
	chunkFilePath := a.chunkFilePath(f.id, chunkID)
	if err := ioutil.WriteFile(chunkFilePath, chunkData, 0644); err != nil {
		return err
	}
	f.chunks[chunkID] = nil
	return nil
}

func (a *tracker) deleteChunk(f *activeUpload, chunkID int64) error {
	if err := os.Remove(a.chunkFilePath(f.id, chunkID)); err != nil {
		return err
	}
	delete(f.chunks, chunkID)
	return nil
}

func (f *activeUpload) countChunks() int64 {
	return int64(len(f.chunks))
}

func (f *activeUpload) totalChunks() int64 {
	return f.info.TotalChunks
}

func (a *tracker) combineChunks(f *activeUpload) (string, error) {
	if f.countChunks() < f.totalChunks() {
		return "", nil
	}
	completedFilePath := a.completedFilePath(f.id)
	finalFile, err := os.Create(completedFilePath)
	if err != nil {
		return "", err
	}
	defer finalFile.Close()
	totalChunks := f.totalChunks()
	for i := int64(0); i < totalChunks; i++ {
		chunk, err := os.ReadFile(a.chunkFilePath(f.id, i))
		if err != nil {
			return "", err
		}
		if _, err := finalFile.Write(chunk); err != nil {
			return "", err
		}
	}
	go func() {
		for i := int64(0); i < totalChunks; i++ {
			_ = a.deleteChunk(f, i)
			if len(f.chunks) == 0 {
				a.uploads.Delete(f.id)
			}
		}
	}()
	return completedFilePath, nil
}

func (a *tracker) chunkFilePath(uploadID int64, chunkID int64) string {
	return path.Join(a.chunkDir, fmt.Sprintf("%d-%d", uploadID, chunkID))
}

func (a *tracker) completedFilePath(uploadID int64) string {
	return path.Join(a.completedDir, fmt.Sprintf("%d", uploadID))
}
