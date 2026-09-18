package tyto

import (
	"context"
	"time"

	runtimev1 "buf.build/gen/go/bonya/tyto/protocolbuffers/go/tyto/runtime/v1"
)

// RunJobOptions configures RunJob and StartJob.
type RunJobOptions struct {
	// IdempotencyKey is sent to the service. If empty, the SDK generates one.
	// The run id is derived from it and the resolved tenant, so a caller
	// cannot address another tenant's run by guessing.
	IdempotencyKey string
}

// RunJob runs a job and blocks until it finishes, bounded by the client's
// own timeout. Use StartJob instead for a job that may outlive one request.
func (c *Client) RunJob(ctx context.Context, spec JobSpec, opts ...RunJobOptions) (*JobRun, error) {
	request, key, err := newRunJobRequest(c, spec, opts...)
	if err != nil {
		return nil, err
	}

	dl, err := startDeadline(c.timeout)
	if err != nil {
		return nil, err
	}
	attempts := 0
	backoff := 50 * time.Millisecond
	for {
		remaining, err := dl.remaining()
		if err != nil {
			return nil, MapRPCError(err, c.secrets(key), WithIdempotencyKey(key))
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := c.tapiClient()
		if tErr != nil {
			cancel()
			return nil, tErr
		}
		response, callErr := client.RunJob(callCtx, request)
		cancel()
		if callErr == nil {
			run := response.GetRun()
			if run == nil {
				return nil, &InvalidRequestError{BaseError{Msg: "RunJob response is missing run", IdempotencyKey: key}}
			}
			return jobRunFromProto(run), nil
		}
		if !IsRetryable(callErr) || attempts >= c.maxRetries {
			return nil, MapRPCError(callErr, c.secrets(key), WithIdempotencyKey(key))
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

// StartJob starts a job durably and returns immediately with its run id,
// without waiting for it to finish. Use GetJobRun to poll for the result.
func (c *Client) StartJob(ctx context.Context, spec JobSpec, opts ...RunJobOptions) (runID string, alreadyRunning bool, err error) {
	request, key, err := newStartJobRequest(c, spec, opts...)
	if err != nil {
		return "", false, err
	}

	dl, err := startDeadline(c.timeout)
	if err != nil {
		return "", false, err
	}
	attempts := 0
	backoff := 50 * time.Millisecond
	for {
		remaining, err := dl.remaining()
		if err != nil {
			return "", false, MapRPCError(err, c.secrets(key), WithIdempotencyKey(key))
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := c.tapiClient()
		if tErr != nil {
			cancel()
			return "", false, tErr
		}
		response, callErr := client.StartJob(callCtx, request)
		cancel()
		if callErr == nil {
			return response.GetRunId(), response.GetAlreadyRunning(), nil
		}
		if !IsRetryable(callErr) || attempts >= c.maxRetries {
			return "", false, MapRPCError(callErr, c.secrets(key), WithIdempotencyKey(key))
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

// GetJobRun fetches a job run's full detail, including its stored spec and
// activity timeline.
func (c *Client) GetJobRun(ctx context.Context, runID string) (*JobRunDetail, error) {
	if runID == "" {
		return nil, &InvalidRequestError{BaseError{Msg: "run_id is required"}}
	}
	request := &runtimev1.TApiGetJobRunRequest{ApiKey: c.apiKey, RunId: runID}

	dl, err := startDeadline(c.timeout)
	if err != nil {
		return nil, err
	}
	attempts := 0
	backoff := 50 * time.Millisecond
	for {
		remaining, err := dl.remaining()
		if err != nil {
			return nil, MapRPCError(err, c.secrets(), WithJobRPC())
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := c.tapiClient()
		if tErr != nil {
			cancel()
			return nil, tErr
		}
		response, callErr := client.GetJobRun(callCtx, request)
		cancel()
		if callErr == nil {
			detail := response.GetDetail()
			if detail == nil {
				return nil, &InvalidRequestError{BaseError{Msg: "GetJobRun response is missing detail"}}
			}
			return jobRunDetailFromProto(detail), nil
		}
		if !IsRetryable(callErr) || attempts >= c.maxRetries {
			return nil, MapRPCError(callErr, c.secrets(), WithJobRPC())
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

// ListJobRunsOptions configures ListJobRuns.
type ListJobRunsOptions struct {
	// SandboxID filters to runs on this sandbox. Empty means no filter.
	SandboxID string
	// ScheduleID filters to runs fired by this schedule. Empty means no filter.
	ScheduleID string
	// Limit caps the number of runs returned. 0 means unlimited.
	Limit int
}

// ListJobRuns fetches job runs, newest first, paging internally as needed,
// and returns them as a single slice. A zero Limit returns every matching run.
func (c *Client) ListJobRuns(ctx context.Context, opts ...ListJobRunsOptions) ([]JobRun, error) {
	var o ListJobRunsOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Limit < 0 {
		return nil, &InvalidRequestError{BaseError{Msg: "limit must be a non-negative integer"}}
	}

	var results []JobRun
	pageToken := ""
	for {
		pageSize := int32(0)
		if o.Limit > 0 {
			remaining := o.Limit - len(results)
			if remaining <= 0 {
				return results, nil
			}
			pageSize = int32(min(100, remaining))
		}
		request := &runtimev1.TApiListJobRunsRequest{
			ApiKey:     c.apiKey,
			SandboxId:  o.SandboxID,
			ScheduleId: o.ScheduleID,
			PageSize:   pageSize,
			PageToken:  pageToken,
		}

		dl, err := startDeadline(c.timeout)
		if err != nil {
			return nil, err
		}
		attempts := 0
		backoff := 50 * time.Millisecond
		var response *runtimev1.TApiListJobRunsResponse
		for {
			remaining, err := dl.remaining()
			if err != nil {
				return nil, MapRPCError(err, c.secrets(pageToken), WithJobRPC())
			}
			callCtx, cancel := context.WithTimeout(ctx, remaining)
			client, tErr := c.tapiClient()
			if tErr != nil {
				cancel()
				return nil, tErr
			}
			resp, callErr := client.ListJobRuns(callCtx, request)
			cancel()
			if callErr == nil {
				response = resp
				break
			}
			if !IsRetryable(callErr) || attempts >= c.maxRetries {
				return nil, MapRPCError(callErr, c.secrets(pageToken), WithJobRPC())
			}
			attempts++
			sleepWithDeadline(ctx, backoff, dl)
			backoff = minDuration(backoff*2, 500*time.Millisecond)
		}

		for _, run := range response.GetRuns() {
			if o.Limit > 0 && len(results) >= o.Limit {
				return results, nil
			}
			results = append(results, *jobRunFromProto(run))
		}
		pageToken = response.GetNextPageToken()
		if pageToken == "" || (o.Limit > 0 && len(results) >= o.Limit) {
			return results, nil
		}
	}
}

// CancelJobRun requests cancellation of a running job. This cancels rather
// than terminates, so the run's own cleanup (e.g. deleting a sandbox it
// created) still executes.
func (c *Client) CancelJobRun(ctx context.Context, runID string) error {
	if runID == "" {
		return &InvalidRequestError{BaseError{Msg: "run_id is required"}}
	}
	request := &runtimev1.TApiCancelJobRunRequest{ApiKey: c.apiKey, RunId: runID}

	dl, err := startDeadline(c.timeout)
	if err != nil {
		return err
	}
	attempts := 0
	backoff := 50 * time.Millisecond
	for {
		remaining, err := dl.remaining()
		if err != nil {
			return MapRPCError(err, c.secrets(), WithJobRPC())
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := c.tapiClient()
		if tErr != nil {
			cancel()
			return tErr
		}
		_, callErr := client.CancelJobRun(callCtx, request)
		cancel()
		if callErr == nil {
			return nil
		}
		if !IsRetryable(callErr) || attempts >= c.maxRetries {
			return MapRPCError(callErr, c.secrets(), WithJobRPC())
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

func newRunJobRequest(c *Client, spec JobSpec, opts ...RunJobOptions) (*runtimev1.TApiRunJobRequest, string, error) {
	protoSpec, err := jobSpecToProto(spec)
	if err != nil {
		return nil, "", err
	}
	var o RunJobOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	key := o.IdempotencyKey
	if key == "" {
		key = generateIdempotencyKey()
	}
	return &runtimev1.TApiRunJobRequest{ApiKey: c.apiKey, IdempotencyKey: key, Spec: protoSpec}, key, nil
}

func newStartJobRequest(c *Client, spec JobSpec, opts ...RunJobOptions) (*runtimev1.TApiStartJobRequest, string, error) {
	protoSpec, err := jobSpecToProto(spec)
	if err != nil {
		return nil, "", err
	}
	var o RunJobOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	key := o.IdempotencyKey
	if key == "" {
		key = generateIdempotencyKey()
	}
	return &runtimev1.TApiStartJobRequest{ApiKey: c.apiKey, IdempotencyKey: key, Spec: protoSpec}, key, nil
}

func jobSpecToProto(spec JobSpec) (*runtimev1.TApiJobSpec, error) {
	if spec.ExistingSandboxID != "" && spec.NewSandbox != nil {
		return nil, &InvalidRequestError{BaseError{Msg: "exactly one of ExistingSandboxID and NewSandbox is required, not both"}}
	}
	if spec.ExistingSandboxID == "" && spec.NewSandbox == nil {
		return nil, &InvalidRequestError{BaseError{Msg: "exactly one of ExistingSandboxID and NewSandbox is required"}}
	}
	if len(spec.Cmd) > 0 && spec.Script != nil {
		return nil, &InvalidRequestError{BaseError{Msg: "exactly one of Cmd and Script is required, not both"}}
	}
	if len(spec.Cmd) == 0 && spec.Script == nil {
		return nil, &InvalidRequestError{BaseError{Msg: "exactly one of Cmd and Script is required"}}
	}

	disposition := runtimev1.TApiSandboxDisposition_TAPI_SANDBOX_DISPOSITION_DELETE
	switch spec.Disposition {
	case "", DispositionDelete:
		disposition = runtimev1.TApiSandboxDisposition_TAPI_SANDBOX_DISPOSITION_DELETE
	case DispositionKeep:
		disposition = runtimev1.TApiSandboxDisposition_TAPI_SANDBOX_DISPOSITION_KEEP
	default:
		return nil, &InvalidRequestError{BaseError{Msg: "disposition must be DispositionDelete or DispositionKeep"}}
	}

	protoSpec := &runtimev1.TApiJobSpec{
		ExistingSandboxId:     spec.ExistingSandboxID,
		Cmd:                   spec.Cmd,
		Env:                   spec.Env,
		Path:                  spec.Path,
		Stdin:                 spec.Stdin,
		CommandTimeoutSeconds: spec.CommandTimeoutSeconds,
		RunDeadlineSeconds:    spec.RunDeadlineSeconds,
		MaxOutputBytes:        spec.MaxOutputBytes,
		Disposition:           disposition,
		ResumeIfSuspended:     spec.ResumeIfSuspended,
		Name:                  spec.Name,
	}
	if spec.NewSandbox != nil {
		protoSpec.NewSandbox = &runtimev1.TApiJobSandboxSpec{
			Template: &runtimev1.TemplateBinding{TemplateId: spec.NewSandbox.Template, Version: spec.NewSandbox.Version},
			Name:     spec.NewSandbox.Name,
		}
	}
	if spec.Script != nil {
		protoSpec.Script = jobScriptSpecToProto(spec.Script)
	}
	if spec.PreRunScript != nil {
		protoSpec.PreRunScript = jobScriptSpecToProto(spec.PreRunScript)
	}
	return protoSpec, nil
}

func jobScriptSpecToProto(spec *JobScriptSpec) *runtimev1.TApiJobScriptSpec {
	return &runtimev1.TApiJobScriptSpec{
		Body:        spec.Body,
		Interpreter: spec.Interpreter,
		Args:        spec.Args,
		Filename:    spec.Filename,
	}
}

func jobSpecFromProto(spec *runtimev1.TApiJobSpec) *JobSpec {
	if spec == nil {
		return nil
	}
	result := &JobSpec{
		ExistingSandboxID:     spec.GetExistingSandboxId(),
		Cmd:                   spec.GetCmd(),
		Env:                   spec.GetEnv(),
		Path:                  spec.GetPath(),
		Stdin:                 spec.GetStdin(),
		CommandTimeoutSeconds: spec.GetCommandTimeoutSeconds(),
		RunDeadlineSeconds:    spec.GetRunDeadlineSeconds(),
		MaxOutputBytes:        spec.GetMaxOutputBytes(),
		Disposition:           dispositionFromProto(spec.GetDisposition()),
		ResumeIfSuspended:     spec.GetResumeIfSuspended(),
		Name:                  spec.GetName(),
	}
	if ns := spec.GetNewSandbox(); ns != nil {
		result.NewSandbox = &JobSandboxSpec{
			Template: ns.GetTemplate().GetTemplateId(),
			Version:  ns.GetTemplate().GetVersion(),
			Name:     ns.GetName(),
		}
	}
	if script := spec.GetScript(); script != nil {
		result.Script = jobScriptSpecFromProto(script)
	}
	if pre := spec.GetPreRunScript(); pre != nil {
		result.PreRunScript = jobScriptSpecFromProto(pre)
	}
	return result
}

func jobScriptSpecFromProto(spec *runtimev1.TApiJobScriptSpec) *JobScriptSpec {
	if spec == nil {
		return nil
	}
	return &JobScriptSpec{
		Body:        spec.GetBody(),
		Interpreter: spec.GetInterpreter(),
		Args:        spec.GetArgs(),
		Filename:    spec.GetFilename(),
	}
}

func dispositionFromProto(d runtimev1.TApiSandboxDisposition) Disposition {
	switch d {
	case runtimev1.TApiSandboxDisposition_TAPI_SANDBOX_DISPOSITION_KEEP:
		return DispositionKeep
	default:
		return DispositionDelete
	}
}

func jobRunStatusFromProto(s runtimev1.TApiJobRunStatus) JobRunStatus {
	switch s {
	case runtimev1.TApiJobRunStatus_TAPI_JOB_RUN_STATUS_RUNNING:
		return JobRunStatusRunning
	case runtimev1.TApiJobRunStatus_TAPI_JOB_RUN_STATUS_COMPLETED:
		return JobRunStatusCompleted
	case runtimev1.TApiJobRunStatus_TAPI_JOB_RUN_STATUS_FAILED:
		return JobRunStatusFailed
	case runtimev1.TApiJobRunStatus_TAPI_JOB_RUN_STATUS_CANCELED:
		return JobRunStatusCanceled
	case runtimev1.TApiJobRunStatus_TAPI_JOB_RUN_STATUS_TIMED_OUT:
		return JobRunStatusTimedOut
	case runtimev1.TApiJobRunStatus_TAPI_JOB_RUN_STATUS_TERMINATED:
		return JobRunStatusTerminated
	default:
		return ""
	}
}

func jobRunActionsFromProto(actions []runtimev1.TApiJobRunAction) []JobRunAction {
	if len(actions) == 0 {
		return nil
	}
	result := make([]JobRunAction, 0, len(actions))
	for _, a := range actions {
		switch a {
		case runtimev1.TApiJobRunAction_TAPI_JOB_RUN_ACTION_CANCEL:
			result = append(result, JobRunActionCancel)
		case runtimev1.TApiJobRunAction_TAPI_JOB_RUN_ACTION_RERUN:
			result = append(result, JobRunActionRerun)
		case runtimev1.TApiJobRunAction_TAPI_JOB_RUN_ACTION_RETRY:
			result = append(result, JobRunActionRetry)
		case runtimev1.TApiJobRunAction_TAPI_JOB_RUN_ACTION_DELETE:
			result = append(result, JobRunActionDelete)
		case runtimev1.TApiJobRunAction_TAPI_JOB_RUN_ACTION_DELETE_SANDBOX:
			result = append(result, JobRunActionDeleteSandbox)
		}
	}
	return result
}

func jobRunTimelineStatusFromProto(s runtimev1.TApiJobRunTimelineStatus) JobRunTimelineStatus {
	switch s {
	case runtimev1.TApiJobRunTimelineStatus_TAPI_JOB_RUN_TIMELINE_STATUS_SCHEDULED:
		return JobRunTimelineStatusScheduled
	case runtimev1.TApiJobRunTimelineStatus_TAPI_JOB_RUN_TIMELINE_STATUS_RUNNING:
		return JobRunTimelineStatusRunning
	case runtimev1.TApiJobRunTimelineStatus_TAPI_JOB_RUN_TIMELINE_STATUS_COMPLETED:
		return JobRunTimelineStatusCompleted
	case runtimev1.TApiJobRunTimelineStatus_TAPI_JOB_RUN_TIMELINE_STATUS_FAILED:
		return JobRunTimelineStatusFailed
	case runtimev1.TApiJobRunTimelineStatus_TAPI_JOB_RUN_TIMELINE_STATUS_CANCELED:
		return JobRunTimelineStatusCanceled
	default:
		return ""
	}
}

func jobResultFromProto(r *runtimev1.TApiJobResult) *JobResult {
	if r == nil {
		return nil
	}
	return &JobResult{
		ExitCode:        r.GetExitCode(),
		Signaled:        r.GetSignaled(),
		Signal:          r.GetSignal(),
		TimedOut:        r.GetTimedOut(),
		Stdout:          r.GetStdout(),
		Stderr:          r.GetStderr(),
		StdoutTruncated: r.GetStdoutTruncated(),
		StderrTruncated: r.GetStderrTruncated(),
	}
}

func jobRunFromProto(run *runtimev1.TApiJobRun) *JobRun {
	if run == nil {
		return nil
	}
	return &JobRun{
		RunID:               run.GetRunId(),
		Status:              jobRunStatusFromProto(run.GetStatus()),
		SandboxID:           run.GetSandboxId(),
		CreatedSandbox:      run.GetCreatedSandbox(),
		ScheduleID:          run.GetScheduleId(),
		StartedAtUnixNanos:  run.GetStartedAtUnixNanos(),
		FinishedAtUnixNanos: run.GetFinishedAtUnixNanos(),
		Result:              jobResultFromProto(run.GetResult()),
		Failure:             run.GetFailure(),
		CleanupFailed:       run.GetCleanupFailed(),
		AvailableActions:    jobRunActionsFromProto(run.GetAvailableActions()),
		Name:                run.GetName(),
	}
}

func jobRunTimelineFromProto(entries []*runtimev1.TApiJobRunTimelineEntry) []JobRunTimelineEntry {
	if len(entries) == 0 {
		return nil
	}
	result := make([]JobRunTimelineEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, JobRunTimelineEntry{
			Name:                e.GetName(),
			Status:              jobRunTimelineStatusFromProto(e.GetStatus()),
			Attempt:             e.GetAttempt(),
			StartedAtUnixNanos:  e.GetStartedAtUnixNanos(),
			FinishedAtUnixNanos: e.GetFinishedAtUnixNanos(),
			Failure:             e.GetFailure(),
		})
	}
	return result
}

func jobRunDetailFromProto(detail *runtimev1.TApiJobRunDetail) *JobRunDetail {
	if detail == nil {
		return nil
	}
	run := jobRunFromProto(detail.GetRun())
	if run == nil {
		run = &JobRun{}
	}
	return &JobRunDetail{
		JobRun:   *run,
		Spec:     jobSpecFromProto(detail.GetSpec()),
		Timeline: jobRunTimelineFromProto(detail.GetTimeline()),
	}
}
