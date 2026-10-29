package sqlcx

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"math"
	"slices"
	"strings"

	"github.com/Xwudao/go-sqlcx/internal/ast"
	"github.com/jackc/pgx/v5"
)

// Select is an immutable SELECT builder producing rows of type R. Every
// method returns a modified copy; a Select can be shared between goroutines
// and extended independently.
type Select[R any] struct {
	st           *ast.Select
	proj         []Projection[R]
	sources      []*relation
	orderKeys    []*columnKey
	limit        *int64
	defaultOrder bool // ORDER BY came from a sqlc query and is replaced by OrderBy
	checkStable  bool
	assumeStable bool
	seek         []SeekKey
	err          error
}

// Subquery is a statement usable inside another statement (EXISTS, IN,
// FROM, WITH, INSERT ... SELECT).
type Subquery interface {
	stmtNode() ast.Node
}

// SelectFields starts a SELECT with an explicit projection and no FROM
// clause; add one with From.
func SelectFields[R any](ps ...Projection[R]) Select[R] {
	return Select[R]{st: &ast.Select{}, proj: slices.Clone(ps)}
}

func (q Select[R]) clone() Select[R] {
	st := *q.st
	q.st = &st
	return q
}

func (q Select[R]) fail(err error) Select[R] {
	if q.err == nil {
		q.err = err
	}
	return q
}

func (q Select[R]) stmtNode() ast.Node {
	if q.err != nil {
		return ast.ErrNode{Err: q.err}
	}
	return q.render()
}

// From adds FROM items.
func (q Select[R]) From(srcs ...Source) Select[R] {
	q = q.clone()
	for _, s := range srcs {
		q.st.From = append(slices.Clip(q.st.From), s.sourceNode())
		q.sources = append(slices.Clip(q.sources), s.sourceRel())
	}
	return q
}

// Where adds conditions, ANDed with existing ones. Empty conditions are
// ignored.
func (q Select[R]) Where(conds ...Typed[bool]) Select[R] {
	c := whereAnd(q.st.Where, conds)
	q = q.clone()
	q.st.Where = c
	return q
}

// WhereIf adds conditions only when ok is true.
func (q Select[R]) WhereIf(ok bool, conds ...Typed[bool]) Select[R] {
	if !ok {
		return q
	}
	return q.Where(conds...)
}

func whereAnd(existing ast.Node, conds []Typed[bool]) ast.Node {
	all := make([]Typed[bool], 0, len(conds)+1)
	if existing != nil {
		all = append(all, Value[bool]{existing})
	}
	all = append(all, conds...)
	return And(all...).n
}

func (q Select[R]) join(kind int, src Source, on []Typed[bool]) Select[R] {
	q = q.clone()
	j := ast.Join{Kind: kind, Source: src.sourceNode()}
	if kind != ast.JoinCross {
		j.On = And(on...).n
		if j.On == nil {
			return q.fail(errors.New("sqlcx: JOIN requires at least one non-empty ON condition"))
		}
	}
	q.st.Joins = append(slices.Clip(q.st.Joins), j)
	q.sources = append(slices.Clip(q.sources), src.sourceRel())
	return q
}

// Join adds INNER JOIN src ON conds (ANDed).
func (q Select[R]) Join(src Source, on ...Typed[bool]) Select[R] {
	return q.join(ast.JoinInner, src, on)
}

// LeftJoin adds LEFT JOIN src ON conds. Columns of src are NULL for
// unmatched rows: bind them with BindNull or EmbedNull.
func (q Select[R]) LeftJoin(src Source, on ...Typed[bool]) Select[R] {
	return q.join(ast.JoinLeft, src, on)
}

// RightJoin adds RIGHT JOIN src ON conds.
func (q Select[R]) RightJoin(src Source, on ...Typed[bool]) Select[R] {
	return q.join(ast.JoinRight, src, on)
}

// FullJoin adds FULL JOIN src ON conds.
func (q Select[R]) FullJoin(src Source, on ...Typed[bool]) Select[R] {
	return q.join(ast.JoinFull, src, on)
}

// CrossJoin adds CROSS JOIN src.
func (q Select[R]) CrossJoin(src Source) Select[R] { return q.join(ast.JoinCross, src, nil) }

// With adds common table expressions.
func (q Select[R]) With(ctes ...*CTE) Select[R] {
	q = q.clone()
	q.st.With = addCTEs(q.st.With, ctes)
	return q
}

// Distinct sets SELECT DISTINCT.
func (q Select[R]) Distinct() Select[R] {
	q = q.clone()
	q.st.Distinct = true
	q.st.DistinctOn = nil
	return q
}

// DistinctOn sets SELECT DISTINCT ON (es...). PostgreSQL requires the
// leading ORDER BY terms to match.
func (q Select[R]) DistinctOn(es ...Expr) Select[R] {
	q = q.clone()
	q.st.Distinct = false
	q.st.DistinctOn = nodes(es)
	return q
}

// GroupBy adds GROUP BY expressions.
func (q Select[R]) GroupBy(es ...Expr) Select[R] {
	q = q.clone()
	q.st.GroupBy = append(slices.Clip(q.st.GroupBy), nodes(es)...)
	return q
}

// Having adds HAVING conditions (ANDed).
func (q Select[R]) Having(conds ...Typed[bool]) Select[R] {
	c := whereAnd(q.st.Having, conds)
	q = q.clone()
	q.st.Having = c
	return q
}

// OrderBy appends ORDER BY terms. The default order of a sqlc query source
// is replaced by the first call.
func (q Select[R]) OrderBy(terms ...OrderTerm) Select[R] {
	q = q.clone()
	if q.defaultOrder {
		q.st.OrderBy = nil
		q.orderKeys = nil
		q.defaultOrder = false
	}
	if q.seek != nil {
		return q.fail(errors.New("sqlcx: OrderBy cannot be combined with SeekAfter; the seek keys define the order"))
	}
	q.st.OrderBy = append(slices.Clip(q.st.OrderBy), orderItems(terms)...)
	q.orderKeys = append(slices.Clip(q.orderKeys), orderKeysOf(terms)...)
	return q
}

// Limit sets LIMIT n (bound as a parameter).
func (q Select[R]) Limit(n int64) Select[R] {
	if n < 0 {
		return q.fail(fmt.Errorf("sqlcx: negative LIMIT %d", n))
	}
	q = q.clone()
	q.st.Limit = param(n)
	q.limit = &n
	return q
}

// Offset sets OFFSET n (bound as a parameter).
func (q Select[R]) Offset(n int64) Select[R] {
	if n < 0 {
		return q.fail(fmt.Errorf("sqlcx: negative OFFSET %d", n))
	}
	q = q.clone()
	q.st.Offset = param(n)
	return q
}

// Page sets LIMIT size OFFSET (page-1)*size for 1-based page numbers;
// pages below 1 are treated as page 1.
func (q Select[R]) Page(page, size int64) Select[R] {
	if size < 0 {
		return q.fail(fmt.Errorf("sqlcx: negative page size %d", size))
	}
	page = max(page, 1)
	if size > 0 && page-1 > math.MaxInt64/size {
		return q.fail(fmt.Errorf("sqlcx: page %d of size %d overflows OFFSET", page, size))
	}
	return q.Limit(size).Offset((page - 1) * size)
}

// LockOption modifies a row-locking clause.
type LockOption func(*ast.Lock)

// NoWait adds NOWAIT.
func NoWait() LockOption { return func(l *ast.Lock) { l.Wait = "NOWAIT" } }

// SkipLocked adds SKIP LOCKED.
func SkipLocked() LockOption { return func(l *ast.Lock) { l.Wait = "SKIP LOCKED" } }

// Of restricts the lock to the given sources.
func Of(srcs ...Source) LockOption {
	return func(l *ast.Lock) {
		for _, s := range srcs {
			l.Of = append(l.Of, s.sourceRel().q)
		}
	}
}

func (q Select[R]) lock(strength string, opts []LockOption) Select[R] {
	q = q.clone()
	l := ast.Lock{Strength: strength}
	for _, o := range opts {
		o(&l)
	}
	q.st.Locks = append(slices.Clip(q.st.Locks), l)
	return q
}

// ForUpdate adds FOR UPDATE.
func (q Select[R]) ForUpdate(opts ...LockOption) Select[R] { return q.lock("UPDATE", opts) }

// ForNoKeyUpdate adds FOR NO KEY UPDATE.
func (q Select[R]) ForNoKeyUpdate(opts ...LockOption) Select[R] {
	return q.lock("NO KEY UPDATE", opts)
}

// ForShare adds FOR SHARE.
func (q Select[R]) ForShare(opts ...LockOption) Select[R] { return q.lock("SHARE", opts) }

// ForKeyShare adds FOR KEY SHARE.
func (q Select[R]) ForKeyShare(opts ...LockOption) Select[R] { return q.lock("KEY SHARE", opts) }

// Into replaces the projection, changing the row type to R2. Every other
// clause is kept:
//
//	rows, err := u.Query().Where(...).Into(
//	    sqlcx.Bind(u.ID, func(r *Summary) *int64 { return &r.ID }),
//	    sqlcx.Bind(u.Name, func(r *Summary) *string { return &r.Name }),
//	).All(ctx, db)
func (q Select[R]) Into[R2 any](ps ...Projection[R2]) Select[R2] {
	return Select[R2]{
		st:           q.st,
		proj:         slices.Clone(ps),
		sources:      q.sources,
		orderKeys:    q.orderKeys,
		limit:        q.limit,
		defaultOrder: q.defaultOrder,
		checkStable:  q.checkStable,
		assumeStable: q.assumeStable,
		seek:         q.seek,
		err:          q.err,
	}
}

// Values replaces the projection with the single expression e, producing a
// single-column query usable with InQuery, CompareAnyQuery, as a scalar
// subquery, or executed directly.
func (q Select[R]) Values[V any](e Typed[V]) Values[V] {
	return Values[V]{q: q.Into(Field[V]{
		item: ast.SelectItem{X: exprNode(e)},
		bind: func(v *V) rowBinding { return rowBinding{targets: []any{v}} },
	})}
}

// RequireStableOrder makes Build fail unless the ORDER BY covers a NOT NULL
// unique key of every source, which guarantees deterministic pagination.
func (q Select[R]) RequireStableOrder() Select[R] {
	q.checkStable = true
	return q
}

// AssumeStableOrder disables the order-stability check of SeekAfter and
// RequireStableOrder, for orders whose uniqueness the caller guarantees
// (for example over derived tables, whose keys are unknown).
func (q Select[R]) AssumeStableOrder() Select[R] {
	q.assumeStable = true
	return q
}

// SeekAfter implements keyset (cursor) pagination: it sets ORDER BY to the
// keys' orders and adds a WHERE condition selecting rows strictly after the
// given cursor values. The keys must be NOT NULL columns covering a unique
// key of every source (checked at Build) unless AssumeStableOrder is used.
//
//	q.SeekAfter(u.CreatedAt.Desc().After(cur.CreatedAt), u.ID.Desc().After(cur.ID)).Limit(20)
func (q Select[R]) SeekAfter(keys ...SeekKey) Select[R] {
	if len(keys) == 0 {
		return q.fail(errors.New("sqlcx: SeekAfter requires at least one key"))
	}
	q = q.clone()
	if q.defaultOrder {
		q.st.OrderBy = nil
		q.orderKeys = nil
		q.defaultOrder = false
	}
	if len(q.st.OrderBy) > 0 {
		return q.fail(errors.New("sqlcx: SeekAfter cannot be combined with OrderBy; the seek keys define the order"))
	}
	q.seek = slices.Clone(keys)
	terms := make([]OrderTerm, len(keys))
	for i, k := range keys {
		terms[i] = k.order
	}
	q.st.OrderBy = orderItems(terms)
	q.orderKeys = orderKeysOf(terms)
	q.st.Where = whereAnd(q.st.Where, []Typed[bool]{seekCond(keys)})
	return q
}

// seekCond builds the keyset predicate. Uniform directions use a row
// comparison, which PostgreSQL can serve from a composite index; mixed
// directions expand to (k1 > v1) OR (k1 = v1 AND k2 < v2) ...
func seekCond(keys []SeekKey) Cond {
	uniform := true
	for _, k := range keys[1:] {
		if k.order.orderItem().Desc != keys[0].order.orderItem().Desc {
			uniform = false
		}
	}
	op := func(desc bool) string {
		if desc {
			return "<"
		}
		return ">"
	}
	if uniform {
		if len(keys) == 1 {
			it := keys[0].order.orderItem()
			return compare(it.X, op(it.Desc), keys[0].val)
		}
		l := &ast.Tuple{}
		r := &ast.Tuple{}
		for _, k := range keys {
			l.Items = append(l.Items, k.order.orderItem().X)
			r.Items = append(r.Items, k.val)
		}
		return compare(l, op(keys[0].order.orderItem().Desc), r)
	}
	var ors []Typed[bool]
	for i := range keys {
		var ands []Typed[bool]
		for j := 0; j < i; j++ {
			ands = append(ands, compare(keys[j].order.orderItem().X, "=", keys[j].val))
		}
		it := keys[i].order.orderItem()
		ands = append(ands, compare(it.X, op(it.Desc), keys[i].val))
		ors = append(ors, And(ands...))
	}
	return Or(ors...)
}

func orderKeysOf(terms []OrderTerm) []*columnKey {
	out := make([]*columnKey, 0, len(terms))
	for _, t := range terms {
		if t == nil {
			continue
		}
		out = append(out, t.orderKey())
	}
	return out
}

// checkOrderStable verifies that the order covers a NOT NULL unique key of
// every source.
func checkOrderStable(sources []*relation, keys []*columnKey, nullsMustBeAbsent bool) error {
	if len(sources) == 0 {
		return errors.New("sqlcx: order stability cannot be checked for a query without sources")
	}
	for i, k := range keys {
		if k == nil {
			return fmt.Errorf("sqlcx: ORDER BY term %d is not a column, so its uniqueness is unknown (use AssumeStableOrder)", i+1)
		}
		if nullsMustBeAbsent && !k.notNull {
			return fmt.Errorf("sqlcx: keyset column %s.%s is nullable; keyset pagination requires NOT NULL keys", k.owner.display, k.name)
		}
	}
	for _, src := range sources {
		covered := false
		for _, uk := range src.stableKeys() {
			all := true
			for _, c := range uk {
				found := false
				for _, k := range keys {
					if k.owner == src && k.name == c {
						found = true
						break
					}
				}
				if !found {
					all = false
					break
				}
			}
			if all {
				covered = true
				break
			}
		}
		if !covered {
			var hint string
			if sk := src.stableKeys(); len(sk) > 0 {
				parts := make([]string, len(sk))
				for i, k := range sk {
					parts[i] = "(" + strings.Join(k, ", ") + ")"
				}
				hint = "; add one of " + strings.Join(parts, " or ")
			} else {
				hint = "; the source has no known NOT NULL unique key (use AssumeStableOrder if the order is unique)"
			}
			return fmt.Errorf("sqlcx: ORDER BY does not cover a unique key of %s%s", src.display, hint)
		}
	}
	return nil
}

func (q Select[R]) render() ast.Node {
	if q.err != nil {
		return ast.ErrNode{Err: q.err}
	}
	if !q.assumeStable && (q.checkStable || q.seek != nil) {
		if err := checkOrderStable(q.sources, q.orderKeys, q.seek != nil); err != nil {
			return ast.ErrNode{Err: err}
		}
	}
	st := *q.st
	st.Items = selectItemsOf(q.proj)
	return &st
}

func (q Select[R]) outputColumns() int { return len(selectItemsOf(q.proj)) }

// Build renders the statement.
func (q Select[R]) Build() (string, []any, error) { return ast.Render(q.render()) }

// SQL is Build without the arguments, for logging and tests.
func (q Select[R]) SQL() (string, error) {
	s, _, err := q.Build()
	return s, err
}

// All executes the query and scans every row.
func (q Select[R]) All(ctx context.Context, db DB) ([]R, error) {
	sql, args, err := q.Build()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, q.proj)
}

// AllPtrs is All returning pointers, for code written against sqlc's
// emit_result_struct_pointers option ([]*Model).
func (q Select[R]) AllPtrs(ctx context.Context, db DB) ([]*R, error) {
	rows, err := q.All(ctx, db)
	return pointers(rows), err
}

// AllAndCount returns the rows of the current page together with the total
// number of matching rows (Count, which ignores ORDER BY, LIMIT and OFFSET),
// the usual shape of a paginated list. The two statements run separately on
// db; pass a pgx.Tx with REPEATABLE READ isolation when they must see the
// same snapshot. When the total is zero the page query is skipped.
func (q Select[R]) AllAndCount(ctx context.Context, db DB) ([]R, int64, error) {
	total, err := q.Count(ctx, db)
	if err != nil || total == 0 {
		return nil, total, err
	}
	rows, err := q.All(ctx, db)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// AllPtrsAndCount is AllAndCount returning pointers (see AllPtrs).
func (q Select[R]) AllPtrsAndCount(ctx context.Context, db DB) ([]*R, int64, error) {
	rows, total, err := q.AllAndCount(ctx, db)
	return pointers(rows), total, err
}

// One executes the query and returns its only row. It returns pgx.ErrNoRows
// when there is none and ErrTooManyRows when there is more than one. A
// LIMIT 2 is added when the query has no LIMIT.
func (q Select[R]) One(ctx context.Context, db DB) (R, error) {
	if q.st.Limit == nil {
		q = q.Limit(2)
	}
	sql, args, err := q.Build()
	if err != nil {
		var zero R
		return zero, err
	}
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		var zero R
		return zero, err
	}
	return single(rows, q.proj, true)
}

// First executes the query with LIMIT 1 and returns the first row, or
// pgx.ErrNoRows. The query must have an ORDER BY; otherwise "first" is not
// well defined and First returns ErrUnordered.
func (q Select[R]) First(ctx context.Context, db DB) (R, error) {
	var zero R
	if len(q.st.OrderBy) == 0 {
		return zero, ErrUnordered
	}
	if q.limit == nil || *q.limit > 1 {
		q = q.Limit(1)
	}
	sql, args, err := q.Build()
	if err != nil {
		return zero, err
	}
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return zero, err
	}
	return single(rows, q.proj, false)
}

// Rows executes the query and yields rows one at a time. Breaking out of
// the loop closes the result set. The row value is reused only by copy, so
// yielded values may be retained.
func (q Select[R]) Rows(ctx context.Context, db DB) iter.Seq2[R, error] {
	return func(yield func(R, error) bool) {
		var zero R
		sql, args, err := q.Build()
		if err != nil {
			yield(zero, err)
			return
		}
		rows, err := db.Query(ctx, sql, args...)
		if err != nil {
			yield(zero, err)
			return
		}
		defer rows.Close()
		var r R
		plan := bindPlan(q.proj, &r)
		for rows.Next() {
			if err := scanRow(rows, &r, plan); err != nil {
				yield(zero, err)
				return
			}
			if !yield(r, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(zero, err)
		}
	}
}

// countStmt wraps the query as SELECT count(*) FROM (...) dropping ORDER BY,
// LIMIT, OFFSET and locks, which do not affect the number of matching rows
// for pagination purposes.
func (q Select[R]) countStmt() ast.Node {
	inner := q.render()
	st, ok := inner.(*ast.Select)
	if !ok {
		return inner
	}
	cp := *st
	cp.OrderBy, cp.Limit, cp.Offset, cp.Locks = nil, nil, nil, nil
	if !cp.Distinct && len(cp.DistinctOn) == 0 {
		cp.Items = []ast.SelectItem{{X: ast.Keyword("1")}}
	}
	with := cp.With
	cp.With = nil
	return &ast.Select{
		With:  with,
		Items: []ast.SelectItem{{X: &ast.Func{Name: "count", Star: true}}},
		From:  []ast.Node{&ast.DerivedSource{Sub: &cp, Alias: &ast.NamedRel{Name: "sqlcx_count"}}},
	}
}

// CountSQL renders the statement used by Count.
func (q Select[R]) CountSQL() (string, []any, error) { return ast.Render(q.countStmt()) }

// Count returns the number of rows the query matches, ignoring ORDER BY,
// LIMIT and OFFSET.
func (q Select[R]) Count(ctx context.Context, db DB) (int64, error) {
	sql, args, err := q.CountSQL()
	if err != nil {
		return 0, err
	}
	var n int64
	err = db.QueryRow(ctx, sql, args...).Scan(&n)
	return n, err
}

// ExistsSQL renders the statement used by Exists.
func (q Select[R]) ExistsSQL() (string, []any, error) {
	inner := q.render()
	if st, ok := inner.(*ast.Select); ok {
		cp := *st
		cp.Locks = nil
		inner = &cp
	}
	return ast.Render(&ast.Select{Items: []ast.SelectItem{{X: &ast.Exists{Sub: inner}}}})
}

// Exists reports whether the query matches at least one row.
func (q Select[R]) Exists(ctx context.Context, db DB) (bool, error) {
	sql, args, err := q.ExistsSQL()
	if err != nil {
		return false, err
	}
	var ok bool
	err = db.QueryRow(ctx, sql, args...).Scan(&ok)
	return ok, err
}

// Values is a single-column query of value type V.
type Values[V any] struct{ q Select[V] }

func (v Values[V]) node() ast.Node     { return &ast.Subquery{Stmt: v.q.render()} }
func (Values[V]) valueType(*V)         {}
func (Values[V]) scanType(*V)          {}
func (v Values[V]) stmtNode() ast.Node { return v.q.render() }

// Query returns the underlying Select.
func (v Values[V]) Query() Select[V] { return v.q }

// Build renders the statement.
func (v Values[V]) Build() (string, []any, error) { return v.q.Build() }

// All executes the query and returns the values.
func (v Values[V]) All(ctx context.Context, db DB) ([]V, error) { return v.q.All(ctx, db) }

// One executes the query and returns its only value.
func (v Values[V]) One(ctx context.Context, db DB) (V, error) { return v.q.One(ctx, db) }

func pointers[R any](rows []R) []*R {
	if rows == nil {
		return nil
	}
	out := make([]*R, len(rows))
	for i := range rows {
		out[i] = &rows[i]
	}
	return out
}

func scanRow[R any](rows pgx.Rows, r *R, plan scanPlan[R]) error {
	var zero R
	*r = zero
	for _, f := range plan.resets {
		f()
	}
	if err := rows.Scan(plan.targets...); err != nil {
		return err
	}
	for _, f := range plan.posts {
		if err := f(); err != nil {
			return err
		}
	}
	return nil
}

func collect[R any](rows pgx.Rows, proj []Projection[R]) ([]R, error) {
	defer rows.Close()
	var out []R
	var r R
	plan := bindPlan(proj, &r)
	for rows.Next() {
		if err := scanRow(rows, &r, plan); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func single[R any](rows pgx.Rows, proj []Projection[R], exactlyOne bool) (R, error) {
	defer rows.Close()
	var r, zero R
	plan := bindPlan(proj, &r)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return zero, err
		}
		return zero, pgx.ErrNoRows
	}
	if err := scanRow(rows, &r, plan); err != nil {
		return zero, err
	}
	if exactlyOne && rows.Next() {
		return zero, ErrTooManyRows
	}
	if err := rows.Err(); err != nil {
		return zero, err
	}
	return r, nil
}
