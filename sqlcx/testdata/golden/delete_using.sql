DELETE FROM "orders" USING "users" WHERE "users"."id" = "orders"."user_id" AND "users"."status" = $1 RETURNING "orders"."id", "orders"."user_id", "orders"."amount", "orders"."quantity", "orders"."note", "orders"."created_at"
-- args: []interface {}{"banned"}
