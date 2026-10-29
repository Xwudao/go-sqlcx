package postgres

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
	pgquery "github.com/wasilibs/go-pgquery"
)

// TableKey identifies a table.
type TableKey struct{ Schema, Name string }

// UniqueKeys maps tables to the column lists of their primary key and
// unique constraints / non-partial unique indexes.
type UniqueKeys map[TableKey][][]string

type keyDef struct {
	name    string // constraint or index name, "" when unnamed
	columns []string
}

type keyCollector struct {
	defaultSchema string
	tables        map[TableKey][]keyDef
	indexes       map[string]TableKey // index/constraint name -> table
}

// SchemaFiles expands sqlc schema paths (files or directories) the same way
// sqlc does: directories contribute their *.sql files in lexical order,
// golang-migrate down migrations are skipped.
func SchemaFiles(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			out = append(out, p)
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		var names []string
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") || strings.HasSuffix(e.Name(), ".down.sql") {
				continue
			}
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, n := range names {
			out = append(out, filepath.Join(p, n))
		}
	}
	return out, nil
}

// stripMigrationDown removes rollback sections of goose, sql-migrate, tern
// and dbmate migrations and psql meta-commands, mirroring sqlc.
func stripMigrationDown(src string) string {
	s := bufio.NewScanner(strings.NewReader(src))
	s.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	var lines []string
	for s.Scan() {
		l := strings.ToLower(s.Text())
		if strings.HasPrefix(l, "-- +goose down") || strings.HasPrefix(l, "-- +migrate down") ||
			strings.HasPrefix(l, "---- create above / drop below ----") || strings.HasPrefix(l, "-- migrate:down") {
			break
		}
		if strings.HasPrefix(strings.TrimSpace(s.Text()), `\`) {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, s.Text())
	}
	return strings.Join(lines, "\n")
}

// ExtractUniqueKeys parses the schema files and returns the unique keys
// that are still in effect after every statement has been applied.
func ExtractUniqueKeys(files []string, defaultSchema string) (UniqueKeys, error) {
	kc := &keyCollector{defaultSchema: defaultSchema, tables: map[TableKey][]keyDef{}, indexes: map[string]TableKey{}}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		tree, err := pgquery.Parse(stripMigrationDown(string(b)))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}
		for _, raw := range tree.GetStmts() {
			kc.stmt(raw.GetStmt())
		}
	}
	out := UniqueKeys{}
	for t, defs := range kc.tables {
		for _, d := range defs {
			out[t] = append(out[t], d.columns)
		}
	}
	return out, nil
}

func (kc *keyCollector) table(rv *pg.RangeVar) TableKey {
	s := rv.GetSchemaname()
	if s == "" {
		s = kc.defaultSchema
	}
	return TableKey{Schema: s, Name: rv.GetRelname()}
}

func (kc *keyCollector) add(t TableKey, d keyDef) {
	if len(d.columns) == 0 {
		return
	}
	for _, existing := range kc.tables[t] {
		if slicesEqual(existing.columns, d.columns) {
			return
		}
	}
	kc.tables[t] = append(kc.tables[t], d)
	if d.name != "" {
		kc.indexes[d.name] = t
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func strs(nodes []*pg.Node) []string {
	var out []string
	for _, n := range nodes {
		if s := n.GetString_(); s != nil {
			out = append(out, s.GetSval())
		}
	}
	return out
}

func (kc *keyCollector) constraint(t TableKey, c *pg.Constraint, column string) {
	switch c.GetContype() {
	case pg.ConstrType_CONSTR_PRIMARY, pg.ConstrType_CONSTR_UNIQUE:
	default:
		return
	}
	if c.GetIndexname() != "" {
		// ADD CONSTRAINT ... USING INDEX: the index already registered the key.
		if it, ok := kc.indexes[c.GetIndexname()]; ok && it == t {
			for i, d := range kc.tables[t] {
				if d.name == c.GetIndexname() {
					kc.tables[t][i].name = c.GetConname()
					kc.indexes[c.GetConname()] = t
				}
			}
		}
		return
	}
	cols := strs(c.GetKeys())
	if column != "" {
		cols = []string{column}
	}
	kc.add(t, keyDef{name: c.GetConname(), columns: cols})
}

func (kc *keyCollector) dropByName(name string) {
	t, ok := kc.indexes[name]
	if !ok {
		return
	}
	defs := kc.tables[t][:0]
	for _, d := range kc.tables[t] {
		if d.name != name {
			defs = append(defs, d)
		}
	}
	kc.tables[t] = defs
	delete(kc.indexes, name)
}

func (kc *keyCollector) stmt(n *pg.Node) {
	switch {
	case n.GetCreateStmt() != nil:
		cs := n.GetCreateStmt()
		t := kc.table(cs.GetRelation())
		for _, el := range cs.GetTableElts() {
			if cd := el.GetColumnDef(); cd != nil {
				for _, cn := range cd.GetConstraints() {
					if c := cn.GetConstraint(); c != nil {
						kc.constraint(t, c, cd.GetColname())
					}
				}
			}
			if c := el.GetConstraint(); c != nil {
				kc.constraint(t, c, "")
			}
		}
	case n.GetAlterTableStmt() != nil:
		as := n.GetAlterTableStmt()
		t := kc.table(as.GetRelation())
		for _, cmdNode := range as.GetCmds() {
			cmd := cmdNode.GetAlterTableCmd()
			if cmd == nil {
				continue
			}
			switch cmd.GetSubtype() {
			case pg.AlterTableType_AT_AddConstraint:
				if c := cmd.GetDef().GetConstraint(); c != nil {
					kc.constraint(t, c, "")
				}
			case pg.AlterTableType_AT_DropConstraint:
				kc.dropByName(cmd.GetName())
			case pg.AlterTableType_AT_AddColumn:
				if cd := cmd.GetDef().GetColumnDef(); cd != nil {
					for _, cn := range cd.GetConstraints() {
						if c := cn.GetConstraint(); c != nil {
							kc.constraint(t, c, cd.GetColname())
						}
					}
				}
			case pg.AlterTableType_AT_DropColumn:
				kc.dropColumn(t, cmd.GetName())
			}
		}
	case n.GetIndexStmt() != nil:
		is := n.GetIndexStmt()
		if !is.GetUnique() || is.GetWhereClause() != nil {
			return
		}
		var cols []string
		for _, p := range is.GetIndexParams() {
			ie := p.GetIndexElem()
			if ie == nil || ie.GetName() == "" || ie.GetExpr() != nil {
				return // expression index: not a column key
			}
			cols = append(cols, ie.GetName())
		}
		kc.add(kc.table(is.GetRelation()), keyDef{name: is.GetIdxname(), columns: cols})
	case n.GetRenameStmt() != nil:
		rs := n.GetRenameStmt()
		switch rs.GetRenameType() {
		case pg.ObjectType_OBJECT_TABLE:
			from := kc.table(rs.GetRelation())
			to := TableKey{Schema: from.Schema, Name: rs.GetNewname()}
			kc.tables[to] = kc.tables[from]
			delete(kc.tables, from)
			for name, t := range kc.indexes {
				if t == from {
					kc.indexes[name] = to
				}
			}
		case pg.ObjectType_OBJECT_COLUMN:
			t := kc.table(rs.GetRelation())
			for i, d := range kc.tables[t] {
				for j, c := range d.columns {
					if c == rs.GetSubname() {
						kc.tables[t][i].columns[j] = rs.GetNewname()
					}
				}
			}
		case pg.ObjectType_OBJECT_TABCONSTRAINT, pg.ObjectType_OBJECT_INDEX:
			old := rs.GetSubname()
			if rs.GetRenameType() == pg.ObjectType_OBJECT_INDEX {
				old = rs.GetRelation().GetRelname()
			}
			if t, ok := kc.indexes[old]; ok {
				for i, d := range kc.tables[t] {
					if d.name == old {
						kc.tables[t][i].name = rs.GetNewname()
					}
				}
				delete(kc.indexes, old)
				kc.indexes[rs.GetNewname()] = t
			}
		}
	case n.GetDropStmt() != nil:
		ds := n.GetDropStmt()
		for _, obj := range ds.GetObjects() {
			parts := strs(obj.GetList().GetItems())
			if len(parts) == 0 {
				continue
			}
			switch ds.GetRemoveType() {
			case pg.ObjectType_OBJECT_TABLE:
				t := TableKey{Schema: kc.defaultSchema, Name: parts[len(parts)-1]}
				if len(parts) > 1 {
					t.Schema = parts[len(parts)-2]
				}
				delete(kc.tables, t)
			case pg.ObjectType_OBJECT_INDEX:
				kc.dropByName(parts[len(parts)-1])
			}
		}
	}
}

func (kc *keyCollector) dropColumn(t TableKey, col string) {
	defs := kc.tables[t][:0]
	for _, d := range kc.tables[t] {
		keep := true
		for _, c := range d.columns {
			if c == col {
				keep = false
			}
		}
		if keep {
			defs = append(defs, d)
		}
	}
	kc.tables[t] = defs
}
