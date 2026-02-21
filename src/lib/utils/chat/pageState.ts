import type { ChatMessage, ChatThread, OnlineMember, RoomMeta, ThreadStatus } from '$lib/types/chat';
import {
	normalizeMessageID,
	parseOptionalTimestamp,
	toStringValue
} from '$lib/utils/chat/core';
import { sortThreads } from '$lib/utils/chat/threadList';

type ThreadPreviewDeps = {
	formatRoomName: (roomId: string) => string;
	getMessagePreviewText: (message: ChatMessage) => string;
	createThread: (id: string, nameOverride?: string, status?: ThreadStatus) => ChatThread;
};

type MessageStoreUpdateDeps = ThreadPreviewDeps;

export function createThread(
	id: string,
	formatRoomName: (roomId: string) => string,
	nameOverride?: string,
	status: ThreadStatus = 'joined'
): ChatThread {
	return {
		id,
		name: nameOverride ?? formatRoomName(id),
		lastMessage: '',
		lastActivity: Date.now(),
		unread: 0,
		status,
		isAdmin: false
	};
}

export function ensureRoomThread(
	roomThreads: ChatThread[],
	targetRoomId: string,
	deps: Pick<ThreadPreviewDeps, 'createThread'>,
	nameOverride?: string,
	status: ThreadStatus = 'joined'
) {
	const existing = roomThreads.find((thread) => thread.id === targetRoomId);
	if (existing) {
		const nextName = nameOverride || existing.name;
		let nextStatus: ThreadStatus = existing.status;
		if (status === 'joined') {
			nextStatus = 'joined';
		} else if (existing.status !== 'joined' && existing.status !== 'left') {
			nextStatus = status;
		}
		if (nextName === existing.name && nextStatus === existing.status) {
			return roomThreads;
		}

		return sortThreads(
			roomThreads.map((thread) =>
				thread.id === targetRoomId
					? {
							...thread,
							name: nextName,
							status: nextStatus
						}
					: thread
			)
		);
	}

	return sortThreads([deps.createThread(targetRoomId, nameOverride, status), ...roomThreads]);
}

export function ensureRoomMeta(
	roomMetaById: Record<string, RoomMeta>,
	targetRoomId: string,
	createdAt: number,
	expiresAt = 0
) {
	if (!targetRoomId) {
		return roomMetaById;
	}
	const existing = roomMetaById[targetRoomId];
	const safeCreatedAt =
		Number.isFinite(createdAt) && createdAt > 0 ? createdAt : (existing?.createdAt ?? 0);
	const safeExpiresAt =
		Number.isFinite(expiresAt) && expiresAt > 0 ? expiresAt : (existing?.expiresAt ?? 0);
	if (existing && existing.createdAt === safeCreatedAt && existing.expiresAt === safeExpiresAt) {
		return roomMetaById;
	}
	return {
		...roomMetaById,
		[targetRoomId]: {
			createdAt: safeCreatedAt,
			expiresAt: safeExpiresAt
		}
	};
}

export function dedupeMembers(members: OnlineMember[]) {
	const byId = new Map<string, OnlineMember>();
	for (const member of members) {
		byId.set(member.id, member);
	}
	return [...byId.values()];
}

export function ensureOnlineSeed(
	onlineByRoom: Record<string, OnlineMember[]>,
	targetRoomId: string,
	currentUserId: string,
	currentUsername: string
) {
	if (onlineByRoom[targetRoomId]?.length) {
		return onlineByRoom;
	}
	return {
		...onlineByRoom,
		[targetRoomId]: dedupeMembers([
			{
				id: currentUserId,
				name: currentUsername,
				isOnline: true,
				joinedAt: Date.now()
			}
		])
	};
}

export function updateThreadPreview(
	roomThreads: ChatThread[],
	messagesByRoom: Record<string, ChatMessage[]>,
	targetRoomId: string,
	deps: ThreadPreviewDeps
) {
	const roomMessages = messagesByRoom[targetRoomId] ?? [];
	const lastMessage = roomMessages[roomMessages.length - 1];
	const fallbackName = deps.formatRoomName(targetRoomId);
	if (!lastMessage) {
		return ensureRoomThread(roomThreads, targetRoomId, { createThread: deps.createThread }, fallbackName, 'joined');
	}

	const merged = roomThreads.some((thread) => thread.id === targetRoomId)
		? roomThreads.map((thread) =>
				thread.id === targetRoomId
					? {
							...thread,
							name: thread.name || fallbackName,
							lastMessage: deps.getMessagePreviewText(lastMessage),
							lastActivity: lastMessage.createdAt
						}
					: thread
			)
		: [
				{
					...deps.createThread(targetRoomId, fallbackName, 'joined'),
					lastMessage: deps.getMessagePreviewText(lastMessage),
					lastActivity: lastMessage.createdAt
				},
				...roomThreads
			];
	return sortThreads(merged);
}

export function markRoomAsRead(roomThreads: ChatThread[], targetRoomId: string) {
	if (!targetRoomId) {
		return roomThreads;
	}
	return sortThreads(
		roomThreads.map((thread) => (thread.id === targetRoomId ? { ...thread, unread: 0 } : thread))
	);
}

export function upsertOnlineMember(
	onlineByRoom: Record<string, OnlineMember[]>,
	targetRoomId: string,
	member: OnlineMember
) {
	const members = onlineByRoom[targetRoomId] ?? [];
	const existingIndex = members.findIndex((entry) => entry.id === member.id);
	let next: OnlineMember[];
	if (existingIndex >= 0) {
		next = [...members];
		next[existingIndex] = { ...next[existingIndex], ...member, isOnline: true };
	} else {
		next = [...members, { ...member, isOnline: true }];
	}
	return {
		...onlineByRoom,
		[targetRoomId]: dedupeMembers(next)
	};
}

export function removeOnlineMember(
	onlineByRoom: Record<string, OnlineMember[]>,
	targetRoomId: string,
	memberId: string
) {
	const members = onlineByRoom[targetRoomId] ?? [];
	return {
		...onlineByRoom,
		[targetRoomId]: members.filter((member) => member.id !== memberId)
	};
}

export function upsertMessageState(
	messagesByRoom: Record<string, ChatMessage[]>,
	roomThreads: ChatThread[],
	targetRoomId: string,
	message: ChatMessage,
	shouldCountUnread: boolean,
	deps: MessageStoreUpdateDeps
) {
	const roomMessages = messagesByRoom[targetRoomId] ?? [];
	const existingIndex = roomMessages.findIndex((entry) => entry.id === message.id);

	let nextMessages: ChatMessage[];
	if (existingIndex >= 0) {
		nextMessages = [...roomMessages];
		nextMessages[existingIndex] = {
			...nextMessages[existingIndex],
			...message,
			pending: false
		};
	} else {
		nextMessages = [...roomMessages, message];
	}

	nextMessages.sort((a, b) => a.createdAt - b.createdAt);
	const nextMessagesByRoom = {
		...messagesByRoom,
		[targetRoomId]: nextMessages
	};

	let nextThreads = updateThreadPreview(roomThreads, nextMessagesByRoom, targetRoomId, deps);
	if (shouldCountUnread) {
		nextThreads = sortThreads(
			nextThreads.map((thread) =>
				thread.id === targetRoomId ? { ...thread, unread: thread.unread + 1 } : thread
			)
		);
	}
	return {
		messagesByRoom: nextMessagesByRoom,
		roomThreads: nextThreads
	};
}

export function mergeMessagesState(
	messagesByRoom: Record<string, ChatMessage[]>,
	roomThreads: ChatThread[],
	targetRoomId: string,
	incoming: ChatMessage[],
	deps: MessageStoreUpdateDeps
) {
	if (incoming.length === 0) {
		return { messagesByRoom, roomThreads };
	}
	const existing = messagesByRoom[targetRoomId] ?? [];
	const merged = new Map<string, ChatMessage>();
	for (const message of existing) {
		merged.set(message.id, message);
	}
	for (const message of incoming) {
		const current = merged.get(message.id);
		merged.set(message.id, { ...current, ...message, pending: false });
	}
	const nextMessages = [...merged.values()].sort((a, b) => a.createdAt - b.createdAt);
	const nextMessagesByRoom = {
		...messagesByRoom,
		[targetRoomId]: nextMessages
	};
	return {
		messagesByRoom: nextMessagesByRoom,
		roomThreads: updateThreadPreview(roomThreads, nextMessagesByRoom, targetRoomId, deps)
	};
}

export function applyMessageEditState(
	messagesByRoom: Record<string, ChatMessage[]>,
	roomThreads: ChatThread[],
	targetRoomId: string,
	payload: unknown,
	deps: MessageStoreUpdateDeps
) {
	if (!payload || typeof payload !== 'object') {
		return { messagesByRoom, roomThreads, changed: false };
	}
	const source = payload as Record<string, unknown>;
	const messageId = normalizeMessageID(toStringValue(source.messageId ?? source.id));
	const nextContent = toStringValue(source.content).trim();
	const editedAt = parseOptionalTimestamp(source.editedAt ?? source.edited_at ?? Date.now());
	if (!messageId || !nextContent) {
		return { messagesByRoom, roomThreads, changed: false };
	}
	const roomMessages = messagesByRoom[targetRoomId] ?? [];
	const index = roomMessages.findIndex((entry) => entry.id === messageId);
	if (index < 0) {
		return { messagesByRoom, roomThreads, changed: false };
	}
	const nextMessages = [...roomMessages];
	nextMessages[index] = {
		...nextMessages[index],
		content: nextContent,
		type: 'text',
		mediaUrl: '',
		mediaType: '',
		fileName: '',
		isEdited: true,
		editedAt,
		isDeleted: false,
		pending: false
	};
	const nextMessagesByRoom = {
		...messagesByRoom,
		[targetRoomId]: nextMessages
	};
	return {
		messagesByRoom: nextMessagesByRoom,
		roomThreads: updateThreadPreview(roomThreads, nextMessagesByRoom, targetRoomId, deps),
		changed: true
	};
}

export function applyMessageDeleteState(
	messagesByRoom: Record<string, ChatMessage[]>,
	roomThreads: ChatThread[],
	targetRoomId: string,
	payload: unknown,
	deletedPlaceholder: string,
	deps: MessageStoreUpdateDeps
) {
	if (!payload || typeof payload !== 'object') {
		return { messagesByRoom, roomThreads, changed: false };
	}
	const source = payload as Record<string, unknown>;
	const messageId = normalizeMessageID(toStringValue(source.messageId ?? source.id));
	if (!messageId) {
		return { messagesByRoom, roomThreads, changed: false };
	}
	const roomMessages = messagesByRoom[targetRoomId] ?? [];
	const index = roomMessages.findIndex((entry) => entry.id === messageId);
	if (index < 0) {
		return { messagesByRoom, roomThreads, changed: false };
	}
	const nextMessages = [...roomMessages];
	nextMessages[index] = {
		...nextMessages[index],
		content: deletedPlaceholder,
		type: 'deleted',
		mediaUrl: '',
		mediaType: '',
		fileName: '',
		replyToMessageId: '',
		replyToSnippet: '',
		isEdited: false,
		editedAt: parseOptionalTimestamp(source.editedAt ?? source.edited_at),
		isDeleted: true,
		pending: false
	};
	const nextMessagesByRoom = {
		...messagesByRoom,
		[targetRoomId]: nextMessages
	};
	return {
		messagesByRoom: nextMessagesByRoom,
		roomThreads: updateThreadPreview(roomThreads, nextMessagesByRoom, targetRoomId, deps),
		changed: true
	};
}
