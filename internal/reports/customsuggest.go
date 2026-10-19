package reports

import (
	"github.com/Schayan-nk/docfind-reports/internal/obs"

	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Completions for an authored report's filters.
//
// The shipped reports declare "-- suggest: TABLE.COLUMN" 27 times, and the
// reason is in suggest.go: an expert or customer filter cannot be a dropdown
// because there are hundreds of them and the list grows, but a free-text box
// makes somebody guess whether a colleague is filed as "محمدرضا" or
// "محمد رضا" -- and a near miss returns an empty report with nothing on
// screen explaining why. An authored report has exactly the same problem.
//
// Why this is safe to offer, having been refused before. CleanParams strips
// Param.Suggest, and that is still right: it arrives from a browser as a table
// and column name, identifiers cannot be bound as parameters, and an
// identifier nobody validated is concatenated straight into a statement. What
// makes the authored version different is not trust but two facts:
//
//   - safeIdentifier proves the names before they are used. The same regexp
//     the shipped sources are held to: letters, digits and underscore, so no
//     quote, space, semicolon or comment marker can reach the statement.
//   - The person asking is an administrator who already holds arbitrary SELECT
//     on this connection. Refusing them a DISTINCT over a column they can
//     name protects nothing -- they can type the query themselves.
//
// The statement below therefore carries no user input at all, only two proven
// identifiers, which is why there is nothing in it to bind.

// suggestCap is how many distinct values are read. The shipped path uses the
// same number, for the same reason: a few hundred short strings is one round
// trip, and a column with more values than this is not one a person picks from.
const suggestCap = 2000

// customSuggestKey identifies one cached column read.
//
// The source id is part of the key, unlike the shipped cache which is keyed on
// table.column alone. It has to be: an administrator may add two connections
// to two different databases that both have a CUSTOMERS table, and a cache
// that could not tell them apart would serve one report the other's values --
// quietly, and only to whichever asked second.
type customSuggestKey struct {
	source, table, column string
	person                bool
}

var (
	customSuggestMu    sync.Mutex
	customSuggestCache = map[customSuggestKey][]string{}
	customSuggestAt    = map[customSuggestKey]time.Time{}
)

// quoteIdentifier wraps a proven identifier for one engine.
//
// Two engines, two conventions, and the shipped path only ever needed MySQL's.
// safeIdentifier has already refused anything containing a backtick or a
// bracket, so neither form can be escaped out of.
func quoteIdentifier(driver Driver, name string) string {
	if driver == DriverSQLServer {
		return "[" + name + "]"
	}
	return "`" + name + "`"
}

// SuggestValues returns completions for one authored filter.
//
// Reads the whole distinct column once and filters in memory, rather than a
// LIKE query per keystroke -- a burst of typing becomes one database round
// trip instead of one per character.
func (s *SourceStore) SuggestValues(ctx context.Context, sourceID, table, column string,
	person bool, typed string) ([]string, error) {
	defer obs.Track(ctx, obs.DB)()

	table = strings.TrimSpace(table)
	column = strings.TrimSpace(column)
	if !safeIdentifier.MatchString(table) || !safeIdentifier.MatchString(column) {
		return nil, fmt.Errorf("%q is not a usable name for suggestions", table+"."+column)
	}

	key := customSuggestKey{source: sourceID, table: table, column: column, person: person}
	values, ok := cachedCustomSuggestions(key)
	if !ok {
		db, driver, err := s.pool(sourceID)
		if err != nil {
			return nil, err
		}
		qt, qc := quoteIdentifier(driver, table), quoteIdentifier(driver, column)
		// Both engines accept this shape. TOP/LIMIT differ, so the cap is
		// applied while reading instead -- one fewer thing to get wrong per
		// driver, and the read stops at the same place either way.
		query := fmt.Sprintf("SELECT DISTINCT %s FROM %s WHERE %s IS NOT NULL AND %s <> ''",
			qc, qt, qc, qc)

		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		rows, done, err := queryReadOnly(c, db, driver, query)
		if err != nil {
			return nil, describeQueryError(err, driver, c)
		}
		defer done()

		for rows.Next() && len(values) < suggestCap {
			var v any
			if err := rows.Scan(&v); err != nil {
				return nil, fmt.Errorf("reading suggestions: %w", err)
			}
			if str := strings.TrimSpace(asString(v)); str != "" {
				values = append(values, str)
			}
		}
		if err := rows.Err(); err != nil {
			return nil, describeQueryError(err, driver, c)
		}
		if person {
			// The same folding the shipped suggestion lists do, so a column of
			// names offers one entry per colleague rather than one per job
			// title they have held.
			values = foldPeople(values)
		}
		storeCustomSuggestions(key, values)
	}

	q := strings.TrimSpace(typed)
	if q == "" {
		if len(values) > maxSuggestions {
			return values[:maxSuggestions], nil
		}
		return values, nil
	}

	// Values that START with what was typed first, then values merely
	// containing it -- somebody typing a surname wants the names beginning
	// with it before the ones mentioning it.
	var starts, contains []string
	lower := strings.ToLower(q)
	for _, v := range values {
		lv := strings.ToLower(v)
		switch {
		case strings.HasPrefix(lv, lower):
			starts = append(starts, v)
		case strings.Contains(lv, lower):
			contains = append(contains, v)
		}
	}
	out := append(starts, contains...)
	if len(out) > maxSuggestions {
		out = out[:maxSuggestions]
	}
	return out, nil
}

func cachedCustomSuggestions(key customSuggestKey) ([]string, bool) {
	customSuggestMu.Lock()
	defer customSuggestMu.Unlock()
	at, ok := customSuggestAt[key]
	if !ok || time.Since(at) > suggestCacheTTL {
		return nil, false
	}
	return customSuggestCache[key], true
}

func storeCustomSuggestions(key customSuggestKey, values []string) {
	customSuggestMu.Lock()
	defer customSuggestMu.Unlock()
	customSuggestCache[key] = values
	customSuggestAt[key] = time.Now()
}
