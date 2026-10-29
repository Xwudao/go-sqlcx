package linker

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jinzhu/inflection"

	"github.com/Xwudao/go-sqlcx/internal/config"
)

// namer reproduces the two naming functions of sqlc's Go generator
// (StructName and the table-name singularisation). It is used only to find
// candidate structs; every match is then verified structurally against the
// go/types view of the generated package, so a naming divergence produces a
// precise error instead of a wrong mapping.
type namer struct {
	rename      map[string]string
	initialisms map[string]bool
	exact       bool
	exclusions  []string
}

func newNamer(o config.GoOptions) namer {
	n := namer{rename: o.Rename, exact: o.EmitExactTableNames, exclusions: o.InflectionExcludeTableNames, initialisms: map[string]bool{}}
	ins := []string{"id"}
	if o.Initialisms != nil {
		ins = *o.Initialisms
	}
	for _, i := range ins {
		n.initialisms[i] = true
	}
	return n
}

// structName mirrors sqlc's StructName.
func (n namer) structName(name string) string {
	if r := n.rename[name]; r != "" {
		return r
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '_'
	}, name)
	var out strings.Builder
	for _, p := range strings.Split(name, "_") {
		if n.initialisms[p] {
			out.WriteString(strings.ToUpper(p))
		} else {
			out.WriteString(title(p))
		}
	}
	s := out.String()
	if r, _ := utf8.DecodeRuneInString(s); unicode.IsDigit(r) {
		return "_" + s
	}
	return s
}

// title is strings.Title (used by sqlc) restricted to the inputs it sees
// here: words of letters and digits, where only the first rune changes.
func title(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 {
		return s
	}
	return string(unicode.ToTitle(r)) + s[size:]
}

// singular mirrors sqlc's inflection.Singular wrapper.
func (n namer) singular(s string) string {
	for _, e := range n.exclusions {
		if strings.EqualFold(s, e) {
			return s
		}
	}
	switch strings.ToLower(s) {
	case "campus", "meta":
		return s
	case "calories":
		return "calorie"
	case "waves":
		return "wave"
	case "metadata":
		return "metadata"
	}
	return inflection.Singular(s)
}

// tableStruct returns the expected model struct name for a table.
func (n namer) tableStruct(schema, table, defaultSchema string) string {
	name := table
	if schema != defaultSchema {
		name = schema + "_" + table
	}
	if !n.exact {
		name = n.singular(name)
	}
	return n.structName(name)
}

// enumName returns the expected Go type name for an enum.
func (n namer) enumName(schema, enum, defaultSchema string) string {
	name := enum
	if schema != defaultSchema {
		name = schema + "_" + enum
	}
	return n.structName(name)
}
