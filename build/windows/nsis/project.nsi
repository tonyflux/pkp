Unicode true
# Two libmpv-2.dll (one per architecture) make up most of the installer: solid LZMA keeps it
# about a quarter smaller than the default zlib, at about a minute more to build.
SetCompressor /SOLID lzma

####
## Please note: Template replacements don't work in this file. They are provided with default defines like
## mentioned underneath.
## If the keyword is not defined, "wails_tools.nsh" will populate them.
## If they are defined here, "wails_tools.nsh" will not touch them. This allows you to use this project.nsi manually
## from outside of Wails for debugging and development of the installer.
## 
## For development first make a wails nsis build to populate the "wails_tools.nsh":
## > wails build --target windows/amd64 --nsis
## Then you can call makensis on this file with specifying the path to your binary:
## For a AMD64 only installer:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app.exe
## For a ARM64 only installer:
## > makensis -DARG_WAILS_ARM64_BINARY=..\..\bin\app.exe
## For a installer with both architectures:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app-amd64.exe -DARG_WAILS_ARM64_BINARY=..\..\bin\app-arm64.exe
####
## The following information is taken from the wails_tools.nsh file, but they can be overwritten here.
####
## !define INFO_PROJECTNAME    "my-project" # Default "pkp"
## !define INFO_COMPANYNAME    "My Company" # Default "PKP"
## !define INFO_PRODUCTNAME    "My Product Name" # Default "PKP"
## !define INFO_PRODUCTVERSION "1.0.2"     # Default "1.0.2"
## !define INFO_COPYRIGHT      "(c) Now, My Company" # Default "© 2026, My Company"
###
## !define PRODUCT_EXECUTABLE  "Application.exe"      # Default "${INFO_PROJECTNAME}.exe"
## !define UNINST_KEY_NAME     "UninstKeyInRegistry"  # Default "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
####
## !define REQUEST_EXECUTION_LEVEL "admin"            # Default "admin"  see also https://nsis.sourceforge.io/Docs/Chapter4.html
## !define WAILS_INSTALL_SCOPE     "user"             # Default "machine" - set to "user" for per-user install ($LOCALAPPDATA) without UAC prompt
####
## Include the wails tools
####
!include "wails_tools.nsh"

Var stage
Var previous
Var updatePID
Var installerMutex
Var installingNew
Var inAppUpdate

!macro pkp.message FLAGS TEXT
    ${If} $inAppUpdate == 1
        ${If} ${Silent}
            SetSilent normal
            MessageBox ${FLAGS} "${TEXT}"
            SetSilent silent
        ${Else}
            MessageBox ${FLAGS} "${TEXT}"
        ${EndIf}
    ${Else}
        MessageBox ${FLAGS} "${TEXT}" /SD IDOK
    ${EndIf}
!macroend

!macro pkp.backup ENTRY
    IfFileExists "$INSTDIR\${ENTRY}" 0 +4
    ClearErrors
    Rename "$INSTDIR\${ENTRY}" "$previous\${ENTRY}"
    IfErrors rollback
!macroend

!macro pkp.activate ENTRY
    ClearErrors
    Rename "$stage\${ENTRY}" "$INSTDIR\${ENTRY}"
    IfErrors rollback
!macroend

!macro pkp.restore ENTRY
    IfFileExists "$previous\${ENTRY}" 0 +6
    Delete "$INSTDIR\${ENTRY}"
    RMDir /r "$INSTDIR\${ENTRY}"
    ClearErrors
    Rename "$previous\${ENTRY}" "$INSTDIR\${ENTRY}"
    IfErrors recoveryFailed
!macroend

# v0.6.0 did not ship this DLL: recovery must remove a newly activated
# helper too when there is no previous helper to restore.
!macro pkp.restorePCM
    ${If} ${FileExists} "$previous\.pkp-pcm-absent"
        ${If} ${FileExists} "$INSTDIR\pkp-pcm.dll"
            ClearErrors
            Delete "$INSTDIR\pkp-pcm.dll"
            IfErrors recoveryFailed
        ${EndIf}
    ${EndIf}
    !insertmacro pkp.restore "pkp-pcm.dll"
!macroend

## Runtime DLLs and LAN media tools, picked like the program for its native
## architecture, plus all bundled-library licenses. `make windows-installer`
## passes ARG_LIBMPV_*, ARG_PCM_*, ARG_FFMPEG_* and ARG_FFPROBE_* paths.
!macro pkp.libmpv
    !ifdef SUPPORTS_AMD64
        ${if} ${IsNativeAMD64}
            File "/oname=libmpv-2.dll" "${ARG_LIBMPV_AMD64}"
            File "/oname=pkp-pcm.dll" "${ARG_PCM_AMD64}"
            File "/oname=ffmpeg.exe" "${ARG_FFMPEG_AMD64}"
            File "/oname=ffprobe.exe" "${ARG_FFPROBE_AMD64}"
        ${EndIf}
    !endif

    !ifdef SUPPORTS_ARM64
        ${if} ${IsNativeARM64}
            File "/oname=libmpv-2.dll" "${ARG_LIBMPV_ARM64}"
            File "/oname=pkp-pcm.dll" "${ARG_PCM_ARM64}"
            File "/oname=ffmpeg.exe" "${ARG_FFMPEG_ARM64}"
            File "/oname=ffprobe.exe" "${ARG_FFPROBE_ARM64}"
        ${EndIf}
    !endif

    SetOutPath "$stage\licenses"
    File /r "${ARG_LIBMPV_LICENSES}\*"
    File /oname=GO_LICENSES.txt "${ARG_GO_LICENSES}"
    File /oname=THIRD_PARTY_LICENSES.txt "${ARG_FRONTEND_LICENSES}"
    SetOutPath $stage
!macroend

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
# !define MUI_WELCOMEFINISHPAGE_BITMAP "resources\leftimage.bmp" #Include this to add a bitmap on the left side of the Welcome Page. Must be a size of 164x314
!define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
# !insertmacro MUI_PAGE_LICENSE "resources\eula.txt" # Adds a EULA page to the installer
!insertmacro MUI_PAGE_DIRECTORY # In which folder install page.
!insertmacro MUI_PAGE_INSTFILES # Installing page.
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uninstalling page

# The first language is the fallback; NSIS otherwise follows the system UI language.
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

LangString PKPBusy ${LANG_SIMPCHINESE} "PKP 或另一个安装程序正在运行。请先退出所有 PKP 窗口和托盘程序再试。"
LangString PKPBusy ${LANG_ENGLISH} "PKP or another installer is still running. Quit all PKP windows and the system tray app, then try again."
LangString PKPWait ${LANG_SIMPCHINESE} "PKP 未能在 30 秒内退出，安装已中止。请手动退出后重新运行安装程序。"
LangString PKPWait ${LANG_ENGLISH} "PKP didn't exit within 30 seconds. Installation stopped. Quit PKP manually and run the installer again."
LangString PKPFailed ${LANG_SIMPCHINESE} "安装未完成，已取消升级并尝试恢复旧版本。请检查磁盘空间与文件夹权限后重试。"
LangString PKPFailed ${LANG_ENGLISH} "Installation failed. The previous version was restored if available. Check disk space and folder permissions, then try again."
LangString PKPRecovery ${LANG_SIMPCHINESE} "未能自动恢复旧版本，备份文件保留在安装目录的 .pkp-previous 中；请退出 PKP 后重新安装。"
LangString PKPRecovery ${LANG_ENGLISH} "Couldn't restore the previous version automatically. Backups remain in .pkp-previous in the install folder; quit PKP and run the installer again."
LangString PKPLocation ${LANG_SIMPCHINESE} "安装位置已更改或无法确定。请手动运行安装程序。"
LangString PKPLocation ${LANG_ENGLISH} "The installation location changed or could not be determined. Run the installer manually."

## The following two statements can be used to sign the installer and the uninstaller. The path to the binaries are provided in %1
#!uninstfinalize 'signtool --file "%1"'
#!finalize 'signtool --file "%1"'

Name "${INFO_PRODUCTNAME}"
!if "${WAILS_INSTALL_SCOPE}" == "user"
    OutFile "..\..\..\bin\${INFO_PROJECTNAME}-${ARCH}-user-installer.exe"
!else
    OutFile "..\..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe"
!endif
!if "${WAILS_INSTALL_SCOPE}" == "user"
    InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
!else
    InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
!endif
ShowInstDetails show # This will always show the installation details.

Function .onInit
    ${GetParameters} $0
    ClearErrors
    ${GetOptions} $0 "/PKPUPDATE=" $1
    ${If} $1 == "1"
        StrCpy $inAppUpdate 1
    ${EndIf}
    !insertmacro wails.checkArchitecture
    System::Call 'kernel32::CreateMutexW(p 0, i 0, w "Global\PKP.InstallerRunning") p .r0 ?e'
    Pop $1
    StrCpy $installerMutex $0
    ${If} $0 == 0
        !insertmacro pkp.message MB_OK|MB_ICONEXCLAMATION "$(PKPBusy)"
        SetErrorLevel 2
        Abort
    ${ElseIf} $1 == 183
        !insertmacro pkp.message MB_OK|MB_ICONEXCLAMATION "$(PKPBusy)"
        SetErrorLevel 71
        Abort
    ${EndIf}
    SetRegView 64
    !if "${WAILS_INSTALL_SCOPE}" == "user"
        ReadRegStr $0 HKCU "${UNINST_KEY}" "InstallLocation"
        ReadRegStr $1 HKCU "${UNINST_KEY}" "UninstallString"
    !else
        ReadRegStr $0 HKLM "${UNINST_KEY}" "InstallLocation"
        ReadRegStr $1 HKLM "${UNINST_KEY}" "UninstallString"
    !endif
    ${If} $0 == ""
    ${AndIf} $1 != ""
        # Earlier installers recorded only a quoted UninstallString.
        StrCpy $2 $1 1
        ${If} $2 == '$\"'
            StrCpy $2 1
            loopUninstallQuote:
                StrCpy $3 $1 1 $2
                ${If} $3 == '$\"'
                    IntOp $2 $2 - 1
                    StrCpy $1 $1 $2 1
                    ${GetParent} "$1" $0
                ${ElseIf} $3 != ""
                    IntOp $2 $2 + 1
                    Goto loopUninstallQuote
                ${EndIf}
        ${EndIf}
    ${EndIf}
    ${If} $inAppUpdate == 1
        ${GetParameters} $2
        ClearErrors
        ${GetOptions} $2 "/EXPECTDIR=" $3
        ${If} ${Errors}
        ${OrIf} $3 == ""
        ${OrIf} $0 == ""
            Goto locationChanged
        ${EndIf}
        GetFullPathName $2 "$0"
        GetFullPathName $3 "$3"
        # Ignore a trailing separator on either spelling of the same directory.
        StrCpy $4 $2 1 -1
        ${If} $4 == "\"
            StrCpy $2 $2 -1
        ${EndIf}
        StrCpy $4 $3 1 -1
        ${If} $4 == "\"
            StrCpy $3 $3 -1
        ${EndIf}
        ${If} $2 == ""
        ${OrIf} $2 != $3
            Goto locationChanged
        ${EndIf}
    ${EndIf}
    ${If} $0 != ""
        StrCpy $INSTDIR $0
    ${EndIf}
    ${GetParameters} $0
    ClearErrors
    ${GetOptions} $0 "/WAITPID=" $updatePID
    ${IfNot} ${Errors}
        System::Call 'kernel32::OpenProcess(i 0x100000, i 0, i $updatePID) p .r0'
        ${If} $0 != 0
            System::Call 'kernel32::WaitForSingleObject(p r0, i 30000) i .r1'
            System::Call 'kernel32::CloseHandle(p r0)'
            ${If} $1 != 0
                !insertmacro pkp.message MB_OK|MB_ICONEXCLAMATION "$(PKPWait)"
                SetErrorLevel 71
                Abort
            ${EndIf}
        ${EndIf}
    ${EndIf}
    Call pkp.checkRunning
    Return
locationChanged:
    !insertmacro pkp.message MB_OK|MB_ICONSTOP "$(PKPLocation)"
    SetErrorLevel 73
    Abort
FunctionEnd

Function pkp.checkRunning
    System::Call 'kernel32::OpenMutexW(i 0x100000, i 0, w "Global\PKP.ApplicationRunning") p .r0 ?e'
    Pop $1
    ${If} $0 != 0
        System::Call 'kernel32::CloseHandle(p r0)'
    ${EndIf}
    ${If} $0 != 0
    ${OrIf} $1 != 2
        !insertmacro pkp.message MB_OK|MB_ICONEXCLAMATION "$(PKPBusy)"
        SetErrorLevel 71
        Abort
    ${EndIf}
    # Pre-updater PKP versions do not own the named object. Include those too.
    System::Call 'kernel32::CreateToolhelp32Snapshot(i 2, i 0) p .r0'
    ${If} $0 == -1
        SetErrorLevel 2
        Abort
    ${EndIf}
    System::Alloc 556
    Pop $1
    System::Call '*$1(i 556)'
    System::Call 'kernel32::Process32FirstW(p r0, p r1) i .r2'
scanProcess:
    ${If} $2 != 0
        System::Call '*$1(i, i, i, p, i, i, i, i, i, &w260 .r3)'
        ${If} $3 == "${PRODUCT_EXECUTABLE}"
            System::Free $1
            System::Call 'kernel32::CloseHandle(p r0)'
            !insertmacro pkp.message MB_OK|MB_ICONEXCLAMATION "$(PKPBusy)"
            SetErrorLevel 71
            Abort
        ${EndIf}
        System::Call 'kernel32::Process32NextW(p r0, p r1) i .r2'
        Goto scanProcess
    ${EndIf}
    System::Free $1
    System::Call 'kernel32::CloseHandle(p r0)'
FunctionEnd

Section
    !insertmacro wails.setShellContext
    !ifndef PKP_TEST_NO_INTEGRATION
        !insertmacro wails.webview2runtime
    !endif
    Call pkp.checkRunning
    StrCpy $stage "$INSTDIR\.pkp-update"
    StrCpy $previous "$INSTDIR\.pkp-previous"
    StrCpy $installingNew 0
    # Recover a power-loss/interrupted transaction before touching its backups.
    !insertmacro pkp.restore "${PRODUCT_EXECUTABLE}"
    !insertmacro pkp.restore "libmpv-2.dll"
    !insertmacro pkp.restorePCM
    !insertmacro pkp.restore "ffmpeg.exe"
    !insertmacro pkp.restore "ffprobe.exe"
    !insertmacro pkp.restore "licenses"
    !insertmacro pkp.restore "LICENSE.txt"
    !insertmacro pkp.restore "uninstall.exe"
    RMDir /r "$previous"
    RMDir /r "$stage"
    SetOverwrite on
    ClearErrors
    SetOutPath $stage
    !insertmacro wails.files
    !insertmacro pkp.libmpv
    File "/oname=LICENSE.txt" "..\..\..\LICENSE"
    WriteUninstaller "$stage\uninstall.exe"
    IfErrors rollback
    CreateDirectory "$previous"
    IfErrors rollback
    ${IfNot} ${FileExists} "$INSTDIR\pkp-pcm.dll"
        ClearErrors
        FileOpen $0 "$previous\.pkp-pcm-absent" w
        IfErrors rollback
        FileClose $0
    ${EndIf}
    Call pkp.checkRunning
    !insertmacro pkp.backup "${PRODUCT_EXECUTABLE}"
    !insertmacro pkp.backup "libmpv-2.dll"
    !insertmacro pkp.backup "pkp-pcm.dll"
    !insertmacro pkp.backup "ffmpeg.exe"
    !insertmacro pkp.backup "ffprobe.exe"
    !insertmacro pkp.backup "licenses"
    !insertmacro pkp.backup "LICENSE.txt"
    !insertmacro pkp.backup "uninstall.exe"
    StrCpy $installingNew 1
    !insertmacro pkp.activate "${PRODUCT_EXECUTABLE}"
    !insertmacro pkp.activate "libmpv-2.dll"
    !insertmacro pkp.activate "pkp-pcm.dll"
    !insertmacro pkp.activate "ffmpeg.exe"
    !insertmacro pkp.activate "ffprobe.exe"
    !insertmacro pkp.activate "licenses"
    !insertmacro pkp.activate "LICENSE.txt"
    !insertmacro pkp.activate "uninstall.exe"
    SetOutPath $INSTDIR
    ClearErrors
    !ifndef PKP_TEST_NO_INTEGRATION
        CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
        CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
        !insertmacro wails.associateFiles
        !insertmacro wails.associateCustomProtocols
    !endif
    !insertmacro wails.writeUninstaller
    !if "${WAILS_INSTALL_SCOPE}" == "user"
        WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
    !else
        WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
    !endif
    IfErrors rollback
    RMDir /r "$previous"
    RMDir /r "$stage"
    Goto complete

rollback:
    # During backup, restore only moved entries; unmoved originals remain intact.
    SetOutPath $INSTDIR
    ${If} $installingNew == 1
        Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
        Delete "$INSTDIR\libmpv-2.dll"
        Delete "$INSTDIR\pkp-pcm.dll"
        Delete "$INSTDIR\ffmpeg.exe"
        Delete "$INSTDIR\ffprobe.exe"
        Delete "$INSTDIR\LICENSE.txt"
        Delete "$INSTDIR\uninstall.exe"
        RMDir /r "$INSTDIR\licenses"
    ${EndIf}
    !insertmacro pkp.restore "${PRODUCT_EXECUTABLE}"
    !insertmacro pkp.restore "libmpv-2.dll"
    !insertmacro pkp.restorePCM
    !insertmacro pkp.restore "ffmpeg.exe"
    !insertmacro pkp.restore "ffprobe.exe"
    !insertmacro pkp.restore "licenses"
    !insertmacro pkp.restore "LICENSE.txt"
    !insertmacro pkp.restore "uninstall.exe"
    RMDir /r "$previous"
    RMDir /r "$stage"
    !insertmacro pkp.message MB_OK|MB_ICONSTOP "$(PKPFailed)"
    SetErrorLevel 2
    Abort

recoveryFailed:
    !insertmacro pkp.message MB_OK|MB_ICONSTOP "$(PKPRecovery)"
    SetErrorLevel 72
    Abort
complete:
SectionEnd

Section "uninstall" 
    !insertmacro wails.setShellContext

    !ifndef PKP_TEST_NO_INTEGRATION
        RMDir /r "$AppData\${PRODUCT_EXECUTABLE}" # Remove the WebView2 DataPath
    !endif
    Delete "$INSTDIR\pkp-pcm.dll"

    RMDir /r $INSTDIR

    !ifndef PKP_TEST_NO_INTEGRATION
        Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
        Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
        !insertmacro wails.unassociateFiles
        !insertmacro wails.unassociateCustomProtocols
    !endif

    !insertmacro wails.deleteUninstaller
SectionEnd
