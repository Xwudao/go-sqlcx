package sqlcx

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Xwudao/go-sqlcx/internal/ast"
)

// OrderTerm is one ORDER BY term.
type OrderTerm interface {
	orderItem() ast.OrderItem
	orderKey() *columnKey
}

// columnKey identifies a column of a relation for order-stability checks.
type columnKey struct {
	owner   *relation
	name    string
	notNull bool
}

// Order is an ORDER BY term over an expression of value type V.
type Order[V any] struct {
	item ast.OrderItem
	key  *columnKey
}

func (o Order[V]) orderItem() ast.OrderItem { return o.item }
func (o Order[V]) orderKey() *columnKey     { return o.key }

// NullsFirst sorts NULL values first.
func (o Order[V]) NullsFirst() Order[V] {
	o.item.Nulls = ast.NullsFirst
	return o
}

// NullsLast sorts NULL values last.
func (o Order[V]) NullsLast() Order[V] {
	o.item.Nulls = ast.NullsLast
	return o
}

// After returns a keyset-pagination key: rows strictly after v in this
// order. See Select.SeekAfter.
func (o Order[V]) After(v V) SeekKey { return SeekKey{order: o, val: param(v)} }

// SeekKey is one component of a keyset pagination cursor.
type SeekKey struct {
	order OrderTerm
	val   ast.Node
}

func orderItems(terms []OrderTerm) []ast.OrderItem {
	if len(terms) == 0 {
		return nil
	}
	out := make([]ast.OrderItem, 0, len(terms))
	for _, t := range terms {
		if t == nil {
			continue
		}
		out = append(out, t.orderItem())
	}
	return out
}

// Sortable is an expression that a Sorter may order by.
type Sortable interface {
	Expr
	sortBy(desc bool) OrderTerm
}

// SortBy orders by s ascending, or descending when desc is true. It is the
// building block for direction chosen at run time:
//
//	sqlcx.SortBy(u.CreatedAt, req.Order == "desc")
func SortBy(s Sortable, desc bool) OrderTerm { return s.sortBy(desc) }

// Sorter maps externally supplied sort keys (for example an HTTP query
// parameter) onto a fixed whitelist of expressions. Unknown keys are an
// error, so user input never reaches an identifier position.
type Sorter struct {
	fields     map[string]Sortable
	tiebreaker []OrderTerm
	maxTerms   int
}

// NewSorter returns a Sorter over the given whitelist.
func NewSorter(fields map[string]Sortable) *Sorter {
	cp := make(map[string]Sortable, len(fields))
	for k, v := range fields {
		cp[k] = v
	}
	return &Sorter{fields: cp, maxTerms: 8}
}

// Tiebreaker sets terms appended after the parsed terms (typically the
// primary key) so that the resulting order is total.
func (s *Sorter) Tiebreaker(terms ...OrderTerm) *Sorter {
	cp := *s
	cp.tiebreaker = append([]OrderTerm(nil), terms...)
	return &cp
}

// MaxTerms limits the number of user-supplied terms (default 8).
func (s *Sorter) MaxTerms(n int) *Sorter {
	cp := *s
	cp.maxTerms = n
	return &cp
}

// Keys lists the accepted keys in sorted order.
func (s *Sorter) Keys() []string {
	keys := make([]string, 0, len(s.fields))
	for k := range s.fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SortSpec is one requested sort key.
type SortSpec struct {
	Key   string
	Desc  bool
	Nulls string // "", "first" or "last"
}

// Parse parses a comma separated list such as "-created_at,name" (a
// leading '-' means descending, '+' or nothing ascending). An optional
// ":nulls_first" / ":nulls_last" suffix sets NULL placement.
func (s *Sorter) Parse(spec string) ([]OrderTerm, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return s.Specs(nil)
	}
	var specs []SortSpec
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("sqlcx: empty sort key in %q", spec)
		}
		var ss SortSpec
		switch part[0] {
		case '-':
			ss.Desc = true
			part = part[1:]
		case '+':
			part = part[1:]
		}
		if key, nulls, ok := strings.Cut(part, ":"); ok {
			part = key
			switch nulls {
			case "nulls_first":
				ss.Nulls = "first"
			case "nulls_last":
				ss.Nulls = "last"
			default:
				return nil, fmt.Errorf("sqlcx: unknown sort modifier %q", nulls)
			}
		}
		ss.Key = part
		specs = append(specs, ss)
	}
	return s.Specs(specs)
}

// Specs validates structured sort requests against the whitelist.
func (s *Sorter) Specs(specs []SortSpec) ([]OrderTerm, error) {
	if s.maxTerms > 0 && len(specs) > s.maxTerms {
		return nil, fmt.Errorf("sqlcx: too many sort keys (%d > %d)", len(specs), s.maxTerms)
	}
	seen := make(map[string]bool, len(specs))
	terms := make([]OrderTerm, 0, len(specs)+len(s.tiebreaker))
	for _, ss := range specs {
		f, ok := s.fields[ss.Key]
		if !ok {
			return nil, fmt.Errorf("sqlcx: unknown sort key %q (allowed: %s)", ss.Key, strings.Join(s.Keys(), ", "))
		}
		if seen[ss.Key] {
			return nil, fmt.Errorf("sqlcx: duplicate sort key %q", ss.Key)
		}
		seen[ss.Key] = true
		t := f.sortBy(ss.Desc)
		item := t.orderItem()
		switch ss.Nulls {
		case "":
		case "first":
			item.Nulls = ast.NullsFirst
		case "last":
			item.Nulls = ast.NullsLast
		default:
			return nil, fmt.Errorf("sqlcx: unknown NULL placement %q", ss.Nulls)
		}
		terms = append(terms, rawOrder{item: item, key: t.orderKey()})
	}
	for _, tb := range s.tiebreaker {
		dup := false
		if k := tb.orderKey(); k != nil {
			for _, t := range terms {
				if tk := t.orderKey(); tk != nil && tk.owner == k.owner && tk.name == k.name {
					dup = true
				}
			}
		}
		if !dup {
			terms = append(terms, tb)
		}
	}
	return terms, nil
}

type rawOrder struct {
	item ast.OrderItem
	key  *columnKey
}

func (o rawOrder) orderItem() ast.OrderItem { return o.item }
func (o rawOrder) orderKey() *columnKey     { return o.key }
