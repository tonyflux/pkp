<script setup lang="ts">
// Execution Receipt: one run as Go recorded it — what was
// done, what was skipped and why, what the plan left alone — and Undo,
// which Go checks item by item again before it moves anything back.
import { computed } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import { stamp } from "../format";
import { t, num } from "../i18n";
import { accountName, assistant, assistantHome, openConversation, recordCounts, stopPlanRun, undoAssistantRecord } from "../store";

const record = computed(() => assistant.receipt);
const counts = computed(() => (record.value ? recordCounts(record.value) : null));
const account = computed(() => accountName(assistant.account));

const headline = computed(() => {
  const r = record.value;
  const n = counts.value;
  if (!r || !n) return "";
  if (r.undo) return r.undo.stopped ? t("撤销已停止，完成的部分已回到原处。") : t("这次整理已撤销。");
  if (r.stopped) return t("已停止：{n} 项已按计划整理。", { n: n.done });
  return t("{n} 项已按计划整理。", { n: n.done });
});

const lede = computed(() => {
  const r = record.value;
  if (!r) return "";
  const scope = (r.scope || "全部文件").replace(/^全部文件 \/ /, ""); // i18n-ignore
  const shown = scope === "全部文件" ? t("全部文件") : scope; // i18n-ignore
  return [stamp(r.at, ""), `${account.value} / ${shown}`, r.task].filter(Boolean).join(" · ");
});

const skipped = computed(() => (record.value?.items ?? []).filter((i) => !!i.error));
const undoSkipped = computed(() => record.value?.undo?.skipped ?? []);
const canUndo = computed(() => !!record.value && !record.value.undo && !!counts.value?.done && !assistant.running);
const undoing = computed(() => assistant.running && assistant.undoing);
const conversation = computed(() => assistant.conversation);
</script>

<template>
  <div class="ai-receipt">
    <div class="receipt-scroll">
      <div v-if="record && counts" class="receipt-inner">
        <span class="receipt-icon" :class="{ undone: !!record.undo }"><Icon :name="record.undo ? 'restore' : 'check'" :size="27" /></span>
        <span class="eyebrow">{{ t("{account} · 回执", { account }) }}</span>
        <h1>{{ headline }}</h1>
        <p class="lede">{{ lede }}</p>

        <div class="stats">
          <div>
            <strong class="tnum">{{ num(counts.done) }}</strong>
            <span>{{ t("stat|项成功") }}</span>
          </div>
          <div>
            <strong class="tnum">{{ num(counts.skipped) }}</strong>
            <span>{{ t("项跳过") }}</span>
          </div>
          <div>
            <strong class="tnum">{{ num(assistant.receiptKept) }}</strong>
            <span>{{ t("项保持原样") }}</span>
          </div>
        </div>

        <section v-if="skipped.length" class="skipped" aria-labelledby="ai-receipt-skipped">
          <h2 id="ai-receipt-skipped">{{ t("{n} 项在执行前发生变化或无法执行，已跳过", { n: skipped.length }) }}</h2>
          <ul>
            <li v-for="item in skipped.slice(0, 20)" :key="item.fileId">
              <strong :title="item.name">{{ item.name }}</strong>
              <span>{{ item.error }}</span>
            </li>
          </ul>
          <p v-if="skipped.length > 20" class="more">{{ t("另有 {n} 项", { n: skipped.length - 20 }) }}</p>
        </section>

        <section v-if="record.undo" class="skipped undo-result" aria-labelledby="ai-receipt-undo">
          <h2 id="ai-receipt-undo">
            {{ t("已恢复 {restored} 项回收站中的项目，移回 {moved} 项，{removed} 个新建的空文件夹已移到回收站。", { restored: record.undo.restored, moved: record.undo.moved, removed: record.undo.removed }) }}
          </h2>
          <template v-if="undoSkipped.length">
            <p>{{ t("{n} 项没有撤销：", { n: undoSkipped.length }) }}</p>
            <ul>
              <li v-for="item in undoSkipped.slice(0, 20)" :key="item.fileId">
                <strong :title="item.name">{{ item.name }}</strong>
                <span>{{ item.error }}</span>
              </li>
            </ul>
            <p v-if="undoSkipped.length > 20" class="more">{{ t("另有 {n} 项", { n: undoSkipped.length - 20 }) }}</p>
          </template>
        </section>

        <div v-if="!record.undo" class="receipt-action">
          <div>
            <strong>{{ t("改变主意了？") }}</strong>
            <p>{{ t("撤销前会重新核对文件的位置和名称；遇到冲突的项会跳过并说明原因。") }}</p>
          </div>
          <div v-if="undoing" class="progress tnum" role="status">
            <Orbit :size="15" />
            {{ assistant.stopping ? t("正在停止…") : t("正在撤销 {done} / {total}", { done: assistant.progress.done, total: assistant.progress.total }) }}
            <button type="button" :disabled="assistant.stopping" @click="stopPlanRun()">{{ t("停止") }}</button>
          </div>
          <button v-else class="undo" type="button" :disabled="!canUndo" @click="undoAssistantRecord()">
            <Icon name="restore" :size="15" />
            {{ t("撤销本次整理") }}
          </button>
        </div>

        <div class="foot">
          <span>{{ t("回收站中的项目可以恢复；本次没有永久删除任何文件。") }}</span>
          <span class="foot-actions">
            <button v-if="conversation" class="quiet back" type="button" :disabled="assistant.running" @click="openConversation(conversation.id)">{{ t("返回对话") }}</button>
            <button class="quiet back" type="button" :disabled="assistant.running" @click="assistantHome()">{{ t("返回") }}</button>
          </span>
        </div>
      </div>
      <p v-else class="none">{{ t("这份回执已不在这台电脑上。") }}</p>
    </div>
  </div>
</template>

<style scoped>
.ai-receipt {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.eyebrow,
h1,
h2,
p,
.stats strong,
.stats span,
.skipped li strong,
.skipped li span,
.receipt-action > div > strong,
.foot > span:first-child {
  -webkit-user-select: text;
  user-select: text;
  --default-contextmenu: show;
}

.receipt-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;
}

.receipt-inner {
  max-width: 700px;
  margin: 0 auto;
  padding: 80px 30px 40px;
  text-align: center;
}

.receipt-icon {
  display: grid;
  place-items: center;
  width: 52px;
  height: 52px;
  margin: 0 auto 19px;
  border-radius: 16px;
  color: var(--success);
  background: color-mix(in srgb, var(--success) 13%, transparent);
}

.receipt-icon.undone {
  color: var(--accent);
  background: var(--accent-soft);
}

.eyebrow {
  font-size: 11px;
  font-weight: 650;
  letter-spacing: 0.08em;
  color: var(--accent);
}

h1 {
  margin: 12px 0 9px;
  font-size: 28px;
  line-height: 1.25;
  font-weight: 650;
  letter-spacing: -0.025em;
  overflow-wrap: anywhere;
}

.lede {
  margin: 0;
  font-size: 13px;
  line-height: 1.7;
  color: var(--fg-dim);
  overflow-wrap: anywhere;
}

.stats {
  display: flex;
  justify-content: center;
  gap: 55px;
  margin: 35px 0;
  padding: 25px 0;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
}

.stats div {
  display: flex;
  flex-direction: column;
}

.stats strong {
  font-size: 25px;
}

.stats span {
  font-size: 11px;
  color: var(--fg-dim);
}

.skipped {
  margin: 0 0 25px;
  text-align: left;
}

.skipped h2 {
  margin: 0 0 8px;
  font-size: 13px;
  font-weight: 600;
  line-height: 1.6;
}

.skipped p {
  margin: 0 0 6px;
  font-size: 12px;
  color: var(--fg-dim);
}

.skipped ul {
  margin: 0 0 6px;
  padding: 0;
  list-style: none;
  border-top: 1px solid var(--line);
}

.skipped li {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 0;
  border-bottom: 1px solid var(--line);
  font-size: 12px;
}

.skipped li strong {
  font-weight: 560;
  overflow-wrap: anywhere;
}

.skipped li span {
  color: var(--danger);
  overflow-wrap: anywhere;
}

.receipt-action {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  margin-bottom: 25px;
  text-align: left;
}

.receipt-action strong {
  font-size: 14px;
}

.receipt-action p {
  margin: 5px 0 0;
  font-size: 11px;
  color: var(--fg-dim);
}

.undo,
.progress button {
  flex: none;
  min-height: 39px;
  padding: 0 14px;
  border-radius: 11px;
  font-size: 12px;
}

.progress {
  flex: none;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}

.foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  padding-top: 16px;
  border-top: 1px solid var(--line);
  font-size: 11px;
  color: var(--fg-dim);
  text-align: left;
}

.foot-actions {
  display: flex;
  gap: 4px;
}

.back {
  min-height: 32px;
  border-radius: 9px;
  font-size: 12px;
  color: var(--accent);
}

.none {
  margin: 48px 0;
  text-align: center;
  color: var(--fg-dim);
}

@media (max-width: 700px) {
  .receipt-inner {
    padding: 48px 22px 32px;
  }
}

@media (max-width: 550px) {
  .ai-receipt {
    flex: 1 0 auto;
    min-height: auto;
  }

  .receipt-scroll {
    flex: none;
    overflow: visible;
  }

  .receipt-inner {
    padding: 36px 16px 24px;
  }

  h1 {
    font-size: 24px;
  }

  .stats {
    gap: 25px;
  }

  .receipt-action {
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
