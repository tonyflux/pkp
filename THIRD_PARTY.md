# 第三方组件与服务

简体中文 · [English](THIRD_PARTY.en.md)

PKP 自有源码采用 [GPL-3.0-only](LICENSE)。已发布的 v0.1.0 安装包仍适用[当时的闭源条款](docs/legal/v0.1.0-proprietary.txt)。第三方组件的权利与义务由其各自许可证决定。

## 应用组件

主要使用 Wails、Vue、Go 及其依赖、本地 SQLite 索引、阿里云 OSS SDK，以及 mpv / libmpv、FFmpeg、libplacebo、libass 等媒体组件。此处仅为简要说明，**不是完整依赖清单，也不代表许可证审计已经完成**。具体版本、版权与许可证应以实际发布包所附材料为准。

当前媒体库构建配置采用 LGPL 路径：mpv 禁用 GPL 部分，FFmpeg 不启用 GPL 选项；libmpv 以单独动态库分发。每个正式 Release 必须同时附上与实际二进制对应的 `libmpv-sources.tar.gz`，包含相关第三方源码与构建脚本。该源码包不代替 PKP 自有源码；v0.1.0 的安装包也不是由本仓库当前源码构建。

局域网兼容副本使用随应用分发的 `ffmpeg` / `ffprobe`，不调用系统 PATH 中的程序。macOS 随包提供通用 arm64/x86_64 工具（`Contents/Helpers`），Windows 安装对应架构的工具到应用目录。工具与播放器使用同一固定 FFmpeg 源码，明确禁用 GPL/nonfree；H.264 编码使用 macOS VideoToolbox（允许系统软件实现）或 Windows Media Foundation 软件实现，AAC 使用 FFmpeg 内置编码器，不包含 libx264。上述源码包和许可证同时覆盖这两个程序；系统编码器是否可用须在目标电脑验证，不支持时转换明确失败。

Windows AI 字幕通过随包的 `pkp-pcm.dll` 解码音频，与 macOS 共用 PKP 的解码源码，静态链接同一固定版本、禁用 GPL/nonfree 的 FFmpeg。第三方对应源码包包含这些库、解码源码与构建脚本，应用目录的 `licenses` 保留相应声明；不调用系统 FFmpeg。

v0.2.0 起另附 `PKP-<版本>-source.tar.gz`：包含与签名更新清单记录的 Git 提交及 tree 对应的 PKP 完整自有源码、资源、锁文件和构建脚本，以及 `third_party/` 中固定 Go 模块源码、npm 发布包和可核实的上游首选源码。清单记录校验值，并提供隔离依赖缓存的恢复说明；不包含 `.git`、账号数据或私钥。系统 SDK、编译器与打包工具仍须按开发指南安装。不能把 npm 中的可选原生构建工具二进制称为其完整源码，也不承诺逐字节可复现构建。

由当前源码构建的安装包中，第三方声明位于 macOS 应用包的 `Contents/Resources/licenses/`（Finder 中选择“显示包内容”），或 Windows 安装目录的 `licenses`。`GO_LICENSES.txt` 收录 Go 工具链及 `go list` 为两个发布架构选出的生产依赖中的许可证、版权、专利与声明文件，包括 modernc 的 SQLite 和 libc 等上游继承声明。`THIRD_PARTY_LICENSES.txt` 由已安装的前端生产依赖和 pdfjs-dist 资源许可证生成；其余许可证目录用于媒体组件。PKP 自身许可证位于 Resources 或安装目录的 `LICENSE.txt`。

当前 `isarray` 1.0.0 npm 包在 README 中包含 MIT 正文，生成器会收录；Wails 的 Go 模块另为 `@wailsio/runtime` 提供 MIT 正文。可选原生 `@napi-rs/canvas` 平台包未附许可证文件；安装该版本时生成器会收录 [v0.1.100 上游 MIT 正文](https://github.com/Brooooooklyn/canvas/blob/v0.1.100/LICENSE)。发布前应确认这些包的内容是否进入分发的前端。

扫码登录使用 `qrcode-generator` 1.4.4（MIT）在本机编码二维码，不调用外部二维码图片服务。npm 包仅在 `qrcode.js` 文件头保留版权及 MIT 链接，构建时另将该声明与 MIT 正文写入 `THIRD_PARTY_LICENSES.txt`。

当前源码已移除内置 PPT/PPTX 预览及 `pptx-preview` 依赖，因此其许可冲突不再是当前源码的发布阻断。此前使用的 `pptx-preview` 1.0.7 元数据声明 ISC，但包内 README 及[上游仓库许可证](https://github.com/501351981/pptx-preview/blob/main/LICENSE)限制源码发布和开源；移除依赖不代表已确认其 GPL 兼容性，也不追溯解决历史副本或既有分发的许可义务。v0.1.0 的实际组成与权利仍须以该版本的材料单独审查。

Go 依赖图及声明文件在构建时选取，不能代替对源码内声明、资源来源、许可证兼容性和实际发布二进制的审查。默认壁纸已找到 AI 概念图提示词与后续裁切、调色的记录；应用标志则基于用户提供的 PikPak 图案，并非独立原创的 AI 标志。维护者已确认当前标志可随开源项目和安装包公开及分发，不再将其列为待确认的发布阻断。具体证据、哈希及尚未证明的导出链见[美术素材来源](docs/legal/artwork-provenance.md)。维护者的确认不表示 PikPak 背书或独立法律审计，也不替代对其他资源及生成服务条款的核实。LGPL 覆盖的库可按适用许可证替换，但可能影响兼容性。

**v0.1.0 安装包及对应源码包已在 [Release](https://github.com/tonyflux/pkp/releases/tag/v0.1.0) 提供。** 已核验归档中 28 项固定源码的哈希及构建脚本一致性，但这不是完整依赖或法律审计，不能仅以此文代替合规审查。

## 认证材料的公开比对

当前 Android 身份参数中的 ClientID、ClientSecret、包名、客户端版本和 User-Agent，与公开项目 `52funny/pikpakapi` 固定提交 [`d105d48371a0df747673e44c00cec170984c9f6b` 的 `pikpak.go`](https://github.com/52funny/pikpakapi/blob/d105d48371a0df747673e44c00cec170984c9f6b/pikpak.go)及同一提交的 [`pikpak_captcha_token.go`](https://github.com/52funny/pikpakapi/blob/d105d48371a0df747673e44c00cec170984c9f6b/pikpak_captcha_token.go)一致。后者还包含与当前实现逐项、按顺序一致的 9 项 salts（含 1 项空字符串），以及相同的种子拼接顺序、逐轮 MD5、小写十六进制编码和 `1.` 签名前缀。该比对未核实 `ClientVersionCode` 的公开来源。

公开项目 `Quan666/PikPakAPI` 固定提交 [`c233bd3db99ecd49fe2e42ec8a2d83072e3a9432` 的 `utils.py`](https://github.com/Quan666/PikPakAPI/blob/c233bd3db99ecd49fe2e42ec8a2d83072e3a9432/pikpakapi/utils.py)也包含相同的 ClientID、ClientSecret 和包名，但使用不同版本的 salts，不能据此证明当前 salts 一致。

这些链接仅记录可复核的公开比对证据，不声明它们是本项目的实际原始来源，也不证明第三方源码复制许可、PikPak 的背书或生产服务当前接受这些参数。提交元数据日期不等于首次公开时间。维护者已确认当前身份参数可公开及复用；本版本保留这些参数与签名算法，不再将其列为待确认的发布阻断。该确认不等于独立法律审计，公开副本也不能替代来源链记录。

扫码登录使用独立的 Web 公共客户端 `YUMx5nI8ZU8Ap8pm`，不使用 Android ClientSecret。包名 `mypikpak.com`、客户端版本 `2.0.0` 和有序的 15 项 captcha salts 来自官方网页 build `89b307c6` 的公开脚本 [`3877.fc93f11b.js`](https://mypikpak.com/drive/3877.fc93f11b.js)；登录客户端 ID 也见同一构建的 [`3765.141625f3.js`](https://mypikpak.com/drive/3765.141625f3.js)。此处复用公开身份和签名参数，不复制网页 SDK 实现；公开可访问不代表 PikPak 授权或背书，服务端仍可能改变协议。Web 会话在本机保留签发客户端信息，续期与恢复时不改用 Android 身份。

## 外部服务与名称

- PikPak 是外部云盘服务，账号、配额与内容处理受其条款约束。PKP 是非官方客户端，不获得或暗示 PikPak 的背书。
- 可选 AI 字幕使用 PikPak 的字幕节点发现与 WebSocket 服务，发送音频及视频标识，使用当前播放账号的鉴权和额度；它不是用户配置的聊天模型服务。PKP 不承诺其准确性、可用性或服务端数据保留策略，详见[隐私说明](PRIVACY.md)。
- AI 模型服务由用户自行配置，其计费、数据处理与使用条件取决于所选服务。
- TMDB 仅用于用户启用的影视整理片名匹配，使用用户自己的凭据。This product uses the TMDB API but is not endorsed or certified by TMDB.

相关商标、服务名称与第三方作品属于各自权利人；文中的提及不表示合作、授权或认可。
