<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import Thumb from "./Thumb.vue";
import SelectMenu from "./SelectMenu.vue";
import { accounts, currentId, drive, locateEntry, play, ui, videoWorksShown } from "../store";
import { clock, shownPath } from "../format";
import { num, t } from "../i18n";
import { inSelectMenu } from "../dom";
import { useCommand } from "../shortcuts";
import {
  continueLibrary, dropAccountMediaLibrary, filterLibrary, libraryQueue, loadMediaLibrary,
  mediaLibrary, preferredVersion, saveLibraryOverride,
  type LibraryEpisode, type LibraryOverride, type LibraryVersion, type LibraryWork,
} from "../medialibrary";

const props = defineProps<{ folderId?: string; starred: boolean; query: string }>();
const selected = ref("");
const season = ref<number | null>(null);
const page = ref(1);
const episodePage = ref(1);
const versions = ref<Record<string, string>>({});
const editing = ref<LibraryVersion | null>(null);
const saving = ref(false);
const editError = ref("");
const grouping = ref("");
const editor = ref<HTMLFormElement>();
const titleInput = ref<HTMLInputElement>();
let opener: HTMLElement | null = null;
const form = ref<LibraryOverride>({ workId: "", title: "", type: "movie", year: 0, season: 1, episode: 1 });
const rows = computed(() => filterLibrary(mediaLibrary.works, props.query, { folderId: props.folderId, starred: props.starred }));
const work = computed(() => rows.value.find((value) => value.key === selected.value) ?? null);
const shown = computed(() => rows.value.slice(0, page.value * 60));
const seasons = computed(() => [...new Set(work.value?.episodes.map((episode) => episode.season) ?? [])]);
const episodes = computed(() => (work.value?.episodes ?? []).filter((episode) => season.value === null || episode.season === season.value));
const shownEpisodes = computed(() => episodes.value.slice(0, episodePage.value * 80));
const manualWorks = computed(() => mediaLibrary.works.filter((value) => value.manual));
const groupOptions = computed(() => [
  { value: "", label: t("新建独立作品") },
  ...manualWorks.value.map((value) => ({
    value: mediaLibrary.overrides[value.episodes[0].versions[0].entry.id].workId,
    label: `${value.title}${value.year ? ` (${value.year})` : ""}`,
  })),
]);
const typeOptions = computed(() => [{ value: "movie" as const, label: t("电影") }, { value: "series" as const, label: t("剧集") }]);
const usable = computed(() => !!currentId.value && currentId.value === mediaLibrary.account && !accounts.switching && !!drive.status?.configured && mediaLibrary.ready);

function picked(episode: LibraryEpisode): LibraryVersion {
  return episode.versions.find((value) => value.entry.id === versions.value[episode.key]) ?? preferredVersion(episode);
}

function open(value: LibraryWork) {
  selected.value = value.key;
  season.value = null;
  episodePage.value = 1;
  versions.value = {};
  editing.value = null;
}

function back() {
  editing.value = null;
  selected.value = "";
}

async function start(value: LibraryVersion) {
  if (!usable.value || !work.value || !work.value.episodes.some((episode) => episode.versions.some((version) => version.entry.id === value.entry.id))) return;
  await play(value.entry, libraryQueue(work.value, value), value.progress ? value.history!.position : 0);
}

async function locate(value: LibraryVersion) {
  if (!usable.value) return;
  ui.videoLayout = "files";
  await locateEntry(value.entry);
}

async function edit(value: LibraryVersion) {
  if (!usable.value || mediaLibrary.loading) return;
  opener = document.activeElement as HTMLElement | null;
  editing.value = value;
  editError.value = "";
  const previous = mediaLibrary.overrides[value.entry.id];
  const episode = work.value?.episodes.find((item) => item.versions.some((version) => version.entry.id === value.entry.id));
  grouping.value = previous?.workId ?? "";
  form.value = previous ? { ...previous } : {
    workId: crypto.randomUUID(), title: work.value?.type === "unknown" ? "" : work.value?.title ?? "",
    type: work.value?.type === "series" ? "series" : "movie", year: work.value?.year ?? 0,
    season: episode?.season ?? 1, episode: episode?.episode || 1,
  };
  await nextTick();
  titleInput.value?.focus();
}

async function closeEditor() {
  if (saving.value) return;
  editing.value = null;
  await nextTick();
  if (opener?.isConnected) opener.focus({ preventScroll: true });
}

function editorKey(event: KeyboardEvent) {
  if (inSelectMenu(event)) return;
  if (event.key === "Escape") { event.preventDefault(); void closeEditor(); }
  if (event.key !== "Tab") return;
  const nodes = Array.from(editor.value?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), [tabindex="0"]') ?? []);
  const first = nodes[0];
  const last = nodes.at(-1);
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
}

function chooseGroup(value: string) {
  grouping.value = value;
  if (!value) { form.value.workId = crypto.randomUUID(); return; }
  const previous = Object.values(mediaLibrary.overrides).find((item) => item.workId === value);
  if (previous) form.value = { ...previous, season: form.value.season, episode: form.value.episode };
}

async function save(reset = false) {
  if (!usable.value || mediaLibrary.loading || !editing.value || saving.value) return;
  const account = currentId.value;
  const fileId = editing.value.entry.id;
  saving.value = true;
  editError.value = "";
  try {
    const saved = await saveLibraryOverride(account, fileId, reset ? null : { ...form.value, title: form.value.title.trim() });
    if (!saved || currentId.value !== account) return;
    editing.value = null;
    const updated = mediaLibrary.works.find((value) => value.episodes.some((episode) => episode.versions.some((version) => version.entry.id === fileId)));
    if (updated) open(updated);
  } catch (error) {
    if (currentId.value === account) editError.value = String(error);
  } finally { if (currentId.value === account) saving.value = false; }
}

watch([() => props.query, () => props.folderId, () => props.starred], () => { page.value = 1; selected.value = ""; editing.value = null; });
watch(season, () => { episodePage.value = 1; });
watch(currentId, (account) => {
  if (account === mediaLibrary.account) return;
  dropAccountMediaLibrary(mediaLibrary.account);
  selected.value = "";
  editing.value = null;
  saving.value = false;
  editError.value = "";
  page.value = 1;
}, { immediate: true, flush: "sync" });
watch([currentId, () => drive.indexProgress?.ready, () => drive.indexProgress?.syncedAt, () => ui.route, videoWorksShown], ([account, ready, revision, route, shown]) => {
  if (route !== "files" || !shown) return;
  if (ready === false && mediaLibrary.ready) dropAccountMediaLibrary(account);
  void loadMediaLibrary(account, false, revision ?? "");
}, { immediate: true });
useCommand("back", () => { if (ui.route === "files" && videoWorksShown.value && selected.value && !editing.value) back(); });
</script>

<template>
  <section class="video-works" :aria-label="t('按作品')" :aria-busy="mediaLibrary.loading">
    <header v-if="work" class="work-head">
      <button class="quiet" @click="back"><Icon name="chevronLeft" :size="16" />{{ t("返回作品") }}</button>
      <h2>{{ work.title }}<small v-if="work.year">{{ String(work.year) }}</small></h2>
    </header>
    <div v-if="mediaLibrary.error" class="notice" role="alert">
      <p>{{ mediaLibrary.error }}</p><button :disabled="mediaLibrary.loading" @click="loadMediaLibrary(currentId)">{{ t("重试") }}</button>
    </div>
    <div v-if="mediaLibrary.historyError" class="notice" role="alert">
      <p>{{ t("播放记录暂不可用，观看状态可能不完整。") }}</p><button :disabled="mediaLibrary.loading" @click="loadMediaLibrary(currentId)">{{ t("重试") }}</button>
    </div>
    <p class="history-note">{{ t("观看状态来自本机最近 50 条和云端最近 200 条播放记录；没有记录不代表从未观看。") }}</p>
    <p v-if="mediaLibrary.historyLoading" class="history-note" role="status">{{ t("正在同步播放记录，不影响浏览和播放。") }}</p>
    <p v-if="mediaLibrary.loading && mediaLibrary.total" class="history-note" role="status">{{ t("已读取 {done} / {total} 个视频，正在生成作品视图…", { done: mediaLibrary.read, total: mediaLibrary.total }) }}</p>
    <div v-if="!work" class="works-summary">
      <span>{{ mediaLibrary.loading ? t("已展示 {n} 部作品 · 已读取 {files} 个视频", { n: rows.length, files: mediaLibrary.read }) : t("{n} 部作品 · {files} 个视频", { n: rows.length, files: rows.reduce((sum, value) => sum + value.episodes.reduce((count, episode) => count + episode.versions.length, 0), 0) }) }}</span>
    </div>
    <div class="scroll">
      <div v-if="mediaLibrary.loading && !mediaLibrary.works.length" class="empty" role="status"><Orbit :size="26" /><h2>{{ t("正在生成作品视图") }}</h2></div>
      <div v-else-if="!mediaLibrary.ready" class="empty" role="status">
        <Icon name="video" :size="36" /><h2>{{ t("建完索引才能按作品查看") }}</h2>
        <p>{{ drive.indexProgress?.error || t("作品视图仅读取本地索引，索引完成后会自动显示。") }}</p>
        <button :disabled="mediaLibrary.loading" @click="loadMediaLibrary(currentId)">{{ t("重试") }}</button>
      </div>
      <template v-else-if="work">
        <div class="work-actions">
          <span class="type">{{ work.type === 'unknown' ? t("未识别") : work.type === 'series' ? t("剧集") : t("电影") }}</span>
          <span>{{ t("已看完 {done} / {total} 集", { done: work.episodes.filter((episode) => episode.finished).length, total: work.episodes.length }) }}</span>
          <button class="primary" :disabled="!usable" @click="start(continueLibrary(work))"><Icon name="play" :size="15" filled />{{ work.progress ? t("继续播放") : t("从头播放") }}</button>
        </div>
        <p v-if="work.type === 'unknown'" class="explain">{{ t("无法可靠识别的文件单独保留。可手动分组，不会更改云盘文件。") }}</p>
        <div v-if="work.type === 'series'" class="seasons" role="group" :aria-label="t('季')">
          <button :class="{ on: season === null }" @click="season = null">{{ t("全部") }}</button>
          <button v-for="value in seasons" :key="value" :class="{ on: season === value }" @click="season = value">{{ value === 0 ? t("特别篇") : t("第 {n} 季", { n: value }) }}</button>
        </div>
        <ol class="episodes">
          <li v-for="episode in shownEpisodes" :key="episode.key" class="episode">
            <div class="episode-body">
              <h2>{{ work.type === 'series' ? t("第 {season} 季 · 第 {episode} 集", { season: episode.season, episode: episode.episode }) : picked(episode).entry.name }}</h2>
              <p class="filename" :title="picked(episode).entry.name">{{ picked(episode).entry.name }}</p>
              <p class="path">{{ shownPath(picked(episode).entry.path) }}</p>
              <span v-if="episode.finished" class="finished">{{ t("已看完") }}</span>
              <span v-else-if="picked(episode).progress">{{ t("已播放 {time}", { time: clock(picked(episode).history!.position) }) }}</span>
              <span v-else>{{ t("未记录观看") }}</span>
              <progress v-if="picked(episode).history && picked(episode).history!.duration > 0" :value="Math.min(picked(episode).history!.position, picked(episode).history!.duration)" :max="picked(episode).history!.duration" :aria-label="t('观看进度')" />
            </div>
            <div class="episode-actions">
              <SelectMenu v-if="episode.versions.length > 1" :model-value="picked(episode).entry.id" :options="episode.versions.map((version) => ({ value: version.entry.id, label: version.entry.name, detail: version.version }))" :label="t('版本')" @update:model-value="versions[episode.key] = $event" />
              <div>
                <button :disabled="!usable" @click="start(picked(episode))"><Icon name="play" :size="14" filled />{{ picked(episode).progress ? t("继续播放") : t("播放") }}</button>
                <button :disabled="!usable" @click="locate(picked(episode))">{{ t("定位") }}</button>
                <button :disabled="!usable || mediaLibrary.loading" @click="edit(picked(episode))">{{ t("手动分组") }}</button>
              </div>
            </div>
          </li>
        </ol>
        <button v-if="shownEpisodes.length < episodes.length" class="more" @click="episodePage++">{{ t("加载更多") }} · {{ num(episodes.length - shownEpisodes.length) }}</button>
      </template>
      <template v-else-if="shown.length">
        <ul class="works">
          <li v-for="value in shown" :key="value.key">
            <button class="work-card" @click="open(value)">
              <div class="art"><Thumb :src="value.episodes[0].versions[0].entry.thumbnail" :alt="value.title" lazy><Icon name="video" :size="34" /></Thumb><span class="work-kind">{{ value.type === 'unknown' ? t("未识别") : value.type === 'series' ? t("剧集") : t("电影") }}</span></div>
              <div class="meta">
                <h2 :title="value.title">{{ value.title }}</h2>
                <p>{{ value.year ? String(value.year) : t("年份未知") }} · {{ t("{n} 个视频", { n: value.episodes.reduce((sum, episode) => sum + episode.versions.length, 0) }) }}</p>
                <p class="watch-state">{{ value.finished ? t("已看完") : value.progress ? t("观看中") : t("未记录观看") }}<span v-if="value.manual"> · {{ t("手动分组") }}</span></p>
              </div>
            </button>
          </li>
        </ul>
        <button v-if="shown.length < rows.length" class="more" @click="page++">{{ t("加载更多") }} · {{ num(rows.length - shown.length) }}</button>
      </template>
      <div v-else class="empty" role="status">
        <Icon name="video" :size="36" /><h2>{{ t("当前范围没有符合条件的作品") }}</h2>
        <p>{{ t("可切换浏览范围、清除搜索，或按文件查看视频。") }}</p>
      </div>
    </div>
    <div v-if="editing" class="scrim group-shade" @keydown.stop="editorKey">
      <form ref="editor" class="group-editor glass" role="dialog" aria-modal="true" :aria-label="t('手动分组')" @submit.prevent="save()">
        <h2>{{ t("手动分组") }}</h2><p class="filename">{{ editing.entry.name }}</p><p>{{ t("仅保存在本机当前账号，不更改文件名或位置。") }}</p>
        <fieldset :disabled="saving">
          <label>{{ t("加入分组") }}<SelectMenu :model-value="grouping" :options="groupOptions" :label="t('加入分组')" :disabled="saving" @update:model-value="chooseGroup" /></label>
          <label>{{ t("作品名称") }}<input ref="titleInput" v-model="form.title" required maxlength="300" :readonly="!!grouping" /></label>
          <div class="fields">
            <label>{{ t("类型") }}<SelectMenu v-model="form.type" :options="typeOptions" :label="t('类型')" :disabled="!!grouping || saving" /></label>
            <label>{{ t("年份（0 表示未知）") }}<input v-model.number="form.year" type="number" min="0" max="2200" :readonly="!!grouping" required /></label>
          </div>
          <div v-if="form.type === 'series'" class="fields">
            <label>{{ t("季") }}<input v-model.number="form.season" type="number" min="0" max="999" required /></label>
            <label>{{ t("集") }}<input v-model.number="form.episode" type="number" min="1" max="9999" required /></label>
          </div>
        </fieldset>
        <p v-if="editError" class="edit-error" role="alert">{{ editError }}</p>
        <footer>
          <button v-if="mediaLibrary.overrides[editing.entry.id]" type="button" :disabled="saving" @click="save(true)">{{ t("恢复自动识别") }}</button>
          <span /><button type="button" :disabled="saving" @click="closeEditor">{{ t("取消") }}</button>
          <button type="submit" class="primary" :disabled="saving || !form.title.trim()">{{ saving ? t("保存中") : t("保存") }}</button>
        </footer>
      </form>
    </div>
  </section>
</template>

<style scoped>
.video-works {
  --t1: var(--fg);
  --t3: var(--fg-dim);
  --surface: var(--glass-control);
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  overflow: hidden;
  min-height: 0;
  position: relative;
  container: library / inline-size;
}
.work-head { display: flex; align-items: center; gap: 16px; margin-bottom: 18px; flex-wrap: wrap; }
.work-head h2 { font-size: 21px; margin: 0; overflow-wrap: anywhere; }
.work-head small { margin-left: 10px; color: var(--t3); font-size: 14px; font-weight: 400; }
.explain { color: var(--t3); font-size: 12px; line-height: 1.7; margin: 0; }
.history-note { color: var(--t3); font-size: 11px; line-height: 1.7; margin: 0 0 10px; }
.works-summary { margin-bottom: 16px; color: var(--t3); font-size: 12px; }
.seasons .on { background: var(--accent-soft); color: var(--accent); }
.scroll { overflow: auto; min-height: 0; flex: 1; padding-bottom: 40px; scrollbar-gutter: stable; }
.works { list-style: none; display: grid; grid-template-columns: repeat(auto-fill, minmax(195px, 1fr)); gap: 20px; padding: 0; margin: 0; }
.works > li { min-width: 0; }
.work-card { display: block; text-align: left; padding: 0; width: 100%; overflow: hidden; border-radius: 16px; background: var(--surface); border: 1px solid var(--line); box-shadow: none; color: var(--t1); }
.work-card:active { transform: scale(.98); }
.art { height: 125px; background: var(--accent-soft); position: relative; overflow: hidden; content-visibility: auto; }
.work-kind { position: absolute; left: 10px; bottom: 10px; background: rgba(0, 0, 0, .6); color: white; border-radius: 6px; padding: 3px 7px; font-size: 10px; }
.meta { padding: 15px; }
.meta h2 { font-size: 15px; line-height: 1.45; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; margin: 0 0 8px; min-height: 22px; }
.meta p { font-size: 11px; color: var(--t3); margin: 5px 0; line-height: 1.6; }
.meta .watch-state { color: var(--accent); }
.empty { display: flex; align-items: center; justify-content: center; flex-direction: column; text-align: center; min-height: 320px; gap: 16px; color: var(--t3); }
.empty h2 { color: var(--t1); font-size: 18px; margin: 0; }
.empty p { max-width: 450px; font-size: 12px; line-height: 1.7; margin: 0; }
.notice { display: flex; align-items: center; gap: 16px; border: 1px solid var(--line); border-radius: 12px; padding: 12px 16px; margin-bottom: 16px; font-size: 12px; }
.notice p { flex: 1; margin: 0; overflow-wrap: anywhere; }
.notice button { flex-shrink: 0; }
.work-actions { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; margin: 0 0 20px; font-size: 12px; color: var(--t3); }
.work-actions .type { background: var(--accent-soft); color: var(--accent); padding: 5px 9px; border-radius: 6px; }
.work-actions > button { margin-left: auto; }
.explain { margin-bottom: 20px; }
.seasons { display: flex; gap: 8px; flex-wrap: wrap; margin-bottom: 18px; }
.episodes { list-style: none; padding: 0; margin: 0; }
.episode { display: flex; gap: 24px; align-items: center; border-bottom: 1px solid var(--line); padding: 20px 0; }
.episode-body { flex: 1; min-width: 0; }
.episode h2 { font-size: 14px; margin: 0 0 7px; overflow-wrap: anywhere; }
.filename { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.episode p { color: var(--t3); font-size: 11px; margin: 5px 0; }
.episode-body > span { font-size: 11px; color: var(--t3); }
.episode-body > .finished { color: var(--accent); }
.path { overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.episode progress { display: block; width: 100%; height: 3px; margin-top: 12px; accent-color: var(--accent); border: 0; }
.episode-actions { display: flex; flex-direction: column; gap: 10px; max-width: 48%; align-items: flex-end; min-width: 0; }
.episode-actions > div { display: flex; gap: 7px; flex-wrap: wrap; justify-content: flex-end; }
.episode-actions :deep(.select-menu) { max-width: min(280px, 100%); min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
input { font: inherit; color: var(--t1); background: var(--surface); border: 1px solid var(--line); border-radius: 8px; padding: 8px; min-width: 0; }
.more { display: block; margin: 24px auto 0; }
.group-shade { position: absolute; inset: 0; z-index: 10; background: rgba(0, 0, 0, .28); display: flex; align-items: center; justify-content: center; padding: 24px; backdrop-filter: blur(6px); }
.group-editor { width: 480px; max-width: 100%; max-height: 100%; overflow: auto; border-radius: 20px; padding: 26px; }
.group-editor h2 { font-size: 20px; margin: 0 0 12px; }
.group-editor > p { font-size: 12px; color: var(--t3); line-height: 1.7; }
.group-editor fieldset { border: 0; padding: 0; margin: 20px 0; display: grid; gap: 14px; min-width: 0; }
.group-editor label { display: flex; flex-direction: column; gap: 7px; font-size: 12px; min-width: 0; }
.fields { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.group-editor footer { display: flex; align-items: center; gap: 8px; margin-top: 20px; flex-wrap: wrap; }
.group-editor footer > span { flex: 1; }
.group-editor .edit-error { color: var(--danger); overflow-wrap: anywhere; }
@container library (max-width: 660px) {
  .episode { align-items: flex-start; flex-direction: column; gap: 14px; }
  .episode-actions { align-items: flex-start; max-width: 100%; width: 100%; }
  .episode-actions > div { justify-content: flex-start; }
  .work-actions > button { margin-left: 0; }
  .works { grid-template-columns: repeat(auto-fill, minmax(165px, 1fr)); }
}
</style>
