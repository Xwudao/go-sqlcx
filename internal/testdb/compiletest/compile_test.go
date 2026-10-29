// Package compiletest proves that misuse of the generated descriptors is
// rejected by the Go compiler. Each case is type-checked against the real
// sqlc and sqlc-dynamic output of internal/testdb.
package compiletest

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

const header = `package cases

import (
	"github.com/Xwudao/go-sqlcx/internal/testdb/db"
	"github.com/Xwudao/go-sqlcx/internal/testdb/dynamic"
	"github.com/Xwudao/go-sqlcx/sqlcx"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	_ = db.User{}
	_ = sqlcx.Cond{}
	_ = pgtype.Int4{}
)
`

const shared = `package cases

// R is a projection target shared by the cases.
type R struct {
	Age  int32
	Name string
}
`

// failures maps a case name to code that must not compile and a fragment of
// the expected compiler error.
var failures = map[string]struct{ code, want string }{
	"set wrong value type":        {`u := dynamic.Users; _ = u.Update().Set(u.Name, 3)`, "cannot use 3"},
	"set column of other table":   {`u := dynamic.Users; _ = u.Update().Set(dynamic.Orders.Note, "x")`, "does not match"},
	"set null on NOT NULL column": {`u := dynamic.Users; _ = u.Update().SetNull(u.Name)`, "does not implement sqlcx.NullableColumn"},
	"insert null on NOT NULL":     {`u := dynamic.Users; _ = u.Insert().SetNull(u.Email)`, "does not implement sqlcx.NullableColumn"},
	"like on number":              {`u := dynamic.Users; _ = u.Age.Like("1%")`, "u.Age.Like undefined"},
	"like on enum":                {`u := dynamic.Users; _ = u.Status.ILike("a%")`, "u.Status.ILike undefined"},
	"eq wrong type":               {`u := dynamic.Users; _ = u.Age.Eq("18")`, "cannot use \"18\""},
	"enum eq other enum literal":  {`u := dynamic.Users; var s string = "x"; _ = u.Status.Eq(s)`, "cannot use s"},
	"compare mismatched columns":  {`u := dynamic.Users; _ = u.ID.EqExpr(u.Name)`, "does not implement sqlcx.Typed[int64]"},
	"compare int4 with int8":      {`u := dynamic.Users; _ = u.Age.EqExpr(u.ID)`, "does not implement sqlcx.Typed[int32]"},
	"jsonb op on json column":     {`u := dynamic.Users; _ = u.Settings.HasKey("k")`, "u.Settings.HasKey undefined"},
	"array op on scalar":          {`u := dynamic.Users; _ = u.Name.Has("x")`, "u.Name.Has undefined"},
	"array element type":          {`u := dynamic.Users; _ = u.Tags.Has(1)`, "cannot use 1"},
	"arithmetic on text":          {`u := dynamic.Users; _ = u.Name.Add("x")`, "u.Name.Add undefined"},
	"increment text column":       {`u := dynamic.Users; _ = u.Update().Increment(u.Name, "x")`, "sqlcx.Numeric"},
	"where with non-bool":         {`u := dynamic.Users; _ = u.Query().Where(u.Name)`, "does not implement sqlcx.Typed[bool]"},
	"bind field type mismatch":    {`u := dynamic.Users; _ = sqlcx.Bind(u.Age, func(r *R) *int32 { return &r.Age })`, "does not match inferred type func(*R) *pgtype.Int4"},
	"over on a column":            {`u := dynamic.Users; _ = sqlcx.Over(u.ID, sqlcx.Window())`, "does not satisfy"},
	"sum of text":                 {`u := dynamic.Users; _ = sqlcx.Sum[int64](u.Name)`, "does not implement sqlcx.Numeric"},
	"into mixed row types": {`u := dynamic.Users; type S struct{ N string }
		_ = u.Query().Into(sqlcx.Bind(u.Name, func(r *R) *string { return &r.Name }), sqlcx.Bind(u.Name, func(s *S) *string { return &s.N }))`, "does not match"},
	"returning wrong model":   {`u := dynamic.Users; var _ []db.Order = nil; rows, _ := u.Delete().AllowFullTable().Returning().All(nil, nil); var _ []db.Order = rows`, "cannot use rows"},
	"view column from table":  {`v := dynamic.ListUsersByStatus; _ = dynamic.Users.Update().Set(v.Name, 1)`, "cannot use 1"},
	"view params type":        {`var s string; _ = dynamic.ListUsersByStatus.Query(s)`, "cannot use s"},
	"min of json":             {`u := dynamic.Users; _ = sqlcx.Max(u.Meta)`, "does not match sqlcx.OrderedTyped[V]"},
	"expression is not field": {`u := dynamic.Users; _ = u.Update().Set(u.Name.Lower(), "x")`, "does not match sqlcx.Assignable[db.User, V]"},
}

// valid must compile; it guards against the failure cases passing for an
// unrelated reason (for example a broken import).
const valid = `func valid() {
	u := dynamic.Users
	o := dynamic.Orders
	_ = u.Update().Set(u.Name, "x").Set(u.Age, 3).SetNull(u.Age).Increment(u.Balance, pgtype.Numeric{}).Where(u.ID.Eq(1))
	_ = u.Query().Where(u.Administrator, u.Age.Gte(18), u.ID.EqExpr(o.UserID), u.Status.Eq(db.UserStatusActive), u.Status.Eq("active"))
	_ = sqlcx.Bind(u.Age, func(r *struct{ A pgtype.Int4 }) *pgtype.Int4 { return &r.A })
	_ = sqlcx.Over(sqlcx.Sum[int64](u.Age), sqlcx.Window())
	_ = u.Insert().SetNull(u.Nickname).Set(u.Nickname, "n")
	_ = dynamic.ListUsersByStatus.Query("active")
}
`

func TestCompileErrors(t *testing.T) {
	dir, err := filepath.Abs("cases")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(failures))
	for n := range failures {
		names = append(names, n)
	}
	sort.Strings(names)
	overlay := map[string][]byte{}
	files := map[string]string{}
	for i, n := range names {
		file := filepath.Join(dir, fmt.Sprintf("case%02d.go", i))
		src := header + fmt.Sprintf("\nfunc case%02d() {\n\t%s\n}\n", i, failures[n].code)
		overlay[file] = []byte(src)
		files[file] = n
	}
	overlay[filepath.Join(dir, "valid.go")] = []byte(header + valid)
	overlay[filepath.Join(dir, "shared.go")] = []byte(shared)

	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo, Dir: dir, Overlay: overlay}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, e := range pkgs[0].Errors {
		pos, _, _ := strings.Cut(e.Pos, ":")
		got[pos] = append(got[pos], e.Msg)
	}
	if errs := got[filepath.Join(dir, "valid.go")]; len(errs) > 0 {
		t.Fatalf("valid code does not compile: %v", errs)
	}
	for file, name := range files {
		errs := got[file]
		if len(errs) == 0 {
			t.Errorf("%s: compiled, but must be rejected", name)
			continue
		}
		joined := strings.Join(errs, "\n")
		if !strings.Contains(joined, failures[name].want) {
			t.Errorf("%s: error %q does not mention %q", name, joined, failures[name].want)
		}
	}
}
