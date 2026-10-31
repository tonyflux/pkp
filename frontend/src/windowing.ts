// Windowed rendering for long lists in one scroller. Only the rows near the
// visible part of the scroller are in the page; padding on each list stands
// in for the others, so the scrollbar and every drawn row sit where they
// would with the whole list drawn. The transfer centre lists every task an
// account ever ran — thousands — and with all of them in the page WebKit
// spent most of each scrolled frame on the off-screen ones, while a filter,
// ⌘A or a search re-rendered every row.
//
// A row is measured once drawn; until then it is as tall as the first
// measured row of its shape. When a measurement moves what the reader is
// looking at, the scroll position follows it (WebKit has no scroll anchoring
// of its own). The focused row stays in the page while scrolled away, so the
// keyboard never loses its place.
import { computed, nextTick, onBeforeUnmount, onMounted, onUpdated, ref, shallowRef, type ComputedRef, type Ref } from "vue";

/** Rows drawn beyond each edge of the visible part, in px: room for a fast fling. */
const OVERSCAN = 600;
/** A row's height before any row of its shape has been measured. */
const GUESS = 72;

export interface RowLayout {
  /** tops[k] is where row k starts, from the top of the list's content. */
  tops: Float64Array;
  heights: Float64Array;
  /** The list's content height: every row and the gaps between them. */
  total: number;
}

/** layoutRows stacks rows of the given heights `gap` apart. */
export function layoutRows(heights: Float64Array, gap: number): RowLayout {
  const tops = new Float64Array(heights.length);
  let y = 0;
  for (let k = 0; k < heights.length; k++) {
    tops[k] = y;
    y += heights[k] + gap;
  }
  return { tops, heights, total: heights.length ? y - gap : 0 };
}

/** rowAt is the first row whose bottom edge is below y, or the row count when none is. */
function rowAt({ tops, heights }: RowLayout, y: number): number {
  let lo = 0;
  let hi = tops.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (tops[mid] + heights[mid] <= y) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

export interface RowSlice {
  /** Rows start to end - 1 are drawn. */
  start: number;
  end: number;
  /** Room kept above the first drawn row and below the last, gaps included. */
  before: number;
  after: number;
}

/** sliceRows is the run of rows reaching into [from, to), in list coordinates. */
export function sliceRows(layout: RowLayout, from: number, to: number): RowSlice {
  const { tops, heights, total } = layout;
  const start = rowAt(layout, from);
  let lo = start;
  let hi = tops.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (tops[mid] < to) lo = mid + 1;
    else hi = mid;
  }
  if (lo === start) return { start, end: start, before: total, after: 0 };
  return { start, end: lo, before: tops[start], after: total - tops[lo - 1] - heights[lo - 1] };
}

/** One list in the scroller. Its element carries data-list="<id>", each drawn row data-row="<key>". */
export interface RowList<T> {
  id: string;
  items: T[];
}

export interface DrawnRow<T> {
  item: T;
  index: number;
  /** Only on the focused row kept while scrolled away: it stays where it belongs, out of the flow. */
  style?: Record<string, string>;
}

export interface ListWindow<T> {
  rows: DrawnRow<T>[];
  /** The list element's padding, standing in for the rows not drawn. */
  style: Record<string, string>;
}

export interface RowWindows<T> {
  /** Per list id: what to draw. The same object while nothing drawn changes. */
  windows: ComputedRef<Record<string, ListWindow<T>>>;
  /** reveal scrolls a row into view, draws it and returns its element. */
  reveal: (key: string) => Promise<HTMLElement | null>;
}

export interface RowWindowOptions<T> {
  scroller: Ref<HTMLElement | null>;
  /** The lists in page order. */
  lists: () => RowList<T>[];
  key: (item: T) => string;
  /** Rows of one shape are equally tall: the first measured one stands in for the others. */
  shape: (item: T) => string;
}

interface Anchor {
  key: string;
  /** Where the row starts, from the top edge of the visible part. */
  offset: number;
}

/** near says two measurements name the same places, to the sub-pixel. */
function near(a: Record<string, number>, b: Record<string, number>): boolean {
  const ids = Object.keys(a);
  return ids.length === Object.keys(b).length && ids.every((id) => id in b && Math.abs(a[id] - b[id]) < 0.01);
}

export function useRowWindows<T>({ scroller, lists: listsOf, key, shape }: RowWindowOptions<T>): RowWindows<T> {
  const lists = computed(listsOf);
  // The visible part of the scroller, in its content's coordinates.
  const top = ref(0);
  const height = ref(0);
  let width = 0;
  // As last measured: where each list's content starts in the scroller's
  // content, and the gap between its rows.
  const listTops = shallowRef<Record<string, number>>({});
  const gaps = shallowRef<Record<string, number>>({});
  const measured = new Map<string, number>();
  const shapes = new Map<string, number>();
  // Bumped when a measurement changes a height the layout used.
  const version = ref(0);
  const focused = ref("");

  const where = computed(() => {
    const map = new Map<string, { list: RowList<T>; index: number }>();
    for (const list of lists.value) list.items.forEach((item, index) => map.set(key(item), { list, index }));
    return map;
  });

  const layouts = computed(() => {
    void version.value;
    const out: Record<string, RowLayout> = {};
    for (const list of lists.value) {
      const heights = new Float64Array(list.items.length);
      list.items.forEach((item, k) => {
        heights[k] = measured.get(key(item)) ?? shapes.get(shape(item)) ?? GUESS;
      });
      out[list.id] = layoutRows(heights, gaps.value[list.id] ?? 0);
    }
    return out;
  });

  // Scrolling within the drawn rows changes nothing here, so the page does
  // not re-render until a row comes into or leaves the window.
  let drawn: { sig: string; items: T[] }[] = [];
  const windows = computed<Record<string, ListWindow<T>>>((previous) => {
    const kept = where.value.get(focused.value);
    const plan = lists.value.map((list) => {
      const layout = layouts.value[list.id];
      const at = listTops.value[list.id] ?? 0;
      const slice = sliceRows(layout, top.value - OVERSCAN - at, top.value + height.value + OVERSCAN - at);
      const keep = kept?.list === list && (kept.index < slice.start || kept.index >= slice.end) ? kept.index : -1;
      const sig = [list.id, slice.start, slice.end, slice.before, slice.after, keep, keep < 0 ? 0 : layout.tops[keep]].join(":");
      return { list, layout, slice, keep, sig };
    });
    if (previous && plan.length === drawn.length && plan.every((p, i) => p.sig === drawn[i].sig && p.list.items === drawn[i].items)) {
      return previous;
    }
    drawn = plan.map((p) => ({ sig: p.sig, items: p.list.items }));
    const out: Record<string, ListWindow<T>> = {};
    for (const { list, layout, slice, keep } of plan) {
      const rows: DrawnRow<T>[] = [];
      for (let k = slice.start; k < slice.end; k++) rows.push({ item: list.items[k], index: k });
      if (keep >= 0) {
        const row = { item: list.items[keep], index: keep, style: { position: "absolute", top: `${layout.tops[keep]}px`, left: "0", right: "0" } };
        if (keep < slice.start) rows.unshift(row);
        else rows.push(row);
      }
      out[list.id] = { rows, style: { paddingTop: `${slice.before}px`, paddingBottom: `${slice.after}px` } };
    }
    return out;
  });

  /** anchor is the first row reaching below the top of the visible part, by the layout drawn. */
  function anchor(): Anchor | null {
    const el = scroller.value;
    if (!el) return null;
    const y = el.scrollTop;
    for (const list of lists.value) {
      const at = listTops.value[list.id];
      if (at === undefined) continue;
      const layout = layouts.value[list.id];
      const k = rowAt(layout, y - at);
      if (k < list.items.length) return { key: key(list.items[k]), offset: at + layout.tops[k] - y };
    }
    return null;
  }

  function measureLayout() {
    const el = scroller.value;
    if (!el) return;
    const origin = el.getBoundingClientRect().top + el.clientTop - el.scrollTop;
    const nextTops: Record<string, number> = {};
    const nextGaps: Record<string, number> = {};
    for (const list of Array.from(el.querySelectorAll<HTMLElement>("[data-list]"))) {
      const id = list.dataset.list ?? "";
      nextTops[id] = list.getBoundingClientRect().top + list.clientTop - origin;
      nextGaps[id] = parseFloat(getComputedStyle(list).rowGap) || 0;
    }
    if (!near(nextTops, listTops.value)) listTops.value = nextTops;
    if (!near(nextGaps, gaps.value)) gaps.value = nextGaps;
  }

  /** measureRows records every drawn row's height; true when one differs from what the layout used. */
  function measureRows(): boolean {
    const el = scroller.value;
    if (!el) return false;
    let moved = false;
    for (const row of Array.from(el.querySelectorAll<HTMLElement>("[data-row]"))) {
      const k = row.dataset.row ?? "";
      const at = where.value.get(k);
      if (!at) continue;
      const s = shape(at.list.items[at.index]);
      // Sub-pixel: a card with a progress bar is not a whole number of px.
      const h = row.getBoundingClientRect().height;
      if (Math.abs(h - (measured.get(k) ?? shapes.get(s) ?? GUESS)) > 0.01) moved = true;
      measured.set(k, h);
      // A list's last row often ends differently (no divider under it).
      if (!shapes.has(s) && at.index < at.list.items.length - 1) shapes.set(s, h);
    }
    return moved;
  }

  // One anchor per round of measurements: the place the reader saw before
  // the first of them, restored once the page has settled.
  let holding = false;
  function relayout(held: Anchor | null) {
    version.value++;
    if (holding) return;
    holding = true;
    void nextTick(() => {
      holding = false;
      restore(held);
    });
  }

  function restore(held: Anchor | null) {
    const el = scroller.value;
    const at = held && where.value.get(held.key);
    if (!el || !held || !at) return;
    measureLayout();
    const want = (listTops.value[at.list.id] ?? 0) + layouts.value[at.list.id].tops[at.index] - held.offset;
    if (Math.abs(want - el.scrollTop) < 0.5) return;
    el.scrollTop = want;
    top.value = el.scrollTop;
  }

  function afterRender() {
    const held = holding ? null : anchor();
    measureLayout();
    if (measureRows()) relayout(held);
  }

  const observer = new ResizeObserver(() => {
    const el = scroller.value;
    if (!el) return;
    height.value = el.clientHeight;
    if (el.clientWidth === width) return;
    width = el.clientWidth;
    // At another width rows wrap and pad differently: measure them all again.
    const held = holding ? null : anchor();
    measured.clear();
    shapes.clear();
    measureLayout();
    measureRows();
    relayout(held);
  });

  function onScroll() {
    top.value = scroller.value?.scrollTop ?? 0;
  }

  function onFocusIn(event: FocusEvent) {
    focused.value = event.target instanceof HTMLElement ? (event.target.closest<HTMLElement>("[data-row]")?.dataset.row ?? "") : "";
  }

  function onFocusOut(event: FocusEvent) {
    if (!(event.relatedTarget instanceof Node && scroller.value?.contains(event.relatedTarget))) focused.value = "";
  }

  async function reveal(k: string): Promise<HTMLElement | null> {
    const el = scroller.value;
    const at = where.value.get(k);
    if (!el || !at) return null;
    const layout = layouts.value[at.list.id];
    const y = (listTops.value[at.list.id] ?? 0) + layout.tops[at.index];
    if (y < el.scrollTop) el.scrollTop = y;
    else if (y + layout.heights[at.index] > el.scrollTop + el.clientHeight) el.scrollTop = y + layout.heights[at.index] - el.clientHeight;
    top.value = el.scrollTop;
    await nextTick();
    return Array.from(el.querySelectorAll<HTMLElement>("[data-row]")).find((row) => row.dataset.row === k) ?? null;
  }

  onMounted(() => {
    const el = scroller.value;
    if (!el) return;
    el.addEventListener("scroll", onScroll, { passive: true });
    el.addEventListener("focusin", onFocusIn);
    el.addEventListener("focusout", onFocusOut);
    top.value = el.scrollTop;
    height.value = el.clientHeight;
    width = el.clientWidth;
    observer.observe(el);
    afterRender();
  });
  onUpdated(afterRender);
  onBeforeUnmount(() => {
    observer.disconnect();
    const el = scroller.value;
    el?.removeEventListener("scroll", onScroll);
    el?.removeEventListener("focusin", onFocusIn);
    el?.removeEventListener("focusout", onFocusOut);
  });

  return { windows, reveal };
}
