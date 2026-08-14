#!/bin/bash
# Stage all changes and commit using the message written by implement_code.
set -e
msg=$(cat .jig/commit-msg.txt 2>/dev/null || echo 'feat: implement SDD parent task')
git add -A
git commit -m "$msg"
echo "Committed: $(git log --oneline -1)"
