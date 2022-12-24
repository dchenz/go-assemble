# go-assemble

go-assemble is a middleware abstraction for chunked file uploads and provides its downstream handlers with one file. It is common practice to break a large file into smaller 'chunks' and upload each chunk in a separate HTTP request. The server has to keep track of this and handle the file's reconstruction.

## Installation

```sh
go get github.com/dchenz/go-assemble
```

## Usage

```go
router := mux.NewRouter()

h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {

    // Get file ID.
    fileID := assemble.GetFileID(r)

    // Treat it as if it were uploaded as one file in body...
    //
    // NOTE:
    // http.ResponseWriter will be nil. The response is used
    // to send the final progress update.
    UploadToObjectStorage(fileID, r.Body)

    // Size of uploaded file.
    fmt.Println(r.Header.Get("Content-Length"))

    // Mimetype of uploaded file. This should be set on the final
    // chunk request in x-assemble-content-type, otherwise it will
    // default to application/octet-stream.
    fmt.Println(r.Header.Get("Content-Type"))
})

// Use default configuration.
fileAssembler := assemble.NewFileChunksAssembler(nil)

// Should only be used on the route handler that needs it.
router.Handle("/api/upload", fileAssembler.Middleware(h)).Methods("POST")
```

The completed file can be rejected in the downstream handler. Rejection adds an error to the final progress update (see example below) and sets the status code.

```go
h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
    if mimetypeNotSupported(r) {
        assemble.RejectFile(r, http.StatusBadRequest, "unsupported mimetype")
        return
    }
})
```

The HTTP response contains a progress update with the number of successful chunks received so far. **On completion (have == want), the response must be checked for errors in case the completed file was rejected by the server.**

```js
// Uploaded with no errors.
{
    "have": 10,
    "want": 10
}

// Uploaded but rejected by server.
{
    "have": 10,
    "want": 10,
    "error": "unsupported mimetype"
}
```

In the client code, it may look something like this:

```js
function onFileSelect(e) {
  const file = e.target.files[0];
  if (file) {
    const fileID = Date.now(); // Use a more reliable ID generator
    const chunkSize = 2 ** 24; // 16MB
    const totalChunks = Math.ceil(file.size / chunkSize);
    for (let i = 0; i < totalChunks; i++) {
      const chunkBlob = file.slice(i * chunkSize, (i + 1) * chunkSize);
      fetch("http://localhost:5000/api/upload", {
        method: "POST",
        headers: {
          // Other headers can go here too, such as Authorization.
          "x-assemble-file-id": fileID,
          "x-assemble-chunk-sequence": i,
          "x-assemble-chunk-total": totalChunks,
          "x-assemble-content-type": file.type,
        },
        body: chunkBlob,
      })
        .then((resp) => resp.json())
        .then((resp) => {
          if (resp.error) {
            console.error(resp.error);
          } else if (resp.have === resp.want) {
            // Successful upload!
          } else {
            // Not guaranteed to be in ascending order.
            console.log(`progress ${resp.have}/${resp.want}`);
          }
        })
        .catch(console.error);
    }
  }
}
```

If a chunk upload has invalid headers or is missing required headers, an error message is returned with HTTP 400.

```js
{
    "error": "file ID is required"
}

{
    "error": "file ID only supports alphanumeric, underscores and hyphens"
}
```

## Configuration

```go
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
```

If ``ChunksDir`` or ``CompletedDir`` aren't provided, it will create and use the default directories. If they are provided, it does not check if the directories exist and will raise an error if accessed.