#!/usr/bin/env bash
#
# Proves the Go and Node SDKs parse every fixture identically.
#
# The per-language conformance tests already compare each SDK against the
# hand-authored .expect.json files. This goes further and diffs the two SDKs'
# canonical output against *each other*, so a fixture that someone adds without
# an expectation file still cannot drift. Adding a language means adding a
# branch here.

set -euo pipefail

cd "$(dirname "$0")/.."
root="$PWD"
golden="$root/spec/golden"

echo "==> Regenerating both targets"
go run ./cmd/simplefin-gen generate --lang go >/dev/null
go run ./cmd/simplefin-gen generate --lang node >/dev/null

echo "==> Verifying generated output matches what is committed"
go run ./cmd/simplefin-gen verify

echo "==> Building the Node SDK"
# Install first when absent. The script has to work on a fresh clone and in
# CI, not only in a tree where someone has already run npm install --
# tsconfig declares @types/node, so a missing node_modules fails the build
# with a confusing "cannot find type definition file" rather than an obvious
# missing-dependency error.
if [ ! -d sdk/node/node_modules ]; then
  echo "    installing node dependencies"
  (cd sdk/node && { npm ci --silent || npm install --silent; })
fi
(cd sdk/node && npm run --silent build)

echo "==> Building the Go canonicalizer"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
go build -o "$tmp/canonicalize" ./cmd/canonicalize

# The Node side of the comparison. Kept inline so the script is the whole
# story rather than pointing at another file.
# The import is written as an absolute path because this file lives in a
# temporary directory, so a relative specifier would resolve against /tmp.
cat > "$tmp/canonicalize.mjs" <<NODE
import { readFileSync } from "node:fs"
import { canonicalJSON, normalizeAccountSet } from "$root/sdk/node/dist/index.js"

const input = JSON.parse(readFileSync(process.argv[2], "utf8"))
console.log(canonicalJSON(normalizeAccountSet(input)))
NODE

failed=0
checked=0

for fixture in "$golden"/*.json; do
  case "$fixture" in
    *.expect.json) continue ;;
  esac

  name="$(basename "$fixture")"
  checked=$((checked + 1))

  go_out="$("$tmp/canonicalize" "$fixture")"
  node_out="$(node "$tmp/canonicalize.mjs" "$fixture")"

  if [ "$go_out" = "$node_out" ]; then
    echo "    ok   $name"
  else
    echo "    FAIL $name — Go and Node disagree:"
    diff <(printf '%s\n' "$go_out") <(printf '%s\n' "$node_out") || true
    failed=$((failed + 1))
  fi
done

if [ "$checked" -eq 0 ]; then
  # An empty fixture directory would otherwise report as a pass.
  echo "==> No fixtures found in $golden" >&2
  exit 1
fi

# Agreeing on what to accept is only half the contract. Both SDKs must also
# reject the same payloads — a lenient parser that coerces a wrong-typed
# balance would otherwise pass every check above.
echo "==> Checking both languages reject the same malformed payloads"
malformed_checked=0

for fixture in "$root"/spec/malformed/*.json; do
  name="$(basename "$fixture")"
  malformed_checked=$((malformed_checked + 1))

  go_rejected=0
  "$tmp/canonicalize" "$fixture" >/dev/null 2>&1 || go_rejected=1

  node_rejected=0
  node "$tmp/canonicalize.mjs" "$fixture" >/dev/null 2>&1 || node_rejected=1

  if [ "$go_rejected" -eq 1 ] && [ "$node_rejected" -eq 1 ]; then
    echo "    ok   $name (both reject)"
  else
    echo "    FAIL $name — go_rejected=$go_rejected node_rejected=$node_rejected" >&2
    failed=$((failed + 1))
  fi
done

if [ "$malformed_checked" -eq 0 ]; then
  echo "==> No fixtures found in $root/spec/malformed" >&2
  exit 1
fi

if [ "$failed" -ne 0 ]; then
  echo "==> $failed of $checked fixture(s) differ between languages" >&2
  exit 1
fi

echo "==> $checked golden + $malformed_checked malformed fixture(s) agree across Go and Node"
