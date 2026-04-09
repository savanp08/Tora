<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import {
		formatTaskAdvisoryTimeLabel,
		severityLabel,
		taskAdvisories,
		taskAdvisoryCounts,
		type TaskAdvisory,
		type TaskAdvisorySeverity
	} from '$lib/stores/taskAdvisories';

	const dispatch = createEventDispatcher<{ openTask: { taskId: string } }>();
	const MAX_VISIBLE_ADVISORIES = 10;

	function severityClass(severity: TaskAdvisorySeverity) {
		if (severity === 'critical') return 'tone-critical';
		if (severity === 'warning') return 'tone-warning';
		return 'tone-info';
	}

	function openTask(taskId: string) {
		const normalizedTaskId = taskId.trim();
		if (!normalizedTaskId) {
			return;
		}
		dispatch('openTask', { taskId: normalizedTaskId });
	}

	$: visibleAdvisories = $taskAdvisories.slice(0, MAX_VISIBLE_ADVISORIES);
	$: extraCount = Math.max(0, $taskAdvisories.length - visibleAdvisories.length);
</script>

<section class="workspace-ai-alerts" aria-label="AI suggestions and alerts">
	<header class="alerts-header">
		<div>
			<p class="alerts-kicker">AI Suggestions</p>
			<h3>Task alerts</h3>
		</div>
		{#if $taskAdvisoryCounts.total > 0}
			<span class="alerts-badge">{$taskAdvisoryCounts.total}</span>
		{/if}
	</header>

	{#if $taskAdvisoryCounts.total > 0}
		<div class="alerts-summary">
			{#if $taskAdvisoryCounts.critical > 0}
				<span class="summary-pill critical">{$taskAdvisoryCounts.critical} critical</span>
			{/if}
			{#if $taskAdvisoryCounts.warning > 0}
				<span class="summary-pill warning">{$taskAdvisoryCounts.warning} warning</span>
			{/if}
			{#if $taskAdvisoryCounts.info > 0}
				<span class="summary-pill info">{$taskAdvisoryCounts.info} info</span>
			{/if}
		</div>

		<ol class="alerts-list">
			{#each visibleAdvisories as advisory (advisory.id)}
				<li class="alerts-item">
					<div class="alerts-item-top">
						<span class={`severity-pill ${severityClass(advisory.severity)}`}>
							{severityLabel(advisory.severity)}
						</span>
						{#if advisory.dueAt}
							<span class="due-pill">{formatTaskAdvisoryTimeLabel(advisory.dueAt)}</span>
						{/if}
					</div>
					<button type="button" class="task-link" on:click={() => openTask(advisory.taskId)}>
						{advisory.taskTitle}
					</button>
					<p class="headline">{advisory.headline}</p>
					<p class="summary">{advisory.summary}</p>
					<p class="detail"><strong>Suggestion:</strong> {advisory.suggestion}</p>
					<p class="detail risk"><strong>Risk:</strong> {advisory.risk}</p>
				</li>
			{/each}
		</ol>

		{#if extraCount > 0}
			<p class="alerts-more">+{extraCount} more AI alerts are available as task conditions change.</p>
		{/if}
	{:else}
		<div class="alerts-empty">
			<p>No AI alerts right now.</p>
			<p>We’ll surface blockers, deadline pressure, checklist risk, and upcoming start windows here.</p>
		</div>
	{/if}
</section>

<style>
	.workspace-ai-alerts {
		display: grid;
		gap: 0.7rem;
		padding: 0.9rem 0.9rem 0.2rem;
		border-bottom: 1px solid var(--ws-border);
		background:
			radial-gradient(circle at top right, rgba(240, 173, 78, 0.12), transparent 42%),
			linear-gradient(180deg, color-mix(in srgb, var(--ws-surface) 92%, #fff6e5), var(--ws-surface));
	}

	.alerts-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 0.8rem;
	}

	.alerts-kicker {
		margin: 0 0 0.18rem;
		font-size: 0.64rem;
		font-weight: 800;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: #c26a00;
	}

	.alerts-header h3 {
		margin: 0;
		font-size: 0.92rem;
		font-weight: 700;
		color: var(--ws-text);
	}

	.alerts-badge {
		min-width: 1.5rem;
		height: 1.5rem;
		padding: 0 0.4rem;
		border-radius: 999px;
		background: #c26a00;
		color: #fff;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		font-size: 0.72rem;
		font-weight: 800;
	}

	.alerts-summary {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem;
	}

	.summary-pill,
	.severity-pill,
	.due-pill {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		border-radius: 999px;
		font-size: 0.66rem;
		font-weight: 700;
		letter-spacing: 0.01em;
	}

	.summary-pill {
		padding: 0.2rem 0.48rem;
		border: 1px solid transparent;
	}

	.summary-pill.critical,
	.severity-pill.tone-critical {
		background: rgba(220, 38, 38, 0.12);
		border-color: rgba(220, 38, 38, 0.24);
		color: #b42318;
	}

	.summary-pill.warning,
	.severity-pill.tone-warning {
		background: rgba(217, 119, 6, 0.12);
		border-color: rgba(217, 119, 6, 0.24);
		color: #b45309;
	}

	.summary-pill.info,
	.severity-pill.tone-info {
		background: rgba(37, 99, 235, 0.12);
		border-color: rgba(37, 99, 235, 0.24);
		color: #1d4ed8;
	}

	.alerts-list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: 0.55rem;
	}

	.alerts-item {
		display: grid;
		gap: 0.26rem;
		padding: 0.72rem;
		border-radius: 14px;
		border: 1px solid color-mix(in srgb, var(--ws-border) 82%, rgba(255, 255, 255, 0.4));
		background: color-mix(in srgb, var(--ws-surface) 90%, white);
		box-shadow: 0 10px 24px rgba(15, 23, 42, 0.06);
	}

	.alerts-item-top {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	.severity-pill {
		padding: 0.18rem 0.46rem;
		border: 1px solid transparent;
	}

	.due-pill {
		padding: 0.16rem 0.42rem;
		background: rgba(15, 23, 42, 0.05);
		color: var(--ws-muted);
	}

	.task-link {
		padding: 0;
		border: none;
		background: transparent;
		color: var(--ws-text);
		font: inherit;
		font-size: 0.86rem;
		font-weight: 700;
		text-align: left;
		cursor: pointer;
	}

	.task-link:hover {
		text-decoration: underline;
		text-underline-offset: 0.14rem;
	}

	.headline,
	.summary,
	.detail,
	.alerts-more,
	.alerts-empty p {
		margin: 0;
	}

	.headline {
		font-size: 0.78rem;
		font-weight: 700;
		color: var(--ws-text);
	}

	.summary,
	.detail,
	.alerts-more,
	.alerts-empty p {
		font-size: 0.74rem;
		line-height: 1.45;
		color: var(--ws-muted);
	}

	.risk strong,
	.detail strong {
		color: var(--ws-text);
	}

	.alerts-empty {
		display: grid;
		gap: 0.3rem;
		padding-bottom: 0.3rem;
	}

	.alerts-more {
		padding-bottom: 0.2rem;
	}
</style>
