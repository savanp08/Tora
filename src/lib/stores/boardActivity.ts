import { browser } from '$app/environment';
import { normalizeRoomIDValue } from '$lib/utils/chat/core';
import { writable } from 'svelte/store';

export type BoardActivityType =
	| 'task_completed'
	| 'task_added'
	| 'task_modified'
	| 'task_deleted'
	| 'task_moved'
	| 'board_cleared'
	| 'sprint_started'
	| 'budget_update'
	| 'board_generated'
	| 'board_edited';

export interface BoardActivityEvent {
	id: string;
	type: BoardActivityType;
	title: string;
	subtitle?: string;
	actor?: string;
	note?: string;
	timestamp: number;
}

export type BoardActivityInput = Omit<BoardActivityEvent, 'id' | 'timestamp'>;

const MAX_ACTIVITY_EVENTS = 100;
const STORAGE_KEY_PREFIX = 'converse:board-activity:v1';

export const boardActivity = writable<BoardActivityEvent[]>([]);
let activeBoardActivityRoomID = '';

function createEventID() {
	if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
		return crypto.randomUUID();
	}
	return `evt-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

export function addBoardActivity(event: BoardActivityInput): BoardActivityEvent {
	const fullEvent: BoardActivityEvent = {
		...event,
		id: createEventID(),
		timestamp: Date.now()
	};
	if (!activeBoardActivityRoomID) {
		boardActivity.update((events) => sortAndLimitEvents([fullEvent, ...events]));
		return fullEvent;
	}
	commitBoardActivityEvent(activeBoardActivityRoomID, fullEvent);
	return fullEvent;
}

export function addBoardActivityFromSocket(rawEvent: unknown, roomID: string) {
	const normalizedRoomID = normalizeRoomIDValue(roomID);
	if (!normalizedRoomID) {
		return false;
	}
	const normalizedEvent = normalizeIncomingEvent(rawEvent);
	if (!normalizedEvent) {
		return false;
	}
	commitBoardActivityEvent(normalizedRoomID, normalizedEvent);
	return true;
}

export function setBoardActivityRoom(roomID: string) {
	const normalizedRoomID = normalizeRoomIDValue(roomID);
	activeBoardActivityRoomID = normalizedRoomID;
	if (!normalizedRoomID) {
		boardActivity.set([]);
		return;
	}
	boardActivity.set(loadBoardActivity(normalizedRoomID));
}

export function clearBoardActivity(roomID?: string) {
	const normalizedRoomID = normalizeRoomIDValue(roomID || activeBoardActivityRoomID);
	if (browser && normalizedRoomID) {
		try {
			window.localStorage.removeItem(storageKeyForRoom(normalizedRoomID));
		} catch {
			// no-op: storage may be unavailable in strict/private modes
		}
	}
	if (!roomID || normalizedRoomID === activeBoardActivityRoomID) {
		boardActivity.set([]);
	}
}

export function getActiveBoardActivityRoomID() {
	return activeBoardActivityRoomID;
}

export function formatTimeAgo(timestamp: number): string {
	const diff = Date.now() - timestamp;
	const minutes = Math.floor(diff / 60000);
	if (minutes < 1) return 'just now';
	if (minutes < 60) return `${minutes}m ago`;
	const hours = Math.floor(minutes / 60);
	if (hours < 24) return `${hours}h ago`;
	const days = Math.floor(hours / 24);
	if (days < 7) return `${days}d ago`;
	return new Date(timestamp).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

function storageKeyForRoom(roomID: string) {
	return `${STORAGE_KEY_PREFIX}:${roomID}`;
}

function isBoardActivityType(value: unknown): value is BoardActivityType {
	if (typeof value !== 'string') return false;
	return (
		value === 'task_completed' ||
		value === 'task_added' ||
		value === 'task_modified' ||
		value === 'task_deleted' ||
		value === 'task_moved' ||
		value === 'board_cleared' ||
		value === 'sprint_started' ||
		value === 'budget_update' ||
		value === 'board_generated' ||
		value === 'board_edited'
	);
}

function normalizeIncomingEvent(raw: unknown): BoardActivityEvent | null {
	if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
		return null;
	}
	const source = raw as Record<string, unknown>;
	const type = source.type;
	if (!isBoardActivityType(type)) {
		return null;
	}
	const id = typeof source.id === 'string' ? source.id.trim() : createEventID();
	const title = typeof source.title === 'string' ? source.title.trim() : '';
	const timestamp =
		typeof source.timestamp === 'number' && Number.isFinite(source.timestamp)
			? source.timestamp
			: Date.now();
	if (!id || !title) {
		return null;
	}
	return {
		id,
		type,
		title,
		subtitle: typeof source.subtitle === 'string' ? source.subtitle.trim() : undefined,
		actor: typeof source.actor === 'string' ? source.actor.trim() : undefined,
		note: typeof source.note === 'string' ? source.note.trim() : undefined,
		timestamp
	};
}

function sortAndLimitEvents(events: BoardActivityEvent[]) {
	return [...events]
		.sort((left, right) => right.timestamp - left.timestamp)
		.slice(0, MAX_ACTIVITY_EVENTS);
}

function mergeActivityEvent(events: BoardActivityEvent[], event: BoardActivityEvent) {
	const withoutDuplicate = events.filter((candidate) => candidate.id !== event.id);
	return sortAndLimitEvents([event, ...withoutDuplicate]);
}

function commitBoardActivityEvent(roomID: string, event: BoardActivityEvent) {
	if (!roomID) {
		return;
	}
	if (roomID === activeBoardActivityRoomID) {
		boardActivity.update((events) => {
			const nextEvents = mergeActivityEvent(events, event);
			persistBoardActivity(nextEvents, roomID);
			return nextEvents;
		});
		return;
	}
	const currentEvents = loadBoardActivity(roomID);
	const nextEvents = mergeActivityEvent(currentEvents, event);
	persistBoardActivity(nextEvents, roomID);
}

function loadBoardActivity(roomID: string): BoardActivityEvent[] {
	if (!browser || !roomID) {
		return [];
	}
	try {
		const raw = window.localStorage.getItem(storageKeyForRoom(roomID));
		if (!raw) {
			return [];
		}
		const parsed = JSON.parse(raw) as unknown;
		if (!Array.isArray(parsed)) {
			return [];
		}
		return sortAndLimitEvents(
			parsed
				.map((entry) => normalizeIncomingEvent(entry))
				.filter((entry): entry is BoardActivityEvent => Boolean(entry))
		);
	} catch {
		return [];
	}
}

function persistBoardActivity(events: BoardActivityEvent[], roomID = activeBoardActivityRoomID) {
	if (!browser || !roomID) {
		return;
	}
	try {
		window.localStorage.setItem(
			storageKeyForRoom(roomID),
			JSON.stringify(events)
		);
	} catch {
		// no-op: storage may be unavailable or full
	}
}
