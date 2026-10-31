#!/usr/bin/env python3
"""Collect license and notice files for the production Go package graph."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys

if len(sys.argv) != 3 or sys.argv[1] not in ("darwin", "windows"):
    sys.exit("usage: python3 build/go-licenses.py darwin|windows OUTPUT")

target, output = sys.argv[1:]
root = Path(__file__).resolve().parent.parent
name = re.compile(r"^(license|licence|copying|notice|copyright|patents)(?:[._-]|$)", re.I)
modules = {}
for arch in ("amd64", "arm64"):
    env = os.environ.copy()
    env.update(GOOS=target, GOARCH=arch, CGO_ENABLED="1" if target == "darwin" else "0")
    data = subprocess.check_output(
        ["go", "list", "-deps", "-json", "-tags", "production", "."],
        cwd=root, env=env, text=True,
    )
    decoder = json.JSONDecoder()
    pos = 0
    while pos < len(data):
        pos = len(data) - len(data[pos:].lstrip())
        if pos == len(data):
            break
        package, pos = decoder.raw_decode(data, pos)
        module = package.get("Module")
        if module and module.get("Main"):
            continue
        directory = Path(package["Dir"])
        if module:
            if module.get("Replace"):
                module = module["Replace"]
            base = Path(module["Dir"])
            key = f'{module["Path"]} {module.get("Version", "(local replacement)")}'
        elif package.get("Standard"):
            base = Path(subprocess.check_output(["go", "env", "GOROOT"], env=env, text=True).strip())
            key = "Go standard library " + subprocess.check_output(["go", "env", "GOVERSION"], env=env, text=True).strip()
        else:
            raise SystemExit(f'Cannot identify module for {package["ImportPath"]}')
        files = modules.setdefault(key, set())
        while directory == base or base in directory.parents:
            for file in directory.iterdir():
                if file.is_file() and name.match(file.name):
                    files.add(file)
            if directory == base:
                break
            directory = directory.parent
        else:
            raise SystemExit(f'Package outside module: {package["ImportPath"]}')
        if not files:
            raise SystemExit(f'No license or notice files found for {key}')

parts = [f"PKP Go runtime third-party notices ({target}, amd64 and arm64)\n"]
for key, files in sorted(modules.items()):
    parts.append(f'\n{"=" * 72}\n{key}\n')
    for file in sorted(files):
        parts.append(f'\n--- {file.name} ---\n{file.read_text(encoding="utf-8")}\n')
Path(output).write_text("\n".join(parts), encoding="utf-8")
