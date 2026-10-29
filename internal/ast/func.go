package ast

import "errors"

// FuncKind classifies functions for clause validation.
type FuncKind uint8

// Function kinds.
const (
	FuncScalar FuncKind = iota
	FuncAggregate
	FuncWindow // window-only functions such as row_number()
)

// Func is a function call, optionally an aggregate with DISTINCT, ORDER BY,
// FILTER or an OVER clause. Name must have been validated.
type Func struct {
	Name     string
	Args     []Node
	Star     bool // name(*)
	Distinct bool
	OrderBy  []OrderItem
	Filter   Node
	Over     *Window
	Kind     FuncKind
}

// Render writes the call.
func (f *Func) Render(w *Writer) {
	RenderFuncName(w, f.Name)
	w.B('(')
	if f.Distinct {
		w.S("DISTINCT ")
	}
	if f.Star {
		w.B('*')
	} else {
		w.List(f.Args)
	}
	if len(f.OrderBy) > 0 {
		w.S(" ORDER BY ")
		RenderOrder(w, f.OrderBy)
	}
	w.B(')')
	if f.Filter != nil {
		w.S(" FILTER (WHERE ")
		w.Node(f.Filter)
		w.B(')')
	}
	if f.Over != nil {
		w.S(" OVER (")
		f.Over.Render(w)
		w.B(')')
	} else if f.Kind == FuncWindow {
		w.Fail(errors.New("sqlcx: window function " + f.Name + "() requires an OVER clause"))
	}
}

// Prec implements Node.
func (*Func) Prec() int { return PrecAtom }

// Nulls ordering.
const (
	NullsDefault = iota
	NullsFirst
	NullsLast
)

// OrderItem is one ORDER BY term.
type OrderItem struct {
	X     Node
	Desc  bool
	Nulls int
}

// RenderOrder writes a comma separated ORDER BY list.
func RenderOrder(w *Writer, items []OrderItem) {
	for i, o := range items {
		if i > 0 {
			w.S(", ")
		}
		w.Node(o.X)
		if o.Desc {
			w.S(" DESC")
		} else {
			w.S(" ASC")
		}
		switch o.Nulls {
		case NullsFirst:
			w.S(" NULLS FIRST")
		case NullsLast:
			w.S(" NULLS LAST")
		}
	}
}

// Window is a window specification.
type Window struct {
	PartitionBy []Node
	OrderBy     []OrderItem
	Frame       string // trusted frame clause built by the expr package
	FrameArgs   []Node // offsets referenced by Frame via "\x00"
}

// Render writes the specification body (without the surrounding parens).
func (wd *Window) Render(w *Writer) {
	sep := ""
	if len(wd.PartitionBy) > 0 {
		w.S("PARTITION BY ")
		w.List(wd.PartitionBy)
		sep = " "
	}
	if len(wd.OrderBy) > 0 {
		w.S(sep)
		w.S("ORDER BY ")
		RenderOrder(w, wd.OrderBy)
		sep = " "
	}
	if wd.Frame != "" {
		w.S(sep)
		arg := 0
		for i := 0; i < len(wd.Frame); i++ {
			if wd.Frame[i] == 0 {
				if arg >= len(wd.FrameArgs) {
					w.Fail(errors.New("sqlcx: internal error: window frame argument mismatch"))
					return
				}
				w.Operand(wd.FrameArgs[arg], PrecUnary)
				arg++
				continue
			}
			w.B(wd.Frame[i])
		}
	}
}
