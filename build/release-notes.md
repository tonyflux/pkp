# PKP 0.7.1

## English

This patch improves crash diagnostics and fixes native player-window lifetime risks on macOS.

- Unhandled Go panics, runtime fatal errors and recorded startup/Wails fatal errors produce a local `PKP-Crash-*.log` on the desktop, or in the system cache if the desktop is unavailable. Normal exits do not produce reports; forced termination, some native-library failures and very early startup failures may not produce one either.
- On the next launch, you can review and edit an available report before choosing whether to send it. Nothing is uploaded unless you select **Send report**. Don't send or closing keeps the local file and stops reminders for that report; sending failures can be retried manually. Feedback is independent of the anonymous usage-statistics setting.
- Reports may contain filenames, paths, links, account details or tokens. Remove sensitive information before sending. The report is processed by Cloudflare and accessible to the developer; a daily cleanup is configured for reports older than 29 days, though failures can delay deletion. Sent reports cannot be automatically recalled.
- Fix a macOS native-window lifetime race when reshaping the separate player during close or shutdown, and an error-message lifetime issue in the video surface. These are confirmed code risks; the cause of an individual reported crash still requires its diagnostics.
- The crash-report and usage-statistics dashboards now adapt to mobile screens, including report review, pagination and touch inspection of statistics charts.

The release contains eight assets: macOS Universal and Windows x64/ARM64 installers, signed update manifest and signature, `PKP-0.7.1-source.tar.gz`, `libmpv-sources.tar.gz`, `LICENSE` and `SHA256SUMS`. macOS is ad hoc signed and not Apple-notarized; Windows lacks Authenticode signing and remains preview support. Native installation and upgrade, real-device matrix and production-cloud acceptance have not been completed. No Linux packages are provided.

[Installation](https://github.com/tonyflux/pkp/blob/v0.7.1/docs/INSTALL.en.md) · [Usage guide](https://github.com/tonyflux/pkp/blob/v0.7.1/docs/USAGE.en.md#settings-and-quitting) · [Privacy](https://github.com/tonyflux/pkp/blob/v0.7.1/PRIVACY.en.md#signing-out-deletion-and-issue-reporting) · [Full changelog](https://github.com/tonyflux/pkp/compare/v0.7.0...v0.7.1)

---

## 简体中文

本补丁改进崩溃诊断，并修复 macOS 独立播放窗口的原生对象生命周期风险。

- 未处理的 Go panic、运行时致命错误和已记录的启动／Wails 致命错误，会在桌面生成 `PKP-Crash-*.log`；桌面不可写时使用系统缓存。正常退出不生成报告；强制结束、部分原生库故障或启动早期失败也可能无法生成。
- 下次启动时可审阅、编辑可用的报告，并选择是否发送。只有点击「发送报告」才会上传；选择「不发送」或关闭会保留本机文件且不再提醒该报告，发送失败可手动重试。崩溃反馈与匿名使用统计开关相互独立。
- 报告可能包含文件名、路径、链接、账号信息或令牌，请先删除敏感内容。发送后由 Cloudflare 处理，开发者可查看；服务端配置每日清理超过 29 天的报告，任务故障可能延迟删除。发送后无法自动撤回。
- 修复 macOS 独立播放窗口调整与关闭／退出并发时的原生窗口生命周期问题，以及视频表面错误信息的生命周期问题。这些是源码中确认的风险；单次闪退的具体原因仍需结合对应报告判断。
- 崩溃报告和使用统计后台适配手机屏幕，支持报告查看、翻页和统计图表触控查看。

本版本包含八件附件：macOS Universal 和 Windows x64／ARM64 安装包、更新清单及签名、`PKP-0.7.1-source.tar.gz`、`libmpv-sources.tar.gz`、`LICENSE` 和 `SHA256SUMS`。macOS 为 ad hoc 签名，未通过 Apple 公证；Windows 无 Authenticode 签名，仍为预览支持。原生安装与升级、真实设备矩阵和生产云端验收尚未完成。不提供 Linux 安装包。

[安装指南](https://github.com/tonyflux/pkp/blob/v0.7.1/docs/INSTALL.md) · [使用说明](https://github.com/tonyflux/pkp/blob/v0.7.1/docs/USAGE.md#设置与退出) · [隐私说明](https://github.com/tonyflux/pkp/blob/v0.7.1/PRIVACY.md#退出删除与问题反馈) · [完整变更](https://github.com/tonyflux/pkp/compare/v0.7.0...v0.7.1)
