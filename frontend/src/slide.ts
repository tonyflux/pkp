import type { ObjectDirective } from "vue";

/*
 * v-slide: a segmented control's selection slides to the chosen option
 * instead of jumping there. The directive only measures: it keeps
 * --slide-x/y/w/h/r on the track at the selected child's box and the track
 * paints the lens as its `::before` (style.css · Sliding selection; each
 * control gives that lens the fill its selected button used to have, and the
 * selected button turns transparent).
 *
 * Selected = a direct child with `.on` or aria-selected/checked/pressed="true".
 * A selection change animates; a resize (window, wrapped labels, counts that
 * change width) or the first sight of the lens jumps.
 */

const SELECTED =
  ':scope > .on, :scope > [aria-selected="true"], :scope > [aria-checked="true"], :scope > [aria-pressed="true"]';

interface Slide {
  mutations: MutationObserver;
  sizes: ResizeObserver | null;
  /** The geometry last written; "" while no lens is drawn. */
  at: string;
}

const slides = new WeakMap<HTMLElement, Slide>();

function place(track: HTMLElement, animate: boolean) {
  const s = slides.get(track);
  if (!s) return;
  const on = track.querySelector<HTMLElement>(SELECTED);
  // offset* is the layout box: a pressed button's scale(0.97), or a sheet
  // scaling in around the control, never leaks into the lens.
  const at = on?.offsetWidth
    ? `${on.offsetLeft} ${on.offsetTop} ${on.offsetWidth} ${on.offsetHeight} ${getComputedStyle(on).borderRadius}`
    : "";
  if (at === s.at) return;
  const jump = !animate || !s.at;
  s.at = at;
  if (!on || !at) {
    track.style.setProperty("--slide-o", "0");
    return;
  }
  if (jump) track.dataset.slide = "still";
  track.style.setProperty("--slide-x", `${on.offsetLeft}px`);
  track.style.setProperty("--slide-y", `${on.offsetTop}px`);
  track.style.setProperty("--slide-w", `${on.offsetWidth}px`);
  track.style.setProperty("--slide-h", `${on.offsetHeight}px`);
  track.style.setProperty("--slide-r", getComputedStyle(on).borderRadius);
  track.style.setProperty("--slide-o", "1");
  if (jump) {
    // Commit the new place while transitions are off, so turning them back
    // on does not animate the jump.
    void getComputedStyle(track, "::before").transform;
    track.dataset.slide = "";
  }
}

function watchChildren(track: HTMLElement, sizes: ResizeObserver | null) {
  if (!sizes) return;
  sizes.observe(track);
  for (const child of Array.from(track.children)) sizes.observe(child);
}

export const vSlide: ObjectDirective<HTMLElement> = {
  mounted(track) {
    track.dataset.slide = "";
    const sizes = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(() => place(track, false));
    const mutations = new MutationObserver((records) => {
      for (const r of records) {
        if (r.type !== "childList" || r.target !== track || !sizes) continue;
        for (const n of Array.from(r.removedNodes)) if (n instanceof Element) sizes.unobserve(n);
        for (const n of Array.from(r.addedNodes)) if (n instanceof Element) sizes.observe(n);
      }
      place(track, true);
    });
    mutations.observe(track, {
      subtree: true,
      childList: true,
      attributes: true,
      attributeFilter: ["class", "aria-selected", "aria-checked", "aria-pressed"],
    });
    slides.set(track, { mutations, sizes, at: "" });
    watchChildren(track, sizes);
    place(track, false);
  },
  unmounted(track) {
    const s = slides.get(track);
    s?.mutations.disconnect();
    s?.sizes?.disconnect();
    slides.delete(track);
  },
};
