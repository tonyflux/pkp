<script setup lang="ts">
// One metered axis of the transfer allowance: what was drawn against the
// month's cap. A zero limit is an axis without a cap, which reads as such
// instead of as a full bar.
import { computed } from "vue";
import { bytes } from "../format";
import { t } from "../i18n";
import type { Usage } from "../../bindings/github.com/tonyflux/pkp/internal/services";

const props = defineProps<{ label: string; usage: Usage }>();

const ratio = computed(() => (props.usage.limit > 0 ? props.usage.used / props.usage.limit : 0));
const tone = computed(() => (ratio.value >= 1 ? "over" : ratio.value >= 0.8 ? "near" : ""));
const used = computed(() => bytes(props.usage.used, "0 B", 1));
</script>

<template>
  <div class="meter" :class="tone">
    <div class="row">
      <span class="label">{{ label }}</span>
      <span v-if="usage.limit > 0" class="value">
        {{ used }} <em>/ {{ bytes(usage.limit, "0 B", 1) }}</em>
      </span>
      <span v-else class="value">{{ used }} <em>· {{ t("不限") }}</em></span>
    </div>
    <div
      v-if="usage.limit > 0"
      class="track"
      role="meter"
      :aria-label="label"
      :aria-valuenow="usage.used"
      aria-valuemin="0"
      :aria-valuemax="usage.limit"
    >
      <i :style="{ width: `${Math.min(100, Math.max(ratio * 100, usage.used > 0 ? 1.5 : 0))}%` }" />
    </div>
    <small v-if="tone === 'over'">{{ t("已超出 {size}", { size: bytes(usage.used - usage.limit, "0 B", 1) }) }}</small>
  </div>
</template>

<style scoped>
.meter {
  display: flex;
  flex-direction: column;
  gap: 7px;
  min-width: 0;
}

/* Label and figures share a line while they fit; a narrow island moves the
   figures below while keeping them right-aligned instead of breaking labels. */
.row {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: 2px 10px;
  font-size: 13px;
}

.label {
  font-weight: 600;
  color: var(--fg);
}

.value {
  margin-left: auto;
  font-variant-numeric: tabular-nums;
  color: var(--fg);
  white-space: nowrap;
}

.value em {
  font-style: normal;
  color: var(--fg-dim);
}

.track {
  height: 6px;
  border-radius: var(--radius-pill);
  background: color-mix(in srgb, var(--fg) 10%, transparent);
  overflow: hidden;
}

.track i {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--primary);
  transition: width var(--dur) var(--ease);
}

.near .track i {
  background: var(--warning);
}

.over .track i {
  background: var(--danger);
}

small {
  font-size: 12px;
  color: var(--danger);
}
</style>
