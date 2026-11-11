# PKP 0.6.0

## English

This release adds optional PikPak AI subtitles to the macOS player.

### Highlights

- **AI subtitles on macOS**: choose Original, Chinese, English, Japanese, or Korean in the player's subtitle menu, including for videos without embedded subtitles. PikPak generates captions from the video's audio; no chat-model API key is required. Captions follow playback, seeking and pauses, and use the existing subtitle style and delay settings.
- **Membership and quota**: eligibility is checked against the account that owns the playing video. Nonmembers retain ordinary subtitles; failed checks can be retried. The subtitle menu and playback settings display the latest server-provided allowance and reset time when available. PikPak controls membership eligibility, available languages, quota, availability, and the one-session-per-account restriction.
- **Reliable handoffs**: rapid language, quality and audio-track changes safely release the previous session. Turning subtitles off or quitting cancels subtitle-owned requests without interrupting other tasks' shared authentication refreshes. Refusals and disconnections are shown rather than retried indefinitely.

### Data sharing and privacy

AI subtitles are off by default. Enabling them sends the playing account's authorization, video GCID/file/media identifiers, selected language, and independently fetched and decoded audio near the playback position to PikPak's subtitle service. The extra media read uses network traffic. Captions are drawn locally rather than automatically saved to the drive. Membership checks use PikPak's membership endpoint; they do not upload audio or video identifiers. See the [usage guide](https://github.com/tonyflux/pkp/blob/v0.6.0/docs/USAGE.en.md#playback-and-reading) and [privacy notice](https://github.com/tonyflux/pkp/blob/v0.6.0/PRIVACY.en.md).

Daily active installation statistics have defaulted to on since v0.3.0; explicit opt-outs are preserved. Settings → Privacy & Security can disable them, including before sign-in. Reports contain a random installation ID, version, OS/architecture, language and local activity dates—not accounts, filenames, paths, searches, file contents or feature-use counts. Opt-out deletes local pending reports, not server history. No configured endpoint means nothing is recorded or sent and no notice is shown.

### Release assets and limitations

The release contains **eight assets**: the macOS Universal installer (macOS 12+, Apple silicon/Intel), combined Windows x64/ARM64 installer, signed update manifest and signature, `PKP-0.6.0-source.tar.gz`, `libmpv-sources.tar.gz`, `LICENSE`, and `SHA256SUMS`. The existing Ed25519 update trust is retained; updates are not automatically downloaded or silently installed. PKP is an unofficial client; PKP-owned source is GPL-3.0-only.

AI subtitles are unavailable on Windows, which lacks the built-in audio decoder. macOS is ad hoc signed and not Apple-notarized; Windows lacks Authenticode signing and remains preview support. No Linux packages. Full native installation and upgrade, real-device matrix, and production-cloud acceptance have not been completed. Do not post credentials, private links or complete configuration directories in feedback.

[Installation](https://github.com/tonyflux/pkp/blob/v0.6.0/docs/INSTALL.en.md) · [Usage](https://github.com/tonyflux/pkp/blob/v0.6.0/docs/USAGE.en.md) · [Privacy](https://github.com/tonyflux/pkp/blob/v0.6.0/PRIVACY.en.md) · [Full changelog](https://github.com/tonyflux/pkp/compare/v0.5.0...v0.6.0)

---

## 简体中文

本版本为 macOS 播放器增加可选的 PikPak AI 字幕。

### 新功能与修复

- **macOS AI 字幕**：在播放器字幕菜单选择原文、中文、英文、日文或韩文，没有内嵌字幕的视频也可使用。字幕由 PikPak 根据视频音频生成，无需聊天模型 API Key；字幕跟随播放、快进和暂停，沿用字幕样式与延迟设置。
- **会员与配额**：按播放视频所属账号检查资格，非会员仍可使用普通字幕，读取失败可重试。字幕菜单和播放设置在有数据时显示服务端最近返回的剩余时长和重置时间。会员资格、可用语言、配额、服务可用性及同一账号只能同时使用一处的限制均由 PikPak 决定。
- **会话交接**：快速切换语言、清晰度和音轨时安全释放上一会话；关闭字幕或退出时取消字幕自身的请求，不中断其他任务的共享鉴权刷新。服务拒绝或断线时显示原因，不无限重连。

### 数据发送与隐私

AI 字幕默认关闭。开启后会向 PikPak 字幕服务发送播放账号的鉴权信息、视频 GCID／文件与媒体标识、目标语言，以及播放位置附近独立读取和解码的音频；额外读取会消耗网络流量。字幕在本机绘制，不自动保存到云盘。会员检查使用 PikPak 会员接口，不上传音频或视频标识。详见[使用说明](https://github.com/tonyflux/pkp/blob/v0.6.0/docs/USAGE.md#播放与阅读)和[隐私说明](https://github.com/tonyflux/pkp/blob/v0.6.0/PRIVACY.md)。

每日活跃安装统计从 v0.3.0 起默认开启，明确关闭的选择会保留，可在「设置 → 隐私与安全」关闭，登录前也可操作。报告仅包含随机安装编号、版本、系统／架构、语言和活跃本地日期，不包含账号、文件名、路径、搜索、文件内容或功能使用次数。关闭会删除本机待发送记录，不删除服务端历史；未配置收集地址时不记录、不发送、不提示。

### 发布附件与限制

本版本包含 **八件附件**：macOS Universal（macOS 12+，Apple 芯片／Intel）安装包、Windows x64／ARM64 合并安装包、更新清单及签名、`PKP-0.6.0-source.tar.gz`、`libmpv-sources.tar.gz`、`LICENSE` 和 `SHA256SUMS`。沿用既有 Ed25519 更新信任，更新不会自动下载或静默安装。PKP 为非官方客户端，PKP 自有源码适用 GPL-3.0-only。

Windows 缺少内置音频解码器，不提供 AI 字幕入口。macOS 为 ad hoc 签名，未通过 Apple 公证；Windows 无 Authenticode 签名，仍为预览支持。不提供 Linux 安装包。完整原生安装／升级、真实设备矩阵和生产云端验收尚未完成。反馈时请勿公开凭据、私人链接或完整配置目录。

[安装指南](https://github.com/tonyflux/pkp/blob/v0.6.0/docs/INSTALL.md) · [使用说明](https://github.com/tonyflux/pkp/blob/v0.6.0/docs/USAGE.md) · [隐私说明](https://github.com/tonyflux/pkp/blob/v0.6.0/PRIVACY.md) · [完整变更](https://github.com/tonyflux/pkp/compare/v0.5.0...v0.6.0)
