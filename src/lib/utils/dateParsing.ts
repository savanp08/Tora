import { normalizeEpoch } from '$lib/utils/chat/core';

const DATE_ONLY_PATTERN = /^(\d{4})-(\d{2})-(\d{2})$/;
const DATE_VALUE_KEYS = [
	'value',
	'date',
	'datetime',
	'timestamp',
	'startDate',
	'endDate',
	'dueDate',
	'start_date',
	'end_date',
	'due_date'
] as const;
const DATE_SECONDS_KEYS = ['seconds', '_seconds'] as const;

function toRecord(value: unknown): Record<string, unknown> | null {
	if (!value || typeof value !== 'object' || Array.isArray(value)) {
		return null;
	}
	return value as Record<string, unknown>;
}

function toDateFromEpoch(value: number) {
	if (!Number.isFinite(value) || value <= 0) {
		return null;
	}
	const normalized = normalizeEpoch(value);
	const parsed = new Date(normalized);
	return Number.isFinite(parsed.getTime()) ? parsed : null;
}

function parseDateOnlyString(value: string) {
	const match = value.match(DATE_ONLY_PATTERN);
	if (!match) {
		return null;
	}
	const year = Number.parseInt(match[1], 10);
	const month = Number.parseInt(match[2], 10);
	const day = Number.parseInt(match[3], 10);
	if (!Number.isFinite(year) || !Number.isFinite(month) || !Number.isFinite(day)) {
		return null;
	}
	return new Date(year, month - 1, day);
}

function parseDateString(value: string) {
	const trimmed = value.trim();
	if (!trimmed) {
		return null;
	}

	const dateOnly = parseDateOnlyString(trimmed);
	if (dateOnly) {
		return dateOnly;
	}

	const numeric = Number(trimmed);
	if (Number.isFinite(numeric) && numeric > 0) {
		return toDateFromEpoch(numeric);
	}

	const parsed = Date.parse(trimmed);
	if (!Number.isFinite(parsed) || parsed <= 0) {
		return null;
	}
	return new Date(parsed);
}

export function parseFlexibleDateValue(value: unknown, seen = new Set<unknown>()): Date | null {
	if (value instanceof Date && Number.isFinite(value.getTime())) {
		return new Date(value.getTime());
	}
	if (typeof value === 'number') {
		return toDateFromEpoch(value);
	}
	if (typeof value === 'string') {
		return parseDateString(value);
	}

	const record = toRecord(value);
	if (!record || seen.has(record)) {
		return null;
	}
	seen.add(record);

	for (const key of DATE_SECONDS_KEYS) {
		const next = parseFlexibleDateValue(record[key], seen);
		if (next) {
			return next;
		}
	}

	for (const key of DATE_VALUE_KEYS) {
		const next = parseFlexibleDateValue(record[key], seen);
		if (next) {
			return next;
		}
	}

	return null;
}
