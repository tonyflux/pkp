// jsdom has no matchMedia; the store asks it for the colour scheme at import.
window.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} }) as unknown as MediaQueryList;
// Tests read the Simplified Chinese source text; jsdom's navigator says en-US.
localStorage.setItem("pkp.language", "zh-Hans");

export {};
