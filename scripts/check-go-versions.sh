#!/usr/bin/env bash
# Fail unless CI, the image and local dev pin the same Go patch release:
# the `toolchain` line in backend-go/go.mod (CI's setup-go reads it), the
# golang tag of the Dockerfile's build stage and `go` in the [tools] table
# of .mise.toml.
#
# Usage:
#   scripts/check-go-versions.sh
#
# Renovate's `go` group bumps all three in one PR; this check stops a PR
# that bumped only some of them, e.g. before Docker Hub has the new tag.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# An indented `toolchain` line is valid Go, but setup-go ignores it and CI
# falls back to the `go` line, so only a line-leading directive counts.
gomod="$(tr -d '\r' <"$root/backend-go/go.mod" |
    sed -n 's/^toolchain go\([^[:space:]]*\).*/\1/p')"
docker="$(tr -d '\r' <"$root/Dockerfile" |
    sed -n 's/^FROM golang:\([^-@[:space:]]*\).*/\1/p')"
mise="$(tr -d '\r' <"$root/.mise.toml" | awk '
    /^[[:space:]]*\[/ { tools = ($0 ~ /^[[:space:]]*\[tools\][[:space:]]*(#.*)?$/); next }
    tools && /^[[:space:]]*go[[:space:]]*=/ {
        if (match($0, /"[^"]*"/)) print substr($0, RSTART + 1, RLENGTH - 2)
    }
')"

echo "backend-go/go.mod toolchain: ${gomod:-missing}"
echo "Dockerfile golang tag:       ${docker:-missing}"
echo ".mise.toml [tools] go:       ${mise:-missing}"

status=0
for pin in "backend-go/go.mod toolchain:$gomod" "Dockerfile golang tag:$docker" ".mise.toml [tools] go:$mise"; do
    name="${pin%%:*}"
    value="${pin#*:}"
    if [[ -z "$value" ]]; then
        echo "error: $name is missing" >&2
        status=1
    elif [[ "$value" == *$'\n'* ]]; then
        echo "error: $name is pinned more than once" >&2
        status=1
    elif [[ ! "$value" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        echo "error: $name must be an exact patch release like 1.27.4, got $value" >&2
        status=1
    fi
done
if [[ "$status" -ne 0 ]]; then
    exit "$status"
fi
if [[ "$docker" != "$gomod" || "$mise" != "$gomod" ]]; then
    echo "error: Go version pins differ; bump them together" >&2
    exit 1
fi
