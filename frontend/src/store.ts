/*
 * One reactive store per window. Every field is either UI state or a value
 * that came back from Go — nothing here invents data the backend did not
 * provide. The main window's copy is the whole application; the separate
 * player window's copy only mirrors the playback session (see inPlayerWindow).
 */
import { computed, markRaw, nextTick, reactive, watch } from "vue";
import { Browser, Clipboard, Dialogs, Events, Window } from "@wailsio/runtime";
import { applyLanguage, i18n, isLocale, N_, t, type LanguagePref } from "./i18n";
import { playFinished } from "./plays";
import { AppLockService, AppearanceService, DriveService, MenuBarService, NotifyService, PlayerService, TelemetryService } from "../bindings/github.com/tonyflux/pkp/internal/services";
import type {
  AIChatReply,
  AIDelta,
  AIReply,
  AISearchHit,
  AISearchResult,
  AISettings,
  AITestResult,
  AccessToken,
  AccessTokenOptions,
  Account,
  AppLockResult,
  AppLockState,
  Arrival,
  ArchiveItem,
  AudioDetails,
  ConversationInfo,
  ConnectedApp,
  CreatedShare,
  DownloadTask,
  DriveStatus,
  DupCopy,
  DupGroup,
  DupPage,
  EmptyFolder,
  EmptyFolderPage,
  Entry,
  FileDetail,
  FolderChoice,
  IndexProgress,
  ListRequest,
  LocalSettings,
  InboxChanged,
  Membership,
  MenuBarOpen,
  NotifyState,
  TelemetryState,
  OfflineResult,
  OrganizeProgress,
  OrganizeRecord,
  PlayerCommand,
  PlayerSession,
  PlayerState,
  LanguageState,
  SubtitleStyle,
  ProxyInput,
  ProxySettings,
  ProxyTestResult,
  QuotaInfo,
  RecentPlay,
  ShareRequest,
  ShareSummary,
  SignInRequest,
  SignInResult,
  RemoveProgress,
  RemoveReport,
  RemoveResult,
  TaskSummary,
  UploadTask,
  TextPreview,
  TorrentMeta,
  TransferPreferences,
  TransferQuota,
  WebDAVClient,
  WebDAVClientOptions,
  TMDBSettings,
  TMDBTestResult,
  TitleMatch,
  WebDAVInfo,
  ViewerReturnRequest,
  ViewerWindowState,
} from "../bindings/github.com/tonyflux/pkp/internal/services";
import { WORKSPACE_QUERY, keychainName, onWindows, settingsAppName } from "./dom";
import { bytes, maskLogin } from "./format";
import { playingLabel } from "./quality";
import { active, edited, hasChange, nameError, problems, summarize, toChanges, treeOf, type Change, type Tree } from "./organizer";
import { buildPlan, classify, emptiedFolders, groupMedia, type Identity, type MediaGroup, type MediaPlan } from "./media";
import {
  estimateTokens,
  followUp,
  formatTokens,
  identify,
  mergeIdentities,
  oneLine,
  parseIdentities,
  type ModelRequest,
  type TokenRange,
} from "./aiprompt";
import { applyMatches, parseGroups, titleQueries } from "./parse";
import {
  cardRows,
  cleanupForModel,
  fileInfoForModel,
  folderSummaryForModel,
  foldersForModel,
  Handles,
  isContentRefused,
  mediaSummaryForModel,
  modelPath,
  planForModel,
  playsForModel,
  quotaForModel,
  ROW_LIMIT,
  retryable,
  runTurn,
  searchForModel,
  transfersForModel,
  type AIChatRequest,
  type AssistantScope,
  type Card,
  type CardRow,
  type DownloadCard,
  type FilesCard,
  type FolderRef,
  type ModelRow,
  type OfflineCard,
  type PlanCard,
  type ProposedItem,
  type SearchResultGroup,
  type ShareCard,
  type StatsCard,
  type Target as AgentTarget,
  type ToolImpls,
  type Trace,
  type TurnDisclosure,
} from "./agent";
import {
  arrivalBasis,
  arrivalLine,
  basisOf,
  cardSource,
  commonTrail,
  conversationBody,
  conversationTitle,
  findCard,
  hasPendingPlan,
  originOf,
  planCounts,
  planRoute,
  planTarget,
  planSample,
  proposedChanges,
  readConversation,
  type ConversationView,
  type PlanOrigin,
} from "./assistantcore";
import { AI_PRESETS, CUSTOM_PRESET_ID, presetOf } from "./aipresets";
import { renameChanges, type RenameRule } from "./renamerules";
import { namingFor, type Naming } from "./templates";
import { failureOf, picksOf, taskFailure, type Failure } from "./extract";
import { mergeMoving, pollDelay, retryLabel, taskMoving, type TransferFilter, type TransferItem } from "./transfers";
import { parseSearch, searchText, sentenceSearch, type AIQuery, type ParsedSearch } from "./searchquery";
import { viewerError, viewerKind, type ViewerKind } from "./viewer";
import type { FolderCover, FolderStats } from "../bindings/github.com/tonyflux/pkp/internal/services";
import { dropAccountSavedSearches, loadSavedSearches, removeSavedSearch, renameSavedSearch, savedSearches, saveSearch, updateSavedSearch, type SavedSearch, type SavedSearchCategory, type SavedSearchRange } from "./savedsearches";
import { cancelMediaLibraryLoad, dropAccountMediaLibrary, loadMediaLibrary } from "./medialibrary";
import wallDark from "./assets/wall-dark.webp";
import wallLight from "./assets/wall-light.webp";

// Mirrors the services.Event* names on the Go side.
const PlayerStateEvent = "player:state";
const IndexProgressEvent = "drive:index";
const UploadStateEvent = "upload:state";
const DownloadStateEvent = "download:state";
const PlayerSessionEvent = "player:session";
const PlayerCommandEvent = "player:command";
const PlayerWindowClosedEvent = "player:window-closed";
const ViewerReturnEvent = "viewer:return";
const ViewerReturnCancelledEvent = "viewer:return-cancelled";
const AccountsEvent = "drive:accounts";
const OrganizeProgressEvent = "organize:progress";
const RemoveProgressEvent = "drive:remove";
const MenuBarOpenEvent = "menubar:open";
const AIDeltaEvent = "ai:delta";
const InboxChangedEvent = "inbox:changed";
const AppLockStateEvent = "applock:state";

/**
 * inPlayerWindow is true in the separate player window's page. Its copy of
 * this store gets mpv's state straight from Go and the playback session from
 * the main window, which owns it; anything that changes the session is sent
 * back there to be done.
 */
export const inPlayerWindow = new URLSearchParams(location.search).get("window") === "player";

/** inMenuBarWindow is true in the menu bar panel's page (MenuBarWindow), which reads Go only. */
export const inMenuBarWindow = new URLSearchParams(location.search).get("window") === "menubar";

export const inViewerWindow = new URLSearchParams(location.search).get("window") === "viewer";

export type Route = "files" | "transfers" | "player" | "shares" | "settings" | "viewer" | "assistant" | "cleanup" | "sync" | "history";
export type LibraryView = "all" | "starred" | "trash";
export type CategoryFilter = "all" | "video" | "image" | "document" | "audio" | "other";
/** All Files's Browse Scope: one folder's direct children, or every file of the drive. */
export type FilesScope = "folder" | "drive";
export type SortKey = "modified" | "name" | "size";
export const sorts: { key: SortKey; label: string }[] = [
  { key: "modified", get label() { return t("按修改时间"); } },
  { key: "name", get label() { return t("按名称"); } },
  { key: "size", get label() { return t("按大小"); } },
];

// Settings · General · Accent Color. The colours are style.css's Skins blocks; cobalt is
// the design's own and the default.
export type Skin = "cobalt" | "teal" | "violet" | "amber" | "rose" | "graphite";
export const skins: { key: Skin; label: string }[] = [
  { key: "cobalt", get label() { return t("钴蓝"); } },
  { key: "teal", get label() { return t("青碧"); } },
  { key: "violet", get label() { return t("紫晶"); } },
  { key: "amber", get label() { return t("琥珀"); } },
  { key: "rose", get label() { return t("樱粉"); } },
  { key: "graphite", get label() { return t("石墨"); } },
];
const savedSkin = localStorage.getItem("pkp.skin");

/** The right column's two views. */
export type PanelTab = "overview" | "details";

export interface Crumb {
  id: string;
  name: string;
}

const emptyPlayerState: PlayerState = {
  running: false,
  idle: true,
  source: "",
  title: "",
  paused: false,
  position: 0,
  duration: 0,
  volume: 100,
  muted: false,
  speed: 1,
  width: 0,
  height: 0,
  buffering: false,
  cacheSeconds: 0,
  eof: false,
  error: "",
  bufferPercent: 0,
  cacheSpeed: 0,
  seeking: false,
  loading: false,
  quality: { options: [], current: "", choice: "auto", switching: "", failed: "", error: "", lowered: false },
  subtitleStyle: { scale: 1, raise: 0, color: "#FFFFFF" },
  subtitleDelay: 0,
  audioDelay: 0,
};

// Appearance: a fixed light or dark, or whatever macOS is set to. Nothing stored
// (a first run) is Follow System.
export type ThemeMode = "system" | "light" | "dark";
const systemDark = window.matchMedia("(prefers-color-scheme: dark)");
const savedTheme = localStorage.getItem("pkp.theme");
const themeMode: ThemeMode = savedTheme === "light" || savedTheme === "dark" ? savedTheme : "system";

export const ui = reactive({
  themeMode,
  // The theme in force: the mode, with Follow System resolved.
  theme: (themeMode === "system" ? (systemDark.matches ? "dark" : "light") : themeMode) as "light" | "dark",
  // A skin from a newer or older build that this one does not know is cobalt.
  skin: skins.find((s) => s.key === savedSkin)?.key ?? "cobalt",
  // The custom wallpaper's version (AppearanceService.SetWallpaper); empty
  // for the built-in scene.
  wallpaper: localStorage.getItem("pkp.wallpaper") ?? "",
  route: "files" as Route,
  view: "all" as LibraryView,
  layout: (localStorage.getItem("pkp.layout") as "grid" | "list" | null) ?? "grid",
  category: "all" as CategoryFilter,
  videoLayout: "files" as "files" | "works",
  // Only All Files has it; the folder (drive.trail) is kept while the drive is shown.
  scope: "folder" as FilesScope,
  sortKey: (localStorage.getItem("pkp.sortKey") as SortKey | null) ?? "modified",
  sortAsc: false,
  // The toolbar search is the page's own: files and folders (query), the
  // share list (shareQuery), the transfer list (transferQuery). One text
  // never filters another page behind the user's back.
  query: "",
  shareQuery: "",
  transferQuery: "",
  playQuery: "",
  cloudAddOpen: false,
  // New · New Folder: the sheet that names the folder.
  newFolderOpen: false,
  // The file page's right column stays there
  // unless the user put it away, a choice kept across launches. Overview is the
  // account's storage, transfers and recent plays; Details is what is picked.
  panelOpen: localStorage.getItem("pkp.panel") !== "0",
  panelTab: "overview" as PanelTab,
  // Under 960px there is no room for both: the files and the column become
  // two pages of one workspace, Files and Info.
  workspace: "files" as "files" | "info",
  busy: false,
  // Set when the very first Status() call fails: the boot splash shows it
  // with a retry instead of waiting forever.
  bootError: "",
  fullscreen: false,
  message: "",
  messageTone: "info" as "info" | "error" | "success",
  // Counts notices, so the same words posted again still read as a new one.
  messageSerial: 0,
  // Glass turned into solid surfaces (Settings · General): the manual twin of the
  // system's reduced-transparency setting, and the cheap mode on weak GPUs.
  reduceTransparency: localStorage.getItem("pkp.reduceTransparency") === "1",
  // The nav as an icon rail (Settings · General, ⌘\). Content narrower than 1080px
  // gets the rail whatever this says.
  navCompact: localStorage.getItem("pkp.navCompact") === "1",
  // The Switch Account panel. One at a time, opened from the nav's
  // account button, the toolbar's under 960px, or a page asking to switch.
  accountsOpen: false,
});

// The independent sync page registers its account-owned draft without
// importing it here (syncstore itself consumes this account store).
export const syncDraft = reactive({ pending: false, busy: false, discard: null as null | (() => void | Promise<void>) });

/**
 * The Settings tab a page sends the user to — Manage Accounts opens Account Management. The page
 * reads it when it mounts or when it changes, and clears it.
 */
export const settingsFocus = reactive({ tab: "" as "" | "accounts" | "ai" | "network" | "keys" | "updates" });

/** openSettings is Settings (⌘,), on `tab` when one is named. */
export function openSettings(tab: "" | "accounts" | "ai" | "network" | "keys" | "updates" = "") {
  if (!leaveSavedHistory(() => openSettings(tab))) return;
  ui.accountsOpen = false;
  settingsFocus.tab = tab;
  ui.route = "settings";
}

/** openAccountManager is Manage Accounts: Settings · Account Management. */
export function openAccountManager() {
  openSettings("accounts");
}

/** Navigation targets in the order of their number shortcuts. */
export type NavKey = LibraryView | "cleanup" | "assistant" | "sync" | "transfers" | "shares" | "history";
export const NAV_KEYS: readonly NavKey[] = ["all", "starred", "cleanup", "assistant", "history", "transfers", "sync", "shares", "trash"];

/** openNav is a click on the nav's item (or its ⌘1…⌘9). */
export function openNav(key: NavKey) {
  if (!leaveSavedHistory(() => openNav(key))) return;
  switch (key) {
    case "history":
      openHistory();
      return;
    case "sync":
      ui.route = "sync";
      return;
    case "transfers":
      ui.route = "transfers";
      return;
    case "shares":
      void openShares();
      return;
    case "cleanup":
      openCleanup();
      return;
    case "assistant":
      openAssistant();
      return;
    default:
      void openView(key);
  }
}

export const drive = reactive({
  // The account on screen (DriveStatus.accountId), or the sign-in page when
  // there is none.
  status: null as DriveStatus | null,
  // Avatar URL of the account on screen; "" until fetched or when none.
  avatar: "",
  entries: [] as Entry[],
  trail: [] as Crumb[],
  // Paging. The index answers with a total and takes an offset; the server
  // (before the index is ready) hands out a token and no total.
  total: -1,
  offset: 0,
  live: false,
  nextPageToken: "",
  indexProgress: null as IndexProgress | null,
  quota: null as QuotaInfo | null,
  // When the quota last answered (ISO), and why the latest refresh failed:
  // a failure keeps the last good figures and says how old they are.
  quotaAt: "",
  quotaError: "",
  // Continue Playing: recent plays, newest first; `playsLoaded` once one answer came,
  // so the empty state is never shown for "not asked yet".
  plays: [] as RecentPlay[],
  playsLoaded: false,
  // Every account's local uploads, each account's oldest first; fed by
  // UploadStateEvent. An upload names its account (UploadTask.account).
  uploads: [] as UploadTask[],
  // Every account's downloads to this computer, each account's oldest
  // first; fed by DownloadStateEvent. A download names its account.
  downloads: [] as DownloadTask[],
  // The entry Details describes in full. Only ever one, and only while exactly
  // one entry is picked; with several it sums them up instead.
  selectedId: "",
  // The multi-selection, in pick order. A plain click replaces it; ⌘/Ctrl
  // toggles one entry and ⇧ extends from `pickAnchor` in drawing order.
  picked: [] as string[],
  pickAnchor: "",
  detail: null as FileDetail | null,
  lastBatch: [] as OfflineResult[],
  // Per-surface waits. `ui.busy` says *something* is in flight; these say
  // which panel should draw a skeleton, and survive an empty answer so the
  // empty state is never mistaken for the loading state.
  listLoading: false,
  loadingMore: false,
  detailLoading: false,
  // Why FileDetail failed for the entry on show; said in the column.
  detailError: "",
});

/**
 * Dialogs of the account flows, one at a
 * time, hosted by AccountDialogs. The two questions resolve a promise the
 * asking action waits on.
 *
 *   signin   Add Account (account "") or Sign In Again of that account
 *   rename   Local Note
 *   remove   Sign Out and Remove, with what stops and what does not
 *   leave    an unsubmitted sheet belongs to the account on screen: stay, or
 *            discard it and switch (`what` names it)
 *   replace  another account's video is playing: stop it and play this one?
 */
export type AccountDialog =
  | { kind: "signin"; account: string }
  | { kind: "rename"; account: string }
  | { kind: "remove"; account: string }
  | { kind: "leave"; target: string; what: string; resolve: (go: boolean) => void }
  | { kind: "replace"; owner: string; resolve: (go: boolean) => void };

export const accounts = reactive({
  // Every account on this computer, in the order added.
  list: [] as Account[],
  // The account a switch is on its way to; "" when the screen is settled.
  switching: "",
  // Continue Playing When Switching Accounts (LocalSettings.keepPlayingOnSwitch), off by default.
  keepPlaying: false,
  dialog: null as AccountDialog | null,
});

/** One account's cloud-add tasks as the transfer list last read them. */
export interface CloudTasks {
  tasks: TaskSummary[];
  loading: boolean;
  // Tasks read so far by the walk in progress, for the page's progress.
  loaded: number;
  // When the list last answered (ISO), for the page's Updated At.
  updatedAt: string;
}

/**
 * Cloud tasks per account. A task list is the account's own and stays its
 * own, cached while another account is on screen and read again when it is
 * shown; All Accounts walks every signed-in account's.
 */
export const transfers = reactive({
  cloud: {} as Record<string, CloudTasks>,
});

// What the end of a file leads to. Decided here, not by mpv's own playlist.
export type PlayMode = "sequential" | "one" | "shuffle" | "stop";

// How the playback session is shown: the right of the file page is the
// context column's, so the picture lives on the immersive page or in the
// separate window, and browsing while it plays shows the bar. The session
// itself (playlist, cursor, mpv state) is independent of the file page: the
// file page's selection and the playing item may differ, and switching
// presentation never reloads the file.
//
//   off        no session
//   immersive  the full player page (route "player")
//   bar        picture hidden, audio continues; a 64px row under the page
//   window     the picture is in the separate player window; this window
//              keeps the session and shows the bar
export type Presentation = "off" | "immersive" | "bar" | "window";

export const player = reactive({
  state: { ...emptyPlayerState },
  playlist: [] as Entry[],
  index: -1,
  mode: (localStorage.getItem("pkp.playMode") as PlayMode | null) ?? "sequential",
  presentation: "off" as Presentation,
  // The route the immersive player returns to.
  returnRoute: "files" as Route,
  // The account the session's videos belong to: the one on screen when it
  // started. Its queue plays on in that account whatever is on screen.
  owner: "",
  // The separate window's copy of the owner, sent with the session.
  ownerInfo: null as Account | null,
  // Where the queue was started from, "All Files / Music" ("" when not known):
  // the music page's Playback Source.
  source: "",
});

// The rectangle the native video surface occupies, in CSS pixels of the
// window. Whoever hosts the picture (the immersive page or the separate
// window's player) reports it; the scene and the host cut this hole
// out of themselves so the surface below the webview shows through. `radius`
// is the hole's corner radius.
export const stage = reactive({ x: 0, y: 0, width: 0, height: 0, visible: false, radius: 0 });

export function setStage(rect: { x: number; y: number; width: number; height: number; visible: boolean }, radius: number) {
  Object.assign(stage, rect, { radius });
  if (rect.visible) document.documentElement.dataset.stage = "on";
  else delete document.documentElement.dataset.stage;
  void PlayerService.SetStage(rect).catch(() => undefined);
}

/**
 * holePath builds an even-odd clip path: `outer` filled, `hole` cut out with
 * rounded corners. Coordinates are relative to the clipped element's border
 * box; `outer` may extend past it so box shadows survive the clip.
 */
export function holePath(
  outer: { x: number; y: number; width: number; height: number },
  hole: { x: number; y: number; width: number; height: number },
  radius: number,
): string {
  const r = Math.max(0, Math.min(radius, hole.width / 2, hole.height / 2));
  const { x, y, width: w, height: h } = hole;
  const o = outer;
  const rect = `M${o.x} ${o.y}H${o.x + o.width}V${o.y + o.height}H${o.x}Z`;
  const round =
    `M${x + r} ${y}H${x + w - r}A${r} ${r} 0 0 1 ${x + w} ${y + r}V${y + h - r}` +
    `A${r} ${r} 0 0 1 ${x + w - r} ${y + h}H${x + r}A${r} ${r} 0 0 1 ${x} ${y + h - r}` +
    `V${y + r}A${r} ${r} 0 0 1 ${x + r} ${y}Z`;
  return `path(evenodd, "${rect} ${round}")`;
}

function applyTheme() {
  ui.theme = ui.themeMode === "system" ? (systemDark.matches ? "dark" : "light") : ui.themeMode;
  document.documentElement.dataset.theme = ui.theme;
}

export function setThemeMode(mode: ThemeMode) {
  ui.themeMode = mode;
  localStorage.setItem("pkp.theme", mode);
  applyTheme();
}

// macOS switching appearance (by hand, or Auto at dusk) reaches Follow System at once.
systemDark.addEventListener("change", () => {
  if (ui.themeMode === "system") applyTheme();
});

export function setSkin(skin: Skin) {
  ui.skin = skin;
  localStorage.setItem("pkp.skin", skin);
  document.documentElement.dataset.skin = skin;
}

// ---------- Interface Language ----------

const LanguageStateEvent = "language:state";
let languageListening = false;

function applyLanguageState(s: LanguageState) {
  if (isLocale(s.locale) && (s.preference === "system" || isLocale(s.preference))) applyLanguage(s.preference, s.locale);
}

/**
 * watchLanguage follows Interface Language as Go keeps it (settings.json), so every
 * window — main, player, menu bar panel — switches together; main.ts calls
 * it before mounting. Until Go answers, i18n.ts's last-known choice stands.
 */
export async function watchLanguage(): Promise<void> {
  if (!languageListening) {
    languageListening = true;
    Events.On(LanguageStateEvent, (event: { data: LanguageState }) => applyLanguageState(event.data));
  }
  try {
    applyLanguageState(await DriveService.Language());
  } catch (err) {
    console.warn("language: unread", err);
  }
}

/** setLanguage saves Interface Language for this computer; Go's text and every window follow. */
export async function setLanguage(pref: LanguagePref) {
  if (pref === i18n.pref) return;
  const state = await run(t("保存界面语言"), () => DriveService.SetLanguage(pref));
  if (state) applyLanguageState(state);
}

/** The scene's picture: the user's wallpaper, or the built-in lake in the theme's light. */
export const wallpaperSrc = computed(() =>
  ui.wallpaper ? `/appearance/wallpaper?v=${ui.wallpaper}` : ui.theme === "dark" ? wallDark : wallLight,
);

/** pickWallpaper asks for a picture and makes it the scene, on this computer. */
export async function pickWallpaper() {
  const path = await Dialogs.OpenFile({
    Title: t("选择壁纸"),
    Message: t("选一张图片作为窗口背景（JPEG、PNG、WebP 或 GIF）"),
    ButtonText: t("设为壁纸"),
    CanChooseFiles: true,
    CanChooseDirectories: false,
    Filters: [{ DisplayName: t("图片"), Pattern: "*.jpg;*.jpeg;*.png;*.webp;*.gif" }],
  });
  if (!path) return;
  const version = await run(t("设置壁纸"), () => AppearanceService.SetWallpaper(path));
  if (!version) return;
  ui.wallpaper = version;
  localStorage.setItem("pkp.wallpaper", version);
}

export async function clearWallpaper() {
  const ok = await run(t("恢复默认壁纸"), async () => {
    await AppearanceService.ClearWallpaper();
    return true;
  });
  if (ok) forgetWallpaper();
}

/** forgetWallpaper puts the built-in scene back: cleared, or the custom picture would not load. */
export function forgetWallpaper() {
  ui.wallpaper = "";
  localStorage.removeItem("pkp.wallpaper");
}

export function setLayout(layout: "grid" | "list") {
  ui.layout = layout;
  localStorage.setItem("pkp.layout", layout);
}

export const videoWorksShown = computed(() => ui.view !== "trash" && ui.category === "video" && ui.videoLayout === "works" && !localSearchShown.value);

export function setVideoLayout(layout: "files" | "works"): void {
  if (layout === ui.videoLayout) return;
  clearSelection();
  ui.videoLayout = layout;
  ui.workspace = "files";
}

export function setReduceTransparency(on: boolean) {
  ui.reduceTransparency = on;
  localStorage.setItem("pkp.reduceTransparency", on ? "1" : "0");
  applyTransparency();
}

export function setNavCompact(on: boolean) {
  ui.navCompact = on;
  localStorage.setItem("pkp.navCompact", on ? "1" : "0");
}

function applyTransparency() {
  if (ui.reduceTransparency) document.documentElement.dataset.transparency = "reduced";
  else delete document.documentElement.dataset.transparency;
}

export function notify(message: string, tone: "info" | "error" | "success" = "info") {
  ui.message = message;
  ui.messageTone = tone;
  ui.messageSerial += 1;
}

/**
 * copyText puts text on the clipboard and says what was copied — never the
 * text itself, which may be a credential. The webview may refuse its own
 * clipboard; the native one is the fallback.
 */
export async function copyText(text: string, what: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    try {
      await Clipboard.SetText(text);
    } catch {
      notify(t("无法复制{what}，请手动选中后复制", { what }), "error");
      return false;
    }
  }
  notify(t("{what}已复制到剪贴板", { what }), "success");
  return true;
}

export function dismiss() {
  ui.message = "";
}

/** run funnels every backend call through one busy flag and one error surface. */
let inFlight = 0;
export async function run<T>(label: string, fn: () => Promise<T>): Promise<T | undefined> {
  // A count, not a flag: bootstrap fires three calls at once and the first
  // to finish must not declare the others done.
  inFlight += 1;
  ui.busy = true;
  try {
    const value = await fn();
    return value;
  } catch (err) {
    // A call that found the account's session dead has already ended it;
    // the account's page says so, not a toast about whichever call got
    // there first.
    if (await sessionLost()) return undefined;
    notify(t("{label}：{error}", { label, error: errorText(err) }), "error");
    return undefined;
  } finally {
    inFlight -= 1;
    ui.busy = inFlight > 0;
  }
}

// errorText is a failure in the page's words. Go answers in Chinese; a
// rejection from the PikPak API arrives as drive.APIError's text
// ("drive: POST /path: 400 name: description") and is said by its status and
// name; any other English message is a library's or the runtime's, kept for
// the console only.
function errorText(err: unknown): string {
  const raw = err instanceof Error ? err.message : String(err);
  // Go may wrap it after words of its own (「Move failed: drive: …」); English
  // wrapping is dropped with the rest.
  const api = /drive: \S+ \S+: (\d{3})(?: ([\w.-]+))?(?:: (.*))?$/s.exec(raw);
  if (api) {
    const lead = raw.slice(0, api.index);
    return (goWords(lead) ? lead : "") + apiErrorText(Number(api[1]), api[2] ?? "", api[3] ?? "");
  }
  if (goWords(raw)) return raw;
  console.warn("untranslated error:", raw);
  return t("发生了意外错误");
}

// goWords tells Go's own words, which arrive in the page's language, from a
// library's English. In English the two look alike: everything is kept.
function goWords(text: string): boolean {
  if (i18n.locale === "en") return /\S/.test(text);
  return /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]/u.test(text);
}

// Error names seen from the real server.
const API_ERROR_NAMES: Record<string, string> = {
  file_duplicated_name: N_("已有同名的文件或文件夹"),
  file_space_not_enough: N_("云盘空间不足"),
  file_in_recycle_bin: N_("这一项已经在回收站里"),
};

/** apiErrorText says a PikPak API rejection in Chinese; the server's name stays as a code for support. */
function apiErrorText(status: number, name: string, description: string): string {
  const known = API_ERROR_NAMES[name];
  if (known) return t(known);
  // The server sometimes explains itself in Chinese already.
  if (/\p{Script=Han}/u.test(description)) return description;
  if (status === 404) return t("云端找不到这一项，可能已被移动或删除");
  if (status === 429) return t("请求太频繁，请稍后再试");
  if (status >= 500) return t("云端暂时出错，请稍后再试（{status}）", { status: String(status) });
  console.warn("untranslated API error:", status, name, description);
  return t("云端没有接受这次操作（{name}）", { name: name || String(status) });
}

/* ---------------- App Lock ---------------- */

// Go holds the lock (AppLockService): while it is up, every window's calls
// into Go wait for the owner, so a page here only shows the lock screen and
// asks Go to unlock. The system's own dialog does the asking; no password
// ever passes through a page.

/** Automatically Lock After Leaving App's ready-made choices, in minutes; Custom takes 1 to MAX_APP_LOCK_DELAY. */
export const APP_LOCK_PRESETS = [0, 5, 15, 30, 60];
export const MAX_APP_LOCK_DELAY = 24 * 60;

/** lockDelayLabel says a delay in minutes the way the menu does: Immediately, After 45 Minutes, After 1 Hour 30 Minutes. */
export function lockDelayLabel(minutes: number): string {
  if (minutes <= 0) return t("立即");
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  if (!h) return t("{m} 分钟后", { m });
  return m ? t("{h} 小时 {m} 分钟后", { h, m }) : t("{h} 小时后", { h });
}

export const appLock = reactive({
  state: null as AppLockState | null,
  /** What the last request that did not go through came to, in words; "" for nothing. */
  note: "",
});

/** The lock screen is up in this window. */
export const appLocked = computed(() => !!appLock.state?.locked);

let lockListening = false;

/** watchAppLock reads how the lock stands and follows it; every window but the menu bar panel calls it first. */
export async function watchAppLock(): Promise<void> {
  if (!lockListening) {
    lockListening = true;
    Events.On(AppLockStateEvent, (event: { data: AppLockState }) => {
      const was = appLock.state?.locked;
      appLock.state = event.data;
      if (was !== event.data.locked) appLock.note = "";
    });
    // Under the lock screen nothing of the page is drawn, focused or read
    // out (style.css): the old page is replaced, not dimmed.
    watch(
      appLocked,
      (locked) => {
        if (locked) document.documentElement.dataset.locked = "";
        else delete document.documentElement.dataset.locked;
      },
      { immediate: true },
    );
  }
  try {
    appLock.state = await AppLockService.State();
  } catch (err) {
    console.warn("applock: state unread", err);
  }
}

/** What a request that had to prove the owner came to, for the line under its button. */
function lockNote(r: AppLockResult, locked: boolean): string {
  switch (r.result) {
    case "ok":
      return "";
    case "cancelled":
      return locked ? t("已取消验证，PKP 仍保持锁定") : t("已取消验证");
    case "busy":
      return t("系统身份验证已经打开，请在弹出的窗口里完成");
    case "wrong":
    case "cooldown":
      return r.message;
  }
  const reason = r.message || t("身份验证没有通过");
  return locked ? t("{reason}，PKP 仍保持锁定", { reason }) : reason;
}

/** lockCall runs one AppLockService request that may have to prove the owner; null when it threw (the note says why). */
async function lockCall(call: () => Promise<AppLockResult>): Promise<AppLockResult | null> {
  try {
    const r = await call();
    appLock.state = r.state;
    return r;
  } catch (err) {
    appLock.note = errorText(err);
    return null;
  }
}

/* ---------- Settings: the Independent Password sheet and the flows that need it ---------- */

export type LockSheetMode = "set" | "change" | "confirm";
type LockSheetInput = { current: string; next: string };

/** The Set/Change/Verify PKP Independent Password sheet (AppLockPasswordSheet). */
export const appLockSheet = reactive({
  mode: "" as "" | LockSheetMode,
  error: "",
  working: false,
  /** Change under System Authentication may also remove the backup password. */
  removable: false,
});
// What the sheet's answer goes on to do; not reactive (a function).
let sheetNext: ((input: LockSheetInput) => Promise<AppLockResult | null>) | null = null;

function openLockSheet(mode: LockSheetMode, next: (input: LockSheetInput) => Promise<AppLockResult | null>, removable = false) {
  sheetNext = next;
  Object.assign(appLockSheet, { mode, error: "", working: false, removable });
  // A password field is up: the window stays out of screenshots meanwhile.
  void AppLockService.ShieldEntry(true).catch(() => undefined);
}

export function closeLockSheet() {
  if (!appLockSheet.mode) return;
  sheetNext = null;
  appLockSheet.mode = "";
  void AppLockService.ShieldEntry(false).catch(() => undefined);
}

async function runLockSheet(step: () => Promise<AppLockResult | null>): Promise<void> {
  appLockSheet.working = true;
  appLockSheet.error = "";
  const r = await step();
  appLockSheet.working = false;
  if (!r) {
    appLockSheet.error = appLock.note;
    appLock.note = "";
    return;
  }
  if (r.result === "ok") closeLockSheet();
  // The sheet asks for the current password: a wrong one is just that.
  else appLockSheet.error = r.result === "wrong" ? t("当前密码不正确") : lockNote(r, false);
}

/** submitLockSheet hands the sheet's fields (already checked for length and repeat) to the flow that opened it. */
export function submitLockSheet(input: LockSheetInput): Promise<void> {
  const next = sheetNext;
  return next ? runLockSheet(() => next(input)) : Promise.resolve();
}

/** removeLockPassword is the sheet's Remove Password: the backup goes once its current password is proven. */
export function removeLockPassword(current: string): Promise<void> {
  return runLockSheet(() => lockCall(() => AppLockService.RemovePassword(current)));
}

/** Sets a new Independent Password, then goes on with it as the proof the next step needs. */
function thenWithNew(after: (password: string) => Promise<AppLockResult>) {
  return async ({ next }: LockSheetInput) => {
    const set = await lockCall(() => AppLockService.SetPassword("", next));
    if (!set || set.result !== "ok") return set;
    return lockCall(() => after(next));
  };
}

/** setAppLockEnabled is the App Lock switch: proven with the way in now — the system's dialog, or the Independent Password asked in the sheet. */
export async function setAppLockEnabled(on: boolean): Promise<void> {
  appLock.note = "";
  const s = appLock.state;
  if (!s) return;
  if (on && s.method === "password" && !s.hasPassword) {
    openLockSheet("set", thenWithNew((pw) => AppLockService.SetEnabled(true, pw)));
    return;
  }
  if (s.proveWith === "password") {
    openLockSheet("confirm", ({ current }) => lockCall(() => AppLockService.SetEnabled(on, current)));
    return;
  }
  const r = await lockCall(() => AppLockService.SetEnabled(on, ""));
  if (r) appLock.note = lockNote(r, false);
}

/** setAppLockMethod is Unlock Method. With the lock on, the Independent Password proves the switch to it, and the system's dialog the switch to System Authentication. */
export async function setAppLockMethod(method: "system" | "password"): Promise<void> {
  appLock.note = "";
  const s = appLock.state;
  if (!s || s.method === method) return;
  if (s.enabled && method === "password") {
    if (s.hasPassword) openLockSheet("confirm", ({ current }) => lockCall(() => AppLockService.SetMethod("password", current)));
    else openLockSheet("set", thenWithNew((pw) => AppLockService.SetMethod("password", pw)));
    return;
  }
  const r = await lockCall(() => AppLockService.SetMethod(method, ""));
  if (r) appLock.note = lockNote(r, false);
}

/** manageLockPassword is Set Password / Change Password. */
export function manageLockPassword(): void {
  appLock.note = "";
  const s = appLock.state;
  if (!s) return;
  if (s.hasPassword) {
    openLockSheet("change", ({ current, next }) => lockCall(() => AppLockService.SetPassword(current, next)), s.method === "system");
  } else {
    openLockSheet("set", ({ next }) => lockCall(() => AppLockService.SetPassword("", next)));
  }
}

/** setAppLockDelay saves Automatically Lock After Leaving App; false when Go refused it (the toast says why). */
export async function setAppLockDelay(minutes: number): Promise<boolean> {
  try {
    await AppLockService.SetDelay(minutes);
    appLock.state = await AppLockService.State();
    return true;
  } catch (err) {
    notify(t("无法保存自动锁定时间：{error}", { error: errorText(err) }), "error");
    return false;
  }
}

/** Lock PKP Now. */
export async function lockNow(): Promise<void> {
  try {
    await AppLockService.Lock();
  } catch (err) {
    appLock.note = errorText(err);
  }
}

/* ---------- the lock screen ---------- */

/** unlockApp asks the system (System Authentication). */
export async function unlockApp(): Promise<void> {
  appLock.note = "";
  const r = await lockCall(() => AppLockService.Unlock());
  if (r) appLock.note = lockNote(r, r.state.locked);
}

/** unlockWithPassword tries the Independent Password; the result says how many tries are left or how long to wait. */
export async function unlockWithPassword(password: string): Promise<AppLockResult | null> {
  appLock.note = "";
  return lockCall(() => AppLockService.UnlockWithPassword(password));
}

/** forgetAppLock is Forgot Password → Sign Out and Clear Local Data. The page starts afresh after: nothing of the old accounts stays in memory. */
export async function forgetAppLock(): Promise<string> {
  const accountIds = accounts.list.map((account) => account.id);
  try {
    await AppLockService.ForgetAndSignOut();
  } catch (err) {
    return errorText(err);
  }
  try {
    for (const id of accountIds) dropAccountSavedSearches(id);
  } catch (err) {
    return errorText(err);
  }
  location.reload();
  return "";
}

// Keys never reach the page under the lock screen: shortcuts registered on
// the window (Space plays, ⌘K searches…) would act on it unseen. This runs
// before any of them, being registered as the store loads. Keys typed into
// the lock screen itself pass; LockScreen stops them from bubbling further.
if (typeof window !== "undefined" && !inMenuBarWindow) {
  const guard = (event: KeyboardEvent) => {
    if (!appLock.state?.locked) return;
    if (event.target instanceof Element && event.target.closest("[data-lock-screen]")) return;
    event.stopImmediatePropagation();
    event.preventDefault();
  };
  for (const type of ["keydown", "keyup", "keypress"] as const) window.addEventListener(type, guard, true);
}

/* ---------------- System Notifications (Settings · Notifications) ---------------- */

// Go decides and posts every notice (NotifyService): downloads, uploads and
// cloud adds that finish or fail, Organize and Delete runs that end, an account
// that has to sign in again. The page only shows and changes the settings.

export const systemNotices = reactive({
  state: null as NotifyState | null,
  saving: false,
  /** What the last test or permission request said, "" for nothing. */
  note: "",
});

export async function loadNotifySettings(): Promise<void> {
  try {
    systemNotices.state = await NotifyService.State();
  } catch (err) {
    systemNotices.note = errorText(err);
  }
}

async function noticeCall(call: () => Promise<NotifyState>, failed: string): Promise<void> {
  systemNotices.saving = true;
  systemNotices.note = "";
  try {
    systemNotices.state = await call();
  } catch (err) {
    notify(t("{what}：{error}", { what: failed, error: errorText(err) }), "error");
    await loadNotifySettings();
  } finally {
    systemNotices.saving = false;
  }
}

/** System Notifications on or off; on asks the system once for permission. */
export function setNotifyEnabled(on: boolean): Promise<void> {
  return noticeCall(() => NotifyService.SetEnabled(on), t("无法保存系统通知设置"));
}

/** Also Notify When PKP Is in the Foreground. */
export function setNotifyForeground(on: boolean): Promise<void> {
  return noticeCall(() => NotifyService.SetForeground(on), t("无法保存系统通知设置"));
}

/** Where the system's own settings let PKP notify, as the platform words it: a key, shown through t(). */
export const notifySettingsSteps = onWindows
  ? N_("请在 Windows 设置的「系统 › 通知」里找到 PKP，打开它的通知。")
  : N_("请在系统设置的「通知」里找到 PKP，打开「允许通知」。");

/** Allow Notifications: asks the system again; macOS only asks the user the first time. */
export async function allowNotifications(): Promise<void> {
  await noticeCall(() => NotifyService.Allow(), t("无法请求通知权限"));
  if (systemNotices.state?.available && !systemNotices.state.allowed) {
    systemNotices.note = t("系统没有允许。{steps}", { steps: t(notifySettingsSteps) });
  }
}

/** Send Test Notification. */
export async function sendTestNotice(): Promise<void> {
  systemNotices.note = "";
  try {
    await NotifyService.SendTest();
    systemNotices.note = onWindows
      ? t("已发送。没看到的话，检查 Windows 设置里 PKP 的通知和「请勿打扰」。")
      : t("已发送。没看到的话，检查系统设置里 PKP 的通知样式和专注模式。");
  } catch (err) {
    systemNotices.note = errorText(err);
  }
  await loadNotifySettings();
}

/** Opens the system's notification settings. */
export function openNotifySettings(): void {
  void NotifyService.OpenSystemSettings().catch((err: unknown) =>
    notify(t("无法打开{app}：{error}", { app: settingsAppName, error: errorText(err) }), "error"),
  );
}

/* ---------------- Anonymous Usage Statistics (Settings · Privacy and Security) ---------------- */

// Go keeps per-day counts of these names and reports them once an hour
// (TelemetryService; its telemetryNames is the list that counts). Never a
// file, path, search or account: only that a feature was used.
export type TelemetryName =
  | `route.${Route}`
  | "play.video"
  | "play.audio"
  | "upload"
  | "download"
  | "cloud_add"
  | "viewer.open"
  | "scope.drive"
  | "search"
  | "share.create"
  | "assistant.ask"
  | "organize.run"
  | "organize.undo"
  | "cleanup.scan"
  | "cleanup.remove"
  | "menubar.open";

/** countUse tells Go a feature was used; it never fails the caller. */
export function countUse(name: TelemetryName): void {
  void TelemetryService.Count(name).catch(() => undefined);
}

export const telemetry = reactive({
  state: null as TelemetryState | null,
  saving: false,
});

export async function loadTelemetry(): Promise<void> {
  try {
    telemetry.state = await TelemetryService.State();
  } catch {
    telemetry.state = null;
  }
}

/** Send Anonymous Usage Statistics on or off; off also forgets this computer's id and unsent counts. */
export async function setTelemetryEnabled(on: boolean): Promise<void> {
  telemetry.saving = true;
  try {
    telemetry.state = await TelemetryService.SetEnabled(on);
  } catch (err) {
    notify(t("{what}：{error}", { what: t("无法保存使用统计设置"), error: errorText(err) }), "error");
    await loadTelemetry();
  } finally {
    telemetry.saving = false;
  }
}

/* ---------------- accounts ---------------- */

/** The account on screen, "" when this computer has none. */
export const currentId = computed(() => drive.status?.accountId ?? "");
export const currentAccount = computed(() => accounts.list.find((a) => a.id === currentId.value) ?? null);

export function accountById(id: string): Account | null {
  return accounts.list.find((a) => a.id === id) ?? null;
}

/**
 * accountLabel is what an account is called on this computer: its Local Note,
 * else the server's nickname, else its masked login. Several accounts may
 * share a label; the masked login next to it tells them apart.
 */
export function accountLabel(a: Pick<Account, "label" | "nickname" | "login"> | null | undefined): string {
  if (!a) return "";
  return a.label || a.nickname || maskLogin(a.login);
}

/** accountName is accountLabel by id; "Original Account" for one no longer here. */
export function accountName(id: string): string {
  return accountLabel(accountById(id)) || t("原账号");
}

export async function refreshStatus() {
  const status = await DriveService.Status();
  drive.status = status;
  return status;
}

// The avatar comes from a separate call: Status() is offline. The picture
// the account list kept stands in until it answers.
watch(
  () => (drive.status?.configured ? drive.status.accountId : ""),
  async (id) => {
    drive.avatar = currentAccount.value?.avatar ?? "";
    if (!id) return;
    const profile = await DriveService.Profile().catch(() => null);
    if (profile && drive.status?.accountId === id) drive.avatar = profile.picture;
  },
  { immediate: true },
);

// Every change of what the screen shows — another account, or the one on
// screen signed out — moves this on. An answer to a request made before it
// is for a screen that is gone and is dropped: a late answer never
// lands on the account now shown.
let epoch = 0;

/** accountKey names the screen a request was made for. */
function accountKey(): string {
  return `${currentId.value}#${epoch}`;
}

// Account actions of this page in flight. They put the right account on
// screen themselves; the backend's account event meanwhile only refreshes
// the list.
let ownActions = 0;
async function own<T>(fn: () => Promise<T>): Promise<T> {
  ownActions += 1;
  try {
    return await fn();
  } finally {
    ownActions -= 1;
  }
}

/**
 * loadAccounts reads the account list and the account on screen, and acts
 * on what changed behind the page's back: the account on screen whose
 * session ended loses what it showed and asks for its password in place
 * and a video whose account was signed out or removed stops.
 * A read that a switch overtook is dropped: it would put the account it
 * was answered for back on screen, and the switch reads again anyway.
 */
export async function loadAccounts() {
  const seen = epoch;
  const [list, status] = await Promise.all([DriveService.Accounts(), DriveService.Status()]).catch(() => [null, null]);
  if (!list || !status || seen !== epoch) return;
  const before = accounts.list;
  accounts.list = list;
  // An account removed and added again in this run is back.
  for (const a of list) removedAccounts.delete(a.id);
  if (player.owner && player.presentation !== "off") {
    const owner = list.find((a) => a.id === player.owner);
    if (!owner || owner.needsPassword) void ownerLost(before.find((a) => a.id === player.owner) ?? null, !owner);
  }
  if (ownActions > 0 || accounts.switching) return;
  const was = drive.status;
  if (status.accountId !== (was?.accountId ?? "")) {
    // Moved from elsewhere: show where the backend is.
    leaveScreen();
    await enterAccount(status, {});
    return;
  }
  drive.status = status;
  if (was?.configured && !status.configured && status.needsPassword) {
    // Keep where it was browsing for when it is signed back in.
    places.set(status.accountId, snapshotPlace());
    clearSession();
    notify(t("「{name}」的登录已失效，请重新登录", { name: accountName(status.accountId) }), "error");
  }
}

/**
 * sessionLost tells, after a failed call, whether the failure was the
 * session of the account on screen ending; loadAccounts then shows it.
 */
async function sessionLost(): Promise<boolean> {
  const was = drive.status;
  const status = await DriveService.Status().catch(() => null);
  if (!status || !was || status.accountId !== was.accountId || status.configured || !status.needsPassword) return false;
  await loadAccounts();
  return true;
}

/** ownerLost stops a video whose account was signed out or removed. */
async function ownerLost(owner: Account | null, removed: boolean) {
  if (!player.owner || player.presentation === "off") return;
  player.owner = "";
  const name = accountLabel(owner) || t("原账号");
  await stopPlayback();
  notify(
    removed
      ? t("已退出「{name}」，它的视频已停止播放", { name })
      : t("「{name}」的登录已失效，播放已停止；重新登录后可从「继续播放」恢复", { name }),
    "error",
  );
}

/* ---------- where each account was browsing ---------- */

/** The file page's place in its list: the card at the top and how far down it sat. */
export interface FilesAnchor {
  id: string;
  offset: number;
  top: number;
}

/** FilesView answers for the list's scroll: what it shows now, and putting a place back. */
export const filesPlace = {
  save: (): FilesAnchor | null => null,
  restore: (_anchor: FilesAnchor | null): void => {},
  /** Scrolls the entry's card or row into view, once drawn. */
  reveal: (_id: string): void => {},
};

interface Place {
  view: LibraryView;
  trail: Crumb[];
  query: string;
  category: CategoryFilter;
  scope: FilesScope;
  anchor: FilesAnchor | null;
}

const places = new Map<string, Place>();
const fileTrails = new Map<string, Crumb[]>();
let pendingAnchor: FilesAnchor | null = null;
// Set while a place is put back: the view's own watchers would reload it
// a second time.
let restoring = false;
// Where each Browse Scope was scrolled to when the user left it: going back
// shows the same folder at the same place.
const scopeAnchors: Record<FilesScope, FilesAnchor | null> = { folder: null, drive: null };

function snapshotPlace(): Place {
  return {
    view: ui.view,
    trail: drive.trail.map((c) => ({ ...c })),
    query: ui.query,
    category: ui.category,
    scope: ui.scope,
    anchor: filesPlace.save(),
  };
}

function restorePlace(id: string, fresh: boolean) {
  const place = fresh ? undefined : places.get(id);
  restoring = true;
  ui.view = place?.view ?? "all";
  drive.trail = place?.trail ?? [];
  ui.query = place?.query ?? "";
  ui.category = place?.category ?? "all";
  ui.scope = place?.scope ?? "folder";
  scopeAnchors.folder = scopeAnchors.drive = null;
  ui.route = "files";
  pendingAnchor = place?.anchor ?? null;
  void nextTick(() => (restoring = false));
}

/* ---------- switching ---------- */

// Switches reach the backend one after another, in the order asked, so the
// last one asked is also the one on screen there.
let switchChain: Promise<unknown> = Promise.resolve();
let switchSerial = 0;

/**
 * leaveScreen keeps where the account on screen was browsing, while it
 * shows anything, and drops all it showed.
 */
function leaveScreen() {
  const from = currentId.value;
  if (from && drive.status?.configured && !accounts.switching) places.set(from, snapshotPlace());
  clearSession();
}

/**
 * enterAccount shows the account status names: where it was browsing, or
 * its root when `fresh` or new to this run, then its data.
 */
async function enterAccount(status: DriveStatus, opts: { fresh?: boolean; message?: string; tone?: "info" | "success" | "error" }) {
  drive.status = status;
  accounts.switching = "";
  restorePlace(status.accountId, !!opts.fresh);
  await loadAccounts();
  if (opts.message) notify(opts.message, opts.tone ?? "success");
  if (status.configured) await loadAccountData();
}

/** loadAccountData fills the screen of the account on screen. */
async function loadAccountData() {
  const anchor = pendingAnchor;
  pendingAnchor = null;
  void loadTasks();
  void loadPlays();
  await Promise.all([reload(true), loadQuota(), loadIndexProgress()]);
  await nextTick();
  filesPlace.restore(anchor);
}

/**
 * switchAccount puts another account on screen. What the leaving account
 * has not submitted is settled first — stay, or discard it — and a video of
 * it is paused with its position kept unless Keep Playing When Switching Accounts is on. The
 * page then drops what it showed and shows the other account's own place.
 * Of several quick switches only the last lands: the others' answers are
 * dropped. Nothing of the leaving account's uploads or index stops.
 */
export async function switchAccount(id: string): Promise<boolean> {
  ui.accountsOpen = false;
  if (!id || id === (accounts.switching || currentId.value)) return true;
  if (!(await leaveUnsubmitted(id))) return false;
  return own(async () => {
    const paused = await settlePlayback(id);
    leaveScreen();
    accounts.switching = id;
    const serial = ++switchSerial;
    const call = switchChain.then(() => DriveService.SwitchAccount(id));
    switchChain = call.catch(() => undefined);
    let status: DriveStatus;
    try {
      status = await call;
    } catch (err) {
      if (serial !== switchSerial) return false;
      const back = await DriveService.Status().catch(() => null);
      if (back) await enterAccount(back, { message: t("没有切换到「{name}」：{error}", { name: accountName(id), error: errorText(err) }), tone: "error" });
      return false;
    }
    if (serial !== switchSerial) return false;
    const name = accountLabel(accounts.list.find((a) => a.id === status.accountId));
    const switched = status.needsPassword ? t("已切换到「{name}」 · 需要重新登录", { name }) : t("已切换到「{name}」", { name });
    const message = [switched, paused].filter(Boolean).join(" · ");
    await enterAccount(status, { message, tone: status.needsPassword ? "info" : "success" });
    return true;
  });
}

/* ---------- unsubmitted sheets ---------- */

/**
 * The cloud-add sheet: the link draft, and the torrent picked on the
 * Torrent tab with the files chosen from it. The draft survives closing the
 * sheet, so losing a pasted batch to a stray click is not possible; the
 * torrent survives with it for the same reason.
 */
export const cloudAdd = reactive({
  draft: "",
  tab: "link" as "link" | "torrent",
  torrent: null as TorrentMeta | null,
  // The indices of cloudAdd.torrent.files to fetch; a fresh torrent starts
  // with all of them.
  picked: new Set<number>(),
  reading: false,
  // Save to: the folder the tasks land in, [] for the server's own download
  // folder (drive.CreateOfflineTask without a parent). The sheet fills it
  // with the page's folder when it opens with nothing pending.
  dest: [] as Crumb[],
});

/** What the account on screen has started and not submitted, "" for nothing. */
function unsubmitted(): string {
  if (savedHistoryDirty.value) return t("未保存的已保存历史更改");
  if (syncDraft.pending) return t("尚未确认的文件夹同步");
  if (cloudAdd.draft.trim()) return t("云添加的链接");
  if (cloudAdd.torrent) return t("云添加的种子");
  if (shares.targets.length && !shares.created) return t("创建中的分享");
  if (ui.newFolderOpen) return t("新建文件夹");
  const ai = assistantPending();
  if (ai) return ai;
  if (cleanupPending()) return t("清理页改过的勾选");
  return "";
}

/**
 * ask shows one of the account questions and resolves with the answer;
 * closing it is No. (The executor form: the project's TypeScript 4.9 has no
 * typings for Promise.withResolvers, and macOS 12's WebKit may lack it.)
 */
type Question = { kind: "leave"; target: string; what: string } | { kind: "replace"; owner: string };
function ask(question: Question): Promise<boolean> {
  return new Promise((resolve) => {
    const answer = (go: boolean) => {
      accounts.dialog = null;
      resolve(go);
    };
    accounts.dialog = { ...question, resolve: answer };
  });
}

/**
 * leaveUnsubmitted asks, when the account on screen has something not yet
 * submitted, whether to stay or to discard it: it is never carried over and
 * done in another account. target is the account asked for, "" a new one.
 */
async function leaveUnsubmitted(target: string): Promise<boolean> {
  if (!(await waitSavedHistoryLeave())) return false;
  if (syncDraft.busy) {
    notify(t("正在确认同步关系，请稍后再切换账号"), "info");
    return false;
  }
  const what = unsubmitted();
  if (!what) return true;
  const go = await ask({ kind: "leave", target, what });
  if (go) {
    try {
      await syncDraft.discard?.();
    } catch {
      return false;
    }
    cloudAdd.draft = "";
    clearTorrent();
    ui.cloudAddOpen = false;
    closeCreateShare();
    ui.newFolderOpen = false;
    resetAssistant();
    resetCleanup();
  }
  return go;
}

/* ---------- playback across accounts ---------- */

/** The account of the video playing, for the bar, the player and the separate window. */
export const playingOwner = computed<Account | null>(() => (inPlayerWindow ? player.ownerInfo : accountById(player.owner)));

/** Whether the video playing belongs to another account than the one on screen. */
export const playingElsewhere = computed(
  () => !inPlayerWindow && player.presentation !== "off" && !!player.owner && player.owner !== currentId.value,
);

/**
 * settlePlayback applies the playback preference to a switch towards
 * target. Off — the default — another account's video is paused for good:
 * its position goes into its own account's record and the player is put
 * away, so the new account shows none of it and the old one offers it under
 * Continue Playing. It returns what to say about that, "" when nothing changed.
 */
async function settlePlayback(target: string): Promise<string> {
  if (inPlayerWindow || player.presentation === "off" || !player.owner || player.owner === target || accounts.keepPlaying) return "";
  return pauseOwner();
}

/** pauseOwner stops the video playing and says whether its position was kept. */
async function pauseOwner(): Promise<string> {
  const name = accountName(player.owner);
  let saved: boolean | null;
  try {
    saved = await DriveService.SavePlayback();
  } catch {
    saved = null;
  }
  await stopPlayback();
  // Never Saved for a position that is not on disk.
  if (saved === null) return t("已暂停「{name}」的播放，但进度没能保存到本机", { name });
  return saved ? t("已暂停「{name}」的播放，进度已保存", { name }) : t("已暂停「{name}」的播放", { name });
}

/**
 * askReplace asks before a video of the account on screen replaces another
 * account's video that is playing: one session, one sound.
 */
function askReplace(owner: string): Promise<boolean> {
  return ask({ kind: "replace", owner });
}

/** returnToOwner is Return to Owning Account on the bar of another account's video. */
export function returnToOwner() {
  if (player.owner) void switchAccount(player.owner);
}

/**
 * setKeepPlaying saves Keep Playing When Switching Accounts. Turning it off while another
 * account's video plays pauses that video at once and keeps its position.
 */
export async function setKeepPlaying(on: boolean) {
  if (accounts.keepPlaying === on) return;
  accounts.keepPlaying = on;
  const ok = await run(t("保存播放偏好"), async () => {
    await DriveService.SetKeepPlayingOnSwitch(on);
    return true;
  });
  if (!ok) {
    accounts.keepPlaying = !on;
    return;
  }
  if (!on && playingElsewhere.value) {
    notify(await pauseOwner(), "info");
    return;
  }
  notify(on ? t("切换账号时继续播放 · 播放器会一直标明视频所属账号") : t("切换账号时暂停播放并保存进度"), "success");
}

/* ---------- signing in, renaming, removing ---------- */

/**
 * openSignIn opens Add Account (account "") or Sign In Again of an account. Either
 * ends with that account on screen, so what the account on screen has not
 * submitted is settled first.
 */
export async function openSignIn(account = "") {
  ui.accountsOpen = false;
  if (account !== currentId.value && !(await leaveUnsubmitted(account))) return;
  accounts.dialog = { kind: "signin", account };
}

/** closeAccountDialog closes the dialog without an answer; a question counts as No. */
export function closeAccountDialog() {
  const d = accounts.dialog;
  if (d && (d.kind === "leave" || d.kind === "replace")) d.resolve(false);
  else accounts.dialog = null;
}

/** PikPak's own sign-up page (with PKP's referral), opened in the browser: PKP has no sign-up form. */
const SIGN_UP_URL = "https://mypikpak.com/drive/login?affiliate=342642";

/** openSignUp is the sign-in page's Sign Up: PikPak's web page in the system browser. */
export function openSignUp(): void {
  void Browser.OpenURL(SIGN_UP_URL).catch((err: unknown) => notify(t("无法打开注册网页：{error}", { error: errorText(err) }), "error"));
}

/**
 * submitSignIn sends a sign-in form: the sign-in page's, Add Account or Sign In Again.
 * A refusal comes back for the form to show, changing nothing. Success puts
 * the account on screen: a new one at its root, one already here where it
 * was browsing.
 */
export async function submitSignIn(req: SignInRequest): Promise<SignInResult> {
  return own(async () => {
    const from = currentId.value;
    let res: SignInResult;
    try {
      res = await DriveService.SignIn(req);
    } catch (err) {
      return { failure: "other", message: errorText(err) } as SignInResult;
    }
    if (res.failure) return res;
    // The form is done once the server said yes; the account's files come in behind it.
    if (accounts.dialog?.kind === "signin") accounts.dialog = null;
    const id = res.status.accountId;
    const paused = id !== from ? await settlePlayback(id) : "";
    const name = accountLabel(res.account);
    if (id === from) {
      // Signing In Again to the account on screen — or adding it again — comes
      // back where it was browsing.
      leaveScreen();
      await enterAccount(res.status, { message: t("「{name}」已重新登录", { name }) });
      return res;
    }
    leaveScreen();
    accounts.switching = id;
    const message = [res.existed ? t("已切换到「{name}」", { name }) : t("已添加并切换到「{name}」", { name }), paused].filter(Boolean).join(" · ");
    await enterAccount(res.status, { fresh: !res.existed, message });
    return res;
  });
}

/** cancelSignIn calls off a sign-in in flight: a late success is not kept. */
export async function cancelSignIn() {
  await DriveService.CancelSignIn().catch(() => undefined);
}

/** renameAccount sets an account's Local Note. */
export async function renameAccount(id: string, label: string): Promise<boolean> {
  const account = await run(t("修改备注"), () => DriveService.RenameAccount(id, label));
  if (!account) return false;
  await loadAccounts();
  notify(t("本机备注已更新"), "success");
  return true;
}

/**
 * removeAccount is Sign Out and Remove: the account's local uploads and downloads stop
 * and it is forgotten on this computer (its unfinished downloads go, finished
 * files stay); its cloud tasks, files and shares are not
 * touched. Removing the account on screen puts the most recently used
 * signed-in account there, or the sign-in page after the last one.
 */
export async function removeAccount(id: string): Promise<boolean> {
  return own(async () => {
    const name = accountName(id);
    const onScreen = id === currentId.value;
    if (player.owner === id && player.presentation !== "off") {
      player.owner = "";
      await stopPlayback();
    }
    let status: DriveStatus;
    try {
      status = await DriveService.RemoveAccount(id);
    } catch (err) {
      // Not removed: nothing changed, and the dialog says so.
      notify(t("「{name}」没有移除：{error}", { name, error: errorText(err) }), "error");
      await loadAccounts();
      return false;
    }
    removedAccounts.add(id);
    let cleanupError = "";
    try {
      dropAccountSavedSearches(id);
      dropAccountMediaLibrary(id);
    } catch (err) {
      cleanupError = errorText(err);
    }
    places.delete(id);
    fileTrails.delete(id);
    delete transfers.cloud[id];
    delete memberships[id];
    drive.uploads = drive.uploads.filter((t) => t.account !== id);
    drive.downloads = drive.downloads.filter((t) => t.account !== id);
    if (!onScreen) {
      await loadAccounts();
      notify(cleanupError || t("已退出并移除「{name}」", { name }), cleanupError ? "error" : "success");
      return true;
    }
    clearSession();
    if (!status.accountId) {
      drive.status = status;
      await loadAccounts();
      notify(cleanupError || t("已退出并移除「{name}」", { name }), cleanupError ? "error" : "success");
      return true;
    }
    const next = accountLabel(((await DriveService.Accounts().catch(() => null)) ?? []).find((a) => a.id === status.accountId));
    await enterAccount(status, { message: t("已退出并移除「{name}」 · 已切换到「{next}」", { name, next }) });
    if (cleanupError) notify(cleanupError, "error");
    return true;
  });
}

/* ---------- what an account shows ---------- */

/**
 * clearSession drops everything the screen showed of an account: files,
 * picks, details, storage, plays, shares, connections, previews, the
 * Cloud Organization plan — and the search with them. Requests still out for it are
 * answered into the void.
 */
function clearSession() {
  savedHistoryCancel?.();
  savedHistoryCancel = null;
  savedHistoryNext = null;
  savedHistory.dialog = null;
  detachSavedHistory();
  epoch += 1;
  listSerial += 1;
  drive.entries = [];
  drive.total = -1;
  drive.offset = 0;
  drive.live = false;
  drive.nextPageToken = "";
  drive.listLoading = false;
  drive.loadingMore = false;
  drive.indexProgress = null;
  drive.quota = null;
  drive.quotaAt = "";
  drive.quotaError = "";
  drive.plays = [];
  drive.playsLoaded = false;
  playsSerial += 1;
  historySerial += 1;
  historyLoad = null;
  historyCloudAt = 0;
  Object.assign(playHistory, { account: "", list: [], loading: false, localLoading: false, syncing: false, loaded: false, pages: 0, error: "", clearing: false });
  drive.lastBatch = [];
  drive.trail = [];
  forgetDetail();
  drive.picked = [];
  localSearch.range = null;
  localSearch.force = false;
  localSerial += 1;
  localSearch.result = null;
  localSearch.loading = false;
  localSearch.error = "";
  localSearch.picked = [];
  drive.pickAnchor = "";
  restoring = true;
  ui.query = "";
  ui.shareQuery = "";
  ui.transferQuery = "";
  ui.playQuery = "";
  ui.category = "all";
  ui.videoLayout = "files";
  ui.scope = "folder";
  scopeAnchors.folder = scopeAnchors.drive = null;
  ui.view = "all";
  void nextTick(() => (restoring = false));
  ui.panelTab = "overview";
  ui.workspace = "files";
  ui.cloudAddOpen = false;
  ui.newFolderOpen = false;
  if (ui.route === "player") player.returnRoute = "files";
  else ui.route = "files";
  shares.list = [];
  shares.nextPageToken = "";
  shares.selectedId = "";
  closeCreateShare();
  // The sheet is the account's; an extraction it started is still followed.
  closeExtract();
  connections.webdav = null;
  connections.apps = [];
  connections.quota = null;
  connections.prefs = null;
  connections.selectedClient = "";
  closeViewer();
  closeRelocate();
  resetAssistant();
  resetCleanup();
  syncDraft.discard?.();
}

/**
 * loadQuota reads the account's storage for the Overview. It is not a toast when
 * it fails: the chart keeps the last good figures, says how old they are and
 * offers Retry, or, with nothing to keep, says the figure is unavailable.
 */
export async function loadQuota() {
  const account = accountKey();
  try {
    const quota = await DriveService.Quota();
    if (account !== accountKey()) return;
    drive.quota = quota;
    drive.quotaAt = new Date().toISOString();
    drive.quotaError = "";
  } catch (err) {
    if ((await sessionLost()) || account !== accountKey()) return;
    drive.quotaError = errorText(err);
  }
}

/** One account's membership as last read: absent until asked, `error` when the read failed. */
export interface MembershipRead {
  value: Membership | null;
  loading: boolean;
  error: string;
}

/**
 * Memberships per account. They change on purchase or expiry, not while
 * browsing, so each is read once and again only when a page that shows it
 * opens (Settings · Account Management); a failed read keeps the last good one.
 */
export const memberships = reactive<Record<string, MembershipRead>>({});

export async function loadMembership(account = currentId.value) {
  if (!account) return;
  // Read back through the reactive record: the object just assigned is the
  // raw one, and writes to it would not reach the page.
  memberships[account] ??= { value: null, loading: false, error: "" };
  const read = memberships[account];
  if (read.loading) return;
  read.loading = true;
  try {
    const value = await DriveService.Membership(account);
    if (memberships[account] !== read) return;
    read.value = value;
    read.error = "";
  } catch (err) {
    if (memberships[account] === read) read.error = errorText(err);
  } finally {
    read.loading = false;
  }
}

// Storage moves when uploads land, files are deleted for good or the index
// finishes a pass (which is also when the split by kind can be checked);
// a burst of those is one refresh.
let quotaTimer = 0;
function refreshQuotaSoon() {
  window.clearTimeout(quotaTimer);
  quotaTimer = window.setTimeout(() => void loadQuota(), 1500);
}

/**
 * loadPlays reads Continue Playing: recent plays, newest first, merged by the backend
 * from the account's cloud history and PKP's own records. A failure leaves
 * what was there.
 */
let playsSerial = 0;
export async function loadPlays() {
  if (playHistory.clearing) return;
  const account = accountKey();
  const serial = ++playsSerial;
  try {
    const plays = await DriveService.RecentPlays(2);
    if (account !== accountKey() || serial !== playsSerial) return;
    drive.plays = plays ?? [];
    drive.playsLoaded = true;
  } catch {
    // Not a toast: the Overview keeps its last list, or its empty state.
    if (await sessionLost()) return;
  }
}

export const playHistory = reactive({
  account: "",
  list: [] as RecentPlay[],
  loading: false,
  localLoading: false,
  syncing: false,
  loaded: false,
  pages: 0,
  error: "",
  clearing: false,
});
let historySerial = 0;
let historyCloudAt = 0;
let historyLoad: { account: string; promise: Promise<void> } | null = null;

export function openHistory(): void {
  if (!leaveSavedHistory(openHistory)) return;
  ui.route = "history";
}

/** Newer progress wins; a cloud-only row never strips the local-record marker. */
function mergeHistory(previous: readonly RecentPlay[], incoming: readonly RecentPlay[]): RecentPlay[] {
  const records = new Map(previous.map((record) => [record.fileId, record]));
  for (const record of incoming) {
    const old = records.get(record.fileId);
    if (!old) { records.set(record.fileId, record); continue; }
    const latest = (Date.parse(record.playedAt) || 0) >= (Date.parse(old.playedAt) || 0) ? record : old;
    records.set(record.fileId, { ...latest, local: old.local || record.local });
  }
  return [...records.values()].sort((a, b) => ((Date.parse(b.playedAt) || 0) - (Date.parse(a.playedAt) || 0)) || a.fileId.localeCompare(b.fileId));
}

/** Automatic opens refresh local progress but reuse cloud results for one minute. */
export function loadPlayHistory(refresh = true): Promise<void> {
  if (playHistory.clearing || !currentId.value) return Promise.resolve();
  const account = accountKey();
  if (historyLoad?.account === account) return historyLoad.promise;
  const owner = currentId.value;
  if (playHistory.account !== owner) {
    Object.assign(playHistory, { account: owner, list: [], loaded: false, pages: 0 });
    historyCloudAt = 0;
  }
  const cloud = refresh || !historyCloudAt || Date.now() - historyCloudAt >= 60_000 || !!playHistory.error;
  const serial = ++historySerial;
  const promise = readPlayHistory(owner, account, serial, cloud);
  historyLoad = { account, promise };
  void promise.finally(() => { if (historyLoad?.promise === promise) historyLoad = null; });
  return promise;
}

async function readPlayHistory(owner: string, account: string, serial: number, cloud: boolean): Promise<void> {
  const current = () => account === accountKey() && serial === historySerial;
  Object.assign(playHistory, { loading: true, localLoading: true, syncing: cloud, error: "", ...(cloud ? { pages: 0 } : {}) });
  let local: RecentPlay[] = [];
  let records: RecentPlay[] = [];
  let receivedPage = false;
  const unavailable = new Set<string>();
  const localRead = DriveService.LocalPlayHistory(owner).then((rows) => {
    if (!current()) return;
    local = rows ?? [];
    local = local.map((record) => unavailable.has(record.fileId) ? { ...record, available: false } : record);
    playHistory.list = mergeHistory(receivedPage ? records : playHistory.list.filter((record) => !record.local), local);
    playHistory.loaded = true;
  }, (err: unknown) => {
    if (current() && !playHistory.pages) playHistory.error = errorText(err);
  }).finally(() => { if (current()) playHistory.localLoading = false; });
  const cloudRead = async () => {
    if (!cloud) return;
    let token = "";
    const seen = new Set<string>();
    try {
      for (let page = 0; page < 4; page++) {
        const result = await DriveService.PlayHistoryPage(owner, token);
        if (!current()) return;
        if (result.account !== owner) throw new Error(t("播放记录所属账号不一致，请重试"));
        for (const id of result.unavailableIds ?? []) unavailable.add(id);
        local = local.map((record) => unavailable.has(record.fileId) ? { ...record, available: false } : record);
        const visible = (result.plays ?? []).filter((record) => !unavailable.has(record.fileId) || record.local)
          .map((record) => unavailable.has(record.fileId) ? { ...record, available: false } : record);
        records = mergeHistory(records.filter((record) => !unavailable.has(record.fileId) || record.local), visible);
        receivedPage = true;
        playHistory.list = mergeHistory(records, local);
        playHistory.list = playHistory.list.filter((record) => !unavailable.has(record.fileId) || record.local)
          .map((record) => unavailable.has(record.fileId) ? { ...record, available: false } : record);
        playHistory.loaded = true;
        playHistory.error = "";
        playHistory.pages = page + 1;
        const next = result.nextPageToken;
        if (!next || next === token || seen.has(next)) break;
        seen.add(token);
        token = next;
      }
      if (current()) historyCloudAt = Date.now();
    } catch (err) {
      if (!current() || await sessionLost() || !current()) return;
      historyCloudAt = 0;
      playHistory.error = errorText(err);
    } finally {
      if (current()) playHistory.syncing = false;
    }
  };
  await Promise.all([localRead, cloudRead()]);
  if (current()) playHistory.loading = false;
}

export async function clearPlayHistory(): Promise<boolean> {
  if (playHistory.clearing) return false;
  const account = accountKey();
  playHistory.clearing = true;
  historySerial += 1;
  historyLoad = null;
  historyCloudAt = 0;
  playsSerial += 1;
  playHistory.loading = false;
  playHistory.localLoading = false;
  playHistory.syncing = false;
  try {
    const ok = await run(t("清除播放记录"), async () => {
      await DriveService.ClearPlays();
      return true;
    });
    if (!ok || account !== accountKey()) return false;
    playHistory.list = [];
    playHistory.loaded = true;
    playHistory.error = "";
    playHistory.account = currentId.value;
    playHistory.pages = 0;
    historyCloudAt = Date.now();
    drive.plays = [];
    drive.playsLoaded = true;
    notify(t("已清除播放记录"), "success");
    return true;
  } finally {
    if (account === accountKey()) playHistory.clearing = false;
  }
}

/**
 * resumePlay starts a Continue Playing entry where it was left. It is an explicit
 * play, so it goes through play() like any other: the session's queue
 * becomes this one file. A record whose file is gone says so and offers to
 * be removed instead of failing on the play.
 */
export async function resumePlay(record: RecentPlay) {
  if (!record.available) return;
  const account = accountKey();
  const detail = await run(t("继续播放"), () => DriveService.FileDetail(record.fileId));
  if (!detail || account !== accountKey()) return;
  if (!detail.playable) {
    notify(t("「{name}」现在无法播放", { name: record.name }), "error");
    return;
  }
  await play(detail, [detail], playFinished(record.position, record.duration) ? 0 : record.position);
}

/** forgetPlay removes PKP's own record of a play whose file is gone. */
export async function forgetPlay(record: RecentPlay) {
  const account = accountKey();
  const ok = await run(t("移除记录"), async () => {
    await DriveService.ForgetPlay(record.fileId);
    return true;
  });
  if (!ok || account !== accountKey()) return;
  drive.plays = drive.plays.filter((p) => p.fileId !== record.fileId);
  historySerial += 1;
  playsSerial += 1;
  historyLoad = null;
  historyCloudAt = 0;
  playHistory.loading = false;
  playHistory.localLoading = false;
  playHistory.syncing = false;
  playHistory.list = playHistory.list.filter((p) => p.fileId !== record.fileId);
  if (ui.route === "history") void loadPlayHistory();
  void loadPlays();
}

/* ---------------- listing ---------------- */

/** Whether the local index can answer listings; false until a full walk completes. */
export const indexReady = computed(() => drive.indexProgress?.ready === true);

/**
 * indexText describes the index for the status pill and Settings · Local Index. A
 * full walk says how far it is: a share when an earlier index gives the
 * drive's size, else the month it has read up to (the walk goes oldest first).
 */
export function indexText(p: IndexProgress | null): string {
  if (!p) return t("未读取");
  if (p.phase === "full") {
    if (p.percent > 0) return t("正在建立索引 · {n} 项 · {percent}%", { n: p.done, percent: p.percent });
    const at = p.reached ? new Date(p.reached) : null;
    if (!at || Number.isNaN(at.getTime())) return t("正在建立索引 · {n} 项", { n: p.done });
    return t("正在建立索引 · {n} 项 · 已到 {year} 年 {month} 月", { n: p.done, year: String(at.getFullYear()), month: at.getMonth() + 1 });
  }
  if (p.phase === "incremental") return t("正在同步");
  if (!p.ready) return t("尚未建立");
  const at = p.syncedAt ? new Date(p.syncedAt) : null;
  if (!at || Number.isNaN(at.getTime())) return t("{n} 项 · 已就绪", { n: p.count });
  return t("{n} 项 · {time} 同步", { n: p.count, time: `${at.getHours()}:${String(at.getMinutes()).padStart(2, "0")}` });
}

/** Relative dates are resolved again when a saved search is opened or refreshed. */
const searchRevision = reactive({ value: 0 });
export const searchParse = computed<ParsedSearch>(() => {
  void searchRevision.value;
  return parseSearch(ui.query, new Date());
});

/**
 * What List searches for: the box as typed while it holds only words, so a
 * plain search is exactly what it always was; with conditions, only its
 * words (the conditions are FilesSearch's, from the index).
 */
const listQuery = computed(() => (searchParse.value.filtered ? searchParse.value.keywords.join(" ") : ui.query.trim()));

/**
 * listRequest describes the current view for the backend. A query turns the
 * view into a whole-drive search, but only once the index can answer it:
 * before that the folder (or, scoped to the drive, the tree) is fetched as
 * usual and `visible` filters it.
 */
function listRequest(offset: number, pageToken: string): ListRequest {
  const query = listQuery.value;
  return {
    view: query && indexReady.value ? "search" : ui.view === "all" && ui.scope === "drive" ? "files" : ui.view,
    parentId: drive.trail.at(-1)?.id ?? "",
    query,
    category: ui.category,
    sort: ui.sortKey,
    asc: ui.sortAsc,
    offset,
    pageToken,
  };
}

/** Whether All Files lays out every file of the drive, rather than a folder or search results. */
export const wholeDrive = computed(() => ui.view === "all" && ui.scope === "drive" && !(listQuery.value && indexReady.value));

/** setScope switches All Files between the folder on screen and every file of the drive. */
export async function setScope(scope: FilesScope) {
  if (ui.scope === scope) return;
  scopeAnchors[ui.scope] = filesPlace.save();
  ui.scope = scope;
  if (scope === "drive") countUse("scope.drive");
  clearSelection();
  await reload(true);
  filesPlace.restore(scopeAnchors[scope]);
}

let listSerial = 0;

/**
 * reload fetches the first page of the current view. `fresh` is for
 * navigation: the old folder's rows are dropped first so a skeleton stands in,
 * rather than the previous contents sitting there until the answer lands.
 * A plain refresh keeps the rows and only dims them.
 */
export async function reload(fresh = false) {
  const serial = ++listSerial;
  if (fresh) {
    drive.entries = [];
    drive.nextPageToken = "";
    drive.total = -1;
    drive.offset = 0;
  }
  drive.listLoading = true;
  try {
    const page = await run(t("加载列表"), () => DriveService.List(listRequest(0, "")));
    // A slower answer for a folder the user already left must not replace
    // the one they are looking at.
    if (serial !== listSerial) return;
    if (!page) return;
    drive.entries = page.entries ?? [];
    drive.total = page.total;
    drive.offset = page.offset + drive.entries.length;
    drive.live = page.live;
    drive.nextPageToken = page.nextPageToken;
  } finally {
    if (serial === listSerial) drive.listLoading = false;
  }
}

/** Whether another page exists for the current view. */
export const hasMore = computed(() => (drive.live ? !!drive.nextPageToken : drive.offset < drive.total));

export async function loadMore() {
  if (!hasMore.value || drive.loadingMore) return;
  const serial = listSerial;
  drive.loadingMore = true;
  try {
    const page = await run(t("加载列表"), () => DriveService.List(listRequest(drive.offset, drive.nextPageToken)));
    if (serial !== listSerial || !page) return;
    const rows = page.entries ?? [];
    drive.entries = [...drive.entries, ...rows];
    drive.total = page.total;
    drive.offset = page.offset + rows.length;
    drive.nextPageToken = page.nextPageToken;
  } finally {
    drive.loadingMore = false;
  }
}

/**
 * removeEntries drops rows the user just acted on without refetching. The
 * offset and total follow so the next page still lines up with the index.
 */
export function removeEntries(ids: string[]) {
  const gone = new Set(ids);
  const before = drive.entries.length;
  drive.entries = drive.entries.filter((e) => !gone.has(e.id));
  const removed = before - drive.entries.length;
  if (!drive.live && removed > 0) {
    drive.offset -= removed;
    drive.total -= removed;
  }
}

/**
 * refresh is the toolbar button: pull the server's changes into the index,
 * then show them. The list reloads at once for the live case; the indexed
 * case reloads again when the pass finishes (see the progress listener).
 */
let refreshPending = false;
export async function refresh() {
  if (indexReady.value) {
    refreshPending = true;
    try {
      await DriveService.RequestSync("");
    } catch {
      refreshPending = false;
    }
  }
  if (videoWorksShown.value) {
    await loadMediaLibrary(currentId.value);
    return;
  }
  await reload(false);
  if (localSearchShown.value) await runLocalSearch();
}

// A file openFolderAt was sent to that the index did not have yet, and the
// folder it is in: picked once the pass that brings it in has landed.
let pendingPick = { folder: "", id: "" };

function pickPending() {
  const { folder, id } = pendingPick;
  pendingPick = { folder: "", id: "" };
  // The user moved on meanwhile: their page is left alone.
  if (!id || ui.route !== "files" || ui.view !== "all" || ui.scope !== "folder" || (drive.trail.at(-1)?.id ?? "") !== folder) return;
  const entry = drive.entries.find((e) => e.id === id);
  if (entry) {
    select(entry);
    filesPlace.reveal(entry.id);
  }
}

export async function rebuildIndex() {
  const ok = await run(t("重建索引"), async () => {
    await DriveService.RebuildIndex();
    return true;
  });
  if (ok) notify(t("正在重建本地索引，期间列表直接读取服务器"), "info");
}

/** Seeds the index state on start; the caller is already loading the list. */
export async function loadIndexProgress() {
  const account = accountKey();
  const progress = await DriveService.IndexProgress().catch(() => null);
  if (!progress || account !== accountKey()) return;
  drive.indexProgress = progress;
  wasReady = progress.ready;
  lastPhase = "";
}

let wasReady = false;
let lastPhase = "";
function applyIndexProgress(progress: IndexProgress) {
  // Every signed-in account keeps its index current; the page follows the
  // one on screen.
  if (progress.account !== currentId.value || !drive.status?.configured) return;
  drive.indexProgress = progress;
  const becameReady = progress.ready && !wasReady;
  wasReady = progress.ready;
  // A pass that just finished is when the split of the storage by kind can
  // be checked against the account's total again.
  const passDone = progress.ready && progress.phase === "idle" && lastPhase !== "" && lastPhase !== "idle";
  lastPhase = progress.phase;
  if (passDone) refreshQuotaSoon();
  if (becameReady) {
    // First full walk done: swap the live page for the indexed one.
    void reload(false);
  } else if (progress.phase === "idle" && refreshPending && progress.ready) {
    refreshPending = false;
    void reload(false).then(pickPending);
  }
}

export async function openView(view: LibraryView) {
  const fromSaved = !!savedHistory.id || localSearch.force;
  if (!leaveSavedHistory(() => void openView(view))) return;
  if (view === ui.view && !fromSaved) {
    ui.route = "files";
    return;
  }
  if (ui.view === "all") fileTrails.set(currentId.value, drive.trail.map((c) => ({ ...c })));
  ui.view = view;
  ui.route = "files";
  ui.query = "";
  localSearch.force = false;
  localSearch.range = null;
  drive.trail = view === "all" ? (fileTrails.get(currentId.value) ?? []).map((c) => ({ ...c })) : [];
  clearSelection();
  await reload(true);
}

export async function openFolder(entry: Entry) {
  if (!leaveSavedHistory(() => void openFolder(entry))) return;
  if (ui.view === "starred") {
    await openFolderAt(entry.id, "");
    return;
  }
  drive.trail.push({ id: entry.id, name: entry.name });
  ui.query = "";
  clearSelection();
  await reload(true);
}

export async function goToCrumb(index: number) {
  if (!leaveSavedHistory(() => void goToCrumb(index))) return;
  drive.trail = drive.trail.slice(0, index);
  ui.query = "";
  clearSelection();
  await reload(true);
}

// Inside a folder, up one level; over the whole drive, back to the folder
// it was opened from.
export const canGoBack = computed(() => ui.view === "all" && (ui.scope === "drive" || drive.trail.length > 0));

/** goBack is the toolbar's Go Up / Return to Current Folder (⌘[). */
export function goBack() {
  if (!canGoBack.value) return;
  if (ui.scope === "drive") void setScope("folder");
  else void goToCrumb(drive.trail.length - 1);
}

/** The folder new folders, cloud-add tasks and uploads land in: the root while the whole drive is shown. */
export const currentFolderId = computed(() =>
  ui.view === "all" && ui.scope === "folder" ? (drive.trail.at(-1)?.id ?? "") : "",
);

/** What the page calls currentFolderId, for saying where something will land. */
export const currentFolderName = computed(() =>
  ui.view === "all" && ui.scope === "folder" ? (drive.trail.at(-1)?.name ?? t("全部文件")) : t("全部文件"),
);

/* ---------------- filtering & sorting ---------------- */

// Category, sort and search are the backend's job once the index answers.
// Before that the server still gets the sort (it honours order), while the
// category and the query are applied to the fetched page by `visible`.
watch([() => ui.category, () => ui.sortKey, () => ui.sortAsc], () => {
  if (drive.status?.configured && !restoring) void reload(false);
});

// The sort is a preference (Settings · General), so it outlives the session.
watch(
  () => ui.sortKey,
  (key) => localStorage.setItem("pkp.sortKey", key),
);

// The list follows the words of the box; conditions alone do not refetch it
// (FilesSearch answers them from the index, see below).
let queryTimer: number | undefined;
watch(listQuery, () => {
  if (!drive.status?.configured || !indexReady.value || restoring) return;
  clearTimeout(queryTimer);
  queryTimer = setTimeout(() => {
    if (listQuery.value) countUse("search");
    void reload(false);
  }, 250);
});

/* ---------- Local Search ---------- */

/**
 * FilesSearch stands in for the list while the box has conditions (Type,
 * Size, Time, Playback…) and the index can answer them. Everything here reads
 * this computer's index (SearchIndex): no model, nothing sent anywhere.
 */
export const localSearchShown = computed(() => localSearch.force || !!(localSearch.range?.account === currentId.value && (localSearch.range.folderId || localSearch.range.ids)) || (searchParse.value.filtered && indexReady.value));

export const localSearch = reactive({
  /** The box text and account the result answers. */
  text: "",
  account: "",
  loading: false,
  error: "",
  result: null as AISearchResult | null,
  /** Ticked rows (ids), for Organize These. */
  picked: [] as string[],
  range: null as { account: string; ids?: string[]; folderId?: string; under: string } | null,
  force: false,
});

watch([videoWorksShown, () => ui.route], ([shown, route]) => {
  if (!shown || route !== "files") cancelMediaLibraryLoad();
}, { flush: "sync" });

/** SearchIndex answers at most 200 rows; a filter page shows them all. */
const LOCAL_SEARCH_LIMIT = 200;

let localSerial = 0;

/** runLocalSearch asks the index for the box's conditions; a later run wins. */
export async function runLocalSearch(): Promise<void> {
  searchRevision.value += 1;
  const parsed = searchParse.value;
  const account = currentId.value;
  if (!localSearchShown.value || !indexReady.value || !account) return;
  const serial = ++localSerial;
  const text = ui.query;
  localSearch.loading = true;
  localSearch.error = "";
  try {
    const range = localSearch.range?.account === account ? localSearch.range : null;
    const result = await DriveService.SearchIndex(account, {
      ...parsed.query,
      ...(!parsed.filtered && ui.category !== "all" ? { category: ui.category === "other" ? "otherfiles" : ui.category } : {}),
      ...(range?.ids ? { ids: range.ids } : {}),
      ...(range?.folderId && range.under === parsed.query.under ? { folderId: range.folderId } : {}),
      limit: parsed.query.limit || LOCAL_SEARCH_LIMIT,
    });
    if (serial !== localSerial) return;
    const ids = new Set((result.hits ?? []).map((h) => h.id));
    if (localSearch.account !== account) localSearch.picked = [];
    localSearch.picked = localSearch.picked.filter((id) => ids.has(id));
    localSearch.result = result;
    localSearch.text = text;
    localSearch.account = account;
  } catch (err) {
    if (serial !== localSerial) return;
    localSearch.error = errorText(err);
  } finally {
    if (serial === localSerial) localSearch.loading = false;
  }
}

let localTimer: number | undefined;
watch([() => ui.query, () => localSearch.force, () => localSearch.range, () => ui.category, currentId, indexReady, () => drive.indexProgress?.syncedAt], () => {
  clearTimeout(localTimer);
  localSerial += 1;
  localSearch.loading = false;
  if (!ui.query.trim() && !currentSavedSearch.value) {
    localSearch.force = false;
    localSearch.range = null;
  }
  if (!localSearchShown.value || restoring) {
    // A box without conditions: the result is stale, and late answers are dropped.
    if (!localSearchShown.value) {
      localSerial++;
      localSearch.loading = false;
    }
    return;
  }
  localTimer = setTimeout(() => void runLocalSearch(), 250);
});

/** The ticked rows of the result, in its order. */
export const localSearchPicked = computed<AISearchHit[]>(() => {
  const picked = new Set(localSearch.picked);
  return (localSearch.result?.hits ?? []).filter((h) => picked.has(h.id));
});

export function toggleLocalPick(id: string): void {
  const at = localSearch.picked.indexOf(id);
  if (at >= 0) localSearch.picked.splice(at, 1);
  else localSearch.picked.push(id);
}

/**
 * openLocalSearch shows the file page searching for `text` across the
 * drive (AI Assistant's Find the Last Unfinished Video): the conditions open FilesSearch.
 */
export function openLocalSearch(text: string, keepRange = false): void {
  if (!leaveSavedHistory(() => openLocalSearch(text, keepRange))) return;
  localSearch.force = false;
  if (!keepRange) localSearch.range = null;
  const moved = ui.view !== "all" || drive.trail.length > 0;
  ui.view = "all";
  ui.route = "files";
  ui.query = text;
  if (!moved) return;
  drive.trail = [];
  clearSelection();
  void reload(true);
}

type SavedHistoryDialog = { kind: "rename" | "delete" | "leave"; account: string; id: string; name: string };
export const savedHistory = reactive({ account: "", id: "", baseline: null as SavedSearch | null, error: "", dialog: null as SavedHistoryDialog | null });
export const savedHistoryFocus = reactive({ serial: 0 });
let savedHistoryNext: (() => void) | null = null;
let savedHistoryCancel: (() => void) | null = null;

export const currentSavedSearch = computed(() => savedHistory.account === currentId.value && savedSearches.account === currentId.value
  ? savedSearches.items.find((item) => item.id === savedHistory.id) : undefined);

function savedHistoryDefinition(): { query: string; range: SavedSearchRange | null; category: SavedSearchCategory } {
  const range = localSearch.range?.account === currentId.value ? localSearch.range : null;
  return {
    query: ui.query,
    range: range?.folderId && range.under === searchParse.value.query.under ? { folderId: range.folderId, under: range.under } : null,
    category: searchParse.value.filtered ? "all" : ui.category,
  };
}

export const savedHistoryCanSave = computed(() => !!currentId.value && !!ui.query.trim() && !(localSearch.range?.account === currentId.value && localSearch.range.ids !== undefined));
export const savedHistoryDirty = computed(() => {
  const baseline = savedHistory.baseline;
  if (!currentSavedSearch.value || !baseline) return false;
  const draft = savedHistoryDefinition();
  return draft.query.trim() !== baseline.query.trim() || draft.category !== baseline.category || (draft.range?.folderId ?? "") !== (baseline.range?.folderId ?? "");
});

function bindSavedHistory(item: SavedSearch): void {
  Object.assign(savedHistory, { account: currentId.value, id: item.id, baseline: { ...item, range: item.range ? { ...item.range } : null }, error: "" });
}

function detachSavedHistory(): void {
  Object.assign(savedHistory, { account: "", id: "", baseline: null, error: "" });
}

/** Keep the stored ID through condition edits; applying a definition never writes storage. */
function applySavedHistory(item: SavedSearch): void {
  localSerial += 1;
  clearTimeout(localTimer);
  localSearch.result = null;
  localSearch.picked = [];
  localSearch.error = "";
  localSearch.loading = false;
  localSearch.range = item.range ? { account: currentId.value, ...item.range } : null;
  clearSelection();
  ui.category = item.category;
  ui.scope = "folder";
  ui.view = "all";
  drive.trail = [];
  ui.route = "files";
  ui.workspace = "files";
  ui.query = item.query;
  localSearch.force = true;
  searchRevision.value += 1;
  bindSavedHistory(item);
  void nextTick(() => {
    clearTimeout(localTimer);
    void runLocalSearch();
  });
}

function savedHistoryFailure(error: unknown): false {
  savedHistory.error = error instanceof Error ? error.message : String(error);
  return false;
}

function validateSavedHistoryDraft(): void {
  if (!ui.query.trim()) throw new Error(t("请输入搜索条件"));
  if (!savedHistoryCanSave.value) throw new Error(t("所选项目范围不能保存为动态搜索，请先移除所选项范围"));
}

export function saveSavedHistoryChanges(): boolean {
  const item = currentSavedSearch.value;
  if (!item) return savedHistoryFailure(t("这条已保存历史已不存在，请重新打开列表"));
  try {
    validateSavedHistoryDraft();
    const draft = savedHistoryDefinition();
    const updated = updateSavedSearch(currentId.value, item.id, draft.query, draft.range, draft.category);
    bindSavedHistory(updated);
    return true;
  } catch (error) { return savedHistoryFailure(error); }
}

export function discardSavedHistoryChanges(): void {
  const item = currentSavedSearch.value;
  if (item) applySavedHistory(item);
}

export function saveSavedHistoryAs(name: string): boolean {
  try {
    validateSavedHistoryDraft();
    const draft = savedHistoryDefinition();
    const item = saveSearch(currentId.value, name, draft.query, draft.range, draft.category);
    bindSavedHistory(item);
    return true;
  } catch (error) { return savedHistoryFailure(error); }
}

/** Synchronous navigation stays synchronous until a dirty saved definition needs a decision. */
function leaveSavedHistory(next: () => void): boolean {
  if (!savedHistoryDirty.value) { detachSavedHistory(); return true; }
  if (savedHistory.dialog?.kind === "leave") return false;
  const item = currentSavedSearch.value!;
  savedHistoryNext = next;
  savedHistory.error = "";
  savedHistory.dialog = { kind: "leave", account: currentId.value, id: item.id, name: item.name };
  return false;
}

function waitSavedHistoryLeave(): Promise<boolean> {
  if (!savedHistoryDirty.value) { detachSavedHistory(); return Promise.resolve(true); }
  if (savedHistory.dialog?.kind === "leave") return Promise.resolve(false);
  // macOS 12 WebKit lacks Promise.withResolvers.
  return new Promise((resolve) => {
    savedHistoryCancel = () => resolve(false);
    leaveSavedHistory(() => resolve(true));
  });
}

export function resolveSavedHistoryLeave(choice: "save" | "discard" | "stay"): void {
  const dialog = savedHistory.dialog;
  if (dialog?.kind !== "leave" || dialog.account !== currentId.value) return;
  if (choice === "save" && !saveSavedHistoryChanges()) return;
  const next = savedHistoryNext;
  const cancel = savedHistoryCancel;
  savedHistoryCancel = null;
  savedHistoryNext = null;
  savedHistory.dialog = null;
  savedHistory.error = "";
  if (choice === "stay" || dialog.account !== currentId.value) { cancel?.(); return; }
  if (choice === "discard") discardSavedHistoryChanges();
  detachSavedHistory();
  next?.();
}

export function openSavedSearch(id: string, edit = false): void {
  if (!currentId.value || accounts.switching) return;
  if (savedSearches.account !== currentId.value) loadSavedSearches(currentId.value);
  const item = savedSearches.items.find((entry) => entry.id === id);
  if (!item) { savedHistoryFailure(t("这条已保存历史已不存在，请重新打开列表")); return; }
  if (savedHistory.id === id && savedHistory.account === currentId.value) {
    if (!savedHistoryDirty.value && !edit) applySavedHistory(item);
    else ui.route = "files";
    if (edit) savedHistoryFocus.serial += 1;
    return;
  }
  if (!leaveSavedHistory(() => openSavedSearch(id, edit))) return;
  applySavedHistory(item);
  if (edit) savedHistoryFocus.serial += 1;
}

export function openSavedHistoryDialog(kind: "rename" | "delete", id: string): void {
  const item = savedSearches.account === currentId.value ? savedSearches.items.find((entry) => entry.id === id) : undefined;
  if (!item) return;
  savedHistory.error = "";
  savedHistory.dialog = { kind, account: currentId.value, id, name: item.name };
}

export function closeSavedHistoryDialog(): void {
  if (savedHistory.dialog?.kind === "leave") { resolveSavedHistoryLeave("stay"); return; }
  savedHistory.dialog = null;
  savedHistory.error = "";
}

export function renameSavedHistory(name: string): boolean {
  const dialog = savedHistory.dialog;
  if (dialog?.kind !== "rename" || dialog.account !== currentId.value) return false;
  try {
    renameSavedSearch(dialog.account, dialog.id, name);
    if (savedHistory.baseline?.id === dialog.id) savedHistory.baseline.name = name.trim();
    closeSavedHistoryDialog();
    return true;
  } catch (error) { return savedHistoryFailure(error); }
}

export function deleteSavedHistory(): boolean {
  const dialog = savedHistory.dialog;
  if (dialog?.kind !== "delete" || dialog.account !== currentId.value) return false;
  try {
    removeSavedSearch(dialog.account, dialog.id);
    if (savedHistory.id === dialog.id) detachSavedHistory();
    closeSavedHistoryDialog();
    return true;
  } catch (error) { return savedHistoryFailure(error); }
}

watch(currentId, () => {
  savedHistoryCancel?.();
  savedHistoryCancel = null;
  savedHistoryNext = null;
  savedHistory.dialog = null;
  detachSavedHistory();
});
watch(() => savedSearches.items, () => {
  if (savedHistory.id && savedSearches.account === savedHistory.account && !savedSearches.items.some((item) => item.id === savedHistory.id)) detachSavedHistory();
});

export const localSearchSelectionCount = computed(() => localSearch.range?.account === currentId.value ? localSearch.range.ids?.length : undefined);

export function clearLocalSearchRange() {
  localSearch.range = null;
  void runLocalSearch();
}

/** Re-run the original source query locally, not just its preview rows. */
export function openAssistantSearch(query: AIQuery): void {
  if (!idle() || assistant.account !== currentId.value) return;
  if (!leaveSavedHistory(() => openAssistantSearch(query))) return;
  localSearch.result = null;
  localSearch.picked = [];
  localSearch.range = {
    account: currentId.value,
    ...(query.ids !== undefined ? { ids: query.ids.slice() } : {}),
    ...(query.folderId ? { folderId: query.folderId } : {}),
    under: query.under,
  };
  openLocalSearch(searchText(query), true);
  void nextTick(() => {
    clearTimeout(localTimer);
    void runLocalSearch();
  });
}

/**
 * searchPreview is the ⌘K dropdown's instant results: up to six hits for
 * the box as typed, from the index; nothing before it is ready, nothing on
 * a failure (the dropdown is a shortcut, the page says what went wrong).
 */
export async function searchPreview(text: string): Promise<AISearchHit[]> {
  const account = currentId.value;
  if (!indexReady.value || !account || !text.trim()) return [];
  const { query } = parseSearch(text, new Date());
  try {
    const result = await DriveService.SearchIndex(account, { ...query, aggregate: null, limit: 6 });
    return account === currentId.value ? (result.hits ?? []).slice(0, 6) : [];
  } catch {
    return [];
  }
}

/**
 * Rename by Rule (SelectionBar): the entries the sheet renames, [] while it is
 * closed. The sheet only previews; openRenamePlan makes the plan the user
 * confirms on the assistant page.
 */
export const renameSheet = reactive({ entries: [] as Entry[] });

/* ---------- What's Here (folder stats in Details) ---------- */

/**
 * folderStats reads what a folder of the account on screen holds at any
 * depth ("" = the whole drive), from the index. A refusal (index not ready,
 * folder gone) comes back as its text, for the card to say in place.
 */
export async function folderStats(folderId: string): Promise<{ account: string; stats: FolderStats | null; error: string }> {
  const account = currentId.value;
  try {
    return { account, stats: await DriveService.FolderStats(account, folderId), error: "" };
  } catch (err) {
    return { account, stats: null, error: errorText(err) };
  }
}

/* ---------- Folder Cover ---------- */

/** A folder's cover with Go's nil slice normalised. */
export type Cover = Omit<FolderCover, "items"> & { items: Entry[] };

/**
 * The folder cards' covers for the account on screen, by folder id (Go
 * FolderCovers, from the index). A card asks for its own while it is on
 * screen (showCover). An index pass makes every cover stale: the cards on
 * screen read theirs again while still showing the old one, so a folder
 * that did not change never blinks. Another account starts empty.
 */
export const folderCovers = reactive(new Map<string, Cover>());
const coversShown = new Set<string>();
// The stamp each cover was last asked at, set when the request goes out so
// a card scrolling out and back in meanwhile does not ask twice.
const coverAsked = new Map<string, string>();
const coverQueue = new Set<string>();
let coverFlush = false;
let coversAccount = "";

// "" while the index cannot answer (not walked yet, or rebuilding).
const coverStamp = computed(() => (indexReady.value ? `${currentId.value} ${drive.indexProgress?.syncedAt ?? ""}` : ""));

/** showCover tells the store a folder card came on screen (or left it). */
export function showCover(id: string, shown: boolean) {
  if (!shown) {
    coversShown.delete(id);
    return;
  }
  coversShown.add(id);
  askCover(id);
}

function askCover(id: string) {
  const stamp = coverStamp.value;
  if (!stamp || coverAsked.get(id) === stamp) return;
  coverQueue.add(id);
  if (coverFlush) return;
  coverFlush = true;
  // The cards entering in one layout pass go out as one call.
  setTimeout(() => void flushCovers(), 0);
}

async function flushCovers() {
  coverFlush = false;
  const ids = [...coverQueue];
  coverQueue.clear();
  const stamp = coverStamp.value;
  const account = currentId.value;
  const at = epoch;
  if (!stamp || !ids.length) return;
  for (const id of ids) coverAsked.set(id, stamp);
  let covers: FolderCover[];
  try {
    covers = (await DriveService.FolderCovers(account, ids)) ?? [];
  } catch {
    // Asked again at the next stamp (the index readying or a pass ending).
    for (const id of ids) if (coverAsked.get(id) === stamp) coverAsked.delete(id);
    return;
  }
  if (at !== epoch || account !== currentId.value) return;
  for (const c of covers) folderCovers.set(c.id, { ...c, items: c.items ?? [] });
}

watch(coverStamp, () => {
  if (coversAccount !== currentId.value) {
    coversAccount = currentId.value;
    folderCovers.clear();
    coverAsked.clear();
  }
  for (const id of coversShown) askCover(id);
});

/**
 * trailTo is the breadcrumb of a folder of the account on screen, its own
 * crumb last ([] = the root): from the page when the folder is on it, else
 * read from Go (a search result's folder). null when it could not be read.
 */
export async function trailTo(folderId: string, name: string): Promise<Crumb[] | null> {
  if (!folderId) return [];
  const crumbs = drive.trail.map((c) => ({ id: c.id, name: c.name }));
  const here = crumbs.findIndex((c) => c.id === folderId);
  if (ui.view === "all" && here >= 0) return crumbs.slice(0, here + 1);
  if (ui.view === "all" && !listQuery.value && drive.entries.some((e) => e.id === folderId)) return [...crumbs, { id: folderId, name }];
  const trail = await run(t("读取文件夹路径"), () => DriveService.FolderTrail(folderId));
  return trail ? trail.map((c) => ({ id: c.id, name: c.name })) : null;
}

/** The file page's Type chips: Other is every file none of the named chips takes. */
export function matchesCategory(entry: Entry, category: CategoryFilter): boolean {
  if (category === "all") return true;
  if (category === "other") {
    return !["video", "image", "document", "audio"].includes(entry.category);
  }
  return entry.category === category;
}

/** sortEntries returns a sorted copy; `rows` is left as it came. */
export function sortEntries(rows: Entry[], key: SortKey, asc: boolean): Entry[] {
  const dir = asc ? 1 : -1;
  return [...rows].sort((a, b) => {
    switch (key) {
      case "name":
        return a.name.localeCompare(b.name, "zh") * dir;
      case "size":
        return (a.size - b.size) * dir;
      default:
        return (Date.parse(a.modifiedTime) - Date.parse(b.modifiedTime) || 0) * dir;
    }
  });
}

/**
 * visible is the page as the list shows it. Indexed pages arrive already
 * filtered and sorted; a live page is filtered and sorted here, which is
 * also what makes the search box useful while the index is still building.
 */
const visible = computed(() => {
  if (!drive.live) return drive.entries;
  const needle = listQuery.value.toLowerCase();
  const rows = drive.entries.filter(
    (e) => matchesCategory(e, ui.category) && (!needle || e.name.toLowerCase().includes(needle)),
  );
  return sortEntries(rows, ui.sortKey, ui.sortAsc);
});

export const visibleFolders = computed(() => visible.value.filter((e) => e.folder));
export const visibleFiles = computed(() => visible.value.filter((e) => !e.folder));
/** Every visible row in the order the page draws them: folders, then files. */
const visibleOrder = computed(() => [...visibleFolders.value, ...visibleFiles.value]);

/* ---------------- selection & the context column ---------------- */

const pickedSet = computed(() => new Set(drive.picked));

export function isPicked(entry: Entry): boolean {
  return pickedSet.value.has(entry.id);
}

/**
 * The picked entries the page still shows, in drawing order. A filter or
 * sort change can hide picked rows; batch actions only ever touch what the
 * user can see.
 */
export const pickedEntries = computed(() => visibleOrder.value.filter((e) => pickedSet.value.has(e.id)));

/**
 * setPicked replaces the selection. A different set brings
 * up Details and an empty one Overview; picking the set that is already picked
 * changes nothing, so an Overview the user chose stays until the set itself
 * changes. The column only changes what it shows: nothing here resizes,
 * reflows or scrolls the file page.
 */
function setPicked(ids: string[], anchor: string) {
  const changed = ids.length !== drive.picked.length || ids.some((id) => !pickedSet.value.has(id));
  drive.picked = ids;
  drive.pickAnchor = anchor;
  if (changed) ui.panelTab = ids.length ? "details" : "overview";
  syncDetail();
}

/** select picks exactly this entry. */
export function select(entry: Entry) {
  setPicked([entry.id], entry.id);
}

/** togglePick adds or removes one entry, keeping the rest (⌘/Ctrl-click, the pick box). */
export function togglePick(entry: Entry) {
  setPicked(pickedSet.value.has(entry.id) ? drive.picked.filter((id) => id !== entry.id) : [...drive.picked, entry.id], entry.id);
}

/** pickRange extends the selection from the anchor to this entry (⇧-click), in drawing order. */
export function pickRange(entry: Entry) {
  const rows = visibleOrder.value;
  const from = rows.findIndex((e) => e.id === drive.pickAnchor);
  const to = rows.findIndex((e) => e.id === entry.id);
  if (from < 0 || to < 0) {
    select(entry);
    return;
  }
  const range = rows.slice(Math.min(from, to), Math.max(from, to) + 1).map((e) => e.id);
  const kept = drive.picked.filter((id) => !range.includes(id));
  setPicked([...kept, ...range], drive.pickAnchor);
}

/** pickAll selects every row on the page (⌘/Ctrl-A). */
export function pickAll() {
  const ids = visibleOrder.value.map((e) => e.id);
  setPicked(ids, ids[0] ?? "");
}

/** clearSelection drops every pick; the column goes back to Overview. */
export function clearSelection() {
  setPicked([], "");
}

// A new filter or search is a new scope: the column goes back to Overview, and
// picks the new scope hides are dropped once the rows are in.
watch([() => ui.category, () => ui.query.trim()], () => {
  ui.panelTab = "overview";
});

watch(visibleOrder, (rows) => {
  if (!drive.picked.length) return;
  const shown = new Set(rows.map((e) => e.id));
  const kept = drive.picked.filter((id) => shown.has(id));
  if (kept.length === drive.picked.length) return;
  drive.picked = kept;
  if (!kept.includes(drive.pickAnchor)) drive.pickAnchor = kept[0] ?? "";
  if (!kept.length) ui.panelTab = "overview";
  syncDetail();
});

/**
 * Details describes one entry in full; with several picked it sums them up
 * instead. While the viewer is up it describes the file being viewed, which
 * may not be in the list (a search hit, a transfer's file).
 */
function syncDetail() {
  const only = drive.picked.length === 1 ? drive.entries.find((e) => e.id === drive.picked[0]) : undefined;
  const viewed = ui.route === "viewer" && drive.picked.every((id) => id === viewer.entry?.id) ? viewer.entry : null;
  const target = only ?? viewed;
  if (target) inspect(target);
  else forgetDetail();
}

function inspect(entry: Entry) {
  if (drive.selectedId === entry.id && (drive.detail?.id === entry.id || drive.detailLoading)) return;
  drive.selectedId = entry.id;
  drive.detail = null;
  void loadDetail(entry);
}

let detailSerial = 0;

function forgetDetail() {
  detailSerial += 1;
  drive.selectedId = "";
  drive.detail = null;
  drive.detailLoading = false;
  drive.detailError = "";
}

/**
 * loadDetail fills in what only FileDetail knows. Picking A and at once B
 * must not let A's late answer land on B, nor one account's on another's:
 * the answer has to belong to the latest request of the same account. The
 * wait and a failure are told in the column, not as a toast.
 */
async function loadDetail(entry: Entry) {
  const serial = ++detailSerial;
  const account = accountKey();
  drive.detailLoading = true;
  drive.detailError = "";
  try {
    const detail = await DriveService.FileDetail(entry.id);
    if (serial !== detailSerial || account !== accountKey()) return;
    drive.detail = detail;
  } catch (err) {
    if (serial !== detailSerial || (await sessionLost())) return;
    drive.detailError = errorText(err);
  } finally {
    if (serial === detailSerial) drive.detailLoading = false;
  }
}

/** retryDetail asks FileDetail again for the entry Details shows. */
export function retryDetail() {
  const entry = drive.entries.find((e) => e.id === drive.selectedId) ?? (viewer.entry?.id === drive.selectedId ? viewer.entry : undefined);
  if (entry) void loadDetail(entry);
}

/** setPanelOpen shows or puts away the right column; the choice is kept across launches. */
export function setPanelOpen(open: boolean) {
  ui.panelOpen = open;
  localStorage.setItem("pkp.panel", open ? "1" : "0");
}

// The width under which the files and the column take turns.
const workspaceQuery = window.matchMedia(WORKSPACE_QUERY);

/**
 * showInfo is View Info and ⌘/Ctrl-I: the column comes back if it was put
 * away and shows Details. This is an explicit layout change, unlike picking;
 * under 960px it opens the Info page.
 */
export function showInfo() {
  setPanelOpen(true);
  ui.panelTab = "details";
  if (workspaceQuery.matches) ui.workspace = "info";
}

/* ---------------- file operations ---------------- */

function patchEntry(id: string, patch: Partial<Entry>) {
  const row = drive.entries.find((e) => e.id === id);
  if (row) Object.assign(row, patch);
  if (drive.detail?.id === id) Object.assign(drive.detail, patch);
}

function ids(entries: Entry[]): string[] {
  return entries.map((e) => e.id);
}

export async function setStarred(entries: Entry[], starred: boolean) {
  if (!entries.length) return;
  const ok = await run(starred ? t("加星标") : t("取消星标"), async () => {
    await DriveService.SetStarred(ids(entries), starred);
    return true;
  });
  if (!ok) return;
  for (const e of entries) patchEntry(e.id, { starred });
  if (ui.view === "starred" && !starred) {
    removeEntries(ids(entries));
    clearSelection();
  }
  if (entries.length > 1) notify(starred ? t("已加星标 {n} 项", { n: entries.length }) : t("已取消星标 {n} 项", { n: entries.length }), "success");
}

export function toggleStar(entry: Entry) {
  return setStarred([entry], !entry.starred);
}

export async function rename(entry: Entry, name: string) {
  const updated = await run(t("重命名"), () => DriveService.RenameFile(entry.id, name));
  if (updated) patchEntry(entry.id, { name: updated.name });
  return !!updated;
}

/**
 * removalNotice words a RemoveReport the eachTransfer way: how many went
 * through, and which did not and why — a batch never claims a success it did
 * not have.
 */
function removalNotice(done: (n: number) => string, report: RemoveReport): { text: string; tone: "success" | "error" | "info" } {
  const removed = report.removed?.length ?? 0;
  const failed = (report.failed ?? []).map((f) => t("{name}（{reason}）", { name: f.name, reason: reasonOf(f.error) }));
  let text = failed.length ? failedText(done(removed), failed) : done(removed);
  if (report.stopped) text = t("{text}，其余已停止", { text });
  return { text, tone: failed.length ? "error" : report.stopped ? "info" : "success" };
}

/** notifyRemoval reports a remove run item by item; done words what went through. */
export function notifyRemoval(done: (n: number) => string, report: RemoveReport): void {
  const { text, tone } = removalNotice(done, report);
  notify(text, tone);
}

/** removalSize is what deleting `entries` for good frees, as far as the list knows. */
export function removalSize(entries: Entry[]): number {
  return entries.reduce((sum, e) => sum + (e.size > 0 ? e.size : 0), 0);
}

/** freedText is the 「Free X Now」 of a permanent-delete confirm. */
export function freedText(entries: Entry[]): string {
  const size = removalSize(entries);
  return size > 0 ? t("立即释放 {size}", { size: bytes(size) }) : t("立即释放它们占用的空间");
}

type Removal = "trash" | "restore" | "delete";
const REMOVAL_LABEL: Record<Removal, string> = { trash: N_("移到回收站"), restore: N_("还原"), delete: N_("彻底删除") };
const REMOVAL_DONE: Record<Removal, (n: number) => string> = {
  trash: (n) => t("已移到回收站 {n} 项", { n }),
  restore: (n) => t("已还原 {n} 项", { n }),
  delete: (n) => t("已彻底删除 {n} 项", { n }),
};
const REMOVAL_ONE: Record<Removal, (name: string) => string> = {
  trash: (name) => t("已把「{name}」移到回收站", { name }),
  restore: (name) => t("已还原「{name}」", { name }),
  delete: (name) => t("已彻底删除「{name}」", { name }),
};

/**
 * removeEntriesVia runs one remove call for the account on screen. Only what
 * the report confirms leaves the list, and only if that screen is still up;
 * a result for an account switched away from names it. A refused call goes
 * through run()'s error path. Trashing frees nothing, so only a
 * permanent delete refreshes the quota.
 */
async function removeEntriesVia(kind: Removal, entries: Entry[], call: (account: string, ids: string[]) => Promise<RemoveReport>) {
  if (!entries.length) return;
  const account = currentId.value;
  const screen = accountKey();
  const report = await run(t(REMOVAL_LABEL[kind]), () => call(account, ids(entries)));
  if (!report) return;
  const removed = report.removed ?? [];
  if (screen === accountKey()) {
    removeEntries(removed);
    clearSelection();
    if (ui.view === "trash") void loadTrashSummary();
  }
  if (kind === "delete" && removed.length) refreshQuotaSoon();

  const notice = removalNotice(REMOVAL_DONE[kind], report);
  const tone = notice.tone;
  let text = notice.text;
  if (tone === "success" && entries.length === 1 && removed.length === 1) text = REMOVAL_ONE[kind](entries[0].name);
  if (removed.length && !report.failed?.length) {
    if (kind === "trash") text = t("{text}，空间要等彻底删除后才释放", { text });
    else if (kind === "delete" && report.freed > 0) text = t("{text}，释放 {size}", { text, size: bytes(report.freed) });
  }
  if (account !== currentId.value) text = t("「{account}」{text}", { account: accountName(account), text });
  notify(text, tone);
}

export function trash(entries: Entry[]) {
  return removeEntriesVia("trash", entries, (account, list) => DriveService.RemoveFiles(account, list, false));
}

export function restore(entries: Entry[]) {
  return removeEntriesVia("restore", entries, (account, list) => DriveService.RestoreFiles(account, list));
}

export function deleteForever(entries: Entry[]) {
  return removeEntriesVia("delete", entries, (account, list) => DriveService.RemoveFiles(account, list, true));
}

/**
 * trashSummary is what Empty Trash would take, from the index: top-level
 * entries and the bytes of every trashed file. ready=false means unknown.
 */
export const trashSummary = reactive({ count: 0, size: 0, ready: false });
let trashSummaryAccount = "";

export async function loadTrashSummary() {
  const account = currentId.value;
  const screen = accountKey();
  if (account !== trashSummaryAccount) Object.assign(trashSummary, { count: 0, size: 0, ready: false });
  trashSummaryAccount = account;
  const summary = await DriveService.TrashSummary(account).catch(() => null);
  if (!summary || screen !== accountKey()) return;
  Object.assign(trashSummary, { count: summary.count, size: summary.size, ready: summary.ready });
}

/** emptyTrash deletes everything in the account's recycle bin for good. */
export async function emptyTrash() {
  const account = currentId.value;
  const screen = accountKey();
  const report = await run(t("清空回收站"), () => DriveService.EmptyTrash(account));
  if (!report) return;
  if (report.removed?.length) refreshQuotaSoon();

  const notice = removalNotice(REMOVAL_DONE.delete, report);
  const tone = notice.tone;
  let text = notice.text;
  if (tone === "success") text = t("已清空回收站 {n} 项", { n: report.removed?.length ?? 0 });
  if (report.freed > 0) text = t("{text}，释放 {size}", { text, size: bytes(report.freed) });
  if (account !== currentId.value) text = t("「{account}」{text}", { account: accountName(account), text });
  notify(text, tone);

  if (screen !== accountKey()) return;
  void loadTrashSummary();
  if (ui.route === "files" && ui.view === "trash") {
    clearSelection();
    await reload(false);
  }
}

/* ---------------- move & copy, and the folder to organize ---------------- */

/** trailPath names a folder by its trail from the root: "All Files / Work Files / Design Projects". */
export function trailPath(trail: Crumb[]): string {
  return [t("全部文件"), ...trail.map((c) => c.name)].join(" / ");
}

/**
 * The folder picker. It walks folders on its own trail, separate from the
 * page's, so choosing never navigates the file list. move / copy pick a
 * destination for `items`; organize picks the folder Media Organization works on;
 * cloudAdd picks where the Cloud Add sheet saves; cleanup picks the folder
 * Cleanup scans. A name typed in its search box is looked up across the drive;
 * New Folder makes a folder in the level on screen and walks into it.
 */
export const relocate = reactive({
  mode: "" as "" | "move" | "copy" | "cloudAdd" | "cleanup" | "sync",
  account: "",
  error: "",
  items: [] as Entry[],
  trail: [] as Crumb[],
  folders: [] as Entry[],
  loading: false,
  working: false,
  query: "",
  results: [] as FolderChoice[],
  searching: false,
  /** False while the index is still being built: the drive cannot be searched yet. */
  searchable: true,
  /** The New Folder name row is open; creating while the server takes the name. */
  naming: false,
  creating: false,
});

let relocateSerial = 0;
let relocateSearchSerial = 0;
let relocateSearchTimer = 0;
let syncFolderChosen: ((trail: Crumb[]) => void) | null = null;

export function openSyncFolderPicker(account: string, folderId: string, choose: (trail: Crumb[]) => void) {
  closeRelocate();
  relocate.mode = "sync";
  relocate.account = account;
  syncFolderChosen = choose;
  void relocateTo(folderId ? [{ id: folderId, name: "" }] : []);
}

export function openRelocate(mode: "move" | "copy", entries: Entry[]) {
  if (!entries.length) return;
  relocate.mode = mode;
  relocate.account = currentId.value;
  relocate.items = [...entries];
  relocate.working = false;
  void relocateTo([]);
}

function clearRelocateSearch() {
  relocateSearchSerial += 1;
  clearTimeout(relocateSearchTimer);
  relocate.query = "";
  relocate.results = [];
  relocate.searching = false;
}

export function closeRelocate() {
  relocateSerial += 1;
  clearRelocateSearch();
  relocate.mode = "";
  relocate.account = "";
  relocate.error = "";
  syncFolderChosen = null;
  relocate.items = [];
  relocate.trail = [];
  relocate.folders = [];
  relocate.loading = false;
  relocate.working = false;
  relocate.naming = false;
  relocate.creating = false;
}

/** relocateTo shows the folders under the last crumb of `trail` ([] = root), leaving any search. */
export async function relocateTo(trail: Crumb[]) {
  const serial = ++relocateSerial;
  clearRelocateSearch();
  relocate.naming = false;
  relocate.trail = trail;
  relocate.folders = [];
  relocate.error = "";
  relocate.loading = true;
  try {
    const account = relocate.account;
    if (relocate.mode === "sync") {
      const actual = await DriveService.SyncFolderTrail(account, trail.at(-1)?.id ?? "");
      if (serial !== relocateSerial) return;
      relocate.trail = actual ?? [];
    }
    const folders = relocate.mode === "sync"
      ? await DriveService.SyncFolders(account, relocate.trail.at(-1)?.id ?? "")
      : await DriveService.ListFolders(trail.at(-1)?.id ?? "");
    if (serial !== relocateSerial) return;
    // Same collation as the page's name sort, so Documents/Photos/Movies read in pinyin order.
    relocate.folders = (folders ?? []).sort((a, b) => a.name.localeCompare(b.name, "zh"));
  } catch (error) {
    if (serial === relocateSerial) relocate.error = errorText(error);
  } finally {
    if (serial === relocateSerial) relocate.loading = false;
  }
}

/** searchRelocateFolders looks for folders anywhere in the drive, a moment after typing stops. */
export function searchRelocateFolders(query: string) {
  const serial = ++relocateSearchSerial;
  clearTimeout(relocateSearchTimer);
  relocate.query = query;
  relocate.naming = false;
  relocate.error = "";
  if (!query.trim()) {
    relocate.results = [];
    relocate.searching = false;
    return;
  }
  relocate.searching = true;
  relocateSearchTimer = window.setTimeout(async () => {
    try {
      const res = await DriveService.SearchFolders(relocate.account, query);
      if (serial !== relocateSearchSerial) return;
      relocate.searchable = res?.ready ?? true;
      // What is being moved or copied, and everything under it, is no
      // destination: a picked folder cannot be entered while browsing either.
      const picked = new Set(relocate.items.map((e) => e.id));
      relocate.results = (res?.folders ?? []).filter((f) => !(f.trail ?? []).some((c) => picked.has(c.id)));
    } catch (error) {
      if (serial === relocateSearchSerial) relocate.error = errorText(error);
    } finally {
      if (serial === relocateSearchSerial) relocate.searching = false;
    }
  }, 200);
}

/** enterRelocateResult opens a search hit's folder and leaves the search. */
export function enterRelocateResult(folder: FolderChoice) {
  void relocateTo((folder.trail ?? []).map((c) => ({ id: c.id, name: c.name })));
}

/**
 * createRelocateFolder makes a folder in the level on screen and walks into
 * it, so it becomes the destination. It answers whether the server took the
 * name; on a refusal the name row stays open under the error notice.
 */
export async function createRelocateFolder(name: string): Promise<boolean> {
  if (relocate.creating || !relocate.mode) return false;
  const serial = relocateSerial;
  const parent = relocate.trail.map((c) => ({ ...c }));
  const parentId = parent.at(-1)?.id ?? "";
  const account = relocate.account;
  relocate.error = "";
  relocate.creating = true;
  let entry: Entry | undefined;
  try {
    entry = await DriveService.CreateFolder(account, parentId, name);
  } catch (error) {
    if (serial === relocateSerial) relocate.error = errorText(error);
  } finally {
    if (serial === relocateSerial) relocate.creating = false;
  }
  if (!entry) return false;
  notify(t("已新建文件夹「{name}」", { name: entry.name }), "success");
  // The page behind lists that folder: show the new one there too.
  if (account === currentId.value && ui.route === "files" && ui.view === "all" && !ui.query.trim() && !wholeDrive.value && currentFolderId.value === parentId) {
    void reload(false);
  }
  // Closed or walked elsewhere meanwhile: the folder is made, the picker stays where it is.
  if (serial === relocateSerial) void relocateTo([...parent, { id: entry.id, name: entry.name }]);
  return true;
}

export async function confirmRelocate() {
  const { mode, items } = relocate;
  if (!mode || relocate.working || relocate.creating || relocate.naming || relocate.loading || relocate.error || (mode === "sync" && relocate.query.trim())) return;
  if (mode === "sync") {
    const trail = relocate.trail.map((c) => ({ ...c }));
    const choose = syncFolderChosen;
    closeRelocate();
    choose?.(trail);
    return;
  }
  if (mode === "cloudAdd") {
    cloudAdd.dest = relocate.trail.map((c) => ({ ...c }));
    closeRelocate();
    return;
  }
  if (mode === "cleanup") {
    const trail = relocate.trail.map((c) => ({ ...c }));
    closeRelocate();
    setCleanupScope(trail);
    return;
  }
  if (!items.length) return;
  const target = relocate.trail.at(-1);
  const targetId = target?.id ?? "";
  const where = target?.name ?? t("全部文件");
  relocate.working = true;
  try {
    const ok = await run(mode === "move" ? t("移动") : t("复制"), async () => {
      if (mode === "move") await DriveService.MoveFiles(ids(items), targetId);
      else await DriveService.CopyFiles(ids(items), targetId);
      return true;
    });
    if (!ok) return;
    closeRelocate();
    if (mode === "move") {
      // Out of this folder: the rows go. Anywhere else (Starred, search) the
      // rows stay but their location changed, so the page is refetched.
      if (ui.view === "all" && !wholeDrive.value && !(ui.query.trim() && indexReady.value)) removeEntries(ids(items));
      else void reload(false);
      clearSelection();
      notify(
        items.length === 1
          ? t("已把「{name}」移动到「{where}」", { name: items[0].name, where })
          : t("已把 {n} 项移动到「{where}」", { n: items.length, where }),
        "success",
      );
    } else {
      notify(
        items.length === 1
          ? t("已开始把「{name}」复制到「{where}」，完成后会出现在目标文件夹", { name: items[0].name, where })
          : t("已开始把 {n} 项复制到「{where}」，完成后会出现在目标文件夹", { n: items.length, where }),
        "success",
      );
    }
  } finally {
    relocate.working = false;
  }
}

/* ---------------- Extract Online ---------------- */

/**
 * The extraction sheet. It reads the archive first and lets the service's
 * answer decide what comes next: the contents to pick from, or a password
 * page that never shows the contents beside it. The password lives here
 * only while the sheet is open, for the listing and the extraction that
 * need it; closing drops it. The destination is the archive's folder or a
 * folder of the same account picked in the sheet's own folder browser.
 */
export type ExtractStage = "reading" | "password" | "failed" | "select" | "running" | "done" | "taskFailed";

export const extract = reactive({
  open: false,
  /** The account the archive belongs to; another account on screen closes the sheet. */
  account: "",
  entry: null as Entry | null,
  stage: "reading" as ExtractStage,
  password: "",
  passwordError: "",
  failure: null as Failure | null,
  items: [] as ArchiveItem[],
  /** Ticked items by path. */
  picked: [] as string[],
  /** The archive's folder (Current Folder); null until its trail is read. */
  here: null as Crumb[] | null,
  dest: "here" as "here" | "other",
  /** The folder last confirmed in the browser; kept while Current Folder is ticked again. */
  chosen: null as Crumb[] | null,
  /** Why Extract Selected did not start, said under the choices. */
  note: "",
  submitting: false,
  task: { id: "", files: 0, folder: "", place: "", progress: 0, fileId: "", error: "", stale: false },
  browser: {
    open: false,
    trail: [] as Crumb[],
    folders: [] as FolderChoice[],
    loading: false,
    error: "",
    query: "",
    results: [] as FolderChoice[],
    searching: false,
    /** false while the index is being built: the whole drive cannot be searched yet. */
    searchable: true,
  },
});

// extractSession ends with the sheet (or the account it was opened for);
// readSerial voids a listing overtaken by a newer one.
let extractSession = 0;
let readSerial = 0;

/** The folder the extraction goes to, from the root ([] = all files). */
export const extractTarget = computed<Crumb[] | null>(() => (extract.dest === "other" ? extract.chosen : extract.here));

/** openExtract starts online extraction for an archive of the account on screen. */
export function openExtract(entry: Entry) {
  if (entry.folder || entry.category !== "archive" || entry.trashed) return;
  closeExtract();
  const session = extractSession;
  Object.assign(extract, { open: true, account: currentId.value, entry, dest: "here", chosen: null, here: null });
  void DriveService.FolderTrail(entry.parentId)
    .then((trail) => {
      if (session === extractSession) extract.here = (trail ?? []).map((c) => ({ id: c.id, name: c.name }));
    })
    .catch(() => {
      if (session === extractSession) extract.note = t("没有读到压缩包所在的文件夹，请选择其他文件夹。");
    });
  void readArchive("");
}

export function closeExtract() {
  extractSession += 1;
  clearTimeout(browserTimer);
  Object.assign(extract, {
    open: false,
    entry: null,
    stage: "reading",
    password: "",
    passwordError: "",
    failure: null,
    items: [],
    picked: [],
    note: "",
    submitting: false,
    task: { id: "", files: 0, folder: "", place: "", progress: 0, fileId: "", error: "", stale: false },
  });
  Object.assign(extract.browser, { open: false, trail: [], folders: [], loading: false, error: "", query: "", results: [], searching: false });
}

/** readArchive asks for the archive's contents; password is "" until the service wants one. */
export async function readArchive(password: string) {
  const entry = extract.entry;
  if (!entry) return;
  const session = extractSession;
  const serial = ++readSerial;
  const current = () => session === extractSession && serial === readSerial;
  extract.stage = "reading";
  extract.failure = null;
  try {
    const res = await DriveService.ArchiveContents(extract.account, entry.id, password);
    if (!current()) return;
    switch (res.status) {
      case "ok":
        extract.password = password;
        extract.passwordError = "";
        extract.items = res.items ?? [];
        extract.picked = extract.items.map((i) => i.path);
        if (extract.items.length) extract.stage = "select";
        else {
          extract.stage = "failed";
          extract.failure = { title: t("压缩包是空的"), detail: t("里面没有可以解压的文件。"), retry: false };
        }
        return;
      case "password":
      case "wrongPassword":
        extract.stage = "password";
        extract.passwordError = res.status === "wrongPassword" ? t("密码不正确，请检查后重试。") : "";
        return;
      case "running":
        showExtractTask(res.taskId, 0);
        return;
      default:
        extract.stage = "failed";
        extract.failure = failureOf(res.status, res.message);
    }
  } catch (err) {
    if (!current()) return;
    if (await sessionLost()) return closeExtract();
    if (!current()) return;
    extract.stage = "failed";
    extract.failure = failureOf("error", errorText(err));
  }
}

/** submitArchivePassword retries the listing with a password; an empty one sends nothing. */
export function submitArchivePassword(password: string) {
  if (!password) {
    extract.passwordError = t("请输入压缩包密码。");
    return;
  }
  void readArchive(password);
}

export function toggleExtractPick(path: string) {
  const picked = new Set(extract.picked);
  if (!picked.delete(path)) picked.add(path);
  extract.picked = extract.items.map((i) => i.path).filter((p) => picked.has(p));
  extract.note = "";
}

export function pickAllExtract(on: boolean) {
  extract.picked = on ? extract.items.map((i) => i.path) : [];
  extract.note = "";
}

/** setExtractHere goes back to current folder; the folder confirmed in the browser is kept for another switch. */
export function setExtractHere() {
  extract.dest = "here";
  extract.note = "";
}

/** openExtractBrowser shows the drive's folders in the sheet, from the folder last confirmed. */
export function openExtractBrowser() {
  extract.browser.open = true;
  extract.browser.query = "";
  extract.browser.results = [];
  extract.browser.searching = false;
  // The first time from the root; after that from the folder confirmed.
  void browseExtractTo(extract.chosen ?? []);
}

/** closeExtractBrowser returns to the sheet without changing the destination. */
export function closeExtractBrowser() {
  clearTimeout(browserTimer);
  extract.browser.open = false;
  extract.browser.query = "";
  extract.browser.results = [];
  extract.browser.searching = false;
}

let browserSerial = 0;
let browserTimer = 0;

/** browseExtractTo shows the folders under the last crumb of trail ([] = the root). */
export async function browseExtractTo(trail: Crumb[]) {
  const serial = ++browserSerial;
  const b = extract.browser;
  b.trail = trail;
  b.folders = [];
  b.error = "";
  b.loading = true;
  try {
    const folders = await DriveService.PickFolders(extract.account, trail.at(-1)?.id ?? "");
    if (serial !== browserSerial) return;
    b.folders = (folders ?? []).sort((x, y) => x.name.localeCompare(y.name, "zh"));
  } catch (err) {
    if (serial === browserSerial) b.error = errorText(err);
  } finally {
    if (serial === browserSerial) b.loading = false;
  }
}

/** searchExtractFolders looks for folders anywhere in the drive, a moment after typing stops. */
export function searchExtractFolders(query: string) {
  const b = extract.browser;
  b.query = query;
  clearTimeout(browserTimer);
  const serial = ++browserSerial;
  if (!query.trim()) {
    b.results = [];
    b.searching = false;
    b.error = "";
    // Typing then clearing must not leave the level half-loaded.
    if (b.loading) void browseExtractTo(b.trail);
    return;
  }
  b.searching = true;
  browserTimer = window.setTimeout(async () => {
    try {
      const res = await DriveService.SearchFolders(extract.account, query);
      if (serial !== browserSerial) return;
      b.searchable = res.ready;
      b.results = res.folders ?? [];
      b.error = "";
    } catch (err) {
      if (serial === browserSerial) b.error = errorText(err);
    } finally {
      if (serial === browserSerial) b.searching = false;
    }
  }, 200);
}

/** enterExtractResult opens a search hit's folder and leaves the search. */
export function enterExtractResult(folder: FolderChoice) {
  clearTimeout(browserTimer);
  extract.browser.query = "";
  extract.browser.results = [];
  extract.browser.searching = false;
  void browseExtractTo((folder.trail ?? []).map((c) => ({ id: c.id, name: c.name })));
}

/** confirmExtractBrowser makes the folder on screen the destination; nothing is extracted yet. */
export function confirmExtractBrowser() {
  extract.chosen = extract.browser.trail.map((c) => ({ ...c }));
  extract.dest = "other";
  extract.note = "";
  closeExtractBrowser();
}

/** A running extraction, read until it ends — also after the sheet closed, which then says how it ended. */
interface ExtractJob {
  account: string;
  name: string;
  /** The destination's name; "" for a task found already running, its destination unknown. */
  place: string;
  misses: number;
}

const extractJobs = new Map<string, ExtractJob>();

/** submitExtract starts extracting the ticked items into the destination. */
export async function submitExtract() {
  const entry = extract.entry;
  if (!entry || extract.submitting) return;
  const picked = new Set(extract.picked);
  if (!picked.size) {
    extract.note = t("请至少选择一个要解压的文件或文件夹。");
    return;
  }
  const target = extractTarget.value;
  if (!target) {
    extract.note = extract.dest === "other" ? t("请先选择目标文件夹。") : t("压缩包所在的文件夹还没有读到，请稍候或选择其他文件夹。");
    return;
  }
  const session = extractSession;
  const account = extract.account;
  const parentId = target.at(-1)?.id ?? "";
  const place = target.at(-1)?.name ?? t("全部文件");
  extract.note = "";
  extract.submitting = true;
  try {
    const res = await DriveService.ExtractArchive(account, {
      fileId: entry.id,
      password: extract.password,
      parentId,
      picks: picksOf(extract.items, picked),
    });
    // A task that started is followed even if the sheet closed meanwhile.
    if (res.status === "started") watchExtract(res.taskId, { account, name: entry.name, place, misses: 0 });
    if (session !== extractSession) return;
    switch (res.status) {
      case "started":
        extract.task.folder = res.folder;
        showExtractTask(res.taskId, res.files);
        return;
      case "running":
        showExtractTask(res.taskId, 0);
        return;
      case "nameTaken":
        extract.note = t("「{place}」里已有「{folder}」，解压会与它重名。请换一个文件夹，或先把已有的「{folder}」改名。", { place, folder: res.folder });
        return;
      case "destinationGone":
        extract.note = t("保存位置已不存在或已移到回收站，请重新选择。");
        return;
      case "password":
      case "wrongPassword":
        extract.password = "";
        extract.stage = "password";
        extract.passwordError = t("密码已失效，请重新输入。");
        return;
      default: {
        const f = failureOf(res.status, res.message);
        extract.note = `${f.title}。${f.detail}`;
      }
    }
  } catch (err) {
    if (session !== extractSession) return;
    if (await sessionLost()) return closeExtract();
    extract.note = t("没有开始解压：{error}", { error: errorText(err) });
  } finally {
    if (session === extractSession) extract.submitting = false;
  }
}

/** showExtractTask puts a running task on the sheet; it is read until it ends. */
function showExtractTask(taskId: string, files: number) {
  watchExtract(taskId, { account: extract.account, name: extract.entry?.name ?? "", place: "", misses: 0 });
  extract.task.id = taskId;
  extract.task.files = files;
  extract.task.place = extractJobs.get(taskId)?.place ?? "";
  extract.task.progress = 0;
  extract.task.stale = false;
  extract.stage = "running";
}

function watchExtract(taskId: string, job: ExtractJob) {
  if (extractJobs.has(taskId)) return;
  extractJobs.set(taskId, job);
  void pollExtract(taskId);
}

async function pollExtract(taskId: string) {
  const job = extractJobs.get(taskId);
  if (!job) return;
  let next: number;
  try {
    const state = await DriveService.ExtractProgress(job.account, taskId);
    job.misses = 0;
    if (extract.open && extract.task.id === taskId) {
      extract.task.progress = state.progress;
      extract.task.stale = false;
    }
    if (state.phase !== "running") {
      extractJobs.delete(taskId);
      finishExtract(taskId, job, state.phase === "done", state.fileId, state.error);
      return;
    }
    next = state.next;
  } catch {
    // A missed reading is not an outcome: the task runs on in the cloud.
    // Read less often, and give up quietly after a long while.
    job.misses += 1;
    if (extract.open && extract.task.id === taskId) extract.task.stale = true;
    if (job.misses >= 20) {
      extractJobs.delete(taskId);
      return;
    }
    next = Math.min(30, 2 * job.misses);
  }
  window.setTimeout(() => void pollExtract(taskId), next * 1000);
}

function finishExtract(taskId: string, job: ExtractJob, done: boolean, fileId: string, error: string) {
  if (extract.open && extract.task.id === taskId) {
    extract.task.fileId = fileId;
    extract.task.error = done ? "" : taskFailure(error);
    extract.stage = done ? "done" : "taskFailed";
  } else if (done) {
    notify(job.place ? t("「{name}」已解压到「{place}」", { name: job.name, place: job.place }) : t("「{name}」已解压完成", { name: job.name }), "success");
  } else {
    notify(t("「{name}」没有解压完成：{error}", { name: job.name, error: taskFailure(error) }), "error");
  }
}

/** revealExtracted closes the sheet and shows what the extraction made, selected in its folder. */
export async function revealExtracted() {
  const { account } = extract;
  const { fileId } = extract.task;
  closeExtract();
  if (fileId) await revealFile({ account, fileId });
}

/** retryExtract goes back to the choices after a task that did not finish. */
export function retryExtract() {
  extract.task.id = "";
  extract.note = "";
  if (extract.items.length) extract.stage = "select";
  else void readArchive(extract.password);
}

/* ---------------- AI Settings (Settings · AI) ---------------- */

/**
 * Settings · AI as Go keeps it. The API key never lives here: the page only
 * learns `keyHint`; whatever the user typed is handed to SetAIKey and dropped.
 */
export const aiSettings = reactive({
  value: null as AISettings | null,
  loading: false,
  saving: false,
  testing: false,
  // The last Test Connection result; cleared when the connection or key changes.
  test: null as AITestResult | null,
});

/** A model is set and it can be asked (a key stored, or a keyless local service): AI requests may go out. */
export const aiReady = computed(() => !!aiSettings.value?.configured);

/** loadAISettings reads the local settings (no network). */
export async function loadAISettings(): Promise<void> {
  aiSettings.loading = true;
  try {
    const settings = await run(t("读取 AI 设置"), () => DriveService.AISettings());
    if (settings) aiSettings.value = settings;
  } finally {
    aiSettings.loading = false;
  }
}

async function saveAI(label: string, fn: () => Promise<AISettings>, resetTest: boolean): Promise<boolean> {
  aiSettings.saving = true;
  try {
    const settings = await run(label, fn);
    if (!settings) return false;
    aiSettings.value = settings;
    if (resetTest) aiSettings.test = null;
    return true;
  } finally {
    aiSettings.saving = false;
  }
}

/** saveAIConnection saves Provider / Base URL / Model Name; Go drops a key entered for another endpoint. */
export async function saveAIConnection(provider: string, baseUrl: string, model: string): Promise<boolean> {
  const hadKey = !!aiSettings.value?.keyHint;
  const ok = await saveAI(t("保存模型连接"), () => DriveService.SetAIConnection(provider, baseUrl, model), true);
  if (ok && hadKey && !aiSettings.value?.keyHint) notify(t("提供商或地址已更换，原来的 API Key 已删除，请重新填写"));
  return ok;
}

/** saveAIKey stores the key in the keychain; "" clears it. The caller empties its field. */
export async function saveAIKey(key: string): Promise<boolean> {
  const ok = await saveAI(key ? t("保存 API Key") : t("清除 API Key"), () => DriveService.SetAIKey(key), true);
  if (ok) notify(key ? t("API Key 已保存到{place}", { place: keychainName }) : t("API Key 已清除"), "success");
  return ok;
}

/** saveAITokenLimit is Per-Conversation Limit (0 = unlimited): a conversation's turns, follow-ups and identify runs share it. */
export function saveAITokenLimit(limit: number): Promise<boolean> {
  return saveAI(t("保存单个对话上限"), () => DriveService.SetAITokenLimit(limit), false);
}

/** tokensLeft is what may still be spent under `limit` (0 = unlimited) after `used`. */
function tokensLeft(limit: number, used: number): number {
  return limit > 0 ? limit - used : Infinity;
}

/** applyAIPreset is the Provider Preset menu: the preset's connection; Custom keeps the fields as they are. */
export async function applyAIPreset(id: string): Promise<boolean> {
  const preset = AI_PRESETS.find((p) => p.id === id);
  const s = aiSettings.value;
  if (!preset) return false;
  if (preset.id !== CUSTOM_PRESET_ID) return saveAIConnection(preset.provider, preset.baseUrl, preset.model);
  if (s?.provider === preset.provider) return true;
  return saveAIConnection(preset.provider, s?.baseUrl ?? "", s?.model ?? "");
}

export function saveAINaming(style: string, series: string, movie: string): Promise<boolean> {
  return saveAI(t("保存命名模板"), () => DriveService.SetAINaming(style, series, movie), false);
}

/**
 * testAIConnection makes the smallest real request and keeps what it found
 * in aiSettings.test; what it learned of tools and streaming (Go saved it)
 * applies at once.
 */
export async function testAIConnection(): Promise<void> {
  aiSettings.testing = true;
  aiSettings.test = null;
  try {
    const result = await run(t("测试连接"), () => DriveService.TestAIConnection());
    if (!result) return;
    aiSettings.test = result;
    const s = aiSettings.value;
    if (s) aiSettings.value = { ...s, tools: result.tools || s.tools, stream: result.stream || s.stream };
  } finally {
    aiSettings.testing = false;
  }
}

/**
 * setAIConsent is the account's Before Sending to AI for one data level: metadata
 * (names, paths, sizes, times) or content (subtitle text); always = Always Allow.
 * The list follows at once.
 */
export async function setAIConsent(account: string, level: "metadata" | "content", always: boolean): Promise<boolean> {
  const ok = await run(t("保存授权设置"), async () => {
    await DriveService.SetAIConsent(account, level, always);
    return true;
  });
  if (!ok) return false;
  const entry = accounts.list.find((a) => a.id === account);
  if (entry && level === "metadata") entry.aiConsent = always;
  else if (entry) entry.aiContentConsent = always;
  return true;
}

/** openAISettings is every Set Up AI / Manage Consent Settings link: Settings · AI. */
export function openAISettings(): void {
  openSettings("ai");
}

/* ---------------- Network & Proxy (Settings · Network & Proxy) ---------------- */

/**
 * Settings · Network & Proxy as Go keeps it: only PKP's own connections, never the
 * computer's VPN or system proxy. The proxy password is not part of it —
 * ApplyProxy hands what the user typed to the keychain and the page only
 * ever learns `passwordHint`.
 */
export const proxySettings = reactive({
  value: null as ProxySettings | null,
  loading: false,
  saving: false,
  testing: false,
  // The last Test Connection result; it belongs to the draft that was tested, so
  // the page drops it as soon as that draft changes.
  test: null as ProxyTestResult | null,
});

/**
 * proxyDraftError is what Custom Proxy refuses, in the order the card is
 * filled in; "" means the draft may be tested and applied. Go checks the
 * same things, but answering here keeps a typo from reaching the network
 * stack and closing every idle connection for nothing.
 */
export function proxyDraftError(mode: string, host: string, port: number): string {
  if (mode !== "custom") return "";
  if (!host.trim()) return t("请填写代理服务器地址");
  if (!Number.isInteger(port) || port < 1 || port > 65535) return t("端口需要是 1 到 65535 之间的整数");
  return "";
}

/** loadProxySettings reads the saved settings and what Follow System resolves to (no network). */
export async function loadProxySettings(): Promise<void> {
  proxySettings.loading = true;
  try {
    const settings = await run(t("读取网络设置"), () => DriveService.ProxySettings());
    if (settings) proxySettings.value = settings;
  } finally {
    proxySettings.loading = false;
  }
}

/**
 * applyProxy is Apply Settings: until it returns, the draft is only a draft and
 * every request still takes the route that is in force. When Go refuses,
 * that route is untouched and run() has already said why.
 */
export async function applyProxy(input: ProxyInput): Promise<boolean> {
  const bad = proxyDraftError(input.mode, input.host, input.port);
  if (bad) {
    notify(bad, "error");
    return false;
  }
  proxySettings.saving = true;
  try {
    const settings = await run(t("应用网络设置"), () => DriveService.ApplyProxy(input));
    if (!settings) return false;
    proxySettings.value = settings;
    proxySettings.test = null;
    // A colour change is not a result: the toast names the route in force.
    notify(t("网络设置已应用，当前使用：{route}", { route: settings.active }), "success");
    return true;
  } finally {
    proxySettings.saving = false;
  }
}

/**
 * testProxy tries the draft without saving it. The result keeps the proxy
 * handshake and the PKP service request apart, so an upstream outage is
 * not read as a wrong address.
 */
export async function testProxy(input: ProxyInput): Promise<void> {
  const bad = proxyDraftError(input.mode, input.host, input.port);
  if (bad) {
    notify(bad, "error");
    return;
  }
  proxySettings.testing = true;
  proxySettings.test = null;
  try {
    const result = await run(t("测试连接"), () => DriveService.TestProxy(input));
    if (result) proxySettings.test = result;
  } finally {
    proxySettings.testing = false;
  }
}

/** forgetProxyPassword deletes the stored proxy password from the keychain. */
export async function forgetProxyPassword(): Promise<boolean> {
  proxySettings.saving = true;
  try {
    const settings = await run(t("清除代理密码"), () => DriveService.ForgetProxyPassword());
    if (!settings) return false;
    proxySettings.value = settings;
    proxySettings.test = null;
    notify(t("代理密码已从{place}清除", { place: keychainName }), "success");
    return true;
  } finally {
    proxySettings.saving = false;
  }
}

/** openProxySettings is every Check Network Settings link: Settings · Network & Proxy. */
export function openProxySettings(): void {
  openSettings("network");
}

/* ---------------- AI Assistant ---------------- */

/*
 * AI is a layer on the save → organize → play line, bound to the account it
 * was started for (assistant.account):
 *   - L0, no model: file-name rules and a TMDB exact match make Media Organization
 *     plans (localMediaPlan); a sentence without a model is answered from
 *     the local index first (localFallback).
 *   - a conversation: agent.ts runs each turn — the model calls read-only
 *     tools here, and write intents become plan cards; nothing touches the
 *     drive before Confirm Execution.
 *   - consent by data level: one sheet per task (a turn, an identify run, a
 *     follow-up), never per request, unless the account says Always Allow.
 * TMDB answers never reach a model request: identify sends only the groups
 * the rules could not name, followUp sends matched groups as 「Matched」, and
 * tool results describe plans without titles. A run goes on in its own
 * account whatever comes on screen.
 */

export type AssistantScreen = "home" | "chat" | "plan" | "receipt";

/** A plan on the plan screen: what buildPlan (or a proposal) made, the user's ticks and names on top by item id. */
export interface AssistantPlan {
  id: string;
  kind: "media" | "changes";
  origin: PlanOrigin;
  /** What the record calls the task: 「Media Organization · Uncategorized」. */
  title: string;
  basis: string;
  /** Groups the rules could not name; the model may identify them. */
  lowGroups: number;
  /** Arrivals included in this plan, retained across account switches. */
  arrivals: Arrival[];
  /** The Scope ([] = all files) and its name. */
  scope: Crumb[];
  scopeName: string;
  task: string;
  instructions: string;
  tree: Tree;
  groups: MediaGroup[];
  identities: Identity[];
  resolutions: ReadonlyMap<string, string>;
  /** Series keys whose guessed year the user adopted. */
  years: string[];
  /** Why unidentified groups stay. */
  missing: string;
  base: MediaPlan;
  picks: Record<string, boolean>;
  names: Record<string, string>;
  /** Canonical suggestions explicitly kept, loaded from this account's local inbox. */
  reviews: string[];
  /** base.changes with picks and names applied, Empty After Execution folders refreshed. */
  changes: Change[];
  /** The user ticked or renamed something. */
  touched: boolean;
  /** The ids the plan was asked for (a selection or an arrival); null = the whole scope. */
  ids: string[] | null;
  /** The low groups still unnamed. */
  low: string[];
  /** What the model said of groups, before TMDB matched them: kept so a reopened draft is named again the same way. */
  modelIdentities: Identity[];
  /** Tokens the plan's own model requests took (identify, follow-ups). */
  tokens: number;
  /** The conversation whose card shows it, "" for none. */
  conversation: string;
}

export interface ConsentSheet {
  accountName: string;
  level: "metadata";
  title: string;
  lede: string;
  sends: string;
  notSends: string;
  /** The first request. */
  estimate: TokenRange;
  /** The limit the task runs under. */
  limit: number;
  /** 「Send to DeepSeek · deepseek-chat」 */
  to: string;
}

export interface ConnectSheet {
  /** Local Index: N Items Found (null = not applicable). */
  found: number | null;
  /** Rules: N Items Can Be Organized. */
  organizable: number | null;
  /** What the user asked. */
  text: string;
  /** 「View Local Plan First / View Local Results First」 is offered. */
  hasLocal: boolean;
  error: string;
  testing: boolean;
}

export type { ConversationView, PlanProposal } from "./assistantcore";

/** A local snapshot of the files or folder this request may read and organize. */
export type AskScope = AssistantScope;

export const assistant = reactive({
  account: "",
  screen: "home" as AssistantScreen,
  prompt: "",
  scope: {} as AskScope,
  conversation: null as ConversationView | null,
  conversations: [] as ConversationInfo[],
  /** The step in flight as the page says it; "" when idle. */
  busy: "",
  consent: null as ConsentSheet | null,
  /** The turn in progress. */
  live: null as null | { turnId: string; user: string; scope?: AskScope; text: string; traces: Trace[]; cards: Card[]; resultGroups?: SearchResultGroup[] },
  connect: null as ConnectSheet | null,
  // Raw: a plan holds the whole Scope; every edit replaces the object.
  plan: null as AssistantPlan | null,
  planBack: "home" as "home" | "chat" | "files" | "transfers",
  confirming: false,
  running: false,
  undoing: false,
  stopping: false,
  runAccount: "",
  progress: { done: 0, total: 0 },
  receipt: null as OrganizeRecord | null,
  /** Keep Unchanged of the run on the receipt: kept and unticked items; 0 for a receipt opened from Recent Tasks. */
  receiptKept: 0,
  history: [] as OrganizeRecord[],
  historyError: "",
  notice: "",
  noticeTone: "info" as "info" | "success" | "warning" | "error",
});

// Bumped whenever the task in flight is dropped (another account, home, a
// new conversation): a reply or an answer still on its way then finds it
// moved on and does nothing.
let taskSerial = 0;
let aiRequestSerial = 0;
let planSerial = 0;
/** The AI request in flight (AIComplete or AIChat), for AICancel. */
let requestId = "";
let turnAbort: AbortController | null = null;
let consentAnswer: ((go: boolean, remember: boolean) => void) | null = null;
/** Where ai:delta text of the request in flight goes. */
let deltaTo: { id: string; push: (text: string) => void } | null = null;
/** The open conversation's handles; snapshotted into it when saved. */
let handles = new Handles();
/** Draft plans of the open conversation's cards, by plan id. */
const drafts = new Map<string, AssistantPlan>();
/** What the connect sheet holds back until a model is connected. */
type Held =
  | { kind: "turn"; text: string; scope?: AskScope; plan: AssistantPlan | null; card: FilesCard | null }
  | { kind: "refine"; text: string }
  | { kind: "identify" };
let held: Held | null = null;

const LOW_REASON = N_("规则无法确定，可交给 AI 识别");
// Go's errNotIndexed (plantree.go): it reaches the page in the page's language.
const NOT_INDEXED = N_("新文件还没进入本地索引，请稍后再试");

function say(text: string, tone: typeof assistant.noticeTone = "info") {
  assistant.notice = text;
  assistant.noticeTone = tone;
}

/** dropTask ends what the task was waiting for: the request in flight, the sheet on screen, the turn. */
function dropTask() {
  taskSerial += 1;
  turnAbort?.abort();
  turnAbort = null;
  if (requestId) void DriveService.AICancel(requestId).catch(() => undefined);
  requestId = "";
  deltaTo = null;
  assistant.busy = "";
  assistant.live = null;
  assistant.connect = null;
  held = null;
  const answer = consentAnswer;
  consentAnswer = null;
  assistant.consent = null;
  answer?.(false, false);
}

/** What the AI Assistant holds that a switch would drop, "" for nothing. */
function assistantPending(): string {
  if (assistant.account !== currentId.value) return "";
  if (assistant.live || assistant.consent || assistant.connect || (assistant.busy && !assistant.running)) return t("AI 助手的任务");
  if (assistant.plan?.touched && assistant.screen === "plan" && !assistant.running) return t("整理计划");
  return "";
}

/** resetAssistant drops the account's assistant (a switch, a sign-out); a run in flight finishes in its account. */
function resetAssistant() {
  dropTask();
  assistant.account = "";
  assistant.screen = "home";
  assistant.prompt = "";
  assistant.scope = {};
  assistant.conversation = null;
  assistant.conversations = [];
  handles = new Handles();
  drafts.clear();
  assistant.plan = null;
  assistant.confirming = false;
  if (!assistant.running) {
    assistant.undoing = false;
    assistant.stopping = false;
  }
  assistant.receipt = null;
  assistant.receiptKept = 0;
  assistant.history = [];
  assistant.historyError = "";
  say("");
  inbox.account = "";
  inbox.arrivals = [];
  inbox.loaded = false;
  inbox.summaries = {};
  arrivalPlans.clear();
}

/** openAssistant is the nav's AI Assistant. */
export function openAssistant() {
  if (!leaveSavedHistory(openAssistant)) return;
  ui.route = "assistant";
}

/** Whether a new task may start: nothing waits on the model, the user or a run. */
function idle(): boolean {
  return !assistant.busy && !assistant.consent && !assistant.connect && !assistant.live && !assistant.running;
}

/** 「Send to DeepSeek · deepseek-chat」 / 「Send to OpenAI-Compatible Service (<host>) · <model>」 */
function modelLine(): string {
  const s = aiSettings.value;
  if (!s) return "";
  const preset = presetOf(s);
  if (preset.id !== CUSTOM_PRESET_ID) return t("发送给 {name} · {model}", { name: preset.label, model: s.model });
  let host = s.baseUrl;
  try {
    host = new URL(s.baseUrl).host;
  } catch {
    // Go keeps only http(s) URLs; the raw text is still the best name.
  }
  return t("发送给 OpenAI 兼容服务（{host}） · {model}", { host, model: s.model });
}

/**
 * askConsent shows the Per-Turn Consent sheet and resolves with the answer; Send and Always
 * Allow saves the account's metadata level first. A task dropped meanwhile
 * counts as Don't Send.
 */
function askConsent(sheet: Pick<ConsentSheet, "title" | "lede" | "sends" | "estimate" | "limit">): Promise<boolean> {
  const account = assistant.account;
  const serial = taskSerial;
  assistant.consent = {
    accountName: accountName(account),
    level: "metadata",
    notSends: t("文件内容、字幕、账号凭据、下载链接和 TMDB 返回的资料。"),
    to: modelLine(),
    ...sheet,
  };
  return new Promise((resolve) => {
    consentAnswer = (go, remember) => {
      consentAnswer = null;
      assistant.consent = null;
      void (async () => {
        if (go && remember) await setAIConsent(account, "metadata", true);
        resolve(go && serial === taskSerial);
      })();
    };
  });
}

/** answerConsent is Don't Send / Send Once / Send and Always Allow. */
export function answerConsent(go: boolean, remember: boolean) {
  consentAnswer?.(go, remember);
}

/** alwaysAllowed: the task's account sends metadata without asking (L1 Always Allow). */
function alwaysAllowed(): boolean {
  return !!accountById(assistant.account)?.aiConsent;
}

/** complete sends one structured request (identify, follow-up) and counts nothing: the caller does. */
async function complete(request: ModelRequest, busy: string): Promise<AIReply> {
  const id = `ai-${++aiRequestSerial}`;
  requestId = id;
  assistant.busy = busy;
  try {
    return await DriveService.AIComplete(id, request);
  } finally {
    if (requestId === id) requestId = "";
  }
}

/** chat is a turn's tools-mode request; streamed text arrives as ai:delta under the call's id. */
async function chat(request: AIChatRequest, onDelta: (text: string) => void, signal?: AbortSignal): Promise<AIChatReply> {
  const id = `ai-${++aiRequestSerial}`;
  requestId = id;
  deltaTo = { id, push: onDelta };
  const cancel = () => void DriveService.AICancel(id).catch(() => undefined);
  signal?.addEventListener("abort", cancel);
  try {
    return await DriveService.AIChat(id, request);
  } finally {
    signal?.removeEventListener("abort", cancel);
    if (deltaTo?.id === id) deltaTo = null;
    if (requestId === id) requestId = "";
  }
}

/** applyAIDelta routes an ai:delta event to the request it belongs to; others' are dropped. */
function applyAIDelta(d: AIDelta) {
  if (deltaTo && d.id === deltaTo.id && d.text) deltaTo.push(d.text);
}

/** cancelAssistantTask is Stop / Cancel: the request in flight is cancelled; what the turn found stays. */
export function cancelAssistantTask() {
  if (assistant.running) return;
  if (turnAbort) {
    turnAbort.abort();
    if (requestId) void DriveService.AICancel(requestId).catch(() => undefined);
    return;
  }
  if (consentAnswer) {
    answerConsent(false, false);
    return;
  }
  if (!assistant.busy) return;
  taskSerial += 1;
  if (requestId) void DriveService.AICancel(requestId).catch(() => undefined);
  requestId = "";
  assistant.busy = "";
  say(t("已取消，没有执行任何操作。"));
}

/* ---------- conversations ---------- */

function newConversationView(): ConversationView {
  const now = new Date().toISOString();
  return {
    id: `c${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`,
    title: "",
    createdAt: now,
    updatedAt: now,
    usage: { used: 0, limit: aiSettings.value?.tokenLimit ?? 50000 },
    turns: [],
    handles: { records: [] },
    plans: {},
  };
}

/** loadConversations reads the account's saved conversations (local, newest first). */
export async function loadConversations() {
  const account = currentId.value;
  if (!account) return;
  try {
    const list = (await DriveService.Conversations(account)) ?? [];
    if (account === currentId.value) assistant.conversations = list;
  } catch {
    // The list is a convenience; the page works without it.
  }
}

/** saveConversation keeps the conversation on this computer after each turn or card change. */
async function saveConversation(conv: ConversationView) {
  const account = assistant.account;
  if (!account || !conv.turns.length) return;
  conv.updatedAt = new Date().toISOString();
  conv.handles = handles.snapshot();
  const info: ConversationInfo = {
    id: conv.id,
    title: conv.title,
    updatedAt: conv.updatedAt,
    turns: conv.turns.length,
    planPending: hasPendingPlan(conv.turns),
  };
  try {
    await DriveService.SaveConversation(account, info, conversationBody(conv));
  } catch (err) {
    if (account === currentId.value) say(t("这段对话没能保存：{error}", { error: errorText(err) }), "warning");
    return;
  }
  if (account !== currentId.value) return;
  assistant.conversations = [info, ...assistant.conversations.filter((c) => c.id !== info.id)];
}

/** newConversation is New Conversation: the home's composer starts it. */
export function newConversation() {
  if (assistant.running) return;
  dropTask();
  assistant.account = currentId.value;
  assistant.conversation = null;
  assistant.scope = {};
  handles = new Handles();
  drafts.clear();
  assistant.plan = null;
  assistant.screen = "home";
  say("");
}

/** openConversation shows a saved conversation, its draft plans rebuilt when opened. */
export async function openConversation(id: string) {
  if (assistant.running) return;
  dropTask();
  const account = currentId.value;
  const serial = taskSerial;
  let body: string;
  try {
    body = await DriveService.Conversation(account, id);
  } catch (err) {
    if (serial === taskSerial) say(errorText(err), "error");
    return;
  }
  if (serial !== taskSerial || account !== currentId.value) return;
  const view = readConversation(body, id);
  if (!view) {
    say(t("这段对话无法读取"), "error");
    return;
  }
  assistant.account = account;
  assistant.conversation = view;
  assistant.scope = snapshotScope(view.scope ?? view.turns.at(-1)?.scope ?? {});
  handles = new Handles(view.handles);
  drafts.clear();
  assistant.plan = null;
  assistant.screen = "chat";
  say("");
}

/** deleteConversation removes a saved conversation; the open one closes. */
export async function deleteConversation(id: string) {
  const account = currentId.value;
  const ok = await run(t("删除对话"), async () => {
    await DriveService.DeleteConversation(account, id);
    return true;
  });
  if (!ok || account !== currentId.value) return;
  assistant.conversations = assistant.conversations.filter((c) => c.id !== id);
  if (assistant.conversation?.id === id) newConversation();
}

/* ---------- a turn ---------- */

function snapshotScope(scope: AskScope): AskScope {
  return {
    ...(scope.folder ? { folder: scope.folder.map(({ id, name }) => ({ id, name })) } : {}),
    ...(scope.entries ? { entries: scope.entries.map(({ id, name, folder }) => ({ id, name, folder })) } : {}),
  };
}

export function assistantScopeLabel(scope: AskScope): string {
  if (scope.entries) return t("已选 {n} 项", { n: scope.entries.length });
  if (scope.folder?.length) return scope.folder.map((c) => c.name).join(" / ");
  return t("整个云盘");
}

export function setAssistantScope(kind: "drive" | "folder" | "selection") {
  if (!idle()) return;
  if (kind === "selection" && !pickedEntries.value.length) return;
  assistant.scope = snapshotScope(kind === "selection" ? { entries: pickedEntries.value } : kind === "folder" ? { folder: drive.trail } : {});
}

/** Only names and short handles enter the prompt; scope ids stay local. */
function contextLine(scope: AskScope): string {
  if (scope.entries) {
    const shown = scope.entries.map((e) => `${handles.of(e.id, e.folder ? "folder" : "file")} ${oneLine(e.name)}${e.folder ? "/" : ""}`);
    return `Limited to the selected ${scope.entries.length} items:${shown.join("；")}. Do not expand the file scope.`;
  }
  if (scope.folder?.length) {
    const path = scope.folder.map((c) => c.name).join("/");
    const id = scope.folder.at(-1)!.id;
    return `Limited to folder ${handles.of(id, "folder", path)}（${oneLine(path)}) and its subfolders. Do not expand the file scope.`;
  }
  return "Entire cloud drive (root directory)";
}

/** askAssistant is every 「Ask AI」: a new conversation with that context, its first turn started. */
export async function askAssistant(text: string, scope?: AskScope) {
  if (!currentId.value || assistant.running) return;
  if (!leaveSavedHistory(() => void askAssistant(text, scope))) return;
  ui.route = "assistant";
  newConversation();
  assistant.scope = snapshotScope(scope ?? {});
  await startTurn(text, assistant.scope);
}

/**
 * submitComposer is the composer: on a media plan a follow-up refines it,
 * anywhere else the sentence is a turn of the open conversation (or of a
 * new one).
 */
export async function submitComposer() {
  const text = assistant.prompt.trim();
  if (!text || !idle()) return;
  if (assistant.screen === "plan" && assistant.plan?.kind === "media" && assistant.account === currentId.value) {
    await refinePlan(text);
    return;
  }
  await startTurn(text, assistant.scope);
}

/** startTurn runs one turn of the conversation through agent.ts; without a model, the local fallback. */
async function startTurn(text: string, range: AskScope = assistant.scope) {
  const scope = snapshotScope(range);
  const account = currentId.value;
  if (!account || !idle()) return;
  countUse("assistant.ask");
  if (!aiReady.value) {
    await localFallback(text, scope);
    return;
  }
  if (!assistant.conversation || assistant.account !== account) {
    dropTask();
    assistant.conversation = newConversationView();
    handles = new Handles();
    drafts.clear();
  }
  assistant.account = account;
  const conv = assistant.conversation!;
  assistant.scope = scope;
  conv.scope = scope;
  // The limit is the one set now: changing it in Settings · AI applies from the next turn, also in an open conversation.
  conv.usage.limit = aiSettings.value?.tokenLimit ?? 50000;
  if (!conv.title) conv.title = conversationTitle(text);
  const serial = taskSerial;
  const abort = new AbortController();
  turnAbort = abort;
  const turnId = `t${Date.now().toString(36)}${(++planSerial).toString(36)}`;
  const scopeSnapshot = snapshotScope(scope);
  assistant.screen = "chat";
  assistant.prompt = "";
  say("");
  assistant.live = { turnId, user: text, scope: scopeSnapshot, text: "", traces: [], cards: [] };
  const spent = { turn: 0, tools: 0 };
  const s = aiSettings.value!;
  const context = contextLine(scope);
  const extra = scope?.entries?.length ? t("另外会发送你从文件页带来的 {n} 项名称。", { n: scope.entries.length }) : "";
  const turn = await runTurn(
    {
      chat,
      json: (request) => complete(request, ""),
      tools: turnTools(account, serial, conv, () => tokensLeft(conv.usage.limit, conv.usage.used + spent.turn + spent.tools), (n) => (spent.tools += n), scope),
      consent: (d: TurnDisclosure) =>
        askConsent({
          title: t("这一轮，要把哪些信息发给模型？"),
          lede: t("本轮只询问一次：AI 在这一轮里调用本地工具查到的内容会一起发送，不会逐次再问。"),
          sends: d.sends + extra,
          estimate: d.estimate,
          limit: conv.usage.limit,
        }),
      budget: Math.max(0, tokensLeft(conv.usage.limit, conv.usage.used)),
      describe: errorText,
    },
    { turns: conv.turns, handles },
    text,
    {
      mode: s.tools === "no" ? "json" : "tools",
      alwaysAllow: alwaysAllowed(),
      stream: s.stream !== "no",
      signal: abort.signal,
      context,
      id: turnId,
      onUpdate: (t) => {
        if (serial !== taskSerial) return;
        spent.turn = t.usage.input + t.usage.output;
        assistant.live = {
          turnId,
          user: t.user,
          scope: scopeSnapshot,
          text: t.text,
          traces: t.traces,
          cards: t.cards,
          ...(t.resultGroups === undefined ? {} : {
            resultGroups: t.resultGroups.map((group) => ({
              ...group,
              sourceIds: group.sourceIds.slice(),
              ...(group.fileIds === undefined ? {} : { fileIds: group.fileIds.slice() }),
            })),
          }),
        };
      },
    },
  );
  if (turnAbort === abort) turnAbort = null;
  assistant.busy = "";
  if (serial !== taskSerial || assistant.conversation?.id !== conv.id) return;
  if (turn.denied) turn.notice = t("未发送：保留本地结果。");
  turn.scope = scopeSnapshot;
  conv.turns.push(turn);
  conv.usage.used += turn.usage.input + turn.usage.output + spent.tools;
  assistant.live = null;
  if (turn.modeNote) say(turn.modeNote);
  await saveConversation(conv);
}

/** The turn Retry is offered on: the conversation's last, when it failed and nothing else is going on. */
export const retryTurnId = computed(() => {
  const last = assistant.conversation?.turns.at(-1);
  return last && retryable(last) && idle() && assistant.account === currentId.value ? last.id : "";
});

/** retryTurn is Retry on a failed turn: the same words asked again, the new turn in its place. */
export async function retryTurn(turnId: string) {
  const conv = assistant.conversation;
  const last = conv?.turns.at(-1);
  if (!conv || !last || retryTurnId.value !== turnId) return;
  const scope = last.scope ?? {};
  conv.turns.pop();
  await startTurn(last.user, scope);
}

/* ---------- the turn's tools ---------- */

/** rowOf is an entry as a tool reports it, `path` the folder it is in. */
function rowOf(e: Pick<Entry, "id" | "name" | "folder" | "size" | "modifiedTime">, path: string): ModelRow {
  return { id: e.id, name: e.name, folder: e.folder, path, size: e.size, modifiedTime: e.modifiedTime };
}

/** cardRowOf keeps a row on a card (real id, local only), uncut: a download or share card lists everything it acts on. */
function cardRowOf(e: Pick<Entry, "id" | "name" | "folder" | "size" | "modifiedTime">, path: string): CardRow {
  return { id: e.id, name: e.name, folder: e.folder, path, size: e.size, modified: e.modifiedTime, played: "" };
}

/** pathOfParent is the folder an entry is in, "Media/Documentaries" ("" = the root). */
async function pathOfParent(parentId: string): Promise<string> {
  if (!parentId) return "";
  const trail = (await DriveService.FolderTrail(parentId)) ?? [];
  return trail.map((c) => c.name).join("/");
}

/** resolveFolder turns the model's folder (a handle, or a path from the root) into an id and a path. */
async function resolveFolder(account: string, ref: FolderRef, scope: AskScope = {}): Promise<FolderRef> {
  if (ref.id || !ref.path) return ref;
  const found = scopeFolders((await DriveService.FindFolders(account, ref.path)) ?? [], scope);
  if (!found.length) throw new Error(`Could not find folder “${ref.path}」`);
  if (found.length !== 1) {
    throw new Error(`Multiple folders with the same name were found. Please select one using its full path or number:\n${foldersForModel(found, handles)}`);
  }
  return { id: found[0].id, path: modelPath(found[0].path) };
}

function scopeFolders<T extends { id: string; trail: Crumb[] | null }>(found: T[], scope: AskScope): T[] {
  if (scope.entries) {
    const selected = new Set(scope.entries.filter((entry) => entry.folder).map((entry) => entry.id));
    return found.filter((folder) => selected.has(folder.id));
  }
  const root = scope.folder?.at(-1)?.id;
  return root ? found.filter((folder) => folder.id === root || folder.trail?.some((crumb) => crumb.id === root)) : found;
}

/** trailOf is a folder's breadcrumb ([] = the root). */
async function trailOf(folderId: string): Promise<Crumb[]> {
  return folderId ? ((await DriveService.FolderTrail(folderId)) ?? []).map((c) => ({ id: c.id, name: c.name })) : [];
}

async function checkScopeTargets(scope: AskScope, targets: readonly AgentTarget[]): Promise<void> {
  if (scope.entries) {
    const ids = new Set(scope.entries.map((e) => e.id));
    if (targets.some((target) => !ids.has(target.id))) throw new Error(t("对象不在本轮范围内，请先切换范围"));
    return;
  }
  const root = scope.folder?.at(-1)?.id;
  if (!root) return;
  for (const target of targets) {
    const trail = target.folder ? await trailOf(target.id) : await trailOf((await DriveService.FileDetail(target.id)).parentId);
    if (!trail.some((c) => c.id === root)) throw new Error(t("对象不在本轮范围内，请先切换范围"));
  }
}

async function scopedQuery(account: string, query: AIQuery, scope: AskScope): Promise<AIQuery> {
  const local = { ...query };
  if (scope.entries) local.ids = scope.entries.map((e) => e.id);
  const root = scope.folder?.at(-1);
  if (query.under) {
    const folder = await resolveFolder(account, { id: "", path: query.under }, scope);
    if (root) await checkScopeTargets({ folder: scope.folder }, [{ id: folder.id, folder: true }]);
    local.folderId = folder.id;
    local.under = folder.path;
  } else if (root) {
    local.folderId = root.id;
    local.under = scope.folder!.map((c) => c.name).join("/");
  }
  return local;
}

// English transfer states for model context, independent of translated UI labels.
const TRANSFER_STATE: Record<string, string> = {
  queued: "Queued",
  uploading: "In progress",
  downloading: "In progress",
  paused: "Paused",
  done: "Completed",
  failed: "Failed",
  cancelled: "Canceled",
};

/**
 * turnTools are the tools of one turn: read-only ones answer from the
 * index and the account at once; plan tools only build cards. `room` is
 * what the conversation may still spend, for organize_media's own identify
 * requests, which `spend` counts.
 */
function turnTools(account: string, serial: number, conv: ConversationView, room: () => number, spend: (n: number) => void, scope: AskScope): ToolImpls {
  return {
    search_files: async ({ query }) => {
      const local = await scopedQuery(account, query, scope);
      const result = await DriveService.SearchIndex(account, local);
      const forModel = searchForModel(result, local, handles);
      const card: FilesCard = {
        kind: "files",
        id: "",
        title: query.aggregate ? t("本地统计") : t("本地搜索结果"),
        state: "done",
        note: result.underMissing ? t("没有找到「{folder}」文件夹", { folder: query.under ?? "" }) : "",
        total: result.total,
        rows: cardRows(result.hits ?? []),
        aggregate: (result.aggregate ?? []).slice(0, ROW_LIMIT).map((a) => ({ key: a.key, label: a.label, size: a.size, count: a.count })),
        query: local,
      };
      return { forModel, trace: t("{n} 项", { n: result.total }), card };
    },
    find_folders: async ({ path }) => {
      const found = scopeFolders((await DriveService.FindFolders(account, path)) ?? [], scope);
      return { forModel: foldersForModel(found, handles), trace: t("{n} 个文件夹", { n: found.length }) };
    },
    folder_summary: async ({ folder }) => {
      const f = await resolveFolder(account, !folder.id && !folder.path && scope.folder?.length
        ? { id: scope.folder.at(-1)!.id, path: scope.folder.map((c) => c.name).join("/") } : folder, scope);
      if (scope.entries) throw new Error(t("统计文件夹前请将范围切换为当前文件夹"));
      await checkScopeTargets(scope, [{ id: f.id, folder: true }]);
      const [stats, inside] = await Promise.all([DriveService.FolderStats(account, f.id), DriveService.PlanTree(account, f.id, [], [])]);
      const children = (inside ?? []).filter((e) => e.parentId === f.id).map((e) => rowOf(e, f.path));
      const largest = (stats.largest ?? []).map((e) => rowOf(e, f.path));
      const card: StatsCard = {
        kind: "stats",
        id: "",
        title: t("「{name}」里都有什么", { name: f.path.split("/").pop() || t("全部文件") }),
        state: "done",
        note: "",
        folderId: f.id,
        path: f.path,
        stats: { ...stats, largest },
      };
      return { forModel: folderSummaryForModel(f.path, { ...stats, largest }, children, handles), trace: t("{n} 个文件", { n: stats.files }), card };
    },
    file_info: async ({ target }) => {
      await checkScopeTargets(scope, [target]);
      const d = await DriveService.FileDetail(target.id);
      const path = await pathOfParent(d.parentId);
      const info = {
        ...rowOf(d, path),
        createdTime: d.createdTime,
        category: d.category,
        width: d.width,
        height: d.height,
        duration: d.duration,
        children: d.children >= 0 ? d.children : undefined,
      };
      return { forModel: fileInfoForModel(info, handles), trace: d.name };
    },
    recent_plays: async () => {
      if (scope.entries || scope.folder?.length) {
        const local = await scopedQuery(account, { ...parseSearch("", new Date()).query, played: "any", sort: "played", limit: ROW_LIMIT }, scope);
        const result = await DriveService.SearchIndex(account, local);
        const card: FilesCard = { kind: "files", id: "", title: t("最近播放"), state: "done", note: "", total: result.total, rows: cardRows(result.hits ?? []), aggregate: [], query: local };
        return { forModel: searchForModel(result, local, handles), trace: t("{n} 条", { n: result.total }), card };
      }
      const plays = (await DriveService.RecentPlays(ROW_LIMIT)) ?? [];
      const rows = plays
        .filter((p) => p.available)
        .map((p) => ({ id: p.fileId, name: p.name, folder: false, path: "", size: 0, modifiedTime: p.playedAt, position: p.position, duration: p.duration }));
      const card: FilesCard = { kind: "files", id: "", title: t("最近播放"), state: "done", note: "", total: rows.length, rows: cardRows(rows), aggregate: [], query: null };
      return { forModel: playsForModel(plays, handles), trace: t("{n} 条", { n: plays.length }), card };
    },
    transfer_status: async () => {
      if (scope.entries || scope.folder?.length) throw new Error(t("此查询需要将范围切换为整个云盘"));
      const page = await DriveService.Tasks(account, "");
      const tasks = page.tasks ?? [];
      const local = [
        ...drive.uploads.filter((u) => u.account === account).map((u) => ({ name: u.name, kind: "Upload", state: TRANSFER_STATE[u.state] ?? u.state, progress: u.size ? (u.done / u.size) * 100 : 0 })),
        ...drive.downloads
          .filter((d) => d.account === account)
          .map((d) => ({ name: d.name, kind: "Download", state: TRANSFER_STATE[d.state] ?? d.state, progress: d.size ? (d.done / d.size) * 100 : 0 })),
      ];
      return { forModel: transfersForModel(tasks, local, handles), trace: t("{n} 个云添加任务", { n: tasks.length }) };
    },
    storage_quota: async () => {
      const q = await DriveService.Quota();
      return { forModel: quotaForModel(q), trace: t("已读取") };
    },
    cleanup_results: async () => {
      if (scope.entries || scope.folder?.length) throw new Error(t("此查询需要将范围切换为整个云盘"));
      const mine = cleanup.account === account;
      return { forModel: cleanupForModel(mine ? cleanup.dup.page : null, mine ? cleanup.empty.page : null, handles), trace: t("已读取") };
    },
    organize_media: async ({ folder, targets, instructions }) => {
      let plan: AssistantPlan;
      const selected = targets.length ? targets : !folder && scope.entries ? scope.entries : [];
      if (selected.length) {
        await checkScopeTargets(scope, selected);
        plan = selected.length === 1 && selected[0].folder
          ? await localMediaPlan({ account, scope: await trailOf(selected[0].id), instructions })
          : await selectionPlan(account, selected.map((target) => target.id), { instructions });
      } else {
        if (scope.entries) throw new Error(t("请明确选择本轮范围内的文件"));
        const f = folder ? await resolveFolder(account, folder, scope) : { id: scope.folder?.at(-1)?.id ?? "", path: "" };
        await checkScopeTargets(scope, [{ id: f.id, folder: true }]);
        plan = await localMediaPlan({ account, scope: await trailOf(f.id), instructions });
      }
      if (serial !== taskSerial) throw new Error("Canceled");
      // The low groups go to the model inside the turn the user agreed to, as long as the conversation has room.
      if (plan.low.length) {
        try {
          plan = await identifyPlan(plan, serial, room, spend);
        } catch (err) {
          if (serial !== taskSerial) throw err;
        }
      }
      // What the user asked of the layout (put in 「Documentaries」, put anime separately) is a follow-up over
      // every group: patterns and earlier answers only, TMDB-named groups as 「Matched」.
      if (plan.instructions.trim()) {
        const prepared = followUp(plan.identities, plan.groups, plan.instructions);
        if (prepared.disclosure.estimate.low <= room()) {
          try {
            const reply = await complete(prepared.request, t("正在按你的要求调整计划…"));
            if (serial !== taskSerial) throw new Error("Canceled");
            spend(reply.input + reply.outputTokens);
            plan = await withFollowUp(plan, parseIdentities(reply.output, plan.groups), "", 0);
          } catch (err) {
            if (serial !== taskSerial) throw err;
          }
        }
      }
      plan.conversation = conv.id;
      drafts.set(plan.id, plan);
      conv.plans[plan.id] = { kind: "media", scope: plan.scope, ids: plan.ids ?? [], instructions: plan.instructions, identities: plan.modelIdentities };
      const counts = planCounts(plan.changes, plan.base.kept.length, problems(plan.changes, plan.tree));
      const matched = new Set(plan.identities.filter((i) => i.source === "tmdb").map((i) => i.id));
      const card: PlanCard = {
        kind: "plan",
        id: plan.id,
        title: planRoute(plan.scope.length ? plan.scopeName : t("全部文件"), plan.changes),
        state: "draft",
        note: "",
        tool: "organize_media",
        planId: plan.id,
        scopeId: plan.scope.at(-1)?.id ?? "",
        scopePath: plan.scope.map((c) => c.name).join("/"),
        counts,
        sample: planSample(plan.changes),
        source: cardSource(plan.origin),
        recordId: "",
      };
      // The model receives uncertainty without permission to change those groups.
      const rest = plan.low.length ? `There are still ${plan.low.length} groups whose rules could not be determined; leave them unchanged.` : "";
      return { forModel: `${mediaSummaryForModel(plan.groups, matched)}\n${planForModel(counts, rest)}`, trace: t("{n} 项可整理", { n: counts.changes }), card };
    },
    propose_changes: async ({ title, items }) => {
      await checkScopeTargets(scope, items);
      const plan = await changesPlan(account, title, items);
      plan.conversation = conv.id;
      drafts.set(plan.id, plan);
      conv.plans[plan.id] = { kind: "changes", title, items };
      const errors = problems(plan.changes, plan.tree);
      const counts = planCounts(plan.changes, 0, errors);
      const card: PlanCard = {
        kind: "plan",
        id: plan.id,
        title: oneLine(title),
        state: "draft",
        note: "",
        tool: "propose_changes",
        planId: plan.id,
        scopeId: "",
        scopePath: "",
        counts,
        sample: planSample(plan.changes),
        source: "",
        recordId: "",
      };
      const gone = items.length - plan.base.changes.length;
      return { forModel: planForModel(counts, gone ? `${gone} items are no longer in the index and were not included in the plan.` : ""), trace: t("{n} 项", { n: counts.changes }), card };
    },
    propose_offline_add: async ({ urls, taskIds, folder }) => {
      const f = folder ? await resolveFolder(account, folder, scope) : { id: scope.folder?.at(-1)?.id ?? "", path: scope.folder?.map((c) => c.name).join("/") ?? "" };
      const card: OfflineCard = { kind: "offline", id: "", title: t("云添加"), state: "draft", note: "", urls, taskIds, folderId: f.id, path: f.path };
      return {
        forModel: `Cloud add card generated:${urls.length + taskIds.length} items, saving to “${f.path || "Root directory"}”. Not yet executed; awaiting user confirmation.`,
        trace: t("{n} 项", { n: urls.length + taskIds.length }),
        card,
      };
    },
    propose_download: async ({ targets }) => {
      await checkScopeTargets(scope, targets);
      const rows = await targetRows(targets);
      const card: DownloadCard = { kind: "download", id: "", title: t("下载到电脑"), state: "draft", note: "", rows };
      return { forModel: `已生成下载卡片：${rows.length} 项。尚未执行，等待用户确认。`, trace: t("{n} 项", { n: rows.length }), card }; // i18n-ignore
    },
    propose_share: async ({ targets }) => {
      await checkScopeTargets(scope, targets);
      const rows = await targetRows(targets);
      const card: ShareCard = { kind: "share", id: "", title: t("创建分享"), state: "draft", note: "", rows };
      return { forModel: `已生成分享卡片：${rows.length} 项。尚未执行，等待用户确认。`, trace: t("{n} 项", { n: rows.length }), card }; // i18n-ignore
    },
  };
}

/** targetRows names what a download or share card acts on. */
async function targetRows(targets: readonly AgentTarget[]): Promise<CardRow[]> {
  const details = await Promise.all(targets.map((t) => DriveService.FileDetail(t.id)));
  return details.map((d) => cardRowOf(d, ""));
}

/* ---------- cards ---------- */

/** setCard replaces a card of the open conversation and saves it. */
async function setCard(turnId: string, cardId: string, patch: Partial<Card>) {
  const conv = assistant.conversation;
  const turn = conv?.turns.find((t) => t.id === turnId);
  const at = turn ? turn.cards.findIndex((c) => c.id === cardId) : -1;
  if (!conv || !turn || at < 0) return;
  turn.cards.splice(at, 1, { ...turn.cards[at], ...patch } as Card);
  await saveConversation(conv);
}

/** entriesOf reads what card rows name again, as the drive has them now. */
async function entriesOf(rows: readonly CardRow[]): Promise<Entry[]> {
  return Promise.all(rows.map((r) => DriveService.FileDetail(r.id)));
}

/** Only the displayed, deduplicated ids enter this local draft; confirmation stays unchanged. */
export async function organizeAssistantResults(rows: readonly CardRow[]): Promise<void> {
  if (!idle() || assistant.account !== currentId.value || !rows.length) return;
  const unique = [...new Map(rows.map((row) => [row.id, row])).values()];
  await openPlan(t("正在读取所选文件…"), async (account) => unique.length === 1 && unique[0].folder
    ? localMediaPlan({ account, scope: await trailOf(unique[0].id) })
    : selectionPlan(account, unique.map((row) => row.id), {}));
}

/**
 * runCard is Confirm on an offline, download or share card: only now the
 * existing transfer or share call is made, for the conversation's account.
 */
export async function runCard(turnId: string, cardId: string) {
  const found = assistant.conversation ? findCard(assistant.conversation.turns, cardId) : null;
  const card = found?.card;
  if (!card || card.state !== "draft" || assistant.account !== currentId.value) return;
  const account = assistant.account;
  if (card.kind === "offline") {
    await setCard(turnId, cardId, { state: "running" });
    let tasks = transfers.cloud[account]?.tasks ?? [];
    if (card.taskIds.some((id) => !tasks.some((t) => t.id === id))) tasks = (await DriveService.Tasks(account, "").catch(() => null))?.tasks ?? tasks;
    // A magnet task is added again from its info hash; the link never went to the model.
    const again = card.taskIds.map((id) => tasks.find((t) => t.id === id)?.hash ?? "").filter((h) => h);
    const results = await addOffline([...card.urls, ...again.map((h) => `magnet:?xt=urn:btih:${h}`)], card.folderId);
    const ok = results.filter((r) => r.ok).length;
    const missed = card.taskIds.length - again.length;
    let note = results.length - ok ? t("已提交 {n} 项，{failed} 项失败", { n: ok, failed: results.length - ok }) : t("已提交 {n} 项", { n: ok });
    if (missed) note = t("{text}，{n} 个任务无法重新添加", { text: note, n: missed });
    await setCard(turnId, cardId, { state: ok ? "done" : "failed", note });
    return;
  }
  if (card.kind === "download" || card.kind === "share") {
    let entries: Entry[];
    try {
      entries = await entriesOf(card.rows);
    } catch (err) {
      await setCard(turnId, cardId, { state: "failed", note: errorText(err) });
      return;
    }
    if (card.kind === "download") {
      await downloadEntries(entries);
      await setCard(turnId, cardId, { state: "done", note: t("已加入 {n} 个下载", { n: entries.length }) });
    } else {
      openCreateShare(entries);
      await setCard(turnId, cardId, { state: "done", note: t("已打开分享设置") });
    }
  }
}

/** discardCard is Discard: the card stays in the conversation, marked so. */
export async function discardCard(turnId: string, cardId: string) {
  const found = assistant.conversation ? findCard(assistant.conversation.turns, cardId) : null;
  if (!found || found.card.state !== "draft") return;
  if (found.card.kind === "plan") drafts.delete(found.card.planId);
  await setCard(turnId, cardId, { state: "cancelled", note: t("已放弃") });
}

/** openPlanCard is View Full Plan: the draft (rebuilt from the saved proposal after a restart), or the run's receipt. */
export async function openPlanCard(cardId: string) {
  const conv = assistant.conversation;
  const found = conv ? findCard(conv.turns, cardId) : null;
  if (!conv || !found || found.card.kind !== "plan" || !idle()) return;
  const card = found.card;
  if (card.recordId) {
    const record = assistant.history.find((r) => r.id === card.recordId) ?? ((await DriveService.OrganizeHistory(assistant.account).catch(() => null)) ?? []).find((r) => r.id === card.recordId);
    if (record) openAssistantRecord(record);
    else say(t("找不到这次整理的记录"), "warning");
    return;
  }
  if (card.state !== "draft") return;
  let plan = drafts.get(card.planId);
  if (!plan) {
    const proposal = conv.plans[card.planId];
    if (!proposal) {
      say(t("这份计划已无法重新生成，请再说一次要求"), "warning");
      return;
    }
    const serial = taskSerial;
    assistant.busy = t("正在重新生成计划…");
    try {
      plan =
        proposal.kind === "media"
          ? proposal.ids.length
            ? await selectionPlan(assistant.account, proposal.ids, { instructions: proposal.instructions, identities: proposal.identities, id: card.planId })
            : await localMediaPlan({ account: assistant.account, scope: proposal.scope, instructions: proposal.instructions, identities: proposal.identities, id: card.planId })
          : await changesPlan(assistant.account, proposal.title, proposal.items, card.planId);
      plan = await loadPlanReviews(assistant.account, plan);
    } catch (err) {
      if (serial === taskSerial) {
        assistant.busy = "";
        say(errorText(err), "error");
      }
      return;
    }
    if (serial !== taskSerial) return;
    assistant.busy = "";
    plan.conversation = conv.id;
    drafts.set(plan.id, plan);
  }
  try { plan = await loadPlanReviews(assistant.account, plan); }
  catch (err) { say(errorText(err), "error"); return; }
  assistant.plan = plan;
  assistant.planBack = "chat";
  assistant.screen = "plan";
  say("");
}

/* ---------- No Model Connected: the local fallback and Inline Configuration ---------- */

/**
 * localFallback answers a sentence without a model: the local index for
 * what the words name, an L0 plan when that holds media; then the connect
 * sheet offers both. Nothing goes to any model before the user connects
 * one and agrees.
 */
async function localFallback(text: string, scope: AskScope = {}) {
  const account = currentId.value;
  const serial = taskSerial;
  assistant.account = account;
  assistant.scope = snapshotScope(scope);
  assistant.prompt = "";
  assistant.busy = t("正在本机查找…");
  let found: number | null = null;
  let plan: AssistantPlan | null = null;
  let card: FilesCard | null = null;
  try {
    if (scope.entries) {
      found = scope.entries.length;
      if (scope.entries.length) plan = await selectionPlan(account, scope.entries.map((e) => e.id), {});
    } else if (scope.folder?.length) {
      plan = await localMediaPlan({ account, scope: scope.folder });
      found = plan.groups.reduce((n, g) => n + g.files.length, 0);
    } else if (indexReady.value) {
      const parsed = sentenceSearch(text, new Date());
      const result = await DriveService.SearchIndex(account, { ...parsed.query, limit: ROW_LIMIT });
      const hits = result.hits ?? [];
      found = result.total;
      card = { kind: "files", id: "", title: t("本地搜索结果"), state: "done", note: "", total: result.total, rows: cardRows(hits), aggregate: [], query: parsed.query };
      const media = hits.filter((h) => !h.folder && classify(h) !== "other").map((h) => h.id);
      if (media.length) plan = await selectionPlan(account, media, {});
    }
  } catch {
    // The sheet still offers to connect; the local part simply has nothing to show.
  }
  if (serial !== taskSerial) return;
  assistant.busy = "";
  held = { kind: "turn", text, scope, plan, card };
  assistant.connect = {
    found,
    organizable: plan ? active(plan.changes).length : null,
    text,
    hasLocal: !!plan || !!card?.rows.length,
    error: "",
    testing: false,
  };
}

/** needModel holds a task behind the connect sheet. */
function needModel(task: Held, text: string) {
  held = task;
  assistant.connect = { found: null, organizable: null, text, hasLocal: false, error: "", testing: false };
}

/** closeConnect is Close on the connect sheet: the held task is dropped, nothing was sent. */
export function closeConnect() {
  assistant.connect = null;
  held = null;
}

/** connectLocal is View Local Plan First / View Local Results First: what the index and the rules found, nothing sent. */
export async function connectLocal() {
  const h = held;
  closeConnect();
  if (h?.kind !== "turn") return;
  if (h.plan) {
    assistant.plan = h.plan;
    assistant.planBack = assistant.screen === "chat" ? "chat" : "home";
    assistant.screen = "plan";
    say("");
    return;
  }
  if (!h.card) return;
  if (!assistant.conversation || assistant.account !== currentId.value) {
    assistant.conversation = newConversationView();
    handles = new Handles();
    drafts.clear();
  }
  const conv = assistant.conversation;
  conv.scope = snapshotScope(h.scope ?? {});
  if (!conv.title) conv.title = conversationTitle(h.text);
  conv.turns.push({
    id: `t${Date.now().toString(36)}${(++planSerial).toString(36)}`,
    at: new Date().toISOString(),
    user: h.text,
    scope: snapshotScope(h.scope ?? {}),
    text: t("还没有连接模型。下面是本地索引直接找到的结果，没有发送任何内容。"),
    traces: [],
    cards: [{ ...h.card, id: `local-${planSerial}` }],
    usage: { input: 0, output: 0, calls: 0 },
    stop: "end",
    error: "",
    denied: false,
    notice: "",
    mode: "tools",
    modeNote: "",
  });
  assistant.screen = "chat";
  await saveConversation(conv);
}

/**
 * connectModel is Connect and Continue: the preset's connection and the key saved,
 * the connection tested, then the held task goes on — which still asks
 * before it sends anything.
 */
export async function connectModel(presetId: string, key: string, baseUrl?: string, model?: string): Promise<boolean> {
  const sheet = assistant.connect;
  const preset = AI_PRESETS.find((p) => p.id === presetId);
  if (!sheet || !preset || sheet.testing) return false;
  sheet.testing = true;
  sheet.error = "";
  try {
    const saved =
      (await saveAIConnection(preset.provider, (baseUrl ?? preset.baseUrl).trim(), (model ?? preset.model).trim())) &&
      (!key.trim() || (await saveAIKey(key.trim())));
    if (!saved) {
      sheet.error = t("没能保存连接设置，请检查地址和模型名。");
      return false;
    }
    await testAIConnection();
    const result = aiSettings.test;
    if (!result?.ok) {
      sheet.error = result?.message || t("连接失败，请检查 API Key 和网络。");
      return false;
    }
  } finally {
    sheet.testing = false;
  }
  const h = held;
  closeConnect();
  if (h?.kind === "turn") await startTurn(h.text, h.scope);
  else if (h?.kind === "refine") await refinePlan(h.text);
  else if (h?.kind === "identify") await identifyRemaining();
  return true;
}

/* ---------- L0: plans without a model ---------- */

/** The naming the plan renders with: Settings · AI's choice, an invalid custom template falling back per kind. */
function planNaming(): Naming {
  const s = aiSettings.value;
  return namingFor(s?.naming || "plex", { series: s?.seriesTemplate ?? "", movie: s?.movieTemplate ?? "" });
}

/** withEdits is the plan's changes with the user's ticks and names, with folders that will be empty after execution worked out afresh for media. */
function withEdits(p: AssistantPlan): Change[] {
  const edit = (c: Change): Change => {
    const pick = p.picks[c.item.id];
    const name = p.names[c.item.id];
    const next = pick === undefined && name === undefined ? c : { ...c, selected: pick ?? c.selected, name: name ?? c.name };
    return p.reviews.includes(planReviewKey(next, p)) ? { ...next, selected: false } : next;
  };
  if (p.kind === "changes") return p.base.changes.map(edit);
  const own = p.base.changes.filter((c) => c.op !== "trashIfEmpty").map(edit);
  return [...own, ...emptiedFolders(p.tree, own).map(edit)];
}

/** rebuilt is the plan built again (template, years, identities), keeping the user's ticks and names. */
function rebuilt(p: AssistantPlan, patch: Partial<AssistantPlan>): AssistantPlan {
  const next = { ...p, ...patch };
  if (next.kind === "media") {
    next.base = buildPlan(next.tree, next.groups, next.identities, {
      naming: planNaming(),
      resolutions: next.resolutions,
      years: new Set(next.years),
      missing: next.missing || undefined,
    });
    next.origin = originOf(next.identities);
    next.basis = basisOf(next.origin);
    next.lowGroups = next.low.length;
  }
  next.changes = withEdits(next);
  return markRaw(next);
}

/**
 * matchTitles asks TMDB for an exact match of every title the rules or the
 * model read (L0). TMDB unavailable is no failure: the names stay as the
 * files say, and the note says why.
 */
async function matchTitles(identities: Identity[]): Promise<{ identities: Identity[]; note: string }> {
  const queries = titleQueries(identities);
  if (!queries.length) return { identities, note: "" };
  try {
    const matches: TitleMatch[] = [];
    for (let i = 0; i < queries.length; i += 50) matches.push(...((await DriveService.MatchTitles(queries.slice(i, i + 50))) ?? []));
    return { identities: applyMatches(identities, matches), note: "" };
  } catch {
    return { identities, note: t("资料匹配不可用，名称按文件名生成。") };
  }
}

/**
 * finishPlan reads what the names depend on: the media info of versions
 * sharing an episode (Go reads at most 500), and — for a plan over some
 * items only — the destination folders, so problems() sees clashes there.
 */
async function finishPlan(account: string, plan: AssistantPlan): Promise<AssistantPlan> {
  let p = plan;
  const missing = p.base.resolutionIds.filter((id) => !p.resolutions.has(id));
  if (missing.length) {
    try {
      const found = (await DriveService.MediaResolutions(account, missing.slice(0, 500))) ?? {};
      const resolutions = new Map(p.resolutions);
      for (const [id, tier] of Object.entries(found)) if (tier) resolutions.set(id, tier);
      if (resolutions.size > p.resolutions.size) p = rebuilt(p, { resolutions });
    } catch {
      // Without media info the names keep what the file names say about the version.
    }
  }
  if (p.ids) {
    const seen = new Set<string>();
    const paths: string[][] = [];
    for (const c of active(p.changes)) {
      const key = c.toPath.join("/");
      if (c.op === "move" && c.toPath.length && !seen.has(key)) {
        seen.add(key);
        paths.push(c.toPath);
      }
    }
    if (paths.length) {
      const scopeId = p.scope.at(-1)?.id ?? "";
      const entries = (await DriveService.PlanTree(account, scopeId, p.ids, paths)) ?? [];
      p = rebuilt(p, { tree: markRaw(treeOf(entries, scopeId)) });
    }
  }
  return loadPlanReviews(account, p);
}

interface MediaSource {
  account: string;
  scope: Crumb[];
  /** A selection or an arrival; absent = the whole scope. */
  ids?: string[];
  instructions?: string;
  /** The model's identities of an earlier run of the same plan. */
  identities?: Identity[];
  arrivals?: Arrival[];
  id?: string;
}

/**
 * localMediaPlan is the L0 media organization plan: the index's tree, the code's
 * groups, the rules' identities, TMDB's exact matches, then buildPlan. No
 * model request anywhere: the groups the rules cannot name stay, and
 * identifyRemaining may ask the model about them.
 */
async function localMediaPlan(src: MediaSource): Promise<AssistantPlan> {
  const scopeId = src.scope.at(-1)?.id ?? "";
  // scopeName is data: the parser and the model read the folder's own name, All Files for the root.
  const scopeName = src.scope.at(-1)?.name ?? "全部文件"; // i18n-ignore
  const shownScope = src.scope.at(-1)?.name ?? t("全部文件");
  const entries = (src.ids ? await DriveService.PlanTree(src.account, scopeId, src.ids, []) : await DriveService.FolderTree(src.account, scopeId)) ?? [];
  const tree = markRaw(treeOf(entries, scopeId));
  const groups = groupMedia(tree, src.ids ? new Set(src.ids) : undefined);
  if (!groups.length) throw new Error(t("「{name}」里没有找到视频、字幕或音频文件", { name: shownScope }));
  const parsed = parseGroups(groups, scopeName);
  const model = new Map((src.identities ?? []).map((i) => [i.id, i]));
  const known: Identity[] = [];
  const low: string[] = [];
  for (const p of parsed) {
    const fromModel = model.get(p.id);
    if (fromModel) known.push(fromModel);
    else if (p.confidence === "high" && p.identity) known.push(p.identity);
    else low.push(p.id);
  }
  const matched = await matchTitles(known);
  let plan = rebuilt(
    {
      id: src.id ?? `p${Date.now().toString(36)}${(++planSerial).toString(36)}`,
      kind: "media",
      origin: "rules",
      title: t("影视整理 · {scope}", { scope: shownScope }),
      basis: "",
      lowGroups: 0,
      arrivals: src.arrivals?.map((a) => ({ ...a })) ?? [],
      scope: src.scope.map((c) => ({ id: c.id, name: c.name })),
      scopeName,
      task: t("影视整理 · {scope}", { scope: shownScope }),
      instructions: src.instructions ?? "",
      tree,
      groups: markRaw(groups),
      identities: matched.identities,
      resolutions: new Map(),
      years: [],
      missing: t(LOW_REASON),
      base: { changes: [], kept: [], series: [], resolutionIds: [] },
      picks: {},
      names: {},
      reviews: [],
      changes: [],
      touched: false,
      ids: src.ids ?? null,
      low,
      modelIdentities: [...model.values()],
      tokens: 0,
      conversation: "",
    },
    {},
  );
  plan = await finishPlan(src.account, plan);
  if (matched.note) planNotes.set(plan.id, matched.note);
  return plan;
}

/** A plan's one-off remark (metadata matching unavailable) the page shows when it opens it. */
const planNotes = new Map<string, string>();

/** selectionPlan is localMediaPlan over some items, in the deepest folder that holds them all. */
async function selectionPlan(account: string, ids: string[], opts: Pick<MediaSource, "instructions" | "identities" | "arrivals" | "id">): Promise<AssistantPlan> {
  const all = (await DriveService.PlanTree(account, "", ids, [])) ?? [];
  const tree = treeOf(all, "");
  const trails = ids.map((id) =>
    tree
      .ancestorsOf(id)
      .reverse()
      .map((a) => ({ id: a, name: tree.byId.get(a)!.name })),
  );
  return localMediaPlan({ account, scope: commonTrail(trails), ids, ...opts });
}

/** changesPlan is a plan of the model's propose_changes items, over the root (toPath is absolute). */
async function changesPlan(account: string, title: string, items: ProposedItem[], id?: string): Promise<AssistantPlan> {
  const seen = new Set<string>();
  const paths: string[][] = [];
  for (const it of items) {
    const key = it.toPath?.join("/") ?? "";
    if (it.toPath?.length && !seen.has(key)) {
      seen.add(key);
      paths.push(it.toPath);
    }
  }
  const entries = (await DriveService.PlanTree(account, "", items.map((i) => i.id), paths)) ?? [];
  const tree = markRaw(treeOf(entries, ""));
  const { changes } = proposedChanges(tree, items);
  return rebuilt(
    {
      id: id ?? `p${Date.now().toString(36)}${(++planSerial).toString(36)}`,
      kind: "changes",
      origin: "agent",
      title: oneLine(title) || t("整理计划"),
      basis: basisOf("agent"),
      lowGroups: 0,
      arrivals: [],
      scope: [],
      scopeName: "全部文件", // i18n-ignore: data, as in localMediaPlan
      task: oneLine(title) || t("整理计划"),
      instructions: "",
      tree,
      groups: [],
      identities: [],
      resolutions: new Map(),
      years: [],
      missing: "",
      base: { changes, kept: [], series: [], resolutionIds: [] },
      picks: {},
      names: {},
      reviews: [],
      changes: [],
      touched: false,
      ids: items.map((i) => i.id),
      low: [],
      modelIdentities: [],
      tokens: 0,
      conversation: "",
    },
    {},
  );
}

/* ---------- entries to a plan ---------- */

/** The page a plan opened from, for Back. */
function backFrom(): typeof assistant.planBack {
  if (ui.route === "files") return "files";
  if (ui.route === "transfers") return "transfers";
  if (ui.route === "assistant" && assistant.screen === "chat") return "chat";
  return "home";
}

/**
 * openPlan builds a plan for the account on screen and shows it on the
 * plan screen; a failure goes back where it came from with the reason.
 */
async function openPlan(busy: string, build: (account: string) => Promise<AssistantPlan>) {
  const account = currentId.value;
  if (!account || assistant.running || assistant.consent || assistant.live) return;
  const back = backFrom();
  dropTask();
  const serial = taskSerial;
  assistant.account = account;
  assistant.planBack = back;
  assistant.plan = null;
  assistant.receipt = null;
  assistant.confirming = false;
  assistant.screen = "plan";
  ui.route = "assistant";
  assistant.busy = busy;
  say("");
  let plan: AssistantPlan;
  try {
    plan = await loadPlanReviews(account, await build(account));
  } catch (err) {
    if (serial !== taskSerial) return;
    assistant.busy = "";
    assistant.screen = back === "chat" ? "chat" : "home";
    say(errorText(err), "error");
    return;
  }
  if (serial !== taskSerial) return;
  assistant.busy = "";
  assistant.plan = plan;
  const note = planNotes.get(plan.id);
  planNotes.delete(plan.id);
  if (note) say(note, "warning");
}

/** organizeEntries is the file page's Organize These: one folder is organized whole, else the items in their common folder. */
export async function organizeEntries(entries: Entry[]) {
  if (!entries.length) return;
  await openPlan(t("正在读取所选文件…"), async (account) =>
    entries.length === 1 && entries[0].folder
      ? localMediaPlan({ account, scope: await trailOf(entries[0].id) })
      : selectionPlan(account, entries.map((e) => e.id), {}),
  );
}

/** organizeFolder is Organize This Folder ([] = the root). */
export async function organizeFolder(trail: Crumb[]) {
  const name = trail.at(-1)?.name ?? t("全部文件");
  await openPlan(t("正在读取「{name}」及子文件夹…", { name }), (account) => localMediaPlan({ account, scope: trail }));
}

/** openRenamePlan is Rename by Rules → Generate Plan: a rename in place for every name the rules change. */
export async function openRenamePlan(entries: Entry[], rules: RenameRule[]) {
  if (!entries.length) return;
  await openPlan(t("正在生成改名计划…"), async (account) => {
    const ids = entries.map((e) => e.id);
    const all = treeOf((await DriveService.PlanTree(account, "", ids, [])) ?? [], "");
    const scope = commonTrail(ids.map((id) => all.ancestorsOf(id).reverse().map((a) => ({ id: a, name: all.byId.get(a)!.name }))));
    const scopeId = scope.at(-1)?.id ?? "";
    const tree = markRaw(treeOf((await DriveService.PlanTree(account, scopeId, ids, [])) ?? [], scopeId));
    const fresh = ids.map((id) => tree.byId.get(id)).filter((e): e is Entry => !!e);
    const changes = renameChanges(fresh, rules, (e) => tree.pathOf(e.id).slice(0, -1));
    if (!changes.length) throw new Error(t("这些规则不会改变所选项的名称"));
    const scopeName = scope.at(-1)?.name ?? "全部文件"; // i18n-ignore: data, as in localMediaPlan
    return rebuilt(
      {
        id: `p${Date.now().toString(36)}${(++planSerial).toString(36)}`,
        kind: "changes",
        origin: "rename",
        title: t("按规则改名 · {n} 项", { n: changes.length }),
        basis: basisOf("rename"),
        lowGroups: 0,
        arrivals: [],
        scope,
        scopeName,
        task: t("按规则改名 · {scope}", { scope: scope.at(-1)?.name ?? t("全部文件") }),
        instructions: "",
        tree,
        groups: [],
        identities: [],
        resolutions: new Map(),
        years: [],
        missing: "",
        base: { changes, kept: [], series: [], resolutionIds: [] },
        picks: {},
        names: {},
        reviews: [],
        changes: [],
        touched: false,
        ids,
        low: [],
        modelIdentities: [],
        tokens: 0,
        conversation: "",
      },
      {},
    );
  });
}

/* ---------- the model on a plan: identify what is left, follow-ups ---------- */

/** planRoom is what a plan's model requests may still spend: its conversation's, else the Settings · AI limit. */
function planRoom(p: AssistantPlan): { left: number; limit: number } {
  const conv = assistant.conversation;
  if (p.conversation && conv?.id === p.conversation) return { left: tokensLeft(conv.usage.limit, conv.usage.used), limit: conv.usage.limit };
  const limit = aiSettings.value?.tokenLimit ?? 50000;
  return { left: tokensLeft(limit, p.tokens), limit };
}

/** chargePlan counts what a plan's request cost, against its conversation when it has one. */
function chargePlan(p: AssistantPlan, n: number) {
  const conv = assistant.conversation;
  if (p.conversation && conv?.id === p.conversation) conv.usage.used += n;
}

/** Refused batches one identify run splits to find what a content check turns down; past it a refused batch stays whole. */
const REFUSAL_SPLITS = 15;

/**
 * identifyPlan asks the model about the plan's low groups only — the rest
 * are known without it, and a TMDB-matched group never goes out — batch by
 * batch while `room` allows, then matches the new titles on TMDB and builds
 * the plan again with the user's ticks and names. The caller has the
 * consent. A batch the service's content check turns down (MiniMax's
 * new_sensitive) is asked again in halves, down to the groups it refuses
 * one by one, so the rest is still identified; refused groups stay as
 * they are.
 */
async function identifyPlan(plan: AssistantPlan, serial: number, room: () => number, spend: (n: number) => void): Promise<AssistantPlan> {
  const asked = new Set(plan.low);
  const lowGroups = plan.groups.filter((g) => asked.has(g.id));
  const byId = new Map(lowGroups.map((g) => [g.id, g]));
  const batchesOf = (groups: readonly MediaGroup[]) => identify(groups, plan.instructions, plan.scopeName).batches;
  const queue = batchesOf(lowGroups);
  const fresh: Identity[] = [];
  const refused: string[] = [];
  let refusal: unknown = null;
  let splits = 0;
  let stopped = 0;
  let sent = 0;
  while (queue.length) {
    const b = queue[0];
    if (estimateTokens(b.request, b.expectedOutput).low > room()) {
      stopped = queue.reduce((n, x) => n + x.groups.length, 0);
      break;
    }
    queue.shift();
    const total = ++sent + queue.length;
    const step = total > 1 ? t("正在进行第 {sent}/{total} 批识别…", { sent, total }) : t("正在进行影视识别…");
    let reply: AIReply;
    try {
      reply = await complete(b.request, step);
    } catch (err) {
      if (serial !== taskSerial || !isContentRefused(err)) throw err;
      refusal ??= err;
      if (b.groups.length > 1 && splits < REFUSAL_SPLITS) {
        splits++;
        const groups = b.groups.map((id) => byId.get(id)!);
        const half = Math.ceil(groups.length / 2);
        queue.unshift(...batchesOf(groups.slice(0, half)), ...batchesOf(groups.slice(half)));
      } else refused.push(...b.groups);
      continue;
    }
    if (serial !== taskSerial) throw new Error(t("已取消"));
    spend(reply.input + reply.outputTokens);
    const inBatch = new Set(b.groups);
    fresh.push(...parseIdentities(reply.output, lowGroups.filter((g) => inBatch.has(g.id))));
  }
  const matched = await matchTitles(fresh);
  if (serial !== taskSerial) throw new Error(t("已取消"));
  const named = new Set(fresh.map((i) => i.id));
  const p = rebuilt(plan, {
    identities: [...plan.identities.filter((i) => !named.has(i.id)), ...matched.identities],
    modelIdentities: [...plan.modelIdentities.filter((i) => !named.has(i.id)), ...fresh],
    low: plan.low.filter((id) => !named.has(id)),
    missing: stopped ? t("已达到 token 上限，未识别") : refused.length ? t("模型服务的内容审核拒绝识别") : t(LOW_REASON),
  });
  const notes: string[] = [];
  if (refused.length) {
    const reason = errorText(refusal);
    const n = refused.length;
    notes.push(
      n < lowGroups.length
        ? t("{reason}。有 {n} 组没有识别，保持原样，其余照常识别；可能是这些组的文件名或路径里有它认为敏感的词，换个模型服务或改名后可以再试。", { reason, n })
        : t("{reason}。有 {n} 组没有识别，保持原样；可能是这些组的文件名或路径里有它认为敏感的词，换个模型服务或改名后可以再试。", { reason, n }),
    );
  }
  if (stopped) notes.push(t("已达到 token 上限，{n} 组没有识别，保持原样。", { n: stopped }));
  if (notes.length) planNotes.set(p.id, notes.join(""));
  return finishPlan(assistant.account, p);
}

/** identifyRemaining is Ask AI to Identify the Remaining N Groups: one consent for every batch; only those groups are sent. */
export async function identifyRemaining() {
  const p = assistant.plan;
  if (!p || p.kind !== "media" || !p.low.length || !idle() || assistant.account !== currentId.value) return;
  if (!aiReady.value) {
    needModel({ kind: "identify" }, t("识别剩下的 {n} 组", { n: p.low.length }));
    return;
  }
  const serial = taskSerial;
  const asked = new Set(p.low);
  const task = identify(
    p.groups.filter((g) => asked.has(g.id)),
    p.instructions,
    p.scopeName,
  );
  const first = estimateTokens(task.batches[0].request, task.batches[0].expectedOutput);
  const { limit } = planRoom(p);
  let used = 0;
  const room = () => planRoom(p).left - used;
  if (first.low > room()) {
    say(t("已达到 token 上限（{limit}），没有发送；剩下的 {n} 组保持原样。", { limit: formatTokens(limit), n: p.low.length }), "warning");
    return;
  }
  if (!alwaysAllowed()) {
    const go = await askConsent({
      title: t("要把剩下的 {n} 组交给模型识别吗？", { n: p.low.length }),
      lede: t("只询问这一次：所有批次一起授权。已由规则或 TMDB 识别的组不会发送。"),
      sends: task.disclosure.sends,
      estimate: first,
      limit,
    });
    if (serial !== taskSerial) return;
    if (!go) {
      say(t("未发送：保留本地结果。"));
      return;
    }
  }
  let next: AssistantPlan;
  try {
    next = await identifyPlan(p, serial, room, (n) => (used += n));
  } catch (err) {
    if (serial !== taskSerial) return;
    assistant.busy = "";
    say(t("{error}，计划保持不变", { error: errorText(err) }), "error");
    return;
  } finally {
    chargePlan(p, used);
  }
  if (serial !== taskSerial) return;
  assistant.busy = "";
  next = rebuilt(next, { tokens: p.tokens + used });
  setPlan(next);
  const note = planNotes.get(next.id);
  planNotes.delete(next.id);
  if (note) say(note, "warning");
  else say(next.low.length ? t("已识别，仍有 {n} 组无法确定，保持原样。", { n: next.low.length }) : t("已识别剩下的组，尚未修改云盘。"), "success");
}

/** setPlan shows a rebuilt plan and keeps its conversation's draft in step. */
function setPlan(p: AssistantPlan) {
  assistant.plan = p;
  if (p.conversation && drafts.has(p.id)) drafts.set(p.id, p);
}

/** refinePlan is a sentence on a media plan: the model re-identifies, the user's edits stay. */
async function refinePlan(text: string) {
  const p = assistant.plan;
  if (!p || p.kind !== "media") return;
  if (!aiReady.value) {
    needModel({ kind: "refine", text }, text);
    return;
  }
  const serial = taskSerial;
  const prepared = followUp(p.identities, p.groups, text);
  const estimate = prepared.disclosure.estimate;
  const { left, limit } = planRoom(p);
  if (estimate.low > left) {
    say(t("已达到 token 上限（{limit}），没有发送；计划保持不变。", { limit: formatTokens(limit) }), "warning");
    return;
  }
  if (!alwaysAllowed()) {
    const go = await askConsent({ title: t("要按这句话重新识别吗？"), lede: t("只询问这一次。"), sends: prepared.disclosure.sends, estimate, limit });
    if (serial !== taskSerial) return;
    if (!go) {
      say(t("未发送：保留本地结果。"));
      return;
    }
  }
  let updates: Identity[];
  let cost = 0;
  try {
    const reply = await complete(prepared.request, t("正在按补充要求重新识别…"));
    if (serial !== taskSerial) return;
    cost = reply.input + reply.outputTokens;
    updates = parseIdentities(reply.output, p.groups);
  } catch (err) {
    if (serial !== taskSerial) return;
    assistant.busy = "";
    say(t("{error}，计划保持不变", { error: errorText(err) }), "error");
    return;
  } finally {
    chargePlan(p, cost);
  }
  if (serial !== taskSerial) return;
  const now = assistant.plan;
  if (!now || now.id !== p.id) {
    assistant.busy = "";
    return;
  }
  const next = await withFollowUp(now, updates, text, cost);
  if (serial !== taskSerial) return;
  assistant.busy = "";
  if (assistant.plan?.id !== p.id) return;
  setPlan(next);
  assistant.prompt = "";
  say(updates.length ? t("已按「{text}」重新生成计划，尚未修改云盘。", { text }) : t("模型没有改动识别结果，计划保持不变。"), updates.length ? "success" : "info");
}

/**
 * withFollowUp is a plan with a follow-up's answers merged. Answers about
 * groups the model named are matched on TMDB again; a TMDB-named
 * group only takes the new top folder (mergeIdentities), so a title the
 * model never saw cannot be replaced by one it guessed.
 */
async function withFollowUp(plan: AssistantPlan, updates: Identity[], text: string, cost: number): Promise<AssistantPlan> {
  const fromModel = updates.filter((u) => {
    const before = plan.identities.find((i) => i.id === u.id);
    return !before || before.source !== "tmdb";
  });
  const matched = await matchTitles(fromModel);
  const changed = new Set(updates.map((u) => u.id));
  const byModel = new Map(fromModel.map((u) => [u.id, u]));
  const next = rebuilt(plan, {
    identities: mergeIdentities(plan.identities, [...matched.identities, ...updates.filter((u) => !byModel.has(u.id))]),
    modelIdentities: [...plan.modelIdentities.filter((i) => !byModel.has(i.id)), ...fromModel],
    low: plan.low.filter((id) => !changed.has(id)),
    instructions: [plan.instructions, text].filter(Boolean).join("；"),
    tokens: plan.tokens + cost,
  });
  return finishPlan(assistant.account, next);
}

/* ---------- editing the plan ---------- */

/** Why each ticked change may not be sent, by item id: problems() and the user's own names. */
export const planErrors = computed(() => {
  const p = assistant.plan;
  const errors = p ? problems(p.changes, p.tree) : new Map<string, string>();
  for (const c of p ? active(p.changes) : []) {
    const bad = edited(c) ? nameError(c.name) : "";
    if (bad) errors.set(c.item.id, bad);
  }
  return errors;
});

/** What the plan would do now, for the side summary, the footer and Final Confirmation. */
export const planSummary = computed(() => (assistant.plan ? summarize(assistant.plan.changes, assistant.plan.tree, planErrors.value) : null));

/** The decision key excludes model wording and includes the file and actual destination. */
export function planReviewKey(c: Change, p = assistant.plan): string {
  const e = c.item;
  return JSON.stringify([e.id, e.name, e.parentId, e.size, e.modifiedTime, c.locked ? "keep" : c.op,
    ...(c.locked ? [] : [[...(p?.scope.map((crumb) => crumb.name) ?? []), ...c.toPath], c.name])]);
}

function planItems(p: AssistantPlan): Change[] {
  return [...p.changes, ...p.base.kept];
}

async function loadPlanReviews(account: string, p: AssistantPlan): Promise<AssistantPlan> {
  const reviews = ((await DriveService.SuggestionReviews(account)) ?? []).map((r) => r.key);
  const next = { ...p, reviews };
  next.changes = withEdits(next);
  return markRaw(next);
}

export function isPlanReviewed(c: Change): boolean {
  return !!assistant.plan?.reviews.includes(planReviewKey(c));
}

export const planReviewed = computed(() => assistant.plan ? planItems(assistant.plan).filter(isPlanReviewed) : []);
export const planUndecided = computed(() => assistant.plan
  ? planItems(assistant.plan).filter((c) => !isPlanReviewed(c) && (c.locked || !c.selected || !hasChange(c))) : []);

/** A keep is durable before its row or reminder can leave the pending list. */
export async function keepPlanItems(ids: readonly string[], keep = true): Promise<void> {
  const p = assistant.plan;
  if (!p || !idle()) return;
  const account = assistant.account;
  const wanted = new Set(ids);
  const reviews = planItems(p).filter((c) => wanted.has(c.item.id)).map((c) => ({ key: planReviewKey(c, p), fileId: c.item.id }));
  if (!reviews.length) return;
  assistant.busy = t("保存处理决定");
  reviewCheckSerial++;
  try {
    await DriveService.SetSuggestionReviews(account, reviews, keep);
    if (assistant.plan?.id !== p.id || assistant.account !== account) return;
    const keys = new Set(p.reviews);
    for (const r of reviews) keep ? keys.add(r.key) : keys.delete(r.key);
    const next = { ...p, reviews: [...keys], touched: true };
    next.changes = withEdits(next);
    setPlan(markRaw(next));
    await updatePlanArrivals(account, next);
  } catch (err) {
    say(errorText(err), "error");
    throw err;
  } finally {
    if (assistant.account === account) assistant.busy = "";
  }
}

export async function keepAllPlanItems(): Promise<void> {
  const p = assistant.plan;
  if (p) await keepPlanItems(planItems(p).map((c) => c.item.id));
}

/** Only complete file coverage resolves an arrival; failure/skips never do. */
async function updatePlanArrivals(account: string, p: AssistantPlan, completed = new Set<string>(), recordId = ""): Promise<number> {
  let finished = 0;
  const reviewed = new Set(planItems(p).filter((c) => p.reviews.includes(planReviewKey(c, p))).map((c) => c.item.id));
  for (const a of p.arrivals) {
    const files = p.groups.flatMap((g) => g.files).filter((f) => f.id === a.fileId || p.tree.ancestorsOf(f.id).includes(a.fileId));
    const covered = files.length > 0 && files.every((f) => completed.has(f.id) || reviewed.has(f.id));
    const row = inbox.account === account ? inbox.arrivals.find((r) => r.id === a.id) : undefined;
    const state = covered ? (files.some((f) => completed.has(f.id)) ? "organized" : "reviewed") : "new";
    // A partial review must not reset a previously completed execution.
    if (!covered && row?.state === "organized") continue;
    if (row?.state !== state || (state === "organized" && row.recordId !== recordId)) {
      inboxLoadSerial++;
      await DriveService.SetArrival(account, a.id, state, state === "organized" ? recordId : "");
      inboxLoadSerial++;
      const current = inbox.account === account ? inbox.arrivals.find((r) => r.id === a.id) : undefined;
      if (current) { current.state = state; current.recordId = state === "organized" ? recordId : ""; }
    }
    if (covered) finished++;
    arrivalPlans.delete(a.id);
    delete inbox.summaries[a.id];
  }
  return finished;
}

function editPlan(picks: Record<string, boolean>, names: Record<string, string>) {
  const p = assistant.plan;
  if (!p || assistant.running) return;
  const next = { ...p, picks, names, touched: true };
  next.changes = withEdits(next);
  setPlan(markRaw(next));
}

/** pickPlanItems ticks or unticks items (a row, a series, a group). */
export function pickPlanItems(ids: readonly string[], on: boolean) {
  const p = assistant.plan;
  if (!p) return;
  const picks = { ...p.picks };
  for (const id of ids) picks[id] = on;
  editPlan(picks, p.names);
}

/** renamePlanItem is Rename; the plan's own name again drops the edit. */
export function renamePlanItem(id: string, name: string) {
  const p = assistant.plan;
  const change = p?.changes.find((c) => c.item.id === id);
  if (!p || !change) return;
  const names = { ...p.names };
  const wanted = name.trim();
  if (wanted === change.proposed) delete names[id];
  else names[id] = wanted;
  editPlan(p.picks, names);
}

/** togglePlanYear is Add Estimated Year for one series: every name of it follows. */
export function togglePlanYear(key: string) {
  const p = assistant.plan;
  if (!p || assistant.running) return;
  const years = p.years.includes(key) ? p.years.filter((k) => k !== key) : [...p.years, key];
  setPlan(rebuilt(p, { years }));
}

/** setPlanNaming is the plan's Naming Template: saved to Settings · AI, then the plan renders with it. */
export async function setPlanNaming(style: string) {
  const s = aiSettings.value;
  if (!s || assistant.running || !(await saveAINaming(style, s.seriesTemplate, s.movieTemplate))) return;
  if (assistant.plan) setPlan(rebuilt(assistant.plan, {}));
}

/** planBackTo is Back to Conversation / Back: where the plan was opened from; a card's draft stays with its card. */
export function planBackTo() {
  if (assistant.running) return;
  const back = assistant.planBack;
  dropTask();
  assistant.plan = null;
  assistant.confirming = false;
  say("");
  if (back === "chat" && assistant.conversation) {
    assistant.screen = "chat";
    return;
  }
  assistant.screen = "home";
  if (back === "files" || back === "transfers") ui.route = back;
}

/* ---------- Confirmation, Execution, Receipt, and Undo ---------- */

export function openPlanConfirm() {
  const s = planSummary.value;
  if (!s || s.conflicts || s.overLimit || assistant.running || assistant.busy) return;
  assistant.confirming = true;
}

export function closePlanConfirm() {
  assistant.confirming = false;
}

/** Counts of a record, for the receipt, the toast and Recent Tasks. */
export function recordCounts(r: OrganizeRecord) {
  const items = r.items ?? [];
  return {
    items: items.length,
    done: items.filter((i) => i.done && !i.error).length,
    skipped: items.filter((i) => !!i.error).length,
    created: (r.created ?? []).length,
  };
}

/** recordStatus is how Recent Tasks names a run's end. */
export function recordStatus(r: OrganizeRecord): string {
  if (r.undo) return t("已撤销");
  if (r.stopped) return t("已停止");
  if (!r.finished) return t("未完成");
  return recordCounts(r).skipped ? t("部分完成") : t("已完成");
}

/** markRecordCard puts a run's outcome on the plan card it came from, when that conversation is open. */
async function markRecordCard(match: (c: PlanCard) => boolean, patch: Partial<PlanCard>) {
  const conv = assistant.conversation;
  if (!conv) return;
  for (const t of conv.turns) {
    const card = t.cards.find((c): c is PlanCard => c.kind === "plan" && match(c));
    if (card) {
      await setCard(t.id, card.id, patch);
      return;
    }
  }
}

/**
 * runPlan is Confirm and Execute: the plan goes to its account. If another account came
 * on screen meanwhile, the run still finishes there and a toast says how.
 */
export async function runPlan(keepUndecided?: boolean) {
  let p = assistant.plan;
  const s = planSummary.value;
  if (!assistant.confirming || !p || !s || s.conflicts || s.overLimit || !idle()) return;
  if (planUndecided.value.length && keepUndecided === undefined) return;
  if (keepUndecided && planUndecided.value.length) {
    try { await keepPlanItems(planUndecided.value.map((c) => c.item.id)); } catch { return; }
    if (assistant.plan?.id !== p.id) return;
    p = assistant.plan;
  }
  const plan = p;
  const account = assistant.account;
  const changes = toChanges(p.changes);
  const kept = planReviewed.value.length;
  if (!changes.length) {
    assistant.busy = t("保存处理决定");
    try {
      await updatePlanArrivals(account, p);
      if (p.conversation && !planUndecided.value.length) {
        drafts.delete(p.id);
        await markRecordCard((c) => c.planId === p!.id, { state: "done", note: t("已确认保留") });
      }
      planBackTo();
      notify(t("处理决定已保存，云盘文件未改动"), "success");
    } catch (err) {
      say(errorText(err), "error");
    } finally {
      assistant.busy = "";
    }
    return;
  }
  assistant.confirming = false;
  assistant.running = true;
  assistant.runAccount = account;
  assistant.stopping = false;
  assistant.progress = { done: 0, total: changes.length };
  say("");
  let record: OrganizeRecord | undefined;
  try {
    record = await run(t("整理"), () => DriveService.Organize(account, { folderId: plan.scope.at(-1)?.id ?? "", task: plan.task, tokens: plan.tokens, changes }));
  } finally {
    assistant.running = false;
    assistant.stopping = false;
  }
  if (!record) return;
  countUse("organize.run");
  const done = record;
  if (account === currentId.value) void reload(false);
  const completed = new Set((done.items ?? []).filter((item) => item.done && !item.error).map((item) => item.fileId));
  const arrivalsFinished = await run(t("更新入库提醒"), () => updatePlanArrivals(account, p!, completed, done.id)) ?? 0;
  if (p.arrivals.length && account === currentId.value) void loadInbox(account);
  if (account !== currentId.value || assistant.account !== account) {
    const n = recordCounts(done);
    const name = accountName(account);
    const text = done.stopped
      ? n.skipped
        ? t("「{name}」的整理已停止：{done} 项完成，{skipped} 项跳过", { name, done: n.done, skipped: n.skipped })
        : t("「{name}」的整理已停止：{done} 项完成", { name, done: n.done })
      : n.skipped
        ? t("「{name}」的整理已完成：{done} 项完成，{skipped} 项跳过", { name, done: n.done, skipped: n.skipped })
        : t("「{name}」的整理已完成：{done} 项完成", { name, done: n.done });
    notify(text, n.skipped ? "error" : "success");
    return;
  }
  if (p.conversation) {
    drafts.delete(p.id);
    await markRecordCard((c) => c.planId === plan.id, { state: "done", recordId: done.id, note: done.stopped ? t("已停止") : "" });
  }
  assistant.plan = null;
  assistant.receipt = done;
  assistant.receiptKept = kept;
  assistant.screen = "receipt";
  const pending = p.arrivals.length - arrivalsFinished;
  if (pending) say(t("本次仍有 {n} 条入库提醒待处理：未决定或执行失败的文件不会自动标为已处理。", { n: pending }), "warning");
  void loadAssistantHistory();
}

/** stopPlanRun is Stop: the step in flight finishes, nothing after it starts. */
export async function stopPlanRun() {
  if (!assistant.running || assistant.stopping) return;
  assistant.stopping = true;
  await DriveService.StopOrganize(assistant.runAccount).catch(() => undefined);
}

/** undoAssistantRecord is Undo This Organization, checked item by item in Go. */
export async function undoAssistantRecord() {
  const r = assistant.receipt;
  if (!r || r.undo || assistant.running) return;
  const account = assistant.account || currentId.value;
  assistant.running = true;
  assistant.undoing = true;
  assistant.runAccount = account;
  assistant.stopping = false;
  assistant.progress = { done: 0, total: (r.items ?? []).length };
  let out: OrganizeRecord | undefined;
  try {
    out = await run(t("撤销整理"), () => DriveService.UndoOrganize(account, r.id));
  } finally {
    assistant.running = false;
    assistant.undoing = false;
    assistant.stopping = false;
  }
  if (!out) return;
  countUse("organize.undo");
  if (account === currentId.value) void reload(false);
  // Every arrival organized by this run waits for a decision again.
  const arrivals = inbox.account === account ? inbox.arrivals.filter((a) => a.recordId === r.id) : [];
  for (const arrival of arrivals) {
    await DriveService.SetArrival(account, arrival.id, "new", "").catch(() => undefined);
  }
  if (arrivals.length) void loadInbox(account);
  if (account !== currentId.value || assistant.receipt?.id !== r.id) {
    notify(t("「{name}」的撤销已完成", { name: accountName(account) }), "success");
    return;
  }
  await markRecordCard((c) => c.recordId === r.id, { state: "undone" });
  // A fresh object: the answer may be the very record shown, filled in.
  assistant.receipt = { ...out };
  void loadAssistantHistory();
}

/** loadAssistantHistory is Recent Tasks of the account on screen (local records, no network). */
export async function loadAssistantHistory() {
  const account = currentId.value;
  if (!account) return;
  try {
    const list = (await DriveService.OrganizeHistory(account)) ?? [];
    if (account !== currentId.value) return;
    assistant.history = list;
    assistant.historyError = "";
  } catch (err) {
    if (account === currentId.value) assistant.historyError = errorText(err);
  }
}

/** openAssistantRecord shows a Recent Tasks receipt, from where it can be undone. */
export function openAssistantRecord(record: OrganizeRecord) {
  if (assistant.busy || assistant.consent || assistant.running || assistant.live) return;
  dropTask();
  assistant.account = currentId.value;
  assistant.plan = null;
  assistant.receipt = record;
  assistant.receiptKept = 0;
  assistant.screen = "receipt";
  say("");
}

/** assistantHome is Back to AI Assistant: what waits on the model is dropped; saved conversations and drafts stay. */
export function assistantHome() {
  if (assistant.running) return;
  dropTask();
  assistant.account = currentId.value;
  assistant.scope = {};
  assistant.screen = "home";
  assistant.conversation = null;
  handles = new Handles();
  drafts.clear();
  assistant.plan = null;
  assistant.receipt = null;
  assistant.confirming = false;
  say("");
  void loadAssistantHistory();
  void loadConversations();
}

/* ---------- Organize on Ingestion ---------- */

/** The Pending Organization Suggestion of one arrival; waiting = the new files are not in the index yet. */
export interface ArrivalSummary {
  state: "loading" | "ready" | "waiting" | "error";
  files: number;
  organizable: number;
  keep: number;
  /** Where the moves go below the folder it was saved in, "Shanhai Travelogue (2025) / Season 01". */
  target: string;
  /** How it was named (the pill), and what else holds (the line). */
  basis: string;
  line: string;
  error: string;
}

export const inbox = reactive({
  account: "",
  arrivals: [] as Arrival[],
  loaded: false,
  summaries: {} as Record<string, ArrivalSummary>,
});

/** The arrivals of the account on screen that wait for a decision: the nav badge, the home card. */
export const pendingArrivals = computed(() => (inbox.account && inbox.account === currentId.value ? inbox.arrivals.filter((a) => a.state === "new") : []));

/** Plans already built for summaries, reused when the arrival is opened. */
const arrivalPlans = new Map<string, AssistantPlan>();
let inboxLoadSerial = 0;

/** loadInbox reads the account's arrivals (local). */
export async function loadInbox(account = currentId.value) {
  if (!account) return;
  const serial = ++inboxLoadSerial;
  let list: Arrival[];
  try {
    list = (await DriveService.Arrivals(account)) ?? [];
  } catch {
    return;
  }
  if (account !== currentId.value || serial !== inboxLoadSerial) return;
  if (inbox.account !== account) {
    inbox.summaries = {};
    arrivalPlans.clear();
  }
  inbox.account = account;
  inbox.arrivals = list;
  inbox.loaded = true;
  void recheckReviewedArrivals(account);
}

let reviewCheckSerial = 0;
/** Re-open only when the file inputs changed; never send a model request in the watcher. */
async function recheckReviewedArrivals(account: string) {
  if (assistant.busy || assistant.running) return;
  const serial = ++reviewCheckSerial;
  const reviewed = inbox.arrivals.filter((a) => a.state === "reviewed");
  if (!reviewed.length) return;
  try {
    const saved = (await DriveService.SuggestionReviews(account)) ?? [];
    const snapshots = new Set(saved.flatMap((r) => {
      try {
        const key: unknown = JSON.parse(r.key);
        return Array.isArray(key) ? [JSON.stringify(key.slice(0, 5))] : [];
      }
      catch { return []; }
    }));
    for (const a of reviewed) {
      const p = await arrivalPlan(a);
      if (serial !== reviewCheckSerial || account !== currentId.value) return;
      const changed = p.groups.flatMap((g) => g.files).some((f) => {
        const e = p.tree.byId.get(f.id);
        return !!e && !snapshots.has(JSON.stringify([e.id, e.name, e.parentId, e.size, e.modifiedTime]));
      });
      if (!changed || inbox.arrivals.find((r) => r.id === a.id)?.state !== "reviewed") continue;
      await DriveService.SetArrival(account, a.id, "new", "");
      const row = inbox.arrivals.find((r) => r.id === a.id);
      if (row) row.state = "new";
      arrivalPlans.delete(a.id);
      delete inbox.summaries[a.id];
    }
  } catch {
    // An unavailable index cannot turn a previous decision into a new reminder.
  }
}

/** arrivalPlan is the L0 plan for what an arrival saved, in the folder it was saved into. */
async function arrivalPlan(a: Arrival): Promise<AssistantPlan> {
  if (!a.fileId) throw new Error(t("这个任务没有产出文件"));
  const scope = await trailOf(a.parentId);
  return localMediaPlan({ account: a.account, scope, ids: [a.fileId], arrivals: [a] });
}

function waiting(err: unknown): boolean {
  const text = errorText(err);
  return text.includes(NOT_INDEXED) || text.includes(t(NOT_INDEXED));
}

/** ensureArrivalSummary works out an arrival's Pending Organization Suggestion once (again after waiting or an error). */
export async function ensureArrivalSummary(a: Arrival) {
  const now = inbox.summaries[a.id];
  if (now && (now.state === "loading" || now.state === "ready")) return;
  const empty: ArrivalSummary = { state: "loading", files: 0, organizable: 0, keep: 0, target: "", basis: "", line: "", error: "" };
  inbox.summaries[a.id] = empty;
  try {
    const plan = await arrivalPlan(a);
    arrivalPlans.set(a.id, plan);
    const keep = plan.base.kept.length;
    inbox.summaries[a.id] = {
      ...empty,
      state: "ready",
      files: plan.groups.reduce((n, g) => n + g.files.length, 0),
      organizable: active(plan.changes).filter((c) => c.op === "move").length,
      keep,
      target: planTarget(plan.changes) || plan.scopeName,
      basis: arrivalBasis(plan.origin),
      line: arrivalLine(keep, plan.base.resolutionIds.length > 0),
    };
  } catch (err) {
    inbox.summaries[a.id] = { ...empty, state: waiting(err) ? "waiting" : "error", error: errorText(err) };
  }
}

/** openArrival is View Organization Suggestion: the arrival's plan on the plan screen; Back goes back where it was opened. */
export async function openArrival(a: Arrival) {
  const ready = arrivalPlans.get(a.id);
  arrivalPlans.delete(a.id);
  await openPlan(t("正在读取新入库的文件…"), () => (ready ? Promise.resolve(ready) : arrivalPlan(a)));
}

/** Builds one reviewable plan for the selected arrivals only. */
export async function openArrivals(arrivals: Arrival[]) {
  const chosen = arrivals.filter((a) => a.account === currentId.value && a.state === "new");
  if (!chosen.length) return;
  if (chosen.length === 1) return openArrival(chosen[0]);
  await openPlan(t("正在读取新入库的文件…"), async (account) => {
    if (chosen.some((a) => !a.fileId)) throw new Error(t("这个任务没有产出文件"));
    const ids = [...new Set(chosen.map((a) => a.fileId))];
    const plan = await selectionPlan(account, ids, { arrivals: chosen });
    if (ids.some((id) => !plan.tree.byId.has(id))) throw new Error(t("部分入库文件尚未进入本地索引，请稍后重试"));
    return { ...plan, title: t("批量整理 · {n} 项入库", { n: chosen.length }), task: t("批量整理 · {n} 项入库", { n: chosen.length }) };
  });
}

/** openArrivalById is the menu bar's View Organization Suggestion: the arrival's account first, then its plan. */
export async function openArrivalById(account: string, id: string) {
  if (account !== currentId.value && !(await switchAccount(account))) return;
  await loadInbox(account);
  const a = inbox.arrivals.find((x) => x.id === id);
  if (!a) {
    ui.route = "assistant";
    notify(t("这条入库记录已不在了"));
    return;
  }
  await openArrival(a);
}

/** dismissArrival is Ignore: the arrival leaves the inbox; nothing on the drive changes. False when it could not be saved. */
export async function dismissArrival(a: Arrival): Promise<boolean> {
  const ok = await run(t("忽略入库提醒"), async () => {
    await DriveService.SetArrival(a.account, a.id, "dismissed", "");
    return true;
  });
  if (!ok) return false;
  const row = inbox.account === a.account ? inbox.arrivals.find((x) => x.id === a.id) : undefined;
  if (row) row.state = "dismissed";
  arrivalPlans.delete(a.id);
  return true;
}

/** Dismisses each selected reminder; failures stay pending and can be retried. */
export async function dismissArrivals(arrivals: Arrival[]): Promise<boolean> {
  let complete = true;
  for (const a of arrivals) if (!(await dismissArrival(a))) complete = false;
  return complete;
}


// The assistant's lists follow the page and the account on screen.
watch(
  () => [ui.route, currentId.value] as const,
  ([route, id], before) => {
    if (inPlayerWindow || inMenuBarWindow || !id) return;
    if (id !== before?.[1]) void loadInbox(id);
    if (route === "assistant") {
      void loadConversations();
      void loadAssistantHistory();
    }
  },
);

// An index pass may bring the files an arrival saved: summaries waiting for it try again.
watch(
  () => drive.indexProgress?.syncedAt,
  () => {
    if (inPlayerWindow || inMenuBarWindow || inbox.account !== currentId.value) return;
    arrivalPlans.clear();
    for (const a of inbox.arrivals) if (inbox.summaries[a.id]?.state === "ready") delete inbox.summaries[a.id];
    void recheckReviewedArrivals(currentId.value);
    for (const a of inbox.arrivals) if (inbox.summaries[a.id]?.state === "waiting") void ensureArrivalSummary(a);
  },
);

export async function copyDownloadLink(entry: Entry) {
  const link = await run(t("获取直链"), () => DriveService.DownloadLink(entry.id));
  if (!link) return;
  await copyText(link, t("下载直链"));
}

/* ---------------- Cleanup ---------------- */

/*
 * Two tabs — Duplicate Files and Empty Folders — each with its own stage and result,
 * bound to the account they were scanned for (cleanup.account). A scan
 * reads the local index only; nothing reaches the drive before the user
 * confirms. A removal goes on in its own account whatever comes on screen,
 * and what it did is reported item by item.
 */

export type CleanupTab = "dup" | "empty";
export type CleanupStage = "idle" | "scanning" | "result" | "running" | "receipt";
export type DupSort = "waste" | "copies" | "modified";

export const DUP_MIN_SIZES: { value: number; label: string }[] = [
  { value: 0, get label() { return t("全部大小"); } },
  { value: 1 << 20, get label() { return t("1 MiB 以上"); } },
  { value: 10 << 20, get label() { return t("10 MiB 以上"); } },
  { value: 100 << 20, get label() { return t("100 MiB 以上"); } },
];
export const DUP_SORTS: { key: DupSort; label: string }[] = [
  { key: "waste", get label() { return t("可回收空间优先"); } },
  { key: "copies", get label() { return t("副本数优先"); } },
  { key: "modified", get label() { return t("最近修改优先"); } },
];
/** Groups per page. */
const DUP_PAGE = 50;
/** The most one removal takes (same cap as Organize). */
export const REMOVE_LIMIT = 5000;

function readDupMin(): number {
  const saved = localStorage.getItem("pkp.dup.min");
  const value = Number(saved);
  return saved !== null && DUP_MIN_SIZES.some((c) => c.value === value) ? value : 1 << 20;
}

function readDupSort(): DupSort {
  const saved = localStorage.getItem("pkp.dup.sort");
  return DUP_SORTS.some((s) => s.key === saved) ? (saved as DupSort) : "waste";
}

/** What a removal did, for the Receipt. */
export interface CleanupReceipt {
  permanent: boolean;
  removed: number;
  failed: RemoveResult[];
  /** Bytes: freed by a permanent delete, or moved to the trash (not freed yet). */
  bytes: number;
  stopped: boolean;
}

// Go's nil slices come over as null; the page takes them in once as empty
// lists, so nothing downstream checks again.
export type DupGroupView = Omit<DupGroup, "copies"> & { copies: DupCopy[] };
export type DupPageView = Omit<DupPage, "groups"> & { groups: DupGroupView[] };
export type EmptyPageView = Omit<EmptyFolderPage, "folders"> & { folders: EmptyFolder[] };

function newDupState() {
  return {
    stage: "idle" as CleanupStage,
    loading: false,
    error: "",
    page: null as DupPageView | null,
    /** Copy id → to remove. Kept across pages and filters, so coming back finds the same ticks. */
    picked: {} as Record<string, boolean>,
    /** Group hash → the copy marked Keep This Copy. */
    keep: {} as Record<string, string>,
    /** Group hash → expanded. */
    open: {} as Record<string, boolean>,
    touched: false,
    receipt: null as CleanupReceipt | null,
  };
}

function newEmptyState() {
  return {
    stage: "idle" as CleanupStage,
    loading: false,
    error: "",
    page: null as EmptyPageView | null,
    picked: {} as Record<string, boolean>,
    touched: false,
    receipt: null as CleanupReceipt | null,
  };
}

export const cleanup = reactive({
  /** The account the scans and ticks belong to. */
  account: "",
  tab: "dup" as CleanupTab,
  /** The Scan Scope both tabs share: [] = the whole drive, else that folder and below. */
  scope: [] as Crumb[],
  minSize: readDupMin(),
  sort: readDupSort(),
  sameFolder: false,
  /** Handling Method of Duplicate Files; Empty Folders only ever go to the trash. */
  permanent: false,
  dup: newDupState(),
  empty: newEmptyState(),
  /** The confirmation on screen. */
  confirming: "" as "" | CleanupTab,
  /** The removal in flight: which tab, for which account. */
  running: "" as "" | CleanupTab,
  runAccount: "",
  stopping: false,
  progress: { done: 0, total: 0, phase: "" },
});

let dupSerial = 0;
let emptySerial = 0;

/** resetCleanup drops the scans and ticks; a removal in flight goes on and reports by toast. */
function resetCleanup() {
  dupSerial += 1;
  emptySerial += 1;
  cleanup.account = "";
  cleanup.scope = [];
  cleanup.permanent = false;
  cleanup.sameFolder = false;
  cleanup.dup = newDupState();
  cleanup.empty = newEmptyState();
  cleanup.confirming = "";
}

/** What Cleanup holds that a switch would drop, "" for nothing. */
function cleanupPending(): boolean {
  if (cleanup.account !== currentId.value) return false;
  const ticked = (tab: CleanupTab) => cleanup[tab].touched && cleanup[tab].stage === "result" && cleanup.running !== tab;
  return ticked("dup") || ticked("empty");
}

/** bindCleanup ties the page to the account on screen; another account's scans go. */
function bindCleanup() {
  if (cleanup.account === currentId.value) return;
  resetCleanup();
  cleanup.account = currentId.value;
}

/** openCleanup is the nav's Cleanup: the tab on screen is scanned the first time it is seen. */
export function openCleanup() {
  if (!leaveSavedHistory(openCleanup)) return;
  ui.route = "cleanup";
  bindCleanup();
  enterCleanupTab(cleanup.tab);
}

/** enterCleanupTab shows a tab; a tab never scanned is scanned now (Empty Folders only when opened). */
export function enterCleanupTab(tab: CleanupTab) {
  bindCleanup();
  cleanup.tab = tab;
  if (!indexReady.value || cleanup[tab].stage !== "idle" || cleanup[tab].error) return;
  void (tab === "dup" ? scanDuplicates() : scanEmptyFolders());
}

/** The Scan Scope in words. */
export const cleanupScopeLabel = computed(() => {
  const at = cleanup.scope.at(-1);
  return at ? t("「{name}」及子文件夹", { name: at.name }) : t("当前账号全盘");
});

/** openCleanupPicker opens the folder picker for the Scan Scope. */
export function openCleanupPicker() {
  if (cleanup.running) return;
  relocate.mode = "cleanup";
  relocate.account = currentId.value;
  relocate.items = [];
  relocate.working = false;
  void relocateTo(cleanup.scope.map((c) => ({ ...c })));
}

/** setCleanupScope changes the range both tabs scan, and scans again what was scanned. */
export function setCleanupScope(trail: Crumb[]) {
  if (cleanup.running) return;
  cleanup.scope = trail;
  rescanCleanup("both");
}

/** rescanCleanup is Rescan: the ticks go with the old result. */
export function rescanCleanup(which: CleanupTab | "both" = cleanup.tab) {
  if (cleanup.running) return;
  bindCleanup();
  if (which !== "empty") {
    const scanned = cleanup.dup.stage !== "idle" || which === "dup";
    cleanup.dup = newDupState();
    if (scanned) void scanDuplicates();
  }
  if (which !== "dup") {
    const scanned = cleanup.empty.stage !== "idle" || which === "empty";
    cleanup.empty = newEmptyState();
    if (scanned) void scanEmptyFolders();
  }
}

export function setDupMinSize(value: number) {
  cleanup.minSize = value;
  localStorage.setItem("pkp.dup.min", String(value));
  void scanDuplicates();
}

export function setDupSort(sort: DupSort) {
  cleanup.sort = sort;
  localStorage.setItem("pkp.dup.sort", sort);
  void scanDuplicates();
}

export function setDupSameFolder(on: boolean) {
  cleanup.sameFolder = on;
  void scanDuplicates();
}

/**
 * scanDuplicates reads one page of Duplicate Files from the index. Groups seen
 * before keep their ticks; a new group starts with Go's default keep and
 * every other copy ticked, a starred one never.
 */
export async function scanDuplicates(offset = 0) {
  bindCleanup();
  const d = cleanup.dup;
  const account = cleanup.account;
  const serial = ++dupSerial;
  if (!d.page) d.stage = "scanning";
  if (!offset) countUse("cleanup.scan");
  d.loading = true;
  d.error = "";
  try {
    const page = await DriveService.ScanDuplicates(account, {
      folderId: cleanup.scope.at(-1)?.id ?? "",
      minSize: cleanup.minSize,
      sort: cleanup.sort,
      sameFolder: cleanup.sameFolder,
      offset,
      limit: DUP_PAGE,
    });
    if (serial !== dupSerial || account !== cleanup.account) return;
    const groups = (page.groups ?? []).map((g) => ({ ...g, copies: g.copies ?? [] }));
    for (const group of groups) {
      if (group.hash in d.keep) continue;
      const keep = group.copies.find((c) => c.keep) ?? group.copies[0];
      d.keep[group.hash] = keep?.id ?? "";
      for (const copy of group.copies) d.picked[copy.id] = copy !== keep && !copy.starred;
    }
    d.page = { ...page, groups };
    d.stage = "result";
  } catch (err) {
    if (serial !== dupSerial || account !== cleanup.account) return;
    if (await sessionLost()) return;
    d.error = errorText(err);
    if (!d.page) d.stage = "idle";
  } finally {
    if (serial === dupSerial) d.loading = false;
  }
}

/** scanEmptyFolders reads the top-level empty folders; new and trash-holding ones start unticked. */
export async function scanEmptyFolders() {
  bindCleanup();
  const e = cleanup.empty;
  const account = cleanup.account;
  const serial = ++emptySerial;
  if (!e.page) e.stage = "scanning";
  countUse("cleanup.scan");
  e.loading = true;
  e.error = "";
  try {
    const page = await DriveService.ScanEmptyFolders(account, cleanup.scope.at(-1)?.id ?? "");
    if (serial !== emptySerial || account !== cleanup.account) return;
    const folders = page.folders ?? [];
    for (const f of folders) if (!(f.id in e.picked)) e.picked[f.id] = !f.recent && !f.hasTrashed;
    e.page = { ...page, folders };
    e.stage = "result";
  } catch (err) {
    if (serial !== emptySerial || account !== cleanup.account) return;
    if (await sessionLost()) return;
    e.error = errorText(err);
    if (!e.page) e.stage = "idle";
  } finally {
    if (serial === emptySerial) e.loading = false;
  }
}

/** Why a copy cannot be ticked, "" when it can. The last unticked copy of a group stays. */
export function dupLock(group: DupGroupView, id: string): string {
  const d = cleanup.dup;
  const copy = group.copies.find((c) => c.id === id);
  if (!copy) return "";
  if (copy.starred) return t("星标文件不能删除");
  if (!d.picked[id] && group.copies.filter((c) => !d.picked[c.id]).length <= 1) return t("每组至少保留一份");
  return "";
}

export function toggleDupCopy(group: DupGroupView, id: string, on: boolean) {
  if (cleanup.running || (on && dupLock(group, id))) return;
  cleanup.dup.picked[id] = on;
  cleanup.dup.touched = true;
}

/** keepDupCopy is Keep This Copy: this copy stays, every other one goes — a starred one stays too. */
export function keepDupCopy(group: DupGroupView, id: string) {
  if (cleanup.running) return;
  const d = cleanup.dup;
  d.keep[group.hash] = id;
  for (const copy of group.copies) d.picked[copy.id] = copy.id !== id && !copy.starred;
  d.touched = true;
}

/** The copies of the page on screen that are ticked: only what the user sees goes. */
export const dupPicked = computed(() => {
  const d = cleanup.dup;
  const out: { id: string; size: number }[] = [];
  for (const group of d.page?.groups ?? []) {
    for (const copy of group.copies) if (d.picked[copy.id]) out.push({ id: copy.id, size: copy.size });
  }
  return out;
});

export const dupPickedBytes = computed(() => dupPicked.value.reduce((sum, c) => sum + c.size, 0));

export const emptyPicked = computed(() => (cleanup.empty.page?.folders ?? []).filter((f) => cleanup.empty.picked[f.id]));

export function toggleEmptyFolder(id: string, on: boolean) {
  if (cleanup.running) return;
  cleanup.empty.picked[id] = on;
  cleanup.empty.touched = true;
}

/** pickAllEmpty is Select All / Deselect All. */
export function pickAllEmpty(on: boolean) {
  if (cleanup.running) return;
  for (const f of cleanup.empty.page?.folders ?? []) cleanup.empty.picked[f.id] = on;
  cleanup.empty.touched = true;
}

export function setCleanupPermanent(on: boolean) {
  if (cleanup.running) return;
  cleanup.permanent = on;
}

export function openCleanupConfirm(tab: CleanupTab) {
  const count = tab === "dup" ? dupPicked.value.length : emptyPicked.value.length;
  if (cleanup.running || !count || count > REMOVE_LIMIT) return;
  cleanup.confirming = tab;
}

export function closeCleanupConfirm() {
  cleanup.confirming = "";
}

/** stopCleanup ends a removal after the batch in flight; what was sent still finishes. */
export function stopCleanup() {
  if (!cleanup.running || cleanup.stopping) return;
  cleanup.stopping = true;
  void DriveService.StopRemove(cleanup.runAccount).catch(() => undefined);
}

/**
 * runRemoval carries out the confirmed removal of a tab for the account it
 * was scanned for and reports what happened; returns the report, or null
 * when nothing was done.
 */
async function runRemoval(tab: CleanupTab, ids: string[], call: () => Promise<RemoveReport>, phase: string) {
  const account = cleanup.account;
  const state = cleanup[tab];
  cleanup.confirming = "";
  cleanup.running = tab;
  cleanup.runAccount = account;
  cleanup.stopping = false;
  cleanup.progress = { done: 0, total: ids.length, phase };
  state.stage = "running";
  let report: RemoveReport | null = null;
  try {
    report = await call();
  } catch (err) {
    if (!(await sessionLost())) notify(t("没能处理：{error}", { error: errorText(err) }), "error");
  } finally {
    cleanup.running = "";
    cleanup.stopping = false;
  }
  // Another account came on screen meanwhile: its page never shows this one's result.
  const here = cleanup.account === account && cleanup[tab] === state;
  if (here && !report) state.stage = "result";
  return { report, here, account };
}

/** removeDuplicates carries out Move to Trash / Delete Permanently of the ticked copies on screen. */
export async function removeDuplicates() {
  const picked = dupPicked.value;
  if (cleanup.running || !picked.length || picked.length > REMOVE_LIMIT) return;
  const permanent = cleanup.permanent;
  const ids = picked.map((c) => c.id);
  const size = new Map(picked.map((c) => [c.id, c.size]));
  const account = cleanup.account;
  const { report, here } = await runRemoval(
    "dup",
    ids,
    () => DriveService.RemoveDuplicates(account, ids, permanent),
    permanent ? "delete" : "trash",
  );
  if (!report) return;
  countUse("cleanup.remove");
  const removed = report.removed ?? [];
  notifyRemoval(permanent ? (n) => t("已彻底删除 {n} 项", { n }) : (n) => t("已移到回收站 {n} 项", { n }), report);
  if (account === currentId.value) {
    removeEntries(removed);
    if (permanent) refreshQuotaSoon();
  }
  if (!here) return;
  const d = cleanup.dup;
  const gone = new Set(removed);
  const bytes = removed.reduce((sum, id) => sum + (size.get(id) ?? 0), 0);
  if (d.page) {
    // The groups that went lose those copies; one left with a single copy is no longer a duplicate.
    let groupsGone = 0;
    let copiesGone = 0;
    const groups = d.page.groups
      .map((g) => {
        const copies = g.copies.filter((c) => !gone.has(c.id));
        copiesGone += g.copies.length - copies.length;
        return { ...g, copies, count: copies.length, waste: g.size * Math.max(0, copies.length - 1) };
      })
      .filter((g) => {
        if (g.copies.length > 1) return true;
        groupsGone += 1;
        return false;
      });
    d.page = {
      ...d.page,
      groups,
      total: Math.max(0, d.page.total - groupsGone),
      copies: Math.max(0, d.page.copies - copiesGone),
      waste: Math.max(0, d.page.waste - bytes),
    };
  }
  for (const id of removed) delete d.picked[id];
  d.touched = false;
  d.receipt = { permanent, removed: removed.length, failed: report.failed ?? [], bytes: permanent ? report.freed : bytes, stopped: report.stopped };
  d.stage = "receipt";
}

/** removeEmptyFolders moves the ticked top-level empty folders to the trash, each re-checked by Go first. */
export async function removeEmptyFolders() {
  const picked = emptyPicked.value;
  if (cleanup.running || !picked.length || picked.length > REMOVE_LIMIT) return;
  const ids = picked.map((f) => f.id);
  const nested = new Map(picked.map((f) => [f.id, f.nested]));
  const account = cleanup.account;
  const { report, here } = await runRemoval("empty", ids, () => DriveService.RemoveEmptyFolders(account, ids), "check");
  if (!report) return;
  countUse("cleanup.remove");
  const removed = report.removed ?? [];
  notifyRemoval((n) => t("已移到回收站 {n} 个空文件夹", { n }), report);
  if (account === currentId.value) removeEntries(removed);
  if (!here) return;
  const e = cleanup.empty;
  const gone = new Set(removed);
  if (e.page) {
    const inside = removed.reduce((sum, id) => sum + 1 + (nested.get(id) ?? 0), 0);
    e.page = { ...e.page, folders: e.page.folders.filter((f) => !gone.has(f.id)), total: Math.max(0, e.page.total - inside) };
  }
  for (const id of removed) delete e.picked[id];
  e.touched = false;
  e.receipt = { permanent: false, removed: removed.length, failed: report.failed ?? [], bytes: 0, stopped: report.stopped };
  e.stage = "receipt";
}

/** backToCleanupResult leaves the Receipt for what is left of the result. */
export function backToCleanupResult(tab: CleanupTab) {
  const state = cleanup[tab];
  state.receipt = null;
  state.stage = state.page ? "result" : "idle";
}

/* ---------------- File Viewing ---------------- */

/**
 * One viewer for every file the page opens without the player: pictures,
 * PDFs, Markdown, text, and — saying so — the rest. It takes the canvas
 * while the file page keeps its state behind it: folder, filter, scroll and
 * selection all survive Back. The file being viewed is the selection, so the
 * context column describes it; playback carries on in the bar, untouched.
 */
export const viewer = reactive({
  windowId: 0,
  detaching: false,
  returning: false,
  entry: null as Entry | null,
  kind: "unsupported" as ViewerKind,
  identity: "",
  comic: false,
  // The opening folder's previewable files, independent of file-page filters.
  siblings: [] as Entry[],
  folderId: "",
  source: "",
  sort: "name" as SortKey,
  asc: true,
  folderLoading: false,
  folderError: "",
  navigation: localStorage.getItem("pkp.viewerNavigation") === "1",
  // Pictures and PDFs, on the loopback proxy.
  url: "",
  // Keep loaded image nodes mounted; browser HTTP caching is not guaranteed.
  images: [] as { id: string; version: string; name: string; url: string; natural: { w: number; h: number } | null; bytes: number }[],
  // Markdown and text: the head of the file, decoded by Go.
  text: null as TextPreview | null,
  loading: false,
  error: "",
  // A picture's own pixels, once decoded; the column shows them when the
  // server gave none.
  natural: null as { w: number; h: number } | null,
  // Text: long lines wrap (kept across launches).
  wrap: localStorage.getItem("pkp.textWrap") !== "0",
  // The route the viewer was opened from, restored by closeViewer, and the
  // entry it opened on (stepping away from it reveals the new one on return).
  returnRoute: "files" as Route,
  openedId: "",
});

let viewerSerial = 0;
let viewerFolderSerial = 0;

function viewerImageVersion(entry: Entry) {
  return `${entry.modifiedTime}:${entry.size}`;
}

function releaseViewerImage(url: string) {
  viewer.images = viewer.images.filter((image) => image.url !== url);
  void DriveService.ClosePreview(url).catch(() => undefined);
}

/** Retain only successfully loaded images, within this viewer's account/session. */
export function rememberViewerImage(url: string, w: number, h: number) {
  const entry = viewer.entry;
  if (!entry || viewer.kind !== "image" || viewer.url !== url) return;
  const natural = w && h ? { w, h } : null;
  viewer.natural = natural;
  if (viewer.images.some((image) => image.url === url)) return;
  viewer.images.push({
    id: entry.id, version: viewerImageVersion(entry), name: entry.name, url, natural,
    bytes: Math.max(0, entry.size) + w * h * 4,
  });
  // LRU: 8 images / ~128 MiB, except the currently displayed image itself.
  let bytes = viewer.images.reduce((sum, image) => sum + image.bytes, 0);
  while (viewer.images.length > 1 && (viewer.images.length > 8 || bytes > 128 * 1024 * 1024)) {
    const oldest = viewer.images[0];
    bytes -= oldest.bytes;
    releaseViewerImage(oldest.url);
  }
}

export async function openComic(entry: Entry) {
  if (entry.folder) {
    // Keep the real folder identity for windows and reading-state persistence.
    await openViewer({ ...entry, folder: false, parentId: entry.id, preview: "comic" });
  } else {
    const identity = await DriveService.ViewerIdentity();
    await openViewer(entry);
    if (viewer.entry?.id !== entry.id) return;
    viewer.identity = `${identity}:${entry.parentId}:comic`;
    viewer.comic = true;
  }
}

export async function openViewer(entry: Entry, siblings?: Entry[]) {
  if (entry.folder) return;
  const newFolder = !viewer.entry || viewer.folderId !== entry.parentId;
  if (ui.route !== "viewer") {
    viewer.returnRoute = ui.route;
    viewer.openedId = entry.id;
    countUse("viewer.open");
  }
  if (newFolder) {
    viewer.folderId = entry.parentId;
    viewer.sort = ui.sortKey;
    viewer.asc = ui.sortAsc;
    viewer.source = entry.path || (drive.trail.at(-1)?.id === entry.parentId
      ? [t("全部文件"), ...drive.trail.map((c) => c.name)].join(" / ")
      : t("所在文件夹"));
    viewer.siblings = (siblings ?? visibleFiles.value).filter((e) => e.preview && e.parentId === entry.parentId);
    if (!viewer.siblings.some((e) => e.id === entry.id)) viewer.siblings.push(entry);
  }
  viewer.entry = entry;
  viewer.comic = false;
  viewer.kind = viewerKind(entry);
  ui.route = "viewer";
  ui.workspace = "files";
  if (!inViewerWindow) {
    select(entry);
    ui.panelTab = "details";
  }
  if (newFolder) void loadViewerFolder();
  await loadViewer(false);
}

/** Read every folder page, independently of file-page filters and selection. */
export async function loadViewerFolder() {
  const serial = ++viewerFolderSerial;
  const parentId = viewer.folderId;
  const rows = new Map<string, Entry>();
  let offset = 0;
  let pageToken = "";
  viewer.folderLoading = true;
  viewer.folderError = "";
  try {
    do {
      const page = await DriveService.ViewerList({
        view: "all", parentId, query: "", category: "all",
        sort: viewer.sort, asc: viewer.asc, offset, pageToken,
      });
      if (serial !== viewerFolderSerial) return;
      const entries = page.entries ?? [];
      for (const row of entries) if (!row.folder && row.preview) rows.set(row.id, row);
      if (viewer.entry && !rows.has(viewer.entry.id)) rows.set(viewer.entry.id, viewer.entry);
      viewer.siblings = sortEntries([...rows.values()], viewer.sort, viewer.asc);
      offset = page.offset + entries.length;
      const next = page.nextPageToken;
      if (page.live ? !next : offset >= page.total || entries.length === 0) break;
      if (page.live && next === pageToken) throw new Error(t("文件列表未能继续加载"));
      pageToken = next;
    } while (serial === viewerFolderSerial);
  } catch (err) {
    if (serial === viewerFolderSerial) viewer.folderError = viewerError(err);
  } finally {
    if (serial === viewerFolderSerial) viewer.folderLoading = false;
  }
}

export function toggleViewerNavigation() {
  viewer.navigation = !viewer.navigation;
  localStorage.setItem("pkp.viewerNavigation", viewer.navigation ? "1" : "0");
}

function viewerSnapshot(): ViewerWindowState {
  return {
    windowId: viewer.windowId, entry: viewer.entry!, siblings: [...viewer.siblings],
    source: viewer.source, sort: viewer.sort, asc: viewer.asc, comic: viewer.comic,
  };
}

export async function popOutViewer() {
  if (inViewerWindow || !viewer.entry || viewer.detaching) return;
  const serial = viewerSerial;
  const account = currentId.value;
  const state = viewerSnapshot();
  viewer.detaching = true;
  try {
    const opened = await run(t("在独立窗口查看"), async () => {
      await DriveService.OpenViewerWindow(state);
      return true;
    });
    // A delayed reply must not close a different file the user opened meanwhile.
    if (opened && serial === viewerSerial && currentId.value === account) closeViewer();
  } finally {
    viewer.detaching = false;
  }
}

export async function returnViewerToMain() {
  if (!inViewerWindow || !viewer.entry || viewer.returning) return;
  viewer.returning = true;
  const sent = await run(t("回到主窗口查看"), async () => {
    await DriveService.ReturnViewerToMain(viewerSnapshot());
    return true;
  });
  if (!sent) viewer.returning = false;
}

function applyViewerSnapshot(state: ViewerWindowState) {
  viewer.windowId = inViewerWindow ? state.windowId : 0;
  viewer.entry = state.entry;
  viewer.comic = state.comic ?? false;
  viewer.kind = viewerKind(state.entry);
  viewer.siblings = state.siblings ?? [];
  viewer.folderId = state.entry.parentId;
  viewer.source = state.source;
  viewer.sort = state.sort as SortKey;
  viewer.asc = state.asc;
  ui.route = "viewer";
  void loadViewerFolder();
  void loadViewer(false);
}

let viewerReturnChain: Promise<unknown> = Promise.resolve();

/** Receive a detached document on its owning account before closing its window. */
export function restoreViewer(request: ViewerReturnRequest): Promise<unknown> {
  if (inViewerWindow || inPlayerWindow || inMenuBarWindow) return Promise.resolve();
  viewerReturnChain = viewerReturnChain.then(() => run(t("回到主窗口查看"), async () => {
    let restored = false;
    try {
      if (currentId.value !== request.account && !(await switchAccount(request.account))) return;
      if (currentId.value !== request.account || accounts.switching || drive.status?.needsPassword) return;
      if (player.presentation === "immersive") collapsePlayer();
      if (viewer.entry) closeViewer();
      viewer.returnRoute = ui.route;
      viewer.openedId = request.state.entry.id;
      ui.workspace = "files";
      select(request.state.entry);
      ui.panelTab = "details";
      applyViewerSnapshot(request.state);
      await nextTick();
      restored = true;
    } finally {
      await DriveService.FinishViewerReturn(request.state.windowId, restored);
    }
  }));
  return viewerReturnChain;
}

let viewerReturnListening = false;

export async function bootstrapViewerWindow() {
  document.documentElement.dataset.theme = ui.theme;
  document.documentElement.dataset.skin = ui.skin;
  applyTransparency();
  if (!viewerReturnListening) {
    viewerReturnListening = true;
    Events.On(ViewerReturnCancelledEvent, (event: { data: number }) => {
      if (event.data === viewer.windowId) viewer.returning = false;
    });
  }
  applyViewerSnapshot(await DriveService.ViewerWindow());
}

/** Navigation reuses loaded images; an explicit retry always reads a fresh URL. */
export async function loadViewer(fresh = true) {
  const serial = ++viewerSerial;
  const entry = viewer.entry;
  const previous = viewer.url;
  if (previous && !viewer.images.some((image) => image.url === previous)) {
    void DriveService.ClosePreview(previous).catch(() => undefined);
  }
  for (const image of [...viewer.images]) {
    if (image.id === entry?.id && (fresh || image.version !== viewerImageVersion(entry))) releaseViewerImage(image.url);
  }
  viewer.url = "";
  viewer.text = null;
  viewer.natural = null;
  viewer.error = "";
  viewer.loading = false;
  if (!entry || viewer.kind === "unsupported") return;
  const cached = viewer.kind === "image" && !viewer.comic ? viewer.images.find((image) => image.id === entry.id) : undefined;
  if (cached) {
    viewer.images = [...viewer.images.filter((image) => image !== cached), cached];
    viewer.natural = cached.natural;
    viewer.url = cached.url;
    return;
  }
  viewer.loading = true;
  try {
    if (viewer.comic || (viewer.kind !== "image" && viewer.kind !== "text" && viewer.kind !== "markdown")) {
      const identity = await DriveService.ViewerIdentity();
      if (serial !== viewerSerial) return;
      viewer.identity = viewer.comic ? `${identity}:${entry.parentId}:comic` : `${identity}:${entry.id}:${entry.modifiedTime}:${entry.size}`;
    }
    if (viewer.comic || (viewer.kind === "comic" && entry.preview === "comic")) return;
    if (viewer.kind !== "markdown" && viewer.kind !== "text") {
      const url = await DriveService.PreviewFile(entry.id);
      if (serial === viewerSerial) viewer.url = url;
      else void DriveService.ClosePreview(url).catch(() => undefined);
    } else {
      const text = await DriveService.ReadText(entry.id);
      if (serial === viewerSerial) viewer.text = text;
    }
  } catch (err) {
    if (serial !== viewerSerial || (!inViewerWindow && await sessionLost())) return;
    viewer.error = viewerError(err);
  } finally {
    if (serial === viewerSerial) viewer.loading = false;
  }
}

export function closeViewer() {
  viewerFolderSerial += 1;
  viewer.folderLoading = false;
  viewer.folderError = "";
  const previous = viewer.url;
  viewerSerial += 1;
  const id = viewer.entry?.id ?? "";
  viewer.entry = null;
  viewer.siblings = [];
  viewer.url = "";
  viewer.text = null;
  viewer.natural = null;
  viewer.error = "";
  viewer.loading = false;
  const retained = viewer.images.some((image) => image.url === previous);
  for (const image of [...viewer.images]) releaseViewerImage(image.url);
  if (previous && !retained) void DriveService.ClosePreview(previous).catch(() => undefined);
  if (inViewerWindow) {
    void Window.Close();
    return;
  }
  // The player opened over the viewer comes back to where the viewer would.
  if (player.returnRoute === "viewer") player.returnRoute = viewer.returnRoute;
  if (ui.route !== "viewer") return;
  ui.route = viewer.returnRoute;
  // Stepped to another file: the list shows where that one is, after it
  // has put its own scroll back.
  if (id && id !== viewer.openedId && ui.route === "files") requestAnimationFrame(() => filesPlace.reveal(id));
}

// Another page taking the canvas (the nav, Show in Folder, a transfer's
// View) puts the viewer away and revokes its proxy link; only the immersive
// player keeps it mounted beneath itself.
watch(
  () => ui.route,
  (route) => {
    if (route !== "viewer" && route !== "player" && viewer.entry) closeViewer();
  },
);

export function stepViewer(delta: number) {
  if (!viewer.entry) return;
  const i = viewer.siblings.findIndex((e) => e.id === viewer.entry!.id);
  const next = i < 0 ? undefined : viewer.siblings[i + delta];
  if (next) void openViewer(next);
}

export function setTextWrap(wrap: boolean) {
  viewer.wrap = wrap;
  localStorage.setItem("pkp.textWrap", wrap ? "1" : "0");
}

/* ---------------- shares ---------------- */

export const shares = reactive({
  list: [] as ShareSummary[],
  nextPageToken: "",
  loading: false,
  loadingMore: false,
  selectedId: "",
  // The create-share sheet: the entries one link is for (empty = closed),
  // and the result once made.
  targets: [] as Entry[],
  created: null as CreatedShare | null,
  creating: false,
});

export const selectedShare = computed(() => shares.list.find((s) => s.id === shares.selectedId) ?? null);
export const hasMoreShares = computed(() => !!shares.nextPageToken);

// One status pill vocabulary for the table and the island. An expired or
// cancelled share is not reachable any more; the pill says so instead of
// hiding the row.
export function shareStatus(share: ShareSummary): { tone: "ok" | "warn" | "off"; text: string } {
  switch (share.status) {
    case "active":
      if (share.daysLeft < 0) return { tone: "ok", text: t("永久有效") };
      if (share.daysLeft === 0) return { tone: "warn", text: t("今天到期") };
      return { tone: "warn", text: t("剩余 {n} 天", { n: share.daysLeft }) };
    case "expired":
      return { tone: "off", text: t("已过期") };
    case "canceled":
      return { tone: "off", text: t("已取消") };
    default:
      return { tone: "off", text: share.statusText || t("不可访问") };
  }
}

export async function openShares() {
  if (!leaveSavedHistory(() => void openShares())) return;
  ui.route = "shares";
  if (!shares.list.length) await loadShares();
}

export async function loadShares() {
  const account = accountKey();
  shares.loading = true;
  try {
    const page = await run(t("分享列表"), () => DriveService.ListShares(""));
    if (!page || account !== accountKey()) return;
    shares.list = page.shares ?? [];
    shares.nextPageToken = page.nextPageToken;
    if (!shares.list.some((s) => s.id === shares.selectedId)) shares.selectedId = "";
  } finally {
    shares.loading = false;
  }
}

export async function loadMoreShares() {
  if (!shares.nextPageToken || shares.loadingMore) return;
  const account = accountKey();
  shares.loadingMore = true;
  try {
    const page = await run(t("分享列表"), () => DriveService.ListShares(shares.nextPageToken));
    if (!page || account !== accountKey()) return;
    shares.list = [...shares.list, ...(page.shares ?? [])];
    shares.nextPageToken = page.nextPageToken;
  } finally {
    shares.loadingMore = false;
  }
}

function shareLinkText(share: { url: string; passCode: string }): string {
  return share.passCode ? t("{url} 提取码：{code}", { url: share.url, code: share.passCode }) : share.url;
}

/** copyShareLink copies the link plus access code; a fresh CreatedShare has no status yet. */
export async function copyShareLink(share: { url: string; passCode: string; status?: string }) {
  const text = shareLinkText(share);
  const ok = await copyText(text, t("分享链接"));
  if (ok && share.status && share.status !== "active") notify(t("已复制，但这个分享已不可访问"), "info");
}

export async function copyShareLinks(items: ShareSummary[]): Promise<void> {
  if (!items.length) return;
  const text = items.map((share) => `${share.title}\n${shareLinkText(share)}`).join("\n\n");
  const ok = await copyText(text, t("分享链接"));
  const inactive = items.filter((share) => share.status && share.status !== "active").length;
  if (ok && inactive) notify(t("已复制，但其中 {n} 个分享已不可访问", { n: inactive }), "info");
}

/** cancelShares stops the links for good; the caller has confirmed. */
export async function cancelShares(items: ShareSummary[]): Promise<void> {
  const ids = new Set(items.filter((share) => share.id && share.status !== "canceled").map((share) => share.id));
  if (!ids.size) {
    if (items.length) notify(t("所选分享均已取消"), "info");
    return;
  }
  const account = accountKey();
  const ok = await run(t("取消分享"), async () => {
    await DriveService.CancelShares([...ids]);
    return true;
  });
  if (!ok || account !== accountKey()) return;
  shares.list = shares.list.filter((share) => !ids.has(share.id));
  if (ids.has(shares.selectedId)) shares.selectedId = "";
  notify(t("已取消 {n} 个分享，链接已失效", { n: ids.size }), "success");
}

export function openCreateShare(entries: Entry[]) {
  if (!entries.length) return;
  shares.targets = [...entries];
  shares.created = null;
  shares.creating = false;
}

export function closeCreateShare() {
  shares.targets = [];
  shares.created = null;
  shares.creating = false;
}

export async function createShare(req: ShareRequest) {
  if (shares.creating) return;
  shares.creating = true;
  try {
    const created = await run(t("创建分享"), () => DriveService.CreateShare(req));
    if (!created) return;
    countUse("share.create");
    shares.created = created;
    // The list is stale now; the next visit fetches it afresh.
    shares.list = [];
  } finally {
    shares.creating = false;
  }
}

/* ---------------- connections (WebDAV, apps) ---------------- */

export const connections = reactive({
  webdav: null as WebDAVInfo | null,
  apps: [] as ConnectedApp[],
  // This month's transfer allowance, and what each connected client and
  // session drew from it. Stays null when the account service refuses.
  quota: null as TransferQuota | null,
  // The account's cloud-download preferences (Settings · Transfers).
  prefs: null as TransferPreferences | null,
  // This computer's settings the Go side acts on (Settings · Transfers · Local Uploads and Download to Computer).
  local: null as LocalSettings | null,
  loading: false,
  // The WebDAV client whose details the right island shows.
  selectedClient: "",
});

export async function loadConnections() {
  const account = accountKey();
  connections.loading = true;
  try {
    const [webdav, apps, quota] = await Promise.all([
      run("WebDAV", () => DriveService.WebDAV()),
      run(t("已连接应用"), async () => (await DriveService.ConnectedApps()) ?? []),
      run(t("传输配额"), () => DriveService.TransferQuota()),
    ]);
    if (account !== accountKey()) return;
    if (webdav) connections.webdav = webdav;
    if (apps) connections.apps = apps;
    if (quota) connections.quota = quota;
  } finally {
    connections.loading = false;
  }
}

async function reloadApps() {
  const account = accountKey();
  const apps = await run(t("已连接应用"), async () => (await DriveService.ConnectedApps()) ?? []);
  if (apps && account === accountKey()) connections.apps = apps;
}

export async function loadTransferSettings() {
  const account = accountKey();
  const [prefs, quota, local] = await Promise.all([
    run(t("传输偏好"), () => DriveService.TransferPreferences()),
    run(t("传输配额"), () => DriveService.TransferQuota()),
    run(t("本机设置"), () => DriveService.LocalSettings()),
  ]);
  if (local) {
    connections.local = local;
    accounts.keepPlaying = local.keepPlayingOnSwitch;
  }
  if (account !== accountKey()) return;
  if (prefs) connections.prefs = prefs;
  if (quota) connections.quota = quota;
}

/**
 * setResumableUploads switches Resumable Uploads on this computer. The list is read
 * again because whether a stopped upload can continue depends on it.
 */
export async function setResumableUploads(on: boolean) {
  const local = connections.local;
  if (!local || local.resumableUploads === on) return;
  local.resumableUploads = on;
  const ok = await run(t("保存断点续传设置"), async () => {
    await DriveService.SetResumableUploads(on);
    return true;
  });
  if (!ok) {
    local.resumableUploads = !on;
    return;
  }
  notify(on ? t("已开启断点续传 · 只影响这台电脑") : t("已关闭断点续传 · 只影响这台电脑"), "success");
  void loadUploads();
}

/** The choices for Concurrent Uploads; the Go side enforces the same 1–5 range. */
export const PARALLEL_UPLOAD_CHOICES = [1, 2, 3, 4, 5];

/**
 * setParallelUploads sets how many files upload at once on this computer.
 * Uploads already moving keep going; queued ones follow the new count.
 */
export async function setParallelUploads(n: number) {
  const local = connections.local;
  if (!local || local.parallelUploads === n) return;
  const was = local.parallelUploads;
  local.parallelUploads = n;
  const ok = await run(t("保存同时上传数"), async () => {
    await DriveService.SetParallelUploads(n);
    return true;
  });
  if (!ok) {
    local.parallelUploads = was;
    return;
  }
  notify(t("同时上传 {n} 个文件 · 只影响这台电脑", { n }), "success");
}

/** The choices for Concurrent Downloads; the Go side enforces the same 1–5 range. */
export const PARALLEL_DOWNLOAD_CHOICES = [1, 2, 3, 4, 5];
/** The choices for Threads per File: the CDN serves one link to at most 8 connections. */
export const DOWNLOAD_THREAD_CHOICES = [1, 2, 4, 8];

/**
 * setParallelDownloads sets how many files download at once on this
 * computer. Downloads under way keep going; queued ones follow the new count.
 */
export async function setParallelDownloads(n: number) {
  const local = connections.local;
  if (!local || local.parallelDownloads === n) return;
  const was = local.parallelDownloads;
  local.parallelDownloads = n;
  const ok = await run(t("保存同时下载数"), async () => {
    await DriveService.SetParallelDownloads(n);
    return true;
  });
  if (!ok) {
    local.parallelDownloads = was;
    return;
  }
  notify(t("同时下载 {n} 个文件 · 只影响这台电脑", { n }), "success");
}

/** setDownloadThreads sets how many connections each file opens; files under way keep theirs. */
export async function setDownloadThreads(n: number) {
  const local = connections.local;
  if (!local || local.downloadThreads === n) return;
  const was = local.downloadThreads;
  local.downloadThreads = n;
  const ok = await run(t("保存下载线程数"), async () => {
    await DriveService.SetDownloadThreads(n);
    return true;
  });
  if (!ok) {
    local.downloadThreads = was;
    return;
  }
  notify(t("每个文件 {n} 个线程 · 只影响这台电脑", { n }), "success");
}

/** pickDownloadDir asks for the folder Download to Computer saves into, on this computer. */
export async function pickDownloadDir() {
  const path = await Dialogs.OpenFile({
    Title: t("选择下载位置"),
    CanChooseFiles: false,
    CanChooseDirectories: true,
    CanCreateDirectories: true,
    ButtonText: t("选择"),
    Directory: connections.local?.downloadDir,
  });
  if (!path) return;
  const ok = await run(t("更改下载位置"), async () => {
    await DriveService.SetDownloadDir(path);
    return true;
  });
  if (!ok) return;
  if (connections.local) connections.local.downloadDir = path;
  notify(t("下载位置已改为 {path}", { path }), "success");
}

export async function revealDownloadDir() {
  await run(t("打开下载文件夹"), () => DriveService.RevealDownloadDir());
}

const preferenceNotices: Record<keyof TransferPreferences, (on: boolean) => string> = {
  separateAppFolders: (on) => (on ? t("已开启分文件夹保存 · 随账号同步到所有 PikPak 客户端") : t("已关闭分文件夹保存 · 随账号同步到所有 PikPak 客户端")),
  smartFilter: (on) => (on ? t("已开启智能过滤 · 随账号同步到所有 PikPak 客户端") : t("已关闭智能过滤 · 随账号同步到所有 PikPak 客户端")),
  autoParseTorrent: (on) => (on ? t("已开启自动解析 Torrent · 随账号同步到所有 PikPak 客户端") : t("已关闭自动解析 Torrent · 随账号同步到所有 PikPak 客户端")),
};

/**
 * setTransferPreference flips one account preference at once and says where
 * it applies; the switch moves back if the server did not take it.
 */
export async function setTransferPreference(name: keyof TransferPreferences, on: boolean) {
  const prefs = connections.prefs;
  if (!prefs || prefs[name] === on) return;
  prefs[name] = on;
  const ok = await run(t("保存传输偏好"), async () => {
    await DriveService.SetTransferPreference(name, on);
    return true;
  });
  if (!ok) {
    prefs[name] = !on;
    return;
  }
  notify(preferenceNotices[name](on), "success");
}

/**
 * revokeSessions removes connections from the account. The list is read
 * again whatever happened: a partly failed batch still removed some.
 */
export async function revokeSessions(ids: string[], what: string) {
  const ok = await run(t("移除连接"), async () => {
    await DriveService.RevokeSessions(ids);
    return true;
  });
  await reloadApps();
  if (ok) notify(t("已移除「{what}」，它将立即无法访问你的 PikPak", { what }), "success");
  return !!ok;
}

/** createAccessToken issues a personal access token; null when it failed. */
export async function createAccessToken(opts: AccessTokenOptions): Promise<AccessToken | null> {
  const token = await run(t("创建令牌"), () => DriveService.CreateAccessToken(opts));
  if (!token) return null;
  await reloadApps();
  return token;
}

/** regenerateAccessToken replaces a token in place; the old one stops working. */
export async function regenerateAccessToken(sessionId: string): Promise<AccessToken | null> {
  const token = await run(t("重新生成令牌"), () => DriveService.RegenerateAccessToken(sessionId, 0));
  if (!token) return null;
  await reloadApps();
  return token;
}

export async function setWebDAVEnabled(enabled: boolean) {
  const ok = await run(enabled ? t("启用 WebDAV") : t("停用 WebDAV"), async () => {
    await DriveService.SetWebDAVEnabled(enabled);
    return true;
  });
  if (ok && connections.webdav) connections.webdav.enabled = enabled;
}

export async function addWebDAVClient(name: string) {
  const client = await run(t("添加客户端"), () => DriveService.AddWebDAVClient(name));
  if (!client) return null;
  await loadConnections();
  connections.selectedClient = client.username;
  notify(t("已添加客户端「{name}」", { name: client.name }), "success");
  return client;
}

export async function updateWebDAVClient(client: WebDAVClient, opts: WebDAVClientOptions) {
  const ok = await run(t("更新客户端"), async () => {
    await DriveService.UpdateWebDAVClient(client.username, client.password, opts);
    return true;
  });
  if (!ok) return false;
  Object.assign(client, { name: opts.name, readOnly: opts.readOnly, mediaOptimized: opts.mediaOptimized, maxEntries: opts.maxEntries });
  return true;
}

export async function renewWebDAVPassword(client: WebDAVClient) {
  const renewed = await run(t("更换密码"), () => DriveService.RenewWebDAVPassword(client.username, client.password));
  if (!renewed) return;
  client.password = renewed.password || client.password;
  notify(t("密码已更换，使用旧密码的客户端将无法连接"), "success");
}

export async function removeWebDAVClient(client: WebDAVClient) {
  const ok = await run(t("删除客户端"), async () => {
    await DriveService.RemoveWebDAVClient(client.username, client.password);
    return true;
  });
  if (!ok || !connections.webdav) return;
  connections.webdav.clients = (connections.webdav.clients ?? []).filter((c) => c.username !== client.username);
  if (connections.selectedClient === client.username) connections.selectedClient = "";
  notify(t("已删除客户端「{name}」", { name: client.name }), "success");
}

/* ---------------- transfers ---------------- */

/**
 * Where the transfer centre opens when another page sends the user there
 * for something in particular — the Overview's Needs Attention, say (`key`: the task
 * whose details open, TransferItem.key). The page
 * reads it once when it mounts and clears it.
 */
export const transferFocus = reactive({
  source: "" as "" | "cloud" | "upload" | "download",
  filter: "" as "" | TransferFilter,
  key: "",
  /** All Accounts when opened for every account's tasks (the menu bar's Open Transfer Center). */
  scope: "" as "" | "current" | "all",
});

export function openTransfers(
  source: "" | "cloud" | "upload" | "download" = "",
  filter: "" | TransferFilter = "",
  key = "",
  scope: "" | "current" | "all" = "",
) {
  if (!leaveSavedHistory(() => openTransfers(source, filter, key, scope))) return;
  transferFocus.source = source;
  transferFocus.filter = filter;
  transferFocus.key = key;
  transferFocus.scope = scope;
  ui.route = "transfers";
}

/**
 * openFromMenuBar does what the menu bar panel asked once Go has brought the
 * window back (Menu Bar Resident): Open File is the file page where it was
 * left, Open Transfer Center every account's tasks with the panel's one open, Cloud Add the
 * usual sheet into the folder on screen of the account on screen, View Organization Suggestions
 * the assistant's plan for the arrival. Every return of the window passes
 * here, the Dock's too ("app").
 */
async function openFromMenuBar(open: MenuBarOpen) {
  // "app" is also every Dock click and second launch; the rest were picked in the panel.
  if (open.target !== "app") countUse("menubar.open");
  // A window closed in full screen left it on the way out; the layout follows.
  if (ui.fullscreen) {
    try {
      if (!(await Window.IsFullscreen())) ui.fullscreen = false;
    } catch {
      /* no native window */
    }
  }
  if (open.target === "app" || !drive.status?.accountId || accounts.switching) return;
  if (open.target !== "sync" && !drive.status.configured) return;
  if (open.target !== "cloudAdd" && !leaveSavedHistory(() => void openFromMenuBar(open))) return;
  if (ui.route === "player") collapsePlayer();
  switch (open.target) {
    case "sync":
      ui.route = "sync";
      break;
    case "files":
      ui.route = "files";
      break;
    case "transfers": {
      const source = open.source === "cloud" || open.source === "upload" || open.source === "download" ? open.source : "";
      openTransfers(source, "", open.key, "all");
      break;
    }
    case "cloudAdd":
      ui.cloudAddOpen = true;
      break;
    case "assistant": {
      // The event names only the arrival; the panel's state says whose it is
      // (it may be another account's: the panel counts every account's).
      const state = await MenuBarService.State().catch(() => null);
      const item = state?.inboxItem;
      await openArrivalById(item && item.id === open.key ? item.account : currentId.value, open.key);
      break;
    }
  }
}

/** One account's cloud tasks, or an empty list never read. */
export function cloudOf(account: string): CloudTasks {
  return transfers.cloud[account] ?? { tasks: [], loading: false, loaded: 0, updatedAt: "" };
}

function cloudEntry(account: string): CloudTasks {
  transfers.cloud[account] ??= { tasks: [], loading: false, loaded: 0, updatedAt: "" };
  return transfers.cloud[account];
}

/** The cloud tasks of the account on screen. */
export const currentTasks = computed(() => cloudOf(currentId.value).tasks);
/** The local uploads of the account on screen. */
export const currentUploads = computed(() => drive.uploads.filter((t) => t.account === currentId.value));
/** The downloads to this computer of the account on screen. */
export const currentDownloads = computed(() => drive.downloads.filter((t) => t.account === currentId.value));

// The walk in progress per account. A walk carries on while another account
// is on screen: its list is that account's, kept for when it is shown.
const walks = new Map<string, { again: boolean; done: Promise<void> }>();
// The next poll per account, while one of its tasks is pending or running.
const polls = new Map<string, number>();

/**
 * loadTasks reads every page of an account's cloud tasks (the account on
 * screen by default); an account keeps thousands, a few seconds' walk. A
 * call while a walk of the account is under way joins it instead of
 * restarting it, unless `changed` says the list moved since that walk began
 * (tasks added or removed); then another walk follows it. A list left with
 * a task still moving is polled until none is (pollTasks).
 */
export function loadTasks(changed = false, account = currentId.value): Promise<void> {
  if (!account) return Promise.resolve();
  const running = walks.get(account);
  if (running) {
    running.again ||= changed;
    return running.done;
  }
  const walk = { again: false, done: Promise.resolve() };
  walk.done = (async () => {
    let expiresIn = 0;
    try {
      do {
        walk.again = false;
        expiresIn = await walkTasks(account);
      } while (walk.again && accountById(account));
    } finally {
      walks.delete(account);
    }
    schedulePoll(account, expiresIn);
  })();
  walks.set(account, walk);
  return walk.done;
}

/**
 * walkTasks is one pass over every page of an account's tasks. The first
 * load shows each page as it lands. A refresh keeps the rows on screen and
 * swaps in the new list once it is whole, so the list never shrinks to one
 * page and grows back. A page that fails keeps what was there; only the
 * account on screen says so in a toast. Returns the server's expires_in, 0
 * when the pass did not finish.
 */
async function walkTasks(account: string): Promise<number> {
  const list = cloudEntry(account);
  const progressive = list.tasks.length === 0;
  list.loading = true;
  list.loaded = 0;
  try {
    const all: TaskSummary[] = [];
    let token = "";
    let expiresIn = 0;
    do {
      const from = token;
      const page =
        account === currentId.value
          ? await run(t("传输列表"), () => DriveService.Tasks(account, from))
          : await DriveService.Tasks(account, from).catch(() => undefined);
      // Removed meanwhile: its list is gone with it.
      if (!page || transfers.cloud[account] !== list) return 0;
      all.push(...(page.tasks ?? []));
      list.loaded = all.length;
      if (progressive) list.tasks = all.slice();
      expiresIn = page.expiresIn;
      // A token that does not move would walk forever.
      token = page.nextPageToken === token ? "" : page.nextPageToken;
    } while (token);
    list.tasks = all;
    list.updatedAt = new Date().toISOString();
    return expiresIn;
  } finally {
    list.loading = false;
  }
}

/**
 * schedulePoll arms the account's next poll when its list holds a task
 * still pending or running, after as long as the server said its reading
 * stays fresh; otherwise the account is not polled. Signed-out and removed
 * accounts are not polled: their sessions cannot read.
 */
function schedulePoll(account: string, expiresIn: number) {
  window.clearTimeout(polls.get(account));
  polls.delete(account);
  const a = accountById(account);
  if (!a || a.needsPassword || !transfers.cloud[account]?.tasks.some(taskMoving)) return;
  polls.set(
    account,
    window.setTimeout(() => void pollTasks(account), pollDelay(expiresIn)),
  );
}

/**
 * pollTasks reads only the account's moving tasks (the server filters by
 * phase: one short request, not the whole walk) and lays their progress
 * over the list. When one finished or failed, or one was added elsewhere,
 * the list is read again in full, which arms the next poll itself, and the
 * account's index is asked for a pass so the files a task just saved are
 * on the file page without waiting for the next scheduled one. A walk
 * under way already will; a failed poll tries again on the same pace,
 * quietly, as nobody asked for it.
 */
async function pollTasks(account: string) {
  polls.delete(account);
  const list = transfers.cloud[account];
  if (!list || walks.has(account)) return;
  const page = await DriveService.ActiveTasks(account).catch(() => undefined);
  if (transfers.cloud[account] !== list || walks.has(account) || polls.has(account)) return;
  if (!page) {
    schedulePoll(account, 0);
    return;
  }
  const merged = mergeMoving(list.tasks, page.tasks ?? []);
  if (!merged) {
    void loadTasks(true, account);
    void DriveService.RequestSync(account).catch(() => undefined);
    return;
  }
  list.tasks = merged;
  list.updatedAt = new Date().toISOString();
  schedulePoll(account, page.expiresIn);
}

/** loadAllTasks reads the cloud tasks of every signed-in account, for All Accounts. */
export function loadAllTasks(changed = false): Promise<void> {
  return Promise.all(accounts.list.filter((a) => !a.needsPassword).map((a) => loadTasks(changed, a.id))).then(() => undefined);
}

/** addOffline submits a cloud-add batch to the account on screen. */
export async function addOffline(urls: string[], parentId: string) {
  const account = currentId.value;
  const results = await run(t("云添加"), () => DriveService.AddOfflineTasks(account, urls, parentId));
  if (!results) return [];
  countUse("cloud_add");
  drive.lastBatch = results ?? [];
  const ok = drive.lastBatch.filter((r) => r.ok).length;
  const failed = drive.lastBatch.length - ok;
  notify(
    failed === 0 ? t("已提交 {n} 项", { n: ok }) : t("提交 {n} 项，{failed} 项失败", { n: ok, failed }),
    failed === 0 ? "success" : "error",
  );
  // Not awaited: the whole list takes seconds to walk, and the sheet that
  // submitted is done once the server has the tasks.
  void loadTasks(true, account);
  return drive.lastBatch;
}

/**
 * pickTorrent opens a .torrent from this computer and lists what is inside
 * it. The file is read here, not uploaded: Go turns it into the magnet link
 * the drive API takes and hands back the files it names, all of them picked
 * to start with.
 */
export async function pickTorrent(): Promise<void> {
  const path = await Dialogs.OpenFile({
    Title: t("选择种子文件"),
    Message: t("选择一个 .torrent 文件"),
    ButtonText: t("打开"),
    CanChooseFiles: true,
    CanChooseDirectories: false,
    Filters: [{ DisplayName: t("种子文件"), Pattern: "*.torrent" }],
  });
  if (!path) return;
  cloudAdd.reading = true;
  const meta = await run(t("读取种子"), () => DriveService.ReadTorrent(path));
  cloudAdd.reading = false;
  if (!meta) return;
  cloudAdd.torrent = meta;
  cloudAdd.picked = new Set((meta.files ?? []).map((f) => f.index));
}

/** clearTorrent drops the picked torrent and its selection. */
export function clearTorrent(): void {
  cloudAdd.torrent = null;
  cloudAdd.picked.clear();
}

/** pickCloudAddFolder is the Cloud Add sheet's Change: the folder picker, opened at the folder chosen now. */
export function pickCloudAddFolder(): void {
  if (!ui.cloudAddOpen) return;
  relocate.mode = "cloudAdd";
  relocate.account = currentId.value;
  relocate.items = [];
  relocate.working = false;
  void relocateTo(cloudAdd.dest.map((c) => ({ ...c })));
}

/**
 * addTorrent creates one cloud-add task for the picked files of the torrent
 * on screen, in the account on screen. It reports whether the server took
 * it, so the sheet can stay open with its selection on a failure.
 */
export async function addTorrent(parentId: string): Promise<boolean> {
  const torrent = cloudAdd.torrent;
  if (!torrent || !cloudAdd.picked.size) return false;
  const account = currentId.value;
  const files = [...cloudAdd.picked].sort((a, b) => a - b);
  const result = await run(t("云添加"), () => DriveService.AddTorrent(account, torrent.path, files, parentId));
  if (!result) return false;
  if (!result.ok) {
    notify(t("云添加：{error}", { error: result.error }), "error");
    return false;
  }
  drive.lastBatch = [result];
  notify(t("已提交「{name}」", { name: result.name || torrent.name }), "success");
  // Not awaited, as in addOffline: the sheet is done once the server has it.
  void loadTasks(true, account);
  return true;
}

/** removeTask drops a cloud task from its account's list; deleteFiles also discards what it saved. */
export async function removeTask(item: TransferItem, deleteFiles: boolean) {
  const ok = await run(t("删除任务"), async () => {
    await DriveService.RemoveTasks(item.account, [item.id], deleteFiles);
    return true;
  });
  if (!ok) return;
  const list = transfers.cloud[item.account];
  if (list) list.tasks = list.tasks.filter((t) => t.id !== item.id);
}

export function uploadActive(t: UploadTask): boolean {
  return t.state === "queued" || t.state === "uploading";
}

export function downloadActive(t: DownloadTask): boolean {
  return t.state === "queued" || t.state === "downloading";
}

/** Cloud tasks still running plus local uploads and downloads still in the queue, of the account on screen. */
export const activeTaskCount = computed(
  () =>
    currentTasks.value.filter((t) => t.phase !== "PHASE_TYPE_COMPLETE" && t.phase !== "PHASE_TYPE_ERROR").length +
    currentUploads.value.filter(uploadActive).length +
    currentDownloads.value.filter(downloadActive).length,
);

/* ---------------- uploads ---------------- */

export async function loadUploads() {
  drive.uploads = (await run(t("上传列表"), async () => (await DriveService.Uploads()) ?? [])) ?? drive.uploads;
}

// Finder drops are queued by the backend and only show up here through
// events, so the toast lives on arrival: one per burst, not one per file.
let newUploads = 0;
let newUploadsTimer = 0;
// Accounts removed in this run: an upload event of theirs still in flight
// must not bring a row back.
const removedAccounts = new Set<string>();

function applyUpload(task: UploadTask) {
  if (removedAccounts.has(task.account)) return;
  const i = drive.uploads.findIndex((t) => t.account === task.account && t.id === task.id);
  if (task.state === "done" && (i < 0 || drive.uploads[i].state !== "done") && task.account === currentId.value) {
    refreshQuotaSoon();
    // publishUpload already requests sync; read indexed rows only after it lands.
    if (indexReady.value) refreshPending = true;
    else void reload(false);
  }
  if (i >= 0) {
    drive.uploads[i] = task;
    return;
  }
  drive.uploads.push(task);
  newUploads += 1;
  window.clearTimeout(newUploadsTimer);
  newUploadsTimer = window.setTimeout(() => {
    notify(t("已加入 {n} 个上传", { n: newUploads }), "success");
    newUploads = 0;
  }, 300);
}

const uploadPickers = {
  files: { get Title() { return t("选择要上传的文件"); }, CanChooseFiles: true, CanChooseDirectories: false },
  folders: { get Title() { return t("选择要上传的文件夹"); }, CanChooseFiles: false, CanChooseDirectories: true },
  either: { get Title() { return t("选择要上传的文件或文件夹"); }, CanChooseFiles: true, CanChooseDirectories: true },
};

/**
 * pickAndUpload asks for files, folders or either and queues them for the
 * folder on screen when the picker opened, in the account it belongs to.
 */
export async function pickAndUpload(what: keyof typeof uploadPickers) {
  const account = currentId.value;
  const parentId = currentFolderId.value;
  const paths = await Dialogs.OpenFile({
    ...uploadPickers[what],
    Message: t("上传到「{name}」", { name: currentFolderName.value }),
    ButtonText: t("上传"),
    AllowsMultipleSelection: true,
  });
  if (!paths?.length) return;
  const added = await run(t("上传"), () => DriveService.UploadFiles(account, paths, parentId));
  for (const task of added ?? []) applyUpload(task);
  if (added?.length) countUse("upload");
  if (added?.length && account === currentId.value) ui.route = "transfers";
}

/**
 * createFolder makes a folder in the current folder and shows it there,
 * selected. The sheet that asked for the name closes once the server took
 * it; on a failure it stays open with the name, under the error notice.
 */
export async function createFolder(name: string) {
  const parentId = currentFolderId.value;
  const entry = await run(t("新建文件夹"), () => DriveService.CreateFolder(currentId.value, parentId, name));
  if (!entry) return;
  ui.newFolderOpen = false;
  notify(t("已新建文件夹「{name}」", { name: entry.name }), "success");
  // Already looking at the folder: refresh in place. Anywhere else (another
  // page, Starred, a search) go to where it was made. Neither is awaited: the
  // sheet's work ends when the server has the folder.
  if (ui.route === "files" && ui.view === "all" && !ui.query.trim() && currentFolderId.value === parentId) {
    void reload(false).then(() => {
      const row = drive.entries.find((e) => e.id === entry.id);
      if (row) select(row);
    });
  } else {
    void openFolderAt(parentId, entry.id);
  }
}

/** The (account, id) pair that names one upload or download. */
type TransferRef = Pick<TransferItem, "account" | "id">;

function isTransfer(t: TransferRef, ref: TransferRef): boolean {
  return t.account === ref.account && t.id === ref.id;
}

export async function cancelUpload(item: TransferRef) {
  await run(t("取消上传"), () => DriveService.CancelUpload(item.account, item.id));
}

export async function retryUpload(item: TransferRef) {
  await run(t("重试上传"), () => DriveService.RetryUpload(item.account, item.id));
}

export async function removeUpload(item: TransferRef) {
  const ok = await run(t("移除上传"), async () => {
    await DriveService.RemoveUpload(item.account, item.id);
    return true;
  });
  if (!ok) return;
  drive.uploads = drive.uploads.filter((t) => !isTransfer(t, item));
}

/* ---------------- downloads ---------------- */

export async function loadDownloads() {
  drive.downloads = (await run(t("下载列表"), async () => (await DriveService.Downloads()) ?? [])) ?? drive.downloads;
}

// Downloads arrive through events as well as DownloadFiles' answer, so the
// toast lives on arrival: one per burst, not one per file.
let newDownloads = 0;
let newDownloadsTimer = 0;

function applyDownload(task: DownloadTask) {
  if (removedAccounts.has(task.account)) return;
  const i = drive.downloads.findIndex((t) => isTransfer(t, task));
  if (i >= 0) {
    drive.downloads[i] = task;
    return;
  }
  drive.downloads.push(task);
  newDownloads += 1;
  window.clearTimeout(newDownloadsTimer);
  newDownloadsTimer = window.setTimeout(() => {
    notify(t("已加入 {n} 个下载", { n: newDownloads }), "success");
    newDownloads = 0;
  }, 300);
}

/**
 * downloadEntries saves entries of the account on screen into the download
 * folder; a folder comes whole, read on the Go side first. The page stays
 * where it is: the toast and the nav badge say what happened.
 */
export async function downloadEntries(entries: Entry[]) {
  if (!entries.length) return;
  const account = currentId.value;
  if (entries.some((e) => e.folder)) {
    notify(entries.length === 1 ? t("正在读取「{name}」里的文件…", { name: entries[0].name }) : t("正在读取 {n} 项里的文件…", { n: entries.length }));
  }
  const added = await run(t("下载"), () => DriveService.DownloadFiles(account, entries.map(downloadItem)));
  for (const task of added ?? []) applyDownload(task);
  if (added?.length) countUse("download");
}

const downloadItem = (e: Entry) => ({ id: e.id, name: e.name, size: e.size, folder: e.folder, parentId: e.parentId });

/**
 * downloadPlaying saves the video playing into the download folder, queued
 * under the account it plays from, which need not be the one on screen.
 * Failures throw: the player says so on its stage, as neither the immersive
 * player nor the separate window shows StatusPill.
 */
export async function downloadPlaying(): Promise<string> {
  const entry = nowPlaying.value;
  if (!entry) return "";
  const added = await DriveService.DownloadFiles(playingOwner.value?.id ?? "", [downloadItem(entry)]);
  for (const task of added ?? []) applyDownload(task);
  countUse("download");
  return entry.name;
}

export async function pauseDownload(item: TransferRef) {
  await run(t("暂停下载"), () => DriveService.PauseDownload(item.account, item.id));
}

export async function resumeDownload(item: TransferRef) {
  await run(t("继续下载"), () => DriveService.ResumeDownload(item.account, item.id));
}

export async function restartDownload(item: TransferRef) {
  await run(t("重新下载"), () => DriveService.RestartDownload(item.account, item.id));
}

export async function cancelDownload(item: TransferRef) {
  await run(t("取消下载"), () => DriveService.CancelDownload(item.account, item.id));
}

export async function openDownload(item: TransferRef) {
  await run(t("打开文件"), () => DriveService.OpenDownload(item.account, item.id));
}

export async function revealDownload(item: TransferRef) {
  await run(t("打开所在文件夹"), () => DriveService.RevealDownload(item.account, item.id));
}

export async function removeDownload(item: TransferRef) {
  const ok = await run(t("移除下载"), async () => {
    await DriveService.RemoveDownload(item.account, item.id);
    return true;
  });
  if (!ok) return;
  drive.downloads = drive.downloads.filter((t) => !isTransfer(t, item));
}

/** pauseDownloads stops picked downloads; what they have on disk stays for Resume. */
export async function pauseDownloads(items: TransferItem[]) {
  await eachTransfer(items, (n) => t("已暂停 {n} 个下载", { n }), (item) => DriveService.PauseDownload(item.account, item.id));
}

/** resumeDownloads continues picked downloads that kept bytes on disk and restarts the rest. */
export async function resumeDownloads(items: TransferItem[]) {
  const label = retryLabel(items);
  // retryLabel's words are data (transfers.ts). i18n-ignore
  const kept = label === "继续" ? "all" : label === "重试" ? "none" : "some";
  await eachTransfer(
    items,
    (n) => (kept === "all" ? t("已继续 {n} 个下载", { n }) : kept === "none" ? t("已重试 {n} 个下载", { n }) : t("已继续或重试 {n} 个下载", { n })),
    (item) => DriveService.ResumeDownload(item.account, item.id),
  );
}

/** cancelDownloads stops picked downloads for good and deletes what they had on disk. */
export async function cancelDownloads(items: TransferItem[]) {
  await eachTransfer(items, (n) => t("已取消 {n} 个下载", { n }), (item) => DriveService.CancelDownload(item.account, item.id));
}

/** removeDownloads drops picked records; finished files stay where they are. */
export async function removeDownloads(items: TransferItem[]) {
  const gone = await eachTransfer(items, (n) => t("已移除 {n} 条记录", { n }), (item) => DriveService.RemoveDownload(item.account, item.id));
  drive.downloads = drive.downloads.filter((t) => !gone.some((item) => isTransfer(t, item)));
}

/* ---------------- batch actions on the transfer list ---------------- */

/**
 * reasonOf shortens a backend error to what fits in a list of failures. Go
 * wraps errors as "op path: cause"; the item is already named, so the cause
 * is the part worth showing.
 */
function reasonOf(err: unknown): string {
  const text = err instanceof Error ? err.message : String(err);
  const cause = text.slice(text.lastIndexOf(": ") + 1).trim() || text;
  return cause.length > 48 ? `${cause.slice(0, 47)}…` : cause;
}

/** failedText adds which items of a batch did not go through, and why, to what did. */
function failedText(done: string, failed: string[]): string {
  const shown = failed.slice(0, 2).join(t("、"));
  const names = failed.length > 2 ? t("{names} 等 {n} 项", { names: shown, n: failed.length }) : shown;
  return t("{done}，{n} 项没有成功：{names}", { done, n: failed.length, names });
}

/**
 * eachTransfer makes one queue call per picked upload or download, each
 * through its own account, and reports item by item: how many went through,
 * and which did not and why — a batch never claims a success it did not
 * have. It returns the items that went through.
 */
async function eachTransfer(items: TransferItem[], said: (n: number) => string, call: (item: TransferItem) => Promise<unknown>) {
  const results = await Promise.allSettled(items.map((item) => call(item)));
  const done: TransferItem[] = [];
  const failed: string[] = [];
  results.forEach((result, i) => {
    if (result.status === "fulfilled") done.push(items[i]);
    else failed.push(t("{name}（{reason}）", { name: items[i].name, reason: reasonOf(result.reason) }));
  });
  if (!failed.length) notify(said(done.length), "success");
  else notify(failedText(said(done.length), failed), "error");
  return done;
}

/** cancelUploads stops picked uploads; what they sent is discarded, Resumable Upload or not. */
export async function cancelUploads(items: TransferItem[]) {
  await eachTransfer(items, (n) => t("已取消 {n} 个上传", { n }), (item) => DriveService.CancelUpload(item.account, item.id));
}

/** retryUploads continues picked uploads that kept their progress and restarts the rest. */
export async function retryUploads(items: TransferItem[]) {
  const label = retryLabel(items);
  // retryLabel's words are data (transfers.ts). i18n-ignore
  const kept = label === "继续" ? "all" : label === "重试" ? "none" : "some";
  await eachTransfer(
    items,
    (n) => (kept === "all" ? t("已继续 {n} 个上传", { n }) : kept === "none" ? t("已重试 {n} 个上传", { n }) : t("已继续或重试 {n} 个上传", { n })),
    (item) => DriveService.RetryUpload(item.account, item.id),
  );
}

/** removeUploads drops picked records; a placeholder one kept for Resumable Upload goes to the trash. */
export async function removeUploads(items: TransferItem[]) {
  const gone = await eachTransfer(items, (n) => t("已移除 {n} 条记录", { n }), (item) => DriveService.RemoveUpload(item.account, item.id));
  drive.uploads = drive.uploads.filter((t) => !gone.some((item) => isTransfer(t, item)));
}

/**
 * removeTasks drops picked cloud tasks; deleteFiles also discards what they
 * saved. A long selection goes in chunks of 100, and one that fails leaves
 * the earlier chunks done, so the list is read again rather than guessed.
 */
export async function removeTasks(items: TransferItem[], deleteFiles: boolean) {
  // One call per account: a task is removed through the account it belongs to.
  const byAccount = new Map<string, string[]>();
  for (const item of items) byAccount.set(item.account, [...(byAccount.get(item.account) ?? []), item.id]);
  let removed = 0;
  for (const [account, ids] of byAccount) {
    const ok = await run(deleteFiles ? t("删除任务和文件") : t("移除记录"), async () => {
      await DriveService.RemoveTasks(account, ids, deleteFiles);
      return true;
    });
    if (!ok) {
      void loadTasks(true, account);
      return;
    }
    const gone = new Set(ids);
    const list = transfers.cloud[account];
    if (list) list.tasks = list.tasks.filter((t) => !gone.has(t.id));
    removed += ids.length;
  }
  notify(deleteFiles ? t("已删除 {n} 个任务和它们保存的文件", { n: removed }) : t("已移除 {n} 条记录 · 云盘里的文件不动", { n: removed }), "success");
}

// Opens folderId on the file page with a full breadcrumb, then selects
// selectId and scrolls to it, reading on through the folder's pages until
// it turns up (a few thousand rows at most); past that, the folder alone
// shows.
async function openFolderAt(folderId: string, selectId: string) {
  if (!leaveSavedHistory(() => void openFolderAt(folderId, selectId))) return;
  const account = currentId.value;
  const switchVersion = switchSerial;
  const trail = await run(t("打开所在文件夹"), () => DriveService.FolderTrail(folderId));
  if (!trail || account !== currentId.value || switchVersion !== switchSerial || accounts.switching) return;
  if (ui.scope === "drive" && ui.route === "files" && ui.view === "all") scopeAnchors.drive = filesPlace.save();
  ui.view = "all";
  ui.scope = "folder";
  ui.route = "files";
  ui.workspace = "files";
  ui.query = "";
  drive.trail = trail.map((c) => ({ id: c.id, name: c.name }));
  clearSelection();
  await reload(true);
  // Clearing the box queued a refetch of its own; this one already answers it.
  clearTimeout(queryTimer);
  const serial = listSerial;
  const find = () => (selectId ? drive.entries.find((e) => e.id === selectId) : undefined);
  let entry = find();
  for (let pages = 0; selectId && !entry && hasMore.value && pages < 25 && serial === listSerial; pages++) {
    await loadMore();
    entry = find();
  }
  if (serial !== listSerial) return;
  if (entry) {
    select(entry);
    filesPlace.reveal(entry.id);
  }
  if (entry || (!selectId && drive.entries.length) || drive.live || !indexReady.value) return;
  // The index catches up with the drive one pass at a time (every 2 min):
  // what a cloud task has just saved is on the server and not in the index
  // yet, so its folder reads empty. Pull the changes in now, as Refresh does.
  pendingPick = { folder: folderId, id: selectId };
  refreshPending = true;
  await DriveService.RequestSync("").catch(() => {
    refreshPending = false;
  });
}

/**
 * showAccount puts a transfer's account on screen before the drive is
 * opened for it: View in Cloud Drive of another account's task switches explicitly;
 * merely reading its details never does.
 */
async function showAccount(account: string): Promise<boolean> {
  return !account || account === currentId.value || (await switchAccount(account));
}

/** Opens a cloud directory only after its owning account is on screen. */
export async function openCloudFolder(account: string, folderId: string) {
  if (!(await showAccount(account)) || currentId.value !== account || !drive.status?.configured || accounts.switching) return;
  await openFolderAt(folderId, "");
}

/**
 * revealFile opens the folder a transferred file is in now and selects it.
 * The folder is read from the file rather than remembered from the
 * transfer, so a file moved since still opens in the right place.
 */
export async function revealFile(item: Pick<TransferItem, "account" | "fileId">) {
  if (!(await showAccount(item.account))) return;
  const detail = await run(t("打开所在文件夹"), () => DriveService.FileDetail(item.fileId));
  if (detail) await openFolderAt(detail.parentId, item.fileId);
}

/**
 * locateEntry is Locate in Containing Folder of a whole-drive or search row:
 * the page goes to Current Directory at the entry's direct parent, with it picked.
 */
export async function locateEntry(entry: Entry) {
  await openFolderAt(entry.parentId, entry.id);
}

/** viewFile plays, previews or opens what a transfer produced; anything else is shown in its folder. */
export async function viewFile(item: Pick<TransferItem, "account" | "fileId">) {
  if (!(await showAccount(item.account))) return;
  const detail = await run(t("查看"), () => DriveService.FileDetail(item.fileId));
  if (!detail) return;
  if (detail.folder) await openFolderAt(detail.id, "");
  else if (detail.playable) await play(detail, [detail]);
  else if (detail.preview) await openViewer(detail, [detail]);
  else await openFolderAt(detail.parentId, detail.id);
}

/* ---------------- playback ---------------- */

// eofHandled stops the auto-advance from firing repeatedly: mpv keeps
// reporting eof until the next file is loaded. It is cleared only by a state
// that says eof=false. Clearing it when a play starts looked harmless, but
// the link lookup takes a moment, any state tick in that window still says
// eof=true, and the advance fired a second time — skipping a file.
let eofHandled = false;

/**
 * play starts or reuses the playback session with `entry`, from `startAt`
 * seconds when it resumes a recent play. The picture opens on the immersive
 * page unless it is already up there or in the separate window; the page
 * underneath is left exactly as it is — route, folder, filter, scroll,
 * selection — for Collapse and Continue Playing to return to. Only an explicit play changes
 * the queue; previewing or selecting other files never does. The session
 * belongs to the account on screen; replacing another account's video asks
 * first.
 */
export async function play(entry: Entry, queue?: Entry[], startAt = 0) {
  const account = currentId.value;
  if (playingElsewhere.value) {
    const owner = player.owner;
    if (!(await askReplace(owner))) return;
    // Opening this video writes where the other one was left; asking first
    // is how a failure to keep it can be said.
    const saved = await DriveService.SavePlayback().catch(() => null);
    if (saved === null) notify(t("没能保存「{name}」的播放进度", { name: accountName(owner) }), "error");
  }
  if (!account || currentId.value !== account || accounts.switching) return;
  // A queue is of one kind: music plays on into music
  // in the same folder, a video into the videos beside it.
  const audio = entry.category === "audio";
  countUse(audio ? "play.audio" : "play.video");
  const list = (queue ?? visibleFiles.value).filter((e) => e.playable && (e.category === "audio") === audio);
  player.owner = account;
  player.playlist = list.length ? list : [entry];
  player.index = Math.max(0, player.playlist.findIndex((e) => e.id === entry.id));
  player.source = playSource(entry);
  if (player.presentation === "off" || player.presentation === "bar") showImmersive();
  // The host reports the stage rectangle on mount, and the native surface
  // has to exist before mpv initialises its video output.
  await nextTick();
  if (currentId.value !== account || accounts.switching) return;
  if (player.presentation === "window") {
    await run(t("独立窗口播放"), () => PlayerService.OpenWindow(audio));
    if (currentId.value !== account || accounts.switching) return;
  }

  await run(t("播放"), () =>
    startAt > 0 ? DriveService.ResumeFile(account, entry.id, startAt) : DriveService.PlayFile(account, entry.id),
  );
  await applyRememberedVolume();
}

/** playSource names where a play starts from, for Playback Source: a search hit's own path, the folder on screen, or "". */
function playSource(entry: Entry): string {
  if (entry.path) return entry.path;
  if (ui.route !== "files" || ui.query.trim()) return "";
  if (ui.view === "starred") return t("星标");
  if (ui.view === "all" && ui.scope === "folder") return [t("全部文件"), ...drive.trail.map((c) => c.name)].join(" / ");
  return "";
}

function showImmersive() {
  if (ui.route !== "player") player.returnRoute = ui.route;
  player.presentation = "immersive";
  ui.route = "player";
}

/** expandPlayer is Restore Video / Expand Player: the immersive page over the same session. */
export function expandPlayer() {
  if (player.presentation === "off") return;
  showImmersive();
}

/** collapsePlayer is Collapse and Continue Playing: back to the page it came from, the bar carrying on. */
export function collapsePlayer() {
  player.presentation = "bar";
  ui.route = player.returnRoute;
  if (ui.fullscreen) void setFullscreen(false);
  void loadPlays();
}

/** stopPlayback is the only action that ends the session. */
export async function stopPlayback() {
  if (inPlayerWindow) return sendPlayerCommand({ name: "stop", index: 0 });
  // The session is dropped only once mpv has stopped; otherwise the bar
  // would vanish while the sound carried on.
  const ok = await run(t("停止"), async () => {
    await PlayerService.StopPlayback();
    return true;
  });
  if (!ok) return;
  if (player.presentation === "window") await PlayerService.CloseWindow().catch(() => undefined);
  player.presentation = "off";
  player.playlist = [];
  player.index = -1;
  player.owner = "";
  if (ui.route === "player") ui.route = player.returnRoute;
  if (ui.fullscreen) void setFullscreen(false);
  // Where it stopped is now the newest Continue Playing.
  void loadPlays();
}

/**
 * popOut moves the audio or video controls into the separate player window.
 * The file, position and buffer carry on; this window owns the session and
 * shows the bar. An already open window is brought forward.
 */
export async function popOut() {
  if (inPlayerWindow || player.presentation === "off") return;
  if (player.presentation === "window") {
    await run(t("显示播放窗口"), () => PlayerService.OpenWindow(nowPlaying.value?.category === "audio"));
    return;
  }
  const before = player.presentation;
  if (ui.route === "player") ui.route = player.returnRoute;
  if (ui.fullscreen) void setFullscreen(false);
  player.presentation = "window";
  // The immersive page leaves first, hiding the picture here.
  await nextTick();
  const ok = await run(t("独立窗口播放"), async () => {
    await PlayerService.OpenWindow(nowPlaying.value?.category === "audio");
    return true;
  });
  if (ok) return;
  if (before === "immersive") showImmersive();
  else player.presentation = "bar";
}

// Set while the separate window is closing to bring the picture back here
// (Back to Main Window), as opposed to the window's own close button, which leaves
// the session in the bar.
let restoreOnClose = false;

/**
 * returnToMain closes the separate window and puts the picture back on the
 * immersive page here. From the separate window's own page it tells the
 * main window first: the closing alone does not say why it closed.
 */
export async function returnToMain() {
  if (inPlayerWindow) {
    await run(t("回到主窗口"), () => Events.Emit(PlayerCommandEvent, { name: "restore", index: 0 }));
    return;
  }
  restoreOnClose = true;
  const ok = await run(t("回到主窗口"), async () => {
    await PlayerService.CloseWindow();
    return true;
  });
  if (ok) playerWindowClosed();
  else restoreOnClose = false;
}

/** playerWindowClosed takes the session back from a separate window that went away. */
function playerWindowClosed() {
  if (player.presentation !== "window") return;
  if (restoreOnClose) showImmersive();
  else player.presentation = "bar";
  restoreOnClose = false;
}

/** publishSession hands the separate window what it shows of the session. */
function publishSession() {
  if (player.presentation !== "window") return;
  const owner = accountById(player.owner) ?? { id: "", login: "", label: "", nickname: "", avatar: "", current: false, needsPassword: false, usedAt: "", aiConsent: false, aiContentConsent: false };
  const session: PlayerSession = { playlist: player.playlist, index: player.index, mode: player.mode, owner, source: player.source };
  void Events.Emit(PlayerSessionEvent, session);
}

function sendPlayerCommand(command: PlayerCommand) {
  void Events.Emit(PlayerCommandEvent, command);
}

/** onPlayerCommand carries out, here, what the separate window asked for. */
function onPlayerCommand(command: PlayerCommand) {
  switch (command.name) {
    case "sync":
      publishSession();
      break;
    case "playAt":
      void playAt(command.index);
      break;
    case "stop":
      void stopPlayback();
      break;
    case "cancelNext":
      cancelNextOnce();
      break;
    case "mode":
      if (command.mode) setPlayMode(command.mode as PlayMode);
      break;
    case "restore":
      void returnToMain();
      break;
  }
}

// The volume belongs to this machine, not to mpv's process lifetime: a fresh
// core starts at 100 and would blast whoever turned it down last time.
let volumeApplied = false;
async function applyRememberedVolume() {
  if (volumeApplied) return;
  volumeApplied = true;
  const saved = Number(localStorage.getItem("pkp.volume"));
  if (Number.isFinite(saved) && saved > 0) {
    // Earlier builds allowed up to 130; anything above 100 is now 100.
    await PlayerService.SetVolume(Math.min(100, saved)).catch(() => undefined);
  }
}

export const nowPlaying = computed<Entry | null>(() => player.playlist[player.index] ?? null);

export async function playAt(index: number) {
  if (inPlayerWindow) return sendPlayerCommand({ name: "playAt", index });
  const entry = player.playlist[index];
  if (!entry) return;
  player.index = index;
  // The queue plays on in the account it was started in.
  await run(t("播放"), () => DriveService.PlayFile(player.owner, entry.id));
  await applyRememberedVolume();
}

function playNext() {
  if (player.index + 1 < player.playlist.length) void playAt(player.index + 1);
}

/** canPlayNextTrack is whether ⏭ leads anywhere: a later entry, or any other one in Shuffle. */
export const canPlayNextTrack = computed(
  () => player.index + 1 < player.playlist.length || (player.mode === "shuffle" && player.playlist.length > 1),
);

/** playNextTrack is ⏭ on the music page: the next entry, a random other one in Shuffle. */
export function playNextTrack() {
  if (player.mode === "shuffle" && player.playlist.length > 1) void playAt(pickShuffle(player.playlist.length, player.index));
  else playNext();
}

/**
 * playPreviousTrack is ⏮ on the music page, as music players do it: back to
 * the start of this one when it has played a few seconds or is the first,
 * else the entry before.
 */
export function playPreviousTrack() {
  if (player.state.position > 3 || player.index === 0) void playerCommands.seekTo(0);
  else void playAt(player.index - 1);
}

/** audioDetails reads the playing music file's own tags and cover (null when it cannot be read). */
export async function audioDetails(fileId: string): Promise<AudioDetails | null> {
  return PlayerService.AudioDetails(fileId).catch(() => null);
}

/** pickShuffle chooses any of `n` positions except `current`. */
export function pickShuffle(n: number, current: number, random = Math.random): number {
  let pick = Math.floor(random() * (n - 1));
  if (pick >= current) pick += 1;
  return pick;
}

// What the end of the current file leads to, for the "up next" notice.
export const nextUp = computed<Entry | null>(() => {
  switch (player.mode) {
    case "sequential":
      return player.playlist[player.index + 1] ?? null;
    case "one":
      return player.playlist[player.index] ?? null;
    default:
      return null;
  }
});

function advanceAfterEnd() {
  if (skipAdvanceOnce) {
    skipAdvanceOnce = false;
    return;
  }
  switch (player.mode) {
    case "sequential":
      playNext();
      break;
    case "one":
      void playAt(player.index);
      break;
    case "shuffle":
      if (player.playlist.length > 1) void playAt(pickShuffle(player.playlist.length, player.index));
      break;
    case "stop":
      break;
  }
}

export function setPlayMode(mode: PlayMode) {
  if (inPlayerWindow) return sendPlayerCommand({ name: "mode", index: 0, mode });
  player.mode = mode;
  localStorage.setItem("pkp.playMode", mode);
}

// cancelNextOnce dismisses the "up next" notice for this file only, without
// flipping the remembered play mode.
let skipAdvanceOnce = false;
export function cancelNextOnce() {
  if (inPlayerWindow) return sendPlayerCommand({ name: "cancelNext", index: 0 });
  skipAdvanceOnce = true;
}

export async function setFullscreen(on: boolean) {
  ui.fullscreen = on;
  // The OS-level part is best effort: the browser-served build has no native
  // window, and the layout alone already hides the chrome.
  try {
    if (on) await Window.Fullscreen();
    else await Window.UnFullscreen();
  } catch {
    /* no native window */
  }
}

export const playerCommands = {
  toggle: () => run(t("播放/暂停"), () => PlayerService.TogglePause()),
  seek: (delta: number) => run(delta < 0 ? t("后退") : t("快进"), () => PlayerService.Seek(delta)),
  seekTo: (pos: number) => run(t("跳转"), () => PlayerService.SeekTo(pos)),
  volume: (v: number) => {
    localStorage.setItem("pkp.volume", String(v));
    return run(t("音量"), () => PlayerService.SetVolume(v));
  },
  mute: (on: boolean) => run(t("静音"), () => PlayerService.SetMuted(on)),
  speed: (v: number) => run(t("倍速"), () => PlayerService.SetSpeed(v)),
  /** quality saves the rendition preference and applies it from the same position. */
  quality: (choice: string) => run(t("切换清晰度"), () => DriveService.SetPlaybackQuality(choice)),
  /** subtitleStyle saves Subtitle Style (size, height, colour) and applies it to every video. */
  subtitleStyle: (style: SubtitleStyle) => run(t("字幕样式"), () => DriveService.SetSubtitleStyle(style)),
  /** subtitleDelay and audioDelay shift the subtitles or the sound for this video; positive is later. */
  subtitleDelay: (seconds: number) => run(t("字幕延迟"), () => PlayerService.SetSubtitleDelay(seconds)),
  audioDelay: (seconds: number) => run(t("音频延迟"), () => PlayerService.SetAudioDelay(seconds)),
};

// A quality switch is answered through the state, after the menu has closed:
// a failure or auto's own step down would otherwise pass unnoticed.
watch(
  () => player.state.quality?.failed,
  (failed) => {
    if (!failed) return;
    const label = playingLabel(player.state.quality);
    notify(label ? t("清晰度切换失败，继续播放 {quality}", { quality: label }) : t("清晰度切换失败，继续播放"), "error");
  },
);
watch(
  () => player.state.quality?.lowered,
  (lowered) => {
    if (lowered) notify(t("网络较慢，已自动切换到 {quality}", { quality: playingLabel(player.state.quality) }), "info");
  },
);

/* ---------------- TMDB (Film and TV Organization) ---------------- */

/*
 * The assistant's Film and TV Organization matches titles against TMDB with the user's own
 * credential; without one it names by rules and the model only. Settings · AI
 * holds the credential, its test and the attribution TMDB's terms ask for.
 */
export const tmdb = reactive({
  settings: null as TMDBSettings | null,
  error: "",
  saving: false,
  testing: false,
  test: null as TMDBTestResult | null,
});

export async function loadTMDBSettings(): Promise<void> {
  try {
    tmdb.settings = await DriveService.TMDBSettings();
    tmdb.error = "";
  } catch (err) {
    tmdb.error = errorText(err);
  }
}

/** saveTMDBKey stores the TMDB credential in the keychain; "" deletes it. */
export async function saveTMDBKey(key: string): Promise<boolean> {
  tmdb.saving = true;
  let settings: TMDBSettings | undefined;
  try {
    settings = await run(key ? t("保存 TMDB 凭据") : t("清除 TMDB 凭据"), () => DriveService.SetTMDBKey(key));
  } finally {
    tmdb.saving = false;
  }
  if (!settings) return false;
  tmdb.settings = settings;
  tmdb.test = null;
  notify(key ? t("TMDB 凭据已保存到{place}", { place: keychainName }) : t("TMDB 凭据已清除"), "success");
  return true;
}

/** testTMDB asks TMDB once with the stored credential. */
export async function testTMDB(): Promise<void> {
  tmdb.testing = true;
  tmdb.test = null;
  try {
    tmdb.test = await DriveService.TestTMDB();
  } catch (err) {
    tmdb.test = { ok: false, kind: "unreachable", message: errorText(err) };
  } finally {
    tmdb.testing = false;
  }
}

/** openTMDBSite opens themoviedb.org from the attribution's explicit link; nothing else opens it. */
export function openTMDBSite(): void {
  void Browser.OpenURL("https://www.themoviedb.org/").catch((err: unknown) => notify(t("无法打开网页：{error}", { error: errorText(err) }), "error"));
}

/* ---------------- bootstrap ---------------- */

let listening = false;

export async function bootstrap() {
  document.documentElement.dataset.theme = ui.theme;
  document.documentElement.dataset.skin = ui.skin;
  applyTransparency();

  // bootstrap is re-entered from the splash's retry button; the event
  // subscription must not be.
  if (!listening) {
    listening = true;
    Events.On(PlayerStateEvent, (event: { data: PlayerState }) => {
      player.state = event.data;
      if (player.state.eof && !eofHandled) {
        eofHandled = true;
        advanceAfterEnd();
      }
      if (!player.state.eof) eofHandled = false;
    });
    Events.On(IndexProgressEvent, (event: { data: IndexProgress }) => applyIndexProgress(event.data));
    Events.On(UploadStateEvent, (event: { data: UploadTask }) => applyUpload(event.data));
    Events.On(DownloadStateEvent, (event: { data: DownloadTask }) => applyDownload(event.data));
    // The separate player window: requests from its page, its closing, and
    // keeping it told what the session is while it shows the picture.
    Events.On(PlayerCommandEvent, (event: { data: PlayerCommand }) => onPlayerCommand(event.data));
    Events.On(PlayerWindowClosedEvent, () => playerWindowClosed());
    Events.On(ViewerReturnEvent, (event: { data: ViewerReturnRequest }) => void restoreViewer(event.data));
    // An account signed in, switched, renamed, removed or its session ended.
    Events.On(AccountsEvent, () => void loadAccounts());
    // A Film and TV Organization run or its Undo as its changes finish, for the run's account only.
    Events.On(OrganizeProgressEvent, (event: { data: OrganizeProgress }) => {
      const p = event.data;
      if (assistant.running && p.account === assistant.runAccount) assistant.progress = { done: p.done, total: p.total };
    });
    // A removal (Cleanup or the file page) as its batches finish; only a Cleanup run draws it.
    Events.On(RemoveProgressEvent, (event: { data: RemoveProgress }) => {
      const p = event.data;
      if (cleanup.running && p.account === cleanup.runAccount) cleanup.progress = { done: p.done, total: p.total, phase: p.phase };
    });
    watch(
      [() => player.playlist, () => player.index, () => player.mode, () => player.presentation, () => playingOwner.value, () => player.source],
      () => publishSession(),
    );
    // The window came back from the menu bar (or the Dock), maybe with something to show.
    Events.On(MenuBarOpenEvent, (event: { data: MenuBarOpen }) => void openFromMenuBar(event.data));
    // A turn's streamed text, matched to the request in flight by its id.
    Events.On(AIDeltaEvent, (event: { data: AIDelta }) => applyAIDelta(event.data));
    // The inbox watcher found new arrivals (or one changed state) for an account.
    Events.On(InboxChangedEvent, (event: { data: InboxChanged }) => {
      if (event.data.account === currentId.value) void loadInbox(event.data.account);
    });
    // Quitting asks before what is typed and not submitted is lost; Go asks, so it is told.
    watch(unsubmitted, (what) => void MenuBarService.SetUnsaved(what).catch(() => undefined), { immediate: true });
    // Anonymous Usage Statistics: which page is on screen, each time it changes (the sign-in page is none).
    watch(
      () => ui.route,
      (route) => {
        if (drive.status?.configured) countUse(`route.${route}`);
      },
    );
  }

  ui.bootError = "";
  let status: DriveStatus;
  try {
    status = await refreshStatus();
  } catch (err) {
    ui.bootError = err instanceof Error ? err.message : String(err);
    return;
  }

  const [list, local] = await Promise.all([
    DriveService.Accounts().catch(() => null),
    DriveService.LocalSettings().catch(() => null),
  ]);
  accounts.list = list ?? [];
  if (local) accounts.keepPlaying = local.keepPlayingOnSwitch;
  // Every account's uploads and downloads: All Accounts lists them, and they keep running.
  void loadUploads();
  void loadDownloads();
  if (status.needsPassword) {
    notify(t("「{name}」的登录已失效，请重新登录", { name: accountName(status.accountId) }), "error");
  }
  if (status.configured) {
    await Promise.all([reload(), loadQuota(), loadTasks(), loadIndexProgress(), loadPlays()]);
    countUse(`route.${ui.route}`);
  }
}

/**
 * bootstrapPlayerWindow starts the separate player window's page: mpv's
 * state and the main window's session flow in, then the video surface is
 * claimed for this window. Only after that may the page report where the
 * picture goes, so the player is mounted once this resolves.
 */
export async function bootstrapPlayerWindow() {
  document.documentElement.dataset.theme = ui.theme;
  document.documentElement.dataset.skin = ui.skin;
  applyTransparency();
  player.presentation = "window";
  Events.On(PlayerStateEvent, (event: { data: PlayerState }) => {
    player.state = event.data;
  });
  let received: (() => void) | undefined;
  const sessionReady = new Promise<void>((resolve, reject) => {
    const timeout = window.setTimeout(() => reject(new Error(t("播放会话同步超时"))), 5000);
    received = () => { window.clearTimeout(timeout); resolve(); };
  });
  Events.On(PlayerSessionEvent, (event: { data: PlayerSession }) => {
    player.playlist = event.data.playlist ?? [];
    player.index = event.data.index;
    player.mode = event.data.mode as PlayMode;
    player.source = event.data.source;
    player.ownerInfo = event.data.owner?.id ? event.data.owner : null;
    received?.();
  });
  sendPlayerCommand({ name: "sync", index: 0 });
  const [state] = await Promise.all([PlayerService.State(), sessionReady]);
  player.state = state;
  if (nowPlaying.value?.category !== "audio") await PlayerService.HostVideo();
}
