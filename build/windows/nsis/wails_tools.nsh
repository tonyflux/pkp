# Wails build helpers with PKP's non-default, per-user association registration.

!include "x64.nsh"
!include "WinVer.nsh"
!include "FileFunc.nsh"

!ifndef INFO_PROJECTNAME
    !define INFO_PROJECTNAME "pkp"
!endif
!ifndef INFO_COMPANYNAME
    !define INFO_COMPANYNAME "PKP"
!endif
!ifndef INFO_PRODUCTNAME
    !define INFO_PRODUCTNAME "PKP"
!endif
!ifndef INFO_PRODUCTVERSION
    !define INFO_PRODUCTVERSION "0.3.0"
!endif
!ifndef INFO_COPYRIGHT
    !define INFO_COPYRIGHT "Copyright (C) 2026 tonyflux"
!endif
!ifndef PRODUCT_EXECUTABLE
    !define PRODUCT_EXECUTABLE "${INFO_PROJECTNAME}.exe"
!endif
!ifndef UNINST_KEY_NAME
    !define UNINST_KEY_NAME "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
!endif
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${UNINST_KEY_NAME}"

!ifndef WAILS_INSTALL_SCOPE
    !define WAILS_INSTALL_SCOPE "machine"
!endif

!ifndef REQUEST_EXECUTION_LEVEL
    !if "${WAILS_INSTALL_SCOPE}" == "user"
        !define REQUEST_EXECUTION_LEVEL "user"
    !else
        !define REQUEST_EXECUTION_LEVEL "admin"
    !endif
!endif

RequestExecutionLevel "${REQUEST_EXECUTION_LEVEL}"

!ifdef ARG_WAILS_AMD64_BINARY
    !define SUPPORTS_AMD64
!endif

!ifdef ARG_WAILS_ARM64_BINARY
    !define SUPPORTS_ARM64
!endif

!ifdef SUPPORTS_AMD64
    !ifdef SUPPORTS_ARM64
        !define ARCH "amd64_arm64"
    !else
        !define ARCH "amd64"
    !endif
!else
    !ifdef SUPPORTS_ARM64
        !define ARCH "arm64"
    !else
        !error "Wails: Undefined ARCH, please provide at least one of ARG_WAILS_AMD64_BINARY or ARG_WAILS_ARM64_BINARY"
    !endif
!endif

!macro wails.checkArchitecture
    !ifndef WAILS_WIN10_REQUIRED
        !define WAILS_WIN10_REQUIRED "This product is only supported on Windows 10 (Server 2016) and later."
    !endif

    !ifndef WAILS_ARCHITECTURE_NOT_SUPPORTED
        !define WAILS_ARCHITECTURE_NOT_SUPPORTED "This product can't be installed on the current Windows architecture. Supports: ${ARCH}"
    !endif

    ${If} ${AtLeastWin10}
        !ifdef SUPPORTS_AMD64
            ${if} ${IsNativeAMD64}
                Goto ok
            ${EndIf}
        !endif

        !ifdef SUPPORTS_ARM64
            ${if} ${IsNativeARM64}
                Goto ok
            ${EndIf}
        !endif

        IfSilent silentArch notSilentArch
        silentArch:
            SetErrorLevel 65
            Abort
        notSilentArch:
            MessageBox MB_OK "${WAILS_ARCHITECTURE_NOT_SUPPORTED}"
            Quit
    ${else}
        IfSilent silentWin notSilentWin
        silentWin:
            SetErrorLevel 64
            Abort
        notSilentWin:
            MessageBox MB_OK "${WAILS_WIN10_REQUIRED}"
            Quit
    ${EndIf}

    ok:
!macroend

!macro wails.files
    !ifdef SUPPORTS_AMD64
        ${if} ${IsNativeAMD64}
            File "/oname=${PRODUCT_EXECUTABLE}" "${ARG_WAILS_AMD64_BINARY}"
        ${EndIf}
    !endif

    !ifdef SUPPORTS_ARM64
        ${if} ${IsNativeARM64}
            File "/oname=${PRODUCT_EXECUTABLE}" "${ARG_WAILS_ARM64_BINARY}"
        ${EndIf}
    !endif
!macroend

!macro wails.writeUninstaller
    WriteUninstaller "$INSTDIR\uninstall.exe"

    SetRegView 64
    !if "${WAILS_INSTALL_SCOPE}" == "user"
        WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "${INFO_COMPANYNAME}"
        WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${INFO_PRODUCTNAME}"
        WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${INFO_PRODUCTVERSION}"
        WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${PRODUCT_EXECUTABLE}"
        WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
        WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"

        ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
        IntFmt $0 "0x%08X" $0
        WriteRegDWORD HKCU "${UNINST_KEY}" "EstimatedSize" "$0"
    !else
        WriteRegStr HKLM "${UNINST_KEY}" "Publisher" "${INFO_COMPANYNAME}"
        WriteRegStr HKLM "${UNINST_KEY}" "DisplayName" "${INFO_PRODUCTNAME}"
        WriteRegStr HKLM "${UNINST_KEY}" "DisplayVersion" "${INFO_PRODUCTVERSION}"
        WriteRegStr HKLM "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${PRODUCT_EXECUTABLE}"
        WriteRegStr HKLM "${UNINST_KEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
        WriteRegStr HKLM "${UNINST_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"

        ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
        IntFmt $0 "0x%08X" $0
        WriteRegDWORD HKLM "${UNINST_KEY}" "EstimatedSize" "$0"
    !endif
!macroend

!macro wails.deleteUninstaller
    Delete "$INSTDIR\uninstall.exe"

    SetRegView 64
    !if "${WAILS_INSTALL_SCOPE}" == "user"
        DeleteRegKey HKCU "${UNINST_KEY}"
    !else
        DeleteRegKey HKLM "${UNINST_KEY}"
    !endif
!macroend

!macro wails.setShellContext
    ${If} ${REQUEST_EXECUTION_LEVEL} == "admin"
        SetShellVarContext all
    ${else}
        SetShellVarContext current
    ${EndIf}
!macroend

# Install webview2 by launching the bootstrapper
# See https://docs.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution#online-only-deployment
!macro wails.webview2runtime
    !ifndef WAILS_INSTALL_WEBVIEW_DETAILPRINT
        !define WAILS_INSTALL_WEBVIEW_DETAILPRINT "Installing: WebView2 Runtime"
    !endif

    SetRegView 64
	# If the admin key exists and is not empty then webview2 is already installed
	ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
    ${If} $0 != ""
        Goto ok
    ${EndIf}

    ${If} ${REQUEST_EXECUTION_LEVEL} == "user"
        # If the installer is run in user level, check the user specific key exists and is not empty then webview2 is already installed
	    ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
        ${If} $0 != ""
            Goto ok
        ${EndIf}
     ${EndIf}
    
	SetDetailsPrint both
    DetailPrint "${WAILS_INSTALL_WEBVIEW_DETAILPRINT}"
    SetDetailsPrint listonly
    
    InitPluginsDir
    CreateDirectory "$pluginsdir\webview2bootstrapper"
    SetOutPath "$pluginsdir\webview2bootstrapper"
    File "MicrosoftEdgeWebview2Setup.exe"
    ExecWait '"$pluginsdir\webview2bootstrapper\MicrosoftEdgeWebview2Setup.exe" /silent /install'
    
    SetDetailsPrint both
    ok:
!macroend

# Advertise candidates only: Windows owns the user's default and UserChoice keys.
!define PKP_CAPABILITIES "Software\PKP\ExternalOpen\Capabilities"
!define PKP_REGISTERED_APP "com.pkp.desktop"
!define PKP_TORRENT_CLASS "PKP.Torrent"
!define PKP_MAGNET_CLASS "PKP.Magnet"

!macro wails.associateFiles
    WriteRegStr HKCU "Software\Classes\${PKP_TORRENT_CLASS}" "" "BitTorrent document"
    WriteRegStr HKCU "Software\Classes\${PKP_TORRENT_CLASS}\DefaultIcon" "" '$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\",0'
    WriteRegStr HKCU "Software\Classes\${PKP_TORRENT_CLASS}\shell\open\command" "" '$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\"'
    WriteRegStr HKCU "Software\Classes\.torrent\OpenWithProgids" "${PKP_TORRENT_CLASS}" ""
    WriteRegStr HKCU "${PKP_CAPABILITIES}" "ApplicationName" "${INFO_PRODUCTNAME}"
    WriteRegStr HKCU "${PKP_CAPABILITIES}" "ApplicationDescription" "PKP for PikPak"
    WriteRegStr HKCU "${PKP_CAPABILITIES}" "ApplicationIcon" '$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\",0'
    WriteRegStr HKCU "${PKP_CAPABILITIES}\FileAssociations" ".torrent" "${PKP_TORRENT_CLASS}"
    WriteRegStr HKCU "Software\RegisteredApplications" "${PKP_REGISTERED_APP}" "${PKP_CAPABILITIES}"
!macroend

!macro wails.unassociateFiles
    ReadRegStr $R0 HKCU "Software\Classes\${PKP_TORRENT_CLASS}\shell\open\command" ""
    ${If} $R0 == '$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\"'
        DeleteRegValue HKCU "Software\Classes\.torrent\OpenWithProgids" "${PKP_TORRENT_CLASS}"
        DeleteRegKey /ifempty HKCU "Software\Classes\.torrent\OpenWithProgids"
        DeleteRegKey /ifempty HKCU "Software\Classes\.torrent"
        DeleteRegKey HKCU "Software\Classes\${PKP_TORRENT_CLASS}"
        DeleteRegValue HKCU "${PKP_CAPABILITIES}\FileAssociations" ".torrent"
        DeleteRegKey /ifempty HKCU "${PKP_CAPABILITIES}\FileAssociations"
    ${EndIf}
!macroend

!macro wails.associateCustomProtocols
    WriteRegStr HKCU "Software\Classes\${PKP_MAGNET_CLASS}" "" "Magnet link"
    WriteRegStr HKCU "Software\Classes\${PKP_MAGNET_CLASS}" "URL Protocol" ""
    WriteRegStr HKCU "Software\Classes\${PKP_MAGNET_CLASS}\DefaultIcon" "" '$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\",0'
    WriteRegStr HKCU "Software\Classes\${PKP_MAGNET_CLASS}\shell\open\command" "" '$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\"'
    WriteRegStr HKCU "Software\Classes\magnet\OpenWithProgids" "${PKP_MAGNET_CLASS}" ""
    WriteRegStr HKCU "${PKP_CAPABILITIES}\UrlAssociations" "magnet" "${PKP_MAGNET_CLASS}"
    WriteRegStr HKCU "Software\Classes\pkp" "" "URL:PKP share preview"
    WriteRegStr HKCU "Software\Classes\pkp" "URL Protocol" ""
    WriteRegStr HKCU "Software\Classes\pkp\DefaultIcon" "" '$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\",0'
    WriteRegStr HKCU "Software\Classes\pkp\shell\open\command" "" '$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\"'
    WriteRegStr HKCU "${PKP_CAPABILITIES}\UrlAssociations" "pkp" "pkp"
    System::Call 'shell32::SHChangeNotify(i 0x08000000, i 0, p 0, p 0)'
!macroend

!macro wails.unassociateCustomProtocols
    ReadRegStr $R0 HKCU "Software\Classes\pkp\shell\open\command" ""
    ${If} $R0 == '$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\"'
        DeleteRegKey HKCU "Software\Classes\pkp"
        DeleteRegValue HKCU "${PKP_CAPABILITIES}\UrlAssociations" "pkp"
    ${EndIf}
    ReadRegStr $R0 HKCU "Software\Classes\${PKP_MAGNET_CLASS}\shell\open\command" ""
    ${If} $R0 == '$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\"'
        DeleteRegValue HKCU "Software\Classes\magnet\OpenWithProgids" "${PKP_MAGNET_CLASS}"
        DeleteRegKey /ifempty HKCU "Software\Classes\magnet\OpenWithProgids"
        DeleteRegKey /ifempty HKCU "Software\Classes\magnet"
        DeleteRegKey HKCU "Software\Classes\${PKP_MAGNET_CLASS}"
        DeleteRegValue HKCU "${PKP_CAPABILITIES}\UrlAssociations" "magnet"
        DeleteRegKey /ifempty HKCU "${PKP_CAPABILITIES}\UrlAssociations"
    ${EndIf}
    ReadRegStr $R0 HKCU "${PKP_CAPABILITIES}" "ApplicationIcon"
    ${If} $R0 == '$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\",0'
        DeleteRegValue HKCU "Software\RegisteredApplications" "${PKP_REGISTERED_APP}"
        DeleteRegKey HKCU "${PKP_CAPABILITIES}"
        DeleteRegKey /ifempty HKCU "Software\PKP\ExternalOpen"
        DeleteRegKey /ifempty HKCU "Software\PKP"
    ${EndIf}
    System::Call 'shell32::SHChangeNotify(i 0x08000000, i 0, p 0, p 0)'
!macroend