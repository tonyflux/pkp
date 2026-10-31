<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import ConfirmInline from "./ConfirmInline.vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import Thumb from "./Thumb.vue";
import { clock, stamp } from "../format";
import { N_, t } from "../i18n";
import { playFinished, playHasProgress } from "../plays";
import { vSlide } from "../slide";
import { clearPlayHistory, currentId, forgetPlay, loadPlayHistory, playHistory, resumePlay, ui } from "../store";

type Filter = "all" | "unfinished" | "finished";
const filters: { key: Filter; label: string }[] = [
  { key: "all", label: N_("全部") },
  { key: "unfinished", label: N_("未看完") },
  { key: "finished", label: N_("已看完") },
];
const filter = ref<Filter>("all");
const layout = ref<"grid" | "list">(localStorage.getItem("pkp.historyLayout") === "list" ? "list" : "grid");
watch(layout, (value) => localStorage.setItem("pkp.historyLayout", value));
const confirming = ref(false);
const scroll = ref<HTMLElement | null>(null);
let scrollTop = 0;
const historyCurrent = computed(() => playHistory.account === currentId.value);
const records = computed(() => historyCurrent.value ? playHistory.list : []);
const pendingLabel = computed(() => historyCurrent.value && !playHistory.localLoading && playHistory.syncing
  ? t("正在同步云端播放记录") : t("正在读取播放记录"));

const rows = computed(() => {
  const needle = ui.playQuery.trim().toLowerCase();
  return records.value
    .map((record) => ({ record, finished: playFinished(record.position, record.duration), progress: playHasProgress(record.position, record.duration) }))
    .filter(({ record, finished }) => (!needle || record.name.toLowerCase().includes(needle))
      && (filter.value === "all" || (filter.value === "finished" ? finished : !finished)));
});

function resetFilters() {
  filter.value = "all";
  ui.playQuery = "";
}

async function clear() {
  if (playHistory.clearing) return;
  if (await clearPlayHistory()) confirming.value = false;
}

// App keeps this canvas mounted beneath immersive playback; restore its own
// scroller after display:none without touching the workspace's scroll position.
watch([currentId, () => ui.route], async ([id, route], previous) => {
  if (previous && id !== previous[0]) {
    confirming.value = false;
    scrollTop = 0;
    if (scroll.value) scroll.value.scrollTop = 0;
  } else if (previous?.[1] === "history" && route !== "history") {
    scrollTop = scroll.value?.scrollTop ?? 0;
  }
  if (route !== "history") return;
  void loadPlayHistory(false);
  await nextTick();
  if (scroll.value) scroll.value.scrollTop = scrollTop;
}, { immediate: true, flush: "pre" });
</script>

<template>
  <section class="history canvas" :aria-label="t('播放历史')" :aria-busy="playHistory.loading || playHistory.clearing">
    <header class="head">
      <div>
        <h1>{{ t("播放历史") }}</h1>
        <p>{{ t("继续未完的观看，或重温看过的视频。") }}</p>
      </div>
      <div class="head-actions">
        <button :disabled="playHistory.loading || playHistory.clearing" @click="loadPlayHistory()">
          <Orbit v-if="playHistory.loading" :size="16" />
          <Icon v-else name="restore" :size="16" />
          {{ playHistory.loading ? t("刷新中") : t("刷新") }}
        </button>
        <button :disabled="!records.length || playHistory.clearing" @click="confirming = !confirming">
          <Orbit v-if="playHistory.clearing" :size="16" />
          <Icon v-else name="trash" :size="16" />
          {{ playHistory.clearing ? t("正在清除播放记录") : t("清除播放记录") }}
        </button>
      </div>
    </header>

    <fieldset v-if="confirming" class="clear-confirm" :disabled="playHistory.clearing">
      <ConfirmInline
        :message="t('仅在这台电脑上隐藏当前账号的已有播放记录，不删除文件，也不删除云端播放记录。')"
        :confirm-label="playHistory.clearing ? t('正在清除播放记录') : t('清除播放记录')"
        danger
        @confirm="clear"
        @cancel="confirming = false"
      />
    </fieldset>

    <div class="controls">
      <div v-slide class="segments filters" role="group" :aria-label="t('观看状态')">
        <button v-for="item in filters" :key="item.key" :class="{ on: filter === item.key }" :aria-pressed="filter === item.key" @click="filter = item.key">
          {{ t(item.label) }}
        </button>
      </div>
      <div v-slide class="segments layouts" role="group" :aria-label="t('显示方式')">
        <button :class="{ on: layout === 'grid' }" :aria-pressed="layout === 'grid'" :aria-label="t('网格')" :title="t('网格')" @click="layout = 'grid'">
          <Icon name="grid" :size="18" />
        </button>
        <button :class="{ on: layout === 'list' }" :aria-pressed="layout === 'list'" :aria-label="t('列表')" :title="t('列表')" @click="layout = 'list'">
          <Icon name="list" :size="18" />
        </button>
      </div>
    </div>

    <div v-if="historyCurrent && playHistory.error" class="error" role="alert">
      <Icon name="alert" :size="18" />
      <div><strong>{{ t("无法读取播放记录") }}</strong><p>{{ playHistory.error }}</p></div>
      <button :disabled="playHistory.loading || playHistory.clearing" @click="loadPlayHistory()">{{ t("重试") }}</button>
    </div>

    <p v-if="historyCurrent && playHistory.syncing" class="sync-status" role="status">
      <Orbit :size="14" />
      {{ t("正在同步云端播放记录，已读取的记录可先播放。") }}
    </p>

    <div ref="scroll" class="scroll" @scroll="scrollTop = scroll?.scrollTop ?? 0">
      <div v-if="!records.length && (!historyCurrent || playHistory.localLoading || playHistory.loading)" class="empty" role="status">
        <Orbit :size="28" :label="pendingLabel" />
        <p>{{ pendingLabel }}</p>
      </div>
      <template v-else-if="records.length">
        <div v-if="!rows.length" class="empty" role="status">
          <Icon name="search" :size="32" />
          <h2>{{ playHistory.loading ? t("正在读取播放记录") : t("没有符合条件的播放记录") }}</h2>
          <p>{{ playHistory.loading ? t("记录仍在读取，稍后可能会有更多结果。") : t("试试其他名称，或查看全部观看状态。") }}</p>
          <button @click="resetFilters">{{ t("查看全部") }}</button>
        </div>
        <ul v-else class="records" :class="layout">
          <li v-for="{ record, finished, progress } in rows" :key="record.fileId" class="record" :class="{ unavailable: !record.available }">
            <div class="thumbnail">
              <Thumb :src="record.thumbnail" :alt="record.name" lazy><Icon name="video" :size="30" /></Thumb>
              <span v-if="record.duration > 0" class="runtime tnum">{{ clock(record.duration) }}</span>
            </div>
            <div class="record-body">
              <h2 :title="record.name">{{ record.name }}</h2>
              <p class="when">{{ t("上次播放 · {when}", { when: stamp(record.playedAt) }) }}</p>
              <div class="watch-state">
                <span v-if="finished" class="finished"><Icon name="check" :size="14" />{{ t("已看完") }}</span>
                <span v-else-if="progress" class="tnum">{{ t("已播放 {time}", { time: clock(record.position) }) }}</span>
                <span v-else>{{ t("未看完") }}</span>
                <span v-if="record.duration > 0 && layout === 'list'" class="duration tnum">{{ clock(record.duration) }}</span>
              </div>
              <progress v-if="progress" :value="Math.min(record.position, record.duration)" :max="record.duration" :aria-label="t('观看进度')" />
              <p v-if="!record.available" class="reason">{{ t("文件已删除或无权访问") }}</p>
            </div>
            <div class="record-actions">
              <button
                class="play-button"
                :disabled="!record.available || playHistory.clearing"
                :title="!record.available ? t('文件已删除或无权访问') : undefined"
                :aria-label="t('播放「{name}」', { name: record.name })"
                @click="resumePlay(record)"
              >
                <Icon name="play" :size="15" filled />
                {{ finished ? t("从头播放") : t("继续播放") }}
              </button>
              <button v-if="!record.available && record.local" class="quiet forget" :disabled="playHistory.clearing" :title="t('仅移除本地记录，云端记录可能再次出现。')" @click="forgetPlay(record)">
                {{ t("移除记录") }}
              </button>
            </div>
          </li>
        </ul>
      </template>
      <div v-else-if="historyCurrent && playHistory.loaded && !playHistory.loading && !playHistory.error" class="empty" role="status">
        <Icon name="clock" :size="36" />
        <h2>{{ t("还没有播放记录") }}</h2>
        <p>{{ t("播放后可在这里继续观看") }}</p>
      </div>
    </div>
  </section>
</template>

<style scoped>
.history {
  container-type: inline-size;
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: 22px 20px 10px;
}
.head { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; }
.head > div { min-width: 0; }
h1 { margin: 0; font-size: 28px; font-weight: 600; line-height: 36px; }
.head p { margin: 4px 0 0; font-size: 14px; color: var(--fg-dim); }
.head-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 8px; }
.head-actions button { font-size: 13px; }
.clear-confirm { margin: 16px 0 0; padding: 0; border: 0; min-inline-size: 0; }
.controls { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 8px; margin: 18px 0 14px; }
.segments { display: flex; align-items: center; padding: 4px; border-radius: var(--radius-pill); }
.segments button { min-height: 36px; padding: 0 18px; border: none; background: transparent; box-shadow: none; color: var(--fg-dim); font-size: 14px; }
.segments::before { background: var(--primary); box-shadow: inset 0 1px 0 var(--primary-rim), 0 4px 12px var(--primary-glow); }
.segments button.on { color: var(--on-primary); font-weight: 600; }
.layouts button { width: 40px; padding: 0; border-radius: 11px; }
.layouts::before { background: var(--accent-soft); box-shadow: none; }
.layouts button.on { color: var(--accent); }
.error { display: flex; align-items: center; gap: 10px; padding: 12px; margin-bottom: 12px; border-radius: 12px; color: var(--danger); background: color-mix(in srgb, var(--danger) 7%, transparent); font-size: 13px; }
.error > div { flex: 1; min-width: 0; }
.error p { margin: 4px 0 0; overflow-wrap: anywhere; }
.error > :first-child { flex: none; }
.sync-status { display: flex; align-items: center; gap: 8px; margin: 0 0 12px; color: var(--fg-dim); font-size: 12px; line-height: 1.6; }
.sync-status > :first-child { flex: none; }
.scroll { flex: 1; min-height: 0; overflow-y: auto; padding: 2px 2px 18px; scrollbar-gutter: stable; }
.records { list-style: none; margin: 0; padding: 0; }
.records.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(220px, 100%), 1fr)); gap: 18px; }
.record { min-width: 0; }
.grid .record { display: flex; flex-direction: column; padding: 10px; border: 1px solid var(--line); border-radius: 16px; }
.thumbnail { position: relative; flex: none; aspect-ratio: 16 / 9; overflow: hidden; border-radius: var(--radius-card); background: color-mix(in srgb, var(--fg) 7%, transparent); }
.runtime { position: absolute; bottom: 7px; right: 7px; padding: 2px 6px; border-radius: 6px; background: rgba(0, 0, 0, 0.7); color: white; font-size: 11px; }
.record-body { min-width: 0; }
.grid .record-body { flex: 1; padding: 12px 2px 0; }
.record h2 { margin: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 14px; font-weight: 600; line-height: 22px; }
.when { margin: 4px 0 8px; font-size: 12px; color: var(--fg-dim); }
.watch-state { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; font-size: 12px; color: var(--fg-dim); }
.finished { display: inline-flex; align-items: center; gap: 4px; color: var(--accent); }
.duration { margin-left: auto; }
progress { display: block; width: 100%; height: 3px; margin-top: 8px; border: 0; border-radius: 3px; overflow: hidden; appearance: none; background: color-mix(in srgb, var(--fg) 9%, transparent); }
progress::-webkit-progress-bar { background: color-mix(in srgb, var(--fg) 9%, transparent); border-radius: 3px; }
progress::-webkit-progress-value { background: var(--accent); border-radius: 3px; }
progress::-moz-progress-bar { background: var(--accent); border-radius: 3px; }
.reason { margin: 8px 0 0; color: var(--fg-dim); font-size: 12px; line-height: 1.5; }
.unavailable .thumbnail { opacity: 0.55; }
.record-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
.grid .record-actions { margin-top: 12px; }
.record-actions button { min-height: 34px; padding: 0 12px; font-size: 12px; }
.play-button { color: var(--accent); }
.forget { color: var(--fg-dim); }
.list .record { display: flex; align-items: center; gap: 16px; padding: 14px 2px; border-bottom: 1px solid var(--line); }
.list .thumbnail { width: 132px; }
.list .record-body { flex: 1; }
.list .record-actions { flex-direction: column; align-items: stretch; }
.empty { min-height: 240px; display: flex; flex-direction: column; justify-content: center; align-items: center; gap: 12px; padding: 32px 16px; text-align: center; color: var(--fg-dim); }
.empty h2 { margin: 0; font-size: 18px; font-weight: 600; color: var(--fg); }
.empty p { margin: 0; max-width: 32em; font-size: 13px; line-height: 1.6; }
@container (max-width: 640px) {
  .head { flex-direction: column; gap: 12px; }
  .head-actions { justify-content: flex-start; }
  .list .record { flex-wrap: wrap; gap: 12px; }
  .list .thumbnail { width: 100px; }
  .list .record-actions { width: 100%; flex-direction: row; justify-content: flex-end; }
}
@container (max-width: 380px) {
  .segments button { padding: 0 12px; font-size: 13px; }
  .layouts button { width: 36px; padding: 0; }
  .list .thumbnail { width: 76px; }
}
</style>
