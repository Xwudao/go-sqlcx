package sqlcx

import (
	"github.com/Xwudao/go-sqlcx/internal/ast"
)

// Expr is any SQL expression. The interface is sealed: only values built by
// this package (columns, operators, functions, parameters and explicitly
// trusted Raw fragments) implement it.
type Expr interface {
	node() ast.Node
}

// Typed is an expression whose values compare against Go values of type V.
// For a nullable column V is the non-null value type (string for a
// pgtype.Text column), so comparisons never take a NULL operand.
type Typed[V any] interface {
	Expr
	valueType(*V)
}

// Selectable is an expression that scans into a Go value of type S. For a
// column S is the model field type (pgtype.Text for a nullable text column);
// for other expressions S equals the value type V.
type Selectable[S any] interface {
	Expr
	scanType(*S)
}

// category is implemented by the expression category types (Value, Text,
// Number, ...). It lets generic helpers re-wrap a node while keeping the
// category, without accepting columns.
type category[E any] interface {
	Expr
	withNode(ast.Node) E
}

// Value is an expression of Go value type V supporting only the operations
// valid for every PostgreSQL type (IS NULL, casts, ordering helpers).
type Value[V any] struct{ n ast.Node }

func (e Value[V]) node() ast.Node             { return e.n }
func (Value[V]) valueType(*V)                 {}
func (Value[V]) scanType(*V)                  {}
func (Value[V]) withNode(n ast.Node) Value[V] { return Value[V]{n} }
func (e Value[V]) sortBy(desc bool) OrderTerm {
	return Order[V]{item: ast.OrderItem{X: e.n, Desc: desc}}
}

// IsNull reports e IS NULL.
func (e Value[V]) IsNull() Cond { return postfix(e.n, "IS NULL") }

// IsNotNull reports e IS NOT NULL.
func (e Value[V]) IsNotNull() Cond { return postfix(e.n, "IS NOT NULL") }

// IsDistinctFrom is the NULL-safe inequality e IS DISTINCT FROM v.
func (e Value[V]) IsDistinctFrom(v V) Cond {
	return binary(e.n, "IS DISTINCT FROM", param(v), ast.PrecIs)
}

// IsNotDistinctFrom is the NULL-safe equality e IS NOT DISTINCT FROM v.
func (e Value[V]) IsNotDistinctFrom(v V) Cond {
	return binary(e.n, "IS NOT DISTINCT FROM", param(v), ast.PrecIs)
}

// IsDistinctFromExpr is the NULL-safe inequality against another expression.
func (e Value[V]) IsDistinctFromExpr(o Typed[V]) Cond {
	return binary(e.n, "IS DISTINCT FROM", o.node(), ast.PrecIs)
}

// IsNotDistinctFromExpr is the NULL-safe equality against another expression.
func (e Value[V]) IsNotDistinctFromExpr(o Typed[V]) Cond {
	return binary(e.n, "IS NOT DISTINCT FROM", o.node(), ast.PrecIs)
}

// Asc orders by e ascending.
func (e Value[V]) Asc() Order[V] { return Order[V]{item: ast.OrderItem{X: e.n}} }

// Desc orders by e descending.
func (e Value[V]) Desc() Order[V] { return Order[V]{item: ast.OrderItem{X: e.n, Desc: true}} }

// Cast converts e with CAST(e AS pgType) and retypes it as U. pgType must be
// a PostgreSQL type name written by the developer; it is validated.
func (e Value[V]) Cast[U any](pgType string) Value[U] { return Cast[U](e, pgType) }

// Comparable adds equality operators to Value.
type Comparable[V any] struct{ Value[V] }

func (Comparable[V]) withNode(n ast.Node) Comparable[V] { return Comparable[V]{Value[V]{n}} }

// Eq reports e = v.
func (e Comparable[V]) Eq(v V) Cond { return compare(e.n, "=", param(v)) }

// Ne reports e <> v. NULL values of e never match; see IsDistinctFrom.
func (e Comparable[V]) Ne(v V) Cond { return compare(e.n, "<>", param(v)) }

// EqExpr reports e = o.
func (e Comparable[V]) EqExpr(o Typed[V]) Cond { return compare(e.n, "=", o.node()) }

// NeExpr reports e <> o.
func (e Comparable[V]) NeExpr(o Typed[V]) Cond { return compare(e.n, "<>", o.node()) }

// In reports e IN (vs...). An empty list yields FALSE.
func (e Comparable[V]) In(vs ...V) Cond { return in(e.n, vs, false) }

// NotIn reports e NOT IN (vs...). An empty list yields TRUE. Rows where e
// is NULL never match.
func (e Comparable[V]) NotIn(vs ...V) Cond { return in(e.n, vs, true) }

// InExpr reports e IN (exprs...). An empty list yields FALSE.
func (e Comparable[V]) InExpr(es ...Typed[V]) Cond { return inExpr(e.n, es, false) }

// InQuery reports e IN (subquery).
func (e Comparable[V]) InQuery(sub Values[V]) Cond {
	return Cond{boolOf(&ast.In{X: e.n, Sub: sub.node()})}
}

// NotInQuery reports e NOT IN (subquery). If the subquery yields a NULL the
// predicate is never true (SQL three-valued logic); prefer NotExists when the
// subquery column is nullable.
func (e Comparable[V]) NotInQuery(sub Values[V]) Cond {
	return Cond{boolOf(&ast.In{X: e.n, Sub: sub.node(), Not: true})}
}

// EqAny reports e = ANY($1) with vs bound as a single array parameter.
func (e Comparable[V]) EqAny(vs []V) Cond { return e.CompareAny(OpEq, vs) }

// NeAll reports e <> ALL($1) with vs bound as a single array parameter.
func (e Comparable[V]) NeAll(vs []V) Cond { return e.CompareAll(OpNe, vs) }

// CompareAny reports e op ANY($1).
func (e Comparable[V]) CompareAny(op Op, vs []V) Cond { return quantified(e.n, op, false, param(vs)) }

// CompareAll reports e op ALL($1).
func (e Comparable[V]) CompareAll(op Op, vs []V) Cond { return quantified(e.n, op, true, param(vs)) }

// CompareAnyQuery reports e op ANY (subquery).
func (e Comparable[V]) CompareAnyQuery(op Op, sub Values[V]) Cond {
	return quantified(e.n, op, false, sub.node())
}

// CompareAllQuery reports e op ALL (subquery).
func (e Comparable[V]) CompareAllQuery(op Op, sub Values[V]) Cond {
	return quantified(e.n, op, true, sub.node())
}

// Ordered adds ordering comparisons to Comparable.
type Ordered[V any] struct{ Comparable[V] }

func (Ordered[V]) withNode(n ast.Node) Ordered[V] { return Ordered[V]{Comparable[V]{Value[V]{n}}} }
func (Ordered[V]) ordered()                       {}

// Gt reports e > v.
func (e Ordered[V]) Gt(v V) Cond { return compare(e.n, ">", param(v)) }

// Gte reports e >= v.
func (e Ordered[V]) Gte(v V) Cond { return compare(e.n, ">=", param(v)) }

// Lt reports e < v.
func (e Ordered[V]) Lt(v V) Cond { return compare(e.n, "<", param(v)) }

// Lte reports e <= v.
func (e Ordered[V]) Lte(v V) Cond { return compare(e.n, "<=", param(v)) }

// GtExpr reports e > o.
func (e Ordered[V]) GtExpr(o Typed[V]) Cond { return compare(e.n, ">", o.node()) }

// GteExpr reports e >= o.
func (e Ordered[V]) GteExpr(o Typed[V]) Cond { return compare(e.n, ">=", o.node()) }

// LtExpr reports e < o.
func (e Ordered[V]) LtExpr(o Typed[V]) Cond { return compare(e.n, "<", o.node()) }

// LteExpr reports e <= o.
func (e Ordered[V]) LteExpr(o Typed[V]) Cond { return compare(e.n, "<=", o.node()) }

// Between reports e BETWEEN lo AND hi (inclusive).
func (e Ordered[V]) Between(lo, hi V) Cond {
	return Cond{boolOf(&ast.Between{X: e.n, Lo: param(lo), Hi: param(hi)})}
}

// NotBetween reports e NOT BETWEEN lo AND hi.
func (e Ordered[V]) NotBetween(lo, hi V) Cond {
	return Cond{boolOf(&ast.Between{X: e.n, Lo: param(lo), Hi: param(hi), Not: true})}
}

// BetweenExpr reports e BETWEEN lo AND hi for expression bounds.
func (e Ordered[V]) BetweenExpr(lo, hi Typed[V]) Cond {
	return Cond{boolOf(&ast.Between{X: e.n, Lo: lo.node(), Hi: hi.node()})}
}

// Text adds string operators to Ordered.
type Text[V any] struct{ Ordered[V] }

func (Text[V]) withNode(n ast.Node) Text[V] { return Text[V]{Ordered[V]{Comparable[V]{Value[V]{n}}}} }
func (Text[V]) textual()                    {}

// Like reports e LIKE pattern. Use EscapeLike for untrusted fragments.
func (e Text[V]) Like(pattern string) Cond { return binary(e.n, "LIKE", param(pattern), ast.PrecRange) }

// NotLike reports e NOT LIKE pattern.
func (e Text[V]) NotLike(pattern string) Cond {
	return binary(e.n, "NOT LIKE", param(pattern), ast.PrecRange)
}

// ILike reports e ILIKE pattern (case-insensitive).
func (e Text[V]) ILike(pattern string) Cond {
	return binary(e.n, "ILIKE", param(pattern), ast.PrecRange)
}

// NotILike reports e NOT ILIKE pattern.
func (e Text[V]) NotILike(pattern string) Cond {
	return binary(e.n, "NOT ILIKE", param(pattern), ast.PrecRange)
}

// SimilarTo reports e SIMILAR TO pattern.
func (e Text[V]) SimilarTo(pattern string) Cond {
	return binary(e.n, "SIMILAR TO", param(pattern), ast.PrecRange)
}

// MatchRegex reports e ~ pattern (POSIX regular expression).
func (e Text[V]) MatchRegex(pattern string) Cond {
	return binary(e.n, "~", param(pattern), ast.PrecOther)
}

// MatchRegexFold reports e ~* pattern (case-insensitive POSIX regex).
func (e Text[V]) MatchRegexFold(pattern string) Cond {
	return binary(e.n, "~*", param(pattern), ast.PrecOther)
}

// HasPrefix reports e LIKE 'prefix%' with prefix escaped.
func (e Text[V]) HasPrefix(prefix string) Cond { return e.Like(EscapeLike(prefix) + "%") }

// Contains reports e LIKE '%s%' with s escaped.
func (e Text[V]) Contains(s string) Cond { return e.Like("%" + EscapeLike(s) + "%") }

// ContainsFold reports e ILIKE '%s%' with s escaped.
func (e Text[V]) ContainsFold(s string) Cond { return e.ILike("%" + EscapeLike(s) + "%") }

// Lower returns lower(e).
func (e Text[V]) Lower() Text[V] { return Text[V]{}.withNode(call("lower", e.n)) }

// Upper returns upper(e).
func (e Text[V]) Upper() Text[V] { return Text[V]{}.withNode(call("upper", e.n)) }

// Length returns char_length(e).
func (e Text[V]) Length() Number[int32] { return Number[int32]{}.withNode(call("char_length", e.n)) }

// Concat returns e || v.
func (e Text[V]) Concat(v V) Text[V] {
	return Text[V]{}.withNode(&ast.Binary{Op: "||", L: e.n, R: param(v), P: ast.PrecOther})
}

// ConcatExpr returns e || o.
func (e Text[V]) ConcatExpr(o Typed[V]) Text[V] {
	return Text[V]{}.withNode(&ast.Binary{Op: "||", L: e.n, R: o.node(), P: ast.PrecOther})
}

// Number adds arithmetic to Ordered.
type Number[V any] struct{ Ordered[V] }

func (Number[V]) withNode(n ast.Node) Number[V] {
	return Number[V]{Ordered[V]{Comparable[V]{Value[V]{n}}}}
}
func (Number[V]) numeric() {}

func (e Number[V]) arith(op string, prec int, r ast.Node) Number[V] {
	return Number[V]{}.withNode(&ast.Binary{Op: op, L: e.n, R: r, P: prec})
}

// Add returns e + v.
func (e Number[V]) Add(v V) Number[V] { return e.arith("+", ast.PrecAdd, param(v)) }

// Sub returns e - v.
func (e Number[V]) Sub(v V) Number[V] { return e.arith("-", ast.PrecAdd, param(v)) }

// Mul returns e * v.
func (e Number[V]) Mul(v V) Number[V] { return e.arith("*", ast.PrecMul, param(v)) }

// Div returns e / v. Integer division truncates as in PostgreSQL.
func (e Number[V]) Div(v V) Number[V] { return e.arith("/", ast.PrecMul, param(v)) }

// Mod returns e % v.
func (e Number[V]) Mod(v V) Number[V] { return e.arith("%", ast.PrecMul, param(v)) }

// AddExpr returns e + o.
func (e Number[V]) AddExpr(o Typed[V]) Number[V] { return e.arith("+", ast.PrecAdd, o.node()) }

// SubExpr returns e - o.
func (e Number[V]) SubExpr(o Typed[V]) Number[V] { return e.arith("-", ast.PrecAdd, o.node()) }

// MulExpr returns e * o.
func (e Number[V]) MulExpr(o Typed[V]) Number[V] { return e.arith("*", ast.PrecMul, o.node()) }

// DivExpr returns e / o.
func (e Number[V]) DivExpr(o Typed[V]) Number[V] { return e.arith("/", ast.PrecMul, o.node()) }

// Neg returns -e.
func (e Number[V]) Neg() Number[V] {
	return Number[V]{}.withNode(&ast.Prefix{Op: "-", X: e.n, P: ast.PrecUnary})
}

// Abs returns abs(e).
func (e Number[V]) Abs() Number[V] { return Number[V]{}.withNode(call("abs", e.n)) }

// Boolean adds truth tests to Comparable. Cond is Boolean[bool].
type Boolean[V any] struct{ Comparable[V] }

func (Boolean[V]) withNode(n ast.Node) Boolean[V] { return Boolean[V]{Comparable[V]{Value[V]{n}}} }

// Cond is a boolean SQL condition. The zero Cond is empty: it is dropped by
// Where, And, Or and Not, which makes optional filters easy to compose.
type Cond = Boolean[bool]

// IsEmpty reports whether the condition is the empty (absent) condition.
func (e Boolean[V]) IsEmpty() bool { return e.n == nil }

// If returns the condition when ok is true and the empty condition
// otherwise, so optional filters stay inline:
//
//	q.Where(
//	    u.Name.ContainsFold(req.Name).If(req.Name != ""),
//	    u.Age.Gte(req.MinAge).If(req.MinAge > 0),
//	)
//
// The operands are evaluated either way; for values behind a pointer use
// Optional, which dereferences only when the pointer is non-nil.
func (e Boolean[V]) If(ok bool) Cond {
	if !ok {
		return Cond{}
	}
	return Cond{boolOf(e.n)}
}

// IsTrue reports e IS TRUE (false for NULL).
func (e Boolean[V]) IsTrue() Cond { return postfix(e.n, "IS TRUE") }

// IsFalse reports e IS FALSE (false for NULL).
func (e Boolean[V]) IsFalse() Cond { return postfix(e.n, "IS FALSE") }

// IsNotTrue reports e IS NOT TRUE (true for NULL).
func (e Boolean[V]) IsNotTrue() Cond { return postfix(e.n, "IS NOT TRUE") }

// IsNotFalse reports e IS NOT FALSE (true for NULL).
func (e Boolean[V]) IsNotFalse() Cond { return postfix(e.n, "IS NOT FALSE") }

// And returns e AND others.
func (e Boolean[V]) And(others ...Typed[bool]) Cond {
	return And(append([]Typed[bool]{Value[bool]{e.n}}, others...)...)
}

// Or returns e OR others.
func (e Boolean[V]) Or(others ...Typed[bool]) Cond {
	return Or(append([]Typed[bool]{Value[bool]{e.n}}, others...)...)
}

// Op is a comparison operator usable with ANY/ALL.
type Op string

// Comparison operators.
const (
	OpEq  Op = "="
	OpNe  Op = "<>"
	OpLt  Op = "<"
	OpLte Op = "<="
	OpGt  Op = ">"
	OpGte Op = ">="
)

func (op Op) valid() bool {
	switch op {
	case OpEq, OpNe, OpLt, OpLte, OpGt, OpGte:
		return true
	}
	return false
}

// ---- node helpers ----

func param(v any) ast.Node { return ast.Param{V: v} }

func boolOf(n ast.Node) Comparable[bool] { return Comparable[bool]{Value[bool]{n}} }

func compare(l ast.Node, op string, r ast.Node) Cond {
	return Cond{boolOf(&ast.Binary{Op: op, L: l, R: r, P: ast.PrecCompare})}
}

func binary(l ast.Node, op string, r ast.Node, prec int) Cond {
	return Cond{boolOf(&ast.Binary{Op: op, L: l, R: r, P: prec})}
}

func postfix(x ast.Node, op string) Cond {
	return Cond{boolOf(&ast.Postfix{X: x, Op: op, P: ast.PrecIs})}
}

func quantified(x ast.Node, op Op, all bool, arr ast.Node) Cond {
	if !op.valid() {
		return Cond{boolOf(ast.Errorf("sqlcx: invalid comparison operator %q", string(op)))}
	}
	return Cond{boolOf(&ast.Quantified{X: x, Op: string(op), All: all, Arr: arr})}
}

func in[V any](x ast.Node, vs []V, not bool) Cond {
	if len(vs) == 0 {
		if not {
			return Cond{boolOf(ast.True)}
		}
		return Cond{boolOf(ast.False)}
	}
	items := make([]ast.Node, len(vs))
	for i, v := range vs {
		items[i] = param(v)
	}
	return Cond{boolOf(&ast.In{X: x, Items: items, Not: not})}
}

func inExpr[V any](x ast.Node, es []Typed[V], not bool) Cond {
	if len(es) == 0 {
		if not {
			return Cond{boolOf(ast.True)}
		}
		return Cond{boolOf(ast.False)}
	}
	items := make([]ast.Node, len(es))
	for i, e := range es {
		items[i] = e.node()
	}
	return Cond{boolOf(&ast.In{X: x, Items: items, Not: not})}
}

func call(name string, args ...ast.Node) ast.Node { return &ast.Func{Name: name, Args: args} }
