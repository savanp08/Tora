import type { ProjectTimeline, Sprint, TimelineTask } from '$lib/types/timeline';
import { setProjectTimeline, timelineError, timelineLoading } from '$lib/stores/timeline';

const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';

type TemplateErrorResponse = {
	error?: string;
	message?: string;
};

function toISODate(offsetDays: number) {
	const date = new Date();
	date.setDate(date.getDate() + offsetDays);
	return date.toISOString().slice(0, 10);
}

function cloneTask(task: TimelineTask): TimelineTask {
	return {
		...task
	};
}

function cloneSprint(sprint: Sprint): Sprint {
	return {
		...sprint,
		tasks: sprint.tasks.map(cloneTask)
	};
}

function cloneTimeline(timeline: ProjectTimeline): ProjectTimeline {
	return {
		...timeline,
		sprints: timeline.sprints.map(cloneSprint)
	};
}

function buildTaskDescription(task: TimelineTask, sprint: Sprint) {
	const metadata = `[Type: ${task.type} | Effort: ${task.effort_score} | Sprint: ${sprint.start_date} -> ${sprint.end_date}]`;
	const details = (task.description || '').trim();
	if (!details) {
		return metadata;
	}
	return `${details}\n\n${metadata}`;
}

async function parseErrorMessage(response: Response) {
	const payload = (await response.json().catch(() => null)) as TemplateErrorResponse | null;
	return payload?.error?.trim() || payload?.message?.trim() || `HTTP ${response.status}`;
}

export const TIMELINE_TEMPLATES: Record<string, ProjectTimeline> = {
	software_mvp: {
		project_name: 'Software MVP Roadmap',
		total_progress: 0,
		sprints: [
			{
				id: 'sprint-db-auth',
				name: 'Phase 1: DB & Auth',
				start_date: toISODate(0),
				end_date: toISODate(6),
				tasks: [
					{
						id: 'task-db-schema',
						title: 'Finalize database schema',
						status: 'todo',
						effort_score: 6,
						type: 'backend',
						description: 'Design core entities and migration plan.'
					},
					{
						id: 'task-auth-flow',
						title: 'Implement JWT auth flow',
						status: 'todo',
						effort_score: 7,
						type: 'backend',
						description: 'Add login, refresh, and role-based guards.'
					},
					{
						id: 'task-login-ui',
						title: 'Ship login and signup UI',
						status: 'todo',
						effort_score: 4,
						type: 'frontend',
						description: 'Create polished auth forms and validation.'
					}
				]
			},
			{
				id: 'sprint-core-api',
				name: 'Phase 2: Core API',
				start_date: toISODate(7),
				end_date: toISODate(13),
				tasks: [
					{
						id: 'task-resource-crud',
						title: 'Build core CRUD endpoints',
						status: 'todo',
						effort_score: 7,
						type: 'backend',
						description: 'Implement create/read/update/delete for key resources.'
					},
					{
						id: 'task-api-contract-tests',
						title: 'Add API contract tests',
						status: 'todo',
						effort_score: 5,
						type: 'qa',
						description: 'Cover success and failure flows for all core endpoints.'
					},
					{
						id: 'task-observability',
						title: 'Add logs and tracing baseline',
						status: 'todo',
						effort_score: 3,
						type: 'devops',
						description: 'Structured logs and request tracing for all API calls.'
					}
				]
			},
			{
				id: 'sprint-frontend',
				name: 'Phase 3: Frontend',
				start_date: toISODate(14),
				end_date: toISODate(20),
				tasks: [
					{
						id: 'task-dashboard-view',
						title: 'Build dashboard screens',
						status: 'todo',
						effort_score: 6,
						type: 'frontend',
						description: 'Create board, list, and detail views for core entities.'
					},
					{
						id: 'task-state-management',
						title: 'Wire state stores to API',
						status: 'todo',
						effort_score: 5,
						type: 'frontend',
						description: 'Connect API endpoints to stores with loading/error handling.'
					},
					{
						id: 'task-uat-polish',
						title: 'Run UAT and polish UX',
						status: 'todo',
						effort_score: 4,
						type: 'qa',
						description: 'Fix edge cases and improve clarity before release.'
					}
				]
			}
		]
	},
	marketing_campaign: {
		project_name: 'Marketing Campaign Plan',
		total_progress: 0,
		sprints: [
			{
				id: 'sprint-asset-creation',
				name: 'Asset Creation',
				start_date: toISODate(0),
				end_date: toISODate(6),
				tasks: [
					{
						id: 'task-brand-brief',
						title: 'Finalize campaign brief',
						status: 'todo',
						effort_score: 4,
						type: 'strategy',
						description: 'Align goals, audience, channels, and core messaging.'
					},
					{
						id: 'task-creative-assets',
						title: 'Produce creative assets',
						status: 'todo',
						effort_score: 6,
						type: 'design',
						description: 'Design ad variants for social, web, and email.'
					},
					{
						id: 'task-copy-pack',
						title: 'Write copy pack',
						status: 'todo',
						effort_score: 5,
						type: 'content',
						description: 'Draft headlines, CTA variants, and email sequence.'
					}
				]
			},
			{
				id: 'sprint-launch-distribution',
				name: 'Launch & Distribution',
				start_date: toISODate(7),
				end_date: toISODate(13),
				tasks: [
					{
						id: 'task-channel-launch',
						title: 'Launch ads across channels',
						status: 'todo',
						effort_score: 6,
						type: 'marketing',
						description: 'Publish campaign across paid social and search.'
					},
					{
						id: 'task-performance-review',
						title: 'Review performance metrics',
						status: 'todo',
						effort_score: 4,
						type: 'analytics',
						description: 'Analyze CTR, conversion, and spend efficiency.'
					},
					{
						id: 'task-iteration',
						title: 'Iterate creatives and targeting',
						status: 'todo',
						effort_score: 5,
						type: 'optimization',
						description: 'Improve campaign performance based on first-week data.'
					}
				]
			}
		]
	}
};

export async function loadTemplate(roomId: string, templateKey: string) {
	const normalizedRoomID = roomId.trim();
	if (!normalizedRoomID) {
		throw new Error('roomId is required');
	}

	const baseTemplate = TIMELINE_TEMPLATES[templateKey];
	if (!baseTemplate) {
		throw new Error(`Unknown template key: ${templateKey}`);
	}

	const timeline = cloneTimeline(baseTemplate);
	timelineLoading.set(true);
	timelineError.set('');

	try {
		for (const sprint of timeline.sprints) {
			for (const task of sprint.tasks) {
				const response = await fetch(`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomID)}/tasks`, {
					method: 'POST',
					headers: {
						'Content-Type': 'application/json'
					},
					credentials: 'include',
					body: JSON.stringify({
						title: task.title,
						description: buildTaskDescription(task, sprint),
						status: task.status,
						sprint_name: sprint.name
					})
				});
				if (!response.ok) {
					throw new Error(await parseErrorMessage(response));
				}

				const created = (await response.json().catch(() => null)) as Record<string, unknown> | null;
				const createdID = typeof created?.id === 'string' ? created.id.trim() : '';
				if (createdID) {
					task.id = createdID;
				}
			}
		}

		setProjectTimeline(timeline);
		return timeline;
	} catch (error) {
		const message = error instanceof Error ? error.message : 'Failed to load timeline template';
		timelineError.set(message);
		throw error instanceof Error ? error : new Error(message);
	} finally {
		timelineLoading.set(false);
	}
}
