# 发布边界

简体中文 · [English](RELEASE.en.md)

本文件只说明公开仓库的发布约束，不记录私钥、本机身份或历史运行编号。

- 源码模块为 `github.com/tonyflux/pkp`。产品名、`com.pkp.desktop`、配置目录和安装定位保持不变。
- PKP 自有源码适用 GPL-3.0-only。v0.1.0 已发布安装包适用 `docs/legal/v0.1.0-proprietary.txt`，不要把当前源码或新许可证追溯到该版本。
- v0.2.0 起发布八件附件：两个安装包、`update.json`、`update.json.sig`、`PKP-<版本>-source.tar.gz`、`libmpv-sources.tar.gz`、`LICENSE`、`SHA256SUMS`。PKP 源码包对应本次构建的提交；安装包不进入 Git。v0.1.0 的七件历史附件保持不变。
- 更新签名私钥只放在仓库外或可选的受保护 GitHub `release` 环境中，不放入普通 CI。丢失后不要静默更换已分发客户端信任的公钥。
- 维护者已确认当前第三方客户端身份参数可公开及复用；保留认证值、签名算法和邀请码行为。这不表示 PikPak 背书或独立法律审计。
- 维护者已确认当前应用标志可随开源源码及安装包公开分发；不再列为待确认阻断。该确认不表示官方背书，来源记录及导出链边界见 `docs/legal/artwork-provenance.md`。
- macOS 从 v0.2.0 起使用显式「下载更新 → 重启并更新」：助手准备完成并获用户确认退出后，在原位置替换应用、重新打开原配置，保留账号与设置。可能出现系统替换权限请求；其他使用同一应用的 PKP 实例须先退出，不强制终止。继续使用现有正式稳定版 `update.json`、Ed25519 签名与 DMG、内置信任公钥，不使用 Sparkle，不自动下载或静默安装，也不移除隔离标记或绕过 Gatekeeper。已发布的 macOS v0.1.0 仍需手动拖拽覆盖一次才能获得此功能；Windows 保持用户确认退出后运行现有安装器的流程。
- 隔离、签名、无窗口的原生样例已验证取消、校验拒绝、实例占用、替换后按原配置重开及启动失败回滚，复现脚本见[开发指南](docs/DEVELOPMENT.md)。正式发布前仍须人工验收真实应用退出确认、系统替换授权的允许/取消、Gatekeeper 与各平台设备上的升级；隔离烟测不等于生产升级验收，更新签名也不等于 Apple 签名或公证。

## 发布路径

v0.2.0 是首个开源版本；v0.3.0 沿用上文的八件附件与同一更新信任公钥。macOS 仍是 ad hoc 签名且尚未公证；Windows 没有 Authenticode 签名，仍为预览支持，设备验收不完整。当前源码已移除 PPT/PPTX 内置预览及 `pptx-preview` 依赖，其许可冲突不再是当前源码的发布阻断；这不追溯解决历史副本或既有分发的许可义务。维护者已确认当前应用标志的公开分发权限；其余素材和来源边界见[第三方组件说明](THIRD_PARTY.md)。正式发布前须审阅两种语言的发布说明并完成平台验收；Windows 原生安装及升级尚待人工验证。签名私钥必须匹配现有客户端内置的公钥，发布工具会检查这一点。不要提交私钥或静默更换信任公钥。

发布前必须把预期源码、资源、依赖锁和发布说明提交为干净快照。`make release-local` 在构建前记录 HEAD，并在归档时确认源码未变；有未提交或非忽略的未跟踪文件、跟踪的缓存、子模块或改变归档内容的 Git attributes 时会拒绝。签名清单记录源码提交、tree 及源码包的大小和哈希。上传时该实际源码提交必须已在公开 `main` 历史内，不能用仅有资料的旧提交代替。源码包附固定 Go/npm 输入与隔离缓存恢复说明；媒体源码另附。完整材料说明见 [THIRD_PARTY.md](THIRD_PARTY.md)。

- **本地发布：** 在 macOS 上按开发指南安装构建依赖，包括 Wails、CMake、NSIS 和捆绑库所需工具。运行 `make release-local RELEASE_NOTES=build/release-notes.md RELEASE_KEY=/secure/path/to/update-ed25519.key`，再运行 `make release-verify`。这只在本地构建和签名，不上传。若要上传已验证的草稿，以有权发布到 `tonyflux/pkp` 的身份登录 `gh`，运行 `make release-draft`；审阅后运行 `make release-publish`，将草稿发布为 Latest。
- **可选 GitHub 工作流：** 在仓库外部设置中为 `release` 环境配置适当的必需审核人和部署限制，并配置环境密钥 `UPDATE_SIGNING_KEY`（匹配的私钥）及 `PUBLIC_RELEASE_TOKEN`（有权向 `tonyflux/pkp` 发布 Release）。环境保护由 GitHub 仓库设置控制，并非本文件强制；不要为拉取请求 CI 配置这些密钥。发布构建仅接受从 `main` 手动运行：指定已有源码标签并将 `publish` 设为 false，运行检查并在环境审批通过后构建、签名、验证及上传草稿。推送标签不会自动启动发布构建；本地发布后的 `release.published` 事件仍由独立的 `release-check.yml` 验证公开附件，`main` 与拉取请求 CI 保持不变。审阅草稿后，从 `main` 手动运行并将 `publish` 设为 true；工作流下载并验证已有草稿，再发布为 Latest，不重新构建。标签必须与源码版本一致且指向 `main` 可到达的提交。不要重新构建、重签或覆盖已经正式发布的版本；应发布新版本。还须单独保护标签创建和工作流变更。

准备这些文件本身不会发布版本。真正发布时须核验八件附件、对应源码、签名和哈希，并执行发布后的公开下载检查。
