# frontend

简体中文 · [English](README.en.md)

Vue 3 + TypeScript，由 Vite 构建；`npm run build` 会执行 `vue-tsc` 类型检查、Vite 生产构建以及生成第三方许可证清单（`scripts/reader-licenses.mjs`）。

- `src/store.ts`（以及 `syncstore.ts`、`updatestore.ts`、`lanstore.ts`、`agent.ts`、`shareimport.ts` 等功能模块）：状态与后端调用；`src/components/`：页面与组件；`src/locales/`：五语词表；`bindings/`：Go 生成的 TS，别手改。本项目的绑定位于 `bindings/github.com/tonyflux/pkp/internal/`；在仓库根目录运行 `wails3 generate bindings -clean=true -ts -i` 重新生成。
- `npm run dev`：浏览器里开发（默认 `127.0.0.1:9245`），`/wails` 与 `/appearance` 代理到 `PKP_SERVER`（默认 `http://localhost:8080`，例如 `go build -tags server`）。
- `npm test`：Vitest 在 `jsdom` 中运行 `src/**/*.test.ts`（stub `@wailsio/runtime`），覆盖纯函数、store 与组件测试。
- `npm run i18n`：运行 `scripts/i18n-check.mjs`，检查中文键名、五语翻译完整性及未使用的词条。

构建、多语言约定与测试说明见 [开发指南](../docs/DEVELOPMENT.md) 与根目录 [中文 README](../README.zh-CN.md)。
