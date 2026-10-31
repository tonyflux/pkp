"""Export pinned dependency inputs without copying private caches or Git history."""

import base64
import concurrent.futures
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import urllib.parse
import urllib.request


ROOT = Path(__file__).resolve().parent.parent


def command(*args, cwd=ROOT):
    return subprocess.run(args, cwd=cwd, check=True, capture_output=True, text=True).stdout


def json_stream(text):
    decoder = json.JSONDecoder()
    while text.strip():
        value, end = decoder.raw_decode(text.lstrip())
        yield value
        text = text.lstrip()[end:]


def download(url, path, integrity=None):
    parsed = urllib.parse.urlsplit(url)
    if parsed.scheme != "https" or parsed.username or parsed.password:
        raise ValueError("dependency URL must be public HTTPS")
    path.parent.mkdir(parents=True, exist_ok=True)
    request = urllib.request.Request(url, headers={"User-Agent": "PKP-corresponding-source"})
    with urllib.request.urlopen(request, timeout=120) as response, path.open("xb") as target:
        shutil.copyfileobj(response, target)
    if integrity:
        verified = False
        for part in integrity.split():
            algorithm, digest = part.split("-", 1)
            if algorithm in hashlib.algorithms_available:
                with path.open("rb") as stream:
                    verified |= base64.b64encode(hashlib.file_digest(stream, algorithm).digest()).decode() == digest
        if not verified:
            raise ValueError("dependency integrity mismatch: " + path.name)
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def escape_module(value):
    return "".join("!" + char.lower() if char.isupper() else char for char in value)


def go_sources(output):
    modules = list(json_stream(command("go", "mod", "download", "-json", "all")))
    command("go", "mod", "verify")
    records = []
    for module in modules:
        if "Error" in module or not module.get("Sum") or not module.get("GoModSum"):
            raise ValueError("incomplete verified Go dependency: " + module["Path"])
        relative = Path("go-proxy") / escape_module(module["Path"]) / "@v" / escape_module(module["Version"])
        files = {}
        for key, suffix in [("Zip", ".zip"), ("GoMod", ".mod"), ("Info", ".info")]:
            source = Path(module[key])
            if source.is_symlink() or not source.is_file():
                raise ValueError("dependency input must be a regular file")
            target = output / (str(relative) + suffix)
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, target)
            with target.open("rb") as stream:
                files[str(target.relative_to(output))] = hashlib.file_digest(stream, "sha256").hexdigest()
        records.append({"module": module["Path"], "version": module["Version"],
                        "sum": module["Sum"], "goModSum": module["GoModSum"], "files": files})
    cache = Path(command("go", "env", "GOMODCACHE").strip()) / "cache/download"
    for edge in sorted(set(command("go", "mod", "graph").split())):
        if "@v" not in edge:
            continue
        name, version = edge.rsplit("@", 1)
        relative = Path(escape_module(name)) / "@v" / (escape_module(version) + ".mod")
        target = output / "go-proxy" / relative
        if not target.exists():
            source = cache / relative
            if not source.is_file():
                selected = json.loads(command("go", "list", "-m", "-json", edge))
                source = Path(selected["GoMod"])
            if source.is_symlink() or not source.is_file():
                raise ValueError("missing Go graph metadata: " + edge)
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, target)
            with target.open("rb") as stream:
                digest = hashlib.file_digest(stream, "sha256").hexdigest()
            records.append({"module": name, "version": version, "metadataOnly": True,
                            "files": {str(target.relative_to(output)): digest}})
    return records


def npm_source(package):
    name, version, data, output = package
    key = hashlib.sha256((name + "@" + version).encode()).hexdigest()[:20]
    relative = Path("npm") / (key + ".tgz")
    digest = download(data["resolved"], output / relative, data["integrity"])
    record = {"name": name, "version": version, "url": data["resolved"],
              "integrity": data["integrity"], "archive": str(relative), "sha256": digest,
              "development": bool(data.get("dev")), "optional": bool(data.get("optional"))}
    embedded = {}
    with tarfile.open(output / relative) as archive:
        for member in archive:
            if not member.isfile() or not member.name.endswith(".map"):
                continue
            source_map = json.load(archive.extractfile(member))
            for source, text in zip(source_map.get("sources", []), source_map.get("sourcesContent", [])):
                if isinstance(text, str):
                    embedded[source] = text
    if embedded:
        sources_path = Path("preferred-source") / (key + ".json")
        (output / sources_path).parent.mkdir(parents=True, exist_ok=True)
        (output / sources_path).write_text(json.dumps(embedded, indent=2) + "\n")
        record["embeddedPreferredSource"] = str(sources_path)
    # Registry archives can contain transpiled/minified JS rather than preferred source.
    if not data.get("dev") and not data.get("optional"):
        encoded = urllib.parse.quote(name, safe="")
        url = "https://registry.npmjs.org/" + encoded + "/" + urllib.parse.quote(version, safe="")
        with urllib.request.urlopen(url, timeout=60) as response:
            metadata = json.load(response)
        if metadata.get("name") != name or metadata.get("version") != version:
            raise ValueError("registry metadata does not match locked dependency")
        repository = metadata.get("repository", {})
        if isinstance(repository, dict):
            repository = repository.get("url", "")
        match = re.fullmatch(r"(?:git\+)?(?:https?|git)://(github\.com|gitlab\.com)/([^/]+)/([^/]+?)(?:\.git)?", repository)
        commit = metadata.get("gitHead", "")
        if match and not re.fullmatch(r"[0-9a-f]{40}", commit):
            remote = "https://" + match[1] + "/" + match[2] + "/" + match[3] + ".git"
            for tag in ["v" + version, version, name + "@" + version]:
                refs = command("git", "ls-remote", remote, "refs/tags/" + tag, "refs/tags/" + tag + "^{}").splitlines()
                if refs:
                    commit = next((line.split()[0] for line in refs if line.endswith("^{}")), refs[0].split()[0])
                    break
        if match and re.fullmatch(r"[0-9a-f]{40}", commit):
            if match[1] == "github.com" or not embedded:
                record["upstream"] = {"host": match[1], "owner": match[2], "repository": match[3], "commit": commit}
        record["license"] = metadata.get("license", data.get("license", ""))
        (output / "npm" / (key + ".json")).write_text(json.dumps({
            field: metadata[field] for field in ["name", "version", "repository", "gitHead", "license"]
            if field in metadata}, indent=2) + "\n")
    return record


def upstream_sources(records, output):
    upstreams = {}
    for record in records:
        upstream = record.get("upstream")
        if not upstream:
            continue
        key = (upstream["host"], upstream["owner"], upstream["repository"], upstream["commit"])
        upstreams.setdefault(key, []).append(record)
    for (host, owner, repository, commit), packages in sorted(upstreams.items()):
        relative = Path("upstream") / (owner + "-" + repository + "-" + commit + ".tar.gz")
        if host == "github.com":
            url = "https://codeload.github.com/" + owner + "/" + repository + "/tar.gz/" + commit
        else:
            url = "https://gitlab.com/" + owner + "/" + repository + "/-/archive/" + commit + "/" + repository + "-" + commit + ".tar.gz"
        digest = download(url, output / relative)
        for package in packages:
            package["upstream"].update({"archive": str(relative), "url": url, "sha256": digest})


def main(destination):
    output = Path(destination).absolute()
    if output.is_symlink() or (output.exists() and (not output.is_dir() or any(output.iterdir()))):
        raise ValueError("output must be an empty directory or a nonexistent path")
    output.mkdir(parents=True, exist_ok=True)
    modules = go_sources(output)
    lock_path = ROOT / "frontend/package-lock.json"
    lock = json.loads(lock_path.read_text())
    packages = {}
    for name, data in lock["packages"].items():
        if not name:
            continue
        if data.get("link") or not data.get("resolved") or not data.get("integrity"):
            raise ValueError("dependency is not integrity-pinned: " + name)
        package_name = name.rsplit("node_modules/", 1)[-1]
        key = (package_name, data["version"])
        if key in packages and packages[key]["integrity"] != data["integrity"]:
            raise ValueError("conflicting package integrity")
        packages[key] = data
    with concurrent.futures.ThreadPoolExecutor(max_workers=6) as workers:
        records = list(workers.map(npm_source, [(name, version, data, output)
                                              for (name, version), data in sorted(packages.items())]))
    upstream_sources(records, output)
    (output / "manifest.json").write_text(json.dumps({"go": modules, "npm": records,
        "frontendLockSHA256": hashlib.sha256(lock_path.read_bytes()).hexdigest()}, indent=2) + "\n")
    (output / "restore.py").write_text('''"""Restore isolated build caches from the bundled, checksum-verified inputs."""
import hashlib
import json
from pathlib import Path
import subprocess
import sys
root = Path(__file__).resolve().parent
manifest = json.loads((root / "manifest.json").read_text())
cache = Path(sys.argv[1]).resolve()
cache.mkdir(parents=True, exist_ok=True)
for module in manifest["go"]:
    for name, digest in module["files"].items():
        with (root / name).open("rb") as stream:
            if hashlib.file_digest(stream, "sha256").hexdigest() != digest:
                raise ValueError("corrupt Go module input: " + name)
for package in manifest["npm"]:
    with (root / package["archive"]).open("rb") as stream:
        if hashlib.file_digest(stream, "sha256").hexdigest() != package["sha256"]:
            raise ValueError("corrupt npm input: " + package["name"])
    subprocess.run(["npm", "cache", "add", str(root / package["archive"]),
                    "--cache", str(cache), "--ignore-scripts", "--offline"], check=True)
print("npm cache:", cache)
print("GOPROXY=" + (root / "go-proxy").as_uri())
''')
    (output / "README.txt").write_text('''PINNED THIRD-PARTY BUILD INPUTS

The adjacent PKP source is the tracked release snapshot. This directory adds
Go module ZIP sources selected by `go mod download all`, verified by Go, plus
superseded go.mod metadata needed to resolve that graph offline, and every exact
frontend package-lock npm tarball including
build/test tools and optional packages. Original license and notice files remain
inside each upstream archive. manifest.json records integrity and SHA-256 values;
it deliberately contains no absolute cache paths, credentials, or Git history.

Registry archives may contain generated JavaScript. preferred-source/ contains
original sourcesContent recovered from integrity-verified package source maps.
For production nonoptional packages, upstream/ additionally contains available
preferred source at registry gitHead or a resolved version tag's exact commit.
See manifest.json for the package-to-source mapping. Optional native Node packages are build-host
inputs, not shipped PKP desktop runtime libraries; they are not asserted to be
source distributions. Media library sources and their build instructions are in
the separate libmpv-sources.tar.gz release attachment. Compilers, system SDKs,
Python, Node, Go and packaging tools remain separately installed prerequisites.
This inventory is not an independent legal audit or a bit-for-bit rebuild claim.

To reuse the pinned package inputs (Python 3.11+, npm installed):
  python3 third_party/restore.py /absolute/path/to/isolated-npm-cache
  export GOPROXY="file://$(pwd)/third_party/go-proxy"
  export GOSUMDB=off
  export GOMODCACHE=/absolute/path/to/isolated-go-cache
  cd frontend
  npm ci --offline --ignore-scripts --cache /absolute/path/to/isolated-npm-cache
  npm run build
  cd ..
  go mod download

Go verifies the recorded go.sum; GOSUMDB=off only prevents network lookup.
For Wails binding generation, install the version pinned in DEVELOPMENT.md
using this GOPROXY and cache. Follow DEVELOPMENT.md and the media archive's build
instructions for the native application and installation packages. Platform SDKs,
Go toolchain, NSIS, dmgbuild dependencies and media-build toolchains may require
separate installation; the commands above restore dependency inputs, not an
entire operating system or a fully offline native packaging environment.
''')
    print("Exported", len(modules), "Go module sources and", len(records), "pinned npm archives")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: release-dependency-sources.py OUTPUT_DIRECTORY")
    main(sys.argv[1])
