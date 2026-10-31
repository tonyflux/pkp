<script setup lang="ts">
// The PKP mark: the PikPak box in blue glass, cut from build/appicon.png
// (the artwork on a transparent square, so `size` is the box it sits in).
// Decorative: the wordmark or the page title beside it names the app.
import mark from "../assets/logo.png";

withDefaults(defineProps<{ size?: number }>(), { size: 44 });
</script>

<template>
  <img class="logo" :src="mark" :width="size" :height="size" alt="" draggable="false" />
</template>

<style scoped>
.logo {
  display: block;
  flex: none;
  filter: drop-shadow(0 4px 10px rgba(31, 95, 208, 0.35));
}
</style>
