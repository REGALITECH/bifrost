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
| `main` (this repo) | REGALI's line: upstream code plus REGALI changes. From the v2 port on, it is based on upstream `transports/v2.2.4` (`ed8371a9`). |
| `release/v1.6` (this repo) | The v1 line, frozen at `ffd2f18c` (the last `main` before the v2 port). Only for v1 hotfixes, tagged `v1.6.11-regali.12` and up. |
| `dev` (upstream) | maximhq's default branch. We sync from its `transports/v*` release tags, not from the branch. |

`main` is protected by an active repository ruleset (`main`): pull request required (0 approvals),
no force push, no branch deletion, no bypass actors. Upstream syncs therefore also go through a PR.
This matches the rulesets on `etla_app`, `clomoni_api` and `aeon_app`.

## What diverges from upstream

As of 2026-10-26, relative to upstream `transports/v2.2.4` (`ed8371a9`, 2026-09-29): 70 upstream
files edited or deleted, 75 new files.

| Change | Paths |
| --- | --- |
| Fish Audio provider (speech + realtime WS) | `core/providers/fishaudio/`, `core/bifrost.go`, `core/utils.go`, `core/schemas/{bifrost.go,realtime.go,speech.go}`, `core/internal/llmtests/`, `core/go.{mod,sum}`, `framework/streaming/audio.go`, `plugins/logging/operations.go`, `transports/bifrost-http/handlers/{wsrealtime.go,realtime_turn_pipeline.go,realtimeheaders.go}`, `transports/bifrost-http/websocket/session.go`, `transports/config.schema.json`, `ui/lib/constants/{config.ts,icons.tsx,logs.ts}`, `docs/providers/supported-providers/fishaudio.mdx`, `docs/openapi/{openapi.json,schemas/inference/common.yaml}`, `docs/docs.json` |
| Realtime fixes shared by every provider (`#3`, `#4`, `#7`) | `core/schemas/realtime.go` (`output_audio_format`, the `extra_params` merge, and the fork-only optional interfaces `RealtimeBinaryProvider`, `RealtimeInputUsageProvider`, `RealtimeModelHeadersProvider`), `core/schemas/realtime_test.go`, `core/providers/openai/{realtime.go,realtime_test.go}` |
| Fish Audio catalog pricing derived from the datasheet's OpenRouter rows | `framework/modelcatalog/datasheet/derive.go`, `framework/modelcatalog/datasheet/derive_test.go`, `framework/modelcatalog/datasheet/testdata/pricing-openrouter-fishaudio.json`, `framework/modelcatalog/datasheet/sync.go` (one call site) |
| Usage endpoints `POST /v1/fishaudio/usage` and `POST /v1/audio/transcriptions/usage` | `transports/bifrost-http/handlers/{fishaudio_usage.go,transcription_usage.go,usagebilling.go}`, `transports/bifrost-http/server/server.go` (routes) |
| STT usage models registered through the `transcription` custom provider | `framework/configstore/transcriptionmodels.go`, `framework/modelcatalog/{main.go,models.go}`, `transports/bifrost-http/handlers/providers.go`, `transports/bifrost-http/server/server.go`, `docs/features/transcription-usage-models.mdx`, `docs/docs.json` |
| Metronome builtin exporter and external usage delivery | `plugins/metronome/`, `transports/bifrost-http/{handlers/fishaudio_usage.go,server/plugins.go,server/server.go,lib/config.go}`, `transports/go.mod`, `transports/config.schema.json`, `Makefile` (`test-metronome`), `docs/plugins/metronome.mdx`, `docs/docs.json`, `examples/configs/withmetronome/` |
| Metronome CI coverage and release smoke test | `.github/workflows/metronome-tests.yml`, `.github/workflows/scripts/test-metronome-image.sh`, `.github/dependabot.yml`, `.github/workflows/regali-docker-release.yml` |
| Fixed Fish Audio TTS model catalog and wildcard virtual-key coverage | `core/schemas/fishaudio.go`, `core/providers/fishaudio/`, `framework/modelcatalog/fishaudioallowlist_test.go`, `.github/workflows/fishaudio-tests.yml`, `tests/e2e/api/{collections/provider-harness.json,fixtures/fishaudio-models/config.json}` |
| Metronome speech and Fish Audio realtime usage | `plugins/metronome/`, `transports/bifrost-http/handlers/metronomerealtime_test.go`, `docs/plugins/metronome.mdx` |
| TypeSafe ordered decision schema, native body preservation, tenant/model-scoped idempotency, replay-safe Metronome billing and Jev 1.14.0 discovery/pricing | `core/{decisionemulation*,bifrost.go,schemas/decision*,providers/typesafe/,providers/utils/decision*}`, `core/schemas/{bifrost.go,chatcompletions.go}`, `core/internal/llmtests/decision.go`, `transports/bifrost-http/{handlers/inference.go,integrations/typesafe*}`, `plugins/{logging,metronome}/`, `framework/modelcatalog/datasheet/{cost.go,decision_cost_test.go}`, `docs/{integrations/typesafe-sdk.mdx,providers/supported-providers/typesafe.mdx}` |
| Decision pricing UI in v2's extracted pricing fields module | `ui/app/workspace/custom-pricing/overrides/{pricingFields.ts,pricingFieldSelector.tsx}`, `ui/lib/{types/config,constants/logs}.ts`, `tests/e2e/features/placeholders/placeholders.spec.ts`, `docs/providers/custom-pricing.mdx` |
| GHCR release workflow (new file) | `.github/workflows/regali-docker-release.yml` |
| PR tests start on pull requests to `main`; `run-tests.sh` follows the current test layout | `.github/workflows/pr-tests.yml`, `.github/workflows/scripts/run-tests.sh` |
| Workflow Lint classifies documentation/provider-fetched URLs, checks input changes, and tests that unknown destinations still fail | `.github/workflows/workflow-lint.yml`, `.github/workflows/scripts/check-egress-allowlist{,.test}.sh` |
| `FISH_AUDIO_API_KEY` plumbed into upstream test jobs | `.github/workflows/pr-tests.yml`, `.github/workflows/release-pipeline.yml` |
| CODEOWNERS deleted | `.github/CODEOWNERS` — it assigned every path to `@maximhq/bifrost-admin`, a team that does not exist in this org, so it was inert. No other REGALITECH repository uses CODEOWNERS. |
| Issue template config replaced | `.github/ISSUE_TEMPLATE/config.yml` — points upstream product reports at maximhq/bifrost |
| This file (new) | `REGALI.md` |

Keep this table current when the fork gains or drops a change; it is the checklist for resolving
conflicts during an upstream sync.

**Recheck when upstream changes `schemas.AllowedRequests`.**
`framework/configstore/transcriptionmodels.go` accepts the `transcription` provider only when its
`AllowedRequests` equals the zero struct. That is correct for the rows stored today, so a stricter
check was left out of the v2 port. If upstream adds a field that cannot be compared, the check stops
compiling; if an upstream default or migration sets a new field to `true` on stored providers, the
check rejects the provider and STT usage reports fail.

## Upstream sync

Direction decided 2026-08-20; updated 2026-09-30 for the v2 line.

**Track upstream `main` at release boundaries.** Sync from the `transports/v*` tags — the gateway
releases, which are also what this fork's own `v*-regali.*` tags are numbered after. Do not track
upstream `dev`: it took 640 commits in the 30 days to 2026-08-20 and carries unreleased work.

**Cadence: at least once a month, up to the newest `transports/v*` tag at that time.** Upstream cut
7 gateway releases between 2026-07-14 and 2026-08-15 — roughly one every 4–5 days — so following
literally every release is more work than it is worth. A month is the ceiling on drift, not a target:
the cost of a sync grows with the size of the gap, so a longer pause is not a saving.

**How.** Merge, not rebase, into `main` through a pull request. Rebasing rewrites the fork's commits
and forces the same conflicts to be resolved again on every replayed commit; a merge resolves them
once. The `main` ruleset requires the PR regardless.

```
git fetch upstream --tags
git switch -c sync/transports-vX.Y.Z main
git merge transports/vX.Y.Z
# resolve conflicts, then open a PR against main
```

**Merge the sync PR with *Create a merge commit* — never *Squash and merge* or *Rebase and merge*.**
A squash keeps the tree but drops the upstream parent, so `main` no longer contains upstream's history
and the next sync's merge-base falls back to the previous base. Simulated on the v2 line, the next sync
then conflicted in 318 files instead of 11. This applies to every PR that brings upstream history into
`main`: sync PRs and the port bridge below. Ordinary feature PRs may still be squashed; the repository
allows all three methods, so pick the method on each PR.

**Major versions: port by cherry-pick.** When one merge would bundle too much — upstream v2 reworked
the realtime, plugin and governance APIs the fork builds on — port the fork onto the new release
instead of merging it:

1. Branch `regali/vX.Y.Z-port` from upstream's `transports/vX.Y.Z`.
2. `git cherry-pick -x` the fork's commits from `main`, oldest first. Keep each subject with its
   `(#N)` and the "(cherry picked from commit …)" trailer. Adapt the code to the new API inside the
   commit that brings it in, amend, and describe the adaptation in a `vX port:` paragraph of that
   commit's message. Fold a commit into the one that needs it when it cannot build alone, and drop it
   when upstream made it unnecessary. Every commit must build.
3. Review the port in a draft PR against a branch at the upstream tag's commit (for example
   `upstream-transports-vX.Y.Z`; the fork carries no upstream tags), with the
   `git range-diff` of the old and new fork commits in its body. Close it; it is not merged.
4. Branch `release/vA.B` from the current `main`, for hotfixes on the old line.
5. Bridge into `main`: on top of the port, `git merge -s ours main` keeps the port's tree and records
   the old `main` as a parent. Merge that PR with *Create a merge commit*. The old SHAs stay
   reachable and the next sync's merge-base is the new upstream tag.

```
git fetch upstream --tags
git switch -c regali/vX.Y.Z-port transports/vX.Y.Z
git cherry-pick -x <fork commit>     # resolve, adapt, git commit --amend; repeat in order
git switch -c regali/vX.Y.Z-switch
git merge -s ours main
git diff --quiet regali/vX.Y.Z-port regali/vX.Y.Z-switch   # same tree as the port
# open a PR from regali/vX.Y.Z-switch into main
```

The v2 port (2026-09) did this on `transports/v2.2.4` (`ed8371a9`) with the fork's commits up to
`ffd2f18c`. One commit was dropped: the Snyk workflow deletion (the workflow is disabled in Actions
instead). Three were folded: `82660c2b` into the Fish Audio provider commit, `f9223746` into the GHCR
workflow commit, and `#9` into `#11`, which replaced it.

The switch branch also incorporates REGALI `main` through `b5d9582e` (2026-10-26),
including PRs #17–#21. TypeSafe's newer request/response schema and replay-safe billing
are retained alongside v2's access resolver, error classification, tracing and cost
breakdown APIs. Decision pricing fields live in v2's extracted `pricingFields.ts`.

**Where the conflicts will be.** Only in files this fork modified that upstream also touched. New
files never conflict. In the v2 port, the conflicted files that the fork still edits were
`core/go.{mod,sum}`, `core/schemas/{bifrost.go,realtime.go}`,
`transports/bifrost-http/handlers/{wsrealtime.go,realtime_turn_pipeline.go,providers.go}`,
`transports/bifrost-http/{lib/config.go,server/plugins.go}`, `transports/{config.schema.json,go.mod}`,
`framework/modelcatalog/{main.go,models.go}`, `ui/lib/constants/{config.ts,logs.ts}` and the
generated `docs/openapi/openapi.json`. The other conflicts were resolved by taking upstream, so those
files are no longer fork-edited: `core/internal/llmtests/realtime.go` (`#3`),
`core/encryptedreasoning_test.go` (`#7`),
`.github/workflows/scripts/{setup-go-workspace.sh,validate-schema-sync.sh}` and
`transports/Dockerfile.local` (`#12`), and the modify/delete on `.github/workflows/snyk.yml`.
Regenerate `go.sum` (`go mod tidy`) and `openapi.json` rather than merging them by hand;
`openapi.json` is bundled from `docs/openapi/**/*.yaml`, so edit the source too. In the v2 port,
`openapi.json` was edited by hand (`bundle.py` needs `pyyaml`, which was not available), with the
same one-line `fishaudio` enum entry added to `docs/openapi/schemas/inference/common.yaml`; re-bundle
it on the next sync.

The ~2,000 lines under `core/providers/fishaudio/` are new files and cost nothing to carry. Keep new
behaviour in new files and hold edits to upstream files down to the few registration lines they
genuinely need — that is what keeps a sync cheap.

**`plugins/metronome/go.mod` is ours to bump.** Upstream never edits it. On every sync, set its `go`
directive and `github.com/maximhq/bifrost/core` version to what upstream's plugins use, then run
`go mod tidy` in `plugins/metronome`. `TestFasthttpVersionIsConsistentAcrossModules` fails when its
fasthttp falls behind upstream's. No CI module list needs editing: since v2,
`transports/Dockerfile.local`, `setup-go-workspace.sh` and `validate-schema-sync.sh` find every
`plugins/*/go.mod` themselves.

**Verify after merging — no credentials required.** The check that matters is that the Fish Audio
provider still builds and its unit tests pass; `82660c2b` ("Adapt Fish Audio provider to current
upstream dev API") is an instance of upstream changing an API out from under it, and that class of
break shows up at compile time or in the transformation tests.

```
make setup-workspace
(cd core && go build ./... && go test ./providers/fishaudio/)
(cd core && go test ./providers/utils/ -run TestFasthttpVersionIsConsistentAcrossModules)
(cd framework && go test ./modelcatalog/ ./modelcatalog/datasheet/)
make test-metronome
```

Upstream reshapes the datasheet loader and the model catalog often; the derivation call site and
tests catch a break at compile or test time. `fishaudioallowlist_test.go` pins that a wildcard
virtual key accepts the fixed Fish Audio TTS models (REGALITECH/etla#256).

Provider tests that call a live API skip themselves when their key is absent, so this passes with no
API key set: the `realtime_test.go` unit tests run, `TestFishAudio` and `TestFishAudioIntegration`
skip, and the package reports `ok`. The same holds for the wider unit layer — upstream's
`test-core-unit` step deliberately excludes every package whose tests import `core/internal/llmtests`,
which is exactly the set that needs credentials.

Hitting the real Fish Audio API is a separate, occasional check, not part of a sync. It needs one
secret (`FISH_AUDIO_API_KEY`) and is worth doing before a release rather than on every merge.

**A sync is not finished in this repository.** Deployment is defined separately, in REGALI's private
infrastructure Terraform repository, which pins both the upstream Helm chart version and the container
image tag. Merging upstream here changes nothing that is running until those pins are raised there.
Treat the two as one unit of work: whoever performs the sync is responsible for the infrastructure
bump, or for handing it over explicitly.

## Fish Audio provider: propose upstream

Direction decided 2026-08-20: aim to contribute the Fish Audio provider to maximhq/bifrost.

If upstream accepts it, the provider stops being fork divergence altogether and upstream maintains
its compatibility — the class of work represented by `82660c2b` disappears, and the largest part of
this fork's diff goes away. Upstream's own guides for this: `docs/contributing/adding-a-provider.mdx`,
`docs/contributing/code-conventions.mdx`, `docs/contributing/raising-a-pr.mdx`.

The trade-off is that the implementation goes under public review and the timing depends on
upstream's judgement. Until it lands, the provider stays fork divergence and is synced like
everything else here.

## Releases

Push a tag matching `v*-regali.*`. `regali-docker-release.yml` builds `transports/Dockerfile.local`
for `linux/amd64` and `linux/arm64` and pushes `ghcr.io/regalitech/bifrost:<tag>`. It also runs on
`workflow_dispatch`, tagging `test-<sha7>`; use that for trial images. Do not push an `-rc` style tag
for a trial: it also matches `v*-regali.*` and is published like a release.

Name tags `v<upstream version>-regali.N`: the upstream `transports/v*` release the line is based on,
with N counting from 1 for each upstream base.

- The first v2 tag is `v2.2.4-regali.1`, on the merge commit that brings the port into `main`.
- A later fix on the same base is `v2.2.4-regali.2`; after the next sync, `v2.x.y-regali.1`.
- v1 hotfixes are tagged from `release/v1.6`, starting at `v1.6.11-regali.12`. The workflow is read
  from the tagged commit, so it works on both lines. A v1 image does not undo the v2 database
  migrations: going back to v1 needs a database snapshot taken before the v2 upgrade.

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
| `metronome-tests.yml` | pull requests to `main`, `workflow_dispatch` |
| `pr-tests.yml` | pull requests to `main` (the secret-bearing job waits for approval in the `pr-testing` environment), `workflow_dispatch` |
| `run-core-tests.yml` | `workflow_dispatch`, gated on write access |
| `workflow-lint.yml` | pull requests touching `.github/workflows/**`, the provider-harness collection, its seeded Python config, or non-test provider Go files |
| `fishaudio-tests.yml` | pull requests to `main` touching Fish Audio, provider utilities, schemas, model catalog or governance; `workflow_dispatch` |

The fork carries the Workflow Lint fix for upstream issue `maximhq/bifrost#7785`.
Documentation URLs and provider-side fetches are classified in `NOT_DIALLED_HOSTS`;
the runner's network allowlists and `egress-policy: block` are unchanged. On an upstream
sync, classify newly reported hosts by their actual use before adding exemptions or
network permissions. Remove the fork fix once upstream supplies an equivalent change.

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
| Snyk checks | needs `SNYK_TOKEN`, which the fork does not have, so it failed on every push. Disabled rather than deleted, like the rest, so `snyk.yml` stays identical to upstream. It runs on pushes to `main` and to any branch whose name contains `/` (such as `regali/v2.2.4-port`), so disable it before pushing the v2 line |
| CodSpeed Benchmarks | new in v2; runs on CodSpeed's `codspeed-macro` runners, which the fork does not have, so its jobs stay queued. Its `pull_request` trigger has no branch filter, so it first runs on the first pull request that carries the v2 tree (the port's review PR or the switch PR into `main`), before v2 is on `main`. Disable it, and cancel its queued runs, as soon as it appears in the list then |

No repository Actions secrets are configured. This does not break anything: tests that call a live
provider skip themselves when their key is absent, and the credential-free unit layer runs regardless.
It only means `pr-tests.yml` and `run-core-tests.yml` currently exercise nothing that needs a key.

If that changes, add only the keys this fork actually needs. Each provider's tests run only when its
own key is present, so the cost follows what you add — and it is not proportional to the number of
keys: upstream enables 3 scenarios for Fish Audio but 57 for OpenAI and 51 for Gemini, including
image generation. Use a credential issued for testing, never a production key.

## Repository settings

- **Visibility: public.** Inherited from the fork relationship and intentionally kept. GitHub cannot
  convert a public fork to private — doing so would require re-creating the repository from a bare
  clone. Assume anything committed here is world-readable.
- **Issues: enabled**, for REGALI's own tracking of fork work.
- **Wiki: enabled**, left at GitHub's default like the rest of the org. It is empty — a fork does
  not inherit the upstream wiki and none was ever created here.
- **Access: no repository-specific grants.** REGALITECH org members get `write` from the org default
  permission and owners get `admin`, the same as every other repository in the org. The org has no
  teams.
- Secret scanning and push protection are enabled; Dependabot security updates are disabled.

## Metronome verification

`make test-metronome` runs the exporter, HTTP handler, and builtin registration
tests with the race detector and no live credentials. The dedicated
`metronome-tests.yml` runs this on every PR to main, independently of the
secret-bearing test approval environment. An unrelated green workflow is not
evidence that these tests ran. The release job also boots each static image
with an environment-backed test key and verifies `metronome` is loaded without sending usage.

The module is fork-only: `transports/go.mod` uses a local replacement until a
separate module publication strategy is adopted. Keep that replacement when
syncing upstream (see *Upstream sync* for its `go.mod`). The builtin is listed in
`lib/config.go` (`builtinPluginNames`) and registered in `server/plugins.go` with
order 10, right after maxim's 9. `server/server.go` hands Metronome's config
normalization and redaction to the plugin, so the plugins API handles its
`api_key` before the plugin is loaded; the Fish Audio handler resolves the loaded
plugin from `lib.Config`. The schema test is
`transports/bifrost-http/lib/metronomeschema_test.go`, next to upstream's schema
test helpers.
