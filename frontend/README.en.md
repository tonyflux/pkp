# frontend

[简体中文](README.md) · English

Vue 3 + TypeScript, built with Vite. `npm run build` runs `vue-tsc` type checking, the Vite production build, and `scripts/reader-licenses.mjs` for frontend third-party notices.

- `src/store.ts` (along with feature modules such as `syncstore.ts`, `updatestore.ts`, `lanstore.ts`, `agent.ts`, and `shareimport.ts`): state and backend calls; `src/components/`: pages and components; `src/locales/`: five-language catalogs; `bindings/`: Go-generated TS—do not edit manually. This project's bindings live in `bindings/github.com/tonyflux/pkp/internal/`; run `wails3 generate bindings -clean=true -ts -i` from the repository root to regenerate them.
- `npm run dev`: develop in the browser (default `127.0.0.1:9245`); `/wails` and `/appearance` proxy to `PKP_SERVER` (default `http://localhost:8080`, e.g. `go build -tags server`).
- `npm test`: Vitest in `jsdom` across `src/**/*.test.ts` (stubbing `@wailsio/runtime`), covering pure functions, stores, and UI component tests.
- `npm run i18n`: runs `scripts/i18n-check.mjs` to verify translation keys, five-language completeness, and unused catalog entries.

For local builds, localization conventions, and tests, see the [development guide](../docs/DEVELOPMENT.en.md) and root [README.md](../README.md).
