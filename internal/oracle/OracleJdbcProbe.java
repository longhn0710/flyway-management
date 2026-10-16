import java.io.DataInputStream;
import java.io.PrintWriter;
import java.nio.charset.StandardCharsets;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;
import java.util.Base64;
import java.util.Properties;

/** Fixed-query, read-only Oracle metadata transport for the Go adapter. */
public final class OracleJdbcProbe {
    private static final int MAGIC = 0x4f52434c;
    private static PrintWriter output;

    public static void main(String[] args) {
        output = new PrintWriter(System.out, true);
        try {
            DataInputStream input = new DataInputStream(System.in);
            if (input.readInt() != MAGIC) {
                throw new IllegalArgumentException("invalid probe input");
            }
            String host = readField(input, 253);
            String transport = readField(input, 4);
            String service = readField(input, 128);
            String username = readField(input, 256);
            String password = readField(input, 4096);
            int port = input.readInt();
            int connectTimeoutMillis = input.readInt();
            int readTimeoutMillis = input.readInt();
            int queryTimeoutSeconds = input.readInt();

            output.println("READY");
            Properties properties = new Properties();
            properties.setProperty("user", username);
            properties.setProperty("password", password);
            properties.setProperty("oracle.net.CONNECT_TIMEOUT", Integer.toString(connectTimeoutMillis));
            properties.setProperty("oracle.jdbc.ReadTimeout", Integer.toString(readTimeoutMillis));
            properties.setProperty("oracle.net.ssl_server_dn_match", "true");
            String protocol = transport.equalsIgnoreCase("TCPS") ? "TCPS" : "TCP";
            String descriptor = "(DESCRIPTION=(ADDRESS=(PROTOCOL=" + protocol + ")(HOST=" + host
                    + ")(PORT=" + port + "))(CONNECT_DATA=(SERVICE_NAME=" + service + "))"
                    + (protocol.equals("TCPS") ? "(SECURITY=(SSL_SERVER_DN_MATCH=YES))" : "") + ")";

            Connection connection;
            try {
                connection = DriverManager.getConnection("jdbc:oracle:thin:@" + descriptor, properties);
            } catch (SQLException error) {
                output.println("CONNECT_ERROR\t" + errorCode(error));
                output.println("DONE");
                output.flush();
                return;
            }
            output.println("CONNECTED");
            readIdentity(connection, queryTimeoutSeconds);
            readUsers(connection, queryTimeoutSeconds);
            readObjects(connection, queryTimeoutSeconds);
            readOneColumn(connection, queryTimeoutSeconds, "SYSTEM_PRIVS", "SELECT PRIVILEGE FROM SESSION_PRIVS ORDER BY PRIVILEGE");
            readOneColumn(connection, queryTimeoutSeconds, "ENABLED_ROLES", "SELECT ROLE FROM SESSION_ROLES ORDER BY ROLE");
            readTwoColumns(connection, queryTimeoutSeconds, "SCHEMA_PRIVS", "SELECT SCHEMA, PRIVILEGE FROM SESSION_SCHEMA_PRIVS ORDER BY SCHEMA, PRIVILEGE");
            readTwoColumns(connection, queryTimeoutSeconds, "ROLE_SCHEMA_PRIVS", "SELECT SCHEMA, PRIVILEGE FROM ROLE_SCHEMA_PRIVS WHERE ROLE IN (SELECT ROLE FROM SESSION_ROLES) ORDER BY SCHEMA, PRIVILEGE");
            output.println("DONE");
            try { connection.close(); } catch (SQLException ignored) { }
        } catch (Throwable ignored) {
            output.println("HELPER_ERROR");
        }
        output.flush();
    }

    private static String readField(DataInputStream input, int maxBytes) throws Exception {
        int length = input.readInt();
        if (length < 0 || length > maxBytes) throw new IllegalArgumentException("invalid input field");
        byte[] bytes = new byte[length];
        input.readFully(bytes);
        return new String(bytes, StandardCharsets.UTF_8);
    }

    private static String b64(String value) {
        return Base64.getEncoder().encodeToString((value == null ? "" : value).getBytes(StandardCharsets.UTF_8));
    }

    private static Statement statement(Connection connection, int timeout) throws SQLException {
        Statement statement = connection.createStatement(ResultSet.TYPE_FORWARD_ONLY, ResultSet.CONCUR_READ_ONLY);
        statement.setFetchSize(128);
        statement.setQueryTimeout(timeout);
        return statement;
    }

    private static void readIdentity(Connection connection, int timeout) {
        String sql = "SELECT SYS_CONTEXT('USERENV','SESSION_USER'), SYS_CONTEXT('USERENV','CURRENT_SCHEMA'), SYS_CONTEXT('USERENV','SERVICE_NAME'), SYS_CONTEXT('USERENV','CON_NAME') FROM DUAL";
        try (Statement statement = statement(connection, timeout); ResultSet rows = statement.executeQuery(sql)) {
            if (rows.next()) output.println("IDENTITY\t" + b64(rows.getString(1)) + "\t" + b64(rows.getString(2)) + "\t" + b64(rows.getString(3)) + "\t" + b64(rows.getString(4)));
            else output.println("META_ERROR\tSESSION_IDENTITY\tORA-00000");
        } catch (SQLException error) {
            output.println("META_ERROR\tSESSION_IDENTITY\t" + errorCode(error));
        }
        output.println("VIEW_DONE\tSESSION_IDENTITY");
    }

    private static void readUsers(Connection connection, int timeout) {
        String maintainedQuery = "SELECT USERNAME, ORACLE_MAINTAINED FROM ALL_USERS ORDER BY USERNAME";
        try {
            readUserRows(connection, timeout, maintainedQuery, true);
            output.println("VIEW_DONE\tALL_USERS");
            return;
        } catch (SQLException error) {
            if (error.getErrorCode() != 904) {
                output.println("META_ERROR\tALL_USERS\t" + errorCode(error));
                return;
            }
            output.println("META_ERROR\tALL_USERS_MAINTAINED\t" + errorCode(error));
        }
        try {
            readUserRows(connection, timeout, "SELECT USERNAME FROM ALL_USERS ORDER BY USERNAME", false);
        } catch (SQLException error) {
            output.println("META_ERROR\tALL_USERS\t" + errorCode(error));
        }
        output.println("VIEW_DONE\tALL_USERS");
    }

    private static void readUserRows(Connection connection, int timeout, String sql, boolean hasMaintained) throws SQLException {
        try (Statement statement = statement(connection, timeout); ResultSet rows = statement.executeQuery(sql)) {
            while (rows.next()) output.println("USER\t" + b64(rows.getString(1)) + "\t" + b64(hasMaintained ? rows.getString(2) : ""));
        }
    }

    private static void readObjects(Connection connection, int timeout) {
        String sql = "SELECT OWNER, COUNT(*) FROM ALL_OBJECTS GROUP BY OWNER";
        try (Statement statement = statement(connection, timeout); ResultSet rows = statement.executeQuery(sql)) {
            while (rows.next()) output.println("OBJECT\t" + b64(rows.getString(1)) + "\t" + rows.getLong(2));
        } catch (SQLException error) {
            output.println("META_ERROR\tALL_OBJECTS\t" + errorCode(error));
        }
        output.println("VIEW_DONE\tALL_OBJECTS");
    }

    private static void readOneColumn(Connection connection, int timeout, String tag, String sql) {
        try (Statement statement = statement(connection, timeout); ResultSet rows = statement.executeQuery(sql)) {
            while (rows.next()) output.println(tag + "\t" + b64(rows.getString(1)));
        } catch (SQLException error) {
            output.println("META_ERROR\t" + tag + "\t" + errorCode(error));
        }
        output.println("VIEW_DONE\t" + tag);
    }

    private static void readTwoColumns(Connection connection, int timeout, String tag, String sql) {
        try (Statement statement = statement(connection, timeout); ResultSet rows = statement.executeQuery(sql)) {
            while (rows.next()) output.println(tag + "\t" + b64(rows.getString(1)) + "\t" + b64(rows.getString(2)));
        } catch (SQLException error) {
            output.println("META_ERROR\t" + tag + "\t" + errorCode(error));
        }
        output.println("VIEW_DONE\t" + tag);
    }

    private static String errorCode(SQLException error) {
        for (SQLException current = error; current != null; current = current.getNextException()) {
            int code = current.getErrorCode();
            if (code > 0 && code < 100000) return String.format("ORA-%05d", code);
        }
        return "ORACLE-CONNECTION-ERROR";
    }
}
