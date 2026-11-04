# PKP 0.3.0

扫码登录、Google Gemini API Key 支持与登录体验修复。

## 新功能与修复

- **PikPak App 扫码登录**：首次登录、添加账号和重新登录默认提供二维码；手机确认后完成登录，仍可切换密码登录。修正登录错误分类，不再把未知服务错误一律提示为账号或密码错误。回应 [#3](https://github.com/tonyflux/pkp/issues/3)。
- **登录可靠性**：修正扫码客户端身份、会话恢复、保存失败时的原会话保护，以及临近二维码过期时丢失成功结果的问题；密码框只保留一个显示／隐藏按钮。
- **Google Gemini API Key 预设**：在「设置 → AI」选择 Google Gemini，填写 Google AI Studio API Key，选择服务端返回的模型或手动输入模型 ID。直连 Google 官方 OpenAI 兼容接口，无需第三方代理；支持 AI 助手与 AI 文件整理。模型目录不等于账号可用权限或配额，Google API 计费独立于 Gemini 订阅，不支持 Google 账号／订阅 OAuth 登录。回应 [#2](https://github.com/tonyflux/pkp/issues/2)。
- **社区与反馈**：「关于与更新」新增 Telegram 交流群及 GitHub 项目入口，登录前后均可访问。
- **助手提醒**：只在新入库文件已有可处理的整理建议时提醒，减少无效角标与通知。
- **macOS 应用内更新**：下载更新后，经确认退出再替换应用并重新打开原配置；保留账号和设置，不自动下载或静默安装。

## 重要：使用统计默认值变化

**v0.3.0 默认开启每日活跃安装统计；v0.2.0 默认关闭。** 升级保留磁盘中可识别的明确关闭选择；此前未明确选择的配置随新默认开启，并显示一次说明。旧版若没有保存任何拒绝字段，无法与「从未选择」区分。

- 只发送随机安装编号、版本、系统及架构、界面语言和活跃本地日期；不再发送功能明细、使用次数或运行日志，也不包含账号、文件名、路径、搜索内容或文件内容。
- 可随时在「设置 → 隐私与安全」关闭；**登录前也能打开该设置**。提示框的「知道了」及关闭只表示已阅，不关闭统计，也不代表同意授权。记录从启动开始，约 30 秒后可发送，不等待提示已阅。
- 关闭删除本机编号与待发送数据，不会删除服务端已收到的数据。统计反映成功上报的活跃安装，不是真实人数或精确安装量。应用 Release 不会自动部署配套统计服务。

## 下载、校验与限制

提供 macOS Universal（macOS 12+，Apple 芯片／Intel）与 Windows x64／ARM64 合并安装包。八件附件包括两个安装包、签名更新清单及签名、`PKP-0.3.0-source.tar.gz`、第三方对应源码、`LICENSE` 和 `SHA256SUMS`。PKP 自有源码适用 GPL-3.0-only。

macOS 仍使用 ad hoc 签名，未经 Apple 公证；Windows 没有 Authenticode 签名，仍为预览支持，原生安装／升级及完整设备矩阵尚未全面验收。不提供 Linux 安装包。请勿在反馈中附上密码、token、API Key 或完整配置目录。

[安装指南](https://github.com/tonyflux/pkp/blob/v0.3.0/docs/INSTALL.md) · [隐私说明](https://github.com/tonyflux/pkp/blob/v0.3.0/PRIVACY.md) · [完整变更](https://github.com/tonyflux/pkp/compare/v0.2.0...v0.3.0)

---

## English

### Highlights

- **PikPak QR sign-in** for initial sign-in, adding accounts and reauthentication, with password sign-in still available. Unknown authentication failures no longer incorrectly claim that the password is wrong. Addresses [#3](https://github.com/tonyflux/pkp/issues/3).
- **More reliable sign-in**: retain the issuing client identity across refresh/restart, preserve existing sessions if QR persistence fails, accept successful in-flight results at expiry, and show only one password-reveal icon.
- **Google Gemini API-key preset**: select Google Gemini in Settings → AI, enter a Google AI Studio key, and select a returned model or enter its ID manually. Connects directly to Google's official OpenAI-compatible endpoint without a third-party proxy; works with the assistant and AI file organization. Listing a model does not guarantee account access or quota. API billing is separate from Gemini subscriptions; Google-account/subscription OAuth sign-in is not included. Addresses [#2](https://github.com/tonyflux/pkp/issues/2).
- **Community & Feedback** links to Telegram and GitHub, available before and after sign-in.
- **Quieter assistant reminders**, shown only for actionable organization suggestions.
- **macOS in-app updates** download only on request, replace the app after quit confirmation and reopen the same profile with accounts/settings preserved.

### Important privacy change

**Daily active installation statistics default to ON in v0.3.0; v0.2.0 defaulted to OFF.** Identifiable explicit opt-outs are preserved. Existing settings with no explicit choice follow the new on default and receive a one-time notice. An old save containing no refusal marker cannot be distinguished from no choice.

Reports contain a random installation ID, version, OS/architecture, language and active local dates—not feature actions, frequencies, application logs, accounts, filenames, paths, searches or file contents. Turn them off in Settings → Privacy & Security, also accessible before sign-in. Got it and dismissal only acknowledge the notice; they are not consent and leave statistics enabled. Recording starts at startup and sending can begin about 30 seconds later, before acknowledgment. Opt-out deletes the local ID and pending reports, not server history. Counts are observed active installations, not verified people or exact installs. Publishing this app does not deploy companion telemetry services.

### Packages and limitations

macOS Universal (macOS 12+, Apple silicon/Intel) and a combined Windows x64/ARM64 installer. Eight assets include both installers, the signed update manifest and its signature, corresponding PKP and third-party source archives, LICENSE and SHA256SUMS. PKP-owned source is GPL-3.0-only.

macOS remains ad hoc signed and not notarized; Windows has no Authenticode signature and remains preview support. Native installation/upgrade and a complete device matrix have not been fully validated. No Linux packages. Never post passwords, tokens, API keys or the full configuration directory in feedback.

[Installation](https://github.com/tonyflux/pkp/blob/v0.3.0/docs/INSTALL.en.md) · [Privacy](https://github.com/tonyflux/pkp/blob/v0.3.0/PRIVACY.en.md) · [Full changelog](https://github.com/tonyflux/pkp/compare/v0.2.0...v0.3.0)
