# go-sqlcx

**sqlc 的类型安全动态查询扩展。** 继续用 sqlc 写静态 SQL、用 sqlc 生成的 Model；在需要动态 WHERE / ORDER BY / 投影 / JOIN / UPDATE 的地方，用基于同一套 Model 生成的列描述来构建 SQL，由 Go 编译器检查字段与值的类型。

```go
u := dynamic.Users

users, err := u.Query().                 // []db.User —— sqlc 生成的原生 Model
    Where(
        u.Status.Eq(db.UserStatusActive), // 枚举列只接受 db.UserStatus
        u.Age.Gte(18),                    // 可空 int4 列与 int32 比较
    ).
    OrderBy(u.CreatedAt.Desc()).
    Limit(20).
    All(ctx, pool)
```

- 仅支持 **PostgreSQL + pgx/v5**（`*pgxpool.Pool`、`*pgx.Conn`、`pgx.Tx`），需要 **Go 1.27**（使用泛型方法）。
- 不是 ORM：没有模型生命周期、脏字段追踪、预加载、迁移。不修改、不 fork sqlc，不改动 sqlc 生成的文件。
- 设计与取舍见 [docs/architecture.md](docs/architecture.md)，更多用法见 [docs/examples.md](docs/examples.md)。

## 为什么 sqlc 需要动态查询扩展

sqlc 的静态 SQL 很好，但列表页、搜索、管理后台这类“条件可选、排序可选、字段可选”的查询，只能写成：

```sql
WHERE (sqlc.narg(name)::text IS NULL OR name ILIKE sqlc.narg(name))
  AND (sqlc.narg(status)::user_status IS NULL OR status = sqlc.narg(status))
ORDER BY CASE WHEN @sort = 'name' THEN name END, ...
```

或者退回到字符串拼接 / 通用 SQL Builder，后者用字符串表示列名、`interface{}` 表示值，也不认识 sqlc 的 Model。go-sqlcx 让动态部分和 sqlc 共用同一份 schema、同一份 Model、同一套类型映射（含 overrides 和 rename），且编译期发现类型错误。

## 安装

```bash
go get github.com/Xwudao/go-sqlcx
go install github.com/Xwudao/go-sqlcx/cmd/sqlc-dynamic@latest   # 插件 + link 工具
```

`sqlc-dynamic` 必须在 `PATH` 中，sqlc 以 process 插件方式调用它。

## sqlc.yaml 配置

只需在现有配置中加一个插件和一个 codegen 目标，没有第二套配置文件：

```yaml
version: "2"
plugins:
  - name: sqlcx
    process:
      cmd: sqlc-dynamic
sql:
  - engine: postgresql
    schema: schema
    queries: queries
    gen:
      go:
        package: db
        out: internal/db
        sql_package: pgx/v5
        emit_db_tags: true          # 推荐：按 db tag 精确对应列
    codegen:
      - plugin: sqlcx
        out: internal/db/dynamic    # 必须与 gen.go.out 不同
        options:
          package: dynamic
          # queries: ["*"]          # 为哪些 sqlc 查询生成动态变体（也可用 -- sqlcx:dynamic 注释）
          # unique_keys: {events: [[tenant_id, seq]]}   # 补充唯一键（用于分页稳定性校验）
          # skip_tables: [schema_migrations]
          # models: internal/db     # 不用 gen.go 时指定 sqlc Go 包目录
          # output: sqlcx_gen.go
```

WASM 插件同样可用（只包含插件部分，link 仍用本地二进制）：

```bash
make wasm   # 产出 bin/sqlc-dynamic.wasm 及其 sha256
```

```yaml
plugins:
  - name: sqlcx
    wasm:
      url: file:///abs/path/bin/sqlc-dynamic.wasm
      sha256: <sha256>
```

## 代码生成流程

```bash
sqlc-dynamic generate        # = sqlc generate && sqlc-dynamic link
# 或分两步
sqlc generate                # sqlc 生成 Model，插件写出 dynamic/sqlcx.manifest.json
sqlc-dynamic link            # 读取清单 + 用 go/types 读取 sqlc 实际生成的代码，写出 dynamic/sqlcx_gen.go
sqlc-dynamic link -check     # CI：生成结果过期时失败
sqlc-dynamic generate -check # CI：sqlc diff + link -check，不写任何文件
```

项目用 go.mod `tool` 指令管理工具时（`go get -tool github.com/Xwudao/go-sqlcx/cmd/sqlc-dynamic`），直接 `go tool sqlc-dynamic generate` 即可：它优先使用项目固定版本的 `go tool sqlc`（先查当前目录所在的 module，再查 sqlc.yaml 所在的 module，因此工具放在嵌套 module 时可以 `cd tools && go tool sqlc-dynamic generate -f ../sqlc.yaml`），并把自身临时放到 sqlc 子进程的 PATH 上，无需安装任何二进制。注意此后单独运行 `sqlc generate` 时插件必须在 PATH 中，建议把项目的 sqlc 生成命令统一换成 `sqlc-dynamic generate`。

为什么是两步：sqlc 并发执行所有 codegen 目标、最后才写文件，插件运行时看不到新生成的 Go 代码；link 在 sqlc 写完之后运行，读取的是实际生成的 Model，因此 overrides、rename、nullable、自定义类型都以 sqlc 的结果为准。输出是确定的（固定顺序、gofmt）。

生成内容（每张表一个描述符，每个 opt-in 的 sqlc 查询一个变体）：

```go
type UsersTable struct {
    *sqlcx.Table[db.User]
    // Nickname is column "nickname" (varchar, nullable).
    Nickname sqlcx.TextColumn[db.User, *string, string, sqlcx.Nullable]
    Age      sqlcx.NumberColumn[db.User, pgtype.Int4, int32, sqlcx.Nullable]
    Status   sqlcx.OrderedColumn[db.User, db.UserStatus, db.UserStatus, sqlcx.NotNull]
    ...
}
var Users = NewUsersTable("")
func (t UsersTable) As(alias string) UsersTable
```

类型参数依次为：sqlc Model、Model 字段类型、比较用的非空值类型、可空性。

## 动态 WHERE

```go
q := u.Query().Where(
    u.Name.ContainsFold(req.Name).If(req.Name != ""), // ILIKE '%…%'，自动转义 % _ \；条件为假时不加
    u.Age.Gte(req.MinAge).If(req.MinAge > 0),
    sqlcx.Optional(req.Status, u.Status.Eq),          // 指针为 nil 时不加（不会解引用 nil）
    sqlcx.IfAny(req.IDs, u.ID.In),                    // 空切片时不加条件（而不是 FALSE）
    sqlcx.Or(u.Age.Gte(18), u.Age.IsNull()),
).
    WhereIf(req.OnlyAdmins, u.Administrator)          // 整组条件按开关添加
```

任何条件都可以链式调用 `.If(ok)`；`Select`、`Update`、`Delete` 都有 `WhereIf(ok, conds...)`。注意 `.If` 的操作数总会求值，值在指针后面时用 `sqlcx.Optional`。`Update`/`Delete` 在所有条件都被跳过时仍返回 `ErrNoWhere`。

```go
```

条件按调用顺序组合，参数编号稳定；构建器不可变，`q.Where(...)` 返回新值，可以安全地从同一个基础查询派生。空条件（`sqlcx.Cond{}`）在 `Where/And/Or/Not` 中被忽略。

支持的操作（按列类型提供）：`Eq Ne Gt Gte Lt Lte In NotIn Between NotBetween Like ILike NotLike NotILike SimilarTo MatchRegex IsNull IsNotNull IsDistinctFrom`、`And Or Not Exists NotExists`、`EqAny NeAll CompareAny CompareAll`（`= ANY($1)`）、子查询 `InQuery CompareAnyQuery`、数组 `Contains ContainedBy Overlaps Has Len Append Remove`、JSONB `Get GetText Path PathText Contains ContainsJSON HasKey HasAnyKey HasAllKeys PathExists MatchPath`、`Cast[U]` 类型转换，以及 `Raw/RawCond` 扩展点。

## JOIN

```go
u, o := dynamic.Users, dynamic.Orders
m := dynamic.Users.As("manager")             // 自连接

type Row struct {
    User    db.User
    Manager *db.User                          // LEFT JOIN 一侧可能整行为 NULL
}
rows, err := u.Query().
    LeftJoin(m, m.ID.EqExpr(u.ManagerID)).    // 列与列比较也做类型检查
    Where(sqlcx.Exists(o.Query().Where(o.UserID.EqExpr(u.ID)))).
    Into(
        u.Embed(func(r *Row) *db.User { return &r.User }),
        m.EmbedNull(func(r *Row) **db.User { return &r.Manager }),
    ).All(ctx, pool)
```

支持 `Join LeftJoin RightJoin FullJoin CrossJoin`、多表、别名、`Derived`（子查询 JOIN）、`Lateral`、`NewCTE`、`RecursiveCTE`、`UnionAll` 等，ON 条件可动态组合。

## 动态 SELECT 与 DTO

完整 Model 查询直接返回 sqlc Model。部分字段必须显式投影到 DTO，每个字段的类型由编译器核对，**不会返回“只填了一部分字段”的 Model**：

```go
type Summary struct {
    ID    int64
    Name  string
    Spent pgtype.Numeric
}
sums, err := u.Query().
    Join(o, o.UserID.EqExpr(u.ID)).
    GroupBy(u.ID, u.Name).
    Having(sqlcx.Count().Gte(3)).
    OrderBy(u.Name.Asc()).
    Into(                                       // 泛型方法：Select[db.User] → Select[Summary]
        sqlcx.Bind(u.ID, func(r *Summary) *int64 { return &r.ID }),
        sqlcx.Bind(u.Name, func(r *Summary) *string { return &r.Name }),
        sqlcx.Bind(sqlcx.Sum[pgtype.Numeric](o.Amount), func(r *Summary) *pgtype.Numeric { return &r.Spent }).As("spent"),
    ).All(ctx, pool)

names, err := u.Query().Distinct().Values(u.Name).All(ctx, pool) // []string
ptrs, err := u.Query().AllPtrs(ctx, pool)                          // []*db.User，配合 emit_result_struct_pointers
```

另有 `DistinctOn`、窗口函数（`Over(RowNumber(), Window().PartitionBy(...).OrderBy(...).Rows(...))`；`Ntile`、`Lag/Lead`、`Preceding/Following` 的整数参数同样接受任意整数类型）、`Filter`、`Case`、`Coalesce`、`BindNull`、`BindScanner`（自定义 Scanner）。

## 动态 ORDER BY 与分页

```go
sorter := sqlcx.NewSorter(map[string]sqlcx.Sortable{
    "name": u.Name, "created_at": u.CreatedAt,
}).Tiebreaker(u.ID.Asc())
order, err := sorter.Parse(r.URL.Query().Get("sort"))   // "-created_at,name"；未知键报错

q = q.OrderBy(order...).RequireStableOrder()            // 必须覆盖一个 NOT NULL 唯一键
next := u.Query().SeekAfter(                            // keyset 分页
    u.CreatedAt.Desc().After(last.CreatedAt.Time),
    u.ID.Desc().After(last.ID),
).Limit(20)
total, err := q.Count(ctx, pool)                        // 忽略 ORDER BY/LIMIT/OFFSET
```

运行时决定排序的三种写法（与条件上的 `.If` 一致，`nil` 排序项会被跳过）：

```go
q.OrderBy(
    u.CreatedAt.Asc().If(req.CreateOrder == "asc"),   // 可选排序项
    u.CreatedAt.Desc().If(req.CreateOrder == "desc"),
    u.Age.Sort(req.AgeOrder == "desc"),               // 方向由 bool 决定（true = DESC）
    sqlcx.SortBy(field, desc),                        // 对任意 Sortable 同样适用
    u.ID.Asc(),
)
```

列表页的常见形态（总数忽略 LIMIT/OFFSET，总数为 0 时不再查询分页数据）：

```go
rows, total, err := q.OrderBy(u.ID.Desc()).Page(req.Page, req.Size).AllAndCount(ctx, pool)
// AllPtrsAndCount 返回 []*M，配合 emit_result_struct_pointers
// Page/Limit/Offset 接受任意整数类型（int32、int、uint…，page 与 size 可不同），无需转换
```

两条语句分别执行；需要同一快照时传入 REPEATABLE READ 的 `pgx.Tx`。唯一键由 link 从 schema DDL 提取。`NullsFirst/NullsLast`、表达式排序、`Limit/Offset`（与 `Page` 一样接受任意整数类型 `sqlcx.Integer`，统一绑定为 int64，越界在 `Build` 时报错）、`Exists`、`First`（要求 ORDER BY）、`One`（多行报 `ErrTooManyRows`）、`Rows`（`iter.Seq2` 流式迭代）均可用。

## UPDATE / INSERT / DELETE

```go
_, err := u.Update().
    Set(u.Name, "Tim").                                       // 值类型由列决定
    SetNull(u.Nickname).                                      // 只有可空列能编译
    Increment(u.Age, 1).
    SetExpr(u.Meta, u.Meta.SetPath([]string{"flags", "beta"}, true)). // JSONB 局部更新
    Where(u.ID.Eq(123)).
    Exec(ctx, pool)

u.Update().Set(u.Name, "x").Exec(ctx, pool)                  // ErrNoWhere：默认禁止全表更新
u.Update().Set(u.Name, "x").AllowFullTable().Exec(ctx, pool) // 显式允许

created, err := u.Insert().Set(u.Email, "a@x").Set(u.Name, "A").Returning().One(ctx, pool)
_, err = u.Insert().Columns(u.Email, u.Name).Values(models...).                  // 批量
    OnConflict(u.Email).DoUpdateExcluded(u.Name).Where(u.Status.Ne(db.UserStatusBanned)).
    Exec(ctx, pool)
_, err = audit.Insert().Columns(audit.UserID, audit.Action).FromQuery(selectQuery).Exec(ctx, pool)

deleted, err := o.Delete().Using(u).Where(u.ID.EqExpr(o.UserID), u.Status.Eq(db.UserStatusBanned)).
    Returning().All(ctx, pool)                                 // []db.Order
```

还有 `UPDATE ... FROM`、`SetField`（用 Model 字段类型显式写入可能为 NULL 的值）、`SetDefault`、`OnConflictConstraint`、`DoNothing`、`ReturningInto[R]`（泛型方法）、数据修改 CTE。

## 事务

不重新实现事务：把 `pgx.Tx` 传给任何执行方法即可，与 sqlc 共用同一个事务。

```go
err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
    user, err := db.New(pool).WithTx(tx).CreateUser(ctx, params)   // sqlc
    if err != nil {
        return err
    }
    _, err = dynamic.Users.Update().Set(dynamic.Users.Age, 30).Where(dynamic.Users.ID.Eq(user.ID)).Exec(ctx, tx)
    return err
})
```

## 与 sqlc 原生 Query 互操作

给 sqlc 查询加 `-- sqlcx:dynamic` 注释（或插件选项 `queries`）即生成一个可追加条件的变体，参数签名与 Row 类型和 sqlc 方法一致：

```sql
-- name: ListUserOrderTotals :many
-- sqlcx:dynamic
SELECT u.id AS user_id, u.name, count(o.id) AS order_count, sum(o.amount)::numeric AS total
FROM users u LEFT JOIN orders o ON o.user_id = u.id
WHERE u.status = sqlc.arg(status)
GROUP BY u.id, u.name
ORDER BY total DESC;
```

```go
v := dynamic.ListUserOrderTotals
rows, err := v.Query(db.ListUserOrderTotalsParams{...}).   // []db.ListUserOrderTotalsRow
    Where(v.OrderCount.Gt(2)).
    Limit(10).All(ctx, pool)
// SELECT ... FROM (<原查询，去掉 ORDER BY>) AS "list_user_order_totals"
//   WHERE "list_user_order_totals"."order_count" > $3 ORDER BY ..."total" DESC LIMIT $4
```

改写基于 PostgreSQL 解析树（libpg_query）：原 `ORDER BY` 从解析树移除后作为默认外层排序，参数位置由 PostgreSQL 词法器确定。含 `LIMIT/OFFSET`、`UNION`、`DISTINCT ON`、行锁、表达式排序等无法安全包裹的查询，link 会明确报错。原生 sqlc 方法不受任何影响。

## 类型安全保证及限制

编译期拒绝（均有编译失败测试 `internal/testdb/compiletest`）：值类型与列不符；跨表 SET；对 NOT NULL 列 SetNull；在不支持的列类型上使用 LIKE / 算术 / JSONB / 数组操作；比较类型不同的两列；投影字段类型与列不符；把表达式当作赋值目标；在列上使用 `Over`。

运行时（`Build`/执行时返回错误）：无 WHERE 的 UPDATE/DELETE；非法别名/类型名/函数名；`Raw` 占位符数量不符或出现 `$n`；`Over`/`Filter` 用于非聚合函数；排序稳定性不满足；`INSERT ... SELECT` 列数不符；超过 65535 个参数。

需要注意：

- 只有上下文能确定参数类型时 PostgreSQL 才能推断；select 列表中的裸参数会被当作 text，此时用 `sqlcx.Val[int32](0).Cast[int32]("integer")`。
- `Ne` 和 `NotInQuery` 遵循 SQL 三值逻辑（NULL 行不匹配），需要 NULL 安全比较时用 `IsDistinctFrom` 或 `NotExists`。
- `Ref` 按名字引用派生表/CTE 的列；JOIN 条件是否引用了 FROM 中的表由 PostgreSQL 检查。
- `Raw` 是信任边界：SQL 文本必须是开发者写的常量。
- 分页稳定性校验是保守的（要求每个来源的唯一键），确认安全时用 `AssumeStableOrder()`。
- 未在编译期区分的 PostgreSQL 类型（域类型、扩展类型）归为可比较类别。

## 性能基准

`make bench`，Intel i5-1145G7，PostgreSQL 18（本地 Docker），单次运行，仅供参考：

| 基准 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| 构建 SQL：go-sqlcx（2 条件 + ORDER BY + LIMIT，17 列） | 2268 | 3256 | 34 |
| 构建 SQL：Squirrel（同等查询） | 10845 | 7651 | 133 |
| 渲染已构建的 go-sqlcx 语句 | 1338 | 1648 | 8 |
| 构建复杂查询（JOIN、OR、数组、JSONB、EXISTS、IN、keyset） | 8865 | 10781 | 117 |
| 执行并扫描 100 行：原生 pgx 手写 Scan | 285811 | 166719 | 1021 |
| 执行并扫描 100 行：go-sqlcx | 272253 | 110044 | 857 |
| 执行并扫描 100 行：Squirrel + `pgx.RowToStructByPos` | 342252 | 174401 | 1153 |

go-sqlcx 的扫描使用生成的目标列表（与 sqlc 相同，无反射），每个查询复用一组扫描目标。执行耗时主要由数据库往返决定。

## 与 ORM 和普通 SQL Builder 的区别

| | go-sqlcx | ORM（GORM 等） | SQL Builder（Squirrel、goqu） |
|---|---|---|---|
| 模型来源 | sqlc 生成的 Model | ORM 自己的模型/标签 | 无 |
| 列名 | 生成的类型化描述 | 字符串或反射 | 字符串 |
| 值类型检查 | 编译期，按列类型 | 运行时 | 无 |
| 操作符按类型限制 | 是（LIKE 仅文本、JSONB 仅 jsonb…） | 否 | 否 |
| 静态 SQL | 继续用 sqlc | 生成 | 手写 |
| 生命周期/预加载/迁移 | 无（有意不做） | 有 | 无 |
| 运行时反射 | 无 | 大量 | 部分 |

## 开发

```bash
make db-up     # docker compose 启动 PostgreSQL 18（127.0.0.1:54329）
make test      # 单元、golden、编译失败、生成一致性、集成测试
make race      # 同上，开启 -race
make lint      # Go 1.27 gofmt + go vet
make generate  # 重新生成 internal/testdb 与 examples/sqlc-pgx（需要 sqlc）
make check     # 生成代码是否最新
make bench
go run ./examples/sqlc-pgx
```

集成测试默认连接 `postgres://sqlcx:sqlcx@localhost:54329/sqlcx`，可用 `SQLCX_DATABASE_URL` 覆盖；每次运行创建并删除独立数据库。无数据库时集成测试跳过，设 `SQLCX_REQUIRE_DB=1` 则失败。

CI（`.github/workflows/ci.yml`）在 PostgreSQL 18 服务容器上依次运行 `make lint`、`make check`、`make race` 和 `make wasm`，并安装 sqlc 以执行 `sqlc diff` 一致性测试。

## License

[MIT](LICENSE)
