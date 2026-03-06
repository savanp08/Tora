<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import type { ChatMessage } from '$lib/types/chat';
	import { parseTaskMessagePayload } from '$lib/utils/chat/task';

	export let message: ChatMessage;
	export let showAddTaskControl = true;
	export let canEditTasks = true;

	let visibleCount = 4;
	let previousMessageId = '';
	let showAddInput = false;
	let newTaskText = '';

	$: if (message.id !== previousMessageId) {
		visibleCount = 4;
		showAddInput = false;
		newTaskText = '';
		previousMessageId = message.id;
	}

	$: taskPayload = parseTaskMessagePayload(message.content);
	$: taskTitle = taskPayload?.title || 'Task';
	$: taskItems = taskPayload?.tasks ?? [];
	$: totalTasks = taskItems.length;
	$: completedTasks = taskItems.filter((item) => item.completed).length;
	$: progressPercent = totalTasks > 0 ? (completedTasks / totalTasks) * 100 : 0;
	$: remainingCount = Math.max(0, taskItems.length - visibleCount);

	const dispatch = createEventDispatcher<{
		toggleTask: { messageId: string; taskIndex: number };
		addTask: { messageId: string; text: string };
	}>();

	function formatSmartTimestamp(timestamp: number) {
		if (!Number.isFinite(timestamp) || timestamp <= 0) {
			return '';
		}
		const date = new Date(timestamp);
		const now = new Date();
		const isSameYear = date.getFullYear() === now.getFullYear();
		return date.toLocaleString([], {
			month: 'short',
			day: 'numeric',
			hour: 'numeric',
			minute: '2-digit',
			...(isSameYear ? {} : { year: 'numeric' })
		});
	}

	function formatHeaderDate(timestamp: number) {
		if (!Number.isFinite(timestamp) || timestamp <= 0) {
			return 'Today';
		}
		const date = new Date(timestamp);
		const now = new Date();
		const isSameYear = date.getFullYear() === now.getFullYear();
		return date.toLocaleDateString([], {
			month: 'short',
			day: 'numeric',
			...(isSameYear ? {} : { year: 'numeric' })
		});
	}

	function onToggleTask(taskIndex: number) {
		if (!canEditTasks) {
			return;
		}
		dispatch('toggleTask', {
			messageId: message.id,
			taskIndex
		});
	}

	function showMore() {
		visibleCount += 4;
	}

	function openAddInput() {
		if (!canEditTasks) {
			return;
		}
		showAddInput = true;
	}

	function cancelAddTask() {
		showAddInput = false;
		newTaskText = '';
	}

	function submitAddTask() {
		const text = (newTaskText || '').trim();
		if (!text) {
			return;
		}
		dispatch('addTask', {
			messageId: message.id,
			text
		});
		newTaskText = '';
		showAddInput = false;
	}

	function onAddInputKeyDown(event: KeyboardEvent) {
		if (event.key === 'Enter') {
			event.preventDefault();
			submitAddTask();
		}
		if (event.key === 'Escape') {
			event.preventDefault();
			cancelAddTask();
		}
	}

	function resolveCreatedBy(fallback: string, index: number) {
		const item = taskItems[index];
		return (item?.createdBy || fallback || 'Unknown').trim();
	}

	function resolveCreatedAt(fallback: number, index: number) {
		const item = taskItems[index];
		const candidate = Number(item?.createdAt ?? 0);
		if (Number.isFinite(candidate) && candidate > 0) {
			return candidate;
		}
		return fallback;
	}
</script>

<div class="task-card" role="group" aria-label={taskTitle}>
	<div class="progress-track" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow={Math.round(progressPercent)}>
		<div class="progress-fill" style={`width: ${progressPercent}%;`}></div>
	</div>

	<header class="task-card-header">
		<div class="task-title-wrap">
			<h4>{taskTitle}</h4>
			<span>{completedTasks}/{totalTasks} completed</span>
		</div>
		<div class="task-header-meta">
			Created by {message.senderName || 'Unknown'} • {formatHeaderDate(message.createdAt)}
		</div>
	</header>

	{#if showAddTaskControl && canEditTasks}
		<button type="button" class="add-task-btn" on:click|stopPropagation={openAddInput}>
			+ Add task
		</button>
	{/if}

	{#if !taskPayload}
		<p class="task-fallback">Task data is unavailable.</p>
	{:else}
		<ul class="task-list">
			{#each taskItems.slice(0, visibleCount) as item, index}
				<li class="task-row" class:completed={item.completed}>
					<button
						type="button"
						class="custom-checkbox"
						class:checked={item.completed}
						disabled={!canEditTasks}
						on:click|stopPropagation={() => onToggleTask(index)}
						aria-label={item.completed ? 'Mark task as open' : 'Mark task as complete'}
					>
						<svg viewBox="0 0 20 20" class="check-icon" aria-hidden="true">
							<path d="m4.5 10.5 3.2 3.2 7.3-7.2"></path>
						</svg>
					</button>
					<div class="task-line-content">
						<span class="task-name">{item.text}</span>
						<div class="task-meta-line">
							<span class="task-meta">
								<strong>{resolveCreatedBy(message.senderName, index)}</strong>
								<small>{formatSmartTimestamp(resolveCreatedAt(message.createdAt, index)) || '-'}</small>
							</span>
							<span class="task-meta">
								{#if item.completedBy}
									<strong class="done-value">{item.completedBy}</strong>
									<small>{item.timestamp > 0 ? formatSmartTimestamp(item.timestamp) : '-'}</small>
								{:else}
									<strong class="open-value">Open</strong>
									<small>Pending</small>
								{/if}
							</span>
						</div>
					</div>
				</li>
			{/each}
		</ul>

			{#if showAddTaskControl && canEditTasks && showAddInput}
				<div class="add-task-input-row">
					<input
						type="text"
						bind:value={newTaskText}
						placeholder="Task name"
						on:keydown={onAddInputKeyDown}
					/>
					<div class="add-task-actions">
						<button type="button" on:click|stopPropagation={submitAddTask}>Add</button>
						<button type="button" class="ghost" on:click|stopPropagation={cancelAddTask}>
							Cancel
						</button>
					</div>
				</div>
			{/if}

		{#if remainingCount > 0}
			<button type="button" class="show-more" on:click|stopPropagation={showMore}>
				Show {Math.min(4, remainingCount)} more
			</button>
		{/if}
	{/if}
</div>

<style>
	.task-card {
		background: var(--bg-surface, #18181b);
		border: 1px solid var(--border, #27272a);
		border-radius: 12px;
		box-shadow: 0 4px 12px rgba(0, 0, 0, 0.1);
		padding: 1.25rem;
		display: flex;
		flex-direction: column;
		gap: 0.9rem;
		color: #f4f4f5;
	}

	.progress-track {
		width: 100%;
		height: 4px;
		border-radius: 4px;
		background: #27272a;
		overflow: hidden;
	}

	.progress-fill {
		height: 4px;
		border-radius: 4px;
		background: #10b981;
		transition: width 0.4s cubic-bezier(0.4, 0, 0.2, 1);
	}

	.task-card-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.8rem;
	}

	.task-title-wrap {
		display: flex;
		flex-direction: column;
		min-width: 0;
	}

	.task-title-wrap h4 {
		margin: 0;
		font-size: 1rem;
		line-height: 1.25;
		font-weight: 650;
		word-break: break-word;
	}

	.task-title-wrap span {
		font-size: 0.74rem;
		color: #a1a1aa;
	}

	.task-header-meta {
		font-size: 0.75rem;
		color: #a1a1aa;
		white-space: nowrap;
	}

	.add-task-btn {
		align-self: flex-start;
		border: 1px solid #2f2f35;
		background: transparent;
		color: #d4d4d8;
		border-radius: 10px;
		padding: 0.36rem 0.62rem;
		font-size: 0.77rem;
		font-weight: 600;
		cursor: pointer;
		transition: background-color 0.2s ease;
	}

	.add-task-btn:hover {
		background: rgba(255, 255, 255, 0.05);
	}

	.task-fallback {
		margin: 0;
		font-size: 0.8rem;
		color: #a1a1aa;
	}

	.task-list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.42rem;
	}

	.task-row {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr);
		gap: 0.6rem;
		align-items: flex-start;
		padding: 0.52rem 0.56rem;
		border-radius: 10px;
		transition: background-color 0.2s ease;
	}

	.task-row:hover {
		background: rgba(255, 255, 255, 0.03);
	}

	.custom-checkbox {
		width: 20px;
		height: 20px;
		border: 2px solid #52525b;
		border-radius: 6px;
		background: transparent;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		padding: 0;
		margin-top: 0.08rem;
		transition: all 0.2s;
		flex-shrink: 0;
	}

	.custom-checkbox.checked {
		background: #10b981;
		border-color: #10b981;
	}

	.custom-checkbox:disabled {
		opacity: 0.56;
		cursor: not-allowed;
	}

	.check-icon {
		width: 11px;
		height: 11px;
		fill: none;
		stroke: #ffffff;
		stroke-width: 2.4;
		transform: scale(0);
		transition: transform 0.2s ease;
	}

	.custom-checkbox.checked .check-icon {
		transform: scale(1);
	}

	.task-line-content {
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
	}

	.task-name {
		font-size: 0.84rem;
		font-weight: 500;
		word-break: break-word;
		transition: all 0.2s ease;
	}

	.task-row.completed .task-name {
		color: #71717a;
		text-decoration: line-through;
	}

	.task-meta-line {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.38rem;
	}

	.task-meta {
		display: inline-flex;
		flex-direction: column;
		gap: 0.06rem;
		padding: 0.23rem 0.36rem;
		border-radius: 8px;
		background: #0f0f12;
		overflow: hidden;
	}

	.task-meta strong,
	.task-meta small {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.task-meta strong {
		font-size: 0.66rem;
		font-weight: 600;
		color: #f4f4f5;
	}

	.task-meta small {
		font-size: 0.64rem;
		color: #a1a1aa;
	}

	.task-meta .done-value {
		color: #10b981;
	}

	.task-meta .open-value {
		color: #a1a1aa;
	}

	.add-task-input-row {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: 0.38rem;
		align-items: center;
		padding: 0.5rem 0.56rem;
		border: 1px dashed #2f2f35;
		border-radius: 10px;
		background: rgba(255, 255, 255, 0.01);
	}

	.add-task-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.38rem;
		flex-wrap: wrap;
	}

	.add-task-input-row input[type='text'] {
		border: 1px solid #2f2f35;
		background: #121216;
		color: #f4f4f5;
		border-radius: 8px;
		padding: 0.36rem 0.52rem;
		font-size: 0.78rem;
		min-width: 0;
	}

	.add-task-input-row button {
		border: 1px solid #2f2f35;
		background: transparent;
		color: #a1a1aa;
		border-radius: 8px;
		padding: 0.3rem 0.48rem;
		font-size: 0.72rem;
		font-weight: 600;
		cursor: pointer;
		white-space: nowrap;
		min-width: 3.5rem;
	}

	.add-task-actions button:first-child {
		border-color: #10b981;
		color: #10b981;
	}

	.show-more {
		align-self: flex-start;
		border: none;
		background: transparent;
		color: #a1a1aa;
		padding: 0;
		font-size: 0.73rem;
		font-weight: 600;
		cursor: pointer;
	}

	.show-more:hover {
		color: #f4f4f5;
	}

	@media (max-width: 680px) {
		.task-card-header {
			flex-direction: column;
			align-items: flex-start;
		}

		.task-header-meta {
			white-space: normal;
		}

		.task-meta-line {
			grid-template-columns: 1fr;
		}

		.add-task-input-row {
			grid-template-columns: 1fr;
		}

		.add-task-actions {
			justify-self: start;
		}
	}
</style>
