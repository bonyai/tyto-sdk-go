package tyto

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	runtimev1grpc "buf.build/gen/go/bonya/tyto/grpc/go/tyto/runtime/v1/runtimev1grpc"
	runtimev1 "buf.build/gen/go/bonya/tyto/protocolbuffers/go/tyto/runtime/v1"
)

// fakeGuest is a minimal in-process GuestService that echoes an Exec
// session's stdin back as stdout, then exits 0 once stdin is closed. It
// exists to exercise ExecSession's request/response streaming machinery
// (Write/CloseStdin/Next) against a real bidi gRPC stream, not a mock.
type fakeGuest struct {
	runtimev1grpc.UnimplementedGuestServiceServer
	attachDelay time.Duration
}

func (f *fakeGuest) Exec(stream runtimev1grpc.GuestService_ExecServer) error {
	var buffered []byte
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			// Client half-closed (CloseSend): send accumulated stdout then exit.
			if len(buffered) > 0 {
				if err := stream.Send(&runtimev1.ExecResponse{Frame: &runtimev1.ExecResponse_Stdout{Stdout: &runtimev1.StdoutData{Data: buffered}}}); err != nil {
					return err
				}
			}
			return stream.Send(&runtimev1.ExecResponse{Frame: &runtimev1.ExecResponse_Exit{Exit: &runtimev1.ExecExit{ExitCode: 0}}})
		}
		if err != nil {
			return nil
		}
		switch frame := req.GetFrame().(type) {
		case *runtimev1.ExecRequest_Start:
			// no-op: session accepted implicitly by continuing to read
		case *runtimev1.ExecRequest_Stdin:
			buffered = append(buffered, frame.Stdin.GetData()...)
		case *runtimev1.ExecRequest_Cancel:
			return nil
		}
	}
}

func (f *fakeGuest) AttachSession(stream runtimev1grpc.GuestService_AttachSessionServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&runtimev1.AttachSessionResponse{
		Frame: &runtimev1.AttachSessionResponse_Accepted{Accepted: &runtimev1.AttachAccepted{
			Session: &runtimev1.SessionInfo{Name: "console", Status: runtimev1.SessionStatus_SESSION_STATUS_ATTACHED},
		}},
	}); err != nil {
		return err
	}
	if f.attachDelay > 0 {
		select {
		case <-time.After(f.attachDelay):
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
	return stream.Send(&runtimev1.AttachSessionResponse{
		Frame: &runtimev1.AttachSessionResponse_Ended{Ended: &runtimev1.AttachEnded{
			Reason: runtimev1.AttachEnded_REASON_DETACHED,
		}},
	})
}

func newBufconnSandbox(t *testing.T, guest *fakeGuest) *Sandbox {
	t.Helper()
	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	runtimev1grpc.RegisterGuestServiceServer(server, guest)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient(
		"passthrough:///bufnet-guest",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn guest: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client, err := NewClient(WithAPIKey("byk_test"), WithEndpoint("https://bufconn-guest.test"), WithTimeout(5*time.Second))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	execEndpoint, err := normalizeEndpoint("https://guest-exec.test")
	if err != nil {
		t.Fatalf("normalizeEndpoint: %v", err)
	}
	client.mu.Lock()
	client.channels[execEndpoint.url] = conn
	client.mu.Unlock()

	return newSandbox(client, sandboxCreateArgs{
		sandboxID:    "sbx-1",
		operationID:  "op-1",
		template:     "ubuntu-24.04",
		status:       StatusRunning,
		execEndpoint: "https://guest-exec.test",
		capability:   "fake.capability.jws",
	})
}

func TestExecStreamEchoesStdinToStdout(t *testing.T) {
	sandbox := newBufconnSandbox(t, &fakeGuest{})

	session, err := sandbox.ExecStream(context.Background(), []string{"cat"})
	if err != nil {
		t.Fatalf("ExecStream: %v", err)
	}
	defer session.Close()

	if err := session.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := session.CloseStdin(); err != nil {
		t.Fatalf("CloseStdin: %v", err)
	}

	var stdout []byte
	var sawExit bool
	for {
		event, err := session.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if event == nil {
			break
		}
		switch e := event.(type) {
		case Stdout:
			stdout = append(stdout, e.Data...)
		case Exit:
			if e.ExitCode != 0 {
				t.Errorf("Exit.ExitCode = %d, want 0", e.ExitCode)
			}
			sawExit = true
		}
	}
	if !sawExit {
		t.Error("did not observe an Exit event")
	}
	if string(stdout) != "hello\n" {
		t.Errorf("stdout = %q, want %q", stdout, "hello\n")
	}
}

func TestSandboxExecBuffered(t *testing.T) {
	sandbox := newBufconnSandbox(t, &fakeGuest{})

	result, err := sandbox.Exec(context.Background(), []string{"cat"}, ExecOptions{Input: []byte("buffered input")})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.Stdout() != "buffered input" {
		t.Errorf("Stdout() = %q, want %q", result.Stdout(), "buffered input")
	}
	if !result.OK() {
		t.Errorf("OK() = false, want true (exit code %d)", result.ExitCode)
	}
}

func TestSessionAttachOutlivesClientOperationTimeout(t *testing.T) {
	sandbox := newBufconnSandbox(t, &fakeGuest{attachDelay: 60 * time.Millisecond})
	sandbox.client.timeout = 20 * time.Millisecond

	stream, err := sandbox.Sessions.Attach(context.Background(), "console")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer stream.Close()

	started := time.Now()
	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 40*time.Millisecond {
		t.Fatalf("Next returned after %s, want it to outlive the 20ms client timeout", elapsed)
	}
	if _, ok := event.(SessionEnded); !ok {
		t.Fatalf("event = %#v, want SessionEnded", event)
	}
}
