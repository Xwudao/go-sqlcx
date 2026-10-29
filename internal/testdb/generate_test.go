// Package testdb is a real sqlc project used by the tests: schema/ and
// queries/ are compiled by sqlc into db/, and sqlc-dynamic generates
// dynamic/ from them. Regenerate with `make generate` in the repository root.
package testdb

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Xwudao/go-sqlcx/internal/codegen"
	"github.com/Xwudao/go-sqlcx/internal/config"
	"github.com/Xwudao/go-sqlcx/internal/linker"
)

// TestGeneratedCodeUpToDate re-runs the linker on the committed manifest and
// sqlc output and requires byte-identical output (determinism and freshness).
func TestGeneratedCodeUpToDate(t *testing.T) {
	targets, err := config.Load("sqlc.yaml", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range targets {
		for i := range 2 {
			f, err := linker.Link(tg)
			if err != nil {
				t.Fatal(err)
			}
			src, err := codegen.Generate(f)
			if err != nil {
				t.Fatal(err)
			}
			old, err := os.ReadFile(filepath.Join(tg.OutDir, "sqlcx_gen.go"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(old, src) {
				t.Fatalf("run %d: generated code is stale; run make generate", i)
			}
		}
	}
}

// TestSqlcPluginRoundTrip runs the real sqlc binary with the plugin (when
// sqlc is installed) and checks with `sqlc diff` that the committed sqlc
// output and manifest are exactly what sqlc produces.
func TestSqlcPluginRoundTrip(t *testing.T) {
	sqlc, err := exec.LookPath("sqlc")
	if err != nil {
		t.Skip("sqlc not installed")
	}
	bin := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "sqlc-dynamic"), "github.com/Xwudao/go-sqlcx/cmd/sqlc-dynamic")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, out)
	}
	diff := exec.Command(sqlc, "diff")
	diff.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := diff.CombinedOutput(); err != nil {
		t.Fatalf("sqlc diff: %v\n%s", err, out)
	}
}
