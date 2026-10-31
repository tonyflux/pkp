export interface ComicPage { id: string; name: string; url?: string }
export type ComicMode = "single" | "double" | "strip";
export type ComicDirection = "ltr" | "rtl";
export interface ComicImage { url: string; release: () => void }
export interface ComicImageState { status: "loading" | "ready" | "error"; image?: ComicImage; error?: unknown }

const natural = new Intl.Collator("en", { numeric: true, sensitivity: "base" });
export function naturalComicPages(pages: ComicPage[]): ComicPage[] {
  return [...pages].sort((a, b) => natural.compare(a.name, b.name) || (a.name < b.name ? -1 : a.name > b.name ? 1 : 0) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}
export function comicRasterType(name: string): string | null {
  if (!name.includes(".")) return null;
  const extension = name.toLowerCase().split(".").pop();
  return ({ jpg: "image/jpeg", jpeg: "image/jpeg", png: "image/png", gif: "image/gif", webp: "image/webp", avif: "image/avif", bmp: "image/bmp" } as Record<string, string>)[extension ?? ""] ?? null;
}

/** Only plain same-directory raster names reach the resolver, never a browser URL. */
export function markdownImageName(source: string): string | null {
  let name: string;
  try { name = decodeURIComponent(source); } catch { return null; }
  if (name.startsWith("./")) name = name.slice(2);
  if (!name || name !== name.trim() || /[\x00-\x1f\x7f/\\:?#]/.test(name) || name === "." || name === ".." || !comicRasterType(name)) return null;
  return name;
}

/** At most eight retained images and two pending resolutions, including stale replies. */
export class ComicImageCache {
  readonly states = new Map<string, ComicImageState>();
  private wanted: string[] = [];
  private pending = 0;
  private closed = false;
  constructor(private readonly load: (id: string) => Promise<ComicImage>, private readonly changed: () => void) {}
  private pendingIds = new Set<string>();

  setWanted(ids: string[]): void {
    if (this.closed) return;
    this.wanted = [...new Set(ids)].slice(0, 8);
    for (const [id, state] of this.states) {
      if (!this.wanted.includes(id)) {
        state.image?.release();
        this.states.delete(id);
      }
    }
    this.pump();
    this.changed();
  }

  retry(id: string): void {
    const state = this.states.get(id);
    if (state?.status !== "error" || this.closed) return;
    state.image?.release();
    this.states.delete(id);
    this.pump();
    this.changed();
  }

  fail(id: string): void {
    const state = this.states.get(id);
    if (state?.status !== "ready" || this.closed) return;
    state.image?.release();
    this.states.set(id, { status: "error" });
    this.changed();
  }

  close(): void {
    this.closed = true;
    this.wanted = [];
    for (const state of this.states.values()) state.image?.release();
    this.states.clear();
  }

  private pump(): void {
    if (this.closed) return;
    for (const id of this.wanted) {
      if (this.pending >= 2) break;
      if (this.states.has(id) || this.pendingIds.has(id)) continue;
      const state: ComicImageState = { status: "loading" };
      this.states.set(id, state);
      this.pendingIds.add(id);
      this.pending++;
      void Promise.resolve().then(() => {
        if (this.closed || this.states.get(id) !== state) return null;
        return this.load(id);
      }).then((image) => {
        if (!image) return;
        if (this.closed || this.states.get(id) !== state) image.release();
        else { state.status = "ready"; state.image = image; }
      }, (error: unknown) => {
        if (!this.closed && this.states.get(id) === state) { state.status = "error"; state.error = error; }
      }).finally(() => {
        this.pending--;
        this.pendingIds.delete(id);
        if (!this.closed) { this.pump(); this.changed(); }
      });
    }
  }
}
