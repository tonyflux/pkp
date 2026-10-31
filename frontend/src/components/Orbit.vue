<script setup lang="ts">
// The one loading indicator. A satellite on a faint ring, trailing a comet
// tail that breathes as it goes round. Drawn at the icon set's 1.6px line
// weight and in currentColor so it sits inside any button, pill or panel
// without a colour of its own.
//
// Fast on purpose (0.9s per revolution): the same request feels shorter
// behind a quick spinner than a slow one. Under prefers-reduced-motion the
// global rule runs the loop once, leaving a still ring with a dot.
import { t } from "../i18n";

withDefaults(defineProps<{ size?: number; label?: string }>(), { size: 18 });
</script>

<template>
  <span class="orbit" role="status" :aria-label="label ?? t('加载中')" :style="{ width: size + 'px', height: size + 'px' }">
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <circle class="track" cx="12" cy="12" r="9" />
      <circle class="tail" cx="12" cy="12" r="9" pathLength="100" />
      <circle class="moon" cx="21" cy="12" r="1.9" />
    </svg>
  </span>
</template>

<style scoped>
.orbit {
  display: inline-grid;
  place-items: center;
  flex: none;
  color: inherit;
}

svg {
  width: 100%;
  height: 100%;
  overflow: visible;
  animation: orbit-spin 0.9s linear infinite;
}

.track,
.tail {
  fill: none;
  stroke: currentColor;
  stroke-width: 1.6;
}

.track {
  opacity: 0.18;
}

/* The dash is the last 14 units of a 100-unit path, so it ends at the path
   start (3 o'clock) where the moon sits; see orbit-tail in style.css. */
.tail {
  stroke-linecap: round;
  stroke-dasharray: 14 100;
  stroke-dashoffset: -86;
  animation: orbit-tail 1.6s ease-in-out infinite;
}

.moon {
  fill: currentColor;
}
</style>
