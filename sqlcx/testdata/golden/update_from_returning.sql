UPDATE "orders" SET "note" = upper("users"."name") FROM "users" WHERE "users"."id" = "orders"."user_id" RETURNING "orders"."id", "orders"."user_id", "orders"."amount", "orders"."quantity", "orders"."note", "orders"."created_at"
-- args: []interface {}(nil)
