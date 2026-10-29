module github.com/Xwudao/go-sqlcx

go 1.27

toolchain go1.27.3

// The module zips of these versions contain a stray 34 MB sqlc-dynamic binary.
retract [v0.1.0, v0.1.1]

require (
	github.com/jackc/pgx/v5 v5.9.2
	github.com/jinzhu/inflection v1.0.0
	github.com/pganalyze/pg_query_go/v6 v6.2.2
	github.com/sqlc-dev/plugin-sdk-go v1.23.0
	github.com/wasilibs/go-pgquery v0.0.0-20250409022910-10ac41983c07
	golang.org/x/tools v0.47.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/tetratelabs/wazero v1.11.0 // indirect
	github.com/wasilibs/wazero-helpers v0.0.0-20250123031827-cd30c44769bb // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20230822172742-b8732ec3820d // indirect
	google.golang.org/grpc v1.59.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
