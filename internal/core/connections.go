package core

import (
	"context"
	"database/sql"
	"errors"
	"flyway-manager/internal/flyway"
	"flyway-manager/internal/oracle"
	"strings"
	"time"
)

// Test credentials remain only in request memory. Save independently probes
// current metadata before starting the single SQLite registration transaction.
type ConnectionInput struct {
	credentialVersion  int
	Target             TargetInput `json:"target"`
	AccountID          string      `json:"account_id"`
	Username           string      `json:"username"`
	Password           string      `json:"password"`
	TimeoutSeconds     int         `json:"timeout_seconds"`
	Schemas            []string    `json:"schemas"`
	GrantCurrentUser   bool        `json:"grant_current_user"`
	GrantProd          bool        `json:"grant_prod"`
	AcknowledgeUnknown bool        `json:"acknowledge_unknown"`
}
type Connection struct {
	Target         Target           `json:"target"`
	Account        ExecutionAccount `json:"account"`
	Schemas        []Schema         `json:"schemas"`
	TimeoutSeconds int              `json:"timeout_seconds"`
}

var connectionSlots = make(chan struct{}, 4)

func (s *Service) connectionInput(ctx context.Context, a Actor, tid string, in ConnectionInput) (ConnectionInput, error) {
	if e := requireAdmin(a); e != nil {
		return in, e
	}
	if tid == "" {
		tid = "connection-test"
	}
	var e error
	in.Target, e = normalizeTarget(tid, in.Target)
	if e != nil {
		return in, e
	}
	in.Username = strings.ToUpper(strings.TrimSpace(in.Username))
	if !oracleName.MatchString(in.Username) {
		return in, invalid("username", "An unquoted Oracle username is required.")
	}
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = 20
	}
	if in.TimeoutSeconds < 5 || in.TimeoutSeconds > 30 {
		return in, invalid("timeout_seconds", "Connection timeout must be 5–30 seconds.")
	}
	if in.Password == "" && in.AccountID != "" {
		account, e := s.Account(ctx, a, in.AccountID)
		if e != nil {
			return in, e
		}
		if account.TargetID != tid || account.Username != in.Username {
			return in, invalid("password", "Enter a password when changing the Oracle account.")
		}
		var payload []byte
		var revoked bool
		if e = s.db.QueryRowContext(ctx, "SELECT payload,revoked,version FROM credential_secrets WHERE account_id=?", in.AccountID).Scan(&payload, &revoked, &in.credentialVersion); e != nil {
			return in, translate(e)
		}
		if revoked {
			return in, conflict("Credential is revoked; enter a replacement.")
		}
		plain, e := s.vault.Open("credential:"+in.AccountID, payload)
		if e != nil {
			return in, e
		}
		in.Password = string(plain)
		clear(plain)
	}
	if in.Password == "" || len(in.Password) > 4096 {
		return in, invalid("password", "An Oracle password up to 4096 bytes is required.")
	}
	return in, nil
}
func (s *Service) probeConnection(ctx context.Context, in ConnectionInput) (oracle.Result, error) {
	select {
	case connectionSlots <- struct{}{}:
		defer func() { <-connectionSlots }()
	default:
		return oracle.Result{}, &Error{Status: 429, Code: "CONNECTION_BUSY", Message: "Connection tests are busy; retry shortly."}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(in.TimeoutSeconds)*time.Second)
	defer cancel()
	probe := s.oracleProbe
	if probe == nil {
		if s.execution != nil {
			if adapter, ok := s.execution.runner.(interface {
				ProbeOracle(context.Context, oracle.Config) (oracle.Result, error)
			}); ok {
				probe = adapter.ProbeOracle
			}
		}
		if probe == nil {
			return oracle.Result{NetworkState: "unknown", AuthenticationState: "unknown", Schemas: []oracle.SchemaCandidate{}, Warnings: []string{}, ErrorCode: "ORACLE_PROBE_UNAVAILABLE", ErrorMessage: "Configure the approved Flyway Java/JDBC runtime before testing Oracle connections."}, nil
		}
	}
	result, e := probe(ctx, oracle.Config{Host: in.Target.Host, Port: in.Target.Port, Service: in.Target.Service, Transport: in.Target.Transport, Username: in.Username, Password: in.Password, OverallTimeout: time.Duration(in.TimeoutSeconds) * time.Second})
	var pe *oracle.ProbeError
	if errors.As(e, &pe) {
		return result, invalid("connection", pe.Message)
	}
	return result, e
}
func (s *Service) TestConnection(ctx context.Context, t Trace, tid string, in ConnectionInput) (oracle.Result, error) {
	in, e := s.connectionInput(ctx, t.Actor, tid, in)
	if e != nil {
		return oracle.Result{}, e
	}
	result, e := s.probeConnection(ctx, in)
	if e != nil {
		return result, e
	}
	outcome := "failure"
	if result.AuthenticationState == "verified" {
		outcome = "success"
	}
	if e = s.Event(ctx, t, "connection.test", tid, outcome); e != nil {
		return oracle.Result{}, e
	}
	return result, nil
}

func (s *Service) Connection(ctx context.Context, a Actor, tid string, accountIDs ...string) (Connection, error) {
	if e := requireAdmin(a); e != nil {
		return Connection{}, e
	}
	out := Connection{Schemas: []Schema{}, TimeoutSeconds: 20}
	var e error
	out.Target, e = s.Target(ctx, a, tid)
	if e != nil {
		return out, e
	}
	var aid string
	e = s.db.QueryRowContext(ctx, "SELECT account_id,timeout_seconds FROM connection_settings WHERE target_id=?", tid).Scan(&aid, &out.TimeoutSeconds)
	if e == sql.ErrNoRows {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	if len(accountIDs) > 0 && accountIDs[0] != "" {
		aid = accountIDs[0]
	}
	out.Account, e = s.Account(ctx, a, aid)
	if e == nil && out.Account.TargetID != tid {
		return Connection{}, denied()
	}
	if e != nil {
		return out, e
	}
	rows, e := s.db.QueryContext(ctx, "SELECT "+schemaColumns+" FROM target_schemas WHERE id IN (SELECT schema_id FROM connection_allowed_schemas WHERE target_id=? AND account_id=?) ORDER BY name", tid, aid)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		x, e := schemaScan(rows)
		if e != nil {
			return out, e
		}
		out.Schemas = append(out.Schemas, x)
	}
	return out, rows.Err()
}

func (s *Service) SaveConnection(ctx context.Context, t Trace, tid string, in ConnectionInput) (Connection, error) {
	create := tid == ""
	if create {
		tid = "connection-" + id()
	}
	originalPassword := in.Password
	var e error
	in, e = s.connectionInput(ctx, t.Actor, tid, in)
	if e != nil {
		return Connection{}, e
	}
	if e = flyway.ValidateCredential(in.Password); e != nil {
		return Connection{}, invalid("password", e.Error())
	}
	result, e := s.probeConnection(ctx, in)
	if e != nil {
		return Connection{}, e
	}
	// Metadata is validated here, never trusted from a browser test response.
	if result.AuthenticationState != "verified" {
		return Connection{}, conflict("Oracle authentication must succeed before saving.")
	}
	candidates := map[string]oracle.SchemaCandidate{}
	for _, x := range result.Schemas {
		candidates[x.Name] = x
	}
	if in.GrantProd && (!in.GrantCurrentUser || in.Target.Environment != "PROD") {
		return Connection{}, invalid("grant_prod", "PROD authorization requires an explicit current-user grant on a PROD connection.")
	}
	if len(in.Schemas) == 0 && (create || in.AccountID == "") {
		return Connection{}, invalid("schemas", "Select at least one usable schema.")
	}
	selected := map[string]oracle.SchemaCandidate{}
	for _, name := range in.Schemas {
		x, ok := candidates[name]
		if !ok || x.MigrationCapability == "unsupported" || !oracleName.MatchString(name) {
			return Connection{}, invalid("schemas", "A selected schema is unavailable or unsupported.")
		}
		if _, ok := selected[name]; ok {
			return Connection{}, invalid("schemas", "Duplicate schema selection.")
		}
		if x.MigrationCapability == "unknown" && !in.AcknowledgeUnknown {
			return Connection{}, invalid("acknowledge_unknown", "Explicitly acknowledge uncertain Oracle privileges.")
		}
		selected[name] = x
	}
	aid := in.AccountID
	if aid == "" {
		aid = id()
	}
	payload, e := s.vault.Seal("credential:"+aid, []byte(in.Password))
	if e != nil {
		return Connection{}, e
	}
	e = s.mutation(ctx, t, "connection.save", tid, func(tx *sql.Tx) error {
		// Prevent endpoint changes once immutable streams exist. Mapping edits and
		// credential rotation preserve the same physical history identity.
		if create {
			if in.AccountID != "" {
				return invalid("account_id", "New connections require a new account.")
			}
			_, e = tx.ExecContext(ctx, "INSERT INTO database_targets(id,name,environment,host,port,service,transport,active,created_at) VALUES(?,?,?,?,?,?,?,1,?)", tid, in.Target.Name, in.Target.Environment, in.Target.Host, in.Target.Port, in.Target.Service, in.Target.Transport, now())
			if e != nil {
				return e
			}
		} else {
			old, e := targetScan(tx.QueryRowContext(ctx, "SELECT "+targetColumns+" FROM database_targets WHERE id=?", tid))
			if e != nil {
				return e
			}
			if !old.Active {
				return conflict("Reactivate this target before saving its connection.")
			}
			var streams int
			if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM migration_streams WHERE target_id=?", tid).Scan(&streams); e != nil {
				return e
			}
			var otherAccounts int
			if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM execution_accounts WHERE target_id=? AND id<>?", tid, aid).Scan(&otherAccounts); e != nil {
				return e
			}
			if (streams > 0 || otherAccounts > 0) && (old.Host != in.Target.Host || old.Port != in.Target.Port || old.Service != in.Target.Service || old.Transport != in.Target.Transport || old.Environment != in.Target.Environment) {
				return conflict("A connection with migration history or other execution accounts cannot change its destination or environment. Register a new connection.")
			}

			if old.Environment != in.Target.Environment {
				if _, e = tx.ExecContext(ctx, "UPDATE permission_grants SET allow_prod=0 WHERE target_id=?", tid); e != nil {
					return e
				}
			}
			if _, e = tx.ExecContext(ctx, "UPDATE database_targets SET name=?,environment=?,host=?,port=?,service=?,transport=? WHERE id=?", in.Target.Name, in.Target.Environment, in.Target.Host, in.Target.Port, in.Target.Service, in.Target.Transport, tid); e != nil {
				return e
			}
		}
		if in.AccountID == "" {
			if _, e = tx.ExecContext(ctx, "INSERT INTO execution_accounts(id,target_id,name,username,active,created_at) VALUES(?,?,?,?,1,?)", aid, tid, in.Username, in.Username, now()); e != nil {
				return e
			}
		} else {
			var target, username string
			var accountActive bool
			if e = tx.QueryRowContext(ctx, "SELECT target_id,username,active FROM execution_accounts WHERE id=?", aid).Scan(&target, &username, &accountActive); e != nil {
				return e
			}
			if target != tid {
				return denied()
			}
			if !accountActive {
				return conflict("Reactivate the execution account before saving its connection.")
			}
			if username != in.Username {
				return invalid("username", "Preserve existing account identity; add a separate execution account for another username.")
			}
			if originalPassword == "" {
				var version int
				var revoked bool
				if e = tx.QueryRowContext(ctx, "SELECT version,revoked FROM credential_secrets WHERE account_id=?", aid).Scan(&version, &revoked); e != nil {
					return e
				}
				if revoked || version != in.credentialVersion {
					return conflict("The saved credential changed during discovery. Test and save again.")
				}
			}
		}
		// An empty password on edit preserves both ciphertext and version.
		if create || in.AccountID == "" || originalPassword != "" {
			if _, e = tx.ExecContext(ctx, `INSERT INTO credential_secrets(account_id,payload,version,updated_at) VALUES(?,?,1,?) ON CONFLICT(account_id) DO UPDATE SET payload=excluded.payload,version=credential_secrets.version+1,updated_at=excluded.updated_at,revoked=0`, aid, payload, now()); e != nil {
				return e
			}
			if e = audit(ctx, tx, t, "credential.replace", aid, "success"); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO connection_settings(target_id,account_id,timeout_seconds) VALUES(?,?,?) ON CONFLICT(target_id) DO UPDATE SET account_id=excluded.account_id,timeout_seconds=excluded.timeout_seconds`, tid, aid, in.TimeoutSeconds); e != nil {
			return e
		}

		if _, e = tx.ExecContext(ctx, "INSERT INTO connection_managed_accounts(target_id,account_id) VALUES(?,?) ON CONFLICT DO NOTHING", tid, aid); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM connection_allowed_schemas WHERE target_id=? AND account_id=?", tid, aid); e != nil {
			return e
		}
		// Clear only this account's defaults and mappings; other accounts survive.
		if _, e = tx.ExecContext(ctx, "UPDATE target_schemas SET default_account_id=NULL WHERE target_id=? AND default_account_id=?", tid, aid); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM account_schema_grants WHERE account_id=?", aid); e != nil {
			return e
		}
		for name, x := range selected {
			sid := id()
			if _, e = tx.ExecContext(ctx, `INSERT INTO target_schemas(id,target_id,name,history_schema,history_table,active,created_at) VALUES(?,?,?,?,?,1,?) ON CONFLICT(target_id,name) DO NOTHING`, sid, tid, name, name, "flyway_schema_history", now()); e != nil {
				return e
			}
			var schemaActive bool
			if e = tx.QueryRowContext(ctx, "SELECT id,active FROM target_schemas WHERE target_id=? AND name=?", tid, name).Scan(&sid, &schemaActive); e != nil {
				return e
			}
			if !schemaActive {
				return conflict("Reactivate a selected schema before adding it to the connection.")
			}
			if _, e = tx.ExecContext(ctx, "INSERT INTO account_schema_grants(account_id,schema_id,target_id) VALUES(?,?,?)", aid, sid, tid); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, "INSERT INTO connection_allowed_schemas(target_id,account_id,schema_id,capability,acknowledged) VALUES(?,?,?,?,?)", tid, aid, sid, x.MigrationCapability, in.AcknowledgeUnknown); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, "UPDATE target_schemas SET default_account_id=COALESCE(default_account_id,?) WHERE id=?", aid, sid); e != nil {
				return e
			}
			if in.GrantCurrentUser {
				// Explicit, schema-scoped operational authorization; Admin is never an
				// implicit migration grant, and PROD needs a separate affirmative choice.
				if _, e = tx.ExecContext(ctx, `INSERT INTO permission_grants(user_id,target_id,schema_id,can_read,can_operate,allow_prod) VALUES(?,?,?,1,1,?) ON CONFLICT DO UPDATE SET can_read=1,can_operate=1,allow_prod=MAX(permission_grants.allow_prod,excluded.allow_prod)`, t.Actor.ID, tid, sid, in.GrantProd); e != nil {
					return e
				}
			}
		}
		if in.GrantCurrentUser {
			if e = audit(ctx, tx, t, "permissions.wizard_grant", tid, "success"); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return Connection{}, e
	}
	return s.Connection(ctx, t.Actor, tid)
}

// Legacy advanced accounts retain their explicit mappings. Wizard-managed
// accounts additionally require their own independently maintained allowlist.
const allowedAccountSQL = `(NOT EXISTS(SELECT 1 FROM connection_managed_accounts m WHERE m.target_id=g.target_id AND m.account_id=g.account_id) OR EXISTS(SELECT 1 FROM connection_allowed_schemas w WHERE w.target_id=g.target_id AND w.account_id=g.account_id AND w.schema_id=g.schema_id))`

func connectionSchemaAllowed(ctx context.Context, q queryer, tid, sid string) error {
	var rejected int
	if e := q.QueryRowContext(ctx, `SELECT count(*) FROM connection_settings c WHERE c.target_id=? AND NOT EXISTS(SELECT 1 FROM account_schema_grants g JOIN execution_accounts a ON a.id=g.account_id JOIN credential_secrets k ON k.account_id=a.id WHERE g.target_id=c.target_id AND g.schema_id=? AND a.active=1 AND k.revoked=0 AND `+allowedAccountSQL+`)`, tid, sid).Scan(&rejected); e != nil {
		return e
	}
	if rejected > 0 {
		return denied()
	}
	return nil
}
func connectionAccountAllowed(ctx context.Context, q queryer, tid, sid, aid string) error {
	var rejected int
	if e := q.QueryRowContext(ctx, `SELECT count(*) FROM connection_managed_accounts m WHERE m.target_id=? AND m.account_id=? AND NOT EXISTS(SELECT 1 FROM connection_allowed_schemas w WHERE w.target_id=m.target_id AND w.account_id=m.account_id AND w.schema_id=?)`, tid, aid, sid).Scan(&rejected); e != nil {
		return e
	}
	if rejected > 0 {
		return denied()
	}
	return nil
}

func (s *Service) MigrationSchemas(ctx context.Context, a Actor, tid string, pages ...Page) ([]Schema, error) {
	out := []Schema{}
	var all []Schema
	for offset := 0; ; offset += 200 {
		batch, e := s.Schemas(ctx, a, tid, Page{Limit: 200, Offset: offset})
		if e != nil {
			return nil, e
		}
		all = append(all, batch...)
		if len(batch) < 200 {
			break
		}
	}
	for _, x := range all {
		if e := s.authorizeExecution(ctx, s.db, a, tid, x.ID); e != nil {
			if v, ok := e.(*Error); ok && (v.Status == 403 || v.Status == 409) {
				continue
			}
			return nil, e
		}
		var mapped int
		if e := s.db.QueryRowContext(ctx, `SELECT count(*) FROM account_schema_grants g JOIN execution_accounts a ON a.id=g.account_id JOIN credential_secrets c ON c.account_id=a.id WHERE g.target_id=? AND g.schema_id=? AND a.active=1 AND c.revoked=0 AND `+allowedAccountSQL, tid, x.ID).Scan(&mapped); e != nil {
			return nil, e
		}
		if mapped > 0 {
			out = append(out, x)
		}
	}
	return operationPage(out, pages), nil
}
func (s *Service) MigrationConnections(ctx context.Context, a Actor, pages ...Page) ([]Target, error) {
	out := []Target{}
	for offset := 0; ; offset += 200 {
		batch, e := s.Targets(ctx, a, Page{Limit: 200, Offset: offset})
		if e != nil {
			return nil, e
		}
		for _, x := range batch {
			schemas, e := s.MigrationSchemas(ctx, a, x.ID)
			if e != nil {
				return nil, e
			}
			if len(schemas) > 0 {
				out = append(out, x)
			}
		}
		if len(batch) < 200 {
			break
		}
	}
	return operationPage(out, pages), nil
}

// Authorization and account restrictions precede pagination.
func operationPage[T any](out []T, pages []Page) []T {
	if len(pages) == 0 {
		return out
	}
	limit, offset := pages[0].bounds()
	if offset >= len(out) {
		return []T{}
	}
	return out[offset:min(len(out), offset+limit)]
}
