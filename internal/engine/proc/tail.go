package proc

import (
	"io"
	"sync"
)

// tail keeps the last capacity bytes written to it. It is safe for concurrent use.
type tail struct {
	mu       sync.Mutex
	capacity int
	data     []byte
}

func newTail(capacity int) *tail {
	return &tail{capacity: capacity, data: make([]byte, 0, capacity)}
}

// Write keeps the newest bytes and drops the oldest once more than capacity bytes have been written.
// It always reports the full length of data with a nil error.
func (t *tail) Write(data []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	written := len(data)
	if len(data) >= t.capacity {
		t.data = append(t.data[:0], data[len(data)-t.capacity:]...)
		return written, nil
	}
	overflow := len(t.data) + len(data) - t.capacity
	if overflow > 0 {
		copy(t.data, t.data[overflow:])
		t.data = t.data[:len(t.data)-overflow]
	}
	t.data = append(t.data, data...)
	return written, nil
}

// String returns the kept bytes in the order they were written.
func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.data)
}

var _ io.Writer = (*tail)(nil)
