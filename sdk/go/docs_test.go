package simplefin

import (
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// Editor support is a stated requirement of this SDK. gopls renders a symbol's
// doc comment as its hover text, so an exported symbol without one is a symbol
// that shows nothing in an editor.
//
// This parses the package's own source and asserts the comments are there,
// which is the Go-side counterpart to the Node target's language-service
// probes.

func packageDocs(t *testing.T) *doc.Package {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}

	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files[name] = parsed
	}
	if len(files) == 0 {
		t.Fatal("no package source files found")
	}

	pkg, err := doc.NewFromFiles(fset, values(files), "github.com/andrepato/simplefin-sdk/sdk/go")
	if err != nil {
		t.Fatalf("building package docs: %v", err)
	}
	return pkg
}

func TestExportedTypesAreDocumented(t *testing.T) {
	d := packageDocs(t)

	seen := map[string]bool{}
	for _, typ := range d.Types {
		seen[typ.Name] = true
		if strings.TrimSpace(typ.Doc) == "" {
			t.Errorf("type %s has no doc comment; it would hover blank in an editor", typ.Name)
		}
	}

	for _, want := range []string{
		"Account", "AccountSet", "Connection", "Transaction", "Error",
		"Info", "Currency", "OrgV1", "ErrorCode", "ProtocolVersion",
		"GetAccountsParams", "Client", "HTTPError",
	} {
		if !seen[want] {
			t.Errorf("type %s is missing from the package", want)
		}
	}
}

func TestExportedStructFieldsAreDocumented(t *testing.T) {
	d := packageDocs(t)

	// Every field of these types is part of the data a caller reads, so each
	// one needs hover text.
	want := map[string]bool{
		"Account": true, "AccountSet": true, "Connection": true,
		"Transaction": true, "Error": true, "Info": true,
		"Currency": true, "OrgV1": true, "GetAccountsParams": true,
	}

	for _, typ := range d.Types {
		if !want[typ.Name] {
			continue
		}
		for _, spec := range typ.Decl.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					if !name.IsExported() {
						continue
					}
					if field.Doc == nil || strings.TrimSpace(field.Doc.Text()) == "" {
						t.Errorf("%s.%s has no doc comment", typ.Name, name.Name)
					}
				}
			}
		}
	}
}

// The doc text must be the protocol's own wording, not a restatement, and the
// two warnings that prevent real bugs must survive into the hover popup.
func TestDocTextCarriesTheSpecWording(t *testing.T) {
	d := packageDocs(t)

	fieldDoc := func(typeName, fieldName string) string {
		for _, typ := range d.Types {
			if typ.Name != typeName {
				continue
			}
			for _, spec := range typ.Decl.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range st.Fields.List {
					for _, name := range field.Names {
						if name.Name == fieldName && field.Doc != nil {
							return strings.Join(strings.Fields(field.Doc.Text()), " ")
						}
					}
				}
			}
		}
		return ""
	}

	cases := []struct {
		typeName, fieldName, want string
	}{
		{"Account", "AvailableBalance", "available balance of the account"},
		{"Account", "AvailableBalance", "Never parse it as a float"},
		{"Account", "BalanceDate", "zero or less means absent"},
		{"Transaction", "Posted", "If the transaction is pending, this may be 0"},
		{"Transaction", "Amount", "Never parse it as a float"},
		{"Account", "ConnID", "ID of the account's Connection"},
		{"GetAccountsParams", "Pending", "pending transactions are NOT included"},
	}

	for _, tc := range cases {
		got := fieldDoc(tc.typeName, tc.fieldName)
		if got == "" {
			t.Errorf("%s.%s has no doc comment", tc.typeName, tc.fieldName)
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s.%s doc = %q, want it to contain %q", tc.typeName, tc.fieldName, got, tc.want)
		}
	}
}

// values returns the parsed files in a stable order, so a failure names the
// same file on every run.
func values(m map[string]*ast.File) []*ast.File {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)

	out := make([]*ast.File, 0, len(names))
	for _, n := range names {
		out = append(out, m[n])
	}
	return out
}
