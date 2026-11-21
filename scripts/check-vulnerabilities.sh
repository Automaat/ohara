#!/usr/bin/env bash
# CI's known-vulnerability gates, with one allowlist for all of them.
#
# Usage:
#   scripts/check-vulnerabilities.sh go <module-dir>
#       govulncheck in its default mode: fails on every vulnerable function
#       the module's code calls, in a dependency or the standard library.
#   scripts/check-vulnerabilities.sh npm
#       npm audit of package-lock.json: fails on high and critical
#       advisories. Every package is a devDependency and the SPA bundles
#       some of them, so dev packages count too.
#   scripts/check-vulnerabilities.sh trivyignore <out-file>
#       turns the allowlist's `image` entries into the .trivyignore.yaml
#       that the image scan in ci.yml reads.
#
# The allowlist, .github/vulnerability-allowlist.json, has a list per gate
# (`go`, `npm`, `image`). Each entry names the advisory (`id`: a Go
# vulnerability ID like GO-2026-0001 for `go`, a GHSA ID for `npm`, a CVE
# or GHSA ID for `image`), why it is accepted (`reason`) and the day after
# which it no longer counts (`expires`, YYYY-MM-DD, UTC). A gate skips only
# the findings with an entry that has not expired.
#
# Renovate tracks the pinned govulncheck version below via a customManager
# (see renovate.json).
set -euo pipefail

# renovate: datasource=go depName=golang.org/x/vuln
GOVULNCHECK_VERSION="v1.10.0"

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
allowlist="${OHARA_VULN_ALLOWLIST:-$root/.github/vulnerability-allowlist.json}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
out="$tmp/out.json"

usage() {
    echo "Usage: $0 go <module-dir> | npm | trivyignore <out-file>" >&2
    exit 2
}

# The allowlist's unexpired entries for one gate, as a JSON array. Fails on
# a malformed allowlist and warns about expired entries.
accepted() {
    local gate="$1" today
    today="$(date -u +%Y-%m-%d)"
    if ! jq -e --arg gate "$gate" 'type == "object" and ((.[$gate] // []) | type == "array")' \
        "$allowlist" >/dev/null; then
        echo "error: $allowlist must be a JSON object whose \"$gate\" is a list" >&2
        return 1
    fi
    local invalid
    invalid="$(jq -r --arg gate "$gate" '
        (.[$gate] // [])[]
        | select(
            (.id | type) != "string" or .id == ""
            or (.reason | type) != "string" or (.reason | test("^\\s*$"))
            or (.expires | type) != "string"
            or (.expires | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}$") | not)
            or (try ((.expires + "T00:00:00Z") | fromdateiso8601 | strftime("%Y-%m-%d")) catch "") != .expires
          )
        | tojson' "$allowlist")"
    if [[ -n "$invalid" ]]; then
        echo "error: every $gate entry in $allowlist needs an id, a reason and expires (YYYY-MM-DD):" >&2
        printf '  %s\n' "$invalid" >&2
        return 1
    fi
    jq -r --arg gate "$gate" --arg today "$today" '
        (.[$gate] // [])[] | select(.expires < $today)
        | "::warning::allowlist entry \(.id) (\($gate)) expired on \(.expires); it no longer counts"' \
        "$allowlist" >&2
    jq -c --arg gate "$gate" --arg today "$today" \
        '[(.[$gate] // [])[] | select(.expires >= $today)]' "$allowlist"
}

# Fails the gate when a finding has no unexpired allowlist entry. $1 is the
# gate, $2 a JSON array of {id, text} findings.
report() {
    local gate="$1" findings="$2" allowed
    allowed="$(accepted "$gate")"
    jq -r --argjson allowed "$allowed" '
        ($allowed | map(.id)) as $ids
        | .[] | select(.id as $id | $ids | index($id))
        | "accepted: \(.id): \(.text)"' <<<"$findings"
    jq -r --argjson findings "$findings" '
        ($findings | map(.id)) as $found
        | .[] | select(.id as $id | $found | index($id) | not)
        | "note: allowlist entry \(.id) matches no finding; remove it once it is fixed"' \
        <<<"$allowed"
    local open
    open="$(jq -c --argjson allowed "$allowed" \
        '($allowed | map(.id)) as $ids | [.[] | select(.id as $id | $ids | index($id) | not)]' \
        <<<"$findings")"
    if [[ "$(jq length <<<"$open")" -eq 0 ]]; then
        echo "$gate: no known vulnerabilities outside the allowlist"
        return 0
    fi
    jq -r '.[] | "::error::\(.id): \(.text)"' <<<"$open"
    echo "$gate: $(jq length <<<"$open") known vulnerabilities; fix them or list them in $allowlist" >&2
    return 1
}

check_go() {
    local dir="$1"
    cd "$dir"
    # The standard library's findings depend on the Go that runs the check
    # and on the build configuration: CI's setup-go installs the go.mod
    # toolchain, the image's Go, and the image builds a Linux binary without
    # cgo, which takes other code paths (os/user, net) than a cgo build.
    # The tool itself is built for the host, so only its run gets them.
    GOBIN="$tmp" go install "golang.org/x/vuln/cmd/govulncheck@$GOVULNCHECK_VERSION"
    echo "$(go version), GOOS=linux CGO_ENABLED=0, govulncheck $GOVULNCHECK_VERSION"
    # JSON output exits 0 with findings and non-zero only on an error.
    GOOS=linux CGO_ENABLED=0 "$tmp/govulncheck" -format json ./... >"$out"
    local findings
    # Only a trace that starts at a function is reachable from our code;
    # without one the module is only required or the package only imported.
    findings="$(jq -s -c '
        (map(select(.osv) | .osv | {key: .id, value: (.summary // "")}) | from_entries) as $summary
        | [.[] | select(.finding) | .finding | select((.trace[0].function // "") != "")
            | {id: .osv,
               text: "\(.trace[0].module)@\(.trace[0].version // "unknown")"
                   + (if .fixed_version then " (fixed in \(.fixed_version))" else " (no fix yet)" end)
                   + ": \($summary[.osv] // "") https://pkg.go.dev/vuln/\(.osv)"}]
        | unique_by(.id)' "$out")"
    report go "$findings"
}

check_npm() {
    local status=0
    cd "$root"
    # npm audit exits 1 when it finds anything, so read its JSON instead.
    npm audit --json >"$out" || status=$?
    if ! jq -e 'type == "object" and has("vulnerabilities") and (has("error") | not)' "$out" >/dev/null; then
        echo "error: npm audit failed (exit $status):" >&2
        cat "$out" >&2
        return 1
    fi
    local findings
    findings="$(jq -c '
        [.vulnerabilities[].via[] | objects
            | select(.severity == "high" or .severity == "critical")
            | {id: (.url | sub(".*/"; "")),
               text: "\(.severity) in \(.name) \(.range): \(.title) \(.url)"}]
        | unique_by(.id)' "$out")"
    report npm "$findings"
}

write_trivyignore() {
    local file="$1" allowed
    allowed="$(accepted image)"
    # Trivy drops an entry from the start of its expired_at day, so give it
    # the day after: an entry counts through its expires day in every gate.
    jq -r '
        if length == 0 then "vulnerabilities: []"
        else "vulnerabilities:",
            (.[] | ((.expires + "T00:00:00Z") | fromdateiso8601 + 86400 | strftime("%Y-%m-%d")) as $until
                | "  - id: \(.id | tojson)\n    statement: \(.reason | tojson)\n    expired_at: \($until)")
        end' <<<"$allowed" >"$file"
    echo "image: $(jq length <<<"$allowed") accepted findings written to $file"
}

[[ $# -ge 1 ]] || usage
case "$1" in
go)
    [[ $# -eq 2 ]] || usage
    check_go "$2"
    ;;
npm)
    [[ $# -eq 1 ]] || usage
    check_npm
    ;;
trivyignore)
    [[ $# -eq 2 ]] || usage
    write_trivyignore "$2"
    ;;
*)
    usage
    ;;
esac
