<script setup lang="ts">
// The rows of the player's own menus (Quality, Playback Speed): a custom listbox
// instead of the system's select popup.
//
// Keys, per the design contract: ↑/↓ move, Home/End jump, Enter or Space
// choose, Esc closes. Handled keys stop here, so the player's own shortcuts
// (space to pause, arrows to seek and set the volume) stay out of the menu.
// The selected row is the one Tab and the opening menu land on. With
// `columns` the options sit in a grid (the compact Playback Speed in More): ←/→ move by
// one, ↑/↓ by a row.
import { nextTick, ref, watch } from "vue";

interface OptionItem {
  key: string;
  label: string;
  /** The hint on the right: Current, Recommended, Switching… or nothing. */
  detail?: string;
  selected: boolean;
  pending?: boolean;
  disabled?: boolean;
}

const props = withDefaults(defineProps<{ items: OptionItem[]; labelledby: string; busy?: boolean; columns?: number }>(), {
  columns: 1,
});
const emit = defineEmits<{ choose: [key: string]; close: [] }>();

const list = ref<HTMLElement>();
const currentIndex = () => {
  const selected = props.items.findIndex((r) => r.selected && !r.disabled);
  return selected >= 0 ? selected : props.items.findIndex((r) => !r.disabled);
};
const active = ref(currentIndex());
watch(() => props.items, () => {
  if (!props.items[active.value] || props.items[active.value].disabled) active.value = currentIndex();
});

function option(i: number): HTMLElement | undefined {
  return list.value?.querySelectorAll<HTMLElement>("[role=option]")[i];
}

/** focusCurrent puts focus on the selected row, as the menu opens. */
async function focusCurrent() {
  await nextTick();
  active.value = currentIndex();
  option(active.value)?.focus();
}

function move(from: number, step: number) {
  for (let i = from + step; i >= 0 && i < props.items.length; i += step) {
    if (props.items[i].disabled) continue;
    active.value = i;
    option(i)?.focus();
    return;
  }
}

function choose(item: OptionItem | undefined) {
  if (item && !item.disabled && !props.busy) emit("choose", item.key);
}

function onKey(event: KeyboardEvent) {
  switch (event.key) {
    case "ArrowDown":
      move(active.value, props.columns);
      break;
    case "ArrowUp":
      move(active.value, -props.columns);
      break;
    case "ArrowRight":
      if (props.columns === 1) return;
      move(active.value, 1);
      break;
    case "ArrowLeft":
      if (props.columns === 1) return;
      move(active.value, -1);
      break;
    case "Home":
      move(-1, 1);
      break;
    case "End":
      move(props.items.length, -1);
      break;
    case "Enter":
    case " ":
      choose(props.items[active.value]);
      break;
    case "Escape":
      emit("close");
      break;
    default:
      return;
  }
  event.preventDefault();
  event.stopPropagation();
}

defineExpose({ focusCurrent });
</script>

<template>
  <div
    ref="list"
    class="list"
    :class="{ grid: columns > 1 }"
    :style="columns > 1 ? { gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` } : undefined"
    role="listbox"
    :aria-labelledby="labelledby"
    :aria-busy="busy"
    @keydown="onKey"
  >
    <div
      v-for="(item, i) in items"
      :key="item.key"
      role="option"
      class="option"
      :class="{ selected: item.selected, pending: item.pending, disabled: item.disabled }"
      :aria-selected="item.selected"
      :aria-disabled="busy || item.disabled || undefined"
      :tabindex="i === active && !item.disabled ? 0 : -1"
      @click="choose(item)"
      @focus="active = i"
    >
      <span>{{ item.label }}</span>
      <em v-if="item.detail">{{ item.detail }}</em>
    </div>
  </div>
</template>

<style scoped>
.list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 6px;
}

/* A grid of short values: centred, the selection shown by the cell alone. */
.list.grid {
  display: grid;
}

.grid .option {
  justify-content: center;
  padding: 0 4px;
}

.grid .option span {
  flex: none;
}

.grid .option.selected::before,
.grid .option em {
  display: none;
}

.option {
  display: flex;
  align-items: center;
  gap: 7px;
  height: 38px;
  padding: 0 9px;
  border: 1px solid transparent;
  border-radius: 11px;
  cursor: pointer;
  outline: none;
  transition:
    background var(--dur) var(--ease),
    border-color var(--dur) var(--ease);
}

.option:hover,
.option.selected {
  border-color: rgba(165, 224, 255, 0.48);
  background: rgba(112, 187, 255, 0.2);
}

/* Visible on the bright snow and the dark water alike: the menu is opaque. */
.option:focus-visible {
  box-shadow: 0 0 0 2px #8de7ff;
}

.option[aria-disabled="true"] {
  cursor: progress;
}

.option.disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.option.selected::before {
  content: "";
  flex: none;
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: #8de7ff;
  box-shadow: 0 0 8px #8de7ff;
}

.option span {
  flex: 1;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.option.selected span {
  font-weight: 650;
}

.option em {
  flex: none;
  font-style: normal;
  font-size: 11px;
  color: #a9c9df;
}

.option.pending em {
  color: #c8f3ff;
}
</style>
