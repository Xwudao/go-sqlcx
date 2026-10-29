package sqlcx

import (
	"testing"

	"github.com/Xwudao/go-sqlcx/internal/ast"
)

func TestRawPlaceholders(t *testing.T) {
	cases := []struct {
		sql  string
		args []any
		want string
		bad  bool
	}{
		{sql: "a = ? AND b = ?", args: []any{1, 2}, want: "a = $1 AND b = $2"},
		{sql: "x ?? 'k' AND y = ?", args: []any{1}, want: "x ? 'k' AND y = $1"},
		{sql: "'it''s ?' || ? -- ? comment\n", args: []any{1}, want: "'it''s ?' || $1 -- ? comment\n"},
		{sql: `"col?" = ? /* ? */`, args: []any{1}, want: `"col?" = $1 /* ? */`},
		{sql: "$tag$ ? $tag$ || ?", args: []any{1}, want: "$tag$ ? $tag$ || $1"},
		{sql: "a = $1", args: []any{1}, bad: true},
		{sql: "a = ?", bad: true},
		{sql: "'open", bad: true},
	}
	for _, c := range cases {
		n, err := parseRaw(c.sql, c.args)
		if c.bad {
			if err == nil {
				t.Errorf("%q: expected error", c.sql)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", c.sql, err)
		}
		got, _, err := ast.Render(n)
		if err != nil || got != c.want {
			t.Errorf("%q: got %q (%v), want %q", c.sql, got, err, c.want)
		}
	}
}

func TestEscapeLike(t *testing.T) {
	if got := EscapeLike(`50%_off\`); got != `50\%\_off\\` {
		t.Fatal(got)
	}
}
