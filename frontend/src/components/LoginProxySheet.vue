<script setup lang="ts">
// Network & proxy on the login page: where the network stops a sign-in, the route
// has to be changeable before there is an account to open Settings with. Same
// draft, test and apply as Settings · Network & proxy (proxydraft.ts), laid out for a
// sheet: no route diagram, the test result in one line. Mounted only while
// open, so every open starts from what is saved.
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import SelectMenu from "./SelectMenu.vue";
import Toggle from "./Toggle.vue";
import { forgetProxyPassword, loadProxySettings, proxySettings, testProxy } from "../store";
import { PROXY_MODES, PROXY_SCHEMES, useProxyDraft } from "../proxydraft";
import { keychainName } from "../dom";
import { t } from "../i18n";

const emit = defineEmits<{ close: []; applied: [] }>();

const {
  px,
  proxy,
  proxyPassword,
  proxyPasswordShown,
  proxyAdvanced,
  proxyModeButtons,
  proxyInput,
  proxyFieldError,
  proxyIncomplete,
  proxyDirty,
  proxyStatus,
  proxyElapsed,
  chooseProxyMode,
  onProxyModeKey,
  applyProxyDraft,
} = useProxyDraft();

function close() {
  if (!proxySettings.saving) emit("close");
}

async function apply() {
  if (await applyProxyDraft()) emit("applied");
}

// In a short window the fields push the result below the fold: a finished
// test scrolls it into view, where Test connection can be judged.
const testLine = ref<HTMLElement>();
watch(
  () => proxySettings.test,
  async (test) => {
    if (!test) return;
    await nextTick();
    testLine.value?.scrollIntoView({ block: "nearest" });
  },
);

// Esc closes the sheet; an open type list takes it first (SelectMenu stops it).
function onKeydown(ev: KeyboardEvent) {
  if (ev.key !== "Escape" || ev.defaultPrevented) return;
  close();
}

onMounted(async () => {
  window.addEventListener("keydown", onKeydown);
  // A result from an earlier open was about another draft.
  proxySettings.test = null;
  await loadProxySettings();
  await nextTick();
  proxyModeButtons.value.find((b) => b.getAttribute("aria-checked") === "true")?.focus();
});
onBeforeUnmount(() => window.removeEventListener("keydown", onKeydown));
</script>

<template>
  <Teleport to="body">
    <div class="scrim" @click.self="close">
      <section class="sheet glass glass-sheet" role="dialog" aria-modal="true" aria-labelledby="net-sheet-title" aria-describedby="net-sheet-text">
        <header class="head">
          <span class="symbol" aria-hidden="true"><Icon name="globe" :size="22" /></span>
          <span class="kicker">NETWORK · PKP</span>
          <h2 id="net-sheet-title">{{ t("网络与代理") }}</h2>
          <p id="net-sheet-text" class="text">{{ t("选择 PKP 的联网方式，登录和之后的所有请求都按它发出。只影响 PKP 自己，不更改电脑上的 VPN 或系统代理。") }}</p>
        </header>

        <div v-if="!px" class="body loading">
          <Orbit :size="18" :label="t('正在读取')" />
          {{ t("正在读取网络设置…") }}
        </div>
        <div v-else class="body">
          <div class="modes" role="radiogroup" :aria-label="t('连接方式')">
            <button
              v-for="(m, i) in PROXY_MODES"
              :key="m.key"
              ref="proxyModeButtons"
              type="button"
              class="mode"
              role="radio"
              :class="{ on: proxy.mode === m.key }"
              :aria-checked="proxy.mode === m.key"
              :tabindex="proxy.mode === m.key ? 0 : -1"
              :disabled="proxySettings.saving"
              @click="chooseProxyMode(m.key)"
              @keydown="onProxyModeKey($event, i)"
            >
              <strong>{{ m.label }}</strong>
              <small>{{ m.desc }}</small>
            </button>
          </div>

          <template v-if="proxy.mode === 'custom'">
            <label class="field">
              <span>{{ t("类型") }}</span>
              <span class="select glass-well">
                <SelectMenu v-model="proxy.scheme" class="face" :options="PROXY_SCHEMES" :label="t('代理类型')" :disabled="proxySettings.saving" />
                <Icon name="chevronDown" :size="16" />
              </span>
            </label>
            <div class="host-row">
              <label class="field">
                <span>{{ t("服务器地址") }}</span>
                <input
                  v-model="proxy.host"
                  spellcheck="false"
                  autocomplete="off"
                  :placeholder="t('例如 {example}', { example: '127.0.0.1' })"
                  :disabled="proxySettings.saving"
                  :aria-invalid="!!proxyFieldError"
                  :aria-describedby="proxyFieldError ? 'net-sheet-field-error' : undefined"
                />
              </label>
              <label class="field">
                <span>{{ t("端口") }}</span>
                <input
                  v-model="proxy.port"
                  inputmode="numeric"
                  autocomplete="off"
                  placeholder="1–65535"
                  :disabled="proxySettings.saving"
                  :aria-invalid="!!proxyFieldError"
                  :aria-describedby="proxyFieldError ? 'net-sheet-field-error' : undefined"
                />
              </label>
            </div>
            <p v-if="proxyFieldError" id="net-sheet-field-error" class="hint bad" role="alert">{{ proxyFieldError }}</p>

            <div class="toggle-row">
              <span>
                <strong>{{ t("需要账号密码") }}</strong>
                <small>{{ t("仅在代理服务器要求时填写。") }}</small>
              </span>
              <Toggle v-model="proxy.auth" :label="t('需要账号密码')" :disabled="proxySettings.saving" />
            </div>

            <template v-if="proxy.auth">
              <label class="field">
                <span>{{ t("用户名") }}</span>
                <input v-model="proxy.username" spellcheck="false" autocomplete="off" :placeholder="t('代理用户名')" :disabled="proxySettings.saving" />
              </label>
              <div class="field">
                <span id="net-sheet-pw">{{ t("密码") }}</span>
                <template v-if="!px.keychain">
                  <input type="password" disabled aria-labelledby="net-sheet-pw" :placeholder="t('无法保存')" />
                  <p class="hint bad">{{ t("这台电脑没有可用的{keychain}，无法保存代理密码", { keychain: keychainName }) }}</p>
                </template>
                <template v-else>
                  <span class="pw-field">
                    <input
                      v-model="proxyPassword"
                      :type="proxyPasswordShown ? 'text' : 'password'"
                      autocomplete="new-password"
                      spellcheck="false"
                      aria-labelledby="net-sheet-pw"
                      :placeholder="px.passwordHint ? t('沿用已保存的密码（{hint}）', { hint: px.passwordHint }) : t('代理密码')"
                      :disabled="proxySettings.saving"
                    />
                    <button
                      type="button"
                      class="pw-eye"
                      :aria-label="proxyPasswordShown ? t('隐藏密码') : t('显示密码')"
                      @click="proxyPasswordShown = !proxyPasswordShown"
                    >
                      <Icon :name="proxyPasswordShown ? 'eyeOff' : 'eye'" :size="17" />
                    </button>
                  </span>
                  <p class="hint">
                    {{ t("密码保存在本机{keychain}，不写进配置文件。", { keychain: keychainName }) }}
                    <button v-if="px.passwordHint" type="button" class="link danger" :disabled="proxySettings.saving" @click="forgetProxyPassword">
                      {{ t("清除已保存的密码") }}
                    </button>
                  </p>
                </template>
              </div>
            </template>
          </template>

          <template v-else-if="proxy.mode === 'system'">
            <p class="hint">{{ px.system }}</p>
            <p v-if="px.systemPac" class="hint bad icon-note" role="alert">
              <Icon name="alert" :size="15" />
              <span>{{ t("电脑用的是 PAC 自动代理脚本，PKP 不执行脚本，这些请求会直接发出。需要确定的路径时请选「自定义代理」。") }}</span>
            </p>
          </template>

          <p v-else class="hint">{{ t("PKP 不额外指定应用代理。电脑上已有的 VPN 或系统路由仍可能影响实际路径。") }}</p>

          <template v-if="proxy.mode !== 'direct'">
            <button
              type="button"
              class="link advanced"
              :aria-expanded="proxyAdvanced"
              aria-controls="net-sheet-bypass"
              @click="proxyAdvanced = !proxyAdvanced"
            >
              {{ t("高级选项") }}
              <Icon :name="proxyAdvanced ? 'chevronUp' : 'chevronDown'" :size="15" />
            </button>
            <label v-if="proxyAdvanced" id="net-sheet-bypass" class="field">
              <span>{{ t("不使用代理的地址") }}</span>
              <textarea v-model="proxy.bypass" rows="2" spellcheck="false" :placeholder="t('例如 {example}', { example: 'localhost, *.example.com' })" :disabled="proxySettings.saving" />
              <small>{{ t("用逗号、空格或换行分隔；留空表示不设例外。本机地址始终直连。") }}</small>
            </label>
          </template>

          <!-- A failure is read out at once; the rest is polite. -->
          <div ref="testLine" class="test-state" :class="proxyStatus.tone" :role="proxyStatus.tone === 'bad' ? 'alert' : 'status'">
            <Orbit v-if="proxySettings.testing" :size="15" :label="t('正在测试')" />
            <Icon v-else :name="proxyStatus.tone === 'ok' ? 'check' : proxyStatus.tone === 'bad' ? 'alert' : 'info'" :size="15" />
            <span>
              <strong>{{ proxyStatus.title }}</strong>
              {{ proxyStatus.text }}
              <small v-if="proxyElapsed">{{ proxyElapsed }}</small>
            </span>
          </div>
          <p class="hint now">{{ t("当前正在使用：{route}", { route: px.active }) }}</p>
        </div>

        <footer>
          <button
            type="button"
            class="test"
            :class="{ working: proxySettings.testing }"
            :disabled="!px || proxySettings.testing || proxySettings.saving || proxyIncomplete"
            @click="testProxy(proxyInput)"
          >
            <Orbit v-if="proxySettings.testing" :size="16" :label="t('正在测试')" />
            {{ t("测试连接") }}
          </button>
          <button type="button" :disabled="proxySettings.saving" @click="close">{{ t("取消") }}</button>
          <!-- Applying mid-test would leave the finished test talking about a
               draft that is no longer the one on screen. -->
          <button
            type="button"
            class="primary"
            :disabled="!proxyDirty || proxySettings.saving || proxySettings.testing || proxyIncomplete"
            @click="apply"
          >
            {{ proxySettings.saving ? t("正在应用…") : t("应用设置") }}
          </button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
/* A shell that never scrolls itself: the .glass layers are absolute to it,
   so the fields scroll in .body and the header and footer stay put. */
.sheet {
  width: min(480px, calc(100vw - 48px));
  max-height: calc(100vh - 48px);
  gap: 16px;
  padding: 26px 28px 22px;
}

.head {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.symbol {
  width: 44px;
  height: 44px;
  display: grid;
  place-items: center;
  border-radius: 14px;
  background: var(--accent-soft);
  color: var(--accent);
}

.kicker {
  margin-top: 4px;
  color: var(--accent);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.18em;
}

h2 {
  margin: -4px 0 0;
  font-size: 22px;
  font-weight: 650;
  line-height: 30px;
}

.text {
  margin: 0;
  color: var(--fg-dim);
  font-size: 13px;
  line-height: 1.6;
  text-wrap: pretty;
}

.body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-height: 0;
  margin: 0 -6px;
  padding: 2px 6px;
  overflow-y: auto;
}

.body.loading {
  flex-direction: row;
  align-items: center;
  gap: 10px;
  padding: 18px 6px;
  color: var(--fg-dim);
  font-size: 14px;
}

/* Connection method: a radio as a card, three across. */
.modes {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
}

.mode {
  flex-direction: column;
  align-items: flex-start;
  justify-content: flex-start;
  gap: 2px;
  min-height: 62px;
  padding: 10px 12px;
  border-radius: 14px;
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--fg) 3%, transparent);
  color: var(--fg);
  text-align: left;
  white-space: normal;
}

.mode:hover:not(.on):not(:disabled) {
  background: color-mix(in srgb, var(--fg) 6%, transparent);
}

.mode.on {
  border-color: color-mix(in srgb, var(--accent) 70%, transparent);
  background: var(--accent-soft);
}

.mode strong {
  font-size: 14px;
  font-weight: 600;
}

.mode small {
  font-size: 12px;
  line-height: 1.4;
  color: var(--fg-dim);
  text-wrap: pretty;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.field > span {
  font-size: 12px;
  color: var(--fg-dim);
}

.field small {
  font-size: 12px;
  line-height: 1.5;
  color: var(--fg-dim);
}

.field textarea {
  width: 100%;
  min-height: 56px;
  font-size: 14px;
}

.host-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 110px;
  gap: 10px;
}

.host-row input {
  min-width: 0;
}

.select {
  position: relative;
  display: flex;
  align-items: center;
  border-radius: 10px;
}

.face {
  flex: 1;
  min-width: 0;
  min-height: 40px;
  padding: 0 36px 0 13px;
  line-height: 40px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--fg);
  font-size: 14px;
  font-weight: 500;
}

.face[aria-disabled="true"] {
  cursor: default;
  opacity: 0.6;
}

.select :deep(svg) {
  position: absolute;
  right: 12px;
  pointer-events: none;
  color: var(--fg-dim);
}

.toggle-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding-top: 10px;
  border-top: 1px solid var(--line);
}

.toggle-row > span {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.toggle-row strong {
  font-size: 14px;
  font-weight: 600;
}

.toggle-row small {
  font-size: 12px;
  color: var(--fg-dim);
}

.pw-field {
  position: relative;
  display: flex;
}

.pw-field input {
  flex: 1;
  min-width: 0;
  padding-right: 44px;
}

.pw-eye {
  position: absolute;
  right: 2px;
  top: 50%;
  transform: translateY(-50%);
  width: 36px;
  min-height: 34px;
  padding: 0;
  border-color: transparent;
  background: transparent;
  color: var(--fg-dim);
}

.pw-eye:active:not(:disabled) {
  transform: translateY(-50%);
}

.hint {
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--fg-dim);
  text-wrap: pretty;
}

.hint.bad {
  color: var(--danger);
}

.icon-note {
  display: flex;
  align-items: flex-start;
  gap: 7px;
}

.icon-note svg {
  flex: none;
  margin-top: 2px;
}

/* A link in the sentence, not a pill: the global button chrome is undone. */
.link {
  min-height: 0;
  padding: 2px 0;
  gap: 4px;
  border: none;
  background: none;
  color: var(--accent);
  font-size: 13px;
  font-weight: 600;
}

.link:hover:not(:disabled) {
  background: none;
  text-decoration: underline;
}

.advanced {
  align-self: flex-start;
  min-height: 32px;
}

.test-state {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 10px 12px;
  border-radius: 12px;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 13px;
  line-height: 1.5;
}

.test-state svg,
.test-state .orbit {
  flex: none;
  margin-top: 2px;
}

.test-state strong {
  margin-right: 4px;
  font-weight: 600;
}

.test-state small {
  display: block;
  opacity: 0.8;
}

.test-state.ok {
  background: color-mix(in srgb, var(--success) 14%, transparent);
  color: var(--success);
}

.test-state.bad {
  background: color-mix(in srgb, var(--danger) 14%, transparent);
  color: var(--danger);
}

.now {
  margin-top: -4px;
  overflow-wrap: anywhere;
}

footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding-top: 14px;
  border-top: 1px solid var(--line);
}

footer button {
  min-width: 88px;
  min-height: 40px;
  padding: 0 16px;
  font-size: 14px;
  font-weight: 600;
}

footer .test {
  margin-right: auto;
}

/* A short window keeps its height for the fields. */
@media (max-height: 700px) {
  .symbol {
    display: none;
  }
}
</style>
