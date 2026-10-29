// Column types. Every category shares the same shape; keep them in sync.

package sqlcx

import "github.com/Xwudao/go-sqlcx/internal/ast"

// col is the column part shared by every column type.
type col[M, T any] struct {
	info *colInfo
	get  func(*M) *T
}

func (c col[M, T]) column() *colInfo    { return c.info }
func (c col[M, T]) target(m *M) any     { return c.get(m) }
func (c col[M, T]) fieldValue(m *M) any { return *c.get(m) }
func (c col[M, T]) nullHolder() nullHolder[M] {
	p := new(*T)
	get := c.get
	return nullHolder[M]{
		dst:    p,
		reset:  func() { *p = nil },
		isNull: func() bool { return *p == nil },
		commit: func(m *M) { *get(m) = **p },
	}
}

// Name returns the column name.
func (c col[M, T]) Name() string { return c.info.name }

func isNotNull[N any]() bool {
	var n N
	switch any(n).(type) {
	case NotNull:
		return true
	case Nullable:
		return false
	}
	panic("sqlcx: nullability type parameter must be NotNull or Nullable")
}

func newCol[M, T, N any](o Owner[M], name string, get func(*M) *T) (col[M, T], ast.Node) {
	info := o.owner().register(name, isNotNull[N]())
	return col[M, T]{info: info, get: get}, info.ref()
}

// ValueColumn is a column of a value expression with no equality operator (json, xml, geometric types). M is the sqlc model,
// T the model field type, V the non-NULL value type used in comparisons and
// N either NotNull or Nullable.
type ValueColumn[M, T, V, N any] struct {
	Value[V]
	col[M, T]
}

// NewValueColumn creates a column and registers it with its table. It is called
// by generated code.
func NewValueColumn[M, T, V, N any](o Owner[M], name string, get func(*M) *T) ValueColumn[M, T, V, N] {
	c, n := newCol[M, T, N](o, name, get)
	v := ValueColumn[M, T, V, N]{Value[V]{n}, c}
	o.owner().cols = append(o.owner().cols, v)
	return v
}

func (ValueColumn[M, T, V, N]) scanType(*T)     {}
func (ValueColumn[M, T, V, N]) nullability(N)   {}
func (ValueColumn[M, T, V, N]) assignTo(*M, V)  {}
func (ValueColumn[M, T, V, N]) fieldType(*M, T) {}

func (c ValueColumn[M, T, V, N]) sortBy(desc bool) OrderTerm {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Asc orders by the column ascending.
func (c ValueColumn[M, T, V, N]) Asc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n}, key: c.info.key()}
}

// Desc orders by the column descending.
func (c ValueColumn[M, T, V, N]) Desc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: true}, key: c.info.key()}
}

// Sort orders by the column, descending when desc is true; use it when the
// direction is decided at run time.
func (c ValueColumn[M, T, V, N]) Sort(desc bool) Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Excluded refers to the value proposed for insertion (EXCLUDED.column)
// inside ON CONFLICT DO UPDATE.
func (c ValueColumn[M, T, V, N]) Excluded() Value[V] {
	return Value[V]{}.withNode(ast.Excluded{Name: c.info.name})
}

// Column is a column of a comparable value (bytea, custom types). M is the sqlc model,
// T the model field type, V the non-NULL value type used in comparisons and
// N either NotNull or Nullable.
type Column[M, T, V, N any] struct {
	Comparable[V]
	col[M, T]
}

// NewColumn creates a column and registers it with its table. It is called
// by generated code.
func NewColumn[M, T, V, N any](o Owner[M], name string, get func(*M) *T) Column[M, T, V, N] {
	c, n := newCol[M, T, N](o, name, get)
	v := Column[M, T, V, N]{Comparable[V]{Value[V]{n}}, c}
	o.owner().cols = append(o.owner().cols, v)
	return v
}

func (Column[M, T, V, N]) scanType(*T)     {}
func (Column[M, T, V, N]) nullability(N)   {}
func (Column[M, T, V, N]) assignTo(*M, V)  {}
func (Column[M, T, V, N]) fieldType(*M, T) {}

func (c Column[M, T, V, N]) sortBy(desc bool) OrderTerm {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Asc orders by the column ascending.
func (c Column[M, T, V, N]) Asc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n}, key: c.info.key()}
}

// Desc orders by the column descending.
func (c Column[M, T, V, N]) Desc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: true}, key: c.info.key()}
}

// Sort orders by the column, descending when desc is true; use it when the
// direction is decided at run time.
func (c Column[M, T, V, N]) Sort(desc bool) Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Excluded refers to the value proposed for insertion (EXCLUDED.column)
// inside ON CONFLICT DO UPDATE.
func (c Column[M, T, V, N]) Excluded() Comparable[V] {
	return Comparable[V]{}.withNode(ast.Excluded{Name: c.info.name})
}

// OrderedColumn is a column of a ordered value (timestamps, dates, uuid, enums, inet). M is the sqlc model,
// T the model field type, V the non-NULL value type used in comparisons and
// N either NotNull or Nullable.
type OrderedColumn[M, T, V, N any] struct {
	Ordered[V]
	col[M, T]
}

// NewOrderedColumn creates a column and registers it with its table. It is called
// by generated code.
func NewOrderedColumn[M, T, V, N any](o Owner[M], name string, get func(*M) *T) OrderedColumn[M, T, V, N] {
	c, n := newCol[M, T, N](o, name, get)
	v := OrderedColumn[M, T, V, N]{Ordered[V]{Comparable[V]{Value[V]{n}}}, c}
	o.owner().cols = append(o.owner().cols, v)
	return v
}

func (OrderedColumn[M, T, V, N]) scanType(*T)     {}
func (OrderedColumn[M, T, V, N]) nullability(N)   {}
func (OrderedColumn[M, T, V, N]) assignTo(*M, V)  {}
func (OrderedColumn[M, T, V, N]) fieldType(*M, T) {}

func (c OrderedColumn[M, T, V, N]) sortBy(desc bool) OrderTerm {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Asc orders by the column ascending.
func (c OrderedColumn[M, T, V, N]) Asc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n}, key: c.info.key()}
}

// Desc orders by the column descending.
func (c OrderedColumn[M, T, V, N]) Desc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: true}, key: c.info.key()}
}

// Sort orders by the column, descending when desc is true; use it when the
// direction is decided at run time.
func (c OrderedColumn[M, T, V, N]) Sort(desc bool) Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Excluded refers to the value proposed for insertion (EXCLUDED.column)
// inside ON CONFLICT DO UPDATE.
func (c OrderedColumn[M, T, V, N]) Excluded() Ordered[V] {
	return Ordered[V]{}.withNode(ast.Excluded{Name: c.info.name})
}

// TextColumn is a column of a character type (text, varchar, char, citext). M is the sqlc model,
// T the model field type, V the non-NULL value type used in comparisons and
// N either NotNull or Nullable.
type TextColumn[M, T, V, N any] struct {
	Text[V]
	col[M, T]
}

// NewTextColumn creates a column and registers it with its table. It is called
// by generated code.
func NewTextColumn[M, T, V, N any](o Owner[M], name string, get func(*M) *T) TextColumn[M, T, V, N] {
	c, n := newCol[M, T, N](o, name, get)
	v := TextColumn[M, T, V, N]{Text[V]{Ordered[V]{Comparable[V]{Value[V]{n}}}}, c}
	o.owner().cols = append(o.owner().cols, v)
	return v
}

func (TextColumn[M, T, V, N]) scanType(*T)     {}
func (TextColumn[M, T, V, N]) nullability(N)   {}
func (TextColumn[M, T, V, N]) assignTo(*M, V)  {}
func (TextColumn[M, T, V, N]) fieldType(*M, T) {}

func (c TextColumn[M, T, V, N]) sortBy(desc bool) OrderTerm {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Asc orders by the column ascending.
func (c TextColumn[M, T, V, N]) Asc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n}, key: c.info.key()}
}

// Desc orders by the column descending.
func (c TextColumn[M, T, V, N]) Desc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: true}, key: c.info.key()}
}

// Sort orders by the column, descending when desc is true; use it when the
// direction is decided at run time.
func (c TextColumn[M, T, V, N]) Sort(desc bool) Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Excluded refers to the value proposed for insertion (EXCLUDED.column)
// inside ON CONFLICT DO UPDATE.
func (c TextColumn[M, T, V, N]) Excluded() Text[V] {
	return Text[V]{}.withNode(ast.Excluded{Name: c.info.name})
}

// NumberColumn is a column of a numeric type (integers, floating point, numeric). M is the sqlc model,
// T the model field type, V the non-NULL value type used in comparisons and
// N either NotNull or Nullable.
type NumberColumn[M, T, V, N any] struct {
	Number[V]
	col[M, T]
}

// NewNumberColumn creates a column and registers it with its table. It is called
// by generated code.
func NewNumberColumn[M, T, V, N any](o Owner[M], name string, get func(*M) *T) NumberColumn[M, T, V, N] {
	c, n := newCol[M, T, N](o, name, get)
	v := NumberColumn[M, T, V, N]{Number[V]{Ordered[V]{Comparable[V]{Value[V]{n}}}}, c}
	o.owner().cols = append(o.owner().cols, v)
	return v
}

func (NumberColumn[M, T, V, N]) scanType(*T)     {}
func (NumberColumn[M, T, V, N]) nullability(N)   {}
func (NumberColumn[M, T, V, N]) assignTo(*M, V)  {}
func (NumberColumn[M, T, V, N]) fieldType(*M, T) {}

func (c NumberColumn[M, T, V, N]) sortBy(desc bool) OrderTerm {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Asc orders by the column ascending.
func (c NumberColumn[M, T, V, N]) Asc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n}, key: c.info.key()}
}

// Desc orders by the column descending.
func (c NumberColumn[M, T, V, N]) Desc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: true}, key: c.info.key()}
}

// Sort orders by the column, descending when desc is true; use it when the
// direction is decided at run time.
func (c NumberColumn[M, T, V, N]) Sort(desc bool) Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Excluded refers to the value proposed for insertion (EXCLUDED.column)
// inside ON CONFLICT DO UPDATE.
func (c NumberColumn[M, T, V, N]) Excluded() Number[V] {
	return Number[V]{}.withNode(ast.Excluded{Name: c.info.name})
}

// BoolColumn is a column of a boolean. M is the sqlc model,
// T the model field type, V the non-NULL value type used in comparisons and
// N either NotNull or Nullable.
type BoolColumn[M, T, V, N any] struct {
	Boolean[V]
	col[M, T]
}

// NewBoolColumn creates a column and registers it with its table. It is called
// by generated code.
func NewBoolColumn[M, T, V, N any](o Owner[M], name string, get func(*M) *T) BoolColumn[M, T, V, N] {
	c, n := newCol[M, T, N](o, name, get)
	v := BoolColumn[M, T, V, N]{Boolean[V]{Comparable[V]{Value[V]{n}}}, c}
	o.owner().cols = append(o.owner().cols, v)
	return v
}

func (BoolColumn[M, T, V, N]) scanType(*T)     {}
func (BoolColumn[M, T, V, N]) nullability(N)   {}
func (BoolColumn[M, T, V, N]) assignTo(*M, V)  {}
func (BoolColumn[M, T, V, N]) fieldType(*M, T) {}

func (c BoolColumn[M, T, V, N]) sortBy(desc bool) OrderTerm {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Asc orders by the column ascending.
func (c BoolColumn[M, T, V, N]) Asc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n}, key: c.info.key()}
}

// Desc orders by the column descending.
func (c BoolColumn[M, T, V, N]) Desc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: true}, key: c.info.key()}
}

// Sort orders by the column, descending when desc is true; use it when the
// direction is decided at run time.
func (c BoolColumn[M, T, V, N]) Sort(desc bool) Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Excluded refers to the value proposed for insertion (EXCLUDED.column)
// inside ON CONFLICT DO UPDATE.
func (c BoolColumn[M, T, V, N]) Excluded() Boolean[V] {
	return Boolean[V]{}.withNode(ast.Excluded{Name: c.info.name})
}

// JSONColumn is a column of a json. M is the sqlc model,
// T the model field type, V the non-NULL value type used in comparisons and
// N either NotNull or Nullable.
type JSONColumn[M, T, V, N any] struct {
	JSON[V]
	col[M, T]
}

// NewJSONColumn creates a column and registers it with its table. It is called
// by generated code.
func NewJSONColumn[M, T, V, N any](o Owner[M], name string, get func(*M) *T) JSONColumn[M, T, V, N] {
	c, n := newCol[M, T, N](o, name, get)
	v := JSONColumn[M, T, V, N]{JSON[V]{Value[V]{n}}, c}
	o.owner().cols = append(o.owner().cols, v)
	return v
}

func (JSONColumn[M, T, V, N]) scanType(*T)     {}
func (JSONColumn[M, T, V, N]) nullability(N)   {}
func (JSONColumn[M, T, V, N]) assignTo(*M, V)  {}
func (JSONColumn[M, T, V, N]) fieldType(*M, T) {}

func (c JSONColumn[M, T, V, N]) sortBy(desc bool) OrderTerm {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Asc orders by the column ascending.
func (c JSONColumn[M, T, V, N]) Asc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n}, key: c.info.key()}
}

// Desc orders by the column descending.
func (c JSONColumn[M, T, V, N]) Desc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: true}, key: c.info.key()}
}

// Sort orders by the column, descending when desc is true; use it when the
// direction is decided at run time.
func (c JSONColumn[M, T, V, N]) Sort(desc bool) Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Excluded refers to the value proposed for insertion (EXCLUDED.column)
// inside ON CONFLICT DO UPDATE.
func (c JSONColumn[M, T, V, N]) Excluded() JSON[V] {
	return JSON[V]{}.withNode(ast.Excluded{Name: c.info.name})
}

// JSONBColumn is a column of a jsonb. M is the sqlc model,
// T the model field type, V the non-NULL value type used in comparisons and
// N either NotNull or Nullable.
type JSONBColumn[M, T, V, N any] struct {
	JSONB[V]
	col[M, T]
}

// NewJSONBColumn creates a column and registers it with its table. It is called
// by generated code.
func NewJSONBColumn[M, T, V, N any](o Owner[M], name string, get func(*M) *T) JSONBColumn[M, T, V, N] {
	c, n := newCol[M, T, N](o, name, get)
	v := JSONBColumn[M, T, V, N]{JSONB[V]{Comparable[V]{Value[V]{n}}}, c}
	o.owner().cols = append(o.owner().cols, v)
	return v
}

func (JSONBColumn[M, T, V, N]) scanType(*T)     {}
func (JSONBColumn[M, T, V, N]) nullability(N)   {}
func (JSONBColumn[M, T, V, N]) assignTo(*M, V)  {}
func (JSONBColumn[M, T, V, N]) fieldType(*M, T) {}

func (c JSONBColumn[M, T, V, N]) sortBy(desc bool) OrderTerm {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Asc orders by the column ascending.
func (c JSONBColumn[M, T, V, N]) Asc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n}, key: c.info.key()}
}

// Desc orders by the column descending.
func (c JSONBColumn[M, T, V, N]) Desc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: true}, key: c.info.key()}
}

// Sort orders by the column, descending when desc is true; use it when the
// direction is decided at run time.
func (c JSONBColumn[M, T, V, N]) Sort(desc bool) Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Excluded refers to the value proposed for insertion (EXCLUDED.column)
// inside ON CONFLICT DO UPDATE.
func (c JSONBColumn[M, T, V, N]) Excluded() JSONB[V] {
	return JSONB[V]{}.withNode(ast.Excluded{Name: c.info.name})
}

// ArrayColumn is a column of an array type. M is the sqlc model, T the model
// field type, V the slice type used in comparisons, E the element type and
// N either NotNull or Nullable.
type ArrayColumn[M, T, V, E, N any] struct {
	Array[V, E]
	col[M, T]
}

// NewArrayColumn creates a column and registers it with its table. It is
// called by generated code.
func NewArrayColumn[M, T, V, E, N any](o Owner[M], name string, get func(*M) *T) ArrayColumn[M, T, V, E, N] {
	c, n := newCol[M, T, N](o, name, get)
	v := ArrayColumn[M, T, V, E, N]{Array[V, E]{Comparable[V]{Value[V]{n}}}, c}
	o.owner().cols = append(o.owner().cols, v)
	return v
}

func (ArrayColumn[M, T, V, E, N]) scanType(*T)     {}
func (ArrayColumn[M, T, V, E, N]) nullability(N)   {}
func (ArrayColumn[M, T, V, E, N]) assignTo(*M, V)  {}
func (ArrayColumn[M, T, V, E, N]) fieldType(*M, T) {}

func (c ArrayColumn[M, T, V, E, N]) sortBy(desc bool) OrderTerm {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Asc orders by the column ascending.
func (c ArrayColumn[M, T, V, E, N]) Asc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n}, key: c.info.key()}
}

// Desc orders by the column descending.
func (c ArrayColumn[M, T, V, E, N]) Desc() Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: true}, key: c.info.key()}
}

// Sort orders by the column, descending when desc is true; use it when the
// direction is decided at run time.
func (c ArrayColumn[M, T, V, E, N]) Sort(desc bool) Order[V] {
	return Order[V]{item: ast.OrderItem{X: c.n, Desc: desc}, key: c.info.key()}
}

// Excluded refers to the value proposed for insertion (EXCLUDED.column)
// inside ON CONFLICT DO UPDATE.
func (c ArrayColumn[M, T, V, E, N]) Excluded() Array[V, E] {
	return Array[V, E]{}.withNode(ast.Excluded{Name: c.info.name})
}
