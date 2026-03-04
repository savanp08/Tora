import type { ChatMessage } from '$lib/types/chat';
import { normalizeMessageID, normalizeRoomIDValue } from '$lib/utils/chat/core';

export function roomPinsEndpoint(apiBase: string, roomId: string) {
	return `${apiBase}/api/rooms/${encodeURIComponent(roomId)}/pins`;
}

export function discussionCommentsEndpoint(
	apiBase: string,
	targetRoomId: string,
	pinnedMessageId: string
) {
	const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
	const normalizedPinnedMessageID = normalizeMessageID(pinnedMessageId);
	return `${apiBase}/api/rooms/${encodeURIComponent(normalizedRoomID)}/pins/${encodeURIComponent(normalizedPinnedMessageID)}/discussion/comments`;
}

export function getDiscussionCommentsCacheKey(targetRoomId: string, pinnedMessageId: string) {
	const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
	const normalizedPinnedMessageID = normalizeMessageID(pinnedMessageId);
	if (!normalizedRoomID || !normalizedPinnedMessageID) {
		return '';
	}
	return `${normalizedRoomID}::${normalizedPinnedMessageID}`;
}

export function readDiscussionCommentsCache(
	cacheByTaskKey: Record<string, ChatMessage[]>,
	targetRoomId: string,
	pinnedMessageId: string
) {
	const key = getDiscussionCommentsCacheKey(targetRoomId, pinnedMessageId);
	if (!key || !Object.prototype.hasOwnProperty.call(cacheByTaskKey, key)) {
		return null;
	}
	const cached = cacheByTaskKey[key] ?? [];
	return [...cached];
}

export function writeDiscussionCommentsCache(
	cacheByTaskKey: Record<string, ChatMessage[]>,
	targetRoomId: string,
	pinnedMessageId: string,
	comments: ChatMessage[]
) {
	const key = getDiscussionCommentsCacheKey(targetRoomId, pinnedMessageId);
	if (!key) {
		return cacheByTaskKey;
	}
	return {
		...cacheByTaskKey,
		[key]: [...comments].sort((left, right) => left.createdAt - right.createdAt)
	};
}

export function upsertDiscussionCommentList(existing: ChatMessage[], comment: ChatMessage) {
	const normalizedID = normalizeMessageID(comment.id);
	if (!normalizedID) {
		return [...existing];
	}
	return [
		...existing.filter((entry) => normalizeMessageID(entry.id) !== normalizedID),
		comment
	].sort((left, right) => left.createdAt - right.createdAt);
}

export function buildDiscussionCommentMap(items: ChatMessage[]) {
	const map = new Map<string, ChatMessage>();
	for (const item of items) {
		const normalizedId = normalizeMessageID(item.id);
		if (!normalizedId) {
			continue;
		}
		map.set(normalizedId, item);
	}
	return map;
}

export function resolveDiscussionCommentDepth(
	commentId: string,
	commentMap: Map<string, ChatMessage>,
	maxReplyDepth: number
) {
	let depth = 1;
	let currentId = normalizeMessageID(commentId);
	const seen = new Set<string>();
	while (currentId && commentMap.has(currentId) && depth <= maxReplyDepth + 2) {
		if (seen.has(currentId)) {
			break;
		}
		seen.add(currentId);
		const parentId = normalizeMessageID(commentMap.get(currentId)?.replyToMessageId || '');
		if (!parentId || !commentMap.has(parentId)) {
			break;
		}
		depth += 1;
		currentId = parentId;
	}
	return depth;
}
