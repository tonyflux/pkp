import AppKit
import CryptoKit
import Darwin
import Foundation
import Security

// This executable never removes quarantine or assesses/overrides Gatekeeper policy.
private let files = FileManager.default
private let identity = "com.pkp.desktop"

private struct Failure: Error {
    let code: String
    let detail: String
    init(_ code: String, _ detail: String) { self.code = code; self.detail = detail }
}

private func log(_ message: String) {
    FileHandle.standardError.write(Data(("[pkp-updater] \(message)\n").utf8))
}

private func canonical(_ url: URL) -> URL {
    url.standardizedFileURL.resolvingSymlinksInPath()
}

private func contained(_ child: URL, in parent: URL) -> Bool {
    child.path == parent.path || child.path.hasPrefix(parent.path + "/")
}

private struct ProcessBirth: Equatable {
    let seconds: UInt64
    let microseconds: UInt64
}

private func processBirth(_ pid: pid_t) -> ProcessBirth? {
    var info = proc_bsdinfo()
    guard proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, Int32(MemoryLayout<proc_bsdinfo>.size)) == Int32(MemoryLayout<proc_bsdinfo>.size),
          info.pbi_status != UInt32(SZOMB) else { return nil }
    return ProcessBirth(seconds: info.pbi_start_tvsec, microseconds: info.pbi_start_tvusec)
}

private func emit(_ object: [String: Any]) {
    guard var data = try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]) else { return }
    data.append(10)
    data.withUnsafeBytes { bytes in
        var offset = 0
        while offset < bytes.count {
            let count = Darwin.write(STDOUT_FILENO, bytes.baseAddress!.advanced(by: offset), bytes.count - offset)
            if count < 0 {
                if errno == EINTR { continue }
                return
            }
            guard count > 0 else { return }
            offset += count
        }
    }
}

private struct Arguments {
    let package: URL
    let target: URL
    let version: String
    let sha256: String
    let size: Int64
    let parentPID: pid_t
    let receipt: URL
    let lock: URL

    init() throws {
        let args = Array(CommandLine.arguments.dropFirst())
        let names: Set<String> = ["--package", "--target", "--version", "--sha256", "--size", "--parent-pid", "--receipt", "--lock"]
        guard args.count == names.count * 2 else { throw Failure("install", "Invalid argument count") }
        var values = [String: String]()
        for i in stride(from: 0, to: args.count, by: 2) {
            guard names.contains(args[i]), values[args[i]] == nil, !args[i + 1].isEmpty else {
                throw Failure("install", "Invalid or duplicate argument")
            }
            values[args[i]] = args[i + 1]
        }
        func path(_ name: String) throws -> URL {
            guard let value = values[name], value.hasPrefix("/"), !value.utf8.contains(0) else {
                throw Failure("install", "Expected absolute path for \(name)")
            }
            return URL(fileURLWithPath: value).standardizedFileURL
        }
        package = try path("--package")
        target = try path("--target")
        receipt = try path("--receipt")
        lock = try path("--lock")
        version = values["--version"]!
        sha256 = values["--sha256"]!.lowercased()
        guard sha256.count == 64, sha256.utf8.allSatisfy({ (48...57).contains($0) || (97...102).contains($0) }),
              let byteCount = Int64(values["--size"]!), byteCount > 0,
              let pid = Int32(values["--parent-pid"]!), pid > 1, pid != getpid(),
              !version.contains("\n"), !version.utf8.contains(0) else {
            throw Failure("install", "Invalid digest, version, size, or parent PID")
        }
        size = byteCount
        parentPID = pid
    }
}

// Callbacks are marshalled onto the main run loop; no GUI event loop or app activation is needed.
private func awaitCallback<T>(timeout: TimeInterval, _ start: (@escaping (T) -> Void) -> Void) throws -> T {
    var value: T?
    start { result in DispatchQueue.main.async { value = result } }
    let deadline = Date(timeIntervalSinceNow: timeout)
    while value == nil && Date() < deadline {
        RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.05))
    }
    guard let result = value else { throw Failure("install", "System API timed out") }
    return result
}

private final class Installer {
    let args: Arguments
    let parentBirth: ProcessBirth
    var workspace: URL?
    var mount: URL?
    var mounted = false
    var staged: URL?
    var backup: URL?
    var authorizedFiles: FileManager?
    var lockFD: Int32 = -1
    var locked = false
    var preserveBackup = false
    var ready = false
    var committed = false
    var replacementAttempted = false
    var helperDirectory: URL?
    var oldVersion = ""
    var oldCodeHash: Data?

    init(_ args: Arguments) throws {
        self.args = args
        guard let app = NSRunningApplication(processIdentifier: args.parentPID),
              app.bundleIdentifier == identity, let bundle = app.bundleURL,
              canonical(bundle) == args.target, let birth = processBirth(args.parentPID), !app.isTerminated else {
            throw Failure("installLocation", "Parent is not the original installed PKP application")
        }
        parentBirth = birth
    }

    func parentAlive() -> Bool {
        processBirth(args.parentPID) == parentBirth
    }

    func runningAtTarget() throws -> Set<pid_t> {
        var result = Set<pid_t>()
        for app in NSWorkspace.shared.runningApplications {
            if let bundle = app.bundleURL, canonical(bundle) == args.target, !app.isTerminated {
                result.insert(app.processIdentifier)
            }
        }
        // Direct executable launches (including another configuration profile) need not have
        // registered with LaunchServices yet. Inspect their actual executable paths too.
        let count = proc_listallpids(nil, 0)
        guard count > 0 else { throw Failure("installBusy", "Cannot enumerate running processes") }
        var pids = [pid_t](repeating: 0, count: Int(count) + 256)
        let found = pids.withUnsafeMutableBytes { proc_listallpids($0.baseAddress, Int32($0.count)) }
        guard found > 0, found < pids.count else { throw Failure("installBusy", "Process list changed during enumeration") }
        // proc_info.h defines PROC_PIDPATHINFO_MAXSIZE as 4 * MAXPATHLEN.
        var path = [CChar](repeating: 0, count: 4 * Int(MAXPATHLEN))
        for pid in pids.prefix(Int(found)) where pid > 0 && pid != getpid() {
            let length = path.withUnsafeMutableBytes { proc_pidpath(pid, $0.baseAddress, UInt32($0.count)) }
            if length > 0 {
                let executable = URL(fileURLWithPath: String(cString: path))
                if contained(canonical(executable), in: args.target), processBirth(pid) != nil { result.insert(pid) }
            }
        }
        result.remove(getpid())
        return result
    }

    func checkNoOtherInstances() throws {
        var instances = try runningAtTarget()
        if parentAlive() { instances.remove(args.parentPID) }
        guard instances.isEmpty else { throw Failure("installBusy", "Another process is using \(args.target.path): \(instances)") }
    }

    func validateLocation() throws {
        let target = args.target
        guard target.pathExtension.lowercased() == "app", canonical(target) == target,
              !target.path.contains("/AppTranslocation/") else {
            throw Failure("installLocation", "Target is symlinked or translocated")
        }
        var ancestor = target.deletingLastPathComponent()
        while ancestor.path != "/" {
            guard ancestor.pathExtension.lowercased() != "app" else { throw Failure("installLocation", "Target is a nested application") }
            ancestor.deleteLastPathComponent()
        }
        var metadata = stat()
        var volume = statfs()
        guard lstat(target.path, &metadata) == 0, metadata.st_mode & S_IFMT == S_IFDIR,
              statfs(target.path, &volume) == 0, volume.f_flags & UInt32(MNT_RDONLY) == 0 else {
            throw Failure("installLocation", "Target is missing, not a directory, or on a read-only volume")
        }
        guard metadata.st_flags & UInt32(UF_IMMUTABLE | SF_IMMUTABLE | UF_APPEND | SF_APPEND) == 0 else {
            throw Failure("installPermission", "Target has immutable or append-only flags")
        }
        let version = try validateBundle(target, expectedVersion: oldVersion.isEmpty ? nil : oldVersion, code: "installLocation")
        if oldVersion.isEmpty { oldVersion = version }
        let hash = try signingIdentity(target)
        if let original = oldCodeHash, original != hash {
            throw Failure("installBusy", "Installed application changed after preparation")
        }
        oldCodeHash = hash
    }

    func signingIdentity(_ app: URL) throws -> Data {
        var code: SecStaticCode?
        var information: CFDictionary?
        guard SecStaticCodeCreateWithPath(app as CFURL, SecCSFlags(rawValue: 0), &code) == errSecSuccess,
              let code = code,
              SecCodeCopySigningInformation(code, SecCSFlags(rawValue: kSecCSSigningInformation), &information) == errSecSuccess,
              let dictionary = information as? [String: Any],
              let hash = dictionary[kSecCodeInfoUnique as String] as? Data else {
            throw Failure("integrity", "Cannot read application signing identity")
        }
        return hash
    }

    @discardableResult
    func validateBundle(_ app: URL, expectedVersion: String?, code: String = "integrity") throws -> String {
        let root = canonical(app)
        guard root == app.standardizedFileURL, app.pathExtension.lowercased() == "app" else {
            throw Failure(code, "Application root is not canonical: \(app.absoluteString), resolved \(root.absoluteString)")
        }
        let infoURL = app.appendingPathComponent("Contents/Info.plist")
        guard contained(canonical(infoURL), in: root),
              let info = try PropertyListSerialization.propertyList(from: Data(contentsOf: infoURL), format: nil) as? [String: Any],
              info["CFBundleIdentifier"] as? String == identity,
              info["CFBundlePackageType"] as? String == "APPL",
              let version = info["CFBundleShortVersionString"] as? String,
              let executable = info["CFBundleExecutable"] as? String,
              !executable.isEmpty, executable != ".", executable != "..", !executable.contains("/"),
              !executable.utf8.contains(0), expectedVersion == nil || version == expectedVersion else {
            throw Failure(code, "Application identity, version, or executable is invalid")
        }
        let binary = app.appendingPathComponent("Contents/MacOS").appendingPathComponent(executable)
        var binaryInfo = stat()
        guard contained(canonical(binary), in: root), stat(binary.path, &binaryInfo) == 0,
              binaryInfo.st_mode & S_IFMT == S_IFREG, access(binary.path, X_OK) == 0 else {
            throw Failure(code, "Application executable is missing or escapes bundle")
        }
        var traversalError: Error?
        guard let entries = files.enumerator(at: app, includingPropertiesForKeys: [.isSymbolicLinkKey], options: [], errorHandler: { _, error in
            traversalError = error
            return false
        }) else { throw Failure(code, "Cannot enumerate application") }
        for case let item as URL in entries {
            if try item.resourceValues(forKeys: [.isSymbolicLinkKey]).isSymbolicLink == true,
               !contained(canonical(item), in: root) {
                throw Failure(code, "Application contains escaping symlink: \(item.path)")
            }
        }
        if let error = traversalError { throw Failure(code, "Cannot inspect application: \(error)") }
        _ = try run("/usr/bin/codesign", ["--verify", "--deep", "--strict", app.path], timeout: 120, code: code)
        return version
    }

    func makeWorkspace() throws {
        let directory = try files.url(for: .itemReplacementDirectory, in: .userDomainMask, appropriateFor: args.target, create: true)
        workspace = canonical(directory)
        try files.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path)
        var source = stat(), destination = stat()
        guard stat(args.target.path, &source) == 0, stat(directory.path, &destination) == 0,
              source.st_dev == destination.st_dev, destination.st_uid == getuid() else {
            throw Failure("installLocation", "Cannot stage safely on the installation volume")
        }
        let executable = canonical(URL(fileURLWithPath: CommandLine.arguments[0]))
        guard !contained(executable, in: args.target) else {
            throw Failure("install", "Helper must be copied outside the application before launching")
        }
        let helperParent = executable.deletingLastPathComponent()
        var helperInfo = stat()
        if executable.lastPathComponent == "pkp-updater", helperParent.lastPathComponent.hasPrefix(".installer-"),
           lstat(helperParent.path, &helperInfo) == 0, helperInfo.st_uid == getuid(),
           helperInfo.st_mode & S_IFMT == S_IFDIR, helperInfo.st_mode & 0o077 == 0 {
            helperDirectory = helperParent
        }
    }

    func run(_ executable: String, _ arguments: [String], timeout: TimeInterval, code: String) throws -> Data {
        let root = workspace ?? canonical(files.temporaryDirectory)
        let token = UUID().uuidString
        let outputURL = root.appendingPathComponent("command-\(token).out")
        let errorURL = root.appendingPathComponent("command-\(token).err")
        guard files.createFile(atPath: outputURL.path, contents: nil, attributes: [.posixPermissions: 0o600]),
              files.createFile(atPath: errorURL.path, contents: nil, attributes: [.posixPermissions: 0o600]) else {
            throw Failure(code, "Cannot create subprocess logs")
        }
        defer { try? files.removeItem(at: outputURL); try? files.removeItem(at: errorURL) }
        let output = try FileHandle(forWritingTo: outputURL)
        let errors = try FileHandle(forWritingTo: errorURL)
        defer { try? output.close(); try? errors.close() }
        let process = Process()
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = arguments
        process.standardInput = FileHandle.nullDevice
        process.standardOutput = output
        process.standardError = errors
        try process.run()
        let deadline = Date(timeIntervalSinceNow: timeout)
        while process.isRunning && Date() < deadline {
            RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.05))
        }
        if process.isRunning {
            process.terminate()
            let grace = Date(timeIntervalSinceNow: 2)
            while process.isRunning && Date() < grace { Thread.sleep(forTimeInterval: 0.05) }
            if process.isRunning { kill(process.processIdentifier, SIGKILL) }
            let killedDeadline = Date(timeIntervalSinceNow: 2)
            while process.isRunning && Date() < killedDeadline {
                RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.05))
            }
            if process.isRunning { preserveBackup = true }
            throw Failure(code, "\(executable) timed out")
        }
        process.waitUntilExit()
        let diagnostics = try Data(contentsOf: errorURL)
        if !diagnostics.isEmpty { log(String(decoding: diagnostics, as: UTF8.self)) }
        let result = try Data(contentsOf: outputURL)
        guard process.terminationReason == .exit, process.terminationStatus == 0 else {
            log(String(decoding: result, as: UTF8.self))
            throw Failure(code, "\(executable) failed (\(process.terminationStatus))")
        }
        return result
    }

    func snapshotPackage() throws -> URL {
        let snapshot = workspace!.appendingPathComponent("update.dmg")
        let fd = open(args.package.path, O_RDONLY | O_NOFOLLOW | O_CLOEXEC)
        guard fd >= 0 else { throw Failure("integrity", "Cannot open downloaded image") }
        let input = FileHandle(fileDescriptor: fd, closeOnDealloc: true)
        defer { try? input.close() }
        var info = stat()
        guard fstat(fd, &info) == 0, info.st_mode & S_IFMT == S_IFREG, info.st_size == args.size else {
            throw Failure("integrity", "Downloaded image size or file type is invalid")
        }
        guard files.createFile(atPath: snapshot.path, contents: nil, attributes: [.posixPermissions: 0o600]) else {
            throw Failure("install", "Cannot create private image snapshot")
        }
        let output = try FileHandle(forWritingTo: snapshot)
        defer { try? output.close() }
        var hash = SHA256()
        var count: Int64 = 0
        while let chunk = try input.read(upToCount: 1024 * 1024), !chunk.isEmpty {
            count += Int64(chunk.count)
            guard count <= args.size else { throw Failure("integrity", "Image changed while reading") }
            hash.update(data: chunk)
            try output.write(contentsOf: chunk)
        }
        try output.synchronize()
        let digest = hash.finalize().map { String(format: "%02x", $0) }.joined()
        guard count == args.size, digest == args.sha256 else { throw Failure("integrity", "Image SHA256 does not match signed manifest") }
        // Copy only metadata from the same open descriptor: no path race, extra image copy,
        // or removal of the download's quarantine attribute.
        guard fcopyfile(fd, output.fileDescriptor, nil, copyfile_flags_t(COPYFILE_XATTR)) == 0 else {
            throw Failure("integrity", "Cannot preserve image extended attributes")
        }
        try output.synchronize()
        return snapshot
    }

    func detach() throws {
        guard mounted, let mount = mount else { return }
        var volume = statfs()
        if statfs(mount.path, &volume) == 0 {
            let mountedAt = withUnsafeBytes(of: &volume.f_mntonname) {
                String(cString: $0.baseAddress!.assumingMemoryBound(to: CChar.self))
            }
            if canonical(URL(fileURLWithPath: mountedAt)) != canonical(mount) {
                mounted = false
                return
            }
        } else if errno == ENOENT {
            mounted = false
            return
        }
        do {
            _ = try run("/usr/bin/hdiutil", ["detach", mount.path], timeout: 30, code: "install")
        } catch {
            log("Normal detach failed: \(error); retrying private read-only mount")
            _ = try run("/usr/bin/hdiutil", ["detach", "-force", mount.path], timeout: 30, code: "install")
        }
        mounted = false
    }

    func prepareImage() throws {
        let snapshot = try snapshotPackage()
        let mountURL = workspace!.appendingPathComponent("image")
        try files.createDirectory(at: mountURL, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
        mount = mountURL
        // Mark before attach: hdiutil may time out after the volume has already mounted.
        mounted = true
        _ = try run("/usr/bin/hdiutil", ["attach", "-readonly", "-nobrowse", "-noautoopen", "-mountpoint", mountURL.path, "-plist", snapshot.path], timeout: 120, code: "integrity")
        let children = try files.contentsOfDirectory(at: mountURL, includingPropertiesForKeys: nil)
        let candidates = children.filter { item in
            guard item.pathExtension.lowercased() == "app", let bundle = Bundle(url: item) else { return false }
            return bundle.bundleIdentifier == identity
        }
        guard candidates.count == 1 else { throw Failure("integrity", "Image must contain exactly one PKP application") }
        _ = try validateBundle(candidates[0], expectedVersion: args.version)
        let newApp = workspace!.appendingPathComponent("New.app")
        _ = try run("/usr/bin/ditto", ["--rsrc", "--extattr", "--qtn", candidates[0].path, newApp.path], timeout: 300, code: "integrity")
        _ = try validateBundle(newApp, expectedVersion: args.version)
        staged = newApp
        try detach()
        try files.removeItem(at: snapshot)
        let oldApp = workspace!.appendingPathComponent("Previous.app")
        _ = try run("/usr/bin/ditto", ["--rsrc", "--extattr", "--qtn", args.target.path, oldApp.path], timeout: 300, code: "install")
        _ = try validateBundle(oldApp, expectedVersion: oldVersion, code: "install")
        guard try signingIdentity(oldApp) == oldCodeHash else { throw Failure("installBusy", "Application changed while backing up") }
        backup = oldApp
    }

    func secureControlPath(_ url: URL) throws {
        let parent = url.deletingLastPathComponent().standardizedFileURL
        var metadata = stat()
        guard canonical(parent) == parent, stat(parent.path, &metadata) == 0,
              metadata.st_mode & S_IFMT == S_IFDIR, metadata.st_uid == getuid(),
              metadata.st_mode & 0o022 == 0, !contained(parent, in: args.target) else {
            throw Failure("install", "Unsafe control-file directory: \(parent.path)")
        }
    }

    func prepareLockAndReceipt() throws {
        try secureControlPath(args.lock)
        try secureControlPath(args.receipt)
        guard args.lock != args.receipt else { throw Failure("install", "Receipt and lock must be distinct") }
        lockFD = open(args.lock.path, O_RDWR | O_CREAT | O_NOFOLLOW | O_CLOEXEC, 0o600)
        var info = stat()
        guard lockFD >= 0, fstat(lockFD, &info) == 0, info.st_mode & S_IFMT == S_IFREG,
              info.st_uid == getuid(), info.st_nlink == 1, info.st_mode & 0o022 == 0 else {
            throw Failure("install", "Unsafe install lock")
        }
        // Probe receipt writability without publishing a success/failure receipt before commit.
        let probe = args.receipt.deletingLastPathComponent().appendingPathComponent(".receipt-\(UUID().uuidString)")
        try Data().write(to: probe, options: .withoutOverwriting)
        try files.removeItem(at: probe)
    }

    func requestAuthorizationIfNeeded() throws {
        guard access(args.target.deletingLastPathComponent().path, W_OK) != 0 ||
                access(args.target.path, W_OK) != 0 else { return }
        let result: Result<FileManager, Error>
        do {
            result = try awaitCallback(timeout: 180) { done in
                NSWorkspace.shared.requestAuthorization(to: .replaceFile) { authorization, error in
                    if let authorization = authorization {
                        done(.success(FileManager(authorization: authorization)))
                    } else {
                        done(.failure(error ?? Failure("installPermission", "Replace authorization cancelled")))
                    }
                }
            }
        } catch {
            throw Failure("installPermission", "Replace authorization did not complete: \(error)")
        }
        switch result {
        case .success(let manager): authorizedFiles = manager
        case .failure(let error): throw Failure("installPermission", "Replace authorization denied or cancelled: \(error)")
        }
    }

    func prepare() throws {
        try validateLocation()
        try makeWorkspace()
        try prepareLockAndReceipt()
        try checkNoOtherInstances()
        try prepareImage()
        try checkNoOtherInstances()
        try requestAuthorizationIfNeeded()
        guard parentAlive() else { throw Failure("install", "Original parent exited before preparation finished") }
        try checkNoOtherInstances()
        ready = true
        emit(["ready": true])
    }

    func waitForCommit() throws -> Bool {
        var line = [UInt8]()
        let deadline = Date(timeIntervalSinceNow: 1800)
        while Date() < deadline {
            var descriptor = pollfd(fd: STDIN_FILENO, events: Int16(POLLIN | POLLHUP), revents: 0)
            let status = poll(&descriptor, 1, 100)
            if status < 0 {
                if errno == EINTR { continue }
                throw Failure("install", "Cannot read parent command")
            }
            if status == 0 {
                if !parentAlive() { return false }
                continue
            }
            var buffer = [UInt8](repeating: 0, count: 64)
            let count = Darwin.read(STDIN_FILENO, &buffer, buffer.count)
            if count == 0 { return false }
            if count < 0 {
                if errno == EINTR { continue }
                throw Failure("install", "Cannot read parent command")
            }
            line.append(contentsOf: buffer.prefix(count))
            // Drain an already-written command before checking parent death: install + immediate
            // parent exit must not race the helper's read of the inherited pipe.
            if let newline = line.firstIndex(of: 10) {
                guard String(bytes: line[..<newline], encoding: .utf8) == "install" else { return false }
                committed = true
                return true
            }
            guard line.count <= 32, parentAlive() else { return false }
        }
        return false
    }

    func acquireExclusiveLock() throws {
        var current = stat(), opened = stat()
        guard lstat(args.lock.path, &current) == 0, fstat(lockFD, &opened) == 0,
              current.st_dev == opened.st_dev, current.st_ino == opened.st_ino,
              flock(lockFD, LOCK_EX | LOCK_NB) == 0 else {
            throw Failure("installBusy", "Install lock is held or has changed")
        }
        locked = true
    }

    func unlock() {
        if locked { _ = flock(lockFD, LOCK_UN); locked = false }
    }

    func receipt(error: String) throws {
        let data = try JSONSerialization.data(withJSONObject: ["version": args.version, "error": error], options: [.sortedKeys])
        let temporary = args.receipt.deletingLastPathComponent().appendingPathComponent(".receipt-\(UUID().uuidString)")
        defer { try? files.removeItem(at: temporary) }
        let fd = open(temporary.path, O_CREAT | O_EXCL | O_WRONLY | O_NOFOLLOW | O_CLOEXEC, 0o600)
        guard fd >= 0 else { throw Failure("install", "Cannot create update receipt") }
        let output = FileHandle(fileDescriptor: fd, closeOnDealloc: true)
        defer { try? output.close() }
        try output.write(contentsOf: data)
        try output.synchronize()
        guard rename(temporary.path, args.receipt.path) == 0 else { throw Failure("install", "Cannot publish update receipt") }
        let directory = open(args.receipt.deletingLastPathComponent().path, O_RDONLY | O_CLOEXEC)
        if directory >= 0 { _ = fsync(directory); close(directory) }
    }

    func launch() throws {
        let config = NSWorkspace.OpenConfiguration()
        config.activates = false
        // Errors are handled by rollback; this setting does not suppress Gatekeeper UI.
        config.promptsUserIfNeeded = false
        config.createsNewApplicationInstance = true
        if let profile = ProcessInfo.processInfo.environment["PKP_CONFIG_DIR"] {
            config.environment = ["PKP_CONFIG_DIR": profile]
        }
        let result: (NSRunningApplication?, Error?)
        do {
            result = try awaitCallback(timeout: 120) { done in
                NSWorkspace.shared.openApplication(at: args.target, configuration: config) { app, error in done((app, error)) }
            }
        } catch {
            // LaunchServices cannot cancel an in-flight open. Keep both versions rather than
            // rolling back underneath a new process that could start after this timeout.
            throw Failure("installRollback", "LaunchServices completion uncertain; backup retained: \(error)")
        }
        guard let app = result.0, result.1 == nil, !app.isTerminated,
              app.bundleURL.map(canonical) == args.target else {
            throw Failure("install", "Relaunch failed: \(String(describing: result.1))")
        }
    }

    func replace(with replacement: URL) throws {
        let manager = authorizedFiles ?? files
        _ = try manager.replaceItemAt(args.target, withItemAt: replacement, backupItemName: nil, options: [.usingNewMetadataOnly])
    }

    func rollback(_ originalError: Error) {
        log("Install failed, attempting rollback: \(originalError)")
        do {
            if !locked { try acquireExclusiveLock() }
            // A launch callback can time out after LaunchServices has started the new application.
            // Never replace a live application or force it to quit, including during rollback.
            guard try runningAtTarget().isEmpty else {
                throw Failure("installRollback", "An application is running at the target; refusing unsafe rollback")
            }
            if let original = oldCodeHash, (try? signingIdentity(args.target)) == original,
               (try? validateBundle(args.target, expectedVersion: oldVersion, code: "installRollback")) != nil {
                try receipt(error: "install")
                unlock()
                try launch()
                log("Replacement failed without changing the original; original relaunched")
                return
            }
            guard let backup = backup, files.fileExists(atPath: backup.path) else { throw Failure("installRollback", "Private backup is missing") }
            // Keep the independent backup even if an authorized restore consumes its input.
            let restore = workspace!.appendingPathComponent("Restore.app")
            _ = try run("/usr/bin/ditto", ["--rsrc", "--extattr", "--qtn", backup.path, restore.path], timeout: 300, code: "installRollback")
            _ = try validateBundle(restore, expectedVersion: oldVersion, code: "installRollback")
            guard files.fileExists(atPath: args.target.path) else {
                // replaceItemAt normally preserves the old item on failure. Never use a privileged
                // move/shell fallback if a broken filesystem left the target unexpectedly missing.
                throw Failure("installRollback", "Target disappeared during replacement; backup retained for recovery")
            }
            try replace(with: restore)
            _ = try validateBundle(args.target, expectedVersion: oldVersion, code: "installRollback")
            try receipt(error: "install")
            unlock()
            try launch()
            log("Previous application restored and relaunched")
        } catch {
            preserveBackup = true
            log("Rollback failed; KEEP backup at \(backup?.path ?? "unknown"): \(error)")
            do { try receipt(error: "installRollback") } catch { log("Cannot record rollback failure: \(error)") }
        }
    }

    func install() throws {
        let deadline = Date(timeIntervalSinceNow: 120)
        while parentAlive() && Date() < deadline {
            RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.1))
        }
        guard !parentAlive() else { throw Failure("install", "Original parent did not exit; no files were replaced") }
        try acquireExclusiveLock()
        try checkNoOtherInstances()
        try validateLocation()
        _ = try validateBundle(staged!, expectedVersion: args.version)
        replacementAttempted = true
        do {
            try replace(with: staged!)
            _ = try validateBundle(args.target, expectedVersion: args.version)
            try receipt(error: "")
            // New launches acquire a shared lock, so release only immediately before LaunchServices.
            unlock()
            try launch()
            log("Installed and relaunched \(args.version)")
        } catch {
            if (error as? Failure)?.code == "installRollback" {
                preserveBackup = true
                log("Relaunch outcome uncertain; KEEP backup at \(backup?.path ?? "unknown")")
                do { try receipt(error: "installRollback") } catch { log("Cannot record launch failure: \(error)") }
            } else {
                rollback(error)
            }
            throw Failure(preserveBackup ? "installRollback" : "install", "Install transaction failed: \(error)")
        }
    }

    func resumeUnchanged() {
        guard !parentAlive() else { return }
        unlock()
        do {
            guard try runningAtTarget().isEmpty else {
                log("Unchanged application still has another instance; not launching a duplicate")
                return
            }
            if lockFD >= 0 {
                guard flock(lockFD, LOCK_SH | LOCK_NB) == 0 else {
                    log("Another installer owns the lock; not relaunching during its transaction")
                    return
                }
                locked = true
            }
            _ = try validateBundle(args.target, expectedVersion: oldVersion.isEmpty ? nil : oldVersion, code: "install")
            guard try signingIdentity(args.target) == oldCodeHash else {
                throw Failure("installRollback", "Original application changed; refusing to launch an unexpected replacement")
            }
            unlock()
            try launch()
            log("Relaunched unchanged application after pre-replacement failure")
        } catch {
            unlock()
            preserveBackup = backup != nil
            log("Could not relaunch unchanged application: \(error)")
            do { try receipt(error: "installRollback") } catch { log("Cannot record relaunch failure: \(error)") }
        }
    }

    func cleanup() {
        unlock()
        if lockFD >= 0 { close(lockFD); lockFD = -1 }
        if mounted {
            do { try detach() } catch {
                preserveBackup = true
                log("Cannot detach private image; retaining workspace: \(error)")
            }
        }
        guard !preserveBackup else { return }
        if let workspace = workspace {
            do { try files.removeItem(at: workspace) } catch { log("Cannot remove staging directory: \(error)") }
        }
        if let helperDirectory = helperDirectory {
            do { try files.removeItem(at: helperDirectory) } catch { log("Cannot remove helper workspace: \(error)") }
        }
    }
}

private var installer: Installer?
var exitCode: Int32 = 0
signal(SIGPIPE, SIG_IGN)
do {
    let args = try Arguments()
    let transaction = try Installer(args)
    installer = transaction
    try transaction.prepare()
    if try transaction.waitForCommit() { try transaction.install() }
    else { log("Update cancelled before commit; installed application untouched") }
} catch {
    let failure = error as? Failure ?? Failure("install", String(describing: error))
    log("\(failure.code): \(failure.detail)")
    if installer?.ready != true { emit(["error": failure.code]) }
    if let transaction = installer, transaction.committed, !transaction.replacementAttempted {
        do { try transaction.receipt(error: failure.code) } catch { log("Cannot record failure: \(error)") }
        transaction.resumeUnchanged()
    }
    exitCode = 1
}
installer?.cleanup()
exit(exitCode)
