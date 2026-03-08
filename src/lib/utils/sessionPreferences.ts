import { browser } from '$app/environment';

export type SessionRoomPreferences = {
	aiEnabled: boolean;
	e2eEnabled: boolean;
};

const SESSION_ROOM_PREFERENCES_KEY = 'converse_session_room_preferences_v1';
const DEFAULT_SESSION_ROOM_PREFERENCES: SessionRoomPreferences = {
	aiEnabled: true,
	e2eEnabled: false
};

function normalizeSessionRoomPreferences(
	value: Partial<SessionRoomPreferences> | null | undefined
): SessionRoomPreferences {
	const aiEnabled =
		typeof value?.aiEnabled === 'boolean'
			? value.aiEnabled
			: DEFAULT_SESSION_ROOM_PREFERENCES.aiEnabled;
	const e2eEnabled =
		typeof value?.e2eEnabled === 'boolean'
			? value.e2eEnabled
			: DEFAULT_SESSION_ROOM_PREFERENCES.e2eEnabled;
	if (e2eEnabled) {
		return {
			aiEnabled: false,
			e2eEnabled: true
		};
	}
	return {
		aiEnabled,
		e2eEnabled: false
	};
}

export function getDefaultSessionRoomPreferences(): SessionRoomPreferences {
	return { ...DEFAULT_SESSION_ROOM_PREFERENCES };
}

export function readSessionRoomPreferences(): SessionRoomPreferences {
	if (!browser) {
		return getDefaultSessionRoomPreferences();
	}
	const raw = window.sessionStorage.getItem(SESSION_ROOM_PREFERENCES_KEY);
	if (!raw) {
		return getDefaultSessionRoomPreferences();
	}
	try {
		const parsed = JSON.parse(raw) as Partial<SessionRoomPreferences>;
		return normalizeSessionRoomPreferences(parsed);
	} catch {
		return getDefaultSessionRoomPreferences();
	}
}

export function writeSessionRoomPreferences(
	value: Partial<SessionRoomPreferences>
): SessionRoomPreferences {
	const normalized = normalizeSessionRoomPreferences(value);
	if (browser) {
		window.sessionStorage.setItem(SESSION_ROOM_PREFERENCES_KEY, JSON.stringify(normalized));
	}
	return normalized;
}
