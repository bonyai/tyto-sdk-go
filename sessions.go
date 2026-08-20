package tyto

import (
	"context"
	"time"

	"google.golang.org/grpc/metadata"

	runtimev1grpc "buf.build/gen/go/bonya/tyto/grpc/go/tyto/runtime/v1/runtimev1grpc"
	runtimev1 "buf.build/gen/go/bonya/tyto/protocolbuffers/go/tyto/runtime/v1"
)

// SandboxSessions is the managed console session RPC surface: persistent,
// guest-owned command sessions that outlive the client connection.
//
// Capability refresh: an UNAUTHENTICATED rejection (an expired token)
// transparently calls Sandbox.ReissueCapability and retries exactly once, at
// admission time only, never mid-stream. PERMISSION_DENIED never triggers a
// refresh.
type SandboxSessions struct {
	sandbox *Sandbox
}

// CreateSessionOptions configures SandboxSessions.Create.
type CreateSessionOptions struct {
	Env  map[string]string
	Cwd  string
	Cols int
	Rows int
	// Replace, if true, replaces an existing terminal (exited/killed/failed)
	// record instead of returning *SessionExistsError. A running or attached
	// session is never replaced regardless of this flag.
	Replace bool
}

// Create starts a named TTY session.
func (s *SandboxSessions) Create(ctx context.Context, name string, command []string, opts ...CreateSessionOptions) (SessionInfo, error) {
	name, err := validateSessionName(name)
	if err != nil {
		return SessionInfo{}, err
	}
	argv, err := validateSessionCommand(command)
	if err != nil {
		return SessionInfo{}, err
	}
	var o CreateSessionOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	env, err := normalizeEnv(o.Env)
	if err != nil {
		return SessionInfo{}, err
	}
	cwd, err := normalizeCwd(o.Cwd)
	if err != nil {
		return SessionInfo{}, err
	}
	cols, err := validateSessionDimension("cols", o.Cols)
	if err != nil {
		return SessionInfo{}, err
	}
	rows, err := validateSessionDimension("rows", o.Rows)
	if err != nil {
		return SessionInfo{}, err
	}

	var result SessionInfo
	err = s.withCapabilityRefresh(ctx, func(ctx context.Context) error {
		client, callCtx, cancel, err := s.call(ctx)
		if err != nil {
			return err
		}
		defer cancel()
		resp, err := client.CreateSession(callCtx, &runtimev1.CreateSessionRequest{
			Name:       name,
			Command:    argv,
			Env:        env,
			WorkingDir: cwd,
			Cols:       uint32(cols),
			Rows:       uint32(rows),
			Replace:    o.Replace,
		})
		if err != nil {
			return s.mapError(err)
		}
		result = sessionInfoFromProto(resp.GetSession())
		return nil
	})
	return result, err
}

// List lists sessions. This works on a suspended sandbox without waking it:
// the result's SandboxSuspended is true when served from the suspend-time
// snapshot rather than the live guest.
func (s *SandboxSessions) List(ctx context.Context) (SessionList, error) {
	var result SessionList
	err := s.withCapabilityRefresh(ctx, func(ctx context.Context) error {
		client, callCtx, cancel, err := s.call(ctx)
		if err != nil {
			return err
		}
		defer cancel()
		resp, err := client.ListSessions(callCtx, &runtimev1.ListSessionsRequest{})
		if err != nil {
			return s.mapError(err)
		}
		infos := make([]SessionInfo, 0, len(resp.GetSessions()))
		for _, info := range resp.GetSessions() {
			infos = append(infos, sessionInfoFromProto(info))
		}
		result = SessionList{Sessions: infos, SandboxSuspended: resp.GetSandboxSuspended()}
		return nil
	})
	return result, err
}

// KillOptions configures SandboxSessions.Kill.
type KillOptions struct {
	// Signal defaults to "TERM" when empty.
	Signal string
	// GraceMS defaults to 5000 when 0: signal, wait, then SIGKILL the
	// process group if it has not exited.
	GraceMS int
}

// Kill signals a session's process group, escalating to SIGKILL after the
// grace period if it has not exited.
func (s *SandboxSessions) Kill(ctx context.Context, name string, opts ...KillOptions) (SessionInfo, error) {
	name, err := validateSessionName(name)
	if err != nil {
		return SessionInfo{}, err
	}
	var o KillOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	signal := o.Signal
	if signal == "" {
		signal = "TERM"
	}
	if o.GraceMS < 0 {
		return SessionInfo{}, &InvalidRequestError{BaseError{Msg: "grace_ms must be a non-negative integer"}}
	}

	var result SessionInfo
	err = s.withCapabilityRefresh(ctx, func(ctx context.Context) error {
		client, callCtx, cancel, err := s.call(ctx)
		if err != nil {
			return err
		}
		defer cancel()
		resp, err := client.KillSession(callCtx, &runtimev1.KillSessionRequest{
			Name:    name,
			Signal:  signal,
			GraceMs: uint32(o.GraceMS),
		})
		if err != nil {
			return s.mapError(err)
		}
		result = sessionInfoFromProto(resp.GetSession())
		return nil
	})
	return result, err
}

// AttachOptions configures SandboxSessions.Attach.
type AttachOptions struct {
	Cols           int
	Rows           int
	MaxReplayBytes int
}

// Attach attaches to a session by name, replaying bounded output produced
// while detached. A second attach preempts an existing one: the loser's
// stream ends with a SessionEnded(TAKEOVER) event.
func (s *SandboxSessions) Attach(ctx context.Context, name string, opts ...AttachOptions) (*SessionStream, error) {
	name, err := validateSessionName(name)
	if err != nil {
		return nil, err
	}
	var o AttachOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	cols, err := validateSessionDimension("cols", o.Cols)
	if err != nil {
		return nil, err
	}
	rows, err := validateSessionDimension("rows", o.Rows)
	if err != nil {
		return nil, err
	}
	if o.MaxReplayBytes < 0 {
		return nil, &InvalidRequestError{BaseError{Msg: "max_replay_bytes must be a non-negative integer"}}
	}
	if err := s.ensureSessionsAllowed(); err != nil {
		return nil, err
	}

	stream, err := openSessionStream(ctx, s.sandbox, name, cols, rows, o.MaxReplayBytes)
	if err != nil {
		var authErr *AuthenticationError
		if !asAuthenticationError(err, &authErr) {
			return nil, err
		}
		if refreshErr := s.sandbox.ReissueCapability(ctx); refreshErr != nil {
			return nil, refreshErr
		}
		return openSessionStream(ctx, s.sandbox, name, cols, rows, o.MaxReplayBytes)
	}
	return stream, nil
}

func asAuthenticationError(err error, target **AuthenticationError) bool {
	if e, ok := err.(*AuthenticationError); ok {
		*target = e
		return true
	}
	return false
}

func (s *SandboxSessions) withCapabilityRefresh(ctx context.Context, call func(context.Context) error) error {
	if err := s.ensureSessionsAllowed(); err != nil {
		return err
	}
	err := call(ctx)
	if err == nil {
		return nil
	}
	var authErr *AuthenticationError
	if !asAuthenticationError(err, &authErr) {
		return err
	}
	if refreshErr := s.sandbox.ReissueCapability(ctx); refreshErr != nil {
		return refreshErr
	}
	return call(ctx)
}

func (s *SandboxSessions) ensureSessionsAllowed() error {
	if s.sandbox.isDeleted() {
		return &SandboxDeletedError{BaseError{Msg: "sandbox has been deleted", SandboxID: s.sandbox.ID, OperationID: s.sandbox.OperationID}}
	}
	if s.sandbox.LastObservedStatus == StatusFailed {
		return s.sandbox.failedError()
	}
	return nil
}

func (s *SandboxSessions) call(ctx context.Context) (runtimev1grpc.GuestServiceClient, context.Context, context.CancelFunc, error) {
	execEndpoint, capability := s.sandbox.snapshotState()
	client, err := s.sandbox.client.guestClient(execEndpoint)
	if err != nil {
		return nil, nil, nil, err
	}
	remaining, err := startDeadlineRemaining(s.sandbox.client.timeout)
	if err != nil {
		return nil, nil, nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, remaining)
	callCtx = metadata.AppendToOutgoingContext(callCtx, "bonya-sandbox-id", s.sandbox.ID, "bonya-exec-capability", capability)
	return client, callCtx, cancel, nil
}

func (s *SandboxSessions) mapError(err error) error {
	_, capability := s.sandbox.snapshotState()
	return MapRPCError(err, s.sandbox.client.secrets(capability), WithSandboxID(s.sandbox.ID), WithOperationID(s.sandbox.OperationID), WithSessionRPC())
}

func sessionInfoFromProto(info *runtimev1.SessionInfo) SessionInfo {
	var endedAt time.Time
	if nanos := info.GetEndedAtUnixNanos(); nanos != 0 {
		endedAt = time.Unix(0, nanos).UTC()
	}
	var exit *Exit
	if e := info.GetExit(); e != nil {
		exit = &Exit{ExitCode: int(e.GetExitCode()), Signaled: e.GetSignaled(), Signal: int(e.GetSignal())}
	}
	return SessionInfo{
		Name:           info.GetName(),
		Command:        append([]string(nil), info.GetCommand()...),
		WorkingDir:     info.GetWorkingDir(),
		Status:         sessionStatusFromProto(info.GetStatus()),
		Attached:       info.GetAttached(),
		StartedAt:      time.Unix(0, info.GetStartedAtUnixNanos()).UTC(),
		LastActivityAt: time.Unix(0, info.GetLastActivityUnixNanos()).UTC(),
		EndedAt:        endedAt,
		Exit:           exit,
	}
}

func sessionStatusFromProto(value runtimev1.SessionStatus) SessionStatus {
	switch value {
	case runtimev1.SessionStatus_SESSION_STATUS_STARTING:
		return SessionStatusStarting
	case runtimev1.SessionStatus_SESSION_STATUS_IDLE:
		return SessionStatusIdle
	case runtimev1.SessionStatus_SESSION_STATUS_ATTACHED:
		return SessionStatusAttached
	case runtimev1.SessionStatus_SESSION_STATUS_EXITED:
		return SessionStatusExited
	case runtimev1.SessionStatus_SESSION_STATUS_KILLED:
		return SessionStatusKilled
	case runtimev1.SessionStatus_SESSION_STATUS_FAILED:
		return SessionStatusFailed
	default:
		return SessionStatusUnspecified
	}
}

func sessionEndedReasonFromProto(value runtimev1.AttachEnded_Reason) SessionEndedReason {
	switch value {
	case runtimev1.AttachEnded_REASON_DETACHED:
		return SessionEndedReasonDetached
	case runtimev1.AttachEnded_REASON_TAKEOVER:
		return SessionEndedReasonTakeover
	default:
		return SessionEndedReasonUnspecified
	}
}
