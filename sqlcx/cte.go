package sqlcx

import (
	"context"
	"errors"
	"slices"

	"github.com/Xwudao/go-sqlcx/internal/ast"
)

// CTE is a common table expression (WITH name AS (...)). It is a Source
// referencing the CTE by name; add it to a statement with With.
type CTE struct {
	def *ast.CTE
	rel *relation
	rec bool
}

// NewCTE defines WITH name AS (body). name is validated.
func NewCTE(name string, body Subquery) *CTE {
	c := &CTE{def: &ast.CTE{Name: name, Err: ast.ValidateAlias(name)}}
	if body != nil {
		c.def.Body = body.stmtNode()
	}
	c.rel = &relation{q: &ast.NamedRel{Name: name, Err: c.def.Err}, display: name, notNull: map[string]bool{}}
	c.rel.source = &ast.CTESource{Name: name}
	return c
}

// RecursiveCTE declares a recursive CTE with explicit column names. Define
// its body with As once the self-referencing part has been built from Ref:
//
//	tree := sqlcx.RecursiveCTE("tree", "id", "parent_id", "depth")
//	id := sqlcx.Ref[sqlcx.Number[int64]](tree, "id")
//	tree = tree.As(sqlcx.UnionAll(base, step))
func RecursiveCTE(name string, columns ...string) *CTE {
	c := NewCTE(name, nil)
	c.rec = true
	c.def.Columns = slices.Clone(columns)
	if c.def.Err == nil && len(columns) == 0 {
		c.def.Err = errors.New("sqlcx: recursive CTE " + name + " requires column names")
	}
	for _, col := range columns {
		if err := ast.ValidateAlias(col); err != nil && c.def.Err == nil {
			c.def.Err = err
		}
	}
	return c
}

// As returns a copy of c with the given body. References created from c
// remain valid because they refer to the CTE by name.
func (c *CTE) As(body Subquery) *CTE {
	cp := *c
	def := *c.def
	def.Body = body.stmtNode()
	cp.def = &def
	return &cp
}

// Columns returns a copy naming the CTE's output columns.
func (c *CTE) Columns(names ...string) *CTE {
	cp := *c
	def := *c.def
	def.Columns = slices.Clone(names)
	for _, n := range names {
		if err := ast.ValidateAlias(n); err != nil && def.Err == nil {
			def.Err = err
		}
	}
	cp.def = &def
	return &cp
}

// Materialized returns a copy marked AS MATERIALIZED.
func (c *CTE) Materialized() *CTE { return c.materialized(1) }

// NotMaterialized returns a copy marked AS NOT MATERIALIZED.
func (c *CTE) NotMaterialized() *CTE { return c.materialized(2) }

func (c *CTE) materialized(m int) *CTE {
	cp := *c
	def := *c.def
	def.Materialized = m
	cp.def = &def
	return &cp
}

func (c *CTE) sourceNode() ast.Node { return c.rel.source }
func (c *CTE) sourceRel() *relation { return c.rel }

// Alias returns a Source referring to the CTE under another name, for
// joining a CTE with itself.
func (c *CTE) Alias(alias string) Source {
	q := &ast.NamedRel{Name: alias, Err: ast.ValidateAlias(alias)}
	rel := &relation{q: q, display: alias, notNull: map[string]bool{}}
	rel.source = &ast.CTESource{Name: c.def.Name, Alias: q}
	return derived{rel}
}

func addCTEs(w *ast.With, ctes []*CTE) *ast.With {
	out := &ast.With{}
	if w != nil {
		*out = *w
		out.CTEs = slices.Clip(out.CTEs)
	}
	for _, c := range ctes {
		d := c.def
		if d.Body == nil && d.Err == nil {
			cp := *d
			cp.Err = errors.New("sqlcx: CTE " + d.Name + " has no body")
			d = &cp
		}
		out.CTEs = append(out.CTEs, d)
		if c.rec {
			out.Recursive = true
		}
	}
	return out
}

// derived is a Source over a relation with a fixed FROM node.
type derived struct{ rel *relation }

func (d derived) sourceNode() ast.Node { return d.rel.source }
func (d derived) sourceRel() *relation { return d.rel }

// Derived uses a subquery as a FROM item: (sub) AS alias.
func Derived(sub Subquery, alias string) Source {
	q := &ast.NamedRel{Name: alias, Err: ast.ValidateAlias(alias)}
	rel := &relation{q: q, display: alias, notNull: map[string]bool{}}
	rel.source = &ast.DerivedSource{Sub: sub.stmtNode(), Alias: q}
	return derived{rel}
}

// Lateral uses a subquery as a LATERAL FROM item, which may reference
// columns of preceding FROM items.
func Lateral(sub Subquery, alias string) Source {
	q := &ast.NamedRel{Name: alias, Err: ast.ValidateAlias(alias)}
	rel := &relation{q: q, display: alias, notNull: map[string]bool{}}
	rel.source = &ast.DerivedSource{Sub: sub.stmtNode(), Alias: q, Lateral: true}
	return derived{rel}
}

// Ref references column of a derived table, CTE or other source with the
// expression category E:
//
//	total := sqlcx.Ref[sqlcx.Number[int64]](stats, "total")
//
// The column name is validated; it must be a developer-written constant.
func Ref[E category[E]](src Source, column string) E {
	var zero E
	if err := ast.ValidateAlias(column); err != nil {
		return zero.withNode(ast.ErrNode{Err: err})
	}
	return zero.withNode(&ast.Column{Q: src.sourceRel().q, Name: column})
}

// Compound is a set operation (UNION, INTERSECT, EXCEPT) of two queries
// with the same row type.
type Compound[R any] struct {
	st   *ast.Compound
	proj []Projection[R]
	err  error
}

func compound[R any](op string, a, b Select[R]) Compound[R] {
	c := Compound[R]{st: &ast.Compound{Op: op, L: a.render(), R: b.render()}, proj: a.proj}
	if len(selectItemsOf(a.proj)) != len(selectItemsOf(b.proj)) {
		c.err = errors.New("sqlcx: " + op + " operands have different numbers of columns")
	}
	return c
}

// Union is a UNION b (duplicates removed).
func Union[R any](a, b Select[R]) Compound[R] { return compound("UNION", a, b) }

// UnionAll is a UNION ALL b.
func UnionAll[R any](a, b Select[R]) Compound[R] { return compound("UNION ALL", a, b) }

// Intersect is a INTERSECT b.
func Intersect[R any](a, b Select[R]) Compound[R] { return compound("INTERSECT", a, b) }

// Except is a EXCEPT b.
func Except[R any](a, b Select[R]) Compound[R] { return compound("EXCEPT", a, b) }

func (c Compound[R]) clone() Compound[R] {
	st := *c.st
	c.st = &st
	return c
}

// OrderBy appends ORDER BY terms applied to the combined result. Terms
// must reference output columns (use Ref against a Derived wrapper, or
// column positions via Raw).
func (c Compound[R]) OrderBy(terms ...OrderTerm) Compound[R] {
	c = c.clone()
	c.st.OrderBy = append(slices.Clip(c.st.OrderBy), orderItems(terms)...)
	return c
}

// Limit sets LIMIT n.
func (c Compound[R]) Limit(n int64) Compound[R] {
	c = c.clone()
	c.st.Limit = param(n)
	return c
}

// Offset sets OFFSET n.
func (c Compound[R]) Offset(n int64) Compound[R] {
	c = c.clone()
	c.st.Offset = param(n)
	return c
}

// With adds common table expressions.
func (c Compound[R]) With(ctes ...*CTE) Compound[R] {
	c = c.clone()
	c.st.With = addCTEs(c.st.With, ctes)
	return c
}

func (c Compound[R]) stmtNode() ast.Node {
	if c.err != nil {
		return ast.ErrNode{Err: c.err}
	}
	return c.st
}

func (c Compound[R]) outputColumns() int { return len(selectItemsOf(c.proj)) }

// Build renders the statement.
func (c Compound[R]) Build() (string, []any, error) { return ast.Render(c.stmtNode()) }

// All executes the statement and scans every row.
func (c Compound[R]) All(ctx context.Context, db DB) ([]R, error) {
	sql, args, err := c.Build()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, c.proj)
}
