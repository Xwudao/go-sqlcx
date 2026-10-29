// Package codegen renders the Go source of the dynamic query descriptors.
// Output is deterministic: tables and columns follow the sqlc catalog order,
// imports are sorted, and the result is gofmt-formatted.
package codegen

import (
	"bytes"
	"fmt"
	"go/format"
	"go/types"
	"sort"
	"strconv"
	"strings"

	"github.com/Xwudao/go-sqlcx/internal/postgres"
)

// Version is the generator version written into generated files.
const Version = "v0.1.2"

// File is the generation model of one output package.
type File struct {
	Package     string
	SQLCVersion string
	ModelsPath  string
	Imports     *Imports
	Tables      []Table
	Views       []View
}

// Table is one table descriptor.
type Table struct {
	TypeName   string
	VarName    string
	Model      string
	Schema     string
	Name       string
	Display    string
	Comment    string
	UniqueKeys [][]string
	Columns    []Column
}

// Column is one column descriptor.
type Column struct {
	Field      string // descriptor field name
	ModelField string // model struct field name
	Name       string
	Kind       string // sqlcx constructor suffix: TextColumn, NumberColumn, ...
	T, V, E    string
	Nullable   bool
	PGType     string
	Comment    string
	Note       string
}

// View is one dynamic variant of a sqlc query.
type View struct {
	TypeName  string
	VarName   string
	QueryName string
	Cmd       string
	Row       string
	Alias     string
	Segments  []string
	Params    []int
	Order     []postgres.OrderTerm
	Columns   []Column
	ArgsDecl  string
	ArgsCall  []string
}

// Imports allocates import names.
type Imports struct {
	self  string
	paths map[string]string
	names map[string]string
}

// NewImports returns an import set for the package with import path self.
func NewImports(self string) *Imports {
	return &Imports{self: self, paths: map[string]string{}, names: map[string]string{}}
}

// Add registers path under a preferred name and returns the allocated name.
func (im *Imports) Add(path, name string) string {
	if n, ok := im.paths[path]; ok {
		return n
	}
	base := name
	for i := 2; ; i++ {
		if _, taken := im.names[name]; !taken {
			break
		}
		name = base + strconv.Itoa(i)
	}
	im.paths[path] = name
	im.names[name] = path
	return name
}

// TypeString renders t, registering the packages it mentions.
func (im *Imports) TypeString(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string {
		if p.Path() == im.self {
			return ""
		}
		return im.Add(p.Path(), p.Name())
	})
}

func (im *Imports) render(b *bytes.Buffer) {
	paths := make([]string, 0, len(im.paths))
	for p := range im.paths {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool {
		si, sj := isStd(paths[i]), isStd(paths[j])
		if si != sj {
			return si
		}
		return paths[i] < paths[j]
	})
	b.WriteString("import (\n")
	for i, p := range paths {
		if i > 0 && isStd(p) != isStd(paths[i-1]) {
			b.WriteByte('\n')
		}
		name := im.paths[p]
		last := p[strings.LastIndexByte(p, '/')+1:]
		if name == last {
			fmt.Fprintf(b, "\t%q\n", p)
		} else {
			fmt.Fprintf(b, "\t%s %q\n", name, p)
		}
	}
	b.WriteString(")\n\n")
}

func isStd(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

func goString(s string) string {
	if !strings.ContainsAny(s, "`\r") && strings.ContainsRune(s, '\n') {
		return "`" + s + "`"
	}
	return strconv.Quote(s)
}

func keysLiteral(keys [][]string) string {
	if len(keys) == 0 {
		return "nil"
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		cols := make([]string, len(k))
		for j, c := range k {
			cols[j] = strconv.Quote(c)
		}
		parts[i] = "{" + strings.Join(cols, ", ") + "}"
	}
	return "[][]string{" + strings.Join(parts, ", ") + "}"
}

func (c Column) typeArgs(model string) string {
	n := "sqlcx.NotNull"
	if c.Nullable {
		n = "sqlcx.Nullable"
	}
	if c.Kind == "ArrayColumn" {
		return fmt.Sprintf("%s, %s, %s, %s, %s", model, c.T, c.V, c.E, n)
	}
	return fmt.Sprintf("%s, %s, %s, %s", model, c.T, c.V, n)
}

func (c Column) doc() string {
	null := "NOT NULL"
	if c.Nullable {
		null = "nullable"
	}
	s := fmt.Sprintf("// %s is column %q (%s, %s).", c.Field, c.Name, c.PGType, null)
	if c.Field != c.ModelField {
		s += fmt.Sprintf("\n\t// It is named %s here because %s is used by the descriptor itself;\n\t// the model field is %s.", c.Field, c.ModelField, c.ModelField)
	}
	if c.Comment != "" {
		for _, line := range strings.Split(c.Comment, "\n") {
			s += "\n\t// " + strings.TrimSpace(line)
		}
	}
	if c.Note != "" {
		s += "\n\t// Note: " + c.Note + "."
	}
	return s
}

func writeColumns(b *bytes.Buffer, model string, cols []Column) {
	for _, c := range cols {
		fmt.Fprintf(b, "\t%s\n\t%s sqlcx.%s[%s]\n", c.doc(), c.Field, c.Kind, c.typeArgs(model))
	}
}

func writeConstructors(b *bytes.Buffer, owner, model string, cols []Column) {
	for _, c := range cols {
		fmt.Fprintf(b, "\t\t%s: sqlcx.New%s[%s](%s, %q, func(m *%s) *%s { return &m.%s }),\n",
			c.Field, c.Kind, c.typeArgs(model), owner, c.Name, model, c.T, c.ModelField)
	}
}

func writeScanner(b *bytes.Buffer, name, model string, cols []Column) {
	fmt.Fprintf(b, "func %s(m *%s) []any {\n\treturn []any{", name, model)
	for i, c := range cols {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "&m.%s", c.ModelField)
	}
	b.WriteString("}\n}\n\n")
}

// Generate renders the file.
func Generate(f *File) ([]byte, error) {
	var body bytes.Buffer
	for _, t := range f.Tables {
		scanner := "scan" + t.TypeName
		fmt.Fprintf(&body, "// %s describes table %q for dynamic queries. Rows scan into\n// %s, the model generated by sqlc.\n", t.TypeName, t.Display, t.Model)
		if t.Comment != "" {
			body.WriteString("//\n")
			for _, line := range strings.Split(t.Comment, "\n") {
				fmt.Fprintf(&body, "// %s\n", strings.TrimSpace(line))
			}
		}
		fmt.Fprintf(&body, "type %s struct {\n\t*sqlcx.Table[%s]\n\n", t.TypeName, t.Model)
		writeColumns(&body, t.Model, t.Columns)
		body.WriteString("}\n\n")
		fmt.Fprintf(&body, "// %s is table %q without an alias.\nvar %s = New%s(\"\")\n\n", t.VarName, t.Display, t.VarName, t.TypeName)
		fmt.Fprintf(&body, "// New%s returns the descriptor of table %q under alias (\"\" for none).\n", t.TypeName, t.Display)
		fmt.Fprintf(&body, "func New%s(alias string) %s {\n", t.TypeName, t.TypeName)
		fmt.Fprintf(&body, "\tt := sqlcx.NewTable(sqlcx.TableDef[%s]{\n\t\tSchema: %q,\n\t\tName: %q,\n\t\tAlias: alias,\n\t\tUniqueKeys: %s,\n\t\tScan: %s,\n\t})\n",
			t.Model, t.Schema, t.Name, keysLiteral(t.UniqueKeys), scanner)
		fmt.Fprintf(&body, "\ttbl := %s{\n\t\tTable: t,\n", t.TypeName)
		writeConstructors(&body, "t", t.Model, t.Columns)
		body.WriteString("\t}\n\ttbl.Seal()\n\treturn tbl\n}\n\n")
		fmt.Fprintf(&body, "// As returns table %q under alias, for self-joins and correlated\n// subqueries.\nfunc (t %s) As(alias string) %s { return New%s(alias) }\n\n", t.Display, t.TypeName, t.TypeName, t.TypeName)
		writeScanner(&body, scanner, t.Model, t.Columns)
	}
	for _, v := range f.Views {
		scanner := "scan" + v.TypeName
		ctor := "new" + v.TypeName
		fmt.Fprintf(&body, "// %s is the result set of the sqlc query %s (%s) as a dynamic\n// source: SELECT ... FROM (<%s>) AS %q, with rows scanning into %s.\n",
			v.TypeName, v.QueryName, v.Cmd, v.QueryName, v.Alias, v.Row)
		fmt.Fprintf(&body, "type %s struct {\n\t*sqlcx.QueryView[%s]\n\n", v.TypeName, v.Row)
		writeColumns(&body, v.Row, v.Columns)
		body.WriteString("}\n\n")
		fmt.Fprintf(&body, "// %s is the dynamic variant of the sqlc query %s.\nvar %s = %s()\n\n", v.VarName, v.QueryName, v.VarName, ctor)
		fmt.Fprintf(&body, "func %s() %s {\n\tv := sqlcx.NewQueryView(%q, sqlcx.QueryTemplate{\n\t\tSegments: []string{\n", ctor, v.TypeName, v.Alias)
		for _, s := range v.Segments {
			fmt.Fprintf(&body, "\t\t\t%s,\n", goString(s))
		}
		params := make([]string, len(v.Params))
		for i, p := range v.Params {
			params[i] = strconv.Itoa(p)
		}
		fmt.Fprintf(&body, "\t\t},\n\t\tParams: []int{%s},\n\t}, ", strings.Join(params, ", "))
		if len(v.Order) == 0 {
			body.WriteString("nil")
		} else {
			body.WriteString("[]sqlcx.QueryOrder{")
			for i, o := range v.Order {
				if i > 0 {
					body.WriteString(", ")
				}
				fmt.Fprintf(&body, "{Column: %q", o.Column)
				if o.Desc {
					body.WriteString(", Desc: true")
				}
				if o.Nulls != 0 {
					fmt.Fprintf(&body, ", Nulls: %d", o.Nulls)
				}
				body.WriteString("}")
			}
			body.WriteString("}")
		}
		fmt.Fprintf(&body, ", %s)\n\tview := %s{\n\t\tQueryView: v,\n", scanner, v.TypeName)
		writeConstructors(&body, "v", v.Row, v.Columns)
		body.WriteString("\t}\n\tview.Seal()\n\treturn view\n}\n\n")
		call := strings.Join(v.ArgsCall, ", ")
		fmt.Fprintf(&body, "// Query binds the parameters of the sqlc query %s and starts a dynamic\n// SELECT over its result rows. The query's own ORDER BY applies until\n// OrderBy is called.\nfunc (v %s) Query(%s) sqlcx.Select[%s] {\n\treturn v.Select(%s)\n}\n\n",
			v.QueryName, v.TypeName, v.ArgsDecl, v.Row, call)
		fmt.Fprintf(&body, "// Source binds the parameters of the sqlc query %s and returns its result\n// set as a FROM item for joins.\nfunc (v %s) Source(%s) sqlcx.BoundView {\n\treturn v.Bind(%s)\n}\n\n",
			v.QueryName, v.TypeName, v.ArgsDecl, call)
		writeScanner(&body, scanner, v.Row, v.Columns)
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by sqlc-dynamic %s. DO NOT EDIT.\n// sqlc: %s\n// models: %s\n\n", Version, f.SQLCVersion, f.ModelsPath)
	fmt.Fprintf(&b, "// Package %s holds type-safe dynamic query descriptors for the sqlc\n// package %s.\npackage %s\n\n", f.Package, f.ModelsPath, f.Package)
	f.Imports.render(&b)
	b.Write(body.Bytes())
	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated code: %w\n%s", err, b.Bytes())
	}
	return out, nil
}
