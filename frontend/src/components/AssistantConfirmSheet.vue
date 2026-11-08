<script setup lang="ts">
// Final Confirmation: what the run will do in numbers — moved and
// renamed, kept, to the trash, never deleted — for the account it runs in.
// Confirm and Run hands the plan to Organize, which checks the drive's latest state
// item by item; while it runs the sheet stays up with the progress and Stop.
import { computed, ref, watch } from "vue";
import AssistantSheet from "./AssistantSheet.vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import { bytes } from "../format";
import { t } from "../i18n";
import { active } from "../organizer";
import { accountName, assistant, closePlanConfirm, planSummary, planReviewed, planUndecided, runPlan, stopPlanRun } from "../store";

const plan = computed(() => assistant.plan);
const summary = computed(() => planSummary.value);
const selected = computed(() => active(plan.value?.changes ?? []));
const trashOnly = computed(() => selected.value.length > 0 && selected.value.every((c) => c.op === "trash"));
const hasSelectedTrashFolders = computed(() => selected.value.some((c) => c.op === "trash" && c.item.folder));
const running = computed(() => assistant.running && !assistant.undoing);
const open = computed(() => !!plan.value && !!summary.value && (assistant.confirming || (running.value && assistant.screen === "plan")));

// The deepest new folders say the most; their parents are in their paths.
const leafFolders = computed(() => {
  const paths = (summary.value?.newFolders ?? []).map((p) => p.join(" / "));
  return paths.filter((p) => !paths.some((q) => q.startsWith(`${p} / `)));
});

const choice = ref<"keep" | "later" | "">("");
watch(open, () => { choice.value = ""; });
const busy = computed(() => !!assistant.busy || !!assistant.live || assistant.running || !!assistant.consent || !!assistant.connect);

const description = computed(() => {
  const p = plan.value;
  const s = summary.value;
  if (!p || !s) return "";
  // scopeName is data (All Files in Chinese for the root): the root is named in the page's language.
  const from = p.scope.length ? p.scopeName : t("全部文件");
  if (!s.selected) return t("本次仅记录决定，不会改动云盘文件。");
  if (trashOnly.value) return t("把「{from}」中选中的 {n} 项移到回收站，不会永久删除。", { from, n: s.selected });
  if (selected.value.some((c) => c.op !== "move")) return t("已选 {n} 项", { n: s.selected });
  if (p.kind === "media") {
    const moving = selected.value;
    const full = [...new Set(moving.map((c) => c.toPath.join(" / ")))];
    const tops = [...new Set(moving.map((c) => c.toPath[0] ?? ""))];
    const target = full.length === 1 ? full[0] : tops.length === 1 ? tops[0] : "";
    if (target) return t("把「{from}」中的选中项移动到「{target}」并规范命名。", { from, target });
    return t("把「{from}」中的选中项按作品放到各自的文件夹并规范命名。", { from });
  }
  return t("按计划移动或改名「{from}」中选中的 {n} 项。", { from, n: s.selected });
});

const percent = computed(() => {
  const { done, total } = assistant.progress;
  return total ? Math.min(100, Math.round((done / total) * 100)) : 0;
});
</script>

<template>
  <AssistantSheet v-if="open && plan && summary" labelledby="ai-confirm-title" :dismissable="!busy" @close="closePlanConfirm">
    <span class="sheet-eyebrow">{{ t("最后确认") }} / {{ accountName(assistant.account) }}</span>
    <h2 id="ai-confirm-title">
      {{ running
        ? (trashOnly ? t("正在移到回收站") : t("正在执行整理计划"))
        : (trashOnly ? t("把 {n} 项移到回收站？", { n: summary.selected }) : t("完成这份整理计划？")) }}
    </h2>
    <p class="sheet-lede">{{ description }}</p>
    <p v-if="hasSelectedTrashFolders" class="sheet-small">{{ t("文件夹移到回收站时会包含其中的全部内容。") }}</p>
    <div class="lines">
      <div><span>{{ t("移动与改名") }}</span><strong class="tnum">{{ t("{n} 项", { n: summary.moves }) }}</strong></div>
      <div><span>{{ t("已确认保留") }}</span><strong class="tnum">{{ t("{n} 项", { n: planReviewed.length }) }}</strong></div>
      <div><span>{{ t("未决定") }}</span><strong class="tnum">{{ t("{n} 项", { n: planUndecided.length }) }}</strong></div>
      <div v-if="summary.trash.count">
        <span>{{ t("移到回收站") }}</span>
        <strong class="tnum">{{ t("{n} 项", { n: summary.trash.count }) }} · {{ bytes(summary.trash.size, "0 B", 1) }}</strong>
      </div>
      <div v-if="summary.trashIfEmpty">
        <span>{{ t("执行后为空才移到回收站") }}</span>
        <strong class="tnum">{{ t("{n} 个文件夹", { n: summary.trashIfEmpty }) }}</strong>
      </div>
      <div><span>{{ t("永久删除") }}</span><strong class="tnum">{{ t("{n} 项", { n: 0 }) }}</strong></div>
    </div>
    <div v-if="summary.newFolders.length" class="folders">
      <span>{{ t("新建 {n} 个文件夹", { n: summary.newFolders.length }) }}</span>
      <strong v-for="path in leafFolders.slice(0, 3)" :key="path">{{ path }}</strong>
      <small v-if="leafFolders.length > 3">{{ t("另有 {n} 处", { n: leafFolders.length - 3 }) }}</small>
    </div>
    <p class="sheet-small">{{ t("执行前还会核对云盘最新状态；期间被改名、移走或删除的项会跳过，并在回执中说明。") }}</p>
    <fieldset v-if="planUndecided.length && !running" class="decision-options" :disabled="busy">
      <legend>{{ t("请选择如何处理未决定的项") }}</legend>
      <label><input v-model="choice" type="radio" name="plan-undecided" value="keep" /> <span>{{ t("保留并结束提醒") }}</span></label>
      <label><input v-model="choice" type="radio" name="plan-undecided" value="later" /> <span>{{ t("留待下次处理") }}</span></label>
    </fieldset>

    <div v-if="running" class="progress" role="status">
      <div class="bar" aria-hidden="true"><i :style="{ width: `${percent}%` }" /></div>
      <span class="tnum">
        <Orbit :size="14" />
        {{ assistant.stopping ? t("正在停止…") : t("正在整理 {done} / {total}", { done: assistant.progress.done, total: assistant.progress.total }) }}
      </span>
    </div>
    <div class="sheet-actions">
      <template v-if="running">
        <button class="sheet-btn" type="button" :disabled="assistant.stopping" data-autofocus @click="stopPlanRun()">
          <Icon name="stop" :size="15" />
          {{ assistant.stopping ? t("正在停止…") : t("停止") }}
        </button>
      </template>
      <template v-else>
        <button class="sheet-btn" type="button" :disabled="busy" data-autofocus @click="closePlanConfirm()">{{ t("继续检查") }}</button>
        <button class="sheet-btn primary ai-confirm-run" type="button" :disabled="busy || (!!planUndecided.length && !choice)" @click="runPlan(choice === 'keep')">
          {{ trashOnly ? t("确认移到回收站") : t("确认完成") }}
          <Icon name="arrowRight" :size="15" />
        </button>
      </template>
    </div>
  </AssistantSheet>
</template>

<style scoped>
.decision-options {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin: 18px 0;
  padding: 14px;
  border: 1px solid var(--line);
  border-radius: 12px;
  font-size: 12px;
}

.decision-options legend { padding: 0 4px; color: var(--fg-dim); }
.decision-options label { display: flex; align-items: flex-start; gap: 8px; }
.decision-options input { flex: none; margin-top: 2px; }

.lines {
  margin: 20px 0;
  padding: 6px 0;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
}

.lines div {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 8px 0;
  font-size: 12px;
}

.lines span {
  color: var(--fg-dim);
}

.lines strong {
  font-weight: 600;
  text-align: right;
}

.folders {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12px;
}

.folders span,
.folders small {
  color: var(--fg-dim);
}

.folders strong {
  padding-left: 12px;
  font-weight: 600;
  overflow-wrap: anywhere;
}

.progress {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 18px;
  font-size: 12px;
}

.progress > span {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.bar {
  height: 4px;
  border-radius: 2px;
  background: color-mix(in srgb, var(--fg) 10%, transparent);
  overflow: hidden;
}

.bar i {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--accent);
  transition: width var(--dur) var(--ease);
}
</style>
