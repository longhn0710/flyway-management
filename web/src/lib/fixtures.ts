// Development fixtures are bundled with the UI only. Never send them to business APIs.
import type {
  DatabaseTarget,
  MigrationRun,
  RunStatus,
  RepairStatus,
} from "./contracts";
const targets: DatabaseTarget[] = [
  {
    id: "db-uat-finance",
    name: "ORA_UAT_FINANCE",
    environment: "UAT",
    host: "oracle-uat.internal:1521",
    service: "FINUAT",
    transport: "TCPS / TLS",
    schemas: ["CORE_BANKING", "PAYMENT"],
  },
  {
    id: "db-prod-finance",
    name: "ORA_PROD_FINANCE",
    environment: "PROD",
    host: "oracle-prod.internal:1521",
    service: "FINPRD",
    transport: "TCPS / TLS",
    schemas: ["CORE_BANKING", "PAYMENT"],
  },
  {
    id: "db-dev-crm",
    name: "ORA_DEV_CRM",
    environment: "DEV",
    host: "dev.internal:1521",
    service: "CRMDEV",
    transport: "TCP",
    schemas: ["CRM"],
  },
];
const statuses: RunStatus[] = [
  "RUNNING",
  "FAILED",
  "BLOCKED",
  "SUCCEEDED",
  "QUEUED",
  "NEEDS_REVIEW",
  "SUCCEEDED",
  "CANCELLED",
  "SUCCEEDED",
  "FAILED",
  "REPAIRING",
  "SUCCEEDED",
];
const repair: RepairStatus[] = [
  "NOT_APPLICABLE",
  "SUCCEEDED",
  "UNSAFE",
  "NOT_REQUIRED",
  "NOT_APPLICABLE",
  "NOT_APPLICABLE",
];
const runs: MigrationRun[] = statuses.map((status, i) => ({
  id: `RUN-${1048 - i}`,
  source: [1, 2, 3, 6, 10].includes(i) ? "API" : "WEB",
  status,
  repair: repair[i] ?? "NOT_REQUIRED",
  targetId: "db-uat-finance",
  target: "ORA_UAT_FINANCE",
  environment: "UAT",
  service: "FINUAT",
  schema: [2, 3, 4, 6, 8].includes(i) ? "PAYMENT" : "CORE_BANKING",
  artifactId:
    i === 0 ? "ART-82FD21" : i === 1 ? "ART-82EB19" : `ART-82EB${17 - i}`,
  artifact:
    i === 0
      ? "finance_v3.zip"
      : i === 1
        ? "finance_v3.2.0_patch_idx.zip"
        : `migration_v${1048 - i}.zip`,
  actor: [1, 2, 3, 6, 10].includes(i)
    ? "ci-finance-uat"
    : i === 4
      ? "Lê Thùy Dung"
      : "Nguyễn Minh Anh",
  actorType: [1, 2, 3, 6, 10].includes(i) ? "Service Account" : "Human User",
  account: "DEPLOY_ADMIN",
  startedAt:
    i === 0
      ? "2025-05-19 14:32:10"
      : i === 1
        ? "2025-05-18 22:15:02"
        : `2025-05-${String(18 - i).padStart(2, "0")} 10:05:18`,
  duration: i === 0 ? "42s" : i === 1 ? "36s" : i === 4 ? "—" : `${24 + i}s`,
  version: i === 0 ? "V3.1.1" : i === 1 ? "V3.2.0" : "V1.9",
}));
const accounts = [
  {
    id: "acc-deploy-admin",
    name: "DEPLOY_ADMIN",
    provider: "Local",
    schemas: ["CORE_BANKING", "PAYMENT"],
    rotated: "2025-04-10 09:30:14",
  },
  {
    id: "acc-app-migrator",
    name: "APP_MIGRATOR",
    provider: "Local",
    schemas: ["CORE_BANKING"],
    rotated: "2025-03-22 14:15:00",
  },
  {
    id: "acc-payment",
    name: "PAYMENT_MIGRATOR",
    provider: "Vault",
    schemas: ["PAYMENT"],
    rotated: "2025-05-01 11:00:45",
  },
];
const users = [
  {
    id: "usr_admin_01",
    name: "Nguyễn Minh Anh",
    email: "minhanh@bank.vn",
    provider: "Local",
    subject: "internal:01",
    role: "Admin",
    active: true,
  },
  {
    id: "usr_op_02",
    name: "Lê Thu Hà",
    email: "lethuha@bank.vn",
    provider: "LDAP",
    subject: "550e8400-e29b-41d4-a716-446655440000",
    role: "Operator",
    active: true,
  },
  {
    id: "usr_vw_03",
    name: "Phạm Quốc Bảo",
    email: "quocbao@bank.vn",
    provider: "OIDC",
    subject: "oidc-sub-99218",
    role: "Viewer",
    active: true,
  },
  {
    id: "usr_op_legacy",
    name: "Đỗ Hoàng Long",
    email: "hoanglong@bank.vn",
    provider: "Local",
    subject: "internal:legacy_09",
    role: "Operator",
    active: false,
  },
];
const manifest = [
  "migrations/V3.1__create_table_settlement_ledger.sql",
  "migrations/V3.1.1__add_index_settlement_tx_id.sql",
  "migrations/V3.2__create_sp_reconcile_daily.sql",
  "views/R__refresh_summary_views.sql",
];
interface LogLine {
  sequence: number;
  time: string;
  level: "INFO" | "WARN" | "ERROR";
  source: "stdout" | "stderr" | "event";
  message: string;
}
function logsFor(run: MigrationRun): LogLine[] {
  const failed = run.status === "FAILED";
  const rows: [LogLine["level"], LogLine["source"], string][] = failed
    ? [
        [
          "INFO",
          "event",
          "Request received from ci-finance-uat. Caller metadata: initiated_by=pipeline-1047 (untrusted).",
        ],
        [
          "INFO",
          "stdout",
          "Migrating schema CORE_BANKING to version 3.2.0 — settlement_speedup",
        ],
        [
          "INFO",
          "stdout",
          "Executing statement: CREATE INDEX CORE_BANKING.IDX_SETTLEMENT_TX_REF ON CORE_BANKING.SETTLEMENT_TX(TX_REF_ID)",
        ],
        [
          "ERROR",
          "stderr",
          "Migration of schema CORE_BANKING to version 3.2.0 failed.",
        ],
        [
          "ERROR",
          "stderr",
          "ORA-00955: name is already used by an existing object",
        ],
        [
          "WARN",
          "event",
          "Execution halted with exitCode=1. Immutable error snapshot persisted BEFORE repair.",
        ],
        [
          "INFO",
          "event",
          "Repair eligibility: single failed tail record; identical stream/config/history; complete historical + new locations.",
        ],
        [
          "INFO",
          "event",
          "REP-204: before/after evidence verified; no unrelated checksum realignment or missing migration deletion.",
        ],
        [
          "INFO",
          "event",
          "Failed row V3.2.0 removed. Repair SUCCEEDED. Migration remains FAILED.",
        ],
        [
          "WARN",
          "event",
          "Stream NEEDS_REVIEW. Partial Oracle DDL may remain. No SQL retried.",
        ],
      ]
    : [
        [
          "INFO",
          "event",
          `Illustrative ${run.id} snapshot. No live execution or log stream.`,
        ],
        [
          "INFO",
          "stdout",
          "Reconstructed immutable migration set: 2 historical files + 14 bundle files = 16 combined files.",
        ],
        [
          "INFO",
          "stdout",
          "Successfully validated 16 migrations in the fixture scenario.",
        ],
        [
          "INFO",
          "event",
          "Stream serialization acquired for db-uat-finance / CORE_BANKING.",
        ],
        ["INFO", "stdout", "Current version of schema CORE_BANKING: V2"],
        [
          "INFO",
          "stdout",
          "Migrating schema CORE_BANKING to version 3.1 — create_table_settlement_ledger",
        ],
        [
          "INFO",
          "stdout",
          "Migrating schema CORE_BANKING to version 3.1.1 — add_index_settlement_tx_id",
        ],
      ];
  return rows.map(([level, source, message], i) => ({
    sequence: i + 1,
    time: `22:15:${String(2 + i * 2).padStart(2, "0")}`,
    level,
    source,
    message,
  }));
}

// Keep preview records behind one explicitly named boundary. These collections
// are browser-only demo content and must never be merged with API responses.
export const previewFixtures = {
  targets,
  runs,
  accounts,
  users,
  manifest,
  logsFor,
} as const;
