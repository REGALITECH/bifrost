-- PostgreSQL only. Run with Bifrost, Admin and decision workers stopped.
-- psql -X -v ON_ERROR_STOP=1 -v apply=false -f scripts/migrate-etla-typesafe.sql
-- Default validates and prints a plan without changing data or sequences.
\if :{?apply}
\else
\set apply false
\endif
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
LOCK TABLE config_providers, config_keys, governance_virtual_key_provider_configs,
    governance_virtual_key_provider_config_keys, governance_pricing_overrides
    IN SHARE ROW EXCLUSIVE MODE;

CREATE TEMP TABLE etla_typesafe_moves ON COMMIT DROP AS
SELECT p.id AS provider_id, p.name AS provider, k.id AS key_db_id, k.key_id
FROM config_providers p JOIN config_keys k ON k.provider_id = p.id
WHERE p.name ~ '^etla-s1-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND p.custom_provider_config_json::jsonb ->> 'base_provider_type' = 'typesafe'
  AND lower(rtrim(p.network_config_json::jsonb ->> 'base_url', '/'))
      IN ('https://api.typesafe.ai', 'https://api.typesafe.ai:443');

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM etla_typesafe_moves GROUP BY provider HAVING count(*) <> 1)
       OR EXISTS (SELECT 1 FROM etla_typesafe_moves WHERE key_id <> substr(provider, 9)) THEN
        RAISE EXCEPTION 'Each legacy ETLA provider must have exactly its UUID key; review before migrating';
    END IF;
    IF EXISTS (SELECT 1 FROM config_providers p WHERE p.name = 'typesafe'
        AND (coalesce(nullif(p.custom_provider_config_json, '')::jsonb, '{}'::jsonb) NOT IN ('{}'::jsonb, 'null'::jsonb)
          OR coalesce(lower(rtrim(nullif(p.network_config_json, '')::jsonb ->> 'base_url', '/')), '')
             NOT IN ('', 'https://api.typesafe.ai', 'https://api.typesafe.ai:443'))) THEN
        RAISE EXCEPTION 'typesafe already has a custom endpoint/configuration; migration refused';
    END IF;
    IF EXISTS (SELECT 1 FROM config_providers p JOIN etla_typesafe_moves m ON p.id = m.provider_id
        WHERE coalesce(p.config_hash, '') <> '' OR p.budget_id IS NOT NULL OR p.rate_limit_id IS NOT NULL
           OR coalesce(nullif(p.proxy_config_json, '')::jsonb, '{}'::jsonb) NOT IN ('{}'::jsonb, 'null'::jsonb)
           OR coalesce(nullif(p.network_config_json, '')::jsonb -> 'extra_headers', '{}'::jsonb) NOT IN ('{}'::jsonb, 'null'::jsonb))
       OR EXISTS (SELECT 1 FROM config_keys k JOIN etla_typesafe_moves m ON k.id = m.key_db_id WHERE coalesce(k.config_hash, '') <> '') THEN
        RAISE EXCEPTION 'Legacy provider has file-managed config, proxy, headers, budget or rate limit; reconcile it explicitly first';
    END IF;
    IF EXISTS (SELECT 1 FROM config_providers p JOIN etla_typesafe_moves m ON p.id = m.provider_id
        WHERE coalesce(p.custom_provider_config_json::jsonb -> 'allowed_requests', 'null'::jsonb) <> 'null'::jsonb
           OR coalesce(p.custom_provider_config_json::jsonb -> 'request_path_overrides', '{}'::jsonb) NOT IN ('{}'::jsonb, 'null'::jsonb)
           OR coalesce((p.custom_provider_config_json::jsonb ->> 'is_key_less')::boolean, false)) THEN
        RAISE EXCEPTION 'Legacy custom provider behavior (allowed requests, request paths or keyless mode) needs explicit reconciliation';
    END IF;
    IF EXISTS (SELECT 1 FROM governance_model_configs c JOIN etla_typesafe_moves m ON c.provider = m.provider)
       OR EXISTS (SELECT 1 FROM governance_model_pricing p JOIN etla_typesafe_moves m ON p.provider = m.provider) THEN
        RAISE EXCEPTION 'Legacy Model Pricing or model governance needs an explicit scoped conversion before migration';
    END IF;
    IF EXISTS (SELECT 1 FROM routing_targets r JOIN etla_typesafe_moves m ON r.provider = m.provider)
       OR EXISTS (SELECT 1 FROM routing_rules r JOIN etla_typesafe_moves m ON strpos(row_to_json(r)::text, m.provider) > 0) THEN
        RAISE EXCEPTION 'Routing rules reference a legacy provider; reconcile them explicitly first';
    END IF;
    -- A shared config cannot express different model permissions per account.
    -- Refuse to broaden them or discard limits to force a merge.
    IF EXISTS (SELECT 1 FROM governance_virtual_key_provider_configs c
        WHERE (c.provider IN (SELECT provider FROM etla_typesafe_moves)
            OR (c.provider = 'typesafe' AND c.virtual_key_id IN
                (SELECT virtual_key_id FROM governance_virtual_key_provider_configs WHERE provider IN (SELECT provider FROM etla_typesafe_moves))))
        AND (coalesce(c.allowed_models::jsonb, 'null'::jsonb) <> '["*"]'::jsonb
          OR coalesce(c.blacklisted_models::jsonb, '[]'::jsonb) NOT IN ('[]'::jsonb, 'null'::jsonb)
          OR c.rate_limit_id IS NOT NULL OR coalesce(c.weight, 1) <> 1)) THEN
        RAISE EXCEPTION 'Virtual key model restrictions/limits differ from ETLA defaults; review before merging';
    END IF;
    IF EXISTS (SELECT 1 FROM governance_pricing_overrides o JOIN etla_typesafe_moves m ON o.provider_id = m.provider
        WHERE coalesce(o.config_hash, '') <> ''
          OR (o.provider_key_id IS NOT NULL AND o.provider_key_id <> m.key_id)
          OR o.scope_kind NOT IN ('provider', 'virtual_key_provider', 'user_provider', 'virtual_key_provider_key', 'user_provider_key')) THEN
        RAISE EXCEPTION 'Unsupported or file-managed legacy pricing override; review before migration';
    END IF;
    -- Destination provider prices did not apply to legacy accounts. They can
    -- outrank the converted key price (or start charging an unpriced model).
    -- Conservatively require reconciliation instead of guessing pattern overlap.
    IF EXISTS (SELECT 1 FROM etla_typesafe_moves)
       AND EXISTS (SELECT 1 FROM governance_pricing_overrides o
           WHERE o.provider_id = 'typesafe'
             AND (o.provider_key_id IS NULL OR o.provider_key_id IN (SELECT key_id FROM etla_typesafe_moves))) THEN
        RAISE EXCEPTION 'Destination TypeSafe pricing may change migrated account rates; reconcile it explicitly first';
    END IF;
    -- Converting provider scope to key scope can change precedence if this
    -- account already has a key override. Preserve it by refusing ambiguity.
    IF EXISTS (SELECT 1 FROM governance_pricing_overrides a
        JOIN etla_typesafe_moves m ON a.provider_id = m.provider
        JOIN governance_pricing_overrides b ON b.provider_key_id = m.key_id AND b.id <> a.id
        WHERE a.provider_key_id IS NULL)
       OR EXISTS (SELECT 1 FROM governance_pricing_overrides o JOIN etla_typesafe_moves m ON strpos(o.pattern, m.provider) > 0) THEN
        RAISE EXCEPTION 'Overlapping or provider-prefixed pricing overrides require explicit reconciliation';
    END IF;
END $$;

SELECT provider AS previous_provider, key_id, 'typesafe' AS next_provider FROM etla_typesafe_moves ORDER BY provider;
\if :apply
\else
ROLLBACK;
\echo 'Plan only. No data or sequences changed.'
\quit
\endif

-- Bootstrap only if there is something to move. Existing keys/config stay intact.
INSERT INTO config_providers (name, network_config_json, concurrency_buffer_json, created_at, updated_at, encryption_status)
SELECT 'typesafe', '{}', '{"concurrency":1000,"buffer_size":5000}', now(), now(), 'plain_text'
WHERE EXISTS (SELECT 1 FROM etla_typesafe_moves)
ON CONFLICT (name) DO NOTHING;

-- Snapshot wildcard grants BEFORE adding accounts. A pre-existing shared VK
-- must not acquire credentials just because they move into its provider.
INSERT INTO governance_virtual_key_provider_config_keys (table_virtual_key_provider_config_id, table_key_id)
SELECT c.id, k.id FROM governance_virtual_key_provider_configs c
JOIN config_keys k ON k.provider = c.provider
WHERE EXISTS (SELECT 1 FROM etla_typesafe_moves)
  AND (c.provider = 'typesafe' OR c.provider IN (SELECT provider FROM etla_typesafe_moves)) AND c.allow_all_keys
ON CONFLICT DO NOTHING;
UPDATE governance_virtual_key_provider_configs SET allow_all_keys = false
WHERE EXISTS (SELECT 1 FROM etla_typesafe_moves)
  AND (provider = 'typesafe' OR provider IN (SELECT provider FROM etla_typesafe_moves));

-- Keep legacy configs and their IDs for audit/rollback. Create a shared entry
-- only for VKs that previously had access; no new tenant is authorized.
INSERT INTO governance_virtual_key_provider_configs (virtual_key_id, provider, weight, allowed_models, blacklisted_models, allow_all_keys)
SELECT DISTINCT c.virtual_key_id, 'typesafe', 1, '["*"]', '[]', false
FROM governance_virtual_key_provider_configs c
WHERE c.provider IN (SELECT provider FROM etla_typesafe_moves)
  AND NOT EXISTS (SELECT 1 FROM governance_virtual_key_provider_configs t WHERE t.virtual_key_id = c.virtual_key_id AND t.provider = 'typesafe');
INSERT INTO governance_virtual_key_provider_config_keys (table_virtual_key_provider_config_id, table_key_id)
SELECT target.id, link.table_key_id
FROM governance_virtual_key_provider_configs old
JOIN governance_virtual_key_provider_config_keys link ON link.table_virtual_key_provider_config_id = old.id
JOIN etla_typesafe_moves m ON m.provider = old.provider AND m.key_db_id = link.table_key_id
JOIN governance_virtual_key_provider_configs target ON target.virtual_key_id = old.virtual_key_id AND target.provider = 'typesafe'
ON CONFLICT DO NOTHING;

-- Move the existing row. Its UUID, database ID, encrypted value, model filters,
-- key-scoped pricing overrides and VK join rows are preserved.
UPDATE config_keys k SET provider_id = target.id, provider = 'typesafe', updated_at = now()
FROM etla_typesafe_moves m, config_providers target
WHERE k.id = m.key_db_id AND target.name = 'typesafe';

-- Preserve the exact rate and account scope; a former single-account provider
-- price must not become a price for every account in the shared provider.
UPDATE governance_pricing_overrides o SET
    scope_kind = CASE o.scope_kind
        WHEN 'provider' THEN 'provider_key'
        WHEN 'virtual_key_provider' THEN 'virtual_key_provider_key'
        WHEN 'user_provider' THEN 'user_provider_key'
        ELSE o.scope_kind END,
    provider_id = CASE WHEN o.scope_kind = 'provider' THEN NULL ELSE 'typesafe' END,
    provider_key_id = m.key_id, updated_at = now()
FROM etla_typesafe_moves m WHERE o.provider_id = m.provider;

COMMIT;
\echo 'Applied. Restart Bifrost and deploy the matching Admin/etla_lib runtime together before resuming decisions.'
