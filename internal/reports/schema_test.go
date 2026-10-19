package reports

import (
	"strings"
)

func fmtRow(r []any) string {
	var b strings.Builder
	for _, v := range r {
		b.WriteString(asString(v))
		b.WriteByte('|')
	}
	return b.String()
}
