<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { Call } from "@wailsio/runtime";
import AssistantSheet from "./AssistantSheet.vue";
import { t } from "../i18n";

interface CrashReport { id: string; text: string; path: string; truncated: boolean }
const props = defineProps<{ active: boolean }>();
const emit = defineEmits<{ (e: "open-change", open: boolean): void; (e: "sent"): void }>();
const service = "github.com/tonyflux/pkp/internal/services.CrashFeedbackService.";
const report = ref<CrashReport | null>(null);
const text = ref("");
const busy = ref(false);
const error = ref("");
const checked = ref(false);
const tooLarge = computed(() => {
  const encoder = new TextEncoder();
  return encoder.encode(text.value).length > 120 * 1024 ||
    encoder.encode(JSON.stringify({ id: "00000000-0000-4000-8000-000000000000", report: text.value }).replace(/\u2028/g, "\\u2028").replace(/\u2029/g, "\\u2029")).length > 128 * 1024;
});

async function pending() {
  if (!props.active || busy.value || report.value || checked.value) return;
  busy.value = true;
  let failed = false;
  try {
    const next: CrashReport | null = await Call.ByName(service + "Pending");
    checked.value = true;
    if (!props.active) {
      checked.value = false;
      return;
    }
    if (next) {
      report.value = next;
      text.value = next.text;
    }
  } catch {
    failed = true;
  } finally {
    busy.value = false;
    if (!failed && props.active && !checked.value) void pending();
  }
}

async function finish(send: boolean) {
  if (!report.value || busy.value || (send && tooLarge.value)) return;
  busy.value = true;
  error.value = "";
  try {
    if (send) await Call.ByName(service + "Send", report.value.id, text.value);
    else await Call.ByName(service + "Dismiss", report.value.id);
    if (send) emit("sent");
    report.value = null;
    text.value = "";
    checked.value = false;
  } catch {
    error.value = t("崩溃报告操作失败，请重试。");
  } finally {
    busy.value = false;
  }
  if (!report.value) void pending();
}
watch(() => props.active, (active) => {
  if (active) void pending();
  else {
    report.value = null;
    text.value = "";
    error.value = "";
    checked.value = false;
  }
}, { immediate: true });
watch(() => props.active && (!!report.value || (busy.value && !checked.value)), (open) => emit("open-change", open), { immediate: true });
</script>

<template>
  <AssistantSheet v-if="active && report" labelledby="crash-feedback-title" :dismissable="!busy" @close="finish(false)">
    <span class="sheet-eyebrow">{{ t("崩溃反馈") }}</span>
    <h2 id="crash-feedback-title">{{ t("上次运行时发生了崩溃") }}</h2>
    <p class="sheet-lede">{{ t("放心，没点发送前什么都不会传；点击后报告会经由 Cloudflare 发给开发者。") }}</p>
    <p class="sheet-small">{{ t("报告里可能带有文件路径、账号之类的隐私信息；下面文本框可以直接删改，只会发出你改完后的内容。") }}</p>
    <p class="sheet-small">{{ t("这是手动反馈通道，跟匿名统计无关，关掉统计后也一样能发。") }}</p>
    <p class="sheet-small">{{ t("报告只给开发者排查问题用，服务端会在 29 天后自动清理；发出去后没法单方面撤回。") }}</p>
    <p v-if="report.truncated" class="sheet-small">{{ t("原始日志太长，这里只截了一部分显示；你电脑上的完整日志不会变。") }}</p>
    <label for="crash-feedback-text">{{ t("要发送的报告") }}</label>
    <textarea id="crash-feedback-text" v-model="text" :disabled="busy" spellcheck="false" />
    <p v-if="tooLarge" class="sheet-error" role="alert">{{ t("报告或编码后的请求过大，请删减后再发送（报告最多 120 KiB，请求最多 128 KiB）。") }}</p>
    <p v-if="error" class="sheet-error" role="alert">{{ error }}</p>
    <p class="sheet-small">{{ t("不发或直接关闭不会删掉本地日志，下次也不会再弹这条提醒。") }}</p>
    <div class="sheet-actions">
      <button class="sheet-btn" type="button" data-autofocus :disabled="busy" @click="finish(false)">{{ t("不发送") }}</button>
      <button class="sheet-btn primary" type="button" :disabled="busy || tooLarge || !text.trim()" @click="finish(true)">{{ t("发送报告") }}</button>
    </div>
  </AssistantSheet>
</template>

<style scoped>
label { display: block; margin-top: 16px; font-size: 12px; font-weight: 650; }
textarea { display: block; width: 100%; min-height: 180px; margin-top: 6px; padding: 10px; box-sizing: border-box; resize: vertical; color: var(--fg); background: var(--bg); border: 1px solid var(--line); border-radius: 8px; font: 12px/1.5 monospace; user-select: text; }
</style>
