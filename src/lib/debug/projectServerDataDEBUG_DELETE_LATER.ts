import { writable } from 'svelte/store';

export type ProjectServerDebugDirectionDEBUG_DELETE_LATER = 'request' | 'response' | 'stream';

export type ProjectServerDebugEntryDEBUG_DELETE_LATER = {
	id: number;
	createdAt: string;
	source: string;
	direction: ProjectServerDebugDirectionDEBUG_DELETE_LATER;
	roomId?: string;
	endpoint: string;
	method?: string;
	status?: number;
	event?: string;
	payload: unknown;
};

type ProjectServerDebugConsoleStateDEBUG_DELETE_LATER = {
	entries: ProjectServerDebugEntryDEBUG_DELETE_LATER[];
	latest: ProjectServerDebugEntryDEBUG_DELETE_LATER | null;
	latestBySource: Record<string, ProjectServerDebugEntryDEBUG_DELETE_LATER>;
	clear: () => void;
};

const MAX_PROJECT_SERVER_DEBUG_ENTRIES_DEBUG_DELETE_LATER = 1000;

let nextProjectServerDebugEntryIdDEBUG_DELETE_LATER = 0;

export const projectServerDataLogDEBUG_DELETE_LATER = writable<
	ProjectServerDebugEntryDEBUG_DELETE_LATER[]
>([]);

function cloneProjectServerPayloadDEBUG_DELETE_LATER(payload: unknown): unknown {
	try {
		if (typeof structuredClone === 'function') {
			return structuredClone(payload);
		}
	} catch {
		// Fall through to JSON serialization when structuredClone fails.
	}

	if (payload === undefined) {
		return undefined;
	}

	try {
		return JSON.parse(JSON.stringify(payload));
	} catch {
		return payload;
	}
}

function buildProjectServerLatestBySourceDEBUG_DELETE_LATER(
	entries: ProjectServerDebugEntryDEBUG_DELETE_LATER[]
) {
	return entries.reduce<Record<string, ProjectServerDebugEntryDEBUG_DELETE_LATER>>((acc, entry) => {
		acc[entry.source] = entry;
		return acc;
	}, {});
}

function syncProjectServerDebugGlobalDEBUG_DELETE_LATER(
	entries: ProjectServerDebugEntryDEBUG_DELETE_LATER[]
) {
	if (typeof window === 'undefined') {
		return;
	}

	const state: ProjectServerDebugConsoleStateDEBUG_DELETE_LATER = {
		entries,
		latest: entries[entries.length - 1] ?? null,
		latestBySource: buildProjectServerLatestBySourceDEBUG_DELETE_LATER(entries),
		clear: clearProjectServerDataLogDEBUG_DELETE_LATER
	};
	window.PROJECT_SERVER_DEBUG_DELETE_LATER = state;
}

function isAIProjectServerEntryDEBUG_DELETE_LATER(
	entry: ProjectServerDebugEntryDEBUG_DELETE_LATER
) {
	const source = entry.source.trim().toLowerCase();
	const endpoint = entry.endpoint.trim().toLowerCase();
	const event = (entry.event || '').trim().toLowerCase();

	return (
		source.includes('ai_') ||
		source.includes('tora') ||
		source.includes('fetch:/api/rooms/') && endpoint.includes('/ai-') ||
		endpoint.includes('/ai-') ||
		event.startsWith('ai') ||
		event.startsWith('tora')
	);
}

function logProjectServerEntryToConsoleDEBUG_DELETE_LATER(
	entry: ProjectServerDebugEntryDEBUG_DELETE_LATER
) {
	if (typeof window === 'undefined') {
		return;
	}

	if (!isAIProjectServerEntryDEBUG_DELETE_LATER(entry)) {
		console.debug('[PROJECT_SERVER_DEBUG_DELETE_LATER]', entry);
		return;
	}

	const direction = entry.direction.toUpperCase();
	const eventLabel = entry.event ? `:${entry.event}` : '';
	const methodLabel = entry.method ? ` ${entry.method}` : '';
	const statusLabel = typeof entry.status === 'number' ? ` ${entry.status}` : '';
	const roomLabel = entry.roomId ? ` room=${entry.roomId}` : '';
	const label = `[AI DEBUG][${direction}${eventLabel}]${methodLabel}${statusLabel}${roomLabel} ${entry.endpoint}`;

	if (
		entry.event === 'error' ||
		entry.event === 'network_error' ||
		(typeof entry.status === 'number' && entry.status >= 400)
	) {
		console.error(label, entry.payload);
		return;
	}

	if (entry.direction === 'stream') {
		console.log(label, entry.payload);
		return;
	}

	console.info(label, entry.payload);
}

export function clearProjectServerDataLogDEBUG_DELETE_LATER() {
	projectServerDataLogDEBUG_DELETE_LATER.set([]);
	syncProjectServerDebugGlobalDEBUG_DELETE_LATER([]);
}

export function recordProjectServerDataDEBUG_DELETE_LATER(
	input: Omit<ProjectServerDebugEntryDEBUG_DELETE_LATER, 'id' | 'createdAt'>
) {
	const entry: ProjectServerDebugEntryDEBUG_DELETE_LATER = {
		...input,
		id: nextProjectServerDebugEntryIdDEBUG_DELETE_LATER + 1,
		createdAt: new Date().toISOString(),
		payload: cloneProjectServerPayloadDEBUG_DELETE_LATER(input.payload)
	};
	nextProjectServerDebugEntryIdDEBUG_DELETE_LATER = entry.id;

	projectServerDataLogDEBUG_DELETE_LATER.update((entries) => {
		const nextEntries = [...entries, entry].slice(
			-MAX_PROJECT_SERVER_DEBUG_ENTRIES_DEBUG_DELETE_LATER
		);
		syncProjectServerDebugGlobalDEBUG_DELETE_LATER(nextEntries);
		return nextEntries;
	});

	logProjectServerEntryToConsoleDEBUG_DELETE_LATER(entry);

	return entry;
}

function resolveProjectServerEndpointDEBUG_DELETE_LATER(input: RequestInfo | URL) {
	if (typeof input === 'string') {
		return input;
	}
	if (input instanceof URL) {
		return input.toString();
	}
	return input.url;
}

function normalizeProjectServerEndpointUrlDEBUG_DELETE_LATER(endpoint: string) {
	try {
		const base =
			typeof window !== 'undefined' ? window.location.origin : 'http://127.0.0.1:8080';
		return new URL(endpoint, base);
	} catch {
		return null;
	}
}

function shouldTrackProjectServerEndpointDEBUG_DELETE_LATER(endpoint: string) {
	const normalizedUrl = normalizeProjectServerEndpointUrlDEBUG_DELETE_LATER(endpoint);
	const pathname = normalizedUrl?.pathname ?? endpoint;
	return pathname.startsWith('/api/rooms/');
}

function buildProjectServerFetchSourceDEBUG_DELETE_LATER(endpoint: string) {
	const normalizedUrl = normalizeProjectServerEndpointUrlDEBUG_DELETE_LATER(endpoint);
	return `fetch:${normalizedUrl?.pathname ?? endpoint}`;
}

function extractProjectServerRoomIdDEBUG_DELETE_LATER(endpoint: string) {
	const normalizedUrl = normalizeProjectServerEndpointUrlDEBUG_DELETE_LATER(endpoint);
	const parts = (normalizedUrl?.pathname ?? endpoint).split('/').filter(Boolean);
	if (parts[0] !== 'api' || parts[1] !== 'rooms') {
		return '';
	}
	return decodeURIComponent(parts[2] ?? '');
}

async function normalizeProjectServerBodyInitDEBUG_DELETE_LATER(
	body: BodyInit | null | undefined
): Promise<unknown> {
	if (body == null) {
		return null;
	}
	if (typeof body === 'string') {
		try {
			return JSON.parse(body);
		} catch {
			return body;
		}
	}
	if (typeof URLSearchParams !== 'undefined' && body instanceof URLSearchParams) {
		return Object.fromEntries(body.entries());
	}
	if (typeof FormData !== 'undefined' && body instanceof FormData) {
		return Array.from(body.entries()).reduce<Record<string, unknown>>((acc, [key, value]) => {
			acc[key] = typeof value === 'string' ? value : `[File ${value.name || 'unnamed'}]`;
			return acc;
		}, {});
	}
	if (typeof Blob !== 'undefined' && body instanceof Blob) {
		return `[Blob ${body.type || 'application/octet-stream'} ${body.size} bytes]`;
	}
	if (body instanceof ArrayBuffer) {
		return `[ArrayBuffer ${body.byteLength} bytes]`;
	}
	if (ArrayBuffer.isView(body)) {
		return `[TypedArray ${body.byteLength} bytes]`;
	}
	return String(body);
}

async function readProjectServerRequestPayloadDEBUG_DELETE_LATER(
	input: RequestInfo | URL,
	init?: RequestInit
) {
	if (init?.body !== undefined) {
		return normalizeProjectServerBodyInitDEBUG_DELETE_LATER(init.body);
	}
	if (typeof Request !== 'undefined' && input instanceof Request) {
		try {
			const clonedRequestDEBUG_DELETE_LATER = input.clone();
			return normalizeProjectServerBodyInitDEBUG_DELETE_LATER(await clonedRequestDEBUG_DELETE_LATER.text());
		} catch {
			return null;
		}
	}
	return null;
}

async function readProjectServerResponsePayloadDEBUG_DELETE_LATER(response: Response) {
	if (response.status === 204) {
		return null;
	}
	const contentTypeDEBUG_DELETE_LATER = response.headers.get('content-type')?.toLowerCase() ?? '';
	if (contentTypeDEBUG_DELETE_LATER.includes('application/json')) {
		return (await response.clone().json().catch(() => null)) as unknown;
	}
	if (contentTypeDEBUG_DELETE_LATER.includes('text/event-stream')) {
		return {
			contentType: contentTypeDEBUG_DELETE_LATER,
			stream: true
		};
	}
	if (contentTypeDEBUG_DELETE_LATER.startsWith('text/')) {
		return (await response.clone().text().catch(() => '')) as unknown;
	}
	if (!contentTypeDEBUG_DELETE_LATER) {
		return null;
	}
	return {
		contentType: contentTypeDEBUG_DELETE_LATER,
		omitted: true
	};
}

export function installProjectServerFetchDebugDEBUG_DELETE_LATER() {
	if (typeof window === 'undefined') {
		return () => {};
	}
	if (window.PROJECT_SERVER_FETCH_RESTORE_DEBUG_DELETE_LATER) {
		return window.PROJECT_SERVER_FETCH_RESTORE_DEBUG_DELETE_LATER;
	}

	const originalFetchDEBUG_DELETE_LATER = window.fetch.bind(window);
	window.PROJECT_SERVER_FETCH_ORIGINAL_DEBUG_DELETE_LATER = originalFetchDEBUG_DELETE_LATER;

	const patchedFetchDEBUG_DELETE_LATER: typeof window.fetch = async (input, init) => {
		const endpoint = resolveProjectServerEndpointDEBUG_DELETE_LATER(input);
		const shouldTrack = shouldTrackProjectServerEndpointDEBUG_DELETE_LATER(endpoint);
		const source = buildProjectServerFetchSourceDEBUG_DELETE_LATER(endpoint);
		const roomId = extractProjectServerRoomIdDEBUG_DELETE_LATER(endpoint) || undefined;
		const method =
			(init?.method ||
				(typeof Request !== 'undefined' && input instanceof Request ? input.method : 'GET') ||
				'GET'
			).toUpperCase();

		if (shouldTrack) {
			recordProjectServerDataDEBUG_DELETE_LATER({
				source,
				direction: 'request',
				roomId,
				endpoint,
				method,
				payload: await readProjectServerRequestPayloadDEBUG_DELETE_LATER(input, init)
			});
		}

		try {
			const response = await originalFetchDEBUG_DELETE_LATER(input, init);
			if (shouldTrack) {
				recordProjectServerDataDEBUG_DELETE_LATER({
					source,
					direction: 'response',
					roomId,
					endpoint,
					method,
					status: response.status,
					event: response.ok ? undefined : 'error',
					payload: await readProjectServerResponsePayloadDEBUG_DELETE_LATER(response)
				});
			}
			return response;
		} catch (error) {
			if (shouldTrack) {
				recordProjectServerDataDEBUG_DELETE_LATER({
					source,
					direction: 'response',
					roomId,
					endpoint,
					method,
					event: 'network_error',
					payload: {
						message: error instanceof Error ? error.message : 'Unknown fetch failure'
					}
				});
			}
			throw error;
		}
	};

	window.fetch = patchedFetchDEBUG_DELETE_LATER;
	const removeProjectServerFetchDebugDEBUG_DELETE_LATER = () => {
		if (window.fetch === patchedFetchDEBUG_DELETE_LATER) {
			window.fetch = originalFetchDEBUG_DELETE_LATER;
		}
		window.PROJECT_SERVER_FETCH_ORIGINAL_DEBUG_DELETE_LATER = undefined;
		window.PROJECT_SERVER_FETCH_RESTORE_DEBUG_DELETE_LATER = undefined;
	};
	window.PROJECT_SERVER_FETCH_RESTORE_DEBUG_DELETE_LATER =
		removeProjectServerFetchDebugDEBUG_DELETE_LATER;
	return removeProjectServerFetchDebugDEBUG_DELETE_LATER;
}

declare global {
	interface Window {
		PROJECT_SERVER_DEBUG_DELETE_LATER?: ProjectServerDebugConsoleStateDEBUG_DELETE_LATER;
		PROJECT_SERVER_FETCH_ORIGINAL_DEBUG_DELETE_LATER?: typeof window.fetch;
		PROJECT_SERVER_FETCH_RESTORE_DEBUG_DELETE_LATER?: () => void;
	}
}

syncProjectServerDebugGlobalDEBUG_DELETE_LATER([]);
