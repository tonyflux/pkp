<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import { inSelectMenu } from "../dom";
import { t } from "../i18n";
import Icon from "./Icon.vue";
import { accounts, appLocked } from "../store";
const props = withDefaults(defineProps<{ title: string; busy?: boolean }>(), { busy: false });
const emit = defineEmits<{ close: [] }>();
const panel = ref<HTMLElement>();
let opener: HTMLElement | null = null;
let pressedScrim = false;
function focusables(): HTMLElement[] {
  return Array.from(panel.value?.querySelectorAll<HTMLElement>("button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], summary, [tabindex]:not([tabindex='-1'])") ?? []).filter((el) => el.offsetParent !== null);
}
function onKey(event: KeyboardEvent) {
  if (appLocked.value || accounts.dialog) return;
  if (event.key === "Escape" && !inSelectMenu(event)) {
    event.preventDefault(); event.stopImmediatePropagation();
    if (!props.busy) emit("close");
  } else if (event.key === "Tab") {
    const items = focusables(), first = items[0], last = items.at(-1);
    if (!first) { event.preventDefault(); panel.value?.focus(); }
    else if (event.shiftKey && (document.activeElement === first || document.activeElement === panel.value)) { event.preventDefault(); last?.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }
}
function onFocus(event: FocusEvent) {
  if (appLocked.value || accounts.dialog) return;
  if (panel.value && !panel.value.contains(event.target as Node)) (focusables()[0] ?? panel.value).focus({ preventScroll: true });
}
onMounted(async () => {
  opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  window.addEventListener("keydown", onKey, true); document.addEventListener("focusin", onFocus);
  await nextTick();
  (panel.value?.querySelector<HTMLElement>("[data-autofocus]") ?? panel.value)?.focus({ preventScroll: true });
});
onBeforeUnmount(() => {
  window.removeEventListener("keydown", onKey, true); document.removeEventListener("focusin", onFocus);
  const back = opener;
  void nextTick(() => { if (back?.isConnected && !back.matches(":disabled") && (!document.activeElement || document.activeElement === document.body)) back.focus({ preventScroll: true }); });
});
</script>
<template>
  <Teleport to="body">
    <div class="scrim sync-scrim" @pointerdown="pressedScrim = $event.target === $event.currentTarget" @click="if (pressedScrim && $event.target === $event.currentTarget && !busy) emit('close'); pressedScrim = false">
      <section ref="panel" class="sheet glass glass-sheet sync-modal" role="dialog" aria-modal="true" :aria-label="title" tabindex="-1" @keydown.stop>
        <header><h2>{{ title }}</h2><button class="quiet icon" :disabled="busy" :aria-label="t('关闭')" @click="emit('close')"><Icon name="close" :size="18" /></button></header>
        <div class="sync-modal-body"><slot /></div>
        <footer><slot name="footer" /></footer>
      </section>
    </div>
  </Teleport>
</template>
<style scoped>
.sync-scrim{padding:16px}.sync-modal{width:min(680px,calc(100vw - 32px));max-height:calc(100dvh - 32px);min-height:0;display:flex;flex-direction:column;padding:0;gap:0;overflow:hidden;animation-duration:180ms}.sync-modal>header{display:flex;align-items:flex-start;justify-content:space-between;gap:16px;padding:24px 28px 16px;flex:none}.sync-modal h2{font-size:24px;line-height:1.35;margin:0;overflow-wrap:anywhere}.sync-modal>header button{width:40px;height:40px;min-height:40px;flex:none;margin:-6px -10px 0 0}.sync-modal-body{min-height:0;overflow:auto;overscroll-behavior:contain;padding:0 28px 24px}.sync-modal>footer{display:flex;align-items:center;justify-content:flex-end;flex-wrap:wrap;gap:10px;border-top:1px solid var(--line);padding:16px 28px;flex:none}.sync-modal>footer :deep(button){min-height:42px;white-space:normal}.sync-modal-body :deep(p){overflow-wrap:anywhere}.sync-modal-body :deep(.hint){color:var(--fg-dim);font-size:13px}.sync-modal-body :deep(.error){color:var(--danger);white-space:pre-wrap;overflow-wrap:anywhere}.sync-modal-body :deep(.note){padding:12px 14px;border-radius:12px;background:var(--accent-soft);font-size:13px}.sync-modal-body :deep(.warning){color:var(--warning)}.sync-modal :deep(:focus-visible){outline:2px solid var(--accent);outline-offset:3px}@media(max-width:520px){.sync-modal>header{padding:20px 20px 14px}.sync-modal-body{padding:0 20px 20px}.sync-modal>footer{padding:12px 20px}.sync-modal h2{font-size:21px}}@media(prefers-reduced-motion:reduce){.sync-modal{animation:none}}
</style>
