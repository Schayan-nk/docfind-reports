package snapshot

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newStore(t *testing.T, c *clock, path string) *Store[int] {
	t.Helper()
	s := New[int](path, time.Minute, 3)
	s.now = c.now
	return s
}

// The first answer waits for the source; later ones are the snapshot; an old
// snapshot is still answered at once while a refresh runs behind it.
func TestServeAtOnceThenRefreshBehind(t *testing.T) {
	c := &clock{t: time.Date(2026, 9, 23, 8, 42, 0, 0, time.UTC)}
	s := newStore(t, c, "")
	var calls atomic.Int32
	fetch := func(context.Context) (int, error) { return int(calls.Add(1)), nil }
	ctx := context.Background()

	v, m, err := s.Get(ctx, "orders", fetch)
	if err != nil || v != 1 || m.Source != "live" {
		t.Fatalf("first: %d %+v %v", v, m, err)
	}
	c.t = c.t.Add(30 * time.Second)
	if v, m, _ := s.Get(ctx, "orders", fetch); v != 1 || m.Source != "snapshot" || m.Stale || calls.Load() != 1 {
		t.Errorf("fresh: %d %+v, %d calls", v, m, calls.Load())
	}
	c.t = c.t.Add(2 * time.Minute)
	v, m, _ = s.Get(ctx, "orders", fetch)
	if v != 1 || !m.Stale || !m.At.Equal(time.Date(2026, 9, 23, 8, 42, 0, 0, time.UTC)) {
		t.Errorf("stale answer: %d %+v", v, m)
	}
	waitFor(t, func() bool { return calls.Load() == 2 })
	waitFor(t, func() bool { v, _, _ := s.Get(ctx, "orders", fetch); return v == 2 })
}

// While the source is down: a snapshot is served and marked unreachable, no
// refresh is attempted, and a question never answered fails at once.
func TestTheSourceIsDown(t *testing.T) {
	c := &clock{t: time.Now()}
	s := newStore(t, c, "")
	up := true
	s.Allow = func() bool { return up }
	calls := 0
	fetch := func(context.Context) (int, error) { calls++; return 7, nil }
	ctx := context.Background()
	s.Get(ctx, "a", fetch)
	up = false
	c.t = c.t.Add(time.Hour)
	if v, m, err := s.Get(ctx, "a", fetch); err != nil || v != 7 || !m.Unreachable || !m.Stale {
		t.Errorf("down, with a copy: %d %+v %v", v, m, err)
	}
	if _, _, err := s.Get(ctx, "b", fetch); !errors.Is(err, ErrUnavailable) {
		t.Errorf("down, no copy: %v", err)
	}
	if calls != 1 {
		t.Errorf("the source was asked %d times while down", calls)
	}
}

// A failed first fetch is an error, and stores nothing.
func TestAFailedFetchStoresNothing(t *testing.T) {
	s := newStore(t, &clock{t: time.Now()}, "")
	boom := errors.New("pm: timeout")
	if _, _, err := s.Get(context.Background(), "x", func(context.Context) (int, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	if s.Len() != 0 {
		t.Error("a failure was stored")
	}
}

// The snapshots survive a restart, and the least recently asked go first.
func TestSnapshotsSurviveARestartAndEvictTheOldest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snap", "reports.json")
	c := &clock{t: time.Now()}
	s := newStore(t, c, path)
	ctx := context.Background()
	for i, k := range []string{"a", "b", "c", "d"} {
		c.t = c.t.Add(time.Second)
		s.Get(ctx, k, func(context.Context) (int, error) { return i, nil })
	}
	if s.Len() != 3 {
		t.Fatalf("kept %d, want 3", s.Len())
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	again := newStore(t, c, path)
	again.Allow = func() bool { return false } // nothing can be fetched
	if v, m, err := again.Get(ctx, "d", nil); err != nil || v != 3 || m.Source != "snapshot" {
		t.Errorf("after a restart: %d %+v %v", v, m, err)
	}
	if _, _, err := again.Get(ctx, "a", nil); !errors.Is(err, ErrUnavailable) {
		t.Errorf("the oldest was not evicted: %v", err)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 200 {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out")
}
