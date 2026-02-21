import type { ChatMessage, ChatThread, ThreadStatus } from '$lib/types/chat';
import { normalizeRoomIDValue } from '$lib/utils/chat/core';

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
