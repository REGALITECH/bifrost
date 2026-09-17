# Changelog

## Unreleased

- Send the authenticated virtual-key UUID as `customer_id` for LLM and Fish Audio usage. Register VK UUIDs as Metronome customer ingest aliases; no Bifrost customer mapping is needed.
- Reject legacy `customer_mapping` and `default_customer_id` settings. Register aliases on existing customers, remove those settings, and increment the plugin configuration version before restarting.

## 0.1.0

- Add an opt-in builtin Metronome exporter for LLM token usage and authenticated external Fish Audio usage.
- Support dry runs, SecretVar credentials, stable external event IDs, and bounded ingestion retries.
