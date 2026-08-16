package tyto

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc/metadata"

	runtimev1 "github.com/bonyai/tyto-go/internal/gen/tyto/runtime/v1"
)

// ExecSession is a live, bidirectional Exec stream. Iterate it with Next,
// write stdin with Write, and clean up with Close (or Cancel to end the
// remote process explicitly).
//
// Internally this replaces the Python SDK's queue+thread+condvar machinery
// with a background reader goroutine per underlying gRPC stream, feeding a
// buffered event channel that outlives a single stream: on a capability
// refresh (see sandbox.go's ExecStream) the session transparently opens a
// new underlying stream and reader goroutine in place, so the *ExecSession
// pointer callers hold never changes.
type ExecSession struct {
	sandbox *Sandbox
	params  execSessionParams
	tty     bool

	deadline       deadline
	cleanupTimeout time.Duration

	mu               sync.Mutex
	current          *execStream
	closed           bool
	refreshed        bool
	responsesStarted bool
	pending          []pendingInput
}

// execStream is one underlying gRPC Exec stream and its reader goroutine.
type execStream struct {
	stream     runtimev1.GuestService_ExecClient
	cancel     context.CancelFunc
	sendMu     sync.Mutex
	events     chan execEvent
	readerDone chan struct{}

	mu          sync.Mutex
	stdinClosed bool
	cancelled   bool
	capability  string
}

type execEvent struct {
	value any // Stdout, Stderr, or Exit
	err   error
	end   bool
}

type pendingInputKind int

const (
	pendingWrite pendingInputKind = iota
	pendingCloseStdin
	pendingResize
)

type pendingInput struct {
	kind       pendingInputKind
	data       []byte
	cols, rows int
}

type execSessionParams struct {
	command []string
	env     map[string]string
	cwd     string
	tty     bool
	cols    int
	rows    int
	timeout time.Duration
}

// newRefreshableExecSession opens a new Exec stream for sandbox and returns
// an ExecSession that transparently refreshes an expired capability exactly
// once, replaying any writes/resizes/stdin-closes issued before the first
// response arrived.
func newRefreshableExecSession(sandbox *Sandbox, params execSessionParams) (*ExecSession, error) {
	dl, err := startDeadline(params.timeout)
	if err != nil {
		return nil, err
	}
	cleanupTimeout := params.timeout
	if cleanupTimeout > 5*time.Second {
		cleanupTimeout = 5 * time.Second
	}
	if cleanupTimeout < 500*time.Millisecond {
		cleanupTimeout = 500 * time.Millisecond
	}
	session := &ExecSession{
		sandbox:        sandbox,
		params:         params,
		tty:            params.tty,
		deadline:       dl,
		cleanupTimeout: cleanupTimeout,
	}
	stream, err := session.openStream()
	if err != nil {
		return nil, err
	}
	session.current = stream
	return session, nil
}

// openStream dials the sandbox's current exec endpoint/capability and sends
// the ExecStart frame.
func (s *ExecSession) openStream() (*execStream, error) {
	execEndpoint, capability := s.sandbox.snapshotState()
	guestClient, err := s.sandbox.client.guestClient(execEndpoint)
	if err != nil {
		return nil, err
	}

	streamCtx, cancel := context.WithTimeout(context.Background(), s.params.timeout+s.cleanupTimeout+time.Second)
	streamCtx = metadata.AppendToOutgoingContext(streamCtx,
		"bonya-sandbox-id", s.sandbox.ID,
		"bonya-exec-capability", capability,
	)
	grpcStream, err := guestClient.Exec(streamCtx)
	if err != nil {
		cancel()
		return nil, s.sandbox.observeExecError(err)
	}

	start := &runtimev1.ExecRequest{
		Frame: &runtimev1.ExecRequest_Start{
			Start: &runtimev1.ExecStart{
				Command:    s.params.command,
				Env:        s.params.env,
				WorkingDir: s.params.cwd,
				Tty:        s.params.tty,
				Cols:       uint32(s.params.cols),
				Rows:       uint32(s.params.rows),
			},
		},
	}
	if err := grpcStream.Send(start); err != nil {
		cancel()
		return nil, s.sandbox.observeExecError(err)
	}

	es := &execStream{
		stream:     grpcStream,
		cancel:     cancel,
		events:     make(chan execEvent, 16),
		readerDone: make(chan struct{}),
		capability: capability,
	}
	go es.readLoop(s.sandbox)
	return es, nil
}

func (es *execStream) readLoop(sandbox *Sandbox) {
	defer close(es.readerDone)
	for {
		resp, err := es.stream.Recv()
		if err != nil {
			es.mu.Lock()
			closedOrCancelled := es.cancelled
			es.mu.Unlock()
			if closedOrCancelled {
				es.emit(execEvent{end: true})
				return
			}
			es.emit(execEvent{err: sandbox.observeExecError(err)})
			return
		}
		event, evErr := execResponseToEvent(resp, sandbox.ID)
		if evErr != nil {
			es.emit(execEvent{err: evErr})
			return
		}
		es.emit(event)
		if _, isExit := event.value.(Exit); isExit {
			es.emit(execEvent{end: true})
			return
		}
	}
}

func execResponseToEvent(resp *runtimev1.ExecResponse, sandboxID string) (execEvent, error) {
	switch frame := resp.GetFrame().(type) {
	case *runtimev1.ExecResponse_Stdout:
		return execEvent{value: Stdout{Data: frame.Stdout.GetData()}}, nil
	case *runtimev1.ExecResponse_Stderr:
		return execEvent{value: Stderr{Data: frame.Stderr.GetData()}}, nil
	case *runtimev1.ExecResponse_Exit:
		return execEvent{value: Exit{
			ExitCode: int(frame.Exit.GetExitCode()),
			Signaled: frame.Exit.GetSignaled(),
			Signal:   int(frame.Exit.GetSignal()),
		}}, nil
	default:
		return execEvent{}, &InvalidRequestError{BaseError{Msg: "Exec response contained no frame", SandboxID: sandboxID}}
	}
}

// emit delivers an event to the channel, giving up after a bounded wait if a
// closed session's consumer is gone.
func (es *execStream) emit(e execEvent) {
	select {
	case es.events <- e:
	case <-time.After(5 * time.Second):
	}
}

// Next blocks for the next event: Stdout, Stderr, or Exit. It returns
// (nil, nil) once the stream has ended cleanly after an Exit event.
func (s *ExecSession) Next() (any, error) {
	event, err := s.next()
	if err != nil {
		if _, ok := err.(*CapabilityRejectedError); ok {
			if refreshedEvent, refreshedErr, handled := s.tryRefresh(); handled {
				return refreshedEvent, refreshedErr
			}
		}
		return nil, err
	}
	s.mu.Lock()
	s.responsesStarted = true
	s.mu.Unlock()
	return event, nil
}

func (s *ExecSession) next() (any, error) {
	s.mu.Lock()
	current := s.current
	s.mu.Unlock()

	remaining, err := s.deadline.remaining()
	if err != nil {
		s.Cancel()
		return nil, &TimeoutError{BaseError{Msg: "Exec timed out", SandboxID: s.sandbox.ID, OperationID: s.sandbox.OperationID}}
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case ev := <-current.events:
		if ev.end {
			return nil, nil
		}
		if ev.err != nil {
			return nil, ev.err
		}
		return ev.value, nil
	case <-timer.C:
		s.Cancel()
		return nil, &TimeoutError{BaseError{Msg: "Exec timed out", SandboxID: s.sandbox.ID, OperationID: s.sandbox.OperationID}}
	}
}

// tryRefresh implements the one-time capability-refresh-and-retry described
// on ExecSession. handled=false means refresh does not apply here (already
// refreshed, or the capability was not actually expired) and the caller
// should treat the original CapabilityRejectedError as final.
func (s *ExecSession) tryRefresh() (any, error, bool) {
	s.mu.Lock()
	if s.refreshed || s.responsesStarted {
		s.mu.Unlock()
		return nil, nil, false
	}
	s.mu.Unlock()

	_, capability := s.sandbox.snapshotState()
	if !jwsExpired(capability) {
		return nil, nil, false
	}

	s.mu.Lock()
	s.refreshed = true
	oldStream := s.current
	s.mu.Unlock()
	oldStream.close(s.cleanupTimeout)

	if err := s.sandbox.ReissueCapability(context.Background()); err != nil {
		return nil, err, true
	}
	newStream, err := s.openStream()
	if err != nil {
		return nil, err, true
	}

	s.mu.Lock()
	s.current = newStream
	pending := s.pending
	s.mu.Unlock()

	for _, p := range pending {
		switch p.kind {
		case pendingWrite:
			_ = newStream.write(p.data)
		case pendingCloseStdin:
			_ = newStream.closeStdin()
		case pendingResize:
			_ = newStream.resize(p.cols, p.rows)
		}
	}

	event, nextErr := s.next()
	s.mu.Lock()
	s.responsesStarted = true
	s.mu.Unlock()
	return event, nextErr, true
}

// Write sends stdin bytes.
func (s *ExecSession) Write(data []byte) error {
	s.mu.Lock()
	current := s.current
	responsesStarted := s.responsesStarted
	refreshed := s.refreshed
	s.mu.Unlock()
	if err := current.write(data); err != nil {
		return err
	}
	if !responsesStarted && !refreshed {
		s.mu.Lock()
		s.pending = append(s.pending, pendingInput{kind: pendingWrite, data: append([]byte(nil), data...)})
		s.mu.Unlock()
	}
	return nil
}

// CloseStdin half-closes stdin. It is idempotent.
func (s *ExecSession) CloseStdin() error {
	s.mu.Lock()
	current := s.current
	responsesStarted := s.responsesStarted
	refreshed := s.refreshed
	s.mu.Unlock()
	if err := current.closeStdin(); err != nil {
		return err
	}
	if !responsesStarted && !refreshed {
		s.mu.Lock()
		s.pending = append(s.pending, pendingInput{kind: pendingCloseStdin})
		s.mu.Unlock()
	}
	return nil
}

// Resize changes the TTY dimensions of a running Exec session.
func (s *ExecSession) Resize(cols, rows int) error {
	c, err := validateDimension("cols", cols)
	if err != nil {
		return err
	}
	r, err := validateDimension("rows", rows)
	if err != nil {
		return err
	}
	if !s.tty {
		return &InvalidRequestError{BaseError{Msg: "resize requires a tty Exec session", SandboxID: s.sandbox.ID}}
	}
	s.mu.Lock()
	current := s.current
	responsesStarted := s.responsesStarted
	refreshed := s.refreshed
	s.mu.Unlock()
	if err := current.resize(c, r); err != nil {
		return err
	}
	if !responsesStarted && !refreshed {
		s.mu.Lock()
		s.pending = append(s.pending, pendingInput{kind: pendingResize, cols: c, rows: r})
		s.mu.Unlock()
	}
	return nil
}

// Cancel cancels the remote Exec. It is idempotent.
func (s *ExecSession) Cancel() {
	s.mu.Lock()
	current := s.current
	s.mu.Unlock()
	current.close(s.cleanupTimeout)
}

// Close closes the session, cancelling an unfinished remote Exec.
func (s *ExecSession) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	current := s.current
	s.mu.Unlock()
	current.close(s.cleanupTimeout)
}

func (es *execStream) write(data []byte) error {
	es.mu.Lock()
	if es.cancelled {
		es.mu.Unlock()
		return &InvalidRequestError{BaseError{Msg: "Exec session is closed"}}
	}
	if es.stdinClosed {
		es.mu.Unlock()
		return &InvalidRequestError{BaseError{Msg: "stdin is closed"}}
	}
	es.mu.Unlock()

	es.sendMu.Lock()
	defer es.sendMu.Unlock()
	return es.stream.Send(&runtimev1.ExecRequest{
		Frame: &runtimev1.ExecRequest_Stdin{Stdin: &runtimev1.StdinData{Data: data}},
	})
}

func (es *execStream) closeStdin() error {
	es.mu.Lock()
	if es.stdinClosed {
		es.mu.Unlock()
		return nil
	}
	es.stdinClosed = true
	es.mu.Unlock()

	es.sendMu.Lock()
	defer es.sendMu.Unlock()
	return es.stream.CloseSend()
}

func (es *execStream) resize(cols, rows int) error {
	es.mu.Lock()
	if es.cancelled {
		es.mu.Unlock()
		return &InvalidRequestError{BaseError{Msg: "Exec session is closed"}}
	}
	if es.stdinClosed {
		es.mu.Unlock()
		return &InvalidRequestError{BaseError{Msg: "stdin is closed"}}
	}
	es.mu.Unlock()

	es.sendMu.Lock()
	defer es.sendMu.Unlock()
	return es.stream.Send(&runtimev1.ExecRequest{
		Frame: &runtimev1.ExecRequest_Resize{Resize: &runtimev1.ExecResize{Cols: uint32(cols), Rows: uint32(rows)}},
	})
}

func (es *execStream) close(cleanupTimeout time.Duration) {
	es.mu.Lock()
	if es.cancelled {
		es.mu.Unlock()
		return
	}
	es.cancelled = true
	es.stdinClosed = true
	es.mu.Unlock()

	es.sendMu.Lock()
	_ = es.stream.Send(&runtimev1.ExecRequest{Frame: &runtimev1.ExecRequest_Cancel{Cancel: &runtimev1.ExecCancel{}}})
	_ = es.stream.CloseSend()
	es.sendMu.Unlock()

	select {
	case <-es.readerDone:
	case <-time.After(cleanupTimeout):
		es.cancel()
		select {
		case <-es.readerDone:
		case <-time.After(100 * time.Millisecond):
		}
	}
	es.cancel()
}
