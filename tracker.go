package assemble

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"sync"
)

type file struct {
	chunkSet      map[int64]interface{}
	expectedTotal int64
	lock          sync.Mutex
}

var ErrChunkQuantityChange = errors.New("cannot change expected number of chunks")

func (a *FileChunksAssembler) getFile(fileID string) *file {
	f, exists := a.data.Load(fileID)
	if !exists {
		return nil
	}
	return f.(*file)
}

func (a *FileChunksAssembler) getFileOrAdd(fileID string, chunkID int64, total int64) *file {
	f := a.getFile(fileID)
	if f == nil {
		f = &file{
			chunkSet:      make(map[int64]interface{}),
			expectedTotal: total,
		}
		a.data.Store(fileID, f)
	}
	return f
}

func (a *FileChunksAssembler) add(fileID string, chunkID int64, data []byte) error {
	chunkFilePath := path.Join(a.Config.ChunksDir, fmt.Sprintf("%s-%d", fileID, chunkID))
	if err := ioutil.WriteFile(chunkFilePath, data, 0644); err != nil {
		return err
	}
	a.getFile(fileID).chunkSet[chunkID] = nil
	return nil
}

func (a *FileChunksAssembler) delete(fileID string, chunkID int64) error {
	chunkFilePath := path.Join(a.Config.ChunksDir, fmt.Sprintf("%s-%d", fileID, chunkID))
	if err := os.Remove(chunkFilePath); err != nil {
		return err
	}
	f := a.getFile(fileID)
	delete(f.chunkSet, chunkID)
	if len(f.chunkSet) == 0 {
		a.data.Delete(fileID)
	}
	return nil
}

func (a *FileChunksAssembler) isComplete(fileID string) bool {
	return a.countChunks(fileID) == a.totalChunks(fileID)
}

func (a *FileChunksAssembler) countChunks(fileID string) int64 {
	f := a.getFile(fileID)
	return int64(len(f.chunkSet))
}

func (a *FileChunksAssembler) totalChunks(fileID string) int64 {
	f := a.getFile(fileID)
	return f.expectedTotal
}

func (a *FileChunksAssembler) combineChunks(fileID string) (string, error) {
	if !a.isComplete(fileID) {
		return "", nil
	}
	completedFilePath := path.Join(a.Config.CompletedDir, fileID)
	f, err := os.Create(completedFilePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	totalChunks := a.totalChunks(fileID)
	for i := int64(0); i < totalChunks; i++ {
		chunkFilePath := path.Join(a.Config.ChunksDir, fmt.Sprintf("%s-%d", fileID, i))
		chunk, err := os.ReadFile(chunkFilePath)
		if err != nil {
			return "", err
		}
		if _, err := f.Write(chunk); err != nil {
			return "", err
		}
	}
	if !a.Config.KeepCompletedChunks {
		go func() {
			for i := int64(0); i < totalChunks; i++ {
				_ = a.delete(fileID, i)
			}
		}()
	}
	return completedFilePath, nil
}
