import { useState } from "react";
import { Database, ChevronDown } from "lucide-react";
import { Badge, Check, Notice, SearchBox } from "./ui";
import { previewFixtures as fixtures } from "../lib/fixtures";
const { targets } = fixtures;
export default function ScopeEditor({ viewer = false }: { viewer?: boolean }) {
  const [q, setQ] = useState(""),
    [grants, setGrants] = useState<Record<string, boolean>>({
      "db-uat-finance:CORE_BANKING:logs": true,
      "db-uat-finance:PAYMENT:logs": true,
      ...(!viewer
        ? {
            "db-uat-finance:CORE_BANKING:execute": true,
            "db-uat-finance:CORE_BANKING:upload": true,
          }
        : {}),
    });
  const grant = (id: string, value: boolean) =>
    setGrants({ ...grants, [id]: value });
  return (
    <div className="scope-editor">
      <h3>Phân quyền chi tiết theo Database & Schema</h3>
      <SearchBox
        label="Tìm scope"
        value={q}
        onChange={setQ}
        placeholder="Tìm target hoặc schema…"
      />
      {targets
        .filter((t) =>
          [t.name, ...t.schemas]
            .join(" ")
            .toLowerCase()
            .includes(q.toLowerCase()),
        )
        .map((target) => (
          <details open key={target.id} className="scope-target">
            <summary>
              <Database size={15} />
              <code>{target.name}</code>
              <Badge tone={target.environment === "PROD" ? "error" : "accent"}>
                {target.environment} · {target.service}
              </Badge>
              <ChevronDown size={14} />
            </summary>
            <div className="scope-schemas">
              {target.schemas.map((schema) => (
                <div className="scope-schema" key={schema}>
                  <div className="spread wrap">
                    <code>Schema: {schema}</code>
                    <Badge>
                      {target.environment === "PROD"
                        ? "Cấp rõ ràng"
                        : "Quyền tùy chỉnh"}
                    </Badge>
                  </div>
                  <div className="grid-two">
                    {[
                      ["execute", "Khởi tạo migration (Chạy DDL)"],
                      ["upload", "Tải lên bundle migration"],
                      ["logs", "Xem nhật ký (Read logs)"],
                      ["sql", "Tải mã nguồn SQL"],
                    ].map(([action, label]) => {
                      const id = `${target.id}:${schema}:${action}`;
                      return (
                        <Check
                          key={id}
                          label={label}
                          checked={!!grants[id]}
                          onChange={(v) => grant(id, v)}
                        />
                      );
                    })}
                  </div>
                  {target.environment === "PROD" && (
                    <Check
                      label="Cho phép chạy PROD (explicit grant, không kế thừa)"
                      checked={!!grants[`${target.id}:${schema}:prod`]}
                      onChange={(v) => grant(`${target.id}:${schema}:prod`, v)}
                    />
                  )}
                </div>
              ))}
            </div>
          </details>
        ))}
      <Notice>
        Admin không tự động có quyền chạy PROD. Bản nháp quyền không được gửi
        tới API.
      </Notice>
    </div>
  );
}
