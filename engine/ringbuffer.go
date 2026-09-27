package engine

import (
	"sync"
)

// RingBuffer is a thread-safe circular memory buffer of fixed size implementing io.Writer.
type RingBuffer struct {
	mu   sync.Mutex
	data []byte
	size int
	pos  int
	full bool
}

// NewRingBuffer creates a new RingBuffer of the given size in bytes.
func NewRingBuffer(size int) *RingBuffer {
	if size <= 0 {
		size = 8 * 1024 // default 8KB
	}
	return &RingBuffer{
		data: make([]byte, size),
		size: size,
	}
}

func (r *RingBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := len(p)
	if n == 0 {
		return 0, nil
	}

	if n >= r.size {
		copy(r.data, p[n-r.size:])
		r.pos = 0
		r.full = true
		return n, nil
	}

	space := r.size - r.pos
	if n <= space {
		copy(r.data[r.pos:], p)
		r.pos += n
		if r.pos == r.size {
			r.pos = 0
			r.full = true
		}
	} else {
		copy(r.data[r.pos:], p[:space])
		copy(r.data, p[space:])
		r.pos = n - space
		r.full = true
	}

	return n, nil
}

func (r *RingBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.full {
		out := make([]byte, r.pos)
		copy(out, r.data[:r.pos])
		return out
	}

	out := make([]byte, r.size)
	copy(out, r.data[r.pos:])
	copy(out[r.size-r.pos:], r.data[:r.pos])
	return out
}

func (r *RingBuffer) String() string {
	return string(r.Bytes())
}

func (r *RingBuffer) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pos = 0
	r.full = false
}
