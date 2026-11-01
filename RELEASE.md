# 发布边界

简体中文 · [English](RELEASE.en.md)

本文件只说明公开仓库的发布约束，不记录私钥、本机身份或历史运行编号。

- 源码模块为 `github.com/tonyflux/pkp`。产品名、`com.pkp.desktop`、配置目录和安装定位保持不变。
- PKP 自有源码适用 GPL-3.0-only。v0.1.0 已发布安装包适用 `docs/legal/v0.1.0-proprietary.txt`，不要把当前源码或新许可证追溯到该版本。
- v0.3.0 起发布八件附件：两个安装包、`update.json`、`update.json.sig`、`PKP-<版本>-source.tar.gz`、`libmpv-sources.tar.gz`、`LICENSE`、`SHA256SUMS`。PKP 源码包对应本次构建的提交；安装包不进入 Git。v0.1.0 的七件历史附件保持不变。
- 更新签名私钥只放在仓库外或可选的受保护 GitHub `release` 环境中，不放入普通 CI。丢失后不要静默更换已分发客户端信任的公钥。
- 维护者已确认当前第三方客户端身份参数可公开及复用；保留认证值、签名算法和邀请码行为。这不表示 PikPak 背书或独立法律审计。
- 维护者已确认当前应用标志可随开源源码及安装包公开分发；不再列为待确认阻断。该确认不表示官方背书，来源记录及导出链边界见 `docs/legal/artwork-provenance.md`。

## 发布路径

v0.3.0 是首个开源版本，安装包先以草稿供人工验收，尚未正式发布。当前源码已移除 PPT/PPTX 内置预览及 `pptx-preview` 依赖，其许可冲突不再是当前源码的发布阻断；这不追溯解决历史副本或既有分发的许可义务。维护者已确认当前应用标志的公开分发权限；其余素材和来源边界见[第三方组件说明](THIRD_PARTY.md)。正式发布前须审阅两种语言的发布说明并完成平台验收；Windows 原生安装及升级尚待人工验证。签名私钥必须匹配现有客户端内置的公钥，发布工具会检查这一点。不要提交私钥或静默更换信任公钥。

发布前必须把预期源码、资源、依赖锁和发布说明提交为干净快照。`make release-local` 在构建前记录 HEAD，并在归档时确认源码未变；有未提交或非忽略的未跟踪文件、跟踪的缓存、子模块或改变归档内容的 Git attributes 时会拒绝。签名清单记录源码提交、tree 及源码包的大小和哈希。上传时该实际源码提交必须已在公开 `main` 历史内，不能用仅有资料的旧提交代替。源码包附固定 Go/npm 输入与隔离缓存恢复说明；媒体源码另附。完整材料说明见 [THIRD_PARTY.md](THIRD_PARTY.md)。

- **本地发布：** 在 macOS 上按开发指南安装构建依赖，包括 Wails、CMake、NSIS 和捆绑库所需工具。运行 `make release-local RELEASE_NOTES=build/release-notes.md RELEASE_KEY=/secure/path/to/update-ed25519.key`，再运行 `make release-verify`。这只在本地构建和签名，不上传。若要上传已验证的草稿，以有权发布到 `tonyflux/pkp` 的身份登录 `gh`，运行 `make release-draft`；审阅后运行 `make release-publish`，将草稿发布为 Latest。
- **可选 GitHub 工作流：** 在仓库外部设置中为 `release` 环境配置适当的必需审核人和部署限制，并配置环境密钥 `UPDATE_SIGNING_KEY`（匹配的私钥）及 `PUBLIC_RELEASE_TOKEN`（有权向 `tonyflux/pkp` 发布 Release）。环境保护由 GitHub 仓库设置控制，并非本文件强制；不要为拉取请求 CI 配置这些密钥。推送稳定标签后运行检查，环境审批通过后构建、签名、验证并上传草稿。也可从 `main` 手动运行工作流，指定已有标签并令 `publish` 为 false。审阅草稿后，从 `main` 手动运行并将 `publish` 设为 true；工作流下载并验证已有草稿，再发布为 Latest，不重新构建。标签必须与源码版本一致且指向 `main` 可到达的提交。还须单独保护标签创建和工作流变更。

准备这些文件本身不会发布版本。真正发布时须核验八件附件、对应源码、签名和哈希，并执行发布后的公开下载检查。
