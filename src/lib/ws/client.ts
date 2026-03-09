import {
	getActiveTaskRoomId,
	removeTaskStoreEntry,
	type Task,
	type TaskStatus,
	upsertTaskStoreEntry
} from '$lib/stores/tasks';
import { normalizeRoomIDValue, toStringValue } from '$lib/utils/chat/core';

export type TaskSocketEventType = 'task_create' | 'task_update' | 'task_move' | 'task_delete';

const TASK_SOCKET_EVENT_TYPES = new Set<TaskSocketEventType>([
	'task_create',
	'task_update',
	'task_move',
	'task_delete'
]);

function toRecord(value: unknown): Record<string, unknown> | null {
	if (!value || typeof value !== 'object' || Array.isArray(value)) {
		return null;
	}
	return value as Record<string, unknown>;
}

function resolveTaskEventType(value: unknown): TaskSocketEventType | '' {
	const normalized = toStringValue(value).trim().toLowerCase() as TaskSocketEventType;
	if (!TASK_SOCKET_EVENT_TYPES.has(normalized)) {
		return '';
	}
	return normalized;
}

function resolveTaskRoomId(source: Record<string, unknown>) {
	const payload = toRecord(source.payload);
	return normalizeRoomIDValue(
		toStringValue(source.roomId ?? source.room_id ?? payload?.roomId ?? payload?.room_id)
	);
}

function resolveTaskSource(source: Record<string, unknown>) {
	const payload = toRecord(source.payload);
	const directTask = toRecord(source.task);
	const nestedTask = toRecord(payload?.task);

	const merged: Record<string, unknown> = {};
	if (payload) {
		Object.assign(merged, payload);
	}
	if (nestedTask) {
		Object.assign(merged, nestedTask);
	}
	if (directTask) {
		Object.assign(merged, directTask);
	}
	Object.assign(merged, source);
	return merged;
}

function resolveTaskIdForDelete(source: Record<string, unknown>) {
	const payload = toRecord(source.payload);
	const nestedTask = toRecord(payload?.task);
	return toStringValue(
		source.id ??
			source.taskId ??
			source.task_id ??
			payload?.id ??
			payload?.taskId ??
			payload?.task_id ??
			nestedTask?.id ??
			nestedTask?.taskId ??
			nestedTask?.task_id
	);
}

function toISOStringOrNow(value: number) {
	if (!Number.isFinite(value) || value <= 0) {
		return new Date().toISOString();
	}
	return new Date(value).toISOString();
}

export function buildTaskSocketPayload(
	type: TaskSocketEventType,
	roomId: string,
	task: Task
): Record<string, unknown> {
	const normalizedRoomId = normalizeRoomIDValue(roomId || task.roomId);
	const createdAt = toISOStringOrNow(task.createdAt);
	const updatedAt = toISOStringOrNow(task.updatedAt);
	const normalizedStatus = toStringValue(task.status).trim().toLowerCase().replace(/\s+/g, '_');

	return {
		type,
		roomId: normalizedRoomId,
		room_id: normalizedRoomId,
		task: {
			id: task.id,
			title: task.title,
			description: task.description,
			status: normalizedStatus,
			assignee_id: task.assigneeId,
			created_at: createdAt,
			updated_at: updatedAt
		},
		payload: {
			id: task.id,
			taskId: task.id,
			task_id: task.id,
			title: task.title,
			description: task.description,
			status: normalizedStatus as TaskStatus,
			assigneeId: task.assigneeId,
			assignee_id: task.assigneeId,
			createdAt,
			created_at: createdAt,
			updatedAt,
			updated_at: updatedAt,
			roomId: normalizedRoomId,
			room_id: normalizedRoomId
		}
	};
}

export function syncTaskStoreFromSocketPayload(rawPayload: unknown) {
	const source = toRecord(rawPayload);
	if (!source) {
		return false;
	}

	const eventType = resolveTaskEventType(source.type);
	if (!eventType) {
		return false;
	}

	const eventRoomId = resolveTaskRoomId(source);
	const activeRoomId = getActiveTaskRoomId();
	if (!eventRoomId || !activeRoomId || eventRoomId !== activeRoomId) {
		return true;
	}

	if (eventType === 'task_delete') {
		const taskId = resolveTaskIdForDelete(source);
		removeTaskStoreEntry(taskId, eventRoomId);
		return true;
	}

	const nextTaskSource = resolveTaskSource(source);
	nextTaskSource.roomId = eventRoomId;
	nextTaskSource.room_id = eventRoomId;
	upsertTaskStoreEntry(nextTaskSource, eventRoomId);
	return true;
}
