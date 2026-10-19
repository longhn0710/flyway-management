package flyway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const stubOracleDriver = `
import java.lang.reflect.*; import java.sql.*; import java.util.*; import java.util.logging.Logger;
public class StubOracleDriver implements Driver {
 static { try { DriverManager.registerDriver(new StubOracleDriver()); } catch(Exception e) { throw new RuntimeException(e); } }
 public boolean acceptsURL(String u){return u.startsWith("jdbc:oracle:thin:@");}
 public Connection connect(String u,Properties p)throws SQLException{ if(!acceptsURL(u))return null; if(!"true".equals(p.getProperty("oracle.net.ssl_server_dn_match")))throw new SQLException("SSL hostname verification missing"); if(u.contains("PROTOCOL=TCPS")&&!u.contains("SSL_SERVER_DN_MATCH=YES"))throw new SQLException("TCPS descriptor hostname verification missing"); return (Connection)Proxy.newProxyInstance(getClass().getClassLoader(),new Class[]{Connection.class},(o,m,a)->{
  if(m.getName().equals("createStatement")) return statement();
  if(m.getName().equals("close")||m.getName().equals("rollback")||m.getName().equals("setAutoCommit"))return null;
  if(m.getName().equals("isClosed"))return false; return def(m.getReturnType()); }); }
 static Object statement(){return Proxy.newProxyInstance(StubOracleDriver.class.getClassLoader(),new Class[]{Statement.class},(o,m,a)->{
  if(m.getName().equals("execute")) { if(!a[0].equals("SET TRANSACTION READ ONLY"))throw new SQLException("not read only", "", 900); return false; }
  if(m.getName().equals("executeQuery")){String q=(String)a[0];
   if(q.contains("V$DATABASE"))return rs(new Object[][]{{"12345","ORCL","FREEPDB1","FREEPDB1",System.getProperty("stub.user","APP"),"APP"}});
   if(q.contains("FROM ALL_OBJECTS"))return rs(new Object[][]{});
   if(q.contains("FROM ALL_TABLES")){if(!q.contains("TABLE_NAME='flyway_schema_history'"))throw new SQLException("table name not preserved","",900);return rs(new Object[][]{{Boolean.getBoolean("stub.present")?1:0}});}
   if(q.startsWith("SELECT \"installed_rank\", \"version\", \"description\", \"type\", \"script\", \"checksum\", \"installed_by\", TO_CHAR(\"installed_on\",'YYYY-MM-DD\"T\"HH24:MI:SS.FF9'")){
    if(!q.contains("FROM \"APP\".\"flyway_schema_history\" ORDER BY \"installed_rank\""))throw new SQLException("quoted Flyway identifiers missing","",900);
    return rs(new Object[][]{{1,"1","first","SQL","V1__first.sql",null,"APP","2026-06-07T08:09:10.123456789",4,"1"}}); }
   throw new SQLException("unexpected query: "+q,"",900);
  }
  if(m.getName().equals("close")||m.getName().startsWith("set"))return null;return def(m.getReturnType()); }); }
 static Object rs(Object[][] rows){final int[] i={-1};return Proxy.newProxyInstance(StubOracleDriver.class.getClassLoader(),new Class[]{ResultSet.class},(o,m,a)->{
  String n=m.getName();if(n.equals("next"))return ++i[0]<rows.length;if(n.equals("close"))return null;
  if(n.equals("getString")||n.equals("getInt")){Object v=rows[i[0]][((Integer)a[0])-1];if(n.equals("getInt"))return v==null?0:Integer.parseInt(v.toString());return v==null?null:v.toString();}
  if(n.equals("wasNull"))return false;return def(m.getReturnType());}); }
 static Object def(Class<?> c){if(!c.isPrimitive())return null;if(c==boolean.class)return false;if(c==int.class)return 0;if(c==long.class)return 0L;return 0;}
 public DriverPropertyInfo[] getPropertyInfo(String u,Properties p){return new DriverPropertyInfo[0];} public int getMajorVersion(){return 1;}public int getMinorVersion(){return 0;}public boolean jdbcCompliant(){return false;}public Logger getParentLogger(){return Logger.getGlobal();}
}`

func TestOracleEvidenceProtocolParsesNullableChecksumAndAbsentHistory(t *testing.T) {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	lines := []string{"READY", "CONNECTED", "IDENTITY\t" + b64("123") + "\t" + b64("DB") + "\t" + b64("PDB") + "\t" + b64("SVC") + "\t" + b64("APP") + "\t" + b64("APP"), "OBJECTS\t" + strings.Repeat("a", 64), "HISTORY_ABSENT", "DONE", ""}
	e, err := readOracleEvidenceProtocol(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil || e.DBID != "123" || e.SessionUser != "APP" || e.ObjectsSHA256 != strings.Repeat("a", 64) || e.History == nil || len(e.History) != 0 {
		t.Fatalf("valid absent-history response failed: %#v %v", e, err)
	}
	row := "HISTORY\t1\t" + b64("1") + "\t" + b64("first") + "\t" + b64("SQL") + "\t" + b64("V1__first.sql") + "\t-\t" + b64("APP") + "\t" + b64("2026-01-01 00:00:00") + "\t2\t1"
	lines = []string{"READY", "CONNECTED", "IDENTITY\t" + b64("123") + "\t" + b64("DB") + "\t" + b64("PDB") + "\t" + b64("SVC") + "\t" + b64("APP") + "\t" + b64("APP"), "OBJECTS\t" + strings.Repeat("b", 64), row, "DONE", ""}
	e, err = readOracleEvidenceProtocol(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil || len(e.History) != 1 || e.History[0].Checksum != nil || !e.History[0].Success {
		t.Fatalf("valid physical row failed: %#v %v", e, err)
	}
}

func TestOracleEvidenceProtocolRejectsMalformedAndPartialOutput(t *testing.T) {
	for _, input := range []string{"", "READY\nCONNECTED\nDONE\n", "READY\nCONNECTED\nIDENTITY\tbad\nDONE\n", "READY\nCONNECTED\nIDENTITY\t\t\t\t\t\t\nOBJECTS\t" + strings.Repeat("a", 64) + "\nDONE\n"} {
		if _, err := readOracleEvidenceProtocol(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted malformed protocol %q", input)
		}
	}
}

func TestOracleEvidenceRejectsIdentifiersBeforeRuntimeAccess(t *testing.T) {
	r := New(Config{}, t.TempDir())
	for _, edit := range []func(*Request){func(q *Request) { q.HistorySchema = `APP".OTHER` }, func(q *Request) { q.HistoryTable = `T; DROP TABLE X` }, func(q *Request) { q.Schema = "" }} {
		req := Request{Schema: "APP", HistorySchema: "APP", HistoryTable: "flyway_schema_history", Username: "APP"}
		edit(&req)
		if _, err := r.OracleEvidence(context.Background(), req); err == nil {
			t.Fatal("unsafe evidence identifier accepted")
		}
	}
}

func TestOracleEvidenceHelperArtifactIsReproducible(t *testing.T) {
	digest := sha256.Sum256(oracleEvidenceClass)
	if hex.EncodeToString(digest[:]) != oracleEvidenceClassSHA256 {
		t.Fatal("embedded Oracle evidence helper digest mismatch")
	}
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac unavailable")
	}
	version, err := exec.Command(javac, "-version").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(version), "javac 21") {
		t.Skip("exact helper regeneration requires JDK 21")
	}
	work := t.TempDir()
	cmd := exec.Command(javac, "--release", "11", "-proc:none", "-g:none", "-d", work, filepath.Join("..", "oracle", "OracleEvidenceProbe.java"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("source compile failed: %v (%s)", err, out)
	}
	got, err := os.ReadFile(filepath.Join(work, "OracleEvidenceProbe.class"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(got)
	if hex.EncodeToString(sum[:]) != oracleEvidenceClassSHA256 {
		t.Fatal("embedded class is not reproducible from source")
	}
}

func TestOracleEvidenceHelperQuotesFlywayColumnsAndDistinguishesInvisibleHistory(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac unavailable")
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java unavailable")
	}
	work := t.TempDir()
	if err = os.WriteFile(filepath.Join(work, "StubOracleDriver.java"), []byte(stubOracleDriver), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(javac, "--release", "11", "-proc:none", "-g:none", "-d", work,
		filepath.Join("..", "oracle", "OracleEvidenceProbe.java"), filepath.Join(work, "StubOracleDriver.java"))
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("stub driver compile failed: %v (%s)", e, out)
	}
	service := filepath.Join(work, "META-INF", "services")
	if err = os.MkdirAll(service, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(service, "java.sql.Driver"), []byte("StubOracleDriver\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(schema string, present bool, transport string) (string, error) {
		req := Request{Host: "localhost", Port: 1521, Transport: transport, Service: "FREEPDB1", Schema: schema, HistorySchema: schema, HistoryTable: "flyway_schema_history", Username: "APP", Password: "not-printed"}
		payload, e := encodeOracleEvidenceInput(req, time.Second, time.Second)
		if e != nil {
			return "", e
		}
		p := exec.Command(java, "-Dstub.present="+strconv.FormatBool(present), "-Dstub.user=APP", "-cp", work, "OracleEvidenceProbe")
		p.Stdin = bytes.NewReader(payload)
		out, e := p.CombinedOutput()
		clear(payload)
		if bytes.Contains(out, []byte(req.Password)) {
			t.Fatal("helper leaked credential")
		}
		return string(out), e
	}
	output, err := run("APP", true, "TCP")
	if err != nil {
		t.Fatalf("stubbed real Flyway table query failed: %v (%s)", err, output)
	}
	evidence, err := readOracleEvidenceProtocol(strings.NewReader(output))
	if err != nil || len(evidence.History) != 1 || evidence.History[0].Script != "V1__first.sql" || evidence.History[0].InstalledOn != "2026-06-07T08:09:10.123456789" || evidence.History[0].Checksum != nil {
		t.Fatalf("quoted lowercase Flyway history query was not retained exactly: %#v %v (%s)", evidence, err, output)
	}
	output, err = run("APP", false, "TCP")
	if err != nil {
		t.Fatalf("owner-visible missing table should be accepted: %v (%s)", err, output)
	}
	if _, err = readOracleEvidenceProtocol(strings.NewReader(output)); err != nil || !strings.Contains(output, "HISTORY_ABSENT") {
		t.Fatalf("owner-visible absence not reported: %v (%s)", err, output)
	}
	output, err = run("OTHER", false, "TCP")
	_, protocolErr := readOracleEvidenceProtocol(strings.NewReader(output))
	if err != nil || protocolErr == nil || !strings.Contains(output, "QUERY_ERROR\tORA-00942") || strings.Contains(output, "HISTORY_ABSENT") {
		t.Fatalf("metadata-invisible history was mistaken for genuine absence: err=%v output=%s", err, output)
	}
	output, err = run("APP", true, "TCPS")
	if err != nil {
		t.Fatalf("TCPS descriptor did not enable hostname verification: %v (%s)", err, output)
	}
	if _, err = readOracleEvidenceProtocol(strings.NewReader(output)); err != nil {
		t.Fatalf("TCPS evidence query failed: %v (%s)", err, output)
	}
}
