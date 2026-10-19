package reports

import "testing"

// One person written several ways, the way a system that stores the job title
// with the name writes them, and what each must reduce to. Every name here is
// invented.
func TestNormalisePersonFoldsTitleVariants(t *testing.T) {
	cases := []struct{ in, want string }{
		// Three spellings of one person, two differing only by a doubled space.
		{"زهرا مرادی کارشناس فروش 1-2 و سرپرست خرید داخلی ", "زهرا مرادی"},
		{"زهرا مرادی  سرپرست تیم فروش 3 ", "زهرا مرادی"},
		{"زهرا مرادی سرپرست تیم فروش 3", "زهرا مرادی"},
		// Two spellings, different teams.
		{"آرش رحیمی  کارشناس فروش 2-1 ", "آرش رحیمی"},
		{"آرش رحیمی کارشناس فروش 1-3", "آرش رحیمی"},
		// Every word that starts a title.
		{"تینا امینی مسئول دفتر واحد 101", "تینا امینی"},
		{"نادر کریمی مشاور", "نادر کریمی"},
		{"سارا کریمی مدیر فروش", "سارا کریمی"},
		{"مریم صادقی رئیس واحد", "مریم صادقی"},
		{"علی رضایی معاون مالی", "علی رضایی"},
		{"کیوان نوری مهندس فروش", "کیوان نوری"},
		// No title at all.
		{"تینا امینی", "تینا امینی"},
		{"  نادر   کریمی  ", "نادر کریمی"},
		// The zero-width non-joiner Persian keyboards insert inconsistently.
		{"سارا‌کریمی کارشناس فروش", "سارا کریمی"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalisePerson(c.in); got != c.want {
			t.Errorf("NormalisePerson(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
	}
}

// Two different people must never merge, however similar their names.
func TestNormalisePersonKeepsDifferentPeopleApart(t *testing.T) {
	distinct := []string{
		"آرش رحیمی کارشناس فروش 2-1",
		"آرش رحیم‌زاده کارشناس فروش 1-9",
		"نادر کریمی کارشناس فروش 2-17",
		"سارا کریمی کارشناس فروش 1-8",
		"زهرا مرادی کارشناس فروش 1-3",
		"زهرا مرادیان کارشناس فروش 1-3",
	}
	seen := map[string]string{}
	for _, s := range distinct {
		n := NormalisePerson(s)
		if prev, clash := seen[n]; clash {
			t.Errorf("%q and %q both normalised to %q", prev, s, n)
		}
		seen[n] = s
	}
}

// A field holding only a title must not collapse every such row into one
// nameless person -- that would silently merge unrelated records.
func TestNormalisePersonKeepsTitleOnlyValues(t *testing.T) {
	for _, in := range []string{"کارشناس فروش 2-1", "سرپرست تیم فروش 4"} {
		if got := NormalisePerson(in); got == "" {
			t.Errorf("NormalisePerson(%q) returned empty; unrelated rows would merge", in)
		}
	}
}

func contains(hay, needle string) bool {
	return len(needle) > 0 && len(hay) >= len(needle) && indexOf(hay, needle) >= 0
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
