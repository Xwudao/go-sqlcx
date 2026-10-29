package sqlcx_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/Xwudao/go-sqlcx/internal/testdb/db"
	"github.com/Xwudao/go-sqlcx/internal/testdb/dynamic"
	"github.com/Xwudao/go-sqlcx/sqlcx"
)

type pageSize uint16

// TestPagingIntegerTypes checks that Limit, Offset and Page accept any
// integer type and always bind int64 arguments.
func TestPagingIntegerTypes(t *testing.T) {
	u := dynamic.Users
	want := []any{int64(20), int64(40)}
	cases := map[string]sqlcx.Select[db.User]{
		"int32":       u.Query().Limit(int32(20)).Offset(int32(40)),
		"uint":        u.Query().Limit(uint(20)).Offset(uint8(40)),
		"page mixed":  u.Query().Page(int32(3), 20),
		"page named":  u.Query().Page(uint64(3), pageSize(20)),
		"page int64s": u.Query().Page(int64(3), int64(20)),
	}
	for name, q := range cases {
		_, args, err := q.Build()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(args, want) {
			t.Errorf("%s: args %#v, want %#v", name, args, want)
		}
	}

	bad := map[string]sqlcx.Select[db.User]{
		"limit overflow":  u.Query().Limit(uint64(math.MaxUint64)),
		"offset overflow": u.Query().Offset(uint(math.MaxInt64) + 1),
		"negative limit":  u.Query().Limit(int8(-1)),
		"page overflow":   u.Query().Page(uint64(math.MaxUint64), 10),
		"negative size":   u.Query().Page(1, int32(-1)),
	}
	for name, q := range bad {
		if _, _, err := q.Build(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// TestIntegerArgs checks the other integer-taking helpers: indexes and
// window arguments accept any integer type and are range-checked.
func TestIntegerArgs(t *testing.T) {
	u := dynamic.Users
	w := sqlcx.Window().OrderBy(u.ID.Asc())
	q := u.Query().Where(
		u.Meta.Index(uint8(1)).IsNotNull(),
		u.Tags.At(int64(2)).IsNotNull(),
		sqlcx.Over(sqlcx.Ntile(uint(4)), w).Gt(1),
		sqlcx.Over(sqlcx.Lag(u.Age, int64(1)), w).IsNull(),
		sqlcx.Over(sqlcx.Lead(u.Age, pageSize(2)), w).IsNull(),
		sqlcx.Over(sqlcx.Rank(), w.Rows(sqlcx.Preceding(int32(3)), sqlcx.Following(uint(5)))).Gt(0),
	)
	_, args, err := q.Build()
	if err != nil {
		t.Fatal(err)
	}
	want := []any{int32(1), int32(2), int32(4), int32(1), int32(1), int32(2), int64(3), int64(5), int64(0)}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("args %#v, want %#v", args, want)
	}

	bad := map[string]sqlcx.Typed[bool]{
		"json index": u.Meta.Index(int64(math.MaxInt32) + 1).IsNull(),
		"array at":   u.Tags.At(int64(math.MinInt32) - 1).IsNull(),
		"ntile":      sqlcx.Over(sqlcx.Ntile(uint32(math.MaxUint32)), w).Gt(1),
		"lag":        sqlcx.Over(sqlcx.Lag(u.Age, uint64(math.MaxUint64)), w).IsNull(),
		"preceding":  sqlcx.Over(sqlcx.Rank(), w.Rows(sqlcx.Preceding(uint64(math.MaxUint64)), sqlcx.CurrentRow())).Gt(0),
	}
	for name, c := range bad {
		if _, _, err := u.Query().Where(c).Build(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
