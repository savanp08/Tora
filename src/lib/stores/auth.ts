import { browser } from '$app/environment';
import { writable } from 'svelte/store';

export interface User {
	id: string;
	email: string;
	name: string;
	avatarUrl: string;
	role: 'admin' | 'member' | 'viewer';
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
		if (!id || !email || !name) {
			return null;
		}
		return { id, email, name, avatarUrl, role };
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
}

if (browser) {
	initializeAuth();
}
