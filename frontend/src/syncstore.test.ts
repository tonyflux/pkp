import { afterEach, describe, expect, it, vi } from "vitest";
import { reactive } from "vue";

const service = vi.hoisted(() => ({ SyncPreview: vi.fn(), SyncStart: vi.fn(), SyncCancelPreview: vi.fn() }));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => ({ DriveService: service }));
vi.mock("./store", () => ({
  syncDraft: reactive({ pending: false, busy: false }),
  relocate: reactive({ mode: "" }), closeRelocate: vi.fn(), openCloudFolder: vi.fn(), openSyncFolderPicker: vi.fn(), run: vi.fn(),
}));
import { closeSyncDraft, openSyncCreate, previewSyncDraft, startSyncDraft, sync, syncDraft } from "./syncstore";

afterEach(async () => { await closeSyncDraft(); vi.clearAllMocks(); });

describe("sync creation", () => {
  it("retains local choice, preview and explicit-start gates", async () => {
    openSyncCreate("source-account");
    await previewSyncDraft();
    await startSyncDraft();
    expect(service.SyncPreview).not.toHaveBeenCalled();
    expect(service.SyncStart).not.toHaveBeenCalled();
    syncDraft.config!.localPath = "/chosen-by-owner";
    syncDraft.config!.name = "Shared movies";
    syncDraft.config!.cloudPath = "全部文件 / Movies";
    service.SyncPreview.mockResolvedValue({ id: "preview", blocked: false, error: "", changes: [] });
    await previewSyncDraft();
    expect(syncDraft.view).toBe("preview");
    await startSyncDraft();
    expect(service.SyncStart).not.toHaveBeenCalled();
    service.SyncStart.mockResolvedValue({ preview: { id: "changed-preview", blocked: false, error: "", changes: [] } });
    syncDraft.acknowledged = true;
    await startSyncDraft();
    expect(syncDraft.preview?.id).toBe("changed-preview");
    expect(syncDraft.acknowledged).toBe(false);
    await startSyncDraft();
    expect(service.SyncStart).toHaveBeenCalledTimes(1);
  });
});
