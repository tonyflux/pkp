# PKP 0.4.0

## English

Read-only LAN sharing, Share Import previews, and improved desktop file actions.

### Highlights

- **Sign-in layout**: keep network actions as text links on hover, restore compact footer labels, and prevent the login card from clipping in narrow windows. Keyboard focus and network settings access remain available.
- **Share Import**: paste PikPak share links or open `pkp://share`, with access codes, thumbnails, grid/list views, nested pagination, cross-folder selection and read-only media previews. Explicitly confirm the receiving account and destination before submission. Cancellation preserves magnet/torrent drafts; uncertain submissions are not automatically replayed. Close, lock and stale replies reclaim preview resources.
- **Desktop clipboard detection** checks validated shares at startup, foreground activation and account readiness. Opens previews only: no automatic import, background polling or interruption of existing dialogs, and no repeated canceled clipboard input. Server mode never reads the host clipboard.
- **Default-off read-only LAN sharing** adds a separate password, fixed account/folder scope, address QR codes, responsive five-language visitor pages, original downloads and desktop advance preparation. Stop, password changes and space disable revoke access. Optional idle-sleep prevention is also off by default and does not prevent manual or lid-close sleep. Fix first-time LAN initialization on Windows so a missing `state.json` starts a new profile and ordinary drive aliases resolve to device paths without weakening symlink/junction rejection.
- **Mobile playback and preparation**: responsive custom controls, seek gestures, supported fullscreen/PiP, an image viewer and lightweight Home Screen/PWA guidance. Prefer supported originals; when necessary, fully prepare compatible copies at up to 1080p before manual playback. Lossless startup optimization remains an explicit choice. Packages include FFmpeg/ffprobe. On-demand pagination, thumbnails and bounded memory caches reduce repeated loading. No Service Worker or offline private-content cache; HTTP installation depends on the browser.
- **Storage and authorization safeguards**: separate original and compatible/lossless-copy locations; reuse completed sync originals only after identity, version and SHA-256 checks without creating or modifying sync. Hide sync staging contents. Cleanup removes only verified, unused owned copies, never originals; revalidate sources, permissions and versions, with explicit resumption for held work.
- **External opens, unified pickers and file actions**: browser `magnet:` links and local `.torrent` files enter Cloud Add confirmation at cold start or while running, without automatic submission or taking over system defaults. Preserve sign-in, lock, account-switch and draft queues. Unify grid/list views and internal scrolling across file and folder selection sheets (LAN space folders and preparation, Share Import, move/copy/sync/cleanup/destination pickers, archive contents/destinations, and torrent files). Files context menus cover single items, multi-selection and the canvas (including single-item “Organize”); shortcuts include `F2` rename, arrows/`Shift` selection and in-drive copy/cut/paste.
- **Reliability and release workflow**: fix native player handle lifetime during shutdown concurrent with background track restoration on macOS/Windows. Share native-media caches across CI and signed builds while keeping all tests, dependency-lock checks and release verification. Release builds are manually dispatched from `main` only to avoid duplicate tag-triggered signing/uploads; post-publication public-download verification remains separate.

### Boundaries and privacy (unchanged policy)

LAN pages use **HTTP on trusted networks only**; do not expose them through public port forwarding. QR codes exclude passwords. HTTP is unencrypted, and a separate password does not replace a trusted network.

**Daily active installation statistics have defaulted to ON since v0.3.0; v0.4.0 does not change this** (v0.2.0 defaulted to OFF). Identifiable explicit opt-outs are preserved; settings without an explicit choice follow the on default, and old saves without refusal markers cannot be distinguished from no choice. With an endpoint, recording starts at startup and sending can begin about 30 seconds later, before notice acknowledgment; without one, nothing is recorded/sent and no notice appears. Got it or dismissal acknowledges the notice, not consent, and leaves statistics enabled.

Reports contain a random installation ID, version, OS/architecture, language and active local dates—not feature actions, frequencies, logs, accounts, filenames, paths, searches or file contents. Turn them off in Settings → Privacy & Security, also available before sign-in. Opt-out deletes the local ID and pending reports, not server history. Counts reflect successfully reporting active installations, not verified people or exact installs; publishing the app does not deploy telemetry services.

### Release assets, verification and limitations

The release asset set has **eight items**: macOS Universal installer (macOS 12+, Apple silicon/Intel), combined Windows x64/ARM64 installer, update manifest and signature, `PKP-0.4.0-source.tar.gz`, `libmpv-sources.tar.gz`, `LICENSE` and `SHA256SUMS`. PKP-owned source is GPL-3.0-only; historical licensing is not changed retroactively. PKP is an unofficial client.

Keep the existing Ed25519 update-signing trust and public key; no trust reset. Drafts are excluded from automatic updates, and publication does not enable automatic downloads or silent installation. macOS remains ad hoc signed and not Apple-notarized; Windows is unsigned by Authenticode and remains preview support. No Linux packages. **Full real-device, native installation/upgrade, device-matrix and production-cloud acceptance is not complete.** Never post passwords, tokens, API keys or the full configuration directory in feedback.

[Installation](https://github.com/tonyflux/pkp/blob/v0.4.0/docs/INSTALL.en.md) · [Usage](https://github.com/tonyflux/pkp/blob/v0.4.0/docs/USAGE.en.md) · [Privacy](https://github.com/tonyflux/pkp/blob/v0.4.0/PRIVACY.en.md) · [Full changelog](https://github.com/tonyflux/pkp/compare/v0.3.0...v0.4.0)

---

## 简体中文

局域网只读共享、分享转存预览与桌面文件操作改进。

### 新功能与修复

- **登录界面**：网络操作悬停时保持文本链接样式，恢复底部入口的小字号，窄窗口不再裁切登录卡片；保留键盘焦点与网络设置入口。
- **分享转存**：粘贴 PikPak 分享链接或通过 `pkp://share` 打开，支持提取码、缩略图、网格／列表、分层翻页、跨目录多选及只读媒体预览。明确选择接收账号与目录后确认提交；取消不覆盖磁力链／种子草稿，结果未明的提交不自动重试。预览资源在关闭、锁定或响应过期时回收。
- **桌面剪贴板识别**：启动、窗口回到前台及账号就绪后检查合法分享，只打开预览，不自动转存、不后台轮询、不抢占现有弹窗；取消后不重复回填。服务端模式不读取宿主机剪贴板。
- **默认关闭的局域网只读共享**：独立密码、固定账号／目录授权、地址二维码、五语响应式访客网页、原件下载与电脑提前准备。停止、改密及停用空间撤销访问；共享期间防止自动休眠也需主动开启，不阻止手动睡眠或合盖。修复 Windows 首次初始化：缺少 `state.json` 时正常创建新配置，并在安全打开前解析普通盘符设备路径，保留符号链接／目录联接保护。
- **手机播放与准备**：适配横竖屏的自定义播放器、手势跳转、受支持的全屏／画中画、图片查看器与轻量主屏幕／PWA 指引。支持的原件优先直放，必要时完整准备最高 1080p 兼容副本再手动播放；可明确选择无损起播优化。安装包随附 FFmpeg/ffprobe。按需分页、缩略图及有限内存缓存减少重复加载；PWA 不注册 Service Worker、不离线缓存私有内容，HTTP 安装能力依浏览器而定。
- **存储与授权保护**：分开设置准备原件与兼容／无损副本目录；复用经身份、版本及 SHA-256 核验的已完成同步原件，不创建或修改同步。隐藏同步暂存内容，清理只删除核验一致且未使用的自有副本，不删除原件；来源、权限与版本持续核验，挂起任务须明确恢复。
- **外部打开、统一选择视图与文件操作**：浏览器 `magnet:` 和本机 `.torrent` 可在冷启动或运行中进入云添加确认，不自动提交、不强占系统默认关联；保留登录、锁定、账号切换与草稿队列保护。文件／目录选择浮层统一支持网格与列表切换及内部滚动（局域网共享目录与准备、分享转存、移动／复制／同步／清理／保存位置选择器、在线解压内容与目标目录、种子文件）。文件页增加单项／多选／空白处右键菜单（含单项「整理这些」）、`F2` 重命名、方向键及 `Shift` 选择、云盘内复制／剪切／粘贴等快捷键。
- **可靠性与发布流程**：修复 macOS／Windows 播放器关闭与后台轨道恢复之间的原生句柄生命周期竞态。普通 CI 与签名构建共用原生媒体缓存，保留全部测试、依赖锁检查及发布校验。发布构建仅从 `main` 手动触发，避免标签重复签名上传；正式发布后的公开下载验签仍独立执行。

### 使用边界与隐私（沿用现有规则）

局域网网页使用 **HTTP，仅限可信网络**，不应做公网端口映射；二维码不含密码。HTTP 不提供传输加密，独立密码不能替代可信网络。

**v0.3.0 起每日活跃安装统计默认开启，v0.4.0 不改变此规则**（v0.2.0 默认关闭）。保留可识别的明确关闭选择；没有明确选择的旧配置随默认开启，未保存拒绝字段的旧配置无法与从未选择区分。有收集地址时启动记录，约 30 秒后可发送，不等待提示已阅；无地址不记录、不发送、不提示。「知道了」或关闭提示仅表示已阅，不是同意，也不关闭统计。

仅发送随机安装编号、版本、系统／架构、语言及活跃本地日期，不发送功能明细、次数、日志、账号、文件名、路径、搜索或文件内容。可在「设置 → 隐私与安全」关闭，登录前也可操作；关闭删除本机编号与待发送数据，不删除服务端历史。统计是成功上报的活跃安装，不是真实人数或精确安装量；发布应用不等于部署统计服务。

### 发布附件、校验与限制

附件集合为 **八件**：macOS Universal（macOS 12+，Apple 芯片／Intel）安装包、Windows x64／ARM64 合并安装包、更新清单及其签名、`PKP-0.4.0-source.tar.gz`、`libmpv-sources.tar.gz`、`LICENSE`、`SHA256SUMS`。PKP 自有源码适用 GPL-3.0-only；历史版本许可不追溯变更，PKP 为非官方客户端。

沿用既有 Ed25519 更新签名信任和公钥，不重置信任；草稿不进入自动更新，公开后也不自动下载或静默安装。macOS 仍为 ad hoc 签名、未经 Apple 公证；Windows 无 Authenticode 签名，仍为预览支持。不提供 Linux 安装包。**完整真实设备、原生安装／升级、设备矩阵及生产云端验收尚未完成。** 反馈勿附密码、token、API Key 或完整配置目录。

[安装指南](https://github.com/tonyflux/pkp/blob/v0.4.0/docs/INSTALL.md) · [使用说明](https://github.com/tonyflux/pkp/blob/v0.4.0/docs/USAGE.md) · [隐私说明](https://github.com/tonyflux/pkp/blob/v0.4.0/PRIVACY.md) · [完整变更](https://github.com/tonyflux/pkp/compare/v0.3.0...v0.4.0)
