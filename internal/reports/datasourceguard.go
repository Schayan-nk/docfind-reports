package reports

import (
	"context"
	"sync/atomic"
)

// Where a builder connection may point.
//
// A connection string is something an administrator typed, and connecting to
// it is this server opening a socket on their say-so -- to the LAN, which is
// the point, but also possibly to this machine's own services or a metadata
// address. The policy lives with the caller (internal/find, which knows the
// allow-list); this is only the hook, checked before a connection is TESTED
// and before a saved one's pool is first OPENED, so a connection saved before
// the rule existed is held to it as well.

// Guard vets a connection string before anything connects to it. A non-nil
// error is shown to the administrator as it is, so it is written for them.
type Guard func(ctx context.Context, driver Driver, dsn string) error

// SetGuard installs the check. Nil removes it.
func (s *SourceStore) SetGuard(g Guard) {
	if g == nil {
		s.guard.Store(nil)
		return
	}
	s.guard.Store(&g)
}

func (s *SourceStore) checkGuard(ctx context.Context, driver Driver, dsn string) error {
	if g := s.guard.Load(); g != nil {
		return (*g)(ctx, driver, dsn)
	}
	return nil
}

// guardSlot is the SourceStore field holding the hook; atomic because the
// hook is installed once at start-up and read on every pool open.
type guardSlot = atomic.Pointer[Guard]
