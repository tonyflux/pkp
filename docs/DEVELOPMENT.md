# 本地构建

简体中文 · [English](DEVELOPMENT.en.md)

Copyright (C) 2026 tonyflux. 构建与运行本仓库源码适用根目录 [GPL-3.0-only](../LICENSE)。

## 准备

- 下列入门命令针对 macOS 12+；Linux 构建见下文独立章节。macOS 产物同时包含 Apple silicon 和 Intel。
- 安装 Go 1.26、Node.js 24、Xcode Command Line Tools、CMake 和 Python 3.10 或更新版本；确保 `go`、`node`、`npm`、`cmake`、`python3` 在 `PATH` 中。构建 libmpv 需要网络下载源码和工具。
- 安装固定版本的 Wails CLI，并将 `$(go env GOPATH)/bin` 加入 `PATH`。即使 `make app-go` 使用 Go 构建，它的前端目标仍需要 `wails3` 生成绑定。
- 签名私钥只存在仓库外的受保护文件或发布环境 secret 中，不要提交。

在仓库根目录执行（首次安装 Xcode Command Line Tools 时按提示完成安装）：

```sh
xcode-select --install
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.28
export PATH="$(go env GOPATH)/bin:$PATH"
cd frontend && npm ci && cd ..
make app-go
```

`make app-go` 会生成绑定、构建前端，并产出 `bin/pkp` 与 `bin/pkp.app`，其中 `Contents/Helpers/pkp-updater` 是独立的 macOS 12+ Universal 更新助手，由 Xcode 自带的 `swiftc` 构建。直接运行 `./bin/pkp` 前，先退出已经打开的旧 PKP；自动替换更新仅适用于已安装的 `.app`。

Go module 是 `github.com/tonyflux/pkp`。前端绑定在 `frontend/bindings/github.com/tonyflux/pkp/`，由 Wails 生成，不要手改。

运行 `make test` 执行 Go 和前端单元测试；`make dev` 使用 Wails 启动热重载开发模式。

崩溃反馈接收端位于 `cloudflare/telemetry`。本地运行 `npm ci && npm run dev`，使用隔离测试报告并设置 `PKP_CRASH_API=http://127.0.0.1:8788` 启动桌面开发构建；服务端构建不读取宿主机崩溃日志。上线前在该目录运行 `npm run deploy`，先执行 D1 迁移再部署采集 Worker 和后台 Worker；确认后台的 Cloudflare Access 策略实际启用，并验证每日 cron。开发者在 `https://pkp-telemetry-admin.pkpapp.cc/crashes` 查看或删除报告。应用发布本身不会部署这两个 Worker；未部署或不可达时发送会失败并允许手动重试。

AI 字幕在 macOS 与 Windows 共用 FFmpeg 音频解码源码：macOS 使用现有 libmpv 构建导出的头文件与动态库；Windows x64／ARM64 由固定工具链将同一源码与静态 FFmpeg 链接为 `pkp-pcm.dll`，安装在 `pkp.exe` 旁，由无 cgo 的 Go 程序按绝对路径加载，不从当前工作目录或 PATH 查找。生产解码不启动 `ffmpeg` 子进程，不依赖用户安装工具；解码回归测试用该命令生成本地合成样本。应用默认使用 PikPak 的 `https://api-drive.mypikpak.com` 发现字幕节点，只在用户开启后发起请求。隔离开发可设置 `PKP_SUBSRELAY_API=http://127.0.0.1:<端口>`；显式设为空可禁用功能。自定义地址可能收到账号令牌与音频，不要使用不可信服务；URL Basic Auth 可替代账号令牌。其他没有音频解码器的平台隐藏字幕入口。

账号鉴权部署会用播放账号查询会员资格，前后端均限制非会员开启；URL Basic Auth 开发部署不执行 PikPak 会员门禁，由该部署决定资格。`AISubtitleState.quota` 保留最近同账号的服务端快照，`known` 区分未知与真实零额度；`limit`／`usage` 为秒，`nextResetTime` 为 RFC 3339。未启用时不为显示额度请求节点；过期或取消的会话不能覆盖新状态。

HTTPS 字幕部署只接受加密节点，节点发现和 WebSocket 握手均拒绝降级到明文的重定向；本地明文服务须显式使用 HTTP 开发地址。

macOS 更新助手验证：

```sh
python3 build/darwin/updater-protocol-test.py bin/pkp-updater
python3 build/darwin/updater-smoke-test.py bin/pkp-updater
```

第一项不启动应用；第二项需要已登录的 macOS 桌面，使用隔离、签名、无窗口的测试应用实际挂载 DMG、替换、重开和回滚，不启动真实 PKP 或读取真实账号配置。系统替换授权的允许/取消、真实应用退出确认及 Gatekeeper 仍需人工验收。助手日志与安装结果位于当前配置对应的用户缓存 `pkp/updates/<配置摘要>/`，回滚不确定时保留旧版备份并在 `install.log` 中记录位置。

## Windows 交叉编译

上述 macOS 宿主可运行 `make windows` 生成 x64 和 ARM64 可执行文件；它们不含运行时 `libmpv-2.dll` 与 `pkp-pcm.dll`。运行 `make windows-installer` 生成包含对应架构 DLL 的安装包，另需安装 NSIS（`makensis` 在 `PATH` 中）。DLL 构建会下载固定版本的 macOS llvm-mingw 工具链及源码，并需要上述 CMake、Python 和网络；这不是 Windows 原生构建指南。手工运行 Windows 二进制或解码测试时，须把同架构的 `build/windows/libmpv/<架构>/pkp-pcm.dll` 放在可执行文件旁；解码测试另需 `ffmpeg` 在 PATH 中生成样本（可直接使用同目录下的 `ffmpeg.exe`）。

CI 复用 macOS 构建产物，在 Windows x64 和 ARM64 上运行同一套 PCM 时间轴、音轨、快进及取消恢复测试，以及字幕会话、会员与配额集成测试。兼容层测试不替代真机播放器界面和安装升级验收。

`pwsh -File build/windows/update-e2e.ps1` 在隔离的临时目录和专用 HKCU 项中编译并验证用户级安装、原目录更新、实例占用、失败回滚、恢复失败和安装包文件锁；需要 NSIS 在 PATH 中。`go test . -run '^TestWindowsWatcherHandoff$'` 使用隔离子进程验证观察器的成功、占用、安装失败、回滚失败及旧协议分支。CI 在 Windows x64 与 ARM64 运行这些测试；它们不模拟机器级 UAC 授权或真实 PKP 的完整升级。

## Linux 构建

使用 Ubuntu 24.04 的 x86_64／ARM64 原生环境，或 `build/docker/Dockerfile.linux` 的匹配架构容器；不能只设置 `GOOS` 跳过 CGO／系统头文件。安装 Go 1.26、Node.js 24、固定 Wails CLI 和 nFPM v2.43.0；系统开发依赖由该 Dockerfile 列明，包含 GTK4、WebKitGTK 6.0、libmpv、FFmpeg 和 libepoxy。随后运行：

```sh
npm --prefix frontend ci
make linux-package ARCH=arm64
# x86_64 宿主使用 ARCH=amd64；跨架构请在匹配架构容器内构建。
```

输出为 `bin/PKP-<版本>-linux-<架构>.deb`、`.rpm`、`.AppImage` 与 `-runtime-sources.tar.gz`。deb 使用系统依赖；RPM 与 AppImage 包含 WebKit 的子进程、资源和动态库闭包，使用用户命名空间保持 WebKit 编译时的 `/usr` 路径，不关闭沙箱。构建需启用 Ubuntu `deb-src`，保留 APT 索引，并下载与实际捆绑包版本一致的对应源码及许可证；不能用 macOS／Windows 的固定媒体源码包替代它。

Linux CI 在打包前运行 X11／Wayland 图形测试，并批量下载运行库对应源码。CI 只缓存与固定构建环境和架构匹配的 Ubuntu 源码下载；每次打包仍从签名索引解析准确版本、校验每个文件的哈希，再验证新产物。

发布前在干净、已提交的同一源码快照分别为两个架构执行打包与 `make linux-provenance ARCH=<架构>`；在容器中使用 `make linux-docker-provenance ARCH=<架构>`。来源校验会检查 deb、rpm、AppImage 内的源码提交，并核对随包运行时与对应源码归档；旧包或改动过的包不能用于发布。普通开发构建允许未提交更改，但不能作为发布包。来源文件只用于发布预检，不是防止人为伪造的签名证明。

```sh
go test ./internal/mpv ./internal/pcm ./internal/audiotags ./internal/lanmedia
PKP_TEST_VIDEO_SURFACE=1 xvfb-run -a go test -tags pkp_surface_test ./internal/mpv -run TestLinuxSurface -count=1
```

Wayland 在 headless Weston 下运行同一表面测试；CI 同时检查两种协议的帧像素、截图及换窗。真实 GNOME／KDE 的密码库、polkit 认证、通知、图形驱动、休眠和安装升级仍需桌面验收。隔离测试必须设置独立 `PKP_CONFIG_DIR`，不要读写真正账号配置。Linux AI 字幕复用 `internal/pcm/decoder.c`，通过 pkg-config 链接 FFmpeg，不启动解码子进程。LAN 兼容副本使用系统 `/usr/bin/ffmpeg`／`ffprobe` 或 AppImage 私有工具，不搜索 CWD／PATH；Linux H.264 编码使用发行版提供的 libx264。

Linux 文件系统信任边界使用目录描述符、statx／文件系统身份和不跟随链接的操作；递归 inotify 事件触发复核，队列溢出会重新扫描。支持普通 ext4、XFS、Btrfs、tmpfs，拒绝不能可靠表示的网络／FUSE／overlay 与目录折叠语义。容器 CI 为测试挂载独立 tmpfs `/tmp`，不靠「不支持」跳过已有安全回归；回收站不可用时明确失败，不永久删除。

Linux 6.8 可能在立即同大小写入时保留相同 ctime／mtime，不能仅用时间戳授权复用旧哈希。因此 Linux 扫描版本包含内容摘要，同次检查复用摘要、稳定性复核再次读取；大型媒体目录扫描仍有两遍文件读取开销。不要以延时测试或弱化文件身份检查掩盖该限制。

局域网持久文件令牌也对内容做一次指纹验证；持续传输在读取前后检查绑定文件描述符的 inotify 证明，改写、属性变更或事件溢出即撤销，不在每个网络分段重复哈希整部视频。

## 仓库语言约定

代码注释、开发者诊断、构建输出、内部模型指令和非多语言管理后台使用英文。公开指南提供互链的中英文版本。

应用文案继续使用现有翻译系统：前端 `t` / `N_` 和 Go 的 `internal/i18n` 使用简体中文源文案作为键。模型指令改用英文，但明确要求按所选界面语言回答。

中文输入语法、中文命名预设、语言自称、持久化路径的规范标签，以及验证 Unicode 或本地化行为的测试样例属于语言数据，不应机械翻译，否则会破坏语言支持或已有数据解释。v0.1.0 历史法律条款和中文界面截图保持原样。不得添加扫描豁免来掩盖非翻译文字。


## 发布材料

面向用户的更新说明首行必须是 `# PKP <版本>`。在仓库根目录运行：

```sh
make release-local RELEASE_NOTES=build/release-notes.md
make release-verify
```

历史八件附件保持可验证。新发布加入用户级 Windows 安装包；含 Linux 的发布再加入六个安装包和两个逐架构运行库源码包。签名清单保留 `packages` 中的旧 macOS／Windows 两项，通过 `windowsUserPackage` 和 `linuxPackages` 认证新增附件，旧客户端继续兼容。在 macOS 汇总两种架构 Linux 的构建输出时，传入 `LINUX_RELEASE_DIR=/路径/同一源码构建的Linux附件`；发布前应提交为干净源码快照。构建／签名不等于上传，也不表示平台真机验收或 Apple 公证完成。私钥不得提交或写入日志。

## 不纳入本仓库的内容

编辑器工程、依赖缓存、构建产物、本机账号配置和私有 Git 历史不属于源码。内部发布运行编号、本机 SSH 身份和签名私钥位置也不在此说明中。
