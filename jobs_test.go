package tyto

import (
	"context"
	"errors"
	"testing"

	runtimev1 "buf.build/gen/go/bonya/tyto/protocolbuffers/go/tyto/runtime/v1"
)

func TestRunJobRequiresExactlyOneSandboxTarget(t *testing.T) {
	client := newBufconnClient(t, &fakeTApi{})

	_, err := client.RunJob(context.Background(), JobSpec{Cmd: []string{"echo", "hi"}})
	var invalid *InvalidRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("RunJob with neither ExistingSandboxID nor NewSandbox error = %v, want *InvalidRequestError", err)
	}

	_, err = client.RunJob(context.Background(), JobSpec{
		ExistingSandboxID: "sbx-1",
		NewSandbox:        &JobSandboxSpec{Template: "ubuntu"},
		Cmd:               []string{"echo", "hi"},
	})
	if !errors.As(err, &invalid) {
		t.Fatalf("RunJob with both ExistingSandboxID and NewSandbox error = %v, want *InvalidRequestError", err)
	}
}

func TestRunJobRequiresExactlyOneCommandForm(t *testing.T) {
	client := newBufconnClient(t, &fakeTApi{})

	_, err := client.RunJob(context.Background(), JobSpec{ExistingSandboxID: "sbx-1"})
	var invalid *InvalidRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("RunJob with neither Cmd nor Script error = %v, want *InvalidRequestError", err)
	}

	_, err = client.RunJob(context.Background(), JobSpec{
		ExistingSandboxID: "sbx-1",
		Cmd:               []string{"echo", "hi"},
		Script:            &JobScriptSpec{Body: []byte("echo hi")},
	})
	if !errors.As(err, &invalid) {
		t.Fatalf("RunJob with both Cmd and Script error = %v, want *InvalidRequestError", err)
	}
}

func TestRunJobSendsSpecAndMapsResult(t *testing.T) {
	fake := &fakeTApi{jobRun: &runtimev1.TApiJobRun{
		RunId:          "run-42",
		Status:         runtimev1.TApiJobRunStatus_TAPI_JOB_RUN_STATUS_COMPLETED,
		SandboxId:      "sbx-1",
		CreatedSandbox: true,
		Result: &runtimev1.TApiJobResult{
			ExitCode: 0,
			Stdout:   []byte("hello"),
		},
		AvailableActions: []runtimev1.TApiJobRunAction{runtimev1.TApiJobRunAction_TAPI_JOB_RUN_ACTION_RERUN},
	}}
	client := newBufconnClient(t, fake)

	run, err := client.RunJob(context.Background(), JobSpec{
		NewSandbox: &JobSandboxSpec{Template: "ubuntu"},
		Cmd:        []string{"echo", "hello"},
	})
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	if run.RunID != "run-42" || run.Status != JobRunStatusCompleted || run.SandboxID != "sbx-1" || !run.CreatedSandbox {
		t.Errorf("run = %+v", run)
	}
	if run.Result == nil || string(run.Result.Stdout) != "hello" {
		t.Errorf("run.Result = %+v", run.Result)
	}
	if len(run.AvailableActions) != 1 || run.AvailableActions[0] != JobRunActionRerun {
		t.Errorf("run.AvailableActions = %v", run.AvailableActions)
	}

	if fake.lastRunJobRequest.GetSpec().GetNewSandbox().GetTemplate().GetTemplateId() != "ubuntu" {
		t.Errorf("service received template = %q, want ubuntu", fake.lastRunJobRequest.GetSpec().GetNewSandbox().GetTemplate().GetTemplateId())
	}
	if fake.lastRunJobRequest.GetIdempotencyKey() == "" {
		t.Error("RunJob did not generate an idempotency key")
	}
}

func TestStartJobReturnsRunIDImmediately(t *testing.T) {
	fake := &fakeTApi{}
	client := newBufconnClient(t, fake)

	runID, alreadyRunning, err := client.StartJob(context.Background(), JobSpec{
		ExistingSandboxID: "sbx-1",
		Cmd:               []string{"sleep", "30"},
	})
	if err != nil {
		t.Fatalf("StartJob: %v", err)
	}
	if runID != "run-1" || alreadyRunning {
		t.Errorf("StartJob() = (%q, %v), want (run-1, false)", runID, alreadyRunning)
	}
	if fake.startJobCalls != 1 {
		t.Errorf("startJobCalls = %d, want 1", fake.startJobCalls)
	}
}

func TestGetJobRunReturnsDetailWithSpecAndTimeline(t *testing.T) {
	fake := &fakeTApi{jobRunDetail: &runtimev1.TApiJobRunDetail{
		Run: &runtimev1.TApiJobRun{RunId: "run-1", Status: runtimev1.TApiJobRunStatus_TAPI_JOB_RUN_STATUS_FAILED},
		Spec: &runtimev1.TApiJobSpec{
			ExistingSandboxId: "sbx-1",
			Cmd:               []string{"false"},
		},
		Timeline: []*runtimev1.TApiJobRunTimelineEntry{
			{Name: "ExecCommand", Status: runtimev1.TApiJobRunTimelineStatus_TAPI_JOB_RUN_TIMELINE_STATUS_FAILED},
		},
	}}
	client := newBufconnClient(t, fake)

	detail, err := client.GetJobRun(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("GetJobRun: %v", err)
	}
	if detail.Status != JobRunStatusFailed {
		t.Errorf("detail.Status = %v, want failed", detail.Status)
	}
	if detail.Spec == nil || detail.Spec.ExistingSandboxID != "sbx-1" {
		t.Errorf("detail.Spec = %+v", detail.Spec)
	}
	if len(detail.Timeline) != 1 || detail.Timeline[0].Name != "ExecCommand" {
		t.Errorf("detail.Timeline = %+v", detail.Timeline)
	}
}

func TestGetJobRunNotFound(t *testing.T) {
	client := newBufconnClient(t, &fakeTApi{notFoundOnGetJobRun: true})

	_, err := client.GetJobRun(context.Background(), "run-missing")
	var notFound *JobRunNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("GetJobRun error = %v, want *JobRunNotFoundError", err)
	}
}

func TestCancelJobRun(t *testing.T) {
	fake := &fakeTApi{}
	client := newBufconnClient(t, fake)

	if err := client.CancelJobRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("CancelJobRun: %v", err)
	}
	if fake.cancelJobRunCalls != 1 {
		t.Errorf("cancelJobRunCalls = %d, want 1", fake.cancelJobRunCalls)
	}
}

func TestListJobRunsMapsFields(t *testing.T) {
	fake := &fakeTApi{jobRuns: []*runtimev1.TApiJobRun{
		{RunId: "run-1", Status: runtimev1.TApiJobRunStatus_TAPI_JOB_RUN_STATUS_RUNNING},
		{RunId: "run-2", Status: runtimev1.TApiJobRunStatus_TAPI_JOB_RUN_STATUS_COMPLETED},
	}}
	client := newBufconnClient(t, fake)

	runs, err := client.ListJobRuns(context.Background())
	if err != nil {
		t.Fatalf("ListJobRuns: %v", err)
	}
	if len(runs) != 2 || runs[0].RunID != "run-1" || runs[1].Status != JobRunStatusCompleted {
		t.Errorf("runs = %+v", runs)
	}
}

func TestCreateJobScheduleRequiresExactlyOneTiming(t *testing.T) {
	client := newBufconnClient(t, &fakeTApi{})
	job := JobSpec{ExistingSandboxID: "sbx-1", Cmd: []string{"echo", "hi"}}

	_, err := client.CreateJobSchedule(context.Background(), ScheduleSpec{}, job)
	var invalid *InvalidRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("CreateJobSchedule with no timing error = %v, want *InvalidRequestError", err)
	}

	_, err = client.CreateJobSchedule(context.Background(), ScheduleSpec{
		CronExpressions: []string{"* * * * *"},
		IntervalSeconds: 60,
	}, job)
	if !errors.As(err, &invalid) {
		t.Fatalf("CreateJobSchedule with two timing fields error = %v, want *InvalidRequestError", err)
	}
}

func TestCreateJobScheduleSendsScheduleAndJob(t *testing.T) {
	fake := &fakeTApi{}
	client := newBufconnClient(t, fake)

	schedule, err := client.CreateJobSchedule(context.Background(), ScheduleSpec{IntervalSeconds: 3600}, JobSpec{
		ExistingSandboxID: "sbx-1",
		Cmd:               []string{"echo", "hi"},
	})
	if err != nil {
		t.Fatalf("CreateJobSchedule: %v", err)
	}
	if schedule.ScheduleID != "sched-1" {
		t.Errorf("schedule.ScheduleID = %q, want sched-1", schedule.ScheduleID)
	}
	if fake.lastCreateScheduleRequest.GetSchedule().GetIntervalSeconds() != 3600 {
		t.Errorf("service received interval_seconds = %d, want 3600", fake.lastCreateScheduleRequest.GetSchedule().GetIntervalSeconds())
	}
	if fake.lastCreateScheduleRequest.GetIdempotencyKey() == "" {
		t.Error("CreateJobSchedule did not generate an idempotency key")
	}
}

func TestGetJobScheduleNotFound(t *testing.T) {
	client := newBufconnClient(t, &fakeTApi{notFoundOnGetJobSchedule: true})

	_, err := client.GetJobSchedule(context.Background(), "sched-missing")
	var notFound *JobScheduleNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("GetJobSchedule error = %v, want *JobScheduleNotFoundError", err)
	}
}

func TestUpdateJobScheduleSendsFullReplacement(t *testing.T) {
	fake := &fakeTApi{}
	client := newBufconnClient(t, fake)

	_, err := client.UpdateJobSchedule(context.Background(), "sched-1", ScheduleSpec{IntervalSeconds: 7200}, JobSpec{
		ExistingSandboxID: "sbx-1",
		Cmd:               []string{"echo", "v2"},
	})
	if err != nil {
		t.Fatalf("UpdateJobSchedule: %v", err)
	}
	if fake.lastUpdateScheduleRequest.GetSchedule().GetIntervalSeconds() != 7200 {
		t.Errorf("service received interval_seconds = %d, want 7200", fake.lastUpdateScheduleRequest.GetSchedule().GetIntervalSeconds())
	}
}

func TestSetJobSchedulePaused(t *testing.T) {
	client := newBufconnClient(t, &fakeTApi{})

	schedule, err := client.SetJobSchedulePaused(context.Background(), "sched-1", true, "pausing for maintenance")
	if err != nil {
		t.Fatalf("SetJobSchedulePaused: %v", err)
	}
	if !schedule.Paused || schedule.Note != "pausing for maintenance" {
		t.Errorf("schedule = %+v", schedule)
	}
}

func TestTriggerAndDeleteJobSchedule(t *testing.T) {
	client := newBufconnClient(t, &fakeTApi{})

	if err := client.TriggerJobSchedule(context.Background(), "sched-1"); err != nil {
		t.Fatalf("TriggerJobSchedule: %v", err)
	}
	if err := client.DeleteJobSchedule(context.Background(), "sched-1"); err != nil {
		t.Fatalf("DeleteJobSchedule: %v", err)
	}
}

func TestListJobSchedulesMapsFields(t *testing.T) {
	fake := &fakeTApi{jobSchedules: []*runtimev1.TApiJobSchedule{
		{ScheduleId: "sched-1", Paused: false},
		{ScheduleId: "sched-2", Paused: true},
	}}
	client := newBufconnClient(t, fake)

	schedules, err := client.ListJobSchedules(context.Background())
	if err != nil {
		t.Fatalf("ListJobSchedules: %v", err)
	}
	if len(schedules) != 2 || schedules[0].ScheduleID != "sched-1" || !schedules[1].Paused {
		t.Errorf("schedules = %+v", schedules)
	}
}
