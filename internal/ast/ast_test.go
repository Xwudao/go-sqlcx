package ast

import (
	"strings"
	"testing"
)

func TestIdentQuoting(t *testing.T) {
	w := NewWriter(0)
	w.Ident(`we"ird`)
	if s, _, _ := w.Result(); s != `"we""ird"` {
		t.Fatal(s)
	}
	w = NewWriter(0)
	w.Ident("a\x00b")
	if _, _, err := w.Result(); err == nil {
		t.Fatal("NUL accepted")
	}
	if _, _, err := Render(&Column{Name: ""}); err == nil {
		t.Fatal("empty identifier accepted")
	}
}

func TestParamNumberingAcrossSubqueries(t *testing.T) {
	sub := &Select{Items: []SelectItem{{X: Param{V: 2}}}, Where: &Binary{Op: "=", L: Param{V: 3}, R: Param{V: 4}, P: PrecCompare}}
	st := &Select{
		Items: []SelectItem{{X: Param{V: 1}}},
		Where: &Logical{Items: []Node{&Exists{Sub: sub}, &Binary{Op: "=", L: Param{V: 5}, R: Param{V: 6}, P: PrecCompare}}},
	}
	sql, args, err := Render(st)
	if err != nil {
		t.Fatal(err)
	}
	if want := "SELECT $1 WHERE EXISTS (SELECT $2 WHERE $3 = $4) AND $5 = $6"; sql != want {
		t.Fatalf("%s\n%s", sql, want)
	}
	for i, a := range args {
		if a != i+1 {
			t.Fatalf("args %v", args)
		}
	}
}

func TestTemplateRenumbering(t *testing.T) {
	tmpl := &Template{Segments: []string{"SELECT * FROM t WHERE a = ", " OR b = ", " AND c = ", ""}, Refs: []int{1, 2, 1}, Args: []any{"x", "y"}}
	st := &Select{Items: []SelectItem{{X: Param{V: 0}}}, From: []Node{&DerivedSource{Sub: tmpl, Alias: &NamedRel{Name: "q"}}}, Where: &Binary{Op: "=", L: Param{V: "z"}, R: True, P: PrecCompare}}
	sql, args, err := Render(st)
	if err != nil {
		t.Fatal(err)
	}
	if want := `SELECT $1 FROM (SELECT * FROM t WHERE a = $2 OR b = $3 AND c = $2) AS "q" WHERE $4 = TRUE`; sql != want {
		t.Fatalf("%s", sql)
	}
	if len(args) != 4 || args[1] != "x" || args[2] != "y" || args[3] != "z" {
		t.Fatalf("%v", args)
	}
}

func TestPrecedence(t *testing.T) {
	or := &Logical{Or: true, Items: []Node{Param{V: 1}, Param{V: 2}}}
	and := &Logical{Items: []Node{or, &Prefix{Op: "NOT", X: &Logical{Items: []Node{Param{V: 3}, Param{V: 4}}}, P: PrecNot}}}
	sql, _, _ := Render(and)
	if sql != "($1 OR $2) AND NOT ($3 AND $4)" {
		t.Fatal(sql)
	}
	sum := &Binary{Op: "*", L: &Binary{Op: "+", L: Param{V: 1}, R: Param{V: 2}, P: PrecAdd}, R: Param{V: 3}, P: PrecMul}
	if sql, _, _ := Render(sum); sql != "($1 + $2) * $3" {
		t.Fatal(sql)
	}
}

func TestTooManyParams(t *testing.T) {
	items := make([]Node, MaxParams+1)
	for i := range items {
		items[i] = Param{V: i}
	}
	_, _, err := Render(&In{X: Param{V: 0}, Items: items})
	if err == nil || !strings.Contains(err.Error(), "65535") {
		t.Fatal(err)
	}
}

func TestValidation(t *testing.T) {
	for _, ok := range []string{"u", "_x1", "Users"} {
		if ValidateAlias(ok) != nil {
			t.Fatal(ok)
		}
	}
	for _, bad := range []string{"", "1a", `a"b`, "a b", "a;b", strings.Repeat("a", 64)} {
		if ValidateAlias(bad) == nil {
			t.Fatal(bad)
		}
	}
	for _, ok := range []string{"text", "numeric(10,2)", "timestamp with time zone", "text[]", "app.money[]"} {
		if ValidateTypeName(ok) != nil {
			t.Fatal(ok)
		}
	}
	for _, bad := range []string{"text; drop", "int)--", "a'b"} {
		if ValidateTypeName(bad) == nil {
			t.Fatal(bad)
		}
	}
}
