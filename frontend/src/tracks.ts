// How the player's menus name audio and subtitle tracks (PlayerService
// Track). A track goes by its title (or the name an MP4 keeps for it), else
// its language in words, else its place: Audio Track 2. The menus add what tells two
// alike apart: the language beside a title, the format, the channels, and
// where the track came from.
import type { Track } from "../bindings/github.com/tonyflux/pkp/internal/services";
import { t, N_, intlTag } from "./i18n";

// Languages are named in the UI language; one DisplayNames per locale tag.
const displayNames = new Map<string, Intl.DisplayNames>();
function languages(): Intl.DisplayNames {
  const tag = intlTag();
  let d = displayNames.get(tag);
  if (!d) {
    d = new Intl.DisplayNames([tag], { type: "language", fallback: "none" });
    displayNames.set(tag, d);
  }
  return d;
}

// PikPak's extracted subtitles name their language as a code with a variant
// glued on ("chiSimplified", "porBrazilian", "korSDH"). A script or region
// variant becomes part of the tag Intl names; the others are said after it.
const SCRIPT: Record<string, string> = { Simplified: "Hans", Traditional: "Hant" };
const REGION: Record<string, string> = { Brazilian: "BR", LatinAmerican: "419", Canadian: "CA" };
const EUROPEAN: Record<string, string> = { por: "PT", spa: "ES", fre: "FR" };
const NOTE: Record<string, string> = { SDH: "SDH", Forced: N_("强制"), European: N_("欧洲") };

function named(tag: string): string | undefined {
  try {
    return languages().of(tag);
  } catch {
    return undefined; // not a language tag Intl can read
  }
}

/** languageName puts a track's language in words; a name that is not a code ("Simplified Chinese subtitles") is kept. */
export function languageName(code: string): string {
  if (!code || code === "und") return "";
  const variant = /^([a-z]{2,3})([A-Z][A-Za-z]+)$/.exec(code);
  if (variant) {
    const [, base, kind] = variant;
    const tag = SCRIPT[kind]
      ? `${base}-${SCRIPT[kind]}`
      : REGION[kind]
        ? `${base}-${REGION[kind]}`
        : kind === "European" && EUROPEAN[base]
          ? `${base}-${EUROPEAN[base]}`
          : "";
    if (tag) {
      const name = named(tag);
      if (name) return name;
    }
    const name = named(base);
    if (name) return t("{name}（{note}）", { name, note: NOTE[kind] ? t(NOTE[kind]) : kind });
  }
  return named(code) ?? code;
}

const CODECS: Record<string, string> = {
  aac: "AAC",
  ac3: "AC-3",
  eac3: "E-AC-3",
  dts: "DTS",
  truehd: "TrueHD",
  flac: "FLAC",
  opus: "Opus",
  mp3: "MP3",
  vorbis: "Vorbis",
  subrip: "SRT",
  ass: "ASS",
  webvtt: "WebVTT",
  mov_text: N_("文本"),
  hdmv_pgs_subtitle: "PGS",
  dvd_subtitle: "VobSub",
};
const CHANNELS: Record<string, string> = { mono: N_("单声道"), stereo: N_("立体声") };

export function trackLabel(track: Track): string {
  return (
    track.title ||
    languageName(track.lang) ||
    (track.type === "audio" ? t("音轨 {id}", { id: String(track.id) }) : t("字幕 {id}", { id: String(track.id) }))
  );
}

export function trackDetail(track: Track): string {
  const language = track.title ? languageName(track.lang) : "";
  const codecName = CODECS[track.codec];
  const codec = codecName ? t(codecName) : track.codec.startsWith("pcm") ? "PCM" : track.codec.toUpperCase();
  // "5.1(side)" is 5.1 to a listener.
  const channelName = CHANNELS[track.channels];
  const channels = channelName ? t(channelName) : track.channels.replace(/\(.*\)$/, "");
  return [
    language !== track.title ? language : "",
    [codec, channels].filter(Boolean).join(" "),
    // A transcode's subtitles are the original's, taken out by PikPak.
    track.external ? t("PikPak 提取") : "",
  ]
    .filter(Boolean)
    .join(" · ");
}
