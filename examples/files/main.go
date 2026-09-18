// Command files reads and writes files inside a sandbox.
//
//	export BONYA_API_KEY=byk_...
//	go run ./examples/files
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	tyto "github.com/bonyai/tyto-go"
)

func main() {
	apiKey := os.Getenv("BONYA_API_KEY")

	client, err := tyto.NewClient(tyto.WithAPIKey(apiKey))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx := context.Background()

	sandbox, err := client.CreateSandbox(ctx, "bonya-dev")
	if err != nil {
		log.Fatal(err)
	}
	defer sandbox.Delete(ctx)

	if err := sandbox.WriteFile(ctx, "/workspace/greeting.txt", []byte("hello\n")); err != nil {
		log.Fatal(err)
	}
	data, err := sandbox.ReadFile(ctx, "/workspace/greeting.txt")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(data))

	if err := sandbox.MkdirFile(ctx, "/workspace/output"); err != nil {
		log.Fatal(err)
	}
	if err := sandbox.MoveFile(ctx, "/workspace/greeting.txt", "/workspace/output/greeting.txt"); err != nil {
		log.Fatal(err)
	}

	entries, err := sandbox.ListFiles(ctx, "/workspace/output")
	if err != nil {
		log.Fatal(err)
	}
	for _, entry := range entries {
		kind := "file"
		if entry.Kind == tyto.FileKindDirectory {
			kind = "dir "
		}
		fmt.Printf("%s %s (%d bytes)\n", kind, entry.Name, entry.Size)
	}

	info, err := sandbox.StatFile(ctx, "/workspace/output/greeting.txt")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("mode %04o, modified %s\n", info.Mode&0o7777, info.ModifiedAt)

	// UploadFile and DownloadFile stream in chunks, so file size is bounded
	// by disk rather than memory. ReadFile buffers, capped by
	// WithFilesystemReadLimit.
	if err := sandbox.UploadFile(ctx, "main.go", "/workspace/output/example.go"); err != nil {
		log.Fatal(err)
	}
	if err := sandbox.DownloadFile(ctx, "/workspace/output/example.go", "/tmp/roundtrip.go"); err != nil {
		log.Fatal(err)
	}

	if err := sandbox.RemoveFile(ctx, "/workspace/output", true); err != nil {
		log.Fatal(err)
	}
}
