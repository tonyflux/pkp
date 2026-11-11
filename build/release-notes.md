# PKP 0.6.1

## English

This release adds optional PikPak AI subtitles on Windows x64 and ARM64, alongside the existing macOS support.

Choose Original, Chinese, English, Japanese or Korean in the player's subtitle menu, including for videos without embedded subtitles. PikPak generates captions from the audio; no chat-model API key or separate FFmpeg installation is required. Membership eligibility, quota, available languages and service availability are controlled by PikPak. Captions follow playback, seeking and audio-track changes and use the existing subtitle style and delay settings.

AI subtitles are off by default. Enabling them sends the playing account's authorization, video identifiers, selected language and audio near the playback position to PikPak's subtitle service. The additional media read uses network traffic. See the [usage guide](https://github.com/tonyflux/pkp/blob/v0.6.1/docs/USAGE.en.md#playback-and-reading) and [privacy notice](https://github.com/tonyflux/pkp/blob/v0.6.1/PRIVACY.en.md).

The release contains eight assets: macOS Universal and Windows x64/ARM64 installers, signed update manifest and signature, `PKP-0.6.1-source.tar.gz`, `libmpv-sources.tar.gz`, `LICENSE` and `SHA256SUMS`. macOS is ad hoc signed and not Apple-notarized; Windows lacks Authenticode signing and remains preview support. Native installation and upgrade, real-device matrix and production-cloud acceptance have not been completed. No Linux packages are provided.

[Installation](https://github.com/tonyflux/pkp/blob/v0.6.1/docs/INSTALL.en.md) · [Full changelog](https://github.com/tonyflux/pkp/compare/v0.6.0...v0.6.1)

---

## 简体中文

本版本在已有 macOS 支持的基础上，为 Windows x64 和 ARM64 增加可选的 PikPak AI 字幕。

在播放器字幕菜单选择原文、中文、英文、日文或韩文，没有内嵌字幕的视频也可使用。PikPak 根据音频生成字幕，无需聊天模型 API Key，也无需另外安装 FFmpeg。会员资格、配额、可用语言和服务可用性由 PikPak 决定。字幕跟随播放、快进与音轨切换，沿用现有字幕样式和延迟设置。

AI 字幕默认关闭。开启后会向 PikPak 字幕服务发送播放账号的鉴权信息、视频标识、目标语言以及播放位置附近的音频；额外读取会消耗网络流量。详见[使用说明](https://github.com/tonyflux/pkp/blob/v0.6.1/docs/USAGE.md#播放与阅读)和[隐私说明](https://github.com/tonyflux/pkp/blob/v0.6.1/PRIVACY.md)。

本版本包含八件附件：macOS Universal 和 Windows x64／ARM64 安装包、更新清单及签名、`PKP-0.6.1-source.tar.gz`、`libmpv-sources.tar.gz`、`LICENSE` 和 `SHA256SUMS`。macOS 为 ad hoc 签名，未通过 Apple 公证；Windows 无 Authenticode 签名，仍为预览支持。原生安装与升级、真实设备矩阵和生产云端验收尚未完成。不提供 Linux 安装包。

[安装指南](https://github.com/tonyflux/pkp/blob/v0.6.1/docs/INSTALL.md) · [完整变更](https://github.com/tonyflux/pkp/compare/v0.6.0...v0.6.1)
