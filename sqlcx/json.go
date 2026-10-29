package sqlcx

import (
	"encoding/json"
	"fmt"

	"github.com/Xwudao/go-sqlcx/internal/ast"
)

// JSON is an expression of PostgreSQL type json. The json type has no
// equality or containment operators; use JSONB for those.
type JSON[V any] struct{ Value[V] }

func (JSON[V]) withNode(n ast.Node) JSON[V] { return JSON[V]{Value[V]{n}} }

// Get returns e -> key.
func (e JSON[V]) Get(key string) JSON[[]byte] { return JSON[[]byte]{}.withNode(jsonOp(e.n, "->", key)) }

// Index returns e -> i for a JSON array.
func (e JSON[V]) Index(i int) JSON[[]byte] {
	return JSON[[]byte]{}.withNode(jsonOp(e.n, "->", int32(i)))
}

// GetText returns e ->> key. The result is NULL when the key is absent.
func (e JSON[V]) GetText(key string) Text[string] {
	return Text[string]{}.withNode(jsonOp(e.n, "->>", key))
}

// Path returns e #> path.
func (e JSON[V]) Path(path ...string) JSON[[]byte] {
	return JSON[[]byte]{}.withNode(jsonOp(e.n, "#>", textArray(path)))
}

// PathText returns e #>> path.
func (e JSON[V]) PathText(path ...string) Text[string] {
	return Text[string]{}.withNode(jsonOp(e.n, "#>>", textArray(path)))
}

// JSONB is an expression of PostgreSQL type jsonb.
type JSONB[V any] struct{ Comparable[V] }

func (JSONB[V]) withNode(n ast.Node) JSONB[V] { return JSONB[V]{Comparable[V]{Value[V]{n}}} }

// Get returns e -> key.
func (e JSONB[V]) Get(key string) JSONB[[]byte] {
	return JSONB[[]byte]{}.withNode(jsonOp(e.n, "->", key))
}

// Index returns e -> i for a JSON array.
func (e JSONB[V]) Index(i int) JSONB[[]byte] {
	return JSONB[[]byte]{}.withNode(jsonOp(e.n, "->", int32(i)))
}

// GetText returns e ->> key. The result is NULL when the key is absent.
func (e JSONB[V]) GetText(key string) Text[string] {
	return Text[string]{}.withNode(jsonOp(e.n, "->>", key))
}

// Path returns e #> path.
func (e JSONB[V]) Path(path ...string) JSONB[[]byte] {
	return JSONB[[]byte]{}.withNode(jsonOp(e.n, "#>", textArray(path)))
}

// PathText returns e #>> path.
func (e JSONB[V]) PathText(path ...string) Text[string] {
	return Text[string]{}.withNode(jsonOp(e.n, "#>>", textArray(path)))
}

// Contains reports e @> v.
func (e JSONB[V]) Contains(v V) Cond {
	return binary(e.n, "@>", jsonbParam(v), ast.PrecOther)
}

// ContainsJSON reports e @> json(v), marshalling v with encoding/json.
func (e JSONB[V]) ContainsJSON(v any) Cond {
	return binary(e.n, "@>", marshalJSONB(v), ast.PrecOther)
}

// ContainedBy reports e <@ v.
func (e JSONB[V]) ContainedBy(v V) Cond {
	return binary(e.n, "<@", jsonbParam(v), ast.PrecOther)
}

// HasKey reports e ? key.
func (e JSONB[V]) HasKey(key string) Cond { return binary(e.n, "?", param(key), ast.PrecOther) }

// HasAnyKey reports e ?| keys.
func (e JSONB[V]) HasAnyKey(keys ...string) Cond {
	return binary(e.n, "?|", textArray(keys), ast.PrecOther)
}

// HasAllKeys reports e ?& keys.
func (e JSONB[V]) HasAllKeys(keys ...string) Cond {
	return binary(e.n, "?&", textArray(keys), ast.PrecOther)
}

// MatchPath reports e @@ jsonpath, a jsonpath predicate check.
func (e JSONB[V]) MatchPath(jsonpath string) Cond {
	return binary(e.n, "@@", &ast.Cast{X: param(jsonpath), Type: "jsonpath"}, ast.PrecOther)
}

// PathExists reports e @? jsonpath.
func (e JSONB[V]) PathExists(jsonpath string) Cond {
	return binary(e.n, "@?", &ast.Cast{X: param(jsonpath), Type: "jsonpath"}, ast.PrecOther)
}

// Concat returns e || v (shallow merge of objects, concatenation of arrays).
func (e JSONB[V]) Concat(v V) JSONB[V] {
	return JSONB[V]{}.withNode(&ast.Binary{Op: "||", L: e.n, R: jsonbParam(v), P: ast.PrecOther})
}

// Merge returns e || json(v), marshalling v with encoding/json.
func (e JSONB[V]) Merge(v any) JSONB[V] {
	return JSONB[V]{}.withNode(&ast.Binary{Op: "||", L: e.n, R: marshalJSONB(v), P: ast.PrecOther})
}

// DeleteKey returns e - key.
func (e JSONB[V]) DeleteKey(key string) JSONB[V] {
	return JSONB[V]{}.withNode(&ast.Binary{Op: "-", L: e.n, R: &ast.Cast{X: param(key), Type: "text"}, P: ast.PrecAdd})
}

// DeletePath returns e #- path.
func (e JSONB[V]) DeletePath(path ...string) JSONB[V] {
	return JSONB[V]{}.withNode(&ast.Binary{Op: "#-", L: e.n, R: textArray(path), P: ast.PrecOther})
}

// SetPath returns jsonb_set(e, path, json(value), true): a partial update of
// the document that creates the key when missing. When e is NULL the result
// is NULL; combine with Coalesce if the column is nullable.
func (e JSONB[V]) SetPath(path []string, value any) JSONB[V] {
	return JSONB[V]{}.withNode(&ast.Func{Name: "jsonb_set", Args: []ast.Node{e.n, textArray(path), marshalJSONB(value), ast.True}})
}

// Strip returns jsonb_strip_nulls(e).
func (e JSONB[V]) Strip() JSONB[V] { return JSONB[V]{}.withNode(call("jsonb_strip_nulls", e.n)) }

// TypeOf returns jsonb_typeof(e).
func (e JSONB[V]) TypeOf() Text[string] { return Text[string]{}.withNode(call("jsonb_typeof", e.n)) }

func jsonOp(x ast.Node, op string, key any) ast.Node {
	var r ast.Node
	switch k := key.(type) {
	case ast.Node:
		r = k
	case string:
		r = &ast.Cast{X: param(k), Type: "text"}
	case int32:
		r = &ast.Cast{X: param(k), Type: "integer"}
	default:
		r = ast.Errorf("sqlcx: internal error: bad json key %T", key)
	}
	return &ast.Binary{Op: op, L: x, R: r, P: ast.PrecOther}
}

func textArray(items []string) ast.Node {
	if items == nil {
		items = []string{}
	}
	return &ast.Cast{X: param(items), Type: "text[]"}
}

func jsonbParam(v any) ast.Node { return &ast.Cast{X: param(v), Type: "jsonb"} }

func marshalJSONB(v any) ast.Node {
	b, err := json.Marshal(v)
	if err != nil {
		return ast.ErrNode{Err: fmt.Errorf("sqlcx: marshal jsonb value: %w", err)}
	}
	// Bind the encoded document as raw JSON so that the caller's Go type
	// never reaches the driver.
	return &ast.Cast{X: param(json.RawMessage(b)), Type: "jsonb"}
}

// Array is an expression of a PostgreSQL array type. V is the Go slice type,
// E its element type.
type Array[V, E any] struct{ Comparable[V] }

func (Array[V, E]) withNode(n ast.Node) Array[V, E] { return Array[V, E]{Comparable[V]{Value[V]{n}}} }

// Contains reports e @> v (every element of v is in e).
func (e Array[V, E]) Contains(v V) Cond { return binary(e.n, "@>", param(v), ast.PrecOther) }

// ContainedBy reports e <@ v.
func (e Array[V, E]) ContainedBy(v V) Cond { return binary(e.n, "<@", param(v), ast.PrecOther) }

// Overlaps reports e && v (at least one common element).
func (e Array[V, E]) Overlaps(v V) Cond { return binary(e.n, "&&", param(v), ast.PrecOther) }

// Has reports elem = ANY(e).
func (e Array[V, E]) Has(elem E) Cond {
	return Cond{boolOf(&ast.Quantified{X: param(elem), Op: "=", Arr: e.n})}
}

// HasExpr reports elem = ANY(e) for an element expression.
func (e Array[V, E]) HasExpr(elem Typed[E]) Cond {
	return Cond{boolOf(&ast.Quantified{X: elem.node(), Op: "=", Arr: e.n})}
}

// Len returns cardinality(e), the total number of elements (0 for empty).
func (e Array[V, E]) Len() Number[int32] { return Number[int32]{}.withNode(call("cardinality", e.n)) }

// At returns e[i] (1-based, as in PostgreSQL).
func (e Array[V, E]) At(i int) Value[E] {
	return Value[E]{&ast.Raw{Parts: []string{"", "[", "]"}, Args: []ast.Node{e.n, &ast.Cast{X: param(int32(i)), Type: "integer"}}, P: ast.PrecCast}}
}

// Append returns array_append(e, elem).
func (e Array[V, E]) Append(elem E) Array[V, E] {
	return Array[V, E]{}.withNode(call("array_append", e.n, param(elem)))
}

// Prepend returns array_prepend(elem, e).
func (e Array[V, E]) Prepend(elem E) Array[V, E] {
	return Array[V, E]{}.withNode(call("array_prepend", param(elem), e.n))
}

// Remove returns array_remove(e, elem).
func (e Array[V, E]) Remove(elem E) Array[V, E] {
	return Array[V, E]{}.withNode(call("array_remove", e.n, param(elem)))
}

// Concat returns e || v.
func (e Array[V, E]) Concat(v V) Array[V, E] {
	return Array[V, E]{}.withNode(&ast.Binary{Op: "||", L: e.n, R: param(v), P: ast.PrecOther})
}

// Position returns array_position(e, elem) (NULL when absent).
func (e Array[V, E]) Position(elem E) Number[int32] {
	return Number[int32]{}.withNode(call("array_position", e.n, param(elem)))
}
