<script setup lang="ts">
// The file page's right column: a fixed 288px beside the canvas, one glass,
// two views of one panel. Overview is the account — storage, transfers, recent
// plays — whatever folder is open; Details is what is picked, or the folder
// itself when nothing is. Picking only changes which view shows: never the
// column's size, the canvas's, or its scroll.
//
// Under 960px it is the Info page of the workspace instead (`page`), with
// no collapse button: Files is the way back.
import { computed, nextTick, ref, watch } from "vue";
import { N_, t } from "../i18n";
import Icon from "./Icon.vue";
import PanelDetails from "./PanelDetails.vue";
import PanelOverview from "./PanelOverview.vue";
import { accountLabel, accounts, currentAccount, drive, setPanelOpen, ui, viewer, type PanelTab } from "../store";
import { vSlide } from "../slide";

defineProps<{ page?: boolean }>();

const tabs: { key: PanelTab; label: string }[] = [
  { key: "overview", label: N_("概览") },
  { key: "details", label: N_("详情") },
];

const body = ref<HTMLElement>();
const tabEls = ref<HTMLButtonElement[]>([]);

// Overview keeps its place across a look at Details; Details starts at the top for
// every new thing it describes.
let overviewTop = 0;
watch(
  () => ui.panelTab,
  async (tab, before) => {
    if (before === "overview" && body.value) overviewTop = body.value.scrollTop;
    await nextTick();
    if (body.value) body.value.scrollTop = tab === "overview" ? overviewTop : 0;
  },
  { flush: "pre" },
);
watch(
  () => drive.picked,
  async () => {
    if (ui.panelTab !== "details") return;
    await nextTick();
    if (body.value) body.value.scrollTop = 0;
  },
);

const multi = computed(() => accounts.list.length > 1);
const currentLabel = computed(() => accountLabel(currentAccount.value));

const caption = computed(() => {
  if (ui.panelTab === "overview") return t("云盘概览 · {name}", { name: multi.value ? currentLabel.value : t("当前账号") });
  if (ui.route === "viewer" && viewer.entry) return t("当前查看 · 文件信息");
  return drive.picked.length ? t("所选文件 · 详情") : t("当前文件夹 · 属性");
});

// A tab strip moves with the arrow keys, as the role promises.
function onTabKey(event: KeyboardEvent, index: number) {
  if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
  event.preventDefault();
  const next = (index + (event.key === "ArrowRight" ? 1 : tabs.length - 1)) % tabs.length;
  ui.panelTab = tabs[next].key;
  tabEls.value[next]?.focus();
}

// Put away, the column hands focus to the toolbar button that brings it
// back, rather than dropping it on the page body.
function collapse() {
  setPanelOpen(false);
  void nextTick(() => document.querySelector<HTMLElement>("[data-panel-toggle]")?.focus());
}
</script>

<template>
  <aside class="panel glass glass-sheet" :class="{ page }" :aria-label="t('上下文信息栏')">
    <header class="head">
      <div v-slide class="tabs" role="tablist" :aria-label="t('信息视图')">
        <button
          v-for="(tab, i) in tabs"
          :id="`panel-tab-${tab.key}`"
          :key="tab.key"
          ref="tabEls"
          role="tab"
          :aria-selected="ui.panelTab === tab.key"
          :aria-controls="`panel-${tab.key}`"
          :tabindex="ui.panelTab === tab.key ? 0 : -1"
          :class="{ on: ui.panelTab === tab.key }"
          @click="ui.panelTab = tab.key"
          @keydown="onTabKey($event, i)"
        >
          {{ t(tab.label) }}
        </button>
      </div>
      <span v-if="multi && currentLabel" class="account-badge" :title="t('当前账号 · {name}', { name: currentLabel })">{{ currentLabel }}</span>
      <button v-if="!page" class="quiet icon collapse" :title="t('收起信息栏')" :aria-label="t('收起信息栏')" @click="collapse">
        <Icon name="panel" :size="19" />
      </button>
    </header>

    <div ref="body" class="body">
      <PanelOverview
        v-if="ui.panelTab === 'overview'"
        id="panel-overview"
        key="overview"
        class="view"
        role="tabpanel"
        aria-labelledby="panel-tab-overview"
      />
      <PanelDetails v-else id="panel-details" key="details" class="view" role="tabpanel" aria-labelledby="panel-tab-details" />
    </div>

    <footer class="foot">
      <Icon name="folder" :size="14" />
      <span>{{ caption }}</span>
    </footer>
  </aside>
</template>

<style scoped>
/* One glass, the canvas's height: the content inside is organised by space
   and hairlines, not by more panes. Its fill is the reading kind
   (--glass-panel): the lists and metadata are read here, over any
   wallpaper; the rim, the edge light and the tab lenses stay glass. */
.panel {
  flex: none;
  width: 288px;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: var(--radius-island);
  background: var(--glass-panel);
}

@media (prefers-reduced-transparency: reduce) {
  .panel {
    background: var(--canvas);
  }
}

/* The Info page under 960px: the workspace's whole width. */
.panel.page {
  flex: 1;
  width: auto;
  min-width: 0;
}

.head {
  flex: none;
  height: 64px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  padding: 10px 10px 10px 16px;
  border-bottom: 1px solid var(--line);
}

.account-badge {
  flex: 0 1 auto;
  min-width: 0;
  max-width: 96px;
  margin-right: auto;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
  color: var(--accent);
  background: var(--accent-soft);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tabs {
  flex: none;
  display: flex;
  align-items: center;
  gap: 2px;
}

.tabs button {
  min-height: 40px;
  padding: 0 12px;
  border-color: transparent;
  background: transparent;
  color: var(--fg-dim);
  font-size: 14px;
  font-weight: 500;
}

.tabs::before {
  background: var(--glass-control);
  border: 1px solid var(--glass-edge);
  box-shadow: inset 0 1px 0 var(--glass-rim);
}

.tabs button.on {
  color: var(--fg);
  font-weight: 600;
  background: transparent;
  border-color: transparent;
}

.collapse {
  flex: none;
  width: 40px;
  height: 40px;
  min-height: 40px;
}

/* The views switch in place, with no animation: a fade held at its first
   frame — WebKit holds animations in a
   window it is not drawing — left the column's middle blank under its
   tabs. */
.body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;
  padding: 20px;
}

.page .body > .view {
  max-width: 420px;
  margin: 0 auto;
}

.foot {
  flex: none;
  height: 40px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 20px;
  border-top: 1px solid var(--line);
  font-size: 12px;
  color: var(--fg-dim);
}

.page .foot {
  justify-content: center;
}
</style>
