#!/usr/bin/env python3
"""Noninteractive CLI regressions: python3 updater-protocol-test.py /path/to/pkp-updater.

These tests never launch an app, mount a disk image, request authorization, or install.
Full native transaction tests must use a separately signed throwaway application.
"""
import json
import pathlib
import subprocess
import sys
import tempfile


def check(helper, arguments, expected):
    result = subprocess.run(
        [str(helper), *arguments], input=b"install\n", capture_output=True, timeout=15
    )
    assert result.returncode != 0, result
    lines = result.stdout.splitlines()
    assert len(lines) == 1, result.stdout
    assert json.loads(lines[0]) == {"error": expected}, result.stdout
    assert b"[pkp-updater]" in result.stderr, result.stderr


def main():
    if len(sys.argv) != 2:
        raise SystemExit("Usage: updater-protocol-test.py /path/to/pkp-updater")
    helper = pathlib.Path(sys.argv[1]).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="PKP updater ' protocol ") as temporary:
        root = pathlib.Path(temporary)
        target = root / "PKP ' untouched.app"
        target.mkdir()
        sentinel = target / "sentinel"
        sentinel.write_bytes(b"unchanged\x00signed bytes")
        package = root / "update ' image.dmg"
        package.write_bytes(b"not an image")
        receipt = root / "receipt ' file.json"
        lock = root / "lock ' file"
        args = [
            "--package", str(package), "--target", str(target),
            "--version", "0.2.0", "--sha256", "a" * 64,
            "--size", str(package.stat().st_size), "--parent-pid", "2147483647",
            "--receipt", str(receipt), "--lock", str(lock),
        ]
        check(helper, [], "install")
        check(helper, ["--unknown", "x"], "install")
        check(helper, args, "installLocation")
        for key, value in [
            ("--size", "0"), ("--size", "-1"),
            ("--sha256", "g" * 64), ("--sha256", "0" * 63),
            ("--parent-pid", "0"), ("--parent-pid", "1"),
            ("--package", "relative.dmg"), ("--version", ""),
        ]:
            invalid = list(args)
            invalid[invalid.index(key) + 1] = value
            check(helper, invalid, "install")
        duplicate = list(args)
        duplicate[duplicate.index("--lock")] = "--receipt"
        check(helper, duplicate, "install")
        assert sentinel.read_bytes() == b"unchanged\x00signed bytes"
        assert not receipt.exists()
        assert not lock.exists()
        assert package.read_bytes() == b"not an image"
    print("Updater protocol regressions passed (no application launched or modified).")


if __name__ == "__main__":
    main()
