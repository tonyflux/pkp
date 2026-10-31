<script setup lang="ts">
// App Lock's lock screen: an
// opaque page over the whole window — never glass over readable files — with
// the brand, the lock, the way back in and what keeps running. Nothing on it
// names an account, a file or a task. Always dark, like the design, in both
// themes. Two ways in: System Authentication is one button that has Go show the system's own
// dialog (Touch ID or the login password, Windows Hello; never a system
// password typed here); PKP Standalone Password is a field checked by Go, which counts
// wrong guesses and imposes the cooldowns. Under system verification a standalone password, if set,
// is the alternative (Use standalone password) — and the only one when the system cannot
// ask. Forgot password explains there is no way back but signing out here.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Logo from "./Logo.vue";
import { appLock, forgetAppLock, unlockApp, unlockWithPassword } from "../store";
import { t } from "../i18n";

const state = computed(() => appLock.state);
const asking = computed(() => !!state.value?.authenticating);
const systemReady = computed(() => !!state.value?.available && state.value?.method === "system");

// The password field shows when the standalone password is the way in, when the system
// cannot ask, or when the user chose it over the system's dialog.
const chosePassword = ref(false);
const passwordShown = computed(
  () => !!state.value?.hasPassword && (state.value.method === "password" || !systemReady.value || chosePassword.value),
);

const button = ref<HTMLButtonElement | null>(null);
const field = ref<HTMLInputElement | null>(null);
const password = ref("");
const visible = ref(false);
const checking = ref(false);
// The last answer to a password: a wrong one, or a cooldown under way.
const answer = ref("");
const shaking = ref(false);

// The cooldown counts down here; Go refuses until it ends either way.
const now = ref(Date.now());
const tick = setInterval(() => (now.value = Date.now()), 1000);
const secondsLeft = computed(() => Math.max(0, Math.ceil(((state.value?.cooldownUntil ?? 0) - now.value) / 1000)));
const cooling = computed(() => secondsLeft.value > 0);
const passwordError = computed(() => {
  if (cooling.value) {
    return secondsLeft.value > 60
      ? t("密码错误 5 次，请 {n} 分钟后再试", { n: Math.ceil(secondsLeft.value / 60) })
      : t("密码错误 5 次，请 {n} 秒后再试", { n: secondsLeft.value });
  }
  return answer.value;
});

const lead = computed(() =>
  passwordShown.value ? t("输入这台电脑上的 PKP 独立密码，回到离开前的画面。") : t("使用这台电脑的身份验证，即可回到离开前的画面。"),
);
const hint = computed(() => {
  if (passwordShown.value) return "";
  if (asking.value) return t("请在系统弹出的窗口中完成验证");
  if (appLock.note) return appLock.note;
  return t("由系统验证你的身份，PKP 不会看到你的密码");
});

// Pause playback and save position: Progress saved only once Go has written it.
const playback = computed(() => {
  switch (state.value?.playback) {
    case "saved":
      return t("播放已暂停，进度已保存");
    case "paused":
      return t("播放已暂停");
  }
  return "";
});

async function focusWay() {
  await nextTick();
  if (passwordShown.value && !cooling.value) field.value?.focus({ preventScroll: true });
  else button.value?.focus({ preventScroll: true });
}

async function unlock() {
  await unlockApp();
  // Still locked (cancelled, refused): the button keeps the focus.
  if (state.value?.locked) void focusWay();
}

async function submit() {
  if (checking.value || cooling.value) return;
  if (!password.value) {
    field.value?.focus({ preventScroll: true });
    return;
  }
  checking.value = true;
  const r = await unlockWithPassword(password.value);
  checking.value = false;
  password.value = "";
  if (!r || r.result === "ok") {
    answer.value = r ? "" : appLock.note;
    return;
  }
  answer.value = r.result === "cooldown" ? "" : r.message;
  now.value = Date.now();
  shaking.value = false;
  await nextTick();
  shaking.value = true;
  void focusWay();
}

function switchWay(toPassword: boolean) {
  chosePassword.value = toPassword;
  answer.value = "";
  password.value = "";
  appLock.note = "";
  void focusWay();
}

// A cooldown ending gives the field back.
watch(cooling, (on) => {
  if (!on) {
    answer.value = "";
    void focusWay();
  }
});

/* ---------- Forgot password ---------- */

const forgetting = ref(false);
const forgetWorking = ref(false);
const forgetError = ref("");
const forgetCancel = ref<HTMLButtonElement | null>(null);

async function openForget() {
  forgetError.value = "";
  forgetting.value = true;
  await nextTick();
  // The safe answer first: going back to the lock screen.
  forgetCancel.value?.focus();
}

function closeForget() {
  if (forgetWorking.value) return;
  forgetting.value = false;
  void focusWay();
}

async function confirmForget() {
  forgetWorking.value = true;
  forgetError.value = await forgetAppLock();
  forgetWorking.value = false;
}

onMounted(focusWay);
onBeforeUnmount(() => clearInterval(tick));
</script>

<template>
  <section
    class="lock-screen"
    data-lock-screen
    role="dialog"
    aria-modal="true"
    aria-labelledby="lock-title"
    @keydown.stop
    @keyup.stop
    @keypress.stop
  >
    <header class="top" style="--wails-draggable: drag">
      <span class="brand"><Logo :size="38" />PKP</span>
      <span class="aside">{{ t("这台电脑上的隐私锁") }}</span>
    </header>

    <div class="middle" :inert="forgetting">
      <div class="emblem" aria-hidden="true">
        <div class="ring"><Icon name="lock" :size="42" /></div>
      </div>
      <p class="kicker"><i aria-hidden="true"></i>{{ t("已锁定") }}</p>
      <h1 id="lock-title">{{ t("你的空间，暂时只属于你。") }}</h1>
      <p class="lead">{{ lead }}</p>

      <form v-if="passwordShown" class="password" :class="{ shaking }" @submit.prevent="submit" @animationend="shaking = false">
        <div class="field" :class="{ off: cooling }">
          <input
            ref="field"
            v-model="password"
            :type="visible ? 'text' : 'password'"
            :placeholder="t('输入 PKP 独立密码')"
            :aria-label="t('PKP 独立密码')"
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
            :disabled="cooling || checking"
            :aria-invalid="!!passwordError"
            aria-describedby="lock-password-error"
          />
          <button
            type="button"
            class="eye"
            :aria-label="visible ? t('隐藏密码') : t('显示密码')"
            :aria-pressed="visible"
            :disabled="cooling"
            @click="visible = !visible"
          >
            <Icon :name="visible ? 'eyeOff' : 'eye'" :size="19" />
          </button>
        </div>
        <button ref="button" type="submit" class="unlock" :disabled="cooling || checking">
          <Icon name="lock" :size="19" />
          {{ t("解锁 PKP") }}
          <Icon class="arrow" name="arrowRight" :size="17" />
        </button>
        <p v-if="passwordError" id="lock-password-error" class="error" role="alert">
          <Icon name="alert" :size="15" />{{ passwordError }}
        </p>
      </form>
      <button v-else ref="button" type="button" class="unlock" :disabled="asking" @click="unlock">
        <Icon name="shield" :size="19" />
        {{ t("解锁 PKP") }}
        <Icon class="arrow" name="arrowRight" :size="17" />
      </button>

      <div class="ways">
        <button v-if="!passwordShown && state?.hasPassword" type="button" class="link" @click="switchWay(true)">{{ t("改用独立密码") }}</button>
        <button v-if="passwordShown && systemReady" type="button" class="link" @click="switchWay(false)">{{ t("改用系统验证") }}</button>
        <button v-if="passwordShown" type="button" class="link" @click="openForget">{{ t("忘记密码？") }}</button>
      </div>
      <p v-if="hint" class="hint" role="status">{{ hint }}</p>
    </div>

    <footer class="bottom">
      <span class="keeps"><Icon name="transfer" :size="17" />{{ t("传输可继续运行") }}</span>
      <span v-if="playback" class="keeps playback"><Icon name="pause" :size="17" />{{ playback }}</span>
      <span class="aside">{{ t("文件与账号信息已隐藏") }}</span>
    </footer>

    <!-- Inside the lock screen, not teleported: under the lock everything
         else in the window is hidden. -->
    <div v-if="forgetting" class="forget-scrim" @click.self="closeForget" @keydown.esc.prevent="closeForget">
      <div class="forget" role="alertdialog" aria-modal="true" aria-labelledby="forget-title" aria-describedby="forget-text">
        <span class="forget-symbol" aria-hidden="true"><Icon name="key" :size="22" /></span>
        <span class="forget-kicker">NO RECOVERY</span>
        <h2 id="forget-title">{{ t("独立密码没有找回通道") }}</h2>
        <p id="forget-text">
          {{ t("PKP 不保存密码原文，也不会通过邮箱或客服重置。要继续使用这台电脑上的 PKP，只能退出登录并清除本机缓存，重新登录后重新设置应用锁。云端文件和已下载到电脑的文件不受影响。") }}
        </p>
        <p v-if="forgetError" class="forget-error" role="alert"><Icon name="alert" :size="15" />{{ forgetError }}</p>
        <div class="forget-actions">
          <button ref="forgetCancel" type="button" :disabled="forgetWorking" @click="closeForget">{{ t("返回锁屏") }}</button>
          <button type="button" class="danger" :disabled="forgetWorking" @click="confirmForget">
            {{ forgetWorking ? t("正在清除…") : t("退出登录并清除本机数据") }}
          </button>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
/* Over everything the page has, teleported menus and sheets included. */
.lock-screen {
  position: fixed;
  inset: 0;
  z-index: 2147483647;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  container-type: inline-size;
  color: #f4f8fd;
  background: radial-gradient(ellipse 49% 60% at 50% 46%, #25506a 0, #15344d 47%, #101f33 100%);
  font-size: 14px;
  line-height: 1.5;
  -webkit-user-select: none;
  user-select: none;
}

.lock-screen::before {
  content: "";
  position: absolute;
  inset: 0;
  pointer-events: none;
  background:
    linear-gradient(145deg, rgba(168, 236, 248, 0.07), transparent 37%),
    radial-gradient(circle at 84% 18%, rgba(141, 218, 225, 0.11), transparent 30%);
}

/* The title bar is hidden: the traffic lights sit in the top strip, which
   also moves the window. */
.top,
.bottom {
  position: relative;
  z-index: 1;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 24px 35px;
  color: #a8bfce;
  font-size: 12px;
}

.top {
  padding-top: 52px;
}

.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  color: #eef7ff;
  font-size: 17px;
  font-weight: 690;
  letter-spacing: 0.03em;
}

.middle {
  position: relative;
  z-index: 1;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  /* Never under the footer: a window too short for it all scrolls here. */
  justify-content: safe center;
  overflow-y: auto;
  padding: 20px 20px 24px;
  text-align: center;
}

.emblem {
  flex: none;
  width: 176px;
  height: 176px;
  margin-bottom: 33px;
  display: grid;
  place-items: center;
  border: 1px solid rgba(186, 234, 252, 0.13);
  border-radius: 50%;
  background: radial-gradient(circle, rgba(109, 210, 230, 0.11), transparent 72%);
  box-shadow:
    0 0 0 23px rgba(145, 218, 247, 0.025),
    0 0 0 53px rgba(145, 218, 247, 0.014);
}

.ring {
  width: 101px;
  height: 101px;
  display: grid;
  place-items: center;
  border: 1px solid rgba(217, 250, 255, 0.42);
  border-radius: 29px;
  background: linear-gradient(150deg, rgba(189, 241, 247, 0.32), rgba(56, 145, 185, 0.23));
  box-shadow:
    0 19px 37px rgba(0, 14, 33, 0.32),
    inset 0 1px rgba(255, 255, 255, 0.38);
  color: #e9fdff;
  transform: rotate(-7deg);
}

.ring :deep(svg) {
  transform: rotate(7deg);
}

.kicker {
  display: flex;
  align-items: center;
  gap: 9px;
  margin: 0;
  color: #b4e5e9;
  font-size: 12px;
  letter-spacing: 0.1em;
}

.kicker i {
  width: 7px;
  height: 7px;
  flex: none;
  border-radius: 50%;
  background: #88deca;
  box-shadow: 0 0 11px rgba(137, 228, 201, 0.4);
}

h1 {
  margin: 13px 0 10px;
  font-size: clamp(25px, 3vw, 37px);
  font-weight: 660;
  line-height: 1.25;
  letter-spacing: -0.03em;
  text-wrap: balance;
}

.lead {
  max-width: 34em;
  margin: 0 0 28px;
  color: #b8cbd7;
  font-size: 14px;
  /* Centred lines: a long language splits evenly, never a 1-word tail. */
  text-wrap: balance;
}

.unlock {
  height: 52px;
  min-width: 240px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 0 18px;
  border: 1px solid #e4faff;
  border-radius: 13px;
  background: #e7f8fa;
  color: #183a53;
  font-size: 15px;
  font-weight: 700;
  box-shadow: 0 12px 30px rgba(0, 23, 44, 0.25);
  transition:
    transform 0.13s var(--ease),
    box-shadow 0.18s var(--ease);
}

.unlock:hover:not(:disabled) {
  background: #f1fbfc;
  box-shadow: 0 15px 33px rgba(0, 23, 44, 0.4);
}

.unlock:active:not(:disabled) {
  transform: scale(0.98);
}

.unlock:disabled {
  opacity: 0.7;
  cursor: progress;
}

.unlock:focus-visible {
  outline: 2px solid #a7e2ff;
  outline-offset: 3px;
}

.unlock .arrow {
  margin-left: auto;
}

.hint {
  max-width: 30em;
  min-height: 1.5em;
  margin: 14px 0 0;
  color: #93b1c2;
  font-size: 12px;
  text-wrap: balance;
}

/* Standalone password: the field over the button, as wide. */
.password {
  width: min(348px, 100%);
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.password .unlock {
  width: 100%;
  min-width: 0;
}

.field {
  display: flex;
  align-items: center;
  height: 54px;
  padding: 0 8px 0 18px;
  border: 1px solid rgba(167, 226, 255, 0.55);
  border-radius: 13px;
  background: rgba(9, 24, 40, 0.62);
  box-shadow: 0 0 0 3px rgba(167, 226, 255, 0.12);
  transition:
    border-color 0.18s var(--ease),
    box-shadow 0.18s var(--ease);
}

.field:focus-within {
  border-color: #a7e2ff;
  box-shadow: 0 0 0 3px rgba(167, 226, 255, 0.28);
}

.field.off {
  border-color: rgba(167, 226, 255, 0.14);
  box-shadow: none;
  opacity: 0.6;
}

.field input {
  flex: 1;
  min-width: 0;
  padding: 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  color: #f4f8fd;
  font-size: 15px;
  letter-spacing: 0.02em;
}

.field input::placeholder {
  color: #8aa3b5;
  letter-spacing: 0;
}

.eye {
  flex: none;
  width: 38px;
  height: 38px;
  min-height: 38px;
  padding: 0;
  border: 0;
  border-radius: 10px;
  background: transparent;
  color: #b9d3e2;
}

.eye:hover:not(:disabled) {
  background: rgba(231, 248, 250, 0.08);
}

/* Centred text with the sign inline: a message that takes two lines
   splits evenly and the sign stays at the start of the first. */
.error {
  margin: 2px 0 0;
  color: #ffb4a8;
  font-size: 13px;
  font-weight: 500;
  text-align: center;
  text-wrap: balance;
}

.error svg {
  display: inline-block;
  margin-right: 7px;
  vertical-align: -2px;
}

/* A wrong password nudges the form sideways; transform only, so it never
   hides anything mid-way. */
.shaking {
  animation: shake 0.36s var(--ease);
}

@keyframes shake {
  20%,
  60% {
    transform: translateX(-7px);
  }
  40%,
  80% {
    transform: translateX(7px);
  }
}

.ways {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 4px 22px;
  margin-top: 14px;
}

.ways:empty {
  display: none;
}

.link {
  min-height: 30px;
  padding: 2px 2px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: #c4e6f2;
  font-size: 13px;
  font-weight: 500;
  text-decoration: underline;
  text-decoration-color: rgba(196, 230, 242, 0.45);
  text-underline-offset: 5px;
}

.link:hover {
  color: #ffffff;
  text-decoration-color: currentColor;
}

.link:focus-visible,
.eye:focus-visible,
.forget button:focus-visible {
  outline: 2px solid #a7e2ff;
  outline-offset: 2px;
}

/* Forgot password: the design's dark dialog, over the lock screen only. */
.forget-scrim {
  position: absolute;
  inset: 0;
  z-index: 2;
  display: grid;
  place-items: center;
  padding: 24px;
  background: rgba(1, 10, 23, 0.72);
}

.forget {
  /* Wide enough that both answers stay on one row in every language. */
  width: min(480px, 100%);
  padding: 30px;
  border: 1px solid rgba(222, 247, 255, 0.32);
  border-radius: 22px;
  background: #1a3348;
  box-shadow: 0 30px 90px rgba(0, 9, 22, 0.7);
  text-align: left;
}

.forget-symbol {
  width: 48px;
  height: 48px;
  margin-bottom: 16px;
  display: grid;
  place-items: center;
  border-radius: 15px;
  background: rgba(121, 210, 231, 0.2);
  color: #dafaff;
}

.forget-kicker {
  color: #86cfe9;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.18em;
}

.forget h2 {
  margin: 10px 0;
  font-size: 21px;
  line-height: 1.3;
}

.forget p {
  margin: 0 0 22px;
  color: #b9cbd8;
  font-size: 13px;
  line-height: 1.7;
}

.forget .forget-error {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  color: #ffb4a8;
}

.forget-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 9px;
}

.forget-actions button {
  min-height: 40px;
  padding: 0 16px;
  border: 1px solid rgba(220, 244, 255, 0.22);
  border-radius: 11px;
  background: transparent;
  color: #f4f8fd;
  font-size: 14px;
}

.forget-actions button:hover:not(:disabled) {
  background: rgba(231, 248, 250, 0.08);
}

.forget-actions .danger {
  font-weight: 650;
}

.bottom {
  border-top: 1px solid rgba(225, 249, 255, 0.1);
  padding: 19px 35px;
}

.keeps {
  display: flex;
  align-items: center;
  gap: 9px;
}

.keeps :deep(svg) {
  color: #a2dbe5;
}

.playback {
  margin-right: auto;
}

/* A narrow window, including the separate player window. */
@container (max-width: 720px) {
  .top,
  .bottom {
    padding-left: 16px;
    padding-right: 16px;
  }

  .top {
    padding-top: 46px;
  }

  .top .aside,
  .bottom .aside {
    display: none;
  }

  .bottom {
    flex-wrap: wrap;
    justify-content: center;
    gap: 6px 18px;
    padding: 16px;
  }

  .playback {
    margin-right: 0;
  }

  .emblem {
    width: 130px;
    height: 130px;
    margin-bottom: 26px;
  }

  .ring {
    width: 78px;
    height: 78px;
    border-radius: 23px;
  }

  .ring :deep(svg) {
    width: 34px;
    height: 34px;
  }

  .lead {
    max-width: 260px;
  }
}

/* Short windows: the emblem goes first, the way back in stays. */
@media (max-height: 680px) {
  .emblem {
    display: none;
  }
}

@media (prefers-reduced-motion: reduce) {
  .unlock,
  .field {
    transition: none;
  }

  .shaking {
    animation: none;
  }
}
</style>
