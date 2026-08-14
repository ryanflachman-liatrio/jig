#!/bin/bash
# Auto-detect and run the project's lint and format checks. Writes output to .jig/lint-output.txt.
# Exit code reflects the lint result so the engine can see a failure.
set -e
mkdir -p .jig

if [ -f go.mod ]; then
  { go vet ./... && gofmt -l .; } > .jig/lint-output.txt 2>&1
elif [ -f package.json ] && command -v eslint >/dev/null 2>&1; then
  eslint . > .jig/lint-output.txt 2>&1
elif [ -f pyproject.toml ] && command -v ruff >/dev/null 2>&1; then
  ruff check . > .jig/lint-output.txt 2>&1
elif [ -f .pre-commit-config.yaml ] && command -v pre-commit >/dev/null 2>&1; then
  pre-commit run --all-files > .jig/lint-output.txt 2>&1
else
  echo "No recognized linter — skipping" > .jig/lint-output.txt
fi
