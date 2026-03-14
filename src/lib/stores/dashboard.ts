import { browser } from '$app/environment';
import { get, writable } from 'svelte/store';
import { authState } from '$lib/stores/auth';

const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
const API_BASE = (() => {
	const configured = API_BASE_RAW?.trim();
	if (configured) {
		return configured;
	}
	if (!browser) {
		return 'http://localhost:8080';
	}
	const protocol = window.location.protocol === 'https:' ? 'https:' : 'http:';
	const host = window.location.hostname;
	if (host === 'localhost' || host === '127.0.0.1') {
		return `${protocol}//${host}:8080`;
	}
	return window.location.origin;
})();

export interface DashboardRoom {
	room_id: string;
	room_name: string;
	role: string;
	last_accessed: string;
}

export interface DashboardConnection {
	user_id: string;
	target_id: string;
	status: string;
	created_at: string;
}

export interface DashboardPersonalItem {
	user_id: string;
	item_id: string;
	type: string;
	title: string;
	content: string;
	description: string;
	status: string;
	due_at: string | null;
	start_at: string | null;
	end_at: string | null;
	remind_at: string | null;
	repeat_rule: string;
	created_at: string;
}

export interface DashboardTask {
	room_id: string;
	id: string;
	title: string;
	description: string;
	status: string;
	assignee_id: string | null;
	created_at: string;
	updated_at: string;
}

export interface DashboardOverview {
	recent_rooms: DashboardRoom[];
	pending_requests: DashboardConnection[];
	upcoming_items: DashboardPersonalItem[];
	assigned_tasks: DashboardTask[];
}

type StatusResponse = {
	error?: string;
	message?: string;
};

const emptyOverview: DashboardOverview = {
	recent_rooms: [],
	pending_requests: [],
	upcoming_items: [],
	assigned_tasks: []
};

export const overview = writable<DashboardOverview | null>(null);
export const overviewLoading = writable(false);
export const overviewError = writable('');

function buildAuthHeaders(contentType = false) {
	const headers: Record<string, string> = {};
	if (contentType) {
		headers['Content-Type'] = 'application/json';
	}
	if (!browser) {
		return headers;
	}

	const fromStore = get(authState).token?.trim() || '';
	const fromStorage = window.localStorage.getItem('converse.auth.token')?.trim() || '';
	const token = fromStore || fromStorage;
	if (token) {
		headers.Authorization = `Bearer ${token}`;
	}
	return headers;
}

function toRecord(value: unknown): Record<string, unknown> | null {
	if (!value || typeof value !== 'object' || Array.isArray(value)) {
		return null;
	}
	return value as Record<string, unknown>;
}

function toStringValue(value: unknown) {
	return typeof value === 'string' ? value.trim() : '';
}

function normalizeRoom(raw: unknown): DashboardRoom | null {
	const source = toRecord(raw);
	if (!source) {
		return null;
	}
	const roomID = toStringValue(source.room_id);
	if (!roomID) {
		return null;
	}
	return {
		room_id: roomID,
		room_name: toStringValue(source.room_name),
		role: toStringValue(source.role),
		last_accessed: toStringValue(source.last_accessed)
	};
}

function normalizeConnection(raw: unknown): DashboardConnection | null {
	const source = toRecord(raw);
	if (!source) {
		return null;
	}
	const userID = toStringValue(source.user_id);
	const targetID = toStringValue(source.target_id);
	if (!userID || !targetID) {
		return null;
	}
	return {
		user_id: userID,
		target_id: targetID,
		status: toStringValue(source.status),
		created_at: toStringValue(source.created_at)
	};
}

function normalizePersonalItem(raw: unknown): DashboardPersonalItem | null {
	const source = toRecord(raw);
	if (!source) {
		return null;
	}
	const itemID = toStringValue(source.item_id);
	if (!itemID) {
		return null;
	}
	const dueAtRaw = toStringValue(source.due_at);
	return {
		user_id: toStringValue(source.user_id),
		item_id: itemID,
		type: toStringValue(source.type),
		title: toStringValue(source.title),
		content: toStringValue(source.content),
		description: toStringValue(source.description),
		status: toStringValue(source.status),
		due_at: dueAtRaw || null,
		start_at: toStringValue(source.start_at) || null,
		end_at: toStringValue(source.end_at) || null,
		remind_at: toStringValue(source.remind_at) || null,
		repeat_rule: toStringValue(source.repeat_rule),
		created_at: toStringValue(source.created_at)
	};
}

function normalizeTask(raw: unknown): DashboardTask | null {
	const source = toRecord(raw);
	if (!source) {
		return null;
	}
	const taskID = toStringValue(source.id);
	if (!taskID) {
		return null;
	}
	const assignee = toStringValue(source.assignee_id);
	return {
		room_id: toStringValue(source.room_id),
		id: taskID,
		title: toStringValue(source.title),
		description: toStringValue(source.description),
		status: toStringValue(source.status),
		assignee_id: assignee || null,
		created_at: toStringValue(source.created_at),
		updated_at: toStringValue(source.updated_at)
	};
}

function normalizeList<T>(value: unknown, normalizer: (row: unknown) => T | null): T[] {
	if (!Array.isArray(value)) {
		return [];
	}
	return value.map((row) => normalizer(row)).filter((row): row is T => Boolean(row));
}

function normalizeOverview(payload: unknown): DashboardOverview {
	const source = toRecord(payload);
	if (!source) {
		return emptyOverview;
	}

	return {
		recent_rooms: normalizeList(source.recent_rooms, normalizeRoom),
		pending_requests: normalizeList(source.pending_requests, normalizeConnection),
		upcoming_items: normalizeList(source.upcoming_items, normalizePersonalItem),
		assigned_tasks: normalizeList(source.assigned_tasks, normalizeTask)
	};
}

async function parseErrorMessage(response: Response) {
	const payload = (await response.json().catch(() => null)) as StatusResponse | null;
	return payload?.error?.trim() || `HTTP ${response.status}`;
}

export async function fetchDashboardOverview() {
	overviewLoading.set(true);
	overviewError.set('');
	try {
		const response = await fetch(`${API_BASE}/api/dashboard/overview`, {
			method: 'GET',
			headers: buildAuthHeaders(),
			credentials: 'include'
		});
		if (!response.ok) {
			throw new Error(await parseErrorMessage(response));
		}

		const payload = (await response.json().catch(() => null)) as unknown;
		const normalized = normalizeOverview(payload);
		overview.set(normalized);
		return normalized;
	} catch (error) {
		overview.set(null);
		const message = error instanceof Error ? error.message : 'Failed to load dashboard overview';
		overviewError.set(message);
		throw error instanceof Error ? error : new Error(message);
	} finally {
		overviewLoading.set(false);
	}
}

export async function acceptPendingRequest(requestUserID: string) {
	const normalizedUserID = requestUserID.trim();
	if (!normalizedUserID) {
		throw new Error('request user id is required');
	}

	const response = await fetch(`${API_BASE}/api/network/accept`, {
		method: 'POST',
		headers: buildAuthHeaders(true),
		credentials: 'include',
		body: JSON.stringify({ target_id: normalizedUserID })
	});
	if (!response.ok) {
		throw new Error(await parseErrorMessage(response));
	}

	overview.update((current) => {
		if (!current) {
			return current;
		}
		return {
			...current,
			pending_requests: current.pending_requests.filter((entry) => entry.user_id !== normalizedUserID)
		};
	});
}

export function declinePendingRequest(requestUserID: string) {
	const normalizedUserID = requestUserID.trim();
	if (!normalizedUserID) {
		return;
	}
	overview.update((current) => {
		if (!current) {
			return current;
		}
		return {
			...current,
			pending_requests: current.pending_requests.filter((entry) => entry.user_id !== normalizedUserID)
		};
	});
}
