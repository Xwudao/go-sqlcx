// Package postgres contains the PostgreSQL-specific analysis used by the
// linker: type categories, unique keys from DDL and the structural analysis
// of sqlc queries for dynamic variants. All SQL parsing uses libpg_query
// (via github.com/wasilibs/go-pgquery, a cgo-free build of the real
// PostgreSQL parser).
package postgres

import "strings"

// Category is the operator family available for a column type.
type Category int

// Categories, from the most to the least capable.
const (
	CatComparable Category = iota // = <> IN
	CatValue                      // no equality operator (json, xml, geometric)
	CatOrdered                    // + < > BETWEEN
	CatText                       // + LIKE, ILIKE, ||
	CatNumber                     // + arithmetic
	CatBool                       // + IS TRUE, usable as a condition
	CatJSON                       // json: -> ->> #> #>>
	CatJSONB                      // jsonb: + @> ? ?| ?& || - #- jsonb_set
	CatArray                      // @> <@ && ANY
)

func (c Category) String() string {
	return [...]string{"comparable", "value", "ordered", "text", "number", "bool", "json", "jsonb", "array"}[c]
}

// NormalizeType strips the pg_catalog schema and maps aliases to the
// canonical internal names.
func NormalizeType(schema, name string) string {
	n := strings.ToLower(name)
	if schema == "pg_catalog" || schema == "" {
		n = strings.TrimPrefix(n, "pg_catalog.")
	} else {
		return schema + "." + n
	}
	switch n {
	case "integer", "int", "serial", "serial4":
		return "int4"
	case "smallint", "smallserial", "serial2":
		return "int2"
	case "bigint", "bigserial", "serial8":
		return "int8"
	case "real":
		return "float4"
	case "double precision", "float":
		return "float8"
	case "boolean":
		return "bool"
	case "character varying":
		return "varchar"
	case "character", "char":
		return "bpchar"
	case "decimal":
		return "numeric"
	case "timestamp without time zone":
		return "timestamp"
	case "timestamp with time zone":
		return "timestamptz"
	case "time without time zone":
		return "time"
	case "time with time zone":
		return "timetz"
	}
	return n
}

// Classify returns the category for a normalized scalar type name. isEnum
// reports whether the type is a catalog enum.
func Classify(typ string, isEnum bool) Category {
	if isEnum {
		return CatOrdered
	}
	base := typ
	if i := strings.LastIndexByte(typ, '.'); i >= 0 {
		base = typ[i+1:]
	}
	switch base {
	case "text", "varchar", "bpchar", "citext", "name":
		return CatText
	case "int2", "int4", "int8", "float4", "float8", "numeric", "money", "oid":
		return CatNumber
	case "bool":
		return CatBool
	case "json":
		return CatJSON
	case "jsonb":
		return CatJSONB
	case "date", "time", "timetz", "timestamp", "timestamptz", "interval",
		"uuid", "inet", "cidr", "macaddr", "macaddr8", "bytea", "bit", "varbit", "xid8", "pg_lsn":
		return CatOrdered
	case "xml", "point", "line", "lseg", "box", "path", "polygon", "circle", "void", "any", "anyelement":
		return CatValue
	}
	return CatComparable
}
