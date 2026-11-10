import { afterEach, describe, expect, it, vi } from "vitest";
import { createApp, nextTick, reactive, type App } from "vue";
import { updates } from "./updatestore";
import UpdateSettings from "./components/UpdateSettings.vue";

vi.mock("./updatestore", () => {
  return {
    updates: reactive({ state: null, pending: false, error: "" }),
    watchUpdates: vi.fn(), checkUpdates: vi.fn(), downloadUpdate: vi.fn(), cancelUpdate: vi.fn(),
    installUpdate: vi.fn(), ignoreUpdate: vi.fn(), remindUpdateLater: vi.fn(), setAutomaticUpdates: vi.fn(),
  };
});

let app: App | undefined;
afterEach(() => { app?.unmount(); app = undefined; document.body.replaceChildren(); });

describe("release notes", () => {
  it("renders release structure without interpreting HTML or enabling unsafe links and images", async () => {
    updates.state = {
      current: "0.3.0", supported: true, automatic: true, phase: "available", error: "", lastChecked: "", received: 0, notice: false,
      info: {
        version: "0.4.0", publishedAt: "2026-11-08T16:43:40Z", size: 1024, minOS: "12.0", compatible: true,
        notes: '# PKP 0.4.0\n\n## Highlights\n\n- **Downloads** keep `file.zip` intact.\n- Read [the release](https://example.test/release).\n\n<script>globalThis.releaseExecuted = true</script>\n\n[unsafe](javascript:alert(1))\n\n![remote](https://example.test/tracker.png)',
      },
    };
    const host = document.createElement("div"); document.body.append(host);
    app = createApp(UpdateSettings); app.mount(host); await nextTick();
    const notes = host.querySelector(".notes")!;
    expect(notes.querySelector("h1")?.textContent).toBe("PKP 0.4.0");
    expect(notes.querySelector("h2")?.textContent).toBe("Highlights");
    expect(notes.querySelector("li strong")?.textContent).toBe("Downloads");
    expect(notes.querySelector("li code")?.textContent).toBe("file.zip");
    expect(notes.querySelectorAll("li")).toHaveLength(2);
    expect(notes.querySelector('a[href="https://example.test/release"]')?.textContent).toBe("the release");
    expect(notes.querySelector("script, img, a[href^='javascript:']")).toBeNull();
    expect(notes.textContent).toContain("<script>globalThis.releaseExecuted = true</script>");
    expect(notes.textContent).toContain("unsafe");
    expect(notes.textContent).toContain("remote");
  });
});
