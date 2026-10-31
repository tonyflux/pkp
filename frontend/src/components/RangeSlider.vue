<script setup lang="ts">
// A painted min–max slider (Volume, Subtitle Size, Subtitle Position). The native
// <input type="range"> is not used: WebKit maps a click through its own inset
// track rather than the element's box, so clicks landed tens of percent off
// (often at the minimum), and its thumb ignored the glass styling. Here the
// value is the pointer's fraction of the track, and the knob is inset by its
// own radius so it never leaves the track at either end.
import { computed, ref, watch } from "vue";
import { useScrub } from "../scrub";

const props = withDefaults(
  defineProps<{ modelValue: number; label: string; min?: number; max?: number; step?: number; keyStep?: number }>(),
  { min: 0, max: 100, step: 1, keyStep: 5 },
);
const emit = defineEmits<{ "update:modelValue": [value: number] }>();

const span = () => props.max - props.min;
function snap(v: number) {
  const stepped = props.min + Math.round((v - props.min) / props.step) * props.step;
  return Math.min(props.max, Math.max(props.min, stepped));
}

const track = ref<HTMLElement>();
// Shift doubles the key step. The bar owns these keys; the player's own arrow
// shortcuts must not also fire (useScrub stops them).
const scrub = useScrub(
  track,
  span,
  () => value.value - props.min,
  (v) => {
    const next = snap(props.min + v);
    // A click already sent its value on press; at either end a key changes nothing.
    if (next !== props.modelValue) emit("update:modelValue", next);
  },
  (e) => (e.shiftKey ? 2 : 1) * props.keyStep,
);

// While dragging, the shown value follows the pointer even before the new
// value is echoed back.
const value = computed(() => snap(scrub.scrubbing.value ? props.min + scrub.value.value : props.modelValue));
// The knob sits on the value, not under the pointer: put under the pointer on
// press and on the step on release, a click made it twitch.
const percent = computed(() => ((value.value - props.min) / span()) * 100);

// A drag is heard as it happens, not on release: each change of the value
// shown goes out at once.
watch(value, (v) => {
  if (scrub.scrubbing.value) emit("update:modelValue", v);
});
</script>

<template>
  <div
    class="rslider"
    role="slider"
    tabindex="0"
    :aria-label="label"
    :aria-valuemin="min"
    :aria-valuemax="max"
    :aria-valuenow="value"
    @pointerdown="scrub.onPointerDown"
    @pointermove="scrub.onPointerMove"
    @pointerup="scrub.onPointerUp"
    @pointercancel="scrub.onPointerUp"
    @keydown="scrub.onKeyDown"
  >
    <div ref="track" class="track">
      <i class="fill" :style="{ width: percent + '%' }" />
      <i class="knob" :style="{ left: percent + '%' }" />
    </div>
  </div>
</template>

<style scoped>
.rslider {
  --knob: 14px;
  flex: 1;
  min-width: 0;
  height: 24px;
  display: flex;
  align-items: center;
  /* The knob's half-width on each side, so its centre can reach both ends
     of the track without spilling outside the control. */
  padding: 0 calc(var(--knob) / 2);
  cursor: pointer;
  touch-action: none;
  outline: none;
}

.track {
  position: relative;
  flex: 1;
  height: 4px;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.22);
}

.fill {
  position: absolute;
  inset: 0 auto 0 0;
  border-radius: inherit;
  background: var(--progress);
}

.knob {
  position: absolute;
  top: 50%;
  width: var(--knob);
  height: var(--knob);
  border-radius: 50%;
  background: #ffffff;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.4);
  transform: translate(-50%, -50%);
}

.rslider:focus-visible .knob {
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--progress-from) 60%, transparent);
}
</style>
