#!/bin/bash
# Run the project's dependency vulnerability scanner; writes to .jig/dep-audit.txt.
# Non-zero exit on vulnerabilities is expected — caller uses on_failure = "continue".
set -e
mkdir -p .jig

if [ -f go.mod ]; then
  echo "=== Go: govulncheck ===" > .jig/dep-audit.txt
  if command -v govulncheck >/dev/null 2>&1; then
    govulncheck ./... >> .jig/dep-audit.txt 2>&1 || true
  else
    echo "govulncheck not installed — skipping. Install: go install golang.org/x/vuln/cmd/govulncheck@latest" >> .jig/dep-audit.txt
  fi
  echo "" >> .jig/dep-audit.txt
  echo "=== Go: go mod verify ===" >> .jig/dep-audit.txt
  go mod verify >> .jig/dep-audit.txt 2>&1 || true
elif [ -f package.json ]; then
  echo "=== npm audit ===" > .jig/dep-audit.txt
  npm audit --audit-level=moderate >> .jig/dep-audit.txt 2>&1 || true
elif [ -f pyproject.toml ] || [ -f requirements.txt ]; then
  echo "=== Python: pip-audit ===" > .jig/dep-audit.txt
  if command -v pip-audit >/dev/null 2>&1; then
    pip-audit >> .jig/dep-audit.txt 2>&1 || true
  elif command -v safety >/dev/null 2>&1; then
    safety check >> .jig/dep-audit.txt 2>&1 || true
  else
    echo "pip-audit not installed — skipping. Install: pip install pip-audit" >> .jig/dep-audit.txt
  fi
elif [ -f Cargo.toml ]; then
  echo "=== Rust: cargo audit ===" > .jig/dep-audit.txt
  if command -v cargo-audit >/dev/null 2>&1; then
    cargo audit >> .jig/dep-audit.txt 2>&1 || true
  else
    echo "cargo-audit not installed — skipping. Install: cargo install cargo-audit" >> .jig/dep-audit.txt
  fi
else
  echo "No recognized package manager — skipping dependency audit." > .jig/dep-audit.txt
fi

cat .jig/dep-audit.txt
