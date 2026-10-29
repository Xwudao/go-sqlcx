package sqlcx

import (
	"fmt"
	"strings"

	"github.com/Xwudao/go-sqlcx/internal/ast"
)

// Raw is the trust boundary for hand-written SQL. The sql text is inserted
// verbatim and MUST be a constant written by the developer; it must never
// contain user input. Every '?' outside string literals, quoted identifiers,
// comments and dollar-quoted strings is a placeholder replaced by the
// corresponding argument: an Expr is rendered in place (parenthesised), any
// other value is bound as a parameter. Write "??" for a literal '?' operator
// (for example the jsonb key-exists operator). Explicit $n placeholders are
// rejected because their numbering would collide with the builder's.
//
//	sqlcx.Raw[int64]("extract(epoch from ?)::bigint", u.CreatedAt)
func Raw[V any](sql string, args ...any) Value[V] {
	n, err := parseRaw(sql, args)
	if err != nil {
		return Value[V]{ast.ErrNode{Err: err}}
	}
	return Value[V]{n}
}

// RawCond is Raw for boolean conditions.
func RawCond(sql string, args ...any) Cond {
	n, err := parseRaw(sql, args)
	if err != nil {
		return Cond{boolOf(ast.ErrNode{Err: err})}
	}
	return Cond{boolOf(n)}
}

func argNode(a any) ast.Node {
	if e, ok := a.(Expr); ok {
		if e.node() == nil {
			return ast.Errorf("sqlcx: empty expression used as an argument")
		}
		return e.node()
	}
	return param(a)
}

func parseRaw(sql string, args []any) (ast.Node, error) {
	var parts []string
	var cur strings.Builder
	i := 0
	for i < len(sql) {
		c := sql[i]
		switch {
		case c == '\'' || c == '"':
			end := quotedEnd(sql, i, c)
			if end < 0 {
				return nil, fmt.Errorf("sqlcx: raw SQL %q has an unterminated quote", sql)
			}
			cur.WriteString(sql[i:end])
			i = end
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			end := strings.IndexByte(sql[i:], '\n')
			if end < 0 {
				end = len(sql) - i
			}
			cur.WriteString(sql[i : i+end])
			i += end
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("sqlcx: raw SQL %q has an unterminated comment", sql)
			}
			cur.WriteString(sql[i : i+2+end+2])
			i += 2 + end + 2
		case c == '$':
			if i+1 < len(sql) && sql[i+1] >= '0' && sql[i+1] <= '9' {
				return nil, fmt.Errorf("sqlcx: raw SQL %q uses an explicit $n placeholder; use ? instead", sql)
			}
			if tag, ok := dollarTag(sql, i); ok {
				end := strings.Index(sql[i+len(tag):], tag)
				if end < 0 {
					return nil, fmt.Errorf("sqlcx: raw SQL %q has an unterminated dollar-quoted string", sql)
				}
				stop := i + len(tag) + end + len(tag)
				cur.WriteString(sql[i:stop])
				i = stop
				continue
			}
			cur.WriteByte(c)
			i++
		case c == '?':
			if i+1 < len(sql) && sql[i+1] == '?' {
				cur.WriteByte('?')
				i += 2
				continue
			}
			parts = append(parts, cur.String())
			cur.Reset()
			i++
		default:
			cur.WriteByte(c)
			i++
		}
	}
	parts = append(parts, cur.String())
	if len(parts)-1 != len(args) {
		return nil, fmt.Errorf("sqlcx: raw SQL %q has %d placeholders but %d arguments", sql, len(parts)-1, len(args))
	}
	nodes := make([]ast.Node, len(args))
	for i, a := range args {
		nodes[i] = argNode(a)
	}
	return &ast.Raw{Parts: parts, Args: nodes}, nil
}

// quotedEnd returns the index just past the closing quote of the literal
// starting at i, honouring doubled quotes.
func quotedEnd(s string, i int, q byte) int {
	for j := i + 1; j < len(s); j++ {
		if s[j] == q {
			if j+1 < len(s) && s[j+1] == q {
				j++
				continue
			}
			return j + 1
		}
	}
	return -1
}

// dollarTag recognises $tag$ at position i.
func dollarTag(s string, i int) (string, bool) {
	for j := i + 1; j < len(s); j++ {
		c := s[j]
		if c == '$' {
			return s[i : j+1], true
		}
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || j > i+1 && c >= '0' && c <= '9') {
			return "", false
		}
	}
	return "", false
}
