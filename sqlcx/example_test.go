package sqlcx_test

import (
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Xwudao/go-sqlcx/internal/testdb/db"
	"github.com/Xwudao/go-sqlcx/internal/testdb/dynamic"
	"github.com/Xwudao/go-sqlcx/sqlcx"
)

// The examples build statements against internal/testdb: db holds the models
// sqlc generated from its schema and dynamic the descriptors sqlc-dynamic
// generated next to them. In an application both packages are generated the
// same way from its own sqlc.yaml. Build only renders SQL; All, One, Count,
// AllAndCount and Exec run the statement on a pgxpool.Pool, *pgx.Conn or
// pgx.Tx.

func printSQL(q interface {
	Build() (string, []any, error)
}) {
	sql, args, err := q.Build()
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(sql)
	fmt.Println(args...)
}

// Conditions join the WHERE clause only when their filter is set.
func Example_dynamicWhere() {
	type Filter struct {
		Name   string
		MinAge int32
		Status *db.UserStatus
		IDs    []int64
	}
	active := db.UserStatusActive
	f := Filter{Name: "ti", Status: &active}

	u := dynamic.Users
	printSQL(u.Query().Where(
		u.Name.ContainsFold(f.Name).If(f.Name != ""),
		u.Age.Gte(f.MinAge).If(f.MinAge > 0),
		sqlcx.Optional(f.Status, u.Status.Eq),
		sqlcx.IfAny(f.IDs, u.ID.In),
	))
	// Output:
	// SELECT "users"."id", "users"."email", "users"."name", "users"."nickname", "users"."age", "users"."status", "users"."prev_status", "users"."balance", "users"."score", "users"."is_admin", "users"."tags", "users"."meta", "users"."settings", "users"."external_id", "users"."manager_id", "users"."birthday", "users"."created_at" FROM "users" WHERE "users"."name" ILIKE $1 AND "users"."status" = $2
	// %ti% active
}

// Page and AllAndCount make the usual list endpoint; Sort takes the direction
// from a bool. Page, Limit and Offset accept any integer type.
func ExampleSelect_Page() {
	u := dynamic.Users
	newestFirst := true
	var page, size int32 = 3, 20 // e.g. from a request struct
	q := u.Query().
		Where(u.Administrator.IsFalse()).
		OrderBy(u.CreatedAt.Sort(newestFirst), u.ID.Sort(newestFirst)).
		Page(page, size)
	// rows, total, err := q.AllAndCount(ctx, pool)
	printSQL(q)
	// Output:
	// SELECT "users"."id", "users"."email", "users"."name", "users"."nickname", "users"."age", "users"."status", "users"."prev_status", "users"."balance", "users"."score", "users"."is_admin", "users"."tags", "users"."meta", "users"."settings", "users"."external_id", "users"."manager_id", "users"."birthday", "users"."created_at" FROM "users" WHERE "users"."is_admin" IS FALSE ORDER BY "users"."created_at" DESC, "users"."id" DESC LIMIT $1 OFFSET $2
	// 20 40
}

// Partial rows are projected into a struct of your own; the type of every
// field is checked against its column at compile time.
func ExampleSelect_Into() {
	type Spent struct {
		Name  string
		Total pgtype.Numeric
	}
	u, o := dynamic.Users, dynamic.Orders
	printSQL(u.Query().
		Join(o, o.UserID.EqExpr(u.ID)).
		GroupBy(u.ID, u.Name).
		Having(sqlcx.Count().Gte(3)).
		Into(
			sqlcx.Bind(u.Name, func(r *Spent) *string { return &r.Name }),
			sqlcx.Bind(sqlcx.Sum[pgtype.Numeric](o.Amount), func(r *Spent) *pgtype.Numeric { return &r.Total }).As("total"),
		))
	// Output:
	// SELECT "users"."name", sum("orders"."amount") AS "total" FROM "users" JOIN "orders" ON "orders"."user_id" = "users"."id" GROUP BY "users"."id", "users"."name" HAVING count(*) >= $1
	// 3
}

// A Sorter turns a client's sort parameter into ORDER BY terms, accepting
// only the keys it lists.
func ExampleNewSorter() {
	u := dynamic.Users
	sorter := sqlcx.NewSorter(map[string]sqlcx.Sortable{
		"name":       u.Name,
		"created_at": u.CreatedAt,
	}).Tiebreaker(u.ID.Asc())

	order, err := sorter.Parse("-created_at,name")
	if err != nil {
		fmt.Println(err)
		return
	}
	printSQL(u.Query().OrderBy(order...).Limit(20))

	_, err = sorter.Parse("password")
	fmt.Println(err)
	// Output:
	// SELECT "users"."id", "users"."email", "users"."name", "users"."nickname", "users"."age", "users"."status", "users"."prev_status", "users"."balance", "users"."score", "users"."is_admin", "users"."tags", "users"."meta", "users"."settings", "users"."external_id", "users"."manager_id", "users"."birthday", "users"."created_at" FROM "users" ORDER BY "users"."created_at" DESC, "users"."name" ASC, "users"."id" ASC LIMIT $1
	// 20
	// sqlcx: unknown sort key "password" (allowed: created_at, name)
}

// SeekAfter continues a listing after the last row of the previous page
// (keyset pagination).
func ExampleSelect_SeekAfter() {
	u := dynamic.Users
	last := db.User{ID: 42, CreatedAt: pgtype.Timestamptz{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Valid: true}}
	printSQL(u.Query().
		SeekAfter(
			u.CreatedAt.Desc().After(last.CreatedAt.Time),
			u.ID.Desc().After(last.ID),
		).
		Limit(20))
	// Output:
	// SELECT "users"."id", "users"."email", "users"."name", "users"."nickname", "users"."age", "users"."status", "users"."prev_status", "users"."balance", "users"."score", "users"."is_admin", "users"."tags", "users"."meta", "users"."settings", "users"."external_id", "users"."manager_id", "users"."birthday", "users"."created_at" FROM "users" WHERE ROW("users"."created_at", "users"."id") < ROW($1, $2) ORDER BY "users"."created_at" DESC, "users"."id" DESC LIMIT $3
	// 2026-01-02 03:04:05 +0000 UTC 42 20
}

// Updates are checked like queries; one without WHERE is refused unless
// AllowFullTable is called.
func ExampleUpdate() {
	u := dynamic.Users
	printSQL(u.Update().
		Set(u.Name, "Tim").
		SetNull(u.Nickname).
		Increment(u.Age, 1).
		Where(u.ID.Eq(123)))

	_, _, err := u.Update().Set(u.Name, "x").Build()
	fmt.Println(errors.Is(err, sqlcx.ErrNoWhere))
	// Output:
	// UPDATE "users" SET "name" = $1, "nickname" = NULL, "age" = "users"."age" + $2 WHERE "users"."id" = $3
	// Tim 1 123
	// true
}

// An upsert of sqlc models.
func ExampleInsert_OnConflict() {
	u := dynamic.Users
	printSQL(u.Insert().
		Columns(u.Email, u.Name).
		Values(db.User{Email: "a@example.com", Name: "A"}, db.User{Email: "b@example.com", Name: "B"}).
		OnConflict(u.Email).DoUpdateExcluded(u.Name))
	// Output:
	// INSERT INTO "users" ("email", "name") VALUES ($1, $2), ($3, $4) ON CONFLICT ("email") DO UPDATE SET "name" = EXCLUDED."name"
	// a@example.com A b@example.com B
}
