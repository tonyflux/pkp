<script setup lang="ts">
// What's in Here: what a folder holds at any depth, read
// from this computer's index — kinds by size, copies Cleanup would find, videos
// not watched to the end, the biggest items — and the two ways on from it:
// Organize This Folder (local rules first) and Ask AI (which asks before sending).
// It sits under the details and never holds them up: a refusal is a line.
import { computed, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import { bytes } from "../format";
import { t, num } from "../i18n";
import type { FolderStats } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import {
  askAssistant,
  cleanup,
  currentId,
  enterCleanupTab,
  folderStats,
  indexReady,
  organizeFolder,
  setCleanupScope,
  trailTo,
  ui,
} from "../store";

const props = defineProps<{ folderId: string; folderName: string }>();

const stats = ref<FolderStats | null>(null);
const error = ref("");
const loading = ref(false);
let serial = 0;

watch(
  [() => props.folderId, currentId, indexReady],
  async () => {
    const mine = ++serial;
    stats.value = null;
    error.value = "";
    if (!indexReady.value) return;
    loading.value = true;
    const answer = await folderStats(props.folderId);
    if (mine !== serial) return;
    loading.value = false;
    stats.value = answer.stats;
    error.value = answer.error;
  },
  { immediate: true },
);

const LABEL: Record<string, () => string> = {
  video: () => t("视频"),
  image: () => t("图片"),
  audio: () => t("音频"),
  document: () => t("文档"),
  archive: () => t("压缩包"),
  other: () => t("其他"),
};

const categories = computed(() => {
  const s = stats.value;
  const rows = (s?.categories ?? []).filter((c) => c.count > 0);
  const top = Math.max(1, ...rows.map((c) => c.size));
  return rows.map((c) => ({ ...c, label: LABEL[c.category]?.() ?? c.category, width: `${Math.max(3, (c.size / top) * 100)}%` }));
});

const largest = computed(() => (stats.value?.largest ?? []).slice(0, 5));
const empty = computed(() => !!stats.value && stats.value.files === 0 && stats.value.folders === 0);

const busy = ref(false);
// The folder's breadcrumb is read only when it is acted on.
async function withTrail(fn: (trail: { id: string; name: string }[]) => unknown) {
  if (busy.value) return;
  busy.value = true;
  try {
    const trail = await trailTo(props.folderId, props.folderName);
    if (trail) await fn(trail);
  } finally {
    busy.value = false;
  }
}

function clean() {
  if (cleanup.running) return;
  void withTrail((trail) => {
    setCleanupScope(trail);
    enterCleanupTab("dup");
    ui.route = "cleanup";
  });
}
</script>

<template>
  <section class="folder-stats" aria-labelledby="folder-stats-title">
    <div class="fsc-head">
      <h3 id="folder-stats-title">{{ t("这里都有什么") }}</h3>
      <Orbit v-if="loading" :label="t('正在统计')" />
    </div>

    <p v-if="!indexReady" class="fsc-note">{{ t("本地索引建立完成后，这里会列出文件夹里的内容构成。") }}</p>
    <p v-else-if="error" class="fsc-note">{{ error }}</p>
    <p v-else-if="empty" class="fsc-note">{{ t("这个文件夹是空的。") }}</p>
    <template v-else-if="stats">
      <p class="fsc-total tnum">
        {{ stats.folders ? t("{files} 个文件 · {folders} 个文件夹 · {size}", { files: stats.files, folders: stats.folders, size: bytes(stats.size, "0 B", 1) }) : t("{files} 个文件 · {size}", { files: stats.files, size: bytes(stats.size, "0 B", 1) }) }}
      </p>

      <ul class="fsc-cats">
        <li v-for="c in categories" :key="c.category">
          <span class="fsc-cat-label">{{ c.label }}</span>
          <span class="fsc-bar" aria-hidden="true"><i :class="c.category" :style="{ width: c.width }"></i></span>
          <span class="fsc-cat-num tnum">{{ num(c.count) }} · {{ bytes(c.size, "0 B", 1) }}</span>
        </li>
      </ul>

      <div v-if="stats.dupGroups" class="fsc-line">
        <Icon name="layers" :size="15" />
        <!-- The link runs on at the end of the sentence, so a longer language
             wraps the sentence, not the sentence beside a squeezed column. -->
        <span>
          {{ t("重复 {n} 组，可回收 {size}", { n: stats.dupGroups, size: bytes(stats.dupWaste, "0 B", 1) }) }}
          <button class="quiet fsc-link" :disabled="busy || !!cleanup.running" @click="clean">{{ t("清理") }} <Icon name="chevronRight" :size="13" /></button>
        </span>
      </div>
      <div v-if="stats.unfinished" class="fsc-line">
        <Icon name="clock" :size="15" />
        <span>{{ t("没看完 {n} 个视频", { n: stats.unfinished }) }}</span>
      </div>

      <div v-if="largest.length" class="fsc-largest">
        <span class="fsc-sub">{{ t("最大的几项") }}</span>
        <ul>
          <li v-for="e in largest" :key="e.id">
            <Icon :name="e.folder ? 'folder' : 'file'" :size="14" />
            <span class="fsc-name" :title="e.name">{{ e.name }}</span>
            <span class="tnum">{{ bytes(e.size, "—", 1) }}</span>
          </li>
        </ul>
      </div>
    </template>

    <div class="fsc-actions">
      <button :disabled="busy" @click="withTrail((trail) => organizeFolder(trail))"><Icon name="film" :size="15" />{{ t("整理这个文件夹") }}</button>
      <button class="quiet" :disabled="busy" @click="withTrail((trail) => askAssistant(t('这个文件夹里都有什么？有哪些可以整理或清理？'), { folder: trail }))">
        <Icon name="spark" :size="15" />{{ t("问 AI") }}
      </button>
    </div>
  </section>
</template>

<style scoped>
.folder-stats {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-top: 18px;
  padding: 14px;
  border: 1px solid var(--line);
  border-radius: var(--radius-card);
  background: color-mix(in srgb, var(--canvas) 55%, transparent);
}

.fsc-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

h3 {
  margin: 0;
  font-size: 14px;
  font-weight: 650;
}

.fsc-note,
.fsc-total {
  margin: 0;
  font-size: 13px;
  color: var(--fg-dim);
}

.fsc-cats,
.fsc-largest ul {
  margin: 0;
  padding: 0;
  list-style: none;
}

/* One label column for every row, as wide as the longest label (ドキュメント)
   but never under the 44px the two-character labels are drawn at. */
.fsc-cats {
  display: grid;
  grid-template-columns: minmax(44px, max-content) minmax(0, 1fr) auto;
  gap: 7px 8px;
}

.fsc-cats li {
  grid-column: 1 / -1;
  display: grid;
  grid-template-columns: subgrid;
  align-items: center;
  font-size: 12px;
}

.fsc-cat-label {
  color: var(--fg);
}

.fsc-bar {
  height: 6px;
  border-radius: 6px;
  background: var(--line);
  overflow: hidden;
}

.fsc-bar i {
  display: block;
  height: 100%;
  border-radius: 6px;
  background: var(--accent);
}

.fsc-bar i.image,
.fsc-bar i.audio {
  background: var(--tint-violet);
}

.fsc-bar i.document,
.fsc-bar i.archive,
.fsc-bar i.other {
  background: var(--fg-dim);
}

.fsc-cat-num {
  color: var(--fg-dim);
  white-space: nowrap;
}

.fsc-line {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}

.fsc-line > span {
  flex: 1;
  min-width: 0;
}

.fsc-line > svg {
  flex: none;
  color: var(--fg-dim);
}

.fsc-link {
  display: inline-flex;
  vertical-align: middle;
  min-height: 26px;
  margin: -4px -6px -4px 0;
  padding: 0 6px;
  gap: 2px;
  color: var(--accent);
  font-size: 12px;
  white-space: nowrap;
}

.fsc-sub {
  font-size: 12px;
  color: var(--fg-dim);
}

.fsc-largest ul {
  display: grid;
  gap: 4px;
  margin-top: 6px;
}

.fsc-largest li {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--fg-dim);
}

.fsc-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--fg);
}

.fsc-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 4px;
}

.fsc-actions button {
  min-height: 32px;
  padding: 0 12px;
  font-size: 13px;
}
</style>
