<script setup lang="ts">
// The modal frame every AI Assistant sheet shares (Authorization, In-place configuration, Final confirmation): a
// dimmed scrim over the canvas, focus held inside while it is up, Esc and
// the close button as the sheet's own 「Don't send / Close / Continue checking」, and focus
// handed back to whatever opened it. The content is the caller's; the
// shared type and button styles reach it through :slotted.
import { nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import Icon from "./Icon.vue";
import { inSelectMenu } from "../dom";
import { t } from "../i18n";

const props = withDefaults(defineProps<{ labelledby: string; dismissable?: boolean }>(), { dismissable: true });
const emit = defineEmits<{ (e: "close"): void }>();

const panel = ref<HTMLElement>();
// Whatever had focus when the sheet came up: the Send button, Check and confirm, …
let opener: HTMLElement | null = null;

function focusables(): HTMLElement[] {
  const found = panel.value?.querySelectorAll<HTMLElement>(
    "button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], [tabindex]:not([tabindex='-1'])",
  );
  return found ? Array.from(found).filter((el) => el.offsetParent !== null || el === document.activeElement) : [];
}

function close() {
  if (props.dismissable) emit("close");
}

function onKeydown(event: KeyboardEvent) {
  if (event.key !== "Tab") return;
  const items = focusables();
  if (!items.length) return;
  const first = items[0];
  const last = items[items.length - 1];
  if (event.shiftKey && (document.activeElement === first || !panel.value?.contains(document.activeElement))) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

// The sheet owns Esc ahead of the page behind it; an open select menu inside keeps its own.
function onEscape(event: KeyboardEvent) {
  if (event.key !== "Escape" || inSelectMenu(event)) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  close();
}

// Focus that wanders out (a click on the scrim, a stray Tab from the page) comes back in.
function onFocusIn(event: FocusEvent) {
  if (!panel.value || panel.value.contains(event.target as Node)) return;
  (focusables()[0] ?? panel.value).focus({ preventScroll: true });
}

// A press that starts and ends on the scrim closes; a drag out of the sheet does not.
let pressedScrim = false;
function onScrimDown(event: PointerEvent) {
  pressedScrim = event.target === event.currentTarget;
}
function onScrimClick(event: MouseEvent) {
  if (pressedScrim && event.target === event.currentTarget) close();
  pressedScrim = false;
}

onMounted(async () => {
  opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  window.addEventListener("keydown", onEscape, true);
  document.addEventListener("focusin", onFocusIn);
  await nextTick();
  const start = panel.value?.querySelector<HTMLElement>("[data-autofocus]:not(:disabled)") ?? focusables()[0] ?? panel.value;
  start?.focus({ preventScroll: true });
});

onBeforeUnmount(() => {
  window.removeEventListener("keydown", onEscape, true);
  document.removeEventListener("focusin", onFocusIn);
  // Back to the opener once this sheet is gone, if the page still has it and
  // it can take focus; else to the composer, where the next step starts. A
  // sheet that came up in its place has focus already and keeps it.
  const back = opener;
  void nextTick(() => {
    if (document.activeElement && document.activeElement !== document.body) return;
    if (back?.isConnected && !back.matches(":disabled")) back.focus({ preventScroll: true });
    else document.getElementById("ai-prompt")?.focus({ preventScroll: true });
  });
});
</script>

<template>
  <Teleport to="body">
    <div class="scrim ai-scrim" @pointerdown="onScrimDown" @click="onScrimClick">
      <section
        ref="panel"
        class="sheet glass glass-sheet ai-sheet"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="labelledby"
        tabindex="-1"
        @keydown="onKeydown"
      >
        <button v-if="dismissable" class="quiet icon ai-sheet-close" type="button" :aria-label="t('关闭')" :title="t('关闭')" @click="close">
          <Icon name="close" :size="17" />
        </button>
        <slot />
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.ai-scrim {
  padding: 16px;
  overflow-y: auto;
}

/* 495px wide; a short window scrolls the sheet itself,
   so its buttons stay reachable at any height. */
.ai-sheet {
  position: relative;
  width: min(495px, calc(100vw - 32px));
  max-height: calc(100dvh - 32px);
  overflow-y: auto;
  overscroll-behavior: contain;
  gap: 0;
  padding: 26px 28px;
  border-radius: 23px;
  color: var(--fg);
  /* Rises without fading in: a sheet that comes up while the window is in
     the background (WebKit holds its animations there) is still readable. */
  animation: ai-sheet-rise var(--dur) var(--ease);
}

.ai-sheet :deep(h2),
.ai-sheet :deep(p),
.ai-sheet :deep(.sheet-eyebrow),
.ai-sheet :deep(.disclosure strong),
.ai-sheet :deep(.lines span),
.ai-sheet :deep(.lines strong),
.ai-sheet :deep(.folders > span),
.ai-sheet :deep(.folders > strong),
.ai-sheet :deep(.folders > small),
.ai-sheet :deep(.steps > span) {
  -webkit-user-select: text;
  user-select: text;
  --default-contextmenu: show;
}

.ai-sheet :deep(button),
.ai-sheet :deep(svg) {
  -webkit-user-select: none;
  user-select: none;
  --default-contextmenu: auto;
}

@keyframes ai-sheet-rise {
  from {
    transform: translateY(14px) scale(0.985);
  }
}

.ai-sheet-close {
  position: absolute;
  top: 13px;
  right: 15px;
  z-index: 1;
  width: 34px;
  height: 34px;
  min-height: 34px;
  border-radius: 9px;
}

.ai-sheet :slotted(.sheet-eyebrow) {
  display: block;
  padding-right: 40px;
  font-size: 11px;
  font-weight: 650;
  letter-spacing: 0.08em;
  color: var(--accent);
  overflow-wrap: anywhere;
}

.ai-sheet :slotted(h2) {
  margin: 9px 0;
  padding-right: 24px;
  font-size: 23px;
  line-height: 1.35;
  font-weight: 620;
  letter-spacing: -0.025em;
  text-wrap: balance;
}

.ai-sheet :slotted(.sheet-lede) {
  margin: 0;
  font-size: 12px;
  line-height: 1.7;
  color: var(--fg-dim);
  overflow-wrap: anywhere;
  text-wrap: pretty;
}

.ai-sheet :slotted(.sheet-small) {
  margin: 15px 0 0;
  font-size: 11px;
  line-height: 1.6;
  color: var(--fg-dim);
  overflow-wrap: anywhere;
  text-wrap: pretty;
}

.ai-sheet :slotted(.sheet-error) {
  margin: 12px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--danger);
  overflow-wrap: anywhere;
}

.ai-sheet :slotted(.sheet-actions) {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 21px;
}

.ai-sheet :slotted(.sheet-btn) {
  min-height: 39px;
  padding: 0 15px;
  border-radius: 12px;
  font-size: 12px;
  font-weight: 600;
}

@media (max-width: 550px) {
  .ai-sheet {
    padding: 24px 18px;
  }

  .ai-sheet :slotted(.sheet-btn) {
    flex: 1;
  }
}

@media (prefers-contrast: more) {
  .ai-sheet {
    border: 1px solid var(--fg);
  }
}
</style>
