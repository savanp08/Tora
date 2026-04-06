<script lang="ts">
	import { onDestroy, tick } from 'svelte';
	import CalendarView from '$lib/components/workspace/CalendarView.svelte';
	import WorkloadView from '$lib/components/workspace/WorkloadView.svelte';
	import type { OnlineMember } from '$lib/types/chat';
	import { currentUser } from '$lib/store';
	import { activeContext } from '$lib/stores/jiraContext';
	import { addBoardActivity, type BoardActivityInput } from '$lib/stores/boardActivity';
	import { fieldSchemaStore, type FieldSchema } from '$lib/stores/fieldSchema';
	import { projectTypeConfig } from '$lib/stores/projectType';
	import { applyTimelineTaskStatusUpdate, projectTimeline } from '$lib/stores/timeline';
	import type { ProjectTimeline } from '$lib/types/timeline';
	import {
		moveTaskOptimistic,
		removeTaskStoreEntry,
		taskStore,
		taskStoreError,
		taskStoreLoading,
		upsertTaskStoreEntry
	} from '$lib/stores/tasks';
	import { normalizeIdentifier, normalizeRoomIDValue, toStringValue } from '$lib/utils/chat/core';
	import { sendSocketPayload } from '$lib/ws';
	import { buildBoardActivitySocketPayload, buildTaskSocketPayload } from '$lib/ws/client';
	import ChangeRequestModal from './ChangeRequestModal.svelte';
	import { submitChangeRequest, type ChangeRequestAction } from '$lib/stores/changeRequests';

	export let roomId = '';
	export let canEdit = true;
	export let isAdmin = false;
	export let sessionUserID = '';
	export let sessionUserName = '';
	export let contextAware = false;
	export let onlineMembers: OnlineMember[] = [];
	export let boardView: BoardView = 'table';
	export let externalEditTaskId = '';
	export let onExternalEditHandled: (taskId: string) => void = () => {};
	export let externalOpenTaskId = '';
	export let onExternalOpenHandled: (taskId: string) => void = () => {};

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';
	const STORAGE_FULL_UPLOAD_MESSAGE =
		'Server storage is temporarily full. Uploads will be available again once older rooms expire.';

	const STATUS_OPTIONS = [
		{ value: 'todo', label: 'To Do' },
		{ value: 'in_progress', label: 'Working on it' },
		{ value: 'done', label: 'Done' }
	] as const;
	const TASK_NOTES_FIELD_KEY = 'task_notes';
	const TASK_DETAIL_SUMMARY_FIELD_KEY = 'task_detail_summary';
	const TASK_DETAIL_STEPS_FIELD_KEY = 'task_detail_steps';
	const TASK_DETAIL_GENERATED_AT_FIELD_KEY = 'task_detail_generated_at';
	const INLINE_GRID_EDIT_FIELDS: EditableField[] = ['title', 'assigneeId', 'budget', 'spent'];

	type ColumnKey = (typeof STATUS_OPTIONS)[number]['value'];
	type BoardView = 'table' | 'kanban' | 'support' | 'calendar' | 'workload';
	type SprintSortColumn = 'title' | 'status' | 'owner' | 'budget' | 'spent' | 'updated';
	type SprintSortDirection = 'asc' | 'desc';
	type SprintSortState = {
		column: SprintSortColumn;
		direction: SprintSortDirection;
	};
	type EditableField =
		| 'title'
		| 'description'
		| 'assigneeId'
		| 'sprintName'
		| 'budget'
		| 'spent'
		| 'dueDate'
		| 'startDate';
	type EditableCellKey = EditableField | 'status';
	type TaskSource = 'personal' | 'room';
	type SupportPriority = 'critical' | 'high' | 'medium' | 'low';
	type DisplaySubtask = {
		id: string;
		content: string;
		completed: boolean;
		position: number;
	};
	type TaskRole = { role: string; responsibilities: string };

	type DisplayTask = {
		id: string;
		roomId: string;
		title: string;
		description: string;
		status: string;
		taskType: string;
		customFields: Record<string, unknown>;
		blockedBy: string[];
		blocks: string[];
		subtasks: DisplaySubtask[];
		completionPercent?: number;
		budget?: number;
		spent?: number;
		dueDate?: number;
		startDate?: number;
		roles?: TaskRole[];
		sprintName: string;
		assigneeId: string;
		statusActorId?: string;
		statusActorName?: string;
		statusChangedAt?: number;
		createdAt: number;
		updatedAt: number;
		source: TaskSource;
	};

	type PersonalItemResponse = {
		item_id?: unknown;
		title?: unknown;
		content?: unknown;
		description?: unknown;
		status?: unknown;
		created_at?: unknown;
		updated_at?: unknown;
	};

	type RoomTaskResponse = {
		id?: unknown;
		title?: unknown;
		description?: unknown;
		status?: unknown;
		task_type?: unknown;
		taskType?: unknown;
		custom_fields?: unknown;
		customFields?: unknown;
		sprint_name?: unknown;
		sprintName?: unknown;
		assignee_id?: unknown;
		assigneeId?: unknown;
		status_actor_id?: unknown;
		statusActorId?: unknown;
		status_actor_name?: unknown;
		statusActorName?: unknown;
		status_changed_at?: unknown;
		statusChangedAt?: unknown;
		blocked_by?: unknown;
		blockedBy?: unknown;
		blocks?: unknown;
		subtasks?: unknown;
		completion_percent?: unknown;
		completionPercent?: unknown;
		budget?: unknown;
		task_budget?: unknown;
		taskBudget?: unknown;
		actual_cost?: unknown;
		actualCost?: unknown;
		spent?: unknown;
		spent_cost?: unknown;
		due_date?: unknown;
		dueDate?: unknown;
		start_date?: unknown;
		startDate?: unknown;
		spentCost?: unknown;
		roles?: unknown;
		created_at?: unknown;
		createdAt?: unknown;
		updated_at?: unknown;
		updatedAt?: unknown;
	};

	type RoomTaskStatusUpdateResponse = {
		status?: unknown;
		status_actor_id?: unknown;
		status_actor_name?: unknown;
		status_changed_at?: unknown;
		updated_at?: unknown;
	};

	type StatusUpdateMetadata = {
		status: ColumnKey;
		statusActorId: string;
		statusActorName: string;
		statusChangedAt: number;
		updatedAt: number;
	};

	type SprintTaskGroup = {
		key: string;
		name: string;
		tasks: DisplayTask[];
		lastUpdatedAt: number;
		searchScore?: number;
	};

	type OwnerOption = {
		id: string;
		label: string;
		isOnline: boolean;
	};

	type KanbanColumn = {
		key: ColumnKey;
		label: string;
		tasks: DisplayTask[];
	};

	type SupportTicketCard = {
		task: DisplayTask;
		priority: SupportPriority;
		concernedIds: string[];
		linkedTaskIds: string[];
		details: string;
	};

	const STATUS_ORDER: Record<ColumnKey, number> = {
		in_progress: 0,
		todo: 1,
		done: 2
	};
	const KANBAN_COLUMN_ORDER: ColumnKey[] = ['todo', 'in_progress', 'done'];
	const SUPPORT_PRIORITY_OPTIONS: Array<{ value: SupportPriority; label: string }> = [
		{ value: 'critical', label: 'Critical' },
		{ value: 'high', label: 'High' },
		{ value: 'medium', label: 'Medium' },
		{ value: 'low', label: 'Low' }
	];
	const sprintNameCollator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });
	const taskSortCollator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

	let contextTasks: DisplayTask[] = [];
	let contextLoading = false;
	let contextError = '';
	let creatingTask = false;
	let newTaskContent = '';
	let lastContextKey = '';
	let contextLoadToken = 0;
	let roomBoardError = '';

	let editingTaskId = '';
	let editingField: EditableField | '' = '';
	let editingValue = '';
	let editingCustomFields: Record<string, unknown> = {};
	let editingCustomFieldsBaseline: Record<string, unknown> = {};
	let editingBlockedBy: string[] = [];
	let editingBlockedByBaseline: string[] = [];
	let editingSubtasks: DisplaySubtask[] = [];
	let editingSubtasksBaseline: DisplaySubtask[] = [];
	let savingRelations = false;
	let savingCellKey = '';
	let savingStatusTaskId = '';
	let quickEditVisible = false;
	let quickEditorElement: HTMLInputElement | HTMLSelectElement | null = null;
	let inlineEditorElement: HTMLInputElement | HTMLSelectElement | null = null;
	let statusMenuTaskId = '';

	// ── task edit modal ───────────────────────────────────────────────────────
	let taskEditModal: DisplayTask | null = null;
	let taskEditVals: {
		title: string;
		description: string;
		notes: string;
		assigneeId: string;
		status: string;
		budget: string;
		spent: string;
		dueDate: string;
		startDate: string;
	} = {
		title: '',
		description: '',
		notes: '',
		assigneeId: '',
		status: 'todo',
		budget: '',
		spent: '',
		dueDate: '',
		startDate: ''
	};
	let taskModalSaving = false;
	let taskDetailSaving = false;
	let taskDetailGenerating = false;
	let taskDetailSummary = '';
	let taskDetailSteps: string[] = [];
	let taskDetailGeneratedAt = '';
	let taskDetailNotice = '';
	let taskDetailError = '';
	let hoveredTaskId = '';
	let newTaskInput: HTMLInputElement | null = null;
	let editingTask: DisplayTask | null = null;
	let sprintComposerNameInput: HTMLInputElement | null = null;
	let sprintComposerTaskInput: HTMLInputElement | null = null;
	let sprintComposerOpen = false;
	let sprintComposerName = '';
	let sprintComposerTaskDrafts: string[] = [];
	let sprintComposerTaskInputValue = '';
	let sprintComposerActiveTaskIndex = -1;
	let sprintComposerSaving = false;
	type SprintComposerMeta = { status: string; assigneeId: string; budget: string; spent: string };
	let sprintComposerRowMeta: Record<number, SprintComposerMeta> = {};
	let sprintDraftGroupsByContext: Record<string, string[]> = {};

	// Sprint add task state
	let sprintAddKey = '';
	let crModalOpen = false;
	let searchExpanded = false;
	let crModalAction: ChangeRequestAction = 'edit_task';
	let crModalTargetLabel = '';
	let crModalPayload: Record<string, unknown> = {};

	function openCR(action: ChangeRequestAction, targetLabel: string, payload: Record<string, unknown> = {}) {
		crModalAction = action;
		crModalTargetLabel = targetLabel;
		crModalPayload = payload;
		crModalOpen = true;
	}
	let sprintAddContent = '';
	let sprintAddCreating = false;
	let taskSearchQuery = '';
	let sprintSortStateByKey: Record<string, SprintSortState | undefined> = {};
	const sprintAddFormByKey = new Map<string, HTMLFormElement>();

	let supportTicketTitle = '';
	let supportTicketDetails = '';
	let supportTicketPriority: SupportPriority = 'medium';
	let selectedSupportTaskIds: string[] = [];
	let selectedConcernedMemberIds: string[] = [];
	let supportTicketCreating = false;
	let boardToastMessage = '';
	let boardToastTimer: ReturnType<typeof setTimeout> | null = null;

	// Per-sprint edit mode (checkboxes + add/delete actions only shown when editing)
	let sprintEditKeys = new Set<string>();

	// Multi-select delete state
	let selectedTaskIds: string[] = [];
	let deletingTaskIds: string[] = [];
	let ownerOptions: OwnerOption[] = [];
	let canCreateSprintTask = false;
	const SPRINT_COMPOSER_MAX_TASKS = 25;
	let externalEditInFlight = false;
	let externalOpenInFlight = false;

	onDestroy(() => {
		if (boardToastTimer) {
			clearTimeout(boardToastTimer);
			boardToastTimer = null;
		}
	});

	$: sessionUserID = ($currentUser?.id || '').trim();
	$: sessionUsername = ($currentUser?.username || '').trim();
	$: normalizedRoomId = normalizeRoomIDValue(roomId);
	$: taskTerm = $projectTypeConfig.taskTerm;
	$: taskTermPlural = $projectTypeConfig.taskTermPlural;
	$: groupTerm = $projectTypeConfig.groupTerm;
	$: groupTermPlural = $projectTypeConfig.groupTermPlural;
	$: taskLabel = taskTerm.toLowerCase();
	$: taskPluralLabel = taskTermPlural.toLowerCase();
	$: groupLabel = groupTerm.toLowerCase();
	$: groupPluralLabel = groupTermPlural.toLowerCase();
	$: boardTitle = contextAware
		? $activeContext.name.trim() || `Workspace ${taskTermPlural}`
		: `Room ${taskTermPlural}`;
	$: contextKey = `${$activeContext.type}:${$activeContext.id}`;
	$: sprintContextKey = contextAware ? contextKey : `room:${normalizedRoomId}`;
	$: if (contextAware && contextKey !== lastContextKey) {
		lastContextKey = contextKey;
		sprintComposerOpen = false;
		sprintComposerName = '';
		sprintComposerTaskDrafts = [];
		sprintComposerTaskInputValue = '';
		sprintComposerActiveTaskIndex = -1;
		sprintComposerRowMeta = {};
		void loadContextTasks();
	}

	$: roomTasks = dedupeDisplayTasksById(
		[...$taskStore].map(
			(task): DisplayTask => ({
				id: task.id,
				roomId: task.roomId,
				title: task.title,
				description: task.description,
				status: task.status,
				taskType: task.taskType,
				customFields: { ...(task.customFields ?? {}) },
				blockedBy: [...(task.blockedBy ?? [])],
				blocks: [...(task.blocks ?? [])],
				subtasks: cloneDisplaySubtasks(task.subtasks ?? []),
				completionPercent: task.completionPercent,
				budget: task.budget,
				spent: task.spent,
				dueDate: task.dueDate,
				startDate: task.startDate,
				sprintName: task.sprintName || '',
				assigneeId: task.assigneeId,
				statusActorId: task.statusActorId,
				statusActorName: task.statusActorName,
				statusChangedAt: task.statusChangedAt,
				createdAt: task.createdAt,
				updatedAt: task.updatedAt,
				source: 'room'
			})
		)
	).sort(compareTasksForGrid);
	$: contextGridTasks = dedupeDisplayTasksById([...contextTasks]).sort(compareTasksForGrid);
	$: boardTasks = contextAware ? contextGridTasks : roomTasks;
	$: timelineDateMap = buildTimelineDateMap($projectTimeline);
	$: calendarWorkloadTasks = boardTasks.map((task) => {
		if (task.dueDate || task.startDate) return task;
		const tl = timelineDateMap.get(task.id);
		if (!tl) return task;
		return {
			...task,
			dueDate: tl.endDate > 0 ? tl.endDate : undefined,
			startDate: tl.startDate > 0 ? tl.startDate : undefined
		};
	});
	$: if (
		boardView !== 'table' &&
		boardView !== 'kanban' &&
		boardView !== 'support' &&
		boardView !== 'calendar' &&
		boardView !== 'workload'
	) {
		boardView = 'table';
	}
	$: boardLoading = contextAware ? contextLoading : $taskStoreLoading;
	$: boardError = contextAware ? contextError : roomBoardError || $taskStoreError;
	$: {
		const requestedExternalEditID = externalEditTaskId.trim();
		if (!externalEditInFlight && requestedExternalEditID) {
			const targetTask = boardTasks.find((task) => task.id === requestedExternalEditID);
			if (targetTask && canEdit) {
				externalEditInFlight = true;
				void (async () => {
					try {
						await startEditing(targetTask, 'title');
					} finally {
						onExternalEditHandled(requestedExternalEditID);
						externalEditInFlight = false;
					}
				})();
			} else if (!boardLoading || !canEdit) {
				onExternalEditHandled(requestedExternalEditID);
			}
		}
	}
	$: {
		const requestedExternalOpenID = externalOpenTaskId.trim();
		if (!externalOpenInFlight && requestedExternalOpenID) {
			const targetTask = boardTasks.find((task) => task.id === requestedExternalOpenID);
			if (targetTask) {
				externalOpenInFlight = true;
				try {
					openTaskDetails(targetTask);
				} finally {
					onExternalOpenHandled(requestedExternalOpenID);
					externalOpenInFlight = false;
				}
			} else if (!boardLoading) {
				onExternalOpenHandled(requestedExternalOpenID);
			}
		}
	}
	$: editingTask = boardTasks.find((task) => task.id === editingTaskId) ?? null;
	$: if (!editingTaskId || !editingField) {
		quickEditVisible = false;
	}
	$: roomFieldSchemas = [...$fieldSchemaStore];
	$: ownerOptions = buildOwnerOptions(onlineMembers, boardTasks);
	$: canCreateSprintTask = canEdit && (!contextAware || $activeContext.type === 'room');
	$: sprintDraftGroups = sprintDraftGroupsByContext[sprintContextKey] ?? [];
	$: sprintComposerPreviewRows = buildSprintComposerPreviewRows(sprintComposerTaskDrafts, sprintComposerRowMeta, sprintComposerActiveTaskIndex);
	$: normalizedTaskSearchQuery = normalizeSearchQuery(taskSearchQuery);
	$: taskSearchTokens = buildSearchTokens(normalizedTaskSearchQuery);
	$: hasActiveTaskSearch = normalizedTaskSearchQuery.length > 0;
	$: hasAnyTasks = boardTasks.length > 0;
	$: boardLastUpdatedAt = boardTasks.reduce(
		(latest, task) => Math.max(latest, Number.isFinite(task.updatedAt) ? task.updatedAt : 0),
		0
	);
	$: sprintTaskGroups = (() => {
		const grouped = new Map<string, SprintTaskGroup>();
		for (const task of supportSourceTasks) {
			const sprintName = task.sprintName.trim() || 'Backlog';
			const key = sprintName.toLowerCase();
			const existing = grouped.get(key);
			if (existing) {
				existing.tasks.push(task);
				existing.lastUpdatedAt = Math.max(existing.lastUpdatedAt, task.updatedAt || 0);
				continue;
			}
			grouped.set(key, {
				key,
				name: sprintName,
				tasks: [task],
				lastUpdatedAt: task.updatedAt || 0
			});
		}
		for (const draftSprintName of sprintDraftGroups) {
			const trimmedDraftName = draftSprintName.trim();
			const key = sprintGroupKey(trimmedDraftName);
			if (!trimmedDraftName || grouped.has(key)) {
				continue;
			}
			grouped.set(key, {
				key,
				name: trimmedDraftName,
				tasks: [],
				lastUpdatedAt: 0
			});
		}

		return [...grouped.values()]
			.map((group) => {
				const sortState = sprintSortStateByKey[group.key];
				const sortedTasks = sortTasksForSprintGroup(group.tasks, sortState);
				if (!hasActiveTaskSearch) {
					return {
						...group,
						tasks: sortedTasks,
						searchScore: 0
					};
				}

				const searchMatches = sortedTasks
					.map((task) => ({
						task,
						score: scoreTaskSearchMatch(
							task,
							group.name,
							normalizedTaskSearchQuery,
							taskSearchTokens
						)
					}))
					.filter((entry) => entry.score > 0)
					.sort(
						(left, right) =>
							right.score - left.score ||
							compareTasksBySortState(left.task, right.task, sortState) ||
							compareTasksForGrid(left.task, right.task)
					);

				return {
					...group,
					tasks: searchMatches.map((entry) => entry.task),
					searchScore: searchMatches[0]?.score ?? 0
				};
			})
			.filter((group) => !hasActiveTaskSearch || group.tasks.length > 0)
			.sort((left, right) => {
				if (hasActiveTaskSearch) {
					const searchDiff = (right.searchScore || 0) - (left.searchScore || 0);
					if (searchDiff !== 0) {
						return searchDiff;
					}
				}
				if (left.name === 'Backlog' && right.name !== 'Backlog') return 1;
				if (right.name === 'Backlog' && left.name !== 'Backlog') return -1;
				return sprintNameCollator.compare(left.name, right.name);
			});
	})();
	$: sprintDisplayNameByKey = (() => {
		const grouped = new Map<string, SprintTaskGroup>();
		for (const task of supportSourceTasks) {
			const sprintName = task.sprintName.trim() || 'Backlog';
			const key = sprintGroupKey(sprintName);
			if (grouped.has(key)) {
				continue;
			}
			grouped.set(key, {
				key,
				name: sprintName,
				tasks: [],
				lastUpdatedAt: 0
			});
		}
		for (const draftSprintName of sprintDraftGroups) {
			const trimmedDraftName = draftSprintName.trim();
			const key = sprintGroupKey(trimmedDraftName);
			if (!trimmedDraftName || grouped.has(key)) {
				continue;
			}
			grouped.set(key, {
				key,
				name: trimmedDraftName,
				tasks: [],
				lastUpdatedAt: 0
			});
		}
		const orderedGroups = [...grouped.values()].sort((left, right) => {
			if (left.name === 'Backlog' && right.name !== 'Backlog') return 1;
			if (right.name === 'Backlog' && left.name !== 'Backlog') return -1;
			return sprintNameCollator.compare(left.name, right.name);
		});
		return buildSprintDisplayNameMap(orderedGroups);
	})();
	$: hasBoardDataForView = boardView === 'table' ? sprintTaskGroups.length > 0 : hasAnyTasks;
	$: kanbanColumns = KANBAN_COLUMN_ORDER.map<KanbanColumn>((columnKey) => ({
		key: columnKey,
		label: statusLabel(columnKey),
		tasks: supportSourceTasks
			.filter((task) => resolveColumn(task.status) === columnKey)
			.sort(compareTasksForGrid)
	}));
	$: onlineConcernedOptions = ownerOptions.filter((option) => option.isOnline);
	$: supportSourceTasks = boardTasks.filter((task) => !isSupportTicket(task));
	$: supportTickets = boardTasks.filter((task) => isSupportTicket(task));
	$: supportCurrentSprintName = resolveSupportCurrentSprintName(supportSourceTasks);
	$: supportCurrentSprintKey = sprintGroupKey(supportCurrentSprintName);
	$: supportSourceTasksForSprint = supportSourceTasks.filter(
		(task) => sprintGroupKey(task.sprintName) === supportCurrentSprintKey
	);
	$: supportTicketCards = supportTickets
		.filter((task) => sprintGroupKey(task.sprintName) === supportCurrentSprintKey)
		.map((task) => buildSupportTicketCard(task))
		.sort(
			(left, right) =>
				supportPriorityRank(left.priority) - supportPriorityRank(right.priority) ||
				right.task.updatedAt - left.task.updatedAt
		);
	$: {
		const validTaskIds = new Set(supportSourceTasksForSprint.map((task) => task.id));
		const nextSelectedTaskIds = selectedSupportTaskIds.filter((taskId) => validTaskIds.has(taskId));
		if (nextSelectedTaskIds.length !== selectedSupportTaskIds.length) {
			selectedSupportTaskIds = nextSelectedTaskIds;
		}
	}
	$: {
		const validConcernedIds = new Set(
			onlineConcernedOptions.map((option) => normalizeMemberId(option.id))
		);
		const nextConcernedIds = selectedConcernedMemberIds.filter((memberId) =>
			validConcernedIds.has(normalizeMemberId(memberId))
		);
		if (nextConcernedIds.length !== selectedConcernedMemberIds.length) {
			selectedConcernedMemberIds = nextConcernedIds;
		}
	}

	function normalizeMemberId(value: string) {
		return normalizeIdentifier(value).toLowerCase();
	}

	function buildOwnerOptions(members: OnlineMember[], tasks: DisplayTask[]) {
		const seen = new Set<string>();
		const next: OwnerOption[] = [];
		for (const member of members) {
			const id = toStringValue(member.id).trim();
			const name = toStringValue(member.name).trim();
			const normalizedId = normalizeMemberId(id);
			if (!id || !name || !normalizedId || seen.has(normalizedId)) {
				continue;
			}
			seen.add(normalizedId);
			next.push({
				id,
				label: name.replace(/_/g, ' '),
				isOnline: Boolean(member.isOnline)
			});
		}
		for (const task of tasks) {
			const explicitOwnerId = (task.assigneeId || '').trim();
			const metadataOwner = readDescriptionMetadataValue(task.description, 'owner').trim();
			const fallbackRaw = explicitOwnerId || metadataOwner;
			if (!fallbackRaw) {
				continue;
			}
			const normalizedFallback = normalizeMemberId(fallbackRaw);
			if (!normalizedFallback || seen.has(normalizedFallback)) {
				continue;
			}
			seen.add(normalizedFallback);
			next.push({
				id: fallbackRaw,
				label: fallbackRaw.replace(/_/g, ' '),
				isOnline: false
			});
		}
		return next.sort(
			(left, right) =>
				Number(right.isOnline) - Number(left.isOnline) ||
				left.label.localeCompare(right.label, undefined, { sensitivity: 'base' })
		);
	}

	function getOwnerOptionById(ownerId: string) {
		const normalizedOwnerId = normalizeMemberId(ownerId);
		if (!normalizedOwnerId) {
			return null;
		}
		return (
			ownerOptions.find((option) => normalizeMemberId(option.id) === normalizedOwnerId) ?? null
		);
	}

	function ownerOptionsForTask(task: DisplayTask) {
		const currentRaw = getTaskFieldValue(task, 'assigneeId').trim();
		const options = [...ownerOptions];
		if (!currentRaw) {
			return options;
		}
		const hasCurrent = options.some(
			(option) => normalizeMemberId(option.id) === normalizeMemberId(currentRaw)
		);
		if (hasCurrent) {
			return options;
		}
		return [
			{
				id: currentRaw,
				label: `${ownerLabel(task)} (current)`,
				isOnline: false
			},
			...options
		];
	}

	function withSessionUserHeaders(headers: Record<string, string> = {}) {
		if (!sessionUserID) {
			if (!sessionUsername) {
				return headers;
			}
			return {
				...headers,
				'X-User-Name': sessionUsername
			};
		}
		return {
			...headers,
			'X-User-Id': sessionUserID,
			'X-User-Name': sessionUsername
		};
	}

	function parseStatusUpdateMetadata(
		payload: unknown,
		fallbackStatus: ColumnKey
	): StatusUpdateMetadata {
		const source =
			payload && typeof payload === 'object' && !Array.isArray(payload)
				? (payload as RoomTaskStatusUpdateResponse)
				: null;
		const statusValue = resolveColumn(toStringValue(source?.status) || fallbackStatus);
		const statusActorId = toStringValue(source?.status_actor_id);
		const statusActorName = toStringValue(source?.status_actor_name);
		const statusChangedAt = parseTimestamp(source?.status_changed_at);
		const updatedAt = parseTimestamp(source?.updated_at) || statusChangedAt;
		return {
			status: statusValue,
			statusActorId,
			statusActorName,
			statusChangedAt,
			updatedAt
		};
	}

	function compareTasksForGrid(left: DisplayTask, right: DisplayTask) {
		const statusDiff =
			STATUS_ORDER[resolveColumn(left.status)] - STATUS_ORDER[resolveColumn(right.status)];
		if (statusDiff !== 0) {
			return statusDiff;
		}
		const updatedDiff = right.updatedAt - left.updatedAt;
		if (updatedDiff !== 0) {
			return updatedDiff;
		}
		return left.title.localeCompare(right.title);
	}

	function normalizeSearchQuery(value: string) {
		return value.trim().toLowerCase();
	}

	function buildSearchTokens(query: string) {
		if (!query) {
			return [] as string[];
		}
		return [
			...new Set(
				query
					.split(/\s+/)
					.map((token) => token.trim())
					.filter(Boolean)
			)
		];
	}

	function scoreSearchField(value: string, token: string, weight: number) {
		if (!value || !token) {
			return 0;
		}
		if (value === token) {
			return weight * 3;
		}
		if (value.startsWith(token)) {
			return weight * 2;
		}
		if (value.includes(token)) {
			return weight;
		}
		return 0;
	}

	function scoreTaskSearchMatch(
		task: DisplayTask,
		sprintName: string,
		normalizedQuery: string,
		tokens: string[]
	) {
		if (!normalizedQuery) {
			return 1;
		}
		const sprintValue = normalizeSearchQuery(sprintName);
		const titleValue = normalizeSearchQuery(task.title);
		const statusValue = normalizeSearchQuery(statusLabel(resolveColumn(task.status)));
		const ownerValue = normalizeSearchQuery(ownerLabel(task));
		const budgetValue = normalizeSearchQuery(formatBudgetCell(task.budget));
		const spentValue = normalizeSearchQuery(formatSpentCell(task.spent, task.budget));
		const updatedValue = normalizeSearchQuery(formatCellTime(task.updatedAt));
		const descriptionBase = parseDescriptionMetadata(task.description).base || task.description;
		const descriptionValue = normalizeSearchQuery(descriptionBase);
		const idValue = normalizeSearchQuery(task.id);

		const weightedFields = [
			{ value: sprintValue, weight: 120 },
			{ value: titleValue, weight: 110 },
			{ value: ownerValue, weight: 52 },
			{ value: statusValue, weight: 40 },
			{ value: descriptionValue, weight: 34 },
			{ value: budgetValue, weight: 26 },
			{ value: spentValue, weight: 26 },
			{ value: updatedValue, weight: 18 },
			{ value: idValue, weight: 14 }
		];

		let score = 0;
		let matchedTokens = 0;
		const searchTokens = tokens.length > 0 ? tokens : [normalizedQuery];
		for (const token of searchTokens) {
			const tokenScore = weightedFields.reduce((best, field) => {
				const fieldScore = scoreSearchField(field.value, token, field.weight);
				return fieldScore > best ? fieldScore : best;
			}, 0);
			if (tokenScore <= 0) {
				continue;
			}
			score += tokenScore;
			matchedTokens += 1;
		}

		if (matchedTokens === 0) {
			return 0;
		}

		if (sprintValue.includes(normalizedQuery)) {
			score += 220;
		}
		if (titleValue.includes(normalizedQuery)) {
			score += 200;
		}
		if (descriptionValue.includes(normalizedQuery)) {
			score += 44;
		}
		if (tokens.length > 1 && matchedTokens === tokens.length) {
			score += 36;
		}
		score += matchedTokens * 8;
		return score;
	}

	function compareTasksBySortColumn(
		left: DisplayTask,
		right: DisplayTask,
		column: SprintSortColumn
	): number {
		if (column === 'title') {
			return taskSortCollator.compare(left.title, right.title);
		}
		if (column === 'status') {
			const statusRank = {
				todo: 0,
				in_progress: 1,
				done: 2
			} as const;
			return statusRank[resolveColumn(left.status)] - statusRank[resolveColumn(right.status)];
		}
		if (column === 'owner') {
			return taskSortCollator.compare(ownerLabel(left), ownerLabel(right));
		}
		if (column === 'budget') {
			const leftBudget =
				typeof left.budget === 'number' && Number.isFinite(left.budget) ? left.budget : -1;
			const rightBudget =
				typeof right.budget === 'number' && Number.isFinite(right.budget) ? right.budget : -1;
			return leftBudget - rightBudget;
		}
		if (column === 'spent') {
			const leftSpent =
				typeof left.spent === 'number' && Number.isFinite(left.spent) ? left.spent : -1;
			const rightSpent =
				typeof right.spent === 'number' && Number.isFinite(right.spent) ? right.spent : -1;
			return leftSpent - rightSpent;
		}
		return (left.updatedAt || 0) - (right.updatedAt || 0);
	}

	function compareTasksBySortState(
		left: DisplayTask,
		right: DisplayTask,
		sortState?: SprintSortState
	): number {
		if (!sortState) {
			return 0;
		}
		const diff = compareTasksBySortColumn(left, right, sortState.column);
		if (diff === 0) {
			return 0;
		}
		return sortState.direction === 'asc' ? diff : -diff;
	}

	function sortTasksForSprintGroup(tasks: DisplayTask[], sortState?: SprintSortState) {
		return [...tasks].sort(
			(left, right) =>
				compareTasksBySortState(left, right, sortState) || compareTasksForGrid(left, right)
		);
	}

	function toggleSprintSort(groupKey: string, column: SprintSortColumn) {
		const current = sprintSortStateByKey[groupKey];
		const nextDirection: SprintSortDirection =
			current?.column === column && current.direction === 'asc' ? 'desc' : 'asc';
		sprintSortStateByKey = {
			...sprintSortStateByKey,
			[groupKey]: {
				column,
				direction: nextDirection
			}
		};
	}

	function sprintSortDirection(
		groupKey: string,
		column: SprintSortColumn
	): SprintSortDirection | '' {
		const current = sprintSortStateByKey[groupKey];
		if (!current || current.column !== column) {
			return '';
		}
		return current.direction;
	}

	function sprintSortIcon(groupKey: string, column: SprintSortColumn) {
		const direction = sprintSortDirection(groupKey, column);
		if (direction === 'asc') {
			return '↑';
		}
		if (direction === 'desc') {
			return '↓';
		}
		return '↕';
	}

	function dedupeDisplayTasksById(tasks: DisplayTask[]) {
		const taskById = new Map<string, DisplayTask>();
		for (const task of tasks) {
			const taskId = toStringValue(task.id).trim();
			if (!taskId) {
				continue;
			}
			const existing = taskById.get(taskId);
			if (!existing || (task.updatedAt || 0) >= (existing.updatedAt || 0)) {
				taskById.set(taskId, task);
			}
		}
		return [...taskById.values()];
	}

	function statusLabel(column: ColumnKey) {
		if (column === 'in_progress') return 'Working on it';
		if (column === 'done') return 'Done';
		return 'To Do';
	}

	function resolveColumn(statusValue: string): ColumnKey {
		const normalized = toStringValue(statusValue).toLowerCase().replace(/\s+/g, '_');
		if (normalized === 'in_progress') {
			return 'in_progress';
		}
		if (normalized === 'done' || normalized === 'completed') {
			return 'done';
		}
		return 'todo';
	}

	function parseTimestamp(value: unknown) {
		if (typeof value === 'number' && Number.isFinite(value)) {
			return value;
		}
		if (typeof value === 'string') {
			const parsed = Date.parse(value);
			if (Number.isFinite(parsed)) {
				return parsed;
			}
		}
		return Date.now();
	}

	function parseBudgetValue(value: unknown): number | undefined {
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

	function parseBudgetFromDescription(description: string): number | undefined {
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
		return parseBudgetValue(budgetMatch[1]);
	}

	function parseSpentFromDescription(description: string): number | undefined {
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
		return parseBudgetValue(spentMatch[1]);
	}

	type DescriptionMetadataEntry = {
		key: string;
		label: string;
		value: string;
	};

	function parseDescriptionMetadata(description: string): {
		base: string;
		entries: DescriptionMetadataEntry[];
	} {
		const trimmed = description.trim();
		if (!trimmed) {
			return { base: '', entries: [] };
		}
		const metadataMatch = trimmed.match(/\[([^\]]+)\]\s*$/);
		if (!metadataMatch) {
			return { base: trimmed, entries: [] };
		}
		const base = trimmed.slice(0, metadataMatch.index).trim();
		const metadataBody = (metadataMatch[1] ?? '').trim();
		if (!metadataBody || !metadataBody.includes(':')) {
			return { base: trimmed, entries: [] };
		}
		const entries: DescriptionMetadataEntry[] = [];
		for (const section of metadataBody.split('|')) {
			const raw = section.trim();
			if (!raw) {
				continue;
			}
			const [rawLabel, ...rawValueParts] = raw.split(':');
			const label = rawLabel.trim();
			const value = rawValueParts.join(':').trim();
			if (!label || !value) {
				continue;
			}
			entries.push({
				key: label.toLowerCase(),
				label,
				value
			});
		}
		return { base, entries };
	}

	function readDescriptionMetadataValue(description: string, key: string) {
		const normalizedKey = key.trim().toLowerCase();
		if (!normalizedKey) {
			return '';
		}
		const metadata = parseDescriptionMetadata(description);
		const found = metadata.entries.find((entry) => entry.key === normalizedKey);
		return found?.value.trim() || '';
	}

	function sprintGroupKey(value: string) {
		// Collapse all whitespace runs to a single space so that "Sprint 1",
		// "sprint 1", "sprint  1" all share one group. "sprint1" (no space)
		// is handled by the backend canonicalization before storage.
		const normalized = value.trim().toLowerCase().replace(/\s+/g, ' ');
		if (!normalized) {
			return 'backlog';
		}
		return normalized;
	}

	function formatVisibleSprintName(name: string, sprintNumber: number) {
		const trimmed = name.trim() || `Sprint ${sprintNumber}`;
		if (sprintGroupKey(trimmed) === 'backlog') {
			return 'Backlog';
		}
		return `${sprintNumber}. ${trimmed.replace(/^\d+\.\s*/, '')}`;
	}

	function buildSprintDisplayNameMap(groups: SprintTaskGroup[]) {
		const labels = new Map<string, string>();
		let sprintNumber = 1;
		for (const group of groups) {
			if (group.key === 'backlog') {
				labels.set(group.key, 'Backlog');
				continue;
			}
			labels.set(group.key, formatVisibleSprintName(group.name, sprintNumber));
			sprintNumber += 1;
		}
		return labels;
	}

	function formatSprintDisplayName(name: string) {
		const trimmed = name.trim() || 'Backlog';
		const key = sprintGroupKey(trimmed);
		return sprintDisplayNameByKey.get(key) ?? (key === 'backlog' ? 'Backlog' : trimmed);
	}

	function setSprintDraftGroupsForContext(contextValue: string, nextGroups: string[]) {
		const contextLabel = contextValue.trim();
		if (!contextLabel) {
			return;
		}
		const seen = new Set<string>();
		const sanitizedGroups: string[] = [];
		for (const groupName of nextGroups) {
			const trimmedGroupName = groupName.trim();
			if (!trimmedGroupName) {
				continue;
			}
			const key = sprintGroupKey(trimmedGroupName);
			if (key === 'backlog' || seen.has(key)) {
				continue;
			}
			seen.add(key);
			sanitizedGroups.push(trimmedGroupName);
		}
		const currentGroups = sprintDraftGroupsByContext[contextLabel] ?? [];
		if (
			currentGroups.length === sanitizedGroups.length &&
			currentGroups.every((groupName, index) => groupName === sanitizedGroups[index])
		) {
			return;
		}
		sprintDraftGroupsByContext = {
			...sprintDraftGroupsByContext,
			[contextLabel]: sanitizedGroups
		};
	}

	function rememberSprintDraftGroup(sprintName: string) {
		const trimmedSprintName = sprintName.trim();
		if (!trimmedSprintName || sprintGroupKey(trimmedSprintName) === 'backlog') {
			return;
		}
		const key = sprintGroupKey(trimmedSprintName);
		const alreadyExists = sprintTaskGroups.some((group) => group.key === key);
		if (alreadyExists) {
			return;
		}
		setSprintDraftGroupsForContext(sprintContextKey, [...sprintDraftGroups, trimmedSprintName]);
	}

	function parseSprintComposerTasks(input: string | string[]) {
		const seen = new Set<string>();
		const next: string[] = [];
		const lines = Array.isArray(input) ? input : input.split(/\r?\n/);
		for (const line of lines) {
			const fragments = line.split('|');
			for (const fragment of fragments) {
				const cleaned = fragment
					.replace(/^[\-*•]\s*/, '')
					.replace(/^\d+[.)]\s*/, '')
					.trim();
				if (!cleaned) {
					continue;
				}
				const key = cleaned.toLowerCase();
				if (seen.has(key)) {
					continue;
				}
				seen.add(key);
				next.push(cleaned);
				if (next.length >= SPRINT_COMPOSER_MAX_TASKS) {
					return next;
				}
			}
		}
		return next;
	}

	function buildSprintComposerPreviewRows(taskDrafts: string[], rowMeta: Record<number, SprintComposerMeta>, activeIndex: number) {
		const normalizedDrafts = parseSprintComposerTasks(taskDrafts);
		// Always show at least 1 row; grow with committed drafts; also show the row being actively edited
		const count = Math.max(1, normalizedDrafts.length, activeIndex >= 0 ? activeIndex + 1 : 0);
		return Array.from({ length: count }, (_, index) => ({
			index,
			title: normalizedDrafts[index] ?? '',
			status: rowMeta[index]?.status ?? 'todo',
			assigneeId: rowMeta[index]?.assigneeId ?? '',
			budget: rowMeta[index]?.budget ?? '',
			spent: rowMeta[index]?.spent ?? ''
		}));
	}

	function addSprintComposerRow() {
		if (sprintComposerSaving) return;
		// Commit any in-progress title edit first
		if (sprintComposerActiveTaskIndex >= 0) commitSprintComposerTaskEdit();
		const drafts = parseSprintComposerTasks(sprintComposerTaskDrafts);
		if (drafts.length >= SPRINT_COMPOSER_MAX_TASKS) return;
		// Add blank slot by appending a placeholder then immediately starting its edit
		const nextIndex = drafts.length;
		void startSprintComposerTaskEdit(nextIndex);
	}

	function setSprintComposerMeta(index: number, field: keyof SprintComposerMeta, value: string) {
		const current = sprintComposerRowMeta[index] ?? { status: 'todo', assigneeId: '', budget: '', spent: '' };
		sprintComposerRowMeta = { ...sprintComposerRowMeta, [index]: { ...current, [field]: value } };
	}

	async function startSprintComposerTaskEdit(index: number, prefill = '') {
		if (sprintComposerSaving) {
			return;
		}
		if (index < 0 || index >= SPRINT_COMPOSER_MAX_TASKS) {
			return;
		}
		const normalizedDrafts = parseSprintComposerTasks(sprintComposerTaskDrafts);
		if (index > normalizedDrafts.length) {
			return;
		}
		const existingValue = normalizedDrafts[index] ?? '';
		sprintComposerActiveTaskIndex = index;
		sprintComposerTaskInputValue = prefill || existingValue;
		await tick();
		sprintComposerTaskInput?.focus();
		sprintComposerTaskInput?.setSelectionRange(
			sprintComposerTaskInputValue.length,
			sprintComposerTaskInputValue.length
		);
	}

	function cancelSprintComposerTaskEdit() {
		sprintComposerActiveTaskIndex = -1;
		sprintComposerTaskInputValue = '';
	}

	function commitSprintComposerTaskEdit() {
		if (sprintComposerActiveTaskIndex < 0) {
			return;
		}
		const normalizedDrafts = parseSprintComposerTasks(sprintComposerTaskDrafts);
		const nextDrafts = [...normalizedDrafts];
		const normalizedInput = parseSprintComposerTasks([sprintComposerTaskInputValue])[0] ?? '';
		if (sprintComposerActiveTaskIndex < nextDrafts.length) {
			if (normalizedInput) {
				nextDrafts[sprintComposerActiveTaskIndex] = normalizedInput;
			} else {
				nextDrafts.splice(sprintComposerActiveTaskIndex, 1);
			}
		} else if (normalizedInput && nextDrafts.length < SPRINT_COMPOSER_MAX_TASKS) {
			nextDrafts.push(normalizedInput);
		}
		sprintComposerTaskDrafts = parseSprintComposerTasks(nextDrafts);
		cancelSprintComposerTaskEdit();
	}

	function onSprintComposerTaskInputKeydown(event: KeyboardEvent) {
		if (event.key === 'Enter') {
			event.preventDefault();
			commitSprintComposerTaskEdit();
			return;
		}
		if (event.key === 'Escape') {
			event.preventDefault();
			cancelSprintComposerTaskEdit();
		}
	}

	async function openSprintComposer(prefill?: { sprintName?: string; taskLine?: string }) {
		if (!canEdit || !canCreateSprintTask || sprintComposerSaving) {
			return;
		}
		let prefillTaskEditIndex = -1;
		let prefillTaskValue = '';
		const nextSprintName = prefill?.sprintName?.trim();
		if (nextSprintName) {
			sprintComposerName = nextSprintName;
		}
		const taskLine = prefill?.taskLine?.trim();
		if (taskLine) {
			const normalizedTaskLine = parseSprintComposerTasks([taskLine])[0] ?? '';
			if (normalizedTaskLine) {
				const existingDrafts = parseSprintComposerTasks(sprintComposerTaskDrafts);
				if (existingDrafts.length < SPRINT_COMPOSER_MAX_TASKS) {
					prefillTaskEditIndex = existingDrafts.length;
					prefillTaskValue = normalizedTaskLine;
				}
			}
		}
		sprintComposerOpen = true;
		clearBoardError();
		await tick();
		if (prefillTaskEditIndex >= 0) {
			await startSprintComposerTaskEdit(prefillTaskEditIndex, prefillTaskValue);
			return;
		}
		sprintComposerNameInput?.focus();
		sprintComposerNameInput?.setSelectionRange(
			sprintComposerName.length,
			sprintComposerName.length
		);
	}

	function closeSprintComposer() {
		if (sprintComposerSaving) {
			return;
		}
		sprintComposerOpen = false;
		sprintComposerName = '';
		sprintComposerTaskDrafts = [];
		sprintComposerTaskInputValue = '';
		sprintComposerActiveTaskIndex = -1;
		sprintComposerRowMeta = {};
	}

	function readSupportMetadataValue(task: DisplayTask, keys: string[]) {
		for (const key of keys) {
			const value = readDescriptionMetadataValue(task.description, key).trim();
			if (value) {
				return value;
			}
		}
		return '';
	}

	function parseSupportPriority(value: string): SupportPriority {
		const normalized = value.trim().toLowerCase();
		if (normalized === 'critical') return 'critical';
		if (normalized === 'high') return 'high';
		if (normalized === 'low') return 'low';
		return 'medium';
	}

	function supportPriorityRank(value: SupportPriority): number {
		if (value === 'critical') return 0;
		if (value === 'high') return 1;
		if (value === 'medium') return 2;
		return 3;
	}

	function supportPriorityLabel(value: SupportPriority): string {
		if (value === 'critical') return 'Critical';
		if (value === 'high') return 'High';
		if (value === 'low') return 'Low';
		return 'Medium';
	}

	function splitMetadataList(value: string) {
		const seen = new Set<string>();
		const next: string[] = [];
		for (const entry of value
			.split(/[;,]/)
			.map((item) => item.trim())
			.filter(Boolean)) {
			const normalized = entry.toLowerCase();
			if (seen.has(normalized)) {
				continue;
			}
			seen.add(normalized);
			next.push(entry);
		}
		return next;
	}

	function isSupportTicket(task: DisplayTask) {
		if (task.taskType === 'support') {
			return true;
		}
		const ticketType = readSupportMetadataValue(task, ['ticket', 'type']).toLowerCase();
		if (
			ticketType === 'support' ||
			ticketType === 'support_ticket' ||
			ticketType === 'support ticket'
		) {
			return true;
		}
		return task.title.trim().toLowerCase().startsWith('support:');
	}

	function resolveSupportCurrentSprintName(tasks: DisplayTask[]) {
		const activeSprintTask = tasks.find(
			(task) =>
				resolveColumn(task.status) === 'in_progress' &&
				sprintGroupKey(task.sprintName) !== 'backlog'
		);
		if (activeSprintTask) {
			return activeSprintTask.sprintName.trim() || 'Backlog';
		}
		const recentSprintTask = [...tasks]
			.sort((left, right) => right.updatedAt - left.updatedAt)
			.find((task) => sprintGroupKey(task.sprintName) !== 'backlog');
		if (recentSprintTask) {
			return recentSprintTask.sprintName.trim() || 'Backlog';
		}
		return 'Backlog';
	}

	function buildSupportTicketCard(task: DisplayTask): SupportTicketCard {
		const priority = parseSupportPriority(readSupportMetadataValue(task, ['priority']));
		const concernedIds = splitMetadataList(
			readSupportMetadataValue(task, ['concerned', 'concerned users'])
		);
		const linkedTaskIds = splitMetadataList(
			readSupportMetadataValue(task, ['linked tasks', 'linked_tasks', 'linked task ids'])
		);
		const details = parseDescriptionMetadata(task.description).base || 'No details provided';
		return {
			task,
			priority,
			concernedIds,
			linkedTaskIds,
			details
		};
	}

	function upsertDescriptionMetadataValue(
		description: string,
		key: string,
		label: string,
		nextValue: string
	) {
		const normalizedKey = key.trim().toLowerCase();
		const normalizedLabel = label.trim() || key.trim();
		const next = nextValue.trim();
		const metadata = parseDescriptionMetadata(description);
		const filtered = metadata.entries.filter((entry) => entry.key !== normalizedKey);
		if (next) {
			filtered.push({
				key: normalizedKey,
				label: normalizedLabel,
				value: next
			});
		}
		if (filtered.length === 0) {
			return metadata.base;
		}
		const metadataBlock = `[${filtered
			.map((entry) => `${entry.label}: ${entry.value}`)
			.join(' | ')}]`;
		if (!metadata.base) {
			return metadataBlock;
		}
		return `${metadata.base}\n\n${metadataBlock}`;
	}

	function replaceDescriptionBase(description: string, nextBase: string) {
		const metadata = parseDescriptionMetadata(description);
		const trimmedBase = nextBase.trim();
		if (metadata.entries.length === 0) {
			return trimmedBase;
		}
		const metadataBlock = `[${metadata.entries
			.map((entry) => `${entry.label}: ${entry.value}`)
			.join(' | ')}]`;
		if (!trimmedBase) {
			return metadataBlock;
		}
		return `${trimmedBase}\n\n${metadataBlock}`;
	}

	function readTaskCustomFieldText(task: DisplayTask, key: string) {
		return toStringValue(task.customFields?.[key]).trim();
	}

	function readTaskCustomFieldSteps(task: DisplayTask) {
		const rawValue = task.customFields?.[TASK_DETAIL_STEPS_FIELD_KEY];
		if (Array.isArray(rawValue)) {
			return rawValue
				.map((entry) => toStringValue(entry).trim())
				.filter(Boolean);
		}
		if (typeof rawValue === 'string') {
			try {
				const parsed = JSON.parse(rawValue);
				if (Array.isArray(parsed)) {
					return parsed
						.map((entry) => toStringValue(entry).trim())
						.filter(Boolean);
				}
			} catch {
				return rawValue
					.split(/\r?\n+/)
					.map((entry) => entry.replace(/^[\-\d.)\s]+/, '').trim())
					.filter(Boolean);
			}
		}
		return [];
	}

	function readTaskDescriptionBase(task: DisplayTask) {
		return parseDescriptionMetadata(task.description).base || '';
	}

	function syncTaskModalFromTask(task: DisplayTask, options?: { preserveCoreValues?: boolean }) {
		taskEditModal = task;
		if (!options?.preserveCoreValues) {
			taskEditVals = {
				title: task.title || '',
				description: readTaskDescriptionBase(task),
				notes: readTaskCustomFieldText(task, TASK_NOTES_FIELD_KEY),
				assigneeId: task.assigneeId || '',
				status: resolveColumn(task.status) || 'todo',
				budget: task.budget != null ? String(task.budget) : '',
				spent: task.spent != null ? String(task.spent) : '',
				dueDate: msToDateInput(task.dueDate),
				startDate: msToDateInput(task.startDate)
			};
		} else {
			taskEditVals = {
				...taskEditVals,
				description: readTaskDescriptionBase(task),
				notes: readTaskCustomFieldText(task, TASK_NOTES_FIELD_KEY)
			};
		}
		taskDetailSummary = readTaskCustomFieldText(task, TASK_DETAIL_SUMMARY_FIELD_KEY);
		taskDetailSteps = readTaskCustomFieldSteps(task);
		taskDetailGeneratedAt = readTaskCustomFieldText(task, TASK_DETAIL_GENERATED_AT_FIELD_KEY);
		taskDetailError = '';
		taskDetailNotice = '';
	}

	function hasGeneratedTaskDetails(task: DisplayTask) {
		return (
			readTaskCustomFieldText(task, TASK_DETAIL_SUMMARY_FIELD_KEY).length > 0 ||
			readTaskCustomFieldSteps(task).length > 0
		);
	}

	function formatTaskDetailGeneratedAt(value: string) {
		const trimmed = value.trim();
		if (!trimmed) {
			return '';
		}
		const parsed = new Date(trimmed);
		if (Number.isNaN(parsed.getTime())) {
			return trimmed;
		}
		return parsed.toLocaleString(undefined, {
			month: 'short',
			day: 'numeric',
			hour: 'numeric',
			minute: '2-digit'
		});
	}

	function isLikelyUUID(value: string) {
		const trimmed = value.trim();
		if (!trimmed) {
			return false;
		}
		const normalized = trimmed.replace(/_/g, '-');
		return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(normalized);
	}

	function parseTaskCustomFieldsValue(value: unknown) {
		if (typeof value === 'string') {
			const trimmed = value.trim();
			if (!trimmed) {
				return {} as Record<string, unknown>;
			}
			try {
				return parseTaskCustomFieldsValue(JSON.parse(trimmed));
			} catch {
				return {} as Record<string, unknown>;
			}
		}
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return {} as Record<string, unknown>;
		}
		const normalized: Record<string, unknown> = {};
		for (const [rawKey, rawValue] of Object.entries(value)) {
			const key = toStringValue(rawKey).trim();
			if (!key) {
				continue;
			}
			normalized[key] = rawValue;
		}
		return normalized;
	}

	function normalizeCustomFieldType(fieldType: unknown) {
		return toStringValue(fieldType).trim().toLowerCase();
	}

	function cloneCustomFieldMap(source: Record<string, unknown>) {
		const cloned: Record<string, unknown> = {};
		for (const [key, value] of Object.entries(source ?? {})) {
			if (Array.isArray(value)) {
				cloned[key] = [...value];
				continue;
			}
			cloned[key] = value;
		}
		return cloned;
	}

	function normalizeTaskRelationIdentifier(value: unknown) {
		return toStringValue(value).trim();
	}

	function normalizeTaskRelationIdentifiers(value: unknown) {
		if (!Array.isArray(value)) {
			return [] as string[];
		}
		const seen = new Set<string>();
		const next: string[] = [];
		for (const entry of value) {
			const relationID = normalizeTaskRelationIdentifier(entry);
			if (!relationID || seen.has(relationID)) {
				continue;
			}
			seen.add(relationID);
			next.push(relationID);
		}
		return next;
	}

	function normalizeDisplaySubtasks(value: unknown) {
		if (!Array.isArray(value)) {
			return [] as DisplaySubtask[];
		}
		const seen = new Set<string>();
		const next: DisplaySubtask[] = [];
		for (const entry of value) {
			if (!entry || typeof entry !== 'object' || Array.isArray(entry)) {
				continue;
			}
			const source = entry as Record<string, unknown>;
			const id = normalizeTaskRelationIdentifier(
				source.id ?? source.subtask_id ?? source.subtaskId
			);
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

	function cloneDisplaySubtasks(subtasks: DisplaySubtask[]) {
		return subtasks.map((subtask) => ({
			id: subtask.id,
			content: subtask.content,
			completed: subtask.completed,
			position: subtask.position
		}));
	}

	function calculateSubtaskCompletionPercent(subtasks: DisplaySubtask[]) {
		if (subtasks.length === 0) {
			return undefined;
		}
		const completedCount = subtasks.filter((subtask) => subtask.completed).length;
		return Math.round((completedCount / subtasks.length) * 100);
	}

	function normalizeCustomFieldPatchValue(schema: FieldSchema, value: unknown) {
		const fieldType = normalizeCustomFieldType(schema.fieldType);
		if (fieldType === 'checkbox') {
			return Boolean(value);
		}
		if (fieldType === 'number') {
			const parsed = parseBudgetValue(toStringValue(value));
			if (parsed == null) {
				if (toStringValue(value).trim()) {
					throw new Error(`${schema.name} must be a valid number.`);
				}
				return null;
			}
			return parsed;
		}
		if (fieldType === 'multi_select') {
			const list = Array.isArray(value)
				? value.map((entry) => toStringValue(entry).trim()).filter(Boolean)
				: toStringValue(value)
						.split(',')
						.map((entry) => entry.trim())
						.filter(Boolean);
			const unique = [...new Set(list)];
			if (schema.options && schema.options.length > 0) {
				const allowed = new Set(schema.options.map((option) => option.toLowerCase()));
				for (const option of unique) {
					if (!allowed.has(option.toLowerCase())) {
						throw new Error(`"${option}" is not a valid option for ${schema.name}.`);
					}
				}
			}
			return unique.length > 0 ? unique : null;
		}

		const normalized = toStringValue(value).trim();
		if (!normalized) {
			return null;
		}
		if (fieldType === 'select' && schema.options && schema.options.length > 0) {
			const allowed = new Set(schema.options.map((option) => option.toLowerCase()));
			if (!allowed.has(normalized.toLowerCase())) {
				throw new Error(`"${normalized}" is not a valid option for ${schema.name}.`);
			}
		}
		if (fieldType === 'url') {
			try {
				const parsed = new URL(normalized);
				if (!parsed.protocol.startsWith('http')) {
					throw new Error('Invalid URL protocol');
				}
			} catch {
				throw new Error(`${schema.name} must be a valid URL.`);
			}
		}
		if (fieldType === 'date') {
			const parsedDate = new Date(normalized);
			if (Number.isNaN(parsedDate.getTime())) {
				throw new Error(`${schema.name} must be a valid date.`);
			}
		}
		return normalized;
	}

	function customFieldValueSignature(value: unknown) {
		if (Array.isArray(value)) {
			return JSON.stringify(value.map((entry) => toStringValue(entry).trim()).filter(Boolean));
		}
		if (value == null) {
			return '';
		}
		if (typeof value === 'object') {
			try {
				return JSON.stringify(value);
			} catch {
				return '';
			}
		}
		return toStringValue(value).trim();
	}

	function buildTaskCustomFieldPatch() {
		const patch: Record<string, unknown> = {};
		for (const schema of roomFieldSchemas) {
			const fieldID = schema.fieldId;
			const baselineValue = editingCustomFieldsBaseline[fieldID];
			const nextRawValue = editingCustomFields[fieldID];
			const nextValue = normalizeCustomFieldPatchValue(schema, nextRawValue);
			if (customFieldValueSignature(baselineValue) === customFieldValueSignature(nextValue)) {
				continue;
			}
			patch[fieldID] = nextValue;
		}
		return patch;
	}

	function applyCustomFieldPatch(
		base: Record<string, unknown>,
		patch: Record<string, unknown>
	): Record<string, unknown> {
		const next = cloneCustomFieldMap(base);
		for (const [fieldID, value] of Object.entries(patch)) {
			if (value == null || value === '' || (Array.isArray(value) && value.length === 0)) {
				delete next[fieldID];
				continue;
			}
			next[fieldID] = value;
		}
		return next;
	}

	function isCustomFieldOptionSelected(fieldID: string, option: string) {
		const current = editingCustomFields[fieldID];
		if (!Array.isArray(current)) {
			return false;
		}
		return current.some(
			(entry) => toStringValue(entry).trim().toLowerCase() === option.trim().toLowerCase()
		);
	}

	function toggleCustomFieldOption(fieldID: string, option: string, checked: boolean) {
		const current = Array.isArray(editingCustomFields[fieldID])
			? editingCustomFields[fieldID].map((entry) => toStringValue(entry).trim()).filter(Boolean)
			: [];
		const normalizedOption = option.trim();
		const nextSet = new Set(current.map((entry) => entry.toLowerCase()));
		if (checked) {
			nextSet.add(normalizedOption.toLowerCase());
		} else {
			nextSet.delete(normalizedOption.toLowerCase());
		}
		const ordered = [
			...new Set(
				checked
					? [...current, normalizedOption]
					: current.filter((entry) => entry.toLowerCase() !== normalizedOption.toLowerCase())
			)
		];
		const next = ordered.filter((entry) => nextSet.has(entry.toLowerCase()));
		editingCustomFields = {
			...editingCustomFields,
			[fieldID]: next
		};
	}

	function normalizePersonalItem(raw: unknown): DisplayTask | null {
		if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
			return null;
		}
		const source = raw as PersonalItemResponse;
		const itemID = toStringValue(source.item_id);
		const title = toStringValue(source.title);
		const content = toStringValue(source.content);
		const description = toStringValue(source.description);
		const displayTitle = title || content || description;
		if (!itemID || !displayTitle) {
			return null;
		}
		const createdAt = parseTimestamp(source.created_at);
		return {
			id: itemID,
			roomId: '',
			title: displayTitle,
			description: description || (content !== displayTitle ? content : ''),
			status: toStringValue(source.status) || 'pending',
			taskType: 'sprint',
			customFields: {},
			blockedBy: [],
			blocks: [],
			subtasks: [],
			completionPercent: undefined,
			budget: undefined,
			spent: undefined,
			sprintName: '',
			assigneeId: '',
			createdAt,
			updatedAt: parseTimestamp(source.updated_at) || createdAt,
			source: 'personal'
		};
	}

	function normalizeRoomTask(raw: unknown): DisplayTask | null {
		if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
			return null;
		}
		const source = raw as RoomTaskResponse;
		const taskID = toStringValue(source.id);
		if (!taskID) {
			return null;
		}
		const createdAt = parseTimestamp(source.created_at ?? source.createdAt);
		const description = toStringValue(source.description);
		const budget =
			parseBudgetValue(source.budget ?? source.task_budget ?? source.taskBudget) ??
			parseBudgetFromDescription(description);
		const spent =
			parseBudgetValue(
				source.actual_cost ??
					source.actualCost ??
					source.spent ??
					source.spent_cost ??
					source.spentCost
			) ?? parseSpentFromDescription(description);
		const blockedBy = normalizeTaskRelationIdentifiers(source.blocked_by ?? source.blockedBy);
		const blocks = normalizeTaskRelationIdentifiers(source.blocks);
		const subtasks = normalizeDisplaySubtasks(source.subtasks);
		const completionPercent =
			parseBudgetValue(source.completion_percent ?? source.completionPercent) ??
			calculateSubtaskCompletionPercent(subtasks);
		const taskTypeRaw = toStringValue(source.task_type ?? source.taskType)
			.trim()
			.toLowerCase();
		const dueDate = parseTimestamp(source.due_date ?? source.dueDate) || undefined;
		const startDate = parseTimestamp(source.start_date ?? source.startDate) || undefined;
		const rolesRaw = source.roles;
		const roles: TaskRole[] | undefined = (() => {
			if (!rolesRaw) return undefined;
			if (Array.isArray(rolesRaw)) return rolesRaw as TaskRole[];
			if (typeof rolesRaw === 'string') { try { return JSON.parse(rolesRaw) as TaskRole[]; } catch { return undefined; } }
			return undefined;
		})();
		return {
			id: taskID,
			roomId: normalizeRoomIDValue(normalizedRoomId || $activeContext.id),
			title: toStringValue(source.title) || 'Untitled Task',
			description,
			status: toStringValue(source.status) || 'todo',
			taskType: taskTypeRaw === 'support' ? 'support' : 'sprint',
			customFields: parseTaskCustomFieldsValue(source.custom_fields ?? source.customFields),
			blockedBy,
			blocks,
			subtasks,
			completionPercent,
			budget,
			spent,
			dueDate: dueDate && dueDate > 0 ? dueDate : undefined,
			startDate: startDate && startDate > 0 ? startDate : undefined,
			roles: roles && roles.length > 0 ? roles : undefined,
			sprintName: toStringValue(source.sprint_name ?? source.sprintName),
			assigneeId: toStringValue(source.assignee_id ?? source.assigneeId),
			statusActorId: toStringValue(source.status_actor_id ?? source.statusActorId) || undefined,
			statusActorName:
				toStringValue(source.status_actor_name ?? source.statusActorName) || undefined,
			statusChangedAt: parseTimestamp(source.status_changed_at ?? source.statusChangedAt),
			createdAt,
			updatedAt: parseTimestamp(source.updated_at ?? source.updatedAt) || createdAt,
			source: 'room'
		};
	}

	async function parseErrorMessage(response: Response) {
		if (response.status === 507) {
			showBoardToast(STORAGE_FULL_UPLOAD_MESSAGE);
			return STORAGE_FULL_UPLOAD_MESSAGE;
		}
		const payload = (await response.json().catch(() => null)) as {
			error?: string;
			message?: string;
		} | null;
		return payload?.error?.trim() || payload?.message?.trim() || `HTTP ${response.status}`;
	}

	function showBoardToast(message: string) {
		const normalizedMessage = message.trim();
		if (!normalizedMessage) {
			return;
		}
		boardToastMessage = normalizedMessage;
		if (boardToastTimer) {
			clearTimeout(boardToastTimer);
		}
		boardToastTimer = setTimeout(() => {
			boardToastMessage = '';
			boardToastTimer = null;
		}, 3500);
	}

	function clearBoardError() {
		if (contextAware) {
			contextError = '';
			return;
		}
		roomBoardError = '';
	}

	function setBoardError(message: string) {
		if (contextAware) {
			contextError = message;
			return;
		}
		roomBoardError = message;
	}

	function publishRoomBoardActivity(roomID: string, event: BoardActivityInput) {
		const normalizedRoomID = normalizeRoomIDValue(roomID);
		const fullEvent = addBoardActivity(event);
		if (normalizedRoomID) {
			sendSocketPayload(buildBoardActivitySocketPayload(normalizedRoomID, fullEvent));
		}
		return fullEvent;
	}

	async function loadContextTasks() {
		if (!contextAware) {
			return;
		}

		contextLoadToken += 1;
		const loadToken = contextLoadToken;
		contextLoading = true;
		contextError = '';
		try {
			let endpoint = '';
			let normalizeRow: (raw: unknown) => DisplayTask | null = normalizeRoomTask;
			if ($activeContext.type === 'personal') {
				endpoint = `${API_BASE}/api/personal/items`;
				normalizeRow = normalizePersonalItem;
			} else {
				const normalizedWorkspaceRoomID = normalizeRoomIDValue($activeContext.id);
				if (!normalizedWorkspaceRoomID) {
					contextTasks = [];
					return;
				}
				endpoint = `${API_BASE}/api/rooms/${encodeURIComponent(normalizedWorkspaceRoomID)}/tasks`;
			}

			const response = await fetch(endpoint, {
				method: 'GET',
				credentials: 'include',
				headers: withSessionUserHeaders()
			});
			if (!response.ok) {
				throw new Error(await parseErrorMessage(response));
			}
			const payload = (await response.json().catch(() => [])) as unknown;
			const records = Array.isArray(payload) ? payload : [];
			const normalized = records
				.map((record) => normalizeRow(record))
				.filter((record): record is DisplayTask => Boolean(record));
			if (loadToken !== contextLoadToken) {
				return;
			}
			contextTasks = normalized;
		} catch (error) {
			if (loadToken !== contextLoadToken) {
				return;
			}
			contextTasks = [];
			contextError = error instanceof Error ? error.message : 'Failed to load tasks';
		} finally {
			if (loadToken === contextLoadToken) {
				contextLoading = false;
			}
		}
	}

	function formatContextStatusForPersonal(column: ColumnKey) {
		if (column === 'done') {
			return 'completed';
		}
		if (column === 'in_progress') {
			return 'in_progress';
		}
		return 'pending';
	}

	async function persistContextTaskStatus(
		taskID: string,
		columnKey: ColumnKey
	): Promise<StatusUpdateMetadata | null> {
		if ($activeContext.type === 'personal') {
			const response = await fetch(
				`${API_BASE}/api/personal/items/${encodeURIComponent(taskID)}/status`,
				{
					method: 'PUT',
					headers: { 'Content-Type': 'application/json' },
					credentials: 'include',
					body: JSON.stringify({
						status: formatContextStatusForPersonal(columnKey)
					})
				}
			);
			if (!response.ok) {
				throw new Error(await parseErrorMessage(response));
			}
			return null;
		}

		const normalizedWorkspaceRoomID = normalizeRoomIDValue($activeContext.id);
		if (!normalizedWorkspaceRoomID) {
			throw new Error('Invalid workspace room id');
		}
		const response = await fetch(
			`${API_BASE}/api/rooms/${encodeURIComponent(normalizedWorkspaceRoomID)}/tasks/${encodeURIComponent(taskID)}/status`,
			{
				method: 'PUT',
				headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
				credentials: 'include',
				body: JSON.stringify({ status: columnKey })
			}
		);
		if (!response.ok) {
			throw new Error(await parseErrorMessage(response));
		}
		const payload = (await response.json().catch(() => null)) as unknown;
		return parseStatusUpdateMetadata(payload, columnKey);
	}

	async function moveContextTaskToColumn(taskID: string, columnKey: ColumnKey) {
		if (!canEdit) {
			return;
		}
		const targetTask = contextTasks.find((task) => task.id === taskID);
		if (!targetTask) {
			return;
		}

		const previousStatus = targetTask.status;
		const previousColumn = resolveColumn(previousStatus);
		if (previousColumn === columnKey) {
			return;
		}

		contextTasks = contextTasks.map((task) =>
			task.id === taskID
				? {
						...task,
						status:
							$activeContext.type === 'personal'
								? formatContextStatusForPersonal(columnKey)
								: columnKey,
						updatedAt: Date.now()
					}
				: task
		);

		try {
			const statusMeta = await persistContextTaskStatus(taskID, columnKey);
			if ($activeContext.type === 'room') {
				const normalizedWorkspaceRoomID = normalizeRoomIDValue($activeContext.id);
				contextTasks = contextTasks.map((task) => {
					if (task.id !== taskID) {
						return task;
					}
					return {
						...task,
						status: statusMeta?.status || columnKey,
						statusActorId: statusMeta?.statusActorId || sessionUserID || task.statusActorId,
						statusActorName: statusMeta?.statusActorName || sessionUsername || task.statusActorName,
						statusChangedAt: statusMeta?.statusChangedAt || Date.now(),
						updatedAt: statusMeta?.updatedAt || Date.now()
					} satisfies DisplayTask;
				});
				const nextTaskForSocket = contextTasks.find((task) => task.id === taskID);
				if (normalizedWorkspaceRoomID && nextTaskForSocket) {
					publishRoomBoardActivity(normalizedWorkspaceRoomID, {
						type: columnKey === 'done' ? 'task_completed' : 'task_moved',
						title:
							columnKey === 'done' ? `Completed ${targetTask.title}` : `Moved ${targetTask.title}`,
						subtitle: `${statusLabel(previousColumn)} → ${statusLabel(columnKey)}`,
						actor: nextTaskForSocket.statusActorName || nextTaskForSocket.statusActorId || 'Unknown'
					});
					sendSocketPayload(
						buildTaskSocketPayload('task_move', normalizedWorkspaceRoomID, nextTaskForSocket)
					);
				}
			}
		} catch (error) {
			contextTasks = contextTasks.map((task) =>
				task.id === taskID
					? {
							...task,
							status: previousStatus
						}
					: task
			);
			contextError = error instanceof Error ? error.message : 'Failed to update task status';
		}
	}

	async function handleCreateTask(contentValue: string) {
		if (!contextAware || creatingTask) {
			return;
		}
		const content = contentValue.trim();
		if (!content) {
			return;
		}

		creatingTask = true;
		contextError = '';
		try {
			if ($activeContext.type === 'personal') {
				const response = await fetch(`${API_BASE}/api/personal/items`, {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					credentials: 'include',
					body: JSON.stringify({
						type: 'task',
						title: content,
						content,
						description: ''
					})
				});
				if (!response.ok) {
					throw new Error(await parseErrorMessage(response));
				}
				const created = normalizePersonalItem(await response.json().catch(() => null));
				if (!created) {
					throw new Error('Invalid personal task response');
				}
				contextTasks = [created, ...contextTasks];
			} else {
				const normalizedWorkspaceRoomID = normalizeRoomIDValue($activeContext.id);
				if (!normalizedWorkspaceRoomID) {
					throw new Error('Invalid workspace room id');
				}
				const response = await fetch(
					`${API_BASE}/api/rooms/${encodeURIComponent(normalizedWorkspaceRoomID)}/tasks`,
					{
						method: 'POST',
						headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
						credentials: 'include',
						body: JSON.stringify({
							content
						})
					}
				);
				if (!response.ok) {
					throw new Error(await parseErrorMessage(response));
				}
				const created = normalizeRoomTask(await response.json().catch(() => null));
				if (!created) {
					throw new Error('Invalid room task response');
				}
				contextTasks = [created, ...contextTasks];
				sendSocketPayload(
					buildTaskSocketPayload('task_create', normalizedWorkspaceRoomID, created)
				);
				publishRoomBoardActivity(normalizedWorkspaceRoomID, {
					type: 'task_added',
					title: `Added ${created.title}`,
					subtitle: 'Created task',
					actor: sessionUsername || sessionUserID || 'Unknown'
				});
			}
			newTaskContent = '';
		} catch (error) {
			contextError = error instanceof Error ? error.message : 'Failed to create task';
		} finally {
			creatingTask = false;
		}
	}

	async function persistRoomTaskStatus(taskId: string, roomIdValue: string, status: ColumnKey) {
		const response = await fetch(
			`${API_BASE}/api/rooms/${encodeURIComponent(roomIdValue)}/tasks/${encodeURIComponent(taskId)}/status`,
			{
				method: 'PUT',
				headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
				credentials: 'include',
				body: JSON.stringify({ status })
			}
		);
		if (!response.ok) {
			throw new Error(await parseErrorMessage(response));
		}
		const payload = (await response.json().catch(() => null)) as unknown;
		return parseStatusUpdateMetadata(payload, status);
	}

	async function moveTaskToColumn(taskId: string, targetColumn: ColumnKey) {
		const existingTask = $taskStore.find((task) => task.id === taskId);
		if (!existingTask) {
			return;
		}
		const previousColumn = resolveColumn(existingTask.status);
		if (previousColumn === targetColumn) {
			return;
		}

		const updatedTask = moveTaskOptimistic(taskId, targetColumn);
		if (!updatedTask) {
			return;
		}

		const targetRoomId = normalizedRoomId || updatedTask.roomId;
		if (!targetRoomId) {
			moveTaskOptimistic(taskId, previousColumn);
			roomBoardError = 'Invalid room id';
			return;
		}

		roomBoardError = '';
		try {
			const updateMeta = await persistRoomTaskStatus(taskId, targetRoomId, targetColumn);
			const nextTask = {
				...updatedTask,
				status: updateMeta.status,
				statusActorId: updateMeta.statusActorId || sessionUserID || undefined,
				statusActorName: updateMeta.statusActorName || sessionUsername || undefined,
				statusChangedAt: updateMeta.statusChangedAt || Date.now(),
				updatedAt: updateMeta.updatedAt || Date.now()
			};
			upsertTaskStoreEntry(nextTask, targetRoomId);
			applyTimelineTaskStatusUpdate(taskId, updateMeta.status, {
				statusActorId: nextTask.statusActorId,
				statusActorName: nextTask.statusActorName,
				statusChangedAt: nextTask.statusChangedAt
			});
			publishRoomBoardActivity(targetRoomId, {
				type: targetColumn === 'done' ? 'task_completed' : 'task_moved',
				title:
					targetColumn === 'done'
						? `Completed ${existingTask.title}`
						: `Moved ${existingTask.title}`,
				subtitle: `${statusLabel(previousColumn)} → ${statusLabel(targetColumn)}`,
				actor: nextTask.statusActorName || nextTask.statusActorId || 'Unknown'
			});
			sendSocketPayload(buildTaskSocketPayload('task_move', targetRoomId, nextTask));
		} catch (error) {
			moveTaskOptimistic(taskId, previousColumn);
			roomBoardError = error instanceof Error ? error.message : 'Failed to update task status';
		}
	}

	async function handleCreateRoomTask(contentValue: string) {
		if (contextAware || creatingTask) {
			return;
		}
		const content = contentValue.trim();
		if (!content) {
			return;
		}

		const normalizedTargetRoomID = normalizeRoomIDValue(normalizedRoomId);
		if (!normalizedTargetRoomID) {
			roomBoardError = 'Invalid room id';
			return;
		}

		creatingTask = true;
		roomBoardError = '';
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedTargetRoomID)}/tasks`,
				{
					method: 'POST',
					headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
					credentials: 'include',
					body: JSON.stringify({ content })
				}
			);
			if (!response.ok) {
				throw new Error(await parseErrorMessage(response));
			}

			const createdPayload = await response.json().catch(() => null);
			const createdTask = upsertTaskStoreEntry(createdPayload, normalizedTargetRoomID);
			if (!createdTask) {
				throw new Error('Invalid room task response');
			}
			sendSocketPayload(buildTaskSocketPayload('task_create', normalizedTargetRoomID, createdTask));
			publishRoomBoardActivity(normalizedTargetRoomID, {
				type: 'task_added',
				title: `Added ${createdTask.title}`,
				subtitle: 'Created task',
				actor: sessionUsername || sessionUserID || 'Unknown'
			});
			newTaskContent = '';
		} catch (error) {
			roomBoardError = error instanceof Error ? error.message : 'Failed to create task';
		} finally {
			creatingTask = false;
		}
	}

	function makeCellKey(taskId: string, field: EditableCellKey) {
		return `${taskId}:${field}`;
	}

	function isEditing(taskId: string, field: EditableField) {
		return editingTaskId === taskId && editingField === field;
	}

	function isSaving(taskId: string, field: EditableCellKey) {
		return savingCellKey === makeCellKey(taskId, field);
	}

	function isSavingStatus(taskId: string) {
		return savingStatusTaskId === taskId;
	}

	function shouldUseInlineGridEditor(field: EditableField, mode: 'auto' | 'inline' | 'panel') {
		if (mode === 'inline') {
			return true;
		}
		if (mode === 'panel') {
			return false;
		}
		return boardView === 'table' && INLINE_GRID_EDIT_FIELDS.includes(field);
	}

	function canEditTaskField(task: DisplayTask, field: EditableField) {
		if (field === 'title') {
			return true;
		}
		return true;
	}

	function canEditTaskStatus(task: DisplayTask) {
		if (contextAware && task.source === 'personal') {
			return true;
		}
		return true;
	}

	function getTaskFieldValue(task: DisplayTask, field: EditableField) {
		if (field === 'title') return task.title;
		if (field === 'description') return task.description;
		if (field === 'assigneeId') {
			const ownerFromMetadata = readDescriptionMetadataValue(task.description, 'owner');
			return task.assigneeId || ownerFromMetadata;
		}
		if (field === 'budget') return task.budget != null ? String(task.budget) : '';
		if (field === 'spent') return task.spent != null ? String(task.spent) : '';
		if (field === 'dueDate') return msToDateInput(task.dueDate);
		if (field === 'startDate') return msToDateInput(task.startDate);
		return task.sprintName;
	}

	function normalizeEditableValue(value: unknown) {
		if (typeof value === 'string') {
			return value.trim();
		}
		if (typeof value === 'number' && Number.isFinite(value)) {
			return String(value).trim();
		}
		if (value == null) {
			return '';
		}
		return String(value).trim();
	}

	async function startEditing(
		task: DisplayTask,
		field: EditableField,
		mode: 'auto' | 'inline' | 'panel' = 'auto'
	) {
		if (!canEditTaskField(task, field)) {
			return;
		}
		statusMenuTaskId = '';
		editingTaskId = task.id;
		editingField = field;
		const baseValue = getTaskFieldValue(task, field);
		if (field === 'assigneeId' && ownerOptions.length > 0) {
			const ownerOption =
				getOwnerOptionById(task.assigneeId) ??
				ownerOptions.find(
					(option) => option.label.toLowerCase() === ownerLabel(task).toLowerCase()
				) ??
				null;
			editingValue = ownerOption?.id ?? baseValue;
		} else {
			editingValue = baseValue;
		}
		editingCustomFields = cloneCustomFieldMap(task.customFields ?? {});
		editingCustomFieldsBaseline = cloneCustomFieldMap(task.customFields ?? {});
		editingBlockedBy = [...(task.blockedBy ?? [])];
		editingBlockedByBaseline = [...(task.blockedBy ?? [])];
		editingSubtasks = cloneDisplaySubtasks(task.subtasks ?? []);
		editingSubtasksBaseline = cloneDisplaySubtasks(task.subtasks ?? []);
		const useInlineEditor = shouldUseInlineGridEditor(field, mode);
		quickEditVisible = !useInlineEditor;
		await tick();
		setTimeout(() => {
			const activeEditorElement = useInlineEditor ? inlineEditorElement : quickEditorElement;
			activeEditorElement?.focus();
			if (activeEditorElement instanceof HTMLInputElement) {
				activeEditorElement.select();
			}
		}, 0);
	}

	function closeQuickEditor() {
		quickEditVisible = false;
		cancelEditing();
	}

	function openTaskDetails(task: DisplayTask) {
		if (boardView !== 'table') {
			boardView = 'table';
		}
		openTaskModal(task);
	}

	function openTaskModal(task: DisplayTask) {
		syncTaskModalFromTask(task);
		taskModalSaving = false;
		taskDetailSaving = false;
		taskDetailGenerating = false;
		statusMenuTaskId = '';
		cancelEditing();
	}

	function closeTaskModal() {
		taskEditModal = null;
		taskEditVals = {
			title: '',
			description: '',
			notes: '',
			assigneeId: '',
			status: 'todo',
			budget: '',
			spent: '',
			dueDate: '',
			startDate: ''
		};
		taskModalSaving = false;
		taskDetailSaving = false;
		taskDetailGenerating = false;
		taskDetailSummary = '';
		taskDetailSteps = [];
		taskDetailGeneratedAt = '';
		taskDetailError = '';
		taskDetailNotice = '';
	}

	async function saveTaskModal() {
		const task = taskEditModal;
		if (!task) return;
		if (!taskEditVals.title.trim()) {
			setBoardError('Task name cannot be empty');
			return;
		}

		// Non-admins submit a change request instead of saving directly
		if (!isAdmin) {
			const current = ($taskStore.find((t) => t.id === task.id) ?? task) as DisplayTask;
			const diff: Record<string, unknown> = { taskId: task.id, taskTitle: task.title };
			if (taskEditVals.title.trim() !== (current.title || '').trim()) { diff.title = taskEditVals.title.trim(); diff.before_title = current.title; }
			if (taskEditVals.assigneeId !== (current.assigneeId || '')) { diff.assigneeId = taskEditVals.assigneeId; diff.before_assigneeId = current.assigneeId; }
			if (String(taskEditVals.budget ?? '').trim() !== String(current.budget ?? '').trim()) { diff.budget = taskEditVals.budget; diff.before_budget = current.budget; }
			if (String(taskEditVals.spent ?? '').trim() !== String(current.spent ?? '').trim()) { diff.spent = taskEditVals.spent; diff.before_spent = current.spent; }
			if (taskEditVals.dueDate !== msToDateInput(current.dueDate)) { diff.dueDate = taskEditVals.dueDate; diff.before_dueDate = msToDateInput(current.dueDate); }
			if (taskEditVals.startDate !== msToDateInput(current.startDate)) { diff.startDate = taskEditVals.startDate; diff.before_startDate = msToDateInput(current.startDate); }
			if (taskEditVals.status !== resolveColumn(current.status as ColumnKey)) { diff.status = taskEditVals.status; diff.before_status = resolveColumn(current.status as ColumnKey); }
			submitChangeRequest(roomId, sessionUserID, sessionUserName, 'edit_task', task.title, diff);
			closeTaskModal();
			return;
		}

		taskModalSaving = true;
		clearBoardError();
		try {
			const current = ($taskStore.find((t) => t.id === task.id) ?? task) as DisplayTask;
			if (taskEditVals.title.trim() !== (current.title || '').trim())
				await updateRoomTaskField(current, 'title', taskEditVals.title.trim());
			if (taskEditVals.assigneeId !== (current.assigneeId || ''))
				await updateRoomTaskField(current, 'assigneeId', taskEditVals.assigneeId);
			const curBudget = current.budget != null ? String(current.budget) : '';
			if (String(taskEditVals.budget ?? '').trim() !== curBudget.trim())
				await updateRoomTaskField(current, 'budget', String(taskEditVals.budget ?? ''));
			const curSpent = current.spent != null ? String(current.spent) : '';
			if (String(taskEditVals.spent ?? '').trim() !== curSpent.trim())
				await updateRoomTaskField(current, 'spent', String(taskEditVals.spent ?? ''));
			const curDueDate = msToDateInput(current.dueDate);
			if (taskEditVals.dueDate !== curDueDate)
				await updateRoomTaskField(current, 'dueDate', taskEditVals.dueDate);
			const curStartDate = msToDateInput(current.startDate);
			if (taskEditVals.startDate !== curStartDate)
				await updateRoomTaskField(current, 'startDate', taskEditVals.startDate);
			if (taskEditVals.status !== resolveColumn(current.status as ColumnKey))
				await applyStatus(current, taskEditVals.status as ColumnKey);
			closeTaskModal();
		} catch (err) {
			setBoardError(err instanceof Error ? err.message : 'Failed to update task');
		} finally {
			taskModalSaving = false;
		}
	}

	function relationTaskOptions(task: DisplayTask) {
		return boardTasks
			.filter((entry) => entry.source === 'room' && entry.id !== task.id)
			.sort((left, right) =>
				left.title.localeCompare(right.title, undefined, { sensitivity: 'base' })
			);
	}

	function isEditingDependencySelected(taskID: string) {
		return editingBlockedBy.includes(taskID);
	}

	function toggleEditingDependency(taskID: string, checked: boolean) {
		const normalizedTaskID = taskID.trim();
		if (!normalizedTaskID) {
			return;
		}
		const nextSet = new Set(editingBlockedBy.map((entry) => entry.trim()).filter(Boolean));
		if (checked) {
			nextSet.add(normalizedTaskID);
		} else {
			nextSet.delete(normalizedTaskID);
		}
		editingBlockedBy = [...nextSet];
	}

	function createDraftSubtask(content = ''): DisplaySubtask {
		return {
			id: `temp-subtask-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
			content: content.trim() || 'Subtask',
			completed: false,
			position: editingSubtasks.length
		};
	}

	function addEditingSubtask() {
		editingSubtasks = [...editingSubtasks, createDraftSubtask('Subtask')].map((subtask, index) => ({
			...subtask,
			position: index
		}));
	}

	function removeEditingSubtask(subtaskID: string) {
		editingSubtasks = editingSubtasks
			.filter((subtask) => subtask.id !== subtaskID)
			.map((subtask, index) => ({
				...subtask,
				position: index
			}));
	}

	function updateEditingSubtaskContent(subtaskID: string, nextValue: string) {
		editingSubtasks = editingSubtasks.map((subtask) =>
			subtask.id !== subtaskID
				? subtask
				: {
						...subtask,
						content: nextValue.trim() || 'Subtask'
					}
		);
	}

	function updateEditingSubtaskCompleted(subtaskID: string, completed: boolean) {
		editingSubtasks = editingSubtasks.map((subtask) =>
			subtask.id !== subtaskID
				? subtask
				: {
						...subtask,
						completed
					}
		);
	}

	function normalizeEditingSubtasks(subtasks: DisplaySubtask[]) {
		return subtasks
			.map((subtask, index) => ({
				id: normalizeTaskRelationIdentifier(subtask.id) || `temp-subtask-${index + 1}`,
				content: subtask.content.trim() || `Subtask ${index + 1}`,
				completed: Boolean(subtask.completed),
				position: index
			}))
			.filter((subtask) => Boolean(subtask.id));
	}

	function dependencySignature(values: string[]) {
		return normalizeTaskRelationIdentifiers(values).sort().join('|');
	}

	function subtasksSignature(subtasks: DisplaySubtask[]) {
		const normalized = normalizeEditingSubtasks(subtasks);
		return JSON.stringify(normalized);
	}

	function taskSubtaskSummary(task: DisplayTask) {
		const subtasks = task.subtasks ?? [];
		if (subtasks.length === 0) {
			return null;
		}
		const completedCount = subtasks.filter((subtask) => subtask.completed).length;
		const percent =
			typeof task.completionPercent === 'number'
				? Math.max(0, Math.min(100, Math.round(task.completionPercent)))
				: Math.round((completedCount / subtasks.length) * 100);
		return {
			completedCount,
			totalCount: subtasks.length,
			percent
		};
	}

	function taskSubtaskSummaryLabel(task: DisplayTask) {
		const summary = taskSubtaskSummary(task);
		if (!summary) {
			return '';
		}
		return `${summary.completedCount}/${summary.totalCount} subtasks`;
	}

	function taskSubtaskPercentLabel(task: DisplayTask) {
		const summary = taskSubtaskSummary(task);
		if (!summary) {
			return '';
		}
		return `${summary.percent}% subtasks`;
	}

	function applyRoomTaskMutationPayload(
		payload: unknown,
		targetRoomID: string,
		source: TaskSource
	): DisplayTask {
		const normalizedUpdatedTask = normalizeRoomTask(payload);
		if (!normalizedUpdatedTask) {
			throw new Error('Invalid task relation response');
		}
		const nextTask = { ...normalizedUpdatedTask, source };
		if (contextAware && source === 'room' && $activeContext.type === 'room') {
			contextTasks = contextTasks.map((entry) => (entry.id === nextTask.id ? nextTask : entry));
		} else {
			upsertTaskStoreEntry(payload, targetRoomID);
		}
		return nextTask;
	}

	async function createTaskRelation(
		targetRoomID: string,
		taskID: string,
		body: Record<string, unknown>,
		source: TaskSource
	) {
		const response = await fetch(
			`${API_BASE}/api/rooms/${encodeURIComponent(targetRoomID)}/tasks/${encodeURIComponent(taskID)}/relations`,
			{
				method: 'POST',
				headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
				credentials: 'include',
				body: JSON.stringify(body)
			}
		);
		if (!response.ok) {
			throw new Error(await parseErrorMessage(response));
		}
		const payload = await response.json().catch(() => null);
		return applyRoomTaskMutationPayload(payload, targetRoomID, source);
	}

	async function updateTaskRelation(
		targetRoomID: string,
		taskID: string,
		relationTargetID: string,
		body: Record<string, unknown>,
		source: TaskSource
	) {
		const response = await fetch(
			`${API_BASE}/api/rooms/${encodeURIComponent(targetRoomID)}/tasks/${encodeURIComponent(taskID)}/relations/${encodeURIComponent(relationTargetID)}`,
			{
				method: 'PATCH',
				headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
				credentials: 'include',
				body: JSON.stringify(body)
			}
		);
		if (!response.ok) {
			throw new Error(await parseErrorMessage(response));
		}
		const payload = await response.json().catch(() => null);
		return applyRoomTaskMutationPayload(payload, targetRoomID, source);
	}

	async function deleteTaskRelation(
		targetRoomID: string,
		taskID: string,
		relationTargetID: string,
		source: TaskSource
	) {
		const response = await fetch(
			`${API_BASE}/api/rooms/${encodeURIComponent(targetRoomID)}/tasks/${encodeURIComponent(taskID)}/relations/${encodeURIComponent(relationTargetID)}`,
			{
				method: 'DELETE',
				headers: withSessionUserHeaders(),
				credentials: 'include'
			}
		);
		if (!response.ok) {
			throw new Error(await parseErrorMessage(response));
		}
		const payload = await response.json().catch(() => null);
		return applyRoomTaskMutationPayload(payload, targetRoomID, source);
	}

	async function persistEditingTaskRelations(task: DisplayTask) {
		if (task.source !== 'room') {
			return true;
		}

		const targetRoomID = contextAware
			? normalizeRoomIDValue($activeContext.id)
			: normalizeRoomIDValue(task.roomId || normalizedRoomId);
		if (!targetRoomID) {
			setBoardError('Invalid room id');
			return false;
		}

		const nextBlockedBy = normalizeTaskRelationIdentifiers(editingBlockedBy);
		const baselineBlockedBy = normalizeTaskRelationIdentifiers(editingBlockedByBaseline);
		const nextSubtasks = normalizeEditingSubtasks(editingSubtasks);
		const baselineSubtasks = normalizeEditingSubtasks(editingSubtasksBaseline);

		const dependenciesChanged =
			dependencySignature(nextBlockedBy) !== dependencySignature(baselineBlockedBy);
		const subtasksChanged = subtasksSignature(nextSubtasks) !== subtasksSignature(baselineSubtasks);
		if (!dependenciesChanged && !subtasksChanged) {
			return true;
		}

		savingRelations = true;
		try {
			let latestTask: DisplayTask | null = null;

			if (dependenciesChanged) {
				const dependenciesToRemove = baselineBlockedBy.filter(
					(dependencyID) => !nextBlockedBy.includes(dependencyID)
				);
				const dependenciesToAdd = nextBlockedBy.filter(
					(dependencyID) => !baselineBlockedBy.includes(dependencyID)
				);

				for (const dependencyID of dependenciesToRemove) {
					latestTask = await deleteTaskRelation(targetRoomID, task.id, dependencyID, task.source);
				}
				for (const dependencyID of dependenciesToAdd) {
					latestTask = await createTaskRelation(
						targetRoomID,
						task.id,
						{
							relation_type: 'blocked_by',
							to_task_id: dependencyID
						},
						task.source
					);
				}
			}

			if (subtasksChanged) {
				const baselineByID = new Map(baselineSubtasks.map((subtask) => [subtask.id, subtask]));
				const nextByID = new Map(nextSubtasks.map((subtask) => [subtask.id, subtask]));

				for (const baselineSubtask of baselineSubtasks) {
					if (nextByID.has(baselineSubtask.id)) {
						continue;
					}
					latestTask = await deleteTaskRelation(
						targetRoomID,
						task.id,
						baselineSubtask.id,
						task.source
					);
				}

				for (const nextSubtask of nextSubtasks) {
					const baselineSubtask = baselineByID.get(nextSubtask.id);
					if (!baselineSubtask) {
						continue;
					}
					const changed =
						baselineSubtask.content !== nextSubtask.content ||
						baselineSubtask.completed !== nextSubtask.completed ||
						baselineSubtask.position !== nextSubtask.position;
					if (!changed) {
						continue;
					}
					latestTask = await updateTaskRelation(
						targetRoomID,
						task.id,
						nextSubtask.id,
						{
							relation_type: 'subtask',
							content: nextSubtask.content,
							completed: nextSubtask.completed,
							position: nextSubtask.position
						},
						task.source
					);
				}

				for (const nextSubtask of nextSubtasks) {
					if (baselineByID.has(nextSubtask.id)) {
						continue;
					}
					const body: Record<string, unknown> = {
						relation_type: 'subtask',
						content: nextSubtask.content,
						completed: nextSubtask.completed,
						position: nextSubtask.position
					};
					if (!nextSubtask.id.startsWith('temp-subtask-')) {
						body.to_task_id = nextSubtask.id;
					}
					latestTask = await createTaskRelation(targetRoomID, task.id, body, task.source);
				}
			}

			if (latestTask) {
				editingBlockedBy = [...(latestTask.blockedBy ?? [])];
				editingBlockedByBaseline = [...(latestTask.blockedBy ?? [])];
				editingSubtasks = cloneDisplaySubtasks(latestTask.subtasks ?? []);
				editingSubtasksBaseline = cloneDisplaySubtasks(latestTask.subtasks ?? []);
			} else {
				editingBlockedByBaseline = [...nextBlockedBy];
				editingSubtasksBaseline = cloneDisplaySubtasks(nextSubtasks);
			}
			return true;
		} catch (error) {
			setBoardError(error instanceof Error ? error.message : 'Failed to save task relations');
			return false;
		} finally {
			savingRelations = false;
		}
	}

	async function saveQuickEditor() {
		if (!editingTask || !editingField) {
			return;
		}
		const nextValue = normalizeEditableValue(editingValue);
		if (editingField === 'title' && !nextValue) {
			setBoardError('Task name cannot be empty');
			return;
		}
		if (
			(editingField === 'budget' || editingField === 'spent') &&
			nextValue &&
			isNaN(Number(nextValue.replace(/[$,]/g, '')))
		) {
			setBoardError(`${fieldLabel(editingField)} must be a number`);
			return;
		}
		const customFieldsSaved = await persistEditingCustomFields(editingTask);
		if (!customFieldsSaved) {
			return;
		}
		const relationsSaved = await persistEditingTaskRelations(editingTask);
		if (!relationsSaved) {
			return;
		}
		await commitEditing(editingTask, editingField);
	}

	function cancelEditing() {
		editingTaskId = '';
		editingField = '';
		editingValue = '';
		quickEditVisible = false;
		editingCustomFields = {};
		editingCustomFieldsBaseline = {};
		editingBlockedBy = [];
		editingBlockedByBaseline = [];
		editingSubtasks = [];
		editingSubtasksBaseline = [];
		savingRelations = false;
	}

	function msToDateInput(ms?: number): string {
		if (!ms) return '';
		return new Date(ms).toISOString().slice(0, 10);
	}

	function formatDateCell(ms?: number): string {
		if (!ms) return '—';
		return new Date(ms).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
	}

	function buildTimelineDateMap(
		timeline: ProjectTimeline | null
	): Map<string, { startDate: number; endDate: number }> {
		const map = new Map<string, { startDate: number; endDate: number }>();
		if (!timeline) return map;
		for (const sprint of timeline.sprints) {
			for (const task of sprint.tasks) {
				const s = task.start_date ? Date.parse(task.start_date) : 0;
				const e = task.end_date ? Date.parse(task.end_date) : 0;
				if ((s > 0 || e > 0) && task.id) {
					map.set(task.id, { startDate: s > 0 ? s : 0, endDate: e > 0 ? e : 0 });
				}
			}
		}
		return map;
	}

	function fieldLabel(field: EditableField) {
		if (field === 'title') return 'task name';
		if (field === 'description') return 'description';
		if (field === 'assigneeId') return 'assignee';
		if (field === 'budget') return 'budget';
		if (field === 'spent') return 'spent';
		if (field === 'dueDate') return 'due date';
		if (field === 'startDate') return 'start date';
		return 'sprint';
	}

	function applyTaskResponseToBoard(
		task: DisplayTask,
		payload: unknown,
		activitySubtitle: string
	): DisplayTask {
		const targetRoomID = contextAware
			? normalizeRoomIDValue($activeContext.id)
			: normalizeRoomIDValue(task.roomId || normalizedRoomId);
		if (!targetRoomID) {
			throw new Error('Invalid room id');
		}

		if (contextAware && task.source === 'room' && $activeContext.type === 'room') {
			const normalizedUpdatedTask = normalizeRoomTask(payload);
			if (!normalizedUpdatedTask) {
				throw new Error('Invalid task update response');
			}
			const nextTask = { ...normalizedUpdatedTask, source: task.source };
			contextTasks = contextTasks.map((entry) => (entry.id === task.id ? nextTask : entry));
			sendSocketPayload(buildTaskSocketPayload('task_update', targetRoomID, nextTask));
			publishRoomBoardActivity(targetRoomID, {
				type: 'task_modified',
				title: `Updated ${nextTask.title}`,
				subtitle: activitySubtitle,
				actor: sessionUsername || sessionUserID || 'Unknown'
			});
			return nextTask;
		}

		const updatedTask = upsertTaskStoreEntry(payload, targetRoomID);
		if (!updatedTask) {
			throw new Error('Invalid task update response');
		}
		publishRoomBoardActivity(targetRoomID, {
			type: 'task_modified',
			title: `Updated ${updatedTask.title}`,
			subtitle: activitySubtitle,
			actor: sessionUsername || sessionUserID || 'Unknown'
		});
		sendSocketPayload(buildTaskSocketPayload('task_update', targetRoomID, updatedTask));
		return {
			id: updatedTask.id,
			roomId: updatedTask.roomId,
			title: updatedTask.title,
			description: updatedTask.description,
			status: updatedTask.status,
			taskType: updatedTask.taskType,
			customFields: { ...(updatedTask.customFields ?? {}) },
			blockedBy: [...(updatedTask.blockedBy ?? [])],
			blocks: [...(updatedTask.blocks ?? [])],
			subtasks: cloneDisplaySubtasks(updatedTask.subtasks ?? []),
			completionPercent: updatedTask.completionPercent,
			budget: updatedTask.budget,
			spent: updatedTask.spent,
			dueDate: updatedTask.dueDate,
			startDate: updatedTask.startDate,
			roles: task.roles,
			sprintName: updatedTask.sprintName,
			assigneeId: updatedTask.assigneeId,
			statusActorId: updatedTask.statusActorId,
			statusActorName: updatedTask.statusActorName,
			statusChangedAt: updatedTask.statusChangedAt,
			createdAt: updatedTask.createdAt,
			updatedAt: updatedTask.updatedAt,
			source: task.source
		};
	}

	function updateLocalTaskDetails(
		task: DisplayTask,
		description: string,
		notes: string,
		generatedSummary?: string,
		generatedSteps?: string[],
		generatedAt?: string
	) {
		const nextDescription = replaceDescriptionBase(task.description, description);
		const nextCustomFields = cloneCustomFieldMap(task.customFields ?? {});
		if (notes.trim()) {
			nextCustomFields[TASK_NOTES_FIELD_KEY] = notes.trim();
		} else {
			delete nextCustomFields[TASK_NOTES_FIELD_KEY];
		}
		if (typeof generatedSummary === 'string') {
			if (generatedSummary.trim()) {
				nextCustomFields[TASK_DETAIL_SUMMARY_FIELD_KEY] = generatedSummary.trim();
			} else {
				delete nextCustomFields[TASK_DETAIL_SUMMARY_FIELD_KEY];
			}
		}
		if (Array.isArray(generatedSteps)) {
			if (generatedSteps.length > 0) {
				nextCustomFields[TASK_DETAIL_STEPS_FIELD_KEY] = [...generatedSteps];
			} else {
				delete nextCustomFields[TASK_DETAIL_STEPS_FIELD_KEY];
			}
		}
		if (typeof generatedAt === 'string') {
			if (generatedAt.trim()) {
				nextCustomFields[TASK_DETAIL_GENERATED_AT_FIELD_KEY] = generatedAt.trim();
			} else {
				delete nextCustomFields[TASK_DETAIL_GENERATED_AT_FIELD_KEY];
			}
		}
		const nextTask: DisplayTask = {
			...task,
			description: nextDescription,
			customFields: nextCustomFields,
			updatedAt: Date.now()
		};
		contextTasks = contextTasks.map((entry) => (entry.id === task.id ? nextTask : entry));
		return nextTask;
	}

	async function saveTaskDetails() {
		const task = taskEditModal;
		if (!task) {
			return;
		}
		taskDetailSaving = true;
		taskDetailError = '';
		taskDetailNotice = '';
		clearBoardError();
		try {
			const nextDescription = replaceDescriptionBase(task.description, taskEditVals.description);
			if (contextAware && task.source === 'personal') {
				const nextTask = updateLocalTaskDetails(task, taskEditVals.description, taskEditVals.notes);
				syncTaskModalFromTask(nextTask, { preserveCoreValues: true });
				taskDetailNotice = 'Saved task details.';
				return;
			}
			const targetRoomID = contextAware
				? normalizeRoomIDValue($activeContext.id)
				: normalizeRoomIDValue(task.roomId || normalizedRoomId);
			if (!targetRoomID) {
				throw new Error('Invalid room id');
			}
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(targetRoomID)}/tasks/${encodeURIComponent(task.id)}`,
				{
					method: 'PUT',
					headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
					credentials: 'include',
					body: JSON.stringify({
						description: nextDescription,
						custom_fields: {
							[TASK_NOTES_FIELD_KEY]: taskEditVals.notes.trim() || null
						}
					})
				}
			);
			if (!response.ok) {
				throw new Error(await parseErrorMessage(response));
			}
			const payload = await response.json().catch(() => null);
			const updatedTask = applyTaskResponseToBoard(task, payload, 'Updated task details');
			syncTaskModalFromTask(updatedTask, { preserveCoreValues: true });
			taskDetailNotice = 'Saved task details.';
		} catch (error) {
			const message = error instanceof Error ? error.message : 'Failed to save task details';
			taskDetailError = message;
			setBoardError(message);
		} finally {
			taskDetailSaving = false;
		}
	}

	async function generateTaskDetails() {
		const task = taskEditModal;
		if (!task || task.source !== 'room') {
			return;
		}
		const targetRoomID = contextAware
			? normalizeRoomIDValue($activeContext.id)
			: normalizeRoomIDValue(task.roomId || normalizedRoomId);
		if (!targetRoomID) {
			taskDetailError = 'Invalid room id';
			return;
		}

		taskDetailGenerating = true;
		taskDetailError = '';
		taskDetailNotice = '';
		clearBoardError();
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(targetRoomID)}/tasks/${encodeURIComponent(task.id)}/details/generate`,
				{
					method: 'POST',
					headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
					credentials: 'include',
					body: JSON.stringify({
						description: taskEditVals.description.trim(),
						notes: taskEditVals.notes.trim()
					})
				}
			);
			if (!response.ok) {
				throw new Error(await parseErrorMessage(response));
			}
			const payload = await response.json().catch(() => null);
			const updatedTask = applyTaskResponseToBoard(task, payload, 'Generated detailed task steps');
			syncTaskModalFromTask(updatedTask, { preserveCoreValues: true });
			taskDetailNotice = 'Generated and stored detailed steps for the team.';
		} catch (error) {
			const message =
				error instanceof Error ? error.message : 'Failed to generate detailed task steps';
			taskDetailError = message;
			setBoardError(message);
		} finally {
			taskDetailGenerating = false;
		}
	}

	function handleTaskRowClick(task: DisplayTask, isEditMode: boolean) {
		if (isEditMode) {
			toggleTaskSelection(task.id);
			return;
		}
		openTaskDetails(task);
	}

	async function updateContextRoomTaskField(
		task: DisplayTask,
		field: EditableField,
		nextValue: string
	) {
		const normalizedWorkspaceRoomID = normalizeRoomIDValue($activeContext.id);
		if (!normalizedWorkspaceRoomID) {
			throw new Error('Invalid workspace room id');
		}

		const body: Record<string, unknown> = {};
		if (field === 'title') {
			body.title = nextValue;
		} else if (field === 'description') {
			body.description = nextValue;
		} else if (field === 'assigneeId') {
			const trimmedOwner = nextValue.trim();
			if (!trimmedOwner) {
				body.assignee_id = '';
				body.description = upsertDescriptionMetadataValue(task.description, 'owner', 'Owner', '');
			} else if (isLikelyUUID(trimmedOwner)) {
				body.assignee_id = trimmedOwner;
				body.description = upsertDescriptionMetadataValue(task.description, 'owner', 'Owner', '');
			} else {
				body.assignee_id = '';
				body.description = upsertDescriptionMetadataValue(
					task.description,
					'owner',
					'Owner',
					trimmedOwner
				);
			}
		} else if (field === 'budget') {
			body.budget = parseBudgetValue(nextValue) ?? 0;
		} else if (field === 'spent') {
			body.actual_cost = parseBudgetValue(nextValue) ?? 0;
		} else {
			body.sprint_name = nextValue;
		}

		const response = await fetch(
			`${API_BASE}/api/rooms/${encodeURIComponent(normalizedWorkspaceRoomID)}/tasks/${encodeURIComponent(task.id)}`,
			{
				method: 'PUT',
				headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
				credentials: 'include',
				body: JSON.stringify(body)
			}
		);
		if (!response.ok) {
			throw new Error(await parseErrorMessage(response));
		}
		const payload = await response.json().catch(() => null);
		const normalizedUpdatedTask = normalizeRoomTask(payload);
		if (!normalizedUpdatedTask) {
			throw new Error('Invalid task update response');
		}
		const nextTask = { ...normalizedUpdatedTask, source: task.source };
		contextTasks = contextTasks.map((entry) => (entry.id === task.id ? nextTask : entry));
		sendSocketPayload(buildTaskSocketPayload('task_update', normalizedWorkspaceRoomID, nextTask));
		publishRoomBoardActivity(normalizedWorkspaceRoomID, {
			type: 'task_modified',
			title: `Updated ${nextTask.title}`,
			subtitle: `Edited ${fieldLabel(field)}`,
			actor: sessionUsername || sessionUserID || 'Unknown'
		});
	}

	function updateContextPersonalTaskField(
		task: DisplayTask,
		field: EditableField,
		nextValue: string
	) {
		contextTasks = contextTasks.map((entry) => {
			if (entry.id !== task.id) {
				return entry;
			}
			if (field === 'title') {
				return { ...entry, title: nextValue, updatedAt: Date.now() };
			}
			if (field === 'assigneeId') {
				return {
					...entry,
					assigneeId: nextValue,
					description: upsertDescriptionMetadataValue(
						entry.description,
						'owner',
						'Owner',
						nextValue
					),
					updatedAt: Date.now()
				};
			}
			if (field === 'budget') {
				return {
					...entry,
					budget: parseBudgetValue(nextValue) ?? 0,
					updatedAt: Date.now()
				};
			}
			if (field === 'spent') {
				return {
					...entry,
					spent: parseBudgetValue(nextValue) ?? 0,
					updatedAt: Date.now()
				};
			}
			if (field === 'description') {
				return {
					...entry,
					description: nextValue,
					updatedAt: Date.now()
				};
			}
			return {
				...entry,
				sprintName: nextValue,
				updatedAt: Date.now()
			};
		});
	}

	async function updateRoomTaskField(task: DisplayTask, field: EditableField, nextValue: string) {
		const currentTask = $taskStore.find((entry) => entry.id === task.id);
		if (!currentTask) {
			throw new Error('Task not found');
		}
		const targetRoomId = normalizeRoomIDValue(currentTask.roomId || normalizedRoomId);
		if (!targetRoomId) {
			throw new Error('Invalid room id');
		}

		const body: Record<string, unknown> = {};
		if (field === 'title') {
			body.title = nextValue;
		} else if (field === 'description') {
			body.description = nextValue;
		} else if (field === 'assigneeId') {
			const trimmedOwner = nextValue.trim();
			if (!trimmedOwner) {
				body.assignee_id = '';
				body.description = upsertDescriptionMetadataValue(task.description, 'owner', 'Owner', '');
			} else if (isLikelyUUID(trimmedOwner)) {
				body.assignee_id = trimmedOwner;
				body.description = upsertDescriptionMetadataValue(task.description, 'owner', 'Owner', '');
			} else {
				body.assignee_id = '';
				body.description = upsertDescriptionMetadataValue(
					task.description,
					'owner',
					'Owner',
					trimmedOwner
				);
			}
		} else if (field === 'budget') {
			body.budget = parseBudgetValue(nextValue) ?? 0;
		} else if (field === 'spent') {
			body.actual_cost = parseBudgetValue(nextValue) ?? 0;
		} else if (field === 'dueDate') {
			body.due_date = nextValue ? new Date(nextValue).toISOString() : null;
		} else if (field === 'startDate') {
			body.start_date = nextValue ? new Date(nextValue).toISOString() : null;
		} else {
			body.sprint_name = nextValue;
		}

		const response = await fetch(
			`${API_BASE}/api/rooms/${encodeURIComponent(targetRoomId)}/tasks/${encodeURIComponent(task.id)}`,
			{
				method: 'PUT',
				headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
				credentials: 'include',
				body: JSON.stringify(body)
			}
		);
		if (!response.ok) {
			throw new Error(await parseErrorMessage(response));
		}

		const payload = await response.json().catch(() => null);
		const updatedTask = upsertTaskStoreEntry(payload, targetRoomId);
		if (!updatedTask) {
			throw new Error('Invalid task update response');
		}

		publishRoomBoardActivity(targetRoomId, {
			type: 'task_modified',
			title: `Updated ${updatedTask.title}`,
			subtitle: `Edited ${fieldLabel(field)}`,
			actor: sessionUsername || sessionUserID || 'Unknown'
		});
		sendSocketPayload(buildTaskSocketPayload('task_update', targetRoomId, updatedTask));
	}

	async function persistEditingCustomFields(task: DisplayTask) {
		if (roomFieldSchemas.length === 0) {
			return true;
		}

		let patch: Record<string, unknown> = {};
		try {
			patch = buildTaskCustomFieldPatch();
		} catch (error) {
			setBoardError(error instanceof Error ? error.message : 'Invalid custom field values');
			return false;
		}
		if (Object.keys(patch).length === 0) {
			return true;
		}

		if (contextAware && task.source === 'personal') {
			const patchedFields = applyCustomFieldPatch(task.customFields ?? {}, patch);
			contextTasks = contextTasks.map((entry) =>
				entry.id === task.id
					? {
							...entry,
							customFields: patchedFields,
							updatedAt: Date.now()
						}
					: entry
			);
			editingCustomFieldsBaseline = cloneCustomFieldMap(patchedFields);
			editingCustomFields = cloneCustomFieldMap(patchedFields);
			return true;
		}

		const targetRoomID = contextAware
			? normalizeRoomIDValue($activeContext.id)
			: normalizeRoomIDValue(task.roomId || normalizedRoomId);
		if (!targetRoomID) {
			setBoardError('Invalid room id');
			return false;
		}

		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(targetRoomID)}/tasks/${encodeURIComponent(task.id)}`,
				{
					method: 'PUT',
					headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
					credentials: 'include',
					body: JSON.stringify({ custom_fields: patch })
				}
			);
			if (!response.ok) {
				throw new Error(await parseErrorMessage(response));
			}

			const payload = await response.json().catch(() => null);
			if (contextAware && task.source === 'room' && $activeContext.type === 'room') {
				const normalizedUpdatedTask = normalizeRoomTask(payload);
				if (!normalizedUpdatedTask) {
					throw new Error('Invalid task update response');
				}
				const nextTask = { ...normalizedUpdatedTask, source: task.source };
				contextTasks = contextTasks.map((entry) => (entry.id === task.id ? nextTask : entry));
				sendSocketPayload(buildTaskSocketPayload('task_update', targetRoomID, nextTask));
				publishRoomBoardActivity(targetRoomID, {
					type: 'task_modified',
					title: `Updated ${nextTask.title}`,
					subtitle: 'Edited custom fields',
					actor: sessionUsername || sessionUserID || 'Unknown'
				});
				editingCustomFieldsBaseline = cloneCustomFieldMap(nextTask.customFields ?? {});
				editingCustomFields = cloneCustomFieldMap(nextTask.customFields ?? {});
				return true;
			}

			const updatedTask = upsertTaskStoreEntry(payload, targetRoomID);
			if (!updatedTask) {
				throw new Error('Invalid task update response');
			}
			publishRoomBoardActivity(targetRoomID, {
				type: 'task_modified',
				title: `Updated ${updatedTask.title}`,
				subtitle: 'Edited custom fields',
				actor: sessionUsername || sessionUserID || 'Unknown'
			});
			sendSocketPayload(buildTaskSocketPayload('task_update', targetRoomID, updatedTask));
			editingCustomFieldsBaseline = cloneCustomFieldMap(updatedTask.customFields ?? {});
			editingCustomFields = cloneCustomFieldMap(updatedTask.customFields ?? {});
			return true;
		} catch (error) {
			setBoardError(error instanceof Error ? error.message : 'Failed to update custom fields');
			return false;
		}
	}

	async function commitEditing(task: DisplayTask, field: EditableField) {
		if (!isEditing(task.id, field)) {
			return;
		}
		const cellKey = makeCellKey(task.id, field);
		if (savingCellKey && savingCellKey !== cellKey) {
			return;
		}

		const currentValue = normalizeEditableValue(getTaskFieldValue(task, field));
		const nextValue = normalizeEditableValue(editingValue);

		if (field === 'title' && !nextValue) {
			setBoardError('Task name cannot be empty');
			return;
		}
		if (
			(field === 'budget' || field === 'spent') &&
			nextValue &&
			isNaN(Number(nextValue.replace(/[$,]/g, '')))
		) {
			setBoardError(`${fieldLabel(field)} must be a number`);
			return;
		}
		if (nextValue === currentValue) {
			cancelEditing();
			return;
		}
		if (!canEditTaskField(task, field)) {
			cancelEditing();
			return;
		}

		// Non-admins submit a change request instead of saving directly
		if (!isAdmin) {
			submitChangeRequest(roomId, sessionUserID, sessionUserName, 'edit_task', task.title, {
				taskId: task.id, taskTitle: task.title,
				field, value: nextValue, before: currentValue
			});
			cancelEditing();
			return;
		}

		savingCellKey = cellKey;
		clearBoardError();
		try {
			if (contextAware) {
				if (task.source === 'room' && $activeContext.type === 'room') {
					await updateContextRoomTaskField(task, field, nextValue);
				} else if (task.source === 'personal' && $activeContext.type === 'personal') {
					updateContextPersonalTaskField(task, field, nextValue);
				} else {
					throw new Error('Selected context cannot update this task');
				}
			} else {
				await updateRoomTaskField(task, field, nextValue);
			}
			cancelEditing();
		} catch (error) {
			setBoardError(error instanceof Error ? error.message : 'Failed to update task');
		} finally {
			if (savingCellKey === cellKey) {
				savingCellKey = '';
			}
		}
	}

	function onEditorKeyDown(event: KeyboardEvent, task: DisplayTask, field: EditableField) {
		if (event.key === 'Escape') {
			event.preventDefault();
			cancelEditing();
			return;
		}
		if (event.key === 'Enter') {
			event.preventDefault();
			if (editingTaskId === task.id && editingField === field) {
				if (quickEditVisible) {
					void saveQuickEditor();
				} else {
					void commitEditing(task, field);
				}
				return;
			}
			void commitEditing(task, field);
		}
	}

	function onOwnerSelectKeyDown(event: KeyboardEvent, task: DisplayTask) {
		if (event.key === 'Escape') {
			event.preventDefault();
			cancelEditing();
			return;
		}
		if (event.key === 'Enter') {
			event.preventDefault();
			if (editingTaskId === task.id && editingField === 'assigneeId') {
				if (quickEditVisible) {
					void saveQuickEditor();
				} else {
					void commitEditing(task, 'assigneeId');
				}
				return;
			}
			void commitEditing(task, 'assigneeId');
		}
	}

	function onInlineEditorBlur(task: DisplayTask, field: EditableField) {
		if (!isEditing(task.id, field) || quickEditVisible) {
			return;
		}
		void commitEditing(task, field);
	}

	function toggleStatusMenu(event: MouseEvent, task: DisplayTask) {
		event.stopPropagation();
		cancelEditing();
		if (!canEditTaskStatus(task) || isSavingStatus(task.id)) {
			return;
		}
		statusMenuTaskId = statusMenuTaskId === task.id ? '' : task.id;
	}

	async function applyStatus(task: DisplayTask, nextStatus: ColumnKey) {
		if (!canEditTaskStatus(task)) {
			return;
		}
		if (isSavingStatus(task.id)) {
			return;
		}
		if (resolveColumn(task.status) === nextStatus) {
			statusMenuTaskId = '';
			return;
		}

		// Non-admins submit a change request for status changes
		if (!isAdmin) {
			submitChangeRequest(roomId, sessionUserID, sessionUserName, 'edit_task', task.title, {
				taskId: task.id, taskTitle: task.title,
				field: 'status', value: nextStatus, before: resolveColumn(task.status)
			});
			statusMenuTaskId = '';
			return;
		}

		savingStatusTaskId = task.id;
		clearBoardError();
		try {
			if (contextAware) {
				await moveContextTaskToColumn(task.id, nextStatus);
			} else {
				await moveTaskToColumn(task.id, nextStatus);
			}
		} catch (error) {
			setBoardError(error instanceof Error ? error.message : 'Failed to update task status');
		} finally {
			if (savingStatusTaskId === task.id) {
				savingStatusTaskId = '';
			}
			statusMenuTaskId = '';
		}
	}

	async function toggleDone(task: DisplayTask) {
		const next = resolveColumn(task.status) === 'done' ? 'todo' : 'done';
		await applyStatus(task, next);
	}

	function ownerLabel(task: DisplayTask) {
		const metadataOwner = readDescriptionMetadataValue(task.description, 'owner');
		const assigneeOption = getOwnerOptionById(task.assigneeId);
		const raw =
			assigneeOption?.label?.trim() ||
			task.assigneeId.trim() ||
			metadataOwner.trim() ||
			task.statusActorName?.trim() ||
			task.statusActorId?.trim() ||
			'';
		if (!raw) {
			return 'Unassigned';
		}
		if (/^[0-9a-f]{8}-[0-9a-f-]{27}$/i.test(raw)) {
			return `User ${raw.slice(0, 8)}`;
		}
		return raw.replace(/_/g, ' ');
	}

	function initials(value: string) {
		const trimmed = value.trim();
		if (!trimmed || trimmed.toLowerCase() === 'unassigned') {
			return '--';
		}
		const parts = trimmed.split(/\s+/).filter(Boolean);
		if (parts.length === 1) {
			return parts[0].slice(0, 2).toUpperCase();
		}
		return `${parts[0][0] ?? ''}${parts[1][0] ?? ''}`.toUpperCase();
	}

	function hueFromSeed(seed: string) {
		let hash = 0;
		for (let index = 0; index < seed.length; index += 1) {
			hash = (hash << 5) - hash + seed.charCodeAt(index);
			hash |= 0;
		}
		return Math.abs(hash) % 360;
	}

	function ownerHue(task: DisplayTask) {
		return hueFromSeed(ownerLabel(task));
	}

	function concernedMemberLabel(memberId: string) {
		const ownerOption = getOwnerOptionById(memberId);
		if (ownerOption?.label) {
			return ownerOption.label;
		}
		const trimmed = memberId.trim();
		if (!trimmed) {
			return 'Unassigned';
		}
		if (/^[0-9a-f]{8}-[0-9a-f-]{27}$/i.test(trimmed)) {
			return `User ${trimmed.slice(0, 8)}`;
		}
		return trimmed.replace(/_/g, ' ');
	}

	function supportLinkedTaskTitle(linkedTaskId: string) {
		const linkedTask = boardTasks.find((task) => task.id === linkedTaskId);
		if (linkedTask?.title?.trim()) {
			return linkedTask.title.trim();
		}
		return linkedTaskId.slice(0, 8);
	}

	function formatCellTime(value: number) {
		if (!Number.isFinite(value) || value <= 0) {
			return 'just now';
		}
		return new Date(value).toLocaleString([], {
			month: 'short',
			day: 'numeric',
			hour: 'numeric',
			minute: '2-digit'
		});
	}

	function formatBudgetCell(value?: number) {
		if (!Number.isFinite(value) || (value ?? 0) <= 0) {
			return '--';
		}
		return new Intl.NumberFormat(undefined, {
			style: 'currency',
			currency: 'USD',
			maximumFractionDigits: 0
		}).format(value as number);
	}

	function formatSpentCell(spent?: number, budget?: number) {
		const normalizedBudget =
			typeof budget === 'number' && Number.isFinite(budget) && budget > 0 ? budget : 0;
		const hasSpent = typeof spent === 'number' && Number.isFinite(spent) && spent >= 0;
		if (!hasSpent && normalizedBudget <= 0) {
			return '--';
		}
		const normalizedSpent = hasSpent ? (spent as number) : 0;
		const spentLabel = new Intl.NumberFormat(undefined, {
			style: 'currency',
			currency: 'USD',
			maximumFractionDigits: 0
		}).format(normalizedSpent);
		if (normalizedBudget <= 0) {
			return spentLabel;
		}
		const budgetLabel = new Intl.NumberFormat(undefined, {
			style: 'currency',
			currency: 'USD',
			maximumFractionDigits: 0
		}).format(normalizedBudget);
		return `${spentLabel} / ${budgetLabel}`;
	}

	function queueFollowUp(task: DisplayTask) {
		if (!canEdit) {
			return;
		}
		const followUpLine = `Follow up: ${task.title}`;
		void openSprintComposer({
			sprintName: task.sprintName.trim(),
			taskLine: followUpLine
		});
	}

	function toggleSupportTask(taskId: string) {
		if (!canEdit) {
			return;
		}
		selectedSupportTaskIds = selectedSupportTaskIds.includes(taskId)
			? selectedSupportTaskIds.filter((value) => value !== taskId)
			: [...selectedSupportTaskIds, taskId];
	}

	function toggleConcernedMember(memberId: string) {
		if (!canEdit) {
			return;
		}
		selectedConcernedMemberIds = selectedConcernedMemberIds.includes(memberId)
			? selectedConcernedMemberIds.filter((value) => value !== memberId)
			: [...selectedConcernedMemberIds, memberId];
	}

	function resetSupportComposer() {
		supportTicketTitle = '';
		supportTicketDetails = '';
		supportTicketPriority = 'medium';
		selectedSupportTaskIds = [];
		selectedConcernedMemberIds = [];
	}

	async function createSupportTicket() {
		if (supportTicketCreating || !canEdit) {
			return;
		}
		const normalizedTitle = supportTicketTitle.trim();
		if (!normalizedTitle) {
			setBoardError('Support ticket title is required');
			return;
		}
		if (selectedSupportTaskIds.length === 0) {
			setBoardError('Select at least one task for the support ticket');
			return;
		}
		const targetRoomId = normalizeRoomIDValue(
			contextAware ? ($activeContext.type === 'room' ? $activeContext.id : '') : normalizedRoomId
		);
		if (!targetRoomId) {
			setBoardError('Invalid room id');
			return;
		}

		const linkedTaskIds = [...selectedSupportTaskIds];
		const linkedTaskTitleList = linkedTaskIds.map((linkedTaskId) =>
			supportLinkedTaskTitle(linkedTaskId)
		);
		const concernedMemberIds = [...selectedConcernedMemberIds];
		const descriptionBody = supportTicketDetails.trim();
		const metadataSegments = [
			'Ticket: Support',
			`Priority: ${supportPriorityLabel(supportTicketPriority)}`,
			`Linked Tasks: ${linkedTaskIds.join(', ')}`,
			`Linked Task Titles: ${linkedTaskTitleList.join(' / ')}`
		];
		if (concernedMemberIds.length > 0) {
			metadataSegments.push(`Concerned: ${concernedMemberIds.join(', ')}`);
		}
		const description = `${descriptionBody || 'Support ticket'}\n\n[${metadataSegments.join(' | ')}]`;
		const requestTitle = normalizedTitle.toLowerCase().startsWith('support:')
			? normalizedTitle
			: `Support: ${normalizedTitle}`;
		const sprintName = supportCurrentSprintName === 'Backlog' ? '' : supportCurrentSprintName;

		supportTicketCreating = true;
		clearBoardError();
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(targetRoomId)}/tasks`,
				{
					method: 'POST',
					headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
					credentials: 'include',
					body: JSON.stringify({
						title: requestTitle,
						description,
						sprint_name: sprintName,
						status: 'todo',
						task_type: 'support'
					})
				}
			);
			if (!response.ok) {
				throw new Error(await parseErrorMessage(response));
			}
			const createdPayload = await response.json().catch(() => null);
			if (contextAware) {
				const createdTask = normalizeRoomTask(createdPayload);
				if (!createdTask) {
					throw new Error('Invalid support ticket response');
				}
				contextTasks = [createdTask, ...contextTasks];
				sendSocketPayload(buildTaskSocketPayload('task_create', targetRoomId, createdTask));
				publishRoomBoardActivity(targetRoomId, {
					type: 'task_added',
					title: `Added ${createdTask.title}`,
					subtitle: 'Created support ticket',
					actor: sessionUsername || sessionUserID || 'Unknown'
				});
			} else {
				const createdTask = upsertTaskStoreEntry(createdPayload, targetRoomId);
				if (!createdTask) {
					throw new Error('Invalid support ticket response');
				}
				sendSocketPayload(buildTaskSocketPayload('task_create', targetRoomId, createdTask));
				publishRoomBoardActivity(targetRoomId, {
					type: 'task_added',
					title: `Added ${createdTask.title}`,
					subtitle: 'Created support ticket',
					actor: sessionUsername || sessionUserID || 'Unknown'
				});
			}
			resetSupportComposer();
		} catch (error) {
			setBoardError(error instanceof Error ? error.message : 'Failed to create support ticket');
		} finally {
			supportTicketCreating = false;
		}
	}

	// ── Multi-select helpers ──────────────────────────────────────────
	function isTaskSelected(taskId: string) {
		return selectedTaskIds.includes(taskId);
	}

	function isTaskDeleting(taskId: string) {
		return deletingTaskIds.includes(taskId);
	}

	function toggleTaskSelection(taskId: string) {
		const next = selectedTaskIds.includes(taskId)
			? selectedTaskIds.filter((entry) => entry !== taskId)
			: [...selectedTaskIds, taskId];
		selectedTaskIds = next;
	}

	function sprintSelectedCount(group: SprintTaskGroup) {
		return group.tasks.filter((t) => selectedTaskIds.includes(t.id)).length;
	}

	function isSprintAllSelected(group: SprintTaskGroup) {
		return group.tasks.length > 0 && group.tasks.every((t) => selectedTaskIds.includes(t.id));
	}

	function toggleSprintSelection(group: SprintTaskGroup) {
		const next = new Set(selectedTaskIds);
		if (isSprintAllSelected(group)) {
			group.tasks.forEach((t) => next.delete(t.id));
		} else {
			group.tasks.forEach((t) => next.add(t.id));
		}
		selectedTaskIds = Array.from(next);
	}

	async function deleteTask(task: DisplayTask): Promise<boolean> {
		const targetRoomId = normalizeRoomIDValue(task.roomId || normalizedRoomId);
		if (!targetRoomId) return false;
		const response = await fetch(
			`${API_BASE}/api/rooms/${encodeURIComponent(targetRoomId)}/tasks/${encodeURIComponent(task.id)}`,
			{ method: 'DELETE', headers: withSessionUserHeaders(), credentials: 'include' }
		);
		if (!response.ok) throw new Error(await parseErrorMessage(response));
		if (contextAware) {
			contextTasks = contextTasks.filter((entry) => entry.id !== task.id);
		} else {
			removeTaskStoreEntry(task.id, targetRoomId);
		}
		sendSocketPayload(buildTaskSocketPayload('task_delete', targetRoomId, task));
		publishRoomBoardActivity(targetRoomId, {
			type: 'task_deleted',
			title: `Deleted ${task.title}`,
			subtitle: 'Removed task',
			actor: sessionUsername || sessionUserID || 'Unknown'
		});
		return true;
	}

	async function deleteSelectedInSprint(group: SprintTaskGroup) {
		if (!canEdit) return;
		const toDelete = group.tasks.filter((t) => selectedTaskIds.includes(t.id));
		if (!toDelete.length) return;

		// Non-admins submit change requests for each selected task deletion
		if (!isAdmin) {
			for (const t of toDelete) {
				submitChangeRequest(roomId, sessionUserID, sessionUserName, 'delete_task', t.title, {
					taskId: t.id, taskTitle: t.title, sprintName: group.name
				});
			}
			// Clear selection and exit edit mode
			const selectedNext = new Set(selectedTaskIds);
			toDelete.forEach((t) => selectedNext.delete(t.id));
			selectedTaskIds = Array.from(selectedNext);
			return;
		}

		const deletingNext = new Set(deletingTaskIds);
		toDelete.forEach((t) => deletingNext.add(t.id));
		deletingTaskIds = Array.from(deletingNext);
		roomBoardError = '';
		try {
			await Promise.all(toDelete.map((t) => deleteTask(t)));
			const selectedNext = new Set(selectedTaskIds);
			toDelete.forEach((t) => selectedNext.delete(t.id));
			selectedTaskIds = Array.from(selectedNext);
		} catch (error) {
			roomBoardError = error instanceof Error ? error.message : 'Failed to delete tasks';
		} finally {
			const deletingClear = new Set(deletingTaskIds);
			toDelete.forEach((t) => deletingClear.delete(t.id));
			deletingTaskIds = Array.from(deletingClear);
		}
	}

	async function createRoomTaskInSprint(content: string, sprintName: string, meta?: SprintComposerMeta) {
		const normalizedContent = content.trim();
		if (!normalizedContent) {
			return;
		}
		const targetRoomId = normalizeRoomIDValue(
			contextAware ? ($activeContext.type === 'room' ? $activeContext.id : '') : normalizedRoomId
		);
		if (!targetRoomId) {
			throw new Error('Invalid room id');
		}
		const normalizedSprintName = sprintName.trim();
		const body: Record<string, unknown> = { content: normalizedContent, sprint_name: normalizedSprintName };
		if (meta?.status && meta.status !== 'todo') body.status = meta.status;
		if (meta?.assigneeId) body.assignee_id = meta.assigneeId;
		if (meta?.budget) { const b = parseFloat(meta.budget); if (!isNaN(b) && b > 0) body.budget = b; }
		if (meta?.spent) { const s = parseFloat(meta.spent); if (!isNaN(s) && s > 0) body.spent = s; }
		const response = await fetch(
			`${API_BASE}/api/rooms/${encodeURIComponent(targetRoomId)}/tasks`,
			{
				method: 'POST',
				headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
				credentials: 'include',
				body: JSON.stringify(body)
			}
		);
		if (!response.ok) {
			throw new Error(await parseErrorMessage(response));
		}
		const payload = await response.json().catch(() => null);
		if (contextAware) {
			const created = normalizeRoomTask(payload);
			if (!created) {
				throw new Error('Invalid task response');
			}
			contextTasks = [created, ...contextTasks];
			sendSocketPayload(buildTaskSocketPayload('task_create', targetRoomId, created));
			publishRoomBoardActivity(targetRoomId, {
				type: 'task_added',
				title: `Added ${created.title}`,
				subtitle: normalizedSprintName ? `Added to ${normalizedSprintName}` : 'Created task',
				actor: sessionUsername || sessionUserID || 'Unknown'
			});
			return;
		}
		const created = upsertTaskStoreEntry(payload, targetRoomId);
		if (!created) {
			throw new Error('Invalid task response');
		}
		sendSocketPayload(buildTaskSocketPayload('task_create', targetRoomId, created));
		publishRoomBoardActivity(targetRoomId, {
			type: 'task_added',
			title: `Added ${created.title}`,
			subtitle: normalizedSprintName ? `Added to ${normalizedSprintName}` : 'Created task',
			actor: sessionUsername || sessionUserID || 'Unknown'
		});
	}

	async function submitSprintComposer() {
		if (sprintComposerSaving || !canCreateSprintTask || !canEdit) {
			return;
		}
		// Always flush in-progress title edit regardless of whether blur already fired
		commitSprintComposerTaskEdit();
		const normalizedSprintName = sprintComposerName.trim();
		if (!normalizedSprintName) {
			setBoardError(`${groupTerm} name is required`);
			await tick();
			sprintComposerNameInput?.focus();
			return;
		}

		const taskTitles = parseSprintComposerTasks(sprintComposerTaskDrafts);
		const capturedMeta = { ...sprintComposerRowMeta };

		// Non-admins submit a change request with the full sprint data
		if (!isAdmin) {
			const tasks = taskTitles.map((title, i) => {
				const meta = capturedMeta[i];
				return { title, status: meta?.status ?? 'todo', assigneeId: meta?.assigneeId ?? '', budget: meta?.budget ?? '' };
			});
			submitChangeRequest(roomId, sessionUserID, sessionUserName, 'add_sprint', normalizedSprintName, {
				sprintName: normalizedSprintName, tasks
			});
			sprintComposerOpen = false;
			sprintComposerName = '';
			sprintComposerTaskDrafts = [];
			sprintComposerTaskInputValue = '';
			sprintComposerActiveTaskIndex = -1;
			sprintComposerRowMeta = {};
			return;
		}

		sprintComposerSaving = true;
		clearBoardError();
		try {
			rememberSprintDraftGroup(normalizedSprintName);
			for (let i = 0; i < taskTitles.length; i++) {
				await createRoomTaskInSprint(taskTitles[i], normalizedSprintName, capturedMeta[i]);
			}
			sprintComposerOpen = false;
			sprintComposerName = '';
			sprintComposerTaskDrafts = [];
			sprintComposerTaskInputValue = '';
			sprintComposerActiveTaskIndex = -1;
			sprintComposerRowMeta = {};
		} catch (error) {
			setBoardError(error instanceof Error ? error.message : `Failed to create ${groupLabel}`);
		} finally {
			sprintComposerSaving = false;
		}
	}

	async function handleCreateRoomTaskInSprint(content: string, sprintName: string) {
		if (sprintAddCreating || !content.trim()) return;

		// Non-admins submit a change request for task addition
		if (!isAdmin) {
			submitChangeRequest(roomId, sessionUserID, sessionUserName, 'add_task', content.trim(), {
				taskTitle: content.trim(), sprintName
			});
			sprintAddContent = '';
			sprintAddKey = '';
			return;
		}

		sprintAddCreating = true;
		clearBoardError();
		try {
			await createRoomTaskInSprint(content, sprintName);
			sprintAddContent = '';
			sprintAddKey = '';
		} catch (error) {
			setBoardError(error instanceof Error ? error.message : 'Failed to create task');
		} finally {
			sprintAddCreating = false;
		}
	}

	function registerSprintAddForm(node: HTMLFormElement, sprintKey: string) {
		let currentKey = sprintKey;
		sprintAddFormByKey.set(currentKey, node);
		return {
			update(nextKey: string) {
				if (nextKey === currentKey) {
					return;
				}
				sprintAddFormByKey.delete(currentKey);
				currentKey = nextKey;
				sprintAddFormByKey.set(currentKey, node);
			},
			destroy() {
				sprintAddFormByKey.delete(currentKey);
			}
		};
	}

	async function toggleSprintAddComposer(sprintKey: string) {
		const nextKey = sprintAddKey === sprintKey ? '' : sprintKey;
		sprintAddKey = nextKey;
		sprintAddContent = '';
		if (!nextKey) {
			return;
		}
		await tick();
		const targetForm = sprintAddFormByKey.get(nextKey);
		if (!targetForm) {
			return;
		}
		targetForm.scrollIntoView({
			behavior: 'smooth',
			block: 'center',
			inline: 'nearest'
		});
		targetForm.querySelector<HTMLInputElement>("input[type='text']")?.focus();
	}

	function handleBoardSubviewEditTask(event: CustomEvent<{ taskId?: string }>) {
		if (!canEdit) {
			return;
		}
		const taskID = event.detail?.taskId?.trim() || '';
		if (!taskID) {
			return;
		}
		const task = boardTasks.find((entry) => entry.id === taskID);
		if (!task) {
			return;
		}
		void startEditing(task, 'title', 'inline');
	}

	function onWindowClick() {
		statusMenuTaskId = '';
	}
</script>

<svelte:window on:click={onWindowClick} />

<section class="task-board" aria-label="Task board">
	{#if boardToastMessage}
		<div class="board-toast" role="status" aria-live="polite">{boardToastMessage}</div>
	{/if}
	{#if boardView === 'table' || boardView === 'kanban'}
		<!-- ── Unified Board Toolbar ─────────────────────────── -->
		<header class="board-toolbar" aria-label="Board toolbar">
			{#if searchExpanded && boardView === 'table'}
				<div class="btb-search-bar">
					<svg class="btb-search-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M21 21l-4.35-4.35M17 11A6 6 0 1 1 5 11a6 6 0 0 1 12 0z"/></svg>
					<input
						type="search"
						class="btb-search-input"
						bind:value={taskSearchQuery}
						placeholder={`Search ${groupLabel}, ${taskLabel}, assignee, status…`}
						autocomplete="off"
						autofocus
					/>
					<button
						type="button"
						class="btb-search-close"
						on:click={() => { searchExpanded = false; taskSearchQuery = ''; }}
						aria-label="Close search"
					>
						<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12"/></svg>
					</button>
				</div>
			{:else}
				<div class="btb-title">
					<h2>{boardTitle}</h2>
					<div class="btb-meta">
						<span class="btb-count">
							{boardTasks.length} {boardTasks.length === 1 ? taskLabel : taskPluralLabel}
						</span>
						{#if boardLastUpdatedAt > 0}
							<span class="btb-sep" aria-hidden="true">·</span>
							<span class="btb-updated">{formatCellTime(boardLastUpdatedAt)}</span>
						{/if}
					</div>
				</div>
				<div class="btb-actions">
					{#if boardView === 'table'}
						<button
							type="button"
							class="btb-icon-btn"
							class:is-active={hasActiveTaskSearch}
							on:click={() => (searchExpanded = true)}
							aria-label={`Search ${taskPluralLabel}`}
							title={`Search ${taskPluralLabel}`}
						>
							<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M21 21l-4.35-4.35M17 11A6 6 0 1 1 5 11a6 6 0 0 1 12 0z"/></svg>
						</button>
					{/if}
					{#if isAdmin}
						<button
							type="button"
							class="btb-add-sprint"
							on:click={() => void openSprintComposer()}
							disabled={!canEdit || !canCreateSprintTask || sprintComposerSaving}
						>
							<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>
							<span>Add {groupTerm}</span>
						</button>
					{:else if canEdit}
						<button
							type="button"
							class="btb-add-sprint btb-request-sprint"
							on:click={() => void openSprintComposer()}
							disabled={sprintComposerSaving}
						>
							<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm1 15h-2v-2h2v2zm0-4h-2V7h2v6z" fill="currentColor"/></svg>
							<span>Request {groupTerm}</span>
						</button>
					{/if}
				</div>
			{/if}
		</header>
	{/if}

	{#if (boardView === 'table' || boardView === 'kanban') && sprintComposerOpen}
		<section class="sprint-composer" aria-label={`Create ${groupLabel}`}>
			{#if sprintComposerOpen}
				<form
					class="sprint-composer-form"
					on:submit|preventDefault={() => void submitSprintComposer()}
				>
					<label class="sprint-composer-field">
						<span>{groupTerm} name</span>
						<input
							bind:this={sprintComposerNameInput}
							type="text"
							bind:value={sprintComposerName}
							placeholder={`${groupTerm} 5 - Planning`}
							autocomplete="off"
							disabled={sprintComposerSaving}
							maxlength="160"
						/>
					</label>
					<section class="sprint-preview-grid" aria-label={`${groupTerm} ${taskPluralLabel} preview`}>
						<div class="sprint-preview-head">
							<span>{taskTerm}</span>
							<span>Status</span>
							<span>Assignee</span>
							<span>Budget</span>
							<span>Cost</span>
							<span>Updated</span>
						</div>
						<div class="sprint-preview-body">
							{#each sprintComposerPreviewRows as previewRow (previewRow.index)}
								<div class="sprint-preview-row">
									<div class="sprint-preview-cell sprint-preview-task-cell">
										{#if sprintComposerActiveTaskIndex === previewRow.index}
											<input
												bind:this={sprintComposerTaskInput}
												type="text"
												class="sprint-preview-task-input"
												bind:value={sprintComposerTaskInputValue}
												on:keydown={onSprintComposerTaskInputKeydown}
												on:blur={commitSprintComposerTaskEdit}
												placeholder={`${taskTerm} title...`}
												autocomplete="off"
												maxlength="220"
											/>
										{:else}
											<button
												type="button"
												class="sprint-preview-task-btn"
												class:is-empty={!previewRow.title}
												on:click={() => void startSprintComposerTaskEdit(previewRow.index)}
												disabled={sprintComposerSaving || !canCreateSprintTask}
											>
												{previewRow.title || `Click any cell in this row to add ${taskLabel}`}
											</button>
										{/if}
									</div>
									<!-- Status -->
									<div class="sprint-preview-cell">
										<select
											class="spc-select"
											value={previewRow.status}
											on:change={(e) => setSprintComposerMeta(previewRow.index, 'status', e.currentTarget.value)}
											disabled={sprintComposerSaving || !canCreateSprintTask}
										>
											{#each STATUS_OPTIONS as opt (opt.value)}
												<option value={opt.value}>{opt.label}</option>
											{/each}
										</select>
									</div>
									<!-- Assignee -->
									<div class="sprint-preview-cell">
										<select
											class="spc-select"
											value={previewRow.assigneeId}
											on:change={(e) => setSprintComposerMeta(previewRow.index, 'assigneeId', e.currentTarget.value)}
											disabled={sprintComposerSaving || !canCreateSprintTask}
										>
											<option value="">Unassigned</option>
											{#each ownerOptions as opt (opt.id)}
												<option value={opt.id}>{opt.label}</option>
											{/each}
										</select>
									</div>
									<!-- Budget -->
									<div class="sprint-preview-cell">
										<input
											class="spc-num-input"
											type="number"
											min="0"
											step="0.01"
											placeholder="0"
											value={previewRow.budget}
											on:change={(e) => setSprintComposerMeta(previewRow.index, 'budget', e.currentTarget.value)}
											disabled={sprintComposerSaving || !canCreateSprintTask}
										/>
									</div>
									<!-- Cost / Spent -->
									<div class="sprint-preview-cell">
										<input
											class="spc-num-input"
											type="number"
											min="0"
											step="0.01"
											placeholder="0"
											value={previewRow.spent}
											on:change={(e) => setSprintComposerMeta(previewRow.index, 'spent', e.currentTarget.value)}
											disabled={sprintComposerSaving || !canCreateSprintTask}
										/>
									</div>
									<!-- Updated — not settable at creation -->
									<div class="sprint-preview-cell spc-static">—</div>
								</div>
							{/each}
						</div>
					</section>
					<div class="sprint-composer-actions">
						<button
							type="button"
							class="spc-add-row-btn"
							on:click={addSprintComposerRow}
							disabled={sprintComposerSaving || !canCreateSprintTask || parseSprintComposerTasks(sprintComposerTaskDrafts).length >= SPRINT_COMPOSER_MAX_TASKS}
						>
							<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14"></path></svg>
							Add {taskLabel}
						</button>
						<div class="spc-actions-right">
							<button type="button" class="sprint-composer-cancel" on:click={closeSprintComposer}>
								Cancel
							</button>
							<button
							type="submit"
							class="sprint-composer-submit"
							disabled={sprintComposerSaving || !sprintComposerName.trim()}
						>
							{sprintComposerSaving
								? 'Submitting…'
								: isAdmin
									? `Create ${groupLabel}`
									: `Request ${groupTerm}`}
						</button>
						</div>
					</div>
				</form>
			{/if}
		</section>
	{/if}

	<ChangeRequestModal
		open={crModalOpen}
		{roomId}
		userId={sessionUserID}
		userName={sessionUserName}
		action={crModalAction}
		targetLabel={crModalTargetLabel}
		payload={crModalPayload}
		on:submitted={() => (crModalOpen = false)}
		on:cancel={() => (crModalOpen = false)}
	/>

	{#if quickEditVisible && editingTask && editingField}
		<section class="quick-edit-panel" aria-label="Task quick editor">
			<div class="quick-edit-head">
				<strong>Editing {fieldLabel(editingField)} for "{editingTask.title}"</strong>
			</div>
			<div class="quick-edit-controls">
				{#if editingField === 'assigneeId'}
					{#if ownerOptions.length > 0}
						<select
							bind:this={quickEditorElement}
							class="quick-edit-input"
							bind:value={editingValue}
							on:keydown={(event) => onOwnerSelectKeyDown(event, editingTask)}
						>
							<option value="">Unassigned</option>
							{#each ownerOptionsForTask(editingTask) as ownerOption (ownerOption.id)}
								<option value={ownerOption.id}>
									{ownerOption.label}{ownerOption.isOnline ? '' : ' (offline)'}
								</option>
							{/each}
						</select>
					{:else}
						<input
							bind:this={quickEditorElement}
							class="quick-edit-input"
							type="text"
							bind:value={editingValue}
							placeholder="Assignee"
							on:keydown={(event) => onEditorKeyDown(event, editingTask, 'assigneeId')}
						/>
					{/if}
				{:else if editingField === 'budget' || editingField === 'spent'}
					<input
						bind:this={quickEditorElement}
						class="quick-edit-input"
						type="number"
						inputmode="decimal"
						min="0"
						step="0.01"
						bind:value={editingValue}
						placeholder="0"
						on:keydown={(event) =>
							onEditorKeyDown(event, editingTask, editingField === 'spent' ? 'spent' : 'budget')}
					/>
				{:else if editingField === 'dueDate' || editingField === 'startDate'}
					<input
						bind:this={quickEditorElement}
						class="quick-edit-input"
						type="date"
						bind:value={editingValue}
						on:keydown={(event) =>
							onEditorKeyDown(
								event,
								editingTask,
								editingField === 'dueDate' ? 'dueDate' : 'startDate'
							)}
					/>
				{:else}
					<input
						bind:this={quickEditorElement}
						class="quick-edit-input"
						type="text"
						bind:value={editingValue}
						placeholder="Task name"
						on:keydown={(event) => onEditorKeyDown(event, editingTask, 'title')}
					/>
				{/if}
				<button
					type="button"
					class="quick-edit-btn quick-edit-save"
					on:click={() => void saveQuickEditor()}
					disabled={isSaving(editingTask.id, editingField) || savingRelations}
				>
					Save
				</button>
				<button type="button" class="quick-edit-btn" on:click={closeQuickEditor}>Cancel</button>
			</div>
			{#if editingTask.source === 'room' && roomFieldSchemas.length > 0}
				<div class="quick-custom-fields">
					<h4>Custom fields</h4>
					<div class="quick-custom-fields-grid">
						{#each roomFieldSchemas as schema (schema.fieldId)}
							<label class="quick-custom-field">
								<span>{schema.name}</span>
								{#if normalizeCustomFieldType(schema.fieldType) === 'checkbox'}
									<input
										type="checkbox"
										checked={Boolean(editingCustomFields[schema.fieldId])}
										on:change={(event) => {
											const target = event.currentTarget as HTMLInputElement;
											editingCustomFields = {
												...editingCustomFields,
												[schema.fieldId]: target.checked
											};
										}}
									/>
								{:else if normalizeCustomFieldType(schema.fieldType) === 'select'}
									<select
										value={toStringValue(editingCustomFields[schema.fieldId])}
										on:change={(event) => {
											const target = event.currentTarget as HTMLSelectElement;
											editingCustomFields = {
												...editingCustomFields,
												[schema.fieldId]: target.value
											};
										}}
									>
										<option value="">Select</option>
										{#each schema.options ?? [] as option (option)}
											<option value={option}>{option}</option>
										{/each}
									</select>
								{:else if normalizeCustomFieldType(schema.fieldType) === 'multi_select'}
									<div class="quick-custom-options">
										{#if schema.options && schema.options.length > 0}
											{#each schema.options as option (option)}
												<label class="quick-custom-option">
													<input
														type="checkbox"
														checked={isCustomFieldOptionSelected(schema.fieldId, option)}
														on:change={(event) => {
															const target = event.currentTarget as HTMLInputElement;
															toggleCustomFieldOption(schema.fieldId, option, target.checked);
														}}
													/>
													<span>{option}</span>
												</label>
											{/each}
										{:else}
											<span class="quick-custom-empty">No options configured</span>
										{/if}
									</div>
								{:else}
									<input
										type={normalizeCustomFieldType(schema.fieldType) === 'number'
											? 'number'
											: normalizeCustomFieldType(schema.fieldType) === 'date'
												? 'date'
												: normalizeCustomFieldType(schema.fieldType) === 'url'
													? 'url'
													: 'text'}
										value={toStringValue(editingCustomFields[schema.fieldId])}
										on:input={(event) => {
											const target = event.currentTarget as HTMLInputElement;
											editingCustomFields = {
												...editingCustomFields,
												[schema.fieldId]: target.value
											};
										}}
									/>
								{/if}
							</label>
						{/each}
					</div>
				</div>
			{/if}
			{#if editingTask.source === 'room'}
				<div class="quick-relations">
					<h4>Dependencies</h4>
					{#if relationTaskOptions(editingTask).length === 0}
						<p class="quick-custom-empty">No other tasks available for dependencies.</p>
					{:else}
						<div class="quick-relation-list">
							{#each relationTaskOptions(editingTask) as dependencyTask (dependencyTask.id)}
								<label class="quick-relation-option">
									<input
										type="checkbox"
										checked={isEditingDependencySelected(dependencyTask.id)}
										on:change={(event) =>
											toggleEditingDependency(
												dependencyTask.id,
												(event.currentTarget as HTMLInputElement).checked
											)}
									/>
									<span>{dependencyTask.title}</span>
								</label>
							{/each}
						</div>
					{/if}

					<h4>Subtasks</h4>
					<div class="quick-subtask-list">
						{#if editingSubtasks.length === 0}
							<p class="quick-custom-empty">No subtasks yet.</p>
						{:else}
							{#each editingSubtasks as subtask (subtask.id)}
								<div class="quick-subtask-row">
									<input
										type="checkbox"
										checked={subtask.completed}
										on:change={(event) =>
											updateEditingSubtaskCompleted(
												subtask.id,
												(event.currentTarget as HTMLInputElement).checked
											)}
									/>
									<input
										type="text"
										value={subtask.content}
										on:input={(event) =>
											updateEditingSubtaskContent(
												subtask.id,
												(event.currentTarget as HTMLInputElement).value
											)}
									/>
									<button
										type="button"
										class="quick-subtask-remove"
										on:click={() => removeEditingSubtask(subtask.id)}
										aria-label="Remove subtask"
									>
										Remove
									</button>
								</div>
							{/each}
						{/if}
					</div>
					<div class="quick-subtask-actions">
						<button
							type="button"
							class="quick-edit-btn"
							on:click={addEditingSubtask}
							disabled={savingRelations}
						>
							+ Add subtask
						</button>
					</div>
				</div>
			{/if}
		</section>
	{/if}

	<div class="board-content-slot">
		{#if boardLoading}
			<div class="board-state">Loading {taskPluralLabel}...</div>
		{:else if boardError}
			<div class="board-state error">Unable to load {taskPluralLabel}: {boardError}</div>
		{:else if !hasBoardDataForView}
			<div class="board-state">
				{#if boardView === 'table' && hasActiveTaskSearch}
					No {taskPluralLabel} matched "{taskSearchQuery.trim()}".
				{:else if contextAware}
					No {taskPluralLabel} yet. Use + Add {groupTerm} to start planning.
				{:else}
					No {taskPluralLabel} yet. Add one to start planning.
				{/if}
			</div>
		{:else if boardView === 'calendar'}
			<CalendarView
				tasks={calendarWorkloadTasks}
				fieldSchemas={roomFieldSchemas}
				{onlineMembers}
				on:editTask={handleBoardSubviewEditTask}
			/>
		{:else if boardView === 'workload'}
			<WorkloadView
				tasks={calendarWorkloadTasks}
				fieldSchemas={roomFieldSchemas}
				{onlineMembers}
				on:editTask={handleBoardSubviewEditTask}
			/>
		{:else if boardView === 'support'}
			<div class="support-view" aria-label="Support ticket board">
				<section class="support-composer">
					<header class="support-composer-head">
						<div>
							<h3>Support Tickets</h3>
							<p>Current {groupLabel}: {formatSprintDisplayName(supportCurrentSprintName)}</p>
						</div>
						<span>{supportTicketCards.length} tickets</span>
					</header>

					<div class="support-form-grid">
						<label class="support-field">
							<span>Ticket title</span>
							<input
								type="text"
								bind:value={supportTicketTitle}
								placeholder="Payment failure on checkout"
								disabled={!canEdit || supportTicketCreating}
							/>
						</label>
						<label class="support-field">
							<span>Priority</span>
							<select
								bind:value={supportTicketPriority}
								disabled={!canEdit || supportTicketCreating}
							>
								{#each SUPPORT_PRIORITY_OPTIONS as priorityOption (priorityOption.value)}
									<option value={priorityOption.value}>{priorityOption.label}</option>
								{/each}
							</select>
						</label>
						<label class="support-field support-field-wide">
							<span>Details</span>
							<textarea
								bind:value={supportTicketDetails}
								placeholder="Add context for support engineers"
								maxlength="1200"
								disabled={!canEdit || supportTicketCreating}
							></textarea>
						</label>
					</div>

					<div class="support-pickers">
						<div class="support-picker">
							<div class="support-picker-head">
								<h4>Choose {taskLabel}(s)</h4>
								<span>{selectedSupportTaskIds.length} selected</span>
							</div>
							{#if supportSourceTasksForSprint.length === 0}
								<div class="support-empty-inline">No {taskPluralLabel} in {formatSprintDisplayName(supportCurrentSprintName)}.</div>
							{:else}
								<div class="support-task-list">
									{#each supportSourceTasksForSprint as sourceTask (sourceTask.id)}
										<label class="support-check-row">
											<input
												type="checkbox"
												checked={selectedSupportTaskIds.includes(sourceTask.id)}
												on:change={() => toggleSupportTask(sourceTask.id)}
												disabled={!canEdit || supportTicketCreating}
											/>
											<span>{sourceTask.title}</span>
										</label>
									{/each}
								</div>
							{/if}
						</div>

						<div class="support-picker">
							<div class="support-picker-head">
								<h4>Concerned (online)</h4>
								<span>{selectedConcernedMemberIds.length} selected</span>
							</div>
							{#if onlineConcernedOptions.length === 0}
								<div class="support-empty-inline">No online members right now.</div>
							{:else}
								<div class="support-member-grid">
									{#each onlineConcernedOptions as memberOption (memberOption.id)}
										<button
											type="button"
											class="support-member-option"
											class:is-selected={selectedConcernedMemberIds.includes(memberOption.id)}
											on:click={() => toggleConcernedMember(memberOption.id)}
											disabled={!canEdit || supportTicketCreating}
										>
											<span
												class="owner-avatar"
												style={`--owner-hue:${hueFromSeed(memberOption.label)};`}
											>
												{initials(memberOption.label)}
											</span>
											<span>{memberOption.label}</span>
										</button>
									{/each}
								</div>
							{/if}
						</div>
					</div>

					<div class="support-composer-actions">
						<button
							type="button"
							class="support-create-btn"
							on:click={() => void createSupportTicket()}
							disabled={!canEdit ||
								supportTicketCreating ||
								!supportTicketTitle.trim() ||
								selectedSupportTaskIds.length === 0}
						>
							{supportTicketCreating ? 'Creating...' : 'Create support ticket'}
						</button>
						<button
							type="button"
							class="support-clear-btn"
							on:click={resetSupportComposer}
							disabled={!canEdit || supportTicketCreating}
						>
							Clear
						</button>
					</div>
				</section>

				<section class="support-ticket-board">
					<header class="support-ticket-head">
						<h3>Tickets in {formatSprintDisplayName(supportCurrentSprintName)}</h3>
						<span>{supportTicketCards.length}</span>
					</header>
					{#if supportTicketCards.length === 0}
						<div class="support-empty">No support tickets yet for this {groupLabel}.</div>
					{:else}
						<div class="support-card-grid">
							{#each supportTicketCards as supportCard (supportCard.task.id)}
								<article class="support-ticket-card">
									<div class="support-ticket-top">
										<span class={`support-priority priority-${supportCard.priority}`}>
											{supportPriorityLabel(supportCard.priority)}
										</span>
										<time>{formatCellTime(supportCard.task.updatedAt)}</time>
									</div>
									<h4>{supportCard.task.title}</h4>
									<p>{supportCard.details}</p>
									<div class="support-linked-meta">
										{supportCard.linkedTaskIds.length} linked task{supportCard.linkedTaskIds
											.length === 1
											? ''
											: 's'}
									</div>
									<div class="support-card-footer">
										<div class="support-avatar-stack" aria-label="Concerned members">
											{#if supportCard.concernedIds.length === 0}
												<span class="support-avatar support-avatar-empty">--</span>
											{:else}
												{#each supportCard.concernedIds.slice(0, 4) as concernedMemberId, memberIndex (`${concernedMemberId}-${memberIndex}`)}
													{@const concernedMemberName = concernedMemberLabel(concernedMemberId)}
													<span
														class="support-avatar"
														style={`--owner-hue:${hueFromSeed(concernedMemberName)}; --avatar-index:${memberIndex};`}
														title={concernedMemberName}
													>
														{initials(concernedMemberName)}
													</span>
												{/each}
												{#if supportCard.concernedIds.length > 4}
													<span
														class="support-avatar support-avatar-more"
														style="--avatar-index:4;"
													>
														+{supportCard.concernedIds.length - 4}
													</span>
												{/if}
											{/if}
										</div>
										<span class="support-sprint-chip"
											>{formatSprintDisplayName(supportCard.task.sprintName)}</span
										>
									</div>
								</article>
							{/each}
						</div>
					{/if}
				</section>
			</div>
		{:else if boardView === 'kanban'}
			<div class="kanban-board" role="list" aria-label="Kanban task board">
				{#each kanbanColumns as column (column.key)}
					<section class="kanban-column" role="listitem" aria-label={`${column.label} lane`}>
						<header class="kanban-column-head">
							<div class="kanban-column-title-wrap">
								<h3>{column.label}</h3>
								<span>{column.tasks.length}</span>
							</div>
						</header>
						<div class="kanban-column-body" role="list" aria-label={`${column.label} tasks`}>
							{#if column.tasks.length === 0}
								<div class="kanban-empty">No {taskPluralLabel}</div>
							{:else}
								{#each column.tasks as task (task.id)}
									<article class="kanban-card" role="listitem">
										<div class="kanban-card-top">
											<button
												type="button"
												class="kanban-card-title"
												on:click|stopPropagation={() => openTaskDetails(task)}
												on:dblclick|stopPropagation={() => openTaskDetails(task)}
											>
												{task.title}
											</button>
											<button
												type="button"
												class="kanban-mini-btn"
												on:click|stopPropagation={() => queueFollowUp(task)}
												disabled={!canEdit || (contextAware && !canCreateSprintTask)}
												title="Follow-up"
											>
												+
											</button>
										</div>
										<div class="task-relation-badges kanban-relation-badges">
											{#if task.blockedBy.length > 0}
												<span class="task-badge task-badge-blocked">Blocked</span>
											{/if}
											{#if taskSubtaskSummary(task)}
												<span class="task-badge task-badge-subtasks">
													{taskSubtaskPercentLabel(task)}
												</span>
											{/if}
										</div>

										<div class="kanban-status-row">
											<div class="status-wrap">
												<button
													type="button"
													class={`status-pill status-${resolveColumn(task.status)}`}
													on:click|stopPropagation={(event) => toggleStatusMenu(event, task)}
													disabled={!canEditTaskStatus(task) || isSavingStatus(task.id)}
												>
													{statusLabel(resolveColumn(task.status))}
												</button>
												{#if statusMenuTaskId === task.id}
													<div class="status-menu">
														{#each STATUS_OPTIONS as option}
															<button
																type="button"
																class={`status-option status-${option.value}`}
																class:is-active={resolveColumn(task.status) === option.value}
																on:click|stopPropagation={() =>
																	void applyStatus(task, option.value)}
															>
																{option.label}
															</button>
														{/each}
													</div>
												{/if}
											</div>
										</div>

										<div class="kanban-meta-grid">
											<button
												type="button"
												class="kanban-meta-btn kanban-owner-btn"
												on:click|stopPropagation={() => void startEditing(task, 'assigneeId')}
												on:dblclick|stopPropagation={() => void startEditing(task, 'assigneeId')}
											>
												<span class="meta-label">Assignee</span>
												<span class="owner-chip">
													<span class="owner-avatar" style={`--owner-hue:${ownerHue(task)};`}>
														{initials(ownerLabel(task))}
													</span>
													<span class="owner-name">{ownerLabel(task)}</span>
												</span>
											</button>

											<button
												type="button"
												class="kanban-meta-btn"
												on:click|stopPropagation={() => void startEditing(task, 'budget')}
												on:dblclick|stopPropagation={() => void startEditing(task, 'budget')}
											>
												<span class="meta-label">Budget</span>
												<span class="meta-value budget-val">{formatBudgetCell(task.budget)}</span>
											</button>

											<button
												type="button"
												class="kanban-meta-btn"
												on:click|stopPropagation={() => void startEditing(task, 'spent')}
												on:dblclick|stopPropagation={() => void startEditing(task, 'spent')}
											>
												<span class="meta-label">Cost</span>
												<span class="meta-value budget-val"
													>{formatSpentCell(task.spent, task.budget)}</span
												>
											</button>
											<button
												type="button"
												class="kanban-meta-btn"
												on:click|stopPropagation={() => void startEditing(task, 'startDate')}
												on:dblclick|stopPropagation={() => void startEditing(task, 'startDate')}
											>
												<span class="meta-label">Start</span>
												<span class="meta-value date-val">{formatDateCell(task.startDate)}</span>
											</button>
											<button
												type="button"
												class="kanban-meta-btn"
												on:click|stopPropagation={() => void startEditing(task, 'dueDate')}
												on:dblclick|stopPropagation={() => void startEditing(task, 'dueDate')}
											>
												<span class="meta-label">Due</span>
												<span class="meta-value date-val">{formatDateCell(task.dueDate)}</span>
											</button>
										</div>

										{#if task.roles?.length}
											<div class="kanban-roles">
												{#each task.roles as r}
													<span class="kanban-role-chip">{r.role}</span>
												{/each}
											</div>
										{/if}

										<footer class="kanban-card-footer">
											<span>{formatSprintDisplayName(task.sprintName)}</span>
											<time datetime={new Date(task.updatedAt).toISOString()}>
												{formatCellTime(task.updatedAt)}
											</time>
										</footer>
									</article>
								{/each}
							{/if}
						</div>
					</section>
				{/each}
			</div>
		{:else}
			<div class="sprint-groups" role="list" aria-label={`${groupTermPlural} ${taskTermPlural}`}>
				{#each sprintTaskGroups as sprintGroup (sprintGroup.key)}
					{@const selCount = sprintSelectedCount(sprintGroup)}
					{@const isEditMode = sprintEditKeys.has(sprintGroup.key)}
					<section
						class="sprint-group"
						role="listitem"
						aria-label={`${formatSprintDisplayName(sprintGroup.name)} ${taskLabel} grid`}
					>
						<header class="sprint-group-header">
							<div class="sgh-left">
								<h3>{formatSprintDisplayName(sprintGroup.name)}</h3>
								<p>
									{sprintGroup.tasks.length} {sprintGroup.tasks.length === 1 ? taskLabel : taskPluralLabel} · {formatCellTime(sprintGroup.lastUpdatedAt)}
								</p>
							</div>
							<div class="sgh-actions">
								{#if isEditMode}
									<button
										type="button"
										class="sgh-btn sgh-delete"
										on:click={() => void deleteSelectedInSprint(sprintGroup)}
										disabled={!canEdit || deletingTaskIds.length > 0}
									>
										{isAdmin ? 'Delete' : 'Request Delete'}{selCount > 0 ? ` (${selCount})` : ''}
									</button>
									{#if canCreateSprintTask}
										<button
											type="button"
											class="sgh-btn sgh-add"
											on:click={() => void toggleSprintAddComposer(sprintGroup.key)}
										>
											+ Add row
										</button>
									{/if}
									<button
										type="button"
										class="sgh-btn sgh-done"
										on:click={() => {
											const nextSprintEditKeys = new Set(sprintEditKeys);
											nextSprintEditKeys.delete(sprintGroup.key);
											sprintEditKeys = nextSprintEditKeys;
											sprintAddKey = '';
											sprintAddContent = '';
											const nextSelectedTaskIds = new Set(selectedTaskIds);
											sprintGroup.tasks.forEach((t) => nextSelectedTaskIds.delete(t.id));
											selectedTaskIds = Array.from(nextSelectedTaskIds);
										}}
									>
										Done
									</button>
									<span class="sgh-hint">Select rows to delete.</span>
								{:else if canEdit}
									<button
										type="button"
										class="sgh-btn sgh-edit"
										on:click={() => {
											const nextSprintEditKeys = new Set(sprintEditKeys);
											nextSprintEditKeys.add(sprintGroup.key);
											sprintEditKeys = nextSprintEditKeys;
										}}
									>
										Edit
									</button>
								{/if}
							</div>
						</header>

						<div class="grid-shell">
							<table class="task-grid" role="grid" aria-label={`${formatSprintDisplayName(sprintGroup.name)} ${taskPluralLabel}`}>
								<thead>
									<tr>
										{#if isEditMode}
											<th scope="col" class="th-check">
												<input
													type="checkbox"
													checked={isSprintAllSelected(sprintGroup)}
													indeterminate={selCount > 0 && !isSprintAllSelected(sprintGroup)}
													on:change={() => toggleSprintSelection(sprintGroup)}
													aria-label={`Select all in ${groupLabel}`}
												/>
											</th>
										{/if}
										<th scope="col" class="th-sort th-task">
											<button
												type="button"
												class="th-sort-btn"
												class:is-active={sprintSortDirection(sprintGroup.key, 'title') !== ''}
												on:click={() => toggleSprintSort(sprintGroup.key, 'title')}
												aria-label={`Sort ${formatSprintDisplayName(sprintGroup.name)} by ${taskLabel} name`}
											>
												<span>{taskTerm}</span>
												<span class="th-sort-icon" aria-hidden="true"
													>{sprintSortIcon(sprintGroup.key, 'title')}</span
												>
											</button>
										</th>
										<th scope="col" class="th-sort th-status">
											<button
												type="button"
												class="th-sort-btn"
												class:is-active={sprintSortDirection(sprintGroup.key, 'status') !== ''}
												on:click={() => toggleSprintSort(sprintGroup.key, 'status')}
												aria-label={`Sort ${formatSprintDisplayName(sprintGroup.name)} by status`}
											>
												<span>Status</span>
												<span class="th-sort-icon" aria-hidden="true"
													>{sprintSortIcon(sprintGroup.key, 'status')}</span
												>
											</button>
										</th>
										<th scope="col" class="th-sort th-owner">
											<button
												type="button"
												class="th-sort-btn"
												class:is-active={sprintSortDirection(sprintGroup.key, 'owner') !== ''}
												on:click={() => toggleSprintSort(sprintGroup.key, 'owner')}
												aria-label={`Sort ${formatSprintDisplayName(sprintGroup.name)} by assignee`}
											>
												<span>Assignee</span>
												<span class="th-sort-icon" aria-hidden="true"
													>{sprintSortIcon(sprintGroup.key, 'owner')}</span
												>
											</button>
										</th>
										<th scope="col" class="th-sort th-budget">
											<button
												type="button"
												class="th-sort-btn"
												class:is-active={sprintSortDirection(sprintGroup.key, 'budget') !== ''}
												on:click={() => toggleSprintSort(sprintGroup.key, 'budget')}
												aria-label={`Sort ${formatSprintDisplayName(sprintGroup.name)} by budget`}
											>
												<span>Budget</span>
												<span class="th-sort-icon" aria-hidden="true"
													>{sprintSortIcon(sprintGroup.key, 'budget')}</span
												>
											</button>
										</th>
										<th scope="col" class="th-sort th-spent">
											<button
												type="button"
												class="th-sort-btn"
												class:is-active={sprintSortDirection(sprintGroup.key, 'spent') !== ''}
												on:click={() => toggleSprintSort(sprintGroup.key, 'spent')}
												aria-label={`Sort ${formatSprintDisplayName(sprintGroup.name)} by spent`}
											>
												<span>Cost</span>
												<span class="th-sort-icon" aria-hidden="true"
													>{sprintSortIcon(sprintGroup.key, 'spent')}</span
												>
											</button>
										</th>
										<th scope="col" class="th-date th-start-date">Start</th>
										<th scope="col" class="th-date th-due-date">Due</th>
										<th scope="col" class="th-sort th-updated">
											<button
												type="button"
												class="th-sort-btn"
												class:is-active={sprintSortDirection(sprintGroup.key, 'updated') !== ''}
												on:click={() => toggleSprintSort(sprintGroup.key, 'updated')}
												aria-label={`Sort ${formatSprintDisplayName(sprintGroup.name)} by updated time`}
											>
												<span>Updated</span>
												<span class="th-sort-icon" aria-hidden="true"
													>{sprintSortIcon(sprintGroup.key, 'updated')}</span
												>
											</button>
										</th>
									</tr>
								</thead>
								<tbody>
									{#if sprintGroup.tasks.length === 0}
										<tr>
											<td class="cell empty-sprint-row" colspan={isEditMode ? 10 : 9}>
												No {taskPluralLabel} in this {groupLabel} yet. Use + Add row or + Add {groupTerm}.
											</td>
										</tr>
									{:else}
										{#each sprintGroup.tasks as task (task.id)}
											<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-noninteractive-element-interactions -->
											<tr
												class:row-todo={resolveColumn(task.status) === 'todo'}
												class:row-progress={resolveColumn(task.status) === 'in_progress'}
												class:row-done={resolveColumn(task.status) === 'done'}
												class:row-status-open={statusMenuTaskId === task.id}
												class:row-selected={isEditMode && isTaskSelected(task.id)}
												class:row-modal-open={taskEditModal?.id === task.id}
												on:mouseenter={() => {
													hoveredTaskId = task.id;
												}}
												on:mouseleave={() => {
													hoveredTaskId = '';
												}}
												on:click={() => handleTaskRowClick(task, isEditMode)}
											>
												<!-- Checkbox -->
												{#if isEditMode}
													<td class="cell check-cell" on:click|stopPropagation>
														<input
															type="checkbox"
															checked={isTaskSelected(task.id)}
															on:change={() => toggleTaskSelection(task.id)}
															disabled={isTaskDeleting(task.id)}
															aria-label={`Select ${task.title}`}
														/>
													</td>
												{/if}

												<!-- Task name -->
												<td class="cell task-cell">
													<button
														type="button"
														class="row-check"
														class:is-visible={hoveredTaskId === task.id ||
															resolveColumn(task.status) === 'done'}
														on:click|stopPropagation={() => void toggleDone(task)}
														disabled={!canEdit}
														aria-label={resolveColumn(task.status) === 'done'
															? 'Mark as to do'
															: 'Mark as done'}
													>
														<svg viewBox="0 0 24 24" aria-hidden="true">
															<path d="m6 12 4 4 8-8"></path>
														</svg>
													</button>
													<div class="task-content">
														<span class="cell-trigger task-title-trigger">{task.title}</span>
														<div class="task-relation-badges">
															{#if task.blockedBy.length > 0}
																<span class="task-badge task-badge-blocked">
																	Blocked by {task.blockedBy.length}
																</span>
															{/if}
															{#if taskSubtaskSummary(task)}
																<span class="task-badge task-badge-subtasks">
																	{taskSubtaskSummaryLabel(task)}
																</span>
															{/if}
														</div>
														<div class="task-actions">
															<button
																type="button"
																on:click|stopPropagation={() => queueFollowUp(task)}
																disabled={!canEdit || (contextAware && !canCreateSprintTask)}
																title="Follow-up">+</button
															>
														</div>
													</div>
												</td>

												<!-- Status -->
												<td
													class="cell status-cell"
													class:menu-open={statusMenuTaskId === task.id}
													on:click|stopPropagation
												>
													<div class="status-wrap">
														<button
															type="button"
															class={`status-pill status-${resolveColumn(task.status)}`}
															on:click|stopPropagation={(e) => toggleStatusMenu(e, task)}
															disabled={!canEditTaskStatus(task) || isSavingStatus(task.id)}
														>
															{statusLabel(resolveColumn(task.status))}
														</button>
														{#if statusMenuTaskId === task.id}
															<div class="status-menu">
																{#each STATUS_OPTIONS as option}
																	<button
																		type="button"
																		class={`status-option status-${option.value}`}
																		class:is-active={resolveColumn(task.status) === option.value}
																		on:click|stopPropagation={() =>
																			void applyStatus(task, option.value)}>{option.label}</button
																	>
																{/each}
															</div>
														{/if}
													</div>
												</td>

												<!-- Assignee -->
												<td class="cell owner-cell">
													<div class="owner-chip">
														<span class="owner-avatar" style={`--owner-hue:${ownerHue(task)};`}
															>{initials(ownerLabel(task))}</span
														>
														<span class="owner-name">{ownerLabel(task)}</span>
													</div>
												</td>

												<!-- Budget -->
												<td class="cell budget-cell">
													<span class="budget-val">{formatBudgetCell(task.budget)}</span>
												</td>

												<!-- Cost -->
												<td class="cell spent-cell">
													<span class="budget-val">{formatSpentCell(task.spent, task.budget)}</span>
												</td>

												<!-- Start Date -->
												<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-noninteractive-element-interactions -->
												<td
													class="cell date-cell"
													class:is-editing={isEditing(task.id, 'startDate')}
													on:click|stopPropagation={() => void startEditing(task, 'startDate')}
												>
													<span class="date-val">{formatDateCell(task.startDate)}</span>
												</td>

												<!-- Due Date -->
												<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-noninteractive-element-interactions -->
												<td
													class="cell date-cell"
													class:is-editing={isEditing(task.id, 'dueDate')}
													on:click|stopPropagation={() => void startEditing(task, 'dueDate')}
												>
													<span class="date-val">{formatDateCell(task.dueDate)}</span>
												</td>

												<td class="cell updated-cell">{formatCellTime(task.updatedAt)}</td>
											</tr>
											{#if taskEditModal?.id === task.id}
												<tr class="edit-expand-row">
													<td class="edit-expand-cell" colspan={isEditMode ? 10 : 9}>
														<div class="edit-expand-inner">
															<div class="task-detail-section">
																<div class="task-detail-copy">
																	<div class="eef eef-full">
																		<label class="eef-label" for="eef-description-{task.id}"
																			>Description</label
																		>
																		<textarea
																			id="eef-description-{task.id}"
																			class="eef-input eef-textarea"
																			rows="4"
																			bind:value={taskEditVals.description}
																			placeholder="Add a short description so someone opening this task knows the goal."
																			disabled={!canEdit}
																		></textarea>
																	</div>
																	<div class="eef eef-full">
																		<label class="eef-label" for="eef-notes-{task.id}">Notes</label>
																		<textarea
																			id="eef-notes-{task.id}"
																			class="eef-input eef-textarea"
																			rows="3"
																			bind:value={taskEditVals.notes}
																			placeholder="Shared notes, links, or context for the team."
																			disabled={!canEdit}
																		></textarea>
																	</div>
																	<div class="task-detail-actions">
																		<button
																			type="button"
																			class="eef-save"
																			on:click={() => void saveTaskDetails()}
																			disabled={!canEdit || taskDetailSaving || taskDetailGenerating}
																		>
																			{taskDetailSaving ? 'Saving details…' : 'Save details'}
																		</button>
																		<button
																			type="button"
																			class="eef-secondary"
																			on:click={() => void generateTaskDetails()}
																			disabled={task.source !== 'room' || !canEdit || taskDetailGenerating}
																		>
																			{taskDetailGenerating
																				? 'Generating steps…'
																				: hasGeneratedTaskDetails(task)
																					? 'Regenerate detailed steps'
																					: 'Generate detailed steps'}
																		</button>
																	</div>
																	{#if taskDetailNotice}
																		<p class="task-detail-notice">{taskDetailNotice}</p>
																	{/if}
																	{#if taskDetailError}
																		<p class="task-detail-error">{taskDetailError}</p>
																	{/if}
																</div>
																<div class="task-detail-guide">
																	<div class="task-detail-guide-head">
																		<h4>Detailed guide</h4>
																		{#if taskDetailGeneratedAt}
																			<span
																				>Updated {formatTaskDetailGeneratedAt(
																					taskDetailGeneratedAt
																				)}</span
																			>
																		{/if}
																	</div>
																	{#if taskDetailSummary}
																		<p class="task-detail-summary">{taskDetailSummary}</p>
																	{:else}
																		<p class="task-detail-empty">
																			Keep the description short here, then generate a fuller guide only when someone needs it.
																		</p>
																	{/if}
																	{#if taskDetailSteps.length > 0}
																		<ol class="task-detail-steps">
																			{#each taskDetailSteps as step, stepIndex (`${task.id}-step-${stepIndex}`)}
																				<li>{step}</li>
																			{/each}
																		</ol>
																	{/if}
																</div>
															</div>
															<div class="edit-expand-fields">
																<div class="eef eef-wide">
																	<label class="eef-label" for="eef-title-{task.id}">Title</label>
																	<input
																		id="eef-title-{task.id}"
																		class="eef-input"
																		type="text"
																		bind:value={taskEditVals.title}
																		on:keydown={(e) => {
																			if (e.key === 'Escape') closeTaskModal();
																			if (e.key === 'Enter' && (e.ctrlKey || e.metaKey))
																				void saveTaskModal();
																		}}
																	/>
																</div>
																<div class="eef">
																	<label class="eef-label" for="eef-status-{task.id}">Status</label>
																	<select
																		id="eef-status-{task.id}"
																		class="eef-select"
																		bind:value={taskEditVals.status}
																	>
																		{#each STATUS_OPTIONS as opt (opt.value)}
																			<option value={opt.value}>{opt.label}</option>
																		{/each}
																	</select>
																</div>
																<div class="eef">
																	<label class="eef-label" for="eef-assignee-{task.id}"
																		>Assignee</label
																	>
																	<select
																		id="eef-assignee-{task.id}"
																		class="eef-select"
																		bind:value={taskEditVals.assigneeId}
																	>
																		<option value="">— unassigned —</option>
																		{#each ownerOptions as opt (opt.id)}
																			<option value={opt.id}
																				>{opt.label}{opt.isOnline ? '' : ' (offline)'}</option
																			>
																		{/each}
																	</select>
																</div>
																<div class="eef">
																	<label class="eef-label" for="eef-budget-{task.id}"
																		>Budget ($)</label
																	>
																	<input
																		id="eef-budget-{task.id}"
																		class="eef-input eef-num"
																		type="number"
																		min="0"
																		step="0.01"
																		bind:value={taskEditVals.budget}
																	/>
																</div>
																<div class="eef">
																	<label class="eef-label" for="eef-spent-{task.id}">Cost ($)</label
																	>
																	<input
																		id="eef-spent-{task.id}"
																		class="eef-input eef-num"
																		type="number"
																		min="0"
																		step="0.01"
																		bind:value={taskEditVals.spent}
																	/>
																</div>
																<div class="eef">
																	<label class="eef-label" for="eef-startdate-{task.id}"
																		>Start Date</label
																	>
																	<input
																		id="eef-startdate-{task.id}"
																		class="eef-input"
																		type="date"
																		bind:value={taskEditVals.startDate}
																	/>
																</div>
																<div class="eef">
																	<label class="eef-label" for="eef-duedate-{task.id}"
																		>Due Date</label
																	>
																	<input
																		id="eef-duedate-{task.id}"
																		class="eef-input"
																		type="date"
																		bind:value={taskEditVals.dueDate}
																	/>
																</div>
															</div>
															<div class="edit-expand-actions">
																<button
																	type="button"
																	class="eef-save"
																	on:click={() => void saveTaskModal()}
																	disabled={taskModalSaving}
																>
																	{taskModalSaving ? 'Submitting…' : isAdmin ? 'Save' : 'Request Save'}
																</button>
																<button type="button" class="eef-cancel" on:click={closeTaskModal}
																	>Cancel</button
																>
															</div>
														</div>
													</td>
												</tr>
											{/if}
										{/each}
									{/if}
								</tbody>
							</table>
						</div>

						<!-- Per-sprint add task form -->
						{#if sprintAddKey === sprintGroup.key}
							<form
								class="sprint-add-form"
								use:registerSprintAddForm={sprintGroup.key}
								on:submit|preventDefault={() =>
									void handleCreateRoomTaskInSprint(sprintAddContent, sprintGroup.name)}
							>
								<!-- svelte-ignore a11y-autofocus -->
								<input
									type="text"
									bind:value={sprintAddContent}
									placeholder={`${taskTerm} name…`}
									autocomplete="off"
									disabled={sprintAddCreating}
									autofocus
								/>
								<button type="submit" disabled={sprintAddCreating || !sprintAddContent.trim()}>
									{sprintAddCreating ? 'Submitting…' : isAdmin ? 'Add' : 'Request Add'}
								</button>
								<button
									type="button"
									on:click={() => {
										sprintAddKey = '';
										sprintAddContent = '';
									}}>Cancel</button
								>
							</form>
						{/if}
					</section>
				{/each}
			</div>
		{/if}
	</div>
</section>

<style>
	:global(:root) {
		--workspace-taskboard-bg:
			radial-gradient(circle at 0% 0%, rgba(89, 105, 255, 0.2), transparent 28%),
			radial-gradient(circle at 100% 8%, rgba(16, 185, 129, 0.16), transparent 24%),
			radial-gradient(circle at 50% 100%, rgba(236, 72, 153, 0.16), transparent 28%),
			linear-gradient(180deg, #eef4ff 0%, #edf1fb 100%);
		--workspace-taskboard-column-border: #d4d5da;
		--workspace-taskboard-item-bg: rgba(255, 255, 255, 0.9);
		--workspace-taskboard-item-border: rgba(43, 46, 56, 0.14);
		--workspace-taskboard-item-text: #17181c;
		--workspace-taskboard-meta: #56617f;

		--tb-panel-bg: rgba(255, 255, 255, 0.78);
		--tb-panel-border: rgba(96, 118, 201, 0.24);
		--tb-form-bg: rgba(255, 255, 255, 0.82);
		--tb-form-border: rgba(96, 118, 201, 0.22);
		--tb-input-bg: rgba(252, 254, 255, 0.94);
		--tb-input-border: #cad3f2;
		--tb-input-text: #1c1e23;
		--tb-input-placeholder: #6d7694;
		--tb-btn-bg: #eaf0ff;
		--tb-btn-border: #c5d0f3;
		--tb-btn-text: #30406f;
		--tb-state-bg: rgba(255, 255, 255, 0.82);
		--tb-state-border: rgba(96, 118, 201, 0.18);
		--tb-state-text: #4f5a79;
		--tb-error-text: #2d3036;

		--tb-grid-bg: rgba(255, 255, 255, 0.88);
		--tb-grid-border: rgba(96, 118, 201, 0.24);
		--tb-grid-head-bg: #243b8f;
		--tb-grid-head-text: #f6f8ff;
		--tb-grid-head-muted: #d9e2ff;
		--tb-grid-row-border: rgba(43, 46, 56, 0.16);
		--tb-grid-col-border: rgba(43, 46, 56, 0.12);
		--tb-grid-row-hover: rgba(68, 90, 180, 0.1);
		--tb-grid-row-done: rgba(20, 184, 166, 0.08);
		--tb-cell-text: #1f2127;
		--tb-cell-muted: #64708d;
		--tb-editor-bg: #fefefe;
		--tb-editor-border: #8e9fd2;
		--tb-editor-ring: rgba(78, 96, 191, 0.22);
		--tb-avatar-text: #ffffff;
		--tb-icon-bg: rgba(68, 90, 180, 0.12);
		--tb-icon-bg-hover: rgba(68, 90, 180, 0.22);
		--tb-icon-text: #354677;

		--tb-accent: #516dff;
		--tb-accent-strong: #2948db;
		--tb-accent-soft: rgba(81, 109, 255, 0.16);
		--tb-status-todo: #6f80ff;
		--tb-status-todo-text: #f7f8ff;
		--tb-status-progress: #ffbf47;
		--tb-status-progress-text: #4d2b00;
		--tb-status-done: #1cc7a1;
		--tb-status-done-text: #042d23;
		--tb-panel-shadow: 0 18px 36px rgba(34, 59, 152, 0.14);
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--workspace-taskboard-bg:
			radial-gradient(circle at 0% 0%, rgba(87, 103, 255, 0.22), transparent 24%),
			radial-gradient(circle at 100% 6%, rgba(20, 184, 166, 0.18), transparent 22%),
			radial-gradient(circle at 50% 100%, rgba(236, 72, 153, 0.16), transparent 24%),
			linear-gradient(180deg, #0e1120 0%, #111523 100%);
		--workspace-taskboard-column-border: rgba(231, 233, 239, 0.2);
		--workspace-taskboard-item-bg: rgba(16, 21, 35, 0.92);
		--workspace-taskboard-item-border: rgba(231, 233, 239, 0.14);
		--workspace-taskboard-item-text: #eef0f5;
		--workspace-taskboard-meta: #aeb9da;

		--tb-panel-bg: rgba(16, 22, 36, 0.86);
		--tb-panel-border: rgba(135, 156, 255, 0.18);
		--tb-form-bg: rgba(12, 18, 31, 0.88);
		--tb-form-border: rgba(135, 156, 255, 0.18);
		--tb-input-bg: rgba(9, 13, 24, 0.92);
		--tb-input-border: rgba(154, 171, 255, 0.24);
		--tb-input-text: #f1f3f8;
		--tb-input-placeholder: #8f9cc0;
		--tb-btn-bg: rgba(43, 57, 112, 0.72);
		--tb-btn-border: rgba(154, 171, 255, 0.22);
		--tb-btn-text: #eceef4;
		--tb-state-bg: rgba(13, 19, 32, 0.9);
		--tb-state-border: rgba(154, 171, 255, 0.16);
		--tb-state-text: #c4cdee;
		--tb-error-text: #d0d3da;

		--tb-grid-bg: rgba(12, 18, 31, 0.92);
		--tb-grid-border: rgba(154, 171, 255, 0.18);
		--tb-grid-head-bg: #111c46;
		--tb-grid-head-text: #f5f7ff;
		--tb-grid-head-muted: #c7d3ff;
		--tb-grid-row-border: rgba(231, 233, 239, 0.14);
		--tb-grid-col-border: rgba(231, 233, 239, 0.12);
		--tb-grid-row-hover: rgba(109, 129, 255, 0.12);
		--tb-grid-row-done: rgba(28, 199, 161, 0.1);
		--tb-cell-text: #eef0f5;
		--tb-cell-muted: #aab6da;
		--tb-editor-bg: rgba(8, 12, 22, 0.96);
		--tb-editor-border: rgba(154, 171, 255, 0.34);
		--tb-editor-ring: rgba(154, 171, 255, 0.22);
		--tb-avatar-text: #ffffff;
		--tb-icon-bg: rgba(123, 147, 255, 0.18);
		--tb-icon-bg-hover: rgba(123, 147, 255, 0.28);
		--tb-icon-text: #eef1ff;

		--tb-accent: #8b9bff;
		--tb-accent-strong: #dfe5ff;
		--tb-accent-soft: rgba(139, 155, 255, 0.18);
		--tb-status-todo: #8794ff;
		--tb-status-todo-text: #f5f7ff;
		--tb-status-progress: #ffc857;
		--tb-status-progress-text: #442700;
		--tb-status-done: #34d399;
		--tb-status-done-text: #04291d;
		--tb-panel-shadow: 0 22px 38px rgba(0, 0, 0, 0.34);
	}

	.task-board {
		height: 100%;
		min-height: 0;
		width: 100%;
		padding: 1rem;
		display: grid;
		grid-template-rows: auto auto minmax(0, 1fr);
		gap: 0.8rem;
		background: var(--workspace-taskboard-bg);
		font-family: 'Manrope', 'Avenir Next', 'Segoe UI', sans-serif;
	}

	.board-toast {
		padding: 0.65rem 0.8rem;
		border-radius: 12px;
		border: 1px solid color-mix(in srgb, var(--tb-error-text) 45%, transparent);
		background: color-mix(in srgb, var(--tb-error-text) 16%, transparent);
		color: var(--tb-error-text);
		font-size: 0.84rem;
		font-weight: 600;
	}

	/* ── Unified Board Toolbar ─────────────────────────── */
	.board-toolbar {
		position: relative;
		isolation: isolate;
		display: flex;
		align-items: center;
		gap: 0.6rem;
		padding: 0.75rem 0.9rem;
		border-radius: 16px;
		background: linear-gradient(
			135deg,
			color-mix(in srgb, var(--tb-accent-soft) 82%, var(--tb-panel-bg) 18%),
			var(--tb-panel-bg)
		);
		border: 1px solid color-mix(in srgb, var(--tb-accent) 20%, var(--tb-panel-border));
		backdrop-filter: blur(14px);
		box-shadow: var(--tb-panel-shadow);
		overflow: hidden;
	}

	.board-toolbar::before {
		content: '';
		position: absolute;
		inset: -40% auto auto 58%;
		width: 220px;
		height: 220px;
		border-radius: 999px;
		background: color-mix(in srgb, var(--tb-accent-soft) 72%, transparent);
		filter: blur(32px);
		pointer-events: none;
	}

	/* Title group */
	.btb-title {
		position: relative;
		z-index: 1;
		display: flex;
		flex-direction: column;
		gap: 0.1rem;
		min-width: 0;
		flex: 1;
	}

	.btb-title h2 {
		margin: 0;
		font-size: 1rem;
		line-height: 1.2;
		font-weight: 700;
		color: var(--tb-accent-strong);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.btb-meta {
		display: flex;
		align-items: center;
		gap: 0.35rem;
	}

	.btb-count {
		font-size: 0.74rem;
		font-weight: 700;
		padding: 0.18rem 0.5rem;
		border-radius: 999px;
		color: var(--tb-accent-strong);
		border: 1px solid color-mix(in srgb, var(--tb-accent) 20%, var(--tb-panel-border));
		background: color-mix(in srgb, var(--tb-accent-soft) 55%, var(--tb-panel-bg) 45%);
		white-space: nowrap;
	}

	.btb-sep {
		font-size: 0.7rem;
		color: var(--workspace-taskboard-meta);
	}

	.btb-updated {
		font-size: 0.7rem;
		color: var(--workspace-taskboard-meta);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		max-width: 180px;
	}

	/* Action buttons */
	.btb-actions {
		position: relative;
		z-index: 1;
		display: flex;
		align-items: center;
		gap: 0.35rem;
		flex-shrink: 0;
	}

	.btb-icon-btn {
		width: 2rem;
		height: 2rem;
		border-radius: 9px;
		border: 1px solid color-mix(in srgb, var(--tb-accent) 20%, var(--tb-btn-border));
		background: color-mix(in srgb, var(--tb-accent-soft) 35%, var(--tb-panel-bg) 65%);
		color: var(--tb-accent-strong);
		display: flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		transition: background 0.15s, border-color 0.15s;
	}

	.btb-icon-btn svg {
		width: 15px;
		height: 15px;
		stroke: currentColor;
		fill: none;
		stroke-width: 2;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.btb-icon-btn:hover {
		background: color-mix(in srgb, var(--tb-accent-soft) 55%, var(--tb-panel-bg) 45%);
		border-color: color-mix(in srgb, var(--tb-accent) 40%, var(--tb-btn-border));
	}

	.btb-icon-btn.is-active {
		background: color-mix(in srgb, var(--tb-accent) 20%, var(--tb-panel-bg) 80%);
		border-color: color-mix(in srgb, var(--tb-accent) 50%, var(--tb-btn-border));
	}

	.btb-add-sprint {
		display: inline-flex;
		align-items: center;
		gap: 0.32rem;
		height: 2rem;
		padding: 0 0.7rem;
		border-radius: 9px;
		border: 1px solid color-mix(in srgb, var(--tb-accent) 35%, var(--tb-btn-border));
		background: color-mix(in srgb, var(--tb-accent-soft) 50%, var(--tb-panel-bg) 50%);
		color: var(--tb-accent-strong);
		font-size: 0.76rem;
		font-weight: 700;
		cursor: pointer;
		transition: background 0.15s, border-color 0.15s;
	}

	.btb-add-sprint svg {
		width: 13px;
		height: 13px;
		stroke: currentColor;
		fill: none;
		stroke-width: 2.5;
		stroke-linecap: round;
	}

	.btb-add-sprint:hover:not(:disabled) {
		background: color-mix(in srgb, var(--tb-accent-soft) 70%, var(--tb-panel-bg) 30%);
		border-color: color-mix(in srgb, var(--tb-accent) 55%, var(--tb-btn-border));
	}

	.btb-add-sprint:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.btb-request-sprint {
		color: #d97706;
		background: color-mix(in srgb, #f59e0b 12%, var(--tb-panel-bg) 88%);
		border-color: color-mix(in srgb, #f59e0b 40%, var(--tb-btn-border));
	}
	.btb-request-sprint:hover {
		background: color-mix(in srgb, #f59e0b 22%, var(--tb-panel-bg) 78%);
	}

	/* Expanded search bar */
	.btb-search-bar {
		position: relative;
		z-index: 1;
		display: flex;
		align-items: center;
		gap: 0.4rem;
		width: 100%;
		animation: btb-expand 0.18s ease;
	}

	@keyframes btb-expand {
		from { opacity: 0; transform: scaleX(0.92); }
		to   { opacity: 1; transform: scaleX(1); }
	}

	.btb-search-icon {
		width: 15px;
		height: 15px;
		flex-shrink: 0;
		stroke: var(--tb-accent-strong);
		fill: none;
		stroke-width: 2;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.btb-search-input {
		flex: 1;
		min-width: 0;
		height: 1.9rem;
		border: 1px solid var(--tb-input-border);
		border-radius: 9px;
		background: var(--tb-input-bg);
		color: var(--tb-input-text);
		font-size: 0.82rem;
		padding: 0 0.65rem;
		outline: none;
		transition: border-color 0.15s;
	}

	.btb-search-input:focus {
		border-color: color-mix(in srgb, var(--tb-accent) 55%, var(--tb-input-border));
	}

	.btb-search-input::placeholder {
		color: var(--tb-input-placeholder);
	}

	.btb-search-close {
		width: 1.9rem;
		height: 1.9rem;
		border-radius: 8px;
		border: 1px solid color-mix(in srgb, var(--tb-btn-border) 80%, transparent);
		background: transparent;
		color: var(--tb-btn-text);
		display: flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		flex-shrink: 0;
		transition: background 0.14s;
	}

	.btb-search-close svg {
		width: 13px;
		height: 13px;
		stroke: currentColor;
		fill: none;
		stroke-width: 2.2;
		stroke-linecap: round;
	}

	.btb-search-close:hover {
		background: color-mix(in srgb, var(--tb-btn-bg) 80%, #ffffff 20%);
	}

	.sprint-composer {
		display: grid;
		gap: 0.62rem;
		padding: 0.78rem;
		border-radius: 16px;
		border: 1px solid var(--tb-form-border);
		background: var(--tb-form-bg);
		backdrop-filter: blur(10px);
		box-shadow: var(--tb-panel-shadow);
	}

	.sprint-composer-head {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 0.5rem;
		flex-wrap: wrap;
	}

	.sprint-composer-head p {
		margin: 0.18rem 0 0;
		font-size: 0.76rem;
		color: var(--tb-cell-muted);
	}

	.sprint-composer-trigger {
		height: 2.1rem;
		padding: 0 0.82rem;
		border-radius: 10px;
		border: 1px solid var(--tb-btn-border);
		background: color-mix(in srgb, var(--tb-btn-bg) 66%, #ffffff 34%);
		color: var(--tb-btn-text);
		font-size: 0.8rem;
		font-weight: 700;
		cursor: pointer;
		transition: background 0.18s ease;
	}

	.sprint-composer-trigger:hover:not(:disabled) {
		background: color-mix(in srgb, var(--tb-btn-bg) 50%, #ffffff 50%);
	}

	.sprint-composer-request {
		border-color: color-mix(in srgb, #f59e0b 45%, transparent);
		background: color-mix(in srgb, #f59e0b 10%, transparent);
		color: #d97706;
	}
	.sprint-composer-request:hover {
		background: color-mix(in srgb, #f59e0b 18%, transparent);
	}

	.sprint-composer-trigger:disabled {
		opacity: 0.52;
		cursor: not-allowed;
	}

	.sprint-composer-form {
		display: grid;
		gap: 0.55rem;
		padding-top: 0.4rem;
		border-top: 1px solid var(--tb-grid-col-border);
	}

	.sprint-composer-field {
		display: grid;
		gap: 0.28rem;
	}

	.sprint-composer-field span {
		font-size: 0.69rem;
		font-weight: 700;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--tb-cell-muted);
	}

	.sprint-composer-field input {
		width: 100%;
		height: 2.25rem;
		border: 1px solid var(--tb-input-border);
		background: var(--tb-input-bg);
		color: var(--tb-input-text);
		border-radius: 9px;
		padding: 0 0.58rem;
		font-size: 0.8rem;
	}

	.sprint-composer-field input:focus-visible {
		outline: none;
		box-shadow: 0 0 0 3px var(--tb-editor-ring);
	}

	.sprint-preview-grid {
		display: grid;
		gap: 0;
		border: 1px solid var(--tb-grid-border);
		border-radius: 12px;
		overflow: auto;
		background: var(--tb-grid-bg);
	}

	.sprint-preview-head,
	.sprint-preview-row {
		min-width: 760px;
		display: grid;
		grid-template-columns: minmax(240px, 2.2fr) repeat(5, minmax(96px, 1fr));
	}

	.sprint-preview-head {
		height: 2rem;
		background: var(--tb-grid-head-bg);
		color: var(--tb-grid-head-text);
		border-bottom: 1px solid var(--tb-grid-row-border);
	}

	.sprint-preview-head span {
		display: flex;
		align-items: center;
		padding: 0 0.6rem;
		font-size: 0.67rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		white-space: nowrap;
	}

	.sprint-preview-head span:not(:last-child),
	.sprint-preview-row > *:not(:last-child) {
		border-right: 1px solid var(--tb-grid-col-border);
	}

	.sprint-preview-body {
		display: grid;
	}

	.sprint-preview-row {
		border-top: 1px solid var(--tb-grid-row-border);
	}

	.sprint-preview-row:first-child {
		border-top: none;
	}

	.sprint-preview-cell {
		min-height: 2.15rem;
		padding: 0.24rem 0.34rem;
		display: flex;
		align-items: center;
	}

	.sprint-preview-task-btn,
	.sprint-preview-meta-cell {
		width: 100%;
		height: 100%;
		border-radius: 8px;
		border: 1px solid transparent;
		background: transparent;
		color: var(--tb-cell-text);
		font-size: 0.74rem;
		text-align: left;
		cursor: pointer;
	}

	.sprint-preview-task-btn {
		padding: 0 0.5rem;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.sprint-preview-task-btn.is-empty {
		color: var(--tb-cell-muted);
		border-style: dashed;
		border-color: var(--tb-grid-col-border);
	}

	.sprint-preview-task-btn:hover:not(:disabled),
	.sprint-preview-meta-cell:hover:not(:disabled) {
		background: color-mix(in srgb, var(--tb-btn-bg) 58%, transparent);
		border-color: var(--tb-grid-col-border);
	}

	.sprint-preview-task-btn:disabled,
	.sprint-preview-meta-cell:disabled {
		opacity: 0.55;
		cursor: not-allowed;
	}

	.sprint-preview-meta-cell {
		padding: 0 0.48rem;
		font-size: 0.7rem;
		font-weight: 600;
		color: var(--tb-cell-muted);
	}

	.sprint-preview-task-input {
		width: 100%;
		height: 100%;
		min-height: 1.65rem;
		border: 1px solid var(--tb-editor-border);
		background: var(--tb-editor-bg);
		color: var(--tb-cell-text);
		border-radius: 8px;
		padding: 0 0.5rem;
		font-size: 0.76rem;
	}

	.sprint-preview-task-input:focus-visible {
		outline: none;
		box-shadow: 0 0 0 3px var(--tb-editor-ring);
	}

	.spc-select,
	.spc-num-input {
		width: 100%;
		height: 1.75rem;
		border: 1px solid var(--tb-input-border);
		border-radius: 7px;
		background: var(--tb-input-bg);
		color: var(--tb-cell-text);
		font-size: 0.73rem;
		padding: 0 0.45rem;
		transition: border-color 0.15s ease, box-shadow 0.15s ease;
		-webkit-appearance: none;
		appearance: none;
	}
	.spc-select {
		background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' stroke='%23888' stroke-width='2' stroke-linecap='round'%3E%3Cpath d='m6 9 6 6 6-6'/%3E%3C/svg%3E");
		background-repeat: no-repeat;
		background-position: right 0.35rem center;
		background-size: 0.85rem;
		padding-right: 1.4rem;
	}
	.spc-select:focus,
	.spc-num-input:focus {
		outline: none;
		border-color: var(--tb-accent, #6366f1);
		box-shadow: 0 0 0 2px color-mix(in srgb, var(--tb-accent, #6366f1) 22%, transparent);
	}
	.spc-select:disabled,
	.spc-num-input:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}
	.spc-static {
		font-size: 0.7rem;
		color: var(--tb-cell-muted);
		padding-left: 0.5rem;
	}

	.sprint-composer-actions {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	.spc-actions-right {
		display: flex;
		align-items: center;
		gap: 0.4rem;
	}

	.spc-add-row-btn {
		display: inline-flex;
		align-items: center;
		gap: 0.32rem;
		height: 2rem;
		padding: 0 0.7rem;
		border-radius: 9px;
		border: 1px dashed var(--tb-grid-col-border);
		background: transparent;
		color: var(--tb-cell-muted);
		font-size: 0.78rem;
		font-weight: 500;
		cursor: pointer;
		transition: color 0.15s ease, border-color 0.15s ease, background 0.15s ease;
	}
	.spc-add-row-btn svg {
		width: 0.88rem;
		height: 0.88rem;
		stroke: currentColor;
		stroke-width: 2.2;
		fill: none;
		stroke-linecap: round;
	}
	.spc-add-row-btn:hover:not(:disabled) {
		color: var(--tb-accent, #6366f1);
		border-color: var(--tb-accent, #6366f1);
		background: color-mix(in srgb, var(--tb-accent, #6366f1) 7%, transparent);
	}
	.spc-add-row-btn:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}

	.sprint-composer-submit,
	.sprint-composer-cancel {
		height: 2rem;
		padding: 0 0.82rem;
		border-radius: 9px;
		border: 1px solid var(--tb-btn-border);
		font-size: 0.78rem;
		font-weight: 600;
		cursor: pointer;
		transition: background 0.15s ease, border-color 0.15s ease, color 0.15s ease;
	}

	.sprint-composer-submit {
		background: var(--tb-accent, #6366f1);
		border-color: var(--tb-accent, #6366f1);
		color: #fff;
	}
	.sprint-composer-submit:hover:not(:disabled) {
		background: color-mix(in srgb, var(--tb-accent, #6366f1) 85%, #000 15%);
	}

	.sprint-composer-cancel {
		background: transparent;
		color: var(--tb-cell-muted);
		border-color: var(--tb-grid-col-border);
	}
	.sprint-composer-cancel:hover:not(:disabled) {
		color: var(--tb-cell-text);
		border-color: var(--tb-input-border);
	}

	.sprint-composer-submit:disabled,
	.sprint-composer-cancel:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}

	/* ── inline expand edit row ── */
	.edit-expand-row {
		background: color-mix(in srgb, var(--tb-grid-head-bg) 5%, var(--tb-grid-bg));
	}

	.edit-expand-cell {
		padding: 0.6rem 0.9rem 0.8rem !important;
		border-bottom: 2px solid color-mix(in srgb, var(--tb-accent) 30%, transparent) !important;
		height: auto !important;
	}

	.edit-expand-inner {
		display: flex;
		flex-direction: column;
		gap: 0.6rem;
	}

	.task-detail-section {
		display: grid;
		grid-template-columns: minmax(0, 1.1fr) minmax(0, 0.9fr);
		gap: 0.75rem;
		padding: 0.15rem 0 0.2rem;
	}

	.task-detail-copy,
	.task-detail-guide {
		border: 1px solid var(--tb-editor-border);
		border-radius: 12px;
		background: color-mix(in srgb, var(--tb-editor-bg) 86%, transparent);
		padding: 0.78rem 0.85rem;
		display: flex;
		flex-direction: column;
		gap: 0.55rem;
	}

	.task-detail-guide-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.65rem;
	}

	.task-detail-guide-head h4 {
		margin: 0;
		font-size: 0.86rem;
		color: var(--tb-cell-text);
	}

	.task-detail-guide-head span,
	.task-detail-notice,
	.task-detail-error,
	.task-detail-empty {
		font-size: 0.76rem;
	}

	.task-detail-notice {
		margin: 0;
		color: color-mix(in srgb, var(--tb-status-done) 72%, var(--tb-cell-text));
	}

	.task-detail-error {
		margin: 0;
		color: color-mix(in srgb, #b91c1c 72%, var(--tb-cell-text));
	}

	.task-detail-summary,
	.task-detail-empty {
		margin: 0;
		color: var(--tb-cell-muted);
		line-height: 1.5;
	}

	.task-detail-steps {
		margin: 0;
		padding-left: 1.1rem;
		display: flex;
		flex-direction: column;
		gap: 0.38rem;
		color: var(--tb-cell-text);
		font-size: 0.8rem;
		line-height: 1.45;
	}

	.edit-expand-fields {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem 0.75rem;
		align-items: flex-end;
	}

	.eef {
		display: flex;
		flex-direction: column;
		gap: 0.2rem;
		min-width: 110px;
	}

	.eef-wide {
		flex: 1 1 220px;
	}

	.eef-full {
		width: 100%;
	}

	.eef-label {
		font-size: 0.68rem;
		font-weight: 700;
		color: var(--tb-cell-muted);
		text-transform: uppercase;
		letter-spacing: 0.05em;
		white-space: nowrap;
	}

	.eef-input,
	.eef-select {
		padding: 0.32rem 0.5rem;
		border: 1px solid var(--tb-editor-border);
		border-radius: 7px;
		background: var(--tb-editor-bg);
		color: var(--tb-cell-text);
		font-size: 0.84rem;
		font-family: inherit;
		outline: none;
		box-sizing: border-box;
		width: 100%;
		transition:
			border-color 0.12s,
			box-shadow 0.12s;
	}

	.eef-textarea {
		min-height: 5.5rem;
		resize: vertical;
		line-height: 1.45;
	}

	.eef-input:focus,
	.eef-select:focus {
		border-color: var(--tb-accent-strong);
		box-shadow: 0 0 0 2px var(--tb-editor-ring);
	}

	.eef-num {
		text-align: right;
	}

	.edit-expand-actions {
		display: flex;
		gap: 0.45rem;
	}

	.task-detail-actions {
		display: flex;
		flex-wrap: wrap;
		gap: 0.45rem;
	}

	.eef-save {
		padding: 0.3rem 0.8rem;
		border-radius: 7px;
		border: none;
		background: var(--tb-grid-head-bg);
		color: var(--tb-grid-head-text);
		font-size: 0.78rem;
		font-weight: 600;
		cursor: pointer;
	}
	.eef-save:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}
	.eef-save:not(:disabled):hover {
		opacity: 0.82;
	}

	.eef-secondary {
		padding: 0.3rem 0.8rem;
		border-radius: 7px;
		border: 1px solid var(--tb-editor-border);
		background: var(--tb-grid-bg);
		color: var(--tb-cell-text);
		font-size: 0.78rem;
		font-weight: 600;
		cursor: pointer;
	}

	.eef-secondary:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.eef-cancel {
		padding: 0.3rem 0.7rem;
		border-radius: 7px;
		border: 1px solid var(--tb-editor-border);
		background: transparent;
		color: var(--tb-cell-muted);
		font-size: 0.78rem;
		cursor: pointer;
	}
	.eef-cancel:hover {
		color: var(--tb-cell-text);
	}

	/* highlight the row above the edit row */
	.task-grid tbody tr.row-modal-open {
		background: color-mix(in srgb, var(--tb-grid-head-bg) 8%, transparent);
		box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--tb-accent) 35%, transparent);
	}

	.quick-edit-panel {
		grid-row: 3;
		display: grid;
		gap: 0.55rem;
		padding: 0.72rem;
		border-radius: 14px;
		border: 1px solid var(--tb-panel-border);
		background: var(--tb-panel-bg);
		backdrop-filter: blur(10px);
		box-shadow: var(--tb-panel-shadow);
	}

	.board-content-slot {
		grid-row: 4;
		min-height: 0;
		display: grid;
	}

	.board-content-slot > .sprint-groups,
	.board-content-slot > .kanban-board,
	.board-content-slot > .support-view,
	.board-content-slot > .board-state {
		min-height: 0;
		height: 100%;
	}

	.quick-edit-head strong {
		font-size: 0.8rem;
		color: var(--tb-cell-text);
		font-weight: 700;
	}

	.quick-edit-controls {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		flex-wrap: wrap;
	}

	.quick-edit-input {
		flex: 1;
		min-width: 220px;
		height: 2rem;
		padding: 0 0.65rem;
		border-radius: 9px;
		border: 1px solid var(--tb-input-border);
		background: var(--tb-input-bg);
		color: var(--tb-input-text);
		font-size: 0.82rem;
	}

	.quick-edit-btn {
		height: 2rem;
		padding: 0 0.72rem;
		border-radius: 9px;
		border: 1px solid var(--tb-btn-border);
		background: var(--tb-btn-bg);
		color: var(--tb-btn-text);
		font-size: 0.78rem;
		font-weight: 650;
		cursor: pointer;
	}

	.quick-edit-save {
		background: color-mix(in srgb, var(--tb-btn-bg) 64%, #ffffff 36%);
	}

	.quick-custom-fields {
		display: grid;
		gap: 0.58rem;
		padding-top: 0.25rem;
		border-top: 1px solid var(--tb-grid-col-border);
	}

	.quick-custom-fields h4 {
		margin: 0;
		font-size: 0.75rem;
		font-weight: 700;
		color: var(--tb-cell-muted);
	}

	.quick-custom-fields-grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
		gap: 0.55rem;
	}

	.quick-custom-field {
		display: grid;
		gap: 0.32rem;
	}

	.quick-custom-field > span {
		font-size: 0.7rem;
		color: var(--tb-cell-muted);
	}

	.quick-custom-field input,
	.quick-custom-field select {
		height: 2rem;
		padding: 0 0.58rem;
		border-radius: 9px;
		border: 1px solid var(--tb-input-border);
		background: var(--tb-input-bg);
		color: var(--tb-input-text);
		font-size: 0.76rem;
	}

	.quick-custom-field input[type='checkbox'] {
		width: 1.02rem;
		height: 1.02rem;
		padding: 0;
	}

	.quick-custom-options {
		display: grid;
		gap: 0.3rem;
		padding: 0.42rem 0.5rem;
		border-radius: 9px;
		border: 1px solid var(--tb-input-border);
		background: var(--tb-input-bg);
		max-height: 118px;
		overflow: auto;
	}

	.quick-custom-option {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		font-size: 0.74rem;
	}

	.quick-custom-option input[type='checkbox'] {
		width: 0.9rem;
		height: 0.9rem;
	}

	.quick-custom-empty {
		font-size: 0.72rem;
		color: var(--tb-cell-muted);
	}

	.quick-relations {
		display: grid;
		gap: 0.56rem;
		padding-top: 0.3rem;
		border-top: 1px solid var(--tb-grid-col-border);
	}

	.quick-relations h4 {
		margin: 0;
		font-size: 0.75rem;
		font-weight: 700;
		color: var(--tb-cell-muted);
	}

	.quick-relation-list {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(210px, 1fr));
		gap: 0.36rem;
		max-height: 130px;
		overflow: auto;
		padding: 0.1rem 0.05rem;
	}

	.quick-relation-option {
		display: inline-flex;
		align-items: center;
		gap: 0.45rem;
		font-size: 0.74rem;
		color: var(--tb-cell-text);
	}

	.quick-subtask-list {
		display: grid;
		gap: 0.4rem;
	}

	.quick-subtask-row {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr) auto;
		align-items: center;
		gap: 0.45rem;
	}

	.quick-subtask-row input[type='text'] {
		height: 1.85rem;
		padding: 0 0.58rem;
		border-radius: 8px;
		border: 1px solid var(--tb-input-border);
		background: var(--tb-input-bg);
		color: var(--tb-input-text);
		font-size: 0.75rem;
	}

	.quick-subtask-remove {
		height: 1.8rem;
		padding: 0 0.54rem;
		border-radius: 8px;
		border: 1px solid var(--tb-btn-border);
		background: var(--tb-btn-bg);
		color: var(--tb-btn-text);
		font-size: 0.71rem;
		font-weight: 650;
		cursor: pointer;
	}

	.quick-subtask-actions {
		display: inline-flex;
	}

	.board-state {
		height: 100%;
		min-height: 220px;
		display: grid;
		place-items: center;
		text-align: center;
		padding: 1rem;
		border-radius: 14px;
		border: 1px solid var(--tb-state-border);
		background: var(--tb-state-bg);
		color: var(--tb-state-text);
		font-size: 0.92rem;
	}

	.board-state.error {
		color: var(--tb-error-text);
	}

	.kanban-board {
		min-height: 0;
		height: 100%;
		display: grid;
		grid-template-columns: repeat(3, minmax(280px, 1fr));
		gap: 0.82rem;
		overflow: auto;
		padding: 0.05rem 0.2rem 0.2rem 0.05rem;
		scrollbar-width: thin;
	}

	.kanban-column {
		min-height: 0;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr);
		border-radius: 16px;
		border: 1px solid color-mix(in srgb, var(--tb-accent) 16%, var(--tb-grid-border));
		background: var(--tb-panel-bg);
		backdrop-filter: blur(10px);
		box-shadow: var(--tb-panel-shadow);
	}

	.kanban-column-head {
		padding: 0.6rem 0.7rem;
		border-bottom: 1px solid var(--tb-grid-col-border);
	}

	.kanban-column:nth-child(1) .kanban-column-head {
		background: linear-gradient(
			180deg,
			color-mix(in srgb, var(--tb-status-todo) 14%, var(--tb-panel-bg)),
			transparent
		);
	}

	.kanban-column:nth-child(2) .kanban-column-head {
		background: linear-gradient(
			180deg,
			color-mix(in srgb, var(--tb-status-progress) 18%, var(--tb-panel-bg)),
			transparent
		);
	}

	.kanban-column:nth-child(3) .kanban-column-head {
		background: linear-gradient(
			180deg,
			color-mix(in srgb, var(--tb-status-done) 16%, var(--tb-panel-bg)),
			transparent
		);
	}

	.kanban-column-title-wrap {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.55rem;
	}

	.kanban-column-title-wrap h3 {
		margin: 0;
		font-size: 0.84rem;
		font-weight: 700;
		color: var(--tb-cell-text);
	}

	.kanban-column-title-wrap span {
		min-width: 1.7rem;
		height: 1.3rem;
		padding: 0 0.35rem;
		display: inline-grid;
		place-items: center;
		border-radius: 999px;
		border: 1px solid var(--tb-grid-col-border);
		background: color-mix(in srgb, var(--tb-btn-bg) 78%, transparent);
		color: var(--tb-cell-muted);
		font-size: 0.72rem;
		font-weight: 700;
		font-variant-numeric: tabular-nums;
	}

	.kanban-column:nth-child(1) .kanban-column-title-wrap span {
		border-color: color-mix(in srgb, var(--tb-status-todo) 36%, var(--tb-grid-col-border));
		background: color-mix(in srgb, var(--tb-status-todo) 16%, var(--tb-btn-bg));
		color: color-mix(in srgb, var(--tb-status-todo) 72%, var(--tb-cell-text));
	}

	.kanban-column:nth-child(2) .kanban-column-title-wrap span {
		border-color: color-mix(in srgb, var(--tb-status-progress) 42%, var(--tb-grid-col-border));
		background: color-mix(in srgb, var(--tb-status-progress) 18%, var(--tb-btn-bg));
		color: color-mix(in srgb, var(--tb-status-progress-text) 72%, var(--tb-cell-text));
	}

	.kanban-column:nth-child(3) .kanban-column-title-wrap span {
		border-color: color-mix(in srgb, var(--tb-status-done) 42%, var(--tb-grid-col-border));
		background: color-mix(in srgb, var(--tb-status-done) 16%, var(--tb-btn-bg));
		color: color-mix(in srgb, var(--tb-status-done) 72%, var(--tb-cell-text));
	}

	.kanban-column-body {
		min-height: 0;
		display: grid;
		align-content: start;
		gap: 0.58rem;
		overflow: auto;
		padding: 0.62rem;
	}

	.kanban-empty {
		border: 1px dashed var(--tb-grid-col-border);
		border-radius: 12px;
		padding: 0.9rem 0.75rem;
		text-align: center;
		font-size: 0.76rem;
		color: var(--tb-cell-muted);
	}

	.kanban-card {
		display: grid;
		gap: 0.54rem;
		padding: 0.62rem;
		border-radius: 14px;
		border: 1px solid var(--tb-grid-col-border);
		background: var(--tb-grid-bg);
		box-shadow:
			0 10px 20px rgba(14, 24, 46, 0.12),
			inset 0 1px 0 rgba(255, 255, 255, 0.18);
		transition:
			transform 180ms ease,
			box-shadow 180ms ease,
			border-color 180ms ease;
	}

	.kanban-card:hover {
		transform: translateY(-2px);
		border-color: color-mix(in srgb, var(--tb-accent) 42%, var(--tb-grid-col-border));
		box-shadow:
			0 14px 28px rgba(14, 24, 46, 0.18),
			inset 0 1px 0 rgba(255, 255, 255, 0.22);
	}

	.kanban-card-top {
		display: flex;
		align-items: flex-start;
		gap: 0.4rem;
	}

	.kanban-card-title {
		flex: 1;
		min-width: 0;
		padding: 0;
		margin: 0;
		border: none;
		background: transparent;
		color: var(--tb-cell-text);
		text-align: left;
		font-size: 0.84rem;
		line-height: 1.35;
		font-weight: 700;
		cursor: pointer;
	}

	.kanban-card-title:hover {
		text-decoration: underline;
		text-underline-offset: 0.14rem;
	}

	.kanban-mini-btn {
		width: 1.5rem;
		height: 1.5rem;
		flex: 0 0 auto;
		display: grid;
		place-items: center;
		padding: 0;
		border-radius: 8px;
		border: 1px solid transparent;
		background: var(--tb-icon-bg);
		color: var(--tb-icon-text);
		font-size: 0.88rem;
		font-weight: 700;
		cursor: pointer;
	}

	.kanban-mini-btn:hover:not(:disabled) {
		background: var(--tb-icon-bg-hover);
	}

	.kanban-mini-btn:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}

	.kanban-status-row {
		display: flex;
		align-items: center;
	}

	.kanban-relation-badges {
		margin-top: -0.15rem;
	}

	.kanban-meta-grid {
		display: grid;
		grid-template-columns: 1fr;
		gap: 0.38rem;
	}

	.kanban-meta-btn {
		width: 100%;
		display: grid;
		gap: 0.24rem;
		padding: 0.48rem 0.52rem;
		border-radius: 10px;
		border: 1px solid var(--tb-grid-col-border);
		background: color-mix(in srgb, var(--tb-panel-bg) 78%, transparent);
		color: var(--tb-cell-text);
		cursor: pointer;
		text-align: left;
	}

	.kanban-meta-btn:hover {
		background: color-mix(in srgb, var(--tb-btn-bg) 62%, transparent);
	}

	.kanban-owner-btn .owner-chip {
		gap: 0.4rem;
	}

	.meta-label {
		font-size: 0.69rem;
		font-weight: 700;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--tb-cell-muted);
	}

	.meta-value {
		font-size: 0.78rem;
		font-weight: 700;
		color: var(--tb-cell-text);
	}

	.kanban-card-footer {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		font-size: 0.71rem;
		color: var(--tb-cell-muted);
	}

	.kanban-roles {
		display: flex;
		flex-wrap: wrap;
		gap: 4px;
		padding: 2px 0;
	}
	.kanban-role-chip {
		font-size: 0.67rem;
		padding: 1px 6px;
		border-radius: 20px;
		background: rgba(124, 58, 237, 0.1);
		color: #7c3aed;
		border: 1px solid rgba(124, 58, 237, 0.2);
		white-space: nowrap;
	}

	.support-view {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-columns: minmax(320px, 0.95fr) minmax(0, 1.05fr);
		gap: 0.85rem;
		overflow: hidden;
	}

	.support-composer,
	.support-ticket-board {
		min-height: 0;
		display: grid;
		align-content: start;
		gap: 0.65rem;
		border: 1px solid var(--tb-grid-border);
		border-radius: 16px;
		background: var(--tb-panel-bg);
		padding: 0.72rem;
		backdrop-filter: blur(10px);
		box-shadow: var(--tb-panel-shadow);
	}

	.support-composer {
		overflow: auto;
	}

	.support-ticket-board {
		overflow: hidden;
	}

	.support-composer-head,
	.support-ticket-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
	}

	.support-composer-head h3,
	.support-ticket-head h3 {
		margin: 0;
		font-size: 0.9rem;
		color: var(--tb-cell-text);
	}

	.support-composer-head p {
		margin: 0.12rem 0 0;
		font-size: 0.74rem;
		color: var(--tb-cell-muted);
	}

	.support-composer-head span,
	.support-ticket-head span {
		height: 1.45rem;
		padding: 0 0.56rem;
		border-radius: 999px;
		border: 1px solid var(--tb-grid-col-border);
		background: color-mix(in srgb, var(--tb-btn-bg) 74%, transparent);
		display: inline-grid;
		place-items: center;
		font-size: 0.72rem;
		font-weight: 700;
		color: var(--tb-cell-muted);
		font-variant-numeric: tabular-nums;
	}

	.support-form-grid {
		display: grid;
		grid-template-columns: minmax(0, 1fr) 142px;
		gap: 0.55rem;
	}

	.support-field {
		display: grid;
		gap: 0.3rem;
	}

	.support-field-wide {
		grid-column: 1 / -1;
	}

	.support-field span {
		font-size: 0.69rem;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--tb-cell-muted);
	}

	.support-field input,
	.support-field select,
	.support-field textarea {
		width: 100%;
		border: 1px solid var(--tb-input-border);
		background: var(--tb-input-bg);
		color: var(--tb-input-text);
		border-radius: 9px;
		padding: 0.48rem 0.58rem;
		font-size: 0.8rem;
	}

	.support-field textarea {
		min-height: 4.8rem;
		resize: vertical;
	}

	.support-field input:focus-visible,
	.support-field select:focus-visible,
	.support-field textarea:focus-visible {
		outline: none;
		box-shadow: 0 0 0 3px var(--tb-editor-ring);
	}

	.support-pickers {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 0.6rem;
	}

	.support-picker {
		border: 1px solid var(--tb-grid-col-border);
		border-radius: 11px;
		padding: 0.55rem;
		display: grid;
		gap: 0.45rem;
		background: color-mix(in srgb, var(--tb-grid-bg) 86%, transparent);
	}

	.support-picker-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	.support-picker-head h4 {
		margin: 0;
		font-size: 0.78rem;
		color: var(--tb-cell-text);
	}

	.support-picker-head span {
		font-size: 0.68rem;
		color: var(--tb-cell-muted);
		font-weight: 700;
	}

	.support-task-list {
		display: grid;
		gap: 0.34rem;
		max-height: 180px;
		overflow: auto;
		padding-right: 0.1rem;
	}

	.support-check-row {
		display: flex;
		align-items: center;
		gap: 0.42rem;
		padding: 0.34rem 0.42rem;
		border-radius: 8px;
		border: 1px solid transparent;
		background: transparent;
		color: var(--tb-cell-text);
		font-size: 0.76rem;
	}

	.support-check-row:hover {
		background: color-mix(in srgb, var(--tb-btn-bg) 56%, transparent);
		border-color: var(--tb-grid-col-border);
	}

	.support-check-row input[type='checkbox'] {
		width: 14px;
		height: 14px;
		cursor: pointer;
	}

	.support-member-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(130px, 1fr));
		gap: 0.34rem;
	}

	.support-member-option {
		border: 1px solid var(--tb-grid-col-border);
		background: var(--tb-grid-bg);
		color: var(--tb-cell-text);
		border-radius: 9px;
		padding: 0.36rem 0.44rem;
		display: flex;
		align-items: center;
		gap: 0.36rem;
		font-size: 0.72rem;
		cursor: pointer;
		transition:
			border-color 0.16s ease,
			background 0.16s ease;
	}

	.support-member-option:hover {
		border-color: var(--tb-grid-head-bg);
	}

	.support-member-option.is-selected {
		border-color: color-mix(in srgb, var(--tb-grid-head-bg) 48%, transparent);
		background: color-mix(in srgb, var(--tb-btn-bg) 65%, transparent);
	}

	.support-empty-inline {
		border: 1px dashed var(--tb-grid-col-border);
		border-radius: 8px;
		padding: 0.5rem;
		font-size: 0.73rem;
		color: var(--tb-cell-muted);
	}

	.support-composer-actions {
		display: flex;
		align-items: center;
		gap: 0.45rem;
		justify-content: flex-end;
	}

	.support-create-btn,
	.support-clear-btn {
		height: 2rem;
		border-radius: 9px;
		border: 1px solid var(--tb-btn-border);
		background: var(--tb-btn-bg);
		color: var(--tb-btn-text);
		font-size: 0.78rem;
		font-weight: 700;
		padding: 0 0.74rem;
		cursor: pointer;
	}

	.support-create-btn {
		background: color-mix(in srgb, var(--tb-btn-bg) 66%, #ffffff 34%);
	}

	.support-create-btn:disabled,
	.support-clear-btn:disabled {
		opacity: 0.55;
		cursor: not-allowed;
	}

	.support-card-grid {
		min-height: 0;
		overflow: auto;
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(170px, 1fr));
		gap: 0.6rem;
		padding-right: 0.12rem;
	}

	.support-ticket-card {
		display: grid;
		grid-template-rows: auto auto 1fr auto auto;
		gap: 0.42rem;
		border: 1px solid var(--tb-grid-col-border);
		border-radius: 14px;
		background: var(--tb-grid-bg);
		padding: 0.58rem;
		aspect-ratio: 1 / 1;
		min-height: 170px;
		box-shadow:
			0 12px 22px rgba(16, 24, 40, 0.12),
			inset 0 1px 0 rgba(255, 255, 255, 0.15);
	}

	.support-ticket-top {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.4rem;
	}

	.support-priority {
		height: 1.32rem;
		padding: 0 0.44rem;
		border-radius: 999px;
		display: inline-grid;
		place-items: center;
		font-size: 0.62rem;
		font-weight: 800;
		text-transform: uppercase;
		letter-spacing: 0.05em;
	}

	.priority-critical {
		background: rgba(19, 20, 23, 0.2);
		color: #1f2126;
	}

	.priority-high {
		background: rgba(36, 38, 44, 0.18);
		color: #2a2d34;
	}

	.priority-medium {
		background: rgba(56, 59, 68, 0.18);
		color: #343842;
	}

	.priority-low {
		background: rgba(74, 78, 89, 0.16);
		color: #414651;
	}

	.support-ticket-top time {
		font-size: 0.66rem;
		color: var(--tb-cell-muted);
	}

	.support-ticket-card h4 {
		margin: 0;
		font-size: 0.81rem;
		line-height: 1.28;
		color: var(--tb-cell-text);
		overflow: hidden;
		display: -webkit-box;
		line-clamp: 2;
		-webkit-line-clamp: 2;
		-webkit-box-orient: vertical;
	}

	.support-ticket-card p {
		margin: 0;
		font-size: 0.72rem;
		line-height: 1.35;
		color: var(--tb-cell-muted);
		overflow: hidden;
		display: -webkit-box;
		line-clamp: 4;
		-webkit-line-clamp: 4;
		-webkit-box-orient: vertical;
	}

	.support-linked-meta {
		font-size: 0.67rem;
		color: var(--tb-cell-muted);
	}

	.support-card-footer {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.38rem;
	}

	.support-avatar-stack {
		display: inline-flex;
		align-items: center;
		padding-left: 0.08rem;
	}

	.support-avatar {
		width: 1.3rem;
		height: 1.3rem;
		border-radius: 999px;
		border: 2px solid var(--tb-grid-bg);
		background: linear-gradient(160deg, #4a4d56, #26282f);
		color: var(--tb-avatar-text);
		display: inline-grid;
		place-items: center;
		font-size: 0.53rem;
		font-weight: 800;
		margin-left: calc(-0.34rem + (var(--avatar-index, 0) * 0rem));
		text-transform: uppercase;
	}

	.support-avatar-stack .support-avatar:first-child {
		margin-left: 0;
	}

	.support-avatar-more {
		background: var(--tb-btn-bg);
		color: var(--tb-btn-text);
	}

	.support-avatar-empty {
		margin-left: 0;
		background: var(--tb-btn-bg);
		color: var(--tb-btn-text);
	}

	.support-sprint-chip {
		font-size: 0.62rem;
		font-weight: 700;
		color: var(--tb-cell-muted);
	}

	.support-empty {
		border: 1px dashed var(--tb-grid-col-border);
		border-radius: 10px;
		padding: 0.72rem;
		font-size: 0.78rem;
		color: var(--tb-cell-muted);
		text-align: center;
	}

	.sprint-groups {
		min-height: 0;
		overflow: auto;
		display: grid;
		align-content: start;
		gap: 0.85rem;
		padding-right: 0.2rem;
	}

	.sprint-group {
		display: grid;
		gap: 0.45rem;
		padding: 0.65rem 0.65rem 0.55rem;
		border-radius: 18px;
		border: 1px solid var(--tb-grid-border);
		background: var(--tb-panel-bg);
		backdrop-filter: blur(10px);
		box-shadow: var(--tb-panel-shadow);
	}

	.sprint-group-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		padding: 0 0.2rem;
	}

	.sgh-left {
		min-width: 0;
	}

	.sprint-group-header h3 {
		margin: 0;
		font-size: 0.88rem;
		font-weight: 700;
		color: var(--tb-cell-text);
	}

	.sprint-group-header p {
		margin: 0.1rem 0 0;
		font-size: 0.74rem;
		color: var(--tb-cell-muted);
	}

	.sgh-actions {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		flex-shrink: 0;
		flex-wrap: wrap;
		justify-content: flex-end;
	}

	.sgh-hint {
		font-size: 0.7rem;
		color: var(--tb-cell-muted);
	}

	.sgh-btn {
		height: 1.9rem;
		padding: 0 0.75rem;
		border-radius: 8px;
		font-size: 0.78rem;
		font-weight: 600;
		cursor: pointer;
		border: 1px solid;
		transition:
			background 0.15s,
			border-color 0.15s;
	}

	.sgh-btn:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.sgh-edit {
		color: var(--tb-btn-text);
		background: var(--tb-btn-bg);
		border-color: var(--tb-btn-border);
	}
	.sgh-edit:hover:not(:disabled) {
		background: color-mix(in srgb, var(--tb-btn-bg) 72%, #ffffff 28%);
	}

	.sgh-done {
		color: var(--tb-btn-text);
		background: color-mix(in srgb, var(--tb-accent) 14%, transparent);
		border-color: color-mix(in srgb, var(--tb-accent) 38%, transparent);
		font-weight: 700;
	}
	.sgh-done:hover:not(:disabled) {
		background: color-mix(in srgb, var(--tb-accent) 22%, transparent);
	}

	.sgh-request {
		color: #d97706;
		background: color-mix(in srgb, #f59e0b 10%, transparent);
		border-color: color-mix(in srgb, #f59e0b 40%, transparent);
	}
	.sgh-request:hover:not(:disabled) {
		background: color-mix(in srgb, #f59e0b 18%, transparent);
	}

	.sgh-add {
		color: var(--tb-btn-text);
		background: var(--tb-btn-bg);
		border-color: var(--tb-btn-border);
	}
	.sgh-add:hover:not(:disabled) {
		background: color-mix(in srgb, var(--tb-btn-bg) 72%, #ffffff 28%);
	}

	.sgh-delete {
		color: var(--tb-error-text);
		background: color-mix(in srgb, var(--tb-error-text) 10%, transparent);
		border-color: color-mix(in srgb, var(--tb-error-text) 35%, transparent);
	}
	.sgh-delete:hover:not(:disabled) {
		background: color-mix(in srgb, var(--tb-error-text) 18%, transparent);
	}

	/* Sprint inline add form */
	.sprint-add-form {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.5rem 0.6rem;
		border: 1px solid var(--tb-grid-border);
		border-top: none;
		border-radius: 0 0 14px 14px;
		background: var(--tb-form-bg);
	}

	.sprint-add-form input {
		flex: 1;
		min-width: 0;
		height: 2rem;
		border-radius: 8px;
		border: 1px solid var(--tb-input-border);
		background: var(--tb-input-bg);
		color: var(--tb-input-text);
		padding: 0 0.65rem;
		font-size: 0.84rem;
	}

	.sprint-add-form button {
		height: 2rem;
		padding: 0 0.75rem;
		border-radius: 8px;
		border: 1px solid var(--tb-btn-border);
		background: var(--tb-btn-bg);
		color: var(--tb-btn-text);
		font-size: 0.8rem;
		font-weight: 600;
		cursor: pointer;
	}
	.sprint-add-form button:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}
	.sprint-add-form button[type='button'] {
		background: transparent;
		border-color: var(--tb-grid-border);
		color: var(--tb-cell-muted);
	}

	.empty-sprint-row {
		height: 2.6rem;
		text-align: center;
		color: var(--tb-cell-muted);
		font-size: 0.8rem;
	}

	.grid-shell {
		min-height: 0;
		overflow: auto;
		border-radius: 16px;
		border: 1px solid color-mix(in srgb, var(--tb-accent) 14%, var(--tb-grid-border));
		background: linear-gradient(
			180deg,
			color-mix(in srgb, var(--tb-accent-soft) 12%, var(--tb-grid-bg) 88%),
			var(--tb-grid-bg)
		);
		scrollbar-width: thin;
		box-shadow:
			inset 0 1px 0 rgba(255, 255, 255, 0.08),
			0 10px 18px rgba(20, 33, 59, 0.1);
	}

	.task-grid {
		width: 100%;
		min-width: 980px;
		border-collapse: separate;
		border-spacing: 0;
		table-layout: fixed;
	}

	.task-grid thead th {
		position: sticky;
		top: 0;
		z-index: 5;
		height: 2.35rem;
		padding: 0.35rem 0.7rem;
		text-align: left;
		font-size: 0.74rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--tb-grid-head-text);
		background: linear-gradient(
			180deg,
			color-mix(in srgb, var(--tb-grid-head-bg) 90%, #ffffff 10%),
			var(--tb-grid-head-bg)
		);
		white-space: nowrap;
		border-bottom: 1px solid var(--tb-grid-row-border);
	}

	.th-sort {
		padding: 0;
	}

	.th-sort-btn {
		width: 100%;
		height: 100%;
		border: none;
		padding: 0.35rem 0.7rem;
		background: transparent;
		color: inherit;
		display: inline-flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.35rem;
		font: inherit;
		letter-spacing: inherit;
		text-transform: inherit;
		cursor: pointer;
	}

	.th-sort-btn:hover {
		background: color-mix(in srgb, #ffffff 12%, transparent);
	}

	.th-sort-btn.is-active .th-sort-icon {
		color: #ffffff;
		opacity: 1;
	}

	.th-sort-icon {
		opacity: 0.72;
		font-size: 0.76rem;
		font-weight: 800;
		line-height: 1;
	}

	.task-grid thead th:not(:last-child),
	.task-grid tbody td:not(:last-child) {
		border-right: 1px solid var(--tb-grid-col-border);
	}

	.task-grid tbody tr {
		position: relative;
		transition:
			background 0.18s ease,
			box-shadow 0.18s ease;
	}

	.task-grid tbody tr:hover {
		background: var(--tb-grid-row-hover);
		box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--tb-accent) 24%, transparent);
	}

	.task-grid tbody tr.row-todo {
		background: color-mix(in srgb, var(--tb-status-todo) 5%, transparent);
		box-shadow: inset 3px 0 0 color-mix(in srgb, var(--tb-status-todo) 58%, transparent);
	}

	.task-grid tbody tr.row-progress {
		background: color-mix(in srgb, var(--tb-status-progress) 7%, transparent);
		box-shadow: inset 3px 0 0 color-mix(in srgb, var(--tb-status-progress) 62%, transparent);
	}

	.task-grid tbody tr.row-done {
		background: var(--tb-grid-row-done);
		box-shadow: inset 3px 0 0 color-mix(in srgb, var(--tb-status-done) 62%, transparent);
	}

	.task-grid tbody tr.row-todo:hover {
		box-shadow:
			inset 3px 0 0 color-mix(in srgb, var(--tb-status-todo) 72%, transparent),
			inset 0 0 0 1px color-mix(in srgb, var(--tb-accent) 24%, transparent);
	}

	.task-grid tbody tr.row-progress:hover {
		box-shadow:
			inset 3px 0 0 color-mix(in srgb, var(--tb-status-progress) 78%, transparent),
			inset 0 0 0 1px color-mix(in srgb, var(--tb-accent) 24%, transparent);
	}

	.task-grid tbody tr.row-done:hover {
		box-shadow:
			inset 3px 0 0 color-mix(in srgb, var(--tb-status-done) 78%, transparent),
			inset 0 0 0 1px color-mix(in srgb, var(--tb-accent) 24%, transparent);
	}

	.task-grid tbody tr.row-selected {
		background: color-mix(in srgb, var(--tb-grid-head-bg) 8%, transparent);
	}

	.task-grid tbody tr.row-status-open {
		z-index: 14;
	}

	.th-check,
	.check-cell {
		width: 42px;
		text-align: center;
		padding: 0 0.4rem;
	}

	.check-cell input[type='checkbox'],
	.th-check input[type='checkbox'] {
		width: 15px;
		height: 15px;
		cursor: pointer;
		accent-color: var(--tb-grid-head-bg);
	}

	.budget-val {
		font-variant-numeric: tabular-nums;
	}

	.task-grid tbody td {
		height: 2.82rem;
		padding: 0.35rem 0.65rem;
		font-size: 0.84rem;
		color: var(--tb-cell-text);
		vertical-align: middle;
		border-bottom: 1px solid var(--tb-grid-row-border);
	}

	.cell {
		position: relative;
	}

	.th-task,
	.task-cell {
		width: 38%;
		padding-left: 0.55rem;
		cursor: pointer;
	}
	.task-cell:not(.is-editing):hover {
		background: color-mix(in srgb, var(--tb-grid-head-bg) 5%, transparent);
	}

	.th-status,
	.status-cell {
		width: 14%;
		max-width: 0;
		padding: 0.3rem 0.4rem;
	}

	.th-status {
		overflow: hidden;
	}

	.status-cell {
		overflow: hidden;
	}

	.status-cell.menu-open {
		overflow: visible;
		z-index: 18;
	}

	.th-owner,
	.owner-cell {
		width: 16%;
		cursor: pointer;
	}
	.owner-cell:not(.is-editing):hover {
		background: color-mix(in srgb, var(--tb-grid-head-bg) 5%, transparent);
	}

	.th-budget,
	.budget-cell {
		width: 10%;
		font-size: 0.8rem;
		font-weight: 600;
		white-space: nowrap;
		cursor: pointer;
	}
	.budget-cell:not(.is-editing):hover {
		background: color-mix(in srgb, var(--tb-grid-head-bg) 5%, transparent);
	}

	.th-spent,
	.spent-cell {
		width: 10%;
		font-size: 0.8rem;
		font-weight: 600;
		white-space: nowrap;
		cursor: pointer;
	}
	.spent-cell:not(.is-editing):hover {
		background: color-mix(in srgb, var(--tb-grid-head-bg) 5%, transparent);
	}

	.th-date,
	.th-start-date,
	.th-due-date,
	.date-cell {
		width: 9%;
		font-size: 0.8rem;
		white-space: nowrap;
		cursor: pointer;
	}
	.date-cell:not(.is-editing):hover {
		background: color-mix(in srgb, var(--tb-grid-head-bg) 5%, transparent);
	}
	.date-val {
		font-size: 0.8rem;
		color: var(--tb-cell-muted);
	}

	.th-updated,
	.updated-cell {
		width: 8%;
		max-width: 0;
		overflow: hidden;
		font-size: 0.72rem;
		color: var(--tb-cell-muted);
		white-space: nowrap;
		text-overflow: ellipsis;
		padding: 0.3rem 0.4rem;
	}

	.task-content {
		display: flex;
		align-items: center;
		gap: 0.45rem;
		width: 100%;
		min-width: 0;
	}

	.task-title {
		display: block;
		max-width: 230px;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-weight: 600;
	}

	.cell-trigger {
		padding: 0;
		margin: 0;
		border: none;
		background: transparent;
		color: inherit;
		font: inherit;
		cursor: pointer;
		text-align: left;
	}

	.cell-trigger:disabled {
		cursor: not-allowed;
		opacity: 0.7;
	}

	.task-title-trigger {
		flex: 1;
		width: 100%;
		max-width: none;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-weight: 600;
	}

	.task-relation-badges {
		display: inline-flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.26rem;
	}

	.task-badge {
		display: inline-flex;
		align-items: center;
		height: 1.18rem;
		padding: 0 0.42rem;
		border-radius: 999px;
		font-size: 0.64rem;
		font-weight: 700;
		letter-spacing: 0.01em;
		white-space: nowrap;
	}

	.task-badge-blocked {
		background: color-mix(in srgb, #ef4444 20%, transparent);
		color: color-mix(in srgb, #b91c1c 72%, var(--tb-cell-text));
		border: 1px solid color-mix(in srgb, #ef4444 35%, transparent);
	}

	.task-badge-subtasks {
		background: color-mix(in srgb, var(--tb-grid-head-bg) 10%, transparent);
		color: var(--tb-cell-muted);
		border: 1px solid color-mix(in srgb, var(--tb-grid-head-bg) 20%, transparent);
	}

	.owner-trigger {
		width: 100%;
	}

	.budget-trigger {
		font: inherit;
	}

	.spent-trigger {
		font: inherit;
	}

	.task-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.22rem;
		opacity: 0;
		pointer-events: none;
		transition: opacity 0.16s ease;
	}

	.task-grid tbody tr:hover .task-actions {
		opacity: 1;
		pointer-events: auto;
	}

	.task-actions button {
		width: 1.45rem;
		height: 1.45rem;
		display: grid;
		place-items: center;
		padding: 0;
		border-radius: 8px;
		border: 1px solid transparent;
		background: var(--tb-icon-bg);
		color: var(--tb-icon-text);
		font-size: 0.85rem;
		font-weight: 700;
		cursor: pointer;
	}

	.task-actions button:hover:not(:disabled) {
		background: var(--tb-icon-bg-hover);
	}

	.task-actions button:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}

	.row-check {
		position: absolute;
		left: 0.12rem;
		top: 50%;
		transform: translateY(-50%);
		width: 1.32rem;
		height: 1.32rem;
		border-radius: 999px;
		border: 1px solid var(--tb-grid-col-border);
		background: transparent;
		color: color-mix(in srgb, var(--tb-cell-text) 82%, #ffffff);
		display: grid;
		place-items: center;
		cursor: pointer;
		opacity: 0;
		transition:
			opacity 0.15s ease,
			background 0.15s ease,
			border-color 0.15s ease;
	}

	.row-check.is-visible {
		opacity: 1;
	}

	.task-grid tbody tr:hover .row-check {
		opacity: 1;
	}

	.row-check:hover:not(:disabled) {
		background: var(--tb-icon-bg);
		border-color: var(--tb-icon-bg-hover);
	}

	.row-check:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}

	.row-check svg {
		width: 0.8rem;
		height: 0.8rem;
		stroke: currentColor;
		fill: none;
		stroke-width: 2.2;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.task-cell .task-content {
		margin-left: 1.55rem;
	}

	.status-wrap {
		position: relative;
		display: flex;
		width: 100%;
		overflow: visible;
	}

	.status-cell.menu-open .status-wrap {
		z-index: 20;
	}

	.status-pill {
		width: 100%;
		height: 1.6rem;
		padding: 0 0.4rem;
		border-radius: 6px;
		border: none;
		font-size: 0.7rem;
		font-weight: 700;
		letter-spacing: 0.01em;
		text-align: center;
		cursor: pointer;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.status-pill:disabled {
		opacity: 0.65;
		cursor: not-allowed;
	}

	.status-todo {
		background: var(--tb-status-todo);
		color: var(--tb-status-todo-text);
	}

	.status-in_progress {
		background: var(--tb-status-progress);
		color: var(--tb-status-progress-text);
	}

	.status-done {
		background: var(--tb-status-done);
		color: var(--tb-status-done-text);
	}

	:global(:root[data-theme='dark']) .status-todo,
	:global(.theme-dark) .status-todo {
		background: var(--tb-status-todo);
		color: var(--tb-status-todo-text);
	}

	:global(:root[data-theme='dark']) .status-in_progress,
	:global(.theme-dark) .status-in_progress {
		background: var(--tb-status-progress);
		color: var(--tb-status-progress-text);
	}

	:global(:root[data-theme='dark']) .status-done,
	:global(.theme-dark) .status-done {
		background: var(--tb-status-done);
		color: var(--tb-status-done-text);
	}

	.status-menu {
		position: absolute;
		top: calc(100% + 0.35rem);
		left: 0;
		z-index: 24;
		min-width: 9.25rem;
		display: grid;
		gap: 0.24rem;
		padding: 0.35rem;
		border-radius: 10px;
		border: 1px solid var(--tb-grid-col-border);
		background: var(--tb-panel-bg);
		box-shadow: 0 8px 24px rgba(0, 0, 0, 0.24);
	}

	.status-option {
		height: 1.72rem;
		border: none;
		border-radius: 8px;
		font-size: 0.74rem;
		font-weight: 700;
		cursor: pointer;
	}

	.status-option.is-active {
		outline: 1px solid color-mix(in srgb, var(--tb-grid-head-bg) 40%, #ffffff 60%);
	}

	.owner-chip {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		max-width: 100%;
	}

	.owner-avatar {
		width: 1.75rem;
		height: 1.75rem;
		border-radius: 999px;
		display: grid;
		place-items: center;
		font-size: 0.67rem;
		font-weight: 700;
		letter-spacing: 0.02em;
		text-transform: uppercase;
		background: linear-gradient(160deg, #4a4d56, #26282f);
		color: var(--tb-avatar-text);
		flex: 0 0 auto;
	}

	.owner-name {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.cell-placeholder {
		color: var(--tb-cell-muted);
	}

	.cell-editor {
		width: 100%;
		height: 1.86rem;
		padding: 0 0.58rem;
		border-radius: 8px;
		border: 1px solid var(--tb-editor-border);
		background: var(--tb-editor-bg);
		color: var(--tb-cell-text);
		font-size: 0.82rem;
	}

	.cell-select-editor {
		cursor: pointer;
		padding-right: 1.8rem;
	}

	.cell-editor:focus-visible {
		outline: none;
		box-shadow: 0 0 0 3px var(--tb-editor-ring);
	}

	.task-board > * {
		animation: board-fade-in 260ms ease both;
	}

	.task-board > *:nth-child(2) {
		animation-delay: 40ms;
	}

	.task-board > *:nth-child(3) {
		animation-delay: 80ms;
	}

	.task-board > *:nth-child(4) {
		animation-delay: 120ms;
	}

	@keyframes board-fade-in {
		from {
			opacity: 0;
			transform: translateY(8px);
		}
		to {
			opacity: 1;
			transform: translateY(0);
		}
	}

	@media (max-width: 640px) {
		.task-board {
			height: auto;
			min-height: 100%;
			grid-template-rows: auto auto auto auto;
		}

		.board-content-slot > .sprint-groups,
		.board-content-slot > .kanban-board,
		.board-content-slot > .support-view,
		.board-content-slot > .board-state {
			height: auto;
			min-height: 320px;
			overflow: visible;
		}

		.sprint-groups {
			overflow: visible;
		}

		.kanban-board {
			height: auto;
			min-height: 400px;
			overflow-x: auto;
			overflow-y: visible;
		}
	}

	@media (max-width: 920px) {
		.task-board {
			padding: 0.72rem;
			gap: 0.65rem;
		}

		.board-header {
			flex-direction: column;
			align-items: flex-start;
		}

		.header-meta {
			justify-content: flex-start;
		}

		.header-main,
		.header-meta {
			width: 100%;
		}

		.task-search {
			flex-direction: column;
			align-items: stretch;
		}

		.task-search-clear {
			width: fit-content;
		}

		.sprint-composer-head {
			flex-direction: column;
		}

		.sprint-composer-actions {
			justify-content: flex-start;
		}

		.quick-edit-controls {
			display: grid;
			grid-template-columns: minmax(0, 1fr);
		}

		.quick-edit-input {
			min-width: 0;
			width: 100%;
		}

		.kanban-board {
			grid-template-columns: repeat(3, minmax(250px, 1fr));
		}

		.support-view {
			grid-template-columns: minmax(0, 1fr);
			overflow: auto;
		}

		.support-composer,
		.support-ticket-board {
			overflow: visible;
		}

		.support-form-grid {
			grid-template-columns: minmax(0, 1fr);
		}

		.support-member-grid {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}

		.support-card-grid {
			grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
		}

		.sprint-group-header {
			flex-direction: column;
			align-items: flex-start;
		}

		.sgh-actions {
			width: 100%;
			justify-content: flex-start;
		}

		.sprint-add-form {
			flex-wrap: wrap;
		}

		.task-detail-section {
			grid-template-columns: minmax(0, 1fr);
		}

		.task-grid {
			min-width: 980px;
		}
	}

	@media (max-width: 640px) {
		.board-header {
			padding: 0.82rem 0.88rem;
		}

		.header-main h2 {
			font-size: 0.96rem;
		}

		.task-search-clear,
		.quick-edit-btn,
		.sprint-composer-trigger,
		.sprint-add-form button,
		.support-create-btn,
		.support-clear-btn {
			width: 100%;
		}

		.quick-subtask-row {
			grid-template-columns: minmax(0, 1fr);
		}

		.quick-subtask-remove {
			width: 100%;
		}

		.sprint-composer-head > :first-child,
		.sprint-composer-head > p {
			width: 100%;
		}

		.sprint-add-form {
			flex-direction: column;
			align-items: stretch;
		}

		.kanban-board {
			grid-template-columns: minmax(0, 1fr);
			overflow: visible;
		}

		.support-composer-actions {
			flex-direction: column;
			align-items: stretch;
		}

		.support-ticket-card {
			aspect-ratio: auto;
			min-height: 180px;
		}
	}
</style>
