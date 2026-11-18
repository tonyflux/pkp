<script setup lang="ts">
import { computed, onMounted } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import { connectivity, testConnectivity, watchConnectivity } from "../connectivity";
import { t } from "../i18n";
import { stamp } from "../format";
const result = computed(() => connectivity.result);
onMounted(() => void watchConnectivity());
function label(service: string) {
  return service === "auth" ? t("登录服务") : service === "webdav" ? t("WebDAV 服务") : t("文件服务");
}
</script>
<template>
  <section class="connectivity-panel" :aria-label="t('连通性测试')" :aria-busy="connectivity.testing">
    <header>
      <span class="heading"><Icon name="globe" :size="20" /><span><h3>{{ t("连通性测试") }}</h3><small>{{ t("测试当前已应用的网络，不使用未保存的代理设置。") }}</small></span></span>
      <button class="primary" :disabled="connectivity.testing" @click="testConnectivity"><Orbit v-if="connectivity.testing" :size="15" />{{ connectivity.testing ? t("正在测试") : t("开始测试") }}</button>
    </header>
    <p class="hint">{{ t("打开应用后每 5 分钟会自动测一次。直连会自动选响应最快的 DNS 节点，走代理时则由代理决定节点。") }}</p>
    <p v-if="connectivity.error" class="warning" role="alert">{{ connectivity.error }}</p>
    <template v-if="result?.at">
      <p class="outcome" :class="{ warning: result.poor }" role="status"><Icon :name="result.poor ? 'alert' : 'check'" :size="16" />{{ result.poor ? t("有些服务比较慢或连不上，检查下网络，或者开关、切换下 VPN 节点再试。") : t("服务连接正常") }}</p>
      <ul>
        <li v-for="row in result.targets" :key="row.service">
          <span class="service"><strong>{{ label(row.service) }}</strong><small>{{ row.host }}</small></span>
          <span class="route"><small>{{ row.via }}</small><small v-if="row.node">{{ t("连接节点") }} · {{ row.node }}</small></span>
          <span class="measurement" :class="{ warning: !row.reachable || row.elapsedMs >= 2000 || row.status >= 500 || row.status === 429 }"><strong>{{ row.reachable ? `${row.elapsedMs} ms` : t("无法连接") }}</strong><small v-if="row.status">HTTP {{ row.status }}</small></span>
        </li>
      </ul>
      <p class="hint">{{ t("检测时间") }} · {{ stamp(result.at) }}</p>
    </template>
    <p class="hint">{{ t("这里测的是 HTTPS 响应头延迟，不是下载速度（收到 401/403/404 也算连通）；不会发送账号密码，也不会改你的系统 VPN。") }}</p>
  </section>
</template>
<style scoped>
.connectivity-panel { margin-top: 28px; padding: 24px; border: 1px solid var(--line); border-radius: 20px; background: color-mix(in srgb, var(--glass-control) 35%, transparent); }
header, .heading { display: flex; align-items: center; gap: 12px; }
header { justify-content: space-between; flex-wrap: wrap; }
.heading > svg { flex: none; color: var(--accent); }
h3 { margin: 0 0 4px; font-size: 17px; font-weight: 600; }
small, .hint { font-size: 12px; color: var(--fg-dim); overflow-wrap: anywhere; }
.hint { line-height: 1.65; margin: 14px 0 0; }
button { min-height: 40px; font-size: 13px; }
.outcome { display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--success); line-height: 1.6; }
.outcome svg { flex: none; }
.warning { color: var(--warning); }
ul { list-style: none; margin: 16px 0 0; padding: 0; }
li { display: grid; grid-template-columns: minmax(120px, 1fr) minmax(120px, 1fr) auto; gap: 16px; align-items: center; padding: 15px 0; border-top: 1px solid var(--line); }
li > span { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
strong { font-size: 13px; font-weight: 600; }
.measurement { text-align: right; font-variant-numeric: tabular-nums; }
@media (max-width: 650px) { .connectivity-panel { padding: 18px; } li { grid-template-columns: minmax(0, 1fr) auto; } .service { grid-column: 1; grid-row: 1; } .route { grid-column: 1; grid-row: 2; } .measurement { grid-column: 2; grid-row: 1 / 3; } }
</style>
