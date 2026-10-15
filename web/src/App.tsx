import { Component, type ErrorInfo, type ReactNode } from "react";
import {
  BrowserRouter,
  Routes,
  Route,
  Navigate,
  Outlet,
  useLocation,
  Link,
} from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Shell } from "./components/Shell";
import { DraftProvider } from "./lib/draft";
import Login from "./pages/Login";
import History from "./pages/History";
import Migration, { Review } from "./pages/Migration";
import RunDetails from "./pages/RunDetails";
import Databases, { DatabaseDetails } from "./pages/Databases";
import Credentials from "./pages/Credentials";
import Users from "./pages/Users";
import ServiceAccounts from "./pages/ServiceAccounts";
import Authentication from "./pages/Authentication";
import { PageHeading, Button, Notice } from "./components/ui";
const client = new QueryClient();
function PreviewGate() {
  const location = useLocation();
  return sessionStorage.getItem("fm-preview") === "true" ? (
    <Outlet />
  ) : (
    <Navigate
      replace
      to={`/login?next=${encodeURIComponent(location.pathname + location.search)}`}
    />
  );
}
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
        <h1>Không thể hiển thị màn hình</h1>
        <Notice tone="error">
          Bản nháp chưa được lưu. Tải lại trang để khôi phục giao diện.
        </Notice>
        <Button onClick={() => location.reload()}>Tải lại</Button>
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
          <DraftProvider>
            <Routes>
              <Route path="/login" element={<Login />} />
              <Route element={<PreviewGate />}>
                <Route element={<Shell />}>
                  <Route index element={<Navigate to="/runs" replace />} />
                  <Route path="/runs" element={<History />} />
                  <Route path="/runs/new" element={<Migration />} />
                  <Route path="/runs/new/review" element={<Review />} />
                  <Route path="/runs/:id" element={<RunDetails />} />
                  <Route path="/databases" element={<Databases />} />
                  <Route path="/databases/:id" element={<DatabaseDetails />} />
                  <Route path="/execution-accounts" element={<Credentials />} />
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
                        <PageHeading title="Không tìm thấy trang" />
                        <p>Đường dẫn này không có trong console.</p>
                        <Link to="/runs" className="button primary">
                          Về Lịch sử chạy
                        </Link>
                      </>
                    }
                  />
                </Route>
              </Route>
            </Routes>
          </DraftProvider>
        </BrowserRouter>
      </QueryClientProvider>
    </ErrorBoundary>
  );
}
