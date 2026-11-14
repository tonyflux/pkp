<script setup lang="ts">
// The stages of a Cleanup tab between its results:
// before the first scan, while scanning, while a removal runs, and the
// receipt of what it did.
import { computed } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import { bytes } from "../format";
import { t, num } from "../i18n";
import { fileTasks } from "../filetasks";
import {
  backToCleanupResult,
  cleanup,
  cleanupScopeLabel,
  openView,
  scanDuplicates,
  scanEmptyFolders,
  stopCleanup,
  type CleanupTab,
} from "../store";

const props = defineProps<{ tab: CleanupTab }>();

const state = computed(() => cleanup[props.tab]);
const dup = computed(() => props.tab === "dup");
const receipt = computed(() => state.value.receipt);
const task = computed(() => fileTasks.items.find((item) => item.id === cleanup.taskId));

const runTitle = computed(() => {
  if (task.value?.phase === "queued") return t("filetask|已排队，尚未提交");
  if (task.value?.phase === "verifying") return t("filetask|正在核对结果");
  if (!dup.value) return t("正在检查并移走空文件夹");
  return cleanup.progress.phase === "delete" ? t("正在彻底删除重复文件") : t("正在移到回收站");
});
// The checking phase counts re-confirmations, not settled entries, so the
// receipt's processed figure is not used until removal starts.
const settled = computed(() => (cleanup.progress.phase === "check" ? cleanup.progress.done : (task.value?.processed ?? cleanup.progress.done)));
const runLine = computed(() => {
  if (task.value?.phase === "queued") return t("filetask|已排队，尚未提交");
  const { total, phase } = cleanup.progress;
  if (phase === "check") return t("正在逐个确认仍然是空的 · {done} / {total}", { done: cleanup.progress.done, total });
  return t("已完成 {done} / {total} 项", { done: settled.value, total });
});
const ratio = computed(() => cleanup.progress.total ? Math.min(1, settled.value / cleanup.progress.total) : 0);

const receiptTitle = computed(() => {
  if (!dup.value) return t("空文件夹已移到回收站");
  return receipt.value?.permanent ? t("已彻底删除") : t("已移到回收站");
});
const receiptLine = computed(() => {
  const r = receipt.value;
  if (!r) return "";
  if (!dup.value) return t("在回收站还原顶层文件夹时，里面的空文件夹也会回来。");
  if (r.permanent) return t("已核对删除结果，释放 {size}，容量会随后刷新。", { size: bytes(r.bytes, "0 B") });
  return t("{size} 可以从回收站还原；彻底删除后空间才会释放。", { size: bytes(r.bytes, "0 B") });
});

function scan() {
  void (dup.value ? scanDuplicates() : scanEmptyFolders());
}
</script>

<template>
  <div class="stage">
    <template v-if="state.stage === 'scanning'">
      <span class="mark"><Orbit :size="24" :label="t('扫描中')" /></span>
      <h2>{{ t("正在读取本地索引") }}</h2>
      <p>{{ t("只在这台电脑上计算结果，不会修改文件。范围：{scope}", { scope: cleanupScopeLabel }) }}</p>
    </template>

    <template v-else-if="state.stage === 'running'">
      <span class="mark"><Icon name="transfer" :size="26" /></span>
      <h2>{{ runTitle }}</h2>
      <p>{{ t("每批处理 100 项；停止只影响还没发出的批次。") }}</p>
      <div
        class="track"
        role="progressbar"
        :aria-valuenow="settled"
        :aria-valuemax="cleanup.progress.total"
        :aria-label="t('处理进度')"
      >
        <i :style="{ width: `${ratio * 100}%` }" />
      </div>
      <small class="tnum">{{ runLine }}</small>
      <button :disabled="cleanup.stopping" @click="stopCleanup">
        <Orbit v-if="cleanup.stopping" :size="14" />
        {{ cleanup.stopping ? t("正在停止…") : t("停止后续批次") }}
      </button>
    </template>

    <template v-else-if="state.stage === 'receipt' && receipt">
      <span class="mark done"><Icon name="check" :size="26" /></span>
      <h2>{{ receiptTitle }}</h2>
      <p>{{ receiptLine }}</p>
      <div class="stats">
        <span><strong class="ok tnum">{{ num(receipt.removed) }}</strong> {{ t("项成功") }}</span>
        <span><strong class="warn tnum">{{ num(receipt.failed.length) }}</strong> {{ t("项未处理") }}</span>
      </div>
      <p v-if="receipt.stopped" class="note">{{ t("已停止：还没发出的批次没有处理，也不算失败。") }}</p>
      <ul v-if="receipt.failed.length" class="failed" :aria-label="t('未处理的项')">
        <li v-for="f in receipt.failed" :key="f.id">
          <strong :title="f.name">{{ f.name || f.id }}</strong>
          <span>{{ f.error }}</span>
        </li>
      </ul>
      <div class="actions">
        <button @click="backToCleanupResult(tab)">{{ t("返回结果") }}</button>
        <button v-if="!receipt.permanent" class="primary" @click="openView('trash')">
          <Icon name="trash" :size="16" />
          {{ t("打开回收站") }}
        </button>
      </div>
    </template>

    <template v-else>
      <span class="mark"><Icon :name="dup ? 'layers' : 'folder'" :size="26" /></span>
      <h2>{{ t("从本地索引开始") }}</h2>
      <p>
        {{
          dup
            ? t("扫描{scope}的内容重复文件；无需 AI，也不会下载文件或请求云端。", { scope: cleanupScopeLabel })
            : t("扫描{scope}的空文件夹；无需 AI，也不会下载文件或请求云端。", { scope: cleanupScopeLabel })
        }}
      </p>
      <p v-if="state.error" class="error" role="alert">{{ t("没能扫描：{error}", { error: state.error }) }}</p>
      <button class="primary" :disabled="state.loading" @click="scan">{{ t("开始扫描") }}</button>
    </template>
  </div>
</template>

<style scoped>
.stage {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 56px var(--pad) 32px;
  text-align: center;
}

.mark {
  display: grid;
  place-items: center;
  width: 62px;
  height: 62px;
  margin-bottom: 18px;
  border-radius: 18px;
  border: 1px solid var(--line-strong);
  background: color-mix(in srgb, var(--glass-control) 60%, transparent);
  color: var(--accent);
}

.mark.done {
  color: var(--success);
}

h2 {
  margin: 0 0 8px;
  font-size: 23px;
  line-height: 31px;
  letter-spacing: -0.01em;
  text-wrap: balance;
}

/* Centred copy wraps into even lines, never a lone word under a long one. */
p {
  max-width: 52ch;
  margin: 0 0 18px;
  font-size: 14px;
  color: var(--fg-dim);
  text-wrap: balance;
}

p.error {
  color: var(--danger);
}

p.note {
  font-size: 13px;
}

.track {
  width: min(420px, 100%);
  height: 6px;
  margin: 4px 0 10px;
  border-radius: 3px;
  background: color-mix(in srgb, var(--fg) 10%, transparent);
  overflow: hidden;
}

.track i {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: linear-gradient(90deg, var(--progress-from), var(--progress-to));
  transition: width var(--dur) var(--ease);
}

small {
  margin-bottom: 18px;
  font-size: 12px;
  color: var(--fg-dim);
}

.stats {
  display: flex;
  gap: 12px;
  margin-bottom: 18px;
}

.stats span {
  padding: 9px 16px;
  border-radius: 12px;
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--glass-control) 55%, transparent);
  font-size: 15px;
  font-weight: 500;
}

.stats .ok {
  color: var(--success);
}

.stats .warn {
  color: var(--warning);
}

.failed {
  width: min(560px, 100%);
  max-height: 240px;
  margin: 0 0 18px;
  padding: 6px 0;
  overflow-y: auto;
  list-style: none;
  border: 1px solid var(--line);
  border-radius: 12px;
  text-align: left;
}

.failed li {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, auto);
  gap: 12px;
  padding: 7px 14px;
  font-size: 13px;
}

.failed strong {
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.failed span {
  color: var(--warning);
}

.actions {
  display: flex;
  gap: 10px;
}

.actions button,
.stage > button {
  min-height: 38px;
  padding: 0 18px;
  border-radius: 12px;
  font-size: 13px;
}

@media (max-height: 800px) {
  .stage {
    padding-top: 32px;
  }
}
</style>
