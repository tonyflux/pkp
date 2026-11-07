import { afterEach, describe, expect, it, vi } from "vitest";
import { mountSelect, selectHTML } from "../../internal/lanshare/web/select.js";

let menu: { destroy(): void } | undefined;
afterEach(() => { menu?.destroy(); menu = undefined; document.body.replaceChildren(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

function open() {
  document.body.innerHTML = selectHTML({ id: "sample", label: "Language", value: "en", options: [
    { value: "en", label: "English" }, { value: "disabled", label: "Disabled", disabled: true }, { value: "ja", label: "日本語" }, { value: "ko", label: "한국어" },
  ] });
  const onChange = vi.fn();
  menu = mountSelect(document.querySelector(".select-menu")!, { onChange });
  const face = document.querySelector<HTMLButtonElement>(".select-face")!;
  return { face, onChange };
}
function key(target: Element, value: string) { target.dispatchEvent(new KeyboardEvent("keydown", { key: value, bubbles: true, cancelable: true })); }

describe("Visitor glass selection", () => {
  it("supports keyboard movement, skips disabled options and commits once", () => {
    const { face, onChange } = open();
    face.focus(); key(face, "ArrowDown");
    expect(face.getAttribute("aria-expanded")).toBe("true");
    expect((document.activeElement as HTMLElement).dataset.selectOption).toBe("en");
    key(document.activeElement!, "ArrowDown");
    expect((document.activeElement as HTMLElement).dataset.selectOption).toBe("ja");
    key(document.activeElement!, "End");
    expect((document.activeElement as HTMLElement).dataset.selectOption).toBe("ko");
    key(document.activeElement!, "Home");
    key(document.activeElement!, "ArrowDown");
    key(document.activeElement!, "Enter");
    expect(onChange).toHaveBeenCalledExactlyOnceWith("ja");
    expect(face.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(face);
    expect(document.querySelector('[data-select-option="ja"]')?.getAttribute("aria-selected")).toBe("true");
  });

  it("dismisses on Escape, Tab and outside pointer without changing the choice", () => {
    const { face, onChange } = open();
    for (const value of ["Escape", "Tab"]) {
      face.click(); key(document.activeElement!, value);
      expect(face.getAttribute("aria-expanded")).toBe("false");
      expect(document.activeElement).toBe(face);
    }
    face.click(); document.body.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    expect(face.getAttribute("aria-expanded")).toBe("false");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("places a rotated player list in logical surface coordinates above its trigger", () => {
    const { face, onChange } = open();
    const root = face.closest(".select-menu")!;
    const page = document.createElement("div");
    page.className = "player-page is-rotated";
    const surface = document.createElement("div");
    surface.className = "player-surface";
    document.body.append(page); page.append(surface); surface.append(root);
    const rect = (left: number, top: number, width: number, height: number) => ({ left, top, width, height, right: left + width, bottom: top + height }) as DOMRect;
    vi.spyOn(surface, "getBoundingClientRect").mockReturnValue(rect(0, 0, 320, 640));
    // Clockwise rotation maps logical (580, 250, 44, 44) to this physical rect.
    vi.spyOn(face, "getBoundingClientRect").mockReturnValue(rect(26, 580, 44, 44));
    const list = root.querySelector<HTMLElement>(".select-list")!;
    const rows = root.querySelector<HTMLElement>(".select-rows")!;
    vi.spyOn(rows, "scrollHeight", "get").mockReturnValue(196);
    vi.spyOn(list, "offsetWidth", "get").mockReturnValue(160);
    vi.spyOn(window, "innerWidth", "get").mockReturnValue(320);
    vi.spyOn(window, "innerHeight", "get").mockReturnValue(640);
    face.click();
    const x = Number.parseFloat(list.style.left), y = Number.parseFloat(list.style.top);
    expect(list.dataset.placement).toBe("above");
    expect(x).toBe(640 - 8 - 160);
    expect(y + 198).toBe(250 - 6);
    expect(y).toBeGreaterThanOrEqual(8);
    expect(list.style.maxHeight).toBe(`${250 - 8 - 6}px`);
    expect(document.activeElement).toBe(root.querySelector('[data-select-option="en"]'));
    key(document.activeElement!, "ArrowDown"); key(document.activeElement!, "Enter");
    expect(onChange).toHaveBeenCalledExactlyOnceWith("ja");
    expect(document.activeElement).toBe(face);
  });

  it("clamps a rotated list to the visual viewport and restores physical placement on exit", () => {
    const { face } = open();
    const root = face.closest(".select-menu")!;
    const page = document.createElement("div"); page.className = "player-page is-rotated";
    const surface = document.createElement("div"); surface.className = "player-surface";
    document.body.append(page); page.append(surface); surface.append(root);
    const rect = (left: number, top: number, width: number, height: number) => ({ left, top, width, height, right: left + width, bottom: top + height }) as DOMRect;
    vi.spyOn(surface, "getBoundingClientRect").mockReturnValue(rect(0, 0, 320, 640));
    const faceBounds = vi.spyOn(face, "getBoundingClientRect").mockReturnValue(rect(26, 580, 44, 44));
    const list = root.querySelector<HTMLElement>(".select-list")!;
    vi.spyOn(root.querySelector<HTMLElement>(".select-rows")!, "scrollHeight", "get").mockReturnValue(396);
    vi.spyOn(list, "offsetWidth", "get").mockReturnValue(160);
    const viewport = new EventTarget();
    Object.assign(viewport, { offsetLeft: 10, offsetTop: 20, width: 300, height: 600 });
    vi.stubGlobal("visualViewport", viewport);
    face.click();
    expect(Number.parseFloat(list.style.left) + 160).toBe(620 - 8);
    expect(Number.parseFloat(list.style.top)).toBe(10 + 8);
    expect(Number.parseFloat(list.style.maxHeight) + Number.parseFloat(list.style.top)).toBe(250 - 6);
    page.classList.remove("is-rotated");
    faceBounds.mockReturnValue(rect(260, 500, 44, 44));
    window.dispatchEvent(new Event("resize"));
    expect(Number.parseFloat(list.style.left) + 160).toBe(310 - 8);
    expect(Number.parseFloat(list.style.top) + 398).toBe(500 - 6);
  });
});
