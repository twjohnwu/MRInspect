package testfake

import (
	"context"
	"sync"
	"time"

	"mrinspect/internal/gitlab"
	"mrinspect/internal/interfaces"
)

// HealthCheckResponse is one programmed result from FakeGitLabClient.HealthCheck.
type HealthCheckResponse struct {
	Healthy bool
	Delay   time.Duration
}

// MergeRequestResponse is one programmed result from FakeGitLabClient.GetMergeRequest.
type MergeRequestResponse struct {
	MergeRequest gitlab.MergeRequest
	Err          error
	Delay        time.Duration
}

// MRChangesResponse is one programmed result from FakeGitLabClient.GetMRChanges.
type MRChangesResponse struct {
	Changes gitlab.MRChangesResponse
	Err     error
	Delay   time.Duration
}

// PostNoteResponse is one programmed result from FakeGitLabClient.PostNote.
type PostNoteResponse struct {
	Note  gitlab.Note
	Err   error
	Delay time.Duration
}

// HealthCheckCall records the context of one HealthCheck call.
type HealthCheckCall struct {
	Context context.Context
}

// MergeRequestCall records the arguments of one GetMergeRequest call.
type MergeRequestCall struct {
	Context   context.Context
	ProjectID string
	MRIID     string
}

// MRChangesCall records the arguments of one GetMRChanges call.
type MRChangesCall struct {
	Context   context.Context
	ProjectID string
	MRIID     string
}

// PostNoteCall records the arguments of one PostNote call.
type PostNoteCall struct {
	Context   context.Context
	ProjectID string
	MRIID     string
	Body      string
}

// FakeGitLabClient is a programmable, concurrency-safe IGitLabClient test double.
// Each operation has an independent response queue and configurable default.
// Configure exported fields before use; use enqueue methods while calls are in flight.
type FakeGitLabClient struct {
	HealthCheckResponses        []HealthCheckResponse
	DefaultHealthCheckResponse  HealthCheckResponse
	MergeRequestResponses       []MergeRequestResponse
	DefaultMergeRequestResponse MergeRequestResponse
	MRChangesResponses          []MRChangesResponse
	DefaultMRChangesResponse    MRChangesResponse
	PostNoteResponses           []PostNoteResponse
	DefaultPostNoteResponse     PostNoteResponse

	mu                sync.Mutex
	healthCheckIndex  int
	mergeRequestIndex int
	mrChangesIndex    int
	postNoteIndex     int
	healthCheckCalls  []HealthCheckCall
	mergeRequestCalls []MergeRequestCall
	mrChangesCalls    []MRChangesCall
	postNoteCalls     []PostNoteCall
}

// HealthCheck records its context and returns the next programmed response.
func (f *FakeGitLabClient) HealthCheck(ctx context.Context) bool {
	f.mu.Lock()
	f.healthCheckCalls = append(f.healthCheckCalls, HealthCheckCall{Context: ctx})
	response := f.DefaultHealthCheckResponse
	if f.healthCheckIndex < len(f.HealthCheckResponses) {
		response = f.HealthCheckResponses[f.healthCheckIndex]
		f.healthCheckIndex++
	}
	f.mu.Unlock()

	return waitContext(ctx, response.Delay) == nil && response.Healthy
}

// GetMergeRequest records its arguments and returns the next programmed response.
func (f *FakeGitLabClient) GetMergeRequest(ctx context.Context, projectID, mrIID string) (gitlab.MergeRequest, error) {
	f.mu.Lock()
	f.mergeRequestCalls = append(f.mergeRequestCalls, MergeRequestCall{
		Context: ctx, ProjectID: projectID, MRIID: mrIID,
	})
	response := f.DefaultMergeRequestResponse
	if f.mergeRequestIndex < len(f.MergeRequestResponses) {
		response = f.MergeRequestResponses[f.mergeRequestIndex]
		f.mergeRequestIndex++
	}
	f.mu.Unlock()

	if err := waitContext(ctx, response.Delay); err != nil {
		return gitlab.MergeRequest{}, err
	}
	return response.MergeRequest, response.Err
}

// GetMRChanges records its arguments and returns the next programmed response.
func (f *FakeGitLabClient) GetMRChanges(ctx context.Context, projectID, mrIID string) (gitlab.MRChangesResponse, error) {
	f.mu.Lock()
	f.mrChangesCalls = append(f.mrChangesCalls, MRChangesCall{
		Context: ctx, ProjectID: projectID, MRIID: mrIID,
	})
	response := f.DefaultMRChangesResponse
	if f.mrChangesIndex < len(f.MRChangesResponses) {
		response = f.MRChangesResponses[f.mrChangesIndex]
		f.mrChangesIndex++
	}
	f.mu.Unlock()

	if err := waitContext(ctx, response.Delay); err != nil {
		return gitlab.MRChangesResponse{}, err
	}
	return cloneMRChanges(response.Changes), response.Err
}

// PostNote records its arguments and returns the next programmed response.
func (f *FakeGitLabClient) PostNote(ctx context.Context, projectID, mrIID, body string) (gitlab.Note, error) {
	f.mu.Lock()
	f.postNoteCalls = append(f.postNoteCalls, PostNoteCall{
		Context: ctx, ProjectID: projectID, MRIID: mrIID, Body: body,
	})
	response := f.DefaultPostNoteResponse
	if f.postNoteIndex < len(f.PostNoteResponses) {
		response = f.PostNoteResponses[f.postNoteIndex]
		f.postNoteIndex++
	}
	f.mu.Unlock()

	if err := waitContext(ctx, response.Delay); err != nil {
		return gitlab.Note{}, err
	}
	return response.Note, response.Err
}

// EnqueueHealthCheckResponses appends responses to the HealthCheck response queue.
func (f *FakeGitLabClient) EnqueueHealthCheckResponses(responses ...HealthCheckResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.HealthCheckResponses = append(f.HealthCheckResponses, responses...)
}

// EnqueueMergeRequestResponses appends responses to the GetMergeRequest response queue.
func (f *FakeGitLabClient) EnqueueMergeRequestResponses(responses ...MergeRequestResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.MergeRequestResponses = append(f.MergeRequestResponses, responses...)
}

// EnqueueMRChangesResponses appends responses to the GetMRChanges response queue.
func (f *FakeGitLabClient) EnqueueMRChangesResponses(responses ...MRChangesResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.MRChangesResponses = append(f.MRChangesResponses, responses...)
}

// EnqueuePostNoteResponses appends responses to the PostNote response queue.
func (f *FakeGitLabClient) EnqueuePostNoteResponses(responses ...PostNoteResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.PostNoteResponses = append(f.PostNoteResponses, responses...)
}

// HealthCheckCallCount returns the number of HealthCheck calls.
func (f *FakeGitLabClient) HealthCheckCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.healthCheckCalls)
}

// HealthCheckCalls returns a snapshot of recorded HealthCheck calls.
func (f *FakeGitLabClient) HealthCheckCalls() []HealthCheckCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]HealthCheckCall(nil), f.healthCheckCalls...)
}

// GetMergeRequestCallCount returns the number of GetMergeRequest calls.
func (f *FakeGitLabClient) GetMergeRequestCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.mergeRequestCalls)
}

// GetMergeRequestCalls returns a snapshot of recorded GetMergeRequest calls.
func (f *FakeGitLabClient) GetMergeRequestCalls() []MergeRequestCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]MergeRequestCall(nil), f.mergeRequestCalls...)
}

// GetMRChangesCallCount returns the number of GetMRChanges calls.
func (f *FakeGitLabClient) GetMRChangesCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.mrChangesCalls)
}

// GetMRChangesCalls returns a snapshot of recorded GetMRChanges calls.
func (f *FakeGitLabClient) GetMRChangesCalls() []MRChangesCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]MRChangesCall(nil), f.mrChangesCalls...)
}

// PostNoteCallCount returns the number of PostNote calls.
func (f *FakeGitLabClient) PostNoteCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.postNoteCalls)
}

// PostNoteCalls returns a snapshot of recorded PostNote calls.
func (f *FakeGitLabClient) PostNoteCalls() []PostNoteCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]PostNoteCall(nil), f.postNoteCalls...)
}

func cloneMRChanges(changes gitlab.MRChangesResponse) gitlab.MRChangesResponse {
	changes.Changes = append([]gitlab.Change(nil), changes.Changes...)
	return changes
}

var _ interfaces.IGitLabClient = (*FakeGitLabClient)(nil)
