<script lang="ts">
	import { onMount } from 'svelte';
	import PersonalTaskboard from '$lib/components/dashboard/PersonalTaskboard.svelte';
	import {
		acceptPendingRequest,
		declinePendingRequest,
		fetchDashboardOverview,
		overview,
		overviewError,
		overviewLoading
	} from '$lib/stores/dashboard';
	import type { DashboardConnection } from '$lib/stores/dashboard';
	import type { PageData } from './$types';

	export let data: PageData;

	let pendingRequestUserID = '';
	let actionError = '';

	const relativeFormatter = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });
	const dateFormatter = new Intl.DateTimeFormat('en-US', {
		month: 'short',
		day: 'numeric',
		hour: 'numeric',
		minute: '2-digit'
	});

	onMount(() => {
		void loadOverview();
	});

	async function loadOverview() {
		actionError = '';
		try {
			await fetchDashboardOverview();
		} catch (error) {
			actionError = error instanceof Error ? error.message : 'Failed to load dashboard overview';
		}
	}

	function parseTimestamp(raw: string) {
		const value = Date.parse(raw);
		return Number.isFinite(value) ? value : null;
	}

	function formatRelative(raw: string) {
		const parsed = parseTimestamp(raw);
		if (parsed === null) {
			return 'Unknown';
		}
		const deltaSeconds = Math.round((parsed - Date.now()) / 1000);
		const absSeconds = Math.abs(deltaSeconds);

		if (absSeconds < 60) {
			return relativeFormatter.format(deltaSeconds, 'second');
		}
		if (absSeconds < 3600) {
			return relativeFormatter.format(Math.round(deltaSeconds / 60), 'minute');
		}
		if (absSeconds < 86400) {
			return relativeFormatter.format(Math.round(deltaSeconds / 3600), 'hour');
		}
		if (absSeconds < 86400 * 30) {
			return relativeFormatter.format(Math.round(deltaSeconds / 86400), 'day');
		}
		return relativeFormatter.format(Math.round(deltaSeconds / (86400 * 30)), 'month');
	}

	function formatDate(raw: string | null) {
		if (!raw) {
			return 'No due date';
		}
		const parsed = parseTimestamp(raw);
		if (parsed === null) {
			return 'No due date';
		}
		return dateFormatter.format(parsed);
	}

	function roomName(value: string, fallback: string) {
		const trimmed = value.trim();
		return trimmed || fallback;
	}

	async function handleAccept(connection: DashboardConnection) {
		if (pendingRequestUserID) {
			return;
		}

		pendingRequestUserID = connection.user_id;
		actionError = '';
		try {
			await acceptPendingRequest(connection.user_id);
		} catch (error) {
			actionError = error instanceof Error ? error.message : 'Failed to accept request';
		} finally {
			pendingRequestUserID = '';
		}
	}

	function handleDecline(connection: DashboardConnection) {
		if (pendingRequestUserID) {
			return;
		}
		declinePendingRequest(connection.user_id);
	}
</script>

<svelte:head>
	<title>Dashboard | Converse</title>
</svelte:head>

<main class="dashboard-shell">
	<header class="top-header">
		<div>
			<h1>Welcome back, {data.user?.name || 'there'}</h1>
			<p>Personal priorities, active rooms, and network activity in one place.</p>
		</div>
		<button type="button" class="refresh-btn" on:click={loadOverview} disabled={$overviewLoading}>
			{$overviewLoading ? 'Refreshing...' : 'Refresh'}
		</button>
	</header>

	{#if $overviewLoading && !$overview}
		<section class="glass-panel state-card">
			<p>Loading dashboard overview...</p>
		</section>
	{:else if !$overview}
		<section class="glass-panel state-card error">
			<p>{$overviewError || actionError || 'Unable to load dashboard overview.'}</p>
			<button type="button" class="refresh-btn" on:click={loadOverview}>Try again</button>
		</section>
	{:else}
		<section class="dashboard-grid">
			<div class="column left-column">
				<PersonalTaskboard />

				<article class="glass-panel section-card">
					<div class="section-head">
						<h2>Upcoming Items</h2>
						<span>{$overview.upcoming_items.length}</span>
					</div>

					{#if $overview.upcoming_items.length === 0}
						<div class="empty-state">No pending personal items.</div>
					{:else}
						<div class="entity-stack">
							{#each $overview.upcoming_items as item (item.item_id)}
								<div class="entity-row">
									<div class="entity-main">
										<p>{item.content || 'Untitled item'}</p>
										<small>{item.type || 'task'} • {item.status || 'pending'}</small>
									</div>
									<small class="entity-meta">{formatDate(item.due_at)}</small>
								</div>
							{/each}
						</div>
					{/if}
				</article>
			</div>

			<div class="column center-column">
				<article class="glass-panel section-card">
					<div class="section-head">
						<h2>Active Workspaces</h2>
						<span>{$overview.recent_rooms.length}</span>
					</div>

					{#if $overview.recent_rooms.length === 0}
						<div class="empty-state">No recent rooms yet.</div>
					{:else}
						<div class="entity-stack">
							{#each $overview.recent_rooms as room (room.room_id)}
								<a class="entity-row link-row" href={`/chat/${encodeURIComponent(room.room_id)}`}>
									<div class="entity-main">
										<p>{roomName(room.room_name, room.room_id)}</p>
										<small>{room.role || 'member'}</small>
									</div>
									<small class="entity-meta">{formatRelative(room.last_accessed)}</small>
								</a>
							{/each}
						</div>
					{/if}
				</article>

				<article class="glass-panel section-card">
					<div class="section-head">
						<h2>Global Tasks</h2>
						<span>{$overview.assigned_tasks.length}</span>
					</div>

					{#if $overview.assigned_tasks.length === 0}
						<div class="empty-state">No assigned tasks right now.</div>
					{:else}
						<div class="entity-stack">
							{#each $overview.assigned_tasks as task (task.id)}
								<div class="entity-row">
									<div class="entity-main">
										<p>{task.title || 'Untitled task'}</p>
										<small>{task.description || 'No description'}</small>
									</div>
									<div class="task-meta">
										<span class="status-pill">{task.status || 'open'}</span>
										<small>{formatRelative(task.updated_at || task.created_at)}</small>
									</div>
								</div>
							{/each}
						</div>
					{/if}
				</article>
			</div>

			<div class="column right-column">
				<article class="glass-panel section-card">
					<div class="section-head">
						<h2>Network</h2>
						<span>{$overview.pending_requests.length}</span>
					</div>

					{#if actionError}
						<div class="error-inline">{actionError}</div>
					{/if}

					{#if $overview.pending_requests.length === 0}
						<div class="empty-state">No pending requests.</div>
					{:else}
						<div class="entity-stack">
							{#each $overview.pending_requests as request (request.user_id)}
								<div class="entity-row network-row">
									<div class="entity-main">
										<p>{request.user_id}</p>
										<small>{formatRelative(request.created_at)}</small>
									</div>
									<div class="network-actions">
										<button
											type="button"
											class="action-btn accept"
											on:click={() => {
												void handleAccept(request);
											}}
											disabled={pendingRequestUserID === request.user_id}
										>
											Accept
										</button>
										<button
											type="button"
											class="action-btn decline"
											on:click={() => {
												handleDecline(request);
											}}
											disabled={pendingRequestUserID === request.user_id}
										>
											Decline
										</button>
									</div>
								</div>
							{/each}
						</div>
					{/if}
				</article>
			</div>
		</section>
	{/if}
</main>

<style>
	:global(:root) {
		--dashboard-shell-bg:
			radial-gradient(circle at 12% -8%, rgba(157, 193, 247, 0.2), transparent 36%),
			radial-gradient(circle at 90% 12%, rgba(188, 210, 245, 0.18), transparent 34%),
			#f3f7ff;
		--dashboard-text-color: #111a2f;
		--dashboard-muted-text: rgba(42, 58, 90, 0.74);
		--dashboard-refresh-border: rgba(94, 123, 176, 0.26);
		--dashboard-refresh-bg: rgba(255, 255, 255, 0.72);
		--dashboard-refresh-text: #0f2342;
		--dashboard-panel-bg: rgba(255, 255, 255, 0.62);
		--dashboard-panel-border: rgba(175, 197, 231, 0.5);
		--dashboard-panel-shadow: 0 18px 44px rgba(93, 120, 168, 0.22);
		--dashboard-section-heading: rgba(28, 45, 74, 0.9);
		--dashboard-section-count: rgba(62, 80, 114, 0.86);
		--dashboard-row-bg: rgba(255, 255, 255, 0.56);
		--dashboard-row-border: rgba(171, 193, 229, 0.54);
		--dashboard-subtle-text: rgba(58, 77, 110, 0.76);
		--dashboard-link-hover-border: rgba(109, 142, 201, 0.5);
		--dashboard-empty-border: rgba(150, 178, 224, 0.52);
		--dashboard-empty-bg: rgba(255, 255, 255, 0.48);
		--dashboard-empty-text: rgba(66, 84, 119, 0.76);
		--dashboard-status-pill-border: rgba(121, 149, 202, 0.52);
		--dashboard-status-pill-text: rgba(20, 39, 68, 0.9);
		--dashboard-action-btn-border: rgba(98, 127, 179, 0.34);
		--dashboard-action-btn-bg: rgba(255, 255, 255, 0.62);
		--dashboard-action-btn-text: #122646;
		--dashboard-action-accept-border: rgba(35, 154, 110, 0.52);
		--dashboard-action-accept-text: #0f6f4f;
		--dashboard-action-decline-border: rgba(211, 79, 104, 0.45);
		--dashboard-action-decline-text: #9e2940;
		--dashboard-error-bg: rgba(220, 38, 38, 0.13);
		--dashboard-error-border: rgba(220, 38, 38, 0.36);
		--dashboard-error-text: #8f2235;
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--dashboard-shell-bg:
			radial-gradient(circle at 12% -8%, rgba(255, 255, 255, 0.08), transparent 36%),
			radial-gradient(circle at 90% 12%, rgba(255, 255, 255, 0.05), transparent 34%),
			#0d0d12;
		--dashboard-text-color: #f4f6ff;
		--dashboard-muted-text: rgba(229, 233, 246, 0.76);
		--dashboard-refresh-border: rgba(255, 255, 255, 0.16);
		--dashboard-refresh-bg: rgba(255, 255, 255, 0.07);
		--dashboard-refresh-text: #f7f9ff;
		--dashboard-panel-bg: rgba(255, 255, 255, 0.03);
		--dashboard-panel-border: rgba(255, 255, 255, 0.09);
		--dashboard-panel-shadow: 0 18px 44px rgba(0, 0, 0, 0.36);
		--dashboard-section-heading: rgba(244, 247, 255, 0.9);
		--dashboard-section-count: rgba(199, 206, 226, 0.9);
		--dashboard-row-bg: rgba(255, 255, 255, 0.03);
		--dashboard-row-border: rgba(255, 255, 255, 0.08);
		--dashboard-subtle-text: rgba(201, 208, 228, 0.74);
		--dashboard-link-hover-border: rgba(173, 194, 238, 0.34);
		--dashboard-empty-border: rgba(255, 255, 255, 0.18);
		--dashboard-empty-bg: rgba(255, 255, 255, 0.02);
		--dashboard-empty-text: rgba(202, 208, 226, 0.78);
		--dashboard-status-pill-border: rgba(255, 255, 255, 0.22);
		--dashboard-status-pill-text: rgba(241, 245, 255, 0.92);
		--dashboard-action-btn-border: rgba(255, 255, 255, 0.16);
		--dashboard-action-btn-bg: rgba(255, 255, 255, 0.06);
		--dashboard-action-btn-text: #f8f9ff;
		--dashboard-action-accept-border: rgba(94, 228, 171, 0.45);
		--dashboard-action-accept-text: #b4f1d4;
		--dashboard-action-decline-border: rgba(246, 120, 140, 0.4);
		--dashboard-action-decline-text: #ffc1cc;
		--dashboard-error-bg: rgba(220, 38, 38, 0.2);
		--dashboard-error-border: rgba(248, 113, 113, 0.4);
		--dashboard-error-text: #ffd3db;
	}

	.dashboard-shell {
		height: 100dvh;
		box-sizing: border-box;
		padding: 5.6rem 1.2rem 1.4rem;
		background: var(--dashboard-shell-bg);
		color: var(--dashboard-text-color);
		display: grid;
		grid-template-rows: auto 1fr;
		gap: 1rem;
		overflow: hidden;
	}

	.top-header {
		display: flex;
		justify-content: space-between;
		align-items: flex-start;
		gap: 0.8rem;
		min-height: 0;
	}

	.top-header h1 {
		margin: 0;
		font-size: clamp(1.2rem, 2.4vw, 1.9rem);
		letter-spacing: -0.02em;
	}

	.top-header p {
		margin: 0.35rem 0 0;
		font-size: 0.88rem;
		color: var(--dashboard-muted-text);
	}

	.refresh-btn {
		border: 1px solid var(--dashboard-refresh-border);
		background: var(--dashboard-refresh-bg);
		color: var(--dashboard-refresh-text);
		padding: 0.56rem 0.86rem;
		border-radius: 10px;
		font-size: 0.78rem;
		cursor: pointer;
	}

	.refresh-btn:disabled {
		opacity: 0.68;
		cursor: not-allowed;
	}

	.dashboard-grid {
		display: grid;
		grid-template-columns: minmax(280px, 1.05fr) minmax(320px, 1.4fr) minmax(260px, 0.95fr);
		gap: 0.95rem;
		min-height: 0;
		overflow: hidden;
	}

	.column {
		display: grid;
		gap: 0.95rem;
		align-content: start;
		min-height: 0;
		overflow: auto;
		padding-right: 0.18rem;
	}

	.glass-panel {
		background: var(--dashboard-panel-bg);
		border: 1px solid var(--dashboard-panel-border);
		border-radius: 16px;
		box-shadow: var(--dashboard-panel-shadow);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
	}

	.section-card {
		padding: 0.86rem;
		display: grid;
		gap: 0.75rem;
	}

	.section-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
	}

	.section-head h2 {
		margin: 0;
		font-size: 0.84rem;
		text-transform: uppercase;
		letter-spacing: 0.08em;
		color: var(--dashboard-section-heading);
	}

	.section-head span {
		font-size: 0.68rem;
		font-family: 'JetBrains Mono', monospace;
		color: var(--dashboard-section-count);
	}

	.entity-stack {
		display: grid;
		gap: 0.6rem;
	}

	.entity-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.65rem;
		padding: 0.58rem 0.64rem;
		border-radius: 11px;
		background: var(--dashboard-row-bg);
		border: 1px solid var(--dashboard-row-border);
	}

	.entity-main {
		min-width: 0;
	}

	.entity-main p {
		margin: 0;
		font-size: 0.82rem;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.entity-main small,
	.entity-meta,
	.task-meta small {
		font-size: 0.68rem;
		color: var(--dashboard-subtle-text);
	}

	.link-row {
		text-decoration: none;
		color: inherit;
		transition: border-color 0.18s ease, transform 0.18s ease;
	}

	.link-row:hover {
		border-color: var(--dashboard-link-hover-border);
		transform: translateY(-1px);
	}

	.empty-state {
		padding: 0.72rem;
		border-radius: 11px;
		border: 1px dashed var(--dashboard-empty-border);
		background: var(--dashboard-empty-bg);
		font-size: 0.78rem;
		color: var(--dashboard-empty-text);
	}

	.task-meta {
		display: grid;
		justify-items: end;
		gap: 0.2rem;
	}

	.status-pill {
		display: inline-block;
		padding: 0.16rem 0.44rem;
		border-radius: 999px;
		border: 1px solid var(--dashboard-status-pill-border);
		font-size: 0.62rem;
		font-family: 'JetBrains Mono', monospace;
		text-transform: lowercase;
		color: var(--dashboard-status-pill-text);
	}

	.network-row {
		align-items: flex-start;
	}

	.network-actions {
		display: inline-flex;
		gap: 0.35rem;
	}

	.action-btn {
		border: 1px solid var(--dashboard-action-btn-border);
		background: var(--dashboard-action-btn-bg);
		color: var(--dashboard-action-btn-text);
		border-radius: 9px;
		padding: 0.35rem 0.55rem;
		font-size: 0.68rem;
		cursor: pointer;
	}

	.action-btn:disabled {
		opacity: 0.62;
		cursor: not-allowed;
	}

	.action-btn.accept {
		border-color: var(--dashboard-action-accept-border);
		color: var(--dashboard-action-accept-text);
	}

	.action-btn.decline {
		border-color: var(--dashboard-action-decline-border);
		color: var(--dashboard-action-decline-text);
	}

	.error-inline {
		padding: 0.5rem 0.6rem;
		border-radius: 10px;
		background: var(--dashboard-error-bg);
		border: 1px solid var(--dashboard-error-border);
		font-size: 0.75rem;
		color: var(--dashboard-error-text);
	}

	.state-card {
		padding: 1rem;
		display: grid;
		gap: 0.6rem;
	}

	.state-card p {
		margin: 0;
		font-size: 0.86rem;
	}

	.state-card.error {
		border-color: var(--dashboard-error-border);
	}

	@media (max-width: 1180px) {
		.dashboard-grid {
			grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
		}

		.right-column {
			grid-column: 1 / -1;
		}
	}

	@media (max-width: 920px) {
		.dashboard-shell {
			padding-top: 5.1rem;
		}

		.dashboard-grid {
			grid-template-columns: 1fr;
		}
	}
</style>
