package find

import snap "github.com/Schayan-nk/docfind-reports/internal/snapshot"

// freshState is the badge: live, stale (a copy, being refreshed) or offline
// (a copy, and the source cannot be reached).
func freshState(m snap.Meta) string {
	switch {
	case m.Unreachable:
		return "offline"
	case m.Stale:
		return "stale"
	default:
		return "live"
	}
}
