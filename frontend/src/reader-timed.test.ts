import { describe, expect, it } from "vitest";
import { cueTime, decodeTimed, formatCueTime, parseTimed } from "./reader-timed";

describe("timed text parsers", () => {
  it("reads SRT multiline cues, markup as text, and rejects invalid ranges", () => {
    const cues = parseTimed('1\r\n00:00:01,250 --> 00:00:03,500\r\n<b>你好</b> &amp; world\r\nsecond line\r\n\r\n\r\n2\r\n00:00:04,000 --> bad\r\ninvalid\r\n\r\n3\r\n00:00:05,000 --> 00:00:06,000\r\n<img src=x onerror=alert(1)>', "srt");
    expect(cues.map(cue => [cue.start, cue.end, cue.text])).toEqual([[1250, 3500, "你好 & world\nsecond line"], [5000, 6000, "<img src=x onerror=alert(1)>"]]);
  });
  it("skips WebVTT headers, notes, styles, and settings while retaining multiline text", () => {
    const cues = parseTimed('WEBVTT captions\nLanguage: zh\n\nNOTE hidden\n00:00.000 --> 00:01.000\nnot a cue\n\nSTYLE\n::cue { color:red }\n\nopening\n00:01.500 --> 00:03.000 line:20% align:start\n<v Speaker><c.yellow>Hello</c></v>\n<00:02.000>world', "vtt");
    expect(cues).toEqual([{ id: 0, start: 1500, end: 3000, text: "Hello\nworld" }]);
  });
  it("honors ASS field order, comma-containing text, tags, and drawing mode", () => {
    const source = '[Script Info]\nTitle: Not dialogue\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nComment: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,hidden\nDialogue: 0,0:00:01.25,0:00:04.50,Default,,0,0,0,,{\\i1}hello, world{\\i0}\\Nnext\\hline{\\p1}m 0 0 l 10 10{\\p0}!';
    expect(parseTimed(source, "ass")).toEqual([{ id: 0, start: 1250, end: 4500, text: "hello, world\nnext\u00a0line!" }]);
    expect(parseTimed('[Events]\nFormat: Start, Text, End\nDialogue: 0:00:01.00,a,b,c,0:00:02.00', "ssa")[0].text).toBe("a,b,c");
  });
  it("expands LRC timestamps, applies final global offset, and derives cue ends", () => {
    const cues = parseTimed('[ar:Artist]\n[00:01.25][00:03.500]same lyric\n[02:00:50]later\n[offset:-250]', "lrc");
    expect(cues.map(cue => [cue.start, cue.end, cue.text])).toEqual([[1000, 3250, "same lyric"], [3250, 120250, "same lyric"], [120250, null, "later"]]);
    expect(parseTimed('[100:00.00]long', "lrc")[0].start).toBe(6000000);
  });
  it("decodes BOM and selectable legacy encodings without changing source bytes", () => {
    expect(decodeTimed(new Uint8Array([0xff, 0xfe, 0x60, 0x4f, 0x7d, 0x59]), "auto")).toBe("你好");
    expect(decodeTimed(new Uint8Array([0xc4, 0xe3, 0xba, 0xc3]), "auto")).toBe("你好");
    expect(decodeTimed(new Uint8Array([0xa7, 0x41, 0xa6, 0x6e]), "big5")).toBe("你好");
    expect(cueTime("00:61:00.000")).toBeNull();
    expect(formatCueTime(3723123)).toBe("01:02:03.123");
  });
});
