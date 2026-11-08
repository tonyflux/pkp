import { reactive } from "vue";
import { DriveService } from "../bindings/github.com/tonyflux/pkp/internal/services";
import type { ShareEntry, ShareImport, SharePreview, ShareRestoreResult } from "../bindings/github.com/tonyflux/pkp/internal/services";
import { accounts, closeRelocate, currentAccount, drive, externalOpen, openCloudFolder, openShareFolderPicker, relocate, type Crumb } from "./store";
import { shareLink } from "./sharelink";
import { t } from "./i18n";

interface Pick { entry: ShareEntry; ancestors: string[] }
export const shareImport = reactive({
  open: false, url: "", input: "", account: "", passCode: "", loading: false, submitting: false,
  error: "", pageError: "", info: null as ShareImport | null, entries: [] as ShareEntry[],
  trail: [] as Crumb[], dest: [] as Crumb[], nextPageToken: "", picked: new Map<string, Pick>(),
  attempted: false, result: null as ShareRestoreResult | null,
  preview: null as SharePreview | null, previewOpen: false, previewLoading: false, previewError: "",
});
let generation = 0;
let pageGeneration = 0;
let previewGeneration = 0;
let failedPage: { trail: Crumb[]; more: boolean } | null = null;
let pollTimer: ReturnType<typeof setTimeout> | undefined;
const folderEntries = new Map<string, ShareEntry[]>();
let deadlineTimer: ReturnType<typeof setTimeout> | undefined;
const message = (error: unknown) => error instanceof Error ? error.message : String(error);
const alive = (version: number) => shareImport.open && version === generation;
export function shareImportBusy(): boolean { return shareImport.submitting; }
export function shareImportPending(): string { return shareImport.open && !shareImport.result ? t("分享转存") : ""; }

function release(url: string) { if (url) void DriveService.ClosePreview(url).catch(() => undefined); }
export function closeSharePreview(): void {
  previewGeneration++;
  release(shareImport.preview?.url ?? "");
  shareImport.preview = null;
  shareImport.previewOpen = false;
  shareImport.previewLoading = false;
  shareImport.previewError = "";
}
function invalidate(): void {
  generation++;
  pageGeneration++;
  clearTimeout(pollTimer);
  clearTimeout(deadlineTimer);
  pollTimer = undefined;
  closeSharePreview();
}
function clearContent(): void {
  shareImport.info = null;
  shareImport.entries = [];
  shareImport.trail = [];
  failedPage = null;
  shareImport.nextPageToken = "";
  shareImport.picked.clear();
  folderEntries.clear();
  shareImport.error = "";
  shareImport.pageError = "";
  shareImport.result = null;
  shareImport.attempted = false;
  shareImport.loading = false;
  shareImport.submitting = false;
}
export function closeShareImport(): void {
  invalidate();
  if (shareImport.open) externalOpen.paused = true;
  if (relocate.mode === "shareImport") closeRelocate();
  clearContent();
  shareImport.open = false;
  shareImport.url = "";
  shareImport.input = "";
  shareImport.account = "";
  shareImport.passCode = "";
  shareImport.dest = [];
}
export async function openShareImport(raw: string): Promise<void> {
  // A second native link must not replace a draft or an accepted transfer.
  if (shareImport.open) return;
  invalidate();
  clearContent();
  shareImport.open = true;
  shareImport.input = raw;
  shareImport.account = currentAccount.value?.id ?? accounts.list[0]?.id ?? "";
  shareImport.dest = shareImport.account === currentAccount.value?.id ? drive.trail.map(c => ({ ...c })) : [];
  if (raw.trim()) await inspectShareInput();
}
export async function inspectShareInput(): Promise<void> {
  if (!shareImport.open || shareImport.attempted) return;
  const link = shareLink(shareImport.input);
  invalidate();
  clearContent();
  if (link?.url !== shareImport.url) shareImport.passCode = link?.passCode ?? "";
  if (!link) { shareImport.url = ""; shareImport.error = t("请输入有效的 PikPak 分享链接"); return; }
  shareImport.url = link.url;
  if (link.passCode && !shareImport.passCode) shareImport.passCode = link.passCode;
  if (!shareImport.account) { shareImport.error = t("请先添加转存账号"); return; }
  const version = generation;
  shareImport.loading = true;
  try {
    const info = await DriveService.InspectShare(shareImport.account, shareImport.url, shareImport.passCode.trim());
    if (!alive(version)) return;
    shareImport.info = info;
    shareImport.entries = info.entries ?? [];
    folderEntries.set("", shareImport.entries);
    shareImport.nextPageToken = info.nextPageToken;
    if (info.status !== "OK" && !shareNeedsCode()) shareImport.error = info.statusText || t("此分享已失效或无法访问");
  } catch (error) { if (alive(version)) shareImport.error = message(error); }
  finally { if (alive(version)) shareImport.loading = false; }
}
export function shareNeedsCode(): boolean { return ["PASS_CODE_EMPTY", "PASS_CODE_ERROR"].includes(shareImport.info?.status ?? ""); }
export async function chooseShareAccount(account: string): Promise<void> {
  if (!shareImport.open || shareImport.attempted || account === shareImport.account || !accounts.list.some(a => a.id === account)) return;
  if (relocate.mode === "shareImport") closeRelocate();
  shareImport.account = account;
  shareImport.dest = [];
  await inspectShareInput();
}
export function pickShareDestination(): void {
  if (!shareImport.account || shareImport.attempted) return;
  const version = generation;
  openShareFolderPicker(shareImport.account, shareImport.dest, trail => {
    if (alive(version) && !shareImport.attempted) shareImport.dest = trail.map(c => ({ ...c }));
  });
}
export async function browseShare(trail: Crumb[], more = false): Promise<void> {
  const info = shareImport.info;
  if (!info || info.status !== "OK" || shareImport.loading || (more && !shareImport.nextPageToken)) return;
  const version = generation, page = ++pageGeneration;
  const target = trail.map(c => ({ ...c }));
  const token = more ? shareImport.nextPageToken : "";
  shareImport.loading = true;
  shareImport.pageError = "";
  failedPage = null;
  try {
    const result = await DriveService.ListShareFiles(shareImport.account, info.shareId, info.passCodeToken, target.at(-1)?.id ?? "", token);
    if (!alive(version) || page !== pageGeneration) return;
    shareImport.trail = target;
    const entries = more ? [...shareImport.entries, ...(result.entries ?? [])] : result.entries ?? [];
    shareImport.entries = [...new Map(entries.map(entry => [entry.id, entry])).values()];
    folderEntries.set(target.at(-1)?.id ?? "", shareImport.entries);
    shareImport.nextPageToken = result.nextPageToken;
    if (result.passCodeToken) info.passCodeToken = result.passCodeToken;
  } catch (error) {
    if (alive(version) && page === pageGeneration) {
      shareImport.pageError = message(error);
      failedPage = { trail: target, more };
    }
  }
  finally { if (alive(version) && page === pageGeneration) shareImport.loading = false; }
}
export async function retrySharePage(): Promise<void> {
  if (failedPage) await browseShare(failedPage.trail, failedPage.more);
}
export function shareCovered(entry: ShareEntry): boolean {
  return !shareImport.picked.has(entry.id) && shareImport.trail.some(c => shareImport.picked.has(c.id));
}
export function shareSelected(entry: ShareEntry): boolean { return shareImport.picked.has(entry.id) || shareCovered(entry); }
function splitCoveredAncestors(excludeId?: string): void {
  const k = shareImport.trail.findIndex(c => shareImport.picked.has(c.id));
  if (k < 0) return;
  for (const crumb of shareImport.trail.slice(k)) shareImport.picked.delete(crumb.id);
  for (let i = k; i < shareImport.trail.length - 1; i++) {
    const folderId = shareImport.trail[i].id, nextId = shareImport.trail[i + 1].id;
    const ancestors = shareImport.trail.slice(0, i + 1).map(c => c.id);
    for (const item of folderEntries.get(folderId) ?? []) {
      if (item.id !== nextId) shareImport.picked.set(item.id, { entry: item, ancestors });
    }
  }
  if (excludeId !== undefined) {
    const ancestors = shareImport.trail.map(c => c.id);
    for (const item of shareImport.entries) {
      if (item.id !== excludeId) shareImport.picked.set(item.id, { entry: item, ancestors });
    }
  }
}
function select(entry: ShareEntry): void {
  if (shareCovered(entry)) return;
  if (entry.folder) for (const [id, pick] of shareImport.picked) if (pick.ancestors.includes(entry.id)) shareImport.picked.delete(id);
  shareImport.picked.set(entry.id, { entry, ancestors: shareImport.trail.map(c => c.id) });
}
export function toggleShareEntry(entry: ShareEntry): void {
  if (shareImport.attempted || shareImport.loading) return;
  if (shareCovered(entry)) { splitCoveredAncestors(entry.id); return; }
  if (shareImport.picked.has(entry.id)) shareImport.picked.delete(entry.id);
  else select(entry);
}
export function selectVisibleShare(all: boolean): void {
  if (shareImport.attempted || shareImport.loading) return;
  if (!all && shareImport.trail.some(c => shareImport.picked.has(c.id))) {
    splitCoveredAncestors();
    return;
  }
  for (const entry of shareImport.entries) {
    if (all) select(entry);
    else shareImport.picked.delete(entry.id);
  }
}
export async function previewShareEntry(entry: ShareEntry): Promise<void> {
  const info = shareImport.info;
  if (!info || entry.folder) return;
  closeSharePreview();
  const version = generation, preview = previewGeneration;
  shareImport.previewOpen = true;
  shareImport.previewLoading = true;
  try {
    const result = await DriveService.PreviewShareFile(shareImport.account, info.shareId, info.passCodeToken, entry.id);
    if (!alive(version) || preview !== previewGeneration) { release(result.url); return; }
    shareImport.preview = result;
  } catch (error) { if (alive(version) && preview === previewGeneration) shareImport.previewError = message(error); }
  finally { if (alive(version) && preview === previewGeneration) shareImport.previewLoading = false; }
}
function pollRestore(version: number, account: string, task: string, deadline: number): void {
  if (!alive(version)) return;
  if (Date.now() >= deadline) { shareImport.submitting = false; return; }
  pollTimer = setTimeout(async () => {
    if (!alive(version)) return;
    try {
      const result = await DriveService.ShareRestoreStatus(account, task);
      if (!alive(version)) return;
      shareImport.result = result;
      if (result.status !== "pending") { clearTimeout(deadlineTimer); shareImport.submitting = false; return; }
    } catch (error) {
      if (!alive(version)) return;
      shareImport.error = t("转存已提交，暂时无法确认进度：{error}", { error: message(error) });
      clearTimeout(deadlineTimer);
      shareImport.submitting = false;
      return;
    }
    pollRestore(version, account, task, deadline);
  }, 1500);
}
export async function confirmShareRestore(): Promise<void> {
  const info = shareImport.info;
  if (!shareImport.open || shareImport.attempted || shareImport.loading || !shareImport.account || info?.status !== "OK" || !shareImport.picked.size) return;
  const version = generation, account = shareImport.account;
  shareImport.attempted = true;
  shareImport.submitting = true;
  shareImport.error = "";
  try {
    const result = await DriveService.RestoreShare(account, { shareId: info.shareId, passCodeToken: info.passCodeToken, parentId: shareImport.dest.at(-1)?.id ?? "", fileIds: [...shareImport.picked.keys()] });
    if (!alive(version)) return;
    shareImport.result = result;
    if (result.status === "pending" && result.taskId) {
      deadlineTimer = setTimeout(() => {
        if (alive(version)) { shareImport.submitting = false; clearTimeout(pollTimer); }
      }, 30000);
      pollRestore(version, account, result.taskId, Date.now() + 30000);
    } else shareImport.submitting = false;
  } catch (error) {
    if (!alive(version)) return;
    // A lost response may already have created files. Never resend this mutation.
    shareImport.error = t("未能确认转存结果，请先检查目标文件夹，勿重复提交：{error}", { error: message(error) });
    shareImport.submitting = false;
  }
}
export async function openShareDestination(): Promise<void> {
  const account = shareImport.account, parent = shareImport.result?.parentId ?? shareImport.dest.at(-1)?.id ?? "";
  closeShareImport();
  await openCloudFolder(account, parent);
}
