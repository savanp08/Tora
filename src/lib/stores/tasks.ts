import { writable } from 'svelte/store';
import { normalizeRoomIDValue, parseOptionalTimestamp, toStringValue } from '$lib/utils/chat/core';

const DEFAULT_API_BASE = 'http://127.0.0.1:8080';

export type TaskStatus = 'todo' | 'in_progress' | 'done' | string;

export type TaskSubtask = {
	id: string;
	content: string;
	completed: boolean;
	position: number;
};

export type TaskRole = {
	role: string;
	responsibilities: string;
};

export type Task = {
	id: string;
	roomId: string;
	title: string;
	description: string;
	status: TaskStatus;
	taskType: string;
	customFields?: Record<string, unknown>;
	blockedBy: string[];
	blocks: string[];
	subtasks: TaskSubtask[];
	completionPercent?: number;
	budget?: number;
	spent?: number;
	sprintName: string;
	assigneeId: string;
	dueDate?: number;
	startDate?: number;
	roles?: TaskRole[];
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

function normalizeTaskBudgetValue(value: unknown): number | undefined {
	if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
		return value;
	}
	if (typeof value === 'string') {
		const match = value.replace(/,/g, '').match(/-?\d+(?:\.\d+)?/);
		if (!match) {
			return undefined;
		}
		const parsed = Number(match[0]);
		if (Number.isFinite(parsed) && parsed >= 0) {
			return parsed;
		}
	}
	return undefined;
}

function normalizeTaskCustomFields(value: unknown): Record<string, unknown> | undefined {
	let source: Record<string, unknown> | null = null;
	if (typeof value === 'string') {
		const trimmed = value.trim();
		if (!trimmed) {
			return undefined;
		}
		try {
			source = toRecord(JSON.parse(trimmed));
		} catch {
			source = null;
		}
	} else {
		source = toRecord(value);
	}
	if (!source) {
		return undefined;
	}
	const normalized: Record<string, unknown> = {};
	for (const [rawKey, rawValue] of Object.entries(source)) {
		const key = rawKey.trim();
		if (!key) {
			continue;
		}
		normalized[key] = rawValue;
	}
	return Object.keys(normalized).length > 0 ? normalized : undefined;
}

function normalizeTaskRelationID(value: unknown) {
	return toStringValue(value).trim();
}

function normalizeTaskRelationIDs(value: unknown): string[] {
	if (!Array.isArray(value)) {
		return [];
	}
	const seen = new Set<string>();
	const next: string[] = [];
	for (const entry of value) {
		const relationID = normalizeTaskRelationID(entry);
		if (!relationID || seen.has(relationID)) {
			continue;
		}
		seen.add(relationID);
		next.push(relationID);
	}
	return next;
}

function normalizeTaskSubtasks(value: unknown): TaskSubtask[] {
	if (!Array.isArray(value)) {
		return [];
	}
	const next: TaskSubtask[] = [];
	const seen = new Set<string>();
	for (const entry of value) {
		const source = toRecord(entry);
		if (!source) {
			continue;
		}
		const id = normalizeTaskRelationID(source.id ?? source.subtask_id ?? source.subtaskId);
		if (!id || seen.has(id)) {
			continue;
		}
		seen.add(id);
		const content = toStringValue(source.content ?? source.title).trim() || 'Subtask';
		const completed = Boolean(source.completed);
		const positionRaw = source.position ?? source.order ?? source.index;
		let position = 0;
		if (typeof positionRaw === 'number' && Number.isFinite(positionRaw)) {
			position = Math.max(0, Math.floor(positionRaw));
		} else if (typeof positionRaw === 'string') {
			const parsed = Number(positionRaw);
			if (Number.isFinite(parsed)) {
				position = Math.max(0, Math.floor(parsed));
			}
		}
		next.push({
			id,
			content,
			completed,
			position
		});
	}
	next.sort((left, right) => left.position - right.position || left.id.localeCompare(right.id));
	return next;
}

function normalizeTaskRoles(value: unknown): TaskRole[] | undefined {
	let source: unknown[] = [];
	if (Array.isArray(value)) {
		source = value;
	} else if (typeof value === 'string') {
		const trimmed = value.trim();
		if (!trimmed) {
			return undefined;
		}
		try {
			const parsed = JSON.parse(trimmed) as unknown;
			if (!Array.isArray(parsed)) {
				return undefined;
			}
			source = parsed;
		} catch {
			return undefined;
		}
	} else {
		return undefined;
	}

	const roles: TaskRole[] = [];
	for (const entry of source) {
		const sourceRole = toRecord(entry);
		if (!sourceRole) {
			continue;
		}
		const role = toStringValue(sourceRole.role).trim();
		const responsibilities = toStringValue(sourceRole.responsibilities).trim();
		if (!role && !responsibilities) {
			continue;
		}
		roles.push({
			role,
			responsibilities
		});
	}
	return roles.length > 0 ? roles : undefined;
}

function calculateTaskCompletionPercent(subtasks: TaskSubtask[]) {
	if (subtasks.length === 0) {
		return undefined;
	}
	const completedCount = subtasks.filter((subtask) => subtask.completed).length;
	return Math.round((completedCount / subtasks.length) * 100);
}

function parseTaskBudgetFromDescription(description: string): number | undefined {
	const trimmed = description.trim();
	if (!trimmed) {
		return undefined;
	}
	const metadataMatch = trimmed.match(/\[([^\]]+)\]\s*$/);
	const metadataBody = (metadataMatch?.[1] ?? '').trim();
	if (!metadataBody) {
		return undefined;
	}
	const budgetMatch = metadataBody.match(/(?:^|\|)\s*budget\s*:\s*([^|\]]+)/i);
	if (!budgetMatch?.[1]) {
		return undefined;
	}
	return normalizeTaskBudgetValue(budgetMatch[1]);
}

function parseTaskSpentFromDescription(description: string): number | undefined {
	const trimmed = description.trim();
	if (!trimmed) {
		return undefined;
	}
	const metadataMatch = trimmed.match(/\[([^\]]+)\]\s*$/);
	const metadataBody = (metadataMatch?.[1] ?? '').trim();
	if (!metadataBody) {
		return undefined;
	}
	const spentMatch = metadataBody.match(
		/(?:^|\|)\s*(?:actual\s*cost|actual_cost|spent|cost)\s*:\s*([^|\]]+)/i
	);
	if (!spentMatch?.[1]) {
		return undefined;
	}
	return normalizeTaskBudgetValue(spentMatch[1]);
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
	const customFields = normalizeTaskCustomFields(
		source.customFields ?? source.custom_fields ?? source.custom_field_values
	);
	const blockedBy = normalizeTaskRelationIDs(source.blockedBy ?? source.blocked_by);
	const blocks = normalizeTaskRelationIDs(source.blocks);
	const subtasks = normalizeTaskSubtasks(source.subtasks ?? source.checklist);
	const completionPercent =
		normalizeTaskBudgetValue(source.completion_percent ?? source.completionPercent) ??
		calculateTaskCompletionPercent(subtasks);
	const budget =
		normalizeTaskBudgetValue(source.budget ?? source.task_budget ?? source.taskBudget) ??
		parseTaskBudgetFromDescription(description);
	const spent =
		normalizeTaskBudgetValue(
			source.actual_cost ??
				source.actualCost ??
				source.spent ??
				source.spent_cost ??
				source.spentCost
		) ?? parseTaskSpentFromDescription(description);
	const sprintName = toStringValue(source.sprintName ?? source.sprint_name).trim();
	const assigneeId = toStringValue(source.assigneeId ?? source.assignee_id).trim();
	const taskTypeRaw = toStringValue(source.taskType ?? source.task_type).trim().toLowerCase();
	const taskType = taskTypeRaw === 'support' ? 'support' : 'sprint';
	const roles = normalizeTaskRoles(source.roles);
	const statusActorId = toStringValue(source.statusActorId ?? source.status_actor_id).trim();
	const statusActorName = toStringValue(source.statusActorName ?? source.status_actor_name).trim();
	const statusChangedAt =
		parseOptionalTimestamp(source.statusChangedAt ?? source.status_changed_at) || 0;
	const dueDate = parseOptionalTimestamp(source.dueDate ?? source.due_date) || undefined;
	const startDate = parseOptionalTimestamp(source.startDate ?? source.start_date) || undefined;
	const now = Date.now();
	const createdAt = parseOptionalTimestamp(source.createdAt ?? source.created_at) || now;
	const updatedAt = parseOptionalTimestamp(source.updatedAt ?? source.updated_at) || createdAt;

	return {
		id,
		roomId,
		title,
		description,
		status,
		taskType,
		customFields,
		blockedBy,
		blocks,
		subtasks,
		completionPercent,
		budget,
		spent,
		sprintName,
		assigneeId,
		dueDate: dueDate && dueDate > 0 ? dueDate : undefined,
		startDate: startDate && startDate > 0 ? startDate : undefined,
		roles,
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
