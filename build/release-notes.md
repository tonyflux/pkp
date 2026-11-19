# PKP 1.0.0

## English

PKP 1.0.0 brings a refined workspace and Linux desktop builds, alongside playback, account browsing and reliability improvements since 0.7.1.

- A softer theme-adaptive mist backdrop, refined light and dark workspaces, clearer file cards, navigation, refresh icon and playback controls. Custom wallpapers, panel opacity and reduced-transparency settings remain available.
- Starred markers now appear beside file and folder names instead of covering thumbnails.
- About & Updates now links to the public GitHub feature-request form with a privacy notice.
- Browse locally indexed files across signed-in accounts, with account ownership and incomplete-index warnings; drives and playback progress remain separate.
- Refined video and music playback, file previews, queues and collapsed playback controls, including previous/next video actions and retained subtitle membership and quota notices.
- Linux x86_64 and ARM64 builds provide deb, rpm and AppImage packages, native playback, safe synchronization and LAN sharing. Linux updates are verified before manual installation or replacement.
- Fix app-lock authentication bypass risks, video-surface shutdown deadlocks, Linux proxy and portal issues, and other platform reliability problems. Sign-in no longer submits developer referral attribution; previously established relationships are unchanged.

This draft is for validation before publication. The planned 16 assets include macOS Universal and Windows x64/ARM64 installers, signed update manifest and signature, source and media-source archives, license, checksums, and Linux deb/rpm/AppImage and runtime sources for both architectures. macOS is ad hoc signed and not Apple-notarized; Windows lacks Authenticode signing and remains preview support. Linux needs a compatible desktop, keyring and authentication agent; standalone AppImage does not install system polkit policy and cannot guarantee screenshot protection. Installation, upgrade and real-device acceptance remain to be verified.

[Installation](https://github.com/tonyflux/pkp/blob/v1.0.0/docs/INSTALL.en.md) · [Usage](https://github.com/tonyflux/pkp/blob/v1.0.0/docs/USAGE.en.md) · [Privacy](https://github.com/tonyflux/pkp/blob/v1.0.0/PRIVACY.en.md) · [Changes since 0.7.1](https://github.com/tonyflux/pkp/compare/v0.7.1...v1.0.0)

---

## 简体中文

PKP 1.0.0 在 0.7.1 基础上更新桌面工作区，增加 Linux 桌面构建，并改进播放、跨账号浏览及稳定性。

- 新增随主题变化的柔和雾面背景，精修浅色和深色工作区、文件卡片、导航、刷新图标与播放控件；保留自定义壁纸、面板不透明度和降低透明度设置。
- 文件和文件夹的星标移至名称旁，不再遮挡缩略图。
- 「关于与更新」新增意见反馈入口，打开公开的 GitHub 功能建议表单前提示隐私风险。
- 支持汇集已登录账号的本地文件索引进行浏览，标明文件所属账号并提示索引可能不完整；云盘和播放进度仍分别管理。
- 优化视频、音乐播放、文件预览、播放队列及收起播放器，新增上一个／下一个视频控制，保留字幕会员及额度提示。
- 新增 Linux x86_64／ARM64 的 deb、rpm、AppImage 构建，提供原生播放、安全同步和局域网共享；更新先验证签名与哈希，再由用户手动安装或替换。
- 修复应用锁认证异常时可能跳过锁定、视频画面关闭时可能死锁，以及 Linux 代理和桌面门户等问题。登录不再提交开发者推广归因；此前已建立的关系不自动撤销。

本草稿供验证，尚未正式发布。计划提供 16 件附件：macOS Universal 与 Windows x64／ARM64 安装包、签名更新清单及签名、源码和媒体源码包、许可证、校验文件，以及两种架构的 Linux deb／rpm／AppImage 与运行库源码。macOS 为 ad hoc 签名、未通过 Apple 公证；Windows 无 Authenticode 签名，仍为预览支持。Linux 需要兼容的桌面、密码库和认证代理；独立 AppImage 不自动安装系统 polkit 策略，也无法保证截图保护。安装、升级和真实设备验收仍待验证。

[安装指南](https://github.com/tonyflux/pkp/blob/v1.0.0/docs/INSTALL.md) · [使用说明](https://github.com/tonyflux/pkp/blob/v1.0.0/docs/USAGE.md) · [隐私说明](https://github.com/tonyflux/pkp/blob/v1.0.0/PRIVACY.md) · [0.7.1 以来的变更](https://github.com/tonyflux/pkp/compare/v0.7.1...v1.0.0)
