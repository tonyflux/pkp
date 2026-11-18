import { describe, expect, it, vi } from "vitest";
import { Browser } from "@wailsio/runtime";
import { openSignUp } from "./store";

describe("sign-up", () => {
  it("opens PikPak without developer attribution", () => {
    const open = vi.spyOn(Browser, "OpenURL");
    openSignUp();
    expect(open).toHaveBeenCalledOnce();
    const url = new URL(open.mock.calls[0][0]);
    expect(url.origin).toBe("https://mypikpak.com");
    expect(url.pathname).toBe("/drive/login");
    expect(url.search).toBe("");
    open.mockRestore();
  });
});
