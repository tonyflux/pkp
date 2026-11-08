<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import qrcode from "qrcode-generator";
import { DriveService, type SignInResult } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import { t } from "../i18n";

const props = withDefaults(defineProps<{ account?: string; label?: string; networkLink?: boolean }>(), { account: "", label: "" });
const emit = defineEmits<{ complete: [result: SignInResult]; network: [] }>();
const state = ref("loading");
const image = ref("");
const message = ref("");
const deadline = ref(0);
const now = ref(Date.now());
const remaining = computed(() => Math.max(0, Math.ceil((deadline.value - now.value) / 1000)));
const timeLeft = computed(() => `${Math.floor(remaining.value / 60).toString().padStart(2, "0")}:${(remaining.value % 60).toString().padStart(2, "0")}`);
const terminal = computed(() => ["expired", "denied", "error", "cancelled"].includes(state.value));
const status = computed(() => {
  if (message.value) return message.value;
  switch (state.value) {
    case "scanned": return t("已扫码，请在手机上确认");
    case "expired": return t("二维码已失效");
    case "cancelled":
    case "denied": return t("已取消授权");
    case "network": return image.value ? t("网络连接中断，正在重试") : t("网络连接失败，请重试");
    case "error": return t("暂时无法生成二维码");
    case "complete": return t("登录成功");
    case "loading": return t("正在生成二维码…");
    default: return t("等待扫码");
  }
});
let generation = 0;
let session = "";
let interval = 2;
let pollTimer: ReturnType<typeof setTimeout> | undefined;
let clock: ReturnType<typeof setInterval> | undefined;
let polling = false;
function stopTimers() {
  clearTimeout(pollTimer);
  clearInterval(clock);
}
function cancel(id: string) {
  if (id) void DriveService.CancelQRSignIn(id).catch(() => undefined);
}
function schedule(serial: number) {
  clearTimeout(pollTimer);
  if (!terminal.value && state.value !== "complete" && session) pollTimer = setTimeout(() => void poll(serial), interval * 1000);
}
function expire() {
  generation++;
  state.value = "expired";
  message.value = "";
  stopTimers();
  const id = session;
  session = "";
  cancel(id);
}
async function poll(serial: number) {
  if (serial !== generation || !session || terminal.value) return;
  if (polling) return;
  if (deadline.value <= Date.now()) { expire(); return; }
  polling = true;
  try {
    const result = await DriveService.PollQRSignIn(session);
    if (serial !== generation) return;
    interval = Math.max(1, result.interval || interval);
    state.value = result.state;
    message.value = result.message;
    if (result.state === "complete" && result.result) {
      stopTimers();
      session = "";
      emit("complete", result.result);
      return;
    }
    if (deadline.value <= Date.now()) { expire(); return; }
    if (terminal.value) stopTimers();
  } catch {
    if (serial !== generation) return;
    if (deadline.value <= Date.now()) { expire(); return; }
    message.value = "";
    state.value = "network";
  } finally {
    polling = false;
    if (serial === generation) schedule(serial);
  }
}
async function begin() {
  const serial = ++generation;
  stopTimers();
  cancel(session);
  session = "";
  state.value = "loading";
  message.value = "";
  image.value = "";
  deadline.value = 0;
  try {
    const result = await DriveService.BeginQRSignIn({ account: props.account, label: props.label.trim() });
    if (serial !== generation) { cancel(result.id); return; }
    session = result.id;
    state.value = result.state;
    message.value = result.message;
    interval = Math.max(1, result.interval || 2);
    deadline.value = Date.parse(result.expiresAt);
    if (!result.url || !session || !Number.isFinite(deadline.value)) {
      if (state.value !== "network") state.value = "error";
      return;
    }
    const code = qrcode(0, "M");
    code.addData(result.url, "Byte");
    code.make();
    image.value = code.createDataURL(4, 16);
    now.value = Date.now();
    clock = setInterval(() => {
      now.value = Date.now();
      if (remaining.value === 0) {
        stopTimers();
        // An in-flight poll may already have committed a sign-in.
        if (!polling) expire();
      }
    }, 1000);
    schedule(serial);
  } catch {
    if (serial === generation) state.value = "error";
  }
}
function retry() {
  if (polling) return;
  if (session && deadline.value > Date.now() && state.value === "network") {
    // Retry still observes the server's minimum polling interval.
    schedule(generation);
  } else void begin();
}
onMounted(() => void begin());
onBeforeUnmount(() => { generation++; stopTimers(); cancel(session); session = ""; });
</script>

<template>
  <div class="qr-signin">
    <div class="instruction"><strong>{{ t("打开 PikPak App 扫一扫") }}</strong><p>{{ t("在手机上确认后，自动完成登录") }}</p></div>
    <div class="qr-square" :aria-busy="state === 'loading'">
      <img v-if="image && state !== 'scanned'" :src="image" :alt="t('PikPak 登录二维码')" :class="{ faded: terminal }" />
      <div v-if="state === 'scanned'" class="qr-content scanned"><Icon name="check" :size="44" /><strong>{{ t("扫码成功") }}</strong><span>{{ t("请在 PikPak App 中确认登录") }}</span></div>
      <div v-else-if="terminal || (state === 'network' && !image)" class="qr-content mask"><span>{{ status }}</span><button type="button" class="primary" @click="retry">{{ t("刷新二维码") }}</button></div>
      <Orbit v-else-if="state === 'loading'" :size="26" :label="status" />
    </div>
    <div class="qr-status"><span class="status-label" role="status" aria-live="polite"><i v-if="state === 'waiting'" class="waiting-dot" />{{ status }}</span><small v-if="deadline && !terminal && state !== 'complete'" :aria-label="t('{n} 秒后失效', { n: remaining })"><Icon name="clock" :size="12" />{{ timeLeft }}</small></div>
    <div v-if="state === 'network'" class="network-actions"><button type="button" @click="retry">{{ t("重试连接") }}</button><button v-if="networkLink" type="button" @click="emit('network')">{{ t("检查网络与代理") }}</button></div>
    <p class="privacy"><Icon name="lock" :size="13" />{{ t("无需向 PKP 提供账号密码") }}</p>
  </div>
</template>

<style scoped>
.qr-signin { display: flex; flex-direction: column; align-items: center; gap: 16px; text-align: center; min-width: 0; }
.instruction strong { font-size: 19px; font-weight: 600; }
.instruction p { margin: 6px 0 0; font-size: 12px; color: var(--fg-dim); }
.qr-square { position: relative; display: grid; place-items: center; width: 206px; height: 206px; max-width: 100%; background: #fff; border: 1px solid rgba(0,0,0,.07); border-radius: 14px; overflow: hidden; color: #192532; }
.qr-square img { width: 100%; height: 100%; image-rendering: pixelated; }
.qr-square img.faded { opacity: .12; }
.qr-content { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; padding: 14px; font-size: 13px; }
.scanned { color: #27865a; }
.scanned span { color: #66717d; font-size: 12px; }
.mask { background: rgba(255,255,255,.8); }
.mask button { border-radius: 8px; padding: 8px 14px; }
.qr-status { width: 206px; max-width: 100%; min-height: 37px; display: flex; align-items: flex-start; justify-content: space-between; gap: 10px; font-size: 12px; text-align: left; }
.status-label { display: inline-flex; align-items: center; gap: 6px; min-width: 0; }
.waiting-dot { flex: none; width: 6px; height: 6px; border-radius: 50%; background: var(--accent); }
.qr-status small { display: inline-flex; align-items: center; gap: 4px; white-space: nowrap; font-variant-numeric: tabular-nums; }
.qr-status small, .privacy { color: var(--fg-dim); font-size: 11px; }
.privacy { display: flex; align-items: center; justify-content: center; gap: 5px; margin: 0; }
.network-actions { display: flex; flex-wrap: wrap; justify-content: center; gap: 10px; }
.network-actions button { min-height: 0; border: 0; border-radius: 6px; background: none; color: var(--accent); font-size: 12px; line-height: 1.5; padding: 2px 4px; white-space: normal; }
.network-actions button:hover:not(:disabled) { background: none; text-decoration: underline; text-underline-offset: 3px; }
@media (max-height: 740px) {
  .qr-signin { gap: 12px; }
  .qr-status { min-height: 22px; }
}
</style>
