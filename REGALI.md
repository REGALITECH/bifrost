# REGALI fork of Bifrost

This repository is REGALITECH's fork of [maximhq/bifrost](https://github.com/maximhq/bifrost).
Only the operational rules that differ from upstream live here; everything else follows upstream's
own docs (`README.md`, `AGENTS.md`).

**Guiding rule: keep the tree as close to upstream as possible.** Every upstream-tracked file we edit
or delete becomes a merge conflict on every future sync. Where a setting can achieve the same result
(disabling a workflow, toggling a repository option), prefer the setting over a code change.

## Branches

| Branch | Role |
| --- | --- |
| `main` (this repo) | REGALI's line: upstream code plus REGALI changes. |
| `dev` (upstream) | maximhq's default branch — the ref we sync from. |

`main` is protected by an active repository ruleset (`main`): pull request required (0 approvals),
no force push, no branch deletion, no bypass actors. Upstream syncs therefore also go through a PR.
This matches the rulesets on `etla_app`, `clomoni_api` and `aeon_app`.

## What diverges from upstream

As of 2026-08-20, relative to upstream commit `0a8a38c9` (2026-08-15):

| Change | Paths |
| --- | --- |
| Fish Audio provider (speech + realtime WS) | `core/providers/fishaudio/`, `core/schemas/`, `core/internal/llmtests/`, `framework/streaming/audio.go`, `plugins/logging/operations.go`, `transports/bifrost-http/`, `transports/config.schema.json`, `ui/lib/constants/`, `docs/providers/supported-providers/fishaudio.mdx` |
| GHCR release workflow (new file) | `.github/workflows/regali-docker-release.yml` |
| `FISH_AUDIO_API_KEY` plumbed into upstream test jobs | `.github/workflows/pr-tests.yml`, `.github/workflows/release-pipeline.yml` |
| Snyk workflow deleted (not usable in a fork) | `.github/workflows/snyk.yml` |
| CODEOWNERS deleted | `.github/CODEOWNERS` — it assigned every path to `@maximhq/bifrost-admin`, a team that does not exist in this org, so it was inert. No other REGALITECH repository uses CODEOWNERS. |
| Issue template config replaced | `.github/ISSUE_TEMPLATE/config.yml` — points upstream product reports at maximhq/bifrost |
| This file (new) | `REGALI.md` |

Keep this table current when the fork gains or drops a change; it is the checklist for resolving
conflicts during an upstream sync.

## Releases

Push a tag matching `v*-regali.*` (for example `v1.6.11-regali.1`). `regali-docker-release.yml`
builds `transports/Dockerfile.local` for `linux/amd64` and `linux/arm64` and pushes
`ghcr.io/regalitech/bifrost:<tag>`. It also runs on `workflow_dispatch`, tagging `test-<sha7>`.

Note that other REGALITECH services publish to ECR through the reusable workflows in
[`gha-shared`](https://github.com/REGALITECH/gha-shared). This repository uses GHCR with the built-in
`GITHUB_TOKEN` instead, because the fork is public and needs no extra credentials. Revisit that choice
if the image has to be deployed through the same ECR/ArgoCD path as the other services.

## GitHub Actions

Upstream ships release and publish workflows that fire on pushes to `main`. In a fork they either fail
or attempt maxim's releases, so they are **disabled in repository settings rather than deleted** —
disabling leaves the tree byte-identical to upstream, so syncs stay conflict-free. Re-enable any of
them from Settings → Actions → the workflow → *Enable workflow*.

Active:

| Workflow | Trigger |
| --- | --- |
| `regali-docker-release.yml` | tag `v*-regali.*`, `workflow_dispatch` |
| `run-core-tests.yml` | `workflow_dispatch`, gated on write access |
| `pr-tests.yml` | `workflow_dispatch` |
| `workflow-lint.yml` | pull requests touching `.github/workflows/**` |

Disabled:

| Workflow | Why |
| --- | --- |
| Release Pipeline, Release CLI, Release Migration CLI | upstream release automation, fires on push to `main` |
| NPX Package Publish | publishes maxim's npm packages |
| Release Helm Chart | publishes maxim's Helm chart |
| OpenAPI Bundle | commits generated output back to `main` |
| PR Test Notifier | maxim-specific, failing in the fork |
| Docs Validation | validates maxim's docs site, failing in the fork |
| Scorecard | OpenSSF scorecard for the upstream OSS project |
| Dependabot Alerts to Issues | opens an issue every Monday |
| E2E Tests | only triggers on a stale upstream branch name |
| Dependency Review | every upstream sync PR carries a large dependency diff |

No repository Actions secrets are configured, so provider-credential tests in `pr-tests.yml` and
`run-core-tests.yml` cannot pass as-is. Add the secrets this fork actually needs (for example
`FISH_AUDIO_API_KEY`) before relying on those jobs.

## Repository settings

- **Visibility: public.** Inherited from the fork relationship and intentionally kept. GitHub cannot
  convert a public fork to private — doing so would require re-creating the repository from a bare
  clone. Assume anything committed here is world-readable.
- **Issues: enabled**, for REGALI's own tracking of fork work.
- **Wiki: disabled.** A fork does not inherit the upstream wiki and none was ever created here.
- **Access: no repository-specific grants.** REGALITECH org members get `write` from the org default
  permission and owners get `admin`, the same as every other repository in the org. The org has no
  teams.
- Secret scanning and push protection are enabled; Dependabot security updates are disabled.
