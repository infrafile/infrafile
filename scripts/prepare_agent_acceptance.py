#!/usr/bin/env python3
"""Prepare private, reviewable T16 evidence; never commit inventory or execute cloud control."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("scenario", choices=["inventory", "cloud"])
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--dir", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--request", type=Path)
    parser.add_argument("--key")
    args = parser.parse_args()
    binary, repo, output = args.binary.resolve(), args.dir.resolve(), args.output.resolve()
    if output == repo or repo in output.parents or output in repo.parents:
        parser.error("output must be a new directory outside the ledger")
    if args.scenario == "inventory" and (args.request is None or args.key is not None):
        parser.error("inventory requires --request and no --key")
    if args.scenario == "cloud" and (args.request is not None or not re.fullmatch(r"Resource/[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?", args.key or "")):
        parser.error("cloud requires a Resource key and no --request")
    os.umask(0o077)
    try:
        output.mkdir(mode=0o700)
    except OSError:
        parser.error("output must be a new writable directory; existing contents are preserved")
    trace = []
    stage = "version"

    def call(name, *command):
        nonlocal stage
        stage = name
        argv = [str(binary), *map(str, command), "--json"]
        item = {"step": name, "client": "local_cli", "argv": argv, "exit": None}
        trace.append(item)
        with (output / (name + ".json")).open("xb") as stdout, (output / (name + ".stderr.log")).open("xb") as stderr:
            process = subprocess.run(argv, stdout=stdout, stderr=stderr, timeout=45, check=False)
        item["exit"] = process.returncode
        if process.returncode != 0:
            raise ValueError("command failed")
        return json.loads((output / (name + ".json")).read_bytes())

    try:
        call("version", "version")
        if args.scenario == "inventory":
            result = call("preview", "change", "preview", repo, "--request", args.request.resolve(), "--out", output / "review.json")
            stage = "inventory_scope"
            if result.get("kind") != "EditPreview" or result.get("ok") is not True or not result.get("files"):
                raise ValueError("invalid inventory preview")
            reviewed = result["digest"]
        else:
            call("capabilities", "capabilities")
            call("context", "context", repo, "--key", args.key)
            result = call("plan", "plan", "cdn-pause", args.key, "--dir", repo, "-o", output / "review.json")
            stage = "cloud_scope"
            action = result["actions"][0]
            if result.get("version") != "v2" or len(result["actions"]) != 1 or action["target"] != args.key or action["operation"] != "cdn-pause" or action["binding"]["plugin"] != "cloudflare" or result.get("scope", {}).get("level") != "zone" or not result["scope"].get("zone_name") or action["before"].get("paused") is not False:
                raise ValueError("requires a scoped Cloudflare plan with an unpaused baseline")
            reviewed = result["digest"]
            shown = call("reviewed-plan", "plan", "show", output / "review.json")
            if shown != result:
                raise ValueError("saved plan differs from generated plan")
        if re.fullmatch(r"[a-f0-9]{64}", reviewed) is None:
            raise ValueError("invalid review digest")
        saved = json.loads((output / "review.json").read_bytes())
        if saved.get("digest") != reviewed:
            raise ValueError("saved review digest differs")
        summary = {"version": "v1", "scenario": args.scenario, "prepared": True, "client": "local_cli_preparation", "review_digest": reviewed, "review_file": str(output / "review.json"), "executed": False, "desktop_client_verified": False}
        (output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
        print(json.dumps(summary))
        return 0
    except (OSError, ValueError, KeyError, TypeError, IndexError, subprocess.SubprocessError):
        (output / "failure.json").write_text(json.dumps({"stage": stage, "executed": False}) + "\n")
        print("preparation failed at " + stage + "; private evidence retained", file=sys.stderr)
        return 2
    finally:
        (output / "trace.json").write_text(json.dumps(trace, indent=2) + "\n")


if __name__ == "__main__":
    sys.exit(main())
