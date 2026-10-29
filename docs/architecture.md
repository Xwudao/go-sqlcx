# go-sqlcx 架构设计

本文记录 go-sqlcx 的技术调研结论、设计边界、关键决策与已知风险。所有结论都在 Go 1.27.3、sqlc v1.31.1、pgx v5.11.0、PostgreSQL 18 上实际验证过；验证方式在各节注明。

## 1. 定位

go-sqlcx 是 sqlc 的伴生扩展：在不修改 sqlc、不 fork sqlc、不改动 sqlc 生成文件的前提下，为 sqlc 已生成的 Model 增加类型安全的动态 SQL 能力。

- **保留**：SQL First、sqlc 原生 Model / 枚举 / nullable 类型 / overrides / rename、sqlc 的静态查询方法。
- **不做**：模型生命周期、脏字段追踪、关联预加载、自动迁移、连接池或事务管理。
- **范围**：仅 PostgreSQL，仅 pgx/v5（`*pgxpool.Pool`、`*pgx.Conn`、`pgx.Tx`），sqlc 配置版本 `"2"`。

## 2. 技术调研结论

### 2.1 sqlc 插件协议（阅读 sqlc v1.31.1 源码验证）

| 事实 | 来源 | 影响 |
|---|---|---|
| 插件收到 `GenerateRequest`：`Settings`、`Catalog`（schema/表/列/枚举）、`Queries`（文本、列、参数、注释）、`plugin_options`、`global_options` | `protos/plugin/codegen.proto` | 表结构元数据可完全来自 sqlc，业务无需重复声明 |
| 插件**拿不到** `gen.go` 的 overrides / rename，也拿不到任何 Go 类型 | `internal/cmd/generate.go` 中插件分支只传本插件 options | 不能在插件里推断 Go 类型 → 需要 Model Linker |
| 所有 codegen 目标在 `errgroup` 中**并发**执行，全部完成后才统一写文件 | `internal/cmd/process.go` `processQuerySets` | 插件运行时 `models.go` 可能尚未生成或是旧版 → 插件不能读取 Go 代码 |
| process 插件以 `cmd /plugin.CodegenService/Generate` 调用，环境变量被清空，工作目录继承自 sqlc | `internal/ext/process/gen.go` | 插件不应依赖 cwd 或环境 |
| `Settings.schema` 是**相对 sqlc.yaml** 的原始路径 | `internal/cmd/shim.go` | 插件无法可靠定位 schema 文件 → DDL 分析放到 link 阶段 |
| catalog 不包含主键/唯一约束 | `codegen.proto` 的 `Table` 只有列 | 分页稳定性校验需要的唯一键由 link 阶段从 DDL 解析 |
| WASM 插件经 wazero 运行，同样接收 protobuf | `internal/ext/wasm` | 只要插件纯计算、无文件访问，同一份代码可编为 `wasip1` |

### 2.2 sqlc 的 pgx/v5 Go 代码（读 `internal/codegen/golang` 并以 `internal/testdb` 实测）

- 表 Model 的字段顺序与表列顺序一致；结构体名 = `StructName(Singular(表名))`，非默认 schema 加 `schema_` 前缀；字段名 = `StructName(列名)`，受 `rename`、`initialisms` 影响。
- nullable：`pgtype.Text/Int4/...`；`timestamptz`、`date`、`uuid`、`numeric` 不论是否可空都是 `pgtype.*`；枚举可空时为 `Null<Enum>{<Enum>, Valid}`；`emit_pointers_for_null_types` 或 overrides 可变成 `*T` / 自定义类型。
- `emit_db_tags: true` 时字段带 `db:"列名"`，可精确对应列。
- Query 的 `Params` 结构体字段顺序与 `$1..$n` 一致；`:many` 返回 `[]Row`，结果列与 Model 完全一致时直接复用 Model。

### 2.3 Go 1.27 泛型方法（scratch 程序实测）

- **支持**：具体类型（含泛型类型）上的方法可以声明自己的类型参数，例如 `func (q Select[R]) Into[R2 any](...) Select[R2]`、`func (u Update[M]) Set[V any](c Assignable[M, V], v V) Update[M]`、`func (q Select[R]) Page[P, S Integer](page P, size S) Select[R]`（分页、窗口、下标等整数参数都用 `Integer` 约束接受任意整数类型，内部按 int64/int32 绑定并检查越界）。
- **不支持**：接口方法不能带类型参数（编译错误 `interface method must have no type parameters`），因此泛型方法不能用于接口约束或动态派发。
- **类型推断**：可以经由接口的（包括未导出的）方法签名推断类型参数（Go 1.21 起的规则）。这是 `Set(u.Name, "Tim")` 推断出 `V=string` 并拒绝 `Set(u.Name, 3)`、拒绝跨表 `Set(orders.Note, ...)` 的基础，已用 `internal/testdb/compiletest` 固化。
- 系统自带的 Go 1.26 `gofmt` 无法解析泛型方法，必须使用 1.27 工具链的 `gofmt`（Makefile 已处理）。

### 2.4 PostgreSQL 解析

- 选用 `github.com/wasilibs/go-pgquery`：真正的 PostgreSQL 解析器（libpg_query）编译成 WASM、由 wazero 执行，**无 cgo**，API 与 `pganalyze/pg_query_go/v6` 一致，sqlc 在禁用 cgo 时也使用它。提供 `Parse`、`Deparse`、`Scan`（词法 token，含 `PARAM` 位置）。
- 无法复用 sqlc 的内部 AST/IR：它们位于 `internal/` 包，不可导入；也不 fork。
- 运行时 SQL 构造不解析 SQL，而是自有的结构化 AST（`internal/ast`）直接渲染。

### 2.5 现有项目对比（基于调研者已有知识，本次未联网核实最新状态）

| 项目 | 与本项目的差异 |
|---|---|
| sqlc 本身 | 只有静态查询；动态条件需写 `sqlc.narg` + `($1 IS NULL OR ...)` 或多份查询 |
| Squirrel / goqu | 字符串列名、`interface{}` 值，无列类型检查，不复用 sqlc Model |
| go-jet | 从数据库生成自己的模型和类型，与 sqlc Model 是两套体系 |
| bob / sqlboiler | 偏 ORM，有自己的代码生成与模型 |

没有发现“复用 sqlc 已生成 Model 并按列类型约束操作符”的现成方案，因此实现本项目。

## 3. 总体架构

```
schema/*.sql, queries/*.sql, sqlc.yaml
        │
        ▼  sqlc generate（并发执行）
 ┌──────────────────────┐        ┌────────────────────────────┐
 │ gen.go（sqlc 内置）    │        │ codegen: sqlc-dynamic 插件  │
 │ → db/models.go 等     │        │ → dynamic/sqlcx.manifest.json│
 └──────────────────────┘        └────────────────────────────┘
        │                                   │
        └──────────────┬────────────────────┘
                       ▼  sqlc-dynamic link
        ┌───────────────────────────────────────────┐
        │ Model Linker                               │
        │  · go/packages + go/types 读取 db 包        │
        │  · 表 → Model、列 → 字段（严格校验）          │
        │  · 字段 Go 类型 T → 比较值类型 V、列类别      │
        │  · libpg_query 解析 DDL 得唯一键              │
        │  · libpg_query 分析 sqlc query → 动态变体     │
        └───────────────────────────────────────────┘
                       ▼
        dynamic/sqlcx_gen.go（只依赖 db 包与 sqlcx 运行时）
                       ▼
        sqlcx 运行时：AST、构建器、参数、执行、扫描
```

`sqlc-dynamic generate` = `sqlc generate` + `sqlc-dynamic link`，是推荐的单命令入口；也可以作为 `//go:generate sqlc-dynamic generate`。

### 3.1 为什么是两段式

插件阶段无法读取 Go 代码（2.1 中的并发与写入时序），也拿不到 overrides；而 Go 侧又无法得到 sqlc 的 catalog（sqlc 编译器是 internal 包）。清单文件 `sqlcx.manifest.json` 把两者解耦：

- 插件只做“请求 → 清单”的纯转换：确定性、无文件访问，因此可原样编译为 WASM（已用 sqlc 的 wasm runner 验证，输出与 process 插件一致）。
- link 阶段在 sqlc 写完文件之后运行，读取的是**实际生成**的代码，执行顺序天然正确且可重复。
- 清单被 sqlc 当作普通生成文件，`sqlc diff` / `sqlc verify` 也覆盖它（`TestSqlcPluginRoundTrip` 用真实 sqlc 验证）。

### 3.2 Model Linker 的映射规则

1. **表 → Model**：按 sqlc 的命名规则（`StructName`、单数化、schema 前缀、`rename`、`initialisms`、`emit_exact_table_names`、`inflection_exclude_table_names`，读自同一个 `sqlc.yaml`）求出候选名，再用 go/types 查找。
2. **严格校验**：字段数必须等于列数；有 `db` tag 时 tag 必须等于列名，否则字段名必须等于 sqlc 规则下的名字。任何不一致直接报错并建议开启 `emit_db_tags`，**绝不猜测**（`TestLinkerErrors` 覆盖了 rename 不一致的情况）。
   - 只移植了 sqlc 的“命名”函数（约 40 行），没有移植类型推断；类型完全来自 go/types。命名移植若与未来 sqlc 版本出现偏差，会被严格校验发现。开启 `emit_db_tags` 可完全绕开命名移植。
3. **类型**：字段类型 `T` 原样使用（overrides、指针、自定义类型、`time.Time` 等都自动生效）。比较值类型 `V` 只由 `T` 决定：`*X→X`；`pgtype.Text→string`、`Int2/4/8→int16/32/64`、`Float4/8`、`Bool`、`Date/Timestamp/Timestamptz→time.Time`、`UUID→[16]byte`；`sql.Null*` 同理；sqlc 的 `Null<Enum>→<Enum>`；其余 `V=T`。因为 `V` 不依赖可空性，`users.id`（NOT NULL）和 `orders.user_id`（可空）可以直接比较。
4. **类别**：由 PostgreSQL 类型决定可用操作符（见 4.2），再结合 `V` 校正（例如 text 列被 override 成非字符串类型时不提供 `LIKE`）。
5. **冲突处理**：多 schema 同名表（`public.users`→`Users`，`audit.users`→`AuditUsers`）、生成名重复（报错）、列字段名与描述符方法同名（加 `Col` 后缀并在 GoDoc 说明）。
6. **构建依赖**：输出包只导入 db 包和运行时；若 db 包（传递地）导入输出包则报错以避免循环；输出目录必须与 db 目录不同。

### 3.3 sqlc Query 动态变体

对 opt-in 的查询（`-- sqlcx:dynamic` 注释，或插件选项 `queries`）生成：

```sql
SELECT "q"."col1", ... FROM (<sqlc 原查询>) AS "q" WHERE <动态> ORDER BY <动态> LIMIT <动态>
```

- 用 libpg_query 解析原查询：必须恰好一条 `SELECT`，不能有集合运算、`VALUES`、`INTO`、`LIMIT/OFFSET`（否则动态过滤会在 LIMIT 之后生效，改变语义）、行锁；输出列名必须唯一且非空。
- 原 `ORDER BY` 若只引用输出列，则从**解析树**中移除并 `Deparse`，再作为外层默认排序；`DISTINCT ON` 依赖原排序，因此拒绝；表达式排序、`USING` 拒绝。
- 用 PostgreSQL 词法器定位 `$n`（字符串里的 `$1` 不会被误认），生成“文本片段 + 参数编号”模板；运行时把原参数重新编号进外层语句，同一原参数复用同一编号。
- 无法安全改写时 link 直接报错并说明原因；没有任何基于关键字字符串查找的改写。
- 原生 sqlc 方法完全不受影响；变体的参数签名、Row 类型都取自 sqlc 生成的方法签名。

## 4. 运行时设计（包 `sqlcx`）

### 4.1 为什么是单一公开包

需求建议了 `expr/ query/ schema/ scan/` 等目录。实现时合并为一个公开包 `sqlcx`，内部 AST 位于 `internal/ast`，原因是**密封接口**：`Expr`、`Typed[V]`、`Assignable[M,V]`、`NullableColumn[M]` 等接口依赖未导出方法，外部代码（包括生成代码）无法伪造表达式；若拆成多个包，这些方法必须导出，内部 AST 就会泄漏到公开 API。生成代码只调用导出的构造函数。

### 4.2 类型体系

表达式类别（嵌入组合，方法逐级增加）：

| 类别 | PostgreSQL 类型 | 额外操作 |
|---|---|---|
| `Value[V]` | json 以外无 `=` 的类型（xml、几何） | IS NULL、IS DISTINCT FROM、Cast、排序 |
| `Comparable[V]` | 未知/自定义类型 | `= <> IN NOT IN ANY ALL`、子查询 IN |
| `Ordered[V]` | 时间、uuid、枚举、inet、bytea… | `> >= < <= BETWEEN` |
| `Text[V]` | text、varchar、char、citext | `LIKE ILIKE SIMILAR ~ ~*`、转义的 `Contains/HasPrefix`、`||` |
| `Number[V]` | 整数、浮点、numeric | 四则运算、`Increment/Decrement`、`Sum/Avg` |
| `Boolean[V]`（`Cond = Boolean[bool]`） | bool | 可直接作为条件、IS TRUE… |
| `JSON[V]` | json | `-> ->> #> #>>` |
| `JSONB[V]` | jsonb | 以上 + `@> <@ ? ?| ?& @? @@ || - #-`、`jsonb_set` |
| `Array[V,E]` | 数组 | `@> <@ &&`、`elem = ANY(col)`、`cardinality`、`array_append`… |

列类型 = 类别 + 列信息：`TextColumn[M, T, V, N]`，其中 `M` 为 sqlc Model，`T` 为字段类型，`V` 为比较值类型，`N` 为幻影类型 `NotNull`/`Nullable`。

编译期保证（`internal/testdb/compiletest` 中 25 个用例逐一验证编译失败）：

- 值类型错误：`u.Age.Eq("18")`、`Set(u.Name, 3)`、`u.Tags.Has(1)`；
- 跨表：`Users.Update().Set(Orders.Note, ...)`；
- 对 NOT NULL 列 `SetNull`（`N` 不匹配 `nullability(Nullable)` 方法）；
- 类别不支持的操作：数值列 `Like`、枚举 `ILike`、json 列 `HasKey`、文本 `Add`、对文本 `Sum`、对 jsonb `Max`；
- 比较两个类型不同的列：`u.ID.EqExpr(u.Name)`、`int4` 与 `int8` 列比较需要显式 `Cast`；
- 投影字段类型与列扫描类型不一致、混用不同 Row 类型的投影、把表达式当作 SET 目标；
- `Over` 用在列上（只接受表达式类别）。

### 4.3 投影与扫描

- `Table.Query()` 返回 `Select[M]`，扫描目标来自生成的 `func(*M) []any { return []any{&m.ID, ...} }`，与 sqlc 自己的 `rows.Scan(&i.ID, ...)` 等价，零反射（`TestModelScannerMatchesSqlc` 用 `reflect.DeepEqual` 对比 sqlc 方法的结果）。
- 部分字段查询必须显式投影到 DTO：`Into(sqlcx.Bind(col, func(*R) *S))`。`S` 必须等于列的扫描类型，由编译器检查；**不存在返回零值填充的不完整 Model 的 API**。
- `Into` 是泛型方法：`Select[R].Into[R2]` 保留所有子句、改变结果类型。
- 外连接：`BindNull`（扫描到 `**S`）、`EmbedNull`（整个 Model 为 `nil`；“部分为 NULL 的 NOT NULL 列”报错）。把 NULL 扫进非指针目标时 pgx 报错，不会静默为零值（集成测试验证）。
- 每行先清零目标行再扫描，保证不同行之间不共享引用类型。

### 4.4 SQL AST 与安全

- 参数与文本严格分离：所有值都是 `$n` 参数；`$n` 在渲染时按文本顺序分配，子查询、CTE、派生表、动态变体模板中的编号都正确（`TestParamNumberingAcrossSubqueries`、`TestTemplateRenumbering`）。超过 65535 个参数时报错。
- 标识符一律双引号转义；表/列名来自生成代码。调用方提供的别名、CTE 名、`Ref` 列名、函数名、类型名都经过白名单正则校验；排序字段通过 `Sorter` 白名单映射，未知键报错。
- 运算符优先级按 PostgreSQL 文档建模，必要处自动加括号。
- NULL：空 `IN` → `FALSE`、空 `NOT IN` → `TRUE`；提供 `IsDistinctFrom`；文档说明 `<>`、`NOT IN (子查询)` 的三值逻辑陷阱。
- `UPDATE` / `DELETE` 没有 WHERE（含“所有可选条件都为空”）时返回 `ErrNoWhere`，必须显式 `AllowFullTable()`。
- 构建器不可变（值语义 + 写时复制切片），AST 节点创建后不再修改，渲染不修改节点；共享基础查询可在多个 goroutine 中派生（`TestConcurrentBuild` 在 `-race` 下运行）。
- 同一结构总是渲染出相同 SQL（golden 测试会二次渲染比较），利于 pgx 语句缓存。
- **Raw 信任边界**：`Raw` / `RawCond` 的 SQL 文本原样插入，必须是开发者写的常量；`?` 为占位符（字符串、注释、带引号标识符、美元引号内的 `?` 不计），`??` 表示字面 `?`；拒绝显式 `$n`；参数一律绑定。

### 4.5 执行

最小接口 `DB`（`Exec`/`Query`/`QueryRow`），与 sqlc 的 `DBTX` 相同，`*pgxpool.Pool`、`*pgx.Conn`、`pgx.Tx` 直接满足。`All`、`One`（多行返回 `ErrTooManyRows`，自动 `LIMIT 2`）、`First`（要求 ORDER BY）、`Rows`（`iter.Seq2`）、`Count`、`Exists`、`Exec`、`Build`。pgx 错误原样返回（`pgx.ErrNoRows`、`*pgconn.PgError`）。

### 4.6 分页稳定性

link 阶段从 DDL（`CREATE TABLE` 内联/表级约束、`ALTER TABLE ADD/DROP CONSTRAINT`、非部分且非表达式的 `CREATE UNIQUE INDEX`、表/列/约束/索引重命名、`DROP`）提取唯一键，生成到表描述中，也可用插件选项 `unique_keys` 补充。`SeekAfter`（keyset）与 `RequireStableOrder` 要求 ORDER BY 覆盖**每个**来源的一个“全部列 NOT NULL”的唯一键；keyset 还要求排序列 NOT NULL。规则是保守的：多对一 JOIN 也会被要求，确认安全时用 `AssumeStableOrder()`。

## 5. 风险与限制

| 风险/限制 | 处理 |
|---|---|
| 命名规则移植与未来 sqlc 版本偏差 | 严格校验，出错即停；建议 `emit_db_tags: true` |
| DDL 唯一键提取是 sqlc catalog 之外的第二次解析 | 只提取唯一键；忽略 `search_path`；找不到的键丢弃；可手动补充 |
| 上下文不足时 PostgreSQL 把参数推断为 text（如 select 列表中的裸参数） | 文档要求显式 `Cast`（递归 CTE 测试即如此） |
| `Over`/`Filter` 用在非聚合函数上 | 只能运行时（Build）报错，Go 类型系统表达代价过高 |
| `Ref` 引用派生表/CTE 列时按名字 | 名字校验 + 由调用方选择类别，PostgreSQL 校验存在性 |
| JOIN 条件引用未出现在 FROM 中的表 | 不在编译期检查，由 PostgreSQL 报错 |
| 动态变体不支持 `sqlc.embed`、`sqlc.slice`、`emit_methods_with_db_argument` | link 明确报错 |
| 域类型、扩展类型 | 归为 `Comparable`，可用 `Cast` + `As*` 转换类别 |
| 仅支持 sqlc 配置 v2、引擎 postgresql、`sql_package: pgx/v5` | 配置读取时校验 |

## 6. 目录结构

```
cmd/sqlc-dynamic/      插件入口 + link/generate 命令（main_wasip1.go 为 WASM 入口）
internal/plugin/       GenerateRequest → 清单
internal/manifest/     清单格式
internal/config/       读取 sqlc.yaml
internal/linker/       go/types 链接、命名、类型映射
internal/postgres/     类型类别、DDL 唯一键、query 变体分析（libpg_query）
internal/codegen/      生成 Go 源码（确定性、gofmt）
internal/ast/          SQL AST 与渲染
sqlcx/                 公开运行时 API
internal/testdb/       真实 sqlc 项目：golden、集成、编译失败、生成一致性测试
examples/sqlc-pgx/     可运行示例
bench/                 与 pgx / sqlc / Squirrel 的对照基准（独立 module）
```
