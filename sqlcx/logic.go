package sqlcx

import (
	"strings"

	"github.com/Xwudao/go-sqlcx/internal/ast"
)

// And combines conditions with AND. Empty conditions are dropped; if every
// condition is empty the result is empty.
func And(conds ...Typed[bool]) Cond { return logical(false, conds) }

// Or combines conditions with OR. Empty conditions are dropped; if every
// condition is empty the result is empty (no constraint), not FALSE.
func Or(conds ...Typed[bool]) Cond { return logical(true, conds) }

func logical(or bool, conds []Typed[bool]) Cond {
	items := make([]ast.Node, 0, len(conds))
	for _, c := range conds {
		if c == nil {
			continue
		}
		n := c.node()
		if n == nil {
			continue
		}
		if l, ok := n.(*ast.Logical); ok && l.Or == or {
			items = append(items, l.Items...)
			continue
		}
		items = append(items, n)
	}
	switch len(items) {
	case 0:
		return Cond{}
	case 1:
		return Cond{boolOf(items[0])}
	}
	return Cond{boolOf(&ast.Logical{Or: or, Items: items})}
}

// Not negates a condition. Not of an empty condition is empty.
func Not(c Typed[bool]) Cond {
	if c == nil || c.node() == nil {
		return Cond{}
	}
	return Cond{boolOf(&ast.Prefix{Op: "NOT", X: c.node(), P: ast.PrecNot})}
}

// If returns c when ok is true and the empty condition otherwise.
func If(ok bool, c Typed[bool]) Cond {
	if !ok || c == nil {
		return Cond{}
	}
	return Cond{boolOf(c.node())}
}

// Optional applies f to *p when p is non-nil, and returns the empty
// condition otherwise:
//
//	q = q.Where(sqlcx.Optional(req.Status, u.Status.Eq))
func Optional[V any](p *V, f func(V) Cond) Cond {
	if p == nil {
		return Cond{}
	}
	return f(*p)
}

// IfSet applies f to v unless v is the zero value of V.
func IfSet[V comparable](v V, f func(V) Cond) Cond {
	var zero V
	if v == zero {
		return Cond{}
	}
	return f(v)
}

// IfAny applies f to vs unless vs is empty, which keeps an optional IN
// filter from turning into FALSE:
//
//	q = q.Where(sqlcx.IfAny(req.IDs, u.ID.In))
func IfAny[V any](vs []V, f func(...V) Cond) Cond {
	if len(vs) == 0 {
		return Cond{}
	}
	return f(vs...)
}

// Exists reports EXISTS (sub).
func Exists(sub Subquery) Cond { return Cond{boolOf(&ast.Exists{Sub: sub.stmtNode()})} }

// NotExists reports NOT EXISTS (sub).
func NotExists(sub Subquery) Cond {
	return Cond{boolOf(&ast.Exists{Sub: sub.stmtNode(), Not: true})}
}

// True is the constant TRUE condition.
func True() Cond { return Cond{boolOf(ast.True)} }

// False is the constant FALSE condition.
func False() Cond { return Cond{boolOf(ast.False)} }

// Val binds v as a parameter expression. PostgreSQL infers a parameter's
// type from its context; where there is none (a bare parameter in a select
// list, a CTE column) it defaults to text, so add an explicit cast there:
//
//	sqlcx.Val[int32](0).Cast[int32]("integer")
func Val[V any](v V) Value[V] { return Value[V]{param(v)} }

// NullOf is a typed SQL NULL.
func NullOf[V any]() Value[V] { return Value[V]{ast.Null} }

// Cast converts e with CAST(e AS pgType), typing the result as U. pgType is
// validated and must not come from untrusted input.
func Cast[U any](e Expr, pgType string) Value[U] {
	if err := ast.ValidateTypeName(pgType); err != nil {
		return Value[U]{ast.ErrNode{Err: err}}
	}
	return Value[U]{&ast.Cast{X: e.node(), Type: pgType}}
}

// EscapeLike escapes the LIKE metacharacters %, _ and \ in s so that it
// matches literally inside a LIKE/ILIKE pattern.
func EscapeLike(s string) string {
	if !strings.ContainsAny(s, `%_\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '%', '_', '\\':
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// AsComparable retypes e as a Comparable expression.
func AsComparable[V any](e Typed[V]) Comparable[V] { return Comparable[V]{}.withNode(e.node()) }

// AsOrdered retypes e as an Ordered expression.
func AsOrdered[V any](e Typed[V]) Ordered[V] { return Ordered[V]{}.withNode(e.node()) }

// AsText retypes e as a Text expression.
func AsText[V any](e Typed[V]) Text[V] { return Text[V]{}.withNode(e.node()) }

// AsNumber retypes e as a Number expression.
func AsNumber[V any](e Typed[V]) Number[V] { return Number[V]{}.withNode(e.node()) }

// AsBool retypes e as a Boolean expression.
func AsBool[V any](e Typed[V]) Boolean[V] { return Boolean[V]{}.withNode(e.node()) }

// AsJSONB retypes e as a JSONB expression.
func AsJSONB[V any](e Typed[V]) JSONB[V] { return JSONB[V]{}.withNode(e.node()) }

// AsArray retypes e as an Array expression with element type E.
func AsArray[V, E any](e Typed[V]) Array[V, E] { return Array[V, E]{}.withNode(e.node()) }
