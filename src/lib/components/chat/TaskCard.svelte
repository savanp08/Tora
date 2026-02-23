<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import type { ChatMessage } from '$lib/types/chat';
	import { parseTaskMessagePayload } from '$lib/utils/chat/task';

	export let message: ChatMessage;
	export let showDiscussButton = true;
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
	$: remainingCount = Math.max(0, taskItems.length - visibleCount);

	const dispatch = createEventDispatcher<{
		toggleTask: { messageId: string; taskIndex: number };
		addTask: { messageId: string; text: string };
		discuss: { messageId: string };
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

	function onToggleTask(taskIndex: number) {
		if (!canEditTasks) {
			return;
		}
		dispatch('toggleTask', {
			messageId: message.id,
			taskIndex
		});
	}

	function onDiscuss() {
		dispatch('discuss', {
			messageId: message.id
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
	<div class="task-card-header">
		<div class="header-text">
			<span class="task-kicker">Task</span>
			<h4>{taskTitle}</h4>
			<span>{taskItems.length} items</span>
		</div>
		{#if showAddTaskControl}
			{#if canEditTasks}
				<button type="button" class="add-task-btn" on:click|stopPropagation={openAddInput}>
					<span class="add-pill">+</span>
					<span>Add Task</span>
				</button>
			{/if}
		{/if}
	</div>

	{#if !taskPayload}
		<p class="task-fallback">Task data is unavailable.</p>
	{:else}
			<ul class="task-list">
				{#each taskItems.slice(0, visibleCount) as item, index}
					<li class:completed={item.completed}>
						<label class="task-line">
							<input
								type="checkbox"
								checked={item.completed}
								disabled={!canEditTasks}
								on:change|stopPropagation={() => onToggleTask(index)}
							/>
								<div class="task-line-content">
									<span class="task-name">{item.text}</span>
									<div class="task-meta-line">
										<span class="meta-group">
											<span class="meta-user">{resolveCreatedBy(message.senderName, index)}</span>
											<span class="meta-time">
												{formatSmartTimestamp(resolveCreatedAt(message.createdAt, index)) || '-'}
											</span>
										</span>
										<span class="meta-group">
											{#if item.completedBy}
												<span class="meta-user done-value">{item.completedBy}</span>
												<span class="meta-time">
													{item.timestamp > 0 ? formatSmartTimestamp(item.timestamp) : '-'}
												</span>
											{:else}
												<span class="meta-user open-value">open</span>
												<span class="meta-time">pending</span>
											{/if}
										</span>
									</div>
								</div>
							</label>
						</li>
				{/each}
			</ul>

		{#if showAddTaskControl && canEditTasks && showAddInput}
			<div class="add-task-input-row">
				<input type="checkbox" disabled aria-hidden="true" />
				<input
					type="text"
					bind:value={newTaskText}
					placeholder="Task name"
					on:keydown={onAddInputKeyDown}
				/>
				<button type="button" on:click|stopPropagation={submitAddTask}>Add</button>
				<button type="button" class="ghost" on:click|stopPropagation={cancelAddTask}>Cancel</button>
			</div>
		{/if}

		{#if remainingCount > 0}
			<button type="button" class="show-more" on:click|stopPropagation={showMore}>
				Show {Math.min(4, remainingCount)} more
			</button>
		{/if}
	{/if}

	{#if showDiscussButton}
		<button type="button" class="discuss-button" on:click|stopPropagation={onDiscuss}>
			💬 Discuss
		</button>
	{/if}
</div>

<style>
	.task-card {
		display: flex;
		flex-direction: column;
		gap: 0.66rem;
		padding: 0.72rem 0.74rem;
		border-radius: 13px;
		border: 1px solid #cdd8e8;
		background: linear-gradient(180deg, #fbfdff 0%, #f0f5fc 100%);
		color: #16253a;
		box-shadow:
			0 10px 20px rgba(15, 23, 42, 0.06),
			inset 0 1px 0 rgba(255, 255, 255, 0.78);
	}

	.task-card-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.6rem;
	}

	.header-text {
		display: flex;
		flex-direction: column;
		gap: 0.16rem;
		min-width: 0;
	}

	.task-kicker {
		font-size: 0.67rem;
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: #4b6283;
	}

	.task-card-header h4 {
		margin: 0;
		font-size: 0.92rem;
		font-weight: 700;
		line-height: 1.24;
		word-break: break-word;
	}

	.header-text > span:last-child {
		font-size: 0.7rem;
		color: #5f738f;
	}

	.add-task-btn {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		border: 1.5px solid #16a34a;
		background: #ecfdf3;
		color: #166534;
		border-radius: 10px;
		padding: 0.34rem 0.6rem;
		font-size: 0.76rem;
		font-weight: 700;
		cursor: pointer;
		white-space: nowrap;
	}

	.add-pill {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 1.1rem;
		height: 1.1rem;
		border-radius: 6px;
		border: 1px solid rgba(22, 101, 52, 0.3);
		background: #f4fdf7;
		font-size: 0.9rem;
		line-height: 1;
	}

	.add-task-btn:hover {
		filter: brightness(1.03);
	}

	.task-fallback {
		margin: 0;
		font-size: 0.8rem;
		color: #58708f;
	}

	.task-list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.45rem;
	}

	.task-list li {
		border: 1px solid #d2dceb;
		border-radius: 10px;
		padding: 0.48rem 0.54rem;
		background: #ffffff;
	}

	.task-line {
		display: flex;
		align-items: flex-start;
		gap: 0.5rem;
		font-size: 0.83rem;
		line-height: 1.28;
		color: inherit;
		cursor: default;
	}

	.task-line-content {
		display: flex;
		flex-direction: column;
		gap: 0.22rem;
		min-width: 0;
	}

	.task-name {
		word-break: break-word;
		font-weight: 600;
	}

	.task-line input {
		width: 0.95rem;
		height: 0.95rem;
		accent-color: #16a34a;
		cursor: pointer;
		flex-shrink: 0;
		margin-top: 0.03rem;
	}

	.task-list li.completed .task-name {
		text-decoration: line-through;
		color: #9ca3af;
	}

	.task-meta-line {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.4rem;
		max-width: 100%;
		font-size: 0.66rem;
		line-height: 1.25;
		color: #5f7694;
	}

	.meta-group {
		display: inline-flex;
		flex-direction: column;
		gap: 0.05rem;
		min-width: 0;
		padding: 0.2rem 0.34rem;
		border-radius: 8px;
		border: 1px solid #d8e2ef;
		background: #f5f8fd;
	}

	.meta-user,
	.meta-time {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.meta-user {
		font-weight: 700;
		color: #2e425f;
	}

	.meta-time {
		color: #5f7694;
	}

	.done-value {
		color: #065f46;
	}

	.open-value {
		color: #66778f;
	}

	.add-task-input-row {
		display: grid;
		grid-template-columns: 1rem minmax(0, 1fr) auto auto;
		gap: 0.36rem;
		align-items: center;
		border: 1px dashed #b8cde2;
		border-radius: 10px;
		padding: 0.44rem 0.5rem;
		background: #f8fbff;
	}

	.add-task-input-row input[type='checkbox'] {
		width: 0.95rem;
		height: 0.95rem;
		accent-color: #16a34a;
	}

	.add-task-input-row input[type='text'] {
		border: 1px solid #b9cde3;
		background: #ffffff;
		color: #142235;
		border-radius: 9px;
		padding: 0.34rem 0.5rem;
		font-size: 0.79rem;
		min-width: 0;
	}

	.add-task-input-row button {
		border: 1px solid #bfd0e8;
		background: #f7fbff;
		color: #2f4b74;
		border-radius: 9px;
		padding: 0.31rem 0.52rem;
		font-size: 0.73rem;
		font-weight: 700;
		cursor: pointer;
	}

	.add-task-input-row button:first-of-type {
		border-color: #16a34a;
		background: #ecfdf3;
		color: #166534;
	}

	.add-task-input-row button.ghost {
		background: transparent;
		border-color: #c8d4e4;
		color: #5e7290;
	}

	.show-more {
		align-self: flex-start;
		border: 1px solid #c1d2e5;
		background: #f8fbff;
		color: #2f4b74;
		border-radius: 9px;
		padding: 0.3rem 0.56rem;
		font-size: 0.73rem;
		font-weight: 600;
		cursor: pointer;
	}

	.show-more:hover {
		background: #edf4ff;
	}

	.discuss-button {
		width: 100%;
		border: 1px solid #0ea5e9;
		background: linear-gradient(180deg, #38bdf8 0%, #0284c7 100%);
		color: #ffffff;
		border-radius: 10px;
		padding: 0.45rem 0.65rem;
		font-size: 0.8rem;
		font-weight: 700;
		cursor: pointer;
	}

	.discuss-button:hover {
		filter: brightness(1.05);
	}

	:global(.messages-shell.theme-dark) .task-card,
	:global(.discussion-shell.theme-dark) .task-card {
		border-color: #3a5682;
		background: linear-gradient(180deg, #132744 0%, #10213a 100%);
		color: #e7f0ff;
		box-shadow:
			0 12px 24px rgba(2, 8, 23, 0.34),
			inset 0 1px 0 rgba(255, 255, 255, 0.08);
	}

	:global(.messages-shell.theme-dark) .task-kicker,
	:global(.discussion-shell.theme-dark) .task-kicker {
		color: #9fbbe0;
	}

	:global(.messages-shell.theme-dark) .header-text > span:last-child,
	:global(.discussion-shell.theme-dark) .header-text > span:last-child {
		color: #99b0cc;
	}

	:global(.messages-shell.theme-dark) .task-list li,
	:global(.discussion-shell.theme-dark) .task-list li {
		border-color: #425f8d;
		background: #142a49;
	}

	:global(.messages-shell.theme-dark) .task-meta-line,
	:global(.discussion-shell.theme-dark) .task-meta-line {
		color: #9eb8d8;
	}

	:global(.messages-shell.theme-dark) .meta-group,
	:global(.discussion-shell.theme-dark) .meta-group {
		border-color: #44648f;
		background: rgba(16, 35, 62, 0.7);
	}

	:global(.messages-shell.theme-dark) .meta-user,
	:global(.discussion-shell.theme-dark) .meta-user {
		color: #d6e8ff;
	}

	:global(.messages-shell.theme-dark) .meta-time,
	:global(.discussion-shell.theme-dark) .meta-time {
		color: #9eb8d8;
	}

	:global(.messages-shell.theme-dark) .open-value,
	:global(.discussion-shell.theme-dark) .open-value {
		color: #a6b9d2;
	}

	:global(.messages-shell.theme-dark) .add-task-input-row,
	:global(.discussion-shell.theme-dark) .add-task-input-row {
		border-color: #47648f;
		background: rgba(16, 35, 62, 0.75);
	}

	:global(.messages-shell.theme-dark) .add-task-input-row input[type='text'],
	:global(.discussion-shell.theme-dark) .add-task-input-row input[type='text'] {
		border-color: #45638f;
		background: #10213b;
		color: #e7f0ff;
	}

	:global(.messages-shell.theme-dark) .add-task-input-row button,
	:global(.discussion-shell.theme-dark) .add-task-input-row button {
		border-color: #3f5d87;
		background: #122640;
		color: #d3e4fb;
	}

	:global(.messages-shell.theme-dark) .add-task-input-row button:first-of-type,
	:global(.discussion-shell.theme-dark) .add-task-input-row button:first-of-type {
		border-color: #22c55e;
		background: rgba(22, 101, 52, 0.24);
		color: #86efac;
	}

	:global(.bubble.mine) .task-card {
		border-color: rgba(255, 255, 255, 0.22);
		background: rgba(255, 255, 255, 0.1);
		color: #eef4ff;
		box-shadow: none;
	}

	:global(.bubble.mine) .task-kicker,
	:global(.bubble.mine) .header-text > span:last-child {
		color: #c8d8ee;
	}

	:global(.bubble.mine) .task-list li {
		border-color: rgba(255, 255, 255, 0.2);
		background: rgba(255, 255, 255, 0.08);
	}

	:global(.bubble.mine) .task-meta-line {
		color: #d2e2f7;
	}

	:global(.bubble.mine) .meta-group {
		border-color: rgba(255, 255, 255, 0.24);
		background: rgba(255, 255, 255, 0.1);
	}

	:global(.bubble.mine) .meta-user,
	:global(.bubble.mine) .meta-time {
		color: #e1ecfb;
	}

	:global(.bubble.mine) .open-value {
		color: #e2ebf9;
	}

	:global(.bubble.mine) .add-task-btn {
		border-color: #86efac;
		background: rgba(22, 163, 74, 0.2);
		color: #dcfce7;
	}

	:global(.bubble.mine) .add-pill {
		border-color: rgba(220, 252, 231, 0.4);
		background: rgba(22, 163, 74, 0.22);
	}

	@media (max-width: 600px) {
		.task-meta-line {
			grid-template-columns: 1fr;
		}

		.add-task-input-row {
			grid-template-columns: 1rem minmax(0, 1fr);
		}

		.add-task-input-row button {
			justify-self: start;
		}
	}
</style>
