<script setup lang="ts">
import { ref } from "vue";
import Icon from "./Icon.vue";
import { updates, remindUpdateLater, ignoreUpdate } from "../updatestore";
import { openSettings } from "../store";
import { t } from "../i18n";
const introduced = ref(localStorage.getItem("pkp.updateNotice") === "1");
function dismissIntro() { introduced.value = true; localStorage.setItem("pkp.updateNotice", "1"); }
function open() { dismissIntro(); openSettings("updates"); }
</script>
<template>
  <aside v-if="updates.state?.supported && (updates.state.notice || (!introduced && updates.state.automatic))" class="update-notice glass glass-sheet" role="status">
    <Icon name="info" :size="18" />
    <span>{{ updates.state.notice ? t("PKP {version} 已发布", { version: updates.state.info?.version ?? '' }) : t("PKP 会自动从 GitHub 检查新版本，不会自动下载或安装，可在「关于与更新」中关闭。") }}</span>
    <div class="actions">
      <button class="quiet" @click="open">{{ t("查看更新") }}</button>
      <template v-if="updates.state.notice">
        <button class="quiet" :disabled="updates.pending" @click="remindUpdateLater">{{ t("稍后提醒") }}</button>
        <button class="quiet" :disabled="updates.pending" @click="ignoreUpdate">{{ t("忽略此版本") }}</button>
      </template>
      <button v-else class="quiet icon" :aria-label="t('关闭')" @click="dismissIntro"><Icon name="close" :size="14" /></button>
    </div>
  </aside>
</template>
<style scoped>
.update-notice { position: fixed; z-index: 33; bottom: 28px; right: 28px; display: flex; align-items: center; flex-wrap: wrap; gap: 12px; max-width: min(540px, calc(100vw - 40px)); padding: 16px; border-radius: 20px; font-size: 13px; line-height: 1.6; }
.update-notice > svg { flex: none; color: var(--accent); }
.update-notice > span { flex: 1; min-width: 160px; }
.actions { display: flex; align-items: center; flex-wrap: wrap; gap: 10px; }
button { font-size: 12px; }
</style>
