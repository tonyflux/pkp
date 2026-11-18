<script setup lang="ts">
// The scene stays behind the workspace and cuts out the native video stage.
// A user-selected wallpaper remains visible; the built-in scene is subdued.
import { computed } from "vue";
import { forgetWallpaper, holePath, notify, stage, ui, wallpaperSrc } from "../store";
import { t } from "../i18n";

function lost() {
  if (!ui.wallpaper) return;
  forgetWallpaper();
  notify(t("自定义壁纸读不出来，已换回内置壁纸"), "error");
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
    <img class="wall" :src="wallpaperSrc" alt="" draggable="false" @error="lost" />
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
  object-position: 50% 42%;
  user-select: none;
  -webkit-user-drag: none;
}

.scene:not(.custom) .wall {
  opacity: var(--wallpaper-visibility, 0.05);
}

@media (prefers-reduced-transparency: reduce) {
  .scene:not(.custom) .wall { opacity: 0.05; }
}

:root[data-transparency="reduced"] .scene:not(.custom) .wall {
  opacity: 0.05;
}

/* The built-in scene is framed for its lake; a picture of the user's own is
   centred, and washed towards the theme so the glass stays readable on it. */
.custom .wall {
  object-position: 50% 50%;
}

.dim {
  position: absolute;
  inset: 0;
  background: var(--scene-dim);
}

</style>
