import { describe, expect, test } from "bun:test";

import { count, daysSince, duration, rangeDays } from "./format";

describe("format", () => {
  test("daysSince floors whole days and never goes negative", () => {
    const now = Date.parse("2026-11-21T12:00:00Z");
    expect(daysSince("2026-11-11T13:00:00Z", now)).toBe(9);
    expect(daysSince("2026-11-22T00:00:00Z", now)).toBe(0);
    expect(daysSince(null, now)).toBeUndefined();
  });

  test.each([
    [undefined, "—"],
    [4_000, "4s"],
    [247_000, "4m 07s"],
    [3_720_000, "1h 02m"],
  ])("duration(%p) = %s", (ms, want) => {
    expect(duration(ms)).toBe(want);
  });

  test("rangeDays and count", () => {
    expect(rangeDays("7d")).toBe(7);
    expect(rangeDays("x")).toBe(30);
    expect(count(12345)).toBe("12,345");
    expect(count(null)).toBe("—");
  });
});
