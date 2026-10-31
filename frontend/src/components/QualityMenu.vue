<script setup lang="ts">
// The dock's Quality entry and the menu it
// opens above itself. Hidden when the video
// offers a single rendition: there is nothing to choose, and a dead control
// would promise a 4K that is not there.
import { computed, watch } from "vue";
import DockMenu from "./DockMenu.vue";
import QualityOptions from "./QualityOptions.vue";
import { player } from "../store";
import { canChoose, entryValue } from "../quality";
import { t } from "../i18n";

const open = defineModel<boolean>("open", { default: false });

const q = computed(() => player.state.quality);
const value = computed(() => entryValue(q.value));
// A failed switch is shown where it happened: the immersive page has no
// status pill, and the menu that says Switch Failed · Retry is closed by then.
const failed = computed(() => !!q.value.failed && !q.value.switching);
const title = computed(() =>
  failed.value
    ? t("清晰度：{value}（切换失败，打开菜单重试）", { value: value.value })
    : t("清晰度：{value}", { value: value.value }),
);

// A new video may have nothing to choose; the menu goes with the entry.
watch(
  () => canChoose(q.value),
  (can) => {
    if (!can) open.value = false;
  },
);
</script>

<template>
  <DockMenu
    v-if="canChoose(q)"
    v-model:open="open"
    icon="quality"
    :label="t('清晰度')"
    :value="value"
    :title="title"
    :busy="!!q.switching"
    :failed="failed"
  >
    <template #default="{ close }">
      <QualityOptions @done="close" @close="close" />
    </template>
  </DockMenu>
</template>
