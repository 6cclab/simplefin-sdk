package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/6cclab/simplefin-sdk/internal/ir"
)

// docWidth is the column the doc renderers wrap at, chosen to keep generated
// comments readable in a side-by-side editor split.
const docWidth = 72

// goType maps an IR field to its Go type.
//
// Optionality is expressed differently per kind, deliberately matching what a
// Go developer would hand-write:
//
//   - numericString: *string when optional. The difference between "no
//     available balance reported" and "an available balance of empty string"
//     is real for money, so it gets a pointer.
//   - string: plain string with omitempty. "" is an adequate absent value for
//     a name or a URL.
//   - epochSeconds: plain int64. The IR already defines <= 0 as absent, so a
//     pointer would add a second way to say the same thing.
//   - bool: plain bool. The protocol documents absent as equivalent to false.
func goType(f ir.Field) string {
	switch f.Kind {
	case ir.KindString:
		if f.Enum == "errorCodes" {
			return "ErrorCode"
		}
		return "string"
	case ir.KindNumericString:
		if !f.Required {
			return "*string"
		}
		return "string"
	case ir.KindEpochSeconds:
		return "int64"
	case ir.KindBool:
		return "bool"
	case ir.KindExtra:
		return "map[string]any"
	case ir.KindAny:
		return "any"
	case ir.KindArrayString:
		return "[]string"
	case ir.KindArrayRef:
		return "[]" + f.Elem
	case ir.KindRef:
		return f.Elem
	case ir.KindEnum:
		if f.Elem == "supportedVersions" {
			return "ProtocolVersion"
		}
		if f.Enum == "errorCodes" || f.Elem == "errorCodes" {
			return "ErrorCode"
		}
		return "string"
	default:
		return "any"
	}
}

// tsType maps an IR field to its TypeScript type. Optionality is carried by
// the `?:` marker the template emits, so this returns the bare type.
func tsType(f ir.Field) string {
	switch f.Kind {
	case ir.KindString:
		if f.Enum == "errorCodes" {
			return "ErrorCode"
		}
		return "string"
	case ir.KindNumericString:
		return "string"
	case ir.KindEpochSeconds:
		return "number"
	case ir.KindBool:
		return "boolean"
	case ir.KindExtra:
		return "Record<string, unknown>"
	case ir.KindAny:
		return "unknown"
	case ir.KindArrayString:
		return "string[]"
	case ir.KindArrayRef:
		return TSTypeName(f.Elem) + "[]"
	case ir.KindRef:
		return TSTypeName(f.Elem)
	case ir.KindEnum:
		if f.Elem == "supportedVersions" {
			return "ProtocolVersion"
		}
		if f.Enum == "errorCodes" || f.Elem == "errorCodes" {
			return "ErrorCode"
		}
		return "string"
	default:
		return "unknown"
	}
}

// goTag builds the struct tag. Synthetic fields carry no wire representation
// and are excluded from both marshalling directions.
func goTag(f ir.Field) string {
	if f.Synthetic || f.Wire == "" {
		return "`json:\"-\"`"
	}
	tag := f.Wire
	if !f.Required {
		tag += ",omitempty"
	}
	return fmt.Sprintf("`json:%q`", tag)
}

// goDoc renders a Go doc comment. Go convention (and gopls' hover rendering)
// expects the comment to begin with the symbol it documents.
func goDoc(indent, symbol, doc string) string {
	return renderComment(indent, "// ", joinSymbol(symbol, doc))
}

// tsDoc renders a TSDoc block. VS Code renders these directly in the
// IntelliSense popup, which is the entire reason docs are a first-class field
// in the IR rather than something templates invent.
func tsDoc(indent, doc string) string {
	return tsDocRemark(indent, doc, "")
}

// tsDocRemark renders a single TSDoc block containing the description and, if
// given, an @remarks section.
//
// It must be one block. TypeScript associates only the *last* leading JSDoc
// comment with a declaration, so emitting the description and the remark as
// two adjacent blocks silently drops the description from hover text — which
// is exactly the thing the IR's doc strings exist to deliver.
func tsDocRemark(indent, doc, remark string) string {
	if strings.TrimSpace(doc) == "" && strings.TrimSpace(remark) == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString(indent + "/**\n")

	writeLines := func(text string) {
		for _, l := range wrap(collapse(text), docWidth) {
			if l == "" {
				b.WriteString(indent + " *\n")
				continue
			}
			b.WriteString(indent + " * " + l + "\n")
		}
	}

	if strings.TrimSpace(doc) != "" {
		writeLines(doc)
	}
	if strings.TrimSpace(remark) != "" {
		if strings.TrimSpace(doc) != "" {
			b.WriteString(indent + " *\n")
		}
		b.WriteString(indent + " * @remarks\n")
		writeLines(remark)
	}

	b.WriteString(indent + " */")
	return b.String()
}

// tsDocBody renders doc text as the interior lines of a TSDoc block that the
// template opens and closes itself. Used where a comment mixes generated prose
// with hand-written lines.
func tsDocBody(prefix, doc string) string {
	if strings.TrimSpace(doc) == "" {
		return ""
	}
	var b strings.Builder
	for i, l := range wrap(collapse(doc), docWidth) {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(prefix + l)
	}
	return b.String()
}

func joinSymbol(symbol, doc string) string {
	doc = collapse(doc)
	if symbol == "" {
		return doc
	}
	if doc == "" {
		return symbol
	}
	// Avoid "Account Account is ..." when the doc already leads with the name.
	if strings.HasPrefix(doc, symbol+" ") {
		return doc
	}
	return symbol + " " + doc
}

func renderComment(indent, prefix, text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	lines := wrap(text, docWidth)
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		if l == "" {
			b.WriteString(indent + strings.TrimRight(prefix, " "))
			continue
		}
		b.WriteString(indent + prefix + l)
	}
	return b.String()
}

// collapse folds the YAML block scalar's incidental newlines into single
// spaces so the renderers can re-wrap to their own width.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func wrap(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var (
		lines []string
		cur   strings.Builder
	)
	for _, w := range words {
		if cur.Len() > 0 && cur.Len()+1+len(w) > width {
			lines = append(lines, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(w)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}

// tsUnion renders a TypeScript string-literal union.
//
// When openEnded is true the union gains `(string & {})`, which keeps the
// literals in the autocomplete popup while still accepting arbitrary strings.
// That combination is required here: the protocol says consumers must tolerate
// unknown subcodes, but an unadorned `string` would offer no completion at all.
func tsUnion(values []string, openEnded bool) string {
	quoted := make([]string, 0, len(values)+1)
	for _, v := range values {
		quoted = append(quoted, strconv.Quote(v))
	}
	if openEnded {
		quoted = append(quoted, "(string & {})")
	}
	return strings.Join(quoted, " | ")
}

// errConst builds the exported identifier for an error code constant.
func errConst(prefix string, c ir.ErrorCode) string {
	return ConstName(prefix, c.Const)
}

func strconv_Quote(s string) string { return strconv.Quote(s) }
