<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import ProjectOnboarding from '$lib/components/workspace/ProjectOnboarding.svelte';
	import ProgressGanttTab from '$lib/components/workspace/ProgressGanttTab.svelte';
	import TaskBoard from '$lib/components/workspace/TaskBoard.svelte';
	import ToraAIPanel from '$lib/components/workspace/ToraAIPanel.svelte';
	import TimelineBoard from '$lib/components/workspace/TimelineBoard.svelte';
	import TableBoard from './TableBoard.svelte';
	import ActivityFeedPanel from './ActivityFeedPanel.svelte';
	import { currentUser } from '$lib/store';
	import type { OnlineMember } from '$lib/types/chat';

	const dispatch = createEventDispatcher<{ close: void }>();
	import {
		activeProjectTab,
		initializeProjectTimelineForRoom,
		isProjectNew,
		projectTimeline,
		setProjectTimeline,
		timelineError,
		timelineLoading,
		type ProjectTab
	} from '$lib/stores/timeline';
	import { clearBoardActivity, setBoardActivityRoom } from '$lib/stores/boardActivity';
	import { initializeTaskStoreForRoom } from '$lib/stores/tasks';
	import { normalizeRoomIDValue } from '$lib/utils/chat/core';

	export let roomId = '';
	export let canEdit = true;
	export let onlineMembers: OnlineMember[] = [];

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';

	type WorkspaceTabMeta = {
		key: ProjectTab;
		label: string;
		// SVG path(s) — stroke-based icons, viewBox 0 0 24 24
		icon: string;
	};

	// Activity bar icons (left rail — icon-only buttons)
	const WORKSPACE_TABS: WorkspaceTabMeta[] = [
		{
			key: 'overview',
			label: 'Overview',
			// dashboard grid
			icon: 'M3 3h7v7H3zM14 3h7v4h-7zM14 10h7v11h-7zM3 13h7v8H3z'
		},
		{
			key: 'tasks',
			label: 'Tasks',
			// kanban columns
			icon: 'M8 7h11M8 12h11M8 17h11M4.5 7h.01M4.5 12h.01M4.5 17h.01'
		},
		{
			key: 'progress',
			label: 'Progress / Gantt',
			// bar chart ascending
			icon: 'M5 18.5h14M7.5 16V9.5M12 16V6.5M16.5 16v-4.2'
		},
		{
			key: 'table',
			label: 'Table View',
			// table grid
			icon: 'M3 5h18M3 10h18M3 15h18M3 20h18M8 5v15M16 5v15'
		},
		{
			key: 'tora_ai',
			label: 'Tora AI',
			// sparkle
			icon: 'M12 4.2 13.7 8l3.8 1.5-3.8 1.5L12 14.8 10.3 11 6.5 9.5 10.3 8 12 4.2Z M18.5 13.5l.9 2.2 2.1.8-2.1.9-.9 2.1-.8-2.1-2.2-.9 2.2-.8z'
		}
	];

	let lastWorkspaceRoomID = '';
	let workspaceLoadToken = 0;
	let clearingTaskboard = false;

	$: sessionUserID = ($currentUser?.id || '').trim();
	$: normalizedWorkspaceRoomID = normalizeRoomIDValue(roomId);
	$: if (normalizedWorkspaceRoomID !== lastWorkspaceRoomID) {
		lastWorkspaceRoomID = normalizedWorkspaceRoomID;
		activeProjectTab.set('overview');
		setBoardActivityRoom(normalizedWorkspaceRoomID);
		void hydrateWorkspaceForRoom(normalizedWorkspaceRoomID);
	}

	$: timeline = $projectTimeline;
	$: sprints = timeline?.sprints ?? [];
	$: allTasks = sprints.flatMap((sprint) => sprint.tasks);
	$: totalTasks = allTasks.length;
	$: completedTasks = allTasks.filter((task) => task.status === 'done').length;
	$: inProgressTasks = allTasks.filter((task) => task.status === 'in_progress').length;
	$: todoCount = allTasks.filter((task) => task.status === 'todo').length;
	$: completionRate = totalTasks > 0 ? Math.round((completedTasks / totalTasks) * 100) : 0;

	function activateTab(tab: ProjectTab) {
		activeProjectTab.set(tab);
	}

	async function parseWorkspaceError(response: Response) {
		const payload = (await response.json().catch(() => null)) as {
			error?: string;
			message?: string;
		} | null;
		return payload?.error?.trim() || payload?.message?.trim() || `HTTP ${response.status}`;
	}

	async function hydrateWorkspaceForRoom(normalizedRoomID: string) {
		workspaceLoadToken += 1;
		const loadToken = workspaceLoadToken;

		if (!normalizedRoomID) {
			setBoardActivityRoom('');
			setProjectTimeline(null);
			isProjectNew.set(true);
			timelineError.set('');
			return;
		}

		await Promise.all([
			initializeProjectTimelineForRoom(normalizedRoomID, { apiBase: API_BASE }),
			initializeTaskStoreForRoom(normalizedRoomID, { apiBase: API_BASE })
		]);

		if (loadToken !== workspaceLoadToken) return;
	}

	async function clearTaskboard() {
		if ($isProjectNew || !$projectTimeline) return;
		const shouldReset = window.confirm(
			'Clear this taskboard and return to setup? This removes all tasks for this room.'
		);
		if (!shouldReset) return;

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
			if (!response.ok) throw new Error(await parseWorkspaceError(response));

			await Promise.all([
				initializeProjectTimelineForRoom(normalizedWorkspaceRoomID, { apiBase: API_BASE }),
				initializeTaskStoreForRoom(normalizedWorkspaceRoomID, { apiBase: API_BASE })
			]);

			clearBoardActivity(normalizedWorkspaceRoomID);
			activeProjectTab.set('overview');
		} catch (error) {
			timelineError.set(error instanceof Error ? error.message : 'Failed to clear room taskboard');
		} finally {
			clearingTaskboard = false;
		}
	}
</script>

<section class="workspace-shell" aria-label="Project workspace">
	<!-- ── Close button ────────────────────────────────────────────────── -->
	<button
		type="button"
		class="close-btn"
		on:click={() => dispatch('close')}
		title="Close task board"
		aria-label="Close task board"
	>
		<svg viewBox="0 0 24 24" aria-hidden="true">
			<path d="M18 6 6 18M6 6l12 12"></path>
		</svg>
	</button>

	<div class="workspace-frame">
		<nav class="activity-bar" aria-label="Workspace navigation">
			{#each WORKSPACE_TABS as tab (tab.key)}
				<button
					type="button"
					class="act-btn"
					class:is-active={$activeProjectTab === tab.key}
					on:click={() => activateTab(tab.key)}
					title={tab.label}
					aria-label={tab.label}
					aria-current={$activeProjectTab === tab.key ? 'page' : undefined}
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d={tab.icon}></path>
					</svg>
				</button>
			{/each}

			<span class="act-spacer" aria-hidden="true"></span>

			<div class="workspace-actions">
				<button
					type="button"
					class="act-btn act-clear"
					on:click={() => void clearTaskboard()}
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
			</div>
		</nav>

		<div class="workspace-content">
			<aside class="activity-feed-sidebar">
				<ActivityFeedPanel />
			</aside>

			<main class="workspace-canvas">
				{#if $timelineLoading && !$projectTimeline}
					<div class="canvas-loading">
						<span class="loading-spinner" aria-hidden="true"></span>
						<p>Loading workspace…</p>
					</div>
				{:else if $isProjectNew || !$projectTimeline}
					<ProjectOnboarding {roomId} />
				{:else if $activeProjectTab === 'overview'}
					<TimelineBoard />
				{:else if $activeProjectTab === 'tasks'}
					<TaskBoard {roomId} {canEdit} {onlineMembers} />
				{:else if $activeProjectTab === 'progress'}
					<ProgressGanttTab {onlineMembers} />
				{:else if $activeProjectTab === 'table'}
					<TableBoard />
				{:else}
					<!-- tora_ai -->
					<ToraAIPanel {roomId} contextKey="taskboard" />
				{/if}
			</main>
		</div>
	</div>
</section>

<style>
	:global(:root) {
		--ws-bg: #f2f6fc;
		--ws-surface: #ffffff;
		--ws-surface-soft: #f8fbff;
		--ws-border: #d5e0f0;
		--ws-text: #12223f;
		--ws-muted: #5c7196;
		--ws-accent: #2563eb;
		--ws-accent-soft: rgba(37, 99, 235, 0.1);
		--ws-danger: #dc2626;
		--ws-danger-soft: rgba(220, 38, 38, 0.1);
		--ws-feed-w: 250px;
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--ws-bg: #161617;
		--ws-surface: #1b1b1d;
		--ws-surface-soft: #232326;
		--ws-border: #34343a;
		--ws-text: #eff2f7;
		--ws-muted: #a3abb8;
		--ws-accent: #ceced5;
		--ws-accent-soft: rgba(206, 206, 213, 0.16);
		--ws-danger: #f87171;
		--ws-danger-soft: rgba(248, 113, 113, 0.14);
	}

	.workspace-shell {
		height: 100%;
		width: 100%;
		min-height: 0;
		min-width: 0;
		background: var(--ws-bg);
		color: var(--ws-text);
		overflow: hidden;
		position: relative;
	}

	.workspace-frame {
		height: 100%;
		min-height: 0;
		min-width: 0;
		display: grid;
		grid-template-columns: 56px minmax(0, 1fr);
		background: var(--ws-surface);
	}

	.close-btn {
		position: absolute;
		top: 0.75rem;
		right: 0.75rem;
		z-index: 20;
		width: 32px;
		height: 32px;
		border-radius: 10px;
		border: 1px solid color-mix(in srgb, var(--ws-danger) 45%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-danger-soft) 72%, transparent);
		color: var(--ws-danger);
		display: grid;
		place-items: center;
		cursor: pointer;
		transition:
			background 0.2s ease,
			border-color 0.2s ease,
			color 0.2s ease;
	}

	.close-btn svg {
		width: 0.92rem;
		height: 0.92rem;
		stroke: currentColor;
		stroke-width: 2;
		fill: none;
		stroke-linecap: round;
	}

	.close-btn:hover {
		background: color-mix(in srgb, var(--ws-danger-soft) 100%, transparent);
		border-color: color-mix(in srgb, var(--ws-danger) 70%, var(--ws-border));
	}

	.activity-bar {
		min-height: 0;
		display: flex;
		flex-direction: column;
		align-items: stretch;
		gap: 0.42rem;
		padding: 0.62rem 0.36rem;
		border-right: 1px solid var(--ws-border);
		background: linear-gradient(180deg, var(--ws-surface) 0%, var(--ws-surface-soft) 100%);
	}

	.act-btn {
		width: 100%;
		min-height: 38px;
		border-radius: 10px;
		border: 1px solid var(--ws-border);
		background: var(--ws-surface);
		color: var(--ws-muted);
		display: grid;
		place-items: center;
		cursor: pointer;
		transition:
			border-color 0.2s ease,
			background 0.2s ease,
			color 0.2s ease,
			transform 0.15s ease;
	}

	.act-btn:hover:not(:disabled) {
		color: var(--ws-text);
		border-color: color-mix(in srgb, var(--ws-accent) 40%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 42%, var(--ws-surface));
		transform: translateY(-1px);
	}

	.act-btn.is-active {
		color: var(--ws-accent);
		border-color: color-mix(in srgb, var(--ws-accent) 60%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 72%, var(--ws-surface));
		box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--ws-accent) 20%, transparent);
	}

	.act-btn svg {
		width: 0.96rem;
		height: 0.96rem;
		stroke: currentColor;
		stroke-width: 1.9;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
		flex-shrink: 0;
	}

	.act-spacer {
		flex: 1;
	}

	.workspace-actions {
		display: flex;
		flex-direction: column;
		gap: 0.55rem;
	}

	.act-clear {
		border-color: color-mix(in srgb, var(--ws-danger) 40%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-danger-soft) 78%, var(--ws-surface));
		color: var(--ws-danger);
	}

	.act-clear:hover:not(:disabled) {
		background: color-mix(in srgb, var(--ws-danger-soft) 100%, var(--ws-surface));
		border-color: color-mix(in srgb, var(--ws-danger) 66%, var(--ws-border));
		color: var(--ws-danger);
	}

	.act-clear:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.workspace-content {
		min-height: 0;
		display: grid;
		grid-template-columns: var(--ws-feed-w) minmax(0, 1fr);
	}

	.activity-feed-sidebar {
		min-height: 0;
		overflow: hidden;
		border-right: 1px solid var(--ws-border);
		background: var(--workspace-taskboard-bg, var(--ws-bg));
	}

	.workspace-canvas {
		min-width: 0;
		min-height: 0;
		overflow: hidden;
		background: var(--ws-bg);
	}

	.canvas-loading {
		height: 100%;
		display: grid;
		place-items: center;
		gap: 0.7rem;
		color: var(--ws-muted);
		font-size: 0.96rem;
	}

	.canvas-loading p {
		margin: 0;
	}

	.loading-spinner {
		display: block;
		width: 1.55rem;
		height: 1.55rem;
		border-radius: 50%;
		border: 2px solid color-mix(in srgb, var(--ws-accent) 20%, var(--ws-border));
		border-top-color: var(--ws-accent);
		animation: spin 0.8s linear infinite;
	}

	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}

	@media (max-width: 1100px) {
		.workspace-content {
			grid-template-columns: minmax(0, 1fr);
		}

		.activity-feed-sidebar {
			display: none;
		}
	}

	@media (max-width: 760px) {
		.workspace-frame {
			grid-template-columns: 50px minmax(0, 1fr);
		}

		.activity-bar {
			padding: 0.58rem 0.28rem;
			gap: 0.36rem;
		}

		.act-btn {
			min-height: 34px;
			border-radius: 9px;
		}

		.act-btn svg {
			width: 0.9rem;
			height: 0.9rem;
		}
	}
</style>
