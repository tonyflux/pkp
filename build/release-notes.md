# PKP 0.5.0

## English

This release extends the AI assistant with reviewed cloud-drive operations and clearer navigation.

### Highlights

- **Review before execution**: ask the assistant to draft folder creation, copying, starring, uploads, Trash restoration, share cancellation and transfer controls. Every change waits for user confirmation; multi-target and destructive suggested-operation cards require a second confirmation. Cards show target details, progress and per-item outcomes, and can stop remaining submissions. Accepted work is not rolled back, and a submitted background task is not necessarily complete.
- **Trash, shares and transfers**: browse each catalog by page, including local uploads and downloads even when the cloud task catalog is unavailable. Restore Trash items, cancel shares, and control transfers as their states permit. Removing unfinished local transfers discards partial data and resume progress; saved cloud files are preserved when a cloud-add task is removed. Cloud-add retry supports failed BTIH tasks only. The assistant cannot empty Trash or permanently delete files.
- **Local pickers and existing dialogs**: upload files or folders using the system picker, never a model-supplied local path. Torrent addition, online extraction and share creation continue in their existing settings dialogs; opening settings does not submit a task.
- **History and reliability**: operation cards retain receipts across restarts; interrupted writes are not automatically replayed. Transfer IDs are not reused after restart, and stale task pages cannot bring removed records back. Follow-up context uses bounded receipt summaries without raw operation errors; full large-upload receipts stay local. If saving operation state or a receipt fails, further submissions stop.
- **Assistant navigation and planning**: return to the assistant home from a conversation, or leave the unfinished-video local search via Back or All Files. Continue saved conversations from Recent conversations. Review full organization plans with AI or identify only unresolved groups; cleanup of displayed search results is a separate, unchecked-by-default preview. Index-blocked queries offer a manual retry when ready, and usage-limit notices link to AI settings.
- **Sign-in**: refine the QR sign-in network-action buttons while retaining connection retry and proxy settings access.

### Data sharing and privacy

Local searches and rules do not require a model. Before a model request, review the data authorization notice. Whole-drive Trash, share and transfer queries may additionally send item names, types, sizes, times, states and progress. Tool queries do not additionally send share links or passcodes, task source URLs, torrent hashes, full local paths or raw service errors. Pagination cursors remain in the local handle map. **Information typed in questions or earlier conversation is still sent as context without automatic redaction.**

Operation approval is separate from permission to send data to a model. AI can make mistakes; review targets, paths and consequences before confirming. Online extraction passwords are sent to the extraction service when needed. See the [Privacy Notice](https://github.com/tonyflux/pkp/blob/v0.5.0/PRIVACY.en.md).

### Usage statistics (unchanged)

Daily active installation statistics have defaulted to ON since v0.3.0. Explicit opt-outs are preserved. With a configured collection endpoint, recording starts at startup and sending can begin about 30 seconds later; acknowledging or dismissing the notice does not change the setting. Reports contain a random installation ID, app version, OS/architecture, language and local activity dates—not accounts, filenames, paths, searches, file contents or feature-use counts. Turn statistics off in Settings → Privacy & Security, including before sign-in. Opt-out deletes the local ID and pending reports, not server history. No configured endpoint means nothing is recorded or sent and no notice is shown. See the Privacy Notice for migration and retention limits.

### Release assets and limitations

The release contains **eight assets**: the macOS Universal installer (macOS 12+, Apple silicon/Intel), combined Windows x64/ARM64 installer, signed update manifest and signature, `PKP-0.5.0-source.tar.gz`, `libmpv-sources.tar.gz`, `LICENSE`, and `SHA256SUMS`. Existing Ed25519 update trust is retained. Updates are not automatically downloaded or silently installed. PKP is an unofficial client; PKP-owned source is GPL-3.0-only.

macOS is ad hoc signed and not Apple-notarized; Windows lacks Authenticode signing and remains preview support. No Linux packages. Full native installation and upgrade, real-device matrix, and production-cloud acceptance have not been completed. Do not post credentials, private links or complete configuration directories in feedback.

[Installation](https://github.com/tonyflux/pkp/blob/v0.5.0/docs/INSTALL.en.md) · [Usage](https://github.com/tonyflux/pkp/blob/v0.5.0/docs/USAGE.en.md) · [Privacy](https://github.com/tonyflux/pkp/blob/v0.5.0/PRIVACY.en.md) · [Full changelog](https://github.com/tonyflux/pkp/compare/v0.4.1...v0.5.0)

---

## 简体中文

本次更新扩展 AI 助手的云盘操作，并改进对话及搜索返回路径。

### 新功能与修复

- **确认后才执行**：助手可生成新建文件夹、复制、星标、上传、回收站恢复、取消分享和传输控制等操作建议。所有修改均需用户确认；多目标或破坏性操作建议需二次确认。卡片显示目标、进度和逐项结果，可停止提交剩余项目。已提交的操作不会回滚，后台任务「已提交」不代表完成。
- **回收站、分享与传输**：分页查询资源；即使云端任务目录不可用，也可读取本机上传和下载记录。可恢复回收站项目、取消分享，并按状态控制传输。移除未完成的本机传输会丢弃分片及续传进度；移除云添加任务保留已保存文件。云添加只重试失败的 BTIH 任务。助手不能清空回收站或逐项永久删除文件。
- **本机选择器和原有设置弹窗**：上传文件或文件夹只使用系统选择器，不接受模型指定的本机路径。种子添加、在线解压和创建分享仍在原有设置弹窗中操作；打开设置不等于已经提交。
- **历史与可靠性**：操作卡片可保存逐项回执，应用中断后不会自动重放未完成的写操作。传输编号跨重启不再重复使用，旧分页不会恢复已移除任务。后续对话仅发送有长度限制的回执摘要，不发送原始操作错误；大批上传的完整回执保留在本机。执行状态或回执保存失败时停止后续提交。
- **助手导航与计划**：对话页可返回助手首页；从「找上次没看完的视频」进入本地搜索后，可用返回按钮或「全部文件」回到列表。已保存对话可从最近对话重新打开。整理计划支持 AI 复核整份计划或仅识别未确认项；清理展示的搜索结果是独立预览，默认不勾选。索引未就绪的查询可在就绪后手动重试，用量上限提示可前往 AI 设置。
- **登录**：优化扫码登录网络操作按钮，保留连接重试和代理设置入口。

### 数据发送与隐私

本地搜索和规则不需要连接模型。发送模型请求前请阅读数据授权提示。全盘回收站、分享和传输查询还可能发送项目名称、类型、大小、时间、状态和进度；工具查询不会额外发送分享链接或口令、任务源地址、种子哈希、本机完整路径或原始服务错误，分页游标保留在本机句柄映射中。**用户在问题或历史问答中写入的内容仍会作为上下文发送，不会自动脱敏。**

操作确认与模型数据发送授权相互独立。AI 可能判断错误，确认前请核对目标、路径和后果。在线解压需要密码时会将其发送给解压服务。详见[隐私说明](https://github.com/tonyflux/pkp/blob/v0.5.0/PRIVACY.md)。

### 使用统计（规则不变）

每日活跃安装统计从 v0.3.0 起默认开启，明确关闭的选择会保留。配置收集地址时启动即记录，约 30 秒后可发送；确认或关闭告知提示不改变设置。只发送随机安装编号、版本、系统／架构、界面语言和活跃日期，不发送账号、文件名、路径、搜索、文件内容或功能使用次数。可在「设置 → 隐私与安全」关闭，登录前也可操作；关闭会删除本机编号与待发送记录，不删除服务端历史。未配置收集地址时不记录、不发送、不提示。迁移和保留边界见隐私说明。

### 发布附件与限制

本版本包含 **八件附件**：macOS Universal（macOS 12+，Apple 芯片／Intel）安装包、Windows x64／ARM64 合并安装包、更新清单及签名、`PKP-0.5.0-source.tar.gz`、`libmpv-sources.tar.gz`、`LICENSE` 和 `SHA256SUMS`。沿用既有 Ed25519 更新信任，更新不会自动下载或静默安装。PKP 为非官方客户端，PKP 自有源码适用 GPL-3.0-only。

macOS 为 ad hoc 签名，未通过 Apple 公证；Windows 无 Authenticode 签名，仍为预览支持。不提供 Linux 安装包。完整原生安装／升级、真实设备矩阵和生产云端验收尚未完成。反馈时请勿公开凭据、私人链接或完整配置目录。

[安装指南](https://github.com/tonyflux/pkp/blob/v0.5.0/docs/INSTALL.md) · [使用说明](https://github.com/tonyflux/pkp/blob/v0.5.0/docs/USAGE.md) · [隐私说明](https://github.com/tonyflux/pkp/blob/v0.5.0/PRIVACY.md) · [完整变更](https://github.com/tonyflux/pkp/compare/v0.4.1...v0.5.0)
