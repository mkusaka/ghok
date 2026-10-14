#!/usr/bin/env python3
"""Equivalent to the Go pre-hook; requires only Python 3's standard library.

Install executable and use ["./hooks/deny-admin-merge.py"] in config.yaml.
See examples/config.yaml for copy/install instructions. No jq is required.
"""

import json
import sys


def main():
    event = json.load(sys.stdin)
    if event.get("schemaVersion") != 1 or event.get("event") != "pre":
        raise ValueError("expected schemaVersion 1 pre event")
    response = {"schemaVersion": 1, "decision": "abstain"}
    for operation in event.get("operations", []):
        if operation.get("execution") in ("not-selected", "skipped"):
            continue
        if (
            operation.get("action", "").startswith("pr.merge.")
            and operation.get("parameters", {}).get("admin") is True
        ):
            response.update(
                decision="deny",
                reason="administrator merge bypass is disabled",
                message="Remove --admin and use the repository's normal merge checks.",
            )
            break
    json.dump(response, sys.stdout)
    sys.stdout.write("\n")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, TypeError, AttributeError, OSError) as error:
        print("deny-admin-merge:", error, file=sys.stderr)
        sys.exit(1)  # The configured pre-hook onError: deny fails closed.
