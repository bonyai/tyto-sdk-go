// Command snapshots captures a running sandbox's state.
//
//	export BONYA_API_KEY=byk_...
//	go run ./examples/snapshots
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

	sandbox, err := client.CreateSandbox(ctx, "ubuntu-24.04", tyto.CreateOptions{Name: "snapshot-source"})
	if err != nil {
		log.Fatal(err)
	}
	defer sandbox.Delete(ctx)

	if err := sandbox.Files.Write(ctx, "/workspace/state.txt", []byte("captured\n")); err != nil {
		log.Fatal(err)
	}

	// Snapshot create requires a running source. Suspended, failed, and
	// deleted sandboxes each return their own error rather than a generic one.
	//
	// Passing an IdempotencyKey makes a retry return the same snapshot instead
	// of minting a second one.
	snapshot, err := client.CreateSnapshot(ctx, sandbox.ID, tyto.SnapshotOptions{IdempotencyKey: "example-snapshot-1"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("snapshot %s from %s\n", snapshot.ID, snapshot.SourceSandboxID)

	// Snapshot identities outlive their source sandbox, so deleting the
	// snapshot is a separate decision from deleting what it came from.
	if err := snapshot.Delete(ctx); err != nil {
		log.Fatal(err)
	}
	if err := snapshot.Delete(ctx); err != nil { // idempotent: a local no-op
		log.Fatal(err)
	}
}
