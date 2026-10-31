# frontend

简体中文 · [English](README.en.md)

Vue 3 + TypeScript，由 Vite 构建，`vue-tsc` 在 `npm run build` 里做类型检查。

- `src/store.ts`：全部状态与后端调用；`src/components/`：页面；`bindings/`：Go 生成的 TS，别手改。本项目的绑定位于 `bindings/github.com/tonyflux/pkp/internal/`；在仓库根目录运行 `wails3 generate bindings -clean=true -ts -i` 重新生成。
- `npm run dev`：浏览器里开发，`/wails` 代理到 `go build -tags server` 的 8080 端口。
- `npm test`：vitest，只测纯函数（`format.ts`、`scrub.ts`、store 里的排序/筛选）。

构建、架构与设计稿见仓库根目录 `README.md`。
