<script setup lang="ts">
import { computed, onMounted } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import Toggle from "./Toggle.vue";
import { bytes } from "../format";
import { onWindows } from "../dom";
import { N_, t, intlTag } from "../i18n";
import { updates, watchUpdates, checkUpdates, downloadUpdate, cancelUpdate, installUpdate, ignoreUpdate, remindUpdateLater, setAutomaticUpdates } from "../updatestore";

onMounted(() => { void watchUpdates(); });
const state = computed(() => updates.state);
const busy = computed(() => updates.pending || ["checking", "downloading", "preparing", "confirming", "installing"].includes(state.value?.phase ?? ""));
const progress = computed(() => state.value?.info?.size ? Math.min(100, Math.round(state.value.received / state.value.info.size * 100)) : 0);
const checkedAt = computed(() => {
  const value = state.value?.lastChecked;
  return value ? new Date(value).toLocaleString(intlTag()) : "";
});
const errors: Record<string, string> = {
  network: N_("无法连接更新源，请检查网络与代理后重试。"),
  download: N_("更新下载失败，请检查网络后重新下载。"),
  signature: N_("更新签名无效，已停止更新。"),
  integrity: N_("安装包校验失败，请重新下载。"),
  manifest: N_("更新信息不完整或格式不正确，已停止更新。"),
  noRelease: N_("暂时没有可用的正式发布，请稍后再检查。"),
  rateLimit: N_("GitHub 暂时限制了请求，请稍后再试。"),
  storage: N_("无法保存更新文件或设置，请检查磁盘空间与目录权限。"),
  installLocation: N_("请将 PKP 安装到「应用程序」后再更新，不要从磁盘镜像、只读位置或临时隔离路径运行。"),
  installPermission: N_("未获准替换应用。请重试并允许 macOS 的权限请求。"),
  installBusy: N_("另一个 PKP 实例正在使用此应用。请退出其他配置或窗口所属的 PKP 进程后重试。"),
  install: onWindows ? N_("未能打开安装程序，当前应用继续运行。") : N_("更新安装失败，请检查磁盘空间与应用权限后重试，或从官方 Release 重新安装。"),
  installRollback: N_("更新失败，且未能恢复原应用。请从官方 Release 重新安装 PKP；账号与本地设置仍保留。"),
};
const error = computed(() => updates.error || (state.value?.error ? t(errors[state.value.error] ?? errors.network) : ""));
const status = computed(() => {
  if (!state.value) return t("正在读取…");
  if (!state.value.supported) return t("开发构建或此平台不支持应用内更新。");
  switch (state.value.phase) {
    case "checking": return t("正在检查更新…");
    case "current": return t("已是最新正式版本");
    case "updated": return t("已更新至 {version}", { version: state.value.current });
    case "downloading": return t("正在下载并校验更新…");
    case "ready": return t("更新已下载，签名与安装包校验通过。");
    case "installing": return t("正在准备安装…");
    case "available": return t("发现新版本 {version}", { version: state.value.info?.version ?? "" });
    default: return t("从 GitHub Releases 获取正式版本。");
  }
});
</script>

<template>
  <section class="update-settings" :aria-label="t('关于与更新')">
    <h2>{{ t("关于与更新") }}</h2>
    <p class="lead">{{ t("检查新版本、查看更新说明，并安全下载官方安装包。") }}</p>
    <div class="update-card glass-well">
      <div class="version-row">
        <span class="app-badge"><Icon name="spark" :size="25" /></span>
        <div class="version"><strong>PKP</strong><span>{{ t("当前版本 {version}", { version: state?.current || "—" }) }}</span></div>
        <button class="pill" :disabled="!state?.supported || busy" @click="checkUpdates">{{ t("检查更新") }}</button>
      </div>
      <p class="status" role="status"><Orbit v-if="busy" :size="16" :label="status" />{{ status }}</p>
      <p v-if="checkedAt" class="muted">{{ t("上次检查：{time}", { time: checkedAt }) }}</p>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
    </div>

    <div class="update-card glass-well automatic">
      <div><strong>{{ t("自动检查更新") }}</strong><p class="muted">{{ t("启动后检查，之后最多每 24 小时一次；不会自动下载或安装。不发送账号、文件内容或统计编号。") }}</p></div>
      <Toggle :model-value="!!state?.automatic" :label="t('自动检查更新')" :disabled="!state?.supported || busy" @update:model-value="setAutomaticUpdates" />
    </div>

    <div v-if="state?.info" class="update-card glass-well release">
      <div class="release-head"><h3>PKP {{ state.info.version }}</h3><span class="muted">{{ state.info.publishedAt.slice(0, 10) }} · {{ bytes(state.info.size) }}</span></div>
      <p v-if="!state.info.compatible" class="error">{{ t("此更新需要 {system} {version} 或更新版本。", { system: onWindows ? 'Windows' : 'macOS', version: state.info.minOS }) }}</p>
      <h4>{{ t("更新说明") }}</h4>
      <p class="notes">{{ state.info.notes }}</p>
      <template v-if="state.phase === 'downloading'">
        <progress :value="state.received" :max="state.info.size" :aria-label="t('更新下载进度')" />
        <div class="download-row"><span class="muted">{{ bytes(state.received) }} / {{ bytes(state.info.size) }} · {{ progress }}%</span><button class="pill" :disabled="updates.pending" @click="cancelUpdate">{{ t("取消下载") }}</button></div>
      </template>
      <div v-else class="actions">
        <button v-if="state.phase === 'ready' || state.phase === 'installing'" class="pill primary" :disabled="busy" @click="installUpdate">{{ onWindows ? t("退出并运行安装程序") : t("重启并更新") }}</button>
        <button v-else class="pill primary" :disabled="busy || !state.info.compatible" @click="downloadUpdate">{{ t("下载更新") }}</button>
        <button class="pill" :disabled="busy" @click="remindUpdateLater">{{ t("稍后提醒") }}</button>
        <button class="quiet" :disabled="busy" @click="ignoreUpdate">{{ t("忽略此版本") }}</button>
      </div>
      <p class="muted install-note">{{ onWindows ? t("安装前将确认退出；取消退出或管理员授权不会强制关闭 PKP。安装器不会删除账号与本地设置。") : t("确认退出后将替换应用并重新打开，账号与本地设置保留。macOS 可能请求替换权限；更新不会绕过 Gatekeeper，重新打开时仍可能需要系统确认。") }}</p>
    </div>
  </section>
</template>

<style scoped>
.update-settings { min-width: 0; }
h2 { margin: 0; font-size: 24px; letter-spacing: -.6px; }
.lead { color: var(--fg-dim); margin: 10px 0 26px; line-height: 1.6; font-size: 13px; }
.update-card { padding: 22px; border-radius: 22px; margin-bottom: 14px; }
.version-row, .automatic, .release-head, .download-row { display: flex; align-items: center; gap: 14px; }
.version { display: grid; gap: 5px; flex: 1; }
.version strong { font-size: 21px; }
.version span, .muted { font-size: 12px; color: var(--fg-dim); line-height: 1.6; }
.app-badge { display: grid; place-items: center; width: 48px; height: 48px; border-radius: 15px; color: var(--accent); background: var(--accent-soft); }
.status { display: flex; align-items: center; gap: 8px; font-size: 13px; margin: 20px 0 8px; }
.error { color: var(--danger); font-size: 13px; line-height: 1.6; }
.automatic { justify-content: space-between; }
.automatic > div { min-width: 0; }
.automatic strong { font-size: 14px; }
.automatic .muted { margin: 7px 0 0; }
.release-head { flex-wrap: wrap; justify-content: space-between; }
h3 { margin: 0; font-size: 18px; } h4 { font-size: 13px; margin-bottom: 8px; }
.notes { white-space: pre-wrap; overflow-wrap: anywhere; font-size: 13px; line-height: 1.8; max-height: 340px; overflow: auto; }
.actions { display: flex; align-items: center; flex-wrap: wrap; gap: 10px; margin-top: 22px; }
.install-note { margin: 16px 0 0; }
progress { display: block; width: 100%; height: 7px; accent-color: var(--accent); margin-top: 20px; }
.download-row { justify-content: space-between; margin-top: 12px; }
@media (max-width: 560px) { .update-card { padding: 16px; } .version-row { flex-wrap: wrap; } }
</style>
