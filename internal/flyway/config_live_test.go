package flyway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// Opt-in, offline compatibility test of an operator-sourced, integrity-pinned
// real distribution. Downloads nothing, opens no database and runs no SQL.
func TestActualFlywayConfigurationLoader(t *testing.T) {
	configPath := os.Getenv("FLYWAY_TEST_RUNTIME_CONFIG")
	if configPath == "" {
		t.Skip("set FLYWAY_TEST_RUNTIME_CONFIG to a JSON file containing an approved flyway runtime pin")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal("cannot read approved runtime configuration")
	}
	var cfg struct {
		Flyway Config `json:"flyway"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		t.Fatal("invalid approved runtime configuration")
	}
	runner := New(cfg.Flyway, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if ready := runner.Check(ctx); !ready.Ready {
		t.Fatalf("approved runtime is not ready: %v", ready.Blockers)
	}
	root, err := runner.distributionRoot()
	if err != nil {
		t.Fatal(err)
	}
	var jars []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".jar") {
			jars = append(jars, path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(jars)
	classpath := strings.Join(jars, string(os.PathListSeparator))
	work := t.TempDir()
	compiler, err := exec.LookPath("javac")
	if err != nil {
		t.Fatal("the opt-in actual-loader test requires javac")
	}
	compile := exec.CommandContext(ctx, compiler, "-proc:none", "--release", "17", "-cp", classpath, "-d", work, filepath.Join("testdata", "FlywayConfigurationProbe.java"))
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("cannot compile the offline actual-loader probe: %v\n%s", err, output)
	}
	javaHome, _ := runtimeJavaHome(root)
	java := filepath.Join(javaHome, "bin", "java")
	if javaHome == "" {
		java, err = exec.LookPath("java")
		if err != nil {
			t.Fatal(err)
		}
	}
	probe := func(mode, path string) map[string]string {
		t.Helper()
		cmd := exec.CommandContext(ctx, java, "-cp", work+string(os.PathListSeparator)+classpath, "FlywayConfigurationProbe", mode, path, work)
		cmd.Dir = work
		cmd.Env = restrictedEnv(work, root)
		output, err := cmd.Output()
		if err != nil {
			// Parser failures may quote credentials; never print captured diagnostics.
			t.Fatalf("actual configuration loader failed: %v", err)
		}
		values := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			key, encoded, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			value, err := base64.StdEncoding.DecodeString(encoded)
			if err == nil {
				values[key] = string(value)
			}
		}
		return values
	}
	url := "jdbc:oracle:thin:@//db.example.test:1521/ORCLPDB1"
	for _, escaped := range []bool{false, true} {
		value := url
		if escaped {
			value = strings.ReplaceAll(value, ":", `\:`)
		}
		path := filepath.Join(work, "legacy.conf")
		if err := os.WriteFile(path, []byte("flyway.url="+value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := probe("legacy", path)["url"]; got != value {
			t.Fatal("pinned legacy parser contract changed")
		}
	}
	passwords := []string{"plain", "colon:=#!$", `literal\n\t\:C:\directory`, " \tleading and trailing \t ", "quote\"and'backslash\\", "Unicode密碼🔒\u00a0\u2003", "${HOME}:${env.USER}", "!{nested ${env.USER}}!{ending}"}
	var controls strings.Builder
	for r := rune(1); r < 32; r++ {
		if r != '\r' && r != '\n' {
			controls.WriteRune(r)
		}
	}
	passwords = append(passwords, controls.String())
	controls.Reset()
	for r := rune(0x7f); r <= 0x9f; r++ {
		if r != '\u0085' {
			controls.WriteRune(r)
		}
	}
	passwords = append(passwords, controls.String())
	for i, password := range passwords {
		req := testRequest("info")
		req.Username = "user:=#!\\ \t用戶"
		req.Password = password
		path := filepath.Join(work, "flyway.toml")
		if err := os.WriteFile(path, []byte(makeConfig(req)), 0600); err != nil {
			t.Fatal(err)
		}
		got := probe("modern", path)
		digest := sha256.Sum256([]byte(password))
		want := map[string]string{
			"url": url, "user": req.Username, "password_sha256": hex.EncodeToString(digest[:]),
			"schemas": "APP", "defaultSchema": "APP", "table": "flyway_schema_history",
			"createSchemas": "false", "cleanDisabled": "true", "validateOnMigrate": "true",
			"placeholderReplacement": "false", "validateMigrationNaming": "true", "baselineOnMigrate": "false", "outOfOrder": "false",
			"callbacks": "0", "ignoreMigrationPatterns": "[*:pending]", "oracle.sqlplus": "false",
		}
		if !reflect.DeepEqual(got, want) {
			var changed []string
			for key, expected := range want {
				if got[key] != expected {
					changed = append(changed, key)
				}
			}
			t.Errorf("actual Flyway loader changed values or policy for synthetic case %d: %v", i, changed)
		}
	}
	// Verify the upstream resolver limitation that motivates fail-closed request
	// validation. The TOML parser accepts these strings but Flyway 13.10.3 keeps
	// the !{...} markers instead of delivering the original credential.
	if cfg.Flyway.Version == "13.10.3" {
		for _, separator := range []rune{'\r', '\n', '\u0085', '\u2028', '\u2029'} {
			req := testRequest("info")
			req.Password = "credential" + string(separator) + "tail"
			if validateRequest(req) == nil {
				t.Fatal("unsupported literal resolver input accepted")
			}
			path := filepath.Join(work, "flyway.toml")
			if err := os.WriteFile(path, []byte(makeConfig(req)), 0600); err != nil {
				t.Fatal(err)
			}
			got := probe("modern", path)
			digest := sha256.Sum256([]byte("!{" + req.Password + "}"))
			if got["password_sha256"] != hex.EncodeToString(digest[:]) {
				t.Fatal("pinned upstream resolver limitation changed; review credential boundary")
			}
		}
	}
}
