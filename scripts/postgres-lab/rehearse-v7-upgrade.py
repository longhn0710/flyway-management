#!/usr/bin/env python3
"""Restore and verify a populated SQLite v7 backup entirely off the live path."""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import struct
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
SOURCES: list[Path] = []
KEY: Path
REPORT: Path
GO_TEST = "TestPostgreSQLFoundationV7UpgradeRehearsal"


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def source_files(db: Path) -> list[Path]:
    return [p for p in (db, Path(str(db) + "-wal"), Path(str(db) + "-shm"), Path(str(db) + "-journal")) if p.exists()]


def file_fingerprints(paths: list[Path]) -> dict[str, str]:
    return {str(p): sha256(p) for p in paths}


def qident(value: str) -> str:
    return '"' + value.replace('"', '""') + '"'


def typed(value_type: bytes, value: object) -> bytes:
    kind = value_type.decode("ascii")
    if kind in ("text", "blob"):
        data = b"" if value is None else bytes(value)
    elif kind == "integer":
        data = str(value).encode("ascii")
    elif kind == "real":
        data = struct.pack(">d", float(value))
    else:
        data = b""
    return kind.encode("ascii") + b"\x00" + struct.pack(">Q", len(data)) + data


def table_digest(conn: sqlite3.Connection, table: str, columns: list[str], where: str = "") -> tuple[int, str]:
    select = ",".join(f"typeof({qident(c)}),{qident(c)}" for c in columns)
    sql = f"SELECT {select} FROM {qident(table)} {where}"
    row_hashes = []
    for row in conn.execute(sql):
        encoded = bytearray()
        for i in range(0, len(row), 2):
            encoded.extend(typed(row[i], row[i + 1]))
        row_hashes.append(hashlib.sha256(encoded).digest())
    row_hashes.sort()
    h = hashlib.sha256()
    for column in columns:
        data = column.encode("utf-8")
        h.update(struct.pack(">Q", len(data)))
        h.update(data)
    for row_hash in row_hashes:
        h.update(row_hash)
    return len(row_hashes), h.hexdigest()


def open_ro(path: Path) -> sqlite3.Connection:
    conn = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    conn.text_factory = bytes
    return conn


def table_columns(conn: sqlite3.Connection, table: str) -> list[str]:
    return [row[1].decode("utf-8") for row in conn.execute(f"PRAGMA table_info({qident(table)})")]


def tables(conn: sqlite3.Connection) -> list[str]:
    return sorted(row[0].decode("utf-8") for row in conn.execute(
        "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"
    ))


def schema_version(conn: sqlite3.Connection) -> int:
    return int(conn.execute("PRAGMA user_version").fetchone()[0])


def manifest(conn: sqlite3.Connection) -> dict[str, dict[str, object]]:
    result = {}
    for table in tables(conn):
        columns = table_columns(conn, table)
        count, digest = table_digest(conn, table, columns)
        result[table] = {"columns": columns, "rows": count, "sha256": digest}
    return result


def report_write(payload: dict[str, object]) -> None:
    REPORT.parent.mkdir(parents=True, exist_ok=True)
    if REPORT.exists():
        raise RuntimeError("upgrade evidence already exists; refusing to overwrite it")
    temp = REPORT.with_suffix(".json.tmp")
    temp.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    os.chmod(temp, 0o600)
    temp.replace(REPORT)
    os.chmod(REPORT, 0o600)


def main() -> int:
    global SOURCES, KEY, REPORT
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', type=Path, required=True, help='Stopped populated v7 backup; never the live registry')
    parser.add_argument('--key', type=Path, required=True, help='Original private external master key (read only)')
    parser.add_argument('--report', type=Path, required=True, help='New sanitized report path; existing evidence is never overwritten')
    args = parser.parse_args()
    SOURCES, KEY, REPORT = [args.source.resolve()], args.key.resolve(), args.report.resolve()
    if os.name != "posix":
        print("This local rehearsal requires POSIX file permissions.", file=sys.stderr)
        return 2
    if REPORT.exists():
        print("Upgrade evidence already exists; refusing to repeat over the retained result.", file=sys.stderr)
        return 2
    if not KEY.is_file() or KEY.stat().st_mode & 0o077 or KEY.stat().st_size != 32:
        print("The specified original key is unavailable or not private; stopping without searching for another key.", file=sys.stderr)
        return 2
    source = None
    for candidate in SOURCES:
        if not candidate.is_file():
            continue
        try:
            conn = open_ro(candidate)
            version = schema_version(conn)
            conn.close()
        except sqlite3.Error:
            continue
        if version == 7:
            source = candidate
            break
    if source is None:
        print("No listed local source has SQLite user_version 7; no upgrade was attempted.", file=sys.stderr)
        return 2

    source_paths = source_files(source)
    key_before = sha256(KEY)
    source_before = file_fingerprints(source_paths)
    # The source is an offline release backup. Refuse if any process has it open.
    for candidate in source_paths:
        if subprocess.run(
            ["fuser", "-s", str(candidate)],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        ).returncode == 0:
            print("The source backup has an open file handle; stopping without touching it.", file=sys.stderr)
            return 2

    state_base = Path(os.environ.get("XDG_STATE_HOME", str(Path.home() / ".local/state"))) / "flyway-manager-upgrade-rehearsals"
    state_base.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(state_base, 0o700)
    work = Path(tempfile.mkdtemp(prefix="flyway-v7-upgrade-", dir=state_base))
    os.chmod(work, 0o700)
    seed_dir, data_dir = work / "source-seed", work / "upgraded-data"
    seed_dir.mkdir(mode=0o700)
    data_dir.mkdir(mode=0o700)
    seed_db = seed_dir / "manager.db"
    data_db = data_dir / "manager.db"
    for path in source_paths:
        suffix = str(path)[len(str(source)):]
        shutil.copyfile(path, Path(str(seed_db) + suffix))
        os.chmod(Path(str(seed_db) + suffix), 0o600)
    if file_fingerprints(source_paths) != source_before:
        print("The source backup changed during private staging; stopping.", file=sys.stderr)
        return 2

    seed = open_ro(seed_db)
    if schema_version(seed) != 7:
        seed.close()
        print("Private staged source is not v7; no upgrade was attempted.", file=sys.stderr)
        return 2
    baseline = manifest(seed)
    if "schema_migrations" not in baseline or "database_targets" not in baseline:
        seed.close()
        print("The v7 source is missing required registry tables; stopping.", file=sys.stderr)
        return 2
    versions = [row[0] for row in seed.execute("SELECT version FROM schema_migrations ORDER BY version")]
    if versions != list(range(1, 8)):
        seed.close()
        print("The source migration ledger is not exactly v1 through v7; stopping.", file=sys.stderr)
        return 2
    destination = sqlite3.connect(data_db)
    seed.backup(destination, pages=256, sleep=0.01)
    destination.close()
    seed.close()
    os.chmod(data_db, 0o600)
    baseline_path = work / "baseline.json"
    baseline_path.write_text(json.dumps({"tables": baseline, "version": 7}, sort_keys=True), encoding="utf-8")
    os.chmod(baseline_path, 0o600)

    env = os.environ.copy()
    env.update({
        "FWM_RUN_V7_UPGRADE_REHEARSAL": "1",
        "FWM_V7_UPGRADE_DATA_DIR": str(data_dir),
        "FWM_V7_UPGRADE_MASTER_KEY": str(KEY),
    })
    test_log = work / "go-test.log"
    with test_log.open("wb") as output:
        completed = subprocess.run(
            ["go", "test", "./internal/core", "-run", f"^{GO_TEST}$", "-count=1"],
            cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT, check=False,
        )
    source_after = file_fingerprints(source_paths)
    key_after = sha256(KEY)
    if completed.returncode != 0:
        report_write({
            "result": "FAIL",
            "stage": "go_upgrade_or_key_validation",
            "source_schema_version": 7,
            "source_database_unchanged": source_after == source_before,
            "original_key_unchanged": key_after == key_before,
            "execution_started": False,
            "private_evidence_directory": str(work),
        })
        print("Upgrade rehearsal failed during the v8 open or original-key credential validation; private logs retained. No alternate key was sought.", file=sys.stderr)
        return completed.returncode or 1

    upgraded = open_ro(data_db)
    try:
        after_version = schema_version(upgraded)
        after_tables = tables(upgraded)
        after_meta = {name: table_columns(upgraded, name) for name in after_tables}
        legacy_tables = list(baseline)
        preserved_rows = 0
        table_results = {}
        all_equal = True
        for table in legacy_tables:
            old_columns = baseline[table]["columns"]
            if not all(c in after_meta.get(table, []) for c in old_columns):
                all_equal = False
                table_results[table] = {"preserved": False, "reason": "legacy column missing"}
                continue
            if table == "schema_migrations":
                where = "WHERE version BETWEEN 1 AND 7"
                count, digest = table_digest(upgraded, table, old_columns, where)
                expected_count, expected_digest = table_digest_conn_seed(seed_db, table, old_columns)
                # The v8 ledger row is the only expected additional row.
                ledger_count = upgraded.execute("SELECT count(*) FROM schema_migrations").fetchone()[0]
                ok = (count, digest) == (expected_count, expected_digest) and ledger_count == 8
            else:
                count, digest = table_digest(upgraded, table, old_columns)
                ok = count == baseline[table]["rows"] and digest == baseline[table]["sha256"]
            if table == "database_targets":
                ok = ok and after_meta[table] == old_columns + [c for c in after_meta[table] if c not in old_columns]
            table_results[table] = {"preserved": bool(ok), "rows": int(count), "sha256": digest}
            all_equal = all_equal and bool(ok)
            preserved_rows += int(count)
        new_tables = sorted(set(after_tables) - set(legacy_tables))
        integrity = upgraded.execute("PRAGMA integrity_check").fetchone()[0].decode("utf-8")
        fk_violations = upgraded.execute("SELECT count(*) FROM pragma_foreign_key_check").fetchone()[0]
        migration8 = upgraded.execute("SELECT name,checksum FROM schema_migrations WHERE version=8").fetchone()
        if migration8 is not None:
            migration8 = tuple(value.decode("utf-8") if isinstance(value, bytes) else value for value in migration8)
        migration8_expected = hashlib.sha256((ROOT / "internal/database/migrations/008_engine_profiles.sql").read_bytes()).hexdigest()
        credentials = upgraded.execute("SELECT count(*) FROM credential_secrets").fetchone()[0]
    finally:
        upgraded.close()

    passed = (
        after_version == 8 and len(legacy_tables) == 34 and len(after_tables) == 35
        and new_tables == ["target_connection_profiles"] and all_equal
        and integrity == "ok" and fk_violations == 0
        and migration8 is not None and migration8[0] == "008_engine_profiles.sql" and migration8[1] == migration8_expected
        and source_after == source_before and key_after == key_before
    )
    payload = {
        "result": "PASS" if passed else "FAIL",
        "source_backup": source.name,
        "source_directory": str(source.parent),
        "restore_method": "SQLite backup API from a private read-only staged copy",
        "schema_version": {"before": 7, "after": after_version},
        "installation_identity_preserved": table_results.get("installation_metadata", {}).get("preserved", False),
        "original_tables": len(legacy_tables),
        "preserved_tables": sum(1 for item in table_results.values() if item.get("preserved")),
        "original_rows": sum(int(item["rows"]) for item in baseline.values()),
        "preserved_rows": preserved_rows,
        "table_checksums": table_results,
        "row_comparison": "Per-row typed SHA-256 over every original column; BLOB values are hashed byte-for-byte.",
        "new_tables": new_tables,
        "legacy_target_columns_preserved": table_results.get("database_targets", {}).get("preserved", False),
        "migration_ledger_v1_v7_preserved": table_results.get("schema_migrations", {}).get("preserved", False),
        "migration_008_checksum_matches_source": bool(migration8 is not None and migration8[1] == migration8_expected),
        "encrypted_credentials": {"count": credentials, "original_key_decryption": "all passed"},
        "integrity_check": integrity,
        "foreign_key_violations": int(fk_violations),
        "source_database_unchanged": source_after == source_before,
        "source_files_checked": len(source_before),
        "original_key_unchanged": key_after == key_before,
        "execution_started": False,
        "runtime_or_oracle_accessed": False,
        "private_evidence_directory": str(work),
        "checked_at": dt.datetime.now(dt.timezone.utc).isoformat(),
    }
    report_write(payload)
    if not passed:
        print(f"Upgrade preservation checks failed; sanitized evidence: {REPORT}", file=sys.stderr)
        return 1
    print(f"PASS: v7 populated-copy upgrade to v8; {payload['preserved_tables']} tables and {preserved_rows} rows preserved; {credentials} credentials decrypted. Evidence: {REPORT}")
    return 0


def table_digest_conn_seed(path: Path, table: str, columns: list[str]) -> tuple[int, str]:
    conn = open_ro(path)
    try:
        return table_digest(conn, table, columns, "WHERE version BETWEEN 1 AND 7" if table == "schema_migrations" else "")
    finally:
        conn.close()


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        # Do not stringify SQLite rows or connection objects; exceptions here
        # contain only sanitized paths/operation names, never key contents.
        print(f"Upgrade rehearsal stopped safely ({type(error).__name__}).", file=sys.stderr)
        raise SystemExit(1)
