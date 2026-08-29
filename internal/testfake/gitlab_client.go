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

// ListNotesResponse is one programmed result from FakeGitLabClient.ListNotes.
type ListNotesResponse struct {
	Notes []gitlab.Note
	Err   error
	Delay time.Duration
}

// UpdateNoteResponse is one programmed result from FakeGitLabClient.UpdateNote.
type UpdateNoteResponse struct {
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

// ListNotesCall records the arguments of one ListNotes call.
type ListNotesCall struct {
	Context   context.Context
	ProjectID string
	MRIID     string
}

// UpdateNoteCall records the arguments of one UpdateNote call.
type UpdateNoteCall struct {
	Context   context.Context
	ProjectID string
	MRIID     string
	NoteID    int
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
	ListNotesResponses          []ListNotesResponse
	DefaultListNotesResponse    ListNotesResponse
	PostNoteResponses           []PostNoteResponse
	DefaultPostNoteResponse     PostNoteResponse
	UpdateNoteResponses         []UpdateNoteResponse
	DefaultUpdateNoteResponse   UpdateNoteResponse

	mu                sync.Mutex
	healthCheckIndex  int
	mergeRequestIndex int
	mrChangesIndex    int
	listNotesIndex    int
	postNoteIndex     int
	updateNoteIndex   int
	healthCheckCalls  []HealthCheckCall
	mergeRequestCalls []MergeRequestCall
	mrChangesCalls    []MRChangesCall
	listNotesCalls    []ListNotesCall
	postNoteCalls     []PostNoteCall
	updateNoteCalls   []UpdateNoteCall
}

// CurrentUser is a T11 RED compile-only stub.
func (*FakeGitLabClient) CurrentUser(context.Context) (gitlab.Author, error) {
	return gitlab.Author{}, nil
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

// ListNotes records its arguments and returns the next programmed response.
func (f *FakeGitLabClient) ListNotes(ctx context.Context, projectID, mrIID string) ([]gitlab.Note, error) {
	f.mu.Lock()
	f.listNotesCalls = append(f.listNotesCalls, ListNotesCall{
		Context: ctx, ProjectID: projectID, MRIID: mrIID,
	})
	response := f.DefaultListNotesResponse
	if f.listNotesIndex < len(f.ListNotesResponses) {
		response = f.ListNotesResponses[f.listNotesIndex]
		f.listNotesIndex++
	}
	f.mu.Unlock()

	if err := waitContext(ctx, response.Delay); err != nil {
		return nil, err
	}
	return append([]gitlab.Note(nil), response.Notes...), response.Err
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

// UpdateNote records its arguments and returns the next programmed response.
func (f *FakeGitLabClient) UpdateNote(ctx context.Context, projectID, mrIID string, noteID int, body string) (gitlab.Note, error) {
	f.mu.Lock()
	f.updateNoteCalls = append(f.updateNoteCalls, UpdateNoteCall{
		Context: ctx, ProjectID: projectID, MRIID: mrIID, NoteID: noteID, Body: body,
	})
	response := f.DefaultUpdateNoteResponse
	if f.updateNoteIndex < len(f.UpdateNoteResponses) {
		response = f.UpdateNoteResponses[f.updateNoteIndex]
		f.updateNoteIndex++
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

// EnqueueListNotesResponses appends responses to the ListNotes response queue.
func (f *FakeGitLabClient) EnqueueListNotesResponses(responses ...ListNotesResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ListNotesResponses = append(f.ListNotesResponses, responses...)
}

// EnqueuePostNoteResponses appends responses to the PostNote response queue.
func (f *FakeGitLabClient) EnqueuePostNoteResponses(responses ...PostNoteResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.PostNoteResponses = append(f.PostNoteResponses, responses...)
}

// EnqueueUpdateNoteResponses appends responses to the UpdateNote response queue.
func (f *FakeGitLabClient) EnqueueUpdateNoteResponses(responses ...UpdateNoteResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.UpdateNoteResponses = append(f.UpdateNoteResponses, responses...)
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

// ListNotesCallCount returns the number of ListNotes calls.
func (f *FakeGitLabClient) ListNotesCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.listNotesCalls)
}

// ListNotesCalls returns a snapshot of recorded ListNotes calls.
func (f *FakeGitLabClient) ListNotesCalls() []ListNotesCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ListNotesCall(nil), f.listNotesCalls...)
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

// UpdateNoteCallCount returns the number of UpdateNote calls.
func (f *FakeGitLabClient) UpdateNoteCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.updateNoteCalls)
}

// UpdateNoteCalls returns a snapshot of recorded UpdateNote calls.
func (f *FakeGitLabClient) UpdateNoteCalls() []UpdateNoteCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]UpdateNoteCall(nil), f.updateNoteCalls...)
}

func cloneMRChanges(changes gitlab.MRChangesResponse) gitlab.MRChangesResponse {
	changes.Changes = append([]gitlab.Change(nil), changes.Changes...)
	return changes
}

var _ interfaces.IGitLabClient = (*FakeGitLabClient)(nil)
