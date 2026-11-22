import { GlobalRegistrator } from "@happy-dom/global-registrator";

// Loaded once per `bun test` process, before any test file (bunfig.toml).
//
// Base UI decides at module load whether a document exists: without one
// it swaps useLayoutEffect for a no-op and every Portal stays unmounted
// for the rest of the process. Which test file imports it first depends
// on the directory order, so the overlays (Base UI) and the command
// palette (cmdk) are loaded here, through the design system's own
// modules, under a document.
//
// The document is removed again before the first test file runs: the
// registrator also replaces fetch, Request and Response, and the BFF's
// tests need Bun's own. DOM tests register it again (web/testing/dom.ts).
GlobalRegistrator.register({ url: "http://localhost/" });
await import("@donaldgifford/design-system/components/dialog");
await import("@donaldgifford/design-system/components/command");
await GlobalRegistrator.unregister();
