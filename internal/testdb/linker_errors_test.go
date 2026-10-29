package testdb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/go-sqlcx/internal/config"
	"github.com/Xwudao/go-sqlcx/internal/linker"
)

// linkWith writes a sqlc.yaml variant next to the real project (so that the
// models package and schema resolve) and links it.
func linkWith(t *testing.T, options string) error {
	return linkWithRename(t, options, "rename: {is_admin: Administrator}")
}

func linkWithRename(t *testing.T, options, rename string) error {
	t.Helper()
	dir, err := os.MkdirTemp(".", "tmp-link-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := os.ReadFile("dynamic/sqlcx.manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "sqlcx.manifest.json"), m, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := `version: "2"
plugins:
  - name: sqlcx
    process: {cmd: sqlc-dynamic}
sql:
  - engine: postgresql
    schema: ../schema
    queries: ../queries
    gen:
      go: {package: db, out: ../db, sql_package: pgx/v5, ` + rename + `}
    codegen:
      - plugin: sqlcx
        out: out
        options:
` + options
	path := filepath.Join(dir, "sqlc.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	targets, err := config.Load(path, "")
	if err != nil {
		return err
	}
	_, err = linker.Link(targets[0])
	return err
}

func TestLinkerErrors(t *testing.T) {
	if err := linkWith(t, "          package: out\n"); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	cases := map[string]string{
		"          queries: [ListRecentOrders]\n": "LIMIT/OFFSET",
		"          queries: [CountUsers]\n":       "row struct",
		"          queries: [NoSuchQuery]\n":      "no such query",
		"          bogus: 1\n":                    "unknown field",
	}
	for opts, want := range cases {
		err := linkWith(t, opts)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", strings.TrimSpace(opts), err, want)
		}
	}
	// A naming configuration that does not match the generated code is
	// detected instead of silently mapping columns to the wrong fields.
	err := linkWithRename(t, "          package: out\n", "emit_json_tags: true")
	if err == nil || !strings.Contains(err.Error(), "expected IsAdmin") {
		t.Errorf("rename mismatch: %v", err)
	}
}
