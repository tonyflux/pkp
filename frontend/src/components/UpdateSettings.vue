<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { Browser } from "@wailsio/runtime";
import Icon from "./Icon.vue";
import Logo from "./Logo.vue";
import Orbit from "./Orbit.vue";
import Toggle from "./Toggle.vue";
import MarkdownDoc from "./MarkdownDoc";
import { bytes } from "../format";
import { onWindows } from "../dom";
import { N_, t, intlTag } from "../i18n";
import { parseMarkdown } from "../markdown";
import { updates, watchUpdates, checkUpdates, downloadUpdate, cancelUpdate, installUpdate, ignoreUpdate, remindUpdateLater, setAutomaticUpdates } from "../updatestore";

onMounted(() => { void watchUpdates(); });
const state = computed(() => updates.state);
const busy = computed(() => updates.pending || ["checking", "downloading", "preparing", "confirming", "installing"].includes(state.value?.phase ?? ""));
const installReady = computed(() => ["ready", "preparing", "confirming", "installing"].includes(state.value?.phase ?? ""));
const progress = computed(() => state.value?.info?.size ? Math.min(100, Math.round(state.value.received / state.value.info.size * 100)) : 0);
const releaseNotes = computed(() => parseMarkdown(state.value?.info?.notes ?? "").blocks);
const notesRoot = ref<HTMLElement>();
const releaseError = ref("");
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
    case "preparing":
    case "installing": return t("正在准备安装…");
    case "confirming": return t("正在等待退出确认…");
    case "available": return t("发现新版本");
    default: return t("从 GitHub Releases 获取正式版本。");
  }
});

async function followReleaseLink(href: string) {
  releaseError.value = "";
  if (href.startsWith("#")) {
    let anchor = href.slice(1);
    try { anchor = decodeURIComponent(anchor); } catch { /* Look up malformed escapes as written. */ }
    notesRoot.value?.querySelector<HTMLElement>(`[data-anchor="${CSS.escape(anchor.toLowerCase())}"]`)?.scrollIntoView({ block: "start" });
    return;
  }
  try {
    await Browser.OpenURL(href);
  } catch (err) {
    releaseError.value = t("无法打开链接：{error}", { error: err instanceof Error ? err.message : String(err) });
  }
}

const communityLinks = [
  { href: "https://t.me/+JKfP8XaSayg5OTU1", label: N_("Telegram 交流群"), description: N_("交流使用问题、建议与更新动态"), icon: "shares" },
  { href: "https://github.com/tonyflux/pkp", label: N_("GitHub 项目"), description: N_("查看源码、发布版本与提交反馈"), icon: "terminal" },
];
const communityError = ref("");

async function openCommunity(href: string) {
  communityError.value = "";
  try {
    await Browser.OpenURL(href);
  } catch (err) {
    communityError.value = t("无法打开链接：{error}", { error: err instanceof Error ? err.message : String(err) });
  }
}
</script>

<template>
  <section class="update-settings" :aria-label="t('关于与更新')">
    <h2>{{ t("关于与更新") }}</h2>
    <p class="lead">{{ t("检查新版本、查看更新说明，并安全下载官方安装包。") }}</p>
    <div class="update-card glass-well version-card">
      <div class="version-row">
        <Logo :size="38" />
        <div class="version"><strong>PKP</strong><span>{{ t("当前版本 {version}", { version: state?.current || "—" }) }}</span></div>
        <button class="pill check" :class="{ working: state?.phase === 'checking' }" :disabled="!state?.supported || busy" @click="checkUpdates">{{ t("检查更新") }}</button>
      </div>
      <div class="result-head">
        <p class="status" :class="{ available: state?.phase === 'available' }" role="status"><Orbit v-if="busy" :size="15" :label="status" /><span v-else-if="state?.phase === 'available'" class="status-dot" aria-hidden="true" />{{ status }}</p>
        <p v-if="checkedAt" class="muted checked-at">{{ t("上次检查：{time}", { time: checkedAt }) }}</p>
      </div>
      <template v-if="state?.info">
        <h3 class="release-version">PKP {{ state.info.version }}</h3>
        <p class="muted release-meta">{{ state.info.publishedAt.slice(0, 10) }} · {{ bytes(state.info.size) }}</p>
        <p v-if="!state.info.compatible" class="error">{{ t("此更新需要 {system} {version} 或更新版本。", { system: onWindows ? 'Windows' : 'macOS', version: state.info.minOS }) }}</p>
      </template>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <template v-if="state?.info">
        <div v-if="state.phase === 'downloading'" class="download">
          <progress :value="state.received" :max="state.info.size" :aria-label="t('更新下载进度')" />
          <div class="download-row"><span class="muted">{{ bytes(state.received) }} / {{ bytes(state.info.size) }} · {{ progress }}%</span><button class="pill" :disabled="updates.pending" @click="cancelUpdate">{{ t("取消下载") }}</button></div>
        </div>
        <div v-else class="actions">
          <button v-if="installReady" class="pill primary" :class="{ working: busy }" :disabled="!state.supported || busy" @click="installUpdate"><Icon name="restore" :size="17" />{{ onWindows ? t("退出并运行安装程序") : t("重启并更新") }}</button>
          <button v-else class="pill primary" :disabled="!state.supported || busy || !state.info.compatible" @click="downloadUpdate"><Icon name="download" :size="17" />{{ t("下载更新") }}</button>
          <button class="pill" :disabled="busy" @click="remindUpdateLater">{{ t("稍后提醒") }}</button>
          <button class="quiet" :disabled="busy" @click="ignoreUpdate">{{ t("忽略此版本") }}</button>
        </div>
        <p class="muted safety"><Icon name="shield" :size="13" />{{ t("不会自动安装，账号与本地设置保留。") }}</p>
      </template>
    </div>

    <section v-if="state?.info" class="release" aria-labelledby="release-notes-title">
      <h3 id="release-notes-title">{{ t("更新说明") }}</h3>
      <div ref="notesRoot" class="notes"><MarkdownDoc :blocks="releaseNotes" @link="followReleaseLink" /></div>
      <p v-if="releaseError" class="error" role="alert">{{ releaseError }}</p>
      <p class="muted install-note">{{ onWindows ? t("安装前将确认退出；取消退出或管理员授权不会强制关闭 PKP。安装器不会删除账号与本地设置。") : t("确认退出后将替换应用并重新打开，账号与本地设置保留。macOS 可能请求替换权限；更新不会绕过 Gatekeeper，重新打开时仍可能需要系统确认。") }}</p>
    </section>

    <div class="update-card glass-well automatic">
      <div><strong>{{ t("自动检查更新") }}</strong><p class="muted">{{ t("启动后检查，之后最多每 24 小时一次；不会自动下载或安装。不发送账号、文件内容或统计编号。") }}</p></div>
      <Toggle :model-value="!!state?.automatic" :label="t('自动检查更新')" :disabled="!state?.supported || busy" @update:model-value="setAutomaticUpdates" />
    </div>

    <section class="community" aria-labelledby="community-title">
      <h3 id="community-title">{{ t("社区与反馈") }}</h3>
      <div class="community-links">
        <a v-for="link in communityLinks" :key="link.href" class="community-link" :href="link.href" target="_blank" rel="noopener noreferrer" @click.prevent="openCommunity(link.href)">
          <span class="community-icon"><Icon :name="link.icon" :size="20" /></span>
          <span class="community-copy"><strong>{{ t(link.label) }}</strong><span>{{ t(link.description) }}</span></span>
          <Icon class="external" name="popout" :size="16" />
        </a>
      </div>
      <p v-if="communityError" class="error" role="alert">{{ communityError }}</p>
    </section>
  </section>
</template>

<style scoped>
.update-settings { min-width: 0; container: update-settings / inline-size; }
h2 { margin: 0; font-size: 24px; letter-spacing: -.6px; }
.lead { color: var(--fg-dim); margin: 6px 0 16px; line-height: 1.6; font-size: 13px; }
.update-card { padding: 18px 20px; border-radius: 20px; }
.version-card { margin-bottom: 20px; }
.version-row, .automatic, .download-row { display: flex; align-items: center; gap: 12px; }
.version-row { padding-bottom: 14px; border-bottom: 1px solid var(--line); }
.version { display: grid; gap: 3px; flex: 1; min-width: 0; }
.version strong { font-size: 17px; }
.version span, .muted { font-size: 12px; color: var(--fg-dim); line-height: 1.6; }
.check { font-size: 12px; }
.result-head { display: flex; flex-wrap: wrap; align-items: baseline; justify-content: space-between; gap: 6px 14px; margin-top: 14px; }
.status { display: flex; align-items: center; gap: 7px; min-width: 0; margin: 0; font-size: 13px; line-height: 1.6; overflow-wrap: anywhere; }
.status.available { color: var(--accent); }
.status-dot { flex: none; width: 6px; height: 6px; border-radius: 50%; background: currentColor; }
.checked-at { margin: 0; font-size: 11px; }
.release-version { margin: 5px 0 2px; font-size: 28px; line-height: 1.25; letter-spacing: -.7px; overflow-wrap: anywhere; }
.release-meta { margin: 0; }
.error { color: var(--danger); font-size: 13px; line-height: 1.6; overflow-wrap: anywhere; margin: 10px 0 0; }
.actions { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; margin-top: 14px; }
.actions button, .download-row button { max-width: 100%; white-space: normal; overflow-wrap: anywhere; font-size: 12px; }
.actions .primary { min-height: 38px; padding: 8px 18px; font-size: 13px; }
.safety { display: flex; align-items: flex-start; gap: 5px; margin: 11px 0 0; font-size: 11px; }
.safety > svg { margin-top: 2px; color: var(--success); }
.release { margin: 0 2px 22px; }
h3 { margin: 0; font-size: 14px; }
.notes { overflow-wrap: anywhere; font-size: 13px; line-height: 1.8; margin: 9px 0 0; }
.notes :deep(h1), .notes :deep(h2), .notes :deep(h3), .notes :deep(h4), .notes :deep(h5), .notes :deep(h6) { margin: 22px 0 8px; font-weight: 600; line-height: 1.4; letter-spacing: -.2px; }
.notes :deep(h1) { font-size: 20px; }
.notes :deep(h2) { font-size: 17px; }
.notes :deep(h3) { font-size: 15px; }
.notes :deep(h4), .notes :deep(h5), .notes :deep(h6) { font-size: 13px; }
.notes :deep(.md > :first-child) { margin-top: 0; }
.notes :deep(p), .notes :deep(ul), .notes :deep(ol) { margin: 0 0 12px; }
.notes :deep(ul), .notes :deep(ol) { padding-left: 22px; }
.notes :deep(li + li) { margin-top: 6px; }
.notes :deep(li > ul), .notes :deep(li > ol) { margin-bottom: 0; }
.notes :deep(blockquote) { margin: 14px 0; padding-left: 14px; border-left: 2px solid var(--accent); color: var(--fg-dim); }
.notes :deep(code) { font-family: ui-monospace, "SF Mono", Menlo, monospace; font-size: .9em; background: var(--glass-inset); padding: 2px 5px; border-radius: 4px; }
.notes :deep(.md-code) { margin: 12px 0; padding: 12px 14px; background: var(--glass-inset); border-radius: 10px; white-space: pre-wrap; overflow-wrap: anywhere; }
.notes :deep(.md-code code) { padding: 0; background: none; }
.notes :deep(a) { color: var(--accent); text-decoration: underline; text-underline-offset: 3px; }
.notes :deep(a:focus-visible) { outline: 2px solid var(--accent); outline-offset: 3px; border-radius: 3px; }
.notes :deep(hr) { margin: 20px 0; border: 0; border-top: 1px solid var(--line); }
.notes :deep(.md-table) { overflow-x: auto; margin: 12px 0; }
.notes :deep(table) { width: 100%; border-collapse: collapse; }
.notes :deep(th), .notes :deep(td) { padding: 6px 10px; border: 1px solid var(--line); }
.notes :deep(.md-image), .notes :deep(.md-link-off) { color: var(--fg-dim); border-bottom: 1px dashed var(--line-strong); }
.notes :deep(.task) { list-style: none; }
.notes :deep(.box) { display: inline-block; width: 12px; height: 12px; margin-right: 6px; border: 1px solid var(--line-strong); border-radius: 3px; vertical-align: -1px; }
.notes :deep(.box.on) { background: var(--accent); border-color: var(--accent); }
.install-note { margin: 14px 0 0; font-size: 11px; }
progress { display: block; width: 100%; height: 7px; accent-color: var(--accent); margin-top: 16px; }
.download-row { flex-wrap: wrap; justify-content: space-between; margin-top: 8px; }
.download-row > span { font-variant-numeric: tabular-nums; }
.automatic { justify-content: space-between; padding: 15px 18px; margin-bottom: 22px; border-radius: 16px; }
.automatic > div { min-width: 0; }
.automatic strong { font-size: 13px; }
.automatic .muted { margin: 5px 0 0; font-size: 11px; }
.community { margin: 0 2px; }
.community h3 { margin-bottom: 4px; }
.community-links { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0 20px; }
.community-link { display: flex; align-items: center; gap: 10px; padding: 12px 0; color: var(--fg); text-decoration: none; }
.community-icon { display: grid; place-items: center; flex: none; width: 30px; height: 30px; border-radius: 10px; color: var(--accent); background: var(--accent-soft); }
.community-copy { display: grid; gap: 3px; flex: 1; min-width: 0; overflow-wrap: anywhere; }
.community-copy strong { font-size: 12px; }
.community-copy > span { color: var(--fg-dim); font-size: 11px; line-height: 1.6; }
.community-link > .external { color: var(--fg-dim); }
.community-link:focus-visible { outline: 2px solid var(--accent); outline-offset: 4px; border-radius: 8px; }
@media (hover: hover) { .community-link:hover .community-copy strong { color: var(--accent); } }
@container update-settings (max-width: 460px) {
  .update-card { padding: 15px 16px; }
  .version-row { flex-wrap: wrap; gap: 10px; }
  .version { flex-basis: 120px; }
  .check { max-width: 100%; white-space: normal; }
  .community-links { grid-template-columns: minmax(0, 1fr); }
  .community-link + .community-link { border-top: 1px solid var(--line); }
}
</style>
