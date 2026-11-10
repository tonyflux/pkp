<script setup lang="ts">
import { computed, ref, watch } from "vue";
import Icon from "./Icon.vue";
import ConfirmInline from "./ConfirmInline.vue";
import { bytes, stamp } from "../format";
import { N_, t } from "../i18n";
import { stateLabel as transferStateLabel, transferState } from "../transfers";
import type { CardRow, OperationCard, ResourcesCard, ResourceRow } from "../agent";
import { assistant, discardCard, runCard, stopAssistantOperation } from "../store";

const props = defineProps<{ card: OperationCard | ResourcesCard; turnId: string; live?: boolean }>();
const expanded = ref(false);
const resultsExpanded = ref(false);
const confirming = ref(false);
const locked = computed(() => !!props.live || !!assistant.busy || assistant.running || props.card.state === "running");
const operation = computed(() => props.card.kind === "operation" ? props.card : null);
const shown = computed(() => expanded.value ? props.card.rows : props.card.rows.slice(0, 6));
const shownResults = computed(() => {
  const results = operation.value?.results ?? [];
  return resultsExpanded.value ? results : results.slice(0, 6);
});
const destructive = computed(() => !!operation.value && ["cancel_download", "cancel_upload", "remove_download", "remove_upload", "remove_cloud_task", "cancel_share", "restart_download"].includes(operation.value.action));
const needsConfirmation = computed(() => destructive.value || (operation.value?.targetIds.length ?? 0) > 1);
const settings = computed(() => !!operation.value && ["upload_files", "upload_folder", "upload_torrent", "extract_archive"].includes(operation.value.action));
const confirmLabel = computed(() => settings.value ? t("继续设置") : t("确认执行"));
const destination = computed(() => !!operation.value && ["create_folder", "copy", "upload_files", "upload_folder", "upload_torrent"].includes(operation.value.action));
const consequence = computed(() => {
  switch (operation.value?.action) {
    case "copy": return t("复制文件夹会包含其中的所有子项，原文件保持不变；提交后由云端继续处理。");
    case "restore": return t("将这些项目从回收站恢复，不会永久删除文件。");
    case "cancel_download": return t("取消下载会删除已下载的本地分片，云端文件保持不变。");
    case "restart_download": return t("将重新开始下载；已有下载进度可能被丢弃。");
    case "cancel_upload": return t("取消上传会丢弃未完成的上传进度。");
    case "remove_cloud_task": return t("仅移除云添加任务，保留已保存的云端文件。");
    case "remove_download": return t("移除下载记录会停止未完成的下载，并丢弃本地分片和续传进度；已完成的本地文件及云端文件保留。");
    case "remove_upload": return t("移除上传记录会停止未完成的上传，并丢弃云端未完成的数据和续传进度；本地源文件及已上传完成的云端文件保留。");
    case "cancel_share": return t("取消分享后，对应的外部分享链接将失效。");
    case "upload_files":
    case "upload_folder": return t("确认后打开本地选择器，只上传你明确选择的文件或文件夹。");
    case "upload_torrent": return t("确认后打开种子添加设置，选择和提交仍需你完成；打开设置不代表添加成功。");
    case "extract_archive": return t("确认后打开解压设置，密码和文件选择仅在该设置中填写；打开设置不代表解压完成。");
    case "pause_download": return t("暂停所选下载，可稍后继续。");
    case "resume_download": return t("继续所选下载。");
    case "retry_upload": return t("重新提交所选上传任务。");
    case "retry_cloud_task": return t("重新提交所选云添加任务。");
    case "star": return t("为所选项目添加星标。");
    case "unstar": return t("移除所选项目的星标，文件保持不变。");
    case "create_folder": return t("在指定云端位置创建此文件夹。");
    case "open_file": return t("使用现有预览或播放器打开所选文件。");
    default: return "";
  }
});
const stateLabel = computed(() => {
  if (operation.value?.stopped) return t("已停止提交");
  switch (props.card.state) {
    case "draft": return t("待你确认");
    case "running": return t("正在执行");
    case "done": return operation.value?.action === "upload_torrent" || operation.value?.action === "extract_archive" ? t("已打开设置") : operation.value?.action === "open_file" ? t("已打开文件") : t("已提交");
    case "failed": return t("未成功");
    case "cancelled": return t("已放弃");
    case "undone": return t("已撤销");
    default: return "";
  }
});
const resourceLabel = computed(() => props.card.kind !== "resources" ? t("操作建议") : props.card.resource === "trash" ? t("回收站") : props.card.resource === "shares" ? t("分享记录") : t("传输任务"));
const SHARE_STATES: Record<string, string> = { active: N_("分享中"), expired: N_("已过期"), canceled: N_("已取消"), blocked: N_("不可访问") };

function operationMeta(row: Pick<CardRow, "path" | "folder" | "size">): string {
  const fileTarget = !!operation.value && ["copy", "star", "unstar", "extract_archive", "open_file"].includes(operation.value.action);
  const path = row.path ? row.path.split("/").join(" / ") : fileTarget ? t("全部文件") : "";
  return [path, row.folder ? t("文件夹") : bytes(row.size, "")].filter(Boolean).join(" · ");
}

function resourceMeta(row: ResourceRow): string {
  const kind = row.kind === "trash_folder" ? t("回收站文件夹") : row.kind === "trash_file" ? t("回收站文件") : row.kind === "share" ? t("分享") : row.kind === "task" ? t("云添加任务") : row.kind === "upload" ? t("上传") : t("下载");
  const transfer = row.kind === "task" || row.kind === "upload" || row.kind === "download";
  let state = row.kind === "share" ? t(SHARE_STATES[row.state] ?? N_("未知")) : "";
  if (row.kind === "task" || row.kind === "upload" || row.kind === "download") {
    const kind = row.kind === "task" ? "cloud" : row.kind;
    state = transferStateLabel({ kind, state: transferState(kind, row.state) });
  }
  return [kind, state, transfer && Number.isFinite(row.progress) ? `${Math.round(Math.min(100, Math.max(0, row.progress)))}%` : "", !row.folder && row.size > 0 ? bytes(row.size, "") : "", stamp(row.modified, "")].filter(Boolean).join(" · ");
}

function execute() {
  if (locked.value || props.card.kind !== "operation" || props.card.state !== "draft") return;
  confirming.value = false;
  void runCard(props.turnId, props.card.id);
}
function requestExecution() {
  if (locked.value) return;
  if (needsConfirmation.value) confirming.value = true;
  else execute();
}
watch([locked, () => props.card.id, () => props.card.state], () => { confirming.value = false; });
</script>

<template>
  <section class="operations-card" :aria-label="card.title">
    <header>
      <div class="heading"><span v-if="card.kind === 'operation'" class="kicker">{{ resourceLabel }}</span><h3>{{ card.title }}</h3></div>
      <span v-if="card.kind === 'operation'" class="status" :class="{ failure: card.state === 'failed' }">{{ stateLabel }}</span>
      <span v-else class="status">{{ t("已加载 {n} 项", { n: card.rows.length }) }}</span>
    </header>

    <template v-if="card.kind === 'operation'">
      <p v-if="card.targetIds.length" class="line">{{ t("操作目标：{n} 项", { n: card.targetIds.length }) }}</p>
      <p v-if="destination" class="line"><Icon name="folder" :size="14" /><span>{{ t("云端目标：{path}", { path: card.path || t("全部文件") }) }}</span></p>
      <p v-if="card.name" class="line">{{ t("文件夹名称：{name}", { name: card.name }) }}</p>
    </template>

    <ul v-if="card.rows.length" class="rows" :aria-label="card.kind === 'operation' ? t('操作目标') : resourceLabel">
      <li v-for="(row, index) in shown" :key="`${row.id}:${index}`">
        <span class="file-icon"><Icon :name="row.folder ? 'folder' : 'file'" :size="16" /></span>
        <div class="row-copy"><strong>{{ row.name }}</strong><small v-if="card.kind === 'resources'">{{ resourceMeta(row as ResourceRow) }}</small><small v-else>{{ operationMeta(row) }}</small></div>
      </li>
    </ul>
    <p v-else-if="card.kind === 'resources'" class="line">{{ t("本页没有项目") }}</p>
    <button v-if="card.rows.length > 6" class="quiet more" type="button" :aria-expanded="expanded" @click="expanded = !expanded">{{ expanded ? t("收起 ↑") : t("查看其余 {n} 项 ↓", { n: card.rows.length - 6 }) }}</button>
    <p v-if="card.kind === 'resources' && card.nextPageToken" class="line">{{ t("还有更多结果，可让助手继续读取下一页；以上不是全部项目。") }}</p>

    <template v-if="card.kind === 'operation'">
      <p v-if="consequence" class="line consequence">{{ consequence }}</p>
      <div v-if="card.state === 'running' || card.progress.done || card.stopped" class="progress" role="status" aria-live="polite">
        <span>{{ t("已处理 {done} / {total} 项", { done: card.progress.done, total: card.progress.total }) }}</span>
        <progress :value="card.progress.done" :max="Math.max(1, card.progress.total)" :aria-label="t('操作进度')" />
      </div>
      <p v-if="card.stopped && !card.note" class="line">{{ t("已停止提交剩余项目；已经提交的操作不会回滚，仍可能完成。") }}</p>
      <div v-if="card.results.length" class="results">
        <h4>{{ t("逐项结果") }}</h4>
        <ul class="rows">
          <li v-for="(result, index) in shownResults" :key="`${result.id}:${index}`" :class="{ failure: !!result.error }">
            <div class="row-copy"><strong>{{ result.name || t("未命名项目") }}</strong><small>{{ result.error || t("该项请求已提交") }}</small></div>
          </li>
        </ul>
        <button v-if="card.results.length > 6" class="quiet more" type="button" :aria-expanded="resultsExpanded" @click="resultsExpanded = !resultsExpanded">{{ resultsExpanded ? t("收起 ↑") : t("查看其余 {n} 项结果 ↓", { n: card.results.length - 6 }) }}</button>
      </div>
    </template>
    <p v-if="card.note" class="line note">{{ card.note }}</p>
    <template v-if="card.kind === 'operation'">
      <div v-if="card.state === 'draft'" class="actions">
        <ConfirmInline v-if="confirming && !locked" :message="t('请再次核对以上所有目标和操作后果，确认后才会执行。')" :confirm-label="confirmLabel" :danger="destructive" @confirm="execute" @cancel="confirming = false" />
        <template v-else>
          <button class="quiet" type="button" :disabled="locked" @click="discardCard(turnId, card.id)">{{ t("放弃") }}</button>
          <button class="primary" type="button" :disabled="locked" @click="requestExecution">{{ confirmLabel }}</button>
        </template>
      </div>
      <div v-else-if="card.state === 'running'" class="actions"><button class="quiet" type="button" :disabled="!!live || card.stopped" @click="stopAssistantOperation()">{{ t("停止提交剩余项目") }}</button></div>
    </template>
  </section>
</template>

<style scoped>
.operations-card { min-width: 0; max-width: 100%; margin: 0 0 14px; border: 1px solid var(--line-strong); border-radius: 16px; background: color-mix(in srgb, var(--fg) 4%, transparent); overflow: hidden; }
header { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 12px; padding: 18px 20px 14px; }
.heading { min-width: 0; flex: 1; }
.kicker { font-size: 10px; font-weight: 650; letter-spacing: .12em; color: var(--accent); }
h3 { margin: 4px 0 0; font-size: 15px; font-weight: 620; }
.status { font-size: 11px; color: var(--fg-dim); }
.line { display: flex; align-items: flex-start; gap: 7px; margin: 0; padding: 0 20px 12px; font-size: 12px; line-height: 1.6; }
.line :deep(svg) { flex: none; margin-top: 3px; }
.note, .consequence { color: var(--fg-dim); }
.rows { padding: 0; margin: 0; list-style: none; }
.rows li { display: flex; align-items: flex-start; gap: 10px; padding: 10px 20px; border-top: 1px solid var(--line); min-width: 0; }
.file-icon { flex: none; display: grid; place-items: center; width: 30px; height: 30px; border-radius: 8px; color: var(--accent); background: var(--accent-soft); }
.row-copy { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 4px; }
.row-copy strong { font-size: 12px; font-weight: 550; line-height: 1.5; }
.row-copy small { font-size: 11px; color: var(--fg-dim); line-height: 1.6; }
h3, h4, .line, .row-copy, .status, .progress { overflow-wrap: anywhere; -webkit-user-select: text; user-select: text; --default-contextmenu: show; }
.failure, .failure small { color: var(--danger); }
.more { display: block; max-width: calc(100% - 40px); margin: 8px 20px 12px; font-size: 12px; white-space: normal; }
.progress { margin: 0 20px 14px; font-size: 12px; color: var(--fg-dim); }
progress { display: block; width: 100%; height: 5px; margin-top: 8px; accent-color: var(--accent); }
h4 { margin: 8px 20px 10px; font-size: 12px; font-weight: 600; }
.actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 8px; padding: 12px 20px 16px; }
.actions > :deep(.confirm) { width: 100%; min-width: 0; }
.actions button { min-height: 36px; max-width: 100%; padding: 0 14px; font-size: 12px; white-space: normal; overflow-wrap: anywhere; }
</style>
