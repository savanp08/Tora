<script lang="ts">
	import { currentUser } from '$lib/store';
	import { activeContext } from '$lib/stores/jiraContext';
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
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';

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
	$: normalizedRoomId = normalizeRoomIDValue(roomId);
	$: todoTasks = $taskStore.filter((task) => resolveColumn(task.status) === 'todo');
	$: inProgressTasks = $taskStore.filter((task) => resolveColumn(task.status) === 'in_progress');
	$: doneTasks = $taskStore.filter((task) => resolveColumn(task.status) === 'done');
	$: hasAnyTasks = $taskStore.length > 0;
	$: contextTodoTasks = contextTasks.filter((task) => resolveColumn(task.status) === 'todo');
	$: contextInProgressTasks = contextTasks.filter((task) => resolveColumn(task.status) === 'in_progress');
	$: contextDoneTasks = contextTasks.filter((task) => resolveColumn(task.status) === 'done');
	$: hasAnyContextTasks = contextTasks.length > 0;
	$: boardTitle = contextAware
		? $activeContext.name.trim() || 'Workspace Tasks'
		: 'Room Tasks';
	$: contextKey = `${$activeContext.type}:${$activeContext.id}`;
	$: if (contextAware && contextKey !== lastContextKey) {
		lastContextKey = contextKey;
		void loadContextTasks();
	}

	function withSessionUserHeaders(headers: Record<string, string> = {}) {
		if (!sessionUserID) {
			return headers;
		}
		return {
			...headers,
			'X-User-Id': sessionUserID
		};
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
		const payload = (await response.json().catch(() => null)) as
			| {
					error?: string;
			  }
			| null;
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
			const response = await fetch(`${API_BASE}/api/personal/items/${encodeURIComponent(taskID)}/status`, {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				credentials: 'include',
				body: JSON.stringify({
					status: formatContextStatusForPersonal(columnKey)
				})
			});
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
						status: $activeContext.type === 'personal' ? formatContextStatusForPersonal(columnKey) : columnKey,
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
			await persistRoomTaskStatus(taskId, targetRoomId, targetColumn);
			sendSocketPayload(buildTaskSocketPayload('task_move', targetRoomId, updatedTask));
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
			const response = await fetch(`${API_BASE}/api/rooms/${encodeURIComponent(normalizedTargetRoomID)}/tasks`, {
				method: 'POST',
				headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
				credentials: 'include',
				body: JSON.stringify({ content })
			});
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
		--workspace-taskboard-bg: rgba(255, 255, 255, 0.02);
		--workspace-taskboard-header-bg: rgba(255, 255, 255, 0.6);
		--workspace-taskboard-header-border: rgba(174, 198, 232, 0.5);
		--workspace-taskboard-header-text: rgba(24, 42, 73, 0.94);
		--workspace-taskboard-count-text: rgba(63, 82, 116, 0.88);
		--workspace-taskboard-count-bg: rgba(226, 237, 252, 0.88);
		--workspace-taskboard-count-border: rgba(143, 171, 216, 0.58);
		--workspace-taskboard-form-bg: rgba(255, 255, 255, 0.58);
		--workspace-taskboard-form-border: rgba(173, 196, 232, 0.5);
		--workspace-taskboard-input-border: rgba(137, 167, 217, 0.5);
		--workspace-taskboard-input-bg: rgba(255, 255, 255, 0.72);
		--workspace-taskboard-input-text: #12223f;
		--workspace-taskboard-input-placeholder: rgba(77, 100, 139, 0.6);
		--workspace-taskboard-btn-border: rgba(101, 133, 191, 0.36);
		--workspace-taskboard-btn-bg: rgba(255, 255, 255, 0.78);
		--workspace-taskboard-btn-text: #122647;
		--workspace-taskboard-state-text: rgba(61, 80, 113, 0.78);
		--workspace-taskboard-state-border: rgba(152, 178, 220, 0.42);
		--workspace-taskboard-state-bg: rgba(255, 255, 255, 0.46);
		--workspace-taskboard-error-text: #9d2b41;
		--workspace-taskboard-column-border: rgba(169, 191, 227, 0.5);
		--workspace-taskboard-column-bg: rgba(255, 255, 255, 0.48);
		--workspace-taskboard-drop-border: rgba(92, 142, 221, 0.66);
		--workspace-taskboard-drop-bg: rgba(159, 197, 253, 0.28);
		--workspace-taskboard-column-divider: rgba(162, 186, 226, 0.5);
		--workspace-taskboard-column-title: rgba(27, 45, 77, 0.9);
		--workspace-taskboard-column-count-text: rgba(67, 86, 118, 0.88);
		--workspace-taskboard-column-count-bg: rgba(226, 237, 252, 0.92);
		--workspace-taskboard-empty-border: rgba(143, 171, 216, 0.5);
		--workspace-taskboard-empty-text: rgba(66, 86, 120, 0.76);
		--workspace-taskboard-item-border: rgba(163, 186, 223, 0.54);
		--workspace-taskboard-item-bg: rgba(255, 255, 255, 0.6);
		--workspace-taskboard-item-text: #142443;
		--workspace-taskboard-item-hover-bg: rgba(236, 245, 255, 0.88);
		--workspace-taskboard-item-hover-border: rgba(111, 144, 205, 0.56);
		--workspace-taskboard-description: rgba(70, 90, 124, 0.86);
		--workspace-taskboard-meta: rgba(80, 99, 131, 0.8);
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--workspace-taskboard-bg: #0d0d12;
		--workspace-taskboard-header-bg: rgba(255, 255, 255, 0.03);
		--workspace-taskboard-header-border: rgba(255, 255, 255, 0.09);
		--workspace-taskboard-header-text: rgba(242, 245, 255, 0.95);
		--workspace-taskboard-count-text: rgba(198, 206, 226, 0.9);
		--workspace-taskboard-count-bg: rgba(255, 255, 255, 0.06);
		--workspace-taskboard-count-border: rgba(255, 255, 255, 0.1);
		--workspace-taskboard-form-bg: rgba(255, 255, 255, 0.03);
		--workspace-taskboard-form-border: rgba(255, 255, 255, 0.09);
		--workspace-taskboard-input-border: rgba(255, 255, 255, 0.13);
		--workspace-taskboard-input-bg: rgba(255, 255, 255, 0.03);
		--workspace-taskboard-input-text: #eef3ff;
		--workspace-taskboard-input-placeholder: rgba(206, 214, 236, 0.62);
		--workspace-taskboard-btn-border: rgba(255, 255, 255, 0.2);
		--workspace-taskboard-btn-bg: rgba(255, 255, 255, 0.08);
		--workspace-taskboard-btn-text: #f2f6ff;
		--workspace-taskboard-state-text: rgba(236, 240, 255, 0.78);
		--workspace-taskboard-state-border: rgba(255, 255, 255, 0.05);
		--workspace-taskboard-state-bg: rgba(255, 255, 255, 0.02);
		--workspace-taskboard-error-text: rgba(255, 150, 150, 0.92);
		--workspace-taskboard-column-border: rgba(255, 255, 255, 0.05);
		--workspace-taskboard-column-bg: rgba(255, 255, 255, 0.02);
		--workspace-taskboard-drop-border: rgba(120, 179, 255, 0.6);
		--workspace-taskboard-drop-bg: rgba(120, 179, 255, 0.08);
		--workspace-taskboard-column-divider: rgba(255, 255, 255, 0.05);
		--workspace-taskboard-column-title: rgba(240, 244, 255, 0.9);
		--workspace-taskboard-column-count-text: rgba(194, 201, 225, 0.85);
		--workspace-taskboard-column-count-bg: rgba(255, 255, 255, 0.05);
		--workspace-taskboard-empty-border: rgba(255, 255, 255, 0.09);
		--workspace-taskboard-empty-text: rgba(210, 216, 236, 0.62);
		--workspace-taskboard-item-border: rgba(255, 255, 255, 0.07);
		--workspace-taskboard-item-bg: rgba(255, 255, 255, 0.03);
		--workspace-taskboard-item-text: #f6f8ff;
		--workspace-taskboard-item-hover-bg: rgba(255, 255, 255, 0.06);
		--workspace-taskboard-item-hover-border: rgba(255, 255, 255, 0.15);
		--workspace-taskboard-description: rgba(215, 222, 247, 0.87);
		--workspace-taskboard-meta: rgba(190, 197, 220, 0.82);
	}

	.task-board {
		height: 100%;
		width: 100%;
		min-height: 0;
		padding: 1rem;
		background: var(--workspace-taskboard-bg);
	}

	.context-aware-board,
	.room-board {
		display: grid;
		grid-template-rows: auto auto 1fr;
		gap: 0.8rem;
	}

	.board-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
		padding: 0.7rem 0.9rem;
		border-radius: 14px;
		background: var(--workspace-taskboard-header-bg);
		border: 1px solid var(--workspace-taskboard-header-border);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
	}

	.board-header h2 {
		margin: 0;
		font-size: 0.92rem;
		letter-spacing: 0.03em;
		color: var(--workspace-taskboard-header-text);
	}

	.board-header span {
		font-size: 0.75rem;
		color: var(--workspace-taskboard-count-text);
		border-radius: 999px;
		padding: 0.18rem 0.55rem;
		background: var(--workspace-taskboard-count-bg);
		border: 1px solid var(--workspace-taskboard-count-border);
	}

	.new-task-form {
		display: flex;
		gap: 0.55rem;
		padding: 0.75rem;
		border-radius: 14px;
		background: var(--workspace-taskboard-form-bg);
		border: 1px solid var(--workspace-taskboard-form-border);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
	}

	.new-task-form input {
		flex: 1;
		min-width: 0;
		border-radius: 10px;
		border: 1px solid var(--workspace-taskboard-input-border);
		background: var(--workspace-taskboard-input-bg);
		color: var(--workspace-taskboard-input-text);
		padding: 0.56rem 0.7rem;
	}

	.new-task-form input::placeholder {
		color: var(--workspace-taskboard-input-placeholder);
	}

	.new-task-form button {
		border-radius: 10px;
		border: 1px solid var(--workspace-taskboard-btn-border);
		background: var(--workspace-taskboard-btn-bg);
		color: var(--workspace-taskboard-btn-text);
		padding: 0.56rem 0.86rem;
		font-size: 0.8rem;
		cursor: pointer;
	}

	.new-task-form button:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	.board-state {
		height: 100%;
		min-height: 240px;
		display: grid;
		place-items: center;
		color: var(--workspace-taskboard-state-text);
		border: 1px solid var(--workspace-taskboard-state-border);
		background: var(--workspace-taskboard-state-bg);
		border-radius: 18px;
	}

	.board-state.error {
		color: var(--workspace-taskboard-error-text);
	}

	.task-grid {
		height: 100%;
		display: grid;
		grid-template-columns: repeat(3, minmax(0, 1fr));
		gap: 0.9rem;
		min-height: 0;
	}

	.task-column {
		min-height: 0;
		display: flex;
		flex-direction: column;
		border-radius: 16px;
		border: 1px solid var(--workspace-taskboard-column-border);
		background: var(--workspace-taskboard-column-bg);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
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
		padding: 0.85rem 0.95rem;
		border-bottom: 1px solid var(--workspace-taskboard-column-divider);
	}

	.task-column-header h3 {
		margin: 0;
		font-size: 0.86rem;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--workspace-taskboard-column-title);
	}

	.task-column-header span {
		font-size: 0.8rem;
		color: var(--workspace-taskboard-column-count-text);
		background: var(--workspace-taskboard-column-count-bg);
		border-radius: 999px;
		padding: 0.2rem 0.55rem;
	}

	.task-column-body {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 0.8rem;
		display: flex;
		flex-direction: column;
		gap: 0.65rem;
	}

	.task-column-empty {
		margin: 0;
		padding: 0.9rem;
		border-radius: 12px;
		border: 1px dashed var(--workspace-taskboard-empty-border);
		color: var(--workspace-taskboard-empty-text);
		font-size: 0.84rem;
		text-align: center;
	}

	.task-item {
		border-radius: 13px;
		border: 1px solid var(--workspace-taskboard-item-border);
		background: var(--workspace-taskboard-item-bg);
		padding: 0.75rem 0.8rem;
		display: grid;
		gap: 0.45rem;
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
		font-size: 0.96rem;
		font-weight: 600;
		line-height: 1.32;
	}

	.task-item-description {
		font-size: 0.86rem;
		line-height: 1.42;
		color: var(--workspace-taskboard-description);
		white-space: pre-wrap;
		word-break: break-word;
	}

	.task-item-meta {
		display: flex;
		flex-wrap: wrap;
		gap: 0.4rem 0.65rem;
		font-size: 0.72rem;
		color: var(--workspace-taskboard-meta);
	}

	@media (max-width: 980px) {
		.task-grid {
			grid-template-columns: 1fr;
		}

		.task-board {
			padding: 0.75rem;
		}
	}
</style>
