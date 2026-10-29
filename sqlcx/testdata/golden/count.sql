SELECT count(*) FROM (SELECT 1 FROM "users" WHERE "users"."age" > $1) AS "sqlcx_count"
-- args: []interface {}{1}
