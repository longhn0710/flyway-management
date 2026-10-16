import * as Dialog from "@radix-ui/react-dialog";
import {
  CheckCircle2,
  Circle,
  AlertCircle,
  AlertTriangle,
  Info,
  X,
  Search,
  ArrowUpDown,
  ChevronLeft,
  ChevronRight,
} from "lucide-react";
import {
  useState,
  useRef,
  useId,
  Children,
  isValidElement,
  cloneElement,
  type ReactNode,
  type ButtonHTMLAttributes,
} from "react";
import {
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  getPaginationRowModel,
  useReactTable,
  type ColumnDef,
  type SortingState,
  type PaginationState,
  type OnChangeFn,
} from "@tanstack/react-table";
export function Button({
  variant = "secondary",
  className = "",
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "secondary" | "danger" | "ghost";
}) {
  return <button className={`button ${variant} ${className}`} {...props} />;
}
export function Badge({
  children,
  tone = "neutral",
}: {
  children: ReactNode;
  tone?: string;
}) {
  return <span className={`badge ${tone}`}>{children}</span>;
}
export function Status({ value }: { value: string }) {
  const tone = ["SUCCEEDED", "READY"].includes(value)
    ? "success"
    : value === "FAILED"
      ? "error"
      : ["RUNNING", "REPAIRING"].includes(value)
        ? "accent"
        : ["BLOCKED", "NEEDS_REVIEW", "UNSAFE"].includes(value)
          ? "warning"
          : "neutral";
  const Icon =
    tone === "success"
      ? CheckCircle2
      : tone === "error"
        ? AlertCircle
        : tone === "warning"
          ? AlertTriangle
          : Circle;
  return (
    <Badge tone={tone}>
      <Icon size={12} />
      {value}
    </Badge>
  );
}
export function Notice({
  children,
  tone = "info",
  title,
}: {
  children: ReactNode;
  tone?: "info" | "warning" | "error" | "success";
  title?: string;
}) {
  const Icon =
    tone === "warning"
      ? AlertTriangle
      : tone === "error"
        ? AlertCircle
        : tone === "success"
          ? CheckCircle2
          : Info;
  return (
    <div className={`notice ${tone}`}>
      <Icon size={17} />
      <div>
        {title && <strong>{title} </strong>}
        {children}
      </div>
    </div>
  );
}
export function Panel({
  title,
  aside,
  children,
  className = "",
}: {
  title?: ReactNode;
  aside?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={`panel ${className}`}>
      {title && (
        <div className="panel-title">
          <h2>{title}</h2>
          {aside}
        </div>
      )}
      <div className="panel-content">{children}</div>
    </section>
  );
}
export function PageHeading({
  title,
  description,
  actions,
  breadcrumb,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
  breadcrumb?: ReactNode;
}) {
  return (
    <>
      <div className="breadcrumb">
        Flyway Manager <span>/</span>
        {breadcrumb ?? title}
      </div>
      <div className="page-heading">
        <div>
          <h1>{title}</h1>
          {description && <p>{description}</p>}
        </div>
        {actions && <div className="actions">{actions}</div>}
      </div>
    </>
  );
}
export function Field({
  label,
  children,
  hint,
  required,
}: {
  label: string;
  children: ReactNode;
  hint?: ReactNode;
  required?: boolean;
}) {
  const labelId = useId();
  const hintId = `${labelId}-hint`;
  function labelControls(nodes: ReactNode): ReactNode {
    return Children.map(nodes, (node) => {
      if (
        !isValidElement<{ children?: ReactNode; "aria-describedby"?: string }>(
          node,
        )
      )
        return node;
      if (
        typeof node.type === "string" &&
        ["input", "select", "textarea"].includes(node.type)
      ) {
        return cloneElement(node, {
          "aria-labelledby": labelId,
          "aria-describedby": hint ? hintId : undefined,
        } as Record<string, string | undefined>);
      }
      return typeof node.type === "string" && node.props.children
        ? cloneElement(node, {}, labelControls(node.props.children))
        : node;
    });
  }
  return (
    <label className="field">
      <span id={labelId}>
        {label}
        {required && (
          <b className="required" aria-hidden="true">
            {" "}
            *
          </b>
        )}
      </span>
      {labelControls(children)}
      {hint && <small id={hintId}>{hint}</small>}
    </label>
  );
}
export function SearchBox({
  value,
  onChange,
  placeholder = "Search…",
  label = "Search",
}: {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  label?: string;
}) {
  return (
    <div className="search">
      <Search size={16} />
      <input
        aria-label={label}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
      />
    </div>
  );
}
export function Modal({
  open,
  onOpenChange,
  title,
  description,
  children,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  children: ReactNode;
}) {
  const returnFocus = useRef<HTMLElement | null>(null);
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content
          className="dialog-content"
          onOpenAutoFocus={() => {
            returnFocus.current =
              document.activeElement instanceof HTMLElement
                ? document.activeElement
                : null;
          }}
          onCloseAutoFocus={(event) => {
            if (returnFocus.current?.isConnected) {
              event.preventDefault();
              returnFocus.current.focus();
            }
          }}
        >
          <Dialog.Title>{title}</Dialog.Title>
          <Dialog.Description>{description}</Dialog.Description>
          <Dialog.Close asChild>
            <Button
              variant="ghost"
              className="dialog-close"
              aria-label="Close dialog"
            >
              <X size={18} />
            </Button>
          </Dialog.Close>
          {children}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
export function KeyValues({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className="key-values">
      {rows.map(([key, value]) => (
        <div key={key}>
          <dt>{key}</dt>
          <dd>{value}</dd>
        </div>
      ))}
    </dl>
  );
}
export function Check({
  label,
  checked,
  onChange,
  disabled,
}: {
  label: ReactNode;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <label className="check">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        disabled={disabled}
      />
      <span>{label}</span>
    </label>
  );
}
export function DataTable<T>({
  data,
  columns,
  pageSize = 6,
  initialSort = [],
  empty = "No matching results. Change or reset the filters.",
  getRowClass,
  pagination: controlledPagination,
  onPaginationChange,
}: {
  data: T[];
  columns: ColumnDef<T>[];
  pageSize?: number;
  initialSort?: SortingState;
  empty?: string;
  getRowClass?: (row: T) => string;
  pagination?: PaginationState;
  onPaginationChange?: OnChangeFn<PaginationState>;
}) {
  const [sorting, setSorting] = useState<SortingState>(initialSort);
  const [localPagination, setLocalPagination] = useState({
    pageIndex: 0,
    pageSize,
  });
  const pagination = controlledPagination ?? localPagination;
  // TanStack Table is intentionally used without React Compiler memoization.
  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({
    data,
    columns,
    state: { sorting, pagination },
    onSortingChange: setSorting,
    onPaginationChange: onPaginationChange ?? setLocalPagination,
    autoResetPageIndex: false,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
  });
  return (
    <div className="data-table">
      <label className="mobile-sort">
        Sort{" "}
        <select
          aria-label="Sort table"
          value={
            sorting[0]
              ? `${sorting[0].id}:${sorting[0].desc ? "desc" : "asc"}`
              : ""
          }
          onChange={(e) => {
            const [id, direction] = e.target.value.split(":");
            setSorting(id ? [{ id, desc: direction === "desc" }] : []);
          }}
        >
          <option value="">Default</option>
          {table
            .getAllLeafColumns()
            .filter((c) => c.getCanSort())
            .flatMap((c) =>
              ["asc", "desc"].map((direction) => (
                <option
                  value={`${c.id}:${direction}`}
                  key={`${c.id}:${direction}`}
                >
                  {String(c.columnDef.header)} {direction === "asc" ? "↑" : "↓"}
                </option>
              )),
            )}
        </select>
      </label>
      <div
        className="table-scroll"
        tabIndex={0}
        role="region"
        aria-label="Data table"
      >
        <table>
          <thead>
            {table.getHeaderGroups().map((group) => (
              <tr key={group.id}>
                {group.headers.map((header) => (
                  <th key={header.id}>
                    {header.column.getCanSort() ? (
                      <button
                        className="sort-button"
                        onClick={header.column.getToggleSortingHandler()}
                        aria-label={`Sort ${String(header.column.columnDef.header)}`}
                      >
                        {flexRender(
                          header.column.columnDef.header,
                          header.getContext(),
                        )}
                        <ArrowUpDown size={12} />
                        {header.column.getIsSorted() === "asc"
                          ? " ↑"
                          : header.column.getIsSorted() === "desc"
                            ? " ↓"
                            : ""}
                      </button>
                    ) : (
                      flexRender(
                        header.column.columnDef.header,
                        header.getContext(),
                      )
                    )}
                  </th>
                ))}
              </tr>
            ))}
          </thead>
          <tbody>
            {table.getRowModel().rows.map((row) => (
              <tr className={getRowClass?.(row.original)} key={row.id}>
                {row.getVisibleCells().map((cell) => (
                  <td
                    key={cell.id}
                    data-label={
                      typeof cell.column.columnDef.header === "string"
                        ? cell.column.columnDef.header
                        : ""
                    }
                  >
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {data.length === 0 && (
        <div className="empty" role="status">
          <Search size={24} />
          <strong>No records found</strong>
          <p>{empty}</p>
        </div>
      )}
      <div className="pagination">
        <label>
          Show{" "}
          <select
            aria-label="Rows per page"
            value={table.getState().pagination.pageSize}
            onChange={(e) => table.setPageSize(Number(e.target.value))}
          >
            {[4, 6, 10, 20].map((n) => (
              <option value={n} key={n}>
                {n} / page
              </option>
            ))}
          </select>
        </label>
        <span>
          {data.length} results · Page{" "}
          {table.getState().pagination.pageIndex + 1} /{" "}
          {Math.max(1, table.getPageCount())}
        </span>
        <div className="actions">
          <Button
            aria-label="Previous page"
            disabled={!table.getCanPreviousPage()}
            onClick={() => table.previousPage()}
          >
            <ChevronLeft size={16} />
          </Button>
          <Button
            aria-label="Next page"
            disabled={!table.getCanNextPage()}
            onClick={() => table.nextPage()}
          >
            <ChevronRight size={16} />
          </Button>
        </div>
      </div>
    </div>
  );
}
