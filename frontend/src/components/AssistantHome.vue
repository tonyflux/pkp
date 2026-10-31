<script setup lang="ts">
// AI Assistant's first screen: what can be done right now without a
// model — the newest arrival waiting to be organised, the file page's
// selection, the local search for unfinished videos, the plan left last
// time — then this account's recent runs and conversations. The composer
// below takes anything else.
import { computed, nextTick, ref, watch } from "vue";
import ConfirmInline from "./ConfirmInline.vue";
import Icon from "./Icon.vue";
import { stamp } from "../format";
import { t } from "../i18n";
import {
  accountName,
  assistant,
  currentId,
  deleteConversation,
  dismissArrival,
  dismissArrivals,
  drive,
  ensureArrivalSummary,
  inbox,
  openArrival,
  openArrivals,
  openAssistantRecord,
  openConversation,
  openLocalSearch,
  organizeEntries,
  pendingArrivals,
  pickedEntries,
  recordCounts,
  recordStatus,
} from "../store";
import type { Arrival, ConversationInfo, OrganizeRecord } from "../../bindings/github.com/tonyflux/pkp/internal/services";

const account = computed(() => accountName(currentId.value));
const locked = computed(() => !!assistant.busy || !!assistant.live || assistant.running || !!assistant.consent || !!assistant.connect);

/* ---------- Newly Added ---------- */

const arrival = computed<Arrival | null>(() => pendingArrivals.value[0] ?? null);
const arrivalSummary = computed(() => (arrival.value ? inbox.summaries[arrival.value.id] : undefined));
const arrivalPicks = ref<string[]>([]);
const dismissing = ref(false);
const batchLocked = computed(() => locked.value || dismissing.value);
const selectedArrivals = computed(() => pendingArrivals.value.filter((a) => arrivalPicks.value.includes(a.id)));
const allArrivalsPicked = computed(() => !!pendingArrivals.value.length && selectedArrivals.value.length === pendingArrivals.value.length);
watch(pendingArrivals, (list) => { arrivalPicks.value = arrivalPicks.value.filter((id) => list.some((a) => a.id === id)); });
watch(currentId, () => { arrivalPicks.value = []; });

async function ignoreSelected() {
  if (batchLocked.value || !selectedArrivals.value.length) return;
  dismissing.value = true;
  try { await dismissArrivals([...selectedArrivals.value]); }
  finally { dismissing.value = false; }
}

watch(
  arrival,
  (a) => {
    if (a) void ensureArrivalSummary(a);
  },
  { immediate: true },
);

const arrivalLine = computed(() => {
  const a = arrival.value;
  if (!a) return "";
  const s = arrivalSummary.value;
  const parts = [t("来自云添加")];
  if (s?.state === "ready") parts.push(t("{n} 项", { n: s.files }));
  else if (s?.state === "waiting") parts.push(t("等待进入本地索引"));
  parts.push(stamp(a.completedAt, ""));
  return parts.filter(Boolean).join(" · ");
});

/* ---------- Continue Here ---------- */

const picked = computed(() => pickedEntries.value);
const pickedWhere = computed(() => drive.trail.map((c) => c.name).join(" / ") || t("全部文件"));

// The latest conversation whose plan is still a draft wins; else the latest run.
const lastPlan = computed<ConversationInfo | null>(() => assistant.conversations.find((c) => c.planPending) ?? null);
const lastRecord = computed<OrganizeRecord | null>(() => assistant.history[0] ?? null);

const resume = computed(() => {
  const c = lastPlan.value;
  if (c) return { kind: "conversation" as const, detail: [stamp(c.updatedAt, ""), c.title, t("一份计划尚未执行")].filter(Boolean).join(" · ") };
  const r = lastRecord.value;
  if (r) return { kind: "record" as const, detail: [stamp(r.at, ""), folderOf(r), recordStatus(r)].filter(Boolean).join(" · ") };
  return null;
});

function continueLast() {
  if (lastPlan.value) void openConversation(lastPlan.value.id);
  else if (lastRecord.value) openAssistantRecord(lastRecord.value);
}

// Two things people ask that rules alone cannot answer, as a start for the composer.
const SAYINGS = computed(() => [t("把上周云添加的纪录片放到「纪录片」里，集数补零"), t("这个月存的视频里，哪些还没看过？")]);

async function useSaying(text: string) {
  assistant.prompt = text;
  await nextTick();
  const input = document.getElementById("ai-prompt") as HTMLInputElement | null;
  input?.focus();
  input?.setSelectionRange(text.length, text.length);
}

/* ---------- Recent Tasks · Recent Conversations ---------- */

function folderOf(r: OrganizeRecord): string {
  return (r.scope || t("全部文件")).split(" / ").pop() ?? "";
}

const records = computed(() =>
  assistant.history.slice(0, 5).map((r) => {
    const n = recordCounts(r);
    return { r, title: r.task || t("整理"), detail: [stamp(r.at, ""), folderOf(r), t("{n} 项 · {status}", { n: n.items, status: r.undo ? t("已撤销") : n.done ? t("可撤销") : recordStatus(r) })].filter(Boolean).join(" · ") };
  }),
);

const talks = computed(() => assistant.conversations.slice(0, 5));
const deleting = ref("");
</script>

<template>
  <div class="ai-home">
    <div class="home-scroll">
      <div class="home-inner">
        <header class="intro">
          <span class="eyebrow"><Icon name="spark" :size="17" /> PKP ASSISTANT <span class="slash">/</span> <span class="who">{{ account }}</span></span>
          <h1>{{ t("文件先看清，再决定怎么动。") }}</h1>
          <p>{{ t("整理建议来自本地索引。需要模型时再交给你的模型，云盘改动始终由你确认。") }}</p>
        </header>

        <section v-if="arrival && pendingArrivals.length === 1" class="inbox" aria-labelledby="ai-home-inbox">
          <div class="inbox-left">
            <span class="glyph" aria-hidden="true"><Icon name="transfer" :size="26" /></span>
            <div class="inbox-text">
              <p class="kicker">{{ t("新入库 · 等你决定") }}</p>
              <h2 id="ai-home-inbox">{{ t("「{name}」已存入云盘", { name: arrival.name }) }}</h2>
              <p class="meta">{{ arrivalLine }}</p>
            </div>
          </div>
          <div class="inbox-right">
            <span class="local-pill"><Icon name="check" :size="13" /> {{ t("不用模型") }}</span>
            <div class="inbox-actions">
              <button class="quiet dismiss" type="button" :disabled="locked" :title="t('不整理，提醒不再出现；云盘里的文件不受影响')" @click="dismissArrival(arrival)">{{ t("忽略") }}</button>
              <button class="primary go" type="button" :disabled="locked" @click="openArrival(arrival)">
                {{ t("查看整理建议") }}
                <Icon name="arrowRight" :size="15" />
              </button>
            </div>
          </div>
        </section>

        <section v-if="pendingArrivals.length > 1" class="arrival-batch" :aria-label="t('批量处理入库提醒')">
          <header class="arrival-batch-head">
            <h2>{{ t("待处理入库 · {n} 项", { n: pendingArrivals.length }) }}</h2>
            <label class="arrival-check"><input type="checkbox" :checked="allArrivalsPicked" :indeterminate="!!selectedArrivals.length && !allArrivalsPicked" :disabled="batchLocked" @change="arrivalPicks = allArrivalsPicked ? [] : pendingArrivals.map((a) => a.id)" />{{ t("全选") }}</label>
          </header>
          <ul class="arrival-options">
            <li v-for="a in pendingArrivals" :key="a.id">
              <label class="arrival-option" :class="{ picked: arrivalPicks.includes(a.id) }">
                <input v-model="arrivalPicks" type="checkbox" :value="a.id" :disabled="batchLocked" />
                <span><strong>{{ a.name }}</strong><small>{{ stamp(a.completedAt, '') }}<template v-if="a.savedTo"> · {{ a.savedTo }}</template></small></span>
              </label>
            </li>
          </ul>
          <footer class="arrival-batch-actions">
            <span class="minor">{{ t("已选 {n} 项", { n: selectedArrivals.length }) }}</span>
            <button class="quiet dismiss" :disabled="batchLocked || !selectedArrivals.length" :title="t('不整理，提醒不再出现；云盘里的文件不受影响')" @click="ignoreSelected">{{ t("忽略所选入库") }}</button>
            <button class="primary go" :disabled="batchLocked || !selectedArrivals.length" @click="openArrivals(selectedArrivals)">{{ t("批量查看整理建议") }}<Icon name="arrowRight" :size="15" /></button>
          </footer>
        </section>

        <div class="columns">
          <section class="focus" aria-labelledby="ai-home-today">
            <div class="section-line">
              <div>
                <span class="kicker">{{ t("从这里继续") }}</span>
                <h2 id="ai-home-today">{{ t("手上有事，直接处理。") }}</h2>
              </div>
              <span class="minor">{{ t("当前账号") }}</span>
            </div>

            <button v-if="picked.length" class="action-row" type="button" :disabled="locked" @click="organizeEntries(picked)">
              <span class="action-icon"><Icon name="folder" :size="19" /></span>
              <span class="action-copy">
                <strong>{{ t("整理已选的 {n} 个文件", { n: picked.length }) }}</strong>
                <small>{{ t("{where} · 规则先出计划，剩余的再问 AI", { where: pickedWhere }) }}</small>
              </span>
              <Icon class="row-arrow" name="chevronRight" :size="15" />
            </button>
            <!-- i18n-ignore -->
            <button class="action-row" type="button" @click="openLocalSearch('类型:视频 没看完')">
              <span class="action-icon soft"><Icon name="search" :size="19" /></span>
              <span class="action-copy">
                <strong>{{ t("找上次没看完的视频") }}</strong>
                <small>{{ t("先用本地索引，结果留在云盘里") }}</small>
              </span>
              <Icon class="row-arrow" name="chevronRight" :size="15" />
            </button>
            <button v-if="resume" class="action-row" type="button" :disabled="locked" @click="continueLast()">
              <span class="action-icon soft"><Icon name="clock" :size="19" /></span>
              <span class="action-copy">
                <strong>{{ t("继续上一次整理") }}</strong>
                <small>{{ resume.detail }}</small>
              </span>
              <Icon class="row-arrow" name="chevronRight" :size="15" />
            </button>
            <button v-for="text in SAYINGS" :key="text" class="action-row saying" type="button" @click="useSaying(text)">
              <span class="action-icon soft"><Icon name="spark" :size="19" /></span>
              <span class="action-copy">
                <strong>{{ t("「{name}」", { name: text }) }}</strong>
                <small>{{ t("填入下方输入框，发送前会先问你") }}</small>
              </span>
              <Icon class="row-arrow" name="chevronRight" :size="15" />
            </button>
          </section>

          <aside class="side-note">
            <span class="kicker">{{ t("这次有什么不同") }}</span>
            <h2>{{ t("AI 只参与") }}<br />{{ t("需要判断的部分。") }}</h2>
            <p>{{ t("搜索与命名规则在本机完成。确定的作品名称由 TMDB 精确匹配，不发给模型；模糊的结果留给你决定。") }}</p>
            <div class="divider" />
            <span class="shield"><Icon name="shield" :size="15" /> {{ t("每次询问或始终允许，由你选") }}</span>
          </aside>
        </div>

        <div class="lists">
          <section aria-labelledby="ai-home-records">
            <div class="list-head">
              <h3 id="ai-home-records">{{ t("最近任务") }}</h3>
              <span class="minor">{{ t("本账号") }}</span>
            </div>
            <p v-if="assistant.historyError" class="empty error">{{ t("读不出最近任务：{error}", { error: assistant.historyError }) }}</p>
            <p v-else-if="!records.length" class="empty">{{ t("还没有整理记录") }}</p>
            <ul v-else>
              <li v-for="row in records" :key="row.r.id">
                <button class="list-row" type="button" :disabled="locked" @click="openAssistantRecord(row.r)">
                  <span class="mark" :class="{ undone: !!row.r.undo }"><Icon :name="row.r.undo ? 'restore' : 'check'" :size="15" /></span>
                  <span class="list-copy">
                    <strong>{{ row.title }}</strong>
                    <small>{{ row.detail }}</small>
                  </span>
                  <Icon class="row-arrow" name="chevronRight" :size="15" />
                </button>
              </li>
            </ul>
          </section>

          <section aria-labelledby="ai-home-talks">
            <div class="list-head">
              <h3 id="ai-home-talks">{{ t("最近对话") }}</h3>
              <span class="minor">{{ t("保存在这台电脑") }}</span>
            </div>
            <p v-if="!talks.length" class="empty">{{ t("还没有对话") }}</p>
            <ul v-else>
              <li v-for="c in talks" :key="c.id">
                <div class="talk">
                  <button class="list-row" type="button" :disabled="locked" @click="openConversation(c.id)">
                    <span class="mark talk-mark"><Icon name="spark" :size="15" /></span>
                    <span class="list-copy">
                      <strong>{{ c.title || t("未命名的对话") }}</strong>
                      <small>{{ [stamp(c.updatedAt, ""), t("{n} 轮", { n: c.turns }), c.planPending ? t("计划尚未执行") : ""].filter(Boolean).join(" · ") }}</small>
                    </span>
                  </button>
                  <button
                    class="quiet icon remove"
                    type="button"
                    :aria-label="t('删除对话「{name}」', { name: c.title || t('未命名的对话') })"
                    :title="t('删除对话')"
                    :disabled="locked"
                    @click="deleting = deleting === c.id ? '' : c.id"
                  >
                    <Icon name="trash" :size="15" />
                  </button>
                </div>
                <ConfirmInline
                  v-if="deleting === c.id"
                  :message="t('删除后这段对话不再保存在这台电脑；已执行的整理和回执不受影响。')"
                  :confirm-label="t('删除对话')"
                  danger
                  @cancel="deleting = ''"
                  @confirm="(deleting = ''), deleteConversation(c.id)"
                />
              </li>
            </ul>
          </section>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ai-home {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.home-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;
}

.home-inner {
  max-width: 1130px;
  margin: 0 auto;
  padding: 38px 44px 24px;
}

.eyebrow,
.kicker {
  font-size: 11px;
  font-weight: 650;
  letter-spacing: 0.08em;
  color: var(--accent);
}

.eyebrow {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  max-width: 100%;
}

.eyebrow .who {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.slash {
  margin: 0 2px;
  color: var(--fg-dim);
}

.kicker {
  font-size: 10px;
  letter-spacing: 0.12em;
}

.intro {
  max-width: 770px;
  margin-bottom: 30px;
}

.intro h1 {
  margin: 12px 0 9px;
  font-size: 28px;
  line-height: 1.25;
  font-weight: 650;
  letter-spacing: -0.025em;
}

.intro p {
  margin: 0;
  font-size: 13px;
  line-height: 1.7;
  color: var(--fg-dim);
}

/* ---------- Newly Added ---------- */

.inbox {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 22px;
  padding: 22px 24px;
  border: 1px solid color-mix(in srgb, var(--accent) 30%, transparent);
  border-radius: 19px;
  background: linear-gradient(110deg, color-mix(in srgb, var(--accent) 16%, transparent), transparent 60%), color-mix(in srgb, var(--fg) 4%, transparent);
  box-shadow: inset 0 1px 0 color-mix(in srgb, var(--glass-rim) 14%, transparent);
}

.inbox-left {
  display: flex;
  align-items: center;
  gap: 19px;
  min-width: 0;
}

.glyph {
  display: grid;
  place-items: center;
  flex: none;
  width: 55px;
  height: 55px;
  border-radius: 16px;
  color: var(--accent);
  background: var(--accent-soft);
}

.inbox-text {
  min-width: 0;
}

.inbox-text p {
  margin: 0;
}

.inbox h2 {
  margin: 5px 0 2px;
  font-size: 17px;
  line-height: 1.4;
  font-weight: 620;
  overflow-wrap: anywhere;
}

.inbox .meta {
  font-size: 12px;
  color: var(--fg-dim);
}

.inbox-right {
  flex: none;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 13px;
}

.local-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 11px;
  color: var(--success);
  white-space: nowrap;
}

.inbox-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.go,
.dismiss {
  min-height: 39px;
  padding: 0 15px;
  border-radius: 12px;
  font-size: 12px;
}

.arrival-batch {
  margin-top: 14px;
  padding: 18px;
  border: 1px solid var(--line);
  border-radius: 19px;
  background: color-mix(in srgb, var(--fg) 3%, transparent);
}

.arrival-batch-head,
.arrival-batch-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
}

.arrival-batch-head h2 {
  margin: 0 auto 0 0;
  font-size: 15px;
  font-weight: 600;
}

.arrival-check {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--fg-dim);
}

.arrival-options {
  max-height: 280px;
  overflow-y: auto;
  list-style: none;
  margin: 12px 0;
  padding: 0;
}

.arrival-option {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px;
  border-radius: 10px;
  cursor: pointer;
}

.arrival-option:hover,
.arrival-option.picked {
  background: var(--accent-soft);
}

.arrival-batch input {
  flex: none;
  width: 16px;
  height: 16px;
  accent-color: var(--accent);
}

.arrival-option span {
  min-width: 0;
}

.arrival-option strong,
.arrival-option small {
  display: block;
  overflow-wrap: anywhere;
}

.arrival-option strong { font-size: 13px; font-weight: 500; }
.arrival-option small { margin-top: 3px; font-size: 11px; color: var(--fg-dim); }
.arrival-batch-actions .minor { margin-right: auto; }

/* ---------- Continue Here ---------- */

.columns {
  display: grid;
  grid-template-columns: minmax(0, 1.55fr) minmax(250px, 0.7fr);
  gap: 32px;
  margin-top: 42px;
}

.section-line {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 12px;
  margin: 0 0 11px;
}

.section-line h2 {
  margin: 5px 0 0;
  font-size: 19px;
  line-height: 1.35;
  font-weight: 620;
}

.minor {
  font-size: 11px;
  color: var(--fg-dim);
  white-space: nowrap;
}

.action-row {
  width: 100%;
  justify-content: flex-start;
  gap: 16px;
  min-height: 72px;
  padding: 15px 7px;
  border: 0;
  border-bottom: 1px solid var(--line);
  border-radius: 10px;
  background: transparent;
  text-align: left;
}

.action-row:hover:not(:disabled) {
  border: 0;
  border-bottom: 1px solid var(--line);
  background: var(--accent-soft);
}

.action-icon {
  display: grid;
  place-items: center;
  flex: none;
  width: 42px;
  height: 42px;
  border-radius: 12px;
  color: var(--accent);
  background: var(--accent-soft);
}

.action-icon.soft {
  color: var(--fg-dim);
  background: color-mix(in srgb, var(--fg) 6%, transparent);
}

.action-copy {
  flex: 1;
  min-width: 0;
}

.action-copy strong,
.action-copy small {
  display: block;
  overflow-wrap: anywhere;
  text-wrap: pretty;
}

.action-copy strong {
  font-size: 14px;
  font-weight: 570;
  text-wrap: balance;
}

.action-copy small {
  margin-top: 3px;
  font-size: 11px;
  color: var(--fg-dim);
}

.saying .action-copy strong {
  font-weight: 500;
}

.row-arrow {
  flex: none;
  color: var(--fg-dim);
}

.side-note {
  align-self: start;
  padding: 23px 25px;
  border-top: 1px solid var(--line-strong);
  background: linear-gradient(170deg, color-mix(in srgb, var(--accent) 8%, transparent), transparent 68%);
}

.side-note h2 {
  margin: 11px 0;
  font-size: 20px;
  line-height: 1.34;
  letter-spacing: -0.02em;
}

.side-note p {
  margin: 0;
  font-size: 12px;
  line-height: 1.85;
  color: var(--fg-dim);
}

.divider {
  height: 1px;
  margin: 22px 0 15px;
  background: var(--line);
}

.shield {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 11px;
  color: var(--fg-dim);
}

.shield svg {
  flex: none;
  color: var(--accent);
}

/* ---------- Recent Tasks · Recent Conversations ---------- */

.lists {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 32px;
  margin-top: 40px;
}

.list-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--line);
}

.list-head h3 {
  margin: 0;
  font-size: 14px;
  font-weight: 620;
}

.lists ul {
  margin: 0;
  padding: 0;
  list-style: none;
}

.lists li {
  border-bottom: 1px solid var(--line);
}

.lists li :deep(.confirm) {
  margin: 0 0 10px;
}

.empty {
  margin: 14px 0 0;
  font-size: 12px;
  color: var(--fg-dim);
}

.empty.error {
  color: var(--danger);
}

.talk {
  display: flex;
  align-items: center;
  gap: 4px;
}

.list-row {
  flex: 1;
  min-width: 0;
  width: 100%;
  justify-content: flex-start;
  gap: 12px;
  min-height: 56px;
  padding: 8px 6px;
  border: 0;
  border-radius: 10px;
  background: transparent;
  text-align: left;
}

.list-row:hover:not(:disabled) {
  border: 0;
  background: var(--accent-soft);
}

.mark {
  display: grid;
  place-items: center;
  flex: none;
  width: 32px;
  height: 32px;
  border-radius: 10px;
  color: var(--success);
  background: color-mix(in srgb, var(--success) 12%, transparent);
}

.mark.undone {
  color: var(--fg-dim);
  background: color-mix(in srgb, var(--fg) 6%, transparent);
}

.mark.talk-mark {
  color: var(--accent);
  background: var(--accent-soft);
}

.list-copy {
  flex: 1;
  min-width: 0;
}

.list-copy strong,
.list-copy small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.list-copy strong {
  font-size: 13px;
  font-weight: 560;
}

.list-copy small {
  margin-top: 2px;
  font-size: 11px;
  color: var(--fg-dim);
}

.remove {
  flex: none;
  width: 32px;
  height: 32px;
  min-height: 32px;
}

@media (max-width: 959px) {
  .home-inner {
    padding: 27px 27px 22px;
  }

  .columns {
    gap: 15px;
  }
}

@media (max-width: 700px) {
  .columns,
  .lists {
    grid-template-columns: minmax(0, 1fr);
    gap: 20px;
  }

  .side-note {
    padding: 14px 16px;
  }

  .side-note h2 br {
    display: none;
  }

  .inbox {
    flex-direction: column;
    align-items: stretch;
  }

  .inbox-right {
    flex-direction: row;
    justify-content: space-between;
    align-items: center;
  }
}

@media (max-width: 550px) {
  .ai-home {
    flex: 1 0 auto;
    min-height: auto;
  }

  .home-scroll {
    flex: none;
    overflow: visible;
  }

  .home-inner {
    padding: 27px 18px;
  }

  .intro {
    margin-bottom: 24px;
  }

  .intro h1 {
    font-size: 24px;
  }

  .inbox {
    padding: 18px;
  }

  .inbox h2 {
    font-size: 15px;
  }

  .glyph {
    width: 40px;
    height: 40px;
  }

  .inbox-right {
    flex-wrap: wrap;
    gap: 10px;
  }

  .columns {
    margin-top: 29px;
  }
}
</style>
