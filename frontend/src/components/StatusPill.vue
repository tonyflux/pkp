<script setup lang="ts">
// What floats over the bottom corner, and only while there is something to
// say: a notice, or an index still being built (first build, or Rebuild Index
// after it cleared the index) — the one piece of progress worth reporting
// over the page, since it decides when search and sorting stop going to
// the server. The weekly re-check walks an index that already answers, so
// it stays off the page (Settings shows it). Counts live on the file canvas's
// foot and transfers in the Overview, so the corner is otherwise left
// to the page.
//
// A notice that something worked, or is under way, leaves by itself once
// there has been time to read it, but not from under the pointer or while
// its close button has focus. A failure stays until it is closed: this is
// the only place it is told.
//
// The first build of a big drive takes many minutes: after a few seconds
// its pill folds to the spinning glyph, opening again under the pointer or
// focus; a click folds it at once.
import { computed, onBeforeUnmount, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import { cleanup, dismiss, drive, indexText, ui } from "../store";
import { t } from "../i18n";

// The compact playback bar is a row under the canvas: the pill stays over the
// canvas's corner, above the bar, never over its controls.
defineProps<{ aboveBar?: boolean }>();

const building = computed(() => drive.indexProgress?.phase === "full" && !drive.indexProgress.ready);
// Cleanup's submit dock spans the canvas's foot: the pill sits above it, never over its button.
const lifted = computed(() => ui.route === "cleanup" && cleanup[cleanup.tab].stage === "result");

const pill = ref<HTMLElement>();
let timer: number | undefined;

// 4s, and 0.1s more for each character, up to 10s. Leaving the pill starts
// the time over.
function arm() {
  window.clearTimeout(timer);
  if (!ui.message || ui.messageTone === "error") return;
  timer = window.setTimeout(() => {
    if (!pill.value?.matches(":hover, :focus-within")) dismiss();
  }, Math.min(10_000, 4_000 + 100 * ui.message.length));
}

// A notice that came while the pill was away (the immersive player) gets
// its time once the pill is back.
watch(() => ui.messageSerial, arm, { immediate: true });
onBeforeUnmount(() => {
  window.clearTimeout(timer);
  window.clearTimeout(foldTimer);
});

const folded = ref(false);
const peek = ref(false);
let foldTimer: number | undefined;
watch(
  building,
  (on) => {
    window.clearTimeout(foldTimer);
    folded.value = false;
    if (on) foldTimer = window.setTimeout(() => (folded.value = true), 5_000);
  },
  { immediate: true },
);
const progressOnly = computed(() => building.value && !ui.message);
const mini = computed(() => progressOnly.value && folded.value && !peek.value);

function fold() {
  if (!progressOnly.value) return;
  window.clearTimeout(foldTimer);
  folded.value = true;
  peek.value = false;
}

function leave() {
  peek.value = false;
  arm();
}
</script>

<template>
  <div
    v-if="ui.message || building"
    ref="pill"
    class="pill glass"
    :class="{ alert: ui.message && ui.messageTone === 'error', lifted, 'above-bar': aboveBar, mini }"
    role="status"
    :tabindex="progressOnly ? 0 : undefined"
    :title="mini ? indexText(drive.indexProgress) : undefined"
    @mouseenter="peek = true"
    @mouseleave="leave"
    @focusin="peek = true"
    @focusout="leave"
    @click="fold"
  >
    <template v-if="ui.message">
      <span class="glyph">
        <Icon :name="ui.messageTone === 'error' ? 'alert' : 'info'" :size="18" />
      </span>
      <span class="text message">{{ ui.message }}</span>
      <button class="quiet icon" :title="t('关闭')" @click="dismiss"><Icon name="close" :size="16" /></button>
    </template>
    <template v-else>
      <span class="glyph busy">
        <Icon name="transfer" :size="18" />
        <Orbit class="late" :size="18" :label="t('正在建立索引')" />
      </span>
      <span class="text">{{ indexText(drive.indexProgress) }}</span>
    </template>
  </div>
</template>

<style scoped>
/* Over the content's bottom-right corner, clear of its rounded edge: the
   shell's 20px margin is too thin to hold it. */
.pill {
  /* What stands between the shell's foot and the canvas's: the compact
     playback bar (64px) and the stack's gap under it. */
  --foot: 0px;
  position: absolute;
  right: 32px;
  bottom: calc(30px + var(--foot));
  display: flex;
  align-items: center;
  gap: 14px;
  min-height: 50px;
  padding: 6px 8px;
  /* Half the one-line height: a single line is still a capsule, and a
     wrapped notice grows taller with the same corners. */
  border-radius: 25px;
  font-size: 15px;
  font-weight: 500;
  color: var(--fg);
  max-width: min(640px, calc(100% - 36px));
  z-index: 20;
}

.pill.alert {
  color: var(--danger);
  border-color: color-mix(in srgb, var(--danger) 45%, transparent);
}

.pill.lifted {
  bottom: calc(112px + var(--foot));
}

.pill.above-bar {
  --foot: 80px;
}

@media (max-width: 959px) {
  .pill.above-bar {
    --foot: 76px;
  }
}

.text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  padding-right: 10px;
}

/* No trailing button: the text still needs the right-hand breathing room. */
.text:last-child {
  padding-right: 14px;
}

/* A notice names whole files and where they went, so it wraps instead of
   losing its end to the ellipsis. The block padding keeps one line within
   the 36px glyph, so the pill only grows once the text wraps. */
.message {
  padding-block: 7px;
  white-space: normal;
  overflow-wrap: anywhere;
}

/* The text gives way to a long notice; the round buttons never do. */
.pill > button {
  flex: none;
}

/* The glyph sits in a darker well cut into the pill, as in the mock. */
.glyph {
  display: grid;
  place-items: center;
  flex: none;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: var(--glass-inset);
  box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.12);
}

.glyph > * {
  grid-area: 1 / 1;
}

/* Same 350ms as .late, so the icon leaves as the orbit arrives. */
.glyph > svg {
  transition: opacity 0.2s var(--ease) 0.35s;
}

.glyph.busy > svg {
  opacity: 0;
}

/* Folded: just the glyph, in a circle (6px all round, as the pill's height
   already has). The text stays for VoiceOver. */
.pill.mini {
  gap: 0;
  padding-inline: 6px;
}

.pill.mini .text {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  overflow: hidden;
  clip-path: inset(50%);
}
</style>
