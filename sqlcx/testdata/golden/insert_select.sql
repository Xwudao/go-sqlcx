INSERT INTO "audit"."users" ("user_id", "action") SELECT "users"."id", $1 FROM "users"
-- args: []interface {}{"seen"}
