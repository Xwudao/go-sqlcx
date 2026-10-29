package ast

import (
	"errors"
	"strconv"
)

// CTE is one WITH entry.
type CTE struct {
	Name         string
	Columns      []string
	Materialized int // 0 default, 1 MATERIALIZED, 2 NOT MATERIALIZED
	Body         Node
	Err          error
}

// With is a WITH clause.
type With struct {
	Recursive bool
	CTEs      []*CTE
}

// Render writes WITH ... followed by a space.
func (wi *With) Render(w *Writer) {
	if wi == nil || len(wi.CTEs) == 0 {
		return
	}
	w.S("WITH ")
	if wi.Recursive {
		w.S("RECURSIVE ")
	}
	seen := make(map[string]bool, len(wi.CTEs))
	for i, c := range wi.CTEs {
		if c.Err != nil {
			w.Fail(c.Err)
			return
		}
		if seen[c.Name] {
			w.Fail(errors.New("sqlcx: WITH query name " + strconv.Quote(c.Name) + " specified more than once"))
			return
		}
		seen[c.Name] = true
		if i > 0 {
			w.S(", ")
		}
		w.Ident(c.Name)
		if len(c.Columns) > 0 {
			w.S(" (")
			for j, col := range c.Columns {
				if j > 0 {
					w.S(", ")
				}
				w.Ident(col)
			}
			w.B(')')
		}
		w.S(" AS ")
		switch c.Materialized {
		case 1:
			w.S("MATERIALIZED ")
		case 2:
			w.S("NOT MATERIALIZED ")
		}
		w.B('(')
		w.Node(c.Body)
		w.B(')')
	}
	w.B(' ')
}

// DerivedSource is a subquery in FROM: [LATERAL] (stmt) AS alias.
type DerivedSource struct {
	Sub     Node
	Alias   *NamedRel
	Lateral bool
}

// Render writes the source.
func (d *DerivedSource) Render(w *Writer) {
	if d.Lateral {
		w.S("LATERAL ")
	}
	w.B('(')
	w.Node(d.Sub)
	w.S(") AS ")
	d.Alias.RenderQualifier(w)
}

// Prec implements Node.
func (*DerivedSource) Prec() int { return PrecAtom }

// CTESource references a CTE by name: "name" [AS "alias"].
type CTESource struct {
	Name  string
	Alias *NamedRel // nil when not aliased
}

// Render writes the source.
func (c *CTESource) Render(w *Writer) {
	w.Ident(c.Name)
	if c.Alias != nil {
		w.S(" AS ")
		c.Alias.RenderQualifier(w)
	}
}

// Prec implements Node.
func (*CTESource) Prec() int { return PrecAtom }

// Template is a pre-tokenised, trusted SQL statement produced by the code
// generator from a sqlc query. Segments are interleaved with references to
// the statement's own parameters ($1..$n in the original text), which are
// renumbered into the enclosing statement.
type Template struct {
	Segments []string // len(Refs)+1 entries
	Refs     []int    // 1-based original parameter numbers
	Args     []any    // original argument values, Args[i] is $i+1
}

// Render writes the template, renumbering parameters.
func (t *Template) Render(w *Writer) {
	if len(t.Segments) != len(t.Refs)+1 {
		w.Fail(errors.New("sqlcx: internal error: malformed query template"))
		return
	}
	assigned := make([]int, len(t.Args))
	for i, seg := range t.Segments {
		w.S(seg)
		if i == len(t.Refs) {
			break
		}
		ref := t.Refs[i]
		if ref < 1 || ref > len(t.Args) {
			w.Fail(errors.New("sqlcx: internal error: query template references parameter $" + strconv.Itoa(ref) + " without a value"))
			return
		}
		if assigned[ref-1] == 0 {
			w.Param(t.Args[ref-1])
			assigned[ref-1] = w.NumArgs()
		} else {
			w.ParamRef(assigned[ref-1])
		}
	}
}

// Prec implements Node.
func (*Template) Prec() int { return PrecAtom }

// Join kinds.
const (
	JoinInner = iota
	JoinLeft
	JoinRight
	JoinFull
	JoinCross
)

var joinKeywords = [...]string{" JOIN ", " LEFT JOIN ", " RIGHT JOIN ", " FULL JOIN ", " CROSS JOIN "}

// Join is one JOIN clause.
type Join struct {
	Kind   int
	Source Node
	On     Node // nil only for CROSS JOIN
}

// SelectItem is one output column.
type SelectItem struct {
	X     Node
	Alias string
}

// Lock is a row-locking clause.
type Lock struct {
	Strength string // "UPDATE", "NO KEY UPDATE", "SHARE", "KEY SHARE"
	Of       []Qualifier
	Wait     string // "", "NOWAIT", "SKIP LOCKED"
}

// Select is a SELECT statement.
type Select struct {
	With       *With
	Distinct   bool
	DistinctOn []Node
	Items      []SelectItem
	From       []Node
	Joins      []Join
	Where      Node
	GroupBy    []Node
	Having     Node
	OrderBy    []OrderItem
	Limit      Node
	Offset     Node
	Locks      []Lock
}

// Render writes the statement.
func (s *Select) Render(w *Writer) {
	s.With.Render(w)
	w.S("SELECT ")
	if s.Distinct {
		w.S("DISTINCT ")
	} else if len(s.DistinctOn) > 0 {
		w.S("DISTINCT ON (")
		w.List(s.DistinctOn)
		w.S(") ")
	}
	if len(s.Items) == 0 {
		w.Fail(errors.New("sqlcx: SELECT without output columns"))
		return
	}
	RenderItems(w, s.Items)
	if len(s.From) > 0 {
		w.S(" FROM ")
		w.List(s.From)
	} else if len(s.Joins) > 0 {
		w.Fail(errors.New("sqlcx: JOIN without FROM"))
		return
	}
	RenderJoins(w, s.Joins)
	if s.Where != nil {
		w.S(" WHERE ")
		w.Node(s.Where)
	}
	if len(s.GroupBy) > 0 {
		w.S(" GROUP BY ")
		w.List(s.GroupBy)
	}
	if s.Having != nil {
		w.S(" HAVING ")
		w.Node(s.Having)
	}
	RenderTail(w, s.OrderBy, s.Limit, s.Offset)
	for _, l := range s.Locks {
		w.S(" FOR ")
		w.S(l.Strength)
		if len(l.Of) > 0 {
			w.S(" OF ")
			for i, q := range l.Of {
				if i > 0 {
					w.S(", ")
				}
				q.RenderQualifier(w)
			}
		}
		if l.Wait != "" {
			w.B(' ')
			w.S(l.Wait)
		}
	}
}

// Prec implements Node.
func (*Select) Prec() int { return PrecAtom }

// RenderItems writes a select list.
func RenderItems(w *Writer, items []SelectItem) {
	for i, it := range items {
		if i > 0 {
			w.S(", ")
		}
		w.Node(it.X)
		if it.Alias != "" {
			w.S(" AS ")
			w.Ident(it.Alias)
		}
	}
}

// RenderJoins writes JOIN clauses.
func RenderJoins(w *Writer, joins []Join) {
	for _, j := range joins {
		w.S(joinKeywords[j.Kind])
		w.Node(j.Source)
		if j.Kind == JoinCross {
			continue
		}
		if j.On == nil {
			w.Fail(errors.New("sqlcx: JOIN without ON condition (use CrossJoin for a cartesian product)"))
			return
		}
		w.S(" ON ")
		w.Node(j.On)
	}
}

// RenderTail writes ORDER BY / LIMIT / OFFSET.
func RenderTail(w *Writer, order []OrderItem, limit, offset Node) {
	if len(order) > 0 {
		w.S(" ORDER BY ")
		RenderOrder(w, order)
	}
	if limit != nil {
		w.S(" LIMIT ")
		w.Node(limit)
	}
	if offset != nil {
		w.S(" OFFSET ")
		w.Node(offset)
	}
}

// Compound is a set operation between two queries.
type Compound struct {
	With    *With
	Op      string // "UNION", "UNION ALL", "INTERSECT", "INTERSECT ALL", "EXCEPT", "EXCEPT ALL"
	L, R    Node
	OrderBy []OrderItem
	Limit   Node
	Offset  Node
}

// Render writes the compound statement. Operands are parenthesised so that
// their own ORDER BY / LIMIT clauses keep their meaning.
func (c *Compound) Render(w *Writer) {
	c.With.Render(w)
	w.B('(')
	w.Node(c.L)
	w.S(") ")
	w.S(c.Op)
	w.S(" (")
	w.Node(c.R)
	w.B(')')
	RenderTail(w, c.OrderBy, c.Limit, c.Offset)
}

// Prec implements Node.
func (*Compound) Prec() int { return PrecAtom }

// Assignment is one SET target = value.
type Assignment struct {
	Column string
	Value  Node
}

// RenderAssignments writes SET items.
func RenderAssignments(w *Writer, as []Assignment) {
	for i, a := range as {
		if i > 0 {
			w.S(", ")
		}
		w.Ident(a.Column)
		w.S(" = ")
		w.Node(a.Value)
	}
}

// OnConflict is an ON CONFLICT clause.
type OnConflict struct {
	Columns     []string
	TargetWhere Node
	Constraint  string
	DoNothing   bool
	Set         []Assignment
	Where       Node
}

// Insert is an INSERT statement.
type Insert struct {
	With       *With
	Table      *Rel
	Columns    []string
	Rows       [][]Node
	Query      Node
	OnConflict *OnConflict
	Returning  []SelectItem
}

// Render writes the statement.
func (in *Insert) Render(w *Writer) {
	in.With.Render(w)
	w.S("INSERT INTO ")
	in.Table.RenderSource(w)
	if len(in.Columns) > 0 {
		w.S(" (")
		for i, c := range in.Columns {
			if i > 0 {
				w.S(", ")
			}
			w.Ident(c)
		}
		w.B(')')
	}
	switch {
	case in.Query != nil:
		w.B(' ')
		w.Node(in.Query)
	case len(in.Rows) == 0:
		if len(in.Columns) > 0 {
			w.Fail(errors.New("sqlcx: INSERT has columns but no rows"))
			return
		}
		w.S(" DEFAULT VALUES")
	default:
		w.S(" VALUES ")
		for i, row := range in.Rows {
			if len(row) != len(in.Columns) {
				w.Fail(errors.New("sqlcx: INSERT row " + strconv.Itoa(i) + " has " + strconv.Itoa(len(row)) + " values for " + strconv.Itoa(len(in.Columns)) + " columns"))
				return
			}
			if i > 0 {
				w.S(", ")
			}
			w.B('(')
			w.List(row)
			w.B(')')
		}
	}
	if oc := in.OnConflict; oc != nil {
		w.S(" ON CONFLICT")
		switch {
		case oc.Constraint != "":
			w.S(" ON CONSTRAINT ")
			w.Ident(oc.Constraint)
		case len(oc.Columns) > 0:
			w.S(" (")
			for i, c := range oc.Columns {
				if i > 0 {
					w.S(", ")
				}
				w.Ident(c)
			}
			w.B(')')
			if oc.TargetWhere != nil {
				w.S(" WHERE ")
				w.Node(oc.TargetWhere)
			}
		}
		if oc.DoNothing {
			w.S(" DO NOTHING")
		} else {
			if len(oc.Columns) == 0 && oc.Constraint == "" {
				w.Fail(errors.New("sqlcx: ON CONFLICT DO UPDATE requires a conflict target"))
				return
			}
			if len(oc.Set) == 0 {
				w.Fail(errors.New("sqlcx: ON CONFLICT DO UPDATE without assignments"))
				return
			}
			w.S(" DO UPDATE SET ")
			RenderAssignments(w, oc.Set)
			if oc.Where != nil {
				w.S(" WHERE ")
				w.Node(oc.Where)
			}
		}
	}
	renderReturning(w, in.Returning)
}

// Prec implements Node.
func (*Insert) Prec() int { return PrecAtom }

func renderReturning(w *Writer, items []SelectItem) {
	if len(items) > 0 {
		w.S(" RETURNING ")
		RenderItems(w, items)
	}
}

// Update is an UPDATE statement.
type Update struct {
	With      *With
	Table     *Rel
	Set       []Assignment
	From      []Node
	Joins     []Join
	Where     Node
	Returning []SelectItem
}

// Render writes the statement.
func (u *Update) Render(w *Writer) {
	u.With.Render(w)
	w.S("UPDATE ")
	u.Table.RenderSource(w)
	if len(u.Set) == 0 {
		w.Fail(errors.New("sqlcx: UPDATE without SET assignments"))
		return
	}
	w.S(" SET ")
	RenderAssignments(w, u.Set)
	if len(u.From) > 0 {
		w.S(" FROM ")
		w.List(u.From)
	}
	RenderJoins(w, u.Joins)
	if u.Where != nil {
		w.S(" WHERE ")
		w.Node(u.Where)
	}
	renderReturning(w, u.Returning)
}

// Prec implements Node.
func (*Update) Prec() int { return PrecAtom }

// Delete is a DELETE statement.
type Delete struct {
	With      *With
	Table     *Rel
	Using     []Node
	Joins     []Join
	Where     Node
	Returning []SelectItem
}

// Render writes the statement.
func (d *Delete) Render(w *Writer) {
	d.With.Render(w)
	w.S("DELETE FROM ")
	d.Table.RenderSource(w)
	if len(d.Using) > 0 {
		w.S(" USING ")
		w.List(d.Using)
	}
	RenderJoins(w, d.Joins)
	if d.Where != nil {
		w.S(" WHERE ")
		w.Node(d.Where)
	}
	renderReturning(w, d.Returning)
}

// Prec implements Node.
func (*Delete) Prec() int { return PrecAtom }
