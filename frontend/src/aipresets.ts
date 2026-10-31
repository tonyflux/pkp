/*
 * Provider presets: choosing a service fills its base URL and a
 * recommended model and links to where its key is made, so Bring Your Own Key is one
 * paste instead of three fields to look up. The values are only what the
 * form starts from; Go validates and stores whatever is saved
 * (SetAIConnection), and the provider strings are internal/ai's
 * (ai.Anthropic "anthropic", ai.OpenAI "openai").
 */
import { t } from "./i18n";

export type AIProvider = "openai" | "anthropic";

export interface AIPreset {
  id: string;
  label: string;
  provider: AIProvider;
  /** "" for Custom: the user types it. Anthropic's is informational: Go always uses its own. */
  baseUrl: string;
  model: string;
  /** Offline suggestions; a successful node catalog takes precedence. */
  models: readonly string[];
  /** 「How to get a Key」; "" when there is nothing to link to. */
  keyUrl: string;
  /** A local service that needs no key (Go sends no Authorization header without one). */
  keyless?: boolean;
}

export const CUSTOM_PRESET_ID = "custom";

export const AI_PRESETS: readonly AIPreset[] = [
  {
    id: "deepseek",
    label: "DeepSeek",
    provider: "openai",
    baseUrl: "https://api.deepseek.com/v1",
    model: "deepseek-flash",
    models: ["deepseek-flash", "deepseek-v4-pro"],
    keyUrl: "https://platform.deepseek.com/api_keys",
  },
  {
    id: "qwen",
    get label() {
      return t("通义千问");
    },
    provider: "openai",
    baseUrl: "https://dashscope.aliyuncs.com/compatible-mode/v1",
    model: "qwen3.7-plus",
    models: ["qwen3.7-plus", "qwen3.8-max", "qwen3.8-flash"],
    keyUrl: "https://help.aliyun.com/zh/model-studio/get-api-key",
  },
  {
    id: "openai",
    label: "OpenAI",
    provider: "openai",
    baseUrl: "https://api.openai.com/v1",
    model: "gpt-5.5",
    models: ["gpt-5.5", "gpt-5.4", "gpt-5.4-mini"],
    keyUrl: "https://platform.openai.com/api-keys",
  },
  {
    id: "anthropic",
    label: "Anthropic",
    provider: "anthropic",
    baseUrl: "https://api.anthropic.com",
    model: "claude-sonnet-4-6",
    models: ["claude-sonnet-4-6", "claude-opus-4-7", "claude-haiku-4-5"],
    keyUrl: "https://console.anthropic.com/settings/keys",
  },
  {
    id: "openrouter",
    label: "OpenRouter",
    provider: "openai",
    baseUrl: "https://openrouter.ai/api/v1",
    model: "openai/gpt-5.5",
    models: ["openai/gpt-5.5", "anthropic/claude-sonnet-4.6", "openai/gpt-5.4-mini"],
    keyUrl: "https://openrouter.ai/keys",
  },
  {
    id: "ollama",
    get label() {
      return t("Ollama 本机");
    },
    provider: "openai",
    baseUrl: "http://localhost:11434/v1",
    model: "qwen3.5",
    models: ["qwen3.5", "gpt-oss:20b"],
    keyUrl: "",
    keyless: true,
  },
  {
    id: CUSTOM_PRESET_ID,
    get label() {
      return t("自定义 OpenAI 兼容");
    },
    provider: "openai",
    baseUrl: "",
    model: "",
    models: [],
    keyUrl: "",
  },
];

/** Base URLs compare the way Go stores them: trimmed, no trailing slash, host case-insensitive. */
function urlKey(raw: string): string {
  const s = raw.trim().replace(/\/+$/, "");
  try {
    const u = new URL(s);
    return `${u.protocol}//${u.host.toLowerCase()}${u.pathname.replace(/\/+$/, "")}`;
  } catch {
    return s.toLowerCase();
  }
}

/**
 * presetOf is the preset the saved connection was made from: Anthropic by
 * provider alone (Go ignores the base URL for it), an OpenAI-compatible
 * service by its base URL; anything else is Custom.
 */
export function presetOf(settings: { provider: string; baseUrl: string }): AIPreset {
  const custom = AI_PRESETS.find((p) => p.id === CUSTOM_PRESET_ID)!;
  if (settings.provider === "anthropic") return AI_PRESETS.find((p) => p.provider === "anthropic")!;
  if (settings.provider !== "openai") return custom;
  const key = urlKey(settings.baseUrl);
  return AI_PRESETS.find((p) => p.provider === "openai" && p.baseUrl && urlKey(p.baseUrl) === key) ?? custom;
}
