import type { ChatMessage, ChatThread } from '$lib/types/chat';
import { normalizeIdentifier, normalizeMessageID, normalizeRoomIDValue } from '$lib/utils/chat/core';
import { sortThreads } from '$lib/utils/chat/threadList';

type ReadProgressParams = {
	targetRoomId: string;
	roomThreads: ChatThread[];
	messagesByRoom: Record<string, ChatMessage[]>;
	unreadAnchorByRoom: Record<string, string>;
	currentUserId: string;
};

export function findUnreadAnchorFromTail(
	roomMessages: ChatMessage[],
	unreadCount: number,
	normalizedCurrentUserID: string
) {
	if (!Array.isArray(roomMessages) || roomMessages.length === 0 || unreadCount <= 0) {
		return '';
	}
	let remainingUnread = unreadCount;
	for (let index = roomMessages.length - 1; index >= 0; index -= 1) {
		const candidate = roomMessages[index];
		const isOwnMessage =
			normalizedCurrentUserID !== '' &&
			normalizeIdentifier(candidate.senderId) === normalizedCurrentUserID;
		if (isOwnMessage) {
			continue;
		}
		remainingUnread -= 1;
		if (remainingUnread <= 0) {
			return candidate.id;
		}
	}
	return roomMessages[0]?.id ?? '';
}

export function getUnreadStartMessageId({
	targetRoomId,
	roomThreads,
	messagesByRoom,
	currentUserId
}: Omit<ReadProgressParams, 'unreadAnchorByRoom'>) {
	const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
	if (!normalizedRoomID) {
		return '';
	}
	const unread = roomThreads.find((thread) => thread.id === normalizedRoomID)?.unread ?? 0;
	if (unread <= 0) {
		return '';
	}
	const roomMessages = messagesByRoom[normalizedRoomID] ?? [];
	if (roomMessages.length === 0) {
		return '';
	}
	const normalizedCurrentUserID = normalizeIdentifier(currentUserId);
	return findUnreadAnchorFromTail(roomMessages, unread, normalizedCurrentUserID);
}

export function getLastReadTimestamp(params: Omit<ReadProgressParams, 'unreadAnchorByRoom'>) {
	const { targetRoomId, roomThreads, messagesByRoom } = params;
	const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
	if (!normalizedRoomID) {
		return Date.now();
	}
	const roomMessages = messagesByRoom[normalizedRoomID] ?? [];
	if (roomMessages.length === 0) {
		return Date.now();
	}
	const unread = roomThreads.find((thread) => thread.id === normalizedRoomID)?.unread ?? 0;
	if (unread <= 0) {
		return roomMessages[roomMessages.length - 1]?.createdAt ?? Date.now();
	}
	const anchorMessageID = getUnreadStartMessageId(params);
	if (anchorMessageID) {
		const anchorIndex = roomMessages.findIndex(
			(message) => normalizeMessageID(message.id) === normalizeMessageID(anchorMessageID)
		);
		if (anchorIndex <= 0) {
			return 0;
		}
		return roomMessages[anchorIndex - 1]?.createdAt ?? 0;
	}
	const firstUnreadIndex = Math.max(0, roomMessages.length - unread);
	if (firstUnreadIndex <= 0) {
		return 0;
	}
	return roomMessages[firstUnreadIndex - 1]?.createdAt ?? 0;
}

export function applyReadProgress(
	lastSeenMessageId: string,
	params: ReadProgressParams
): {
	changed: boolean;
	roomThreads: ChatThread[];
	unreadAnchorByRoom: Record<string, string>;
} {
	const { targetRoomId, roomThreads, messagesByRoom, unreadAnchorByRoom, currentUserId } = params;
	const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
	if (!normalizedRoomID) {
		return { changed: false, roomThreads, unreadAnchorByRoom };
	}
	const thread = roomThreads.find((entry) => entry.id === normalizedRoomID);
	const unread = thread?.unread ?? 0;
	if (unread <= 0) {
		return { changed: false, roomThreads, unreadAnchorByRoom };
	}

	const roomMessages = messagesByRoom[normalizedRoomID] ?? [];
	if (roomMessages.length === 0) {
		return { changed: false, roomThreads, unreadAnchorByRoom };
	}

	const anchorMessageID = getUnreadStartMessageId({
		targetRoomId: normalizedRoomID,
		roomThreads,
		messagesByRoom,
		currentUserId
	});
	if (!anchorMessageID) {
		return { changed: false, roomThreads, unreadAnchorByRoom };
	}
	const anchorIndex = roomMessages.findIndex(
		(message) => normalizeMessageID(message.id) === normalizeMessageID(anchorMessageID)
	);
	if (anchorIndex < 0) {
		return { changed: false, roomThreads, unreadAnchorByRoom };
	}

	const seenIndex = roomMessages.findIndex(
		(message) => normalizeMessageID(message.id) === normalizeMessageID(lastSeenMessageId)
	);
	if (seenIndex < anchorIndex) {
		return { changed: false, roomThreads, unreadAnchorByRoom };
	}
	const normalizedCurrentUserID = normalizeIdentifier(currentUserId);
	let seenUnreadCount = 0;
	for (let index = anchorIndex; index <= seenIndex; index += 1) {
		const candidate = roomMessages[index];
		const isOwnMessage =
			normalizedCurrentUserID !== '' &&
			normalizeIdentifier(candidate.senderId) === normalizedCurrentUserID;
		if (!isOwnMessage) {
			seenUnreadCount += 1;
		}
	}
	const seenCount = Math.min(unread, seenUnreadCount);
	if (seenCount <= 0) {
		return { changed: false, roomThreads, unreadAnchorByRoom };
	}

	const nextUnread = Math.max(0, unread - seenCount);
	const nextThreads = sortThreads(
		roomThreads.map((entry) => (entry.id === normalizedRoomID ? { ...entry, unread: nextUnread } : entry))
	);

	if (nextUnread <= 0) {
		const nextUnreadAnchors = { ...unreadAnchorByRoom };
		delete nextUnreadAnchors[normalizedRoomID];
		return {
			changed: true,
			roomThreads: nextThreads,
			unreadAnchorByRoom: nextUnreadAnchors
		};
	}

	const nextAnchorMessageId = findUnreadAnchorFromTail(
		roomMessages,
		nextUnread,
		normalizedCurrentUserID
	);
	if (!nextAnchorMessageId) {
		return {
			changed: true,
			roomThreads: nextThreads,
			unreadAnchorByRoom
		};
	}
	return {
		changed: true,
		roomThreads: nextThreads,
		unreadAnchorByRoom: {
			...unreadAnchorByRoom,
			[normalizedRoomID]: nextAnchorMessageId
		}
	};
}
