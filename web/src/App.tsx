import { Component, type ErrorInfo, type ReactNode } from "react";
import { BrowserRouter, Routes, Route, Navigate, Link } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Shell } from "./components/Shell";
import Login from "./pages/Login";
import History from "./pages/History";
import Migration, { Review } from "./pages/Migration";
import RunDetails from "./pages/RunDetails";
import Instances, {
  InstanceDetails,
  LegacyInstance,
  SchemaCatalog,
  Overview,
} from "./pages/Instances";

import Users from "./pages/Users";
import ServiceAccounts from "./pages/ServiceAccounts";
import Authentication from "./pages/Authentication";
import { ApiError } from "./lib/contracts";
import { SessionGate } from "./lib/identity";
import { PageHeading, Button, Notice } from "./components/ui";
const client = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (count, error) =>
        count < 1 && (!(error instanceof ApiError) || error.status >= 500),
    },
  },
});
class ErrorBoundary extends Component<
  { children: ReactNode },
  { error: boolean }
> {
  state = { error: false };
  static getDerivedStateFromError() {
    return { error: true };
  }
  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(
      "Console rendering failed",
      error.message,
      info.componentStack,
    );
  }
  render() {
    return this.state.error ? (
      <div className="fatal-error">
        <h1>Unable to display this page</h1>
        <Notice tone="error">Reload the page to recover the console.</Notice>
        <Button onClick={() => location.reload()}>Reload</Button>
      </div>
    ) : (
      this.props.children
    );
  }
}
export default function App() {
  return (
    <ErrorBoundary>
      <QueryClientProvider client={client}>
        <BrowserRouter>
          <Routes>
            <Route path="/login" element={<Login />} />
            <Route element={<SessionGate />}>
              <Route element={<Shell />}>
                <Route index element={<Navigate to="/home" replace />} />
                <Route path="/home" element={<Overview />} />
                <Route path="/instances" element={<Instances />} />
                <Route path="/instances/:id" element={<InstanceDetails />} />
                <Route
                  path="/instances/:id/:section"
                  element={<InstanceDetails />}
                />
                <Route path="/schemas" element={<SchemaCatalog />} />
                <Route path="/runs" element={<History />} />
                <Route path="/runs/new" element={<Migration />} />
                <Route path="/runs/new/review" element={<Review />} />
                <Route path="/runs/:id" element={<RunDetails />} />
                <Route
                  path="/databases"
                  element={<Navigate to="/instances" replace />}
                />
                <Route path="/databases/:id" element={<LegacyInstance />} />
                <Route
                  path="/execution-accounts"
                  element={<Navigate to="/instances" replace />}
                />
                <Route path="/access/users" element={<Users />} />
                <Route
                  path="/access/service-accounts"
                  element={<ServiceAccounts />}
                />
                <Route
                  path="/settings/authentication"
                  element={<Authentication />}
                />
                <Route
                  path="*"
                  element={
                    <>
                      <PageHeading title="Page not found" />
                      <p>This page does not exist.</p>
                      <Link to="/runs" className="button primary">
                        Open migration history
                      </Link>
                    </>
                  }
                />
              </Route>
            </Route>
          </Routes>
        </BrowserRouter>
      </QueryClientProvider>
    </ErrorBoundary>
  );
}
