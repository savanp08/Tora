import { writable } from 'svelte/store';

const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';

export interface PersonalItem {
	user_id: string;
	item_id: string;
	type: string;
	content: string;
	status: string;
	due_at: string | null;
	created_at: string;
}

type PersonalStatusResponse = {
	error?: string;
	message?: string;
};

export const personalItems = writable<PersonalItem[]>([]);

function normalizePersonalItem(raw: unknown): PersonalItem | null {
	if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
		return null;
	}
	const source = raw as Record<string, unknown>;
	const itemID = typeof source.item_id === 'string' ? source.item_id.trim() : '';
	if (!itemID) {
		return null;
	}
	return {
		user_id: typeof source.user_id === 'string' ? source.user_id.trim() : '',
		item_id: itemID,
		type: typeof source.type === 'string' ? source.type.trim() : '',
		content: typeof source.content === 'string' ? source.content.trim() : '',
		status: typeof source.status === 'string' ? source.status.trim() : '',
		due_at: typeof source.due_at === 'string' && source.due_at.trim() ? source.due_at.trim() : null,
		created_at: typeof source.created_at === 'string' ? source.created_at.trim() : ''
	};
}

async function parseErrorMessage(response: Response) {
	const payload = (await response.json().catch(() => null)) as PersonalStatusResponse | null;
	return payload?.error?.trim() || `HTTP ${response.status}`;
}

export async function fetchItems() {
	const response = await fetch(`${API_BASE}/api/personal/items`, {
		method: 'GET',
		credentials: 'include'
	});
	if (!response.ok) {
		throw new Error(await parseErrorMessage(response));
	}
	const payload = (await response.json().catch(() => [])) as unknown;
	const records = Array.isArray(payload) ? payload : [];
	const normalized = records
		.map((item) => normalizePersonalItem(item))
		.filter((item): item is PersonalItem => Boolean(item));
	personalItems.set(normalized);
	return normalized;
}

export async function addItem(type: string, content: string) {
	const response = await fetch(`${API_BASE}/api/personal/items`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		credentials: 'include',
		body: JSON.stringify({
			type,
			content
		})
	});
	if (!response.ok) {
		throw new Error(await parseErrorMessage(response));
	}
	const created = normalizePersonalItem(await response.json().catch(() => null));
	if (!created) {
		throw new Error('Invalid personal item response');
	}
	personalItems.update((items) => [created, ...items]);
	return created;
}

export async function updateStatus(itemId: string, status: string) {
	const normalizedItemID = itemId.trim();
	if (!normalizedItemID) {
		throw new Error('item id is required');
	}
	const response = await fetch(`${API_BASE}/api/personal/items/${encodeURIComponent(normalizedItemID)}/status`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		credentials: 'include',
		body: JSON.stringify({ status })
	});
	if (!response.ok) {
		throw new Error(await parseErrorMessage(response));
	}
	personalItems.update((items) =>
		items.map((item) => {
			if (item.item_id !== normalizedItemID) {
				return item;
			}
			return {
				...item,
				status
			};
		})
	);
}

export async function deleteItem(itemId: string) {
	const normalizedItemID = itemId.trim();
	if (!normalizedItemID) {
		throw new Error('item id is required');
	}
	const response = await fetch(`${API_BASE}/api/personal/items/${encodeURIComponent(normalizedItemID)}`, {
		method: 'DELETE',
		credentials: 'include'
	});
	if (!response.ok) {
		throw new Error(await parseErrorMessage(response));
	}
	personalItems.update((items) => items.filter((item) => item.item_id !== normalizedItemID));
}
