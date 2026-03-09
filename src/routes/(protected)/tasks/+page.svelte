<script lang="ts">
	import { onMount } from 'svelte';
	import TaskBoard from '$lib/components/workspace/TaskBoard.svelte';
	import { fetchDashboardOverview, overview, overviewError, overviewLoading } from '$lib/stores/dashboard';
	import { activeContext, setContext } from '$lib/stores/jiraContext';

	let workspaceLoadError = '';

	onMount(() => {
		if ($overview) {
			return;
		}
		void loadWorkspaces();
	});

	async function loadWorkspaces() {
		workspaceLoadError = '';
		try {
			await fetchDashboardOverview();
		} catch (error) {
			workspaceLoadError = error instanceof Error ? error.message : 'Failed to load workspaces';
		}
	}

	function switchToPersonal() {
		setContext('personal', 'personal', 'Personal Taskboard');
	}

	function switchToRoom(roomID: string, roomName: string) {
		const normalizedRoomID = roomID.trim();
		if (!normalizedRoomID) {
			return;
		}
		setContext('room', normalizedRoomID, roomName.trim() || normalizedRoomID);
	}

	function isActiveWorkspace(workspaceID: string) {
		return $activeContext.id === workspaceID;
	}
</script>

<svelte:head>
	<title>Tasks | Converse</title>
</svelte:head>

<main class="tasks-shell">
	<section class="jira-layout">
		<aside class="sidebar">
			<div class="sidebar-block">
				<h2>Jira-Lite</h2>
				<p>Switch workspaces instantly.</p>
			</div>

			<div class="sidebar-block">
				<button
					type="button"
					class="workspace-btn"
					class:active={isActiveWorkspace('personal')}
					on:click={switchToPersonal}
				>
					Personal
				</button>
			</div>

			<div class="sidebar-block">
				<div class="section-title-row">
					<h3>Workspaces</h3>
					<button type="button" class="refresh-btn" on:click={loadWorkspaces} disabled={$overviewLoading}>
						{$overviewLoading ? '...' : 'Refresh'}
					</button>
				</div>
				{#if workspaceLoadError || $overviewError}
					<div class="side-error">{workspaceLoadError || $overviewError}</div>
				{/if}

				{#if !$overview}
					<div class="side-state">Loading workspaces...</div>
				{:else if $overview.recent_rooms.length === 0}
					<div class="side-state">No active rooms yet.</div>
				{:else}
					<div class="workspace-list">
						{#each $overview.recent_rooms as room (room.room_id)}
							<button
								type="button"
								class="workspace-btn"
								class:active={isActiveWorkspace(room.room_id)}
								on:click={() => switchToRoom(room.room_id, room.room_name)}
							>
								{room.room_name || room.room_id}
							</button>
						{/each}
					</div>
				{/if}
			</div>
		</aside>

		<section class="board-shell">
			<TaskBoard contextAware={true} canEdit={true} />
		</section>
	</section>
</main>

<style>
	:global(:root) {
		--tasks-shell-bg:
			radial-gradient(circle at 10% -6%, rgba(160, 196, 248, 0.2), transparent 34%),
			radial-gradient(circle at 92% 12%, rgba(176, 210, 248, 0.2), transparent 34%),
			#f3f7ff;
		--tasks-text: #13203b;
		--tasks-panel-bg: rgba(255, 255, 255, 0.62);
		--tasks-panel-border: rgba(174, 197, 232, 0.48);
		--tasks-panel-shadow: 0 18px 44px rgba(93, 120, 168, 0.2);
		--tasks-muted-text: rgba(61, 80, 114, 0.74);
		--tasks-heading-text: rgba(25, 42, 73, 0.88);
		--tasks-refresh-border: rgba(95, 125, 178, 0.3);
		--tasks-refresh-bg: rgba(255, 255, 255, 0.72);
		--tasks-refresh-text: rgba(19, 38, 66, 0.9);
		--tasks-workspace-border: rgba(159, 184, 224, 0.5);
		--tasks-workspace-bg: rgba(255, 255, 255, 0.52);
		--tasks-workspace-text: rgba(27, 46, 80, 0.9);
		--tasks-workspace-hover-border: rgba(109, 145, 206, 0.58);
		--tasks-workspace-hover-bg: rgba(233, 242, 255, 0.75);
		--tasks-workspace-active-bg: rgba(212, 228, 251, 0.88);
		--tasks-workspace-active-border: rgba(103, 142, 207, 0.64);
		--tasks-state-bg: rgba(255, 255, 255, 0.5);
		--tasks-state-border: rgba(140, 171, 218, 0.5);
		--tasks-state-text: rgba(67, 87, 122, 0.76);
		--tasks-error-bg: rgba(220, 38, 38, 0.12);
		--tasks-error-border: rgba(220, 38, 38, 0.35);
		--tasks-error-text: #8f2235;
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--tasks-shell-bg:
			radial-gradient(circle at 10% -6%, rgba(255, 255, 255, 0.08), transparent 34%),
			radial-gradient(circle at 92% 12%, rgba(255, 255, 255, 0.04), transparent 34%),
			#0d0d12;
		--tasks-text: #f4f7ff;
		--tasks-panel-bg: rgba(255, 255, 255, 0.03);
		--tasks-panel-border: rgba(255, 255, 255, 0.09);
		--tasks-panel-shadow: 0 18px 44px rgba(0, 0, 0, 0.36);
		--tasks-muted-text: rgba(205, 213, 235, 0.74);
		--tasks-heading-text: rgba(236, 241, 255, 0.86);
		--tasks-refresh-border: rgba(255, 255, 255, 0.2);
		--tasks-refresh-bg: rgba(255, 255, 255, 0.08);
		--tasks-refresh-text: rgba(243, 248, 255, 0.92);
		--tasks-workspace-border: rgba(255, 255, 255, 0.12);
		--tasks-workspace-bg: rgba(255, 255, 255, 0.04);
		--tasks-workspace-text: rgba(239, 244, 255, 0.9);
		--tasks-workspace-hover-border: rgba(255, 255, 255, 0.24);
		--tasks-workspace-hover-bg: rgba(255, 255, 255, 0.08);
		--tasks-workspace-active-bg: rgba(255, 255, 255, 0.1);
		--tasks-workspace-active-border: rgba(178, 201, 247, 0.68);
		--tasks-state-bg: rgba(255, 255, 255, 0.03);
		--tasks-state-border: rgba(255, 255, 255, 0.16);
		--tasks-state-text: rgba(201, 209, 229, 0.74);
		--tasks-error-bg: rgba(220, 38, 38, 0.18);
		--tasks-error-border: rgba(248, 113, 113, 0.35);
		--tasks-error-text: #ffd0dc;
	}

	.tasks-shell {
		height: 100dvh;
		box-sizing: border-box;
		padding: 5.6rem 1.2rem 1.3rem;
		background: var(--tasks-shell-bg);
		color: var(--tasks-text);
		overflow: hidden;
	}

	.jira-layout {
		height: 100%;
		display: grid;
		grid-template-columns: 250px minmax(0, 1fr);
		gap: 0.95rem;
		min-height: 0;
		overflow: hidden;
	}

	.sidebar,
	.board-shell {
		min-height: 0;
		border-radius: 16px;
		border: 1px solid var(--tasks-panel-border);
		background: var(--tasks-panel-bg);
		box-shadow: var(--tasks-panel-shadow);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
	}

	.sidebar {
		padding: 0.9rem;
		display: grid;
		grid-template-rows: auto auto 1fr;
		gap: 0.95rem;
		overflow: auto;
	}

	.sidebar-block h2 {
		margin: 0;
		font-size: 0.98rem;
		letter-spacing: 0.04em;
		text-transform: uppercase;
	}

	.sidebar-block p {
		margin: 0.33rem 0 0;
		font-size: 0.78rem;
		color: var(--tasks-muted-text);
	}

	.section-title-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
	}

	.section-title-row h3 {
		margin: 0;
		font-size: 0.82rem;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--tasks-heading-text);
	}

	.refresh-btn {
		border: 1px solid var(--tasks-refresh-border);
		background: var(--tasks-refresh-bg);
		color: var(--tasks-refresh-text);
		border-radius: 9px;
		padding: 0.28rem 0.5rem;
		font-size: 0.67rem;
		cursor: pointer;
	}

	.refresh-btn:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	.workspace-list {
		margin-top: 0.55rem;
		display: grid;
		gap: 0.5rem;
		max-height: 100%;
		overflow-y: auto;
	}

	.workspace-btn {
		width: 100%;
		text-align: left;
		border-radius: 10px;
		border: 1px solid var(--tasks-workspace-border);
		background: var(--tasks-workspace-bg);
		color: var(--tasks-workspace-text);
		padding: 0.53rem 0.62rem;
		font-size: 0.8rem;
		cursor: pointer;
		transition:
			border-color 0.2s ease,
			background 0.2s ease;
	}

	.workspace-btn:hover {
		border-color: var(--tasks-workspace-hover-border);
		background: var(--tasks-workspace-hover-bg);
	}

	.workspace-btn.active {
		background: var(--tasks-workspace-active-bg);
		border-color: var(--tasks-workspace-active-border);
	}

	.side-state,
	.side-error {
		margin-top: 0.52rem;
		padding: 0.6rem 0.62rem;
		border-radius: 10px;
		font-size: 0.74rem;
	}

	.side-state {
		background: var(--tasks-state-bg);
		border: 1px dashed var(--tasks-state-border);
		color: var(--tasks-state-text);
	}

	.side-error {
		background: var(--tasks-error-bg);
		border: 1px solid var(--tasks-error-border);
		color: var(--tasks-error-text);
	}

	.board-shell {
		overflow: auto;
	}

	@media (max-width: 980px) {
		.jira-layout {
			height: 100%;
			grid-template-columns: 1fr;
			grid-template-rows: auto 1fr;
		}

		.sidebar {
			grid-template-rows: auto auto auto;
			max-height: 42dvh;
		}

		.board-shell {
			min-height: 0;
		}
	}
</style>
