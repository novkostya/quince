import { createBrowserRouter, Navigate } from "react-router-dom";
import { AppLayout } from "./AppLayout";
import { LoginGate, RequireAuth, SetupGate } from "./guards";
import { SetupPasswordPage } from "@/pages/SetupPasswordPage";
import { LoginPage } from "@/pages/LoginPage";
import { DashboardPage } from "@/pages/DashboardPage";
import { DeviceDetailsPage } from "@/pages/DeviceDetailsPage";
import { SettingsPage } from "@/pages/SettingsPage";

export const router = createBrowserRouter([
  {
    path: "/setup",
    element: (
      <SetupGate>
        <SetupPasswordPage />
      </SetupGate>
    ),
  },
  {
    path: "/login",
    element: (
      <LoginGate>
        <LoginPage />
      </LoginGate>
    ),
  },
  {
    element: (
      <RequireAuth>
        <AppLayout />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <Navigate to="/devices" replace /> },
      { path: "devices", element: <DashboardPage /> },
      { path: "devices/:udid", element: <DeviceDetailsPage /> },
      { path: "settings", element: <SettingsPage /> },
    ],
  },
  { path: "*", element: <Navigate to="/" replace /> },
]);
