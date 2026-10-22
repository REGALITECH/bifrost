"""Integration tests against a DISPOSABLE PostgreSQL DB with Bifrost's schema.

PGDATABASE must start with typesafe_migration_test. Load the schema from an
empty Bifrost instance (or a schema-only dump) before running this file.
Never run this test against the application's database.
"""
import os
from pathlib import Path
import subprocess
import unittest


MIGRATION = Path(__file__).with_name("migrate-etla-typesafe.sql").read_text()
A = "0192f0c4-7b1e-7c3a-9a4e-1d2b3c4d5e61"
B = "0192f0c4-7b1e-7c3a-9a4e-1d2b3c4d5e62"
C = "0192f0c4-7b1e-7c3a-9a4e-1d2b3c4d5e63"


def sql(text, success=True):
    result = subprocess.run(
        ["psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1"], input=text,
        text=True, capture_output=True, check=False,
    )
    if success and result.returncode:
        raise AssertionError(result.stderr)
    return result


class MigrationTest(unittest.TestCase):
    def setUp(self):
        if not os.environ.get("PGDATABASE", "").startswith("typesafe_migration_test"):
            self.fail("Use a disposable PGDATABASE named typesafe_migration_test*")
        sql(f"""
        TRUNCATE config_providers, governance_virtual_keys, governance_pricing_overrides,
                 governance_model_configs, governance_model_pricing, routing_rules CASCADE;
        INSERT INTO config_providers(name, network_config_json, custom_provider_config_json, created_at, updated_at)
        VALUES ('etla-s1-{A}', '{{"base_url":"https://api.typesafe.ai"}}', '{{"base_provider_type":"typesafe"}}', now(), now()),
               ('etla-s1-{B}', '{{"base_url":"https://api.typesafe.ai"}}', '{{"base_provider_type":"typesafe"}}', now(), now()),
               ('typesafe', '{{}}', NULL, now(), now()),
               ('private', '{{"base_url":"http://private:8080"}}', '{{"base_provider_type":"typesafe"}}', now(), now());
        INSERT INTO config_keys(name, provider_id, provider, key_id, value, models_json, created_at, updated_at)
        SELECT v.key, p.id, p.name, v.key, 'TEST-ONLY', '["*"]', now(), now()
        FROM (VALUES ('etla-s1-{A}', '{A}'), ('etla-s1-{B}', '{B}'), ('typesafe', '{C}')) v(provider,key)
        JOIN config_providers p ON p.name = v.provider;
        INSERT INTO governance_virtual_keys(id,name,value,created_at,updated_at)
        VALUES ('a','a','TEST-ONLY-a',now(),now()), ('b','b','TEST-ONLY-b',now(),now()),
               ('both','both','TEST-ONLY-both',now(),now()), ('shared','shared','TEST-ONLY-shared',now(),now());
        INSERT INTO governance_virtual_key_provider_configs(virtual_key_id,provider,allowed_models,blacklisted_models,allow_all_keys)
        VALUES ('a','etla-s1-{A}','["*"]','[]',true), ('b','etla-s1-{B}','["*"]','[]',true),
               ('both','etla-s1-{A}','["*"]','[]',true), ('both','etla-s1-{B}','["*"]','[]',true),
               ('shared','typesafe','["*"]','[]',true);
        INSERT INTO governance_pricing_overrides(id,name,scope_kind,provider_id,virtual_key_id,match_type,pattern,request_types_json,pricing_patch_json,created_at,updated_at)
        VALUES ('price-a','price-a','provider','etla-s1-{A}',NULL,'exact','jev-latest','["decision"]','{{"input_cost_per_token":0.0001}}',now(),now()),
               ('price-b','price-b','virtual_key_provider','etla-s1-{B}','b','exact','jev-preview','["decision"]','{{"input_cost_per_token":0.0002}}',now(),now());
        """)

    def migrate(self, apply, success=True):
        return sql("\\set apply " + ("true" if apply else "false") + "\n" + MIGRATION, success)

    def grants(self, vk):
        return sql(f"""SELECT k.key_id FROM governance_virtual_key_provider_configs c
            JOIN governance_virtual_key_provider_config_keys j ON j.table_virtual_key_provider_config_id=c.id
            JOIN config_keys k ON k.id=j.table_key_id
            WHERE c.virtual_key_id='{vk}' AND c.provider='typesafe' ORDER BY k.key_id""").stdout.strip().splitlines()

    def test_dry_run_and_apply_preserve_ids_prices_and_isolation(self):
        before = sql("SELECT key_id,id,value FROM config_keys ORDER BY key_id").stdout
        self.migrate(False)
        self.assertEqual([], self.grants("a"))
        self.assertEqual('2', sql("SELECT count(*) FROM config_keys WHERE provider LIKE 'etla-s1-%'").stdout.strip())
        self.migrate(True)
        self.assertEqual(before, sql("SELECT key_id,id,value FROM config_keys ORDER BY key_id").stdout)
        self.assertEqual([A], self.grants("a"))
        self.assertEqual([B], self.grants("b"))
        self.assertEqual([A, B], self.grants("both"))
        self.assertEqual([C], self.grants("shared"))
        self.assertEqual('0', sql("SELECT count(*) FROM governance_virtual_key_provider_configs WHERE provider='typesafe' AND allow_all_keys").stdout.strip())
        self.assertEqual(f'provider_key||{A}', sql("SELECT scope_kind,provider_id,provider_key_id FROM governance_pricing_overrides WHERE id='price-a'").stdout.strip())
        self.assertEqual(f'virtual_key_provider_key|typesafe|{B}', sql("SELECT scope_kind,provider_id,provider_key_id FROM governance_pricing_overrides WHERE id='price-b'").stdout.strip())
        self.assertEqual('0.0001', sql("SELECT pricing_patch_json::jsonb->>'input_cost_per_token' FROM governance_pricing_overrides WHERE id='price-a'").stdout.strip())
        self.assertEqual('4', sql("SELECT count(*) FROM config_providers").stdout.strip())
        snapshot = sql("SELECT row_to_json(c) FROM config_keys c ORDER BY key_id").stdout
        self.migrate(True)
        self.assertEqual(snapshot, sql("SELECT row_to_json(c) FROM config_keys c ORDER BY key_id").stdout)
        self.assertEqual([A, B], self.grants("both"))

    def test_custom_upstream_is_untouched(self):
        sql(f"UPDATE config_providers SET network_config_json='{{\"base_url\":\"http://private:8080\"}}' WHERE name='etla-s1-{B}'")
        self.migrate(True)
        self.assertEqual(f'etla-s1-{B}', sql(f"SELECT provider FROM config_keys WHERE key_id='{B}'").stdout.strip())
        self.assertEqual([], self.grants("b"))

    def test_conflicting_endpoint_rolls_back(self):
        sql("UPDATE config_providers SET network_config_json='{\"base_url\":\"https://elsewhere.test\"}' WHERE name='typesafe'")
        self.assertNotEqual(0, self.migrate(True, False).returncode)
        self.assertEqual('2', sql("SELECT count(*) FROM config_keys WHERE provider LIKE 'etla-s1-%'").stdout.strip())

    def test_model_restrictions_are_not_broadened(self):
        sql("UPDATE governance_virtual_key_provider_configs SET allowed_models='[\"jev-latest\"]' WHERE virtual_key_id='a'")
        self.assertNotEqual(0, self.migrate(True, False).returncode)
        self.assertEqual([], self.grants("a"))

    def test_overlapping_prices_are_not_silently_reprioritized(self):
        sql(f"""INSERT INTO governance_pricing_overrides(id,name,scope_kind,provider_key_id,match_type,pattern,created_at,updated_at)
            VALUES ('key-price','key-price','provider_key','{A}','glob','*',now(),now())""")
        self.assertNotEqual(0, self.migrate(True, False).returncode)
        self.assertEqual('provider', sql("SELECT scope_kind FROM governance_pricing_overrides WHERE id='price-a'").stdout.strip())



    def snapshot(self):
        return tuple(sql(f"SELECT row_to_json(r) FROM {table} r ORDER BY row_to_json(r)::text").stdout
                     for table in ("config_providers", "config_keys", "governance_virtual_key_provider_configs",
                                   "governance_virtual_key_provider_config_keys", "governance_pricing_overrides"))

    def test_destination_prices_require_reconciliation(self):
        for scope, vk, key in (("provider", "NULL", "NULL"),
                               ("virtual_key_provider", "'a'", "NULL"),
                               ("user_provider", "NULL", "NULL"),
                               ("virtual_key_provider_key", "'a'", f"'{A}'")):
            for apply in (False, True):
                with self.subTest(scope=scope, apply=apply):
                    self.setUp()
                    sql(f"""INSERT INTO governance_pricing_overrides
                        (id,name,scope_kind,provider_id,virtual_key_id,provider_key_id,match_type,pattern,pricing_patch_json,created_at,updated_at)
                        VALUES ('dest','dest','{scope}','typesafe',{vk},{key},'exact','jev-latest',
                                '{{"input_cost_per_token":0.9}}',now(),now())""")
                    before = self.snapshot()
                    result = self.migrate(apply, False)
                    self.assertNotEqual(0, result.returncode)
                    self.assertIn('Destination TypeSafe pricing', result.stderr)
                    self.assertEqual(before, self.snapshot())

    def test_legacy_custom_behavior_requires_reconciliation(self):
        for config in ('"allowed_requests":{"decision":false}',
                       '"allowed_requests":{}',
                       '"request_path_overrides":{"decision":"/alternate"}',
                       '"is_key_less":true'):
            for apply in (False, True):
                with self.subTest(config=config, apply=apply):
                    self.setUp()
                    sql(f"""UPDATE config_providers SET custom_provider_config_json=
                        '{{"base_provider_type":"typesafe",{config}}}' WHERE name='etla-s1-{A}'""")
                    before = self.snapshot()
                    result = self.migrate(apply, False)
                    self.assertNotEqual(0, result.returncode)
                    self.assertIn('Legacy custom provider behavior', result.stderr)
                    self.assertEqual(before, self.snapshot())

    def test_default_custom_config_is_migratable(self):
        sql(f"""UPDATE config_providers SET custom_provider_config_json=
            '{{"base_provider_type":"typesafe","is_key_less":false,"allowed_requests":null,"request_path_overrides":{{}}}}'
            WHERE name='etla-s1-{A}'""")
        self.migrate(True)
        self.assertEqual('typesafe', sql(f"SELECT provider FROM config_keys WHERE key_id='{A}'").stdout.strip())


if __name__ == "__main__":
    unittest.main()
