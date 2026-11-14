import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { nextTick } from "vue";
import type * as Services from "../bindings/github.com/tonyflux/pkp/internal/services";
import type { Entry, FileDetail } from "../bindings/github.com/tonyflux/pkp/internal/services";
import { clearSelection, drive, pickAll, select, togglePick, ui } from "./store";

// FileDetail answers when the test says so, to stage a slow answer for one
// entry arriving after a fast one for the next. (vi.mock is hoisted above the
// store's import.)
const pending = new Map<string, (detail: FileDetail) => void>();
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", async (importOriginal) => {
  const real = await importOriginal<typeof Services>();
  return {
    ...real,
    DriveService: {
      ...real.DriveService,
      // The executor form: the project's TypeScript (4.9) has no typings
      // for Promise.withResolvers.
      FileDetail: (id: string) => new Promise<FileDetail>((resolve) => pending.set(id, resolve)),
    },
  };
});

const entry = (id: string, category = "video"): Entry =>
  ({ id, name: id, size: 1, modifiedTime: "", category, folder: false } as Entry);
const detail = (id: string): FileDetail => ({ ...entry(id), createdTime: "", width: 0, height: 0, hash: "", children: -1 });

// Lets the store's awaits on an answered FileDetail run to the end.
async function settle() {
  for (let i = 0; i < 5; i++) await Promise.resolve();
}

beforeEach(() => {
  drive.live = true;
  drive.entries = [entry("a"), entry("b"), entry("c", "document")];
  ui.category = "all";
  ui.query = "";
  clearSelection();
  pending.clear();
});

afterEach(() => clearSelection());

describe("the context column follows the selection", () => {
  it("shows Details for a new set, keeps a chosen Overview for the same set, and returns to Overview when cleared", () => {
    select(drive.entries[0]);
    expect(ui.panelTab).toBe("details");

    ui.panelTab = "overview";
    select(drive.entries[0]);
    expect(ui.panelTab).toBe("overview");

    togglePick(drive.entries[1]);
    expect(ui.panelTab).toBe("details");
    expect(drive.selectedId).toBe("");

    clearSelection();
    expect(ui.panelTab).toBe("overview");
    expect(drive.picked).toEqual([]);
  });

  it("never lets a slow answer for A replace B picked after it", async () => {
    select(drive.entries[0]);
    select(drive.entries[1]);
    pending.get("b")!(detail("b"));
    await settle();
    expect(drive.detail?.id).toBe("b");

    pending.get("a")!(detail("a"));
    await settle();
    expect(drive.detail?.id).toBe("b");
    expect(drive.selectedId).toBe("b");
    expect(drive.detailLoading).toBe(false);
  });

  it("drops picks a new filter hides and goes back to Overview", async () => {
    select(drive.entries[0]);
    togglePick(drive.entries[2]);
    expect(drive.picked).toEqual(["a", "c"]);

    ui.category = "document";
    await nextTick();
    expect(drive.picked).toEqual(["c"]);
    expect(ui.panelTab).toBe("overview");
  });

  it("selects only loaded matching rows and never silently expands to later results", async () => {
    drive.total = 500;
    ui.category = "video";
    pickAll();
    await nextTick();
    expect(drive.picked).toEqual(["a", "b"]);
    drive.entries = [...drive.entries, entry("later")];
    await nextTick();
    expect(drive.picked).toEqual(["a", "b"]);
  });
});
