# PKP 1.0.1

## English

- Windows: Reduce the top margin of the main workspace while retaining the native title bar. Player layouts and macOS are unchanged.
- Linux: Fix intermittent black video on X11 and Wayland by avoiding a faulty scaler path in older libmpv. The rendering checks cover frame colors, resizing while paused, screenshots, and moving playback between windows.

macOS builds are ad hoc signed and not notarized; Windows installers are not Authenticode-signed. On Linux, install the verified package manually or replace the AppImage after exiting PKP.

[Installation](https://github.com/tonyflux/pkp/blob/v1.0.1/docs/INSTALL.en.md) · [Changes since 1.0.0](https://github.com/tonyflux/pkp/compare/v1.0.0...v1.0.1)

---

## 简体中文

- Windows：收紧主工作区顶部留白，保留原生标题栏；播放器和 macOS 布局不变。
- Linux：避开旧版 libmpv 有缺陷的缩放路径，修复 X11 和 Wayland 下偶发的播放黑帧。渲染测试覆盖实际画面颜色、暂停时缩放、截图及窗口切换。

macOS 安装包为临时签名，未经过 Apple 公证；Windows 安装器未使用 Authenticode 签名。Linux 安装包通过验证后需手动安装，或退出 PKP 后替换 AppImage。

[安装指南](https://github.com/tonyflux/pkp/blob/v1.0.1/docs/INSTALL.md) · [相对 1.0.0 的变更](https://github.com/tonyflux/pkp/compare/v1.0.0...v1.0.1)
