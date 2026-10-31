# frontend

[简体中文](README.md) · English

Vue 3 + TypeScript, built with Vite. `vue-tsc` performs type checking as part of `npm run build`.

- `src/store.ts`: all state and backend calls; `src/components/`: pages; `bindings/`: Go-generated TS—do not edit manually. This project's bindings are located in `bindings/github.com/tonyflux/pkp/internal/`; run `wails3 generate bindings -clean=true -ts -i` from the repository root to regenerate them.
- `npm run dev`: develop in the browser; `/wails` proxies to port 8080 of `go build -tags server`.
- `npm test`: vitest; tests only pure functions (`format.ts`, `scrub.ts`, and sorting/filtering in the store).

For build instructions, architecture, and design mockups, see `README.md` at the repository root.
