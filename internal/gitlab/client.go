package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"

	"mrinspect/internal/config"
	"mrinspect/internal/logger"
)

type RetryConfig struct {
	Attempts   int
	DelayMs    int
	MaxDelayMs int
	TimeoutMs  int
}

type Client struct {
	httpClient *http.Client
	token      string
	apiBase    string
	retry      RetryConfig
	log        *logger.Logger
}

func NewClient(cfg config.Config, log *logger.Logger) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.API.TimeoutMs) * time.Millisecond,
		},
		token:   cfg.GitLabToken,
		apiBase: cfg.GitLabAPIBase,
		retry: RetryConfig{
			Attempts:   cfg.API.RetryAttempts,
			DelayMs:    cfg.API.RetryDelayMs,
			MaxDelayMs: cfg.API.MaxRetryDelayMs,
			TimeoutMs:  cfg.API.TimeoutMs,
		},
		log: log,
	}
}

// CurrentUser returns the user associated with the configured token.
func (c *Client) CurrentUser(ctx context.Context) (Author, error) {
	var author Author
	if err := c.getJSON(ctx, "/user", &author); err != nil {
		return Author{}, fmt.Errorf("CurrentUser: %w", err)
	}
	return author, nil
}

func (c *Client) GetMergeRequest(ctx context.Context, projectID, mrIID string) (MergeRequest, error) {
	path := fmt.Sprintf("/projects/%s/merge_requests/%s", projectID, mrIID)
	var mr MergeRequest
	if err := c.getJSON(ctx, path, &mr); err != nil {
		return MergeRequest{}, fmt.Errorf("GetMergeRequest: %w", err)
	}
	return mr, nil
}

func (c *Client) GetMRChanges(ctx context.Context, projectID, mrIID string) (MRChangesResponse, error) {
	path := fmt.Sprintf("/projects/%s/merge_requests/%s/changes", projectID, mrIID)
	var resp MRChangesResponse
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return MRChangesResponse{}, fmt.Errorf("GetMRChanges: %w", err)
	}
	return resp, nil
}

func (c *Client) PostNote(ctx context.Context, projectID, mrIID, body string) (Note, error) {
	path := fmt.Sprintf("/projects/%s/merge_requests/%s/notes", projectID, mrIID)
	payload := map[string]string{"body": body}
	var note Note
	if err := c.postJSON(ctx, path, payload, &note); err != nil {
		return Note{}, fmt.Errorf("PostNote: %w", err)
	}
	return note, nil
}

func (c *Client) ListNotes(ctx context.Context, projectID, mrIID string) ([]Note, error) {
	path := fmt.Sprintf("/projects/%s/merge_requests/%s/notes", projectID, mrIID)
	var notes []Note
	nextPage := ""

	for {
		requestPath := path + "?per_page=100"
		if nextPage != "" {
			requestPath += "&page=" + url.QueryEscape(nextPage)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+requestPath, nil)
		if err != nil {
			return nil, fmt.Errorf("ListNotes: %w", err)
		}
		req.Header.Set("PRIVATE-TOKEN", c.token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.doWithRetry(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("ListNotes: %w", err)
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("ListNotes: %w", readErr)
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("ListNotes: gitlab API error %d: %s", resp.StatusCode, string(data))
		}

		var page []Note
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, fmt.Errorf("ListNotes: %w", err)
		}
		notes = append(notes, page...)
		nextPage = resp.Header.Get("X-Next-Page")
		if nextPage == "" {
			return notes, nil
		}
	}
}

func (c *Client) UpdateNote(ctx context.Context, projectID, mrIID string, noteID int, body string) (Note, error) {
	path := fmt.Sprintf("/projects/%s/merge_requests/%s/notes/%d", projectID, mrIID, noteID)
	payload := map[string]string{"body": body}
	var note Note
	if err := c.putJSON(ctx, path, payload, &note); err != nil {
		return Note{}, fmt.Errorf("UpdateNote: %w", err)
	}
	return note, nil
}

func (c *Client) HealthCheck(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+"/version", nil)
	if err != nil {
		return false
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doWithRetry(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("gitlab API error %d: %s", resp.StatusCode, string(data))
	}
	return json.Unmarshal(data, out)
}

func (c *Client) postJSON(ctx context.Context, path string, payload, out any) error {
	return c.writeJSON(ctx, http.MethodPost, path, payload, out)
}

func (c *Client) putJSON(ctx context.Context, path string, payload, out any) error {
	return c.writeJSON(ctx, http.MethodPut, path, payload, out)
}

func (c *Client) writeJSON(ctx context.Context, method, path string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.apiBase+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doWithRetry(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("gitlab API error %d: %s", resp.StatusCode, string(data))
	}
	return json.Unmarshal(data, out)
}

func (c *Client) doWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	var (
		resp *http.Response
		err  error
	)
	for attempt := 0; attempt <= c.retry.Attempts; attempt++ {
		if attempt > 0 {
			delay := time.Duration(math.Min(
				float64(c.retry.DelayMs)*math.Pow(2, float64(attempt-1)),
				float64(c.retry.MaxDelayMs),
			)) * time.Millisecond

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}

			// Re-create the body reader for request retries.
			if req.GetBody != nil {
				req.Body, _ = req.GetBody()
			}
		}

		start := time.Now()
		resp, err = c.httpClient.Do(req)
		dur := time.Since(start).Milliseconds()

		if err != nil {
			c.log.LogAPICall("gitlab", req.URL.Path, dur, false, err)
			continue
		}

		if resp.StatusCode < 500 {
			c.log.LogAPICall("gitlab", req.URL.Path, dur, resp.StatusCode < 400, nil)
			return resp, nil
		}

		// 5xx — retry
		resp.Body.Close()
		c.log.LogAPICall("gitlab", req.URL.Path, dur, false,
			fmt.Errorf("status %d", resp.StatusCode))
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("gitlab: exceeded retry attempts for %s", req.URL.Path)
}
