import { statusError } from "./errors.js"

/** Options accepted by {@link claim}. */
export interface ClaimOptions {
  /** Overrides the fetch implementation, primarily for tests. */
  fetch?: typeof globalThis.fetch
  /** Request timeout in milliseconds. */
  timeoutMs?: number
}

/**
 * Decodes a SimpleFIN Token into the claim URL it points at.
 *
 * A SimpleFIN Token is a Base64-encoded URL. Surrounding whitespace is
 * tolerated because users paste these by hand out of a browser.
 */
export function decodeSetupToken(setupToken: string): string {
  const trimmed = setupToken.trim()
  if (trimmed === "") {
    throw new Error("simplefin: setup token is empty")
  }

  let decoded: string
  try {
    decoded = atob(trimmed)
  } catch {
    throw new Error("simplefin: setup token is not valid base64")
  }

  const claimUrl = decoded.trim()
  if (!claimUrl.startsWith("http://") && !claimUrl.startsWith("https://")) {
    throw new Error("simplefin: setup token did not decode to a URL")
  }
  return claimUrl
}

/**
 * Exchanges a SimpleFIN Token for an Access URL.
 *
 * This is one-shot. The server consumes the token whether or not the caller
 * successfully stores the result, so a failure here means a new setup token
 * must be generated — never retry with the same one.
 *
 * A 403 means the token either does not exist or has already been claimed by
 * someone else. The protocol's application checklist requires notifying the
 * user in that case, because it can mean their transaction information has
 * been compromised.
 *
 * Store the returned Access URL at least as securely as the financial data
 * itself: it embeds Basic Auth credentials and is the only thing standing
 * between a reader and the user's accounts.
 */
export async function claim(
  setupToken: string,
  options: ClaimOptions = {},
): Promise<string> {
  const claimUrl = decodeSetupToken(setupToken)
  const doFetch = options.fetch ?? globalThis.fetch
  const timeoutMs = options.timeoutMs ?? 60_000

  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)

  let response: Response
  try {
    response = await doFetch(claimUrl, {
      method: "POST",
      signal: controller.signal,
    })
  } catch {
    // Not including the cause: transport errors embed the claim URL, which is
    // a one-time secret.
    throw new Error("simplefin: claim request failed")
  } finally {
    clearTimeout(timer)
  }

  const statusErr = statusError(response.status, "POST claim")
  if (statusErr) throw statusErr

  const accessUrl = (await response.text()).trim()
  if (!accessUrl.startsWith("http://") && !accessUrl.startsWith("https://")) {
    // Deliberately not echoing the body: a partial or garbled response can
    // still contain the credentials it was meant to deliver.
    throw new Error("simplefin: claim response was not an access URL")
  }
  return accessUrl
}
