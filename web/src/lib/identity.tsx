import { useEffect, type ReactNode } from "react";
import { Navigate, Outlet, useLocation, useNavigate } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  api,
  setCsrfToken,
  setUnauthorizedHandler,
  type Identity,
} from "./contracts";
import { IdentityContext } from "./identity-context";

export function SessionGate({ children }: { children?: ReactNode }) {
  const queryClient = useQueryClient();
  const location = useLocation();
  const navigate = useNavigate();
  const me = useQuery({
    queryKey: ["identity"],
    queryFn: async ({ signal }) => {
      const identity = await api<Identity>("/auth/me", { signal });
      setCsrfToken(identity.csrf_token);
      return identity;
    },
    retry: false,
  });
  useEffect(() => {
    setUnauthorizedHandler(() => {
      setCsrfToken("");
      void queryClient.cancelQueries();
      queryClient.clear();
      const next = location.pathname + location.search;
      navigate(`/login?next=${encodeURIComponent(next)}`, { replace: true });
    });
    return () => setUnauthorizedHandler(undefined);
  }, [location.pathname, location.search, navigate, queryClient]);
  if (me.isPending)
    return (
      <div className="page-loading" role="status">
        Verifying session…
      </div>
    );
  if (me.isError) {
    const status = (me.error as { status?: number }).status;
    if (status === 401)
      return (
        <Navigate
          replace
          to={`/login?next=${encodeURIComponent(location.pathname + location.search)}`}
        />
      );
    return (
      <div className="page-loading">
        <p role="alert">
          Unable to verify your session.{" "}
          {me.error instanceof Error ? me.error.message : "Please try again."}
        </p>
        <button className="button secondary" onClick={() => void me.refetch()}>
          Retry
        </button>
      </div>
    );
  }
  return (
    <IdentityContext.Provider value={me.data}>
      {children ?? <Outlet />}
    </IdentityContext.Provider>
  );
}
