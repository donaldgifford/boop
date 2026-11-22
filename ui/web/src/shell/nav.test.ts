import { describe, expect, test } from "bun:test";

import { crumbsFor, labelFor, NAV, navGroups, sectionFor } from "./nav";

describe("nav", () => {
  test("groups keep FLEET, ACTIVITY, SYSTEM order and every entry", () => {
    const groups = navGroups(NAV);
    expect(groups.map(([g]) => g)).toEqual(["FLEET", "ACTIVITY", "SYSTEM"]);
    expect(groups.flatMap(([, items]) => items)).toHaveLength(NAV.length);
  });

  test("a detail page belongs to its section, never to the estate", () => {
    expect(sectionFor("/repositories/42")?.to).toBe("/repositories");
    expect(sectionFor("/packages/pkg%3Anpm%2Flodash")?.to).toBe("/packages");
    expect(sectionFor("/nope")).toBeUndefined();
    expect(labelFor("/runs")).toBe("Runs");
    expect(labelFor("/nope")).toBe("boop");
  });

  test("crumbs: the section, then the decoded detail", () => {
    expect(crumbsFor("/")).toEqual([{ label: "Estate" }]);
    expect(crumbsFor("/repositories/42")).toEqual([{ label: "Repositories", to: "/repositories" }, { label: "42" }]);
    expect(crumbsFor("/packages/pkg%3Anpm%2Flodash")).toEqual([{ label: "Packages", to: "/packages" }, { label: "pkg:npm/lodash" }]);
    expect(crumbsFor("/packages/%E0%A4%A")).toEqual([{ label: "Packages", to: "/packages" }, { label: "%E0%A4%A" }]);
  });
});
