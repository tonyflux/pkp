// Catalog shapes (see i18n.ts). Area files import these as types only, so
// scripts/i18n-check.mjs can load them with Node's type stripping.

/**
 * Params fills `{name}` in a message. Numbers arrive formatted for the
 * locale (1,234); a function entry sees the same strings, so an English
 * singular is `p.n === "1"`.
 */
export type Params = Record<string, string | number>;
export type Text = string | ((p: Record<string, string>) => string);

/** One message in the four languages besides the Simplified Chinese source key. */
export interface Entry {
  /** Traditional Chinese (Taiwan usage). */
  tw: Text;
  en: Text;
  ja: Text;
  ko: Text;
}

export type Messages = Record<string, Entry>;
