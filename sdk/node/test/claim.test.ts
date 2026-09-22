import assert from "node:assert/strict"
import { createServer, type Server, type IncomingMessage, type ServerResponse } from "node:http"
import type { AddressInfo } from "node:net"
import { after, test } from "node:test"

import { claim, decodeSetupToken, isAuthError } from "../dist/index.js"

const servers: Server[] = []
after(() => {
  for (const s of servers) s.close()
})

async function startServer(
  handler: (req: IncomingMessage, res: ServerResponse) => void,
): Promise<string> {
  const server = createServer(handler)
  servers.push(server)
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve))
  const { port } = server.address() as AddressInfo
  return `http://127.0.0.1:${port}`
}

const claimUrl = "https://bridge.example/simplefin/claim/abc123"
const token = btoa(claimUrl)

test("decodeSetupToken decodes a base64 URL", () => {
  assert.equal(decodeSetupToken(token), claimUrl)
})

// Users paste these by hand out of a browser, so stray whitespace is the
// normal case rather than the exceptional one.
test("decodeSetupToken tolerates surrounding whitespace", () => {
  assert.equal(decodeSetupToken(`  \n\t${token}\n  `), claimUrl)
})

test("decodeSetupToken rejects bad input", () => {
  const bad: Record<string, string> = {
    empty: "",
    "whitespace only": "   ",
    "not base64": "!!!not base64!!!",
    "base64 of nonsense": btoa("hello there"),
    "base64 of a non-URL": btoa("ftp://example.org/claim"),
  }
  for (const [name, input] of Object.entries(bad)) {
    assert.throws(() => decodeSetupToken(input), Error, name)
  }
})

test("claim POSTs and trims the returned access URL", async () => {
  const accessUrl = "https://user:pass@bridge.example/simplefin"
  let gotMethod = ""

  const origin = await startServer((req, res) => {
    gotMethod = req.method ?? ""
    // A trailing newline is common from servers that echo the URL.
    res.end(`${accessUrl}\n`)
  })

  const got = await claim(btoa(`${origin}/claim/abc`))

  assert.equal(gotMethod, "POST")
  assert.equal(got, accessUrl)
})

/*
 * Per the protocol checklist, a 403 here can mean the token was already
 * claimed by someone else and the user's data is compromised, so it must be
 * distinguishable from any other failure.
 */
test("a 403 during claim is an auth error", async () => {
  const origin = await startServer((_req, res) => {
    res.statusCode = 403
    res.end()
  })

  await assert.rejects(
    () => claim(btoa(`${origin}/claim/abc`)),
    (err: unknown) => {
      assert.ok(isAuthError(err), "expected an auth error")
      return true
    },
  )
})

test("a non-URL claim response is rejected without echoing it", async () => {
  const secret = "oops-this-might-be-a-credential"
  const origin = await startServer((_req, res) => res.end(secret))

  await assert.rejects(
    () => claim(btoa(`${origin}/claim/abc`)),
    (err: unknown) => {
      const msg = err instanceof Error ? err.message : String(err)
      assert.ok(msg.includes("access URL"), `error should mention the access URL: ${msg}`)
      assert.ok(!msg.includes(secret), `error echoes the response body: ${msg}`)
      return true
    },
  )
})
