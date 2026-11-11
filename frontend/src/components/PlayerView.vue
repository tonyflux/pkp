<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { Window } from "@wailsio/runtime";
import AccountIdentity from "./AccountIdentity.vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import DockMenu from "./DockMenu.vue";
import QualityMenu from "./QualityMenu.vue";
import QualityOptions from "./QualityOptions.vue";
import SpeedOptions from "./SpeedOptions.vue";
import TrackOptions from "./TrackOptions.vue";
import AISubtitleStatus from "./AISubtitleStatus.vue";
import SelectMenu, { type SelectOption } from "./SelectMenu.vue";
import SubtitleSettings from "./SubtitleSettings.vue";
import PlaylistQueue from "./PlaylistQueue.vue";
import RangeSlider from "./RangeSlider.vue";
import { bytes, clock } from "../format";
import { onWindows, useOutsideClick } from "../dom";
import { canChoose } from "../quality";
import { useStage } from "../stage";
import { trackDetail, trackLabel } from "../tracks";
import { modes, usePlayerKeys, useTransport } from "../transport";
import { PlayerService } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import type { Chapter, Track } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import {
  accountLabel,
  accounts,
  playingElsewhere,
  playingOwner,
  collapsePlayer,
  downloadPlaying,
  drive,
  loadMembership,
  memberships,
  nextUp,
  nowPlaying,
  playAt,
  player,
  playerCommands,
  popOut,
  returnToMain,
  run,
  setFullscreen,
  setPlayMode,
  stopPlayback,
  ui,
} from "../store";
import { t, N_ } from "../i18n";

// windowed: this is the separate player window, which is all picture — no
// title island, docked playlist or file line, fullscreen or not. Its own
// title bar and playlist float over the picture instead.
const props = defineProps<{ windowed?: boolean }>();
const bare = computed(() => ui.fullscreen || props.windowed);
const showOwner = computed(() => !!playingOwner.value && (accounts.list.length > 1 || playingElsewhere.value));
// The separate window's playlist drawer.
const listOpen = ref(false);
const dockedListOpen = ref(true);
const sidebarOpen = computed({
  get: () => (props.windowed ? listOpen.value : dockedListOpen.value),
  set: (open: boolean) => {
    if (props.windowed) listOpen.value = open;
    else dockedListOpen.value = open;
  },
});
const listButton = ref<HTMLButtonElement>();

const tracks = ref<Track[]>([]);
const chapters = ref<Chapter[]>([]);
const hoverTime = ref<number | null>(null);
const hoverX = ref(0);
const stage = ref<HTMLElement>();
const pointerIdle = ref(false);
// The pointer rests on the dock or on the separate window's playlist.
const controlsHovered = ref(false);
// The dock's popovers: Subtitles, Audio tracks, Quality, Speed, and More, which holds whichever
// of them no longer fit in the dock's row (`fitAdvanced`).
const subtitleOpen = ref(false);
const audioOpen = ref(false);
const qualityOpen = ref(false);
const speedOpen = ref(false);
const moreOpen = ref(false);
const moreButton = ref<HTMLButtonElement>();
const moreQuality = ref<InstanceType<typeof QualityOptions>>();
const moreSubtitle = ref<InstanceType<typeof TrackOptions>>();
const moreAudio = ref<InstanceType<typeof TrackOptions>>();
const morePop = ref<HTMLElement>();
// Subtitle and A/V sync, a popover of its own beside More.
const tuneOpen = ref(false);
const tuneButton = ref<HTMLButtonElement>();
const tunePop = ref<HTMLElement>();
// The separate window's playlist Play order list: teleported, so the pointer on
// it has left the drawer.
const modeOpen = ref(false);

const { scrubEl, scrub, state, position, percent, progress, cached, showNext, dismissNext } = useTransport();
const scrubbing = scrub.scrubbing;

// Both labels reserve the width of the longest value they will show, so the
// position ticking past an hour does not reflow the bar between them.
const timeWidth = computed(() => ({ minWidth: `${clock(state.value.duration).length}ch` }));

// mpv's media-title for a proxied stream is the loopback handle id, which is
// noise. The folder path is what the user actually recognises.
const locationLabel = computed(() => drive.trail.map((c) => c.name).join(" / ") || t("全部文件"));

const subtitles = computed(() => tracks.value.filter((tr) => tr.type === "sub"));
const audios = computed(() => tracks.value.filter((tr) => tr.type === "audio"));
const activeSub = computed(() => subtitles.value.find((tr) => tr.selected)?.id ?? 0);
const audioValue = computed(() => String(audios.value.find((tr) => tr.selected)?.id ?? 0));

const aiLanguages = [
  { code: "", label: N_("原文") },
  { code: "zh", label: "中文" }, // i18n-ignore
  { code: "en", label: "English" },
  { code: "ja", label: "日本語" }, // i18n-ignore
  { code: "ko", label: "한국어" }, // i18n-ignore
];
const aiLanguageLabel = (label: string) => (label === "原文" ? t("原文") : label); // i18n-ignore
const ai = computed(() => state.value.aiSubtitles);
const subtitleAccount = computed(() => props.windowed ? player.ownerInfo?.id ?? "" : player.owner);
const aiMembership = computed(() => memberships[subtitleAccount.value]);
const aiAccess = computed<"ready" | "loading" | "error" | "nonmember">(() => {
  if (!ai.value.requiresMembership) return "ready";
  const read = aiMembership.value;
  if (read?.loading) return "loading";
  if (read?.error || !subtitleAccount.value) return "error";
  if (!read?.value) return "loading";
  return read.value.active ? "ready" : "nonmember";
});
function refreshAIMembership() {
  if (ai.value.available && ai.value.requiresMembership && subtitleAccount.value) {
    void loadMembership(subtitleAccount.value);
  }
}
watch([subtitleAccount, () => ai.value.available, () => ai.value.requiresMembership], refreshAIMembership, { immediate: true });
watch(subtitleOpen, (open) => open && refreshAIMembership());
watch(moreOpen, (open) => open && folded.value.includes("subtitles") && refreshAIMembership());
watch(sidebarOpen, (open) => open && !bare.value && refreshAIMembership());
const showSubtitles = computed(() => subtitles.value.length > 0 || ai.value.available);
const subValue = computed(() => (ai.value.on ? `ai:${ai.value.language}` : String(activeSub.value)));
const subOptions = computed<SelectOption<string>[]>(() => [
  { value: "0", label: t("关") },
  ...subtitles.value.map((tr) => ({ value: String(tr.id), label: trackLabel(tr), detail: trackDetail(tr) })),
  ...(ai.value.available
    ? aiLanguages.map((l) => ({
        value: `ai:${l.code}`, label: aiLanguageLabel(l.label), group: t("AI 字幕"),
        disabled: aiAccess.value !== "ready",
      }))
    : []),
]);
const subLabel = computed(() => {
  if (ai.value.on) {
    const found = aiLanguages.find((l) => l.code === ai.value.language);
    const name = found ? aiLanguageLabel(found.label) : ai.value.language;
    return ai.value.status === "connecting" ? t("AI {name} · 连接中", { name }) : `AI ${name}`;
  }
  const track = subtitles.value.find((x) => x.selected);
  return track ? trackLabel(track) : t("关");
});
// Subtitle style reaches every text subtitle, which mpv 0.41 converts to ASS and
// restyles. It reaches only in part an "ass" track, drawn as styled, and
// picture tracks, drawn as their bitmaps (sd_lavc's codecs, less teletext and
// ARIB, which the bundled FFmpeg cannot decode); the panel says how.
const PICTURE_SUBS = new Set(["hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", "xsub"]);
const subKind = computed(() => {
  if (ai.value.on) return undefined;
  const codec = subtitles.value.find((tr) => tr.selected)?.codec ?? "";
  if (codec === "ass") return "ass" as const;
  return PICTURE_SUBS.has(codec) ? ("picture" as const) : undefined;
});
// Whether Subtitle and A/V sync has anything on screen to act on.
const subShown = computed(() =>
  ai.value.on || activeSub.value !== 0 ? ("on" as const) : showSubtitles.value ? ("off" as const) : ("none" as const),
);
const audioLabel = computed(() => {
  const track = audios.value.find((x) => x.selected);
  return track ? trackLabel(track) : "—";
});
const audioOptions = computed<SelectOption<string>[]>(() =>
  audios.value.map((tr) => ({ value: String(tr.id), label: trackLabel(tr), detail: trackDetail(tr) })),
);

const modeIcon = computed(() => modes.find((m) => m.value === player.mode)?.icon ?? "list");

// The chapter the hover sits in, so the tooltip can name it.
const hoverChapter = computed(() => {
  if (hoverTime.value === null) return null;
  let found: Chapter | null = null;
  for (const c of chapters.value) if (c.time <= hoverTime.value) found = c;
  return found;
});

const extension = computed(() => {
  const name = nowPlaying.value?.name ?? "";
  const dot = name.lastIndexOf(".");
  return dot > 0 ? name.slice(dot + 1).toUpperCase() : "";
});

// The dock, and in the separate window the title bar and the playlist, cover
// the picture and the subtitles, so they only stay while there is a reason:
// the pointer moved recently or sits on one of them, a menu of the dock or the
// playlist's Play order is open, playback is paused, a scrub is in progress, or
// there is nothing to watch yet.
const controlsHidden = computed(() => {
  const s = state.value;
  if (
    scrubbing.value ||
    controlsHovered.value ||
    subtitleOpen.value ||
    audioOpen.value ||
    qualityOpen.value ||
    speedOpen.value ||
    moreOpen.value ||
    tuneOpen.value ||
    modeOpen.value
  )
    return false;
  if (s.paused || s.error || s.buffering || s.loading || s.duration <= 0) return false;
  return pointerIdle.value;
});

// The separate window has no frame: its traffic lights belong to the title
// bar over the picture and come and go with it. On Windows the page draws
// the caption buttons in that title bar itself.
if (props.windowed) {
  watch(
    controlsHidden,
    (hidden) => void PlayerService.ShowWindowButtons(!hidden).catch(() => undefined),
    { immediate: true },
  );
}

function minimiseWindow() {
  void Window.Minimise().catch(() => undefined);
}

// As the window's own close button would: the picture goes home, the
// session stays in the main window's bar.
function closeWindow() {
  void Window.Close().catch(() => undefined);
}

// The video is drawn by a native view sitting behind the webview; the stage
// div is only the hole it shows through.
useStage(stage, () => (bare.value ? 0 : 18));

let tracksRequest = 0;
let disposed = false;
async function refreshTracks() {
  if (disposed) return;
  const request = ++tracksRequest;
  const [trackList, c] = await Promise.all([
    PlayerService.Tracks().catch(() => []),
    PlayerService.Chapters().catch(() => []),
  ]);
  if (request !== tracksRequest) return;
  tracks.value = trackList ?? [];
  chapters.value = c ?? [];
}

function onScrubHover(event: PointerEvent) {
  const time = scrub.valueAt(event);
  if (time === null) return;
  hoverX.value = (time / state.value.duration) * 100;
  hoverTime.value = time;
  scrub.onPointerMove(event);
}

function setTrack(kind: "audio" | "subtitle", id: string) {
  void run(t("切换轨道"), async () => {
    await PlayerService.SelectTrack(kind, Number(id));
    await refreshTracks();
  });
}

let subtitleChoice = 0;
let subtitleQueue = Promise.resolve();
function selectSubtitle(value: string) {
  const choice = ++subtitleChoice;
  subtitleQueue = subtitleQueue.then(async () => {
    if (choice !== subtitleChoice) return;
    if (value.startsWith("ai:")) {
      if (ai.value.requiresMembership) {
        if (aiAccess.value !== "ready" && aiAccess.value !== "nonmember") await loadMembership(subtitleAccount.value);
        if (choice !== subtitleChoice) return;
        if (aiAccess.value !== "ready") {
          flash(t("AI 字幕"), aiAccess.value === "nonmember" ? t("AI 字幕需要 PikPak 会员") : t("会员状态读取失败"));
          return;
        }
      }
      await playerCommands.aiSubtitles(true, value.slice(3));
      if (choice !== subtitleChoice) return;
      await refreshTracks();
      return;
    }
    await run(t("切换轨道"), async () => {
      // State events lag commands; finish stopping AI before selecting a native track.
      if (ai.value.available && !(await playerCommands.aiSubtitles(false, ""))) return;
      if (choice !== subtitleChoice) return;
      await PlayerService.SelectTrack("subtitle", Number(value));
      if (choice !== subtitleChoice) return;
      await refreshTracks();
    });
  });
}

function invalidateSubtitleChoices() {
  subtitleChoice++;
  tracksRequest++;
}
// A raw track ID belongs to this view's current file and rendition only.
watch(
  [
    subtitleAccount,
    () => player.presentation,
    () => player.index,
    () => nowPlaying.value?.id,
    () => state.value.source,
    () => state.value.loading,
    () => state.value.quality.current,
    () => state.value.quality.switching,
  ],
  invalidateSubtitleChoices,
  { flush: "sync" },
);

function toggleMute() {
  void playerCommands.mute(!state.value.muted);
}

function retry() {
  void playAt(player.index);
}

function toggleFullscreen() {
  void setFullscreen(!ui.fullscreen);
}

// notice shows screenshot and download results on the stage, not in
// StatusPill, which fullscreen and the player window do not show. A
// screenshot carries its file so it can be revealed in the file manager,
// and stays longer: the folder is the thing to find.
const notice = ref<{ label: string; name: string; path?: string } | null>(null);
let noticeTimer: number | undefined;
function flash(label: string, name: string, path?: string) {
  notice.value = { label, name, path };
  window.clearTimeout(noticeTimer);
  noticeTimer = window.setTimeout(() => (notice.value = null), path ? 8000 : 3000);
}

watch(
  [() => ai.value.errorRevision, () => ai.value.error],
  ([, error]) => error && flash(t("AI 字幕已关闭"), error),
  { immediate: true, flush: "sync" },
);

async function screenshot() {
  try {
    const path = await PlayerService.Screenshot(nowPlaying.value?.name || state.value.title);
    flash(t("截图已存到「图片 › PKP」"), path.slice(Math.max(path.lastIndexOf("/"), path.lastIndexOf("\\")) + 1), path);
  } catch (err) {
    flash(t("截图失败"), err instanceof Error ? err.message : String(err));
  }
}

// Download to computer for the video playing; the transfer center takes it from there.
async function download() {
  try {
    const name = await downloadPlaying();
    if (name) flash(t("已加入下载"), name);
  } catch (err) {
    flash(t("下载失败"), err instanceof Error ? err.message : String(err));
  }
}

async function revealScreenshot(path: string) {
  notice.value = null;
  try {
    await PlayerService.RevealScreenshot(path);
  } catch (err) {
    flash(t("无法打开截图位置"), err instanceof Error ? err.message : String(err));
  }
}

// Keyboard map from the design contract, only while nothing is being typed.
const onKey = usePlayerKeys({
  fullscreen: toggleFullscreen,
  screenshot,
  escape: () => {
    if (!ui.fullscreen) return false;
    void setFullscreen(false);
    return true;
  },
  after: onPointerActivity,
});

let idleTimer: number | undefined;
function onPointerActivity() {
  pointerIdle.value = false;
  window.clearTimeout(idleTimer);
  idleTimer = window.setTimeout(() => {
    pointerIdle.value = true;
  }, 2500);
}

// Leaving the picture for the playlist or the title bar is not "resting on
// it"; the dock goes away at once rather than lingering for the timeout.
function onPointerLeave() {
  window.clearTimeout(idleTimer);
  pointerIdle.value = true;
}

function closeList() {
  sidebarOpen.value = false;
  // The drawer disappears under the pointer without a pointerleave.
  controlsHovered.value = false;
  onPointerActivity();
  listButton.value?.focus({ preventScroll: true });
}

// More opens on the current row of its top section, Subtitles or Audio tracks, so the panel
// does not open scrolled past them; without those on Quality's, else on Speed's.
// Esc and a click elsewhere close it, focus returns to the button.
watch(
  moreOpen,
  (open) => {
    if (!open) return;
    const first = moreSubtitle.value ?? moreAudio.value ?? moreQuality.value;
    if (first) void first.focusCurrent();
    else morePop.value?.querySelector<HTMLElement>('[role=option][tabindex="0"]')?.focus();
  },
  { flush: "post" },
);

function closeMore() {
  moreOpen.value = false;
  moreButton.value?.focus();
}

function onMoreKey(event: KeyboardEvent) {
  if (event.key !== "Escape" || !moreOpen.value) return;
  event.preventDefault();
  event.stopPropagation();
  closeMore();
}

useOutsideClick("[data-player-more]", () => moreOpen.value, () => (moreOpen.value = false));

watch(
  tuneOpen,
  (open) => {
    if (open) tunePop.value?.querySelector<HTMLElement>("input")?.focus();
  },
  { flush: "post" },
);

function closeTune() {
  tuneOpen.value = false;
  tuneButton.value?.focus();
}

useOutsideClick("[data-player-tune]", () => tuneOpen.value, () => (tuneOpen.value = false));

function onTuneFocusOut(event: FocusEvent) {
  const next = event.relatedTarget as Node | null;
  if (next && !(event.currentTarget as HTMLElement).contains(next)) tuneOpen.value = false;
}

function onMoreFocusOut(event: FocusEvent) {
  const next = event.relatedTarget as Node | null;
  if (next && !(event.currentTarget as HTMLElement).contains(next)) moreOpen.value = false;
}

// Subtitles, Audio tracks, Quality and Speed stay in the row, in that order, as far as they
// fit beside everything else at full size; the rest fold into More in the
// same words and order, and More shows only then. Measured rather than set by
// a width: the entries come and go with the video, and their labels vary.
const controlRow = ref<HTMLElement>();
const advanced = ref<HTMLElement>();
const spacer = ref<HTMLElement>();
const moreWrap = ref<HTMLElement>();
// The entries in More by their data-fold key, and whether that is every one:
// only then does the volume slider give way as well.
const folded = ref<string[]>([]);
const crowded = ref(false);
const moreSwitching = computed(() => !!state.value.quality.switching && folded.value.includes("quality"));

function fitAdvanced() {
  const row = controlRow.value;
  const more = moreWrap.value;
  if (!row || !more || !advanced.value || !spacer.value) return;
  const box = (el: Element) => el.getBoundingClientRect();
  const gap = parseFloat(getComputedStyle(row).columnGap) || 0;
  const entries = Array.from(advanced.value.children) as HTMLElement[];
  const widths = entries.map((el) => box(el).width + gap);
  const moreWidth = box(more).width + gap;
  // The room for the entries and More: the row's slack, less whatever runs
  // past its end, plus what they take up now.
  let room = box(spacer.value).width + box(row).right - box(row.lastElementChild!).right;
  entries.forEach((el, i) => {
    if (!el.classList.contains("out")) room += widths[i];
  });
  if (!more.classList.contains("out")) room += moreWidth;
  let shown = entries.length;
  if (widths.reduce((sum, w) => sum + w, 0) > room) {
    // Not all of them: More takes its place first, then the entries in order.
    let used = moreWidth;
    shown = 0;
    while (shown < entries.length && used + widths[shown] <= room) used += widths[shown++];
  }
  const next = entries.slice(shown).map((el) => el.dataset.fold ?? "");
  if (next.join() !== folded.value.join()) folded.value = next;
  // While crowded the volume slider gives way, and the room it gave counted
  // above; once not, it takes it back and the row is measured again.
  const wasCrowded = crowded.value;
  crowded.value = shown === 0;
  if (wasCrowded && !crowded.value) requestAnimationFrame(fitAdvanced);
}

// The row follows the stage's width, each entry its label; an entry coming
// or going (tracks found, a video without renditions) is watched afresh.
const fitter = new ResizeObserver(fitAdvanced);
const entryList = new MutationObserver(watchEntries);
function watchEntries() {
  fitter.disconnect();
  if (controlRow.value) fitter.observe(controlRow.value);
  for (const el of Array.from(advanced.value?.children ?? [])) fitter.observe(el);
}

// A popover whose entry just moved into More, or out of it, goes with it.
watch(folded, () => {
  moreOpen.value = false;
  subtitleOpen.value = false;
  audioOpen.value = false;
  qualityOpen.value = false;
  speedOpen.value = false;
});

// A new video may have no subtitles or a single audio track; the menu goes
// with the entry, as Quality's does (QualityMenu).
watch(showSubtitles, (shown) => {
  if (!shown) subtitleOpen.value = false;
});
watch(
  () => audios.value.length > 1,
  (shown) => {
    if (!shown) audioOpen.value = false;
  },
);

// Tracks and chapters only exist once a file is loaded, so they are re-read
// whenever the playlist cursor moves or a new duration arrives — and after a
// quality switch, whose rendition may carry other tracks (the transcodes
// have no subtitles).
watch(
  () => player.index,
  () => setTimeout(refreshTracks, 600),
);
watch(
  () => state.value.duration,
  (d) => d > 0 && refreshTracks(),
);
watch(
  () => state.value.quality.current,
  () => setTimeout(refreshTracks, 600),
);

// Re-read native tracks when AI connects, stops or fails before a state event arrives.
watch(
  [() => ai.value.on, () => ai.value.status, () => ai.value.errorRevision, () => ai.value.error],
  () => void refreshTracks(),
);

onMounted(() => {
  window.addEventListener("keydown", onKey);
  void refreshTracks();
  watchEntries();
  entryList.observe(advanced.value!, { childList: true });
});

onBeforeUnmount(() => {
  disposed = true;
  invalidateSubtitleChoices();
  window.removeEventListener("keydown", onKey);
  window.clearTimeout(idleTimer);
  window.clearTimeout(noticeTimer);
  if (ui.fullscreen) void setFullscreen(false);
  fitter.disconnect();
  entryList.disconnect();
});
</script>

<template>
  <div class="player" :class="{ fullscreen: bare, quiet: bare && controlsHidden }">
    <header v-if="!bare" class="bar glass" style="--wails-draggable: drag">
      <button class="quiet back" style="--wails-draggable: no-drag" :title="player.returnRoute === 'history' ? t('返回播放历史') : t('回到文件页，视频继续播放')" @click="collapsePlayer">
        <Icon name="chevronLeft" :size="17" />
        {{ player.returnRoute === "history" ? t("返回播放历史") : t("收起并继续播放") }}
      </button>
      <span class="divider" />
      <strong class="now" :title="nowPlaying?.name">{{ nowPlaying?.name ?? t("未选择文件") }}</strong>
      <span class="path" :title="locationLabel">{{ locationLabel }}</span>
      <span class="spacer" />
      <span v-if="showOwner" class="owner" :aria-label="t('所属账号')">
        <AccountIdentity :account="playingOwner" :size="24" dense />
      </span>
      <button
        v-if="nowPlaying"
        class="quiet icon download"
        style="--wails-draggable: no-drag"
        :title="t('下载到电脑')"
        :aria-label="t('下载到电脑')"
        @click="download"
      >
        <Icon name="download" :size="17" />
      </button>
    </header>

    <div class="body">
      <!-- Transparent on purpose: the native mpv surface shows through here.
           Nothing may paint a background inside it. In the separate window,
           which has no frame, dragging the picture moves the window, and the
           title bar and the playlist float over the picture. -->
      <section
        ref="stage"
        class="stage"
        :class="{ moves: windowed }"
        @pointermove="onPointerActivity"
        @pointerdown="onPointerActivity"
        @pointerleave="onPointerLeave"
      >
        <!-- A play request answers at once, over whatever frame is still
             there. The others are `.late`: a 200ms stall mid-stream must not
             blink a spinner. -->
        <div v-if="state.loading" class="stage-loading">
          <Orbit :size="28" :label="t('加载中')" />
          <span class="stage-loading-name" :title="nowPlaying?.name">{{ nowPlaying?.name ?? t("正在加载…") }}</span>
        </div>
        <!-- A seek (or a start) runs in two steps: reaching the new place, with
             nothing to measure but the incoming data, then mpv waiting paused
             until a second is read ahead, which it counts up. -->
        <div v-else-if="state.buffering || state.seeking" class="stage-wait late" role="status">
          <span class="stage-wait-dial">
            <template v-if="state.buffering">
              <svg viewBox="0 0 48 48" aria-hidden="true">
                <circle class="track" cx="24" cy="24" r="21" pathLength="100" />
                <circle class="fill" cx="24" cy="24" r="21" pathLength="100" :style="{ strokeDashoffset: 100 - state.bufferPercent }" />
              </svg>
              <b>{{ state.bufferPercent }}<small>%</small></b>
            </template>
            <Orbit v-else :size="30" :label="t('加载中')" />
          </span>
          <span class="stage-wait-text">
            {{ state.buffering ? t("缓冲中") : t("加载中") }}
            <template v-if="state.cacheSpeed > 0"> · {{ bytes(state.cacheSpeed, "", 1) }}/s</template>
          </span>
        </div>
        <div v-else-if="state.error" class="stage-error" role="alert">
          <span class="stage-error-icon"><Icon name="alert" :size="22" /></span>
          <strong>{{ t("无法播放") }}</strong>
          <p>{{ state.error === "playback failed" ? t("视频没能加载出来，可以重试一次") : state.error }}</p>
          <div class="stage-error-actions">
            <button class="primary" @click="retry">{{ t("重试") }}</button>
            <button v-if="windowed" @click="returnToMain">{{ t("回到主窗口") }}</button>
            <button v-else @click="collapsePlayer">{{ player.returnRoute === "history" ? t("返回播放历史") : t("返回文件") }}</button>
            <button @click="stopPlayback">{{ t("停止播放") }}</button>
          </div>
        </div>
        <div v-else-if="!state.duration" class="stage-state late">
          <Orbit :size="16" :label="t('准备中')" />
          {{ t("准备中…") }}
        </div>

        <!-- The separate window's title bar, over the picture with the
             controls; its traffic lights, or on Windows its caption
             buttons, come and go with it. -->
        <header v-if="windowed" class="titlebar" :class="{ hidden: controlsHidden, win: onWindows }">
          <strong class="titlebar-name" :title="nowPlaying?.name">{{ nowPlaying?.name ?? t("PKP 播放器") }}</strong>
          <span v-if="playingOwner" class="titlebar-owner" :title="t('所属账号 · {name}', { name: accountLabel(playingOwner) })">
            <AccountIdentity :account="playingOwner" :size="22" dense />
          </span>
          <button v-if="nowPlaying" class="icon ghost" :title="t('下载到电脑')" :aria-label="t('下载到电脑')" @click="download">
            <Icon name="download" :size="17" />
          </button>
          <template v-if="onWindows">
            <button class="icon ghost" :title="t('最小化')" :aria-label="t('最小化')" @click="minimiseWindow">
              <Icon name="minus" :size="17" />
            </button>
            <button class="icon ghost caption-close" :title="t('关闭（视频回到主窗口的播放条）')" :aria-label="t('关闭')" @click="closeWindow">
              <Icon name="close" :size="17" />
            </button>
          </template>
        </header>

        <transition name="rise">
          <div
            v-if="notice"
            key="notice"
            class="next"
            :class="{ notice: !notice.path, low: controlsHidden, beside: listOpen }"
            role="status"
          >
            <span class="next-text">
              <span class="next-label">{{ notice.label }}</span>
              <span class="next-name" :title="notice.name">{{ notice.name }}</span>
            </span>
            <button v-if="notice.path" class="quiet" @click="revealScreenshot(notice.path)">
              {{ onWindows ? t("在资源管理器中显示") : t("在访达中显示") }}
            </button>
          </div>
          <div v-else-if="showNext" key="next" class="next" :class="{ low: controlsHidden, beside: listOpen }">
            <span class="next-text">
              <span class="next-label">{{ player.mode === "one" ? t("即将重播") : t("即将播放") }}</span>
              <span class="next-name" :title="nextUp?.name">{{ nextUp?.name }}</span>
            </span>
            <button class="quiet" @click="dismissNext">{{ t("取消") }}</button>
          </div>
        </transition>

        <div
          class="dock"
          :class="{ hidden: controlsHidden }"
          @pointerenter="controlsHovered = true"
          @pointerleave="controlsHovered = false"
        >
          <div class="timeline">
            <span class="time mono" :style="timeWidth">{{ clock(position) }}</span>
            <div
              ref="scrubEl"
              class="scrub"
              role="slider"
              tabindex="0"
              :aria-label="t('播放进度')"
              :aria-valuemin="0"
              :aria-valuemax="Math.round(state.duration)"
              :aria-valuenow="Math.round(position)"
              :aria-valuetext="clock(position)"
              :aria-disabled="state.duration <= 0"
              @pointerdown="scrub.onPointerDown"
              @pointermove="onScrubHover"
              @pointerup="scrub.onPointerUp"
              @pointercancel="scrub.onPointerUp"
              @pointerleave="hoverTime = null"
              @keydown="scrub.onKeyDown"
            >
              <div class="track">
                <i class="cache" :style="{ width: cached + '%' }" />
                <i class="played" :style="{ width: progress + '%' }" />
              </div>
              <i
                v-for="(c, i) in chapters"
                :key="i"
                class="tick"
                :class="{ passed: c.time <= position }"
                :style="{ left: percent(c.time) + '%' }"
              />
              <i class="knob" :style="{ left: progress + '%' }" />
              <div v-if="hoverTime !== null" class="hover" :style="{ left: hoverX + '%' }">
                <span v-if="hoverChapter?.title" class="hover-chapter">{{ hoverChapter.title }}</span>
                <span class="mono">{{ clock(hoverTime) }}</span>
              </div>
            </div>
            <span class="time mono" :style="timeWidth">{{ clock(state.duration) }}</span>
          </div>

          <div ref="controlRow" class="controls" :class="{ crowded }">
            <button class="icon main" :title="state.paused ? t('播放') : t('暂停')" @click="playerCommands.toggle()">
              <Icon :name="state.paused ? 'play' : 'pause'" :size="22" filled />
            </button>

            <button class="step" :title="t('后退 15 秒')" @click="playerCommands.seek(-15)">
              <Icon name="back15" :size="26" />
              <em>15</em>
            </button>
            <button class="step" :title="t('前进 15 秒')" @click="playerCommands.seek(15)">
              <Icon name="forward15" :size="26" />
              <em>15</em>
            </button>

            <div class="volume">
              <button class="icon ghost" :title="state.muted ? t('取消静音') : t('静音')" @click="toggleMute">
                <Icon :name="state.muted || state.volume <= 0 ? 'mute' : 'volume'" :size="19" />
              </button>
              <RangeSlider :label="t('音量')" :model-value="state.volume" @update:model-value="(v) => playerCommands.volume(v)" />
            </div>

            <span ref="spacer" class="spacer" />

            <!-- Subtitles, audio, quality and speed stay in the row while
                 they fit; the rest move into the overflow menu so core controls never squeeze. -->
            <div ref="advanced" class="advanced">
              <DockMenu
                v-if="showSubtitles"
                v-model:open="subtitleOpen"
                :class="{ out: folded.includes('subtitles') }"
                data-fold="subtitles"
                icon="subtitles"
                icon-only
                :status-dot="subShown === 'on'"
                :value="subLabel"
                :title="t('字幕：{name}', { name: subLabel })"
              >
                <template #default="{ close }">
                  <TrackOptions
                    kind="subtitle"
                    :options="subOptions"
                    :value="subValue"
                    :current="subLabel"
                    @choose="selectSubtitle"
                    @done="close"
                    @close="close"
                  >
                    <template #ai-status>
                      <AISubtitleStatus :state="ai" :account="subtitleAccount" :access="aiAccess" @retry="refreshAIMembership" />
                    </template>
                  </TrackOptions>
                </template>
              </DockMenu>

              <DockMenu
                v-if="audios.length > 1"
                v-model:open="audioOpen"
                :class="{ out: folded.includes('audio') }"
                data-fold="audio"
                icon="waveform"
                icon-only
                :value="audioLabel"
                :title="t('音轨：{name}', { name: audioLabel })"
              >
                <template #default="{ close }">
                  <TrackOptions
                    kind="audio"
                    :options="audioOptions"
                    :value="audioValue"
                    :current="audioLabel"
                    @choose="setTrack('audio', $event)"
                    @done="close"
                    @close="close"
                  />
                </template>
              </DockMenu>

              <QualityMenu v-model:open="qualityOpen" :class="{ out: folded.includes('quality') }" data-fold="quality" />

              <DockMenu
                v-model:open="speedOpen"
                :class="{ out: folded.includes('speed') }"
                data-fold="speed"
                icon="gauge"
                :value="`${state.speed}×`"
                :title="t('倍速：{speed}', { speed: `${state.speed}×` })"
              >
                <template #default="{ close }">
                  <SpeedOptions @done="close" @close="close" />
                </template>
              </DockMenu>
            </div>

            <div
              ref="moreWrap"
              class="more-wrap"
              :class="{ out: !folded.length }"
              data-player-more
              @keydown="onMoreKey"
              @focusout="onMoreFocusOut"
            >
              <button
                ref="moreButton"
                class="icon ghost"
                :class="{ on: moreOpen }"
                :title="moreSwitching ? t('更多 · 正在切换清晰度') : t('更多')"
                aria-haspopup="true"
                :aria-expanded="moreOpen"
                @click="moreOpen = !moreOpen"
              >
                <Orbit v-if="moreSwitching" :size="17" :label="t('正在切换清晰度')" />
                <Icon v-else name="more" :size="19" filled />
              </button>
              <div v-if="moreOpen" ref="morePop" class="more-pop" role="group" :aria-label="t('更多播放设置')">
                <TrackOptions
                  v-if="showSubtitles && folded.includes('subtitles')"
                  ref="moreSubtitle"
                  kind="subtitle"
                  class="more-section"
                  :options="subOptions"
                  :value="subValue"
                  :current="subLabel"
                  @choose="selectSubtitle"
                  @done="closeMore"
                  @close="closeMore"
                >
                  <template #ai-status>
                    <AISubtitleStatus :state="ai" :account="subtitleAccount" :access="aiAccess" @retry="refreshAIMembership" />
                  </template>
                </TrackOptions>
                <TrackOptions
                  v-if="audios.length > 1 && folded.includes('audio')"
                  ref="moreAudio"
                  kind="audio"
                  class="more-section"
                  :options="audioOptions"
                  :value="audioValue"
                  :current="audioLabel"
                  @choose="setTrack('audio', $event)"
                  @done="closeMore"
                  @close="closeMore"
                />
                <SpeedOptions
                  v-if="folded.includes('speed')"
                  compact
                  class="more-section"
                  @done="closeMore"
                  @close="closeMore"
                />
                <QualityOptions
                  v-if="canChoose(state.quality) && folded.includes('quality')"
                  ref="moreQuality"
                  class="more-section"
                  @done="closeMore"
                  @close="closeMore"
                />
              </div>
            </div>

            <div class="tune-wrap" data-player-tune @focusout="onTuneFocusOut">
              <button
                ref="tuneButton"
                class="icon ghost"
                :class="{ on: tuneOpen }"
                :title="t('字幕与音画同步')"
                aria-haspopup="dialog"
                :aria-expanded="tuneOpen"
                @click="tuneOpen = !tuneOpen"
              >
                <Icon name="tune" :size="19" />
              </button>
              <div v-if="tuneOpen" ref="tunePop" class="more-pop tune-pop" role="dialog" :aria-label="t('字幕与音画同步')">
                <SubtitleSettings :kind="subKind" :subtitles="subShown" @close="closeTune" />
              </div>
            </div>

            <button class="icon ghost" :title="t('截图（S），存到「图片 › PKP」')" @click="screenshot">
              <Icon name="camera" :size="19" />
            </button>
            <button
              v-if="!bare || windowed"
              ref="listButton"
              class="icon ghost"
              :class="{ on: sidebarOpen }"
              :title="sidebarOpen ? t('收起侧栏') : t('展开侧栏')"
              :aria-label="sidebarOpen ? t('收起侧栏') : t('展开侧栏')"
              :aria-expanded="sidebarOpen"
              aria-controls="player-sidebar"
              @click="sidebarOpen = !sidebarOpen"
            >
              <Icon name="panel" :size="19" />
            </button>
            <button v-if="windowed" class="icon ghost" :title="t('回到主窗口播放')" @click="returnToMain">
              <Icon name="popin" :size="19" />
            </button>
            <button v-else class="icon ghost" :title="t('在独立窗口播放')" @click="popOut">
              <Icon name="popout" :size="19" />
            </button>
            <button class="icon ghost" :title="ui.fullscreen ? t('退出全屏') : t('全屏')" @click="toggleFullscreen">
              <Icon :name="ui.fullscreen ? 'exitFullscreen' : 'fullscreen'" :size="19" />
            </button>
          </div>
        </div>

        <!-- The separate window's playlist floats over the right of the
             picture: the window stays the picture's shape. It fades with
             the dock. -->
        <transition name="drawer">
          <aside
            v-if="windowed && listOpen"
            id="player-sidebar"
            class="drawer"
            :class="{ hidden: controlsHidden }"
            :aria-label="t('播放列表')"
            @pointerenter="controlsHovered = true"
            @pointerleave="controlsHovered = false"
          >
            <header>
              <div class="list-head">
                <div class="list-title">
                  <strong>{{ t("播放列表") }}</strong>
                  <small>{{ t("{n} 个视频", { n: player.playlist.length }) }}</small>
                </div>
                <SelectMenu
                  class="pill mode"
                  :title="t('播放顺序')"
                  :label="t('播放顺序')"
                  dark
                  :model-value="player.mode"
                  :options="modes"
                  @update:model-value="setPlayMode"
                  @open="modeOpen = true"
                  @close="modeOpen = false"
                >
                  <template #default="{ label }">
                    <Icon :name="modeIcon" :size="16" />
                    <b>{{ label }}</b>
                    <Icon name="chevronDown" :size="13" />
                  </template>
                </SelectMenu>
              </div>
              <button class="quiet icon" :title="t('收起播放列表')" @click="closeList">
                <Icon name="close" :size="15" />
              </button>
            </header>
            <PlaylistQueue compact />
          </aside>
        </transition>
      </section>

      <aside v-if="!bare && dockedListOpen" id="player-sidebar" class="playlist glass glass-sheet" :aria-label="t('播放列表')">
        <header>
          <div class="list-head">
            <div class="list-title">
              <strong>{{ t("播放列表") }}</strong>
              <small>{{ t("{n} 个视频", { n: player.playlist.length }) }}</small>
            </div>
            <SelectMenu
              class="pill mode"
              :title="t('播放顺序')"
              :label="t('播放顺序')"
              :model-value="player.mode"
              :options="modes"
              @update:model-value="setPlayMode"
            >
              <template #default="{ label }">
                <Icon :name="modeIcon" :size="17" />
                <b>{{ label }}</b>
                <Icon name="chevronDown" :size="14" />
              </template>
            </SelectMenu>
          </div>
          <button class="quiet icon" :title="t('收起侧栏')" :aria-label="t('收起侧栏')" @click="closeList">
            <Icon name="chevronRight" :size="17" />
          </button>
        </header>

        <PlaylistQueue />

        <section class="settings">
          <h4>{{ t("播放设置") }}</h4>
          <SelectMenu
            v-if="showSubtitles"
            class="row"
            :label="t('字幕')"
            :model-value="subValue"
            :options="subOptions"
            allow-reselect
            @update:model-value="selectSubtitle"
            @open="refreshAIMembership"
          >
            <Icon name="subtitles" :size="18" />
            <span>{{ t("字幕") }}</span>
            <b>{{ subLabel }}</b>
            <Icon name="chevronRight" :size="15" />
          </SelectMenu>
          <div v-else class="row off">
            <Icon name="subtitles" :size="18" />
            <span>{{ t("字幕") }}</span>
            <b>{{ t("无") }}</b>
          </div>
          <AISubtitleStatus
            v-if="ai.available"
            class="ai-sidebar-status"
            :state="ai"
            :account="subtitleAccount"
            :access="aiAccess"
            @retry="refreshAIMembership"
          />
          <p v-if="ai.available" class="ai-disclosure">
            {{ t("开启 AI 字幕会向 PikPak 发送音频和视频标识，并使用 AI 配额。") }}
          </p>
          <SelectMenu
            v-if="audios.length > 1"
            class="row"
            :label="t('音轨')"
            :model-value="audioValue"
            :options="audioOptions"
            @update:model-value="setTrack('audio', $event)"
          >
            <Icon name="waveform" :size="18" />
            <span>{{ t("音轨") }}</span>
            <b>{{ audioLabel }}</b>
            <Icon name="chevronRight" :size="15" />
          </SelectMenu>
          <div v-else class="row off">
            <Icon name="waveform" :size="18" />
            <span>{{ t("音轨") }}</span>
            <b>{{ audioLabel }}</b>
          </div>
        </section>
      </aside>
    </div>

    <footer v-if="!bare && nowPlaying" class="meta">
      <Icon name="video" :size="18" />
      <span class="meta-name" :title="nowPlaying.name">{{ nowPlaying.name }}</span>
      <template v-if="extension"><i>·</i><span>{{ extension }}</span></template>
      <template v-if="nowPlaying.size"><i>·</i><span>{{ bytes(nowPlaying.size) }}</span></template>
      <template v-if="state.width && state.height">
        <i>·</i><span class="mono">{{ state.width }} × {{ state.height }}</span>
      </template>
    </footer>
  </div>
</template>

<style scoped>
.ai-disclosure {
  margin: 0;
  padding: 0 14px 10px;
  color: var(--fg-dim);
  font-size: 11px;
  line-height: 1.5;
}

.ai-sidebar-status {
  margin: 0;
  padding: 0 14px 6px;
  color: var(--fg-dim);
}

.player {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.player.fullscreen {
  gap: 0;
}

.player.quiet {
  cursor: none;
}

/* ---------- title island ---------- */

.bar {
  display: flex;
  align-items: center;
  gap: 14px;
  min-height: 60px;
  padding: 8px 18px 8px 14px;
  border-radius: var(--radius-island);
}

.back {
  flex: none;
}

.divider {
  width: 1px;
  height: 22px;
  background: var(--line-strong);
}

.now {
  flex: 0 1 auto;
  min-width: 0;
  font-size: 20px;
  font-weight: 600;
  letter-spacing: -0.01em;
  max-width: 34%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* The path gives way first: the title, the account and the button keep
   their room, the path takes what is left and the spacer the rest. */
.path {
  flex: 0 100 auto;
  min-width: 0;
  font-size: 13px;
  color: var(--fg-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.spacer {
  flex: 1;
}

.bar .download {
  flex: none;
}

.body {
  flex: 1;
  min-height: 0;
  display: flex;
  gap: 14px;
}

/* ---------- stage ---------- */

/* No background, no border, no shadow: anything painted here would hide the
   native video view behind the webview. A size container: the dock lays
   itself out by the stage's width, and its popovers are bounded by its height. */
.stage {
  position: relative;
  flex: 1;
  min-width: 0;
  display: grid;
  place-items: center;
  border-radius: 18px;
  overflow: hidden;
  container: stage / size;
}

.fullscreen .stage {
  border-radius: 0;
}

/* The separate window has no frame: the picture is what moves it, and what
   sits on the picture to be clicked does not. */
.stage.moves {
  --wails-draggable: drag;
}

.stage.moves .stage-state,
.stage.moves .stage-error,
.stage.moves .next,
.stage.moves .dock,
.stage.moves .drawer,
.stage.moves .titlebar button {
  --wails-draggable: no-drag;
}

/* ---------- the separate window's title bar and playlist ---------- */

/* Where a title bar would be, over the top of the picture: a shade for the
   name, the account and the traffic lights, which take its first 80 points
   and centre 26 down. The shade runs past the 52-point row so its second
   line reads on a bright frame. It comes and goes with the dock. */
.titlebar {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 68px;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 12px 16px 96px;
  background: linear-gradient(
    180deg,
    rgba(6, 10, 20, 0.8),
    rgba(6, 10, 20, 0.58) 52%,
    rgba(6, 10, 20, 0)
  );
  color: #eef6ff;
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.5);
  --fg: #eef6ff;
  --fg-dim: rgba(228, 240, 255, 0.76);
  transition: opacity 0.35s var(--ease);
}

.titlebar.hidden {
  opacity: 0;
  pointer-events: none;
}

/* Windows: no traffic lights on the left; the caption buttons end the row. */
.titlebar.win {
  padding-left: 18px;
  padding-right: 8px;
}

.titlebar-name {
  flex: 1;
  min-width: 0;
  font-size: 14px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.titlebar-owner {
  flex: 0 1 auto;
  min-width: 0;
  max-width: 200px;
}

.titlebar .ghost {
  color: #eef6ff;
}

.titlebar .caption-close:hover {
  background: #c42b1c;
  color: #fff;
}

/* Over the right of the picture, between the title bar and the dock, so the
   window keeps the picture's shape. The dock's fog, and its light type
   whatever the theme. */
.drawer {
  position: absolute;
  top: 60px;
  right: 18px;
  bottom: 162px;
  width: min(320px, calc(100% - 36px));
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: 22px;
  background: rgba(14, 28, 52, 0.84);
  border: 1px solid rgba(190, 225, 255, 0.22);
  box-shadow:
    0 16px 48px rgba(0, 0, 0, 0.4),
    inset 0 1px 0 rgba(255, 255, 255, 0.28);
  color: #eef6ff;
  cursor: auto;
  --fg: #eef6ff;
  --fg-dim: rgba(228, 240, 255, 0.66);
  --line: rgba(190, 225, 255, 0.16);
  --line-strong: rgba(190, 225, 255, 0.26);
  --glass-control: rgba(255, 255, 255, 0.14);
  --accent-soft: color-mix(in srgb, var(--accent) 26%, transparent);
  transition:
    opacity 0.35s var(--ease),
    transform 0.35s var(--ease);
}

.drawer.hidden {
  opacity: 0;
  transform: translateX(16px);
  pointer-events: none;
}

/* A short window has no room between the bars: the list runs down over the
   dock, below the title bar still. */
@container stage (max-height: 420px) {
  .drawer {
    bottom: 12px;
  }
}

.drawer > header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 14px 10px 10px 18px;
}

/* The title and Play order share a row while they fit; a long title or mode
   sends the pill to a row of its own, under the title. */
.list-head {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.list-title {
  flex: 1 0 auto;
  max-width: 100%;
}

.list-head .pill.mode {
  margin-left: auto;
}

/* The close button stays level with the title when the pill wraps. */
.list-head + button {
  align-self: flex-start;
  margin-top: var(--close-top);
}

.playlist > header {
  --close-top: 10px;
}

.drawer > header {
  --close-top: 5px;
}

.drawer header strong {
  display: block;
  font-size: 17px;
  font-weight: 600;
}

.drawer header small {
  font-size: 12px;
  color: var(--fg-dim);
}

.drawer .pill.mode {
  min-height: 28px;
  padding: 0 8px;
  font-size: 12px;
}

.drawer .quiet {
  color: #eef6ff;
}

.drawer-enter-active,
.drawer-leave-active {
  transition:
    opacity var(--dur) var(--ease),
    transform var(--dur) var(--ease);
}

.drawer-enter-from,
.drawer-leave-to {
  opacity: 0;
  transform: translateX(16px);
}

/* Overlays sit on the raw picture, where backdrop-filter has nothing to
   sample: the native layer is below the webview. The fog is therefore a
   real tint, dark enough for 4.5:1 on any frame. */
.stage-state,
.stage-wait,
.stage-error,
.next,
.dock,
.hover {
  background: rgba(14, 28, 52, 0.7);
  border: 1px solid rgba(190, 225, 255, 0.22);
  box-shadow:
    0 16px 48px rgba(0, 0, 0, 0.4),
    inset 0 1px 0 rgba(255, 255, 255, 0.28),
    inset 1px 0 0 rgba(255, 255, 255, 0.1);
  color: #eef6ff;
}

.stage-state {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 18px;
  border-radius: var(--radius-pill);
  font-size: 14px;
}

/* Seeking and buffering: a dial with how far the wait has got, and how fast
   the data is coming in under it. */
.stage-wait {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  min-width: 136px;
  padding: 16px 20px 14px;
  border-radius: 20px;
}

.stage-wait-dial {
  position: relative;
  display: grid;
  place-items: center;
  width: 52px;
  height: 52px;
}

.stage-wait-dial svg {
  position: absolute;
  inset: 0;
  transform: rotate(-90deg);
}

.stage-wait-dial circle {
  fill: none;
  stroke-width: 3.5;
}

.stage-wait-dial .track {
  stroke: rgba(255, 255, 255, 0.16);
}

.stage-wait-dial .fill {
  stroke: var(--progress-to);
  stroke-linecap: round;
  stroke-dasharray: 100 100;
  transition: stroke-dashoffset 0.3s var(--ease);
}

.stage-wait-dial b {
  font-size: 15px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.stage-wait-dial small {
  margin-left: 1px;
  font-size: 10px;
  font-weight: 500;
  opacity: 0.8;
}

.stage-wait-text {
  font-size: 13px;
  color: #c9dcf0;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

/* A card, not a pill: the reason can run two lines, and Retry leads. */
.stage-error {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  max-width: min(420px, calc(100% - 48px));
  padding: 24px 28px 20px;
  border-radius: 22px;
  text-align: center;
}

.stage-error-icon {
  display: grid;
  place-items: center;
  width: 44px;
  height: 44px;
  margin-bottom: 6px;
  border-radius: 50%;
  color: #ff9fb2;
  background: rgba(255, 107, 138, 0.16);
}

.stage-error strong {
  font-size: 16px;
  font-weight: 600;
}

.stage-error p {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: #b9cde6;
  /* Wrap at the punctuation, evenly; a run with none still breaks anywhere. */
  text-wrap: balance;
  word-break: keep-all;
  overflow-wrap: anywhere;
}

.stage-error-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 8px;
  margin-top: 14px;
}

.stage-error-actions button:not(.primary) {
  border-color: rgba(255, 255, 255, 0.14);
  background: rgba(255, 255, 255, 0.08);
  color: #eef6ff;
}

.stage-error-actions button:not(.primary):hover {
  background: rgba(255, 255, 255, 0.16);
}

.stage-loading {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 14px;
  padding: 24px;
  color: #eef6ff;
  background: rgba(8, 12, 22, 0.55);
  animation: stage-loading-in 0.18s var(--ease) both;
}

.stage-loading-name {
  max-width: min(70%, 640px);
  font-size: 15px;
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  opacity: 0.85;
}

@keyframes stage-loading-in {
  from {
    opacity: 0;
  }
}

/* Above the dock, which sits 18px up and is 132px tall (two rows: the 30px
   timeline and the 64px play button, 14px padding, 8px gap, its border);
   118px left the notice under the dock's glass. */
.next {
  position: absolute;
  right: 18px;
  bottom: 162px;
  display: flex;
  align-items: center;
  gap: 10px;
  max-width: min(420px, calc(100% - 36px));
  padding: 8px 8px 8px 14px;
  border-radius: var(--radius-pill);
  font-size: 13px;
  transition: bottom 0.35s var(--ease);
}

/* Follows the dock down when it hides, so the notice never floats mid-air. */
.next.low {
  bottom: 18px;
}

/* The playlist drawer holds the right: the notice moves to the left. */
.next.beside {
  right: auto;
  left: 18px;
}

/* No trailing button: even padding on both ends. */
.next.notice {
  padding-right: 14px;
}

/* The words and the file share a line while the file keeps some room; a
   long label (a translated one) puts the file on a line of its own. */
.next-text {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  column-gap: 10px;
}

.next-label {
  flex: none;
  max-width: 100%;
  color: #a9c6e8;
}

.next-name {
  flex: 1 1 80px;
  min-width: 80px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.next .quiet {
  flex: none;
  min-height: 28px;
  color: #eef6ff;
}

.rise-enter-active,
.rise-leave-active {
  transition:
    opacity var(--dur) var(--ease),
    transform var(--dur) var(--ease);
}

.rise-enter-from,
.rise-leave-to {
  opacity: 0;
  transform: translateY(8px);
}

/* ---------- dock: one piece of glass, two rows ---------- */

.dock {
  position: absolute;
  left: 18px;
  right: 18px;
  bottom: 18px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 14px 24px 14px 20px;
  border-radius: 28px;
  background-image: linear-gradient(180deg, rgba(255, 255, 255, 0.07), transparent 40%);
  transition:
    opacity 0.35s var(--ease),
    transform 0.35s var(--ease);
}

.dock.hidden {
  opacity: 0;
  transform: translateY(12px);
  pointer-events: none;
}

.timeline {
  display: flex;
  align-items: center;
  gap: 16px;
  height: 30px;
}

.time {
  font-size: 15px;
  white-space: nowrap;
  color: #e9f2fc;
  min-width: 50px;
}

.time:last-child {
  text-align: right;
}

.scrub {
  position: relative;
  flex: 1;
  min-width: 90px;
  height: 28px;
  display: flex;
  align-items: center;
  cursor: pointer;
  touch-action: none;
  outline: none;
}

.scrub:focus-visible .knob {
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--progress-from) 60%, transparent);
}

.track {
  position: absolute;
  left: 0;
  right: 0;
  height: 4px;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.22);
  overflow: hidden;
}

.track i {
  position: absolute;
  left: 0;
  top: 0;
  height: 100%;
  border-radius: inherit;
}

/* Buffered and played are different facts and get different weights. */
.track .cache {
  background: rgba(255, 255, 255, 0.28);
}

.track .played {
  background: var(--progress);
  transition: width 0.12s linear;
}

.tick {
  position: absolute;
  top: 50%;
  width: 6px;
  height: 6px;
  margin: -3px 0 0 -3px;
  border-radius: 50%;
  background: #eaf4ff;
  opacity: 0.85;
  pointer-events: none;
}

.tick.passed {
  background: #ffffff;
}

.knob {
  position: absolute;
  top: 50%;
  width: 16px;
  height: 16px;
  margin: -8px 0 0 -8px;
  border-radius: 50%;
  background: #ffffff;
  box-shadow:
    0 0 0 4px color-mix(in srgb, var(--progress-from) 35%, transparent),
    0 1px 4px rgba(0, 0, 0, 0.45);
  transition:
    width 0.12s linear,
    transform var(--dur) var(--ease);
  pointer-events: none;
}

.scrub:hover .knob {
  transform: scale(1.2);
}

.hover {
  position: absolute;
  bottom: 26px;
  transform: translateX(-50%);
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 10px;
  border-radius: 10px;
  font-size: 12px;
  white-space: nowrap;
  pointer-events: none;
}

.hover-chapter {
  color: #a9c6e8;
}

.controls {
  display: flex;
  align-items: center;
  gap: 10px;
}

.main {
  width: 64px;
  height: 64px;
  min-height: 64px;
  margin-right: 8px;
  background: var(--primary) border-box;
  color: #ffffff;
  border: 1px solid rgba(255, 255, 255, 0.35);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.5),
    0 0 0 5px color-mix(in srgb, var(--progress-from) 22%, transparent),
    0 8px 24px var(--primary-glow);
}

/* Keeps its fill and rim: the plain button:hover outranks `.main`. */
.main:hover:not(:disabled) {
  background: var(--primary) border-box;
  border-color: rgba(255, 255, 255, 0.35);
  filter: brightness(1.08);
}

/* The step amount sits inside the circular arrow, as on the design board. */
.step {
  position: relative;
  width: 46px;
  height: 46px;
  min-height: 46px;
  padding: 0;
  border-radius: 50%;
  background: transparent;
  border-color: transparent;
  color: #e3eefb;
}

.step em {
  position: absolute;
  font-style: normal;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: -0.02em;
  padding-top: 2px;
}

.ghost {
  background: transparent;
  border-color: transparent;
  color: #e3eefb;
}

.ghost:hover,
.step:hover,
.ghost.on {
  background: rgba(255, 255, 255, 0.12);
}

/* Round controls keep their shape in a narrow dock; the volume slider gives
   way only after every advanced control moves into the overflow menu. */
.main,
.step,
.ghost {
  flex: none;
}

.volume {
  display: flex;
  align-items: center;
  gap: 4px;
  width: 170px;
  margin-left: 6px;
  padding-left: 10px;
  border-left: 1px solid rgba(255, 255, 255, 0.14);
  color: #d6e6f8;
}

.controls:not(.crowded) .volume {
  flex: none;
}

@container stage (max-width: 650px) {
  .controls {
    gap: 6px;
  }

  .main {
    width: 52px;
    height: 52px;
    min-height: 52px;
    margin-right: 0;
  }

  .step {
    width: 36px;
    height: 36px;
    min-height: 36px;
  }
}

/* ---------- Subtitles · Audio tracks · Quality · Speed: in the dock, or in More ---------- */

/* Entries of the row itself (no box of their own), so each can leave it alone. */
.advanced {
  display: contents;
}

.more-wrap {
  position: relative;
}

.tune-wrap {
  position: relative;
  flex: none;
}

.more-pop.tune-pop {
  width: 300px;
  padding: 12px 14px 12px;
}

/* An entry folded into More, or More with nothing in it (`fitAdvanced`): out
   of the row and unseen, but still laid out, so its width can be measured. */
.controls .out {
  position: absolute;
  width: max-content;
  visibility: hidden;
}

/* The Quality menu's fixed dark glass, above the button. The stage clips, so
   the popover's height is bounded by it and scrolls past that. */
.more-pop {
  position: absolute;
  right: -10px;
  bottom: calc(100% + 13px);
  z-index: 4;
  width: 240px;
  max-height: calc(100cqh - 110px);
  overflow-y: auto;
  padding: 10px 14px 14px;
  border: 1px solid rgba(201, 238, 255, 0.72);
  border-radius: 20px;
  background: linear-gradient(145deg, rgba(53, 87, 134, 0.96), rgba(16, 31, 61, 0.98));
  box-shadow:
    inset 0 1px rgba(255, 255, 255, 0.54),
    0 18px 34px rgba(2, 14, 38, 0.46);
  color: #edf8ff;
}

.more-pop > * + .more-section {
  margin-top: 8px;
  padding-top: 12px;
  border-top: 1px solid rgba(202, 233, 255, 0.18);
}

/* ---------- playlist island ---------- */

.playlist {
  width: 330px;
  flex: none;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: var(--radius-sheet);
}

.bar .owner {
  flex: none;
  min-width: 0;
  max-width: 220px;
}

.playlist > header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 20px 14px 14px 22px;
}

.playlist header strong {
  display: block;
  font-size: 24px;
  font-weight: 600;
  letter-spacing: -0.01em;
}

.playlist header small {
  font-size: 14px;
  color: var(--fg-dim);
}

/* Play order: the current mode, opening the app's own list. */
.pill.mode {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 32px;
  padding: 0 10px;
  border-radius: 12px;
  border: 1px solid var(--line-strong);
  background: color-mix(in srgb, var(--glass-control) 70%, transparent);
  color: var(--fg);
  font-size: 15px;
  font-weight: 500;
  white-space: nowrap;
}

.pill.mode b {
  font-weight: 500;
}

.settings {
  padding: 14px 14px 16px;
  border-top: 1px solid var(--line);
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.settings h4 {
  margin: 2px 8px 6px;
  font-size: 17px;
  font-weight: 600;
}

/* A settings row reads the current value and opens its list. */
.row {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 50px;
  padding: 0 12px 0 16px;
  border-radius: 14px;
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--glass-control) 55%, transparent);
  font-size: 15px;
  font-weight: 500;
}

.row.off {
  color: var(--fg-dim);
}

/* The label keeps its words; the value (a track name) gives way. */
.row > span {
  flex: 1 0 auto;
}

.row b {
  min-width: 0;
  font-weight: 500;
  color: var(--fg-dim);
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* ---------- meta line ---------- */

.meta {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 8px;
  font-size: 13px;
  color: var(--fg-dim);
  min-height: 20px;
}

.meta-name {
  color: var(--fg);
  max-width: 40%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.meta i {
  font-style: normal;
  opacity: 0.6;
}
</style>
