import { browser } from '$app/environment';

export const SESSION_TOKEN_STORAGE_KEY = 'converse_session_token';

export function setSessionToken(token: string) {
	if (!browser) {
		return;
	}
	const normalized = (token || '').trim();
	if (!normalized) {
		return;
	}
	window.sessionStorage.setItem(SESSION_TOKEN_STORAGE_KEY, normalized);
}

export function getSessionToken() {
	if (!browser) {
		return '';
	}
	return (window.sessionStorage.getItem(SESSION_TOKEN_STORAGE_KEY) || '').trim();
}

export function clearSessionToken() {
	if (!browser) {
		return;
	}
	window.sessionStorage.removeItem(SESSION_TOKEN_STORAGE_KEY);
}
