package tyto

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMapRPCError(t *testing.T) {
	tests := []struct {
		name    string
		code    codes.Code
		details string
		opts    []ErrorOption
		want    any
	}{
		{name: "unauthenticated -> AuthenticationError", code: codes.Unauthenticated, details: "bad key", want: &AuthenticationError{}},
		{name: "invalid argument -> InvalidRequestError", code: codes.InvalidArgument, details: "bad field", want: &InvalidRequestError{}},
		{name: "not found -> SandboxNotFoundError", code: codes.NotFound, details: "missing", want: &SandboxNotFoundError{}},
		{name: "not found + filesystem -> RemoteFileNotFoundError", code: codes.NotFound, details: "missing", opts: []ErrorOption{WithFilesystemRPC()}, want: &RemoteFileNotFoundError{}},
		{name: "not found + session -> SessionNotFoundError", code: codes.NotFound, details: "missing", opts: []ErrorOption{WithSessionRPC()}, want: &SessionNotFoundError{}},
		{name: "already exists + filesystem -> RemoteFileExistsError", code: codes.AlreadyExists, details: "exists", opts: []ErrorOption{WithFilesystemRPC()}, want: &RemoteFileExistsError{}},
		{name: "already exists + session -> SessionExistsError", code: codes.AlreadyExists, details: "exists", opts: []ErrorOption{WithSessionRPC()}, want: &SessionExistsError{}},
		{name: "unavailable -> ConnectionError", code: codes.Unavailable, details: "down", want: &ConnectionError{}},
		{name: "unavailable + filesystem -> FilesystemError", code: codes.Unavailable, details: "down", opts: []ErrorOption{WithFilesystemRPC()}, want: &FilesystemError{}},
		{name: "deadline exceeded -> TimeoutError", code: codes.DeadlineExceeded, details: "slow", want: &TimeoutError{}},
		{name: "deadline exceeded + create -> SandboxCreationTimeoutError", code: codes.DeadlineExceeded, details: "slow", opts: []ErrorOption{WithCreate()}, want: &SandboxCreationTimeoutError{}},
		{name: "deadline exceeded + filesystem -> FilesystemError", code: codes.DeadlineExceeded, details: "slow", opts: []ErrorOption{WithFilesystemRPC()}, want: &FilesystemError{}},
		{name: "permission denied + exec -> CapabilityRejectedError", code: codes.PermissionDenied, details: "denied", opts: []ErrorOption{WithExecRPC()}, want: &CapabilityRejectedError{}},
		{name: "permission denied + session -> CapabilityRejectedError", code: codes.PermissionDenied, details: "denied", opts: []ErrorOption{WithSessionRPC()}, want: &CapabilityRejectedError{}},
		{name: "permission denied + filesystem capability rejection -> CapabilityRejectedError", code: codes.PermissionDenied, details: "filesystem capability rejected", opts: []ErrorOption{WithFilesystemRPC()}, want: &CapabilityRejectedError{}},
		{name: "permission denied + filesystem other -> FilesystemError", code: codes.PermissionDenied, details: "no access", opts: []ErrorOption{WithFilesystemRPC()}, want: &FilesystemError{}},
		{name: "failed precondition sandbox_deleted -> SandboxDeletedError", code: codes.FailedPrecondition, details: "sandbox_deleted: gone", want: &SandboxDeletedError{}},
		{name: "failed precondition sandbox_suspended -> SandboxSuspendedError", code: codes.FailedPrecondition, details: "sandbox_suspended: asleep", want: &SandboxSuspendedError{}},
		{name: "failed precondition sandbox_failed -> SandboxFailedError", code: codes.FailedPrecondition, details: "sandbox_failed: oops", want: &SandboxFailedError{}},
		{name: "failed precondition + create -> SandboxCreationFailedError", code: codes.FailedPrecondition, details: "cannot create", opts: []ErrorOption{WithCreate()}, want: &SandboxCreationFailedError{}},
		{name: "failed precondition + exec -> ServiceError", code: codes.FailedPrecondition, details: "exec broke", opts: []ErrorOption{WithExecRPC()}, want: &ServiceError{}},
		{name: "failed precondition cross_filesystem_move + filesystem -> CrossFilesystemMoveError", code: codes.FailedPrecondition, details: "cross_filesystem_move not allowed", opts: []ErrorOption{WithFilesystemRPC()}, want: &CrossFilesystemMoveError{}},
		{name: "aborted -> SandboxBusyError", code: codes.Aborted, details: "busy", want: &SandboxBusyError{}},
		{name: "aborted + filesystem -> FilesystemError", code: codes.Aborted, details: "busy", opts: []ErrorOption{WithFilesystemRPC()}, want: &FilesystemError{}},
		{name: "resource exhausted + filesystem -> FilesystemLimitError", code: codes.ResourceExhausted, details: "too big", opts: []ErrorOption{WithFilesystemRPC()}, want: &FilesystemLimitError{}},
		{name: "resource exhausted default -> ServiceError", code: codes.ResourceExhausted, details: "too big", want: &ServiceError{}},
		{name: "unknown filesystem catch-all -> FilesystemError", code: codes.Internal, details: "weird", opts: []ErrorOption{WithFilesystemRPC()}, want: &FilesystemError{}},
		{name: "unknown default -> ServiceError", code: codes.Internal, details: "weird", want: &ServiceError{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := status.Error(tt.code, tt.details)
			got := MapRPCError(err, nil, tt.opts...)
			if got == nil {
				t.Fatalf("MapRPCError() = nil, want %T", tt.want)
			}
			gotType := typeName(got)
			wantType := typeName(tt.want)
			if gotType != wantType {
				t.Errorf("MapRPCError() type = %s, want %s (err: %v)", gotType, wantType, got)
			}
		})
	}
}

func typeName(v any) string {
	switch v.(type) {
	case *AuthenticationError:
		return "AuthenticationError"
	case *InvalidRequestError:
		return "InvalidRequestError"
	case *SandboxNotFoundError:
		return "SandboxNotFoundError"
	case *RemoteFileNotFoundError:
		return "RemoteFileNotFoundError"
	case *SessionNotFoundError:
		return "SessionNotFoundError"
	case *RemoteFileExistsError:
		return "RemoteFileExistsError"
	case *SessionExistsError:
		return "SessionExistsError"
	case *ConnectionError:
		return "ConnectionError"
	case *FilesystemError:
		return "FilesystemError"
	case *TimeoutError:
		return "TimeoutError"
	case *SandboxCreationTimeoutError:
		return "SandboxCreationTimeoutError"
	case *CapabilityRejectedError:
		return "CapabilityRejectedError"
	case *SandboxDeletedError:
		return "SandboxDeletedError"
	case *SandboxSuspendedError:
		return "SandboxSuspendedError"
	case *SandboxFailedError:
		return "SandboxFailedError"
	case *SandboxCreationFailedError:
		return "SandboxCreationFailedError"
	case *ServiceError:
		return "ServiceError"
	case *CrossFilesystemMoveError:
		return "CrossFilesystemMoveError"
	case *SandboxBusyError:
		return "SandboxBusyError"
	case *FilesystemLimitError:
		return "FilesystemLimitError"
	default:
		return "unknown"
	}
}

func TestMapRPCErrorPassesThroughTypedErrors(t *testing.T) {
	original := &SandboxNotFoundError{BaseError{Msg: "already typed"}}
	got := MapRPCError(original, nil)
	if got != error(original) {
		t.Errorf("MapRPCError() should pass through an already-typed error unchanged, got %v", got)
	}
}

func TestMapRPCErrorNonGRPCError(t *testing.T) {
	got := MapRPCError(errors.New("boom"), nil)
	var svcErr *ServiceError
	if !errors.As(got, &svcErr) {
		t.Fatalf("MapRPCError(non-gRPC error) = %T, want *ServiceError", got)
	}
}

func TestIsRetryable(t *testing.T) {
	if !IsRetryable(status.Error(codes.Unavailable, "down")) {
		t.Error("IsRetryable(UNAVAILABLE) = false, want true")
	}
	if IsRetryable(status.Error(codes.InvalidArgument, "bad")) {
		t.Error("IsRetryable(INVALID_ARGUMENT) = true, want false")
	}
	if IsRetryable(errors.New("not a grpc error")) {
		t.Error("IsRetryable(non-gRPC error) = true, want false")
	}
}

func TestErrorsAsWorksThroughHierarchy(t *testing.T) {
	var err error = &RemoteFileNotFoundError{FilesystemError{BaseError{Msg: "missing"}}}
	var fsErr *FilesystemError
	if !errors.As(err, &fsErr) {
		t.Fatal("errors.As should find *FilesystemError in a *RemoteFileNotFoundError")
	}
	var notFound *RemoteFileNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatal("errors.As should find *RemoteFileNotFoundError directly")
	}
}
