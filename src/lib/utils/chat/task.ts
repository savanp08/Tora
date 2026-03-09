import type { TaskChecklistItem, TaskMessagePayload } from '$lib/types/chat';
import { APP_LIMITS } from '$lib/config/limits';

const MAX_TITLE_LENGTH = APP_LIMITS.tasks.maxTitleLength;
const MAX_TASK_TEXT_LENGTH = APP_LIMITS.tasks.maxTaskTextLength;
const MAX_TASK_ITEMS = APP_LIMITS.tasks.maxItems;

function normalizeTitle(value: unknown) {
	if (typeof value !== 'string') {
		return 'Task';
	}
	const normalized = value.trim().replace(/\s+/g, ' ');
	if (!normalized) {
		return 'Task';
	}
	return normalized.slice(0, MAX_TITLE_LENGTH);
}

function normalizeText(value: unknown) {
	if (typeof value !== 'string') {
		return '';
	}
	return value.trim().replace(/\s+/g, ' ').slice(0, MAX_TASK_TEXT_LENGTH);
}

function normalizeTimestamp(value: unknown) {
	if (typeof value === 'number' && Number.isFinite(value) && value > 0) {
		return value > 1_000_000_000_000 ? Math.trunc(value) : Math.trunc(value * 1000);
	}
	if (typeof value === 'string') {
		const trimmed = value.trim();
		if (!trimmed) {
			return 0;
		}
		const numeric = Number(trimmed);
		if (Number.isFinite(numeric) && numeric > 0) {
			return numeric > 1_000_000_000_000 ? Math.trunc(numeric) : Math.trunc(numeric * 1000);
		}
		const parsed = Date.parse(trimmed);
		if (Number.isFinite(parsed) && parsed > 0) {
			return parsed;
		}
	}
	return 0;
}

function toTaskItem(value: unknown): TaskChecklistItem | null {
	if (!value || typeof value !== 'object') {
		return null;
	}
	const source = value as Record<string, unknown>;
	const text = normalizeText(source.text);
	if (!text) {
		return null;
	}
	const completed = Boolean(source.completed);
	const completedBy = completed ? normalizeText(source.completedBy) : '';
	const createdBy = normalizeText(source.createdBy);
	const createdAt = normalizeTimestamp(source.createdAt);
	return {
		text,
		completed,
		completedBy,
		timestamp: completed ? normalizeTimestamp(source.timestamp) : 0,
		createdBy,
		createdAt
	};
}

export function parseTaskMessagePayload(content: string): TaskMessagePayload | null {
	const trimmed = (content || '').trim();
	if (!trimmed) {
		return null;
	}
	let parsed: unknown;
	try {
		parsed = JSON.parse(trimmed);
	} catch {
		return null;
	}
	if (!parsed || typeof parsed !== 'object') {
		return null;
	}
	const source = parsed as Record<string, unknown>;
	const title = normalizeTitle(source.title);
	const items = Array.isArray(source.tasks) ? source.tasks : [];
	const tasks = items.map(toTaskItem).filter((entry): entry is TaskChecklistItem => Boolean(entry));
	return {
		title,
		tasks: tasks.slice(0, MAX_TASK_ITEMS)
	};
}

export function stringifyTaskMessagePayload(payload: TaskMessagePayload) {
	return JSON.stringify(payload);
}

export function buildTaskMessagePayload(title: string, taskLines: string[]) {
	const now = Date.now();
	const normalizedTitle = normalizeTitle(title);
	const tasks = taskLines
		.map((line) => normalizeText(line))
		.filter((line) => line !== '')
		.slice(0, MAX_TASK_ITEMS)
		.map((text) => ({
			text,
			completed: false,
			completedBy: '',
			timestamp: 0,
			createdBy: '',
			createdAt: now
		}));

	return {
		title: normalizedTitle,
		tasks
	} satisfies TaskMessagePayload;
}

export function addTaskItem(
	payload: TaskMessagePayload,
	text: string,
	createdBy: string,
	now = Date.now()
): TaskMessagePayload | null {
	if (!payload) {
		return null;
	}
	const normalizedText = normalizeText(text);
	if (!normalizedText) {
		return null;
	}
	if (payload.tasks.length >= MAX_TASK_ITEMS) {
		return null;
	}
	const nextTask: TaskChecklistItem = {
		text: normalizedText,
		completed: false,
		completedBy: '',
		timestamp: 0,
		createdBy: normalizeText(createdBy),
		createdAt: now
	};
	return {
		title: payload.title,
		tasks: [...payload.tasks, nextTask]
	};
}

export function toggleTaskItem(
	payload: TaskMessagePayload,
	index: number,
	completedBy: string,
	now = Date.now()
): TaskMessagePayload | null {
	if (!payload || index < 0 || index >= payload.tasks.length) {
		return null;
	}
	const nextTasks = payload.tasks.map((entry, itemIndex) => {
		if (itemIndex !== index) {
			return { ...entry };
		}
		const nextCompleted = !entry.completed;
		return {
			...entry,
			completed: nextCompleted,
			completedBy: nextCompleted ? normalizeText(completedBy) : '',
			timestamp: nextCompleted ? now : 0
		};
	});

	return {
		title: payload.title,
		tasks: nextTasks
	};
}
