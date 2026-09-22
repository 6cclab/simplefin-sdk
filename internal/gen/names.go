package gen

import "strings"

// goInitialisms are the words Go convention (and golint/gopls) expects to be
// fully capitalized. Without this, camelCase field names would generate
// Go identifiers like ConnId and SfinUrl, which read as wrong to any Go
// developer and would be flagged by linters.
var goInitialisms = map[string]string{
	"id":   "ID",
	"url":  "URL",
	"api":  "API",
	"http": "HTTP",
	"json": "JSON",
	"sfin": "Sfin", // not an initialism; listed so it is never mangled
}

// splitCamel breaks a lowerCamelCase identifier into its words.
// "availableBalance" -> ["available", "Balance"]
func splitCamel(s string) []string {
	if s == "" {
		return nil
	}
	var words []string
	start := 0
	for i := 1; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			words = append(words, s[start:i])
			start = i
		}
	}
	return append(words, s[start:])
}

// GoName converts a camelCase IR name to an exported Go identifier, applying
// Go's initialism convention: connId -> ConnID, sfinUrl -> SfinURL, id -> ID.
func GoName(s string) string {
	var b strings.Builder
	for _, w := range splitCamel(s) {
		if w == "" {
			continue
		}
		if initialism, ok := goInitialisms[strings.ToLower(w)]; ok {
			b.WriteString(initialism)
			continue
		}
		b.WriteString(strings.ToUpper(w[:1]))
		b.WriteString(strings.ToLower(w[1:]))
	}
	return b.String()
}

// TSName returns the TypeScript identifier for a field or method. The IR
// already stores camelCase, which is what TypeScript wants, so this is
// identity — it exists so templates never hardcode the assumption and a future
// language can swap the rule.
func TSName(s string) string { return s }

// tsTypeRenames maps IR type names that would collide with a TypeScript or
// JavaScript global. `Error` is the important one: exporting an interface
// called Error would shadow the built-in inside every consuming module, so the
// TS target exports SimpleFinError instead. Go has no such conflict and keeps
// the spec's name.
var tsTypeRenames = map[string]string{
	"Error": "SimpleFinError",
}

// TSTypeName returns the TypeScript name for an IR type.
func TSTypeName(s string) string {
	if renamed, ok := tsTypeRenames[s]; ok {
		return renamed
	}
	return s
}

// PascalCase uppercases the first letter without applying initialisms. Used
// for type names, which are already PascalCase in the IR.
func PascalCase(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ConstName builds an exported constant identifier from a prefix and a name,
// e.g. ("ErrCode", "ConnectionAuth") -> "ErrCodeConnectionAuth".
func ConstName(prefix, name string) string { return prefix + PascalCase(name) }
