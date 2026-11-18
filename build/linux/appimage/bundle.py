#!/usr/bin/env python3
"""Bundle installed Ubuntu runtime package closure and its exact source manifest."""
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys


def output(*args):
    return subprocess.check_output(args, text=True).strip()


def packages(seeds):
    pending = list(seeds)
    seen = set()
    while pending:
        package = pending.pop().split(":")[0]
        if package in seen:
            continue
        output("dpkg-query", "-W", "-f=${Status}", package) == "install ok installed" or sys.exit(f"Missing runtime package: {package}")
        seen.add(package)
        dependencies = output("apt-cache", "depends", package)
        virtual = False
        for line in dependencies.splitlines():
            match = re.match(r"\s*\|?(?:PreDepends|Depends): (<?[^\s]+)", line)
            if match:
                virtual = match[1].startswith("<")
                candidate = match[1].strip("<>").split(":")[0]
            elif virtual and re.match(r"^    \S", line):
                candidate = line.strip().split(":")[0]
            else:
                virtual = False
                continue
            status = subprocess.run(["dpkg-query", "-W", "-f=${Status}", candidate], capture_output=True, text=True)
            if status.returncode == 0 and status.stdout == "install ok installed":
                pending.append(candidate)
    return sorted(seen)


def bundle(destination):
    dest = Path(destination)
    seeds = ["libgtk-4-1", "libwebkitgtk-6.0-4", "libmpv2", "ffmpeg", "bubblewrap", "xdg-dbus-proxy",
             "libglib2.0-bin", "libgdk-pixbuf2.0-bin", "glib-networking", "gstreamer1.0-plugins-base",
             "gstreamer1.0-plugins-good", "fontconfig", "fonts-dejavu-core", "fonts-noto-cjk", "adwaita-icon-theme", "libgl1-mesa-dri",
             "libegl-mesa0", "libglx-mesa0", "libvulkan1", "shared-mime-info", "dash", "coreutils"]
    seeds.extend(["gsettings-desktop-schemas", "libc-bin", "base-files"])
    rows = []
    for package in packages(seeds):
        metadata = output("dpkg-query", "-W", "-f=${binary:Package}\t${Version}\t${source:Package}\t${source:Version}", package)
        rows.append(metadata)
        for filename in output("dpkg-query", "-L", package).splitlines():
            src = Path(filename)
            if not src.is_file() and not src.is_symlink():
                continue
            if not filename.startswith(("/usr/", "/lib/", "/lib64/", "/bin/", "/sbin/")):
                continue
            if filename.startswith(("/usr/share/man/", "/usr/share/lintian/", "/usr/share/bug/")):
                continue
            # Merged-/usr packages may retain /lib and /bin in their manifests.
            relative = filename.lstrip("/")
            if not relative.startswith("usr/"):
                relative = "usr/" + relative
            target = dest / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            if target.exists() or target.is_symlink():
                target.unlink()
            if src.is_symlink():
                link = os.readlink(src)
                if link.startswith("/"):
                    relative_link = link.lstrip("/")
                    if not relative_link.startswith("usr/"):
                        relative_link = "usr/" + relative_link
                    link = os.path.relpath(dest / relative_link, target.parent)
                target.symlink_to(link)
            else:
                shutil.copy2(src, target)
        # Debian copyright files can refer to /usr/share/common-licenses.
    shutil.copytree("/usr/share/common-licenses", dest / "usr/share/common-licenses", dirs_exist_ok=True)
    (dest / "runtime-packages.tsv").write_text("\n".join(rows) + "\n")
    # update-alternatives creates BLAS/LAPACK SONAME links outside dpkg manifests.
    # Link only to files already in the closure, never copy unrelated host ELFs.
    libdir = Path("/usr/lib") / output("gcc", "-dumpmachine")
    for alias in libdir.glob("*.so*"):
        if alias.is_symlink():
            real = alias.resolve()
            bundled = dest / real.relative_to("/")
            target = dest / alias.relative_to("/")
            if bundled.is_file() and not target.exists() and not target.is_symlink():
                target.symlink_to(os.path.relpath(bundled, target.parent))
    # Never carry a setuid executable in an AppImage.
    for item in dest.rglob("*"):
        if item.is_file() and not item.is_symlink():
            item.chmod(item.stat().st_mode & ~0o6000)


if __name__ == "__main__":
    bundle(sys.argv[1])
    dest = Path(sys.argv[1])
    triplet = output("gcc", "-dumpmachine")
    pixbuf = dest / "usr/lib" / triplet / "gdk-pixbuf-2.0/2.10.0"
    query = f"/usr/lib/{triplet}/gdk-pixbuf-2.0/gdk-pixbuf-query-loaders"
    cache = output(query, *map(str, sorted((pixbuf / "loaders").glob("*.so"))))
    (pixbuf / "loaders.cache").write_text(cache.replace(str(dest), "") + "\n")
