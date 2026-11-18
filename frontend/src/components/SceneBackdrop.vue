<script setup lang="ts">
// The scene cuts out the native video stage and shows a custom image when chosen.
import { computed } from "vue";
import { forgetWallpaper, holePath, notify, stage, ui, wallpaperSrc } from "../store";
import { t } from "../i18n";

function lost() {
  if (!ui.wallpaper) return;
  forgetWallpaper();
  notify(t("壁纸加载失败，已切回默认壁纸"), "error");
}

const clip = computed(() => {
  if (!stage.visible || stage.width <= 0 || stage.height <= 0) return "";
  return holePath(
    { x: 0, y: 0, width: window.innerWidth, height: window.innerHeight },
    stage,
    stage.radius,
  );
});
</script>

<template>
  <div class="scene" :class="{ custom: !!ui.wallpaper }" :style="clip ? { clipPath: clip } : undefined" aria-hidden="true">
    <img v-if="ui.wallpaper" class="wall" :src="wallpaperSrc" alt="" draggable="false" @error="lost" />
    <div v-if="ui.wallpaper" class="dim" />
  </div>
</template>

<style scoped>
.wall {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
  object-position: 50% 50%;
  user-select: none;
  -webkit-user-drag: none;
}


.dim {
  position: absolute;
  inset: 0;
  background: var(--scene-dim);
}

</style>
