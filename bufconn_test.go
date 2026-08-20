package tyto

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	runtimev1grpc "buf.build/gen/go/bonya/tyto/grpc/go/tyto/runtime/v1/runtimev1grpc"
	runtimev1 "buf.build/gen/go/bonya/tyto/protocolbuffers/go/tyto/runtime/v1"
)

// fakeTApi is an in-process TApiService implementation for testing the SDK's
// gRPC client behavior (org-context header injection, retry/backoff)
// without a real network or server.
type fakeTApi struct {
	runtimev1grpc.UnimplementedTApiServiceServer

	mu sync.Mutex

	// lastMetadata captures the incoming metadata of the most recent Create call.
	lastMetadata metadata.MD

	// failCount, if >0, makes the next N Create/GetSandbox/ListSandboxes calls
	// return UNAVAILABLE before succeeding.
	failuresRemaining int
	failWithCode      codes.Code

	createCalls int
	getCalls    int
	listCalls   int

	// createdName echoes back from Create, standing in for the name the
	// service settles on when the request leaves it blank.
	createdName string
	// lastCreateName captures the name the most recent Create was asked for.
	lastCreateName string
	// lastListName captures the name filter of the most recent ListSandboxes.
	lastListName string
	// listSandboxes, when set, replaces the single-sandbox default response.
	listSandboxes []*runtimev1.TApiSandboxMetadata

	// listOrganizationsCalls counts ListOrganizations invocations.
	listOrganizationsCalls int
	// organizations, when set, replaces the default single-organization
	// response for ListOrganizations.
	organizations []*runtimev1.TApiOrganization

	// listTemplatesCalls counts ListTemplates invocations.
	listTemplatesCalls int
	// templates, when set, replaces the default single-template response
	// for ListTemplates.
	templates []*runtimev1.TApiTemplate

	// createSnapshotCalls, createPreviewCalls, listPreviewCalls, and
	// deletePreviewCalls count their respective invocations, for tests that
	// need to prove a flat method reached the sandbox-scoped RPC.
	createSnapshotCalls int
	deleteSnapshotCalls int
	createPreviewCalls  int
	listPreviewCalls    int
	deletePreviewCalls  int

	// execEndpoint, when set, overrides GetSandbox's default ExecEndpoint --
	// tests that also need a working guest connection point this at the
	// same bufconn address the client's control-plane channel is dialed to.
	execEndpoint string
}

func (f *fakeTApi) Create(ctx context.Context, req *runtimev1.TApiServiceCreateRequest) (*runtimev1.TApiServiceCreateResponse, error) {
	f.mu.Lock()
	f.createCalls++
	f.lastCreateName = req.GetName()
	createdName := f.createdName
	if createdName == "" {
		createdName = req.GetName()
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		f.lastMetadata = md
	} else {
		f.lastMetadata = nil
	}
	if f.failuresRemaining > 0 {
		f.failuresRemaining--
		code := f.failWithCode
		f.mu.Unlock()
		return nil, status.Error(code, "temporarily unavailable")
	}
	f.mu.Unlock()
	return &runtimev1.TApiServiceCreateResponse{
		OperationId:             "op-1",
		SandboxId:               "sbx-1",
		ExecCapabilityJws:       "fake.capability.jws",
		ExecEndpoint:            "https://100.89.203.43:9443",
		ResolvedTemplateId:      req.GetTemplate().GetTemplateId(),
		ResolvedTemplateVersion: "dev",
		Name:                    createdName,
	}, nil
}

func (f *fakeTApi) GetSandbox(ctx context.Context, req *runtimev1.TApiGetSandboxRequest) (*runtimev1.TApiGetSandboxResponse, error) {
	f.mu.Lock()
	f.getCalls++
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		f.lastMetadata = md
	} else {
		f.lastMetadata = nil
	}
	if f.failuresRemaining > 0 {
		f.failuresRemaining--
		code := f.failWithCode
		f.mu.Unlock()
		return nil, status.Error(code, "temporarily unavailable")
	}
	execEndpoint := f.execEndpoint
	if execEndpoint == "" {
		execEndpoint = "https://100.89.203.43:9443"
	}
	f.mu.Unlock()
	return &runtimev1.TApiGetSandboxResponse{
		Sandbox: &runtimev1.TApiSandboxMetadata{
			SandboxId:   req.GetSandboxId(),
			OperationId: "op-1",
			Observed:    &runtimev1.TerminalStatus{State: runtimev1.TerminalState_TERMINAL_STATE_RUNNING},
		},
		ExecEndpoint:      execEndpoint,
		ExecCapabilityJws: "fake.capability.jws",
	}, nil
}

func (f *fakeTApi) ListSandboxes(ctx context.Context, req *runtimev1.TApiListSandboxesRequest) (*runtimev1.TApiListSandboxesResponse, error) {
	f.mu.Lock()
	f.listCalls++
	f.lastListName = req.GetName()
	listed := f.listSandboxes
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		f.lastMetadata = md
	} else {
		f.lastMetadata = nil
	}
	if f.failuresRemaining > 0 {
		f.failuresRemaining--
		code := f.failWithCode
		f.mu.Unlock()
		return nil, status.Error(code, "temporarily unavailable")
	}
	f.mu.Unlock()
	if listed == nil {
		listed = []*runtimev1.TApiSandboxMetadata{
			{SandboxId: "sbx-1", OperationId: "op-1", Observed: &runtimev1.TerminalStatus{State: runtimev1.TerminalState_TERMINAL_STATE_RUNNING}},
		}
	}
	return &runtimev1.TApiListSandboxesResponse{Sandboxes: listed}, nil
}

func (f *fakeTApi) DeleteSandbox(ctx context.Context, req *runtimev1.TApiDeleteSandboxRequest) (*runtimev1.TApiDeleteSandboxResponse, error) {
	f.mu.Lock()
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		f.lastMetadata = md
	} else {
		f.lastMetadata = nil
	}
	if f.failuresRemaining > 0 {
		f.failuresRemaining--
		code := f.failWithCode
		f.mu.Unlock()
		return nil, status.Error(code, "temporarily unavailable")
	}
	f.mu.Unlock()
	return &runtimev1.TApiDeleteSandboxResponse{SandboxId: req.GetSandboxId()}, nil
}

func (f *fakeTApi) ResumeSandbox(ctx context.Context, req *runtimev1.TApiResumeSandboxRequest) (*runtimev1.TApiResumeSandboxResponse, error) {
	f.mu.Lock()
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		f.lastMetadata = md
	} else {
		f.lastMetadata = nil
	}
	if f.failuresRemaining > 0 {
		f.failuresRemaining--
		code := f.failWithCode
		f.mu.Unlock()
		return nil, status.Error(code, "temporarily unavailable")
	}
	f.mu.Unlock()
	return &runtimev1.TApiResumeSandboxResponse{
		SandboxId:         req.GetSandboxId(),
		ExecCapabilityJws: "fake.resumed.capability.jws",
		ExecEndpoint:      "https://100.89.203.43:9443",
	}, nil
}

func (f *fakeTApi) ListOrganizations(ctx context.Context, req *runtimev1.TApiListOrganizationsRequest) (*runtimev1.TApiListOrganizationsResponse, error) {
	f.mu.Lock()
	f.listOrganizationsCalls++
	orgs := f.organizations
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		f.lastMetadata = md
	} else {
		f.lastMetadata = nil
	}
	f.mu.Unlock()
	if orgs == nil {
		orgs = []*runtimev1.TApiOrganization{
			{OrganizationId: "org-personal", Name: "personal", Personal: true, Role: "owner", CreatedAtUnixNanos: 1},
		}
	}
	return &runtimev1.TApiListOrganizationsResponse{Organizations: orgs}, nil
}

func (f *fakeTApi) ListTemplates(ctx context.Context, req *runtimev1.TApiListTemplatesRequest) (*runtimev1.TApiListTemplatesResponse, error) {
	f.mu.Lock()
	f.listTemplatesCalls++
	templates := f.templates
	f.mu.Unlock()
	if templates == nil {
		templates = []*runtimev1.TApiTemplate{
			{TemplateId: "ubuntu-24.04", Version: "1", Digest: "sha256:default", IsDefault: true},
		}
	}
	return &runtimev1.TApiListTemplatesResponse{Templates: templates}, nil
}

func (f *fakeTApi) CreateSnapshot(ctx context.Context, req *runtimev1.TApiCreateSnapshotRequest) (*runtimev1.TApiCreateSnapshotResponse, error) {
	f.mu.Lock()
	f.createSnapshotCalls++
	f.mu.Unlock()
	return &runtimev1.TApiCreateSnapshotResponse{
		SnapshotId:      "snap-1",
		SourceSandboxId: req.GetSandboxId(),
	}, nil
}

func (f *fakeTApi) DeleteSnapshot(ctx context.Context, req *runtimev1.TApiDeleteSnapshotRequest) (*runtimev1.TApiDeleteSnapshotResponse, error) {
	f.mu.Lock()
	f.deleteSnapshotCalls++
	f.mu.Unlock()
	return &runtimev1.TApiDeleteSnapshotResponse{}, nil
}

func (f *fakeTApi) CreatePreview(ctx context.Context, req *runtimev1.TApiCreatePreviewRequest) (*runtimev1.TApiCreatePreviewResponse, error) {
	f.mu.Lock()
	f.createPreviewCalls++
	f.mu.Unlock()
	return &runtimev1.TApiCreatePreviewResponse{
		Preview: &runtimev1.PreviewInfo{
			Record: &runtimev1.PreviewRecord{
				PreviewId: "pv-1",
				SandboxId: req.GetSandboxId(),
				Port:      req.GetPort(),
				AuthMode:  req.GetAuthMode(),
				Name:      req.GetName(),
			},
			Url: "https://pv-1.preview.tyto.run",
		},
		CapabilityJws: "fake.preview.capability.jws",
	}, nil
}

func (f *fakeTApi) ListPreviews(ctx context.Context, req *runtimev1.TApiListPreviewsRequest) (*runtimev1.TApiListPreviewsResponse, error) {
	f.mu.Lock()
	f.listPreviewCalls++
	f.mu.Unlock()
	return &runtimev1.TApiListPreviewsResponse{
		Previews: []*runtimev1.PreviewInfo{
			{
				Record: &runtimev1.PreviewRecord{PreviewId: "pv-1", SandboxId: req.GetSandboxId(), Port: 8080},
				Url:    "https://pv-1.preview.tyto.run",
			},
		},
	}, nil
}

func (f *fakeTApi) DeletePreview(ctx context.Context, req *runtimev1.TApiDeletePreviewRequest) (*runtimev1.TApiDeletePreviewResponse, error) {
	f.mu.Lock()
	f.deletePreviewCalls++
	f.mu.Unlock()
	return &runtimev1.TApiDeletePreviewResponse{}, nil
}

// newBufconnClient starts an in-process gRPC server hosting fake and returns
// a *Client dialed to it over bufconn, plus a cleanup function.
func newBufconnClient(t *testing.T, fake *fakeTApi, opts ...Option) *Client {
	t.Helper()
	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	runtimev1grpc.RegisterTApiServiceServer(server, fake)
	go func() {
		_ = server.Serve(lis)
	}()
	t.Cleanup(server.Stop)

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }

	baseOpts := []Option{
		WithAPIKey("byk_test_key"),
		WithEndpoint("https://bufconn.test"),
		WithMaxRetries(2),
		WithTimeout(5 * time.Second),
	}
	client, err := NewClient(append(baseOpts, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	// Swap in a bufconn-backed connection for the control-plane endpoint so
	// the client's normal dial path (including the org-context interceptor
	// registered in Client.channel) is exercised unchanged.
	interceptor := orgContextInterceptor{currentOrganizationID: client.OrganizationID}
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
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

func TestOrgContextHeaderInjectedWhenConfigured(t *testing.T) {
	fake := &fakeTApi{}
	client := newBufconnClient(t, fake, WithOrganizationID("org-123"))

	_, err := client.Sandboxes.Create(context.Background(), "ubuntu-24.04")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	fake.mu.Lock()
	md := fake.lastMetadata
	fake.mu.Unlock()

	values := md.Get(organizationMetadataKey)
	if len(values) != 1 || values[0] != "org-123" {
		t.Errorf("bonya-organization-id metadata = %v, want [org-123]", values)
	}
}

// TestSetOrganizationIDAffectsAnAlreadyDialedChannel guards the mutability
// fix in orgContextInterceptor: SetOrganizationID must change what an
// existing, already-cached gRPC channel sends, not just what a channel
// dialed after the call would send. newBufconnClient pre-seeds the client's
// channel cache before this test ever calls SetOrganizationID, so a
// regression back to capturing the org id by value at dial time would make
// the second Create still carry "org-before" (or nothing).
func TestSetOrganizationIDAffectsAnAlreadyDialedChannel(t *testing.T) {
	fake := &fakeTApi{}
	client := newBufconnClient(t, fake, WithOrganizationID("org-before"))

	if _, err := client.Sandboxes.Create(context.Background(), "ubuntu-24.04"); err != nil {
		t.Fatalf("Create (before): %v", err)
	}
	fake.mu.Lock()
	before := fake.lastMetadata.Get(organizationMetadataKey)
	fake.mu.Unlock()
	if len(before) != 1 || before[0] != "org-before" {
		t.Fatalf("metadata before SetOrganizationID = %v, want [org-before]", before)
	}

	if err := client.SetOrganizationID("org-after"); err != nil {
		t.Fatalf("SetOrganizationID: %v", err)
	}

	if _, err := client.Sandboxes.Create(context.Background(), "ubuntu-24.04"); err != nil {
		t.Fatalf("Create (after): %v", err)
	}
	fake.mu.Lock()
	after := fake.lastMetadata.Get(organizationMetadataKey)
	fake.mu.Unlock()
	if len(after) != 1 || after[0] != "org-after" {
		t.Fatalf("metadata after SetOrganizationID = %v, want [org-after] -- the already-dialed channel did not observe the change", after)
	}
}

func TestSetOrganizationIDRejectsEmpty(t *testing.T) {
	client := newBufconnClient(t, &fakeTApi{}, WithOrganizationID("org-before"))

	err := client.SetOrganizationID("")
	var invalid *InvalidRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("SetOrganizationID(\"\") error = %v, want *InvalidRequestError", err)
	}
	if got := client.OrganizationID(); got != "org-before" {
		t.Fatalf("OrganizationID after rejected SetOrganizationID(\"\") = %q, want unchanged %q", got, "org-before")
	}
}

func TestOrgContextHeaderAbsentWhenNotConfigured(t *testing.T) {
	fake := &fakeTApi{}
	client := newBufconnClient(t, fake)

	_, err := client.Sandboxes.Create(context.Background(), "ubuntu-24.04")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	fake.mu.Lock()
	md := fake.lastMetadata
	fake.mu.Unlock()

	values := md.Get(organizationMetadataKey)
	if len(values) != 0 {
		t.Errorf("bonya-organization-id metadata = %v, want none", values)
	}
}

func TestCreateRetriesOnUnavailableThenSucceeds(t *testing.T) {
	fake := &fakeTApi{failuresRemaining: 2, failWithCode: codes.Unavailable}
	client := newBufconnClient(t, fake, WithMaxRetries(2))

	sandbox, err := client.Sandboxes.Create(context.Background(), "ubuntu-24.04")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sandbox.ID != "sbx-1" {
		t.Errorf("sandbox.ID = %q, want sbx-1", sandbox.ID)
	}
	fake.mu.Lock()
	calls := fake.createCalls
	fake.mu.Unlock()
	if calls != 3 {
		t.Errorf("createCalls = %d, want 3 (2 failures + 1 success)", calls)
	}
}

func TestCreateFailsAfterExhaustingRetries(t *testing.T) {
	fake := &fakeTApi{failuresRemaining: 10, failWithCode: codes.Unavailable}
	client := newBufconnClient(t, fake, WithMaxRetries(2))

	_, err := client.Sandboxes.Create(context.Background(), "ubuntu-24.04")
	if err == nil {
		t.Fatal("Create() = nil error, want an error after exhausting retries")
	}
	var connErr *ConnectionError
	if _, ok := err.(*ConnectionError); !ok {
		t.Errorf("Create() error type = %T, want *ConnectionError", err)
		_ = connErr
	}
	fake.mu.Lock()
	calls := fake.createCalls
	fake.mu.Unlock()
	// 1 initial attempt + 2 retries = 3 calls.
	if calls != 3 {
		t.Errorf("createCalls = %d, want 3", calls)
	}
}

func TestGetDoesNotRetryOnNonRetryableCode(t *testing.T) {
	fake := &fakeTApi{failuresRemaining: 1, failWithCode: codes.InvalidArgument}
	client := newBufconnClient(t, fake, WithMaxRetries(2))

	_, err := client.Sandboxes.Get(context.Background(), "sbx-1")
	if err == nil {
		t.Fatal("Get() = nil error, want an error")
	}
	if _, ok := err.(*InvalidRequestError); !ok {
		t.Errorf("Get() error type = %T, want *InvalidRequestError", err)
	}
	fake.mu.Lock()
	calls := fake.getCalls
	fake.mu.Unlock()
	if calls != 1 {
		t.Errorf("getCalls = %d, want 1 (no retry on INVALID_ARGUMENT)", calls)
	}
}

func TestListRetriesOnUnavailableThenSucceeds(t *testing.T) {
	fake := &fakeTApi{failuresRemaining: 1, failWithCode: codes.Unavailable}
	client := newBufconnClient(t, fake, WithMaxRetries(2))

	summaries, err := client.Sandboxes.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != "sbx-1" {
		t.Errorf("List() = %+v, want one summary with ID sbx-1", summaries)
	}
}

func runningMetadata(sandboxID, name string) *runtimev1.TApiSandboxMetadata {
	return &runtimev1.TApiSandboxMetadata{
		SandboxId:   sandboxID,
		OperationId: "op-1",
		Observed:    &runtimev1.TerminalStatus{State: runtimev1.TerminalState_TERMINAL_STATE_RUNNING},
		Name:        name,
	}
}

func TestCreateSendsNameAndReturnsIt(t *testing.T) {
	fake := &fakeTApi{}
	client := newBufconnClient(t, fake)
	sandbox, err := client.Sandboxes.Create(context.Background(), "ubuntu", CreateOptions{Name: "my-box", Wait: WaitNone})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if fake.lastCreateName != "my-box" {
		t.Fatalf("service received name %q, want my-box", fake.lastCreateName)
	}
	if sandbox.Name != "my-box" {
		t.Fatalf("sandbox.Name = %q, want my-box", sandbox.Name)
	}
}

// With no name supplied the service generates one, and the SDK has to surface
// it or the caller never learns what their sandbox is called.
func TestCreateSurfacesAGeneratedName(t *testing.T) {
	fake := &fakeTApi{createdName: "brave-cedar-6268"}
	client := newBufconnClient(t, fake)
	sandbox, err := client.Sandboxes.Create(context.Background(), "ubuntu", CreateOptions{Wait: WaitNone})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if fake.lastCreateName != "" {
		t.Fatalf("service received name %q, want empty", fake.lastCreateName)
	}
	if sandbox.Name != "brave-cedar-6268" {
		t.Fatalf("sandbox.Name = %q, want the generated brave-cedar-6268", sandbox.Name)
	}
}

func TestListPassesNameFilterAndReturnsNames(t *testing.T) {
	fake := &fakeTApi{listSandboxes: []*runtimev1.TApiSandboxMetadata{runningMetadata("sbx-1", "my-box")}}
	client := newBufconnClient(t, fake)
	summaries, err := client.Sandboxes.List(context.Background(), ListOptions{Name: "my-box"})
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if fake.lastListName != "my-box" {
		t.Fatalf("service received name filter %q, want my-box", fake.lastListName)
	}
	if len(summaries) != 1 || summaries[0].Name != "my-box" {
		t.Fatalf("summaries = %#v, want one named my-box", summaries)
	}
}

func TestGetByNameResolvesToASandbox(t *testing.T) {
	fake := &fakeTApi{listSandboxes: []*runtimev1.TApiSandboxMetadata{runningMetadata("sbx-42", "my-box")}}
	client := newBufconnClient(t, fake)
	sandbox, err := client.Sandboxes.GetByName(context.Background(), "my-box")
	if err != nil {
		t.Fatalf("GetByName error = %v", err)
	}
	if sandbox.ID != "sbx-42" {
		t.Fatalf("sandbox.ID = %q, want sbx-42", sandbox.ID)
	}
	// The name is only used to find the id; the fetch itself is by id.
	if fake.getCalls != 1 {
		t.Fatalf("GetSandbox calls = %d, want 1", fake.getCalls)
	}
}

func TestGetByNameReportsNoMatch(t *testing.T) {
	fake := &fakeTApi{listSandboxes: []*runtimev1.TApiSandboxMetadata{}}
	client := newBufconnClient(t, fake)
	_, err := client.Sandboxes.GetByName(context.Background(), "absent")
	var notFound *SandboxNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("GetByName error = %v, want SandboxNotFoundError", err)
	}
}

// Names are not unique, and silently picking one would let a later Delete
// destroy an arbitrary sandbox.
func TestGetByNameRefusesToGuessBetweenDuplicates(t *testing.T) {
	fake := &fakeTApi{listSandboxes: []*runtimev1.TApiSandboxMetadata{
		runningMetadata("sbx-1", "shared"),
		runningMetadata("sbx-2", "shared"),
	}}
	client := newBufconnClient(t, fake)
	_, err := client.Sandboxes.GetByName(context.Background(), "shared")
	var invalid *InvalidRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("GetByName error = %v, want InvalidRequestError", err)
	}
	if fake.getCalls != 0 {
		t.Fatalf("GetSandbox calls = %d, want 0: an ambiguous name must not fetch anything", fake.getCalls)
	}
}

func TestGetByNameRequiresAName(t *testing.T) {
	client := newBufconnClient(t, &fakeTApi{})
	_, err := client.Sandboxes.GetByName(context.Background(), "")
	var invalid *InvalidRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("GetByName error = %v, want InvalidRequestError", err)
	}
}
