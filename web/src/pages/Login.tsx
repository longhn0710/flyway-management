import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowRight,
  LockKeyhole,
  Building2,
  ShieldCheck,
  Eye,
  EyeOff,
} from "lucide-react";
import { Brand } from "../components/Shell";
import { Button, Field, Notice } from "../components/ui";
import {
  api,
  setCsrfToken,
  type AuthStatus,
  type Identity,
  type Provider,
} from "../lib/contracts";

function safeNext(value: string | null) {
  if (
    !value ||
    !value.startsWith("/") ||
    value.startsWith("//") ||
    value.includes("\\")
  )
    return "/runs";
  try {
    const destination = new URL(value, window.location.origin);
    if (
      destination.origin !== window.location.origin ||
      destination.pathname === "/login" ||
      destination.pathname.startsWith("/login/")
    )
      return "/runs";
    return `${destination.pathname}${destination.search}${destination.hash}`;
  } catch {
    return "/runs";
  }
}
export default function Login() {
  const [provider, setProvider] = useState<Provider>("local");
  const [show, setShow] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [setupToken, setSetupToken] = useState("");
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const queryClient = useQueryClient();
  const status = useQuery({
    queryKey: ["auth-status"],
    queryFn: () => api<AuthStatus>("/auth/status"),
    retry: false,
  });
  const submit = useMutation({
    gcTime: 0,
    mutationFn: (body: Record<string, string>) =>
      api<Identity>(
        status.data?.bootstrap_required ? "/auth/bootstrap" : "/auth/login",
        { method: "POST", body: JSON.stringify(body) },
      ),
    onSuccess: async (identity) => {
      setCsrfToken(identity.csrf_token);
      await queryClient.cancelQueries();
      queryClient.clear();
      queryClient.setQueryData(["identity"], identity);
      setPassword("");
      setSetupToken("");
      submit.reset();
      navigate(safeNext(params.get("next")), { replace: true });
    },
    onError: () => {
      setPassword("");
      if (isBootstrap) setSetupToken("");
    },
  });
  const isBootstrap = status.data?.bootstrap_required === true;
  const providers = status.data?.providers ?? [];
  const loginProviders = providers.filter(
    (item) => item === "local" || item === "ldap",
  );
  const activeProvider =
    loginProviders.find((item) => item === provider) ??
    loginProviders[0] ??
    "local";
  return (
    <div className="login-page">
      <header className="topbar">
        <Brand />
      </header>
      <main className="login-main">
        <section className="login-card">
          <h1>{isBootstrap ? "Set up administrator" : "Sign in"}</h1>
          <p>Manage database instances, migrations and access.</p>
          {status.isPending ? (
            <div
              className="skeleton"
              role="status"
              aria-label="Checking setup"
            />
          ) : status.isError ? (
            <Notice tone="error">
              Unable to load authentication status: {status.error.message}{" "}
              <Button onClick={() => void status.refetch()}>Retry</Button>
            </Notice>
          ) : null}
          {!isBootstrap && loginProviders.length > 1 && (
            <div
              className="tabs login-tabs"
              role="tablist"
              aria-label="Sign-in method"
            >
              {loginProviders.map((p) => (
                <button
                  key={p}
                  role="tab"
                  aria-selected={activeProvider === p}
                  onClick={() => setProvider(p)}
                >
                  {p === "local" ? (
                    <LockKeyhole size={15} />
                  ) : (
                    <Building2 size={15} />
                  )}{" "}
                  {p === "local" ? "Local account" : "LDAP / Active Directory"}
                </button>
              ))}
            </div>
          )}
          {status.data && (isBootstrap || loginProviders.length > 0) && (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                submit.mutate(
                  isBootstrap
                    ? {
                        username,
                        display_name: displayName,
                        password,
                        setup_token: setupToken,
                      }
                    : { provider: activeProvider, username, password },
                );
              }}
            >
              {isBootstrap && (
                <>
                  <Notice title="One-time setup">
                    Create the first administrator. Read the setup token from
                    the file shown at server startup:{" "}
                    <code className="break-word">
                      {status.data.bootstrap_token_file ||
                        "path shown in server logs"}
                    </code>
                  </Notice>
                  <Field label="Setup token" required>
                    <input
                      required
                      type="password"
                      autoComplete="off"
                      value={setupToken}
                      onChange={(e) => setSetupToken(e.target.value)}
                    />
                  </Field>
                  <Field label="Display name" required>
                    <input
                      required
                      maxLength={120}
                      value={displayName}
                      onChange={(e) => setDisplayName(e.target.value)}
                    />
                  </Field>
                </>
              )}
              <Field label="Username" required>
                <input
                  required
                  autoComplete="username"
                  minLength={3}
                  maxLength={128}
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                />
              </Field>
              <Field label={isBootstrap ? "New password" : "Password"} required>
                <div className="password-field">
                  <input
                    required
                    type={show ? "text" : "password"}
                    autoComplete={
                      isBootstrap ? "new-password" : "current-password"
                    }
                    minLength={isBootstrap ? 12 : undefined}
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                  />
                  <Button
                    variant="ghost"
                    onClick={() => setShow(!show)}
                    type="button"
                    aria-label={show ? "Hide password" : "Show password"}
                  >
                    {show ? <EyeOff size={16} /> : <Eye size={16} />}
                  </Button>
                </div>
              </Field>
              {submit.isError && (
                <Notice tone="error">{submit.error.message}</Notice>
              )}
              <Button
                variant="primary"
                className="full"
                disabled={submit.isPending}
              >
                {submit.isPending
                  ? "Processing…"
                  : isBootstrap
                    ? "Create administrator"
                    : "Sign in"}{" "}
                <ArrowRight size={16} />
              </Button>
            </form>
          )}
          {status.data && !isBootstrap && providers.includes("oidc") && (
            <>
              <div className="divider">OR CONTINUE WITH</div>
              <Button
                className="full"
                onClick={() => {
                  window.location.assign("/api/v1/auth/oidc/start");
                }}
              >
                <ShieldCheck size={16} /> Sign in with SSO (OIDC)
              </Button>
            </>
          )}
          {status.data &&
            !isBootstrap &&
            loginProviders.length === 0 &&
            !providers.includes("oidc") && (
              <Notice tone="error">
                No sign-in provider is currently available.
              </Notice>
            )}
          <Notice title={isBootstrap ? "Session security" : "Secure sign-in"}>
            {isBootstrap
              ? "Use HTTPS for remote access. The password is stored only as a hash. The setup token is used only for initialization."
              : "Sessions are managed by the server. Credentials are never stored in browser web storage."}
          </Notice>
        </section>
      </main>
      <footer>Flyway Manager · Database migration management</footer>
    </div>
  );
}
