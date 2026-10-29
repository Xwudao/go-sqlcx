SELECT "users"."name", sum("orders"."amount") AS "total", rank() OVER (ORDER BY sum("orders"."quantity") DESC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM "users" JOIN "orders" ON "orders"."user_id" = "users"."id" GROUP BY "users"."name" HAVING count(*) > $1
-- args: []interface {}{1}
