<script setup lang="ts">
// Per-turn authorization: what this turn would send to the model and what
// it never sends, asked once per turn. Do not send (or Esc, or ×) keeps the local
// result and sends nothing; the box makes the account's L1 Always allow — the
// subtitle level is set apart and stays as it is.
import { ref, watch } from "vue";
import AssistantSheet from "./AssistantSheet.vue";
import Icon from "./Icon.vue";
import { formatTokens } from "../aiprompt";
import { t } from "../i18n";
import { answerConsent, assistant } from "../store";

const remember = ref(false);
// Every sheet starts unticked: Always allow is a choice made on purpose, never carried over.
watch(
  () => assistant.consent,
  () => (remember.value = false),
);

function deny() {
  answerConsent(false, false);
}

function allow() {
  answerConsent(true, remember.value);
}
</script>

<template>
  <AssistantSheet v-if="assistant.consent" labelledby="ai-consent-title" @close="deny">
    <span class="sheet-eyebrow">{{ t("{account} / 元数据", { account: assistant.consent.accountName }) }}</span>
    <h2 id="ai-consent-title">{{ assistant.consent.title }}</h2>
    <p class="sheet-lede">{{ assistant.consent.lede }}</p>
    <div class="disclosure">
      <div>
        <span class="positive" aria-hidden="true"><Icon name="check" :size="15" /></span>
        <div>
          <strong>{{ t("会发送") }}</strong>
          <p>{{ assistant.consent.sends }}</p>
        </div>
      </div>
      <div>
        <span aria-hidden="true">—</span>
        <div>
          <strong>{{ t("不会发送") }}</strong>
          <p>{{ assistant.consent.notSends }}</p>
        </div>
      </div>
    </div>
    <label class="remember">
      <input v-model="remember" type="checkbox" />
      {{ t("以后{who}的元数据始终允许", { who: assistant.consent.accountName === t("原账号") ? assistant.consent.accountName : t("「{name}」", { name: assistant.consent.accountName }) }) }}
    </label>
    <p class="sheet-small">
      {{ t("字幕内容单独设置，不受此项影响 · 本次预计 {n} tokens", { n: formatTokens(assistant.consent.estimate) }) }}
      <template v-if="assistant.consent.limit"> · {{ t("本对话上限 {n}", { n: formatTokens(assistant.consent.limit) }) }}</template>
    </p>
    <p v-if="assistant.consent.to" class="sheet-small to">{{ assistant.consent.to }}</p>
    <div class="sheet-actions">
      <button class="sheet-btn" type="button" @click="deny">{{ t("不发送") }}</button>
      <button class="sheet-btn primary" type="button" data-autofocus @click="allow">
        {{ remember ? t("发送并始终允许") : t("发送一次") }}
        <Icon name="arrowRight" :size="15" />
      </button>
    </div>
  </AssistantSheet>
</template>

<style scoped>
.disclosure {
  margin: 19px 0 17px;
  padding: 8px 0;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
}

.disclosure > div {
  display: flex;
  gap: 14px;
  margin: 12px 0;
}

.disclosure > div > span {
  flex: none;
  width: 15px;
  color: var(--fg-dim);
}

.disclosure > div > span.positive {
  color: var(--success);
}

.disclosure > div > div {
  min-width: 0;
}

.disclosure strong {
  font-size: 12px;
}

.disclosure p {
  margin: 3px 0 0;
  font-size: 11px;
  line-height: 1.65;
  color: var(--fg-dim);
  overflow-wrap: anywhere;
}

.remember {
  display: flex;
  align-items: center;
  gap: 9px;
  font-size: 12px;
  cursor: pointer;
}

.remember input {
  flex: none;
  width: 16px;
  height: 16px;
  margin: 0;
  padding: 0;
  accent-color: var(--accent);
}

.to {
  margin-top: 4px;
}
</style>
