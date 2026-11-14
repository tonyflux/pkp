# PKP 0.7.0

## English

This release improves batch file operations and adds a background task history with per-item receipts.

- Moves, copies, starring, trash, restore, permanent deletion, Cleanup and download submissions show queued, processing and verification states. You can continue using the app while work runs.
- File changes for the same account run in order. Stop prevents remaining submissions; it does not undo work already submitted. Successful results automatically fold away, and File tasks in the sidebar reopens session receipts.
- Review failures and retry only items confirmed not to have been applied. A timeout or unconfirmed result may already have taken effect: check the cloud before acting again. Selection changes do not change an already submitted task.
- Select all covers loaded, matching items only. Empty Trash processes its captured snapshot, not items added later. Download receipts confirm submission to Transfers, not completed downloads. Task history lasts only for the current window session.

The release contains eight assets: macOS Universal and Windows x64/ARM64 installers, signed update manifest and signature, `PKP-0.7.0-source.tar.gz`, `libmpv-sources.tar.gz`, `LICENSE` and `SHA256SUMS`. macOS is ad hoc signed and not Apple-notarized; Windows lacks Authenticode signing and remains preview support. Native installation and upgrade, real-device matrix and production-cloud acceptance have not been completed. No Linux packages are provided.

[Installation](https://github.com/tonyflux/pkp/blob/v0.7.0/docs/INSTALL.en.md) · [Usage guide](https://github.com/tonyflux/pkp/blob/v0.7.0/docs/USAGE.en.md#batch-file-operations) · [Full changelog](https://github.com/tonyflux/pkp/compare/v0.6.1...v0.7.0)

---

## 简体中文

本版本优化批量文件操作，新增后台任务历史与逐项回执。

- 移动、复制、星标、移到回收站、还原、永久删除、清理和下载提交可查看排队、处理与核验状态；任务运行时可以继续使用应用。
- 同账号文件改动按顺序执行。「停止剩余提交」不会撤销已经提交的操作。全部成功后任务条自动收起，可从侧栏「文件任务」重新查看本次会话回执。
- 可查看失败原因，并只重试确认未生效的项目。超时或结果待确认的操作可能已经生效，请先检查云端结果；之后更改选择不会改变已提交任务。
- 全选只覆盖已加载且符合筛选的项目；清空回收站只处理所捕获的快照，不包含后来新增的项目。下载回执表示已加入传输队列，不表示下载完成。任务历史仅保留在当前窗口会话中。

本版本包含八件附件：macOS Universal 和 Windows x64／ARM64 安装包、更新清单及签名、`PKP-0.7.0-source.tar.gz`、`libmpv-sources.tar.gz`、`LICENSE` 和 `SHA256SUMS`。macOS 为 ad hoc 签名，未通过 Apple 公证；Windows 无 Authenticode 签名，仍为预览支持。原生安装与升级、真实设备矩阵和生产云端验收尚未完成。不提供 Linux 安装包。

[安装指南](https://github.com/tonyflux/pkp/blob/v0.7.0/docs/INSTALL.md) · [使用说明](https://github.com/tonyflux/pkp/blob/v0.7.0/docs/USAGE.md#批量文件操作) · [完整变更](https://github.com/tonyflux/pkp/compare/v0.6.1...v0.7.0)
