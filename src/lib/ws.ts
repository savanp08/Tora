import { browser } from '$app/environment';
import { resolveApiBase } from '$lib/config/apiBase';
import { APP_LIMITS } from '$lib/config/limits';
import { recordProjectServerDataDEBUG_DELETE_LATER } from '$lib/debug/projectServerDataDEBUG_DELETE_LATER';
import { applyServerTier } from '$lib/stores/auth';
import {
	syncBoardActivityFromSocketPayload,
	syncFieldSchemaStoreFromSocketPayload,
	syncTaskStoreFromSocketPayload
} from '$lib/ws/client';
import { writable } from 'svelte/store';

export type GlobalSocketState = 'idle' | 'connecting' | 'open' | 'closed' | 'error';

export type GlobalSocketEvent = {
	payload: unknown;
	receivedAt: number;
};

const MAX_QUEUED_MESSAGES = APP_LIMITS.ws.maxQueuedMessages;
const DEFAULT_API_BASE = 'http://localhost:8080';

export const socketState = writable<GlobalSocketState>('idle');
export const globalMessages = writable<GlobalSocketEvent | null>(null);

let socket: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectAttempts = 0;
let shouldReconnect = false;
let activeUserID = '';
let activeUsername = '';
let lastSubscriptionSignature = '';

const subscribedRoomIDs = new Set<string>();
const outboundQueue: string[] = [];

function toSocketRecordDEBUG_DELETE_LATER(value: unknown): Record<string, unknown> | null {
	if (!value || typeof value !== 'object' || Array.isArray(value)) {
		return null;
	}
	return value as Record<string, unknown>;
}

function toSocketStringDEBUG_DELETE_LATER(value: unknown) {
	return typeof value === 'string' ? value.trim() : '';
}

function extractSocketEventTypeDEBUG_DELETE_LATER(payload: unknown) {
	const source = toSocketRecordDEBUG_DELETE_LATER(payload);
	return toSocketStringDEBUG_DELETE_LATER(source?.type) || undefined;
}

function extractSocketRoomIdDEBUG_DELETE_LATER(payload: unknown) {
	const source = toSocketRecordDEBUG_DELETE_LATER(payload);
	const nestedPayload = toSocketRecordDEBUG_DELETE_LATER(source?.payload);
	return (
		toSocketStringDEBUG_DELETE_LATER(source?.roomId) ||
		toSocketStringDEBUG_DELETE_LATER(source?.room_id) ||
		toSocketStringDEBUG_DELETE_LATER(nestedPayload?.roomId) ||
		toSocketStringDEBUG_DELETE_LATER(nestedPayload?.room_id) ||
		undefined
	);
}

export function initGlobalSocket(userId: string, username: string) {
	if (!browser) {
		return;
	}

	const nextUserID = normalizeIdentifier(userId) || 'guest';
	const nextUsername = normalizeIdentifier(username) || 'Guest';
	const identityChanged = nextUserID !== activeUserID || nextUsername !== activeUsername;

	activeUserID = nextUserID;
	activeUsername = nextUsername;
	shouldReconnect = true;

	if (identityChanged && socket) {
		disconnectSocket();
	}
	connectSocket();
}

type SubscribeToRoomsOptions = {
	force?: boolean;
};

export function subscribeToRooms(roomIds: string[], options: SubscribeToRoomsOptions = {}) {
	if (!browser || !Array.isArray(roomIds)) {
		return;
	}

	const shouldForce = options.force === true;
	for (const roomId of roomIds) {
		const normalizedRoomID = normalizeRoomID(roomId);
		if (!normalizedRoomID) {
			continue;
		}
		subscribedRoomIDs.add(normalizedRoomID);
	}
	sendSubscriptions(shouldForce);
}

export function sendSocketPayload(payload: unknown) {
	if (!browser || payload === undefined || payload === null) {
		return false;
	}

	const encoded = safeStringify(payload);
	if (!encoded) {
		return false;
	}

	if (socket && socket.readyState === WebSocket.OPEN) {
		socket.send(encoded);
		return true;
	}

	queueOutbound(encoded);
	connectSocket();
	return false;
}

export function closeGlobalSocket() {
	if (!browser) {
		return;
	}
	shouldReconnect = false;
	clearReconnectTimer();
	disconnectSocket();
	socketState.set('idle');
}

function connectSocket() {
	if (!browser || !activeUserID) {
		return;
	}
	if (
		socket &&
		(socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)
	) {
		return;
	}

	clearReconnectTimer();
	socketState.set('connecting');

	const socketURL = buildSocketURL(activeUserID, activeUsername);
	const nextSocket = new WebSocket(socketURL);
	socket = nextSocket;

	nextSocket.onopen = () => {
		if (socket !== nextSocket) {
			return;
		}
		reconnectAttempts = 0;
		socketState.set('open');
		sendSubscriptions(true);
		flushOutboundQueue();
	};

	nextSocket.onmessage = (event: MessageEvent) => {
		if (socket !== nextSocket) {
			return;
		}
		const payload = parseMessagePayload(event.data);
		recordProjectServerDataDEBUG_DELETE_LATER({
			source: `socket:${extractSocketEventTypeDEBUG_DELETE_LATER(payload) || 'message'}`,
			direction: 'response',
			roomId: extractSocketRoomIdDEBUG_DELETE_LATER(payload),
			endpoint: 'websocket:/ws',
			event: extractSocketEventTypeDEBUG_DELETE_LATER(payload),
			payload
		});
		syncTaskStoreFromSocketPayload(payload);
		syncBoardActivityFromSocketPayload(payload);
		syncFieldSchemaStoreFromSocketPayload(payload);

		// Handle server-resolved tier on connect
		if (
			payload &&
			typeof payload === 'object' &&
			(payload as Record<string, unknown>).type === 'session_info'
		) {
			const raw = ((payload as Record<string, unknown>).tier as string | undefined)?.trim().toLowerCase();
			const validTiers = ['free', 'plus', 'pro', 'team'] as const;
			const tier = validTiers.includes(raw as (typeof validTiers)[number])
				? (raw as (typeof validTiers)[number])
				: 'free';
			console.log(`[session] tier from server: ${tier}`);
			applyServerTier(tier);
		}

		globalMessages.set({
			payload,
			receivedAt: Date.now()
		});
	};

	nextSocket.onerror = () => {
		if (socket !== nextSocket) {
			return;
		}
		socketState.set('error');
		if (import.meta.env.DEV) {
			console.warn('[ws] connect error', { socketURL });
		}
	};

	nextSocket.onclose = (event: CloseEvent) => {
		if (socket !== nextSocket) {
			return;
		}
		socket = null;
		socketState.set('closed');
		if (import.meta.env.DEV) {
			console.warn('[ws] closed', {
				socketURL,
				code: event.code,
				reason: event.reason,
				wasClean: event.wasClean
			});
		}
		if (shouldReconnect) {
			scheduleReconnect();
		}
	};
}

function disconnectSocket() {
	if (!socket) {
		return;
	}

	const active = socket;
	socket = null;
	active.onopen = null;
	active.onmessage = null;
	active.onclose = null;
	active.onerror = null;

	if (active.readyState === WebSocket.OPEN || active.readyState === WebSocket.CONNECTING) {
		active.close();
	}
}

function scheduleReconnect() {
	clearReconnectTimer();
	reconnectAttempts = Math.min(reconnectAttempts + 1, 8);
	const baseDelay = Math.min(1000 * 2 ** (reconnectAttempts - 1), 60000);
	const jitter = Math.floor(Math.random() * 2000);
	const delay = baseDelay + jitter;
	reconnectTimer = setTimeout(() => {
		connectSocket();
	}, delay);
}

function clearReconnectTimer() {
	if (!reconnectTimer) {
		return;
	}
	clearTimeout(reconnectTimer);
	reconnectTimer = null;
}

function sendSubscriptions(force = false) {
	const roomIDs = [...subscribedRoomIDs].sort();
	if (roomIDs.length === 0) {
		return;
	}
	const signature = roomIDs.join(',');
	if (!force && signature === lastSubscriptionSignature) {
		return;
	}
	lastSubscriptionSignature = signature;

	if (!socket || socket.readyState !== WebSocket.OPEN) {
		return;
	}

	const encoded = safeStringify({
		type: 'subscribe',
		payload: roomIDs
	});
	if (!encoded) {
		return;
	}
	socket.send(encoded);
}

function queueOutbound(payload: string) {
	outboundQueue.push(payload);
	if (outboundQueue.length > MAX_QUEUED_MESSAGES) {
		outboundQueue.shift();
	}
}

function flushOutboundQueue() {
	if (!socket || socket.readyState !== WebSocket.OPEN) {
		return;
	}

	while (outboundQueue.length > 0) {
		const payload = outboundQueue.shift();
		if (!payload) {
			continue;
		}
		socket.send(payload);
	}
}

function parseMessagePayload(raw: unknown) {
	if (typeof raw !== 'string') {
		return raw;
	}
	try {
		return JSON.parse(raw);
	} catch {
		return raw;
	}
}

function safeStringify(payload: unknown) {
	try {
		return JSON.stringify(payload);
	} catch {
		return '';
	}
}

function buildSocketURL(userId: string, username: string) {
	const explicitWSBase = toNonEmpty(import.meta.env.VITE_WS_BASE as string | undefined);
	const apiBase = resolveApiBase(import.meta.env.VITE_API_BASE as string | undefined);
	const baseURL = explicitWSBase ? toWebSocketURL(explicitWSBase) : toWebSocketURL(apiBase);

	const path = baseURL.pathname.replace(/\/+$/g, '');
	if (path === '' || path === '/') {
		baseURL.pathname = '/ws';
	} else if (!path.endsWith('/ws')) {
		baseURL.pathname = `${path}/ws`;
	} else {
		baseURL.pathname = path;
	}

	baseURL.searchParams.set('userId', userId);
	baseURL.searchParams.set('username', username);
	return baseURL.toString();
}

function toWebSocketURL(raw: string) {
	const parsed = new URL(raw, browser ? window.location.origin : DEFAULT_API_BASE);
	if (parsed.protocol === 'http:') {
		parsed.protocol = 'ws:';
	} else if (parsed.protocol === 'https:') {
		parsed.protocol = 'wss:';
	}
	return parsed;
}

function normalizeRoomID(raw: string) {
	return raw
		.toLowerCase()
		.trim()
		.replace(/[^a-z0-9]/g, '');
}

function normalizeIdentifier(raw: string) {
	return raw
		.trim()
		.replace(/[^a-zA-Z0-9\s_-]/g, '')
		.replace(/[\s-]+/g, '_')
		.replace(/_+/g, '_')
		.replace(/^_+|_+$/g, '');
}

function toNonEmpty(value: string | undefined) {
	const normalized = (value ?? '').trim();
	return normalized || '';
}
