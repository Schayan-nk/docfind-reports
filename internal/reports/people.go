package reports

import "strings"

// Person names that carry a job title.
//
// Some systems store a person as "<name> <job title> <unit number>", so one
// person appears once per role they have held, and every count keyed on the
// raw field splits them: a ranked list moves people up and down and hides real
// workload. A report marks such columns ("-- person:", and the "person" flag
// on a builder column), and their values are folded to the name.

// personTitles are the words that begin a job title. The name is everything
// before the first of them.
var personTitles = []string{
	"کارشناس",
	"سرپرست",
	"مسئول",
	"مشاور",
	"مدیر",
	"رئیس",
	"معاون",
	"مهندس",
}

// NormalisePerson reduces a name-plus-title to just the name.
//
// Whitespace is collapsed as well as trimmed: a name that differs only in
// spacing is the same person by any reading.
func NormalisePerson(s string) string {
	name := strings.TrimSpace(s)
	if name == "" {
		return ""
	}

	// Cut at whichever title appears earliest.
	cut := -1
	for _, t := range personTitles {
		if i := strings.Index(name, t); i > 0 && (cut < 0 || i < cut) {
			cut = i
		}
	}
	if cut > 0 {
		name = name[:cut]
	}

	// Collapse runs of whitespace, and drop the zero-width joiner that
	// Persian keyboards insert inconsistently.
	name = strings.ReplaceAll(name, "\u200c", " ")
	name = strings.Join(strings.Fields(name), " ")

	// A name reduced to nothing means the field held only a title; keep the
	// original rather than merging every such row into one blank person.
	if name == "" {
		return strings.Join(strings.Fields(s), " ")
	}
	return name
}
