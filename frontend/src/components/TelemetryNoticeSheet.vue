<script setup lang="ts">
import AssistantSheet from "./AssistantSheet.vue";
import { t } from "../i18n";
import { acknowledgeTelemetryNotice, telemetry } from "../store";
</script>

<template>
  <AssistantSheet labelledby="telemetry-notice-title" :dismissable="!telemetry.saving" @close="acknowledgeTelemetryNotice">
    <span class="sheet-eyebrow">{{ t("使用统计") }}</span>
    <h2 id="telemetry-notice-title">{{ t("匿名使用统计已默认开启") }}</h2>
    <p class="sheet-lede">{{ t("仅统计每日启动情况，不会记录您的具体操作内容或使用频次") }}</p>
    <dl class="disclosure">
      <div>
        <dt>{{ t("会发送") }}</dt>
        <dd>{{ t("仅含使用日期、PKP 版本、系统架构、界面语言，以及一个和账号无关的随机安装编号") }}</dd>
      </div>
      <div>
        <dt>{{ t("不会发送") }}</dt>
        <dd>{{ t("功能操作、使用次数、运行日志、账号、文件名、路径、搜索词或文件内容") }}</dd>
      </div>
    </dl>
    <p class="sheet-small">{{ t("该功能默认开启，您可随时前往「设置 → 隐私与安全」关闭（关闭此提示不会停用统计）。") }}</p>
    <p class="sheet-small">{{ t("可随时关闭；关闭后将清除本机随机编号及待发送记录（已上传至服务端的数据无法撤回），再次开启时将生成新编号。") }}</p>
    <p v-if="telemetry.error" class="sheet-error" role="alert">{{ telemetry.error }}</p>
    <div class="sheet-actions">
      <button class="sheet-btn primary" type="button" data-autofocus :disabled="telemetry.saving" @click="acknowledgeTelemetryNotice">{{ t("知道了") }}</button>
    </div>
  </AssistantSheet>
</template>

<style scoped>
.disclosure {
  margin: 18px 0 0;
  padding: 2px 0;
  border-block: 1px solid var(--line);
}
.disclosure > div { margin: 12px 0; }
dt { font-size: 12px; font-weight: 650; }
dd { margin: 4px 0 0; color: var(--fg-dim); font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }
</style>
