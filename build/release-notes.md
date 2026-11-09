# PKP 0.4.1

## English

Refined About & Updates layout, stationary glass shell when signed out, and safe Markdown rendering for release notes.

### Highlights

- **About & Updates top card**: show check results, release version, download progress, and download/install actions together in the top version card. The default window exposes the main update action and the start of the release notes without scrolling; long notes scroll with the page, followed by automatic-check settings and community links. Signed-in, signed-out, notification, and menu entries return to the result area at the top while preserving signature verification, skipped versions, installation confirmation, all five UI languages, and light/dark themes.
- **Signed-out update shell & Markdown release notes**: keep the glass rim on a stationary outer shell while scrolling long content inside the inner body so border decoration no longer crosses text. Render release notes with the existing safe Markdown reader (headings, lists, emphasis, and code); open external links only on explicit clicks without loading remote images or interpreting raw HTML.
- **Includes v0.4.0 capabilities**: default-off read-only LAN sharing (with Windows first-time initialization and drive-alias resolution fixes), PikPak Share Import previews and desktop clipboard detection, external `magnet:` and `.torrent` opening, unified grid/list pickers across file/folder dialogs, and desktop file context menus and keyboard shortcuts.

### Boundaries and privacy (unchanged policy)

LAN pages use **HTTP on trusted networks only**; do not expose them through public port forwarding. QR codes exclude passwords. HTTP is unencrypted, and a separate password does not replace a trusted network.

**Daily active installation statistics have defaulted to ON since v0.3.0; v0.4.1 does not change this** (v0.2.0 defaulted to OFF). Identifiable explicit opt-outs are preserved; settings without an explicit choice follow the on default, and old saves without refusal markers cannot be distinguished from no choice. With an endpoint, recording starts at startup and sending can begin about 30 seconds later, before notice acknowledgment; without one, nothing is recorded/sent and no notice appears. Got it or dismissal acknowledges the notice, not consent, and leaves statistics enabled.

Reports contain a random installation ID, version, OS/architecture, language and active local dates—not feature actions, frequencies, logs, accounts, filenames, paths, searches or file contents. Turn them off in Settings → Privacy & Security, also available before sign-in. Opt-out deletes the local ID and pending reports, not server history. Counts reflect successfully reporting active installations, not verified people or exact installs; publishing the app does not deploy telemetry services.

### Release assets, verification and limitations

The release asset set has **eight items**: macOS Universal installer (macOS 12+, Apple silicon/Intel), combined Windows x64/ARM64 installer, update manifest and signature, `PKP-0.4.1-source.tar.gz`, `libmpv-sources.tar.gz`, `LICENSE` and `SHA256SUMS`. PKP-owned source is GPL-3.0-only; historical licensing is not changed retroactively. PKP is an unofficial client.

Keep the existing Ed25519 update-signing trust and public key; no trust reset. Drafts are excluded from automatic updates, and publication does not enable automatic downloads or silent installation. macOS remains ad hoc signed and not Apple-notarized; Windows is unsigned by Authenticode and remains preview support. No Linux packages. **Full real-device, native installation/upgrade, device-matrix and production-cloud acceptance is not complete.** Never post passwords, tokens, API keys or the full configuration directory in feedback.

[Installation](https://github.com/tonyflux/pkp/blob/v0.4.1/docs/INSTALL.en.md) · [Usage](https://github.com/tonyflux/pkp/blob/v0.4.1/docs/USAGE.en.md) · [Privacy](https://github.com/tonyflux/pkp/blob/v0.4.1/PRIVACY.en.md) · [Full changelog](https://github.com/tonyflux/pkp/compare/v0.4.0...v0.4.1)

---

## 简体中文

优化「关于与更新」顶部版本卡操作、修复登录前更新页滚动边框，并使用安全 Markdown 渲染更新说明。

### 新功能与修复

- **「关于与更新」顶部版本卡**：集中展示检查结果、新版本号、下载进度及下载／安装操作，默认窗口无需滚动即可查看主要操作与更新说明开头；长说明随页面滚动，自动检查和社区链接移至其后。登录前后、更新通知与菜单入口均定位到结果区，保留原有更新签名校验、忽略版本和安装确认机制，并适配五语及深浅主题。
- **登录前更新页边框与 Markdown 更新说明**：固定登录前更新页外层玻璃面板边缘，仅在内部滚动正文，修复长内容滚动时圆角装饰横穿文字的问题。更新说明复用安全 Markdown 渲染，正确显示标题、列表、粗体和代码；链接仅在点击后打开，远程图片与原始 HTML 不加载或执行。
- **包含 v0.4.0 能力**：默认关闭的局域网只读共享（含 Windows 首次初始化与盘符设备路径解析修复）、PikPak 分享转存预览与桌面剪贴板识别、外部 `magnet:` 与 `.torrent` 打开、统一网格／列表选择器，以及桌面文件右键菜单与快捷键。

### 使用边界与隐私（沿用现有规则）

局域网网页使用 **HTTP，仅限可信网络**，不应做公网端口映射；二维码不含密码。HTTP 不提供传输加密，独立密码不能替代可信网络。

**v0.3.0 起每日活跃安装统计默认开启，v0.4.1 不改变此规则**（v0.2.0 默认关闭）。保留可识别的明确关闭选择；没有明确选择的旧配置随默认开启，未保存拒绝字段的旧配置无法与从未选择区分。有收集地址时启动记录，约 30 秒后可发送，不等待提示已阅；无地址不记录、不发送、不提示。「知道了」或关闭提示仅表示已阅，不是同意，也不关闭统计。

仅发送随机安装编号、版本、系统／架构、语言及活跃本地日期，不发送功能明细、次数、日志、账号、文件名、路径、搜索或文件内容。可在「设置 → 隐私与安全」关闭，登录前也可操作；关闭删除本机编号与待发送数据，不删除服务端历史。统计是成功上报的活跃安装，不是真实人数或精确安装量；发布应用不等于部署统计服务。

### 发布附件、校验与限制

附件集合为 **八件**：macOS Universal（macOS 12+，Apple 芯片／Intel）安装包、Windows x64／ARM64 合并安装包、更新清单及其签名、`PKP-0.4.1-source.tar.gz`、`libmpv-sources.tar.gz`、`LICENSE`、`SHA256SUMS`。PKP 自有源码适用 GPL-3.0-only；历史版本许可不追溯变更，PKP 为非官方客户端。

沿用既有 Ed25519 更新签名信任和公钥，不重置信任；草稿不进入自动更新，公开后也不自动下载或静默安装。macOS 仍为 ad hoc 签名、未经 Apple 公证；Windows 无 Authenticode 签名，仍为预览支持。不提供 Linux 安装包。**完整真实设备、原生安装／升级、设备矩阵及生产云端验收尚未完成。** 反馈勿附密码、token、API Key 或完整配置目录。

[安装指南](https://github.com/tonyflux/pkp/blob/v0.4.1/docs/INSTALL.md) · [使用说明](https://github.com/tonyflux/pkp/blob/v0.4.1/docs/USAGE.md) · [隐私说明](https://github.com/tonyflux/pkp/blob/v0.4.1/PRIVACY.md) · [完整变更](https://github.com/tonyflux/pkp/compare/v0.4.0...v0.4.1)
