import { describe, expect, it } from "vitest";
import { CUSTOM_PRESET_ID, presetOf } from "./aipresets";

describe("presetOf", () => {
  it("matches an OpenAI-compatible service by base URL", () => {
    expect(presetOf({ provider: "openai", baseUrl: "https://api.deepseek.com/v1" }).id).toBe("deepseek");
    expect(presetOf({ provider: "openai", baseUrl: " https://API.DeepSeek.com/v1/ " }).id).toBe("deepseek");
    expect(presetOf({ provider: "openai", baseUrl: "http://localhost:11434/v1" }).id).toBe("ollama");
    expect(presetOf({ provider: "openai", baseUrl: "https://api.openai.com/v1" }).id).toBe("openai");
  });

  it("matches Anthropic by provider whatever the stored base URL", () => {
    expect(presetOf({ provider: "anthropic", baseUrl: "https://api.openai.com/v1" }).id).toBe("anthropic");
  });

  it("falls back to Custom", () => {
    expect(presetOf({ provider: "openai", baseUrl: "http://localhost:1234/v1" }).id).toBe(CUSTOM_PRESET_ID);
    expect(presetOf({ provider: "openai", baseUrl: "" }).id).toBe(CUSTOM_PRESET_ID);
    expect(presetOf({ provider: "", baseUrl: "https://api.deepseek.com/v1" }).id).toBe(CUSTOM_PRESET_ID);
  });
});
