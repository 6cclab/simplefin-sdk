/**
 * @6cclab/simplefin — a typed client for the SimpleFIN protocol.
 *
 * @see https://www.simplefin.org/protocol.html
 */

export { claim, decodeSetupToken, type ClaimOptions } from "./claim.js"
export {
  createClient,
  isCustomCurrency,
  DEFAULT_TIMEOUT_MS,
  type ClientOptions,
  type SimpleFinClient,
} from "./client.js"
export {
  SimpleFinHttpError,
  SimpleFinAuthError,
  SimpleFinPaymentRequiredError,
  isHttpError,
  isAuthError,
  isPaymentRequiredError,
  statusError,
} from "./errors.js"
export {
  ErrorCodes,
  errorCodePrefix,
  isAuthErrorCode,
  isKnownErrorCode,
  normalizeErrorCode,
  DEFAULT_PROTOCOL_VERSION,
  type ErrorCode,
  type ErrorPrefix,
  type ProtocolVersion,
} from "./errorcodes.js"
export {
  epochToDate,
  balanceTime,
  postedTime,
  transactedTime,
  type Account,
  type AccountSet,
  type Connection,
  type Currency,
  type Info,
  type OrgV1,
  type SimpleFinError,
  type Transaction,
} from "./models.js"
export {
  normalizeAccountSet,
  normalizeErrors,
  parseAccount,
  parseConnection,
  parseTransaction,
} from "./normalize.js"
export {
  encodeGetAccountsParams,
  type GetAccountsParams,
} from "./params.js"
export { canonicalJSON } from "./canonical.js"
