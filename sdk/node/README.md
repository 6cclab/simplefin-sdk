# @6cclab/simplefin

A typed [SimpleFIN](https://www.simplefin.org/protocol.html) client for reading bank balances and
transactions. No runtime dependencies.

SimpleFIN is a small protocol with sharp edges: amounts are strings that must never become floats,
the wire format mixes three naming conventions, protocol v1 and v2 disagree about where
institutions live, and a pending transaction's timestamp is `0`. This client handles all of that
and presents one consistent model.

```bash
npm install @6cclab/simplefin
```

## Use

```ts
import { claim, createClient, ErrorCodes, isAuthError, postedTime } from "@6cclab/simplefin"

// One-time: trade a setup token for an Access URL. Store it as securely as
// the financial data itself — it embeds credentials. This is one-shot; if it
// fails you need a new setup token.
const accessUrl = await claim(setupToken)

const client = createClient(accessUrl)

try {
  const set = await client.getAccounts({
    startDate: new Date(Date.now() - 30 * 864e5), // Date or epoch seconds
    pending: true,                                 // omitted by the server otherwise
  })

  for (const account of set.accounts) {
    // Exactly the string the server sent. Do not parse as a float.
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
    // 403: access revoked. SimpleFIN auth is all-or-nothing per Access URL,
    // so re-claim the whole thing rather than relinking one connection.
  }
}
```

## What it handles

- **Money stays exact.** Amounts are strings on the wire and strings here. Nothing converts them to
  a float.
- **Credentials stay out of URLs.** An Access URL embeds Basic Auth; the client splits it into an
  `Authorization` header, and no error message on any failure path contains it. This isn't optional
  in Node — `fetch` rejects userinfo in a URL outright.
- **v1 and v2 look identical.** v1 nests institutions inside each account as `org` with no
  `conn_id`; v2 uses a top-level `connections` array. Both normalize to the same model, so you
  never branch on version. This matters: v2 is still a draft, servers choose the default, and at
  least one bridge reports `versions: ["1.0"]` while happily serving v2.
- **Pending transactions aren't dated to 1970.** `posted` is `0` while pending; `postedTime()`
  falls back to `transactedAt`, then returns `null`.
- **Unknown error codes still work.** Per the spec, an unrecognized subcode degrades to its naked
  prefix, so `con.somethingnew` is still handled as a connection error.
- **HTML error pages are reported honestly.** Status is checked before parsing, so a rejected
  request is an auth error rather than a JSON syntax error.
- **Malformed payloads are rejected, not coerced.** A missing field or `null` yields the zero
  value, but a field present with the wrong type throws `SimpleFinParseError` naming the field —
  because silently rendering a wrong-typed balance as `""` is worse than refusing the response.

## Undocumented fields

Real servers send fields the specification never defines, outside the documented `extra` object. A
live bridge response carried `holdings` on every account (populated on the investment ones) and
`payee`, `memo` and `mcc` on every transaction.

They're captured rather than dropped, and you supply the type:

```ts
type Holding = { symbol: string; shares: string }

const set = await client.getAccounts<
  { holdings: Holding[] },
  { payee: string; memo: string; mcc: string }
>()

set.accounts[0].unknown.holdings                  // typed, autocompletes
set.accounts[0].transactions?.[0]?.unknown.payee
```

Anything a server adds later is captured too, with no upgrade needed.

## Editor support

Parameters, return types and errors all autocomplete, with hover text taken verbatim from the
protocol specification:

```ts
client.getAccounts({ pending: true, version: "2" })
//                   ^ completes    ^ completes "1" | "2"

set.accounts[0].availableBalance
//              ^ hover shows the spec's wording plus "never parse as a float"

if (e.code === "con.auth") e.connId       // narrows: connId visible
if (e.code === "act.failed") e.accountId  // narrows: accountId visible
```

Because a `catch` binding is `unknown` in TypeScript, the package ships narrowing guards —
`isAuthError`, `isPaymentRequiredError`, `isHttpError` — so you don't cast.

## What it deliberately doesn't do

It's a protocol client, not a financial data layer. It won't guess an account's type from its name,
extract a card mask, default a blank currency, or correct an institution's sign errors. It also
does no retrying or rate limiting — SimpleFIN has no webhooks, so scheduling is yours.

## Source

Generated from a single machine-readable specification, alongside a Go client that is held to
byte-identical output on a shared fixture suite:
[github.com/6cclab/simplefin-sdk](https://github.com/6cclab/simplefin-sdk)

Pre-1.0 — the underlying specification is itself `2.0.0-draft`.

MIT
