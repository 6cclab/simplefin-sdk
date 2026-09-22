import assert from "node:assert/strict"
import { test } from "node:test"

import { normalizeAccountSet } from "../dist/index.js"

/*
 * Real SimpleFIN servers send fields the specification never documented. A
 * live beta-bridge response carried `holdings` on all 25 accounts (populated
 * on 5) and `payee`, `memo` and `mcc` on all 142 transactions. Dropping them
 * silently is the same failure mode as dropping v1 institution data, so they
 * are captured instead of discarded.
 */

type Holding = { symbol: string; shares: string }
type AccountExtras = { holdings: Holding[] }
type TxnExtras = { payee: string; memo: string; mcc: string }

const undocumented = {
  errlist: [],
  connections: [
    { conn_id: "CONN-1", name: "Example", org_id: "ORG-1", sfin_url: "https://x/simplefin" },
  ],
  accounts: [
    {
      id: "ACT-1", name: "Brokerage", conn_id: "CONN-1", currency: "USD",
      balance: "100.00", "balance-date": 1757635200,
      holdings: [{ symbol: "VOO", shares: "1.5" }],
      transactions: [
        {
          id: "TXN-1", posted: 1757548800, amount: "-1.00", description: "SQ *COFFEE",
          payee: "Blue Bottle", memo: "", mcc: "5814",
        },
      ],
    },
    {
      id: "ACT-2", name: "Checking", conn_id: "CONN-1", currency: "USD",
      balance: "5.00", "balance-date": 1757635200,
      holdings: [],
    },
  ],
}

test("undocumented fields are captured and typed by the caller", () => {
  const set = normalizeAccountSet<AccountExtras, TxnExtras>(undocumented)

  const brokerage = set.accounts[0]!
  const checking = set.accounts[1]!

  // Typed through the parameter, no cast at the use site.
  assert.equal(brokerage.unknown.holdings.length, 1)
  assert.equal(brokerage.unknown.holdings[0]?.symbol, "VOO")

  // An account whose holdings are empty still reports the key, so callers can
  // distinguish "no holdings" from "server never mentioned holdings".
  assert.deepEqual(checking.unknown.holdings, [])

  const tx = brokerage.transactions![0]!
  assert.equal(tx.unknown.payee, "Blue Bottle")
  assert.equal(tx.unknown.mcc, "5814")
})

test("documented fields are not duplicated into the unknown bucket", () => {
  const set = normalizeAccountSet(undocumented)
  const account = set.accounts[0]!

  for (const documented of [
    "id", "name", "conn_id", "currency", "balance", "balance-date", "transactions",
  ]) {
    assert.ok(
      !(documented in account.unknown),
      `documented key ${documented} leaked into unknown`,
    )
  }

  const tx = account.transactions![0]!
  for (const documented of ["id", "posted", "amount", "description"]) {
    assert.ok(!(documented in tx.unknown), `documented key ${documented} leaked into unknown`)
  }
})

// `org` is consumed by the v1 normalizer, so surfacing it as an undocumented
// extra would be duplication rather than discovery.
test("v1 org does not leak into the unknown bucket", () => {
  const v1 = {
    errors: [],
    accounts: [
      {
        org: { domain: "bank.example", name: "Bank", "sfin-url": "https://sfin.bank.example" },
        id: "ACT-1", name: "Checking", currency: "USD",
        balance: "1.00", "balance-date": 1757635200,
        holdings: [],
      },
    ],
  }

  const set = normalizeAccountSet(v1)

  // The normalizer must still see org.
  assert.equal(set.connections.length, 1, "the org was not consumed")
  assert.equal(set.accounts[0]?.connId, "bank.example")
  assert.ok(!("org" in set.accounts[0]!.unknown), "org leaked into unknown")
  // But a genuinely undocumented sibling key is still captured.
  assert.ok("holdings" in set.accounts[0]!.unknown, "holdings was not captured")
})
