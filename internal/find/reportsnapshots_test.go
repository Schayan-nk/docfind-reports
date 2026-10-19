package find

import (
	"testing"
	"time"

	snap "github.com/Schayan-nk/docfind-reports/internal/snapshot"
)

// The same question asked with its filters in any order is one snapshot;
// a different page or filter value is another.
func TestSnapshotKeys(t *testing.T) {
	a := snapKey("orders", map[string]string{"from": "1403/01/01", "customer": "پارس"}, 1, 100)
	b := snapKey("orders", map[string]string{"customer": "پارس", "from": "1403/01/01"}, 1, 100)
	if a != b {
		t.Errorf("filter order changed the key:\n%s\n%s", a, b)
	}
	for _, other := range []string{
		snapKey("orders", map[string]string{"from": "1403/01/01", "customer": "پارس"}, 2, 100),
		snapKey("orders", map[string]string{"from": "1403/01/02", "customer": "پارس"}, 1, 100),
		snapKey("guarantees", map[string]string{"from": "1403/01/01", "customer": "پارس"}, 1, 100),
	} {
		if other == a {
			t.Errorf("two different questions share a key: %s", a)
		}
	}
}

// What the page reads to decide whether to say "cached, last refreshed".
func TestDataSourceCarriesAgeAndReachability(t *testing.T) {
	at := time.Date(2026, 9, 23, 8, 42, 0, 0, time.Local)
	ds := dataSource(snap.Meta{Source: "snapshot", At: at, Stale: true, Unreachable: true})
	if ds["source"] != "snapshot" || ds["at"] != at || ds["stale"] != true || ds["unreachable"] != true {
		t.Errorf("data_source = %v", ds)
	}
}
