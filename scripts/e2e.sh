#!/usr/bin/env bash
# The Playwright smoke suite (e2e/) against the production image with
# Postgres, the lookup stub and no provider keys (e2e/compose.yaml). The
# stack and its data are gone afterwards, whatever the outcome.
#
# Usage:
#   scripts/e2e.sh [IMAGE] [PLAYWRIGHT ARGS...]
#
# Without IMAGE the image is built from this checkout; with it (e.g. the one
# CI built) it is used as it is. Needs Docker and Playwright's Chromium
# (`npx playwright install chromium`). The app is published on
# 127.0.0.1:8100 (playwright.config.ts), clear of `mise run dev` on 8000.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

compose=(docker compose -p ohara-e2e -f e2e/compose.yaml)
up=(up --detach --wait --wait-timeout 120)
if [[ $# -gt 0 && $1 != -* ]]; then
    export OHARA_E2E_IMAGE="$1"
    shift
    up+=(--no-build)
else
    up+=(--build)
fi

# One run at a time owns the stack, from any checkout: creating a Docker
# network is atomic, and the daemon is what every run shares.
lock=ohara-e2e-lock
if ! docker network create --label "ohara.e2e.pid=$$" "$lock" >/dev/null 2>&1; then
    echo "scripts/e2e.sh: another smoke run holds the stack (Docker network $lock)." >&2
    echo "If none is running, free it with: docker network rm $lock" >&2
    exit 1
fi

cleanup() {
    local status=$1
    trap - EXIT INT TERM
    if [[ $status -ne 0 ]]; then
        "${compose[@]}" logs --no-color app app-discogs >&2 || true
    fi
    "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
    docker network rm "$lock" >/dev/null 2>&1 || true
    exit "$status"
}
trap 'cleanup "$?"' EXIT
trap 'cleanup 130' INT
trap 'cleanup 143' TERM

"${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
"${compose[@]}" "${up[@]}"
npx playwright test "$@"
