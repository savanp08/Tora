<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import { taskStore, type Task } from '$lib/stores/tasks';
	import { toStringValue } from '$lib/utils/chat/core';

	const dispatch = createEventDispatcher<{
		requestTaskEdit: { taskId: string };
	}>();

	type BudgetSegment = {
		key: 'done' | 'in_progress' | 'todo';
		label: string;
		amount: number;
		percentage: number;
	};

	type CostByTypeRow = {
		type: string;
		amount: number;
		percentage: number;
	};

	$: tasks = [...$taskStore];
	$: totalBudgetAllocated = tasks.reduce((sum, task) => sum + normalizeBudget(task.budget), 0);
	$: completedTaskBudget = sumBudgetByStatus(tasks, 'done');
	$: inProgressTaskBudget = sumBudgetByStatus(tasks, 'in_progress');
	$: todoTaskBudget = sumBudgetByStatus(tasks, 'todo');
	$: burnRateSegments = buildBurnRateSegments();
	$: costByTypeRows = buildCostByTypeRows();
	$: topExpensiveTasks = [...tasks]
		.filter((task) => normalizeBudget(task.budget) > 0)
		.sort((left, right) => normalizeBudget(right.budget) - normalizeBudget(left.budget))
		.slice(0, 5);

	function normalizeBudget(value: unknown) {
		if (typeof value === 'number' && Number.isFinite(value) && value > 0) {
			return value;
		}
		if (typeof value === 'string') {
			const parsed = Number(value.replace(/[^\d.\-]/g, ''));
			if (Number.isFinite(parsed) && parsed > 0) {
				return parsed;
			}
		}
		return 0;
	}

	function normalizeStatus(value: unknown): 'done' | 'in_progress' | 'todo' {
		const normalized = toStringValue(value).trim().toLowerCase().replace(/\s+/g, '_');
		if (normalized === 'done' || normalized === 'completed') {
			return 'done';
		}
		if (normalized === 'in_progress') {
			return 'in_progress';
		}
		return 'todo';
	}

	function parseDescriptionMetadata(description: string) {
		const trimmed = description.trim();
		if (!trimmed) {
			return [] as Array<{ key: string; value: string }>;
		}
		const metadataMatch = trimmed.match(/\[([^\]]+)\]\s*$/);
		if (!metadataMatch) {
			return [] as Array<{ key: string; value: string }>;
		}
		const entries: Array<{ key: string; value: string }> = [];
		for (const section of metadataMatch[1].split('|')) {
			const [rawLabel, ...rawValueParts] = section.split(':');
			const key = rawLabel?.trim().toLowerCase();
			const value = rawValueParts.join(':').trim();
			if (!key || !value) {
				continue;
			}
			entries.push({ key, value });
		}
		return entries;
	}

	function resolveTaskType(task: Task) {
		const entries = parseDescriptionMetadata(task.description || '');
		const typeFromMetadata = entries.find((entry) => entry.key === 'type')?.value || '';
		const normalized = typeFromMetadata.trim().toLowerCase();
		if (!normalized) {
			return 'General';
		}
		return normalized.replace(/[_-]+/g, ' ').replace(/\b\w/g, (char) => char.toUpperCase());
	}

	function sumBudgetByStatus(taskList: Task[], status: 'done' | 'in_progress' | 'todo') {
		return taskList.reduce((sum, task) => {
			if (normalizeStatus(task.status) !== status) {
				return sum;
			}
			return sum + normalizeBudget(task.budget);
		}, 0);
	}

	function buildBurnRateSegments(): BudgetSegment[] {
		const divisor = totalBudgetAllocated > 0 ? totalBudgetAllocated : 1;
		return [
			{
				key: 'done',
				label: 'Completed',
				amount: completedTaskBudget,
				percentage: (completedTaskBudget / divisor) * 100
			},
			{
				key: 'in_progress',
				label: 'In Progress',
				amount: inProgressTaskBudget,
				percentage: (inProgressTaskBudget / divisor) * 100
			},
			{
				key: 'todo',
				label: 'To Do',
				amount: todoTaskBudget,
				percentage: (todoTaskBudget / divisor) * 100
			}
		];
	}

	function buildCostByTypeRows(): CostByTypeRow[] {
		const spendByType = new Map<string, number>();
		for (const task of tasks) {
			const type = resolveTaskType(task);
			const current = spendByType.get(type) ?? 0;
			spendByType.set(type, current + normalizeBudget(task.budget));
		}
		const divisor = totalBudgetAllocated > 0 ? totalBudgetAllocated : 1;
		return [...spendByType.entries()]
			.map(([type, amount]) => ({
				type,
				amount,
				percentage: (amount / divisor) * 100
			}))
			.filter((entry) => entry.amount > 0)
			.sort((left, right) => right.amount - left.amount);
	}

	function formatMoney(value: number) {
		return value.toLocaleString(undefined, {
			style: 'currency',
			currency: 'USD',
			maximumFractionDigits: 2
		});
	}

	function openTaskEditor(taskID: string) {
		const normalized = taskID.trim();
		if (!normalized) {
			return;
		}
		dispatch('requestTaskEdit', { taskId: normalized });
	}
</script>

<section class="cost-panel" aria-label="Cost management">
	<header class="cost-header">
		<h3>Cost Management</h3>
		<p>Total budget allocated: <strong>{formatMoney(totalBudgetAllocated)}</strong></p>
	</header>

	<section class="burn-rate" aria-label="Burn rate">
		<div class="section-head">
			<h4>Burn Rate</h4>
			<span>{formatMoney(totalBudgetAllocated)}</span>
		</div>
		<div class="stacked-bar" role="presentation" aria-hidden="true">
			{#each burnRateSegments as segment (segment.key)}
				<div
					class={`stacked-segment ${segment.key}`}
					style={`width:${Math.max(0, segment.percentage)}%`}
					title={`${segment.label}: ${formatMoney(segment.amount)}`}
				></div>
			{/each}
		</div>
		<div class="segment-legend">
			{#each burnRateSegments as segment (segment.key)}
				<div class="legend-item">
					<span class={`swatch ${segment.key}`}></span>
					<div>
						<strong>{segment.label}</strong>
						<small>{formatMoney(segment.amount)} · {Math.round(segment.percentage)}%</small>
					</div>
				</div>
			{/each}
		</div>
	</section>

	<section class="cost-by-type" aria-label="Cost by type">
		<div class="section-head">
			<h4>Cost by Type</h4>
			<span>{costByTypeRows.length} categories</span>
		</div>
		{#if costByTypeRows.length === 0}
			<p class="section-empty">No task budgets found yet.</p>
		{:else}
			<div class="type-list">
				{#each costByTypeRows as entry (entry.type)}
					<div class="type-row">
						<div class="type-row-head">
							<strong>{entry.type}</strong>
							<span>{formatMoney(entry.amount)}</span>
						</div>
						<div class="type-track" role="presentation">
							<div class="type-fill" style={`width:${Math.max(0, entry.percentage)}%`}></div>
						</div>
						<small>{Math.round(entry.percentage)}% of total budget</small>
					</div>
				{/each}
			</div>
		{/if}
	</section>

	<section class="quick-edit" aria-label="Quick edit expensive tasks">
		<div class="section-head">
			<h4>Quick Edit</h4>
			<span>Top 5 expensive tasks</span>
		</div>
		{#if topExpensiveTasks.length === 0}
			<p class="section-empty">No budgeted tasks available for quick edit.</p>
		{:else}
			<div class="quick-edit-list">
				{#each topExpensiveTasks as task (task.id)}
					<button type="button" class="quick-task" on:click={() => openTaskEditor(task.id)}>
						<div>
							<strong>{task.title}</strong>
							<small>{normalizeStatus(task.status).replace(/_/g, ' ')}</small>
						</div>
						<span>{formatMoney(normalizeBudget(task.budget))}</span>
					</button>
				{/each}
			</div>
		{/if}
	</section>
</section>

<style>
	.cost-panel {
		height: 100%;
		min-height: 0;
		overflow: auto;
		display: grid;
		grid-template-rows: auto auto auto auto;
		gap: 0.75rem;
		padding-right: 0.2rem;
	}

	.cost-header h3 {
		margin: 0;
		font-size: 0.9rem;
	}

	.cost-header p {
		margin: 0.26rem 0 0;
		font-size: 0.74rem;
		color: var(--ws-muted);
	}

	.burn-rate,
	.cost-by-type,
	.quick-edit {
		border: 1px solid color-mix(in srgb, var(--ws-border) 90%, transparent);
		border-radius: 12px;
		padding: 0.68rem;
		background: color-mix(in srgb, var(--ws-surface) 88%, var(--ws-surface-soft));
		display: grid;
		gap: 0.58rem;
	}

	.section-head {
		display: flex;
		justify-content: space-between;
		align-items: baseline;
		gap: 0.5rem;
	}

	.section-head h4 {
		margin: 0;
		font-size: 0.78rem;
		font-weight: 700;
	}

	.section-head span {
		font-size: 0.7rem;
		color: var(--ws-muted);
	}

	.stacked-bar {
		display: flex;
		height: 11px;
		border-radius: 999px;
		overflow: hidden;
		background: color-mix(in srgb, var(--ws-border) 80%, transparent);
	}

	.stacked-segment {
		height: 100%;
	}

	.stacked-segment.done,
	.swatch.done {
		background: #22c55e;
	}

	.stacked-segment.in_progress,
	.swatch.in_progress {
		background: #f59e0b;
	}

	.stacked-segment.todo,
	.swatch.todo {
		background: #94a3b8;
	}

	.segment-legend {
		display: grid;
		grid-template-columns: repeat(3, minmax(0, 1fr));
		gap: 0.42rem;
	}

	.legend-item {
		display: flex;
		align-items: center;
		gap: 0.35rem;
	}

	.legend-item strong {
		display: block;
		font-size: 0.7rem;
	}

	.legend-item small {
		font-size: 0.67rem;
		color: var(--ws-muted);
	}

	.swatch {
		width: 0.58rem;
		height: 0.58rem;
		border-radius: 999px;
		flex-shrink: 0;
	}

	.type-list {
		display: grid;
		gap: 0.48rem;
	}

	.type-row {
		display: grid;
		gap: 0.28rem;
	}

	.type-row-head {
		display: flex;
		justify-content: space-between;
		gap: 0.5rem;
		font-size: 0.72rem;
	}

	.type-track {
		height: 7px;
		border-radius: 999px;
		background: color-mix(in srgb, var(--ws-border) 82%, transparent);
		overflow: hidden;
	}

	.type-fill {
		height: 100%;
		border-radius: inherit;
		background: linear-gradient(
			90deg,
			color-mix(in srgb, var(--ws-accent) 84%, #22d3ee),
			color-mix(in srgb, var(--ws-accent) 65%, #60a5fa)
		);
	}

	.type-row small {
		font-size: 0.66rem;
		color: var(--ws-muted);
	}

	.quick-edit-list {
		display: grid;
		gap: 0.4rem;
	}

	.quick-task {
		width: 100%;
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.6rem;
		text-align: left;
		border: 1px solid color-mix(in srgb, var(--ws-border) 90%, transparent);
		border-radius: 10px;
		padding: 0.5rem 0.56rem;
		background: color-mix(in srgb, var(--ws-surface) 96%, transparent);
		color: var(--ws-text);
		cursor: pointer;
		transition:
			border-color 0.2s ease,
			transform 0.15s ease;
	}

	.quick-task:hover {
		border-color: color-mix(in srgb, var(--ws-accent) 52%, var(--ws-border));
		transform: translateY(-1px);
	}

	.quick-task strong {
		display: block;
		font-size: 0.74rem;
	}

	.quick-task small {
		font-size: 0.66rem;
		color: var(--ws-muted);
		text-transform: capitalize;
	}

	.quick-task span {
		font-size: 0.72rem;
		font-weight: 700;
	}

	.section-empty {
		margin: 0;
		font-size: 0.72rem;
		color: var(--ws-muted);
	}

	@media (max-width: 760px) {
		.segment-legend {
			grid-template-columns: minmax(0, 1fr);
		}
	}
</style>
