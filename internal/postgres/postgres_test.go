package postgres

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAnalyzeQuery(t *testing.T) {
	v, err := AnalyzeQuery("-- name: X :many\nSELECT id, name FROM users WHERE a = $1 AND b = $2 OR c = $1 ORDER BY name DESC NULLS LAST, users.id;", []string{"id", "name"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v.Params, []int{1, 2, 1}) {
		t.Fatalf("params %v", v.Params)
	}
	if strings.Contains(strings.ToUpper(strings.Join(v.Segments, "")), "ORDER BY") {
		t.Fatalf("ORDER BY not removed: %q", v.Segments)
	}
	want := []OrderTerm{{Column: "name", Desc: true, Nulls: 2}, {Column: "id"}}
	if !reflect.DeepEqual(v.Order, want) {
		t.Fatalf("order %+v", v.Order)
	}
	// Without ORDER BY the original text is kept verbatim (minus the semicolon).
	v, err = AnalyzeQuery("SELECT id FROM users WHERE note = '$1 is not a param' AND id = $1;", []string{"id"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v.Segments, []string{"SELECT id FROM users WHERE note = '$1 is not a param' AND id = ", ""}) {
		t.Fatalf("%q", v.Segments)
	}

	bad := map[string]string{
		"SELECT id FROM users LIMIT 10":                     "LIMIT",
		"SELECT id FROM a UNION SELECT id FROM b":           "set operations",
		"SELECT id FROM users ORDER BY lower(name)":         "expression",
		"SELECT id FROM users ORDER BY name":                "not an output column",
		"SELECT DISTINCT ON (id) id FROM users ORDER BY id": "DISTINCT ON",
		"SELECT id FROM users FOR UPDATE":                   "row-locking",
		"UPDATE users SET id = 1":                           "only SELECT",
		"SELECT id FROM users WHERE id = $3":                "exceeds",
	}
	for sql, want := range bad {
		_, err := AnalyzeQuery(sql, []string{"id"}, 1)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", sql, err, want)
		}
	}
	if _, err := AnalyzeQuery("SELECT a.id, b.id FROM a, b", []string{"id", "id"}, 0); err == nil {
		t.Error("duplicate output names accepted")
	}
}

func TestExtractUniqueKeys(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("001.up.sql", `
CREATE TABLE a (id int PRIMARY KEY, code text UNIQUE, x int, y int, UNIQUE (x, y));
CREATE UNIQUE INDEX a_partial ON a (x) WHERE x > 0;
CREATE UNIQUE INDEX a_expr ON a (lower(code));
CREATE TABLE s.b (k int, CONSTRAINT b_pk PRIMARY KEY (k));
ALTER TABLE a ADD CONSTRAINT a_x_key UNIQUE (x);
`)
	write("001.down.sql", `DROP TABLE a;`)
	write("002.sql", `
-- +goose Up
ALTER TABLE a DROP CONSTRAINT a_x_key;
ALTER TABLE a RENAME COLUMN code TO slug;
ALTER TABLE s.b RENAME TO c;
\connect other
-- +goose Down
DROP TABLE a;
`)
	files, err := SchemaFiles([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files %v", files)
	}
	keys, err := ExtractUniqueKeys(files, "public")
	if err != nil {
		t.Fatal(err)
	}
	if got := keys[TableKey{"public", "a"}]; !reflect.DeepEqual(got, [][]string{{"id"}, {"slug"}, {"x", "y"}}) {
		t.Fatalf("a: %v", got)
	}
	if got := keys[TableKey{"s", "c"}]; !reflect.DeepEqual(got, [][]string{{"k"}}) {
		t.Fatalf("c: %v (all %v)", got, keys)
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]Category{
		NormalizeType("", "bigserial"):                CatNumber,
		NormalizeType("pg_catalog", "varchar"):        CatText,
		NormalizeType("", "timestamp with time zone"): CatOrdered,
		NormalizeType("pg_catalog", "bool"):           CatBool,
		NormalizeType("", "jsonb"):                    CatJSONB,
		NormalizeType("", "json"):                     CatJSON,
		NormalizeType("", "point"):                    CatValue,
		NormalizeType("", "hstore"):                   CatComparable,
	}
	for typ, want := range cases {
		if got := Classify(typ, false); got != want {
			t.Errorf("%s: %v, want %v", typ, got, want)
		}
	}
	if Classify("app.status", true) != CatOrdered {
		t.Error("enum")
	}
}
