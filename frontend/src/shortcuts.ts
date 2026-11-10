// Keyboard Shortcuts: the app-wide shortcuts of the main window, one command each.
// A key (commandFor) and the macOS menu bar's item of the same name (Go's
// appmenu.go, event "menu:command") both end in runCommand, so a shortcut
// and its menu item never disagree. Keys the page handles never reach the
// menu: WebKit offers a key equivalent to the page first and to the menu
// only when the page did not take it.
//
// Page keys that act on one list or player (Space, Delete, ←/→ …) stay with
// their pages; the list in Settings · Shortcuts (shortcutGroups) shows them all.
import { onBeforeUnmount, onMounted } from "vue";
import { Events } from "@wailsio/runtime";
import { RAIL_QUERY, isTyping, modKey } from "./dom";
import { t } from "./i18n";
import { checkUpdates } from "./updatestore";
import {
  NAV_KEYS,
  accounts,
  appLock,
  cleanup,
  closeViewer,
  collapsePlayer,
  drive,
  goBack,
  loadPlayHistory,
  loadShares,
  openNav,
  openSettings,
  pickAndUpload,
  playHistory,
  refresh,
  rescanCleanup,
  setNavCompact,
  shares,
  showInfo,
  ui,
  videoWorksShown,
  type NavKey,
} from "./store";

export type Command =
  | "settings"
  | "updates"
  | "shortcuts"
  | "search"
  | "refresh"
  | "back"
  | "toggleNav"
  | "info"
  | "cloudAdd"
  | "newFolder"
  | "upload"
  | "uploadFolder"
  | `nav:${NavKey}`;

const MenuCommandEvent = "menu:command";
const mac = modKey === "⌘";

type KeyLike = Pick<KeyboardEvent, "key" | "code" | "metaKey" | "ctrlKey" | "altKey" | "shiftKey" | "isComposing">;

/**
 * commandFor names the command a key press asks for, or null. macOS takes ⌘
 * alone (⌃⌘F is the system's full screen), elsewhere Ctrl alone. Letters
 * fall back to the physical key under a non-Latin layout; punctuation reads
 * the character, so a layout that moves "/" keeps ⌘- for zooming out.
 * `typing`: keys that edit text (⌘↑, Alt+←) are left to the field.
 */
export function commandFor(e: KeyLike, onMac: boolean, typing: boolean): Command | null {
  if (e.isComposing) return null;
  const mod = onMac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey;
  const key = e.key.length === 1 ? e.key.toLowerCase() : e.key;
  const letter = /^[a-z]$/.test(key) ? key : (/^Key([A-Z])$/.exec(e.code)?.[1].toLowerCase() ?? "");
  if (!mod) {
    if (onMac || e.metaKey || e.ctrlKey || e.shiftKey) return null;
    if (e.key === "F5" && !e.altKey) return "refresh";
    if (!typing && e.altKey && (e.key === "ArrowLeft" || e.key === "ArrowUp")) return "back";
    if (!typing && e.altKey && e.key === "Enter") return "info";
    if (!typing && !e.altKey && e.key === "Backspace") return "back";
    return null;
  }
  if (e.altKey) return null;
  if (e.shiftKey) {
    if (letter === "n") return "newFolder";
    if (letter === "u") return "uploadFolder";
    return null;
  }
  if (/^[1-9]$/.test(key)) {
    const nav = NAV_KEYS[Number(key) - 1];
    return nav ? `nav:${nav}` : null;
  }
  switch (key) {
    case ",":
      return "settings";
    case "/":
      return "shortcuts";
    case "[":
      return "back";
    case "\\":
      return "toggleNav";
    case "ArrowUp":
      return typing ? null : "back";
  }
  switch (letter) {
    case "f":
    case "k":
      return "search";
    case "r":
      return "refresh";
    case "n":
      return "cloudAdd";
    case "u":
      return "upload";
    case "i":
      return "info";
  }
  return null;
}

// Commands whose work lives in a component (the search box's element, the
// transfer list's own lens) are registered by it while it is mounted.
const handlers = new Map<Command, Set<() => void>>();

/** useCommand runs `fn` for the command while the component is mounted. */
export function useCommand(command: Command, fn: () => void) {
  onMounted(() => {
    if (!handlers.has(command)) handlers.set(command, new Set());
    handlers.get(command)!.add(fn);
  });
  onBeforeUnmount(() => handlers.get(command)?.delete(fn));
}

// These take the user somewhere: from the immersive player, the page it
// came from first, the session carrying on in the bar.
const LEAVES_PLAYER: Record<string, true> = {
  settings: true,
  updates: true,
  shortcuts: true,
  cloudAdd: true,
  newFolder: true,
  upload: true,
  uploadFolder: true,
};

/** runCommand does what a shortcut or a menu item asks for, when it can be done now. */
export function runCommand(command: string): void {
  // Nothing runs under the lock screen, without an account on screen, during
  // an account switch, or while a sheet asks for an answer: it would act
  // behind it, unseen. Every sheet draws the global `.scrim`.
  if (appLock.state?.locked || accounts.switching || document.querySelector(".scrim")) return;
  if (command === "updates") {
    if (ui.route === "player") collapsePlayer();
    openSettings("updates");
    void checkUpdates();
    return;
  }
  if (!drive.status?.accountId) return;
  if (ui.route === "player" && (LEAVES_PLAYER[command] || command.startsWith("nav:"))) collapsePlayer();
  if (command.startsWith("nav:")) {
    const key = command.slice(4) as NavKey;
    if (NAV_KEYS.includes(key)) openNav(key);
    return;
  }
  for (const fn of handlers.get(command as Command) ?? []) fn();
  switch (command) {
    case "settings":
      openSettings();
      break;
    case "shortcuts":
      openSettings("keys");
      break;
    case "cloudAdd":
      ui.cloudAddOpen = true;
      break;
    case "newFolder":
      ui.newFolderOpen = true;
      break;
    case "upload":
      void pickAndUpload("files");
      break;
    case "uploadFolder":
      void pickAndUpload("folders");
      break;
    case "toggleNav":
      // Under 1080px the rail is not a choice.
      if (!window.matchMedia(RAIL_QUERY).matches) setNavCompact(!ui.navCompact);
      break;
    case "info":
      if ((ui.route === "files" && !videoWorksShown.value) || ui.route === "viewer") showInfo();
      break;
    case "back":
      if (ui.route === "files" && !videoWorksShown.value) goBack();
      else if (ui.route === "viewer") closeViewer();
      break;
    case "refresh":
      // Transfer Centre refreshes its own lens (TransfersView).
      if (ui.route === "files") void refresh();
      else if (ui.route === "shares" && !shares.loading) void loadShares();
      else if (ui.route === "history" && !playHistory.loading) void loadPlayHistory();
      else if (ui.route === "cleanup") rescanCleanup();
      break;
  }
}

/** installShortcuts listens for the keys and the menu bar's items; the main window calls it once. */
export function installShortcuts(): void {
  window.addEventListener("keydown", (event) => {
    if (event.defaultPrevented) return;
    const command = commandFor(event, mac, isTyping(event));
    if (!command) return;
    // Taken even when it cannot run now: the menu must not run it instead.
    event.preventDefault();
    runCommand(command);
  });
  Events.On(MenuCommandEvent, (event: { data: string }) => runCommand(event.data));
}

/* ---------- the list in Settings · Shortcuts ---------- */

export interface ShortcutRow {
  label: string;
  /** Alternatives, each a chord of keys as drawn. */
  keys: string[][];
}

const shift = mac ? "⇧" : "Shift";
const alt = mac ? "⌥" : "Alt";

/** shortcutGroups lists every shortcut of the window for the platform, in the UI's language. */
export function shortcutGroups(): { title: string; rows: ShortcutRow[] }[] {
  const m = (key: string) => [modKey, key];
  const navLabels: Record<NavKey, string> = {
    all: t("nav|全部文件"),
    starred: t("星标"),
    cleanup: t("nav|清理"),
    assistant: t("nav|AI 助手"),
    history: t("nav|播放历史"),
    sync: t("同步与备份"),
    transfers: t("传输中心"),
    shares: t("我的分享"),
    trash: t("回收站"),
    lan: t("局域网共享"),
  };
  const space = t("空格");
  return [
    {
      title: t("通用"),
      rows: [
        { label: t("打开设置"), keys: [m(",")] },
        { label: t("键盘快捷键"), keys: [m("/")] },
        { label: t("搜索"), keys: [m("K"), m("F")] },
        { label: t("刷新当前页面"), keys: mac ? [m("R")] : [m("R"), ["F5"]] },
        { label: t("切换侧栏"), keys: [m("\\")] },
        ...(mac
          ? [
              { label: t("关闭窗口（PKP 留在菜单栏）"), keys: [m("W")] },
              { label: t("退出 PKP"), keys: [m("Q")] },
            ]
          : []),
      ],
    },
    {
      title: t("前往"),
      rows: [
        { label: t("返回上一级"), keys: mac ? [m("["), m("↑")] : [m("["), m("↑"), [alt, "←"], [alt, "↑"], ["Backspace"]] },
        ...NAV_KEYS.map((key, i) => ({ label: navLabels[key], keys: [m(String(i + 1))] })),
      ],
    },
    {
      title: t("新建"),
      rows: [
        { label: t("云添加"), keys: [m("N")] },
        { label: t("新建文件夹"), keys: [[shift, modKey, "N"]] },
        { label: t("上传文件"), keys: [m("U")] },
        { label: t("上传文件夹"), keys: [[shift, modKey, "U"]] },
      ],
    },
    {
      title: t("文件列表"),
      rows: [
        { label: t("打开所选项"), keys: mac ? [["↩"], m("↓")] : [["Enter"]] },
        { label: t("预览所选文件"), keys: [[space]] },
        { label: t("移动选中项"), keys: [["↑"], ["↓"], ["←"], ["→"]] },
        { label: t("加选或取消一项"), keys: [m(t("点按"))] },
        { label: t("连续选择"), keys: [[shift, t("点按")], [shift, "↑/↓/←/→"]] },
        { label: t("全选"), keys: [m("A")] },
        { label: t("取消选择"), keys: [["Esc"]] },
        { label: t("重命名"), keys: [["F2"]] },
        { label: t("复制 / 剪切 / 粘贴"), keys: [m("C"), m("X"), m("V")] },
        { label: t("打开快捷菜单"), keys: [[t("右键点按")], [shift, "F10"]] },
        { label: t("移到回收站"), keys: mac ? [m("⌫")] : [["Delete"], m("Backspace")] },
        { label: t("彻底删除"), keys: mac ? [[alt, modKey, "⌫"]] : [[shift, "Delete"]] },
        { label: t("查看信息"), keys: mac ? [m("I")] : [m("I"), [alt, "Enter"]] },
      ],
    },
    {
      title: t("我的分享"),
      rows: [
        { label: t("加选或取消一项"), keys: [m(t("点按")), [space]] },
        { label: t("连续选择"), keys: [[shift, t("点按")]] },
        { label: t("全选"), keys: [m("A")] },
        { label: t("取消选择"), keys: [["Esc"]] },
        { label: t("关闭分享详情"), keys: [["Esc"]] },
      ],
    },
    {
      title: t("预览"),
      rows: [
        { label: t("上一个 / 下一个"), keys: [["←"], ["→"]] },
        { label: t("放大 / 缩小"), keys: [["+"], ["-"]] },
        { label: t("适合窗口"), keys: [["0"]] },
        { label: t("关闭预览"), keys: [["Esc"]] },
      ],
    },
    {
      title: t("播放"),
      rows: [
        { label: t("播放 / 暂停"), keys: [[space]] },
        { label: t("后退 / 前进 15 秒"), keys: [["←"], ["→"]] },
        { label: t("调高 / 调低音量"), keys: [["↑"], ["↓"]] },
        { label: t("静音"), keys: [["M"]] },
        { label: t("全屏"), keys: [["F"]] },
        { label: t("截图"), keys: [["S"]] },
        { label: t("退出全屏"), keys: [["Esc"]] },
      ],
    },
  ];
}
