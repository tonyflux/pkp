import { resolve } from "node:path";
import vue from "@vitejs/plugin-vue";
import { defineConfig } from "vitest/config";

// Pure-function, component and store tests run in jsdom; the Wails runtime is
// stubbed so no socket is opened.
export default defineConfig({
  plugins: [vue()],
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.ts"],
    setupFiles: ["src/test/setup.ts"],
    alias: { "@wailsio/runtime": resolve(__dirname, "src/test/wails-stub.ts") },
  },
});
