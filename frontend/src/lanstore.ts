import { reactive } from "vue";
import * as LANService from "../bindings/github.com/tonyflux/pkp/internal/services/lanservice";
import { N_, t } from "./i18n";

export interface LANSpaceRequest { name: string; kind: string; accountId: string; rootId: string; localPath: string; allowPrepare: boolean }
export interface LANSpace extends LANSpaceRequest { id: string; enabled: boolean; available: boolean; error: string }
export interface LANTask { id: string; spaceId: string; fileId: string; name: string; kind: string; state: string; done: number; total: number; error: string; optimizeStartup?: boolean }
export interface LANStatus { running: boolean; address: string; interfaceId: string; hasPassword: boolean; spaces: LANSpace[]; tasks: LANTask[]; cacheBytes: number; originalDir: string; copyDir: string; storageBusy: boolean; error: string }
export interface LANInterface { id: string; name: string; address: string }
export const lan = reactive({ status: null as LANStatus | null, interfaces: [] as LANInterface[], loading: false, busy: false, error: "" });
export { LANService };
const errors: Record<string, string> = {
  source_unavailable: N_("lan|来源暂不可用"),
  forbidden: N_("lan|文件已不在授权范围内，请重新选择空间。"),
  not_ready: N_("lan|文件尚未完整准备到电脑。"),
  prepare_disabled: N_("lan|此空间未允许准备文件。"),
  invalid_request: N_("lan|请检查密码、网卡和共享空间设置。"),
  rate_limited: N_("lan|准备任务已达上限，请等待任务结束后重试。"),
  disk_full: N_("lan|电脑磁盘可用空间不足，请清理后重试。"),
  media_unavailable: N_("lan|媒体工具不可用，请检查应用安装是否完整。"),
  unsupported_media: N_("lan|此媒体暂不支持兼容处理，请下载原件。"),
  invalid_media: N_("lan|媒体损坏或已改变，请重新核对原件。"),
  held: N_("lan|等待电脑处理"),
  storage_unavailable: N_("lan|保存位置不可用，请检查磁盘和目录权限。"),
  storage_busy: N_("lan|请等待正在准备的任务结束或取消后，再更改保存位置。"),
  service_closed: N_("lan|服务已退出，请重新打开 PKP。"),
};
export function lanError(error: unknown): string {
  const text = error instanceof Error ? error.message : String(error);
  const code = text.split(":", 1)[0]?.trim() ?? "";
  return t(errors[code] ?? N_("lan|操作未完成，请检查网络、保存位置与访问权限后重试。"));
}
let refreshing: Promise<void> | null = null;
let refreshingTasks: Promise<void> | null = null;
let taskRevision = 0;
export function refreshLAN(): Promise<void> {
  if (refreshing) return refreshing;
  const revision = ++taskRevision;
  lan.loading = true;
  refreshing = (async () => {
    try {
      const status = await LANService.Status();
      const tasks = revision === taskRevision || !lan.status ? (status.tasks ?? []).map(item => ({ ...item, error: item.error ? lanError(item.error) : "" })) : lan.status.tasks;
      lan.status = {
        ...status,
        error: status.error ? lanError(status.error) : "",
        spaces: (status.spaces ?? []).map(item => ({ ...item, error: item.error ? lanError(item.error) : "" })),
        tasks,
      };
      lan.error = "";
    } catch (error) { lan.error = lanError(error); }
    finally { lan.loading = false; refreshing = null; }
  })();
  return refreshing;
}
export function refreshLANTasks(): Promise<void> {
  if (refreshingTasks) return refreshingTasks;
  if (lan.busy || !lan.status) return Promise.resolve();
  const revision = taskRevision;
  refreshingTasks = (async () => {
    try {
      const tasks = await LANService.Tasks();
      if (revision === taskRevision && !lan.busy && lan.status) {
        taskRevision++;
        lan.status.tasks = (tasks ?? []).map(item => ({ ...item, error: item.error ? lanError(item.error) : "" }));
      }
    } catch (error) { if (revision === taskRevision && !lan.busy) lan.error = lanError(error); }
    finally { refreshingTasks = null; }
  })();
  return refreshingTasks;
}
export async function loadLANInterfaces(): Promise<void> { lan.interfaces = (await LANService.Interfaces()) ?? []; }
export async function changeLAN(action: () => Promise<unknown>): Promise<boolean> {
  if (lan.busy) return false;
  lan.busy = true; lan.error = ""; taskRevision++;
  try { await refreshing; await action(); await refreshLAN(); return true; }
  catch (error) { lan.error = lanError(error); return false; }
  finally { lan.busy = false; }
}
const states: Record<string, string> = { queued: N_("lan|等待准备"), downloading: N_("lan|下载到电脑"), converting: N_("lan|正在转换"), held: N_("lan|等待电脑处理"), ready: N_("lan|已就绪"), failed: N_("lan|准备失败"), cancelled: N_("lan|已取消") };
export function lanTaskState(state: string): string { return t(states[state] ?? N_("lan|状态待确认")); }
