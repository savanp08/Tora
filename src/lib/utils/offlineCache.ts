import { browser } from '$app/environment';

const TRUSTED_DEVICE_KEY = 'converse_trusted_device';
const OFFLINE_DB_NAME = 'converse_offline_cache_v1';
const OFFLINE_STORE_NAME = 'room_cache';
const OFFLINE_DB_VERSION = 1;

type CacheRecord = {
	roomId: string;
	iv: ArrayBuffer;
	cipher: ArrayBuffer;
	updatedAt: number;
};

export type TrustedDevicePreference = 'yes' | 'no' | 'unset';

export function getTrustedDevicePreference(): TrustedDevicePreference {
	if (!browser) {
		return 'unset';
	}
	const raw = (window.localStorage.getItem(TRUSTED_DEVICE_KEY) || '').trim().toLowerCase();
	if (raw === 'yes' || raw === 'no') {
		return raw;
	}
	return 'unset';
}

export function setTrustedDevicePreference(value: 'yes' | 'no') {
	if (!browser) {
		return;
	}
	window.localStorage.setItem(TRUSTED_DEVICE_KEY, value);
}

export function isOfflineCacheSupported() {
	return Boolean(
		browser &&
			typeof window.indexedDB !== 'undefined' &&
			typeof window.crypto !== 'undefined' &&
			window.crypto.subtle
	);
}

export async function saveEncryptedRoomMessages(
	roomId: string,
	messages: unknown[],
	sessionToken: string
) {
	if (!isOfflineCacheSupported()) {
		return;
	}
	const normalizedRoomID = normalizeRoomID(roomId);
	const normalizedToken = (sessionToken || '').trim();
	if (!normalizedRoomID || !normalizedToken) {
		return;
	}

	const sourceMessages = Array.isArray(messages) ? messages : [];
	const normalMessages = sourceMessages
		.filter(
			(entry) =>
				Boolean(entry) &&
				typeof entry === 'object' &&
				(entry as { type?: unknown }).type !== 'task'
		)
		.slice(-50);
	const taskMessages = sourceMessages
		.filter(
			(entry) =>
				Boolean(entry) &&
				typeof entry === 'object' &&
				(entry as { type?: unknown }).type === 'task'
		)
		.slice(-30);
	const recentMessages = [...normalMessages, ...taskMessages].sort((left, right) => {
		const leftCreatedAt =
			typeof (left as { createdAt?: unknown })?.createdAt === 'number'
				? Number((left as { createdAt?: unknown }).createdAt)
				: 0;
		const rightCreatedAt =
			typeof (right as { createdAt?: unknown })?.createdAt === 'number'
				? Number((right as { createdAt?: unknown }).createdAt)
				: 0;
		return leftCreatedAt - rightCreatedAt;
	});
	const plaintext = JSON.stringify(recentMessages);
	const key = await deriveEncryptionKey(normalizedToken);
	const iv = window.crypto.getRandomValues(new Uint8Array(12));
	const cipher = await window.crypto.subtle.encrypt(
		{ name: 'AES-GCM', iv },
		key,
		new TextEncoder().encode(plaintext)
	);

	const record: CacheRecord = {
		roomId: normalizedRoomID,
		iv: iv.buffer,
		cipher,
		updatedAt: Date.now()
	};
	const db = await openOfflineDB();
	await withTransaction(db, OFFLINE_STORE_NAME, 'readwrite', (store) => store.put(record));
}

export async function loadEncryptedRoomMessages(roomId: string, sessionToken: string): Promise<unknown[]> {
	if (!isOfflineCacheSupported()) {
		return [];
	}
	const normalizedRoomID = normalizeRoomID(roomId);
	const normalizedToken = (sessionToken || '').trim();
	if (!normalizedRoomID || !normalizedToken) {
		return [];
	}

	const db = await openOfflineDB();
	const record = (await withTransaction(db, OFFLINE_STORE_NAME, 'readonly', (store) =>
		store.get(normalizedRoomID)
	)) as CacheRecord | undefined;
	if (!record || !record.iv || !record.cipher) {
		return [];
	}

	try {
		const key = await deriveEncryptionKey(normalizedToken);
		const plaintext = await window.crypto.subtle.decrypt(
			{ name: 'AES-GCM', iv: new Uint8Array(record.iv) },
			key,
			record.cipher
		);
		const decoded = new TextDecoder().decode(plaintext);
		const parsed = JSON.parse(decoded);
		return Array.isArray(parsed) ? parsed : [];
	} catch {
		return [];
	}
}

export async function wipeEncryptedRoomCache() {
	if (!browser || typeof window.indexedDB === 'undefined') {
		return;
	}
	await new Promise<void>((resolve) => {
		const request = window.indexedDB.deleteDatabase(OFFLINE_DB_NAME);
		request.onsuccess = () => resolve();
		request.onerror = () => resolve();
		request.onblocked = () => resolve();
	});
}

async function deriveEncryptionKey(sessionToken: string) {
	const digest = await window.crypto.subtle.digest(
		'SHA-256',
		new TextEncoder().encode(sessionToken)
	);
	return window.crypto.subtle.importKey('raw', digest, { name: 'AES-GCM' }, false, [
		'encrypt',
		'decrypt'
	]);
}

async function openOfflineDB() {
	return new Promise<IDBDatabase>((resolve, reject) => {
		const request = window.indexedDB.open(OFFLINE_DB_NAME, OFFLINE_DB_VERSION);
		request.onupgradeneeded = () => {
			const db = request.result;
			if (!db.objectStoreNames.contains(OFFLINE_STORE_NAME)) {
				db.createObjectStore(OFFLINE_STORE_NAME, { keyPath: 'roomId' });
			}
		};
		request.onsuccess = () => resolve(request.result);
		request.onerror = () => reject(request.error || new Error('IndexedDB open failed'));
	});
}

async function withTransaction(
	db: IDBDatabase,
	storeName: string,
	mode: IDBTransactionMode,
	action: (store: IDBObjectStore) => IDBRequest
) {
	return new Promise<unknown>((resolve, reject) => {
		const tx = db.transaction(storeName, mode);
		const store = tx.objectStore(storeName);
		const request = action(store);

		request.onsuccess = () => resolve(request.result);
		request.onerror = () => reject(request.error || new Error('IndexedDB request failed'));
		tx.onabort = () => reject(tx.error || new Error('IndexedDB transaction aborted'));
		tx.onerror = () => reject(tx.error || new Error('IndexedDB transaction failed'));
	});
}

function normalizeRoomID(raw: string) {
	return raw
		.toLowerCase()
		.trim()
		.replace(/[^a-z0-9]/g, '');
}
