import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("prepare_agent_acceptance.py")
FAKE = r'''#!/usr/bin/env python3
import json,os,pathlib,sys
a=sys.argv[1:];mode=os.environ.get("FAKE_PREP_MODE","")
if "apply" in a:raise SystemExit(9)
if a[:2]==["change","preview"]:
 r={"kind":"EditPreview","ok":True,"digest":"a"*64,"files":[{"path":"inventory/servers/web.yaml"}]}
 pathlib.Path(a[a.index("--out")+1]).write_text(json.dumps(r))
elif a[:2]==["plan","show"]:r=json.loads(pathlib.Path(a[2]).read_text())
elif a[0]=="plan":
 r={"version":"v2","digest":"a"*64,"scope":{"level":"zone","zone_name":"example.test"},"actions":[{"target":"Resource/edge","operation":"cdn-pause","binding":{"plugin":"cloudflare"},"before":{"paused":mode=="paused"}}]}
 if mode=="external":r["actions"][0]["binding"]["plugin"]="external"
 if mode=="missing_scope":r.pop("scope")
 pathlib.Path(a[a.index("-o")+1]).write_text(json.dumps(r))
else:r={"version":"v1"}
print(json.dumps(r))
'''


class Preparation(unittest.TestCase):
    def run_scenario(self, scenario, mode=""):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        root = Path(temp.name)
        repo = root / "ledger"
        repo.mkdir()
        binary = root / "infrafile"
        binary.write_text(FAKE)
        binary.chmod(0o700)
        request = root / "request.json"
        request.write_text("{}")
        output = root / "evidence"
        args = [sys.executable, str(SCRIPT), scenario, "--binary", str(binary), "--dir", str(repo), "--output", str(output)]
        args += ["--request", str(request)] if scenario == "inventory" else ["--key", "Resource/edge"]
        process = subprocess.run(args, env={**os.environ, "FAKE_PREP_MODE": mode}, capture_output=True, text=True)
        return process, output, args

    def test_both_scenarios_only_prepare_private_review_and_trace(self):
        for scenario in ["inventory", "cloud"]:
            process, output, args = self.run_scenario(scenario)
            self.assertEqual(process.returncode, 0, process.stderr)
            summary = json.loads(process.stdout)
            self.assertFalse(summary["executed"])
            trace = json.loads((output / "trace.json").read_text())
            self.assertTrue(trace)
            self.assertFalse(any("apply" in event["argv"] for event in trace))
            self.assertEqual(output.stat().st_mode & 0o777, 0o700)
            self.assertTrue(all(p.stat().st_mode & 0o077 == 0 for p in output.iterdir()))
            before = (output / "review.json").read_bytes()
            retry = subprocess.run(args, capture_output=True)
            self.assertEqual(retry.returncode, 2)
            self.assertEqual((output / "review.json").read_bytes(), before)

    def test_wrong_cloud_provider_missing_scope_and_noop_baseline_refused(self):
        for mode in ["external", "missing_scope", "paused"]:
            process, output, _ = self.run_scenario("cloud", mode)
            self.assertEqual(process.returncode, 2)
            self.assertFalse((output / "summary.json").exists())
            self.assertEqual(json.loads((output / "failure.json").read_text())["stage"], "cloud_scope")
            self.assertFalse(any("apply" in event["argv"] for event in json.loads((output / "trace.json").read_text())))


if __name__ == "__main__":
    unittest.main()
