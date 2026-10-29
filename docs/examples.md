# go-sqlcx 用法示例

以下示例使用 `internal/testdb` 的 schema（`users`、`orders`、`categories`、`audit.users`）。每个示例都有对应的可执行测试，括号中给出位置；生成的 SQL 见 `sqlcx/testdata/golden/*.sql`。

```go
u, o := dynamic.Users, dynamic.Orders
```

## 条件

```go
u.Status.Eq(db.UserStatusActive)            // "users"."status" = $1
u.Age.Between(18, 30)                       // 可空 int4，用 int32 比较
u.Name.In()                                 // FALSE（空 IN）
u.Name.NotIn()                              // TRUE
u.ID.EqAny([]int64{1, 2, 3})                // "users"."id" = ANY ($1)
u.Age.CompareAll(sqlcx.OpGt, []int32{1, 2}) // > ALL ($1)
u.Name.Contains("50%_off")                  // LIKE $1，参数为 %50\%\_off%
u.Age.IsDistinctFrom(17)                    // NULL 安全
sqlcx.Not(u.Administrator)                  // bool 列本身就是条件
u.Age.Cast[int64]("bigint").IsNotNull()     // 泛型方法 Cast[U]
sqlcx.AsText(u.Status.Cast[string]("text")).HasPrefix("act")
sqlcx.RawCond("length(?) > ?", u.Name, 3)   // 受控扩展点
```

（`internal/testdb/itest/query_test.go` `TestDynamicWhere`，约 50 个用例逐一在 PostgreSQL 上执行。）

## JSONB 与数组

```go
u.Meta.ContainsJSON(map[string]string{"plan": "pro"})  // meta @> CAST($1 AS jsonb)
u.Meta.Get("flags").HasKey("beta")                     // (meta -> $1) ? $2
u.Meta.PathText("flags", "beta").Eq("true")            // meta #>> $1 = $2
u.Meta.PathExists(`$.flags.beta ? (@ == true)`)        // jsonpath
u.Meta.Index(i).IsNotNull()                            // meta -> CAST($1 AS integer)，i 可为任意整数类型
u.Tags.Has("go")                                       // $1 = ANY (tags)
u.Tags.Overlaps([]string{"go", "sql"})                 // tags && $1
u.Tags.Len().Eq(0)                                     // cardinality(tags) = $1
u.Tags.At(1).IsNotDistinctFrom("go")                   // tags[CAST($1 AS integer)] IS NOT DISTINCT FROM $2，1 起始

u.Update().SetExpr(u.Meta, u.Meta.SetPath([]string{"flags", "beta"}, false)) // jsonb_set
u.Update().SetExpr(u.Tags, u.Tags.Append("rust"))                            // array_append
```

## 子查询

```go
sqlcx.Exists(o.Query().Where(o.UserID.EqExpr(u.ID)))                  // 关联子查询
u.ID.InQuery(o.Query().Where(o.Quantity.Gte(2)).Values(o.UserID))     // Values[V] 单列子查询
u.Balance.LtExpr(o.Query().Values(sqlcx.Max(o.Amount)))               // 标量子查询
u.ID.CompareAllQuery(sqlcx.OpNe, o.Query().Values(o.UserID))          // <> ALL (SELECT ...)
```

`Values(e)` 的值类型来自表达式的比较类型 `V`，因此 `u.ID.InQuery(...Values(o.Note))` 无法编译。

## 递归 CTE（`TestCTEAndDerived`）

```go
c := dynamic.Categories
type Node struct{ ID, Depth int32 }
bind := func(id, depth sqlcx.Selectable[int32]) []sqlcx.Projection[Node] {
    return []sqlcx.Projection[Node]{
        sqlcx.Bind(id, func(n *Node) *int32 { return &n.ID }),
        sqlcx.Bind(depth, func(n *Node) *int32 { return &n.Depth }),
    }
}
tree := sqlcx.RecursiveCTE("tree", "id", "depth")
tid := sqlcx.Ref[sqlcx.Number[int32]](tree, "id")
tdepth := sqlcx.Ref[sqlcx.Number[int32]](tree, "depth")
base := c.Query().Where(c.ID.Eq(1)).Into(bind(c.ID, sqlcx.Val[int32](0).Cast[int32]("integer"))...)
step := c.Query().Join(tree, c.ParentID.EqExpr(tid)).Into(bind(c.ID, tdepth.Add(1))...)
tree = tree.As(sqlcx.UnionAll(base, step))

nodes, err := sqlcx.SelectFields(bind(tid, tdepth)...).From(tree).With(tree).OrderBy(tid.Asc()).All(ctx, db)
```

## 派生表与 LATERAL

```go
type Spend struct {
    UserID int64
    Total  pgtype.Numeric
}
spendQ := o.Query().GroupBy(o.UserID).Into(
    sqlcx.Bind(o.UserID, func(s *Spend) *int64 { return &s.UserID }),
    sqlcx.Bind(sqlcx.Sum[pgtype.Numeric](o.Amount), func(s *Spend) *pgtype.Numeric { return &s.Total }).As("total"),
)
spend := sqlcx.Derived(spendQ, "spend")
big, err := u.Query().
    Join(spend, sqlcx.Ref[sqlcx.Number[int64]](spend, "user_id").EqExpr(u.ID)).
    Where(sqlcx.Ref[sqlcx.Number[pgtype.Numeric]](spend, "total").Gt(n)).
    All(ctx, db)

latest := sqlcx.Lateral(o.Query().Where(o.UserID.EqExpr(u.ID)).OrderBy(o.ID.Desc()).Limit(1).Values(o.Amount), "latest")
u.Query().Join(latest, sqlcx.True())
```

## 窗口函数

```go
type Ranked struct {
    Name string
    Rank int64
}
u.Query().Into(
    sqlcx.Bind(u.Name, func(r *Ranked) *string { return &r.Name }),
    sqlcx.Bind(sqlcx.Over(sqlcx.RowNumber(),
        sqlcx.Window().PartitionBy(u.Status).OrderBy(u.Age.Desc().NullsLast()).
            Rows(sqlcx.UnboundedPreceding(), sqlcx.CurrentRow())),
        func(r *Ranked) *int64 { return &r.Rank }),
)
```

`Ntile(n)`、`Lag(e, offset)`、`Lead(e, offset)`、`Preceding(n)`、`Following(n)` 的整数参数接受任意整数类型，超出 PostgreSQL 参数范围时 `Build` 报错。

## 写操作（`TestMutations`）

```go
u.Update().
    Set(u.Name, "Annie").
    SetNull(u.Score).
    Set(u.Nickname, "an").                          // *string override：用 string 赋值
    Increment(u.Age, 1).
    Set(u.PrevStatus, db.UserStatusInactive).       // Null 枚举列：用枚举值赋值
    Where(u.ID.Eq(id)).
    Returning().One(ctx, db)

u.Update().SetField(u.PrevStatus, db.NullUserStatus{}).SetField(u.Nickname, nil) // 显式写 NULL
o.Update().SetExpr(o.Note, sqlcx.AsText(u.Name.Lower())).From(u).Where(u.ID.EqExpr(o.UserID))

u.Insert().Set(u.Email, "eve@example.com").Set(u.Name, "Eve").SetNull(u.Age).Returning().One(ctx, db)
u.Insert().Columns(u.Email, u.Name, u.Status).Values(m1, m2).Returning().All(ctx, db)
u.Insert().Set(u.Email, e).Set(u.Name, n).OnConflict(u.Email).DoUpdateExcluded(u.Name)
u.Insert().Set(u.Email, e).Set(u.Name, n).OnConflict(u.Email).DoNothing()

o.Delete().Using(u).Where(u.ID.EqExpr(o.UserID), u.Name.Eq("Bob")).Returning().All(ctx, db)
dynamic.AuditUsers.Delete().AllowFullTable().ReturningInto(sqlcx.Bind(a.ID, ...)).All(ctx, db)
```

## 错误

```go
errors.Is(err, sqlcx.ErrNoWhere)     // 无 WHERE 的 UPDATE/DELETE
errors.Is(err, sqlcx.ErrTooManyRows) // One 返回多行
errors.Is(err, sqlcx.ErrUnordered)   // First 没有 ORDER BY
errors.Is(err, pgx.ErrNoRows)        // One/First 没有结果
var pgErr *pgconn.PgError            // 数据库错误原样返回
```

## 完整可运行示例

`examples/sqlc-pgx`：sqlc + go-sqlcx 的独立小项目（作者/文章），演示事务中混用 sqlc 方法与动态语句、白名单排序的搜索接口、DTO 投影、upsert、聚合与 sqlc 查询变体。

```bash
docker compose up -d --wait
go run ./examples/sqlc-pgx
```
