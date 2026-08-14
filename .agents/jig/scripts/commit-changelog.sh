#!/bin/bash
# Commit the CHANGELOG.md entry written by gen_changelog.
# Reads the spec name from .jig/ to compose a meaningful commit message.
set -e

if ! git diff --name-only HEAD 2>/dev/null | grep -q CHANGELOG.md && \
   ! git ls-files --others --exclude-standard 2>/dev/null | grep -q CHANGELOG.md; then
  echo "SKIP: CHANGELOG.md has no changes to commit"
  exit 0
fi

# Try to extract spec name from recent feat/fix commit messages or .jig/spec-name.txt
SPEC_NAME=$(cat .jig/spec-name.txt 2>/dev/null || \
  git log --oneline -5 --format='%s' | grep -o 'docs/specs/[^/]*' | head -1 | sed 's|docs/specs/||' || \
  echo "implementation")

git add CHANGELOG.md

if git diff --cached --name-only | grep -q CHANGELOG.md; then
  git commit -m "docs: add changelog entry for ${SPEC_NAME}"
  echo "Changelog committed: $(git log --oneline -1)"
else
  echo "SKIP: CHANGELOG.md has no staged changes"
  exit 0
fi
