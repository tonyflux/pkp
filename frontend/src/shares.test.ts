import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DriveStatus, ShareSummary } from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => {
  const state = { current: "a" };
  const calls: { name: string; args: unknown[] }[] = [];
  const answers = new Map<string, (...args: unknown[]) => unknown>();
  const service = () =>
    new Proxy(
      {},
      {
        get:
          (_target, name: string) =>
          (...args: unknown[]) => {
            calls.push({ name, args: JSON.parse(JSON.stringify(args)) });
            try {
              return Promise.resolve(answers.get(name)?.(...args) ?? null);
            } catch (err) {
              return Promise.reject(err);
            }
          },
      },
    );
  return { state, calls, answers, service };
});

vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => ({
  DriveService: backend.service(),
  PlayerService: backend.service(),
  AppearanceService: backend.service(),
}));

import { cancelShares, copyShareLink, copyShareLinks, drive, shares, switchAccount, ui } from "./store";

function statusOf(id: string): DriveStatus {
  return {
    accountId: id,
    configured: true,
    account: `${id}@example.com`,
    clientId: "",
    deviceId: id,
    userId: `user-${id}`,
    apiBaseUrl: "",
    tokenExpiry: "",
    configDir: "",
    needsPassword: false,
  };
}

function share(id: string, status = "active"): ShareSummary {
  return {
    id,
    title: `Title ${id}`,
    thumbnail: "",
    fileId: `file-${id}`,
    folder: false,
    fileSize: 1,
    fileNum: 1,
    status,
    statusText: "",
    views: 0,
    saves: 0,
    createdAt: "",
    expiresAt: "",
    daysLeft: -1,
    url: `https://example.com/s/${id}`,
    passCode: "",
  };
}

const cancellations = () => backend.calls.filter((call) => call.name === "CancelShares");
const writeText = vi.fn(async (_text: string) => {});

beforeEach(() => {
  backend.calls.length = 0;
  backend.answers.clear();
  backend.state.current = "a";
  backend.answers.set("Status", () => statusOf(backend.state.current));
  backend.answers.set("Accounts", () => []);
  backend.answers.set("SwitchAccount", (id) => {
    backend.state.current = String(id);
    return statusOf(backend.state.current);
  });
  backend.answers.set("List", () => ({ entries: [], total: 0, offset: 0, live: false, nextPageToken: "" }));
  drive.status = statusOf("a");
  shares.list = [];
  shares.selectedId = "";
  ui.message = "";
  writeText.mockClear();
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
});

describe("canceling shares", () => {
  it("removes only the requested rows and keeps an unrelated detail selected", async () => {
    const first = share("first");
    const second = share("second");
    const retained = share("retained");
    shares.list = [first, retained, second];
    shares.selectedId = retained.id;

    await cancelShares([first, second]);

    expect(cancellations()).toEqual([{ name: "CancelShares", args: [["first", "second"]] }]);
    expect(shares.list).toEqual([retained]);
    expect(shares.selectedId).toBe(retained.id);
  });

  it("clears the detail only when its share was canceled", async () => {
    const removed = share("removed");
    shares.list = [removed];
    shares.selectedId = removed.id;

    await cancelShares([removed]);

    expect(shares.list).toEqual([]);
    expect(shares.selectedId).toBe("");
  });

  it("retains rows and their selected detail when cancellation fails", async () => {
    const first = share("first");
    const second = share("second");
    shares.list = [first, second];
    shares.selectedId = first.id;
    backend.answers.set("CancelShares", () => {
      throw new Error("drive: POST /shares: 500 server_error: failed");
    });

    await cancelShares([first, second]);

    expect(shares.list).toEqual([first, second]);
    expect(shares.selectedId).toBe(first.id);
    expect(ui.messageTone).toBe("error");
  });

  it("excludes canceled or empty IDs and sends other unique IDs once", async () => {
    const active = share("active");
    const expired = share("expired", "expired");
    const canceled = share("canceled", "canceled");
    const blocked = share("blocked", "blocked");
    shares.list = [active, expired, canceled, blocked];
    shares.selectedId = canceled.id;

    await cancelShares([active, canceled, expired, active, share(""), blocked]);

    expect(cancellations()).toEqual([{ name: "CancelShares", args: [["active", "expired", "blocked"]] }]);
    expect(shares.list).toEqual([canceled]);
    expect(shares.selectedId).toBe(canceled.id);
  });

  it("does not call the backend for empty or already canceled selections", async () => {
    const canceled = share("canceled", "canceled");
    shares.list = [canceled];
    shares.selectedId = canceled.id;

    await cancelShares([]);
    await cancelShares([canceled]);

    expect(cancellations()).toEqual([]);
    expect(shares.list).toEqual([canceled]);
    expect(shares.selectedId).toBe(canceled.id);
  });

  it.each(["b", "a"])("ignores a late cancellation after switching to %s, including returning to the original account", async (destination) => {
    const previous = share("same-id");
    shares.list = [previous];
    shares.selectedId = previous.id;
    let complete!: () => void;
    backend.answers.set("CancelShares", () => new Promise<void>((resolve) => (complete = resolve)));
    const pending = cancelShares([previous]);

    await switchAccount("b");
    if (destination === "a") await switchAccount("a");
    const current = { ...share("same-id"), title: "Current account's share" };
    shares.list = [current];
    shares.selectedId = current.id;
    const messageSerial = ui.messageSerial;
    complete();
    await pending;

    expect(drive.status?.accountId).toBe(destination);
    expect(shares.list).toEqual([current]);
    expect(shares.selectedId).toBe(current.id);
    expect(ui.messageSerial).toBe(messageSerial);
  });
});

describe("copying share links", () => {
  it("keeps each title, URL and access code together with blank lines between shares", async () => {
    const first = { ...share("first"), title: "First collection", passCode: "2468" };
    const second = { ...share("second"), title: "Second collection" };
    const third = { ...share("third"), title: "Third collection", passCode: "abcd" };

    await copyShareLinks([first, second, third]);

    expect(writeText).toHaveBeenCalledExactlyOnceWith(
      "First collection\nhttps://example.com/s/first 提取码：2468\n\nSecond collection\nhttps://example.com/s/second\n\nThird collection\nhttps://example.com/s/third 提取码：abcd",
    );
    expect(ui.messageTone).toBe("success");
  });

  it("still copies fresh single links without a title or an inactive warning", async () => {
    await copyShareLink({ url: "https://example.com/s/new", passCode: "1357" });

    expect(writeText).toHaveBeenCalledExactlyOnceWith("https://example.com/s/new 提取码：1357");
    expect(ui.messageTone).toBe("success");
  });

  it("copies inactive links but warns instead of presenting them as accessible", async () => {
    const active = share("active");
    const expired = share("expired", "expired");
    const canceled = share("canceled", "canceled");

    await copyShareLinks([active, expired, canceled]);

    expect(writeText).toHaveBeenCalledTimes(1);
    expect(writeText.mock.calls[0][0]).toContain(expired.url);
    expect(writeText.mock.calls[0][0]).toContain(canceled.url);
    expect(ui.messageTone).toBe("info");
  });

  it("leaves the clipboard alone for an empty selection", async () => {
    await copyShareLinks([]);

    expect(writeText).not.toHaveBeenCalled();
  });
});
