package postgres

import (
	"errors"
	"fmt"
	"strconv"

	pg "github.com/pganalyze/pg_query_go/v6"
	pgquery "github.com/wasilibs/go-pgquery"
)

// OrderTerm is an ORDER BY term lifted out of a sqlc query.
type OrderTerm struct {
	Column string
	Desc   bool
	Nulls  int // 0 default, 1 NULLS FIRST, 2 NULLS LAST
}

// Variant is a sqlc query prepared for use as a derived table: the SQL text
// split around its parameter placeholders, plus the original ORDER BY.
type Variant struct {
	Segments []string
	Params   []int
	Order    []OrderTerm
}

// AnalyzeQuery checks, on the PostgreSQL parse tree, that a sqlc query can
// be wrapped as SELECT ... FROM (<query>) AS q WHERE <dynamic> ORDER BY
// <dynamic> without changing its meaning, and prepares the template.
//
// The rewrite never edits SQL text by keyword search: the query is parsed by
// libpg_query, the ORDER BY (if any) is removed from the parse tree and the
// tree is deparsed; parameters are located with the PostgreSQL lexer.
func AnalyzeQuery(text string, outputs []string, numParams int) (*Variant, error) {
	tree, err := pgquery.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	stmts := tree.GetStmts()
	if len(stmts) != 1 {
		return nil, fmt.Errorf("expected exactly one statement, found %d", len(stmts))
	}
	raw := stmts[0]
	sel := raw.GetStmt().GetSelectStmt()
	if sel == nil {
		return nil, errors.New("only SELECT queries can be used as dynamic sources")
	}
	switch {
	case sel.GetOp() != pg.SetOperation_SETOP_NONE:
		return nil, errors.New("set operations (UNION/INTERSECT/EXCEPT) are not supported; wrap the query in a CTE or view")
	case len(sel.GetValuesLists()) > 0:
		return nil, errors.New("VALUES lists are not supported")
	case sel.GetIntoClause() != nil:
		return nil, errors.New("SELECT INTO is not supported")
	case sel.GetLimitCount() != nil || sel.GetLimitOffset() != nil:
		return nil, errors.New("the query has LIMIT/OFFSET, which would apply before the dynamic filters; remove it and use Limit/Offset on the dynamic query")
	case len(sel.GetLockingClause()) > 0:
		return nil, errors.New("row-locking clauses (FOR UPDATE/SHARE) are not supported inside a derived table")
	}

	seen := map[string]int{}
	for _, o := range outputs {
		seen[o]++
	}
	for _, o := range outputs {
		if o == "" || o == "?column?" {
			return nil, errors.New("every output column needs a name; add AS aliases")
		}
		if seen[o] > 1 {
			return nil, fmt.Errorf("output column %q appears more than once; add AS aliases", o)
		}
	}

	v := &Variant{}
	sql := statementText(text, raw)
	if sorts := sel.GetSortClause(); len(sorts) > 0 {
		if dc := sel.GetDistinctClause(); len(dc) > 0 && !(len(dc) == 1 && dc[0].GetNode() == nil) {
			return nil, errors.New("DISTINCT ON depends on the query's ORDER BY, which cannot be lifted out of a derived table")
		}
		for i, sn := range sorts {
			sb := sn.GetSortBy()
			if sb == nil {
				return nil, fmt.Errorf("ORDER BY term %d is not supported", i+1)
			}
			if len(sb.GetUseOp()) > 0 {
				return nil, fmt.Errorf("ORDER BY term %d uses USING, which is not supported", i+1)
			}
			cr := sb.GetNode().GetColumnRef()
			if cr == nil {
				return nil, fmt.Errorf("ORDER BY term %d is an expression; only references to output columns can be lifted (select the expression with an alias and order by the alias)", i+1)
			}
			fields := strs(cr.GetFields())
			if len(fields) != len(cr.GetFields()) || len(fields) == 0 {
				return nil, fmt.Errorf("ORDER BY term %d is not a plain column reference", i+1)
			}
			name := fields[len(fields)-1]
			if seen[name] != 1 {
				return nil, fmt.Errorf("ORDER BY column %q is not an output column of the query; select it so the order can be applied to the result", name)
			}
			t := OrderTerm{Column: name}
			switch sb.GetSortbyDir() {
			case pg.SortByDir_SORTBY_DESC:
				t.Desc = true
			case pg.SortByDir_SORTBY_USING:
				return nil, fmt.Errorf("ORDER BY term %d uses USING, which is not supported", i+1)
			}
			switch sb.GetSortbyNulls() {
			case pg.SortByNulls_SORTBY_NULLS_FIRST:
				t.Nulls = 1
			case pg.SortByNulls_SORTBY_NULLS_LAST:
				t.Nulls = 2
			}
			v.Order = append(v.Order, t)
		}
		sel.SortClause = nil
		out, err := pgquery.Deparse(&pg.ParseResult{Version: tree.GetVersion(), Stmts: []*pg.RawStmt{{Stmt: raw.GetStmt()}}})
		if err != nil {
			return nil, fmt.Errorf("deparse: %w", err)
		}
		sql = out
	}

	scan, err := pgquery.Scan(sql)
	if err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	last := 0
	for _, tok := range scan.GetTokens() {
		if tok.GetToken() != pg.Token_PARAM {
			continue
		}
		lit := sql[tok.GetStart():tok.GetEnd()]
		n, err := strconv.Atoi(lit[1:])
		if err != nil || n < 1 {
			return nil, fmt.Errorf("unexpected parameter token %q", lit)
		}
		if n > numParams {
			return nil, fmt.Errorf("parameter $%d exceeds the %d parameters sqlc reported", n, numParams)
		}
		v.Segments = append(v.Segments, sql[last:tok.GetStart()])
		v.Params = append(v.Params, n)
		last = int(tok.GetEnd())
	}
	v.Segments = append(v.Segments, sql[last:])
	return v, nil
}

// statementText returns the statement without surrounding comments and the
// trailing semicolon.
func statementText(text string, raw *pg.RawStmt) string {
	start := int(raw.GetStmtLocation())
	end := len(text)
	if l := int(raw.GetStmtLen()); l > 0 {
		end = start + l
	}
	return text[start:end]
}
