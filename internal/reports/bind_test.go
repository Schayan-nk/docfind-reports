package reports

import (
	"strings"
	"testing"
)

func params(ps ...Param) []Param { return ps }

func TestBindReplacesPlaceholdersInOrder(t *testing.T) {
	sql := "SELECT * FROM t WHERE a = @one AND b = @two AND c = @one"
	got, args, err := Bind(sql, params(
		Param{Name: "one", Kind: KindText},
		Param{Name: "two", Kind: KindText},
	), map[string]string{"one": "1", "two": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "SELECT * FROM t WHERE a = ? AND b = ? AND c = ?"; got != want {
		t.Errorf("sql = %q, want %q", got, want)
	}
	// A placeholder used twice must bind twice, in order.
	if len(args) != 3 || args[0] != "1" || args[1] != "2" || args[2] != "1" {
		t.Errorf("args = %v, want [1 2 1]", args)
	}
}

// A filter left blank binds "%". Losing that turns every
// unfiltered report into an empty one.
func TestEmptyLikeBecomesWildcard(t *testing.T) {
	_, args, err := Bind("WHERE x LIKE @pref", params(Param{Name: "pref", Kind: KindLike}), map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 1 || args[0] != "%" {
		t.Errorf("args = %v, want [%%]", args)
	}
}

// A filter box is a "contains" box. Binding what was typed bare produces
// LIKE 'احمد', an exact match that finds nothing -- which is what made every
// filter on the dashboard return an empty table.
func TestLikeFilterMatchesFragments(t *testing.T) {
	for _, c := range []struct{ typed, want string }{
		{"احمد", "%احمد%"},
		{"Parsia", "%Parsia%"},
		{"  spaces  ", "%spaces%"},
		{"", "%"},
		// Someone who writes their own wildcard means it: leave it alone
		// rather than widening an anchored match into a contains.
		{"ORD-4%", "ORD-4%"},
		{"%exact%", "%exact%"},
		{"a_b", "a_b"},
	} {
		_, args, err := Bind("WHERE x LIKE @p", params(Param{Name: "p", Kind: KindLike}),
			map[string]string{"p": c.typed})
		if err != nil {
			t.Fatalf("Bind(%q): %v", c.typed, err)
		}
		if len(args) != 1 || args[0] != c.want {
			t.Errorf("typed %q -> bound %v, want [%s]", c.typed, args, c.want)
		}
	}
}

// A list cannot be one bind variable: binding "1,2" as a single value matches
// the literal string "1,2" and quietly returns nothing.
func TestListExpandsToPlaceholderGroup(t *testing.T) {
	got, args, err := Bind("WHERE ord_type in(@ptypeo)",
		params(Param{Name: "ptypeo", Kind: KindList}),
		map[string]string{"ptypeo": "1, 2,3"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "in(?,?,?)") {
		t.Errorf("sql = %q, want an expanded group", got)
	}
	if len(args) != 3 || args[0] != "1" || args[1] != "2" || args[2] != "3" {
		t.Errorf("args = %v, want [1 2 3]", args)
	}
}

// An empty list must not produce "IN ()", which will not parse.
func TestEmptyListIsNotASyntaxError(t *testing.T) {
	got, args, err := Bind("WHERE ord_type in(@ptypeo)",
		params(Param{Name: "ptypeo", Kind: KindList}),
		map[string]string{"ptypeo": "  "})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "in()") {
		t.Errorf("produced invalid SQL: %q", got)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want none", args)
	}
}

// The whole point of this package: a value can never become SQL. If a
// semicolon or a comment marker in a filter could reach the query text, this
// app would be an injection route into a production database.
func TestValuesNeverReachTheSQLText(t *testing.T) {
	hostile := "'; DROP TABLE application; --"
	got, args, err := Bind("WHERE name LIKE @p", params(Param{Name: "p", Kind: KindLike}),
		map[string]string{"p": hostile})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "DROP") || strings.Contains(got, ";") {
		t.Fatalf("a filter value reached the SQL text: %q", got)
	}
	// The value is wrapped for fragment matching, but it is still a bound
	// parameter and still carries the hostile text intact -- the point is
	// that it travels as data, not that it is sanitised. Rewriting the
	// content would be the wrong fix and would break real searches for
	// values containing quotes or hyphens.
	if len(args) != 1 {
		t.Fatalf("expected exactly one bound argument, got %v", args)
	}
	if bound, ok := args[0].(string); !ok || !strings.Contains(bound, hostile) {
		t.Errorf("the value should be bound intact, got %v", args)
	}
}

// An "@" inside a string literal or a comment is data, not a placeholder.
func TestAtSignInsideLiteralsAndCommentsIsLeftAlone(t *testing.T) {
	for _, sql := range []string{
		"SELECT 'user@example.com' AS a WHERE x = @p",
		"SELECT \"a@b\" WHERE x = @p",
		"SELECT `weird@column` WHERE x = @p",
		"-- a note about @nothing\nWHERE x = @p",
		"/* block @nothing */ WHERE x = @p",
		"SELECT 'it''s @escaped' WHERE x = @p",
	} {
		got, args, err := Bind(sql, params(Param{Name: "p", Kind: KindText}), map[string]string{"p": "v"})
		if err != nil {
			t.Fatalf("Bind(%q): %v", sql, err)
		}
		if len(args) != 1 {
			t.Errorf("Bind(%q) bound %d args, want 1 -- an @ in a literal was treated as a placeholder", sql, len(args))
		}
		if strings.Count(got, "?") != 1 {
			t.Errorf("Bind(%q) = %q, want exactly one placeholder", sql, got)
		}
	}
}

// A query naming a parameter the report does not declare is a bug in the
// report, and must fail loudly rather than binding something arbitrary.
func TestUndeclaredParameterIsRefused(t *testing.T) {
	_, _, err := Bind("WHERE x = @mystery", params(Param{Name: "known", Kind: KindText}), nil)
	if err == nil {
		t.Fatal("an undeclared parameter was accepted")
	}
	if !strings.Contains(err.Error(), "mystery") {
		t.Errorf("error should name the offending parameter, got: %v", err)
	}
}

func TestDefaultsApplyWhenValueMissing(t *testing.T) {
	_, args, err := Bind("WHERE a = @x", params(Param{Name: "x", Kind: KindText, Default: "fallback"}), map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 1 || args[0] != "fallback" {
		t.Errorf("args = %v, want [fallback]", args)
	}
}
