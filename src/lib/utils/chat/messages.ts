import type { ChatMessage, OnlineMember } from '$lib/types/chat';
import {
	createMessageId,
	isLikelyMediaURL,
	isMediaMessageType,
	normalizeMessageID,
	normalizeRoomIDValue,
	normalizeUsernameValue,
	parseOptionalTimestamp,
	toAbsoluteMediaURL,
	toBool,
	toInt,
	toStringValue,
	toTimestamp
} from '$lib/utils/chat/core';
import { parseTaskMessagePayload } from '$lib/utils/chat/task';

export const DELETED_MESSAGE_PLACEHOLDER = 'This message was deleted';

export function getMessagePreviewText(message: ChatMessage) {
	const content = (message.content || '').trim();
	if (message.type === 'task') {
		const taskPayload = parseTaskMessagePayload(content);
		if (!taskPayload) {
			return 'Task';
		}
		const taskCount = taskPayload.tasks.length;
		if (taskCount <= 0) {
			return `Task: ${taskPayload.title}`;
		}
		return `Task: ${taskPayload.title} (${taskCount})`;
	}
	if (message.type === 'image') {
		if (content && !isLikelyMediaURL(content)) {
			return content;
		}
		return 'Photo';
	}
	if (message.type === 'video') {
		if (content && !isLikelyMediaURL(content)) {
			return content;
		}
		return 'Video';
	}
	if (message.type === 'file') {
		if (content && !isLikelyMediaURL(content)) {
			return content;
		}
		const fileName = (message.fileName || '').trim();
		return fileName ? `File: ${fileName}` : 'Attachment';
	}
	if (message.type === 'audio') {
		if (content && !isLikelyMediaURL(content)) {
			return content;
		}
		return 'Voice message';
	}
	if (message.type === 'call_log') {
		const mode = (message.mediaType || '').trim().toLowerCase() === 'video' ? 'Video call' : 'Voice call';
		return `${mode}: ${content || 'Call ended'}`;
	}
	return content;
}

export function buildReplySnippet(senderName: string, content: string) {
	const normalizedSender = normalizeUsernameValue(senderName) || 'User';
	const normalizedContent = content.trim().replace(/\s+/g, ' ');
	const base = normalizedContent ? `${normalizedSender}: ${normalizedContent}` : normalizedSender;
	if (base.length <= 140) {
		return base;
	}
	return `${base.slice(0, 137)}...`;
}

export function parseIncomingMessage(
	value: unknown,
	fallbackRoomId: string,
	apiBase: string,
	deletedPlaceholder = DELETED_MESSAGE_PLACEHOLDER
): ChatMessage | null {
	if (!value || typeof value !== 'object') {
		return null;
	}

	const source = value as Record<string, unknown>;
	const nextRoomId = normalizeRoomIDValue(
		toStringValue(source.roomId ?? source.room_id ?? fallbackRoomId)
	);
	if (!nextRoomId) {
		return null;
	}

	const nextType = toStringValue(source.type ?? 'text') || 'text';
	const rawText = toStringValue(source.text ?? source.content ?? source.caption ?? '');
	const rawMediaURL = toStringValue(source.mediaUrl ?? source.media_url ?? '');
	let normalizedMediaURL = toAbsoluteMediaURL(rawMediaURL, apiBase);
	let nextContent = rawText;
	if (isMediaMessageType(nextType) && !normalizedMediaURL && isLikelyMediaURL(rawText)) {
		normalizedMediaURL = toAbsoluteMediaURL(rawText, apiBase);
		nextContent = '';
	}
	const hasBreakRoom =
		toBool(source.hasBreakRoom ?? source.has_break_room) ||
		toStringValue(source.breakRoomId ?? source.break_room_id) !== '';
	const breakRoomId = toStringValue(source.breakRoomId ?? source.break_room_id);
	const branchCount = Math.max(
		toInt(source.branchesCreated ?? source.branches_created),
		hasBreakRoom ? 1 : 0
	);

	const normalizedCallType =
		toStringValue(source.callType ?? source.call_type ?? source.mediaType ?? source.media_type)
			.trim()
			.toLowerCase() === 'video'
			? 'video'
			: 'audio';

	return {
		id:
			toStringValue(
				source.id ??
					source.commentId ??
					source.commentID ??
					source.comment_id ??
					source.messageId ??
					source.messageID ??
					source.message_id
			) || createMessageId(nextRoomId),
		roomId: nextRoomId,
		senderId: toStringValue(source.userId ?? source.senderId ?? source.sender_id ?? 'unknown'),
		senderName:
			normalizeUsernameValue(
				toStringValue(source.username ?? source.senderName ?? source.sender_name ?? 'Unknown')
			) || 'Unknown',
		content: nextContent,
		type: nextType,
		mediaUrl:
			normalizedMediaURL ||
			(isMediaMessageType(nextType) && isLikelyMediaURL(rawText)
				? toAbsoluteMediaURL(rawText, apiBase)
				: ''),
		mediaType:
			nextType === 'call_log'
				? normalizedCallType
				: toStringValue(source.mediaType ?? source.media_type ?? source.type ?? nextType),
		fileName: toStringValue(source.fileName ?? source.file_name),
		isEdited: toBool(source.isEdited ?? source.is_edited),
		editedAt: parseOptionalTimestamp(source.editedAt ?? source.edited_at),
		isDeleted:
			nextType === 'deleted' ||
			toBool(source.isDeleted ?? source.is_deleted) ||
			toStringValue(source.content).trim() === deletedPlaceholder,
		replyToMessageId: normalizeMessageID(
			toStringValue(
				source.replyToMessageId ??
					source.replyToMessageID ??
					source.reply_to_message_id ??
					source.parentCommentId ??
					source.parentCommentID ??
					source.parent_comment_id
			)
		),
		replyToSnippet: toStringValue(source.replyToSnippet ?? source.reply_to_snippet).trim(),
		totalReplies: toInt(source.totalReplies ?? source.total_replies),
		branchesCreated: branchCount,
		createdAt: toTimestamp(
			source.time ?? source.createdAt ?? source.created_at ?? source.timestamp
		),
		hasBreakRoom,
		breakRoomId,
		breakJoinCount: toInt(source.breakJoinCount ?? source.break_join_count),
		isPinned: toBool(source.isPinned ?? source.is_pinned),
		pinnedBy: toStringValue(source.pinnedBy ?? source.pinned_by),
		pinnedByName: toStringValue(source.pinnedByName ?? source.pinned_by_name),
		pending: false
	};
}

export function parseMember(value: unknown, fallbackIndex: number): OnlineMember | null {
	if (!value || typeof value !== 'object') {
		return null;
	}
	const source = value as Record<string, unknown>;
	const memberId = toStringValue(
		source.id ?? source.userId ?? source.user_id ?? `member-${fallbackIndex}`
	);
	const memberName =
		toStringValue(source.name ?? source.username ?? source.userName ?? source.user_name) ||
		memberId;
	const joinedAt = toTimestamp(source.joinedAt ?? source.joined_at ?? Date.now());
	const isAdmin = toBool(
		source.isAdmin ??
			source.is_admin ??
			source.roomAdmin ??
			source.room_admin ??
			source.admin ??
			(source.role === 'admin')
	);
	if (!memberId) {
		return null;
	}
	return { id: memberId, name: memberName, isOnline: true, joinedAt, isAdmin };
}

export function toWireMessage(message: ChatMessage) {
	const normalizedType = (message.type || '').trim().toLowerCase();
	const isCallLog = normalizedType === 'call_log';
	const callType =
		(message.mediaType || '').trim().toLowerCase() === 'video' ? 'video' : 'audio';
	const mediaType = isMediaMessageType(message.type) ? message.type : '';
	const mediaURL = mediaType
		? (message.mediaUrl || '').trim() || (isLikelyMediaURL(message.content) ? message.content : '')
		: '';
	const contentText =
		mediaType && mediaURL && message.content.trim() === mediaURL ? '' : message.content;

	return {
		id: message.id,
		roomId: message.roomId,
		userId: message.senderId,
		username: message.senderName,
		text: contentText,
		time: new Date(message.createdAt).toISOString(),
		senderId: message.senderId,
		senderName: message.senderName,
		content: contentText,
		type: message.type,
		mediaUrl: mediaURL,
		mediaType: isCallLog ? callType : mediaType,
		callType: isCallLog ? callType : '',
		call_type: isCallLog ? callType : '',
		fileName: message.fileName ?? '',
		replyToMessageId: normalizeMessageID(message.replyToMessageId ?? ''),
		replyToSnippet: (message.replyToSnippet || '').trim(),
		reply_to_message_id: normalizeMessageID(message.replyToMessageId ?? ''),
		reply_to_snippet: (message.replyToSnippet || '').trim(),
		createdAt: new Date(message.createdAt).toISOString()
	};
}
