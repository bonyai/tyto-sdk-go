// Command sessions demonstrates managed console sessions: terminals that
// outlive your connection.
//
// A session keeps running after you detach, survives the sandbox suspending
// and resuming, and replays what it produced while nobody was attached. That
// is the difference from ExecStream, whose process dies when the stream
// closes.
//
//	export BONYA_API_KEY=byk_...
//	go run ./examples/sessions
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

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

	// Session names match ^[a-z][a-z0-9-]{0,31}$ and are the identity you
	// reattach with later.
	info, err := client.CreateSession(ctx, sandbox.ID, "worker",
		[]string{"bash", "-c", "for i in $(seq 1 10); do echo tick $i; sleep 1; done"},
		tyto.CreateSessionOptions{Cols: 120, Rows: 40})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("started %s: %s\n", info.Name, info.Status)

	// Let it produce output with nobody attached, so the attach below has
	// something to replay.
	time.Sleep(3 * time.Second)

	stream, err := client.AttachSession(ctx, sandbox.ID, "worker")
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()

	// Populated before the first Next call: they describe the bounded replay
	// buffer, not live output.
	fmt.Printf("replaying %d bytes\n", stream.ReplayedBytes)
	if stream.HistoryDropped {
		fmt.Println("(some older output was dropped)")
	}

	for {
		event, err := stream.Next()
		if err != nil {
			log.Fatal(err)
		}
		if event == nil {
			break
		}
		done := false
		switch e := event.(type) {
		case tyto.Stdout:
			fmt.Print(string(e.Data))
		case tyto.Exit:
			fmt.Printf("process exited with %d\n", e.ExitCode)
			done = true
		case tyto.SessionEnded:
			fmt.Printf("attach ended: %s\n", e.Reason)
			done = true
		case tyto.SessionOutputDropped:
			// Reading too slowly. The attach is still live.
			fmt.Printf("[dropped %d bytes]\n", e.DroppedBytes)
		}
		if done {
			break
		}
	}

	list, err := client.ListSessions(ctx, sandbox.ID)
	if err != nil {
		log.Fatal(err)
	}
	for _, session := range list.Sessions {
		fmt.Printf("%s: %s\n", session.Name, session.Status)
	}

	if _, err := client.KillSession(ctx, sandbox.ID, "worker"); err != nil {
		log.Fatal(err)
	}
}
