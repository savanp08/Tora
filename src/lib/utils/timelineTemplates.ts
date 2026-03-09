import { get } from 'svelte/store';
import type { ProjectTimeline, Sprint, TimelineTask } from '$lib/types/timeline';
import { currentUser } from '$lib/store';
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
	software_agile: {
		project_name: 'Software Agile Delivery',
		total_progress: 0,
		sprints: [
			{
				id: 'sprint-backlog',
				name: 'Backlog',
				start_date: toISODate(0),
				end_date: toISODate(6),
				tasks: [
					{
						id: 'task-refinement',
						title: 'Refine backlog stories',
						status: 'todo',
						effort_score: 4,
						type: 'planning',
						description: 'Split epics into sprint-ready tasks with clear acceptance criteria.'
					},
					{
						id: 'task-prioritization',
						title: 'Prioritize sprint scope',
						status: 'todo',
						effort_score: 3,
						type: 'planning',
						description: 'Balance impact and effort across frontend, backend, and QA.'
					}
				]
			},
			{
				id: 'sprint-frontend',
				name: 'Frontend',
				start_date: toISODate(7),
				end_date: toISODate(13),
				tasks: [
					{
						id: 'task-ui-shell',
						title: 'Build workspace shell screens',
						status: 'todo',
						effort_score: 6,
						type: 'frontend',
						description: 'Implement primary layout, routing, and responsive behavior.'
					},
					{
						id: 'task-forms',
						title: 'Implement task create/edit flows',
						status: 'todo',
						effort_score: 5,
						type: 'frontend',
						description: 'Create polished task forms, validation, and optimistic updates.'
					}
				]
			},
			{
				id: 'sprint-backend',
				name: 'Backend',
				start_date: toISODate(14),
				end_date: toISODate(20),
				tasks: [
					{
						id: 'task-apis',
						title: 'Ship core task APIs',
						status: 'todo',
						effort_score: 7,
						type: 'backend',
						description: 'Expose CRUD endpoints, status transitions, and filters.'
					},
					{
						id: 'task-events',
						title: 'Integrate websocket task events',
						status: 'todo',
						effort_score: 5,
						type: 'backend',
						description: 'Broadcast create/move/update events for collaborative updates.'
					}
				]
			},
			{
				id: 'sprint-qa',
				name: 'QA',
				start_date: toISODate(21),
				end_date: toISODate(27),
				tasks: [
					{
						id: 'task-regression',
						title: 'Run regression checklist',
						status: 'todo',
						effort_score: 4,
						type: 'qa',
						description: 'Validate all critical paths across desktop and mobile.'
					},
					{
						id: 'task-bug-bash',
						title: 'Bug bash and release polish',
						status: 'todo',
						effort_score: 4,
						type: 'qa',
						description: 'Fix high-impact issues and stabilize release build.'
					}
				]
			}
		]
	},
	marketing_blitz: {
		project_name: 'Marketing Blitz Campaign',
		total_progress: 0,
		sprints: [
			{
				id: 'sprint-strategy',
				name: 'Strategy',
				start_date: toISODate(0),
				end_date: toISODate(6),
				tasks: [
					{
						id: 'task-audience',
						title: 'Define ICP and audience segments',
						status: 'todo',
						effort_score: 4,
						type: 'strategy',
						description: 'Map priorities, channels, and conversion goals.'
					},
					{
						id: 'task-offer',
						title: 'Finalize offer and message pillars',
						status: 'todo',
						effort_score: 4,
						type: 'strategy',
						description: 'Lock the offer narrative and value proposition.'
					}
				]
			},
			{
				id: 'sprint-assets',
				name: 'Asset Creation',
				start_date: toISODate(7),
				end_date: toISODate(13),
				tasks: [
					{
						id: 'task-ad-creative',
						title: 'Produce ad creative set',
						status: 'todo',
						effort_score: 6,
						type: 'design',
						description: 'Create static/video variations for top channels.'
					},
					{
						id: 'task-creative-assets',
						title: 'Build landing page + email assets',
						status: 'todo',
						effort_score: 6,
						type: 'content',
						description: 'Prepare launch copy and tracking-ready page sections.'
					}
				]
			},
			{
				id: 'sprint-launch',
				name: 'Ad Launch',
				start_date: toISODate(14),
				end_date: toISODate(20),
				tasks: [
					{
						id: 'task-launch-ops',
						title: 'Launch paid campaigns',
						status: 'todo',
						effort_score: 5,
						type: 'marketing',
						description: 'Activate channel plans and monitor budget pacing.'
					},
					{
						id: 'task-week1-opt',
						title: 'Optimize first-week performance',
						status: 'todo',
						effort_score: 5,
						type: 'analytics',
						description: 'Adjust copy, audience, and bids from performance signals.'
					}
				]
			}
		]
	},
	time_critical: {
		project_name: 'Time-Critical Ops Plan',
		total_progress: 0,
		sprints: [
			{
				id: 'day-1',
				name: 'Day 1',
				start_date: toISODate(0),
				end_date: toISODate(0),
				tasks: [
					{
						id: 'day1-triage',
						title: 'Initial incident triage',
						status: 'todo',
						effort_score: 5,
						type: 'operations',
						description: 'Assess impact and lock immediate mitigation steps.'
					},
					{
						id: 'day1-owners',
						title: 'Assign owners and escalation paths',
						status: 'todo',
						effort_score: 3,
						type: 'coordination',
						description: 'Map each blocker to an accountable owner.'
					}
				]
			},
			{
				id: 'day-2',
				name: 'Day 2',
				start_date: toISODate(1),
				end_date: toISODate(1),
				tasks: [
					{
						id: 'day2-fixes',
						title: 'Ship critical fixes',
						status: 'todo',
						effort_score: 7,
						type: 'execution',
						description: 'Deliver high-priority fixes and verify rollback safety.'
					},
					{
						id: 'day2-comms',
						title: 'Publish stakeholder update',
						status: 'todo',
						effort_score: 3,
						type: 'communication',
						description: 'Share progress, risk status, and next timeline.'
					}
				]
			}
		]
	},
	high_volume: {
		project_name: 'High-Volume Workflow Board',
		total_progress: 0,
		sprints: [
			{
				id: 'bucket-triage',
				name: 'Triage',
				start_date: toISODate(0),
				end_date: toISODate(2),
				tasks: [
					{
						id: 'triage-intake',
						title: 'Intake queue review',
						status: 'todo',
						effort_score: 3,
						type: 'bucket',
						description: 'Dummy bucket task: classify incoming items by urgency.'
					},
					{
						id: 'triage-routing',
						title: 'Route items to workstreams',
						status: 'todo',
						effort_score: 4,
						type: 'bucket',
						description: 'Dummy bucket task: assign each item to correct pipeline.'
					}
				]
			},
			{
				id: 'bucket-processing',
				name: 'Processing',
				start_date: toISODate(3),
				end_date: toISODate(6),
				tasks: [
					{
						id: 'processing-core',
						title: 'Bulk processing pass',
						status: 'todo',
						effort_score: 6,
						type: 'bucket',
						description: 'Dummy bucket task: execute primary handling workflow.'
					},
					{
						id: 'processing-escalations',
						title: 'Exception handling',
						status: 'todo',
						effort_score: 5,
						type: 'bucket',
						description: 'Dummy bucket task: resolve escalated edge cases.'
					}
				]
			},
			{
				id: 'bucket-review',
				name: 'Review',
				start_date: toISODate(7),
				end_date: toISODate(9),
				tasks: [
					{
						id: 'review-qa',
						title: 'Quality review sample',
						status: 'todo',
						effort_score: 4,
						type: 'bucket',
						description: 'Dummy bucket task: audit quality against expected output.'
					},
					{
						id: 'review-signoff',
						title: 'Final sign-off batch',
						status: 'todo',
						effort_score: 3,
						type: 'bucket',
						description: 'Dummy bucket task: close processed work and publish summary.'
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
	const sessionUserID = (get(currentUser)?.id || '').trim();

	try {
		for (const sprint of timeline.sprints) {
			for (const task of sprint.tasks) {
				const response = await fetch(`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomID)}/tasks`, {
					method: 'POST',
					headers: {
						'Content-Type': 'application/json',
						...(sessionUserID ? { 'X-User-Id': sessionUserID } : {})
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
