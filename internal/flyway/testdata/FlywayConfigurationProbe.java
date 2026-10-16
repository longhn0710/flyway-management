import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.util.Base64;
import java.util.HexFormat;
import org.flywaydb.commandline.configuration.CommandLineArguments;
import org.flywaydb.commandline.configuration.ModernConfigurationManager;
import org.flywaydb.core.api.configuration.Configuration;
import org.flywaydb.core.internal.configuration.ConfigUtils;
import org.flywaydb.core.internal.plugin.PluginRegister;
import org.flywaydb.database.oracle.OracleConfigurationExtension;

// Offline probe of the actual pinned CLI loader, with no database connection.
// Only synthetic credentials are supplied, in private files, never argv/output.
public class FlywayConfigurationProbe {
    private static void value(String key, String value) {
        System.out.println(key + "=" + Base64.getEncoder().encodeToString(value.getBytes(StandardCharsets.UTF_8)));
    }
    public static void main(String[] args) throws Exception {
        if (args[0].equals("legacy")) {
            var values = ConfigUtils.loadConfigurationFromString(Files.readString(Path.of(args[1])));
            value("url", values.get("flyway.url"));
            return;
        }
        var cli = new CommandLineArguments(new PluginRegister(), "-configFiles=" + args[1], "-workingDirectory=" + args[2], "info");
        Configuration c = new ModernConfigurationManager().getConfiguration(cli);
        value("url", c.getUrl());
        value("user", c.getUser());
        var digest = MessageDigest.getInstance("SHA-256").digest(c.getPassword().getBytes(StandardCharsets.UTF_8));
        value("password_sha256", HexFormat.of().formatHex(digest));
        value("schemas", String.join(",", c.getSchemas()));
        value("defaultSchema", c.getDefaultSchema());
        value("table", c.getTable());
        value("createSchemas", Boolean.toString(c.isCreateSchemas()));
        value("cleanDisabled", Boolean.toString(c.isCleanDisabled()));
        value("validateOnMigrate", Boolean.toString(c.isValidateOnMigrate()));
        value("placeholderReplacement", Boolean.toString(c.isPlaceholderReplacement()));
        value("validateMigrationNaming", Boolean.toString(c.isValidateMigrationNaming()));
        value("baselineOnMigrate", Boolean.toString(c.isBaselineOnMigrate()));
        value("outOfOrder", Boolean.toString(c.isOutOfOrder()));
        value("callbacks", Integer.toString(c.getCallbacks().length));
        value("ignoreMigrationPatterns", java.util.Arrays.toString(c.getIgnoreMigrationPatterns()));
        value("oracle.sqlplus", Boolean.toString(c.getConfigurationExtension(OracleConfigurationExtension.class).getSqlplus()));
    }
}
