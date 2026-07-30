#!/usr/bin/env bash
set -euo pipefail
go test ./... | tee test-report.txt
