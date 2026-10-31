import { reactive } from "vue";
import { Events } from "@wailsio/runtime";
import { UpdateService, type UpdateState } from "../bindings/github.com/tonyflux/pkp/internal/services";

export const updates = reactive({ state: null as UpdateState | null, pending: false, error: "" });
let listening = false;

async function action(run: () => Promise<unknown>) {
  if (updates.pending) return;
  updates.pending = true;
  updates.error = "";
  try { await run(); }
  catch (err) { updates.error = err instanceof Error ? err.message : String(err); }
  finally { updates.pending = false; }
}

export async function watchUpdates() {
  if (!listening) {
    listening = true;
    Events.On("update:state", (event: { data: UpdateState }) => { updates.state = event.data; });
  }
  await action(async () => { updates.state = await UpdateService.State(); });
}

export const checkUpdates = () => action(() => UpdateService.Check());
export const downloadUpdate = () => action(() => UpdateService.Download());
export const cancelUpdate = () => action(() => UpdateService.Cancel());
export const installUpdate = () => action(() => UpdateService.Install());
export const ignoreUpdate = () => action(() => UpdateService.Ignore());
export const remindUpdateLater = () => action(() => UpdateService.RemindLater());
export const setAutomaticUpdates = (on: boolean) => action(async () => { updates.state = await UpdateService.SetAutomatic(on); });
