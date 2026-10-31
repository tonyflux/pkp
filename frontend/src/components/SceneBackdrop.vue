<script setup lang="ts">
// The atmosphere layer. It carries no information; it exists so the glass has
// something to refract. It is a snow-and-lake scene cropped from a player
// frame, because a
// generated gradient gave the islands nothing to bend: glass over a flat field
// is indistinguishable from a translucent panel.
//
// `waiting` wakes the scene: while the app is fetching, the light on the lake
// drifts. It is the one place a wait may be ambient rather than a labelled
// indicator — the scene is already decorative.
//
// The native video surface sits *below* the webview. Whenever a page reports a
// stage rectangle, the scene cuts that rectangle out of itself so the picture
// shows through; nothing else in the window paints there.
//
// The picture can be the user's own (Settings · Basic · Wallpaper). One that will not load
// — its file deleted, or no longer an image — gives way to the built-in one.
import { computed } from "vue";
import { forgetWallpaper, holePath, notify, stage, ui, wallpaperSrc } from "../store";
import { t } from "../i18n";

defineProps<{ waiting?: boolean }>();

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
  <div class="scene" :class="{ waiting, custom: !!ui.wallpaper }" :style="clip ? { clipPath: clip } : undefined" aria-hidden="true">
    <img class="wall" :src="wallpaperSrc" alt="" draggable="false" @error="lost" />
    <div v-if="ui.wallpaper" class="dim" />
    <div class="glow" />
    <div class="haze" />
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

/* A warm patch of light on the water that only moves while waiting. */
.glow {
  position: absolute;
  inset: 0;
  background: radial-gradient(
    42% 36% at 62% 68%,
    var(--scene-glow) 0%,
    transparent 70%
  );
  opacity: 0.35;
  mix-blend-mode: screen;
  transition: opacity 0.8s var(--ease);
}

/* The scene darkens slightly towards the edges so the islands' shadows read as
   floating rather than printed. */
.haze {
  position: absolute;
  inset: 0;
  background:
    linear-gradient(180deg, var(--scene-veil-top) 0%, transparent 28%, transparent 70%, var(--scene-veil-bottom) 100%),
    radial-gradient(120% 90% at 50% 50%, transparent 55%, var(--scene-vignette) 100%);
}

.waiting .glow {
  opacity: 0.6;
  animation: drift 5.2s ease-in-out infinite alternate;
}

@keyframes drift {
  from {
    transform: translateX(-28px);
  }
  to {
    transform: translateX(28px);
  }
}
</style>
