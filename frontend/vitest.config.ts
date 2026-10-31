import { resolve } from "node:path";
import { defineConfig } from "vitest/config";

// Pure-function tests, plus switching.test.ts, which replaces the services
// binding to drive the store. store.ts reads localStorage/matchMedia at
// import time, hence jsdom; the Wails runtime is stubbed so no socket is
// opened.
export default defineConfig({
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.ts"],
    setupFiles: ["src/test/setup.ts"],
    alias: { "@wailsio/runtime": resolve(__dirname, "src/test/wails-stub.ts") },
  },
});
