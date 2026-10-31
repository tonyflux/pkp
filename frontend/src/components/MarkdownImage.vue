<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from "vue";
import { t } from "../i18n";
import { markdownImageName } from "../reader-comic";

const props = defineProps<{
  src: string;
  alt: string;
  resolveImage: (relative: string) => Promise<string | null>;
  releaseImage: (url: string) => void;
}>();
const root = ref<HTMLElement>();
const url = ref("");
const state = ref<"waiting" | "loading" | "ready" | "error">("waiting");
let observer: IntersectionObserver | undefined;
let generation = 0;
let visible = false;
let disposed = false;
let release: (() => void) | undefined;

function reset() {
  generation++;
  release?.();
  release = undefined;
  url.value = "";
  state.value = markdownImageName(props.src) ? "waiting" : "error";
  if (visible && state.value === "waiting") void load();
}
async function load() {
  if (disposed || !visible || state.value === "loading" || state.value === "ready") return;
  const relative = markdownImageName(props.src);
  if (!relative) { state.value = "error"; return; }
  const ticket = ++generation;
  const releaseImage = props.releaseImage;
  state.value = "loading";
  try {
    const result = await props.resolveImage(relative);
    if (disposed || ticket !== generation) { if (result) releaseImage(result); return; }
    if (!result) { state.value = "error"; return; }
    url.value = result;
    release = () => releaseImage(result);
    state.value = "ready";
  } catch {
    if (!disposed && ticket === generation) state.value = "error";
  }
}
function imageFailed() {
  release?.();
  release = undefined;
  url.value = "";
  state.value = "error";
}
watch(() => [props.src, props.resolveImage, props.releaseImage], reset, { immediate: true });
onMounted(() => {
  if (typeof IntersectionObserver === "undefined") { visible = true; void load(); return; }
  observer = new IntersectionObserver((entries) => {
    visible = entries.some(entry => entry.isIntersecting);
    if (visible && state.value === "waiting") void load();
  }, { rootMargin: "200px" });
  if (root.value) observer.observe(root.value);
});
onBeforeUnmount(() => {
  disposed = true;
  generation++;
  observer?.disconnect();
  release?.();
});
</script>

<template>
  <span ref="root" class="markdown-image" :aria-busy="state === 'loading'">
    <img v-if="url" :src="url" :alt="alt" decoding="async" @error="imageFailed" />
    <span v-else-if="state === 'error'" class="markdown-image-error" role="status">
      {{ t("未能加载文档图片：{alt}", { alt: alt || t("未命名") }) }}
      <button v-if="markdownImageName(src)" type="button" @click="load">{{ t("重试") }}</button>
    </span>
    <span v-else>{{ t("图片：{alt}", { alt: alt || t("未命名") }) }}</span>
  </span>
</template>

<style scoped>
.markdown-image { display: inline-block; max-width: 100%; min-height: 2em; vertical-align: middle; }
.markdown-image img { display: block; max-width: 100%; height: auto; border-radius: 4px; }
.markdown-image-error { display: inline-flex; align-items: center; flex-wrap: wrap; gap: 8px; color: var(--fg-dim); font-size: .9em; }
.markdown-image button { font: inherit; color: inherit; background: transparent; border: 1px solid var(--line); border-radius: 5px; padding: 3px 8px; cursor: pointer; }
.markdown-image button:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
</style>
