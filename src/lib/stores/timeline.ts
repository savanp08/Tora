import { get, writable } from 'svelte/store';
import type {
	ProjectTimeline,
	Sprint,
	TimelineTask,
	TimelineTaskDurationUnit,
	TimelineTaskPriority,
	TimelineTaskStatus
} from '$lib/types/timeline';
import { addBoardActivity } from '$lib/stores/boardActivity';
import { currentUser } from '$lib/store';
import { normalizeRoomIDValue } from '$lib/utils/chat/core';

const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';

type TimelineErrorResponse = {
	error?: string;
	message?: string;
};

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

type RoomTaskRecord = {
	id: string;
	title: string;
	description: string;
	status: string;
	sprintName: string;
	statusActorID: string;
	statusActorName: string;
	statusChangedAt: number;
	createdAt: number;
	updatedAt: number;
};

type ParsedTaskMetadata = {
	cleanDescription: string;
	type: string;
	effortScore: number;
	sprintStartDate: string;
	sprintEndDate: string;
	durationUnit: TimelineTaskDurationUnit;
	durationValue: number;
};

type TimelineSprintAccumulator = {
	name: string;
	startDate: string;
	endDate: string;
	earliestCreatedAt: number;
	tasks: TimelineTask[];
};

export type ProjectTab = 'overview' | 'tasks' | 'progress' | 'table' | 'tora_ai';

// ─── AI Output Format Schema (injected into every AI prompt) ─────────────────
// This tells the AI model exactly what JSON structure to return so the frontend
// can parse it without guesswork.  Keep it compact so it doesn't dominate the
// user's prompt but complete enough that the AI won't omit fields.
export const AI_TIMELINE_FORMAT_HINT = `
[OUTPUT FORMAT – return ONLY valid JSON, no markdown, no extra text]
{
  "project_name": "string",
  "description": "string",
  "tech_stack": ["string"],
  "target_audience": "string",
  "estimated_cost": "$N,NNN",
  "budget_total": number,
  "roles_needed": ["string"],
  "sprints": [
    {
      "id": "sprint-N",
      "name": "Sprint N: Label",
      "start_date": "YYYY-MM-DD",
      "end_date": "YYYY-MM-DD",
      "goal": "one-line sprint goal",
      "budget_allocated": number,
      "tasks": [
        {
          "id": "t-N-N",
          "title": "string",
          "status": "todo | in_progress | done",
          "priority": "critical | high | medium | low",
          "effort_score": 1-10,
          "type": "backend | frontend | design | qa | planning | strategy | general",
          "assignee": "Role or Name",
          "description": "string",
          "duration_value": number,
          "duration_unit": "days | hours"
        }
      ]
    }
  ]
}
[END FORMAT]
`.trim();

export const projectTimeline = writable<ProjectTimeline | null>(null);
export const timelineLoading = writable(false);
export const timelineError = writable('');
export const activeProjectTab = writable<ProjectTab>('overview');
export const isProjectNew = writable<boolean>(true);

let activeTimelineRoomId = '';
let activeTimelineLoadToken = 0;

function toRecord(value: unknown): Record<string, unknown> | null {
	if (!value || typeof value !== 'object' || Array.isArray(value)) {
		return null;
	}
	return value as Record<string, unknown>;
}

function toStringValue(value: unknown) {
	return typeof value === 'string' ? value.trim() : '';
}

function toNumberValue(value: unknown, fallback: number) {
	if (typeof value === 'number' && Number.isFinite(value)) {
		return value;
	}
	if (typeof value === 'string') {
		const parsed = Number(value);
		if (Number.isFinite(parsed)) {
			return parsed;
		}
	}
	return fallback;
}

function normalizeDurationUnit(value: unknown): TimelineTaskDurationUnit {
	const normalized = toStringValue(value).toLowerCase();
	if (normalized === 'hour' || normalized === 'hours') {
		return 'hours';
	}
	return 'days';
}

function normalizeDurationValue(value: unknown, unit: TimelineTaskDurationUnit) {
	const parsed = toNumberValue(value, unit === 'hours' ? 4 : 1);
	if (!Number.isFinite(parsed) || parsed <= 0) {
		return unit === 'hours' ? 4 : 1;
	}
	return parsed;
}

function parseTimelineDate(value: string, fallback: Date) {
	const parsed = Date.parse(value);
	if (Number.isFinite(parsed)) {
		return new Date(parsed);
	}
	return new Date(fallback.getTime());
}

function toTimelineDateString(value: Date) {
	return value.toISOString().slice(0, 10);
}

function getTaskEndDate(
	startDate: Date,
	durationUnit: TimelineTaskDurationUnit,
	durationValue: number
) {
	const endDate = new Date(startDate.getTime());
	if (durationUnit === 'hours') {
		endDate.setTime(endDate.getTime() + durationValue * 60 * 60 * 1000);
		return endDate;
	}
	endDate.setDate(endDate.getDate() + durationValue);
	return endDate;
}

function normalizeTaskStatus(value: unknown): TimelineTaskStatus {
	const normalized = toStringValue(value).toLowerCase().replace(/\s+/g, '_');
	if (normalized === 'in_progress') {
		return 'in_progress';
	}
	if (normalized === 'done' || normalized === 'completed') {
		return 'done';
	}
	return 'todo';
}

function normalizeTaskPriority(value: unknown): TimelineTaskPriority | undefined {
	const normalized = toStringValue(value).toLowerCase();
	if (normalized === 'critical') return 'critical';
	if (normalized === 'high') return 'high';
	if (normalized === 'medium') return 'medium';
	if (normalized === 'low') return 'low';
	return undefined;
}

function normalizeTask(raw: unknown, fallbackID: string): TimelineTask | null {
	const source = toRecord(raw);
	if (!source) {
		return null;
	}
	const title = toStringValue(source.title);
	if (!title) {
		return null;
	}

	const taskID = toStringValue(source.id) || toStringValue(source.task_id) || fallbackID;
	const effort = Math.max(1, Math.min(10, Math.floor(toNumberValue(source.effort_score, 3))));
	const durationUnit = normalizeDurationUnit(source.duration_unit ?? source.durationUnit);
	const durationValue = normalizeDurationValue(
		source.duration_value ?? source.durationValue,
		durationUnit
	);
	const priority = normalizeTaskPriority(source.priority);
	const assignee = toStringValue(source.assignee) || undefined;
	const statusActorID = toStringValue(source.status_actor_id ?? source.statusActorId);
	const statusActorName = toStringValue(source.status_actor_name ?? source.statusActorName);
	const statusChangedAt = toStringValue(source.status_changed_at ?? source.statusChangedAt);

	return {
		id: taskID,
		title,
		status: normalizeTaskStatus(source.status),
		effort_score: effort,
		type: toStringValue(source.type) || 'general',
		priority,
		assignee,
		status_actor_id: statusActorID || undefined,
		status_actor_name: statusActorName || undefined,
		status_changed_at: statusChangedAt || undefined,
		description: toStringValue(source.description) || undefined,
		start_date: toStringValue(source.start_date ?? source.startDate),
		end_date: toStringValue(source.end_date ?? source.endDate),
		duration_unit: durationUnit,
		duration_value: durationValue
	};
}

function normalizeSprint(raw: unknown, sprintIndex: number): Sprint | null {
	const source = toRecord(raw);
	if (!source) {
		return null;
	}
	const sprintName = toStringValue(source.name);
	if (!sprintName) {
		return null;
	}

	const rawTasks = Array.isArray(source.tasks) ? source.tasks : [];
	const tasks = rawTasks
		.map((task, taskIndex) => normalizeTask(task, `task-${sprintIndex + 1}-${taskIndex + 1}`))
		.filter((task): task is TimelineTask => Boolean(task));

	if (tasks.length === 0) {
		return null;
	}

	const goal = toStringValue(source.goal) || undefined;
	const budgetAllocated = toNumberValue(source.budget_allocated ?? source.budgetAllocated, 0);

	return {
		id: toStringValue(source.id) || `sprint-${sprintIndex + 1}`,
		name: sprintName,
		start_date: toStringValue(source.start_date),
		end_date: toStringValue(source.end_date),
		goal,
		budget_allocated: budgetAllocated > 0 ? budgetAllocated : undefined,
		tasks
	};
}

function normalizeTimeline(payload: unknown): ProjectTimeline {
	const source = toRecord(payload);
	if (!source) {
		throw new Error('Invalid timeline response');
	}

	const sprintsSource = Array.isArray(source.sprints) ? source.sprints : [];
	const sprints = sprintsSource
		.map((sprint, sprintIndex) => normalizeSprint(sprint, sprintIndex))
		.filter((sprint): sprint is Sprint => Boolean(sprint));
	if (sprints.length === 0) {
		throw new Error('No valid sprints returned from timeline response');
	}

	const projectName = toStringValue(source.project_name) || 'Project Timeline';
	const description = toStringValue(source.description) || undefined;
	const techStack = Array.isArray(source.tech_stack)
		? source.tech_stack.map((entry) => toStringValue(entry)).filter(Boolean)
		: [];
	const targetAudience = toStringValue(source.target_audience);
	const estimatedCost = toStringValue(source.estimated_cost);
	const budgetTotal = toNumberValue(source.budget_total ?? source.budgetTotal, 0);
	const budgetSpent = toNumberValue(source.budget_spent ?? source.budgetSpent, 0);
	const rolesNeeded = Array.isArray(source.roles_needed)
		? source.roles_needed.map((entry) => toStringValue(entry)).filter(Boolean)
		: [];
	const missingSprints = Array.isArray(source.missing_sprints)
		? source.missing_sprints.map((entry) => toStringValue(entry)).filter(Boolean)
		: [];
	const normalized: ProjectTimeline = {
		project_name: projectName,
		description,
		tech_stack: techStack,
		target_audience: targetAudience,
		estimated_cost: estimatedCost,
		budget_total: budgetTotal > 0 ? budgetTotal : undefined,
		budget_spent: budgetSpent > 0 ? budgetSpent : undefined,
		roles_needed: rolesNeeded,
		is_partial: Boolean(source.is_partial),
		missing_sprints: missingSprints,
		total_progress: 0,
		sprints
	};
	normalized.total_progress = calculateTotalProgress(normalized);
	return normalized;
}

async function parseErrorMessage(response: Response) {
	const payload = (await response.json().catch(() => null)) as TimelineErrorResponse | null;
	return payload?.error?.trim() || payload?.message?.trim() || `HTTP ${response.status}`;
}

function withTimelineUserHeaders(userID: string, headers: Record<string, string> = {}) {
	if (!userID) {
		return headers;
	}
	return {
		...headers,
		'X-User-Id': userID
	};
}

function parseTaskTimestamp(value: unknown) {
	if (typeof value === 'number' && Number.isFinite(value)) {
		return value;
	}
	if (typeof value === 'string') {
		const numeric = Number(value);
		if (Number.isFinite(numeric)) {
			return numeric;
		}
		const parsed = Date.parse(value);
		if (Number.isFinite(parsed)) {
			return parsed;
		}
	}
	return Date.now();
}

function parseTaskTimestampOptional(value: unknown) {
	if (typeof value === 'number' && Number.isFinite(value)) {
		return value;
	}
	if (typeof value === 'string') {
		const numeric = Number(value);
		if (Number.isFinite(numeric)) {
			return numeric;
		}
		const parsed = Date.parse(value);
		if (Number.isFinite(parsed)) {
			return parsed;
		}
	}
	return 0;
}

function normalizeRoomTaskRecord(raw: unknown): RoomTaskRecord | null {
	const source = toRecord(raw);
	if (!source) {
		return null;
	}
	const id = toStringValue(source.id);
	const title = toStringValue(source.title);
	if (!id || !title) {
		return null;
	}
	return {
		id,
		title,
		description: toStringValue(source.description),
		status: toStringValue(source.status),
		sprintName: toStringValue(source.sprint_name ?? source.sprintName),
		statusActorID: toStringValue(source.status_actor_id ?? source.statusActorId),
		statusActorName: toStringValue(source.status_actor_name ?? source.statusActorName),
		statusChangedAt: parseTaskTimestampOptional(source.status_changed_at ?? source.statusChangedAt),
		createdAt: parseTaskTimestamp(source.created_at ?? source.createdAt),
		updatedAt: parseTaskTimestamp(source.updated_at ?? source.updatedAt)
	};
}

function parseTaskMetadata(description: string): ParsedTaskMetadata {
	const trimmed = description.trim();
	if (!trimmed) {
		return {
			cleanDescription: '',
			type: 'general',
			effortScore: 3,
			sprintStartDate: '',
			sprintEndDate: '',
			durationUnit: 'days',
			durationValue: 1
		};
	}

	const metadataMatch = trimmed.match(/\[([^\]]+)\]\s*$/);
	if (!metadataMatch) {
		return {
			cleanDescription: trimmed,
			type: 'general',
			effortScore: 3,
			sprintStartDate: '',
			sprintEndDate: '',
			durationUnit: 'days',
			durationValue: 1
		};
	}

	let parsedType = 'general';
	let parsedEffortScore = 3;
	let sprintStartDate = '';
	let sprintEndDate = '';
	let parsedDurationUnit: TimelineTaskDurationUnit = 'days';
	let parsedDurationValue = 1;
	const metadataBody = metadataMatch[1] ?? '';
	for (const section of metadataBody.split('|')) {
		const [rawKey, ...rawValueParts] = section.split(':');
		const key = toStringValue(rawKey).toLowerCase();
		const rawValue = rawValueParts.join(':').trim();
		if (!key || !rawValue) {
			continue;
		}
		if (key === 'type') {
			parsedType = rawValue.toLowerCase() || 'general';
			continue;
		}
		if (key === 'effort') {
			const numeric = Number(rawValue);
			if (Number.isFinite(numeric)) {
				parsedEffortScore = Math.max(1, Math.min(10, Math.floor(numeric)));
			}
			continue;
		}
		if (key === 'sprint' || key === 'sprint window') {
			const dateWindowMatch = rawValue.match(/(\d{4}-\d{2}-\d{2})\s*->\s*(\d{4}-\d{2}-\d{2})/);
			if (dateWindowMatch) {
				sprintStartDate = dateWindowMatch[1];
				sprintEndDate = dateWindowMatch[2];
			}
			continue;
		}
		if (key === 'duration') {
			const durationMatch = rawValue.match(/(-?\d+(?:\.\d+)?)\s*(hour|hours|day|days)?/i);
			if (durationMatch) {
				parsedDurationUnit = normalizeDurationUnit(durationMatch[2] ?? 'days');
				parsedDurationValue = normalizeDurationValue(durationMatch[1], parsedDurationUnit);
			}
		}
	}

	return {
		cleanDescription: trimmed.slice(0, metadataMatch.index).trim(),
		type: parsedType,
		effortScore: parsedEffortScore,
		sprintStartDate,
		sprintEndDate,
		durationUnit: parsedDurationUnit,
		durationValue: parsedDurationValue
	};
}

function createSprintId(seed: string, index: number) {
	const normalized = seed
		.toLowerCase()
		.replace(/[^a-z0-9]+/g, '-')
		.replace(/^-+|-+$/g, '');
	if (normalized) {
		return `sprint-${normalized}-${index + 1}`;
	}
	return `sprint-${index + 1}`;
}

function buildTimelineFromRoomTasks(
	taskRecords: RoomTaskRecord[],
	fallbackProjectName = 'Project Timeline'
): ProjectTimeline | null {
	if (taskRecords.length === 0) {
		return null;
	}

	const sprintMap = new Map<string, TimelineSprintAccumulator>();
	for (const taskRecord of taskRecords) {
		const sprintName = taskRecord.sprintName || 'Backlog';
		const existing = sprintMap.get(sprintName) ?? {
			name: sprintName,
			startDate: '',
			endDate: '',
			earliestCreatedAt: taskRecord.createdAt,
			tasks: []
		};
		const metadata = parseTaskMetadata(taskRecord.description);
		if (!existing.startDate && metadata.sprintStartDate) {
			existing.startDate = metadata.sprintStartDate;
		}
		if (!existing.endDate && metadata.sprintEndDate) {
			existing.endDate = metadata.sprintEndDate;
		}
		existing.earliestCreatedAt = Math.min(existing.earliestCreatedAt, taskRecord.createdAt);
		existing.tasks.push({
			id: taskRecord.id,
			title: taskRecord.title,
			status: normalizeTaskStatus(taskRecord.status),
			effort_score: metadata.effortScore,
			type: metadata.type,
			status_actor_id: taskRecord.statusActorID || undefined,
			status_actor_name: taskRecord.statusActorName || undefined,
			status_changed_at:
				taskRecord.statusChangedAt > 0
					? new Date(taskRecord.statusChangedAt).toISOString()
					: undefined,
			description: metadata.cleanDescription || undefined,
			start_date: '',
			end_date: '',
			duration_unit: metadata.durationUnit,
			duration_value: metadata.durationValue
		});
		sprintMap.set(sprintName, existing);
	}

	const sprintEntries = [...sprintMap.values()].sort((left, right) => {
		if (left.startDate && right.startDate) {
			if (left.startDate !== right.startDate) {
				return left.startDate.localeCompare(right.startDate);
			}
		}
		if (left.earliestCreatedAt !== right.earliestCreatedAt) {
			return left.earliestCreatedAt - right.earliestCreatedAt;
		}
		return left.name.localeCompare(right.name, undefined, { sensitivity: 'base' });
	});

	const sprints: Sprint[] = sprintEntries.map((entry, index) => ({
		id: createSprintId(entry.name, index),
		name: entry.name,
		start_date: entry.startDate,
		end_date: entry.endDate,
		tasks: entry.tasks
	}));
	if (sprints.length === 0) {
		return null;
	}

	return {
		project_name: fallbackProjectName,
		tech_stack: [],
		target_audience: '',
		estimated_cost: '',
		roles_needed: [],
		is_partial: false,
		missing_sprints: [],
		total_progress: 0,
		sprints
	};
}

function applyTaskDurationDefaults(task: TimelineTask): TimelineTask {
	const durationUnit = normalizeDurationUnit(task.duration_unit);
	const durationValue = normalizeDurationValue(task.duration_value, durationUnit);
	return {
		...task,
		duration_unit: durationUnit,
		duration_value: durationValue
	};
}

export function recalculateGanttDates(tasks: TimelineTask[], projectStart: Date): TimelineTask[] {
	const startSeed = Number.isFinite(projectStart.getTime()) ? projectStart : new Date();
	let currentStart = new Date(startSeed.getTime());

	return tasks.map((task) => {
		const taskWithDuration = applyTaskDurationDefaults(task);
		const taskStart = new Date(currentStart.getTime());
		const taskEnd = getTaskEndDate(
			taskStart,
			taskWithDuration.duration_unit || 'days',
			taskWithDuration.duration_value || 1
		);
		currentStart = new Date(taskEnd.getTime());
		return {
			...taskWithDuration,
			start_date: toTimelineDateString(taskStart),
			end_date: toTimelineDateString(taskEnd)
		};
	});
}

function applyTimelineGanttDates(timeline: ProjectTimeline): ProjectTimeline {
	const nextSprints: Sprint[] = [];
	let nextSprintSeed = parseTimelineDate(timeline.sprints[0]?.start_date || '', new Date());

	for (const sprint of timeline.sprints) {
		const taskDefaults = sprint.tasks.map((task) => applyTaskDurationDefaults(task));
		const hasCompleteDates = taskDefaults.every((task) =>
			Boolean(task.start_date && task.end_date)
		);
		const sprintStartSeed = parseTimelineDate(sprint.start_date || '', nextSprintSeed);
		const nextTasks = hasCompleteDates
			? taskDefaults
			: recalculateGanttDates(taskDefaults, sprintStartSeed);
		const sprintStart = nextTasks[0]?.start_date || toTimelineDateString(sprintStartSeed);
		const sprintEnd = nextTasks[nextTasks.length - 1]?.end_date || sprint.end_date || sprintStart;
		nextSprints.push({
			...sprint,
			start_date: sprintStart,
			end_date: sprintEnd,
			tasks: nextTasks
		});
		nextSprintSeed = parseTimelineDate(sprintEnd, nextSprintSeed);
	}

	return {
		...timeline,
		sprints: nextSprints
	};
}

function recalculateSprintDatesFromIndex(
	tasks: TimelineTask[],
	taskIndex: number,
	startDate: Date
) {
	const nextTasks = [...tasks];
	let currentStart = new Date(startDate.getTime());
	for (let index = taskIndex; index < nextTasks.length; index += 1) {
		const taskWithDuration = applyTaskDurationDefaults(nextTasks[index]);
		const taskStart = new Date(currentStart.getTime());
		const taskEnd = getTaskEndDate(
			taskStart,
			taskWithDuration.duration_unit || 'days',
			taskWithDuration.duration_value || 1
		);
		nextTasks[index] = {
			...taskWithDuration,
			start_date: toTimelineDateString(taskStart),
			end_date: toTimelineDateString(taskEnd)
		};
		currentStart = new Date(taskEnd.getTime());
	}
	return nextTasks;
}

export function calculateTotalProgress(timeline: ProjectTimeline) {
	const allTasks = timeline.sprints.flatMap((sprint) => sprint.tasks);
	const total = allTasks.length;
	if (total === 0) {
		return 0;
	}
	const completed = allTasks.filter((task) => task.status === 'done').length;
	return Number(((completed / total) * 100).toFixed(1));
}

export function setProjectTimeline(value: ProjectTimeline | null) {
	if (!value) {
		projectTimeline.set(null);
		isProjectNew.set(true);
		return;
	}
	const timelineWithDefaults: ProjectTimeline = {
		...value,
		tech_stack: value.tech_stack ?? [],
		target_audience: value.target_audience ?? '',
		estimated_cost: value.estimated_cost ?? '',
		roles_needed: value.roles_needed ?? [],
		is_partial: Boolean(value.is_partial),
		missing_sprints: value.missing_sprints ?? []
	};
	const timelineWithDates = applyTimelineGanttDates(timelineWithDefaults);
	const nextValue: ProjectTimeline = {
		...timelineWithDates,
		total_progress: calculateTotalProgress(timelineWithDates)
	};
	projectTimeline.set(nextValue);
	isProjectNew.set(false);
}

export function applyTimelinePayload(payload: unknown) {
	const normalized = normalizeTimeline(payload);
	setProjectTimeline(normalized);
	timelineError.set('');
	return normalized;
}

export function updateTaskDates(taskId: string, newStart: Date | string) {
	const normalizedTaskID = taskId.trim();
	if (!normalizedTaskID) {
		return false;
	}

	const timeline = get(projectTimeline);
	if (!timeline) {
		return false;
	}

	const manualStart =
		typeof newStart === 'string'
			? parseTimelineDate(newStart, new Date())
			: Number.isFinite(newStart.getTime())
				? new Date(newStart.getTime())
				: new Date();

	let didUpdate = false;
	const nextSprints = timeline.sprints.map((sprint) => {
		const taskIndex = sprint.tasks.findIndex((task) => task.id === normalizedTaskID);
		if (taskIndex < 0) {
			return sprint;
		}
		didUpdate = true;
		const nextTasks = recalculateSprintDatesFromIndex(
			sprint.tasks.map((task) => applyTaskDurationDefaults(task)),
			taskIndex,
			manualStart
		);
		return {
			...sprint,
			start_date: nextTasks[0]?.start_date || sprint.start_date,
			end_date: nextTasks[nextTasks.length - 1]?.end_date || sprint.end_date,
			tasks: nextTasks
		};
	});

	if (!didUpdate) {
		return false;
	}

	const nextTimeline: ProjectTimeline = {
		...timeline,
		sprints: nextSprints,
		total_progress: calculateTotalProgress({
			...timeline,
			sprints: nextSprints
		})
	};
	projectTimeline.set(nextTimeline);
	return true;
}

export function applyTimelineTaskStatusUpdate(
	taskId: string,
	status: TimelineTaskStatus,
	metadata?: {
		statusActorId?: string;
		statusActorName?: string;
		statusChangedAt?: string | number | Date;
	}
) {
	const normalizedTaskID = taskId.trim();
	if (!normalizedTaskID) {
		return false;
	}

	const timeline = get(projectTimeline);
	if (!timeline) {
		return false;
	}

	const normalizedStatus = normalizeTaskStatus(status);
	const statusActorID = toStringValue(metadata?.statusActorId);
	const statusActorName = toStringValue(metadata?.statusActorName);
	const statusChangedAtRaw = metadata?.statusChangedAt;
	let statusChangedAtISO = new Date().toISOString();
	if (typeof statusChangedAtRaw === 'string') {
		const parsed = Date.parse(statusChangedAtRaw);
		if (Number.isFinite(parsed)) {
			statusChangedAtISO = new Date(parsed).toISOString();
		}
	} else if (typeof statusChangedAtRaw === 'number' && Number.isFinite(statusChangedAtRaw)) {
		statusChangedAtISO = new Date(statusChangedAtRaw).toISOString();
	} else if (statusChangedAtRaw instanceof Date && Number.isFinite(statusChangedAtRaw.getTime())) {
		statusChangedAtISO = statusChangedAtRaw.toISOString();
	}

	let didUpdate = false;
	const nextSprints = timeline.sprints.map((sprint) => {
		let sprintTouched = false;
		const nextTasks = sprint.tasks.map((task) => {
			if (task.id !== normalizedTaskID) {
				return task;
			}
			sprintTouched = true;
			didUpdate = true;
			return {
				...task,
				status: normalizedStatus,
				status_actor_id: statusActorID || undefined,
				status_actor_name: statusActorName || undefined,
				status_changed_at: statusChangedAtISO
			};
		});
		if (!sprintTouched) {
			return sprint;
		}
		return {
			...sprint,
			tasks: nextTasks
		};
	});

	if (!didUpdate) {
		return false;
	}

	const nextTimeline: ProjectTimeline = {
		...timeline,
		sprints: nextSprints,
		total_progress: calculateTotalProgress({
			...timeline,
			sprints: nextSprints
		})
	};
	projectTimeline.set(nextTimeline);
	return true;
}

// Compress the timeline state for large projects to avoid huge AI payloads.
// Strips long descriptions but keeps all structural/status fields.
function compressTimelineForAI(timeline: ProjectTimeline): ProjectTimeline {
	const raw = JSON.stringify(timeline);
	if (raw.length < 24000) {
		return timeline;
	}
	return {
		...timeline,
		sprints: timeline.sprints.map((sprint) => ({
			...sprint,
			tasks: sprint.tasks.map((task) => ({
				id: task.id,
				title: task.title,
				status: task.status,
				priority: task.priority,
				type: task.type,
				effort_score: task.effort_score,
				assignee: task.assignee,
				duration_value: task.duration_value,
				duration_unit: task.duration_unit
				// description omitted to save tokens
			}))
		}))
	};
}

export async function generateAITimeline(roomId: string, prompt: string) {
	const normalizedRoomID = roomId.trim();
	const normalizedPrompt = prompt.trim();
	const sessionUserID = (get(currentUser)?.id ?? '').trim();
	if (!normalizedRoomID) {
		throw new Error('roomId is required');
	}
	if (!normalizedPrompt) {
		throw new Error('prompt is required');
	}

	const enrichedPrompt = `${AI_TIMELINE_FORMAT_HINT}\n\nUSER REQUEST:\n${normalizedPrompt}`;

	timelineLoading.set(true);
	timelineError.set('');
	try {
		const response = await fetch(
			`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomID)}/ai-timeline`,
			{
				method: 'POST',
				headers: withTimelineUserHeaders(sessionUserID, {
					'Content-Type': 'application/json'
				}),
				credentials: 'include',
				body: JSON.stringify({
					prompt: enrichedPrompt,
					userId: sessionUserID
				})
			}
		);
		if (!response.ok) {
			throw new Error(await parseErrorMessage(response));
		}

		const payload = (await response.json().catch(() => null)) as unknown;
		const normalized = applyTimelinePayload(payload);
		addBoardActivity({
			type: 'board_generated',
			title: 'Board generated by Tora AI',
			subtitle: normalized.project_name
		});
		return normalized;
	} catch (error) {
		const message = error instanceof Error ? error.message : 'Failed to generate project timeline';
		timelineError.set(message);
		throw error instanceof Error ? error : new Error(message);
	} finally {
		timelineLoading.set(false);
	}
}

export async function editAITimeline(
	roomId: string,
	prompt: string,
	currentState: ProjectTimeline | null
) {
	const normalizedRoomID = roomId.trim();
	const normalizedPrompt = prompt.trim();
	const sessionUserID = (get(currentUser)?.id ?? '').trim();
	if (!normalizedRoomID) {
		throw new Error('roomId is required');
	}
	if (!normalizedPrompt) {
		throw new Error('prompt is required');
	}

	const enrichedEditPrompt = `${AI_TIMELINE_FORMAT_HINT}\n\nEDIT REQUEST:\n${normalizedPrompt}`;

	timelineLoading.set(true);
	timelineError.set('');
	try {
		const response = await fetch(
			`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomID)}/ai-edit`,
			{
				method: 'POST',
				headers: withTimelineUserHeaders(sessionUserID, {
					'Content-Type': 'application/json'
				}),
				credentials: 'include',
				body: JSON.stringify({
					prompt: enrichedEditPrompt,
					current_state: currentState ? compressTimelineForAI(currentState) : null,
					userId: sessionUserID
				})
			}
		);
		if (!response.ok) {
			throw new Error(await parseErrorMessage(response));
		}

		const payload = (await response.json().catch(() => null)) as unknown;
		const normalized = applyTimelinePayload(payload);
		addBoardActivity({
			type: 'board_edited',
			title: 'Board updated by Tora AI',
			subtitle: normalized.project_name
		});
		return normalized;
	} catch (error) {
		const message = error instanceof Error ? error.message : 'Failed to edit project timeline';
		timelineError.set(message);
		throw error instanceof Error ? error : new Error(message);
	} finally {
		timelineLoading.set(false);
	}
}

export async function initializeProjectTimelineForRoom(
	roomId: string,
	options?: {
		fetchImpl?: FetchLike;
		apiBase?: string;
		fallbackProjectName?: string;
	}
) {
	const normalizedRoomID = normalizeRoomIDValue(roomId);
	activeTimelineRoomId = normalizedRoomID;
	activeTimelineLoadToken += 1;
	const loadToken = activeTimelineLoadToken;

	if (!normalizedRoomID) {
		setProjectTimeline(null);
		timelineError.set('');
		timelineLoading.set(false);
		return null;
	}

	timelineLoading.set(true);
	timelineError.set('');
	setProjectTimeline(null);

	const fetchImpl = options?.fetchImpl ?? fetch;
	const apiBase = options?.apiBase?.trim() || API_BASE;

	try {
		const response = await fetchImpl(
			`${apiBase}/api/rooms/${encodeURIComponent(normalizedRoomID)}/tasks`,
			{
				method: 'GET',
				credentials: 'include'
			}
		);
		if (!response.ok) {
			throw new Error(await parseErrorMessage(response));
		}

		const payload = (await response.json().catch(() => [])) as unknown;
		if (loadToken !== activeTimelineLoadToken || normalizedRoomID !== activeTimelineRoomId) {
			return null;
		}

		const records = Array.isArray(payload) ? payload : [];
		const normalizedTasks = records
			.map((record) => normalizeRoomTaskRecord(record))
			.filter((record): record is RoomTaskRecord => Boolean(record));
		const timeline = buildTimelineFromRoomTasks(
			normalizedTasks,
			options?.fallbackProjectName || 'Project Timeline'
		);
		setProjectTimeline(timeline);
		timelineError.set('');
		return timeline;
	} catch (error) {
		if (loadToken === activeTimelineLoadToken) {
			setProjectTimeline(null);
			timelineError.set(error instanceof Error ? error.message : 'Failed to load project timeline');
		}
		return null;
	} finally {
		if (loadToken === activeTimelineLoadToken) {
			timelineLoading.set(false);
		}
	}
}
