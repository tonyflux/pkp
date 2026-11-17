// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApp, h, nextTick, reactive, type App } from "vue";
import CrashFeedbackSheet from "./components/CrashFeedbackSheet.vue";

const backend = vi.hoisted(() => ({ call: vi.fn() }));
vi.mock("@wailsio/runtime", () => ({ Call: { ByName: backend.call } }));

const service = "github.com/tonyflux/pkp/internal/services.CrashFeedbackService.";
const report = { id: "00000000-0000-4000-8000-000000000000", text: "original", path: "/local/log", truncated: false };
let app: App;
let host: HTMLElement;
let options: { active: boolean };
async function flush() { await Promise.resolve(); await nextTick(); await Promise.resolve(); await nextTick(); }
function button(selector: string) { return document.querySelector<HTMLButtonElement>(selector)!; }

beforeEach(async () => {
  backend.call.mockReset();
  backend.call.mockImplementation((name: string) => Promise.resolve(name.endsWith(".Pending") ? report : undefined));
  host = document.createElement("div");
  document.body.appendChild(host);
  options = reactive({ active: true });
  app = createApp({ render: () => h(CrashFeedbackSheet, { active: options.active }) });
  app.mount(host);
  await flush();
});
afterEach(() => { app.unmount(); host.remove(); });

describe("crash feedback consent", () => {
  it.each(["[data-autofocus]", ".ai-sheet-close"])("dismisses without sending via %s", async (selector) => {
    backend.call.mockImplementation((name: string) => Promise.resolve(name.endsWith(".Pending") ? null : undefined));
    button(selector).click();
    await flush();
    expect(backend.call).toHaveBeenCalledWith(service + "Dismiss", report.id);
    expect(backend.call.mock.calls.some(([name]) => name === service + "Send")).toBe(false);
  });

  it("sends exactly the edited text and retries after failure", async () => {
    const textarea = document.querySelector<HTMLTextAreaElement>("textarea")!;
    textarea.value = "edited 中文\nsecret removed";
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
    backend.call.mockRejectedValueOnce(new Error("offline"));
    button(".sheet-btn.primary").click();
    await flush();
    expect(document.querySelector("[role=alert]")).not.toBeNull();
    expect(textarea.value).toBe("edited 中文\nsecret removed");
    backend.call.mockImplementation((name: string) => Promise.resolve(name.endsWith(".Pending") ? null : undefined));
    button(".sheet-btn.primary").click();
    await flush();
    expect(backend.call.mock.calls.filter(([name]) => name === service + "Send")).toEqual([
      [service + "Send", report.id, "edited 中文\nsecret removed"],
      [service + "Send", report.id, "edited 中文\nsecret removed"],
    ]);
  });

  it("keeps the dialog open when dismissal fails", async () => {
    backend.call.mockRejectedValueOnce(new Error("disk"));
    button("[data-autofocus]").click();
    await flush();
    expect(document.querySelector("textarea")).not.toBeNull();
    expect(document.querySelector("[role=alert]")).not.toBeNull();
    expect(backend.call.mock.calls.some(([name]) => name === service + "Send")).toBe(false);
  });

  it("clears private edits when locked and reloads the local preview after unlock", async () => {
    const textarea = document.querySelector<HTMLTextAreaElement>("textarea")!;
    textarea.value = "redacted";
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
    options.active = false;
    await flush();
    expect(document.querySelector("textarea")).toBeNull();
    expect(backend.call.mock.calls.some(([name]) => name === service + "Send")).toBe(false);
    options.active = true;
    await flush();
    expect(document.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("original");
  });
  it("blocks oversized UTF-8 and JSON-escaped payloads without changing text", async () => {
    const textarea = document.querySelector<HTMLTextAreaElement>("textarea")!;
    for (const value of ["中".repeat(40961), '"'.repeat(70000)]) {
      textarea.value = value;
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      await nextTick();
      expect(button(".sheet-btn.primary").disabled).toBe(true);
      expect(textarea.value).toBe(value);
    }
    expect(backend.call.mock.calls.some(([name]) => name === service + "Send")).toBe(false);
  });
});
