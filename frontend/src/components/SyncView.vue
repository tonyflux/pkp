<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { accountName, currentId, openSignIn } from "../store";
import { t } from "../i18n";
import { bytes, shownPath, stamp } from "../format";
import { isTyping, onWindows, useMedia } from "../dom";
import { loadSync, openSyncCreate, openSyncDialog, openSyncEdit, openSyncRoot, sync, syncActionName, syncDirectionName, syncModeName, syncNeedsAttention, syncPairAction, syncStateName, type SyncPair } from "../syncstore";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
const compact = useMedia("(max-width: 1199px)");
const attentionOnly = ref(false);
const list = ref<HTMLElement>();
const detailHeading = ref<HTMLElement>();
const matching = computed(() => {
  const query = sync.query.trim().toLocaleLowerCase();
  return sync.pairs.filter((pair) => (sync.scope === "all" || pair.account === currentId.value) && (!query || [pair.name, pair.localPath, pair.cloudPath, accountName(pair.account)].some((text) => text.toLocaleLowerCase().includes(query))));
});
const attention = computed(() => matching.value.filter(syncNeedsAttention).length);
watch(attention, (count) => { if (!count) attentionOnly.value = false; }, { flush: "sync" });
const visible = computed(() => matching.value.filter((pair) => !attentionOnly.value || syncNeedsAttention(pair)));
const selected = computed(() => visible.value.find((pair) => pair.id === sync.selected));
const outside = computed(() => sync.pairs.filter((pair) => pair.account !== currentId.value && syncNeedsAttention(pair)));
const tabs = computed(() => [{ id: "activity" as const, name: t("同步活动") }, { id: "issues" as const, name: t("需要处理") }, { id: "settings" as const, name: t("配对设置") }]);
watch(() => visible.value.map((pair) => pair.id).join("\0"), () => {
  if (!visible.value.some((pair) => pair.id === sync.selected)) { sync.selected = visible.value[0]?.id ?? ""; sync.detail = false; }
}, { immediate: true });
watch(currentId, () => { if (sync.scope === "current") { sync.detail = false; sync.selected = visible.value[0]?.id ?? ""; } });
async function select(pair: SyncPair, open: boolean) {
  sync.selected = pair.id;
  if (open && compact.value) { sync.detail = true; await nextTick(); detailHeading.value?.focus({ preventScroll: true }); }
}
async function back() {
  sync.detail = false;
  await nextTick(); list.value?.querySelector<HTMLElement>("[aria-selected='true']")?.focus({ preventScroll: true });
}
function listKey(event: KeyboardEvent) {
  if (isTyping(event)) return;
  const index = visible.value.findIndex((pair) => pair.id === sync.selected);
  if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
    event.preventDefault();
    const next = event.key === "Home" ? 0 : event.key === "End" ? visible.value.length - 1 : Math.max(0, Math.min(visible.value.length - 1, index + (event.key === "ArrowDown" ? 1 : -1)));
    const pair = visible.value[next];
    if (pair) { sync.selected = pair.id; void nextTick(() => list.value?.querySelector<HTMLElement>("[aria-selected='true']")?.focus()); }
  } else if (event.key === "Enter" && selected.value) { event.preventDefault(); void select(selected.value, true); }
}
function tabKey(event: KeyboardEvent) {
  if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  const index = tabs.value.findIndex((tab) => tab.id === sync.tab);
  sync.tab = tabs.value[event.key === "Home" ? 0 : event.key === "End" ? 2 : (index + (event.key === "ArrowRight" ? 1 : 2)) % 3].id;
  void nextTick(() => document.getElementById(`sync-tab-${sync.tab}`)?.focus());
}
function openRoot(event: MouseEvent, pair: SyncPair, side: "local" | "cloud") {
  const selection = window.getSelection();
  if (event.detail && selection && !selection.isCollapsed && (event.currentTarget as HTMLElement).contains(selection.anchorNode)) return;
  void openSyncRoot(pair, side);
}
</script>
<template>
  <section class="sync-page" :class="{ compact, 'show-detail': sync.detail }" :aria-label="t('同步与备份')">
    <header class="page-head"><div><h1>{{ t("同步与备份") }}</h1><p>{{ t("让电脑与云盘里的文件，各就各位。") }}</p></div><button class="primary" :disabled="!currentId" @click="openSyncCreate(currentId)"><Icon name="plus" :size="18" />{{ t("新建同步") }}</button></header>
    <div class="scope-bar"><div class="scopes" role="group" :aria-label="t('同步账号范围')"><button :aria-pressed="sync.scope === 'current'" @click="sync.scope = 'current'">{{ t("当前账号") }}</button><button :aria-pressed="sync.scope === 'all'" @click="sync.scope = 'all'">{{ t("全部账号") }}</button></div><label v-if="attention" class="attention-filter"><input v-model="attentionOnly" type="checkbox" />{{ t("需要处理") }} · {{ attention }}</label><button class="quiet icon" :title="t('刷新同步状态')" :aria-label="t('刷新同步状态')" :disabled="sync.loading" @click="loadSync"><Icon name="restore" :size="18" /></button></div>
    <p v-if="sync.scope === 'current' && outside.length" class="outside">{{ t("其他账号另有 {n} 个同步对需要处理", { n: outside.length }) }} · {{ [...new Set(outside.map((pair) => accountName(pair.account)))].join(' / ') }}<button class="quiet" @click="sync.scope = 'all'">{{ t("查看全部账号") }}</button></p>
    <p v-if="sync.error" class="page-error" role="alert">{{ sync.error }}</p>
    <div v-if="!sync.pairs.length && sync.loading" class="empty" role="status"><Orbit :size="24" /><p>{{ t("正在加载") }}</p></div>
    <div v-else-if="!sync.pairs.length" class="empty"><Icon name="repeat" :size="38" /><h2>{{ t("把两端文件夹配对") }}</h2><p>{{ t("选择双向同步、本地备份或云端下载。先检查完整计划，再开始同步。") }}</p><button class="primary" :disabled="!currentId" @click="openSyncCreate(currentId)">{{ t("新建同步") }}</button></div>
    <div v-else class="workspace">
      <div v-show="!compact || !sync.detail" ref="list" class="pair-list" role="listbox" :aria-label="t('同步文件夹列表')" @keydown="listKey">
        <p v-if="!visible.length" class="no-results">{{ t("没有符合筛选的同步对") }}</p>
        <div v-for="pair in visible" :key="pair.id" class="pair" role="option" :aria-selected="sync.selected === pair.id" :tabindex="sync.selected === pair.id ? 0 : -1" @click="select(pair, true)" @keydown.space.prevent="select(pair, false)">
          <Icon name="folder" :size="22" /><div class="pair-copy"><strong :title="pair.name">{{ pair.name }}</strong><small>{{ syncModeName(pair.mode) }}</small><small v-if="sync.scope === 'all'" class="owner">{{ accountName(pair.account) }}</small><div class="pair-state"><span>{{ syncStateName(pair) }}</span><span v-if="syncNeedsAttention(pair)" class="warning"><Icon name="alert" :size="14" />{{ pair.issues.length ? t('{n} 项需处理', { n: pair.issues.length }) : t('需要处理') }}</span></div></div><Icon :name="pair.paused ? 'pause' : syncNeedsAttention(pair) ? 'alert' : pair.state === 'idle' ? 'check' : 'repeat'" :size="17" />
        </div>
      </div>
      <article v-show="!compact || sync.detail" v-if="selected" class="pair-detail" @keydown.esc.stop.prevent="compact && back()">
        <button v-if="compact" class="quiet back" @click="back"><Icon name="chevronLeft" :size="16" />{{ t("返回文件夹列表") }}</button>
        <header class="detail-head"><div><h2 ref="detailHeading" tabindex="-1">{{ selected.name }}</h2><p>{{ t("所属账号") }} · {{ accountName(selected.account) }}</p><small class="accent">{{ syncModeName(selected.mode) }}</small></div><div class="detail-actions"><button :disabled="sync.busy[selected.id] || selected.state === 'pausing'" @click="syncPairAction(selected, 'scan')">{{ t("立即检查") }}</button><button :disabled="sync.busy[selected.id] || selected.state === 'pausing'" @click="syncPairAction(selected, selected.paused ? 'resume' : 'pause')"><Icon :name="selected.paused ? 'play' : 'pause'" :size="16" />{{ selected.paused ? t('继续同步') : t('暂停') }}</button></div></header>
        <div class="roots">
          <div>
            <small><Icon name="laptop" :size="16" />{{ t("本地文件夹") }}</small>
            <p><a href="#" draggable="false" class="root-link" :title="onWindows ? t('在资源管理器中显示') : t('在访达中显示')" @click.prevent="openRoot($event, selected, 'local')">{{ selected.localPath }}</a></p>
          </div>
          <Icon name="repeat" :size="20" />
          <div>
            <small><Icon name="folder" :size="16" />{{ t("云端文件夹") }}</small>
            <p><a href="#" draggable="false" class="root-link" :title="selected.account === currentId ? t('打开云端文件夹') : t('切换并打开所在文件夹')" @click.prevent="openRoot($event, selected, 'cloud')">{{ shownPath(selected.cloudPath) }}</a></p>
          </div>
        </div>
        <div class="status" aria-live="polite"><Icon :name="syncNeedsAttention(selected) ? 'alert' : selected.paused ? 'pause' : 'repeat'" :size="16" /><strong>{{ syncStateName(selected) }}</strong></div>
        <p v-if="selected.paused && selected.state !== 'paused' && selected.state !== 'pausing'" class="status-note">{{ t("已手动暂停；恢复连接后仍保持暂停。") }}</p>
        <p v-if="selected.phase" class="status-note">{{ selected.phase === 'scan' ? t('正在核对内容') : selected.phase === 'transfer' ? t('正在传输') : selected.phase }}</p>
        <p v-if="selected.error" class="error">{{ selected.error }}</p>
        <p v-if="selected.state === 'offline'" class="status-note">{{ t("连接恢复后重新核对；旧结果并非实时状态。") }}</p>
        <p v-if="selected.state === 'disk'" class="status-note">{{ t("等待原磁盘恢复并验证身份；未将无法读取的目录当成空目录。") }}</p>
        <p v-if="selected.state === 'pausing'" class="status-note">{{ t("停止派发新步骤，已发出的请求正在安全核实。") }}</p>
        <button v-if="selected.state === 'auth'" @click="openSignIn(selected.account)">{{ t("需要重新登录") }}</button>
        <div v-if="selected.state === 'syncing' && selected.progress.total > 0 && selected.progress.done < selected.progress.total" class="progress"><div><span>{{ selected.progress.path }}</span><small>{{ bytes(selected.progress.done, '0 B') }} / {{ bytes(selected.progress.total) }}</small></div><progress :value="selected.progress.done" :max="selected.progress.total" :aria-label="t('同步进度')" /><p>{{ syncDirectionName(selected.progress.direction) }}<template v-if="selected.progress.files > 0"> · {{ t("已处理 {done} / {total} 个文件", { done: selected.progress.completed, total: selected.progress.files }) }}</template></p></div>
        <dl class="timestamps"><div><dt>{{ t("上次成功核对") }}</dt><dd>{{ stamp(selected.lastSuccess, t('尚未成功核对')) }}</dd></div><div><dt>{{ t("上次尝试") }}</dt><dd>{{ stamp(selected.lastAttempt, t('尚未尝试')) }}</dd></div></dl>
        <button v-if="syncNeedsAttention(selected) && sync.tab !== 'issues'" class="attention-banner" @click="sync.tab = 'issues'"><Icon name="alert" :size="18" /><span>{{ t("查看此同步对需要处理的问题") }}</span><Icon name="chevronRight" :size="17" /></button>
        <div class="tabs" role="tablist" :aria-label="t('同步详情')" @keydown="tabKey"><button v-for="tab in tabs" :id="`sync-tab-${tab.id}`" :key="tab.id" role="tab" :tabindex="sync.tab === tab.id ? 0 : -1" :aria-selected="sync.tab === tab.id" :aria-controls="`sync-panel-${tab.id}`" @click="sync.tab = tab.id">{{ tab.name }}<small v-if="tab.id === 'issues' && selected.issues.length">{{ selected.issues.length }}</small></button></div>
        <div :id="`sync-panel-${sync.tab}`" class="tab-panel" role="tabpanel" :aria-labelledby="`sync-tab-${sync.tab}`" tabindex="0">
          <template v-if="sync.tab === 'activity'"><p v-if="!selected.activities.length" class="muted">{{ t("尚无已核实的同步活动") }}</p><ol v-else class="activities"><li v-for="activity in selected.activities" :key="activity.id"><Icon :name="activity.direction === 'upload' ? 'upload' : activity.direction === 'download' ? 'download' : 'file'" :size="17" /><div><strong>{{ activity.path }}</strong><p>{{ syncDirectionName(activity.direction) }} · {{ syncActionName(activity.action) }} · {{ syncActionName(activity.result) }}</p><p v-if="activity.error" class="error">{{ activity.error }}</p></div><time :datetime="activity.at">{{ stamp(activity.at) }}</time></li></ol></template>
          <template v-else-if="sync.tab === 'issues'">
            <section v-if="selected.deletePlan" class="issue"><h3>{{ t("删除保护 · 等待确认") }}</h3><p>{{ t("尚未执行本批删除。请审阅准确清单，或修正源端后重新检查。") }}</p><button @click="openSyncDialog('deletes', selected)">{{ t("审阅这批删除") }}</button></section>
            <section v-for="issue in selected.issues" :key="issue.id" class="issue"><h3>{{ issue.path || t('同步根目录') }}</h3><p>{{ issue.reason }}</p><button v-if="issue.keepName && ['conflict', 'target-edited'].includes(issue.kind)" @click="openSyncDialog('conflict', selected, issue)">{{ t("保留两者") }}</button><p v-else class="muted">{{ t("请修正具体问题后重新检查；不会自动覆盖或清洗名称。") }}</p></section>
            <p v-if="!selected.issues.length && !selected.deletePlan && !syncNeedsAttention(selected)" class="muted">{{ t("此同步对暂无需要处理的问题") }}</p>
            <div v-if="syncNeedsAttention(selected) && !selected.issues.length && !selected.deletePlan" class="issue"><p>{{ selected.error || syncStateName(selected) }}</p><button v-if="selected.state === 'repair'" @click="openSyncEdit(selected)">{{ t("重新配对并预览") }}</button><button v-else :disabled="sync.busy[selected.id]" @click="syncPairAction(selected, 'scan')">{{ t("立即检查") }}</button></div>
          </template>
          <template v-else><dl class="settings"><dt>{{ t("所属账号") }}</dt><dd>{{ accountName(selected.account) }}</dd><dt>{{ t("同步方式") }}</dt><dd>{{ syncModeName(selected.mode) }}</dd><dt>{{ t("跟随删除") }}</dt><dd>{{ selected.deletes[selected.mode] ? t("开启 · 已跟踪删除移入回收站") : t("关闭 · 保留另一端") }}</dd></dl><p class="muted">{{ t("更改根目录、同步方式或删除偏好，需重新扫描并确认新计划。") }}</p><div class="setting-actions"><button @click="openSyncEdit(selected)">{{ t("修改并重新预览") }}</button><button class="remove danger-action" :disabled="sync.busy[selected.id]" @click="openSyncDialog('remove', selected)">{{ t("移除同步") }}</button></div><p class="muted">{{ t("移除关系会保留已完成的本地和云端文件。") }}</p></template>
        </div>
      </article>
    </div>
    <footer class="page-foot"><span><Icon name="laptop" :size="14" />{{ t("仅在 PKP 运行时同步") }}</span><span>{{ t("同步记录独立于传输中心") }}</span></footer>
  </section>
</template>
<style scoped>
.root-link {
  color: inherit;
  text-decoration: none;
  border-radius: 3px;
  -webkit-user-select: text;
  user-select: text;
}
.root-link:hover,
.root-link:focus-visible {
  color: var(--accent);
  text-decoration: underline;
  text-underline-offset: 3px;
}
.sync-page{flex:1}
.sync-page{height:100%;min-height:0;min-width:0;display:flex;flex-direction:column;background:var(--canvas);border:1px solid var(--line);border-radius:var(--radius-island);overflow:hidden;color:var(--fg)}.page-head{display:flex;align-items:center;justify-content:space-between;gap:20px;padding:28px 30px 18px}.page-head h1{font-size:28px;line-height:34px;margin:0;font-weight:650;letter-spacing:-.03em}.page-head p{margin:7px 0 0;color:var(--fg-dim);font-size:14px}.page-head>button{flex:none;min-height:42px;white-space:normal}.scope-bar{display:flex;align-items:center;gap:16px;padding:0 30px 16px;flex-wrap:wrap}.scopes{display:flex;gap:4px;flex-wrap:wrap}.scopes button{min-height:36px;font-size:13px;padding:6px 12px;background:transparent;border-color:transparent}.scopes button[aria-pressed=true]{background:var(--accent-soft);color:var(--accent);border-color:var(--line-strong)}.scope-bar>.scopes{margin-right:auto}.attention-filter{display:flex;align-items:center;gap:7px;font-size:13px;color:var(--warning)}.attention-filter input{accent-color:var(--accent);width:16px;height:16px}.scope-bar>.icon{width:40px;height:40px;min-height:40px}.outside{font-size:12px;color:var(--warning);margin:0;padding:0 30px 12px;overflow-wrap:anywhere}.outside button{font-size:12px}.page-error{margin:0;padding:10px 30px;color:var(--danger);white-space:pre-wrap;overflow-wrap:anywhere;border-top:1px solid var(--line)}.workspace{flex:1;min-height:0;display:grid;grid-template-columns:minmax(270px,34%) minmax(0,1fr);border-top:1px solid var(--line)}.pair-list{overflow-y:auto;overscroll-behavior:contain;padding:16px 12px;border-right:1px solid var(--line);min-height:0}.pair{display:flex;align-items:flex-start;gap:14px;min-height:118px;padding:19px 14px;cursor:pointer;border:1px solid transparent;border-radius:14px;outline-offset:-3px}.pair>svg{flex:none;color:var(--accent);margin-top:3px}.pair[aria-selected=true]{background:var(--accent-soft);border-color:var(--line-strong)}.pair-copy{min-width:0;flex:1}.pair-copy>strong{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:16px}.pair-copy>small{display:block;color:var(--fg-dim);font-size:12px;margin:4px 0}.pair-copy>.owner{color:var(--accent)}.pair-state{display:flex;justify-content:space-between;gap:8px;font-size:12px;margin-top:14px;flex-wrap:wrap;color:var(--fg-dim)}.pair-state>span:last-child{display:flex;align-items:center;gap:4px}.warning{color:var(--warning)}.pair-detail{overflow:auto;overscroll-behavior:contain;padding:26px 28px 32px;min-width:0;min-height:0}.detail-head{display:flex;align-items:flex-start;justify-content:space-between;gap:14px;margin-bottom:24px}.detail-head>div:first-child{min-width:0}.detail-head h2{font-size:23px;line-height:1.35;margin:0;overflow-wrap:anywhere;font-weight:600}.detail-head p{font-size:13px;color:var(--fg-dim);margin:6px 0}.accent{color:var(--accent);font-size:13px}.detail-actions{display:flex;justify-content:flex-end;gap:8px;flex-wrap:wrap;flex:none;max-width:48%}.detail-actions button{min-height:40px;font-size:12px;white-space:normal}.roots{display:grid;grid-template-columns:minmax(0,1fr) 20px minmax(0,1fr);align-items:center;gap:12px;margin-bottom:18px}.roots>div{background:var(--glass-inset);padding:14px;border-radius:12px;min-height:92px}.roots small{display:flex;align-items:center;gap:7px;color:var(--fg-dim);font-size:12px}.roots small svg{flex:none}.roots p{font-size:13px;line-height:1.6;margin:9px 0 0;overflow-wrap:anywhere;font-weight:600}.roots>svg{color:var(--accent)}.status{display:flex;align-items:center;gap:8px;color:var(--accent);font-size:13px}.status-note{font-size:13px;color:var(--fg-dim);overflow-wrap:anywhere}.error{font-size:13px;color:var(--danger);white-space:pre-wrap;overflow-wrap:anywhere}.progress{margin:16px 0}.progress>div{display:flex;justify-content:space-between;gap:12px;font-size:12px}.progress span{overflow-wrap:anywhere;min-width:0}.progress small{flex:none}.progress progress{width:100%;height:5px;display:block;margin:10px 0;accent-color:var(--accent)}.progress p{font-size:12px;color:var(--fg-dim)}.timestamps{display:flex;gap:24px;flex-wrap:wrap;font-size:12px;margin:16px 0 20px}.timestamps>div{display:flex;gap:8px;flex-wrap:wrap}.timestamps dt{color:var(--fg-dim)}.timestamps dd{margin:0}.attention-banner{width:100%;display:flex;align-items:center;text-align:left;gap:10px;min-height:48px;border-color:color-mix(in srgb,var(--warning) 35%,transparent);color:var(--warning);margin:16px 0}.attention-banner span{flex:1;white-space:normal}.attention-banner svg{flex:none}.tabs{display:flex;gap:20px;border-bottom:1px solid var(--line);margin-top:24px;flex-wrap:wrap}.tabs button {
  position: relative;
  border: 0;
  border-radius: 10px;
  padding: 10px 12px;
  min-height: 44px;
  background: transparent;
  box-shadow: none;
  white-space: normal;
  font-size: 13px;
  color: var(--fg-dim);
}
.tabs button:hover:not(:disabled) {
  background: var(--accent-soft);
  color: var(--fg);
}
.tabs button[aria-selected=true] {
  color: var(--fg);
  font-weight: 600;
}
.tabs button[aria-selected=true]::after {
  content: "";
  position: absolute;
  inset: auto 12px 0;
  height: 2px;
  border-radius: 2px;
  background: var(--accent);
}
.tabs small{color:var(--warning);margin-left:6px}.tab-panel{padding-top:8px}.activities{list-style:none;margin:0;padding:0}.activities li{display:flex;gap:12px;align-items:flex-start;padding:18px 0;border-bottom:1px solid var(--line)}.activities svg{flex:none;color:var(--accent);margin-top:3px}.activities li>div{flex:1;min-width:0}.activities strong{font-size:13px;overflow-wrap:anywhere;font-weight:600}.activities p{font-size:12px;color:var(--fg-dim);margin:5px 0 0;overflow-wrap:anywhere}.activities p.error{color:var(--danger)}.activities time{font-size:11px;color:var(--fg-dim);flex:none;max-width:100px}.muted{font-size:13px;color:var(--fg-dim);line-height:1.7;overflow-wrap:anywhere}.issue{padding:16px 0;border-bottom:1px solid var(--line)}.issue h3{margin:0;font-size:15px;color:var(--warning);overflow-wrap:anywhere}.issue p{font-size:13px;overflow-wrap:anywhere}.issue button{min-height:40px;font-size:13px}.settings{display:grid;grid-template-columns:auto 1fr;gap:14px 20px;font-size:13px;margin:20px 0}.settings dt{color:var(--fg-dim)}.settings dd{margin:0;overflow-wrap:anywhere}.setting-actions{display:flex;gap:12px;flex-wrap:wrap;margin:20px 0}.setting-actions button{min-height:42px;white-space:normal}.page-foot{display:flex;justify-content:space-between;flex-wrap:wrap;align-items:center;gap:8px 20px;padding:10px 24px;border-top:1px solid var(--line);font-size:11px;color:var(--fg-dim);flex:none}.page-foot span:first-child{display:flex;align-items:center;gap:7px}.empty{flex:1;min-height:0;overflow:auto;text-align:center;padding:40px 28px;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:14px}.empty h2{font-size:22px;margin:0}.empty p{font-size:14px;color:var(--fg-dim);max-width:460px;margin:0 0 12px}.empty>svg{color:var(--accent);flex:none}.no-results{font-size:14px;color:var(--fg-dim);padding:24px 16px}.back{min-height:40px;margin:-10px 0 16px;padding-left:0;font-size:13px;color:var(--accent)}.sync-page :focus-visible{outline:2px solid var(--accent);outline-offset:2px}.compact .workspace{display:block;overflow:hidden}.compact .pair-list{height:100%;border-right:0;padding:10px 18px}.compact .pair-detail{height:100%;padding:20px 24px 32px}.compact .pair{min-height:104px}.compact .page-head{padding:20px 24px 14px}.compact .page-head p{display:none}.compact .scope-bar{padding:0 24px 12px}.compact .page-foot{padding:9px 24px}.compact .detail-head{margin-bottom:18px}.compact .roots{margin-bottom:16px}@media(max-width:700px){.page-head{gap:12px}.page-head>button{font-size:13px}.scope-bar{gap:8px}.detail-head{flex-wrap:wrap}.detail-actions{max-width:none;justify-content:flex-start}.roots{grid-template-columns:1fr}.roots>svg{display:none}.roots>div{min-height:0}.settings{grid-template-columns:1fr;gap:5px}.settings dd{margin-bottom:10px}.activities li{flex-wrap:wrap}.activities time{margin-left:29px;max-width:none}.page-foot span:last-child{display:none}.empty{justify-content:flex-start;padding-top:30px}}@media(prefers-reduced-transparency:reduce),(prefers-contrast:more){.sync-page{background:var(--canvas-flat)}}
</style>
