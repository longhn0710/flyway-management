import javax.net.ssl.*;
import java.net.*;
import java.nio.file.*;
import java.security.*;
import java.security.cert.*;
public class LdapTLSProbe {
 public static void main(String[] args) throws Exception {
  try {
   var cf=CertificateFactory.getInstance("X.509");
   var ks=KeyStore.getInstance(KeyStore.getDefaultType()); ks.load(null,null);
   try(var input=Files.newInputStream(Path.of(args[0]))) {ks.setCertificateEntry("lab",cf.generateCertificate(input));}
   var tm=TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());tm.init(ks);
   var ctx=SSLContext.getInstance("TLS");ctx.init(null,tm.getTrustManagers(),null);
   var raw=new Socket();raw.connect(new InetSocketAddress(args[1],Integer.parseInt(args[2])),5000);raw.setSoTimeout(5000);
   try(var sock=(SSLSocket)ctx.getSocketFactory().createSocket(raw,args[1],Integer.parseInt(args[2]),true)) {
    var p=sock.getSSLParameters();p.setEndpointIdentificationAlgorithm("LDAPS");sock.setSSLParameters(p);sock.startHandshake();
    System.out.println("LDAP_CA_HOSTNAME_VERIFIED "+sock.getSession().getProtocol());
   }
  } catch(Exception e) {System.out.println("LDAP_TLS_PROBE_FAILED "+e.getClass().getSimpleName());System.exit(1);}
 }
}
