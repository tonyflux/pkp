<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { accountName, relocate } from "../store";
import { t } from "../i18n";
import { bytes, shownPath } from "../format";
import { backSyncDraft, browseSyncCloud, closeSyncDraft, pickSyncLocal, previewSyncDraft, startSyncDraft, sync, syncActionName, syncDirectionName, syncDraft, syncModeName, syncModes } from "../syncstore";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import Toggle from "./Toggle.vue";
import SyncModal from "./SyncModal.vue";
const heading = ref<HTMLElement>();
const config = computed(() => syncDraft.config);
const preview = computed(() => syncDraft.preview);
const title = computed(() => syncDraft.view === "preview" ? t("检查同步计划") : config.value?.id ? t("修改同步配对") : t("新建同步"));
const canPreview = computed(() => !!config.value?.name.trim() && !!config.value.localPath && !!config.value.cloudPath && !syncDraft.scanning && !syncDraft.picking && !syncDraft.closing && !syncDraft.localError);
watch(() => syncDraft.view, async () => { await nextTick(); heading.value?.focus({ preventScroll: true }); });
</script>
<template>
  <SyncModal v-if="sync.draftOpen && config && relocate.mode !== 'sync'" :title="title" :busy="syncDraft.starting || syncDraft.closing" @close="syncDraft.scanning ? closeSyncDraft() : backSyncDraft()">
    <p class="owner">{{ t("所属账号") }} · <strong>{{ accountName(config.account) }}</strong></p>
    <p ref="heading" class="step" tabindex="-1">{{ syncDraft.view === 'preview' ? t('2 · 检查与预览') : t('1 · 选择文件夹与方式') }}</p>
    <template v-if="syncDraft.view === 'form'">
      <label class="name-field">{{ t("同步名称") }}<input v-model="config.name" :disabled="syncDraft.scanning" maxlength="160" autocomplete="off" /></label>
      <div class="root-field">
        <div><Icon name="laptop" :size="18" /><strong>{{ t("本地文件夹") }}</strong></div>
        <p>{{ config.localPath || t("尚未选择文件夹") }}</p>
        <button :disabled="syncDraft.scanning || syncDraft.picking" @click="pickSyncLocal">{{ syncDraft.picking ? t("正在加载") : t("选择文件夹…") }}</button>
        <p v-if="syncDraft.localError" class="error" role="alert">{{ syncDraft.localError }}</p>
      </div>
      <div class="root-field">
        <div><Icon name="folder" :size="18" /><strong>{{ t("云端文件夹") }}</strong></div>
        <p>{{ config.cloudPath ? shownPath(config.cloudPath) : t("尚未选择文件夹") }}</p>
        <button :disabled="syncDraft.scanning || syncDraft.picking" @click="browseSyncCloud()">{{ t("选择云端文件夹") }}</button>
      </div>
      <fieldset :disabled="syncDraft.scanning || syncDraft.picking"><legend>{{ t("同步方式") }}</legend>
        <label v-for="mode in syncModes" :key="mode.value" class="mode" :class="{ chosen: config.mode === mode.value }"><input v-model="config.mode" type="radio" name="sync-mode" :value="mode.value" /><span><strong>{{ t(mode.title) }}</strong><small>{{ t(mode.note) }}</small></span><Icon :name="mode.value === 'bidirectional' ? 'repeat' : mode.value === 'backup' ? 'upload' : 'download'" :size="19" /></label>
      </fieldset>
      <div class="delete-choice"><div><strong>{{ t("跟随删除") }}</strong><p class="hint">{{ config.deletes[config.mode] ? t("已跟踪内容的删除会传播到另一端，并移入回收站。") : t("源端删除后保留另一端，不清理目标原有额外文件。") }}</p></div><Toggle v-model="config.deletes[config.mode]" :label="t('跟随删除')" :disabled="syncDraft.scanning" /></div>
      <p class="note">{{ config.id ? t("修改根、方式或删除规则后必须重新预览。取消保留原设置及安全停点。") : t("首次扫描不删除。冲突保留两者，确认预览前不写入。") }}</p>
      <p v-if="syncDraft.scanning" class="working" role="status"><Orbit :size="18" />{{ t("正在核对内容；尚未写入，可以取消。") }}</p>
    </template>
    <template v-else-if="preview">
      <p class="note">{{ t("尚未写入；以下为完整扫描得到的真实计划。") }}</p>
      <p v-if="syncDraft.changed" class="warning" role="status">{{ t("内容或权限已变化。请审阅重新计算的计划，再次确认。") }}</p>
      <div class="preview-roots"><strong>{{ config.name }} · {{ syncModeName(config.mode) }}</strong><p>{{ config.localPath }}</p><p>{{ shownPath(config.cloudPath) }}</p></div>
      <dl class="totals"><div><dt>{{ t("上传") }}</dt><dd>{{ preview.uploads }}</dd></div><div><dt>{{ t("下载") }}</dt><dd>{{ preview.downloads }}</dd></div><div><dt>{{ t("保留两者") }}</dt><dd>{{ preview.conflicts }}</dd></div><div><dt>{{ t("跳过") }}</dt><dd>{{ preview.skipped }}</dd></div><div><dt>{{ t("移到回收站") }}</dt><dd>{{ preview.deletes }}</dd></div></dl>
      <p class="hint">{{ t("计划传输 {size} · 目录 {n} 个", { size: bytes(preview.bytes, '0 B'), n: preview.directories }) }}</p>
      <p v-if="preview.error" class="error" role="alert">{{ preview.error }}</p>
      <p v-if="preview.blocked" class="warning">{{ t("存在阻断项。请先处理名称、权限或目录问题，再重新扫描。") }}</p>
      <ul class="changes"><li v-for="change in preview.changes" :key="change.id" :class="{ warning: change.action === 'blocked' || change.action === 'conflict' }"><div><strong>{{ change.path }}</strong><span>{{ syncActionName(change.action) }}</span></div><p v-if="change.reason" class="hint">{{ change.reason }}</p><p class="hint">{{ syncDirectionName(change.direction) }}<template v-if="change.keepName"> · {{ t("保留名称") }}: {{ change.keepName }}</template></p></li></ul>
      <p v-if="!preview.changes.length && !preview.error" class="hint">{{ t("完整核对后未发现待执行的变化。") }}</p>
      <label class="ack"><input v-model="syncDraft.acknowledged" type="checkbox" :disabled="preview.blocked || !!preview.error || syncDraft.starting" /><span>{{ t("我已审阅计划，理解冲突保留两者及当前删除规则。") }}</span></label>
    </template>
    <p v-if="syncDraft.error" class="error" role="alert">{{ syncDraft.error }}</p>
    <template #footer>
      <span class="runtime hint">{{ t("仅在 PKP 运行时同步") }}</span>
      <template v-if="syncDraft.view === 'preview'"><button :disabled="syncDraft.starting || syncDraft.closing" @click="backSyncDraft">{{ t("上一步") }}</button><button class="primary" :disabled="!syncDraft.acknowledged || preview?.blocked || !!preview?.error || syncDraft.starting || syncDraft.scanning || syncDraft.closing" @click="startSyncDraft"><Orbit v-if="syncDraft.starting" :size="16" />{{ syncDraft.starting ? t("正在重新校验…") : t("开始同步") }}</button></template>
      <template v-else><button :disabled="syncDraft.closing" @click="closeSyncDraft">{{ t("取消") }}</button><button class="primary" :disabled="!canPreview" @click="previewSyncDraft"><Orbit v-if="syncDraft.scanning" :size="16" />{{ t("扫描并预览") }}</button></template>
    </template>
  </SyncModal>
</template>
<style scoped>
.owner{margin:0 0 8px;color:var(--fg-dim);font-size:14px}.owner strong{color:var(--fg)}.step{font-size:13px;color:var(--accent);margin:0 0 20px}.name-field{display:grid;gap:8px;font-weight:600;margin-bottom:16px}.name-field input{width:100%;min-height:44px}.root-field{position:relative;padding:14px 16px;border:1px solid var(--line);border-radius:12px;margin:12px 0;background:var(--glass-inset)}.root-field>div{display:flex;gap:8px;align-items:center}.root-field>p{font-size:13px;margin:8px 0 12px;overflow-wrap:anywhere}.root-field button{min-height:40px}fieldset{margin:20px 0 16px;padding:0;border:0;min-width:0}legend{font-weight:600;margin-bottom:10px}.mode{display:flex;align-items:center;gap:12px;border:1px solid var(--line);padding:12px 14px;margin:8px 0;border-radius:12px;cursor:pointer}.mode.chosen{border-color:var(--accent);background:var(--accent-soft)}.mode input,.ack input{accent-color:var(--accent);width:18px;height:18px;flex:none}.mode span{display:grid;gap:3px;flex:1;min-width:0}.mode small{color:var(--fg-dim);font-size:13px}.delete-choice{display:flex;align-items:center;gap:16px;margin:18px 0}.delete-choice>div{flex:1}.delete-choice p{margin:5px 0}.working{display:flex;align-items:center;gap:8px;font-size:14px}.preview-roots{padding:14px;background:var(--glass-inset);border-radius:12px;font-size:13px}.preview-roots p{margin:6px 0;overflow-wrap:anywhere}.totals{display:flex;flex-wrap:wrap;gap:16px;margin:20px 0}.totals>div{flex:1;min-width:75px}.totals dt{font-size:12px;color:var(--fg-dim)}.totals dd{font-size:26px;font-weight:600;margin:4px 0 0}.changes{list-style:none;padding:0;margin:14px 0}.changes li{padding:12px 0;border-top:1px solid var(--line)}.changes li>div{display:flex;justify-content:space-between;align-items:baseline;gap:12px}.changes strong{overflow-wrap:anywhere;min-width:0}.changes span{flex:none;font-size:13px}.changes p{margin:5px 0;font-size:12px}.ack{display:flex;align-items:flex-start;gap:10px;font-size:13px;margin-top:18px}.runtime{font-size:12px;margin-right:auto;max-width:100%;color:var(--fg-dim)}@media(max-width:520px){.runtime{flex-basis:100%}.changes li>div{flex-wrap:wrap}.totals{gap:12px}}
</style>
