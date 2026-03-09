import { APP_LIMITS } from '$lib/config/limits';

export type JoinMode = 'create' | 'join';

const ROOM_NAME_MAX_LENGTH = APP_LIMITS.room.nameMaxLength;
const ROOM_CODE_DIGITS = APP_LIMITS.room.codeDigits;

export function normalizeRoomNameInput(value: string) {
	const trimmed = value.trim();
	if (!trimmed) {
		return '';
	}
	return trimmed.replace(/\s+/g, '_').replace(/_+/g, '_').slice(0, ROOM_NAME_MAX_LENGTH);
}

export function normalizeRoomIdValue(value: string) {
	return value
		.toLowerCase()
		.trim()
		.replace(/[^a-z0-9]/g, '');
}

export function normalizeUsernameInput(value: string) {
	return value
		.trim()
		.replace(/[^a-zA-Z0-9\s_-]/g, '')
		.replace(/[\s-]+/g, '_')
		.replace(/_+/g, '_')
		.replace(/^_+|_+$/g, '');
}

export function normalizeRoomCodeInput(value: string) {
	const digitsOnly = value.replace(/\D+/g, '');
	if (digitsOnly.length !== ROOM_CODE_DIGITS) {
		return '';
	}
	return digitsOnly;
}

export function sanitizeRoomCodePartial(value: string) {
	return value.replace(/\D+/g, '').slice(0, ROOM_CODE_DIGITS);
}
