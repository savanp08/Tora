export type JoinMode = 'create' | 'join';

export function normalizeRoomNameInput(value: string) {
	const trimmed = value.trim();
	if (!trimmed) {
		return '';
	}
	return trimmed.replace(/\s+/g, '_').replace(/_+/g, '_').slice(0, 20);
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
	if (digitsOnly.length !== 6) {
		return '';
	}
	return digitsOnly;
}

export function sanitizeRoomCodePartial(value: string) {
	return value.replace(/\D+/g, '').slice(0, 6);
}
