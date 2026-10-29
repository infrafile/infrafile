import pathlib
import subprocess
import sys
import tempfile
import unittest


class HomebrewFormulaTest(unittest.TestCase):
    def generate(self, lines, tag="v0.1.0"):
        with tempfile.TemporaryDirectory() as directory:
            manifest = pathlib.Path(directory) / "checksums.txt"
            manifest.write_text("\n".join(lines) + "\n")
            return subprocess.run(
                [sys.executable, str(pathlib.Path(__file__).with_name("homebrew.py")), tag, str(manifest)],
                text=True, capture_output=True,
            )

    def manifest(self):
        return [f"{digit * 64}  infrafile_0.1.0_{target}.tar.gz" for digit, target in
                zip("1234", ["darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"])]

    def test_release_formula_installs_binary_alias_and_example(self):
        result = self.generate(self.manifest())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('version "0.1.0"', result.stdout)
        self.assertIn('on_macos do', result.stdout)
        self.assertIn('on_linux do', result.stdout)
        self.assertIn('sha256 "' + "2" * 64 + '"', result.stdout)
        self.assertIn('https://github.com/infrafile/infrafile/releases/download/v0.1.0/infrafile_0.1.0_darwin_arm64.tar.gz', result.stdout)
        self.assertIn('bin.install_symlink "infrafile" => "ifl"', result.stdout)
        self.assertIn('pkgshare.install "examples", "docs", "scripts"', result.stdout)

    def test_invalid_input_never_emits_partial_formula(self):
        for lines, tag in [
            (self.manifest()[:-1], "v0.1.0"),
            (self.manifest() + self.manifest()[:1], "v0.1.0"),
            ([self.manifest()[0].replace("1" * 64, "invalid")] + self.manifest()[1:], "v0.1.0"),
            (self.manifest(), 'v0.1.0"; system "unexpected'),
        ]:
            with self.subTest(lines=lines, tag=tag):
                result = self.generate(lines, tag)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, "")


if __name__ == "__main__":
    unittest.main()
