# 第三方组件与服务

PKP 自有部分采用[闭源软件使用条款](LICENSE)；第三方组件的权利与义务由其各自许可证决定，不因 PKP 闭源而消失。

## 应用组件

主要使用 Wails、Vue、Go 及其依赖、本地 SQLite 索引、阿里云 OSS SDK，以及 mpv / libmpv、FFmpeg、libplacebo、libass 等媒体组件。此处仅为简要说明，**不是完整依赖清单，也不代表许可证审计已经完成**。具体版本、版权与许可证应以实际发布包所附材料为准。

当前媒体库构建配置采用 LGPL 路径：mpv 禁用 GPL 部分，FFmpeg 不启用 GPL 选项；libmpv 以单独动态库分发。每个正式 Release 必须同时附上与实际二进制对应的 `libmpv-sources.tar.gz`，包含相关第三方源码与构建脚本；该源码包不等于 PKP 自有应用源码。

安装包附第三方许可证：macOS 位于应用包的 `Contents/Resources/licenses/`，Windows 位于安装目录的 `licenses`。LGPL 覆盖的库允许按适用许可证替换，相关调试修改与逆向工程权利不受 PKP 自有软件条款限制。替换库可能影响兼容性，具体权利及条件请阅读随包的原始许可证。

**v0.1.0 安装包及对应源码包已在 [Release](https://github.com/tonyflux/pkp/releases/tag/v0.1.0) 提供。** 已核验归档中 28 项固定源码的哈希及构建脚本一致性，但这不是完整依赖或法律审计，不能仅以此文代替合规审查。

## 外部服务与名称

- PikPak 是外部云盘服务，账号、配额与内容处理受其条款约束。PKP 是非官方客户端，不获得或暗示 PikPak 的背书。
- AI 模型服务由用户自行配置，其计费、数据处理与使用条件取决于所选服务。
- TMDB 仅用于用户启用的影视整理片名匹配，使用用户自己的凭据。This product uses the TMDB API but is not endorsed or certified by TMDB.

相关商标、服务名称与第三方作品属于各自权利人；文中的提及不表示合作、授权或认可。
