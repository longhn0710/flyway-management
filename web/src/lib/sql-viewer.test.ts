import { describe, expect, it, vi, afterEach } from "vitest";
import {
  highlightSQL,
  matchingFile,
  previewByteLimit,
  readSQL,
  type RunFile,
} from "./sql-viewer";
const file = (path: string): RunFile => ({
  artifact_id: "old-artifact",
  path,
  kind: "versioned",
  version: "1",
  sha256: "",
  size: 0,
});
afterEach(() => vi.unstubAllGlobals());
describe("historical SQL matching", () => {
  it("matches a native full path to the exact immutable relative path", () => {
    const files = [file("one/V1__same.sql"), file("two/V1__same.sql")];
    expect(
      matchingFile({ filepath: "/private/run/sql/two/V1__same.sql" }, files),
    ).toBe(files[1]);
    expect(matchingFile({ script: "V1__same.sql" }, files)).toBeNull();
    expect(
      matchingFile({ version: "1", description: "same" }, files),
    ).toBeNull();
    expect(matchingFile({ filepath: "/new/V9__other.sql" }, files)).toBeNull();
  });
});
describe("untrusted SQL highlighting", () => {
  it("retains text as tokens with multiline comments, strings, HTML and CRLF", () => {
    const text =
      "-- <script>alert(1)</script>\r\nCREATE TABLE t (x VARCHAR2(20));\r\n/* comment\nSELECT */ SELECT 'a''b';";
    const lines = highlightSQL(text);
    expect(lines.map((l) => l.map((t) => t.text).join("")).join("\n")).toBe(
      text.replaceAll("\r\n", "\n"),
    );
    expect(lines[0][0].kind).toBe("comment");
    expect(lines[1][0]).toEqual({ text: "CREATE", kind: "keyword" });
    expect(lines[3][0]).toEqual({ text: "SELECT */", kind: "comment" });
  });
  it("handles a long unterminated comment in one pass", () => {
    const text = "/*a".repeat(100_000);
    const lines = highlightSQL(text);
    expect(lines[0]).toEqual([{ text, kind: "comment" }]);
  });
});
describe("bounded verified SQL reads", () => {
  async function fixture(bytes: Uint8Array) {
    const sha256 = Array.from(
      new Uint8Array(await crypto.subtle.digest("SHA-256", new Uint8Array(bytes).buffer)),
      (b) => b.toString(16).padStart(2, "0"),
    ).join("");
    return { ...file("V1__bytes.sql"), size: bytes.length, sha256 };
  }
  it("preserves BOM and CRLF and rejects altered bytes", async () => {
    const text = "\ufeff-- exact bytes\r\nSELECT 1;\r\n";
    const bytes = new TextEncoder().encode(text),
      f = await fixture(bytes);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(bytes)),
    );
    expect(await readSQL("/run/files/content", f)).toBe(text);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(new TextEncoder().encode("SELECT 2;"))),
    );
    await expect(readSQL("/run/files/content", f)).rejects.toThrow(
      "integrity check failed",
    );
  });
  it("rejects invalid UTF-8, binary and oversized previews without loading them", async () => {
    for (const bytes of [new Uint8Array([0xff]), new Uint8Array([0])]) {
      const f = await fixture(bytes);
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => new Response(bytes)),
      );
      await expect(readSQL("/content", f)).rejects.toThrow(/UTF-8|Binary/);
    }
    const fetcher = vi.fn();
    vi.stubGlobal("fetch", fetcher);
    await expect(
      readSQL("/content", {
        ...file("V1__large.sql"),
        size: previewByteLimit + 1,
      }),
    ).rejects.toThrow("2 MiB");
    expect(fetcher).not.toHaveBeenCalled();
  });
  it("limits streamed output even without Content-Length", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(new Uint8Array(previewByteLimit + 1))),
    );
    await expect(readSQL("/content", file("V1__large.sql"))).rejects.toThrow(
      "2 MiB",
    );
  });
});
