import { computed, reactive, watch } from "vue";
import { Dialogs, Events } from "@wailsio/runtime";
import { DriveService } from "../bindings/github.com/tonyflux/pkp/internal/services";
import type { Activity, Change, Config, Issue, Node as SyncNode, Pair, Preview, Progress } from "../bindings/github.com/tonyflux/pkp/internal/foldersync/models";
import { N_, t } from "./i18n";
import { closeRelocate, openCloudFolder, openSyncFolderPicker, relocate, run, syncDraft as syncDraftGuard } from "./store";

export type SyncMode = "bidirectional" | "backup" | "download";
export type SyncConfig = Omit<Config, "deletes"> & { deletes: Record<string, boolean> };
export type { SyncNode };
export type SyncIssue = Issue;
export type SyncActivity = Activity;
export type SyncProgress = Progress;
export type SyncChange = Change;
export type SyncPreview = Omit<Preview, "changes"> & { changes: Change[] };
export type SyncPair = Omit<Pair, "issues" | "activities" | "deletes" | "deletePlan"> & {
  issues: Issue[];
  activities: Activity[];
  deletes: Record<string, boolean | undefined>;
  deletePlan: SyncPreview | null;
};
const service = DriveService;
export const syncModes: { value: SyncMode; title: string; note: string }[] = [
  { value: "bidirectional", title: N_("双向同步"), note: N_("两个方向更新") },
  { value: "backup", title: N_("本地备份到云端"), note: N_("只上传本地变化") },
  { value: "download", title: N_("云端下载到本地"), note: N_("只下载云端变化") },
];
export function syncModeName(mode: string): string { return t(syncModes.find((m) => m.value === mode)?.title ?? N_("未知")); }
const stateWords: Record<string, string> = {
  idle: N_("已同步"), scanning: N_("正在核对内容"), syncing: N_("正在同步"), paused: N_("已暂停"), pausing: N_("正在安全暂停"), offline: N_("等待网络"), disk: N_("等待磁盘"), auth: N_("需要重新登录"), permission: N_("无访问权限"), "delete-guard": N_("删除保护 · 等待确认"), repair: N_("需要重新配对"), failed: N_("失败"), unknown: N_("状态待确认"),
};
export function syncStateName(pair: SyncPair): string {
  if (pair.state === "idle" && !pair.lastSuccess) return t("等待首次核对");
  return t(stateWords[pair.state] ?? N_("状态待确认"));
}
export function syncNeedsAttention(pair: SyncPair): boolean { return !!pair.issues?.length || ["disk", "auth", "permission", "delete-guard", "repair", "failed", "unknown"].includes(pair.state); }
export function syncActionName(action: string): string {
  const words: Record<string, string> = { upload: N_("上传"), download: N_("下载"), mkdir: N_("保留目录结构"), rename: N_("重命名"), trash: N_("移到回收站"), keep: N_("保留"), conflict: N_("保留两者"), skip: N_("跳过"), blocked: N_("阻断项"), done: N_("已完成"), complete: N_("已完成"), failed: N_("失败"), unknown: N_("状态待确认"), success: N_("已完成") };
  return words[action] ? t(words[action]) : action;
}
export function syncDirectionName(direction: string): string {
  if (["upload", "local-to-cloud", "to-cloud"].includes(direction)) return t("本地 → 云端");
  if (["download", "cloud-to-local", "to-local"].includes(direction)) return t("云端 → 本地");
  if (direction === "both" || direction === "bidirectional") return t("本地 ↔ 云端");
  return direction;
}
export const sync = reactive({ pairs: [] as SyncPair[], loading: false, error: "", query: "", scope: "current" as "current" | "all", selected: "", tab: "activity" as "activity" | "issues" | "settings", detail: false, draftOpen: false, busy: {} as Record<string, boolean> });
export const syncAttention = computed(() => new Set(sync.pairs.filter(syncNeedsAttention).map((p) => p.id)).size);
export const syncDraft = reactive({ config: null as SyncConfig | null, view: "form" as "form" | "preview", preview: null as SyncPreview | null, scanning: false, starting: false, closing: false, picking: false, error: "", localError: "", acknowledged: false, changed: false });
export const syncDialog = reactive({ kind: "" as "" | "conflict" | "deletes" | "remove", pairID: "", issue: null as SyncIssue | null, preview: null as SyncPreview | null, acknowledged: false, working: false, error: "" });
watch(() => [sync.draftOpen, syncDraft.starting || syncDraft.closing || syncDialog.working] as const, ([open, busy]) => {
  syncDraftGuard.pending = open;
  syncDraftGuard.busy = busy;
  syncDraftGuard.discard = cancelSyncInteraction;
}, { immediate: true, flush: "sync" });
let loadGeneration = 0;
let stateGeneration = 0;
let draftGeneration = 0;
let previewRequest = "";
let listening = false;
function errorText(error: unknown): string { return error instanceof Error ? error.message : String(error); }
function setPairs(pairs: Pair[] | null) {
  sync.pairs = (pairs ?? []).map((p) => ({ ...p, issues: p.issues ?? [], activities: p.activities ?? [], deletes: p.deletes ?? {}, progress: p.progress ?? { path: "", direction: "", done: 0, total: 0, completed: 0, files: 0 }, deletePlan: p.deletePlan ? { ...p.deletePlan, changes: p.deletePlan.changes ?? [] } : null }));
  if (sync.selected && !sync.pairs.some((p) => p.id === sync.selected)) { sync.selected = ""; sync.detail = false; }
  if (syncDialog.kind && !sync.pairs.some((p) => p.id === syncDialog.pairID) && !syncDialog.working) syncDialog.kind = "";
}
export async function loadSync(): Promise<void> {
  const generation = ++loadGeneration, version = stateGeneration;
  sync.loading = true;
  try { const pairs = await service.SyncPairs(); if (generation === loadGeneration && version === stateGeneration) { setPairs(pairs); sync.error = ""; } }
  catch (error) { if (generation === loadGeneration) sync.error = errorText(error); }
  finally { if (generation === loadGeneration) sync.loading = false; }
}
export function watchSync(): void {
  if (listening) return;
  listening = true;
  Events.On("sync:state", (event) => { stateGeneration++; setPairs(event.data); });
  void loadSync();
}
export function openSyncCreate(account: string): void {
  if (!account || syncDraftGuard.busy || sync.draftOpen || syncDialog.kind) return;
  draftGeneration++;
  syncDraft.config = { id: "", account, name: "", localPath: "", cloudId: "", cloudPath: "", mode: "bidirectional", deletes: { bidirectional: true, backup: false, download: false } };
  resetDraft();
}
export async function openSyncEdit(pair: SyncPair): Promise<void> {
  if (syncDraftGuard.busy || sync.draftOpen || syncDialog.kind || sync.busy[pair.id]) return;
  const generation = ++draftGeneration;
  sync.busy[pair.id] = true;
  syncDraftGuard.pending = true;
  syncDraftGuard.busy = true;
  try {
    await service.SyncBeginEdit(pair.id);
    if (generation !== draftGeneration) { await cancelPreview(pair.id); return; }
    syncDraft.config = { id: pair.id, account: pair.account, name: pair.name, localPath: pair.localPath, cloudId: pair.cloudId, cloudPath: pair.cloudPath, mode: pair.mode, deletes: { bidirectional: true, backup: false, download: false, ...pair.deletes } };
    resetDraft();
  } catch (error) { sync.error = errorText(error); }
  finally { delete sync.busy[pair.id]; syncDraftGuard.pending = sync.draftOpen; syncDraftGuard.busy = syncDraft.starting; }
}
function resetDraft() { syncDraft.view = "form"; syncDraft.preview = null; syncDraft.scanning = false; syncDraft.starting = false; syncDraft.picking = false; syncDraft.error = ""; syncDraft.localError = ""; syncDraft.acknowledged = false; syncDraft.changed = false; sync.draftOpen = true; }
async function cancelPreview(id: string) { if (!id) return; try { await service.SyncCancelPreview(id); } catch (error) { sync.error = errorText(error); } }
export async function closeSyncDraft(): Promise<boolean> {
  if (syncDraft.starting || syncDraft.closing) return false;
  draftGeneration++;
  if (relocate.mode === "sync") closeRelocate();
  const previewID = syncDraft.preview?.id;
  const requestID = previewRequest;
  const pairID = syncDraft.config?.id;
  syncDraft.scanning = false;
  syncDraft.closing = true;
  try {
    await Promise.all([previewID, requestID].filter((id): id is string => !!id).map((id) => service.SyncCancelPreview(id)));
    if (pairID) await service.SyncCancelPreview(pairID);
    previewRequest = "";
    sync.draftOpen = false; syncDraft.config = null; syncDraft.preview = null;
    return true;
  } catch (error) {
    syncDraft.error = errorText(error);
    syncDraft.view = "form"; syncDraft.acknowledged = false;
    sync.error = syncDraft.error;
    return false;
  } finally { syncDraft.closing = false; }
}
export function syncUnsubmitted(): string { return sync.draftOpen ? t("未提交的文件夹同步设置") : ""; }
export async function cancelSyncInteraction(): Promise<void> {
  if (syncDraft.starting || syncDialog.working) return;
  syncDialog.kind = "";
  if (!(await closeSyncDraft())) throw new Error(syncDraft.error);
}
export function backSyncDraft(): void {
  if (syncDraft.starting || syncDraft.closing) return;
  if (syncDraft.view === "form") { void closeSyncDraft(); return; }
  syncDraft.view = "form"; syncDraft.acknowledged = false;
}
export async function pickSyncLocal(): Promise<void> {
  if (!syncDraft.config || syncDraft.picking || syncDraft.scanning || syncDraft.closing) return;
  const generation = draftGeneration;
  syncDraft.picking = true; syncDraft.localError = "";
  try {
    const path = await Dialogs.OpenFile({ Title: t("选择同步的本地文件夹"), CanChooseFiles: false, CanChooseDirectories: true, CanCreateDirectories: true, ButtonText: t("选择"), Directory: syncDraft.config.localPath || undefined });
    if (generation !== draftGeneration || !syncDraft.config || !path) return;
    const validated = await service.SyncLocalFolder(path);
    if (generation !== draftGeneration || !syncDraft.config) return;
    syncDraft.config.localPath = validated;
    if (!syncDraft.config.name) syncDraft.config.name = validated.replace(/[\\/]+$/, "").split(/[\\/]/).at(-1) ?? "";
  } catch (error) { if (generation === draftGeneration) syncDraft.localError = errorText(error); }
  finally { if (generation === draftGeneration) syncDraft.picking = false; }
}
export function browseSyncCloud(): void {
  const config = syncDraft.config;
  if (!config || syncDraft.scanning || syncDraft.starting || syncDraft.picking || syncDraft.closing) return;
  const generation = draftGeneration;
  openSyncFolderPicker(config.account, config.cloudId, (trail) => {
    if (generation !== draftGeneration || syncDraft.config !== config) return;
    config.cloudId = trail.at(-1)?.id ?? "";
    config.cloudPath = ["全部文件", ...trail.map((crumb) => crumb.name)].join(" / "); // i18n-ignore: canonical root metadata
  });
}
export async function previewSyncDraft(): Promise<void> {
  if (!syncDraft.config || syncDraft.scanning || syncDraft.starting || syncDraft.picking || syncDraft.closing) return;
  if (!syncDraft.config.localPath || !syncDraft.config.cloudPath || !syncDraft.config.name.trim()) return;
  const generation = draftGeneration;
  const previous = syncDraft.preview;
  syncDraft.scanning = true; syncDraft.error = ""; syncDraft.acknowledged = false;
  const request = crypto.randomUUID(); previewRequest = request;
  const config = { ...syncDraft.config, name: syncDraft.config.name.trim(), deletes: { ...syncDraft.config.deletes } };
  try {
    const preview = await service.SyncPreview(request, config);
    if (generation !== draftGeneration || !sync.draftOpen) { await cancelPreview(preview.id); return; }
    syncDraft.preview = { ...preview, changes: preview.changes ?? [] }; syncDraft.view = "preview";
    if (!config.id && previous && previous.id !== preview.id) await cancelPreview(previous.id);
  } catch (error) { if (generation === draftGeneration) syncDraft.error = errorText(error); }
  finally { if (generation === draftGeneration) { syncDraft.scanning = false; previewRequest = ""; } }
}
export async function startSyncDraft(): Promise<void> {
  const preview = syncDraft.preview;
  if (syncDraft.view !== "preview" || !preview || preview.blocked || preview.error || !syncDraft.acknowledged || syncDraft.starting || syncDraft.scanning || syncDraft.closing) return;
  const generation = draftGeneration;
  syncDraft.starting = true; syncDraft.error = "";
  try {
    const result = await service.SyncStart(preview.id);
    if (generation !== draftGeneration) return;
    if (result.preview) { syncDraft.preview = { ...result.preview, changes: result.preview.changes ?? [] }; syncDraft.changed = true; syncDraft.acknowledged = false; }
    else if (result.pair) { sync.selected = result.pair.id; sync.detail = true; sync.draftOpen = false; syncDraft.config = null; syncDraft.preview = null; await loadSync(); }
    else syncDraft.error = t("服务未返回同步关系或新预览，请重新检查。");
  } catch (error) { if (generation === draftGeneration) syncDraft.error = errorText(error); }
  finally { if (generation === draftGeneration) syncDraft.starting = false; }
}
export async function openSyncRoot(pair: SyncPair, side: "local" | "cloud"): Promise<void> {
  if (side === "local") await run(t("打开本地文件夹"), () => service.RevealSyncLocalFolder(pair.localPath));
  else await openCloudFolder(pair.account, pair.cloudId);
}
export async function syncPairAction(pair: SyncPair, action: "pause" | "resume" | "scan"): Promise<void> {
  if (sync.busy[pair.id]) return;
  sync.busy[pair.id] = true; sync.error = "";
  try { if (action === "scan") await service.SyncScan(pair.id); else await service.SyncPause(pair.id, action === "pause"); await loadSync(); }
  catch (error) { sync.error = errorText(error); }
  finally { delete sync.busy[pair.id]; }
}
export function openSyncDialog(kind: "conflict" | "deletes" | "remove", pair: SyncPair, issue?: SyncIssue): void {
  if (syncDialog.working || sync.draftOpen || syncDraftGuard.busy) return;
  syncDialog.pairID = pair.id; syncDialog.issue = issue ? { ...issue } : null; syncDialog.preview = pair.deletePlan ? { ...pair.deletePlan, changes: [...pair.deletePlan.changes] } : null;
  syncDialog.acknowledged = false; syncDialog.error = ""; syncDialog.kind = kind;
}
export async function confirmSyncDialog(): Promise<void> {
  const pair = sync.pairs.find((p) => p.id === syncDialog.pairID);
  if (!pair || syncDialog.working) return;
  const kind = syncDialog.kind, preview = syncDialog.preview, issue = syncDialog.issue;
  if (kind === "deletes" && (!syncDialog.acknowledged || !preview || pair.deletePlan?.id !== preview.id)) { syncDialog.error = t("删除计划已变化，请关闭后重新审阅。"); return; }
  if (kind === "conflict" && (!issue?.keepName || !pair.issues.some((i) => i.id === issue.id && i.keepName === issue.keepName))) { syncDialog.error = t("冲突计划已变化，请关闭后重新审阅。"); return; }
  syncDialog.working = true; syncDialog.error = "";
  try {
    if (kind === "remove") await service.SyncRemove(pair.id);
    else if (kind === "deletes" && preview) await service.SyncApproveDeletes(pair.id, preview.id);
    else if (kind === "conflict" && issue) await service.SyncKeepBoth(pair.id, issue.id);
    else return;
    syncDialog.kind = ""; await loadSync();
  } catch (error) { syncDialog.error = errorText(error); }
  finally { syncDialog.working = false; }
}
