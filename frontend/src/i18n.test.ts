import { describe, expect, it } from "vitest";
import { check } from "../scripts/i18n-check.mjs";
import { applyLanguage, resolveLocale, t } from "./i18n";

describe("i18n", () => {
  it("has no untranslated text, missing or unused entries", async () => {
    const { problems } = (await check()) as { problems: string[] };
    expect(problems).toEqual([]);
  }, 60_000);

  it("resolves system language lists", () => {
    expect(resolveLocale(["zh-Hans-CN"])).toBe("zh-Hans");
    expect(resolveLocale(["zh-SG"])).toBe("zh-Hans");
    expect(resolveLocale(["zh-HK"])).toBe("zh-Hant");
    expect(resolveLocale(["zh-Hant-TW"])).toBe("zh-Hant");
    expect(resolveLocale(["zh-Hans-HK"])).toBe("zh-Hans");
    expect(resolveLocale(["fr-FR", "ko-KR"])).toBe("ko");
    expect(resolveLocale(["en-GB", "ja-JP"])).toBe("en");
    expect(resolveLocale(["fr-FR"])).toBe("en");
    expect(resolveLocale([])).toBe("en");
  });

  it("translates with params and falls back to the source", () => {
    try {
      applyLanguage("en", "en");
      expect(t("跟随这台电脑的语言设置，当前为{name}。", { name: "English" })).toBe("Follows this computer's language settings; currently English.");
      expect(t("没有这条文字 {n}", { n: 12345 })).toBe("没有这条文字 12,345");
      expect(t("ctx|没有这条文字")).toBe("没有这条文字");
    } finally {
      applyLanguage("zh-Hans", "zh-Hans");
    }
  });
});
