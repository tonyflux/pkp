import { createApp } from "vue";
import App from "./App.vue";
import MenuBarWindow from "./MenuBarWindow.vue";
import PlayerWindow from "./PlayerWindow.vue";
import ViewerWindow from "./ViewerWindow.vue";
import * as store from "./store";
import "./style.css";

declare global {
  interface Window {
    // Dev only: lets a browser session drive the store (simulate a playback
    // session, select files) without a native player behind it.
    pkp?: typeof store;
  }
}

if (import.meta.env.DEV) window.pkp = store;

// UI language, in every window; the last choice draws the first frame.
void store.watchLanguage();

// Secondary windows share the entry point, not the main browsing state.
createApp(store.inMenuBarWindow ? MenuBarWindow : store.inPlayerWindow ? PlayerWindow : store.inViewerWindow ? ViewerWindow : App).mount("#app");
