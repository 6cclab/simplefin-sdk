# Spec diff: generated SDK vs. the two hand-written predecessors

This SDK was written spec-first — the IR in `spec/simplefin.yaml` was transcribed from
`protocol.html` and `protocol-v1.html`, not extracted from existing code. The two hand-written
SimpleFIN clients that preceded it were then compared against the result, and every difference
classified.

Sources compared:

| Label | Path |
| --- | --- |
| **go-bt** | `budget-track-expo-push/apps/api/internal/simplefin/` (Go, 809 lines) |
| **ts-ft** | `fintrack/packages/simplefin/src/` (TypeScript, the "reference implementation") |

`budget-track/apps/api/internal/simplefin/` is byte-identical to go-bt and is not listed separately.

Each difference is classified as:

- **protocol** — the specification says so; the generated SDK follows it.
- **workaround** — a production discovery about a specific server or institution. Correct to have
  learned, but it belongs in the application, not in a protocol client.
- **bug** — the predecessor is wrong or incomplete against the specification.

---

## bug — carried into this SDK as fixes

### 1. `version: "1"` was offered but unparseable

Both predecessors expose a version selector — go-bt `GetAccountsParams.Version string`, ts-ft
`version?: "1" | "2"` — and both send it to the server. Neither can parse the response.

Protocol v1 nests institution identity inside each account as `org`, with **no** `conn_id` and
**no** top-level `connections` array. go-bt's `rawAccountSet` and ts-ft's `types.ts` model only the
v2 shape, so a v1 response yields accounts whose `ConnID` is `""` and a `Connections` list that is
empty. Nothing errors. The institution data simply vanishes.

This matters more than it looks: the spec says the server picks the default version when the
parameter is absent, and v2 is still `2.0.0-draft`.

**Fixed here** by modelling `OrgV1` and generating a normalizer that synthesizes one `Connection`
per distinct org, deriving `conn_id` from the first non-empty of `org.id`, `org.domain`,
`org.sfin-url`. Covered by `spec/golden/v1-full.json`.

### 2. `GET /info` was never implemented

Neither predecessor has it. There was no way to ask a server which protocol versions it supports —
which is the only principled way to decide whether to send `version=1` or `version=2`.

**Added here** as `Info(ctx)` / `client.info()`.

Worth recording: the live SimpleFIN Bridge currently **302-redirects `/simplefin/info` to its
marketing homepage** rather than returning JSON. The SDK surfaces that as a "body is not JSON"
error. The endpoint is implemented to spec; the Bridge does not honour it.

### 3. Custom currencies were never resolved

The spec allows `currency` to be a URL resolving to `{name, abbr}`. Both predecessors treat
`currency` as an opaque string.

**Added here** as `ResolveCurrency(ctx, url)` / `client.resolveCurrency()`, plus `IsCustomCurrency`
to detect when it is needed. The credentials are deliberately **not** attached to that request —
the URL is chosen by the server and points at an arbitrary third-party host. Covered by a test in
both languages.

### 4. `errlist` entries lost their code and ids

go-bt's `Error` is `{Message string, Raw any}` and ts-ft's is `{message, raw}`. Both discard
`code`, `conn_id` and `account_id`, keeping them only inside the untyped `Raw` blob.

That throws away the ability to act on an error programmatically: `con.auth` means "this one
connection needs relinking" and `act.failed` means "retry this account later", but a caller holding
only a display string cannot distinguish them without matching on prose.

**Fixed here**: `Error` carries `code` (a typed `ErrorCode`), `msg`, `connId`, `accountId`, and
`raw`. Unknown subcodes degrade to the naked prefix per the spec's stated rule, so
`con.somethingnew` is still handled as a connection error. Covered by `spec/golden/odd-errors.json`.

---

## found by this project, fixed here

Not predecessor bugs. These surfaced while building the cross-language check and while running
against a real bridge — neither is visible from reading the specification alone.

### 5. TypeScript coerced wrong-typed fields; Go rejected them

Not a predecessor bug — a divergence found *inside this project*, while building the
cross-language check, and worth recording because it is the kind of thing that only surfaces when
two implementations are compared directly.

go-bt and the Go target use `encoding/json`, which **rejects** a payload where a field has the
wrong type: `"balance": 12.5` fails the whole response. The first draft of this SDK's TypeScript
parser was written defensively and **coerced** the same payload, yielding `balance: ""`.

Both are defensible in isolation. Together they are not: the same fixture produced an error in one
language and a silently wrong balance in the other.

**Resolved toward strictness**, in both languages, matching `encoding/json` exactly:

- a missing field, or an explicit `null`, yields the zero value
- a field present with the wrong type is an error

The deciding argument is money. A server sending the number `12.5` where the protocol documents a
string is broken, and a client that quietly renders that as an empty balance is more dangerous than
one that refuses the response. The TypeScript side throws `SimpleFinParseError` naming the field
path, so the caller can report the problem upstream rather than guessing.

`spec/malformed/` holds the payloads both targets must reject; `spec/golden/sparse.json` holds the
absent-and-null cases both must accept, so a parser that rejected everything could not pass.

### 6. Undocumented wire fields were silently dropped

Found by running against the real `beta-bridge.simplefin.org`, not against fixtures — no amount
of spec reading would have surfaced it. Every response carries fields the specification never
defines, and they are *not* inside `extra`; they sit at the top level of each object, so the
documented `extra` escape hatch does not catch them.

Measured over one live response (25 accounts, 142 transactions):

| Field | Present on | Actually populated |
| --- | --- | --- |
| `holdings` (account) | 25/25 | **5** — the investment accounts; the other 20 send `[]` |
| `payee` (transaction) | 142/142 | **142** |
| `memo` (transaction) | 142/142 | 4 |
| `mcc` (transaction) | 142/142 | 1, from a single institution |

`payee` is the valuable one: fully populated and cleaner than parsing `description`. `mcc` looked
promising as a categorization signal but is populated on one transaction out of 142, so it is not
something to build on.

Both predecessors discard all of this, and so did the first cut of this SDK.

**Fixed here** with a generic capture rather than typed fields. Each capturing type retains any
wire key the spec does not define, and the caller supplies the shape:

```go
holdings, ok := simplefin.Field[[]Holding](account.Unknown, "holdings")
```

```ts
const set = await client.getAccounts<{ holdings: Holding[] }, { payee: string }>()
set.accounts[0].unknown.holdings              // typed, completes
set.accounts[0].transactions?.[0]?.unknown.payee
```

Two properties this buys, both tested: the data separates itself by account kind without the SDK
inventing an account-type concept (empty `holdings` stays empty, populated stays populated), and
anything the bridge adds later is captured with no SDK change.

`org` is explicitly excluded from the bucket on Account, because the v1 normalizer already
consumes it — reporting it as an undocumented extra would be duplication, not discovery.

One Go hazard worth recording: giving `Account` an `UnmarshalJSON` promotes that method to any
struct embedding it, which silently took over decoding in `rawAccount` and left `Org` nil on every
v1 response. No error, just missing institutions. `rawAccount` now holds `Account` as a named
field with its own unmarshaller.

### 7. `GET /info` is not trustworthy

`beta-bridge.simplefin.org` reports `{"versions":["1.0"]}` while happily serving a full v2
response when asked for `version=2`. The production bridge is worse: `/simplefin/info`
302-redirects to the marketing homepage rather than returning JSON.

The endpoint is implemented to spec, but it cannot be used to decide which version to request.
This is why the v1 normalizer is in scope rather than gated behind a version probe.
---

## protocol — agreed with the predecessors, kept

These were already right, and the generated SDK reproduces them deliberately rather than by
accident. Several are load-bearing security properties.

| Behaviour | Why |
| --- | --- |
| Amounts stay **raw strings**, never parsed to float | SimpleFIN sends `"-33293.43"` as a string precisely to avoid binary-float precision loss |
| Credentials split out of the Access URL into an `Authorization` header | Keeps Basic Auth out of request logs, redirect targets, and error strings. In Node it is mandatory: `fetch` rejects userinfo in a URL outright |
| `url.Parse` errors are **not** wrapped | Go's parse errors quote the input, which is the Access URL |
| Transport errors are **not** wrapped | They embed the request URL |
| `errlist` preferred, falling back to deprecated `errors` | Both shapes are live |
| An unrecognized error object keeps its raw JSON as the message | The protocol *requires* showing these to users; a placeholder would defeat that |
| `posted == 0` → `transacted_at` → null, with `<= 0` treated as absent | A bare zero check in one path and a `> 0` check in another is how pending rows get dated to 1970 in one place and null in the next |
| `pending` and `balances-only` are opt-in, sent as `1`, omitted when false | Sending `0` is not the same to a server that checks only for presence |
| `account` is a repeated key, never comma-joined | A comma-joined value reads as one id containing a comma |
| Tests run against a real HTTP server, never a mocked client | Recorded in budget-track's design doc: a prior defect survived 52 green tests because every one mocked the transport |

One addition in the same spirit: **status is checked before any JSON decode**, and a non-JSON body
reports its `Content-Type` rather than its contents. The Bridge serves HTML error pages, so
decoding first turns a plain 403 into a confusing syntax error — and echoing the body of a
misrouted authenticated request could print credentials back out.

---

## workaround — deliberately left in budget-track

These live in `budget-track-expo-push/apps/api/internal/bank/simplefin.go`. They are real
knowledge, expensively acquired, and they do **not** belong in a protocol client: an SDK that
silently rewrites amounts or invents fields the wire never sent is surprising and hard to debug.

| Workaround | What it does | Why it stays out |
| --- | --- | --- |
| `correctBrokerageDebitSign` | Negates `"YOU BOUGHT …"` and `"PURCHASE INTO CORE ACCOUNT …"` amounts, which Fidelity reports as positive magnitudes. Verified against 25 investment rows where a prior provider signed the same transactions correctly | Institution-specific and description-matched. A protocol client must report what the server sent |
| `deriveMask` | Regex-extracts a last-four mask from the account *name* | SimpleFIN has no mask field. This is inference, not parsing |
| `inferAccountType` | Keyword table over the account name | SimpleFIN has no account-category field. Same reasoning |
| Orphan-account synthesis | Invents a `Connection` when an account's `conn_id` matches none | Genuinely useful, but it is a domain decision about whether to drop or keep the account. Note the v1 normalizer here *does* synthesize connections — that is different: v1 legitimately has no connections array, so building one is parsing, not guessing |
| Currency defaulting to `"USD"` when empty | Fills a blank currency | Guessing a currency is a decision with financial consequences |

---

## Differences that are neither fix nor workaround

**camelCase field names.** The wire format uses three conventions at once — hyphen-case
(`available-balance`, `balance-date`), snake_case (`conn_id`, `org_url`, `transacted_at`), and bare
lowercase — and changes `sfin-url` (v1) to `sfin_url` (v2) for the same field. ts-ft mirrored the
wire verbatim, including quoted keys like `"available-balance"?: string`. This SDK presents
camelCase uniformly in every language, with the wire names held in the IR.

This is a **source-incompatible** change for ts-ft. go-bt already mapped to `AvailableBalance` via
struct tags, so Go callers see no difference.

**`Error` is `SimpleFinError` in TypeScript.** Exporting an interface named `Error` would shadow the
JavaScript built-in in every consuming module. Go keeps the spec's name, since it has no conflict.
ts-ft made the same call independently.

**No retry, backoff, or rate limiting — in either predecessor or here.** budget-track's design doc
records a 90-day window per request and a ~24-requests-per-day budget as an external constraint,
but neither predecessor enforces it in the client, and neither does this SDK. Scheduling is the
caller's concern; SimpleFIN has no webhooks, so polling cadence is a application-level decision.
Recorded here so the omission is known rather than assumed.
