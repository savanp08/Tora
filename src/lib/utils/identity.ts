import { generateUsername } from '$lib/utils/usernameGenerator';

const IDENTITY_STORAGE_KEY = 'chat_identity';

export interface UserIdentity {
	id: string;
	username: string;
}

function createIdentity(): UserIdentity {
	const id =
		typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
			? crypto.randomUUID()
			: `user_${Date.now()}_${Math.floor(Math.random() * 1000000)}`;

	return {
		id,
		username: generateUsername()
	};
}

function isValidIdentity(value: unknown): value is UserIdentity {
	if (!value || typeof value !== 'object') {
		return false;
	}

	const candidate = value as Record<string, unknown>;
	return (
		typeof candidate.id === 'string' &&
		candidate.id !== '' &&
		typeof candidate.username === 'string' &&
		candidate.username !== ''
	);
}

export function getOrInitIdentity(): UserIdentity {
	if (typeof window === 'undefined') {
		return createIdentity();
	}

	// Per-tab identity: survives reload in this tab, differs across tabs.
	const rawIdentity = window.sessionStorage.getItem(IDENTITY_STORAGE_KEY);
	if (rawIdentity) {
		try {
			const parsed = JSON.parse(rawIdentity);
			if (isValidIdentity(parsed)) {
				return parsed;
			}
		} catch {
			// ignore malformed values and regenerate
		}
	}

	const freshIdentity = createIdentity();
	window.sessionStorage.setItem(IDENTITY_STORAGE_KEY, JSON.stringify(freshIdentity));
	return freshIdentity;
}

export function updateUsername(name: string): UserIdentity {
	const identity = getOrInitIdentity();
	const normalized = name.trim().replace(/[\s-]+/g, '_');
	const nextIdentity: UserIdentity = {
		...identity,
		username: normalized || identity.username
	};

	if (typeof window !== 'undefined') {
		window.sessionStorage.setItem(IDENTITY_STORAGE_KEY, JSON.stringify(nextIdentity));
	}

	return nextIdentity;
}
