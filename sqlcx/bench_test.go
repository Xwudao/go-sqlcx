package sqlcx_test

import (
	"time"

	"testing"

	"github.com/Xwudao/go-sqlcx/internal/testdb/db"
	"github.com/Xwudao/go-sqlcx/internal/testdb/dynamic"
	"github.com/Xwudao/go-sqlcx/sqlcx"
)

func BenchmarkBuildSimple(b *testing.B) {
	u := dynamic.Users
	b.ReportAllocs()
	for b.Loop() {
		_, _, err := u.Query().Where(u.Status.Eq(db.UserStatusActive), u.Age.Gte(18)).OrderBy(u.CreatedAt.Desc()).Limit(20).Build()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildComplex(b *testing.B) {
	u := dynamic.Users
	o := dynamic.Orders
	m := dynamic.Users.As("m")
	b.ReportAllocs()
	for b.Loop() {
		_, _, err := u.Query().
			LeftJoin(m, m.ID.EqExpr(u.ManagerID)).
			Where(
				sqlcx.Or(u.Name.ContainsFold("a"), u.Email.HasPrefix("a")),
				u.Tags.Overlaps([]string{"go", "sql"}),
				u.Meta.ContainsJSON(map[string]string{"plan": "pro"}),
				sqlcx.Exists(o.Query().Where(o.UserID.EqExpr(u.ID), o.Quantity.Gt(1))),
				u.ID.In(1, 2, 3, 4, 5, 6, 7, 8),
			).
			SeekAfter(u.CreatedAt.Desc().After(benchTime), u.ID.Desc().After(100)).
			AssumeStableOrder(). // the manager join is many-to-one
			Limit(50).Build()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRenderShared measures rendering alone: the statement is built
// once and rendered repeatedly (for example when cached by the caller).
func BenchmarkRenderShared(b *testing.B) {
	u := dynamic.Users
	q := u.Query().Where(u.Status.Eq(db.UserStatusActive), u.Age.Gte(18)).OrderBy(u.CreatedAt.Desc()).Limit(20)
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := q.Build(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildParallel(b *testing.B) {
	u := dynamic.Users
	base := u.Query().Where(u.Status.Eq(db.UserStatusActive))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, _, err := base.Where(u.Age.Gte(18)).Limit(20).Build(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkUpdateBuild(b *testing.B) {
	u := dynamic.Users
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := u.Update().Set(u.Name, "x").Increment(u.Age, 1).SetNull(u.Score).Where(u.ID.Eq(1)).Build(); err != nil {
			b.Fatal(err)
		}
	}
}

var benchTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
