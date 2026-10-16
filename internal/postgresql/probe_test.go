package postgresql

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func testCA(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestValidatePostgreSQLProfileInputs(t *testing.T) {
	ca := testCA(t)
	valid := Config{Host: "db.example.test", Port: 5432, DatabaseName: "appdb", Username: "app_user", Password: "secret", CAPEM: ca}
	if err := Validate(valid); err != nil {
		t.Fatalf("valid profile rejected: %v", err)
	}
	for name, change := range map[string]func(*Config){
		"uppercase database": func(c *Config) { c.DatabaseName = "Appdb" },
		"quoted identifier":  func(c *Config) { c.Username = "app;select" },
		"missing password":   func(c *Config) { c.Password = "" },
		"non-CA certificate": func(c *Config) { c.CAPEM = "-----BEGIN CERTIFICATE-----\nAA==\n-----END CERTIFICATE-----\n" },
		"extra data":         func(c *Config) { c.CAPEM += "not pem" },
	} {
		t.Run(name, func(t *testing.T) {
			c := valid
			change(&c)
			if err := Validate(c); err == nil {
				t.Fatal("invalid PostgreSQL profile accepted")
			}
		})
	}
}

func TestResultFromMetadataChecksIdentityVersionAndClassifiesSchemas(t *testing.T) {
	cfg := Config{DatabaseName: "appdb", Username: "app_user"}
	result, err := ResultFromMetadata(cfg, Metadata{DatabaseName: "appdb", SessionUser: "app_user", CurrentUser: "app_user", ServerVersion: "170005", Schemas: []SchemaMetadata{{Name: "app", Owner: "app_user", Create: true}, {Name: "readonly", Owner: "db_owner", Create: false}, {Name: "pg_catalog", Owner: "app_user", Create: true}, {Name: "BadName", Owner: "app_user", Create: true}}})
	if err != nil || result.AuthenticationState != "verified" || result.DatabaseName != "appdb" || result.ServerVersion != "170005" || len(result.Schemas) != 2 {
		t.Fatalf("unexpected successful result: %#v %v", result, err)
	}
	if result.Schemas[0].MigrationCapability != "potential" || result.Schemas[1].MigrationCapability != "unknown" || !result.Schemas[0].Own || result.Schemas[1].Own || result.ActualUser != "app_user" {
		t.Fatalf("schema privileges misclassified: %#v", result.Schemas)
	}
	result, err = ResultFromMetadata(cfg, Metadata{DatabaseName: "other", SessionUser: "app_user", ServerVersion: "170005"})
	if err != nil || result.AuthenticationState != "rejected" || result.ErrorCode != "POSTGRESQL_IDENTITY_MISMATCH" {
		t.Fatalf("database identity mismatch accepted: %#v %v", result, err)
	}
	result, err = ResultFromMetadata(cfg, Metadata{DatabaseName: "appdb", SessionUser: "other_user", CurrentUser: "other_user", ServerVersion: "170005"})
	if err != nil || result.AuthenticationState != "rejected" || result.ErrorCode != "POSTGRESQL_IDENTITY_MISMATCH" {
		t.Fatalf("session role identity mismatch accepted: %#v %v", result, err)
	}
	result, err = ResultFromMetadata(cfg, Metadata{DatabaseName: "appdb", SessionUser: "app_user", CurrentUser: "app_user", ServerVersion: "160009"})
	if err != nil || result.ErrorCode != "POSTGRESQL_VERSION_UNSUPPORTED" || len(result.Schemas) != 0 {
		t.Fatalf("non-17 server accepted: %#v %v", result, err)
	}
}

func TestBoundedPostgreSQLProbeTimeout(t *testing.T) {
	if got := BoundedTimeout(Config{}); got != defaultOverallTimeout {
		t.Fatalf("default timeout=%s", got)
	}
	if got := BoundedTimeout(Config{OverallTimeout: time.Hour}); got != maxOverallTimeout {
		t.Fatalf("bounded timeout=%s", got)
	}
}
