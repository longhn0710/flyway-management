import java.io.DataInputStream;
import java.io.PrintWriter;
import java.nio.charset.StandardCharsets;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.util.Base64;
import java.util.Properties;

/** Fixed-query, read-only PostgreSQL identity and schema probe. */
public final class PostgreSQLJdbcProbe {
    private static final int MAGIC = 0x50475351;
    private static final PrintWriter OUT = new PrintWriter(System.out, true);

    public static void main(String[] args) {
        try {
            DataInputStream in = new DataInputStream(System.in);
            if (in.readInt() != MAGIC) throw new IllegalArgumentException("invalid probe input");
            String host = field(in, 253);
            String database = field(in, 63);
            String username = field(in, 63);
            String password = field(in, 4096);
            String caFile = field(in, 4096);
            int port = in.readInt();
            int connectTimeout = in.readInt();
            int socketTimeout = in.readInt();
            int queryTimeout = in.readInt();
            OUT.println("READY");

            Properties props = new Properties();
            props.setProperty("user", username);
            props.setProperty("password", password);
            props.setProperty("sslmode", "verify-full");
            props.setProperty("sslrootcert", caFile);
            props.setProperty("connectTimeout", Integer.toString(connectTimeout));
            props.setProperty("socketTimeout", Integer.toString(socketTimeout));
            props.setProperty("ApplicationName", "FlywayManagerReadOnlyProbe");
            String url = "jdbc:postgresql://" + (host.indexOf(':') >= 0 ? "[" + host + "]" : host)
                    + ":" + port + "/" + database;
            Connection connection;
            try {
                connection = DriverManager.getConnection(url, props);
            } catch (SQLException error) {
                OUT.println("CONNECT_ERROR\t" + safeSqlState(error));
                OUT.println("DONE");
                return;
            }
            OUT.println("CONNECTED");
            try (Connection c = connection) {
                c.setReadOnly(true);
                try (PreparedStatement statement = c.prepareStatement(
                        "SELECT current_database(), session_user, current_user, current_setting('server_version_num')")) {
                    statement.setQueryTimeout(queryTimeout);
                    try (ResultSet rows = statement.executeQuery()) {
                        if (!rows.next()) throw new SQLException("missing identity");
                        OUT.println("IDENTITY\t" + b64(rows.getString(1)) + "\t" + b64(rows.getString(2))
                                + "\t" + b64(rows.getString(3)) + "\t" + b64(rows.getString(4)));
                    }
                }
                try (PreparedStatement statement = c.prepareStatement(
                        "SELECT n.nspname, pg_catalog.pg_get_userbyid(n.nspowner), has_schema_privilege(n.oid,'CREATE') "
                        + "FROM pg_catalog.pg_namespace n "
                        + "WHERE n.nspname <> 'information_schema' AND n.nspname !~ '^pg_' "
                        + "AND has_schema_privilege(n.oid,'USAGE') ORDER BY n.nspname")) {
                    statement.setFetchSize(128);
                    statement.setQueryTimeout(queryTimeout);
                    try (ResultSet rows = statement.executeQuery()) {
                        while (rows.next()) OUT.println("SCHEMA\t" + b64(rows.getString(1)) + "\t" + b64(rows.getString(2)) + "\t" + (rows.getBoolean(3) ? "1" : "0"));
                    }
                }
            } catch (SQLException error) {
                OUT.println("META_ERROR\t" + safeSqlState(error));
            }
            OUT.println("DONE");
        } catch (Throwable ignored) {
            OUT.println("HELPER_ERROR");
        }
        OUT.flush();
    }

    private static String field(DataInputStream in, int max) throws Exception {
        int length = in.readInt();
        if (length < 0 || length > max) throw new IllegalArgumentException("invalid field length");
        byte[] bytes = new byte[length];
        in.readFully(bytes);
        return new String(bytes, StandardCharsets.UTF_8);
    }

    private static String b64(String value) {
        return Base64.getEncoder().encodeToString((value == null ? "" : value).getBytes(StandardCharsets.UTF_8));
    }

    private static String safeSqlState(SQLException error) {
        String state = error.getSQLState();
        return state != null && state.matches("[0-9A-Z]{5}") ? state : "08000";
    }
}
