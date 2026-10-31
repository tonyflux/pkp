import { describe, expect, it, vi } from "vitest";
import { ComicImageCache, markdownImageName, naturalComicPages, type ComicImage } from "./reader-comic";

const settle = async () => { for (let index = 0; index < 60; index++) await Promise.resolve(); };
// TypeScript 4.9 does not declare this native runtime API yet.
const deferred = () => (Promise as PromiseConstructor & {
  withResolvers<T>(): { promise: Promise<T>; resolve: (value: T) => void; reject: (reason?: unknown) => void };
}).withResolvers<ComicImage>();

describe("comic page ordering", () => {
  it("sorts naturally without mutating input and breaks equal numeric names deterministically", () => {
    const pages = ["page10.png", "page2.png", "page1.png", "Page2.png"].map(name => ({ id: name, name }));
    expect(naturalComicPages(pages).map(page => page.name)).toEqual(["page1.png", "Page2.png", "page2.png", "page10.png"]);
    expect(pages[0].name).toBe("page10.png");
  });
});

describe("Markdown sibling images", () => {
  it("allows raster siblings and percent-encoded spaces", () => {
    expect(markdownImageName("封面%20一.png")).toBe("封面 一.png");
    expect(markdownImageName("page.JPEG")).toBe("page.JPEG");
    expect(markdownImageName("./cover.png")).toBe("cover.png");
  });
  it.each(["https://remote.test/a.png", "//remote.test/a.png", "data:image/png;base64,x", "blob:abc.png", "../cover.png", "./../cover.png", "folder/a.png", "folder\\a.png", "%2e%2e%2fcover.png", "a.png?token=x", "a.png#x", "a.svg", "a.png%00", "%", ""])('blocks unsafe reference %s', source => {
    expect(markdownImageName(source)).toBeNull();
  });
});

describe("bounded comic image lifecycle", () => {
  it("retains at most eight URLs and releases evicted and closed images", async () => {
    const releases: string[] = [];
    const cache = new ComicImageCache(async id => ({ url: id, release: () => { releases.push(id); } }), () => undefined);
    cache.setWanted(Array.from({ length: 30 }, (_, index) => String(index)));
    await settle();
    expect(cache.states.size).toBe(8);
    expect([...cache.states.values()].every(state => state.status === "ready")).toBe(true);
    cache.setWanted(["20"]);
    await settle();
    expect(releases).toHaveLength(8);
    expect(cache.states.get("20")?.image?.url).toBe("20");
    cache.close();
    expect(releases).toContain("20");
  });
  it("releases late replies and starts no queued request after close", async () => {
    const first = deferred(), second = deferred();
    const release = vi.fn();
    const load = vi.fn((id: string) => id === "a" ? first.promise : second.promise);
    const cache = new ComicImageCache(load, () => undefined);
    cache.setWanted(["a", "b", "c"]);
    await settle();
    expect(load).toHaveBeenCalledTimes(2);
    cache.close();
    first.resolve({ url: "first", release });
    second.resolve({ url: "second", release });
    await settle();
    expect(release).toHaveBeenCalledTimes(2);
    expect(load).toHaveBeenCalledTimes(2);
    expect(cache.states.size).toBe(0);
  });
  it("does not duplicate an in-flight archive image when scrolling away and back", async () => {
    const pending = deferred();
    const release = vi.fn();
    const load = vi.fn(() => pending.promise);
    const cache = new ComicImageCache(load, () => undefined);
    cache.setWanted(["a"]);
    await settle();
    cache.setWanted([]);
    cache.setWanted(["a"]);
    expect(load).toHaveBeenCalledTimes(1);
    pending.resolve({ url: "late", release });
    await settle();
    expect(release).toHaveBeenCalledTimes(1);
    expect(load).toHaveBeenCalledTimes(2);
    cache.close();
  });
  it("retries failed pages without disturbing ready neighbors", async () => {
    let attempts = 0;
    const cache = new ComicImageCache(async id => {
      if (id === "bad" && attempts++ === 0) throw new Error("failed");
      return { url: id, release: () => undefined };
    }, () => undefined);
    cache.setWanted(["bad", "good"]);
    await settle();
    expect(cache.states.get("bad")?.status).toBe("error");
    const neighbor = cache.states.get("good");
    cache.retry("bad");
    await settle();
    expect(cache.states.get("bad")?.status).toBe("ready");
    expect(cache.states.get("good")).toBe(neighbor);
    cache.close();
  });
});
