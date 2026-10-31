<script setup lang="ts">
// AI Assistant: the canvas frames four screens — home, a
// conversation, the full plan, a receipt — and the composer pinned under
// home and chat. The Authorization / In-place Configuration / Final Confirmation sheets come up over it.
// The store does the work (store.ts 「AI Assistant」); nothing here sends or
// changes anything without one of those sheets or an explicit click.
import { computed, onMounted } from "vue";
import AssistantChat from "./AssistantChat.vue";
import AssistantComposer from "./AssistantComposer.vue";
import AssistantConfirmSheet from "./AssistantConfirmSheet.vue";
import AssistantConnectSheet from "./AssistantConnectSheet.vue";
import AssistantConsentSheet from "./AssistantConsentSheet.vue";
import AssistantHome from "./AssistantHome.vue";
import AssistantPlan from "./AssistantPlan.vue";
import AssistantReceipt from "./AssistantReceipt.vue";
import Orbit from "./Orbit.vue";
import { t } from "../i18n";
import { assistant, cancelAssistantTask, loadAISettings } from "../store";

const screen = computed(() => assistant.screen);
const composing = computed(() => screen.value === "home" || screen.value === "chat");

// The step in flight, or the last word on what happened; a running turn
// speaks for itself in the chat, so its step line is not repeated here.
const status = computed(() => {
  if (assistant.busy && !assistant.live) return { text: assistant.busy, tone: "busy" };
  if (assistant.notice) return { text: assistant.notice, tone: assistant.noticeTone };
  return null;
});
const cancellable = computed(() => !!assistant.busy && !assistant.running && !assistant.live);

// Conversations, Recent Tasks and arrivals follow the account through the
// store's own watcher; the model state is read fresh for the toolbar pill.
onMounted(() => void loadAISettings());
</script>

<template>
  <section class="assistant canvas" :class="`on-${screen}`" :aria-busy="!!assistant.busy || !!assistant.live">
    <p v-if="status && !composing" class="ai-status top" :class="status.tone" role="status">
      <Orbit v-if="status.tone === 'busy'" :size="14" />
      <span>{{ status.text }}</span>
      <button v-if="cancellable" class="quiet" type="button" @click="cancelAssistantTask()">{{ t("取消") }}</button>
    </p>

    <AssistantHome v-if="screen === 'home'" />
    <AssistantChat v-else-if="screen === 'chat'" />
    <AssistantPlan v-else-if="screen === 'plan'" />
    <AssistantReceipt v-else />

    <p v-if="status && composing" class="ai-status" :class="status.tone" role="status">
      <Orbit v-if="status.tone === 'busy'" :size="14" />
      <span>{{ status.text }}</span>
      <button v-if="cancellable" class="quiet" type="button" @click="cancelAssistantTask()">{{ t("取消") }}</button>
    </p>
    <AssistantComposer v-if="composing" />

    <AssistantConsentSheet />
    <AssistantConnectSheet />
    <AssistantConfirmSheet />
  </section>
</template>

<style scoped>
/* The canvas is a column: the screen's own scroll region takes what is
   left after the composer (or the plan's dock), so neither ever covers
   the content. Each screen sizes its reading column itself. */
.assistant {
  container: ai / inline-size;
  position: relative;
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* Only reading content opts in; the canvas and its controls stay app-like. */
.ai-status > span,
.assistant :deep(.ending) {
  -webkit-user-select: text;
  user-select: text;
  --default-contextmenu: show;
}

.assistant :deep(button),
.assistant :deep(svg),
.assistant :deep(.avatar),
.assistant :deep(.mark),
.assistant :deep(.trace),
.assistant :deep(.caret) {
  -webkit-user-select: none;
  user-select: none;
  --default-contextmenu: auto;
}

.ai-status {
  flex: none;
  align-self: center;
  display: flex;
  align-items: center;
  gap: 8px;
  width: min(960px, calc(100% - 88px));
  min-height: 30px;
  margin: 6px auto -2px;
  padding: 0 6px;
  font-size: 12px;
  color: var(--fg-dim);
  overflow-wrap: anywhere;
}

.ai-status.top {
  width: min(940px, calc(100% - 76px));
  margin: 10px auto 0;
}

.ai-status.success {
  color: var(--success);
}

.ai-status.warning {
  color: var(--warning);
}

.ai-status.error {
  color: var(--danger);
}

.ai-status span {
  min-width: 0;
}

.ai-status button {
  flex: none;
  min-height: 26px;
  margin: 0 -2px;
  padding: 2px 10px;
  font-size: 12px;
  color: var(--accent);
}

@media (max-width: 959px) {
  .ai-status,
  .ai-status.top {
    width: calc(100% - 48px);
  }
}

/* A small window scrolls the canvas as one page: the screen's region, the
   composer and the plan's dock follow each other, so the dock's button and
   the composer are always reachable by scrolling at widths ≤550px. */
@media (max-width: 550px) {
  .assistant {
    overflow-y: auto;
    overscroll-behavior: contain;
  }

  .ai-status,
  .ai-status.top {
    width: calc(100% - 26px);
  }
}
</style>
