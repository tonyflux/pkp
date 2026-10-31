<script setup lang="ts">
// In-Place Setup: the local result is already there; connecting a
// model is offered where it is needed instead of sending the user to Settings.
// Preview Local Plan sends nothing; Connect and Continue saves the service and key, tests
// them, and only then carries on with the held request (which still asks
// for consent before anything goes out).
import { computed, ref, watch } from "vue";
import { Browser } from "@wailsio/runtime";
import AssistantSheet from "./AssistantSheet.vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import SelectMenu, { type SelectOption } from "./SelectMenu.vue";
import { AI_PRESETS, CUSTOM_PRESET_ID, presetOf } from "../aipresets";
import { keychainName } from "../dom";
import { t } from "../i18n";
import { aiSettings, assistant, closeConnect, connectLocal, connectModel } from "../store";

const PRESET_OPTIONS: SelectOption<string>[] = AI_PRESETS.map((p) => ({ value: p.id, label: p.label }));

const sheet = computed(() => assistant.connect);

const presetId = ref(AI_PRESETS[0].id);
const key = ref("");
const baseUrl = ref("");
const model = ref("");

const preset = computed(() => AI_PRESETS.find((p) => p.id === presetId.value) ?? AI_PRESETS[0]);
const custom = computed(() => preset.value.id === CUSTOM_PRESET_ID);
// A key already in the keychain for this very service may stay as it is.
const keyKept = computed(() => {
  const s = aiSettings.value;
  return !!s?.keyHint && presetOf(s).id === presetId.value && (!custom.value || (s.baseUrl === baseUrl.value.trim() && s.model === model.value.trim()));
});
const noKeychain = computed(() => !preset.value.keyless && aiSettings.value?.keychain === false);

// Each sheet starts from the saved connection, else from the first preset.
watch(
  () => !!sheet.value,
  (open) => {
    if (!open) return;
    const s = aiSettings.value;
    const from = s && (s.model || s.baseUrl) ? presetOf(s) : AI_PRESETS[0];
    presetId.value = from.id;
    baseUrl.value = from.id === CUSTOM_PRESET_ID ? (s?.baseUrl ?? "") : from.baseUrl;
    model.value = from.id === CUSTOM_PRESET_ID ? (s?.model ?? "") : from.model;
    key.value = "";
  },
  { immediate: true },
);

watch(presetId, (id, was) => {
  if (id === was) return;
  const p = AI_PRESETS.find((x) => x.id === id);
  if (!p || p.id === CUSTOM_PRESET_ID) return;
  baseUrl.value = p.baseUrl;
  model.value = p.model;
});

const canConnect = computed(() => {
  if (!sheet.value || sheet.value.testing || noKeychain.value) return false;
  if (custom.value && (!baseUrl.value.trim() || !model.value.trim())) return false;
  return !!preset.value.keyless || !!key.value.trim() || keyKept.value;
});

const localLabel = computed(() => (sheet.value?.organizable ? t("先看本地计划") : t("先看本地结果")));

async function connect() {
  if (!canConnect.value) return;
  const ok = await connectModel(presetId.value, key.value.trim(), custom.value ? baseUrl.value.trim() : undefined, custom.value ? model.value.trim() : undefined);
  if (ok) key.value = "";
}

// Enter in a field is Connect and Continue, as a form would; the select keeps its own Enter.
function onEnter(event: KeyboardEvent) {
  if (!(event.target instanceof HTMLInputElement)) return;
  event.preventDefault();
  void connect();
}

function openKeyPage() {
  if (preset.value.keyUrl) void Browser.OpenURL(preset.value.keyUrl).catch(() => undefined);
}
</script>

<template>
  <AssistantSheet v-if="sheet" labelledby="ai-connect-title" @close="closeConnect">
    <span class="sheet-eyebrow">{{ t("需要模型时再连接") }}</span>
    <h2 id="ai-connect-title">{{ sheet.hasLocal ? t("本地建议已经准备好。") : t("这一步需要连接模型。") }}</h2>
    <p class="sheet-lede">
      {{ sheet.hasLocal ? t("尚未连接你的模型。整理计划和本地搜索仍可用；若要继续问 AI，可以在这里配置。") : t("尚未连接你的模型。本地搜索与规则整理不需要模型；若要继续问 AI，可以在这里配置。") }}
    </p>
    <div class="steps">
      <span v-if="sheet.found !== null"><Icon name="check" :size="14" /> {{ t("本地索引：{n} 项已找到", { n: sheet.found }) }}</span>
      <span v-if="sheet.organizable !== null"><Icon name="check" :size="14" /> {{ t("规则：{n} 项可整理", { n: sheet.organizable }) }}</span>
      <span class="model-state">
        <template v-if="sheet.testing"><Orbit :size="13" /> {{ t("模型：正在测试连接…") }}</template>
        <template v-else>{{ t("模型：未连接") }}</template>
      </span>
    </div>
    <div class="config" @keydown.enter="onEnter">
      <div class="pick">
        {{ t("选择提供商") }}
        <span class="select">
          <SelectMenu v-model="presetId" class="face" :options="PRESET_OPTIONS" :label="t('选择提供商')" :disabled="sheet.testing" />
          <Icon name="chevronDown" :size="14" />
        </span>
      </div>
      <label v-if="!preset.keyless">
        API Key
        <input
          v-model="key"
          type="password"
          autocomplete="off"
          spellcheck="false"
          :placeholder="keyKept ? t('已保存 {hint}，留空沿用', { hint: aiSettings.value?.keyHint ?? '' }) : t('保存到本机{keychain}', { keychain: keychainName })"
          :disabled="sheet.testing"
          data-autofocus
        />
      </label>
      <p v-else class="keyless">{{ t("本机服务，不需要 API Key。") }}</p>
      <template v-if="custom">
        <label>
          Base URL
          <input v-model="baseUrl" type="url" autocomplete="off" spellcheck="false" placeholder="https://…/v1" :disabled="sheet.testing" />
        </label>
        <label>
          {{ t("模型名") }}
          <input v-model="model" type="text" autocomplete="off" spellcheck="false" :placeholder="t('例如 qwen-plus')" :disabled="sheet.testing" />
        </label>
      </template>
    </div>
    <p v-if="noKeychain" class="sheet-error">{{ t("这台电脑没有可用的{keychain}，无法保存 API Key。", { keychain: keychainName }) }}</p>
    <p v-else-if="sheet.error" class="sheet-error" role="alert">{{ sheet.error }}</p>
    <div class="sheet-actions">
      <button v-if="preset.keyUrl && !preset.keyless" class="sheet-btn quiet key-link" type="button" @click="openKeyPage()">{{ t("如何获取 Key") }}</button>
      <span class="buttons">
        <button v-if="sheet.hasLocal" class="sheet-btn" type="button" :disabled="sheet.testing" @click="connectLocal()">{{ localLabel }}</button>
        <button class="sheet-btn primary" :class="{ working: sheet.testing }" type="button" :disabled="!canConnect" @click="connect">
          <template v-if="sheet.testing"><Orbit :size="14" /> {{ t("正在连接…") }}</template>
          <template v-else>{{ t("连接并继续") }} <Icon name="arrowRight" :size="15" /></template>
        </button>
      </span>
    </div>
    <p class="sheet-small">{{ t("密钥只保存在本机{keychain}。连接后发送任何文件信息前，仍会先问你。", { keychain: keychainName }) }}</p>
  </AssistantSheet>
</template>

<style scoped>
.steps {
  display: flex;
  flex-direction: column;
  gap: 9px;
  margin: 22px 0 8px;
  padding: 15px;
  border: 1px solid var(--line);
  border-radius: 12px;
  font-size: 12px;
}

.steps > span {
  display: flex;
  align-items: center;
  gap: 9px;
}

.steps svg {
  flex: none;
  color: var(--success);
}

.steps .model-state {
  color: var(--fg-dim);
}

.config {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 10px;
  margin-top: 14px;
}

.config label,
.config .pick {
  min-width: 0;
  font-size: 11px;
  color: var(--fg-dim);
}

.config input,
.config .face {
  display: block;
  width: 100%;
  height: 37px;
  margin-top: 5px;
  padding: 0 9px;
  border: 1px solid var(--line-strong);
  border-radius: 9px;
  background: color-mix(in srgb, var(--canvas) 86%, transparent);
  font-size: 12px;
  color: var(--fg);
}

.config input:focus,
.config .face:focus,
.config .face.open {
  border-color: var(--accent);
}

.select {
  position: relative;
  display: block;
}

.config .face {
  padding-right: 30px;
  line-height: 35px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.config .face[aria-disabled="true"] {
  opacity: 0.6;
}

.select > svg {
  position: absolute;
  top: calc(50% + 2.5px);
  right: 10px;
  transform: translateY(-50%);
  pointer-events: none;
  color: var(--fg-dim);
}

.config input::placeholder {
  font-size: 11px;
}

.keyless {
  align-self: end;
  margin: 0;
  padding-bottom: 10px;
  font-size: 11px;
  color: var(--fg-dim);
}

.key-link {
  margin-right: auto;
  color: var(--accent);
}

/* The two buttons wrap under the link together, never one of them alone. */
.buttons {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 10px;
  margin-left: auto;
}

@media (max-width: 700px) {
  .config {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
