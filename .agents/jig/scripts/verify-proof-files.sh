#!/bin/bash
# Check that at least one proof artifact markdown file exists under docs/specs.
# Writes a report to .jig/proof-check-output.txt for assemble_report to read.
set -e
mkdir -p .jig

found=$(find docs/specs -path '*proofs*' -name '*.md' 2>/dev/null | sort)
count=$(echo "$found" | grep -c . 2>/dev/null || echo 0)

{
  echo "Proof files found: $count"
  echo "$found"
  if [ "$count" -eq 0 ]; then
    echo "GATE C: FAIL — no proof artifact files found under docs/specs"
    exit 1
  fi
  echo "GATE C: PASS — $count proof file(s) present"
} > .jig/proof-check-output.txt

cat .jig/proof-check-output.txt
