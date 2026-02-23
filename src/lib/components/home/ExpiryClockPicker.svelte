<script lang="ts">
	type QuickOption = {
		label: string;
		hours: number;
	};

	const QUICK_OPTIONS: QuickOption[] = [
		{ label: '1 Hour', hours: 1 },
		{ label: '6 Hours', hours: 6 },
		{ label: '12 Hours', hours: 12 },
		{ label: '1 Day', hours: 24 },
		{ label: '3 Days', hours: 72 },
		{ label: '7 Days', hours: 168 },
		{ label: '15 Days', hours: 360 }
	];
	const DEFAULT_HOURS = 24;

	export let valueHours = DEFAULT_HOURS;
	export let disabled = false;

	const optionHours = new Set(QUICK_OPTIONS.map((option) => option.hours));

	$: if (!optionHours.has(valueHours)) {
		valueHours = DEFAULT_HOURS;
	}

	$: selectedLabel =
		QUICK_OPTIONS.find((option) => option.hours === valueHours)?.label ?? `${valueHours} Hours`;

	function selectOption(hours: number) {
		if (disabled) {
			return;
		}
		valueHours = hours;
	}
</script>

<section class="expiry-picker" aria-label="Room expiry">
	<div class="meta-row">
		<span class="meta-label">Room expiry</span>
		<strong class="meta-value">{selectedLabel}</strong>
	</div>
	<div class="chip-row" role="radiogroup" aria-label="Room expiry options">
		{#each QUICK_OPTIONS as option (option.hours)}
			<button
				type="button"
				class="chip {valueHours === option.hours ? 'active' : ''}"
				role="radio"
				aria-checked={valueHours === option.hours}
				{disabled}
				on:click={() => selectOption(option.hours)}
			>
				{option.label}
			</button>
		{/each}
	</div>
</section>

<style>
	.expiry-picker {
		display: flex;
		flex-direction: column;
		gap: 0.55rem;
	}

	.meta-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
	}

	.meta-label {
		font-size: 0.82rem;
		font-weight: 600;
		color: #475569;
	}

	.meta-value {
		font-size: 0.82rem;
		font-weight: 700;
		color: #1f2937;
	}

	.chip-row {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}

	.chip {
		border: 1px solid #cbd5e1;
		background: #f8fafc;
		color: #334155;
		border-radius: 999px;
		padding: 0.4rem 0.72rem;
		font-size: 0.78rem;
		font-weight: 600;
		line-height: 1.1;
		cursor: pointer;
	}

	.chip:hover:not(:disabled) {
		background: #eff6ff;
		border-color: #93c5fd;
	}

	.chip.active {
		background: #2563eb;
		border-color: #2563eb;
		color: #ffffff;
	}

	.chip:disabled {
		opacity: 0.65;
		cursor: not-allowed;
	}
</style>
