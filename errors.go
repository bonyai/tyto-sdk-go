package tyto

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// BaseError carries the fields common to every typed SDK error.
type BaseError struct {
	Msg            string
	SandboxID      string
	OperationID    string
	IdempotencyKey string
}

// Error implements the error interface.
func (e *BaseError) Error() string { return e.Msg }

// Message returns the human-readable error message.
func (e *BaseError) Message() string { return e.Msg }

// bonyaError marks a type as a member of this SDK's typed error hierarchy,
// so MapRPCError can pass an already-typed error through unchanged.
func (e *BaseError) bonyaError() {}

// bonyaErrorMarker is implemented by every typed error in this package.
type bonyaErrorMarker interface {
	error
	bonyaError()
}

// AuthenticationError indicates an invalid or rejected API key.
type AuthenticationError struct{ BaseError }

// InvalidRequestError indicates invalid local arguments or an invalid service response.
type InvalidRequestError struct{ BaseError }

// SandboxNotFoundError indicates the sandbox is missing, deleted, or not visible to the API key.
type SandboxNotFoundError struct{ BaseError }

// SandboxDeletedError indicates the operation cannot run because the sandbox is deleted.
type SandboxDeletedError struct{ BaseError }

// SandboxSuspendedError indicates the operation reported a suspended sandbox.
type SandboxSuspendedError struct{ BaseError }

// SandboxBusyError indicates the service rejected a lifecycle operation as busy.
type SandboxBusyError struct{ BaseError }

// SandboxFailedError indicates the operation cannot run because the sandbox failed.
type SandboxFailedError struct{ BaseError }

// SandboxCreationFailedError indicates create reached a failed terminal state.
type SandboxCreationFailedError struct{ BaseError }

// SandboxCreationTimeoutError indicates the create deadline expired.
type SandboxCreationTimeoutError struct{ BaseError }

// CapabilityRejectedError indicates the guest capability was rejected and could not be refreshed.
type CapabilityRejectedError struct{ BaseError }

// SessionExistsError indicates Sessions.Create targeted a name that already has a
// record and either Replace was not set or the record is not terminal.
type SessionExistsError struct{ BaseError }

// SessionNotFoundError indicates Sessions.Attach or Sessions.Kill named a session that does not exist.
type SessionNotFoundError struct{ BaseError }

// FilesystemError is a general filesystem failure.
type FilesystemError struct{ BaseError }

// RemoteFileNotFoundError indicates a remote file or directory is missing.
type RemoteFileNotFoundError struct{ FilesystemError }

// Unwrap lets errors.As/errors.Is match *FilesystemError against a *RemoteFileNotFoundError.
func (e *RemoteFileNotFoundError) Unwrap() error { return &e.FilesystemError }

// RemoteFileExistsError indicates a remote destination already exists.
type RemoteFileExistsError struct{ FilesystemError }

// Unwrap lets errors.As/errors.Is match *FilesystemError against a *RemoteFileExistsError.
func (e *RemoteFileExistsError) Unwrap() error { return &e.FilesystemError }

// CrossFilesystemMoveError indicates a remote move crossed filesystems.
type CrossFilesystemMoveError struct{ FilesystemError }

// Unwrap lets errors.As/errors.Is match *FilesystemError against a *CrossFilesystemMoveError.
func (e *CrossFilesystemMoveError) Unwrap() error { return &e.FilesystemError }

// FilesystemLimitError indicates a client or service filesystem size/frame limit was hit.
type FilesystemLimitError struct{ FilesystemError }

// Unwrap lets errors.As/errors.Is match *FilesystemError against a *FilesystemLimitError.
func (e *FilesystemLimitError) Unwrap() error { return &e.FilesystemError }

// ExecFailedError indicates ExecResult.Check saw a non-OK result.
type ExecFailedError struct {
	BaseError
	Result *ExecResult
}

// TimeoutError indicates the operation deadline expired.
type TimeoutError struct{ BaseError }

// ConnectionError indicates a retryable transport failure exhausted its retries.
type ConnectionError struct{ BaseError }

// ServiceError is a service or unexpected transport failure not covered by another type.
type ServiceError struct{ BaseError }

func (e *ExecFailedError) Error() string { return e.Msg }

// errorOptions configures how a mapped error's identity fields are populated.
type errorOptions struct {
	sandboxID      string
	operationID    string
	idempotencyKey string
	create         bool
	execRPC        bool
	filesystemRPC  bool
	sessionRPC     bool
}

// ErrorOption customizes MapRPCError's behavior.
type ErrorOption func(*errorOptions)

// WithSandboxID attaches a sandbox ID to a mapped error.
func WithSandboxID(id string) ErrorOption { return func(o *errorOptions) { o.sandboxID = id } }

// WithOperationID attaches an operation ID to a mapped error.
func WithOperationID(id string) ErrorOption { return func(o *errorOptions) { o.operationID = id } }

// WithIdempotencyKey attaches an idempotency key to a mapped error.
func WithIdempotencyKey(key string) ErrorOption {
	return func(o *errorOptions) { o.idempotencyKey = key }
}

// WithCreate marks the RPC being mapped as a sandbox create call.
func WithCreate() ErrorOption { return func(o *errorOptions) { o.create = true } }

// WithExecRPC marks the RPC being mapped as an Exec call.
func WithExecRPC() ErrorOption { return func(o *errorOptions) { o.execRPC = true } }

// WithFilesystemRPC marks the RPC being mapped as a filesystem call.
func WithFilesystemRPC() ErrorOption { return func(o *errorOptions) { o.filesystemRPC = true } }

// WithSessionRPC marks the RPC being mapped as a managed-session call.
func WithSessionRPC() ErrorOption { return func(o *errorOptions) { o.sessionRPC = true } }

var filesystemCapabilityRejectionMessages = map[string]bool{
	"filesystem capability rejected":                 true,
	"filesystem capability sandbox binding rejected": true,
}

// IsRetryable reports whether err is a transport failure the SDK retries
// automatically -- currently gRPC UNAVAILABLE only.
func IsRetryable(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	return st.Code() == codes.Unavailable
}

// MapRPCError converts a gRPC (or other) error into a typed BonyaError,
// applying the same code/detail mapping rules as the reference SDKs.
func MapRPCError(err error, secrets []string, opts ...ErrorOption) error {
	if err == nil {
		return nil
	}
	var already bonyaErrorMarker
	if errors.As(err, &already) {
		return err
	}

	var cfg errorOptions
	for _, opt := range opts {
		opt(&cfg)
	}

	st, ok := status.FromError(err)
	if !ok {
		return &ServiceError{BaseError{
			Msg:            sanitizeMessage(err.Error(), secrets),
			SandboxID:      cfg.sandboxID,
			OperationID:    cfg.operationID,
			IdempotencyKey: cfg.idempotencyKey,
		}}
	}

	code := st.Code()
	rawDetails := st.Message()
	if rawDetails == "" {
		rawDetails = code.String()
	}
	details := sanitizeMessage(rawDetails, secrets)

	base := func() BaseError {
		return BaseError{Msg: details, SandboxID: cfg.sandboxID, OperationID: cfg.operationID}
	}
	baseWithKey := func() BaseError {
		return BaseError{
			Msg:            details,
			SandboxID:      cfg.sandboxID,
			OperationID:    cfg.operationID,
			IdempotencyKey: cfg.idempotencyKey,
		}
	}

	switch {
	case cfg.filesystemRPC && code == codes.DeadlineExceeded:
		return &FilesystemError{base()}
	case cfg.filesystemRPC && code == codes.Unavailable:
		return &FilesystemError{base()}
	case code == codes.DeadlineExceeded:
		if cfg.create {
			return &SandboxCreationTimeoutError{baseWithKey()}
		}
		return &TimeoutError{baseWithKey()}
	case code == codes.Unavailable:
		return &ConnectionError{baseWithKey()}
	case code == codes.Unauthenticated:
		return &AuthenticationError{base()}
	case code == codes.InvalidArgument:
		return &InvalidRequestError{base()}
	case code == codes.NotFound && cfg.filesystemRPC:
		return &RemoteFileNotFoundError{FilesystemError{base()}}
	case code == codes.NotFound && cfg.sessionRPC:
		return &SessionNotFoundError{base()}
	case code == codes.NotFound:
		return &SandboxNotFoundError{base()}
	case code == codes.AlreadyExists && cfg.filesystemRPC:
		return &RemoteFileExistsError{FilesystemError{base()}}
	case code == codes.AlreadyExists && cfg.sessionRPC:
		return &SessionExistsError{base()}
	case code == codes.PermissionDenied && cfg.execRPC:
		return &CapabilityRejectedError{BaseError{
			Msg:         "exec capability was rejected; capability refresh/reconnect is unavailable in this SDK version",
			SandboxID:   cfg.sandboxID,
			OperationID: cfg.operationID,
		}}
	case code == codes.PermissionDenied && cfg.filesystemRPC && filesystemCapabilityRejectionMessages[details]:
		return &CapabilityRejectedError{base()}
	case code == codes.PermissionDenied && cfg.filesystemRPC:
		return &FilesystemError{base()}
	case code == codes.PermissionDenied && cfg.sessionRPC:
		return &CapabilityRejectedError{base()}
	case code == codes.FailedPrecondition && strings.Contains(details, "sandbox_deleted"):
		return &SandboxDeletedError{base()}
	case code == codes.FailedPrecondition && strings.Contains(details, "sandbox_suspended"):
		return &SandboxSuspendedError{base()}
	case code == codes.FailedPrecondition && strings.Contains(details, "sandbox_failed"):
		return &SandboxFailedError{base()}
	case code == codes.Aborted && cfg.filesystemRPC:
		return &FilesystemError{baseWithKey()}
	case code == codes.Aborted:
		return &SandboxBusyError{baseWithKey()}
	case code == codes.FailedPrecondition && cfg.create:
		return &SandboxCreationFailedError{baseWithKey()}
	case code == codes.FailedPrecondition && cfg.execRPC:
		return &ServiceError{base()}
	case code == codes.FailedPrecondition && cfg.filesystemRPC && strings.Contains(details, "cross_filesystem_move"):
		return &CrossFilesystemMoveError{FilesystemError{base()}}
	case code == codes.ResourceExhausted && cfg.filesystemRPC:
		return &FilesystemLimitError{FilesystemError{base()}}
	case cfg.filesystemRPC:
		return &FilesystemError{base()}
	default:
		return &ServiceError{baseWithKey()}
	}
}

// errFmt is a small helper retained for readability at call sites that build
// ad hoc InvalidRequestErrors.
func errFmt(format string, args ...any) error {
	return &InvalidRequestError{BaseError{Msg: fmt.Sprintf(format, args...)}}
}
