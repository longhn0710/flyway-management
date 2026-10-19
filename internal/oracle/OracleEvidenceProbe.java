import java.io.DataInputStream;
import java.io.PrintWriter;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;
import java.util.ArrayList;
import java.util.Base64;
import java.util.List;
import java.util.Properties;

/** Fixed, read-only Oracle identity, object-catalogue, and Flyway history probe. */
public final class OracleEvidenceProbe {
    private static final int MAGIC = 0x4f524345;
    private static final String ID = "[A-Za-z][A-Za-z0-9_$#]{0,127}";
    private static PrintWriter output;

    public static void main(String[] args) {
        output = new PrintWriter(System.out, true);
        try {
            DataInputStream input = new DataInputStream(System.in);
            if (input.readInt() != MAGIC) throw new IllegalArgumentException("invalid input");
            String host = readField(input, 253);
            String transport = readField(input, 4);
            String service = readField(input, 128);
            String username = readField(input, 256);
            String password = readField(input, 4096);
            int port = input.readInt();
            int connectTimeout = input.readInt();
            int readTimeout = input.readInt();
            int queryTimeout = input.readInt();
            String schema = readField(input, 128);
            String table = readField(input, 128);
            if (!schema.matches(ID) || !table.matches(ID)) throw new IllegalArgumentException("invalid identifier");

            output.println("READY");
            Properties properties = new Properties();
            properties.setProperty("user", username);
            properties.setProperty("password", password);
            properties.setProperty("oracle.net.CONNECT_TIMEOUT", Integer.toString(connectTimeout));
            properties.setProperty("oracle.jdbc.ReadTimeout", Integer.toString(readTimeout));
            properties.setProperty("oracle.net.ssl_server_dn_match", "true");
            String protocol = transport.equalsIgnoreCase("TCPS") ? "TCPS" : "TCP";
            String descriptor = "(DESCRIPTION=(ADDRESS=(PROTOCOL=" + protocol + ")(HOST=" + host
                    + ")(PORT=" + port + "))(CONNECT_DATA=(SERVICE_NAME=" + service + "))"
                    + (protocol.equals("TCPS") ? "(SECURITY=(SSL_SERVER_DN_MATCH=YES))" : "") + ")";

            try (Connection connection = DriverManager.getConnection("jdbc:oracle:thin:@" + descriptor, properties)) {
                output.println("CONNECTED");
                connection.setAutoCommit(false);
                try (Statement statement = connection.createStatement()) {
                    statement.execute("SET TRANSACTION READ ONLY");
                }
                String sessionUser = readIdentity(connection, queryTimeout);
                readObjects(connection, queryTimeout, schema);
                readHistory(connection, queryTimeout, schema, table, sessionUser);
                connection.rollback();
                output.println("DONE");
            } catch (SQLException error) {
                output.println("QUERY_ERROR\t" + errorCode(error));
                output.println("DONE");
            }
        } catch (Throwable ignored) {
            output.println("HELPER_ERROR");
        }
        output.flush();
    }

    private static String readField(DataInputStream input, int maximumBytes) throws Exception {
        int length = input.readInt();
        if (length < 0 || length > maximumBytes) throw new IllegalArgumentException("invalid input field");
        byte[] value = new byte[length];
        input.readFully(value);
        return new String(value, StandardCharsets.UTF_8);
    }

    private static String encoded(String value) {
        return Base64.getEncoder().encodeToString((value == null ? "" : value).getBytes(StandardCharsets.UTF_8));
    }

    private static Statement statement(Connection connection, int timeout) throws SQLException {
        Statement statement = connection.createStatement(ResultSet.TYPE_FORWARD_ONLY, ResultSet.CONCUR_READ_ONLY);
        statement.setFetchSize(128);
        statement.setQueryTimeout(timeout);
        return statement;
    }

    private static String readIdentity(Connection connection, int timeout) throws Exception {
        // Resolve trusted dictionary objects explicitly: a migration account can
        // create private views that shadow PUBLIC synonyms.
        String query = "SELECT (SELECT DBID FROM SYS.V_$DATABASE), (SELECT DB_UNIQUE_NAME FROM SYS.V_$DATABASE), "
                + "SYS_CONTEXT('USERENV','CON_NAME'), SYS_CONTEXT('USERENV','SERVICE_NAME'), "
                + "SYS_CONTEXT('USERENV','SESSION_USER'), SYS_CONTEXT('USERENV','CURRENT_SCHEMA') FROM SYS.DUAL";
        try (Statement statement = statement(connection, timeout);
             ResultSet rows = statement.executeQuery(query)) {
            if (!rows.next()) throw new SQLException("Oracle identity query returned no row");
            output.println("IDENTITY\t" + encoded(rows.getString(1)) + "\t" + encoded(rows.getString(2))
                    + "\t" + encoded(rows.getString(3)) + "\t" + encoded(rows.getString(4))
                    + "\t" + encoded(rows.getString(5)) + "\t" + encoded(rows.getString(6)));
            return rows.getString(5);
        }
    }

    private static void readObjects(Connection connection, int timeout, String schema) throws Exception {
        String query = "SELECT OBJECT_ID, OBJECT_NAME, OBJECT_TYPE, STATUS, "
                + "TO_CHAR(LAST_DDL_TIME,'YYYY-MM-DD HH24:MI:SS') FROM SYS.ALL_OBJECTS WHERE OWNER='"
                + schema.toUpperCase() + "' ORDER BY OBJECT_ID, OBJECT_NAME, OBJECT_TYPE";
        List<String> canonicalRows = new ArrayList<>();
        try (Statement statement = statement(connection, timeout);
             ResultSet rows = statement.executeQuery(query)) {
            while (rows.next()) {
                canonicalRows.add(value(rows.getString(1)) + "\t" + value(rows.getString(2)) + "\t"
                        + value(rows.getString(3)) + "\t" + value(rows.getString(4)) + "\t"
                        + value(rows.getString(5)));
            }
        }
        MessageDigest digest = MessageDigest.getInstance("SHA-256");
        for (String row : canonicalRows) {
            byte[] bytes = row.getBytes(StandardCharsets.UTF_8);
            digest.update((byte) (bytes.length >>> 24));
            digest.update((byte) (bytes.length >>> 16));
            digest.update((byte) (bytes.length >>> 8));
            digest.update((byte) bytes.length);
            digest.update(bytes);
        }
        StringBuilder hex = new StringBuilder();
        for (byte item : digest.digest()) hex.append(String.format("%02x", item));
        output.println("OBJECTS\t" + hex);
    }

    private static String value(String value) {
        return value == null ? "" : value;
    }

    private static void readHistory(Connection connection, int timeout, String schema, String table,
                                    String sessionUser) throws Exception {
        String owner = schema.toUpperCase();
        String metadataQuery = "SELECT COUNT(*) FROM SYS.ALL_TABLES WHERE OWNER='" + owner
                + "' AND TABLE_NAME='" + table + "'";
        try (Statement statement = statement(connection, timeout);
             ResultSet rows = statement.executeQuery(metadataQuery)) {
            if (!rows.next()) throw new SQLException("Oracle history metadata query returned no row");
            if (rows.getInt(1) == 0) {
                if (!owner.equalsIgnoreCase(sessionUser)) {
                    throw new SQLException("history metadata is not visible", "", 942);
                }
                output.println("HISTORY_ABSENT");
                return;
            }
        }

        // Flyway creates these Oracle columns and the default table as lowercase quoted identifiers.
        String query = "SELECT \"installed_rank\", \"version\", \"description\", \"type\", \"script\", "
                + "\"checksum\", \"installed_by\", TO_CHAR(\"installed_on\","
                + "'YYYY-MM-DD\"T\"HH24:MI:SS.FF9','NLS_DATE_LANGUAGE=English NLS_CALENDAR=Gregorian'), "
                + "\"execution_time\", \"success\" FROM \"" + owner + "\".\"" + table
                + "\" ORDER BY \"installed_rank\"";
        try (Statement statement = statement(connection, timeout);
             ResultSet rows = statement.executeQuery(query)) {
            while (rows.next()) {
                String checksum = rows.getString(6);
                String success = rows.getString(10);
                output.println("HISTORY\t" + rows.getInt(1) + "\t" + encoded(rows.getString(2)) + "\t"
                        + encoded(rows.getString(3)) + "\t" + encoded(rows.getString(4)) + "\t"
                        + encoded(rows.getString(5)) + "\t" + (checksum == null ? "-" : checksum) + "\t"
                        + encoded(rows.getString(7)) + "\t" + encoded(rows.getString(8)) + "\t"
                        + rows.getInt(9) + "\t" + (("1".equals(success) || "Y".equals(success)) ? "1" : "0"));
            }
        }
    }

    private static String errorCode(SQLException error) {
        for (SQLException current = error; current != null; current = current.getNextException()) {
            int code = current.getErrorCode();
            if (code > 0 && code < 100000) return String.format("ORA-%05d", code);
        }
        return "ORACLE-ERROR";
    }
}
