import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Routes, Route, Navigate, useLocation } from "react-router-dom";
import type { ReactNode } from "react";
import { AuthProvider, useAuth } from "./hooks/useAuth";
import { useLiveEvents } from "./hooks/useLiveEvents";
import { UploadQueueProvider } from "./hooks/useUploadQueue";
import { Layout } from "./components/Layout";
import { MusicPlayerBar } from "./components/MusicPlayerBar";
import { LoginPage } from "./pages/LoginPage";
import { HomePage } from "./pages/HomePage";
import { MovieDetailPage } from "./pages/MovieDetailPage";
import { MusicHomePage } from "./pages/MusicHomePage";
import { ArtistPage } from "./pages/ArtistPage";
import { AlbumPage } from "./pages/AlbumPage";
import { AdminMoviesPage } from "./pages/AdminMoviesPage";
import { AdminActivityPage } from "./pages/AdminActivityPage";
import { AdminDevicesPage } from "./pages/AdminDevicesPage";
import { AdminInfoPage } from "./pages/AdminInfoPage";

const queryClient = new QueryClient();

function ProtectedRoute({ children }: { children: ReactNode }) {
  const { isAuthenticated } = useAuth();
  const location = useLocation();

  if (!isAuthenticated) {
    return <Navigate to="/login" state={{ from: location.pathname }} replace />;
  }
  return <>{children}</>;
}

function AppRoutes() {
  useLiveEvents();
  const { isAuthenticated } = useAuth();

  return (
    <>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route
          path="/admin/movies"
          element={<ProtectedRoute><Layout><AdminMoviesPage /></Layout></ProtectedRoute>}
        />
        <Route
          path="/admin/activity"
          element={<ProtectedRoute><Layout><AdminActivityPage /></Layout></ProtectedRoute>}
        />
        <Route
          path="/admin/devices"
          element={<ProtectedRoute><Layout><AdminDevicesPage /></Layout></ProtectedRoute>}
        />
        <Route
          path="/admin/info"
          element={<ProtectedRoute><Layout><AdminInfoPage /></Layout></ProtectedRoute>}
        />
        <Route
          path="/"
          element={
            <ProtectedRoute>
              <Layout>
                <HomePage />
              </Layout>
            </ProtectedRoute>
          }
        />
        <Route
          path="/movies/:id"
          element={
            <ProtectedRoute>
              <Layout>
                <MovieDetailPage />
              </Layout>
            </ProtectedRoute>
          }
        />
        <Route
          path="/music"
          element={
            <ProtectedRoute>
              <Layout>
                <MusicHomePage />
              </Layout>
            </ProtectedRoute>
          }
        />
        <Route
          path="/music/artists/:id"
          element={
            <ProtectedRoute>
              <Layout>
                <ArtistPage />
              </Layout>
            </ProtectedRoute>
          }
        />
        <Route
          path="/music/albums/:id"
          element={
            <ProtectedRoute>
              <Layout>
                <AlbumPage />
              </Layout>
            </ProtectedRoute>
          }
        />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>

      {/* Mounted as a sibling of <Routes>, not inside Layout, so the single
          <audio> element survives route changes instead of being torn down
          and recreated on every navigation. */}
      {isAuthenticated && <MusicPlayerBar />}
    </>
  );
}

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <UploadQueueProvider>
          <BrowserRouter>
            <AppRoutes />
          </BrowserRouter>
        </UploadQueueProvider>
      </AuthProvider>
    </QueryClientProvider>
  );
}
