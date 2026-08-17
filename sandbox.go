package tyto

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strconv"
	"strings"
	"sync"
	"time"

	runtimev1 "buf.build/gen/go/bonya/tyto/protocolbuffers/go/tyto/runtime/v1"
)

// Sandbox is a handle to a running or reconnectable sandbox.
type Sandbox struct {
	client *Client

	ID                 string
	OperationID        string
	Template           string
	Version            string
	LastObservedStatus Status
	// Name is the display name. The service generates one when Create is not
	// given a name. Names are not unique, and nothing is addressed by them:
	// every operation on a sandbox is keyed by ID.
	Name string

	// Files, Sessions, and Previews are the dedicated RPC surfaces for this sandbox.
	Files    *SandboxFiles
	Sessions *SandboxSessions
	Previews *SandboxPreviews

	mu             sync.Mutex
	execEndpoint   string
	capability     string
	failureCode    string
	failureMessage string
	deleted        bool
}

type sandboxCreateArgs struct {
	sandboxID      string
	operationID    string
	template       string
	version        string
	status         Status
	execEndpoint   string
	capability     string
	failureCode    string
	failureMessage string
	name           string
}

func newSandbox(client *Client, args sandboxCreateArgs) *Sandbox {
	s := &Sandbox{
		client:             client,
		ID:                 args.sandboxID,
		OperationID:        args.operationID,
		Template:           args.template,
		Version:            args.version,
		LastObservedStatus: args.status,
		Name:               args.name,
		execEndpoint:       args.execEndpoint,
		capability:         args.capability,
		failureCode:        args.failureCode,
		failureMessage:     args.failureMessage,
	}
	s.Files = &SandboxFiles{sandbox: s}
	s.Sessions = &SandboxSessions{sandbox: s}
	s.Previews = &SandboxPreviews{sandbox: s}
	return s
}

func (s *Sandbox) snapshotState() (execEndpoint, capability string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.execEndpoint, s.capability
}

func (s *Sandbox) isDeleted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleted || s.LastObservedStatus == StatusDeleted
}

func (s *Sandbox) failureDetail() (code, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failureCode, s.failureMessage
}

// ExecResult is the buffered result of Sandbox.Exec.
type ExecResult struct {
	StdoutBytes []byte
	StderrBytes []byte
	ExitCode    int
	Signaled    bool
	Signal      int
	SandboxID   string
}

// Stdout returns stdout decoded as UTF-8, with invalid sequences replaced.
func (r *ExecResult) Stdout() string { return toValidUTF8(r.StdoutBytes) }

// Stderr returns stderr decoded as UTF-8, with invalid sequences replaced.
func (r *ExecResult) Stderr() string { return toValidUTF8(r.StderrBytes) }

// OK reports whether the process exited cleanly with status 0.
func (r *ExecResult) OK() bool { return r.ExitCode == 0 && !r.Signaled }

// Check returns an *ExecFailedError if the command did not exit cleanly.
func (r *ExecResult) Check() error {
	if r.OK() {
		return nil
	}
	return &ExecFailedError{
		BaseError: BaseError{
			Msg:       "command failed with exit code " + strconv.Itoa(r.ExitCode),
			SandboxID: r.SandboxID,
		},
		Result: r,
	}
}

// String returns Stdout(), so an ExecResult can be printed directly.
func (r *ExecResult) String() string { return r.Stdout() }

// ExecOptions configures Sandbox.Exec and Sandbox.ExecStream.
type ExecOptions struct {
	Env     map[string]string
	Cwd     string
	TTY     bool
	Cols    int
	Rows    int
	Timeout time.Duration
	// Check, when true, calls ExecResult.Check before Exec returns. Ignored by ExecStream.
	Check bool
	// Input is written to stdin (then stdin is half-closed) for buffered Exec.
	// It requires TTY to be false. Ignored by ExecStream; use ExecSession.Write instead.
	Input []byte
}

// Exec runs a command and buffers stdout, stderr, and exit status.
//
// command may be a single string (executed as ["/bin/sh", "-c", command]) or
// an argv slice. Use ExecStream for large output, long-running commands,
// interactive stdin, or cancellation.
func (s *Sandbox) Exec(ctx context.Context, command any, opts ...ExecOptions) (*ExecResult, error) {
	var o ExecOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Input != nil && o.TTY {
		return nil, &InvalidRequestError{BaseError{Msg: "input requires tty=false"}}
	}

	session, err := s.ExecStream(ctx, command, opts...)
	if err != nil {
		return nil, err
	}
	if o.Input != nil {
		if err := session.Write(o.Input); err != nil {
			session.Cancel()
			return nil, err
		}
		if err := session.CloseStdin(); err != nil {
			session.Cancel()
			return nil, err
		}
	}

	var stdout, stderr []byte
	var terminal *Exit
	for {
		event, err := session.Next()
		if err != nil {
			session.Cancel()
			return nil, err
		}
		if event == nil {
			break
		}
		switch e := event.(type) {
		case Stdout:
			stdout = append(stdout, e.Data...)
		case Stderr:
			stderr = append(stderr, e.Data...)
		case Exit:
			terminal = &e
		}
	}
	if terminal == nil {
		return nil, &InvalidRequestError{BaseError{Msg: "Exec stream ended without an exit event", SandboxID: s.ID}}
	}
	result := &ExecResult{
		StdoutBytes: stdout,
		StderrBytes: stderr,
		ExitCode:    terminal.ExitCode,
		Signaled:    terminal.Signaled,
		Signal:      terminal.Signal,
		SandboxID:   s.ID,
	}
	if o.Check {
		if err := result.Check(); err != nil {
			return result, err
		}
	}
	return result, nil
}

// ExecStream starts a streaming Exec session and returns events as they
// arrive over the returned ExecSession.
func (s *Sandbox) ExecStream(ctx context.Context, command any, opts ...ExecOptions) (*ExecSession, error) {
	if err := s.ensureExecAllowed(); err != nil {
		return nil, err
	}
	var o ExecOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	argv, err := normalizeCommand(command)
	if err != nil {
		return nil, err
	}
	env, err := normalizeEnv(o.Env)
	if err != nil {
		return nil, err
	}
	cwd, err := normalizeCwd(o.Cwd)
	if err != nil {
		return nil, err
	}
	tty, cols, rows, err := validateExecTTYOptions(o.TTY, o.Cols, o.Rows)
	if err != nil {
		return nil, err
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = s.client.timeout
	}

	return newRefreshableExecSession(s, execSessionParams{
		command: argv,
		env:     env,
		cwd:     cwd,
		tty:     tty,
		cols:    cols,
		rows:    rows,
		timeout: timeout,
	})
}

// Delete deletes the sandbox. It is idempotent: calling it again on the same
// Sandbox is local and returns AlreadyDeleted=true without another RPC.
//
// The RPC itself is SandboxCollection.Delete; this adds the local
// already-deleted short-circuit and updates the handle's own status, which
// only make sense with a handle to check and update.
func (s *Sandbox) Delete(ctx context.Context) (*DeleteResult, error) {
	s.mu.Lock()
	if s.deleted {
		s.mu.Unlock()
		return &DeleteResult{SandboxID: s.ID, AlreadyDeleted: true}, nil
	}
	s.mu.Unlock()

	result, err := s.client.Sandboxes.Delete(ctx, s.ID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.deleted = true
	s.LastObservedStatus = StatusDeleted
	s.mu.Unlock()
	return result, nil
}

// SnapshotOptions configures Sandbox.Snapshot.
type SnapshotOptions struct {
	IdempotencyKey string
}

// Snapshot creates a snapshot from this (running) sandbox.
func (s *Sandbox) Snapshot(ctx context.Context, opts ...SnapshotOptions) (*Snapshot, error) {
	if s.isDeleted() {
		return nil, &SandboxDeletedError{BaseError{Msg: "sandbox has been deleted", SandboxID: s.ID, OperationID: s.OperationID}}
	}
	if s.LastObservedStatus == StatusFailed {
		return nil, s.failedError()
	}
	if s.LastObservedStatus == StatusSuspended {
		return nil, &SandboxSuspendedError{BaseError{Msg: "sandbox is suspended", SandboxID: s.ID, OperationID: s.OperationID}}
	}
	var o SnapshotOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	key := o.IdempotencyKey
	if key == "" {
		key = generateRandomToken()
	}
	request := &runtimev1.TApiCreateSnapshotRequest{ApiKey: s.client.apiKey, SandboxId: s.ID, IdempotencyKey: key}

	dl, err := startDeadline(s.client.timeout)
	if err != nil {
		return nil, err
	}
	attempts := 0
	backoff := 50 * time.Millisecond
	for {
		remaining, err := dl.remaining()
		if err != nil {
			return nil, MapRPCError(err, s.client.secrets(key), WithSandboxID(s.ID), WithOperationID(s.OperationID), WithIdempotencyKey(key))
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := s.client.tapiClient()
		if tErr != nil {
			cancel()
			return nil, tErr
		}
		response, callErr := client.CreateSnapshot(callCtx, request)
		cancel()
		if callErr == nil {
			snapshotID := response.GetSnapshotId()
			sourceSandboxID := response.GetSourceSandboxId()
			if snapshotID == "" || sourceSandboxID == "" {
				return nil, &InvalidRequestError{BaseError{Msg: "CreateSnapshot response is missing snapshot identity", SandboxID: s.ID, OperationID: s.OperationID, IdempotencyKey: key}}
			}
			if sourceSandboxID != s.ID {
				return nil, &InvalidRequestError{BaseError{Msg: "CreateSnapshot response is missing source identity", SandboxID: s.ID, OperationID: s.OperationID, IdempotencyKey: key}}
			}
			return &Snapshot{client: s.client, ID: snapshotID, SourceSandboxID: sourceSandboxID}, nil
		}
		if !IsRetryable(callErr) || attempts >= s.client.maxRetries {
			return nil, MapRPCError(callErr, s.client.secrets(key), WithSandboxID(s.ID), WithOperationID(s.OperationID), WithIdempotencyKey(key))
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

// ResumeOptions configures Sandbox.Resume.
type ResumeOptions struct {
	IdempotencyKey string
}

// Resume explicitly resumes a suspended sandbox before running work.
//
// The RPC itself is SandboxCollection.resumeSandbox; this additionally
// copies the refreshed capability and exec endpoint onto the handle, which
// only makes sense with a handle to update, and checks for a locally known
// failed status before making a request the server would refuse anyway.
func (s *Sandbox) Resume(ctx context.Context, opts ...ResumeOptions) (*ResumeResult, error) {
	if s.LastObservedStatus == StatusFailed {
		return nil, s.failedError()
	}
	result, response, err := s.client.Sandboxes.resumeSandbox(ctx, s.ID, opts...)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if cap := response.GetExecCapabilityJws(); cap != "" {
		s.capability = cap
	}
	if ep := response.GetExecEndpoint(); ep != "" {
		s.execEndpoint = ep
	}
	s.LastObservedStatus = StatusRunning
	s.mu.Unlock()
	return result, nil
}

// ReissueCapability mints a fresh data-plane capability via TApi and uses it
// for subsequent calls on this Sandbox. Sessions and Files call this
// transparently on a rejected/expired capability; call it directly only if
// you manage tokens yourself.
func (s *Sandbox) ReissueCapability(ctx context.Context) error {
	_, capability := s.snapshotState()
	request := &runtimev1.TApiReissueCapabilityRequest{ApiKey: s.client.apiKey, SandboxId: s.ID}
	dl, err := startDeadline(s.client.timeout)
	if err != nil {
		return err
	}
	remaining, err := dl.remaining()
	if err != nil {
		return MapRPCError(err, s.client.secrets(capability), WithSandboxID(s.ID), WithOperationID(s.OperationID))
	}
	callCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	client, tErr := s.client.tapiClient()
	if tErr != nil {
		return tErr
	}
	response, callErr := client.ReissueCapability(callCtx, request)
	if callErr != nil {
		return MapRPCError(callErr, s.client.secrets(capability), WithSandboxID(s.ID), WithOperationID(s.OperationID))
	}
	newCapability := response.GetCapabilityJws()
	if newCapability == "" {
		return &InvalidRequestError{BaseError{Msg: "ReissueCapability response is missing capability_jws", SandboxID: s.ID, OperationID: s.OperationID}}
	}
	s.mu.Lock()
	s.capability = newCapability
	s.mu.Unlock()
	return nil
}

// refreshCapabilityOnce re-fetches the sandbox via Get and copies its
// updated state (status, endpoint, capability) onto this handle. Used by
// Files and Sessions after a capability rejection.
func (s *Sandbox) refreshCapabilityOnce(ctx context.Context) error {
	refreshed, err := s.client.Sandboxes.Get(ctx, s.ID)
	if err != nil {
		return err
	}
	if refreshed.LastObservedStatus == StatusFailed {
		s.mu.Lock()
		s.LastObservedStatus = StatusFailed
		s.failureCode = refreshed.failureCode
		s.failureMessage = refreshed.failureMessage
		s.mu.Unlock()
		return s.failedError()
	}
	s.mu.Lock()
	s.OperationID = refreshed.OperationID
	s.Template = refreshed.Template
	s.Version = refreshed.Version
	s.LastObservedStatus = refreshed.LastObservedStatus
	s.execEndpoint = refreshed.execEndpoint
	s.capability = refreshed.capability
	s.failureCode = ""
	s.failureMessage = ""
	s.mu.Unlock()
	return nil
}

func (s *Sandbox) ensureExecAllowed() error {
	if s.isDeleted() {
		return &SandboxDeletedError{BaseError{Msg: "sandbox has been deleted", SandboxID: s.ID, OperationID: s.OperationID}}
	}
	if s.LastObservedStatus == StatusFailed {
		return s.failedError()
	}
	return nil
}

func (s *Sandbox) failedError() error {
	code, message := s.failureDetail()
	msg := message
	if msg == "" {
		msg = code
	}
	if msg == "" {
		msg = "sandbox failed"
	}
	return &SandboxFailedError{BaseError{Msg: msg, SandboxID: s.ID, OperationID: s.OperationID}}
}

// observeExecError maps a raw Exec RPC error and updates local sandbox state
// (deleted/suspended/failed) to reflect what the server reported.
func (s *Sandbox) observeExecError(err error) error {
	_, capability := s.snapshotState()
	mapped := MapRPCError(err, s.client.secrets(capability), WithSandboxID(s.ID), WithOperationID(s.OperationID), WithExecRPC())
	switch mapped.(type) {
	case *SandboxDeletedError:
		s.mu.Lock()
		s.deleted = true
		s.LastObservedStatus = StatusDeleted
		s.mu.Unlock()
	case *SandboxSuspendedError:
		s.mu.Lock()
		s.LastObservedStatus = StatusSuspended
		s.mu.Unlock()
	}
	return mapped
}

// Snapshot is a point-in-time filesystem snapshot of a sandbox.
type Snapshot struct {
	client          *Client
	ID              string
	SourceSandboxID string
	deleted         bool
	mu              sync.Mutex
}

// Delete deletes the snapshot. It is idempotent on the same Snapshot object.
func (snap *Snapshot) Delete(ctx context.Context) error {
	snap.mu.Lock()
	if snap.deleted {
		snap.mu.Unlock()
		return nil
	}
	snap.mu.Unlock()

	if err := deleteSnapshotRPC(ctx, snap.client, snap.SourceSandboxID, snap.ID); err != nil {
		return err
	}
	snap.mu.Lock()
	snap.deleted = true
	snap.mu.Unlock()
	return nil
}

// DeleteSnapshot deletes a snapshot of this sandbox by ID, for callers who
// persisted only the snapshot ID (per this SDK's "store sandbox.ID, close
// the client, reconnect later" ownership guidance) rather than holding the
// *Snapshot handle Sandbox.Snapshot returned at creation time. Prefer
// Snapshot.Delete when you already have that handle -- it is idempotent
// across repeated local calls, which this free-standing form is not.
func (s *Sandbox) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	return deleteSnapshotRPC(ctx, s.client, s.ID, snapshotID)
}

func deleteSnapshotRPC(ctx context.Context, client *Client, sourceSandboxID, snapshotID string) error {
	request := &runtimev1.TApiDeleteSnapshotRequest{
		ApiKey:          client.apiKey,
		SourceSandboxId: sourceSandboxID,
		SnapshotId:      snapshotID,
	}
	dl, err := startDeadline(client.timeout)
	if err != nil {
		return err
	}
	attempts := 0
	backoff := 50 * time.Millisecond
	for {
		remaining, err := dl.remaining()
		if err != nil {
			return MapRPCError(err, client.secrets(snapshotID), WithSandboxID(sourceSandboxID))
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		tapiClient, tErr := client.tapiClient()
		if tErr != nil {
			cancel()
			return tErr
		}
		_, callErr := tapiClient.DeleteSnapshot(callCtx, request)
		cancel()
		if callErr == nil {
			return nil
		}
		if !IsRetryable(callErr) || attempts >= client.maxRetries {
			return MapRPCError(callErr, client.secrets(snapshotID), WithSandboxID(sourceSandboxID))
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

func generateRandomToken() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func toValidUTF8(b []byte) string {
	return strings.ToValidUTF8(string(b), string([]rune{0xFFFD}))
}
