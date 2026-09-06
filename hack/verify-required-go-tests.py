#!/usr/bin/env python3
# Copyright 2026 The Faros Authors.
# SPDX-License-Identifier: Apache-2.0
"""Reject an empty or skipped required integration-test run (go test -json)."""

import json
import sys

for result_path in sys.argv[1:]:
    with open(result_path) as result_file:
        events = [json.loads(line) for line in result_file if line.strip()]
    passed = {e["Test"] for e in events if e.get("Action") == "pass" and "Test" in e}
    skipped = {e["Test"] for e in events if e.get("Action") == "skip" and "Test" in e}
    failed = [e for e in events if e.get("Action") == "fail"]
    if skipped or failed or not passed:
        raise SystemExit(
            f"Required tests did not pass: {result_path}: "
            f"passed={len(passed)}, skipped={sorted(skipped)}, failures={len(failed)}"
        )
    print(f"Required tests: {len(passed)} passed, zero skipped ({result_path})")

if len(sys.argv) < 2:
    raise SystemExit("A go test -json result file is required")
