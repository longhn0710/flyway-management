import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import type {
  ColumnDef,
  PaginationState,
  Updater,
} from "@tanstack/react-table";
import { Play, Columns3, RotateCcw } from "lucide-react";
import { runs, targets } from "../lib/fixtures";
import type { MigrationRun } from "../lib/contracts";
import {
  PageHeading,
  Button,
  DataTable,
  Status,
  Badge,
  Notice,
  SearchBox,
  Modal,
  Check,
} from "../components/ui";
export default function History() {
  const [params, setParams] = useSearchParams(),
    [columnsOpen, setColumnsOpen] = useState(false),
    [showArtifact, setShowArtifact] = useState(true),
    [showAccount, setShowAccount] = useState(true);
  const filter = (key: string, value: string) =>
    setParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        if (value) next.set(key, value);
        else next.delete(key);
        next.delete("page");
        return next;
      },
      { replace: true },
    );
  const data = useMemo(
    () =>
      runs.filter((r) => {
        const q = (params.get("q") ?? "").toLowerCase();
        return (
          [r.id, r.actor, r.artifact, r.target, r.schema]
            .join(" ")
            .toLowerCase()
            .includes(q) &&
          (!params.get("target") || r.targetId === params.get("target")) &&
          (!params.get("schema") || r.schema === params.get("schema")) &&
          (!params.get("status") || r.status === params.get("status")) &&
          (!params.get("source") || r.source === params.get("source")) &&
          (!params.get("actor") || r.actor === params.get("actor")) &&
          (!params.get("from") ||
            r.startedAt.slice(0, 10) >= params.get("from")!) &&
          (!params.get("until") ||
            r.startedAt.slice(0, 10) <= params.get("until")!)
        );
      }),
    [params],
  );
  const pageSize = [4, 6, 10, 20].includes(Number(params.get("page_size")))
    ? Number(params.get("page_size"))
    : 6;
  const pagination = {
    pageIndex: Math.min(
      Math.max(0, Number(params.get("page")) || 0),
      Math.max(0, Math.ceil(data.length / pageSize) - 1),
    ),
    pageSize,
  };
  function changePage(updater: Updater<PaginationState>) {
    const value = typeof updater === "function" ? updater(pagination) : updater;
    setParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        next.set("page", String(value.pageIndex));
        next.set("page_size", String(value.pageSize));
        return next;
      },
      { replace: true },
    );
  }
  const columns = useMemo<ColumnDef<MigrationRun>[]>(
    () => [
      {
        accessorKey: "id",
        header: "Run ID & Nguồn",
        cell: ({ row }) => (
          <div className="inline nowrap">
            <Link className="mono strong" to={`/runs/${row.original.id}`}>
              {row.original.id}
            </Link>
            <Badge tone={row.original.source === "API" ? "accent" : "neutral"}>
              {row.original.source}
            </Badge>
          </div>
        ),
      },
      {
        accessorKey: "status",
        header: "Trạng thái migration",
        cell: ({ getValue }) => <Status value={getValue<string>()} />,
      },
      {
        accessorKey: "repair",
        header: "Kết quả repair",
        cell: ({ getValue }) =>
          ["SUCCEEDED", "UNSAFE"].includes(getValue<string>()) ? (
            <Status value={getValue<string>()} />
          ) : (
            <Badge>
              {getValue() === "NOT_REQUIRED" ? "Không yêu cầu" : "Chưa áp dụng"}
            </Badge>
          ),
      },
      {
        accessorKey: "target",
        header: "Database / Env",
        cell: ({ row }) => (
          <span className="mono small nowrap">
            {row.original.environment} · {row.original.target}
          </span>
        ),
      },
      {
        accessorKey: "schema",
        header: "Target Schema",
        cell: ({ getValue }) => <code>{getValue<string>()}</code>,
      },
      ...(showArtifact
        ? [
            {
              accessorKey: "artifact",
              header: "Artifact filename",
              cell: ({ getValue }: { getValue: () => unknown }) => (
                <span
                  className="truncate mono small"
                  title={String(getValue())}
                >
                  {String(getValue())}
                </span>
              ),
            } as ColumnDef<MigrationRun>,
          ]
        : []),
      {
        accessorKey: "actor",
        header: "Người yêu cầu & Loại",
        cell: ({ row }) => (
          <div>
            {row.original.actor}
            <small className="block muted">{row.original.actorType}</small>
          </div>
        ),
      },
      ...(showAccount
        ? [
            {
              accessorKey: "account",
              header: "Tài khoản thực thi",
              cell: ({ getValue }: { getValue: () => unknown }) => (
                <code>{String(getValue())}</code>
              ),
            } as ColumnDef<MigrationRun>,
          ]
        : []),
      {
        accessorKey: "startedAt",
        header: "Bắt đầu / Thời lượng",
        cell: ({ row }) => (
          <div className="mono small nowrap">
            {row.original.startedAt}
            <small className="block muted">{row.original.duration}</small>
          </div>
        ),
      },
    ],
    [showArtifact, showAccount],
  );
  return (
    <>
      <PageHeading
        title="Lịch sử chạy"
        description={`Nhật ký kiểm toán và trạng thái thực thi migration schema cơ sở dữ liệu · ${data.length} bản ghi minh họa`}
        breadcrumb="Vận hành Oracle"
        actions={
          <>
            <Button onClick={() => setColumnsOpen(true)}>
              <Columns3 size={15} />
              Tùy chọn cột
            </Button>
            <Link className="button primary" to="/runs/new">
              <Play size={15} />
              Chạy migration
            </Link>
          </>
        }
      />
      <div className="filter-bar">
        <SearchBox
          value={params.get("q") ?? ""}
          onChange={(q) => filter("q", q)}
          placeholder="Tìm Run ID, artifact, người…"
        />
        {[
          ["target", "Database", targets.map((t) => [t.id, t.name])],
          [
            "schema",
            "Target Schema",
            ["CORE_BANKING", "PAYMENT"].map((s) => [s, s]),
          ],
          [
            "status",
            "Trạng thái",
            [
              "RUNNING",
              "SUCCEEDED",
              "FAILED",
              "QUEUED",
              "BLOCKED",
              "NEEDS_REVIEW",
              "REPAIRING",
              "CANCELLED",
            ].map((s) => [s, s]),
          ],
          ["source", "Nguồn", ["WEB", "API"].map((s) => [s, s])],
          [
            "actor",
            "Người yêu cầu",
            Array.from(new Set(runs.map((r) => r.actor))).map((s) => [s, s]),
          ],
        ].map(([key, label, options]) => (
          <select
            key={String(key)}
            aria-label={String(label)}
            value={params.get(String(key)) ?? ""}
            onChange={(e) => filter(String(key), e.target.value)}
          >
            <option value="">{String(label)}: Tất cả</option>
            {(options as string[][]).map(([id, name]) => (
              <option key={id} value={id}>
                {name}
              </option>
            ))}
          </select>
        ))}
        <label className="inline small">
          Từ{" "}
          <input
            type="date"
            aria-label="Từ ngày"
            value={params.get("from") ?? ""}
            onChange={(e) => filter("from", e.target.value)}
          />
        </label>
        <label className="inline small">
          Đến{" "}
          <input
            type="date"
            aria-label="Đến ngày"
            value={params.get("until") ?? ""}
            onChange={(e) => filter("until", e.target.value)}
          />
        </label>
        <Button variant="ghost" onClick={() => setParams({})}>
          <RotateCcw size={14} />
          Đặt lại
        </Button>
      </div>
      <div className="panel">
        <DataTable
          columns={columns}
          data={data}
          pagination={pagination}
          onPaginationChange={changePage}
        />
        <div className="table-note">
          <Notice title="Ghi chú nghiệp vụ:">
            Repair thành công không đổi kết quả migration FAILED. Lịch sử repair
            không bao giờ rollback partial Oracle DDL.
          </Notice>
        </div>
      </div>
      <Modal
        open={columnsOpen}
        onOpenChange={setColumnsOpen}
        title="Tùy chọn cột"
        description="Chọn thông tin bổ sung trong bảng lịch sử."
      >
        <Check
          label="Artifact filename"
          checked={showArtifact}
          onChange={setShowArtifact}
        />
        <Check
          label="Tài khoản thực thi Oracle"
          checked={showAccount}
          onChange={setShowAccount}
        />
        <Button variant="primary" onClick={() => setColumnsOpen(false)}>
          Áp dụng
        </Button>
      </Modal>
    </>
  );
}
