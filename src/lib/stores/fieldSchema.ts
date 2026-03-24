import { writable } from 'svelte/store';
import { normalizeRoomIDValue, toStringValue } from '$lib/utils/chat/core';

const DEFAULT_API_BASE = 'http://127.0.0.1:8080';

export type FieldSchemaType =
	| 'text'
	| 'number'
	| 'date'
	| 'select'
	| 'multi_select'
	| 'checkbox'
	| 'person'
	| 'url'
	| string;

export type FieldSchema = {
	fieldId: string;
	roomId: string;
	name: string;
	fieldType: FieldSchemaType;
	options?: string[];
	position: number;
};

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

type FieldSchemaCreateInput = {
	name: string;
	fieldType: string;
	options?: string[];
	position?: number;
};

type FieldSchemaUpdateInput = {
	name?: string;
	fieldType?: string;
	options?: string[];
	position?: number;
};

type FieldSchemaSource = {
	field_id?: unknown;
	fieldId?: unknown;
	room_id?: unknown;
	roomId?: unknown;
	name?: unknown;
	field_type?: unknown;
	fieldType?: unknown;
	options?: unknown;
	position?: unknown;
};

export const fieldSchemaStore = writable<FieldSchema[]>([]);
export const fieldSchemaStoreLoading = writable<boolean>(false);
export const fieldSchemaStoreError = writable<string>('');

let activeFieldSchemaRoomId = '';
let activeFieldSchemaLoadToken = 0;

function normalizeApiBase(value?: string) {
	const trimmed = (value ?? '').trim();
	return trimmed || DEFAULT_API_BASE;
}

function normalizeFieldSchemaID(value: unknown) {
	return toStringValue(value)
		.trim()
		.replace(/[^a-zA-Z0-9_-]/g, '');
}

function normalizeFieldSchemaType(value: unknown): FieldSchemaType {
	const normalized = toStringValue(value).trim().toLowerCase();
	return normalized || 'text';
}

function sanitizeFieldSchemaOptions(value: unknown): string[] | undefined {
	if (!Array.isArray(value)) {
		return undefined;
	}
	const seen = new Set<string>();
	const next: string[] = [];
	for (const rawOption of value) {
		const trimmed = toStringValue(rawOption).trim();
		if (!trimmed) {
			continue;
		}
		const dedupeKey = trimmed.toLowerCase();
		if (seen.has(dedupeKey)) {
			continue;
		}
		seen.add(dedupeKey);
		next.push(trimmed);
	}
	return next.length > 0 ? next : undefined;
}

function normalizeFieldSchemaPosition(value: unknown) {
	if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
		return Math.floor(value);
	}
	if (typeof value === 'string') {
		const parsed = Number.parseInt(value.trim(), 10);
		if (Number.isFinite(parsed) && parsed >= 0) {
			return parsed;
		}
	}
	return 0;
}

function normalizeFieldSchemaRecord(raw: unknown, fallbackRoomId = activeFieldSchemaRoomId) {
	if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
		return null;
	}
	const source = raw as FieldSchemaSource;
	const fieldId = normalizeFieldSchemaID(source.field_id ?? source.fieldId);
	if (!fieldId) {
		return null;
	}
	const roomId = normalizeRoomIDValue(
		toStringValue(source.room_id ?? source.roomId ?? fallbackRoomId)
	);
	if (!roomId) {
		return null;
	}
	const name = toStringValue(source.name).trim();
	if (!name) {
		return null;
	}
	return {
		fieldId,
		roomId,
		name,
		fieldType: normalizeFieldSchemaType(source.field_type ?? source.fieldType),
		options: sanitizeFieldSchemaOptions(source.options),
		position: normalizeFieldSchemaPosition(source.position)
	} as FieldSchema;
}

function sortFieldSchemas(schemas: FieldSchema[]) {
	return [...schemas].sort(
		(left, right) =>
			left.position - right.position ||
			left.name.localeCompare(right.name, undefined, { sensitivity: 'base' }) ||
			left.fieldId.localeCompare(right.fieldId, undefined, { sensitivity: 'base' })
	);
}

function dedupeFieldSchemasByID(schemas: FieldSchema[]) {
	const byID = new Map<string, FieldSchema>();
	for (const schema of schemas) {
		byID.set(schema.fieldId, schema);
	}
	return sortFieldSchemas([...byID.values()]);
}

function parseErrorMessage(payload: unknown, status: number) {
	if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
		return `HTTP ${status}`;
	}
	const source = payload as { error?: unknown; message?: unknown };
	return toStringValue(source.error ?? source.message).trim() || `HTTP ${status}`;
}

export function getActiveFieldSchemaRoomId() {
	return activeFieldSchemaRoomId;
}

export function clearFieldSchemaStore() {
	fieldSchemaStore.set([]);
	fieldSchemaStoreError.set('');
	fieldSchemaStoreLoading.set(false);
}

export function setFieldSchemaStoreForRoom(records: unknown[], roomId = activeFieldSchemaRoomId) {
	const normalizedRoomId = normalizeRoomIDValue(roomId);
	if (!normalizedRoomId) {
		fieldSchemaStore.set([]);
		return;
	}
	const normalized = records
		.map((record) => normalizeFieldSchemaRecord(record, normalizedRoomId))
		.filter((schema): schema is FieldSchema => Boolean(schema));
	fieldSchemaStore.set(dedupeFieldSchemasByID(normalized));
}

export function upsertFieldSchemaStoreEntry(raw: unknown, roomId = activeFieldSchemaRoomId) {
	const next = normalizeFieldSchemaRecord(raw, roomId);
	if (!next) {
		return null;
	}
	if (activeFieldSchemaRoomId && next.roomId !== activeFieldSchemaRoomId) {
		return null;
	}
	fieldSchemaStore.update((schemas) =>
		dedupeFieldSchemasByID([...schemas.filter((schema) => schema.fieldId !== next.fieldId), next])
	);
	return next;
}

export function removeFieldSchemaStoreEntry(
	fieldIdValue: unknown,
	roomId = activeFieldSchemaRoomId
) {
	const fieldId = normalizeFieldSchemaID(fieldIdValue);
	const normalizedRoomId = normalizeRoomIDValue(roomId);
	if (!fieldId || !normalizedRoomId) {
		return false;
	}
	if (activeFieldSchemaRoomId && normalizedRoomId !== activeFieldSchemaRoomId) {
		return false;
	}
	let removed = false;
	fieldSchemaStore.update((schemas) => {
		const next = schemas.filter((schema) => schema.fieldId !== fieldId);
		removed = next.length !== schemas.length;
		return next;
	});
	return removed;
}

export async function initializeFieldSchemasForRoom(
	roomId: string,
	options?: {
		fetchImpl?: FetchLike;
		apiBase?: string;
	}
) {
	const normalizedRoomId = normalizeRoomIDValue(roomId);
	activeFieldSchemaRoomId = normalizedRoomId;
	activeFieldSchemaLoadToken += 1;
	const loadToken = activeFieldSchemaLoadToken;

	if (!normalizedRoomId) {
		clearFieldSchemaStore();
		return [];
	}

	fieldSchemaStore.set([]);
	fieldSchemaStoreError.set('');
	fieldSchemaStoreLoading.set(true);

	const fetchImpl = options?.fetchImpl ?? fetch;
	const apiBase = normalizeApiBase(options?.apiBase);

	try {
		const response = await fetchImpl(
			`${apiBase}/api/rooms/${encodeURIComponent(normalizedRoomId)}/field-schemas`,
			{
				method: 'GET',
				credentials: 'include'
			}
		);
		const payload = (await response.json().catch(() => null)) as unknown;
		if (!response.ok) {
			throw new Error(parseErrorMessage(payload, response.status));
		}
		if (loadToken !== activeFieldSchemaLoadToken || normalizedRoomId !== activeFieldSchemaRoomId) {
			return [];
		}
		const records = Array.isArray(payload) ? payload : [];
		const normalized = records
			.map((record) => normalizeFieldSchemaRecord(record, normalizedRoomId))
			.filter((schema): schema is FieldSchema => Boolean(schema));
		const deduped = dedupeFieldSchemasByID(normalized);
		fieldSchemaStore.set(deduped);
		fieldSchemaStoreError.set('');
		return deduped;
	} catch (error) {
		const message = error instanceof Error ? error.message : 'Failed to load field schemas';
		if (loadToken === activeFieldSchemaLoadToken) {
			fieldSchemaStoreError.set(message);
			fieldSchemaStore.set([]);
		}
		throw error;
	} finally {
		if (loadToken === activeFieldSchemaLoadToken) {
			fieldSchemaStoreLoading.set(false);
		}
	}
}

export async function refreshFieldSchemasForRoom(
	roomId = activeFieldSchemaRoomId,
	options?: {
		fetchImpl?: FetchLike;
		apiBase?: string;
	}
) {
	const normalizedRoomId = normalizeRoomIDValue(roomId);
	if (!normalizedRoomId || normalizedRoomId !== activeFieldSchemaRoomId) {
		return [];
	}
	return initializeFieldSchemasForRoom(normalizedRoomId, options);
}

export async function createFieldSchema(
	roomId: string,
	data: FieldSchemaCreateInput,
	options?: {
		fetchImpl?: FetchLike;
		apiBase?: string;
	}
) {
	const normalizedRoomId = normalizeRoomIDValue(roomId);
	if (!normalizedRoomId) {
		throw new Error('Invalid room id');
	}
	const fetchImpl = options?.fetchImpl ?? fetch;
	const apiBase = normalizeApiBase(options?.apiBase);
	const response = await fetchImpl(
		`${apiBase}/api/rooms/${encodeURIComponent(normalizedRoomId)}/field-schemas`,
		{
			method: 'POST',
			credentials: 'include',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({
				name: data.name,
				field_type: data.fieldType,
				options: data.options,
				position: data.position
			})
		}
	);
	const payload = (await response.json().catch(() => null)) as unknown;
	if (!response.ok) {
		throw new Error(parseErrorMessage(payload, response.status));
	}
	const schema = upsertFieldSchemaStoreEntry(payload, normalizedRoomId);
	if (!schema) {
		throw new Error('Invalid field schema response');
	}
	return schema;
}

export async function updateFieldSchema(
	roomId: string,
	fieldId: string,
	data: FieldSchemaUpdateInput,
	options?: {
		fetchImpl?: FetchLike;
		apiBase?: string;
	}
) {
	const normalizedRoomId = normalizeRoomIDValue(roomId);
	const normalizedFieldID = normalizeFieldSchemaID(fieldId);
	if (!normalizedRoomId || !normalizedFieldID) {
		throw new Error('Invalid field schema context');
	}
	const fetchImpl = options?.fetchImpl ?? fetch;
	const apiBase = normalizeApiBase(options?.apiBase);
	const response = await fetchImpl(
		`${apiBase}/api/rooms/${encodeURIComponent(normalizedRoomId)}/field-schemas/${encodeURIComponent(normalizedFieldID)}`,
		{
			method: 'PATCH',
			credentials: 'include',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({
				name: data.name,
				field_type: data.fieldType,
				options: data.options,
				position: data.position
			})
		}
	);
	const payload = (await response.json().catch(() => null)) as unknown;
	if (!response.ok) {
		throw new Error(parseErrorMessage(payload, response.status));
	}
	const schema = upsertFieldSchemaStoreEntry(payload, normalizedRoomId);
	if (!schema) {
		throw new Error('Invalid field schema response');
	}
	return schema;
}

export async function deleteFieldSchema(
	roomId: string,
	fieldId: string,
	options?: {
		fetchImpl?: FetchLike;
		apiBase?: string;
	}
) {
	const normalizedRoomId = normalizeRoomIDValue(roomId);
	const normalizedFieldID = normalizeFieldSchemaID(fieldId);
	if (!normalizedRoomId || !normalizedFieldID) {
		throw new Error('Invalid field schema context');
	}
	const fetchImpl = options?.fetchImpl ?? fetch;
	const apiBase = normalizeApiBase(options?.apiBase);
	const response = await fetchImpl(
		`${apiBase}/api/rooms/${encodeURIComponent(normalizedRoomId)}/field-schemas/${encodeURIComponent(normalizedFieldID)}`,
		{
			method: 'DELETE',
			credentials: 'include'
		}
	);
	if (!response.ok && response.status !== 204) {
		const payload = (await response.json().catch(() => null)) as unknown;
		throw new Error(parseErrorMessage(payload, response.status));
	}
	removeFieldSchemaStoreEntry(normalizedFieldID, normalizedRoomId);
	return true;
}
