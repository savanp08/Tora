import { toStringValue } from '$lib/utils/chat/core';

export function normalizeRoomPasswordValue(value: string) {
	return (value || '').trim().slice(0, 32);
}

export function normalizeRoomAccessPasswordValue(value: string) {
	return (value || '').trim().slice(0, 64);
}

export function normalizeAdminCodeValue(value: unknown) {
	return toStringValue(value).trim().toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 4);
}

export function buildRoomPasswordHash(password: string) {
	const normalizedPassword = normalizeRoomPasswordValue(password);
	return normalizedPassword ? `#key=${encodeURIComponent(normalizedPassword)}` : '';
}
