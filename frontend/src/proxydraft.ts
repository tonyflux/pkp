// Network & Proxy as a draft: the state behind Settings · Network & Proxy
// and the login page's Network & Proxy sheet. Each caller edits its own draft;
// what is saved and in force is the store's `proxySettings`.
import { computed, reactive, ref, watch } from "vue";
import type { ProxyInput } from "../bindings/github.com/tonyflux/pkp/internal/services";
import { applyProxy, proxyDraftError, proxySettings } from "./store";
import { t, N_ } from "./i18n";

export const PROXY_MODES: { key: string; label: string; desc: string; icon: string }[] = [
  {
    key: "direct",
    get label() { return t("直连"); },
    get desc() { return t("不使用应用代理"); },
    icon: "globe",
  },
  {
    key: "system",
    get label() { return t("proxy|跟随系统"); },
    get desc() { return t("使用电脑的网络设置"); },
    icon: "shield",
  },
  {
    key: "custom",
    get label() { return t("自定义代理"); },
    get desc() { return t("只为 PKP 指定服务器"); },
    icon: "link",
  },
];

export const PROXY_SCHEMES: { value: string; label: string }[] = [
  { value: "socks5", label: "SOCKS5" },
  { value: "http", label: "HTTP" },
  { value: "https", label: "HTTPS" },
];

// Connection Overview in one headline and one line of what to do about it. Go splits
// the proxy handshake from the PKP service request, so an outage upstream
// is never reported as a wrong address.
const PROXY_TEST_TITLE: Record<string, string> = {
  ok: N_("测试通过"),
  proxy: N_("连不上代理服务器"),
  auth: N_("代理拒绝了账号密码"),
  service: N_("代理可用，PKP 服务不可访问"),
  config: N_("还不能测试"),
};

/*
 * useProxyDraft is one Network & Proxy form: picking another Connection Method or typing
 * in a field changes only what is Ready to Apply, and applyProxyDraft (Apply Settings)
 * is what makes it the route every request takes. The draft lives in the
 * calling component, so it goes when that component does and the saved
 * settings are read into the next one.
 */
export function useProxyDraft() {
  const px = computed(() => proxySettings.value);

  const proxy = reactive({ mode: "system", scheme: "socks5", host: "", port: "", auth: false, username: "", bypass: "" });
  // The password is bound, unlike the API keys: the field has a Show/Hide
  // button, which needs the value. It is cleared the moment Go has taken it
  // into the keychain, and nothing else reads it.
  const proxyPassword = ref("");
  const proxyPasswordShown = ref(false);
  const proxyAdvanced = ref(false);
  const proxyModeButtons = ref<HTMLButtonElement[]>([]);

  // The saved settings fill the draft; while the user has an unapplied draft
  // nothing changes under it, because this only runs when Go's answer differs.
  watch(
    () => {
      const s = px.value;
      return s ? `${s.mode}\n${s.scheme}\n${s.host}\n${s.port}\n${s.auth}\n${s.username}\n${s.bypass}` : "";
    },
    () => {
      const s = px.value;
      if (!s) return;
      proxy.mode = s.mode || "system";
      proxy.scheme = s.scheme || "socks5";
      proxy.host = s.host;
      proxy.port = s.port ? String(s.port) : "";
      proxy.auth = s.auth;
      proxy.username = s.username;
      proxy.bypass = s.bypass;
      proxyPassword.value = "";
      proxyPasswordShown.value = false;
    },
    { immediate: true },
  );

  const proxyPort = computed(() => Number(proxy.port.trim()));

  /*
   * What Test Connection and Apply Settings send. An empty password with a hint saved
   * means "keep the one in the keychain"; an empty one without a hint, or
   * Require Username and Password turned off, clears it.
   */
  const proxyInput = computed<ProxyInput>(() => ({
    mode: proxy.mode,
    scheme: proxy.scheme,
    host: proxy.host.trim(),
    port: proxyPort.value || 0,
    username: proxy.auth ? proxy.username.trim() : "",
    password: proxy.auth ? proxyPassword.value : "",
    keepPassword: proxy.auth && !proxyPassword.value && !!px.value?.passwordHint,
    bypass: proxy.bypass,
  }));

  // A card still being filled in is not a mistake to shout about: the error
  // shows once either field has something in it.
  const proxyFieldError = computed(() =>
    proxy.mode === "custom" && (proxy.host.trim() || proxy.port.trim()) ? proxyDraftError("custom", proxy.host, proxyPort.value) : "",
  );
  const proxyIncomplete = computed(() => !!proxyDraftError(proxy.mode, proxy.host, proxyPort.value));

  /** Unapplied Changes: the draft and what is in force differ. */
  const proxyDirty = computed(() => {
    const s = px.value;
    if (!s) return false;
    const i = proxyInput.value;
    if (i.mode !== s.mode) return true;
    // Addresses Not Using Proxy applies to Follow System too, so it is a change of its own.
    if (i.mode === "direct") return false;
    if (i.bypass.trim() !== s.bypass.trim()) return true;
    if (i.mode !== "custom") return false;
    return (
      i.scheme !== s.scheme ||
      i.host !== s.host ||
      i.port !== s.port ||
      proxy.auth !== s.auth ||
      i.username !== s.username ||
      !!proxyPassword.value
    );
  });

  const proxySchemeLabel = computed(() => PROXY_SCHEMES.find((s) => s.value === proxy.scheme)?.label ?? proxy.scheme.toUpperCase());
  const proxyModeLabel = computed(() => PROXY_MODES.find((m) => m.key === proxy.mode)?.label ?? proxy.mode);
  // The middle node of the route diagram, and the Ready to Apply line beneath it.
  const proxyRouteNode = computed(() => (proxy.mode === "custom" ? proxySchemeLabel.value : proxyModeLabel.value));
  const proxyPending = computed(() => {
    if (proxy.mode !== "custom") return proxyModeLabel.value;
    return `${proxySchemeLabel.value} · ${proxy.host.trim() || t("服务器地址")}:${proxy.port.trim() || t("端口")}`;
  });

  const proxyStatus = computed<{ title: string; tone: "" | "ok" | "bad"; text: string }>(() => {
    if (proxySettings.testing) return { title: t("正在测试"), tone: "", text: t("正在连接代理，并访问一次 PKP 服务。") };
    const test = proxySettings.test;
    if (test) {
      const key = PROXY_TEST_TITLE[test.kind];
      const title = key ? t(key) : test.ok ? t("测试通过") : t("连接失败");
      const detail = [test.proxyMessage, test.serviceMessage].filter(Boolean).join(t("；"));
      return { title, tone: test.ok ? "ok" : "bad", text: detail || title };
    }
    if (proxy.mode === "direct") return { title: t("尚未测试"), tone: "", text: t("直连不经过代理，测试只检查 PKP 服务是否可访问。") };
    return { title: t("尚未测试"), tone: "", text: proxyDirty.value ? t("配置已更改，请重新测试。") : t("先测试连接，再应用设置。") };
  });
  const proxyElapsed = computed(() => {
    const t = proxySettings.test;
    if (!t) return "";
    return [t.via, t.elapsedMs > 0 ? `${t.elapsedMs} ms` : ""].filter(Boolean).join(" · ");
  });

  // A tested draft that then changes was tested as something else.
  watch(proxyInput, () => {
    if (proxySettings.test) proxySettings.test = null;
  });

  function chooseProxyMode(mode: string) {
    proxy.mode = mode;
  }

  // Connection Method is a radiogroup: the arrow keys move the choice and the focus,
  // as they do in a native one.
  function onProxyModeKey(ev: KeyboardEvent, index: number) {
    const step = ev.key === "ArrowLeft" || ev.key === "ArrowUp" ? -1 : ev.key === "ArrowRight" || ev.key === "ArrowDown" ? 1 : 0;
    if (!step) return;
    ev.preventDefault();
    const next = (index + step + PROXY_MODES.length) % PROXY_MODES.length;
    proxy.mode = PROXY_MODES[next].key;
    proxyModeButtons.value[next]?.focus();
  }

  /** applyProxyDraft is Apply Settings; true once Go has put the draft in force. */
  async function applyProxyDraft(): Promise<boolean> {
    if (!(await applyProxy(proxyInput.value))) return false;
    // Go has it now; what was typed does not stay in the window.
    proxyPassword.value = "";
    proxyPasswordShown.value = false;
    return true;
  }

  return {
    px,
    proxy,
    proxyPassword,
    proxyPasswordShown,
    proxyAdvanced,
    proxyModeButtons,
    proxyInput,
    proxyFieldError,
    proxyIncomplete,
    proxyDirty,
    proxyModeLabel,
    proxyRouteNode,
    proxyPending,
    proxyStatus,
    proxyElapsed,
    chooseProxyMode,
    onProxyModeKey,
    applyProxyDraft,
  };
}
