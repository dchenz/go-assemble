package assemble

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
)

type progressResponse struct {
	CurrentChunks  int64   `json:"have"`
	ExpectedChunks int64   `json:"want"`
	RejectedError  *string `json:"error,omitempty"`
}
type errorResponse struct {
	Error string `json:"error"`
}

func badRequest(w http.ResponseWriter, message string) {
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(errorResponse{
		Error: message,
	})
}

type contextKey string

// GetFileID returns the ID of the assembled upload. If the request is
// not wrapped by go-assemble and has no file ID, this method will panic.
func GetFileID(r *http.Request) string {
	fileID := r.Context().Value(contextKey("id"))
	if fileID == nil {
		panic("GetFileID can only be used inside handlers wrapped by go-assemble")
	}
	return fileID.(string)
}

func RejectFile(r *http.Request, status int, reason string) {
	ctx := r.Context()
	ctx = context.WithValue(ctx, contextKey("error-code"), status)
	ctx = context.WithValue(ctx, contextKey("error-message"), reason)
	*r = *r.WithContext(ctx)
}

func containsInvalidCharacters(s string) bool {
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z') &&
			!(c >= 'a' && c <= 'z') &&
			!(c >= '0' && c <= '9') &&
			c != '_' && c != '-' {
			return true
		}
	}
	return false
}

func getFileSize(path string) (int64, error) {
	f, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return f.Size(), nil
}
