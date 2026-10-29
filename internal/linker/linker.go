// Package linker connects the sqlc catalog (from the plugin manifest) with
// the Go code sqlc actually generated. It loads the sqlc package with
// go/types and maps every table column to the model struct field sqlc
// produced, so overrides, renames, nullable wrappers, enums and custom
// types are taken from the real generated code rather than re-derived.
package linker

import (
	"errors"
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/Xwudao/go-sqlcx/internal/codegen"
	"github.com/Xwudao/go-sqlcx/internal/config"
	"github.com/Xwudao/go-sqlcx/internal/manifest"
	"github.com/Xwudao/go-sqlcx/internal/postgres"
)

const pgtypePath = "github.com/jackc/pgx/v5/pgtype"

// Link builds the code generation model for one target.
func Link(t config.Target) (*codegen.File, error) {
	m, err := manifest.Read(filepath.Join(t.OutDir, manifest.FileName))
	if err != nil {
		return nil, err
	}
	if m.Engine != "postgresql" {
		return nil, fmt.Errorf("manifest engine %q is not postgresql", m.Engine)
	}
	// The options in sqlc.yaml are authoritative (the manifest copy may be
	// stale if the config changed after the last sqlc generate).
	opts := t.Options
	pkgName := opts.Package
	if pkgName == "" {
		pkgName = filepath.Base(t.OutDir)
	}
	if !isIdent(pkgName) {
		return nil, fmt.Errorf("invalid Go package name %q (set the plugin option \"package\")", pkgName)
	}

	pkg, err := loadModels(t.ModelsDir)
	if err != nil {
		return nil, err
	}
	outPath, err := importPathOf(pkg, t.OutDir)
	if err != nil {
		return nil, err
	}
	if importsPath(pkg.Types, outPath, map[*types.Package]bool{}) {
		return nil, fmt.Errorf("the sqlc package %s imports %s; generating there would create an import cycle", pkg.PkgPath, outPath)
	}

	l := &linker{
		target:  t,
		m:       m,
		pkg:     pkg.Types,
		namer:   newNamer(t.Go),
		imports: codegen.NewImports(outPath),
		enums:   map[string]bool{},
	}
	for _, s := range m.Schemas {
		for _, e := range s.Enums {
			l.enums[postgres.NormalizeType(schemaOrEmpty(s.Name, m.DefaultSchema), e.Name)] = true
		}
	}
	file := &codegen.File{
		Package:     pkgName,
		SQLCVersion: m.SQLCVersion,
		Imports:     l.imports,
		ModelsPath:  pkg.PkgPath,
	}
	l.imports.Add("github.com/Xwudao/go-sqlcx/sqlcx", "sqlcx")

	keys, err := l.uniqueKeys()
	if err != nil {
		return nil, err
	}
	skip := map[string]bool{}
	for _, s := range opts.SkipTables {
		skip[s] = true
	}
	for _, s := range m.Schemas {
		for _, tb := range s.Tables {
			if skip[tb.Name] || skip[tb.Schema+"."+tb.Name] {
				continue
			}
			to, err := l.table(tb, keys)
			if err != nil {
				return nil, err
			}
			file.Tables = append(file.Tables, to)
		}
	}
	if err := uniqueGoNames(file.Tables); err != nil {
		return nil, err
	}
	views, err := l.views(opts)
	if err != nil {
		return nil, err
	}
	file.Views = views
	names := map[string]string{}
	for _, t := range file.Tables {
		names[t.VarName], names[t.TypeName] = "table "+t.Display, "table "+t.Display
	}
	for _, v := range views {
		for _, n := range []string{v.VarName, v.TypeName} {
			if prev, ok := names[n]; ok {
				return nil, fmt.Errorf("query %s and %s both map to the Go name %s", v.QueryName, prev, n)
			}
			names[n] = "query " + v.QueryName
		}
	}
	return file, nil
}

func schemaOrEmpty(schema, def string) string {
	if schema == def {
		return ""
	}
	return schema
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func loadModels(dir string) (*packages.Package, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("sqlc Go package directory %s: %w (run `sqlc generate` first)", dir, err)
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedFiles | packages.NeedModule | packages.NeedImports | packages.NeedDeps,
		Dir:  dir,
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, fmt.Errorf("load sqlc package in %s: %w", dir, err)
	}
	if len(pkgs) != 1 {
		return nil, fmt.Errorf("load sqlc package in %s: expected one package, got %d", dir, len(pkgs))
	}
	p := pkgs[0]
	var errs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			errs = append(errs, e.Error())
		}
	})
	if len(errs) > 0 {
		return nil, fmt.Errorf("the sqlc package in %s does not type-check:\n  %s", dir, strings.Join(errs, "\n  "))
	}
	if p.Module == nil {
		return nil, fmt.Errorf("the sqlc package in %s is not part of a Go module", dir)
	}
	return p, nil
}

func importPathOf(p *packages.Package, dir string) (string, error) {
	modDir, err := filepath.EvalSymlinks(p.Module.Dir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		abs = filepath.Join(parent, filepath.Base(abs))
	}
	rel, err := filepath.Rel(modDir, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("output directory %s is outside module %s", dir, p.Module.Path)
	}
	if rel == "." {
		return p.Module.Path, nil
	}
	return p.Module.Path + "/" + filepath.ToSlash(rel), nil
}

func importsPath(p *types.Package, path string, seen map[*types.Package]bool) bool {
	if seen[p] {
		return false
	}
	seen[p] = true
	for _, imp := range p.Imports() {
		if imp.Path() == path || importsPath(imp, path, seen) {
			return true
		}
	}
	return false
}

type linker struct {
	target  config.Target
	m       *manifest.Manifest
	pkg     *types.Package
	namer   namer
	imports *codegen.Imports
	enums   map[string]bool
}

func (l *linker) uniqueKeys() (postgres.UniqueKeys, error) {
	files, err := postgres.SchemaFiles(l.target.Schema)
	if err != nil {
		return nil, fmt.Errorf("schema files: %w", err)
	}
	keys, err := postgres.ExtractUniqueKeys(files, l.m.DefaultSchema)
	if err != nil {
		return nil, err
	}
	for name, ks := range l.target.Options.UniqueKeys {
		schema, table, ok := strings.Cut(name, ".")
		if !ok {
			schema, table = l.m.DefaultSchema, name
		}
		k := postgres.TableKey{Schema: schema, Name: table}
		keys[k] = append(keys[k], ks...)
	}
	return keys, nil
}

// lookupStruct finds a named struct type in the sqlc package.
func (l *linker) lookupStruct(name string) (*types.Named, *types.Struct, error) {
	obj := l.pkg.Scope().Lookup(name)
	if obj == nil {
		return nil, nil, fmt.Errorf("type %s.%s not found", l.pkg.Name(), name)
	}
	tn, ok := obj.(*types.TypeName)
	if !ok {
		return nil, nil, fmt.Errorf("%s.%s is not a type", l.pkg.Name(), name)
	}
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return nil, nil, fmt.Errorf("%s.%s is not a named type", l.pkg.Name(), name)
	}
	st, ok := named.Underlying().(*types.Struct)
	if !ok {
		return nil, nil, fmt.Errorf("%s.%s is not a struct", l.pkg.Name(), name)
	}
	return named, st, nil
}

func (l *linker) table(tb manifest.Table, keys postgres.UniqueKeys) (codegen.Table, error) {
	structName := l.namer.tableStruct(tb.Schema, tb.Name, l.m.DefaultSchema)
	display := tb.Name
	if tb.Schema != l.m.DefaultSchema {
		display = tb.Schema + "." + tb.Name
	}
	named, st, err := l.lookupStruct(structName)
	if err != nil {
		return codegen.Table{}, fmt.Errorf("table %s: expected sqlc model %s: %w", display, structName, err)
	}
	if st.NumFields() != len(tb.Columns) {
		return codegen.Table{}, fmt.Errorf("table %s: model %s has %d fields but the table has %d columns", display, structName, st.NumFields(), len(tb.Columns))
	}
	varName := tb.Name
	if tb.Schema != l.m.DefaultSchema {
		varName = tb.Schema + "_" + tb.Name
	}
	varName = l.namer.structName(varName)
	out := codegen.Table{
		TypeName: varName + "Table",
		VarName:  varName,
		Model:    l.imports.TypeString(named),
		Name:     tb.Name,
		Display:  display,
		Comment:  tb.Comment,
	}
	if tb.Schema != l.m.DefaultSchema {
		out.Schema = tb.Schema
	}
	used := reservedTableNames()
	notNull := map[string]bool{}
	for i, c := range tb.Columns {
		f := st.Field(i)
		if err := l.verifyField(st, i, c.Name); err != nil {
			return codegen.Table{}, fmt.Errorf("table %s, column %s: %w", display, c.Name, err)
		}
		col, err := l.column(c, f, used)
		if err != nil {
			return codegen.Table{}, fmt.Errorf("table %s, column %s: %w", display, c.Name, err)
		}
		out.Columns = append(out.Columns, col)
		notNull[c.Name] = c.NotNull
	}
	for _, k := range keys[postgres.TableKey{Schema: tb.Schema, Name: tb.Name}] {
		valid := true
		for _, c := range k {
			if _, ok := notNull[c]; !ok {
				valid = false
			}
		}
		if valid && !containsKey(out.UniqueKeys, k) {
			out.UniqueKeys = append(out.UniqueKeys, slices.Clone(k))
		}
	}
	return out, nil
}

func containsKey(keys [][]string, k []string) bool {
	for _, e := range keys {
		if slices.Equal(e, k) {
			return true
		}
	}
	return false
}

// verifyField checks that struct field i is the field sqlc generated for
// column name: by db tag when present, otherwise by sqlc's field naming.
func (l *linker) verifyField(st *types.Struct, i int, column string) error {
	f := st.Field(i)
	if tag, ok := reflect.StructTag(st.Tag(i)).Lookup("db"); ok {
		if tag != column {
			return fmt.Errorf("field %s has db tag %q, expected %q", f.Name(), tag, column)
		}
		return nil
	}
	if want := l.namer.structName(column); f.Name() != want {
		return fmt.Errorf("field %d of the model is %s, expected %s; enable emit_db_tags in gen.go for exact column matching", i, f.Name(), want)
	}
	return nil
}

func reservedTableNames() map[string]bool {
	m := map[string]bool{}
	for _, n := range []string{"Table", "As", "Seal", "Columns", "Embed", "EmbedNull", "Query", "Update", "Insert", "Delete", "TableName", "TableAlias"} {
		m[n] = true
	}
	return m
}

func reservedViewNames() map[string]bool {
	m := map[string]bool{}
	for _, n := range []string{"QueryView", "Seal", "Columns", "Embed", "EmbedNull", "Bind", "Select", "Query", "Source"} {
		m[n] = true
	}
	return m
}

func fieldName(name string, used map[string]bool) string {
	n := name
	for used[n] {
		n += "Col"
	}
	used[n] = true
	return n
}

func (l *linker) column(c manifest.Column, f *types.Var, used map[string]bool) (codegen.Column, error) {
	T := f.Type()
	V := valueType(T, l.pkg)
	typ := postgres.NormalizeType(c.Type.Schema, c.Type.Name)
	if c.Type.Schema == l.m.DefaultSchema {
		typ = postgres.NormalizeType("", c.Type.Name)
	}
	cat := postgres.Classify(typ, l.enums[typ])
	col := codegen.Column{
		Field:      fieldName(f.Name(), used),
		ModelField: f.Name(),
		Name:       c.Name,
		Nullable:   !c.NotNull,
		PGType:     typ,
		Comment:    c.Comment,
		T:          l.imports.TypeString(T),
	}
	if c.IsArray || c.ArrayDims > 0 {
		col.PGType += "[]"
		if sl, ok := V.Underlying().(*types.Slice); ok {
			col.Kind = "ArrayColumn"
			col.V = l.imports.TypeString(V)
			col.E = l.imports.TypeString(sl.Elem())
			return col, nil
		}
		col.Kind = "Column"
		col.V = l.imports.TypeString(V)
		col.Note = "array column with a non-slice Go type: array operators unavailable"
		return col, nil
	}
	switch cat {
	case postgres.CatText:
		if isKind(V, types.IsString) {
			col.Kind = "TextColumn"
		} else {
			col.Kind = "OrderedColumn"
			col.Note = "text column with a non-string Go type: LIKE operators unavailable"
		}
	case postgres.CatNumber:
		col.Kind = "NumberColumn"
	case postgres.CatBool:
		if b, ok := V.Underlying().(*types.Basic); ok && b.Kind() == types.Bool {
			col.Kind = "BoolColumn"
		} else {
			col.Kind = "Column"
		}
	case postgres.CatJSON:
		col.Kind = "JSONColumn"
	case postgres.CatJSONB:
		col.Kind = "JSONBColumn"
	case postgres.CatOrdered:
		col.Kind = "OrderedColumn"
	case postgres.CatValue:
		col.Kind = "ValueColumn"
	default:
		col.Kind = "Column"
	}
	col.V = l.imports.TypeString(V)
	return col, nil
}

func isKind(t types.Type, info types.BasicInfo) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&info != 0
}

// valueType derives the comparison operand type V from the model field type
// T. V depends only on T, so the NOT NULL and the nullable variant of a
// column type always share V and can be compared with each other.
func valueType(t types.Type, models *types.Package) types.Type {
	if p, ok := t.(*types.Pointer); ok {
		return p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return t
	}
	pkg, name := named.Obj().Pkg().Path(), named.Obj().Name()
	field := func(n string) types.Type {
		st, ok := named.Underlying().(*types.Struct)
		if !ok {
			return nil
		}
		for i := 0; i < st.NumFields(); i++ {
			if st.Field(i).Name() == n {
				return st.Field(i).Type()
			}
		}
		return nil
	}
	switch pkg {
	case pgtypePath:
		fields := map[string]string{
			"Text": "String", "Int2": "Int16", "Int4": "Int32", "Int8": "Int64",
			"Float4": "Float32", "Float8": "Float64", "Bool": "Bool",
			"Date": "Time", "Timestamp": "Time", "Timestamptz": "Time", "UUID": "Bytes",
		}
		if f, ok := fields[name]; ok {
			if ft := field(f); ft != nil {
				return ft
			}
		}
		return t
	case "database/sql":
		if ta := named.TypeArgs(); name == "Null" && ta != nil && ta.Len() == 1 {
			return ta.At(0)
		}
		fields := map[string]string{
			"NullString": "String", "NullInt16": "Int16", "NullInt32": "Int32", "NullInt64": "Int64",
			"NullFloat64": "Float64", "NullBool": "Bool", "NullTime": "Time", "NullByte": "Byte",
		}
		if f, ok := fields[name]; ok {
			if ft := field(f); ft != nil {
				return ft
			}
		}
		return t
	}
	// sqlc's Null<Enum> wrapper: struct { <Enum> Enum; Valid bool } in the
	// models package.
	if named.Obj().Pkg() == models {
		if st, ok := named.Underlying().(*types.Struct); ok && st.NumFields() == 2 &&
			st.Field(1).Name() == "Valid" && types.Identical(st.Field(1).Type(), types.Typ[types.Bool]) {
			if en, ok := st.Field(0).Type().(*types.Named); ok && en.Obj().Pkg() == models && en.Obj().Name() == st.Field(0).Name() {
				return en
			}
		}
	}
	return t
}

func uniqueGoNames(tables []codegen.Table) error {
	seen := map[string]string{}
	for _, t := range tables {
		if prev, ok := seen[t.VarName]; ok {
			return fmt.Errorf("tables %s and %s map to the same Go name %s", prev, t.Display, t.VarName)
		}
		seen[t.VarName] = t.Display
	}
	return nil
}

// views builds dynamic variants of opted-in sqlc queries.
func (l *linker) views(opts manifest.Options) ([]codegen.View, error) {
	all := slices.Contains(opts.Queries, "*")
	wanted := map[string]bool{}
	for _, q := range opts.Queries {
		wanted[q] = true
	}
	found := map[string]bool{}
	var out []codegen.View
	for _, q := range l.m.Queries {
		optIn := wanted[q.Name] || slices.ContainsFunc(q.Comments, func(c string) bool {
			return strings.TrimSpace(strings.TrimPrefix(c, "--")) == "sqlcx:dynamic"
		})
		if !optIn && !all {
			continue
		}
		found[q.Name] = true
		v, err := l.view(q)
		if err != nil {
			if !optIn && all {
				// "*" selects eligible queries only.
				continue
			}
			return nil, fmt.Errorf("query %s (%s): cannot generate a dynamic variant: %w", q.Name, q.Filename, err)
		}
		out = append(out, v)
	}
	for _, q := range opts.Queries {
		if q != "*" && !found[q] {
			return nil, fmt.Errorf("plugin option queries lists %q, but sqlc reported no such query", q)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].QueryName < out[j].QueryName })
	return out, nil
}

func (l *linker) view(q manifest.Query) (codegen.View, error) {
	if q.Cmd != ":many" && q.Cmd != ":one" {
		return codegen.View{}, fmt.Errorf("command %s is not :many or :one", q.Cmd)
	}
	outputs := make([]string, len(q.Columns))
	for i, c := range q.Columns {
		if c.EmbedTable != nil {
			return codegen.View{}, errors.New("sqlc.embed() columns are not supported")
		}
		outputs[i] = c.Name
	}
	for i, p := range q.Params {
		if p.Number != i+1 {
			return codegen.View{}, fmt.Errorf("parameters are not numbered $1..$%d", len(q.Params))
		}
		if p.Column.IsSqlcSlice {
			return codegen.View{}, errors.New("sqlc.slice() parameters are not supported")
		}
	}
	variant, err := postgres.AnalyzeQuery(q.Text, outputs, len(q.Params))
	if err != nil {
		return codegen.View{}, err
	}

	method, err := l.queryMethod(q.Name)
	if err != nil {
		return codegen.View{}, err
	}
	sig := method.Type().(*types.Signature)
	res := sig.Results()
	if res.Len() != 2 {
		return codegen.View{}, fmt.Errorf("method %s does not return (rows, error)", q.Name)
	}
	rowT := res.At(0).Type()
	if q.Cmd == ":many" {
		sl, ok := rowT.(*types.Slice)
		if !ok {
			return codegen.View{}, fmt.Errorf("method %s does not return a slice", q.Name)
		}
		rowT = sl.Elem()
	}
	// emit_result_struct_pointers: []*Row / *Row. The variant still scans
	// into Row values; Select.AllPtrs returns pointers when needed.
	if p, ok := rowT.(*types.Pointer); ok {
		rowT = p.Elem()
	}
	var cols []codegen.Column
	used := reservedViewNames()
	rowNamed, isNamed := rowT.(*types.Named)
	var rowStruct *types.Struct
	if isNamed {
		rowStruct, _ = rowNamed.Underlying().(*types.Struct)
	}
	if rowStruct == nil {
		return codegen.View{}, fmt.Errorf("method %s returns %s; dynamic variants need a row struct (select at least two columns)", q.Name, rowT)
	}
	if rowStruct.NumFields() != len(q.Columns) {
		return codegen.View{}, fmt.Errorf("row type %s has %d fields but the query returns %d columns", rowNamed.Obj().Name(), rowStruct.NumFields(), len(q.Columns))
	}
	for i, c := range q.Columns {
		if tag, ok := reflect.StructTag(rowStruct.Tag(i)).Lookup("db"); ok && tag != c.Name {
			return codegen.View{}, fmt.Errorf("row field %s has db tag %q, expected %q", rowStruct.Field(i).Name(), tag, c.Name)
		}
		col, err := l.column(c, rowStruct.Field(i), used)
		if err != nil {
			return codegen.View{}, fmt.Errorf("column %s: %w", c.Name, err)
		}
		cols = append(cols, col)
	}

	// Parameters: positional (ctx, a, b) or a single Params struct.
	params := sig.Params()
	if params.Len() == 0 || params.At(0).Type().String() != "context.Context" {
		return codegen.View{}, fmt.Errorf("method %s does not take a context.Context first", q.Name)
	}
	v := codegen.View{
		TypeName:  q.Name + "View",
		VarName:   q.Name,
		QueryName: q.Name,
		Row:       l.imports.TypeString(rowNamed),
		Alias:     snake(q.Name),
		Segments:  variant.Segments,
		Params:    variant.Params,
		Order:     variant.Order,
		Columns:   cols,
		Cmd:       q.Cmd,
	}
	n := len(q.Params)
	switch {
	case n == 0:
		if params.Len() != 1 {
			return codegen.View{}, fmt.Errorf("method %s takes %d arguments, expected none besides ctx", q.Name, params.Len()-1)
		}
	case params.Len() == 2 && isParamsStruct(params.At(1).Type(), q.Name+"Params"):
		pt := params.At(1).Type()
		if p, ok := pt.(*types.Pointer); ok {
			pt = p.Elem()
		}
		st := pt.Underlying().(*types.Struct)
		if st.NumFields() != n {
			return codegen.View{}, fmt.Errorf("%s has %d fields but the query has %d parameters", q.Name+"Params", st.NumFields(), n)
		}
		v.ArgsDecl = "arg " + l.imports.TypeString(params.At(1).Type())
		for i := 0; i < n; i++ {
			v.ArgsCall = append(v.ArgsCall, "arg."+st.Field(i).Name())
		}
	case params.Len() == n+1:
		var decl []string
		for i := 1; i <= n; i++ {
			name := params.At(i).Name()
			if name == "" || name == "_" || name == "v" {
				name = fmt.Sprintf("p%d", i)
			}
			decl = append(decl, name+" "+l.imports.TypeString(params.At(i).Type()))
			v.ArgsCall = append(v.ArgsCall, name)
		}
		v.ArgsDecl = strings.Join(decl, ", ")
	default:
		return codegen.View{}, fmt.Errorf("method %s has %d arguments for %d query parameters", q.Name, params.Len()-1, n)
	}
	return v, nil
}

func isParamsStruct(t types.Type, name string) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Name() != name {
		return false
	}
	_, ok = n.Underlying().(*types.Struct)
	return ok
}

func (l *linker) queryMethod(name string) (*types.Func, error) {
	obj := l.pkg.Scope().Lookup("Queries")
	if obj == nil {
		return nil, errors.New("type Queries not found in the sqlc package")
	}
	ms := types.NewMethodSet(types.NewPointer(obj.Type()))
	sel := ms.Lookup(l.pkg, name)
	if sel == nil {
		return nil, fmt.Errorf("method Queries.%s not found (emit_methods_with_db_argument is not supported)", name)
	}
	return sel.Obj().(*types.Func), nil
}

func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				prev := s[i-1]
				next := byte(0)
				if i+1 < len(s) {
					next = s[i+1]
				}
				if prev >= 'a' && prev <= 'z' || prev >= '0' && prev <= '9' || (prev >= 'A' && prev <= 'Z' && next >= 'a' && next <= 'z') {
					b.WriteByte('_')
				}
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
