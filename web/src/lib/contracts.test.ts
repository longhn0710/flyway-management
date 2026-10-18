import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, apiV2, getHealth } from "./contracts";
afterEach(() => vi.unstubAllGlobals());
describe("Go health API client", () => {
  it("reads the live response envelope and forwards cancellation", async () => {
    const data = {
      status: "ok",
      database: "ok",
      schema_version: 1,
      installation_id: "test-installation",
      capabilities: {
        authentication: false,
        migration_execution: false,
        registry: false,
      },
    };
    const fetch = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify({ data })));
    vi.stubGlobal("fetch", fetch);
    const controller = new AbortController();
    expect(await getHealth(controller.signal)).toEqual(data);
    expect(fetch).toHaveBeenCalledWith("/api/v1/health", {
      signal: controller.signal,
      headers: new Headers({ Accept: "application/json" }),
      credentials: "same-origin",
    });
  });
  it("preserves typed server errors", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: {
              code: "DATABASE_UNAVAILABLE",
              message: "SQLite unavailable",
            },
          }),
          { status: 503 },
        ),
      ),
    );
    await expect(getHealth()).rejects.toMatchObject({
      status: 503,
      code: "DATABASE_UNAVAILABLE",
      message: "SQLite unavailable",
    });
  });
  it.each([
    [
      403,
      "PROJECT_ACTION_DENIED",
      "This identity cannot execute this deployment target.",
      "deployment_target_id",
    ],
    [
      409,
      "DEPLOYMENT_TARGET_STALE",
      "Deployment Target changed; assess again.",
      "revision",
    ],
  ])(
    "reads RFC 9457 v2 error fields (%s)",
    async (status, code, detail, field) => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(
            JSON.stringify({
              type: "about:blank",
              title: status === 403 ? "Forbidden" : "Conflict",
              status,
              detail,
              code,
              field,
            }),
            { status },
          ),
        ),
      );
      await expect(
        apiV2("/projects/project/deployment-targets/dt"),
      ).rejects.toMatchObject({ status, code, message: detail, field });
    },
  );
  it("handles a non-JSON gateway failure", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response("Bad gateway", { status: 502 })),
    );
    await expect(getHealth()).rejects.toBeInstanceOf(ApiError);
  });
  it("rejects missing installation metadata instead of reporting API ready", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          new Response(JSON.stringify({ data: { status: "ok" } })),
        ),
    );
    await expect(getHealth()).rejects.toThrow("expected contract");
  });
  it("does not replace network failures with fixture health", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new TypeError("Network failed")),
    );
    await expect(getHealth()).rejects.toThrow("Network failed");
  });
  it("handles a degraded health data envelope without assuming an error property", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            data: { status: "degraded", database: "unavailable" },
          }),
          { status: 503 },
        ),
      ),
    );
    await expect(getHealth()).rejects.toMatchObject({
      status: 503,
      code: "HTTP_ERROR",
    });
  });
  it("rejects a null successful response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("null")));
    await expect(getHealth()).rejects.toThrow("expected contract");
  });
});
