import type { SocketEnvelope } from '$lib/types/chat';
import {
	normalizeIdentifier,
	normalizeMessageID,
	normalizeRoomIDValue,
	toStringValue
} from '$lib/utils/chat/core';

export function isEnvelope(value: unknown): value is SocketEnvelope {
	return Boolean(
		value &&
			typeof value === 'object' &&
			'type' in value &&
			'payload' in value &&
			typeof (value as { type?: unknown }).type === 'string'
	);
}

export function resolveEnvelopeRoomID(envelope: SocketEnvelope) {
	const directRoomID = normalizeRoomIDValue(toStringValue(envelope.roomId ?? envelope.room_id));
	if (directRoomID) {
		return directRoomID;
	}
	if (envelope.payload && typeof envelope.payload === 'object') {
		const payload = envelope.payload as Record<string, unknown>;
		return normalizeRoomIDValue(toStringValue(payload.roomId ?? payload.room_id));
	}
	return '';
}

export function resolveEnvelopePayloadRecord(envelope: SocketEnvelope): Record<string, unknown> {
	if (
		!envelope.payload ||
		typeof envelope.payload !== 'object' ||
		Array.isArray(envelope.payload)
	) {
		return {};
	}
	return envelope.payload as Record<string, unknown>;
}

export function resolveEnvelopeTargetUserID(envelope: SocketEnvelope) {
	const source = envelope as Record<string, unknown>;
	const payload = resolveEnvelopePayloadRecord(envelope);
	return normalizeIdentifier(
		toStringValue(
			source.targetUserId ??
				source.target_user_id ??
				payload.targetUserId ??
				payload.target_user_id
		)
	);
}

export function resolveDiscussionPinMessageID(envelope: SocketEnvelope) {
	const source = envelope as Record<string, unknown>;
	const directPinID = normalizeMessageID(
		toStringValue(source.pinMessageId ?? source.pin_message_id)
	);
	if (directPinID) {
		return directPinID;
	}
	if (envelope.payload && typeof envelope.payload === 'object') {
		const payload = envelope.payload as Record<string, unknown>;
		const payloadPinID = normalizeMessageID(
			toStringValue(
				payload.pinMessageId ??
					payload.pin_message_id ??
					payload.replyToMessageId ??
					payload.reply_to_message_id
			)
		);
		if (payloadPinID) {
			return payloadPinID;
		}
	}
	return '';
}
