import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { apiFetch, getToken, setToken } from "../api/client";

interface AuthContextValue {
  isAuthenticated: boolean;
  /** True while checking whether the home network lets us in without a password. */
  isChecking: boolean;
  login: (password: string) => Promise<void>;
  logout: () => void;
}

const AuthContext = createContext<AuthContextValue | null>(null);

interface AuthenticateByNameResponse {
  AccessToken: string;
  ServerId: string;
  User: { Id: string; Name: string };
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [isAuthenticated, setIsAuthenticated] = useState(() => Boolean(getToken()));
  const [isChecking, setIsChecking] = useState(() => !getToken());

  // On the home network the server hands out a home-only token without a
  // password; through the public address this 403s and the login page shows.
  useEffect(() => {
    if (!isChecking) return;
    apiFetch<{ AccessToken: string }>("/api/auth/lan", { method: "POST" })
      .then((res) => {
        setToken(res.AccessToken);
        setIsAuthenticated(true);
      })
      .catch(() => {})
      .finally(() => setIsChecking(false));
  }, [isChecking]);

  async function login(password: string) {
    const res = await apiFetch<AuthenticateByNameResponse>("/Users/AuthenticateByName", {
      method: "POST",
      body: JSON.stringify({ Username: "magicboxie-web", Pw: password }),
    });
    setToken(res.AccessToken);
    setIsAuthenticated(true);
  }

  function logout() {
    setToken(null);
    setIsAuthenticated(false);
  }

  return (
    <AuthContext.Provider value={{ isAuthenticated, isChecking, login, logout }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
