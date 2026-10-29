// Command sqlc-dynamic is both the sqlc codegen plugin and the model linker
// of go-sqlcx.
//
// As a plugin it is invoked by sqlc (process plugin protocol) and writes
// sqlcx.manifest.json into the codegen output directory. The link step then
// reads the manifest together with the Go code sqlc generated and writes the
// type-safe dynamic query descriptors:
//
//	sqlc generate && sqlc-dynamic link
//
// or, equivalently,
//
//	sqlc-dynamic generate
//go:build !wasip1

package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sqlc-dev/plugin-sdk-go/codegen"

	cg "github.com/Xwudao/go-sqlcx/internal/codegen"
	"github.com/Xwudao/go-sqlcx/internal/config"
	"github.com/Xwudao/go-sqlcx/internal/linker"
	"github.com/Xwudao/go-sqlcx/internal/plugin"
)

func main() {
	// sqlc invokes process plugins with the gRPC method name as the only
	// argument.
	if len(os.Args) == 2 && strings.HasPrefix(os.Args[1], "/") {
		codegen.Run(plugin.Generate)
		return
	}
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "sqlc-dynamic:", err)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage:
  sqlc-dynamic link     [-f sqlc.yaml] [-plugin name] [-check]
  sqlc-dynamic generate [-f sqlc.yaml] [-plugin name] [-sqlc path] [-check]
  sqlc-dynamic version

link      reads the manifest written by the plugin and the Go code generated
          by sqlc, and writes the dynamic query descriptors.
generate  runs "sqlc generate" and then link.
-check    verifies that the generated files are up to date without writing;
          with generate it runs "sqlc diff" instead of "sqlc generate".
`)
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stderr)
		return errors.New("missing command")
	}
	cmd, args := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("f", "", "path to the sqlc configuration file")
	pluginName := fs.String("plugin", "", "name of the sqlc-dynamic plugin entry (default: detect by process cmd)")
	check := fs.Bool("check", false, "fail if generated files are out of date instead of writing them")
	sqlcBin := fs.String("sqlc", "", "sqlc command (generate only); default: \"go tool sqlc\" if the module of the working directory or of the configuration declares it, else sqlc from PATH")
	switch cmd {
	case "version":
		fmt.Fprintln(stdout, "sqlc-dynamic", cg.Version)
		return nil
	case "link", "generate":
	case "help", "-h", "--help":
		usage(stdout)
		return nil
	default:
		usage(stderr)
		return fmt.Errorf("unknown command %q", cmd)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := *file
	if path == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		if path, err = config.Find(wd); err != nil {
			return err
		}
	}
	if cmd == "generate" {
		if err := runSqlc(*sqlcBin, path, *check, stdout, stderr); err != nil {
			return err
		}
	}
	return link(path, *pluginName, *check, stdout)
}

func link(path, pluginName string, check bool, stdout io.Writer) error {
	targets, err := config.Load(path, pluginName)
	if err != nil {
		return err
	}
	var stale []string
	for _, t := range targets {
		f, err := linker.Link(t)
		if err != nil {
			return fmt.Errorf("%s: %w", rel(t.ConfigDir, t.OutDir), err)
		}
		src, err := cg.Generate(f)
		if err != nil {
			return err
		}
		name := t.Options.Output
		if name == "" {
			name = "sqlcx_gen.go"
		}
		if filepath.Base(name) != name || !strings.HasSuffix(name, ".go") {
			return fmt.Errorf("invalid output file name %q", name)
		}
		out := filepath.Join(t.OutDir, name)
		old, _ := os.ReadFile(out)
		if bytes.Equal(old, src) {
			continue
		}
		if check {
			stale = append(stale, rel(t.ConfigDir, out))
			continue
		}
		if err := writeFile(out, src); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "wrote", rel(t.ConfigDir, out))
	}
	if len(stale) > 0 {
		return fmt.Errorf("generated files are out of date: %s (run sqlc-dynamic generate)", strings.Join(stale, ", "))
	}
	return nil
}

// runSqlc runs `sqlc generate`, or `sqlc diff` when check is set. The running
// sqlc-dynamic executable is put first on the child's PATH under the name sqlc
// sees in sqlc.yaml, so the plugin resolves even when sqlc-dynamic was started
// with `go tool` and is not installed.
func runSqlc(sqlcCmd, configPath string, check bool, stdout, stderr io.Writer) error {
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	argv, dir, err := resolveSqlc(sqlcCmd, []string{wd, filepath.Dir(configPath)}, goToolDeclares, exec.LookPath)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	binDir, err := os.MkdirTemp("", "sqlc-dynamic-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(binDir)
	if err := os.Symlink(self, filepath.Join(binDir, config.PluginCommand)); err != nil {
		return err
	}
	action := "generate"
	if check {
		action = "diff"
	}
	c := exec.Command(argv[0], append(argv[1:], action, "-f", configPath)...)
	c.Dir = dir
	c.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	c.Stdout, c.Stderr = stdout, stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", strings.Join(argv, " "), action, err)
	}
	return nil
}

// resolveSqlc picks the sqlc command and the directory to run it in. An
// explicit command runs in the configuration directory (the last of dirs).
// Otherwise the version pinned by a `tool` directive wins, looked up in dirs
// in order: the working directory comes first so that a nested tools module
// (cd tools && go tool sqlc-dynamic generate -f ../sqlc.yaml) uses its own
// pin, and `go tool` has to run inside that module. sqlc from PATH is the
// fallback.
func resolveSqlc(sqlcCmd string, dirs []string, declares func(dir string) bool, lookPath func(string) (string, error)) ([]string, string, error) {
	configDir := dirs[len(dirs)-1]
	if argv := strings.Fields(sqlcCmd); len(argv) > 0 {
		return argv, configDir, nil
	}
	for _, d := range dirs {
		if declares(d) {
			return []string{"go", "tool", "sqlc"}, d, nil
		}
	}
	if p, err := lookPath("sqlc"); err == nil {
		return []string{p}, configDir, nil
	}
	return nil, "", errors.New("sqlc not found: install it, add it as a go.mod tool, or pass -sqlc")
}

// goToolDeclares reports whether the module containing dir declares sqlc as
// a tool.
func goToolDeclares(dir string) bool {
	probe := exec.Command("go", "tool", "-n", "sqlc")
	probe.Dir = dir
	return probe.Run() == nil
}

// writeFile replaces path atomically.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sqlcx-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func rel(base, p string) string {
	if r, err := filepath.Rel(base, p); err == nil {
		return r
	}
	return p
}
