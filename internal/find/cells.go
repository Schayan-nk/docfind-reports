package find

import (
	"fmt"
	"strconv"
	"strings"
)

// A result cell as text, and an amount grouped in thousands, for the totals
// under a report's table.

func cellString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return strings.TrimSpace(string(x))
	case string:
		return strings.TrimSpace(x)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// groupRials writes n the way the report does: thousands with commas.
func groupRials(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
