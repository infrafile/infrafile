#!/usr/bin/env python3
"""Render the release formula from GoReleaser checksums, without publishing it."""
import argparse
import pathlib
import re

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("tag")
parser.add_argument("checksums", type=pathlib.Path)
args = parser.parse_args()
if not re.fullmatch(r"v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?", args.tag):
    parser.error("expected a v-prefixed release tag")
version = args.tag[1:]
targets = [("macos", "darwin"), ("linux", "linux")]
checksums = {}
for line in args.checksums.read_text().splitlines():
    parts = line.split()
    if len(parts) != 2 or not re.fullmatch(r"[a-f0-9]{64}", parts[0]) or parts[1] in checksums:
        parser.error("invalid or duplicate checksum entry")
    checksums[parts[1]] = parts[0]
blocks = []
for platform, goos in targets:
    blocks.append(f"  on_{platform} do")
    for cpu, goarch in [("arm", "arm64"), ("intel", "amd64")]:
        archive = f"infrafile_{version}_{goos}_{goarch}.tar.gz"
        if archive not in checksums:
            parser.error(f"missing archive: {archive}")
        blocks.append(f'''    on_{cpu} do
      url "https://github.com/infrafile/infrafile/releases/download/{args.tag}/{archive}"
      sha256 "{checksums[archive]}"
    end''')
    blocks.append("  end")

print(f'''class Infrafile < Formula
  desc "Git-backed infrastructure ledger with credential references and read-only MCP"
  homepage "https://github.com/infrafile/infrafile"
  version "{version}"
  license "Apache-2.0"

{chr(10).join(blocks)}

  def install
    bin.install "infrafile"
    bin.install_symlink "infrafile" => "ifl"
    pkgshare.install "examples", "docs", "scripts"
  end

  test do
    assert_match version.to_s, shell_output("#{{bin}}/infrafile version")
    assert_equal shell_output("#{{bin}}/infrafile version"), shell_output("#{{bin}}/ifl version")
    system bin/"infrafile", "validate", pkgshare/"examples/homelab"
  end
end''')
