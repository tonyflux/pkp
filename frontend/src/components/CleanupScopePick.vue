<script setup lang="ts">
// Scan scope, shared by both Cleanup tabs: the whole drive, or a folder with
// everything below it, chosen in the folder picker.
import { computed } from "vue";
import Icon from "./Icon.vue";
import SelectMenu, { type SelectOption } from "./SelectMenu.vue";
import { cleanup, cleanupScopeLabel, openCleanupPicker, setCleanupScope } from "../store";
import { t } from "../i18n";

// The face says the range in force until the picker picks another, so
// choosing Choose folder… opens the picker and leaves the value as it is.
const value = computed(() => (cleanup.scope.length ? "folder" : "all"));
const options = computed<SelectOption<string>[]>(() => [
  { value: "all", label: t("当前账号全盘") },
  ...(cleanup.scope.length ? [{ value: "folder", label: cleanupScopeLabel.value }] : []),
  { value: "pick", label: t("选择文件夹…") },
]);

function choose(next: string) {
  if (next === "all") setCleanupScope([]);
  else if (next === "pick") openCleanupPicker();
}
</script>

<template>
  <SelectMenu
    class="pick"
    :class="{ busy: !!cleanup.running }"
    :model-value="value"
    :options="options"
    :label="t('扫描范围')"
    :disabled="!!cleanup.running"
    @update:model-value="choose"
  >
    <span>{{ cleanupScopeLabel }}</span>
    <Icon name="chevronDown" :size="15" />
  </SelectMenu>
</template>

<style scoped>
/* The app's own menu under the control's face. */
.pick {
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  min-width: 160px;
  max-width: 280px;
  min-height: 38px;
  padding: 0 12px 0 14px;
  border-radius: 10px;
  border: 1px solid var(--line-strong);
  background: color-mix(in srgb, var(--glass-control) 55%, transparent);
  font-size: 13px;
  font-weight: 600;
}

.pick span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pick.busy {
  opacity: 0.5;
}

.pick > svg {
  flex: none;
  opacity: 0.8;
}
</style>
