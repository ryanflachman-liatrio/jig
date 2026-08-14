#!/bin/bash
# Auto-detect and run the project's test suite. Writes output to .jig/test-output.txt.
# Exit code reflects the test result so the engine can see a failure.
set -e
mkdir -p .jig

if [ -f go.mod ]; then
  go test ./... > .jig/test-output.txt 2>&1
elif [ -f package.json ]; then
  npm test > .jig/test-output.txt 2>&1
elif [ -f pyproject.toml ] || [ -f setup.py ]; then
  python -m pytest -v > .jig/test-output.txt 2>&1
elif [ -f Cargo.toml ]; then
  cargo test > .jig/test-output.txt 2>&1
else
  echo "No recognized test runner — skipping" > .jig/test-output.txt
fi
