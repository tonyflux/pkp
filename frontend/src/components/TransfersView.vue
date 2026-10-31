<script setup lang="ts">
// Transfer Center:
// one row shape for cloud tasks and local uploads, grouped by what needs a
// look — work in flight with its progress, failures with their reason, a
// quiet list of what finished. Glass stays on the type lens and the filters;
// the rows sit on the flat reading surface. The toolbar keeps the only Cloud Add
// entry and, on this page, the only search: it filters these tasks. Details
// open on demand in the island beside the canvas, never over it, so the
// list keeps its scroll and filters. With more than one account
// on this computer the list is the account on screen's (Current Account) or every
// account's (All Accounts, each row naming its owner); it opens on Current Account —
// All Accounts when the menu bar's Open Transfer Center asks — and goes back to Current Account
// whenever the account on screen changes.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import AccountIdentity from "./AccountIdentity.vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import TransferBatchBar from "./TransferBatchBar.vue";
import TransferDetail from "./TransferDetail.vue";
import TransferRow from "./TransferRow.vue";
import { isTyping } from "../dom";
import { maskLogin } from "../format";
import { useCommand } from "../shortcuts";
import {
  accountLabel,
  accounts,
  cloudOf,
  currentAccount,
  currentId,
  currentTasks,
  currentDownloads,
  currentUploads,
  drive,
  loadAllTasks,
  loadTasks,
  loadDownloads,
  loadUploads,
  openSignIn,
  pickAndUpload,
  revealDownloadDir,
  transferFocus,
  ui,
} from "../store";
import {
  counts as tally,
  fromDownload,
  fromTask,
  fromUpload,
  groupOf,
  sections as sectionsOf,
  type TransferFilter,
  type TransferGroup,
} from "../transfers";
import { useRowWindows } from "../windowing";
import { vSlide } from "../slide";
import { t, N_, intlTag } from "../i18n";

type Source = "cloud" | "upload" | "download";
type Scope = "current" | "all";

// Opened from elsewhere for something in particular (the Overview's Needs Attention,
// the menu bar's Open Transfer Center), the page starts on it; otherwise on its
// defaults.
const source = ref<Source>(transferFocus.source || "cloud");
const filter = ref<TransferFilter>(transferFocus.filter || "all");
// The toolbar's search on this page (ui.transferQuery): it starts empty,
// like the filters, every time the page opens.
const needle = computed(() => ui.transferQuery);
// A task asked for by key opens its details as soon as the list holds it.
const openKey = ref(transferFocus.key);
// The scope only exists while there is more than one account to choose from.
const multi = computed(() => accounts.list.length > 1);
const scope = ref<Scope>(transferFocus.scope === "all" && multi.value ? "all" : "current");
transferFocus.source = "";
transferFocus.filter = "";
transferFocus.key = "";
transferFocus.scope = "";

// Asked for something again while the page is open (the menu bar's Open Transfer Center
// over an open transfer list): the same, in place. The list and scope go
// first — changing them resets the filter and the details — then those.
watch(
  () => [transferFocus.source, transferFocus.filter, transferFocus.key, transferFocus.scope] as const,
  async ([s, f, k, sc]) => {
    if (!s && !f && !k && !sc) return;
    transferFocus.source = "";
    transferFocus.filter = "";
    transferFocus.key = "";
    transferFocus.scope = "";
    if (s) source.value = s;
    if (sc === "all" && multi.value) scope.value = "all";
    else if (sc === "current") scope.value = "current";
    await nextTick();
    if (f) filter.value = f;
    if (k) openKey.value = k;
  },
);

const FILTERS: { key: TransferFilter; label: string; empty: string }[] = [
  { key: "all", label: N_("全部"), empty: "" },
  { key: "active", label: N_("进行中"), empty: N_("没有进行中的任务") },
  { key: "attention", label: N_("需要处理"), empty: N_("没有需要处理的任务") },
  { key: "done", label: N_("已完成"), empty: N_("没有已完成的任务") },
];

const SECTION: Record<TransferGroup, { title: string; note: string }> = {
  active: { title: N_("进行中"), note: N_("仅运行中的任务显示进度") },
  attention: { title: N_("需要处理"), note: N_("失败原因就在任务旁") },
  done: { title: N_("最近完成"), note: N_("按完成时间排列") },
};

// Accounts whose session ended: their tasks are shown, but nothing reaches their server.
const signedOut = computed(() => new Set(accounts.list.filter((a) => a.needsPassword).map((a) => a.id)));
const owners = computed(() => new Map(accounts.list.map((a) => [a.id, a])));

const items = computed(() => {
  const blocked = signedOut.value;
  const all = scope.value === "all";
  if (source.value === "cloud") {
    const tasks = all ? accounts.list.flatMap((a) => cloudOf(a.id).tasks) : currentTasks.value;
    return tasks.map((t) => fromTask(t, blocked.has(t.account)));
  }
  if (source.value === "download") {
    const downloads = all ? drive.downloads : currentDownloads.value;
    return downloads.map((t) => fromDownload(t, blocked.has(t.account)));
  }
  const uploads = all ? drive.uploads : currentUploads.value;
  return uploads.map((t) => fromUpload(t, blocked.has(t.account)));
});
// Counts follow the search too, so a chip never promises rows the list hides.
const matched = computed(() => {
  const q = needle.value.trim().toLowerCase();
  return q ? items.value.filter((item) => item.name.toLowerCase().includes(q)) : items.value;
});
const counts = computed(() => tally(matched.value));
const sections = computed(() => sectionsOf(matched.value, filter.value));
// Looked up among all items, not the matched ones: typing a search must not
// close the details the user is reading. A removed task closes them.
const opened = computed(() => items.value.find((item) => item.key === openKey.value) ?? null);

// Only the rows near the visible part of the list are in the page (an
// account keeps thousands of tasks); padding stands in for the others.
const scroller = ref<HTMLElement | null>(null);
const { windows, reveal } = useRowWindows({
  scroller,
  lists: () => sections.value.map((s) => ({ id: s.group, items: s.items })),
  key: (item) => item.key,
  // What makes a row taller: a card rather than a line of the finished
  // list, a progress bar, and under All Accounts the owner (it wraps under the
  // name on a narrow canvas).
  shape: (item) =>
    `${groupOf(item.state) === "done" ? "line" : "card"}${item.progress !== null && !item.blocked ? ":bar" : ""}${scope.value === "all" ? ":owner" : ""}`,
});

// …and its row is brought into view then, once.
if (openKey.value) {
  const stop = watch(opened, async (item) => {
    if (!item) return;
    stop();
    await nextTick();
    void reveal(item.key);
  });
}

// The batch: rows picked with ⌘/Ctrl-click, ⇧-click, the pick boxes, Space or
// ⌘A, always among the rows on screen. Two or more bring up the batch bar in
// place of the details, which only ever describe one.
const picked = ref<string[]>([]);
let anchor = "";
const visible = computed(() => sections.value.flatMap((s) => s.items));
const pickedSet = computed(() => new Set(picked.value));
const pickedItems = computed(() => visible.value.filter((item) => pickedSet.value.has(item.key)));
const batch = computed(() => pickedItems.value.length > 1);

function setPicked(keys: string[]) {
  picked.value = keys;
  if (keys.length > 1) openKey.value = "";
}

/** openRow is a plain click: this row alone, with its details. */
function openRow(key: string) {
  picked.value = [key];
  anchor = key;
  openDetails(key);
}

/** toggleRow adds the row to the batch or takes it out. */
function toggleRow(key: string) {
  anchor = key;
  setPicked(pickedSet.value.has(key) ? picked.value.filter((k) => k !== key) : [...picked.value, key]);
}

/** rangeRow picks every row from the last one touched to this one, in page order. */
function rangeRow(key: string) {
  const keys = visible.value.map((item) => item.key);
  const from = keys.indexOf(anchor);
  const to = keys.indexOf(key);
  if (from < 0 || to < 0) {
    toggleRow(key);
    return;
  }
  setPicked(keys.slice(Math.min(from, to), Math.max(from, to) + 1));
}

function pickAll() {
  setPicked(visible.value.map((item) => item.key));
  anchor = picked.value[0] ?? "";
}

function clearPicked() {
  picked.value = [];
  anchor = "";
}

// Rows that leave the screen leave the batch: a filter, a search, a removal.
watch(visible, (rows) => {
  const shown = new Set(rows.map((item) => item.key));
  if (picked.value.some((key) => !shown.has(key))) picked.value = picked.value.filter((key) => shown.has(key));
});

// The cloud lists the page reads: the account on screen's, or every account's.
const shownClouds = computed(() => (scope.value === "all" ? accounts.list.map((a) => cloudOf(a.id)) : [cloudOf(currentId.value)]));
const tasksLoading = computed(() => shownClouds.value.some((c) => c.loading));

// Only the very first fetch gets ghosts; a refresh keeps the rows it has.
const firstLoad = computed(() => source.value === "cloud" && tasksLoading.value && !items.value.length);
const busy = computed(() => source.value === "cloud" && tasksLoading.value);

// Cloud tasks are a snapshot fetched on demand; uploads stream in as events.
// The walk over every page takes a few seconds, so it counts as it goes.
// Across accounts the count adds up and the time is the oldest list's.
const live = computed(() => {
  if (source.value !== "cloud") return t("实时更新");
  const clouds = shownClouds.value;
  if (tasksLoading.value) {
    const read = clouds.reduce((n, c) => n + (c.loading ? c.loaded : 0), 0);
    return read ? t("正在读取 · 已读 {n} 条", { n: read }) : t("正在刷新…");
  }
  const times = clouds.map((c) => Date.parse(c.updatedAt)).filter((ms) => !Number.isNaN(ms));
  if (!times.length) return "";
  const d = new Date(Math.min(...times));
  const time = new Intl.DateTimeFormat(intlTag(), { hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(d);
  return t("更新于 {time}", { time });
});

const currentName = computed(() => accountLabel(currentAccount.value));
const summary = computed(() => {
  const n = t("{n} 个任务", { n: items.value.length });
  if (scope.value === "all") return t("全部 {count} 个账号 · {tasks}", { count: accounts.list.length, tasks: n });
  const a = currentAccount.value;
  return a ? `${currentName.value} · ${maskLogin(a.login)} · ${n}` : n;
});

// Signed-out accounts with no cloud tasks read before: nothing to show until
// they sign back in, which the page says instead of an empty silence.
const unsynced = computed(() => {
  if (source.value !== "cloud") return [];
  const shown = scope.value === "all" ? accounts.list : currentAccount.value ? [currentAccount.value] : [];
  return shown.filter((a) => a.needsPassword && !cloudOf(a.id).tasks.length);
});

const empty = computed(() => {
  const q = needle.value.trim();
  if (q) return { title: t("没有匹配“{q}”的任务", { q }), hint: "", clear: t("清除搜索") };
  if (filter.value !== "all") {
    const empty = FILTERS.find((f) => f.key === filter.value)?.empty ?? "";
    return { title: t(empty), hint: "", clear: t("查看全部") };
  }
  if (source.value === "download") {
    return { title: t("还没有下载"), hint: t("在文件页选中文件或文件夹，点「下载到电脑」。"), clear: "" };
  }
  if (source.value === "upload") {
    return { title: t("还没有本地上传"), hint: t("把文件或文件夹拖到「全部文件」，或点「上传文件」。"), clear: "" };
  }
  return { title: t("还没有云添加任务"), hint: t("用顶栏的「云添加」提交链接，任务会出现在这里。"), clear: "" };
});

function clearEmpty() {
  if (needle.value.trim()) ui.transferQuery = "";
  else filter.value = "all";
}

function refresh() {
  if (source.value === "upload") void loadUploads();
  else if (source.value === "download") void loadDownloads();
  else if (scope.value === "all") void loadAllTasks();
  else void loadTasks();
}
useCommand("refresh", () => {
  if (!busy.value) refresh();
});

// Closing hands focus back to whatever opened the details, so a keyboard
// user lands on the same row instead of the top of the page.
let opener: HTMLElement | null = null;

function openDetails(key: string) {
  if (!openKey.value) opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  openKey.value = key;
}

async function closeDetails() {
  openKey.value = "";
  if (picked.value.length < 2) clearPicked();
  await nextTick();
  const back = opener;
  opener = null;
  if (back?.isConnected) {
    back.focus();
    return;
  }
  // Its row left the page while the list scrolled: bring the row back.
  const row = back?.closest<HTMLElement>("[data-row]")?.dataset.row;
  if (row) (await reveal(row))?.focus();
}

watch(source, () => {
  openKey.value = "";
  filter.value = "all";
  clearPicked();
});

watch(scope, (s) => {
  openKey.value = "";
  clearPicked();
  if (s === "all") void loadAllTasks();
  else void loadTasks();
});

// Another account on screen: the list starts over on it.
watch(currentId, () => {
  if (scope.value !== "current") scope.value = "current";
  else void loadTasks();
});

watch(multi, (m) => {
  if (!m) scope.value = "current";
});

function onKeydown(event: KeyboardEvent) {
  if (isTyping(event) || ui.cloudAddOpen || ui.newFolderOpen) return;
  if ((event.metaKey || event.ctrlKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === "a") {
    event.preventDefault();
    pickAll();
    return;
  }
  if (event.key !== "Escape") return;
  // Details belong to a one-row pick, and closing them clears it.
  if (openKey.value) void closeDetails();
  else clearPicked();
}

onMounted(() => {
  void (scope.value === "all" ? loadAllTasks() : loadTasks());
  void loadUploads();
  void loadDownloads();
  window.addEventListener("keydown", onKeydown);
});
onBeforeUnmount(() => {
  window.removeEventListener("keydown", onKeydown);
  ui.transferQuery = "";
});
</script>

<template>
  <section class="transfers canvas">
    <header class="head">
      <p class="eyebrow">Transfer Center</p>
      <div class="title-line">
        <div class="titles">
          <h1>{{ t("传输中心") }}</h1>
          <p class="subtitle">{{ t("查看进行中的任务、失败原因和最近完成记录。") }}</p>
        </div>
        <div class="title-side">
          <AccountIdentity v-if="multi" class="here" :account="currentAccount" :size="24" dense />
          <span v-if="live" class="live" :class="{ busy }"><i class="live-dot" aria-hidden="true" />{{ live }}</span>
          <!-- Uploading is this page's own create action; Cloud Add is not
               repeated here, the toolbar has it. -->
          <button v-if="source === 'upload'" class="primary upload" @click="pickAndUpload('either')">
            <Icon name="upload" :size="17" />
            {{ t("上传文件") }}
          </button>
          <button v-if="source === 'download'" class="upload" @click="revealDownloadDir"><Icon name="folder" :size="17" />{{ t("下载文件夹") }}</button>
        </div>
      </div>

      <!-- Whose tasks: the account on screen's, or every account's. -->
      <div v-if="multi" class="scope-line">
        <div v-slide class="types scope" role="group" :aria-label="t('传输账号范围')">
          <button :aria-pressed="scope === 'current'" :class="{ on: scope === 'current' }" @click="scope = 'current'">{{ t("当前账号") }}</button>
          <button :aria-pressed="scope === 'all'" :class="{ on: scope === 'all' }" @click="scope = 'all'">{{ t("全部账号") }}</button>
        </div>
        <span class="summary">{{ summary }}</span>
      </div>

      <div class="switches">
        <div v-slide class="types" role="tablist" :aria-label="t('传输类型')">
          <button role="tab" :aria-selected="source === 'cloud'" :class="{ on: source === 'cloud' }" @click="source = 'cloud'">
            {{ t("云添加") }}
          </button>
          <button role="tab" :aria-selected="source === 'upload'" :class="{ on: source === 'upload' }" @click="source = 'upload'">
            {{ t("本地上传") }}
          </button>
          <button role="tab" :aria-selected="source === 'download'" :class="{ on: source === 'download' }" @click="source = 'download'">
            {{ t("下载到电脑") }}
          </button>
        </div>
      </div>

      <div class="filter-line">
        <div v-slide class="filters" role="group" :aria-label="t('状态筛选')">
          <button
            v-for="f in FILTERS"
            :key="f.key"
            class="filter"
            :class="{ on: filter === f.key, alert: f.key === 'attention' && counts.attention > 0 }"
            :aria-pressed="filter === f.key"
            @click="filter = f.key"
          >
            {{ t(f.label) }}
            <strong>{{ counts[f.key] }}</strong>
          </button>
        </div>
        <button class="utility" :class="{ working: busy }" :disabled="busy" :title="t('刷新任务状态')" @click="refresh">
          <Orbit v-if="busy" :size="14" :label="t('刷新中')" />
          <Icon v-else name="restore" :size="14" />
          <span class="utility-label">{{ t("刷新") }}</span>
        </button>
      </div>
    </header>

    <div ref="scroller" class="scroll" :class="{ selecting: batch }">
      <div v-if="firstLoad" class="ghosts late" aria-busy="true" :aria-label="t('正在加载')">
        <div v-for="i in 3" :key="i" class="ghost" :style="{ '--i': i }">
          <span class="ghost-tile skel" />
          <span class="ghost-text">
            <span class="skel ghost-line" :style="{ width: 30 + ((i * 23) % 36) + '%' }" />
            <span class="skel ghost-line thin" style="width: 42%" />
          </span>
        </div>
      </div>

      <template v-else>
        <p v-for="a in unsynced" :key="a.id" class="unsynced">
          <Icon name="alert" :size="15" />
          <span>{{ t("「{name}」需要重新登录后才能同步云添加任务", { name: accountLabel(a) }) }}</span>
          <button class="quiet" @click="openSignIn(a.id)">{{ t("重新登录") }}</button>
        </p>

        <section v-for="s in sections" :key="s.group" class="group" :aria-label="t(SECTION[s.group].title)">
          <header class="group-head">
            <h2>
              {{ t(SECTION[s.group].title) }}
              <span class="count">{{ s.items.length }}</span>
            </h2>
            <span class="group-note">{{ t(SECTION[s.group].note) }}</span>
          </header>
          <div :data-list="s.group" :class="s.group === 'done' ? 'done-list' : 'cards'" :style="windows[s.group]?.style">
            <TransferRow
              v-for="row in windows[s.group]?.rows"
              :key="row.item.key"
              :data-row="row.item.key"
              :style="row.style"
              :item="row.item"
              :last="row.index === s.items.length - 1"
              :open="row.item.key === openKey"
              :picked="pickedSet.has(row.item.key)"
              :selecting="batch"
              :owner="scope === 'all' ? (owners.get(row.item.account) ?? null) : null"
              @open="openRow(row.item.key)"
              @toggle="toggleRow(row.item.key)"
              @range="rangeRow(row.item.key)"
            />
          </div>
        </section>

        <div v-if="!sections.length && !(unsynced.length && !items.length)" class="empty">
          <Icon :name="source === 'upload' ? 'upload' : source === 'download' ? 'download' : 'transfer'" :size="26" />
          <strong>{{ empty.title }}</strong>
          <p v-if="empty.hint">{{ empty.hint }}</p>
          <button v-if="empty.clear" class="quiet" @click="clearEmpty">{{ empty.clear }}</button>
        </div>
      </template>
    </div>

    <footer v-if="multi" class="scope-foot">
      <span>{{ scope === "all" ? t("全部账号 · {n} 项", { n: visible.length }) : t("当前账号 · {n} 项", { n: visible.length }) }}</span>
      <span class="new-owner" :title="t('新建任务属于 {name}', { name: currentName })">{{ t("新建任务属于 {name}", { name: currentName }) }}</span>
    </footer>

    <TransferBatchBar v-if="batch" :class="{ 'over-foot': multi }" :items="pickedItems" :total="visible.length" @pick-all="pickAll" @clear="clearPicked" />
  </section>

  <TransferDetail v-if="opened" :item="opened" @close="closeDetails" />
</template>

<style scoped>
.transfers {
  /* The canvas narrows when the details island takes the right of the row
     or the window is small; the page lays itself out by the canvas width. */
  container-type: inline-size;
  position: relative;
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* The list ends in a short fade rather than a hard cut at the radius. */
.transfers::after {
  content: "";
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 20px;
  border-radius: 0 0 var(--radius-island) var(--radius-island);
  background: linear-gradient(transparent, var(--canvas));
  pointer-events: none;
}

/* ---------- header: title, type lens, task search, status filters ---------- */

.head {
  flex: none;
  padding: 28px 34px 0;
}

.eyebrow {
  margin: 0 0 6px;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  color: color-mix(in srgb, var(--accent) 78%, var(--fg-dim));
}

/* The heading never breaks; on a narrow canvas the account, the live
   status and the page's action drop under it instead of squeezing it. */
.title-line {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: 12px 20px;
}

.titles {
  flex: 1 1 200px;
}

.title-line h1 {
  margin: 0;
  font-size: 30px;
  font-weight: 700;
  line-height: 1.15;
  letter-spacing: -0.03em;
  white-space: nowrap;
}

.subtitle {
  margin: 7px 0 0;
  font-size: 13px;
  color: var(--fg-dim);
  text-wrap: balance;
}

.title-side {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px 16px;
  min-width: 0;
}

.live {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding-bottom: 3px;
  font-size: 12px;
  color: var(--fg-dim);
  white-space: nowrap;
}

.live-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--success);
  box-shadow: 0 0 10px color-mix(in srgb, var(--success) 60%, transparent);
}

.live.busy .live-dot {
  background: var(--accent);
  box-shadow: 0 0 10px color-mix(in srgb, var(--accent) 60%, transparent);
}

.upload {
  min-height: 40px;
  padding: 0 18px 0 15px;
  font-size: 14px;
}

/* Whose tasks: the type lens's family (.types), with the summary beside it
   and under it once the canvas is narrow. */
.here {
  max-width: 220px;
}

.scope-line {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 10px 18px;
  margin-top: 18px;
}

.summary {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
  color: var(--fg-dim);
}

.switches {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  margin-top: 22px;
}

/* The type lens: a light glass track whose selection is local fog, a thin
   bright rim and an inner highlight — not a saturated cobalt block, which
   stays reserved for create actions and real progress. */
.types {
  display: flex;
  gap: 3px;
  padding: 4px;
  border-radius: 16px;
  background: color-mix(in srgb, var(--accent) 9%, transparent);
  border: 1px solid color-mix(in srgb, var(--accent) 17%, transparent);
}

.types button {
  min-width: 112px;
  min-height: 37px;
  padding: 0 16px;
  border: 0;
  border-radius: 12px;
  background: transparent;
  color: var(--fg-dim);
  font-size: 13px;
  font-weight: 600;
}

.types button:hover:not(:disabled):not(.on) {
  background: color-mix(in srgb, var(--accent) 10%, transparent);
  color: var(--fg);
}

.types::before {
  background: linear-gradient(
    135deg,
    color-mix(in srgb, var(--accent) 34%, transparent),
    color-mix(in srgb, var(--accent) 18%, transparent)
  );
  box-shadow:
    inset 0 1px 0 color-mix(in srgb, var(--glass-rim) 80%, transparent),
    inset 0 0 0 1px color-mix(in srgb, var(--accent) 34%, transparent),
    0 3px 10px rgba(7, 29, 61, 0.2);
}

.types button.on {
  color: var(--fg);
  background: transparent;
}

.types button:disabled {
  opacity: 0.48;
}

/* Status filters are a lighter layer than the type lens. */
.filter-line {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  margin-top: 18px;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--line);
}

.filters {
  display: flex;
  gap: 4px;
  min-width: 0;
  overflow-x: auto;
  scrollbar-width: none;
}

.filter {
  flex: none;
  gap: 5px;
  min-height: 34px;
  padding: 0 12px;
  border: 0;
  border-radius: 12px;
  background: transparent;
  color: var(--fg-dim);
  font-size: 13px;
  font-weight: 600;
}

.filter:hover:not(:disabled):not(.on) {
  background: color-mix(in srgb, var(--accent) 9%, transparent);
  color: var(--fg);
}

.filters::before {
  background: color-mix(in srgb, var(--accent) 15%, transparent);
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--accent) 26%, transparent);
}

.filter.on {
  color: var(--fg);
  background: transparent;
}

.filter strong {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.filter.alert strong {
  color: var(--danger);
}

.utility {
  flex: none;
  gap: 5px;
  min-height: 32px;
  padding: 0 10px;
  border: 0;
  border-radius: 10px;
  background: transparent;
  color: var(--fg-dim);
  font-size: 12px;
  font-weight: 500;
}

.utility:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent) 12%, transparent);
  color: var(--fg);
}

/* ---------- the list ---------- */

/* Only the list scrolls; the bottom padding keeps the last row clear of
   the status pill that floats over the canvas edge. */
.scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 2px 34px 40px;
}

/* The batch bar floats over the list's end; the last rows must be able to
   scroll clear of it. */
.scroll.selecting {
  padding-bottom: 92px;
}

.group {
  margin-top: 22px;
}

.group-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 11px;
}

.group-head h2 {
  display: flex;
  align-items: center;
  gap: 9px;
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  letter-spacing: -0.01em;
}

.count {
  min-width: 20px;
  padding: 2px 6px;
  border-radius: 7px;
  text-align: center;
  font-size: 11px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  color: var(--fg-dim);
  background: color-mix(in srgb, var(--accent) 14%, transparent);
}

.group-note {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--fg-dim);
}

/* Both lists place the focused row kept while scrolled away (windowing.ts). */
.cards {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* Finished work is one quiet list with hairlines, not a stack of cards. */
.done-list {
  position: relative;
  overflow: hidden;
  border-radius: 16px;
  border: 1px solid color-mix(in srgb, var(--accent) 12%, transparent);
  background: color-mix(in srgb, var(--fg) 3%, transparent);
}

/* ---------- waiting and empty ---------- */

.ghosts {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 22px;
}

.ghost {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 14px 18px;
  border-radius: 15px;
  border: 1px solid color-mix(in srgb, var(--accent) 10%, transparent);
  background: color-mix(in srgb, var(--fg) 3%, transparent);
}

.ghost-tile {
  width: 44px;
  height: 44px;
  flex: none;
  border-radius: 12px;
}

.ghost-text {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 9px;
}

.ghost-line {
  display: block;
  height: 12px;
  border-radius: 6px;
}

.ghost-line.thin {
  height: 9px;
}

.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 64px 20px;
  text-align: center;
  color: var(--fg-dim);
}

.empty strong {
  margin-top: 4px;
  font-size: 15px;
  font-weight: 600;
  color: var(--fg);
}

.empty p {
  max-width: 340px;
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
}

.empty button {
  margin-top: 6px;
  color: var(--accent);
  font-weight: 600;
}

/* A signed-out account with nothing read: a quiet line, not an empty page. */
.unsynced {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 16px 0 0;
  padding: 8px 8px 8px 12px;
  border-radius: 12px;
  background: color-mix(in srgb, var(--warning) 8%, transparent);
  font-size: 13px;
  color: var(--fg);
}

.unsynced svg {
  flex: none;
  color: var(--warning);
}

.unsynced span {
  flex: 1;
  min-width: 0;
}

.unsynced button {
  flex: none;
  min-height: 40px;
  font-size: 13px;
  font-weight: 600;
  color: var(--accent);
}

/* What the list holds, and whose a new task will be. Above the canvas's
   bottom fade so it stays readable. */
.scope-foot {
  position: relative;
  z-index: 1;
  flex: none;
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 10px 34px 16px;
  border-top: 1px solid var(--line);
  font-size: 12px;
  color: var(--fg-dim);
  white-space: nowrap;
}

.new-owner {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* The batch bar floats over the list's end, clear of the footer's lines. */
.transfers > .over-foot {
  bottom: 54px;
}

/* ---------- narrower canvases ---------- */

@container (max-width: 760px) {
  .head {
    padding: 22px 22px 0;
  }

  .scroll {
    padding: 2px 22px 36px;
  }

  .types button {
    min-width: 0;
    padding: 0 14px;
  }

  .scope-foot {
    padding-inline: 22px;
  }
}

/* The type lens takes the full width; the explanatory lines go, the title,
   filters and every task stay. */
@container (max-width: 600px) {
  .eyebrow,
  .subtitle,
  .group-note {
    display: none;
  }

  .title-line {
    align-items: center;
  }

  .title-line h1 {
    font-size: 24px;
  }

  .switches {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
    margin-top: 14px;
  }

  /* Each lens button starts from its own label and shares what is left, so
     a long label (コンピュータにダウンロード) gets the room it needs. */
  .types button {
    flex: 1 1 auto;
    padding: 0 10px;
    font-size: 12px;
    line-height: 1.3;
  }

  .filter-line {
    margin-top: 10px;
    padding-bottom: 10px;
  }

  .filter {
    padding: 0 8px;
    font-size: 12px;
  }

  .utility-label {
    display: none;
  }
}

@container (max-width: 420px) {
  .head {
    padding: 18px 14px 0;
  }

  .scroll {
    padding: 2px 14px 32px;
  }

  .live,
  .here {
    display: none;
  }

  .scope-foot {
    padding-inline: 14px;
  }
}

/* A short window compresses the header, never the list. The mock draws this
   at 960×640; the default 1100×760 window has the same chrome around the
   canvas, so it gets the compact header too and one more visible row. */
@media (max-height: 780px) {
  .eyebrow {
    display: none;
  }

  .head {
    padding-top: 20px;
  }

  .title-line h1 {
    font-size: 26px;
  }

  .switches {
    margin-top: 14px;
  }

  .filter-line {
    margin-top: 10px;
    padding-bottom: 10px;
  }

  .group {
    margin-top: 14px;
  }

  .group-head {
    margin-bottom: 8px;
  }
}
</style>
