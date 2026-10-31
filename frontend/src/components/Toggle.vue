<script setup lang="ts">
// The on/off switch of the settings islands and cards. A switch acts at once:
// whoever owns the value saves it and reverts it if saving fails, so this
// only reports the flip.
defineProps<{ modelValue: boolean; label: string; disabled?: boolean }>();
defineEmits<{ "update:modelValue": [on: boolean] }>();
</script>

<template>
  <button
    class="switch"
    role="switch"
    :aria-checked="modelValue"
    :aria-label="label"
    :title="label"
    :disabled="disabled"
    @click="$emit('update:modelValue', !modelValue)"
  >
    <i />
  </button>
</template>

<style scoped>
.switch {
  position: relative;
  flex: none;
  width: 48px;
  height: 28px;
  min-height: 28px;
  padding: 0;
  border-radius: var(--radius-pill);
  border: 1px solid var(--line-strong);
  background: color-mix(in srgb, var(--fg) 12%, transparent);
  transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease);
}

.switch:hover:not(:disabled) {
  background: color-mix(in srgb, var(--fg) 18%, transparent);
}

.switch i {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: #ffffff;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.3);
  transition: transform var(--dur) var(--ease);
}

.switch[aria-checked="true"] {
  background: var(--primary) border-box;
  border-color: transparent;
}

.switch[aria-checked="true"] i {
  transform: translateX(20px);
}

/* The press shrink of a button would make the thumb jump. */
.switch:active:not(:disabled) {
  transform: none;
}
</style>
