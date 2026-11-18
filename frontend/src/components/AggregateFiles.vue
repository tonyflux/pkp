<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import SelectMenu from "./SelectMenu.vue";
import Thumb from "./Thumb.vue";
import { DriveService } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import type { AggregateAccount, AggregateEntry, ListRequest } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import { bytes, clock, maskLogin, shownPath } from "../format";
import { t } from "../i18n";
import { vSlide } from "../slide";
import { removeChip } from "../searchquery";
import {
  accountName, accounts, aggregateRefresh, currentId, downloadEntries, entryKey, filesPlace,
  nowPlaying, notify, openViewer, play, player, revealFile, searchParse, setLayout, sorts,
  switchAccount, ui, type CategoryFilter, type FilesAnchor, type OwnedEntry,
} from "../store";

const chips: { key: CategoryFilter; label: string }[] = [
  { key: "all", get label() { return t("全部"); } },
  { key: "video", get label() { return t("视频"); } },
  { key: "audio", get label() { return t("chip|音频"); } },
  { key: "image", get label() { return t("图片"); } },
  { key: "document", get label() { return t("chip|文档"); } },
  { key: "other", get label() { return t("其他"); } },
];
const rows = ref<OwnedEntry[]>([]);
const states = ref<AggregateAccount[]>([]);
const next = ref("");
const total = ref(0);
const loading = ref(false);
const more = ref(false);
const error = ref("");
const selected = ref("");
const scroller = ref<HTMLElement>();
let serial = 0;
let timer: number | undefined;
let alive = true;

const warning = computed(() => states.value.filter((state) => state.state !== "ready" || state.error).map((state) => {
  const reason = state.state === "signin" ? t("需要重新登录") : state.state === "index" ? t("索引尚未就绪") : state.state === "error" ? t("索引查询失败") : t("索引同步失败");
  return `${accountName(state.account)} · ${reason}`;
}).join("；"));
const sortLabel = computed(() => sorts.find((item) => item.key === ui.sortKey)?.label ?? sorts[0].label);
const blocked = computed(() => !!searchParse.value.query.aggregate);

function request(): ListRequest {
  return { view: "files", parentId: "", query: ui.query.trim(), category: ui.category, sort: ui.sortKey, asc: ui.sortAsc, offset: 0, pageToken: "" };
}

async function load(cursor = "") {
  if (cursor && (loading.value || more.value)) return;
  const version = cursor ? serial : ++serial;
  if (!cursor) {
    rows.value = [];
    states.value = [];
    next.value = "";
    total.value = 0;
    selected.value = "";
  }
  if (blocked.value) { loading.value = false; more.value = false; error.value = ""; return; }
  if (cursor) more.value = true;
  else loading.value = true;
  error.value = "";
  try {
    const parsed = searchParse.value;
    const query = parsed.filtered ? { ...parsed.query, category: parsed.query.category || (ui.category === "other" ? "otherfiles" : ui.category === "all" ? "" : ui.category) } : null;
    const page = await DriveService.ListAllAccounts(request(), cursor, query);
    if (!alive || version !== serial) return;
    const incoming = (page.entries ?? []).map((item: AggregateEntry) => ({ ...item.entry, account: item.account }));
    rows.value = cursor ? [...rows.value, ...incoming] : incoming;
    states.value = page.accounts ?? [];
    total.value = page.total;
    next.value = page.next;
  } catch (err) {
    if (!alive || version !== serial) return;
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    if (alive && version === serial) { loading.value = false; more.value = false; }
  }
}

watch([() => ui.query, () => ui.category, () => ui.sortKey, () => ui.sortAsc, aggregateRefresh, () => accounts.list.map((a) => `${a.id}:${a.needsPassword}`).join("|")], () => {
  serial++;
  clearTimeout(timer);
  loading.value = true;
  more.value = false;
  rows.value = [];
  next.value = "";
  selected.value = "";
  total.value = 0;
  states.value = [];
  timer = window.setTimeout(() => void load(), 180);
}, { immediate: true });

function isPlaying(entry: OwnedEntry) {
  return player.presentation !== "off" && !!nowPlaying.value && entryKey(nowPlaying.value) === entryKey(entry);
}
function source(entry: OwnedEntry) {
  const account = accounts.list.find((value) => value.id === entry.account);
  const name = accountName(entry.account || "");
  const duplicate = accounts.list.filter((value) => accountName(value.id) === name).length > 1;
  return `${name}${duplicate && account ? ` (${maskLogin(account.login)})` : ""} · ${shownPath(entry.path)}`;
}
async function manage(entry: OwnedEntry) {
  if (!entry.account) return;
  await revealFile({ account: entry.account, fileId: entry.id });
}
async function preview(entry: OwnedEntry) {
  if (!entry.account) return;
  if (entry.account !== currentId.value && !(await switchAccount(entry.account))) return;
  if (currentId.value !== entry.account || accounts.switching) return;
  await openViewer(entry, [entry]);
}
function activate(entry: OwnedEntry) {
  if (entry.folder) void manage(entry);
  else if (entry.playable) void play(entry, rows.value);
  else if (entry.preview) void preview(entry);
  else void manage(entry);
}
function download(entry: OwnedEntry) {
  if (entry.account) void downloadEntries([entry], entry.account);
  else notify(t("发生了意外错误"), "error");
}
function savePlace(): FilesAnchor | null {
  const root = scroller.value;
  return root ? { id: selected.value, offset: 0, top: root.scrollTop } : null;
}
async function restorePlace(place: FilesAnchor | null) {
  await nextTick();
  if (scroller.value) scroller.value.scrollTop = place?.top ?? 0;
}
filesPlace.save = savePlace;
filesPlace.restore = (place) => void restorePlace(place);
onBeforeUnmount(() => { alive = false; serial++; clearTimeout(timer); });
</script>

<template>
  <div class="controls">
    <div v-slide class="segments kinds glass-well" role="tablist" :aria-label="t('分类')">
      <button v-for="chip in chips" :key="chip.key" role="tab" :aria-selected="ui.category === chip.key" :class="{ on: ui.category === chip.key }" @click="ui.category = chip.key">{{ chip.label }}</button>
    </div>
    <span class="spacer" />
    <div v-slide class="segments compact glass-well" role="group" :aria-label="t('视图')">
      <button :class="{ on: ui.layout === 'grid' }" :title="t('网格')" :aria-label="t('网格')" :aria-pressed="ui.layout === 'grid'" @click="setLayout('grid')"><Icon name="grid" :size="18" /></button>
      <button :class="{ on: ui.layout === 'list' }" :title="t('列表')" :aria-label="t('列表')" :aria-pressed="ui.layout === 'list'" @click="setLayout('list')"><Icon name="list" :size="18" /></button>
    </div>
    <div class="sort glass-well">
      <SelectMenu v-model="ui.sortKey" :label="t('排序方式')" :options="sorts.map((s) => ({ value: s.key, label: s.label }))"><span>{{ sortLabel }}</span><Icon name="chevronDown" :size="16" /></SelectMenu>
      <button class="quiet icon" :class="{ asc: ui.sortAsc }" :title="ui.sortAsc ? t('升序') : t('降序')" @click="ui.sortAsc = !ui.sortAsc"><Icon name="sort" :size="18" /></button>
    </div>
  </div>
  <div v-if="searchParse.chips.length" class="filter-chips">
    <button v-for="chip in searchParse.chips" :key="chip.key" class="filter-chip" :title="t('移除条件：{label}', { label: chip.label })" :aria-label="t('移除条件：{label}', { label: chip.label })" @click="ui.query = removeChip(ui.query, chip)">{{ chip.label }} <Icon name="close" :size="12" /></button>
  </div>
  <p v-if="warning" class="notice" role="status">{{ t("结果可能不完整：{accounts}", { accounts: warning }) }}</p>
  <p v-if="blocked" class="notice" role="alert">{{ t("跨账号统计暂不可用，请移除统计条件。") }}</p>
  <p v-if="error" class="notice" role="alert">{{ t("加载列表") }}：{{ t("发生了意外错误") }} <button class="quiet" @click="load()">{{ t("重试") }}</button></p>
  <div ref="scroller" class="scroll" :aria-busy="loading || more">
    <div v-if="loading" class="empty" role="status"><Orbit :size="26" /> {{ t("正在加载…") }}</div>
    <div v-else-if="!rows.length && !error && !blocked" class="empty">{{ t("聚合范围内未找到文件") }}</div>
    <div v-else-if="ui.layout === 'grid'" class="grid">
      <article v-for="entry in rows" :key="entryKey(entry)" class="card" :class="{ selected: selected === entryKey(entry) }" tabindex="0" @click="selected = entryKey(entry)" @dblclick="activate(entry)" @keydown.enter.prevent="activate(entry)">
        <div class="thumb">
          <Thumb :src="entry.thumbnail" :alt="entry.name" lazy><Icon :name="entry.folder ? 'folder' : entry.category" :size="28" /></Thumb>
          <span v-if="entry.duration" class="duration">{{ clock(entry.duration) }}</span>
          <span v-if="isPlaying(entry)" class="playing"><Icon name="play" :size="13" filled /></span>
          <button v-if="entry.playable || entry.preview" class="play" :title="entry.playable ? t('播放') : t('预览')" @click.stop="activate(entry)"><Icon :name="entry.playable ? 'play' : 'eye'" :size="16" :filled="entry.playable" /></button>
        </div>
        <div class="meta">
          <strong :title="entry.name">{{ entry.name }}</strong>
          <small class="path" :title="source(entry)">{{ source(entry) }}</small>
          <div class="actions">
            <span>{{ bytes(entry.size) }}</span>
            <button class="quiet icon" :title="t('下载到电脑')" :aria-label="t('下载到电脑')" @click.stop="download(entry)"><Icon name="download" :size="16" /></button>
            <button class="quiet icon" :title="t('进入所属账号管理')" :aria-label="t('进入所属账号管理')" @click.stop="manage(entry)"><Icon name="folder" :size="16" /></button>
          </div>
        </div>
      </article>
    </div>
    <table v-else class="rows">
      <thead><tr><th>{{ t("名称") }}</th><th>{{ t("大小") }}</th><th>{{ t("column|位置") }}</th><th></th></tr></thead>
      <tbody><tr v-for="entry in rows" :key="entryKey(entry)" :class="{ selected: selected === entryKey(entry) }" tabindex="0" @click="selected = entryKey(entry)" @dblclick="activate(entry)" @keydown.enter.prevent="activate(entry)">
        <td><Icon :name="entry.folder ? 'folder' : entry.category" :size="18" /> <span :title="entry.name">{{ entry.name }}</span></td>
        <td>{{ bytes(entry.size) }}</td><td :title="source(entry)">{{ source(entry) }}</td>
        <td class="actions"><button v-if="entry.playable || entry.preview" class="quiet icon" :title="entry.playable ? t('播放') : t('预览')" @click.stop="activate(entry)"><Icon :name="entry.playable ? 'play' : 'eye'" :size="16" /></button><button class="quiet icon" :title="t('下载到电脑')" @click.stop="download(entry)"><Icon name="download" :size="16" /></button><button class="quiet icon" :title="t('进入所属账号管理')" @click.stop="manage(entry)"><Icon name="folder" :size="16" /></button></td>
      </tr></tbody>
    </table>
    <div v-if="next" class="more"><button :disabled="more" @click="load(next)"><Orbit v-if="more" :size="15" /> {{ more ? t("正在加载…") : t("加载更多") }}</button></div>
  </div>
  <footer>{{ t("所有账号 · {n} 个文件", { n: total }) }}<span>{{ selected ? t("已选 {n} 项", { n: 1 }) : t("未选择文件") }}</span></footer>
</template>

<style scoped>
.controls { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; margin: 14px 0 20px; }
.spacer { flex: 1; }
.segments, .sort { display: flex; align-items: center; min-height: 44px; padding: 4px; border-radius: var(--radius-pill); }
.segments button { min-height: 36px; padding: 0 20px; border: 0; border-radius: var(--radius-pill); background: none; color: var(--fg-dim); font-size: 15px; }
.segments::before { background: var(--primary); }
.segments button.on { color: var(--on-primary); font-weight: 600; }
.compact button { width: 40px; padding: 0; }
.compact::before { background: var(--accent-soft); }
.compact button.on { color: var(--fg); }
.sort { gap: 8px; padding-left: 14px; }
.sort .asc svg { transform: scaleY(-1); }
.filter-chips { display: flex; flex-wrap: wrap; gap: 6px; margin: -8px 0 12px; }
.filter-chip { min-height: 28px; padding: 0 9px; gap: 5px; border-radius: 8px; font-size: 12px; color: var(--accent); background: var(--accent-soft); }
.notice { flex: none; margin: 0 0 12px; font-size: 13px; line-height: 1.5; color: var(--warning); overflow-wrap: anywhere; }
.scroll { flex: 1; min-height: 0; overflow-y: auto; margin: 0 -8px; padding: 4px 8px 20px; }
.empty { min-height: 180px; display: flex; align-items: center; justify-content: center; gap: 10px; color: var(--fg-dim); }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 216px), 1fr)); gap: 20px 16px; }
.card { min-width: 0; cursor: pointer; }
.thumb { position: relative; aspect-ratio: 16 / 9; border-radius: 12px; overflow: hidden; background: var(--surface); }
.selected .thumb { box-shadow: 0 0 0 2px var(--accent); }
.duration, .playing { position: absolute; left: 10px; bottom: 10px; padding: 3px 6px; border-radius: 5px; color: white; background: rgba(0, 0, 0, .7); font-size: 11px; }
.play { position: absolute; right: 10px; bottom: 10px; width: 34px; height: 34px; min-height: 34px; padding: 0; border-radius: 50%; color: var(--on-primary); background: var(--primary); opacity: 0; }
.card:hover .play, .card:focus-within .play { opacity: 1; }
.meta { display: flex; flex-direction: column; gap: 3px; padding: 10px 2px 0; min-width: 0; }
.meta strong, .meta .path { overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.meta strong { font-size: 15px; }
.meta .path { color: var(--accent); font-size: 12px; }
.actions { display: flex; align-items: center; justify-content: flex-end; gap: 2px; color: var(--fg-dim); }
.actions span { flex: 1; font-size: 12px; }
.actions button { width: 30px; height: 30px; min-height: 30px; }
.rows { width: 100%; table-layout: fixed; border-collapse: collapse; }
.rows th, .rows td { padding: 10px 12px; border-bottom: 1px solid var(--line); text-align: left; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.rows th { font-size: 13px; color: var(--fg-dim); font-weight: 500; }
.rows tr { cursor: pointer; }
.rows tr.selected { background: var(--accent-soft); }
.rows td:first-child svg { vertical-align: middle; color: var(--fg-dim); }
.rows td:first-child svg { display: inline-block; margin-right: 8px; }
.rows td:first-child span { vertical-align: middle; }
.rows th:nth-child(2) { width: 100px; }
.rows th:nth-child(3) { width: 30%; }
.rows th:last-child { width: 110px; }
.more { display: flex; justify-content: center; padding: 24px; }
footer { flex: none; height: 40px; display: flex; align-items: center; justify-content: space-between; gap: 12px; margin: 0 -24px; padding: 0 24px; border-top: 1px solid var(--line); color: var(--fg-dim); font-size: 12px; }
@container files (max-width: 679px) { .kinds { flex-wrap: wrap; height: auto; } .kinds button { padding-inline: 10px; } .rows th:nth-child(2), .rows td:nth-child(2) { display: none; } }
</style>
