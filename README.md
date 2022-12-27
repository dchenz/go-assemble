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

    // Get file metadata. This is an object sent in the initial request.
    fmt.Println("File metadata:", assemble.GetFileMetadata(r))

    // Size of uploaded file.
    fmt.Println("File size:", r.Header.Get("Content-Length"))

    // Mimetype of uploaded file. This should be set in the initial
    // request, otherwise it defaults to application/octet-stream.
    fmt.Println("File type:", r.Header.Get("Content-Type"))

    // Treat it as if it were uploaded as one file in body...
    UploadToObjectStorage(fileID, r.Body)

    // NOTE:
    // http.ResponseWriter will be nil. The response is used
    // to send the final progress update.
})

// Use default configuration.
fileAssembler, err := assemble.NewFileChunksAssembler(nil)
if err != nil {
    panic(err)
}

// Two routes are required. One receives an "initiator request" and the other
// receives chunked file parts to be re-assembled. More details below.
router.Handle("/api/upload/init", http.HandlerFunc(fileAssembler.UploadStartHandler)).Methods("POST")
router.Handle("/api/upload/parts", fileAssembler.ChunksMiddleware(h)).Methods("POST")
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

Before sending file chunks, an upload must be started by sending a request to the designated endpoint. The request body should contain an object like below which tells the server how many chunks to expect and other metadata. Metadata is optional, however ``"type"`` should be set to the correct mimetype.

```js
{
    "total_chunks": 10,
    "metadata": {
        "type": "video/mp4",
        "name": "test.mp4"
    }
}

```

The above request will respond with an upload ID and the client can start sending file chunks. This ID must be set in the headers along with a chunk sequence number from 0 to ``total_chunks``.

```js
{
    "id": 123
}
```

In the client code, it may look something like this:

```js
async function onFileSelect(e) {
  const file = e.target.files[0];
  if (file) {
    const chunkSize = 2 ** 24; // 16MB
    const totalChunks = Math.ceil(file.size / chunkSize);

    const uploadInitResponse = await (
      await fetch("http://localhost:5000/api/upload/init", {
        method: "POST",
        headers: {
          // Other headers can go here too, such as Authorization.
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          total_chunks: totalChunks,
          metadata: {
            name: file.name,
            type: file.type,
          },
        }),
      })
    ).json();

    for (let i = 0; i < totalChunks; i++) {
      const chunkBlob = file.slice(i * chunkSize, (i + 1) * chunkSize);

      const chunkResponse = await (
        await fetch("http://localhost:5000/api/upload/parts", {
          method: "POST",
          headers: {
            // Other headers can go here too, such as Authorization.
            "x-assemble-upload-id": uploadInitResponse.id,
            "x-assemble-chunk-id": i,
          },
          body: chunkBlob,
        })
      ).json();

      if (chunkResponse.error) {
        console.error(chunkResponse.error);
      } else if (chunkResponse.have === chunkResponse.want) {
        // Successful upload!
      } else {
        // Not guaranteed to be in ascending order.
        console.log(`Progress: ${chunkResponse.have}/${chunkResponse.want}`);
      }
    }
  }
}
```

If a chunk upload has invalid headers or is missing required headers, an error message is returned with HTTP 400.

```js
{
    "error": "invalid chunk ID"
}

{
    "error": "chunk cannot be empty"
}
```

## Configuration

```go
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
```

If ``ChunksDir`` or ``CompletedDir`` aren't provided, it will create and use the default directories. If they are provided, it does not check if the directories exist and will raise an error if accessed.