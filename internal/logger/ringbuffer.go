package logger

import (
	"bytes"
	"io"
	"sync"
)

type RingBuffer struct {
	mu       sync.RWMutex
	lines    []string
	capacity int
	head     int
	size     int
	partial  bytes.Buffer
}

func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 2000
	}
	return &RingBuffer{
		lines:    make([]string, capacity),
		capacity: capacity,
	}
}

func (r *RingBuffer) Write(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n = len(p)
	for _, b := range p {
		if b == '\n' {
			line := r.partial.String()
			r.partial.Reset()
			r.pushLineLocked(line)
		} else if b != '\r' {
			r.partial.WriteByte(b)
		}
	}
	return n, nil
}

func (r *RingBuffer) pushLineLocked(line string) {
	r.lines[r.head] = line
	r.head = (r.head + 1) % r.capacity
	if r.size < r.capacity {
		r.size++
	}
}

func (r *RingBuffer) Push(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pushLineLocked(line)
}

func (r *RingBuffer) GetLines(n int) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if n <= 0 || n > r.size {
		n = r.size
	}

	res := make([]string, n)
	start := (r.head - n + r.capacity) % r.capacity
	for i := 0; i < n; i++ {
		idx := (start + i) % r.capacity
		res[i] = r.lines[idx]
	}
	return res
}

func (r *RingBuffer) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.size
}

var _ io.Writer = (*RingBuffer)(nil)
