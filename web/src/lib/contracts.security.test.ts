import { afterEach, describe, expect, it, vi } from "vitest";
import {
  api,
  apiList,
  setCsrfToken,
  clearCsrfToken,
  setUnauthorizedHandler,
} from "./contracts";

afterEach(() => {
  vi.unstubAllGlobals();
  clearCsrfToken();
  setUnauthorizedHandler(undefined);
});
function response(data: unknown, status = 200) {
  return new Response(JSON.stringify({ data }), { status });
}
describe("authenticated API client", () => {
  it("sends the restored CSRF token only with mutations, and clears it on signout", async () => {
    const fetch = vi
      .fn()
      .mockImplementation(() => Promise.resolve(response(true)));
    vi.stubGlobal("fetch", fetch);
    setCsrfToken("test-csrf");
    await api("/database-targets");
    await api("/database-targets", { method: "POST", body: "{}" });
    clearCsrfToken();
    await api("/auth/login", { method: "POST", body: "{}" });
    const readHeaders = fetch.mock.calls[0][1].headers as Headers;
    const write = fetch.mock.calls[1][1] as RequestInit;
    expect(readHeaders.has("X-CSRF-Token")).toBe(false);
    expect((write.headers as Headers).get("X-CSRF-Token")).toBe("test-csrf");
    expect(write.credentials).toBe("same-origin");
    expect(
      (fetch.mock.calls[2][1].headers as Headers).has("X-CSRF-Token"),
    ).toBe(false);
  });
  it("expires identity for business 401 but preserves it for a denied action or failed login", async () => {
    const expire = vi.fn();
    setUnauthorizedHandler(expire);
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(new Response("{}", { status: 403 }))
      .mockResolvedValueOnce(new Response("{}", { status: 401 }))
      .mockResolvedValueOnce(new Response("{}", { status: 401 }));
    vi.stubGlobal("fetch", fetch);
    await expect(api("/access/users")).rejects.toMatchObject({ status: 403 });
    await expect(
      api("/auth/login", { method: "POST", body: "{}" }),
    ).rejects.toMatchObject({ status: 401 });
    expect(expire).not.toHaveBeenCalled();
    await expect(api("/database-targets")).rejects.toMatchObject({
      status: 401,
    });
    expect(expire).toHaveBeenCalledOnce();
  });
  it("loads later pages while preserving scope filters", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(response([{ id: "a" }, { id: "b" }]))
      .mockResolvedValueOnce(response([{ id: "c" }]));
    vi.stubGlobal("fetch", fetch);
    expect(await apiList("/execution-accounts?target_id=uat", 2)).toEqual([
      { id: "a" },
      { id: "b" },
      { id: "c" },
    ]);
    expect(fetch.mock.calls[1][0]).toBe(
      "/api/v1/execution-accounts?target_id=uat&limit=2&offset=2",
    );
  });
  it("rejects malformed list data and invalid page sizes", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(response({ id: "not-an-array" })),
    );
    await expect(apiList("/database-targets")).rejects.toThrow(
      "expected contract",
    );
    await expect(apiList("/database-targets", 0)).rejects.toThrow("page size");
  });
});
