// What the menu bar panel says, from the state
// Go works out for it (services.MenuBarState). Pure: the panel component
// only draws these. Its rules: one transfer is shown, with a real percentage
// and amount only while it moves; waiting and unknown progress are words,
// never 0%; nothing claims Completed or 0 Items when the cloud could not be read;
// counts may be added across accounts, bytes never are.
import type { MenuBarState } from "../bindings/github.com/tonyflux/pkp/internal/services";
import { onWindows } from "./dom";
import { bytes } from "./format";
import { N_, t } from "./i18n";
import { fromDownload, fromTask, fromUpload, stateLabel, type TransferItem } from "./transfers";

export type CardTone = "running" | "waiting" | "attention" | "idle" | "unknown" | "reading";

export interface PanelCard {
  tone: CardTone;
  title: string;
  detail: string;
  /** Percent while the transfer moves, null otherwise. */
  progress: number | null;
  /** "2.7 GB / 4 GB" while moving, the size while waiting, "" when unknown. */
  amount: string;
  /** What carries on regardless of the window, "" when nothing does. */
  note: string;
  /** The task the transfer list opens on (TransferItem.key / kind), "" for none. */
  key: string;
  source: "" | TransferItem["kind"];
  /** N transfers need attention under a card about something else, "" when nothing needs the user. */
  extra: string;
}

const KIND: Record<TransferItem["kind"], string> = { download: N_("下载到电脑"), upload: N_("本地上传"), cloud: N_("云添加") };

/** The panel's transfer item as the transfer list would draw it, null for none. */
export function panelItem(state: MenuBarState): TransferItem | null {
  const it = state.item;
  if (it.kind === "upload" && it.upload) return fromUpload(it.upload, it.blocked);
  if (it.kind === "download" && it.download) return fromDownload(it.download, it.blocked);
  if (it.kind === "cloud" && it.task) return fromTask(it.task, it.blocked);
  return null;
}

/**
 * panelCard is the card under Transfer Status. name gives an account's name by id
 * (the panel says whose every transfer is: tasks of several accounts run
 * at once).
 */
export function panelCard(state: MenuBarState, name: (account: string) => string): PanelCard {
  const item = panelItem(state);
  const card: PanelCard = { tone: "idle", title: "", detail: "", progress: null, amount: "", note: "", key: "", source: "", extra: "" };
  if (!item) {
    if (state.cloud === "unknown") {
      return { ...card, tone: "unknown", title: t("状态待同步"), detail: t("当前无法确认云端任务进度") };
    }
    if (state.cloud === "") {
      return { ...card, tone: "reading", title: t("正在同步任务状态"), detail: t("稍候即可看到云添加的进度") };
    }
    const detail = onWindows ? t("关闭窗口后仍可从系统托盘打开 PKP") : t("关闭窗口后仍可从菜单栏打开 PKP");
    return { ...card, tone: "idle", title: t("暂无进行中的传输"), detail };
  }
  card.key = item.key;
  card.source = item.kind;
  const owner = name(item.account);
  if (item.blocked || item.state === "failed" || item.state === "interrupted") {
    const why = item.blocked ? t("登录已失效，请在应用内恢复") : item.reason || stateLabel(item);
    return {
      ...card,
      tone: "attention",
      title: t("{n} 项传输需要处理", { n: state.attention || 1 }),
      detail: `${owner} · ${why}`,
    };
  }
  card.extra = state.attention > 0 ? t("另有 {n} 项传输需要处理", { n: state.attention }) : "";
  card.title = `${stateLabel(item)} · ${item.name}`;
  card.detail = `${owner} · ${t(KIND[item.kind])}`;
  if (item.progress !== null) {
    card.tone = "running";
    card.progress = item.progress;
    card.amount =
      item.kind === "cloud"
        ? item.totalBytes > 0
          ? t("共 {size}", { size: bytes(item.totalBytes, "", 1) })
          : ""
        : `${bytes(item.doneBytes, "0 B", 1)} / ${bytes(item.totalBytes, "0 B", 1)}`;
    card.note = item.kind === "cloud" ? t("云端执行，与窗口无关") : t("关闭窗口后仍在继续");
    return card;
  }
  // Waiting, parsing, paused: words, no percentage or speed.
  card.tone = "waiting";
  card.amount = item.totalBytes > 0 ? t("共 {size}", { size: bytes(item.totalBytes, "", 1) }) : "";
  return card;
}

/** The line beside Transfer Status: every account's counts, or why there is none. */
export function countLine(state: MenuBarState): string {
  const parts: string[] = [];
  if (state.running > 0) parts.push(t("{n} 项进行中", { n: state.running }));
  if (state.attention > 0) parts.push(t("{n} 项需处理", { n: state.attention }));
  if (!parts.length) {
    parts.push(state.cloud === "unknown" ? t("状态待同步") : state.cloud === "" ? t("正在同步") : t("暂无活动任务"));
  } else if (state.cloud === "unknown") {
    parts.push(t("云端待同步"));
  }
  return [t("全部账号"), ...parts].join(" · ");
}

export interface InboxCard {
  /** 「「name」saved to cloud drive」. */
  title: string;
  /** 「To Organize · Added from Cloud」, with the owner when several accounts are here. */
  detail: string;
  /** N more newly added items await organization, "" when the card is the only one. */
  extra: string;
  /** The arrival MenuBarService.Open("assistant", …) opens. */
  key: string;
}

/**
 * inboxCard is the Newly Added card under the transfers, null when nothing waits.
 * Arrivals only wait, so they never take the transfer card's place; an
 * arrival is named by its task.
 * `several` adds whose arrival it is when more than one account is here.
 */
export function inboxCard(state: MenuBarState, name: (account: string) => string, several = false): InboxCard | null {
  const a = state.inboxItem;
  if (!a || state.inbox <= 0) return null;
  return {
    title: t("「{name}」已存入云盘", { name: a.name || t("新任务") }),
    detail: [several ? name(a.account) : "", t("待整理"), t("来自云添加")].filter(Boolean).join(" · "),
    extra: state.inbox > 1 ? t("另有 {n} 项新入库待整理", { n: state.inbox - 1 }) : "",
    key: a.id,
  };
}

/** The panel's subtitle under PKP. */
export function windowLine(state: MenuBarState): string {
  return state.windowOpen ? t("窗口已打开 · 仍在运行") : t("窗口已关闭 · 仍在运行");
}
