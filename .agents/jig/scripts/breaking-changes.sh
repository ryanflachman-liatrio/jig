#!/bin/bash
# Detect breaking changes in OpenAPI or Protobuf API schemas.
# Writes findings to stdout; step captures in .jig/breaking-changes-output.txt.
# Exits 0 on PASS or SKIP; exits 1 if BREAKING changes detected (on_failure = continue handles this).
set -e
mkdir -p .jig

FOUND_SCHEMA=false
BREAKING=false

# OpenAPI/Swagger detection
OPENAPI_FILE=""
for f in openapi.yaml openapi.json openapi.yml swagger.yaml swagger.json swagger.yml; do
  [ -f "$f" ] && OPENAPI_FILE="$f" && break
done

if [ -n "$OPENAPI_FILE" ]; then
  FOUND_SCHEMA=true
  echo "=== OpenAPI Breaking Change Detection ($OPENAPI_FILE) ==="
  if command -v openapi-diff >/dev/null 2>&1; then
    git show HEAD~1:"$OPENAPI_FILE" > .jig/openapi-base-temp.yaml 2>/dev/null || {
      echo "SKIP: no previous version of $OPENAPI_FILE in git history"
      exit 0
    }
    DIFF_OUTPUT=$(openapi-diff .jig/openapi-base-temp.yaml "$OPENAPI_FILE" 2>&1 || true)
    echo "$DIFF_OUTPUT"
    if echo "$DIFF_OUTPUT" | grep -q "Breaking"; then
      BREAKING=true
      echo "BREAKING CHANGES DETECTED"
    else
      echo "PASS: no breaking changes"
    fi
  elif command -v npx >/dev/null 2>&1 && npx --yes @openapitools/openapi-diff --version >/dev/null 2>&1; then
    git show HEAD~1:"$OPENAPI_FILE" > .jig/openapi-base-temp.yaml 2>/dev/null || {
      echo "SKIP: no previous version of $OPENAPI_FILE in git history"
      exit 0
    }
    DIFF_OUTPUT=$(npx @openapitools/openapi-diff .jig/openapi-base-temp.yaml "$OPENAPI_FILE" 2>&1 || true)
    echo "$DIFF_OUTPUT"
    if echo "$DIFF_OUTPUT" | grep -q "Breaking"; then
      BREAKING=true
      echo "BREAKING CHANGES DETECTED"
    else
      echo "PASS: no breaking changes"
    fi
  else
    echo "SKIP: openapi-diff not installed. Install: npm install -g @openapitools/openapi-diff"
  fi
fi

# Protobuf detection
PROTO_COUNT=$(find . -name "*.proto" -not -path "*/vendor/*" -not -path "*/node_modules/*" 2>/dev/null | wc -l | tr -d ' ')

if [ "$PROTO_COUNT" -gt 0 ]; then
  if command -v buf >/dev/null 2>&1; then
    FOUND_SCHEMA=true
    echo "=== Protobuf Breaking Change Detection (buf) ==="
    BUF_OUTPUT=$(buf breaking --against ".git#branch=HEAD~1" 2>&1 || echo "EXIT_NONZERO")
    echo "$BUF_OUTPUT"
    if echo "$BUF_OUTPUT" | grep -q "EXIT_NONZERO"; then
      BREAKING=true
      echo "BREAKING CHANGES DETECTED"
    else
      echo "PASS: no breaking changes"
    fi
  else
    echo "SKIP: buf not installed. Install: https://buf.build/docs/installation"
  fi
fi

if [ "$FOUND_SCHEMA" = false ]; then
  echo "SKIP: no API schema files found (OpenAPI or Protobuf)"
  exit 0
fi

if [ "$BREAKING" = true ]; then
  exit 1
fi

exit 0
