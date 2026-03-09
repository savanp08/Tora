import { get, writable } from 'svelte/store';
import type { ProjectTimeline, Sprint, TimelineTask, TimelineTaskStatus } from '$lib/types/timeline';
import { currentUser } from '$lib/store';
import { normalizeRoomIDValue } from '$lib/utils/chat/core';

const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';

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
	createdAt: number;
	updatedAt: number;
};

type ParsedTaskMetadata = {
	cleanDescription: string;
	type: string;
	effortScore: number;
	sprintStartDate: string;
	sprintEndDate: string;
};

type TimelineSprintAccumulator = {
	name: string;
	startDate: string;
	endDate: string;
	earliestCreatedAt: number;
	tasks: TimelineTask[];
};

export type ProjectTab = 'overview' | 'tasks' | 'progress' | 'visualizations' | 'tora_ai';

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

	return {
		id: taskID,
		title,
		status: normalizeTaskStatus(source.status),
		effort_score: effort,
		type: toStringValue(source.type) || 'general',
		description: toStringValue(source.description) || undefined
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

	return {
		id: toStringValue(source.id) || `sprint-${sprintIndex + 1}`,
		name: sprintName,
		start_date: toStringValue(source.start_date),
		end_date: toStringValue(source.end_date),
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
	const normalized: ProjectTimeline = {
		project_name: projectName,
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
			sprintEndDate: ''
		};
	}

	const metadataMatch = trimmed.match(/\[([^\]]+)\]\s*$/);
	if (!metadataMatch) {
		return {
			cleanDescription: trimmed,
			type: 'general',
			effortScore: 3,
			sprintStartDate: '',
			sprintEndDate: ''
		};
	}

	let parsedType = 'general';
	let parsedEffortScore = 3;
	let sprintStartDate = '';
	let sprintEndDate = '';
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
		}
	}

	return {
		cleanDescription: trimmed.slice(0, metadataMatch.index).trim(),
		type: parsedType,
		effortScore: parsedEffortScore,
		sprintStartDate,
		sprintEndDate
	};
}

function createSprintId(seed: string, index: number) {
	const normalized = seed.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
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
			description: metadata.cleanDescription || undefined
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
		total_progress: 0,
		sprints
	};
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
	const nextValue: ProjectTimeline = {
		...value,
		total_progress: calculateTotalProgress(value)
	};
	projectTimeline.set(nextValue);
	isProjectNew.set(false);
}

export async function generateAITimeline(roomId: string, prompt: string) {
	const normalizedRoomID = roomId.trim();
	const normalizedPrompt = prompt.trim();
	if (!normalizedRoomID) {
		throw new Error('roomId is required');
	}
	if (!normalizedPrompt) {
		throw new Error('prompt is required');
	}

	timelineLoading.set(true);
	timelineError.set('');
	try {
		const response = await fetch(`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomID)}/ai-timeline`, {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			credentials: 'include',
			body: JSON.stringify({
				prompt: normalizedPrompt,
				userId: get(currentUser)?.id ?? ''
			})
		});
		if (!response.ok) {
			throw new Error(await parseErrorMessage(response));
		}

		const payload = (await response.json().catch(() => null)) as unknown;
		const normalized = normalizeTimeline(payload);
		setProjectTimeline(normalized);
		return normalized;
	} catch (error) {
		const message = error instanceof Error ? error.message : 'Failed to generate project timeline';
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
