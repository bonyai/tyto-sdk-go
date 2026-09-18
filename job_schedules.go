package tyto

import (
	"context"
	"time"

	runtimev1 "buf.build/gen/go/bonya/tyto/protocolbuffers/go/tyto/runtime/v1"
)

// CreateJobScheduleOptions configures CreateJobSchedule.
type CreateJobScheduleOptions struct {
	// IdempotencyKey is sent to the service. If empty, the SDK generates one.
	IdempotencyKey string
}

// CreateJobSchedule creates a durable cron, interval, or one-shot trigger
// for a job. Exactly one of ScheduleSpec.CronExpressions, IntervalSeconds,
// and RunAtUnixNanos is required.
func (c *Client) CreateJobSchedule(ctx context.Context, schedule ScheduleSpec, job JobSpec, opts ...CreateJobScheduleOptions) (*JobSchedule, error) {
	protoSchedule, err := scheduleSpecToProto(schedule)
	if err != nil {
		return nil, err
	}
	protoJob, err := jobSpecToProto(job)
	if err != nil {
		return nil, err
	}
	var o CreateJobScheduleOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	key := o.IdempotencyKey
	if key == "" {
		key = generateIdempotencyKey()
	}
	request := &runtimev1.TApiCreateJobScheduleRequest{
		ApiKey:         c.apiKey,
		IdempotencyKey: key,
		Schedule:       protoSchedule,
		Spec:           protoJob,
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
			return nil, MapRPCError(err, c.secrets(key), WithIdempotencyKey(key), WithJobScheduleRPC())
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := c.tapiClient()
		if tErr != nil {
			cancel()
			return nil, tErr
		}
		response, callErr := client.CreateJobSchedule(callCtx, request)
		cancel()
		if callErr == nil {
			schedule := response.GetSchedule()
			if schedule == nil {
				return nil, &InvalidRequestError{BaseError{Msg: "CreateJobSchedule response is missing schedule", IdempotencyKey: key}}
			}
			return jobScheduleFromProto(schedule), nil
		}
		if !IsRetryable(callErr) || attempts >= c.maxRetries {
			return nil, MapRPCError(callErr, c.secrets(key), WithIdempotencyKey(key), WithJobScheduleRPC())
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

// GetJobSchedule fetches a job schedule by id.
func (c *Client) GetJobSchedule(ctx context.Context, scheduleID string) (*JobSchedule, error) {
	if scheduleID == "" {
		return nil, &InvalidRequestError{BaseError{Msg: "schedule_id is required"}}
	}
	request := &runtimev1.TApiGetJobScheduleRequest{ApiKey: c.apiKey, ScheduleId: scheduleID}

	dl, err := startDeadline(c.timeout)
	if err != nil {
		return nil, err
	}
	attempts := 0
	backoff := 50 * time.Millisecond
	for {
		remaining, err := dl.remaining()
		if err != nil {
			return nil, MapRPCError(err, c.secrets(), WithJobScheduleRPC())
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := c.tapiClient()
		if tErr != nil {
			cancel()
			return nil, tErr
		}
		response, callErr := client.GetJobSchedule(callCtx, request)
		cancel()
		if callErr == nil {
			schedule := response.GetSchedule()
			if schedule == nil {
				return nil, &InvalidRequestError{BaseError{Msg: "GetJobSchedule response is missing schedule"}}
			}
			return jobScheduleFromProto(schedule), nil
		}
		if !IsRetryable(callErr) || attempts >= c.maxRetries {
			return nil, MapRPCError(callErr, c.secrets(), WithJobScheduleRPC())
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

// ListJobSchedulesOptions configures ListJobSchedules.
type ListJobSchedulesOptions struct {
	// Limit caps the number of schedules returned. 0 means unlimited.
	Limit int
}

// ListJobSchedules fetches job schedules, paging internally as needed, and
// returns them as a single slice. A zero Limit returns every schedule.
func (c *Client) ListJobSchedules(ctx context.Context, opts ...ListJobSchedulesOptions) ([]JobSchedule, error) {
	var o ListJobSchedulesOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Limit < 0 {
		return nil, &InvalidRequestError{BaseError{Msg: "limit must be a non-negative integer"}}
	}

	var results []JobSchedule
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
		request := &runtimev1.TApiListJobSchedulesRequest{ApiKey: c.apiKey, PageSize: pageSize, PageToken: pageToken}

		dl, err := startDeadline(c.timeout)
		if err != nil {
			return nil, err
		}
		attempts := 0
		backoff := 50 * time.Millisecond
		var response *runtimev1.TApiListJobSchedulesResponse
		for {
			remaining, err := dl.remaining()
			if err != nil {
				return nil, MapRPCError(err, c.secrets(pageToken), WithJobScheduleRPC())
			}
			callCtx, cancel := context.WithTimeout(ctx, remaining)
			client, tErr := c.tapiClient()
			if tErr != nil {
				cancel()
				return nil, tErr
			}
			resp, callErr := client.ListJobSchedules(callCtx, request)
			cancel()
			if callErr == nil {
				response = resp
				break
			}
			if !IsRetryable(callErr) || attempts >= c.maxRetries {
				return nil, MapRPCError(callErr, c.secrets(pageToken), WithJobScheduleRPC())
			}
			attempts++
			sleepWithDeadline(ctx, backoff, dl)
			backoff = minDuration(backoff*2, 500*time.Millisecond)
		}

		for _, schedule := range response.GetSchedules() {
			if o.Limit > 0 && len(results) >= o.Limit {
				return results, nil
			}
			results = append(results, *jobScheduleFromProto(schedule))
		}
		pageToken = response.GetNextPageToken()
		if pageToken == "" || (o.Limit > 0 && len(results) >= o.Limit) {
			return results, nil
		}
	}
}

// UpdateJobSchedule replaces a job schedule's timing and job spec. It
// replaces the whole schedule, so pass every field you want to keep, not
// just the one you're changing.
func (c *Client) UpdateJobSchedule(ctx context.Context, scheduleID string, schedule ScheduleSpec, job JobSpec) (*JobSchedule, error) {
	if scheduleID == "" {
		return nil, &InvalidRequestError{BaseError{Msg: "schedule_id is required"}}
	}
	protoSchedule, err := scheduleSpecToProto(schedule)
	if err != nil {
		return nil, err
	}
	protoJob, err := jobSpecToProto(job)
	if err != nil {
		return nil, err
	}
	request := &runtimev1.TApiUpdateJobScheduleRequest{
		ApiKey:     c.apiKey,
		ScheduleId: scheduleID,
		Schedule:   protoSchedule,
		Spec:       protoJob,
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
			return nil, MapRPCError(err, c.secrets(), WithJobScheduleRPC())
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := c.tapiClient()
		if tErr != nil {
			cancel()
			return nil, tErr
		}
		response, callErr := client.UpdateJobSchedule(callCtx, request)
		cancel()
		if callErr == nil {
			result := response.GetSchedule()
			if result == nil {
				return nil, &InvalidRequestError{BaseError{Msg: "UpdateJobSchedule response is missing schedule"}}
			}
			return jobScheduleFromProto(result), nil
		}
		if !IsRetryable(callErr) || attempts >= c.maxRetries {
			return nil, MapRPCError(callErr, c.secrets(), WithJobScheduleRPC())
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

// SetJobSchedulePaused pauses or resumes a job schedule, optionally
// recording a note.
func (c *Client) SetJobSchedulePaused(ctx context.Context, scheduleID string, paused bool, note string) (*JobSchedule, error) {
	if scheduleID == "" {
		return nil, &InvalidRequestError{BaseError{Msg: "schedule_id is required"}}
	}
	request := &runtimev1.TApiSetJobSchedulePausedRequest{ApiKey: c.apiKey, ScheduleId: scheduleID, Paused: paused, Note: note}

	dl, err := startDeadline(c.timeout)
	if err != nil {
		return nil, err
	}
	attempts := 0
	backoff := 50 * time.Millisecond
	for {
		remaining, err := dl.remaining()
		if err != nil {
			return nil, MapRPCError(err, c.secrets(), WithJobScheduleRPC())
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := c.tapiClient()
		if tErr != nil {
			cancel()
			return nil, tErr
		}
		response, callErr := client.SetJobSchedulePaused(callCtx, request)
		cancel()
		if callErr == nil {
			result := response.GetSchedule()
			if result == nil {
				return nil, &InvalidRequestError{BaseError{Msg: "SetJobSchedulePaused response is missing schedule"}}
			}
			return jobScheduleFromProto(result), nil
		}
		if !IsRetryable(callErr) || attempts >= c.maxRetries {
			return nil, MapRPCError(callErr, c.secrets(), WithJobScheduleRPC())
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

// TriggerJobSchedule fires one run of a job schedule immediately,
// overriding the schedule's own timing.
func (c *Client) TriggerJobSchedule(ctx context.Context, scheduleID string) error {
	if scheduleID == "" {
		return &InvalidRequestError{BaseError{Msg: "schedule_id is required"}}
	}
	request := &runtimev1.TApiTriggerJobScheduleRequest{ApiKey: c.apiKey, ScheduleId: scheduleID}

	dl, err := startDeadline(c.timeout)
	if err != nil {
		return err
	}
	attempts := 0
	backoff := 50 * time.Millisecond
	for {
		remaining, err := dl.remaining()
		if err != nil {
			return MapRPCError(err, c.secrets(), WithJobScheduleRPC())
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := c.tapiClient()
		if tErr != nil {
			cancel()
			return tErr
		}
		_, callErr := client.TriggerJobSchedule(callCtx, request)
		cancel()
		if callErr == nil {
			return nil
		}
		if !IsRetryable(callErr) || attempts >= c.maxRetries {
			return MapRPCError(callErr, c.secrets(), WithJobScheduleRPC())
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

// DeleteJobSchedule permanently deletes a job schedule. Does not cancel any
// run currently in flight.
func (c *Client) DeleteJobSchedule(ctx context.Context, scheduleID string) error {
	if scheduleID == "" {
		return &InvalidRequestError{BaseError{Msg: "schedule_id is required"}}
	}
	request := &runtimev1.TApiDeleteJobScheduleRequest{ApiKey: c.apiKey, ScheduleId: scheduleID}

	dl, err := startDeadline(c.timeout)
	if err != nil {
		return err
	}
	attempts := 0
	backoff := 50 * time.Millisecond
	for {
		remaining, err := dl.remaining()
		if err != nil {
			return MapRPCError(err, c.secrets(), WithJobScheduleRPC())
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := c.tapiClient()
		if tErr != nil {
			cancel()
			return tErr
		}
		_, callErr := client.DeleteJobSchedule(callCtx, request)
		cancel()
		if callErr == nil {
			return nil
		}
		if !IsRetryable(callErr) || attempts >= c.maxRetries {
			return MapRPCError(callErr, c.secrets(), WithJobScheduleRPC())
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

func scheduleSpecToProto(spec ScheduleSpec) (*runtimev1.TApiScheduleSpec, error) {
	set := 0
	if len(spec.CronExpressions) > 0 {
		set++
	}
	if spec.IntervalSeconds > 0 {
		set++
	}
	if spec.RunAtUnixNanos > 0 {
		set++
	}
	if set != 1 {
		return nil, &InvalidRequestError{BaseError{Msg: "exactly one of CronExpressions, IntervalSeconds, and RunAtUnixNanos is required"}}
	}

	overlap := runtimev1.TApiScheduleOverlap_TAPI_SCHEDULE_OVERLAP_SKIP
	switch spec.Overlap {
	case "", ScheduleOverlapSkip:
		overlap = runtimev1.TApiScheduleOverlap_TAPI_SCHEDULE_OVERLAP_SKIP
	case ScheduleOverlapBufferOne:
		overlap = runtimev1.TApiScheduleOverlap_TAPI_SCHEDULE_OVERLAP_BUFFER_ONE
	case ScheduleOverlapAllowAll:
		overlap = runtimev1.TApiScheduleOverlap_TAPI_SCHEDULE_OVERLAP_ALLOW_ALL
	default:
		return nil, &InvalidRequestError{BaseError{Msg: "overlap must be a valid ScheduleOverlap value"}}
	}

	return &runtimev1.TApiScheduleSpec{
		CronExpressions: spec.CronExpressions,
		IntervalSeconds: spec.IntervalSeconds,
		RunAtUnixNanos:  spec.RunAtUnixNanos,
		TimeZone:        spec.TimeZone,
		JitterSeconds:   spec.JitterSeconds,
		Overlap:         overlap,
		Paused:          spec.Paused,
	}, nil
}

func scheduleSpecFromProto(spec *runtimev1.TApiScheduleSpec) *ScheduleSpec {
	if spec == nil {
		return nil
	}
	return &ScheduleSpec{
		CronExpressions: spec.GetCronExpressions(),
		IntervalSeconds: spec.GetIntervalSeconds(),
		RunAtUnixNanos:  spec.GetRunAtUnixNanos(),
		TimeZone:        spec.GetTimeZone(),
		JitterSeconds:   spec.GetJitterSeconds(),
		Overlap:         scheduleOverlapFromProto(spec.GetOverlap()),
		Paused:          spec.GetPaused(),
	}
}

func scheduleOverlapFromProto(o runtimev1.TApiScheduleOverlap) ScheduleOverlap {
	switch o {
	case runtimev1.TApiScheduleOverlap_TAPI_SCHEDULE_OVERLAP_BUFFER_ONE:
		return ScheduleOverlapBufferOne
	case runtimev1.TApiScheduleOverlap_TAPI_SCHEDULE_OVERLAP_ALLOW_ALL:
		return ScheduleOverlapAllowAll
	default:
		return ScheduleOverlapSkip
	}
}

func scheduleActionsFromProto(actions []runtimev1.TApiScheduleAction) []ScheduleAction {
	if len(actions) == 0 {
		return nil
	}
	result := make([]ScheduleAction, 0, len(actions))
	for _, a := range actions {
		switch a {
		case runtimev1.TApiScheduleAction_TAPI_SCHEDULE_ACTION_PAUSE:
			result = append(result, ScheduleActionPause)
		case runtimev1.TApiScheduleAction_TAPI_SCHEDULE_ACTION_RESUME:
			result = append(result, ScheduleActionResume)
		case runtimev1.TApiScheduleAction_TAPI_SCHEDULE_ACTION_TRIGGER:
			result = append(result, ScheduleActionTrigger)
		case runtimev1.TApiScheduleAction_TAPI_SCHEDULE_ACTION_UPDATE:
			result = append(result, ScheduleActionUpdate)
		case runtimev1.TApiScheduleAction_TAPI_SCHEDULE_ACTION_DELETE:
			result = append(result, ScheduleActionDelete)
		}
	}
	return result
}

func jobScheduleFromProto(schedule *runtimev1.TApiJobSchedule) *JobSchedule {
	if schedule == nil {
		return nil
	}
	return &JobSchedule{
		ScheduleID:                    schedule.GetScheduleId(),
		Schedule:                      scheduleSpecFromProto(schedule.GetSchedule()),
		Spec:                          jobSpecFromProto(schedule.GetSpec()),
		Paused:                        schedule.GetPaused(),
		Note:                          schedule.GetNote(),
		NextRunAtUnixNanos:            schedule.GetNextRunAtUnixNanos(),
		OneShot:                       schedule.GetOneShot(),
		RemainingActions:              schedule.GetRemainingActions(),
		AvailableActions:              scheduleActionsFromProto(schedule.GetAvailableActions()),
		CreatedAtUnixNanos:            schedule.GetCreatedAtUnixNanos(),
		UpdatedAtUnixNanos:            schedule.GetUpdatedAtUnixNanos(),
		NumActions:                    schedule.GetNumActions(),
		NumActionsSkippedOverlap:      schedule.GetNumActionsSkippedOverlap(),
		NumActionsMissedCatchupWindow: schedule.GetNumActionsMissedCatchupWindow(),
		RecentRunIDs:                  schedule.GetRecentRunIds(),
		RunningRunIDs:                 schedule.GetRunningRunIds(),
	}
}
