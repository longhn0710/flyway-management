import type { MigrationFile } from "./execution";
import type { NativeMigration } from "./flyway-info";

export interface RunFile extends MigrationFile {
  artifact_id: string;
}
export const previewByteLimit = 2 * 1024 * 1024;
export const linesPerPage = 200;
export function runFileHref(run: string, file: RunFile) {
  return `/runs/${encodeURIComponent(run)}?${new URLSearchParams({ tab: "sql", artifact_id: file.artifact_id, path: file.path })}`;
}
export function contentHref(run: string, file: RunFile) {
  return `/api/v1/migration-runs/${encodeURIComponent(run)}/files/content?${new URLSearchParams({ artifact_id: file.artifact_id, path: file.path })}`;
}
// Only native path/filename evidence can connect a row to source SQL. Do not
// guess from version, description or the current uploaded package.
export function matchingFile<T extends MigrationFile>(
  row: NativeMigration,
  files: T[],
): T | null {
  const nativePaths = [row.filepath, row.script].filter(
    (p): p is string => typeof p === "string" && !!p,
  );
  const matches = files.filter((f) =>
    nativePaths.some((p) => p === f.path || p.endsWith(`/${f.path}`)),
  );
  if (matches.length === 1) return matches[0];
  if (matches.length > 1) return null;
  const names = nativePaths.map((p) => p.split("/").at(-1));
  const byName = files.filter((f) => names.includes(f.path.split("/").at(-1)));
  return byName.length === 1 ? byName[0] : null;
}

export async function readSQL(
  url: string,
  file: MigrationFile,
  signal?: AbortSignal,
) {
  if (file.size > previewByteLimit)
    throw new Error(
      "This script exceeds the 2 MiB preview limit. Download the original file to read all bytes.",
    );
  const response = await fetch(url, {
    credentials: "same-origin",
    signal,
    cache: "no-store",
  });
  if (!response.ok)
    throw new Error(
      `SQL unavailable (${response.status}). Access may be denied or the historical artifact is missing or corrupt.`,
    );
  const declared = Number(response.headers.get("Content-Length"));
  if (declared > previewByteLimit) {
    await response.body?.cancel();
    throw new Error(
      "SQL exceeds the 2 MiB preview limit. Download the original file.",
    );
  }
  const reader = response.body?.getReader();
  if (!reader) throw new Error("SQL response body is unavailable.");
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > previewByteLimit) {
        await reader.cancel();
        throw new Error(
          "SQL exceeds the 2 MiB preview limit. Download the original file.",
        );
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  const hash = Array.from(
    new Uint8Array(await crypto.subtle.digest("SHA-256", bytes)),
    (b) => b.toString(16).padStart(2, "0"),
  ).join("");
  if (size !== file.size || hash !== file.sha256)
    throw new Error(
      "SQL integrity check failed. The returned bytes do not match the historical manifest.",
    );
  // Retain BOM and CRLF for Copy SQL. Only the visible line presentation strips CR.
  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(
      bytes,
    );
  } catch {
    throw new Error(
      "Preview requires UTF-8 text. Download the original file to preserve its encoding.",
    );
  }
  if (text.includes("\0"))
    throw new Error(
      "Binary SQL content cannot be previewed. Download the original file.",
    );
  return text;
}

export type SQLToken = { text: string; kind: string };
// Cosmetic lexical highlighting only; the SQL is always untrusted React text.
// This does not resolve, parse or execute Flyway migrations.
export function highlightSQL(text: string): SQLToken[][] {
  const pattern =
    /(--|\/\*|'|"|\b(?:SELECT|FROM|WHERE|CREATE|ALTER|DROP|TABLE|VIEW|INDEX|INSERT|INTO|VALUES|UPDATE|SET|DELETE|BEGIN|END|COMMIT|ROLLBACK|DECLARE|AS|IS|NULL|NOT|PRIMARY|KEY|FOREIGN|REFERENCES|CONSTRAINT|NUMBER|VARCHAR2?|INTEGER|DATE|TIMESTAMP|DEFAULT|IF|THEN|ELSE|EXECUTE|REPLACE|PROCEDURE|FUNCTION|RETURN|GRANT|ON|TO|AND|OR|JOIN|ORDER|BY|GROUP|HAVING|UNION|ALL|DISTINCT)\b|\b\d+(?:\.\d+)?\b)/gi;
  const lines: SQLToken[][] = [[]];
  const append = (value: string, kind: string) => {
    value.split("\n").forEach((part, index) => {
      if (index) lines.push([]);
      if (part) lines.at(-1)!.push({ text: part.replace(/\r$/, ""), kind });
    });
  };
  let offset = 0;
  let match: RegExpExecArray | null;
  while ((match = pattern.exec(text)) !== null) {
    append(text.slice(offset, match.index), "");
    const start = match.index;
    let end = pattern.lastIndex;
    let kind = /^\d/.test(match[0]) ? "number" : "keyword";
    if (match[0] === "--") {
      const newline = text.indexOf("\n", end);
      end = newline < 0 ? text.length : newline;
      kind = "comment";
    } else if (match[0] === "/*") {
      const close = text.indexOf("*/", end);
      end = close < 0 ? text.length : close + 2;
      kind = "comment";
    } else if (match[0] === "'" || match[0] === '"') {
      const quote = match[0];
      kind = "string";
      while (end < text.length) {
        if (text[end++] === quote) {
          if (text[end] === quote) end++;
          else break;
        }
      }
    }
    append(text.slice(start, end), kind);
    offset = end;
    pattern.lastIndex = end;
  }
  append(text.slice(offset), "");
  return lines;
}
