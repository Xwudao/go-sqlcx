// Command sqlc-pgx is a runnable go-sqlcx example. It needs a PostgreSQL
// server (docker compose up -d --wait in the repository root) and creates
// its tables in a throwaway schema:
//
//	go run ./examples/sqlc-pgx
//
// Regenerate the code after editing schema.sql or query.sql with
//
//	sqlc-dynamic generate      # = sqlc generate && sqlc-dynamic link
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Xwudao/go-sqlcx/examples/sqlc-pgx/db"
	"github.com/Xwudao/go-sqlcx/examples/sqlc-pgx/dynamic"
	"github.com/Xwudao/go-sqlcx/sqlcx"
)

//go:generate sqlc-dynamic generate

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

// SearchRequest is what an HTTP handler might decode from query parameters.
type SearchRequest struct {
	Title  string
	Status *db.PostStatus
	Tags   []string
	Sort   string // e.g. "-views,title"
}

// PostSummary is a DTO projection: only the selected columns are scanned,
// each bound to its field with a compile-time type check.
type PostSummary struct {
	ID     int64
	Title  string
	Author string
	Views  int32
}

func search(ctx context.Context, d sqlcx.DB, req SearchRequest) ([]PostSummary, error) {
	p, a := dynamic.Posts, dynamic.Authors
	sorter := sqlcx.NewSorter(map[string]sqlcx.Sortable{
		"title": p.Title, "views": p.Views, "published_at": p.PublishedAt,
	}).Tiebreaker(p.ID.Asc())
	order, err := sorter.Parse(req.Sort) // unknown keys are rejected
	if err != nil {
		return nil, err
	}
	q := p.Query().Join(a, a.ID.EqExpr(p.AuthorID)).Where(
		p.Title.ContainsFold(req.Title).If(req.Title != ""), // LIKE metacharacters escaped
		sqlcx.Optional(req.Status, p.Status.Eq),
		sqlcx.IfAny(req.Tags, func(tags ...string) sqlcx.Cond { return p.Tags.Overlaps(tags) }),
	)
	return q.OrderBy(order...).Limit(20).Into(
		sqlcx.Bind(p.ID, func(r *PostSummary) *int64 { return &r.ID }),
		sqlcx.Bind(p.Title, func(r *PostSummary) *string { return &r.Title }),
		sqlcx.Bind(a.Name, func(r *PostSummary) *string { return &r.Author }),
		sqlcx.Bind(p.Views, func(r *PostSummary) *int32 { return &r.Views }),
	).All(ctx, d)
}

func run(ctx context.Context) error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://sqlcx:sqlcx@localhost:54329/sqlcx?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return err
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = "sqlcx_example"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	schema, err := os.ReadFile("examples/sqlc-pgx/schema.sql")
	if err != nil {
		schema, err = os.ReadFile("schema.sql")
		if err != nil {
			return err
		}
	}
	if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS sqlcx_example CASCADE; CREATE SCHEMA sqlcx_example; "+string(schema)); err != nil {
		return err
	}
	defer pool.Exec(context.Background(), "DROP SCHEMA sqlcx_example CASCADE")

	q := db.New(pool)
	p, a := dynamic.Posts, dynamic.Authors

	// Writes inside one pgx transaction mix sqlc methods and dynamic statements.
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		qtx := q.WithTx(tx)
		ann, err := qtx.CreateAuthor(ctx, db.CreateAuthorParams{Email: "ann@example.com", Name: "Ann"})
		if err != nil {
			return err
		}
		bob, err := qtx.CreateAuthor(ctx, db.CreateAuthorParams{Email: "bob@example.com", Name: "Bob", Bio: pgtype.Text{String: "Writes about SQL", Valid: true}})
		if err != nil {
			return err
		}
		// Batch insert from sqlc models, choosing the columns explicitly.
		_, err = p.Insert().Columns(p.AuthorID, p.Title, p.Status, p.Tags, p.Views).Values(
			db.Post{AuthorID: ann.ID, Title: "Generics in Go", Status: db.PostStatusPublished, Tags: []string{"go"}, Views: 120},
			db.Post{AuthorID: ann.ID, Title: "100% safe LIKE", Status: db.PostStatusPublished, Tags: []string{"sql", "go"}, Views: 40},
			db.Post{AuthorID: bob.ID, Title: "Keyset pagination", Status: db.PostStatusDraft, Tags: []string{"sql"}},
		).Exec(ctx, tx)
		if err != nil {
			return err
		}
		// Dynamic UPDATE with an expression and a JSONB partial update.
		tag, err := p.Update().
			Set(p.PublishedAt, time.Now()). // timestamptz compares and assigns as time.Time
			SetExpr(p.Attrs, p.Attrs.SetPath([]string{"featured"}, true)).
			Increment(p.Views, 1).
			Where(p.Status.Eq(db.PostStatusPublished)).
			Exec(ctx, tx)
		fmt.Println("published posts touched:", tag.RowsAffected())
		return err
	})
	if err != nil {
		return err
	}

	// Dynamic search with a whitelisted sort.
	published := db.PostStatusPublished
	rows, err := search(ctx, pool, SearchRequest{Title: "%", Status: &published, Sort: "-views"})
	if err != nil {
		return err
	}
	fmt.Printf("search %q: %+v\n", "%", rows)
	if _, err := search(ctx, pool, SearchRequest{Sort: "title; DROP TABLE posts"}); err != nil {
		fmt.Println("rejected sort:", err)
	}

	// The full model comes back as the sqlc type.
	authors, err := a.Query().Where(a.Bio.IsNotNull()).OrderBy(a.ID.Asc()).All(ctx, pool)
	if err != nil {
		return err
	}
	fmt.Println("authors with a bio:", len(authors), authors[0].Name)

	// Upsert.
	up, err := a.Insert().Set(a.Email, "ann@example.com").Set(a.Name, "Ann Lee").
		OnConflict(a.Email).DoUpdateExcluded(a.Name).Returning().One(ctx, pool)
	if err != nil {
		return err
	}
	fmt.Println("upserted:", up.ID, up.Name)

	// The sqlc query ListPublished, extended with dynamic filters and order.
	lp := dynamic.ListPublished
	popular, err := lp.Query().Where(lp.Views.Gte(50)).OrderBy(lp.Title.Asc()).All(ctx, pool)
	if err != nil {
		return err
	}
	fmt.Printf("popular published: %d (%s)\n", len(popular), popular[0].Title)

	// Aggregates with GROUP BY / HAVING into a DTO.
	type Stat struct {
		Author string
		Posts  int64
	}
	stats, err := a.Query().Join(p, p.AuthorID.EqExpr(a.ID)).GroupBy(a.Name).Having(sqlcx.Count().Gte(1)).
		OrderBy(a.Name.Asc()).Into(
		sqlcx.Bind(a.Name, func(s *Stat) *string { return &s.Author }),
		sqlcx.Bind(sqlcx.Count(), func(s *Stat) *int64 { return &s.Posts }),
	).All(ctx, pool)
	if err != nil {
		return err
	}
	fmt.Printf("stats: %+v\n", stats)

	// Guard: UPDATE without WHERE is refused unless explicitly allowed.
	_, err = p.Update().Set(p.Views, 0).Exec(ctx, pool)
	fmt.Println("unguarded update:", err)

	sql, args, _ := p.Query().Where(p.Tags.Has("go")).OrderBy(p.ID.Desc()).Limit(5).Build()
	fmt.Println("SQL:", strings.Fields(sql)[0], "...", len(args), "args")
	return nil
}
