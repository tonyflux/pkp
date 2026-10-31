export const TABLE_LIMITS = {
  textBytes: 16 * 1024 * 1024,
  workbookBytes: 20 * 1024 * 1024,
  expandedBytes: 32 * 1024 * 1024,
  memberBytes: 8 * 1024 * 1024,
  members: 512,
  sheets: 64,
  rows: 100_000,
  columns: 512,
  cells: 1_000_000,
  fieldCharacters: 262_144,
} as const;

export type TableCell = string | number | boolean | Date | null;
export type TableRows = TableCell[][];
export type TableEncoding = "utf-8" | "gb18030" | "big5" | "utf-16le" | "utf-16be";
export type TableFailure = "limit" | "csv" | "encoding" | "workbook";
export class TableReadError extends Error {
  constructor(readonly reason: TableFailure) { super(reason); }
}

/** RFC 4180 quoting also applies when a tab or semicolon is selected. */
export function parseDelimited(text: string, delimiter: string): string[][] {
  if (![",", "\t", ";", "|"].includes(delimiter)) throw new TableReadError("csv");
  if (text.charCodeAt(0) === 0xfeff) text = text.slice(1);
  if (!text.length) return [];
  const rows: string[][] = [];
  let row: string[] = [], field = "", quoted = false, closed = false, endedRow = false, columns = 0;
  function cell() {
    row.push(field);
    field = "";
    closed = false;
    if (row.length > TABLE_LIMITS.columns) throw new TableReadError("limit");
  }
  function finishRow() {
    cell();
    columns = Math.max(columns, row.length);
    rows.push(row);
    row = [];
    if (rows.length > TABLE_LIMITS.rows || rows.length * columns > TABLE_LIMITS.cells) throw new TableReadError("limit");
  }
  for (let i = 0; i < text.length; i++) {
    const ch = text[i]!;
    endedRow = false;
    if (quoted) {
      if (ch === '"') {
        if (text[i + 1] === '"') { field += '"'; i++; }
        else { quoted = false; closed = true; }
      } else field += ch;
    } else if (ch === delimiter) cell();
    else if (ch === "\r" || ch === "\n") {
      finishRow();
      if (ch === "\r" && text[i + 1] === "\n") i++;
      endedRow = true;
    } else if (closed) throw new TableReadError("csv");
    else if (ch === '"') {
      if (field.length) throw new TableReadError("csv");
      quoted = true;
    } else field += ch;
    if (field.length > TABLE_LIMITS.fieldCharacters) throw new TableReadError("limit");
  }
  if (quoted) throw new TableReadError("csv");
  if (!endedRow) finishRow();
  return rows;
}

export function decodeTable(bytes: Uint8Array, encoding: TableEncoding): string {
  try { return new TextDecoder(encoding, { fatal: true }).decode(bytes); }
  catch { throw new TableReadError("encoding"); }
}

export function validateTable(rows: TableRows): void {
  let columns = 0;
  if (rows.length > TABLE_LIMITS.rows) throw new TableReadError("limit");
  for (const row of rows) {
    columns = Math.max(columns, row.length);
    if (columns > TABLE_LIMITS.columns || columns * rows.length > TABLE_LIMITS.cells) throw new TableReadError("limit");
    for (const value of row) if (typeof value === "string" && value.length > TABLE_LIMITS.fieldCharacters) throw new TableReadError("limit");
  }
}

export function columnLabel(index: number): string {
  let label = "";
  for (let n = index + 1; n > 0; n = Math.floor((n - 1) / 26)) label = String.fromCharCode(65 + (n - 1) % 26) + label;
  return label;
}

export type TableWorkerRequest =
  | { kind: "text"; bytes: ArrayBuffer; delimiter: string; encoding: TableEncoding }
  | { kind: "workbook"; bytes: ArrayBuffer; sheet: number; names: boolean };
export type TableWorkerResponse = { rows: TableRows; sheets?: string[]; sheet?: number } | { error: TableFailure };
