package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type nativeProcess struct {
	cmd     *exec.Cmd
	done    chan error
	logs    chan string
	baseURL string
}

func launchNative(t *testing.T, dir string, expectReady bool, configs ...string) *nativeProcess {
	t.Helper()
	binary, e := filepath.Abs("../../bin/flyway-manager")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(binary); os.IsNotExist(e) {
		t.Skip("native integration requires make build")
	}
	cmd := exec.Command(binary)
	env := []string{}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "FLYWAY_MANAGER_") {
			env = append(env, value)
		}
	}
	cmd.Env = append(env, "FLYWAY_MANAGER_DATA_DIR="+dir, "FLYWAY_MANAGER_LISTEN_ADDRESS=127.0.0.1:0")
	if len(configs) > 0 {
		cmd.Env = append(cmd.Env, "FLYWAY_MANAGER_CONFIG="+configs[0])
	}
	pipe, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	cmd.Stderr = cmd.Stdout
	p := &nativeProcess{cmd: cmd, done: make(chan error, 1), logs: make(chan string, 1)}
	ready := make(chan string, 1)
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	go func() {
		var log bytes.Buffer
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			line := scanner.Bytes()
			log.Write(line)
			log.WriteByte('\n')
			var entry map[string]any
			if json.Unmarshal(line, &entry) == nil && entry["msg"] == "server listening" {
				ready <- "http://" + fmt.Sprint(entry["address"])
			}
		}
		p.done <- cmd.Wait()
		p.logs <- log.String()
	}()
	if !expectReady {
		return p
	}
	select {
	case p.baseURL = <-ready:
		return p
	case e := <-p.done:
		t.Fatalf("native startup failed: %v, %s", e, <-p.logs)
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("native startup timed out")
	}
	return nil
}
func (p *nativeProcess) stop(t *testing.T) string {
	t.Helper()
	if e := p.cmd.Process.Signal(syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-p.done:
		if e != nil {
			t.Fatal(e)
		}
		return <-p.logs
	case <-time.After(15 * time.Second):
		_ = p.cmd.Process.Kill()
		t.Fatal("native shutdown timed out")
	}
	return ""
}
func nativeCall(t *testing.T, client *http.Client, base, method, path, csrf string, body any, want int) json.RawMessage {
	t.Helper()
	var data io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
		data = bytes.NewReader(b)
	}
	req, e := http.NewRequest(method, base+"/api/v1"+path, data)
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", base)
	req.Header.Set("X-CSRF-Token", csrf)
	res, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(res.Body)
	if e != nil {
		t.Fatal(e)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s: %d, %s", method, path, res.StatusCode, raw)
	}
	var envelope struct{ Data json.RawMessage }
	if e = json.Unmarshal(raw, &envelope); e != nil {
		t.Fatal(e)
	}
	return envelope.Data
}
func TestNativeRestartPreservesIdentityRegistryAndEncryptedCredentials(t *testing.T) {
	if testing.Short() {
		t.Skip("native process integration")
	}
	dir := t.TempDir()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	p := launchNative(t, dir, true)
	defer func() {
		if p != nil {
			_ = p.cmd.Process.Kill()
		}
	}()
	health := nativeCall(t, client, p.baseURL, "GET", "/health", "", nil, 200)
	var before map[string]any
	json.Unmarshal(health, &before)
	setup, e := os.ReadFile(filepath.Join(dir, "secrets", "bootstrap.token"))
	if e != nil {
		t.Fatal(e)
	}
	var identity struct {
		User struct{ ID string }
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(nativeCall(t, client, p.baseURL, "POST", "/auth/bootstrap", "", map[string]string{"username": "native-admin", "password": "native-password-2026", "setup_token": strings.TrimSpace(string(setup))}, 201), &identity)
	nativeCall(t, client, p.baseURL, "POST", "/database-targets", identity.CSRF, map[string]any{"id": "native-target", "name": "Oracle Native", "environment": "DEV", "host": "native.example", "port": 1521, "service": "APP", "transport": "TCP"}, 201)
	var schema struct{ ID string }
	json.Unmarshal(nativeCall(t, client, p.baseURL, "POST", "/database-targets/native-target/schemas", identity.CSRF, map[string]string{"name": "CORE_BANKING"}, 201), &schema)
	var account struct{ ID string }
	json.Unmarshal(nativeCall(t, client, p.baseURL, "POST", "/execution-accounts", identity.CSRF, map[string]any{"target_id": "native-target", "name": "native-deploy", "username": "DEPLOY_ADMIN", "schema_ids": []string{schema.ID}, "password": "native-oracle-password-2026"}, 201), &account)
	nativeCall(t, client, p.baseURL, "POST", "/migration-runs", identity.CSRF, map[string]string{}, 400)
	logs := p.stop(t)
	p = nil
	for _, value := range []string{"native-password-2026", "native-oracle-password-2026", strings.TrimSpace(string(setup))} {
		if strings.Contains(logs, value) {
			t.Fatal("secret leaked into native logs")
		}
	}
	keyPath := filepath.Join(dir, "secrets", "master.key")
	key, e := os.ReadFile(keyPath)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"manager.db", "secrets/master.key", "secrets/bootstrap.token"} {
		info, e := os.Stat(filepath.Join(dir, name))
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("nonprivate file %s: %v", name, e)
		}
	}
	p = launchNative(t, dir, true)
	var after map[string]any
	json.Unmarshal(nativeCall(t, client, p.baseURL, "GET", "/health", "", nil, 200), &after)
	if before["installation_id"] != after["installation_id"] {
		t.Fatal("installation replaced after restart")
	}
	var restored struct {
		User struct{ ID string }
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(nativeCall(t, client, p.baseURL, "GET", "/auth/me", "", nil, 200), &restored)
	if restored.User.ID != identity.User.ID || restored.CSRF != identity.CSRF {
		t.Fatal("server session failed after restart")
	}
	raw := nativeCall(t, client, p.baseURL, "GET", "/execution-accounts/"+account.ID, "", nil, 200)
	if !bytes.Contains(raw, []byte(`"configured":true`)) || bytes.Contains(raw, []byte("native-oracle-password")) {
		t.Fatal("credential metadata invalid after restart")
	}
	// Startup verified decryptability of every encrypted credential before opening its listener.
	p.stop(t)
	p = nil
	for _, missing := range []bool{false, true} {
		if missing {
			if e = os.Remove(keyPath); e != nil {
				t.Fatal(e)
			}
		} else {
			if e = os.WriteFile(keyPath, make([]byte, 32), 0600); e != nil {
				t.Fatal(e)
			}
		}
		failed := launchNative(t, dir, false)
		select {
		case e = <-failed.done:
			if e == nil {
				t.Fatal("invalid encryption key accepted")
			}
			msg := <-failed.logs
			if !strings.Contains(msg, "master key") {
				t.Fatalf("wrong startup failure: %s", msg)
			}
		case <-time.After(15 * time.Second):
			_ = failed.cmd.Process.Kill()
			t.Fatal("invalid key did not stop startup")
		}
		if missing {
			if _, e = os.Stat(keyPath); !os.IsNotExist(e) {
				t.Fatal("missing key regenerated")
			}
		}
	}
	if e = os.WriteFile(keyPath, key, 0600); e != nil {
		t.Fatal(e)
	}
	p = launchNative(t, dir, true)
	nativeCall(t, client, p.baseURL, "GET", "/execution-accounts/"+account.ID, "", nil, 200)
	p.stop(t)
	p = nil
}
