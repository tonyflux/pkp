<script setup lang="ts">
import { computed } from "vue";
import { accountName } from "../store";
import { bytes, shownPath, stamp } from "../format";
import { t } from "../i18n";
import { confirmSyncDialog, sync, syncDialog, syncDirectionName, type SyncNode } from "../syncstore";
import SyncModal from "./SyncModal.vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
const pair = computed(() => sync.pairs.find((p) => p.id === syncDialog.pairID));
const title = computed(() => syncDialog.kind === "remove" ? t("移除同步") : syncDialog.kind === "deletes" ? t("这次删除需要审阅") : t("保留冲突的两个版本"));
const changed = computed(() => syncDialog.kind === "deletes" ? pair.value?.deletePlan?.id !== syncDialog.preview?.id : syncDialog.kind === "conflict" ? !pair.value?.issues.some((issue) => issue.id === syncDialog.issue?.id && issue.keepName === syncDialog.issue?.keepName) : false);
const deletions = computed(() => syncDialog.preview?.changes.filter((change) => change.action === "trash") ?? []);
const sources = computed(() => [{ label: t("本地文件夹"), node: syncDialog.issue?.local }, { label: t("云端文件夹"), node: syncDialog.issue?.cloud }] as { label: string; node: SyncNode | null | undefined }[]);
const mapping = computed(() => {
  const issue = syncDialog.issue;
  if (!issue || !pair.value) return "";
  if (!issue.local || !issue.cloud) return pair.value.mode === "bidirectional" ? t("保留当前修改版本，并在缺失端补回对应文件，不会联动删除。") : t("仅保留目标端修改后的版本，不改动源端，也不会联动删除。");
  if (pair.value.mode === "bidirectional") return t("本地版本保留原名；云端版本保存为「{name}」，最终在两端补齐两个版本。", { name: issue.keepName });
  if (pair.value.mode === "backup") return t("云端编辑版本保存为「{name}」，本地源版本在云端保留原名；本地不变。", { name: issue.keepName });
  return t("本地编辑版本保存为「{name}」，云端源版本在本地保留原名；云端不变。", { name: issue.keepName });
});
</script>
<template>
  <SyncModal v-if="syncDialog.kind && pair" :title="title" :busy="syncDialog.working" @close="syncDialog.kind = ''">
    <p class="owner">{{ pair.name }} · {{ t("所属账号") }} · {{ accountName(pair.account) }}</p>
    <div class="roots"><p>{{ t("本地文件夹") }}: {{ pair.localPath }}</p><p>{{ t("云端文件夹") }}: {{ shownPath(pair.cloudPath) }}</p></div>
    <template v-if="syncDialog.kind === 'remove'">
      <p>{{ t("停用并移除此同步任务后，已同步的本地与云端文件均会保留。") }}</p>
      <p class="hint">{{ t("进行中的步骤将在完成后停止，已发往云端的请求无法撤回；后续如需同步，需重新创建并预览计划。") }}</p>
    </template>
    <template v-else-if="syncDialog.kind === 'conflict' && syncDialog.issue">
      <h3 class="path"><Icon name="file" :size="20" />{{ syncDialog.issue.path }}</h3>
      <p>{{ syncDialog.issue.reason }}</p>
      <p class="hint">{{ t("时间仅供辨认，不用于自动选择覆盖版本。") }}</p>
      <div class="sources"><div v-for="source in sources" :key="source.label"><strong>{{ source.label }}</strong><template v-if="source.node"><p>{{ source.node.directory ? t("文件夹") : bytes(source.node.size, '0 B') }}</p><p>{{ stamp(source.node.modified) }}</p><p class="hint">{{ source.node.hash ? t("内容已核对") : t("内容待核对") }}</p><details v-if="source.node.hash"><summary>{{ t("内容指纹") }}</summary><code>{{ source.node.hash }}</code></details></template><p v-else>{{ t("该端当前不存在") }}</p></div></div>
      <p v-if="syncDialog.issue.local && syncDialog.issue.cloud" class="note">{{ pair.mode === 'bidirectional' ? t("两边都会保留原名和下面这一份文件；真正执行前还会再核对一遍。") : t("只会在目标端保留原版和修改版，不会改动或回写源端。") }}</p>
      <p class="mapping">{{ mapping }}</p>
      <dl class="names"><dt>{{ t("原相对路径") }}</dt><dd>{{ syncDialog.issue.path }}</dd><template v-if="syncDialog.issue.local && syncDialog.issue.cloud"><dt>{{ t("保留名称") }}</dt><dd>{{ syncDialog.issue.keepName || t("尚无可执行的保留名称，请重新检查。") }}</dd></template></dl>
    </template>
    <template v-else-if="syncDialog.kind === 'deletes' && syncDialog.preview">
      <p class="warning">{{ t("这条同步的写入已经暂停；选暂不删除的话，两边文件都不会动。") }}</p>
      <p class="count">{{ t("{n} 个受跟踪文件将移入回收站", { n: syncDialog.preview.deletes }) }}</p>
      <p class="hint">{{ t("另含 {n} 个目录", { n: syncDialog.preview.deleteDirectories }) }}</p>
      <p class="hint">{{ t("上一次同步记录了 {n} 个文件", { n: syncDialog.preview.tracked }) }}<template v-if="syncDialog.preview.tracked > 0"> · {{ t("占受跟踪文件的 {ratio}%", { ratio: (syncDialog.preview.deletes / syncDialog.preview.tracked * 100).toFixed(1) }) }}</template></p>
      <p>{{ t("请检查磁盘连接、文件夹权限和源端内容；这次确认只针对这一批，以后的删除保护依然有效。") }}</p>
      <details class="batch" open><summary>{{ t("查看本批全部删除项") }} · {{ deletions.length }}</summary><ul><li v-for="change in deletions" :key="change.id"><strong>{{ change.path }}</strong><small>{{ change.direction === 'upload' ? t('删除位置：PikPak 回收站') : change.direction === 'download' ? t('删除位置：本地系统回收站') : syncDirectionName(change.direction) }} · {{ change.directory ? t('文件夹') : t('文件') }}</small><small v-if="change.reason">{{ change.reason }}</small></li></ul></details>
      <p class="note">{{ t("这些项目将移至本地或 PikPak 回收站，不会永久删除；若后续范围发生变化，仍需重新确认。") }}</p>
      <label class="ack"><input v-model="syncDialog.acknowledged" type="checkbox" :disabled="changed || syncDialog.working" /><span>{{ t("我都核对好了，确认执行这批删除。") }}</span></label>
    </template>
    <p v-if="changed" class="error" role="alert">{{ t("计划已变化，请关闭后重新审阅。") }}</p>
    <p v-if="syncDialog.error" class="error" role="alert">{{ syncDialog.error }}</p>
    <template #footer>
      <button data-autofocus :disabled="syncDialog.working" @click="syncDialog.kind = ''">{{ syncDialog.kind === 'remove' ? t("保留同步") : syncDialog.kind === 'deletes' ? t("暂不删除") : t("稍后处理") }}</button>
      <button :class="syncDialog.kind === 'conflict' ? 'primary' : 'confirm-danger'" :disabled="syncDialog.working || changed || (syncDialog.kind === 'deletes' && !syncDialog.acknowledged) || (syncDialog.kind === 'conflict' && !syncDialog.issue?.keepName)" @click="confirmSyncDialog"><Orbit v-if="syncDialog.working" :size="16" />{{ syncDialog.working ? t("正在处理…") : syncDialog.kind === 'remove' ? t("确认移除") : syncDialog.kind === 'deletes' ? t("确认这批删除") : t("保留两者") }}</button>
    </template>
  </SyncModal>
</template>
<style scoped>
.owner{margin-top:0;color:var(--fg-dim);font-size:13px;overflow-wrap:anywhere}.roots{background:var(--glass-inset);border-radius:12px;padding:10px 14px;font-size:13px;margin-bottom:20px}.roots p{margin:5px 0}.path{display:flex;gap:10px;font-size:17px;overflow-wrap:anywhere}.path svg{flex:none}.sources{display:grid;grid-template-columns:1fr 1fr;gap:12px;margin:20px 0}.sources>div{background:var(--glass-inset);border:1px solid var(--line);padding:16px;border-radius:12px;min-width:0}.sources strong{font-size:13px}.sources p{margin:8px 0}.sources code{font-size:11px;overflow-wrap:anywhere;white-space:normal}.sources summary{cursor:pointer;font-size:12px}.names{font-size:13px}.names dt{color:var(--fg-dim);margin-top:12px}.names dd{margin:5px 0;overflow-wrap:anywhere;font-weight:600}.count{font-size:22px;font-weight:600}.batch{border:1px solid var(--line);border-radius:12px;padding:12px 14px;margin:20px 0}.batch summary{cursor:pointer;min-height:28px;overflow-wrap:anywhere}.batch ul{padding:0;list-style:none}.batch li{padding:10px 0;border-top:1px solid var(--line);display:grid;gap:4px;overflow-wrap:anywhere}.batch small{color:var(--fg-dim)}.ack{display:flex;gap:10px;align-items:flex-start;font-size:13px}.ack input{width:18px;height:18px;flex:none;accent-color:var(--accent)}@media(max-width:460px){.sources{grid-template-columns:1fr}}
</style>
