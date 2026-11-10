<script setup lang="ts">
// Settings · Privacy & Security · Standalone PKP Password: one sheet in three
// modes — Set (new + repeat), Change (current + new + repeat; Remove Password while it is
// only the backup of System Authentication) and Verify (current, to prove a change to the
// lock). Nonempty input and repeat are checked here; Go checks the current password,
// counts wrong ones and keeps only a derived verifier. The fields live in this
// component and are emptied on close: no password sits in the store.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import { appLockSheet, closeLockSheet, removeLockPassword, submitLockSheet } from "../store";
import { t, N_ } from "../i18n";

const current = ref("");
const next = ref("");
const repeat = ref("");
const local = ref("");
const currentField = ref<HTMLInputElement>();
const nextField = ref<HTMLInputElement>();
const repeatField = ref<HTMLInputElement>();

const mode = computed(() => appLockSheet.mode);
const asksCurrent = computed(() => mode.value === "change" || mode.value === "confirm");
const asksNew = computed(() => mode.value === "set" || mode.value === "change");
const error = computed(() => local.value || appLockSheet.error);

const COPY = {
  set: {
    title: N_("设置 PKP 独立密码"),
    text: N_("独立密码只保存在这台电脑，与 PikPak 账号密码无关；忘记后无法找回，只能退出登录清除本机数据后重设。"),
    action: N_("保存密码"),
  },
  change: {
    title: N_("更改 PKP 独立密码"),
    text: N_("先验证当前密码，再设置新的独立密码。忘记后无法找回，只能退出登录清除本机数据后重设。"),
    action: N_("保存新密码"),
  },
  confirm: {
    title: N_("验证 PKP 独立密码"),
    text: N_("修改应用锁设置前，需要先证明你知道当前的独立密码。"),
    action: N_("验证"),
  },
};
const copy = computed(() => (mode.value ? COPY[mode.value] : COPY.set));

watch(mode, async (m) => {
  current.value = next.value = repeat.value = local.value = "";
  if (!m) return;
  await nextTick();
  (asksCurrent.value ? currentField.value : nextField.value)?.focus();
});

// A refused answer goes back to the field it is about.
watch(
  () => appLockSheet.error,
  async (e) => {
    if (!e) return;
    await nextTick();
    // Go's text, as sent (source) or already translated. i18n-ignore
    if (asksCurrent.value && (e === "当前密码不正确" || e === t("当前密码不正确"))) {
      current.value = "";
      currentField.value?.focus();
    }
  },
);

async function submit() {
  if (appLockSheet.working) return;
  local.value = "";
  if (asksCurrent.value && !current.value) {
    local.value = t("请输入当前密码");
    currentField.value?.focus();
    return;
  }
  if (asksNew.value) {
    if (!next.value) {
      local.value = t("请输入新密码");
      nextField.value?.focus();
      return;
    }
    if (next.value !== repeat.value) {
      local.value = t("两次输入不一致");
      repeat.value = "";
      repeatField.value?.focus();
      return;
    }
  }
  await submitLockSheet({ current: current.value, next: next.value });
}

async function remove() {
  local.value = "";
  if (!current.value) {
    local.value = t("移除前请输入当前密码");
    currentField.value?.focus();
    return;
  }
  await removeLockPassword(current.value);
}

function close() {
  if (!appLockSheet.working) closeLockSheet();
}

// The sheet owns Esc while it is up, ahead of the page behind it.
function onKeydown(ev: KeyboardEvent) {
  if (!appLockSheet.mode || ev.key !== "Escape") return;
  ev.stopImmediatePropagation();
  close();
}

onMounted(() => window.addEventListener("keydown", onKeydown, true));
onBeforeUnmount(() => {
  window.removeEventListener("keydown", onKeydown, true);
  closeLockSheet();
});
</script>

<template>
  <Teleport to="body">
    <div v-if="mode" class="scrim" @click.self="close">
      <form
        class="sheet glass glass-sheet"
        role="dialog"
        aria-modal="true"
        aria-labelledby="lock-sheet-title"
        aria-describedby="lock-sheet-text"
        @keydown.stop
        @submit.prevent="submit"
      >
        <span class="symbol" aria-hidden="true"><Icon name="key" :size="22" /></span>
        <span class="kicker">PKP PASSWORD</span>
        <h2 id="lock-sheet-title">{{ t(copy.title) }}</h2>
        <p id="lock-sheet-text" class="text">{{ t(copy.text) }}</p>

        <label v-if="asksCurrent" class="field">
          <span>{{ t("当前密码") }}</span>
          <input
            ref="currentField"
            v-model="current"
            type="password"
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
            :disabled="appLockSheet.working"
          />
        </label>
        <template v-if="asksNew">
          <label class="field">
            <span>{{ t("新密码") }}</span>
            <input
              ref="nextField"
              v-model="next"
              type="password"
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
              :disabled="appLockSheet.working"
            />
          </label>
          <label class="field">
            <span>{{ t("再次输入") }}</span>
            <input
              ref="repeatField"
              v-model="repeat"
              type="password"
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
              :disabled="appLockSheet.working"
            />
          </label>
        </template>

        <p v-if="error" class="error" role="alert"><Icon name="alert" :size="15" />{{ error }}</p>

        <footer>
          <button v-if="mode === 'change' && appLockSheet.removable" type="button" class="remove quiet danger-action" :disabled="appLockSheet.working" @click="remove">
            {{ t("移除密码") }}
          </button>
          <span class="answers">
            <button type="button" :disabled="appLockSheet.working" @click="close">{{ t("取消") }}</button>
            <button type="submit" class="primary" :class="{ working: appLockSheet.working }" :disabled="appLockSheet.working">
              <Orbit v-if="appLockSheet.working" :size="15" />
              {{ t(copy.action) }}
            </button>
          </span>
        </footer>
      </form>
    </div>
  </Teleport>
</template>

<style scoped>
.sheet {
  width: min(460px, calc(100vw - 80px));
  gap: 14px;
  padding: 28px 32px;
}

.symbol {
  width: 48px;
  height: 48px;
  display: grid;
  place-items: center;
  border-radius: 15px;
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
  margin: -6px 0 0;
  font-size: 24px;
  font-weight: 650;
  line-height: 32px;
}

.text {
  margin: -4px 0 4px;
  color: var(--fg-dim);
  font-size: 14px;
  line-height: 1.7;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 7px;
}

.field span {
  font-size: 13px;
  font-weight: 600;
}

.field input {
  min-height: 46px;
  padding: 0 14px;
  border-radius: 12px;
  font-size: 15px;
  letter-spacing: 0.04em;
}

.error {
  display: flex;
  align-items: center;
  gap: 7px;
  margin: 0;
  color: var(--danger);
  font-size: 13px;
  font-weight: 500;
}

/* The secondary removal action sits left of cancel and the primary action.
   When they do not fit, the paired answers stay above removal. */
footer {
  display: flex;
  flex-wrap: wrap-reverse;
  justify-content: flex-end;
  align-items: center;
  gap: 10px;
  margin-top: 6px;
}

.answers {
  display: flex;
  gap: 10px;
  margin-left: auto;
}

footer button {
  min-width: 96px;
  min-height: 44px;
  padding: 0 20px;
  font-size: 15px;
  font-weight: 600;
}

/* The backup's way out sits apart from Cancel and the main answer. */
footer .remove {
  margin-right: auto;
}

</style>
