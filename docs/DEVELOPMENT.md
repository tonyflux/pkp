# 本地构建

简体中文 · [English](DEVELOPMENT.en.md)

Copyright (C) 2026 tonyflux. 构建与运行本仓库源码适用根目录 [GPL-3.0-only](../LICENSE)。

## 准备

- 以下命令针对 macOS 12 或更新版本，构建 Apple silicon 和 Intel 通用程序；本指南不保证其他宿主平台的原生构建。
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

macOS 更新助手验证：

```sh
python3 build/darwin/updater-protocol-test.py bin/pkp-updater
python3 build/darwin/updater-smoke-test.py bin/pkp-updater
```

第一项不启动应用；第二项需要已登录的 macOS 桌面，使用隔离、签名、无窗口的测试应用实际挂载 DMG、替换、重开和回滚，不启动真实 PKP 或读取真实账号配置。系统替换授权的允许/取消、真实应用退出确认及 Gatekeeper 仍需人工验收。助手日志与安装结果位于当前配置对应的用户缓存 `pkp/updates/<配置摘要>/`，回滚不确定时保留旧版备份并在 `install.log` 中记录位置。

## Windows 交叉编译

上述 macOS 宿主可运行 `make windows` 生成 x64 和 ARM64 可执行文件；它们不含运行时 libmpv DLL。运行 `make windows-installer` 生成包含 DLL 的安装包，另需安装 NSIS（`makensis` 在 `PATH` 中）。DLL 构建会下载固定版本的 macOS llvm-mingw 工具链及源码，并需要上述 CMake、Python 和网络；这不是 Windows 原生构建指南。

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

这只在本地生成八件附件，不上传：两个安装包、`update.json`、`update.json.sig`、`PKP-<version>-source.tar.gz`、`libmpv-sources.tar.gz`、`LICENSE` 和 `SHA256SUMS`。PKP 自有源码包须包含构建该版本安装包所用的完整对应源码和构建脚本；第三方对应源码单独提供。v0.2.0 已于 2026-11-01 在 GitHub Releases 发布，并带有上述八件附件。macOS 构建仍是 ad hoc 签名、尚未公证，设备验收也不完整。签名密钥不得提交，也不得写入日志。

## 不纳入本仓库的内容

编辑器工程、依赖缓存、构建产物、本机账号配置和私有 Git 历史不属于源码。内部发布运行编号、本机 SSH 身份和签名私钥位置也不在此说明中。
