import { describe, expect, it } from "vitest";
import { playFinished, playHasProgress } from "./plays";

describe("playFinished", () => {
  it("matches plays.go: 98% or the last ten seconds, never an unknown runtime", () => {
    expect(playFinished(0, 0)).toBe(false);
    expect(playFinished(5000, 0)).toBe(false);
    expect(playFinished(970, 1000)).toBe(false);
    expect(playFinished(980, 1000)).toBe(true);
    expect(playFinished(90, 100)).toBe(true);
    expect(playFinished(89, 100)).toBe(false);
  });

  it("draws a bar only past the opening seconds", () => {
    expect(playHasProgress(4, 100)).toBe(false);
    expect(playHasProgress(5, 100)).toBe(true);
    expect(playHasProgress(30, 0)).toBe(false);
  });
});
