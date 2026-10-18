import javax.net.ssl.HttpsURLConnection;import java.net.*;import java.io.*;
public class OIDCDiscoveryProbe {
 public static void main(String[] a) {
 try {
 var conn=(HttpsURLConnection)new URL("https://auth.apps.drgdevlab.com/realms/kubernetes/.well-known/openid-configuration").openConnection();conn.setConnectTimeout(4000);conn.setReadTimeout(4000);conn.setInstanceFollowRedirects(false);var code=conn.getResponseCode();System.out.println("CANONICAL_KEYCLOAK_TLS_DISCOVERY_HTTP_"+code);conn.disconnect();
 }catch(Exception e){System.out.println("CANONICAL_KEYCLOAK_PROBE_FAILED "+e.getClass().getSimpleName());System.exit(1);}
 }
}
