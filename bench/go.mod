module github.com/Xwudao/go-sqlcx/bench

go 1.27

toolchain go1.27.3

replace github.com/Xwudao/go-sqlcx => ../

require (
	github.com/Masterminds/squirrel v1.5.4
	github.com/Xwudao/go-sqlcx v0.0.0-00010101000000-000000000000
	github.com/jackc/pgx/v5 v5.11.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/lann/builder v0.0.0-20180802200727-47ae307949d0 // indirect
	github.com/lann/ps v0.0.0-20150810152359-62de8c46ede0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)
