import assert from "node:assert/strict"
import { dirname, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import { test } from "node:test"

import ts from "typescript"

/*
 * Editor support is a stated requirement of this SDK, so it is tested rather
 * than assumed.
 *
 * These queries go through the TypeScript language service — the same engine
 * behind VS Code's IntelliSense — against the built `dist/`, which is what a
 * consumer actually installs. A type that resolves under `tsc --noEmit` can
 * still offer no completions and no hover text; only the language service can
 * tell you which.
 *
 * Each probe gets its own virtual source file. An incomplete expression like
 * `account.` is a syntax error, and TypeScript's error recovery makes
 * anything after it unreliable.
 */

const packageRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..")

const options: ts.CompilerOptions = {
  target: ts.ScriptTarget.ES2022,
  module: ts.ModuleKind.NodeNext,
  moduleResolution: ts.ModuleResolutionKind.NodeNext,
  strict: true,
  noEmit: true,
}

function serviceFor(text: string): { service: ts.LanguageService; fileName: string } {
  const fileName = resolve(packageRoot, "__intellisense_probe.ts")
  const host: ts.LanguageServiceHost = {
    getScriptFileNames: () => [fileName],
    getScriptVersion: () => "1",
    getScriptSnapshot: (f) =>
      f === fileName
        ? ts.ScriptSnapshot.fromString(text)
        : ts.sys.fileExists(f)
          ? ts.ScriptSnapshot.fromString(ts.sys.readFile(f) ?? "")
          : undefined,
    getCurrentDirectory: () => packageRoot,
    getCompilationSettings: () => options,
    getDefaultLibFileName: (o) => ts.getDefaultLibFilePath(o),
    fileExists: ts.sys.fileExists,
    readFile: ts.sys.readFile,
    readDirectory: ts.sys.readDirectory,
    directoryExists: ts.sys.directoryExists,
    getDirectories: ts.sys.getDirectories,
  }
  return { service: ts.createLanguageService(host, ts.createDocumentRegistry()), fileName }
}

// Resolved by Node's self-reference support, so this exercises the published
// `exports` map rather than a relative path into src.
const PRELUDE = `import { createClient, isHttpError, type SimpleFinError, type Account, type Transaction } from "@6cclab/simplefin"
const client = createClient("https://u:p@host.invalid/simplefin")
declare const account: Account
declare const tx: Transaction
declare const err: SimpleFinError
declare const caught: unknown
`

function completionsAt(body: string, marker: string): string[] {
  const text = PRELUDE + body
  const { service, fileName } = serviceFor(text)
  const at = text.indexOf(marker)
  assert.ok(at >= 0, `probe marker not found: ${marker}`)
  const info = service.getCompletionsAtPosition(fileName, at + marker.length, {})
  return (info?.entries ?? []).map((e) => e.name)
}

/** Hover text as a user sees it: the description plus any @remarks. */
function hoverAt(body: string, marker: string): string {
  const text = PRELUDE + body
  const { service, fileName } = serviceFor(text)
  const at = text.indexOf(marker)
  assert.ok(at >= 0, `probe marker not found: ${marker}`)
  const q = service.getQuickInfoAtPosition(fileName, at + marker.length - 1)
  const doc = ts.displayPartsToString(q?.documentation ?? [])
  const tags = (q?.tags ?? [])
    .map((t) => `@${t.name} ${ts.displayPartsToString(t.text ?? [])}`)
    .join(" ")
  // Hover text is hard-wrapped, so collapse whitespace before matching:
  // a literal space in a pattern would otherwise land on a newline.
  return [doc, tags].filter(Boolean).join(" ").replace(/\s+/g, " ").trim()
}

function assertCompletes(got: string[], wanted: string[], label: string): void {
  const missing = wanted.filter((w) => !got.includes(w))
  assert.deepEqual(missing, [], `${label} — missing ${JSON.stringify(missing)}`)
}

test("query parameters autocomplete", () => {
  assertCompletes(
    completionsAt("async function f() { await client.getAccounts({ ", "getAccounts({ "),
    ["startDate", "endDate", "pending", "accounts", "balancesOnly", "version"],
    "getAccounts params",
  )
})

test("the version parameter narrows to the supported literals", () => {
  assertCompletes(
    completionsAt(`async function f() { await client.getAccounts({ version: "" }) }`, 'version: "'),
    ["1", "2"],
    "version union",
  )
})

test("return types autocomplete", () => {
  assertCompletes(
    completionsAt("const x = account.", "const x = account."),
    ["id", "name", "connId", "connName", "currency", "balance", "availableBalance", "balanceDate", "transactions"],
    "Account members",
  )
  assertCompletes(
    completionsAt("async function f() { const set = await client.getAccounts(); set. }", "set."),
    ["accounts", "connections", "errlist"],
    "AccountSet members",
  )
})

test("error codes autocomplete as literals", () => {
  assertCompletes(
    completionsAt(`const y = err.code === "";`, 'err.code === "'),
    ["gen.", "gen.api", "gen.auth", "con.", "con.auth", "act.", "act.failed", "act.missingdata"],
    "ErrorCode union",
  )
})

test("errors expose their connection and account ids", () => {
  assertCompletes(
    completionsAt("const z = err.", "const z = err."),
    ["code", "msg", "connId", "accountId"],
    "SimpleFinError members",
  )
})

// TypeScript types a `catch` binding as `unknown`, so without the generated
// guards a caller would need an `as` cast to reach `status`.
test("a caught error narrows through the generated guard", () => {
  assertCompletes(
    completionsAt("function f() { if (isHttpError(caught)) { caught. } }", "caught."),
    ["status", "op", "message", "name"],
    "narrowed error members",
  )
})

test("hover text is the protocol specification's own wording", () => {
  const availableBalance = hoverAt("const a = account.availableBalance", "account.availableBalance")
  assert.match(availableBalance, /available balance of the account/i)
  // The money warning must reach the popup, not just the source.
  assert.match(availableBalance, /never parse as a float/i)

  assert.match(hoverAt("const p = tx.posted", "tx.posted"), /if the transaction is pending, this may be 0/i)
  assert.match(hoverAt("const b = account.balanceDate", "account.balanceDate"), /zero or less means absent/i)
  assert.match(
    hoverAt("const c = account.connId", "account.connId"),
    /id of the account's connection/i,
  )
})
