WITH RECURSIVE "tree" ("id", "depth") AS ((SELECT "categories"."id", CAST($1 AS integer) FROM "categories" WHERE "categories"."parent_id" IS NULL) UNION ALL (SELECT "categories"."id", "tree"."depth" + $2 FROM "categories" JOIN "tree" ON "categories"."parent_id" = "tree"."id")) SELECT "tree"."id", "tree"."depth" FROM "tree"
-- args: []interface {}{0, 1}
