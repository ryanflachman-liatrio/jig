#!/bin/sh
# Deliberately send only these three files to the image builder.
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
context=$(mktemp -d "${TMPDIR:-/tmp}/jig-d05-image.XXXXXX")
trap 'rm -rf "$context"' EXIT HUP INT TERM
cp "$repo/go.mod" "$repo/go.sum" "$context/"
cp "$repo/testdata/docker/d05/Dockerfile" "$context/Dockerfile"
docker build --pull=false --tag jig-d05-go:local "$context"
