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

	sandbox, err := client.CreateSandbox(ctx, "ubuntu-24.04")
	if err != nil {
		log.Fatal(err)
	}
	defer sandbox.Delete(ctx)

	files := sandbox.Files

	if err := files.Write(ctx, "/workspace/greeting.txt", []byte("hello\n")); err != nil {
		log.Fatal(err)
	}
	data, err := files.Read(ctx, "/workspace/greeting.txt")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(data))

	if err := files.Mkdir(ctx, "/workspace/output"); err != nil {
		log.Fatal(err)
	}
	if err := files.Move(ctx, "/workspace/greeting.txt", "/workspace/output/greeting.txt"); err != nil {
		log.Fatal(err)
	}

	entries, err := files.List(ctx, "/workspace/output")
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

	info, err := files.Stat(ctx, "/workspace/output/greeting.txt")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("mode %04o, modified %s\n", info.Mode&0o7777, info.ModifiedAt)

	// Upload and Download stream in chunks, so file size is bounded by disk
	// rather than memory. Read buffers, capped by WithFilesystemReadLimit.
	if err := files.Upload(ctx, "main.go", "/workspace/output/example.go"); err != nil {
		log.Fatal(err)
	}
	if err := files.Download(ctx, "/workspace/output/example.go", "/tmp/roundtrip.go"); err != nil {
		log.Fatal(err)
	}

	if err := files.Remove(ctx, "/workspace/output", true); err != nil {
		log.Fatal(err)
	}
}
