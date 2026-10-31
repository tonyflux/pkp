<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { t } from "../i18n";
import { errorMessage, loadReading, readBounded, saveReading, type ConsumptionEntry } from "../consumption";
import { cueTime, decodeTimed, formatCueTime, parseTimed, TIMED_MAX_BYTES, type TimedCue, type TimedEncoding, type TimedFormat } from "../reader-timed";

const props = defineProps<{ url: string; entry: ConsumptionEntry; identity: string; format: TimedFormat }>();
const cues = ref<TimedCue[]>([]);
const encoding = ref<TimedEncoding>("auto");
const query = ref("");
const selected = ref(-1);
const timeInput = ref("");
const error = ref("");
const loading = ref(false);
const list = ref<HTMLElement>();
const top = ref(0);
const height = ref(600);
const rowHeight = 112;
let bytes: Uint8Array | undefined;
let controller: AbortController | undefined;
let generation = 0;
let observer: ResizeObserver | undefined;
let restoredTop = 0;
const filtered = computed(() => {
  const term = query.value.trim().toLocaleLowerCase();
  return term ? cues.value.filter(cue => cue.text.toLocaleLowerCase().includes(term)) : cues.value;
});
const first = computed(() => Math.max(0, Math.floor(top.value / rowHeight) - 4));
const visible = computed(() => filtered.value.slice(first.value, first.value + Math.ceil(height.value / rowHeight) + 9));
const totalHeight = computed(() => filtered.value.length * rowHeight);
function remember() {
  saveReading(`${props.identity}.timed`, { encoding: encoding.value, top: top.value, selected: selected.value });
}
function parse() {
  if (!bytes) return;
  try {
    cues.value = parseTimed(decodeTimed(bytes, encoding.value), props.format);
    error.value = "";
  } catch (cause) {
    cues.value = [];
    error.value = cause instanceof RangeError ? t("timed|字幕条目超过安全上限") : errorMessage(cause);
  }
}
async function load() {
  const run = ++generation;
  controller?.abort();
  controller = new AbortController();
  bytes = undefined;
  cues.value = [];
  error.value = "";
  loading.value = true;
  query.value = "";
  const state = loadReading<{ encoding?: TimedEncoding; top?: number; selected?: number }>(`${props.identity}.timed`, {});
  encoding.value = ["auto", "utf-8", "gb18030", "big5", "utf-16le", "utf-16be"].includes(state.encoding ?? "") ? state.encoding! : "auto";
  selected.value = Number.isInteger(state.selected) ? state.selected! : -1;
  restoredTop = typeof state.top === "number" && Number.isFinite(state.top) ? Math.max(0, state.top) : 0;
  try {
    const content = await readBounded(props.url, TIMED_MAX_BYTES, controller.signal);
    if (run !== generation) return;
    bytes = content;
    parse();
  } catch (cause) { if (run === generation && !controller.signal.aborted) error.value = errorMessage(cause); }
  finally {
    if (run === generation) {
      loading.value = false;
      await nextTick();
      if (run !== generation) return;
      if (list.value) { list.value.scrollTop = restoredTop; top.value = list.value.scrollTop; }
    }
  }
}
function changeEncoding() {
  parse();
  if (list.value) list.value.scrollTop = 0;
  top.value = 0;
  remember();
}
function scroll() { top.value = list.value?.scrollTop ?? 0; remember(); }
function select(cue: TimedCue) {
  selected.value = cue.id;
  timeInput.value = formatCueTime(cue.start);
  remember();
}
function goTime() {
  const time = cueTime(timeInput.value);
  if (time === null || !filtered.value.length) return;
  let index = 0;
  for (let i = 0; i < filtered.value.length && filtered.value[i].start <= time; i++) index = i;
  select(filtered.value[index]);
  list.value?.scrollTo({ top: index * rowHeight });
}
function onKey(event: KeyboardEvent) {
  if (event.key === "Escape") return;
  event.stopPropagation();
  if ((event.target as HTMLElement).matches("input,select,textarea")) return;
  if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
  event.preventDefault();
  const current = filtered.value.findIndex(cue => cue.id === selected.value);
  const index = Math.max(0, Math.min(filtered.value.length - 1, current + (event.key === "ArrowDown" ? 1 : -1)));
  const cue = filtered.value[index];
  if (cue) { select(cue); list.value?.scrollTo({ top: index * rowHeight }); }
}
watch(query, async () => { await nextTick(); if (list.value) list.value.scrollTop = 0; top.value = 0; });
watch(() => [props.url, props.identity, props.format], load, { immediate: true });
watch(list, element => { observer?.disconnect(); if (element) { height.value = element.clientHeight; observer?.observe(element); } });
onMounted(() => {
  observer = new ResizeObserver(entries => { height.value = entries[0]?.contentRect.height ?? 600; });
  if (list.value) { height.value = list.value.clientHeight; observer.observe(list.value); }
});
onBeforeUnmount(() => { ++generation; controller?.abort(); bytes = undefined; observer?.disconnect(); });
</script>

<template>
  <section class="timed-reader" :aria-label="entry.name" @keydown="onKey">
    <div class="timed-toolbar">
      <label>{{ t("timed|文本编码") }}
        <select v-model="encoding" @change="changeEncoding">
          <option value="auto">{{ t("timed|自动（UTF-8 / GB18030）") }}</option>
          <option value="utf-8">UTF-8</option><option value="gb18030">GB18030</option><option value="big5">Big5</option>
          <option value="utf-16le">UTF-16 LE</option><option value="utf-16be">UTF-16 BE</option>
        </select>
      </label>
      <input v-model="query" type="search" :aria-label="t('timed|搜索字幕文本')" :placeholder="t('timed|搜索字幕文本')" />
      <form @submit.prevent="goTime">
        <input v-model="timeInput" class="timed-time" :aria-label="t('timed|定位时间')" :placeholder="t('timed|时:分:秒.毫秒')" />
        <button type="submit">{{ t("timed|定位时间") }}</button>
      </form>
      <span>{{ t("timed|{n} 条", { n: filtered.length }) }}</span>
    </div>
    <p class="timed-notice">{{ t("timed|只读时间轴，不控制播放器") }}</p>
    <div v-if="loading" class="timed-status" role="status">{{ t("正在加载…") }}</div>
    <div v-else-if="error" class="timed-status" role="alert"><p>{{ error }}</p><button @click="load">{{ t("重试") }}</button></div>
    <div v-else-if="!filtered.length" class="timed-status">{{ query ? t("timed|没有匹配的字幕") : t("timed|未找到有效的时间条目") }}</div>
    <div v-else ref="list" class="timed-list" role="list" tabindex="0" @scroll="scroll">
      <div :style="{ height: `${totalHeight}px`, position: 'relative' }">
        <article v-for="(cue, index) in visible" :key="cue.id" class="timed-cue" :class="{ selected: selected === cue.id }" role="listitem" :aria-posinset="first + index + 1" :aria-setsize="filtered.length" :style="{ top: `${(first + index) * rowHeight}px`, height: `${rowHeight}px` }">
          <button class="timed-stamp" :aria-pressed="selected === cue.id" @click="select(cue)"><time>{{ formatCueTime(cue.start) }}</time><span v-if="cue.end !== null"> — {{ formatCueTime(cue.end) }}</span></button>
          <p tabindex="0">{{ cue.text }}</p>
        </article>
      </div>
    </div>
  </section>
</template>

<style scoped>
.timed-reader{display:flex;flex-direction:column;height:100%;min-height:0;width:100%;color:var(--text,#eee)}
.timed-toolbar{display:flex;align-items:center;flex-wrap:wrap;gap:10px;padding:12px;border-bottom:1px solid var(--border,#ffffff20);font-size:12px}
.timed-toolbar label,.timed-toolbar form{display:flex;align-items:center;gap:6px}.timed-toolbar input,.timed-toolbar select,.timed-toolbar button,.timed-status button{font:inherit;color:inherit;background:var(--surface,#252525);border:1px solid var(--border,#ffffff30);border-radius:6px;padding:6px 8px;min-width:0}.timed-toolbar input{width:170px}.timed-toolbar .timed-time{width:150px}.timed-notice{font-size:11px;opacity:.65;margin:8px 14px}.timed-status{padding:32px;text-align:center;overflow:auto}.timed-list{flex:1;min-height:0;overflow:auto;overflow-anchor:none}.timed-cue{position:absolute;left:0;right:0;padding:10px 16px;box-sizing:border-box;border-bottom:1px solid var(--border,#ffffff15)}.timed-cue.selected{background:var(--accent-soft,#79aaff1c)}.timed-stamp{border:0;background:none;padding:0;color:var(--accent,#8ab4ff);font:12px ui-monospace,monospace;cursor:pointer;text-align:left}.timed-cue p{white-space:pre-wrap;overflow:auto;overflow-wrap:anywhere;margin:7px 0 0;max-height:65px;line-height:1.45;font-size:14px}.timed-reader :focus-visible{outline:2px solid var(--accent,#8ab4ff);outline-offset:2px}
</style>
