import { browser } from '$app/environment';
import { writable } from 'svelte/store';

export interface User {
	id: string;
	email: string;
	name: string;
	avatarUrl: string;
	role: 'admin' | 'member' | 'viewer';
	tier?: 'free' | 'plus' | 'pro' | 'team';
}

export interface AuthState {
	isAuthenticated: boolean;
	user: User | null;
	token: string | null;
}

const AUTH_TOKEN_STORAGE_KEY = 'converse.auth.token';
const AUTH_USER_STORAGE_KEY = 'converse.auth.user';
const AUTH_COOKIE_KEY = 'tora_auth';
const LEGACY_AUTH_COOKIE_KEY = 'converse_auth_token';
const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';

const initialState: AuthState = {
	isAuthenticated: false,
	user: null,
	token: null
};

export const authState = writable<AuthState>(initialState);

// Server-resolved tier for this browser tab. Updated on every WebSocket connect
// via session_info. Scoped to the tab (sessionStorage) so multiple tabs with
// different rooms/users don't overwrite each other.
export type UserTier = 'free' | 'plus' | 'pro' | 'team';
const SESSION_TIER_KEY = 'converse.session.tier';
function readSessionTier(): UserTier {
	if (!browser) return 'free';
	try {
		const raw = sessionStorage.getItem(SESSION_TIER_KEY)?.trim().toLowerCase();
		if (raw === 'plus' || raw === 'pro' || raw === 'team') return raw;
	} catch { /* ignore */ }
	return 'free';
}
export const sessionTier = writable<UserTier>(readSessionTier());
export function applyServerTier(tier: UserTier) {
	sessionTier.set(tier);
	try { sessionStorage.setItem(SESSION_TIER_KEY, tier); } catch { /* ignore */ }
	// Also patch the user object if logged in so the tier persists on full reload
	// via login-time storage (localStorage is intentional here — it's the auth
	// record, not the per-tab value; the sessionStorage copy above is authoritative
	// during the tab's lifetime).
	authState.update((s) => {
		if (!s.user) return s;
		return { ...s, user: { ...s.user, tier } };
	});
}

function readCookieToken() {
	if (!browser) {
		return null;
	}
	const allCookies = document.cookie.split(';').map((entry) => entry.trim());
	const cookieKeys = [AUTH_COOKIE_KEY, LEGACY_AUTH_COOKIE_KEY];
	for (const key of cookieKeys) {
		const cookie = allCookies.find((entry) => entry.startsWith(`${key}=`));
		if (!cookie) {
			continue;
		}
		const value = cookie.split('=').slice(1).join('=').trim();
		if (!value) {
			continue;
		}
		try {
			return decodeURIComponent(value);
		} catch {
			return value;
		}
	}
	return null;
}

function writeCookieToken(token: string | null) {
	if (!browser) {
		return;
	}
	if (!token) {
		document.cookie = `${AUTH_COOKIE_KEY}=; Max-Age=0; Path=/; SameSite=Lax`;
		document.cookie = `${LEGACY_AUTH_COOKIE_KEY}=; Max-Age=0; Path=/; SameSite=Lax`;
		return;
	}
	document.cookie = `${AUTH_COOKIE_KEY}=${encodeURIComponent(token)}; Path=/; SameSite=Lax`;
	document.cookie = `${LEGACY_AUTH_COOKIE_KEY}=; Max-Age=0; Path=/; SameSite=Lax`;
}

function parseStoredUser(raw: string | null): User | null {
	if (!raw) {
		return null;
	}
	try {
		const parsed = JSON.parse(raw) as Partial<User>;
		const id = typeof parsed.id === 'string' ? parsed.id.trim() : '';
		const email = typeof parsed.email === 'string' ? parsed.email.trim() : '';
		const name = typeof parsed.name === 'string' ? parsed.name.trim() : '';
		const avatarUrl = typeof parsed.avatarUrl === 'string' ? parsed.avatarUrl.trim() : '';
		const role = parsed.role === 'admin' || parsed.role === 'viewer' ? parsed.role : 'member';
		const validTiers = ['free', 'plus', 'pro', 'team'] as const;
		const tier = validTiers.includes(parsed.tier as (typeof validTiers)[number])
			? (parsed.tier as User['tier'])
			: undefined;
		if (!id || !email || !name) {
			return null;
		}
		return { id, email, name, avatarUrl, role, tier };
	} catch {
		return null;
	}
}

export function login(token: string, user: User) {
	const normalizedToken = token.trim();
	if (!normalizedToken) {
		return;
	}
	authState.set({
		isAuthenticated: true,
		token: normalizedToken,
		user
	});
	if (!browser) {
		return;
	}
	window.localStorage.setItem(AUTH_TOKEN_STORAGE_KEY, normalizedToken);
	window.localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify(user));
	writeCookieToken(normalizedToken);
}

export async function logout() {
	if (browser) {
		try {
			await fetch(`${API_BASE}/api/auth/logout`, {
				method: 'POST',
				credentials: 'include'
			});
		} catch {
			// Ignore network errors and still clear local auth state.
		}
	}

	authState.set(initialState);
	if (!browser) {
		return;
	}
	window.localStorage.removeItem(AUTH_TOKEN_STORAGE_KEY);
	window.localStorage.removeItem(AUTH_USER_STORAGE_KEY);
	writeCookieToken(null);
}

export function initializeAuth() {
	if (!browser) {
		return;
	}
	const storageToken = window.localStorage.getItem(AUTH_TOKEN_STORAGE_KEY);
	const cookieToken = readCookieToken();
	const token = (storageToken && storageToken.trim()) || (cookieToken && cookieToken.trim()) || null;
	if (!token) {
		authState.set(initialState);
		return;
	}

	const user = parseStoredUser(window.localStorage.getItem(AUTH_USER_STORAGE_KEY));
	authState.set({
		isAuthenticated: true,
		token,
		user
	});

	// Keep storage and cookie in sync for downstream route middleware and reloads.
	window.localStorage.setItem(AUTH_TOKEN_STORAGE_KEY, token);
	writeCookieToken(token);

	// If the stored user has no tier (logged in before tier support, or OAuth),
	// fetch the resolved tier from the server and patch the store.
	if (user && !user.tier) {
		fetch(`${API_BASE}/api/auth/me`, {
			headers: { Authorization: `Bearer ${token}` },
			credentials: 'include'
		})
			.then((r) => (r.ok ? r.json() : null))
			.then((data: { tier?: string } | null) => {
				const validTiers = ['free', 'plus', 'pro', 'team'] as const;
				const raw = data?.tier?.trim().toLowerCase();
				const tier = validTiers.includes(raw as (typeof validTiers)[number])
					? (raw as User['tier'])
					: undefined;
				if (!tier) return;
				const patched: User = { ...user, tier };
				authState.update((s) => ({ ...s, user: patched }));
				window.localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify(patched));
			})
			.catch(() => {/* non-critical, ignore */});
	}
}

if (browser) {
	initializeAuth();
}
