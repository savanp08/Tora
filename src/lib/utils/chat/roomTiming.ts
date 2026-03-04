import type { RoomMeta } from '$lib/types/chat';

export function getRoomCreatedAt(roomMetaById: Record<string, RoomMeta>, targetRoomId: string) {
	return roomMetaById[targetRoomId]?.createdAt ?? 0;
}

export function getRoomExpiry(roomMetaById: Record<string, RoomMeta>, targetRoomId: string) {
	const meta = roomMetaById[targetRoomId];
	if (!meta) {
		return 0;
	}
	if (meta.expiresAt > 0) {
		return meta.expiresAt;
	}
	return 0;
}

export function getRoomRemainingMs(
	roomMetaById: Record<string, RoomMeta>,
	targetRoomId: string,
	tickMs: number,
	getApproxServerNowMs: (tickMs?: number) => number
) {
	const expiry = getRoomExpiry(roomMetaById, targetRoomId);
	if (!expiry) {
		return Number.POSITIVE_INFINITY;
	}
	const now = getApproxServerNowMs(tickMs);
	return expiry - now;
}

export function getRemainingHoursLabel(
	roomMetaById: Record<string, RoomMeta>,
	targetRoomId: string,
	tickMs: number,
	getApproxServerNowMs: (tickMs?: number) => number
) {
	const remainingMs = getRoomRemainingMs(roomMetaById, targetRoomId, tickMs, getApproxServerNowMs);
	if (!Number.isFinite(remainingMs) || remainingMs === Number.POSITIVE_INFINITY) {
		return '--';
	}
	if (remainingMs <= 0) {
		return 'Expired';
	}
	const ceilToSingleDecimal = (value: number) => Math.ceil(value * 10) / 10;
	if (remainingMs < 60 * 60 * 1000) {
		const minutes = ceilToSingleDecimal(remainingMs / 60000);
		return `${minutes.toFixed(1)}m`;
	}
	if (remainingMs < 24 * 60 * 60 * 1000) {
		const hours = ceilToSingleDecimal(remainingMs / 3600000);
		return `${hours.toFixed(1)}h`;
	}
	const days = ceilToSingleDecimal(remainingMs / 86400000);
	return `${days.toFixed(1)}d`;
}
