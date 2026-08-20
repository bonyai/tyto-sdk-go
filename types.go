package tyto

import "time"

// Status is the last-observed lifecycle status of a sandbox.
type Status string

const (
	StatusCreating   Status = "creating"
	StatusRunning    Status = "running"
	StatusSuspending Status = "suspending"
	StatusSuspended  Status = "suspended"
	StatusResuming   Status = "resuming"
	StatusFailed     Status = "failed"
	StatusDeleted    Status = "deleted"
)

// String implements fmt.Stringer.
func (s Status) String() string { return string(s) }

// Wait controls when Sandboxes.Create returns.
type Wait string

const (
	// WaitReady asks the service to return once the sandbox is running.
	WaitReady Wait = "ready"
	// WaitNone returns as soon as the service accepts the create request.
	WaitNone Wait = "none"
)

// String implements fmt.Stringer.
func (w Wait) String() string { return string(w) }

// FileKind classifies a remote filesystem entry.
type FileKind string

const (
	FileKindFile      FileKind = "file"
	FileKindDirectory FileKind = "directory"
	FileKindSymlink   FileKind = "symlink"
	FileKindOther     FileKind = "other"
)

// String implements fmt.Stringer.
func (k FileKind) String() string { return string(k) }

// SessionStatus is the lifecycle status of a managed console session.
type SessionStatus string

const (
	SessionStatusUnspecified SessionStatus = "unspecified"
	SessionStatusStarting    SessionStatus = "starting"
	SessionStatusIdle        SessionStatus = "idle"
	SessionStatusAttached    SessionStatus = "attached"
	SessionStatusExited      SessionStatus = "exited"
	SessionStatusKilled      SessionStatus = "killed"
	SessionStatusFailed      SessionStatus = "failed"
)

// String implements fmt.Stringer.
func (s SessionStatus) String() string { return string(s) }

// SessionEndedReason explains why a SessionStream ended without the process exiting.
type SessionEndedReason string

const (
	SessionEndedReasonUnspecified SessionEndedReason = "unspecified"
	SessionEndedReasonDetached    SessionEndedReason = "detached"
	SessionEndedReasonTakeover    SessionEndedReason = "takeover"
)

// String implements fmt.Stringer.
func (r SessionEndedReason) String() string { return string(r) }

// PreviewAuth controls how a preview URL admits a request.
type PreviewAuth string

const (
	// PreviewAuthToken means the sandbox's data-plane capability admits the request.
	PreviewAuthToken PreviewAuth = "token"
	// PreviewAuthPublic means no authentication: anyone holding the URL reaches the service.
	PreviewAuthPublic PreviewAuth = "public"
)

// String implements fmt.Stringer.
func (a PreviewAuth) String() string { return string(a) }

// Stdout is a chunk of standard-output bytes from an Exec or session stream.
type Stdout struct {
	Data []byte
}

// Stderr is a chunk of standard-error bytes from an Exec stream.
type Stderr struct {
	Data []byte
}

// Exit reports that a process has exited.
type Exit struct {
	ExitCode int
	Signaled bool
	Signal   int
}

// OK reports whether the process exited cleanly with status 0.
func (e Exit) OK() bool { return e.ExitCode == 0 && !e.Signaled }

// SessionEnded reports that a SessionStream attach ended without the process exiting.
type SessionEnded struct {
	Reason SessionEndedReason
}

// SessionOutputDropped reports that live session output was dropped because the
// client was reading too slowly. It does not end the attach.
type SessionOutputDropped struct {
	DroppedBytes int
}

// FileInfo is lstat-style metadata about a remote filesystem entry.
type FileInfo struct {
	Path       string
	Name       string
	Kind       FileKind
	Size       uint64
	Mode       uint32
	ModifiedAt time.Time
}

// SessionInfo describes a managed console session.
type SessionInfo struct {
	Name           string
	Command        []string
	WorkingDir     string
	Status         SessionStatus
	Attached       bool
	StartedAt      time.Time
	LastActivityAt time.Time
	// EndedAt is the zero time while the session is running.
	EndedAt time.Time
	// Exit is non-nil only once the session has reached a terminal status.
	Exit *Exit
}

// SessionList is the result of SandboxSessions.List.
type SessionList struct {
	Sessions []SessionInfo
	// SandboxSuspended is true when this result was served from the
	// suspend-time snapshot rather than the live guest.
	SandboxSuspended bool
}

// Preview is a published preview URL for one guest port.
type Preview struct {
	ID        string
	SandboxID string
	Port      int
	Auth      PreviewAuth
	Name      string
	URL       string
	CreatedAt time.Time
}

// Organization is one organization the caller's API key's user belongs to.
type Organization struct {
	ID   string
	Name string
	// Personal marks the deterministic tenant an omitted organization
	// context resolves to. Every account has exactly one.
	Personal bool
	// Role is the caller's role in this organization: "owner" or "member".
	Role      string
	CreatedAt time.Time
}

// Template is one template_id/version binding the deployment's catalog
// offers to Sandboxes.Create and RunJob.
//
// One entry per version, not one per template_id: a template_id with
// several published versions appears once per version, and IsDefault marks
// the one a caller resolving by template_id alone (version omitted) gets.
type Template struct {
	ID        string
	Version   string
	Digest    string
	IsDefault bool
	Metadata  TemplateMetadata
}

// TemplateMetadata describes the operating system and tools preinstalled in
// a Template. A catalog entry without annotations yields its zero value.
type TemplateMetadata struct {
	Description     string
	OS              string
	OSVersion       string
	Stacks          []TemplateStack
	AgentCLISupport []string
}

// TemplateStack is one language or runtime toolchain preinstalled in a
// template.
type TemplateStack struct {
	Name    string
	Version string
}

// DeleteResult is the result of Sandbox.Delete.
type DeleteResult struct {
	SandboxID      string
	AlreadyDeleted bool
}

// ResumeResult is the result of Sandbox.Resume.
type ResumeResult struct {
	SandboxID            string
	LifecycleOperationID string
	AlreadyRunning       bool
}

// SandboxSummary is metadata-only information about a sandbox returned by List.
// It cannot run Exec; call Sandboxes.Get(summary.ID) for a usable handle.
type SandboxSummary struct {
	ID                 string
	OperationID        string
	Template           string
	Version            string
	LastObservedStatus Status
	FailureCode        string
	FailureMessage     string
	Name               string
}
