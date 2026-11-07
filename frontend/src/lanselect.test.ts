import { afterEach, describe, expect, it, vi } from "vitest";
import { mountSelect, selectHTML } from "../../internal/lanshare/web/select.js";

let menu: { destroy(): void } | undefined;
afterEach(() => { menu?.destroy(); menu = undefined; document.body.replaceChildren(); vi.restoreAllMocks(); });

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
});
