<script setup lang="ts">
// The body of the Playback Speed menu, drawn like Quality's (QualityOptions): a heading
// with the current speed and the speeds as a listbox. The dock's own Playback Speed
// entry shows them as rows; More, where Quality's list follows, as a compact grid.
import { computed, ref, useId } from "vue";
import OptionList from "./OptionList.vue";
import { player, playerCommands } from "../store";
import { t } from "../i18n";

defineProps<{ compact?: boolean }>();
const emit = defineEmits<{ done: []; close: [] }>();

const SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 2];

const id = useId();
const options = ref<InstanceType<typeof OptionList>>();
const items = computed(() =>
  SPEEDS.map((s) => ({
    key: String(s),
    label: `${s}×`,
    detail: s === 1 ? t("正常") : "",
    selected: s === player.state.speed,
  })),
);

function choose(key: string) {
  emit("done");
  const speed = Number(key);
  if (speed !== player.state.speed) void playerCommands.speed(speed);
}

/** focusCurrent puts focus on the selected row, as the menu opens. */
const focusCurrent = () => options.value?.focusCurrent();

defineExpose({ focusCurrent });
</script>

<template>
  <div class="speed-options">
    <header>
      <strong :id="`${id}-title`">{{ t("倍速") }}</strong>
      <span>{{ t("当前 {name}", { name: `${player.state.speed}×` }) }}</span>
    </header>
    <OptionList
      ref="options"
      :items="items"
      :labelledby="`${id}-title`"
      :columns="compact ? 3 : 1"
      @choose="choose"
      @close="emit('close')"
    />
  </div>
</template>

<style scoped>
/* Fixed dark glass in both themes, as Quality's: it sits on the native picture. */
.speed-options {
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
  font-size: 11px;
  color: #a9c3db;
  white-space: nowrap;
}
</style>
