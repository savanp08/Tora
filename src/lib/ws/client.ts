import {
	getActiveTaskRoomId,
	removeTaskStoreEntry,
	type Task,
	type TaskStatus,
	upsertTaskStoreEntry
} from '$lib/stores/tasks';
import { addBoardActivityFromSocket, type BoardActivityEvent } from '$lib/stores/boardActivity';
import { refreshFieldSchemasForRoom } from '$lib/stores/fieldSchema';
import { normalizeRoomIDValue, toStringValue } from '$lib/utils/chat/core';

export type TaskSocketEventType =
	| 'task_create'
	| 'task_update'
	| 'task_move'
	| 'task_delete'
	| 'task_relation_update';
export const BOARD_ACTIVITY_SOCKET_EVENT_TYPE = 'board_activity';
export const FIELD_SCHEMA_SOCKET_EVENT_TYPE = 'field_schema_update';

const TASK_SOCKET_EVENT_TYPES = new Set<TaskSocketEventType>([
	'task_create',
	'task_update',
	'task_move',
	'task_delete',
	'task_relation_update'
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
	const statusChangedAt = toISOStringOrNow(task.statusChangedAt ?? task.updatedAt);
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
			custom_fields: task.customFields,
			blocked_by: task.blockedBy,
			blocks: task.blocks,
			subtasks: task.subtasks,
			completion_percent: task.completionPercent,
			budget: task.budget,
			actual_cost: task.spent,
			sprint_name: task.sprintName,
			assignee_id: task.assigneeId,
			status_actor_id: task.statusActorId,
			status_actor_name: task.statusActorName,
			status_changed_at: statusChangedAt,
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
			customFields: task.customFields,
			custom_fields: task.customFields,
			blockedBy: task.blockedBy,
			blocked_by: task.blockedBy,
			blocks: task.blocks,
			subtasks: task.subtasks,
			completionPercent: task.completionPercent,
			completion_percent: task.completionPercent,
			budget: task.budget,
			actualCost: task.spent,
			actual_cost: task.spent,
			spent: task.spent,
			sprintName: task.sprintName,
			sprint_name: task.sprintName,
			assigneeId: task.assigneeId,
			assignee_id: task.assigneeId,
			statusActorId: task.statusActorId,
			status_actor_id: task.statusActorId,
			statusActorName: task.statusActorName,
			status_actor_name: task.statusActorName,
			statusChangedAt: statusChangedAt,
			status_changed_at: statusChangedAt,
			createdAt,
			created_at: createdAt,
			updatedAt,
			updated_at: updatedAt,
			roomId: normalizedRoomId,
			room_id: normalizedRoomId
		}
	};
}

export function buildBoardActivitySocketPayload(roomId: string, event: BoardActivityEvent) {
	const normalizedRoomId = normalizeRoomIDValue(roomId);
	return {
		type: BOARD_ACTIVITY_SOCKET_EVENT_TYPE,
		roomId: normalizedRoomId,
		room_id: normalizedRoomId,
		payload: {
			roomId: normalizedRoomId,
			room_id: normalizedRoomId,
			activity: {
				...event
			}
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

export function syncFieldSchemaStoreFromSocketPayload(rawPayload: unknown) {
	const source = toRecord(rawPayload);
	if (!source) {
		return false;
	}
	const eventType = toStringValue(source.type).trim().toLowerCase();
	if (eventType !== FIELD_SCHEMA_SOCKET_EVENT_TYPE) {
		return false;
	}
	const eventRoomId = resolveTaskRoomId(source);
	if (!eventRoomId) {
		return true;
	}
	void refreshFieldSchemasForRoom(eventRoomId);
	return true;
}

export function syncBoardActivityFromSocketPayload(rawPayload: unknown) {
	const source = toRecord(rawPayload);
	if (!source) {
		return false;
	}

	const eventType = toStringValue(source.type).trim().toLowerCase();
	if (eventType !== BOARD_ACTIVITY_SOCKET_EVENT_TYPE) {
		return false;
	}

	const payload = toRecord(source.payload);
	const eventRoomID = normalizeRoomIDValue(
		toStringValue(source.roomId ?? source.room_id ?? payload?.roomId ?? payload?.room_id)
	);
	if (!eventRoomID) {
		return true;
	}

	const activityCandidate =
		payload && 'activity' in payload ? payload.activity : (source.activity ?? source.payload);
	addBoardActivityFromSocket(activityCandidate, eventRoomID);
	return true;
}
