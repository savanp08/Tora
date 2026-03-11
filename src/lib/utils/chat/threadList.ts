import type { ChatMessage, ChatThread, ThreadStatus } from '$lib/types/chat';
import { normalizeRoomIDValue } from '$lib/utils/chat/core';

export type ThreadSearchResultKind = 'room' | 'message';

export type ThreadSearchResult = {
	key: string;
	kind: ThreadSearchResultKind;
	roomId: string;
	roomName: string;
	status: ThreadStatus;
	preview: string;
	lastActivity: number;
	messageId?: string;
	messageCreatedAt?: number;
	senderName?: string;
};

export function sortThreads(threads: ChatThread[]) {
	return [...threads].sort((a, b) => b.lastActivity - a.lastActivity);
}

export function filterThreadsByStatus(threads: ChatThread[], status: ThreadStatus) {
	return threads.filter((thread) => thread.status === status);
}

export function filterThreadList(
	threads: ChatThread[],
	searchQuery: string,
	messageMap: Record<string, ChatMessage[]>,
	activeRoomId: string
) {
	const query = searchQuery.trim().toLowerCase();
	if (!query) {
		return threads;
	}
	const filtered = threads.filter((thread) => {
		if (thread.name.toLowerCase().includes(query)) {
			return true;
		}
		if (thread.lastMessage.toLowerCase().includes(query)) {
			return true;
		}
		const messages = messageMap[thread.id] ?? [];
		return messages.some(
			(message) =>
				message.content.toLowerCase().includes(query) ||
				message.senderName.toLowerCase().includes(query)
		);
	});

	if (activeRoomId && !filtered.some((thread) => thread.id === activeRoomId)) {
		const active = threads.find((thread) => thread.id === activeRoomId);
		if (active) {
			return [active, ...filtered];
		}
	}
	return filtered;
}

function compactSearchPreview(value: string, maxLength = 160) {
	const compact = value.replace(/\s+/g, ' ').trim();
	if (!compact) {
		return '';
	}
	if (compact.length <= maxLength) {
		return compact;
	}
	return `${compact.slice(0, Math.max(1, maxLength - 1)).trimEnd()}...`;
}

function resolveRoomPreview(thread: ChatThread) {
	const preview = compactSearchPreview(thread.lastMessage || '', 140);
	if (preview) {
		return preview;
	}
	if (thread.status === 'left') {
		return 'You left this room';
	}
	return thread.status === 'joined' ? 'No messages yet' : 'Preview and join';
}

export function buildThreadSearchResults(
	threads: ChatThread[],
	searchQuery: string,
	messageMap: Record<string, ChatMessage[]>
) {
	const query = searchQuery.trim().toLowerCase();
	if (!query) {
		return [] as ThreadSearchResult[];
	}
	const dedupedThreads = [...new Map(threads.map((thread) => [thread.id, thread])).values()];

	const roomMatches = dedupedThreads
		.map((thread) => {
			const roomName = (thread.name || '').toLowerCase();
			const lastMessage = (thread.lastMessage || '').toLowerCase();
			let rank = -1;
			if (roomName.startsWith(query)) {
				rank = 0;
			} else if (roomName.includes(query)) {
				rank = 1;
			} else if (lastMessage.includes(query)) {
				rank = 2;
			}
			if (rank < 0) {
				return null;
			}
			const result: ThreadSearchResult = {
				key: `room:${thread.id}`,
				kind: 'room',
				roomId: thread.id,
				roomName: thread.name,
				status: thread.status,
				preview: resolveRoomPreview(thread),
				lastActivity: Number.isFinite(thread.lastActivity) ? thread.lastActivity : 0
			};
			return { result, rank };
		})
		.filter((entry): entry is { result: ThreadSearchResult; rank: number } => Boolean(entry))
		.sort((left, right) => {
			if (left.rank !== right.rank) {
				return left.rank - right.rank;
			}
			if (left.result.lastActivity !== right.result.lastActivity) {
				return right.result.lastActivity - left.result.lastActivity;
			}
			return left.result.roomName.localeCompare(right.result.roomName, undefined, {
				sensitivity: 'base'
			});
		})
		.map((entry) => entry.result);

	const maxMessageMatchesPerRoom = 5;
	const maxMessageMatches = 120;
	const messageMatches: ThreadSearchResult[] = [];
	for (const thread of sortThreads(dedupedThreads)) {
		if (messageMatches.length >= maxMessageMatches) {
			break;
		}
		const roomMessages = [...(messageMap[thread.id] ?? [])].sort((a, b) => b.createdAt - a.createdAt);
		let roomMatchCount = 0;
		for (const message of roomMessages) {
			if (roomMatchCount >= maxMessageMatchesPerRoom || messageMatches.length >= maxMessageMatches) {
				break;
			}
			const senderName = (message.senderName || '').trim();
			const content = message.content || '';
			const senderMatch = senderName.toLowerCase().includes(query);
			const contentMatch = content.toLowerCase().includes(query);
			if (!senderMatch && !contentMatch) {
				continue;
			}
			const preview = compactSearchPreview(content, 160);
			const safeCreatedAt = Number.isFinite(message.createdAt) ? message.createdAt : thread.lastActivity;
			messageMatches.push({
				key: `message:${thread.id}:${message.id}`,
				kind: 'message',
				roomId: thread.id,
				roomName: thread.name,
				status: thread.status,
				preview: preview || '[message]',
				lastActivity: Number.isFinite(thread.lastActivity) ? thread.lastActivity : safeCreatedAt,
				messageId: message.id,
				messageCreatedAt: safeCreatedAt,
				senderName: senderName || 'Unknown'
			});
			roomMatchCount += 1;
		}
	}

	messageMatches.sort((left, right) => {
		const leftTime = left.messageCreatedAt ?? 0;
		const rightTime = right.messageCreatedAt ?? 0;
		if (leftTime !== rightTime) {
			return rightTime - leftTime;
		}
		if (left.lastActivity !== right.lastActivity) {
			return right.lastActivity - left.lastActivity;
		}
		return left.roomName.localeCompare(right.roomName, undefined, { sensitivity: 'base' });
	});

	return [...roomMatches, ...messageMatches];
}

export function collectLocalRoomSubtreeIDs(rootRoomId: string, roomThreads: ChatThread[]) {
	const normalizedRoot = normalizeRoomIDValue(rootRoomId);
	const ids = new Set<string>();
	if (!normalizedRoot) {
		return ids;
	}
	ids.add(normalizedRoot);

	let changed = true;
	while (changed) {
		changed = false;
		for (const thread of roomThreads) {
			const threadRoomId = normalizeRoomIDValue(thread.id);
			const parentRoomId = normalizeRoomIDValue(thread.parentRoomId || '');
			if (!threadRoomId || !parentRoomId) {
				continue;
			}
			if (!ids.has(parentRoomId) || ids.has(threadRoomId)) {
				continue;
			}
			ids.add(threadRoomId);
			changed = true;
		}
	}
	return ids;
}
