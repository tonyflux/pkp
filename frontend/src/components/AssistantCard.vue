<script setup lang="ts">
// One result card in a conversation turn (agent.ts Card): a plan summary
// that opens the full plan, local results that can be located or opened,
// a folder's 「What's Here」, and the Cloud Add / Download / Share proposals that do
// nothing until Confirm is clicked here. Rows keep their real ids on this
// computer; none of this was sent anywhere.
import { computed, ref } from "vue";
import Icon from "./Icon.vue";
import AssistantOperations from "./AssistantOperations.vue";
import { bytes, stamp } from "../format";
import { t, num } from "../i18n";
import { classify } from "../media";
import type { Card, CardRow, CardState } from "../agent";
import { assistant, discardCard, openPlanCard, revealFile, runCard, viewFile } from "../store";

const props = defineProps<{ card: Card; turnId: string; number: number; live?: boolean; displayedCount?: number }>();

const STATE_LABEL: Record<CardState, () => string> = {
  draft: () => t("待你确认"),
  running: () => t("正在执行"),
  done: () => t("已执行"),
  failed: () => t("未成功"),
  cancelled: () => t("已放弃"),
  undone: () => t("已撤销"),
};

const stampLabel = computed(() => STATE_LABEL[props.card.state]?.() ?? "");
const locked = computed(() => !!props.live || assistant.running || !!assistant.busy);

/* ---------- plan ---------- */

const SOURCE_NOTE: Record<string, () => string> = {
  tmdb: () => t("TMDB 只做精确匹配，不发给模型"),
  rules: () => t("本地规则生成，未请求模型"),
  model: () => t("模型只提出草案，执行前由你确认"),
  "": () => t("模型只提出草案，执行前由你确认"),
};

const planHeadline = computed(() => {
  const c = props.card;
  if (c.kind !== "plan") return "";
  const n = c.counts;
  if (!n.changes) return n.kept ? t("没有可整理的项，{n} 项保持原样", { n: n.kept }) : t("没有需要改动的项");
  return n.kept ? t("{n} 项可整理，{m} 项暂不动", { n: n.changes, m: n.kept }) : t("{n} 项可整理", { n: n.changes });
});

/* ---------- rows ---------- */

const ROW_PREVIEW = 6;
const expanded = ref(false);

function rowsOf(card: Card): CardRow[] {
  return card.kind === "files" || card.kind === "download" || card.kind === "share" ? card.rows : [];
}

const rows = computed(() => rowsOf(props.card));
const shown = computed(() => (expanded.value ? rows.value : rows.value.slice(0, ROW_PREVIEW)));

function rowIcon(row: CardRow): string {
  if (row.folder) return "folder";
  const kind = classify({ name: row.name, category: "" });
  return kind === "video" ? "film" : kind === "subtitle" ? "subtitles" : kind === "audio" ? "audio" : "file";
}

function rowMeta(row: CardRow): string {
  return [row.path ? row.path.split("/").join(" / ") : t("全部文件"), row.folder ? "" : bytes(row.size, ""), stamp(row.modified, ""), row.played]
    .filter(Boolean)
    .join(" · ");
}

function locate(row: CardRow) {
  void revealFile({ account: assistant.account, fileId: row.id });
}

function open(row: CardRow) {
  void viewFile({ account: assistant.account, fileId: row.id });
}

/* ---------- stats ---------- */

const CATEGORY_LABEL: Record<string, () => string> = {
  video: () => t("视频"),
  image: () => t("图片"),
  audio: () => t("音频"),
  document: () => t("文档"),
  archive: () => t("压缩包"),
  other: () => t("其他"),
};

const categories = computed(() => {
  const c = props.card;
  if (c.kind !== "stats" || !c.stats) return [];
  const list = (c.stats.categories ?? []).filter((x) => x.count > 0);
  const top = Math.max(1, ...list.map((x) => x.size));
  return list.map((x) => ({ ...x, label: CATEGORY_LABEL[x.category]?.() ?? x.category, share: Math.max(2, Math.round((x.size / top) * 100)) }));
});

const proposalVerb = computed(() =>
  props.card.kind === "offline" ? t("确认添加") : props.card.kind === "download" ? t("确认下载") : props.card.kind === "share" ? t("确认创建分享") : t("确定"),
);
</script>

<template>
  <!-- Draft plan -->
  <section v-if="card.kind === 'plan'" class="ai-card plan-card" :class="`is-${card.state}`">
    <div class="plan-head">
      <div class="plan-title">
        <span class="kicker">{{ t("计划草案") }} / {{ String(number).padStart(2, "0") }}</span>
        <h2>{{ planHeadline }}</h2>
        <p>{{ card.title }}</p>
      </div>
      <span class="stamp" :class="card.state">{{ stampLabel }}</span>
    </div>
    <div class="plan-counts">
      <div>
        <strong class="tnum">{{ num(card.counts.changes) }}</strong>
        <span>{{ card.tool === "organize_media" ? t("移动并规范命名") : t("移动或改名") }}</span>
      </div>
      <div>
        <strong class="tnum">{{ num(card.counts.kept) }}</strong>
        <span>{{ t("无法确认 · 保持原样") }}</span>
      </div>
      <div>
        <strong class="tnum">{{ num(card.counts.trash) }}</strong>
        <span>{{ card.counts.trash ? t("移到回收站") : t("删除") }}</span>
      </div>
    </div>
    <p v-if="card.counts.conflicts" class="conflicts">{{ t("{n} 项路径冲突，需在完整计划里取消或改名", { n: card.counts.conflicts }) }}</p>
    <div v-if="card.sample" class="sample">
      <span class="file-icon"><Icon name="film" :size="19" /></span>
      <div>
        <strong>{{ card.sample.from }}</strong>
        <small>→ {{ card.sample.to }}</small>
      </div>
    </div>
    <p v-if="card.note" class="card-note">{{ card.note }}</p>
    <div class="plan-foot">
      <span><Icon name="shield" :size="14" /> {{ (SOURCE_NOTE[card.source] ?? SOURCE_NOTE[""])() }}</span>
      <button class="primary go" type="button" :disabled="!!live" @click="openPlanCard(card.id)">
        {{ card.state === "draft" ? t("查看完整计划") : t("查看计划") }}
        <Icon name="arrowRight" :size="15" />
      </button>
    </div>
  </section>
  <AssistantOperations v-else-if="card.kind === 'operation' || card.kind === 'resources'" :card="card" :turn-id="turnId" :live="live" />

  <!-- Local results / Downloads / Shares -->
  <section v-else-if="(card.kind === 'files' && (card.rows.length || card.aggregate.some(row => row.count > 0))) || card.kind === 'download' || card.kind === 'share'" class="ai-card rows-card">
    <div class="card-head">
      <div>
        <span class="kicker">{{ card.kind === "files" ? t("本地索引") : card.kind === "download" ? t("下载建议") : t("分享建议") }}</span>
        <h3>{{ card.title }}</h3>
      </div>
      <span v-if="card.kind !== 'files'" class="stamp" :class="card.state">{{ stampLabel }}</span>
      <span v-else class="count tnum">{{ displayedCount === undefined ? t("{n} 项", { n: card.total }) : t("展示 {n} 项", { n: displayedCount }) }}</span>
    </div>
    <slot name="metadata" />
    <ul class="rows">
      <li v-for="row in shown" :key="row.id">
        <span class="file-icon small"><Icon :name="rowIcon(row)" :size="16" /></span>
        <span class="row-copy">
          <strong :title="row.name">{{ row.name }}</strong>
          <small>{{ rowMeta(row) }}</small>
        </span>
        <span class="row-actions">
          <button class="quiet" type="button" @click="locate(row)">{{ t("定位") }}</button>
          <button class="quiet" type="button" @click="open(row)">{{ t("打开") }}</button>
        </span>
      </li>
    </ul>
    <button v-if="rows.length > ROW_PREVIEW" class="more" type="button" :aria-expanded="expanded" @click="expanded = !expanded">
      {{ expanded ? t("收起 ↑") : t("查看其余 {n} 项 ↓", { n: rows.length - ROW_PREVIEW }) }}
    </button>
    <table v-if="card.kind === 'files' && card.aggregate.length" class="aggregate">
      <thead>
        <tr><th>{{ t("分组") }}</th><th class="num">{{ t("数量") }}</th><th class="num">{{ t("大小") }}</th></tr>
      </thead>
      <tbody>
        <tr v-for="a in card.aggregate" :key="a.key">
          <td>{{ a.label }}</td>
          <td class="num tnum">{{ num(a.count) }}</td>
          <td class="num tnum">{{ bytes(a.size, "0 B", 1) }}</td>
        </tr>
      </tbody>
    </table>
    <p v-if="card.note" class="card-note">{{ card.note }}</p>
    <div v-if="card.kind !== 'files' && card.state === 'draft'" class="card-actions">
      <button class="quiet" type="button" :disabled="locked" @click="discardCard(turnId, card.id)">{{ t("放弃") }}</button>
      <button class="primary go" type="button" :disabled="locked" @click="runCard(turnId, card.id)">{{ proposalVerb }}</button>
    </div>
    <slot name="actions" />
  </section>

  <!-- Add from cloud -->
  <section v-else-if="card.kind === 'offline'" class="ai-card offline-card">
    <div class="card-head">
      <div>
        <span class="kicker">{{ t("云添加建议") }}</span>
        <h3>{{ card.title }}</h3>
      </div>
      <span class="stamp" :class="card.state">{{ stampLabel }}</span>
    </div>
    <ul class="links">
      <li v-for="url in card.urls.slice(0, 4)" :key="url" :title="url">{{ url }}</li>
      <li v-if="card.urls.length > 4" class="dim">{{ t("另有 {n} 个链接", { n: card.urls.length - 4 }) }}</li>
      <li v-if="card.taskIds.length" class="dim">{{ t("重新添加 {n} 个之前的云添加任务", { n: card.taskIds.length }) }}</li>
    </ul>
    <p class="card-line"><Icon name="folder" :size="14" /> {{ t("保存到 {path}", { path: card.path ? card.path.split("/").join(" / ") : t("全部文件") }) }}</p>
    <p v-if="card.note" class="card-note">{{ card.note }}</p>
    <div v-if="card.state === 'draft'" class="card-actions">
      <button class="quiet" type="button" :disabled="locked" @click="discardCard(turnId, card.id)">{{ t("放弃") }}</button>
      <button class="primary go" type="button" :disabled="locked" @click="runCard(turnId, card.id)">{{ proposalVerb }}</button>
    </div>
  </section>

  <!-- What's here -->
  <section v-else-if="card.kind === 'stats'" class="ai-card stats-card">
    <div class="card-head">
      <div>
        <span class="kicker">{{ t("这里都有什么") }}</span>
        <h3>{{ card.path ? card.path.split("/").join(" / ") : t("整个云盘") }}</h3>
      </div>
    </div>
    <template v-if="card.stats">
      <p class="card-line tnum">
        {{ t("{files} 个文件 · {folders} 个文件夹 · {size}", { files: card.stats.files, folders: card.stats.folders, size: bytes(card.stats.size, "0 B", 1) }) }}
      </p>
      <ul class="bars">
        <li v-for="c in categories" :key="c.category">
          <span class="bar-label">{{ c.label }}</span>
          <span class="bar" aria-hidden="true"><i :style="{ width: `${c.share}%` }" /></span>
          <span class="bar-figure tnum">{{ num(c.count) }} · {{ bytes(c.size, "0 B", 1) }}</span>
        </li>
      </ul>
      <p v-if="card.stats.dupGroups" class="card-line">{{ t("重复 {n} 组，多出 {copies} 份，可回收 {size}", { n: card.stats.dupGroups, copies: card.stats.dupCopies, size: bytes(card.stats.dupWaste, "0 B", 1) }) }}</p>
      <p v-if="card.stats.unfinished" class="card-line">{{ t("没看完的视频 {n} 个", { n: card.stats.unfinished }) }}</p>
      <ul v-if="card.stats.largest?.length" class="rows">
        <li v-for="row in card.stats.largest" :key="row.id">
          <span class="file-icon small"><Icon :name="row.folder ? 'folder' : 'file'" :size="16" /></span>
          <span class="row-copy">
            <strong :title="row.name">{{ row.name }}</strong>
            <small class="tnum">{{ bytes(row.size, "0 B", 1) }}</small>
          </span>
        </li>
      </ul>
    </template>
    <p v-if="card.note" class="card-note">{{ card.note }}</p>
  </section>
</template>

<style scoped>
.ai-card {
  min-width: 0;
  margin: 0 0 14px;
  border: 1px solid var(--line-strong);
  border-radius: 16px;
  background: color-mix(in srgb, var(--fg) 4%, transparent);
  overflow: hidden;
}

.plan-title h2,
.plan-title p,
.plan-counts strong,
.plan-counts span,
.conflicts,
.sample strong,
.sample small,
.card-head h3,
.row-copy strong,
.row-copy small,
.aggregate th,
.aggregate td,
.links li,
.card-line,
.card-note,
.plan-foot > span,
.bar-label,
.bar-figure {
  -webkit-user-select: text;
  user-select: text;
  --default-contextmenu: show;
}

.kicker {
  font-size: 10px;
  font-weight: 650;
  letter-spacing: 0.12em;
  color: var(--accent);
}

.stamp {
  flex: none;
  align-self: flex-start;
  font-size: 11px;
  color: var(--fg-dim);
  white-space: nowrap;
}

.stamp.draft {
  color: var(--warning);
}

.stamp.done {
  color: var(--success);
}

.stamp.failed {
  color: var(--danger);
}

.file-icon {
  display: grid;
  place-items: center;
  flex: none;
  width: 36px;
  height: 36px;
  border-radius: 9px;
  color: var(--accent);
  background: var(--accent-soft);
}

.file-icon.small {
  width: 30px;
  height: 30px;
  border-radius: 8px;
}

.go {
  min-height: 39px;
  padding: 0 15px;
  border-radius: 12px;
  font-size: 12px;
}

.card-note {
  margin: 0;
  padding: 0 20px 12px;
  font-size: 11px;
  line-height: 1.6;
  color: var(--fg-dim);
  overflow-wrap: anywhere;
}

/* ---------- plan ---------- */

.plan-head {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 18px 20px 14px;
}

.plan-title {
  min-width: 0;
}

.plan-title h2 {
  margin: 4px 0;
  font-size: 17px;
  font-weight: 620;
  overflow-wrap: anywhere;
}

.plan-title p {
  margin: 0;
  font-size: 11px;
  color: var(--fg-dim);
  overflow-wrap: anywhere;
}

.plan-counts {
  display: flex;
  padding: 0 20px 17px;
}

.plan-counts div {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  padding-left: 18px;
  border-right: 1px solid var(--line);
}

.plan-counts div:first-child {
  padding-left: 0;
}

.plan-counts div:last-child {
  border-right: 0;
}

.plan-counts strong {
  font-size: 20px;
  line-height: 1.2;
  font-weight: 610;
}

.plan-counts span {
  margin-top: 2px;
  font-size: 10px;
  line-height: 1.35;
  color: var(--fg-dim);
  text-wrap: balance;
}

.conflicts {
  margin: 0;
  padding: 0 20px 12px;
  font-size: 11px;
  color: var(--danger);
}

.sample {
  display: flex;
  align-items: center;
  gap: 11px;
  min-width: 0;
  padding: 12px 20px;
  border-top: 1px solid var(--line);
  background: color-mix(in srgb, var(--fg) 4%, transparent);
}

.sample div {
  min-width: 0;
}

.sample strong,
.sample small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 11px;
}

.sample small {
  margin-top: 2px;
  font-size: 10px;
  color: var(--fg-dim);
}

.sample + .card-note {
  padding-top: 10px;
}

.plan-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding: 12px 20px;
}

.plan-foot > span {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  font-size: 10px;
  color: var(--fg-dim);
}

.plan-foot > span svg {
  flex: none;
}

/* ---------- rows ---------- */

.card-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 14px;
  padding: 16px 20px 10px;
}

.card-head > div {
  min-width: 0;
}

.card-head h3 {
  margin: 4px 0 0;
  font-size: 15px;
  font-weight: 610;
  overflow-wrap: anywhere;
}

.count {
  flex: none;
  font-size: 11px;
  color: var(--fg-dim);
}

.rows {
  margin: 0;
  padding: 0;
  list-style: none;
  border-top: 1px solid var(--line);
}

.rows li {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 54px;
  padding: 8px 20px;
  border-bottom: 1px solid var(--line);
}

.row-copy {
  flex: 1;
  min-width: 0;
}

.row-copy strong,
.row-copy small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row-copy strong {
  font-size: 12px;
  font-weight: 560;
}

.row-copy small {
  margin-top: 2px;
  font-size: 11px;
  color: var(--fg-dim);
}

.row-actions {
  flex: none;
  display: flex;
  gap: 2px;
}

.row-actions button {
  min-height: 30px;
  padding: 2px 9px;
  border-radius: 8px;
  font-size: 11px;
  color: var(--accent);
}

.more {
  width: 100%;
  justify-content: flex-start;
  min-height: 40px;
  padding: 0 20px 0 62px;
  border: 0;
  border-radius: 0;
  background: transparent;
  font-size: 11px;
  color: var(--accent);
}

.more:hover:not(:disabled) {
  border: 0;
  background: var(--accent-soft);
}

.aggregate {
  width: calc(100% - 40px);
  margin: 12px 20px;
  border-collapse: collapse;
  font-size: 12px;
}

.aggregate th,
.aggregate td {
  padding: 6px 0;
  border-bottom: 1px solid var(--line);
  text-align: left;
  overflow-wrap: anywhere;
}

.aggregate th {
  font-weight: 500;
  color: var(--fg-dim);
}

.aggregate .num {
  padding-left: 12px;
  text-align: right;
  white-space: nowrap;
}

.card-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 12px 20px;
}

.card-actions .quiet {
  min-height: 39px;
  border-radius: 12px;
  font-size: 12px;
}

/* ---------- offline / stats ---------- */

.links {
  margin: 0;
  padding: 4px 20px 8px;
  list-style: none;
  font-size: 12px;
}

.links li {
  padding: 3px 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.links .dim {
  color: var(--fg-dim);
}

.card-line {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  padding: 0 20px 10px;
  font-size: 12px;
  color: var(--fg-dim);
  overflow-wrap: anywhere;
}

.card-line svg {
  flex: none;
}

.bars {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr) auto;
  column-gap: 10px;
  margin: 0;
  padding: 0 20px 12px;
  list-style: none;
}

.bars li {
  display: grid;
  grid-column: 1 / -1;
  grid-template-columns: subgrid;
  align-items: center;
  padding: 4px 0;
  font-size: 12px;
}

.bar {
  height: 6px;
  border-radius: 3px;
  background: color-mix(in srgb, var(--fg) 8%, transparent);
  overflow: hidden;
}

.bar i {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--accent);
}

.bar-figure {
  color: var(--fg-dim);
  white-space: nowrap;
}

@media (max-width: 550px) {
  .plan-head,
  .sample,
  .plan-foot {
    padding-left: 12px;
    padding-right: 12px;
  }

  .plan-head {
    gap: 6px;
  }

  .plan-title h2 {
    font-size: 14px;
  }

  .plan-counts {
    padding: 0 12px 16px;
  }

  .plan-counts div {
    padding-left: 7px;
  }

  .plan-counts strong {
    font-size: 17px;
  }

  .plan-foot {
    flex-wrap: wrap;
  }

  .plan-foot .go {
    margin-left: auto;
  }

  .rows li {
    flex-wrap: wrap;
    padding: 8px 12px;
  }

  .row-actions {
    margin-left: 42px;
  }

  .card-head,
  .links,
  .card-line,
  .bars,
  .card-note {
    padding-left: 12px;
    padding-right: 12px;
  }
}
</style>
