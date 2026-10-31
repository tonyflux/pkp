<script setup lang="ts">
// A thumbnail fetched over the network. The host owns the skeleton tint;
// decoded pictures become visible without depending on animation frames.
// Falls back to the slot (an icon) when there is no URL or it fails.
import { onMounted, ref, watch } from "vue";

const props = defineProps<{ src?: string; alt: string; lazy?: boolean }>();

const img = ref<HTMLImageElement>();
const loaded = ref(false);
const failed = ref(false);

watch(
  () => props.src,
  () => {
    loaded.value = false;
    failed.value = false;
  },
);

// A cached image can complete before the listener is attached; `load`
// would then never fire and the skeleton would sit on a finished picture.
onMounted(() => {
  const el = img.value;
  if (el?.complete && el.naturalWidth > 0) loaded.value = true;
});
</script>

<template>
  <span class="thumb-img" :class="{ loaded, skel: src && !loaded && !failed }">
    <img
      v-if="src && !failed"
      ref="img"
      :src="src"
      :alt="alt"
      :loading="lazy ? 'lazy' : undefined"
      @load="loaded = true"
      @error="failed = true"
    />
    <span v-else class="fallback"><slot /></span>
  </span>
</template>

<style scoped>
.thumb-img {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  /* The host box owns the radius and the tint; the sweep clips to it. */
  border-radius: inherit;
  background: none;
}

/* Absolutely positioned, not a grid item: WebKit sizes an implicit grid
   track from the image's intrinsic height before resolving `height: 100%`,
   then centres the box-sized picture in that oversized track — a portrait
   thumbnail lands mostly below the box and shows as a blank or a sliver. */
img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
  visibility: hidden;
}

.loaded img {
  visibility: visible;
}

.fallback {
  display: contents;
  color: var(--fg-dim);
}
</style>
