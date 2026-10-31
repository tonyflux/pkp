<script setup lang="ts">
// The account dialogs: Add account /
// Sign in again, Local note, Sign out and remove, and the two questions a switch or a play
// may ask. One modal at a time, whichever accounts.dialog names. Esc, the
// scrim and × give the safe answer; focus stays inside and goes back to
// where it was when the dialog opened.
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import AccountIdentity from "./AccountIdentity.vue";
import { WORKSPACE_QUERY, keychainName } from "../dom";
import { maskLogin } from "../format";
import {
  accountById,
  accountLabel,
  accountName,
  accounts,
  cancelSignIn,
  closeAccountDialog,
  cloudOf,
  currentAccount,
  drive,
  loadTasks,
  removeAccount,
  renameAccount,
  submitSignIn,
  uploadActive,
  type AccountDialog,
} from "../store";
import { DriveService, type SavedSignIn } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import { t } from "../i18n";

const LABEL_MAX = 24;

const dialog = computed(() => accounts.dialog);
const sheet = ref<HTMLElement>();
const loginField = ref<HTMLInputElement>();
const passwordField = ref<HTMLInputElement>();
const renameField = ref<HTMLInputElement>();

// The account the dialog is about.
const subject = computed(() => {
  const d = dialog.value;
  if (!d) return null;
  switch (d.kind) {
    case "signin":
    case "rename":
    case "remove":
      return accountById(d.account);
    case "leave":
      return d.target ? accountById(d.target) : null;
    case "replace":
      return accountById(d.owner);
  }
});

const currentLabel = computed(() => accountLabel(currentAccount.value) || t("当前账号"));

const title = computed(() => {
  const d = dialog.value;
  switch (d?.kind) {
    case "signin":
      return d.account ? t("重新登录「{name}」", { name: accountName(d.account) }) : t("添加 PikPak 账号");
    case "rename":
      return t("修改本机备注");
    case "remove":
      return t("退出并移除此账号？");
    case "leave":
      return t("先处理未提交的内容");
    case "replace":
      return t("播放此账号的视频？");
  }
  return "";
});

/* ---------- Add account / Sign in again ---------- */

const emptySaved = (): SavedSignIn => ({ supported: false, account: "", hasPassword: false, remember: false, autoSignIn: false });
const signin = reactive({ login: "", password: "", label: "", remember: true, autoSignIn: true, pending: false, error: "" });
const saved = ref<SavedSignIn>(emptySaved());
// Bumped whenever an answer in flight stops mattering: the form was closed,
// or another one opened.
let signinSerial = 0;

const reauth = computed(() => dialog.value?.kind === "signin" && !!dialog.value.account);
// The remembered password stands in for an empty field, for its own account only.
const usesSaved = computed(
  () => saved.value.hasPassword && !!saved.value.account && signin.login.trim() === saved.value.account,
);

function toggleRemember() {
  signin.remember = !signin.remember;
  if (!signin.remember) signin.autoSignIn = false;
}

function toggleAutoSignIn() {
  signin.autoSignIn = !signin.autoSignIn;
  if (signin.autoSignIn) signin.remember = true;
}

async function submitSignin() {
  const d = accounts.dialog;
  if (d?.kind !== "signin" || signin.pending) return;
  const login = signin.login.trim();
  const label = signin.label.trim();
  if (!login || (!signin.password && !usesSaved.value)) {
    signin.error = t("请填写账号和密码");
    (login ? passwordField : loginField).value?.focus();
    return;
  }
  if (!d.account && [...label].length > LABEL_MAX) {
    signin.error = t("本机备注最多 {n} 个字", { n: LABEL_MAX });
    return;
  }
  signin.error = "";
  signin.pending = true;
  const serial = ++signinSerial;
  const res = await submitSignIn({
    login,
    password: signin.password,
    label: d.account ? "" : label,
    account: d.account,
    remember: signin.remember,
    autoSignIn: signin.autoSignIn,
  });
  // Closed or replaced meanwhile: the answer is nobody's any more.
  if (serial !== signinSerial) return;
  signin.pending = false;
  if (!res.failure) {
    if (accounts.dialog === d) closeAccountDialog();
    return;
  }
  if (res.failure === "cancelled") return;
  signin.error = res.message || t("登录失败，请稍后再试");
  signin.password = "";
  await nextTick();
  passwordField.value?.focus();
}

/* ---------- Local note ---------- */

const rename = reactive({ name: "", error: "", saving: false });

async function saveRename() {
  const d = accounts.dialog;
  if (d?.kind !== "rename" || rename.saving) return;
  const name = rename.name.trim();
  if (!name) {
    rename.error = t("请填写本机备注");
    renameField.value?.focus();
    return;
  }
  if ([...name].length > LABEL_MAX) {
    rename.error = t("本机备注最多 {n} 个字", { n: LABEL_MAX });
    renameField.value?.focus();
    return;
  }
  rename.error = "";
  rename.saving = true;
  const ok = await renameAccount(d.account, name);
  rename.saving = false;
  if (ok && accounts.dialog === d) closeAccountDialog();
}

/* ---------- Sign out and remove ---------- */

const removal = reactive({ removing: false, checking: false });

// Uploads on this computer that removing the account stops.
const localCount = computed(() => {
  const d = dialog.value;
  if (d?.kind !== "remove") return 0;
  return drive.uploads.filter((task) => task.account === d.account && uploadActive(task)).length;
});

const localText = computed(() =>
  localCount.value
    ? t("本机将停止此账号的 {n} 个本地传输任务，并清理登录状态和账号缓存。已下载的文件会保留。", { n: localCount.value })
    : t("本机将清理此账号的登录状态和账号缓存。已下载的文件会保留。"),
);

// Cloud-add tasks carry on on the server whatever happens here.
const cloudNote = computed(() => {
  const d = dialog.value;
  if (d?.kind !== "remove") return "";
  if (subject.value?.needsPassword) return t("云添加任务暂时无法读取；移除不会取消云端任务。");
  if (removal.checking) return t("正在检查云添加任务…");
  const running = cloudOf(d.account).tasks.filter(
    (task) => task.phase !== "PHASE_TYPE_COMPLETE" && task.phase !== "PHASE_TYPE_ERROR",
  ).length;
  return running ? t("{n} 个云添加任务可能仍在云端运行。移除不会取消云端任务，本机将不再跟踪。", { n: running }) : "";
});

async function confirmRemove() {
  const d = accounts.dialog;
  if (d?.kind !== "remove" || removal.removing) return;
  removal.removing = true;
  const ok = await removeAccount(d.account);
  removal.removing = false;
  if (ok && accounts.dialog === d) closeAccountDialog();
}

/* ---------- the two questions ---------- */

const leaveText = computed(() => {
  const d = dialog.value;
  if (d?.kind !== "leave") return "";
  return d.target
    ? t("「{what}」属于「{owner}」，不会转到「{target}」执行。", {
        what: d.what,
        owner: currentLabel.value,
        target: accountName(d.target),
      })
    : t("「{what}」属于「{owner}」，不会转到新账号执行。", { what: d.what, owner: currentLabel.value });
});

function answer(go: boolean) {
  const d = accounts.dialog;
  if (d?.kind === "leave" || d?.kind === "replace") d.resolve(go);
}

/* ---------- opening, closing, focus ---------- */

// Esc, ×, Cancel and the scrim: a sign-in in flight is called off first, and
// its late answer ignored.
function dismiss() {
  if (signin.pending) {
    signinSerial += 1;
    signin.pending = false;
    void cancelSignIn();
  }
  closeAccountDialog();
}

function focusDefault() {
  void nextTick(() => {
    const el = sheet.value?.querySelector<HTMLElement>("[data-autofocus]");
    el?.focus({ preventScroll: true });
    if (el instanceof HTMLInputElement && el === renameField.value) el.select();
  });
}

async function open(d: AccountDialog) {
  signinSerial += 1;
  Object.assign(signin, { login: "", password: "", label: "", remember: true, autoSignIn: true, pending: false, error: "" });
  saved.value = emptySaved();
  Object.assign(rename, { name: "", error: "", saving: false });
  Object.assign(removal, { removing: false, checking: false });
  const a = subject.value;
  if (d.kind === "rename") rename.name = a?.label || accountLabel(a);
  if (d.kind === "signin") signin.login = d.account ? (a?.login ?? "") : "";
  focusDefault();

  if (d.kind === "signin") {
    const s = await DriveService.SavedSignIn(d.account).catch(() => null);
    if (!s || accounts.dialog !== d) return;
    saved.value = s;
    // Sign in again starts from the login remembered for that account, unless
    // the user has typed meanwhile; Add account starts empty.
    if (d.account && s.account && (!signin.login || signin.login === a?.login)) signin.login = s.account;
    signin.remember = s.remember;
    signin.autoSignIn = s.autoSignIn;
  } else if (d.kind === "remove" && a && !a.needsPassword) {
    removal.checking = true;
    await loadTasks(false, a.id).catch(() => undefined);
    if (accounts.dialog === d) removal.checking = false;
  }
}

let opener: HTMLElement | null = null;

// The visible Switch account trigger, for when what opened the dialog is gone (a
// row of the panel, or a page the dialog's answer replaced).
function triggerFallback(): HTMLElement | null {
  const shown = Array.from(document.querySelectorAll<HTMLElement>("[data-account-trigger]")).filter((el) => el.offsetParent);
  return (window.matchMedia(WORKSPACE_QUERY).matches ? shown.at(-1) : shown[0]) ?? null;
}

function restoreFocus() {
  const from = opener;
  opener = null;
  void nextTick(() => {
    if (accounts.dialog) return;
    const target = from && from.isConnected && from !== document.body ? from : triggerFallback();
    target?.focus({ preventScroll: true });
  });
}

watch(
  dialog,
  (d, old) => {
    if (d && !old) opener = document.activeElement as HTMLElement | null;
    if (d && d !== old) void open(d);
    if (!d && old) restoreFocus();
  },
  { flush: "pre", immediate: true },
);

function focusables(): HTMLElement[] {
  const found = sheet.value?.querySelectorAll<HTMLElement>(
    'button:not(:disabled), input:not(:disabled), [tabindex]:not([tabindex="-1"])',
  );
  return found ? Array.from(found) : [];
}

function onKeydown(event: KeyboardEvent) {
  if (event.key !== "Tab") return;
  const items = focusables();
  if (!items.length) return;
  const first = items[0];
  const last = items[items.length - 1];
  const active = document.activeElement;
  if (event.shiftKey && (active === first || !sheet.value?.contains(active))) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && active === last) {
    event.preventDefault();
    first.focus();
  }
}

// The dialog owns Esc ahead of everything behind it.
function onEscape(event: KeyboardEvent) {
  if (event.key !== "Escape" || !accounts.dialog) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  dismiss();
}

// Focus that wanders out (a click on the page behind) comes back in.
function onFocusIn(event: FocusEvent) {
  if (!accounts.dialog || !sheet.value) return;
  if (sheet.value.contains(event.target as Node)) return;
  (focusables()[0] ?? sheet.value).focus({ preventScroll: true });
}

// A press that starts and ends on the scrim closes; a drag out of a field
// does not.
let pressedScrim = false;
function onScrimDown(event: PointerEvent) {
  pressedScrim = event.target === event.currentTarget;
}
function onScrimClick(event: MouseEvent) {
  if (pressedScrim && event.target === event.currentTarget) dismiss();
  pressedScrim = false;
}

onMounted(() => {
  window.addEventListener("keydown", onEscape, true);
  document.addEventListener("focusin", onFocusIn);
});
onBeforeUnmount(() => {
  window.removeEventListener("keydown", onEscape, true);
  document.removeEventListener("focusin", onFocusIn);
});
</script>

<template>
  <Teleport to="body">
    <div v-if="dialog" class="scrim account-scrim" @pointerdown="onScrimDown" @click="onScrimClick">
      <section
        ref="sheet"
        class="sheet glass glass-sheet"
        role="dialog"
        aria-modal="true"
        aria-labelledby="account-dialog-title"
        tabindex="-1"
        @keydown="onKeydown"
      >
        <header>
          <h2 id="account-dialog-title">{{ title }}</h2>
          <button type="button" class="quiet icon close" :title="t('关闭')" :aria-label="t('关闭')" @click="dismiss">
            <Icon name="close" :size="16" />
          </button>
        </header>

        <!-- Add account / Sign in again -->
        <form v-if="dialog.kind === 'signin'" class="body" novalidate @submit.prevent="submitSignin">
          <p class="lead">
            {{ reauth ? t("验证此账号后恢复访问，原有任务保留归属。") : t("保留已登录账号，添加你的备用号或内容分区。") }}
          </p>
          <label class="field">
            <span>{{ t("账号（邮箱或手机号）") }}</span>
            <input
              ref="loginField"
              v-model="signin.login"
              autocomplete="username"
              spellcheck="false"
              placeholder="you@example.com"
              :data-autofocus="reauth ? undefined : ''"
            />
          </label>
          <label class="field">
            <span>{{ t("密码") }}</span>
            <input
              ref="passwordField"
              v-model="signin.password"
              type="password"
              autocomplete="current-password"
              :placeholder="usesSaved ? t('已记住密码，可直接登录') : ''"
              :data-autofocus="reauth ? '' : undefined"
            />
          </label>
          <label v-if="!reauth" class="field">
            <span>{{ t("本机备注") }} <small>{{ t("可选") }}</small></span>
            <input v-model="signin.label" :maxlength="LABEL_MAX" :placeholder="t('例如：影视收藏')" />
          </label>

          <div v-if="saved.supported" class="choices">
            <button
              type="button"
              class="choice"
              :class="{ on: signin.remember }"
              role="checkbox"
              :aria-checked="signin.remember"
              :title="t('账号存在这台电脑上，密码存进系统{keychain}', { keychain: keychainName })"
              @click="toggleRemember"
            >
              <i class="box"><Icon v-if="signin.remember" name="check" :size="12" /></i>
              {{ t("记住账号密码") }}
            </button>
            <button
              type="button"
              class="choice"
              :class="{ on: signin.autoSignIn }"
              role="checkbox"
              :aria-checked="signin.autoSignIn"
              :title="t('登录失效时用记住的密码自动登录')"
              @click="toggleAutoSignIn"
            >
              <i class="box"><Icon v-if="signin.autoSignIn" name="check" :size="12" /></i>
              {{ t("自动登录") }}
            </button>
          </div>

          <p class="error" :class="{ shown: !!signin.error }" role="alert">{{ signin.error }}</p>

          <div class="actions">
            <button type="button" @click="dismiss">{{ t("取消") }}</button>
            <button type="submit" class="primary" :class="{ working: signin.pending }" :disabled="signin.pending">
              <Orbit v-if="signin.pending" :size="15" :label="t('正在登录')" />
              {{ signin.pending ? t("正在登录…") : reauth ? t("重新登录") : t("登录并添加") }}
            </button>
          </div>
          <p class="foot">{{ t("登录成功后切换到此账号；同一账号不会重复添加。") }}</p>
        </form>

        <!-- Local note -->
        <form v-else-if="dialog.kind === 'rename'" class="body" novalidate @submit.prevent="saveRename">
          <p class="lead">{{ t("仅改变这台电脑上的显示名称，不修改 PikPak 昵称。") }}</p>
          <label class="field">
            <span>{{ t("本机备注") }}</span>
            <input ref="renameField" v-model="rename.name" :maxlength="LABEL_MAX" required data-autofocus />
          </label>
          <small v-if="subject" class="login">{{ maskLogin(subject.login) }}</small>
          <p class="error" :class="{ shown: !!rename.error }" role="alert">{{ rename.error }}</p>
          <div class="actions">
            <button type="button" @click="dismiss">{{ t("取消") }}</button>
            <button type="submit" class="primary" :class="{ working: rename.saving }" :disabled="rename.saving">
              {{ t("保存备注") }}
            </button>
          </div>
        </form>

        <!-- Sign out and remove -->
        <div v-else-if="dialog.kind === 'remove'" class="body">
          <AccountIdentity class="owner" :account="subject" />
          <p class="lead">{{ localText }}</p>
          <p class="note">{{ t("此账号的同步关系也会解除；已经完成的本地和云端文件都会保留。") }}</p>
          <p v-if="cloudNote" class="note" role="status">{{ cloudNote }}</p>
          <p class="foot">{{ t("不会注销 PikPak 账号、删除云盘文件或撤销已有分享。") }}</p>
          <div class="actions">
            <button type="button" data-autofocus @click="dismiss">{{ t("保留账号") }}</button>
            <button
              type="button"
              class="danger"
              :class="{ working: removal.removing }"
              :disabled="removal.removing"
              @click="confirmRemove"
            >
              {{ removal.removing ? t("正在移除…") : t("退出并移除") }}
            </button>
          </div>
        </div>

        <!-- An unsubmitted sheet belongs to the account on screen. -->
        <div v-else-if="dialog.kind === 'leave'" class="body">
          <p class="lead">{{ leaveText }}</p>
          <div class="actions">
            <button type="button" data-autofocus @click="answer(false)">{{ t("留在当前账号") }}</button>
            <button type="button" class="danger" @click="answer(true)">{{ t("放弃并切换") }}</button>
          </div>
        </div>

        <!-- Another account's video is playing. -->
        <div v-else-if="dialog.kind === 'replace'" class="body">
          <p class="lead">
            {{ t("将停止「{from}」的播放并保存进度，再播放「{to}」的视频。", { from: accountLabel(subject) || t("原账号"), to: currentLabel }) }}
          </p>
          <div class="actions">
            <button type="button" data-autofocus @click="answer(false)">{{ t("继续原视频") }}</button>
            <button type="button" class="primary" @click="answer(true)">{{ t("播放此视频") }}</button>
          </div>
        </div>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
/* Above every other sheet: its questions come up over them. */
.account-scrim {
  z-index: 50;
  padding: 16px;
  animation-duration: 0.16s;
}

.sheet {
  width: min(480px, calc(100vw - 32px));
  max-height: calc(100dvh - 32px);
  overflow-y: auto;
  scrollbar-width: thin;
  gap: 14px;
  padding: 24px;
  border-radius: 28px;
  animation-duration: 0.16s;
}

header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

h2 {
  margin: 0;
  font-size: 22px;
  font-weight: 700;
  line-height: 1.35;
  overflow-wrap: anywhere;
  text-wrap: balance;
}

.close {
  flex: none;
  width: 40px;
  height: 40px;
  min-height: 40px;
  margin: -6px -8px 0 0;
}

.body {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.lead {
  margin: 0;
  font-size: 14px;
  color: var(--fg-dim);
  overflow-wrap: anywhere;
  text-wrap: pretty;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.field > span {
  font-size: 13px;
  font-weight: 600;
  color: var(--fg);
}

.field > span small {
  font-weight: 500;
  color: var(--fg-dim);
}

.field input {
  height: 44px;
  font-size: 15px;
}

.login {
  margin-top: -6px;
  font-size: 13px;
  color: var(--fg-dim);
}

.choices {
  display: flex;
  flex-wrap: wrap;
  gap: 18px;
}

/* As on the sign-in page: a box and its words, not a pill. */
.choice {
  min-height: 40px;
  padding: 0;
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

.error {
  margin: 0;
  padding: 10px 13px;
  border-radius: 12px;
  font-size: 13px;
  background: color-mix(in srgb, var(--danger) 14%, transparent);
  border: 1px solid color-mix(in srgb, var(--danger) 40%, transparent);
  color: var(--danger);
  text-wrap: pretty;
}

/* Kept in the page while empty, so the alert is announced when words
   arrive; it takes no room then (the margin cancels the column's gap). */
.error:not(.shown) {
  margin-top: -14px;
  padding: 0;
  border: 0;
  background: none;
}

.note {
  margin: 0;
  padding: 12px 16px;
  border-radius: 14px;
  font-size: 14px;
  color: var(--fg);
  background: color-mix(in srgb, var(--fg) 6%, transparent);
  border: 1px solid var(--line);
  text-wrap: pretty;
}

.foot {
  margin: 0;
  font-size: 12px;
  color: var(--fg-dim);
  text-wrap: pretty;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 4px;
}

.actions button {
  min-width: 96px;
  min-height: 40px;
  padding: 0 18px;
  font-size: 14px;
  font-weight: 600;
}

@media (prefers-reduced-motion: reduce) {
  .account-scrim,
  .sheet {
    animation: none;
  }
}
</style>
