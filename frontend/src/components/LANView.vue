<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { Clipboard, Dialogs } from "@wailsio/runtime";
import qrcode from "qrcode-generator";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import SelectMenu from "./SelectMenu.vue";
import Toggle from "./Toggle.vue";
import LANPreparationPicker from "./LANPreparationPicker.vue";
import FileViewSwitch from "./FileViewSwitch.vue";
import PickerItems from "./PickerItems.vue";
import { inSelectMenu } from "../dom";
import * as DriveService from "../../bindings/github.com/tonyflux/pkp/internal/services/driveservice";
import type { FolderChoice } from "../../bindings/github.com/tonyflux/pkp/internal/services/models";
import { accounts, accountLabel, appLocked, notify, openNav, trailPath } from "../store";
import { bytes } from "../format";
import { i18n, t } from "../i18n";
import { lan, LANService, changeLAN, refreshLAN, refreshLANTasks, loadLANInterfaces, lanError, lanTaskState, type LANSpace } from "../lanstore";
import { loadSync, sync, syncStateName, type SyncPair } from "../syncstore";

const selected = ref("");
const preparingSpace = ref("");
const preparationTrigger = ref<HTMLElement>();
watch(selected, () => { preparingSpace.value = ""; });
watch(() => { const item = lan.status?.spaces.find(value => value.id === preparingSpace.value); return [preparingSpace.value, item?.enabled, item?.available]; }, ([id, enabled, available]) => { if (id && (!enabled || !available)) preparingSpace.value = ""; }, { flush: "sync" });
const space = computed(() => lan.status?.spaces.find(item => item.id === selected.value));
const tab = ref("settings");
const network = ref("");
const modal = ref<"" | "start" | "stop" | "password" | "create" | "remove" | "disable" | "prepare" | "cache" | "cancel" | "resume">("");
const dialog = ref<HTMLElement>();
const password = ref("");
const repeat = ref("");
const showPassword = ref(false);
const acknowledged = ref(false);
const needsAcknowledgement = ref(true);
const modalError = ref("");
const notice = ref("");
const taskID = ref("");
const targetID = ref("");
const draft = reactive({ name: "", kind: "local", accountId: "", rootId: "", localPath: "", allowPrepare: false });
const nameEdited = ref(false);
const folders = ref<{ id: string; name: string }[]>([]);
const trail = ref<{ id: string; name: string }[]>([]);
const picking = ref(false);
const cloudChosen = ref(false);
const chosenTrail = ref<{ id: string; name: string }[]>([]);
const cloudPickerOpen = ref(false);
const folderLoading = ref(false);
const folderReady = ref(false);
const folderError = ref("");
const folderList = ref<HTMLElement>();
const folderLayout = ref<"grid" | "list">("grid");
const folderQuery = ref("");
const folderMatches = ref<FolderChoice[]>([]);
const folderSearching = ref(false);
const folderSearchReady = ref(true);
const folderSearchError = ref("");
const folderSearchActive = computed(() => !!folderQuery.value.trim());
let folderSearchGeneration = 0;
let folderSearchTimer: ReturnType<typeof setTimeout> | undefined;
let folderBrowseScroll = 0;
const cloudPath = computed(() => [t("lan|云盘根目录"), ...chosenTrail.value.map(item => item.name)].join(" / "));
const currentFolder = computed(() => trail.value.at(-1)?.name ?? t("lan|云盘根目录"));
let folderGeneration = 0;
let opener: HTMLElement | null = null;
let timer: ReturnType<typeof setTimeout> | undefined;
let progressTimer: ReturnType<typeof setTimeout> | undefined;
let copyGeneration = 0;
let interfacesReady = false;
let disposed = false;
watch([() => lan.status?.running, () => lan.status?.address, () => lan.error], () => { copyGeneration++; }, { flush: "sync" });
const tasks = computed(() => lan.status?.tasks.filter(item => !selected.value || item.spaceId === selected.value) ?? []);
const taskHeading = ref<HTMLElement>();
const isFinished = (state: string) => ["ready", "failed", "cancelled"].includes(state);
const taskPercent = (done: number, total: number) => done >= total ? 100 : Math.floor(Math.max(0, done / total) * 100);
const hasFinishedTasks = computed(() => tasks.value.some(item => isFinished(item.state)));
watch(tasks, async current => {
  if (lan.busy) return;
  const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const row = active?.closest<HTMLElement>("[data-lan-task]");
  if (!row || current.some(item => item.id === row.dataset.lanTask)) return;
  const neighbours = [row.nextElementSibling, row.previousElementSibling];
  await nextTick();
  if (document.activeElement !== document.body && document.activeElement !== active) return;
  const next = neighbours.find(element => element?.isConnected)?.querySelector<HTMLElement>("button:not(:disabled)");
  (next ?? taskHeading.value)?.focus();
});
async function dismissHistory(id = "") {
  if (lan.busy || !hasFinishedTasks.value) return;
  const scope = selected.value;
  const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const row = active?.closest<HTMLElement>("[data-lan-task]");
  const neighbours = row ? [row.nextElementSibling, row.previousElementSibling] : [];
  const ok = await changeLAN(() => LANService.DismissTaskHistory(scope, id));
  if (!ok) return;
  await nextTick();
  if (active && (!active.isConnected || (active instanceof HTMLButtonElement && active.disabled)) && (document.activeElement === document.body || document.activeElement === active)) {
    const next = neighbours.find(element => element?.isConnected)?.querySelector<HTMLElement>("button:not(:disabled)");
    (next ?? taskHeading.value)?.focus();
  }
}
const networkOptions = computed(() => {
  const options = [{ value: "", label: t("lan|请选择网卡") }, ...lan.interfaces.map(item => ({ value: item.id, label: `${item.name} · ${item.address}` }))];
  const activeID = lan.status?.running ? lan.status.interfaceId : "";
  if (activeID && !lan.interfaces.some(item => item.id === activeID)) options.push({ value: activeID, label: activeID.replace("@", " · ") });
  return options;
});
const sourceOptions = computed(() => [{ value: "local", label: t("lan|本地目录") }, { value: "cloud", label: t("lan|云盘目录") }]);
const accountOptions = computed(() => [{ value: "", label: t("lan|请选择账号") }, ...accounts.list.map(account => ({ value: account.id, label: `${accountLabel(account)} · ${account.login}`, disabled: account.needsPassword }))]);
const spaceOptions = computed(() => [{ value: "", label: t("lan|全部空间") }, ...(lan.status?.spaces ?? []).map(item => ({ value: item.id, label: item.name }))]);
const syncTrail = ref<{ id: string; name: string }[]>([]);
const syncTrailReady = ref(false);
const syncLoading = ref(false);
const syncError = ref("");
let syncGeneration = 0;
const sourceAccountReady = computed(() => {
  const account = accounts.list.find(item => item.id === space.value?.accountId);
  return !!account && !account.needsPassword;
});
const coveringSyncs = computed(() => {
  const item = space.value;
  if (item?.kind !== "cloud") return [];
  const roots = new Set([item.rootId, ...(syncTrailReady.value ? ["", ...syncTrail.value.map(crumb => crumb.id)] : [])]);
  return sync.pairs.filter(pair => pair.account === item.accountId && (pair.mode === "download" || pair.mode === "bidirectional") && roots.has(pair.cloudId));
});
const canPrepareFiles = computed(() => !!space.value?.enabled && !!space.value.available && !lan.busy && !lan.error);
watch([() => space.value?.id, () => space.value?.kind, () => space.value?.accountId, () => space.value?.rootId, sourceAccountReady], () => { void loadSpaceSync(); }, { immediate: true, flush: "sync" });
async function loadSpaceSync() {
  const generation = ++syncGeneration, item = space.value;
  syncTrail.value = []; syncTrailReady.value = false; syncLoading.value = false; syncError.value = "";
  if (item?.kind !== "cloud" || !sourceAccountReady.value) return;
  syncLoading.value = true;
  try {
    const actual = await DriveService.SyncFolderTrail(item.accountId, item.rootId);
    if (generation !== syncGeneration) return;
    syncTrail.value = actual ?? []; syncTrailReady.value = true;
  } catch (error) {
    if (generation === syncGeneration) syncError.value = lanError(error);
  } finally {
    if (generation === syncGeneration) syncLoading.value = false;
  }
}
function manageSync(pair: SyncPair) {
  sync.scope = "all"; sync.query = ""; sync.selected = pair.id; sync.detail = true; sync.tab = "settings";
  openNav("sync");
}
const qr = computed(() => {
  const address = lan.status?.running && !lan.error ? lan.status.address : "";
  if (!address) return null;
  const url = new URL(address); url.searchParams.set("lang", i18n.locale);
  const code = qrcode(0, "M"); code.addData(url.toString()); code.make();
  const size = code.getModuleCount();
  let path = "";
  for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) if (code.isDark(y, x)) path += `M${x + 4} ${y + 4}h1v1h-1z`;
  return { size: size + 8, path };
});
const canStart = computed(() => !!lan.status?.hasPassword && lan.interfaces.some(item => item.id === network.value) && !!lan.status?.spaces.some(item => item.enabled && item.available));
const canCreate = computed(() => draft.name.trim() && (draft.kind === "local" ? draft.localPath : draft.accountId && cloudChosen.value && !cloudPickerOpen.value) && !picking.value);
const canSubmit = computed(() => !lan.busy && (!needsAcknowledgement.value || acknowledged.value) && (modal.value === "create" ? canCreate.value : modal.value === "password" ? !!password.value && password.value === repeat.value : modal.value === "start" ? canStart.value : !!modal.value));
const title = computed(() => ({ start: t("lan|开始共享"), stop: t("lan|停止共享"), password: lan.status?.hasPassword ? t("lan|重置共享密码") : t("lan|设置共享密码"), create: t("lan|新建共享空间"), remove: t("lan|移除空间"), disable: lan.status?.spaces.find(item => item.id === targetID.value)?.enabled ? t("lan|停用空间") : t("lan|启用空间"), prepare: t("lan|准备权限"), cache: t("lan|清理兼容副本"), cancel: t("lan|取消准备"), resume: t("lan|恢复准备") }[modal.value || "start"]));
watch(() => lan.status, status => {
  if (!status) return;
  if ((selected.value || tab.value !== "tasks") && !status.spaces.some(item => item.id === selected.value)) selected.value = status.spaces[0]?.id ?? "";
  if (status.running) network.value = status.interfaceId;
  else if (interfacesReady && network.value && !lan.interfaces.some(item => item.id === network.value)) network.value = lan.interfaces.length === 1 ? lan.interfaces[0].id : "";
});
function open(kind: Exclude<typeof modal.value, "">, id = "") {
  if (lan.busy || picking.value) return;
  opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  targetID.value = id || selected.value; acknowledged.value = false; modalError.value = ""; password.value = ""; repeat.value = "";
  needsAcknowledgement.value = (kind !== "password" || !!lan.status?.hasPassword) && localStorage.getItem(`pkp.lan.confirmed.${kind}`) !== "1";
  if (!needsAcknowledgement.value && kind !== "create" && kind !== "password") { void perform(kind); return; }
  modal.value = kind;
  showPassword.value = false;
  if (kind === "create") { Object.assign(draft, { name: "", kind: "local", accountId: "", rootId: "", localPath: "", allowPrepare: false }); nameEdited.value = false; accountChanged(); }
  void nextTick(() => dialog.value?.querySelector<HTMLElement>("input,button,[role=combobox]")?.focus());
}
function close() { if (lan.busy || picking.value) return; folderGeneration++; resetFolderSearch(); folderLoading.value = false; modal.value = ""; password.value = ""; repeat.value = ""; opener?.focus(); }
function dialogFocusables() {
  return Array.from(dialog.value?.querySelectorAll<HTMLElement>("button:not(:disabled), input:not(:disabled), summary, [role=combobox]:not([aria-disabled=true])") ?? []).filter(item => item.offsetParent !== null);
}
function dialogKey(event: KeyboardEvent) {
  if (!modal.value || appLocked.value || accounts.dialog || inSelectMenu(event)) return;
  if (event.key === "Escape") { event.preventDefault(); event.stopImmediatePropagation(); close(); }
  else if (event.key === "Tab") {
    const items = dialogFocusables(), first = items[0], last = items.at(-1);
    if (!first) { event.preventDefault(); dialog.value?.focus(); }
    else if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog.value)) { event.preventDefault(); last?.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }
}
function dialogFocus(event: FocusEvent) {
  if (!modal.value || appLocked.value || accounts.dialog || (event.target as HTMLElement)?.closest("[data-select-list]")) return;
  if (dialog.value && !dialog.value.contains(event.target as Node)) (dialogFocusables()[0] ?? dialog.value).focus({ preventScroll: true });
}
async function networks() {
  try {
    await loadLANInterfaces();
    interfacesReady = true;
    if (!lan.status?.running && !lan.interfaces.some(item => item.id === network.value)) network.value = lan.interfaces.length === 1 ? lan.interfaces[0].id : "";
  } catch (error) { lan.error = lanError(error); }
}
async function poll() { await refreshLAN(); if (!disposed) timer = setTimeout(poll, 2000); }
async function pollProgress() {
  if (disposed) return;
  if (tab.value !== "tasks" || !tasks.value.some(item => item.state === "downloading" || item.state === "converting")) return;
  await refreshLANTasks();
  clearTimeout(progressTimer);
  if (!disposed && tab.value === "tasks" && tasks.value.some(item => item.state === "downloading" || item.state === "converting")) progressTimer = setTimeout(pollProgress, 500);
}
watch([tab, () => tasks.value.some(item => item.state === "downloading" || item.state === "converting")], ([current, active]) => {
  clearTimeout(progressTimer);
  if (current === "tasks" && active) progressTimer = setTimeout(pollProgress, 500);
});
onMounted(() => { void networks(); void poll(); window.addEventListener("keydown", dialogKey, true); document.addEventListener("focusin", dialogFocus); });
onBeforeUnmount(() => { disposed = true; syncGeneration++; folderGeneration++; resetFolderSearch(); clearTimeout(timer); clearTimeout(progressTimer); window.removeEventListener("keydown", dialogKey, true); document.removeEventListener("focusin", dialogFocus); });
async function localRoot() {
  picking.value = true; modalError.value = "";
  try { const path = await Dialogs.OpenFile({ Title: t("lan|选择本地授权目录"), CanChooseFiles: false, CanChooseDirectories: true, CanCreateDirectories: false }); if (path && path !== draft.localPath) { draft.localPath = path; acknowledged.value = false; if (!nameEdited.value || !draft.name.trim()) { draft.name = path.replace(/[\\/]+$/, "").split(/[\\/]/).at(-1) || path; nameEdited.value = false; } } }
  catch (error) { modalError.value = lanError(error); }
  finally { picking.value = false; }
}
async function storageRoot(kind: "original" | "copies") {
  if (lan.busy || picking.value || !lan.status || lan.status.storageBusy) return;
  picking.value = true; lan.error = "";
  try {
    const path = await Dialogs.OpenFile({ Title: kind === "original" ? t("lan|选择准备原件保存目录") : t("lan|选择兼容与无损副本保存目录"), CanChooseFiles: false, CanChooseDirectories: true, CanCreateDirectories: false, Directory: (kind === "original" ? lan.status.originalDir : lan.status.copyDir) || undefined });
    if (path) await changeLAN(() => LANService.SetStorageDirectory(kind, path));
  } catch (error) { lan.error = lanError(error); }
  finally { picking.value = false; }
}
function resetFolderSearch() {
  folderSearchGeneration++; clearTimeout(folderSearchTimer);
  folderQuery.value = ""; folderMatches.value = []; folderSearching.value = false;
  folderSearchReady.value = true; folderSearchError.value = "";
}
function searchFolders(query: string) {
  const wasSearching = folderSearchActive.value;
  if (!wasSearching && query.trim()) folderBrowseScroll = folderList.value?.scrollTop ?? 0;
  resetFolderSearch();
  folderQuery.value = query;
  if (!query.trim()) {
    if (wasSearching) void nextTick(() => { if (folderList.value) folderList.value.scrollTop = folderBrowseScroll; });
    return;
  }
  if (!draft.accountId || draft.kind !== "cloud" || !cloudPickerOpen.value) return;
  void nextTick(() => { if (folderList.value) folderList.value.scrollTop = 0; });
  const generation = folderSearchGeneration, account = draft.accountId;
  folderSearching.value = true;
  folderSearchTimer = setTimeout(async () => {
    try {
      const result = await DriveService.SearchFolders(account, query.trim());
      if (generation !== folderSearchGeneration || account !== draft.accountId) return;
      folderSearchReady.value = result.ready; folderMatches.value = result.folders ?? [];
    } catch (error) {
      if (generation === folderSearchGeneration) folderSearchError.value = lanError(error);
    } finally {
      if (generation === folderSearchGeneration) folderSearching.value = false;
    }
  }, 200);
}
async function browse(next: { id: string; name: string }[]) {
  if (!draft.accountId || draft.kind !== "cloud") return;
  resetFolderSearch(); folderBrowseScroll = 0;
  const generation = ++folderGeneration;
  const active = document.activeElement;
  const moveFocus = active instanceof HTMLElement && !!active.closest("[data-lan-cloud-picker], [data-lan-change-cloud]");
  const revealPicker = moveFocus || (active instanceof HTMLElement && active.getAttribute("aria-label") === t("lan|固定来源账号"));
  trail.value = [...next]; folders.value = [];
  if (folderList.value) folderList.value.scrollTop = 0;
  folderLoading.value = true; folderReady.value = false; folderError.value = "";
  try {
    const result = await LANService.CloudFolders(draft.accountId, next.at(-1)?.id ?? "");
    if (generation !== folderGeneration) return;
    folders.value = result ?? []; folderReady.value = true;
  } catch (error) {
    if (generation === folderGeneration) folderError.value = lanError(error);
  } finally {
    if (generation === folderGeneration) {
      folderLoading.value = false;
      void nextTick(() => {
        const crumbs = dialog.value?.querySelector<HTMLElement>(".folder-crumbs");
        if (crumbs) crumbs.scrollLeft = crumbs.scrollWidth;
        if (generation !== folderGeneration || (document.activeElement !== active && document.activeElement !== document.body)) return;
        if (moveFocus && !folderError.value) (folderList.value?.querySelector<HTMLElement>("[data-lan-folder]") ?? dialog.value?.querySelector<HTMLElement>("[data-lan-choose-cloud]"))?.focus({ preventScroll: true });
        if (revealPicker) (folderError.value ? folderList.value : dialog.value?.querySelector<HTMLElement>(".folder-toolbar"))?.scrollIntoView({ block: "nearest" });
      });
    }
  }
}
function chooseCloud() {
  if (folderSearchActive.value || !folderReady.value || folderLoading.value || folderError.value) return;
  draft.rootId = trail.value.at(-1)?.id ?? ""; chosenTrail.value = [...trail.value];
  cloudChosen.value = true; cloudPickerOpen.value = false; acknowledged.value = false;
  if (!nameEdited.value || !draft.name.trim()) { draft.name = currentFolder.value; nameEdited.value = false; }
  void nextTick(() => {
    const change = dialog.value?.querySelector<HTMLElement>("[data-lan-change-cloud]");
    change?.closest("[data-lan-chosen-directory]")?.scrollIntoView({ block: "nearest" });
    change?.focus({ preventScroll: true });
  });
}
function editCloud() { cloudPickerOpen.value = true; void browse(chosenTrail.value); }
function cancelCloud() {
  folderGeneration++; resetFolderSearch(); folderLoading.value = false; cloudPickerOpen.value = false;
  void nextTick(() => dialog.value?.querySelector<HTMLElement>("[data-lan-change-cloud]")?.focus({ preventScroll: true }));
}
function accountChanged() {
  folderGeneration++; resetFolderSearch(); draft.rootId = ""; folders.value = []; trail.value = []; chosenTrail.value = [];
  cloudChosen.value = false; folderReady.value = false; folderLoading.value = false; folderError.value = ""; acknowledged.value = false;
  cloudPickerOpen.value = draft.kind === "cloud" && !!draft.accountId;
  if (cloudPickerOpen.value) void browse([]);
}
function sourceName(item: LANSpace) { return item.kind === "local" ? t("lan|本地目录") : accountLabel(accounts.list.find(account => account.id === item.accountId) ?? null) || item.accountId; }
async function copy() {
  const address = lan.status?.address;
  if (!address) return;
  const generation = ++copyGeneration;
  try {
    await Clipboard.SetText(address);
    if (!disposed && generation === copyGeneration) notify(t("lan|地址已复制"), "success");
  } catch {
    if (!disposed && generation === copyGeneration) notify(t("复制失败，请重试或选中文字手动复制"), "error");
  }
}
async function submit() {
  if (!canSubmit.value || !modal.value) return;
  await perform(modal.value);
}
async function perform(kind: Exclude<typeof modal.value, "">) {
  const target = lan.status?.spaces.find(item => item.id === targetID.value);
  const ok = await changeLAN(async () => {
    switch (kind) {
      case "start": await LANService.Start({ interfaceId: network.value, port: 17891 }); break;
      case "stop": await LANService.Stop(); break;
      case "password": await LANService.SetPassword(password.value); break;
      case "create": { const item = await LANService.AddSpace({ ...draft, name: draft.name.trim(), accountId: draft.kind === "cloud" ? draft.accountId : "", rootId: draft.kind === "cloud" ? draft.rootId : "", localPath: draft.kind === "local" ? draft.localPath : "" }); selected.value = item.id; break; }
      case "remove": await LANService.RemoveSpace(targetID.value); break;
      case "disable": if (target) await LANService.UpdateSpace({ id: target.id, name: target.name, enabled: !target.enabled, allowPrepare: target.allowPrepare }); break;
      case "prepare": if (target) await LANService.UpdateSpace({ id: target.id, name: target.name, enabled: target.enabled, allowPrepare: !target.allowPrepare }); break;
      case "cache": { const result = await LANService.ClearCache(); notice.value = t("lan|已清理 {size}，跳过 {n} 个正在使用的副本", { size: bytes(result.removedBytes), n: result.skipped }); break; }
      case "cancel": await LANService.CancelTask(taskID.value); break;
      case "resume": await LANService.ResumeTask(taskID.value); break;
    }
  });
  if (ok) {
    if (needsAcknowledgement.value) localStorage.setItem(`pkp.lan.confirmed.${kind}`, "1");
    close();
  } else modalError.value = lan.error;
}
const rename = ref("");
watch(() => [space.value?.id, space.value?.name], ([id, name], previous) => { if (id !== previous?.[0] || name !== previous?.[1]) rename.value = name ?? ""; });
async function saveName() { const item = space.value; if (!item || !rename.value.trim()) return; await changeLAN(() => LANService.UpdateSpace({ id: item.id, name: rename.value.trim(), enabled: item.enabled, allowPrepare: item.allowPrepare })); }
</script>

<template>
  <section class="lan-page" data-lan-view>
    <header class="page-heading"><div><p class="eyebrow">{{ t('lan|这台电脑 · 只读共享') }}</p><h1>{{ t('局域网共享') }}</h1><p>{{ t('lan|电脑作中转，只分享你明确选中的目录。') }}</p></div><button class="primary" data-lan-create :disabled="lan.busy || !lan.status" @click="open('create')"><Icon name="plus" :size="16" />{{ t('lan|新建共享空间') }}</button></header>
    <p v-if="lan.error || lan.status?.error" class="error" role="alert">{{ lan.error || lan.status?.error }} <button @click="refreshLAN">{{ t('lan|刷新状态') }}</button></p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <section class="service glass glass-sheet" data-lan-service>
      <div class="service-main"><div class="status-line"><span class="dot" :class="{ live: lan.status?.running && !lan.error }"></span><h2>{{ lan.error ? t('lan|连接状态待确认') : lan.status?.running ? t('lan|正在共享') : t('lan|共享已关闭') }}</h2></div><p>{{ t('lan|普通 HTTP 不加密，局域网内可观察网络的人可能看到共享密码和文件内容。请仅在可信网络使用。') }}</p>
        <div class="network"><span>{{ t('lan|监听网卡') }}</span><span class="select glass-well"><SelectMenu id="lan-network" v-model="network" class="face" :options="networkOptions" :label="t('lan|监听网卡')" :disabled="lan.status?.running || lan.busy"/><Icon name="chevronDown" :size="16"/></span><button :disabled="lan.busy" @click="networks">{{ t('lan|刷新网卡') }}</button></div>
        <p v-if="lan.status?.running">{{ t('lan|更换网卡前请停止共享；旧连接将中断。') }}</p>
        <div v-if="lan.status?.running && !lan.error" class="address"><code data-lan-address>{{ lan.status.address }}</code><button data-lan-copy @click="copy">{{ t('lan|复制地址') }}</button></div>
        <div class="actions"><button class="primary" data-lan-start-stop :disabled="lan.busy || !lan.status || (!lan.status.running && !canStart)" @click="open(lan.status?.running ? 'stop' : 'start')">{{ lan.status?.running ? t('lan|停止共享') : t('lan|开始共享') }}</button><button data-lan-password :disabled="lan.busy || !lan.status" @click="open('password')">{{ lan.status?.hasPassword ? t('lan|重置共享密码') : t('lan|设置共享密码') }}</button></div>
        <p v-if="!lan.status?.running">{{ t('lan|开启前：选择网卡、设置密码，并启用至少一个可用空间。') }}</p>
        <section class="sleep-prevention" data-lan-sleep-prevention>
          <div class="sleep-prevention-heading"><strong>{{ t('lan|共享期间防止电脑自动休眠') }}</strong><Toggle :model-value="!!lan.status?.preventSleep" :label="t('lan|共享期间防止电脑自动休眠')" :disabled="lan.busy || !lan.status || !lan.status.preventSleepSupported" aria-describedby="lan-sleep-description lan-sleep-limits" data-lan-prevent-sleep @update:model-value="changeLAN(() => LANService.SetPreventSleep($event))" /></div>
          <p v-if="lan.status?.preventSleep || lan.status?.preventSleepActive" class="sleep-prevention-state" role="status" data-lan-prevent-sleep-status>{{ lan.status.preventSleepActive ? t('lan|自动休眠保护已生效') : lan.status.running ? t('lan|自动休眠保护未生效') : t('lan|已开启，将在下次开始共享时生效') }}</p>
          <p v-if="lan.status && !lan.status.preventSleepSupported" role="status">{{ t('lan|此系统或构建不支持防止自动休眠。') }}</p>
          <p id="lan-sleep-description">{{ t('lan|默认关闭。开启后仅在共享服务运行时阻止系统因空闲自动休眠，屏幕仍可熄灭；关闭此选项、停止共享或退出 PKP 会释放保护。') }}</p>
          <p id="lan-sleep-limits">{{ t('lan|不能阻止手动休眠、合盖、关机、系统强制休眠或网络中断。建议连接电源；开启会增加耗电。') }}</p>
        </section>
      </div><div class="qr-panel"><svg v-if="qr" data-lan-qr :viewBox="`0 0 ${qr.size} ${qr.size}`" role="img" :aria-label="t('lan|访问地址二维码')" shape-rendering="crispEdges"><rect width="100%" height="100%" fill="white"/><path :d="qr.path" fill="black"/></svg><div v-else class="qr-placeholder" aria-hidden="true"><Icon name="qrCode" :size="54"/></div><strong>{{ t('lan|扫码访问') }}</strong><small>{{ qr ? t('lan|二维码不含密码') : t('lan|开启后显示二维码') }}</small></div>
    </section>
    <div class="content-grid"><section class="spaces"><h2>{{ t('lan|共享空间') }} <span class="space-count tnum">{{ lan.status?.spaces.length ?? 0 }}</span></h2><p v-if="!lan.status?.spaces.length" class="empty">{{ t('lan|还没有共享空间，添加目录不会自动开始共享。') }}</p>
      <div v-if="lan.status?.spaces.length" class="space-list">
        <button v-for="item in lan.status?.spaces" :key="item.id" class="space-card glass glass-sheet" :class="{ selected: selected === item.id }" :aria-pressed="selected === item.id" :data-lan-space="item.id" @click="selected = item.id"><Icon :name="item.kind === 'local' ? 'folder' : 'cloud'" :size="24"/><span><strong :title="item.name">{{ item.name }}</strong><small :title="sourceName(item)">{{ sourceName(item) }}</small><small class="root" :title="item.localPath || item.rootId || t('lan|云盘根目录')">{{ item.localPath || item.rootId || t('lan|云盘根目录') }}</small><span class="chips"><span>{{ !item.enabled ? t('lan|已停用') : !item.available ? t('lan|来源暂不可用') : t('lan|已启用') }}</span><span>{{ item.allowPrepare ? t('lan|允许准备文件') : t('lan|仅下载已就绪文件') }}</span></span></span></button>
      </div>
      <p class="read-note"><Icon name="lock" :size="18"/>{{ t('lan|网页只读，不允许上传、删除、移动或修改设置。') }}</p>
    </section><section class="detail glass glass-sheet" data-lan-detail><div class="tabs"><button :aria-pressed="tab === 'settings'" @click="tab = 'settings'">{{ t('lan|空间设置') }}</button><button :aria-pressed="tab === 'tasks'" @click="tab = 'tasks'">{{ t('lan|准备任务') }}</button><button :aria-pressed="tab === 'cache'" @click="tab = 'cache'">{{ t('lan|存储与副本') }}</button></div>
      <template v-if="tab === 'settings' && space">
        <div class="settings-grid">
        <section class="space-identity">
          <h2 :title="space.name">{{ space.name }}</h2><p>{{ sourceName(space) }}</p><p class="root">{{ space.localPath || space.rootId || t('lan|云盘根目录') }}</p><p>{{ t('lan|来源账号和根目录固定，切换桌面账号不会改变此空间。更换来源请新建空间。') }}</p><p v-if="space.error" class="error" role="alert">{{ space.error }}</p><label class="field">{{ t('lan|空间名称') }}<input v-model="rename" maxlength="120" /></label><button :disabled="lan.busy || !rename.trim() || rename.trim() === space.name" @click="saveName">{{ t('lan|保存名称') }}</button>
        </section>
        <section class="space-preparation">
        <div class="setting"><div><strong>{{ t('lan|允许准备文件') }}</strong><p>{{ t('lan|访客可让这台电脑下载云文件并生成兼容副本，会占用网络、磁盘和算力。本地空间仅生成兼容副本。') }}</p></div><button role="switch" :aria-checked="space.allowPrepare" :disabled="lan.busy" data-lan-prepare @click="open('prepare')">{{ space.allowPrepare ? t('lan|已允许') : t('lan|未允许') }}</button></div>
        <section v-if="space.kind === 'cloud'" data-lan-sync>
          <hr/><h3>{{ t('lan|准备文件') }}</h3>
          <p>{{ t('lan|已完成且核对一致的同步原件会自动复用；访客仍按需准备文件，不能创建或控制同步关系。兼容副本单独管理。') }}</p>
          <p>{{ t('lan|电脑可直接选择文件准备，无需创建同步关系。') }}</p>
          <p v-if="!sourceAccountReady" role="status">{{ t('lan|来源账号不可用，请先在账号管理中恢复登录。') }}</p>
          <p v-if="syncLoading" role="status">{{ t('正在加载') }}</p>
          <p v-if="syncError" class="error" role="alert">{{ syncError }} <button @click="loadSpaceSync">{{ t('lan|刷新状态') }}</button></p>
          <p v-if="sync.error" class="error" role="alert">{{ sync.error }} <button @click="loadSync">{{ t('刷新同步状态') }}</button></p>
          <p v-if="!sync.loading && !sync.error && !syncLoading && syncTrailReady && !coveringSyncs.length">{{ t('lan|尚无覆盖此云盘目录的同步关系。') }}</p>
          <article v-for="pair in coveringSyncs" :key="pair.id" class="task" :data-lan-sync-pair="pair.id">
            <strong>{{ pair.name }}</strong><small class="root">{{ pair.localPath }}</small><span>{{ syncStateName(pair) }}</span>
            <button @click="manageSync(pair)">{{ t('lan|查看与管理同步') }}</button>
          </article>
        </section>
        <div class="desktop-preparation"><p v-if="space.kind !== 'cloud'">{{ t('lan|电脑可直接选择文件准备，无需创建同步关系。') }}</p><button ref="preparationTrigger" data-lan-choose-preparation :disabled="!canPrepareFiles" @click="preparingSpace = space.id">{{ t('lan|选择文件准备') }}</button></div>
        </section>
        </div>
        <div class="actions space-actions"><button :disabled="lan.busy" @click="open('disable')">{{ space.enabled ? t('lan|停用空间') : t('lan|启用空间') }}</button><button class="danger" :disabled="lan.busy" @click="open('remove')">{{ t('lan|移除空间') }}</button></div>
      </template>
      <p v-else-if="tab === 'settings'" class="empty">{{ t('lan|选择一个空间查看授权范围。') }}</p>
      <template v-if="tab === 'tasks'">
        <div class="actions task-heading"><h2 ref="taskHeading" tabindex="-1">{{ t('lan|准备任务') }}</h2><button data-lan-dismiss-all :disabled="lan.busy || !hasFinishedTasks" @click="dismissHistory()">{{ t('lan|清空已结束记录') }}</button></div>
        <label class="field">{{ t('lan|任务范围') }}<span class="select glass-well"><SelectMenu v-model="selected" class="face" :options="spaceOptions" :label="t('lan|任务范围')" :disabled="lan.busy"/><Icon name="chevronDown" :size="16"/></span></label>
        <p>{{ t('lan|停止共享不自动取消电脑准备；恢复等待任务需重新确认授权。') }}</p>
        <p>{{ t('lan|清理仅隐藏当前任务范围内电脑端的已结束记录，不影响访客、源文件或缓存。等待任务需先取消才能清理。') }}</p>
        <p v-if="!tasks.length" class="empty">{{ t('lan|暂无准备任务') }}</p>
        <div v-else class="task-list" role="region" :aria-label="t('lan|准备任务')">
          <article v-for="item in tasks" :key="item.id" class="task task-row" :data-lan-task="item.id">
            <div class="task-main">
              <strong class="task-name" :title="item.name">{{ item.name }}</strong>
              <div class="task-meta"><small class="task-space" :title="lan.status?.spaces.find(value => value.id === item.spaceId)?.name">{{ lan.status?.spaces.find(value => value.id === item.spaceId)?.name }}</small><span>{{ lanTaskState(item.state) }}</span><small v-if="item.total > 0">{{ bytes(item.done) }} / {{ bytes(item.total) }}</small><strong v-if="item.total > 0" class="task-percent">{{ taskPercent(item.done, item.total) }}%</strong></div>
              <p v-if="item.error" class="error">{{ item.error }}</p>
            </div>
            <div class="actions task-actions"><button v-if="item.state === 'held'" :disabled="lan.busy" @click="taskID = item.id; open('resume')">{{ t('lan|恢复准备') }}</button><button v-if="!isFinished(item.state)" :disabled="lan.busy" @click="taskID = item.id; open('cancel')">{{ t('lan|取消准备') }}</button><button v-if="isFinished(item.state)" data-lan-dismiss :disabled="lan.busy" @click="dismissHistory(item.id)">{{ t('lan|删除记录') }}</button></div>
            <div v-if="item.total > 0" :key="`${item.id}:${item.state}`" class="task-progress" role="progressbar" :aria-label="lanTaskState(item.state)" aria-valuemin="0" :aria-valuemax="item.total" :aria-valuenow="Math.max(0, Math.min(item.done, item.total))"><span class="task-fill" :class="{ moving: item.state === 'downloading' || item.state === 'converting' }" :style="{ transform: `scaleX(${Math.max(0, Math.min(1, item.done / item.total))})` }" /></div>
          </article>
        </div>
      </template>
      <template v-if="tab === 'cache'">
        <h2>{{ t('lan|存储与副本') }}</h2>
        <p>{{ t('lan|新任务使用所选目录；已有原件和副本留在原处继续复用，不会移动或删除。等待任务恢复时仍使用原保存位置。') }}</p>
        <section class="storage-location"><h3>{{ t('lan|准备原件保存目录') }}</h3><p class="root" data-lan-original-directory>{{ lan.status?.originalDir }}</p><button data-lan-original-picker :disabled="lan.busy || picking || !lan.status || lan.status.storageBusy" @click="storageRoot('original')">{{ t('lan|选择准备原件保存目录') }}</button></section>
        <section class="storage-location"><h3>{{ t('lan|兼容与无损副本保存目录') }}</h3><p class="root" data-lan-copy-directory>{{ lan.status?.copyDir }}</p><button data-lan-copy-picker :disabled="lan.busy || picking || !lan.status || lan.status.storageBusy" @click="storageRoot('copies')">{{ t('lan|选择兼容与无损副本保存目录') }}</button></section>
        <p v-if="lan.status?.storageBusy" role="status">{{ t('lan|请等待正在准备的任务结束或取消后，再更改保存位置。') }}</p>
        <p>{{ t('lan|所选目录必须已存在且可写；磁盘断开或目录被替换时会暂停准备，不会自动创建或改用其他位置。') }}</p>
        <hr/><section class="cache-summary">
          <div><h3>{{ t('lan|兼容副本') }}</h3>
            <p v-if="!lan.status || lan.error" role="status">{{ !lan.status && lan.loading ? t('正在加载') : t('lan|状态待确认') }}</p>
            <p v-else-if="lan.status.cacheBytes === 0" role="status">{{ t('lan|暂无兼容副本') }}</p>
            <p v-else role="status">{{ t('lan|已占用 {size}', { size: bytes(lan.status.cacheBytes) }) }}</p>
          </div>
          <button data-lan-cache :disabled="lan.busy || !lan.status || !!lan.error || lan.status.cacheBytes <= 0" @click="open('cache')">{{ t('lan|清理兼容副本') }}</button>
        </section>
        <p>{{ t('lan|仅清理本功能生成的兼容副本，正在使用的副本会跳过。不会删除源原件、已下载原件或其他下载结果。') }}</p>
      </template>
      <p v-if="tab === 'tasks' && lan.status?.originalDir" class="root">{{ t('lan|新准备原件保存到 {path}；已有文件留在原处。', { path: lan.status.originalDir }) }}</p>
    </section></div><footer>{{ t('lan|锁定应用、关窗驻留后继续共享；退出 PKP 或主动停止会断开访客连接。') }}</footer>
    <LANPreparationPicker v-if="preparingSpace && space?.id === preparingSpace" :key="preparingSpace" :space-id="preparingSpace" :space-name="space.name" :opener="preparationTrigger" @close="preparingSpace = ''" @tasks="preparingSpace = ''; tab = 'tasks'; refreshLANTasks()" />
    <Teleport to="body"><div v-if="modal" class="scrim lan-scrim"><section ref="dialog" class="lan-dialog" :class="{ 'create-dialog': modal === 'create' }" role="dialog" aria-modal="true" aria-labelledby="lan-dialog-title" tabindex="-1" @keydown.stop><form @submit.prevent="submit"><header><h2 id="lan-dialog-title">{{ title }}</h2><button type="button" :disabled="lan.busy || picking" :aria-label="t('lan|关闭对话框')" @click="close">×</button></header>
      <div class="dialog-body">
      <template v-if="modal === 'create'">
        <label class="field">{{ t('lan|空间名称') }}<input v-model="draft.name" maxlength="120" required @input="nameEdited = true" /></label>
        <label class="field">{{ t('lan|来源类型') }}<span class="select glass-well"><SelectMenu v-model="draft.kind" class="face" :options="sourceOptions" :label="t('lan|来源类型')" :disabled="picking || lan.busy" @update:model-value="accountChanged"/><Icon name="chevronDown" :size="16"/></span></label>
        <div v-if="draft.kind === 'local'" class="folder-field" role="group" aria-labelledby="lan-folder-title">
          <h3 id="lan-folder-title" class="field-title">{{ t('lan|共享目录') }}</h3>
          <div class="folder-selection" :class="{ chosen: !!draft.localPath }">
            <span class="folder-mark"><Icon name="folder" :size="22"/></span>
            <div class="folder-copy"><strong :title="draft.localPath ? draft.localPath.replace(/[\\/]+$/, '').split(/[\\/]/).at(-1) || draft.localPath : undefined">{{ draft.localPath ? draft.localPath.replace(/[\\/]+$/, '').split(/[\\/]/).at(-1) || draft.localPath : t('尚未选择文件夹') }}</strong><details v-if="draft.localPath" class="folder-path"><summary :title="draft.localPath"><span>{{ draft.localPath }}</span></summary></details></div>
            <button type="button" :disabled="picking || lan.busy" data-lan-local-picker @click="localRoot">{{ draft.localPath ? t('lan|更改目录') : t('lan|选择本地授权目录') }}</button>
          </div>
        </div>
        <template v-else>
          <label class="field">{{ t('lan|固定来源账号') }}<span class="select glass-well"><SelectMenu v-model="draft.accountId" class="face" :options="accountOptions" :label="t('lan|固定来源账号')" :disabled="lan.busy" @update:model-value="accountChanged"/><Icon name="chevronDown" :size="16"/></span></label>
          <div class="folder-field" role="group" aria-labelledby="lan-folder-title">
            <h3 id="lan-folder-title" class="field-title">{{ t('lan|共享目录') }}</h3>
            <p v-if="!draft.accountId" class="folder-placeholder"><Icon name="folder" :size="22"/>{{ t('lan|先选择来源账号，再选择共享目录。') }}</p>
            <div v-else-if="cloudChosen && !cloudPickerOpen" class="folder-selection chosen" data-lan-chosen-directory>
              <span class="folder-mark"><Icon name="folder" :size="22"/></span>
              <div class="folder-copy"><small class="folder-choice-status"><Icon name="check" :size="14"/>{{ t('lan|已选择此目录') }}</small><strong :title="chosenTrail.at(-1)?.name ?? t('lan|云盘根目录')">{{ chosenTrail.at(-1)?.name ?? t('lan|云盘根目录') }}</strong><details v-if="chosenTrail.length" class="folder-path"><summary :title="cloudPath"><span>{{ cloudPath }}</span></summary></details><p v-else class="folder-root-warning">{{ t('lan|选择云盘根目录将共享此账号下的全部文件，请确认范围。') }}</p></div>
              <button type="button" :disabled="lan.busy" data-lan-change-cloud @click="editCloud">{{ t('lan|更改目录') }}</button>
            </div>
            <div v-else class="folder-picker" data-lan-cloud-picker>
              <p class="folder-picker-hint">{{ t('lan|进入文件夹浏览，确认后共享该目录及其子目录。') }}</p>
              <label class="folder-search"><Icon name="search" :size="17"/><input type="search" :value="folderQuery" :placeholder="t('lan|搜索此账号的文件夹')" :aria-label="t('lan|搜索此账号的文件夹')" autocomplete="off" spellcheck="false" data-lan-folder-search @input="searchFolders(($event.target as HTMLInputElement).value)" @keydown.enter.prevent/><button v-if="folderQuery" type="button" class="quiet" :aria-label="t('清除搜索')" @click="searchFolders('')"><Icon name="close" :size="16"/></button></label>
              <div class="folder-toolbar">
                <template v-if="!folderSearchActive">
                  <button type="button" class="quiet folder-back" :disabled="!trail.length || folderLoading" :aria-label="t('lan|返回上级目录')" :title="t('lan|返回上级目录')" @click="browse(trail.slice(0, -1))"><Icon name="arrowLeft" :size="18"/></button>
                  <nav class="folder-crumbs" :aria-label="t('lan|授权根目录')">
                    <button v-if="trail.length" type="button" class="quiet" :disabled="folderLoading" @click="browse([])">{{ t('lan|云盘根目录') }}</button><span v-else aria-current="location">{{ t('lan|云盘根目录') }}</span>
                    <template v-for="(crumb, index) in trail" :key="crumb.id"><Icon name="chevronRight" :size="13"/><span v-if="index === trail.length - 1" aria-current="location" :title="crumb.name">{{ crumb.name }}</span><button v-else type="button" class="quiet" :disabled="folderLoading" :title="crumb.name" @click="browse(trail.slice(0, index + 1))">{{ crumb.name }}</button></template>
                  </nav>
                </template>
                <p v-else class="folder-search-scope">{{ t('搜索整个云盘的文件夹') }}</p>
                <FileViewSwitch v-model="folderLayout" :disabled="lan.busy" />
              </div>
              <div ref="folderList" class="folder-list" :aria-busy="folderSearchActive ? folderSearching : folderLoading">
                <PickerItems :layout="folderLayout">
                <template v-if="folderSearchActive">
                  <p v-if="folderSearching" class="folder-state picker-state" role="status"><Orbit :size="20"/>{{ t('正在搜索…') }}</p>
                  <div v-else-if="folderSearchError" class="folder-state picker-state"><p class="error" role="alert">{{ folderSearchError }}</p><button type="button" @click="searchFolders(folderQuery)">{{ t('重试') }}</button></div>
                  <p v-else-if="!folderSearchReady" class="folder-state picker-state" role="status">{{ t('云盘索引还在建立，暂时不能搜索全盘；可以逐层浏览。') }}</p>
                  <p v-else-if="!folderMatches.length" class="folder-state picker-state" role="status">{{ t('没有找到文件夹，试试其他名称') }}</p>
                  <template v-else>
                    <button v-for="folder in folderMatches" :key="folder.id" type="button" class="quiet picker-item" :data-lan-folder-result="folder.id" :title="trailPath(folder.trail ?? [])" @click="browse(folder.trail ?? [])"><span class="picker-thumb"><Icon name="folder" :size="20" filled/></span><span class="picker-copy"><strong class="picker-name">{{ folder.name }}</strong><small class="picker-meta">{{ trailPath(folder.trail ?? []) }}</small></span><Icon class="picker-chevron" name="chevronRight" :size="16"/></button>
                    <p v-if="folderMatches.length >= 100" class="folder-search-limit picker-state">{{ t('lan|最多显示 100 个结果，请缩小搜索范围。') }}</p>
                  </template>
                </template>
                <template v-else>
                  <p v-if="folderLoading" class="folder-state picker-state" role="status"><Orbit :size="20"/>{{ t('lan|读取目录中') }}</p>
                  <div v-else-if="folderError" class="folder-state picker-state"><Icon name="alert" :size="22"/><p class="error" role="alert">{{ folderError }}</p><button type="button" @click="browse(trail)">{{ t('重试') }}</button></div>
                  <p v-else-if="folderReady && !folders.length" class="folder-state picker-state">{{ t('这里没有子文件夹，可以选择这里。') }}</p>
                  <button v-for="folder in folders" v-else :key="folder.id" type="button" class="quiet picker-item" :data-lan-folder="folder.id" :title="folder.name" @click="browse([...trail, folder])"><span class="picker-thumb"><Icon name="folder" :size="20" filled/></span><span class="picker-copy"><span class="picker-name">{{ folder.name }}</span></span><Icon class="picker-chevron" name="chevronRight" :size="16"/></button>
                </template>
                </PickerItems>
              </div>
              <div v-if="!folderSearchActive" class="folder-current"><span>{{ t('lan|当前目录') }}</span><strong :title="currentFolder">{{ currentFolder }}</strong></div>
              <p v-if="!folderSearchActive && folderReady && !trail.length" class="folder-root-warning"><Icon name="info" :size="16"/>{{ t('lan|选择云盘根目录将共享此账号下的全部文件，请确认范围。') }}</p>
              <div class="folder-actions"><button v-if="cloudChosen" type="button" @click="cancelCloud">{{ t('取消') }}</button><button type="button" class="primary" :disabled="folderSearchActive || folderLoading || !folderReady || !!folderError" data-lan-choose-cloud @click="chooseCloud">{{ t('选择这个文件夹') }}</button></div>
            </div>
          </div>
        </template>
        <label class="check"><input v-model="draft.allowPrepare" type="checkbox"/>{{ t('lan|允许准备文件') }}</label>
        <p>{{ t('lan|访客可让这台电脑下载云文件并生成兼容副本，会占用网络、磁盘和算力。本地空间仅生成兼容副本。') }}</p>
        <p>{{ t('lan|持有共享密码的人均可读取此目录，包括其中的隐藏文件。请勿授权含敏感内容的目录。') }}</p>
      </template>
      <template v-else-if="modal === 'password'"><p>{{ t('lan|密码独立于应用锁和云盘登录，两次输入一致即可。密码不会显示在二维码中。') }}</p><label class="field">{{ t('lan|共享密码') }}<input v-model="password" :type="showPassword ? 'text' : 'password'" autocomplete="new-password" required /></label><label class="field">{{ t('lan|再次输入密码') }}<input v-model="repeat" :type="showPassword ? 'text' : 'password'" autocomplete="new-password" required /></label><button type="button" :aria-pressed="showPassword" @click="showPassword = !showPassword">{{ showPassword ? t('隐藏密码') : t('显示密码') }}</button><p v-if="lan.status?.hasPassword">{{ t('lan|所有访客将退出，正在下载和播放会中断；已取得的文件无法收回。电脑准备不自动取消。') }}</p></template>
      <p v-else-if="modal === 'start'">{{ t('lan|普通 HTTP 不加密，局域网内可观察网络的人可能看到共享密码和文件内容。请仅在可信网络使用。') }}</p>
      <p v-else-if="modal === 'stop'">{{ t('lan|所有访客会退出，下载到设备和播放会中断。已启动的电脑准备不自动取消，原件和完成结果会保留；若需结束准备，请到任务中取消。') }}</p>
      <p v-else-if="modal === 'cache'">{{ t('lan|仅清理本功能生成的兼容副本，正在使用的副本会跳过。不会删除源原件、已下载原件或其他下载结果。') }}</p>
      <template v-else-if="modal === 'prepare'"><p>{{ t('lan|访客可让这台电脑下载云文件并生成兼容副本，会占用网络、磁盘和算力。本地空间仅生成兼容副本。') }}</p><p>{{ t('lan|关闭访客准备权限会阻止访客发起新准备；仅由访客发起的任务等待处理，电脑主动准备仍会继续。已完成文件仍可按当前授权读取。') }}</p></template>
      <p v-else-if="modal === 'cancel'">{{ t('lan|取消此电脑准备？所有等待此任务的访客都会受到影响。保留原件，未完成副本不会发布。') }}</p>
      <p v-else-if="modal === 'resume'">{{ t('lan|恢复会重新核验来源和准备权限，并继续占用网络、磁盘和算力。') }}</p>
      <p v-else>{{ t('lan|停用或移除会中断此空间的访问和传输，准备任务等待处理；不删除源文件和完成结果。启用后持有密码的访客可读取。') }}</p>
      <p v-if="modal === 'create' && draft.kind === 'local'">{{ t('lan|所选目录中的隐藏文件也可能被共享。请选择专用目录；符号链接和目录联接不会提供给访客。') }}</p>
      <label v-if="needsAcknowledgement" class="check"><input v-model="acknowledged" type="checkbox" data-lan-ack/>{{ t('lan|我已了解授权范围和影响，同类操作不再提示') }}</label><p v-if="modalError" class="error" role="alert">{{ modalError }}</p>
      </div><footer><button type="button" :disabled="lan.busy || picking" @click="close">{{ t('lan|暂不操作') }}</button><button class="primary" data-lan-confirm :disabled="!canSubmit">{{ lan.busy ? t('lan|正在处理') : title }}</button></footer>
    </form></section></div></Teleport>
  </section>
</template>

<style scoped>
.lan-scrim { background: #15242d66; }
.lan-scrim .lan-dialog { background: var(--glass-panel); backdrop-filter: blur(24px); }
.select { display: flex; align-items: center; position: relative; min-width: 0; width: 100%; border-radius: 10px; }
.select .face { width: 100%; min-width: 0; padding: 10px 36px 10px 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.select > .icon { position: absolute; right: 12px; pointer-events: none; }
.network .select { flex: 1; min-width: 180px; width: auto; }
.space-card small { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.space-card > span { flex: 1; grid-template-columns: minmax(0, 1fr); }
.space-card strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.lan-page {
  flex: 1;
  width: 100%;
  min-width: 0;
  min-height: 0;
  padding: 24px;
  overflow: auto;
  scrollbar-width: none;
  color: var(--fg);
  background: var(--canvas);
  border: 1px solid var(--line);
  container-type: inline-size;
  border-radius: var(--radius-island);
}
.lan-page::-webkit-scrollbar { display: none; }
.page-heading,.status-line,.actions,.network,.setting,.lan-dialog header,.lan-dialog footer { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.task-heading h2 { margin: 0; }
.page-heading { justify-content: space-between; margin-bottom: 24px; } h1 { font-size: 30px; letter-spacing: -.8px; margin: 5px 0 8px; } h2 { font-size: 18px; margin: 0 0 16px; } p { color: var(--fg-dim); line-height: 1.65; } .eyebrow { font-size: 13px; font-weight: 500; letter-spacing: .02em; margin: 0; } button { border-radius: 11px; padding: 9px 13px; cursor: pointer; } button:disabled { opacity: .45; cursor: default; } button:focus-visible,input:focus-visible,[role=combobox]:focus-visible { outline: 2px solid var(--accent, #3b9a91); outline-offset: 3px; } .primary { background: var(--primary); color: var(--on-primary); display: inline-flex; align-items: center; gap: 7px; } .service { display: flex; gap: 28px; padding: 24px; border-radius: 22px; margin-bottom: 26px; } .service-main { flex: 1; min-width: 0; } .status-line h2 { margin: 0; } .dot { width: 9px; height: 9px; border-radius: 50%; background: var(--fg-dim); } .dot.live { background: #41a48a; box-shadow: 0 0 0 5px #41a48a18; } input:not([type=checkbox]) { width: 100%; min-width: 0; border: 1px solid var(--stroke, #8884); background: var(--glass-fill, #8881); color: inherit; padding: 10px; border-radius: 10px; } .address { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; margin: 16px 0; } code { font-size: 17px; overflow-wrap: anywhere; } .qr-panel { width: 155px; flex: 0 0 155px; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 10px; text-align: center; } .qr-panel svg { width: 145px; height: 145px; border-radius: 12px; } small { font-size: 13px; color: var(--fg-dim); line-height: 1.5; } .content-grid { display: grid; grid-template-columns: minmax(260px, 1fr) minmax(330px, 1.15fr); gap: 22px; align-items: start; } .spaces h2 { display: flex; justify-content: space-between; } .space-card { display: flex; width: 100%; gap: 16px; text-align: left; padding: 20px; border-radius: 18px; margin: 12px 0; border: 1px solid transparent; color: inherit; } .space-card.selected { border-color: var(--accent, #378d83); background: #60ab9514; } .space-card > span { display: grid; gap: 7px; min-width: 0; } strong,.root,.task { overflow-wrap: anywhere; } .chips { display: flex; flex-wrap: wrap; gap: 8px; font-size: 13px; } .chips > span { padding: 4px 7px; background: #8881; border-radius: 7px; } .detail { padding: 22px; border-radius: 22px; min-width: 0; } .tabs { display: flex; gap: 5px; flex-wrap: wrap; padding-bottom: 20px; } .tabs button[aria-pressed=true] { background: #60ab9525; } .field { display: grid; gap: 8px; margin: 16px 0; } .setting { align-items: flex-start; } .setting > div { flex: 1; min-width: 160px; } hr { border: 0; border-top: 1px solid #8883; margin: 22px 0; } .danger,.error { color: var(--danger, #b95555); } .empty { padding: 24px 0; } .read-note { display: flex; align-items: flex-start; gap: 10px; font-size: 14px; } .task { display: grid; gap: 8px; padding: 18px 0; border-bottom: 1px solid #8883; } progress { width: 100%; accent-color: var(--accent, #378d83); } .lan-page > footer { font-size: 13px; line-height: 1.65; color: var(--fg-dim); border-top: 1px solid var(--line); margin-top: 24px; padding-top: 16px; } .lan-dialog { width: min(560px, calc(100vw - 48px)); max-height: calc(100dvh - 64px); overflow: hidden; border: 1px solid var(--stroke, #8885); border-radius: 24px; padding: 26px; color: var(--fg); background: var(--surface, #f2f3f2); box-shadow: 0 24px 90px #0005; } .lan-dialog header { justify-content: space-between; } .lan-dialog h2 { margin: 0; flex: 1; } .lan-dialog footer { justify-content: flex-end; padding-top: 20px; } .check { display: flex; gap: 10px; align-items: flex-start; line-height: 1.6; margin-top: 18px; } .check input { margin-top: 5px; flex: none; }
.task-list { max-height: clamp(240px, 48vh, 480px); overflow-y: auto; overscroll-behavior: contain; padding: 4px; margin: 0 -4px; }
.task.task-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; gap: 8px 14px; padding: 12px 4px; }
.task-main { min-width: 0; display: grid; grid-template-columns: minmax(0, 1fr); gap: 6px; }
.task-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.task-meta { display: flex; align-items: baseline; flex-wrap: wrap; gap: 5px 14px; color: var(--fg-dim); font-size: 13px; }
.task-space { max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.task-meta > span { color: var(--fg); }
.task-percent { color: var(--fg); font-variant-numeric: tabular-nums; }
.task-main .error { margin: 0; font-size: 13px; overflow-wrap: anywhere; }
.task-actions { flex: none; max-width: 100%; gap: 8px; }
.task-actions button { padding: 7px 11px; font-size: 13px; }
.task-progress { grid-column: 1 / -1; height: 7px; overflow: hidden; border-radius: 99px; background: color-mix(in srgb, var(--accent) 20%, transparent); }
.task-fill { display: block; width: 100%; height: 100%; border-radius: inherit; background: var(--accent); transform-origin: left center; }
.task-fill.moving { transition: transform 500ms linear; }
@container (max-width: 500px) { .task.task-row { grid-template-columns: minmax(0, 1fr); } .task-progress { grid-column: 1; } }
.qr-panel small { width: 100%; overflow-wrap: anywhere; text-wrap: balance; }
.qr-placeholder { display: grid; place-items: center; width: 145px; height: 145px; border: 1px solid var(--line); border-radius: 16px; background: var(--glass-inset); color: var(--fg-dim); }
.qr-panel .qr-placeholder .icon { width: 54px; height: 54px; opacity: .7; }
@media (max-width: 1100px) { .qr-placeholder { width: 120px; height: 120px; } }
.spaces h2 { justify-content: flex-start; align-items: center; gap: 8px; }
.space-count { min-width: 22px; padding: 1px 7px; border-radius: 999px; background: color-mix(in srgb, var(--fg) 9%, transparent); color: var(--fg-dim); font-size: 12px; font-weight: 600; line-height: 1.5; text-align: center; }
.service .actions { margin-top: 16px; }
.sleep-prevention { margin-top: 18px; padding-top: 16px; border-top: 1px solid var(--line); }
.sleep-prevention-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.sleep-prevention-heading > strong { min-width: 0; }
.sleep-prevention > p { margin: 8px 0 0; font-size: 13px; }
.sleep-prevention > .sleep-prevention-state { color: var(--fg); font-weight: 500; }
.read-note .icon { flex: none; margin-top: 2px; }
.content-grid { grid-template-columns: minmax(220px, .7fr) minmax(0, 2.3fr); }
.spaces { min-width: 0; }
.detail { min-width: 0; container-type: inline-size; }
.settings-grid { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 24px; align-items: start; }
.space-identity,.space-preparation { min-width: 0; }
.space-identity > h2,.space-identity > p,.space-preparation > p { overflow-wrap: anywhere; }
.space-identity > h2 { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 3; overflow: hidden; }
.space-preparation { border-left: 1px solid var(--line); padding-left: 24px; }
.desktop-preparation { margin-top: 16px; }
.storage-location { margin: 18px 0; padding: 14px; border: 1px solid var(--line); border-radius: 14px; min-width: 0; }
.storage-location h3 { margin: 0; font-size: 15px; }
.storage-location .root { overflow-wrap: anywhere; user-select: text; }
.cache-summary { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px 20px; }
.cache-summary > div { min-width: 0; }
.cache-summary h3 { margin: 0; font-size: 15px; }
.cache-summary p { margin: 5px 0 0; font-size: 14px; }
.cache-summary button { max-width: 100%; }
.space-actions { border-top: 1px solid var(--line); margin-top: 22px; padding-top: 18px; }
@container (max-width: 610px) {
  .settings-grid { grid-template-columns: 1fr; gap: 20px; }
  .space-preparation { border-left: 0; border-top: 1px solid var(--line); padding: 20px 0 0; }
}
.lan-dialog.create-dialog { width: min(640px, calc(100vw - 48px)); }
.lan-dialog form { display: flex; flex-direction: column; max-height: calc(100dvh - 116px); }
.lan-dialog header { flex: none; padding-bottom: 4px; }
.dialog-body { min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 0 4px 2px; margin: 0 -4px; }
.lan-dialog footer { flex: none; border-top: 1px solid var(--line); margin-top: 18px; padding-top: 16px; }
.folder-field { margin: 18px 0; min-width: 0; }
.field-title { font-size: 15px; font-weight: 500; margin: 0 0 10px; }
.folder-selection { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; gap: 12px; align-items: center; padding: 14px; border: 1px solid var(--line); border-radius: 14px; background: var(--canvas); }
.folder-selection.chosen { border-color: color-mix(in srgb, var(--accent) 35%, var(--line)); }
.folder-mark { display: grid; place-items: center; width: 40px; height: 40px; border-radius: 11px; color: var(--accent); background: var(--accent-soft); }
.folder-copy { display: grid; gap: 4px; min-width: 0; }
.folder-copy strong { font-weight: 600; overflow-wrap: anywhere; display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; }
.folder-copy .folder-path { margin: 0; font-size: 13px; line-height: 1.6; overflow-wrap: anywhere; }
.folder-path > summary { display: flex; align-items: flex-start; gap: 6px; cursor: pointer; list-style: none; }
.folder-path > summary::-webkit-details-marker { display: none; }
.folder-path > summary::before { content: "▸"; flex: none; color: var(--accent); }
.folder-path[open] > summary::before { content: "▾"; }
.folder-path > summary > span { min-width: 0; display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; }
.folder-path[open] > summary > span { display: block; }
.folder-path > summary:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; border-radius: 4px; }
.folder-copy .folder-root-warning { margin: 0; padding: 0; }
.folder-choice-status { display: flex; align-items: center; gap: 4px; color: var(--success); }
.folder-placeholder { display: flex; align-items: center; gap: 12px; border: 1px dashed var(--line); border-radius: 14px; padding: 18px; font-size: 14px; }
.folder-picker { overflow: hidden; border: 1px solid var(--line); border-radius: 16px; background: var(--canvas); }
.folder-picker .folder-picker-hint { margin: 0; padding: 12px 14px 8px; font-size: 13px; }
.folder-search { display: flex; align-items: center; gap: 8px; margin: 4px 14px 10px; padding: 0 10px; border: 1px solid var(--line); border-radius: 10px; background: var(--field); }
.folder-search > .icon { flex: none; color: var(--fg-dim); }
.folder-search input { flex: 1; min-width: 0; width: 100%; padding: 10px 0; border: 0; background: transparent; color: var(--fg); }
.folder-search input::-webkit-search-cancel-button { display: none; }
.folder-search button { display: grid; place-items: center; flex: none; padding: 6px; }
.folder-picker .folder-search-scope,.folder-search-limit { margin: 0; font-size: 13px; color: var(--fg-dim); }
.folder-search-scope { flex: 1; min-width: 0; padding-left: 6px; }
.folder-search-limit { padding: 8px; }
.folder-toolbar { display: flex; align-items: center; gap: 4px; padding: 4px 8px 8px; border-bottom: 1px solid var(--line); }
.folder-toolbar .folder-back { display: grid; place-items: center; flex: none; width: 34px; min-height: 34px; padding: 6px; }
.folder-crumbs { display: flex; align-items: center; gap: 3px; flex: 1; min-width: 0; overflow-x: auto; white-space: nowrap; scrollbar-width: thin; }
.folder-crumbs button,.folder-crumbs > span { flex: none; max-width: 200px; overflow: hidden; text-overflow: ellipsis; padding: 6px; font-size: 13px; }
.folder-crumbs [aria-current] { font-weight: 600; color: var(--fg); }
.folder-list { min-height: 116px; max-height: clamp(116px, 28dvh, 280px); overflow-y: auto; overscroll-behavior: contain; scrollbar-width: thin; }
.folder-state { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 10px; text-align: center; min-height: 104px; margin: 0; padding: 18px; font-size: 14px; }
.folder-state p { margin: 0; }
.folder-current { display: grid; gap: 4px; border-top: 1px solid var(--line); padding: 12px 14px 0; }
.folder-current > span { font-size: 13px; color: var(--fg-dim); }
.folder-current strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
.folder-root-warning { display: flex; align-items: flex-start; gap: 8px; margin: 0; padding: 10px 14px 0; color: var(--warning); font-size: 13px; }
.folder-root-warning .icon { flex: none; margin-top: 3px; }
.folder-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 8px; padding: 12px 14px; }
@media (max-width: 600px) {
  .lan-dialog { padding: 20px; }
  .lan-dialog form { max-height: calc(100dvh - 104px); }
  .folder-selection { grid-template-columns: auto minmax(0, 1fr); }
  .folder-selection > button { grid-column: 2; justify-self: start; }
}
@media (max-width: 1100px) { .lan-page { padding: 18px; } .content-grid { gap: 14px; } .service { gap: 18px; padding: 20px; } .qr-panel { width: 125px; flex-basis: 125px; } .qr-panel svg { width: 120px; height: 120px; } .detail { padding: 18px; } }
@container (max-width: 1050px) {
  .content-grid { grid-template-columns: 1fr; }
  .space-list { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr)); gap: 10px; max-height: min(320px, 40vh); padding: 6px; overflow-y: auto; overscroll-behavior: contain; }
  .space-card { margin: 0; padding: 14px; gap: 12px; box-shadow: inset 0 1px 0 var(--glass-rim); }
}
@container (max-width: 760px) { .service { flex-wrap: wrap; } .qr-panel { width: 100%; flex-basis: 100%; } }
</style>
