# CI details

Read before changing the vulnerability checks, their allowlist, the Go
version, the built-page CSP check or how a release is tagged.

Moved from `CLAUDE.md`, whose conventions still apply.

## Git and CI

- CI fails on known vulnerabilities (`scripts/check-vulnerabilities.sh`),
  on every push and PR and nightly: `govulncheck` for Go code the backend
  calls, under the go.mod toolchain; `npm audit` for high and critical
  advisories in any package; Trivy for fixable high and critical ones in
  the image's OS packages, before the push (the Dockerfile pins the runtime
  base by digest, so the push rebuild uses the scanned base). govulncheck
  runs with `GOOS=linux CGO_ENABLED=0`, like the image build. A finding
  is accepted only through `.github/vulnerability-allowlist.json` (`go`,
  `npm`, `image` lists of `id`, `reason` and `expires`, YYYY-MM-DD); an
  entry counts through its expiry day. Renovate's `osvVulnerabilityAlerts` opens
  fix PRs for vulnerable dependencies.
- One Go patch release everywhere: `toolchain` in `backend-go/go.mod`
  (CI's `setup-go` reads it), the Dockerfile's `golang` tag and `go` in
  `.mise.toml`. Renovate's `go` group bumps them in one PR, opened only
  once all three have the release (`minimumGroupSize: 3`), and CI's
  `scripts/check-go-versions.sh` fails when they differ; the `go`
  language line stays where it is.
- `npm run build` ends with `scripts/check-csp.mjs`, which reads every
  built page with an HTML parser (`parse5`, logic in
  `scripts/csp-problems.mjs`) and judges it as a browser would. Only a
  `content-security-policy` `<meta>` that is a child of `<head>` counts;
  one elsewhere is reported, and a page with none fails. A comma in one
  is reported too: Chromium and WebKit enforce each comma-separated
  policy, so the page must satisfy every part. A policy covers only the
  scripts after it: a script before any policy that restricts scripts
  fails, and every earlier policy must allow each inline script through a
  hash in its effective `script-src-elem` (falling back to `script-src`,
  then `default-src`). Directive names and keywords are case-insensitive,
  a repeated directive or one holding a non-ASCII character is ignored, as
  browsers ignore it, and no script directive may allow `'unsafe-inline'`
  or `'unsafe-eval'`. A script with a `src` (`href` in SVG), an empty one,
  an HTML `nomodule` classic one and a data block (a `type` that is not
  JavaScript, `module`, `importmap` or `speculationrules`) need no hash;
  an SVG script reads no `language` or `nomodule`. Fixtures for these cases live in
  `test/check-csp/`, run by `npm run test:coverage`.

## Release tags

`workflow_dispatch` runs the copy of `release.yml` on the ref it is
dispatched on, so a branch cut before a change to the workflow keeps the
old copy and its old checks. Repository settings, not the file, decide
who may tag a release:

- The tag ruleset "Tags: release.yml on main only" covers every tag
  (`~ALL`), not only `v*`: an old copy checks its version input with
  `grep`, one line at a time, so a version with a newline passes and can
  turn the tag into `v/anything`, which a `v*` pattern does not match.
  `release.yml` on `main` now matches the whole string. The ruleset
  refuses creating, moving or deleting a tag to everyone, the owner and
  `GITHUB_TOKEN` included. Its one bypass is "deploy keys".
- The repository's one write deploy key, "release.yml tag push
  (environment release)", has its private key in the `RELEASE_DEPLOY_KEY`
  secret of the `release` environment. The environment admits only the
  `main` branch, and administrators cannot bypass that.
- The `release` job runs in that environment and pushes the tag over SSH
  with the key, checking GitHub's host keys against `gh api meta`. It
  then creates the release with `--verify-tag`, so the release never
  creates a tag itself.

A copy on another branch therefore cannot tag, and with no tag it cannot
cut a release: creating a release for a missing tag creates the tag,
which the ruleset refuses too. Every copy older than the main-only check
pushes its tag before it builds the image, so it fails before it pushes
anything to `ghcr.io`.

What the settings do not cover:

- A release for a tag that already exists changes no ref, so any token
  with `contents: write` can create one, from any branch. Only `main`
  creates tags, so such a release can only name a commit a `main` run
  tagged; an old copy never gets there, because its own tag push fails
  first.
- A draft release changes no ref either, so any token with
  `contents: write` can save a draft for a tag that does not exist. It
  creates no tag and stays private to writers, and publishing it is
  refused: publishing creates the tag, which the ruleset refuses.
- Every job on `main` that names `environment: release` gets the key,
  whatever its workflow file. Add the environment to no other job.
- Every write deploy key bypasses the ruleset, so add no other deploy key
  with write access.

If a run pushes its tag and then fails to create the release, a rerun of
that version stops at the existing tag, and no one but the deploy key can
delete the tag. Check that the tag names the intended `main` commit
(`git ls-remote origin refs/tags/vX.Y.Z` against the run's commit), then
create the release by hand:
`gh release create vX.Y.Z --verify-tag --title vX.Y.Z --generate-notes`.
To drop the tag instead, temporarily disable the ruleset in the
repository settings, delete the tag and enable the ruleset again.

To rotate the key: create an ed25519 key pair, add the public key as a
write deploy key, set the private key as `RELEASE_DEPLOY_KEY` in the
`release` environment (`gh secret set RELEASE_DEPLOY_KEY --env release`),
then delete the old deploy key and the local private key.
