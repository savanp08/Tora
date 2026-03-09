<script lang="ts">
	import { onMount } from 'svelte';
	import {
		addItem,
		addItemsBulk,
		deleteItem,
		fetchItems,
		personalItems,
		type PersonalItem,
		type PersonalItemInput,
		updateStatus
	} from '$lib/stores/personal';

	type AddMode = 'note' | 'reminder' | 'tasks' | null;

	type TaskDraft = {
		key: string;
		done: boolean;
		title: string;
		description: string;
		startAt: string;
		endAt: string;
	};

	const repeatOptions = [
		{ value: 'none', label: 'Does not repeat' },
		{ value: 'daily', label: 'Daily' },
		{ value: 'weekly', label: 'Weekly' },
		{ value: 'monthly', label: 'Monthly' },
		{ value: 'weekdays', label: 'Weekdays' }
	];

	const creationTimeFormatter = new Intl.DateTimeFormat('en-US', {
		month: 'short',
		day: 'numeric',
		year: 'numeric',
		hour: 'numeric',
		minute: '2-digit'
	});

	let isLoading = false;
	let isSaving = false;
	let activeItemID = '';
	let errorMessage = '';
	let addMode: AddMode = null;
	let isAddMenuOpen = false;

	let noteTitle = '';
	let noteText = '';

	let reminderTitle = '';
	let reminderText = '';
	let reminderStartAt = toDateTimeLocal(new Date());
	let reminderAt = toDateTimeLocal(addMinutes(new Date(), 10));
	let reminderRepeat = 'none';

	let taskDraftSerial = 1;
	let taskDrafts: TaskDraft[] = [createTaskDraft()];

	$: sortedItems = [...$personalItems].sort(
		(left, right) => timestampOf(right.created_at) - timestampOf(left.created_at)
	);
	$: validTaskDraftCount = taskDrafts.filter(
		(draft) => draft.title.trim() !== '' || draft.description.trim() !== ''
	).length;

	onMount(() => {
		void loadItems();
	});

	function timestampOf(value: string) {
		const parsed = Date.parse(value);
		return Number.isFinite(parsed) ? parsed : 0;
	}

	function addMinutes(input: Date, minutes: number) {
		return new Date(input.getTime() + minutes * 60_000);
	}

	function toDateTimeLocal(input: Date) {
		const local = new Date(input.getTime() - input.getTimezoneOffset() * 60_000);
		return local.toISOString().slice(0, 16);
	}

	function toISOOrNull(value: string) {
		const normalized = value.trim();
		if (!normalized) {
			return null;
		}
		const parsed = Date.parse(normalized);
		if (!Number.isFinite(parsed)) {
			return null;
		}
		return new Date(parsed).toISOString();
	}

	function createTaskDraft(): TaskDraft {
		const key = `task-draft-${Date.now()}-${taskDraftSerial++}`;
		return {
			key,
			done: false,
			title: '',
			description: '',
			startAt: '',
			endAt: ''
		};
	}

	function openAddMenu() {
		isAddMenuOpen = !isAddMenuOpen;
	}

	function closeAddMenu() {
		isAddMenuOpen = false;
	}

	function openComposer(mode: Exclude<AddMode, null>) {
		addMode = mode;
		closeAddMenu();
		errorMessage = '';
		if (mode === 'note') {
			noteTitle = '';
			noteText = '';
		}
		if (mode === 'reminder') {
			reminderTitle = '';
			reminderText = '';
			reminderStartAt = toDateTimeLocal(new Date());
			reminderAt = toDateTimeLocal(addMinutes(new Date(), 10));
			reminderRepeat = 'none';
		}
		if (mode === 'tasks') {
			taskDrafts = [createTaskDraft()];
		}
	}

	function closeComposer() {
		addMode = null;
		errorMessage = '';
	}

	function normalizedStatus(value: string) {
		return value.trim().toLowerCase();
	}

	function isCompleted(item: PersonalItem) {
		const normalized = normalizedStatus(item.status);
		return normalized === 'completed' || normalized === 'done';
	}

	function displayTitle(item: PersonalItem) {
		const title = item.title.trim();
		if (title) {
			return title;
		}
		const content = item.content.trim();
		if (content) {
			return content;
		}
		return 'Untitled item';
	}

	function displayBody(item: PersonalItem) {
		const title = displayTitle(item);
		const description = item.description.trim();
		if (description) {
			return description;
		}
		const content = item.content.trim();
		if (!content || content === title) {
			return '';
		}
		return content;
	}

	function formatDateTime(value: string | null) {
		if (!value) {
			return '';
		}
		const parsed = Date.parse(value);
		if (!Number.isFinite(parsed)) {
			return '';
		}
		return creationTimeFormatter.format(parsed);
	}

	function formatCreationTime() {
		return creationTimeFormatter.format(new Date());
	}

	function typeLabel(type: string) {
		const normalized = type.trim().toLowerCase();
		if (normalized === 'reminder') {
			return 'Reminder';
		}
		if (normalized === 'note') {
			return 'Note';
		}
		return 'Task';
	}

	function buildScheduleSummary(item: PersonalItem) {
		const type = item.type.trim().toLowerCase();
		if (type === 'reminder') {
			const reminderAt = formatDateTime(item.remind_at || item.due_at);
			if (!reminderAt) {
				return 'Reminder time not set';
			}
				if (item.repeat_rule.trim()) {
					return `Reminds ${reminderAt} - repeats ${item.repeat_rule}`;
				}
			return `Reminds ${reminderAt}`;
		}
		if (type === 'task') {
			const start = formatDateTime(item.start_at);
			const end = formatDateTime(item.end_at || item.due_at);
			if (start && end) {
					return `${start} -> ${end}`;
			}
			if (end) {
				return `Due ${end}`;
			}
			if (start) {
				return `Starts ${start}`;
			}
			return 'No schedule';
		}
		const due = formatDateTime(item.due_at);
		if (due) {
			return `Linked reminder ${due}`;
		}
		return 'No schedule';
	}

	async function loadItems() {
		isLoading = true;
		errorMessage = '';
		try {
			await fetchItems();
		} catch (error) {
			errorMessage = error instanceof Error ? error.message : 'Failed to load personal items';
		} finally {
			isLoading = false;
		}
	}

	async function handleCreateNote() {
		if (isSaving) {
			return;
		}
		const content = noteText.trim();
		const title = noteTitle.trim();
		if (!content && !title) {
			errorMessage = 'Add note text before saving.';
			return;
		}
		isSaving = true;
		errorMessage = '';
		try {
			await addItem({
				type: 'note',
				title,
				content: content || title,
				description: content
			});
			closeComposer();
		} catch (error) {
			errorMessage = error instanceof Error ? error.message : 'Failed to create note';
		} finally {
			isSaving = false;
		}
	}

	async function handleCreateReminder() {
		if (isSaving) {
			return;
		}
		const content = reminderText.trim();
		const title = reminderTitle.trim();
		if (!content && !title) {
			errorMessage = 'Add reminder text before scheduling.';
			return;
		}
		const remindAtISO = toISOOrNull(reminderAt);
		if (!remindAtISO) {
			errorMessage = 'Reminder date and time are required.';
			return;
		}

		isSaving = true;
		errorMessage = '';
		try {
			await addItem({
				type: 'reminder',
				title,
				content: content || title,
				description: content,
				start_at: toISOOrNull(reminderStartAt),
				remind_at: remindAtISO,
				due_at: remindAtISO,
				repeat_rule: reminderRepeat
			});
			closeComposer();
		} catch (error) {
			errorMessage = error instanceof Error ? error.message : 'Failed to create reminder';
		} finally {
			isSaving = false;
		}
	}

	function addTaskDraft() {
		taskDrafts = [...taskDrafts, createTaskDraft()];
	}

	function removeTaskDraft(draftKey: string) {
		if (taskDrafts.length === 1) {
			taskDrafts = [{ ...taskDrafts[0], title: '', description: '', startAt: '', endAt: '', done: false }];
			return;
		}
		taskDrafts = taskDrafts.filter((draft) => draft.key !== draftKey);
	}

	async function handleCreateTasks() {
		if (isSaving) {
			return;
		}

		const draftPayloads: Array<PersonalItemInput | null> = taskDrafts.map((draft) => {
				const title = draft.title.trim();
				const description = draft.description.trim();
				const content = title || description;
				if (!content) {
					return null;
				}
				return {
					type: 'task',
					title: title || content,
					content,
					description,
					status: draft.done ? 'completed' : 'pending',
					start_at: toISOOrNull(draft.startAt),
					end_at: toISOOrNull(draft.endAt),
					due_at: toISOOrNull(draft.endAt)
				};
			});
		const payloads: PersonalItemInput[] = draftPayloads.filter(
			(value): value is PersonalItemInput => value !== null
		);

		if (payloads.length === 0) {
			errorMessage = 'Add at least one task title or description.';
			return;
		}

		isSaving = true;
		errorMessage = '';
		try {
			if (payloads.length === 1) {
				await addItem(payloads[0]);
			} else {
				await addItemsBulk(payloads);
			}
			closeComposer();
		} catch (error) {
			errorMessage = error instanceof Error ? error.message : 'Failed to create tasks';
		} finally {
			isSaving = false;
		}
	}

	async function handleToggleStatus(item: PersonalItem) {
		if (activeItemID) {
			return;
		}
		activeItemID = item.item_id;
		errorMessage = '';
		try {
			const nextStatus = isCompleted(item) ? 'pending' : 'completed';
			await updateStatus(item.item_id, nextStatus);
		} catch (error) {
			errorMessage = error instanceof Error ? error.message : 'Failed to update item status';
		} finally {
			activeItemID = '';
		}
	}

	async function handleDelete(item: PersonalItem) {
		if (activeItemID) {
			return;
		}
		activeItemID = item.item_id;
		errorMessage = '';
		try {
			await deleteItem(item.item_id);
		} catch (error) {
			errorMessage = error instanceof Error ? error.message : 'Failed to delete item';
		} finally {
			activeItemID = '';
		}
	}
</script>

<section class="personal-taskboard" class:menu-open={isAddMenuOpen} aria-label="Personal taskboard">
	<header class="board-header">
		<div>
			<h2>Personal Taskboard</h2>
			<p>Capture notes, schedule reminders, and plan tasks with context.</p>
		</div>
		<div class="header-actions">
			<button type="button" class="refresh-btn" on:click={loadItems} disabled={isLoading || isSaving}>
				Refresh
			</button>
			<div class="add-menu-wrap">
				<button type="button" class="add-btn" on:click={openAddMenu} aria-expanded={isAddMenuOpen}>
					+ Add
				</button>
				{#if isAddMenuOpen}
					<div class="add-menu" role="menu" aria-label="Add personal item">
						<button type="button" on:click={() => openComposer('note')}>Note</button>
						<button type="button" on:click={() => openComposer('reminder')}>Reminder</button>
						<button type="button" on:click={() => openComposer('tasks')}>Tasks</button>
					</div>
				{/if}
			</div>
		</div>
	</header>

	{#if addMode}
		<section class="composer kind-{addMode}">
			<header class="composer-header">
				<div>
					<h3>
						{#if addMode === 'note'}
							New Note
						{:else if addMode === 'reminder'}
							Schedule Reminder
						{:else}
							Create Tasks
						{/if}
					</h3>
					<p>Created at {formatCreationTime()}</p>
				</div>
				<button type="button" class="composer-close" on:click={closeComposer}>Close</button>
			</header>

			{#if addMode === 'note'}
				<div class="form-grid">
					<label>
						<span>Title</span>
						<input type="text" bind:value={noteTitle} placeholder="Design review notes" autocomplete="off" />
					</label>
					<label class="full-width">
						<span>Note text</span>
						<textarea
							bind:value={noteText}
							placeholder="Write what matters when you come back later..."
							rows="4"
						></textarea>
					</label>
				</div>
				<div class="composer-actions">
					<button
						type="button"
						class="primary"
						on:click={() => {
							void handleCreateNote();
						}}
						disabled={isSaving}
					>
						{isSaving ? 'Saving...' : 'Save Note'}
					</button>
				</div>
			{:else if addMode === 'reminder'}
				<div class="form-grid">
					<label>
						<span>Title</span>
						<input type="text" bind:value={reminderTitle} placeholder="Ship update reminder" autocomplete="off" />
					</label>
					<label class="full-width">
						<span>Reminder text</span>
						<textarea bind:value={reminderText} placeholder="Message to show when reminder fires" rows="3"></textarea>
					</label>
					<label>
						<span>Schedule</span>
						<input type="datetime-local" bind:value={reminderAt} />
					</label>
					<label>
						<span>Start from</span>
						<input type="datetime-local" bind:value={reminderStartAt} />
					</label>
					<label>
						<span>Repeat</span>
						<select bind:value={reminderRepeat}>
							{#each repeatOptions as option}
								<option value={option.value}>{option.label}</option>
							{/each}
						</select>
					</label>
				</div>
				<div class="composer-actions">
					<button
						type="button"
						class="primary"
						on:click={() => {
							void handleCreateReminder();
						}}
						disabled={isSaving}
					>
						{isSaving ? 'Scheduling...' : 'Schedule Reminder'}
					</button>
				</div>
			{:else}
				<div class="task-builder">
					<div class="task-builder-head">
						<span>{validTaskDraftCount} ready</span>
						<button type="button" class="ghost" on:click={addTaskDraft}>+ Row</button>
					</div>

					<div class="task-drafts">
						{#each taskDrafts as draft (draft.key)}
							<article class="task-draft-row">
								<label class="task-complete-toggle">
									<input type="checkbox" bind:checked={draft.done} />
									<span>Done</span>
								</label>
								<div class="task-draft-main">
									<input
										type="text"
										placeholder="Task title"
										bind:value={draft.title}
										autocomplete="off"
									/>
									<textarea
										rows="2"
										placeholder="Description (optional)"
										bind:value={draft.description}
									></textarea>
								</div>
								<div class="task-draft-dates">
									<label>
										<span>Start</span>
										<input type="datetime-local" bind:value={draft.startAt} />
									</label>
									<label>
										<span>End</span>
										<input type="datetime-local" bind:value={draft.endAt} />
									</label>
								</div>
								<button type="button" class="remove-row" on:click={() => removeTaskDraft(draft.key)}>
									Remove
								</button>
							</article>
						{/each}
					</div>
				</div>
				<div class="composer-actions">
					<button
						type="button"
						class="primary"
						on:click={() => {
							void handleCreateTasks();
						}}
						disabled={isSaving}
					>
						{isSaving ? 'Saving Tasks...' : 'Save Tasks'}
					</button>
				</div>
			{/if}
		</section>
	{/if}

	{#if errorMessage}
		<div class="error-banner">{errorMessage}</div>
	{/if}

	{#if isLoading}
		<div class="state-text">Loading personal items...</div>
	{:else if sortedItems.length === 0}
		<div class="state-text">No items yet. Use + Add to create your first note, reminder, or task.</div>
	{:else}
		<div class="items-grid">
			{#each sortedItems as item (item.item_id)}
				<article class="item-card {isCompleted(item) ? 'completed' : ''}">
					<div class="item-head">
						<span class="type-pill type-{item.type}">{typeLabel(item.type)}</span>
						<button
							type="button"
							class="delete-btn"
							on:click={() => {
								void handleDelete(item);
							}}
							disabled={activeItemID === item.item_id}
							aria-label="Delete personal item"
						>
							<svg viewBox="0 0 24 24" aria-hidden="true">
								<path
									d="M9 3h6l1 2h4v2H4V5h4l1-2Zm1 6h2v9h-2V9Zm4 0h2v9h-2V9ZM7 9h2v9H7V9Z"
									fill="currentColor"
								/>
							</svg>
						</button>
					</div>

					<label class="status-toggle">
						<input
							type="checkbox"
							checked={isCompleted(item)}
							on:change={() => {
								void handleToggleStatus(item);
							}}
							disabled={activeItemID === item.item_id}
						/>
						<div class="item-text">
							<strong>{displayTitle(item)}</strong>
							{#if displayBody(item)}
								<p>{displayBody(item)}</p>
							{/if}
						</div>
					</label>

					<div class="item-meta">
						<small>Created {formatDateTime(item.created_at) || 'Unknown'}</small>
						<small>{buildScheduleSummary(item)}</small>
					</div>
				</article>
			{/each}
		</div>
	{/if}
</section>

<style>
	:global(:root) {
		--personal-board-bg:
			radial-gradient(circle at 12% -10%, rgba(153, 188, 245, 0.22), transparent 40%),
			radial-gradient(circle at 92% 8%, rgba(176, 205, 246, 0.18), transparent 35%),
			rgba(255, 255, 255, 0.66);
		--personal-board-border: rgba(168, 193, 232, 0.56);
		--personal-board-shadow: 0 18px 46px rgba(95, 124, 171, 0.22);
		--personal-text: #121f3c;
		--personal-subtle: rgba(60, 79, 114, 0.78);
		--personal-btn-bg: rgba(255, 255, 255, 0.76);
		--personal-btn-border: rgba(106, 138, 193, 0.36);
		--personal-btn-text: #13284b;
		--personal-btn-hover-bg: rgba(232, 243, 255, 0.86);
		--personal-btn-hover-border: rgba(113, 148, 209, 0.54);
		--personal-add-btn-bg: linear-gradient(140deg, rgba(80, 130, 214, 0.2), rgba(55, 96, 168, 0.16));
		--personal-add-btn-border: rgba(87, 130, 206, 0.55);
		--personal-add-btn-text: #0f2d5e;
		--personal-menu-bg: rgba(255, 255, 255, 0.78);
		--personal-menu-border: rgba(170, 195, 234, 0.66);
		--personal-menu-hover: rgba(218, 232, 252, 0.58);
		--personal-input-bg: rgba(255, 255, 255, 0.7);
		--personal-input-border: rgba(136, 166, 216, 0.5);
		--personal-input-text: #10223f;
		--personal-input-placeholder: rgba(79, 102, 140, 0.62);
		--personal-item-bg: rgba(255, 255, 255, 0.66);
		--personal-item-border: rgba(166, 190, 228, 0.55);
		--personal-item-meta: rgba(70, 91, 127, 0.76);
		--personal-error-bg: rgba(220, 38, 38, 0.13);
		--personal-error-border: rgba(220, 38, 38, 0.36);
		--personal-error-text: #902338;
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--personal-board-bg:
			radial-gradient(circle at 12% -10%, rgba(255, 255, 255, 0.07), transparent 40%),
			radial-gradient(circle at 92% 8%, rgba(255, 255, 255, 0.04), transparent 35%),
			#0d0d12;
		--personal-board-border: rgba(255, 255, 255, 0.1);
		--personal-board-shadow: 0 18px 46px rgba(0, 0, 0, 0.38);
		--personal-text: #f3f6ff;
		--personal-subtle: rgba(214, 221, 243, 0.78);
		--personal-btn-bg: rgba(255, 255, 255, 0.08);
		--personal-btn-border: rgba(255, 255, 255, 0.17);
		--personal-btn-text: #f5f8ff;
		--personal-btn-hover-bg: rgba(255, 255, 255, 0.14);
		--personal-btn-hover-border: rgba(255, 255, 255, 0.28);
		--personal-add-btn-bg: linear-gradient(140deg, rgba(134, 175, 255, 0.16), rgba(74, 126, 228, 0.2));
		--personal-add-btn-border: rgba(164, 199, 255, 0.36);
		--personal-add-btn-text: #e7f0ff;
		--personal-menu-bg: rgba(21, 30, 45, 0.78);
		--personal-menu-border: rgba(145, 172, 214, 0.34);
		--personal-menu-hover: rgba(124, 161, 230, 0.2);
		--personal-input-bg: rgba(255, 255, 255, 0.04);
		--personal-input-border: rgba(255, 255, 255, 0.14);
		--personal-input-text: #f1f5ff;
		--personal-input-placeholder: rgba(196, 206, 233, 0.56);
		--personal-item-bg: rgba(255, 255, 255, 0.04);
		--personal-item-border: rgba(255, 255, 255, 0.1);
		--personal-item-meta: rgba(201, 210, 233, 0.78);
		--personal-error-bg: rgba(220, 38, 38, 0.22);
		--personal-error-border: rgba(248, 113, 113, 0.42);
		--personal-error-text: #ffd4dc;
	}

	.personal-taskboard {
		position: relative;
		z-index: 0;
		display: grid;
		gap: 0.9rem;
		padding: 1rem;
		border-radius: 18px;
		border: 1px solid var(--personal-board-border);
		background: var(--personal-board-bg);
		color: var(--personal-text);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
		box-shadow: var(--personal-board-shadow);
		overflow: visible;
	}

	.personal-taskboard.menu-open {
		z-index: 32;
	}

	.board-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 0.8rem;
	}

	.board-header h2 {
		margin: 0;
		font-size: 1.02rem;
		letter-spacing: 0.01em;
	}

	.board-header p {
		margin: 0.2rem 0 0;
		font-size: 0.82rem;
		color: var(--personal-subtle);
	}

	.header-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
	}

	.refresh-btn,
	.add-btn,
	.composer-close,
	.delete-btn,
	.composer-actions button,
	.task-builder-head .ghost,
	.remove-row {
		border: 1px solid var(--personal-btn-border);
		background: var(--personal-btn-bg);
		color: var(--personal-btn-text);
		border-radius: 10px;
		cursor: pointer;
		transition:
			background 0.2s ease,
			border-color 0.2s ease,
			transform 0.15s ease;
	}

	.refresh-btn {
		padding: 0.44rem 0.74rem;
		font-size: 0.78rem;
	}

	.add-btn {
		padding: 0.44rem 0.78rem;
		background: var(--personal-add-btn-bg);
		border-color: var(--personal-add-btn-border);
		color: var(--personal-add-btn-text);
		font-size: 0.8rem;
		font-weight: 600;
	}

	.add-menu-wrap {
		position: relative;
	}

	.add-menu {
		position: absolute;
		top: calc(100% + 6px);
		right: 0;
		min-width: 140px;
		padding: 0.4rem;
		border-radius: 12px;
		border: 1px solid var(--personal-menu-border);
		background: var(--personal-menu-bg);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
		display: grid;
		gap: 0.2rem;
		z-index: 120;
	}

	.add-menu button {
		border: 0;
		background: transparent;
		color: var(--personal-text);
		padding: 0.5rem 0.56rem;
		text-align: left;
		border-radius: 8px;
		cursor: pointer;
		font-size: 0.84rem;
	}

	.add-menu button:hover {
		background: var(--personal-menu-hover);
	}

	.composer {
		border: 1px solid var(--personal-item-border);
		background: var(--personal-item-bg);
		border-radius: 14px;
		padding: 0.8rem;
		display: grid;
		gap: 0.75rem;
	}

	.composer-header {
		display: flex;
		justify-content: space-between;
		gap: 0.75rem;
		align-items: flex-start;
	}

	.composer-header h3 {
		margin: 0;
		font-size: 0.95rem;
	}

	.composer-header p {
		margin: 0.2rem 0 0;
		font-size: 0.75rem;
		color: var(--personal-subtle);
	}

	.composer-close {
		padding: 0.35rem 0.66rem;
		font-size: 0.74rem;
	}

	.form-grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.62rem;
	}

	.form-grid label,
	.task-draft-dates label {
		display: grid;
		gap: 0.36rem;
	}

	.form-grid label span,
	.task-draft-dates label span {
		font-size: 0.71rem;
		text-transform: uppercase;
		letter-spacing: 0.07em;
		color: var(--personal-subtle);
	}

	.full-width {
		grid-column: 1 / -1;
	}

	.form-grid input,
	.form-grid textarea,
	.form-grid select,
	.task-draft-main input,
	.task-draft-main textarea,
	.task-draft-dates input {
		width: 100%;
		box-sizing: border-box;
		border-radius: 10px;
		border: 1px solid var(--personal-input-border);
		background: var(--personal-input-bg);
		color: var(--personal-input-text);
		padding: 0.56rem 0.68rem;
		font: inherit;
	}

	.form-grid input::placeholder,
	.form-grid textarea::placeholder,
	.task-draft-main input::placeholder,
	.task-draft-main textarea::placeholder {
		color: var(--personal-input-placeholder);
	}

	.form-grid textarea,
	.task-draft-main textarea {
		resize: vertical;
		min-height: 78px;
	}

	.composer-actions {
		display: flex;
		justify-content: flex-end;
	}

	.composer-actions .primary {
		padding: 0.56rem 0.9rem;
		font-size: 0.8rem;
		font-weight: 600;
	}

	.task-builder {
		display: grid;
		gap: 0.58rem;
	}

	.task-builder-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
		font-size: 0.8rem;
		color: var(--personal-subtle);
	}

	.task-builder-head .ghost {
		padding: 0.4rem 0.7rem;
		font-size: 0.74rem;
	}

	.task-drafts {
		display: grid;
		gap: 0.52rem;
		max-height: min(44vh, 360px);
		overflow: auto;
		padding-right: 0.1rem;
	}

	.task-draft-row {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr) minmax(190px, 220px) auto;
		gap: 0.54rem;
		align-items: start;
		border: 1px solid var(--personal-item-border);
		background: var(--personal-item-bg);
		border-radius: 12px;
		padding: 0.58rem;
	}

	.task-complete-toggle {
		display: inline-flex;
		align-items: center;
		gap: 0.36rem;
		font-size: 0.72rem;
		color: var(--personal-subtle);
		padding-top: 0.36rem;
	}

	.task-complete-toggle input {
		accent-color: #6ea8ff;
	}

	.task-draft-main {
		display: grid;
		gap: 0.4rem;
	}

	.task-draft-main textarea {
		min-height: 60px;
	}

	.task-draft-dates {
		display: grid;
		gap: 0.42rem;
	}

	.remove-row {
		padding: 0.45rem 0.62rem;
		font-size: 0.72rem;
	}

	.error-banner {
		color: var(--personal-error-text);
		background: var(--personal-error-bg);
		border: 1px solid var(--personal-error-border);
		padding: 0.55rem 0.66rem;
		border-radius: 10px;
		font-size: 0.82rem;
	}

	.state-text {
		font-size: 0.84rem;
		color: var(--personal-subtle);
	}

	.items-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
		gap: 0.62rem;
	}

	.item-card {
		border: 1px solid var(--personal-item-border);
		background: var(--personal-item-bg);
		border-radius: 14px;
		padding: 0.68rem;
		display: grid;
		gap: 0.54rem;
		backdrop-filter: blur(14px);
		-webkit-backdrop-filter: blur(14px);
	}

	.item-card.completed {
		opacity: 0.82;
	}

	.item-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	.type-pill {
		font-size: 0.65rem;
		padding: 0.16rem 0.48rem;
		border-radius: 999px;
		text-transform: uppercase;
		letter-spacing: 0.08em;
		font-family: 'JetBrains Mono', monospace;
		border: 1px solid var(--personal-btn-border);
	}

	.type-pill.type-note {
		background: rgba(85, 123, 193, 0.14);
	}

	.type-pill.type-reminder {
		background: rgba(180, 110, 42, 0.2);
	}

	.type-pill.type-task {
		background: rgba(29, 156, 116, 0.18);
	}

	.status-toggle {
		display: flex;
		align-items: flex-start;
		gap: 0.55rem;
	}

	.status-toggle input {
		margin-top: 0.2rem;
		accent-color: #6ea8ff;
	}

	.item-text strong {
		display: block;
		font-size: 0.9rem;
		line-height: 1.34;
	}

	.item-card.completed .item-text strong {
		text-decoration: line-through;
	}

	.item-text p {
		margin: 0.2rem 0 0;
		font-size: 0.8rem;
		line-height: 1.38;
		color: var(--personal-subtle);
		white-space: pre-wrap;
		word-break: break-word;
	}

	.item-meta {
		display: grid;
		gap: 0.2rem;
	}

	.item-meta small {
		font-size: 0.7rem;
		color: var(--personal-item-meta);
	}

	.delete-btn {
		width: 30px;
		height: 30px;
		padding: 0;
		display: inline-flex;
		align-items: center;
		justify-content: center;
	}

	.delete-btn svg {
		width: 15px;
		height: 15px;
	}

	.refresh-btn:hover:not(:disabled),
	.add-btn:hover:not(:disabled),
	.composer-close:hover:not(:disabled),
	.delete-btn:hover:not(:disabled),
	.composer-actions button:hover:not(:disabled),
	.task-builder-head .ghost:hover:not(:disabled),
	.remove-row:hover:not(:disabled) {
		background: var(--personal-btn-hover-bg);
		border-color: var(--personal-btn-hover-border);
		transform: translateY(-1px);
	}

	button:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	@media (max-width: 900px) {
		.form-grid {
			grid-template-columns: 1fr;
		}

		.task-draft-row {
			grid-template-columns: 1fr;
		}

		.task-complete-toggle {
			padding-top: 0;
		}

		.remove-row {
			justify-self: start;
		}
	}

	@media (max-width: 640px) {
		.board-header {
			flex-direction: column;
			align-items: stretch;
		}

		.header-actions {
			width: 100%;
			justify-content: space-between;
		}
	}
</style>
