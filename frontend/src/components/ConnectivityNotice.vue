<script setup lang="ts">
import Icon from "./Icon.vue";
import { connectivity } from "../connectivity";
import { openProxySettings } from "../store";
import { t } from "../i18n";
</script>
<template>
  <aside v-if="connectivity.result?.poor && connectivity.dismissedAt !== connectivity.result.at" class="connectivity-notice glass glass-sheet" role="status">
    <Icon name="alert" :size="17" />
    <span>{{ t("服务连接较差，建议检查或切换 VPN 节点") }}</span>
    <button class="quiet" @click="openProxySettings">{{ t("检查网络") }}</button>
    <button class="quiet icon" :aria-label="t('关闭')" @click="connectivity.dismissedAt = connectivity.result.at"><Icon name="close" :size="14" /></button>
  </aside>
</template>
<style scoped>
.connectivity-notice { position: fixed; z-index: 32; top: 92px; right: 30px; display: flex; align-items: center; gap: 10px; max-width: min(560px, calc(100vw - 40px)); padding: 10px 12px 10px 16px; border-radius: 16px; font-size: 13px; }
.connectivity-notice > svg { flex: none; color: var(--warning); }
span { min-width: 0; }
button { flex: none; font-size: 12px; }
</style>
