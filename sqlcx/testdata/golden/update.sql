UPDATE "users" SET "name" = $1, "nickname" = NULL, "age" = "users"."age" + $2, "meta" = jsonb_set("users"."meta", CAST($3 AS text[]), CAST($4 AS jsonb), TRUE) WHERE "users"."id" = $5
-- args: []interface {}{"Tim", 1, []string{"a", "b"}, jsontext.Value{0x74, 0x72, 0x75, 0x65}, 1}
