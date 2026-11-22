#!/usr/bin/env bash
# Fail when two migrations share a version: the NNNN of
# backend-go/internal/db/migrations/NNNN_name.sql. The app refuses to start
# with such a pair (ErrDuplicateVersion in backend-go/internal/db).
#
# Usage:
#   scripts/check-migrations.sh                              # the working tree
#   scripts/check-migrations.sh [--since SINCE] BASE [HEAD]  # also against BASE
#
# HEAD is a Git revision to read the migrations from instead of the working
# tree. With BASE (a revision such as origin/main), a migration HEAD adds
# must not take a version BASE already uses for another file. HEAD adds what
# it has and SINCE (default BASE) lacks: a PR stacked on another passes its
# base branch as SINCE, so the parent's migrations stay the parent's to fix.
# Two PRs can each add a 0018_*.sql under another name: both merge cleanly
# and each passes CI against the main it started from, so only a check
# against the current main catches the second one.
set -euo pipefail

dir=backend-go/internal/db/migrations
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

usage() {
    echo "usage: scripts/check-migrations.sh [[--since SINCE] BASE [HEAD]]" >&2
    exit 2
}
since=""
if [[ "${1:-}" == --since ]]; then
    [[ $# -ge 3 && -n "$2" ]] || usage
    since="$2"
    shift 2
fi
if [[ $# -gt 2 || "${1:-}" == -* ]]; then
    usage
fi
base="${1:-}"
head="${2:-}"
for rev in "$since" "$base" "$head"; do
    if [[ -n "$rev" ]] && ! git rev-parse --verify --quiet "$rev^{commit}" >/dev/null; then
        echo "error: $rev is not a Git revision" >&2
        exit 2
    fi
done

# migrations REV prints the .sql file names in $dir at REV, or in the working
# tree when REV is empty, sorted bytewise for comm.
migrations() {
    if [[ -z "$1" ]]; then
        local path
        for path in "$dir"/*.sql; do
            if [[ -f "$path" ]]; then
                printf '%s\n' "${path##*/}"
            fi
        done
    else
        git ls-tree --name-only "$1" -- "$dir/" | sed -n 's|.*/||; /\.sql$/p'
    fi | LC_ALL=C sort
}

# version NAME prints the part of NAME before its first underscore.
version() {
    printf '%s\n' "${1%%_*}"
}

status=0
fail() {
    if [[ "${GITHUB_ACTIONS:-}" == true ]]; then
        echo "::error file=$dir/$1::$2"
    else
        echo "error: $2" >&2
    fi
    status=1
}

head_label="${head:-the working tree}"
head_names="$(migrations "$head")"

seen=""
while IFS= read -r name; do
    [[ -n "$name" ]] || continue
    v="$(version "$name")"
    other="$(printf '%s\n' "$seen" | awk -v v="$v" 'index($0, v "_") == 1 { print; exit }')"
    if [[ -n "$other" ]]; then
        fail "$name" "$other and $name in $head_label share version $v"
    fi
    seen+="$name"$'\n'
done <<<"$head_names"

if [[ -n "$base" ]]; then
    base_names="$(migrations "$base")"
    highest="$(printf '%s\n' "$base_names" | sed -n 's/_.*//p' | LC_ALL=C sort | tail -n 1)"
    while IFS= read -r name; do
        [[ -n "$name" ]] || continue
        v="$(version "$name")"
        taken="$(printf '%s\n' "$base_names" | awk -v v="$v" 'index($0, v "_") == 1 { print; exit }')"
        if [[ -n "$taken" ]]; then
            fail "$name" "$name takes version $v, which $base already uses for $taken; renumber it above $highest"
        fi
    done < <(LC_ALL=C comm -23 <(printf '%s\n' "$head_names") <(migrations "${since:-$base}") |
        LC_ALL=C comm -23 - <(printf '%s\n' "$base_names"))
fi

if [[ "$status" -eq 0 ]]; then
    echo "Migration versions are unique${base:+ against $base}."
fi
exit "$status"
