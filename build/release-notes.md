# PKP 0.3.0

简体中文 · [English](release-notes.en.md)

计划中的首个开源版本；本说明不表示 0.3.0 或其安装包已经发布。PKP 自有源码适用 GPL-3.0-only，已发布的 v0.1.0 安装包条款保持不变。该版本发布时须提供八件附件，包括与安装包对应的完整 PKP 自有源码包 `PKP-0.3.0-source.tar.gz`；`libmpv-sources.tar.gz` 单独提供第三方对应源码。

- 文件工作区、本地索引搜索、预览、播放、传输和多账号管理。
- 更新清单签名和安装包完整性验证；更新不会自动下载或安装。
- 计划提供 macOS Universal（macOS 12 或更高版本）和 Windows x64 / ARM64 合并安装包。
- 使用统计改为默认关闭；从旧版升级后需在隐私设置中重新主动开启。
- 移除 PPT/PPTX 内置预览及 `pptx-preview` 依赖；保留 DOCX、PDF、XLSX 等预览。PPT/PPTX 仍归为文档并使用既有不支持预览界面，下载等操作不变。

## 已知限制

- macOS 构建使用临时签名，未经过 Apple 公证；Windows 构建没有 Authenticode 签名。实体设备验收覆盖仍不完整。
- 不提供 Linux 安装包。

[安装指南](https://github.com/tonyflux/pkp/blob/main/docs/INSTALL.md) · [隐私说明](https://github.com/tonyflux/pkp/blob/main/PRIVACY.md)
