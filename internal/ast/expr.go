package ast

import (
	"errors"
	"fmt"
)

// Qualifier renders the prefix of a qualified column reference.
type Qualifier interface {
	RenderQualifier(w *Writer)
}

// Rel is a reference to a base table, optionally aliased.
type Rel struct {
	Schema string // empty for tables in the default schema
	Name   string
	Alias  string
	Err    error // set when the alias failed validation
}

// RenderQualifier writes the alias, or the (schema-qualified) table name.
func (r *Rel) RenderQualifier(w *Writer) {
	if r.Err != nil {
		w.Fail(r.Err)
		return
	}
	if r.Alias != "" {
		w.Ident(r.Alias)
		return
	}
	r.RenderName(w)
}

// RenderName writes the (schema-qualified) table name without alias.
func (r *Rel) RenderName(w *Writer) {
	if r.Schema != "" {
		w.Ident(r.Schema)
		w.B('.')
	}
	w.Ident(r.Name)
}

// RenderSource writes the table name followed by AS alias when aliased.
func (r *Rel) RenderSource(w *Writer) {
	if r.Err != nil {
		w.Fail(r.Err)
		return
	}
	r.RenderName(w)
	if r.Alias != "" {
		w.S(" AS ")
		w.Ident(r.Alias)
	}
}

// Render implements Node so that a Rel can be used as a FROM item.
func (r *Rel) Render(w *Writer) { r.RenderSource(w) }

// Prec implements Node.
func (r *Rel) Prec() int { return PrecAtom }

// NamedRel qualifies columns of derived tables, CTEs and query sources.
type NamedRel struct {
	Name string
	Err  error
}

// RenderQualifier writes the quoted name.
func (r *NamedRel) RenderQualifier(w *Writer) {
	if r.Err != nil {
		w.Fail(r.Err)
		return
	}
	w.Ident(r.Name)
}

// Column is a qualified column reference.
type Column struct {
	Q    Qualifier
	Name string
}

// Render writes "qualifier"."name".
func (c *Column) Render(w *Writer) {
	if c.Q != nil {
		c.Q.RenderQualifier(w)
		w.B('.')
	}
	w.Ident(c.Name)
}

// Prec implements Node.
func (c *Column) Prec() int { return PrecAtom }

// Param is a bound value.
type Param struct{ V any }

// Render writes the placeholder.
func (p Param) Render(w *Writer) { w.Param(p.V) }

// Prec implements Node.
func (Param) Prec() int { return PrecAtom }

// Keyword is a trusted SQL keyword or constant (TRUE, NULL, DEFAULT, *).
type Keyword string

// Render writes the keyword.
func (k Keyword) Render(w *Writer) { w.S(string(k)) }

// Prec implements Node.
func (Keyword) Prec() int { return PrecAtom }

// Common keywords.
const (
	True    Keyword = "TRUE"
	False   Keyword = "FALSE"
	Null    Keyword = "NULL"
	Default Keyword = "DEFAULT"
	Star    Keyword = "*"
)

// ErrNode is a placeholder for an expression that failed to build. Rendering
// it reports the error.
type ErrNode struct{ Err error }

// Render reports the error.
func (e ErrNode) Render(w *Writer) { w.Fail(e.Err) }

// Prec implements Node.
func (ErrNode) Prec() int { return PrecAtom }

// Errorf returns an ErrNode.
func Errorf(format string, args ...any) Node { return ErrNode{fmt.Errorf(format, args...)} }

// Binary is an infix operator. Op must be a trusted operator string.
type Binary struct {
	Op   string
	L, R Node
	P    int
}

// Render writes L op R.
func (b *Binary) Render(w *Writer) {
	w.Operand(b.L, b.P)
	w.B(' ')
	w.S(b.Op)
	w.B(' ')
	w.Operand(b.R, b.P)
}

// Prec implements Node.
func (b *Binary) Prec() int { return b.P }

// Prefix is a prefix operator such as NOT or unary minus.
type Prefix struct {
	Op string
	X  Node
	P  int
}

// Render writes op X.
func (p *Prefix) Render(w *Writer) {
	w.S(p.Op)
	w.B(' ')
	w.Operand(p.X, p.P-1)
}

// Prec implements Node.
func (p *Prefix) Prec() int { return p.P }

// Postfix is a postfix operator such as IS NULL.
type Postfix struct {
	X  Node
	Op string
	P  int
}

// Render writes X op.
func (p *Postfix) Render(w *Writer) {
	w.Operand(p.X, p.P)
	w.B(' ')
	w.S(p.Op)
}

// Prec implements Node.
func (p *Postfix) Prec() int { return p.P }

// Logical is an AND/OR chain. Items never contains nil.
type Logical struct {
	Or    bool
	Items []Node
}

// Render writes the chain. Nested chains of the same kind are flattened by
// the constructors, so only mixed nesting is parenthesised.
func (l *Logical) Render(w *Writer) {
	op, p := " AND ", PrecAnd
	if l.Or {
		op, p = " OR ", PrecOr
	}
	for i, it := range l.Items {
		if i > 0 {
			w.S(op)
		}
		if ll, ok := it.(*Logical); ok && ll.Or == l.Or {
			ll.Render(w)
			continue
		}
		w.Operand(it, p)
	}
}

// Prec implements Node.
func (l *Logical) Prec() int {
	if l.Or {
		return PrecOr
	}
	return PrecAnd
}

// In is X [NOT] IN (items) or X [NOT] IN (subquery).
type In struct {
	X     Node
	Items []Node // must be non-empty when Sub is nil
	Sub   Node
	Not   bool
}

// Render writes the IN predicate.
func (n *In) Render(w *Writer) {
	w.Operand(n.X, PrecRange)
	if n.Not {
		w.S(" NOT IN ")
	} else {
		w.S(" IN ")
	}
	if n.Sub != nil {
		w.Node(n.Sub)
		return
	}
	if len(n.Items) == 0 {
		w.Fail(errors.New("sqlcx: internal error: empty IN list reached the renderer"))
		return
	}
	w.B('(')
	w.List(n.Items)
	w.B(')')
}

// Prec implements Node.
func (*In) Prec() int { return PrecRange }

// Between is X [NOT] BETWEEN Lo AND Hi.
type Between struct {
	X, Lo, Hi Node
	Not       bool
}

// Render writes the BETWEEN predicate.
func (b *Between) Render(w *Writer) {
	w.Operand(b.X, PrecRange)
	if b.Not {
		w.S(" NOT BETWEEN ")
	} else {
		w.S(" BETWEEN ")
	}
	w.Operand(b.Lo, PrecRange)
	w.S(" AND ")
	w.Operand(b.Hi, PrecRange)
}

// Prec implements Node.
func (*Between) Prec() int { return PrecRange }

// Quantified is X op ANY|ALL (array-or-subquery).
type Quantified struct {
	X   Node
	Op  string
	All bool
	Arr Node
}

// Render writes the quantified comparison.
func (q *Quantified) Render(w *Writer) {
	w.Operand(q.X, PrecCompare)
	w.B(' ')
	w.S(q.Op)
	if q.All {
		w.S(" ALL ")
	} else {
		w.S(" ANY ")
	}
	if _, ok := q.Arr.(*Subquery); ok {
		w.Node(q.Arr)
		return
	}
	w.B('(')
	w.Node(q.Arr)
	w.B(')')
}

// Prec implements Node.
func (*Quantified) Prec() int { return PrecCompare }

// Cast is X::type. Type must have been validated.
type Cast struct {
	X    Node
	Type string
}

// Render writes CAST(X AS type).
func (c *Cast) Render(w *Writer) {
	w.S("CAST(")
	w.Node(c.X)
	w.S(" AS ")
	w.S(c.Type)
	w.B(')')
}

// Prec implements Node.
func (*Cast) Prec() int { return PrecAtom }

// Tuple is a row constructor (a, b, ...).
type Tuple struct{ Items []Node }

// Render writes (a, b).
func (t *Tuple) Render(w *Writer) {
	w.S("ROW(")
	w.List(t.Items)
	w.B(')')
}

// Prec implements Node.
func (*Tuple) Prec() int { return PrecAtom }

// Subquery is a parenthesised statement used as an expression or source.
type Subquery struct{ Stmt Node }

// Render writes (stmt).
func (s *Subquery) Render(w *Writer) {
	w.B('(')
	w.Node(s.Stmt)
	w.B(')')
}

// Prec implements Node.
func (*Subquery) Prec() int { return PrecAtom }

// Exists is [NOT] EXISTS (subquery).
type Exists struct {
	Sub Node
	Not bool
}

// Render writes the EXISTS predicate.
func (e *Exists) Render(w *Writer) {
	if e.Not {
		w.S("NOT ")
	}
	w.S("EXISTS ")
	w.Node(&Subquery{Stmt: e.Sub})
}

// Prec implements Node.
func (e *Exists) Prec() int {
	if e.Not {
		return PrecNot
	}
	return PrecAtom
}

// When is one branch of a CASE expression.
type When struct{ Cond, Then Node }

// Case is CASE WHEN ... THEN ... [ELSE ...] END.
type Case struct {
	Whens []When
	Else  Node
}

// Render writes the CASE expression.
func (c *Case) Render(w *Writer) {
	if len(c.Whens) == 0 {
		w.Fail(errors.New("sqlcx: CASE expression without WHEN branches"))
		return
	}
	w.S("CASE")
	for _, wh := range c.Whens {
		w.S(" WHEN ")
		w.Node(wh.Cond)
		w.S(" THEN ")
		w.Node(wh.Then)
	}
	if c.Else != nil {
		w.S(" ELSE ")
		w.Node(c.Else)
	}
	w.S(" END")
}

// Prec implements Node.
func (*Case) Prec() int { return PrecAtom }

// Excluded is EXCLUDED."name" inside ON CONFLICT DO UPDATE.
type Excluded struct{ Name string }

// Render writes the reference.
func (e Excluded) Render(w *Writer) {
	w.S("EXCLUDED.")
	w.Ident(e.Name)
}

// Prec implements Node.
func (Excluded) Prec() int { return PrecAtom }

// Raw is a trusted SQL fragment with bound arguments. Parts has exactly
// len(Args)+1 elements; argument i is rendered between Parts[i] and
// Parts[i+1].
type Raw struct {
	Parts []string
	Args  []Node
	P     int
}

// Render writes the fragment.
func (r *Raw) Render(w *Writer) {
	for i, p := range r.Parts {
		w.S(p)
		if i < len(r.Args) {
			w.Operand(r.Args[i], PrecUnary)
		}
	}
}

// Prec implements Node.
func (r *Raw) Prec() int { return r.P }
