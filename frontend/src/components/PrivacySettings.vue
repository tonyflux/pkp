<script setup lang="ts">
// Settings · Privacy & Security: App Lock on the left — the switch,
// Unlock Method (System Authentication or PKP Standalone Password, a segmented control), the Standalone Password row
// (the backup under System Authentication, the way in under Standalone Password), Auto-lock After Leaving App,
// what locking hides, Lock PKP Now — and on the right what stays private,
// folded away once the canvas is too narrow for both. Every change to the
// lock is proven with the way in now: Go asks the system, or the Standalone Password is
// asked in AppLockPasswordSheet (the store's flows open it). Below the lock,
// Usage Statistics: the default-on daily active installation switch and its disclosure.
import { computed, nextTick, onMounted, ref } from "vue";
import Icon from "./Icon.vue";
import SelectMenu, { type SelectOption } from "./SelectMenu.vue";
import Toggle from "./Toggle.vue";
import { onMac, onWindows } from "../dom";
import { t } from "../i18n";
import { vSlide } from "../slide";
import {
  APP_LOCK_PRESETS,
  MAX_APP_LOCK_DELAY,
  appLock,
  appLockSheet,
  lockDelayLabel,
  lockNow,
  manageLockPassword,
  setAppLockDelay,
  setAppLockEnabled,
  setAppLockMethod,
  setTelemetryEnabled,
  loadTelemetry,
  telemetry,
} from "../store";

onMounted(() => void loadTelemetry());

const state = computed(() => appLock.state);
const enabled = computed(() => !!state.value?.enabled);
const asking = computed(() => !!state.value?.authenticating);
const byPassword = computed(() => state.value?.method === "password");
const busy = computed(() => asking.value || !!appLockSheet.mode);

const methodNote = computed(() => {
  if (byPassword.value) return t("只用这台电脑上单独设的 PKP 密码来解锁");
  if (state.value && !state.value.available)
    return t("系统验证不可用：{reason}。可以改用独立密码", { reason: state.value.unavailable || t("这台电脑无法弹出系统身份验证") });
  return onWindows ? t("系统验证：Windows Hello（PIN、指纹或面部）")
    : onMac ? t("系统验证：Touch ID 或这台 Mac 的登录密码")
    : t("系统验证：由桌面环境通过 polkit 验证当前系统账户");
});

const passwordTitle = computed(() => (byPassword.value ? t("PKP 独立密码") : t("备用独立密码")));
const passwordNote = computed(() => {
  const has = !!state.value?.hasPassword;
  if (byPassword.value) return has ? t("已设好，锁屏时直接输密码解锁") : t("开启应用锁时顺便设一个");
  return has ? t("已设好，系统验证用不了的时候拿它兜底") : t("选填，万一系统验证用不了时有个退路");
});

/* ---------- Auto-lock After Leaving App: presets, the saved custom value, Custom… ---------- */

const CUSTOM = -1;
const delay = computed(() => state.value?.delay ?? 5);
const delayOptions = computed<SelectOption<number>[]>(() => {
  const list = APP_LOCK_PRESETS.map((m) => ({ value: m, label: lockDelayLabel(m) }));
  // A typed value stays in the menu, where the face can name it.
  if (!APP_LOCK_PRESETS.includes(delay.value)) list.push({ value: delay.value, label: lockDelayLabel(delay.value) });
  list.push({ value: CUSTOM, label: t("自定义…") });
  return list;
});

const editing = ref(false);
// v-model on a number field hands back a number once typed into, "" when cleared.
const draft = ref<string | number>("");
const field = ref<HTMLInputElement | null>(null);
const draftMinutes = computed(() => Number(draft.value));
const draftError = computed(() => {
  const m = draftMinutes.value;
  if (String(draft.value).trim() === "" || !Number.isInteger(m) || m < 1 || m > MAX_APP_LOCK_DELAY) {
    return t("请输入 1–{max} 之间的整数分钟", { max: MAX_APP_LOCK_DELAY });
  }
  return "";
});

async function pickDelay(value: number) {
  if (value !== CUSTOM) {
    editing.value = false;
    void setAppLockDelay(value);
    return;
  }
  draft.value = String(delay.value > 0 ? delay.value : 45);
  editing.value = true;
  await nextTick();
  field.value?.focus();
  field.value?.select();
}

async function saveCustom() {
  if (draftError.value) return;
  if (await setAppLockDelay(draftMinutes.value)) editing.value = false;
}

const note = computed(() => {
  if (asking.value) return t("请在系统弹出的窗口中完成验证");
  return appLock.note;
});
</script>

<template>
  <div class="privacy">
    <h2>{{ t("应用锁") }}</h2>
    <p class="lead">{{ t("人暂时离开电脑时，先锁住 PKP 里的文件、分享和播放界面，传输还是会照常跑。") }}</p>

    <div class="grid">
      <section class="column" :aria-label="t('应用锁设置')">
        <div class="lock-card">
          <div class="card-head">
            <span class="tile" aria-hidden="true"><Icon name="lock" :size="23" /></span>
            <div class="head-text">
              <p>{{ t("可以用电脑本身的系统验证，也可以另设一个 PKP 独立密码") }}</p>
            </div>
            <Toggle :model-value="enabled" :label="t('开启应用锁')" :disabled="busy || !state || (!enabled && !byPassword && !state.available)" @update:model-value="setAppLockEnabled" />
          </div>
          <p v-if="note" class="note" role="status">{{ note }}</p>

          <div class="divider"></div>

          <div class="row">
            <span class="small-icon" aria-hidden="true"><Icon name="key" :size="17" /></span>
            <div class="row-text">
              <strong>{{ t("解锁方式") }}</strong>
              <small :class="{ warn: !byPassword && state && !state.available }">{{ methodNote }}</small>
            </div>
            <div v-slide class="seg glass-well" role="radiogroup" :aria-label="t('解锁方式')">
              <button
                type="button"
                role="radio"
                :aria-checked="!byPassword"
                :class="{ on: !byPassword }"
                :disabled="busy || !state?.available"
                @click="setAppLockMethod('system')"
              >
                {{ t("系统验证") }}
              </button>
              <button
                type="button"
                role="radio"
                :aria-checked="byPassword"
                :class="{ on: byPassword }"
                :disabled="busy || !state"
                @click="setAppLockMethod('password')"
              >
                {{ t("独立密码") }}
              </button>
            </div>
          </div>

          <div class="row">
            <span class="small-icon" aria-hidden="true"><Icon name="more" :size="17" /></span>
            <div class="row-text">
              <strong>{{ passwordTitle }}</strong>
              <small>{{ passwordNote }}</small>
            </div>
            <button type="button" class="manage" :disabled="busy || !state" @click="manageLockPassword">
              {{ state?.hasPassword ? t("更改密码") : t("设置密码") }}
            </button>
          </div>


          <div class="row">
            <span class="small-icon" aria-hidden="true"><Icon name="clock" :size="17" /></span>
            <div class="row-text">
              <strong>{{ t("离开应用后自动锁定") }}</strong>
              <small>{{ t("系统锁屏和关闭 PKP 窗口时始终立即锁定") }}</small>
            </div>
            <span class="select glass-well" :class="{ off: !enabled }">
              <SelectMenu
                class="face"
                :model-value="delay"
                :options="delayOptions"
                :label="t('离开应用后自动锁定时间')"
                :disabled="!enabled"
                @update:model-value="pickDelay"
              />
              <Icon name="chevronDown" :size="14" />
            </span>
          </div>

          <form v-if="editing && enabled" class="custom" @submit.prevent="saveCustom">
            <label for="lock-minutes">{{ t("自定义时间") }}</label>
            <span class="minutes">
              <input
                id="lock-minutes"
                ref="field"
                v-model="draft"
                type="number"
                inputmode="numeric"
                min="1"
                :max="MAX_APP_LOCK_DELAY"
                step="1"
                :aria-invalid="!!draftError"
                aria-describedby="lock-minutes-hint"
                @keydown.esc.stop.prevent="editing = false"
              />
              <em>{{ t("分钟") }}</em>
            </span>
            <span id="lock-minutes-hint" class="custom-hint" :class="{ bad: !!draftError }">
              {{ draftError || t("离开 {when}锁定", { when: lockDelayLabel(draftMinutes) }) }}
            </span>
            <span class="custom-actions">
              <button type="button" @click="editing = false">{{ t("取消") }}</button>
              <button type="submit" class="primary" :disabled="!!draftError">{{ t("保存") }}</button>
            </span>
          </form>

          <div class="row last">
            <span class="small-icon" aria-hidden="true"><Icon name="laptop" :size="17" /></span>
            <div class="row-text">
              <strong>{{ t("锁定时隐藏内容") }}</strong>
              <small>{{ state?.screenshotProtectionAvailable ? (onMac ? t("窗口预览和菜单栏不展示账号与私人文件名") : t("窗口预览和系统托盘不展示账号与私人文件名")) : t("锁屏后不会露出文件、账号和任务详情，解锁就能回到刚才的位置。") }}</small>
            </div>
            <span class="always">{{ t("始终开启") }}</span>
          </div>
          <p v-if="state && !state.screenshotProtectionAvailable" class="note" role="note">{{ t("Linux 无法保证阻止截图、录屏或桌面窗口预览。应用锁会隐藏 PKP 内的内容，但不能清除桌面环境已保存的画面。") }}</p>
        </div>

        <div class="section-title"><span>{{ t("快速操作") }}</span><span class="line" aria-hidden="true"></span></div>
        <button type="button" class="action-card" :disabled="!enabled" @click="lockNow">
          <span class="action-icon" aria-hidden="true"><Icon name="lock" :size="17" /></span>
          <span class="action-text">
            <strong>{{ t("现在锁定 PKP") }}</strong>
            <small>{{ enabled ? t("立即遮住文件与账号，传输保持运行") : t("回到应用时再验证；未开启应用锁时不可用") }}</small>
          </span>
          <Icon class="chevron" name="chevronRight" :size="15" />
        </button>

        <div class="section-title"><span>{{ t("使用统计") }}</span><span class="line" aria-hidden="true"></span></div>
        <div class="lock-card stats-card">
          <div class="card-head">
            <span class="tile" aria-hidden="true"><Icon name="gauge" :size="23" /></span>
            <div class="head-text">
              <h3>{{ t("发送匿名使用统计") }}</h3>
              <p>{{ t("只估算每天大概有多少人打开过 PKP，不会记录你点过什么、用过几次") }}</p>
            </div>
            <Toggle
              :model-value="!!telemetry.state?.enabled"
              :label="t('发送匿名使用统计')"
              :disabled="telemetry.saving || !telemetry.state"
              @update:model-value="setTelemetryEnabled"
            />
          </div>
          <p class="note">{{ t("默认开启；关闭统计不影响正常使用。") }}</p>
          <div class="divider"></div>
          <div class="row">
            <span class="small-icon" aria-hidden="true"><Icon name="check" :size="17" /></span>
            <div class="row-text">
              <strong>{{ t("会发送") }}</strong>
              <small>{{ t("仅含使用日期、PKP 版本、系统架构、界面语言，以及一个和账号无关的随机安装编号") }}</small>
            </div>
          </div>
          <div class="row last">
            <span class="small-icon" aria-hidden="true"><Icon name="eyeOff" :size="17" /></span>
            <div class="row-text">
              <strong>{{ t("不会发送") }}</strong>
              <small>{{ t("功能操作、使用次数、运行日志、账号、文件名、路径、搜索词或文件内容") }}</small>
            </div>
          </div>
          <p class="note">{{ t("随时可以关掉；关掉就会删掉本地编号和待发记录（已经发出的服务端数据没法撤回），下次重开会生成新编号。") }}</p>
          <p v-if="telemetry.error" class="note" role="alert">{{ telemetry.error }}</p>
        </div>
      </section>

      <aside class="side">
        <div class="private-card">
          <h3>{{ t("人离开电脑，") }}{{ t("界面先锁上。") }}</h3>
          <p>{{ t("锁屏后不会露出文件、账号和任务详情，解锁就能回到刚才的位置。") }}</p>
          <p class="foot">{{ t("只在这台电脑生效") }}</p>
        </div>
        <p class="footnote">
          {{ t("应用锁只是锁定 PKP 界面，不会退出账号，也不会加密下到硬盘里的文件。独立密码跟 PikPak 账号密码无关，忘了没法找回，只能退出登录后清掉本地数据重设。") }}
        </p>
      </aside>
    </div>
  </div>
</template>

<style scoped>
.privacy {
  container: privacy / inline-size;
  --soft: color-mix(in srgb, var(--fg) 2%, transparent);
  --hair: var(--line);
}

h2 {
  margin: 0 0 5px;
  font-size: 21px;
  font-weight: 600;
  line-height: 30px;
}

.lead {
  margin: 0;
  max-width: 680px;
  color: var(--fg-dim);
  font-size: 14px;
  line-height: 1.5;
}

.grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(220px, 280px);
  gap: 24px;
  max-width: 1060px;
  margin-top: 20px;
}

.column,
.side {
  min-width: 0;
}

.lock-card {
  border-radius: 10px;
  border: 1px solid var(--line);
  background: var(--soft);
}


.card-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  padding: 16px 18px;
}

.tile {
  width: 36px;
  height: 36px;
  flex: none;
  display: grid;
  place-items: center;
  border-radius: 8px;
  background: var(--soft);
  color: var(--fg-dim);
}

.head-text {
  flex: 1 1 180px;
  min-width: 0;
}

.head-text h3 {
  margin: 0 0 2px;
  font-size: 15px;
  font-weight: 650;
}

.head-text p {
  margin: 0;
  color: var(--fg-dim);
  font-size: 13px;
  line-height: 1.5;
}

.note {
  margin: -4px 18px 14px 66px;
  color: var(--warning);
  font-size: 13px;
}

.stats-card .note {
  color: var(--fg-dim);
  line-height: 1.5;
}

.stats-card .note[role="alert"] {
  color: var(--warning);
}

.divider {
  height: 1px;
  margin: 0 18px;
  background: var(--hair);
}

.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  margin: 0 18px;
  padding: 15px 0;
  border-bottom: 1px solid var(--hair);
}

/* A wide control (a long Unlock Method pair) drops under the text, at the
   right, before the text is squeezed into a narrow column. */
.row > :last-child {
  margin-left: auto;
}

.row.last {
  border-bottom: 0;
}

/* Custom: under the delay row, lined up with its text. */
.custom {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px 12px;
  margin: 0 18px;
  padding: 12px 0 14px 42px;
  border-bottom: 1px solid var(--hair);
}

.custom label {
  font-size: 13px;
  font-weight: 600;
}

.minutes {
  display: flex;
  align-items: center;
  gap: 8px;
}

.minutes input {
  width: 96px;
  font-size: 14px;
  font-variant-numeric: tabular-nums;
}

.minutes input[aria-invalid="true"] {
  border-color: var(--danger);
}

.minutes em {
  color: var(--fg-dim);
  font-size: 13px;
  font-style: normal;
}

.custom-hint {
  flex: 1 1 160px;
  min-width: 0;
  color: var(--fg-dim);
  font-size: 12px;
}

.custom-hint.bad {
  color: var(--danger);
}

.custom-actions {
  display: flex;
  gap: 8px;
  margin-left: auto;
}

.custom-actions button {
  min-height: 34px;
  padding: 0 14px;
  font-size: 13px;
}

.small-icon {
  width: 30px;
  height: 30px;
  flex: none;
  display: grid;
  place-items: center;
  color: var(--fg-dim);
}

.row-text {
  flex: 1 1 200px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.row-text strong {
  font-size: 14px;
  font-weight: 600;
}

.row-text small {
  color: var(--fg-dim);
  font-size: 13px;
  line-height: 1.5;
}

/* The settings page's select (SettingsView .select), at its compact size. */
.select {
  position: relative;
  flex: none;
  display: flex;
  align-items: center;
  min-width: 128px;
  border-radius: 7px;
}

.select.off {
  opacity: 0.45;
}

.face {
  flex: 1;
  min-width: 0;
  min-height: 38px;
  padding: 0 32px 0 12px;
  line-height: 38px;
  font-size: 13px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.select :deep(svg) {
  position: absolute;
  right: 11px;
  pointer-events: none;
  color: var(--fg-dim);
}

/* Unlock Method: the settings page's segmented control (SettingsView .seg). */
.seg {
  display: flex;
  flex: none;
  padding: 4px;
  border-radius: 7px;
}

.seg button {
  min-height: 32px;
  padding: 0 13px;
  border: none;
  border-radius: 5px;
  background: none;
  color: var(--fg-dim);
  font-size: 13px;
  font-weight: 500;
}

.seg::before {
  background: var(--primary);
}

.seg button.on {
  background: transparent;
  color: var(--on-primary);
  font-weight: 600;
}

.manage {
  flex: none;
  min-height: 38px;
  padding: 0 16px;
  border-radius: 7px;
  font-size: 13px;
}

.row-text small.warn {
  color: var(--warning);
}

.always {
  flex: none;
  color: var(--success);
  font-size: 12px;
  white-space: nowrap;
}

.section-title {
  display: flex;
  align-items: center;
  gap: 14px;
  margin: 24px 1px 10px;
  color: var(--fg-dim);
  font-size: 13px;
  font-weight: 600;
}

.line {
  flex: 1;
  height: 1px;
  background: var(--hair);
}

.action-card {
  width: 100%;
  min-height: 62px;
  justify-content: flex-start;
  gap: 12px;
  padding: 12px 15px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--soft);
  color: var(--fg);
  text-align: left;
  white-space: normal;
}

.action-card:hover:not(:disabled) {
  background: color-mix(in srgb, var(--fg) 8%, transparent);
}

.action-card:disabled {
  opacity: 0.6;
}

.action-icon {
  width: 30px;
  height: 30px;
  flex: none;
  display: grid;
  place-items: center;
  color: var(--fg-dim);
}

.action-text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.action-text strong {
  font-size: 14px;
  font-weight: 600;
}

.action-text small {
  color: var(--fg-dim);
  font-size: 13px;
  line-height: 1.5;
}

.chevron {
  flex: none;
  color: var(--fg-dim);
}

.private-card {
  padding: 2px 0 0 16px;
  border-left: 2px solid var(--line);
}

.private-card h3 {
  margin: 0 0 8px;
  font-size: 14px;
  font-weight: 600;
}

.private-card p {
  margin: 0;
  color: var(--fg-dim);
  font-size: 13px;
  line-height: 1.6;
}

.private-card .foot {
  margin-top: 12px;
  font-size: 12px;
}

.footnote {
  margin: 16px 0 0 18px;
  color: var(--fg-dim);
  font-size: 12px;
  line-height: 1.7;
}

@container privacy (max-width: 760px) {
  .grid {
    grid-template-columns: minmax(0, 1fr);
    margin-top: 20px;
  }

}

@container privacy (max-width: 460px) {
  .card-head {
    gap: 10px;
    padding: 17px 15px;
  }

  .tile {
    width: 32px;
    height: 32px;
  }

  .note {
    margin: -6px 15px 14px;
  }

  .divider {
    margin: 0 15px;
  }

  .row {
    flex-wrap: wrap;
    gap: 9px;
    margin: 0 15px;
  }

  .row-text {
    flex-basis: calc(100% - 39px);
  }

  .select,
  .seg,
  .manage {
    margin-left: 39px;
  }

  .custom {
    margin: 0 15px;
    padding-left: 39px;
  }
}
</style>
