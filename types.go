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

// SessionList is the result of Sandbox.ListSessions.
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
	CreatedAt          time.Time
}

// Disposition controls what happens to a sandbox a job created once the run
// ends, however it ends. Not meaningful for a job given an existing sandbox.
type Disposition string

const (
	// DispositionDelete deletes a job-created sandbox once the run ends. This is the default.
	DispositionDelete Disposition = "delete"
	// DispositionKeep leaves a job-created sandbox in place once the run ends.
	DispositionKeep Disposition = "keep"
)

// String implements fmt.Stringer.
func (d Disposition) String() string { return string(d) }

// JobRunStatus is the lifecycle status of a job run.
type JobRunStatus string

const (
	JobRunStatusRunning    JobRunStatus = "running"
	JobRunStatusCompleted  JobRunStatus = "completed"
	JobRunStatusFailed     JobRunStatus = "failed"
	JobRunStatusCanceled   JobRunStatus = "canceled"
	JobRunStatusTimedOut   JobRunStatus = "timed_out"
	JobRunStatusTerminated JobRunStatus = "terminated"
)

// String implements fmt.Stringer.
func (s JobRunStatus) String() string { return string(s) }

// JobRunAction is something the caller may do to a job run right now,
// computed server-side. A caller must not re-derive these from Status alone.
type JobRunAction string

const (
	JobRunActionCancel        JobRunAction = "cancel"
	JobRunActionRerun         JobRunAction = "rerun"
	JobRunActionRetry         JobRunAction = "retry"
	JobRunActionDelete        JobRunAction = "delete"
	JobRunActionDeleteSandbox JobRunAction = "delete_sandbox"
)

// String implements fmt.Stringer.
func (a JobRunAction) String() string { return string(a) }

// JobRunTimelineStatus is the status of one JobRunTimelineEntry.
type JobRunTimelineStatus string

const (
	JobRunTimelineStatusScheduled JobRunTimelineStatus = "scheduled"
	JobRunTimelineStatusRunning   JobRunTimelineStatus = "running"
	JobRunTimelineStatusCompleted JobRunTimelineStatus = "completed"
	JobRunTimelineStatusFailed    JobRunTimelineStatus = "failed"
	JobRunTimelineStatusCanceled  JobRunTimelineStatus = "canceled"
)

// String implements fmt.Stringer.
func (s JobRunTimelineStatus) String() string { return string(s) }

// ScheduleOverlap controls what a schedule fire does when the previous run
// from the same schedule is still going.
type ScheduleOverlap string

const (
	// ScheduleOverlapSkip drops the new fire. This is the default.
	ScheduleOverlapSkip ScheduleOverlap = "skip"
	// ScheduleOverlapBufferOne queues at most one fire to run after the current one finishes.
	ScheduleOverlapBufferOne ScheduleOverlap = "buffer_one"
	// ScheduleOverlapAllowAll lets fires run concurrently without limit.
	ScheduleOverlapAllowAll ScheduleOverlap = "allow_all"
)

// String implements fmt.Stringer.
func (o ScheduleOverlap) String() string { return string(o) }

// ScheduleAction is something the caller may do to a job schedule right now,
// computed server-side.
type ScheduleAction string

const (
	ScheduleActionPause   ScheduleAction = "pause"
	ScheduleActionResume  ScheduleAction = "resume"
	ScheduleActionTrigger ScheduleAction = "trigger"
	ScheduleActionUpdate  ScheduleAction = "update"
	ScheduleActionDelete  ScheduleAction = "delete"
)

// String implements fmt.Stringer.
func (a ScheduleAction) String() string { return string(a) }

// JobScriptSpec is a script to run inline instead of Cmd. The body travels
// inline and is size-capped; a script too large for that belongs in the
// template or should fetch itself.
type JobScriptSpec struct {
	Body        []byte
	Interpreter string
	Args        []string
	Filename    string
}

// JobSandboxSpec describes a new sandbox for a job to create, in place of
// targeting an existing one via ExistingSandboxID.
type JobSandboxSpec struct {
	Template string
	Version  string
	Name     string
}

// JobSpec is a job's definition: what to run, and where. Exactly one of
// ExistingSandboxID and NewSandbox is required, and exactly one of Cmd and
// Script is required.
type JobSpec struct {
	// ExistingSandboxID targets an already-running sandbox. Exactly one of
	// ExistingSandboxID and NewSandbox is required.
	ExistingSandboxID string
	// NewSandbox creates a sandbox for this run. Exactly one of
	// ExistingSandboxID and NewSandbox is required.
	NewSandbox *JobSandboxSpec
	// Cmd is the command and arguments to run. Exactly one of Cmd and Script
	// is required.
	Cmd []string
	// Script is a script to run instead of Cmd. Exactly one of Cmd and
	// Script is required.
	Script *JobScriptSpec
	// PreRunScript runs after the sandbox is ready and before Cmd/Script. A
	// non-zero exit stops the job.
	PreRunScript          *JobScriptSpec
	Env                   map[string]string
	Path                  string
	Stdin                 []byte
	CommandTimeoutSeconds int32
	RunDeadlineSeconds    int32
	MaxOutputBytes        int32
	// Disposition controls what happens to a sandbox this job created once
	// the run ends. Defaults to DispositionDelete. Not meaningful for
	// ExistingSandboxID.
	Disposition Disposition
	// ResumeIfSuspended applies to ExistingSandboxID only. False (the
	// default) makes a suspended target a failure rather than an implicit
	// resume, which is a billable action the caller may not have intended.
	ResumeIfSuspended bool
	// Name is an optional human-readable name for this job.
	Name string
}

// JobResult is a finished job run's command outcome.
type JobResult struct {
	ExitCode        int32
	Signaled        bool
	Signal          int32
	TimedOut        bool
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
}

// JobRun is a job run's status and outcome, without its stored spec or
// timeline. GetJobRun returns the fuller JobRunDetail; ListJobRuns returns
// this, since assembling a timeline is a history read per run.
type JobRun struct {
	RunID     string
	Status    JobRunStatus
	SandboxID string
	// CreatedSandbox distinguishes whether this run created SandboxID or was
	// given it, which decides whether the caller still owns it after the run.
	CreatedSandbox      bool
	ScheduleID          string
	StartedAtUnixNanos  int64
	FinishedAtUnixNanos int64
	Result              *JobResult
	// Failure is the run's own failure -- create/resume/exec-transport
	// failure -- never the command's own non-zero exit. A command that
	// exits non-zero is a completed run with a non-zero exit code.
	Failure string
	// CleanupFailed says the run finished but could not delete the sandbox
	// it created. That sandbox is still billed and still visible in
	// Sandboxes.List.
	CleanupFailed bool
	// AvailableActions is what the caller may do to this run right now,
	// computed server-side.
	AvailableActions []JobRunAction
	Name             string
}

// JobRunTimelineEntry is one activity attempt on a job run's timeline, e.g.
// CreateSandbox, WriteScript, ExecCommand, DeleteSandbox.
type JobRunTimelineEntry struct {
	Name                string
	Status              JobRunTimelineStatus
	Attempt             int32
	StartedAtUnixNanos  int64
	FinishedAtUnixNanos int64
	Failure             string
}

// JobRunDetail adds the stored spec and activity timeline to JobRun. Only
// GetJobRun returns this.
type JobRunDetail struct {
	JobRun
	Spec     *JobSpec
	Timeline []JobRunTimelineEntry
}

// ScheduleSpec is a job schedule's timing. Exactly one of CronExpressions,
// IntervalSeconds, and RunAtUnixNanos is required.
type ScheduleSpec struct {
	CronExpressions []string
	IntervalSeconds int64
	// RunAtUnixNanos is a one-shot: a single future calendar time. Refused
	// if in the past.
	RunAtUnixNanos int64
	// TimeZone is an IANA zone, e.g. "US/Pacific". Empty is UTC.
	TimeZone      string
	JitterSeconds int64
	// Overlap defaults to ScheduleOverlapSkip.
	Overlap ScheduleOverlap
	Paused  bool
}

// JobSchedule is a durable cron, interval, or one-shot trigger for a job.
type JobSchedule struct {
	ScheduleID         string
	Schedule           *ScheduleSpec
	Spec               *JobSpec
	Paused             bool
	Note               string
	NextRunAtUnixNanos int64
	// OneShot is true when this schedule fires exactly once (RunAtUnixNanos).
	OneShot bool
	// RemainingActions is present for one-shot schedules; 0 once spent.
	RemainingActions int32
	// AvailableActions is what the caller may do to this schedule right
	// now, computed server-side.
	AvailableActions                                                    []ScheduleAction
	CreatedAtUnixNanos, UpdatedAtUnixNanos                              int64
	NumActions, NumActionsSkippedOverlap, NumActionsMissedCatchupWindow int32
	// RecentRunIds is the last 10 fires' run ids, oldest first, including
	// manual triggers. Each is a valid Jobs.Get id.
	RecentRunIDs []string
	// RunningRunIDs is which of this schedule's own fires are in flight
	// right now.
	RunningRunIDs []string
}
