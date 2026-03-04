type TypingUsersByRoom = Record<string, Record<string, { name: string; expiresAt: number }>>;

type TypingControllerParams = {
	getRoomId: () => string;
	getIsMember: () => boolean;
	getTypingUsersByRoom: () => TypingUsersByRoom;
	setTypingUsersByRoom: (next: TypingUsersByRoom) => void;
	normalizeIdentifier: (value: string) => string;
	sendSocketPayload: (payload: Record<string, unknown>) => void;
	typingPingIntervalMs: number;
	typingStopDelayMs: number;
	typingSafetyTimeoutMs: number;
};

export function createTypingController({
	getRoomId,
	getIsMember,
	getTypingUsersByRoom,
	setTypingUsersByRoom,
	normalizeIdentifier,
	sendSocketPayload,
	typingPingIntervalMs,
	typingStopDelayMs,
	typingSafetyTimeoutMs
}: TypingControllerParams) {
	let typingStopTimer: ReturnType<typeof setTimeout> | null = null;
	let typingIsActive = false;
	let typingLastPingAt = 0;
	let typingSafetyTimers = new Map<string, ReturnType<typeof setTimeout>>();

	function typingTimerKey(targetRoomId: string, userId: string) {
		return `${targetRoomId}:${userId}`;
	}

	function clearTypingStopTimer() {
		if (typingStopTimer) {
			clearTimeout(typingStopTimer);
			typingStopTimer = null;
		}
	}

	function sendTypingStart() {
		const roomId = getRoomId();
		const isMember = getIsMember();
		if (!roomId || !isMember) {
			return;
		}
		const now = Date.now();
		if (typingIsActive && now - typingLastPingAt < typingPingIntervalMs) {
			return;
		}
		typingIsActive = true;
		typingLastPingAt = now;
		sendSocketPayload({
			type: 'typing_start',
			roomId
		});
	}

	function sendTypingStop() {
		const roomId = getRoomId();
		const isMember = getIsMember();
		if (!typingIsActive || !roomId || !isMember) {
			clearTypingStopTimer();
			typingIsActive = false;
			return;
		}
		typingIsActive = false;
		typingLastPingAt = 0;
		clearTypingStopTimer();
		sendSocketPayload({
			type: 'typing_stop',
			roomId
		});
	}

	function scheduleTypingStop() {
		clearTypingStopTimer();
		typingStopTimer = setTimeout(() => {
			sendTypingStop();
		}, typingStopDelayMs);
	}

	function onComposerTyping(value: string) {
		if (!value) {
			sendTypingStop();
			return;
		}
		sendTypingStart();
		scheduleTypingStop();
	}

	function clearAllTypingSafetyTimers() {
		for (const timer of typingSafetyTimers.values()) {
			clearTimeout(timer);
		}
		typingSafetyTimers = new Map<string, ReturnType<typeof setTimeout>>();
	}

	function clearTypingIndicator(targetRoomId: string, userId: string) {
		if (!targetRoomId || !userId) {
			return;
		}
		const typingUsersByRoom = getTypingUsersByRoom();
		const roomIndicators = typingUsersByRoom[targetRoomId];
		if (!roomIndicators || !roomIndicators[userId]) {
			return;
		}

		const nextRoomIndicators = { ...roomIndicators };
		delete nextRoomIndicators[userId];
		const nextTypingByRoom = { ...typingUsersByRoom };
		if (Object.keys(nextRoomIndicators).length === 0) {
			delete nextTypingByRoom[targetRoomId];
		} else {
			nextTypingByRoom[targetRoomId] = nextRoomIndicators;
		}
		setTypingUsersByRoom(nextTypingByRoom);

		const key = typingTimerKey(targetRoomId, userId);
		const existing = typingSafetyTimers.get(key);
		if (existing) {
			clearTimeout(existing);
			typingSafetyTimers.delete(key);
		}
	}

	function setTypingIndicator(
		targetRoomId: string,
		userId: string,
		userName: string,
		expiresAt: number = Date.now() + typingSafetyTimeoutMs
	) {
		if (!targetRoomId || !userId) {
			return;
		}
		const typingUsersByRoom = getTypingUsersByRoom();
		const roomIndicators = typingUsersByRoom[targetRoomId] ?? {};
		setTypingUsersByRoom({
			...typingUsersByRoom,
			[targetRoomId]: {
				...roomIndicators,
				[userId]: {
					name: userName || 'User',
					expiresAt
				}
			}
		});

		const key = typingTimerKey(targetRoomId, userId);
		const existing = typingSafetyTimers.get(key);
		if (existing) {
			clearTimeout(existing);
		}
		const timer = setTimeout(() => {
			clearTypingIndicator(targetRoomId, userId);
		}, typingSafetyTimeoutMs);
		typingSafetyTimers.set(key, timer);
	}

	function getActiveTypingUsers(targetRoomId: string, currentUserId: string) {
		if (!targetRoomId) {
			return [];
		}
		const roomIndicators = getTypingUsersByRoom()[targetRoomId] ?? {};
		const now = Date.now();
		const active = Object.entries(roomIndicators)
			.filter(([userId, entry]) => {
				if (!entry || entry.expiresAt <= now) {
					clearTypingIndicator(targetRoomId, userId);
					return false;
				}
				return normalizeIdentifier(userId) !== normalizeIdentifier(currentUserId);
			})
			.map(([, entry]) => entry.name);
		return active;
	}

	function destroy() {
		clearTypingStopTimer();
		clearAllTypingSafetyTimers();
	}

	return {
		destroy,
		sendTypingStop,
		onComposerTyping,
		setTypingIndicator,
		clearTypingIndicator,
		getActiveTypingUsers
	};
}
