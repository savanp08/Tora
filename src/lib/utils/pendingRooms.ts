import { getSessionToken } from '$lib/utils/sessionToken';

const PENDING_ROOMS_STORAGE_KEY = 'tora_pending_rooms';
const MAX_PENDING_ROOM_AGE_MS = 7 * 24 * 60 * 60 * 1000;

export interface PendingRoom {
	roomId: string;
	roomName: string;
	sessionToken: string;
	capturedAt: number;
	isEphemeral: true;
}

function isBrowser() {
	return typeof window !== 'undefined';
}

function isValidPendingRoom(value: unknown): value is PendingRoom {
	if (!value || typeof value !== 'object') {
		return false;
	}

	const candidate = value as Record<string, unknown>;
	return (
		typeof candidate.roomId === 'string' &&
		candidate.roomId.trim() !== '' &&
		typeof candidate.roomName === 'string' &&
		typeof candidate.sessionToken === 'string' &&
		typeof candidate.capturedAt === 'number' &&
		Number.isFinite(candidate.capturedAt) &&
		candidate.isEphemeral === true
	);
}

function prunePendingRooms(rooms: PendingRoom[]) {
	const cutoff = Date.now() - MAX_PENDING_ROOM_AGE_MS;
	return rooms.filter((room) => room.capturedAt >= cutoff);
}

function readPendingRooms() {
	if (!isBrowser()) {
		return [] as PendingRoom[];
	}

	try {
		const rawValue = window.localStorage.getItem(PENDING_ROOMS_STORAGE_KEY);
		if (!rawValue) {
			return [];
		}

		const parsed = JSON.parse(rawValue);
		if (!Array.isArray(parsed)) {
			return [];
		}

		return prunePendingRooms(parsed.filter(isValidPendingRoom));
	} catch {
		return [];
	}
}

function writePendingRooms(rooms: PendingRoom[]) {
	if (!isBrowser()) {
		return;
	}

	try {
		const nextRooms = prunePendingRooms(rooms);
		if (nextRooms.length === 0) {
			window.localStorage.removeItem(PENDING_ROOMS_STORAGE_KEY);
			return;
		}
		window.localStorage.setItem(PENDING_ROOMS_STORAGE_KEY, JSON.stringify(nextRooms));
	} catch {
		// Ignore localStorage write failures and preserve the current flow.
	}
}

export function captureCurrentRoom(roomId: string, roomName: string): void {
	if (!isBrowser()) {
		return;
	}

	const normalizedRoomId = (roomId || '').trim();
	if (!normalizedRoomId) {
		return;
	}

	const existingRooms = readPendingRooms();
	if (existingRooms.some((room) => room.roomId === normalizedRoomId)) {
		writePendingRooms(existingRooms);
		return;
	}

	existingRooms.push({
		roomId: normalizedRoomId,
		roomName: (roomName || '').trim() || normalizedRoomId,
		sessionToken: getSessionToken(),
		capturedAt: Date.now(),
		isEphemeral: true
	});
	writePendingRooms(existingRooms);
}

export function getPendingRooms(): PendingRoom[] {
	return readPendingRooms();
}

export function clearPendingRooms(): void {
	if (!isBrowser()) {
		return;
	}

	try {
		window.localStorage.removeItem(PENDING_ROOMS_STORAGE_KEY);
	} catch {
		// Ignore localStorage failures to keep the client flow resilient.
	}
}

export function removePendingRoom(roomId: string): void {
	if (!isBrowser()) {
		return;
	}

	const normalizedRoomId = (roomId || '').trim();
	if (!normalizedRoomId) {
		return;
	}

	const remainingRooms = readPendingRooms().filter((room) => room.roomId !== normalizedRoomId);
	writePendingRooms(remainingRooms);
}
