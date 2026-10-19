// Package snapshot keeps the last good answer to each question asked of a
// slow or unreliable source -- a reporting database -- and answers from it
// at once: stale-while-revalidate, with the copy on disk.
//
// A question answered less than Fresh ago is answered from the snapshot. An
// older one is still answered from the snapshot, immediately, and a refresh is
// started in the background, so the next person gets the new figures and
// nobody waits for them. Only a question never answered before waits for the
// source -- and not even that when the source is known to be down, when it
// fails at once. Every answer says where it came from and how old it is, so a
// page can say "cached, last refreshed 08:42" instead of passing old figures
// off as new.
//
// The snapshots are written to one file, so they survive a restart, and
// survive the reporting database and PostgreSQL both being down: nothing here
// needs either to answer.
package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrUnavailable: never answered, and the source is down.
var ErrUnavailable = errors.New("snapshot: no copy yet and the source is unavailable")

// Meta says where an answer came from.
type Meta struct {
	// Source is "live" (fetched for this request) or "snapshot".
	Source string    `json:"source"`
	At     time.Time `json:"at"`
	// Stale: older than Fresh; a refresh has been started, or could not be.
	Stale bool `json:"stale,omitempty"`
	// Unreachable: the source is down, so this copy is all there is.
	Unreachable bool `json:"unreachable,omitempty"`
}

type entry[T any] struct {
	V    T         `json:"v"`
	At   time.Time `json:"at"`
	used time.Time
}

// Store is the snapshots of one kind of answer.
type Store[T any] struct {
	// Fresh is how long an answer is served without refreshing it.
	Fresh time.Duration
	// Max is how many questions are kept; the least recently asked go first.
	Max int
	// Allow reports whether the source may be asked now -- the dependency's
	// circuit breaker. Nil means always.
	Allow func() bool
	// RefreshTimeout bounds a background refresh.
	RefreshTimeout time.Duration

	path       string
	now        func() time.Time
	mu         sync.Mutex
	entries    map[string]*entry[T]
	refreshing map[string]bool
	dirty      bool
}

// New returns a store kept in path ("" for memory only), loading what is
// there. An unreadable file is not an error: the snapshots are a cache.
func New[T any](path string, fresh time.Duration, max int) *Store[T] {
	s := &Store[T]{Fresh: fresh, Max: max, RefreshTimeout: 10 * time.Minute, path: path, now: time.Now,
		entries: map[string]*entry[T]{}, refreshing: map[string]bool{}}
	if path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			var saved map[string]*entry[T]
			if json.Unmarshal(raw, &saved) == nil {
				for k, e := range saved {
					if e != nil {
						e.used = e.At
						s.entries[k] = e
					}
				}
			}
		}
	}
	return s
}

// Get answers key: from the snapshot when there is one, from fetch when
// there is not. fetch gets its own context -- it may outlive the request that
// started it, as a background refresh.
func (s *Store[T]) Get(ctx context.Context, key string, fetch func(context.Context) (T, error)) (T, Meta, error) {
	now := s.now()
	allowed := s.Allow == nil || s.Allow()
	s.mu.Lock()
	e := s.entries[key]
	if e != nil {
		e.used = now
		v, at := e.V, e.At
		m := Meta{Source: "snapshot", At: at, Unreachable: !allowed}
		if now.Sub(at) >= s.Fresh {
			m.Stale = true
			if allowed && !s.refreshing[key] {
				s.refreshing[key] = true
				go s.refresh(key, fetch)
			}
		}
		s.mu.Unlock()
		return v, m, nil
	}
	s.mu.Unlock()
	var zero T
	if !allowed {
		return zero, Meta{Unreachable: true}, ErrUnavailable
	}
	v, err := fetch(ctx)
	if err != nil {
		return zero, Meta{}, err
	}
	s.put(key, v, s.now())
	return v, Meta{Source: "live", At: s.now()}, nil
}

func (s *Store[T]) refresh(key string, fetch func(context.Context) (T, error)) {
	ctx, cancel := context.WithTimeout(context.Background(), s.RefreshTimeout)
	defer cancel()
	v, err := fetch(ctx)
	s.mu.Lock()
	delete(s.refreshing, key)
	s.mu.Unlock()
	if err == nil {
		s.put(key, v, s.now())
	}
}

func (s *Store[T]) put(key string, v T, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = &entry[T]{V: v, At: at, used: at}
	s.dirty = true
	for s.Max > 0 && len(s.entries) > s.Max {
		var oldest string
		var when time.Time
		for k, e := range s.entries {
			if oldest == "" || e.used.Before(when) {
				oldest, when = k, e.used
			}
		}
		delete(s.entries, oldest)
	}
}

// Len is how many questions have a snapshot.
func (s *Store[T]) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// Flush writes the snapshots to disk if they changed: a temporary file
// renamed over the old one, so a crash mid-write leaves the previous copy.
func (s *Store[T]) Flush() error {
	s.mu.Lock()
	if s.path == "" || !s.dirty {
		s.mu.Unlock()
		return nil
	}
	raw, err := json.Marshal(s.entries)
	s.dirty = false
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// KeepFlushed writes the snapshots every interval until ctx ends, and once
// more then.
func (s *Store[T]) KeepFlushed(ctx context.Context, every time.Duration) {
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = s.Flush()
				return
			case <-t.C:
				_ = s.Flush()
			}
		}
	}()
}
