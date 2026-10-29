package itest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Xwudao/go-sqlcx/internal/testdb/db"
	"github.com/Xwudao/go-sqlcx/internal/testdb/dynamic"
	"github.com/Xwudao/go-sqlcx/sqlcx"
)

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func numeric(t *testing.T, s string) pgtype.Numeric {
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil {
		t.Fatal(err)
	}
	return n
}

// seed creates a small fixed data set and returns the users in insertion order.
func seed(t *testing.T, ctx context.Context, d db.DBTX) []db.User {
	t.Helper()
	q := db.New(d)
	u := dynamic.Users
	specs := []struct {
		email, name string
		status      db.UserStatus
		age         int32
		tags        []string
		meta        string
	}{
		{"ann@example.com", "Ann", db.UserStatusActive, 31, []string{"go", "sql"}, `{"plan":"pro","flags":{"beta":true}}`},
		{"bob@example.com", "Bob", db.UserStatusActive, 17, []string{"js"}, `{"plan":"free"}`},
		{"cat@example.com", "Cat", db.UserStatusInactive, 45, []string{"go"}, ``},
		{"dan@example.com", "Dan_O'Neil", db.UserStatusBanned, 0, nil, `{"plan":"pro"}`},
	}
	var out []db.User
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, s := range specs {
		created := must[db.User](t)(q.CreateUser(ctx, db.CreateUserParams{Email: s.email, Name: s.name, Status: s.status}))
		upd := u.Update().
			SetExpr(u.CreatedAt, sqlcx.Val(base.Add(time.Duration(i)*24*time.Hour))).
			Set(u.Balance, numeric(t, []string{"10.50", "0", "99.99", "5"}[i])).
			Where(u.ID.Eq(created.ID))
		if s.age != 0 {
			upd = upd.Set(u.Age, s.age)
		}
		if s.tags != nil {
			upd = upd.Set(u.Tags, s.tags)
		}
		if s.meta != "" {
			upd = upd.Set(u.Meta, []byte(s.meta))
		}
		got := must[db.User](t)(upd.Returning().One(ctx, d))
		out = append(out, got)
	}
	// Dan reports to Ann.
	if _, err := u.Update().Set(u.ManagerID, out[0].ID).Where(u.ID.Eq(out[3].ID)).Exec(ctx, d); err != nil {
		t.Fatal(err)
	}
	out[3].ManagerID = pgtype.Int8{Int64: out[0].ID, Valid: true}
	for i, amt := range []string{"10.00", "25.50", "7.25"} {
		uid := out[0].ID
		if i == 2 {
			uid = out[1].ID
		}
		must[db.Order](t)(q.CreateOrder(ctx, db.CreateOrderParams{UserID: uid, Amount: numeric(t, amt), Quantity: int32(i + 1)}))
	}
	return out
}

func names(us []db.User) []string {
	var out []string
	for _, u := range us {
		out = append(out, u.Name)
	}
	return out
}

func TestModelScannerMatchesSqlc(t *testing.T) {
	tx(t, func(ctx context.Context, d pgx.Tx) {
		users := seed(t, ctx, d)
		q := db.New(d)
		u := dynamic.Users
		for _, want := range users {
			native := must[db.User](t)(q.GetUser(ctx, want.ID))
			dyn := must[db.User](t)(u.Query().Where(u.ID.Eq(want.ID)).One(ctx, d))
			if !reflect.DeepEqual(native, dyn) {
				t.Fatalf("dynamic scan differs from sqlc scan:\nsqlc: %+v\ndyn:  %+v", native, dyn)
			}
		}
		// Another schema, same table name, time.Time override.
		if err := q.AddAuditEntry(ctx, db.AddAuditEntryParams{UserID: users[0].ID, Action: "login"}); err != nil {
			t.Fatal(err)
		}
		a := dynamic.AuditUsers
		entry := must[db.AuditUser](t)(a.Query().Where(a.Action.Eq("login"), a.At.Lte(time.Now().Add(time.Hour))).One(ctx, d))
		if entry.UserID != users[0].ID || entry.At.IsZero() {
			t.Fatalf("audit entry: %+v", entry)
		}
		// The sqlc query variant scans into the same model as the sqlc method.
		nativeList := must[[]db.User](t)(q.ListUsersByStatus(ctx, db.UserStatusActive))
		dynList := must[[]db.User](t)(dynamic.ListUsersByStatus.Query(db.UserStatusActive).All(ctx, d))
		if !reflect.DeepEqual(nativeList, dynList) {
			t.Fatalf("variant differs from sqlc query:\nsqlc: %v\ndyn:  %v", names(nativeList), names(dynList))
		}
	})
}

func TestDynamicWhere(t *testing.T) {
	tx(t, func(ctx context.Context, d pgx.Tx) {
		users := seed(t, ctx, d)
		u := dynamic.Users
		o := dynamic.Orders
		cases := []struct {
			name string
			cond sqlcx.Cond
			want []string
		}{
			{"eq enum", u.Status.Eq(db.UserStatusActive), []string{"Ann", "Bob"}},
			{"ne", u.Status.Ne(db.UserStatusActive), []string{"Cat", "Dan_O'Neil"}},
			{"gte nullable", u.Age.Gte(18), []string{"Ann", "Cat"}},
			{"is null", u.Age.IsNull(), []string{"Dan_O'Neil"}},
			{"between", u.Age.Between(17, 31), []string{"Ann", "Bob"}},
			{"not between", u.Age.NotBetween(17, 31), []string{"Cat"}},
			{"in", u.Name.In("Ann", "Cat", "nobody"), []string{"Ann", "Cat"}},
			{"empty in", u.Name.In(), nil},
			{"empty not in", u.Name.NotIn(), []string{"Ann", "Bob", "Cat", "Dan_O'Neil"}},
			{"not in", u.Name.NotIn("Ann"), []string{"Bob", "Cat", "Dan_O'Neil"}},
			{"like", u.Email.Like("%@example.com"), []string{"Ann", "Bob", "Cat", "Dan_O'Neil"}},
			{"ilike", u.Name.ILike("a%"), []string{"Ann"}},
			{"not like", u.Name.NotLike("%n%"), []string{"Bob", "Cat"}},
			{"contains escapes _", u.Name.Contains("n_O"), []string{"Dan_O'Neil"}},
			{"contains escapes literally", u.Name.Contains("_"), []string{"Dan_O'Neil"}},
			{"or + not", sqlcx.Or(u.Name.Eq("Ann"), sqlcx.Not(u.Age.Lt(40))), []string{"Ann", "Cat"}},
			{"nested", sqlcx.And(u.Status.Eq(db.UserStatusActive), sqlcx.Or(u.Age.Gt(30), u.Tags.Has("js"))), []string{"Ann", "Bob"}},
			{"distinct from", u.Age.IsDistinctFrom(17), []string{"Ann", "Cat", "Dan_O'Neil"}},
			{"eq any", u.Name.EqAny([]string{"Bob", "Dan_O'Neil"}), []string{"Bob", "Dan_O'Neil"}},
			{"ne all", u.Name.NeAll([]string{"Bob", "Dan_O'Neil"}), []string{"Ann", "Cat"}},
			{"gt any", u.Age.CompareAny(sqlcx.OpGt, []int32{40, 100}), []string{"Cat"}},
			{"array contains", u.Tags.Contains([]string{"go"}), []string{"Ann", "Cat"}},
			{"array overlaps", u.Tags.Overlaps([]string{"js", "sql"}), []string{"Ann", "Bob"}},
			{"array len", u.Tags.Len().Eq(0), []string{"Dan_O'Neil"}},
			{"jsonb contains", u.Meta.ContainsJSON(map[string]string{"plan": "pro"}), []string{"Ann", "Dan_O'Neil"}},
			{"jsonb key", u.Meta.Get("flags").HasKey("beta"), []string{"Ann"}},
			{"jsonb text", u.Meta.GetText("plan").Eq("free"), []string{"Bob"}},
			{"jsonb path", u.Meta.PathText("flags", "beta").Eq("true"), []string{"Ann"}},
			{"jsonpath", u.Meta.PathExists(`$.flags.beta ? (@ == true)`), []string{"Ann"}},
			{"has any key", u.Meta.HasAnyKey("flags", "nope"), []string{"Ann"}},
			{"numeric", u.Balance.Gt(numeric(t, "6")), []string{"Ann", "Cat"}},
			{"timestamp", u.CreatedAt.Gte(time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)), []string{"Cat", "Dan_O'Neil"}},
			{"json get", u.Settings.GetText("x").IsNull(), []string{"Ann", "Bob", "Cat", "Dan_O'Neil"}},
			{"date", u.Birthday.IsNull(), []string{"Ann", "Bob", "Cat", "Dan_O'Neil"}},
			{"uuid", u.ExternalID.Eq(users[1].ExternalID.Bytes), []string{"Bob"}},
			{"nullable enum", u.PrevStatus.IsNull(), []string{"Ann", "Bob", "Cat", "Dan_O'Neil"}},
			{"pointer override", u.Nickname.IsNull(), []string{"Ann", "Bob", "Cat", "Dan_O'Neil"}},
			{"bool column", sqlcx.Not(u.Administrator), []string{"Ann", "Bob", "Cat", "Dan_O'Neil"}},
			{"exists", sqlcx.Exists(o.Query().Where(o.UserID.EqExpr(u.ID))), []string{"Ann", "Bob"}},
			{"not exists", sqlcx.NotExists(o.Query().Where(o.UserID.EqExpr(u.ID))), []string{"Cat", "Dan_O'Neil"}},
			{"in subquery", u.ID.InQuery(o.Query().Where(o.Quantity.Gte(2)).Values(o.UserID)), []string{"Ann", "Bob"}},
			{"scalar subquery", u.Balance.LtExpr(o.Query().Values(sqlcx.Max(o.Amount))), []string{"Ann", "Bob", "Dan_O'Neil"}},
			{"all subquery", u.ID.CompareAllQuery(sqlcx.OpNe, o.Query().Values(o.UserID)), []string{"Cat", "Dan_O'Neil"}},
			{"cast", u.Age.Cast[int64]("bigint").IsNotNull(), []string{"Ann", "Bob", "Cat"}},
			{"cast to text", sqlcx.AsText(u.Status.Cast[string]("text")).HasPrefix("act"), []string{"Ann", "Bob"}},
			{"raw", sqlcx.RawCond("length(?) > ?", u.Name, 3), []string{"Dan_O'Neil"}},
			{"optional nil", sqlcx.Optional[string](nil, u.Name.Eq), []string{"Ann", "Bob", "Cat", "Dan_O'Neil"}},
			{"if false", sqlcx.If(false, u.Name.Eq("x")), []string{"Ann", "Bob", "Cat", "Dan_O'Neil"}},
			{"or of empties", sqlcx.Or(sqlcx.Cond{}, sqlcx.Cond{}), []string{"Ann", "Bob", "Cat", "Dan_O'Neil"}},
			{"case", sqlcx.AsText(sqlcx.Case[string]().When(u.Age.Lt(18), "minor").Else("adult")).Eq("minor"), []string{"Bob"}},
			{"coalesce", sqlcx.AsNumber(sqlcx.CoalesceVal(u.Age, 0)).Eq(0), []string{"Dan_O'Neil"}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				got, err := u.Query().Where(c.cond).OrderBy(u.ID.Asc()).All(ctx, d)
				if err != nil {
					sql, _, _ := u.Query().Where(c.cond).Build()
					t.Fatalf("%v\n%s", err, sql)
				}
				if !reflect.DeepEqual(names(got), c.want) {
					t.Fatalf("got %v, want %v", names(got), c.want)
				}
			})
		}
	})
}

func TestDynamicComposition(t *testing.T) {
	tx(t, func(ctx context.Context, d pgx.Tx) {
		seed(t, ctx, d)
		u := dynamic.Users
		type req struct {
			Name   string
			Status *db.UserStatus
			IDs    []int64
		}
		active := db.UserStatusActive
		run := func(r req) []string {
			q := u.Query()
			if r.Name != "" {
				q = q.Where(u.Name.ContainsFold(r.Name))
			}
			q = q.Where(sqlcx.Optional(r.Status, u.Status.Eq), sqlcx.IfAny(r.IDs, u.ID.In))
			return names(must[[]db.User](t)(q.OrderBy(u.Name.Asc()).All(ctx, d)))
		}
		if got := run(req{}); len(got) != 4 {
			t.Fatalf("no filters: %v", got)
		}
		if got := run(req{Name: "a", Status: &active}); !reflect.DeepEqual(got, []string{"Ann"}) {
			t.Fatalf("filters: %v", got)
		}
		// Inline optional conditions: Cond.If and WhereIf.
		inline := func(r req, minAge int32) []string {
			q := u.Query().
				Where(
					u.Name.ContainsFold(r.Name).If(r.Name != ""),
					u.Age.Gte(minAge).If(minAge > 0),
					sqlcx.Optional(r.Status, u.Status.Eq),
				).
				WhereIf(len(r.IDs) > 0, u.ID.In(r.IDs...))
			return names(must[[]db.User](t)(q.OrderBy(u.Name.Asc()).All(ctx, d)))
		}
		if got := inline(req{}, 0); len(got) != 4 {
			t.Fatalf("inline, no filters: %v", got)
		}
		if got := inline(req{Name: "a", Status: &active}, 18); !reflect.DeepEqual(got, []string{"Ann"}) {
			t.Fatalf("inline filters: %v", got)
		}
		if _, err := u.Update().Set(u.Name, "x").WhereIf(false, u.ID.Eq(1)).Exec(ctx, d); !errors.Is(err, sqlcx.ErrNoWhere) {
			t.Fatalf("WhereIf(false) must keep the full-table guard: %v", err)
		}
		// A shared base query is not mutated by derived queries.
		base := u.Query().Where(u.Status.Eq(db.UserStatusActive))
		a := base.Where(u.Name.Eq("Ann"))
		_ = base.Where(u.Name.Eq("Bob"))
		if got := must[[]db.User](t)(a.All(ctx, d)); !reflect.DeepEqual(names(got), []string{"Ann"}) {
			t.Fatalf("shared base: %v", names(got))
		}
	})
}

func TestProjectionJoinsAndAggregates(t *testing.T) {
	tx(t, func(ctx context.Context, d pgx.Tx) {
		users := seed(t, ctx, d)
		u := dynamic.Users
		o := dynamic.Orders
		m := dynamic.Users.As("manager")

		// DTO projection with aggregates, GROUP BY and HAVING.
		type Total struct {
			Name  string
			Count int64
			Sum   pgtype.Numeric
		}
		totals := must[[]Total](t)(u.Query().
			Join(o, o.UserID.EqExpr(u.ID)).
			GroupBy(u.ID, u.Name).
			Having(sqlcx.Count().Gte(1)).
			OrderBy(u.Name.Asc()).
			Into(
				sqlcx.Bind(u.Name, func(r *Total) *string { return &r.Name }),
				sqlcx.Bind(sqlcx.Count(), func(r *Total) *int64 { return &r.Count }),
				sqlcx.Bind(sqlcx.Sum[pgtype.Numeric](o.Amount), func(r *Total) *pgtype.Numeric { return &r.Sum }),
			).All(ctx, d))
		if len(totals) != 2 || totals[0].Name != "Ann" || totals[0].Count != 2 || totals[1].Count != 1 {
			t.Fatalf("totals: %+v", totals)
		}
		if v, _ := totals[0].Sum.Value(); v != "35.50" {
			t.Fatalf("sum: %v", v)
		}

		// Self join + LEFT JOIN with a nullable embedded model.
		type WithManager struct {
			User    db.User
			Manager *db.User
		}
		rows := must[[]WithManager](t)(u.Query().
			LeftJoin(m, m.ID.EqExpr(u.ManagerID)).
			OrderBy(u.ID.Asc()).
			Into(
				u.Embed(func(r *WithManager) *db.User { return &r.User }),
				m.EmbedNull(func(r *WithManager) **db.User { return &r.Manager }),
			).All(ctx, d))
		if len(rows) != 4 || rows[0].Manager != nil || rows[3].Manager == nil || rows[3].Manager.ID != users[0].ID {
			t.Fatalf("self join: %+v", rows)
		}
		if !reflect.DeepEqual(rows[3].User, users[3]) || !reflect.DeepEqual(*rows[3].Manager, users[0]) {
			t.Fatal("embedded models differ from sqlc models")
		}

		// Nullable scalar from an outer join.
		type Pair struct {
			Name    string
			OrderID *int64
		}
		pairs := must[[]Pair](t)(u.Query().LeftJoin(o, o.UserID.EqExpr(u.ID)).
			OrderBy(u.ID.Asc(), o.ID.Asc().NullsLast()).
			Into(sqlcx.Bind(u.Name, func(r *Pair) *string { return &r.Name }),
				sqlcx.BindNull(o.ID, func(r *Pair) **int64 { return &r.OrderID })).All(ctx, d))
		if len(pairs) != 5 || pairs[4].OrderID != nil || pairs[0].OrderID == nil {
			t.Fatalf("pairs: %+v", pairs)
		}
		// Scanning NULL into a non-pointer target is an explicit error, never a zero value.
		_, err := u.Query().LeftJoin(o, o.UserID.EqExpr(u.ID)).
			Into(sqlcx.Bind(o.ID, func(r *Pair) *int64 { return new(int64) })).All(ctx, d)
		if err == nil {
			t.Fatal("expected an error scanning NULL into int64")
		}

		// Window function, DISTINCT ON, RIGHT/FULL/CROSS joins.
		type Ranked struct {
			Name string
			Rank int64
		}
		ranked := must[[]Ranked](t)(u.Query().OrderBy(u.ID.Asc()).Into(
			sqlcx.Bind(u.Name, func(r *Ranked) *string { return &r.Name }),
			sqlcx.Bind(sqlcx.Over(sqlcx.RowNumber(), sqlcx.Window().PartitionBy(u.Status).OrderBy(u.Age.Desc().NullsLast())), func(r *Ranked) *int64 { return &r.Rank }),
		).All(ctx, d))
		if ranked[0].Rank != 1 || ranked[1].Rank != 2 {
			t.Fatalf("ranked: %+v", ranked)
		}
		first := must[[]db.User](t)(u.Query().DistinctOn(u.Status).OrderBy(u.Status.Asc(), u.Age.Desc().NullsLast()).All(ctx, d))
		if len(first) != 3 {
			t.Fatalf("distinct on: %v", names(first))
		}
		n := must[int64](t)(u.Query().FullJoin(o, o.UserID.EqExpr(u.ID)).Count(ctx, d))
		if n != 5 {
			t.Fatalf("full join count %d", n)
		}
		n = must[int64](t)(o.Query().RightJoin(u, o.UserID.EqExpr(u.ID)).Count(ctx, d))
		if n != 5 {
			t.Fatalf("right join count %d", n)
		}
		n = must[int64](t)(u.Query().CrossJoin(m).Count(ctx, d))
		if n != 16 {
			t.Fatalf("cross join count %d", n)
		}
		ok := must[bool](t)(u.Query().Where(u.Name.Eq("Ann")).Exists(ctx, d))
		if !ok {
			t.Fatal("exists")
		}
		// Distinct count.
		n = must[int64](t)(u.Query().Distinct().Into(sqlcx.Bind(u.Status, func(s *db.UserStatus) *db.UserStatus { return s })).Count(ctx, d))
		if n != 3 {
			t.Fatalf("distinct count %d", n)
		}
		vals := must[[]string](t)(u.Query().Where(u.Status.Eq(db.UserStatusActive)).OrderBy(u.Name.Desc()).Values(u.Name).All(ctx, d))
		if !reflect.DeepEqual(vals, []string{"Bob", "Ann"}) {
			t.Fatalf("values: %v", vals)
		}
	})
}

func TestCTEAndDerived(t *testing.T) {
	tx(t, func(ctx context.Context, d pgx.Tx) {
		seed(t, ctx, d)
		c := dynamic.Categories
		ins := c.Insert().Columns(c.ID, c.ParentID, c.Name, c.Slug).Values(
			db.Category{ID: 1, Name: "root", Slug: "root"},
			db.Category{ID: 2, ParentID: pgtype.Int4{Int32: 1, Valid: true}, Name: "child", Slug: "child"},
			db.Category{ID: 3, ParentID: pgtype.Int4{Int32: 2, Valid: true}, Name: "grandchild", Slug: "grandchild"},
			db.Category{ID: 4, Name: "other", Slug: "other"},
		)
		if tag := must[pgconnTag](t)(execTag(ins.Exec(ctx, d))); tag != 4 {
			t.Fatalf("inserted %d", tag)
		}

		// Recursive CTE: all descendants of root with depth.
		type Node struct {
			ID    int32
			Depth int32
		}
		tree := sqlcx.RecursiveCTE("tree", "id", "depth")
		tid := sqlcx.Ref[sqlcx.Number[int32]](tree, "id")
		tdepth := sqlcx.Ref[sqlcx.Number[int32]](tree, "depth")
		bind := func(id, depth sqlcx.Selectable[int32]) []sqlcx.Projection[Node] {
			return []sqlcx.Projection[Node]{
				sqlcx.Bind(id, func(n *Node) *int32 { return &n.ID }),
				sqlcx.Bind(depth, func(n *Node) *int32 { return &n.Depth }),
			}
		}
		base := c.Query().Where(c.ID.Eq(1)).Into(bind(c.ID, sqlcx.Val[int32](0).Cast[int32]("integer"))...)
		step := c.Query().Join(tree, c.ParentID.EqExpr(tid)).Into(bind(c.ID, tdepth.Add(1))...)
		tree = tree.As(sqlcx.UnionAll(base, step))
		nodes := must[[]Node](t)(sqlcx.SelectFields(bind(tid, tdepth)...).From(tree).With(tree).OrderBy(tid.Asc()).All(ctx, d))
		if !reflect.DeepEqual(nodes, []Node{{1, 0}, {2, 1}, {3, 2}}) {
			t.Fatalf("tree: %+v", nodes)
		}

		// Derived table and a plain CTE.
		u := dynamic.Users
		o := dynamic.Orders
		type Spend struct {
			UserID int64
			Total  pgtype.Numeric
		}
		spendQ := o.Query().GroupBy(o.UserID).Into(
			sqlcx.Bind(o.UserID, func(s *Spend) *int64 { return &s.UserID }),
			sqlcx.Bind(sqlcx.Sum[pgtype.Numeric](o.Amount), func(s *Spend) *pgtype.Numeric { return &s.Total }).As("total"),
		)
		spend := sqlcx.Derived(spendQ, "spend")
		sUser := sqlcx.Ref[sqlcx.Number[int64]](spend, "user_id")
		sTotal := sqlcx.Ref[sqlcx.Number[pgtype.Numeric]](spend, "total")
		big := must[[]db.User](t)(u.Query().Join(spend, sUser.EqExpr(u.ID)).Where(sTotal.Gt(numeric(t, "20"))).All(ctx, d))
		if !reflect.DeepEqual(names(big), []string{"Ann"}) {
			t.Fatalf("derived: %v", names(big))
		}
		cte := sqlcx.NewCTE("spend", spendQ)
		cUser := sqlcx.Ref[sqlcx.Number[int64]](cte, "user_id")
		withCTE := must[[]db.User](t)(u.Query().With(cte).Join(cte, cUser.EqExpr(u.ID)).OrderBy(u.ID.Asc()).All(ctx, d))
		if len(withCTE) != 2 {
			t.Fatalf("cte: %v", names(withCTE))
		}
		// LATERAL: latest order per user.
		latest := sqlcx.Lateral(o.Query().Where(o.UserID.EqExpr(u.ID)).OrderBy(o.ID.Desc()).Limit(1).Values(o.Amount), "latest")
		cnt := must[int64](t)(u.Query().Join(latest, sqlcx.True()).Count(ctx, d))
		if cnt != 2 {
			t.Fatalf("lateral: %d", cnt)
		}
	})
}

type pgconnTag = int64

func execTag(tag interface{ RowsAffected() int64 }, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func TestMutations(t *testing.T) {
	tx(t, func(ctx context.Context, d pgx.Tx) {
		users := seed(t, ctx, d)
		u := dynamic.Users
		o := dynamic.Orders
		a := dynamic.AuditUsers

		// UPDATE: dynamic SET, NULL, increments, JSONB partial update, RETURNING.
		got := must[db.User](t)(u.Update().
			Set(u.Name, "Annie").
			SetNull(u.Score).
			Set(u.Nickname, "an").
			Increment(u.Age, 1).
			SetExpr(u.Meta, u.Meta.SetPath([]string{"flags", "beta"}, false)).
			SetExpr(u.Tags, u.Tags.Append("rust")).
			Set(u.PrevStatus, db.UserStatusInactive).
			Where(u.ID.Eq(users[0].ID)).
			Returning().One(ctx, d))
		var meta map[string]any
		_ = json.Unmarshal(got.Meta, &meta)
		if got.Name != "Annie" || got.Age.Int32 != 32 || got.Nickname == nil || *got.Nickname != "an" ||
			meta["flags"].(map[string]any)["beta"] != false || got.Tags[2] != "rust" ||
			got.PrevStatus != (db.NullUserStatus{UserStatus: db.UserStatusInactive, Valid: true}) {
			t.Fatalf("update: %+v %v", got, meta)
		}
		// Explicit NULL through the model field type.
		got = must[db.User](t)(u.Update().SetField(u.PrevStatus, db.NullUserStatus{}).SetField(u.Nickname, nil).
			Where(u.ID.Eq(users[0].ID)).Returning().One(ctx, d))
		if got.PrevStatus.Valid || got.Nickname != nil {
			t.Fatalf("set field null: %+v", got)
		}
		// Guards.
		if _, err := u.Update().Set(u.Name, "x").Exec(ctx, d); !errors.Is(err, sqlcx.ErrNoWhere) {
			t.Fatalf("update without where: %v", err)
		}
		if _, err := u.Update().Set(u.Name, "x").Where(sqlcx.Optional[int64](nil, u.ID.Eq)).Exec(ctx, d); !errors.Is(err, sqlcx.ErrNoWhere) {
			t.Fatalf("update with only empty conditions must be guarded: %v", err)
		}
		tag := must[int64](t)(execTag(u.Update().Increment(u.Balance, numeric(t, "1")).AllowFullTable().Exec(ctx, d)))
		if tag != 4 {
			t.Fatalf("full update %d", tag)
		}
		// UPDATE ... FROM.
		tag = must[int64](t)(execTag(o.Update().SetExpr(o.Note, sqlcx.AsText(u.Name.Lower())).From(u).Where(u.ID.EqExpr(o.UserID)).Exec(ctx, d)))
		if tag != 3 {
			t.Fatalf("update from %d", tag)
		}

		// INSERT: single row, batch, upsert, INSERT ... SELECT.
		ins := must[db.User](t)(u.Insert().Set(u.Email, "eve@example.com").Set(u.Name, "Eve").Set(u.Tags, []string{"a"}).SetNull(u.Age).Returning().One(ctx, d))
		if ins.ID == 0 || ins.Status != db.UserStatusActive || ins.Age.Valid {
			t.Fatalf("insert: %+v", ins)
		}
		batch := must[[]db.User](t)(u.Insert().Columns(u.Email, u.Name, u.Status).Values(
			db.User{Email: "f@example.com", Name: "F", Status: db.UserStatusBanned},
			db.User{Email: "g@example.com", Name: "G", Status: db.UserStatusActive},
		).Returning().All(ctx, d))
		if len(batch) != 2 || batch[0].Status != db.UserStatusBanned {
			t.Fatalf("batch: %+v", batch)
		}
		up := must[db.User](t)(u.Insert().Set(u.Email, "eve@example.com").Set(u.Name, "Eve2").
			OnConflict(u.Email).DoUpdateExcluded(u.Name).Returning().One(ctx, d))
		if up.ID != ins.ID || up.Name != "Eve2" {
			t.Fatalf("upsert: %+v", up)
		}
		// DO UPDATE ... WHERE that does not match leaves the row untouched and returns nothing.
		if _, err := u.Insert().Set(u.Email, "eve@example.com").Set(u.Name, "Eve3").
			OnConflict(u.Email).DoUpdate(sqlcx.AssignExcluded(u.Name)).Where(u.Name.Eq("nobody")).Returning().One(ctx, d); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("conditional upsert: %v", err)
		}
		if _, err := u.Insert().Set(u.Email, "eve@example.com").Set(u.Name, "x").OnConflict(u.Email).DoNothing().Exec(ctx, d); err != nil {
			t.Fatal(err)
		}
		type AuditRow struct {
			UserID int64
			Action string
		}
		tag = must[int64](t)(execTag(a.Insert().Columns(a.UserID, a.Action).FromQuery(
			u.Query().Where(u.Status.Eq(db.UserStatusActive)).Into(
				sqlcx.Bind(u.ID, func(r *AuditRow) *int64 { return &r.UserID }),
				sqlcx.Bind(sqlcx.Val("seen"), func(r *AuditRow) *string { return &r.Action }),
			)).Exec(ctx, d)))
		if tag != 4 {
			t.Fatalf("insert select %d", tag)
		}

		// DELETE: guard, USING, RETURNING.
		if _, err := o.Delete().Exec(ctx, d); !errors.Is(err, sqlcx.ErrNoWhere) {
			t.Fatalf("delete without where: %v", err)
		}
		deleted := must[[]db.Order](t)(o.Delete().Using(u).Where(u.ID.EqExpr(o.UserID), u.Name.Eq("Bob")).Returning().All(ctx, d))
		if len(deleted) != 1 || deleted[0].UserID != users[1].ID {
			t.Fatalf("delete using: %+v", deleted)
		}
		type IDOnly struct{ ID int64 }
		ids := must[[]IDOnly](t)(a.Delete().AllowFullTable().ReturningInto(sqlcx.Bind(a.ID, func(r *IDOnly) *int64 { return &r.ID })).All(ctx, d))
		if len(ids) != 4 {
			t.Fatalf("delete all: %d", len(ids))
		}
	})
}

func TestPaginationAndErrors(t *testing.T) {
	tx(t, func(ctx context.Context, d pgx.Tx) {
		seed(t, ctx, d)
		u := dynamic.Users
		page1 := must[[]db.User](t)(u.Query().OrderBy(u.CreatedAt.Desc(), u.ID.Desc()).RequireStableOrder().Limit(2).All(ctx, d))
		last := page1[len(page1)-1]
		page2 := must[[]db.User](t)(u.Query().SeekAfter(u.CreatedAt.Desc().After(last.CreatedAt.Time), u.ID.Desc().After(last.ID)).Limit(2).All(ctx, d))
		if !reflect.DeepEqual(names(page1), []string{"Dan_O'Neil", "Cat"}) || !reflect.DeepEqual(names(page2), []string{"Bob", "Ann"}) {
			t.Fatalf("pages: %v %v", names(page1), names(page2))
		}
		// Mixed directions.
		mixed := must[[]db.User](t)(u.Query().SeekAfter(u.Status.Asc().After(db.UserStatusActive), u.ID.Desc().After(1<<62)).All(ctx, d))
		if len(mixed) != 4 {
			t.Fatalf("mixed keyset: %v", names(mixed))
		}
		// Page + AllAndCount: the total ignores LIMIT/OFFSET.
		pageRows, total, err := u.Query().Where(u.Name.Ne("Bob")).OrderBy(u.ID.Asc()).Page(2, 2).AllAndCount(ctx, d)
		if err != nil || total != 3 || !reflect.DeepEqual(names(pageRows), []string{"Dan_O'Neil"}) {
			t.Fatalf("AllAndCount: %v %d %v", names(pageRows), total, err)
		}
		ptrRows, total, err := u.Query().OrderBy(u.ID.Asc()).Page(0, 1).AllPtrsAndCount(ctx, d)
		if err != nil || total != 4 || len(ptrRows) != 1 || ptrRows[0].Name != "Ann" {
			t.Fatalf("AllPtrsAndCount: %v %d %v", ptrRows, total, err)
		}
		none, total, err := u.Query().Where(u.ID.Eq(-1)).Page(1, 10).AllAndCount(ctx, d)
		if err != nil || total != 0 || none != nil {
			t.Fatalf("AllAndCount empty: %v %d %v", none, total, err)
		}
		if _, _, err := u.Query().Page(1, -1).Build(); err == nil {
			t.Fatal("negative page size accepted")
		}
		// Optional order terms.
		dir := "desc"
		byAge := must[[]db.User](t)(u.Query().Where(u.Age.IsNotNull()).OrderBy(
			u.Age.Asc().If(dir == "asc"), u.Age.Desc().If(dir == "desc"), u.ID.Asc(),
		).All(ctx, d))
		if !reflect.DeepEqual(names(byAge), []string{"Cat", "Ann", "Bob"}) {
			t.Fatalf("Order.If: %v", names(byAge))
		}
		// Runtime direction on a column keeps the column identity for the
		// stability check.
		sorted := must[[]db.User](t)(u.Query().OrderBy(u.CreatedAt.Sort(true), u.ID.Sort(false)).RequireStableOrder().All(ctx, d))
		if !reflect.DeepEqual(names(sorted), []string{"Dan_O'Neil", "Cat", "Bob", "Ann"}) {
			t.Fatalf("Sort: %v", names(sorted))
		}
		offset := must[[]db.User](t)(u.Query().OrderBy(u.ID.Asc()).Limit(2).Offset(2).All(ctx, d))
		if !reflect.DeepEqual(names(offset), []string{"Cat", "Dan_O'Neil"}) {
			t.Fatalf("offset: %v", names(offset))
		}
		if _, err := u.Query().SeekAfter(u.CreatedAt.Desc().After(time.Now())).All(ctx, d); err == nil || !strings.Contains(err.Error(), "unique key") {
			t.Fatalf("unstable keyset must fail: %v", err)
		}
		if _, err := u.Query().SeekAfter(u.Age.Asc().After(1), u.ID.Asc().After(1)).All(ctx, d); err == nil || !strings.Contains(err.Error(), "nullable") {
			t.Fatalf("nullable keyset must fail: %v", err)
		}
		if _, err := u.Query().First(ctx, d); !errors.Is(err, sqlcx.ErrUnordered) {
			t.Fatalf("first without order: %v", err)
		}
		if _, err := u.Query().One(ctx, d); !errors.Is(err, sqlcx.ErrTooManyRows) {
			t.Fatalf("one: %v", err)
		}
		if _, err := u.Query().Where(u.ID.Eq(-1)).One(ctx, d); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("one none: %v", err)
		}
		f := must[db.User](t)(u.Query().OrderBy(u.Name.Desc()).First(ctx, d))
		if f.Name != "Dan_O'Neil" {
			t.Fatal(f.Name)
		}
		var seen []string
		for row, err := range u.Query().OrderBy(u.ID.Asc()).Rows(ctx, d) {
			if err != nil {
				t.Fatal(err)
			}
			seen = append(seen, row.Name)
			if len(seen) == 2 {
				break
			}
		}
		if len(seen) != 2 {
			t.Fatal(seen)
		}
		// The connection is usable after breaking out of Rows.
		if must[int64](t)(u.Query().Count(ctx, d)) != 4 {
			t.Fatal("count")
		}
		// pgx errors propagate unchanged.
		_, err = u.Insert().Set(u.Email, "ann@example.com").Set(u.Name, "dup").Exec(ctx, d)
		var pgErr interface{ SQLState() string }
		if !errors.As(err, &pgErr) || pgErr.SQLState() != "23505" {
			t.Fatalf("expected unique violation, got %v", err)
		}
	})
}

func TestQueryVariants(t *testing.T) {
	tx(t, func(ctx context.Context, d pgx.Tx) {
		seed(t, ctx, d)
		q := db.New(d)
		lv := dynamic.ListUsersByStatus
		// Default order from the sqlc query is kept until OrderBy is called.
		def := must[[]db.User](t)(lv.Query(db.UserStatusActive).All(ctx, d))
		if !reflect.DeepEqual(names(def), []string{"Bob", "Ann"}) {
			t.Fatalf("default order: %v", names(def))
		}
		filtered := must[[]db.User](t)(lv.Query(db.UserStatusActive).Where(lv.Age.Gte(18)).OrderBy(lv.Name.Asc()).All(ctx, d))
		if !reflect.DeepEqual(names(filtered), []string{"Ann"}) {
			t.Fatalf("variant filter: %v", names(filtered))
		}
		tv := dynamic.ListUserOrderTotals
		params := db.ListUserOrderTotalsParams{Since: pgtype.Timestamptz{Time: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}, Status: db.UserStatusActive}
		native := must[[]db.ListUserOrderTotalsRow](t)(q.ListUserOrderTotals(ctx, params))
		dyn := must[[]db.ListUserOrderTotalsRow](t)(tv.Query(params).All(ctx, d))
		if !reflect.DeepEqual(native, dyn) {
			t.Fatalf("variant differs:\n%+v\n%+v", native, dyn)
		}
		// Dynamic parameters are numbered after the query's own ($1, $2).
		withOrders := must[[]db.ListUserOrderTotalsRow](t)(tv.Query(params).Where(tv.OrderCount.Gt(0), tv.Name.Ne("zzz")).Limit(5).All(ctx, d))
		if len(withOrders) != 2 {
			t.Fatalf("variant filters: %+v", withOrders)
		}
		// Join a bound view with a table.
		u := dynamic.Users
		joined := must[int64](t)(u.Query().Join(tv.Source(params), tv.UserID.EqExpr(u.ID)).Where(tv.Total.Gt(numeric(t, "10"))).Count(ctx, d))
		if joined != 1 {
			t.Fatalf("view join %d", joined)
		}
	})
}

func TestPoolAndTransactions(t *testing.T) {
	ctx := context.Background()
	u := dynamic.Users
	// Works directly on the pool.
	if _, err := u.Query().Count(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// Works on a pgx.Conn.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Query().Count(ctx, conn.Conn()); err != nil {
		t.Fatal(err)
	}
	conn.Release()
	// Mixed sqlc + dynamic statements in one transaction, rolled back.
	txn, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created := must[db.User](t)(db.New(pool).WithTx(txn).CreateUser(ctx, db.CreateUserParams{Email: "tx@example.com", Name: "Tx", Status: db.UserStatusActive}))
	if must[int64](t)(u.Query().Where(u.ID.Eq(created.ID)).Count(ctx, txn)) != 1 {
		t.Fatal("not visible inside the transaction")
	}
	if err := txn.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if must[int64](t)(u.Query().Where(u.ID.Eq(created.ID)).Count(ctx, pool)) != 0 {
		t.Fatal("visible after rollback")
	}
}

func TestInjection(t *testing.T) {
	tx(t, func(ctx context.Context, d pgx.Tx) {
		seed(t, ctx, d)
		u := dynamic.Users
		evil := `x'); DROP TABLE users; --`
		if n := must[int64](t)(u.Query().Where(u.Name.Eq(evil)).Count(ctx, d)); n != 0 {
			t.Fatal(n)
		}
		must[db.User](t)(u.Insert().Set(u.Email, evil).Set(u.Name, evil).Returning().One(ctx, d))
		if n := must[int64](t)(u.Query().Where(u.Email.Eq(evil)).Count(ctx, d)); n != 1 {
			t.Fatal("value not stored literally")
		}
		// Sort keys come from a whitelist.
		sorter := sqlcx.NewSorter(map[string]sqlcx.Sortable{"name": u.Name, "created_at": u.CreatedAt}).Tiebreaker(u.ID.Asc())
		if _, err := sorter.Parse(`name; DROP TABLE users`); err == nil {
			t.Fatal("sorter accepted an unknown key")
		}
		terms := must[[]sqlcx.OrderTerm](t)(sorter.Parse("-created_at,name"))
		rows := must[[]db.User](t)(u.Query().OrderBy(terms...).RequireStableOrder().All(ctx, d))
		if rows[0].Name != evil {
			t.Fatalf("sorted: %v", names(rows))
		}
		// Aliases are validated.
		if _, _, err := dynamic.Users.As(`u"; DROP TABLE users; --`).Query().Build(); err == nil {
			t.Fatal("invalid alias accepted")
		}
		if _, _, err := u.Query().Where(sqlcx.RawCond("name = $1", "x")).Build(); err == nil {
			t.Fatal("raw $n accepted")
		}
		if must[int64](t)(u.Query().Count(ctx, d)) != 5 {
			t.Fatal("table damaged")
		}
	})
}
