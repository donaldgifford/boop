import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { afterAll, afterEach, beforeAll } from "bun:test";

// A browser DOM for one test file. Import it FIRST in the file: Base UI,
// cmdk and user-event check for a document when they load.
//
// The DOM is scoped to the file, not the process, because happy-dom
// replaces fetch, Request and Response and the BFF's tests need Bun's.
// withDOM() registers it for the file's tests and removes it afterwards.
function register(): void {
  if (!GlobalRegistrator.isRegistered) {
    GlobalRegistrator.register({ url: "http://localhost/" });
  }
  // happy-dom lays nothing out, and Base UI's popovers observe sizes.
  if (typeof globalThis.ResizeObserver === "undefined") {
    globalThis.ResizeObserver = class {
      observe(): void {}
      unobserve(): void {}
      disconnect(): void {}
    } as unknown as typeof ResizeObserver;
  }
}

register();

// withDOM wires the file's hooks. Pass testing-library's cleanup: Bun has
// no global afterEach for it to register itself, and a tree left mounted
// outlives its document. It is passed in rather than imported because
// testing-library binds to the window present when it loads.
export function withDOM(unmount?: () => void): void {
  beforeAll(register);
  afterEach(() => {
    unmount?.();
    document.body.replaceChildren();
  });
  afterAll(async () => {
    // React runs passive effects on a later macrotask and reads
    // window.event there; let it finish before the window goes.
    await new Promise((resolve) => setImmediate(resolve));
    await new Promise((resolve) => setTimeout(resolve, 0));
    if (GlobalRegistrator.isRegistered) {
      await GlobalRegistrator.unregister();
    }
  });
}
