import { normalizeIdentifier } from '$lib/utils/chat/core';

const LIGHT_SENDER_NAME_FALLBACK = '#475569';
const DARK_SENDER_NAME_FALLBACK = '#cbd5e1';
const SELF_LIGHT_SENDER_NAME_FALLBACK = '#f5f8ff';

const senderNameColorCache = new Map<string, string>();

type SenderNameColorOptions = {
	senderId: string;
	senderName: string;
	isDarkMode: boolean;
	isOwnMessage?: boolean;
};

export function resolveSenderNameColor(options: SenderNameColorOptions) {
	const normalizedSenderId = normalizeIdentifier(options.senderId || '');
	const normalizedSenderName = normalizeIdentifier(options.senderName || '');
	const identity = normalizedSenderId || normalizedSenderName;
	const isOwnMessage = options.isOwnMessage === true;
	const isDarkMode = options.isDarkMode === true;

	if (!identity) {
		if (isOwnMessage && !isDarkMode) {
			return SELF_LIGHT_SENDER_NAME_FALLBACK;
		}
		return isDarkMode ? DARK_SENDER_NAME_FALLBACK : LIGHT_SENDER_NAME_FALLBACK;
	}

	const theme = isDarkMode ? 'dark' : 'light';
	const cacheKey = `${theme}:${isOwnMessage ? 'mine' : 'peer'}:${identity}`;
	const cached = senderNameColorCache.get(cacheKey);
	if (cached) {
		return cached;
	}

	let hash = 2166136261;
	for (let index = 0; index < identity.length; index += 1) {
		hash ^= identity.charCodeAt(index);
		hash = Math.imul(hash, 16777619) >>> 0;
	}
	const hue = hash % 360;
	let saturation = isDarkMode ? 72 : 68;
	let lightness = isDarkMode ? 66 + (hash % 8) : 34 + (hash % 10);
	if (isOwnMessage && !isDarkMode) {
		saturation = 80 + (hash % 8);
		lightness = 88 + (hash % 6);
	}

	const color = `hsl(${hue} ${saturation}% ${lightness}%)`;
	senderNameColorCache.set(cacheKey, color);
	return color;
}
