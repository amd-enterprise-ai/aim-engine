# MIT License
#
# Copyright (c) 2026 Advanced Micro Devices, Inc.

import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[1] / "combine-ca.sh"


@unittest.skipUnless(shutil.which("openssl"), "openssl is required")
class CombineCATests(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.root = Path(self.tempdir.name)
        self.system_ca = self._certificate("system-public-root")
        self.additional_ca = self._certificate("company-private-root")
        self.s3_ca = self._certificate("private-s3-root")

    def tearDown(self):
        self.tempdir.cleanup()

    def _certificate(self, common_name: str) -> Path:
        key = self.root / f"{common_name}.key"
        cert = self.root / f"{common_name}.crt"
        subprocess.run(
            [
                "openssl",
                "req",
                "-x509",
                "-newkey",
                "rsa:2048",
                "-nodes",
                "-days",
                "1",
                "-subj",
                f"/CN={common_name}",
                "-keyout",
                str(key),
                "-out",
                str(cert),
            ],
            check=True,
            capture_output=True,
            text=True,
        )
        return cert

    def _combine(self, *inputs: Path):
        output = self.root / "output" / "ca-bundle.crt"
        env = os.environ.copy()
        env["AIM_SYSTEM_CA_BUNDLE"] = str(self.system_ca)
        completed = subprocess.run(
            ["sh", str(SCRIPT), str(output), *(str(item) for item in inputs)],
            check=True,
            capture_output=True,
            text=True,
            env=env,
        )
        return output.read_text(), completed

    def test_preserves_system_roots_and_appends_custom_ca(self):
        output, _ = self._combine(self.additional_ca)

        self.assertIn(self.system_ca.read_text().strip(), output)
        self.assertIn(self.additional_ca.read_text().strip(), output)

    def test_combines_multiple_valid_bundles(self):
        output, _ = self._combine(self.additional_ca, self.s3_ca)

        self.assertIn(self.additional_ca.read_text().strip(), output)
        self.assertIn(self.s3_ca.read_text().strip(), output)

    def test_malformed_bundle_is_warned_and_skipped(self):
        malformed = self.root / "malformed.pem"
        malformed.write_text("this is not a certificate\n")

        output, completed = self._combine(malformed, self.additional_ca)

        self.assertIn("WARNING:", completed.stderr)
        self.assertNotIn(malformed.read_text().strip(), output)
        self.assertIn(self.system_ca.read_text().strip(), output)
        self.assertIn(self.additional_ca.read_text().strip(), output)

    def test_append_failure_does_not_publish_partial_bundle(self):
        bin_dir = self.root / "bin"
        bin_dir.mkdir()
        fake_cat = bin_dir / "cat"
        fake_cat.write_text("#!/bin/sh\nprintf partial\nexit 1\n")
        fake_cat.chmod(0o755)

        output = self.root / "output" / "ca-bundle.crt"
        env = os.environ.copy()
        env["AIM_SYSTEM_CA_BUNDLE"] = str(self.system_ca)
        env["PATH"] = f"{bin_dir}{os.pathsep}{env['PATH']}"
        completed = subprocess.run(
            ["sh", str(SCRIPT), str(output), str(self.additional_ca)],
            check=False,
            capture_output=True,
            text=True,
            env=env,
        )

        self.assertNotEqual(completed.returncode, 0)
        self.assertFalse(output.exists())


if __name__ == "__main__":
    unittest.main()
