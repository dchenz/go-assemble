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

func badRequest(w http.ResponseWriter, err error) {
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(errorResponse{
		Error: err.Error(),
	})
}

type contextKey string

func GetFileMetadata(r *http.Request) map[string]interface{} {
	m := r.Context().Value(contextKey("metadata"))
	return m.(map[string]interface{})
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
