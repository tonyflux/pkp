"""Verify downloaded official PKP assets using only Python and OpenSSL 3."""

import base64
import hashlib
import json
import pathlib
import re
import subprocess
import posixpath
import sys
import tempfile
import tarfile

PUBLIC_KEY = "OWVabjpKxRDdGMlH9Jpk7NYhth7AFCws7Nvaox6C/ac="


def verify_source(directory, version, manifest, digests):
    source = manifest.get("source")
    if tuple(map(int, version.split("."))) < (0, 3, 0):
        if source is not None:
            raise ValueError("historical release cannot claim new source metadata")
        return
    name = f"PKP-{version}-source.tar.gz"
    if not isinstance(source, dict) or any(not re.fullmatch(r"[0-9a-f]{40}", source.get(key, "")) for key in ("commit", "tree")):
        raise ValueError("missing signed release source identity")
    if source.get("url") != f"https://github.com/tonyflux/pkp/releases/download/v{version}/{name}" or source.get("size") != (directory / name).stat().st_size or source.get("sha256") != digests[name]:
        raise ValueError("source archive differs from signed manifest")
    prefix = f"PKP-{version}/"
    seen = set()
    identity = dependencies = False
    with tarfile.open(directory / name, "r:gz") as archive:
        for entry in archive:
            if entry.isdir() and entry.name == prefix.rstrip("/"):
                continue
            relative = entry.name.removeprefix(prefix)
            clean = posixpath.normpath(relative)
            if not entry.name.startswith(prefix) or "\\" in relative or clean.startswith("/") or clean == ".." or clean.startswith("../") or (relative.rstrip("/") != clean and relative != "") or "/.git/" in f"/{clean}/" or clean in seen:
                raise ValueError("unsafe or duplicate source entry")
            seen.add(clean)
            if not (entry.isfile() or entry.isdir() or entry.issym()):
                raise ValueError("unsupported source archive entry")
            if entry.issym():
                target = posixpath.normpath(posixpath.join(posixpath.dirname(relative), entry.linkname))
                if entry.linkname.startswith("/") or "\\" in entry.linkname or target == ".." or target.startswith("../"):
                    raise ValueError("escaping source symlink")
            if relative == "release-source.json":
                if not entry.isfile() or entry.size > 4096:
                    raise ValueError("invalid source identity metadata")
                metadata = json.load(archive.extractfile(entry))
                if any(metadata.get(key) != source[key] for key in ("commit", "tree")):
                    raise ValueError("archive source identity differs from manifest")
                identity = True
            if relative.startswith("third_party/") and entry.isfile():
                dependencies = True
    if not identity or not dependencies:
        raise ValueError("source identity or dependency sources missing")


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
    if tuple(map(int, version.split("."))) >= (0, 3, 0):
        names.add(f"PKP-{version}-source.tar.gz")
    if {p.name for p in directory.iterdir()} != names:
        raise ValueError(f"expected exactly {len(names)} release assets")
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
    verify_source(directory, version, manifest, digests)
    for platform, name in packages.items():
        package = manifest["packages"][platform]
        if package["url"] != f"https://github.com/tonyflux/pkp/releases/download/{tag}/{name}":
            raise ValueError("untrusted installer URL")
        if package["size"] != (directory / name).stat().st_size or package["sha256"] != digests[name]:
            raise ValueError("installer differs from signed manifest")
    print(f"Verified {tag}: signature, both installers, all {len(names)} assets and SHA256SUMS")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit("usage: verify_release.py ASSET_DIRECTORY vVERSION")
    verify(pathlib.Path(sys.argv[1]), sys.argv[2])
