// Package ir loads and validates spec/simplefin.yaml, the single source of
// truth for every generated SimpleFIN SDK.
//
// Validation is deliberately strict and loud. A typo in a field kind or a
// dangling type reference must fail the generator rather than silently emit an
// SDK with a wrong or missing field — a silently-dropped field in a financial
// data client looks exactly like a bank that stopped reporting one.
package ir

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind classifies a field's wire semantics. These carry meaning that JSON
// Schema and OpenAPI have no vocabulary for — notably that money is a string
// that must never be parsed as a float, and that an epoch of 0 means absent.
type Kind string

const (
	KindString        Kind = "string"
	KindNumericString Kind = "numericString"
	KindEpochSeconds  Kind = "epochSeconds"
	KindBool          Kind = "bool"
	KindExtra         Kind = "extra"
	KindRef           Kind = "ref"
	KindArrayRef      Kind = "array:ref"
	KindArrayString   Kind = "array:string"
	KindEnum          Kind = "enum"
	KindAny           Kind = "any"
)

// Field is one attribute of a type or one query parameter.
type Field struct {
	Name      string `yaml:"name"`
	Wire      string `yaml:"wire"`
	WireV1    string `yaml:"wireV1"`
	RawKind   string `yaml:"kind"`
	Required  bool   `yaml:"required"`
	Doc       string `yaml:"doc"`
	Enum      string `yaml:"enum"`
	Serialize string `yaml:"serialize"`
	Default   string `yaml:"default"`
	Synthetic bool   `yaml:"synthetic"`

	// Resolved during validation.
	Kind Kind   `yaml:"-"`
	Elem string `yaml:"-"`
}

// Accessor is a generated convenience method, such as the postedTime fallback
// that keeps pending transactions from being dated to 1970.
type Accessor struct {
	Name    string   `yaml:"name"`
	Returns string   `yaml:"returns"`
	Doc     string   `yaml:"doc"`
	Prefer  []string `yaml:"prefer"`
}

// Type is a generated struct/interface.
type Type struct {
	Name      string     `yaml:"name"`
	Doc       string     `yaml:"doc"`
	Root      bool       `yaml:"root"`
	Fields    []Field    `yaml:"fields"`
	Accessors []Accessor `yaml:"accessors"`
}

// Protocol carries top-level metadata about the specification itself.
type Protocol struct {
	Name              string   `yaml:"name"`
	SpecVersion       string   `yaml:"specVersion"`
	SpecURL           string   `yaml:"specUrl"`
	SupportedVersions []string `yaml:"supportedVersions"`
	DefaultVersion    string   `yaml:"defaultVersion"`
	Summary           string   `yaml:"summary"`
}

// Normalization describes how a v1 response becomes the v2 model.
type Normalization struct {
	ConnIDFrom []string `yaml:"connIdFrom"`
	Doc        string   `yaml:"doc"`
}

// ErrorCode is one entry in the protocol's error code table.
type ErrorCode struct {
	Code  string   `yaml:"code"`
	Const string   `yaml:"const"`
	Extra []string `yaml:"extra"`
	Doc   string   `yaml:"doc"`
}

// Prefix returns the code's prefix ("con" for "con.auth").
func (e ErrorCode) Prefix() string {
	if i := strings.Index(e.Code, "."); i >= 0 {
		return e.Code[:i]
	}
	return e.Code
}

// IsNaked reports whether this is a bare prefix such as "con." — the value
// consumers must fall back to when they meet an unknown subcode.
func (e ErrorCode) IsNaked() bool { return strings.HasSuffix(e.Code, ".") }

// ErrorCodes is the whole error table plus its open-endedness rules.
type ErrorCodes struct {
	OpenEnded       bool              `yaml:"openEnded"`
	UnknownFallback string            `yaml:"unknownFallback"`
	Prefixes        map[string]string `yaml:"prefixes"`
	Values          []ErrorCode       `yaml:"values"`
}

// Status maps an HTTP status code to its protocol meaning.
type Status struct {
	Code  int    `yaml:"code"`
	Kind  string `yaml:"kind"`
	Const string `yaml:"const"`
	Doc   string `yaml:"doc"`
}

// Endpoint is one HTTP operation.
type Endpoint struct {
	Name       string  `yaml:"name"`
	Method     string  `yaml:"method"`
	Path       string  `yaml:"path"`
	Auth       bool    `yaml:"auth"`
	Returns    string  `yaml:"returns"`
	ReturnsRaw string  `yaml:"returnsRaw"`
	Doc        string  `yaml:"doc"`
	Params     []Field `yaml:"params"`
}

// ClientRule is a behavioural invariant the hand-written client must honour.
// Recorded in the IR so the reasoning survives into every language target.
type ClientRule struct {
	ID  string `yaml:"id"`
	Doc string `yaml:"doc"`
}

// Spec is the whole loaded specification.
type Spec struct {
	Protocol      Protocol      `yaml:"protocol"`
	Types         []Type        `yaml:"types"`
	V1Types       []Type        `yaml:"v1Types"`
	Normalization Normalization `yaml:"normalization"`
	ErrorCodes    ErrorCodes    `yaml:"errorCodes"`
	Statuses      []Status      `yaml:"statuses"`
	Endpoints     []Endpoint    `yaml:"endpoints"`
	ClientRules   []ClientRule  `yaml:"clientRules"`
}

// Load reads and validates the specification at path.
func Load(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ir: reading spec: %w", err)
	}

	var spec Spec
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true) // an unrecognized key is a typo, not a comment
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("ir: parsing spec: %w", err)
	}

	if err := spec.validate(); err != nil {
		return nil, err
	}
	return &spec, nil
}

// TypeByName returns a declared type, searching both v2 and v1 declarations.
func (s *Spec) TypeByName(name string) (*Type, bool) {
	for i := range s.Types {
		if s.Types[i].Name == name {
			return &s.Types[i], true
		}
	}
	for i := range s.V1Types {
		if s.V1Types[i].Name == name {
			return &s.V1Types[i], true
		}
	}
	return nil, false
}

// EndpointByName returns a declared endpoint.
func (s *Spec) EndpointByName(name string) (*Endpoint, bool) {
	for i := range s.Endpoints {
		if s.Endpoints[i].Name == name {
			return &s.Endpoints[i], true
		}
	}
	return nil, false
}

// ErrorCodeValues returns the error table sorted by code, for deterministic
// output. Generated files must be byte-stable across runs so that
// `simplefin-gen verify` can act as a CI gate.
func (s *Spec) ErrorCodeValues() []ErrorCode {
	out := append([]ErrorCode(nil), s.ErrorCodes.Values...)
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

func (s *Spec) validate() error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if s.Protocol.Name == "" {
		add("protocol.name is required")
	}
	if s.Protocol.DefaultVersion == "" {
		add("protocol.defaultVersion is required")
	} else if !contains(s.Protocol.SupportedVersions, s.Protocol.DefaultVersion) {
		add("protocol.defaultVersion %q is not in supportedVersions %v",
			s.Protocol.DefaultVersion, s.Protocol.SupportedVersions)
	}

	known := map[string]bool{}
	for _, t := range s.Types {
		known[t.Name] = true
	}
	for _, t := range s.V1Types {
		if known[t.Name] {
			add("type %q is declared in both types and v1Types", t.Name)
		}
		known[t.Name] = true
	}

	roots := 0
	for i := range s.Types {
		if s.Types[i].Root {
			roots++
		}
		s.validateType(&s.Types[i], known, "types", add)
	}
	if roots != 1 {
		add("exactly one type must be marked root:true, found %d", roots)
	}
	for i := range s.V1Types {
		s.validateType(&s.V1Types[i], known, "v1Types", add)
	}

	s.validateErrorCodes(add)
	s.validateStatuses(add)
	s.validateEndpoints(known, add)
	s.validateNormalization(add)

	if len(problems) > 0 {
		return fmt.Errorf("ir: spec is invalid:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

func (s *Spec) validateType(t *Type, known map[string]bool, section string, add func(string, ...any)) {
	where := fmt.Sprintf("%s.%s", section, t.Name)
	if t.Name == "" {
		add("%s: name is required", section)
	}
	if t.Doc == "" {
		// Docs drive IDE hover text; a missing one is a silently degraded SDK.
		add("%s: doc is required (it becomes the editor hover text)", where)
	}
	if len(t.Fields) == 0 {
		add("%s: at least one field is required", where)
	}

	seenName := map[string]bool{}
	seenWire := map[string]bool{}
	for i := range t.Fields {
		f := &t.Fields[i]
		fwhere := fmt.Sprintf("%s.%s", where, f.Name)

		if f.Name == "" {
			add("%s: field name is required", where)
			continue
		}
		if seenName[f.Name] {
			add("%s: duplicate field name", fwhere)
		}
		seenName[f.Name] = true

		if f.Doc == "" {
			add("%s: doc is required (it becomes the editor hover text)", fwhere)
		}
		if !isLowerCamel(f.Name) {
			add("%s: field names must be lowerCamelCase (the SDKs present camelCase uniformly)", fwhere)
		}

		// A synthetic field has no wire representation by design.
		if f.Synthetic {
			if f.Wire != "" {
				add("%s: synthetic fields must not declare a wire name", fwhere)
			}
		} else {
			if f.Wire == "" {
				add("%s: wire is required", fwhere)
			}
			if seenWire[f.Wire] {
				add("%s: duplicate wire name %q", fwhere, f.Wire)
			}
			seenWire[f.Wire] = true
		}

		if err := f.resolveKind(known); err != nil {
			add("%s: %v", fwhere, err)
		}
		if f.Enum != "" && f.Enum != "errorCodes" && f.Enum != "supportedVersions" {
			add("%s: unknown enum %q", fwhere, f.Enum)
		}
	}

	for _, a := range t.Accessors {
		awhere := fmt.Sprintf("%s.%s()", where, a.Name)
		if a.Doc == "" {
			add("%s: doc is required", awhere)
		}
		if a.Returns != "timeOrNull" {
			add("%s: unsupported accessor return %q (only timeOrNull)", awhere, a.Returns)
		}
		if len(a.Prefer) == 0 {
			add("%s: prefer must name at least one field", awhere)
		}
		for _, p := range a.Prefer {
			if !seenName[p] {
				add("%s: prefer references unknown field %q", awhere, p)
				continue
			}
			for i := range t.Fields {
				if t.Fields[i].Name == p && t.Fields[i].Kind != KindEpochSeconds {
					add("%s: prefer field %q is %s, must be epochSeconds",
						awhere, p, t.Fields[i].Kind)
				}
			}
		}
	}
}

// resolveKind parses the raw kind string into a Kind plus an element type.
func (f *Field) resolveKind(known map[string]bool) error {
	raw := f.RawKind
	if raw == "" {
		return fmt.Errorf("kind is required")
	}

	switch {
	case raw == string(KindString):
		f.Kind = KindString
	case raw == string(KindNumericString):
		f.Kind = KindNumericString
	case raw == string(KindEpochSeconds):
		f.Kind = KindEpochSeconds
	case raw == string(KindBool):
		f.Kind = KindBool
	case raw == string(KindExtra):
		f.Kind = KindExtra
	case raw == string(KindAny):
		f.Kind = KindAny
	case raw == "array:string":
		f.Kind = KindArrayString
	case strings.HasPrefix(raw, "array:"):
		elem := strings.TrimPrefix(raw, "array:")
		if !known[elem] {
			return fmt.Errorf("kind %q references unknown type %q", raw, elem)
		}
		f.Kind, f.Elem = KindArrayRef, elem
	case strings.HasPrefix(raw, "ref:"):
		elem := strings.TrimPrefix(raw, "ref:")
		if !known[elem] {
			return fmt.Errorf("kind %q references unknown type %q", raw, elem)
		}
		f.Kind, f.Elem = KindRef, elem
	case strings.HasPrefix(raw, "enum:"):
		f.Kind, f.Elem = KindEnum, strings.TrimPrefix(raw, "enum:")
	default:
		return fmt.Errorf("unknown kind %q", raw)
	}
	return nil
}

func (s *Spec) validateErrorCodes(add func(string, ...any)) {
	if len(s.ErrorCodes.Values) == 0 {
		add("errorCodes.values must not be empty")
		return
	}
	if s.ErrorCodes.UnknownFallback != "prefix" {
		add("errorCodes.unknownFallback must be %q", "prefix")
	}

	seen := map[string]bool{}
	seenConst := map[string]bool{}
	nakedByPrefix := map[string]bool{}
	for _, v := range s.ErrorCodes.Values {
		if v.Code == "" {
			add("errorCodes: a value is missing its code")
			continue
		}
		if seen[v.Code] {
			add("errorCodes: duplicate code %q", v.Code)
		}
		seen[v.Code] = true

		if v.Const == "" {
			add("errorCodes[%s]: const is required", v.Code)
		} else if seenConst[v.Const] {
			add("errorCodes[%s]: duplicate const %q", v.Code, v.Const)
		}
		seenConst[v.Const] = true

		if v.Doc == "" {
			add("errorCodes[%s]: doc is required", v.Code)
		}
		if _, ok := s.ErrorCodes.Prefixes[v.Prefix()]; !ok {
			add("errorCodes[%s]: prefix %q is not declared in errorCodes.prefixes",
				v.Code, v.Prefix())
		}
		if v.IsNaked() {
			nakedByPrefix[v.Prefix()] = true
		}
		for _, e := range v.Extra {
			if e != "connId" && e != "accountId" {
				add("errorCodes[%s]: unknown extra attribute %q", v.Code, e)
			}
		}
	}

	// The spec requires falling back to the naked prefix for unknown subcodes,
	// so every declared prefix needs a naked form to fall back to.
	for prefix := range s.ErrorCodes.Prefixes {
		if !nakedByPrefix[prefix] {
			add("errorCodes: prefix %q has no naked %q. value to fall back to", prefix, prefix)
		}
	}
}

func (s *Spec) validateStatuses(add func(string, ...any)) {
	seen := map[int]bool{}
	for _, st := range s.Statuses {
		if seen[st.Code] {
			add("statuses: duplicate code %d", st.Code)
		}
		seen[st.Code] = true
		if st.Doc == "" {
			add("statuses[%d]: doc is required", st.Code)
		}
		if st.Kind != "ok" && st.Const == "" {
			add("statuses[%d]: const is required for non-ok statuses", st.Code)
		}
	}
	for _, required := range []int{402, 403} {
		if !seen[required] {
			add("statuses: %d must be declared (the protocol assigns it meaning)", required)
		}
	}
}

func (s *Spec) validateEndpoints(known map[string]bool, add func(string, ...any)) {
	if len(s.Endpoints) == 0 {
		add("endpoints must not be empty")
	}
	seen := map[string]bool{}
	for i := range s.Endpoints {
		e := &s.Endpoints[i]
		where := fmt.Sprintf("endpoints.%s", e.Name)

		if e.Name == "" {
			add("endpoints: a name is required")
			continue
		}
		if seen[e.Name] {
			add("%s: duplicate endpoint name", where)
		}
		seen[e.Name] = true

		if e.Doc == "" {
			add("%s: doc is required", where)
		}
		if e.Method != "GET" && e.Method != "POST" {
			add("%s: unsupported method %q", where, e.Method)
		}
		if e.Returns == "" && e.ReturnsRaw == "" {
			add("%s: must declare either returns or returnsRaw", where)
		}
		if e.Returns != "" {
			if !known[e.Returns] {
				add("%s: returns unknown type %q", where, e.Returns)
			}
			if e.ReturnsRaw != "" {
				add("%s: declares both returns and returnsRaw", where)
			}
		}

		seenParam := map[string]bool{}
		for j := range e.Params {
			p := &e.Params[j]
			pwhere := fmt.Sprintf("%s.params.%s", where, p.Name)

			if p.Name == "" {
				add("%s: param name is required", where)
				continue
			}
			if seenParam[p.Name] {
				add("%s: duplicate param", pwhere)
			}
			seenParam[p.Name] = true

			if p.Wire == "" {
				add("%s: wire is required", pwhere)
			}
			if p.Doc == "" {
				add("%s: doc is required", pwhere)
			}
			if err := p.resolveKind(known); err != nil {
				add("%s: %v", pwhere, err)
			}

			switch p.Serialize {
			case "unixSeconds", "optInOne", "repeated", "valueWithDefault":
			case "":
				add("%s: serialize is required", pwhere)
			default:
				add("%s: unknown serialize rule %q", pwhere, p.Serialize)
			}
			if p.Serialize == "valueWithDefault" && p.Default == "" {
				add("%s: serialize valueWithDefault requires a default", pwhere)
			}
			// optInOne exists precisely because these params are omitted when
			// false rather than sent as "0"; a non-bool would be meaningless.
			if p.Serialize == "optInOne" && p.Kind != KindBool {
				add("%s: serialize optInOne requires a bool, got %s", pwhere, p.Kind)
			}
			if p.Serialize == "repeated" && p.Kind != KindArrayString {
				add("%s: serialize repeated requires array:string, got %s", pwhere, p.Kind)
			}
		}
	}
}

func (s *Spec) validateNormalization(add func(string, ...any)) {
	if s.Normalization.Doc == "" {
		add("normalization.doc is required")
	}
	if len(s.Normalization.ConnIDFrom) == 0 {
		add("normalization.connIdFrom must name at least one field")
		return
	}
	org, ok := s.TypeByName("OrgV1")
	if !ok {
		add("normalization: OrgV1 must be declared in v1Types")
		return
	}
	for _, name := range s.Normalization.ConnIDFrom {
		found := false
		for _, f := range org.Fields {
			if f.Name == name {
				found = true
				break
			}
		}
		if !found {
			add("normalization.connIdFrom references unknown OrgV1 field %q", name)
		}
	}
}

func contains(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}

func isLowerCamel(s string) bool {
	if s == "" {
		return false
	}
	if s[0] < 'a' || s[0] > 'z' {
		return false
	}
	return !strings.ContainsAny(s, "_-. ")
}
