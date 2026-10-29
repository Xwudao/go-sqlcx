package sqlcx

import (
	"errors"
	"time"

	"github.com/Xwudao/go-sqlcx/internal/ast"
)

// Numeric is implemented by Number expressions and numeric columns.
type Numeric interface {
	Expr
	numeric()
}

// OrderedTyped is implemented by ordered expressions and columns of value
// type V.
type OrderedTyped[V any] interface {
	Typed[V]
	ordered()
}

// Textual is implemented by text expressions and columns.
type Textual interface {
	Expr
	textual()
}

func agg(name string, args ...ast.Node) *ast.Func {
	return &ast.Func{Name: name, Args: args, Kind: ast.FuncAggregate}
}

// Count is count(*).
func Count() Number[int64] {
	return Number[int64]{}.withNode(&ast.Func{Name: "count", Star: true, Kind: ast.FuncAggregate})
}

// CountOf is count(e), counting non-NULL values.
func CountOf(e Expr) Number[int64] { return Number[int64]{}.withNode(agg("count", e.node())) }

// CountDistinct is count(DISTINCT e).
func CountDistinct(e Expr) Number[int64] {
	f := agg("count", e.node())
	f.Distinct = true
	return Number[int64]{}.withNode(f)
}

// Sum is sum(e). PostgreSQL widens the result type (sum(int4) is int8,
// sum(int8) is numeric), so the Go result type R is stated explicitly:
//
//	sqlcx.Sum[int64](o.Quantity)
//
// The result is NULL for an empty input; scan with BindNull or Coalesce.
func Sum[R any](e Numeric) Number[R] { return Number[R]{}.withNode(agg("sum", e.node())) }

// Avg is avg(e) with an explicit result type (numeric or double precision).
func Avg[R any](e Numeric) Number[R] { return Number[R]{}.withNode(agg("avg", e.node())) }

// Min is min(e).
func Min[V any](e OrderedTyped[V]) Ordered[V] { return Ordered[V]{}.withNode(agg("min", e.node())) }

// Max is max(e).
func Max[V any](e OrderedTyped[V]) Ordered[V] { return Ordered[V]{}.withNode(agg("max", e.node())) }

// BoolAnd is bool_and(e).
func BoolAnd(e Typed[bool]) Cond { return Cond{boolOf(agg("bool_and", e.node()))} }

// BoolOr is bool_or(e).
func BoolOr(e Typed[bool]) Cond { return Cond{boolOf(agg("bool_or", e.node()))} }

// ArrayAgg is array_agg(e).
func ArrayAgg[V any](e Typed[V], order ...OrderTerm) Array[[]V, V] {
	f := agg("array_agg", e.node())
	f.OrderBy = orderItems(order)
	return Array[[]V, V]{}.withNode(f)
}

// StringAgg is string_agg(e, sep).
func StringAgg(e Textual, sep string, order ...OrderTerm) Text[string] {
	f := agg("string_agg", e.node(), &ast.Cast{X: param(sep), Type: "text"})
	f.OrderBy = orderItems(order)
	return Text[string]{}.withNode(f)
}

// JSONBAgg is jsonb_agg(e).
func JSONBAgg(e Expr, order ...OrderTerm) JSONB[[]byte] {
	f := agg("jsonb_agg", e.node())
	f.OrderBy = orderItems(order)
	return JSONB[[]byte]{}.withNode(f)
}

// ToJSONB is to_jsonb(e).
func ToJSONB(e Expr) JSONB[[]byte] { return JSONB[[]byte]{}.withNode(call("to_jsonb", e.node())) }

// Coalesce is coalesce(first, rest...).
func Coalesce[V any](first Typed[V], rest ...Typed[V]) Value[V] {
	args := make([]ast.Node, 0, 1+len(rest))
	args = append(args, first.node())
	for _, r := range rest {
		args = append(args, r.node())
	}
	return Value[V]{call("coalesce", args...)}
}

// CoalesceVal is coalesce(e, def).
func CoalesceVal[V any](e Typed[V], def V) Value[V] {
	return Value[V]{call("coalesce", e.node(), param(def))}
}

// NullIf is nullif(e, v).
func NullIf[V any](e Typed[V], v V) Value[V] { return Value[V]{call("nullif", e.node(), param(v))} }

// Greatest is greatest(first, rest...).
func Greatest[V any](first OrderedTyped[V], rest ...OrderedTyped[V]) Ordered[V] {
	return Ordered[V]{}.withNode(variadic("greatest", first, rest))
}

// Least is least(first, rest...).
func Least[V any](first OrderedTyped[V], rest ...OrderedTyped[V]) Ordered[V] {
	return Ordered[V]{}.withNode(variadic("least", first, rest))
}

func variadic[V any](name string, first OrderedTyped[V], rest []OrderedTyped[V]) ast.Node {
	args := []ast.Node{first.node()}
	for _, r := range rest {
		args = append(args, r.node())
	}
	return call(name, args...)
}

// Now is now() (transaction start time).
func Now() Ordered[time.Time] { return Ordered[time.Time]{}.withNode(call("now")) }

// Func calls a scalar function by name. name is validated and must be a
// developer-written constant; args follow the Raw rules (Expr rendered in
// place, other values bound).
func Func[V any](name string, args ...any) Value[V] {
	if err := ast.ValidateFuncName(name); err != nil {
		return Value[V]{ast.ErrNode{Err: err}}
	}
	nodes := make([]ast.Node, len(args))
	for i, a := range args {
		nodes[i] = argNode(a)
	}
	return Value[V]{&ast.Func{Name: name, Args: nodes}}
}

// AggregateFunc calls an aggregate function by name, so that Over and
// Filter accept it.
func AggregateFunc[V any](name string, args ...any) Value[V] {
	v := Func[V](name, args...)
	if f, ok := v.n.(*ast.Func); ok {
		f.Kind = ast.FuncAggregate
	}
	return v
}

// ---- window functions ----

func windowFn(name string, args ...ast.Node) *ast.Func {
	return &ast.Func{Name: name, Args: args, Kind: ast.FuncWindow}
}

// RowNumber is row_number(); it must be used with Over.
func RowNumber() Number[int64] { return Number[int64]{}.withNode(windowFn("row_number")) }

// Rank is rank(); it must be used with Over.
func Rank() Number[int64] { return Number[int64]{}.withNode(windowFn("rank")) }

// DenseRank is dense_rank(); it must be used with Over.
func DenseRank() Number[int64] { return Number[int64]{}.withNode(windowFn("dense_rank")) }

// PercentRank is percent_rank(); it must be used with Over.
func PercentRank() Number[float64] { return Number[float64]{}.withNode(windowFn("percent_rank")) }

// CumeDist is cume_dist(); it must be used with Over.
func CumeDist() Number[float64] { return Number[float64]{}.withNode(windowFn("cume_dist")) }

// Ntile is ntile(n); it must be used with Over.
func Ntile[N Integer](n N) Number[int32] {
	return Number[int32]{}.withNode(windowFn("ntile", int32Param("ntile", n)))
}

// Lag is lag(e, offset); it must be used with Over. The result is NULL for
// rows without a predecessor.
func Lag[V any, N Integer](e Typed[V], offset N) Value[V] {
	return Value[V]{windowFn("lag", e.node(), &ast.Cast{X: int32Param("lag offset", offset), Type: "integer"})}
}

// Lead is lead(e, offset); it must be used with Over.
func Lead[V any, N Integer](e Typed[V], offset N) Value[V] {
	return Value[V]{windowFn("lead", e.node(), &ast.Cast{X: int32Param("lead offset", offset), Type: "integer"})}
}

// FirstValue is first_value(e); it must be used with Over.
func FirstValue[V any](e Typed[V]) Value[V] { return Value[V]{windowFn("first_value", e.node())} }

// LastValue is last_value(e); it must be used with Over.
func LastValue[V any](e Typed[V]) Value[V] { return Value[V]{windowFn("last_value", e.node())} }

// Over attaches a window to an aggregate or window function call:
//
//	sqlcx.Over(sqlcx.Sum[int64](o.Amount), sqlcx.Window().PartitionBy(o.UserID))
//
// Using Over with an expression that is not an aggregate or window function
// call is reported when the statement is built.
func Over[E category[E]](e E, w WindowSpec) E {
	f, ok := e.node().(*ast.Func)
	if !ok || f.Kind == ast.FuncScalar {
		return e.withNode(ast.ErrNode{Err: errors.New("sqlcx: OVER requires an aggregate or window function call")})
	}
	if f.Over != nil {
		return e.withNode(ast.ErrNode{Err: errors.New("sqlcx: window function already has an OVER clause")})
	}
	cp := *f
	spec := w.spec
	cp.Over = &spec
	return e.withNode(&cp)
}

// Filter attaches FILTER (WHERE cond) to an aggregate call.
func Filter[E category[E]](e E, cond Typed[bool]) E {
	f, ok := e.node().(*ast.Func)
	if !ok || f.Kind != ast.FuncAggregate {
		return e.withNode(ast.ErrNode{Err: errors.New("sqlcx: FILTER requires an aggregate function call")})
	}
	if cond == nil || cond.node() == nil {
		return e
	}
	cp := *f
	cp.Filter = cond.node()
	return e.withNode(&cp)
}

// WindowSpec is a window definition built with Window().
type WindowSpec struct{ spec ast.Window }

// Window starts an empty window definition (OVER ()).
func Window() WindowSpec { return WindowSpec{} }

// PartitionBy sets PARTITION BY.
func (w WindowSpec) PartitionBy(es ...Expr) WindowSpec {
	w.spec.PartitionBy = nodes(es)
	return w
}

// OrderBy sets the window ORDER BY.
func (w WindowSpec) OrderBy(terms ...OrderTerm) WindowSpec {
	w.spec.OrderBy = orderItems(terms)
	return w
}

// FrameBound is one end of a window frame.
type FrameBound struct {
	text string
	arg  ast.Node
}

// UnboundedPreceding is UNBOUNDED PRECEDING.
func UnboundedPreceding() FrameBound { return FrameBound{text: "UNBOUNDED PRECEDING"} }

// UnboundedFollowing is UNBOUNDED FOLLOWING.
func UnboundedFollowing() FrameBound { return FrameBound{text: "UNBOUNDED FOLLOWING"} }

// CurrentRow is CURRENT ROW.
func CurrentRow() FrameBound { return FrameBound{text: "CURRENT ROW"} }

// Preceding is n PRECEDING (ROWS and GROUPS frames).
func Preceding[N Integer](n N) FrameBound {
	return FrameBound{text: "\x00 PRECEDING", arg: int64Param("frame offset", n)}
}

// Following is n FOLLOWING (ROWS and GROUPS frames).
func Following[N Integer](n N) FrameBound {
	return FrameBound{text: "\x00 FOLLOWING", arg: int64Param("frame offset", n)}
}

// Rows sets ROWS BETWEEN start AND end.
func (w WindowSpec) Rows(start, end FrameBound) WindowSpec { return w.frame("ROWS", start, end) }

// Range sets RANGE BETWEEN start AND end.
func (w WindowSpec) Range(start, end FrameBound) WindowSpec { return w.frame("RANGE", start, end) }

// Groups sets GROUPS BETWEEN start AND end.
func (w WindowSpec) Groups(start, end FrameBound) WindowSpec { return w.frame("GROUPS", start, end) }

func (w WindowSpec) frame(mode string, start, end FrameBound) WindowSpec {
	w.spec.Frame = mode + " BETWEEN " + start.text + " AND " + end.text
	w.spec.FrameArgs = nil
	for _, b := range []FrameBound{start, end} {
		if b.arg != nil {
			w.spec.FrameArgs = append(w.spec.FrameArgs, b.arg)
		}
	}
	return w
}

// ---- CASE ----

// CaseBuilder builds a searched CASE expression of value type V.
type CaseBuilder[V any] struct{ c ast.Case }

// Case starts CASE WHEN ... END.
func Case[V any]() CaseBuilder[V] { return CaseBuilder[V]{} }

// When adds WHEN cond THEN v.
func (b CaseBuilder[V]) When(cond Typed[bool], v V) CaseBuilder[V] {
	return b.WhenExpr(cond, Val(v))
}

// WhenExpr adds WHEN cond THEN e.
func (b CaseBuilder[V]) WhenExpr(cond Typed[bool], e Typed[V]) CaseBuilder[V] {
	n := cond.node()
	if n == nil {
		n = ast.Errorf("sqlcx: CASE WHEN with an empty condition")
	}
	b.c.Whens = append(b.c.Whens[:len(b.c.Whens):len(b.c.Whens)], ast.When{Cond: n, Then: e.node()})
	return b
}

// Else sets ELSE v and returns the expression.
func (b CaseBuilder[V]) Else(v V) Value[V] { return b.ElseExpr(Val(v)) }

// ElseExpr sets ELSE e and returns the expression.
func (b CaseBuilder[V]) ElseExpr(e Typed[V]) Value[V] {
	c := b.c
	c.Else = e.node()
	return Value[V]{&c}
}

// End returns the expression without ELSE (NULL when no branch matches).
func (b CaseBuilder[V]) End() Value[V] {
	c := b.c
	return Value[V]{&c}
}

func nodes(es []Expr) []ast.Node {
	out := make([]ast.Node, len(es))
	for i, e := range es {
		out[i] = e.node()
	}
	return out
}
