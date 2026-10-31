import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import wails from "@wailsio/runtime/plugins/vite";

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
    // Browser-only development against `go build -tags server` (listening on
    // :8080): the Wails runtime talks to same-origin /wails/*, and the custom
    // wallpaper is served under /appearance, so forward both.
    proxy: {
      "/wails": { target: process.env.PKP_SERVER || "http://localhost:8080", ws: true, changeOrigin: true },
      "/appearance": { target: process.env.PKP_SERVER || "http://localhost:8080", changeOrigin: true },
    },
  },
  plugins: [vue(), wails("./bindings")],
});
