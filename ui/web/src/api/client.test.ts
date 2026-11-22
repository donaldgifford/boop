import { describe, expect, test } from "bun:test";

import { ApiError } from "@donaldgifford/design-system/lib/errors";

import { signInPath, toApiError, unwrap } from "./client";
import { shouldRetry } from "./query";

const response = (status: number, headers: Record<string, string> = {}) => new Response(null, { status, headers });

describe("toApiError", () => {
  test("reads a boopd problem: status, code, message and request id", () => {
    const e = toApiError(
      { type: "workflow_missing", title: "No workflow", status: 409, detail: "repository 3 has no RepoWorkflow", request_id: "req-1" },
      response(409),
    );
    expect(e).toBeInstanceOf(ApiError);
    expect(e.status).toBe(409);
    expect(e.reason).toBe("workflow_missing");
    expect(e.message).toBe("repository 3 has no RepoWorkflow");
    expect(e.requestId).toBe("req-1");
  });

  test("falls back to the status for a body that is not a problem", () => {
    const e = toApiError({ error: "bad_gateway" }, response(502, { "x-request-id": "req-2" }));
    expect(e.status).toBe(502);
    expect(e.reason).toBeUndefined();
    expect(e.requestId).toBe("req-2");
  });
});

describe("unwrap", () => {
  test("returns the data of a success", async () => {
    expect(await unwrap(Promise.resolve({ data: { ok: true }, response: response(200) }))).toEqual({ ok: true });
  });

  test("a 401 signs in again and throws", async () => {
    let signedIn = 0;
    const call = Promise.resolve({ error: { error: "unauthenticated" }, response: response(401) });
    await expect(unwrap(call, () => signedIn++)).rejects.toBeInstanceOf(ApiError);
    expect(signedIn).toBe(1);
  });
});

test("signInPath returns to the page", () => {
  expect(signInPath({ pathname: "/pulls", search: "?state=open" })).toBe("/auth/login?return_to=%2Fpulls%3Fstate%3Dopen");
});

test("retries server errors and network failures, never a 4xx", () => {
  expect(shouldRetry(0, new ApiError(503, undefined, "x"))).toBe(true);
  expect(shouldRetry(0, new TypeError("fetch failed"))).toBe(true);
  expect(shouldRetry(0, new ApiError(404, undefined, "x"))).toBe(false);
  expect(shouldRetry(2, new ApiError(503, undefined, "x"))).toBe(false);
});
