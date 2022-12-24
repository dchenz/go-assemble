package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/dchenz/go-assemble"
	"github.com/gorilla/mux"
)

func main() {
	router := mux.NewRouter()

	// Use default configuration.
	fileAssembler, err := assemble.NewFileChunksAssembler(nil)
	if err != nil {
		panic(err)
	}

	// Should only be used on the route handler that needs it.
	router.Handle("/api/upload",
		fileAssembler.Middleware(http.HandlerFunc(fileHandler)),
	).Methods("POST")

	router.Handle("/", http.HandlerFunc(serveIndex)).Methods("GET")

	server := http.Server{
		Handler:           router,
		Addr:              "localhost:5000",
		ReadHeaderTimeout: 3 * time.Second,
	}
	fmt.Println("server is running on localhost:5000")
	if err := server.ListenAndServe(); err != nil {
		panic(err)
	}
}

func fileHandler(_ http.ResponseWriter, r *http.Request) {
	// Get file ID.
	fmt.Println("File ID:", assemble.GetFileID(r))

	// Size of uploaded file.
	fmt.Println("File size:", r.Header.Get("Content-Length"))

	// Mimetype of uploaded file. This should be set on the final
	// chunk request in x-assemble-content-type, otherwise it will
	// default to application/octet-stream.
	fmt.Println("File type:", r.Header.Get("Content-Type"))

	// Access file data.
	// ioutil.ReadAll(r.Body)
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "./index.html")
}
