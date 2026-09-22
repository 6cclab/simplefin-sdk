package gen

import (
	"slices"
	"strings"

	"github.com/andrepato/simplefin-sdk/internal/ir"
)

// The view model flattens the IR into exactly what templates need, so the
// templates stay declarative. Anything requiring a conditional or an index
// calculation is resolved here, in Go, where it can be unit tested.

// View is the root template context.
type View struct {
	Spec    *ir.Spec
	Package string
	Lang    string
	Header  string

	Types   []TypeView
	V1Types []TypeView

	// ErrorCodes is the protocol's error table, sorted for deterministic output.
	ErrorCodes []ErrorCodeView
	// ErrorCodeLiterals is every code as a bare string, for building unions.
	ErrorCodeLiterals []string
	// PrefixLiterals is every declared prefix, for building unions.
	PrefixLiterals []string
	// NakedPrefixes maps a prefix ("con") to its naked code ("con.").
	NakedPrefixes []PrefixView

	Statuses []ir.Status

	// GetAccounts is the parameterized endpoint, pulled out because every
	// language template needs it by name.
	GetAccounts EndpointView
	Endpoints   []EndpointView

	SupportedVersions []string
	DefaultVersion    string

	// ConnIDFrom is the ordered fallback chain used to synthesize a conn_id
	// for a protocol v1 response, which carries none.
	ConnIDFrom []StepView
	// NormalizationDoc explains the v1 to v2 conversion.
	NormalizationDoc string
	// GeneralNaked is the naked general error code ("gen."), used as the code
	// for v1's plain-string errors, which carry no code of their own.
	GeneralNaked string
	// GeneralNakedConst is that same code's constant name.
	GeneralNakedConst string
}

// TypeView is one generated struct/interface.
type TypeView struct {
	Name      string
	TSName    string // may differ from Name to avoid shadowing a JS global
	Doc       string
	Recv      string // Go receiver variable
	Fields    []FieldView
	Accessors []AccessorView
	Root      bool
}

// FieldView is one generated field.
type FieldView struct {
	Name     string // camelCase, as the SDK exposes it
	GoName   string
	TSName   string
	Wire     string
	WireV1   string
	Doc      string
	Required  bool
	Optional  bool
	Synthetic bool
	GoType   string
	TSType   string
	GoTag    string
	Kind     ir.Kind
	Elem     string

	// IsMoney marks a numericString. Templates use it to attach the
	// do-not-parse-as-float warning to the hover text.
	IsMoney bool
	// IsEpoch marks an epochSeconds field, whose zero value means absent.
	IsEpoch bool

	// Canonical marks a field that participates in the cross-language
	// conformance comparison. Synthetic fields and open-ended `extra` objects
	// are excluded: their contents are server-specific, so requiring two
	// languages to serialize them identically would test the harness rather
	// than the parser.
	Canonical bool
	// IsRefArray and IsStringArray let templates branch without re-parsing
	// the kind.
	IsRefArray    bool
	IsStringArray bool
	IsOptionalRef bool
	// NeedsStringCast marks a field whose Go type is a named string type
	// (ErrorCode, ProtocolVersion) and so must be converted before being put
	// into an untyped map.
	NeedsStringCast bool
}

// AccessorView is a generated convenience method such as postedTime.
type AccessorView struct {
	Name   string
	GoName string
	TSName string
	Doc    string
	// Steps is the fallback chain, in order. All but the last are guarded;
	// the last is returned directly.
	Steps []StepView
}

// StepView is one link in an accessor's fallback chain.
type StepView struct {
	Field  string
	GoName string
	TSName string
	IsLast bool
}

// ErrorCodeView is one row of the protocol's error table.
type ErrorCodeView struct {
	Code       string
	Const      string
	GoConst    string
	Doc        string
	Prefix     string
	IsNaked    bool
	HasConnID  bool
	HasAcctID  bool
	NakedForm  string
	PrefixName string
}

// PrefixView is a declared error prefix and the naked code it falls back to.
type PrefixView struct {
	Prefix string
	Naked  string
	Name   string
}

// EndpointView is one HTTP operation.
type EndpointView struct {
	Name       string
	GoName     string
	TSName     string
	Method     string
	Path       string
	Auth       bool
	Returns    string
	ReturnsRaw string
	Doc        string
	Params     []ParamView
	HasParams  bool
}

// ParamView is one query parameter, with its serialization rule resolved.
type ParamView struct {
	FieldView
	Serialize string
	Default   string

	IsUnixSeconds bool
	IsOptInOne    bool
	IsRepeated    bool
	IsWithDefault bool
}

// BuildView converts the validated IR into the template context.
func BuildView(spec *ir.Spec, lang, pkg, header string) *View {
	v := &View{
		Spec:              spec,
		Package:           pkg,
		Lang:              lang,
		Header:            header,
		Statuses:          spec.Statuses,
		SupportedVersions: spec.Protocol.SupportedVersions,
		DefaultVersion:    spec.Protocol.DefaultVersion,
	}

	for _, t := range spec.Types {
		v.Types = append(v.Types, buildType(t))
	}
	for _, t := range spec.V1Types {
		v.V1Types = append(v.V1Types, buildType(t))
	}

	nakedByPrefix := map[string]string{}
	for _, c := range spec.ErrorCodeValues() {
		if c.IsNaked() {
			nakedByPrefix[c.Prefix()] = c.Code
		}
	}
	for _, c := range spec.ErrorCodeValues() {
		v.ErrorCodes = append(v.ErrorCodes, ErrorCodeView{
			Code:       c.Code,
			Const:      c.Const,
			GoConst:    ConstName("ErrCode", c.Const),
			Doc:        c.Doc,
			Prefix:     c.Prefix(),
			IsNaked:    c.IsNaked(),
			HasConnID:  hasExtra(c.Extra, "connId"),
			HasAcctID:  hasExtra(c.Extra, "accountId"),
			NakedForm:  nakedByPrefix[c.Prefix()],
			PrefixName: spec.ErrorCodes.Prefixes[c.Prefix()],
		})
		v.ErrorCodeLiterals = append(v.ErrorCodeLiterals, c.Code)
	}
	for prefix, name := range spec.ErrorCodes.Prefixes {
		v.NakedPrefixes = append(v.NakedPrefixes, PrefixView{
			Prefix: prefix,
			Naked:  nakedByPrefix[prefix],
			Name:   name,
		})
	}
	sortPrefixes(v.NakedPrefixes)
	for _, p := range v.NakedPrefixes {
		v.PrefixLiterals = append(v.PrefixLiterals, p.Prefix)
	}

	v.NormalizationDoc = spec.Normalization.Doc
	v.GeneralNaked = nakedByPrefix["gen"]
	for _, c := range v.ErrorCodes {
		if c.Code == v.GeneralNaked {
			v.GeneralNakedConst = c.Const
		}
	}
	for i, name := range spec.Normalization.ConnIDFrom {
		v.ConnIDFrom = append(v.ConnIDFrom, StepView{
			Field:  name,
			GoName: GoName(name),
			TSName: TSName(name),
			IsLast: i == len(spec.Normalization.ConnIDFrom)-1,
		})
	}

	for _, e := range spec.Endpoints {
		ev := buildEndpoint(e)
		v.Endpoints = append(v.Endpoints, ev)
		if e.Name == "getAccounts" {
			v.GetAccounts = ev
		}
	}

	return v
}

func buildType(t ir.Type) TypeView {
	tv := TypeView{
		Name:   t.Name,
		TSName: TSTypeName(t.Name),
		Doc:    t.Doc,
		Recv:   receiverFor(t.Name),
		Root:   t.Root,
	}
	for _, f := range t.Fields {
		tv.Fields = append(tv.Fields, buildField(f))
	}
	for _, a := range t.Accessors {
		av := AccessorView{
			Name:   a.Name,
			GoName: GoName(a.Name),
			TSName: TSName(a.Name),
			Doc:    a.Doc,
		}
		for i, p := range a.Prefer {
			av.Steps = append(av.Steps, StepView{
				Field:  p,
				GoName: GoName(p),
				TSName: TSName(p),
				IsLast: i == len(a.Prefer)-1,
			})
		}
		tv.Accessors = append(tv.Accessors, av)
	}
	return tv
}

func buildField(f ir.Field) FieldView {
	return FieldView{
		Name:     f.Name,
		GoName:   GoName(f.Name),
		TSName:   TSName(f.Name),
		Wire:     f.Wire,
		WireV1:   f.WireV1,
		Doc:      f.Doc,
		Required:  f.Required,
		Optional:  !f.Required,
		Synthetic: f.Synthetic,
		GoType:   goType(f),
		TSType:   tsType(f),
		GoTag:    goTag(f),
		Kind:     f.Kind,
		Elem:     f.Elem,
		IsMoney:  f.Kind == ir.KindNumericString,
		IsEpoch:  f.Kind == ir.KindEpochSeconds,

		Canonical:     !f.Synthetic && f.Kind != ir.KindExtra && f.Kind != ir.KindAny,
		IsRefArray:    f.Kind == ir.KindArrayRef,
		IsStringArray: f.Kind == ir.KindArrayString,
		IsOptionalRef: f.Kind == ir.KindNumericString && !f.Required,

		NeedsStringCast: goType(f) == "ErrorCode" || goType(f) == "ProtocolVersion",
	}
}

func buildEndpoint(e ir.Endpoint) EndpointView {
	ev := EndpointView{
		Name:       e.Name,
		GoName:     GoName(e.Name),
		TSName:     TSName(e.Name),
		Method:     e.Method,
		Path:       e.Path,
		Auth:       e.Auth,
		Returns:    e.Returns,
		ReturnsRaw: e.ReturnsRaw,
		Doc:        e.Doc,
		HasParams:  len(e.Params) > 0,
	}
	for _, p := range e.Params {
		fv := buildField(p)

		// Params differ from model fields on epoch handling. In a response,
		// zero already means absent. In a request, the caller must be able to
		// distinguish "no start date" from "the epoch", so the Go type gains a
		// pointer and the TS type accepts a Date for ergonomics.
		if fv.IsEpoch {
			fv.GoType = "*int64"
			fv.TSType = "Date | number"
		}

		ev.Params = append(ev.Params, ParamView{
			FieldView:     fv,
			Serialize:     p.Serialize,
			Default:       p.Default,
			IsUnixSeconds: p.Serialize == "unixSeconds",
			IsOptInOne:    p.Serialize == "optInOne",
			IsRepeated:    p.Serialize == "repeated",
			IsWithDefault: p.Serialize == "valueWithDefault",
		})
	}
	return ev
}

// receiverFor picks a Go receiver name: the lowercased first letter of the
// type, matching ordinary Go style.
func receiverFor(typeName string) string {
	if typeName == "" {
		return "v"
	}
	return strings.ToLower(typeName[:1])
}

func hasExtra(extra []string, want string) bool {
	return slices.Contains(extra, want)
}

func sortPrefixes(p []PrefixView) {
	for i := 1; i < len(p); i++ {
		for j := i; j > 0 && p[j].Prefix < p[j-1].Prefix; j-- {
			p[j], p[j-1] = p[j-1], p[j]
		}
	}
}
