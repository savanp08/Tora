import type { ChatThread } from '$lib/types/chat';

const MS_EPOCH_THRESHOLD = 1_000_000_000_000;
export const MESSAGE_TEXT_MAX_BYTES = 4000;

export function getUTF8ByteLength(value: string) {
	if (!value) {
		return 0;
	}
	return new TextEncoder().encode(value).length;
}

export function parseTimestampParam(value: string | null) {
	if (!value) {
		return 0;
	}
	const numeric = Number(value);
	if (!Number.isFinite(numeric) || numeric <= 0) {
		return 0;
	}
	return normalizeEpoch(numeric);
}

export function normalizeRoomIDValue(value: string) {
	return value
		.toLowerCase()
		.trim()
		.replace(/[^a-z0-9]/g, '');
}

export function normalizeRoomNameValue(value: string) {
	const trimmed = value.trim();
	if (!trimmed) {
		return '';
	}
	return trimmed.replace(/\s+/g, ' ').slice(0, 20);
}

export function normalizeUsernameValue(value: string) {
	return value
		.trim()
		.replace(/[^a-zA-Z0-9\s_-]/g, '')
		.replace(/[\s-]+/g, '_')
		.replace(/_+/g, '_')
		.replace(/^_+|_+$/g, '');
}

export function normalizeIdentifier(value: string) {
	return value
		.trim()
		.replace(/[^a-zA-Z0-9\s_-]/g, '')
		.replace(/[\s-]+/g, '_')
		.replace(/_+/g, '_')
		.replace(/^_+|_+$/g, '');
}

export function normalizeMessageID(value: string) {
	return value.trim().replace(/[^a-zA-Z0-9_-]/g, '');
}

export function createMessageId(targetRoomId: string) {
	const cryptoCandidate = globalThis?.crypto;
	if (cryptoCandidate && typeof cryptoCandidate.randomUUID === 'function') {
		return cryptoCandidate.randomUUID();
	}
	return `m${targetRoomId}${Date.now().toString(36)}${Math.floor(Math.random() * 1_000_000).toString(36)}`;
}

export function formatRoomName(targetRoomId: string) {
	const trimmed = normalizeRoomIDValue(targetRoomId);
	if (!trimmed) {
		return 'Room';
	}
	return 'Room';
}

export function formatDateTime(timestamp: number) {
	if (!Number.isFinite(timestamp) || timestamp <= 0) {
		return 'Unknown';
	}
	return new Date(timestamp).toLocaleString([], {
		year: 'numeric',
		month: 'short',
		day: 'numeric',
		hour: '2-digit',
		minute: '2-digit'
	});
}

export function toTimestamp(value: unknown) {
	if (typeof value === 'number' && Number.isFinite(value)) {
		return normalizeEpoch(value);
	}
	if (typeof value === 'string') {
		const trimmed = value.trim();
		if (!trimmed) {
			return Date.now();
		}
		const asNumber = Number(trimmed);
		if (Number.isFinite(asNumber)) {
			return normalizeEpoch(asNumber);
		}
		const parsed = Date.parse(trimmed);
		if (Number.isFinite(parsed)) {
			return parsed;
		}
	}
	if (value instanceof Date) {
		return value.getTime();
	}
	return Date.now();
}

export function parseOptionalTimestamp(value: unknown) {
	if (value === null || value === undefined) {
		return 0;
	}
	if (typeof value === 'number') {
		if (!Number.isFinite(value) || value <= 0) {
			return 0;
		}
		return normalizeEpoch(value);
	}
	if (typeof value === 'string') {
		const trimmed = value.trim();
		if (!trimmed) {
			return 0;
		}
		const numeric = Number(trimmed);
		if (Number.isFinite(numeric) && numeric > 0) {
			return normalizeEpoch(numeric);
		}
		const parsed = Date.parse(trimmed);
		if (Number.isFinite(parsed) && parsed > 0) {
			return parsed;
		}
		return 0;
	}
	if (value instanceof Date) {
		return value.getTime();
	}
	return 0;
}

export function normalizeEpoch(value: number) {
	if (value > 0 && value < MS_EPOCH_THRESHOLD) {
		return value * 1000;
	}
	return value;
}

export function toBool(value: unknown) {
	if (typeof value === 'boolean') {
		return value;
	}
	if (typeof value === 'string') {
		const lower = value.toLowerCase();
		return lower === '1' || lower === 'true';
	}
	if (typeof value === 'number') {
		return value === 1;
	}
	return false;
}

export function toInt(value: unknown) {
	if (typeof value === 'number' && Number.isFinite(value)) {
		return Math.trunc(value);
	}
	if (typeof value === 'string') {
		const parsed = Number(value);
		if (Number.isFinite(parsed)) {
			return Math.trunc(parsed);
		}
	}
	return 0;
}

export function toStringValue(value: unknown) {
	if (typeof value === 'string') {
		return value;
	}
	if (typeof value === 'number' || typeof value === 'boolean') {
		return String(value);
	}
	return '';
}

export function isMediaMessageType(value: string) {
	const normalized = value.trim().toLowerCase();
	return (
		normalized === 'image' ||
		normalized === 'video' ||
		normalized === 'file' ||
		normalized === 'audio'
	);
}

export function isLikelyMediaURL(value: string) {
	const trimmed = value.trim();
	return (
		trimmed.startsWith('http://') ||
		trimmed.startsWith('https://') ||
		trimmed.startsWith('blob:') ||
		trimmed.startsWith('data:') ||
		trimmed.startsWith('/')
	);
}

export function toAbsoluteMediaURL(value: string, apiBase: string) {
	const trimmed = value.trim();
	if (!trimmed) {
		return '';
	}
	if (trimmed.startsWith('blob:') || trimmed.startsWith('data:')) {
		return trimmed;
	}
	if (/^https?:\/\//i.test(trimmed)) {
		try {
			const parsed = new URL(trimmed);
			if (parsed.hostname.endsWith('.r2.cloudflarestorage.com')) {
				const pathParts = parsed.pathname.split('/').filter(Boolean);
				if (pathParts.length >= 2) {
					const objectKey = decodeIfNeeded(pathParts.slice(1).join('/'));
					return `${apiBase}/api/upload/object/${encodeURIComponent(objectKey)}`;
				}
			}
		} catch {
			return trimmed;
		}
		return trimmed;
	}
	if (trimmed.startsWith('/')) {
		return `${apiBase}${trimmed}`;
	}
	return `${apiBase}/${trimmed}`;
}

export function resolveRoomMembership(roomID: string, threads: ChatThread[], memberHint: string | null) {
	if (!roomID) {
		return true;
	}
	if (memberHint === '0') {
		return false;
	}
	if (memberHint === '1') {
		return true;
	}
	const thread = threads.find((entry) => entry.id === roomID);
	if (!thread) {
		return false;
	}
	return thread.status === 'joined';
}

function decodeIfNeeded(value: string) {
	try {
		return decodeURIComponent(value);
	} catch {
		return value;
	}
}
