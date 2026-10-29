// Package config reads the parts of sqlc.yaml (version 2) that the linker
// needs: the sqlc-dynamic codegen targets, the schema files of their sql
// block and the gen.go options that influence Go names.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Xwudao/go-sqlcx/internal/manifest"
)

// PluginCommand is the executable name that identifies sqlc-dynamic plugin
// entries.
const PluginCommand = "sqlc-dynamic"

// Paths lists the configuration file names sqlc looks for, in order.
var Paths = []string{"sqlc.yaml", "sqlc.yml", "sqlc.json"}

type stringList []string

func (s *stringList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*s = []string{n.Value}
		return nil
	case yaml.SequenceNode:
		var out []string
		if err := n.Decode(&out); err != nil {
			return err
		}
		*s = out
		return nil
	}
	return fmt.Errorf("line %d: expected a string or a list of strings", n.Line)
}

type file struct {
	Version   string     `yaml:"version"`
	Plugins   []plugin   `yaml:"plugins"`
	SQL       []sqlBlock `yaml:"sql"`
	Overrides struct {
		Go struct {
			Rename map[string]string `yaml:"rename"`
		} `yaml:"go"`
	} `yaml:"overrides"`
}

type plugin struct {
	Name    string `yaml:"name"`
	Process *struct {
		Cmd string `yaml:"cmd"`
	} `yaml:"process"`
	WASM *struct {
		URL string `yaml:"url"`
	} `yaml:"wasm"`
}

type sqlBlock struct {
	Engine  string     `yaml:"engine"`
	Schema  stringList `yaml:"schema"`
	Queries stringList `yaml:"queries"`
	Gen     struct {
		Go *GoOptions `yaml:"go"`
	} `yaml:"gen"`
	Codegen []codegen `yaml:"codegen"`
}

type codegen struct {
	Plugin  string    `yaml:"plugin"`
	Out     string    `yaml:"out"`
	Options yaml.Node `yaml:"options"`
}

// GoOptions are the gen.go settings relevant to Go naming.
type GoOptions struct {
	Package                     string            `yaml:"package"`
	Out                         string            `yaml:"out"`
	SQLPackage                  string            `yaml:"sql_package"`
	EmitDBTags                  bool              `yaml:"emit_db_tags"`
	EmitExactTableNames         bool              `yaml:"emit_exact_table_names"`
	InflectionExcludeTableNames []string          `yaml:"inflection_exclude_table_names"`
	Rename                      map[string]string `yaml:"rename"`
	Initialisms                 *[]string         `yaml:"initialisms"`
}

// Target is one sqlc-dynamic codegen entry.
type Target struct {
	ConfigDir string
	Engine    string
	Schema    []string // absolute paths
	OutDir    string   // absolute
	ModelsDir string   // absolute
	Options   manifest.Options
	Go        GoOptions // effective (global rename merged)
	// HasGoGen reports whether Go options came from gen.go of the block.
	HasGoGen bool
}

// Find locates the sqlc configuration file in dir.
func Find(dir string) (string, error) {
	for _, p := range Paths {
		full := filepath.Join(dir, p)
		if _, err := os.Stat(full); err == nil {
			return full, nil
		}
	}
	return "", fmt.Errorf("no sqlc configuration (%s) found in %s", strings.Join(Paths, ", "), dir)
}

// Load parses the configuration and returns every sqlc-dynamic target.
// pluginName, when non-empty, selects the plugin by name instead of by
// its process command.
func Load(path, pluginName string) ([]Target, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f file
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Version != "2" {
		return nil, fmt.Errorf("%s: sqlc-dynamic requires configuration version \"2\", got %q", path, f.Version)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(abs)

	names := map[string]bool{}
	for _, p := range f.Plugins {
		switch {
		case pluginName != "":
			if p.Name == pluginName {
				names[p.Name] = true
			}
		case p.Process != nil && strings.TrimSuffix(filepath.Base(p.Process.Cmd), ".exe") == PluginCommand:
			names[p.Name] = true
		case p.WASM != nil && strings.Contains(p.WASM.URL, PluginCommand):
			names[p.Name] = true
		}
	}
	if len(names) == 0 {
		if pluginName != "" {
			return nil, fmt.Errorf("%s: no plugin named %q", path, pluginName)
		}
		return nil, fmt.Errorf("%s: no plugin with process cmd %q; add it under plugins and reference it from sql[].codegen", path, PluginCommand)
	}

	var targets []Target
	for i, blk := range f.SQL {
		for _, cg := range blk.Codegen {
			if !names[cg.Plugin] {
				continue
			}
			if blk.Engine != "postgresql" {
				return nil, fmt.Errorf("%s: sql[%d]: sqlc-dynamic supports only engine postgresql, got %q", path, i, blk.Engine)
			}
			if cg.Out == "" {
				return nil, fmt.Errorf("%s: sql[%d]: codegen %q has no out directory", path, i, cg.Plugin)
			}
			t := Target{ConfigDir: dir, Engine: blk.Engine, OutDir: filepath.Join(dir, cg.Out)}
			for _, s := range blk.Schema {
				t.Schema = append(t.Schema, filepath.Join(dir, s))
			}
			if !cg.Options.IsZero() {
				if err := decodeOptions(&cg.Options, &t.Options); err != nil {
					return nil, fmt.Errorf("%s: sql[%d]: codegen %q options: %w", path, i, cg.Plugin, err)
				}
			}
			if blk.Gen.Go != nil {
				t.Go = *blk.Gen.Go
				t.HasGoGen = true
			}
			if len(f.Overrides.Go.Rename) > 0 {
				merged := map[string]string{}
				for k, v := range t.Go.Rename {
					merged[k] = v
				}
				for k, v := range f.Overrides.Go.Rename {
					merged[k] = v
				}
				t.Go.Rename = merged
			}
			switch {
			case t.Options.Models != "":
				t.ModelsDir = filepath.Join(dir, t.Options.Models)
			case blk.Gen.Go != nil && blk.Gen.Go.Out != "":
				t.ModelsDir = filepath.Join(dir, blk.Gen.Go.Out)
				if sp := blk.Gen.Go.SQLPackage; sp != "pgx/v5" {
					return nil, fmt.Errorf("%s: sql[%d]: gen.go.sql_package must be \"pgx/v5\" for sqlc-dynamic, got %q", path, i, sp)
				}
			default:
				return nil, fmt.Errorf("%s: sql[%d]: cannot locate the sqlc Go models; set gen.go.out or the plugin option \"models\"", path, i)
			}
			if filepath.Clean(t.ModelsDir) == filepath.Clean(t.OutDir) {
				return nil, fmt.Errorf("%s: sql[%d]: the sqlc-dynamic out directory must differ from the sqlc Go package directory", path, i)
			}
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("%s: no sql block uses the sqlc-dynamic plugin in codegen", path)
	}
	sort.SliceStable(targets, func(i, j int) bool { return targets[i].OutDir < targets[j].OutDir })
	for i := 1; i < len(targets); i++ {
		if targets[i].OutDir == targets[i-1].OutDir {
			return nil, errors.New(path + ": two sqlc-dynamic targets share the out directory " + targets[i].OutDir)
		}
	}
	return targets, nil
}

// decodeOptions converts YAML options to JSON and decodes them strictly,
// exactly as sqlc forwards them to the plugin.
func decodeOptions(n *yaml.Node, out *manifest.Options) error {
	var v any
	if err := n.Decode(&v); err != nil {
		return err
	}
	j, err := json.Marshal(normalize(v))
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(j))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = normalize(e)
		}
		return x
	case map[any]any:
		m := map[string]any{}
		for k, e := range x {
			m[fmt.Sprint(k)] = normalize(e)
		}
		return m
	case []any:
		for i, e := range x {
			x[i] = normalize(e)
		}
	}
	return v
}
