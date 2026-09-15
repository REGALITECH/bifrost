#!/usr/bin/env python3
"""Save a synthetic usage event once, then resend that file without regenerating it."""
import argparse
import datetime
import getpass
import json
import os
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    prepare = commands.add_parser("prepare")
    prepare.add_argument("--event", type=Path, required=True)
    prepare.add_argument("--billable-bytes", type=int, default=54)
    prepare.add_argument("--audio-ms", type=int, default=2500)
    prepare.add_argument("--outcome", choices=["completed", "barged_in", "failed", "cache_hit"], default="completed")
    send = commands.add_parser("send")
    send.add_argument("--event", type=Path, required=True)
    send.add_argument("--base-url", default="http://127.0.0.1:18081")
    args = parser.parse_args()
    if args.command == "prepare":
        if args.billable_bytes < 0 or args.audio_ms < 0:
            parser.error("usage quantities must be non-negative")
        event_id = "fish-test-" + str(uuid.uuid4())
        event = {
            "request_id": event_id,
            "payload": {
                "billable_bytes": args.billable_bytes,
                "audio_ms": args.audio_ms,
                "outcome": args.outcome,
                "turn_id": event_id,
                "sub_id": "test-sub-1",
                "model": "s2-pro",
                "occurred_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            },
        }
        with args.event.open("x", encoding="utf-8") as output:
            json.dump(event, output, ensure_ascii=False, indent=2)
            output.write("\n")
        print(f"Saved {args.event}; reuse this file for every retry.")
        return

    event = json.loads(args.event.read_text(encoding="utf-8"))
    parsed = urllib.parse.urlsplit(args.base_url)
    if parsed.username or parsed.password or parsed.query or parsed.fragment:
        parser.error("base URL must not contain credentials, query, or fragment")
    if parsed.scheme != "https" and not (parsed.scheme == "http" and parsed.hostname in ("127.0.0.1", "localhost", "::1")):
        parser.error("use HTTPS or HTTP on localhost")
    key = os.environ.get("BIFROST_VIRTUAL_KEY") or getpass.getpass("Bifrost virtual key: ")
    request = urllib.request.Request(
        args.base_url.rstrip("/") + "/v1/fishaudio/usage",
        data=json.dumps(event["payload"]).encode("utf-8"),
        headers={"Content-Type": "application/json", "x-bf-vk": key, "x-request-id": event["request_id"]},
        method="POST",
    )
    # Do not forward the VK to a redirect target.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            return None

    try:
        with urllib.request.build_opener(NoRedirect).open(request, timeout=20) as response:
            receipt = json.load(response)
            if response.status != 202 or receipt.get("id") != event["request_id"]:
                raise SystemExit("Unexpected acknowledgment; retain the event file.")
    except urllib.error.HTTPError as exc:
        print(f"HTTP {exc.code}: {exc.read().decode('utf-8', errors='replace')}")
        raise SystemExit("Not delivered. Keep the event file; retry it after resolving the error.") from None
    print(json.dumps(receipt, indent=2))
    if receipt.get("metronome_status") != "sent":
        raise SystemExit("Metronome live delivery is not confirmed (dry run or logging only).")


if __name__ == "__main__":
    main()
