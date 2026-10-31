<script setup lang="ts">
// The body of the Subtitles and Audio track menus, drawn like Playback speed's (SpeedOptions): a
// heading with the current track and the tracks as a listbox. PlayerView
// owns the tracks and what choosing one does; the playlist's Playback settings rows
// offer the same options in a SelectMenu.
import { computed, ref, useId } from "vue";
import OptionList from "./OptionList.vue";
import type { SelectOption } from "./SelectMenu.vue";
import { t } from "../i18n";

const props = defineProps<{ kind: "subtitle" | "audio"; options: SelectOption<string>[]; value: string; current: string }>();
const emit = defineEmits<{ choose: [value: string]; done: []; close: [] }>();

const id = useId();
const plainList = ref<InstanceType<typeof OptionList>>();

const rows = computed(() =>
  props.options.map((o) => ({ key: o.value, label: o.label, detail: o.detail, selected: o.value === props.value })),
);

function choose(key: string) {
  emit("done");
  if (key !== props.value) emit("choose", key);
}

/** focusCurrent puts focus on the selected row as the menu opens. */
const focusCurrent = () => plainList.value?.focusCurrent();

defineExpose({ focusCurrent });
</script>

<template>
  <div class="track-options">
    <header>
      <strong :id="`${id}-title`">{{ kind === "audio" ? t("音轨") : t("字幕") }}</strong>
      <span>{{ t("当前 {name}", { name: current }) }}</span>
    </header>
    <div class="lists">
      <OptionList
        v-if="rows.length"
        ref="plainList"
        :items="rows"
        :labelledby="`${id}-title`"
        @choose="choose"
        @close="emit('close')"
      />
    </div>
  </div>
</template>

<style scoped>
/* Fixed dark glass in both themes, as Quality's: it sits on the native picture. */
.track-options {
  color: #edf8ff;
  font-size: 13px;
}

header {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 12px;
  padding: 0 3px 10px;
  border-bottom: 1px solid rgba(202, 233, 255, 0.18);
}

header strong {
  flex: none;
  font-size: 16px;
  font-weight: 600;
}

header span {
  min-width: 0;
  font-size: 11px;
  color: #a9c3db;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Files may carry many subtitles. Rows scroll within the window-height bound
   instead of running past the picture; padding keeps focus rings visible. */
.lists {
  max-height: calc(100cqh - 170px);
  overflow-y: auto;
  margin: 0 -3px;
  padding: 0 3px 3px;
}
</style>
