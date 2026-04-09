import { browser } from '$app/environment';
import { derived, readable } from 'svelte/store';
import { taskStore, type Task } from '$lib/stores/tasks';

const MINUTE_MS = 60_000;
const HOUR_MS = 60 * MINUTE_MS;
const DAY_MS = 24 * HOUR_MS;

export type TaskAdvisorySeverity = 'critical' | 'warning' | 'info';
export type TaskAdvisoryKind =
	| 'dependency'
	| 'schedule'
	| 'upcoming'
	| 'checklist'
	| 'downstream';

export type TaskAdvisory = {
	id: string;
	taskId: string;
	taskTitle: string;
	severity: TaskAdvisorySeverity;
	kind: TaskAdvisoryKind;
	headline: string;
	summary: string;
	suggestion: string;
	risk: string;
	relatedTaskIds: string[];
	dueAt?: number;
};

type AdvisoryMap = Record<string, TaskAdvisory[]>;

type AdvisoryCounts = {
	total: number;
	critical: number;
	warning: number;
	info: number;
};

const advisoryClock = readable(Date.now(), (set) => {
	if (!browser) {
		return undefined;
	}
	set(Date.now());
	const timer = window.setInterval(() => {
		set(Date.now());
	}, MINUTE_MS);
	return () => window.clearInterval(timer);
});

export const taskAdvisories = derived([taskStore, advisoryClock], ([$taskStore, $clock]) =>
	buildTaskAdvisories($taskStore, $clock)
);

export const taskAdvisoriesByTaskId = derived(taskAdvisories, ($taskAdvisories): AdvisoryMap => {
	const next: AdvisoryMap = {};
	for (const advisory of $taskAdvisories) {
		if (!next[advisory.taskId]) {
			next[advisory.taskId] = [];
		}
		next[advisory.taskId].push(advisory);
	}
	return next;
});

export const taskAdvisoryCounts = derived(taskAdvisories, ($taskAdvisories): AdvisoryCounts => {
	const counts: AdvisoryCounts = {
		total: $taskAdvisories.length,
		critical: 0,
		warning: 0,
		info: 0
	};
	for (const advisory of $taskAdvisories) {
		counts[advisory.severity] += 1;
	}
	return counts;
});

export function severityLabel(severity: TaskAdvisorySeverity) {
	if (severity === 'critical') return 'Critical';
	if (severity === 'warning') return 'Warning';
	return 'Heads-up';
}

export function formatTaskAdvisoryTimeLabel(timestamp: number, now = Date.now()) {
	if (!Number.isFinite(timestamp)) {
		return '';
	}
	const diff = timestamp - now;
	if (Math.abs(diff) < MINUTE_MS) {
		return 'now';
	}
	const prefix = diff < 0 ? '' : 'in ';
	const suffix = diff < 0 ? ' ago' : '';
	const absDiff = Math.abs(diff);
	return `${prefix}${formatDurationCompact(absDiff)}${suffix}`;
}

function isOpenTask(candidate: Task | undefined): candidate is Task {
	return candidate !== undefined && !isTaskDone(candidate);
}

function buildTaskAdvisories(tasks: Task[], now: number) {
	if (!Array.isArray(tasks) || tasks.length === 0) {
		return [] as TaskAdvisory[];
	}

	const taskById = new Map<string, Task>();
	for (const task of tasks) {
		const taskId = task.id.trim();
		if (!taskId) {
			continue;
		}
		taskById.set(taskId, task);
	}

	const advisories: TaskAdvisory[] = [];
	for (const task of tasks) {
		if (!task || isTaskDone(task)) {
			continue;
		}

		const incompleteDependencies = task.blockedBy
			.map((taskId) => taskById.get(taskId))
			.filter(isOpenTask);
		const waitingTasks = task.blocks
			.map((taskId) => taskById.get(taskId))
			.filter(isOpenTask);
		const incompleteSubtasks = (task.subtasks ?? []).filter((subtask) => !subtask.completed);
		const progressRatio = completionRatio(task);
		const estimatedRemainingMs = estimateRemainingWorkMs(
			task,
			progressRatio,
			incompleteDependencies.length,
			incompleteSubtasks.length
		);
		const dueAt = finiteTimestamp(task.dueDate);
		const startAt = finiteTimestamp(task.startDate);
		const taskLabel = task.title.trim() || 'Untitled task';

		if (incompleteDependencies.length > 0) {
			const dependencyNames = summarizeTaskTitles(incompleteDependencies);
			advisories.push({
				id: `${task.id}:dependency`,
				taskId: task.id,
				taskTitle: taskLabel,
				severity: task.status === 'in_progress' ? 'critical' : 'warning',
				kind: 'dependency',
				headline:
					incompleteDependencies.length === 1
						? `Finish ${dependencyNames} before pushing this`
						: `${incompleteDependencies.length} prerequisites still need attention`,
				summary: `${taskLabel} depends on ${dependencyNames}.`,
				suggestion: 'Complete those prerequisite tasks first or split out prep work before continuing.',
				risk: 'Starting without the required inputs can create rework, missing context, or blocked handoffs.',
				relatedTaskIds: incompleteDependencies.map((dependencyTask) => dependencyTask.id),
				dueAt
			});
		}

		if (typeof dueAt === 'number') {
			const timeLeftMs = dueAt - now;
			if (timeLeftMs < 0) {
				advisories.push({
					id: `${task.id}:overdue`,
					taskId: task.id,
					taskTitle: taskLabel,
					severity: 'critical',
					kind: 'schedule',
					headline: `Overdue by ${formatDurationCompact(Math.abs(timeLeftMs))}`,
					summary: `${taskLabel} missed its due date and still has work remaining.`,
					suggestion:
						incompleteDependencies.length > 0
							? 'Clear blockers first, then replan the remaining work with a new delivery date.'
							: 'Replan the remaining work now or cut scope before the delay spreads.',
					risk: 'Overdue work tends to pull attention from the next planned tasks and can cascade across the board.',
					relatedTaskIds: incompleteDependencies.map((dependencyTask) => dependencyTask.id),
					dueAt
				});
			} else {
				if (timeLeftMs < estimatedRemainingMs) {
					advisories.push({
						id: `${task.id}:schedule-risk`,
						taskId: task.id,
						taskTitle: taskLabel,
						severity: timeLeftMs <= 12 * HOUR_MS ? 'critical' : 'warning',
						kind: 'schedule',
						headline: `Only ${formatDurationCompact(timeLeftMs)} left, likely ${formatDurationCompact(estimatedRemainingMs)} needed`,
						summary: `${taskLabel} is tracking behind the remaining delivery window.`,
						suggestion:
							incompleteDependencies.length > 0
								? 'Finish prerequisites, reduce scope, or add help now so the remaining work can still land on time.'
								: 'Reduce scope, add help, or reschedule now before the deadline becomes an emergency.',
						risk: 'The current plan likely overruns the available time, which can spill into the next scheduled tasks.',
						relatedTaskIds: incompleteDependencies.map((dependencyTask) => dependencyTask.id),
						dueAt
					});
				} else if (timeLeftMs <= 36 * HOUR_MS) {
					advisories.push({
						id: `${task.id}:due-soon`,
						taskId: task.id,
						taskTitle: taskLabel,
						severity: progressRatio < 0.45 ? 'warning' : 'info',
						kind: 'upcoming',
						headline: `Due ${formatTaskAdvisoryTimeLabel(dueAt, now)}`,
						summary: `${taskLabel} is entering its final delivery window.`,
						suggestion: 'Confirm owner availability, acceptance steps, and any last dependencies before the final push.',
						risk: 'Late surprises this close to the due date can turn into an immediate slip.',
						relatedTaskIds: [],
						dueAt
					});
				}
			}
		}

		if (typeof startAt === 'number' && startAt > now && startAt - now <= 24 * HOUR_MS) {
			advisories.push({
				id: `${task.id}:starts-soon`,
				taskId: task.id,
				taskTitle: taskLabel,
				severity: incompleteDependencies.length > 0 ? 'warning' : 'info',
				kind: 'upcoming',
				headline: `Starts ${formatTaskAdvisoryTimeLabel(startAt, now)}`,
				summary: `${taskLabel} is scheduled to start soon.`,
				suggestion:
					incompleteDependencies.length > 0
						? 'Clear the prerequisites before kickoff so the assignee can start cleanly.'
						: 'Prep notes, owner context, and required files before the start window opens.',
				risk: 'A cold start burns into the available delivery window immediately.',
				relatedTaskIds: incompleteDependencies.map((dependencyTask) => dependencyTask.id),
				dueAt
			});
		}

		if (task.status === 'in_progress' && incompleteSubtasks.length >= 2) {
			advisories.push({
				id: `${task.id}:checklist`,
				taskId: task.id,
				taskTitle: taskLabel,
				severity: dueAt && dueAt-now <= 24 * HOUR_MS ? 'warning' : 'info',
				kind: 'checklist',
				headline: `${incompleteSubtasks.length} checklist item${incompleteSubtasks.length === 1 ? '' : 's'} still open`,
				summary: `${taskLabel} still has unfinished completion steps.`,
				suggestion: 'Use the remaining checklist to tighten execution before marking the task done.',
				risk: 'Skipping those last steps often creates QA churn, reopens, or partial handoffs.',
				relatedTaskIds: [],
				dueAt
			});
		}

		if (waitingTasks.length > 0) {
			const downstreamNames = summarizeTaskTitles(waitingTasks);
			const downstreamDueSoon = waitingTasks.some((waitingTask) => {
				const relatedDueAt = finiteTimestamp(waitingTask.dueDate);
				return typeof relatedDueAt === 'number' && relatedDueAt - now <= 2 * DAY_MS;
			});
			advisories.push({
				id: `${task.id}:downstream`,
				taskId: task.id,
				taskTitle: taskLabel,
				severity: downstreamDueSoon ? 'warning' : 'info',
				kind: 'downstream',
				headline:
					waitingTasks.length === 1
						? `${downstreamNames} is waiting on this`
						: `${waitingTasks.length} follow-on tasks are waiting`,
				summary: `${taskLabel} is a dependency for ${downstreamNames}.`,
				suggestion: 'If you want the next workstream to move, prioritize this unblocker next.',
				risk: 'Delay here can stall downstream tasks and compress the rest of the schedule.',
				relatedTaskIds: waitingTasks.map((waitingTask) => waitingTask.id),
				dueAt
			});
		}
	}

	return advisories.sort(compareAdvisories);
}

function compareAdvisories(left: TaskAdvisory, right: TaskAdvisory) {
	const severityDiff = severityRank(right.severity) - severityRank(left.severity);
	if (severityDiff !== 0) {
		return severityDiff;
	}
	const leftDueAt = Number.isFinite(left.dueAt) ? left.dueAt ?? Number.MAX_SAFE_INTEGER : Number.MAX_SAFE_INTEGER;
	const rightDueAt = Number.isFinite(right.dueAt) ? right.dueAt ?? Number.MAX_SAFE_INTEGER : Number.MAX_SAFE_INTEGER;
	if (leftDueAt !== rightDueAt) {
		return leftDueAt - rightDueAt;
	}
	return left.taskTitle.localeCompare(right.taskTitle, undefined, { sensitivity: 'base' });
}

function severityRank(severity: TaskAdvisorySeverity) {
	if (severity === 'critical') return 3;
	if (severity === 'warning') return 2;
	return 1;
}

function completionRatio(task: Task) {
	if (typeof task.completionPercent === 'number' && Number.isFinite(task.completionPercent)) {
		return clamp(task.completionPercent / 100, 0, 1);
	}
	const subtasks = Array.isArray(task.subtasks) ? task.subtasks : [];
	if (subtasks.length > 0) {
		const completedCount = subtasks.filter((subtask) => subtask.completed).length;
		return clamp(completedCount / subtasks.length, 0, 1);
	}
	if (isTaskDone(task)) {
		return 1;
	}
	if (task.status === 'in_progress') {
		return 0.45;
	}
	return 0.15;
}

function estimateRemainingWorkMs(
	task: Task,
	progressRatio: number,
	incompleteDependencyCount: number,
	incompleteSubtaskCount: number
) {
	let estimateMs = task.status === 'in_progress' ? 6 * HOUR_MS : 12 * HOUR_MS;
	estimateMs += incompleteDependencyCount * 90 * MINUTE_MS;
	estimateMs += incompleteSubtaskCount * 2 * HOUR_MS;
	if (task.roles?.length && task.roles.length > 1) {
		estimateMs += (task.roles.length - 1) * 45 * MINUTE_MS;
	}

	const startAt = finiteTimestamp(task.startDate);
	const dueAt = finiteTimestamp(task.dueDate);
	if (typeof startAt === 'number' && typeof dueAt === 'number' && dueAt > startAt) {
		const plannedWindowMs = dueAt - startAt;
		const windowBasedRemainingMs = plannedWindowMs * Math.max(0.15, 1 - clamp(progressRatio, 0, 1));
		estimateMs = Math.max(estimateMs, windowBasedRemainingMs);
	}

	return Math.max(2 * HOUR_MS, estimateMs);
}

function summarizeTaskTitles(tasks: Task[], limit = 2) {
	const labels = tasks
		.map((task) => task.title.trim())
		.filter(Boolean);
	if (labels.length === 0) {
		return 'other tasks';
	}
	if (labels.length <= limit) {
		return labels.join(labels.length === 2 ? ' and ' : ', ');
	}
	return `${labels.slice(0, limit).join(', ')} and ${labels.length - limit} more`;
}

function finiteTimestamp(value: number | undefined) {
	return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : undefined;
}

function isTaskDone(task: Task) {
	return task.status.trim().toLowerCase() === 'done';
}

function clamp(value: number, min: number, max: number) {
	return Math.min(max, Math.max(min, value));
}

function formatDurationCompact(durationMs: number) {
	const absMs = Math.abs(durationMs);
	if (absMs < HOUR_MS) {
		return `${Math.max(1, Math.round(absMs / MINUTE_MS))}m`;
	}
	if (absMs < DAY_MS) {
		const hours = Math.round(absMs / HOUR_MS);
		return `${Math.max(1, hours)}h`;
	}
	const days = Math.floor(absMs / DAY_MS);
	const remainingHours = Math.round((absMs % DAY_MS) / HOUR_MS);
	if (remainingHours <= 0) {
		return `${days}d`;
	}
	return `${days}d ${remainingHours}h`;
}
