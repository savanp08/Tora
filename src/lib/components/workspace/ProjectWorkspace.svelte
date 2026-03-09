<script lang="ts">
	import ProjectOnboarding from '$lib/components/workspace/ProjectOnboarding.svelte';
	import TaskBoard from '$lib/components/workspace/TaskBoard.svelte';
	import TimelineBoard from '$lib/components/workspace/TimelineBoard.svelte';
	import { currentUser } from '$lib/store';
	import {
		activeProjectTab,
		generateAITimeline,
		initializeProjectTimelineForRoom,
		isProjectNew,
		projectTimeline,
		setProjectTimeline,
		timelineError,
		timelineLoading,
		type ProjectTab
	} from '$lib/stores/timeline';
	import { initializeTaskStoreForRoom } from '$lib/stores/tasks';
	import { normalizeRoomIDValue } from '$lib/utils/chat/core';

	export let roomId = '';
	export let canEdit = true;

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';

	type WorkspaceTabMeta = {
		key: ProjectTab;
		label: string;
		icon: string;
	};

	type ToraMessage = {
		role: 'user' | 'assistant';
		text: string;
		timestamp: number;
	};

	const WORKSPACE_TABS: WorkspaceTabMeta[] = [
		{ key: 'overview', label: 'Overview', icon: 'M4.5 4.5h6.8v6.8H4.5zM12.7 4.5h6.8V9h-6.8zM12.7 10.7h6.8v8.8h-6.8zM4.5 12.7h6.8v6.8H4.5z' },
		{ key: 'tasks', label: 'Tasks', icon: 'M8 7h11M8 12h11M8 17h11M4.5 7h.01M4.5 12h.01M4.5 17h.01' },
		{ key: 'progress', label: 'Progress', icon: 'M5 18.5h14M7.5 16V9.5M12 16V6.5M16.5 16v-4.2' },
		{ key: 'visualizations', label: 'Visuals', icon: 'M5 17.5V12l3.8-3.8 3 3L17.5 5l1.5 1.5M19.5 19.5h-15v-15' },
		{ key: 'tora_ai', label: 'Tora AI', icon: 'M12 4.2 13.7 8l3.8 1.5-3.8 1.5L12 14.8 10.3 11 6.5 9.5 10.3 8 12 4.2Z' }
	];

	const TASK_LABELS = ['backend', 'frontend', 'qa', 'design', 'strategy', 'planning'];

	let selectedSprintFilter = '';
	let toraPrompt = '';
	let toraMessages: ToraMessage[] = [];
	let lastWorkspaceRoomID = '';
	let workspaceLoadToken = 0;
	let clearingTaskboard = false;

	$: sessionUserID = ($currentUser?.id || '').trim();
	$: normalizedWorkspaceRoomID = normalizeRoomIDValue(roomId);
	$: if (normalizedWorkspaceRoomID !== lastWorkspaceRoomID) {
		lastWorkspaceRoomID = normalizedWorkspaceRoomID;
		activeProjectTab.set('overview');
		selectedSprintFilter = '';
		toraPrompt = '';
		toraMessages = [];
		void hydrateWorkspaceForRoom(normalizedWorkspaceRoomID);
	}

	$: timeline = $projectTimeline;
	$: sprints = timeline?.sprints ?? [];
	$: if (sprints.length > 0 && !sprints.some((sprint) => sprint.id === selectedSprintFilter)) {
		selectedSprintFilter = sprints[0].id;
	}
	$: allTasks = sprints.flatMap((sprint) => sprint.tasks);
	$: totalTasks = allTasks.length;
	$: completedTasks = allTasks.filter((task) => task.status === 'done').length;
	$: inProgressTasks = allTasks.filter((task) => task.status === 'in_progress').length;
	$: todoTasks = allTasks.filter((task) => task.status === 'todo').length;
	$: completionRate = totalTasks > 0 ? Math.round((completedTasks / totalTasks) * 100) : 0;
	$: activeSprint = sprints.find((sprint) => sprint.id === selectedSprintFilter) ?? null;
	$: typeDistribution = TASK_LABELS.map((label) => ({
		label,
		count: allTasks.filter((task) => (task.type || '').toLowerCase() === label).length
	})).filter((entry) => entry.count > 0);
	$: maxTypeCount = typeDistribution.reduce((max, entry) => Math.max(max, entry.count), 0);

	function activateTab(tab: ProjectTab) {
		activeProjectTab.set(tab);
	}

	function jumpToTasks() {
		activeProjectTab.set('tasks');
	}

	async function parseWorkspaceError(response: Response) {
		const payload = (await response.json().catch(() => null)) as
			| {
					error?: string;
					message?: string;
			  }
			| null;
		return payload?.error?.trim() || payload?.message?.trim() || `HTTP ${response.status}`;
	}

	async function hydrateWorkspaceForRoom(normalizedRoomID: string) {
		workspaceLoadToken += 1;
		const loadToken = workspaceLoadToken;

		if (!normalizedRoomID) {
			setProjectTimeline(null);
			isProjectNew.set(true);
			timelineError.set('');
			return;
		}

		await Promise.all([
			initializeProjectTimelineForRoom(normalizedRoomID, {
				apiBase: API_BASE
			}),
			initializeTaskStoreForRoom(normalizedRoomID, {
				apiBase: API_BASE
			})
		]);

		if (loadToken !== workspaceLoadToken) {
			return;
		}
	}

	async function clearTaskboard() {
		if ($isProjectNew || !$projectTimeline) {
			return;
		}
		const shouldReset = window.confirm(
			'Clear this taskboard and return to setup? This removes all tasks for this room.'
		);
		if (!shouldReset) {
			return;
		}

		if (!normalizedWorkspaceRoomID) {
			setProjectTimeline(null);
			timelineError.set('');
			return;
		}

		clearingTaskboard = true;
		timelineError.set('');
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedWorkspaceRoomID)}/tasks`,
				{
					method: 'DELETE',
					credentials: 'include',
					headers: sessionUserID ? { 'X-User-Id': sessionUserID } : undefined
				}
			);
			if (!response.ok) {
				throw new Error(await parseWorkspaceError(response));
			}

			await Promise.all([
				initializeProjectTimelineForRoom(normalizedWorkspaceRoomID, {
					apiBase: API_BASE
				}),
				initializeTaskStoreForRoom(normalizedWorkspaceRoomID, {
					apiBase: API_BASE
				})
			]);

			activeProjectTab.set('overview');
			selectedSprintFilter = '';
			toraPrompt = '';
			toraMessages = [];
		} catch (error) {
			timelineError.set(error instanceof Error ? error.message : 'Failed to clear room taskboard');
		} finally {
			clearingTaskboard = false;
		}
	}

	function formatRange(startDate: string, endDate: string) {
		if (!startDate && !endDate) {
			return 'No date range';
		}
		if (startDate && endDate) {
			return `${startDate} -> ${endDate}`;
		}
		return startDate || endDate;
	}

	async function askToraToModifyBoard() {
		const normalizedPrompt = toraPrompt.trim();
		if (!normalizedPrompt) {
			return;
		}

		toraMessages = [
			...toraMessages,
			{ role: 'user', text: normalizedPrompt, timestamp: Date.now() }
		];

		const normalizedRoomID = roomId.trim();
		if (!normalizedRoomID) {
			toraMessages = [
				...toraMessages,
				{
					role: 'assistant',
					text: 'Room id is missing, so I cannot regenerate this board yet.',
					timestamp: Date.now()
				}
			];
			toraPrompt = '';
			return;
		}

		try {
			await generateAITimeline(normalizedRoomID, normalizedPrompt);
			await initializeTaskStoreForRoom(normalizeRoomIDValue(normalizedRoomID), {
				apiBase: API_BASE
			});
			isProjectNew.set(false);
			toraMessages = [
				...toraMessages,
				{
					role: 'assistant',
					text: 'Workspace updated from your prompt. Review the refreshed plan in Overview.',
					timestamp: Date.now()
				}
			];
			activeProjectTab.set('overview');
		} catch (error) {
			toraMessages = [
				...toraMessages,
				{
					role: 'assistant',
					text: error instanceof Error ? error.message : 'Failed to modify workspace.',
					timestamp: Date.now()
				}
			];
		} finally {
			toraPrompt = '';
		}
	}
</script>

<section class="project-workspace-shell" aria-label="Project workspace">
	<nav class="activity-bar" aria-label="Workspace navigation">
		{#each WORKSPACE_TABS as tab (tab.key)}
			<button
				type="button"
				class="activity-btn"
				class:is-active={$activeProjectTab === tab.key}
				on:click={() => activateTab(tab.key)}
				title={tab.label}
				aria-label={tab.label}
			>
				<svg viewBox="0 0 24 24" aria-hidden="true">
					<path d={tab.icon}></path>
				</svg>
			</button>
		{/each}
		<span class="activity-spacer" aria-hidden="true"></span>
		<button
			type="button"
			class="activity-btn clear-taskboard-btn"
			on:click={() => {
				void clearTaskboard();
			}}
			disabled={clearingTaskboard || $isProjectNew || !$projectTimeline}
			title="Clear Taskboard"
			aria-label="Clear Taskboard"
		>
			<svg viewBox="0 0 24 24" aria-hidden="true">
				<path d="M5 6h14M9 6V4.8a1.3 1.3 0 0 1 1.3-1.3h3.4A1.3 1.3 0 0 1 15 4.8V6"></path>
				<path d="m8 6 .7 12.2a1.6 1.6 0 0 0 1.6 1.5h3.4a1.6 1.6 0 0 0 1.6-1.5L16 6"></path>
				<path d="M10.2 10.1v6.1M13.8 10.1v6.1"></path>
			</svg>
		</button>
	</nav>

	<aside class="secondary-sidebar">
		{#if $activeProjectTab === 'overview'}
			<section class="sidebar-section">
				<h3>Project Overview</h3>
				{#if timeline}
					<div class="sidebar-meta-list">
						<div class="meta-row">
							<span>Name</span>
							<strong>{timeline.project_name}</strong>
						</div>
						<div class="meta-row">
							<span>Sprints</span>
							<strong>{sprints.length}</strong>
						</div>
						<div class="meta-row">
							<span>Total Tasks</span>
							<strong>{totalTasks}</strong>
						</div>
						<div class="meta-row">
							<span>Completion</span>
							<strong>{completionRate}%</strong>
						</div>
					</div>
				{:else}
					<p class="sidebar-empty">Generate or select a template to initialize this workspace.</p>
				{/if}
			</section>
		{:else if $activeProjectTab === 'tasks'}
			<section class="sidebar-section">
				<h3>Task Controls</h3>
				<button type="button" class="sidebar-cta-btn" on:click={jumpToTasks}>Add Task</button>
				<h4>Sprint Filters</h4>
				{#if sprints.length === 0}
					<p class="sidebar-empty">No sprints available yet.</p>
				{:else}
					<div class="sidebar-chip-list">
						{#each sprints as sprint (sprint.id)}
							<button
								type="button"
								class="chip-btn"
								class:is-active={selectedSprintFilter === sprint.id}
								on:click={() => {
									selectedSprintFilter = sprint.id;
								}}
							>
								{sprint.name}
							</button>
						{/each}
					</div>
				{/if}
				<h4>Labels</h4>
				<div class="sidebar-chip-list labels">
					{#each TASK_LABELS as label (label)}
						<span class="label-chip">{label}</span>
					{/each}
				</div>
			</section>
		{:else if $activeProjectTab === 'tora_ai'}
			<section class="sidebar-section">
				<h3>Tora AI</h3>
				<p class="sidebar-empty">
					Ask Tora to reshape timeline structure, sprint order, or task grouping.
				</p>
				<div class="tora-chat-preview">
					{#if toraMessages.length === 0}
						<div class="sidebar-empty">No AI requests yet.</div>
					{:else}
						{#each toraMessages.slice(-6) as message (`${message.timestamp}-${message.role}`)}
							<div class="tora-chip {message.role}">{message.text}</div>
						{/each}
					{/if}
				</div>
			</section>
		{:else if $activeProjectTab === 'progress'}
			<section class="sidebar-section">
				<h3>Progress Summary</h3>
				<div class="sidebar-meta-list">
					<div class="meta-row">
						<span>Done</span>
						<strong>{completedTasks}</strong>
					</div>
					<div class="meta-row">
						<span>In Progress</span>
						<strong>{inProgressTasks}</strong>
					</div>
					<div class="meta-row">
						<span>To Do</span>
						<strong>{todoTasks}</strong>
					</div>
				</div>
			</section>
		{:else}
			<section class="sidebar-section">
				<h3>Visualizations</h3>
				<p class="sidebar-empty">Inspect type distribution and sprint load visuals.</p>
			</section>
		{/if}
	</aside>

	<main class="workspace-canvas">
		{#if $timelineLoading && !$projectTimeline}
			<section class="canvas-panel">
				<div class="empty-viz">Loading taskboard...</div>
			</section>
		{:else if $isProjectNew || !$projectTimeline}
			<ProjectOnboarding {roomId} />
		{:else if $activeProjectTab === 'overview'}
			<TimelineBoard />
		{:else if $activeProjectTab === 'tasks'}
			<TaskBoard {roomId} {canEdit} />
		{:else if $activeProjectTab === 'progress'}
			<section class="canvas-panel">
				<header class="canvas-head">
					<h2>Progress</h2>
					<span>{completionRate}% complete</span>
				</header>
				<div class="progress-stats">
					<div class="stat-card done">
						<span>Done</span>
						<strong>{completedTasks}</strong>
					</div>
					<div class="stat-card active">
						<span>In Progress</span>
						<strong>{inProgressTasks}</strong>
					</div>
					<div class="stat-card todo">
						<span>To Do</span>
						<strong>{todoTasks}</strong>
					</div>
				</div>
				<div class="sprint-progress-list">
					{#each sprints as sprint (sprint.id)}
						{@const sprintTotal = sprint.tasks.length}
						{@const sprintDone = sprint.tasks.filter((task) => task.status === 'done').length}
						{@const sprintPercent = sprintTotal > 0 ? Math.round((sprintDone / sprintTotal) * 100) : 0}
						<article class="sprint-progress-card">
							<div class="sprint-progress-head">
								<strong>{sprint.name}</strong>
								<span>{sprintPercent}%</span>
							</div>
							<div class="sprint-progress-track">
								<div class="sprint-progress-fill" style={`width:${sprintPercent}%;`}></div>
							</div>
							<small>{formatRange(sprint.start_date, sprint.end_date)}</small>
						</article>
					{/each}
				</div>
			</section>
		{:else if $activeProjectTab === 'visualizations'}
			<section class="canvas-panel">
				<header class="canvas-head">
					<h2>Visualizations</h2>
					<span>{totalTasks} tasks tracked</span>
				</header>

				<div class="viz-grid">
					<section class="viz-card">
						<h3>Type Distribution</h3>
						{#if typeDistribution.length === 0}
							<div class="empty-viz">No labeled tasks yet.</div>
						{:else}
							<div class="bar-list">
								{#each typeDistribution as entry (entry.label)}
									<div class="bar-row">
										<div class="bar-meta">
											<span>{entry.label}</span>
											<strong>{entry.count}</strong>
										</div>
										<div class="bar-track">
											<div
												class="bar-fill"
												style={`width:${maxTypeCount > 0 ? Math.max(10, (entry.count / maxTypeCount) * 100) : 0}%;`}
											></div>
										</div>
									</div>
								{/each}
							</div>
						{/if}
					</section>

					<section class="viz-card">
						<h3>Sprint Snapshot</h3>
						{#if activeSprint}
							<div class="sprint-snapshot">
								<strong>{activeSprint.name}</strong>
								<small>{formatRange(activeSprint.start_date, activeSprint.end_date)}</small>
								<div class="snapshot-kpis">
									<span>Tasks: {activeSprint.tasks.length}</span>
									<span>Done: {activeSprint.tasks.filter((task) => task.status === 'done').length}</span>
								</div>
							</div>
						{:else}
							<div class="empty-viz">Select a sprint from the Tasks sidebar.</div>
						{/if}
					</section>
				</div>
			</section>
		{:else}
			<section class="canvas-panel tora-panel">
				<header class="canvas-head">
					<h2>Tora AI Console</h2>
					<span>{$timelineLoading ? 'Working...' : 'Ready'}</span>
				</header>
				<div class="tora-thread">
					{#if toraMessages.length === 0}
						<div class="empty-viz">Ask Tora to modify your board and it will regenerate the plan.</div>
					{:else}
						{#each toraMessages as message (`${message.timestamp}-${message.role}`)}
							<article class="tora-message {message.role}">
								<span>{message.role === 'user' ? 'You' : 'Tora'}</span>
								<p>{message.text}</p>
							</article>
						{/each}
					{/if}
				</div>
				<div class="tora-compose">
					<textarea
						bind:value={toraPrompt}
						placeholder="Example: Split this into 4 sprints with QA in each sprint."
						rows="3"
					></textarea>
					<button
						type="button"
						on:click={() => {
							void askToraToModifyBoard();
						}}
						disabled={$timelineLoading || !toraPrompt.trim()}
					>
						{$timelineLoading ? 'Applying...' : 'Apply With Tora'}
					</button>
				</div>
			</section>
		{/if}
	</main>
</section>

<style>
	.project-workspace-shell {
		height: 100%;
		width: 100%;
		min-height: 0;
		display: flex;
		background: #0d0d12;
		color: #eef4ff;
		overflow: hidden;
	}

	.activity-bar {
		width: 60px;
		flex: 0 0 60px;
		border-right: 1px solid rgba(255, 255, 255, 0.1);
		background: rgba(255, 255, 255, 0.02);
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 0.34rem;
		padding: 0.6rem 0.35rem;
	}

	.activity-spacer {
		flex: 1;
		min-height: 0.3rem;
	}

	.activity-btn {
		width: 44px;
		height: 44px;
		border: 1px solid rgba(255, 255, 255, 0.12);
		border-left: 2px solid transparent;
		border-radius: 12px;
		background: rgba(255, 255, 255, 0.03);
		color: rgba(219, 228, 248, 0.74);
		display: grid;
		place-items: center;
		cursor: pointer;
		transition:
			color 0.16s ease,
			border-color 0.16s ease,
			background 0.16s ease;
	}

	.activity-btn svg {
		width: 1rem;
		height: 1rem;
		stroke: currentColor;
		stroke-width: 1.75;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.activity-btn:hover {
		color: #f2f7ff;
		border-color: rgba(162, 197, 255, 0.58);
	}

	.activity-btn:disabled {
		opacity: 0.46;
		cursor: not-allowed;
	}

	.activity-btn:disabled:hover {
		color: rgba(219, 228, 248, 0.74);
		border-color: rgba(255, 255, 255, 0.12);
	}

	.activity-btn.is-active {
		color: #cce0ff;
		border-left-color: rgba(142, 188, 255, 0.96);
		border-color: rgba(142, 188, 255, 0.72);
		background: rgba(128, 179, 255, 0.14);
	}

	.clear-taskboard-btn {
		color: rgba(255, 174, 174, 0.9);
		border-color: rgba(255, 132, 132, 0.34);
		background: rgba(255, 72, 72, 0.14);
	}

	.clear-taskboard-btn:hover:not(:disabled) {
		color: #ffe6e6;
		border-color: rgba(255, 146, 146, 0.78);
		background: rgba(255, 92, 92, 0.24);
	}

	.secondary-sidebar {
		width: 250px;
		flex: 0 0 250px;
		border-right: 1px solid rgba(255, 255, 255, 0.1);
		background: rgba(255, 255, 255, 0.03);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
		padding: 0.82rem;
		overflow: auto;
	}

	.sidebar-section {
		display: grid;
		gap: 0.64rem;
	}

	.sidebar-section h3 {
		margin: 0;
		font-size: 0.84rem;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: rgba(232, 239, 255, 0.9);
	}

	.sidebar-section h4 {
		margin: 0.5rem 0 0;
		font-size: 0.72rem;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: rgba(177, 193, 225, 0.84);
	}

	.sidebar-empty {
		margin: 0;
		font-size: 0.78rem;
		line-height: 1.4;
		color: rgba(187, 198, 224, 0.74);
	}

	.sidebar-meta-list {
		display: grid;
		gap: 0.36rem;
	}

	.meta-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		border: 1px solid rgba(255, 255, 255, 0.1);
		border-radius: 10px;
		background: rgba(255, 255, 255, 0.03);
		padding: 0.5rem 0.6rem;
	}

	.meta-row span {
		font-size: 0.73rem;
		color: rgba(182, 194, 221, 0.8);
	}

	.meta-row strong {
		font-size: 0.76rem;
	}

	.sidebar-cta-btn,
	.chip-btn,
	.label-chip {
		border-radius: 9px;
		border: 1px solid rgba(255, 255, 255, 0.14);
		background: rgba(255, 255, 255, 0.06);
		color: #edf4ff;
		font-size: 0.75rem;
		padding: 0.42rem 0.52rem;
	}

	.sidebar-cta-btn,
	.chip-btn {
		cursor: pointer;
	}

	.sidebar-cta-btn:hover,
	.chip-btn:hover {
		border-color: rgba(174, 206, 255, 0.6);
		background: rgba(255, 255, 255, 0.12);
	}

	.sidebar-chip-list {
		display: flex;
		flex-wrap: wrap;
		gap: 0.34rem;
	}

	.chip-btn.is-active {
		border-color: rgba(150, 192, 255, 0.78);
		background: rgba(108, 164, 255, 0.24);
	}

	.label-chip {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		text-transform: lowercase;
		color: rgba(212, 224, 250, 0.9);
	}

	.tora-chat-preview {
		display: grid;
		gap: 0.34rem;
		max-height: 260px;
		overflow: auto;
	}

	.tora-chip {
		font-size: 0.73rem;
		line-height: 1.35;
		padding: 0.44rem 0.52rem;
		border-radius: 9px;
		border: 1px solid rgba(255, 255, 255, 0.12);
		background: rgba(255, 255, 255, 0.05);
		color: rgba(228, 236, 255, 0.9);
	}

	.tora-chip.user {
		border-color: rgba(157, 197, 255, 0.56);
		background: rgba(95, 155, 252, 0.2);
	}

	.workspace-canvas {
		flex: 1;
		min-width: 0;
		min-height: 0;
		overflow: hidden;
	}

	.canvas-panel {
		height: 100%;
		min-height: 0;
		padding: 0.95rem;
		display: grid;
		grid-template-rows: auto auto 1fr;
		gap: 0.8rem;
		background:
			radial-gradient(circle at 14% -10%, rgba(255, 255, 255, 0.06), transparent 30%),
			#0d0d12;
	}

	.canvas-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.65rem;
		padding: 0.7rem 0.85rem;
		border: 1px solid rgba(255, 255, 255, 0.1);
		border-radius: 14px;
		background: rgba(255, 255, 255, 0.03);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
	}

	.canvas-head h2 {
		margin: 0;
		font-size: 0.96rem;
		letter-spacing: 0.03em;
	}

	.canvas-head span {
		font-size: 0.76rem;
		color: rgba(186, 199, 227, 0.84);
	}

	.progress-stats {
		display: grid;
		grid-template-columns: repeat(3, minmax(0, 1fr));
		gap: 0.6rem;
	}

	.stat-card {
		border-radius: 12px;
		border: 1px solid rgba(255, 255, 255, 0.1);
		background: rgba(255, 255, 255, 0.04);
		padding: 0.64rem 0.7rem;
		display: grid;
		gap: 0.2rem;
	}

	.stat-card span {
		font-size: 0.72rem;
		color: rgba(183, 197, 228, 0.78);
	}

	.stat-card strong {
		font-size: 1.15rem;
	}

	.stat-card.done {
		border-color: rgba(125, 215, 165, 0.4);
	}

	.stat-card.active {
		border-color: rgba(123, 182, 255, 0.45);
	}

	.stat-card.todo {
		border-color: rgba(255, 255, 255, 0.2);
	}

	.sprint-progress-list {
		min-height: 0;
		overflow: auto;
		display: grid;
		gap: 0.54rem;
	}

	.sprint-progress-card {
		border: 1px solid rgba(255, 255, 255, 0.1);
		border-radius: 12px;
		background: rgba(255, 255, 255, 0.03);
		padding: 0.62rem 0.7rem;
		display: grid;
		gap: 0.38rem;
	}

	.sprint-progress-head {
		display: flex;
		justify-content: space-between;
		gap: 0.5rem;
		font-size: 0.78rem;
	}

	.sprint-progress-track {
		height: 8px;
		border-radius: 999px;
		background: rgba(255, 255, 255, 0.08);
		overflow: hidden;
	}

	.sprint-progress-fill {
		height: 100%;
		background: linear-gradient(90deg, rgba(129, 185, 255, 0.95), rgba(102, 154, 232, 0.95));
	}

	.sprint-progress-card small {
		color: rgba(175, 190, 220, 0.78);
	}

	.viz-grid {
		min-height: 0;
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.66rem;
	}

	.viz-card {
		border: 1px solid rgba(255, 255, 255, 0.1);
		border-radius: 12px;
		background: rgba(255, 255, 255, 0.03);
		padding: 0.65rem;
		display: grid;
		gap: 0.5rem;
	}

	.viz-card h3 {
		margin: 0;
		font-size: 0.8rem;
		letter-spacing: 0.04em;
		text-transform: uppercase;
	}

	.empty-viz {
		font-size: 0.78rem;
		color: rgba(180, 194, 224, 0.76);
		padding: 0.55rem;
		border: 1px dashed rgba(255, 255, 255, 0.16);
		border-radius: 10px;
	}

	.bar-list {
		display: grid;
		gap: 0.45rem;
	}

	.bar-row {
		display: grid;
		gap: 0.22rem;
	}

	.bar-meta {
		display: flex;
		justify-content: space-between;
		gap: 0.4rem;
		font-size: 0.75rem;
	}

	.bar-track {
		height: 8px;
		border-radius: 999px;
		background: rgba(255, 255, 255, 0.08);
		overflow: hidden;
	}

	.bar-fill {
		height: 100%;
		background: linear-gradient(90deg, rgba(163, 203, 255, 0.95), rgba(113, 167, 249, 0.95));
	}

	.sprint-snapshot {
		border: 1px solid rgba(255, 255, 255, 0.12);
		border-radius: 10px;
		background: rgba(255, 255, 255, 0.04);
		padding: 0.58rem 0.62rem;
		display: grid;
		gap: 0.34rem;
	}

	.sprint-snapshot small {
		color: rgba(175, 190, 220, 0.78);
	}

	.snapshot-kpis {
		display: flex;
		gap: 0.46rem;
		flex-wrap: wrap;
		font-size: 0.74rem;
		color: rgba(197, 208, 232, 0.84);
	}

	.tora-panel {
		grid-template-rows: auto 1fr auto;
	}

	.tora-thread {
		min-height: 0;
		overflow: auto;
		display: grid;
		gap: 0.45rem;
		align-content: start;
	}

	.tora-message {
		border: 1px solid rgba(255, 255, 255, 0.12);
		border-radius: 11px;
		background: rgba(255, 255, 255, 0.04);
		padding: 0.5rem 0.56rem;
		display: grid;
		gap: 0.2rem;
	}

	.tora-message span {
		font-size: 0.68rem;
		font-weight: 700;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: rgba(184, 197, 225, 0.82);
	}

	.tora-message p {
		margin: 0;
		font-size: 0.78rem;
		line-height: 1.4;
		color: rgba(228, 235, 252, 0.94);
	}

	.tora-message.user {
		border-color: rgba(150, 194, 255, 0.56);
		background: rgba(104, 164, 252, 0.2);
	}

	.tora-compose {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: 0.55rem;
		align-items: end;
	}

	.tora-compose textarea {
		border: 1px solid rgba(255, 255, 255, 0.14);
		border-radius: 11px;
		background: rgba(255, 255, 255, 0.03);
		color: #eff5ff;
		padding: 0.6rem 0.68rem;
		resize: none;
	}

	.tora-compose textarea::placeholder {
		color: rgba(191, 203, 229, 0.64);
	}

	.tora-compose button {
		border: 1px solid rgba(255, 255, 255, 0.16);
		border-radius: 10px;
		background: rgba(255, 255, 255, 0.08);
		color: #edf5ff;
		padding: 0.54rem 0.72rem;
		font-size: 0.78rem;
		cursor: pointer;
	}

	.tora-compose button:hover:not(:disabled) {
		border-color: rgba(175, 208, 255, 0.62);
		background: rgba(255, 255, 255, 0.14);
	}

	.tora-compose button:disabled {
		opacity: 0.56;
		cursor: not-allowed;
	}

	@media (max-width: 980px) {
		.secondary-sidebar {
			display: none;
		}

		.viz-grid {
			grid-template-columns: minmax(0, 1fr);
		}

		.progress-stats {
			grid-template-columns: minmax(0, 1fr);
		}
	}
</style>
