# Malformed fixtures

Payloads that every SDK must **reject**, not coerce.

Each one is a field present on the wire with the wrong type. The rule both
SDKs implement — matching Go's `encoding/json` semantics — is:

- a missing field, or an explicit `null`, yields the zero value
- a field present with the wrong type is an error

The second half matters most for money. A server sending the number `12.5`
where the protocol documents a string is broken, and a client that quietly
renders that as an empty balance is more dangerous than one that refuses the
response.

Valid payloads, including ones with absent and null fields, live in
`../golden/` instead and are compared against hand-authored expectations.
