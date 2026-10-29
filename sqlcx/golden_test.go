package sqlcx_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Xwudao/go-sqlcx/internal/testdb/db"
	"github.com/Xwudao/go-sqlcx/internal/testdb/dynamic"
	"github.com/Xwudao/go-sqlcx/sqlcx"
)

var update = flag.Bool("update", false, "rewrite golden files")

type builder interface {
	Build() (string, []any, error)
}

type countSQL struct{ q sqlcx.Select[db.User] }

func (c countSQL) Build() (string, []any, error) { return c.q.CountSQL() }

func goldenCases() map[string]builder {
	u := dynamic.Users
	o := dynamic.Orders
	m := dynamic.Users.As("m")
	a := dynamic.AuditUsers
	type Row struct {
		Name  string
		Total pgtype.Numeric
		N     int64
	}
	type Pair struct {
		User  db.User
		Order *db.Order
	}
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tree := sqlcx.RecursiveCTE("tree", "id", "depth")
	tid := sqlcx.Ref[sqlcx.Number[int32]](tree, "id")
	c := dynamic.Categories
	type Node struct{ ID, Depth int32 }
	nb := func(id, depth sqlcx.Selectable[int32]) []sqlcx.Projection[Node] {
		return []sqlcx.Projection[Node]{sqlcx.Bind(id, func(n *Node) *int32 { return &n.ID }), sqlcx.Bind(depth, func(n *Node) *int32 { return &n.Depth })}
	}
	tree = tree.As(sqlcx.UnionAll(
		c.Query().Where(c.ParentID.IsNull()).Into(nb(c.ID, sqlcx.Val[int32](0).Cast[int32]("integer"))...),
		c.Query().Join(tree, c.ParentID.EqExpr(tid)).Into(nb(c.ID, sqlcx.Ref[sqlcx.Number[int32]](tree, "depth").Add(1))...),
	))
	return map[string]builder{
		"where_basic": u.Query().Where(u.Status.Eq(db.UserStatusActive), u.Age.Gte(18)).OrderBy(u.CreatedAt.Desc()).Limit(20),
		"where_nested": u.Query().Where(
			sqlcx.Or(u.Name.ILike("%a%"), sqlcx.And(u.Age.Between(1, 9), sqlcx.Not(u.Tags.Has("x")))),
			u.Email.In(), u.Email.NotIn(), u.ID.EqAny([]int64{1, 2}),
		),
		"where_json_array": u.Query().Where(u.Meta.ContainsJSON(map[string]int{"a": 1}), u.Meta.Get("k").HasKey("x"),
			u.Meta.PathText("a", "b").Eq("c"), u.Tags.Overlaps([]string{"go"}), u.Tags.Len().Gt(1)),
		"where_subqueries": u.Query().Where(
			sqlcx.Exists(o.Query().Where(o.UserID.EqExpr(u.ID), o.Amount.Gt(pgtype.Numeric{}))),
			u.ID.InQuery(o.Query().Where(o.Quantity.Gt(1)).Values(o.UserID)),
		).Where(u.Name.Eq("after")),
		"join_self_left": u.Query().LeftJoin(m, m.ID.EqExpr(u.ManagerID)).LeftJoin(o, o.UserID.EqExpr(u.ID)).
			Into(u.Embed(func(p *Pair) *db.User { return &p.User }), o.EmbedNull(func(p *Pair) **db.Order { return &p.Order })),
		"group_having_window": u.Query().Join(o, o.UserID.EqExpr(u.ID)).GroupBy(u.Name).Having(sqlcx.Count().Gt(1)).Into(
			sqlcx.Bind(u.Name, func(r *Row) *string { return &r.Name }),
			sqlcx.Bind(sqlcx.Sum[pgtype.Numeric](o.Amount), func(r *Row) *pgtype.Numeric { return &r.Total }).As("total"),
			sqlcx.Bind(sqlcx.Over(sqlcx.Rank(), sqlcx.Window().OrderBy(sqlcx.Sum[int64](o.Quantity).Desc()).Rows(sqlcx.UnboundedPreceding(), sqlcx.CurrentRow())), func(r *Row) *int64 { return &r.N }),
		),
		"distinct_on_lock": u.Query().DistinctOn(u.Status).OrderBy(u.Status.Asc(), u.Age.Desc().NullsLast()).ForUpdate(sqlcx.SkipLocked()),
		"keyset_uniform":   u.Query().SeekAfter(u.CreatedAt.Desc().After(since), u.ID.Desc().After(10)).Limit(20),
		"sort_by_runtime":  u.Query().OrderBy(sqlcx.SortBy(u.CreatedAt, true), sqlcx.SortBy(u.Name, false)).RequireStableOrder().OrderBy(u.ID.Asc()),
		"order_if_page": u.Query().OrderBy(
			u.CreatedAt.Asc().If(false), u.CreatedAt.Desc().If(true), u.Name.Asc().NullsLast().If(true),
			u.Age.Sort(true), u.Name.Lower().Sort(false), u.ID.Asc(),
		).Page(3, 20),
		"keyset_mixed":  u.Query().SeekAfter(u.Name.Asc().After("n"), u.ID.Desc().After(10)),
		"count":         countSQL{u.Query().Where(u.Age.Gt(1)).OrderBy(u.ID.Asc()).Limit(5)},
		"cte_recursive": sqlcx.SelectFields(nb(tid, sqlcx.Ref[sqlcx.Number[int32]](tree, "depth"))...).From(tree).With(tree),
		"update": u.Update().Set(u.Name, "Tim").SetNull(u.Nickname).Increment(u.Age, 1).
			SetExpr(u.Meta, u.Meta.SetPath([]string{"a", "b"}, true)).Where(u.ID.Eq(1)),
		"update_from_returning": o.Update().SetExpr(o.Note, sqlcx.AsText(u.Name.Upper())).From(u).Where(u.ID.EqExpr(o.UserID)).Returning(),
		"insert_batch_upsert": u.Insert().Columns(u.Email, u.Name).Values(db.User{Email: "a", Name: "A"}, db.User{Email: "b", Name: "B"}).
			OnConflict(u.Email).DoUpdateExcluded(u.Name).Where(u.Status.Ne(db.UserStatusBanned)).Returning(),
		"insert_select": a.Insert().Columns(a.UserID, a.Action).FromQuery(u.Query().Into(
			sqlcx.Bind(u.ID, func(r *db.AuditUser) *int64 { return &r.UserID }),
			sqlcx.Bind(sqlcx.Val("seen"), func(r *db.AuditUser) *string { return &r.Action }))),
		"insert_default_values": a.Insert(),
		"delete_using":          o.Delete().Using(u).Where(u.ID.EqExpr(o.UserID), u.Status.Eq(db.UserStatusBanned)).Returning(),
		"schema_qualified":      a.Query().Where(a.Action.Eq("x")),
		"view_variant": dynamic.ListUserOrderTotals.Query(db.ListUserOrderTotalsParams{Status: db.UserStatusActive}).
			Where(dynamic.ListUserOrderTotals.OrderCount.Gt(2)).Limit(10),
		"view_default_order": dynamic.ListUsersByStatus.Query(db.UserStatusActive),
		"raw":                u.Query().Where(sqlcx.RawCond("? @@ to_tsquery(?) AND meta ?? 'k' AND name <> '?'", sqlcx.Raw[string]("to_tsvector(?)", u.Name), "go")),
	}
}

func TestGoldenSQL(t *testing.T) {
	for name, b := range goldenCases() {
		t.Run(name, func(t *testing.T) {
			sql, args, err := b.Build()
			if err != nil {
				t.Fatal(err)
			}
			got := sql + "\n-- args: " + fmt.Sprintf("%#v", args) + "\n"
			path := filepath.Join("testdata", "golden", name+".sql")
			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run go test -update)", err)
			}
			if got != string(want) {
				t.Fatalf("SQL changed:\n got: %s\nwant: %s", got, want)
			}
			// Rendering is deterministic and side-effect free.
			sql2, _, _ := b.Build()
			if sql2 != sql {
				t.Fatal("second render differs")
			}
			if n := strings.Count(sql, "$"); n < len(args) {
				t.Fatalf("%d placeholders for %d args", n, len(args))
			}
		})
	}
}
