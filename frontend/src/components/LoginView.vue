<script setup lang="ts">
// The sign-in form. Remember Account and Password keeps the account and password on this
// computer (the password in the macOS keychain or the Windows Credential
// Manager; the page only learns that
// one is there), and Auto Sign-In lets the app use them by itself whenever it
// would otherwise stop here. Both start ticked; afterwards the form shows
// what the last sign-in chose. Builds without a keychain offer neither.
// The first account on this computer; more come from Add Account.
// Network & Proxy opens here too (LoginProxySheet): without a route out there is
// no signing in, so the setting cannot wait for Settings.
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref } from "vue";
import Icon from "./Icon.vue";
import LoginProxySheet from "./LoginProxySheet.vue";
import Logo from "./Logo.vue";
import Orbit from "./Orbit.vue";
import QRSignIn from "./QRSignIn.vue";
import { completeQRSignIn, cancelSignIn } from "../store";
import { keychainName } from "../dom";
import { drive, loadProxySettings, openSettings, openSignUp, proxySettings, submitSignIn, ui } from "../store";
import { PROXY_MODES } from "../proxydraft";
import { DriveService, type SavedSignIn } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import { t } from "../i18n";

const form = reactive({ account: drive.status?.account ?? "", password: "", remember: true, autoSignIn: true });
const saved = ref<SavedSignIn>({ supported: false, account: "", hasPassword: false, remember: false, autoSignIn: false });
const pending = ref(false);
// Why the last attempt was turned down, in the words the backend chose.
const error = ref("");
// The server could not be reached: the notice offers Network & Proxy.
const unreachable = ref(false);
const passwordField = ref<HTMLInputElement>();
const passwordShown = ref(false);
const netOpen = ref(false);
const netButton = ref<HTMLButtonElement>();
const mode = ref<"qr" | "password">("qr");
let signinSerial = 0;
let mounted = true;
const changingMode = ref(false);
async function setMode(value: "qr" | "password") {
  if (value === mode.value || changingMode.value) return;
  changingMode.value = true;
  signinSerial++;
  if (pending.value) await cancelSignIn();
  pending.value = false;
  changingMode.value = false;
  if (!mounted) return;
  mode.value = value;
  passwordShown.value = false;
  error.value = "";
  void nextTick(() => document.getElementById(value === "qr" ? "login-qr-tab" : "login-password-tab")?.focus());
}
onBeforeUnmount(() => { mounted = false; signinSerial++; });

// The route in force, in the words of Connection Method; a custom one names its server.
const netLabel = computed(() => {
  const s = proxySettings.value;
  if (!s) return "";
  if (s.mode === "custom") return `${s.scheme.toUpperCase()} ${s.host}:${s.port}`;
  return PROXY_MODES.find((m) => m.key === s.mode)?.label ?? "";
});

// The remembered password stands in for an empty field, for its own account only.
const usesSaved = computed(() => saved.value.hasPassword && form.account.trim() === saved.value.account);
const canSubmit = computed(() => form.account.trim().length > 0 && (form.password.length > 0 || usesSaved.value));

onMounted(async () => {
  void loadProxySettings();
  const s = await DriveService.SavedSignIn("").catch(() => null);
  if (!s) return;
  saved.value = s;
  // Whatever the session record named gives way to the remembered account,
  // unless the user has typed one meanwhile.
  if (s.account && (!form.account || form.account === drive.status?.account)) form.account = s.account;
  form.remember = s.remember;
  form.autoSignIn = s.autoSignIn;
});

function toggleRemember() {
  form.remember = !form.remember;
  if (!form.remember) form.autoSignIn = false;
}

function toggleAutoSignIn() {
  form.autoSignIn = !form.autoSignIn;
  if (form.autoSignIn) form.remember = true;
}

async function submit() {
  if (!canSubmit.value || pending.value || changingMode.value || mode.value !== "password") return;
  const serial = ++signinSerial;
  pending.value = true;
  passwordShown.value = false;
  error.value = "";
  unreachable.value = false;
  const res = await submitSignIn({
    login: form.account.trim(),
    password: form.password,
    label: "",
    account: "",
    remember: form.remember,
    autoSignIn: form.autoSignIn,
  });
  if (!mounted || serial !== signinSerial) return;
  pending.value = false;
  form.password = "";
  // Success puts the account on screen and this page goes away.
  if (!res.failure || res.failure === "cancelled") return;
  error.value = res.message || t("登录失败，请稍后再试");
  unreachable.value = res.failure === "network";
  await nextTick();
  passwordField.value?.focus();
}

async function closeNet() {
  netOpen.value = false;
  await nextTick();
  netButton.value?.focus();
}

// The new route is in force: the old failure no longer says anything, and
// the store's notice names the route instead.
function netApplied() {
  error.value = "";
  unreachable.value = false;
  void closeNet();
}
</script>

<template>
  <div class="login-wrap">
    <section class="login glass glass-sheet">
      <header style="--wails-draggable: drag">
        <Logo :size="48" />
        <div>
          <h1>PKP</h1>
          <p>for PikPak</p>
        </div>
        <button
          ref="netButton"
          type="button"
          class="net-button"
          :class="{ on: proxySettings.value?.mode === 'custom' }"
          style="--wails-draggable: no-drag"
          :aria-label="t('网络与代理')"
          :title="t('网络与代理：选择 PKP 的联网方式')"
          @click="netOpen = true"
        >
          <Icon name="globe" :size="19" />
        </button>
      </header>

      <div class="signin-tabs" role="tablist" :aria-label="t('登录方式')" @keydown.left.prevent="setMode(mode === 'qr' ? 'password' : 'qr')" @keydown.right.prevent="setMode(mode === 'qr' ? 'password' : 'qr')">
        <button id="login-qr-tab" type="button" role="tab" :aria-selected="mode === 'qr'" aria-controls="login-qr-panel" :tabindex="mode === 'qr' ? 0 : -1" @click="setMode('qr')">{{ t("扫码登录") }}</button>
        <button id="login-password-tab" type="button" role="tab" :aria-selected="mode === 'password'" aria-controls="login-password-panel" :tabindex="mode === 'password' ? 0 : -1" @click="setMode('password')">{{ t("密码登录") }}</button>
      </div>
      <div v-if="mode === 'qr'" id="login-qr-panel" role="tabpanel" aria-labelledby="login-qr-tab">
        <QRSignIn network-link @complete="completeQRSignIn" @network="netOpen = true" />
      </div>
      <div v-else id="login-password-panel" class="password-panel" role="tabpanel" aria-labelledby="login-password-tab">
      <p v-if="error" class="notice" role="alert">
        {{ error }}
        <button v-if="unreachable" type="button" class="notice-link" @click="netOpen = true">{{ t("检查网络与代理") }}</button>
      </p>
      <p v-else-if="ui.message" class="notice" :class="ui.messageTone">{{ ui.message }}</p>

      <label class="field">
        <span>{{ t("账号（邮箱或手机号）") }}</span>
        <input v-model="form.account" autocomplete="username" spellcheck="false" placeholder="you@example.com" @keyup.enter="submit" />
      </label>
      <div class="field">
        <label for="login-password">{{ t("密码") }}</label>
        <div class="pw-field">
          <input
            id="login-password"
            ref="passwordField"
            v-model="form.password"
            :type="passwordShown ? 'text' : 'password'"
            autocomplete="current-password"
            autocapitalize="off"
            spellcheck="false"
            :placeholder="usesSaved ? t('已记住密码，可直接登录') : ''"
            @keyup.enter="submit"
          />
          <button
            type="button"
            class="pw-eye"
            :aria-label="passwordShown ? t('隐藏密码') : t('显示密码')"
            :aria-pressed="passwordShown"
            @mousedown.prevent
            @click="passwordShown = !passwordShown"
          >
            <Icon :name="passwordShown ? 'eyeOff' : 'eye'" :size="18" />
          </button>
        </div>
      </div>

      <div v-if="saved.supported" class="choices">
        <button
          type="button"
          class="choice"
          :class="{ on: form.remember }"
          role="checkbox"
          :aria-checked="form.remember"
          :title="t('账号存在这台电脑上，密码存进系统{keychain}', { keychain: keychainName })"
          @click="toggleRemember"
        >
          <i class="box"><Icon v-if="form.remember" name="check" :size="12" /></i>
          {{ t("记住账号密码") }}
        </button>
        <button
          type="button"
          class="choice"
          :class="{ on: form.autoSignIn }"
          role="checkbox"
          :aria-checked="form.autoSignIn"
          :title="t('登录失效时用记住的密码自动登录，不再停在这一页')"
          @click="toggleAutoSignIn"
        >
          <i class="box"><Icon v-if="form.autoSignIn" name="check" :size="12" /></i>
          {{ t("自动登录") }}
        </button>
      </div>

      <button class="primary submit" :class="{ working: pending }" :disabled="!canSubmit || pending" @click="submit">
        <Orbit v-if="pending" :size="16" :label="t('登录中')" />
        {{ pending ? t("登录中…") : t("登录") }}
      </button>
      </div>

      <!-- PKP has no sign-up form: PikPak's own page opens in the browser. -->
      <p class="signup">
        {{ t("还没有 PikPak 账号？") }}
        <button type="button" class="signup-link" :title="t('在浏览器中打开 PikPak 注册页')" @click="openSignUp">
          {{ t("注册") }}<Icon name="arrowRight" :size="14" />
        </button>
      </p>

      <!-- The route in force, said where it is read; changing it is the globe above. -->
      <p v-if="netLabel" class="net">
        <Icon name="globe" :size="14" />
        {{ t("网络与代理") }} · {{ netLabel }}
      </p>
      <button class="quiet" type="button" @click="openSettings('updates')">{{ t("关于与更新") }}</button>
    </section>

    <LoginProxySheet v-if="netOpen" @close="closeNet" @applied="netApplied" />
  </div>
</template>

<style scoped>
.signin-tabs { display: flex; padding: 3px; border-radius: 10px; background: var(--accent-soft); margin-bottom: 5px; }
.signin-tabs button { flex: 1; padding: 9px 8px; border: 0; border-radius: 8px; background: transparent; color: var(--fg-dim); font-size: 13px; }
.signin-tabs button[aria-selected="true"] { background: var(--primary); color: var(--on-primary); box-shadow: inset 0 1px 0 var(--primary-rim), 0 2px 7px var(--primary-glow); }
.password-panel { display: flex; flex-direction: column; gap: 14px; }
.login-wrap {
  position: relative;
  z-index: 1;
  height: 100%;
  overflow-y: auto;
  display: grid;
  place-items: center;
  align-items: safe center;
  padding: clamp(16px, 5vw, 40px);
}

.login {
  width: 400px;
  max-width: 100%;
  padding: 26px;
  border-radius: var(--radius-sheet);
  display: flex;
  flex-direction: column;
  gap: 14px;
}

header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding-bottom: 4px;
}

h1 {
  margin: 0;
  font-size: 21px;
  line-height: 1.2;
}

header p {
  margin: 0;
  font-size: 12px;
  color: var(--fg-dim);
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.field > span,
.field > label {
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

.choices {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 18px;
  margin-top: -2px;
}

/* A box and its words, not a pill: the global button chrome is undone. */
.choice {
  min-height: 0;
  padding: 2px 0;
  gap: 8px;
  border: none;
  border-radius: 6px;
  background: none;
  color: var(--fg-dim);
  font-size: 13px;
  font-weight: 500;
}

.choice:hover:not(:disabled) {
  background: none;
  color: var(--fg);
}

.choice.on {
  color: var(--fg);
}

.box {
  display: grid;
  place-items: center;
  width: 16px;
  height: 16px;
  border-radius: 5px;
  border: 1.5px solid var(--line-strong);
  color: var(--on-primary);
  transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease);
}

.choice.on .box {
  border-color: transparent;
  background: var(--primary) border-box;
}

.submit {
  min-height: 44px;
  font-size: 15px;
}

.signup {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: 4px;
  margin: -2px 0 0;
  font-size: 13px;
  color: var(--fg-dim);
}

/* A link in the sentence, not a pill: the global button chrome is undone. */
.signup-link {
  min-height: 0;
  padding: 2px 4px;
  gap: 3px;
  border: none;
  border-radius: 6px;
  background: none;
  color: var(--accent);
  font-size: 13px;
  font-weight: 600;
}

.signup-link:hover:not(:disabled) {
  background: var(--accent-soft);
  color: var(--accent);
}

/* Network & Proxy in the header's corner: a round glass button, tinted while a
   proxy of PKP's own is in force. */
.net-button {
  width: 38px;
  height: 38px;
  min-height: 38px;
  margin-left: auto;
  padding: 0;
  border-radius: 50%;
  color: var(--fg-dim);
}

.net-button:hover:not(:disabled) {
  color: var(--fg);
}

.net-button.on {
  border-color: color-mix(in srgb, var(--accent) 55%, transparent);
  background: var(--accent-soft);
  color: var(--accent);
}

/* The route in force: read-only, under everything else. */
.net {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  margin: -6px 0 0;
  color: var(--fg-dim);
  font-size: 12px;
  font-weight: 500;
  overflow-wrap: anywhere;
}

.net svg {
  flex: none;
}

.notice-link {
  min-height: 0;
  margin-left: 4px;
  padding: 0;
  border: none;
  background: none;
  color: inherit;
  font-size: 13px;
  font-weight: 600;
  text-decoration: underline;
}

.notice-link:hover:not(:disabled) {
  background: none;
}

.notice {
  margin: 0;
  padding: 10px 13px;
  border-radius: 12px;
  font-size: 13px;
  background: color-mix(in srgb, var(--danger) 14%, transparent);
  border: 1px solid color-mix(in srgb, var(--danger) 40%, transparent);
  color: var(--danger);
}

.notice.success {
  background: color-mix(in srgb, var(--success) 14%, transparent);
  border-color: color-mix(in srgb, var(--success) 40%, transparent);
  color: var(--success);
}

.notice.info {
  background: var(--accent-soft);
  border-color: var(--glass-edge);
  color: var(--fg);
}
.login > .quiet { min-height: 28px; padding: 4px 10px; font-size: 12px; }

@media (max-height: 740px) {
  .login-wrap { padding: 16px; }
  .login { padding: 20px; gap: 10px; }
}
</style>
