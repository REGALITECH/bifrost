# Metronome sandbox exporter

Native Go plugin. Sends successful Chat
Completions, Text Completions and Responses usage (including completed streams)
to `https://api.metronome.com/v1/ingest` as `token-billing` events.
Also forwards authenticated external Fish Audio reports through the existing
`HTTPTransportPostHook` as `fishaudio-usage` events; no core or loader changes.

## Build and load

From the Bifrost repository root, use the same Go toolchain, source revision,
workspace, dependencies and build flags for the gateway and plugin. Go native
plugins cannot be loaded into arbitrary prebuilt Bifrost images.

```sh
make setup-workspace
make build LOCAL=1
make build-metronome
make test-metronome
make test-metronome-integration
```

Merge the `plugins` entry from `config.example.json` into your existing Bifrost
configuration. Replace `path` with the absolute path to `build/metronome.so`.
For Docker, build both binaries for Linux inside the same build environment and
mount the plugin at the configured container path. Restart Bifrost after loading
the configuration; no existing process is reconfigured by this source change.

The default `dry_run: true` writes `[metronome] dry_run` events to the gateway
process logs and needs no API key. Set a customer mapping first. A made-up
customer ID is fine for dry run only.

## Customer identity

`customer_mapping` maps **Bifrost governance virtual-key UUIDs** to Metronome
customer IDs or ingest aliases. Use the UUID in the virtual-key management view,
not the secret `sk-bf-...` value passed in `x-bf-vk`. The ID is read from the
authenticated governance context; arbitrary caller-supplied tenant headers are
not trusted as billing identities. Unmapped token requests are skipped with a warning;
external audio reports fail visibly instead.

For one local sandbox customer, `default_customer_id` can route otherwise
unmapped token usage to that customer. External audio requires an explicit mapping. Keep it empty when exercising tenant isolation.
Multiple virtual-key UUIDs may map to the same Metronome customer.

## Enable sandbox delivery later

1. Register at https://signup.metronome.com/ and complete the emailed login flow.
2. In the sandbox, create a customer and note its ID (or configure an ingest alias).
3. Create a rate card and customer contract. For a manual first experiment, make
   four SUM billable metrics filtered by `event_type = token-billing`, measuring
   `input_tokens`, `output_tokens`, `cached_input_tokens`, `cached_write_tokens`.
   Add `model` and `provider` as group keys before saving the metrics. Create
   usage products and rates using those keys, and attach the rate card to the
   customer contract. Event ingestion alone does not create pricing or contracts.
4. Update `customer_mapping` in the plugin configuration.
5. Put a **sandbox** API key in the Bifrost process environment as
   `METRONOME_API_KEY` (or the variable named by `api_key_env`). For Docker, pass
   the environment variable into the container. Do not put secrets in config JSON.
6. Set `dry_run: false` and restart. Missing credentials cause plugin initialization
   to fail visibly; the gateway's existing plugin loader reports that status.
7. When ready for a later integration check, make a normal gateway request using
   the mapped virtual key, then inspect Metronome Events and the draft invoice.

The API hostname is shared between sandbox and production; the API key determines
the instance. This plugin cannot infer the instance from an opaque API key.

## Model mapping

Rates and events must use the same model/provider strings. The default adds the
provider prefix to unqualified models, e.g. `openai` + `gpt-example` becomes
`openai/gpt-example`. It does not fetch a price catalog. For Vertex, Bedrock,
Azure, aliases or self-hosted providers, set explicit mappings to your rate card:

```json
{
  "model_mapping": { "vertex/example-model": "google/example-model" },
  "provider_mapping": { "vertex": "YOUR_RATE_CARD_PROVIDER_VALUE" }
}
```

Model aliases and server-side fallback metadata are resolved before mapping.
Bifrost's input count includes cached tokens; the exporter subtracts cache reads
and writes from ordinary input tokens and emits them as separate properties.
Prompt, response text, raw virtual keys and provider/API secrets are never part
of an event. Dry-run logs include the customer ID and usage metadata.

## Prototype boundaries

- No pricing changes, contract/customer provisioning, Stripe payment setup or
  database migration is performed by the plugin.
- Direct Fish TTS, STT, WebSocket Realtime, embeddings, images,
  failed/cancelled requests and semantic-cache hits are excluded in this first
  LLM prototype. Advanced token categories (audio/image/cache TTL/tier) are not
  separately priced by this four-counter event format.
- Only completed streams with usage on the final hook are exported. Missing or
  inconsistent usage is not estimated. Cancellation billing and provider-specific
  final-usage behavior need integration checks before production use.
- For token events, a bounded in-memory queue keeps HTTP calls out of the hook. Up to three attempts
  reuse the identical payload and transaction ID for network errors, 429 and 5xx.
  Other HTTP errors are logged without retry. A fresh gateway request gets a new
  event ID, even if it is an application-level retry.
- For token events there is no durable outbox: queue overflow, retry exhaustion, process failure
  or shutdown exceeding five seconds can lose events. This is a sandbox prototype,
  not a production billing ledger. Existing Bifrost governance remains active.

API reference: https://docs.metronome.com/api-reference/usage/ingest-events

Tests cover token compatibility, audio conversion, HTTP retries against a local
server, handler rejection/acknowledgment, and native plugin loading in dry run.
Live Sandbox ingestion is a separate validation step.

## External Fish Audio usage prototype

The transport resolves only the configured `metronome` plugin for each report.
After input validation, governance authentication and the existing accounting /
logging hooks, it invokes `HTTPTransportPostHook` with the normalized usage body.
It does not run other HTTP/LLM plugins or make a provider call. No new core types,
provider methods, plugin-loader support, or database migrations are needed.

### Request and acknowledgment

`POST /v1/fishaudio/usage` now **requires `x-request-id`** for all callers. With
Metronome loaded, `occurred_at` is also required (RFC3339, actual occurrence time,
retained across retries). It is optional for legacy logging-only reports. The
body is otherwise unchanged; unknown fields are still rejected.

```json
{
  "billable_bytes": 54,
  "audio_ms": 2500,
  "outcome": "completed",
  "turn_id": "turn-1",
  "sub_id": "sub-1",
  "model": "s2-pro",
  "occurred_at": "2026-09-15T06:00:00Z"
}
```

Unlike queued token events, audio reports wait for Metronome's HTTP 200 before
returning 202. Up to three identical requests are attempted for network errors,
429 and 5xx, with a 12-second overall deadline and 3-second per-attempt timeout.
An ingest error, missing customer mapping, shutdown, or an old plugin without
external-usage support returns 503. The caller must retain and retry its event.
There is no server-side background retry after a failed external report.

A successful response includes `metronome_status: "sent"` and `transaction_id`.
Dry run returns `metronome_status: "dry_run"`; it does not contact Metronome.
When the plugin is absent or disabled the endpoint retains its logging-only
behavior and omits these fields. **202 without `metronome_status: "sent"` does
not confirm Metronome delivery.** Ingest acceptance also does not prove that a
customer, billable metric, or invoice matched; verify those in Sandbox.

The Metronome plugin requires an explicit `customer_mapping` entry keyed by the
authenticated governance VK UUID, including for audio dry run. Audio never uses
`default_customer_id`. Do not use a caller header, secret VK value, or submitted
customer field as the billing identity. For the existing local stub, the mapping
key is `d9092762-f3ba-4109-af19-f76fb5ff0e41`; choose an actual Sandbox customer
as its value. This example does not change any live mapping or credentials.

### Event and pricing

`fishaudio-usage` properties contain numeric `billable_bytes` (UTF-8 input bytes,
not characters or audio file size), numeric `audio_ms`, string `outcome`,
`provider`, `model`, `turn_id`, `sub_id`, `usage_source: "external"`, and
`schema_version: 1`. Model/provider mappings also apply to these events. No text,
audio bytes, secret headers, or arbitrary caller dimensions are forwarded.

The plugin preserves all four outcomes (`completed`, `barged_in`, `failed`,
`cache_hit`) and zero quantities. It does not infer free usage from an outcome or
estimate missing quantities. A failed or interrupted generation may still have
billable bytes. For cached playback without new generation, submit zero bytes.
Define whether `audio_ms` measures generated or actually played audio in the
reporting application; the exporter preserves the reported measurement.

Create a SUM metric for `billable_bytes`, filtered by `fishaudio-usage`. Initially
keep `audio_ms` as an observation unless the commercial model explicitly charges
for duration too. Define provider/model group keys and the rate card / customer
contract before sending test events. This plugin does not create them.

### Retry guarantees and prototype limits

The transaction ID is SHA-256 of the JSON array
`["bifrost", "fishaudio-usage", authenticated_vk_uuid, x_request_id]`, prefixed
with `bf-fishaudio-`. It stays stable across identical client retries and process
restarts; the caller-supplied timestamp is normalized to UTC, never regenerated.
Metronome deduplicates the transaction ID within its 34-day window:
https://docs.metronome.com/api-reference/idempotency

- Save the event **before** sending and resend exactly the same file. Do not
  rotate the VK UUID or change its customer/model mapping while replaying events.
- Reusing an ID with changed quantities is not a correction: this prototype has
  no durable receipt ledger or payload-conflict detection. Metronome can ignore
  the changed event. Do not use this interface for adjustments.
- Keep historical timestamps intact. Metronome accepts up to 34 days of backfill;
  older usage needs a separate operational process.
- The local logging/governance hooks finish before delivery. A 503 can therefore
  coexist with a usage log / budget update. Logging is asynchronous, so 202 is
  not a durable-log acknowledgment. Governance's approximately 5-minute in-memory
  deduplication does not guarantee unique local budget updates after restart or
  across Pods. The stable ID protects Metronome metering, not those local counters.
- Outbox persistence, durable local accounting deduplication, conflict detection,
  and billing reconciliation remain follow-up work for production use.

### Local test and Sandbox verification

From the repository root, first build the gateway/plugin together (see above),
configure the explicit Sandbox mapping, and restart with the Sandbox key in the
process environment. `transports/Dockerfile.metronome` builds both Linux binaries
in one environment; loading a rebuilt .so into an older image is not supported.

Prepare a fresh synthetic event (no Fish Audio API call or audio generation):

```sh
python3 plugins/metronome/examples/fishaudio.py prepare --event /tmp/fish-event.json
python3 plugins/metronome/examples/fishaudio.py send --event /tmp/fish-event.json
# Repeat the SAME send command to check deduplication.
```

The sender prompts for a VK without echoing it, or reads `BIFROST_VIRTUAL_KEY`.
Its default gateway is `http://127.0.0.1:18081`; override with `--base-url`.
The existing external stub must add `occurred_at` to its saved payload before it
can use a Metronome-enabled gateway. Its synthetic quantities are not a Fish
Audio invoice or proof of real generation.

After `metronome_status: "sent"`, query `/v1/events/search` with
`{"transactionIds": ["<returned transaction_id>"]}` using the Sandbox key.
Check `matched_customer`, `matched_billable_metrics`, and `is_duplicate`, then
verify metric/invoice quantities: first send = 54 bytes / 2500 ms; replay must not
increase billable quantity. Also test unmapped VK, inactive VK, interrupted
outcomes, and delivery failure. Search API reference:
https://docs.metronome.com/api-reference/usage/search-events
