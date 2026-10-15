import { test, expect, type Page } from "@playwright/test";
const routes = [
  "/runs",
  "/runs/new",
  "/runs/RUN-1048",
  "/runs/RUN-1047",
  "/databases",
  "/databases/db-uat-finance",
  "/execution-accounts",
  "/access/users",
  "/access/service-accounts",
  "/settings/authentication",
];
async function preview(page: Page) {
  await page.goto("/login");
  await page.getByRole("button", { name: "Mở console minh họa" }).click();
  await expect(page).toHaveURL("/runs");
}
async function controlSlowFileReads(page: Page) {
  await page.addInitScript(() => {
    const original = File.prototype.arrayBuffer;
    const browserWindow = window as Window & { __slowFilePending?: boolean };
    browserWindow.__slowFilePending = false;
    File.prototype.arrayBuffer = function () {
      if (this.name !== "slow.sql") return original.call(this);
      browserWindow.__slowFilePending = true;
      return new Promise<ArrayBuffer>((resolve) => {
        window.addEventListener(
          "release-slow-file-read",
          () => {
            browserWindow.__slowFilePending = false;
            original.call(this).then(resolve);
          },
          { once: true },
        );
      });
    };
  });
}
async function waitForSlowFileRead(page: Page) {
  await page.waitForFunction(
    () => (window as Window & { __slowFilePending?: boolean }).__slowFilePending,
  );
}
async function releaseSlowFileRead(page: Page) {
  await page.evaluate(() =>
    window.dispatchEvent(new Event("release-slow-file-read")),
  );
}
test("login separates providers and preview from unavailable authentication", async ({
  page,
}) => {
  await page.goto("/login");
  await expect(
    page.getByRole("button", { name: "Đăng nhập vào console" }),
  ).toBeDisabled();
  await page.getByRole("tab", { name: "LDAP / Active Directory" }).click();
  await expect(page.getByText("Đăng nhập bằng tài khoản LDAP")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Đăng nhập bằng SSO (OIDC)" }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Mở console minh họa" }).click();
  await expect(
    page.getByText("Dữ liệu minh họa · Development fixtures"),
  ).toBeVisible();
  await page.getByRole("button", { name: "Trạng thái hệ thống" }).click();
  await expect(page.getByRole("dialog")).toContainText("SQLite");
  await expect(page.getByRole("dialog")).toContainText("ok");
});
test("history search, source, sorting, columns, URL pagination and empty state", async ({
  page,
}) => {
  await preview(page);
  await page.getByRole("button", { name: "Trang sau", exact: true }).click();
  await expect(page).toHaveURL(/page=1/);
  await page.reload();
  await expect(page.getByText("12 kết quả · Trang 2 / 2")).toBeVisible();
  await page
    .getByRole("combobox", { name: "Nguồn", exact: true })
    .selectOption("API");
  await expect(page).not.toHaveURL(/page=1/);
  await expect(page.locator("tbody tr")).toHaveCount(5);
  await page.getByRole("button", { name: "Sắp xếp Run ID & Nguồn" }).click();
  await page.getByRole("button", { name: "Tùy chọn cột" }).click();
  await page.getByRole("checkbox", { name: "Artifact filename" }).uncheck();
  await page.getByRole("button", { name: "Áp dụng", exact: true }).click();
  await expect(
    page.getByRole("columnheader", { name: "Artifact filename" }),
  ).toHaveCount(0);
  await page
    .getByRole("textbox", { name: "Tìm kiếm", exact: true })
    .fill("does-not-exist");
  await expect(page.getByText("Không tìm thấy dữ liệu")).toBeVisible();
  await page.getByRole("button", { name: "Đặt lại", exact: true }).click();
  await expect(page.locator("tbody tr")).toHaveCount(6);
});
test("local upload metadata, draft review, no execution requests", async ({
  page,
}) => {
  const mutations: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes("/api/") && r.method() !== "GET")
      mutations.push(r.url());
  });
  await preview(page);
  await page.goto("/runs/new");
  await page
    .getByRole("button", { name: "Tiếp tục: Kiểm tra & xác nhận" })
    .click();
  await expect(page.getByRole("alert")).toContainText("Vui lòng chọn tệp");
  await page.getByRole("button", { name: "Tải tệp SQL", exact: true }).click();
  await page.locator("input[type=file]").setInputFiles({
    name: "V1__create_table.sql",
    mimeType: "text/plain",
    buffer: Buffer.from("CREATE TABLE example (id NUMBER);"),
  });
  await expect(page.getByText(/SHA-256 cục bộ:/)).toBeVisible();
  await expect(page.getByText("CHƯA XÁC MINH BỞI MÁY CHỦ")).toBeVisible();
  await page
    .getByRole("button", { name: "Tiếp tục: Kiểm tra & xác nhận" })
    .click();
  await expect(page).toHaveURL("/runs/new/review");
  await expect(
    page.getByRole("button", { name: "Gửi migration" }),
  ).toBeDisabled();
  await page.getByRole("link", { name: "Quay lại chọn bundle" }).click();
  await expect(page.getByText("V1__create_table.sql")).toBeVisible();
  expect(mutations).toEqual([]);
});
test("latest local bundle selection wins over an older pending checksum read", async ({
  page,
}) => {
  await controlSlowFileReads(page);
  await preview(page);
  await page.goto("/runs/new");
  const input = page.locator("input[type=file]");
  await input.setInputFiles({
    name: "slow.sql",
    mimeType: "text/plain",
    buffer: Buffer.from("SELECT 1;"),
  });
  await waitForSlowFileRead(page);
  await input.setInputFiles({
    name: "latest.sql",
    mimeType: "text/plain",
    buffer: Buffer.from("SELECT 2;"),
  });
  await expect(page.getByText("latest.sql")).toBeVisible();
  await expect(page.getByText("slow.sql")).toHaveCount(0);
  await releaseSlowFileRead(page);
  await expect(page.getByText("latest.sql")).toBeVisible();
  await expect(page.getByText("slow.sql")).toHaveCount(0);

  await input.setInputFiles({
    name: "slow.sql",
    mimeType: "text/plain",
    buffer: Buffer.from("SELECT 3;"),
  });
  await waitForSlowFileRead(page);
  await page.getByLabel("Target Schema", { exact: false }).selectOption("PAYMENT");
  await releaseSlowFileRead(page);
  await expect(page.getByText("slow.sql")).toBeVisible();
  await expect(page.getByLabel("Target Schema", { exact: false })).toHaveValue(
    "PAYMENT",
  );

  await input.setInputFiles({
    name: "slow.sql",
    mimeType: "text/plain",
    buffer: Buffer.from("SELECT 4;"),
  });
  await waitForSlowFileRead(page);
  await page.getByRole("button", { name: "Dùng bundle minh họa" }).click();
  await releaseSlowFileRead(page);
  await expect(page.getByText("FIXTURE · Minh họa")).toBeVisible();
  await expect(page.getByText("slow.sql")).toHaveCount(0);
});
test("clearing bundle invalidates pending checksum work", async ({ page }) => {
  await controlSlowFileReads(page);
  await preview(page);
  await page.goto("/runs/new");
  const input = page.locator("input[type=file]");
  await input.setInputFiles({
    name: "baseline.sql",
    mimeType: "text/plain",
    buffer: Buffer.from("SELECT 0;"),
  });
  await expect(page.getByText("baseline.sql")).toBeVisible();
  await input.setInputFiles({
    name: "slow.sql",
    mimeType: "text/plain",
    buffer: Buffer.from("SELECT 1;"),
  });
  await waitForSlowFileRead(page);
  await page.getByRole("button", { name: "Xóa" }).click();
  await releaseSlowFileRead(page);
  await expect(page.getByText("baseline.sql")).toHaveCount(0);
  await expect(page.getByText("slow.sql")).toHaveCount(0);
  await expect(page.getByText("1 tệp cục bộ")).toHaveCount(0);
});
test("unmounting migration invalidates pending read before a newer draft", async ({
  page,
}) => {
  await controlSlowFileReads(page);
  await preview(page);
  await page.goto("/runs/new");
  const input = page.locator("input[type=file]");
  await input.setInputFiles({
    name: "slow.sql",
    mimeType: "text/plain",
    buffer: Buffer.from("SELECT 1;"),
  });
  await waitForSlowFileRead(page);
  await page.getByRole("link", { name: "Lịch sử chạy" }).click();
  await page.getByRole("link", { name: "Chạy migration" }).click();
  await page.locator("input[type=file]").setInputFiles({
    name: "newer.sql",
    mimeType: "text/plain",
    buffer: Buffer.from("SELECT 2;"),
  });
  await expect(page.getByText("newer.sql")).toBeVisible();
  await releaseSlowFileRead(page);
  await expect(page.getByText("newer.sql")).toBeVisible();
  await expect(page.getByText("slow.sql")).toHaveCount(0);
});
test("fixture bundle review and schema selection preserve distinct history", async ({
  page,
}) => {
  await preview(page);
  await page.goto("/runs/new");
  await page
    .getByLabel("Target Schema", { exact: false })
    .selectOption("PAYMENT");
  await page.getByRole("button", { name: "Dùng bundle minh họa" }).click();
  await page
    .getByRole("button", { name: "Tiếp tục: Kiểm tra & xác nhận" })
    .click();
  await expect(page.getByText("PAYMENT.flyway_schema_history")).toBeVisible();
  await expect(page.getByText("Chưa sẵn sàng để thực thi.")).toBeVisible();
});
test("failure remains failed after successful repair; searchable source-labelled logs and downloads", async ({
  page,
}) => {
  await preview(page);
  await page.goto("/runs/RUN-1047");
  await expect(page.getByText("FAILED", { exact: true }).first()).toBeVisible();
  await expect(
    page.getByText("SUCCEEDED", { exact: true }).first(),
  ).toBeVisible();
  await expect(
    page.getByText("Schema: NEEDS_REVIEW · Exit code: 1"),
  ).toBeVisible();
  await page.getByRole("textbox", { name: "Tìm trong log" }).fill("ORA-00955");
  await expect(page.locator(".log-row")).toHaveCount(1);
  await page
    .getByRole("combobox", { name: "Nguồn log" })
    .selectOption("stdout");
  await expect(page.getByText("Không có dòng log khớp bộ lọc.")).toBeVisible();
  await page
    .getByRole("combobox", { name: "Nguồn log" })
    .selectOption("stderr");
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Tải log minh họa (.txt)" }).click();
  expect((await download).suggestedFilename()).toBe(
    "RUN-1047-fixture-logs.txt",
  );
  await page.getByRole("tab", { name: "Schema History / Audit" }).click();
  await expect(
    page.getByText("CORE_BANKING.flyway_schema_history"),
  ).toBeVisible();
});
test("registry filtering, draft form, database stream configuration", async ({
  page,
}) => {
  await preview(page);
  await page.goto("/databases");
  await page.getByRole("button", { name: "PROD", exact: true }).click();
  await expect(page.locator(".registry-row")).toHaveCount(1);
  await page
    .getByRole("button", { name: "Thêm Database", exact: true })
    .click();
  await page.getByLabel("Tên hiển thị").fill("ORA_DEV_TEST");
  await page.getByLabel("Stable ID").fill("db-dev-test");
  await page.getByLabel("Hostname").fill("oracle.local");
  await page.getByLabel("Oracle Service / PDB").fill("TEST");
  await page.getByRole("button", { name: "Kiểm tra bản nháp" }).click();
  await expect(page.getByRole("status")).toContainText("Chưa lưu");
  await page.keyboard.press("Escape");
  await page.goto("/databases/db-uat-finance");
  await page.getByRole("tab", { name: "PAYMENT V1" }).click();
  await expect(page.getByLabel("Target schema (đích migration)")).toHaveValue(
    "PAYMENT",
  );
  await page.getByRole("radio", { name: "Dedicated History Schema" }).check();
  await page
    .getByLabel("History schema", { exact: true })
    .fill("FLYWAY_HISTORY");
  await expect(
    page.getByText("FLYWAY_HISTORY.flyway_schema_history"),
  ).toBeVisible();
});
test("credential draft masking and accessible discard confirmation", async ({
  page,
}) => {
  await preview(page);
  await page.goto("/execution-accounts");
  const password = page.getByLabel("Mật khẩu mới Oracle", { exact: true });
  await expect(password).toHaveValue("");
  await password.fill("fixture-secret");
  await expect(password).toHaveAttribute("type", "password");
  await page.getByRole("button", { name: "Hiện mật khẩu mới" }).click();
  await expect(password).toHaveAttribute("type", "text");
  await page.getByRole("button", { name: "Hủy thay đổi", exact: true }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.getByRole("button", { name: "Hủy bản nháp", exact: true }).click();
  await expect(password).toHaveValue("");
  await expect(
    page.getByRole("button", { name: "Lưu & Cập nhật ánh xạ" }),
  ).toBeDisabled();
});
test("scoped RBAC keeps PROD explicit and modal focus returns", async ({
  page,
}) => {
  await preview(page);
  await page.goto("/access/users");
  const prod = page
    .getByRole("checkbox", {
      name: "Cho phép chạy PROD (explicit grant, không kế thừa)",
    })
    .first();
  await expect(prod).not.toBeChecked();
  await expect(prod).toBeEnabled();
  await prod.check();
  await expect(prod).toBeChecked();
  await expect(
    page.getByRole("button", { name: "Lưu quyền truy cập" }),
  ).toBeDisabled();
  const disable = page.getByRole("button", {
    name: "Vô hiệu hóa người dùng",
    exact: true,
  });
  await disable.click();
  await expect(page.getByRole("dialog")).toContainText("Lê Thu Hà");
  await page.keyboard.press("Escape");
  await expect(disable).toBeFocused();
});
test("service tokens and auth providers expose honest unavailable actions", async ({
  page,
}) => {
  await preview(page);
  await page.goto("/access/service-accounts");
  await expect(
    page.getByRole("button", { name: "Tạo mã Token Scoped mới" }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "Thu hồi token", exact: true })
    .first()
    .click();
  await expect(page.getByRole("dialog")).toContainText("CI/CD");
  await expect(
    page.getByRole("button", { name: "Xác nhận thu hồi" }),
  ).toBeDisabled();
  await page.keyboard.press("Escape");
  await page.goto("/settings/authentication");
  await page
    .getByRole("checkbox", { name: "Kích hoạt đăng nhập cục bộ" })
    .click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.getByRole("button", { name: "Giữ cấu hình cục bộ" }).click();
  await page.getByRole("tab", { name: "LDAP / Active Directory" }).click();
  await expect(
    page.getByText("Direct LDAP bind · Không lưu mật khẩu LDAP"),
  ).toBeVisible();
  await page.getByRole("tab", { name: "OIDC / SSO" }).click();
  await expect(
    page.getByRole("heading", { name: "Authorization Code + PKCE" }),
  ).toBeVisible();
});
test("API connection error is visible and retryable", async ({ page }) => {
  await page.route("**/api/v1/health", (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({
        error: { code: "DATABASE_UNAVAILABLE", message: "SQLite unavailable" },
      }),
    }),
  );
  await preview(page);
  await page.getByRole("button", { name: "Trạng thái hệ thống" }).click();
  await expect(
    page.getByRole("dialog").getByText("SQLite unavailable"),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Thử lại", exact: true }),
  ).toBeVisible();
});
for (const width of [1440, 390])
  test(`all routes render with no page overflow at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 });
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await preview(page);
    for (const route of routes) {
      await page.goto(route);
      await expect(page.locator("h1")).toBeVisible();
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth),
      ).toBeLessThanOrEqual(width);
      await expect(
        page.getByText("Dữ liệu minh họa · Development fixtures"),
      ).toBeVisible();
      if (["/runs", "/runs/new", "/access/users"].includes(route))
        await page.screenshot({
          path: testInfo.outputPath(
            `${route.replaceAll("/", "-")}-${width}.png`,
          ),
          fullPage: true,
        });
    }
    if (width === 390) {
      await page.getByRole("button", { name: "Mở điều hướng" }).click();
      await page
        .getByRole("dialog")
        .getByRole("link", { name: "Database & Schema" })
        .click();
      await expect(page).toHaveURL("/databases");
    }
    expect(errors).toEqual([]);
  });
