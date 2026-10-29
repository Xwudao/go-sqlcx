package ast

import (
	"fmt"
	"regexp"
	"strings"
)

// MaxIdentLen is PostgreSQL's NAMEDATALEN-1.
const MaxIdentLen = 63

var (
	aliasPattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	funcNamePattern = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*\.)?[A-Za-z_][A-Za-z0-9_]*$`)
	// typeNamePattern accepts PostgreSQL type names such as "text",
	// "timestamp with time zone", "numeric(10,2)", "varchar(20)[]" and
	// schema-qualified names such as "app.money_amount[]".
	typeNamePattern = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*\.)?[A-Za-z_][A-Za-z0-9_]*( [A-Za-z_][A-Za-z0-9_]*)*(\(\d+(,\s*\d+)?\))?(\[\d*\])*$`)
)

// ValidateAlias checks that name is safe to use as a caller-chosen alias
// (table alias, CTE name, column alias). Aliases are always quoted when
// rendered, but restricting them to plain identifiers keeps untrusted input
// out of identifier positions altogether.
func ValidateAlias(name string) error {
	if len(name) > MaxIdentLen {
		return fmt.Errorf("sqlcx: alias %q is longer than %d bytes", name, MaxIdentLen)
	}
	if !aliasPattern.MatchString(name) {
		return fmt.Errorf("sqlcx: invalid alias %q: aliases must match [A-Za-z_][A-Za-z0-9_]*", name)
	}
	return nil
}

// ValidateFuncName checks a (optionally schema-qualified) function name.
func ValidateFuncName(name string) error {
	if !funcNamePattern.MatchString(name) || len(name) > 2*MaxIdentLen+1 {
		return fmt.Errorf("sqlcx: invalid function name %q", name)
	}
	return nil
}

// ValidateTypeName checks a type name used in a cast.
func ValidateTypeName(name string) error {
	if len(name) > 256 || !typeNamePattern.MatchString(name) {
		return fmt.Errorf("sqlcx: invalid type name %q", name)
	}
	return nil
}

// RenderFuncName writes a function name. Plain lower-case names are written
// bare so that built-in functions resolve as usual; other names are quoted.
func RenderFuncName(w *Writer, name string) {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		if i > 0 {
			w.B('.')
		}
		if p == strings.ToLower(p) {
			w.S(p)
		} else {
			w.Ident(p)
		}
	}
}
