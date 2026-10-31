<script setup lang="ts">
// The body of the Quality menu: a heading with
// what plays now, the options as a listbox (OptionList), and the note that
// the choice keeps the position. The dock's own Quality button and the dock's
// More popover both show it, so the wording and order are the same in both.
import { computed, ref, useId } from "vue";
import OptionList from "./OptionList.vue";
import { player, playerCommands } from "../store";
import { qualityItems, qualityLabel, playingLabel } from "../quality";
import { t } from "../i18n";

const emit = defineEmits<{ done: []; close: [] }>();

const id = useId();
const options = ref<InstanceType<typeof OptionList>>();

const q = computed(() => player.state.quality);
const busy = computed(() => !!q.value.switching);
const items = computed(() =>
  qualityItems(q.value).map((r) => ({
    key: r.choice,
    label: r.label,
    detail: q.value.switching === r.choice ? t("切换中…") : r.detail,
    selected: r.selected,
    pending: q.value.switching === r.choice,
  })),
);

function labelOf(key: string): string {
  const o = q.value.options?.find((x) => x.key === key);
  return o ? qualityLabel(o) : "";
}

const heading = computed(() => {
  if (busy.value) return t("切换到 {name}…", { name: labelOf(q.value.switching) });
  const playing = playingLabel(q.value);
  return q.value.lowered ? t("网络较慢 · 已降至 {name}", { name: playing }) : t("当前 {name}", { name: playing });
});

const failure = computed(() => {
  if (!q.value.failed) return "";
  return t("{name}：{error}", { name: labelOf(q.value.failed), error: q.value.error || t("无法打开") });
});

function choose(key: string) {
  emit("done");
  const item = items.value.find((r) => r.key === key);
  if (item?.selected && !q.value.failed) return;
  void playerCommands.quality(key);
}

function retry() {
  emit("done");
  void playerCommands.quality(q.value.choice);
}

/** focusCurrent puts focus on the selected row, as the menu opens. */
const focusCurrent = () => options.value?.focusCurrent();

defineExpose({ focusCurrent });
</script>

<template>
  <div class="quality-options">
    <header>
      <strong :id="`${id}-title`">{{ t("清晰度") }}</strong>
      <span>{{ heading }}</span>
    </header>
    <p v-if="failure" class="failed" role="alert" :title="failure">
      <span>{{ t("切换失败") }}</span>
      <i aria-hidden="true">·</i>
      <button type="button" class="retry" :disabled="busy" @click="retry">{{ t("重试") }}</button>
    </p>
    <OptionList
      ref="options"
      :items="items"
      :labelledby="`${id}-title`"
      :busy="busy"
      @choose="choose"
      @close="emit('close')"
    />
    <p class="note">{{ t("切换后将从当前时间点继续播放。可用档位由视频源决定。") }}</p>
  </div>
</template>

<style scoped>
/* Fixed dark glass in both themes because the native picture gives the
   webview backdrop-filter nothing to sample. */
.quality-options {
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

/* What plays now, or why it was lowered: a status, so it wraps to a second
   line rather than lose its end. */
header span {
  min-width: 0;
  font-size: 11px;
  color: #a9c3db;
  text-align: right;
  text-wrap: balance;
}

.failed {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 8px 0 0;
  padding: 6px 9px;
  border-radius: 11px;
  border: 1px solid rgba(255, 150, 140, 0.4);
  background: rgba(255, 110, 100, 0.14);
  color: #ffd9d4;
  font-size: 12px;
}

.failed i {
  font-style: normal;
  opacity: 0.6;
}

.retry {
  min-height: 24px;
  padding: 0 8px;
  border-radius: 8px;
  border: 1px solid rgba(255, 217, 212, 0.4);
  background: transparent;
  color: #fff3f1;
  font-size: 12px;
  font-weight: 600;
}

.retry:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.12);
}

.note {
  margin: 0;
  padding: 10px 3px 0;
  color: #9bb8d1;
  font-size: 11px;
  line-height: 1.45;
}
</style>
