<script setup lang="ts">
// The separate audio/video window mirrors the main window's playback session.
// Only video claims the native surface before mounting its stage.
import { nextTick, onMounted, ref, watch } from "vue";
import { Window } from "@wailsio/runtime";
import PlayerView from "./components/PlayerView.vue";
import MusicPlayer from "./components/MusicPlayer.vue";
import { PlayerService } from "../bindings/github.com/tonyflux/pkp/internal/services";
import LockScreen from "./components/LockScreen.vue";
import { appLocked, bootstrapPlayerWindow, dismiss, nowPlaying, returnToMain, ui, watchAppLock } from "./store";
import { t } from "./i18n";

const ready = ref(false);
const failure = ref("");

// The page draws its own title bar; the window's name still shows in
// Mission Control, the Window menu and the Dock, and in Windows' taskbar —
// never the file's while App Lock holds.
watch(
  [nowPlaying, appLocked],
  ([entry, locked]) => {
    void Window.SetTitle(locked ? "PKP" : (entry?.name ?? t("PKP 播放器"))).catch(() => undefined);
  },
  { immediate: true },
);

watch(
  () => nowPlaying.value?.category === "audio",
  async (audio) => {
    if (!ready.value) return;
    ready.value = false;
    failure.value = "";
    try {
      await nextTick();
      if (!audio) await PlayerService.HostVideo();
      ready.value = true;
    } catch (err) {
      failure.value = err instanceof Error ? err.message : String(err);
    }
  },
);

onMounted(async () => {
  void watchAppLock();
  try {
    await bootstrapPlayerWindow();
    ready.value = true;
  } catch (err) {
    failure.value = err instanceof Error ? err.message : String(err);
  }
});
</script>

<template>
  <!-- The window has no frame: until the player is up, and over a failure,
       the page itself is what moves it. -->
  <main class="player-window" style="--wails-draggable: drag">
    <MusicPlayer v-if="ready && nowPlaying?.category === 'audio'" windowed />
    <PlayerView v-else-if="ready" windowed />
    <div v-else-if="failure" class="failure">
      <p>{{ t("无法在这个窗口里播放：{error}", { error: failure }) }}</p>
      <button style="--wails-draggable: no-drag" @click="returnToMain">{{ t("回到主窗口") }}</button>
    </div>

    <!-- The main window's status pill is not here; errors from this page's
         own requests surface in place instead of vanishing. -->
    <button
      v-if="ui.message && ui.messageTone === 'error'"
      class="toast"
      style="--wails-draggable: no-drag"
      @click="dismiss"
    >{{ ui.message }}</button>
  </main>

  <!-- App Lock: the picture is paused under it, the controls and the title gone. -->
  <LockScreen v-if="appLocked" />
</template>

<style scoped>
.player-window {
  position: relative;
  height: 100%;
  display: flex;
}

.failure {
  margin: auto;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 14px;
  padding: 24px;
  color: #eef6ff;
  font-size: 14px;
  text-align: center;
}

/* Below the title bar the player draws. */
.toast {
  position: absolute;
  top: 60px;
  left: 50%;
  transform: translateX(-50%);
  max-width: calc(100% - 48px);
  padding: 8px 16px;
  border-radius: var(--radius-pill);
  border: 1px solid rgba(255, 155, 172, 0.4);
  background: rgba(14, 28, 52, 0.82);
  color: var(--danger);
  font-size: 13px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
