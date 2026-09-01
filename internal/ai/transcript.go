package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type transcriptEntry struct {
	Timestamp string `json:"ts"`
	Provider  string `json:"provider"`
	Model     string `json:"model,omitempty"`
	Attempt   int    `json:"attempt"`
	Prompt    string `json:"prompt"`
	Response  string `json:"response"`
	Error     string `json:"error,omitempty"`
}

type transcriptLogger struct {
	mu       sync.Mutex
	file     *os.File
	disabled bool
	warnOnce sync.Once
}

var processTranscript = &transcriptLogger{}

func (l *transcriptLogger) append(logDir string, entry transcriptEntry) {
	if logDir == "" {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.disabled {
		return
	}

	if l.file == nil {
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			l.disable(err)
			return
		}
		name := fmt.Sprintf("ai-log-%s-%d.jsonl", time.Now().Format("20060102-150405"), os.Getpid())
		file, err := os.OpenFile(filepath.Join(logDir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			l.disable(err)
			return
		}
		l.file = file
	}

	line, err := json.Marshal(entry)
	if err == nil {
		line = append(line, '\n')
		_, err = l.file.Write(line)
	}
	if err != nil {
		l.disable(err)
	}
}

func (l *transcriptLogger) disable(err error) {
	l.disabled = true
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	l.warnOnce.Do(func() {
		_, _ = fmt.Fprintf(os.Stderr, "WARN: AI transcript logging disabled: %v\n", err)
	})
}
