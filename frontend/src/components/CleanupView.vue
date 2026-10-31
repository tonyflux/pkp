<script setup lang="ts">
// Cleanup: Duplicate Files and Empty Folders of
// the account on screen, found in the local index. The page frames the two
// tabs and the stages each goes through — scan, result, removal, receipt —
// and the store does the work (store.ts 「Cleanup」).
import { computed, nextTick, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import CleanupConfirm from "./CleanupConfirm.vue";
import CleanupDuplicates from "./CleanupDuplicates.vue";
import CleanupEmptyFolders from "./CleanupEmptyFolders.vue";
import CleanupStatus from "./CleanupStatus.vue";
import { cleanup, currentId, drive, enterCleanupTab, indexReady, indexText, type CleanupTab } from "../store";
import { vSlide } from "../slide";
import { t, N_, num } from "../i18n";

const tabs: { key: CleanupTab; label: string; icon: string }[] = [
  { key: "dup", label: N_("重复文件"), icon: "layers" },
  { key: "empty", label: N_("空文件夹"), icon: "folder" },
];

// What each tab found, once scanned: groups, and top-level folders.
const counts = computed<Record<CleanupTab, number | null>>(() => ({
  dup: cleanup.dup.page?.total ?? null,
  empty: cleanup.empty.page?.folders.length ?? null,
}));

const state = computed(() => cleanup[cleanup.tab]);
const tabRefs = ref<HTMLButtonElement[]>([]);

// Arrow keys move between the tabs (the tablist pattern).
async function onTabKey(event: KeyboardEvent) {
  if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
  event.preventDefault();
  const next = cleanup.tab === "dup" ? "empty" : "dup";
  enterCleanupTab(next);
  await nextTick();
  tabRefs.value[next === "dup" ? 0 : 1]?.focus();
}

// The page opens on the account on screen: one switched to, or an index that
// finished building, scans the tab on screen.
watch([currentId, indexReady], () => enterCleanupTab(cleanup.tab), { immediate: true });
</script>

<template>
  <section class="cleanup canvas" :aria-busy="state.loading || cleanup.running === cleanup.tab">
    <div class="shell">
      <header class="head">
        <div>
          <p class="eyebrow">SPACE, BACK IN YOUR HANDS · PKP</p>
          <h1>{{ t("清理") }}</h1>
          <p class="lede">{{ t("找回被重复副本占用的空间，再整理留下的空文件夹。") }}</p>
        </div>
        <span class="mark" aria-hidden="true"><Icon name="layers" :size="26" /></span>
      </header>

      <div v-slide class="tabs" role="tablist" :aria-label="t('清理类型')" @keydown="onTabKey">
        <button
          v-for="tab in tabs"
          :id="`cleanup-tab-${tab.key}`"
          :key="tab.key"
          ref="tabRefs"
          role="tab"
          class="quiet"
          :aria-selected="cleanup.tab === tab.key"
          :aria-controls="`cleanup-panel-${tab.key}`"
          :tabindex="cleanup.tab === tab.key ? 0 : -1"
          @click="enterCleanupTab(tab.key)"
        >
          <Icon :name="tab.icon" :size="17" />
          {{ t(tab.label) }}
          <span v-if="counts[tab.key] !== null" class="count tnum">{{ num(counts[tab.key]!) }}</span>
        </button>
      </div>

      <div :id="`cleanup-panel-${cleanup.tab}`" class="panel" role="tabpanel" :aria-labelledby="`cleanup-tab-${cleanup.tab}`">
        <div v-if="!indexReady" class="gate">
          <span class="gate-mark"><Orbit :size="22" /></span>
          <h2>{{ t("建完索引才能扫描") }}</h2>
          <p>{{ t("清理只读这台电脑上的本地索引，不请求云端。") }}</p>
          <small>{{ t("本地索引 · {state}", { state: indexText(drive.indexProgress) }) }}</small>
        </div>
        <CleanupDuplicates v-else-if="cleanup.tab === 'dup' && cleanup.dup.stage === 'result'" />
        <CleanupEmptyFolders v-else-if="cleanup.tab === 'empty' && cleanup.empty.stage === 'result'" />
        <CleanupStatus v-else :tab="cleanup.tab" />
      </div>
    </div>
    <CleanupConfirm />
  </section>
</template>

<style scoped>
/* Cleanup canvas: head, tabs under a hairline, then the tab's own
   scroller. The canvas sizes everything, not the window. */
.cleanup {
  container-type: inline-size;
  position: relative;
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  overflow: hidden;
}

.shell {
  --pad: 32px;
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  padding: 26px var(--pad) 12px;
}

.eyebrow {
  margin: 0 0 5px;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.16em;
  color: var(--accent);
}

.head h1 {
  margin: 0;
  font-size: 28px;
  line-height: 34px;
  font-weight: 650;
  letter-spacing: -0.02em;
}

.lede {
  margin: 5px 0 0;
  font-size: 13px;
  color: var(--fg-dim);
}

.mark {
  flex: none;
  display: grid;
  place-items: center;
  width: 54px;
  height: 54px;
  border-radius: 16px;
  border: 1px solid var(--line-strong);
  background: color-mix(in srgb, var(--glass-control) 60%, transparent);
  color: var(--accent);
}

.tabs {
  display: flex;
  gap: 24px;
  padding: 0 var(--pad);
  border-bottom: 1px solid var(--line);
}

.tabs button {
  gap: 8px;
  min-height: 46px;
  padding: 0 2px;
  border: 0;
  border-bottom: 2px solid transparent;
  border-radius: 0;
  background: none;
  font-size: 15px;
  font-weight: 500;
  color: var(--fg-dim);
}

.tabs button:hover:not(:disabled) {
  background: none;
  color: var(--fg);
}

/* The lens here is the 2px underline along the selected tab's bottom edge. */
.tabs::before {
  height: 2px;
  background: var(--accent);
  transform: translate(var(--slide-x, 0), calc(var(--slide-y, 0px) + var(--slide-h, 0px) - 2px));
}

.tabs button[aria-selected="true"] {
  color: var(--fg);
  font-weight: 650;
}

.count {
  padding: 1px 7px;
  border-radius: 7px;
  background: color-mix(in srgb, var(--fg) 9%, transparent);
  font-size: 11px;
  font-weight: 600;
  color: var(--fg-dim);
}

.panel {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.gate {
  margin: auto;
  max-width: 440px;
  padding: 32px 24px;
  text-align: center;
}

.gate-mark {
  display: grid;
  place-items: center;
  width: 58px;
  height: 58px;
  margin: 0 auto 18px;
  border-radius: 18px;
  border: 1px solid var(--line-strong);
  background: color-mix(in srgb, var(--glass-control) 60%, transparent);
}

.gate h2 {
  margin: 0 0 6px;
  font-size: 21px;
}

.gate p {
  margin: 0 0 12px;
  font-size: 13px;
  color: var(--fg-dim);
}

.gate small {
  font-size: 12px;
  color: var(--fg-dim);
}

/* A short window (the 1100×760 default, 960×640) keeps the room for the list. */
@media (max-height: 800px) {
  .head {
    padding-top: 18px;
    padding-bottom: 8px;
  }

  .head h1 {
    font-size: 22px;
    line-height: 28px;
  }

  .lede {
    display: none;
  }

  .mark {
    width: 44px;
    height: 44px;
    border-radius: 13px;
  }

  .tabs button {
    min-height: 40px;
  }
}

@container (max-width: 959px) {
  .shell {
    --pad: 20px;
  }
}

@container (max-width: 650px) {
  .shell {
    --pad: 16px;
  }

  .mark {
    display: none;
  }
}
</style>
