package reports

import (
	"github.com/Schayan-nk/docfind-reports/internal/obs"

	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Completions for the open-ended name filters.
//
// The expert and customer filters cannot be dropdowns -- there are dozens of
// experts and hundreds of customers, and the list grows -- but a free-text box
// makes someone guess whether a colleague is filed as "محمدرضا" or
// "محمد رضا", and a near miss returns an empty report with nothing on screen
// explaining why. Suggesting real values as they type removes the guess
// without constraining what they may search for.

// safeIdentifier is what may appear as a table or column name.
//
// Identifiers cannot be bound as parameters -- only values can -- so these are
// concatenated into the statement and therefore have to be proven safe rather
// than trusted. The pattern admits exactly what MySQL identifiers in this
// schema use and nothing else: no quotes, no spaces, no semicolons, no
// comment markers. The values people type are still bound normally.
var safeIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// Valid reports whether a source names something this package will query.
func (s SuggestSource) Valid() bool {
	return safeIdentifier.MatchString(s.Table) && safeIdentifier.MatchString(s.Column)
}

func (s SuggestSource) String() string { return s.Table + "." + s.Column }

const (
	// maxSuggestions is how many completions come back. Long enough to be
	// useful, short enough that the list stays scannable.
	maxSuggestions = 15
	// suggestCacheTTL is how long a column's values are reused. These are
	// staff and customer names: they change over weeks, not seconds, and
	// re-querying on every keystroke would put a DISTINCT scan on the
	// production database per typed character.
	suggestCacheTTL = 10 * time.Minute
)

// suggestEntry is one column's cached values, in both the forms a source can
// ask for.
//
// Holding both is what keeps the fold and the cache from interfering. The
// entry is keyed by table.column -- the identity of the database read, which
// is the expensive part and is the same either way -- and a source's Person
// flag then selects a *field* rather than taking part in the key. Two reports
// reading the same column, one folding and one not, therefore share the one
// DISTINCT query and still cannot be served each other's list. Keying the
// cache by table.column alone while storing only one shape would have done
// exactly that, silently, to whichever report asked second.
//
// Entries are immutable once stored, so there is no half-populated state to
// reason about: both fields are computed before the entry goes in the map.
type suggestEntry struct {
	raw    []string
	folded []string
	loaded time.Time
}

// newSuggestEntry folds once, at load, rather than on every keystroke.
//
// The unfolded shape is a few hundred short strings and the fold is one pass
// plus a sort, so computing it even for the columns that never ask costs
// microseconds once per TTL -- cheaper than the branch needed to make it lazy,
// and it keeps the entry immutable.
func newSuggestEntry(raw []string) suggestEntry {
	return suggestEntry{raw: raw, folded: foldPeople(raw), loaded: time.Now()}
}

// view returns the shape this source completes against.
func (e suggestEntry) view(person bool) []string {
	if person {
		return e.folded
	}
	return e.raw
}

var (
	suggestMu    sync.Mutex
	suggestCache = map[string]suggestEntry{}
)

// SuggestWhere is Suggest keeping only the values keep admits: a person
// filter's names narrowed to the reader's team (teamscope.go). Narrowed
// before the list is cut to length, so a narrowed list is not a short one.
func (d *DB) SuggestWhere(ctx context.Context, src SuggestSource, typed string, keep func(string) bool) ([]string, error) {
	defer obs.Track(ctx, obs.DB)()
	if !src.Valid() {
		return nil, fmt.Errorf("reports: %q is not a usable suggestion source", src)
	}

	values, err := d.distinctValues(ctx, src)
	if err != nil {
		return nil, err
	}
	if keep != nil {
		kept := make([]string, 0, len(values))
		for _, v := range values {
			if keep(v) {
				kept = append(kept, v)
			}
		}
		values = kept
	}

	q := strings.TrimSpace(typed)
	if q == "" {
		if len(values) > maxSuggestions {
			return values[:maxSuggestions], nil
		}
		return values, nil
	}

	// Prefix matches first, then anything containing the text. Someone typing
	// "محمد" wants the people whose names start that way at the top, not
	// buried among everyone with it in the middle.
	var prefix, contains []string
	lower := strings.ToLower(q)
	for _, v := range values {
		lv := strings.ToLower(v)
		switch {
		case strings.HasPrefix(lv, lower):
			prefix = append(prefix, v)
		case strings.Contains(lv, lower):
			contains = append(contains, v)
		}
		if len(prefix) >= maxSuggestions {
			break
		}
	}
	out := append(prefix, contains...)
	if len(out) > maxSuggestions {
		out = out[:maxSuggestions]
	}
	return out, nil
}

// distinctValues reads and caches one column's distinct values, in whichever
// shape the source asked for.
func (d *DB) distinctValues(ctx context.Context, src SuggestSource) ([]string, error) {
	if values, ok := cachedValues(src); ok {
		return values, nil
	}

	// Identifiers are validated above; the statement carries no user input at
	// all, which is why there is nothing here to bind.
	query := fmt.Sprintf(
		"SELECT DISTINCT `%s` FROM `%s` WHERE `%s` IS NOT NULL AND `%s` <> '' LIMIT 2000",
		src.Column, src.Table, src.Column, src.Column)

	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	rows, err := d.db.QueryContext(c, query)
	if err != nil {
		return nil, fmt.Errorf("reports: suggestions for %s: %w", src, err)
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		s := strings.TrimSpace(asString(v))
		if s != "" {
			values = append(values, s)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(values)

	entry := newSuggestEntry(values)
	suggestMu.Lock()
	suggestCache[src.String()] = entry
	suggestMu.Unlock()
	return entry.view(src.Person), nil
}

// cachedValues returns a source's completions if the column is cached and
// still fresh.
func cachedValues(src SuggestSource) ([]string, bool) {
	suggestMu.Lock()
	entry, ok := suggestCache[src.String()]
	suggestMu.Unlock()
	if !ok || time.Since(entry.loaded) >= suggestCacheTTL {
		return nil, false
	}
	return entry.view(src.Person), true
}

// foldPeople collapses the spellings of one person into a single suggestion.
//
// The same person appears once per job title they have held, so a person
// filter lists more values than there are people and asks the reader to guess
// which "زهرا مرادی" is the one they meant -- when the answer is that both are, and
// picking either hid half of that person's work.
//
// What makes this safe is the substring test below, not the normalisation.
// These filters bind through likePattern, which wraps the offered value V as
// %V%, so if V occurs inside every raw spelling it replaced then filtering by V
// matches every row any of those spellings would have matched. Folding can then
// only ever *widen* a result, never drop a row -- which is the whole point,
// since picking زهرا مرادی should return all of their records rather than one
// team's. Where that does not hold the group is left exactly as the database
// spelled it: a visible duplicate is a nuisance, but a filter that quietly
// returns fewer rows than it should is a wrong answer nobody can see.
func foldPeople(values []string) []string {
	groups := map[string][]string{}
	var canonical []string
	for _, v := range values {
		name := NormalisePerson(v)
		if _, seen := groups[name]; !seen {
			canonical = append(canonical, name)
		}
		groups[name] = append(groups[name], v)
	}

	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	add := func(v string) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	for _, name := range canonical {
		spellings := groups[name]
		if foldsWithoutLosingRows(name, spellings) {
			add(name)
			continue
		}
		for _, v := range spellings {
			add(v)
		}
	}
	// Re-sorted because a canonical name sorts where the raw spellings did not,
	// and the client shows the list in the order it arrives.
	sort.Strings(out)
	return out
}

// foldsWithoutLosingRows reports whether offering one value in place of several
// can cost a row.
//
// Two conditions, both necessary:
//
//   - The offer occurs inside every spelling it replaces. Then %offer% matches
//     at least the rows all of those spellings matched.
//   - The offer carries no LIKE wildcard. likePattern deliberately leaves a
//     value containing % or _ exactly as typed, so that someone who knows LIKE
//     can anchor a match -- which means such an offer would *not* be widened to
//     %offer% and the substring argument above would not apply to it.
func foldsWithoutLosingRows(offer string, spellings []string) bool {
	if strings.TrimSpace(offer) == "" || strings.ContainsAny(offer, "%_") {
		return false
	}
	for _, v := range spellings {
		if !strings.Contains(v, offer) {
			return false
		}
	}
	return true
}

// asString renders a scanned value as text; this driver hands back []byte.
func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case string:
		return t
	}
	return fmt.Sprint(v)
}

// ResetSuggestCache drops every cached column, so a reload picks up staff
// added since the server started.
func ResetSuggestCache() {
	suggestMu.Lock()
	suggestCache = map[string]suggestEntry{}
	suggestMu.Unlock()
}
