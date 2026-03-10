<script lang="ts">
	import { currentUser } from '$lib/store';
	import { activeContext } from '$lib/stores/jiraContext';
	import { addBoardActivity } from '$lib/stores/boardActivity';
	import { applyTimelineTaskStatusUpdate } from '$lib/stores/timeline';
	import {
		moveTaskOptimistic,
		taskStore,
		taskStoreError,
		taskStoreLoading,
		upsertTaskStoreEntry,
		type Task
	} from '$lib/stores/tasks';
	import { normalizeRoomIDValue, toStringValue } from '$lib/utils/chat/core';
	import { sendSocketPayload } from '$lib/ws';
	import { buildTaskSocketPayload } from '$lib/ws/client';

	export let roomId = '';
	export let canEdit = true;
	export let contextAware = false;

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';

	const COLUMNS = [
		{ key: 'todo', label: 'To Do' },
		{ key: 'in_progress', label: 'In Progress' },
		{ key: 'done', label: 'Done' }
	] as const;

	type ColumnKey = (typeof COLUMNS)[number]['key'];
	type ContextTask = {
		id: string;
		title: string;
		description: string;
		status: string;
		assigneeId: string;
		createdAt: number;
		updatedAt: number;
		source: 'personal' | 'room';
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
		assignee_id?: unknown;
		created_at?: unknown;
		updated_at?: unknown;
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

	let draggedTaskId = '';
	let activeDropColumn: ColumnKey | '' = '';
	let contextDraggedTaskId = '';
	let contextActiveDropColumn: ColumnKey | '' = '';
	let contextTasks: ContextTask[] = [];
	let contextLoading = false;
	let contextError = '';
	let creatingTask = false;
	let newTaskContent = '';
	let lastContextKey = '';
	let contextLoadToken = 0;
	let roomBoardError = '';

	$: sessionUserID = ($currentUser?.id || '').trim();
	$: sessionUsername = ($currentUser?.username || '').trim();
	$: normalizedRoomId = normalizeRoomIDValue(roomId);
	$: todoTasks = $taskStore.filter((task) => resolveColumn(task.status) === 'todo');
	$: inProgressTasks = $taskStore.filter((task) => resolveColumn(task.status) === 'in_progress');
	$: doneTasks = $taskStore.filter((task) => resolveColumn(task.status) === 'done');
	$: hasAnyTasks = $taskStore.length > 0;
	$: contextTodoTasks = contextTasks.filter((task) => resolveColumn(task.status) === 'todo');
	$: contextInProgressTasks = contextTasks.filter(
		(task) => resolveColumn(task.status) === 'in_progress'
	);
	$: contextDoneTasks = contextTasks.filter((task) => resolveColumn(task.status) === 'done');
	$: hasAnyContextTasks = contextTasks.length > 0;
	$: boardTitle = contextAware ? $activeContext.name.trim() || 'Workspace Tasks' : 'Room Tasks';
	$: contextKey = `${$activeContext.type}:${$activeContext.id}`;
	$: if (contextAware && contextKey !== lastContextKey) {
		lastContextKey = contextKey;
		void loadContextTasks();
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

	function statusLabel(column: ColumnKey) {
		if (column === 'in_progress') return 'In Progress';
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

	function getColumnTasks(columnKey: ColumnKey): Task[] {
		if (columnKey === 'in_progress') {
			return inProgressTasks;
		}
		if (columnKey === 'done') {
			return doneTasks;
		}
		return todoTasks;
	}

	function getContextColumnTasks(columnKey: ColumnKey): ContextTask[] {
		if (columnKey === 'in_progress') {
			return contextInProgressTasks;
		}
		if (columnKey === 'done') {
			return contextDoneTasks;
		}
		return contextTodoTasks;
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

	function normalizePersonalItem(raw: unknown): ContextTask | null {
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
			title: displayTitle,
			description: description || (content !== displayTitle ? content : ''),
			status: toStringValue(source.status) || 'pending',
			assigneeId: '',
			createdAt,
			updatedAt: parseTimestamp(source.updated_at) || createdAt,
			source: 'personal'
		};
	}

	function normalizeRoomTask(raw: unknown): ContextTask | null {
		if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
			return null;
		}
		const source = raw as RoomTaskResponse;
		const taskID = toStringValue(source.id);
		if (!taskID) {
			return null;
		}
		const createdAt = parseTimestamp(source.created_at);
		return {
			id: taskID,
			title: toStringValue(source.title) || 'Untitled Task',
			description: toStringValue(source.description),
			status: toStringValue(source.status) || 'todo',
			assigneeId: toStringValue(source.assignee_id),
			createdAt,
			updatedAt: parseTimestamp(source.updated_at) || createdAt,
			source: 'room'
		};
	}

	async function parseErrorMessage(response: Response) {
		const payload = (await response.json().catch(() => null)) as {
			error?: string;
		} | null;
		return payload?.error?.trim() || `HTTP ${response.status}`;
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
			let normalizeRow: (raw: unknown) => ContextTask | null = normalizeRoomTask;
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
				.filter((record): record is ContextTask => Boolean(record))
				.sort((left, right) => right.updatedAt - left.updatedAt);
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

	async function persistContextTaskStatus(taskID: string, columnKey: ColumnKey) {
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
			return;
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
		if (resolveColumn(previousStatus) === columnKey) {
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
			await persistContextTaskStatus(taskID, columnKey);
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
			}
			newTaskContent = '';
		} catch (error) {
			contextError = error instanceof Error ? error.message : 'Failed to create task';
		} finally {
			creatingTask = false;
		}
	}

	function startContextDragging(event: DragEvent, taskID: string) {
		if (!canEdit || !contextAware) {
			return;
		}
		contextDraggedTaskId = taskID;
		if (event.dataTransfer) {
			event.dataTransfer.effectAllowed = 'move';
			event.dataTransfer.setData('application/x-tora-context-task-id', taskID);
			event.dataTransfer.setData('text/plain', taskID);
		}
	}

	function stopContextDragging() {
		contextDraggedTaskId = '';
		contextActiveDropColumn = '';
	}

	function onContextColumnDragOver(event: DragEvent, columnKey: ColumnKey) {
		if (!canEdit || !contextAware) {
			return;
		}
		event.preventDefault();
		contextActiveDropColumn = columnKey;
		if (event.dataTransfer) {
			event.dataTransfer.dropEffect = 'move';
		}
	}

	function onContextColumnDrop(event: DragEvent, columnKey: ColumnKey) {
		if (!canEdit || !contextAware) {
			return;
		}
		event.preventDefault();
		const incomingTaskID =
			event.dataTransfer?.getData('application/x-tora-context-task-id') ||
			event.dataTransfer?.getData('text/plain') ||
			contextDraggedTaskId;
		if (!incomingTaskID) {
			stopContextDragging();
			return;
		}
		void moveContextTaskToColumn(incomingTaskID, columnKey);
		stopContextDragging();
	}

	function startDragging(event: DragEvent, taskId: string) {
		if (!canEdit) {
			return;
		}
		draggedTaskId = taskId;
		if (event.dataTransfer) {
			event.dataTransfer.effectAllowed = 'move';
			event.dataTransfer.setData('application/x-tora-task-id', taskId);
			event.dataTransfer.setData('text/plain', taskId);
		}
	}

	function stopDragging() {
		draggedTaskId = '';
		activeDropColumn = '';
	}

	function onColumnDragOver(event: DragEvent, columnKey: ColumnKey) {
		if (!canEdit) {
			return;
		}
		event.preventDefault();
		activeDropColumn = columnKey;
		if (event.dataTransfer) {
			event.dataTransfer.dropEffect = 'move';
		}
	}

	function onColumnDrop(event: DragEvent, columnKey: ColumnKey) {
		if (!canEdit) {
			return;
		}
		event.preventDefault();
		const incomingTaskId =
			event.dataTransfer?.getData('application/x-tora-task-id') ||
			event.dataTransfer?.getData('text/plain') ||
			draggedTaskId;
		if (!incomingTaskId) {
			stopDragging();
			return;
		}

		void moveTaskToColumn(incomingTaskId, columnKey);
		stopDragging();
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
			addBoardActivity({
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
			roomBoardError = error instanceof Error ? error.message : 'Failed to move task';
		}
	}

	function formatUpdatedAt(value: number) {
		if (!Number.isFinite(value) || value <= 0) {
			return 'Updated just now';
		}
		return `Updated ${new Date(value).toLocaleString([], {
			month: 'short',
			day: 'numeric',
			hour: 'numeric',
			minute: '2-digit'
		})}`;
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
			newTaskContent = '';
		} catch (error) {
			roomBoardError = error instanceof Error ? error.message : 'Failed to create task';
		} finally {
			creatingTask = false;
		}
	}
</script>

{#if contextAware}
	<section class="task-board context-aware-board" aria-label="Task board">
		<header class="board-header">
			<h2>{boardTitle}</h2>
			<span>{contextTasks.length}</span>
		</header>

		<form
			class="new-task-form"
			on:submit|preventDefault={() => {
				void handleCreateTask(newTaskContent);
			}}
		>
			<input
				type="text"
				bind:value={newTaskContent}
				placeholder="New Task"
				autocomplete="off"
				disabled={creatingTask}
			/>
			<button type="submit" disabled={creatingTask || !newTaskContent.trim()}>
				{creatingTask ? 'Adding...' : 'Add'}
			</button>
		</form>

		{#if contextLoading}
			<div class="board-state">Loading tasks...</div>
		{:else if contextError}
			<div class="board-state error">Unable to load tasks: {contextError}</div>
		{:else if !hasAnyContextTasks}
			<div class="board-state">No tasks yet in this workspace.</div>
		{:else}
			<div class="task-grid">
				{#each COLUMNS as column}
					<section
						class="task-column"
						class:is-drop-target={contextActiveDropColumn === column.key && canEdit}
						aria-label={column.label}
						on:dragover={(event) => onContextColumnDragOver(event, column.key)}
						on:drop={(event) => onContextColumnDrop(event, column.key)}
					>
						<header class="task-column-header">
							<h3>{column.label}</h3>
							<span>{getContextColumnTasks(column.key).length}</span>
						</header>

						<div class="task-column-body">
							{#if getContextColumnTasks(column.key).length === 0}
								<p class="task-column-empty">
									{canEdit ? 'Drop tasks here' : 'No tasks in this column'}
								</p>
							{:else}
								{#each getContextColumnTasks(column.key) as task (task.id)}
									<article
										class="task-item"
										draggable={canEdit}
										on:dragstart={(event) => startContextDragging(event, task.id)}
										on:dragend={stopContextDragging}
									>
										<div class="task-item-title">{task.title}</div>
										{#if task.description}
											<div class="task-item-description">{task.description}</div>
										{/if}
										<div class="task-item-meta">
											<span>{formatUpdatedAt(task.updatedAt)}</span>
											{#if task.assigneeId}
												<span>Assignee: {task.assigneeId}</span>
											{/if}
										</div>
									</article>
								{/each}
							{/if}
						</div>
					</section>
				{/each}
			</div>
		{/if}
	</section>
{:else}
	<section class="task-board room-board" aria-label="Task board">
		<header class="board-header">
			<h2>{boardTitle}</h2>
			<span>{$taskStore.length}</span>
		</header>

		<form
			class="new-task-form"
			on:submit|preventDefault={() => {
				void handleCreateRoomTask(newTaskContent);
			}}
		>
			<input
				type="text"
				bind:value={newTaskContent}
				placeholder="New Task"
				autocomplete="off"
				disabled={creatingTask}
			/>
			<button type="submit" disabled={creatingTask || !newTaskContent.trim() || !canEdit}>
				{creatingTask ? 'Adding...' : 'Add'}
			</button>
		</form>

		{#if $taskStoreLoading}
			<div class="board-state">Loading tasks...</div>
		{:else if roomBoardError}
			<div class="board-state error">Unable to load tasks: {roomBoardError}</div>
		{:else if $taskStoreError}
			<div class="board-state error">Unable to load tasks: {$taskStoreError}</div>
		{:else if !hasAnyTasks}
			<div class="board-state">No tasks in this room yet.</div>
		{:else}
			<div class="task-grid">
				{#each COLUMNS as column}
					<section
						class="task-column"
						class:is-drop-target={activeDropColumn === column.key && canEdit}
						aria-label={column.label}
						on:dragover={(event) => onColumnDragOver(event, column.key)}
						on:drop={(event) => onColumnDrop(event, column.key)}
					>
						<header class="task-column-header">
							<h3>{column.label}</h3>
							<span>{getColumnTasks(column.key).length}</span>
						</header>

						<div class="task-column-body">
							{#if getColumnTasks(column.key).length === 0}
								<p class="task-column-empty">
									{canEdit ? 'Drop tasks here' : 'No tasks in this column'}
								</p>
							{:else}
								{#each getColumnTasks(column.key) as task (task.id)}
									<article
										class="task-item"
										draggable={canEdit}
										on:dragstart={(event) => startDragging(event, task.id)}
										on:dragend={stopDragging}
									>
										<div class="task-item-title">{task.title}</div>
										{#if task.description}
											<div class="task-item-description">{task.description}</div>
										{/if}
										<div class="task-item-meta">
											<span>{formatUpdatedAt(task.updatedAt)}</span>
											{#if task.assigneeId}
												<span>Assignee: {task.assigneeId}</span>
											{/if}
										</div>
									</article>
								{/each}
							{/if}
						</div>
					</section>
				{/each}
			</div>
		{/if}
	</section>
{/if}

<style>
	:global(:root) {
		--workspace-taskboard-bg: #eef4fb;
		--workspace-taskboard-header-bg: #ffffff;
		--workspace-taskboard-header-border: #cfdbef;
		--workspace-taskboard-header-text: #122645;
		--workspace-taskboard-count-text: #21426f;
		--workspace-taskboard-count-bg: #e9f1ff;
		--workspace-taskboard-count-border: #b9cdec;
		--workspace-taskboard-form-bg: #ffffff;
		--workspace-taskboard-form-border: #cfdbef;
		--workspace-taskboard-input-border: #bcd0ec;
		--workspace-taskboard-input-bg: #ffffff;
		--workspace-taskboard-input-text: #122647;
		--workspace-taskboard-input-placeholder: #6b83aa;
		--workspace-taskboard-btn-border: #9fb8df;
		--workspace-taskboard-btn-bg: #eaf2ff;
		--workspace-taskboard-btn-text: #153664;
		--workspace-taskboard-state-text: #4f6487;
		--workspace-taskboard-state-border: #c8d8f0;
		--workspace-taskboard-state-bg: #f6f9ff;
		--workspace-taskboard-error-text: #b42318;
		--workspace-taskboard-column-border: #ccdaef;
		--workspace-taskboard-column-bg: #f9fbff;
		--workspace-taskboard-drop-border: #4f83d9;
		--workspace-taskboard-drop-bg: #e7f0ff;
		--workspace-taskboard-column-divider: #d2def2;
		--workspace-taskboard-column-title: #1a3157;
		--workspace-taskboard-column-count-text: #224372;
		--workspace-taskboard-column-count-bg: #e9f1ff;
		--workspace-taskboard-empty-border: #b8cce8;
		--workspace-taskboard-empty-text: #55709a;
		--workspace-taskboard-item-border: #cad8ee;
		--workspace-taskboard-item-bg: #ffffff;
		--workspace-taskboard-item-text: #142b4c;
		--workspace-taskboard-item-hover-bg: #f3f8ff;
		--workspace-taskboard-item-hover-border: #9ebce6;
		--workspace-taskboard-description: #3f5780;
		--workspace-taskboard-meta: #5d7398;
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--workspace-taskboard-bg: #171717;
		--workspace-taskboard-header-bg: #1e1e21;
		--workspace-taskboard-header-border: #343438;
		--workspace-taskboard-header-text: #f1f1f4;
		--workspace-taskboard-count-text: #d7d7de;
		--workspace-taskboard-count-bg: #29292d;
		--workspace-taskboard-count-border: #414147;
		--workspace-taskboard-form-bg: #1e1e21;
		--workspace-taskboard-form-border: #343438;
		--workspace-taskboard-input-border: #434349;
		--workspace-taskboard-input-bg: #1a1a1e;
		--workspace-taskboard-input-text: #f1f1f4;
		--workspace-taskboard-input-placeholder: #92929b;
		--workspace-taskboard-btn-border: #4b4b52;
		--workspace-taskboard-btn-bg: #2b2b31;
		--workspace-taskboard-btn-text: #ededf3;
		--workspace-taskboard-state-text: #c7c7cf;
		--workspace-taskboard-state-border: #37373d;
		--workspace-taskboard-state-bg: #1b1b1e;
		--workspace-taskboard-error-text: #ffb4b4;
		--workspace-taskboard-column-border: #33333a;
		--workspace-taskboard-column-bg: #1c1c20;
		--workspace-taskboard-drop-border: #5b5b63;
		--workspace-taskboard-drop-bg: #26262d;
		--workspace-taskboard-column-divider: #383840;
		--workspace-taskboard-column-title: #e5e5eb;
		--workspace-taskboard-column-count-text: #dadae2;
		--workspace-taskboard-column-count-bg: #2c2c33;
		--workspace-taskboard-empty-border: #4f4f57;
		--workspace-taskboard-empty-text: #b0b0b8;
		--workspace-taskboard-item-border: #3d3d43;
		--workspace-taskboard-item-bg: #222226;
		--workspace-taskboard-item-text: #f5f5f8;
		--workspace-taskboard-item-hover-bg: #2b2b30;
		--workspace-taskboard-item-hover-border: #57575d;
		--workspace-taskboard-description: #c7c7cf;
		--workspace-taskboard-meta: #a2a2ab;
	}

	.task-board {
		height: 100%;
		width: 100%;
		min-height: 0;
		padding: 1.25rem;
		background: linear-gradient(
			180deg,
			color-mix(in srgb, var(--workspace-taskboard-bg) 90%, #ffffff) 0%,
			var(--workspace-taskboard-bg) 100%
		);
	}

	:global(:root[data-theme='dark']) .task-board,
	:global(.theme-dark) .task-board {
		background: var(--workspace-taskboard-bg);
	}

	.context-aware-board,
	.room-board {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto auto minmax(0, 1fr);
		gap: 1rem;
	}

	.board-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.65rem;
		padding: 0.95rem 1.1rem;
		border-radius: 16px;
		background: var(--workspace-taskboard-header-bg);
		border: 1px solid var(--workspace-taskboard-header-border);
	}

	.board-header h2 {
		margin: 0;
		font-size: 1.1rem;
		font-weight: 700;
		letter-spacing: 0.01em;
		color: var(--workspace-taskboard-header-text);
	}

	.board-header span {
		font-size: 0.84rem;
		font-weight: 600;
		color: var(--workspace-taskboard-count-text);
		border-radius: 999px;
		padding: 0.24rem 0.64rem;
		background: var(--workspace-taskboard-count-bg);
		border: 1px solid var(--workspace-taskboard-count-border);
	}

	.new-task-form {
		display: flex;
		align-items: center;
		gap: 0.65rem;
		padding: 0.85rem;
		border-radius: 16px;
		background: var(--workspace-taskboard-form-bg);
		border: 1px solid var(--workspace-taskboard-form-border);
	}

	.new-task-form input {
		flex: 1;
		min-width: 0;
		border-radius: 12px;
		border: 1px solid var(--workspace-taskboard-input-border);
		background: var(--workspace-taskboard-input-bg);
		color: var(--workspace-taskboard-input-text);
		padding: 0.68rem 0.84rem;
		font-size: 0.93rem;
	}

	.new-task-form input::placeholder {
		color: var(--workspace-taskboard-input-placeholder);
	}

	.new-task-form button {
		border-radius: 12px;
		border: 1px solid var(--workspace-taskboard-btn-border);
		background: var(--workspace-taskboard-btn-bg);
		color: var(--workspace-taskboard-btn-text);
		padding: 0.68rem 0.96rem;
		font-size: 0.85rem;
		font-weight: 600;
		cursor: pointer;
		transition:
			border-color 0.2s ease,
			background 0.2s ease;
	}

	.new-task-form button:hover:not(:disabled) {
		border-color: color-mix(
			in srgb,
			var(--workspace-taskboard-btn-border) 70%,
			var(--workspace-taskboard-item-text)
		);
		background: color-mix(
			in srgb,
			var(--workspace-taskboard-btn-bg) 82%,
			var(--workspace-taskboard-item-bg)
		);
	}

	.new-task-form button:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	.board-state {
		height: 100%;
		min-height: 260px;
		display: grid;
		place-items: center;
		text-align: center;
		padding: 1rem;
		color: var(--workspace-taskboard-state-text);
		border: 1px solid var(--workspace-taskboard-state-border);
		background: var(--workspace-taskboard-state-bg);
		border-radius: 18px;
		font-size: 0.95rem;
	}

	.board-state.error {
		color: var(--workspace-taskboard-error-text);
	}

	.task-grid {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-columns: repeat(3, minmax(0, 1fr));
		gap: 1rem;
	}

	.task-column {
		min-height: 0;
		display: flex;
		flex-direction: column;
		border-radius: 18px;
		border: 1px solid var(--workspace-taskboard-column-border);
		background: var(--workspace-taskboard-column-bg);
		transition:
			border-color 0.2s ease,
			background 0.2s ease;
	}

	.task-column.is-drop-target {
		border-color: var(--workspace-taskboard-drop-border);
		background: var(--workspace-taskboard-drop-bg);
	}

	.task-column-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0.9rem 1rem;
		border-bottom: 1px solid var(--workspace-taskboard-column-divider);
	}

	.task-column-header h3 {
		margin: 0;
		font-size: 0.8rem;
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--workspace-taskboard-column-title);
	}

	.task-column-header span {
		font-size: 0.81rem;
		font-weight: 600;
		color: var(--workspace-taskboard-column-count-text);
		background: var(--workspace-taskboard-column-count-bg);
		border-radius: 999px;
		padding: 0.22rem 0.62rem;
	}

	.task-column-body {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 0.9rem;
		display: flex;
		flex-direction: column;
		gap: 0.78rem;
		scrollbar-width: thin;
	}

	.task-column-empty {
		margin: 0;
		padding: 1rem;
		border-radius: 12px;
		border: 1px dashed var(--workspace-taskboard-empty-border);
		color: var(--workspace-taskboard-empty-text);
		font-size: 0.9rem;
		text-align: center;
	}

	.task-item {
		border-radius: 14px;
		border: 1px solid var(--workspace-taskboard-item-border);
		background: var(--workspace-taskboard-item-bg);
		padding: 0.9rem 0.94rem;
		display: grid;
		gap: 0.52rem;
		color: var(--workspace-taskboard-item-text);
		cursor: grab;
		transition:
			transform 0.15s ease,
			border-color 0.2s ease,
			background 0.2s ease;
	}

	.task-item:hover {
		background: var(--workspace-taskboard-item-hover-bg);
		border-color: var(--workspace-taskboard-item-hover-border);
		transform: translateY(-1px);
	}

	.task-item:active {
		cursor: grabbing;
	}

	.task-item-title {
		font-size: 1rem;
		font-weight: 650;
		line-height: 1.42;
	}

	.task-item-description {
		font-size: 0.92rem;
		line-height: 1.45;
		color: var(--workspace-taskboard-description);
		white-space: pre-wrap;
		word-break: break-word;
	}

	.task-item-meta {
		display: flex;
		flex-wrap: wrap;
		gap: 0.42rem 0.68rem;
		font-size: 0.78rem;
		color: var(--workspace-taskboard-meta);
	}

	@media (max-width: 1180px) {
		.task-grid {
			display: flex;
			overflow-x: auto;
			overflow-y: hidden;
			padding-bottom: 0.25rem;
		}

		.task-column {
			flex: 0 0 min(360px, calc(100vw - 8.5rem));
		}
	}

	@media (max-width: 760px) {
		.task-board {
			padding: 0.85rem;
		}

		.context-aware-board,
		.room-board {
			gap: 0.75rem;
		}

		.board-header h2 {
			font-size: 0.98rem;
		}

		.task-column {
			flex-basis: calc(100vw - 2.1rem);
		}

		.new-task-form {
			padding: 0.72rem;
		}
	}
</style>
