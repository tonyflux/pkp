<script setup lang="ts">
// Local search: the file page while the search box holds
// conditions. Every row comes from this computer's index (SearchIndex); no
// model is asked and nothing is sent. Chips edit the box itself, so the
// toolbar, the chips and the results never disagree. Ask AI is the one way
// out to a model, and it only routes to the assistant, which asks first.
import { computed } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import SaveSearch from "./SaveSearch.vue";
import SearchFilters from "./SearchFilters.vue";
import { bytes, clock, day } from "../format";
import { extList, removeChip } from "../searchquery";
import { N_, t } from "../i18n";
import {
  askAssistant,
  currentId,
  currentSavedSearch,
  indexReady,
  clearLocalSearchRange,
  localSearchSelectionCount,
  localSearch,
  localSearchPicked,
  openViewer,
  organizeEntries,
  play,
  revealFile,
  runLocalSearch,
  searchParse,
  toggleLocalPick,
  ui,
  viewFile,
} from "../store";
import type { AISearchHit } from "../../bindings/github.com/tonyflux/pkp/internal/services";

const parsed = computed(() => searchParse.value);
// A result of another account or of an older box is not shown as this one's.
const result = computed(() => (localSearch.account === currentId.value ? localSearch.result : null));
const hits = computed(() => result.value?.hits ?? []);
const aggregate = computed(() => result.value?.aggregate ?? null);
const stale = computed(() => localSearch.text !== ui.query);

const CATEGORY_NOUN: Record<string, string> = {
  "": N_("noun|文件"),
  video: N_("noun|视频"),
  audio: N_("noun|音频"),
  image: N_("noun|图片"),
  document: N_("noun|文档"),
  other: N_("其他文件"),
};

const PLAYED_ADJECTIVE: Record<string, string> = { unfinished: N_("没看完的"), finished: N_("看过的"), any: N_("播放过的") };

// 「Find videos not finished watching.」: what the conditions mean as a sentence; the chips
// carry the rest (size, dates, folder).
const title = computed(() => {
  const q = parsed.value.query;
  if (q.aggregate) return q.aggregate.by === "folder" ? t("按文件夹统计大小。") : q.aggregate.by === "ext" ? t("按扩展名统计大小。") : t("按类型统计大小。");
  const played = q.played ? t(PLAYED_ADJECTIVE[q.played]) : "";
  const starred = q.starred === "yes" ? t("adj|星标") : "";
  const words = parsed.value.keywords.length ? t("「{w}」", { w: parsed.value.keywords.join(" ") }) : "";
  const noun =
    q.kind === "folder"
      ? t("noun|文件夹")
      : q.ext.length
        ? t("{ext} 文件", { ext: extList(q.ext).toUpperCase() })
        : t(CATEGORY_NOUN[q.category || (!parsed.value.filtered && ui.category !== "all" ? ui.category : "")] ?? CATEGORY_NOUN[""]);
  return t("找{played}{starred}{words}{noun}。", { played, starred, words, noun });
});

const scopeLabel = computed(() => result.value?.under || parsed.value.query.under || (localSearch.range?.account === currentId.value && localSearch.range.folderId ? t("指定文件夹范围") : t("全部文件")));

const SORT_LABEL: Record<string, string> = {
  "": N_("修改时间"),
  modified: N_("修改时间"),
  created: N_("保存时间"),
  size: N_("大小"),
  name: N_("名称"),
  played: N_("上次播放时间"),
};
const sortNote = computed(() => {
  const q = parsed.value.query;
  const field = t(SORT_LABEL[q.sort] ?? SORT_LABEL[""]);
  return `${q.asc ? t("按{field}升序", { field }) : t("按{field}排序", { field })} · ${t("本地索引")}`;
});

const countText = computed(() => {
  const r = result.value;
  if (!r) return "";
  if (aggregate.value) return t("{n} 个文件", { n: r.total });
  return r.total > hits.value.length ? t("{n} 项结果 · 显示前 {m} 项", { n: r.total, m: hits.value.length }) : t("{n} 项结果", { n: r.total });
});

const aggregateMax = computed(() => Math.max(1, ...(aggregate.value ?? []).map((r) => (parsed.value.query.aggregate?.measure === "count" ? r.count : r.size))));

function barWidth(row: { size: number; count: number }): string {
  const v = parsed.value.query.aggregate?.measure === "count" ? row.count : row.size;
  return `${Math.max(2, (v / aggregateMax.value) * 100)}%`;
}

const ICON: Record<string, string> = { video: "film", image: "image", audio: "audio", document: "document", archive: "archive" };

// Where it lives gives way to a long path; how far it played or how big it
// is stays whole after it.
function meta(hit: AISearchHit): { where: string; extra: string } {
  const where = hit.path || t("全部文件");
  if (hit.position > 0 && !hit.folder) return { where, extra: t("播放至 {time}", { time: clock(hit.position) }) };
  return { where, extra: hit.folder ? day(hit.modifiedTime) : bytes(hit.size, "0 B", 1) };
}

function openLabel(hit: AISearchHit): string {
  if (hit.folder) return t("打开");
  if (hit.playable) return t("播放");
  return hit.preview ? t("打开") : "";
}

function open(hit: AISearchHit) {
  if (hit.folder) void viewFile({ account: currentId.value, fileId: hit.id });
  else if (hit.playable) void play(hit, hits.value.filter((h) => h.playable));
  else if (hit.preview) void openViewer(hit, hits.value.filter((h) => h.preview));
}

const picked = computed(() => new Set(localSearch.picked));
const allPicked = computed(() => hits.value.length > 0 && hits.value.every((h) => picked.value.has(h.id)));

function pickAll() {
  localSearch.picked = allPicked.value ? [] : hits.value.map((h) => h.id);
}

/* ---------- chips ---------- */


function clearConditions() {
  // The words stay: Clear conditions takes the page back to a plain search for them.
  ui.query = parsed.value.keywords.join(" ");
}
</script>

<template>
  <div class="files-search" aria-labelledby="files-search-title">
    <div class="fs-inner">
      <span class="fs-eyebrow">{{ currentSavedSearch ? t("已保存历史") : t("全部文件") }} <span aria-hidden="true">/</span> {{ t("本地搜索") }}</span>
      <h1 id="files-search-title">{{ currentSavedSearch?.name ?? title }}</h1>
      <p class="fs-lede">{{ t("筛选直接在本地索引完成，不需要模型，也不发送文件名。") }}</p>

      <div class="fs-chips" role="group" :aria-label="t('搜索条件')">
        <span class="fs-chip scope"><Icon name="folder" :size="14" />{{ scopeLabel }}</span>
        <span v-if="localSearchSelectionCount !== undefined" class="fs-chip">
          {{ t("已选 {n} 项", { n: localSearchSelectionCount }) }}
          <button class="fs-chip-remove" type="button" :aria-label="t('移除所选项范围')" @click="clearLocalSearchRange"><Icon name="close" :size="12" /></button>
        </span>
        <span v-for="chip in parsed.chips" :key="chip.key" class="fs-chip">
          {{ chip.label }}
          <button
            class="fs-chip-remove"
            :aria-label="t('移除条件：{label}', { label: chip.label })"
            :title="t('移除「{label}」', { label: chip.label })"
            @click="ui.query = removeChip(ui.query, chip)"
          >
            <Icon name="close" :size="12" />
          </button>
        </span>
        <!-- Add condition and Clear conditions wrap as one: neither is left alone on a row. -->
        <div class="fs-tools">
          <SearchFilters />
          <button v-if="parsed.chips.length" class="fs-clear" @click="clearConditions">{{ t("清除条件") }}</button>
          <SaveSearch />
        </div>
      </div>

      <div class="fs-summary">
        <strong>
          <Orbit v-if="localSearch.loading" :size="14" :label="t('正在筛选')" />
          {{ countText || (localSearch.loading ? t("正在筛选…") : "") }}
        </strong>
        <span v-if="result && !aggregate">{{ sortNote }}</span>
      </div>

      <p v-if="localSearch.error" class="fs-error" role="alert">
        {{ t("本地筛选没有完成：{error}", { error: localSearch.error }) }}
        <button class="quiet" @click="runLocalSearch()">{{ t("重试") }}</button>
      </p>
      <p v-else-if="!indexReady" class="fs-empty" role="status">{{ t("本地索引尚未就绪，搜索会在索引就绪后运行。") }}</p>
      <p v-else-if="result?.underMissing" class="fs-empty">{{ t("没有找到名为「{name}」的文件夹。", { name: parsed.query.under }) }}</p>

      <div v-else-if="aggregate" class="fs-table" :class="{ dim: stale }" role="table" :aria-label="t('统计结果')">
        <div v-for="row in aggregate" :key="row.key" class="fs-agg-row" role="row">
          <span class="fs-agg-label" role="cell" :title="row.label">{{ row.label || t("全部文件") }}</span>
          <span class="fs-agg-bar" role="cell" aria-hidden="true"><i :style="{ width: barWidth(row) }"></i></span>
          <span class="fs-agg-count" role="cell">{{ t("{n} 个", { n: row.count }) }}</span>
          <span class="fs-agg-size" role="cell">{{ bytes(row.size, "0 B", 1) }}</span>
        </div>
        <p v-if="!aggregate.length" class="fs-empty">{{ t("没有符合条件的文件。") }}</p>
      </div>

      <template v-else-if="result">
        <p v-if="!hits.length" class="fs-empty">{{ t("没有符合条件的文件。换个条件试试，或者清除条件。") }}</p>
        <div v-else class="fs-list" :class="{ dim: stale }">
          <div class="fs-list-head">
            <label class="fs-pick">
              <input type="checkbox" :checked="allPicked" :aria-label="t('全选本页结果')" :title="t('仅选择当前列表中已加载且符合筛选条件的项目，不包含尚未加载的结果。')" @change="pickAll" />
              <span>{{ localSearch.picked.length ? t("已选 {n} 项", { n: localSearch.picked.length }) : t("全选本页结果") }}</span>
            </label>
            <button v-if="localSearch.picked.length" class="fs-organize" @click="organizeEntries(localSearchPicked)">
              <Icon name="spark" :size="15" />{{ t("整理这些") }}
            </button>
          </div>
          <div v-for="hit in hits" :key="hit.id" class="fs-row" :class="{ picked: picked.has(hit.id) }">
            <input
              type="checkbox"
              class="fs-row-pick"
              :checked="picked.has(hit.id)"
              :aria-label="t('选择 {name}', { name: hit.name })"
              @change="toggleLocalPick(hit.id)"
            />
            <span class="fs-glyph" aria-hidden="true"><Icon :name="hit.folder ? 'folder' : (ICON[hit.category] ?? 'file')" :size="17" /></span>
            <span class="fs-words">
              <strong :title="hit.name">{{ hit.name }}</strong>
              <small class="fs-meta" :title="`${meta(hit).where} · ${meta(hit).extra}`">
                <span class="fs-where">{{ meta(hit).where }}</span><span class="fs-extra">&nbsp;· {{ meta(hit).extra }}</span>
              </small>
            </span>
            <span class="fs-actions">
              <button class="fs-act" @click="revealFile({ account: currentId, fileId: hit.id })">{{ t("定位") }}</button>
              <button v-if="openLabel(hit)" class="fs-act" @click="open(hit)">{{ openLabel(hit) }}</button>
            </span>
          </div>
        </div>
      </template>

      <div class="fs-help">
        <Icon name="shield" :size="16" />
        <span>{{ t("这只是本地筛选。想进一步按意思找，可以选择「问 AI」，发送前先确认数据范围。") }}</span>
        <button class="fs-ask" @click="askAssistant(ui.query.trim())"><Icon name="spark" :size="14" />{{ t("问 AI") }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.files-search {
  flex: 1;
  min-height: 0;
  margin: -26px -24px 0;
  padding: 48px 24px 40px;
  overflow-y: auto;
}

.fs-inner {
  max-width: 870px;
  margin: 0 auto;
}

.fs-eyebrow {
  display: block;
  color: var(--accent);
  font-size: 12px;
  font-weight: 650;
  letter-spacing: 0.02em;
}

h1 {
  margin: 10px 0 8px;
  font-size: 26px;
  line-height: 1.25;
  font-weight: 700;
  overflow-wrap: anywhere;
}

.fs-lede {
  margin: 0;
  color: var(--fg-dim);
  font-size: 13px;
  text-wrap: pretty;
}

.fs-chips {
  position: relative;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin: 22px 0 18px;
}

.fs-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 32px;
  padding: 0 6px 0 12px;
  border-radius: 9px;
  background: var(--accent-soft);
  font-size: 12px;
  font-weight: 600;
}

.fs-chip.scope {
  padding-right: 12px;
  background: transparent;
  border: 1px solid var(--line-strong);
}

.fs-chip-remove {
  min-height: 22px;
  width: 22px;
  padding: 0;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--fg-dim);
}

.fs-chip-remove:hover:not(:disabled) {
  background: var(--accent-soft);
  color: var(--fg);
}

.fs-tools {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.fs-clear {
  min-height: 32px;
  padding: 0 10px;
  border: 0;
  border-radius: 9px;
  background: transparent;
  color: var(--accent);
  font-size: 12px;
  font-weight: 600;
}

.fs-clear:hover:not(:disabled) {
  background: var(--accent-soft);
}


.fs-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  min-height: 24px;
  margin-bottom: 10px;
}

.fs-summary strong {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
}

.fs-summary span {
  color: var(--fg-dim);
  font-size: 12px;
  white-space: nowrap;
}

.fs-list,
.fs-table {
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-card);
  overflow: hidden;
  transition: opacity var(--dur) var(--ease);
}

.dim {
  opacity: 0.6;
}

.fs-list-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 44px;
  padding: 0 14px 0 20px;
  border-bottom: 1px solid var(--line);
}

.fs-pick {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  color: var(--fg-dim);
  font-size: 12px;
}

.fs-organize {
  min-height: 30px;
  padding: 0 12px;
  border-color: color-mix(in srgb, var(--accent) 40%, transparent);
  color: var(--accent);
  font-size: 12px;
  font-weight: 650;
}

/* One column set for every row (subgrid): Locate and Play/Open line up down
   the list whatever the words' lengths in the language. */
.fs-list {
  display: grid;
  grid-template-columns: auto auto minmax(0, 1fr) auto auto;
}

.fs-list-head {
  grid-column: 1 / -1;
}

.fs-row {
  grid-column: 1 / -1;
  display: grid;
  grid-template-columns: subgrid;
  align-items: center;
  column-gap: 0;
  min-height: 72px;
  padding: 10px 14px 10px 20px;
}

.fs-glyph {
  margin-inline: 14px;
}

.fs-row + .fs-row {
  border-top: 1px solid var(--line);
}

.fs-row.picked {
  background: var(--accent-soft);
}

input[type="checkbox"] {
  width: 15px;
  height: 15px;
  margin: 0;
  padding: 0;
  accent-color: var(--accent);
}

.fs-glyph {
  width: 36px;
  height: 36px;
  display: grid;
  place-items: center;
  border-radius: 10px;
  background: var(--accent-soft);
  color: var(--accent);
}

.fs-words {
  min-width: 0;
  margin-right: 14px;
}

.fs-words strong {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.fs-meta {
  display: flex;
  min-width: 0;
  white-space: nowrap;
}

.fs-where {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

.fs-extra {
  flex: none;
}

.fs-words strong {
  font-size: 13px;
  font-weight: 650;
}

.fs-words small {
  margin-top: 3px;
  color: var(--fg-dim);
  font-size: 12px;
}

.fs-actions {
  display: contents;
}

.fs-act + .fs-act {
  margin-left: 6px;
}

.fs-act {
  min-height: 30px;
  padding: 0 12px;
  border-radius: 8px;
  color: var(--accent);
  font-size: 12px;
  font-weight: 600;
}

.fs-agg-row {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 1.4fr) 72px 84px;
  align-items: center;
  gap: 14px;
  min-height: 46px;
  padding: 0 20px;
  font-size: 13px;
}

.fs-agg-row + .fs-agg-row {
  border-top: 1px solid var(--line);
}

.fs-agg-label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.fs-agg-bar {
  height: 6px;
  border-radius: 6px;
  background: var(--line);
  overflow: hidden;
}

.fs-agg-bar i {
  display: block;
  height: 100%;
  border-radius: 6px;
  background: var(--accent);
}

.fs-agg-count,
.fs-agg-size {
  color: var(--fg-dim);
  font-size: 12px;
  text-align: right;
  font-variant-numeric: tabular-nums;
}

.fs-empty,
.fs-error {
  margin: 0;
  padding: 26px 20px;
  border: 1px dashed var(--line-strong);
  border-radius: var(--radius-card);
  color: var(--fg-dim);
  font-size: 13px;
}

.fs-table .fs-empty {
  border: 0;
}

.fs-error {
  color: var(--danger);
}


.fs-help {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 22px;
  color: var(--fg-dim);
  font-size: 12px;
}

.fs-help > span {
  flex: 1;
  min-width: 0;
  text-wrap: pretty;
}

.fs-help > :first-child {
  flex: none;
  color: var(--success);
}

.fs-ask {
  flex: none;
  min-height: 30px;
  padding: 0 12px;
  font-size: 12px;
  font-weight: 600;
}

@media (max-width: 700px) {
  .files-search {
    padding-top: 28px;
  }

  .fs-row {
    grid-template-columns: auto minmax(0, 1fr) auto;
  }

  .fs-glyph {
    display: none;
  }

  .fs-agg-row {
    grid-template-columns: minmax(0, 1fr) 60px 72px;
  }

  .fs-agg-bar {
    display: none;
  }
}
</style>
