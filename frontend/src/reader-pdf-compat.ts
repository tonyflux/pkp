// PDF.js legacy bundles provide the remaining platform polyfills in both realms.
// This primitive must exist before either PDF.js module evaluates on WKWebView.
const promises = Promise as PromiseConstructor & {
  withResolvers?: <T>() => { promise: Promise<T>; resolve: (value: T | PromiseLike<T>) => void; reject: (reason?: unknown) => void };
};
if (!promises.withResolvers) {
  Object.defineProperty(promises, "withResolvers", { configurable: true, writable: true, value: function withResolvers<T>() {
    let resolve!: (value: T | PromiseLike<T>) => void;
    let reject!: (reason?: unknown) => void;
    const promise = new Promise<T>((accept, fail) => { resolve = accept; reject = fail; });
    return { promise, resolve, reject };
  } });
}
export {};
