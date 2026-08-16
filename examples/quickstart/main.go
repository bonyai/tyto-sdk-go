// Command quickstart creates a sandbox, runs a command in it, and cleans up.
//
//	export BONYA_API_KEY=byk_...
//	go run ./examples/quickstart
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
	// Go has no context-manager protocol, so cleanup is an explicit defer.
	// Drop this for a sandbox meant to outlive the program.
	defer sandbox.Delete(ctx)

	fmt.Printf("created %s (%s)\n", sandbox.Name, sandbox.ID)

	result, err := sandbox.Exec(ctx, []string{"echo", "hello from bonya"}, tyto.ExecOptions{Check: true})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(result.Stdout())
}
