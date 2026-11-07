import { beforeEach, describe, expect, it, vi } from "vitest";
const bridge = vi.hoisted(() => ({ Status: vi.fn(), Tasks: vi.fn(), Interfaces: vi.fn() }));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services/lanservice", () => bridge);
import { changeLAN, lan, refreshLAN, refreshLANTasks, type LANStatus, type LANTask } from "./lanstore";

const status = (): LANStatus => ({ running: false, address: "", interfaceId: "", hasPassword: false, spaces: [], tasks: [], cacheBytes: 0, originalDir: "/downloads/lan", copyDir: "/cache/lan", storageBusy: false, error: "" });
beforeEach(() => { vi.clearAllMocks(); lan.status = null; lan.busy = false; lan.error = ""; bridge.Status.mockResolvedValue(status()); });
describe("LAN desktop state", () => {
  it("serializes management actions and reports bridge failures", async () => {
    let reject!: (error: Error) => void;
    const action = vi.fn(() => new Promise<void>((_, fail) => { reject = fail; }));
    const pending = changeLAN(action);
    await Promise.resolve();
    const other = vi.fn();
    expect(await changeLAN(other)).toBe(false);
    expect(other).not.toHaveBeenCalled();
    reject(new Error("source_unavailable"));
    expect(await pending).toBe(false);
    expect(lan.busy).toBe(false);
  });
  it("waits out an old status request before applying a change", async () => {
    let resolve!: (value: LANStatus) => void;
    bridge.Status.mockImplementationOnce(() => new Promise<LANStatus>(done => { resolve = done; }));
    const old = refreshLAN();
    const action = vi.fn(async () => { bridge.Status.mockResolvedValue({ ...status(), running: true, address: "http://192.168.1.2:17891" }); });
    const changed = changeLAN(action);
    expect(action).not.toHaveBeenCalled();
    resolve(status());
    await old;
    expect(await changed).toBe(true);
    expect(lan.status?.running).toBe(true);
    expect(lan.status?.address).toBe("http://192.168.1.2:17891");
  });
  it("ignores an older task snapshot after a cleanup and reports the latest progress", async () => {
    const task = (done: number): LANTask => ({ id: "job", spaceId: "space", fileId: "file", name: "movie", kind: "original", state: "downloading", done, total: 100, error: "" });
    bridge.Status.mockResolvedValue({ ...status(), tasks: [task(10)] });
    await refreshLAN();
    let resolve!: (value: LANTask[]) => void;
    bridge.Tasks.mockImplementationOnce(() => new Promise<LANTask[]>(done => { resolve = done; }));
    const old = refreshLANTasks();
    await changeLAN(async () => { bridge.Status.mockResolvedValue(status()); });
    resolve([task(10)]); await old;
    expect(lan.status?.tasks).toEqual([]);
    bridge.Tasks.mockResolvedValue([task(67)]);
    await refreshLANTasks();
    expect(lan.status?.tasks[0]?.done).toBe(67);
  });
  it("keeps newer task progress when a slower full status returns", async () => {
    const task = (done: number): LANTask => ({ id: "job", spaceId: "space", fileId: "file", name: "movie", kind: "original", state: "downloading", done, total: 100, error: "" });
    bridge.Status.mockResolvedValue({ ...status(), tasks: [task(10)] }); await refreshLAN();
    let resolve!: (value: LANStatus) => void;
    bridge.Status.mockImplementationOnce(() => new Promise<LANStatus>(done => { resolve = done; }));
    const slow = refreshLAN();
    bridge.Tasks.mockResolvedValue([task(80)]); await refreshLANTasks();
    resolve({ ...status(), tasks: [task(20)] }); await slow;
    expect(lan.status?.tasks[0]?.done).toBe(80);
  });
});
