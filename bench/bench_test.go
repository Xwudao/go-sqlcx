// Package bench compares go-sqlcx with a hand-written query, sqlc and
// Squirrel. It is a separate module so the library does not depend on the
// comparison libraries. Execution benchmarks need the docker compose
// PostgreSQL (see the repository README); build benchmarks run anywhere.
package bench

import (
	"context"
	"os"
	"testing"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Xwudao/go-sqlcx/internal/testdb/db"
	"github.com/Xwudao/go-sqlcx/internal/testdb/dynamic"
)

// ---- statement construction ----

func BenchmarkBuild_Sqlcx(b *testing.B) {
	u := dynamic.Users
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := u.Query().Where(u.Status.Eq(db.UserStatusActive), u.Age.Gte(18)).OrderBy(u.CreatedAt.Desc()).Limit(20).Build(); err != nil {
			b.Fatal(err)
		}
	}
}

var userCols = []string{"id", "email", "name", "nickname", "age", "status", "prev_status", "balance", "score", "is_admin", "tags", "meta", "settings", "external_id", "manager_id", "birthday", "created_at"}

func BenchmarkBuild_Squirrel(b *testing.B) {
	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := psql.Select(userCols...).From("users").Where(sq.Eq{"status": "active"}).Where(sq.GtOrEq{"age": 18}).OrderBy("created_at DESC").Limit(20).ToSql(); err != nil {
			b.Fatal(err)
		}
	}
}

// ---- execution (fetch 100 rows into sqlc models) ----

func connect(b *testing.B) *pgxpool.Pool {
	url := os.Getenv("SQLCX_DATABASE_URL")
	if url == "" {
		url = "postgres://sqlcx:sqlcx@localhost:54329/sqlcx?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err == nil {
		err = pool.Ping(ctx)
	}
	if err != nil {
		b.Skip("no PostgreSQL:", err)
	}
	schema, err := os.ReadFile("../internal/testdb/schema/001_init.sql")
	if err != nil {
		b.Fatal(err)
	}
	stmts := []string{"DROP SCHEMA IF EXISTS bench CASCADE", "CREATE SCHEMA bench", "SET search_path TO bench"}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		b.Fatal(err)
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := conn.Exec(ctx, string(schema)); err != nil {
		b.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO users (email, name, age, tags, meta) SELECT 'u'||g||'@x', 'user'||g, g % 80, '{a,b}', '{"k":1}' FROM generate_series(1, 1000) g`); err != nil {
		b.Fatal(err)
	}
	conn.Release()
	pool.Close()
	cfg, _ := pgxpool.ParseConfig(url)
	cfg.ConnConfig.RuntimeParams["search_path"] = "bench"
	pool, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(pool.Close)
	return pool
}

const rawSQL = `SELECT id, email, name, nickname, age, status, prev_status, balance, score, is_admin, tags, meta, settings, external_id, manager_id, birthday, created_at FROM users WHERE status = $1 AND age >= $2 ORDER BY id LIMIT 100`

func BenchmarkExec_RawPgx(b *testing.B) {
	pool := connect(b)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		rows, err := pool.Query(ctx, rawSQL, db.UserStatusActive, int32(18))
		if err != nil {
			b.Fatal(err)
		}
		var out []db.User
		for rows.Next() {
			var i db.User
			if err := rows.Scan(&i.ID, &i.Email, &i.Name, &i.Nickname, &i.Age, &i.Status, &i.PrevStatus, &i.Balance, &i.Score, &i.Administrator, &i.Tags, &i.Meta, &i.Settings, &i.ExternalID, &i.ManagerID, &i.Birthday, &i.CreatedAt); err != nil {
				b.Fatal(err)
			}
			out = append(out, i)
		}
		if rows.Err() != nil || len(out) != 100 {
			b.Fatal(rows.Err(), len(out))
		}
	}
}

func BenchmarkExec_Sqlcx(b *testing.B) {
	pool := connect(b)
	ctx := context.Background()
	u := dynamic.Users
	b.ReportAllocs()
	for b.Loop() {
		out, err := u.Query().Where(u.Status.Eq(db.UserStatusActive), u.Age.Gte(18)).OrderBy(u.ID.Asc()).Limit(100).All(ctx, pool)
		if err != nil || len(out) != 100 {
			b.Fatal(err, len(out))
		}
	}
}

func BenchmarkExec_SquirrelPgx(b *testing.B) {
	pool := connect(b)
	ctx := context.Background()
	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	b.ReportAllocs()
	for b.Loop() {
		sql, args, err := psql.Select(userCols...).From("users").Where(sq.Eq{"status": db.UserStatusActive}).Where(sq.GtOrEq{"age": int32(18)}).OrderBy("id").Limit(100).ToSql()
		if err != nil {
			b.Fatal(err)
		}
		out, err := pgx.CollectRows(must(pool.Query(ctx, sql, args...)), pgx.RowToStructByPos[db.User])
		if err != nil || len(out) != 100 {
			b.Fatal(err, len(out))
		}
	}
}

// BenchmarkExec_SqlcStatic is the sqlc-generated static query (no age
// filter, no limit: it returns every active user), shown as the cost of a
// fixed query scanning into the same model.
func BenchmarkExec_SqlcStatic(b *testing.B) {
	pool := connect(b)
	ctx := context.Background()
	q := db.New(pool)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := q.ListUsersByStatus(ctx, db.UserStatusActive); err != nil {
			b.Fatal(err)
		}
	}
}

func must(r pgx.Rows, err error) pgx.Rows {
	if err != nil {
		panic(err)
	}
	return r
}
