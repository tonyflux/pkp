# PKP 1.0.2

## English

- macOS: Fix in-app updates mistaking the crash monitor for another PKP instance while retaining checks for actual competing instances.
- Windows: In-app updates select the matching machine-wide or per-user installer and, with supported installers, update and reopen PKP after confirmation. Older clients or installers may require the installation wizard. Real-device validation of Windows upgrade, authorization and rollback remains outstanding.

**Upgrading from macOS 1.0.1:** The existing updater may still report another instance because it uses the old helper. If this happens, quit PKP and manually replace the installed app with the app from the 1.0.2 DMG. Your accounts and settings are retained.

macOS builds are ad hoc signed and not notarized; Windows installers are not Authenticode-signed. On Linux, install the verified package manually or replace the AppImage after exiting PKP.

[Installation](https://github.com/tonyflux/pkp/blob/v1.0.2/docs/INSTALL.en.md) · [Changes since 1.0.1](https://github.com/tonyflux/pkp/compare/v1.0.1...v1.0.2)

---

## 简体中文

- macOS：修复应用内更新将崩溃监控进程误判为另一个 PKP 实例的问题，保留对真实多实例的检查。
- Windows：应用内更新选择与原安装范围匹配的机器级或用户级安装包；支持自动更新的安装器在确认后更新并重新打开 PKP。旧客户端或安装器可能仍需使用安装向导。Windows 升级、授权及回滚的真机验收仍待完成。

**从 macOS 1.0.1 升级：** 现有更新助手仍可能误报其他实例。遇到此问题时，请退出 PKP，再用 1.0.2 DMG 中的应用手动替换已安装的应用；账号和设置会保留。

macOS 安装包为临时签名，未经过 Apple 公证；Windows 安装器未使用 Authenticode 签名。Linux 安装包通过验证后需手动安装，或退出 PKP 后替换 AppImage。

[安装指南](https://github.com/tonyflux/pkp/blob/v1.0.2/docs/INSTALL.md) · [相对 1.0.1 的变更](https://github.com/tonyflux/pkp/compare/v1.0.1...v1.0.2)
