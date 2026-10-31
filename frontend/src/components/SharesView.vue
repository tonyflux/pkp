<script setup lang="ts">
// My shares: a scannable table on the reading canvas. A click only selects
// a share; Details — the row's button or a double-click — opens its details in
// an island column beside the canvas, like the file page's context column
// and the transfer details: the canvas gives up the column's width, so no
// row is ever covered and the list keeps its order and scroll. Under the
// workspace width where the file page turns its column into a page (960px),
// the details are a page in the canvas's place, with Back to share list. Closing
// hands focus back to what opened them. Selecting a share never touches the
// file page's selection.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import Thumb from "./Thumb.vue";
import { bytes, day } from "../format";
import { WORKSPACE_QUERY, isTyping, useMedia } from "../dom";
import { t } from "../i18n";
import ConfirmInline from "./ConfirmInline.vue";
import BatchBar from "./BatchBar.vue";
import {
  accounts,
  appLocked,
  cancelShares,
  copyShareLinks,
  currentId,
  copyShareLink,
  hasMoreShares,
  loadMoreShares,
  loadShares,
  selectedShare,
  shareStatus,
  shares,
  ui,
} from "../store";
import type { ShareSummary } from "../../bindings/github.com/tonyflux/pkp/internal/services";

const confirming = ref(false);
const batchConfirming = ref(false);
const actionBusy = ref(false);
const picked = ref<string[]>([]);
let pickAnchor = "";

const firstLoad = computed(() => shares.loading && !shares.list.length);

// The status is derived once per row; the table reads it from three places.
const rows = computed(() => {
  const needle = ui.shareQuery.trim().toLowerCase();
  const list = needle ? shares.list.filter((s) => s.title.toLowerCase().includes(needle)) : shares.list;
  return list.map((share) => ({ share, status: shareStatus(share) }));
});

const toneIcon = { ok: "check", warn: "clock", off: "close" } as const;

const pickedSet = computed(() => new Set(picked.value));
const selected = computed(() => rows.value.map(({ share }) => share).filter((share) => pickedSet.value.has(share.id)));
const cancelable = computed(() => selected.value.filter((share) => share.status !== "canceled"));
const multi = computed(() => selected.value.length >= 2);
const allPicked = computed(() => rows.value.length > 0 && selected.value.length === rows.value.length);

function setPicked(ids: string[], anchor = "") {
  picked.value = ids;
  pickAnchor = anchor;
  shares.selectedId = ids.length === 1 ? ids[0] : "";
  if (ids.length !== 1) {
    detailOpen.value = false;
    opener = null;
  }
}

function select(share: ShareSummary) {
  setPicked([share.id], share.id);
}

function togglePick(share: ShareSummary) {
  setPicked(pickedSet.value.has(share.id) ? picked.value.filter((id) => id !== share.id) : [...picked.value, share.id], share.id);
}

function pick(share: ShareSummary, event: MouseEvent) {
  if (event.metaKey || event.ctrlKey) {
    togglePick(share);
  } else if (event.shiftKey) {
    const from = rows.value.findIndex((row) => row.share.id === pickAnchor);
    const to = rows.value.findIndex((row) => row.share.id === share.id);
    if (from < 0 || to < 0) return select(share);
    const range = rows.value.slice(Math.min(from, to), Math.max(from, to) + 1).map((row) => row.share.id);
    const rangeSet = new Set(range);
    setPicked([...picked.value.filter((id) => !rangeSet.has(id)), ...range], pickAnchor);
  } else {
    select(share);
  }
}

function pickAll() {
  const ids = rows.value.map(({ share }) => share.id);
  setPicked(ids, ids[0] ?? "");
}

function clearPicks() {
  setPicked([]);
}

function onDoubleClick(share: ShareSummary, event: MouseEvent) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  void openDetail(share, event.currentTarget);
}

/* ---------- the details column ---------- */

// Open only on request, and only ever for the selected share: picking
// another row while it is open shows that one instead.
const detailOpen = ref(false);
const detail = computed(() => (detailOpen.value ? selectedShare.value : null));
const detailStatus = computed(() => (detail.value ? shareStatus(detail.value) : null));
const expiry = computed(() => {
  const s = detail.value;
  if (!s) return "";
  if (s.status === "active" && s.daysLeft < 0) return t("永久有效");
  // Beside the Expiration label: English already says "Expires" there.
  return s.expiresAt ? t("detail|{date} 到期", { date: day(s.expiresAt) }) : "—";
});

const detailEl = ref<HTMLElement | null>(null);
const area = ref<HTMLElement | null>(null);

// Where the file page's files and context column take turns (under 960px),
// the details take the canvas's place instead of squeezing it to a sliver.
const asPage = useMedia(WORKSPACE_QUERY);

// Whatever opened the details — the row's Details, the row itself — gets
// focus back when they close, so the keyboard lands where it left.
let opener: HTMLElement | null = null;

async function openDetail(share: ShareSummary, from: EventTarget | null) {
  select(share);
  if (!detailOpen.value) opener = from instanceof HTMLElement ? from : null;
  detailOpen.value = true;
  await nextTick();
  detailEl.value?.focus({ preventScroll: true });
}

async function closeDetail() {
  if (!detailOpen.value) return;
  detailOpen.value = false;
  confirming.value = false;
  await nextTick();
  const back = opener;
  opener = null;
  // preventScroll: the list stays where it was, as it was.
  if (back?.isConnected) back.focus({ preventScroll: true });
  else area.value?.querySelector<HTMLElement>(`tr[data-share="${CSS.escape(shares.selectedId)}"]`)?.focus({ preventScroll: true });
}

// The details page takes the canvas's place rather than covering it, so
// the row is only as tall as what it shows: in a short window at a large
// zoom the whole workspace scrolls (App's stack), and the playback bar
// follows right after the details instead of after the whole hidden list.
// The list's place, and the workspace's, are put back when it returns.
const listEl = ref<HTMLElement | null>(null);
const pageShown = computed(() => !!detail.value && asPage.value);
let parked: { list: number; outer: Element | null; outerTop: number } | null = null;

function scrollParent(el: Element | null): Element | null {
  for (let p = el?.parentElement; p; p = p.parentElement) {
    const y = getComputedStyle(p).overflowY;
    if ((y === "auto" || y === "scroll") && p.scrollHeight > p.clientHeight) return p;
  }
  return null;
}

// "pre": read the places while the list is still on screen.
watch(
  pageShown,
  async (shown) => {
    if (shown) {
      const outer = scrollParent(area.value);
      parked = { list: listEl.value?.scrollTop ?? 0, outer, outerTop: outer?.scrollTop ?? 0 };
      await nextTick();
      if (outer) detailEl.value?.scrollIntoView({ block: "start" });
      return;
    }
    const back = parked;
    parked = null;
    await nextTick();
    if (back && listEl.value) listEl.value.scrollTop = back.list;
    if (back?.outer) back.outer.scrollTop = back.outerTop;
  },
  { flush: "pre" },
);

// A share that goes away (cancelled, the list reloaded without it) takes
// its details with it.
watch(selectedShare, (share) => {
  if (!share) {
    detailOpen.value = false;
    confirming.value = false;
  }
});

// Keep batch actions strictly within the loaded rows still on screen.
watch(
  () => rows.value.map(({ share }) => share.id),
  (ids) => {
    const visible = new Set(ids);
    const kept = picked.value.filter((id) => visible.has(id));
    if (kept.length !== picked.value.length) setPicked(kept, visible.has(pickAnchor) ? pickAnchor : "");
    else if (!visible.has(pickAnchor)) pickAnchor = "";
  },
  { flush: "sync" },
);

watch(
  () => JSON.stringify(selected.value.map((share) => [share.id, share.status]).sort(([a], [b]) => a.localeCompare(b))),
  () => {
    confirming.value = false;
    batchConfirming.value = false;
  },
  { flush: "sync" },
);

watch([currentId, () => accounts.switching], () => {
  clearPicks();
  confirming.value = false;
  batchConfirming.value = false;
}, { flush: "sync" });

const batchBar = ref<InstanceType<typeof BatchBar> | null>(null);
const batchHeight = ref(0);
const barResize = new ResizeObserver(([entry]) => {
  batchHeight.value = entry.borderBoxSize?.[0]?.blockSize ?? entry.target.getBoundingClientRect().height;
});
watch(batchBar, (bar) => {
  barResize.disconnect();
  batchHeight.value = 0;
  if (bar?.$el instanceof HTMLElement) {
    batchHeight.value = bar.$el.getBoundingClientRect().height;
    barResize.observe(bar.$el);
  }
}, { flush: "post" });

function onKeydown(event: KeyboardEvent) {
  if (
    event.defaultPrevented || ui.route !== "shares" || isTyping(event) || appLocked.value ||
    accounts.switching || accounts.dialog || ui.cloudAddOpen || ui.newFolderOpen || ui.accountsOpen ||
    document.querySelector('.scrim, [aria-modal="true"]')
  ) return;
  if (event.key === "Escape") {
    if (detailOpen.value) {
      event.preventDefault();
      void closeDetail();
    } else if (picked.value.length) {
      event.preventDefault();
      clearPicks();
    }
  } else if ((event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey && event.key.toLowerCase() === "a" && !pageShown.value) {
    event.preventDefault();
    pickAll();
  } else if (event.key === " " && !event.metaKey && !event.ctrlKey && !event.altKey) {
    const target = event.target;
    if (!(target instanceof HTMLElement) || !target.matches("tr[data-share]") || !area.value?.contains(target)) return;
    const share = rows.value.find((row) => row.share.id === target.dataset.share)?.share;
    if (!share || event.repeat) return;
    event.preventDefault();
    togglePick(share);
  }
}

async function confirmCancel() {
  const share = detail.value;
  if (!share || actionBusy.value || share.status === "canceled") return;
  actionBusy.value = true;
  try {
    await cancelShares([share]);
  } finally {
    confirming.value = false;
    actionBusy.value = false;
  }
}

async function confirmBatchCancel() {
  if (actionBusy.value || !batchConfirming.value || !cancelable.value.length) return;
  const items = [...cancelable.value];
  actionBusy.value = true;
  try {
    await cancelShares(items);
  } finally {
    batchConfirming.value = false;
    actionBusy.value = false;
  }
}

async function copyPickedLinks() {
  if (actionBusy.value || !selected.value.length) return;
  actionBusy.value = true;
  try {
    await copyShareLinks([...selected.value]);
  } finally {
    actionBusy.value = false;
  }
}

onMounted(() => {
  if (!shares.list.length) void loadShares();
  window.addEventListener("keydown", onKeydown);
});

onBeforeUnmount(() => {
  window.removeEventListener("keydown", onKeydown);
  barResize.disconnect();
});
</script>

<template>
  <section v-show="!pageShown" class="shares canvas">
    <header class="head">
      <div>
        <h1>{{ t("我的分享") }}</h1>
        <p>{{ t("管理已创建的分享") }}</p>
      </div>
      <button class="quiet refresh" :class="{ working: shares.loading }" :disabled="shares.loading" :title="t('刷新')" @click="loadShares">
        <Orbit v-if="shares.loading" :size="16" :label="t('刷新中')" />
        <Icon v-else name="restore" :size="16" />
        <span class="refresh-label">{{ t("刷新") }}</span>
      </button>
    </header>

    <div ref="area" class="list-area">
      <!-- The details page takes the canvas's place; the list keeps its
           selection and search, and its scroll comes back with it. -->
      <div ref="listEl" class="scroll" :class="{ selecting: multi }" :style="multi ? { paddingBottom: `${batchHeight + 28}px` } : undefined">
        <table class="rows">
          <thead>
            <tr>
              <th>{{ t("名称") }}</th>
              <th class="num views">{{ t("count|浏览") }}</th>
              <th class="num saves">{{ t("count|保存") }}</th>
              <th class="date">{{ t("分享时间") }}</th>
              <th class="state">{{ t("状态") }}</th>
              <th class="actions"><span class="sr-only">{{ t("操作") }}</span></th>
            </tr>
          </thead>
          <tbody v-if="firstLoad" class="late" aria-busy="true" :aria-label="t('正在加载')">
            <tr v-for="i in 4" :key="i" class="ghost" :style="{ '--i': i }">
              <td>
                <div class="name">
                  <span class="tile skel" />
                  <span class="skel ghost-line" :style="{ width: 30 + ((i * 29) % 40) + '%' }" />
                </div>
              </td>
              <td class="num views"><span class="skel ghost-line thin" style="width: 28px" /></td>
              <td class="num saves"><span class="skel ghost-line thin" style="width: 20px" /></td>
              <td class="date"><span class="skel ghost-line thin" style="width: 80px" /></td>
              <td class="state"><span class="skel ghost-line" style="width: 90px" /></td>
              <td class="actions" />
            </tr>
          </tbody>
          <tbody v-else>
            <tr
              v-for="{ share, status: rowStatus } in rows"
              :key="share.id"
              :data-share="share.id"
              :class="{ on: pickedSet.has(share.id) }"
              :aria-selected="pickedSet.has(share.id)"
              tabindex="0"
              @click="pick(share, $event)"
              @dblclick="onDoubleClick(share, $event)"
              @keydown.enter.self.prevent="select(share)"
            >
              <td>
                <div class="name">
                  <button
                    class="check"
                    :class="{ on: pickedSet.has(share.id) }"
                    role="checkbox"
                    :aria-checked="pickedSet.has(share.id)"
                    :aria-label="t('选择「{name}」', { name: share.title })"
                    @click.stop="togglePick(share)"
                    @dblclick.stop
                  >
                    <Icon name="check" :size="12" />
                  </button>
                  <span class="tile">
                    <Thumb v-if="share.thumbnail" :src="share.thumbnail" :alt="share.title" />
                    <Icon v-else :name="share.folder ? 'folder' : 'file'" :size="22" :filled="share.folder" />
                  </span>
                  <span class="name-text">
                    <strong :title="share.title">{{ share.title }}</strong>
                    <span class="pill inline-state" :class="rowStatus.tone">
                      <Icon :name="toneIcon[rowStatus.tone]" :size="12" />
                      {{ rowStatus.text }}
                    </span>
                  </span>
                </div>
              </td>
              <td class="num views">{{ share.views }}</td>
              <td class="num saves">{{ share.saves }}</td>
              <td class="date">{{ day(share.createdAt) }}</td>
              <td class="state">
                <span class="pill" :class="rowStatus.tone">
                  <Icon :name="toneIcon[rowStatus.tone]" :size="13" />
                  {{ rowStatus.text }}
                </span>
              </td>
              <td class="actions">
                <button
                  class="quiet open-detail"
                  :class="{ on: detail?.id === share.id }"
                  :aria-label="t('「{name}」的分享详情', { name: share.title })"
                  :title="t('分享详情')"
                  @click.stop="openDetail(share, $event.currentTarget)"
                  @dblclick.stop
                >
                  <Icon name="info" :size="16" />
                  <span class="detail-label">{{ t("详情") }}</span>
                </button>
              </td>
            </tr>
          </tbody>
        </table>

        <p v-if="!rows.length && !shares.loading" class="empty">
          <Icon name="shares" :size="26" />
          {{ ui.shareQuery.trim() ? t("没有匹配的分享") : t("还没有创建过分享") }}
        </p>

        <div v-if="hasMoreShares" class="more-row">
          <button :class="{ working: shares.loadingMore }" :disabled="shares.loadingMore" @click="loadMoreShares">
            <Orbit v-if="shares.loadingMore" :size="15" />
            {{ shares.loadingMore ? t("正在加载…") : t("加载更多") }}
          </button>
        </div>
      </div>
      <BatchBar
        v-if="multi"
        ref="batchBar"
        :count="selected.length"
        :all-picked="allPicked"
        :aria-busy="actionBusy"
        @pick-all="pickAll"
        @clear="clearPicks"
      >
        <ConfirmInline
          v-if="batchConfirming"
          :inert="actionBusy"
          :aria-busy="actionBusy"
          :message="t('取消 {n} 个分享？外部链接会立即失效，无法撤销。', { n: cancelable.length })"
          :confirm-label="t('确认取消分享')"
          :cancel-label="t('保留')"
          danger
          @cancel="batchConfirming = false"
          @confirm="confirmBatchCancel"
        />
        <template v-else>
          <button class="quiet" :disabled="actionBusy" @click="copyPickedLinks">
            <Icon name="copy" :size="16" />
            {{ t("复制链接") }}
          </button>
          <button
            class="quiet warn-hover"
            :disabled="actionBusy || !cancelable.length"
            :title="!cancelable.length ? t('所选分享均已取消') : undefined"
            @click="batchConfirming = true"
          >
            <Icon name="trash" :size="16" />
            {{ t("取消分享（{n}）", { n: cancelable.length }) }}
          </button>
        </template>
      </BatchBar>
    </div>
    <footer class="foot">
      <span class="tnum">{{ hasMoreShares ? t("已加载 {n} 项，还有更多", { n: rows.length }) : t("{n} 项", { n: rows.length }) }}</span>
      <span v-if="selected.length" class="picks">
        <span class="tnum">{{ t("已选 {n} 项", { n: selected.length }) }}</span>
        <button class="quiet clear" @click="clearPicks">{{ t("取消选择") }}</button>
      </span>
    </footer>
  </section>

  <!-- A column of App's content row beside the canvas, never over it. -->
  <aside
    v-if="detail"
    ref="detailEl"
    class="detail"
    :class="asPage ? 'canvas page' : 'glass glass-sheet'"
    :aria-label="t('分享详情')"
    tabindex="-1"
  >
    <header class="detail-head">
      <button v-if="asPage" class="quiet back" @click="closeDetail">
        <Icon name="arrowLeft" :size="18" />
        {{ t("返回分享列表") }}
      </button>
      <template v-else>
        <Icon name="info" :size="18" />
        <strong>{{ t("分享详情") }}</strong>
        <button class="quiet icon" :title="t('关闭')" :aria-label="t('关闭分享详情')" @click="closeDetail"><Icon name="close" :size="16" /></button>
      </template>
    </header>

    <div class="detail-body">
      <!-- A picture only when the share has one; a folder or a file
           without one keeps its small type icon, not an empty frame. -->
      <div v-if="detail.thumbnail" class="hero">
        <Thumb :src="detail.thumbnail" :alt="detail.title" />
      </div>
      <div class="ident">
        <span v-if="!detail.thumbnail" class="tile">
          <Icon :name="detail.folder ? 'folder' : 'file'" :size="22" :filled="detail.folder" />
        </span>
        <div class="ident-text">
          <h2>{{ detail.title }}</h2>
          <p class="stats">
            {{ t("浏览 {views} · 保存 {saves}", { views: detail.views, saves: detail.saves }) }}
            <template v-if="detail.fileSize"><i>·</i>{{ bytes(detail.fileSize) }}</template>
          </p>
        </div>
      </div>

      <dl class="facts">
        <div>
          <dt><Icon name="clock" :size="17" />{{ t("分享状态") }}</dt>
          <dd><span class="pill" :class="detailStatus?.tone">{{ detailStatus?.text }}</span></dd>
        </div>
        <div>
          <dt><Icon name="calendar" :size="17" />{{ t("分享时间") }}</dt>
          <dd>{{ day(detail.createdAt) }}</dd>
        </div>
        <div>
          <dt><Icon name="calendar" :size="17" />{{ t("有效期") }}</dt>
          <dd>{{ expiry }}</dd>
        </div>
        <div>
          <dt><Icon name="lock" :size="17" />{{ t("提取码") }}</dt>
          <dd :class="{ mono: detail.passCode }">{{ detail.passCode || t("无提取码") }}</dd>
        </div>
        <div>
          <dt><Icon name="link" :size="17" />{{ t("分享链接") }}</dt>
          <dd>{{ detail.status === "active" ? t("已创建") : t("已失效") }}</dd>
        </div>
      </dl>

      <div class="link glass-well">
        <span class="masked mono" :title="detail.url">{{ detail.url.replace(/^https?:\/\//, "") }}</span>
        <button class="quiet text" :disabled="actionBusy" @click="copyShareLink(detail)">
          <Icon name="copy" :size="15" />
          {{ t("复制链接") }}
        </button>
      </div>
      <p v-if="detail.status !== 'active'" class="note">
        {{ t("这个分享已不可访问；复制的是旧链接，打开后会提示失效。取消分享不会恢复它。") }}
      </p>
    </div>

    <footer class="detail-foot">
      <ConfirmInline
        v-if="confirming"
        :inert="actionBusy"
        :aria-busy="actionBusy"
        :message="t('取消「{name}」的分享？外部链接会立即失效，无法撤销。', { name: detail.title })"
        :confirm-label="t('确认取消分享')"
        :cancel-label="t('保留')"
        danger
        @cancel="confirming = false"
        @confirm="confirmCancel"
      />
      <button v-else class="cancel" :disabled="actionBusy || detail.status === 'canceled'" @click="confirming = true">
        <Icon name="trash" :size="17" />
        {{ t("取消分享") }}
      </button>
    </footer>
  </aside>
</template>

<style scoped>
.shares {
  /* The canvas narrows when the nav, the playback bar or the details column
     takes its share of the row, so the table sizes itself by the canvas,
     not by the window. */
  container-type: inline-size;
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  padding: 22px 20px 10px;
  overflow: hidden;
}

.head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 18px;
}

.head > div {
  min-width: 0;
}

.head h1 {
  margin: 0;
  font-size: 28px;
  font-weight: 600;
  line-height: 36px;
  letter-spacing: 0.01em;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.head p {
  margin: 4px 0 0;
  font-size: 15px;
  color: var(--fg-dim);
}

.refresh {
  flex: none;
  font-size: 14px;
  font-weight: 500;
}

/* ---------- table ---------- */

/* The list under the heading; only it scrolls. */
.list-area {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-bottom: 20px;
}

/* Match the file rows: an 18px ring with a 40px target beside the thumbnail. */
.rows .check {
  position: relative;
  flex: none;
  width: 18px;
  height: 18px;
  min-height: 18px;
  padding: 0;
  display: grid;
  place-items: center;
  border: 1.5px solid var(--fg-dim);
  border-radius: 50%;
  color: transparent;
  background: transparent;
  box-shadow: none;
  cursor: pointer;
  opacity: 0;
  transition: opacity var(--dur) var(--ease);
}

.check::before {
  content: "";
  position: absolute;
  inset: calc(50% - 20px);
  border-radius: 50%;
}

.rows tbody tr:hover .check,
.rows tbody tr:focus-within .check,
.selecting .check,
.rows .check.on {
  opacity: 1;
}

.rows .check.on {
  color: var(--on-primary);
  border-color: transparent;
  background: var(--primary) border-box;
}

.rows .check:hover:not(:disabled) {
  background: transparent;
}

.rows .check.on:hover:not(:disabled) {
  background: var(--primary) border-box;
}

@media (hover: none), (pointer: coarse) {
  .rows .check {
    opacity: 1;
  }
}

.foot {
  flex: none;
  min-height: 40px;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0 12px;
  margin: 0 -20px -10px;
  padding: 0 20px;
  border-top: 1px solid var(--line);
  font-size: 12px;
  color: var(--fg-dim);
}

.picks {
  display: flex;
  align-items: center;
  gap: 8px;
}

.picks .clear {
  min-height: 40px;
  margin: 0 -8px;
  padding: 0 16px;
  font-size: 12px;
  color: var(--accent);
}

/* Auto layout gives every non-name column at least its configured width and
   lets localized labels widen it. The name column takes the remainder and
   ellipsizes instead of widening the table. */
.rows {
  width: 100%;
  table-layout: auto;
  border-collapse: separate;
  border-spacing: 0;
  font-size: 15px;
}

.rows th,
.rows td {
  text-align: left;
  padding: 0 12px;
  border-bottom: 1px solid var(--line);
  vertical-align: middle;
}

.rows tr > :first-child {
  max-width: 0;
}

.rows th,
.rows td.date {
  white-space: nowrap;
}

.rows th {
  position: sticky;
  top: 0;
  z-index: 1;
  height: 36px;
  font-weight: 500;
  font-size: 13px;
  color: var(--fg-dim);
  background: color-mix(in srgb, var(--fg) 4%, transparent);
  border-top: 1px solid var(--line);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
}

.rows th:first-child {
  border-top-left-radius: 12px;
}

.rows th:last-child {
  border-top-right-radius: 12px;
}

.rows td {
  height: 72px;
}

.rows tbody tr {
  cursor: default;
  outline: none;
}

.rows tbody tr:hover td {
  background: color-mix(in srgb, var(--fg) 3%, transparent);
}

.rows tbody tr.on td {
  background: var(--accent-soft);
}

.rows tbody tr.on td:first-child {
  box-shadow: inset 3px 0 0 var(--accent);
}

.rows tbody tr:focus-visible td {
  background: var(--accent-soft);
}

.num {
  width: 72px;
  font-variant-numeric: tabular-nums;
}

.date {
  width: 120px;
  color: var(--fg-dim);
}

.state {
  width: 132px;
}

.name {
  display: flex;
  align-items: center;
  gap: 16px;
  min-width: 0;
}

.name-text {
  min-width: 0;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 5px;
}

.name strong {
  max-width: 100%;
  font-size: 15px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* The status under the name, only once its own column has given way. Two
   classes, so it outranks .pill's display further down. */
.pill.inline-state {
  display: none;
}

.tile {
  position: relative;
  width: 56px;
  height: 56px;
  flex: none;
  border-radius: 10px;
  display: grid;
  place-items: center;
  overflow: hidden;
  background: var(--accent-soft);
  color: var(--accent);
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--accent) 18%, transparent);
}

.tile :deep(img) {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 30px;
  padding: 0 12px;
  border-radius: 999px;
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
}

.pill.ok {
  color: var(--success);
  background: color-mix(in srgb, var(--success) 16%, transparent);
}

.pill.warn {
  color: var(--warning, #f0b35b);
  background: color-mix(in srgb, var(--warning, #f0b35b) 16%, transparent);
}

.pill.off {
  color: var(--fg-dim);
  background: color-mix(in srgb, var(--fg) 8%, transparent);
}

.ghost-line {
  display: inline-block;
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
  gap: 10px;
  padding: 56px 0;
  color: var(--fg-dim);
  font-size: 15px;
}

.more-row {
  display: flex;
  justify-content: center;
  padding: 12px 0;
}

/* The row's own way to its details; quiet until the row is looked at. */
.actions {
  width: 96px;
}

.rows td.actions {
  padding-right: 8px;
  text-align: right;
}

.open-detail {
  gap: 6px;
  min-height: 36px;
  padding: 0 12px;
  font-size: 13px;
  font-weight: 500;
  white-space: nowrap;
}

.open-detail.on,
.rows tbody tr.on .open-detail,
.rows tbody tr:hover .open-detail {
  color: var(--accent);
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}

/* ---------- the details column ---------- */

/* A column of the content row beside the canvas, the canvas's height, like
   the file page's context column and the transfer details: the canvas gives
   up the width, so no row is ever covered and the list keeps its scroll.
   Only the body scrolls; the footer stays at the bottom. Its fill is the
   reading glass (--glass-panel), the context column's. */
.detail {
  flex: none;
  width: var(--inspector-w);
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--glass-panel);
  outline: none;
  /* A slide, no fade: an animation WebKit holds at its first frame (it does
     in a window it is not drawing) must not leave the details invisible. */
  animation: detail-in var(--dur) var(--ease);
}

@media (prefers-reduced-transparency: reduce) {
  .detail {
    background: var(--canvas);
  }
}

@keyframes detail-in {
  from {
    transform: translateX(22px);
  }
}

/* Under 960px (the file page's Files / Info pages): a page on the reading
   canvas in the list's place, its column centred. It fills the row where
   the canvas has a height of its own (the body scrolls) and is as tall as
   its content where the workspace scrolls instead. */
.detail.page {
  flex: 1 1 auto;
  width: auto;
  min-width: 0;
  background: var(--canvas);
  animation: none;
}

.detail.page > * {
  width: 100%;
  max-width: 520px;
  margin: 0 auto;
}

@media (prefers-reduced-motion: reduce) {
  .detail {
    animation: none;
  }
}

.detail-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 18px 16px 12px 20px;
  font-size: 16px;
}

.detail-head strong {
  flex: 1;
  font-weight: 600;
}

.detail-head .back {
  gap: 8px;
  margin-left: -8px;
  padding: 0 12px 0 8px;
  font-size: 15px;
  font-weight: 600;
  color: var(--fg);
}

.detail-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: 0 20px 16px;
}

/* A real picture, in a media frame; nothing stands in for a missing one. */
.hero {
  position: relative;
  aspect-ratio: 16 / 9;
  margin-bottom: 14px;
  border-radius: 14px;
  overflow: hidden;
  background: var(--glass-inset);
}

.hero :deep(img) {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.ident {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  margin-bottom: 14px;
}

.ident .tile {
  width: 44px;
  height: 44px;
}

.ident-text {
  flex: 1;
  min-width: 0;
}

/* The details give the whole name: it wraps, links and names without
   spaces included, instead of running out of the sheet. */
.detail-body h2 {
  margin: 0 0 6px;
  font-size: 18px;
  font-weight: 600;
  line-height: 1.35;
  overflow-wrap: anywhere;
}

.stats {
  margin: 0;
  font-size: 14px;
  color: var(--fg-dim);
}

.stats i {
  font-style: normal;
  margin: 0 6px;
  opacity: 0.6;
}

.facts {
  margin: 0;
  padding: 10px 0;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
}

.facts > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 44px;
}

.facts dt {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 14px;
  color: var(--fg-dim);
}

.facts dd {
  margin: 0;
  font-size: 14px;
  font-weight: 500;
}

.link {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 14px;
  padding: 6px 6px 6px 14px;
  border-radius: 12px;
}

.link .masked {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
  color: var(--fg-dim);
}

.link .text {
  color: var(--accent);
  font-weight: 600;
  white-space: nowrap;
}

.note {
  margin: 12px 0 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--fg-dim);
}

.detail-foot {
  padding: 12px 20px 18px;
  border-top: 1px solid var(--line);
}

.cancel {
  width: 100%;
  min-height: 48px;
  gap: 10px;
  font-size: 15px;
  font-weight: 600;
}

/* The footer already has its hairline; the buttons fill its width. */
.detail-foot .confirm {
  padding-top: 0;
  border-top: 0;
}

.detail-foot :deep(.confirm-actions button) {
  flex: 1;
  min-height: 42px;
}

/* Fixed columns must leave enough room for the name tile and title. Less
   important counts and dates disappear first; status and detail access remain. */
@container (max-width: 760px) {
  .saves {
    display: none;
  }
}

@container (max-width: 690px) {
  .views {
    display: none;
  }
}

@container (max-width: 620px) {
  .date {
    display: none;
  }
}

@container (max-width: 470px) {
  .actions {
    width: 52px;
  }

  .open-detail {
    padding: 0 10px;
  }

  .detail-label {
    display: none;
  }
}

/* The narrowest canvas — a narrow window, a large zoom — has no room for a
   status column beside the name. The table becomes a list: a smaller tile,
   the name, and its status pill under it; the header keeps its title on
   one line and the refresh button drops its label. */
@container (max-width: 380px) {
  .head {
    margin-bottom: 12px;
  }

  .head h1 {
    font-size: 22px;
    line-height: 30px;
  }

  .head p,
  .refresh-label,
  .state {
    display: none;
  }

  .rows th,
  .rows td {
    padding: 0 8px;
  }

  .name {
    gap: 10px;
  }

  .name strong {
    font-size: 14px;
  }

  .tile {
    width: 40px;
    height: 40px;
    border-radius: 8px;
  }

  .pill.inline-state {
    display: inline-flex;
    min-height: 22px;
    padding: 0 8px;
    gap: 4px;
    font-size: 12px;
  }
}
</style>
