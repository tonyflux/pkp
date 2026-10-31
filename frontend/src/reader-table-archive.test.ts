import { describe, expect, it, vi } from "vitest";
vi.mock("./consumption", () => ({ DocumentArchive: {} }));
import { inspectWorksheet } from "./reader-table-archive";
import { TableReadError } from "./reader-table";

function sheet(content: string): Document {
  return new DOMParser().parseFromString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">${content}</worksheet>`, "application/xml");
}

describe("workbook allocation preflight", () => {
  it("accepts bounded dense or dimensionless sheets", () => {
    expect(() => inspectWorksheet(sheet('<dimension ref="A1:C3"/><sheetData><row r="1"><c r="A1"><v>123</v></c><c r="C3"><v>9</v></c></row></sheetData>'))).not.toThrow();
    expect(() => inspectWorksheet(sheet('<sheetData><row r="2"><c r="B2"><v>5</v></c></row></sheetData>'))).not.toThrow();
  });
  it("refuses sparse coordinates and malicious dimension declarations before allocation", () => {
    expect(() => inspectWorksheet(sheet('<dimension ref="A1:XFD1048576"/>'))).toThrow(TableReadError);
    expect(() => inspectWorksheet(sheet('<sheetData><row r="100000"><c r="SR100000"/></row></sheetData>'))).toThrow(TableReadError);
    expect(() => inspectWorksheet(sheet('<sheetData><row r="999999999"/></sheetData>'))).toThrow(TableReadError);
  });
  it("refuses declared bounds that would silently discard actual cell data", () => {
    expect(() => inspectWorksheet(sheet('<dimension ref="A1:B2"/><sheetData><row r="3"><c r="C3"><v>hidden</v></c></row></sheetData>'))).toThrow(TableReadError);
  });
  it("refuses malformed cell references", () => {
    expect(() => inspectWorksheet(sheet('<sheetData><row r="1"><c r="not-a-cell"/></row></sheetData>'))).toThrow(TableReadError);
  });
});
