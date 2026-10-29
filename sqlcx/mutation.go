package sqlcx

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/Xwudao/go-sqlcx/internal/ast"
	"github.com/jackc/pgx/v5/pgconn"
)

// Assignment is one SET column = value for a table with model M.
type Assignment[M any] struct {
	a   ast.Assignment
	err error
}

// Assign is column = v with a non-NULL value.
func Assign[M, V any](c Assignable[M, V], v V) Assignment[M] {
	return Assignment[M]{a: ast.Assignment{Column: c.column().name, Value: param(v)}}
}

// AssignExpr is column = e, for expressions such as c.Add(1) or a JSONB
// SetPath.
func AssignExpr[M, V any](c Assignable[M, V], e Typed[V]) Assignment[M] {
	if e == nil || e.node() == nil {
		return Assignment[M]{err: fmt.Errorf("sqlcx: empty expression assigned to %s", c.column().name)}
	}
	return Assignment[M]{a: ast.Assignment{Column: c.column().name, Value: e.node()}}
}

// AssignNull is column = NULL. Only nullable columns are accepted.
func AssignNull[M any](c NullableColumn[M]) Assignment[M] {
	return Assignment[M]{a: ast.Assignment{Column: c.column().name, Value: ast.Null}}
}

// AssignField is column = v where v has the model field type T, for example
// a pgtype.Text taken from a sqlc model. An invalid (Valid=false) value
// stores NULL, so this is the explicit way to write a possibly-NULL value.
func AssignField[M, T any](c FieldColumn[M, T], v T) Assignment[M] {
	return Assignment[M]{a: ast.Assignment{Column: c.column().name, Value: param(v)}}
}

// AssignDefault is column = DEFAULT.
func AssignDefault[M any](c ColumnOf[M]) Assignment[M] {
	return Assignment[M]{a: ast.Assignment{Column: c.column().name, Value: ast.Default}}
}

// AssignExcluded is column = EXCLUDED.column, for ON CONFLICT DO UPDATE.
func AssignExcluded[M any](c ColumnOf[M]) Assignment[M] {
	return Assignment[M]{a: ast.Assignment{Column: c.column().name, Value: ast.Excluded{Name: c.column().name}}}
}

func checkDuplicate(set []ast.Assignment, name string) error {
	for _, a := range set {
		if a.Column == name {
			return fmt.Errorf("sqlcx: column %s assigned more than once", name)
		}
	}
	return nil
}

// ---- UPDATE ----

// Update is an immutable UPDATE builder for a table with model M.
type Update[M any] struct {
	t        *Table[M]
	st       *ast.Update
	allowAll bool
	err      error
}

func (u Update[M]) clone() Update[M] {
	st := *u.st
	u.st = &st
	return u
}

func (u Update[M]) fail(err error) Update[M] {
	if u.err == nil {
		u.err = err
	}
	return u
}

// Assign adds assignments.
func (u Update[M]) Assign(as ...Assignment[M]) Update[M] {
	u = u.clone()
	for _, a := range as {
		if a.err != nil {
			return u.fail(a.err)
		}
		if err := checkDuplicate(u.st.Set, a.a.Column); err != nil {
			return u.fail(err)
		}
		u.st.Set = append(slices.Clip(u.st.Set), a.a)
	}
	return u
}

// Set assigns a non-NULL value. The value type is checked against the
// column at compile time:
//
//	u.Update().Set(u.Name, "Tim").Set(u.Age, 30)
func (u Update[M]) Set[V any](c Assignable[M, V], v V) Update[M] { return u.Assign(Assign(c, v)) }

// SetExpr assigns an expression of the column's value type.
func (u Update[M]) SetExpr[V any](c Assignable[M, V], e Typed[V]) Update[M] {
	return u.Assign(AssignExpr(c, e))
}

// SetNull assigns NULL; only nullable columns compile.
func (u Update[M]) SetNull(c NullableColumn[M]) Update[M] { return u.Assign(AssignNull(c)) }

// SetField assigns a value of the model field type (see AssignField).
func (u Update[M]) SetField[T any](c FieldColumn[M, T], v T) Update[M] {
	return u.Assign(AssignField(c, v))
}

// SetDefault assigns DEFAULT.
func (u Update[M]) SetDefault(c ColumnOf[M]) Update[M] { return u.Assign(AssignDefault(c)) }

// Increment is column = column + by.
func (u Update[M]) Increment[V any](c interface {
	Assignable[M, V]
	Numeric
}, by V) Update[M] {
	return u.Assign(Assignment[M]{a: ast.Assignment{Column: c.column().name, Value: &ast.Binary{Op: "+", L: c.node(), R: param(by), P: ast.PrecAdd}}})
}

// Decrement is column = column - by.
func (u Update[M]) Decrement[V any](c interface {
	Assignable[M, V]
	Numeric
}, by V) Update[M] {
	return u.Assign(Assignment[M]{a: ast.Assignment{Column: c.column().name, Value: &ast.Binary{Op: "-", L: c.node(), R: param(by), P: ast.PrecAdd}}})
}

// From adds UPDATE ... FROM sources.
func (u Update[M]) From(srcs ...Source) Update[M] {
	u = u.clone()
	for _, s := range srcs {
		u.st.From = append(slices.Clip(u.st.From), s.sourceNode())
	}
	return u
}

// Where adds conditions (ANDed). Empty conditions are ignored.
func (u Update[M]) Where(conds ...Typed[bool]) Update[M] {
	c := whereAnd(u.st.Where, conds)
	u = u.clone()
	u.st.Where = c
	return u
}

// WhereIf adds conditions only when ok is true. If no condition remains,
// the statement is still refused without AllowFullTable.
func (u Update[M]) WhereIf(ok bool, conds ...Typed[bool]) Update[M] {
	if !ok {
		return u
	}
	return u.Where(conds...)
}

// With adds common table expressions.
func (u Update[M]) With(ctes ...*CTE) Update[M] {
	u = u.clone()
	u.st.With = addCTEs(u.st.With, ctes)
	return u
}

// AllowFullTable permits an UPDATE without WHERE.
func (u Update[M]) AllowFullTable() Update[M] {
	u.allowAll = true
	return u
}

func (u Update[M]) render(returning []ast.SelectItem) ast.Node {
	if u.err != nil {
		return ast.ErrNode{Err: u.err}
	}
	if u.st.Where == nil && !u.allowAll {
		return ast.ErrNode{Err: ErrNoWhere}
	}
	st := *u.st
	st.Returning = returning
	return &st
}

func (u Update[M]) stmtNode() ast.Node { return u.render(nil) }

// Build renders the statement.
func (u Update[M]) Build() (string, []any, error) { return ast.Render(u.render(nil)) }

// Exec executes the statement.
func (u Update[M]) Exec(ctx context.Context, db DB) (pgconn.CommandTag, error) {
	return execNode(ctx, db, u.render(nil))
}

// Returning adds RETURNING of every column, scanning into M.
func (u Update[M]) Returning() Returning[M] {
	return newReturning([]FieldSet[M]{u.t.self()}, func(items []ast.SelectItem) ast.Node { return u.render(items) })
}

// ReturningInto adds RETURNING with an explicit projection.
func (u Update[M]) ReturningInto[R any](ps ...Projection[R]) Returning[R] {
	return newReturning(ps, func(items []ast.SelectItem) ast.Node { return u.render(items) })
}

// ---- DELETE ----

// Delete is an immutable DELETE builder for a table with model M.
type Delete[M any] struct {
	t        *Table[M]
	st       *ast.Delete
	allowAll bool
}

func (d Delete[M]) clone() Delete[M] {
	st := *d.st
	d.st = &st
	return d
}

// Using adds DELETE ... USING sources.
func (d Delete[M]) Using(srcs ...Source) Delete[M] {
	d = d.clone()
	for _, s := range srcs {
		d.st.Using = append(slices.Clip(d.st.Using), s.sourceNode())
	}
	return d
}

// Where adds conditions (ANDed). Empty conditions are ignored.
func (d Delete[M]) Where(conds ...Typed[bool]) Delete[M] {
	c := whereAnd(d.st.Where, conds)
	d = d.clone()
	d.st.Where = c
	return d
}

// WhereIf adds conditions only when ok is true. If no condition remains,
// the statement is still refused without AllowFullTable.
func (d Delete[M]) WhereIf(ok bool, conds ...Typed[bool]) Delete[M] {
	if !ok {
		return d
	}
	return d.Where(conds...)
}

// With adds common table expressions.
func (d Delete[M]) With(ctes ...*CTE) Delete[M] {
	d = d.clone()
	d.st.With = addCTEs(d.st.With, ctes)
	return d
}

// AllowFullTable permits a DELETE without WHERE.
func (d Delete[M]) AllowFullTable() Delete[M] {
	d.allowAll = true
	return d
}

func (d Delete[M]) render(returning []ast.SelectItem) ast.Node {
	if d.st.Where == nil && !d.allowAll {
		return ast.ErrNode{Err: ErrNoWhere}
	}
	st := *d.st
	st.Returning = returning
	return &st
}

func (d Delete[M]) stmtNode() ast.Node { return d.render(nil) }

// Build renders the statement.
func (d Delete[M]) Build() (string, []any, error) { return ast.Render(d.render(nil)) }

// Exec executes the statement.
func (d Delete[M]) Exec(ctx context.Context, db DB) (pgconn.CommandTag, error) {
	return execNode(ctx, db, d.render(nil))
}

// Returning adds RETURNING of every column, scanning into M.
func (d Delete[M]) Returning() Returning[M] {
	return newReturning([]FieldSet[M]{d.t.self()}, func(items []ast.SelectItem) ast.Node { return d.render(items) })
}

// ReturningInto adds RETURNING with an explicit projection.
func (d Delete[M]) ReturningInto[R any](ps ...Projection[R]) Returning[R] {
	return newReturning(ps, func(items []ast.SelectItem) ast.Node { return d.render(items) })
}

// ---- INSERT ----

// Insert is an immutable INSERT builder for a table with model M. Rows are
// supplied either column by column for a single row (Set, SetExpr, ...),
// from models (Columns + Values), or from a query (FromQuery).
type Insert[M any] struct {
	t      *Table[M]
	st     *ast.Insert
	single bool
	models bool
	cols   []ColumnOf[M]
	err    error
}

func (in Insert[M]) clone() Insert[M] {
	st := *in.st
	in.st = &st
	return in
}

func (in Insert[M]) fail(err error) Insert[M] {
	if in.err == nil {
		in.err = err
	}
	return in
}

func (in Insert[M]) add(a Assignment[M]) Insert[M] {
	if a.err != nil {
		return in.fail(a.err)
	}
	if in.models || in.st.Query != nil {
		return in.fail(errors.New("sqlcx: Set cannot be combined with Values or FromQuery"))
	}
	for _, c := range in.st.Columns {
		if c == a.a.Column {
			return in.fail(fmt.Errorf("sqlcx: column %s inserted more than once", c))
		}
	}
	in = in.clone()
	in.single = true
	in.st.Columns = append(slices.Clip(in.st.Columns), a.a.Column)
	var row []ast.Node
	if len(in.st.Rows) == 1 {
		row = slices.Clip(in.st.Rows[0])
	}
	in.st.Rows = [][]ast.Node{append(row, a.a.Value)}
	return in
}

// Set inserts a non-NULL value into the column (single-row insert).
func (in Insert[M]) Set[V any](c Assignable[M, V], v V) Insert[M] { return in.add(Assign(c, v)) }

// SetExpr inserts an expression (single-row insert).
func (in Insert[M]) SetExpr[V any](c Assignable[M, V], e Typed[V]) Insert[M] {
	return in.add(AssignExpr(c, e))
}

// SetNull inserts NULL into a nullable column (single-row insert).
func (in Insert[M]) SetNull(c NullableColumn[M]) Insert[M] { return in.add(AssignNull(c)) }

// SetField inserts a value of the model field type (single-row insert).
func (in Insert[M]) SetField[T any](c FieldColumn[M, T], v T) Insert[M] {
	return in.add(AssignField(c, v))
}

// SetDefault inserts DEFAULT (single-row insert).
func (in Insert[M]) SetDefault(c ColumnOf[M]) Insert[M] { return in.add(AssignDefault(c)) }

// Columns selects the columns written by Values and FromQuery. Columns not
// listed receive their defaults.
func (in Insert[M]) Columns(cols ...ColumnOf[M]) Insert[M] {
	if in.single {
		return in.fail(errors.New("sqlcx: Columns cannot be combined with Set"))
	}
	if in.cols != nil {
		return in.fail(errors.New("sqlcx: Columns called more than once"))
	}
	in = in.clone()
	in.cols = slices.Clone(cols)
	in.st.Columns = make([]string, len(cols))
	seen := map[string]bool{}
	for i, c := range cols {
		name := c.column().name
		if seen[name] {
			return in.fail(fmt.Errorf("sqlcx: column %s listed more than once", name))
		}
		seen[name] = true
		in.st.Columns[i] = name
	}
	return in
}

// Values appends one row per model, reading the fields of the selected
// Columns (multi-row insert). Field values are bound as-is, so an invalid
// pgtype value or a nil pointer stores NULL.
func (in Insert[M]) Values(ms ...M) Insert[M] {
	if in.cols == nil {
		return in.fail(errors.New("sqlcx: Values requires Columns"))
	}
	if in.st.Query != nil {
		return in.fail(errors.New("sqlcx: Values cannot be combined with FromQuery"))
	}
	in = in.clone()
	in.models = true
	rows := slices.Clip(in.st.Rows)
	for i := range ms {
		row := make([]ast.Node, len(in.cols))
		for j, c := range in.cols {
			row[j] = param(c.fieldValue(&ms[i]))
		}
		rows = append(rows, row)
	}
	in.st.Rows = rows
	return in
}

// FromQuery inserts the rows of q (INSERT INTO t (columns) SELECT ...). The
// number of output columns of q must equal the number of Columns.
func (in Insert[M]) FromQuery(q interface {
	Subquery
	outputColumns() int
}) Insert[M] {
	if in.cols == nil {
		return in.fail(errors.New("sqlcx: FromQuery requires Columns"))
	}
	if in.models {
		return in.fail(errors.New("sqlcx: FromQuery cannot be combined with Values"))
	}
	if n := q.outputColumns(); n != len(in.cols) {
		return in.fail(fmt.Errorf("sqlcx: INSERT lists %d columns but the query returns %d", len(in.cols), n))
	}
	in = in.clone()
	in.st.Query = q.stmtNode()
	return in
}

// With adds common table expressions.
func (in Insert[M]) With(ctes ...*CTE) Insert[M] {
	in = in.clone()
	in.st.With = addCTEs(in.st.With, ctes)
	return in
}

// OnConflict starts an ON CONFLICT (columns) clause.
func (in Insert[M]) OnConflict(cols ...ColumnOf[M]) Conflict[M] {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.column().name
	}
	return Conflict[M]{in: in, oc: ast.OnConflict{Columns: names}}
}

// OnConflictConstraint starts an ON CONFLICT ON CONSTRAINT name clause.
func (in Insert[M]) OnConflictConstraint(name string) Conflict[M] {
	c := Conflict[M]{in: in, oc: ast.OnConflict{Constraint: name}}
	if err := ast.ValidateAlias(name); err != nil {
		c.in = c.in.fail(err)
	}
	return c
}

// OnConflictDoNothing adds ON CONFLICT DO NOTHING without a target.
func (in Insert[M]) OnConflictDoNothing() Insert[M] {
	in = in.clone()
	in.st.OnConflict = &ast.OnConflict{DoNothing: true}
	return in
}

// Conflict configures an ON CONFLICT clause.
type Conflict[M any] struct {
	in Insert[M]
	oc ast.OnConflict
}

// Where sets the conflict target predicate (for partial unique indexes).
func (c Conflict[M]) Where(conds ...Typed[bool]) Conflict[M] {
	c.oc.TargetWhere = whereAnd(c.oc.TargetWhere, conds)
	return c
}

// DoNothing finishes the clause with DO NOTHING.
func (c Conflict[M]) DoNothing() Insert[M] {
	oc := c.oc
	oc.DoNothing = true
	in := c.in.clone()
	in.st.OnConflict = &oc
	return in
}

// DoUpdate finishes the clause with DO UPDATE SET assignments (an upsert).
// Use AssignExcluded to copy the proposed values.
func (c Conflict[M]) DoUpdate(as ...Assignment[M]) Upsert[M] {
	oc := c.oc
	in := c.in
	for _, a := range as {
		if a.err != nil {
			in = in.fail(a.err)
			continue
		}
		if err := checkDuplicate(oc.Set, a.a.Column); err != nil {
			in = in.fail(err)
			continue
		}
		oc.Set = append(slices.Clip(oc.Set), a.a)
	}
	in = in.clone()
	in.st.OnConflict = &oc
	return Upsert[M]{in}
}

// DoUpdateExcluded is DoUpdate assigning EXCLUDED values to cols.
func (c Conflict[M]) DoUpdateExcluded(cols ...ColumnOf[M]) Upsert[M] {
	as := make([]Assignment[M], len(cols))
	for i, col := range cols {
		as[i] = AssignExcluded(col)
	}
	return c.DoUpdate(as...)
}

// Upsert is an INSERT with ON CONFLICT DO UPDATE.
type Upsert[M any] struct{ Insert[M] }

// Where adds the DO UPDATE ... WHERE condition: rows not matching it are
// left unchanged.
func (u Upsert[M]) Where(conds ...Typed[bool]) Upsert[M] {
	in := u.Insert.clone()
	oc := *in.st.OnConflict
	oc.Where = whereAnd(oc.Where, conds)
	in.st.OnConflict = &oc
	return Upsert[M]{in}
}

func (in Insert[M]) render(returning []ast.SelectItem) ast.Node {
	if in.err != nil {
		return ast.ErrNode{Err: in.err}
	}
	st := *in.st
	if in.cols != nil && !in.models && st.Query == nil {
		return ast.ErrNode{Err: errors.New("sqlcx: Columns requires Values or FromQuery")}
	}
	st.Returning = returning
	return &st
}

func (in Insert[M]) stmtNode() ast.Node { return in.render(nil) }

// Build renders the statement.
func (in Insert[M]) Build() (string, []any, error) { return ast.Render(in.render(nil)) }

// Exec executes the statement.
func (in Insert[M]) Exec(ctx context.Context, db DB) (pgconn.CommandTag, error) {
	return execNode(ctx, db, in.render(nil))
}

// Returning adds RETURNING of every column, scanning into M.
func (in Insert[M]) Returning() Returning[M] {
	return newReturning([]FieldSet[M]{in.t.self()}, func(items []ast.SelectItem) ast.Node { return in.render(items) })
}

// ReturningInto adds RETURNING with an explicit projection.
func (in Insert[M]) ReturningInto[R any](ps ...Projection[R]) Returning[R] {
	return newReturning(ps, func(items []ast.SelectItem) ast.Node { return in.render(items) })
}

// ---- RETURNING ----

// Returning is a data-modifying statement with a RETURNING clause producing
// rows of type R.
type Returning[R any] struct {
	proj   []Projection[R]
	render func([]ast.SelectItem) ast.Node
}

func newReturning[R any, P Projection[R]](ps []P, render func([]ast.SelectItem) ast.Node) Returning[R] {
	proj := make([]Projection[R], len(ps))
	for i, p := range ps {
		proj[i] = p
	}
	return Returning[R]{proj: proj, render: render}
}

func (r Returning[R]) stmtNode() ast.Node { return r.render(selectItemsOf(r.proj)) }

func (r Returning[R]) outputColumns() int { return len(selectItemsOf(r.proj)) }

// Build renders the statement.
func (r Returning[R]) Build() (string, []any, error) { return ast.Render(r.stmtNode()) }

// All executes the statement and scans every returned row.
func (r Returning[R]) All(ctx context.Context, db DB) ([]R, error) {
	sql, args, err := r.Build()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, r.proj)
}

// AllPtrs is All returning pointers (see Select.AllPtrs).
func (r Returning[R]) AllPtrs(ctx context.Context, db DB) ([]*R, error) {
	rows, err := r.All(ctx, db)
	return pointers(rows), err
}

// One executes the statement and returns its only returned row
// (pgx.ErrNoRows when none, ErrTooManyRows when several).
func (r Returning[R]) One(ctx context.Context, db DB) (R, error) {
	sql, args, err := r.Build()
	if err != nil {
		var zero R
		return zero, err
	}
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		var zero R
		return zero, err
	}
	return single(rows, r.proj, true)
}

func execNode(ctx context.Context, db DB, n ast.Node) (pgconn.CommandTag, error) {
	sql, args, err := ast.Render(n)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	return db.Exec(ctx, sql, args...)
}
