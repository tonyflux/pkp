$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# NSIS displays real errors even for in-app silent updates. Dismiss only windows
# belonging to the child installer, rather than changing its error handling.
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class InstallerWindows {
    public delegate bool Callback(IntPtr window, IntPtr state);
    [DllImport("user32.dll")] public static extern bool EnumWindows(Callback callback, IntPtr state);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr window);
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
    [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window, uint message, IntPtr wParam, IntPtr lParam);
    public static void CloseDialogs(uint processId) {
        EnumWindows((window, state) => {
            uint owner;
            GetWindowThreadProcessId(window, out owner);
            if (owner == processId && IsWindowVisible(window))
                PostMessage(window, 0x10, IntPtr.Zero, IntPtr.Zero);
            return true;
        }, IntPtr.Zero);
    }
}
'@

$root = Join-Path ([IO.Path]::GetTempPath()) ("pkp-update-e2e-" + [guid]::NewGuid().ToString('N'))
$keyName = 'PKP-Update-E2E-' + [guid]::NewGuid().ToString('N')
$keyPath = "Software\Microsoft\Windows\CurrentVersion\Uninstall\$keyName"
$registry = [Microsoft.Win32.RegistryKey]::OpenBaseKey('CurrentUser', 'Registry64')
$oldHome = $env:HOME
$oldLocal = $env:LOCALAPPDATA
$mutex = $null
$lock = $null

function Assert-True([bool] $condition, [string] $message) {
    if (-not $condition) { throw $message }
}
function Invoke-Installer([string] $installer, [string] $arguments) {
    $info = [Diagnostics.ProcessStartInfo]::new()
    $info.FileName = $installer
    $info.Arguments = $arguments
    $info.UseShellExecute = $false
    $process = [Diagnostics.Process]::Start($info)
    try {
        $deadline = [DateTime]::UtcNow.AddSeconds(60)
        while (-not $process.WaitForExit(200)) {
            [InstallerWindows]::CloseDialogs([uint32]$process.Id)
            if ([DateTime]::UtcNow -gt $deadline) {
                $process.Kill()
                throw "Installer timed out: $arguments"
            }
        }
        return $process.ExitCode
    } finally { $process.Dispose() }
}
function Assert-Content([string] $path, [string] $expected) {
    Assert-True ((Get-Content -LiteralPath $path -Raw).Trim() -eq $expected) "Unexpected content: $path"
}
function Set-InstallLocation([string] $location, [bool] $legacy = $false) {
    $key = $registry.CreateSubKey($keyPath)
    try {
        if ($legacy) {
            $key.DeleteValue('InstallLocation', $false)
            $key.SetValue('UninstallString', "`"$location\uninstall.exe`"")
        } else { $key.SetValue('InstallLocation', $location) }
    } finally { $key.Dispose() }
}

try {
    $source = Join-Path $PSScriptRoot 'nsis'
    $work = Join-Path $root 'work'
    New-Item -ItemType Directory -Force "$work\build\windows\nsis", "$work\bin", "$root\home", "$root\local", "$root\payload\licenses" | Out-Null
    Copy-Item "$source\*" "$work\build\windows\nsis" -Recurse
    Copy-Item (Join-Path $PSScriptRoot 'icon.ico') "$work\build\windows\icon.ico"
    Copy-Item (Join-Path $PSScriptRoot '..\..\LICENSE') "$work\LICENSE"
    $env:HOME = "$root\home"
    $env:LOCALAPPDATA = "$root\local"
    $payload = "$root\payload\file.txt"
    $license = "$root\payload\licenses\license.txt"
    Set-Content -LiteralPath $license -Value 'test license'
    $arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'ARM64' } else { 'AMD64' }
    $compiler = (Get-Command makensis.exe -ErrorAction Stop).Source
    $product = 'PKP-E2E-' + [guid]::NewGuid().ToString('N')
    $output = "$work\bin\$product-$($arch.ToLowerInvariant())-user-installer.exe"
    $defines = @(
        '-DWAILS_INSTALL_SCOPE=user', '-DPKP_TEST_NO_INTEGRATION', "-DUNINST_KEY_NAME=$keyName",
        "-DINFO_PROJECTNAME=$product", "-DINFO_PRODUCTNAME=$product", "-DARG_WAILS_${arch}_BINARY=$payload",
        "-DARG_LIBMPV_$arch=$payload", "-DARG_PCM_$arch=$payload", "-DARG_FFMPEG_$arch=$payload", "-DARG_FFPROBE_$arch=$payload",
        "-DARG_LIBMPV_LICENSES=$root\payload\licenses", "-DARG_GO_LICENSES=$license", "-DARG_FRONTEND_LICENSES=$license"
    )
    Push-Location "$work\build\windows\nsis"
    try {
        Set-Content -LiteralPath $payload -Value 'version-one'
        & $compiler @defines 'project.nsi'
        if ($LASTEXITCODE -ne 0) { throw "makensis failed: $LASTEXITCODE" }
        $v1 = "$root\v1.exe"
        Copy-Item $output $v1
        Set-Content -LiteralPath $payload -Value 'version-two'
        & $compiler @defines 'project.nsi'
        if ($LASTEXITCODE -ne 0) { throw "makensis failed: $LASTEXITCODE" }
        $v2 = "$root\v2.exe"
        Copy-Item $output $v2
    } finally { Pop-Location }

    # Match the updater's GENERIC_READ / FILE_SHARE_READ package handle.
    $lockedPackage = "$root\locked-package.exe"
    $replacement = "$root\replacement.exe"
    Copy-Item $v1 $lockedPackage
    Copy-Item $v2 $replacement
    $lock = [IO.File]::Open($lockedPackage, 'Open', 'Read', 'Read')
    $writeBlocked = $false
    try {
        $writer = [IO.File]::Open($lockedPackage, 'Open', 'Write', 'ReadWrite')
        $writer.Dispose()
    } catch [IO.IOException] { $writeBlocked = $true }
    Assert-True $writeBlocked 'FILE_SHARE_READ did not block package writes'
    $replaceBlocked = $false
    try { [IO.File]::Replace($replacement, $lockedPackage, $null) } catch [IO.IOException] { $replaceBlocked = $true }
    Assert-True $replaceBlocked 'FILE_SHARE_READ did not block package replacement'
    $lock.Dispose(); $lock = $null
    $writer = [IO.File]::Open($lockedPackage, 'Open', 'Write', 'ReadWrite')
    $writer.Dispose()
    [IO.File]::Replace($replacement, $lockedPackage, $null)


    $install = "$root\Custom install with spaces"
    $exe = "$install\$product.exe"
    Assert-True ((Invoke-Installer $v1 "/S /D=$install") -eq 0) 'Initial install failed'
    Assert-Content $exe 'version-one'
    $key = $registry.OpenSubKey($keyPath)
    try { Assert-True ($key.GetValue('InstallLocation') -eq $install) 'Initial location not registered' } finally { $key.Dispose() }
    $update = "/S /PKPUPDATE=1 /WAITPID=0 /EXPECTDIR=`"$install`""
    Assert-True ((Invoke-Installer $v2 $update) -eq 0) 'In-place update failed'
    foreach ($file in @("$product.exe", 'libmpv-2.dll', 'pkp-pcm.dll', 'ffmpeg.exe', 'ffprobe.exe')) {
        Assert-Content (Join-Path $install $file) 'version-two'
    }

    $other = "$root\wrong location"
    New-Item -ItemType Directory $other | Out-Null
    Set-Content "$other\sentinel.txt" 'untouched'
    $code = Invoke-Installer $v1 "/S /PKPUPDATE=1 /WAITPID=0 /EXPECTDIR=`"$other`""
    Assert-True ($code -eq 73) "Location mismatch returned $code, expected 73"
    Assert-Content $exe 'version-two'
    Assert-Content "$other\sentinel.txt" 'untouched'
    Assert-True (-not (Test-Path "$other\$product.exe")) 'Mismatch wrote to other directory'

    $created = $false
    $mutex = [Threading.Mutex]::new($false, 'Global\PKP.ApplicationRunning', [ref]$created)
    Assert-True $created 'Application mutex already exists; cannot safely run isolated test'
    $code = Invoke-Installer $v1 $update
    Assert-True ($code -eq 71) "Busy update returned $code, expected 71"
    Assert-Content $exe 'version-two'
    $mutex.Dispose(); $mutex = $null

    # Lock a later backup entry so earlier entries must be restored after failure.
    $lock = [IO.File]::Open("$install\ffmpeg.exe", 'Open', 'Read', 'None')
    $code = Invoke-Installer $v1 $update
    Assert-True ($code -eq 2) "Locked-file update returned $code, expected general failure code 2"
    $lock.Dispose(); $lock = $null
    Assert-Content $exe 'version-two'
    Assert-Content "$install\libmpv-2.dll" 'version-two'
    Assert-Content "$install\ffmpeg.exe" 'version-two'

    # An interrupted transaction with a locked backup cannot be recovered.
    New-Item -ItemType Directory -Force "$install\.pkp-previous" | Out-Null
    Set-Content "$install\.pkp-previous\$product.exe" 'recovery-copy'
    $lock = [IO.File]::Open("$install\.pkp-previous\$product.exe", 'Open', 'Read', 'None')
    $code = Invoke-Installer $v1 $update
    Assert-True ($code -eq 72) "Failed recovery returned $code, expected 72"
    $lock.Dispose(); $lock = $null
    Assert-Content "$install\.pkp-previous\$product.exe" 'recovery-copy'
    Remove-Item "$install\.pkp-previous" -Recurse -Force

    # Old installers stored only the quoted uninstaller path; ordinary silent
    # wizard invocation remains supported without the new protocol parameters.
    Set-InstallLocation $install $true
    Assert-True ((Invoke-Installer $v1 '/S') -eq 0) 'Legacy uninstall-key upgrade failed'
    Assert-Content $exe 'version-one'
    Write-Host 'Windows installer update scenarios passed. Real UAC consent/elevation is not simulated by this user-scope test.'
} finally {
    if ($null -ne $lock) { $lock.Dispose() }
    if ($null -ne $mutex) { $mutex.Dispose() }
    $registry.DeleteSubKeyTree($keyPath, $false)
    $registry.Dispose()
    $env:HOME = $oldHome
    $env:LOCALAPPDATA = $oldLocal
    Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
}
