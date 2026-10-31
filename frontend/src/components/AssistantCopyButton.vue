<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { Clipboard } from "@wailsio/runtime";
import Icon from "./Icon.vue";
import { t } from "../i18n";

const props = defineProps<{ text: string; label: string }>();
const state = ref<"idle" | "copying" | "success" | "error">("idle");
const feedback = computed(() => {
  if (state.value === "copying") return t("正在复制…");
  if (state.value === "success") return t("assistant|已复制");
  if (state.value === "error") return t("复制失败，请重试或选中文字手动复制");
  return "";
});
let reset: ReturnType<typeof setTimeout> | undefined;
let revision = 0;
function clearReset() {
  clearTimeout(reset);
  reset = undefined;
}
watch(() => props.text, () => {
  revision++;
  clearReset();
  state.value = "idle";
});
onBeforeUnmount(() => {
  revision++;
  clearReset();
});

async function copy() {
  if (state.value === "copying") return;
  clearReset();
  const current = ++revision;
  const text = props.text;
  state.value = "copying";
  try {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      await Clipboard.SetText(text);
    }
    if (current !== revision) return;
    state.value = "success";
    reset = setTimeout(() => { state.value = "idle"; }, 2200);
  } catch {
    if (current === revision) state.value = "error";
  }
}
</script>

<template>
  <span class="assistant-copy">
    <button type="button" class="quiet" :disabled="state === 'copying'" :aria-label="label" @click="copy">
      <Icon :name="state === 'success' ? 'check' : 'copy'" :size="13" />
      {{ label }}
    </button>
    <span class="copy-feedback" :class="{ error: state === 'error' }" role="status" aria-live="polite" aria-atomic="true">{{ feedback }}</span>
  </span>
</template>

<style scoped>
.assistant-copy {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 5px 8px;
  font-size: 11px;
  line-height: 1.5;
  -webkit-user-select: none;
  user-select: none;
}
.assistant-copy button {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-height: 26px;
  padding: 4px 7px;
  border-radius: 6px;
  color: var(--fg-dim);
  font-size: inherit;
  transition: none;
}
.assistant-copy button:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}
.copy-feedback { color: var(--fg-dim); }
.copy-feedback.error { color: var(--danger); }
</style>
