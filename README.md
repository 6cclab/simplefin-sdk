# simplefin-sdk

Typed [SimpleFIN](https://www.simplefin.org/protocol.html) clients for Go and TypeScript,
generated from one machine-readable specification.

SimpleFIN lets you read your own bank balances and transactions over HTTP. It is a small protocol —
four endpoints, four data types — but it has sharp edges: amounts are strings that must never
become floats, the wire format mixes three naming conventions, protocol v1 and v2 disagree about
where institutions live, and a pending transaction's timestamp is `0`.

This repository encodes those rules once, in `spec/simplefin.yaml`, and generates the parts of each
SDK that would otherwise drift apart.

## Install

**Go**

```bash
go get github.com/6cclab/simplefin-sdk/sdk/go
```

**Node**

```bash
npm install @6cclab/simplefin
```

## Use

**Go**

```go
import simplefin "github.com/6cclab/simplefin-sdk/sdk/go"

// One-time: exchange a setup token for an Access URL. Store it securely —
// it embeds Basic Auth credentials. This is one-shot; a failure means you
// need a new setup token.
accessURL, err := simplefin.Claim(ctx, setupToken)

client, err := simplefin.New(accessURL)

start := time.Now().AddDate(0, 0, -30).Unix()
set, err := client.GetAccounts(ctx, simplefin.GetAccountsParams{
    StartDate: &start,
    Pending:   true, // the server omits pending transactions by default
})
if errors.Is(err, simplefin.ErrAuth) {
    // Access was revoked. Re-claim the whole Access URL — SimpleFIN auth is
    // all-or-nothing, so there is no per-connection relink.
}

for _, account := range set.Accounts {
    // Balance is the exact string the server sent. Do not parse it as a float.
    fmt.Println(account.Name, account.Balance, account.Currency)

    for _, tx := range account.Transactions {
        when := tx.PostedTime() // *time.Time, nil when pending with no date
        fmt.Println(tx.Description, tx.Amount, when)
    }
}

for _, e := range set.Errlist {
    if e.Code == simplefin.ErrCodeConnectionAuth {
        fmt.Println("reconnect", e.ConnID, ":", e.Msg)
    }
}
```

**TypeScript**

```ts
import {
  claim, createClient, ErrorCodes, isAuthError, postedTime,
} from "@6cclab/simplefin"

const accessUrl = await claim(setupToken)
const client = createClient(accessUrl)

try {
  const set = await client.getAccounts({
    startDate: new Date(Date.now() - 30 * 864e5), // Date or epoch seconds
    pending: true,
  })

  for (const account of set.accounts) {
    // balance is the exact string the server sent. Do not parse as a float.
    console.log(account.name, account.balance, account.currency)

    for (const tx of account.transactions ?? []) {
      console.log(tx.description, tx.amount, postedTime(tx))
    }
  }

  for (const e of set.errlist) {
    if (e.code === ErrorCodes.ConnectionAuth) {
      console.log("reconnect", e.connId, ":", e.msg)
    }
  }
} catch (err) {
  if (isAuthError(err)) {
    // Access revoked — re-claim the Access URL.
  }
}
```

## What this handles for you

- **Money stays exact.** Amounts are strings on the wire and strings in the SDK. Nothing in either
  client converts them to a float.
- **Credentials stay out of URLs.** An Access URL embeds Basic Auth. Both clients split it into an
  `Authorization` header, and no error message on any failure path contains it. (In Node this is
  not optional — `fetch` rejects userinfo in a URL outright.)
- **Protocol v1 and v2 look the same.** v1 nests institutions under each account as `org` and has
  no `conn_id`; v2 has a top-level `connections` array. The SDK normalizes v1 into the v2 shape, so
  you never branch on version. This matters because v2 is still a draft and the server chooses the
  default.
- **Pending transactions aren't dated to 1970.** `posted` is `0` while pending. `postedTime()`
  falls back to `transactedAt`, then reports nothing.
- **Unknown error codes still work.** The spec requires falling back to the naked prefix, so
  `con.somethingnew` is handled as a connection error rather than dropped.
- **HTML error pages are reported honestly.** The SimpleFIN Bridge answers rejected requests with
  HTML. Status is checked before parsing, so you get an auth error, not a JSON syntax error.
- **Nothing the server sends is dropped.** Real bridges send fields the spec never documented —
  `holdings` on accounts, `payee`/`memo`/`mcc` on transactions — and not inside `extra`. They are
  captured, and you supply the type:

  ```ts
  const set = await client.getAccounts<{ holdings: Holding[] }, { payee: string }>()
  set.accounts[0].unknown.holdings                    // typed, completes
  set.accounts[0].transactions?.[0]?.unknown.payee
  ```

  ```go
  holdings, ok := simplefin.Field[[]Holding](account.Unknown, "holdings")
  ```

## Editor support

Types, parameters and errors all autocomplete, with hover text taken verbatim from the protocol
specification:

```ts
client.getAccounts({ pending: true, version: "2" })
//                   ^ completes    ^ completes "1" | "2"

set.accounts[0].availableBalance
//              ^ hover shows the spec's description and the
//                "do not parse as a float" warning

if (e.code === "con.auth") e.connId      // narrows: connId visible
if (e.code === "act.failed") e.accountId // narrows: accountId visible
```

TypeScript `catch` bindings are `unknown`, so the SDK ships narrowing guards — `isAuthError`,
`isPaymentRequiredError`, `isHttpError` — rather than expecting you to cast.

## What this deliberately does not do

It is a protocol client, not a financial data layer. It will not guess an account's type from its
name, extract a card mask, default a blank currency, or correct an institution's sign errors. Those
are real problems, but an SDK that silently rewrites amounts is worse than one that reports what
the server said. See [`docs/SPEC-DIFF.md`](docs/SPEC-DIFF.md).

It also does no retrying, backoff, or rate limiting. The Bridge documents roughly a 90-day window
per request and a limited daily request budget; scheduling is left to the caller, since SimpleFIN
has no webhooks.

## Repository layout

```
spec/simplefin.yaml    the source of truth
spec/golden/           fixtures + hand-authored expectations, shared by all targets
cmd/simplefin-gen      the generator
templates/{go,node}    per-language templates
sdk/{go,node}          generated files + hand-written HTTP client
docs/                  SPEC-DIFF.md, PORTING.md
```

## Developing

```bash
go run ./cmd/simplefin-gen langs                # list targets
go run ./cmd/simplefin-gen generate --lang go
go run ./cmd/simplefin-gen generate --lang node
go run ./cmd/simplefin-gen verify               # fails if generated files were hand-edited

go test ./internal/...                          # generator
(cd sdk/go && go test ./...)                    # Go SDK
(cd sdk/node && npm test)                       # Node SDK
./scripts/cross-language-check.sh               # Go and Node must agree, fixture by fixture
```

Generated files carry a `Code generated by simplefin-gen. DO NOT EDIT.` header. Edit the spec or
the templates, never the output — `verify` runs in CI and will catch it.

Adding a language is a template directory plus one hand-written client:
[`docs/PORTING.md`](docs/PORTING.md).

## Verification

Beyond unit tests, the two SDKs are held to each other: every fixture in `spec/golden/` must parse
to a hand-authored expectation in *both* languages and produce byte-identical canonical output,
and every fixture in `spec/malformed/` must be rejected by both. Editor support is asserted by
querying the TypeScript language service and by parsing Go doc comments, rather than assumed.

This has also been run against a real bridge: a live response of 25 accounts and 142 transactions
parsed cleanly on both the v1 and v2 code paths, producing identical connection and account counts
from each.

## Status

Pre-1.0. The underlying specification is itself `2.0.0-draft`, so the API may change.

## License

MIT
