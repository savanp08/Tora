<script lang="ts">
	import type { PageData } from './$types';

	type DashboardRoom = PageData['rooms'][number];

	type TaskPriority = 'High' | 'Medium' | 'Low';
	type DashboardTask = {
		id: string;
		title: string;
		roomName: string;
		priority: TaskPriority;
		dueLabel: string;
	};

	const mockTasks: DashboardTask[] = [
		{ id: 't1', title: 'Prepare rollout checklist', roomName: 'Launch War Room', priority: 'High', dueLabel: 'Due in 3h' },
		{ id: 't2', title: 'Review API timeout regressions', roomName: 'Infra Reliability', priority: 'High', dueLabel: 'Due today' },
		{ id: 't3', title: 'Consolidate stakeholder notes', roomName: 'Client Discovery', priority: 'Medium', dueLabel: 'Due tomorrow' },
		{ id: 't4', title: 'Update task board labels', roomName: 'Launch War Room', priority: 'Low', dueLabel: 'Due this week' }
	];

	const priorityRank: Record<TaskPriority, number> = {
		High: 0,
		Medium: 1,
		Low: 2
	};

	const relativeFormatter = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });

	function formatRelativeTime(rawTimestamp: string) {
		const parsed = Date.parse(rawTimestamp);
		if (!Number.isFinite(parsed)) {
			return 'Unknown';
		}

		const deltaSeconds = Math.round((parsed - Date.now()) / 1000);
		const absoluteSeconds = Math.abs(deltaSeconds);
		if (absoluteSeconds < 60) {
			return relativeFormatter.format(Math.round(deltaSeconds), 'second');
		}
		if (absoluteSeconds < 60 * 60) {
			return relativeFormatter.format(Math.round(deltaSeconds / 60), 'minute');
		}
		if (absoluteSeconds < 60 * 60 * 24) {
			return relativeFormatter.format(Math.round(deltaSeconds / 3600), 'hour');
		}
		if (absoluteSeconds < 60 * 60 * 24 * 30) {
			return relativeFormatter.format(Math.round(deltaSeconds / 86400), 'day');
		}
		return relativeFormatter.format(Math.round(deltaSeconds / (86400 * 30)), 'month');
	}

	export let data: PageData;

	$: sortedTasks = [...mockTasks].sort((left, right) => priorityRank[left.priority] - priorityRank[right.priority]);
	$: welcomeName = data.user?.name || 'there';
	$: rooms = data.rooms ?? [];
</script>

<svelte:head>
	<title>Dashboard | Converse</title>
</svelte:head>

<main class="dashboard-shell">
	<header class="top-header">
		<h1>Welcome back, {welcomeName}</h1>
		<p>Monitor active collaboration rooms and cross-room assignments in one place.</p>
	</header>

	<section class="dashboard-grid">
		<article class="panel panel-main">
			<div class="panel-head">
				<h2>Active Rooms</h2>
				<span>{rooms.length}</span>
			</div>
			<div class="room-list">
				{#if rooms.length === 0}
					<div class="empty-state">No persistent rooms yet.</div>
				{:else}
					{#each rooms as room (room.room_id)}
						<a class="room-card-link" href={`/room/${encodeURIComponent(room.room_id)}`}>
							<div class="room-card">
								<h3>{room.room_name || room.room_id}</h3>
								<p>Last accessed: {formatRelativeTime(room.last_accessed)}</p>
								<small>{room.role || 'member'}</small>
							</div>
						</a>
					{/each}
				{/if}
			</div>
		</article>

		<aside class="panel panel-side">
			<div class="panel-head">
				<h2>Global Tasks</h2>
				<span>{sortedTasks.length}</span>
			</div>
			<div class="task-list">
				{#each sortedTasks as task (task.id)}
					<div class="task-row priority-{task.priority.toLowerCase()}">
						<div>
							<strong>{task.title}</strong>
							<small>{task.roomName}</small>
						</div>
						<div class="task-meta">
							<span>{task.priority}</span>
							<small>{task.dueLabel}</small>
						</div>
					</div>
				{/each}
			</div>
		</aside>
	</section>
</main>

<style>
	.dashboard-shell {
		min-height: 100dvh;
		padding: 5.6rem 1.2rem 1.4rem;
		background: #0d0d12;
		color: #f2f6ff;
	}

	.top-header h1 {
		margin: 0;
		font-size: clamp(1.35rem, 2.4vw, 2rem);
		letter-spacing: -0.02em;
	}

	.top-header p {
		margin: 0.45rem 0 0;
		color: rgba(202, 209, 227, 0.85);
		font-size: 0.92rem;
	}

	.dashboard-grid {
		margin-top: 1.15rem;
		display: grid;
		grid-template-columns: minmax(0, 2fr) minmax(280px, 1fr);
		gap: 0.95rem;
	}

	.panel {
		background: rgba(255, 255, 255, 0.03);
		border: 1px solid rgba(255, 255, 255, 0.08);
		border-radius: 14px;
		padding: 0.85rem;
	}

	.panel-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 0.7rem;
	}

	.panel-head h2 {
		margin: 0;
		font-size: 0.95rem;
		letter-spacing: 0.03em;
		text-transform: uppercase;
	}

	.panel-head span {
		font-family: 'JetBrains Mono', monospace;
		font-size: 0.7rem;
		color: rgba(154, 169, 196, 0.92);
	}

	.room-list,
	.task-list {
		display: grid;
		gap: 0.62rem;
	}

	.room-card-link {
		display: block;
		text-decoration: none;
		color: inherit;
	}

	.room-card {
		padding: 0.68rem;
		border-radius: 11px;
		background: rgba(255, 255, 255, 0.03);
		border: 1px solid rgba(255, 255, 255, 0.08);
		transition: border-color 0.18s ease, transform 0.18s ease;
	}

	.room-card-link:hover .room-card {
		border-color: rgba(169, 193, 238, 0.35);
		transform: translateY(-1px);
	}

	.room-card h3 {
		margin: 0;
		font-size: 0.92rem;
	}

	.room-card p {
		margin: 0.22rem 0 0.26rem;
		color: rgba(199, 207, 223, 0.84);
		font-size: 0.78rem;
	}

	.room-card small {
		color: rgba(147, 217, 189, 0.95);
		font-family: 'JetBrains Mono', monospace;
		font-size: 0.67rem;
		text-transform: uppercase;
	}

	.empty-state {
		padding: 0.7rem;
		border-radius: 11px;
		border: 1px dashed rgba(255, 255, 255, 0.2);
		background: rgba(255, 255, 255, 0.02);
		font-size: 0.8rem;
		color: rgba(187, 197, 216, 0.85);
	}

	.task-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 0.6rem 0.62rem;
		border-radius: 10px;
		background: rgba(255, 255, 255, 0.03);
		border: 1px solid rgba(255, 255, 255, 0.08);
		gap: 0.8rem;
	}

	.task-row strong {
		display: block;
		font-size: 0.82rem;
	}

	.task-row small {
		display: block;
		margin-top: 0.18rem;
		font-size: 0.67rem;
		color: rgba(189, 199, 218, 0.86);
	}

	.task-meta {
		text-align: right;
	}

	.task-meta span {
		display: inline-block;
		padding: 0.18rem 0.4rem;
		border-radius: 999px;
		font-size: 0.65rem;
		font-family: 'JetBrains Mono', monospace;
		border: 1px solid rgba(255, 255, 255, 0.2);
	}

	.priority-high .task-meta span {
		border-color: rgba(252, 96, 119, 0.5);
		color: #ffbac7;
	}

	.priority-medium .task-meta span {
		border-color: rgba(255, 214, 128, 0.45);
		color: #ffe5b0;
	}

	.priority-low .task-meta span {
		border-color: rgba(139, 225, 187, 0.44);
		color: #b5f0d7;
	}

	@media (max-width: 980px) {
		.dashboard-shell {
			padding-top: 5.1rem;
		}

		.dashboard-grid {
			grid-template-columns: 1fr;
		}
	}
</style>
