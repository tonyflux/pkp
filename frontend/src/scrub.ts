/*
 * Scrubbing a value by pointer on a painted horizontal track.
 *
 * The bars used to be native <input type="range"> elements hidden under the
 * painted track. WebKit maps a click on a range input through the native
 * slider's geometry — a track inset by ~25px on each side plus half a thumb —
 * not through the element's box, so on a 246px bar a click at 10%
 * produced 0 and a click at 25% landed 6.5 minutes early in a two-hour film.
 * Here the value is simply the pointer's fraction of the track element's
 * width, which is exactly where the played bar and knob are painted.
 */
import { ref, type Ref } from "vue";

export interface Scrub {
  /** True from pointer down to release; the bar shows `value` meanwhile. */
  scrubbing: Ref<boolean>;
  /** The value under the pointer while scrubbing. */
  value: Ref<number>;
  /** Value at a pointer position, or null when the bar cannot be measured. */
  valueAt(event: PointerEvent): number | null;
  onPointerDown(event: PointerEvent): void;
  onPointerMove(event: PointerEvent): void;
  onPointerUp(event: PointerEvent): void;
  onKeyDown(event: KeyboardEvent): void;
}

/**
 * useScrub wires one track element spanning 0..`max`. `max` is read per
 * event; `commit` receives the target when the pointer is released or a key
 * seeks, by `step` (in the bar's unit) for the arrow keys.
 */
export function useScrub(
  el: Ref<HTMLElement | undefined>,
  max: () => number,
  position: () => number,
  commit: (target: number) => void,
  step: (event: KeyboardEvent) => number = () => 5,
): Scrub {
  const scrubbing = ref(false);
  const value = ref(0);
  // The box the gesture started on. Showing the preview value can reflow
  // the controls around the bar (a time label growing from 30:00 to
  // 1:00:00), and the release must be measured against the bar the user
  // pressed on, not where it moved to.
  let gestureBox: DOMRect | null = null;

  function valueAt(event: PointerEvent): number | null {
    const total = max();
    const box = gestureBox ?? el.value?.getBoundingClientRect();
    if (!box || box.width <= 0 || total <= 0) return null;
    const ratio = Math.min(1, Math.max(0, (event.clientX - box.left) / box.width));
    return ratio * total;
  }

  function onPointerDown(event: PointerEvent) {
    if (event.button !== 0) return;
    const target = valueAt(event);
    if (target === null) return;
    // Capture keeps move/up coming to the bar when a drag leaves it. The
    // default is prevented to stop text selection, which also skips the
    // focus a click would give, so focus is taken explicitly.
    const bar = event.currentTarget as HTMLElement;
    bar.setPointerCapture(event.pointerId);
    event.preventDefault();
    bar.focus({ preventScroll: true });
    gestureBox = el.value?.getBoundingClientRect() ?? null;
    scrubbing.value = true;
    value.value = target;
  }

  function onPointerMove(event: PointerEvent) {
    if (!scrubbing.value) return;
    const target = valueAt(event);
    if (target !== null) value.value = target;
  }

  function onPointerUp(event: PointerEvent) {
    if (!scrubbing.value) return;
    const target = event.type === "pointercancel" ? null : (valueAt(event) ?? value.value);
    scrubbing.value = false;
    gestureBox = null;
    if (target !== null) commit(target);
  }

  function onKeyDown(event: KeyboardEvent) {
    const total = max();
    if (total <= 0) return;
    let target: number;
    switch (event.key) {
      case "ArrowLeft":
      case "ArrowDown":
        target = position() - step(event);
        break;
      case "ArrowRight":
      case "ArrowUp":
        target = position() + step(event);
        break;
      case "Home":
        target = 0;
        break;
      case "End":
        target = total;
        break;
      default:
        return;
    }
    // The bar owns these keys; the surrounding view's shortcuts must not
    // seek a second time.
    event.preventDefault();
    event.stopPropagation();
    commit(Math.min(total, Math.max(0, target)));
  }

  return { scrubbing, value, valueAt, onPointerDown, onPointerMove, onPointerUp, onKeyDown };
}
