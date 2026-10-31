// Stand-in for @wailsio/runtime under vitest: the store imports it at module
// level, and the real one talks to the host window.
export const Events = { On: () => () => {}, Emit: () => {} };
export const Window = { Get: () => ({}) };
export const Clipboard = { SetText: async () => {} };
export const Browser = { OpenURL: async () => {} };
export const Call = { ByID: () => Promise.reject(new Error("no backend in tests")) };
