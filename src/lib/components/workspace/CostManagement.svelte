<script lang="ts">
	import { projectTimeline } from '$lib/stores/timeline';
	import { taskStore } from '$lib/stores/tasks';

	$: totalTaskBudget = $taskStore.reduce((sum, task) => sum + (task.budget ?? 0), 0);
	$: totalTaskSpent = $taskStore.reduce((sum, task) => sum + (task.spent ?? 0), 0);
	$: declaredBudget = Number.isFinite($projectTimeline?.budget_total)
		? Number($projectTimeline?.budget_total)
		: 0;
	$: estimatedBudget = parseCurrencyLike($projectTimeline?.estimated_cost);
	$: budgetBaseline = declaredBudget > 0 ? declaredBudget : estimatedBudget;
	$: remainingBudget = budgetBaseline > 0 ? budgetBaseline - totalTaskSpent : totalTaskBudget - totalTaskSpent;
	$: spentPercent =
		budgetBaseline > 0 ? Math.min(100, Math.max(0, (totalTaskSpent / budgetBaseline) * 100)) : 0;

	function parseCurrencyLike(value: unknown) {
		if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
			return value;
		}
		if (typeof value === 'string') {
			const parsed = Number(value.replace(/[^\d.\-]/g, ''));
			if (Number.isFinite(parsed) && parsed >= 0) {
				return parsed;
			}
		}
		return 0;
	}

	function formatMoney(value: number) {
		return value.toLocaleString(undefined, {
			style: 'currency',
			currency: 'USD',
			maximumFractionDigits: 2
		});
	}
</script>

<section class="cost-panel" aria-label="Cost management">
	<div class="cost-grid">
		<article class="cost-card">
			<h3>Budget Baseline</h3>
			<strong>{formatMoney(Math.max(0, budgetBaseline))}</strong>
			<p>Project baseline from timeline settings.</p>
		</article>
		<article class="cost-card">
			<h3>Allocated in Tasks</h3>
			<strong>{formatMoney(Math.max(0, totalTaskBudget))}</strong>
			<p>Total budget currently assigned on tasks.</p>
		</article>
		<article class="cost-card">
			<h3>Spent</h3>
			<strong>{formatMoney(Math.max(0, totalTaskSpent))}</strong>
			<p>Actual spend captured from task records.</p>
		</article>
		<article class="cost-card" class:is-negative={remainingBudget < 0}>
			<h3>Remaining</h3>
			<strong>{formatMoney(remainingBudget)}</strong>
			<p>{remainingBudget < 0 ? 'Budget overrun detected.' : 'Projected runway at current spend.'}</p>
		</article>
	</div>

	<div class="cost-progress">
		<div class="cost-progress-head">
			<span>Spend utilization</span>
			<span>{Math.round(spentPercent)}%</span>
		</div>
		<div class="cost-track" role="presentation">
			<div class="cost-fill" style={`width:${spentPercent}%`}></div>
		</div>
	</div>
</section>

<style>
	.cost-panel {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto auto;
		gap: 0.72rem;
	}

	.cost-grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.55rem;
	}

	.cost-card {
		border: 1px solid color-mix(in srgb, var(--ws-border) 90%, transparent);
		border-radius: 12px;
		padding: 0.62rem;
		background: color-mix(in srgb, var(--ws-surface) 88%, var(--ws-surface-soft));
		display: grid;
		gap: 0.24rem;
	}

	.cost-card h3 {
		margin: 0;
		font-size: 0.74rem;
		color: var(--ws-muted);
		font-weight: 600;
	}

	.cost-card strong {
		font-size: 0.92rem;
		line-height: 1.2;
	}

	.cost-card p {
		margin: 0;
		font-size: 0.72rem;
		color: var(--ws-muted);
	}

	.cost-card.is-negative strong {
		color: var(--ws-danger);
	}

	.cost-progress {
		border: 1px solid color-mix(in srgb, var(--ws-border) 90%, transparent);
		border-radius: 12px;
		padding: 0.62rem;
		background: color-mix(in srgb, var(--ws-surface) 88%, var(--ws-surface-soft));
		display: grid;
		gap: 0.5rem;
	}

	.cost-progress-head {
		display: flex;
		justify-content: space-between;
		font-size: 0.74rem;
		color: var(--ws-muted);
	}

	.cost-track {
		height: 8px;
		border-radius: 999px;
		background: color-mix(in srgb, var(--ws-border) 80%, transparent);
		overflow: hidden;
	}

	.cost-fill {
		height: 100%;
		border-radius: inherit;
		background: linear-gradient(
			90deg,
			color-mix(in srgb, var(--ws-accent) 92%, #36d399),
			color-mix(in srgb, var(--ws-accent) 74%, #60a5fa)
		);
		transition: width 0.2s ease;
	}

	@media (max-width: 760px) {
		.cost-grid {
			grid-template-columns: minmax(0, 1fr);
		}
	}
</style>
