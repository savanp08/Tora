<script lang="ts">
	import {
		moveTaskOptimistic,
		taskStore,
		taskStoreError,
		taskStoreLoading,
		type Task
	} from '$lib/stores/tasks';
	import { normalizeRoomIDValue } from '$lib/utils/chat/core';
	import { sendSocketPayload } from '$lib/ws';
	import { buildTaskSocketPayload } from '$lib/ws/client';

	export let roomId = '';
	export let canEdit = true;

	const COLUMNS = [
		{ key: 'todo', label: 'To Do' },
		{ key: 'in_progress', label: 'In Progress' },
		{ key: 'done', label: 'Done' }
	] as const;

	type ColumnKey = (typeof COLUMNS)[number]['key'];

	let draggedTaskId = '';
	let activeDropColumn: ColumnKey | '' = '';

	$: normalizedRoomId = normalizeRoomIDValue(roomId);
	$: todoTasks = $taskStore.filter((task) => resolveColumn(task.status) === 'todo');
	$: inProgressTasks = $taskStore.filter((task) => resolveColumn(task.status) === 'in_progress');
	$: doneTasks = $taskStore.filter((task) => resolveColumn(task.status) === 'done');
	$: hasAnyTasks = $taskStore.length > 0;

	function resolveColumn(statusValue: string): ColumnKey {
		const normalized = (statusValue || '').trim().toLowerCase().replace(/\s+/g, '_');
		if (normalized === 'in_progress') {
			return 'in_progress';
		}
		if (normalized === 'done') {
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

		moveTaskToColumn(incomingTaskId, columnKey);
		stopDragging();
	}

	function moveTaskToColumn(taskId: string, targetColumn: ColumnKey) {
		const existingTask = $taskStore.find((task) => task.id === taskId);
		if (!existingTask) {
			return;
		}
		if (resolveColumn(existingTask.status) === targetColumn) {
			return;
		}

		const updatedTask = moveTaskOptimistic(taskId, targetColumn);
		if (!updatedTask) {
			return;
		}

		const targetRoomId = normalizedRoomId || updatedTask.roomId;
		if (!targetRoomId) {
			return;
		}

		sendSocketPayload(buildTaskSocketPayload('task_move', targetRoomId, updatedTask));
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
</script>

<section class="task-board" aria-label="Task board">
	{#if $taskStoreLoading}
		<div class="board-state">Loading tasks...</div>
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

<style>
	.task-board {
		height: 100%;
		min-height: 0;
		padding: 1rem;
		background: #0d0d12;
	}

	.board-state {
		height: 100%;
		min-height: 240px;
		display: grid;
		place-items: center;
		color: rgba(236, 240, 255, 0.78);
		border: 1px solid rgba(255, 255, 255, 0.05);
		background: rgba(255, 255, 255, 0.02);
		border-radius: 18px;
	}

	.board-state.error {
		color: rgba(255, 150, 150, 0.92);
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
		border: 1px solid rgba(255, 255, 255, 0.05);
		background: rgba(255, 255, 255, 0.02);
		backdrop-filter: blur(12px);
		transition:
			border-color 0.2s ease,
			background 0.2s ease;
	}

	.task-column.is-drop-target {
		border-color: rgba(120, 179, 255, 0.6);
		background: rgba(120, 179, 255, 0.08);
	}

	.task-column-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0.85rem 0.95rem;
		border-bottom: 1px solid rgba(255, 255, 255, 0.05);
	}

	.task-column-header h3 {
		margin: 0;
		font-size: 0.86rem;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: rgba(240, 244, 255, 0.9);
	}

	.task-column-header span {
		font-size: 0.8rem;
		color: rgba(194, 201, 225, 0.85);
		background: rgba(255, 255, 255, 0.05);
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
		border: 1px dashed rgba(255, 255, 255, 0.09);
		color: rgba(210, 216, 236, 0.62);
		font-size: 0.84rem;
		text-align: center;
	}

	.task-item {
		border-radius: 13px;
		border: 1px solid rgba(255, 255, 255, 0.07);
		background: rgba(255, 255, 255, 0.03);
		padding: 0.75rem 0.8rem;
		display: grid;
		gap: 0.45rem;
		color: #f6f8ff;
		cursor: grab;
		transition:
			transform 0.15s ease,
			border-color 0.2s ease,
			background 0.2s ease;
	}

	.task-item:hover {
		background: rgba(255, 255, 255, 0.06);
		border-color: rgba(255, 255, 255, 0.15);
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
		color: rgba(215, 222, 247, 0.87);
		white-space: pre-wrap;
		word-break: break-word;
	}

	.task-item-meta {
		display: flex;
		flex-wrap: wrap;
		gap: 0.4rem 0.7rem;
		font-size: 0.72rem;
		color: rgba(191, 199, 223, 0.82);
	}

	@media (max-width: 1100px) {
		.task-grid {
			grid-template-columns: 1fr;
		}
	}
</style>
