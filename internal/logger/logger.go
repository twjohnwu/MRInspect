package logger

import (
	"encoding/json"
	"log/slog"
	"os"
	"time"
)

type APICallMetric struct {
	Service    string `json:"service"`
	Endpoint   string `json:"endpoint"`
	DurationMs int64  `json:"durationMs"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	Timestamp  int64  `json:"timestamp"`
}

type StepMetric struct {
	Step       string         `json:"step"`
	DurationMs *int64         `json:"durationMs,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Timestamp  int64          `json:"timestamp"`
}

type ErrorMetric struct {
	Type      string `json:"type"`
	Stage     string `json:"stage"`
	Category  string `json:"category"`
	Error     string `json:"error"`
	Timestamp int64  `json:"timestamp"`
}

type Metrics struct {
	StartTimeMs int64           `json:"startTimeMs"`
	EndTimeMs   int64           `json:"endTimeMs,omitempty"`
	MrID        int             `json:"mrId,omitempty"`
	ProjectID   int             `json:"projectId,omitempty"`
	Success     bool            `json:"success"`
	TotalDurMs  int64           `json:"totalDurationMs,omitempty"`
	APICalls    []APICallMetric `json:"apiCalls"`
	Steps       []StepMetric    `json:"steps"`
	Errors      []ErrorMetric   `json:"errors"`
}

type ReviewSummary struct {
	TotalDuration string `json:"totalDuration"`
	APICalls      struct {
		Total      int   `json:"total"`
		Successful int   `json:"successful"`
		Failed     int   `json:"failed"`
		AvgDurMs   int64 `json:"avgDurationMs"`
	} `json:"apiCalls"`
	Errors int `json:"errors"`
	Steps  int `json:"steps"`
}

type Logger struct {
	slog        *slog.Logger
	metrics     Metrics
	metricsFile string
}

func New(level slog.Level, metricsFile string) *Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return &Logger{
		slog:        slog.New(handler),
		metricsFile: metricsFile,
		metrics: Metrics{
			APICalls: []APICallMetric{},
			Steps:    []StepMetric{},
			Errors:   []ErrorMetric{},
		},
	}
}

func (l *Logger) Info(msg string, args ...any) {
	l.slog.Info(msg, args...)
}

func (l *Logger) Warn(msg string, args ...any) {
	l.slog.Warn(msg, args...)
}

func (l *Logger) Error(msg string, args ...any) {
	l.slog.Error(msg, args...)
}

func (l *Logger) Debug(msg string, args ...any) {
	l.slog.Debug(msg, args...)
}

func (l *Logger) StartReview(mrID, projectID int) {
	l.metrics.StartTimeMs = time.Now().UnixMilli()
	l.metrics.MrID = mrID
	l.metrics.ProjectID = projectID
}

func (l *Logger) LogStep(step string, durationMs *int64, meta map[string]any) {
	l.metrics.Steps = append(l.metrics.Steps, StepMetric{
		Step:       step,
		DurationMs: durationMs,
		Metadata:   meta,
		Timestamp:  time.Now().UnixMilli(),
	})
}

func (l *Logger) LogAPICall(service, endpoint string, durationMs int64, success bool, err error) {
	m := APICallMetric{
		Service:    service,
		Endpoint:   endpoint,
		DurationMs: durationMs,
		Success:    success,
		Timestamp:  time.Now().UnixMilli(),
	}
	if err != nil {
		m.Error = err.Error()
	}
	l.metrics.APICalls = append(l.metrics.APICalls, m)
}

func (l *Logger) LogError(errType, stage, category string, err error) {
	l.metrics.Errors = append(l.metrics.Errors, ErrorMetric{
		Type:      errType,
		Stage:     stage,
		Category:  category,
		Error:     err.Error(),
		Timestamp: time.Now().UnixMilli(),
	})
}

func (l *Logger) CompleteReview(success bool, finalErr error) ReviewSummary {
	now := time.Now().UnixMilli()
	l.metrics.EndTimeMs = now
	l.metrics.Success = success
	l.metrics.TotalDurMs = now - l.metrics.StartTimeMs

	summary := ReviewSummary{
		TotalDuration: formatDuration(l.metrics.TotalDurMs),
		Errors:        len(l.metrics.Errors),
		Steps:         len(l.metrics.Steps),
	}
	summary.APICalls.Total = len(l.metrics.APICalls)
	var totalDur int64
	for _, c := range l.metrics.APICalls {
		if c.Success {
			summary.APICalls.Successful++
		} else {
			summary.APICalls.Failed++
		}
		totalDur += c.DurationMs
	}
	if summary.APICalls.Total > 0 {
		summary.APICalls.AvgDurMs = totalDur / int64(summary.APICalls.Total)
	}
	return summary
}

func (l *Logger) SaveMetrics() error {
	var history []Metrics
	if data, err := os.ReadFile(l.metricsFile); err == nil {
		_ = json.Unmarshal(data, &history)
	}
	history = append(history, l.metrics)
	if len(history) > 100 {
		history = history[len(history)-100:]
	}
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(l.metricsFile, data, 0644)
}

func formatDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Minute {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}
