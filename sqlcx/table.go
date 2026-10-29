package sqlcx

import (
	"fmt"
	"slices"

	"github.com/Xwudao/go-sqlcx/internal/ast"
)

// NotNull marks a column declared NOT NULL.
type NotNull struct{}

// Nullable marks a column that accepts NULL.
type Nullable struct{}

// relation is the immutable identity of a FROM item: a base table, an
// aliased table, a derived table, a CTE reference or a sqlc query source.
// Column references and order terms point at it, which lets the builder
// check order stability per source.
type relation struct {
	q       ast.Qualifier
	source  ast.Node // FROM item; nil for sqlc query sources (bound per call)
	display string
	keys    [][]string
	notNull map[string]bool
}

func (r *relation) stableKeys() [][]string {
	var out [][]string
	for _, k := range r.keys {
		ok := len(k) > 0
		for _, c := range k {
			if !r.notNull[c] {
				ok = false
			}
		}
		if ok {
			out = append(out, k)
		}
	}
	return out
}

// Source is anything usable in FROM / JOIN: generated tables, aliases,
// derived tables, CTEs and bound sqlc query sources.
type Source interface {
	sourceNode() ast.Node
	sourceRel() *relation
}

// colInfo describes one column of a relation.
type colInfo struct {
	owner   *relation
	name    string
	notNull bool
	node    *ast.Column // immutable, shared by every reference
}

func (c *colInfo) ref() ast.Node { return c.node }
func (c *colInfo) key() *columnKey {
	return &columnKey{owner: c.owner, name: c.name, notNull: c.notNull}
}
func (c *colInfo) bare() *ast.Column { return &ast.Column{Name: c.name} }

// ColumnOf is a column of a relation whose rows scan into model type M.
type ColumnOf[M any] interface {
	Expr
	column() *colInfo
	target(m *M) any
	fieldValue(m *M) any
	nullHolder() nullHolder[M]
}

// Assignable is a column of M accepting non-NULL values of type V.
type Assignable[M, V any] interface {
	ColumnOf[M]
	assignTo(*M, V)
}

// FieldColumn is a column of M whose model field has Go type T.
type FieldColumn[M, T any] interface {
	ColumnOf[M]
	fieldType(*M, T)
}

// NullableColumn is a nullable column of M. Passing a NOT NULL column is a
// compile-time error.
type NullableColumn[M any] interface {
	ColumnOf[M]
	nullability(Nullable)
}

// nullHolder scans a column into an intermediate pointer so that a whole
// model can be NULL (LEFT JOIN).
type nullHolder[M any] struct {
	dst    any
	reset  func()
	isNull func() bool
	commit func(m *M)
}

// model is the shared part of Table and QueryView.
type model[M any] struct {
	rel      *relation
	cols     []ColumnOf[M]
	scan     func(*M) []any
	sealed   bool
	allItems []ast.SelectItem // every column, built by Seal; read-only
}

// Owner is implemented by *Table[M] and *QueryView[M]; generated code passes
// it to the column constructors.
type Owner[M any] interface {
	owner() *model[M]
}

func (m *model[M]) owner() *model[M] { return m }

func (m *model[M]) register(name string, notNull bool) *colInfo {
	if m.sealed {
		panic("sqlcx: column " + name + " registered after the table was sealed")
	}
	if notNull {
		m.rel.notNull[name] = true
	}
	return &colInfo{owner: m.rel, name: name, notNull: notNull, node: &ast.Column{Q: m.rel.q, Name: name}}
}

// Seal finishes construction. It is called by generated code after every
// column has been created and verifies that the column list and the
// generated scanner agree.
func (m *model[M]) Seal() {
	var probe M
	if n := len(m.scan(&probe)); n != len(m.cols) {
		panic(fmt.Sprintf("sqlcx: %s has %d columns but its scanner has %d targets", m.rel.display, len(m.cols), n))
	}
	m.allItems = m.buildItems()
	m.sealed = true
}

func (m *model[M]) items() []ast.SelectItem {
	if m.allItems != nil {
		return m.allItems
	}
	return m.buildItems()
}

func (m *model[M]) buildItems() []ast.SelectItem {
	items := make([]ast.SelectItem, len(m.cols))
	for i, c := range m.cols {
		items[i] = ast.SelectItem{X: c.column().ref()}
	}
	return items
}

// Columns returns the columns in model field order.
func (m *model[M]) Columns() []ColumnOf[M] { return slices.Clone(m.cols) }

// Embed projects every column of the relation into the *M returned by ptr:
//
//	type Row struct{ User db.User; Order db.Order }
//	sqlcx.SelectFields(u.Embed(func(r *Row) *db.User { return &r.User }), ...)
func (m *model[M]) Embed[R any](ptr func(*R) *M) FieldSet[R] {
	scan := m.scan
	return FieldSet[R]{
		items: m.items(),
		bind: func(r *R) rowBinding {
			return rowBinding{targets: scan(ptr(r))}
		},
	}
}

// EmbedNull projects every column of the relation into a *M that is nil
// when all columns are NULL, as for the optional side of an outer join. A
// NULL in a NOT NULL column of an otherwise present row is an error.
func (m *model[M]) EmbedNull[R any](ptr func(*R) **M) FieldSet[R] {
	cols := m.cols
	display := m.rel.display
	return FieldSet[R]{
		items: m.items(),
		bind: func(r *R) rowBinding {
			holders := make([]nullHolder[M], len(cols))
			targets := make([]any, len(cols))
			for i, c := range cols {
				holders[i] = c.nullHolder()
				targets[i] = holders[i].dst
			}
			return rowBinding{
				targets: targets,
				reset: func() {
					for _, h := range holders {
						h.reset()
					}
				},
				post: func() error {
					dst := ptr(r)
					allNull := true
					for _, h := range holders {
						if !h.isNull() {
							allNull = false
							break
						}
					}
					if allNull {
						*dst = nil
						return nil
					}
					v := new(M)
					for i, h := range holders {
						if h.isNull() {
							if cols[i].column().notNull {
								return fmt.Errorf("sqlcx: %s.%s is NULL in a partially present row", display, cols[i].column().name)
							}
							continue
						}
						h.commit(v)
					}
					*dst = v
					return nil
				},
			}
		},
	}
}

// TableDef configures a generated table descriptor.
type TableDef[M any] struct {
	Schema     string // empty for the default schema
	Name       string
	Alias      string
	UniqueKeys [][]string
	Scan       func(*M) []any
}

// Table describes a database table whose rows scan into the sqlc model M.
// Generated code embeds it in a struct with one field per column.
type Table[M any] struct {
	*model[M]
	ref *ast.Rel
}

// NewTable creates a table descriptor. It is called by generated code.
func NewTable[M any](def TableDef[M]) *Table[M] {
	r := &ast.Rel{Schema: def.Schema, Name: def.Name, Alias: def.Alias}
	if def.Alias != "" {
		r.Err = ast.ValidateAlias(def.Alias)
	}
	display := def.Name
	if def.Schema != "" {
		display = def.Schema + "." + def.Name
	}
	if def.Alias != "" {
		display += " AS " + def.Alias
	}
	rel := &relation{q: r, source: r, display: display, keys: def.UniqueKeys, notNull: map[string]bool{}}
	return &Table[M]{model: &model[M]{rel: rel, scan: def.Scan}, ref: r}
}

func (t *Table[M]) sourceNode() ast.Node { return t.ref }
func (t *Table[M]) sourceRel() *relation { return t.model.rel }

// TableName returns the (schema-qualified) table name.
func (t *Table[M]) TableName() string {
	if t.ref.Schema != "" {
		return t.ref.Schema + "." + t.ref.Name
	}
	return t.ref.Name
}

// TableAlias returns the alias, or "" when unaliased.
func (t *Table[M]) TableAlias() string { return t.ref.Alias }

// Query starts a SELECT of every column, scanning into M.
func (t *Table[M]) Query() Select[M] {
	return Select[M]{
		st:      &ast.Select{From: []ast.Node{t.ref}},
		proj:    []Projection[M]{t.self()},
		sources: []*relation{t.model.rel},
	}
}

// self projects every column into the row itself.
func (m *model[M]) self() FieldSet[M] {
	scan := m.scan
	return FieldSet[M]{items: m.items(), bind: func(r *M) rowBinding { return rowBinding{targets: scan(r)} }}
}

// Update starts an UPDATE of the table.
func (t *Table[M]) Update() Update[M] { return Update[M]{t: t, st: &ast.Update{Table: t.ref}} }

// Insert starts an INSERT into the table.
func (t *Table[M]) Insert() Insert[M] { return Insert[M]{t: t, st: &ast.Insert{Table: t.ref}} }

// Delete starts a DELETE from the table.
func (t *Table[M]) Delete() Delete[M] { return Delete[M]{t: t, st: &ast.Delete{Table: t.ref}} }

// QueryView describes the result set of a sqlc query used as a dynamic
// source: SELECT ... FROM (<sqlc query>) AS alias. Rows scan into the
// sqlc row type M.
type QueryView[M any] struct {
	*model[M]
	tmpl         QueryTemplate
	defaultOrder []QueryOrder
}

// QueryTemplate is the pre-tokenised SQL of a sqlc query. It is produced by
// the code generator from the sqlc query text; it is trusted SQL.
type QueryTemplate struct {
	Segments []string
	Params   []int
}

// QueryOrder is an ORDER BY term lifted from the original sqlc query.
type QueryOrder struct {
	Column string
	Desc   bool
	Nulls  int // 0 default, 1 first, 2 last
}

// NewQueryView creates a query source descriptor. It is called by generated
// code.
func NewQueryView[M any](alias string, tmpl QueryTemplate, order []QueryOrder, scan func(*M) []any) *QueryView[M] {
	q := &ast.NamedRel{Name: alias, Err: ast.ValidateAlias(alias)}
	if len(tmpl.Segments) != len(tmpl.Params)+1 {
		q.Err = fmt.Errorf("sqlcx: malformed query template for %s", alias)
	}
	rel := &relation{q: q, display: alias, notNull: map[string]bool{}}
	return &QueryView[M]{model: &model[M]{rel: rel, scan: scan}, tmpl: tmpl, defaultOrder: order}
}

// BoundView is a QueryView with its parameters bound; it is a Source.
type BoundView struct {
	rel  *relation
	node ast.Node
}

func (b BoundView) sourceNode() ast.Node { return b.node }
func (b BoundView) sourceRel() *relation { return b.rel }

// Bind binds the sqlc query's parameters ($1..$n, in order). Generated code
// calls it with the fields of the sqlc Params struct.
func (v *QueryView[M]) Bind(args ...any) BoundView {
	t := &ast.Template{Segments: v.tmpl.Segments, Refs: v.tmpl.Params, Args: args}
	return BoundView{rel: v.rel, node: &ast.DerivedSource{Sub: t, Alias: v.rel.q.(*ast.NamedRel)}}
}

// Select binds the parameters and starts a SELECT of every output column,
// ordered like the original query until OrderBy is called.
func (v *QueryView[M]) Select(args ...any) Select[M] {
	b := v.Bind(args...)
	st := &ast.Select{From: []ast.Node{b.node}}
	for _, o := range v.defaultOrder {
		st.OrderBy = append(st.OrderBy, ast.OrderItem{X: &ast.Column{Q: v.rel.q, Name: o.Column}, Desc: o.Desc, Nulls: o.Nulls})
	}
	return Select[M]{
		st:           st,
		proj:         []Projection[M]{v.self()},
		sources:      []*relation{v.rel},
		defaultOrder: len(st.OrderBy) > 0,
	}
}
