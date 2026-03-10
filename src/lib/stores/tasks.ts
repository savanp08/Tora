import { writable } from 'svelte/store';
import { normalizeRoomIDValue, parseOptionalTimestamp, toStringValue } from '$lib/utils/chat/core';

const DEFAULT_API_BASE = 'http://127.0.0.1:8080';

export type TaskStatus = 'todo' | 'in_progress' | 'done' | string;

export type Task = {
	id: string;
	roomId: string;
	title: string;
	description: string;
	status: TaskStatus;
	assigneeId: string;
	statusActorId?: string;
	statusActorName?: string;
	statusChangedAt?: number;
	createdAt: number;
	updatedAt: number;
};

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export const taskStore = writable<Task[]>([]);
export const taskStoreLoading = writable<boolean>(false);
export const taskStoreError = writable<string>('');

let activeTaskRoomId = '';
let activeTaskLoadToken = 0;

export function getActiveTaskRoomId() {
	return activeTaskRoomId;
}

export function clearTaskStore() {
	taskStore.set([]);
	taskStoreError.set('');
	taskStoreLoading.set(false);
}

export function normalizeTaskStatus(value: unknown): TaskStatus {
	const normalized = toStringValue(value).trim().toLowerCase().replace(/\s+/g, '_');
	if (!normalized) {
		return 'todo';
	}
	return normalized;
}

function normalizeTaskId(value: unknown) {
	return toStringValue(value)
		.trim()
		.replace(/[^a-zA-Z0-9_-]/g, '');
}

function toRecord(value: unknown): Record<string, unknown> | null {
	if (!value || typeof value !== 'object' || Array.isArray(value)) {
		return null;
	}
	return value as Record<string, unknown>;
}

function normalizeApiBase(value?: string) {
	const trimmed = (value ?? '').trim();
	return trimmed || DEFAULT_API_BASE;
}

function dedupeByTaskId(tasks: Task[]) {
	const taskMap = new Map<string, Task>();
	for (const task of tasks) {
		taskMap.set(task.id, task);
	}
	return [...taskMap.values()];
}

export function normalizeTaskRecord(raw: unknown, fallbackRoomId = activeTaskRoomId): Task | null {
	const source = toRecord(raw);
	if (!source) {
		return null;
	}

	const id = normalizeTaskId(source.id ?? source.taskId ?? source.task_id);
	if (!id) {
		return null;
	}

	const roomId = normalizeRoomIDValue(
		toStringValue(source.roomId ?? source.room_id ?? fallbackRoomId)
	);
	if (!roomId) {
		return null;
	}

	const title = toStringValue(source.title).trim() || 'Untitled Task';
	const description = toStringValue(source.description).trim();
	const status = normalizeTaskStatus(source.status);
	const assigneeId = toStringValue(source.assigneeId ?? source.assignee_id).trim();
	const statusActorId = toStringValue(source.statusActorId ?? source.status_actor_id).trim();
	const statusActorName = toStringValue(source.statusActorName ?? source.status_actor_name).trim();
	const statusChangedAt =
		parseOptionalTimestamp(source.statusChangedAt ?? source.status_changed_at) || 0;
	const now = Date.now();
	const createdAt = parseOptionalTimestamp(source.createdAt ?? source.created_at) || now;
	const updatedAt = parseOptionalTimestamp(source.updatedAt ?? source.updated_at) || createdAt;

	return {
		id,
		roomId,
		title,
		description,
		status,
		assigneeId,
		statusActorId: statusActorId || undefined,
		statusActorName: statusActorName || undefined,
		statusChangedAt: statusChangedAt > 0 ? statusChangedAt : undefined,
		createdAt,
		updatedAt
	};
}

export function setTaskStoreForRoom(records: unknown[], roomId = activeTaskRoomId) {
	const normalizedRoomId = normalizeRoomIDValue(roomId);
	if (!normalizedRoomId) {
		taskStore.set([]);
		return;
	}

	const normalizedTasks = records
		.map((record) => normalizeTaskRecord(record, normalizedRoomId))
		.filter((task): task is Task => Boolean(task));

	taskStore.set(dedupeByTaskId(normalizedTasks));
}

export function upsertTaskStoreEntry(raw: unknown, fallbackRoomId = activeTaskRoomId): Task | null {
	const nextTask = normalizeTaskRecord(raw, fallbackRoomId);
	if (!nextTask) {
		return null;
	}

	if (activeTaskRoomId && nextTask.roomId !== activeTaskRoomId) {
		return null;
	}

	taskStore.update((tasks) => {
		const taskIndex = tasks.findIndex((task) => task.id === nextTask.id);
		if (taskIndex < 0) {
			return [...tasks, nextTask];
		}

		const nextTasks = [...tasks];
		nextTasks[taskIndex] = {
			...nextTasks[taskIndex],
			...nextTask
		};
		return nextTasks;
	});
	return nextTask;
}

export function removeTaskStoreEntry(taskIdValue: unknown, roomId = activeTaskRoomId) {
	const taskId = normalizeTaskId(taskIdValue);
	const normalizedRoomId = normalizeRoomIDValue(roomId);
	if (!taskId || !normalizedRoomId) {
		return false;
	}
	if (activeTaskRoomId && normalizedRoomId !== activeTaskRoomId) {
		return false;
	}

	let removed = false;
	taskStore.update((tasks) => {
		const nextTasks = tasks.filter((task) => task.id !== taskId);
		removed = nextTasks.length !== tasks.length;
		return nextTasks;
	});
	return removed;
}

export function moveTaskOptimistic(taskIdValue: unknown, nextStatusValue: unknown): Task | null {
	const taskId = normalizeTaskId(taskIdValue);
	const nextStatus = normalizeTaskStatus(nextStatusValue);
	if (!taskId || !nextStatus) {
		return null;
	}

	let updatedTask: Task | null = null;
	taskStore.update((tasks) =>
		tasks.map((task) => {
			if (task.id !== taskId) {
				return task;
			}
			if (task.status === nextStatus) {
				updatedTask = task;
				return task;
			}
			updatedTask = {
				...task,
				status: nextStatus,
				updatedAt: Date.now()
			};
			return updatedTask;
		})
	);
	return updatedTask;
}

export async function initializeTaskStoreForRoom(
	roomId: string,
	options?: {
		fetchImpl?: FetchLike;
		apiBase?: string;
	}
) {
	const normalizedRoomId = normalizeRoomIDValue(roomId);
	activeTaskRoomId = normalizedRoomId;
	activeTaskLoadToken += 1;
	const loadToken = activeTaskLoadToken;

	if (!normalizedRoomId) {
		clearTaskStore();
		return [];
	}

	taskStore.set([]);
	taskStoreError.set('');
	taskStoreLoading.set(true);

	const fetchImpl = options?.fetchImpl ?? fetch;
	const apiBase = normalizeApiBase(options?.apiBase);

	try {
		const response = await fetchImpl(
			`${apiBase}/api/rooms/${encodeURIComponent(normalizedRoomId)}/tasks`,
			{
				method: 'GET',
				credentials: 'include'
			}
		);

		if (!response.ok) {
			throw new Error(`HTTP ${response.status}`);
		}

		const payload = (await response.json()) as unknown;
		if (loadToken !== activeTaskLoadToken || normalizedRoomId !== activeTaskRoomId) {
			return [];
		}

		const records = Array.isArray(payload) ? payload : [];
		setTaskStoreForRoom(records, normalizedRoomId);
		taskStoreError.set('');
		return records;
	} catch (error) {
		if (loadToken === activeTaskLoadToken) {
			taskStore.set([]);
			taskStoreError.set(error instanceof Error ? error.message : 'Failed to load tasks');
		}
		return [];
	} finally {
		if (loadToken === activeTaskLoadToken) {
			taskStoreLoading.set(false);
		}
	}
}
