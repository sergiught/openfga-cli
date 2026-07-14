// Package apilog captures OpenFGA HTTP traffic for the playground's API Logs
// view. It is transport-level and independent of the TUI.
package apilog

import (
	"net/http"
	"sync"
	"time"
)

// Entry is one captured HTTP attempt. Bodies are stored capped (see Transport);
// Err is set only on a transport-level failure, not on an HTTP error status.
type Entry struct {
	Time                time.Time
	Method              string
	URL                 string
	ReqHeaders          http.Header
	ReqBody             []byte
	Status              int
	StatusText          string
	RespHeaders         http.Header
	RespBody            []byte
	Elapsed             time.Duration
	RequestID           string
	ServerQueryDuration string
	Err                 string
	Attempt             int
}

// Recorder is a concurrency-safe ring buffer of the most recent entries. Add is
// called from HTTP goroutines; Snapshot is read from the UI goroutine.
type Recorder struct {
	mu      sync.Mutex
	entries []Entry
	cap     int
	index   int
	full    bool
	notify  func()
}

// NewRecorder returns a Recorder holding at most capacity entries.
func NewRecorder(capacity int) *Recorder {
	return &Recorder{
		entries: make([]Entry, capacity),
		cap:     capacity,
	}
}

// Add appends e, evicting the oldest entry past capacity, then fires notify.
func (r *Recorder) Add(e Entry) {
	r.mu.Lock()
	r.entries[r.index] = e
	r.index++
	if r.index >= r.cap {
		r.index = 0
		r.full = true
	}
	n := r.notify
	r.mu.Unlock()

	if n != nil {
		n()
	}
}

// Snapshot returns an independent copy of the buffer, oldest first.
func (r *Recorder) Snapshot() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()

	var result []Entry
	if !r.full {
		result = make([]Entry, r.index)
		copy(result, r.entries[:r.index])
		return result
	}

	result = make([]Entry, r.cap)
	copy(result, r.entries[r.index:])
	copy(result[r.cap-r.index:], r.entries[:r.index])
	return result
}

// Len returns how many entries the buffer currently holds (0..capacity),
// without copying them — cheap enough to call on every render.
func (r *Recorder) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.full {
		return r.cap
	}
	return r.index
}

// Clear empties the buffer.
func (r *Recorder) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries = make([]Entry, r.cap)
	r.index = 0
	r.full = false
}

// SetNotify registers a callback fired after each Add (e.g. program.Send).
func (r *Recorder) SetNotify(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.notify = fn
}
