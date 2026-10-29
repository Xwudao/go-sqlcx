//go:build !wasip1

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestResolveSqlc(t *testing.T) {
	noPath := func(string) (string, error) { return "", errors.New("not found") }
	onPath := func(string) (string, error) { return "/usr/bin/sqlc", nil }
	declaredIn := func(dirs ...string) func(string) bool {
		return func(d string) bool { return slices.Contains(dirs, d) }
	}
	dirs := []string{"/proj/tools", "/proj"}
	cases := []struct {
		name     string
		cmd      string
		declares func(string) bool
		lookPath func(string) (string, error)
		argv     []string
		dir      string
		err      string
	}{
		{name: "nested tools module", declares: declaredIn("/proj/tools"), lookPath: onPath, argv: []string{"go", "tool", "sqlc"}, dir: "/proj/tools"},
		{name: "working directory first", declares: declaredIn("/proj/tools", "/proj"), lookPath: onPath, argv: []string{"go", "tool", "sqlc"}, dir: "/proj/tools"},
		{name: "configuration module", declares: declaredIn("/proj"), lookPath: onPath, argv: []string{"go", "tool", "sqlc"}, dir: "/proj"},
		{name: "path fallback", declares: declaredIn(), lookPath: onPath, argv: []string{"/usr/bin/sqlc"}, dir: "/proj"},
		{name: "explicit command", cmd: "go tool sqlc", declares: declaredIn("/proj/tools"), lookPath: onPath, argv: []string{"go", "tool", "sqlc"}, dir: "/proj"},
		{name: "not found", declares: declaredIn(), lookPath: noPath, err: "sqlc not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			argv, dir, err := resolveSqlc(c.cmd, dirs, c.declares, c.lookPath)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(argv, c.argv) || dir != c.dir {
				t.Fatalf("got %q in %s, want %q in %s", argv, dir, c.argv, c.dir)
			}
		})
	}
}

// TestGenerateCheckRunsDiff runs `generate -check` with a fake sqlc and
// requires that it asks sqlc for a diff, not a generate, and that the link
// step only verifies.
func TestGenerateCheckRunsDiff(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake sqlc is a shell script")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "sqlc")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := filepath.Abs("../../internal/testdb/sqlc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	gen := filepath.Join(filepath.Dir(cfg), "dynamic", "sqlcx_gen.go")
	before, err := os.Stat(gen)
	if err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"generate", "-check", "-sqlc", fake, "-f", cfg}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := "diff\n-f\n" + cfg + "\n"; string(got) != want {
		t.Fatalf("sqlc args = %q, want %q", got, want)
	}
	after, err := os.Stat(gen)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("generate -check rewrote the generated file")
	}

	if err := run([]string{"generate", "-sqlc", fake, "-f", cfg}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(argsFile); !strings.HasPrefix(string(got), "generate\n") {
		t.Fatalf("sqlc args = %q, want generate", got)
	}
}
