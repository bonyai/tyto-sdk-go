package tyto

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	runtimev1 "buf.build/gen/go/bonya/tyto/protocolbuffers/go/tyto/runtime/v1"
)

// SandboxCollection is the entry point for creating, fetching, and listing sandboxes.
type SandboxCollection struct {
	client *Client
}

// CreateOptions configures SandboxCollection.Create.
type CreateOptions struct {
	// Version selects a template version. The server's default template
	// version is used when empty.
	Version string
	// Wait controls when Create returns. Defaults to WaitReady.
	Wait Wait
	// IdempotencyKey is sent to the service. If empty, the SDK generates one
	// and reuses it for create transport retries.
	IdempotencyKey string
	// Name is an optional display name, at most 80 bytes. When empty the
	// service generates a friendly one, returned on the resulting Sandbox.
	// Names are not unique.
	Name string
}

// Create starts a new sandbox from a template.
func (s *SandboxCollection) Create(ctx context.Context, template string, opts ...CreateOptions) (*Sandbox, error) {
	if template == "" {
		return nil, &InvalidRequestError{BaseError{Msg: "template is required"}}
	}
	var o CreateOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	wait := o.Wait
	if wait == "" {
		wait = WaitReady
	}
	if wait != WaitReady && wait != WaitNone {
		return nil, &InvalidRequestError{BaseError{Msg: "wait must be WaitReady or WaitNone"}}
	}
	key := o.IdempotencyKey
	if key == "" {
		key = generateIdempotencyKey()
	}

	protoWait := runtimev1.CreateWait_CREATE_WAIT_READY
	if wait == WaitNone {
		protoWait = runtimev1.CreateWait_CREATE_WAIT_NONE
	}
	request := &runtimev1.TApiServiceCreateRequest{
		ApiKey:         s.client.apiKey,
		IdempotencyKey: key,
		Template: &runtimev1.TemplateBinding{
			TemplateId: template,
			Version:    o.Version,
		},
		Wait: protoWait,
		Name: o.Name,
	}

	dl, err := startDeadline(s.client.timeout)
	if err != nil {
		return nil, err
	}
	attempts := 0
	backoff := 50 * time.Millisecond
	for {
		remaining, err := dl.remaining()
		if err != nil {
			return nil, &SandboxCreationTimeoutError{BaseError{Msg: err.Error(), IdempotencyKey: key}}
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := s.client.tapiClient()
		if tErr != nil {
			cancel()
			return nil, tErr
		}
		response, callErr := client.Create(callCtx, request)
		cancel()
		if callErr == nil {
			return sandboxFromCreate(s.client, response, wait, key)
		}
		if !IsRetryable(callErr) || attempts >= s.client.maxRetries {
			var timeoutErr *TimeoutError
			if errors.As(callErr, &timeoutErr) {
				return nil, &SandboxCreationTimeoutError{BaseError{Msg: timeoutErr.Msg, IdempotencyKey: key}}
			}
			mapped := MapRPCError(callErr, s.client.secrets(key), WithIdempotencyKey(key), WithCreate())
			var mappedTimeout *TimeoutError
			if errors.As(mapped, &mappedTimeout) {
				return nil, &SandboxCreationTimeoutError{BaseError{Msg: mappedTimeout.Msg, IdempotencyKey: key}}
			}
			return nil, mapped
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

// Get reconnects to an existing sandbox by ID.
func (s *SandboxCollection) Get(ctx context.Context, sandboxID string) (*Sandbox, error) {
	if sandboxID == "" {
		return nil, &InvalidRequestError{BaseError{Msg: "sandbox_id is required"}}
	}
	request := &runtimev1.TApiGetSandboxRequest{ApiKey: s.client.apiKey, SandboxId: sandboxID}

	dl, err := startDeadline(s.client.timeout)
	if err != nil {
		return nil, err
	}
	attempts := 0
	backoff := 50 * time.Millisecond
	for {
		remaining, err := dl.remaining()
		if err != nil {
			return nil, MapRPCError(err, s.client.secrets(), WithSandboxID(sandboxID))
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := s.client.tapiClient()
		if tErr != nil {
			cancel()
			return nil, tErr
		}
		response, callErr := client.GetSandbox(callCtx, request)
		cancel()
		if callErr == nil {
			return sandboxFromGet(s.client, response, sandboxID)
		}
		if !IsRetryable(callErr) || attempts >= s.client.maxRetries {
			return nil, MapRPCError(callErr, s.client.secrets(), WithSandboxID(sandboxID))
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

// GetByName reconnects to an existing sandbox by name.
//
// Names are not unique. This resolves the name to a single sandbox and then
// fetches it by ID, so it reports an error rather than guessing when the name
// matches more than one: picking one silently would make a later Delete
// destroy an arbitrary sandbox.
func (s *SandboxCollection) GetByName(ctx context.Context, name string) (*Sandbox, error) {
	if name == "" {
		return nil, &InvalidRequestError{BaseError{Msg: "name is required"}}
	}
	// Two is enough to tell "one match" from "more than one" without paging
	// the whole tenant.
	matches, err := s.List(ctx, ListOptions{Name: name, Limit: 2})
	if err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, &SandboxNotFoundError{BaseError{Msg: "no sandbox is named " + name}}
	case 1:
		return s.Get(ctx, matches[0].ID)
	default:
		return nil, &InvalidRequestError{BaseError{Msg: "more than one sandbox is named " + name +
			", including " + matches[0].ID + " and " + matches[1].ID + "; use Get with a sandbox id"}}
	}
}

// Delete deletes a sandbox by id in a single RPC, without first fetching a
// handle. It backs the flat Client.DeleteSandbox; Sandbox.Delete also calls
// through to this and additionally updates its own local state (LastObservedStatus,
// the local "already deleted" short-circuit) since it has a handle to update.
//
// Because there is no handle here, there is no local idempotency check: a
// second call always makes a second RPC, and the response's AlreadyDeleted
// reports what the server observed rather than what this SDK remembers.
func (s *SandboxCollection) Delete(ctx context.Context, sandboxID string) (*DeleteResult, error) {
	if sandboxID == "" {
		return nil, &InvalidRequestError{BaseError{Msg: "sandbox_id is required"}}
	}
	request := &runtimev1.TApiDeleteSandboxRequest{ApiKey: s.client.apiKey, SandboxId: sandboxID}

	dl, err := startDeadline(s.client.timeout)
	if err != nil {
		return nil, err
	}
	attempts := 0
	backoff := 50 * time.Millisecond
	for {
		remaining, err := dl.remaining()
		if err != nil {
			return nil, MapRPCError(err, s.client.secrets(), WithSandboxID(sandboxID))
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := s.client.tapiClient()
		if tErr != nil {
			cancel()
			return nil, tErr
		}
		response, callErr := client.DeleteSandbox(callCtx, request)
		cancel()
		if callErr == nil {
			resultID := response.GetSandboxId()
			if resultID == "" {
				resultID = sandboxID
			}
			return &DeleteResult{SandboxID: resultID, AlreadyDeleted: response.GetAlreadyDeleted()}, nil
		}
		if !IsRetryable(callErr) || attempts >= s.client.maxRetries {
			return nil, MapRPCError(callErr, s.client.secrets(), WithSandboxID(sandboxID))
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

// Resume resumes a sandbox by id in a single RPC, without first fetching a
// handle. It backs the flat Client.ResumeSandbox; Sandbox.Resume also calls
// through to the same RPC via resumeSandbox, additionally copying the
// refreshed capability and exec endpoint onto its own handle, since only a
// handle has those to update -- ResumeResult itself never carries them.
//
// Because there is no handle here, this does not check for a locally known
// failed status first the way Sandbox.Resume does -- the server is always
// asked, and a failed sandbox's rejection comes back as an ordinary RPC error.
func (s *SandboxCollection) Resume(ctx context.Context, sandboxID string, opts ...ResumeOptions) (*ResumeResult, error) {
	result, _, err := s.resumeSandbox(ctx, sandboxID, opts...)
	return result, err
}

// resumeSandbox is the one ResumeSandbox call site. It returns the raw
// response alongside the mapped ResumeResult so Sandbox.Resume can read the
// capability and exec endpoint fields ResumeResult does not expose, without
// a second implementation of the retry loop.
func (s *SandboxCollection) resumeSandbox(ctx context.Context, sandboxID string, opts ...ResumeOptions) (*ResumeResult, *runtimev1.TApiResumeSandboxResponse, error) {
	if sandboxID == "" {
		return nil, nil, &InvalidRequestError{BaseError{Msg: "sandbox_id is required"}}
	}
	var o ResumeOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	key := o.IdempotencyKey
	if key == "" {
		key = generateRandomToken()
	}
	request := &runtimev1.TApiResumeSandboxRequest{ApiKey: s.client.apiKey, SandboxId: sandboxID, IdempotencyKey: key}

	dl, err := startDeadline(s.client.timeout)
	if err != nil {
		return nil, nil, err
	}
	attempts := 0
	backoff := 50 * time.Millisecond
	for {
		remaining, err := dl.remaining()
		if err != nil {
			return nil, nil, MapRPCError(err, s.client.secrets(), WithSandboxID(sandboxID), WithIdempotencyKey(key))
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := s.client.tapiClient()
		if tErr != nil {
			cancel()
			return nil, nil, tErr
		}
		response, callErr := client.ResumeSandbox(callCtx, request)
		cancel()
		if callErr == nil {
			resultID := response.GetSandboxId()
			if resultID == "" {
				resultID = sandboxID
			}
			return &ResumeResult{
				SandboxID:            resultID,
				LifecycleOperationID: response.GetLifecycleOperationId(),
				AlreadyRunning:       response.GetAlreadyRunning(),
			}, response, nil
		}
		if !IsRetryable(callErr) || attempts >= s.client.maxRetries {
			return nil, nil, MapRPCError(callErr, s.client.secrets(), WithSandboxID(sandboxID), WithIdempotencyKey(key))
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

// ListOptions configures SandboxCollection.List.
type ListOptions struct {
	// States filters results to the given lifecycle states. StatusDeleted is
	// not a valid filter. Empty means no filter.
	States []Status
	// Limit caps the number of summaries returned. 0 means unlimited; a
	// nil/absent Limit is treated the same as 0 (unlimited) since Go has no
	// natural "unset" for an int without a pointer -- pass a Limit only when
	// you want to cap results.
	Limit int
	// Name filters to sandboxes carrying this exact name. Names are not
	// unique, so this can still match more than one. Empty means no filter.
	Name string
}

// List fetches sandbox summaries, paging internally as needed, and returns
// them as a single slice. A zero Limit returns every matching sandbox.
func (s *SandboxCollection) List(ctx context.Context, opts ...ListOptions) ([]SandboxSummary, error) {
	var o ListOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Limit < 0 {
		return nil, &InvalidRequestError{BaseError{Msg: "limit must be a non-negative integer"}}
	}
	stateValues, err := normalizeStateFilters(o.States)
	if err != nil {
		return nil, err
	}

	var results []SandboxSummary
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
		request := &runtimev1.TApiListSandboxesRequest{
			ApiKey:    s.client.apiKey,
			States:    stateValues,
			PageSize:  pageSize,
			PageToken: pageToken,
			Name:      o.Name,
		}

		dl, err := startDeadline(s.client.timeout)
		if err != nil {
			return nil, err
		}
		attempts := 0
		backoff := 50 * time.Millisecond
		var response *runtimev1.TApiListSandboxesResponse
		for {
			remaining, err := dl.remaining()
			if err != nil {
				return nil, MapRPCError(err, s.client.secrets(pageToken))
			}
			callCtx, cancel := context.WithTimeout(ctx, remaining)
			client, tErr := s.client.tapiClient()
			if tErr != nil {
				cancel()
				return nil, tErr
			}
			resp, callErr := client.ListSandboxes(callCtx, request)
			cancel()
			if callErr == nil {
				response = resp
				break
			}
			if !IsRetryable(callErr) || attempts >= s.client.maxRetries {
				return nil, MapRPCError(callErr, s.client.secrets(pageToken))
			}
			attempts++
			sleepWithDeadline(ctx, backoff, dl)
			backoff = minDuration(backoff*2, 500*time.Millisecond)
		}

		for _, sandbox := range response.GetSandboxes() {
			if o.Limit > 0 && len(results) >= o.Limit {
				return results, nil
			}
			summary, err := summaryFromMetadata(sandbox)
			if err != nil {
				return nil, err
			}
			results = append(results, summary)
		}
		pageToken = response.GetNextPageToken()
		if pageToken == "" || (o.Limit > 0 && len(results) >= o.Limit) {
			return results, nil
		}
	}
}

func generateIdempotencyKey() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func sandboxFromCreate(client *Client, response *runtimev1.TApiServiceCreateResponse, wait Wait, key string) (*Sandbox, error) {
	sandboxID := response.GetSandboxId()
	operationID := response.GetOperationId()
	if sandboxID == "" || operationID == "" {
		return nil, &InvalidRequestError{BaseError{Msg: "Create response is missing sandbox identity", IdempotencyKey: key}}
	}
	execEndpoint := response.GetExecEndpoint()
	if execEndpoint == "" {
		return nil, &InvalidRequestError{BaseError{Msg: "Create response is missing exec_endpoint", SandboxID: sandboxID, OperationID: operationID, IdempotencyKey: key}}
	}
	capability := response.GetExecCapabilityJws()
	if capability == "" {
		return nil, &InvalidRequestError{BaseError{Msg: "Create response is missing exec capability", SandboxID: sandboxID, OperationID: operationID, IdempotencyKey: key}}
	}
	if terminal := response.GetTerminal(); terminal != nil && terminal.GetState() == runtimev1.TerminalState_TERMINAL_STATE_FAILED {
		msg := terminal.GetMessage()
		if msg == "" {
			msg = "sandbox creation failed"
		}
		return nil, &SandboxCreationFailedError{BaseError{Msg: msg, SandboxID: sandboxID, OperationID: operationID, IdempotencyKey: key}}
	}

	status := StatusCreating
	if wait == WaitReady {
		status = StatusRunning
	}
	return newSandbox(client, sandboxCreateArgs{
		sandboxID:    sandboxID,
		operationID:  operationID,
		template:     response.GetResolvedTemplateId(),
		version:      response.GetResolvedTemplateVersion(),
		status:       status,
		execEndpoint: execEndpoint,
		capability:   capability,
		name:         response.GetName(),
	}), nil
}

func sandboxFromGet(client *Client, response *runtimev1.TApiGetSandboxResponse, requestedSandboxID string) (*Sandbox, error) {
	metadata := response.GetSandbox()
	if metadata == nil {
		return nil, &InvalidRequestError{BaseError{Msg: "GetSandbox response is missing sandbox metadata", SandboxID: requestedSandboxID}}
	}
	sandboxID := metadata.GetSandboxId()
	operationID := metadata.GetOperationId()
	if sandboxID == "" || operationID == "" {
		return nil, &InvalidRequestError{BaseError{Msg: "GetSandbox response is missing sandbox identity", SandboxID: requestedSandboxID}}
	}
	status, err := terminalStateToStatus(metadata.GetObserved().GetState())
	if err != nil {
		return nil, err
	}
	if status == StatusFailed {
		return newSandbox(client, sandboxCreateArgs{
			sandboxID:      sandboxID,
			operationID:    operationID,
			template:       metadata.GetResolvedTemplateId(),
			version:        metadata.GetResolvedTemplateVersion(),
			status:         status,
			failureCode:    metadata.GetObserved().GetCode(),
			failureMessage: metadata.GetObserved().GetMessage(),
			name:           metadata.GetName(),
		}), nil
	}
	execEndpoint := response.GetExecEndpoint()
	if execEndpoint == "" {
		return nil, &InvalidRequestError{BaseError{Msg: "GetSandbox response is missing exec_endpoint", SandboxID: sandboxID, OperationID: operationID}}
	}
	capability := response.GetExecCapabilityJws()
	if capability == "" {
		return nil, &InvalidRequestError{BaseError{Msg: "GetSandbox response is missing exec capability", SandboxID: sandboxID, OperationID: operationID}}
	}
	return newSandbox(client, sandboxCreateArgs{
		sandboxID:    sandboxID,
		operationID:  operationID,
		template:     metadata.GetResolvedTemplateId(),
		version:      metadata.GetResolvedTemplateVersion(),
		status:       status,
		execEndpoint: execEndpoint,
		capability:   capability,
		name:         metadata.GetName(),
	}), nil
}

func summaryFromMetadata(metadata *runtimev1.TApiSandboxMetadata) (SandboxSummary, error) {
	status, err := terminalStateToStatus(metadata.GetObserved().GetState())
	if err != nil {
		return SandboxSummary{}, err
	}
	return SandboxSummary{
		ID:                 metadata.GetSandboxId(),
		OperationID:        metadata.GetOperationId(),
		Template:           metadata.GetResolvedTemplateId(),
		Version:            metadata.GetResolvedTemplateVersion(),
		LastObservedStatus: status,
		FailureCode:        metadata.GetObserved().GetCode(),
		FailureMessage:     metadata.GetObserved().GetMessage(),
		Name:               metadata.GetName(),
	}, nil
}

func terminalStateToStatus(state runtimev1.TerminalState) (Status, error) {
	switch state {
	case runtimev1.TerminalState_TERMINAL_STATE_CREATING:
		return StatusCreating, nil
	case runtimev1.TerminalState_TERMINAL_STATE_RUNNING:
		return StatusRunning, nil
	case runtimev1.TerminalState_TERMINAL_STATE_SUSPENDING:
		return StatusSuspending, nil
	case runtimev1.TerminalState_TERMINAL_STATE_SUSPENDED:
		return StatusSuspended, nil
	case runtimev1.TerminalState_TERMINAL_STATE_RESUMING:
		return StatusResuming, nil
	case runtimev1.TerminalState_TERMINAL_STATE_FAILED:
		return StatusFailed, nil
	case runtimev1.TerminalState_TERMINAL_STATE_DELETED:
		return StatusDeleted, nil
	default:
		return "", &InvalidRequestError{BaseError{Msg: "sandbox metadata contained an unsupported state"}}
	}
}

func statusToTerminalState(status Status) (runtimev1.TerminalState, error) {
	switch status {
	case StatusCreating:
		return runtimev1.TerminalState_TERMINAL_STATE_CREATING, nil
	case StatusRunning:
		return runtimev1.TerminalState_TERMINAL_STATE_RUNNING, nil
	case StatusSuspending:
		return runtimev1.TerminalState_TERMINAL_STATE_SUSPENDING, nil
	case StatusSuspended:
		return runtimev1.TerminalState_TERMINAL_STATE_SUSPENDED, nil
	case StatusResuming:
		return runtimev1.TerminalState_TERMINAL_STATE_RESUMING, nil
	case StatusFailed:
		return runtimev1.TerminalState_TERMINAL_STATE_FAILED, nil
	default:
		return 0, &InvalidRequestError{BaseError{Msg: "states must contain a valid Status value"}}
	}
}

func normalizeStateFilters(states []Status) ([]runtimev1.TerminalState, error) {
	if len(states) == 0 {
		return nil, nil
	}
	values := make([]runtimev1.TerminalState, 0, len(states))
	for _, state := range states {
		if state == StatusDeleted {
			return nil, &InvalidRequestError{BaseError{Msg: "StatusDeleted is not a valid list filter"}}
		}
		value, err := statusToTerminalState(state)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}
