import { writable } from 'svelte/store';

const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';

export type PersonalItemType = 'note' | 'reminder' | 'task';

export interface PersonalItem {
	user_id: string;
	item_id: string;
	type: PersonalItemType | string;
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

export interface PersonalItemInput {
	type: PersonalItemType;
	title?: string;
	content?: string;
	description?: string;
	status?: string;
	due_at?: string | null;
	start_at?: string | null;
	end_at?: string | null;
	remind_at?: string | null;
	repeat_rule?: string;
}

type PersonalStatusResponse = {
	error?: string;
	message?: string;
};

export const personalItems = writable<PersonalItem[]>([]);

function normalizeDateTimeOrNull(value: unknown) {
	return typeof value === 'string' && value.trim() ? value.trim() : null;
}

function normalizeType(value: unknown): PersonalItemType | string {
	const normalized = typeof value === 'string' ? value.trim().toLowerCase() : '';
	if (normalized === 'note' || normalized === 'reminder' || normalized === 'task') {
		return normalized;
	}
	return normalized || 'task';
}

function normalizePersonalItem(raw: unknown): PersonalItem | null {
	if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
		return null;
	}
	const source = raw as Record<string, unknown>;
	const itemID = typeof source.item_id === 'string' ? source.item_id.trim() : '';
	if (!itemID) {
		return null;
	}
	const content = typeof source.content === 'string' ? source.content.trim() : '';
	const title = typeof source.title === 'string' ? source.title.trim() : '';
	return {
		user_id: typeof source.user_id === 'string' ? source.user_id.trim() : '',
		item_id: itemID,
		type: normalizeType(source.type),
		title: title || content,
		content,
		description: typeof source.description === 'string' ? source.description.trim() : '',
		status: typeof source.status === 'string' ? source.status.trim() : '',
		due_at: normalizeDateTimeOrNull(source.due_at),
		start_at: normalizeDateTimeOrNull(source.start_at),
		end_at: normalizeDateTimeOrNull(source.end_at),
		remind_at: normalizeDateTimeOrNull(source.remind_at),
		repeat_rule: typeof source.repeat_rule === 'string' ? source.repeat_rule.trim() : '',
		created_at: typeof source.created_at === 'string' ? source.created_at.trim() : ''
	};
}

function sanitizeInput(input: PersonalItemInput) {
	const title = input.title?.trim() ?? '';
	const content = input.content?.trim() ?? '';
	const description = input.description?.trim() ?? '';
	const nextContent = content || title;

	return {
		type: input.type,
		title,
		content: nextContent,
		description,
		status: input.status?.trim() || 'pending',
		due_at: input.due_at ?? null,
		start_at: input.start_at ?? null,
		end_at: input.end_at ?? null,
		remind_at: input.remind_at ?? null,
		repeat_rule: input.repeat_rule?.trim() || ''
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

export async function addItem(input: PersonalItemInput) {
	const payload = sanitizeInput(input);
	const response = await fetch(`${API_BASE}/api/personal/items`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		credentials: 'include',
		body: JSON.stringify(payload)
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

export async function addItemsBulk(inputs: PersonalItemInput[]) {
	if (!Array.isArray(inputs) || inputs.length === 0) {
		return [] as PersonalItem[];
	}
	const payload = {
		items: inputs.map((input) => sanitizeInput(input))
	};
	const response = await fetch(`${API_BASE}/api/personal/items/bulk`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		credentials: 'include',
		body: JSON.stringify(payload)
	});
	if (!response.ok) {
		throw new Error(await parseErrorMessage(response));
	}
	const result = (await response.json().catch(() => [])) as unknown;
	const records = Array.isArray(result) ? result : [];
	const created = records
		.map((item) => normalizePersonalItem(item))
		.filter((item): item is PersonalItem => Boolean(item));
	if (created.length > 0) {
		personalItems.update((items) => [...created, ...items]);
	}
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
