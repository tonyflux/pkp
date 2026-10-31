<script setup lang="ts">
// Switch account (AccountPopover): a non-modal dialog over
// the page, one row per account and the ways to add, manage and reach Settings.
// ui.accountsOpen is the one switch for it; the nav renders it from 960px up,
// the toolbar below that, so only one is ever mounted.
import { nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import Icon from "./Icon.vue";
import AccountIdentity from "./AccountIdentity.vue";
import { maskLogin } from "../format";
import { accountLabel, accounts, currentId, openAccountManager, openSignIn, switchAccount, ui } from "../store";
import type { Account } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import { t } from "../i18n";

const props = defineProps<{
  // nav: above the account trigger, from the nav's inner edge; rail: beside
  // the 72px rail; toolbar: under the toolbar's account button.
  placement: "nav" | "rail" | "toolbar";
  // Where focus goes back to once the panel closes.
  trigger?: HTMLElement | null;
}>();

const root = ref<HTMLElement>();

function close() {
  ui.accountsOpen = false;
}

function status(a: Account): { text: string; tone: string } {
  if (accounts.switching === a.id) return { text: t("正在切换…"), tone: "busy" };
  if (a.needsPassword) return { text: t("重新登录"), tone: "warning" };
  if (a.id === currentId.value) return { text: t("当前"), tone: "current" };
  return { text: t("切换"), tone: "" };
}

function rowLabel(a: Account): string {
  let state = t("已登录");
  if (accounts.switching === a.id) state = t("正在切换");
  else if (a.id === currentId.value) state = a.needsPassword ? t("当前账号，需要重新登录") : t("当前账号");
  else if (a.needsPassword) state = t("需要重新登录");
  return [accountLabel(a), maskLogin(a.login), state].filter(Boolean).join(t("，"));
}

function pick(a: Account) {
  if (a.needsPassword) void openSignIn(a.id);
  else if (a.id === currentId.value) close();
  else void switchAccount(a.id);
}

function openSettings() {
  ui.route = "settings";
  close();
}

/* ---------- where it sits ---------- */

// Positioned against its anchor (the nav's account row, or the toolbar);
// only the room it may take and, under the toolbar, the 16px safe margin
// depend on where that anchor is on screen.
const placed = ref<Record<string, string>>({});
const SAFE = 16;

function place() {
  const el = root.value;
  const anchor = el?.offsetParent as HTMLElement | null;
  if (!el || !anchor) return;
  const r = anchor.getBoundingClientRect();
  const style: Record<string, string> = {};
  let room: number;
  if (props.placement === "nav") {
    // The nav's inner edge: 12px inside the island, as the rows' lens.
    const nav = anchor.closest("aside")?.getBoundingClientRect();
    if (nav) style.left = `${nav.left + 12 - r.left}px`;
    room = r.top - 12 - SAFE;
  } else if (props.placement === "rail") {
    room = r.bottom - SAFE;
  } else {
    style.left = `${SAFE - r.left}px`;
    room = window.innerHeight - r.bottom - 8 - SAFE;
  }
  style.maxHeight = `min(calc(100dvh - ${SAFE * 2}px), ${Math.max(Math.round(room), 160)}px)`;
  placed.value = style;
}

/* ---------- keyboard and pointer ---------- */

function buttons(): HTMLButtonElement[] {
  const found = root.value?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)");
  return found ? Array.from(found) : [];
}

function onKeydown(event: KeyboardEvent) {
  const items = buttons();
  if (!items.length) return;
  const index = items.indexOf(document.activeElement as HTMLButtonElement);
  if (event.key === "Tab") {
    if (event.shiftKey && index <= 0) {
      event.preventDefault();
      items[items.length - 1].focus();
    } else if (!event.shiftKey && index === items.length - 1) {
      event.preventDefault();
      items[0].focus();
    }
  } else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    event.preventDefault();
    const step = event.key === "ArrowDown" ? 1 : -1;
    const next = index < 0 ? (step > 0 ? 0 : items.length - 1) : (index + step + items.length) % items.length;
    items[next].focus();
  }
}

// The panel owns Esc while it is open, ahead of the page behind it. A modal
// account dialog above it answers Esc itself.
function onEscape(event: KeyboardEvent) {
  if (event.key !== "Escape" || accounts.dialog) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  close();
}

// A press on the trigger is left to the trigger, which toggles.
function onPointerDown(event: PointerEvent) {
  const target = event.target as HTMLElement | null;
  if (target?.closest?.("[data-account-popover], [data-account-trigger]")) return;
  close();
}

onMounted(async () => {
  place();
  window.addEventListener("resize", place);
  window.addEventListener("keydown", onEscape, true);
  window.addEventListener("pointerdown", onPointerDown, true);
  // The row shows its ring only if what opened the panel shows one, i.e. it
  // was opened from the keyboard. Left to itself WebKit rings the row after
  // a plain click too: a clicked button does not take focus on macOS.
  const keyboard = !!document.activeElement?.matches(":focus-visible");
  await nextTick();
  const current = root.value?.querySelector<HTMLElement>('[aria-current="true"]');
  // focusVisible (Safari 18.4, Chromium 145) is newer than TS 4.9's DOM types.
  (current ?? buttons()[0])?.focus({ preventScroll: true, focusVisible: keyboard } as FocusOptions);
});

// However it closes — Esc, ×, the trigger, a press outside, a row — focus
// that was inside goes back to the trigger, unless an account dialog took
// it or the press outside landed on something that takes focus itself.
onBeforeUnmount(() => {
  window.removeEventListener("resize", place);
  window.removeEventListener("keydown", onEscape, true);
  window.removeEventListener("pointerdown", onPointerDown, true);
  const hadFocus = !!root.value?.contains(document.activeElement);
  const trigger = props.trigger;
  if (!hadFocus || !trigger) return;
  void nextTick(() => {
    if (accounts.dialog || !trigger.isConnected) return;
    const active = document.activeElement;
    if (!active || active === document.body) trigger.focus({ preventScroll: true });
  });
});
</script>

<template>
  <section
    ref="root"
    class="account-popover glass glass-sheet"
    :class="`at-${placement}`"
    :style="placed"
    role="dialog"
    aria-labelledby="account-popover-title"
    data-account-popover
    @keydown="onKeydown"
  >
    <header>
      <h2 id="account-popover-title">{{ t("切换账号") }}</h2>
      <button class="quiet icon close" :title="t('关闭')" :aria-label="t('关闭账号面板')" @click="close">
        <Icon name="close" :size="16" />
      </button>
    </header>

    <div class="rows">
      <button
        v-for="a in accounts.list"
        :key="a.id"
        class="row"
        :class="{ current: a.id === currentId }"
        :aria-current="a.id === currentId ? 'true' : undefined"
        :aria-label="rowLabel(a)"
        @click="pick(a)"
      >
        <AccountIdentity class="who" :account="a" />
        <span class="status" :class="status(a).tone">{{ status(a).text }}</span>
      </button>
    </div>

    <footer>
      <button class="quiet" :aria-label="t('添加账号')" @click="openSignIn('')">
        <Icon name="plus" :size="18" />{{ t("popover|添加账号") }}
      </button>
      <button class="quiet" :aria-label="t('管理账号')" @click="openAccountManager()">
        <Icon name="grid" :size="18" />{{ t("popover|管理账号") }}
      </button>
      <button class="quiet" :class="{ on: ui.route === 'settings' }" @click="openSettings">
        <Icon name="settings" :size="18" />{{ t("设置") }}
      </button>
    </footer>
    <p class="hint">{{ t("切换后，后台传输继续进行") }}</p>
  </section>
</template>

<style scoped>
/* Wherever it opens it lies over rows or the file canvas, so it is an opaque
   reading surface, not glass on glass, which let the names
   underneath read through it. At the canvas's 94% they still did, unblurred. */
.account-popover {
  --wails-draggable: no-drag;
  position: absolute;
  z-index: 5;
  width: min(360px, calc(100vw - 32px));
  max-height: calc(100dvh - 32px);
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 14px 10px 12px;
  overflow-y: auto;
  scrollbar-width: thin;
  border-radius: 24px;
  background: var(--canvas-flat);
  animation: popover-in 0.16s var(--ease);
}

/* Above the account row. (Placement classes are prefixed: the nav's own
   scoped `.nav` rule would reach this root element too.) */
.account-popover.at-nav {
  bottom: calc(100% + 12px);
  left: 0;
  transform-origin: bottom left;
}

/* Beside the rail, level with the account row's bottom. */
.account-popover.at-rail {
  left: calc(100% + 28px);
  bottom: 0;
  transform-origin: bottom left;
}

/* Under the toolbar, 16px from the window's sides. */
.account-popover.at-toolbar {
  top: calc(100% + 8px);
  left: 0;
  transform-origin: top left;
}

@keyframes popover-in {
  from {
    opacity: 0;
    transform: translateY(6px) scale(0.98);
  }
}

.account-popover.at-toolbar {
  animation-name: popover-down;
}

@keyframes popover-down {
  from {
    opacity: 0;
    transform: translateY(-6px) scale(0.98);
  }
}

header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 0 4px 6px 10px;
}

h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
}

.close {
  width: 40px;
  height: 40px;
  min-height: 40px;
}

/* It scrolls, so it clips: 4px of room on every side keeps a row's focus
   ring (2px, 2px out) whole without moving the rows. */
.rows {
  flex: 1 1 auto;
  min-height: 84px;
  margin: -4px;
  padding: 4px;
  overflow-y: auto;
  scrollbar-width: thin;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.row {
  flex: none;
  width: 100%;
  min-height: 76px;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 14px 10px 12px;
  border-radius: 18px;
  border-color: transparent;
  background: transparent;
  text-align: left;
}

.row:hover:not(:disabled) {
  background: color-mix(in srgb, var(--glass-control) 55%, transparent);
  border-color: transparent;
}

.row.current {
  background: var(--accent-soft);
  border-color: var(--glass-edge);
  box-shadow: inset 0 1px 0 color-mix(in srgb, var(--glass-rim) 40%, transparent);
}

.who {
  flex: 1;
  min-width: 0;
}

.status {
  flex: none;
  font-size: 13px;
  font-weight: 600;
  color: var(--fg-dim);
}

.status.current {
  color: var(--accent);
}

.status.warning {
  color: var(--warning);
}

.status.busy {
  font-weight: 500;
}

/* Three equal cells where the labels fit (as designed); a cell never
   squeezes its label under a neighbour: a language whose labels are wider
   than a third wraps the row instead. */
/* The popover sits inside the nav's own <footer>: a bare `footer button`
   would also match the header's close button. */
.account-popover > footer {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 6px;
  padding-top: 8px;
  border-top: 1px solid var(--line);
}

.account-popover > footer button {
  flex: 1 1 0;
  min-width: max-content;
  min-height: 44px;
  gap: 8px;
  padding: 0 8px;
  border-radius: 14px;
  color: var(--fg);
  font-size: 14px;
  font-weight: 500;
}

.account-popover > footer button.on {
  color: var(--accent);
}

.hint {
  margin: 4px 10px 0;
  font-size: 12px;
  color: var(--fg-dim);
}

@media (prefers-reduced-motion: reduce) {
  .account-popover {
    animation: none;
  }
}
</style>
