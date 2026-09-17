#!/usr/bin/env bash
set -euo pipefail

# Run the actual release binary and initialize its builtin plugin without credentials
# or outbound network access. Usage: bash test-metronome-image.sh IMAGE
image=${1:?Usage: test-metronome-image.sh IMAGE}
repo_root=$(cd "$(dirname "$0")/../../.." && pwd)
test_dir=$(mktemp -d)
container_id=""

# shellcheck disable=SC2329 # Invoked by the EXIT trap.
cleanup() {
  result=$?
  if [[ -n "$container_id" ]]; then
    if [[ "$result" -ne 0 ]]; then
      docker logs "$container_id" >&2 || true
    fi
    docker rm -f "$container_id" >/dev/null || true
  fi
  rm -rf "$test_dir"
  exit "$result"
}
trap cleanup EXIT

# Keep the documented plugin configuration and use local catalog fixtures so
# first startup does not depend on the public pricing service.
jq '.framework.pricing = {
  pricing_url: "file:///bifrost-test/pricing.json",
  model_parameters_url: "file:///bifrost-test/model-parameters.json",
  mcp_library_url: "file:///bifrost-test/mcp-library.json"
}' "$repo_root/examples/configs/withmetronome/config.json" > "$test_dir/config.json"
cp "$repo_root/framework/modelcatalog/datasheet/testdata/pricing.json" "$test_dir/"
cp "$repo_root/framework/modelcatalog/datasheet/testdata/model-parameters.json" "$test_dir/"
printf '[]\n' > "$test_dir/mcp-library.json"
chmod 755 "$test_dir"
chmod 644 "$test_dir/"*.json
container_id=$(docker create --network none --read-only \
  --tmpfs /app/data:rw,uid=1000,gid=0,mode=0770 \
  --tmpfs /tmp:rw,mode=1777 \
  --mount "type=bind,src=$test_dir,dst=/bifrost-test,readonly" \
  --mount "type=bind,src=$test_dir/config.json,dst=/app/data/config.json,readonly" \
  "$image")
docker start "$container_id" >/dev/null

# Verify initialization of the real builtin in the statically linked image.
for ((attempt = 0; attempt < 60; attempt++)); do
  if docker exec "$container_id" wget -q -T 2 -O - \
      http://127.0.0.1:8080/api/plugins/loaded > "$test_dir/loaded.json" 2>/dev/null && \
      jq -e '.plugins | index("metronome") != null' "$test_dir/loaded.json" >/dev/null; then
    echo "Metronome plugin loaded successfully from the release image."
    exit 0
  fi
  if [[ $(docker inspect --format '{{.State.Running}}' "$container_id") != true ]]; then
    echo "Bifrost exited before loading Metronome." >&2
    exit 1
  fi
  sleep 1
done
echo "Metronome did not appear in /api/plugins/loaded within the startup deadline." >&2
exit 1
