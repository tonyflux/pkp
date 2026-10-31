import { describe, expect, it } from "vitest";
import { columnLabel, decodeTable, parseDelimited, TABLE_LIMITS, TableReadError, validateTable } from "./reader-table";

describe("delimited table parsing", () => {
  it("preserves quoted delimiters, multiline fields and escaped quotes", () => {
    expect(parseDelimited('name,note\r\n"张三","one, two\r\nthree ""quoted"""\r\n', ",")).toEqual([
      ["name", "note"], ["张三", 'one, two\r\nthree "quoted"'],
    ]);
  });
  it("handles TSV quoting, ragged rows, blank fields and a final delimiter", () => {
    expect(parseDelimited('a\tb\n"tab\there"\t"line\nnext"\nlast\t', "\t")).toEqual([
      ["a", "b"], ["tab\there", "line\nnext"], ["last", ""],
    ]);
    expect(parseDelimited("a,b\r\nc\r\n\r\n", ",")).toEqual([["a", "b"], ["c"], [""]]);
  });
  it("does not invent a row for an empty file or final line ending", () => {
    expect(parseDelimited("", ",")).toEqual([]);
    expect(parseDelimited("\ufeffa,b\n", ",")).toEqual([["a", "b"]]);
    expect(parseDelimited('""', ",")).toEqual([[""]]);
    expect(parseDelimited("\n", ",")).toEqual([[""]]);
  });
  it.each(['"unterminated', 'un"quoted', '"closed"trailing'])('rejects malformed quoting: %s', text => {
    expect(() => parseDelimited(text, ",")).toThrow(TableReadError);
  });
  it("keeps formula-like text inert and does not coerce identifiers or dates", () => {
    expect(parseDelimited("=SUM(A1:A2),001,2026-10-22,true", ",")).toEqual([["=SUM(A1:A2)", "001", "2026-10-22", "true"]]);
  });
  it("refuses oversized rows, columns and fields rather than returning partial data", () => {
    expect(() => parseDelimited("a\n".repeat(TABLE_LIMITS.rows + 1), ",")).toThrow(TableReadError);
    expect(() => parseDelimited(",".repeat(TABLE_LIMITS.columns), ",")).toThrow(TableReadError);
    expect(() => parseDelimited("x".repeat(TABLE_LIMITS.fieldCharacters + 1), ",")).toThrow(TableReadError);
    const rows = Array.from({ length: 2000 }, () => Array(501).fill(null));
    expect(() => validateTable(rows)).toThrow(TableReadError);
  });
});

describe("table encoding and coordinates", () => {
  it("decodes Chinese GB18030 and Big5 explicitly and refuses malformed UTF-8", () => {
    expect(decodeTable(new Uint8Array([0xd6, 0xd0, 0xce, 0xc4]), "gb18030")).toBe("中文");
    expect(decodeTable(new Uint8Array([0xa4, 0xa4, 0xa4, 0xe5]), "big5")).toBe("中文");
    expect(() => decodeTable(new Uint8Array([0xff]), "utf-8")).toThrow(TableReadError);
  });
  it("retains typed workbook values and generates spreadsheet column addresses", () => {
    const date = new Date("2026-10-22T00:00:00Z");
    const rows = [[123, date, "001", true, null]];
    expect(() => validateTable(rows)).not.toThrow();
    expect(rows[0]?.[1]).toBe(date);
    expect([0, 25, 26, 51, 511].map(columnLabel)).toEqual(["A", "Z", "AA", "AZ", "SR"]);
  });
});
