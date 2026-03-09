import { get, writable } from 'svelte/store';
import type { ProjectTimeline, Sprint, TimelineTask, TimelineTaskStatus } from '$lib/types/timeline';
import { currentUser } from '$lib/store';

const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';

type TimelineErrorResponse = {
	error?: string;
	message?: string;
};

export const projectTimeline = writable<ProjectTimeline | null>(null);
export const timelineLoading = writable(false);
export const timelineError = writable('');

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
		return;
	}
	const nextValue: ProjectTimeline = {
		...value,
		total_progress: calculateTotalProgress(value)
	};
	projectTimeline.set(nextValue);
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
