// Package manifest defines the metadata file exchanged between the sqlc
// codegen plugin (which sees sqlc's catalog and analysed queries) and the
// model linker (which sees the Go code sqlc generated). sqlc runs all
// codegen targets concurrently and writes files only at the end, so the
// plugin cannot read the generated models itself; the manifest decouples
// the two steps.
package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// FileName is the manifest file written into the plugin's output directory.
const FileName = "sqlcx.manifest.json"

// Version is the manifest format version.
const Version = 1

// Manifest is the plugin output.
type Manifest struct {
	Version       int      `json:"version"`
	SQLCVersion   string   `json:"sqlc_version"`
	Engine        string   `json:"engine"`
	DefaultSchema string   `json:"default_schema"`
	Schemas       []Schema `json:"schemas"`
	Queries       []Query  `json:"queries"`
	Options       Options  `json:"options"`
}

// Options are the plugin options from sqlc.yaml (codegen[].options).
type Options struct {
	// Package is the Go package name of the generated dynamic code.
	Package string `json:"package,omitempty"`
	// Models is the directory (relative to sqlc.yaml) of the sqlc generated
	// Go package. Defaults to gen.go.out of the same sql block.
	Models string `json:"models,omitempty"`
	// Output is the generated Go file name (default sqlcx_gen.go).
	Output string `json:"output,omitempty"`
	// Queries lists sqlc query names for which dynamic variants are
	// generated; "*" selects every eligible query. Queries can also opt in
	// with a "-- sqlcx:dynamic" comment.
	Queries []string `json:"queries,omitempty"`
	// UniqueKeys declares additional unique keys ("table" or
	// "schema.table" -> list of column lists) for order-stability checks.
	UniqueKeys map[string][][]string `json:"unique_keys,omitempty"`
	// SkipTables excludes tables ("table" or "schema.table").
	SkipTables []string `json:"skip_tables,omitempty"`
}

// Schema is one catalog schema.
type Schema struct {
	Name   string  `json:"name"`
	Tables []Table `json:"tables"`
	Enums  []Enum  `json:"enums,omitempty"`
}

// Table is one catalog table or view.
type Table struct {
	Schema  string   `json:"schema"`
	Name    string   `json:"name"`
	Comment string   `json:"comment,omitempty"`
	Columns []Column `json:"columns"`
}

// Enum is one catalog enum type.
type Enum struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Identifier is a qualified name.
type Identifier struct {
	Schema string `json:"schema,omitempty"`
	Name   string `json:"name"`
}

// Column is a table column or a query output/parameter column.
type Column struct {
	Name         string      `json:"name"`
	OriginalName string      `json:"original_name,omitempty"`
	Type         Identifier  `json:"type"`
	NotNull      bool        `json:"not_null"`
	IsArray      bool        `json:"is_array,omitempty"`
	ArrayDims    int         `json:"array_dims,omitempty"`
	Comment      string      `json:"comment,omitempty"`
	Table        *Identifier `json:"table,omitempty"`
	EmbedTable   *Identifier `json:"embed_table,omitempty"`
	IsSqlcSlice  bool        `json:"is_sqlc_slice,omitempty"`
	IsNamedParam bool        `json:"is_named_param,omitempty"`
	IsFuncCall   bool        `json:"is_func_call,omitempty"`
}

// Param is a query parameter.
type Param struct {
	Number int    `json:"number"`
	Column Column `json:"column"`
}

// Query is one analysed sqlc query.
type Query struct {
	Name     string   `json:"name"`
	Cmd      string   `json:"cmd"`
	Text     string   `json:"text"`
	Filename string   `json:"filename"`
	Comments []string `json:"comments,omitempty"`
	Columns  []Column `json:"columns,omitempty"`
	Params   []Param  `json:"params,omitempty"`
}

// Marshal encodes the manifest deterministically.
func (m *Manifest) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Read loads a manifest file.
func Read(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w (run `sqlc generate` first)", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	if m.Version != Version {
		return nil, fmt.Errorf("manifest %s has version %d, this sqlc-dynamic expects %d; re-run `sqlc generate`", path, m.Version, Version)
	}
	return &m, nil
}
