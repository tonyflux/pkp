<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { t } from "../i18n";
import { useRowWindows } from "../windowing";
import {
  dismissFileTask,
  fileTaskActiveCount,
  fileTaskUnseen,
  fileTasks,
  retryFileTask,
  stopFileTask,
  taskCounts,
  taskFinished,
  type FileTask,
  type FileTaskItem,
  type FileTaskItemState,
} from "../filetasks";
import ConfirmInline from "./ConfirmInline.vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";

const toggle = ref<HTMLButtonElement | null>(null);
const confirmation = ref<HTMLElement | null>(null);
const scroller = ref<HTMLElement | null>(null);
const retry = ref<{ taskId: string; itemIds: string[] } | null>(null);
let retryTrigger: HTMLElement | null = null;
let retryRowKey = "";
let restoringScroll = false;
let scrollRevision = 0;
const scrollPositions = new Map<string, number>();
const history = computed(() => [...fileTasks.items].sort((a, b) => b.createdAt - a.createdAt));
const activeCount = fileTaskActiveCount;
const selected = computed(() => fileTasks.items.find((task) => task.id === fileTasks.selectedId)
  ?? history.value.find((task) => !taskFinished(task)) ?? history.value[0]);
const counts = computed(() => selected.value ? taskCounts(selected.value) : null);
const retryable = computed(() => selected.value?.items.filter((item) => item.state === "failed" && !item.retried) ?? []);
const processed = computed(() => selected.value?.processed ?? 0);
const panel = ref<HTMLElement | null>(null);
let hideTimer: ReturnType<typeof setTimeout> | undefined;

function armAutoHide() {
  clearTimeout(hideTimer);
  if (!fileTasks.visible || fileTasks.open || activeCount.value || !selected.value || selected.value.phase !== "done") return;
  hideTimer = setTimeout(() => {
    if (!fileTasks.visible || fileTasks.open || activeCount.value || selected.value?.phase !== "done") return;
    if (panel.value?.matches(":hover, :focus-within")) return;
    fileTasks.visible = false;
  }, 5000);
}

function hidePanel() {
  fileTasks.open = false;
  fileTasks.visible = false;
  retry.value = null;
  nextTick(() => document.querySelector<HTMLButtonElement>("[data-file-task-entry]")?.focus());
}

watch(() => [fileTasks.visible, fileTasks.open, activeCount.value, selected.value?.id, selected.value?.phase] as const, armAutoHide, { immediate: true });
watch(() => [fileTasks.visible, fileTasks.open, selected.value?.id, selected.value?.phase] as const, () => {
  if (fileTasks.visible && fileTasks.open && selected.value && taskFinished(selected.value)) selected.value.viewed = true;
}, { immediate: true });
onBeforeUnmount(() => clearTimeout(hideTimer));
const rowKey = (item: FileTaskItem) => `${selected.value?.id}:${item.id}`;
const { windows, reveal } = useRowWindows<FileTaskItem>({
  scroller,
  lists: () => [{ id: "receipt", items: selected.value?.items ?? [] }],
  key: rowKey,
  shape: (item) => `${Math.ceil(item.name.length / 40)}:${Math.ceil(item.error.length / 60)}`,
});
const drawn = computed(() => windows.value.receipt ?? { rows: [], style: {} });
const retryTask = computed(() => retry.value ? fileTasks.items.find((task) => task.id === retry.value!.taskId) : undefined);
const retryItems = computed(() => retryTask.value?.items.filter((item) => item.state === "failed" && !item.retried && retry.value?.itemIds.includes(item.id)) ?? []);
const retryMessage = computed(() => {
  const task = retryTask.value;
  if (!task) return "";
  let message = t("filetask|在账号「{account}」重新执行「{operation}」，仅重试 {n} 个确认未生效的失败项。", {
    account: task.accountName, operation: task.label, n: retryItems.value.length,
  });
  if (task.destination) message += " " + t("filetask|原目标：{destination}。", { destination: task.destination });
  if (task.action === "delete" || task.action === "empty") message += " " + t("filetask|这是永久删除，无法撤销，也不会移入回收站。");
  return message;
});

function phaseLabel(task: FileTask): string {
  if (task.stopping && !taskFinished(task)) return t("filetask|正在停止提交，等待已提交项目结算");
  switch (task.phase) {
    case "queued": return t("filetask|已排队，尚未提交");
    case "submitting": return t("filetask|正在提交");
    case "processing": return t("filetask|云端处理中");
    case "verifying": return t("filetask|正在核对结果");
    case "done": return task.action === "download" ? t("filetask|已加入传输队列") : t("filetask|全部完成");
    case "partial": return !taskCounts(task).done && taskCounts(task).unknown
      ? t("filetask|结果待确认") : t("filetask|部分完成，请查看回执");
    case "failed": return t("filetask|未完成，请查看回执");
    case "stopped": return t("filetask|已停止剩余提交");
  }
}

function itemLabel(state: FileTaskItemState): string {
  switch (state) {
    case "pending": return t("filetask|待处理");
    case "done": return t("filetask|已完成");
    case "failed": return t("filetask|未生效，可重试");
    case "unknown": return t("filetask|结果待确认，请先检查");
    case "unprocessed": return t("filetask|未提交");
  }
}

function itemIcon(state: FileTaskItemState): string {
  return { pending: "clock", done: "check", failed: "alert", unknown: "info", unprocessed: "minus" }[state];
}

function processedLabel(task: FileTask): string {
  return t("filetask|已处理 {done} / {total} 项", { done: task.processed, total: task.items.length });
}

function choose(task: FileTask) {
  fileTasks.selectedId = task.id;
}

function collapse() {
  fileTasks.open = false;
  retry.value = null;
  nextTick(() => toggle.value?.focus());
}

async function cancelRetry() {
  retry.value = null;
  await nextTick();
  if (retryTrigger?.isConnected) retryTrigger.focus();
  else if (retryRowKey) {
    const row = await reveal(retryRowKey);
    (row?.querySelector<HTMLButtonElement>("[data-file-task-retry]") ?? toggle.value)?.focus();
  } else toggle.value?.focus();
}

function requestRetry(task: FileTask, event: MouseEvent, itemId?: string) {
  if (!taskFinished(task)) return;
  const itemIds = task.items.filter((item) => item.state === "failed" && !item.retried && (!itemId || item.id === itemId)).map((item) => item.id);
  if (!itemIds.length) return;
  retryTrigger = event.currentTarget as HTMLElement;
  retryRowKey = itemId ? `${task.id}:${itemId}` : "";
  retry.value = { taskId: task.id, itemIds };
  nextTick(() => confirmation.value?.focus());
}
function confirmRetry() {
  const task = retryTask.value;
  const itemIds = retryItems.value.map((item) => item.id);
  if (!task || !taskFinished(task) || !itemIds.length) return cancelRetry();
  retryFileTask(task.id, itemIds);
  retry.value = null;
  nextTick(() => toggle.value?.focus());
}

function dismiss(task: FileTask) {
  if (!taskFinished(task)) return;
  dismissFileTask(task.id);
  nextTick(() => {
    scrollPositions.delete(task.id);
    if (fileTasks.visible) toggle.value?.focus();
    else document.querySelector<HTMLElement>("[data-file-task-entry], .nav-buttons button:not(:disabled), .search input")?.focus();
  });
}

function escape(event: KeyboardEvent) {
  if (!retry.value && !fileTasks.open) return;
  event.stopPropagation();
  if (retry.value) cancelRetry();
  else if (activeCount.value) collapse();
  else hidePanel();
}

function rememberScroll() {
  if (!restoringScroll && fileTasks.open && selected.value) {
    scrollPositions.set(selected.value.id, scroller.value?.scrollTop ?? 0);
  }
}

watch(() => [selected.value?.id, fileTasks.open] as const, async ([id, open], [previousId, wasOpen]) => {
  if (previousId && wasOpen && !restoringScroll) {
    scrollPositions.set(previousId, scroller.value?.scrollTop ?? 0);
  }
  retry.value = null;
  const revision = ++scrollRevision;
  restoringScroll = true;
  // Restore after the window helper has settled its measured-height anchor.
  await nextTick();
  await nextTick();
  if (revision !== scrollRevision) return;
  if (open && id && scroller.value) scroller.value.scrollTop = scrollPositions.get(id) ?? 0;
  restoringScroll = false;
});
</script>

<template>
  <section v-show="fileTasks.visible && selected && counts" ref="panel" class="file-tasks glass" data-file-tasks :class="{ expanded: fileTasks.open }"
    :aria-label="t('filetask|后台文件任务')" @keydown.esc="escape" @mouseleave="armAutoHide" @focusout="armAutoHide">
    <header v-if="selected && counts" class="task-strip">
      <button ref="toggle" class="quiet task-toggle" data-file-task-toggle :aria-expanded="fileTasks.open"
        aria-controls="file-task-details" :aria-label="fileTasks.open ? t('filetask|收起任务回执') : t('filetask|展开任务回执')"
        @click="fileTasks.open ? collapse() : fileTasks.open = true">
        <Orbit v-if="!taskFinished(selected) && selected.phase !== 'queued'" :size="18" :label="phaseLabel(selected)" />
        <Icon v-else :name="selected.phase === 'done' ? 'check' : taskFinished(selected) ? 'list' : 'clock'" :size="18" />
        <span class="strip-identity">
          <strong>{{ selected.label }}</strong>
          <small :title="selected.accountName + (selected.destination ? ' · ' + selected.destination : '')">
            {{ selected.accountName }}<template v-if="selected.destination"> · {{ selected.destination }}</template>
          </small>
        </span>
        <Icon :name="fileTasks.open ? 'chevronDown' : 'chevronUp'" :size="15" />
      </button>
      <div class="strip-status" role="status" aria-live="polite" aria-atomic="true">
        <span class="phase">{{ phaseLabel(selected) }}</span>
        <span class="processed">{{ processedLabel(selected) }}</span>
        <progress :value="processed" :max="Math.max(counts.total, 1)" :aria-label="t('filetask|已处理项目进度')"
          :aria-valuetext="processedLabel(selected)" />
      </div>
      <span class="session-count" :title="t('filetask|本次会话保留 {n} 条任务回执', { n: history.length })">
        {{ activeCount ? t('filetask|{n} 项任务进行中', { n: activeCount }) : t('filetask|{n} 条回执', { n: history.length }) }}
      </span>
      <button v-if="!taskFinished(selected)" class="quiet strip-stop" data-file-task-stop :disabled="selected.stopping"
        :title="t('filetask|只停止尚未提交的项目；已提交的操作会继续结算，不会撤销。')" @click="stopFileTask(selected.id)">
        <Icon name="stop" :size="15" />{{ selected.stopping ? t('filetask|正在停止') : t('filetask|停止剩余提交') }}
      </button>
      <button v-if="!activeCount" class="quiet icon" data-file-task-hide :aria-label="t('filetask|收起文件任务')"
        :title="t('filetask|收起文件任务')" @click="hidePanel"><Icon name="close" :size="16" /></button>
    </header>

    <div v-show="fileTasks.open" id="file-task-details" class="task-details">
      <nav class="task-history" :aria-label="t('filetask|本次会话任务历史')">
        <button v-for="task in history" :key="task.id" class="quiet history-entry" data-file-task-history
          :data-task-id="task.id" :aria-pressed="selected?.id === task.id" @click="choose(task)">
          <strong>{{ task.label }}</strong>
          <small>{{ task.accountName }}</small>
          <span>{{ phaseLabel(task) }}</span>
          <small v-if="fileTaskUnseen.includes(task)" class="history-attention">{{ t('filetask|待查看') }}</small>
          <small>{{ processedLabel(task) }}</small>
        </button>
      </nav>
      <div ref="scroller" class="task-receipt" data-file-task-receipt :data-task-id="selected?.id" tabindex="0"
        :aria-label="t('filetask|逐项操作回执')" @scroll.passive="rememberScroll">
        <template v-if="selected && counts">
        <div class="receipt-heading">
          <div class="receipt-identity">
            <strong>{{ selected.label }}</strong>
            <span>{{ t('filetask|原账号：{account}', { account: selected.accountName }) }}</span>
            <span v-if="selected.destination">{{ t('filetask|原目标：{destination}。', { destination: selected.destination }) }}</span>
          </div>
          <button v-if="taskFinished(selected)" class="quiet remove-receipt" data-file-task-dismiss
            @click="dismiss(selected)">{{ t('filetask|移除当前回执') }}</button>
          <button v-if="taskFinished(selected) && retryable.length && !retry" class="quiet retry-all" data-file-task-retry-all
            @click="requestRetry(selected, $event)">{{ t('filetask|重试全部失败项（{n}）', { n: retryable.length }) }}</button>
        </div>
        <div class="receipt-counts" :aria-label="t('filetask|回执统计')">
          <span>{{ t('filetask|完成 {n}', { n: counts.done }) }}</span>
          <span v-if="counts.failed" class="failed">{{ t('filetask|失败 {n}', { n: counts.failed }) }}</span>
          <span v-if="counts.unknown" class="unknown">{{ t('filetask|待确认 {n}', { n: counts.unknown }) }}</span>
          <span v-if="counts.unprocessed">{{ t('filetask|未提交 {n}', { n: counts.unprocessed }) }}</span>
        </div>
        <p v-if="selected.action === 'download' && counts.done" class="receipt-note">
          {{ t('下载已加入传输队列，实际下载进度请到传输中心查看。') }}
        </p>
        <p v-if="!taskFinished(selected) || selected.phase === 'stopped'" class="receipt-note">
          {{ t('filetask|只停止尚未提交的项目；已提交的操作会继续结算，不会撤销。') }}
        </p>
        <p v-if="counts.unknown" class="receipt-note unknown">
          {{ t('filetask|部分结果无法确认，可能已生效。请先检查云端结果；这些项目不能直接重试。') }}
        </p>
        <p v-if="selected.error" class="action-error" role="alert">{{ selected.error }}</p>
        <div v-if="retry && retryTask?.id === selected.id" ref="confirmation" class="retry-confirmation" data-file-task-confirm
          tabindex="-1" role="group" :aria-label="t('filetask|确认重试失败项')">
          <ConfirmInline :message="retryMessage" :confirm-label="t('filetask|确认重试 {n} 项', { n: retryItems.length })"
            :danger="selected.action === 'delete' || selected.action === 'empty'" @confirm="confirmRetry" @cancel="cancelRetry" />
        </div>
        <ul class="receipt-items" data-list="receipt" :style="drawn.style" :aria-label="t('filetask|逐项操作回执')">
          <li v-for="{ item, index, style } in drawn.rows" :key="rowKey(item)" class="receipt-item" :class="item.state"
            :style="style" :data-row="rowKey(item)" :aria-posinset="index + 1" :aria-setsize="selected.items.length" tabindex="0"
            data-file-task-item :data-item-id="item.id" :data-state="item.state">
            <Icon :name="itemIcon(item.state)" :size="16" />
            <div class="item-copy">
              <strong>{{ item.name }}</strong>
              <span class="item-state">{{ item.retried ? t('filetask|已重新提交，请查看新回执') : item.state === 'done' && selected.action === 'download' ? t('filetask|已加入传输队列') : itemLabel(item.state) }}</span>
              <p v-if="item.error" class="item-error">{{ item.error }}</p>
            </div>
            <button v-if="taskFinished(selected) && item.state === 'failed' && !item.retried && !retry" class="quiet item-retry" data-file-task-retry
              :aria-label="t('filetask|重试失败项「{name}」', { name: item.name })" @click="requestRetry(selected, $event, item.id)">
              <Icon name="restore" :size="14" />{{ t('重试') }}
            </button>
          </li>
        </ul>
        </template>
      </div>
    </div>
  </section>
</template>

<style scoped>
.file-tasks {
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  min-width: 0;
  max-height: min(440px, 52vh);
  overflow: hidden;
  border-radius: 18px;
  font-size: 13px;
}
.task-strip { flex: none; display: flex; align-items: center; gap: 12px; min-width: 0; padding: 8px 12px; }
.task-toggle { flex: 1 1 180px; justify-content: flex-start; gap: 10px; min-width: 0; padding: 4px 6px; text-align: left; }
.task-toggle > :not(.strip-identity) { flex: none; }
.strip-identity { display: flex; flex: 1; flex-direction: column; gap: 2px; min-width: 0; }
.strip-identity strong { font-size: 13px; font-weight: 600; }
.strip-identity :is(strong, small) { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.strip-identity small { color: var(--fg-dim); font-size: 12px; }
.strip-status { flex: 0 1 260px; display: grid; gap: 3px; min-width: 0; }
.phase { color: var(--fg); overflow-wrap: anywhere; }
.processed { color: var(--fg-dim); font-variant-numeric: tabular-nums; }
progress { display: block; appearance: none; width: 100%; height: 3px; border: 0; border-radius: 4px; overflow: hidden; background: var(--line-strong); color: var(--accent); }
progress::-webkit-progress-bar { background: var(--line-strong); }
progress::-webkit-progress-value { background: var(--accent); }
progress::-moz-progress-bar { background: var(--accent); }
.session-count { flex: none; color: var(--fg-dim); font-size: 12px; }
.strip-stop { flex: none; gap: 5px; font-size: 12px; white-space: normal; }
.task-details { flex: 1 1 auto; display: grid; grid-template-columns: minmax(140px, 190px) minmax(0, 1fr); min-height: 0; border-top: 1px solid var(--line); overflow: hidden; background: var(--canvas-flat); }
.task-history { display: flex; flex-direction: column; gap: 4px; min-height: 0; padding: 8px; overflow: auto; border-right: 1px solid var(--line); }
.history-entry { flex: none; display: flex; flex-direction: column; align-items: stretch; justify-content: flex-start; gap: 3px; min-width: 0; padding: 9px; border-radius: 10px; text-align: left; font-size: 12px; white-space: normal; overflow-wrap: anywhere; }
.history-entry strong { font-size: 12px; }
.history-entry small { color: var(--fg-dim); font-size: 12px; }
.history-entry .history-attention { color: var(--warning); font-weight: 600; }
.history-entry[aria-pressed="true"], .history-entry[aria-pressed="true"]:hover { background: var(--accent-soft); color: var(--accent); }
.task-receipt { min-width: 0; min-height: 0; padding: 12px 14px; overflow: auto; overscroll-behavior: contain; scrollbar-width: thin; }
.receipt-heading { display: flex; align-items: flex-start; justify-content: space-between; flex-wrap: wrap; gap: 8px; }
.receipt-identity { flex: 1 1 180px; display: flex; flex-direction: column; gap: 4px; min-width: 0; overflow-wrap: anywhere; }
.receipt-identity strong { font-size: 13px; }
.receipt-identity span { color: var(--fg-dim); }
.remove-receipt { font-size: 12px; }
.retry-all { max-width: 100%; font-size: 12px; white-space: normal; }
.receipt-counts { display: flex; flex-wrap: wrap; gap: 6px 14px; margin-top: 10px; color: var(--fg-dim); font-variant-numeric: tabular-nums; }
.receipt-note { margin: 8px 0 0; color: var(--fg-dim); font-size: 12px; line-height: 1.5; overflow-wrap: anywhere; }
.failed, .action-error { color: var(--danger); }
.unknown { color: var(--warning); }
.action-error { margin: 8px 0 0; overflow-wrap: anywhere; }
.retry-confirmation { margin-top: 10px; border-radius: 6px; overflow-wrap: anywhere; }
.receipt-items { position: relative; list-style: none; margin: 10px 0 0; padding: 0; }
.receipt-item { display: flex; align-items: flex-start; gap: 9px; padding: 9px 0; border-top: 1px solid var(--line); }
.receipt-item > :first-child { flex: none; margin-top: 2px; }
.receipt-item.done > :first-child { color: var(--accent); }
.receipt-item:is(.pending, .unprocessed) > :first-child { color: var(--fg-dim); }
.item-copy { flex: 1; display: flex; flex-direction: column; gap: 3px; min-width: 0; line-height: 1.4; overflow-wrap: anywhere; }
.item-copy strong { color: var(--fg); font-weight: 500; }
.item-state { font-size: 12px; color: var(--fg-dim); }
.receipt-item.failed .item-state { color: var(--danger); }
.receipt-item.unknown .item-state { color: var(--warning); }
.item-error { margin: 0; color: var(--fg-dim); font-size: 12px; white-space: pre-wrap; }
.item-retry { flex: none; gap: 4px; min-height: 28px; padding: 3px 7px; font-size: 12px; }
.file-tasks :is(button, [tabindex]):focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
@media (max-width: 1000px) {
  .session-count { display: none; }
}
@media (max-width: 680px) {
  .task-strip { flex-wrap: wrap; gap: 6px 8px; padding: 8px; }
  .task-toggle { flex: 1 1 calc(100% - 150px); }
  .strip-status { order: 2; flex: 1 1 100%; grid-template-columns: minmax(0, 1fr) auto; gap: 4px 8px; padding: 0 6px; }
  .strip-status progress { grid-column: 1 / -1; }
  .strip-stop { max-width: 145px; padding: 5px 8px; }
  .task-details { display: flex; flex-direction: column; }
  .task-history { flex: none; flex-direction: row; max-height: 92px; border-right: 0; border-bottom: 1px solid var(--line); }
  .history-entry { flex: 0 0 150px; padding: 6px 8px; }
  .history-entry > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .history-entry > small:last-child { display: none; }
  .task-receipt { flex: 1 1 auto; padding: 10px; }
}
@media (max-height: 620px) {
  .file-tasks { max-height: 56vh; }
}
</style>
