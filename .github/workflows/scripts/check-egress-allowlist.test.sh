#!/usr/bin/env bash
set -euo pipefail

# Exercise the real checker without provider credentials or external requests.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

python3 - "${SCRIPT_DIR}/check-egress-allowlist.sh" <<'PY'
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

with tempfile.TemporaryDirectory(prefix="bifrost-egress-test-") as tmp:
    root = Path(tmp)
    scripts = root / ".github/workflows/scripts"
    scripts.mkdir(parents=True)
    checker = scripts / "check-egress-allowlist.sh"
    shutil.copyfile(sys.argv[1], checker)
    (scripts.parent / "harness.yml").write_text("""jobs:
  harness:
    steps:
      - uses: step-security/harden-runner@test
        with:
          egress-policy: block
          allowed-endpoints: api.openai.com:443
      - run: ./test-core.sh
""")
    collection = root / "tests/e2e/api/collections/provider-harness.json"
    collection.parent.mkdir(parents=True)
    config = root / "tests/integrations/python/config.json"
    config.parent.mkdir(parents=True)
    config.write_text('{}')
    payload = {
        "description": "Docs: https://ai.google.dev/gemini-api/docs "
                       "and https://learn.microsoft.com/azure/ai-services/openai/",
        "request": {
            "url": "https://api.openai.com/v1/responses",
            "body": {"mcp_servers": [{"url": "https://mcp.deepwiki.com/mcp"}]},
        },
    }

    def check():
        collection.write_text(json.dumps(payload))
        return subprocess.run(
            ["bash", str(checker)], cwd=root, text=True, capture_output=True,
        )

    result = check()
    if result.returncode != 0 or "all 1 harness-running" not in result.stdout:
        sys.exit("FAIL: documented and provider-fetched URLs should pass\n"
                 + result.stdout + result.stderr)
    print("ok - documentation and provider-fetched URLs need no runner egress")

    payload["request"]["url"] = "https://unclassified-egress.invalid/input"
    result = check()
    if result.returncode != 1 or "but omits unclassified-egress.invalid" not in result.stderr:
        sys.exit("FAIL: an unclassified destination must fail\n"
                 + result.stdout + result.stderr)
    print("ok - an unclassified destination still fails")
PY
