import { browser } from '$app/environment';

const LOCAL_BACKEND_PORT = '8080';

export function resolveApiBase(configuredBase: string | undefined) {
	const configured = (configuredBase ?? '').trim();
	if (configured) {
		return configured;
	}
	if (!browser) {
		return `http://localhost:${LOCAL_BACKEND_PORT}`;
	}
	const protocol = window.location.protocol === 'https:' ? 'https:' : 'http:';
	const host = window.location.hostname;
	if (import.meta.env.DEV) {
		return `${protocol}//${host}:${LOCAL_BACKEND_PORT}`;
	}
	if (host === 'localhost' || host === '127.0.0.1') {
		return `${protocol}//${host}:${LOCAL_BACKEND_PORT}`;
	}
	return window.location.origin;
}
