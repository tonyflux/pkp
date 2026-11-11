<script lang="ts">
export interface SelectOption<T extends string | number = string | number> {
  value: T;
  label: string;
  /** A quieter hint after the label: a track's language and format. */
  detail?: string;
  /** Options sharing a group sit under its heading, as an <optgroup> did. */
  group?: string;
  disabled?: boolean;
}
</script>

<script setup lang="ts" generic="T extends string | number">
// One value from a list, chosen in the app's own glass menu instead of the
// system's select popup, which follows neither the theme nor the skin. The
// face is the slot, so each site keeps its own pill or field; with no slot it
// is the current label. The list opens under the face (above it when there is
// more room there), teleported to <body> so no scrolling or clipping parent
// cuts it off, and follows the face when something scrolls.
//
// Keys: on the face Enter, Space and ↑/↓ open the list on the current value;
// in the list ↑/↓ move, Home/End jump, Enter/Space choose, Esc and Tab close
// with focus back on the face. Handled keys stop here, so page shortcuts (the
// player's space and arrows) stay out; `isTyping` and `inSelectMenu` in dom.ts
// tell window-level handlers the key is ours.
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from "vue";
import Icon from "./Icon.vue";
import { useAnchoredList, useOutsideClick } from "../dom";

const props = defineProps<{
  modelValue: T;
  options: SelectOption<T>[];
  label?: string;
  disabled?: boolean;
  /** Explicitly choosing the current value can cancel a pending subtitle change. */
  allowReselect?: boolean;
  /** The player's fixed dark glass, for a list over the native picture. */
  dark?: boolean;
  /** Lay a long flat list (Type) out in this many columns, filled row by row. */
  columns?: number;
}>();
const emit = defineEmits<{ "update:modelValue": [value: T]; open: []; close: [] }>();

const id = useId();
const face = ref<HTMLElement>();
const list = ref<HTMLElement>();
const rows = ref<HTMLElement>();
const open = ref(false);
const active = ref(0);
const { spot, place } = useAnchoredList(() => face.value, list, rows, open, () => hide());

const current = computed(() => props.options.find((o) => o.value === props.modelValue));

async function show() {
  if (open.value || props.disabled) return;
  emit("open");
  spot.placed = false;
  await nextTick();
  const selected = props.options.findIndex((o) => o.value === props.modelValue && !o.disabled);
  active.value = selected >= 0 ? selected : props.options.findIndex((o) => !o.disabled);
  open.value = true;
  await nextTick();
  place();
  // Focusable only once drawn: the list stays hidden until it is placed.
  await nextTick();
  list.value?.querySelectorAll<HTMLElement>("[role=option]")[active.value]?.focus();
}

function hide(refocus = false) {
  if (!open.value) return;
  open.value = false;
  emit("close");
  if (refocus) face.value?.focus({ preventScroll: true });
}

function choose(option: SelectOption<T> | undefined) {
  if (!option || option.disabled) return;
  hide(true);
  if (props.allowReselect || option.value !== props.modelValue) emit("update:modelValue", option.value);
}

/** move goes `step` rows from `from`, past disabled ones; it stays put at either end. */
function move(from: number, step: number) {
  for (let i = from + step; i >= 0 && i < props.options.length; i += step) {
    if (!props.options[i].disabled) {
      active.value = i;
      list.value?.querySelectorAll<HTMLElement>("[role=option]")[i]?.focus();
      return;
    }
  }
}

// In columns ↑/↓ go a row up or down and ←/→ go along the row.
const across = computed(() => Math.max(1, props.columns ?? 1));

function onFaceKey(event: KeyboardEvent) {
  switch (event.key) {
    case "Enter":
    case " ":
    case "ArrowDown":
    case "ArrowUp":
      void show();
      break;
    default:
      return;
  }
  event.preventDefault();
  event.stopPropagation();
}

function onListKey(event: KeyboardEvent) {
  switch (event.key) {
    case "ArrowDown":
      move(active.value, across.value);
      break;
    case "ArrowUp":
      move(active.value, -across.value);
      break;
    case "ArrowRight":
      if (across.value === 1) return;
      move(active.value, 1);
      break;
    case "ArrowLeft":
      if (across.value === 1) return;
      move(active.value, -1);
      break;
    case "Home":
      move(-1, 1);
      break;
    case "End":
      move(props.options.length, -1);
      break;
    case "Enter":
    case " ":
      choose(props.options[active.value]);
      break;
    case "Escape":
    case "Tab":
      hide(true);
      break;
    default:
      return;
  }
  event.preventDefault();
  event.stopPropagation();
}

// A list grown or shrunk while open (options loading in) is placed again.
watch(
  () => props.options.length,
  async () => {
    if (!open.value) return;
    await nextTick();
    place();
  },
);
watch(
  () => props.disabled,
  (off) => off && hide(),
);

// Gone while open (its row hidden): the parent still hears the list close.
onBeforeUnmount(() => {
  if (open.value) emit("close");
});

useOutsideClick(`[data-select-menu="${id}"]`, () => open.value, () => hide());
</script>

<template>
  <div
    ref="face"
    class="select-menu"
    :class="{ open }"
    role="combobox"
    :tabindex="disabled ? -1 : 0"
    aria-haspopup="listbox"
    :aria-expanded="open"
    :aria-controls="open ? `${id}-list` : undefined"
    :aria-label="label"
    :aria-disabled="disabled || undefined"
    :data-select-menu="id"
    @click="open ? hide(true) : show()"
    @keydown="onFaceKey"
  >
    <slot :label="current?.label ?? ''" :open="open">{{ current?.label }}</slot>
    <!-- Teleported from inside the one root: a fragment root would not carry
         the parent's scoped-style attribute, and sites' face rules would miss. -->
    <Teleport to="body">
      <div
        v-if="open"
        :id="`${id}-list`"
        ref="list"
        class="select-list"
        :class="dark ? 'dark' : 'glass glass-sheet'"
        role="listbox"
        :aria-label="label"
        :data-select-menu="id"
        data-select-list
        :style="{
          top: `${spot.top}px`,
          left: `${spot.left}px`,
          minWidth: `${spot.width}px`,
          maxHeight: spot.maxHeight ? `${spot.maxHeight}px` : undefined,
          visibility: spot.placed ? undefined : 'hidden',
        }"
        @keydown="onListKey"
      >
        <!-- The glass's rim and sheen are absolute layers of this box: the
             rows scroll in a box of their own, or the rim would scroll away
             with the first screen of rows. -->
        <div ref="rows" class="rows" :class="{ grid: across > 1 }" :style="across > 1 ? { '--columns': across } : undefined">
          <template v-for="(o, i) in options" :key="String(o.value)">
            <div v-if="o.group && o.group !== options[i - 1]?.group" class="group" role="presentation">{{ o.group }}</div>
            <button
              type="button"
              class="quiet"
              :class="{ on: o.value === modelValue }"
              role="option"
              :aria-selected="o.value === modelValue"
              :disabled="o.disabled"
              :tabindex="i === active ? 0 : -1"
              @click="choose(o)"
              @focus="active = i"
            >
              <span>{{ o.label }}</span>
              <em v-if="o.detail">{{ o.detail }}</em>
              <Icon v-if="o.value === modelValue" name="check" :size="15" />
            </button>
          </template>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped src="./select-list.css"></style>

<style scoped>
.select-menu {
  cursor: pointer;
  outline: none;
}

.select-menu:focus-visible {
  outline: 2px solid color-mix(in srgb, var(--accent) 70%, transparent);
  outline-offset: 2px;
}

.select-menu[aria-disabled="true"] {
  cursor: default;
}
</style>
