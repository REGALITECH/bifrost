# Changelog

## Unreleased

- Reject removed `dry_run` settings and align schema validation with the environment-only API key requirement. Explicitly choose `enabled` when migrating legacy configurations.
- Enabled instances require an environment-backed API key and send token and Fish Audio usage.
- Send the authenticated virtual-key UUID as `customer_id` for LLM and Fish Audio usage. Register VK UUIDs as Metronome customer ingest aliases; no Bifrost customer mapping is needed.
- Reject legacy `customer_mapping` and `default_customer_id` settings. Register aliases on existing customers, remove those settings, and increment the plugin configuration version before restarting.

## 0.1.0

- Add an opt-in builtin Metronome exporter for LLM token usage and authenticated external Fish Audio usage.
- Support SecretVar credentials, stable external event IDs, and bounded ingestion retries.
