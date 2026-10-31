// DOM helpers shared by the views. Keyboard shortcuts and popover menus
// exist in several components; the rules for "is the user typing" and
// "was that click outside the menu" live here so they cannot drift.
import { onBeforeUnmount, onMounted, reactive, ref, watch, type Ref } from "vue";
import { i18n, t } from "./i18n";

/** The shortcut modifier as the platform writes it: macOS says ⌘, Windows and Linux say Ctrl. */
export const modKey = navigator.platform.toLowerCase().includes("mac") ? "⌘" : "Ctrl";

/** shortcutText writes the modifier and `key` the way the platform does: ⌘R on macOS, Ctrl+R elsewhere. */
export function shortcutText(key: string, shift = false): string {
  if (modKey === "⌘") return `${shift ? "⇧" : ""}⌘${key}`;
  return `Ctrl+${shift ? "Shift+" : ""}${key}`;
}

/** Windows, where windows go by its own conventions: caption buttons on the right, File Explorer for the file manager. */
export const onWindows = navigator.platform.startsWith("Win");

// What the page calls the platform's own parts, so no Windows user reads a
// macOS name. They are live bindings re-set on a language switch (sync, so
// the re-render that follows already reads the new words): callers keep
// importing plain strings.
/** The system as a whole. */
export const systemName = onWindows ? "Windows" : "macOS";
/** Where Go keeps passwords and keys (PasswordVault). */
export let keychainName = "";
/** Where PKP stays with its window closed: the menu bar item or the tray icon. */
export let trayName = "";
/** The system's settings app. */
export let settingsAppName = "";

function nameParts() {
  keychainName = onWindows ? t("凭据管理器") : t("钥匙串");
  trayName = onWindows ? t("系统托盘") : t("菜单栏");
  settingsAppName = onWindows ? t("Windows 设置") : t("系统设置");
}
nameParts();
watch(() => i18n.locale, nameParts, { flush: "sync" });

// The file page's widths, measured on
// the window's content area. Under 1080 the nav is an icon rail; under 960
// the files and the context column take turns as two pages.
export const RAIL_QUERY = "(max-width: 1079px)";
export const WORKSPACE_QUERY = "(max-width: 959px)";

/** useMedia follows a media query for the component's lifetime. */
export function useMedia(query: string): Ref<boolean> {
  const list = window.matchMedia(query);
  const matches = ref(list.matches);
  const onChange = (event: MediaQueryListEvent) => (matches.value = event.matches);
  onMounted(() => list.addEventListener("change", onChange));
  onBeforeUnmount(() => list.removeEventListener("change", onChange));
  return matches;
}

/**
 * isTyping reports whether a key event belongs to a text field or a choice
 * (a SelectMenu's face or list, as a <select> once was), where page shortcuts
 * must stay out of the way.
 */
export function isTyping(event: KeyboardEvent): boolean {
  const target = event.target as HTMLElement | null;
  const tag = target?.tagName ?? "";
  return (
    tag === "INPUT" ||
    tag === "SELECT" ||
    tag === "TEXTAREA" ||
    !!target?.isContentEditable ||
    !!target?.closest?.("[data-select-menu]")
  );
}

/**
 * inSelectMenu reports a key for an open SelectMenu's list or SuggestMenu's
 * field, which handles it itself — Esc closes the list only. Capture-phase
 * handlers that would close a whole sheet on Esc check it first.
 */
export function inSelectMenu(event: KeyboardEvent): boolean {
  return !!(event.target as HTMLElement | null)?.closest?.("[data-select-list]");
}

/**
 * useOutsideClick calls `close` on any pointerdown outside an element matching
 * `selector` while `isOpen()` holds. Registered for the component's lifetime.
 */
export function useOutsideClick(selector: string, isOpen: () => boolean, close: () => void) {
  function onPointerDown(ev: PointerEvent) {
    if (!isOpen()) return;
    if ((ev.target as HTMLElement | null)?.closest?.(selector)) return;
    close();
  }
  onMounted(() => window.addEventListener("pointerdown", onPointerDown));
  onBeforeUnmount(() => window.removeEventListener("pointerdown", onPointerDown));
}

/**
 * useAnchoredList places a list teleported to <body> under its anchor (above
 * it when there is more room there) and keeps it there while `open` holds:
 * scrolling anything but the list itself, or resizing the window, places it
 * again. `rows` is the box that scrolls inside the list (behind its 1px
 * edge); `gone` runs when the anchor no longer has a box. Shared by
 * SelectMenu and SuggestMenu, so both lists sit the same way.
 */
export function useAnchoredList(
  anchor: () => HTMLElement | undefined,
  list: Ref<HTMLElement | undefined>,
  rows: Ref<HTMLElement | undefined>,
  open: Ref<boolean>,
  gone: () => void,
) {
  const GAP = 6;
  const MARGIN = 8;
  const spot = reactive({ top: 0, left: 0, width: 0, maxHeight: 0, up: false, placed: false });

  function place() {
    const box = anchor()?.getBoundingClientRect();
    if (!box || !list.value || (box.width === 0 && box.height === 0)) {
      gone();
      return;
    }
    const below = window.innerHeight - box.bottom - GAP - MARGIN;
    const above = box.top - GAP - MARGIN;
    const tall = (rows.value?.scrollHeight ?? 0) + 2;
    spot.up = tall > below && above > below;
    spot.maxHeight = Math.max(0, spot.up ? above : below);
    spot.top = spot.up ? box.top - GAP - Math.min(tall, spot.maxHeight) : box.bottom + GAP;
    spot.width = box.width;
    const wide = Math.max(list.value.offsetWidth, box.width);
    spot.left = Math.min(Math.max(MARGIN, box.left), window.innerWidth - wide - MARGIN);
    spot.placed = true;
  }

  function onScroll(event: Event) {
    if (!(event.target instanceof Node && list.value?.contains(event.target))) place();
  }
  function listen(on: boolean) {
    const method = on ? "addEventListener" : "removeEventListener";
    window[method]("scroll", onScroll, true);
    window[method]("resize", place);
  }
  watch(open, listen);
  onBeforeUnmount(() => listen(false));

  return { spot, place };
}
