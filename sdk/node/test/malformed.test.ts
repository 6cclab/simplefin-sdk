import assert from "node:assert/strict"
import { readdirSync, readFileSync } from "node:fs"
import { join } from "node:path"
import { fileURLToPath } from "node:url"
import { test } from "node:test"

import { normalizeAccountSet } from "../dist/index.js"

/*
 * Payloads every SDK must reject rather than coerce.
 *
 * The rule both language targets implement is Go's encoding/json semantics: a
 * missing field or an explicit null yields the zero value, but a field present
 * with the wrong type is an error. That second half matters most for money — a
 * server sending the number 12.5 where the protocol documents a string is
 * broken, and quietly rendering that as an empty balance would be worse than
 * refusing the response.
 */
const malformedDir = fileURLToPath(new URL("../../../spec/malformed", import.meta.url))
const goldenDir = fileURLToPath(new URL("../../../spec/golden", import.meta.url))

const malformed = readdirSync(malformedDir).filter((n) => n.endsWith(".json"))

test("malformed fixtures are present", () => {
  assert.ok(malformed.length > 0, `no fixtures found in ${malformedDir}`)
})

for (const name of malformed) {
  test(`rejects malformed: ${name.replace(/\.json$/, "")}`, () => {
    const input = JSON.parse(readFileSync(join(malformedDir, name), "utf8"))
    assert.throws(
      () => normalizeAccountSet(input),
      (err: unknown) => {
        // The error must name the offending field. A bare "invalid response"
        // would leave a caller no way to report the problem upstream.
        assert.ok(err instanceof Error)
        assert.match(err.message, /simplefin: .+ should be/)
        return true
      },
      name,
    )
  })
}

// Valid payloads must still parse. Without this, a parser that rejected
// everything would pass the tests above.
test("a sparse but valid payload is accepted", () => {
  const input = JSON.parse(readFileSync(join(goldenDir, "sparse.json"), "utf8"))
  const set = normalizeAccountSet(input)

  assert.equal(set.accounts.length, 2)
  // An absent available-balance and an explicit null must both read as absent
  // rather than as an empty string.
  for (const account of set.accounts) {
    assert.equal(account.availableBalance, undefined, account.id)
  }
})
