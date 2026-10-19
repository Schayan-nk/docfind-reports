package reports

import (
	"strings"
	"testing"
)

func TestDiscoverParamsFindsWhatTheBinderWillDemand(t *testing.T) {
	cases := []struct {
		why, sql string
		want     []string
	}{
		{"one", "WHERE a = @code", []string{"code"}},
		{"in query order, not alphabetical",
			"WHERE z = @zulu AND a = @alpha", []string{"zulu", "alpha"}},
		{"a repeat is one filter used twice",
			"WHERE a = @year OR b = @year", []string{"year"}},
		{"none", "SELECT 1", nil},

		// Everywhere an "@" is not a filter. Each of these would otherwise
		// appear as a filter box that does nothing.
		{"inside a string literal", "WHERE note = 'write to @sales'", nil},
		{"inside a line comment", "-- ask @sales first\nSELECT 1", nil},
		{"inside a block comment", "/* @sales */ SELECT 1", nil},
		{"inside a bracketed identifier", "SELECT [an @odd name] FROM t", nil},
		{"a server variable", "SELECT @@VERSION", nil},
		{"a server variable beside a real filter",
			"SELECT @@VERSION WHERE a = @code", []string{"code"}},
		{"a bare at-sign", "SELECT '@' + @code", []string{"code"}},
	}
	for _, c := range cases {
		got := DiscoverParams(c.sql)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s:\n  sql  %q\n  got  %v\n  want %v", c.why, c.sql, got, c.want)
		}
	}
}

// The @@ rule exists so this works. It is the first thing somebody types to
// check a connection, and before the rule it was read as a parameter named
// VERSION and refused as undeclared.
func TestServerVariableIsNotAFilter(t *testing.T) {
	for _, q := range []string{"SELECT @@VERSION", "SELECT @@SERVERNAME AS s"} {
		if names := DiscoverParams(q); len(names) != 0 {
			t.Errorf("%q offered filters %v", q, names)
		}
		if _, _, err := Bind(q, nil, nil); err != nil {
			t.Errorf("%q would not bind: %v", q, err)
		}
	}
}

// The type a placeholder gets by default comes from its own clause.
//
// Not cosmetic. Measured against the live database: a placeholder against LIKE
// defaulted to plain text binds "" rather than "%", and the query returned 3
// rows of 28 -- the three whose column is empty. A report that looks broken on
// its first run is a report nobody trusts afterwards.
func TestSuggestParamsReadsTheClause(t *testing.T) {
	cases := []struct{ why, sql, name, want string }{
		{"a LIKE clause means a fragment search, where empty is everything",
			"WHERE m LIKE @method", "method", string(KindLike)},
		{"NOT LIKE still ends in LIKE",
			"WHERE m NOT LIKE @method", "method", string(KindLike)},
		{"a newline between the operator and the placeholder",
			"WHERE m LIKE\n  @method", "method", string(KindLike)},
		{"REGEXP is the several-values kind",
			"WHERE m REGEXP @firms", "firms", string(KindAnyOf)},
		{"RLIKE is MySQL's other name for it",
			"WHERE m RLIKE @firms", "firms", string(KindAnyOf)},
		{"an IN group is a list",
			"WHERE code IN (@codes)", "codes", string(KindList)},
		{"an IN group with spaces inside the bracket",
			"WHERE code IN ( @codes )", "codes", string(KindList)},
		{"plain equality is a plain value",
			"WHERE code = @code", "code", string(KindText)},
		{"a BETWEEN bound is a plain value",
			"WHERE d BETWEEN @from AND @to", "from", string(KindText)},
		{"a bracket that is not an IN group",
			"WHERE (a = @x)", "x", string(KindText)},
	}
	for _, c := range cases {
		found := SuggestParams(c.sql)
		var got string
		for _, f := range found {
			if f.Name == c.name {
				got = f.Kind
			}
		}
		if got != c.want {
			t.Errorf("%s:\n  sql  %q\n  @%s got %q, want %q", c.why, c.sql, c.name, got, c.want)
		}
	}

	// And it agrees with DiscoverParams about what exists, because the page
	// uses one for the order and the other for the types.
	const sql = "WHERE a LIKE @one AND b IN (@two) AND c = @three"
	names := DiscoverParams(sql)
	found := SuggestParams(sql)
	if len(names) != len(found) {
		t.Fatalf("discovery found %v but suggestion found %d", names, len(found))
	}
	for i, n := range names {
		if found[i].Name != n {
			t.Errorf("order differs at %d: %q vs %q", i, n, found[i].Name)
		}
	}

	// Whatever it suggests has to be a type the builder accepts, or the page
	// would render a row whose dropdown has no matching option.
	for _, f := range SuggestParams("WHERE a LIKE @a AND b REGEXP @b AND c IN (@c) AND d = @d") {
		if _, ok := kindInfo(ParamKind(f.Kind)); !ok {
			t.Errorf("@%s was suggested the unknown type %q", f.Name, f.Kind)
		}
	}
}

func TestCleanParamsRefusesWhatCannotWork(t *testing.T) {
	cases := []struct {
		why    string
		driver Driver
		sql    string
		params []Param
		// want is a fragment of the message, so the test asserts the person
		// reading it is told which thing is wrong.
		want string
	}{
		{"a name the scanner could never match", DriverMySQL,
			"WHERE a = @x", []Param{{Name: "order-date", Kind: KindText}}, "order-date"},
		{"a Persian name, which cannot be a placeholder", DriverMySQL,
			"WHERE a = @x", []Param{{Name: "نام", Kind: KindText}}, "نام"},
		{"a name starting with a digit", DriverMySQL,
			"WHERE a = @x", []Param{{Name: "1st", Kind: KindText}}, "1st"},
		{"the same filter twice", DriverMySQL, "WHERE a = @x",
			[]Param{{Name: "x", Kind: KindText}, {Name: "x", Kind: KindLike}}, "declared twice"},
		{"a type that does not exist", DriverMySQL, "WHERE a = @x",
			[]Param{{Name: "x", Kind: ParamKind("slider")}}, "slider"},
		{"a dropdown with nothing in it", DriverMySQL, "WHERE a = @x",
			[]Param{{Name: "x", Kind: KindChoice}}, "at least one option"},
		{"a filter the query never uses is fine, but a query filter undeclared is not",
			DriverMySQL, "WHERE a = @x AND b = @y", []Param{{Name: "x", Kind: KindText}}, "@y"},

		// The engine rule. REGEXP is MySQL's; allowing this would save a report
		// that fails with a syntax error the first time anybody opens it.
		{"anyof against SQL Server", DriverSQLServer, "WHERE a REGEXP @x",
			[]Param{{Name: "x", Kind: KindAnyOf}}, "REGEXP"},
	}
	for _, c := range cases {
		_, err := CleanParams(c.driver, c.sql, c.params)
		if err == nil {
			t.Errorf("%s: accepted", c.why)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: the message does not mention %q:\n  %v", c.why, c.want, err)
		}
	}
}

func TestCleanParamsAcceptsAndNormalises(t *testing.T) {
	// anyof is allowed here because the driver is MySQL, which has REGEXP.
	in := []Param{
		{Name: "code", Kind: KindText, Default: "  1  "},
		{Name: "name", Kind: KindLike, Label: "  نام  "},
		{Name: "when", Kind: KindDate},
		{Name: "firms", Kind: KindAnyOf},
		{Name: "status", Kind: KindChoice, Options: []Option{
			{Value: " TO_DO "}, {Value: "", Label: ""}, {Value: "DONE", Label: "انجام شد"},
		}},
		// Everything a web form must not be able to set.
		{Name: "sneaky", Kind: KindText, Suggestable: true,
			Suggest: &SuggestSource{Table: "users", Column: "name"}},
	}
	const sql = "WHERE a = @code AND b LIKE @name AND c = @when AND d REGEXP @firms " +
		"AND e LIKE @status AND f = @sneaky"

	out, err := CleanParams(DriverMySQL, sql, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("got %d filters, want %d", len(out), len(in))
	}
	by := map[string]Param{}
	for _, p := range out {
		by[p.Name] = p
	}

	if by["code"].Default != "1" {
		t.Errorf("the default was not trimmed: %q", by["code"].Default)
	}
	if by["name"].Label != "نام" {
		t.Errorf("the label was not trimmed: %q", by["name"].Label)
	}
	if by["code"].Label != "code" {
		t.Errorf("a missing label should fall back to the name, got %q", by["code"].Label)
	}
	if by["when"].Calendar != "gregorian" {
		t.Errorf("a date filter should default to gregorian, got %q", by["when"].Calendar)
	}
	if by["code"].Calendar != "" {
		t.Errorf("a calendar on a non-date filter means nothing, got %q", by["code"].Calendar)
	}

	opts := by["status"].Options
	if len(opts) != 2 {
		t.Fatalf("got %d options, want 2 (the blank one dropped)", len(opts))
	}
	if opts[0].Value != "TO_DO" || opts[0].Label != "TO_DO" {
		t.Errorf("an option with no label should label itself: %+v", opts[0])
	}

	// The important one. Suggest names a table the server would then build a
	// statement around, so a value arriving from a browser in that field is an
	// identifier nobody validated.
	if by["sneaky"].Suggest != nil {
		t.Error("a suggest source survived from the request")
	}
	if by["sneaky"].Suggestable {
		t.Error("suggestable survived from the request, promising completions nothing provides")
	}
}

// A filter declared but never used in the query is allowed. It is dead, not
// dangerous, and refusing it would fight somebody mid-edit who has written the
// filter before the WHERE clause that uses it.
func TestCleanParamsAllowsAnUnusedFilter(t *testing.T) {
	out, err := CleanParams(DriverMySQL, "SELECT 1",
		[]Param{{Name: "spare", Kind: KindText}})
	if err != nil {
		t.Fatalf("an unused filter was refused: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d filters, want 1", len(out))
	}
}

// KindList against SQL Server has to survive the whole path, because it is the
// one kind that turns one placeholder into several -- and the ordinal rewrite
// has to number all of them.
func TestListFilterNumbersEveryPlaceholderOnSQLServer(t *testing.T) {
	params := []Param{{Name: "codes", Kind: KindList}}
	bound, args, err := BindFor(DriverSQLServer, "WHERE code IN (@codes)", params,
		map[string]string{"codes": "1,2,3"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bound, "(@p1,@p2,@p3)") {
		t.Errorf("the IN group was not numbered through: %q", bound)
	}
	if len(args) != 3 {
		t.Errorf("got %d arguments for three values: %v", len(args), args)
	}
	if strings.Contains(bound, "?") {
		t.Errorf("a question mark survived, which SQL Server will refuse: %q", bound)
	}
}
