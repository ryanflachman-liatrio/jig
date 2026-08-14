#!/bin/bash
# Gate F: scan proof artifact files for real credential patterns.
# Exits 1 when real credentials found; on_failure = "continue" lets the report capture it.
set -euo pipefail
mkdir -p .jig

OUTPUT=".jig/credentials-check.txt"
PROOFS_DIR="docs/specs"

if [ ! -d "$PROOFS_DIR" ]; then
  echo "GATE F: SKIP" | tee "$OUTPUT"
  echo "No docs/specs directory found — skipping credential scan." >> "$OUTPUT"
  exit 0
fi

# Grep for credential-like patterns across all proof markdown files.
# Excludes obvious placeholders: your-*, <tag>, example, REDACTED, ***, test-*, fake-*
HITS=$(grep -rn --include="*.md" \
  -E 'password\s*[=:]\s*\S{6,}|Bearer\s+[A-Za-z0-9._/\-]{20,}|AKIA[0-9A-Z]{16}|-----BEGIN[[:space:]]+(RSA |EC )?PRIVATE|api[_-]?key\s*[=:]\s*\S{6,}' \
  "$PROOFS_DIR" 2>/dev/null \
  | grep -viE 'your[-_]|<[a-z]|placeholder|example\.|REDACTED|\*{3,}|test[-_]|fake[-_]|changeme|hunter2' \
  || true)

if [ -z "$HITS" ]; then
  { echo "GATE F: PASS"; echo "No real credential patterns found in proof artifacts."; } | tee "$OUTPUT"
else
  { echo "GATE F: FAIL"; echo "Real credential patterns found in proof artifacts:"; echo ""; echo "$HITS"; } | tee "$OUTPUT"
  exit 1
fi
