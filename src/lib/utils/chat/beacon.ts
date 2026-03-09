import { parseOptionalTimestamp } from '$lib/utils/chat/core';

export const BEACON_MESSAGE_PAYLOAD_KIND = 'beacon_v1';

export type BeaconMessagePayload = {
	kind: typeof BEACON_MESSAGE_PAYLOAD_KIND;
	text: string;
	beaconAt: number;
	beaconLabel: string;
	createdAt: number;
};

function toTrimmedString(value: unknown) {
	return typeof value === 'string' ? value.trim() : '';
}

function toFiniteTimestamp(value: unknown) {
	const parsed = parseOptionalTimestamp(value);
	if (!Number.isFinite(parsed) || parsed <= 0) {
		return 0;
	}
	return parsed;
}

export function formatBeaconTimestamp(timestamp: number) {
	if (!Number.isFinite(timestamp) || timestamp <= 0) {
		return '';
	}
	return new Date(timestamp).toLocaleString([], {
		month: 'short',
		day: 'numeric',
		hour: 'numeric',
		minute: '2-digit'
	});
}

export function buildBeaconMessagePayload(input: {
	text: string;
	beaconAt: number;
	beaconLabel?: string;
	createdAt?: number;
}) {
	const normalizedText = toTrimmedString(input.text);
	const beaconAt = toFiniteTimestamp(input.beaconAt);
	const createdAt = toFiniteTimestamp(input.createdAt) || Date.now();
	const beaconLabel = toTrimmedString(input.beaconLabel) || formatBeaconTimestamp(beaconAt);
	return JSON.stringify({
		kind: BEACON_MESSAGE_PAYLOAD_KIND,
		text: normalizedText,
		beaconAt,
		beaconLabel,
		createdAt
	} satisfies BeaconMessagePayload);
}

export function parseBeaconMessagePayload(rawContent: string): BeaconMessagePayload | null {
	const trimmed = toTrimmedString(rawContent);
	if (!trimmed.startsWith('{') || !trimmed.endsWith('}')) {
		return null;
	}
	try {
		const parsed = JSON.parse(trimmed);
		if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
			return null;
		}
		const record = parsed as Record<string, unknown>;
		const kind = toTrimmedString(record.kind).toLowerCase();
		if (kind !== BEACON_MESSAGE_PAYLOAD_KIND) {
			return null;
		}
		const text = toTrimmedString(record.text);
		const beaconAt = toFiniteTimestamp(record.beaconAt ?? record.beacon_at);
		if (!text || beaconAt <= 0) {
			return null;
		}
		const createdAt = toFiniteTimestamp(record.createdAt ?? record.created_at) || Date.now();
		const beaconLabel = toTrimmedString(record.beaconLabel ?? record.beacon_label) || formatBeaconTimestamp(beaconAt);
		return {
			kind: BEACON_MESSAGE_PAYLOAD_KIND,
			text,
			beaconAt,
			beaconLabel,
			createdAt
		};
	} catch {
		return null;
	}
}
