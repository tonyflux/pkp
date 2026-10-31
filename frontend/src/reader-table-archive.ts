import { DocumentArchive } from "./consumption";
import { TABLE_LIMITS, TableReadError } from "./reader-table";
import { checkXmlComplexity } from "./reader-xml";

function coordinate(value: string): { row: number; column: number } {
  const match = /^\$?([A-Z]{1,3})\$?([1-9]\d*)$/.exec(value);
  if (!match) throw new TableReadError("workbook");
  let column = 0;
  for (const letter of match[1]!) column = column * 26 + letter.charCodeAt(0) - 64;
  const row = Number(match[2]);
  if (column > TABLE_LIMITS.columns || row > TABLE_LIMITS.rows || column * row > TABLE_LIMITS.cells) throw new TableReadError("limit");
  return { row, column };
}

/** Validate both declared bounds and actual coordinates before the library allocates its dense grid. */
export function inspectWorksheet(document: Document): void {
  let maxRow = 0, maxColumn = 0;
  const dimensions = document.getElementsByTagNameNS("*", "dimension");
  let declared: { row: number; column: number } | undefined;
  if (dimensions.length) {
    const range = dimensions[0]!.getAttribute("ref")?.split(":");
    if (!range?.length || range.length > 2) throw new TableReadError("workbook");
    coordinate(range[0]!);
    declared = coordinate(range.at(-1)!);
  }
  const cells = document.getElementsByTagNameNS("*", "c");
  if (cells.length > TABLE_LIMITS.cells) throw new TableReadError("limit");
  for (const cell of Array.from(cells)) {
    const point = coordinate(cell.getAttribute("r") ?? "");
    maxRow = Math.max(maxRow, point.row);
    maxColumn = Math.max(maxColumn, point.column);
    if (maxRow * maxColumn > TABLE_LIMITS.cells) throw new TableReadError("limit");
  }
  for (const row of Array.from(document.getElementsByTagNameNS("*", "row"))) {
    const index = Number(row.getAttribute("r"));
    if (index > TABLE_LIMITS.rows) throw new TableReadError("limit");
  }
  if (declared && (maxRow > declared.row || maxColumn > declared.column)) throw new TableReadError("workbook");
}

export async function inspectWorkbook(bytes: Uint8Array, signal: AbortSignal): Promise<void> {
  const url = URL.createObjectURL(new Blob([bytes], { type: "application/zip" }));
  let archive: DocumentArchive | undefined;
  try {
    archive = await DocumentArchive.open(url, signal);
    const names = archive.names();
    if (names.length > TABLE_LIMITS.members) throw new TableReadError("limit");
    if (!names.includes("xl/workbook.xml") || !names.includes("[Content_Types].xml")) throw new TableReadError("workbook");
    let expanded = 0, sheets = 0;
    for (const name of names) {
      if (signal.aborted) throw new DOMException("Aborted", "AbortError");
      // Read every member with a hard cap before the XLSX library unzips the complete container.
      const content = await archive.read(name, TABLE_LIMITS.memberBytes);
      expanded += content.byteLength;
      if (expanded > TABLE_LIMITS.expandedBytes) throw new TableReadError("limit");
      if (!/\.(?:xml|rels)$/i.test(name)) continue;
      const text = new TextDecoder("utf-8", { fatal: true }).decode(content);
      if (/<!DOCTYPE|<!ENTITY/i.test(text)) throw new TableReadError("workbook");
      checkXmlComplexity(text, 1000000);
      const document = new DOMParser().parseFromString(text, "application/xml");
      if (document.getElementsByTagName("parsererror").length) throw new TableReadError("workbook");
      if (document.documentElement.localName === "worksheet") {
        sheets++;
        if (sheets > TABLE_LIMITS.sheets) throw new TableReadError("limit");
        inspectWorksheet(document);
      }
      if (name === "xl/workbook.xml" && document.getElementsByTagNameNS("*", "sheet").length > TABLE_LIMITS.sheets) throw new TableReadError("limit");
    }
    if (!sheets) throw new TableReadError("workbook");
  } finally {
    await archive?.close();
    URL.revokeObjectURL(url);
  }
}
