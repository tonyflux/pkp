<script setup lang="ts">
import { onMounted, ref, watch } from "vue";
import { Window } from "@wailsio/runtime";
import FileViewer from "./components/FileViewer.vue";
import LockScreen from "./components/LockScreen.vue";
import { appLocked, bootstrapViewerWindow, dismiss, ui, useTelemetryActivity, viewer, watchAppLock } from "./store";
import { t } from "./i18n";

const ready = ref(false);
const failure = ref("");
useTelemetryActivity();
watch([() => viewer.entry?.name, appLocked], ([name, locked]) => {
  void Window.SetTitle(locked ? "PKP" : name || "PKP").catch(() => undefined);
}, { immediate: true });

async function start() {
  failure.value = "";
  try {
    await watchAppLock();
    await bootstrapViewerWindow();
    ready.value = true;
  } catch (err) {
    failure.value = err instanceof Error ? err.message : String(err);
  }
}
onMounted(start);
</script>

<template>
  <main class="viewer-window">
    <FileViewer v-if="ready" />
    <div v-else-if="failure" class="failure" role="alert">
      <p>{{ t("无法打开查看窗口：{error}", { error: failure }) }}</p>
      <button @click="start">{{ t("重试") }}</button>
      <button @click="Window.Close()">{{ t("关闭") }}</button>
    </div>
    <button v-if="ui.message && ui.messageTone === 'error'" class="toast" role="alert" @click="dismiss">{{ ui.message }}</button>
  </main>
  <LockScreen v-if="appLocked" />
</template>

<style scoped>
.viewer-window { position: relative; height: 100%; display: flex; background: #0b192b; }
.failure { margin: auto; padding: 24px; text-align: center; }
.failure button { margin: 4px; }
.toast { position: absolute; z-index: 5; bottom: 24px; left: 24px; max-width: calc(100% - 48px); color: var(--danger); background: var(--canvas); }
</style>
