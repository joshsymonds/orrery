// Package route routes waiting and done notices to the desk and the phone, and logs each ping and silence.
package route

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Routes name where a notice went. None is a silence: nothing was sent.
const (
	None      = "none"
	Desk      = "desk"
	Phone     = "phone"
	DeskPhone = "desk+phone"
)

const (
	logDirPerm  = 0o700
	logFilePerm = 0o600
)

// Line is one line of the notice log: one ping or silence, as JSON.
type Line struct {
	Time    int64  `json:"time"`
	Host    string `json:"host"`
	Pane    string `json:"pane,omitempty"`
	Session string `json:"session,omitempty"`
	Epic    string `json:"epic,omitempty"`
	State   string `json:"state"`
	Route   string `json:"route"`
	Reason  string `json:"reason"`
}

// Log appends Lines to one file as JSON Lines. A Log is safe for concurrent use.
type Log struct {
	mu   sync.Mutex
	file *os.File
}

// OpenLog opens path for appending, creating it and its parent directory when absent.
func OpenLog(path string) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), logDirPerm); err != nil {
		return nil, fmt.Errorf("create notice log directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, logFilePerm)
	if err != nil {
		return nil, fmt.Errorf("open notice log: %w", err)
	}
	return &Log{file: file}, nil
}

// Write appends line to the log as one JSON object followed by a newline, in one write.
func (l *Log) Write(line Line) error {
	data, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("encode notice line: %w", err)
	}
	data = append(data, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err = l.file.Write(data); err != nil {
		return fmt.Errorf("write notice line: %w", err)
	}
	return nil
}

// Close closes the log file. Writes after Close return an error.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close notice log: %w", err)
	}
	return nil
}
