import { describe, expect, test } from "bun:test";

import { localizeTimes, when } from "./when";

describe("when", () => {
  test("UTC is labelled; local is the browser's zone, unlabelled", () => {
    expect(when("2026-11-24T10:41:02Z", "utc")).toBe("2026-11-24 10:41 UTC");
    expect(when("2026-11-24T10:41:02Z", "local")).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/);
  });

  test("a summary's UTC times follow the zone", () => {
    expect(localizeTimes("next run 2026-11-25T10:39:55Z", "utc")).toBe("next run 2026-11-25 10:39 UTC");
    expect(localizeTimes("next run 2026-11-25T10:39:55.123Z (webhook)", "utc")).toBe("next run 2026-11-25 10:39 UTC (webhook)");
    expect(localizeTimes("#35 synced: open", "utc")).toBe("#35 synced: open");
  });
});
