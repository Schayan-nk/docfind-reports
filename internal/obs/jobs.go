package obs

import (
	"sort"
	"sync"
	"time"
)

// Background work, measured the same way requests are.
//
// docfind does most of its work when nobody is asking: the archive rescan every
// thirty seconds, the customer sync, the Persian-name drafting, the internet
// and WhatsApp probes, the mail classification, the notification sender and
// its watchers. When a page is slow the first question is "what else was the
// server doing?", and before this there was no answer.
//
// Each job records its last run, how long it took, how many items it handled,
// its last error, and how many runs and failures since start. A job that has
// stopped running shows its last run getting older -- which is the one failure
// a job's own log line can never report.

// JobStats is one job's figures.
type JobStats struct {
	Name       string    `json:"name"`
	Runs       int64     `json:"runs"`
	Failures   int64     `json:"failures"`
	LastStart  time.Time `json:"last_start,omitzero"`
	LastEnd    time.Time `json:"last_end,omitzero"`
	LastMs     float64   `json:"last_ms"`
	MaxMs      float64   `json:"max_ms"`
	LastItems  int       `json:"last_items"`
	LastError  string    `json:"last_error,omitempty"`
	LastOK     time.Time `json:"last_ok,omitzero"`
	Running    bool      `json:"running"`
	QueueDepth int       `json:"queue_depth,omitempty"`
}

// Jobs is the registry. The zero value is ready; use the package default.
type Jobs struct {
	mu   sync.Mutex
	jobs map[string]*JobStats
}

var defaultJobs = &Jobs{}

// DefaultJobs is the process's registry.
func DefaultJobs() *Jobs { return defaultJobs }

// Snapshot returns every job, by name.
func (j *Jobs) Snapshot() []JobStats {
	j.mu.Lock()
	out := make([]JobStats, 0, len(j.jobs))
	for _, s := range j.jobs {
		out = append(out, *s)
	}
	j.mu.Unlock()
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}
