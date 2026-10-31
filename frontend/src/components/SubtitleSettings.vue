<script setup lang="ts">
// Subtitle & A/V Sync, the dock's popover body: Subtitle Style (size, height, colour —
// saved, the same for every video) and the shifts of the
// subtitles and of the sound against the picture, which hold for the video
// playing only. Keys stay here: the player's ←/→/↑/↓ would otherwise seek
// and change the volume while a slider has focus. Esc closes.
import { computed, onBeforeUnmount, reactive, useId, watch } from "vue";
import RangeSlider from "./RangeSlider.vue";
import { player, playerCommands } from "../store";
import type { SubtitleStyle } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import { t, N_ } from "../i18n";

// kind: the subtitles showing are an ASS track or pictures (PGS, VobSub, …),
// which the style reaches only in part; the panel says how. subtitles: some
// are drawn ("on"), this video has some but they are off, or it has none —
// then nothing on screen answers the controls, and the panel says why.
const props = defineProps<{ kind?: "ass" | "picture"; subtitles: "on" | "off" | "none" }>();
const emit = defineEmits<{ close: [] }>();

const id = useId();
const COLORS = [
  { value: "#FFFFFF", label: N_("白色") },
  { value: "#FFD60A", label: N_("黄色") },
  { value: "#7CE38B", label: N_("绿色") },
  { value: "#64C8FF", label: N_("蓝色") },
  { value: "#FF8FB8", label: N_("粉色") },
];
const DEFAULT_STYLE: SubtitleStyle = { scale: 1, raise: 0, color: "#FFFFFF" };
const MAX_SHIFT = 600;
const SHIFTS = ["subtitle", "audio"] as const;

// Drafts: a drag or a run of clicks moves them at once, and the service gets
// the latest value, one call at a time. The state echoes up to 200ms late,
// so while values are going out, and a little after, it may not pull the
// drafts back; once things are quiet the drafts take what the state says
// (a refused value, a new video's shifts back at 0).
const style = reactive<SubtitleStyle>({ ...player.state.subtitleStyle });
const shifts = reactive({ subtitle: player.state.subtitleDelay, audio: player.state.audioDelay });
const QUIET_MS = 600;
let quietUntil = 0;
let settleTimer: number | undefined;

function sync() {
  if (Date.now() < quietUntil) return;
  Object.assign(style, player.state.subtitleStyle);
  shifts.subtitle = player.state.subtitleDelay;
  shifts.audio = player.state.audioDelay;
}

function quiet() {
  quietUntil = Date.now() + QUIET_MS;
  window.clearTimeout(settleTimer);
  settleTimer = window.setTimeout(sync, QUIET_MS + 20);
}

function latest<T>(send: (value: T) => Promise<unknown>) {
  let busy = false;
  let next: { value: T } | null = null;
  return (value: T) => {
    next = { value };
    quiet();
    if (busy) return;
    busy = true;
    void (async () => {
      while (next) {
        const { value: v } = next;
        next = null;
        await send(v);
      }
      busy = false;
      quiet();
    })();
  };
}

const sendStyle = latest((s: SubtitleStyle) => playerCommands.subtitleStyle(s));
const sendSubtitle = latest((s: number) => playerCommands.subtitleDelay(s));
const sendAudio = latest((s: number) => playerCommands.audioDelay(s));

watch(
  () => [player.state.subtitleStyle, player.state.subtitleDelay, player.state.audioDelay],
  sync,
  { deep: true },
);
onBeforeUnmount(() => window.clearTimeout(settleTimer));

function setStyle(change: Partial<SubtitleStyle>) {
  Object.assign(style, change);
  sendStyle({ ...style });
}

type Shift = "subtitle" | "audio";

function setShift(kind: Shift, seconds: number) {
  const value = Math.round(Math.min(MAX_SHIFT, Math.max(-MAX_SHIFT, seconds)) * 100) / 100;
  if (value === shifts[kind]) return;
  shifts[kind] = value;
  (kind === "subtitle" ? sendSubtitle : sendAudio)(value);
}

function typedShift(kind: Shift, event: Event) {
  const input = event.target as HTMLInputElement;
  const seconds = Number(input.value.trim().replace("−", "-").replace(/秒|s$/i, ""));
  if (Number.isFinite(seconds)) setShift(kind, seconds);
  input.value = signed(shifts[kind]);
}

/** signed is "+0.5", "-1.25" or "0.0". */
function signed(seconds: number) {
  const abs = Math.abs(seconds);
  const text = Number.isInteger(Math.round(abs * 100) / 10) ? abs.toFixed(1) : abs.toFixed(2);
  return seconds > 0 ? `+${text}` : seconds < 0 ? `-${text}` : text;
}

// Subtitle Delay waits for subtitles on screen to be set against.
const locked = (kind: Shift) => kind === "subtitle" && props.subtitles !== "on";

function shiftHint(kind: Shift) {
  if (locked(kind)) return props.subtitles === "none" ? t("这个视频没有字幕") : t("打开字幕后可调");
  const v = shifts[kind];
  const n = Math.abs(v);
  if (kind === "subtitle") {
    if (v === 0) return t("字幕早了加，晚了减");
    return v > 0 ? t("字幕推迟 {n} 秒", { n }) : t("字幕提前 {n} 秒", { n });
  }
  if (v === 0) return t("声音早了加，晚了减");
  return v > 0 ? t("声音推迟 {n} 秒", { n }) : t("声音提前 {n} 秒", { n });
}

const heightLabel = computed(() => (style.raise === 0 ? t("底部") : t("上移 {n}%", { n: style.raise })));
const isDefault = computed(
  () =>
    style.scale === DEFAULT_STYLE.scale &&
    style.raise === DEFAULT_STYLE.raise &&
    style.color === DEFAULT_STYLE.color &&
    shifts.subtitle === 0 &&
    shifts.audio === 0,
);

function reset() {
  if (style.scale !== DEFAULT_STYLE.scale || style.raise !== DEFAULT_STYLE.raise || style.color !== DEFAULT_STYLE.color) {
    setStyle({ ...DEFAULT_STYLE });
  }
  setShift("subtitle", 0);
  setShift("audio", 0);
}

function onKey(event: KeyboardEvent) {
  if (event.key === "Escape") {
    event.preventDefault();
    emit("close");
  }
  event.stopPropagation();
}
</script>

<template>
  <div class="subtitle-settings" @keydown="onKey">
    <header>
      <strong :id="`${id}-title`">{{ t("字幕与音画同步") }}</strong>
      <button type="button" class="reset" :disabled="isDefault" @click="reset">{{ t("恢复默认") }}</button>
    </header>

    <section :aria-labelledby="`${id}-style`">
      <h5 :id="`${id}-style`">{{ t("字幕样式") }} <small>{{ t("所有视频") }}</small></h5>
      <p v-if="subtitles !== 'on'" class="note idle">
        {{
          subtitles === "none"
            ? t("这个视频没有字幕。调整会保存下来，在有字幕的视频上生效。")
            : t("字幕已关闭。调整会保存下来，打开字幕后生效。")
        }}
      </p>
      <div class="slider">
        <span>{{ t("大小") }}</span>
        <b>{{ Math.round(style.scale * 100) }}%</b>
        <RangeSlider
          class="bar"
          :label="t('字幕大小')"
          :min="50"
          :max="250"
          :step="1"
          :key-step="10"
          :model-value="Math.round(style.scale * 100)"
          :aria-valuetext="`${Math.round(style.scale * 100)}%`"
          @update:model-value="(v) => setStyle({ scale: v / 100 })"
        />
      </div>
      <div class="slider">
        <span>{{ t("subtitle|位置") }}</span>
        <b>{{ heightLabel }}</b>
        <RangeSlider
          class="bar"
          :label="t('字幕位置，离底部的高度')"
          :key-step="1"
          :model-value="style.raise"
          :aria-valuetext="heightLabel"
          @update:model-value="(v) => setStyle({ raise: v })"
        />
      </div>
      <div class="colors" role="radiogroup" :aria-label="t('字幕颜色')">
        <span>{{ t("颜色") }}</span>
        <button
          v-for="c in COLORS"
          :key="c.value"
          type="button"
          role="radio"
          class="swatch"
          :class="{ on: style.color === c.value }"
          :style="{ '--swatch': c.value }"
          :aria-checked="style.color === c.value"
          :aria-label="t(c.label)"
          :title="t(c.label)"
          @click="setStyle({ color: c.value })"
        />
      </div>
      <p v-if="kind === 'ass'" class="note">
        {{ t("这条是 ASS 特效字幕：颜色以字幕文件为准；位置只挪底部的普通对白，定位过的特效和顶部、居中的行不动。") }}
      </p>
      <p v-else-if="kind === 'picture'" class="note">
        {{ t("这条是图形字幕（蓝光 PGS、DVD 等）：颜色改不了；位置只挪画面下半部分的字幕。") }}
      </p>
    </section>

    <section :aria-labelledby="`${id}-sync`">
      <h5 :id="`${id}-sync`">{{ t("音画同步") }} <small>{{ t("仅当前视频") }}</small></h5>
      <div v-for="kind in SHIFTS" :key="kind" class="shift">
        <div class="shift-text">
          <span>{{ kind === "subtitle" ? t("字幕延迟") : t("音频延迟") }}</span>
          <small>{{ shiftHint(kind) }}</small>
        </div>
        <div class="stepper" :class="{ off: locked(kind) }">
          <button
            type="button"
            :disabled="locked(kind)"
            :aria-label="kind === 'subtitle' ? t('字幕提前 0.1 秒') : t('声音提前 0.1 秒')"
            :title="t('提前 0.1 秒')"
            @click="setShift(kind, shifts[kind] - 0.1)"
          >
            −
          </button>
          <input
            type="text"
            inputmode="decimal"
            :disabled="locked(kind)"
            :value="signed(shifts[kind])"
            :aria-label="kind === 'subtitle' ? t('字幕延迟（秒）') : t('音频延迟（秒）')"
            @change="typedShift(kind, $event)"
            @keydown.enter="($event.target as HTMLInputElement).blur()"
          />
          <button
            type="button"
            :disabled="locked(kind)"
            :aria-label="kind === 'subtitle' ? t('字幕推迟 0.1 秒') : t('声音推迟 0.1 秒')"
            :title="t('推迟 0.1 秒')"
            @click="setShift(kind, shifts[kind] + 0.1)"
          >
            +
          </button>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
/* Fixed dark glass in both themes, as Quality's: it sits on the native picture. */
.subtitle-settings {
  color: #edf8ff;
  font-size: 13px;
}

header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 2px 3px 10px;
  border-bottom: 1px solid rgba(202, 233, 255, 0.18);
}

header strong {
  font-size: 16px;
  font-weight: 600;
  text-wrap: balance;
}

.reset {
  flex: none;
  min-height: 28px;
  padding: 0 10px;
  border-radius: 9px;
  border: 1px solid rgba(255, 255, 255, 0.16);
  background: rgba(255, 255, 255, 0.07);
  color: #d6e9fb;
  font-size: 12px;
  white-space: nowrap;
}

.reset:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.14);
}

.reset:disabled {
  opacity: 0.45;
}

section {
  padding: 10px 3px 4px;
}

section + section {
  margin-top: 6px;
  border-top: 1px solid rgba(202, 233, 255, 0.18);
}

h5 {
  margin: 0 0 6px;
  font-size: 12px;
  font-weight: 600;
  color: #a9c9df;
}

h5 small {
  margin-left: 6px;
  font-weight: 400;
  color: #85a6c0;
}

.slider {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  row-gap: 2px;
  padding: 4px 0;
}

.slider b,
.shift-text small {
  font-weight: 500;
  color: #a9c9df;
}

.slider .bar {
  grid-column: 1 / -1;
  margin: 2px -7px;
}

.colors {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 0 4px;
}

.colors span {
  flex: 1;
}

.swatch {
  width: 24px;
  height: 24px;
  min-height: 24px;
  padding: 0;
  border-radius: 50%;
  border: 2px solid rgba(16, 31, 61, 0.9);
  background: var(--swatch);
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.28);
}

.swatch.on {
  box-shadow: 0 0 0 2px #c8f3ff;
}

.note {
  margin: 4px 0 2px;
  font-size: 12px;
  color: #a9c9df;
}

/* Why the controls change nothing on screen, read before them. */
.note.idle {
  margin: 0 0 6px;
  padding: 7px 10px;
  border-radius: 9px;
  background: rgba(143, 211, 255, 0.12);
  color: #cfe6f7;
}

.shift {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 5px 0;
}

.shift-text {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  gap: 1px;
}

.shift-text small {
  font-size: 11px;
  text-wrap: balance;
}

.stepper {
  flex: none;
  display: flex;
  align-items: center;
  border: 1px solid rgba(255, 255, 255, 0.16);
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.07);
}

.stepper.off {
  opacity: 0.45;
}

/* .off dims the whole stepper once. */
.stepper button:disabled {
  opacity: 1;
  cursor: default;
}

.stepper button {
  width: 30px;
  min-height: 30px;
  padding: 0;
  border: 0;
  background: transparent;
  color: #e9f2fc;
  font-size: 16px;
}

.stepper button:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.12);
}

.stepper button:first-child {
  border-radius: 9px 0 0 9px;
}

.stepper button:last-child {
  border-radius: 0 9px 9px 0;
}

.stepper input {
  width: 52px;
  min-height: 30px;
  padding: 0;
  border: 0;
  border-inline: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 0;
  background: transparent;
  color: #edf8ff;
  font: inherit;
  font-variant-numeric: tabular-nums;
  text-align: center;
  box-shadow: none;
}
</style>
