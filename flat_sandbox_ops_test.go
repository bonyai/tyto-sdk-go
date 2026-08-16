package tyto

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	runtimev1 "github.com/bonyai/tyto-go/internal/gen/tyto/runtime/v1"
)

// fakeGuestSessions is a minimal in-process GuestService implementing only
// CreateSession, for testing the flat session methods' resolve-then-delegate
// wiring without exercising the streaming Exec/Attach machinery.
type fakeGuestSessions struct {
	runtimev1.UnimplementedGuestServiceServer

	createSessionCalls int
}

func (f *fakeGuestSessions) CreateSession(ctx context.Context, req *runtimev1.CreateSessionRequest) (*runtimev1.CreateSessionResponse, error) {
	f.createSessionCalls++
	return &runtimev1.CreateSessionResponse{
		Session: &runtimev1.SessionInfo{
			Name:    req.GetName(),
			Command: req.GetCommand(),
			Status:  runtimev1.SessionStatus_SESSION_STATUS_STARTING,
		},
	}, nil
}

// newBufconnClientWithGuestSessions starts a single in-process gRPC server
// hosting both TApiService (fake) and GuestService (guest) on the same
// bufconn listener, and points the fake's GetSandbox response at that same
// address -- so a flat session method can resolve the sandbox over TApi and
// then reach the guest over the same connection, the way a real deployment
// reaches both through one endpoint.
func newBufconnClientWithGuestSessions(t *testing.T, fake *fakeTApi, guest *fakeGuestSessions) *Client {
	t.Helper()
	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	runtimev1.RegisterTApiServiceServer(server, fake)
	runtimev1.RegisterGuestServiceServer(server, guest)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }

	client, err := NewClient(
		WithAPIKey("byk_test_key"),
		WithEndpoint("https://bufconn-combined.test"),
		WithMaxRetries(2),
		WithTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	fake.execEndpoint = "https://bufconn-combined.test"

	interceptor := orgContextInterceptor{currentOrganizationID: client.OrganizationID}
	conn, err := grpc.NewClient(
		"passthrough:///bufnet-combined",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(interceptor.unary()),
		grpc.WithChainStreamInterceptor(interceptor.stream()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client.mu.Lock()
	client.channels[client.endpoint.url] = conn
	client.mu.Unlock()

	return client
}

// TestFlatCreateSessionResolvesTheHandleThenDelegates proves CreateSession
// reaches the guest's CreateSession RPC for the sandbox named by id, going
// through GetSandbox first the same way the snapshot and preview flat
// methods do.
func TestFlatCreateSessionResolvesTheHandleThenDelegates(t *testing.T) {
	fake := &fakeTApi{}
	guest := &fakeGuestSessions{}
	client := newBufconnClientWithGuestSessions(t, fake, guest)
	ctx := context.Background()

	info, err := client.CreateSession(ctx, "sbx-1", "dev", []string{"bash"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if info.Name != "dev" {
		t.Fatalf("Name = %q, want %q", info.Name, "dev")
	}
	if fake.getCalls != 1 {
		t.Fatalf("getCalls = %d, want 1 (CreateSession must resolve the handle first)", fake.getCalls)
	}
	if guest.createSessionCalls != 1 {
		t.Fatalf("createSessionCalls = %d, want 1", guest.createSessionCalls)
	}
}

// TestFlatSnapshotMethodsResolveTheHandleThenDelegate proves the flat
// snapshot methods reach the same RPCs the handle-based methods do: each
// resolves the sandbox via GetSandbox first, since CreateSnapshot checks
// the handle's observed status before calling through.
func TestFlatSnapshotMethodsResolveTheHandleThenDelegate(t *testing.T) {
	fake := &fakeTApi{}
	client := newBufconnClient(t, fake)
	ctx := context.Background()

	snapshot, err := client.CreateSnapshot(ctx, "sbx-1")
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if snapshot.SourceSandboxID != "sbx-1" {
		t.Fatalf("SourceSandboxID = %q, want %q", snapshot.SourceSandboxID, "sbx-1")
	}
	if fake.createSnapshotCalls != 1 {
		t.Fatalf("createSnapshotCalls = %d, want 1", fake.createSnapshotCalls)
	}
	if fake.getCalls != 1 {
		t.Fatalf("getCalls = %d, want 1 (CreateSnapshot must resolve the handle first)", fake.getCalls)
	}

	if err := client.DeleteSnapshot(ctx, "sbx-1", snapshot.ID); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}
	if fake.deleteSnapshotCalls != 1 {
		t.Fatalf("deleteSnapshotCalls = %d, want 1", fake.deleteSnapshotCalls)
	}
}

// TestFlatPreviewMethodsResolveTheHandleThenDelegate mirrors the snapshot
// test for the preview surface.
func TestFlatPreviewMethodsResolveTheHandleThenDelegate(t *testing.T) {
	fake := &fakeTApi{}
	client := newBufconnClient(t, fake)
	ctx := context.Background()

	preview, err := client.CreatePreview(ctx, "sbx-1", 8080)
	if err != nil {
		t.Fatalf("CreatePreview: %v", err)
	}
	if preview.SandboxID != "sbx-1" {
		t.Fatalf("SandboxID = %q, want %q", preview.SandboxID, "sbx-1")
	}
	if fake.createPreviewCalls != 1 {
		t.Fatalf("createPreviewCalls = %d, want 1", fake.createPreviewCalls)
	}

	previews, err := client.ListPreviews(ctx, "sbx-1")
	if err != nil {
		t.Fatalf("ListPreviews: %v", err)
	}
	if len(previews) != 1 {
		t.Fatalf("ListPreviews = %+v, want one preview", previews)
	}
	if fake.listPreviewCalls != 1 {
		t.Fatalf("listPreviewCalls = %d, want 1", fake.listPreviewCalls)
	}

	if err := client.DeletePreview(ctx, "sbx-1", preview.ID); err != nil {
		t.Fatalf("DeletePreview: %v", err)
	}
	if fake.deletePreviewCalls != 1 {
		t.Fatalf("deletePreviewCalls = %d, want 1", fake.deletePreviewCalls)
	}
}

// TestFlatSandboxOpsPropagateGetSandboxFailure guards the shared
// resolve-then-delegate shape: if GetSandbox fails, the flat method must
// return that error rather than attempting the follow-up call.
func TestFlatSandboxOpsPropagateGetSandboxFailure(t *testing.T) {
	fake := &fakeTApi{failuresRemaining: 1, failWithCode: 5} // codes.NotFound
	client := newBufconnClient(t, fake)
	ctx := context.Background()

	if _, err := client.CreateSnapshot(ctx, "sbx-missing"); err == nil {
		t.Fatal("CreateSnapshot with a failing GetSandbox = nil error, want an error")
	}
	if fake.createSnapshotCalls != 0 {
		t.Fatalf("createSnapshotCalls = %d, want 0 (must not call through after GetSandbox fails)", fake.createSnapshotCalls)
	}
}
