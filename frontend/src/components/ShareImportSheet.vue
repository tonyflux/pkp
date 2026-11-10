<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { Browser } from "@wailsio/runtime";
import Icon from "./Icon.vue";
import Thumb from "./Thumb.vue";
import Orbit from "./Orbit.vue";
import FileViewSwitch from "./FileViewSwitch.vue";
import PickerItems from "./PickerItems.vue";
import SelectMenu, { type SelectOption } from "./SelectMenu.vue";
import { accountLabel, accounts, appLocked, notify, relocate } from "../store";
import { bytes, clock, kindLabel, maskLogin } from "../format";
import { t } from "../i18n";
import type { ShareEntry } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import {
  shareImport as s, browseShare, chooseShareAccount, closeShareImport, closeSharePreview,
  confirmShareRestore, inspectShareInput, openShareDestination, pickShareDestination,
  previewShareEntry, retrySharePage, selectVisibleShare, shareCovered, shareNeedsCode, shareSelected, toggleShareEntry,
} from "../shareimport";

const sheet = ref<HTMLElement>();
const previewCard = ref<HTMLElement>();
const layout = ref<"grid" | "list">("grid");
const fileWell = ref<HTMLElement>();
let viewScroll = { grid: 0, list: 0 };
watch(layout, async (view, previous) => {
  const box = fileWell.value?.querySelector<HTMLElement>(".file-scroll");
  if (box) viewScroll[previous] = box.scrollTop;
  await nextTick();
  const current = fileWell.value?.querySelector<HTMLElement>(".file-scroll");
  if (current) current.scrollTop = viewScroll[view];
}, { flush: "pre" });
watch(() => `${s.account}:${s.info?.shareId}:${s.trail.at(-1)?.id ?? ''}`, () => { viewScroll = { grid: 0, list: 0 }; });
const resourceLoading = ref(false);
let autoPlayPending = false;
watch(() => s.preview, preview => {
  const isMedia = !!preview && (preview.kind === "video" || preview.kind === "audio");
  resourceLoading.value = !!preview && (isMedia || preview.kind === "image" || preview.kind === "pdf");
  autoPlayPending = isMedia;
});
let previousFocus: HTMLElement | null = null;
let previewFocus: HTMLElement | null = null;
watch(() => s.open, async open => {
  if (!open) { previousFocus?.focus(); return; }
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  await nextTick();
  if (s.open) sheet.value?.focus();
});
watch(() => s.previewOpen, async open => {
  if (!open) {
    const target = previewFocus;
    await nextTick();
    if (s.open && !s.previewOpen && !appLocked.value && target?.isConnected) target.focus();
    return;
  }
  previewFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  await nextTick();
  if (s.previewOpen) previewCard.value?.querySelector<HTMLButtonElement>("button")?.focus();
});
watch(appLocked, locked => { if (locked) closeSharePreview(); });
const selectedSize = computed(() => [...s.picked.values()].reduce((sum, pick) => sum + pick.entry.size, 0));
const destination = computed(() => [t("全部文件"), ...s.dest.map(c => c.name)].join(" / "));
const ready = computed(() => s.info?.status === "OK");
const receivingAccount = computed(() => accounts.list.find(account => account.id === s.account));
const accountOptions = computed<SelectOption<string>[]>(() => accounts.list.map(account => ({
  value: account.id, label: accountLabel(account), detail: maskLogin(account.login),
})));
function previewFailed() {
  resourceLoading.value = false;
  s.previewError = t("无法加载此预览");
}
function mediaReady(event: Event) {
  resourceLoading.value = false;
  if (!autoPlayPending) return;
  autoPlayPending = false;
  const media = event.currentTarget as HTMLMediaElement | null;
  if (media?.paused) {
    try { void media.play()?.catch(() => undefined); } catch {}
  }
}
function icon(entry: ShareEntry) { return entry.folder ? "folder" : entry.category; }
function metadata(entry: ShareEntry) {
  return [kindLabel(entry), bytes(entry.size, ""), layout.value === "list" && entry.width && entry.height ? `${entry.width} × ${entry.height}` : ""].filter(Boolean).join(" · ");
}
let clickSnapshot: { id: string; time: number; picked: Map<string, { entry: ShareEntry; ancestors: string[] }> } | null = null;
function restoreClickSnapshot(entry: ShareEntry) {
  if (!clickSnapshot || clickSnapshot.id !== entry.id || Date.now() - clickSnapshot.time >= 600) return;
  s.picked.clear();
  for (const [id, pick] of clickSnapshot.picked) s.picked.set(id, pick);
}
function clickEntry(entry: ShareEntry, event: MouseEvent) {
  if (event.detail > 1) {
    restoreClickSnapshot(entry);
    return;
  }
  clickSnapshot = event.detail === 1 ? { id: entry.id, time: Date.now(), picked: new Map(s.picked) } : null;
  toggleShareEntry(entry);
}
function view(entry: ShareEntry) {
  if (s.loading) return;
  restoreClickSnapshot(entry);
  clickSnapshot = null;
  if (entry.folder) void browseShare([...s.trail, { id: entry.id, name: entry.name }]);
  else void previewShareEntry(entry);
}
function openSource() {
  if (!s.url) return;
  void Browser.OpenURL(s.url).catch(error => notify(t("无法打开链接：{error}", { error: error instanceof Error ? error.message : String(error) }), "error"));
}
function keydown(event: KeyboardEvent) {
  if (!s.open || appLocked.value || relocate.mode === "shareImport") return;
  if (event.key === "Escape") {
    event.stopPropagation();
    if (s.previewOpen) closeSharePreview();
    else closeShareImport();
  } else if (event.key === "Tab") {
    const scope = s.previewOpen ? previewCard.value : sheet.value;
    const elements = Array.from(scope?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), [role="combobox"]:not([aria-disabled="true"]), video[controls], audio[controls], iframe') ?? []);
    const first = elements[0], last = elements.at(-1);
    if (event.shiftKey && (document.activeElement === first || document.activeElement === scope)) { event.preventDefault(); last?.focus(); }
    else if (!event.shiftKey && (document.activeElement === last || document.activeElement === scope)) { event.preventDefault(); first?.focus(); }
  }
}
onBeforeUnmount(closeSharePreview);
</script>

<template>
  <Teleport to="body">
    <div v-if="s.open" class="scrim share-scrim" @click.self="closeShareImport" @keydown="keydown">
      <section ref="sheet" class="share-sheet glass glass-sheet" role="dialog" aria-modal="true" aria-labelledby="share-import-title" :aria-hidden="s.previewOpen || undefined" :inert="s.previewOpen || undefined" tabindex="-1">
        <header>
          <div><h2 id="share-import-title">{{ t("分享转存") }}</h2><p>{{ t("先预览分享内容，再选择要保存的文件") }}</p></div>
          <button class="icon close" :aria-label="t('关闭')" @click="closeShareImport"><Icon name="close" :size="18" /></button>
        </header>

        <form v-if="!s.url" class="link-form glass-well" @submit.prevent="inspectShareInput">
          <label for="share-link">{{ t("PikPak 分享链接") }}</label>
          <div><input id="share-link" v-model="s.input" autofocus type="text" spellcheck="false" placeholder="https://mypikpak.com/s/…" /><button class="primary" :disabled="s.loading || s.attempted">{{ t("读取分享") }}</button></div>
        </form>
        <div v-else class="source glass-well">
          <span class="source-icon"><Icon name="folder" :size="23" /></span>
          <div class="source-copy">
            <div class="source-title"><strong :title="s.info?.title">{{ s.info?.title || t("PikPak 分享") }}</strong><span v-if="s.info?.title" class="tag">{{ t("PikPak 分享") }}</span></div>
            <p><span v-if="s.info?.owner">{{ t("来自「{name}」的分享", { name: s.info.owner }) }} · </span><span v-if="ready">{{ t("{n} 个项目", { n: s.info?.fileCount ?? 0 }) }} · </span><span class="source-url">{{ s.url }}</span></p>
          </div>
          <button class="text source-open" @click="openSource">{{ t("浏览器打开") }}<Icon name="expand" :size="13" /></button>
        </div>

        <form v-if="shareNeedsCode()" class="gate glass-well" @submit.prevent="inspectShareInput">
          <Icon name="lock" :size="30" /><h3>{{ t("此分享需要提取码") }}</h3>
          <p v-if="s.info?.status === 'PASS_CODE_ERROR'" class="error" role="alert">{{ t("提取码不正确，请重试") }}</p>
          <div><input v-model="s.passCode" :aria-label="t('提取码')" :placeholder="t('提取码')" autocomplete="off" :disabled="s.loading" /><button class="primary" :disabled="s.loading || !s.passCode.trim()">{{ t("解锁分享") }}</button></div>
        </form>
        <div v-if="s.error" class="error-panel" role="alert"><Icon name="alert" :size="18" /><span>{{ s.error }}</span><button v-if="!s.attempted && !shareNeedsCode()" class="text" :disabled="s.loading" @click="inspectShareInput">{{ t("重试") }}</button></div>
        <div v-if="s.loading && !ready" class="loading" role="status"><Orbit :size="25" />{{ t("正在读取分享内容") }}</div>

        <div v-if="ready" class="files">
          <div class="toolbar">
            <strong>{{ t("文件预览") }} <span class="count">{{ s.entries.length }}</span></strong>
            <div class="preview-tools">
              <div class="bulk-select"><button class="text" :disabled="s.loading || s.attempted" @click="selectVisibleShare(true)">{{ t("全选") }}</button><span>·</span><button class="text muted" :disabled="s.loading || s.attempted" @click="selectVisibleShare(false)">{{ t("全不选") }}</button></div>
              <FileViewSwitch v-model="layout" :label="t('视图')" />
            </div>
          </div>
          <div ref="fileWell" class="file-well">
            <nav class="breadcrumbs" :aria-label="t('分享目录')">
              <Icon name="folder" :size="13" /><button :disabled="s.loading" @click="browseShare([])">{{ t("全部文件") }}</button>
              <template v-for="(crumb, index) in s.trail" :key="crumb.id"><Icon name="chevronRight" :size="11" /><button :disabled="s.loading" @click="browseShare(s.trail.slice(0, index + 1))">{{ crumb.name }}</button></template>
            </nav>
            <PickerItems :key="`${s.account}:${s.info?.shareId}:${s.trail.at(-1)?.id ?? ''}`" :layout="layout" class="file-scroll" :aria-busy="s.loading">
              <article v-for="entry in s.entries" :key="entry.id" class="file-row picker-item" :class="{ selected: shareSelected(entry), 'is-picked': shareSelected(entry) }" @click="clickEntry(entry, $event)" @dblclick="view(entry)">
                <button type="button" class="thumbnail picker-thumb" :class="{ folder: entry.folder, compact: !entry.thumbnail && !['video', 'image', 'audio'].includes(entry.category) }" :aria-label="entry.folder ? t('打开文件夹：{name}', { name: entry.name }) : t('预览文件：{name}', { name: entry.name })" :disabled="s.loading" @keydown.enter.prevent="view(entry)">
                  <Thumb :src="entry.thumbnail" :alt="entry.name" lazy><Icon :name="icon(entry)" :size="23" /></Thumb>
                  <span v-if="entry.category === 'video' && !entry.folder" class="play"><Icon name="play" :size="12" /></span>
                  <span v-if="entry.duration > 0 && !entry.folder" class="duration">{{ clock(entry.duration) }}</span>
                </button>
                <div class="file-copy picker-copy">
                  <div class="file-heading"><button type="button" class="filename" :title="entry.name" :disabled="s.loading" @click.stop="view(entry)" @dblclick.stop><span class="picker-name">{{ entry.name }}</span><Icon v-if="entry.folder" class="folder-chevron" name="chevronRight" :size="13" /></button><span v-if="entry.width >= 3840" class="tag">4K</span></div>
                  <p>{{ metadata(entry) }}</p>
                  <small v-if="shareCovered(entry)">{{ t("已包含在所选文件夹中") }}</small>
                </div>
                <input class="pick picker-check" type="checkbox" :checked="shareSelected(entry)" :aria-label="t('选择：{name}', { name: entry.name })" :disabled="s.loading || s.attempted" @click.stop @dblclick.stop @change="toggleShareEntry(entry)" />
              </article>
              <p v-if="!s.entries.length && !s.loading" class="empty picker-state">{{ t("此目录没有文件") }}</p>
              <p v-if="s.pageError" class="error page-error picker-state" role="alert">{{ s.pageError }}</p>
              <button v-if="s.nextPageToken || s.pageError" class="load-more text picker-state" :disabled="s.loading" @click="s.pageError ? retrySharePage() : browseShare(s.trail, true)"><Orbit v-if="s.loading" :size="16" />{{ s.pageError ? t("重试") : t("加载更多") }}</button>
              <div v-else-if="s.loading" class="loading picker-state"><Orbit :size="20" /></div>
            </PickerItems>
            <div class="selection-summary"><span>{{ t("已选 {n} 个项目", { n: s.picked.size }) }} <b v-if="selectedSize">· {{ bytes(selectedSize) }}</b></span><span class="hint"><Icon name="image" :size="13" />{{ t("单击选择 · 双击预览") }}</span></div>
          </div>
        </div>

        <div class="destination glass-well">
          <div class="destination-row">
            <Icon name="user" :size="19" /><span>{{ t("转存账号") }}</span>
            <SelectMenu v-slot="{ label }" id="share-account" class="account-select" :model-value="s.account" :options="accountOptions" :label="t('转存账号')" :disabled="s.attempted || appLocked || s.previewOpen || !accountOptions.length" @update:model-value="chooseShareAccount">
              <span>{{ label || t("请先添加转存账号") }}</span>
              <em v-if="receivingAccount?.label || receivingAccount?.nickname" class="account-login">{{ maskLogin(receivingAccount.login) }}</em>
              <Icon name="chevronDown" :size="14" />
            </SelectMenu>
          </div>
          <div class="destination-row"><Icon name="folder" :size="19" /><span>{{ t("保存到") }}</span><strong :title="destination">{{ destination }}</strong><button class="text" :disabled="!s.account || s.attempted" @click="pickShareDestination">{{ t("更改") }}</button></div>
        </div>

        <div v-if="s.submitting && !s.result" class="loading" role="status"><Orbit :size="22" />{{ t("正在提交分享转存") }}</div>
        <div v-if="s.result" class="receipt" :class="{ error: s.result.status === 'error' }" role="status"><Icon :name="s.result.status === 'complete' ? 'check' : s.result.status === 'error' ? 'alert' : 'clock'" :size="21" /><div><strong>{{ s.result.status === 'complete' ? t("分享已转存完成") : s.result.status === 'error' ? t("分享转存失败") : t("转存已提交，正在云端处理") }}</strong><p>{{ s.result.message }}</p><p v-if="s.result.status === 'pending' && !s.submitting">{{ t("尚未确认完成，可到目标文件夹查看。请勿重复提交。") }}</p></div></div>
        <footer><span class="security"><Icon name="lock" :size="14" />{{ t("仅在确认后转存") }}</span><div class="actions"><button class="secondary" @click="closeShareImport">{{ s.attempted ? t("关闭") : t("取消") }}</button><button v-if="s.attempted" class="primary" @click="openShareDestination"><Icon name="folder" :size="17" />{{ t("打开目标文件夹") }}</button><button v-else class="primary" :disabled="!ready || s.loading || !s.picked.size || !s.account" @click="confirmShareRestore"><Icon name="download" :size="18" />{{ t("转存 {n} 个项目", { n: s.picked.size }) }}</button></div></footer>
      </section>

      <div v-if="s.previewOpen" class="preview-layer" role="dialog" aria-modal="true" :aria-label="t('分享文件预览')" @click.self="closeSharePreview">
        <div ref="previewCard" class="preview-card glass glass-sheet">
          <header><strong>{{ s.preview?.name || t("分享文件预览") }}</strong><button class="icon" :aria-label="t('关闭预览')" @click="closeSharePreview"><Icon name="close" :size="18" /></button></header>
          <div v-if="s.previewLoading" class="loading preview-loading" role="status"><Orbit :size="30" />{{ t("正在加载预览") }}</div>
          <p v-else-if="s.previewError" class="error" role="alert">{{ s.previewError }}</p>
          <div v-else-if="s.preview" :key="s.preview.url" class="preview-content" :class="s.preview.kind" :aria-busy="resourceLoading">
            <img v-if="s.preview.kind === 'image'" :src="s.preview.url" :alt="s.preview.name" @load="resourceLoading = false" @error="previewFailed" />
            <video v-else-if="s.preview.kind === 'video'" :src="s.preview.url" autoplay controls playsinline preload="auto" @loadstart="resourceLoading = true" @waiting="resourceLoading = true" @seeking="resourceLoading = true" @loadeddata="mediaReady" @canplay="mediaReady" @playing="resourceLoading = false; autoPlayPending = false" @seeked="resourceLoading = false" @error="previewFailed" />
            <audio v-else-if="s.preview.kind === 'audio'" :src="s.preview.url" autoplay controls preload="auto" @loadstart="resourceLoading = true" @waiting="resourceLoading = true" @seeking="resourceLoading = true" @loadeddata="mediaReady" @canplay="mediaReady" @playing="resourceLoading = false; autoPlayPending = false" @seeked="resourceLoading = false" @error="previewFailed" />
            <iframe v-else-if="s.preview.kind === 'pdf'" :src="s.preview.url" :title="s.preview.name" @load="resourceLoading = false" @error="previewFailed" />
            <template v-else-if="s.preview.kind === 'text'"><pre>{{ s.preview.text?.text }}</pre><p v-if="s.preview.text?.truncated" class="muted">{{ t("仅显示部分文本") }}</p></template>
            <div v-if="resourceLoading" class="preview-wait" role="status"><Orbit :size="25" /><span>{{ t("正在加载预览") }}</span></div>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.share-scrim { padding: 22px; }
.share-sheet { position: relative; width: min(760px, 100%); max-height: calc(100vh - 44px); display: flex; flex-direction: column; gap: 15px; padding: 28px; border-radius: 28px; overflow: hidden; }
header { display: flex; align-items: flex-start; justify-content: space-between; gap: 18px; }
h2 { margin: 0; font-size: 26px; line-height: 1.2; letter-spacing: -.6px; }
header p { margin: 7px 0 3px; color: var(--fg-dim); font-size: 13px; }
.close { width: 40px; height: 40px; border-radius: 50%; border: 1px solid var(--line-strong); background: var(--glass-control); flex: none; }
.source { display: flex; align-items: center; gap: 12px; padding: 14px; border-radius: 17px; background: var(--canvas); border: 1px solid var(--line-strong); }
.source-icon { display: grid; place-items: center; color: var(--accent); background: var(--accent-soft); border: 1px solid var(--line-strong); width: 40px; height: 40px; border-radius: 12px; flex: none; }
.source-copy { flex: 1; min-width: 0; }
.source-title { display: flex; align-items: center; gap: 8px; }
.source-title strong { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tag { display: inline-block; flex: none; color: var(--accent); background: var(--accent-soft); border: 1px solid var(--line); border-radius: 4px; padding: 2px 5px; font-size: 10px; vertical-align: middle; }
.source p { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 11px; color: var(--fg-dim); margin: 5px 0 0; }
.source-url { opacity: .7; }
.text { min-height: 28px; border-radius: 6px; color: var(--accent); background: transparent; border: 0; padding: 3px 6px; font-size: 12px; }
.text:hover:not(:disabled) { background: var(--accent-soft); }
.source-open { display: flex; align-items: center; gap: 5px; flex: none; }
.files { flex: 1; min-height: 0; display: flex; flex-direction: column; }
.toolbar, .toolbar > div { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.toolbar { padding: 0 3px 8px; font-size: 14px; flex-wrap: wrap; }
.preview-tools { display: flex; align-items: center; gap: 10px; }
.bulk-select { display: flex; align-items: center; gap: 4px; }
.count { font-size: 11px; font-weight: 400; color: var(--fg-dim); background: var(--glass-inset); border-radius: 20px; padding: 2px 6px; margin-left: 5px; }
.file-well { flex: 1; min-height: 0; display: flex; flex-direction: column; border: 1px solid var(--line-strong); border-radius: 17px; overflow: hidden; background: var(--canvas); }
.breadcrumbs { flex: none; display: flex; align-items: center; gap: 5px; padding: 6px 14px; color: var(--fg-dim); font-size: 11px; line-height: 1; background: var(--glass-inset); border-bottom: 1px solid var(--line); overflow-x: auto; scrollbar-width: none; }
.breadcrumbs button { display: inline-flex; align-items: center; min-height: 24px; padding: 0 2px; border: 0; border-radius: 4px; background: transparent; color: inherit; font-size: inherit; line-height: 1; white-space: nowrap; }
.file-scroll { flex: 1; max-height: 365px; min-height: 0; overflow-y: auto; scrollbar-width: none; }
.file-scroll::-webkit-scrollbar, .breadcrumbs::-webkit-scrollbar, .preview-card::-webkit-scrollbar { display: none; }
.file-row { cursor: pointer; user-select: none; }
.thumbnail { padding: 0; }
.file-scroll:not(.grid) .thumbnail { width: 96px; height: 54px; }
.file-scroll:not(.grid) .thumbnail.compact, .file-scroll:not(.grid) .thumbnail.folder { width: 36px; height: 36px; }
.thumbnail:hover:not(:disabled) { background: var(--glass-inset); border-color: var(--accent); }
.play { position: absolute; inset: 0; margin: auto; width: 24px; height: 24px; border-radius: 50%; background: #0009; color: white; display: grid; place-items: center; pointer-events: none; }
.duration { position: absolute; bottom: 3px; right: 3px; padding: 0 3px; border-radius: 3px; background: #000b; color: white; font: 9px ui-monospace, monospace; }
/* Text actions must not inherit the global pill button's hover lens or height. */
.file-heading { display: flex; align-items: center; gap: 6px; min-width: 0; }
.filename { display: inline-flex; align-items: center; gap: 3px; min-width: 0; max-width: 100%; min-height: 20px; padding: 0; border: 0; border-radius: 4px; background: transparent; font: inherit; font-size: 13px; font-weight: 500; line-height: 18px; color: var(--fg); text-align: left; }
.filename:hover:not(:disabled), .breadcrumbs button:hover:not(:disabled) { background: transparent; color: var(--accent); }
.filename:hover:not(:disabled) > span, .breadcrumbs button:hover:not(:disabled) { text-decoration: underline; text-underline-offset: 3px; }
.filename:hover:not(:disabled) .folder-chevron { color: var(--accent); }
.filename:active:not(:disabled), .breadcrumbs button:active:not(:disabled) { transform: none; }
.folder-chevron { flex: none; color: var(--fg-dim); }
.file-copy p { margin: 4px 0 0; color: var(--fg-dim); font-size: 11px; line-height: 1.5; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
.file-copy small { font-size: 10px; color: var(--accent); }
.grid .file-heading { align-items: flex-start; min-height: 36px; }
.grid .filename { align-items: flex-start; min-height: 0; font-size: 12px; line-height: 18px; }
.grid .folder-chevron { height: 18px; }
.grid .file-copy p { margin-top: 2px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.grid .file-copy small { display: none; }
.selection-summary { flex: none; display: flex; justify-content: space-between; align-items: center; gap: 12px; padding: 10px 16px; font-size: 11px; border-top: 1px solid var(--line); background: var(--glass-inset); }
.selection-summary b { color: var(--accent); margin-left: 6px; }
.hint { display: flex; gap: 5px; align-items: center; color: var(--fg-dim); font-size: 10px; }
.destination { padding: 8px 15px; border-radius: 17px; background: var(--canvas); border: 1px solid var(--line-strong); }
.destination-row { display: flex; align-items: center; gap: 10px; min-height: 40px; font-size: 12px; }
.destination-row + .destination-row { border-top: 1px solid var(--line); }
.destination-row > svg { color: var(--accent); flex: none; }
.destination-row > span { min-width: 52px; color: var(--fg-dim); white-space: nowrap; }
.account-select { display: flex; align-items: center; gap: 8px; flex: 1; min-width: 0; min-height: 32px; font-weight: 600; padding: 4px 10px; background: var(--glass-inset); border: 1px solid var(--line); border-radius: 9px; font-size: 12px; }
.account-select > span { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.account-select > svg { flex: none; color: var(--fg-dim); }
.account-select:hover:not([aria-disabled="true"]) { border-color: var(--accent); }
.account-select[aria-disabled="true"] { opacity: .5; }
.pick:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.account-login { flex: none; max-width: 42%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--fg-dim); font-style: normal; font-weight: 400; font-size: 11px; }
.destination-row strong { flex: 1; min-width: 0; padding-left: 11px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; }
footer { flex: none; display: flex; align-items: center; justify-content: space-between; gap: 12px; padding-top: 2px; }
.security { display: flex; align-items: center; gap: 7px; font-size: 11px; color: var(--fg-dim); }
.security svg { color: var(--accent); }
.actions { display: flex; gap: 11px; }
.actions button { min-height: 44px; padding: 0 26px; border-radius: 999px; font-size: 13px; font-weight: 600; display: flex; align-items: center; justify-content: center; gap: 8px; }
.secondary { background: var(--glass-control); border: 1px solid var(--line-strong); color: var(--fg); }
.loading { display: flex; align-items: center; justify-content: center; gap: 12px; padding: 22px; color: var(--fg-dim); font-size: 12px; }
.error { color: var(--danger); }
.error-panel { display: flex; align-items: center; gap: 10px; padding: 12px 14px; border: 1px solid var(--line-strong); border-radius: 12px; background: var(--canvas); color: var(--danger); font-size: 12px; line-height: 1.5; }
.error-panel > svg { flex: none; }
.error-panel > span { flex: 1; overflow-wrap: anywhere; }
.gate { padding: 24px; display: flex; flex-direction: column; align-items: center; gap: 10px; border-radius: 16px; }
.gate h3, .gate p { margin: 0; }
.gate h3 { font-size: 15px; }
.gate > div, .link-form > div { display: flex; gap: 10px; }
.gate input, .link-form input { min-width: 0; flex: 1; }
.gate button, .link-form button { padding: 8px 16px; border-radius: 10px; }
.link-form { padding: 16px; border-radius: 16px; }
.link-form label { display: block; margin-bottom: 8px; font-size: 12px; }
.empty { text-align: center; color: var(--fg-dim); margin: 30px 10px; font-size: 12px; }
.page-error { padding: 0 16px; font-size: 12px; }
.load-more { display: flex; align-items: center; justify-content: center; gap: 8px; width: 100%; padding: 13px; }
.muted { color: var(--fg-dim); }
.receipt { display: flex; align-items: flex-start; gap: 12px; padding: 14px; border: 1px solid var(--line-strong); border-radius: 14px; background: var(--canvas); color: var(--accent); font-size: 12px; line-height: 1.5; }
.receipt.error { color: var(--danger); }
.receipt svg { flex: none; }
.receipt p { margin: 4px 0 0; color: var(--fg-dim); overflow-wrap: anywhere; }
.preview-layer { position: fixed; inset: 0; z-index: 2; background: var(--scrim); backdrop-filter: blur(12px); display: flex; align-items: center; justify-content: center; padding: 22px; overflow: hidden; overscroll-behavior: contain; }
.preview-card { padding: 18px; width: min(720px, 100%); max-height: 100%; overflow: auto; scrollbar-width: none; overscroll-behavior: contain; border-radius: 18px; }
.preview-card header { align-items: center; margin-bottom: 15px; font-size: 13px; }
.preview-card header .icon { flex: none; width: 34px; height: 34px; padding: 0; border-radius: 50%; }
.preview-card header strong { overflow-wrap: anywhere; }
.preview-loading { min-height: 180px; }
.preview-content { position: relative; }
.preview-content.image { min-height: 160px; display: grid; place-items: center; }
.preview-content.audio { padding-top: 68px; }
.preview-content.video video { aspect-ratio: 16 / 9; }
.preview-wait { position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); display: flex; align-items: center; gap: 10px; padding: 10px 16px; border: 1px solid var(--line-strong); border-radius: 999px; background: var(--glass-panel); color: var(--fg); font-size: 12px; white-space: nowrap; pointer-events: none; }
.preview-content.audio .preview-wait { top: 28px; }
.preview-card img, .preview-card video { display: block; width: 100%; max-height: 55vh; object-fit: contain; background: #000; border-radius: 8px; }
.preview-card audio { display: block; width: 100%; }
.preview-card iframe { width: 100%; height: 55vh; border: 0; background: white; }
.preview-card pre { white-space: pre-wrap; overflow-wrap: anywhere; user-select: text; font-size: 12px; }
@media (max-height: 760px) { .share-scrim { padding: 12px; } .share-sheet { max-height: calc(100vh - 24px); padding: 18px 22px; gap: 10px; } h2 { font-size: 22px; } header p { margin: 3px 0 0; } .source { padding: 10px 12px; } .toolbar { padding-bottom: 5px; } .destination { padding: 4px 13px; } .destination-row { min-height: 34px; } .actions button { min-height: 40px; } }
@media (max-width: 600px) { .share-scrim { padding: 10px; } .share-sheet { padding: 18px; max-height: calc(100vh - 20px); gap: 10px; } .hint, .source-title .tag, .security, .account-login { display: none; } .source { flex-wrap: wrap; } .source-open { margin-left: auto; } footer { justify-content: flex-end; } .actions { width: 100%; } .actions button { flex: 1; padding: 0 14px; } .file-row { padding: 10px; gap: 10px; } }
</style>
