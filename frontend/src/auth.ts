// OAuth2 Authorization Code + PKCE flow for Keycloak
// No external dependencies — uses Web Crypto API

const KEYCLOAK_URL = import.meta.env.VITE_KEYCLOAK_URL || 'http://localhost:8180';
const KEYCLOAK_REALM = import.meta.env.VITE_KEYCLOAK_REALM || 'agents';
const CLIENT_ID = import.meta.env.VITE_KEYCLOAK_CLIENT_ID || 'frontend-admin';
const REDIRECT_URI = `${window.location.origin}/`;

const REALM_BASE = `${KEYCLOAK_URL}/realms/${KEYCLOAK_REALM}/protocol/openid-connect`;
const AUTH_ENDPOINT = `${REALM_BASE}/auth`;
export const TOKEN_ENDPOINT = `${REALM_BASE}/token`;
const LOGOUT_ENDPOINT = `${REALM_BASE}/logout`;
const USERINFO_ENDPOINT = `${REALM_BASE}/userinfo`;

const STORAGE_KEY = 'auth_tokens';
const VERIFIER_KEY = 'pkce_verifier';

export interface AuthTokens {
  access_token: string;
  refresh_token: string;
  expires_at: number; // unix ms
  refresh_expires_at: number;
}

export interface UserInfo {
  sub: string;
  preferred_username: string;
  email?: string;
  name?: string;
  given_name?: string;
  family_name?: string;
  realm_access?: { roles: string[] };
}

// --- PKCE helpers ---

function randomString(length: number): string {
  const array = new Uint8Array(length);
  crypto.getRandomValues(array);
  return Array.from(array, (b) => b.toString(36).padStart(2, '0')).join('').slice(0, length);
}

async function sha256(plain: string): Promise<ArrayBuffer> {
  return crypto.subtle.digest('SHA-256', new TextEncoder().encode(plain));
}

function base64UrlEncode(buf: ArrayBuffer): string {
  return btoa(String.fromCharCode(...new Uint8Array(buf)))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '');
}

// --- Token storage ---

function saveTokens(tokens: AuthTokens) {
  sessionStorage.setItem(STORAGE_KEY, JSON.stringify(tokens));
}

function loadTokens(): AuthTokens | null {
  const raw = sessionStorage.getItem(STORAGE_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

function clearTokens() {
  sessionStorage.removeItem(STORAGE_KEY);
  sessionStorage.removeItem(VERIFIER_KEY);
}

export { clearTokens as clearSession };

// --- Public API ---

export async function login() {
  const verifier = randomString(64);
  sessionStorage.setItem(VERIFIER_KEY, verifier);

  const challenge = base64UrlEncode(await sha256(verifier));

  const params = new URLSearchParams({
    client_id: CLIENT_ID,
    response_type: 'code',
    redirect_uri: REDIRECT_URI,
    scope: 'openid',
    code_challenge: challenge,
    code_challenge_method: 'S256',
  });

  window.location.href = `${AUTH_ENDPOINT}?${params}`;
}

export async function handleCallback(): Promise<AuthTokens | null> {
  const params = new URLSearchParams(window.location.search);
  const code = params.get('code');
  if (!code) return null;

  const verifier = sessionStorage.getItem(VERIFIER_KEY);
  if (!verifier) return null;

  // Clean up URL
  window.history.replaceState({}, '', window.location.pathname);

  const body = new URLSearchParams({
    grant_type: 'authorization_code',
    client_id: CLIENT_ID,
    code,
    redirect_uri: REDIRECT_URI,
    code_verifier: verifier,
  });

  const res = await fetch(TOKEN_ENDPOINT, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body,
  });

  if (!res.ok) {
    console.error('Token exchange failed:', await res.text());
    clearTokens();
    return null;
  }

  const data = await res.json();
  const now = Date.now();
  const tokens: AuthTokens = {
    access_token: data.access_token,
    refresh_token: data.refresh_token,
    expires_at: now + data.expires_in * 1000,
    refresh_expires_at: now + (data.refresh_expires_in || 1800) * 1000,
  };

  saveTokens(tokens);
  sessionStorage.removeItem(VERIFIER_KEY);
  return tokens;
}

export async function refreshAccessToken(): Promise<AuthTokens | null> {
  const tokens = loadTokens();
  if (!tokens?.refresh_token) return null;

  if (Date.now() > tokens.refresh_expires_at) {
    clearTokens();
    return null;
  }

  const body = new URLSearchParams({
    grant_type: 'refresh_token',
    client_id: CLIENT_ID,
    refresh_token: tokens.refresh_token,
  });

  const res = await fetch(TOKEN_ENDPOINT, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body,
  });

  if (!res.ok) {
    clearTokens();
    return null;
  }

  const data = await res.json();
  const now = Date.now();
  const updated: AuthTokens = {
    access_token: data.access_token,
    refresh_token: data.refresh_token,
    expires_at: now + data.expires_in * 1000,
    refresh_expires_at: now + (data.refresh_expires_in || 1800) * 1000,
  };

  saveTokens(updated);
  return updated;
}

let refreshPromise: Promise<AuthTokens | null> | null = null;

export async function getAccessToken(): Promise<string | null> {
  let tokens = loadTokens();
  if (!tokens) return null;

  // Refresh if expiring within 30s, deduplicating concurrent calls
  if (Date.now() > tokens.expires_at - 30_000) {
    if (!refreshPromise) {
      refreshPromise = refreshAccessToken().finally(() => { refreshPromise = null; });
    }
    tokens = await refreshPromise;
    if (!tokens) return null;
  }

  return tokens.access_token;
}

export function isAuthenticated(): boolean {
  const tokens = loadTokens();
  if (!tokens) return false;
  // Consider authenticated if refresh token hasn't expired
  return Date.now() < tokens.refresh_expires_at;
}

export async function fetchUserInfo(): Promise<UserInfo | null> {
  const token = await getAccessToken();
  if (!token) return null;

  const res = await fetch(USERINFO_ENDPOINT, {
    headers: { Authorization: `Bearer ${token}` },
  });

  if (!res.ok) return null;
  return res.json();
}

export function logout() {
  const tokens = loadTokens();
  clearTokens();

  const params = new URLSearchParams({
    client_id: CLIENT_ID,
    post_logout_redirect_uri: REDIRECT_URI,
  });
  if (tokens?.refresh_token) {
    params.set('refresh_token', tokens.refresh_token);
  }

  window.location.href = `${LOGOUT_ENDPOINT}?${params}`;
}
