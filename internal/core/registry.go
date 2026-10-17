package core

import (
	"context"
	"database/sql"
	"net"
	"regexp"
	"strings"
)

type Target struct {
	Engine          string `json:"engine,omitempty"`
	DatabaseName    string `json:"database_name,omitempty"`
	ProfileRevision int    `json:"profile_revision,omitempty"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	Environment     string `json:"environment"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Service         string `json:"service"`
	Transport       string `json:"transport"`
	Active          bool   `json:"active"`
	CreatedAt       string `json:"created_at"`
}
type TargetInput struct {
	Engine       string             `json:"engine"`
	DatabaseName string             `json:"database_name"`
	Profile      *ConnectionProfile `json:"profile,omitempty"`
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Environment  string             `json:"environment"`
	Host         string             `json:"host"`
	Port         int                `json:"port"`
	Service      string             `json:"service"`
	Transport    string             `json:"transport"`
	Active       bool               `json:"active"`
}

const targetColumns = `id,name,environment,host,port,service,transport,active,created_at,engine,database_name,profile_revision`

func targetScan(row interface{ Scan(...any) error }) (Target, error) {
	var x Target
	var c int64
	e := row.Scan(&x.ID, &x.Name, &x.Environment, &x.Host, &x.Port, &x.Service, &x.Transport, &x.Active, &c, &x.Engine, &x.DatabaseName, &x.ProfileRevision)
	if x.Engine == "oracle" {
		x.Engine = ""
		x.ProfileRevision = 0
	}
	x.CreatedAt = stamp(c)
	return x, translate(e)
}
func visibleTarget(a Actor, target string, grants []Grant) bool {
	if a.Admin() {
		return true
	}
	for _, g := range grants {
		if g.TargetID == target && g.Read {
			return true
		}
	}
	return false
}
func (s *Service) Target(ctx context.Context, a Actor, tid string) (Target, error) {
	x, e := targetScan(s.db.QueryRowContext(ctx, "SELECT "+targetColumns+" FROM database_targets WHERE id=?", tid))
	if e != nil {
		return x, e
	}
	gs, e := s.Grants(ctx, a.Kind, a.ID)
	if e != nil {
		return Target{}, e
	}
	if !visibleTarget(a, tid, gs) || !x.Active && !a.Admin() {
		return Target{}, denied()
	}
	return x, nil
}
func (s *Service) Targets(ctx context.Context, a Actor, p Page) ([]Target, error) {
	gs, e := s.Grants(ctx, a.Kind, a.ID)
	if e != nil {
		return nil, e
	}
	// Apply authorization before pagination: scoped users never page through hidden resources.
	rows, e := s.db.QueryContext(ctx, "SELECT "+targetColumns+" FROM database_targets WHERE (name LIKE ? OR host LIKE ? OR service LIKE ? OR database_name LIKE ? OR id LIKE ?) AND (?='' OR environment=?) ORDER BY name,id", "%"+p.Query+"%", "%"+p.Query+"%", "%"+p.Query+"%", "%"+p.Query+"%", "%"+p.Query+"%", p.Environment, p.Environment)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Target{}
	l, o := p.bounds()
	seen := 0
	for rows.Next() {
		x, e := targetScan(rows)
		if e != nil {
			return nil, e
		}
		if !visibleTarget(a, x.ID, gs) || !x.Active && !a.Admin() {
			continue
		}
		if seen >= o && len(out) < l {
			out = append(out, x)
		}
		seen++
	}
	return out, rows.Err()
}

var hostname = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)
var serviceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func (s *Service) SaveTarget(ctx context.Context, t Trace, tid string, in TargetInput) (Target, error) {
	if e := requireAdmin(t.Actor); e != nil {
		return Target{}, e
	}
	create := tid == ""
	if create {
		tid = in.ID
		in.Active = true
	}
	var err error
	in, err = normalizeTarget(tid, in)
	if err != nil {
		return Target{}, err
	}
	action := "target.update"
	if create {
		action = "target.create"
	}
	e := s.mutation(ctx, t, action, tid, func(tx *sql.Tx) error {
		if create {
			if in.Engine == "postgresql" {
				return invalid("engine", "Use connection discovery to register PostgreSQL atomically.")
			}
			_, e := tx.ExecContext(ctx, "INSERT INTO database_targets(id,name,environment,host,port,service,transport,active,created_at) VALUES(?,?,?,?,?,?,?,1,?)", tid, in.Name, in.Environment, in.Host, in.Port, in.Service, in.Transport, now())
			return e
		}
		var old Target
		var e error
		old, e = targetScan(tx.QueryRowContext(ctx, "SELECT "+targetColumns+" FROM database_targets WHERE id=?", tid))
		if e != nil {
			return e
		}
		if engineOf(old.Engine) != in.Engine {
			return invalid("engine", "Engine is immutable.")
		}
		if in.Engine == "postgresql" && (old.Host != in.Host || old.Port != in.Port || old.DatabaseName != in.DatabaseName || old.Environment != in.Environment) {
			return conflict("Use the connection wizard to revise a PostgreSQL profile.")
		}
		// An environment change is a new security decision: revoke inherited operational PROD grants.
		if old.Environment != in.Environment {
			if _, e = tx.ExecContext(ctx, "UPDATE permission_grants SET allow_prod=0 WHERE target_id=?", tid); e != nil {
				return e
			}
			if e = audit(ctx, tx, t, "permissions.prod_reset", tid, "success"); e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, "UPDATE database_targets SET name=?,environment=?,host=?,port=?,service=?,transport=?,active=? WHERE id=?", in.Name, in.Environment, in.Host, in.Port, in.Service, in.Transport, in.Active, tid)
		return e
	})
	if e != nil {
		return Target{}, e
	}
	return s.Target(ctx, t.Actor, tid)
}

type Schema struct {
	ID               string `json:"id"`
	TargetID         string `json:"target_id"`
	Name             string `json:"name"`
	HistorySchema    string `json:"history_schema"`
	HistoryTable     string `json:"history_table"`
	DefaultAccountID string `json:"default_account_id"`
	Active           bool   `json:"active"`
	CreatedAt        string `json:"created_at"`
}
type SchemaInput struct {
	Name             string `json:"name"`
	HistorySchema    string `json:"history_schema"`
	HistoryTable     string `json:"history_table"`
	DefaultAccountID string `json:"default_account_id"`
	Active           bool   `json:"active"`
}

const schemaColumns = `id,target_id,name,history_schema,history_table,IFNULL(default_account_id,''),active,created_at`

func schemaScan(row interface{ Scan(...any) error }) (Schema, error) {
	var x Schema
	var c int64
	e := row.Scan(&x.ID, &x.TargetID, &x.Name, &x.HistorySchema, &x.HistoryTable, &x.DefaultAccountID, &x.Active, &c)
	x.CreatedAt = stamp(c)
	return x, translate(e)
}
func (s *Service) Schema(ctx context.Context, a Actor, tid, sid string) (Schema, error) {
	if e := s.Authorize(ctx, a, "read", tid, sid); e != nil {
		return Schema{}, e
	}
	return schemaScan(s.db.QueryRowContext(ctx, "SELECT "+schemaColumns+" FROM target_schemas WHERE id=? AND target_id=?", sid, tid))
}
func (s *Service) Schemas(ctx context.Context, a Actor, tid string, p Page) ([]Schema, error) {
	target, e := s.Target(ctx, a, tid)
	if e != nil {
		return nil, e
	}
	gs, e := s.Grants(ctx, a.Kind, a.ID)
	if e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, "SELECT "+schemaColumns+" FROM target_schemas WHERE target_id=? AND name LIKE ? ORDER BY name,id", tid, "%"+p.Query+"%")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Schema{}
	l, o := p.bounds()
	seen := 0
	for rows.Next() {
		x, e := schemaScan(rows)
		if e != nil {
			return nil, e
		}
		if !Allowed(a, "read", tid, x.ID, target.Environment, gs) || !x.Active && !a.Admin() {
			continue
		}
		if seen >= o && len(out) < l {
			out = append(out, x)
		}
		seen++
	}
	return out, rows.Err()
}
func (s *Service) SaveSchema(ctx context.Context, t Trace, tid, sid string, in SchemaInput) (Schema, error) {
	if e := requireAdmin(t.Actor); e != nil {
		return Schema{}, e
	}
	create := sid == ""
	if create {
		sid = id()
		in.Active = true
	}
	target, err := s.Target(ctx, t.Actor, tid)
	if err != nil {
		return Schema{}, err
	}
	in.Name = normalizeDBName(target.Engine, in.Name)
	in.HistorySchema = normalizeDBName(target.Engine, in.HistorySchema)
	in.HistoryTable = strings.TrimSpace(in.HistoryTable)
	if in.HistorySchema == "" {
		in.HistorySchema = in.Name
	}
	if in.HistoryTable == "" {
		in.HistoryTable = "flyway_schema_history"
	}
	if !validDBName(target.Engine, in.Name) || !validDBName(target.Engine, in.HistorySchema) || !validDBName(target.Engine, in.HistoryTable) {
		return Schema{}, invalid("schema", "Use unquoted identifiers supported by the selected engine for schema and history names.")
	}
	action := "schema.update"
	if create {
		action = "schema.create"
	}
	e := s.mutation(ctx, t, action, sid, func(tx *sql.Tx) error {
		var defaultID any
		if in.DefaultAccountID != "" {
			var n int
			if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM account_schema_grants g JOIN execution_accounts a ON a.id=g.account_id WHERE g.target_id=? AND g.schema_id=? AND g.account_id=? AND a.active=1`, tid, sid, in.DefaultAccountID).Scan(&n); e != nil {
				return e
			}
			if n != 1 {
				return invalid("default_account_id", "Default account must be active and explicitly mapped to this schema.")
			}
			defaultID = in.DefaultAccountID
		}
		if create {
			_, e := tx.ExecContext(ctx, "INSERT INTO target_schemas(id,target_id,name,history_schema,history_table,default_account_id,active,created_at) VALUES(?,?,?,?,?,?,1,?)", sid, tid, in.Name, in.HistorySchema, in.HistoryTable, defaultID, now())
			return e
		}
		res, e := tx.ExecContext(ctx, "UPDATE target_schemas SET name=?,history_schema=?,history_table=?,default_account_id=?,active=? WHERE id=? AND target_id=?", in.Name, in.HistorySchema, in.HistoryTable, defaultID, in.Active, sid, tid)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return missing()
		}
		return nil
	})
	if e != nil {
		return Schema{}, e
	}
	return s.Schema(ctx, t.Actor, tid, sid)
}

type CredentialMetadata struct {
	Configured bool   `json:"configured"`
	Version    int    `json:"version"`
	UpdatedAt  string `json:"updated_at"`
	Revoked    bool   `json:"revoked"`
}
type ExecutionAccount struct {
	ID         string             `json:"id"`
	TargetID   string             `json:"target_id"`
	Name       string             `json:"name"`
	Username   string             `json:"username"`
	Active     bool               `json:"active"`
	SchemaIDs  []string           `json:"schema_ids"`
	Credential CredentialMetadata `json:"credential"`
	CreatedAt  string             `json:"created_at"`
}
type AccountInput struct {
	TargetID  string   `json:"target_id"`
	Name      string   `json:"name"`
	Username  string   `json:"username"`
	Active    bool     `json:"active"`
	SchemaIDs []string `json:"schema_ids"`
	Password  string   `json:"password"`
}

func accountScan(row interface{ Scan(...any) error }) (ExecutionAccount, error) {
	var x ExecutionAccount
	var c int64
	e := row.Scan(&x.ID, &x.TargetID, &x.Name, &x.Username, &x.Active, &c)
	x.CreatedAt = stamp(c)
	x.SchemaIDs = []string{}
	return x, translate(e)
}
func (s *Service) Account(ctx context.Context, a Actor, aid string) (ExecutionAccount, error) {
	if e := requireAdmin(a); e != nil {
		return ExecutionAccount{}, e
	}
	x, e := accountScan(s.db.QueryRowContext(ctx, "SELECT id,target_id,name,username,active,created_at FROM execution_accounts WHERE id=?", aid))
	if e != nil {
		return x, e
	}
	rows, e := s.db.QueryContext(ctx, "SELECT schema_id FROM account_schema_grants WHERE account_id=? ORDER BY schema_id", aid)
	if e != nil {
		return x, e
	}
	for rows.Next() {
		var sid string
		if e = rows.Scan(&sid); e != nil {
			break
		}
		x.SchemaIDs = append(x.SchemaIDs, sid)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return x, e
	}
	var u int64
	e = s.db.QueryRowContext(ctx, "SELECT version,updated_at,revoked FROM credential_secrets WHERE account_id=?", aid).Scan(&x.Credential.Version, &u, &x.Credential.Revoked)
	if e == sql.ErrNoRows {
		return x, nil
	}
	if e != nil {
		return x, e
	}
	x.Credential.Configured = !x.Credential.Revoked
	x.Credential.UpdatedAt = stamp(u)
	return x, nil
}
func (s *Service) Accounts(ctx context.Context, a Actor, targetID string, p Page) ([]ExecutionAccount, error) {
	if e := requireAdmin(a); e != nil {
		return nil, e
	}
	l, o := p.bounds()
	rows, e := s.db.QueryContext(ctx, "SELECT id FROM execution_accounts WHERE (?='' OR target_id=?) AND (name LIKE ? OR username LIKE ?) ORDER BY name,id LIMIT ? OFFSET ?", targetID, targetID, "%"+p.Query+"%", "%"+p.Query+"%", l, o)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var aid string
		if e = rows.Scan(&aid); e != nil {
			break
		}
		ids = append(ids, aid)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []ExecutionAccount{}
	for _, aid := range ids {
		x, e := s.Account(ctx, a, aid)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, nil
}
func (s *Service) SaveAccount(ctx context.Context, t Trace, aid string, in AccountInput) (ExecutionAccount, error) {
	if e := requireAdmin(t.Actor); e != nil {
		return ExecutionAccount{}, e
	}
	create := aid == ""
	if create {
		aid = id()
		in.Active = true
	} else if in.Password != "" {
		return ExecutionAccount{}, invalid("password", "Use the write-only credential route to replace a credential.")
	}
	targetID := in.TargetID
	if !create {
		if err := s.db.QueryRowContext(ctx, "SELECT target_id FROM execution_accounts WHERE id=?", aid).Scan(&targetID); err != nil {
			return ExecutionAccount{}, translate(err)
		}
	}
	target, err := s.Target(ctx, t.Actor, targetID)
	if err != nil {
		return ExecutionAccount{}, err
	}
	in.Username = normalizeDBName(target.Engine, in.Username)
	in.Name = strings.TrimSpace(in.Name)
	if !validDBName(target.Engine, in.Username) || !validText(in.Name, 200) || len(in.SchemaIDs) > 1000 {
		return ExecutionAccount{}, invalid("account", "A name, engine-supported unquoted username and valid schema mappings are required.")
	}
	if len(in.Password) > 4096 {
		return ExecutionAccount{}, invalid("password", "Credential is too long.")
	}
	var payload []byte
	var e error
	if in.Password != "" {
		payload, e = s.vault.Seal("credential:"+aid, []byte(in.Password))
		if e != nil {
			return ExecutionAccount{}, e
		}
	}
	action := "execution_account.update"
	if create {
		action = "execution_account.create"
	}
	e = s.mutation(ctx, t, action, aid, func(tx *sql.Tx) error {
		target := in.TargetID
		if create {
			if _, e := tx.ExecContext(ctx, "INSERT INTO execution_accounts(id,target_id,name,username,active,created_at) VALUES(?,?,?,?,1,?)", aid, target, in.Name, in.Username, now()); e != nil {
				return e
			}
		} else {
			if e := tx.QueryRowContext(ctx, "SELECT target_id FROM execution_accounts WHERE id=?", aid).Scan(&target); e != nil {
				return e
			}
			if in.TargetID != "" && in.TargetID != target {
				return invalid("target_id", "Execution accounts cannot move between targets.")
			}
			if _, e := tx.ExecContext(ctx, "UPDATE execution_accounts SET name=?,username=?,active=? WHERE id=?", in.Name, in.Username, in.Active, aid); e != nil {
				return e
			}
		}
		rows, e := tx.QueryContext(ctx, "SELECT id FROM target_schemas WHERE default_account_id=?", aid)
		if e != nil {
			return e
		}
		defaults := []string{}
		for rows.Next() {
			var sid string
			if e = rows.Scan(&sid); e != nil {
				break
			}
			defaults = append(defaults, sid)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return e
		}
		set := map[string]bool{}
		for _, sid := range in.SchemaIDs {
			if set[sid] {
				return invalid("schema_ids", "Duplicate schema mapping.")
			}
			set[sid] = true
		}
		for _, sid := range defaults {
			if !in.Active || !set[sid] {
				return conflict("Clear schema default-account references before disabling or removing their mapping.")
			}
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM account_schema_grants WHERE account_id=?", aid); e != nil {
			return e
		}
		for _, sid := range in.SchemaIDs {
			if _, e = tx.ExecContext(ctx, "INSERT INTO account_schema_grants(account_id,schema_id,target_id) VALUES(?,?,?)", aid, sid, target); e != nil {
				return e
			}
		}
		if payload != nil {
			if _, e = tx.ExecContext(ctx, "INSERT INTO credential_secrets(account_id,payload,version,updated_at) VALUES(?,?,1,?)", aid, payload, now()); e != nil {
				return e
			}
			if e = audit(ctx, tx, t, "credential.create", aid, "success"); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return ExecutionAccount{}, e
	}
	return s.Account(ctx, t.Actor, aid)
}
func (s *Service) ReplaceCredential(ctx context.Context, t Trace, aid, password string, revoke bool) (CredentialMetadata, error) {
	if e := requireAdmin(t.Actor); e != nil {
		return CredentialMetadata{}, e
	}
	if !revoke && (password == "" || len(password) > 4096) {
		return CredentialMetadata{}, invalid("password", "A nonempty credential up to 4096 bytes is required.")
	}
	if revoke {
		password = ""
	}
	payload, e := s.vault.Seal("credential:"+aid, []byte(password))
	if e != nil {
		return CredentialMetadata{}, e
	}
	action := "credential.replace"
	if revoke {
		action = "credential.revoke"
	}
	e = s.mutation(ctx, t, action, aid, func(tx *sql.Tx) error {
		var n int
		if e := tx.QueryRowContext(ctx, "SELECT 1 FROM execution_accounts WHERE id=?", aid).Scan(&n); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO credential_secrets(account_id,payload,version,updated_at,revoked) VALUES(?,?,1,?,?) ON CONFLICT(account_id) DO UPDATE SET payload=excluded.payload,version=credential_secrets.version+1,updated_at=excluded.updated_at,revoked=excluded.revoked`, aid, payload, now(), revoke)
		return e
	})
	if e != nil {
		return CredentialMetadata{}, e
	}
	account, e := s.Account(ctx, t.Actor, aid)
	return account.Credential, e
}

func normalizeTarget(tid string, in TargetInput) (TargetInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(in.Host)), ".")
	if ip := net.ParseIP(in.Host); ip != nil {
		in.Host = ip.String()
	}
	in.Engine = engineOf(in.Engine)
	if in.Engine != "oracle" && in.Engine != "postgresql" {
		return in, invalid("engine", "Engine is not implemented.")
	}
	if in.Engine == "oracle" {
		if in.DatabaseName != "" || in.Profile != nil {
			return in, invalid("profile", "Oracle uses its existing service and transport.")
		}
		in.Service = strings.ToUpper(strings.TrimSpace(in.Service))
	} else {
		if in.Service != "" || !validDBName(in.Engine, in.DatabaseName) || in.Transport != "TCPS" {
			return in, invalid("database_name", "PostgreSQL requires an explicit lowercase database, empty service and verified TLS.")
		}
	}
	in.Environment = strings.ToUpper(in.Environment)
	if !identifier.MatchString(tid) || !validText(in.Name, 200) || (!hostname.MatchString(in.Host) && net.ParseIP(in.Host) == nil) || (in.Engine == "oracle" && !serviceName.MatchString(in.Service)) || in.Port < 1 || in.Port > 65535 {
		return in, invalid("target", "Valid stable ID, name, host, port (1–65535) and Oracle service/PDB are required.")
	}
	if in.Environment != "DEV" && in.Environment != "SIT" && in.Environment != "UAT" && in.Environment != "PROD" {
		return in, invalid("environment", "Environment must be DEV, SIT, UAT or PROD.")
	}
	if in.Transport != "TCP" && in.Transport != "TCPS" {
		return in, invalid("transport", "Transport must be TCP or TCPS.")
	}
	return in, nil
}
