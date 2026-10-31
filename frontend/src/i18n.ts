// UI language: Simplified Chinese, Traditional Chinese, English, Japanese, Korean.
//
// Messages are keyed by their Simplified Chinese source text (gettext style): t(key)
// reads as the source and falls back to it, and src/locales/<area>.ts holds
// the four other languages per key. `{name}` takes a param; numbers are
// formatted for the locale. A key may carry a context prefix for two
// meanings of one word: a lowercase ASCII context followed by "|", dropped
// from the Simplified Chinese text.
//
// t() reads i18n.locale, so templates and computeds follow a switch. Text
// built once and kept (a toast already shown, an error from Go) stays in the
// language it was made in.
//
// Go owns the choice (settings.json `language`, DriveService.SetLanguage) so
// its notifications and errors match; every window follows `language:state`
// (store.ts watchLanguage). localStorage keeps the last one for first paint.
import { reactive } from "vue";
import catalog from "./locales";
import type { Entry, Params, Text } from "./locales/types";

export type { Params } from "./locales/types";

export type Locale = "zh-Hans" | "zh-Hant" | "en" | "ja" | "ko";
export type LanguagePref = "system" | Locale;

/** The languages, each named in itself. */
export const LOCALES: readonly { value: Locale; label: string }[] = [
  { value: "zh-Hans", label: "简体中文" },
  { value: "zh-Hant", label: "繁體中文" },
  { value: "en", label: "English" },
  { value: "ja", label: "日本語" },
  { value: "ko", label: "한국어" },
];

const FIELD: Record<Exclude<Locale, "zh-Hans">, keyof Entry> = { "zh-Hant": "tw", en: "en", ja: "ja", ko: "ko" };

/** The tag Intl and <html lang> get for each locale. */
const TAG: Record<Locale, string> = { "zh-Hans": "zh-CN", "zh-Hant": "zh-TW", en: "en-US", ja: "ja-JP", ko: "ko-KR" };

const PREF_KEY = "pkp.language";
const LOCALE_KEY = "pkp.locale";

export function isLocale(v: unknown): v is Locale {
  return LOCALES.some((l) => l.value === v);
}

/**
 * resolveLocale picks the first supported language of a preference list
 * (BCP 47 tags, most wanted first). Chinese is Traditional when the tag says
 * Hant or a Traditional region (TW, HK, MO), else Simplified. Nothing
 * supported reads as English.
 */
export function resolveLocale(tags: readonly string[]): Locale {
  for (const raw of tags) {
    const parts = raw.toLowerCase().replace(/_/g, "-").split("-");
    switch (parts[0]) {
      case "zh":
        if (parts.includes("hant") || (!parts.includes("hans") && parts.some((p) => p === "tw" || p === "hk" || p === "mo"))) return "zh-Hant";
        return "zh-Hans";
      case "en":
        return "en";
      case "ja":
        return "ja";
      case "ko":
        return "ko";
    }
  }
  return "en";
}

function initial(): { pref: LanguagePref; locale: Locale } {
  const saved = localStorage.getItem(PREF_KEY);
  const pref: LanguagePref = saved === "system" || isLocale(saved) ? saved : "system";
  if (pref !== "system") return { pref, locale: pref };
  const last = localStorage.getItem(LOCALE_KEY);
  return { pref, locale: isLocale(last) ? last : resolveLocale(navigator.languages ?? [navigator.language]) };
}

export const i18n = reactive(initial());
document.documentElement.lang = TAG[i18n.locale];

/** applyLanguage puts a choice in force (Go's answer or event) and keeps it for the next first paint. */
export function applyLanguage(pref: LanguagePref, locale: Locale) {
  i18n.pref = pref;
  i18n.locale = locale;
  localStorage.setItem(PREF_KEY, pref);
  localStorage.setItem(LOCALE_KEY, locale);
  document.documentElement.lang = TAG[locale];
}

/** intlTag is the locale's tag for Intl formatters. */
export function intlTag(): string {
  return TAG[i18n.locale];
}

/** num groups a figure the locale's way (12,345). */
export function num(n: number): string {
  return n.toLocaleString(TAG[i18n.locale]);
}

// A context prefix: lowercase ASCII then "|", not part of the Simplified Chinese text.
const CONTEXT = /^[a-z][a-z0-9-]*\|/;

const warned = new Set<string>();

/** t is `key` in the language in force, its `{name}`s filled from params. */
export function t(key: string, params?: Params): string {
  const locale = i18n.locale;
  let text: Text | undefined;
  if (locale !== "zh-Hans") {
    text = catalog[key]?.[FIELD[locale]];
    if (text === undefined && import.meta.env.DEV && !warned.has(key)) {
      warned.add(key);
      console.warn(`i18n: no ${locale} text for`, key);
    }
  }
  const values: Record<string, string> = {};
  if (params) for (const [k, v] of Object.entries(params)) values[k] = typeof v === "number" ? num(v) : v;
  if (typeof text === "function") return text(values);
  return (text ?? key.replace(CONTEXT, "")).replace(/\{(\w+)\}/g, (whole, name: string) => values[name] ?? whole);
}

/**
 * N_ marks a key that is translated later, where it is shown: a label in a
 * module-level table uses N_(key), and the template says t(label).
 */
export function N_<T extends string>(key: T): T {
  return key;
}
