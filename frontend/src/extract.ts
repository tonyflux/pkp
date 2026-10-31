// Online extraction: the words and choices of the extraction sheet that do
// not depend on the store. Every outcome the service can give has words of
// its own: a password, a timeout and a format it cannot open are never
// explained as one another.
import { bytes } from "./format";
import { t, N_ } from "./i18n";
import type { ArchiveItem, ArchivePick } from "../bindings/github.com/tonyflux/pkp/internal/services";

/** A reading that went nowhere: what the sheet says, and whether Retry can help. */
export interface Failure {
  title: string;
  detail: string;
  retry: boolean;
}

/** failureOf words an archive outcome other than ok / password / running; `text` is an error's own message. */
export function failureOf(status: string, text = ""): Failure {
  switch (status) {
    case "timeout":
      return { title: t("读取压缩包超时"), detail: t("云端一时没有读完文件列表，稍后再试通常就好。"), retry: true };
    case "unsupported":
      return { title: t("无法读取这个压缩包"), detail: t("格式不受支持，或者文件已经损坏。在线解压支持 ZIP、RAR、7Z、TAR 等常见格式。"), retry: false };
    case "tooMany":
      return { title: t("压缩包里的文件太多"), detail: t("超过在线解压的文件数上限，可以下载到电脑后再解压。"), retry: false };
    case "tooBig":
      return { title: t("压缩包太大"), detail: t("超过在线解压的大小上限，可以下载到电脑后再解压。"), retry: false };
    case "quota":
      return { title: t("当前账号不能在线解压"), detail: t("这个压缩包需要会员权益才能在线解压。"), retry: false };
    case "noSpace":
      return { title: t("云盘空间不足"), detail: t("解压出的文件放不下了。清理一些空间后再试。"), retry: false };
    case "unfinished":
      return { title: t("压缩包还没有完整保存到云盘"), detail: t("等上传或云添加完成后再来解压。"), retry: true };
    case "refused":
      return { title: t("云端拒绝了这个压缩包"), detail: text || t("它可能已被删除、正在审核或包含受限内容。"), retry: false };
    default:
      return { title: t("没有读到压缩包"), detail: text || t("请检查网络后重试。"), retry: true };
  }
}

/**
 * taskFailure words the reason a cloud extraction ended without finishing
 * (the task's error_detail); "" when the service gave none.
 */
export function taskFailure(code: string): string {
  if (!code) return t("云端没有说明原因，可以稍后再试。");
  if (code === "file_space_not_enough") return t("云盘空间不足，解压出的文件放不下。");
  if (code === "has_sensitive_words") return t("压缩包里有文件名包含受限内容，云端没有保存。");
  if (code === "second_upload_file_failed") return t("云端保存解压出的文件时失败，可以稍后再试。");
  if (/timeout/i.test(code)) return t("云端处理超时，可以稍后再试。");
  return t("云端没有完成解压（{code}）。", { code });
}

/** archiveType says what an archive is from its name: "ZIP archive". */
export function archiveType(name: string): string {
  const dot = name.lastIndexOf(".");
  const ext = dot > 0 ? name.slice(dot + 1).toUpperCase() : "";
  return ext ? t("{ext} 压缩文件", { ext }) : t("压缩文件");
}

const KIND: Record<string, string> = { video: N_("视频"), image: N_("图片"), audio: N_("音频"), document: N_("文档"), archive: N_("压缩包") };

/** itemMeta is a member's line under its name: "Folder", "Image · JPG", "File". */
export function itemMeta(item: ArchiveItem): string {
  if (item.folder) return t("文件夹");
  const dot = item.name.lastIndexOf(".");
  const ext = dot > 0 ? item.name.slice(dot + 1).toUpperCase() : "";
  const kind = KIND[item.category];
  return [kind ? t(kind) : t("文件"), ext].filter(Boolean).join(" · ");
}

/**
 * selectionSummary counts what is ticked. A size is given only when every
 * ticked item is a file: the service reports no size for folders, and a
 * total without them would be smaller than what gets extracted.
 */
export function selectionSummary(items: ArchiveItem[], picked: ReadonlySet<string>): string {
  const chosen = items.filter((i) => picked.has(i.path));
  if (!chosen.length) return t("未选择内容");
  const folders = chosen.filter((i) => i.folder).length;
  if (folders) return t("已选 {n} 项 · 含 {folders} 个文件夹", { n: chosen.length, folders });
  const size = chosen.reduce((sum, i) => sum + i.size, 0);
  return size ? t("已选 {n} 项 · {size}", { n: chosen.length, size: bytes(size) }) : t("已选 {n} 项", { n: chosen.length });
}

/**
 * picksOf is what goes to the service: nothing when every item is ticked —
 * the whole archive, not merely the members the listing showed — and
 * otherwise each ticked item.
 */
export function picksOf(items: ArchiveItem[], picked: ReadonlySet<string>): ArchivePick[] {
  const chosen = items.filter((i) => picked.has(i.path));
  if (chosen.length === items.length) return [];
  return chosen.map((i) => ({ index: i.index, folder: i.folder, path: i.path }));
}
