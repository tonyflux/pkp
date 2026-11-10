import { describe, expect, it } from "vitest";
import { shareLink } from "./sharelink";

const url = "https://mypikpak.com/s/Share_123-abc";

describe("PikPak share intake URL boundary", () => {
  it("keeps the share identity and access code while removing website navigation and tracking", () => {
    expect(shareLink("HTTPS://WWW.MYPIKPAK.COM/s/Share_123-abc/Folder_1/?password=Ab12&utm_source=browser")).toEqual({ url: `${url}?pass_code=Ab12`, passCode: "Ab12" });
    expect(shareLink(`${url}?pass_code=Ab12&password=Ab12`)).toEqual({ url: `${url}?pass_code=Ab12`, passCode: "Ab12" });
    expect(shareLink(`${url}?password=%21%27%28%29%2A~`)).toEqual({ url: `${url}?pass_code=%21%27%28%29%2A~`, passCode: "!'()*~" });
  });

  it("unwraps only the explicit share deep link, preserving its embedded code", () => {
    expect(shareLink(`pkp://share?url=${encodeURIComponent(`${url}?pass_code=Ab12`)}`)).toEqual({ url: `${url}?pass_code=Ab12`, passCode: "Ab12" });
  });

  it.each([
    "https://mypikpak.com.evil.example/s/a", "https://evil.example/s/a",
    "https://user@mypikpak.com/s/a", "https://mypikpak.com:443/s/a", "http://mypikpak.com/s/a",
    "https://mypikpak.com/S/a", "https://mypikpak.com/s/", "https://mypikpak.com/s/a//b",
    "https://mypikpak.com/s/a/../b", "https://mypikpak.com/s/a/%2e%2e/b", "https://mypikpak.com/s/a%2fb",
    "https://mypikpak.com\\@evil.example/s/a", `${url}#`, `${url}#fragment`, `${url}?password=Ab12&pass_code=Cd34`,
    `${url}?pass_code=Ab12&pass_code=Ab12`, `${url}?x=%ZZ`, `${url}?x=%0a`, `${url}?x=a;b`,
    `${url}?pass_code=%E2%80%A8`, `${url}?password=%E2%80%A9`, `${url}?pass_code=%FF`,
    `pkp://share?url=${encodeURIComponent(url)}&file=/tmp/a.torrent`, "pkp://share?url=file%3A%2F%2F%2Ftmp%2Fa.torrent",
    `pkp://other?url=${encodeURIComponent(url)}`, `pkp://share?url=${encodeURIComponent(url)}#`,
    `https://mypikpak.com/s/${"a".repeat(257)}`, `${url}?pass_code=${"a".repeat(129)}`,
  ])("does not route unsafe or ambiguous input %s", (input) => {
    expect(shareLink(input)).toBeNull();
  });
});
