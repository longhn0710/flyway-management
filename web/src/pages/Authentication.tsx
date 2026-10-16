import { useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LockKeyhole, Building2, ShieldCheck } from "lucide-react";
import { api, ApiError, type AuthConfiguration } from "../lib/contracts";
import { useIdentity } from "../lib/identity-context";
import {
  Panel,
  PageHeading,
  Button,
  Badge,
  Notice,
  Field,
  Check,
  Modal,
} from "../components/ui";

type Tab = "local" | "ldap" | "oidc";
export default function Authentication() {
  const identity = useIdentity();
  const admin = identity.user.role === "Admin";
  const [tab, setTab] = useState<Tab>("local");
  const queryClient = useQueryClient();
  const settings = useQuery({
    queryKey: ["authentication-settings"],
    queryFn: () => api<AuthConfiguration>("/settings/authentication"),
  });
  const [draft, setDraft] = useState<AuthConfiguration | null>(null);
  const [clientSecret, setClientSecret] = useState("");
  const [confirmSave, setConfirmSave] = useState(false);
  const config = draft ?? settings.data;
  const save = useMutation({
    mutationFn: () =>
      api<AuthConfiguration>("/settings/authentication", {
        method: "PUT",
        body: JSON.stringify({
          ...config,
          oidc: { ...config!.oidc, client_secret: clientSecret || undefined },
        }),
      }),
    onSuccess: (value) => {
      setDraft(null);
      setClientSecret("");
      queryClient.setQueryData(["authentication-settings"], value);
    },
  });
  const update = <K extends keyof AuthConfiguration>(
    key: K,
    values: AuthConfiguration[K],
  ) =>
    setDraft((current) => ({ ...(current ?? settings.data!), [key]: values }));
  if (settings.isPending)
    return (
      <>
        <PageHeading title="Authentication settings" />
        <div
          className="skeleton"
          role="status"
          aria-label="Loading authentication settings"
        />
      </>
    );
  if (settings.isError)
    return (
      <>
        <PageHeading title="Authentication settings" />
        <Notice tone="error">
          {settings.error instanceof ApiError && settings.error.status === 403
            ? "Access denied. Authentication configuration requires Admin."
            : settings.error.message}
          <Button onClick={() => void settings.refetch()}>Retry</Button>
        </Notice>
      </>
    );
  return (
    <>
      <PageHeading
        title="Authentication providers"
        description="Configure local login, verified LDAP/Active Directory and OIDC sign-in."
        breadcrumb="System settings / Authentication"
        actions={
          <Button
            variant="primary"
            disabled={!admin || save.isPending || !draft}
            onClick={() => setConfirmSave(true)}
          >
            {save.isPending ? "Saving…" : "Save configuration"}
          </Button>
        }
      />
      {!admin && (
        <Notice tone="warning">
          Read-only view. Authentication configuration requires Admin.
        </Notice>
      )}
      <p className="small muted">
        Need API tokens for automation?{" "}
        <Link to="/access/service-accounts">Manage Service Accounts →</Link>
      </p>
      <Notice tone="warning" title="Provider safety">
        Keep at least one working Admin sign-in method. External providers do
        not silently fall back to local login.
      </Notice>
      <div
        className="tabs auth-tabs"
        role="tablist"
        aria-label="Authentication providers"
      >
        {(
          [
            ["local", "Local", LockKeyhole],
            ["ldap", "LDAP / Active Directory", Building2],
            ["oidc", "OIDC / SSO", ShieldCheck],
          ] as const
        ).map(([key, label, Icon]) => (
          <button
            role="tab"
            aria-selected={tab === key}
            key={key}
            onClick={() => setTab(key)}
          >
            <Icon size={16} />
            {label}
          </button>
        ))}
      </div>
      {tab === "local" && (
        <Panel
          title={
            <>
              <LockKeyhole size={17} />
              Local authentication
            </>
          }
          aside={
            <Badge tone={config!.local_enabled ? "success" : "neutral"}>
              {config!.local_enabled ? "Enabled" : "Disabled"}
            </Badge>
          }
        >
          <Check
            label="Enable local authentication"
            checked={config!.local_enabled}
            disabled={!admin}
            onChange={(local_enabled) => update("local_enabled", local_enabled)}
          />
          <Notice>
            Passwords are stored as Argon2id hashes. Bootstrap and password
            changes are handled through identity management.
          </Notice>
          <p className="small muted">
            The backend rejects configurations that would remove the last usable
            Admin sign-in method.
          </p>
        </Panel>
      )}
      {tab === "ldap" && (
        <Panel
          title={
            <>
              <Building2 size={17} />
              LDAP / Active Directory
            </>
          }
          aside={
            <Badge tone={config!.ldap.enabled ? "success" : "neutral"}>
              {config!.ldap.enabled ? "Enabled" : "Disabled"}
            </Badge>
          }
        >
          <Check
            label="Enable LDAP sign-in"
            checked={config!.ldap.enabled}
            disabled={!admin}
            onChange={(enabled) => update("ldap", { ...config!.ldap, enabled })}
          />
          <Field
            label="Secure LDAP URL"
            required
            hint="Use LDAPS, or LDAP with StartTLS enabled. Certificate validation stays on."
          >
            <input
              required
              type="url"
              placeholder="ldaps://directory.example:636"
              value={config!.ldap.url}
              disabled={!admin}
              onChange={(event) =>
                update("ldap", { ...config!.ldap, url: event.target.value })
              }
            />
          </Field>
          <Check
            label="Require StartTLS"
            checked={config!.ldap.start_tls}
            disabled={!admin}
            onChange={(start_tls) =>
              update("ldap", { ...config!.ldap, start_tls })
            }
          />
          <div className="grid-two">
            <Field label="Base DN">
              <input
                value={config!.ldap.base_dn}
                disabled={!admin}
                onChange={(event) =>
                  update("ldap", {
                    ...config!.ldap,
                    base_dn: event.target.value,
                  })
                }
              />
            </Field>
            <Field label="User DN template" required>
              <input
                required
                value={config!.ldap.bind_dn_template}
                disabled={!admin}
                placeholder="uid={username},ou=Users,dc=example,dc=com"
                onChange={(event) =>
                  update("ldap", {
                    ...config!.ldap,
                    bind_dn_template: event.target.value,
                  })
                }
              />
            </Field>
          </div>
          <div className="grid-two">
            <Field
              label="Login username attribute"
              required
              hint="For AD, this may be sAMAccountName."
            >
              <input
                required
                value={config!.ldap.username_attribute}
                disabled={!admin}
                onChange={(event) =>
                  update("ldap", {
                    ...config!.ldap,
                    username_attribute: event.target.value,
                  })
                }
              />
            </Field>
            <Field label="Display name attribute">
              <input
                value={config!.ldap.display_name_attribute}
                disabled={!admin}
                onChange={(event) =>
                  update("ldap", {
                    ...config!.ldap,
                    display_name_attribute: event.target.value,
                  })
                }
              />
            </Field>
          </div>
          <div className="grid-two">
            <Field label="Immutable subject attribute" required>
              <input
                required
                value={config!.ldap.subject_attribute}
                disabled={!admin}
                onChange={(event) =>
                  update("ldap", {
                    ...config!.ldap,
                    subject_attribute: event.target.value,
                  })
                }
              />
            </Field>
            <Field label="Custom CA file (optional)">
              <input
                value={config!.ldap.ca_file}
                disabled={!admin}
                onChange={(event) =>
                  update("ldap", {
                    ...config!.ldap,
                    ca_file: event.target.value,
                  })
                }
              />
            </Field>
          </div>
          <Notice title="Direct user bind">
            User passwords are sent only during sign-in and are never saved in
            provider settings. External user binding requires a stable provider
            and subject.
          </Notice>
        </Panel>
      )}
      {tab === "oidc" && (
        <Panel
          title={
            <>
              <ShieldCheck size={17} />
              OpenID Connect
            </>
          }
          aside={
            <Badge tone={config!.oidc.enabled ? "success" : "neutral"}>
              {config!.oidc.enabled ? "Enabled" : "Disabled"}
            </Badge>
          }
        >
          <Check
            label="Enable OIDC sign-in"
            checked={config!.oidc.enabled}
            disabled={!admin}
            onChange={(enabled) => update("oidc", { ...config!.oidc, enabled })}
          />
          <Field label="Issuer URL" required>
            <input
              required
              type="url"
              value={config!.oidc.issuer}
              disabled={!admin}
              placeholder="https://identity.example"
              onChange={(event) =>
                update("oidc", { ...config!.oidc, issuer: event.target.value })
              }
            />
          </Field>
          <div className="grid-two">
            <Field label="Client ID" required>
              <input
                required
                value={config!.oidc.client_id}
                disabled={!admin}
                onChange={(event) =>
                  update("oidc", {
                    ...config!.oidc,
                    client_id: event.target.value,
                  })
                }
              />
            </Field>
            <Field
              label="Client secret (write only)"
              hint={
                config!.oidc.client_secret_configured
                  ? "A secret is configured. Leave empty to keep it."
                  : "Enter a secret to configure OIDC."
              }
            >
              <input
                type="password"
                autoComplete="new-password"
                value={clientSecret}
                disabled={!admin}
                onChange={(event) => {
                  setClientSecret(event.target.value);
                  setDraft((current) => current ?? settings.data!);
                }}
              />
            </Field>
          </div>
          <div className="grid-two">
            <Field label="Redirect URL" required>
              <input
                required
                type="url"
                value={config!.oidc.redirect_url}
                disabled={!admin}
                onChange={(event) =>
                  update("oidc", {
                    ...config!.oidc,
                    redirect_url: event.target.value,
                  })
                }
              />
            </Field>
            <Field label="Custom CA file (optional)">
              <input
                value={config!.oidc.ca_file}
                disabled={!admin}
                onChange={(event) =>
                  update("oidc", {
                    ...config!.oidc,
                    ca_file: event.target.value,
                  })
                }
              />
            </Field>
          </div>
          <Notice title="Authorization Code + PKCE">
            OIDC uses state, nonce, issuer, audience and token validation.
            External identities must be bound explicitly by issuer and subject;
            no automatic Admin grant.
          </Notice>
          <Badge>
            {config!.oidc.client_secret_configured
              ? "Client secret configured"
              : "Client secret not configured"}
          </Badge>
        </Panel>
      )}
      {save.isError && (
        <Notice tone="error">
          Could not save configuration: {save.error.message}
        </Notice>
      )}
      <Modal
        open={confirmSave}
        onOpenChange={setConfirmSave}
        title="Apply authentication changes?"
        description="Provider changes affect who can sign in to this installation."
      >
        <Notice tone="warning">
          Verify that an Admin can still sign in. Local authentication is being
          disabled only if another configured provider remains available.
        </Notice>
        {save.isError && <Notice tone="error">{save.error.message}</Notice>}
        <div className="dialog-actions">
          <Button onClick={() => setConfirmSave(false)}>Review settings</Button>
          <Button
            variant="danger"
            disabled={save.isPending}
            onClick={() => {
              setConfirmSave(false);
              save.mutate();
            }}
          >
            {save.isPending ? "Saving…" : "Apply changes"}
          </Button>
        </div>
      </Modal>
    </>
  );
}
