import { describe, expect, it } from "vitest";
import { layoutRows, sliceRows } from "./windowing";

describe("sliceRows", () => {
  it("draws the rows reaching into the range and keeps the others as room, gaps included", () => {
    // tops 0, 18, 46, 84, 132; the list is 182 tall.
    const rows = layoutRows(Float64Array.from([10, 20, 30, 40, 50]), 8);
    expect(sliceRows(rows, 20, 90)).toEqual({ start: 1, end: 4, before: 18, after: 58 });
  });

  it("leaves out a row ending where the range starts and one starting where it ends", () => {
    const rows = layoutRows(Float64Array.from([10, 10, 10]), 0);
    expect(sliceRows(rows, 10, 20)).toEqual({ start: 1, end: 2, before: 10, after: 10 });
  });

  it("draws nothing for a range above or below the list, which keeps its height", () => {
    const rows = layoutRows(Float64Array.from([10, 10]), 4);
    expect(sliceRows(rows, -100, -10)).toEqual({ start: 0, end: 0, before: 24, after: 0 });
    expect(sliceRows(rows, 30, 90)).toEqual({ start: 2, end: 2, before: 24, after: 0 });
    expect(sliceRows(layoutRows(new Float64Array(0), 8), 0, 100)).toEqual({ start: 0, end: 0, before: 0, after: 0 });
  });

  it("keeps the list's height wherever the window is", () => {
    const heights = [65, 65, 103.390625, 74, 64, 65, 65];
    const gap = 8;
    const rows = layoutRows(Float64Array.from(heights), gap);
    for (let from = -60; from < rows.total + 60; from += 7) {
      const { start, end, before, after } = sliceRows(rows, from, from + 130);
      const drawn = heights.slice(start, end).reduce((sum, h) => sum + h, 0) + Math.max(0, end - start - 1) * gap;
      expect(before + drawn + after).toBeCloseTo(rows.total, 6);
    }
  });
});
