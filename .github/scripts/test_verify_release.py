"""Behavior regressions for the standalone verifier (Python 3.11+, OpenSSL 3)."""

import base64
import contextlib
import hashlib
import importlib.util
import io
import json
import pathlib
import subprocess
import tarfile
import sys
import tempfile
import unittest

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("verify_release", pathlib.Path(__file__).with_name("verify_release.py"))
verifier = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verifier)


class ReleaseVerifierTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary = tempfile.TemporaryDirectory(prefix="pkp-verifier-test-")
        cls.key = pathlib.Path(cls.temporary.name) / "key.pem"
        subprocess.run(["openssl", "genpkey", "-algorithm", "ED25519", "-out", str(cls.key)], check=True, capture_output=True)
        public = subprocess.run(["openssl", "pkey", "-in", str(cls.key), "-pubout", "-outform", "DER"], check=True, capture_output=True).stdout
        cls.original_key = verifier.PUBLIC_KEY
        verifier.PUBLIC_KEY = base64.b64encode(public[-32:]).decode()

    @classmethod
    def tearDownClass(cls):
        verifier.PUBLIC_KEY = cls.original_key
        cls.temporary.cleanup()

    def fixture(self, directory, version, source_change=None):
        manifest = {"version": version, "publishedAt": "2026-11-01T00:00:00Z", "notes": "Release notes", "packages": {}}
        for platform, suffix in (("darwin-universal", "macos-universal.dmg"), ("windows-x64-arm64", "windows-x64-arm64-setup.exe")):
            name = f"PKP-{version}-{suffix}"
            body = b"installer bytes"
            (directory / name).write_bytes(body)
            manifest["packages"][platform] = {"url": f"https://github.com/tonyflux/pkp/releases/download/v{version}/{name}", "size": len(body), "sha256": hashlib.sha256(body).hexdigest(), "minOS": "12.0"}
        (directory / "LICENSE").write_text("License")
        (directory / "libmpv-sources.tar.gz").write_bytes(b"library sources")
        if tuple(map(int, version.split("."))) >= (0, 3, 0):
            source = {"commit": "a" * 40, "tree": "b" * 40}
            name = f"PKP-{version}-source.tar.gz"
            with tarfile.open(directory / name, "w:gz") as archive:
                for relative, body in {"release-source.json": json.dumps(source).encode(), "main.go": b"package main", "third_party/manifest.json": b"{}"}.items():
                    header = tarfile.TarInfo(f"PKP-{version}/{relative}")
                    header.size = len(body)
                    archive.addfile(header, io.BytesIO(body))
            source.update(url=f"https://github.com/tonyflux/pkp/releases/download/v{version}/{name}", size=(directory / name).stat().st_size, sha256=hashlib.sha256((directory / name).read_bytes()).hexdigest())
            manifest["source"] = source
            if source_change:
                source_change(manifest)
        (directory / "update.json").write_text(json.dumps(manifest))
        signed = subprocess.run(["openssl", "pkeyutl", "-sign", "-inkey", str(self.key), "-rawin", "-in", str(directory / "update.json")], check=True, capture_output=True).stdout
        (directory / "update.json.sig").write_bytes(base64.b64encode(signed))
        self.checksums(directory)

    def checksums(self, directory):
        (directory / "SHA256SUMS").write_text("".join(f"{hashlib.sha256(file.read_bytes()).hexdigest()}  {file.name}\n" for file in sorted(directory.iterdir()) if file.name != "SHA256SUMS"))

    def verify(self, directory, version):
        with contextlib.redirect_stdout(io.StringIO()):
            verifier.verify(directory, f"v{version}")

    def test_historical_seven_and_current_eight_assets(self):
        for version in ("0.1.0", "0.2.0", "0.3.0", "1.0.0"):
            with self.subTest(version=version), tempfile.TemporaryDirectory() as temporary:
                directory = pathlib.Path(temporary)
                self.fixture(directory, version)
                self.verify(directory, version)

    def test_source_is_authenticated_not_only_checksummed(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = pathlib.Path(temporary)
            self.fixture(directory, "0.3.0")
            (directory / "PKP-0.3.0-source.tar.gz").write_bytes(b"replacement archive")
            self.checksums(directory)
            with self.assertRaises(ValueError):
                self.verify(directory, "0.3.0")

    def test_source_identity_required_and_matches_archive(self):
        for change in (lambda manifest: manifest.pop("source"), lambda manifest: manifest["source"].update(commit="c" * 40)):
            with tempfile.TemporaryDirectory() as temporary:
                directory = pathlib.Path(temporary)
                self.fixture(directory, "0.3.0", change)
                with self.assertRaises(ValueError):
                    self.verify(directory, "0.3.0")

    def test_strict_allowlist_in_both_generations(self):
        for version in ("0.2.0", "0.3.0"):
            with tempfile.TemporaryDirectory() as temporary:
                directory = pathlib.Path(temporary)
                self.fixture(directory, version)
                (directory / "private.go").write_text("unexpected")
                self.checksums(directory)
                with self.assertRaises(ValueError):
                    self.verify(directory, version)


if __name__ == "__main__":
    unittest.main()
