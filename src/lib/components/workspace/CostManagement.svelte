<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import { taskStore, type Task } from '$lib/stores/tasks';
	import { toStringValue } from '$lib/utils/chat/core';

	const dispatch = createEventDispatcher<{
		requestTaskEdit: { taskId: string };
	}>();
	export let canEdit = true;
	export let isAdmin = false;
	export let roomId = '';
	export let sessionUserID = '';
	export let sessionUserName = '';
	import ChangeRequestModal from './ChangeRequestModal.svelte';
	import { type ChangeRequestAction } from '$lib/stores/changeRequests';

	let crModalOpen = false;
	let crModalAction: ChangeRequestAction = 'edit_cost';
	let crModalTargetLabel = '';
	let crModalPayload: Record<string, unknown> = {};

	function openCR(action: ChangeRequestAction, targetLabel: string, payload: Record<string, unknown> = {}) {
		crModalAction = action;
		crModalTargetLabel = targetLabel;
		crModalPayload = payload;
		crModalOpen = true;
	}

	// ── Cost calculation methods ─────────────────────────────────
	type CostMethod = 'fixed' | 'hourly' | 'time_materials' | 'value_based' | 'retainer';

	type CostMethodMeta = {
		key: CostMethod;
		label: string;
		description: string;
		unit: string; // what the "budget" field represents
	};

	const COST_METHODS: CostMethodMeta[] = [
		{
			key: 'fixed',
			label: 'Fixed Price',
			description: 'Total agreed cost per task regardless of time spent.',
			unit: 'Fixed ($)'
		},
		{
			key: 'hourly',
			label: 'Hourly Rate',
			description: 'Cost based on hours × rate. Budget field = estimated hours.',
			unit: 'Hours'
		},
		{
			key: 'time_materials',
			label: 'Time & Materials',
			description: 'Actual hours + materials billed. Budget = approved ceiling.',
			unit: 'Budget ceiling ($)'
		},
		{
			key: 'value_based',
			label: 'Value-Based',
			description: 'Price set by delivered value, not hours. Budget = agreed value.',
			unit: 'Value ($)'
		},
		{
			key: 'retainer',
			label: 'Retainer',
			description: 'Recurring fee for ongoing availability. Budget = monthly retainer.',
			unit: 'Monthly ($)'
		}
	];

	// Common profession presets: (method, hourly rate or multiplier)
	type Preset = { label: string; method: CostMethod; rateHint: string };
	const PROFESSION_PRESETS: Preset[] = [
		{ label: 'Software Engineer', method: 'hourly', rateHint: '$80–150/hr' },
		{ label: 'Designer (UI/UX)', method: 'hourly', rateHint: '$60–120/hr' },
		{ label: 'Product Manager', method: 'retainer', rateHint: '$5k–12k/mo' },
		{ label: 'DevOps / Infra', method: 'time_materials', rateHint: '$90–160/hr' },
		{ label: 'Consultant', method: 'value_based', rateHint: 'Project-based' },
		{ label: 'Fixed Contract', method: 'fixed', rateHint: 'Per milestone' }
	];

	let selectedMethod: CostMethod = 'fixed';
	let methodPickerOpen = false;

	$: currentMethodMeta = COST_METHODS.find((m) => m.key === selectedMethod) ?? COST_METHODS[0];

	// ── Budget aggregation ───────────────────────────────────────
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
		const customFieldType = readTaskTypeFromCustomFields(task.customFields);
		if (customFieldType) {
			return customFieldType;
		}
		const entries = parseDescriptionMetadata(task.description || '');
		const typeFromMetadata = entries.find((entry) => entry.key === 'type')?.value || '';
		if (!typeFromMetadata.trim()) {
			return 'General';
		}
		return toTitleLabel(typeFromMetadata);
	}

	function readTaskTypeFromCustomFields(fields: Task['customFields']) {
		if (!fields || typeof fields !== 'object') {
			return '';
		}
		for (const key of ['type', 'task_type', 'taskType', 'category', 'workstream']) {
			const candidate = toStringValue((fields as Record<string, unknown>)[key]).trim();
			if (!candidate) {
				continue;
			}
			return toTitleLabel(candidate);
		}
		return '';
	}

	function toTitleLabel(rawValue: string) {
		const normalized = rawValue.trim().toLowerCase();
		if (!normalized) return 'General';
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

	function formatAmount(value: number) {
		if (selectedMethod === 'hourly') {
			// display as hours
			return `${value.toLocaleString(undefined, { maximumFractionDigits: 1 })} hrs`;
		}
		return value.toLocaleString(undefined, {
			style: 'currency',
			currency: 'USD',
			maximumFractionDigits: 2
		});
	}

	function formatMoney(value: number) {
		return value.toLocaleString(undefined, {
			style: 'currency',
			currency: 'USD',
			maximumFractionDigits: 2
		});
	}

	function openTaskEditor(taskID: string) {
		if (!canEdit) {
			return;
		}
		const normalized = taskID.trim();
		if (!normalized) {
			return;
		}
		dispatch('requestTaskEdit', { taskId: normalized });
	}

	function applyPreset(preset: Preset) {
		selectedMethod = preset.method;
		methodPickerOpen = false;
	}
</script>

<section class="cost-panel" aria-label="Cost management">
	<!-- ── Method selector ──────────────────────────────────── -->
	<div class="method-selector">
		<div class="method-selector-header">
			<div>
				<h3>Cost Management</h3>
				<p class="method-subtitle">
					Method: <strong>{currentMethodMeta.label}</strong> · {currentMethodMeta.unit}
				</p>
			</div>
			<button
				type="button"
				class="method-toggle-btn"
				class:is-open={methodPickerOpen}
				on:click={() => (methodPickerOpen = !methodPickerOpen)}
				aria-expanded={methodPickerOpen}
			>
				<svg viewBox="0 0 24 24" aria-hidden="true"
					><path
						d="M9.8 8.2 8.4 5.9l1.4-1.4 2.3 1.4a5.7 5.7 0 0 1 1.8 0l2.3-1.4 1.4 1.4-1.4 2.3c.2.6.3 1.2.3 1.8s-.1 1.2-.3 1.8l1.4 2.3-1.4 1.4-2.3-1.4a5.7 5.7 0 0 1-1.8 0l-2.3 1.4-1.4-1.4 1.4-2.3a5.7 5.7 0 0 1 0-3.6ZM12 14.2a2.2 2.2 0 1 0 0-4.4 2.2 2.2 0 0 0 0 4.4Z"
					></path></svg
				>
				<span>Method</span>
			</button>
		</div>

		{#if methodPickerOpen}
			<div class="method-picker">
				<div class="method-picker-section-label">Calculation methods</div>
				<div class="method-list">
					{#each COST_METHODS as method (method.key)}
						<button
							type="button"
							class="method-option"
							class:is-selected={selectedMethod === method.key}
							on:click={() => {
								selectedMethod = method.key;
							}}
						>
							<div class="method-option-head">
								<strong>{method.label}</strong>
								<span class="method-option-unit">{method.unit}</span>
							</div>
							<p class="method-option-desc">{method.description}</p>
						</button>
					{/each}
				</div>
				<div class="method-picker-section-label" style="margin-top:0.55rem">Profession presets</div>
				<div class="preset-list">
					{#each PROFESSION_PRESETS as preset (preset.label)}
						<button
							type="button"
							class="preset-chip"
							class:is-active={selectedMethod === preset.method}
							on:click={() => applyPreset(preset)}
							title={preset.rateHint}
						>
							<span>{preset.label}</span>
							<small>{preset.rateHint}</small>
						</button>
					{/each}
				</div>
			</div>
		{/if}
	</div>

	<!-- ── Burn rate ────────────────────────────────────────── -->
	<section class="burn-rate" aria-label="Burn rate">
		<div class="section-head">
			<h4>Burn Rate</h4>
			<span>{formatAmount(totalBudgetAllocated)}</span>
		</div>
		<div class="stacked-bar" role="presentation" aria-hidden="true">
			{#each burnRateSegments as segment (segment.key)}
				<div
					class={`stacked-segment ${segment.key}`}
					style={`width:${Math.max(0, segment.percentage)}%`}
					title={`${segment.label}: ${formatAmount(segment.amount)}`}
				></div>
			{/each}
		</div>
		<div class="segment-legend">
			{#each burnRateSegments as segment (segment.key)}
				<div class="legend-item">
					<span class={`swatch ${segment.key}`}></span>
					<div>
						<strong>{segment.label}</strong>
						<small>{formatAmount(segment.amount)} · {Math.round(segment.percentage)}%</small>
					</div>
				</div>
			{/each}
		</div>
	</section>

	<!-- ── Cost by type ─────────────────────────────────────── -->
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
							<span>{formatAmount(entry.amount)}</span>
						</div>
						<div class="type-track" role="presentation">
							<div class="type-fill" style={`width:${Math.max(0, entry.percentage)}%`}></div>
						</div>
						<small>{Math.round(entry.percentage)}% of total</small>
					</div>
				{/each}
			</div>
		{/if}
	</section>

	<!-- ── Quick edit ────────────────────────────────────────── -->
	<section class="quick-edit" aria-label="Quick edit expensive tasks">
		<div class="section-head">
			<h4>Quick Edit</h4>
			<span>Top 5 by budget</span>
		</div>
		{#if topExpensiveTasks.length === 0}
			<p class="section-empty">No budgeted tasks available.</p>
		{:else}
			<div class="quick-edit-list">
				{#each topExpensiveTasks as task (task.id)}
					<button
						type="button"
						class="quick-task"
						disabled={!canEdit}
						on:click={() => openTaskEditor(task.id)}
					>
						<div>
							<strong>{task.title}</strong>
							<small>{normalizeStatus(task.status).replace(/_/g, ' ')}</small>
						</div>
						<span>{formatAmount(normalizeBudget(task.budget))}</span>
					</button>
				{/each}
			</div>
		{/if}
	</section>

	<!-- ── Method info footer ───────────────────────────────── -->
	<div class="method-info-footer">
		<svg viewBox="0 0 24 24" aria-hidden="true"
			><circle cx="12" cy="12" r="10"></circle><path d="M12 16v-4M12 8h.01"></path></svg
		>
		<p>{currentMethodMeta.description}</p>
	</div>
</section>

<ChangeRequestModal
	open={crModalOpen}
	{roomId}
	userId={sessionUserID}
	userName={sessionUserName}
	action={crModalAction}
	targetLabel={crModalTargetLabel}
	payload={crModalPayload}
	on:submitted={() => (crModalOpen = false)}
	on:cancel={() => (crModalOpen = false)}
/>

<style>
	.cost-panel {
		height: 100%;
		min-height: 0;
		overflow: auto;
		display: grid;
		grid-template-rows: auto auto auto auto auto;
		gap: 0.75rem;
		padding-right: 0.2rem;
	}

	/* ── Method selector ──────────────────────────────────────── */
	.method-selector {
		border: 1px solid color-mix(in srgb, var(--ws-border) 90%, transparent);
		border-radius: 12px;
		padding: 0.68rem;
		background: color-mix(in srgb, var(--ws-surface) 88%, var(--ws-surface-soft));
	}

	.method-selector-header {
		display: flex;
		justify-content: space-between;
		align-items: flex-start;
		gap: 0.5rem;
	}

	.method-selector-header h3 {
		margin: 0;
		font-size: 0.9rem;
	}

	.method-subtitle {
		margin: 0.22rem 0 0;
		font-size: 0.72rem;
		color: var(--ws-muted);
	}

	.method-toggle-btn {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
		padding: 0.3rem 0.6rem;
		border: 1px solid var(--ws-border);
		border-radius: 8px;
		background: var(--ws-surface);
		color: var(--ws-muted);
		font-size: 0.72rem;
		font-weight: 600;
		cursor: pointer;
		flex-shrink: 0;
		transition:
			background 0.15s ease,
			color 0.15s ease,
			border-color 0.15s ease;
	}

	.method-request-btn {
		color: #d97706;
		border-color: color-mix(in srgb, #f59e0b 45%, transparent);
		background: color-mix(in srgb, #f59e0b 10%, transparent);
	}
	.method-request-btn:hover {
		background: color-mix(in srgb, #f59e0b 18%, transparent);
	}

	.method-toggle-btn svg {
		width: 0.8rem;
		height: 0.8rem;
		stroke: currentColor;
		fill: none;
		stroke-width: 1.8;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.method-toggle-btn:hover,
	.method-toggle-btn.is-open {
		color: var(--ws-accent);
		border-color: color-mix(in srgb, var(--ws-accent) 40%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 60%, var(--ws-surface));
	}

	.method-picker {
		margin-top: 0.65rem;
		padding-top: 0.58rem;
		border-top: 1px solid color-mix(in srgb, var(--ws-border) 70%, transparent);
		display: grid;
		gap: 0.28rem;
	}

	.method-picker-section-label {
		font-size: 0.65rem;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.07em;
		color: var(--ws-muted);
		padding: 0.1rem 0 0.15rem;
		opacity: 0.8;
	}

	.method-list {
		display: grid;
		gap: 0.28rem;
	}

	.method-option {
		width: 100%;
		text-align: left;
		padding: 0.48rem 0.56rem;
		border: 1px solid color-mix(in srgb, var(--ws-border) 80%, transparent);
		border-radius: 9px;
		background: color-mix(in srgb, var(--ws-surface) 92%, transparent);
		color: var(--ws-text);
		cursor: pointer;
		transition:
			border-color 0.15s ease,
			background 0.15s ease;
	}

	.method-option:hover {
		border-color: color-mix(in srgb, var(--ws-accent) 40%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 40%, var(--ws-surface));
	}

	.method-option.is-selected {
		border-color: color-mix(in srgb, var(--ws-accent) 60%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 75%, var(--ws-surface));
	}

	.method-option-head {
		display: flex;
		justify-content: space-between;
		align-items: baseline;
		gap: 0.4rem;
	}

	.method-option-head strong {
		font-size: 0.74rem;
	}

	.method-option-unit {
		font-size: 0.66rem;
		color: var(--ws-muted);
	}

	.method-option-desc {
		margin: 0.18rem 0 0;
		font-size: 0.67rem;
		color: var(--ws-muted);
		line-height: 1.4;
	}

	.preset-list {
		display: flex;
		flex-wrap: wrap;
		gap: 0.28rem;
	}

	.preset-chip {
		display: inline-flex;
		flex-direction: column;
		align-items: flex-start;
		padding: 0.28rem 0.52rem;
		border: 1px solid var(--ws-border);
		border-radius: 999px;
		background: var(--ws-surface);
		color: var(--ws-muted);
		cursor: pointer;
		transition:
			background 0.15s ease,
			color 0.15s ease,
			border-color 0.15s ease;
	}

	.preset-chip span {
		font-size: 0.7rem;
		font-weight: 600;
	}

	.preset-chip small {
		font-size: 0.62rem;
		color: var(--ws-muted);
	}

	.preset-chip:hover,
	.preset-chip.is-active {
		color: var(--ws-accent);
		border-color: color-mix(in srgb, var(--ws-accent) 50%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 70%, var(--ws-surface));
	}

	/* ── Shared section card ──────────────────────────────────── */
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

	.quick-task:disabled {
		opacity: 0.62;
		cursor: not-allowed;
		transform: none;
		border-color: color-mix(in srgb, var(--ws-border) 90%, transparent);
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
		flex-shrink: 0;
	}

	/* ── Method info footer ──────────────────────────────────── */
	.method-info-footer {
		display: flex;
		align-items: flex-start;
		gap: 0.4rem;
		padding: 0.5rem 0.6rem;
		border-radius: 9px;
		background: color-mix(in srgb, var(--ws-accent-soft) 35%, transparent);
		border: 1px solid color-mix(in srgb, var(--ws-accent) 22%, var(--ws-border));
	}

	.method-info-footer svg {
		width: 0.88rem;
		height: 0.88rem;
		stroke: var(--ws-accent);
		fill: none;
		stroke-width: 2;
		stroke-linecap: round;
		flex-shrink: 0;
		margin-top: 0.06rem;
	}

	.method-info-footer p {
		margin: 0;
		font-size: 0.7rem;
		color: var(--ws-muted);
		line-height: 1.45;
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
