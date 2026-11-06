<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from "vue";
import SceneBackdrop from "./components/SceneBackdrop.vue";
import FloatingNav from "./components/FloatingNav.vue";
import FloatingToolbar from "./components/FloatingToolbar.vue";
import FilesView from "./components/FilesView.vue";
import TransfersView from "./components/TransfersView.vue";
import SharesView from "./components/SharesView.vue";
import SettingsView from "./components/SettingsView.vue";
import AssistantView from "./components/AssistantView.vue";
import CleanupView from "./components/CleanupView.vue";
import PlayHistoryView from "./components/PlayHistoryView.vue";
import SyncView from "./components/SyncView.vue";
import LANView from "./components/LANView.vue";
import SyncDraft from "./components/SyncDraft.vue";
import SyncDialogs from "./components/SyncDialogs.vue";
import SavedHistoryDialogs from "./components/SavedHistoryDialogs.vue";
import { watchSync } from "./syncstore";
import FileViewer from "./components/FileViewer.vue";
import PlayerView from "./components/PlayerView.vue";
import MusicPlayer from "./components/MusicPlayer.vue";
import ContextPanel from "./components/ContextPanel.vue";
import CompactPlaybackBar from "./components/CompactPlaybackBar.vue";
import CloudAddSheet from "./components/CloudAddSheet.vue";
import NewFolderSheet from "./components/NewFolderSheet.vue";
import CreateShareSheet from "./components/CreateShareSheet.vue";
import RelocateSheet from "./components/RelocateSheet.vue";
import ExtractSheet from "./components/ExtractSheet.vue";
import RenameRulesSheet from "./components/RenameRulesSheet.vue";
import { vSlide } from "./slide";
import StatusPill from "./components/StatusPill.vue";
import LoginView from "./components/LoginView.vue";
import BootSplash from "./components/BootSplash.vue";
import AccountDialogs from "./components/AccountDialogs.vue";
import AccountScreen from "./components/AccountScreen.vue";
import LockScreen from "./components/LockScreen.vue";
import AppLockPasswordSheet from "./components/AppLockPasswordSheet.vue";
import TelemetryNoticeSheet from "./components/TelemetryNoticeSheet.vue";
import ConnectivityNotice from "./components/ConnectivityNotice.vue";
import UpdateSettings from "./components/UpdateSettings.vue";
import PrivacySettings from "./components/PrivacySettings.vue";
import UpdateNotice from "./components/UpdateNotice.vue";
import { watchUpdates } from "./updatestore";
import { watchConnectivity } from "./connectivity";
import { WORKSPACE_QUERY, useMedia } from "./dom";
import { t } from "./i18n";
import { installShortcuts } from "./shortcuts";
import { accounts, appLocked, bootstrap, drive, nowPlaying, openSettings, player, settingsFocus, telemetry, ui, useTelemetryActivity, videoWorksShown, watchAppLock } from "./store";

// The shell stays up while there is an account on screen, signed in or not:
// an account whose session ended shows Log In Again in place, and
// only a computer without any account gets the sign-in page.
const signedIn = computed(() => !!drive.status?.accountId);
// The immersive player fills the window and only the floating chrome stays.
const immersive = computed(() => ui.route === "player");
// Music has nothing to watch: its queue gets the music page instead
// on the same session.
const music = computed(() => nowPlaying.value?.category === "audio");
// The playback session is independent of the page: while it exists and the
// picture is not up on the immersive page, the bar under the page carries
// it, on every page. While the separate player window has the picture, the
// same bar shows the session and brings it back.
const bar = computed(() => !immersive.value && (player.presentation === "bar" || player.presentation === "window"));
// Nothing is known until the first Status() answers: no account, no theme
// of content. The scene alone would be a blank window on a slow link.
const booting = computed(() => drive.status === null);
const showTelemetryNotice = computed(() => !!telemetry.state?.needsNotice && !booting.value && !appLocked.value);

// File browsing — the list, or the viewer on top of it — has the context
// column on its right. Under 960px the files and the column take
// turns as two pages of one workspace instead of squeezing side by side.
const browsing = computed(() => ui.route === "viewer" || (ui.route === "files" && !videoWorksShown.value));
const narrow = useMedia(WORKSPACE_QUERY);
const infoPage = computed(() => browsing.value && narrow.value && ui.workspace === "info");
const showPanel = computed(() => browsing.value && !covered.value && (narrow.value ? infoPage.value : ui.panelOpen));

// The account states that take the page's place: a switch
// on its way, or the account on screen needing Log In Again where its files
// would be. Transfer Center and Settings stay usable while it is signed out.
const switching = computed(() => !!accounts.switching && ui.route !== "player" && ui.route !== "lan");
const expired = computed(
  () =>
    !!drive.status?.needsPassword &&
    (ui.route === "files" || ui.route === "viewer" || ui.route === "shares" || ui.route === "assistant" || ui.route === "cleanup" || ui.route === "history"),
);
const covered = computed(() => switching.value || expired.value);

// The page the immersive player came from stays mounted, hidden, beneath
// it: Collapse and Continue Playing returns to the same folder, filter, scroll and
// selection rather than to a fresh list.
const below = computed(() => (ui.route === "player" ? player.returnRoute : ui.route));
const filesMounted = computed(() => below.value === "files" || below.value === "viewer");

// At a large zoom the whole workspace scrolls (see the styles); Files and Info
// each keep their own place in narrow windows and at high zoom.
const stack = ref<HTMLElement>();
const workspaceTop = { files: 0, info: 0 };
watch(
  infoPage,
  async (info, was) => {
    if (!stack.value) return;
    workspaceTop[was ? "info" : "files"] = stack.value.scrollTop;
    await nextTick();
    stack.value.scrollTop = workspaceTop[info ? "info" : "files"];
  },
  { flush: "pre" },
);

useTelemetryActivity();

// The lock comes first: with App Lock on, PKP starts locked and bootstrap's
// calls wait in Go until the owner unlocks.
onMounted(() => {
  void watchAppLock();
  void watchConnectivity();
  void watchUpdates();
  void bootstrap();
  void watchSync();
  installShortcuts();
});
</script>

<template>
  <!-- The scene paints the whole window and cuts the stage hole out of
       itself wherever the native video surface is showing through. -->
  <SceneBackdrop :waiting="booting || drive.listLoading" />

  <BootSplash v-if="booting" />

  <div v-else-if="!signedIn && ui.route === 'settings'" class="signedout-settings glass glass-sheet">
    <div class="signedout-settings-head">
      <button class="pill" @click="ui.route = 'files'">{{ t("返回登录") }}</button>
      <nav :aria-label="t('设置分组')">
        <button class="pill" :aria-pressed="settingsFocus.tab === 'privacy'" @click="openSettings('privacy')">{{ t("tab|隐私与安全") }}</button>
        <button class="pill" :aria-pressed="settingsFocus.tab !== 'privacy'" @click="openSettings('updates')">{{ t("关于与更新") }}</button>
      </nav>
    </div>
    <PrivacySettings v-if="settingsFocus.tab === 'privacy'" />
    <UpdateSettings v-else />
  </div>
  <div v-else-if="!signedIn && ui.route === 'lan'" class="signedout-lan">
    <button class="pill" @click="ui.route = 'files'">{{ t("返回登录") }}</button>
    <LANView />
  </div>
  <LoginView v-else-if="!signedIn" />

  <div v-else class="shell" :class="{ immersive, fullscreen: immersive && ui.fullscreen }">
    <FloatingNav v-if="!immersive" />

    <main ref="stack" class="stack">
      <FloatingToolbar v-if="!immersive && ui.route !== 'lan'" />

      <div v-if="browsing && narrow && !covered" v-slide class="workspace glass-well" role="tablist" :aria-label="t('工作区')">
        <button
          role="tab"
          :aria-selected="!infoPage"
          :class="{ on: !infoPage }"
          @click="ui.workspace = 'files'"
        >
          {{ ui.route === "viewer" ? t("预览") : t("文件") }}
        </button>
        <button role="tab" :aria-selected="infoPage" :class="{ on: infoPage }" @click="ui.workspace = 'info'">
          {{ t("信息") }}
        </button>
      </div>

      <div class="content">
        <!-- The file page stays mounted under the viewer, the Info page, the
             immersive player and the account states, so going back lands on
             the same folder, filter, scroll and selection. -->
        <AccountScreen v-if="switching" state="switching" />
        <AccountScreen v-else-if="expired" state="expired" />
        <FilesView v-if="filesMounted" v-show="ui.route === 'files' && !infoPage && !covered" />
        <FileViewer v-if="below === 'viewer'" v-show="ui.route === 'viewer' && !infoPage && !covered" />
        <PlayHistoryView v-if="below === 'history'" v-show="ui.route === 'history' && !covered" />
        <TransfersView v-if="ui.route === 'transfers' && !switching" />
        <SharesView v-if="ui.route === 'shares' && !covered" />
        <AssistantView v-if="ui.route === 'assistant' && !covered" />
        <CleanupView v-if="ui.route === 'cleanup' && !covered" />
        <SyncView v-if="ui.route === 'sync' && !switching" />
        <LANView v-if="ui.route === 'lan'" />
        <SettingsView v-if="ui.route === 'settings' && !switching" />
        <MusicPlayer v-if="ui.route === 'player' && music" />
        <PlayerView v-else-if="ui.route === 'player'" />

        <!-- The one right-hand slot of file browsing; picking never adds,
             removes or resizes it. -->
        <ContextPanel v-if="showPanel" :page="narrow" />
      </div>

      <CompactPlaybackBar v-if="bar" />
    </main>

    <StatusPill v-if="!immersive" :above-bar="bar" />
    <ConnectivityNotice v-if="!immersive && ui.route !== 'settings'" />
    <CloudAddSheet />
    <NewFolderSheet />
    <CreateShareSheet />
    <RenameRulesSheet />
    <RelocateSheet />
    <ExtractSheet />
    <SyncDraft />
    <SyncDialogs />
    <SavedHistoryDialogs />
    <AppLockPasswordSheet />
    <!-- Last: its questions come up over any of these sheets. -->
    <AccountDialogs />
  </div>
  <AppLockPasswordSheet v-if="!signedIn" />
  <TelemetryNoticeSheet v-if="showTelemetryNotice" />
  <UpdateNotice v-if="!booting && !appLocked && !showTelemetryNotice && ui.route !== 'player' && ui.route !== 'settings'" />

  <!-- App Lock: replaces whatever is above, the player included. -->
  <LockScreen v-if="appLocked" />
</template>

<style scoped>
.signedout-settings { position: fixed; z-index: 1; inset: 44px 20px 20px; max-width: 780px; margin: 0 auto; padding: 24px; overflow: auto; border-radius: 26px; }
.signedout-settings-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 24px; }
.signedout-settings-head nav { display: flex; flex-wrap: wrap; gap: 8px; }
.signedout-settings-head [aria-pressed="true"] { color: var(--accent); border-color: var(--accent); }
.signedout-lan { position: fixed; inset: 48px 20px 20px; overflow: auto; }
.signedout-lan > button { margin-bottom: 12px; }
/* Layer 3 floats over the scene with wallpaper between the islands. The file
   page uses 20px outside and 16px between navigation, content and context.
   With the title bar hidden, traffic lights sit on wallpaper and both islands
   start on one line. */
.shell {
  position: relative;
  z-index: 1;
  height: 100%;
  display: flex;
  gap: 16px;
  padding: 44px 20px 20px;
}

.shell.immersive {
  padding: 36px 14px 14px;
}

.shell.fullscreen {
  padding: 0;
}

.stack {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* Detail islands — the context column, the transfer and share details, the
   settings islands — are columns of this row beside the canvas, never
   layered over it: the canvas gives up their width.
   --inspector-w is the transfer, share and settings islands' width:
   280–300px once the window drops under 1280; the file page's column
   is a fixed 288. */
.content {
  --inspector-w: 318px;
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  gap: 16px;
}

@media (max-width: 1279px) {
  .content {
    --inspector-w: 284px;
  }
}

/* Files / Info: two pages of one workspace under 960px. */
.workspace {
  flex: none;
  align-self: flex-start;
  display: flex;
  gap: 2px;
  padding: 3px;
  border-radius: var(--radius-pill);
}

.workspace button {
  min-width: 72px;
  min-height: 40px;
  border-color: transparent;
  background: transparent;
  color: var(--fg-dim);
  font-weight: 500;
}

.workspace::before {
  background: var(--glass-control);
  border: 1px solid var(--glass-edge);
  box-shadow: inset 0 1px 0 var(--glass-rim);
}

.workspace button.on {
  background: transparent;
  border-color: transparent;
  color: var(--fg);
  font-weight: 600;
}

@media (max-width: 959px) {
  .shell {
    gap: 12px;
    padding: 44px 12px 12px;
  }

  .stack,
  .content {
    gap: 12px;
  }
}

/* A short window scrolls the workspace instead of crushing its reading area. */
@media (max-height: 620px) {
  .shell:not(.immersive) .stack {
    overflow-y: auto;
    scrollbar-width: thin;
  }

  .shell:not(.immersive) .stack > [data-playback] {
    position: sticky;
    bottom: 0;
    z-index: 2;
  }

  .shell:not(.immersive) .content {
    flex: 0 0 480px;
    min-height: 480px;
  }
}
</style>
