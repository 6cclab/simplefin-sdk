/**
 * A client for the SimpleFIN protocol (https://www.simplefin.org/protocol.html).
 *
 * This module speaks protocol types only and knows nothing about any
 * particular application's domain. Amounts are exposed exactly as the server
 * sent them — as strings — because SimpleFIN transmits them that way precisely
 * to avoid the precision loss of a binary float.
 *
 * The data types, error codes, query parameter serialization and the v1/v2
 * normalizer are generated from spec/simplefin.yaml. Only the HTTP plumbing in
 * this file, claim.ts, and their tests are hand-written.
 */

import { statusError } from "./errors.js"
import type { AccountSet, Currency, Info, UnknownFields } from "./models.js"
import { normalizeAccountSet } from "./normalize.js"
import { encodeGetAccountsParams, type GetAccountsParams } from "./params.js"

/** Bounds a single SimpleFIN request, in milliseconds. */
export const DEFAULT_TIMEOUT_MS = 60_000

/** Options accepted when constructing a client. */
export interface ClientOptions {
  /** Overrides the fetch implementation, primarily for tests. */
  fetch?: typeof globalThis.fetch
  /** Per-request timeout in milliseconds. Defaults to {@link DEFAULT_TIMEOUT_MS}. */
  timeoutMs?: number
}

/** A client bound to one SimpleFIN Access URL. */
export interface SimpleFinClient {
  /**
   * Fetches accounts, balances, and transactions.
   *
   * Pass `pending: true` to receive pending transactions; the server omits
   * them by default. A 403 means authentication failed or access was revoked —
   * SimpleFIN auth is all-or-nothing per Access URL, so it is the signal to
   * re-claim the whole Access URL rather than relink a single connection.
   *
   * The type parameters describe wire keys the specification does not define,
   * which real servers do send. Supply them once here and they reach every
   * account and nested transaction:
   *
   * ```ts
   * const set = await client.getAccounts<
   *   { holdings: Holding[] },
   *   { payee: string; memo: string; mcc: string }
   * >({ pending: true })
   *
   * set.accounts[0].unknown.holdings          // typed
   * set.accounts[0].transactions?.[0]?.unknown.payee  // typed
   * ```
   */
  getAccounts<AccountUnknown = UnknownFields, TransactionUnknown = UnknownFields>(
    params?: GetAccountsParams,
  ): Promise<AccountSet<AccountUnknown, TransactionUnknown>>

  /**
   * Reports which protocol versions the server supports.
   *
   * Note that a server is not obliged to make this useful: the SimpleFIN
   * Bridge currently redirects `/info` to its marketing homepage rather than
   * returning JSON, which surfaces here as a non-JSON body error.
   */
  info(): Promise<Info>

  /**
   * Resolves a custom currency definition.
   *
   * When an Account's `currency` is a URL rather than an ISO 4217 code, it
   * identifies a custom currency such as frequent flyer miles or reward
   * points. All strings returned here must be sanitized before display.
   */
  resolveCurrency(currencyUrl: string): Promise<Currency>
}

/**
 * Builds a client from a SimpleFIN Access URL.
 *
 * An Access URL embeds Basic Auth credentials
 * (`https://user:pass@host/simplefin`). Those credentials are split out of the
 * URL and sent as an `Authorization` header instead of being left in the
 * request URL, so they cannot leak into request logs, redirect targets, or
 * error messages. This is not merely prudent in Node: `fetch` rejects a URL
 * containing userinfo outright, so passing the Access URL through unchanged
 * would fail before any request was made.
 */
export function createClient(
  accessUrl: string,
  options: ClientOptions = {},
): SimpleFinClient {
  let parsed: URL
  try {
    parsed = new URL(accessUrl.trim())
  } catch {
    // Deliberately not including the cause: URL parse errors quote the input,
    // which would put the credentials into the message.
    throw new Error("simplefin: access URL is not a valid URL")
  }

  if (parsed.protocol !== "https:" && parsed.protocol !== "http:") {
    throw new Error("simplefin: access URL must be http or https")
  }
  if (parsed.host === "") {
    throw new Error("simplefin: access URL has no host")
  }

  let authHeader = ""
  if (parsed.username !== "" || parsed.password !== "") {
    // decodeURIComponent because URL percent-encodes userinfo, so a password
    // of "p@ss" arrives here as "p%40ss" and must be decoded before it is
    // base64'd or the server will reject it.
    const user = safeDecode(parsed.username)
    const pass = safeDecode(parsed.password)
    authHeader = `Basic ${btoa(`${user}:${pass}`)}`
    parsed.username = ""
    parsed.password = ""
  }

  const baseUrl = parsed.toString().replace(/\/$/, "")
  const doFetch = options.fetch ?? globalThis.fetch
  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS

  async function getJson(
    url: string,
    op: string,
    auth: boolean,
  ): Promise<unknown> {
    const headers: Record<string, string> = { Accept: "application/json" }
    if (auth && authHeader !== "") headers["Authorization"] = authHeader

    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), timeoutMs)

    let response: Response
    try {
      response = await doFetch(url, { headers, signal: controller.signal })
    } catch {
      // Not including the cause: transport errors embed the request URL,
      // which for an authenticated call is the Access URL.
      throw new Error(`simplefin: ${op} request failed`)
    } finally {
      clearTimeout(timer)
    }

    // The status is checked before any parse is attempted. The SimpleFIN
    // Bridge serves HTML error pages, so parsing first would turn a plain 403
    // into a confusing JSON syntax error and hide the real cause.
    const statusErr = statusError(response.status, op)
    if (statusErr) throw statusErr

    const body = await response.text()
    try {
      return JSON.parse(body)
    } catch {
      // Report the content type rather than the body: the body of a misrouted
      // authenticated request can echo back credentials.
      const contentType = response.headers.get("content-type") ?? ""
      throw new Error(
        `simplefin: ${op} returned a body that is not JSON (content-type ${JSON.stringify(contentType)})`,
      )
    }
  }

  return {
    async getAccounts<AccountUnknown = UnknownFields, TransactionUnknown = UnknownFields>(
      params: GetAccountsParams = {},
    ): Promise<AccountSet<AccountUnknown, TransactionUnknown>> {
      const query = encodeGetAccountsParams(params).toString()
      const raw = await getJson(`${baseUrl}/accounts?${query}`, "GET /accounts", true)
      return normalizeAccountSet<AccountUnknown, TransactionUnknown>(raw)
    },

    async info(): Promise<Info> {
      const raw = await getJson(`${baseUrl}/info`, "GET /info", false)
      const versions = (raw as { versions?: unknown })?.versions
      return {
        versions: Array.isArray(versions)
          ? versions.filter((v): v is string => typeof v === "string")
          : [],
      }
    },

    async resolveCurrency(currencyUrl: string): Promise<Currency> {
      let target: URL
      try {
        target = new URL(currencyUrl.trim())
      } catch {
        throw new Error("simplefin: currency URL is not a valid URL")
      }
      if (target.protocol !== "https:" && target.protocol !== "http:") {
        throw new Error("simplefin: currency URL must be http or https")
      }

      // A custom currency URL points at an arbitrary third-party host, so the
      // Access URL credentials must not be attached to it.
      const raw = (await getJson(target.toString(), "GET currency", false)) as
        | Record<string, unknown>
        | null
      return {
        name: typeof raw?.["name"] === "string" ? raw["name"] : "",
        abbr: typeof raw?.["abbr"] === "string" ? raw["abbr"] : "",
      }
    },
  }
}

/**
 * Reports whether an Account's `currency` is a custom currency URL rather than
 * an ISO 4217 code, and so needs {@link SimpleFinClient.resolveCurrency}.
 */
export function isCustomCurrency(currency: string): boolean {
  const c = currency.trim()
  return c.startsWith("https://") || c.startsWith("http://")
}

/** Percent-decodes userinfo, falling back to the raw value if it is malformed. */
function safeDecode(value: string): string {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}
