<script setup lang="ts">
// Cleanup · Empty Folders: only the outermost
// empty folders, each with how many empty ones it holds; moving one to the
// trash takes the whole empty subtree. Folders made in the last 24 hours and
// ones holding recycle-bin entries are listed but start unticked. There is no
// Delete Permanently here.
import { computed, ref } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import CleanupDock from "./CleanupDock.vue";
import CleanupScopePick from "./CleanupScopePick.vue";
import { ago, shownPath, stamp } from "../format";
import { t, num } from "../i18n";
import { useRowWindows } from "../windowing";
import { REMOVE_LIMIT, cleanup, emptyPicked, openCleanupConfirm, pickAllEmpty, toggleEmptyFolder } from "../store";
import type { EmptyFolder } from "../../bindings/github.com/tonyflux/pkp/internal/services";

const e = computed(() => cleanup.empty);
const page = computed(() => e.value.page);
const folders = computed(() => page.value?.folders ?? []);
const busy = computed(() => !!cleanup.running);

const synced = computed(() => (page.value?.syncedAt ? t("最后同步 {time}", { time: stamp(page.value.syncedAt) }) : t("本地索引")));

const scroller = ref<HTMLElement | null>(null);
const { windows } = useRowWindows<EmptyFolder>({
  scroller,
  lists: () => [{ id: "folders", items: folders.value }],
  key: (f) => f.id,
  shape: () => "folder",
});
const drawn = computed(() => windows.value.folders ?? { rows: [], style: {} });

/** Why a folder starts unticked, "" when it does not. */
function reason(f: EmptyFolder): string {
  if (f.hasTrashed) return t("里面有回收站条目");
  if (f.recent) return t("刚创建");
  return "";
}

const count = computed(() => emptyPicked.value.length);
const nested = computed(() => emptyPicked.value.reduce((sum, f) => sum + f.nested, 0));
const tooMany = computed(() => count.value > REMOVE_LIMIT);
const dockTitle = computed(() => t("将处理 {n} 个顶层文件夹 · 连同 {nested} 个嵌套文件夹", { n: count.value, nested: nested.value }));
const dockHint = computed(() =>
  tooMany.value
    ? t("一次最多处理 {n} 项，请缩小范围或取消部分勾选", { n: REMOVE_LIMIT })
    : t("空文件夹不占空间，只能移到回收站；发送前会逐个重新确认。"),
);
</script>

<template>
  <div class="tab">
    <div ref="scroller" class="scroll">
      <div class="content">
        <section class="hero" :aria-label="t('空文件夹')">
          <span class="hero-mark" aria-hidden="true"><Icon name="folder" :size="26" /></span>
          <div class="hero-copy">
            <span class="kicker">{{ t("文件夹整理 · 不释放空间") }}</span>
            <h2>{{ t("留下的空壳，也一起收好。") }}</h2>
            <p>{{ t("只显示最外层空文件夹；移走一项，里面的空文件夹也会一起进入回收站。") }}</p>
          </div>
          <div class="metric">
            <strong class="tnum">{{ num(page?.total ?? 0) }}</strong>
            <span>{{ t("个空文件夹") }}</span>
            <small class="tnum">{{ t("其中 {n} 个顶层", { n: folders.length }) }}</small>
          </div>
        </section>

        <div class="list-head">
          <div>
            <strong>{{ folders.length ? t("顶层空文件夹") : t("没有空文件夹") }}</strong>
            <span>{{ t("按完整路径排序 · {synced}", { synced }) }}</span>
            <Orbit v-if="e.loading" :size="14" :label="t('读取中')" />
          </div>
          <div class="tools">
            <span class="scope-label">{{ t("扫描范围") }}</span>
            <CleanupScopePick />
            <template v-if="folders.length">
              <button class="quiet link" :disabled="busy" @click="pickAllEmpty(true)">{{ t("全选") }}</button>
              <span class="divider" aria-hidden="true" />
              <button class="quiet link" :disabled="busy" @click="pickAllEmpty(false)">{{ t("全不选") }}</button>
            </template>
          </div>
        </div>

        <p v-if="e.error" class="error" role="alert">{{ t("没能读取：{error}", { error: e.error }) }}</p>

        <div v-if="!folders.length && !e.loading" class="none">
          <Icon name="check" :size="22" />
          <strong>{{ t("没有空文件夹") }}</strong>
          <span>{{ t("空文件夹指整棵子树里没有任何未回收的文件；「My Pack」等特殊文件夹和根目录不算在内。") }}</span>
        </div>

        <div v-else data-list="folders" class="rows" :style="drawn.style">
          <label
            v-for="{ item: f, index, style } in drawn.rows"
            :key="f.id"
            class="row"
            :class="{ off: !e.picked[f.id], last: index === folders.length - 1 }"
            :data-row="f.id"
            :style="style"
          >
            <input
              type="checkbox"
              :checked="!!e.picked[f.id]"
              :disabled="busy"
              :aria-label="t('选择「{name}」', { name: f.path })"
              @change="toggleEmptyFolder(f.id, ($event.target as HTMLInputElement).checked)"
            />
            <Icon name="folder" :size="20" />
            <div class="details">
              <strong :title="shownPath(f.path)">{{ shownPath(f.path) }}</strong>
              <small>{{ f.nested ? t("里面还套着 {n} 个空文件夹", { n: f.nested }) : t("无嵌套空文件夹") }}</small>
            </div>
            <div class="meta">
              <span :class="{ warn: !!reason(f) }">{{ reason(f) || t("可移到回收站") }}</span>
              <small>{{ t("创建于 {time}", { time: ago(f.createdTime) }) }}</small>
            </div>
          </label>
        </div>
      </div>
    </div>

    <CleanupDock :title="dockTitle" :hint="dockHint" :warn="tooMany">
      <button class="primary go" :disabled="busy || !count || tooMany" @click="openCleanupConfirm('empty')">
        {{ t("移到回收站") }}
        <Icon name="arrowRight" :size="15" />
      </button>
    </CleanupDock>
  </div>
</template>

<style scoped>
.tab {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
}

.scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  scrollbar-width: thin;
}

.content {
  max-width: 1380px;
  margin: 0 auto;
  padding: 22px var(--pad) 120px;
}

.hero {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 22px;
  padding: 26px;
  border-radius: 20px;
  border: 1px solid var(--line-strong);
  background: linear-gradient(140deg, var(--accent-soft), transparent 70%), color-mix(in srgb, var(--fg) 3%, transparent);
}

.hero-mark {
  display: grid;
  place-items: center;
  width: 58px;
  height: 58px;
  border-radius: 16px;
  border: 1px solid var(--line-strong);
  background: color-mix(in srgb, var(--glass-control) 60%, transparent);
  color: var(--fg-dim);
}

.kicker {
  font-size: 12px;
  font-weight: 600;
  color: var(--fg-dim);
}

.hero h2 {
  margin: 4px 0;
  font-size: 21px;
  letter-spacing: -0.01em;
}

.hero p {
  margin: 0;
  font-size: 12px;
  color: var(--fg-dim);
}

.metric {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  padding-left: 28px;
  border-left: 1px solid var(--line);
}

.metric strong {
  font-size: 40px;
  line-height: 1.05;
  font-weight: 700;
}

.metric span {
  font-size: 12px;
}

.metric small {
  margin-top: 4px;
  font-size: 12px;
  color: var(--fg-dim);
}

.list-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  margin: 26px 0 12px;
}

.list-head > div:first-child {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.list-head strong {
  font-size: 16px;
  font-weight: 650;
}

.list-head > div:first-child > span {
  font-size: 12px;
  color: var(--fg-dim);
}

.tools {
  display: flex;
  align-items: center;
  gap: 8px;
}

.scope-label {
  font-size: 12px;
  color: var(--fg-dim);
}

.link {
  min-height: 30px;
  padding: 2px 8px;
  font-size: 13px;
  color: var(--accent);
}

.divider {
  width: 1px;
  height: 14px;
  background: var(--line-strong);
}

.error {
  margin: 0 0 12px;
  font-size: 13px;
  color: var(--danger);
}

.none {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  max-width: 520px;
  margin: 0 auto;
  padding: 42px 0;
  text-align: center;
  color: var(--fg-dim);
  font-size: 13px;
  text-wrap: balance;
}

.none svg {
  color: var(--success);
}

.none strong {
  font-size: 16px;
  color: var(--fg);
}

.rows {
  position: relative;
  border-radius: 16px;
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--fg) 3%, transparent);
  overflow: hidden;
}

.row {
  display: grid;
  grid-template-columns: auto auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 14px;
  min-height: 64px;
  padding: 10px 18px 10px 20px;
  border-bottom: 1px solid var(--line);
  cursor: pointer;
}

/* The list's last row, not the last drawn one (the list is windowed). */
.row.last {
  border-bottom: 0;
}

.row input {
  width: 17px;
  height: 17px;
  margin: 0;
  padding: 0;
  accent-color: var(--accent);
}

.row > svg {
  color: var(--fg-dim);
}

.details {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.details strong {
  font-size: 13px;
  font-weight: 650;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.details small,
.meta small {
  font-size: 12px;
  color: var(--fg-dim);
}

.meta {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 2px;
  white-space: nowrap;
}

.meta span {
  font-size: 12px;
  font-weight: 600;
}

.meta span.warn {
  color: var(--warning);
}

.off .details strong {
  font-weight: 500;
}

.go {
  gap: 6px;
}

.go svg {
  transform: rotate(-45deg);
}

@container (max-width: 850px) {
  .scope-label {
    display: none;
  }

  .metric strong {
    font-size: 32px;
  }
}

@container (max-width: 650px) {
  .hero {
    grid-template-columns: minmax(0, 1fr) auto;
    padding: 20px;
  }

  .hero-mark {
    display: none;
  }

  .metric {
    padding-left: 16px;
  }

  .row {
    padding: 10px 12px;
    gap: 10px;
  }

  .meta small {
    display: none;
  }
}
</style>
