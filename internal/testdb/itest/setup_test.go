// Package itest runs go-sqlcx against a real PostgreSQL server using the
// code that sqlc and sqlc-dynamic generated for internal/testdb.
//
// Start the server with `docker compose up -d --wait` in the repository
// root. SQLCX_DATABASE_URL overrides the default connection string; the
// tests are skipped when no server is reachable unless SQLCX_REQUIRE_DB=1.
package itest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultURL = "postgres://sqlcx:sqlcx@localhost:54329/sqlcx?sslmode=disable"

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	url := os.Getenv("SQLCX_DATABASE_URL")
	if url == "" {
		url = defaultURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		if os.Getenv("SQLCX_REQUIRE_DB") == "1" {
			fmt.Fprintln(os.Stderr, "itest: cannot connect:", err)
			return 1
		}
		fmt.Fprintln(os.Stderr, "itest: skipping, no PostgreSQL:", err)
		return 0
	}
	name := fmt.Sprintf("sqlcx_it_%d", rand.Uint64())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		fmt.Fprintln(os.Stderr, "itest:", err)
		return 1
	}
	defer func() {
		if pool != nil {
			pool.Close()
		}
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(context.Background())
	}()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "itest:", err)
		return 1
	}
	cfg.ConnConfig.Database = name
	pool, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "itest:", err)
		return 1
	}
	files, _ := filepath.Glob("../schema/*.sql")
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "itest:", err)
			return 1
		}
		if _, err := pool.Exec(ctx, string(b)); err != nil {
			fmt.Fprintln(os.Stderr, "itest: apply", f, err)
			return 1
		}
	}
	return m.Run()
}

// tx runs fn in a transaction that is always rolled back, isolating tests.
func tx(t *testing.T, fn func(ctx context.Context, tx pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	fn(ctx, tx)
}
