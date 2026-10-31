import readXlsxFile, { readSheetNames } from "read-excel-file/web-worker";
import { decodeTable, parseDelimited, TABLE_LIMITS, TableReadError, validateTable, type TableRows, type TableWorkerRequest, type TableWorkerResponse } from "./reader-table";

self.onmessage = async (event: MessageEvent<TableWorkerRequest>) => {
  const request = event.data;
  try {
    let response: TableWorkerResponse;
    if (request.kind === "text") {
      response = { rows: parseDelimited(decodeTable(new Uint8Array(request.bytes), request.encoding), request.delimiter) };
    } else {
      const sheets = request.names ? await readSheetNames(request.bytes) : undefined;
      if (sheets && sheets.length > TABLE_LIMITS.sheets) throw new TableReadError("limit");
      const sheet = sheets ? Math.min(Math.max(1, request.sheet), sheets.length) : request.sheet;
      if (!sheet) throw new TableReadError("workbook");
      // The library only reads cached formula results; neither formulas nor VBA are evaluated.
      const rows = await readXlsxFile(request.bytes, { sheet, trim: false }) as unknown as TableRows;
      validateTable(rows);
      response = { rows, sheets, sheet };
    }
    self.postMessage(response);
  } catch (error) {
    const response: TableWorkerResponse = { error: error instanceof TableReadError ? error.reason : request.kind === "text" ? "csv" : "workbook" };
    self.postMessage(response);
  }
};
