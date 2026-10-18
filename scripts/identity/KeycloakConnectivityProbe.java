import java.net.*;
import java.nio.charset.StandardCharsets;
import java.security.KeyStore;
import java.security.cert.*;
import java.util.List;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.regex.Pattern;
import javax.net.ssl.*;

// Bounded public-metadata/SNI probe: no credentials, trust bypass or global SSL changes.
public class KeycloakConnectivityProbe {
    static final String HOST = "auth.apps.drgdevlab.com";
    static final String ISSUER = "https://" + HOST + "/realms/kubernetes";

    public static void main(String[] args) throws Exception {
        String mode = args.length == 1 ? args[0] : "";
        if (mode.equals("addresses")) {
            String addresses = java.util.Arrays.stream(InetAddress.getAllByName(HOST)).map(x -> "\"" + x.getHostAddress() + "\"").collect(java.util.stream.Collectors.joining(","));
            System.out.println("{\"mode\":\"addresses\",\"resolved\":[" + addresses + "],\"proxy\":\"" + ProxySelector.getDefault().select(URI.create(ISSUER)).get(0).type() + "\"}");
            return;
        }
        if (mode.equals("discovery")) {
            try {
                var call = (HttpsURLConnection) new URL(ISSUER + "/.well-known/openid-configuration").openConnection();
                call.setConnectTimeout(4000);
                call.setReadTimeout(4000);
                call.setInstanceFollowRedirects(false);
                try {
                    int status = call.getResponseCode();
                    byte[] body = call.getInputStream().readNBytes(65537);
                    String json = new String(body, StandardCharsets.UTF_8);
                    boolean issuer = Pattern.compile("\"issuer\"\\s*:\\s*\"" + Pattern.quote(ISSUER) + "\"").matcher(json).find();
                    boolean pkce = Pattern.compile("\"code_challenge_methods_supported\"\\s*:\\s*\\[[^\\]]*\"S256\"").matcher(json).find();
                    System.out.println("{\"mode\":\"discovery\",\"http_status\":" + status + ",\"issuer_exact\":" + issuer + ",\"pkce_s256\":" + pkce + ",\"bounded_body\":" + (body.length <= 65536) + "}");
                } finally { call.disconnect(); }
            } catch (Exception e) {
                System.out.println("{\"mode\":\"discovery\",\"exception\":\"" + e.getClass().getSimpleName() + "\"}");
            }
            return;
        }
        if (!mode.equals("alternate-sni") && !mode.equals("no-sni")) throw new IllegalArgumentException("expected discovery, alternate-sni or no-sni");
        var seen = new AtomicBoolean();
        var managers = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
        managers.init((KeyStore) null);
        if (!(managers.getTrustManagers()[0] instanceof X509ExtendedTrustManager original)) throw new IllegalStateException("extended system trust manager required");
        var context = SSLContext.getInstance("TLS");
        context.init(null, new TrustManager[]{new RecordingTrust(original, seen)}, null);
        try (var raw = new Socket()) {
            raw.connect(new InetSocketAddress(HOST, 443), 4000);
            raw.setSoTimeout(4000);
            try (var tls = (SSLSocket) context.getSocketFactory().createSocket(raw, HOST, 443, true)) {
                var parameters = tls.getSSLParameters();
                parameters.setEndpointIdentificationAlgorithm("HTTPS");
                parameters.setServerNames(mode.equals("no-sni") ? List.of() : List.of(new SNIHostName("database-manager-auth.apps.drgdevlab.com")));
                tls.setSSLParameters(parameters);
                tls.setEnabledProtocols(new String[]{"TLSv1.3", "TLSv1.2"});
                tls.startHandshake();
                System.out.println("{\"mode\":\"" + mode + "\",\"tls_accepted\":true,\"server_certificate_seen\":" + seen.get() + "}");
            }
        } catch (Exception e) {
            System.out.println("{\"mode\":\"" + mode + "\",\"tls_accepted\":false,\"server_certificate_seen\":" + seen.get() + ",\"exception\":\"" + e.getClass().getSimpleName() + "\"}");
        }
    }

    // Preserve every system trust and hostname check, recording only whether a
    // certificate was presented. Rejection before a certificate differs from
    // a default ingress certificate that fails ordinary CA/hostname validation.
    static final class RecordingTrust extends X509ExtendedTrustManager {
        final X509ExtendedTrustManager delegate;
        final AtomicBoolean seen;
        RecordingTrust(X509ExtendedTrustManager delegate, AtomicBoolean seen) { this.delegate = delegate; this.seen = seen; }
        public X509Certificate[] getAcceptedIssuers() { return delegate.getAcceptedIssuers(); }
        public void checkClientTrusted(X509Certificate[] c, String a) throws CertificateException { delegate.checkClientTrusted(c, a); }
        public void checkServerTrusted(X509Certificate[] c, String a) throws CertificateException { seen.set(true); delegate.checkServerTrusted(c, a); }
        public void checkClientTrusted(X509Certificate[] c, String a, Socket s) throws CertificateException { delegate.checkClientTrusted(c, a, s); }
        public void checkServerTrusted(X509Certificate[] c, String a, Socket s) throws CertificateException { seen.set(true); delegate.checkServerTrusted(c, a, s); }
        public void checkClientTrusted(X509Certificate[] c, String a, SSLEngine s) throws CertificateException { delegate.checkClientTrusted(c, a, s); }
        public void checkServerTrusted(X509Certificate[] c, String a, SSLEngine s) throws CertificateException { seen.set(true); delegate.checkServerTrusted(c, a, s); }
    }
}
