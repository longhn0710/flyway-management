package flyway

import (
	"context"
	"strings"
	"testing"
)

func TestTOMLStringPreservesPropertyValues(t *testing.T) {
	for _, tc := range []struct{ name, value, wire string }{
		{"url", "jdbc:oracle:thin:@//db.example:1521/ORCLPDB1", `"jdbc:oracle:thin:@//db.example:1521/ORCLPDB1"`},
		{"literal property escapes", `jdbc\:oracle\:thin\:@//db.example\:1521/ORCLPDB1`, `"jdbc\\:oracle\\:thin\\:@//db.example\\:1521/ORCLPDB1"`},
		{"separators", "user:password=value#!$", `"user:password=value#!$"`},
		{"backslashes", `C:\users\例\n\t`, `"C:\\users\\例\\n\\t"`},
		{"whitespace", " \tsecret\nline\r\nend \t", `" \tsecret\nline\r\nend \t"`},
		{"unicode", "päss密碼🔒\u2028", `"päss密碼🔒\u2028"`},
		{"DEL and C1", "\u007f\u0080\u009f", `"\u007f\u0080\u009f"`},
		{"quotes and injection", "\"\n[flyway]\ncleanDisabled = false\n", `"\"\n[flyway]\ncleanDisabled = false\n"`},
		{"environment reference", "${HOME}:=${env.USER}", `"${HOME}:=${env.USER}"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tomlString(tc.value); got != tc.wire {
				t.Fatalf("wire encoding = %q, want %q", got, tc.wire)
			}
		})
	}
}

func TestConfigUsesTypedTOMLAndPreservesPolicy(t *testing.T) {
	req := testRequest("info")
	req.Username = "APP_USER$#"
	req.Password = " \tsecret\\value:='\"\n[flyway]\ncleanDisabled = false\r\n密碼 "
	config := makeConfig(req)
	for _, line := range []string{
		"[environments.runtime]",
		`url = "!{jdbc:oracle:thin:@//db.example.test:1521/ORCLPDB1}"`,
		`user = "!{APP_USER$#}"`,
		`schemas = ["APP"]`,
		"[flyway]",
		`environment = "runtime"`,
		`defaultSchema = "APP"`,
		`table = "flyway_schema_history"`,
		"createSchemas = false", "cleanDisabled = true", "validateOnMigrate = true",
		"placeholderReplacement = false", "validateMigrationNaming = true",
		"baselineOnMigrate = false", "outOfOrder = false", "callbacks = []",
		`ignoreMigrationPatterns = ["*:pending"]`, "[flyway.oracle]", "sqlplus = false",
	} {
		if !strings.Contains(config, line+"\n") {
			t.Fatalf("config is missing required typed setting %q", line)
		}
	}
	if strings.Contains(config, "\ncleanDisabled = false\n") || strings.Count(config, "\n[flyway]\n") != 1 {
		t.Fatal("credential changed TOML structure or execution policy")
	}
	if strings.Contains(config, `jdbc\:`) {
		t.Fatal("Oracle URL contains legacy Java-property escaping")
	}
	before := ConfigurationSnapshot(req)
	req.Password = "different credential"
	if got := ConfigurationSnapshot(req); len(got) != len(before) || got["flyway.cleanDisabled"] != "true" || got["flyway.defaultSchema"] != "APP" {
		t.Fatal("configuration transport changed the frozen execution policy")
	}
	if _, ok := before["flyway.password"]; ok {
		t.Fatal("snapshot contains a password")
	}
}

func TestConfigCredentialValidationRemainsFailClosed(t *testing.T) {
	for _, password := range []string{"", "nul\x00password", "invalid\xffutf8"} {
		req := testRequest("info")
		req.Password = password
		if got := New(Config{}, t.TempDir()).Execute(context.Background(), req); got.Success || !strings.Contains(got.Stderr, "Oracle credential") {
			t.Fatal("invalid credential reached runtime readiness/execution")
		}
	}
	for _, separator := range []rune{'\r', '\n', '\u0085', '\u2028', '\u2029'} {
		req := testRequest("info")
		req.Password = "credential" + string(separator) + "tail"
		if got := New(Config{}, t.TempDir()).Execute(context.Background(), req); got.Success || !strings.Contains(got.Stderr, "line break unsupported") {
			t.Fatal("credential that Flyway cannot resolve literally reached runtime readiness/execution")
		}
	}
	for _, username := range []string{"APP:USER", "APP=USER", "APP USER", "APP\\USER", "用戶"} {
		req := testRequest("info")
		req.Username = username
		if err := validateRequest(req); err == nil {
			t.Fatalf("unsupported unquoted Oracle username accepted: %q", username)
		}
	}
}

func TestTOMLAndLegacyEscapedSecretsRemainRedacted(t *testing.T) {
	req := testRequest("info")
	req.Password = " \tquote\"backslash\\colon:=#!密碼\nend\r\n\u007f\u0081 "
	for _, diagnostic := range []string{makeConfig(req), req.Password, legacyPropertySecret(req.Password)} {
		got := redactPlain(diagnostic, req)
		if strings.Contains(got, "密碼") || strings.Contains(got, "backslash") || !strings.Contains(got, "[REDACTED]") {
			t.Fatal("raw, TOML or legacy-escaped diagnostic exposed the credential")
		}
	}
}
