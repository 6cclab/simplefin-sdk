import assert from "node:assert/strict"
import { createServer, type Server, type IncomingMessage, type ServerResponse } from "node:http"
import type { AddressInfo } from "node:net"
import { after, test } from "node:test"

import {
  createClient,
  ErrorCodes,
  isAuthError,
  isCustomCurrency,
  isHttpError,
  isPaymentRequiredError,
  postedTime,
  type Transaction,
} from "../dist/index.js"

/*
 * These tests run against a real http server rather than a stubbed fetch.
 * That is a deliberate constraint carried over from the SDKs this one
 * replaces: a prior implementation's most expensive defect survived 52 green
 * tests because every one of them mocked the transport, so the bug lived in
 * the part no test ever executed.
 */

const accountsFixture = JSON.stringify({
  errlist: [
    { code: "con.auth", msg: "Example Bank needs re-authentication", conn_id: "CONN-1" },
  ],
  connections: [
    {
      conn_id: "CONN-1",
      name: "Example Bank",
      org_id: "ORG-1",
      org_url: "https://examplebank.com",
      sfin_url: "https://bridge.example/simplefin",
    },
  ],
  accounts: [
    {
      id: "ACT-001",
      name: "Checking",
      conn_id: "CONN-1",
      currency: "USD",
      balance: "1250.33",
      "available-balance": "1200.00",
      "balance-date": 1757635200,
      transactions: [
        {
          id: "TXN-001",
          posted: 1757548800,
          amount: "-33.45",
          description: "SQ *BLUE BOTTLE COFF #47",
        },
      ],
    },
  ],
})

const servers: Server[] = []
after(() => {
  for (const s of servers) s.close()
})

type Handler = (req: IncomingMessage, res: ServerResponse) => void

async function startServer(handler: Handler): Promise<string> {
  const server = createServer(handler)
  servers.push(server)
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve))
  const { port } = server.address() as AddressInfo
  return `http://127.0.0.1:${port}`
}

/** Builds an Access URL for a test server, with credentials embedded. */
function accessUrlFor(origin: string, userinfo: string): string {
  return origin.replace("http://", `http://${userinfo}@`) + "/simplefin"
}

/*
 * The central security property.
 *
 * The password is URL-encoded in the Access URL ("p%40ss" is "p@ss"), so this
 * also pins that the credentials are percent-decoded before being base64'd —
 * sending the still-encoded form would authenticate as a different password.
 */
test("credentials are sent as a header, not in the URL", async () => {
  let gotAuth: string | undefined
  let gotUrl = ""

  const origin = await startServer((req, res) => {
    gotAuth = req.headers.authorization
    gotUrl = req.url ?? ""
    res.setHeader("content-type", "application/json")
    res.end(accountsFixture)
  })

  const client = createClient(accessUrlFor(origin, "user123:p%40ss"))
  await client.getAccounts()

  assert.equal(gotAuth, `Basic ${btoa("user123:p@ss")}`)
  for (const secret of ["user123", "p@ss", "p%40ss"]) {
    assert.ok(!gotUrl.includes(secret), `request URL ${gotUrl} contains ${secret}`)
  }
})

test("errors never leak credentials", async () => {
  const user = "secretuser"
  const pass = "secretpass"

  const cases: Array<{ name: string; handler: Handler }> = [
    {
      name: "server error",
      handler: (_req, res) => {
        res.statusCode = 500
        res.end()
      },
    },
    {
      name: "forbidden",
      handler: (_req, res) => {
        res.statusCode = 403
        res.end()
      },
    },
    {
      name: "non-JSON body",
      handler: (_req, res) => {
        res.setHeader("content-type", "text/html")
        res.end(`<html><body>${user}:${pass}</body></html>`)
      },
    },
  ]

  for (const { name, handler } of cases) {
    const origin = await startServer(handler)
    const client = createClient(accessUrlFor(origin, `${user}:${pass}`))

    await assert.rejects(
      () => client.getAccounts(),
      (err: unknown) => {
        const msg = err instanceof Error ? err.message : String(err)
        assert.ok(!msg.includes(user), `${name}: message leaks user: ${msg}`)
        assert.ok(!msg.includes(pass), `${name}: message leaks pass: ${msg}`)
        return true
      },
    )
  }
})

/*
 * Pins the behaviour that motivated checking status before parsing: the
 * SimpleFIN Bridge answers a rejected request with an HTML page, and
 * reporting that as a JSON syntax error would send a caller hunting for a
 * parser bug instead of an expired Access URL.
 */
test("an HTML 403 page surfaces as an auth error, not a parse error", async () => {
  const origin = await startServer((_req, res) => {
    res.statusCode = 403
    res.setHeader("content-type", "text/html; charset=utf-8")
    res.end("<!doctype html><html><body><h1>Forbidden</h1></body></html>")
  })

  const client = createClient(accessUrlFor(origin, "u:p"))
  await assert.rejects(
    () => client.getAccounts(),
    (err: unknown) => {
      assert.ok(isAuthError(err), "expected an auth error")
      assert.ok(isHttpError(err))
      assert.equal(err.status, 403)
      assert.ok(!/json/i.test(err.message), `reported as a parse problem: ${err.message}`)
      return true
    },
  )
})

test("query parameters are serialized per the protocol's rules", async () => {
  const cases: Array<{ name: string; params: Parameters<ReturnType<typeof createClient>["getAccounts"]>[0]; want: string }> = [
    { name: "zero value sends only the version", params: {}, want: "version=2" },
    { name: "pending is opt-in and sent as 1", params: { pending: true }, want: "pending=1&version=2" },
    // A false flag must be absent entirely. Sending "0" is not the same thing
    // to a server that checks only for presence.
    { name: "false flags are omitted", params: { pending: false, balancesOnly: false }, want: "version=2" },
    {
      name: "dates accept a Date",
      params: { startDate: new Date(1757462400 * 1000) },
      want: "start-date=1757462400&version=2",
    },
    {
      name: "dates accept raw epoch seconds",
      params: { startDate: 1757462400 },
      want: "start-date=1757462400&version=2",
    },
    // Comma-joining would be read as one account id containing a comma.
    {
      name: "account filters repeat the key",
      params: { accounts: ["ACT-1", "ACT-2"] },
      want: "account=ACT-1&account=ACT-2&version=2",
    },
    { name: "explicit version overrides the default", params: { version: "1" }, want: "version=1" },
  ]

  for (const { name, params, want } of cases) {
    let gotQuery = ""
    const origin = await startServer((req, res) => {
      gotQuery = (req.url ?? "").split("?")[1] ?? ""
      res.setHeader("content-type", "application/json")
      res.end(`{"errlist":[],"connections":[],"accounts":[]}`)
    })

    const client = createClient(accessUrlFor(origin, "u:p"))
    await client.getAccounts(params)

    const sorted = (q: string) => q.split("&").sort().join("&")
    assert.equal(sorted(gotQuery), sorted(want), name)
  }
})

test("status codes map to typed errors", async () => {
  const cases: Array<{ status: number; check: (e: unknown) => boolean; desc: string }> = [
    { status: 403, check: isAuthError, desc: "auth error" },
    { status: 402, check: isPaymentRequiredError, desc: "payment required" },
    {
      status: 500,
      check: (e) => isHttpError(e) && e.status === 500 && !isAuthError(e),
      desc: "generic http error",
    },
  ]

  for (const { status, check, desc } of cases) {
    const origin = await startServer((_req, res) => {
      res.statusCode = status
      res.end()
    })
    const client = createClient(accessUrlFor(origin, "u:p"))

    await assert.rejects(
      () => client.getAccounts(),
      (err: unknown) => {
        assert.ok(check(err), `status ${status} did not produce ${desc}`)
        return true
      },
    )
  }
})

test("bad access URLs are rejected", () => {
  for (const bad of ["", "not a url", "ftp://example.org/simplefin", "://"]) {
    assert.throws(() => createClient(bad), Error, `createClient(${JSON.stringify(bad)}) should throw`)
  }
})

test("a malformed access URL error does not echo the credentials", () => {
  assert.throws(
    () => createClient("https://user:hunter2@exa mple.com/simplefin"),
    (err: unknown) => {
      const msg = err instanceof Error ? err.message : String(err)
      assert.ok(!msg.includes("hunter2"), `error echoes credentials: ${msg}`)
      return true
    },
  )
})

test("the fixture parses into the camelCase model", async () => {
  const origin = await startServer((_req, res) => {
    res.setHeader("content-type", "application/json")
    res.end(accountsFixture)
  })

  const client = createClient(accessUrlFor(origin, "u:p"))
  const set = await client.getAccounts({ pending: true })

  assert.equal(set.accounts.length, 1)
  assert.equal(set.accounts[0]?.id, "ACT-001")
  // Money must survive as the exact string the server sent.
  assert.equal(set.accounts[0]?.balance, "1250.33")
  assert.equal(set.accounts[0]?.availableBalance, "1200.00")
  assert.equal(set.accounts[0]?.balanceDate, 1757635200)
  assert.equal(set.errlist[0]?.code, ErrorCodes.ConnectionAuth)
  assert.equal(set.errlist[0]?.connId, "CONN-1")
})

test("postedTime falls back to transactedAt for pending transactions", () => {
  const cases: Array<{ name: string; tx: Transaction; want: number | null }> = [
    {
      name: "posted wins",
      tx: { id: "1", posted: 1757548800, transactedAt: 1757462400, amount: "0", description: "", unknown: {} },
      want: 1757548800,
    },
    {
      name: "pending falls back to transactedAt",
      tx: { id: "2", posted: 0, transactedAt: 1757462400, amount: "0", description: "", unknown: {} },
      want: 1757462400,
    },
    { name: "neither set is null", tx: { id: "3", posted: 0, amount: "0", description: "", unknown: {} }, want: null },
    {
      name: "negative is treated as absent",
      tx: { id: "4", posted: -1, amount: "0", description: "", unknown: {} },
      want: null,
    },
  ]

  for (const { name, tx, want } of cases) {
    const got = postedTime(tx)
    if (want === null) {
      assert.equal(got, null, name)
    } else {
      assert.equal(got?.getTime(), want * 1000, name)
    }
  }
})

test("info reports supported versions", async () => {
  let gotPath = ""
  const origin = await startServer((req, res) => {
    gotPath = (req.url ?? "").split("?")[0] ?? ""
    res.setHeader("content-type", "application/json")
    res.end(`{"versions":["1","2"]}`)
  })

  const client = createClient(accessUrlFor(origin, "u:p"))
  const info = await client.info()

  assert.equal(gotPath, "/simplefin/info")
  assert.deepEqual(info.versions, ["1", "2"])
})

/*
 * A custom currency URL is chosen by the server and points at an arbitrary
 * third-party host. Attaching the Access URL's credentials would hand them to
 * that host.
 */
test("resolveCurrency does not send credentials to the third-party host", async () => {
  let gotAuth: string | undefined
  const currencyOrigin = await startServer((req, res) => {
    gotAuth = req.headers.authorization
    res.setHeader("content-type", "application/json")
    res.end(`{"name":"Example Airline Miles","abbr":"miles"}`)
  })

  const apiOrigin = await startServer((_req, res) => res.end("{}"))
  const client = createClient(accessUrlFor(apiOrigin, "secretuser:secretpass"))

  const currency = await client.resolveCurrency(`${currencyOrigin}/flight-miles`)

  assert.equal(gotAuth, undefined, "credentials were sent to the currency host")
  assert.equal(currency.abbr, "miles")
  assert.equal(currency.name, "Example Airline Miles")
})

test("isCustomCurrency distinguishes URLs from ISO codes", () => {
  assert.equal(isCustomCurrency("USD"), false)
  assert.equal(isCustomCurrency("ZMW"), false)
  assert.equal(isCustomCurrency(""), false)
  assert.equal(isCustomCurrency("https://www.example.com/flight-miles"), true)
  assert.equal(isCustomCurrency("http://example.com/points"), true)
})
