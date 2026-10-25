"""Verify downloaded official PKP assets using only Python and OpenSSL 3."""

import base64
import hashlib
import json
import pathlib
import re
import subprocess
import sys
import tempfile

PUBLIC_KEY = "OWVabjpKxRDdGMlH9Jpk7NYhth7AFCws7Nvaox6C/ac="


def verify(directory, tag):
    if not re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", tag):
        raise ValueError("not a stable release tag")
    version = tag[1:]
    if any(int(part) > 65535 for part in version.split(".")):
        raise ValueError("version outside supported range")
    packages = {
        "darwin-universal": f"PKP-{version}-macos-universal.dmg",
        "windows-x64-arm64": f"PKP-{version}-windows-x64-arm64-setup.exe",
    }
    names = {*packages.values(), "update.json", "update.json.sig", "libmpv-sources.tar.gz", "LICENSE", "SHA256SUMS"}
    if {p.name for p in directory.iterdir()} != names:
        raise ValueError("expected exactly seven release assets")
    for name in names:
        path = directory / name
        if path.is_symlink() or not path.is_file() or path.stat().st_size == 0:
            raise ValueError(f"not a nonempty regular asset: {name}")
    if (directory / "update.json").stat().st_size > 128 * 1024 or (directory / "update.json.sig").stat().st_size > 256:
        raise ValueError("oversized manifest or signature")
    data = (directory / "update.json").read_bytes()
    signature = base64.b64decode((directory / "update.json.sig").read_bytes().strip(), validate=True)
    with tempfile.TemporaryDirectory(prefix="pkp-signature-") as temporary:
        temporary = pathlib.Path(temporary)
        # RFC 8410 SubjectPublicKeyInfo for Ed25519, followed by the raw public key.
        (temporary / "key.der").write_bytes(bytes.fromhex("302a300506032b6570032100") + base64.b64decode(PUBLIC_KEY))
        (temporary / "signature").write_bytes(signature)
        subprocess.run([
            "openssl", "pkeyutl", "-verify", "-pubin", "-keyform", "DER",
            "-inkey", str(temporary / "key.der"), "-rawin", "-in", str(directory / "update.json"),
            "-sigfile", str(temporary / "signature"),
        ], check=True)
    manifest = json.loads(data)
    if manifest["version"] != version or set(manifest["packages"]) != set(packages) or not manifest["notes"].strip():
        raise ValueError("manifest does not match release")
    digests = {}
    for name in names - {"SHA256SUMS"}:
        with (directory / name).open("rb") as stream:
            digests[name] = hashlib.file_digest(stream, "sha256").hexdigest()
    seen = set()
    for line in (directory / "SHA256SUMS").read_text().splitlines():
        digest, name = line.split("  ", 1)
        if name in seen or name not in digests or digest != digests[name]:
            raise ValueError(f"invalid checksum entry: {name}")
        seen.add(name)
    if seen != set(digests):
        raise ValueError("incomplete checksum file")
    for platform, name in packages.items():
        package = manifest["packages"][platform]
        if package["url"] != f"https://github.com/tonyflux/pkp/releases/download/{tag}/{name}":
            raise ValueError("untrusted installer URL")
        if package["size"] != (directory / name).stat().st_size or package["sha256"] != digests[name]:
            raise ValueError("installer differs from signed manifest")
    print(f"Verified {tag}: signature, both installers, all seven assets and SHA256SUMS")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit("usage: verify_release.py ASSET_DIRECTORY vVERSION")
    verify(pathlib.Path(sys.argv[1]), sys.argv[2])
