import { reactive } from "vue";
import { Events } from "@wailsio/runtime";
import { DriveService } from "../bindings/github.com/tonyflux/pkp/internal/services";
import type { ConnectivityResult } from "../bindings/github.com/tonyflux/pkp/internal/services";

export const connectivity = reactive({ result: null as ConnectivityResult | null, testing: false, error: "", dismissedAt: "" });
let listening = false;
export async function watchConnectivity() {
  if (!listening) {
    listening = true;
    Events.On("network:connectivity", (event: { data: ConnectivityResult }) => { connectivity.result = event.data; });
  }
  const before = connectivity.result;
  try { const result = await DriveService.Connectivity(); if (connectivity.result === before) connectivity.result = result; }
  catch (error) { connectivity.error = error instanceof Error ? error.message : String(error); }
}
export async function testConnectivity() {
  if (connectivity.testing) return;
  connectivity.testing = true;
  connectivity.error = "";
  try { const result = await DriveService.TestConnectivity(); if (result.at) connectivity.result = result; }
  catch (error) { connectivity.error = error instanceof Error ? error.message : String(error); }
  finally { connectivity.testing = false; }
}
