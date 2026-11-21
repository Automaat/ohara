#!/usr/bin/env bash
# Fail unless CI, the image and local dev pin the same Go patch release:
# the `toolchain` line in backend-go/go.mod (CI's setup-go reads it), the
# golang tag of the Dockerfile's build stage and `go` in .mise.toml.
#
# Usage:
#   scripts/check-go-versions.sh
#
# Renovate's `go` group bumps all three in one PR; this check stops a PR
# that bumped only some of them, e.g. before Docker Hub has the new tag.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

gomod="$(awk '$1 == "toolchain" { sub(/^go/, "", $2); print $2 }' "$root/backend-go/go.mod")"
docker="$(sed -n 's/^FROM golang:\([^-@ ]*\).*/\1/p' "$root/Dockerfile")"
mise="$(sed -n 's/^go = "\([^"]*\)".*/\1/p' "$root/.mise.toml")"

echo "backend-go/go.mod toolchain: ${gomod:-missing}"
echo "Dockerfile golang tag:       ${docker:-missing}"
echo ".mise.toml go:               ${mise:-missing}"

if [[ -z "$gomod" || -z "$docker" || -z "$mise" ]]; then
    echo "error: a Go version pin is missing" >&2
    exit 1
fi
if [[ "$gomod" == *$'\n'* || "$docker" == *$'\n'* || "$mise" == *$'\n'* ]]; then
    echo "error: a file has more than one Go version pin" >&2
    exit 1
fi
if [[ "$docker" != "$gomod" || "$mise" != "$gomod" ]]; then
    echo "error: Go version pins differ; bump them together" >&2
    exit 1
fi
