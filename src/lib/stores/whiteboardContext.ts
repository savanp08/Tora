import { writable } from 'svelte/store';
import { normalizeRoomIDValue } from '$lib/utils/chat/core';

type WhiteboardContextEntry = {
	description: string;
	updatedAt: number;
};

export const whiteboardContextByRoom = writable<Record<string, WhiteboardContextEntry>>({});

export function setWhiteboardContext(roomId: string, description: string) {
	const normalizedRoomID = normalizeRoomIDValue(roomId);
	if (!normalizedRoomID) {
		return;
	}
	const normalizedDescription = String(description || '').trim();
	whiteboardContextByRoom.update((existing) => ({
		...existing,
		[normalizedRoomID]: {
			description: normalizedDescription,
			updatedAt: Date.now()
		}
	}));
}

export function clearWhiteboardContext(roomId: string) {
	const normalizedRoomID = normalizeRoomIDValue(roomId);
	if (!normalizedRoomID) {
		return;
	}
	whiteboardContextByRoom.update((existing) => {
		if (!(normalizedRoomID in existing)) {
			return existing;
		}
		const next = { ...existing };
		delete next[normalizedRoomID];
		return next;
	});
}
