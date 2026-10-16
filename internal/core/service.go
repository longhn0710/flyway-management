package core

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"flyway-manager/internal/database"
	"flyway-manager/internal/oracle"
	"flyway-manager/internal/secrets"
	"golang.org/x/crypto/argon2"
)

type Service struct {
	oracleProbe    func(context.Context, oracle.Config) (oracle.Result, error)
	db             *sql.DB
	vault          secrets.Store
	SetupTokenFile string
	setupHash      string
	passwordSlots  chan struct{}
	limiterMu      sync.Mutex
	limits         map[string]rateEntry
	dummyHash      string
	execution      *Execution
	PublicURL      string
}
type rateEntry struct {
	Count int
	Until int64
}

func New(ctx context.Context, db *database.DB, dataDir, publicURL string) (*Service, error) {
	return NewWithMasterKeyFile(ctx, db, dataDir, publicURL, "")
}

// NewWithMasterKeyFile accepts an optional externally managed master key path.
// An empty path preserves the default key stored under dataDir/secrets.
func NewWithMasterKeyFile(ctx context.Context, db *database.DB, dataDir, publicURL, masterKeyFile string) (*Service, error) {
	absoluteDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	dataDir = absoluteDir
	var check string
	err = db.SQL.QueryRowContext(ctx, "SELECT value FROM installation_metadata WHERE key='encryption_check'").Scan(&check)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var encrypted int
	if err = db.SQL.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM credential_secrets)+(SELECT count(*) FROM auth_settings)+(SELECT count(*) FROM oidc_flows)`).Scan(&encrypted); err != nil {
		return nil, err
	}
	var store *secrets.Local
	if masterKeyFile != "" {
		if err = privateRuntimeDirectory(filepath.Join(dataDir, "secrets")); err != nil {
			return nil, err
		}
		store, err = secrets.NewExternal(masterKeyFile)
	} else {
		store, err = secrets.NewLocal(filepath.Join(dataDir, "secrets", "master.key"), check != "" || encrypted > 0)
	}
	if err != nil {
		return nil, err
	}
	if check != "" {
		payload, e := base64.RawStdEncoding.DecodeString(check)
		if e != nil {
			return nil, errors.New("invalid encryption key check")
		}
		plain, e := store.Open("installation:"+db.InstallationID, payload)
		if e != nil || string(plain) != "flyway-manager-key-check" {
			return nil, errors.New("wrong master key: restore the original master.key")
		}
	}
	if check == "" {
		payload, e := store.Seal("installation:"+db.InstallationID, []byte("flyway-manager-key-check"))
		if e != nil {
			return nil, e
		}
		if _, e = db.SQL.ExecContext(ctx, "INSERT INTO installation_metadata(key,value) VALUES('encryption_check',?)", base64.RawStdEncoding.EncodeToString(payload)); e != nil {
			return nil, e
		}
	}
	// Fail at startup, before serving, if any persisted credential has been tampered with.
	rows, err := db.SQL.QueryContext(ctx, "SELECT account_id,payload FROM credential_secrets")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var account string
		var payload []byte
		if err = rows.Scan(&account, &payload); err != nil {
			break
		}
		var plain []byte
		plain, err = store.Open("credential:"+account, payload)
		clear(plain)
		if err != nil {
			break
		}
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("verify encrypted credentials: %w", err)
	}
	s := &Service{db: db.SQL, vault: store, SetupTokenFile: filepath.Join(dataDir, "secrets", "bootstrap.token"), passwordSlots: make(chan struct{}, 2), limits: map[string]rateEntry{}, PublicURL: publicURL}
	s.dummyHash, err = s.hashPassword(ctx, "dummy-password-for-timing-equalization")
	if err != nil {
		return nil, err
	}
	required, err := s.BootstrapRequired(ctx)
	if err != nil {
		return nil, err
	}
	if required {
		info, e := os.Lstat(s.SetupTokenFile)
		if errors.Is(e, os.ErrNotExist) {
			f, e := os.OpenFile(s.SetupTokenFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return nil, e
			}
			token := random()
			_, e = f.WriteString(token + "\n")
			if e == nil {
				e = f.Sync()
			}
			closeErr := f.Close()
			if e == nil {
				e = closeErr
			}
			if e != nil {
				return nil, e
			}
		} else if e != nil {
			return nil, e
		} else if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("bootstrap token file must be private and regular")
		}
		token, e := os.ReadFile(s.SetupTokenFile)
		if e != nil {
			return nil, e
		}
		s.setupHash = digest(strings.TrimSpace(string(token)))
		clear(token)
	}
	// Validate/decrypt saved provider settings at startup too.
	if _, err = s.Settings(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func privateRuntimeDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("secret directory must be a real directory")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.Chmod(dir, 0700)
}
func (s *Service) BootstrapRequired(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM installation_metadata WHERE key='admin_bootstrapped'").Scan(&n)
	return n == 0, err
}
func (s *Service) RateAllow(key string) bool {
	// Hash attacker-supplied keys so the bounded entry count also bounds memory use.
	key = digest(key)
	s.limiterMu.Lock()
	defer s.limiterMu.Unlock()
	n := now()
	for k, v := range s.limits {
		if v.Until <= n {
			delete(s.limits, k)
		}
	}
	v := s.limits[key]
	if v.Until <= n {
		v = rateEntry{Until: n + 60}
	}
	if len(s.limits) >= 10000 && v.Count == 0 {
		return false
	}
	v.Count++
	s.limits[key] = v
	return v.Count <= 10
}
func (s *Service) hashPassword(ctx context.Context, password string) (string, error) {
	select {
	case s.passwordSlots <- struct{}{}:
		defer func() { <-s.passwordSlots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return "$argon2id$v=19$m=65536,t=3,p=2$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash), nil
}
func (s *Service) passwordMatches(ctx context.Context, password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=3,p=2" {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(parts[4])
	if e != nil || len(salt) != 16 {
		return false
	}
	wanted, e := base64.RawStdEncoding.DecodeString(parts[5])
	if e != nil || len(wanted) != 32 {
		return false
	}
	select {
	case s.passwordSlots <- struct{}{}:
		defer func() { <-s.passwordSlots }()
	case <-ctx.Done():
		return false
	}
	got := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare(got, wanted) == 1
}
func validatePassword(password string) error {
	if len(password) < 12 || len(password) > 1024 {
		return invalid("password", "Password must contain 12 to 1024 bytes.")
	}
	return nil
}
func (s *Service) sealJSON(record string, v any) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	defer clear(b)
	return s.vault.Seal(record, b)
}
func (s *Service) openJSON(record string, payload []byte, v any) error {
	b, e := s.vault.Open(record, payload)
	if e != nil {
		return e
	}
	defer clear(b)
	return json.Unmarshal(b, v)
}
