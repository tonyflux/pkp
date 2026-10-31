<script setup lang="ts">
// Full Plan: every change of the
// plan, by where it ends up — one card per destination folder, three rows
// shown and the rest a click away — or by where it is now. Each row keeps its
// own tick and Rename; junk proposed for the trash is unticked with its
// reason; what stays and why is listed too. The dock at the canvas foot says
// what a run would do and leads to Final Confirmation; nothing changes on the drive
// before Confirm and Run. Long plans draw only the rows near the visible part
// (windowing.ts): the cards are drawn row by row, each row its own slice of
// the card.
import { computed, nextTick, ref } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import SelectMenu, { type SelectOption } from "./SelectMenu.vue";
import { bytes } from "../format";
import { t, num } from "../i18n";
import type { PlanSeries } from "../media";
import { nameError, type Change } from "../organizer";
import { templateError } from "../templates";
import { useRowWindows } from "../windowing";
import { vSlide } from "../slide";
import {
  accountName,
  aiReady,
  aiSettings,
  assistant,
  identifyRemaining,
  openAISettings,
  openPlanConfirm,
  pickPlanItems,
  planBackTo,
  planErrors,
  planSummary,
  planReviewed,
  isPlanReviewed,
  keepPlanItems,
  keepAllPlanItems,
  renamePlanItem,
  setPlanNaming,
  stopPlanRun,
  submitComposer,
  togglePlanYear,
} from "../store";

/** How many rows a card shows before 「View Remaining N Items」. */
const FOLD = 3;

type CardTone = "target" | "trash" | "empty" | "kept" | "source";

type Row =
  | {
      kind: "head";
      key: string;
      tone: CardTone;
      title: string;
      meta: string;
      pill: string;
      ids: string[];
      reviewIds?: string[];
      on: number;
      series: PlanSeries | null;
      movie: boolean;
      first: boolean;
      last: boolean;
    }
  | { kind: "change"; key: string; change: Change; error: string; detail: string; op: string; last: boolean; reviewed?: boolean }
  | { kind: "line"; key: string; change: Change; target: string; last: boolean }
  | { kind: "more"; key: string; card: string; hidden: number; open: boolean; label: string };

const plan = computed(() => assistant.plan);
const summary = computed(() => planSummary.value);
const view = ref<"target" | "source">("target");
const media = computed(() => plan.value?.kind === "media");
// Fold state by card key; a card opens on its own while it holds a conflict.
const opened = ref<Record<string, boolean>>({});

function toggle(key: string, open: boolean) {
  opened.value = { ...opened.value, [key]: !open };
}

const SOURCE_LABEL: Record<string, () => string> = {
  tmdb: () => t("TMDB 唯一匹配"),
  model: () => t("模型识别 · 待你确认"),
  rules: () => t("文件名规则识别"),
};

function tickState(list: readonly Change[]) {
  const open = list.filter((c) => !c.locked);
  return { ids: open.map((c) => c.item.id), on: open.filter((c) => c.selected).length };
}

// scopeName is data (All Files in Chinese for the root); the page names the root in its own language.
const scopeLabel = computed(() => (plan.value?.scope.length ? plan.value.scopeName : t("全部文件")));

function sourcePath(c: Change): string {
  return [scopeLabel.value, ...c.from].filter(Boolean).join(" / ");
}

// Two files that become the same episode in different qualities: the plan keeps both.
const VERSION = /(?:^|[\s._\-[(])(?:\d{3,4}p|[48]k|uhd|hdr(?:10\+?)?|dv|dovi|web-?dl|webrip|blu-?ray|bdrip|remux|x26[45]|h\.?26[45]|hevc|avc|aac|flac|dts)(?=$|[\s._\-\])])/gi;

function versionKey(name: string): string {
  const dot = name.lastIndexOf(".");
  const stem = dot > 0 ? name.slice(0, dot) : name;
  return stem.replace(VERSION, " ").replace(/[\s._\-[\]()]+/g, " ").trim().toLowerCase();
}

function opLabel(c: Change, versions: ReadonlyMap<string, number>): string {
  if (c.op === "trash") return t("移到回收站");
  if (c.op === "trashIfEmpty") return t("为空才移到回收站");
  if (c.locked) return t("保持原样");
  if (c.role === "subtitle") return t("字幕 · 配对视频");
  const n = c.role === "video" ? (versions.get(versionKey(c.name)) ?? 1) : 1;
  if (n > 1) return n === 2 ? t("保留双版本") : t("保留 {n} 个版本", { n });
  const moved = c.toPath.join("/") !== c.from.join("/");
  const renamed = c.name !== c.item.name;
  if (moved && renamed) return t("移动 · 改名");
  if (moved) return t("仅移动");
  return t("仅改名");
}

const rows = computed<Row[]>(() => {
  const p = plan.value;
  if (!p) return [];
  const errors = planErrors.value;
  const out: Row[] = [];
  const seriesByKey = new Map(p.base.series.map((s) => [s.key, s]));
  const pending = p.changes.filter((c) => !isPlanReviewed(c));
  const kept = p.base.kept.filter((c) => !isPlanReviewed(c));
  const moves = pending.filter((c) => c.op === "move");
  const trash = pending.filter((c) => c.op === "trash");
  const empties = pending.filter((c) => c.op === "trashIfEmpty");
  const errorOf = (c: Change) => (c.selected ? (errors.get(c.item.id) ?? "") : "");

  // One card: its head, its first rows (all when open), and the fold.
  const card = (
    key: string,
    head: Omit<Extract<Row, { kind: "head" }>, "kind" | "key" | "first" | "last">,
    list: readonly Change[],
    row: (c: Change) => Row,
    fold: number,
    moreLabel: (hidden: number) => string,
  ) => {
    const open = opened.value[key] ?? list.some((c) => !!errorOf(c));
    const shown = open ? list : list.slice(0, fold);
    const hidden = list.length - shown.length;
    const folds = list.length > fold;
    out.push({ kind: "head", key, ...head, first: out.length === 0, last: !shown.length && !folds });
    shown.forEach((c, i) => {
      const r = row(c);
      if (r.kind === "change" || r.kind === "line") r.last = i === shown.length - 1 && !folds;
      out.push(r);
    });
    if (folds) out.push({ kind: "more", key: `m:${key}`, card: key, hidden, open, label: open ? t("收起 ↑") : moreLabel(hidden) });
  };

  const reviewedCard = () => {
    if (!planReviewed.value.length) return;
    card(
      "g:reviewed",
      { tone: "kept", title: t("已处理"), meta: t("已确认保留 {n} 项", { n: planReviewed.value.length }), pill: "", ids: [], on: 0, series: null, movie: false },
      planReviewed.value,
      (c) => ({ kind: "change", key: `r:${c.item.id}`, change: c, error: "", detail: sourcePath(c), op: t("保持原样"), reviewed: true, last: false }),
      0,
      (hidden) => t("查看这 {n} 项 ↓", { n: hidden }),
    );
  };

  if (view.value === "target") {
    // By destination folder, in path order.
    const byFolder = new Map<string, Change[]>();
    for (const c of moves) {
      const k = c.toPath.join(" / ");
      const list = byFolder.get(k);
      if (list) list.push(c);
      else byFolder.set(k, [c]);
    }
    const folders = [...byFolder.keys()].sort((a, b) => a.localeCompare(b, undefined, { numeric: true }));
    folders.forEach((folder) => {
      const list = byFolder.get(folder)!;
      const versions = new Map<string, number>();
      for (const c of list) if (c.role === "video") versions.set(versionKey(c.name), (versions.get(versionKey(c.name)) ?? 0) + 1);
      const series = list.map((c) => seriesByKey.get(c.series)).find((s) => !!s) ?? null;
      const source = series?.source ?? (p.origin === "rename" ? "" : p.origin);
      const ticks = tickState(list);
      const meta = [t("已选 {n} 项", { n: ticks.on }), SOURCE_LABEL[source]?.() ?? ""].filter(Boolean).join(" · ");
      card(
        `t:${folder}`,
        {
          tone: "target",
          title: folder || t("{name}（原位置）", { name: scopeLabel.value }),
          meta,
          pill: source === "tmdb" ? t("来源已核对") : "",
          ...ticks,
          series: series && (series.yearGuessed || series.guess) ? series : null,
          movie: series?.type === "movie",
        },
        list,
        (c) => ({ kind: "change", key: `c:${c.item.id}`, change: c, error: errorOf(c), detail: `→ ${c.name}`, op: opLabel(c, versions), last: false }),
        FOLD,
        (hidden) => t("查看其余 {n} 项 ↓", { n: hidden }),
      );
    });

    const none = new Map<string, number>();
    if (trash.length) {
      const size = trash.reduce((sum, c) => sum + (c.item.folder ? 0 : c.item.size || 0), 0);
      card(
        "g:trash",
        { tone: "trash", title: t("移到回收站"), meta: t("{n} 项 · {size} · 默认不选择，逐项说明原因", { n: trash.length, size: bytes(size, "0 B", 1) }), pill: "", ...tickState(trash), series: null, movie: false },
        trash,
        (c) => ({ kind: "change", key: `c:${c.item.id}`, change: c, error: errorOf(c), detail: `${sourcePath(c)} · ${c.note}`, op: opLabel(c, none), last: false }),
        FOLD,
        (hidden) => t("查看其余 {n} 项 ↓", { n: hidden }),
      );
    }
    if (empties.length) {
      card(
        "g:empty",
        { tone: "empty", title: t("执行后为空才移到回收站"), meta: t("{n} 个文件夹 · 执行时确认为空才处理", { n: empties.length }), pill: "", ...tickState(empties), series: null, movie: false },
        empties,
        (c) => ({ kind: "change", key: `c:${c.item.id}`, change: c, error: errorOf(c), detail: sourcePath(c) || t("全部文件"), op: opLabel(c, none), last: false }),
        FOLD,
        (hidden) => t("查看其余 {n} 项 ↓", { n: hidden }),
      );
    }
    if (kept.length) {
      card(
        "g:kept",
        { tone: "kept", title: t("保持原样"), meta: t("{n} 项 · 不会改动，逐项说明原因", { n: kept.length }), pill: "", ids: [], reviewIds: kept.map((c) => c.item.id), on: 0, series: null, movie: false },
        kept,
        (c) => ({ kind: "change", key: `k:${c.item.id}`, change: c, error: "", detail: `${sourcePath(c) || t("全部文件")} · ${c.locked}`, op: t("保持原样"), last: false }),
        0,
        (hidden) => t("查看这 {n} 项 ↓", { n: hidden }),
      );
    }
    reviewedCard();
    return out;
  }

  // By Source: the same selected changes, by the folder they are in now.
  const doing = pending.filter((c) => c.selected && !c.locked);
  const byDir = new Map<string, Change[]>();
  for (const c of doing) {
    const dir = c.from.join(" / ");
    const list = byDir.get(dir);
    if (list) list.push(c);
    else byDir.set(dir, [c]);
  }
  const dirs = [...byDir.keys()].sort((a, b) => a.localeCompare(b, undefined, { numeric: true }));
  for (const dir of dirs) {
    const list = byDir.get(dir)!;
    const moving = list.filter((c) => c.op === "move");
    const targets = [...new Set(moving.map((c) => c.toPath.join(" / ")))];
    const trashing = list.filter((c) => c.op === "trash").length;
    const emptied = list.length - moving.length - trashing;
    const unrecognised = kept.filter((c) => c.from.join(" / ") === dir).length;
    const unticked = pending.filter((c) => !c.selected && !c.locked && c.from.join(" / ") === dir).length;
    const remaining = unrecognised + unticked;
    const target = targets[0] || scopeLabel.value;
    const parts = [
      moving.length
        ? targets.length > 1
          ? t("{n} 项将移动到「{target}」等 {places} 处", { n: moving.length, target, places: targets.length })
          : t("{n} 项将移动到「{target}」", { n: moving.length, target })
        : "",
      trashing ? t("{n} 项移到回收站", { n: trashing }) : "",
      emptied ? t("{n} 个文件夹执行后为空才移到回收站", { n: emptied }) : "",
      remaining ? t("另有 {n} 项未决定，请在按目标中查看", { n: remaining }) : "",
    ].filter(Boolean);
    const key = `d:${dir}`;
    card(
      key,
      { tone: "source", title: [scopeLabel.value, dir].filter(Boolean).join(" / "), meta: t("{parts}。", { parts: parts.join(t("；")) }), pill: "", ids: [], on: 0, series: null, movie: false },
      list,
      (c) => ({
        kind: "line",
        key: `l:${c.item.id}`,
        change: c,
        target:
          c.op === "move"
            ? c.toPath.join("/") === c.from.join("/")
              ? c.name
              : [...c.toPath, c.name].join(" / ")
            : c.op === "trash"
              ? t("回收站")
              : t("执行后为空才移到回收站"),
        last: false,
      }),
      Number.MAX_SAFE_INTEGER,
      () => "",
    );
  }
  reviewedCard();
  return out;
});

const scroller = ref<HTMLElement | null>(null);
const { windows, reveal } = useRowWindows<Row>({
  scroller,
  lists: () => [{ id: "rows", items: rows.value }],
  key: (row) => row.key,
  shape: (row) => {
    if (row.kind === "change") return row.error ? "change:error" : row.last ? "change:last" : "change";
    if (row.kind === "head") return `head:${row.tone}${row.first ? ":first" : ""}`;
    return row.kind;
  },
});
const drawn = computed(() => windows.value.rows ?? { rows: [], style: {} });

/* ---------- Rename ---------- */

const editing = ref("");
const draft = ref("");
const draftError = computed(() => (editing.value ? nameError(draft.value) : ""));

async function startRename(c: Change) {
  editing.value = c.item.id;
  draft.value = c.name;
  const row = await reveal(`c:${c.item.id}`);
  await nextTick();
  const input = row?.querySelector<HTMLInputElement>("input.rename-input");
  input?.focus();
  input?.select();
}

function commitRename() {
  const id = editing.value;
  if (!id || draftError.value) return;
  renamePlanItem(id, draft.value);
  editing.value = "";
}

function cancelRename() {
  editing.value = "";
}

/* ---------- Naming Templates ---------- */

const naming = computed(() => aiSettings.value?.naming || "plex");
const customValid = computed(() => {
  const s = aiSettings.value;
  return !!s && !!s.seriesTemplate && !!s.movieTemplate && !templateError(s.seriesTemplate, "series") && !templateError(s.movieTemplate, "movie");
});

// Custom is offered only when its templates are usable, or already chosen.
const namingOptions = computed<SelectOption<string>[]>(() => [
  { value: "plex", label: "Plex / Infuse" },
  { value: "chinese", label: t("中文") },
  ...(customValid.value || naming.value === "custom" ? [{ value: "custom", label: t("自定义") }] : []),
]);

/* ---------- Additional Requirements ---------- */

const refining = ref(false);
const busy = computed(() => !!assistant.busy || !!assistant.live || assistant.running || !!assistant.consent || !!assistant.connect);

async function openRefine() {
  refining.value = !refining.value;
  if (!refining.value) return;
  await nextTick();
  document.getElementById("ai-refine")?.focus();
}

function refine() {
  if (!busy.value && assistant.prompt.trim()) void submitComposer();
}

/* ---------- Scope & Summary ---------- */

const account = computed(() => accountName(assistant.account));
const runningPlan = computed(() => assistant.running && !assistant.undoing);
const emptyReason = computed(() => summary.value?.selected ? "" : t("本次仅记录决定，不会改动云盘文件。"));
const dockNote = computed(() => {
  const s = summary.value;
  if (!s) return "";
  if (!s.selected) return emptyReason.value;
  const parts = [t("已选 {n} 项", { n: s.selected }), s.conflicts ? t("{n} 项路径冲突", { n: s.conflicts }) : t("无路径冲突")];
  if (s.overLimit) parts.push(t("超过 {n} 项，请缩小范围", { n: 5000 }));
  parts.push(t("尚未执行"));
  return parts.join(" · ");
});
const canConfirm = computed(() => {
  const s = summary.value;
  return !!s && !s.conflicts && !s.overLimit && !busy.value;
});

async function reviewItems(ids: readonly string[], keep = true) {
  try {
    await keepPlanItems(ids, keep);
  } catch {
    // The store reports persistence failures; leave the plan open for retry.
  }
}

async function keepAllAndConfirm() {
  try {
    await keepAllPlanItems();
    openPlanConfirm();
  } catch {
    // The store reports persistence failures; do not open confirmation.
  }
}
</script>

<template>
  <div class="ai-plan">
    <div ref="scroller" class="plan-scroll">
      <div class="plan-inner">
        <button class="text-back" type="button" :disabled="assistant.running" @click="planBackTo()">
          ‹ {{ assistant.planBack === "chat" ? t("返回对话") : t("返回") }}
        </button>

        <p v-if="!plan" class="waiting">
          <Orbit :size="18" />
          {{ assistant.busy || t("正在准备计划…") }}
        </p>

        <template v-else-if="summary">
          <div class="plan-heading">
            <div class="heading-text">
              <span class="eyebrow">{{ t("整理计划 · {account}", { account }) }} <span class="slash">/</span> {{ scopeLabel }}</span>
              <h1>{{ t("先看清去向，再决定执行。") }}</h1>
              <p>{{ plan.basis }}</p>
            </div>
            <span class="plan-count"><strong class="tnum">{{ num(summary.selected) }}</strong> {{ t("项可执行") }}</span>
          </div>

          <div class="plan-tools">
            <div v-slide class="seg" role="group" :aria-label="t('查看方式')">
              <button type="button" :aria-pressed="view === 'target'" @click="view = 'target'">{{ t("按目标") }}</button>
              <button type="button" :aria-pressed="view === 'source'" @click="view = 'source'">{{ t("按来源") }}</button>
            </div>
            <div v-if="media" class="tools-right">
              <button class="quiet link" type="button" :aria-expanded="refining" :disabled="assistant.running" @click="openRefine()">{{ t("补充要求") }}</button>
              <span class="naming-label">{{ t("命名模板") }}</span>
              <SelectMenu
                v-slot="{ label }"
                class="plan-naming-pick"
                :model-value="naming"
                :options="namingOptions"
                :label="t('命名模板')"
                :disabled="assistant.running || aiSettings.saving"
                @update:model-value="setPlanNaming"
              >
                {{ label }}
                <Icon name="chevronDown" :size="13" />
              </SelectMenu>
              <button class="quiet link" type="button" @click="openAISettings()">{{ t("自定义模板") }}</button>
            </div>
          </div>

          <form v-if="media && refining" class="refine" @submit.prevent="refine">
            <input
              id="ai-refine"
              v-model="assistant.prompt"
              type="text"
              autocomplete="off"
              spellcheck="false"
              :placeholder="t('补充要求，例如：动漫单独放、特别篇放进 Season 00')"
              :aria-label="t('补充要求')"
              :disabled="assistant.running"
            />
            <button class="primary" type="submit" :disabled="busy || !assistant.prompt.trim()">{{ t("重新生成") }}</button>
            <small>{{ t("已取消的勾选和改过的名称会保留；需要模型时会先问你。") }}</small>
          </form>

          <p v-if="!rows.length" class="none">{{ view === "source" ? t("没有选中的项。") : t("没有可以整理的项。") }}</p>
          <div data-list="rows" class="rows" :style="drawn.style">
            <template v-for="{ item: row, style } in drawn.rows" :key="row.key">
              <div v-if="row.kind === 'head'" class="prow head" :class="{ first: row.first }" :data-row="row.key" :style="style">
                <div class="cell top" :class="[row.tone, { last: row.last }]">
                  <label v-if="row.ids.length" class="check">
                    <input
                      type="checkbox"
                      :checked="row.on === row.ids.length"
                      :indeterminate.prop="row.on > 0 && row.on < row.ids.length"
                      :disabled="assistant.running"
                      :aria-label="t('选择「{name}」里的全部项', { name: row.title })"
                      @change="pickPlanItems(row.ids, ($event.target as HTMLInputElement).checked)"
                    />
                  </label>
                  <span v-if="row.tone === 'target' || row.tone === 'source'" class="folder-visual" :class="{ movie: row.movie }" aria-hidden="true" />
                  <span v-else class="group-icon" aria-hidden="true">
                    <Icon :name="row.tone === 'trash' ? 'trash' : row.tone === 'empty' ? 'folder' : 'info'" :size="18" />
                  </span>
                  <div class="top-text">
                    <h2 :title="row.title">{{ row.title }}</h2>
                    <p>{{ row.meta }}</p>
                    <button
                      v-if="row.series"
                      class="year"
                      :class="{ on: row.series.yearGuessed }"
                      type="button"
                      :aria-pressed="row.series.yearGuessed"
                      :disabled="assistant.running"
                      @click="togglePlanYear(row.series.key)"
                    >
                      {{ row.series.yearGuessed ? t("✓ 已采用推测年份 {year}", { year: String(row.series.year) }) : t("＋ 补上推测年份 {year}", { year: String(row.series.guess) }) }}
                    </button>
                  </div>
                  <span v-if="row.pill" class="local-pill">{{ row.pill }}</span>
                  <button v-if="row.reviewIds?.length" class="quiet review-action" type="button" :disabled="busy" @click="reviewItems(row.reviewIds)">{{ t("全部确认保留") }}</button>
                </div>
              </div>

              <div v-else-if="row.kind === 'change'" class="prow" :data-row="row.key" :style="style">
                <div
                  class="cell tree-row"
                  :class="{ last: row.last, off: !row.change.selected || !!row.change.locked, bad: !!row.error, kept: !!row.change.locked || row.reviewed }"
                >
                  <label class="check">
                    <input
                      v-if="!row.change.locked && !row.reviewed"
                      type="checkbox"
                      :checked="row.change.selected"
                      :disabled="busy"
                      :aria-label="t('选择「{name}」', { name: row.change.item.name })"
                      @change="pickPlanItems([row.change.item.id], ($event.target as HTMLInputElement).checked)"
                    />
                  </label>
                  <div class="row-text">
                    <strong :title="row.change.item.name">{{ row.change.item.name }}</strong>
                    <template v-if="!row.reviewed && editing === row.change.item.id">
                      <input
                        v-model="draft"
                        class="rename-input"
                        type="text"
                        spellcheck="false"
                        :aria-label="t('「{name}」的新名称', { name: row.change.item.name })"
                        :aria-invalid="!!draftError"
                        @keydown.enter.prevent="commitRename"
                        @keydown.esc.stop.prevent="cancelRename"
                        @blur="commitRename"
                      />
                      <small v-if="draftError" class="error">{{ draftError }}</small>
                    </template>
                    <small v-else :title="row.detail">{{ row.detail }}</small>
                    <small v-if="row.error" class="error">{{ row.error }}</small>
                  </div>
                  <button
                    v-if="!row.reviewed && row.change.op === 'move' && !row.change.locked && editing !== row.change.item.id"
                    class="quiet rename"
                    type="button"
                    :disabled="assistant.running"
                    :aria-label="t('给「{name}」改名', { name: row.change.item.name })"
                    @click="startRename(row.change)"
                  >
                    {{ t("改名") }}
                  </button>
                  <button class="quiet review-action" type="button" :disabled="busy" :aria-label="row.reviewed ? t('撤销对「{name}」的保留决定', { name: row.change.item.name }) : t('确认保留「{name}」', { name: row.change.item.name })" @click="reviewItems([row.change.item.id], !row.reviewed)">
                    {{ row.reviewed ? t("撤销决定") : row.change.locked ? t("确认保留") : t("保留并完成") }}
                  </button>
                  <em>{{ row.op }}</em>
                </div>
              </div>

              <div v-else-if="row.kind === 'line'" class="prow" :data-row="row.key" :style="style">
                <div class="cell source-line" :class="{ last: row.last }">
                  <strong :title="row.change.item.name">{{ row.change.item.name }}</strong>
                  <small :title="row.target">→ {{ row.target }}</small>
                </div>
              </div>

              <div v-else class="prow" :data-row="row.key" :style="style">
                <div class="cell more-cell last">
                  <button class="tree-more" type="button" :aria-expanded="row.open" @click="toggle(row.card, row.open)">{{ row.label }}</button>
                </div>
              </div>
            </template>
          </div>

          <p v-if="view === 'source'" class="source-help"><button class="quiet link" type="button" @click="view = 'target'">{{ t("查看未决定的项、修改勾选或确认保留，请切换到按目标。") }}</button></p>

          <div v-if="plan.lowGroups && plan.base.kept.some((c) => !isPlanReviewed(c))" class="low">
            <Icon name="info" :size="16" />
            <p>{{ t("还有 {n} 组规则无法确定作品，先保持原样。", { n: plan.lowGroups }) }}</p>
            <button type="button" :disabled="busy" @click="identifyRemaining()">
              {{ aiReady ? t("问 AI 识别剩下的 {n} 组", { n: plan.lowGroups }) : t("配置模型后可识别剩下的 {n} 组", { n: plan.lowGroups }) }}
            </button>
          </div>

          <div class="plan-caution">
            <Icon name="shield" :size="17" />
            <p>{{ t("这次不会永久删除任何文件。执行前还会重新读取云盘状态；重名或位置变化的项将跳过并在回执中说明。") }}</p>
          </div>
        </template>
      </div>
    </div>

    <footer v-if="plan && summary" class="plan-dock">
      <span v-if="runningPlan" class="dock-note running tnum">
        <Orbit :size="15" />
        {{ assistant.stopping ? t("正在停止…") : t("正在整理 {done} / {total}", { done: assistant.progress.done, total: assistant.progress.total }) }}
      </span>
      <span v-else class="dock-note" :class="{ warn: summary.conflicts || summary.overLimit }">{{ dockNote }}</span>
      <button v-if="runningPlan" class="dock-button" type="button" :disabled="assistant.stopping" @click="stopPlanRun()">{{ t("停止") }}</button>
      <button v-if="!runningPlan" class="quiet dock-button" type="button" :disabled="busy" @click="keepAllAndConfirm()">{{ t("全部保留并完成") }}</button>
      <button v-if="!runningPlan" class="primary dock-button ai-plan-confirm" type="button" :disabled="!canConfirm" @click="openPlanConfirm()">
        {{ t("检查并完成") }}
        <Icon name="arrowRight" :size="15" />
      </button>
    </footer>
  </div>
</template>

<style scoped>
/* The plan scrolls; the dock is the canvas's last row, so it is always at
   the foot and never covers a row. */
.ai-plan {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.heading-text,
.top-text h2,
.top-text p,
.row-text strong,
.row-text small,
.source-line strong,
.source-line small,
.refine > small,
.low p,
.plan-caution p,
.dock-note,
.none {
  -webkit-user-select: text;
  user-select: text;
  --default-contextmenu: show;
}

.plan-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;
}

.plan-inner {
  max-width: 940px;
  margin: 0 auto;
  padding: 28px 38px;
}

.text-back {
  min-height: 30px;
  margin: 0 -5px;
  padding: 0 10px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  font-size: 12px;
  color: var(--accent);
}

.text-back:hover:not(:disabled) {
  border: 0;
  background: var(--accent-soft);
}

.waiting {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  margin: 48px 0;
  color: var(--fg-dim);
}

.plan-heading {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 18px;
  margin: 16px 0 25px;
}

.heading-text {
  min-width: 0;
}

.eyebrow {
  display: block;
  font-size: 11px;
  font-weight: 650;
  letter-spacing: 0.08em;
  color: var(--accent);
  overflow-wrap: anywhere;
}

.slash {
  margin: 0 2px;
  color: var(--fg-dim);
}

.plan-heading h1 {
  margin: 12px 0 9px;
  font-size: 25px;
  line-height: 1.25;
  font-weight: 650;
  letter-spacing: -0.025em;
}

.plan-heading p {
  max-width: 64ch;
  margin: 0;
  font-size: 13px;
  line-height: 1.7;
  color: var(--fg-dim);
}

.plan-count {
  flex: none;
  font-size: 11px;
  color: var(--fg-dim);
  white-space: nowrap;
}

.plan-count strong {
  font-size: 27px;
  color: var(--accent);
}

.plan-tools {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 10px;
  margin-bottom: 12px;
  font-size: 11px;
  color: var(--fg-dim);
}

.seg {
  display: flex;
  gap: 3px;
  padding: 3px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: color-mix(in srgb, var(--fg) 4%, transparent);
}

.seg button {
  min-height: 28px;
  padding: 4px 12px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  font-size: 11px;
  color: var(--fg-dim);
}

.seg::before {
  background: var(--accent-soft);
  box-shadow: inset 0 0 0 1px var(--line-strong);
}

.seg button[aria-pressed="true"] {
  font-weight: 600;
  color: var(--fg);
  background: transparent;
}

.tools-right {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 0;
}

.plan-naming-pick {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 30px;
  padding: 0 8px;
  border: 1px solid transparent;
  border-radius: 8px;
  font-size: 11px;
  font-weight: 600;
  color: var(--fg);
  white-space: nowrap;
  cursor: pointer;
}

.plan-naming-pick:hover {
  border-color: var(--line-strong);
}

.plan-naming-pick[aria-disabled="true"] {
  cursor: default;
  opacity: 0.6;
}

.link {
  min-height: 30px;
  padding: 2px 8px;
  border-radius: 8px;
  font-size: 11px;
  color: var(--accent);
}

.refine {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 10px;
  margin: 0 0 14px;
  padding: 12px;
  border: 1px solid var(--line-strong);
  border-radius: 14px;
  background: color-mix(in srgb, var(--accent) 6%, transparent);
}

.refine input {
  flex: 1;
  min-width: 0;
  height: 36px;
  font-size: 13px;
}

.refine .primary {
  flex: none;
  min-height: 36px;
  border-radius: 11px;
  font-size: 12px;
}

.refine small {
  flex-basis: 100%;
  font-size: 11px;
  color: var(--fg-dim);
}

.none {
  margin: 0;
  padding: 24px 4px;
  font-size: 13px;
  color: var(--fg-dim);
}

/* ---------- cards, drawn row by row ---------- */

.rows {
  position: relative;
}

/* The gap between cards is the head row's own padding: the windowing
   measures rows by their boxes, never their margins. */
.prow.head:not(.first) {
  padding-top: 14px;
}

.cell {
  border-left: 1px solid var(--line-strong);
  border-right: 1px solid var(--line-strong);
  background: color-mix(in srgb, var(--fg) 4%, transparent);
}

.cell.last {
  border-bottom: 1px solid var(--line-strong);
  border-radius: 0 0 15px 15px;
}

.cell.top {
  display: flex;
  align-items: center;
  gap: 17px;
  min-width: 0;
  padding: 22px 23px;
  border-top: 1px solid var(--line-strong);
  border-radius: 15px 15px 0 0;
}

.cell.top.last {
  border-radius: 15px;
}

.cell.top:not(.last) {
  border-bottom: 1px solid var(--line);
}

.folder-visual {
  flex: none;
  width: 44px;
  height: 34px;
  border-radius: 5px 8px 8px 8px;
  background: linear-gradient(180deg, color-mix(in srgb, var(--accent) 80%, white 20%), var(--accent));
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.35);
}

.folder-visual.movie {
  background: linear-gradient(180deg, color-mix(in srgb, var(--tint-violet) 80%, white 20%), var(--tint-violet));
}

.group-icon {
  display: grid;
  place-items: center;
  flex: none;
  width: 44px;
  height: 34px;
  color: var(--fg-dim);
}

.cell.top.trash .group-icon {
  color: var(--warning);
}

.top-text {
  flex: 1;
  min-width: 0;
}

.top-text h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.top-text p {
  margin: 3px 0 0;
  font-size: 11px;
  line-height: 1.55;
  color: var(--fg-dim);
  overflow-wrap: anywhere;
}

.year {
  min-height: 28px;
  margin-top: 8px;
  padding: 3px 10px;
  border-color: color-mix(in srgb, var(--warning) 35%, transparent);
  background: transparent;
  font-size: 11px;
  color: var(--warning);
}

.year:hover:not(:disabled),
.year.on {
  border-color: color-mix(in srgb, var(--warning) 45%, transparent);
  background: color-mix(in srgb, var(--warning) 12%, transparent);
}

.local-pill {
  flex: none;
  align-self: flex-start;
  font-size: 11px;
  color: var(--success);
  white-space: nowrap;
}

.check {
  display: grid;
  place-items: center;
  flex: none;
  width: 20px;
  min-height: 36px;
  cursor: pointer;
}

.check input {
  width: 17px;
  height: 17px;
  margin: 0;
  padding: 0;
  accent-color: var(--accent);
  cursor: pointer;
}

.check input:disabled {
  cursor: default;
}

.check input:focus-visible {
  outline: 2px solid color-mix(in srgb, var(--accent) 70%, transparent);
  outline-offset: 2px;
}

.tree-row {
  display: flex;
  align-items: center;
  gap: 13px;
  min-height: 64px;
  padding: 10px 23px;
}

.tree-row:not(.last) {
  border-bottom: 1px solid var(--line);
}

.row-text {
  flex: 1;
  min-width: 0;
}

.row-text strong,
.row-text small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row-text strong {
  font-size: 12px;
  font-weight: 600;
}

.row-text small {
  margin-top: 3px;
  font-size: 11px;
  color: var(--fg-dim);
}

.row-text small.error {
  white-space: normal;
  overflow-wrap: anywhere;
  color: var(--danger);
}

.tree-row.off .row-text {
  opacity: 0.6;
}

.tree-row em {
  flex: none;
  font-style: normal;
  font-size: 11px;
  color: var(--fg-dim);
  white-space: nowrap;
}

.rename-input {
  width: 100%;
  height: 30px;
  margin-top: 3px;
  padding: 0 8px;
  border-color: var(--accent);
  border-radius: 6px;
  font-size: 12px;
}

.rename-input[aria-invalid="true"] {
  border-color: var(--danger);
}

.rename {
  flex: none;
  min-height: 28px;
  padding: 2px 8px;
  border-radius: 8px;
  font-size: 11px;
  color: var(--accent);
}

.source-line {
  padding: 12px 24px;
  font-size: 12px;
}

.source-line:not(.last) {
  border-bottom: 1px solid var(--line);
}

.source-line strong,
.source-line small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.source-line small {
  margin-top: 4px;
  font-size: 11px;
  color: var(--fg-dim);
}

.more-cell {
  padding: 0;
}

.tree-more {
  width: 100%;
  justify-content: flex-start;
  min-height: 44px;
  padding: 0 23px 0 56px;
  border: 0;
  border-radius: 0 0 15px 15px;
  background: transparent;
  font-size: 11px;
  color: var(--accent);
}

.tree-more:hover:not(:disabled) {
  border: 0;
  background: var(--accent-soft);
}

.source-help {
  margin: 10px 4px 0;
  font-size: 11px;
  color: var(--fg-dim);
}

.low {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 16px;
  padding: 12px 16px;
  border: 1px solid color-mix(in srgb, var(--warning) 35%, transparent);
  border-radius: 12px;
  background: color-mix(in srgb, var(--warning) 8%, transparent);
  font-size: 12px;
}

.low > svg {
  flex: none;
  color: var(--warning);
}

.low p {
  flex: 1;
  min-width: 12em;
  margin: 0;
}

.low button {
  flex: none;
  min-height: 34px;
  border-radius: 11px;
  font-size: 12px;
}

.plan-caution {
  display: flex;
  gap: 10px;
  padding: 19px 3px 7px;
  font-size: 11px;
  line-height: 1.7;
  color: var(--fg-dim);
}

.plan-caution > svg {
  flex: none;
  color: var(--accent);
}

.plan-caution p {
  margin: 0;
}

/* ---------- dock ---------- */

.plan-dock {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 65px;
  padding: 10px 36px;
  border-top: 1px solid var(--line);
  background: var(--canvas-flat);
  font-size: 12px;
  color: var(--fg-dim);
}

.dock-note {
  min-width: 0;
  margin-right: auto;
  overflow-wrap: anywhere;
}

.dock-note.warn {
  color: var(--danger);
}

.dock-note.running {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: var(--fg);
}

.dock-button {
  flex: none;
  min-height: 39px;
  padding: 0 15px;
  border-radius: 12px;
  font-size: 12px;
}

.review-action {
  flex: none;
  padding: 6px 8px;
  font-size: 11px;
  color: var(--accent);
  white-space: normal;
}

@media (max-width: 1079px) {
  .naming-label {
    display: none;
  }
}

@media (max-width: 700px) {
  .plan-heading {
    align-items: flex-start;
  }

  .plan-count {
    align-self: flex-start;
  }

  .plan-inner {
    padding: 26px 20px;
  }

  .plan-dock {
    padding: 10px 20px;
  }
}

/* The canvas scrolls as one page here: the dock sticks to its foot while
   the plan passes under it, and is reached at the end either way. */
@media (max-width: 550px) {
  .ai-plan {
    flex: 1 0 auto;
    min-height: auto;
  }

  .plan-scroll {
    flex: none;
    overflow: visible;
  }

  .plan-dock {
    position: sticky;
    bottom: 0;
    z-index: 1;
    min-height: 72px;
    gap: 10px;
    flex-wrap: wrap;
  }

  .dock-note {
    font-size: 11px;
  }

  .plan-heading {
    flex-direction: column;
    gap: 9px;
  }

  .plan-heading h1 {
    font-size: 22px;
  }

  .plan-count strong {
    font-size: 20px;
  }

  .cell.top {
    gap: 10px;
    padding: 17px 12px;
    flex-wrap: wrap;
  }

  .cell.top .local-pill {
    display: none;
  }

  .top-text h2 {
    font-size: 13px;
  }

  .tree-row {
    gap: 8px;
    padding: 12px;
    flex-wrap: wrap;
  }

  .tree-row em {
    display: none;
  }

  .source-line {
    padding: 12px;
  }

  .tree-more {
    padding-left: 38px;
  }
}
</style>
