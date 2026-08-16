// Command exec-streaming streams a command's output as it is produced.
//
// Use this over Exec when output is large, the command is long-running, or you
// want to react to output before the process finishes.
//
//	export BONYA_API_KEY=byk_...
//	go run ./examples/exec-streaming
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

	// Events arrive as they happen: one line per second, rather than three
	// lines after three seconds.
	session, err := sandbox.ExecStream(ctx, []string{"bash", "-c", "for i in 1 2 3; do echo line $i; sleep 1; done"})
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	for {
		event, err := session.Next()
		if err != nil {
			log.Fatal(err)
		}
		if event == nil {
			break // stream ended cleanly after Exit
		}
		switch e := event.(type) {
		case tyto.Stdout:
			fmt.Print(string(e.Data))
		case tyto.Stderr:
			fmt.Print(string(e.Data))
		case tyto.Exit:
			fmt.Printf("exited with %d\n", e.ExitCode)
		}
	}

	// Streaming stdin: write, then half-close so the process sees EOF.
	piped, err := sandbox.ExecStream(ctx, []string{"cat"})
	if err != nil {
		log.Fatal(err)
	}
	defer piped.Close()

	if err := piped.Write([]byte("piped through cat\n")); err != nil {
		log.Fatal(err)
	}
	if err := piped.CloseStdin(); err != nil {
		log.Fatal(err)
	}

	for {
		event, err := piped.Next()
		if err != nil || event == nil {
			break
		}
		if out, ok := event.(tyto.Stdout); ok {
			fmt.Print(string(out.Data))
		}
	}
}
