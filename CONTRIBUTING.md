# 参与贡献

简体中文 · [English](CONTRIBUTING.en.md)

感谢参与 PKP。请先搜索现有 issue 和 pull request；功能建议可通过 [功能建议模板](https://github.com/tonyflux/pkp/issues/new?template=feature_request.yml)提交，缺陷请使用缺陷报告模板。公开内容不要包含凭据、私人文件或账号信息。安全漏洞请勿公开提交，参见[安全政策](SECURITY.md)。

## 开发和提交

1. 按[本地构建指南](docs/DEVELOPMENT.md)安装依赖，在仓库根目录执行 `cd frontend && npm ci && cd ..`、`make app-go` 和 `make test`。指南也说明 macOS 宿主和 Windows 交叉编译的限制。
2. 从当前代码创建分支，保持改动聚焦；必要时补充测试，并遵循开发指南中的语言约定。不要手改生成的 Wails 绑定。
3. 提交 pull request，说明问题、解决方式、测试命令及结果；界面更改可附脱敏截图。标明未完成的验证和相关 issue。

提交贡献即表示你有权按本仓库的 [GPL-3.0-only](LICENSE) 许可证提供这些改动。引入或分发第三方代码、资源和依赖时，请确认许可证兼容并保留所需版权和许可证声明。
