<script setup lang="ts">
// The transfer list's batch actions, on the shared bar. Each action says how
// many of the picked rows it applies to when that is not all of them, and is
// off — with the reason in its tooltip — when it applies to none. Cancelling
// uploads and deleting files ask once, in place; results come back per item.
// Rows of an account that needs signing back in are in no action; the bar
// says how many it leaves out.
import { computed, ref, watch } from "vue";
import BatchBar from "./BatchBar.vue";
import ConfirmInline from "./ConfirmInline.vue";
import Icon from "./Icon.vue";
import {
  cancelDownloads,
  cancelUploads,
  pauseDownloads,
  removeDownloads,
  removeTasks,
  removeUploads,
  resumeDownloads,
  retryUploads,
} from "../store";
import { batchPlan, retryLabel, type TransferItem } from "../transfers";
import { t } from "../i18n";

const props = defineProps<{ items: TransferItem[]; total: number }>();
const emit = defineEmits<{ pickAll: []; clear: [] }>();

const plan = computed(() => batchPlan(props.items));
// Only one source shows at a time, so the picked rows are all of one kind.
const kind = computed(() => props.items[0]?.kind ?? "upload");
const blocked = computed(() => (plan.value.blocked.length ? t("{n} 项需要重新登录，不会处理", { n: plan.value.blocked.length }) : ""));

/** title adds what the batch leaves out to an action's own tooltip. */
function title(text: string): string {
  return blocked.value ? t("{action}；{blocked}", { action: text, blocked: blocked.value }) : text;
}

/** retryText shows retryLabel's word (data store compares) in the page's language. */
function retryText(items: TransferItem[]): string {
  if (!items.length) return t("重试");
  const word = retryLabel(items);
  if (word === "继续") return t("继续"); // i18n-ignore
  if (word === "重试") return t("重试"); // i18n-ignore
  return t("继续 / 重试");
}
const asking = ref<"" | "cancel" | "delete">("");

// A different set is a different question.
watch(
  () => props.items.map((item) => item.key).join(),
  () => (asking.value = ""),
);

/** label adds the count when an action covers only part of the pick. */
function label(text: string, applies: TransferItem[]): string {
  return applies.length && applies.length < props.items.length ? `${text} ${applies.length}` : text;
}

async function act(run: () => Promise<void>) {
  asking.value = "";
  await run();
  emit("clear");
}
</script>

<template>
  <BatchBar :count="items.length" :all-picked="items.length === total" @pick-all="emit('pickAll')" @clear="emit('clear')">
    <ConfirmInline
      v-if="asking === 'cancel' && kind === 'download'"
      :message="t('取消 {n} 个下载？已下载的部分会删除。', { n: plan.cancel.length })"
      :confirm-label="t('取消下载')"
      :cancel-label="t('继续下载')"
      danger
      @cancel="asking = ''"
      @confirm="act(() => cancelDownloads(plan.cancel))"
    />
    <ConfirmInline
      v-else-if="asking === 'cancel'"
      :message="t('取消 {n} 个上传？已上传的部分不保留。', { n: plan.cancel.length })"
      :confirm-label="t('取消上传')"
      :cancel-label="t('继续上传')"
      danger
      @cancel="asking = ''"
      @confirm="act(() => cancelUploads(plan.cancel))"
    />
    <ConfirmInline
      v-else-if="asking === 'delete'"
      :message="t('删除 {n} 个任务和它们保存到云盘的文件？', { n: plan.deleteFiles.length })"
      :confirm-label="t('删除文件')"
      :cancel-label="t('保留')"
      danger
      @cancel="asking = ''"
      @confirm="act(() => removeTasks(plan.deleteFiles, true))"
    />

    <template v-else-if="kind === 'cloud'">
      <span v-if="blocked" class="blocked" role="note">{{ blocked }}</span>
      <button
        class="quiet"
        :disabled="!plan.remove.length"
        :title="title(t('只清理传输列表，云盘里的文件不动'))"
        @click="act(() => removeTasks(plan.remove, false))"
      >
        <Icon name="close" :size="17" />{{ label(t("移除记录"), plan.remove) }}
      </button>
      <button
        class="quiet warn-hover"
        :disabled="!plan.deleteFiles.length"
        :title="blocked || undefined"
        @click="asking = 'delete'"
      >
        <Icon name="trash" :size="17" />{{ label(t("删除文件"), plan.deleteFiles) }}
      </button>
    </template>

    <template v-else-if="kind === 'download'">
      <span v-if="blocked" class="blocked" role="note">{{ blocked }}</span>
      <button
        class="quiet"
        :disabled="!plan.pause.length"
        :title="title(plan.pause.length ? t('已下载的部分保留') : t('所选的下载都没有在进行'))"
        @click="act(() => pauseDownloads(plan.pause))"
      >
        <Icon name="pause" :size="17" />{{ label(t("暂停"), plan.pause) }}
      </button>
      <button
        class="quiet"
        :disabled="!plan.retry.length"
        :title="title(plan.retry.length ? t('保留了进度的从中断处继续，其余从头开始') : t('所选的下载里没有暂停、失败、取消或中断的'))"
        @click="act(() => resumeDownloads(plan.retry))"
      >
        <Icon name="restore" :size="17" />{{ label(retryText(plan.retry), plan.retry) }}
      </button>
      <button
        class="quiet"
        :disabled="!plan.cancel.length"
        :title="title(plan.cancel.length ? t('已下载的部分会删除') : t('所选的下载都已停下'))"
        @click="asking = 'cancel'"
      >
        <Icon name="stop" :size="17" />{{ label(t("取消"), plan.cancel) }}
      </button>
      <button
        class="quiet warn-hover"
        :disabled="!plan.remove.length"
        :title="title(plan.remove.length ? t('只清理列表；已下载的文件不动') : t('正在下载的记录要先暂停或取消才能移除'))"
        @click="act(() => removeDownloads(plan.remove))"
      >
        <Icon name="close" :size="17" />{{ label(t("移除记录"), plan.remove) }}
      </button>
    </template>

    <template v-else>
      <span v-if="blocked" class="blocked" role="note">{{ blocked }}</span>
      <button
        class="quiet"
        :disabled="!plan.retry.length"
        :title="title(plan.retry.length ? t('保留了进度的从中断处继续，其余从头开始') : t('所选的上传里没有失败、取消或中断的'))"
        @click="act(() => retryUploads(plan.retry))"
      >
        <Icon name="restore" :size="17" />{{ label(retryText(plan.retry), plan.retry) }}
      </button>
      <button
        class="quiet"
        :disabled="!plan.cancel.length"
        :title="title(plan.cancel.length ? t('已上传的部分不保留') : t('所选的上传都已停下'))"
        @click="asking = 'cancel'"
      >
        <Icon name="stop" :size="17" />{{ label(t("取消"), plan.cancel) }}
      </button>
      <button
        class="quiet warn-hover"
        :disabled="!plan.remove.length"
        :title="title(plan.remove.length ? t('只清理列表；已上传的文件不动') : t('正在上传的记录要先取消才能移除'))"
        @click="act(() => removeUploads(plan.remove))"
      >
        <Icon name="close" :size="17" />{{ label(t("移除记录"), plan.remove) }}
      </button>
    </template>
  </BatchBar>
</template>

<style scoped>
.blocked {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
  font-weight: 500;
  color: var(--warning);
}
</style>
