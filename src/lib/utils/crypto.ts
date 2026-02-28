const E2EE_PREFIX = 'E2EE::';
const PBKDF2_ITERATIONS = 100000;
const AES_GCM_IV_LENGTH_BYTES = 12;
const TEXT_ENCODER = new TextEncoder();
const TEXT_DECODER = new TextDecoder();
const DERIVED_KEY_CACHE = new Map<string, Promise<CryptoKey>>();

export const SALT = new TextEncoder().encode('converse-e2ee-salt');

function toBase64(bytes: Uint8Array): string {
	let binary = '';
	const chunkSize = 0x8000;
	for (let offset = 0; offset < bytes.length; offset += chunkSize) {
		const chunk = bytes.subarray(offset, offset + chunkSize);
		binary += String.fromCharCode(...chunk);
	}
	return btoa(binary);
}

function fromBase64(value: string): Uint8Array {
	const binary = atob(value);
	const bytes = new Uint8Array(binary.length);
	for (let index = 0; index < binary.length; index += 1) {
		bytes[index] = binary.charCodeAt(index);
	}
	return bytes;
}

function toArrayBuffer(bytes: Uint8Array): ArrayBuffer {
	return new Uint8Array(bytes).buffer;
}

function hasWebCrypto() {
	return typeof window !== 'undefined' && Boolean(window.crypto?.subtle);
}

export async function deriveKey(password: string): Promise<CryptoKey> {
	const normalizedPassword = (password || '').trim();
	if (!normalizedPassword) {
		throw new Error('Password is required');
	}
	if (!hasWebCrypto()) {
		throw new Error('Web Crypto API unavailable');
	}
	const cachedKeyPromise = DERIVED_KEY_CACHE.get(normalizedPassword);
	if (cachedKeyPromise) {
		return cachedKeyPromise;
	}

	const derivedKeyPromise = (async () => {
		const passwordKey = await window.crypto.subtle.importKey(
			'raw',
			TEXT_ENCODER.encode(normalizedPassword),
			'PBKDF2',
			false,
			['deriveKey']
		);

		return window.crypto.subtle.deriveKey(
			{
				name: 'PBKDF2',
				salt: SALT,
				iterations: PBKDF2_ITERATIONS,
				hash: 'SHA-256'
			},
			passwordKey,
			{
				name: 'AES-GCM',
				length: 256
			},
			false,
			['encrypt', 'decrypt']
		);
	})();
	DERIVED_KEY_CACHE.set(normalizedPassword, derivedKeyPromise);
	try {
		return await derivedKeyPromise;
	} catch (error) {
		DERIVED_KEY_CACHE.delete(normalizedPassword);
		throw error;
	}
}

export async function encryptText(text: string, password: string): Promise<string> {
	const normalizedPassword = (password || '').trim();
	if (!normalizedPassword) {
		return text;
	}
	try {
		const key = await deriveKey(normalizedPassword);
		if (!hasWebCrypto()) {
			return text;
		}
		const iv = window.crypto.getRandomValues(new Uint8Array(AES_GCM_IV_LENGTH_BYTES));
		const cipherBuffer = await window.crypto.subtle.encrypt(
			{ name: 'AES-GCM', iv },
			key,
			TEXT_ENCODER.encode(text)
		);
		const cipherBytes = new Uint8Array(cipherBuffer);
		return `${E2EE_PREFIX}${toBase64(iv)}:${toBase64(cipherBytes)}`;
	} catch {
		return text;
	}
}

export async function decryptText(encryptedPayload: string, password: string): Promise<string> {
	const payload = typeof encryptedPayload === 'string' ? encryptedPayload : '';
	if (!payload.startsWith(E2EE_PREFIX)) {
		return payload;
	}

	const normalizedPassword = (password || '').trim();
	if (!normalizedPassword || !hasWebCrypto()) {
		return '[Encrypted Message]';
	}

	try {
		const raw = payload.slice(E2EE_PREFIX.length);
		const separatorIndex = raw.indexOf(':');
		if (separatorIndex <= 0 || separatorIndex >= raw.length - 1) {
			return '[Encrypted Message]';
		}

		const ivBase64 = raw.slice(0, separatorIndex);
		const cipherBase64 = raw.slice(separatorIndex + 1);
		const iv = fromBase64(ivBase64);
		const cipher = fromBase64(cipherBase64);
		const key = await deriveKey(normalizedPassword);
		const plainBuffer = await window.crypto.subtle.decrypt(
			{ name: 'AES-GCM', iv: toArrayBuffer(iv) },
			key,
			toArrayBuffer(cipher)
		);
		return TEXT_DECODER.decode(plainBuffer);
	} catch {
		return '[Encrypted Message]';
	}
}
