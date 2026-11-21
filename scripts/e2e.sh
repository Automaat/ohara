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

compose=(docker compose -f e2e/compose.yaml)
up=(up --detach --wait --wait-timeout 120)
if [[ $# -gt 0 && $1 != -* ]]; then
    export OHARA_E2E_IMAGE="$1"
    shift
    up+=(--no-build)
else
    up+=(--build)
fi

cleanup() {
    status=$?
    if [[ $status -ne 0 ]]; then
        "${compose[@]}" logs --no-color app >&2 || true
    fi
    "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
    exit "$status"
}
trap cleanup EXIT

"${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
"${compose[@]}" "${up[@]}"
npx playwright test "$@"
