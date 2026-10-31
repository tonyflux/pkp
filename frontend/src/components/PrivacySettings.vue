<script setup lang="ts">
// Settings · Privacy & Security: App Lock on the left — the switch,
// Unlock Method (System Authentication or PKP Standalone Password, a segmented control), the Standalone Password row
// (the backup under System Authentication, the way in under Standalone Password), Auto-lock After Leaving App,
// what locking hides, Lock PKP Now — and on the right what stays private,
// folded away once the canvas is too narrow for both. Every change to the
// lock is proven with the way in now: Go asks the system, or the Standalone Password is
// asked in AppLockPasswordSheet (the store's flows open it). Below the lock,
// Usage Statistics: the Anonymous Usage Statistics switch and what it does and does not send.
import { computed, nextTick, onMounted, ref } from "vue";
import Icon from "./Icon.vue";
import SelectMenu, { type SelectOption } from "./SelectMenu.vue";
import Toggle from "./Toggle.vue";
import { onWindows } from "../dom";
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
  if (byPassword.value) return t("独立密码：只认这台电脑上的 PKP 密码，不依赖系统账户");
  if (state.value && !state.value.available)
    return t("系统验证不可用：{reason}。可以改用独立密码", { reason: state.value.unavailable || t("这台电脑无法弹出系统身份验证") });
  return onWindows ? t("系统验证：Windows Hello（PIN、指纹或面部）") : t("系统验证：Touch ID 或这台 Mac 的登录密码");
});

const passwordTitle = computed(() => (byPassword.value ? t("PKP 独立密码") : t("备用独立密码")));
const passwordNote = computed(() => {
  const has = !!state.value?.hasPassword;
  if (byPassword.value) return has ? t("已设置；锁屏直接输入密码解锁") : t("必填；开启应用锁时设置");
  return has ? t("已设置；系统验证不可用或失败时改用它解锁") : t("可选；系统验证不可用时没有后备入口");
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
    <p class="eyebrow"><Icon name="shield" :size="16" />PRIVACY &amp; SECURITY</p>
    <h2>{{ t("把云盘留在你的视线里。") }}</h2>
    <p class="lead">{{ t("离开电脑时，锁住 PKP 里的文件、分享和播放画面。已有传输可以继续运行。") }}</p>

    <div class="grid">
      <section class="column" :aria-label="t('应用锁设置')">
        <div class="lock-card">
          <div class="card-head">
            <span class="tile" aria-hidden="true"><Icon name="lock" :size="23" /></span>
            <div class="head-text">
              <h3>{{ t("应用锁") }}</h3>
              <p>{{ t("解锁方式二选一：这台电脑的系统验证，或 PKP 独立密码") }}</p>
            </div>
            <Toggle :model-value="enabled" :label="t('开启应用锁')" :disabled="busy || !state" @update:model-value="setAppLockEnabled" />
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
                :disabled="busy || !state"
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
              <small>{{ onWindows ? t("窗口预览和系统托盘不展示账号与私人文件名") : t("窗口预览和菜单栏不展示账号与私人文件名") }}</small>
            </div>
            <span class="always">{{ t("始终开启") }}</span>
          </div>
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
              <p>{{ t("每天用了哪些功能、各用了几次，帮助决定先改进什么") }}</p>
            </div>
            <Toggle
              :model-value="!!telemetry.state?.enabled"
              :label="t('发送匿名使用统计')"
              :disabled="telemetry.saving || !telemetry.state"
              @update:model-value="setTelemetryEnabled"
            />
          </div>
          <div class="divider"></div>
          <div class="row">
            <span class="small-icon" aria-hidden="true"><Icon name="check" :size="17" /></span>
            <div class="row-text">
              <strong>{{ t("会发送") }}</strong>
              <small>{{ t("功能使用次数、PKP 版本、系统和界面语言，以及一个随机生成、与账号无关的编号") }}</small>
            </div>
          </div>
          <div class="row last">
            <span class="small-icon" aria-hidden="true"><Icon name="eyeOff" :size="17" /></span>
            <div class="row-text">
              <strong>{{ t("不会发送") }}</strong>
              <small>{{ t("文件名、路径、搜索内容、播放内容、账号和 PikPak 用户编号。关闭后，这台电脑的编号和未发送的统计会一并删除") }}</small>
            </div>
          </div>
        </div>
      </section>

      <aside class="side">
        <div class="private-card">
          <span class="eyebrow small">WHAT STAYS PRIVATE</span>
          <div class="visual" aria-hidden="true">
            <div class="halo"></div>
            <div class="shield"><Icon name="shield" :size="41" /></div>
            <i class="orbit one"></i>
            <i class="orbit two"></i>
          </div>
          <h3>{{ t("离开屏幕，") }}<br />{{ t("内容不必跟着暴露。") }}</h3>
          <p>{{ t("锁屏不显示文件、账号和任务详情；解锁后回到之前的位置。") }}</p>
          <div class="foot"><i class="dot" aria-hidden="true"></i>{{ t("只在这台电脑生效") }}</div>
        </div>
        <p class="footnote">
          {{ t("应用锁不等于退出登录，也不加密已下载到电脑的文件。独立密码与 PikPak 账号密码无关、只存在这台电脑，忘记后无法找回，只能退出登录清除本机数据后重设。") }}
        </p>
      </aside>
    </div>
  </div>
</template>

<style scoped>
.privacy {
  container: privacy / inline-size;
  --tile-from: #5cb8d2;
  --tile-to: #2c7092;
  --soft: color-mix(in srgb, var(--fg) 4%, transparent);
  --hair: var(--line);
}

.eyebrow {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0;
  color: var(--accent);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.18em;
}

.eyebrow.small {
  font-size: 10px;
}

h2 {
  margin: 12px 0 10px;
  font-size: clamp(25px, 2.6vw, 34px);
  font-weight: 650;
  line-height: 1.2;
  letter-spacing: -0.03em;
}

.lead {
  margin: 0;
  color: var(--fg-dim);
  font-size: 15px;
}

.grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(240px, 300px);
  gap: 24px;
  max-width: 1060px;
  margin-top: 32px;
}

.column,
.side {
  min-width: 0;
}

.lock-card {
  border-radius: 20px;
  border: 1px solid var(--line);
  background: var(--soft);
  box-shadow: inset 0 1px 0 color-mix(in srgb, var(--glass-rim) 30%, transparent);
}

/* Usage Statistics is not part of the lock: its own tile colour. */
.stats-card {
  --tile-from: #8d84ee;
  --tile-to: #5149b3;
}

.card-head {
  display: flex;
  align-items: center;
  gap: 15px;
  padding: 24px;
}

.tile {
  width: 47px;
  height: 47px;
  flex: none;
  display: grid;
  place-items: center;
  border-radius: 14px;
  background: linear-gradient(145deg, var(--tile-from), var(--tile-to));
  color: #f5fdff;
  box-shadow: 0 7px 20px rgba(27, 138, 166, 0.2);
}

.head-text {
  flex: 1;
  min-width: 0;
}

.head-text h3 {
  margin: 0 0 2px;
  font-size: 17px;
  font-weight: 650;
}

.head-text p {
  margin: 0;
  color: var(--fg-dim);
  font-size: 13px;
}

.note {
  margin: -8px 24px 18px 86px;
  color: var(--warning);
  font-size: 13px;
}

.divider {
  height: 1px;
  margin: 0 24px;
  background: var(--hair);
}

.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 14px;
  margin: 0 24px;
  padding: 20px 0;
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
  margin: 0 24px;
  padding: 16px 0 18px 49px;
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
  width: 35px;
  height: 35px;
  flex: none;
  display: grid;
  place-items: center;
  border-radius: 10px;
  border: 1px solid var(--line);
  color: var(--accent);
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
  font-size: 12px;
}

/* The settings page's select (SettingsView .select), at its compact size. */
.select {
  position: relative;
  flex: none;
  display: flex;
  align-items: center;
  min-width: 128px;
  border-radius: 10px;
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
  border-radius: 12px;
}

.seg button {
  min-height: 32px;
  padding: 0 13px;
  border: none;
  border-radius: 9px;
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
  border-radius: 10px;
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
  margin: 28px 1px 12px;
  color: var(--fg-dim);
  font-size: 13px;
}

.line {
  flex: 1;
  height: 1px;
  background: var(--hair);
}

.action-card {
  width: 100%;
  min-height: 75px;
  justify-content: flex-start;
  gap: 14px;
  padding: 14px 17px;
  border: 1px solid var(--line);
  border-radius: 17px;
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
  width: 36px;
  height: 36px;
  flex: none;
  display: grid;
  place-items: center;
  border-radius: 11px;
  background: var(--accent-soft);
  color: var(--accent);
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
  font-size: 12px;
}

.chevron {
  flex: none;
  color: var(--fg-dim);
}

/* The decorative card is the design's deep teal in both themes: it is the
   lock screen in miniature. */
.private-card {
  position: relative;
  min-height: 420px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: 23px;
  border-radius: 20px;
  border: 1px solid rgba(173, 228, 247, 0.19);
  background:
    radial-gradient(ellipse at 55% 30%, rgba(98, 187, 206, 0.22), transparent 51%),
    linear-gradient(165deg, #173b50, #16334a 64%, #172d43);
  color: #f4f8fd;
}

.private-card .eyebrow {
  color: #86cfe9;
}

.visual {
  position: relative;
  height: 168px;
  display: grid;
  place-items: center;
}

.halo {
  position: absolute;
  width: 160px;
  height: 160px;
  border-radius: 50%;
  border: 1px solid rgba(182, 236, 248, 0.09);
  background: radial-gradient(circle, rgba(104, 204, 222, 0.19), transparent 72%);
}

.shield {
  width: 89px;
  height: 89px;
  display: grid;
  place-items: center;
  border-radius: 27px;
  border: 1px solid rgba(206, 246, 253, 0.38);
  background: linear-gradient(145deg, rgba(175, 232, 243, 0.33), rgba(95, 171, 197, 0.13));
  box-shadow:
    0 16px 35px rgba(2, 24, 43, 0.42),
    inset 0 1px rgba(255, 255, 255, 0.35);
  color: #dafaff;
  transform: rotate(-8deg);
}

.shield :deep(svg) {
  transform: rotate(8deg);
}

.orbit {
  position: absolute;
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: #ace9ed;
  box-shadow: 0 0 14px #b2f5f0;
}

.orbit.one {
  left: 24%;
  top: 31%;
}

.orbit.two {
  right: 20%;
  bottom: 28%;
  width: 3px;
  height: 3px;
}

.private-card h3 {
  margin: 0 0 9px;
  font-size: 21px;
  line-height: 1.35;
  letter-spacing: -0.02em;
}

.private-card p {
  margin: 0;
  color: #bed4e1;
  font-size: 13px;
  line-height: 1.7;
}

.foot {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: auto;
  padding-top: 16px;
  border-top: 1px solid rgba(216, 244, 252, 0.14);
  color: #c5e6e8;
  font-size: 12px;
}

.dot {
  width: 7px;
  height: 7px;
  flex: none;
  border-radius: 50%;
  background: #88deca;
  box-shadow: 0 0 11px rgba(137, 228, 201, 0.4);
}

.footnote {
  margin: 14px 3px;
  color: var(--fg-dim);
  font-size: 12px;
  line-height: 1.7;
}

/* Too narrow for both columns: the settings stay, the illustration goes
   (the design's ≤1130px window, measured on the canvas). */
@container privacy (max-width: 760px) {
  .grid {
    grid-template-columns: minmax(0, 1fr);
    margin-top: 24px;
  }

  .private-card {
    display: none;
  }

  .footnote {
    margin-top: 0;
  }
}

@container privacy (max-width: 460px) {
  .card-head {
    gap: 10px;
    padding: 17px 15px;
  }

  .tile {
    width: 40px;
    height: 40px;
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
    flex-basis: calc(100% - 44px);
  }

  .select,
  .seg,
  .manage {
    margin-left: 44px;
  }

  .custom {
    margin: 0 15px;
    padding-left: 44px;
  }
}
</style>
