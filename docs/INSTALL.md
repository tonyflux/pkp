# 安装与首次启动

## 下载状态

**v0.1.0 已正式发布。** 从 [最新 Release](https://github.com/tonyflux/pkp/releases/latest) 下载对应平台安装包：[macOS Universal](https://github.com/tonyflux/pkp/releases/download/v0.1.0/PKP-0.1.0-macos-universal.dmg) / [Windows x64 与 ARM64](https://github.com/tonyflux/pkp/releases/download/v0.1.0/PKP-0.1.0-windows-x64-arm64-setup.exe)。对照同一 Release 的 `SHA256SUMS` 并验证签名；不要从不明来源获取安装包。

同一 Release 提供第三方对应源码包 `libmpv-sources.tar.gz` 和使用条款；这不表示 PKP 自有源码开放。

## 校验发布附件

正式版应有七件附件：macOS DMG、Windows 安装程序、`update.json`、`update.json.sig`、`libmpv-sources.tar.gz`、`LICENSE` 与 `SHA256SUMS`。前两项是应用安装包；第三方源码包不包含 PKP 自有源码。

`SHA256SUMS` 用于检查下载完整性，不能单独证明发布者身份。应用内更新会用内置公钥验证 Ed25519 签名清单，再检查安装包大小与 SHA-256；签名或哈希错误时不要继续安装。

需要手动完整核验时，将同一 Release 的七件附件放在独立目录，并下载公开的 [verify_release.py](../.github/scripts/verify_release.py)。安装 Python 3.11+ 和 OpenSSL 3+ 后运行（版本号替换为实际标签）：

```sh
python3 verify_release.py /路径/发布附件 v0.1.0
```

工具只含公开公钥，不需要 GitHub token、账号登录或私钥；签名、标签、安装包或附件集合不一致时返回失败。公开仓库的「Verify public release」工作流会在正式发布后自动核验 GitHub 附件与匿名下载地址；工作流失败不是允许绕过校验的理由。

## macOS

要求 macOS 12 或更新版本，Universal 包同时包含 Apple 芯片与 Intel 架构，内置播放库，无需另装 mpv 或 Homebrew。

取得并校验安装包后：

1. 打开 DMG，把 PKP 拖入「应用程序」。不要直接在磁盘镜像中运行。
2. 从「应用程序」打开 PKP。当前构建为 **ad hoc 签名、未经 Apple 公证**，首次启动可能被系统拦截；更新后也可能再次提示。
3. macOS 15 及更新版本：先尝试打开并关闭拦截提示，再到「系统设置 → 隐私与安全性 → 安全性」，找到 PKP 的「仍要打开」，按系统要求确认。
4. macOS 12–14：在「应用程序」中按住 Control 点按 PKP（或右键），选择「打开」，再确认系统提示。

若仍提示「已损坏」或上述操作无效，先确认下载来源与校验值；不要为未知文件绕过安全检查。只有确认文件可信且安装路径正确后，才可在终端针对该应用移除隔离属性：

```sh
xattr -dr com.apple.quarantine /Applications/PKP.app
```

这会移除该应用的隔离标记，不是安全认证，也不要扩展到整个下载目录或关闭系统 Gatekeeper。实际应用名称、路径不同则不要直接照抄。

## Windows 与 Linux

Windows 提供 x64 / ARM64 共用安装包，按本机架构选择应用与播放库。用户已反馈首发草稿验收通过，但未提供完整设备矩阵，不能承诺与 macOS 一致的体验；播放表面、同步、通知和系统身份验证等仍需扩大真机覆盖。菜单栏面板与 WebView 代理等存在平台限制，安装包没有 Authenticode 发布者签名。

Linux 暂不承诺支持或发布安装包。

## 更新应用

支持更新功能的版本可从「设置 → 关于与更新」检查；未登录时使用登录页同名入口，macOS 也可使用 PKP 菜单的「检查更新…」。自动检查默认开启，可单独关闭；不会自动下载或安装。发现新版后可查看说明、稍后提醒或忽略此版本，手动检查仍可查看被忽略的版本。

- macOS：下载并校验后点击「打开安装镜像」，先退出 PKP，再拖拽覆盖原应用。不要直接运行镜像内应用。当前 ad hoc 签名与未公证限制仍适用。
- Windows：点击「退出并运行安装程序」，按退出确认和系统管理员授权继续；取消退出或授权不会强制关闭 PKP。安装器要求退出其它 PKP 进程，沿用原安装目录，不需要先卸载；账号与本地设置保留。仍需 Windows 真机充分验收。
- 没有更新功能或信任旧开发公钥的本地测试版本，需要先手动下载并安装正式版本；同为 0.1.0 时不会提示升级。

## 首次启动

- 使用自己的 PikPak 账号登录。当前「记住账号密码」与「自动登录」默认勾选，不希望保存密码时请取消勾选。
- 先阅读[隐私说明](../PRIVACY.md)。生产构建默认开启使用统计，可到「设置 → 隐私与安全」关闭；当前登录成功后还会提交邀请关系。
- 首次登录会建立本地文件索引，期间仍可浏览；搜索与全盘统计可能要等待索引就绪。
- macOS 可能请求系统通知权限；是否允许由你决定。当前 ad hoc 构建的实际通知表现尚待真机确认。
- 使用同步或批量整理前，先备份重要数据，并仔细检查预览和确认内容。
