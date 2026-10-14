# Changelog

## Unreleased

- Reject removed `dry_run` settings and align schema validation with the environment-only API key requirement. Explicitly choose `enabled` when migrating legacy configurations.
- Enabled instances require an environment-backed API key and send token and Fish Audio usage.
- Send the authenticated virtual-key UUID as `customer_id` for LLM and Fish Audio usage. Register VK UUIDs as Metronome customer ingest aliases; no Bifrost customer mapping is needed.
- Reject legacy `customer_mapping` and `default_customer_id` settings. Register aliases on existing customers, remove those settings, and increment the plugin configuration version before restarting.
- Export Fish Audio realtime turns (`/v1/realtime?model=fishaudio/...`) as `fishaudio-usage` events with `usage_source: "realtime"`. `billable_bytes` is the turn's input byte count as the realtime logs record it (whitespace-trimmed `user` text), which can differ by a few bytes from what Fish bills; text sent with another role is not counted. They are queued like token events and use attempt-based `bf-fishaudio-realtime-` transaction IDs, which never match external report IDs. Other providers' realtime turns remain excluded.
- Export speech usage (`/v1/audio/speech`, including completed streams) as `token-billing` input and output tokens, for example Gemini TTS. Speech without reported tokens, such as character-billed TTS, is skipped. Fish Audio speech is exported as `fishaudio-usage` with `usage_source: "speech"` and UTF-8 input bytes, never as `token-billing`, under attempt-based `bf-fishaudio-speech-` transaction IDs.

## 0.1.0

- Add an opt-in builtin Metronome exporter for LLM token usage and authenticated external Fish Audio usage.
- Support SecretVar credentials, stable external event IDs, and bounded ingestion retries.
