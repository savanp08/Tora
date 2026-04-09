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
import { recordProjectServerDataDEBUG_DELETE_LATER } from '$lib/debug/projectServerDataDEBUG_DELETE_LATER';
import { sendSocketPayload } from '$lib/ws';
import { buildBoardActivitySocketPayload } from '$lib/ws/client';

const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';

type TimelineErrorResponse = {
	error?: string;
	message?: string;
	code?: string;
	stage?: string;
	detail?: string;
	retryable?: boolean;
	provider_status?: number;
	timeout_ms?: number;
	prompt_timeout_ms?: number;
};

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

type RoomTaskRecord = {
	id: string;
	title: string;
	description: string;
	status: string;
	budget?: number;
	actualCost?: number;
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
	budget: number;
	actualCost: number;
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
export type AITimelineIntent = 'chat' | 'modify_project' | 'generate_project' | 'clarify';
export type AITimelineConversationMessage = {
	role: 'user' | 'assistant';
	text: string;
	intent?: AITimelineIntent;
};
export type AITimelineResult = {
	timeline: ProjectTimeline | null;
	assistantReply: string;
	intent?: AITimelineIntent;
};
export type StreamAITimelineBlueprintEvent = {
	projectName: string;
	assistantReply: string;
	sprintNames: string[];
};
export type StreamAITimelineSprintEvent = {
	sprintIndex: number;
	sprintTotal: number;
	sprintName: string;
	taskCount: number;
	taskTitles: string[];
};
export type StreamAITimelineDoneEvent = {
	assistantReply: string;
	isPartial: boolean;
	missingSprints: string[];
};
export type StreamAITimelineErrorEvent = {
	message: string;
	isStopped: boolean;
};
export type StreamAIStatusMeta = {
	timeoutMs?: number;
	promptTimeoutMs?: number;
	strategy?: string;
};
export type StreamAITimelineCallbacks = {
	onStatus?: (
		step: string,
		label: string,
		sprintIndex?: number,
		sprintTotal?: number,
		meta?: StreamAIStatusMeta
	) => void;
	onChat?: (intent: string, assistantReply: string) => void;
	onBlueprint?: (event: StreamAITimelineBlueprintEvent) => void;
	onSprint?: (event: StreamAITimelineSprintEvent) => void;
	onDone?: (event: StreamAITimelineDoneEvent) => void;
	onError?: (event: StreamAITimelineErrorEvent) => void;
};
export type StreamAIEditTimelineCallbacks = {
	onStatus?: (
		step: string,
		label: string,
		appliedCount?: number,
		operationTotal?: number,
		meta?: StreamAIStatusMeta
	) => void;
	// Heartbeat label updates during long LLM calls — does NOT create a new
	// workflow entry, only updates the label on the currently active one.
	onProgress?: (step: string, label: string) => void;
	onPlan?: (assistantReply: string, operationTotal: number) => void;
	onOperation?: (summary: string, appliedCount: number, operationTotal: number) => void;
	// Incremental text chunks for the final assistant reply (typing effect).
	onTextDelta?: (delta: string) => void;
	onChat?: (intent: string, assistantReply: string) => void;
	onError?: (message: string, meta?: { isStopped?: boolean }) => void;
};

type TimelineEditProjectPatch = {
	project_name?: string;
	tech_stack?: string[];
	target_audience?: string;
	estimated_cost?: string;
	roles_needed?: string[];
};

type TimelineEditOperation = {
	op: 'add_task' | 'update_task' | 'delete_task';
	task_id?: string;
	id?: string;
	sprint_name?: string;
	title?: string;
	status?: string;
	task_type?: string;
	assignee_id?: string;
	budget?: number;
	actual_cost?: number;
	duration_unit?: TimelineTaskDurationUnit;
	duration_value?: number;
	description?: string;
};

// ─── AI Output Format Schema (injected into every AI prompt) ─────────────────
// This tells the AI model exactly what JSON structure to return so the frontend
// can parse it without guesswork.  Keep it compact so it doesn't dominate the
// user's prompt but complete enough that the AI won't omit fields.
export const AI_TIMELINE_FORMAT_HINT = `
[OUTPUT FORMAT – return ONLY valid JSON, no markdown, no extra text]
{
  "assistant_reply": "short user-facing explanation of what was generated/changed; tone must be professional, friendly, lightly witty/sarcastic, and never dismissive or arrogant",
  "timeline": {
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
            "budget": number,
            "actual_cost": number,
            "duration_value": number,
            "duration_unit": "days | hours"
          }
        ]
      }
    ]
  }
}
[END FORMAT]
`.trim();

export const projectTimeline = writable<ProjectTimeline | null>(null);
export const timelineLoading = writable(false);
export const timelineError = writable('');
export const activeProjectTab = writable<ProjectTab>('overview');
export const isProjectNew = writable<boolean>(true);
export const lastAIAssistantReply = writable('');
export const timelineCanStop = writable(false);

let activeTimelineRoomId = '';
let activeTimelineLoadToken = 0;
let activeTimelineRequestState:
	| {
			controller: AbortController;
			kind: 'generate' | 'edit';
			roomId: string;
			stopRequested: boolean;
	  }
	| null = null;

export class TimelineRequestStoppedError extends Error {
	timeline: ProjectTimeline | null;
	assistantReply: string;

	constructor(message: string, timeline: ProjectTimeline | null = null, assistantReply = '') {
		super(message);
		this.name = 'TimelineRequestStoppedError';
		this.timeline = timeline;
		this.assistantReply = assistantReply;
	}
}

export function isTimelineRequestStoppedError(
	error: unknown
): error is TimelineRequestStoppedError {
	return error instanceof Error && error.name === 'TimelineRequestStoppedError';
}

function setActiveTimelineRequest(
	state:
		| {
				controller: AbortController;
				kind: 'generate' | 'edit';
				roomId: string;
				stopRequested: boolean;
		  }
		| null
) {
	activeTimelineRequestState = state;
	timelineCanStop.set(Boolean(state));
}

export function stopActiveTimelineRequest() {
	if (!activeTimelineRequestState) {
		return false;
	}
	activeTimelineRequestState.stopRequested = true;
	activeTimelineRequestState.controller.abort();
	return true;
}

function toRecord(value: unknown): Record<string, unknown> | null {
	if (!value || typeof value !== 'object' || Array.isArray(value)) {
		return null;
	}
	return value as Record<string, unknown>;
}

function toStringValue(value: unknown) {
	return typeof value === 'string' ? value.trim() : '';
}

function isAbortError(error: unknown) {
	return Boolean(
		error &&
			typeof error === 'object' &&
			'name' in error &&
			(error as { name?: string }).name === 'AbortError'
	);
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

function normalizeTaskBudget(value: unknown): number | undefined {
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
	const budget = normalizeTaskBudget(source.budget ?? source.task_budget ?? source.taskBudget);
	const actualCost = normalizeTaskBudget(
		source.actual_cost ?? source.actualCost ?? source.spent_cost ?? source.spentCost
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
		budget,
		actual_cost: actualCost,
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
	return formatTimelineErrorMessage(payload, response.status);
}

function formatTimelineErrorMessage(
	payload: TimelineErrorResponse | null | undefined,
	status?: number
) {
	if (!payload) {
		return typeof status === 'number' ? `HTTP ${status}` : 'Request failed';
	}
	const primary = payload.error?.trim() || payload.message?.trim();
	const detail = payload.detail?.trim();
	const stage = payload.stage?.trim();
	const code = payload.code?.trim();
	const timeoutMs = typeof payload.timeout_ms === 'number' && payload.timeout_ms > 0 ? payload.timeout_ms : undefined;
	const promptTimeoutMs = typeof payload.prompt_timeout_ms === 'number' && payload.prompt_timeout_ms > 0 ? payload.prompt_timeout_ms : undefined;
	const parts: string[] = [];
	if (primary) {
		parts.push(primary);
	}
	if (stage) {
		parts.push(`Stage: ${stage}`);
	}
	if (code) {
		parts.push(`Code: ${code}`);
	}
	if (timeoutMs !== undefined) {
		parts.push(`Step budget: ${Math.round(timeoutMs / 1000)}s`);
	}
	if (promptTimeoutMs !== undefined) {
		parts.push(`Total budget: ${Math.round(promptTimeoutMs / 1000)}s`);
	}
	if (detail) {
		parts.push(detail);
	}
	if (parts.length > 0) {
		return parts.join('\n');
	}
	return typeof status === 'number' ? `HTTP ${status}` : 'Request failed';
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

function buildTimelineConversationHistoryPayload(
	history: AITimelineConversationMessage[] | null | undefined
) {
	if (!Array.isArray(history) || history.length === 0) {
		return [];
	}
	const normalized = history
		.filter((entry) => entry && typeof entry === 'object')
		.map((entry) => {
			const role = entry.role === 'assistant' ? 'assistant' : 'user';
			const text = toStringValue(entry.text).slice(0, 1800);
			const intent = toStringValue(entry.intent).toLowerCase();
			if (!text) {
				return null;
			}
			if (
				intent !== 'chat' &&
				intent !== 'modify_project' &&
				intent !== 'generate_project' &&
				intent !== 'clarify'
			) {
				return { role, text };
			}
			return { role, text, intent };
		})
		.filter(
			(entry): entry is { role: 'user' | 'assistant'; text: string; intent?: AITimelineIntent } =>
				Boolean(entry)
		);
	return normalized.slice(-40);
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
		budget: normalizeTaskBudget(source.budget ?? source.task_budget ?? source.taskBudget),
		actualCost: normalizeTaskBudget(
			source.actual_cost ??
				source.actualCost ??
				source.spent ??
				source.spent_cost ??
				source.spentCost
		),
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
			budget: 0,
			actualCost: 0,
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
			budget: 0,
			actualCost: 0,
			sprintStartDate: '',
			sprintEndDate: '',
			durationUnit: 'days',
			durationValue: 1
		};
	}

	let parsedType = 'general';
	let parsedEffortScore = 3;
	let parsedBudget = 0;
	let parsedActualCost = 0;
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
		if (key === 'budget') {
			const parsed = normalizeTaskBudget(rawValue);
			if (typeof parsed === 'number') {
				parsedBudget = parsed;
			}
			continue;
		}
		if (key === 'actual cost' || key === 'actual_cost' || key === 'spent' || key === 'cost') {
			const parsed = normalizeTaskBudget(rawValue);
			if (typeof parsed === 'number') {
				parsedActualCost = parsed;
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
		budget: parsedBudget,
		actualCost: parsedActualCost,
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
		const resolvedBudget =
			typeof taskRecord.budget === 'number' &&
			Number.isFinite(taskRecord.budget) &&
			taskRecord.budget > 0
				? taskRecord.budget
				: metadata.budget;
		const resolvedActualCost =
			typeof taskRecord.actualCost === 'number' &&
			Number.isFinite(taskRecord.actualCost) &&
			taskRecord.actualCost >= 0
				? taskRecord.actualCost
				: metadata.actualCost;
		existing.tasks.push({
			id: taskRecord.id,
			title: taskRecord.title,
			status: normalizeTaskStatus(taskRecord.status),
			effort_score: metadata.effortScore,
			budget: resolvedBudget > 0 ? resolvedBudget : undefined,
			actual_cost: resolvedActualCost >= 0 ? resolvedActualCost : undefined,
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

	const allTasks = sprints.flatMap((sprint) => sprint.tasks);
	const budgetTotal = allTasks.reduce(
		(sum, task) =>
			sum +
			(typeof task.budget === 'number' && Number.isFinite(task.budget) && task.budget > 0
				? task.budget
				: 0),
		0
	);
	const budgetSpent = allTasks.reduce(
		(sum, task) =>
			sum +
			(typeof task.actual_cost === 'number' &&
			Number.isFinite(task.actual_cost) &&
			task.actual_cost >= 0
				? task.actual_cost
				: 0),
		0
	);
	const hasAnySpentData = allTasks.some(
		(task) =>
			typeof task.actual_cost === 'number' &&
			Number.isFinite(task.actual_cost) &&
			task.actual_cost >= 0
	);

	return {
		project_name: fallbackProjectName,
		tech_stack: [],
		target_audience: '',
		estimated_cost: '',
		budget_total: budgetTotal > 0 ? budgetTotal : undefined,
		budget_spent: hasAnySpentData ? budgetSpent : undefined,
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

function applyProjectTimelineState(value: ProjectTimeline | null, preserveProjectNew = false) {
	if (!value) {
		projectTimeline.set(null);
		if (!preserveProjectNew) {
			isProjectNew.set(true);
		}
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
	if (!preserveProjectNew) {
		isProjectNew.set(false);
	}
}

function previewProjectTimeline(value: ProjectTimeline | null) {
	applyProjectTimelineState(value, true);
}

export function setProjectTimeline(value: ProjectTimeline | null) {
	applyProjectTimelineState(value, false);
}

function extractTimelinePayloadFromAIResponse(payload: unknown): unknown {
	const source = toRecord(payload);
	if (!source) {
		return payload;
	}

	const nestedCandidates = [
		source.timeline,
		source.project_timeline,
		source.projectTimeline,
		source.project
	];
	for (const candidate of nestedCandidates) {
		const candidateRecord = toRecord(candidate);
		if (candidateRecord && Array.isArray(candidateRecord.sprints)) {
			return candidate;
		}
	}
	if (Array.isArray(source.sprints)) {
		return source;
	}
	return payload;
}

function extractAssistantReplyFromAIResponse(payload: unknown) {
	const source = toRecord(payload);
	if (!source) {
		return '';
	}
	return toStringValue(source.assistant_reply ?? source.assistantReply);
}

function extractIntentFromAIResponse(payload: unknown): AITimelineIntent | '' {
	const source = toRecord(payload);
	if (!source) {
		return '';
	}
	const intent = toStringValue(source.intent).toLowerCase();
	if (
		intent === 'chat' ||
		intent === 'modify_project' ||
		intent === 'generate_project' ||
		intent === 'clarify'
	) {
		return intent;
	}
	return '';
}

export function applyTimelinePayload(payload: unknown) {
	const normalized = normalizeTimeline(extractTimelinePayloadFromAIResponse(payload));
	setProjectTimeline(normalized);
	timelineError.set('');
	return normalized;
}

function cloneTimelineState(timeline: ProjectTimeline) {
	if (typeof structuredClone === 'function') {
		return structuredClone(timeline);
	}
	return JSON.parse(JSON.stringify(timeline)) as ProjectTimeline;
}

function normalizeTimelineEditProjectPatch(payload: unknown): TimelineEditProjectPatch | null {
	const source = toRecord(payload);
	if (!source) {
		return null;
	}
	const techStackSource = Array.isArray(source.tech_stack ?? source.techStack)
		? ((source.tech_stack ?? source.techStack) as unknown[])
		: [];
	const techStack = techStackSource.map((entry: unknown) => toStringValue(entry)).filter(Boolean);
	const rolesNeededSource = Array.isArray(source.roles_needed ?? source.rolesNeeded)
		? ((source.roles_needed ?? source.rolesNeeded) as unknown[])
		: [];
	const rolesNeeded = rolesNeededSource
		.map((entry: unknown) => toStringValue(entry))
		.filter(Boolean);
	const patch: TimelineEditProjectPatch = {};
	const projectName = toStringValue(source.project_name ?? source.projectName);
	if (projectName) patch.project_name = projectName;
	if (techStack.length > 0) patch.tech_stack = techStack;
	const targetAudience = toStringValue(source.target_audience ?? source.targetAudience);
	if (targetAudience) patch.target_audience = targetAudience;
	const estimatedCost = toStringValue(source.estimated_cost ?? source.estimatedCost);
	if (estimatedCost) patch.estimated_cost = estimatedCost;
	if (rolesNeeded.length > 0) patch.roles_needed = rolesNeeded;
	return Object.keys(patch).length > 0 ? patch : null;
}

function normalizeTimelineEditOperationPayload(payload: unknown): TimelineEditOperation | null {
	const source = toRecord(payload);
	if (!source) {
		return null;
	}
	const durationSource = toRecord(source.duration);
	const op = toStringValue(source.op ?? source.action).toLowerCase();
	if (op !== 'add_task' && op !== 'update_task' && op !== 'delete_task') {
		return null;
	}
	const durationUnit = normalizeDurationUnit(
		source.duration_unit ?? source.durationUnit ?? durationSource?.unit
	);
	const durationValueRaw =
		source.duration_value ?? source.durationValue ?? durationSource?.value ?? undefined;
	const durationValue =
		durationValueRaw === undefined ? undefined : normalizeDurationValue(durationValueRaw, durationUnit);
	const normalized: TimelineEditOperation = {
		op,
		task_id: toStringValue(source.task_id ?? source.taskId),
		id: toStringValue(source.id),
		sprint_name: toStringValue(source.sprint_name ?? source.sprintName ?? source.sprint),
		title: toStringValue(source.title),
		status: toStringValue(source.status),
		task_type: toStringValue(source.task_type ?? source.taskType ?? source.type),
		assignee_id: toStringValue(source.assignee_id ?? source.assigneeId ?? source.assignee),
		description: toStringValue(source.description)
	};
	if (source.budget !== undefined) {
		normalized.budget = Math.max(0, toNumberValue(source.budget, 0));
	}
	if (source.actual_cost !== undefined || source.actualCost !== undefined || source.spent !== undefined) {
		normalized.actual_cost = Math.max(
			0,
			toNumberValue(source.actual_cost ?? source.actualCost ?? source.spent, 0)
		);
	}
	if (durationUnit) {
		normalized.duration_unit = durationUnit;
	}
	if (durationValue !== undefined) {
		normalized.duration_value = durationValue;
	}
	return normalized;
}

function ensureTimelineSprintState(project: ProjectTimeline, sprintName: string) {
	const normalizedTarget = sprintName.trim();
	if (!normalizedTarget) {
		if (project.sprints.length === 0) {
			project.sprints = [
				{
					id: 'sprint-1',
					name: 'Sprint 1',
					start_date: '',
					end_date: '',
					tasks: []
				}
			];
		}
		return 0;
	}
	const existingIndex = project.sprints.findIndex(
		(sprint) => sprint.name.trim().toLowerCase() === normalizedTarget.toLowerCase()
	);
	if (existingIndex >= 0) {
		return existingIndex;
	}
	project.sprints = [
		...project.sprints,
		{
			id: `sprint-${project.sprints.length + 1}`,
			name: normalizedTarget,
			start_date: '',
			end_date: '',
			tasks: []
		}
	];
	return project.sprints.length - 1;
}

function findTimelineTaskPosition(project: ProjectTimeline, taskId: string) {
	const normalizedTaskID = taskId.trim();
	if (!normalizedTaskID) {
		return { sprintIndex: -1, taskIndex: -1 };
	}
	for (let sprintIndex = 0; sprintIndex < project.sprints.length; sprintIndex += 1) {
		const taskIndex = project.sprints[sprintIndex]?.tasks.findIndex(
			(task) => task.id.trim() === normalizedTaskID
		);
		if ((taskIndex ?? -1) >= 0) {
			return { sprintIndex, taskIndex: taskIndex as number };
		}
	}
	return { sprintIndex: -1, taskIndex: -1 };
}

function applyTimelineProjectPatchToState(
	current: ProjectTimeline,
	patch: TimelineEditProjectPatch | null
) {
	if (!patch) {
		return current;
	}
	const next = cloneTimelineState(current);
	if (patch.project_name) next.project_name = patch.project_name;
	if (patch.tech_stack && patch.tech_stack.length > 0) next.tech_stack = patch.tech_stack;
	if (patch.target_audience) next.target_audience = patch.target_audience;
	if (patch.estimated_cost) next.estimated_cost = patch.estimated_cost;
	if (patch.roles_needed && patch.roles_needed.length > 0) next.roles_needed = patch.roles_needed;
	return normalizeTimeline(next);
}

function applyTimelineEditOperationToState(
	current: ProjectTimeline,
	operation: TimelineEditOperation | null
) {
	if (!operation) {
		return current;
	}
	const next = cloneTimelineState(current);
	switch (operation.op) {
		case 'delete_task': {
			const { sprintIndex, taskIndex } = findTimelineTaskPosition(
				next,
				operation.task_id || operation.id || ''
			);
			if (sprintIndex < 0 || taskIndex < 0) {
				return current;
			}
			next.sprints[sprintIndex].tasks.splice(taskIndex, 1);
			break;
		}
		case 'update_task': {
			const { sprintIndex, taskIndex } = findTimelineTaskPosition(
				next,
				operation.task_id || operation.id || ''
			);
			if (sprintIndex < 0 || taskIndex < 0) {
				return current;
			}
			const updatedTask = { ...next.sprints[sprintIndex].tasks[taskIndex] };
			if (operation.title) updatedTask.title = operation.title;
			if (operation.status) updatedTask.status = normalizeTaskStatus(operation.status);
			if (operation.task_type) updatedTask.type = operation.task_type;
			if (operation.assignee_id) updatedTask.assignee = operation.assignee_id;
			if (operation.budget !== undefined) updatedTask.budget = operation.budget;
			if (operation.actual_cost !== undefined) updatedTask.actual_cost = operation.actual_cost;
			if (operation.duration_unit) updatedTask.duration_unit = operation.duration_unit;
			if (operation.duration_value !== undefined) {
				const durationUnit = updatedTask.duration_unit ?? operation.duration_unit ?? 'days';
				updatedTask.duration_value = normalizeDurationValue(operation.duration_value, durationUnit);
			}
			if (operation.description) updatedTask.description = operation.description;
			next.sprints[sprintIndex].tasks[taskIndex] = updatedTask;

			if (operation.sprint_name) {
				const targetSprintIndex = ensureTimelineSprintState(next, operation.sprint_name);
				if (targetSprintIndex >= 0 && targetSprintIndex !== sprintIndex) {
					const [movedTask] = next.sprints[sprintIndex].tasks.splice(taskIndex, 1);
					if (movedTask) {
						next.sprints[targetSprintIndex].tasks = [...next.sprints[targetSprintIndex].tasks, movedTask];
					}
				}
			}
			break;
		}
		case 'add_task': {
			const targetSprintIndex = ensureTimelineSprintState(next, operation.sprint_name || '');
			const durationUnit = operation.duration_unit ?? 'days';
			const durationValue =
				operation.duration_value !== undefined
					? normalizeDurationValue(operation.duration_value, durationUnit)
					: durationUnit === 'hours'
						? 4
						: 1;
			const taskId =
				operation.task_id ||
				operation.id ||
				`ai-task-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
			next.sprints[targetSprintIndex].tasks = [
				...next.sprints[targetSprintIndex].tasks,
				{
					id: taskId,
					title: operation.title || 'New task',
					status: normalizeTaskStatus(operation.status || 'todo'),
					effort_score: 1,
					budget: operation.budget,
					actual_cost: operation.actual_cost,
					type: operation.task_type || 'general',
					assignee: operation.assignee_id,
					description: operation.description,
					duration_unit: durationUnit,
					duration_value: durationValue
				}
			];
			break;
		}
	}
	return normalizeTimeline(next);
}

function summarizeTimelineEditOperation(operation: TimelineEditOperation | null) {
	if (!operation) {
		return 'Applied board change.';
	}
	const title = operation.title?.trim();
	switch (operation.op) {
		case 'add_task':
			if (title && operation.sprint_name) return `Added "${title}" to ${operation.sprint_name}.`;
			if (title) return `Added "${title}".`;
			return 'Added a new task.';
		case 'update_task':
			if (title && operation.sprint_name) return `Updated "${title}" in ${operation.sprint_name}.`;
			if (title) return `Updated "${title}".`;
			return 'Updated a task.';
		case 'delete_task':
			if (title) return `Deleted "${title}".`;
			return 'Deleted a task.';
		default:
			return 'Applied board change.';
	}
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
				budget: task.budget,
				actual_cost: task.actual_cost,
				assignee: task.assignee,
				duration_value: task.duration_value,
				duration_unit: task.duration_unit
				// description omitted to save tokens
			}))
		}))
	};
}

function buildClientTimeContext() {
	const now = new Date();
	const timezone =
		typeof Intl !== 'undefined' ? Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC' : 'UTC';
	const offsetMinutes = -now.getTimezoneOffset();
	const offsetSign = offsetMinutes >= 0 ? '+' : '-';
	const absOffset = Math.abs(offsetMinutes);
	const offsetHours = String(Math.floor(absOffset / 60)).padStart(2, '0');
	const offsetMins = String(absOffset % 60).padStart(2, '0');
	const utcOffset = `${offsetSign}${offsetHours}:${offsetMins}`;
	const localDateTime = now
		.toLocaleString('en-US', {
			year: 'numeric',
			month: 'long',
			day: 'numeric',
			hour: '2-digit',
			minute: '2-digit',
			second: '2-digit',
			hour12: false
		})
		.replace(',', '');

	return [
		'CLIENT TIME CONTEXT (use this as the current reference for all scheduling):',
		`- current_time_utc: ${now.toISOString()}`,
		`- current_time_local: ${localDateTime}`,
		`- timezone: ${timezone}`,
		`- utc_offset: ${utcOffset}`,
		'- If explicit dates are not provided, generate dates relative to this current time context.'
	].join('\n');
}

export async function generateAITimeline(
	roomId: string,
	prompt: string,
	conversationHistory: AITimelineConversationMessage[] = []
): Promise<AITimelineResult> {
	const normalizedRoomID = roomId.trim();
	const normalizedPrompt = prompt.trim();
	const sessionUserID = (get(currentUser)?.id ?? '').trim();
	if (!normalizedRoomID) {
		throw new Error('roomId is required');
	}
	if (!normalizedPrompt) {
		throw new Error('prompt is required');
	}

	const enrichedPrompt = `${AI_TIMELINE_FORMAT_HINT}\n\n${buildClientTimeContext()}\n\nUSER REQUEST:\n${normalizedPrompt}`;
	const normalizedConversationHistory =
		buildTimelineConversationHistoryPayload(conversationHistory);
	const endpoint = `${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomID)}/ai-timeline`;
	const requestPayloadDEBUG_DELETE_LATER = {
		prompt: enrichedPrompt,
		userId: sessionUserID,
		conversation_history: normalizedConversationHistory
	};

	timelineLoading.set(true);
	timelineError.set('');
	const generateAbort = new AbortController();
	const generateTimeoutId = setTimeout(() => generateAbort.abort(), 15 * 60 * 1000); // 15-min hard cap
	try {
		const response = await fetch(endpoint, {
			method: 'POST',
			headers: withTimelineUserHeaders(sessionUserID, {
				'Content-Type': 'application/json'
			}),
			credentials: 'include',
			signal: generateAbort.signal,
			body: JSON.stringify(requestPayloadDEBUG_DELETE_LATER)
		});
		clearTimeout(generateTimeoutId);
		if (!response.ok) {
			throw new Error(await parseErrorMessage(response));
		}

		const payload = (await response.json().catch(() => null)) as unknown;
		const intent = extractIntentFromAIResponse(payload);
		const assistantReply = extractAssistantReplyFromAIResponse(payload);
		if (intent === 'chat' || intent === 'clarify') {
			lastAIAssistantReply.set(assistantReply);
			return {
				timeline: get(projectTimeline),
				assistantReply,
				intent
			};
		}

		const normalized = applyTimelinePayload(payload);
		lastAIAssistantReply.set(assistantReply);
		const boardActivityEvent = addBoardActivity({
			type: 'board_generated',
			title: 'Board generated by Tora AI',
			subtitle: normalized.project_name
		});
		sendSocketPayload(buildBoardActivitySocketPayload(normalizedRoomID, boardActivityEvent));
		return {
			timeline: normalized,
			assistantReply,
			intent: intent || 'generate_project'
		};
	} catch (error) {
		const message = error instanceof Error ? error.message : 'Failed to generate project timeline';
		timelineError.set(message);
		throw error instanceof Error ? error : new Error(message);
	} finally {
		timelineLoading.set(false);
	}
}

/**
 * streamAITimeline streams project generation from the /ai-timeline/stream SSE endpoint.
 * Sprint tasks arrive incrementally and are applied to the store as each sprint completes,
 * so the board populates in real-time rather than after the full generation finishes.
 */
export async function streamAITimeline(
	roomId: string,
	prompt: string,
	conversationHistory: AITimelineConversationMessage[] = [],
	callbacks: StreamAITimelineCallbacks = {},
	fitToTier = false
): Promise<AITimelineResult> {
	const normalizedRoomID = roomId.trim();
	const normalizedPrompt = prompt.trim();
	const sessionUserID = (get(currentUser)?.id ?? '').trim();
	if (!normalizedRoomID) throw new Error('roomId is required');
	if (!normalizedPrompt) throw new Error('prompt is required');

	const enrichedPrompt = `${AI_TIMELINE_FORMAT_HINT}\n\n${buildClientTimeContext()}\n\nUSER REQUEST:\n${normalizedPrompt}`;
	const normalizedConversationHistory = buildTimelineConversationHistoryPayload(conversationHistory);
	const endpoint = `${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomID)}/ai-timeline/stream`;
	const requestPayloadDEBUG_DELETE_LATER = {
		prompt: enrichedPrompt,
		userId: sessionUserID,
		conversation_history: normalizedConversationHistory,
		...(fitToTier ? { fit_to_tier: true } : {})
	};

	timelineLoading.set(true);
	timelineError.set('');

	// Accumulated state built from SSE events
	let blueprintMeta: {
		project_name: string;
		assistant_reply: string;
		tech_stack: string[];
		target_audience: string;
		estimated_cost: string;
		roles_needed: string[];
		sprint_shells: Array<{ id: string; name: string; start_date: string; end_date: string }>;
	} | null = null;
	// Sparse array indexed by sprint_index so out-of-order events work correctly
	const completedSprints: (Sprint | undefined)[] = [];

	function buildAccumulatedTimeline(isPartial: boolean, missingSprints: string[]): ProjectTimeline {
		const meta = blueprintMeta!;
		const derivedMissingSprints = meta.sprint_shells
			.filter((_shell, index) => !completedSprints[index])
			.map((shell) => shell.name)
			.filter(Boolean);
		const resolvedMissingSprints = Array.from(
			new Set((missingSprints.length > 0 ? missingSprints : isPartial ? derivedMissingSprints : []).filter(Boolean))
		);
		const timeline: ProjectTimeline = {
			project_name: meta.project_name || 'Project Timeline',
			tech_stack: meta.tech_stack,
			target_audience: meta.target_audience,
			estimated_cost: meta.estimated_cost,
			roles_needed: meta.roles_needed,
			is_partial: isPartial,
			missing_sprints: resolvedMissingSprints,
			total_progress: 0,
			sprints: meta.sprint_shells.map((shell, index) => {
				const completedSprint = completedSprints[index];
				if (completedSprint) {
					return completedSprint;
				}
				return {
					id: shell.id || `sprint-${index + 1}`,
					name: shell.name || `Sprint ${index + 1}`,
					start_date: shell.start_date || '',
					end_date: shell.end_date || '',
					tasks: []
				} satisfies Sprint;
			})
		};
		timeline.total_progress = calculateTotalProgress(timeline);
		return timeline;
	}

	const abortController = new AbortController();
	const requestState = {
		controller: abortController,
		kind: 'generate' as const,
		roomId: normalizedRoomID,
		stopRequested: false
	};
	let timedOut = false;
	const timeoutId = setTimeout(() => {
		timedOut = true;
		abortController.abort();
	}, 15 * 60 * 1000);

	try {
		setActiveTimelineRequest(requestState);

		const response = await fetch(endpoint, {
			method: 'POST',
			headers: withTimelineUserHeaders(sessionUserID, { 'Content-Type': 'application/json' }),
			credentials: 'include',
			signal: abortController.signal,
			body: JSON.stringify(requestPayloadDEBUG_DELETE_LATER)
		});

		if (!response.ok) {
			clearTimeout(timeoutId);
			throw new Error(await parseErrorMessage(response));
		}
		if (!response.body) {
			clearTimeout(timeoutId);
			throw new Error('Streaming not supported by server');
		}

		const reader = response.body.getReader();
		const decoder = new TextDecoder();
		let buffer = '';
		let currentEvent = '';
		let currentData = '';
		type SSEDonePayload = {
			is_partial?: boolean;
			missing_sprints?: unknown[];
			assistant_reply?: string;
		};
		let chatResult: AITimelineResult | null = null;
		let donePayload: SSEDonePayload | null = null;
		let streamError: string | null = null;

		try {
			while (true) {
				const { done, value } = await reader.read();
				if (done) break;
				buffer += decoder.decode(value, { stream: true });

				const lines = buffer.split('\n');
				buffer = lines.pop() ?? '';

				for (const line of lines) {
					if (line.startsWith('event: ')) {
						currentEvent = line.slice(7).trim();
					} else if (line.startsWith('data: ')) {
						currentData = line.slice(6).trim();
					} else if (line === '') {
						if (currentEvent && currentData) {
							try {
								const payload = JSON.parse(currentData) as Record<string, unknown>;
								recordProjectServerDataDEBUG_DELETE_LATER({
									source: 'ai_generate_stream',
									direction: 'stream',
									roomId: normalizedRoomID,
									endpoint,
									method: 'POST',
									status: response.status,
									event: currentEvent,
									payload
								});

								if (currentEvent === 'status') {
									callbacks.onStatus?.(
										String(payload.step ?? ''),
										String(payload.label ?? ''),
										typeof payload.sprint_index === 'number' ? payload.sprint_index : undefined,
										typeof payload.sprint_total === 'number' ? payload.sprint_total : undefined,
										{
											timeoutMs:
												typeof payload.timeout_ms === 'number'
													? payload.timeout_ms
													: undefined,
											promptTimeoutMs:
												typeof payload.prompt_timeout_ms === 'number'
													? payload.prompt_timeout_ms
													: undefined,
											strategy:
												typeof payload.strategy === 'string'
													? payload.strategy
													: undefined
										}
									);
								} else if (currentEvent === 'chat') {
									const intent = String(payload.intent ?? 'chat');
									const assistantReply = String(payload.assistant_reply ?? '');
									callbacks.onChat?.(intent, assistantReply);
									lastAIAssistantReply.set(assistantReply);
									chatResult = {
										timeline: get(projectTimeline),
										assistantReply,
										intent: intent as AITimelineIntent
									};
								} else if (currentEvent === 'blueprint') {
									blueprintMeta = {
										project_name: String(payload.project_name ?? 'Project Timeline'),
										assistant_reply: String(payload.assistant_reply ?? ''),
										tech_stack: Array.isArray(payload.tech_stack)
											? payload.tech_stack.map(String)
											: [],
										target_audience: String(payload.target_audience ?? ''),
										estimated_cost: String(payload.estimated_cost ?? ''),
										roles_needed: Array.isArray(payload.roles_needed)
											? payload.roles_needed.map(String)
											: [],
										sprint_shells: Array.isArray(payload.sprints)
											? payload.sprints.map((s: unknown) => {
													const sr = s as Record<string, unknown>;
													return {
														id: String(sr.id ?? ''),
														name: String(sr.name ?? ''),
														start_date: String(sr.start_date ?? ''),
														end_date: String(sr.end_date ?? '')
													};
												})
											: []
									};
									callbacks.onBlueprint?.({
										projectName: blueprintMeta.project_name,
										assistantReply: blueprintMeta.assistant_reply,
										sprintNames: blueprintMeta.sprint_shells
											.map((shell) => shell.name)
											.filter(Boolean)
									});
									previewProjectTimeline(buildAccumulatedTimeline(true, []));
								} else if (currentEvent === 'sprint_tasks') {
									if (blueprintMeta) {
										const sprintIndex =
											typeof payload.sprint_index === 'number' ? payload.sprint_index : 0;
										const sprintName = String(
											payload.sprint_name ?? `Sprint ${sprintIndex + 1}`
										);
										const shell = blueprintMeta.sprint_shells[sprintIndex];
										const sprint: Sprint = {
											id:
												String(payload.sprint_id ?? shell?.id ?? '') ||
												`sprint-${sprintIndex + 1}`,
											name: sprintName,
											start_date: String(payload.start_date ?? shell?.start_date ?? ''),
											end_date: String(payload.end_date ?? shell?.end_date ?? ''),
											tasks: (Array.isArray(payload.tasks) ? payload.tasks : [])
												.map((t, i) =>
													normalizeTask(t, `task-${sprintIndex + 1}-${i + 1}`)
												)
												.filter((t): t is TimelineTask => Boolean(t))
										};
										completedSprints[sprintIndex] = sprint;
										// Incrementally update the board as each sprint arrives.
										previewProjectTimeline(buildAccumulatedTimeline(true, []));
										callbacks.onSprint?.({
											sprintIndex,
											sprintTotal: blueprintMeta.sprint_shells.length,
											sprintName,
											taskCount: sprint.tasks.length,
											taskTitles: sprint.tasks
												.map((task) => task.title)
												.filter(Boolean)
										});
									}
								} else if (currentEvent === 'done') {
									donePayload = payload as SSEDonePayload;
								} else if (currentEvent === 'error') {
									streamError = formatTimelineErrorMessage(
										payload as TimelineErrorResponse,
										response.status
									);
									callbacks.onError?.({
										message: streamError,
										isStopped: false
									});
								}
							} catch {
								// ignore malformed SSE frames
							}
						}
						currentEvent = '';
						currentData = '';
					}
				}
			}
		} finally {
			reader.releaseLock();
		}

		// Chat/clarify short-circuit
		if (chatResult) {
			return chatResult;
		}

		if (streamError && !blueprintMeta) {
			throw new Error(streamError);
		}

		// Apply final (possibly partial) timeline
		const assistantReply = (donePayload?.assistant_reply ?? '') || blueprintMeta?.assistant_reply || '';
		const missingSprintsFromDone = Array.isArray(donePayload?.missing_sprints)
			? donePayload!.missing_sprints!.map(String).filter(Boolean)
			: [];

		if (blueprintMeta) {
			const inferredMissingSprints = blueprintMeta.sprint_shells
				.filter((_shell, index) => !completedSprints[index])
				.map((shell) => shell.name)
				.filter(Boolean);
			const missingSprints =
				missingSprintsFromDone.length > 0 ? missingSprintsFromDone : inferredMissingSprints;
			const isPartial =
				Boolean(donePayload?.is_partial) || Boolean(streamError) || missingSprints.length > 0;
			const finalTimeline = buildAccumulatedTimeline(isPartial, missingSprints);
			previewProjectTimeline(finalTimeline);
			timelineError.set('');
			if (streamError) {
				timelineError.set(streamError);
			}
			lastAIAssistantReply.set(assistantReply);
			callbacks.onDone?.({
				assistantReply,
				isPartial: finalTimeline.is_partial === true,
				missingSprints: finalTimeline.missing_sprints ?? []
			});
			const boardActivityEvent = addBoardActivity({
				type: 'board_generated',
				title: 'Board generated by Tora AI',
				subtitle: finalTimeline.project_name
			});
			sendSocketPayload(buildBoardActivitySocketPayload(normalizedRoomID, boardActivityEvent));
			return {
				timeline: finalTimeline,
				assistantReply,
				intent: 'generate_project'
			};
		}

		if (streamError) throw new Error(streamError);
		throw new Error('AI did not return a timeline. Please provide more detail and try again.');
	} catch (error) {
		const userStopped = requestState.stopRequested && isAbortError(error);
		if (userStopped) {
			let partialTimeline: ProjectTimeline | null = null;
			const assistantReply = blueprintMeta?.assistant_reply || '';
			if (blueprintMeta) {
				const missingSprints = blueprintMeta.sprint_shells
					.filter((_shell, index) => !completedSprints[index])
					.map((shell) => shell.name)
					.filter(Boolean);
				partialTimeline = buildAccumulatedTimeline(true, missingSprints);
				previewProjectTimeline(partialTimeline);
			}
			timelineError.set('');
			callbacks.onError?.({
				message: partialTimeline
					? 'AI generation stopped. Partial workspace kept.'
					: 'AI generation stopped.',
				isStopped: true
			});
			throw new TimelineRequestStoppedError(
				partialTimeline
					? 'AI generation stopped. Partial workspace kept.'
					: 'AI generation stopped.',
				partialTimeline,
				assistantReply
			);
		}
		if (timedOut && isAbortError(error)) {
			const message = 'AI timeline request timed out';
			timelineError.set(message);
			callbacks.onError?.({
				message,
				isStopped: false
			});
			throw new Error(message);
		}
		const message =
			error instanceof Error ? error.message : 'Failed to generate project timeline';
		timelineError.set(message);
		callbacks.onError?.({
			message,
			isStopped: false
		});
		throw error instanceof Error ? error : new Error(message);
	} finally {
		clearTimeout(timeoutId);
		if (activeTimelineRequestState === requestState) {
			setActiveTimelineRequest(null);
		}
		timelineLoading.set(false);
	}
}

export async function editAITimeline(
	roomId: string,
	prompt: string,
	currentState: ProjectTimeline | null,
	conversationHistory: AITimelineConversationMessage[] = [],
	callbacks: StreamAIEditTimelineCallbacks = {}
): Promise<AITimelineResult> {
	const normalizedRoomID = roomId.trim();
	const normalizedPrompt = prompt.trim();
	const sessionUserID = (get(currentUser)?.id ?? '').trim();
	if (!normalizedRoomID) {
		throw new Error('roomId is required');
	}
	if (!normalizedPrompt) {
		throw new Error('prompt is required');
	}
	const initialTimeline = currentState ?? get(projectTimeline);
	if (!initialTimeline) {
		throw new Error('current_state is required');
	}

	const enrichedEditPrompt = `${buildClientTimeContext()}\n\nEDIT REQUEST:\n${normalizedPrompt}`;
	const normalizedConversationHistory =
		buildTimelineConversationHistoryPayload(conversationHistory);
	const endpoint = `${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomID)}/ai-edit/stream`;
	const requestPayloadDEBUG_DELETE_LATER = {
		prompt: enrichedEditPrompt,
		current_state: compressTimelineForAI(initialTimeline),
		userId: sessionUserID,
		conversation_history: normalizedConversationHistory
	};

	timelineLoading.set(true);
	timelineError.set('');
	const editAbort = new AbortController();
	const requestState = {
		controller: editAbort,
		kind: 'edit' as const,
		roomId: normalizedRoomID,
		stopRequested: false
	};
	let timedOut = false;
	const editTimeoutId = setTimeout(() => {
		timedOut = true;
		editAbort.abort();
	}, 15 * 60 * 1000);
	let workingTimeline = cloneTimelineState(initialTimeline);
	let planAssistantReply = '';
	let sawStateChange = false;
	try {
		setActiveTimelineRequest(requestState);
		const response = await fetch(endpoint, {
			method: 'POST',
			headers: withTimelineUserHeaders(sessionUserID, {
				'Content-Type': 'application/json'
			}),
			credentials: 'include',
			signal: editAbort.signal,
			body: JSON.stringify(requestPayloadDEBUG_DELETE_LATER)
		});
		if (!response.ok) {
			throw new Error(await parseErrorMessage(response));
		}
		if (!response.body) {
			throw new Error('Streaming not supported by server');
		}

		const reader = response.body.getReader();
		const decoder = new TextDecoder();
		let buffer = '';
		let currentEvent = '';
		let currentData = '';
		let chatResult: AITimelineResult | null = null;
		let donePayload: Record<string, unknown> | null = null;
		let streamError: string | null = null;
		let appliedCount = 0;
		let operationTotal = 0;

		try {
			while (true) {
				const { done, value } = await reader.read();
				if (done) break;
				buffer += decoder.decode(value, { stream: true });

				const lines = buffer.split('\n');
				buffer = lines.pop() ?? '';

				for (const line of lines) {
					if (line.startsWith('event: ')) {
						currentEvent = line.slice(7).trim();
					} else if (line.startsWith('data: ')) {
						currentData = line.slice(6).trim();
					} else if (line === '') {
						if (currentEvent && currentData) {
							try {
								const payload = JSON.parse(currentData) as Record<string, unknown>;
								recordProjectServerDataDEBUG_DELETE_LATER({
									source: 'ai_edit_stream',
									direction: 'stream',
									roomId: normalizedRoomID,
									endpoint,
									method: 'POST',
									status: response.status,
									event: currentEvent,
									payload
								});

								if (currentEvent === 'status') {
									const nextAppliedCount = toNumberValue(
										payload.applied_count ?? payload.appliedCount,
										appliedCount
									);
									const nextOperationTotal = toNumberValue(
										payload.operation_total ?? payload.operationTotal,
										operationTotal
									);
									appliedCount = Math.max(appliedCount, nextAppliedCount);
									operationTotal = Math.max(operationTotal, nextOperationTotal);
									callbacks.onStatus?.(
										String(payload.step ?? ''),
										String(payload.label ?? ''),
										appliedCount,
										operationTotal,
										{
											timeoutMs:
												typeof payload.timeout_ms === 'number'
													? payload.timeout_ms
													: undefined,
											promptTimeoutMs:
												typeof payload.prompt_timeout_ms === 'number'
													? payload.prompt_timeout_ms
													: undefined,
											strategy:
												typeof payload.strategy === 'string'
													? payload.strategy
													: undefined
										}
									);
								} else if (currentEvent === 'chat') {
									const intent = String(payload.intent ?? 'chat') as AITimelineIntent;
									const assistantReply = String(payload.assistant_reply ?? '');
									callbacks.onChat?.(intent, assistantReply);
									lastAIAssistantReply.set(assistantReply);
									chatResult = {
										timeline: initialTimeline,
										assistantReply,
										intent
									};
								} else if (currentEvent === 'plan') {
									planAssistantReply = String(payload.assistant_reply ?? '').trim();
									operationTotal = Math.max(
										operationTotal,
										toNumberValue(payload.operation_total ?? payload.operationTotal, operationTotal)
									);
									const projectPatch = normalizeTimelineEditProjectPatch(
										payload.project_patch ?? payload.projectPatch
									);
									if (projectPatch) {
										workingTimeline = applyTimelineProjectPatchToState(workingTimeline, projectPatch);
										setProjectTimeline(workingTimeline);
										sawStateChange = true;
									}
									callbacks.onPlan?.(planAssistantReply, operationTotal);
								} else if (currentEvent === 'operation_applied') {
									appliedCount = Math.max(
										appliedCount,
										toNumberValue(payload.applied_count ?? payload.appliedCount, appliedCount + 1)
									);
									operationTotal = Math.max(
										operationTotal,
										toNumberValue(payload.operation_total ?? payload.operationTotal, operationTotal)
									);
									const operation = normalizeTimelineEditOperationPayload(payload.operation);
									workingTimeline = applyTimelineEditOperationToState(workingTimeline, operation);
									setProjectTimeline(workingTimeline);
									sawStateChange = true;
									callbacks.onOperation?.(
										summarizeTimelineEditOperation(operation),
										appliedCount,
										operationTotal
									);
								} else if (currentEvent === 'progress') {
									// Heartbeat during LLM generation — update active step label only.
									callbacks.onProgress?.(
										String(payload.step ?? ''),
										String(payload.label ?? '')
									);
								} else if (currentEvent === 'text_delta') {
									// Streaming assistant reply chunk.
									callbacks.onTextDelta?.(String(payload.delta ?? ''));
								} else if (currentEvent === 'done') {
									donePayload = payload;
								} else if (currentEvent === 'error') {
									streamError = formatTimelineErrorMessage(
										payload as TimelineErrorResponse,
										response.status
									);
									callbacks.onError?.(streamError, { isStopped: false });
								}
							} catch {
								// ignore malformed SSE frames
							}
						}
						currentEvent = '';
						currentData = '';
					}
				}
			}
		} finally {
			reader.releaseLock();
		}

		if (chatResult) {
			return chatResult;
		}

		if (donePayload?.timeline) {
			workingTimeline = applyTimelinePayload(donePayload.timeline);
			sawStateChange = true;
		} else if (sawStateChange) {
			setProjectTimeline(workingTimeline);
		}

		if (streamError && !sawStateChange) {
			throw new Error(streamError);
		}
		if (!sawStateChange && !donePayload) {
			throw new Error('AI did not return any board changes. Please try again.');
		}

		if (streamError) {
			timelineError.set(streamError);
		} else {
			timelineError.set('');
		}

		const assistantReply =
			extractAssistantReplyFromAIResponse(donePayload) || planAssistantReply || 'Board updated.';
		lastAIAssistantReply.set(assistantReply);
		const finalTimeline = workingTimeline ?? get(projectTimeline);
		if (!finalTimeline) {
			throw new Error('AI edit finished without an active board timeline.');
		}
		const boardActivityEvent = addBoardActivity({
			type: 'board_edited',
			title: 'Board updated by Tora AI',
			subtitle: finalTimeline.project_name
		});
		sendSocketPayload(buildBoardActivitySocketPayload(normalizedRoomID, boardActivityEvent));
		return {
			timeline: finalTimeline,
			assistantReply,
			intent: extractIntentFromAIResponse(donePayload) || 'modify_project'
		};
	} catch (error) {
		const userStopped = requestState.stopRequested && isAbortError(error);
		if (userStopped) {
			if (sawStateChange) {
				setProjectTimeline(workingTimeline);
			}
			timelineError.set('');
			callbacks.onError?.(
				sawStateChange
					? 'AI board update stopped. Partial changes were kept.'
					: 'AI board update stopped.',
				{ isStopped: true }
			);
			throw new TimelineRequestStoppedError(
				sawStateChange
					? 'AI board update stopped. Partial changes were kept.'
					: 'AI board update stopped.',
				workingTimeline,
				planAssistantReply
			);
		}
		if (timedOut && isAbortError(error)) {
			const message = 'AI timeline edit request timed out';
			timelineError.set(message);
			callbacks.onError?.(message, { isStopped: false });
			throw new Error(message);
		}
		const message = error instanceof Error ? error.message : 'Failed to edit project timeline';
		timelineError.set(message);
		callbacks.onError?.(message, { isStopped: false });
		throw error instanceof Error ? error : new Error(message);
	} finally {
		clearTimeout(editTimeoutId);
		if (activeTimelineRequestState === requestState) {
			setActiveTimelineRequest(null);
		}
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
	const endpoint = `${apiBase}/api/rooms/${encodeURIComponent(normalizedRoomID)}/tasks`;

	try {
		const response = await fetchImpl(endpoint, {
			method: 'GET',
			credentials: 'include'
		});
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
