import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.io.FileOutputStream;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.Timestamp;
import java.util.ArrayList;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Properties;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.regex.Pattern;

public final class PostgreSQLExecutionGuard {
    private static FileOutputStream journal;

    private static void journal(String json) throws Exception {
        if (journal == null) throw new Exception("journal unavailable");
        journal.write((json + "\n").getBytes(java.nio.charset.StandardCharsets.UTF_8));
        journal.flush();
        journal.getFD().sync();
    }

    private static final int MAGIC = 0x50475351;
    private static final int MAX_CURRENT_SESSIONS = 8;
    private static final int MAX_SNAPSHOTS = 64;
    private static final Pattern APPLICATION_NAME = Pattern.compile("^fwm_[A-Za-z0-9_-]{16,58}$");

    private static final class Session {
        int pid;
        Timestamp backendStart;
        Timestamp xactStart;
        Timestamp queryStart;
        String state;
        String waitType;
        String waitEvent;

        String identity() {
            return pid + "/" + timestamp(backendStart);
        }

        String snapshotKey() {
            return identity() + "/" + state + "/" + timestamp(xactStart) + "/"
                    + timestamp(queryStart) + "/" + waitType + "/" + waitEvent;
        }
    }

    private static String readString(DataInputStream in) throws Exception {
        int length = in.readInt();
        if (length < 0 || length > 65536) {
            throw new Exception("invalid input length");
        }
        byte[] bytes = new byte[length];
        in.readFully(bytes);
        return new String(bytes, "UTF-8");
    }

    private static List<Session> scan(Connection connection, String database, String role,
            String applicationName, int guardPid) throws Exception {
        String sql = "select pid, backend_start, state, xact_start, query_start, "
                + "wait_event_type, wait_event from pg_catalog.pg_stat_activity "
                + "where datname=? and usename=? and application_name=? and pid<>? "
                + "order by pid limit 9";
        List<Session> sessions = new ArrayList<>();
        try (PreparedStatement statement = connection.prepareStatement(sql)) {
            statement.setString(1, database);
            statement.setString(2, role);
            statement.setString(3, applicationName);
            statement.setInt(4, guardPid);
            statement.setQueryTimeout(1);
            try (ResultSet rows = statement.executeQuery()) {
                while (rows.next()) {
                    Session session = new Session();
                    session.pid = rows.getInt(1);
                    session.backendStart = rows.getTimestamp(2);
                    session.state = rows.getString(3);
                    session.xactStart = rows.getTimestamp(4);
                    session.queryStart = rows.getTimestamp(5);
                    session.waitType = rows.getString(6);
                    session.waitEvent = rows.getString(7);
                    sessions.add(session);
                }
            }
        }
        return sessions;
    }

    private static String timestamp(Timestamp value) {
        return value == null ? "" : value.toInstant().toString();
    }

    private static String quote(String value) {
        StringBuilder result = new StringBuilder("\"");
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            if (c == '"' || c == '\\') {
                result.append('\\').append(c);
            } else if (c < 32) {
                result.append(String.format("\\u%04x", (int) c));
            } else {
                result.append(c);
            }
        }
        return result.append('"').toString();
    }

    private static String encodeSnapshots(Map<String, Session> snapshots) {
        StringBuilder json = new StringBuilder("[");
        boolean first = true;
        for (Session session : snapshots.values()) {
            if (!first) {
                json.append(',');
            }
            first = false;
            json.append("{\"pid\":").append(session.pid)
                    .append(",\"backend_start\":").append(quote(timestamp(session.backendStart)))
                    .append(",\"state\":").append(quote(session.state == null ? "" : session.state))
                    .append(",\"xact_start\":").append(quote(timestamp(session.xactStart)))
                    .append(",\"query_start\":").append(quote(timestamp(session.queryStart)))
                    .append(",\"wait_event_type\":").append(quote(session.waitType == null ? "" : session.waitType))
                    .append(",\"wait_event\":").append(quote(session.waitEvent == null ? "" : session.waitEvent))
                    .append('}');
        }
        byte[] bytes = json.append(']').toString().getBytes(java.nio.charset.StandardCharsets.UTF_8);
        return Base64.getEncoder().encodeToString(bytes);
    }

    private static void emitDone(DataOutputStream out, String outcome, boolean gone,
            boolean cleanupVerified, String applicationName, Map<String, Session> snapshots) {
        try {
            journal("{\"event\":\"finished\",\"application_name\":" + quote(applicationName)
                    + ",\"outcome\":" + quote(outcome) + ",\"gone\":" + gone
                    + ",\"cleanup_verified\":" + cleanupVerified + ",\"snapshots\":"
                    + new String(Base64.getDecoder().decode(encodeSnapshots(snapshots)), java.nio.charset.StandardCharsets.UTF_8) + "}");
            out.write(("DONE\t" + outcome + "\t" + gone + "\t" + cleanupVerified + "\t"
                    + applicationName + "\t" + encodeSnapshots(snapshots) + "\n").getBytes("UTF-8"));
            out.flush();
        } catch (Exception ignored) {
            // The Go caller treats a missing final record as unverified cleanup.
        }
    }

    public static void main(String[] args) {
        DataOutputStream output = new DataOutputStream(System.out);
        Map<String, Session> snapshots = new LinkedHashMap<>();
        String applicationName = "";
        Connection connection = null;
        try {
            DataInputStream input = new DataInputStream(System.in);
            if (input.readInt() != MAGIC) {
                throw new Exception("invalid protocol");
            }
            String host = readString(input);
            String database = readString(input);
            String role = readString(input);
            String password = readString(input);
            String caPath = readString(input);
            int port = input.readInt();
            int connectTimeout = input.readInt();
            int socketTimeout = input.readInt();
            int queryTimeout = input.readInt();
            applicationName = readString(input);
            int executionBudget = input.readInt();
            String journalPath = readString(input);
            journal = new FileOutputStream(journalPath, true);
            if (!APPLICATION_NAME.matcher(applicationName).matches() || port < 1 || port > 65535
                    || executionBudget < 1 || executionBudget > 3600) {
                throw new Exception("invalid guard identity");
            }

            Class.forName("org.postgresql.Driver");
            Properties properties = new Properties();
            properties.setProperty("user", role);
            properties.setProperty("password", password);
            properties.setProperty("sslmode", "verify-full");
            properties.setProperty("sslrootcert", caPath);
            properties.setProperty("ApplicationName", "FlywayManagerExecutionGuard");
            properties.setProperty("connectTimeout", Integer.toString(connectTimeout));
            properties.setProperty("socketTimeout", Integer.toString(Math.min(socketTimeout, 2)));
            properties.setProperty("options", "-c default_transaction_read_only=on -c statement_timeout=" + queryTimeout * 1000);
            String jdbcHost = host.indexOf(':') >= 0 ? "[" + host + "]" : host;
            String jdbcUrl = "jdbc:postgresql://" + jdbcHost + ":" + port + "/" + database;
            connection = DriverManager.getConnection(jdbcUrl, properties);

            int guardPid;
            try (PreparedStatement statement = connection.prepareStatement(
                    "select pg_backend_pid(), current_database(), session_user, current_user, "
                            + "current_setting('server_version_num')")) {
                statement.setQueryTimeout(queryTimeout);
                try (ResultSet row = statement.executeQuery()) {
                    if (!row.next() || !database.equals(row.getString(2))
                            || !role.equals(row.getString(3)) || !role.equals(row.getString(4))
                            || !row.getString(5).startsWith("170")) {
                        throw new Exception("identity mismatch");
                    }
                    guardPid = row.getInt(1);
                }
            }

            journal("{\"event\":\"ready\",\"application_name\":" + quote(applicationName)
                    + ",\"guard_backend_pid\":" + guardPid + ",\"execution_budget_seconds\":" + executionBudget + "}");
            output.write("READY\n".getBytes("UTF-8"));
            output.flush();
            AtomicInteger control = new AtomicInteger(0);
            Thread controlReader = new Thread(() -> {
                try {
                    int command = System.in.read();
                    control.set(command < 0 ? 'C' : command);
                } catch (Exception e) {
                    control.set('C');
                }
            });
            controlReader.setDaemon(true);
            controlReader.start();

            long deadline = System.nanoTime() + (executionBudget + 10L) * 1_000_000_000L;
            String outcome = "unknown";
            boolean cancelled = false;
            while (true) {
                int command = control.get();
                if (command == 'C') {
                    cancelled = true;
                } else if (command == 'S') {
                    cancelled = false;
                    outcome = "success";
                } else if (command != 0) {
                    throw new Exception("invalid command");
                }
                if (System.nanoTime() > deadline && !cancelled) {
                    cancelled = true;
                }

                List<Session> current = scan(connection, database, role, applicationName, guardPid);
                for (Session session : current) {
                    String key = session.snapshotKey();
                    if (!snapshots.containsKey(key)) {
                        if (snapshots.size() >= MAX_SNAPSHOTS) {
                            emitDone(output, "unknown", false, false, applicationName, snapshots);
                            return;
                        }
                        snapshots.put(key, session);
                        journal("{\"event\":\"observed\",\"application_name\":" + quote(applicationName)
                                + ",\"snapshots\":" + new String(Base64.getDecoder().decode(encodeSnapshots(snapshots)), java.nio.charset.StandardCharsets.UTF_8) + "}");
                    }
                }
                if (current.size() > MAX_CURRENT_SESSIONS) {
                    emitDone(output, "unknown", false, false, applicationName, snapshots);
                    return;
                }
                if ("success".equals(outcome) && current.isEmpty()) {
                    emitDone(output, "success_gone", true, true, applicationName, snapshots);
                    return;
                }
                if (cancelled) {
                    // Observe only. application_name is client-controlled and cannot
                    // authorize termination of a database backend. Startup timeouts
                    // bound the supported execution; uncertain outcomes remain held.
                    long cleanupDeadline = deadline + 5_000_000_000L;
                    boolean gone = false;
                    while (System.nanoTime() < cleanupDeadline) {
                        List<Session> remaining = scan(connection, database, role, applicationName, guardPid);
                        for (Session session : remaining) {
                            String key = session.snapshotKey();
                            if (!snapshots.containsKey(key)) {
                                if (snapshots.size() >= MAX_SNAPSHOTS) {
                                    emitDone(output, "unknown", false, false, applicationName, snapshots);
                                    return;
                                }
                                snapshots.put(key, session);
                            }
                        }
                        if (remaining.size() > MAX_CURRENT_SESSIONS) {
                            emitDone(output, "unknown", false, false, applicationName, snapshots);
                            return;
                        }
                        if (remaining.isEmpty()) {
                            gone = true;
                            break;
                        }
                        Thread.sleep(100);
                    }
                    emitDone(output, gone ? "cancelled_gone" : "cancelled_unknown",
                            gone, gone, applicationName, snapshots);
                    return;
                }
                Thread.sleep(100);
            }
        } catch (Throwable t) {
            emitDone(output, "unknown", false, false, applicationName, snapshots);
        } finally {
            try {
                if (journal != null) {
                    journal.close();
                }
                if (connection != null) {
                    connection.close();
                }
            } catch (Exception ignored) {
                // Guard evidence has already been emitted, if execution began.
            }
        }
    }
}
