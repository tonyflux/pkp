<script lang="ts">
export interface SuggestOption {
  /** What picking it writes into the field. */
  value: string;
  /** A quieter hint after the value: whose base URL it is. */
  detail?: string;
}
</script>

<script setup lang="ts">
// Suggestions for a text field in the glass list SelectMenu opens, instead of
// a <datalist>, whose popup is the system's and follows neither the theme nor
// the skin. The field is the slot — one <input>, bound and styled by the site
// as before; the list only offers values for it. A click on the field or ↓
// shows every suggestion, typing narrows them to those containing the text.
// Picking one writes it in as typing would (an `input` event, so v-model
// follows) and commits it with a `change` event, so the site's
// save-on-change runs; focus stays in the field.
//
// Keys stay in the field: ↑/↓ mark a suggestion, Enter picks the marked one
// (with none marked it is the site's), Esc closes the list only, Tab closes
// it and moves on. While the list is open the wrapper carries
// data-select-list, so a sheet's Esc handler (`inSelectMenu`) leaves the key
// to it.
import { computed, nextTick, onMounted, ref, useId, watch, watchEffect } from "vue";
import Icon from "./Icon.vue";
import { useAnchoredList, useOutsideClick } from "../dom";

const props = defineProps<{ options: SuggestOption[]; label?: string }>();

const id = useId();
const root = ref<HTMLElement>();
const list = ref<HTMLElement>();
const rows = ref<HTMLElement>();
const open = ref(false);
// What was typed since the list opened; "" shows every suggestion.
const query = ref("");
// The field's text, for the check on the suggestion it already holds.
const text = ref("");
// The suggestion ↑/↓ marked, -1 for none.
const active = ref(-1);

const field = () => root.value?.querySelector("input") ?? undefined;
const { spot, place } = useAnchoredList(field, list, rows, open, () => hide());

const shown = computed(() => {
  const q = query.value.trim().toLowerCase();
  if (!q) return props.options;
  return props.options.filter((o) => o.value.toLowerCase().includes(q) || !!o.detail?.toLowerCase().includes(q));
});

async function show(mark: boolean) {
  const input = field();
  if (!input || input.disabled || input.readOnly) return;
  text.value = input.value;
  if (!shown.value.length) {
    hide();
    return;
  }
  active.value = mark ? Math.max(0, shown.value.findIndex((o) => o.value === text.value)) : -1;
  if (open.value) return;
  spot.placed = false;
  open.value = true;
  await nextTick();
  place();
}

function hide() {
  open.value = false;
  active.value = -1;
  query.value = "";
}

function pick(option: SuggestOption | undefined) {
  const input = field();
  hide();
  if (!input || !option || input.value === option.value) return;
  input.value = option.value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  input.dispatchEvent(new Event("change", { bubbles: true }));
}

/** mark moves the mark `step` suggestions on; it stays put at either end. */
function mark(step: number) {
  const next = active.value < 0 ? (step > 0 ? 0 : shown.value.length - 1) : active.value + step;
  if (next < 0 || next >= shown.value.length) return;
  active.value = next;
  list.value?.querySelector(`#${CSS.escape(`${id}-${next}`)}`)?.scrollIntoView({ block: "nearest" });
}

function onClick(event: MouseEvent) {
  if (event.target !== field()) return;
  query.value = "";
  void show(false);
}

function onInput(event: Event) {
  // Our own pick dispatches untrusted events: those only feed v-model.
  if (event.target !== field() || !event.isTrusted) return;
  query.value = (event.target as HTMLInputElement).value;
  void show(false);
}

function onKey(event: KeyboardEvent) {
  if (event.target !== field() || event.isComposing) return;
  switch (event.key) {
    case "ArrowDown":
    case "ArrowUp":
      if (!open.value) {
        query.value = "";
        void show(true);
      } else mark(event.key === "ArrowDown" ? 1 : -1);
      break;
    case "Enter":
      if (active.value < 0) {
        hide();
        return;
      }
      pick(shown.value[active.value]);
      break;
    case "Escape":
      if (!open.value) return;
      hide();
      break;
    case "Tab":
      hide();
      return;
    default:
      return;
  }
  event.preventDefault();
  event.stopPropagation();
}

// Narrowed or widened while open: placed again (it may sit above the field).
watch(
  () => shown.value.length,
  async () => {
    if (!open.value) return;
    await nextTick();
    place();
  },
);

onMounted(() => {
  const input = field();
  input?.setAttribute("role", "combobox");
  input?.setAttribute("aria-autocomplete", "list");
});
watchEffect(() => {
  const input = field();
  if (!input) return;
  input.setAttribute("aria-expanded", String(open.value));
  if (open.value) input.setAttribute("aria-controls", `${id}-list`);
  else input.removeAttribute("aria-controls");
  if (open.value && active.value >= 0) input.setAttribute("aria-activedescendant", `${id}-${active.value}`);
  else input.removeAttribute("aria-activedescendant");
});

useOutsideClick(`[data-select-menu="${id}"]`, () => open.value, hide);
</script>

<template>
  <span
    ref="root"
    class="suggest-menu"
    :data-select-menu="id"
    :data-select-list="open ? '' : undefined"
    @click="onClick"
    @input="onInput"
    @keydown.capture="onKey"
    @focusout="hide"
  >
    <slot />
    <Teleport to="body">
      <!-- mousedown would take focus from the field and close the list before the click lands. -->
      <div
        v-if="open"
        :id="`${id}-list`"
        ref="list"
        class="select-list glass glass-sheet"
        role="listbox"
        :aria-label="label"
        :data-select-menu="id"
        :style="{
          top: `${spot.top}px`,
          left: `${spot.left}px`,
          minWidth: `${spot.width}px`,
          maxHeight: spot.maxHeight ? `${spot.maxHeight}px` : undefined,
          visibility: spot.placed ? undefined : 'hidden',
        }"
        @mousedown.prevent
      >
        <div ref="rows" class="rows">
          <button
            v-for="(o, i) in shown"
            :id="`${id}-${i}`"
            :key="o.value"
            type="button"
            class="quiet"
            :class="{ on: o.value === text, active: i === active }"
            role="option"
            :aria-selected="o.value === text"
            tabindex="-1"
            @click="pick(o)"
          >
            <span>{{ o.value }}</span>
            <em v-if="o.detail">{{ o.detail }}</em>
            <Icon v-if="o.value === text" name="check" :size="15" />
          </button>
        </div>
      </div>
    </Teleport>
  </span>
</template>

<style scoped src="./select-list.css"></style>

<style scoped>
/* The field lays out as if it were not wrapped. */
.suggest-menu {
  display: contents;
}

/* Focus stays in the field, so the marked row draws SelectMenu's focus ring. */
.select-list button.active {
  outline: 2px solid color-mix(in srgb, var(--accent) 70%, transparent);
  outline-offset: -2px;
}
</style>
