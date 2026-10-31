import { describe, expect, it } from "vitest";
import { languageName, trackDetail, trackLabel } from "./tracks";
import type { Track } from "../bindings/github.com/tonyflux/pkp/internal/services";

const track = (p: Partial<Track>): Track =>
  ({ id: 3, type: "sub", title: "", lang: "", codec: "subrip", selected: false, default: false, channels: "", external: false, ...p }) as Track;

describe("track names", () => {
  it("names PikPak's extracted languages: codes, variants and plain titles", () => {
    expect(languageName("eng")).toBe("英语");
    expect(languageName("chiSimplified")).toBe("简体中文");
    expect(languageName("chiTraditional")).toBe("繁体中文");
    expect(languageName("porBrazilian")).toBe("巴西葡萄牙语");
    expect(languageName("korSDH")).toBe("韩语（SDH）");
    // A title the extraction kept as the language stays as it is.
    expect(languageName("简体中字")).toBe("简体中字");
    expect(languageName("und")).toBe("");
  });

  it("an extracted track says where it came from; the file's own does not", () => {
    const own = track({ lang: "chi", title: "简体中字" });
    const extracted = track({ lang: "chiSimplified", external: true });
    expect(trackLabel(extracted)).toBe("简体中文");
    expect(trackDetail(extracted)).toBe("SRT · PikPak 提取");
    expect(trackDetail(own)).toBe("中文 · SRT");
    expect(trackLabel(track({ lang: "", external: true }))).toBe("字幕 3");
  });
});
