package assemble

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"
)

type file struct {
	chunkSet      map[int64]interface{}
	expectedTotal int64
}

var ErrChunkQuantityChange = errors.New("cannot change expected number of chunks")

func (a *FileChunksAssembler) addFileIfNotExists(fileID string, chunkID int64, total int64) error {
	if !a.exists(fileID) {
		a.data[fileID] = &file{
			chunkSet:      make(map[int64]interface{}),
			expectedTotal: total,
		}
	} else if a.data[fileID].expectedTotal != total {
		return ErrChunkQuantityChange
	}
	return nil
}

func (a *FileChunksAssembler) add(fileID string, chunkID int64, data []byte) error {
	chunkFilePath := path.Join(a.Config.ChunksDir, fmt.Sprintf("%s-%d", fileID, chunkID))
	if err := ioutil.WriteFile(chunkFilePath, data, 0644); err != nil {
		return err
	}
	a.data[fileID].chunkSet[chunkID] = nil
	return nil
}

func (a *FileChunksAssembler) delete(fileID string, chunkID int64) error {
	chunkFilePath := path.Join(a.Config.ChunksDir, fmt.Sprintf("%s-%d", fileID, chunkID))
	if err := os.Remove(chunkFilePath); err != nil {
		return err
	}
	f := a.data[fileID]
	delete(f.chunkSet, chunkID)
	if len(f.chunkSet) == 0 {
		delete(a.data, fileID)
	}
	return nil
}

func (a *FileChunksAssembler) isComplete(fileID string) bool {
	return a.countChunks(fileID) == a.totalChunks(fileID)
}

func (a *FileChunksAssembler) countChunks(fileID string) int64 {
	f := a.data[fileID]
	return int64(len(f.chunkSet))
}

func (a *FileChunksAssembler) totalChunks(fileID string) int64 {
	f := a.data[fileID]
	return f.expectedTotal
}

func (a *FileChunksAssembler) exists(fileID string) bool {
	_, exists := a.data[fileID]
	return exists
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
