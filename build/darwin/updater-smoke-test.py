#!/usr/bin/env python3
"""Exercise the real updater using signed, windowless, disposable macOS apps.

Requires a logged-in macOS desktop, swiftc, hdiutil and a built updater helper.
Never launches, quits, installs over, or reads configuration from the real PKP app.
"""
import ctypes
import fcntl
import hashlib
import json
import os
import pathlib
import plistlib
import select
import shutil
import signal
import subprocess
import sys
import tempfile
import time

FIXTURE = r'''
import AppKit
import Darwin
import Foundation

final class Delegate: NSObject, NSApplicationDelegate {
    var timer: Timer?
    var lock: Int32 = -1
    func applicationDidFinishLaunching(_ notification: Notification) {
        guard let directory = ProcessInfo.processInfo.environment["PKP_CONFIG_DIR"],
              directory.contains("pkp-updater-smoke-") else { exit(80) }
        let profile = URL(fileURLWithPath: directory)
        let path = Bundle.main.bundleURL.deletingLastPathComponent().appendingPathComponent("install.lock").path
        lock = Darwin.open(path, O_RDWR | O_CREAT | O_CLOEXEC, 0o600)
        guard lock >= 0, flock(lock, LOCK_SH | LOCK_NB) == 0 else { exit(81) }
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as! String
        let data = try! JSONSerialization.data(withJSONObject: ["pid": getpid(), "version": version, "profile": directory])
        try! data.write(to: profile.appendingPathComponent("started-\(getpid()).json"), options: .atomic)
        timer = Timer.scheduledTimer(withTimeInterval: 0.05, repeats: true) { _ in
            if FileManager.default.fileExists(atPath: profile.appendingPathComponent("quit-\(getpid())").path) {
                NSApp.terminate(nil)
            }
        }
    }
}
let app = NSApplication.shared
app.setActivationPolicy(.prohibited)
let delegate = Delegate()
app.delegate = delegate
app.run()
'''


def run(*args):
    result = subprocess.run([str(arg) for arg in args], capture_output=True, timeout=180)
    if result.returncode:
        raise AssertionError(f"{args[0]} failed: {result.stderr.decode(errors='replace')}")
    return result.stdout


def until(read, description, timeout=30):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = read()
        if result:
            return result
        time.sleep(0.05)
    raise AssertionError(f"Timed out: {description}")


def version(bundle):
    with (bundle / "Contents/Info.plist").open("rb") as stream:
        return plistlib.load(stream)["CFBundleShortVersionString"]


def bundle(path, executable, release, minimum="12.0"):
    (path / "Contents/MacOS").mkdir(parents=True)
    (path / "Contents/Resources").mkdir()
    shutil.copy2(executable, path / "Contents/MacOS/pkp")
    (path / "Contents/Resources/payload").write_text(release)
    with (path / "Contents/Info.plist").open("wb") as stream:
        plistlib.dump({"CFBundleIdentifier": "com.pkp.desktop", "CFBundleName": "PKP Updater Smoke",
                      "CFBundleExecutable": "pkp", "CFBundlePackageType": "APPL",
                      "CFBundleShortVersionString": release, "CFBundleVersion": release,
                      "LSMinimumSystemVersion": minimum, "LSBackgroundOnly": True}, stream)
    run("/usr/bin/codesign", "--force", "--deep", "--sign", "-", path)


def image(root, executable, name, minimum="12.0", corrupt=False):
    source = root / (name + "-source")
    source.mkdir()
    app = source / "PKP ' Update.app"
    bundle(app, executable, "0.3.0", minimum)
    if corrupt:
        (app / "Contents/Resources/payload").write_text("modified after signing")
    dmg = root / (name + " ' update.dmg")
    run("/usr/bin/hdiutil", "create", "-quiet", "-format", "UDZO", "-srcfolder", source, dmg)
    return dmg


class Scenario:
    def __init__(self, root, name, executable, helper):
        self.root = root / name
        self.root.mkdir(mode=0o700)
        self.target = self.root / "PKP ' Installed.app"
        bundle(self.target, executable, "0.2.0")
        self.profile = self.root / "profile"
        self.profile.mkdir(mode=0o700)
        self.helper = helper
        self.parents = []
        self.helpers = []
        self.logs = []
        self.env = dict(os.environ, PKP_CONFIG_DIR=str(self.profile))
        self.receipt = self.profile / "install-result.json"
        self.lock = self.root / "install.lock"

    def start(self):
        parent = subprocess.Popen([str(self.target / "Contents/MacOS/pkp")], env=self.env,
                                  stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.parents.append(parent)
        until(lambda: (self.profile / f"started-{parent.pid}.json").exists(), "background fixture startup")
        return parent

    def quit(self, parent):
        (self.profile / f"quit-{parent.pid}").touch()
        parent.wait(timeout=15)

    def prepare(self, dmg, digest=None):
        workspace = pathlib.Path(tempfile.mkdtemp(prefix=".installer-", dir=self.profile))
        copied = workspace / "pkp-updater"
        shutil.copy2(self.helper, copied)
        log = (self.profile / f"helper-{len(self.helpers)}.log").open("wb")
        self.logs.append(log)
        digest = digest or hashlib.sha256(dmg.read_bytes()).hexdigest()
        proc = subprocess.Popen([str(copied), "--package", str(dmg), "--target", str(self.target),
                                 "--version", "0.3.0", "--sha256", digest, "--size", str(dmg.stat().st_size),
                                 "--parent-pid", str(self.parents[0].pid), "--receipt", str(self.receipt),
                                 "--lock", str(self.lock)], env=self.env,
                                stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=log)
        self.helpers.append(proc)
        ready, _, _ = select.select([proc.stdout], [], [], 120)
        assert ready, f"helper preparation timed out; logs in {self.profile}"
        message = proc.stdout.readline()
        assert message, f"helper exited without protocol response; logs in {self.profile}"
        return proc, json.loads(message)

    def finish(self, proc, expected=0):
        proc.wait(timeout=180)
        assert proc.returncode == expected, f"helper exit {proc.returncode}; logs in {self.profile}"

    def restarted(self, expected):
        original = {parent.pid for parent in self.parents}
        def read():
            for path in self.profile.glob("started-*.json"):
                data = json.loads(path.read_text())
                if data["pid"] not in original and data["version"] == expected:
                    return data
        data = until(read, f"relaunch {expected}")
        assert data["profile"] == str(self.profile), data
        return data

    def close(self):
        for proc in self.helpers:
            if proc.poll() is None:
                proc.stdin.close()
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait(timeout=5)
        # Only terminate recorded fixture PIDs whose executable still lives under this scenario.
        libproc = ctypes.CDLL("/usr/lib/libproc.dylib")
        for marker in self.profile.glob("started-*.json"):
            pid = json.loads(marker.read_text())["pid"]
            path = ctypes.create_string_buffer(4096)
            if libproc.proc_pidpath(pid, path, len(path)) > 0 and pathlib.Path(path.value.decode()).resolve().is_relative_to(self.root):
                os.kill(pid, signal.SIGTERM)
        for parent in self.parents:
            try:
                parent.wait(timeout=5)
            except subprocess.TimeoutExpired:
                parent.kill()
                parent.wait(timeout=5)
        for log in self.logs:
            log.close()
        run("/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister", "-u", self.target)


def main():
    if sys.platform != "darwin" or len(sys.argv) != 2:
        raise SystemExit("Usage (macOS desktop): python3 updater-smoke-test.py /path/to/pkp-updater")
    helper = pathlib.Path(sys.argv[1]).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="pkp-updater-smoke-") as temporary:
        root = pathlib.Path(temporary).resolve()
        source = root / "fixture.swift"
        source.write_text(FIXTURE)
        executable = root / "fixture"
        run("/usr/bin/xcrun", "swiftc", source, "-o", executable)
        good = image(root, executable, "good")
        corrupt = image(root, executable, "corrupt", corrupt=True)
        unlaunchable = image(root, executable, "unsupported-system", minimum="99.0")
        scenarios = []
        try:
            cancelled = Scenario(root, "cancel", executable, helper)
            scenarios.append(cancelled)
            old = cancelled.start()
            proc, message = cancelled.prepare(good)
            assert message == {"ready": True}, message
            assert version(cancelled.target) == "0.2.0" and old.poll() is None
            proc.stdin.close()
            cancelled.finish(proc)
            assert version(cancelled.target) == "0.2.0" and old.poll() is None and not cancelled.receipt.exists()
            print("PASS cancel: old application remains running and unchanged", flush=True)

            abandoned = Scenario(root, "parent-exit", executable, helper)
            scenarios.append(abandoned)
            old = abandoned.start()
            proc, message = abandoned.prepare(good)
            assert message == {"ready": True}, message
            abandoned.quit(old)
            proc.stdin.close()
            abandoned.finish(proc)
            assert version(abandoned.target) == "0.2.0" and not abandoned.receipt.exists()
            print("PASS parent loss: no install without an explicit commit", flush=True)

            for name, dmg, digest in [("checksum", good, "0" * 64), ("signature", corrupt, None)]:
                rejected = Scenario(root, name, executable, helper)
                scenarios.append(rejected)
                old = rejected.start()
                proc, message = rejected.prepare(dmg, digest)
                assert message == {"error": "integrity"}, message
                rejected.finish(proc, 1)
                assert old.poll() is None and version(rejected.target) == "0.2.0"
                print(f"PASS {name}: rejected before quitting", flush=True)

            busy = Scenario(root, "other-profile", executable, helper)
            scenarios.append(busy)
            busy.start()
            busy.start()
            proc, message = busy.prepare(good)
            assert message == {"error": "installBusy"}, message
            busy.finish(proc, 1)
            assert all(parent.poll() is None for parent in busy.parents)
            print("PASS competing instance: no application forced to quit", flush=True)

            occupied = Scenario(root, "late-install-lock", executable, helper)
            scenarios.append(occupied)
            old = occupied.start()
            proc, message = occupied.prepare(good)
            assert message == {"ready": True}, message
            with occupied.lock.open("rb") as guard:
                fcntl.flock(guard, fcntl.LOCK_SH)
                proc.stdin.write(b"install\n")
                proc.stdin.flush()
                proc.stdin.close()
                occupied.quit(old)
                occupied.finish(proc, 1)
                occupied.restarted("0.2.0")
            assert version(occupied.target) == "0.2.0"
            assert json.loads(occupied.receipt.read_text()) == {"version": "0.3.0", "error": "installBusy"}
            print("PASS late lock: replacement refused and unchanged application reopened", flush=True)

            for name, dmg, expected, exit_code in [("success", good, "0.3.0", 0),
                                                    ("rollback", unlaunchable, "0.2.0", 1)]:
                installed = Scenario(root, name, executable, helper)
                scenarios.append(installed)
                old = installed.start()
                proc, message = installed.prepare(dmg)
                assert message == {"ready": True}, message
                assert version(installed.target) == "0.2.0"
                proc.stdin.write(b"install\n")
                proc.stdin.flush()
                proc.stdin.close()
                installed.quit(old)
                installed.finish(proc, exit_code)
                installed.restarted(expected)
                assert version(installed.target) == expected
                assert (installed.target / "Contents/Resources/payload").read_text() == expected
                receipt = json.loads(installed.receipt.read_text())
                assert receipt == {"version": "0.3.0", "error": "" if exit_code == 0 else "install"}, receipt
                run("/usr/bin/codesign", "--verify", "--deep", "--strict", installed.target)
                print(f"PASS {name}: {expected} relaunched with the original isolated profile", flush=True)
        except BaseException:
            for scenario in scenarios:
                for path in scenario.profile.glob("helper-*.log"):
                    print(f"--- {scenario.root.name}/{path.name} ---\n{path.read_text(errors='replace')}", file=sys.stderr)
            raise
        finally:
            for scenario in reversed(scenarios):
                scenario.close()
    print("Native updater smoke passed; no real PKP application or user profile was touched.")


if __name__ == "__main__":
    main()
