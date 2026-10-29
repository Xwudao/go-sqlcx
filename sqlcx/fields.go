package sqlcx

import (
	"github.com/Xwudao/go-sqlcx/internal/ast"
)

// rowBinding holds the scan targets of one projection for one destination
// row, plus optional per-row reset and post-scan hooks.
type rowBinding struct {
	targets []any
	reset   func()
	post    func() error
}

// Projection is a set of output columns together with the code that scans
// them into a row of type R. Field and FieldSet implement it.
type Projection[R any] interface {
	selectItems() []ast.SelectItem
	bindRow(r *R) rowBinding
}

// Field binds one output expression to one field of R.
type Field[R any] struct {
	item ast.SelectItem
	bind func(r *R) rowBinding
}

func (f Field[R]) selectItems() []ast.SelectItem { return []ast.SelectItem{f.item} }
func (f Field[R]) bindRow(r *R) rowBinding       { return f.bind(r) }

// As sets the output column alias.
func (f Field[R]) As(alias string) Field[R] {
	if err := ast.ValidateAlias(alias); err != nil {
		f.item.X = ast.ErrNode{Err: err}
		return f
	}
	f.item.Alias = alias
	return f
}

// Bind binds expression e to the field returned by ptr. The field type must
// equal the expression's scan type, which the compiler checks:
//
//	sqlcx.Bind(u.Name, func(r *Summary) *string { return &r.Name })
func Bind[R, S any](e Selectable[S], ptr func(*R) *S) Field[R] {
	return Field[R]{
		item: ast.SelectItem{X: exprNode(e)},
		bind: func(r *R) rowBinding { return rowBinding{targets: []any{ptr(r)}} },
	}
}

// BindNull binds expression e to a pointer field; NULL scans as nil. Use it
// for columns of outer-joined tables and for aggregates over empty input.
func BindNull[R, S any](e Selectable[S], ptr func(*R) **S) Field[R] {
	return Field[R]{
		item: ast.SelectItem{X: exprNode(e)},
		bind: func(r *R) rowBinding { return rowBinding{targets: []any{ptr(r)}} },
	}
}

// BindScanner binds expression e to a custom scan target, for example a
// type implementing sql.Scanner or a pgtype value.
func BindScanner[R any](e Expr, target func(*R) any) Field[R] {
	return Field[R]{
		item: ast.SelectItem{X: exprNode(e)},
		bind: func(r *R) rowBinding { return rowBinding{targets: []any{target(r)}} },
	}
}

// FieldSet is a group of output columns scanned together (Embed, EmbedNull).
type FieldSet[R any] struct {
	items []ast.SelectItem
	bind  func(r *R) rowBinding
}

func (f FieldSet[R]) selectItems() []ast.SelectItem { return f.items }
func (f FieldSet[R]) bindRow(r *R) rowBinding       { return f.bind(r) }

func exprNode(e Expr) ast.Node {
	if e == nil || e.node() == nil {
		return ast.Errorf("sqlcx: empty expression in select list")
	}
	return e.node()
}

// scanPlan binds a list of projections to a destination row.
type scanPlan[R any] struct {
	targets []any
	resets  []func()
	posts   []func() error
}

func bindPlan[R any](ps []Projection[R], r *R) scanPlan[R] {
	var p scanPlan[R]
	for _, pr := range ps {
		b := pr.bindRow(r)
		p.targets = append(p.targets, b.targets...)
		if b.reset != nil {
			p.resets = append(p.resets, b.reset)
		}
		if b.post != nil {
			p.posts = append(p.posts, b.post)
		}
	}
	return p
}

func selectItemsOf[R any](ps []Projection[R]) []ast.SelectItem {
	if len(ps) == 1 {
		return ps[0].selectItems() // never mutated by the renderer
	}
	var items []ast.SelectItem
	for _, p := range ps {
		items = append(items, p.selectItems()...)
	}
	return items
}
