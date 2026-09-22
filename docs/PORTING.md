# Adding a language target

A new SDK is a template directory plus one hand-written HTTP client. The models, query-parameter
serialization, error codes, v1→v2 normalizer, canonical serializer, and conformance test are all
generated from `spec/simplefin.yaml`.

## What you write

```
templates/<lang>/
  <file>.tmpl              # rendered to <file>
  src__<file>.tmpl         # "__" is a path separator -> src/<file>
```

Register the directory in two places:

1. `templates/embed.go` — add it to the `//go:embed` pattern, or it will not be compiled in.
2. `cmd/simplefin-gen/main.go` — add a `defaultOut` entry so `generate` and `verify` know where it
   lands.

Then hand-write the HTTP client for that language. It is roughly 150 lines and it is the only part
that is not generated, because it is where the security invariants live and where generated code
reads least like the target language.

## What the template is executed against

`*gen.View` (see `internal/gen/view.go`). It flattens the IR into exactly what a template needs, so
templates stay declarative — anything requiring a conditional or an index calculation is resolved
in Go, where it can be unit tested.

Key fields:

| Field | Contents |
| --- | --- |
| `.Types` / `.V1Types` | `TypeView` with `Fields`, `Accessors`, `Recv`, `TSName` |
| `.GetAccounts` | The parameterized endpoint, with each param's serialization rule pre-resolved |
| `.ErrorCodes` / `.NakedPrefixes` | The error table, sorted for deterministic output |
| `.Statuses` | HTTP status semantics (402, 403) |
| `.ConnIDFrom` | The v1 `conn_id` derivation chain |
| `.Header` | The generated-code banner, already comment-prefixed |

Each `FieldView` carries both `Wire` (the key on the wire) and the language-appropriate name, plus
flags so templates branch without re-parsing kinds: `IsMoney`, `IsEpoch`, `IsRefArray`,
`IsStringArray`, `Optional`, `Canonical`, `Synthetic`.

Add a `<lang>Type` mapping in `internal/gen/types.go` and a `<lang>Doc` renderer if the language
needs one. Register both in `funcMap()`.

## Rules the hand-written client must honour

These are recorded in the IR under `clientRules` so they survive into every target. They are not
stylistic.

1. **Credentials out of the URL.** An Access URL embeds Basic Auth
   (`https://user:pass@host/simplefin`). Split them out and send an `Authorization` header. Do not
   leave them in the request URL, where they reach logs and redirect targets. Percent-decode the
   userinfo first — `p%40ss` must be sent as `p@ss`.
2. **Never wrap URL-parse or transport errors.** Both quote or embed the input URL, which contains
   the credentials. Return a fixed message.
3. **HTTPS only.** Reject any scheme other than `http`/`https`, and verify certificates.
4. **Check status before decoding.** The SimpleFIN Bridge serves HTML error pages; decoding first
   turns a 403 into a confusing parse error. When a body fails to parse, report the `Content-Type`,
   never the body — a misrouted authenticated response can echo credentials back.
5. **Never parse money as a float.** Expose the exact wire string.
6. **Do not attach credentials to a custom currency URL.** That host is server-chosen and
   third-party.
7. **Claim is one-shot.** The server consumes the token whether or not you store the result. Never
   retry with the same token.
8. **Never drop an undocumented field.** Real servers send keys the specification does not define —
   the Bridge attaches `holdings` to every account and `payee`/`memo`/`mcc` to every transaction,
   outside `extra`. Types marked `captureUnknown` in the spec must retain them, keyed by wire name,
   with the caller supplying the type. Keys listed in `unknownExclude` are consumed elsewhere and
   must stay out of the bucket.

## Passing

A target is done when all of these hold:

```bash
go test ./internal/...                      # generator unit tests
go run ./cmd/simplefin-gen verify           # generated output matches what is committed
```

plus, inside the new SDK:

- **Conformance.** Every `spec/golden/*.json` parses to its hand-authored `.expect.json`. The
  generated conformance test does this for you once `canonicalJSON` is emitted.
- **Cross-language equivalence.** The canonical output must be byte-identical to the other targets'.
  Two rules make that possible: object keys are sorted alphabetically (matching Go's map
  marshalling), and JSON embedded in an error message is compacted so it does not inherit the
  server's whitespace.
- **The security tests.** Port them. At minimum: credentials appear in the header and never in the
  URL; no error message on any failure path contains the username or password; a URL-encoded
  password round-trips correctly.
- **No mocked transport.** Tests run against a real HTTP server. This is not a preference — a prior
  SimpleFIN client's most expensive defect survived 52 green tests because every one of them mocked
  the HTTP client, so the bug lived in the only part no test executed.
- **Rejection agreement.** Every fixture in `spec/malformed/` must be rejected, and every fixture
  in `spec/golden/` accepted. Parsing follows Go's `encoding/json` semantics: a missing field or an
  explicit `null` yields the zero value, but a field present with the wrong type is an error. A
  lenient parser that coerced a wrong-typed balance to `""` would pass conformance and still be
  wrong — see `docs/SPEC-DIFF.md`.
- **Editor support, tested not assumed.** Hover text and completions are a stated requirement, so
  each target asserts them. The Node target queries the TypeScript language service
  (`test/intellisense.test.ts`); the Go target parses its own source and checks every exported
  symbol carries a doc comment (`docs_test.go`). A type that compiles can still offer an empty
  popup; only a query against the language tooling tells you which.

## What not to add

Keep the SDK pure protocol. Institution-specific corrections, field inference, and defaulting
belong in the consuming application. `docs/SPEC-DIFF.md` lists the workarounds that were
deliberately left out and why.
