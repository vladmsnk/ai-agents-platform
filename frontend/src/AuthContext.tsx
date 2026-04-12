import { createContext, useContext, useEffect, useState, useCallback, type ReactNode } from 'react';
import {
  login as authLogin,
  logout as authLogout,
  handleCallback,
  isAuthenticated,
  fetchUserInfo,
  getAccessToken,
  clearSession,
  type UserInfo,
} from './auth';

interface AuthContextType {
  user: UserInfo | null;
  loading: boolean;
  login: () => void;
  logout: () => void;
  getToken: () => Promise<string | null>;
}

const AuthContext = createContext<AuthContextType | null>(null);

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used within AuthProvider');
  return ctx;
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<UserInfo | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    async function init() {
      // Check if returning from Keycloak login
      const params = new URLSearchParams(window.location.search);
      if (params.has('code')) {
        const tokens = await handleCallback();
        if (tokens) {
          const info = await fetchUserInfo();
          setUser(info);
          setLoading(false);
          return;
        }
      }

      // Check existing session
      if (isAuthenticated()) {
        const info = await fetchUserInfo();
        if (info) {
          setUser(info);
        } else {
          clearSession();
        }
      }
      setLoading(false);
    }
    init();
  }, []);

  const login = useCallback(() => { authLogin().catch(console.error) }, []);
  const logout = useCallback(() => authLogout(), []);
  const getToken = useCallback(() => getAccessToken(), []);

  return (
    <AuthContext.Provider value={{ user, loading, login, logout, getToken }}>
      {children}
    </AuthContext.Provider>
  );
}
