SELECT "audit"."users"."id", "audit"."users"."user_id", "audit"."users"."action", "audit"."users"."at" FROM "audit"."users" WHERE "audit"."users"."action" = $1
-- args: []interface {}{"x"}
