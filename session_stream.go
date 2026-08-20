package tyto

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc/metadata"

	runtimev1grpc "buf.build/gen/go/bonya/tyto/grpc/go/tyto/runtime/v1/runtimev1grpc"
	runtimev1 "buf.build/gen/go/bonya/tyto/protocolbuffers/go/tyto/runtime/v1"
)

// SessionStream is a live attach to a managed session. It mirrors
// ExecSession's streaming mechanics (background reader goroutine, bounded
// event channel, Next/Write/Resize/Close) but proxies AttachSession instead
// of Exec: the constructor blocks for the first (accepted) frame, so Info,
// ReplayedBytes, and HistoryDropped are available immediately, before any
// call to Next.
type SessionStream struct {
	sandboxID string
	name      string

	Info           SessionInfo
	ReplayedBytes  int
	HistoryDropped bool
	Cols           int
	Rows           int

	stream     runtimev1grpc.GuestService_AttachSessionClient
	cancel     context.CancelFunc
	sendMu     sync.Mutex
	events     chan sessionEvent
	readerDone chan struct{}
	readerOnce sync.Once

	cleanupTimeout time.Duration

	mu     sync.Mutex
	closed bool
}

type sessionEvent struct {
	value any // Stdout, Exit, SessionEnded, or SessionOutputDropped
	err   error
	end   bool
}

func openSessionStream(ctx context.Context, sandbox *Sandbox, name string, cols, rows, maxReplayBytes int) (*SessionStream, error) {
	execEndpoint, capability := sandbox.snapshotState()
	guestClient, err := sandbox.client.guestClient(execEndpoint)
	if err != nil {
		return nil, err
	}

	cleanupTimeout := sandbox.client.timeout
	if cleanupTimeout > 5*time.Second {
		cleanupTimeout = 5 * time.Second
	}
	if cleanupTimeout < 500*time.Millisecond {
		cleanupTimeout = 500 * time.Millisecond
	}

	// A managed-session attach is a live terminal, not a bounded operation.
	// Applying Client.timeout here used to terminate healthy CLI consoles after
	// the default 30 seconds. The caller's context still controls cancellation;
	// callers that need a deadline can supply one explicitly.
	streamCtx, cancel := context.WithCancel(ctx)
	streamCtx = metadata.AppendToOutgoingContext(streamCtx, "bonya-sandbox-id", sandbox.ID, "bonya-exec-capability", capability)
	grpcStream, err := guestClient.AttachSession(streamCtx)
	if err != nil {
		cancel()
		return nil, mapSessionError(sandbox, capability, err)
	}

	start := &runtimev1.AttachSessionRequest{
		Frame: &runtimev1.AttachSessionRequest_Start{
			Start: &runtimev1.AttachStart{
				Name:           name,
				Cols:           uint32(cols),
				Rows:           uint32(rows),
				MaxReplayBytes: uint32(maxReplayBytes),
			},
		},
	}
	if err := grpcStream.Send(start); err != nil {
		cancel()
		return nil, mapSessionError(sandbox, capability, err)
	}

	first, err := grpcStream.Recv()
	if err != nil {
		cancel()
		return nil, mapSessionError(sandbox, capability, err)
	}
	accepted, ok := first.GetFrame().(*runtimev1.AttachSessionResponse_Accepted)
	if !ok {
		cancel()
		return nil, &InvalidRequestError{BaseError{Msg: "AttachSession response did not begin with an accepted frame", SandboxID: sandbox.ID}}
	}

	s := &SessionStream{
		sandboxID:      sandbox.ID,
		name:           name,
		Info:           sessionInfoFromProto(accepted.Accepted.GetSession()),
		ReplayedBytes:  int(accepted.Accepted.GetReplayedBytes()),
		HistoryDropped: accepted.Accepted.GetHistoryDropped(),
		Cols:           int(accepted.Accepted.GetCols()),
		Rows:           int(accepted.Accepted.GetRows()),
		stream:         grpcStream,
		cancel:         cancel,
		events:         make(chan sessionEvent, 16),
		readerDone:     make(chan struct{}),
		cleanupTimeout: cleanupTimeout,
	}
	return s, nil
}

func mapSessionError(sandbox *Sandbox, capability string, err error) error {
	return MapRPCError(err, sandbox.client.secrets(capability), WithSandboxID(sandbox.ID), WithSessionRPC())
}

func (s *SessionStream) ensureReader() {
	s.readerOnce.Do(func() {
		go s.readLoop()
	})
}

func (s *SessionStream) readLoop() {
	defer close(s.readerDone)
	for {
		resp, err := s.stream.Recv()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				s.emit(sessionEvent{end: true})
				return
			}
			s.emit(sessionEvent{err: &ServiceError{BaseError{Msg: sanitizeMessage(err.Error(), nil), SandboxID: s.sandboxID}}})
			return
		}
		event, terminal, evErr := sessionResponseToEvent(resp, s.sandboxID)
		if evErr != nil {
			s.emit(sessionEvent{err: evErr})
			return
		}
		s.emit(event)
		if terminal {
			s.emit(sessionEvent{end: true})
			return
		}
	}
}

func sessionResponseToEvent(resp *runtimev1.AttachSessionResponse, sandboxID string) (sessionEvent, bool, error) {
	switch frame := resp.GetFrame().(type) {
	case *runtimev1.AttachSessionResponse_Output:
		return sessionEvent{value: Stdout{Data: frame.Output.GetData()}}, false, nil
	case *runtimev1.AttachSessionResponse_Exit:
		return sessionEvent{value: Exit{
			ExitCode: int(frame.Exit.GetExitCode()),
			Signaled: frame.Exit.GetSignaled(),
			Signal:   int(frame.Exit.GetSignal()),
		}}, true, nil
	case *runtimev1.AttachSessionResponse_Ended:
		return sessionEvent{value: SessionEnded{Reason: sessionEndedReasonFromProto(frame.Ended.GetReason())}}, true, nil
	case *runtimev1.AttachSessionResponse_OutputDropped:
		return sessionEvent{value: SessionOutputDropped{DroppedBytes: int(frame.OutputDropped.GetDroppedBytes())}}, false, nil
	default:
		return sessionEvent{}, false, &InvalidRequestError{BaseError{Msg: "AttachSession response contained no frame", SandboxID: sandboxID}}
	}
}

func (s *SessionStream) emit(e sessionEvent) {
	select {
	case s.events <- e:
	case <-time.After(5 * time.Second):
	}
}

// Next blocks for the next event: Stdout, Exit, SessionEnded, or
// SessionOutputDropped. It returns (nil, nil) once the stream has ended
// cleanly after an Exit or SessionEnded event. A live attach has no SDK
// deadline; pass a deadline on the context supplied to Attach when needed.
func (s *SessionStream) Next() (any, error) {
	s.ensureReader()
	ev := <-s.events
	if ev.end {
		return nil, nil
	}
	if ev.err != nil {
		return nil, ev.err
	}
	return ev.value, nil
}

// Write sends stdin bytes.
func (s *SessionStream) Write(data []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return &InvalidRequestError{BaseError{Msg: "session is closed", SandboxID: s.sandboxID}}
	}
	s.mu.Unlock()
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	return s.stream.Send(&runtimev1.AttachSessionRequest{
		Frame: &runtimev1.AttachSessionRequest_Stdin{Stdin: &runtimev1.StdinData{Data: data}},
	})
}

// Resize changes the TTY dimensions of the attached session.
func (s *SessionStream) Resize(cols, rows int) error {
	c, err := validateDimension("cols", cols)
	if err != nil {
		return err
	}
	r, err := validateDimension("rows", rows)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return &InvalidRequestError{BaseError{Msg: "session is closed", SandboxID: s.sandboxID}}
	}
	s.mu.Unlock()
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	return s.stream.Send(&runtimev1.AttachSessionRequest{
		Frame: &runtimev1.AttachSessionRequest_Resize{Resize: &runtimev1.ExecResize{Cols: uint32(c), Rows: uint32(r)}},
	})
}

// Detach ends the attach gracefully without touching the process. It is
// idempotent.
func (s *SessionStream) Detach() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	s.sendMu.Lock()
	_ = s.stream.Send(&runtimev1.AttachSessionRequest{Frame: &runtimev1.AttachSessionRequest_Detach{Detach: &runtimev1.AttachDetach{}}})
	_ = s.stream.CloseSend()
	s.sendMu.Unlock()

	select {
	case <-s.readerDone:
	case <-time.After(s.cleanupTimeout):
		s.cancel()
		select {
		case <-s.readerDone:
		case <-time.After(100 * time.Millisecond):
		}
	}
	s.cancel()
}

// Close calls Detach if the stream is still open. Closing an unfinished
// session detaches the guest process rather than killing it; the process
// keeps running.
func (s *SessionStream) Close() {
	s.Detach()
}
