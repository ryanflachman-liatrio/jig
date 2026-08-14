#!/bin/bash
# Parse the test coverage profile and fail if total coverage is below threshold.
# COVERAGE_THRESHOLD env var overrides the default (70%).
# Writes a human-readable summary to stdout; the step captures it in .jig/coverage-output.txt.
set -e
mkdir -p .jig

THRESHOLD="${COVERAGE_THRESHOLD:-70}"

if [ -f go.mod ]; then
  if [ -f .jig/coverage.out ]; then
    COVER_OUTPUT=$(go tool cover -func .jig/coverage.out)
    echo "$COVER_OUTPUT"
    COVERAGE=$(echo "$COVER_OUTPUT" | grep '^total:' | awk '{print $3}' | tr -d '%')
    echo "Coverage total: ${COVERAGE}%"
    if awk "BEGIN { exit !(${COVERAGE:-0} < ${THRESHOLD}) }"; then
      echo "FAIL: ${COVERAGE}% is below the ${THRESHOLD}% threshold"
      exit 1
    else
      echo "PASS: ${COVERAGE}% meets the ${THRESHOLD}% threshold"
    fi
  else
    echo "SKIP: no Go coverage profile found — run-tests.sh may need -coverprofile flag"
  fi
elif [ -f package.json ]; then
  if [ -f coverage/coverage-summary.json ]; then
    COVERAGE=$(node -e "const s = require('./coverage/coverage-summary.json'); console.log(s.total.lines.pct);")
    echo "Coverage total: ${COVERAGE}%"
    if awk "BEGIN { exit !(${COVERAGE:-0} < ${THRESHOLD}) }"; then
      echo "FAIL: ${COVERAGE}% is below the ${THRESHOLD}% threshold"
      exit 1
    else
      echo "PASS: ${COVERAGE}% meets the ${THRESHOLD}% threshold"
    fi
  else
    echo "SKIP: no Jest coverage summary — run tests with --coverage flag"
  fi
elif [ -f pyproject.toml ] || [ -f setup.py ]; then
  if [ -f .coverage ]; then
    COVERAGE=$(python -m coverage report | tail -1 | awk '{print $NF}' | tr -d '%')
    echo "Coverage total: ${COVERAGE}%"
    if awk "BEGIN { exit !(${COVERAGE:-0} < ${THRESHOLD}) }"; then
      echo "FAIL: ${COVERAGE}% is below the ${THRESHOLD}% threshold"
      exit 1
    else
      echo "PASS: ${COVERAGE}% meets the ${THRESHOLD}% threshold"
    fi
  else
    echo "SKIP: no Python coverage data found — run pytest with --cov flag"
  fi
else
  echo "SKIP: no coverage data found for this project type"
fi
