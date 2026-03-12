<script lang="ts">
	import {
		boardActivity,
		formatTimeAgo,
		type BoardActivityType
	} from '$lib/stores/boardActivity';

	// Icon/color mappings per event type
	function eventIcon(type: BoardActivityType): string {
		switch (type) {
			case 'task_completed': return '✓';
			case 'task_added':    return '+';
			case 'task_modified': return '✎';
			case 'task_deleted':  return '−';
			case 'task_moved':    return '⇄';
			case 'board_cleared': return '⟲';
			case 'sprint_started': return '▶';
			case 'budget_update': return '$';
			case 'board_generated': return '✦';
			case 'board_edited':  return '✦';
			default: return '·';
		}
	}

	function eventClass(type: BoardActivityType): string {
		switch (type) {
			case 'task_completed': return 'ev-done';
			case 'task_added':    return 'ev-add';
			case 'task_modified': return 'ev-mod';
			case 'task_deleted':  return 'ev-del';
			case 'task_moved':    return 'ev-move';
			case 'board_cleared': return 'ev-clear';
			case 'sprint_started': return 'ev-sprint';
			case 'budget_update': return 'ev-budget';
			case 'board_generated':
			case 'board_edited':  return 'ev-ai';
			default: return 'ev-default';
		}
	}
</script>

<aside class="workspace-activity-panel" aria-label="Activity feed">
	<header class="feed-header">
		<span class="feed-icon" aria-hidden="true">
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
				stroke-linecap="round" stroke-linejoin="round">
				<path d="M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9"/>
				<path d="M13.73 21a2 2 0 0 1-3.46 0"/>
			</svg>
		</span>
		<h3>Activity</h3>
		{#if $boardActivity.length > 0}
			<span class="feed-badge">{$boardActivity.length}</span>
		{/if}
	</header>

	<div class="feed-scroll">
		{#if $boardActivity.length === 0}
			<div class="feed-empty">
				<p>No activity yet.</p>
				<p>Events appear here when tasks are created, updated, or the AI modifies the board.</p>
			</div>
		{:else}
			<ol class="feed-list">
				{#each $boardActivity as event (event.id)}
					<li class="feed-item">
						<span class="ev-dot {eventClass(event.type)}" aria-hidden="true">
							{eventIcon(event.type)}
						</span>
						<div class="ev-body">
							<p class="ev-title">{event.title}</p>
							{#if event.subtitle}
								<p class="ev-sub">{event.subtitle}</p>
							{/if}
							{#if event.note}
								<p class="ev-note">"{event.note}"</p>
							{/if}
							<div class="ev-meta">
								{#if event.actor}
									<span class="ev-actor">{event.actor}</span>
									<span class="ev-sep">·</span>
								{/if}
								<time class="ev-time">{formatTimeAgo(event.timestamp)}</time>
							</div>
						</div>
					</li>
				{/each}
			</ol>
		{/if}
	</div>
</aside>

<style>
	/* ── Theme tokens ────────────────────────────────────────────────── */
	:global(:root) {
		--af-bg: var(--workspace-taskboard-bg, var(--ws-bg, var(--bg-secondary, #f1f1f1)));
		--af-border: var(
			--workspace-taskboard-column-border,
			var(--ws-border, var(--border-subtle, #e1e1e1))
		);
		--af-text: var(--workspace-taskboard-item-text, var(--ws-text, var(--text-primary, #141414)));
		--af-muted: var(
			--workspace-taskboard-meta,
			var(--ws-muted, var(--text-tertiary, #747474))
		);
		--af-item-bg: var(
			--workspace-taskboard-item-bg,
			var(--ws-surface, var(--surface-primary, #ffffff))
		);
		--af-item-border: var(--workspace-taskboard-item-border, var(--af-border));
		--af-header-text: var(--af-text);
	}

	/* ── Panel shell ─────────────────────────────────────────────────── */
	.workspace-activity-panel {
		height: 100%;
		min-height: 0;
		display: flex;
		flex-direction: column;
		gap: 0;
		background: var(--af-bg);
		border-right: 0;
	}

	:global(:root[data-theme='dark']) .workspace-activity-panel,
	:global(.theme-dark) .workspace-activity-panel {
		--af-bg: var(--workspace-taskboard-bg, #171717);
		--af-border: var(--workspace-taskboard-column-border, #33333a);
		--af-text: var(--workspace-taskboard-item-text, #f5f5f8);
		--af-muted: var(--workspace-taskboard-meta, #a2a2ab);
		--af-item-bg: var(--workspace-taskboard-item-bg, #222226);
		--af-item-border: var(--workspace-taskboard-item-border, #3d3d43);
		--af-header-text: var(--af-text);
		background: var(--af-bg);
	}

	/* ── Header ──────────────────────────────────────────────────────── */
	.feed-header {
		flex-shrink: 0;
		padding: 0.72rem 0.76rem 0.6rem;
		display: flex;
		align-items: center;
		gap: 0.46rem;
		border-bottom: 1px solid var(--af-border);
	}

	.feed-icon {
		display: flex;
		align-items: center;
		color: var(--af-muted);
	}

	.feed-icon svg {
		width: 0.94rem;
		height: 0.94rem;
	}

	.feed-header h3 {
		margin: 0;
		flex: 1;
		font-size: 0.74rem;
		font-weight: 700;
		letter-spacing: 0.07em;
		text-transform: uppercase;
		color: var(--af-header-text);
	}

	.feed-badge {
		font-size: 0.62rem;
		padding: 0.08rem 0.36rem;
		border-radius: 999px;
		background: rgba(122, 181, 255, 0.2);
		border: 1px solid rgba(122, 181, 255, 0.35);
		color: #a8cbff;
	}

	/* ── Scrollable list ─────────────────────────────────────────────── */
	.feed-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 0.52rem 0.56rem;
	}

	.feed-empty {
		padding: 0.6rem 0.3rem;
		display: grid;
		gap: 0.3rem;
	}

	.feed-empty p {
		margin: 0;
		font-size: 0.74rem;
		line-height: 1.42;
		color: var(--af-muted);
	}

	.feed-list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: 0.4rem;
	}

	/* ── Feed item ───────────────────────────────────────────────────── */
	.feed-item {
		display: flex;
		gap: 0.46rem;
		align-items: flex-start;
		padding: 0.48rem 0.52rem;
		border-radius: 9px;
		border: 1px solid var(--af-item-border);
		background: var(--af-item-bg);
	}

	/* ── Event dot ───────────────────────────────────────────────────── */
	.ev-dot {
		flex-shrink: 0;
		width: 1.3rem;
		height: 1.3rem;
		border-radius: 50%;
		display: grid;
		place-items: center;
		font-size: 0.64rem;
		font-weight: 700;
		border: 1px solid transparent;
	}

	.ev-done    { background: rgba(95, 209, 139, 0.18); border-color: rgba(95, 209, 139, 0.4); color: #5fd18b; }
	.ev-add     { background: rgba(122, 181, 255, 0.18); border-color: rgba(122, 181, 255, 0.4); color: #7ab5ff; }
	.ev-mod     { background: rgba(247, 178, 79, 0.18); border-color: rgba(247, 178, 79, 0.4); color: #f7b24f; }
	.ev-del     { background: rgba(239, 68, 68, 0.18); border-color: rgba(239, 68, 68, 0.38); color: #fca5a5; }
	.ev-move    { background: rgba(155, 120, 255, 0.18); border-color: rgba(155, 120, 255, 0.4); color: #b09aff; }
	.ev-clear   { background: rgba(123, 132, 153, 0.18); border-color: rgba(123, 132, 153, 0.36); color: #b9c3d8; }
	.ev-sprint  { background: rgba(65, 199, 199, 0.18); border-color: rgba(65, 199, 199, 0.4); color: #41c7c7; }
	.ev-budget  { background: rgba(239, 68, 68, 0.15); border-color: rgba(239, 68, 68, 0.38); color: #fca5a5; }
	.ev-ai      { background: rgba(122, 181, 255, 0.22); border-color: rgba(122, 181, 255, 0.52); color: #a8cbff; }
	.ev-default { background: rgba(138, 154, 179, 0.12); border-color: rgba(138, 154, 179, 0.28); color: #8a9ab3; }

	/* ── Event body ──────────────────────────────────────────────────── */
	.ev-body {
		display: grid;
		gap: 0.14rem;
		min-width: 0;
	}

	.ev-title {
		margin: 0;
		font-size: 0.74rem;
		font-weight: 500;
		color: var(--af-text);
		line-height: 1.3;
	}

	.ev-sub {
		margin: 0;
		font-size: 0.68rem;
		color: var(--af-muted);
		overflow: hidden;
		white-space: nowrap;
		text-overflow: ellipsis;
	}

	.ev-note {
		margin: 0;
		font-size: 0.68rem;
		color: var(--af-muted);
		font-style: italic;
		line-height: 1.35;
	}

	.ev-meta {
		display: flex;
		gap: 0.26rem;
		align-items: center;
		font-size: 0.64rem;
		color: var(--af-muted);
		margin-top: 0.06rem;
	}

	.ev-actor {
		font-weight: 600;
	}

	.ev-sep {
		opacity: 0.5;
	}

	.ev-time {
		opacity: 0.8;
	}
</style>
