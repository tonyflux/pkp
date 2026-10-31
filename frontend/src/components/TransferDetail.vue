<script setup lang="ts">
// The details island beside the transfer list, opened from a row and closed
// back to it: the full name, where it went, the reason a failure gave, and
// the actions that state has. Clearing the record and deleting what the task
// saved are separate buttons with separate words:
// removing a record never touches the drive. It names the account the task
// belongs to; opening it never switches accounts — only View / Show in folder of
// another account's file do, and say so.
import { computed, ref, watch } from "vue";
import AccountIdentity from "./AccountIdentity.vue";
import Icon from "./Icon.vue";
import ConfirmInline from "./ConfirmInline.vue";
import { bytes, stamp } from "../format";
import {
  accountById,
  accountName,
  cancelDownload,
  cancelUpload,
  currentId,
  dismissArrival,
  ensureArrivalSummary,
  inbox,
  openArrival,
  openDownload,
  openSignIn,
  openView,
  pauseDownload,
  removeDownload,
  removeTask,
  removeUpload,
  restartDownload,
  resumeDownload,
  retryUpload,
  revealDownload,
  revealFile,
  viewFile,
} from "../store";
import { groupOf, stateLabel, type TransferItem } from "../transfers";
import { t, N_ } from "../i18n";

const props = defineProps<{ item: TransferItem }>();
defineEmits<{ close: [] }>();

const confirming = ref(false);
watch(
  () => props.item.key,
  () => (confirming.value = false),
);

const group = computed(() => groupOf(props.item.state));
const owner = computed(() => accountById(props.item.account));
const ownerName = computed(() => accountName(props.item.account));
// Another account than the one on screen: reading it here switches nothing.
const elsewhere = computed(() => props.item.account !== currentId.value);
const stale = computed(() => {
  const it = props.item;
  return it.blocked && it.state !== "done" && !(it.kind === "cloud" && it.state === "failed");
});
const tile = computed(() => (props.item.category && props.item.category !== "other" ? props.item.category : "file"));

const ENDED: Partial<Record<TransferItem["state"], string>> = {
  done: N_("完成时间"),
  failed: N_("失败时间"),
  cancelled: N_("取消时间"),
  interrupted: N_("中断时间"),
};

const facts = computed(() => {
  const it = props.item;
  const rows: { label: string; value: string; tone?: string; mono?: boolean }[] = [
    { label: t("任务类型"), value: it.kind === "cloud" ? t("云添加") : it.kind === "download" ? t("下载到电脑") : t("本地上传") },
    { label: t("状态"), value: stateLabel(it), tone: stale.value ? "stale" : it.state },
    { label: t("保存到"), value: it.savedTo || t("未知") },
  ];
  if (it.kind === "download" && it.origin) rows.push({ label: t("来自"), value: it.origin });
  // A figure never breaks from its unit when a value wraps (8.38 GB).
  const size = (n: number, zero?: string) => bytes(n, zero).replace(" ", "\u00a0");
  if (it.progress !== null) {
    const moved = it.totalBytes ? ` · ${size(it.doneBytes, "0 B")} / ${size(it.totalBytes)}` : "";
    rows.push({ label: t("进度"), value: `${it.progress.toFixed(0)}%${moved}` });
  } else if (it.resumable) {
    rows.push({ label: it.kind === "download" ? t("已下载") : t("已上传"), value: `${size(it.doneBytes)} / ${size(it.totalBytes)}` });
  } else if (it.totalBytes) {
    rows.push({ label: t("大小"), value: bytes(it.totalBytes) });
  }
  if (it.speed) rows.push({ label: t("速度"), value: `${bytes(it.speed)}/s` });
  if (it.kind === "download" && it.state === "running") rows.push({ label: t("连接"), value: t("{n} 个连接", { n: it.threads }) });
  const ended = ENDED[it.state];
  if (ended && it.endedAt) rows.push({ label: t(ended), value: stamp(it.endedAt) });
  if (it.source) rows.push({ label: t("本地文件"), value: it.source, mono: true });
  // The failure in the server's or the engine's own words, for whoever
  // needs the exact text; the reason above is the page's.
  if (it.rawError) rows.push({ label: it.kind === "cloud" ? t("服务端信息") : t("错误信息"), value: it.rawError, mono: true });
  return rows;
});

async function deleteWithFiles() {
  confirming.value = false;
  await removeTask(props.item, true);
}

// Organize on import: a finished cloud add of the account on screen that the inbox
// recorded carries a suggestion — computed locally, never executed here.
const arrival = computed(() => {
  const it = props.item;
  if (it.kind !== "cloud" || it.state !== "done" || inbox.account !== it.account) return null;
  return inbox.arrivals.find((a) => a.id === it.id) ?? null;
});
const summary = computed(() => (arrival.value ? (inbox.summaries[arrival.value.id] ?? null) : null));
watch(
  arrival,
  (a) => {
    if (a?.state === "new") void ensureArrivalSummary(a);
  },
  { immediate: true },
);
const arrivalSource = computed(() => {
  const a = arrival.value;
  if (!a) return "";
  return `${t("来自云添加")}${a.completedAt ? ` · ${stamp(a.completedAt)}` : ""}`;
});
</script>

<template>
  <aside class="detail glass glass-sheet" :aria-label="t('任务详情')" tabindex="-1">
    <header class="detail-head">
      <Icon name="info" :size="18" />
      <strong>{{ t("任务详情") }}</strong>
      <button class="quiet icon" :title="t('关闭')" @click="$emit('close')"><Icon name="close" :size="16" /></button>
    </header>

    <div class="detail-body">
      <section class="owner" :aria-label="t('所属账号')">
        <span class="owner-label">{{ t("所属账号") }}</span>
        <AccountIdentity :account="owner" />
        <p v-if="elsewhere" class="owner-note">{{ t("当前浏览账号：{name}（查看详情不切换账号）", { name: accountName(currentId) }) }}</p>
      </section>

      <span class="file-icon" :class="[tile, item.state]" aria-hidden="true"><Icon :name="tile" :size="24" /></span>
      <h2 class="title">{{ item.name }}</h2>

      <p v-if="item.reason" class="reason">
        <Icon name="alert" :size="16" />
        <span>{{ item.reason }}</span>
      </p>
      <p v-if="item.fileGone" class="note">
        <Icon name="info" :size="16" />
        <span>{{ t("任务完成后，保存到云盘的文件被删除了，云端因此把它记为失败。重新运行找不回这个文件；它若还在回收站，可以从回收站还原。") }}</span>
      </p>
      <p v-else-if="item.state === 'interrupted' && item.kind === 'download'" class="note">
        <Icon name="info" :size="16" />
        <span v-if="item.resumable">{{ t("应用退出时这个下载还没完成。已下载的部分保留在本地，继续下载会从中断的地方接着下载。") }}</span>
        <span v-else>{{ t("应用退出时这个下载还没完成。重新下载会从头开始。") }}</span>
      </p>
      <p v-else-if="item.state === 'interrupted'" class="note">
        <Icon name="info" :size="16" />
        <span v-if="item.resumable">{{ t("应用退出时这个上传还没完成。已上传的部分留在云端，继续上传会从中断的地方接着上传。") }}</span>
        <span v-else>{{ t("应用退出时这个上传还没完成。重新上传会从头开始，不会接着之前的进度。") }}</span>
      </p>
      <p v-if="item.resumable && item.state === 'failed' && item.kind === 'upload'" class="note">
        <Icon name="info" :size="16" />
        <span>{{ t("已上传的部分留在云端，继续上传会从中断的地方接着上传。") }}</span>
      </p>

      <dl class="facts">
        <div v-for="row in facts" :key="row.label">
          <dt>{{ row.label }}</dt>
          <dd :class="[row.tone, { mono: row.mono }]">{{ row.value }}</dd>
        </div>
      </dl>

      <section v-if="arrival?.state === 'new'" class="arrival" aria-labelledby="arrival-title">
        <span class="owner-label" id="arrival-title">{{ t("待整理建议") }}</span>
        <p class="arrival-source">{{ arrivalSource }}</p>
        <template v-if="!summary || summary.state === 'loading'">
          <p class="arrival-line">{{ t("正在按本地规则生成建议…") }}</p>
        </template>
        <template v-else-if="summary.state === 'waiting'">
          <p class="arrival-line">{{ t("新文件还没进入本地索引，同步后再生成建议。") }}</p>
        </template>
        <template v-else-if="summary.state === 'error'">
          <p class="arrival-line error">{{ summary.error || t("建议没有生成") }}</p>
          <button class="quiet link" @click="ensureArrivalSummary(arrival)">{{ t("重试") }}</button>
        </template>
        <template v-else>
          <strong class="arrival-head">
            {{ summary.organizable ? t("{n} 项能规范命名并放到「{target}」", { n: summary.organizable, target: summary.target }) : t("没有需要整理的项") }}
          </strong>
          <p class="arrival-line">{{ summary.line }}</p>
          <span v-if="summary.basis" class="arrival-basis"><Icon name="check" :size="13" />{{ summary.basis }}</span>
        </template>
        <div class="arrival-actions">
          <button class="primary" :disabled="summary?.state !== 'ready' || !summary.organizable" @click="openArrival(arrival)">
            {{ t("查看计划") }} <Icon name="arrowRight" :size="15" />
          </button>
          <button class="quiet" @click="dismissArrival(arrival)">{{ t("忽略") }}</button>
        </div>
      </section>
      <p v-else-if="arrival?.state === 'organized'" class="note">
        <Icon name="check" :size="16" />
        <span>{{ t("已按计划整理。") }}</span>
      </p>
    </div>

    <footer class="detail-foot">
      <!-- Nothing reaches the server of an account that needs signing back in. -->
      <template v-if="item.blocked">
        <button class="primary wide" @click="openSignIn(item.account)">{{ t("重新登录「{name}」", { name: ownerName }) }}</button>
        <p class="foot-note">
          {{
            item.kind === "upload"
              ? t("登录恢复后可继续上传")
              : item.kind === "download"
                ? t("登录恢复后可继续下载")
                : t("登录恢复后同步任务状态；云端任务可能仍在运行")
          }}
        </p>
      </template>

      <template v-else-if="item.kind === 'download'">
        <template v-if="item.state === 'running' || item.state === 'waiting'">
          <button class="wide" @click="pauseDownload(item)"><Icon name="pause" :size="16" />{{ t("暂停下载") }}</button>
          <button class="quiet wide" @click="cancelDownload(item)">{{ t("取消下载") }}</button>
        </template>
        <template v-else-if="item.state === 'paused'">
          <button class="primary wide" @click="resumeDownload(item)"><Icon name="restore" :size="16" />{{ t("继续下载") }}</button>
          <button class="quiet wide" @click="restartDownload(item)">{{ t("从头重新下载") }}</button>
          <button class="quiet wide" @click="cancelDownload(item)">{{ t("取消下载") }}</button>
        </template>
        <template v-else-if="group === 'attention'">
          <button class="primary wide" @click="resumeDownload(item)">
            <Icon name="restore" :size="16" />{{ item.resumable ? t("继续下载") : t("重新下载") }}
          </button>
          <button v-if="item.resumable" class="quiet wide" @click="restartDownload(item)">{{ t("从头重新下载") }}</button>
          <button class="quiet wide" @click="removeDownload(item)">{{ t("移除记录") }}</button>
        </template>
        <template v-else>
          <div class="pair">
            <button class="primary" @click="openDownload(item)"><Icon name="file" :size="16" />{{ t("打开") }}</button>
            <button @click="revealDownload(item)"><Icon name="folder" :size="16" />{{ t("打开所在文件夹") }}</button>
          </div>
          <button class="quiet wide" :title="t('只清理列表，下载的文件不动')" @click="removeDownload(item)">{{ t("移除记录") }}</button>
        </template>
      </template>

      <template v-else>
        <!-- Another account's file: these two switch to it first, and say so. -->
        <div v-if="item.state === 'done' && item.fileId" class="pair" :class="{ stack: elsewhere }">
          <button class="primary" @click="viewFile(item)">
            <Icon name="eye" :size="16" />{{ elsewhere ? t("切换到「{name}」并查看", { name: ownerName }) : t("查看") }}
          </button>
          <button @click="revealFile(item)">
            <Icon name="folder" :size="16" />{{ elsewhere ? t("切换并打开所在文件夹") : t("所在文件夹") }}
          </button>
        </div>

        <button v-if="item.kind === 'upload' && group === 'active'" class="wide" @click="cancelUpload(item)">
          <Icon name="close" :size="16" />{{ t("取消上传") }}
        </button>
        <button v-else-if="item.kind === 'upload' && group === 'attention'" class="primary wide" @click="retryUpload(item)">
          <Icon name="restore" :size="16" />{{ item.resumable ? t("继续上传") : t("重新上传") }}
        </button>

        <template v-if="item.kind === 'upload'">
          <button v-if="group !== 'active'" class="quiet wide" @click="removeUpload(item)">{{ t("移除记录") }}</button>
        </template>
        <template v-else-if="item.fileGone">
          <button v-if="!elsewhere" class="wide" @click="openView('trash')"><Icon name="trash" :size="16" />{{ t("打开回收站") }}</button>
          <button class="quiet wide" :title="t('只清理传输列表；文件已不在原处')" @click="removeTask(item, false)">{{ t("移除记录") }}</button>
        </template>
        <template v-else>
          <ConfirmInline
            v-if="confirming"
            :message="t('移除这条记录，并删除它保存到云盘的文件？')"
            :confirm-label="t('删除文件')"
            :cancel-label="t('保留')"
            danger
            @cancel="confirming = false"
            @confirm="deleteWithFiles"
          />
          <div v-else class="pair">
            <button class="quiet" :title="t('只清理传输列表，云盘里的文件不动')" @click="removeTask(item, false)">{{ t("移除记录") }}</button>
            <button class="quiet danger" @click="confirming = true">{{ t("删除文件") }}</button>
          </div>
        </template>
      </template>
    </footer>
  </aside>
</template>

<style scoped>
/* A column of the content row beside the canvas: the canvas gives up the
   width, so no row is ever covered and the list keeps its scroll and
   filters. */
.detail {
  position: relative;
  flex: none;
  align-self: stretch;
  z-index: 2;
  width: var(--inspector-w);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: var(--radius-sheet);
  outline: none;
  /* A slide, no fade: an animation WebKit holds at its first frame (it does
     in a window it is not drawing) must not leave the details invisible. */
  animation: detail-in var(--dur) var(--ease);
}

@keyframes detail-in {
  from {
    transform: translateX(22px);
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

.detail-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 2px 20px 16px;
}

/* Account: whose task this is, first. */
.owner {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 0 0 16px;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--line);
}

.owner-label {
  font-size: 12px;
  color: var(--fg-dim);
}

/* Suggestions to organize: a suggestion, warm like Pending confirmation, with its two choices. */
.arrival {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  margin-top: 14px;
  padding: 14px;
  border: 1px solid color-mix(in srgb, var(--warning) 35%, var(--line));
  border-radius: var(--radius-card);
  background: color-mix(in srgb, var(--warning) 7%, transparent);
}

.arrival .owner-label {
  color: var(--warning);
  font-weight: 650;
}

.arrival-source,
.arrival-line {
  margin: 0;
  font-size: 13px;
  color: var(--fg-dim);
}

.arrival-line.error {
  color: var(--danger);
}

.arrival-head {
  font-size: 15px;
  font-weight: 650;
  line-height: 1.4;
}

.arrival-basis {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 9px;
  border-radius: var(--radius-pill);
  background: color-mix(in srgb, var(--success) 14%, transparent);
  color: var(--success);
  font-size: 12px;
}

.arrival-actions {
  display: flex;
  gap: 8px;
  margin-top: 6px;
}

/* Retry stands alone under the error line: a pill with room around the text,
   pulled back by the margin so the text keeps its place. */
.arrival .link {
  min-height: 0;
  margin: -3px -8px;
  padding: 3px 10px;
  color: var(--accent);
  font-size: inherit;
}

.owner-note {
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--fg-dim);
}

.file-icon {
  --tint: var(--accent);
  width: 52px;
  height: 52px;
  display: grid;
  place-items: center;
  border-radius: 14px;
  color: var(--tint);
  background: linear-gradient(
    150deg,
    color-mix(in srgb, var(--tint) 24%, transparent),
    color-mix(in srgb, var(--tint) 9%, transparent)
  );
  border: 1px solid color-mix(in srgb, var(--tint) 26%, transparent);
}

.file-icon.archive,
.file-icon.document {
  --tint: var(--tint-violet);
}

.file-icon.failed {
  --tint: var(--danger);
}

/* The full name, wrapped and selectable: the row could only ellipsize it. */
.title {
  margin: 14px 0 0;
  font-size: 16px;
  font-weight: 600;
  line-height: 1.45;
  overflow-wrap: anywhere;
  -webkit-user-select: text;
  user-select: text;
}

.reason {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin: 12px 0 0;
  padding: 10px 12px;
  border-radius: 12px;
  font-size: 13px;
  line-height: 1.5;
  color: var(--danger);
  background: color-mix(in srgb, var(--danger) 10%, transparent);
  overflow-wrap: anywhere;
  -webkit-user-select: text;
  user-select: text;
}

.reason svg,
.note svg {
  flex: none;
  margin-top: 2px;
}

.note {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin: 12px 0 0;
  padding: 10px 12px;
  border-radius: 12px;
  font-size: 13px;
  line-height: 1.5;
  color: var(--fg-dim);
  background: color-mix(in srgb, var(--fg) 6%, transparent);
}

/* One label column for every row, as wide as the longest label (at least
   72px, at most half the column), so no label breaks mid-word and the
   values line up. */
.facts {
  display: grid;
  grid-template-columns: fit-content(50%) minmax(0, 1fr);
  column-gap: 12px;
  margin: 16px 0 0;
  padding: 4px 0;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
}

.facts > div {
  display: grid;
  grid-column: 1 / -1;
  grid-template-columns: subgrid;
  padding: 9px 0;
  font-size: 13px;
  line-height: 1.45;
}

.facts dt {
  min-width: 72px;
  color: var(--fg-dim);
}

.facts dd {
  margin: 0;
  font-weight: 500;
  overflow-wrap: anywhere;
  -webkit-user-select: text;
  user-select: text;
}

.facts dd.running {
  color: var(--accent);
}

.facts dd.done {
  color: var(--success);
}

.facts dd.failed {
  color: var(--danger);
}

.facts dd.stale {
  color: var(--warning);
}

.facts dd.mono {
  font-family: ui-monospace, "SF Mono", "Cascadia Mono", Menlo, monospace;
  font-size: 12px;
  font-weight: 400;
}

.detail-foot {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 20px 18px;
  border-top: 1px solid var(--line);
}

.pair {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}

.detail-foot button {
  min-height: 40px;
  font-size: 14px;
  font-weight: 600;
}

.detail-foot .wide {
  width: 100%;
}

/* Longer words when the file is another account's: one per line. */
.pair.stack {
  grid-template-columns: 1fr;
}

.foot-note {
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--fg-dim);
  text-align: center;
}

.detail-foot .confirm {
  padding-top: 0;
  border-top: 0;
}

.detail-foot :deep(.confirm-actions button) {
  flex: 1;
  min-height: 40px;
}
</style>
