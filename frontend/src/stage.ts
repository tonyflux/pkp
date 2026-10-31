/*
 * useStage binds an element to the native video surface. The video is drawn
 * by a view *behind* the webview; the element is only the hole it shows
 * through. Its rectangle has to follow every layout change, because nothing
 * in AppKit knows the page moved. There is one surface, so there is one host
 * at a time: the immersive page, or the separate player window's.
 */
import { onBeforeUnmount, onMounted, type Ref } from "vue";
import { setStage } from "./store";

export function useStage(el: Ref<HTMLElement | undefined>, radius: number | (() => number)) {
  let observer: ResizeObserver | undefined;

  function report(visible = true) {
    const box = el.value?.getBoundingClientRect();
    if (!box) return;
    const r = typeof radius === "function" ? radius() : radius;
    setStage({ x: box.x, y: box.y, width: box.width, height: box.height, visible }, r);
  }

  const onResize = () => report(true);

  onMounted(() => {
    report(true);
    // The host's own size, and the document's: the hole moves when a
    // neighbouring island opens or the window is resized, not only when the
    // host itself changes size.
    observer = new ResizeObserver(onResize);
    if (el.value) observer.observe(el.value);
    observer.observe(document.body);
    window.addEventListener("resize", onResize);
  });

  onBeforeUnmount(() => {
    // Leaving hides the surface; otherwise the picture keeps covering
    // whatever the next page draws in that rectangle.
    report(false);
    observer?.disconnect();
    window.removeEventListener("resize", onResize);
  });

  return { report };
}
