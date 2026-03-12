<script lang="ts">
	import { onDestroy, tick } from 'svelte';
	import type { OnlineMember } from '$lib/types/chat';
	import { currentUser } from '$lib/store';
	import { activeContext } from '$lib/stores/jiraContext';
	import { addBoardActivity, type BoardActivityInput } from '$lib/stores/boardActivity';
	import { applyTimelineTaskStatusUpdate } from '$lib/stores/timeline';
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

	export let roomId = '';
	export let canEdit = true;
	export let contextAware = false;
	export let onlineMembers: OnlineMember[] = [];

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';
	const STORAGE_FULL_UPLOAD_MESSAGE =
		'Server storage is temporarily full. Uploads will be available again once older rooms expire.';

	const STATUS_OPTIONS = [
		{ value: 'todo', label: 'To Do' },
		{ value: 'in_progress', label: 'Working on it' },
		{ value: 'done', label: 'Done' }
	] as const;

	type ColumnKey = (typeof STATUS_OPTIONS)[number]['value'];
	type BoardView = 'table' | 'kanban' | 'support';
	type EditableField = 'title' | 'description' | 'assigneeId' | 'sprintName' | 'budget' | 'spent';
	type EditableCellKey = EditableField | 'status';
	type TaskSource = 'personal' | 'room';
	type SupportPriority = 'critical' | 'high' | 'medium' | 'low';
	type DisplayTask = {
		id: string;
		roomId: string;
		title: string;
		description: string;
		status: string;
		budget?: number;
		spent?: number;
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
		budget?: unknown;
		task_budget?: unknown;
		taskBudget?: unknown;
		actual_cost?: unknown;
		actualCost?: unknown;
		spent?: unknown;
		spent_cost?: unknown;
		spentCost?: unknown;
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
	const BOARD_VIEW_OPTIONS: Array<{ value: BoardView; label: string }> = [
		{ value: 'table', label: 'Table' },
		{ value: 'kanban', label: 'Kanban' },
		{ value: 'support', label: 'Support' }
	];
	const KANBAN_COLUMN_ORDER: ColumnKey[] = ['todo', 'in_progress', 'done'];
	const SUPPORT_PRIORITY_OPTIONS: Array<{ value: SupportPriority; label: string }> = [
		{ value: 'critical', label: 'Critical' },
		{ value: 'high', label: 'High' },
		{ value: 'medium', label: 'Medium' },
		{ value: 'low', label: 'Low' }
	];
	const sprintNameCollator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

	let contextTasks: DisplayTask[] = [];
	let contextLoading = false;
	let contextError = '';
	let creatingTask = false;
	let newTaskContent = '';
	let boardView: BoardView = 'table';
	let lastContextKey = '';
	let contextLoadToken = 0;
	let roomBoardError = '';

	let editingTaskId = '';
	let editingField: EditableField | '' = '';
	let editingValue = '';
	let savingCellKey = '';
	let quickEditVisible = false;
	let quickEditorElement: HTMLInputElement | HTMLSelectElement | null = null;
	let statusMenuTaskId = '';
	let hoveredTaskId = '';
	let newTaskInput: HTMLInputElement | null = null;
	let editingTask: DisplayTask | null = null;

	// Sprint add task state
	let sprintAddKey = '';
	let sprintAddContent = '';
	let sprintAddCreating = false;

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

	onDestroy(() => {
		if (boardToastTimer) {
			clearTimeout(boardToastTimer);
			boardToastTimer = null;
		}
	});

	$: sessionUserID = ($currentUser?.id || '').trim();
	$: sessionUsername = ($currentUser?.username || '').trim();
	$: normalizedRoomId = normalizeRoomIDValue(roomId);
	$: boardTitle = contextAware ? $activeContext.name.trim() || 'Workspace Tasks' : 'Room Tasks';
	$: contextKey = `${$activeContext.type}:${$activeContext.id}`;
	$: if (contextAware && contextKey !== lastContextKey) {
		lastContextKey = contextKey;
		void loadContextTasks();
	}

	$: roomTasks = dedupeDisplayTasksById(
		[...$taskStore].map((task): DisplayTask => ({
			id: task.id,
			roomId: task.roomId,
			title: task.title,
			description: task.description,
			status: task.status,
			budget: task.budget,
			spent: task.spent,
			sprintName: task.sprintName || '',
			assigneeId: task.assigneeId,
			statusActorId: task.statusActorId,
			statusActorName: task.statusActorName,
			statusChangedAt: task.statusChangedAt,
			createdAt: task.createdAt,
			updatedAt: task.updatedAt,
			source: 'room'
		}))
	).sort(compareTasksForGrid);
	$: contextGridTasks = dedupeDisplayTasksById([...contextTasks]).sort(compareTasksForGrid);
	$: boardTasks = contextAware ? contextGridTasks : roomTasks;
	$: boardLoading = contextAware ? contextLoading : $taskStoreLoading;
	$: boardError = contextAware ? contextError : roomBoardError || $taskStoreError;
	$: editingTask = boardTasks.find((task) => task.id === editingTaskId) ?? null;
	$: if (!editingTaskId || !editingField) {
		quickEditVisible = false;
	}
	$: ownerOptions = buildOwnerOptions(onlineMembers, boardTasks);
	$: canCreateSprintTask = canEdit && (!contextAware || $activeContext.type === 'room');
	$: hasAnyTasks = boardTasks.length > 0;
	$: boardLastUpdatedAt = boardTasks.reduce(
		(latest, task) => Math.max(latest, Number.isFinite(task.updatedAt) ? task.updatedAt : 0),
		0
	);
	$: sprintTaskGroups = (() => {
		const grouped = new Map<string, SprintTaskGroup>();
		for (const task of boardTasks) {
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

		return [...grouped.values()]
			.map((group) => ({
				...group,
				tasks: [...group.tasks].sort(compareTasksForGrid)
			}))
			.sort((left, right) => {
				if (left.name === 'Backlog' && right.name !== 'Backlog') return 1;
				if (right.name === 'Backlog' && left.name !== 'Backlog') return -1;
				return sprintNameCollator.compare(left.name, right.name);
			});
		})();
	$: kanbanColumns = KANBAN_COLUMN_ORDER.map<KanbanColumn>((columnKey) => ({
		key: columnKey,
		label: statusLabel(columnKey),
		tasks: boardTasks
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
		.sort((left, right) => supportPriorityRank(left.priority) - supportPriorityRank(right.priority) || right.task.updatedAt - left.task.updatedAt);
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
		return ownerOptions.find((option) => normalizeMemberId(option.id) === normalizedOwnerId) ?? null;
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
		const statusDiff = STATUS_ORDER[resolveColumn(left.status)] - STATUS_ORDER[resolveColumn(right.status)];
		if (statusDiff !== 0) {
			return statusDiff;
		}
		const updatedDiff = right.updatedAt - left.updatedAt;
		if (updatedDiff !== 0) {
			return updatedDiff;
		}
		return left.title.localeCompare(right.title);
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
		const trimmed = value.trim();
		if (!trimmed) {
			return 'backlog';
		}
		return trimmed.toLowerCase();
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
		const ticketType = readSupportMetadataValue(task, ['ticket', 'type']).toLowerCase();
		if (ticketType === 'support' || ticketType === 'support_ticket' || ticketType === 'support ticket') {
			return true;
		}
		return task.title.trim().toLowerCase().startsWith('support:');
	}

	function resolveSupportCurrentSprintName(tasks: DisplayTask[]) {
		const activeSprintTask = tasks.find(
			(task) => resolveColumn(task.status) === 'in_progress' && sprintGroupKey(task.sprintName) !== 'backlog'
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
		const concernedIds = splitMetadataList(readSupportMetadataValue(task, ['concerned', 'concerned users']));
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

	function isLikelyUUID(value: string) {
		const trimmed = value.trim();
		if (!trimmed) {
			return false;
		}
		const normalized = trimmed.replace(/_/g, '-');
		return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(normalized);
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
		return {
			id: taskID,
			roomId: normalizeRoomIDValue(normalizedRoomId || $activeContext.id),
			title: toStringValue(source.title) || 'Untitled Task',
			description,
			status: toStringValue(source.status) || 'todo',
			budget,
			spent,
			sprintName: toStringValue(source.sprint_name ?? source.sprintName),
			assigneeId: toStringValue(source.assignee_id ?? source.assigneeId),
			statusActorId: toStringValue(source.status_actor_id ?? source.statusActorId) || undefined,
			statusActorName: toStringValue(source.status_actor_name ?? source.statusActorName) || undefined,
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
						statusActorName:
							statusMeta?.statusActorName || sessionUsername || task.statusActorName,
						statusChangedAt: statusMeta?.statusChangedAt || Date.now(),
						updatedAt: statusMeta?.updatedAt || Date.now()
					} satisfies DisplayTask;
				});
				const nextTaskForSocket = contextTasks.find((task) => task.id === taskID);
				if (normalizedWorkspaceRoomID && nextTaskForSocket) {
					publishRoomBoardActivity(normalizedWorkspaceRoomID, {
						type: columnKey === 'done' ? 'task_completed' : 'task_moved',
						title:
							columnKey === 'done'
								? `Completed ${targetTask.title}`
								: `Moved ${targetTask.title}`,
						subtitle: `${statusLabel(previousColumn)} → ${statusLabel(columnKey)}`,
						actor:
							nextTaskForSocket.statusActorName ||
							nextTaskForSocket.statusActorId ||
							'Unknown'
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

	async function startEditing(task: DisplayTask, field: EditableField) {
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
		quickEditVisible = true;
		await tick();
		quickEditorElement?.focus();
		if (quickEditorElement instanceof HTMLInputElement) {
			quickEditorElement.select();
		}
	}

	function closeQuickEditor() {
		quickEditVisible = false;
		cancelEditing();
	}

	async function saveQuickEditor() {
		if (!editingTask || !editingField) {
			return;
		}
		await commitEditing(editingTask, editingField);
	}

	function cancelEditing() {
		editingTaskId = '';
		editingField = '';
		editingValue = '';
	}

	function fieldLabel(field: EditableField) {
		if (field === 'title') return 'task name';
		if (field === 'description') return 'description';
		if (field === 'assigneeId') return 'owner';
		if (field === 'budget') return 'budget';
		if (field === 'spent') return 'spent';
		return 'sprint';
	}

	async function updateContextRoomTaskField(task: DisplayTask, field: EditableField, nextValue: string) {
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

	function updateContextPersonalTaskField(task: DisplayTask, field: EditableField, nextValue: string) {
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
		if ((field === 'budget' || field === 'spent') && nextValue && isNaN(Number(nextValue.replace(/[$,]/g, '')))) {
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
			void commitEditing(task, 'assigneeId');
		}
	}

	function toggleStatusMenu(event: MouseEvent, task: DisplayTask) {
		event.stopPropagation();
		cancelEditing();
		if (!canEditTaskStatus(task)) {
			return;
		}
		statusMenuTaskId = statusMenuTaskId === task.id ? '' : task.id;
	}

	async function applyStatus(task: DisplayTask, nextStatus: ColumnKey) {
		if (!canEditTaskStatus(task)) {
			return;
		}
		if (resolveColumn(task.status) === nextStatus) {
			statusMenuTaskId = '';
			return;
		}

		const cellKey = makeCellKey(task.id, 'status');
		savingCellKey = cellKey;
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
			if (savingCellKey === cellKey) {
				savingCellKey = '';
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
		const hasSpent =
			typeof spent === 'number' && Number.isFinite(spent) && spent >= 0;
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
		newTaskContent = `Follow up: ${task.title}`;
		void tick().then(() => {
			newTaskInput?.focus();
			newTaskInput?.setSelectionRange(newTaskContent.length, newTaskContent.length);
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
		const linkedTaskTitleList = linkedTaskIds.map((linkedTaskId) => supportLinkedTaskTitle(linkedTaskId));
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
						status: 'todo'
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

	async function handleCreateRoomTaskInSprint(content: string, sprintName: string) {
		if (sprintAddCreating || !content.trim()) return;
		const targetRoomId = normalizeRoomIDValue(
			contextAware ? ($activeContext.type === 'room' ? $activeContext.id : '') : normalizedRoomId
		);
		if (!targetRoomId) {
			setBoardError('Invalid room id');
			return;
		}
		sprintAddCreating = true;
		clearBoardError();
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(targetRoomId)}/tasks`,
				{
					method: 'POST',
					headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
					credentials: 'include',
					body: JSON.stringify({ content: content.trim(), sprint_name: sprintName })
				}
			);
			if (!response.ok) throw new Error(await parseErrorMessage(response));
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
					subtitle: sprintName.trim() ? `Added to ${sprintName.trim()}` : 'Created task',
					actor: sessionUsername || sessionUserID || 'Unknown'
				});
			} else {
				const created = upsertTaskStoreEntry(payload, targetRoomId);
				if (!created) throw new Error('Invalid task response');
				sendSocketPayload(buildTaskSocketPayload('task_create', targetRoomId, created));
				publishRoomBoardActivity(targetRoomId, {
					type: 'task_added',
					title: `Added ${created.title}`,
					subtitle: sprintName.trim() ? `Added to ${sprintName.trim()}` : 'Created task',
					actor: sessionUsername || sessionUserID || 'Unknown'
				});
			}
			sprintAddContent = '';
			sprintAddKey = '';
		} catch (error) {
			setBoardError(error instanceof Error ? error.message : 'Failed to create task');
		} finally {
			sprintAddCreating = false;
		}
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
	<header class="board-header">
		<div class="header-main">
			<h2>{boardTitle}</h2>
			<p>{boardTasks.length} tasks</p>
		</div>
		<div class="header-meta">
			<span>Latest update {formatCellTime(boardLastUpdatedAt)}</span>
			<nav class="board-view-switch" aria-label="Task board view options">
				{#each BOARD_VIEW_OPTIONS as option (option.value)}
					<button
						type="button"
						class="view-option"
						class:is-active={boardView === option.value}
						on:click={() => {
							boardView = option.value;
							statusMenuTaskId = '';
						}}
					>
						{option.label}
					</button>
				{/each}
			</nav>
		</div>
	</header>

	<form
		class="new-task-form"
		on:submit|preventDefault={() => {
			if (contextAware) {
				void handleCreateTask(newTaskContent);
				return;
			}
			void handleCreateRoomTask(newTaskContent);
		}}
	>
		<input
			bind:this={newTaskInput}
			type="text"
			bind:value={newTaskContent}
			placeholder="Add a task..."
			autocomplete="off"
			disabled={creatingTask}
		/>
		<button type="submit" disabled={creatingTask || !newTaskContent.trim() || !canEdit}>
			{creatingTask ? 'Adding...' : 'Add task'}
		</button>
	</form>


t
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
							placeholder="Owner"
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
					disabled={isSaving(editingTask.id, editingField)}
				>
					Save
				</button>
				<button type="button" class="quick-edit-btn" on:click={closeQuickEditor}>Cancel</button>
			</div>
		</section>
	{/if}

		<div class="board-content-slot">
			{#if boardLoading}
				<div class="board-state">Loading tasks...</div>
			{:else if boardError}
				<div class="board-state error">Unable to load tasks: {boardError}</div>
			{:else if !hasAnyTasks}
				<div class="board-state">No tasks yet. Add one to start planning.</div>
				{:else}
					{#if boardView === 'support'}
						<div class="support-view" aria-label="Support ticket board">
							<section class="support-composer">
								<header class="support-composer-head">
									<div>
										<h3>Support Tickets</h3>
										<p>Current sprint: {supportCurrentSprintName}</p>
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
										<select bind:value={supportTicketPriority} disabled={!canEdit || supportTicketCreating}>
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
											<h4>Choose task(s)</h4>
											<span>{selectedSupportTaskIds.length} selected</span>
										</div>
										{#if supportSourceTasksForSprint.length === 0}
											<div class="support-empty-inline">No tasks in {supportCurrentSprintName}.</div>
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
										disabled={!canEdit || supportTicketCreating || !supportTicketTitle.trim() || selectedSupportTaskIds.length === 0}
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
									<h3>Tickets in {supportCurrentSprintName}</h3>
									<span>{supportTicketCards.length}</span>
								</header>
								{#if supportTicketCards.length === 0}
									<div class="support-empty">No support tickets yet for this sprint.</div>
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
													{supportCard.linkedTaskIds.length} linked task{supportCard.linkedTaskIds.length === 1 ? '' : 's'}
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
																<span class="support-avatar support-avatar-more" style="--avatar-index:4;">
																	+{supportCard.concernedIds.length - 4}
																</span>
															{/if}
														{/if}
													</div>
													<span class="support-sprint-chip">{supportCard.task.sprintName.trim() || 'Backlog'}</span>
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
										<div class="kanban-empty">No tasks</div>
									{:else}
										{#each column.tasks as task (task.id)}
											<article class="kanban-card" role="listitem">
												<div class="kanban-card-top">
													<button
														type="button"
														class="kanban-card-title"
														on:click|stopPropagation={() => void startEditing(task, 'title')}
														on:dblclick|stopPropagation={() => void startEditing(task, 'title')}
													>
														{task.title}
													</button>
													<button
														type="button"
														class="kanban-mini-btn"
														on:click|stopPropagation={() => queueFollowUp(task)}
														disabled={!canEdit}
														title="Follow-up"
													>
														+
													</button>
												</div>

												<div class="kanban-status-row">
													<div class="status-wrap">
														<button
															type="button"
															class={`status-pill status-${resolveColumn(task.status)}`}
															on:click|stopPropagation={(event) => toggleStatusMenu(event, task)}
															disabled={!canEditTaskStatus(task) || isSaving(task.id, 'status')}
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
																		on:click|stopPropagation={() => void applyStatus(task, option.value)}
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
														<span class="meta-label">Owner</span>
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
														<span class="meta-label">Spent</span>
														<span class="meta-value budget-val">{formatSpentCell(task.spent, task.budget)}</span>
													</button>
												</div>

												<footer class="kanban-card-footer">
													<span>{task.sprintName.trim() || 'Backlog'}</span>
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
					<div class="sprint-groups" role="list" aria-label="Sprint task groups">
				{#each sprintTaskGroups as sprintGroup (sprintGroup.key)}
					{@const selCount = sprintSelectedCount(sprintGroup)}
					{@const isEditMode = sprintEditKeys.has(sprintGroup.key)}
					<section class="sprint-group" role="listitem" aria-label={`${sprintGroup.name} task grid`}>
					<header class="sprint-group-header">
						<div class="sgh-left">
							<h3>{sprintGroup.name}</h3>
							<p>{sprintGroup.tasks.length} tasks · {formatCellTime(sprintGroup.lastUpdatedAt)}</p>
						</div>
						<div class="sgh-actions">
							{#if isEditMode}
								<button
									type="button"
									class="sgh-btn sgh-delete"
									on:click={() => void deleteSelectedInSprint(sprintGroup)}
									disabled={!canEdit || deletingTaskIds.length > 0}
								>
									Delete selected{selCount > 0 ? ` (${selCount})` : ''}
								</button>
								{#if canCreateSprintTask}
									<button
										type="button"
										class="sgh-btn sgh-add"
										on:click={() => {
											sprintAddKey = sprintAddKey === sprintGroup.key ? '' : sprintGroup.key;
											sprintAddContent = '';
										}}
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
						<table class="task-grid" role="grid" aria-label={`${sprintGroup.name} tasks`}>
							<thead>
								<tr>
									{#if isEditMode}
									<th scope="col" class="th-check">
										<input
											type="checkbox"
											checked={isSprintAllSelected(sprintGroup)}
											indeterminate={selCount > 0 && !isSprintAllSelected(sprintGroup)}
											on:change={() => toggleSprintSelection(sprintGroup)}
											aria-label="Select all in sprint"
										/>
									</th>
									{/if}
									<th scope="col">Task</th>
									<th scope="col">Status</th>
									<th scope="col">Owner</th>
									<th scope="col">Budget</th>
									<th scope="col">Spent</th>
									<th scope="col">Updated</th>
								</tr>
							</thead>
							<tbody>
								{#each sprintGroup.tasks as task (task.id)}
									<tr
										class:row-done={resolveColumn(task.status) === 'done'}
										class:row-selected={isEditMode && isTaskSelected(task.id)}
										on:mouseenter={() => { hoveredTaskId = task.id; }}
										on:mouseleave={() => { hoveredTaskId = ''; }}
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
										<td
											class="cell task-cell"
											on:click={() => void startEditing(task, 'title')}
											on:dblclick|stopPropagation={() => void startEditing(task, 'title')}
										>
											<button
												type="button"
												class="row-check"
												class:is-visible={hoveredTaskId === task.id || resolveColumn(task.status) === 'done'}
												on:click|stopPropagation={() => void toggleDone(task)}
												disabled={!canEdit}
												aria-label={resolveColumn(task.status) === 'done' ? 'Mark as to do' : 'Mark as done'}
											>
												<svg viewBox="0 0 24 24" aria-hidden="true">
													<path d="m6 12 4 4 8-8"></path>
												</svg>
											</button>
											<div class="task-content">
												<button
													type="button"
													class="cell-trigger task-title-trigger"
													on:click|stopPropagation={() => void startEditing(task, 'title')}
													on:dblclick|stopPropagation={() => void startEditing(task, 'title')}
												>
													{task.title}
												</button>
												<div class="task-actions">
													<button type="button" on:click|stopPropagation={() => queueFollowUp(task)} disabled={!canEdit} title="Follow-up">+</button>
												</div>
											</div>
										</td>

										<!-- Status -->
										<td class="cell status-cell">
											<div class="status-wrap">
												<button
													type="button"
													class={`status-pill status-${resolveColumn(task.status)}`}
													on:click|stopPropagation={(e) => toggleStatusMenu(e, task)}
													disabled={!canEditTaskStatus(task) || isSaving(task.id, 'status')}
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
																on:click|stopPropagation={() => void applyStatus(task, option.value)}
															>{option.label}</button>
														{/each}
													</div>
												{/if}
											</div>
										</td>

										<!-- Owner -->
										<td
											class="cell owner-cell"
											on:click={() => void startEditing(task, 'assigneeId')}
											on:dblclick|stopPropagation={() => void startEditing(task, 'assigneeId')}
										>
											<button
												type="button"
												class="cell-trigger owner-trigger"
												on:click|stopPropagation={() => void startEditing(task, 'assigneeId')}
												on:dblclick|stopPropagation={() => void startEditing(task, 'assigneeId')}
											>
												<div class="owner-chip">
													<span class="owner-avatar" style={`--owner-hue:${ownerHue(task)};`}>{initials(ownerLabel(task))}</span>
													<span class="owner-name">{ownerLabel(task)}</span>
												</div>
											</button>
										</td>

										<!-- Budget (editable) -->
										<td
											class="cell budget-cell"
											on:click={() => void startEditing(task, 'budget')}
											on:dblclick|stopPropagation={() => void startEditing(task, 'budget')}
										>
											<button
												type="button"
												class="cell-trigger budget-trigger"
												on:click|stopPropagation={() => void startEditing(task, 'budget')}
												on:dblclick|stopPropagation={() => void startEditing(task, 'budget')}
												title="Click to edit budget"
											>
											<span class="budget-val">{formatBudgetCell(task.budget)}</span>
										</button>
									</td>

										<!-- Spent (editable) -->
										<td
											class="cell spent-cell"
											on:click={() => void startEditing(task, 'spent')}
											on:dblclick|stopPropagation={() => void startEditing(task, 'spent')}
										>
											<button
												type="button"
												class="cell-trigger spent-trigger"
												on:click|stopPropagation={() => void startEditing(task, 'spent')}
												on:dblclick|stopPropagation={() => void startEditing(task, 'spent')}
												title="Click to edit spent amount"
											>
												<span class="budget-val">{formatSpentCell(task.spent, task.budget)}</span>
											</button>
										</td>

										<td class="cell updated-cell">{formatCellTime(task.updatedAt)}</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>

					<!-- Per-sprint add task form -->
					{#if sprintAddKey === sprintGroup.key}
						<form
							class="sprint-add-form"
							on:submit|preventDefault={() => void handleCreateRoomTaskInSprint(sprintAddContent, sprintGroup.name)}
						>
							<!-- svelte-ignore a11y-autofocus -->
							<input
								type="text"
								bind:value={sprintAddContent}
								placeholder="Task name…"
								autocomplete="off"
								disabled={sprintAddCreating}
								autofocus
							/>
							<button type="submit" disabled={sprintAddCreating || !sprintAddContent.trim()}>
								{sprintAddCreating ? 'Adding…' : 'Add'}
							</button>
							<button type="button" on:click={() => { sprintAddKey = ''; sprintAddContent = ''; }}>Cancel</button>
						</form>
					{/if}
					</section>
				{/each}
					</div>
				{/if}
			{/if}
		</div>
	</section>

<style>
	:global(:root) {
		--workspace-taskboard-bg: #f2f4f8;
		--workspace-taskboard-column-border: #d9dee8;
		--workspace-taskboard-item-bg: #ffffff;
		--workspace-taskboard-item-border: #dfe4ed;
		--workspace-taskboard-item-text: #202636;
		--workspace-taskboard-meta: #5d6678;

		--tb-panel-bg: #ffffff;
		--tb-panel-border: #d7dde8;
		--tb-form-bg: #ffffff;
		--tb-form-border: #d4dae5;
		--tb-input-bg: #ffffff;
		--tb-input-border: #c7ceda;
		--tb-input-text: #172134;
		--tb-input-placeholder: #6b7488;
		--tb-btn-bg: #ecf1fa;
		--tb-btn-border: #c8d2e2;
		--tb-btn-text: #22314d;
		--tb-state-bg: #ffffff;
		--tb-state-border: #d4dbe8;
		--tb-state-text: #4d5a74;
		--tb-error-text: #b42318;

		--tb-grid-bg: #ffffff;
		--tb-grid-border: #d3dae7;
		--tb-grid-head-bg: #1e2430;
		--tb-grid-head-text: #f3f6ff;
		--tb-grid-head-muted: #ccd6eb;
		--tb-grid-row-border: rgba(33, 45, 68, 0.12);
		--tb-grid-col-border: rgba(33, 45, 68, 0.1);
		--tb-grid-row-hover: rgba(30, 41, 59, 0.06);
		--tb-grid-row-done: rgba(14, 159, 110, 0.06);
		--tb-cell-text: #1f293d;
		--tb-cell-muted: #6a7388;
		--tb-editor-bg: #ffffff;
		--tb-editor-border: #9aa9c3;
		--tb-editor-ring: rgba(37, 99, 235, 0.16);
		--tb-avatar-text: #ffffff;
		--tb-icon-bg: rgba(38, 52, 84, 0.08);
		--tb-icon-bg-hover: rgba(38, 52, 84, 0.2);
		--tb-icon-text: #2c3c60;
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--workspace-taskboard-bg: #18181b;
		--workspace-taskboard-column-border: rgba(255, 255, 255, 0.11);
		--workspace-taskboard-item-bg: #1f2025;
		--workspace-taskboard-item-border: rgba(255, 255, 255, 0.11);
		--workspace-taskboard-item-text: #f1f4fb;
		--workspace-taskboard-meta: #a2a9b8;

		--tb-panel-bg: #1f2025;
		--tb-panel-border: rgba(255, 255, 255, 0.12);
		--tb-form-bg: #1e2025;
		--tb-form-border: rgba(255, 255, 255, 0.12);
		--tb-input-bg: #17181c;
		--tb-input-border: rgba(255, 255, 255, 0.14);
		--tb-input-text: #f2f5fb;
		--tb-input-placeholder: #8e97ab;
		--tb-btn-bg: #2a2d35;
		--tb-btn-border: rgba(255, 255, 255, 0.17);
		--tb-btn-text: #eef2ff;
		--tb-state-bg: #1d1e23;
		--tb-state-border: rgba(255, 255, 255, 0.14);
		--tb-state-text: #b9c0d0;
		--tb-error-text: #ffb4b4;

		--tb-grid-bg: #1a1b20;
		--tb-grid-border: rgba(255, 255, 255, 0.12);
		--tb-grid-head-bg: #14171d;
		--tb-grid-head-text: #f5f7ff;
		--tb-grid-head-muted: #aeb8cc;
		--tb-grid-row-border: rgba(255, 255, 255, 0.1);
		--tb-grid-col-border: rgba(255, 255, 255, 0.08);
		--tb-grid-row-hover: rgba(255, 255, 255, 0.06);
		--tb-grid-row-done: rgba(12, 97, 70, 0.25);
		--tb-cell-text: #edf2ff;
		--tb-cell-muted: #a8b1c4;
		--tb-editor-bg: #101217;
		--tb-editor-border: rgba(140, 168, 222, 0.56);
		--tb-editor-ring: rgba(111, 145, 214, 0.24);
		--tb-avatar-text: #ffffff;
		--tb-icon-bg: rgba(208, 219, 255, 0.14);
		--tb-icon-bg-hover: rgba(208, 219, 255, 0.26);
		--tb-icon-text: #dce6ff;
	}

	.task-board {
		height: 100%;
		min-height: 0;
		width: 100%;
		padding: 1rem;
		display: grid;
		grid-template-rows: auto auto auto minmax(0, 1fr);
		gap: 0.8rem;
		background: var(--workspace-taskboard-bg);
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

	.board-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.8rem;
		padding: 0.85rem 1rem;
		border-radius: 14px;
		background: var(--tb-panel-bg);
		border: 1px solid var(--tb-panel-border);
	}

	.header-main {
		display: grid;
		gap: 0.15rem;
	}

	.header-main h2 {
		margin: 0;
		font-size: 1.03rem;
		line-height: 1.2;
		font-weight: 700;
		color: var(--workspace-taskboard-item-text);
	}

	.header-main p {
		margin: 0;
		font-size: 0.82rem;
		color: var(--workspace-taskboard-meta);
	}

	.header-meta {
		display: flex;
		flex-wrap: wrap;
		justify-content: flex-end;
		gap: 0.4rem;
		align-items: center;
	}

	.header-meta span {
		font-size: 0.74rem;
		font-weight: 600;
		padding: 0.22rem 0.55rem;
		border-radius: 999px;
		color: var(--workspace-taskboard-meta);
		border: 1px solid var(--tb-panel-border);
		background: color-mix(in srgb, var(--tb-panel-bg) 82%, #ffffff 18%);
	}

	.board-view-switch {
		display: inline-flex;
		align-items: center;
		gap: 0.2rem;
		padding: 0.2rem;
		border-radius: 999px;
		border: 1px solid var(--tb-panel-border);
		background: color-mix(in srgb, var(--tb-panel-bg) 88%, #ffffff 12%);
	}

	.view-option {
		height: 1.75rem;
		padding: 0 0.72rem;
		border: 1px solid transparent;
		border-radius: 999px;
		background: transparent;
		color: var(--tb-cell-muted);
		font-size: 0.74rem;
		font-weight: 700;
		letter-spacing: 0.01em;
		cursor: pointer;
		transition:
			background 0.18s ease,
			border-color 0.18s ease,
			color 0.18s ease;
	}

	.view-option:hover {
		background: color-mix(in srgb, var(--tb-btn-bg) 68%, transparent);
		color: var(--tb-cell-text);
	}

	.view-option.is-active {
		background: var(--tb-btn-bg);
		border-color: var(--tb-btn-border);
		color: var(--tb-btn-text);
	}

	.new-task-form {
		display: flex;
		align-items: center;
		gap: 0.58rem;
		padding: 0.65rem;
		border-radius: 14px;
		background: var(--tb-form-bg);
		border: 1px solid var(--tb-form-border);
	}

	.new-task-form input {
		flex: 1;
		min-width: 0;
		height: 2.2rem;
		border-radius: 10px;
		border: 1px solid var(--tb-input-border);
		background: var(--tb-input-bg);
		color: var(--tb-input-text);
		padding: 0 0.75rem;
		font-size: 0.88rem;
	}

	.new-task-form input::placeholder {
		color: var(--tb-input-placeholder);
	}

	.new-task-form button {
		height: 2.2rem;
		padding: 0 0.85rem;
		border-radius: 10px;
		border: 1px solid var(--tb-btn-border);
		background: var(--tb-btn-bg);
		color: var(--tb-btn-text);
		font-size: 0.82rem;
		font-weight: 650;
		cursor: pointer;
		transition:
			background 0.2s ease,
			border-color 0.2s ease;
	}

	.new-task-form button:hover:not(:disabled) {
		background: color-mix(in srgb, var(--tb-btn-bg) 72%, #ffffff 28%);
	}

	.new-task-form button:disabled {
		opacity: 0.58;
		cursor: not-allowed;
	}

	.quick-edit-panel {
		grid-row: 3;
		display: grid;
		gap: 0.55rem;
		padding: 0.72rem;
		border-radius: 12px;
		border: 1px solid var(--tb-panel-border);
		background: var(--tb-panel-bg);
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
		border-radius: 14px;
		border: 1px solid var(--tb-grid-border);
		background: var(--tb-panel-bg);
	}

	.kanban-column-head {
		padding: 0.6rem 0.7rem;
		border-bottom: 1px solid var(--tb-grid-col-border);
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
		border-radius: 12px;
		border: 1px solid var(--tb-grid-col-border);
		background: var(--tb-grid-bg);
		box-shadow: 0 1px 2px rgba(0, 0, 0, 0.06);
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
			border-radius: 14px;
			background: var(--tb-panel-bg);
			padding: 0.72rem;
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
			transition: border-color 0.16s ease, background 0.16s ease;
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
			border-radius: 12px;
			background: var(--tb-grid-bg);
			padding: 0.58rem;
			aspect-ratio: 1 / 1;
			min-height: 170px;
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
			background: rgba(181, 35, 56, 0.16);
			color: #b52338;
		}

		.priority-high {
			background: rgba(194, 110, 24, 0.18);
			color: #b96618;
		}

		.priority-medium {
			background: rgba(46, 97, 178, 0.16);
			color: #2e61b2;
		}

		.priority-low {
			background: rgba(23, 132, 88, 0.16);
			color: #178458;
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
			background: hsl(var(--owner-hue) 62% 47%);
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
	}

	.sprint-group-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		padding: 0 0.2rem;
	}

	.sgh-left { min-width: 0; }

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
		transition: background 0.15s, border-color 0.15s;
	}

	.sgh-btn:disabled { opacity: 0.5; cursor: not-allowed; }

	.sgh-edit {
		color: var(--tb-btn-text);
		background: var(--tb-btn-bg);
		border-color: var(--tb-btn-border);
	}
	.sgh-edit:hover:not(:disabled) {
		background: color-mix(in srgb, var(--tb-btn-bg) 72%, #ffffff 28%);
	}

	.sgh-done {
		color: #1a73e8;
		background: rgba(26, 115, 232, 0.1);
		border-color: rgba(26, 115, 232, 0.35);
		font-weight: 700;
	}
	.sgh-done:hover:not(:disabled) {
		background: rgba(26, 115, 232, 0.18);
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
	.sprint-add-form button:disabled { opacity: 0.5; cursor: not-allowed; }
	.sprint-add-form button[type='button'] {
		background: transparent;
		border-color: var(--tb-grid-border);
		color: var(--tb-cell-muted);
	}

	.grid-shell {
		min-height: 0;
		overflow: auto;
		border-radius: 14px;
		border: 1px solid var(--tb-grid-border);
		background: var(--tb-grid-bg);
		scrollbar-width: thin;
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
		background: var(--tb-grid-head-bg);
		white-space: nowrap;
		border-bottom: 1px solid var(--tb-grid-row-border);
	}

	.task-grid thead th:not(:last-child),
	.task-grid tbody td:not(:last-child) {
		border-right: 1px solid var(--tb-grid-col-border);
	}

	.task-grid tbody tr {
		transition: background 0.18s ease;
	}

	.task-grid tbody tr:hover {
		background: var(--tb-grid-row-hover);
	}

	.task-grid tbody tr.row-done {
		background: var(--tb-grid-row-done);
	}

	.task-grid tbody tr.row-selected {
		background: color-mix(in srgb, var(--tb-grid-head-bg) 8%, transparent);
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
		height: 2.7rem;
		padding: 0.35rem 0.65rem;
		font-size: 0.84rem;
		color: var(--tb-cell-text);
		vertical-align: middle;
		border-bottom: 1px solid var(--tb-grid-row-border);
	}

	.cell {
		position: relative;
	}

	.task-cell {
		width: 30%;
		padding-left: 0.55rem;
		cursor: pointer;
	}
	.task-cell:not(.is-editing):hover {
		background: color-mix(in srgb, var(--tb-grid-head-bg) 5%, transparent);
	}

	.status-cell {
		width: 18%;
	}

	.owner-cell {
		width: 18%;
		cursor: pointer;
	}
	.owner-cell:not(.is-editing):hover {
		background: color-mix(in srgb, var(--tb-grid-head-bg) 5%, transparent);
	}

	.budget-cell {
		width: 12%;
		font-size: 0.8rem;
		font-weight: 600;
		white-space: nowrap;
		cursor: pointer;
	}
	.budget-cell:not(.is-editing):hover {
		background: color-mix(in srgb, var(--tb-grid-head-bg) 5%, transparent);
	}

	.spent-cell {
		width: 12%;
		font-size: 0.8rem;
		font-weight: 600;
		white-space: nowrap;
		cursor: pointer;
	}
	.spent-cell:not(.is-editing):hover {
		background: color-mix(in srgb, var(--tb-grid-head-bg) 5%, transparent);
	}

	.updated-cell {
		width: 10%;
		font-size: 0.78rem;
		color: var(--tb-cell-muted);
		white-space: nowrap;
	}

	.task-content {
		display: flex;
		align-items: center;
		gap: 0.45rem;
		min-width: 0;
	}

	.task-title {
		display: block;
		max-width: 150px;
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
		max-width: 150px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-weight: 600;
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
		display: inline-flex;
	}

	.status-pill {
		min-width: 7.25rem;
		height: 1.72rem;
		padding: 0 0.75rem;
		border-radius: 999px;
		border: none;
		font-size: 0.73rem;
		font-weight: 700;
		letter-spacing: 0.01em;
		text-align: center;
		cursor: pointer;
	}

	.status-pill:disabled {
		opacity: 0.65;
		cursor: not-allowed;
	}

	.status-todo {
		background: #5a6478;
		color: #f7f9ff;
	}

	.status-in_progress {
		background: #f59e0b;
		color: #241300;
	}

	.status-done {
		background: #12a06f;
		color: #052417;
	}

	:global(:root[data-theme='dark']) .status-todo,
	:global(.theme-dark) .status-todo {
		background: #6a748c;
		color: #f4f7ff;
	}

	:global(:root[data-theme='dark']) .status-in_progress,
	:global(.theme-dark) .status-in_progress {
		background: #f6b03f;
		color: #2c1800;
	}

	:global(:root[data-theme='dark']) .status-done,
	:global(.theme-dark) .status-done {
		background: #33b784;
		color: #0a2f1f;
	}

	.status-menu {
		position: absolute;
		top: calc(100% + 0.35rem);
		left: 0;
		z-index: 8;
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
		background: hsl(var(--owner-hue) 64% 48%);
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

		.board-view-switch {
			width: 100%;
			justify-content: flex-start;
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

			.task-grid {
				min-width: 980px;
			}
		}
</style>
