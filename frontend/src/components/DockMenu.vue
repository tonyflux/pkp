<script setup lang="ts">
// A dock entry that opens its menu above itself: Subtitles/Audio tracks show only an icon,
// Quality/Speed show the current value. The body is the slot, handed `close`.
// Enter, Space or ↑/↓ open it on the current row; Esc, a click elsewhere
// or tabbing out close it, the first and last with focus back on the entry.
// All of them stay away from the player's own shortcuts.
import { ref, useId, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import { useOutsideClick } from "../dom";
import { t } from "../i18n";

defineProps<{
  icon: string;
  label?: string;
  value: string;
  title: string;
  iconOnly?: boolean;
  statusDot?: boolean;
  busy?: boolean;
  failed?: boolean;
}>();
const open = defineModel<boolean>("open", { default: false });

const id = useId();
const entry = ref<HTMLButtonElement>();
const popover = ref<HTMLElement>();

// Focus the selected row, falling back to the first keyboard-accessible option.
function focusCurrent() {
  const el = popover.value;
  (el?.querySelector<HTMLElement>('[role=option][aria-selected="true"]') ??
    el?.querySelector<HTMLElement>('[role=option][tabindex="0"]'))?.focus();
}

// The list exists only once the popover has rendered.
watch(
  open,
  (now) => {
    if (now) focusCurrent();
  },
  { flush: "post" },
);

function close() {
  open.value = false;
  entry.value?.focus();
}

function onFocusOut(event: FocusEvent) {
  const next = event.relatedTarget as Node | null;
  if (next && !(event.currentTarget as HTMLElement).contains(next)) open.value = false;
}

function onEntryKey(event: KeyboardEvent) {
  switch (event.key) {
    case "Enter":
    case " ":
      open.value = !open.value;
      break;
    case "ArrowUp":
    case "ArrowDown":
      if (open.value) focusCurrent();
      else open.value = true;
      break;
    case "Escape":
      if (!open.value) return;
      open.value = false;
      break;
    default:
      return;
  }
  event.preventDefault();
  event.stopPropagation();
}

useOutsideClick(`[data-dock-menu="${id}"]`, () => open.value, () => (open.value = false));
</script>

<template>
  <div class="dock-menu" :data-dock-menu="id" @focusout="onFocusOut">
    <button
      ref="entry"
      type="button"
      class="entry"
      :class="{ on: open, failed: failed && !busy, 'icon-only': iconOnly, 'status-dot': statusDot }"
      aria-haspopup="listbox"
      :aria-expanded="open"
      :aria-label="title"
      :title="title"
      @click="open = !open"
      @keydown="onEntryKey"
    >
      <Orbit v-if="busy" :size="16" :label="t('切换中')" />
      <Icon v-else :name="failed ? 'alert' : icon" :size="18" />
      <span v-if="!iconOnly && label">{{ label }}</span>
      <b v-if="!iconOnly">{{ value }}</b>
    </button>
    <div v-if="open" ref="popover" class="popover">
      <slot :close="close" />
    </div>
  </div>
</template>

<style scoped>
.dock-menu {
  position: relative;
  flex: none;
}

/* Secondary controls sit on the dock itself; keep their 40px hit area. */
.entry {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 40px;
  padding: 0 14px;
  border-radius: 12px;
  border: 1px solid transparent;
  background: transparent;
  box-shadow: none;
  color: #e3eefb;
  font-size: 15px;
  font-weight: 500;
  white-space: nowrap;
}

.entry.icon-only {
  position: relative;
  width: 40px;
  padding: 0;
}

.entry.icon-only.status-dot::after {
  content: "";
  position: absolute;
  left: 50%;
  bottom: 3px;
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: #82caff;
  transform: translateX(-50%);
}

.entry:hover:not(:disabled),
.entry:focus-visible {
  border-color: transparent;
  background: rgba(255, 255, 255, 0.12);
}

.entry.on,
.entry.on:hover:not(:disabled) {
  border-color: transparent;
  background: rgba(139, 201, 255, 0.16);
}

.entry.failed:not(.on) {
  border-color: rgba(255, 150, 140, 0.55);
  color: #ffe2de;
}

.entry b {
  font-weight: 600;
}

.entry.on b {
  color: #c8f3ff;
}

/* Above the entry, its right edge just past the entry's, the tail over it. */
.popover {
  position: absolute;
  right: -13px;
  bottom: calc(100% + 13px);
  z-index: 4;
  width: 254px;
  padding: 14px;
  border: 1px solid rgba(201, 238, 255, 0.72);
  border-radius: 20px;
  background: linear-gradient(145deg, rgba(53, 87, 134, 0.96), rgba(16, 31, 61, 0.98));
  box-shadow:
    inset 0 1px rgba(255, 255, 255, 0.54),
    0 18px 34px rgba(2, 14, 38, 0.46);
  animation: pop-in 0.18s var(--ease) both;
}

.popover::after {
  content: "";
  position: absolute;
  right: 40px;
  bottom: -7px;
  width: 13px;
  height: 13px;
  transform: rotate(45deg);
  background: #203d6a;
  border-right: 1px solid rgba(201, 238, 255, 0.58);
  border-bottom: 1px solid rgba(201, 238, 255, 0.58);
}

/* A rise without a fade: inside the stage's size container, Chromium leaves a
   finished opacity animation drawn at about half strength (measured in the
   headless harness), and the menu sits over the picture. */
@keyframes pop-in {
  from {
    transform: translateY(6px);
  }
}
</style>
