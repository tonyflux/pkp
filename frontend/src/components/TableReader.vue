<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from "vue";
import { errorMessage, loadReading, readBounded, saveReading, type ConsumptionEntry } from "../consumption";
import { intlTag, t } from "../i18n";
import { inspectWorkbook } from "../reader-table-archive";
import { columnLabel, TABLE_LIMITS, TableReadError, type TableCell, type TableEncoding, type TableFailure, type TableRows, type TableWorkerRequest, type TableWorkerResponse } from "../reader-table";

const props = defineProps<{ url: string; entry: ConsumptionEntry; identity: string; format: "csv" | "tsv" | "xlsx" }>();
const ROW = 36, COLUMN = 180, GUTTER = 54;
const viewport = ref<HTMLElement>();
const rows = shallowRef<TableRows>([]);
const sheets = ref<string[]>([]);
const sheet = ref(1);
const encoding = ref<TableEncoding>("utf-8");
const delimiter = ref(",");
const loading = ref(false);
const failure = shallowRef<unknown>();
const query = ref("");
const searchResult = ref<"" | "missing" | "found">("");
const selected = ref<{ row: number; column: number }>();
const gotoRow = ref(1);
const scroll = ref({ top: 0, left: 0, width: 800, height: 500 });
const key = computed(() => `${props.identity}.table`);
let bytes: Uint8Array | undefined;
let controller: AbortController | undefined;
let worker: Worker | undefined;
let rejectWorker: ((error: unknown) => void) | undefined;
let resize: ResizeObserver | undefined;
let loadVersion = 0, parseVersion = 0;
let saveTimer: ReturnType<typeof setTimeout> | undefined;
let savedPosition = { top: 0, left: 0 };
const columnCount = computed(() => rows.value.reduce((count, row) => Math.max(count, row.length), 0));
const firstColumn = computed(() => Math.max(0, Math.floor((scroll.value.left - GUTTER) / COLUMN) - 2));
const visibleColumns = computed(() => Array.from({ length: Math.max(0, Math.min(columnCount.value - firstColumn.value, Math.ceil(scroll.value.width / COLUMN) + 4, 40)) }, (_, i) => firstColumn.value + i));
const firstRow = computed(() => Math.max(1, Math.floor(scroll.value.top / ROW) - 3));
const visibleRows = computed(() => Array.from({ length: Math.max(0, Math.min(rows.value.length - firstRow.value, Math.ceil(scroll.value.height / ROW) + 6, 100)) }, (_, i) => firstRow.value + i));
const dateFormatter = computed(() => new Intl.DateTimeFormat(intlTag(), { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", timeZone: "UTC" }));
const numberFormatter = computed(() => new Intl.NumberFormat(intlTag(), { maximumSignificantDigits: 21 }));
function display(value: TableCell | undefined): string {
  if (value == null) return "";
  if (value instanceof Date) return Number.isNaN(value.getTime()) ? "" : dateFormatter.value.format(value);
  if (typeof value === "boolean") return value ? t("table|真") : t("table|假");
  if (typeof value === "number") return numberFormatter.value.format(value);
  return value;
}
const selectedText = computed(() => selected.value ? display(rows.value[selected.value.row]?.[selected.value.column]) : "");
const selectedAddress = computed(() => selected.value ? `${columnLabel(selected.value.column)}${selected.value.row + 1}` : "");
const errorText = computed(() => {
  const error = failure.value;
  if (!(error instanceof TableReadError)) return error ? errorMessage(error) : "";
  const reason: TableFailure = error.reason;
  if (reason === "limit") return t("表格超过安全读取上限：文本 16 MiB、工作簿 20 MiB、展开 32 MiB、10 万行、512 列或 100 万个网格单元；未截断显示。请用电子表格应用打开。");
  if (reason === "encoding") return t("无法按所选编码解码，请切换字符编码后重试。");
  if (reason === "csv") return t("分隔文本的引号格式不正确，请检查分隔符及成对引号。");
  return t("无法读取此工作簿，文件可能损坏、加密或包含不支持的结构。");
});
function persist() {
  saveReading(key.value, { encoding: encoding.value, delimiter: delimiter.value, sheet: sheet.value, top: scroll.value.top, left: scroll.value.left });
}
function onScroll() {
  if (!viewport.value) return;
  scroll.value = { ...scroll.value, top: viewport.value.scrollTop, left: viewport.value.scrollLeft };
  gotoRow.value = Math.min(rows.value.length, Math.max(1, Math.floor(scroll.value.top / ROW) + 1));
  clearTimeout(saveTimer);
  saveTimer = setTimeout(persist, 200);
}
function stopWorker() {
  const reject = rejectWorker;
  if (reject) reject(new DOMException("Aborted", "AbortError"));
  else worker?.terminate();
  worker = undefined; rejectWorker = undefined;
}
function runWorker(request: TableWorkerRequest): Promise<TableWorkerResponse> {
  stopWorker();
  return new Promise((resolve, reject) => {
    const instance = new Worker(new URL("../reader-table-worker.ts", import.meta.url), { type: "module" });
    worker = instance;
    const timeout = setTimeout(() => { finish(); reject(new TableReadError("limit")); }, 30_000);
    function finish() { clearTimeout(timeout); instance.terminate(); if (worker === instance) { worker = undefined; rejectWorker = undefined; } }
    rejectWorker = (error) => { finish(); reject(error); };
    instance.onmessage = (event: MessageEvent<TableWorkerResponse>) => { finish(); resolve(event.data); };
    instance.onerror = () => { finish(); reject(new TableReadError(request.kind === "text" ? "csv" : "workbook")); };
    try { instance.postMessage(request); }
    catch (error) { finish(); reject(error); }
  });
}
async function parse(restore = false) {
  if (!bytes) return;
  const version = ++parseVersion;
  loading.value = true; failure.value = undefined; rows.value = []; selected.value = undefined; searchResult.value = "";
  try {
    // Keep the bounded source bytes for encoding/delimiter changes and sheet selection.
    const request: TableWorkerRequest = props.format === "xlsx"
      ? { kind: "workbook", bytes: bytes.buffer as ArrayBuffer, sheet: sheet.value, names: !sheets.value.length }
      : { kind: "text", bytes: bytes.buffer as ArrayBuffer, delimiter: delimiter.value, encoding: encoding.value };
    const result = await runWorker(request);
    if (version !== parseVersion) return;
    if ("error" in result) throw new TableReadError(result.error);
    rows.value = result.rows;
    if (result.sheets) sheets.value = result.sheets;
    if (result.sheet) sheet.value = result.sheet;
    await nextTick();
    if (version !== parseVersion) return;
    viewport.value?.scrollTo(restore ? savedPosition : { top: 0, left: 0 });
    onScroll(); persist();
  } catch (error) {
    if (version === parseVersion && !(error instanceof DOMException && error.name === "AbortError")) failure.value = error;
  } finally { if (version === parseVersion) loading.value = false; }
}
async function load() {
  const version = ++loadVersion;
  ++parseVersion; controller?.abort(); stopWorker();
  controller = new AbortController();
  const signal = controller.signal;
  bytes = undefined; rows.value = []; sheets.value = []; loading.value = true; failure.value = undefined;
  try {
    const max = props.format === "xlsx" ? TABLE_LIMITS.workbookBytes : TABLE_LIMITS.textBytes;
    if (props.entry.size > max) throw new TableReadError("limit");
    const source = await readBounded(props.url, max, signal);
    if (props.format === "xlsx") await inspectWorkbook(source, signal);
    if (version !== loadVersion) return;
    bytes = source;
    await parse(true);
  } catch (error) {
    if (version === loadVersion && !signal.aborted) {
      failure.value = error;
      loading.value = false;
    }
  }
}
watch(() => [props.url, props.identity, props.format], () => {
  clearTimeout(saveTimer);
  const saved = loadReading<Record<string, unknown>>(key.value, {});
  encoding.value = ["utf-8", "gb18030", "big5", "utf-16le", "utf-16be"].includes(String(saved.encoding)) ? saved.encoding as TableEncoding : "utf-8";
  delimiter.value = [",", "\t", ";", "|"].includes(String(saved.delimiter)) ? String(saved.delimiter) : props.format === "tsv" ? "\t" : ",";
  sheet.value = typeof saved.sheet === "number" && Number.isInteger(saved.sheet) && saved.sheet >= 1 && saved.sheet <= TABLE_LIMITS.sheets ? saved.sheet : 1;
  savedPosition = { top: typeof saved.top === "number" && Number.isFinite(saved.top) ? Math.max(0, saved.top) : 0, left: typeof saved.left === "number" && Number.isFinite(saved.left) ? Math.max(0, saved.left) : 0 };
  query.value = "";
  void load();
}, { immediate: true });
function reparse() { void parse(); }
function selectCell(row: number, column: number, reveal = false) {
  selected.value = { row, column };
  if (!reveal || !viewport.value) return;
  const top = row * ROW, left = GUTTER + column * COLUMN;
  const el = viewport.value;
  if (row && (top < el.scrollTop + ROW || top + ROW > el.scrollTop + el.clientHeight)) el.scrollTop = Math.max(0, top - ROW);
  if (left < el.scrollLeft + GUTTER || left + COLUMN > el.scrollLeft + el.clientWidth) el.scrollLeft = Math.max(0, left - GUTTER);
  onScroll();
}
function navigateRow() {
  const row = Math.min(rows.value.length, Math.max(1, Math.trunc(Number(gotoRow.value) || 1)));
  gotoRow.value = row;
  selectCell(row - 1, selected.value?.column ?? 0, true);
  viewport.value?.focus();
}
function find(direction: 1 | -1) {
  const needle = query.value.toLocaleLowerCase();
  if (!needle || !columnCount.value) return;
  const columns = columnCount.value, total = rows.value.length * columns;
  const start = selected.value ? selected.value.row * columns + selected.value.column : direction > 0 ? -1 : 0;
  for (let step = 1; step <= total; step++) {
    const index = ((start + step * direction) % total + total) % total;
    const row = Math.floor(index / columns), column = index % columns;
    if (display(rows.value[row]?.[column]).toLocaleLowerCase().includes(needle)) {
      selectCell(row, column, true); searchResult.value = "found"; return;
    }
  }
  searchResult.value = "missing";
}
watch(query, () => { searchResult.value = ""; });
function onGridKey(event: KeyboardEvent) {
  const steps: Record<string, [number, number]> = { ArrowUp: [-1, 0], ArrowDown: [1, 0], ArrowLeft: [0, -1], ArrowRight: [0, 1], PageUp: [-Math.max(1, Math.floor(scroll.value.height / ROW) - 1), 0], PageDown: [Math.max(1, Math.floor(scroll.value.height / ROW) - 1), 0] };
  const step = steps[event.key];
  if (!step || !rows.value.length) return;
  event.preventDefault();
  selectCell(Math.min(rows.value.length - 1, Math.max(0, (selected.value?.row ?? 0) + step[0])), Math.min(columnCount.value - 1, Math.max(0, (selected.value?.column ?? 0) + step[1])), true);
}
onMounted(() => {
  resize = new ResizeObserver(() => {
    if (viewport.value) scroll.value = { ...scroll.value, width: viewport.value.clientWidth, height: viewport.value.clientHeight };
  });
  if (viewport.value) resize.observe(viewport.value);
});
onBeforeUnmount(() => {
  clearTimeout(saveTimer); persist(); ++loadVersion; ++parseVersion; controller?.abort(); stopWorker(); resize?.disconnect(); bytes = undefined; rows.value = [];
});
</script>

<template>
  <section class="table-reader" @keydown.stop>
    <div class="table-toolbar" role="toolbar" :aria-label="t('表格阅读工具')">
      <span class="readonly">{{ t('table|只读') }}</span>
      <label v-if="format === 'xlsx'">{{ t('table|工作表') }}
        <select v-model.number="sheet" :disabled="loading || !sheets.length" @change="reparse"><option v-for="(name, index) in sheets" :key="index" :value="index + 1">{{ name }}</option></select>
      </label>
      <template v-else>
        <label>{{ t('table|编码') }}<select v-model="encoding" :disabled="loading" @change="reparse"><option value="utf-8">UTF-8</option><option value="gb18030">GB18030 / GBK</option><option value="big5">Big5</option><option value="utf-16le">UTF-16 LE</option><option value="utf-16be">UTF-16 BE</option></select></label>
        <label>{{ t('table|分隔符') }}<select v-model="delimiter" :disabled="loading" @change="reparse"><option value=",">{{ t('table|逗号') }}</option><option value="&#9;">{{ t('table|制表符') }}</option><option value=";">{{ t('table|分号') }}</option><option value="|">{{ t('table|竖线') }}</option></select></label>
      </template>
      <form class="table-find" @submit.prevent="find(1)">
        <input v-model="query" type="search" :placeholder="t('在整个工作表中查找')" :aria-label="t('在整个工作表中查找')" :disabled="loading" @keydown.enter.shift.prevent="find(-1)" />
        <button type="button" :disabled="loading || !query" :title="t('table|上一个匹配')" :aria-label="t('table|上一个匹配')" @click="find(-1)">↑</button>
        <button type="submit" :disabled="loading || !query" :title="t('table|下一个匹配')" :aria-label="t('table|下一个匹配')">↓</button>
      </form>
      <form class="table-goto" @submit.prevent="navigateRow"><label>{{ t('table|行号') }}<input v-model.number="gotoRow" type="number" min="1" :max="rows.length || 1" :disabled="loading || !rows.length" /></label><button :disabled="loading || !rows.length">{{ t('table|前往') }}</button></form>
    </div>
    <div class="table-status" aria-live="polite">
      <span v-if="loading">{{ t('table|正在读取表格…') }}</span>
      <span v-else-if="!failure">{{ t('表格共 {rows} 行、{columns} 列', { rows: rows.length, columns: columnCount }) }}</span>
      <span v-if="searchResult === 'missing'">{{ t('table|没有匹配项') }}</span>
      <span v-else-if="searchResult === 'found'">{{ t('匹配位置：{cell}', { cell: selectedAddress }) }}</span>
    </div>
    <div v-if="failure" class="table-error" role="alert"><p>{{ errorText }}</p><button @click="load">{{ t('重试') }}</button></div>
    <div ref="viewport" class="table-viewport" role="grid" tabindex="0" :aria-label="entry.name" aria-readonly="true" :aria-rowcount="rows.length" :aria-colcount="columnCount" :aria-busy="loading" @scroll="onScroll" @keydown="onGridKey">
      <div class="table-canvas" :style="{ width: `${GUTTER + columnCount * COLUMN}px`, height: `${Math.max(1, rows.length) * ROW}px` }">
        <div v-if="rows.length" class="table-row table-header" role="row" aria-rowindex="1" :style="{ width: `${GUTTER + columnCount * COLUMN}px` }">
          <span class="row-number" role="rowheader">1</span>
          <div v-for="column in visibleColumns" :key="column" class="table-cell" :class="{ selected: selected?.row === 0 && selected?.column === column }" role="columnheader" :aria-colindex="column + 1" :aria-selected="selected?.row === 0 && selected?.column === column" :title="`${columnLabel(column)}1: ${display(rows[0]?.[column])}`" :style="{ left: `${GUTTER + column * COLUMN}px` }" @click="selectCell(0, column)">{{ display(rows[0]?.[column]) }}</div>
        </div>
        <div v-for="row in visibleRows" :key="row" class="table-row" role="row" :aria-rowindex="row + 1" :style="{ top: `${row * ROW}px`, width: `${GUTTER + columnCount * COLUMN}px` }">
          <span class="row-number" role="rowheader">{{ row + 1 }}</span>
          <div v-for="column in visibleColumns" :key="column" class="table-cell" :class="{ selected: selected?.row === row && selected?.column === column }" role="gridcell" :aria-colindex="column + 1" :aria-selected="selected?.row === row && selected?.column === column" :title="display(rows[row]?.[column])" :style="{ left: `${GUTTER + column * COLUMN}px` }" @click="selectCell(row, column)">{{ display(rows[row]?.[column]) }}</div>
        </div>
      </div>
      <p v-if="!loading && !failure && !rows.length" class="table-empty">{{ t('table|空表格') }}</p>
    </div>
    <div v-if="selected" class="table-value"><strong>{{ selectedAddress }}</strong><pre tabindex="0" :aria-label="t('table|所选单元格内容')">{{ selectedText }}</pre></div>
    <p v-if="format === 'xlsx'" class="table-note">{{ t('仅显示已保存的单元格值，不执行公式或宏；日期按工作簿值显示，部分自定义数字格式不受支持。') }}</p>
  </section>
</template>

<style scoped>
.table-reader {
  --surface: #17293e;
  --text: var(--fg, #edf4fa);
  --muted: var(--fg-dim, #a7bdcc);
  color-scheme: dark;
}
.table-viewport::-webkit-scrollbar-corner { background: var(--surface); }
.table-reader{height:100%;min-height:0;width:100%;display:flex;flex-direction:column;color:var(--text);background:var(--surface,transparent);font-size:13px;overflow:hidden}.table-toolbar{display:flex;align-items:center;gap:8px;flex-wrap:wrap;padding:10px 12px;border-bottom:1px solid var(--line, #8884)}.readonly{font-size:11px;border:1px solid var(--line,#8884);border-radius:5px;padding:4px 7px;white-space:nowrap}.table-toolbar label{display:flex;align-items:center;gap:5px;min-width:0}.table-toolbar select,.table-toolbar input,.table-toolbar button,.table-error button{font:inherit;color:inherit;background:var(--surface,#8881);border:1px solid var(--line,#8885);border-radius:5px;min-height:30px;padding:4px 7px}.table-toolbar select{max-width:190px;min-width:0}.table-toolbar button{cursor:pointer;min-width:30px}.table-toolbar button:disabled{cursor:default;opacity:.45}.table-toolbar input:focus-visible,.table-toolbar select:focus-visible,.table-toolbar button:focus-visible,.table-viewport:focus-visible,.table-value pre:focus-visible{outline:2px solid var(--accent,#527ade);outline-offset:-2px}.table-find{display:flex;gap:4px;flex:1;min-width:190px;max-width:420px}.table-find input{width:100%;min-width:100px}.table-goto{display:flex;gap:5px}.table-goto input{width:74px}.table-status{display:flex;gap:12px;flex-wrap:wrap;padding:6px 12px;min-height:28px;font-size:12px;color:var(--muted,#888)}.table-viewport{position:relative;overflow:auto;flex:1;min-height:0;overscroll-behavior:contain;outline:none}.table-canvas{position:relative;min-width:100%}.table-row{position:absolute;left:0;height:36px}.table-header{position:sticky;top:0;z-index:3;background:var(--surface,#f3f3f3);font-weight:600;box-shadow:0 1px 0 var(--line,#8885)}.table-cell{position:absolute;top:0;width:180px;height:36px;padding:9px 10px;border-right:1px solid var(--line,#8883);border-bottom:1px solid var(--line,#8883);white-space:pre;overflow:hidden;text-overflow:ellipsis;box-sizing:border-box;cursor:cell}.table-cell.selected{background:var(--accent-soft,#527ade22);box-shadow:inset 0 0 0 2px var(--accent,#527ade)}.row-number{position:sticky;left:0;z-index:2;display:block;width:54px;height:36px;box-sizing:border-box;text-align:right;padding:9px 8px;background:var(--surface,#eee);border-right:1px solid var(--line,#8885);border-bottom:1px solid var(--line,#8883);color:var(--muted,#888);font-variant-numeric:tabular-nums}.table-header .row-number{z-index:4}.table-error{padding:14px 18px;color:var(--danger,#bd4545)}.table-error p{white-space:pre-wrap;overflow-wrap:anywhere}.table-error+.table-viewport{display:none}.table-empty{position:absolute;inset:30px 12px auto;text-align:center;color:var(--muted,#888)}.table-value{display:flex;gap:12px;padding:8px 12px;border-top:1px solid var(--line,#8884);min-height:36px;max-height:140px;overflow:hidden}.table-value strong{font-size:11px;padding-top:3px}.table-value pre{margin:0;white-space:pre-wrap;overflow:auto;overflow-wrap:anywhere;min-width:0;flex:1;font:inherit;user-select:text}.table-note{margin:0;padding:5px 12px;font-size:11px;line-height:1.5;color:var(--muted,#888)}@media(max-width:600px){.table-toolbar{padding:7px;gap:6px}.table-toolbar select{max-width:145px}.table-find{order:2;max-width:none;flex-basis:100%}.table-goto{margin-left:auto}.table-toolbar label{font-size:12px}.table-note{max-height:44px;overflow:auto}}
</style>
