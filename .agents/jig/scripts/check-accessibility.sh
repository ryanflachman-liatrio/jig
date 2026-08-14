#!/bin/bash
# WCAG 2.1 AA static grep scan across UI source files.
# Exits 1 when violations found; on_failure = "continue" lets the compliance report capture it.
set -euo pipefail
mkdir -p .jig

OUTPUT=".jig/accessibility-check.txt"

# Find UI files, excluding generated/dependency directories
UI_FILES=$(find . \( \
  -name "*.tsx" -o -name "*.jsx" -o -name "*.vue" \
  -o -name "*.html" -o -name "*.svelte" -o -name "*.erb" \
  \) \
  ! -path "*/node_modules/*" ! -path "*/.git/*" \
  ! -path "*/dist/*"  ! -path "*/build/*" ! -path "*/.next/*" \
  2>/dev/null || true)

if [ -z "$UI_FILES" ]; then
  { echo "WCAG CHECK: SKIP"; echo "No UI files found — accessibility check not applicable."; } | tee "$OUTPUT"
  exit 0
fi

FAIL_COUNT=0

{
  echo "=== WCAG 2.1 AA Static Scan ==="
  echo ""

  # 1.1.1 — img elements without alt attribute
  HITS=$(echo "$UI_FILES" | xargs grep -ln '<img[^>]*>' 2>/dev/null \
    | xargs grep -n '<img[^>]*>' 2>/dev/null \
    | grep -v 'alt=' || true)
  if [ -n "$HITS" ]; then
    echo "FAIL [1.1.1] img elements missing alt attribute:"
    echo "$HITS" | sed 's/^/  /'
    FAIL_COUNT=$((FAIL_COUNT + 1))
  fi

  # 1.3.1 — input elements without aria-label or aria-labelledby (heuristic)
  HITS=$(echo "$UI_FILES" | xargs grep -n '<input[^>]*>' 2>/dev/null \
    | grep -v 'aria-label\|aria-labelledby\|type="hidden"\|type=.hidden' || true)
  if [ -n "$HITS" ]; then
    echo "WARN [1.3.1] input elements potentially missing labels (verify manually):"
    echo "$HITS" | sed 's/^/  /'
  fi

  # 2.1.1 — onClick handlers without keyboard equivalent
  HITS=$(echo "$UI_FILES" | xargs grep -n 'onClick=' 2>/dev/null \
    | grep -v 'onKey\|onKeyDown\|onKeyPress\|onKeyUp\|role="button"\|<button\|<a ' || true)
  if [ -n "$HITS" ]; then
    echo "FAIL [2.1.1] onClick without keyboard handler (non-interactive elements):"
    echo "$HITS" | sed 's/^/  /'
    FAIL_COUNT=$((FAIL_COUNT + 1))
  fi

  # 4.1.2 — tabIndex > 0 breaks natural tab order
  HITS=$(echo "$UI_FILES" | xargs grep -n 'tabIndex\s*=\s*["{'"'"'][1-9]\|tabindex\s*=\s*"[1-9]' 2>/dev/null || true)
  if [ -n "$HITS" ]; then
    echo "FAIL [4.1.2] tabIndex > 0 disrupts natural tab order:"
    echo "$HITS" | sed 's/^/  /'
    FAIL_COUNT=$((FAIL_COUNT + 1))
  fi

  UI_COUNT=$(echo "$UI_FILES" | wc -l | tr -d ' ')
  ARIA_COUNT=$(echo "$UI_FILES" | xargs grep -l 'aria-' 2>/dev/null | wc -l | tr -d ' ')
  echo ""
  echo "Scanned $UI_COUNT UI files. $ARIA_COUNT use ARIA attributes."
  echo ""

  if [ "$FAIL_COUNT" -eq 0 ]; then
    echo "WCAG CHECK: PASS"
  else
    echo "WCAG CHECK: FAIL ($FAIL_COUNT criteria violated)"
  fi
} | tee "$OUTPUT"

[ "$FAIL_COUNT" -eq 0 ] || exit 1
