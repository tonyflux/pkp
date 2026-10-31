<script setup lang="ts">
// The file page's batch actions. It acts on what is picked and visible;
// deleting for good asks once, in place, before it goes. Organize these and Rename by rule
// only make a plan (AI Assistant) the user checks and confirms before anything moves.
import { computed, ref, watch } from "vue";
import { useOutsideClick } from "../dom";
import BatchBar from "./BatchBar.vue";
import Icon from "./Icon.vue";
import ConfirmInline from "./ConfirmInline.vue";
import { t } from "../i18n";
import {
  clearSelection,
  deleteForever,
  downloadEntries,
  freedText,
  openCreateShare,
  openRelocate,
  organizeEntries,
  pickAll,
  pickedEntries,
  renameSheet,
  restore,
  setStarred,
  trash,
  ui,
  visibleFiles,
  visibleFolders,
} from "../store";

const confirming = ref(false);
const count = computed(() => pickedEntries.value.length);
const allPicked = computed(() => count.value === visibleFolders.value.length + visibleFiles.value.length);
// Star the set unless every one already is; then the same button clears it.
const allStarred = computed(() => pickedEntries.value.every((e) => e.starred));
// On a canvas narrower than the wide one the bar keeps one or two rows:
// Copy to, Organize these and Rename by rule (the last two only make a plan) wait behind More.
// The menu goes to <body>: inside the bar's glass its own blur would only
// see the bar.
const moreAt = ref<{ right: number; bottom: number } | null>(null);
useOutsideClick(".batch-more, .batch-menu", () => !!moreAt.value, () => (moreAt.value = null));

function toggleMore(ev: MouseEvent) {
  if (moreAt.value) {
    moreAt.value = null;
    return;
  }
  const button = (ev.currentTarget as HTMLElement).getBoundingClientRect();
  const bar = (ev.currentTarget as HTMLElement).closest(".bar")?.getBoundingClientRect() ?? button;
  moreAt.value = { right: window.innerWidth - button.right, bottom: window.innerHeight - bar.top + 8 };
}

function fromMore(action: () => void) {
  moreAt.value = null;
  action();
}

// A different set is a different question.
watch(count, () => {
  confirming.value = false;
  moreAt.value = null;
});
</script>

<template>
  <BatchBar :count="count" :all-picked="allPicked" @pick-all="pickAll" @clear="clearSelection">
    <ConfirmInline
      v-if="confirming"
      :message="t('彻底删除 {n} 项？无法撤销，{freed}。', { n: count, freed: freedText(pickedEntries) })"
      :confirm-label="t('确认删除')"
      :cancel-label="t('保留')"
      danger
      @cancel="confirming = false"
      @confirm="confirming = false; deleteForever(pickedEntries)"
    />

    <template v-else-if="ui.view === 'trash'">
      <button class="quiet" @click="restore(pickedEntries)"><Icon name="restore" :size="17" />{{ t("还原") }}</button>
      <button class="quiet warn-hover" @click="confirming = true"><Icon name="trash" :size="17" />{{ t("彻底删除") }}</button>
    </template>

    <template v-else>
      <button class="quiet folds" :title="t('按文件名整理成影视文件夹结构，执行前可逐项检查')" @click="organizeEntries(pickedEntries)">
        <Icon name="spark" :size="17" />{{ t("整理这些") }}
      </button>
      <button class="quiet folds" :title="t('去掉标签、集数补零或查找替换，先预览再生成计划')" @click="renameSheet.entries = [...pickedEntries]">
        <Icon name="text" :size="17" />{{ t("按规则改名") }}
      </button>
      <button class="quiet" @click="openCreateShare(pickedEntries)"><Icon name="share" :size="17" />{{ t("分享") }}</button>
      <button class="quiet" @click="downloadEntries(pickedEntries)"><Icon name="download" :size="17" />{{ t("下载") }}</button>
      <button class="quiet" @click="openRelocate('move', pickedEntries)"><Icon name="move" :size="17" />{{ t("batch|移动到") }}</button>
      <button class="quiet folds" @click="openRelocate('copy', pickedEntries)"><Icon name="copy" :size="17" />{{ t("batch|复制到") }}</button>
      <button class="quiet" @click="setStarred(pickedEntries, !allStarred)">
        <Icon name="star" :size="17" :filled="allStarred" />{{ allStarred ? t("batch|取消星标") : t("batch|星标") }}
      </button>
      <button class="quiet warn-hover" :title="t('移到回收站后可以还原；空间要等彻底删除后才释放')" @click="trash(pickedEntries)">
        <Icon name="trash" :size="17" />{{ t("batch|移到回收站") }}
      </button>
      <span class="batch-more">
        <button class="quiet" aria-haspopup="menu" :aria-expanded="!!moreAt" @click="toggleMore">
          <Icon name="more" :size="17" filled />{{ t("更多") }}
        </button>
      </span>
      <Teleport to="body">
        <div
          v-if="moreAt"
          class="menu batch-menu glass-sheet"
          role="menu"
          :style="{ right: `${moreAt.right}px`, bottom: `${moreAt.bottom}px` }"
        >
          <button class="quiet" role="menuitem" @click="fromMore(() => openRelocate('copy', pickedEntries))">
            <Icon name="copy" :size="17" />{{ t("复制到…") }}
          </button>
          <button class="quiet" role="menuitem" :title="t('按文件名整理成影视文件夹结构，执行前可逐项检查')" @click="fromMore(() => organizeEntries(pickedEntries))">
            <Icon name="spark" :size="17" />{{ t("整理这些") }}
          </button>
          <button class="quiet" role="menuitem" :title="t('去掉标签、集数补零或查找替换，先预览再生成计划')" @click="fromMore(() => (renameSheet.entries = [...pickedEntries]))">
            <Icon name="text" :size="17" />{{ t("按规则改名") }}
          </button>
        </div>
      </Teleport>
    </template>
  </BatchBar>
</template>

<style scoped>
.batch-more {
  display: none;
}

/* Unless the canvas is wide, eight actions would wrap and isolate the
   destructive action on a second row. Three actions move behind the overflow
   menu and lose their icons on the narrowest canvas. */
@container files (max-width: 1000px) {
  .folds {
    display: none;
  }

  .batch-more {
    display: flex;
  }

  .bar > button > svg,
  .batch-more > button > svg {
    display: none;
  }
}

/* Anchored above the bar; the shared menu recipe supplies the shape and rows. */
.batch-menu {
  position: fixed;
  z-index: 60;
  min-width: 180px;
  border: 1px solid var(--glass-edge);
  animation: batch-menu-in 0.18s var(--ease) both;
}

.batch-menu button {
  gap: 10px;
  padding: 0 12px;
}

@keyframes batch-menu-in {
  from {
    opacity: 0;
    transform: translateY(4px) scale(0.98);
  }
}
</style>
