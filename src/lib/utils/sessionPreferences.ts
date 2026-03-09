import { browser } from '$app/environment';

export type SessionRoomPreferences = {
	aiEnabled: boolean;
	e2eEnabled: boolean;
};

export type SessionChatLayoutPreferences = {
	onlinePanelCollapsed: boolean;
};

const SESSION_ROOM_PREFERENCES_KEY = 'converse_session_room_preferences_v1';
const SESSION_CHAT_LAYOUT_PREFERENCES_KEY = 'converse_session_chat_layout_preferences_v1';
const DEFAULT_SESSION_ROOM_PREFERENCES: SessionRoomPreferences = {
	aiEnabled: true,
	e2eEnabled: false
};
const DEFAULT_SESSION_CHAT_LAYOUT_PREFERENCES: SessionChatLayoutPreferences = {
	onlinePanelCollapsed: false
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

function normalizeSessionChatLayoutPreferences(
	value: Partial<SessionChatLayoutPreferences> | null | undefined
): SessionChatLayoutPreferences {
	return {
		onlinePanelCollapsed:
			typeof value?.onlinePanelCollapsed === 'boolean'
				? value.onlinePanelCollapsed
				: DEFAULT_SESSION_CHAT_LAYOUT_PREFERENCES.onlinePanelCollapsed
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

export function getDefaultSessionChatLayoutPreferences(): SessionChatLayoutPreferences {
	return { ...DEFAULT_SESSION_CHAT_LAYOUT_PREFERENCES };
}

export function readSessionChatLayoutPreferences(): SessionChatLayoutPreferences {
	if (!browser) {
		return getDefaultSessionChatLayoutPreferences();
	}
	const raw = window.sessionStorage.getItem(SESSION_CHAT_LAYOUT_PREFERENCES_KEY);
	if (!raw) {
		return getDefaultSessionChatLayoutPreferences();
	}
	try {
		const parsed = JSON.parse(raw) as Partial<SessionChatLayoutPreferences>;
		return normalizeSessionChatLayoutPreferences(parsed);
	} catch {
		return getDefaultSessionChatLayoutPreferences();
	}
}

export function writeSessionChatLayoutPreferences(
	value: Partial<SessionChatLayoutPreferences>
): SessionChatLayoutPreferences {
	const normalized = normalizeSessionChatLayoutPreferences(value);
	if (browser) {
		window.sessionStorage.setItem(SESSION_CHAT_LAYOUT_PREFERENCES_KEY, JSON.stringify(normalized));
	}
	return normalized;
}
