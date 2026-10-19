package find

import (
	"strings"
)

// styleBlocks returns the contents of each <style> element in a page.
func styleBlocks(page string) []string {
	var out []string
	rest := page
	for {
		i := strings.Index(rest, "<style")
		if i < 0 {
			return out
		}
		rest = rest[i:]
		open := strings.Index(rest, ">")
		if open < 0 {
			return out
		}
		end := strings.Index(rest, "</style>")
		if end < 0 {
			return out
		}
		out = append(out, rest[open+1:end])
		rest = rest[end+len("</style>"):]
	}
}
