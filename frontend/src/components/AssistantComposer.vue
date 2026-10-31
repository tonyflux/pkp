<script setup lang="ts">
// The composer: one sentence to the assistant, pinned under
// the home and chat columns so it never covers them. It is a form: Enter
// sends. While a turn, a step or a run is in flight it does not send; the
// live turn's Stop sits beside it then.
import { computed } from "vue";
import Icon from "./Icon.vue";
import SelectMenu, { type SelectOption } from "./SelectMenu.vue";
import { t } from "../i18n";
import { accountName, assistant, assistantScopeLabel, cancelAssistantTask, currentId, drive, pickedEntries, setAssistantScope, submitComposer } from "../store";

const busy = computed(() => !!assistant.busy || !!assistant.live || assistant.running || !!assistant.consent || !!assistant.connect);
const canSend = computed(() => !busy.value && !!assistant.prompt.trim());
const account = computed(() => accountName(assistant.account || currentId.value));
const scopeKind = computed(() => assistant.scope.entries ? "selection" : assistant.scope.folder?.length ? "folder" : "drive");
const scopeOptions = computed<SelectOption<"drive" | "folder" | "selection">[]>(() => [
  { value: "drive", label: t("整个云盘") },
  { value: "folder", label: t("当前文件夹：{name}", { name: drive.trail.map((c) => c.name).join(" / ") || t("全部文件") }), disabled: !drive.trail.length },
  { value: "selection", label: t("文件页已选 {n} 项", { n: pickedEntries.value.length }), disabled: !pickedEntries.value.length },
]);

const placeholder = computed(() =>
  assistant.screen === "chat" && assistant.conversation?.turns.length
    ? t("继续说，例如：特别篇先保持原样")
    : t("找上周云添加的纪录片，或说说想怎么整理…"),
);

function submit() {
  if (canSend.value) void submitComposer();
}
</script>

<template>
  <form class="ai-composer" :aria-busy="busy" @submit.prevent="submit">
    <input
      id="ai-prompt"
      v-model="assistant.prompt"
      type="text"
      autocomplete="off"
      spellcheck="false"
      :placeholder="placeholder"
      :aria-label="t('描述要找的文件或整理方式')"
      :disabled="assistant.running"
    />
    <div class="tools">
      <SelectMenu :model-value="scopeKind" :options="scopeOptions" :label="t('助手范围')" :disabled="busy" @update:model-value="setAssistantScope">
        <span class="scope" :title="`${account} · ${assistantScopeLabel(assistant.scope)}`"><Icon name="folder" :size="14" /><span>{{ account }} · {{ assistantScopeLabel(assistant.scope) }}</span></span>
      </SelectMenu>
      <span class="hint">{{ t("输入内容仅在你同意后发给模型") }}</span>
      <button v-if="assistant.live" class="stop" type="button" @click="cancelAssistantTask()">
        <Icon name="stop" :size="14" />
        {{ t("停止") }}
      </button>
      <button class="send" type="submit" :aria-label="t('发送')" :title="t('发送')" :disabled="!canSend">
        <Icon name="arrowRight" :size="17" />
      </button>
    </div>
  </form>
</template>

<style scoped>
/* A lens over the canvas foot, 960 wide at most; it takes its own row of
   the page, so the scroll region above ends where it starts. */
.ai-composer {
  flex: none;
  align-self: center;
  display: flex;
  flex-direction: column;
  gap: 10px;
  width: min(960px, calc(100% - 88px));
  min-height: 90px;
  margin: 9px auto 23px;
  padding: 13px 15px 9px;
  border: 1px solid var(--line-strong);
  border-radius: 20px;
  background: linear-gradient(165deg, color-mix(in srgb, var(--accent) 12%, transparent), transparent 70%), var(--glass-sheet);
  box-shadow: var(--shadow-island), inset 0 1px 0 color-mix(in srgb, var(--glass-rim) 40%, transparent);
  transition: border-color var(--dur) var(--ease);
}

.ai-composer:focus-within {
  border-color: color-mix(in srgb, var(--accent) 60%, transparent);
}

.ai-composer > input {
  height: 24px;
  min-width: 0;
  padding: 0 6px;
  border: 0;
  background: none;
  font-size: 14px;
}

.ai-composer > input:focus {
  background: none;
}

.tools {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 32px;
  min-width: 0;
}

.tools :deep(.select-menu) { min-width: 0; max-width: 55%; padding: 4px 8px; font-size: 11px; }

.scope {
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
  font-size: 11px;
  color: var(--fg-dim);
}

.scope svg {
  flex: none;
}

.scope span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.hint {
  flex: none;
  margin-left: auto;
  font-size: 11px;
  color: var(--fg-dim);
  white-space: nowrap;
}

.stop {
  flex: none;
  min-height: 33px;
  padding: 0 12px;
  border-radius: 11px;
  font-size: 12px;
}

.send {
  flex: none;
  width: 33px;
  height: 33px;
  min-height: 33px;
  padding: 0;
  border: 0;
  border-radius: 11px;
  background: var(--accent);
  color: var(--on-accent);
}

.send:hover:not(:disabled) {
  border: 0;
  background: var(--accent);
  filter: brightness(1.08);
}

.send svg {
  transform: rotate(-45deg);
}

@media (max-width: 959px) {
  .ai-composer {
    width: calc(100% - 48px);
  }
}

@media (max-width: 550px) {
  .ai-composer {
    width: calc(100% - 26px);
    margin: 12px auto 16px;
  }

  .hint {
    display: none;
  }

  .stop {
    margin-left: auto;
  }

  .send {
    margin-left: auto;
  }

  .stop + .send {
    margin-left: 0;
  }
}
</style>
