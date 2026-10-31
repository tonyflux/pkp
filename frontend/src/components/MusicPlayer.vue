<script setup lang="ts">
// Music playback: the immersive page while the queue is
// music. Nothing to watch, so the cover and the track are the subject: the
// file's own embedded picture (read by Go from its header), else PikPak's
// thumbnail, else a plain type figure — never a cover borrowed from another
// track. The queue stays beside it; one dark control dock holds the
// timeline, play / skip and the volume. Same playback session as the video
// page and the bar under the files: Collapse and continue playing only changes how it is
// shown. No lyrics, spectrum or anything else made up.
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import AccountIdentity from "./AccountIdentity.vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import RangeSlider from "./RangeSlider.vue";
import SelectMenu from "./SelectMenu.vue";
import { Window } from "@wailsio/runtime";
import Thumb from "./Thumb.vue";
import { bytes, clock } from "../format";
import { onWindows } from "../dom";
import { modes, usePlayerKeys, useTransport } from "../transport";
import {
  accounts,
  audioDetails,
  canPlayNextTrack,
  collapsePlayer,
  nowPlaying,
  playAt,
  player,
  playerCommands,
  playingElsewhere,
  playingOwner,
  playNextTrack,
  playPreviousTrack,
  setFullscreen,
  setPlayMode,
  stopPlayback,
  ui,
  popOut,
  returnToMain,
  type PlayMode,
  type Route,
} from "../store";
import type { AudioDetails, Entry } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import { t, N_, i18n } from "../i18n";

defineProps<{ windowed?: boolean }>();
const { scrubEl, scrub, state, position, progress } = useTransport();

const entry = computed(() => nowPlaying.value);
const count = computed(() => player.playlist.length);
const two = (n: number) => String(n).padStart(2, "0");
const showOwner = computed(() => !!playingOwner.value && (accounts.list.length > 1 || playingElsewhere.value));

const RETURN_LABELS: Partial<Record<Route, string>> = {
  files: N_("我的文件"),
  viewer: N_("预览"),
  assistant: N_("AI 助手"),
  history: N_("播放历史"),
  transfers: N_("传输中心"),
  shares: N_("我的分享"),
  cleanup: N_("清理"),
  settings: N_("设置"),
};
const returnLabel = computed(() => {
  const label = RETURN_LABELS[player.returnRoute];
  return label ? t(label) : t("返回");
});

/* ---------- what the file says about itself ---------- */

// Read once the file is open (Go reads its header on a connection of its
// own). Cleared the moment the track changes, so the next one never shows
// this one's cover or title while its own are on the way.
const details = ref<AudioDetails | null>(null);
watch(
  () => [entry.value?.id, state.value.loading, state.value.duration > 0] as const,
  async ([id, loading, loaded]) => {
    if (details.value && details.value.fileId !== id) details.value = null;
    if (!id || loading || !loaded || details.value?.fileId === id) return;
    const got = await audioDetails(id);
    if (got && entry.value?.id === id) details.value = got;
  },
  { immediate: true },
);

function stem(name: string): string {
  const dot = name.lastIndexOf(".");
  return dot > 0 ? name.slice(0, dot) : name;
}

function extension(e: Entry | null): string {
  if (!e) return "";
  const dot = e.name.lastIndexOf(".");
  return dot > 0 ? e.name.slice(dot + 1).toUpperCase() : "";
}

// Only formats that are lossless by definition; AAC and ALAC share .m4a,
// so it makes no claim either way.
const LOSSLESS = new Set(["FLAC", "APE", "WAV", "AIFF", "AIF", "WV", "TAK", "TTA", "DSF", "DFF"]);

const title = computed(() => details.value?.title || (entry.value ? stem(entry.value.name) : "—"));
const year = computed(() => details.value?.date.match(/^\d{4}/)?.[0] ?? "");
const byline = computed(() => {
  const d = details.value;
  const parts = [d?.artist, d?.album, year.value ? t("{year} 年", { year: year.value }) : ""].filter(Boolean);
  if (parts.length) return parts.join(" · ");
  const ext = extension(entry.value);
  return ext ? t("{ext} 音频", { ext }) : t("音频");
});
const format = computed(() => {
  const ext = extension(entry.value);
  if (!ext) return "";
  return LOSSLESS.has(ext) ? t("{ext} · 无损", { ext }) : ext;
});
const source = computed(() => player.source);
const folderName = computed(() => {
  const parts = player.source.split(" / ").filter(Boolean);
  return parts.at(-1) ?? "";
});
const cover = computed(() => details.value?.cover || entry.value?.thumbnail || "");
// The cover, blurred far out, tints the room behind the track.
const artStyle = computed(() => (cover.value ? { "--art": `url("${cover.value}")` } : undefined));

/* ---------- transport ---------- */

const hasDuration = computed(() => state.value.duration > 0);
const caption = computed(() => {
  const s = state.value;
  if (s.error) return t("无法播放");
  if (s.loading) return t("加载中");
  if (s.buffering) return s.bufferPercent > 0 ? t("缓冲中 · {n}%", { n: s.bufferPercent }) : t("缓冲中");
  return s.paused ? t("已暂停") : t("正在播放");
});
const sounding = computed(() => !state.value.paused && !state.value.loading && !state.value.error && !state.value.buffering);
// Music repeats a track, not an episode: Repeat track here, Repeat episode for video.
const musicModes = computed(() => modes.map((m) => (m.value === "one" ? { ...m, label: t("单曲循环") } : { ...m, label: m.label })));
const modeLabel = computed(() => musicModes.value.find((m) => m.value === player.mode)?.label ?? "");
const modeOptions = computed(() => musicModes.value.map((m) => ({ value: m.value, label: m.label })));

function toggleMute() {
  void playerCommands.mute(!state.value.muted);
}

function retry() {
  void playAt(player.index);
}

function queueTail(e: Entry, i: number): string {
  if (i === player.index) return hasDuration.value ? clock(state.value.duration) : "";
  return e.duration > 0 ? clock(e.duration) : "";
}

function queueMeta(e: Entry): string {
  return [extension(e), e.size > 0 ? bytes(e.size) : ""].filter(Boolean).join(" · ");
}

// The current track scrolls into the queue's view when it changes.
const queueList = ref<HTMLElement>();
watch(
  () => player.index,
  () => {
    requestAnimationFrame(() => {
      queueList.value?.querySelector<HTMLElement>("[aria-current='true']")?.scrollIntoView({ block: "nearest" });
    });
  },
);

// The player's keyboard map; there is no picture to make full screen or
// capture here.
const onKey = usePlayerKeys({
  fullscreen: () => undefined,
  screenshot: () => undefined,
  escape: () => {
    if (!ui.fullscreen) return false;
    void setFullscreen(false);
    return true;
  },
});
onMounted(() => window.addEventListener("keydown", onKey));
onBeforeUnmount(() => window.removeEventListener("keydown", onKey));
</script>

<template>
  <section class="music" :class="{ windowed, win: onWindows }" style="--wails-draggable: no-drag" :aria-label="t('音乐播放')">
    <header class="topbar" style="--wails-draggable: drag">
      <nav class="crumbs" :aria-label="t('位置')">
        <button class="back" style="--wails-draggable: no-drag" :title="windowed ? t('回到主窗口播放') : t('回到{name}，音乐继续播放', { name: returnLabel })" @click="windowed ? returnToMain() : collapsePlayer()">
          <Icon name="chevronLeft" :size="17" />
        </button>
        <span class="from">{{ windowed ? t("独立窗口播放") : returnLabel }}</span>
        <span class="slash" aria-hidden="true">/</span>
        <strong>{{ t("音乐播放") }}</strong>
      </nav>
      <div class="actions" style="--wails-draggable: no-drag">
        <span v-if="showOwner" class="owner" :aria-label="t('所属账号')">
          <AccountIdentity :account="playingOwner" :size="24" dense />
        </span>
        <span class="source-pill"><i class="dot" aria-hidden="true" />{{ t("云盘音频") }}</span>
        <button v-if="!windowed" class="collapse" :title="t('独立窗口播放')" :aria-label="t('独立窗口播放')" @click="popOut">
          <Icon name="popout" :size="17" />
        </button>
        <button v-if="!windowed" class="collapse" :title="t('回到文件，音乐在底部播放条里继续')" @click="collapsePlayer">
          <Icon name="collapse" :size="17" />
          <span>{{ t("收起并继续播放") }}</span>
        </button>
        <button v-else-if="onWindows" class="collapse" :title="t('关闭窗口并继续播放')" :aria-label="t('关闭窗口并继续播放')" @click="Window.Close()">
          <Icon name="close" :size="17" />
        </button>
      </div>
    </header>

    <main class="layout">
      <section class="listening" :class="{ art: !!cover }" :style="artStyle">
        <div class="intro">
          <!-- The English eyebrow carries its gloss in the reader's language;
               in English it would only repeat itself. -->
          <span class="eyebrow">
            <i class="dot" aria-hidden="true" />NOW PLAYING
            <template v-if="i18n.locale !== 'en'"><span class="slash">/</span> {{ t("正在播放") }}</template>
          </span>
          <span class="number num">{{ two(player.index + 1) }} <span>/ {{ two(count) }}</span></span>
        </div>

        <div class="hero">
          <div class="cover-wrap">
            <div class="cover-shadow" aria-hidden="true" />
            <div class="cover">
              <Thumb :key="cover" :src="cover" :alt="cover ? t('{title} 的封面', { title }) : ''">
                <span class="cover-fallback"><Icon name="audio" :size="windowed ? 36 : 76" /></span>
              </Thumb>
              <span v-if="state.loading" class="cover-wait"><Orbit :size="26" :label="t('加载中')" /></span>
            </div>
            <p class="cover-caption" aria-hidden="true">PKP · YOUR SPACE, YOUR SOUND</p>
          </div>

          <div class="track-info">
            <span class="side-rule" aria-hidden="true" />
            <p class="overline">
              {{ t("来自你的云端收藏") }}
              <span class="num">{{ two(player.index + 1) }} — {{ two(count) }}</span>
            </p>
            <h1 :title="entry?.name">{{ title }}</h1>
            <p class="byline">{{ byline }}</p>
            <span class="info-line" aria-hidden="true" />

            <div v-if="state.error" class="problem" role="alert">
              <strong>{{ t("无法播放") }}</strong>
              <p>{{ state.error === "playback failed" ? t("这首没能加载出来，可以重试一次") : state.error }}</p>
              <div>
                <button class="primary" @click="retry">{{ t("重试") }}</button>
                <button @click="stopPlayback">{{ t("停止播放") }}</button>
              </div>
            </div>
            <template v-else>
              <p class="context">{{ t("把这一刻留给音乐。") }}</p>
              <dl class="metadata">
                <div v-if="format" class="metadata-format">
                  <dt>{{ t("文件格式") }}</dt>
                  <dd>{{ format }}</dd>
                </div>
                <div v-if="source">
                  <dt>{{ t("播放来源") }}</dt>
                  <dd :title="source">{{ source }}</dd>
                </div>
              </dl>
            </template>
          </div>
        </div>

        <section class="dock" :aria-label="t('播放控制')">
          <div class="seek-row">
            <span class="time num">{{ clock(position) }}</span>
            <div
              ref="scrubEl"
              class="scrub"
              :class="{ scrubbing: scrub.scrubbing.value }"
              role="slider"
              tabindex="0"
              :aria-label="t('播放进度')"
              :aria-valuemin="0"
              :aria-valuemax="Math.round(state.duration)"
              :aria-valuenow="Math.round(position)"
              :aria-valuetext="clock(position)"
              :aria-disabled="!hasDuration"
              @pointerdown="scrub.onPointerDown"
              @pointermove="scrub.onPointerMove"
              @pointerup="scrub.onPointerUp"
              @pointercancel="scrub.onPointerUp"
              @keydown="scrub.onKeyDown"
            >
              <div class="track"><i :style="{ width: progress + '%' }" /></div>
              <i v-if="hasDuration" class="knob" :style="{ left: progress + '%' }" />
            </div>
            <span class="time num end">{{ hasDuration ? clock(state.duration) : "--:--" }}</span>
          </div>

          <div class="control-row">
            <div class="transport">
              <button class="skip" :title="t('上一首')" :aria-label="t('上一首')" :disabled="!hasDuration && player.index === 0" @click="playPreviousTrack">
                <Icon name="skipPrevious" :size="20" filled />
              </button>
              <button class="play" :title="state.paused ? t('播放') : t('暂停')" :aria-label="state.paused ? t('播放') : t('暂停')" @click="playerCommands.toggle()">
                <Icon :name="state.paused ? 'play' : 'pause'" :size="23" filled />
              </button>
              <button class="skip" :title="t('下一首')" :aria-label="t('下一首')" :disabled="!canPlayNextTrack" @click="playNextTrack">
                <Icon name="skipNext" :size="20" filled />
              </button>
            </div>

            <span class="caption" role="status">
              <Orbit v-if="state.loading" :size="14" :label="t('加载中')" />
              {{ caption }}
              <span v-if="sounding" class="wave" aria-hidden="true"><i /><i /><i /><i /></span>
            </span>

            <div class="volume">
              <button class="skip" :title="state.muted ? t('取消静音') : t('静音')" @click="toggleMute">
                <Icon :name="state.muted || state.volume <= 0 ? 'mute' : 'volume'" :size="19" />
              </button>
              <RangeSlider :label="t('音量')" :model-value="state.muted ? 0 : state.volume" @update:model-value="(v) => playerCommands.volume(v)" />
            </div>
          </div>
        </section>
      </section>

      <aside class="queue" :aria-label="t('播放队列')">
        <div class="queue-top">
          <div>
            <span class="eyebrow">
              UP NEXT
              <template v-if="i18n.locale !== 'en'"><span class="slash">/</span> {{ t("稍后播放") }}</template>
            </span>
            <h2>{{ t("播放队列") }}<sup class="num">{{ two(count) }}</sup></h2>
          </div>
          <SelectMenu
            class="mode"
            :label="t('播放顺序')"
            :title="t('播放顺序：{mode}', { mode: modeLabel })"
            :model-value="player.mode"
            :options="modeOptions"
            @update:model-value="(v) => setPlayMode(v as PlayMode)"
          >
            <Icon :name="modes.find((m) => m.value === player.mode)?.icon ?? 'list'" :size="19" />
          </SelectMenu>
        </div>
        <p class="queue-source">{{ folderName ? t("当前文件夹 · {name}", { name: folderName }) : t("播放顺序 · {mode}", { mode: modeLabel }) }}</p>
        <span class="queue-divider" aria-hidden="true" />

        <ol ref="queueList">
          <li v-for="(e, i) in player.playlist" :key="e.id">
            <button
              class="queue-track"
              :class="{ active: i === player.index }"
              :aria-current="i === player.index ? 'true' : undefined"
              :title="e.name"
              @click="i === player.index ? playerCommands.toggle() : playAt(i)"
            >
              <span class="thumb">
                <Thumb :src="i === player.index ? cover : e.thumbnail" alt="" lazy>
                  <Icon name="audio" :size="22" />
                </Thumb>
              </span>
              <span class="queue-copy">
                <strong>{{ i === player.index ? title : stem(e.name) }}</strong>
                <small>{{ queueMeta(e) }}</small>
              </span>
              <span class="queue-tail num">
                <span v-if="i === player.index && sounding" class="wave mini" role="img" :aria-label="t('正在播放')"><i /><i /><i /></span>
                <template v-else>{{ queueTail(e, i) }}</template>
              </span>
            </button>
          </li>
        </ol>

        <div class="queue-bottom">
          <span class="queue-divider" aria-hidden="true" />
          <p>{{ folderName ? t("同一文件夹中的音频") : t("这一次的播放队列") }}</p>
          <small>{{ t("{n} 首曲目", { n: count }) }}</small>
        </div>
      </aside>
    </main>
  </section>
</template>

<style scoped>
/* A dark listening room in either theme, like the video page's stage: the
   colours are the mock's, the product's silver-blue on deep navy. */
.music {
  --fg: #ecf4fa;
  --fg-dim: #a9bbcb;
  --line: rgba(190, 221, 245, 0.14);
  --line-strong: rgba(190, 221, 245, 0.26);
  --glass-control: rgba(183, 216, 238, 0.12);
  --accent: #9ad3f7;
  --accent-soft: rgba(140, 196, 228, 0.16);
  --progress: linear-gradient(90deg, #7fc2ee, #a9dcfa);
  --progress-from: #7fc2ee;
  --tone: #96d2f6;
  container: music / size;
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: 24px;
  border: 1px solid rgba(190, 221, 245, 0.12);
  color: var(--fg);
  color-scheme: dark;
  background:
    radial-gradient(ellipse 700px 500px at 29% 48%, #334657 0%, #1a3048 48%, transparent 100%),
    radial-gradient(ellipse 650px 500px at 75% 18%, #23384f 0%, transparent 80%),
    #0b1728;
  box-shadow: 0 24px 60px rgba(2, 10, 24, 0.4);
}

.num {
  font-variant-numeric: tabular-nums;
}

button {
  color: inherit;
}

/* ---------- top bar ---------- */

.topbar {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-height: 72px;
  padding: 0 28px;
}

.crumbs,
.actions {
  display: flex;
  align-items: center;
  gap: 14px;
  min-width: 0;
}

.crumbs {
  font-size: 14px;
  color: var(--fg-dim);
}

.crumbs strong {
  color: #eff5fc;
  font-weight: 600;
  white-space: nowrap;
}

.from {
  white-space: nowrap;
}

.slash {
  opacity: 0.45;
  margin: 0 2px;
}

.back {
  width: 36px;
  height: 36px;
  min-height: 36px;
  padding: 0;
  border-radius: 50%;
  border: 1px solid rgba(213, 236, 250, 0.24);
  background: transparent;
}

.back:hover:not(:disabled) {
  background: rgba(209, 236, 252, 0.12);
}

.source-pill {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 8px 14px;
  border-radius: 999px;
  border: 1px solid rgba(180, 213, 235, 0.16);
  background: rgba(196, 220, 240, 0.045);
  font-size: 12px;
  color: #b3c5d7;
  white-space: nowrap;
}

.dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #98d0f8;
  box-shadow: 0 0 13px #7abff0;
}

.collapse {
  gap: 9px;
  min-height: 40px;
  padding: 0 15px;
  border-radius: 14px;
  border: 1px solid rgba(213, 235, 250, 0.23);
  background: rgba(183, 216, 238, 0.075);
  color: #dbecf8;
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
}

.collapse:hover:not(:disabled) {
  background: rgba(183, 216, 238, 0.16);
}

/* ---------- layout ---------- */

.layout {
  flex: 1;
  min-height: 0;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 330px;
  gap: 20px;
  padding: 0 24px 24px;
}

.listening,
.queue {
  position: relative;
  min-width: 0;
  min-height: 0;
  border-radius: 26px;
}

.listening {
  isolation: isolate;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  gap: 18px;
  padding: 26px 32px 28px;
  overflow: hidden;
  border: 1px solid rgba(192, 225, 248, 0.12);
  background: linear-gradient(125deg, rgba(127, 163, 189, 0.13), rgba(11, 26, 45, 0.5) 56%, rgba(17, 37, 57, 0.3));
  box-shadow:
    inset 0 1px 0 rgba(229, 246, 255, 0.16),
    0 30px 85px rgba(4, 14, 28, 0.24);
}

/* The cover, blurred far out, tints the room: only when there is one. */
.listening.art::before {
  content: "";
  position: absolute;
  inset: 0;
  z-index: -1;
  background: var(--art) center 38% / cover no-repeat;
  filter: blur(80px) saturate(0.7);
  opacity: 0.15;
  transform: scale(1.2);
  pointer-events: none;
}

.intro {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.eyebrow {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 11px;
  font-weight: 650;
  letter-spacing: 0.16em;
  color: #acd0e7;
  white-space: nowrap;
}

.eyebrow .slash {
  margin: 0 -2px;
}

.number {
  font-size: 12px;
  letter-spacing: 0.08em;
  color: #e5f2f9;
}

.number span {
  color: #849cb0;
}

/* ---------- hero ---------- */

.hero {
  flex: 1;
  min-height: 0;
  display: grid;
  grid-template-columns: minmax(200px, 380px) minmax(220px, 1fr);
  align-items: center;
  gap: clamp(28px, 5cqi, 72px);
}

.cover-wrap {
  position: relative;
  width: 100%;
  max-width: 380px;
  justify-self: center;
}

.cover-shadow {
  position: absolute;
  inset: 8% -4% -9% 5%;
  background: radial-gradient(ellipse, #a58680 0%, #365d72 45%, transparent 73%);
  filter: blur(30px);
  opacity: 0.42;
}

.cover {
  position: relative;
  width: 100%;
  aspect-ratio: 1;
  overflow: hidden;
  border-radius: 10px;
  border: 1px solid rgba(255, 255, 255, 0.24);
  background: linear-gradient(145deg, #496278, #152d44 72%);
  box-shadow:
    0 25px 56px rgba(3, 11, 22, 0.5),
    inset 0 1px rgba(255, 255, 255, 0.22);
}

.cover-fallback {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: #cde6f3;
  opacity: 0.8;
  background:
    radial-gradient(circle at 75% 22%, rgba(162, 129, 142, 0.7), transparent 37%),
    linear-gradient(145deg, #496278, #152d44 72%);
}

.cover-wait {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  background: rgba(8, 20, 36, 0.35);
}

.cover-caption {
  margin: 16px 0 0;
  text-align: center;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.19em;
  color: #9aafbe;
  white-space: nowrap;
}

.track-info {
  min-width: 0;
  max-width: 440px;
}

.side-rule {
  display: block;
  width: 36px;
  height: 2px;
  margin-bottom: 30px;
  background: #9ccfe7;
}

.overline {
  display: flex;
  align-items: center;
  gap: 18px;
  margin: 0 0 16px;
  font-size: 13px;
  color: #bfd3e1;
}

.overline span {
  font-size: 11px;
  color: #819aae;
}

h1 {
  margin: 0 0 12px;
  font-size: clamp(30px, 4.6cqi, 56px);
  line-height: 1.18;
  font-weight: 680;
  letter-spacing: -0.04em;
  overflow-wrap: anywhere;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.byline {
  margin: 0;
  font-size: 17px;
  color: #d0e0ec;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.info-line {
  display: block;
  height: 1px;
  margin: 32px 0 24px;
  background: linear-gradient(90deg, rgba(196, 221, 238, 0.28), transparent);
}

.context {
  margin: 0 0 32px;
  font-size: 15px;
  line-height: 1.65;
  color: #c1d2df;
}

.metadata {
  display: flex;
  gap: 40px;
  margin: 0;
  min-width: 0;
}

.metadata div {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
}

.metadata-format {
  flex-shrink: 0;
}

.metadata dt {
  font-size: 12px;
  color: #8fa9bc;
  white-space: nowrap;
}

.metadata dd {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: #e2edf4;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.problem strong {
  font-size: 15px;
}

.problem p {
  margin: 6px 0 14px;
  font-size: 13px;
  color: #c1d2df;
}

.problem div {
  display: flex;
  gap: 10px;
}

/* ---------- dock ---------- */

.dock {
  flex: none;
  padding: 20px 26px 16px;
  border-radius: 22px;
  border: 1px solid rgba(203, 231, 251, 0.19);
  background: rgba(10, 24, 39, 0.72);
  box-shadow:
    inset 0 1px 0 rgba(232, 245, 254, 0.13),
    0 12px 45px rgba(2, 11, 22, 0.19);
  backdrop-filter: blur(24px);
  -webkit-backdrop-filter: blur(24px);
}

.seek-row {
  display: flex;
  align-items: center;
  gap: 16px;
}

.time {
  min-width: 38px;
  font-size: 12px;
  color: #c1d4e1;
}

.time.end {
  text-align: right;
}

.scrub {
  position: relative;
  flex: 1;
  min-width: 80px;
  height: 24px;
  display: flex;
  align-items: center;
  cursor: pointer;
  touch-action: none;
  outline: none;
}

.scrub[aria-disabled="true"] {
  cursor: default;
}

.scrub .track {
  position: absolute;
  left: 0;
  right: 0;
  height: 4px;
  border-radius: 8px;
  background: rgba(218, 234, 247, 0.24);
  overflow: hidden;
}

.scrub .track i {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--tone);
  transition: width 0.2s linear;
}

.scrub .knob {
  position: absolute;
  top: 50%;
  width: 13px;
  height: 13px;
  margin: -6.5px 0 0 -6.5px;
  border-radius: 50%;
  border: 3px solid #e8f7ff;
  background: #8bc8ef;
  box-shadow: 0 0 0 3px rgba(152, 211, 246, 0.13);
  transition:
    left 0.2s linear,
    transform var(--dur) var(--ease);
  pointer-events: none;
}

.scrubbing .track i,
.scrubbing .knob {
  transition: none;
}

.scrub:hover .knob,
.scrubbing .knob {
  transform: scale(1.2);
}

.scrub:focus-visible .knob {
  box-shadow: 0 0 0 3px rgba(152, 211, 246, 0.6);
}

.control-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding-top: 14px;
}

.transport {
  display: flex;
  align-items: center;
  gap: 18px;
}

.skip {
  width: 40px;
  height: 40px;
  min-height: 40px;
  padding: 0;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: #d9e7f0;
}

.skip:hover:not(:disabled) {
  background: rgba(209, 236, 252, 0.13);
}

.skip:disabled {
  opacity: 0.35;
}

.play,
.play:hover:not(:disabled) {
  width: 54px;
  height: 54px;
  min-height: 54px;
  padding: 0;
  border-radius: 50%;
  border: 1px solid rgba(255, 255, 255, 0.33);
  background: linear-gradient(145deg, #c2e7f8, #84bce2);
  color: #10324c;
  box-shadow:
    0 5px 20px rgba(113, 187, 234, 0.22),
    inset 0 1px rgba(255, 255, 255, 0.67);
}

.play:hover:not(:disabled) {
  filter: brightness(1.08);
}

.caption {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  font-size: 12px;
  color: #a8c5d7;
  white-space: nowrap;
}

.wave {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  height: 17px;
}

.wave i {
  width: 2px;
  height: 8px;
  border-radius: 2px;
  background: #9ad7fb;
}

.wave i:nth-child(2) {
  height: 16px;
}

.wave i:nth-child(3) {
  height: 11px;
}

.wave i:nth-child(4) {
  height: 6px;
}

.volume {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 150px;
}

.volume :deep(.rslider) {
  --knob: 13px;
}

/* ---------- queue ---------- */

.queue {
  display: flex;
  flex-direction: column;
  padding: 28px 18px 22px;
  border: 1px solid rgba(202, 230, 247, 0.19);
  background: linear-gradient(165deg, rgba(65, 91, 118, 0.36), rgba(15, 33, 52, 0.82) 46%, rgba(15, 32, 49, 0.86));
  box-shadow:
    inset 0 1px 0 rgba(247, 254, 255, 0.17),
    0 26px 65px rgba(3, 13, 28, 0.2);
  backdrop-filter: blur(25px);
  -webkit-backdrop-filter: blur(25px);
}

.queue-top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 0 9px;
}

.queue h2 {
  margin: 16px 0 0;
  font-size: 25px;
  font-weight: 650;
  letter-spacing: -0.03em;
}

.queue h2 sup {
  margin-left: 8px;
  font-size: 12px;
  font-weight: 500;
  letter-spacing: 0;
  color: #8fb4cc;
}

.mode {
  display: grid;
  place-items: center;
  width: 36px;
  height: 36px;
  margin-top: 22px;
  border-radius: 10px;
  color: #9dbdd2;
}

.mode:hover {
  background: rgba(209, 236, 252, 0.12);
}

.queue-source {
  margin: 12px 9px 20px;
  font-size: 13px;
  color: #a7bbcc;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.queue-divider {
  display: block;
  flex: none;
  height: 1px;
  margin: 0 9px 14px;
  background: linear-gradient(90deg, rgba(197, 223, 242, 0.2), rgba(197, 223, 242, 0.04));
}

.queue ol {
  flex: 1;
  min-height: 0;
  margin: 0;
  padding: 1px;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 5px;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;
}

.queue-track {
  justify-content: flex-start;
  gap: 11px;
  width: 100%;
  min-height: 70px;
  padding: 8px 9px;
  border: 1px solid transparent;
  border-radius: 14px;
  background: transparent;
  text-align: left;
}

.queue-track:hover:not(:disabled) {
  background: rgba(190, 225, 247, 0.09);
}

.queue-track.active,
.queue-track.active:hover {
  background: rgba(140, 196, 228, 0.13);
  border-color: rgba(166, 218, 248, 0.27);
}

.thumb {
  position: relative;
  flex: none;
  display: grid;
  place-items: center;
  width: 50px;
  height: 50px;
  overflow: hidden;
  border-radius: 8px;
  color: #d4e6f0;
  background: linear-gradient(125deg, #445776, #26384e);
}

.queue-copy {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.queue-copy strong {
  font-size: 14px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.queue-copy small {
  font-size: 11px;
  color: #9fb6c7;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.queue-tail {
  flex: none;
  font-size: 11px;
  color: #b3c9d9;
}

.queue-bottom {
  flex: none;
  padding: 16px 1px 0;
  font-size: 12px;
  color: #91acbe;
}

.queue-bottom .queue-divider {
  margin: 0 9px 14px;
}

.queue-bottom p,
.queue-bottom small {
  display: block;
  margin: 0 9px;
}

.queue-bottom p {
  margin-bottom: 5px;
  color: #c7d9e4;
}

/* ---------- smaller rooms ---------- */

@container music (max-width: 1100px) {
  .layout {
    grid-template-columns: minmax(0, 1fr) 290px;
    gap: 14px;
    padding: 0 18px 18px;
  }

  .topbar {
    padding: 0 22px;
  }

  .listening {
    padding: 22px 24px 22px;
  }

  .hero {
    grid-template-columns: minmax(180px, 1fr) minmax(180px, 1fr);
    gap: 24px;
  }

  .queue {
    padding: 24px 12px 18px;
  }

  .context {
    margin-bottom: 22px;
  }

  .info-line {
    margin: 22px 0;
  }

  .metadata {
    gap: 18px;
  }

  .volume {
    width: 120px;
  }
}

/* The 1100 × 760 window: a three-line title leaves the File format · Playback source
   row under the dock unless the rules around the text close up a little. */
@container music (max-height: 740px) {
  .side-rule {
    margin-bottom: 18px;
  }

  .info-line {
    margin: 18px 0;
  }

  .context {
    margin-bottom: 16px;
  }
}

/* A short room (the 960 × 640 window): the cover and the type shrink
   before anything goes. Heights are the page's, not the window's. */
@container music (max-height: 640px) {
  .topbar {
    min-height: 60px;
  }

  .listening {
    gap: 12px;
    padding-top: 18px;
    padding-bottom: 18px;
  }

  .hero {
    grid-template-columns: minmax(160px, 235px) minmax(190px, 1fr);
    gap: 22px;
  }

  .cover-wrap {
    max-width: 235px;
  }

  .cover-caption {
    margin-top: 10px;
  }

  .side-rule {
    margin-bottom: 16px;
  }

  .overline {
    margin-bottom: 8px;
  }

  h1 {
    font-size: 34px;
    margin-bottom: 6px;
    -webkit-line-clamp: 2;
  }

  .byline {
    font-size: 14px;
  }

  .info-line {
    margin: 16px 0;
  }

  .context {
    font-size: 13px;
    margin-bottom: 14px;
  }

  .dock {
    padding: 14px 20px 10px;
  }

  .control-row {
    padding-top: 6px;
  }

  .queue {
    padding-top: 20px;
    padding-bottom: 14px;
  }

  .queue h2 {
    margin-top: 8px;
    font-size: 22px;
  }

  .mode {
    margin-top: 12px;
  }

  .queue-source {
    margin-bottom: 12px;
  }

  .queue-track {
    min-height: 62px;
  }
}

@container music (max-height: 560px) {
  .cover-caption,
  .context,
  .info-line,
  .side-rule {
    display: none;
  }

  .hero {
    grid-template-columns: minmax(120px, 170px) minmax(190px, 1fr);
  }
}

/* Too narrow for the room beside the queue: the queue goes under it. */
@container music (max-width: 760px) {
  .layout {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: minmax(0, 1fr) minmax(200px, 38%);
  }

  .source-pill,
  .collapse span {
    display: none;
  }

  .caption {
    display: none;
  }
}

@media (prefers-reduced-transparency: reduce) {
  .dock,
  .queue {
    backdrop-filter: none;
    -webkit-backdrop-filter: none;
    background: #14283e;
  }
}

@media (prefers-contrast: more) {
  .queue,
  .listening,
  .dock {
    border-color: #d5f0ff;
  }

  .queue-copy small,
  .queue-source,
  .metadata dt {
    color: #d5e7f3;
  }
}

/* The popout keeps the track and transport together; only the queue grows. */
.music.windowed {
  border-radius: 0;
  border: 0;
}

.windowed .topbar {
  padding: 0 16px 0 96px;
  min-height: 52px;
  gap: 8px;
}

.windowed.win .topbar {
  padding-left: 16px;
}

.windowed .crumbs {
  gap: 10px;
}

.windowed .back,
.windowed .collapse {
  width: 32px;
  height: 32px;
  min-height: 32px;
  padding: 0;
  border: 0;
  border-radius: 10px;
  background: transparent;
}

.windowed .from,
.windowed .slash,
.windowed .source-pill,
.windowed .intro,
.windowed .overline,
.windowed .cover-caption,
.windowed .side-rule,
.windowed .info-line,
.windowed .context,
.windowed .metadata {
  display: none;
}

.windowed .layout {
  grid-template-columns: minmax(0, 1fr);
  grid-template-rows: minmax(0, max-content) minmax(120px, 1fr);
  padding: 0 12px 12px;
  gap: 10px;
}

.windowed .listening {
  justify-content: flex-start;
  overflow: auto;
  padding: 14px;
  gap: 12px;
  border-radius: 18px;
}

.windowed .hero {
  flex: none;
  min-height: min-content;
  grid-template-columns: 80px minmax(0, 1fr);
  gap: 14px;
  align-items: center;
}

.windowed .cover-wrap {
  max-width: 80px;
}

.windowed h1 {
  font-size: 20px;
  margin: 0 0 6px;
  -webkit-line-clamp: 2;
}

.windowed .byline {
  font-size: 13px;
}

.windowed .dock {
  margin: 0;
  padding: 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
  backdrop-filter: none;
  -webkit-backdrop-filter: none;
}

.windowed .seek-row {
  gap: 10px;
}

.windowed .control-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
  gap: 8px;
  padding-top: 4px;
}

.windowed .transport {
  grid-column: 2;
  grid-row: 1;
  gap: 10px;
}

.windowed .skip {
  width: 32px;
  height: 32px;
  min-height: 32px;
}

.windowed .play,
.windowed .play:hover:not(:disabled) {
  width: 48px;
  height: 48px;
  min-height: 48px;
}

.windowed .caption {
  display: flex;
  grid-column: 1;
  grid-row: 1;
  gap: 6px;
  font-size: 11px;
}

.windowed .caption .wave {
  display: none;
}

.windowed .volume {
  grid-column: 3;
  grid-row: 1;
  width: 104px;
  max-width: 100%;
  justify-self: end;
}

.windowed .queue {
  padding: 8px;
  border-radius: 18px;
}

.windowed .queue-top {
  flex: none;
  align-items: center;
  padding: 0 8px;
}

.windowed .queue h2 {
  font-size: 15px;
  margin: 0;
}

.windowed .queue .eyebrow,
.windowed .queue-source,
.windowed .queue-bottom {
  display: none;
}

.windowed .mode {
  margin-top: 0;
}

.windowed .queue .queue-divider {
  margin: 4px 8px 8px;
}

.windowed .queue-track {
  min-height: 54px;
  padding: 8px;
  border-radius: 10px;
}

.windowed .thumb {
  width: 36px;
  height: 36px;
}

.windowed .queue-copy {
  gap: 3px;
}

.windowed .queue-copy strong {
  font-size: 13px;
}

@container music (max-height: 500px) {
  .windowed .hero {
    grid-template-columns: 56px minmax(0, 1fr);
    gap: 12px;
  }

  .windowed .cover-wrap {
    max-width: 56px;
  }

  .windowed h1 {
    font-size: 18px;
    margin-bottom: 4px;
  }
}
</style>
