export interface ShareLink { url: string; passCode: string }

const unsafeText = /[\s\u0000-\u001f\u007f-\u009f\u2028\u2029]/u;

/** Recognizes only PikPak's HTTPS share host, never an arbitrary fetch target. */
export function shareLink(input: string): ShareLink | null {
  const raw = input.trim();
  if (!raw || raw.length > 8192 || unsafeText.test(raw)) return null;
  try {
    if (/^pkp:/i.test(raw)) {
      const wrapper = new URL(raw);
      if (wrapper.protocol !== "pkp:" || wrapper.hostname !== "share" || wrapper.port || wrapper.username || wrapper.password || wrapper.pathname || raw.includes("#")) return null;
      let count = 0;
      wrapper.searchParams.forEach(() => count++);
      if (count !== 1) return null;
      return httpsShare(wrapper.searchParams.get("url") ?? "");
    }
    return httpsShare(raw);
  } catch { return null; }
}

function httpsShare(raw: string): ShareLink | null {
  // Validate the raw path before URL() can normalize dot segments or backslashes.
  const match = /^https:\/\/(?:www\.)?mypikpak\.com(\/[^?#]*)(?:\?([^#]*))?$/i.exec(raw);
  const path = match && /^\/s\/([A-Za-z0-9_-]{1,256})(?:\/[A-Za-z0-9_-]{1,256})*\/?$/.exec(match[1]);
  if (!match || !path || raw.length > 8192 || unsafeText.test(raw)) return null;
  const query = match[2] ?? "";
  if (query.includes(";")) return null;
  for (const field of query.split("&")) {
    const decoded = decodeURIComponent(field.replace(/\+/g, " "));
    if (/[\u0000-\u001f\u007f-\u009f\u2028\u2029]/u.test(decoded)) return null;
  }
  const params = new URLSearchParams(query);
  const passCodes = params.getAll("pass_code"), passwords = params.getAll("password");
  if (passCodes.length > 1 || passwords.length > 1 || (passCodes.length && passwords.length && passCodes[0] !== passwords[0])) return null;
  const passCode = passCodes[0] ?? passwords[0] ?? "";
  if (passCode.length > 128) return null;
  // Match Go's QueryEscape so duplicate native deliveries retain one identity.
  const encodedCode = encodeURIComponent(passCode).replace(/[!'()*]/g, c => `%${c.charCodeAt(0).toString(16).toUpperCase()}`).replace(/%20/g, "+");
  return { url: `https://mypikpak.com/s/${path[1]}${passCode ? `?pass_code=${encodedCode}` : ""}`, passCode };
}
