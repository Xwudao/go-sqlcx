// Package ast is the internal SQL abstract syntax tree used by the public
// expr and query packages. Nodes are immutable after construction; rendering
// never mutates a node, so a tree can be shared freely between goroutines and
// rendered any number of times.
package ast

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Node is a renderable SQL fragment.
type Node interface {
	// Render appends the SQL text of the node to w. Bound values are
	// registered through w.Param, never inlined.
	Render(w *Writer)
	// Prec reports the binding strength of the node's top-level operator.
	// Atoms report PrecAtom.
	Prec() int
}

// Operator precedence levels, following the PostgreSQL documentation
// ("Operator Precedence", lowest first).
const (
	PrecOr = iota + 1
	PrecAnd
	PrecNot
	PrecIs
	PrecCompare
	PrecRange // BETWEEN, IN, LIKE, ILIKE, SIMILAR
	PrecOther // any other operator (||, @>, ->, ?, ...)
	PrecAdd
	PrecMul
	PrecExp
	PrecUnary
	PrecCast
	PrecAtom = 100
)

// MaxParams is the maximum number of bind parameters PostgreSQL accepts in a
// single statement.
const MaxParams = 65535

// Writer accumulates SQL text and bind arguments. Parameters are numbered in
// the order they are written, which matches their textual position, so nested
// subqueries are numbered correctly.
type Writer struct {
	b    []byte
	args []any
	err  error
}

// NewWriter returns a writer with preallocated buffers.
func NewWriter(sizeHint int) *Writer {
	return &Writer{b: make([]byte, 0, sizeHint)}
}

// S appends trusted SQL text (keywords, operators, punctuation).
func (w *Writer) S(s string) { w.b = append(w.b, s...) }

// B appends a single trusted byte.
func (w *Writer) B(c byte) { w.b = append(w.b, c) }

// Ident appends a double-quoted identifier.
func (w *Writer) Ident(name string) {
	if name == "" {
		w.Fail(errors.New("sqlcx: empty identifier"))
		return
	}
	if strings.IndexByte(name, 0) >= 0 {
		w.Fail(fmt.Errorf("sqlcx: identifier %q contains a NUL byte", name))
		return
	}
	w.b = append(w.b, '"')
	for i := 0; i < len(name); i++ {
		if name[i] == '"' {
			w.b = append(w.b, '"', '"')
		} else {
			w.b = append(w.b, name[i])
		}
	}
	w.b = append(w.b, '"')
}

// Param registers v as the next bind argument and writes its placeholder.
func (w *Writer) Param(v any) {
	w.args = append(w.args, v)
	w.b = append(w.b, '$')
	w.b = strconv.AppendInt(w.b, int64(len(w.args)), 10)
}

// ParamRef writes a placeholder for an already registered argument number.
func (w *Writer) ParamRef(n int) {
	w.b = append(w.b, '$')
	w.b = strconv.AppendInt(w.b, int64(n), 10)
}

// NumArgs reports the number of registered bind arguments.
func (w *Writer) NumArgs() int { return len(w.args) }

// Fail records the first rendering error.
func (w *Writer) Fail(err error) {
	if w.err == nil && err != nil {
		w.err = err
	}
}

// Err returns the first rendering error.
func (w *Writer) Err() error { return w.err }

// Node renders n, failing on nil.
func (w *Writer) Node(n Node) {
	if n == nil {
		w.Fail(errors.New("sqlcx: empty expression"))
		return
	}
	n.Render(w)
}

// Operand renders n as an operand of an operator with precedence prec,
// wrapping it in parentheses when it binds more loosely or equally.
func (w *Writer) Operand(n Node, prec int) {
	if n == nil {
		w.Fail(errors.New("sqlcx: empty expression"))
		return
	}
	if n.Prec() <= prec {
		w.B('(')
		n.Render(w)
		w.B(')')
		return
	}
	n.Render(w)
}

// List renders nodes separated by ", ".
func (w *Writer) List(nodes []Node) {
	for i, n := range nodes {
		if i > 0 {
			w.S(", ")
		}
		w.Node(n)
	}
}

// Result returns the SQL text and arguments, or the first error.
func (w *Writer) Result() (string, []any, error) {
	if w.err != nil {
		return "", nil, w.err
	}
	if len(w.args) > MaxParams {
		return "", nil, fmt.Errorf("sqlcx: statement uses %d bind parameters, PostgreSQL allows at most %d", len(w.args), MaxParams)
	}
	return string(w.b), w.args, nil
}

// Render renders a complete statement.
func Render(n Node) (string, []any, error) {
	w := NewWriter(256)
	w.Node(n)
	return w.Result()
}
