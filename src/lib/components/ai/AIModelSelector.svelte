<script lang="ts">
	import { onMount, createEventDispatcher } from 'svelte';
	import {
		aiSettings,
		availableModels,
		effortLevels,
		fetchAvailableModels,
		type AIEffortLevel,
		type AIModelInfo
	} from '$lib/stores/aiSettings';

	// When true renders as a minimal icon-only pill (no label text).
	export let compact = true;

	const dispatch = createEventDispatcher<{
		change: { modelId: string; effort: AIEffortLevel };
	}>();

	let open = false;
	let wrapEl: HTMLDivElement | null = null;

	$: settings = $aiSettings;
	$: models = $availableModels;
	$: selectedModel = models.find((m) => m.id === settings.modelId) ?? null;
	$: currentEffort = effortLevels.find((e) => e.id === settings.effort) ?? effortLevels[1];

	// Group models by provider for display.
	type ProviderGroup = { provider: string; models: AIModelInfo[] };
	$: providerGroups = (() => {
		const map = new Map<string, AIModelInfo[]>();
		for (const m of models) {
			if (!map.has(m.provider)) map.set(m.provider, []);
			map.get(m.provider)!.push(m);
		}
		const groups: ProviderGroup[] = [];
		for (const [provider, list] of map) {
			groups.push({ provider, models: list });
		}
		return groups;
	})();

	const providerLabel: Record<string, string> = {
		vertex: 'Gemini',
		gemini: 'Gemini',
		groq: 'Llama',
		xai: 'Grok',
		mistral: 'Mistral',
		openai: 'GPT'
	};

	// SVG path data for provider logos (simple geometric forms).
	const providerIconPaths: Record<string, string> = {
		vertex:
			'M12 2L6.5 11.5H17.5L12 2ZM12 22L6.5 12.5H17.5L12 22ZM2 12L11.5 6.5V17.5L2 12ZM22 12L12.5 6.5V17.5L22 12Z',
		gemini:
			'M12 2L6.5 11.5H17.5L12 2ZM12 22L6.5 12.5H17.5L12 22ZM2 12L11.5 6.5V17.5L2 12ZM22 12L12.5 6.5V17.5L22 12Z',
		groq: 'M4 4h16v4H4zM4 10h10v4H4zM4 16h16v4H4z',
		xai: 'M4 4l16 16M20 4L4 20',
		mistral: 'M4 12h16M12 4l8 8-8 8',
		openai: 'M12 2a10 10 0 1 0 0 20A10 10 0 0 0 12 2z'
	};

	const providerColor: Record<string, string> = {
		vertex: '#4285f4',
		gemini: '#4285f4',
		groq: '#f55036',
		xai: '#e5e7eb',
		mistral: '#ff7000',
		openai: '#10a37f'
	};

	const effortIcon: Record<AIEffortLevel, string> = {
		fast: '⚡',
		extended: '◆',
		max: '✦'
	};
	const effortColor: Record<AIEffortLevel, string> = {
		fast: '#34a853',
		extended: '#fbbc04',
		max: '#ea4335'
	};

	function activeProviderKey(): string {
		return selectedModel?.provider ?? 'vertex';
	}

	function toggleOpen() {
		open = !open;
	}

	function selectModel(id: string) {
		aiSettings.setModel(id);
		dispatch('change', { modelId: id, effort: settings.effort });
	}

	function selectEffort(effort: AIEffortLevel) {
		aiSettings.setEffort(effort);
		dispatch('change', { modelId: settings.modelId, effort });
	}

	function handleOutsideClick(e: MouseEvent) {
		if (wrapEl && !wrapEl.contains(e.target as Node)) {
			open = false;
		}
	}

	onMount(() => {
		fetchAvailableModels();
		document.addEventListener('click', handleOutsideClick, true);
		return () => document.removeEventListener('click', handleOutsideClick, true);
	});
</script>

<div class="ams-wrap" bind:this={wrapEl}>
	<!-- Trigger pill -->
	<button
		type="button"
		class="ams-trigger"
		class:ams-trigger--open={open}
		on:click={toggleOpen}
		title="AI model & effort"
		aria-haspopup="true"
		aria-expanded={open}
	>
		<!-- Provider icon -->
		<span class="ams-provider-icon" style="--c:{providerColor[activeProviderKey()]}">
			<svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
				<path d={providerIconPaths[activeProviderKey()] ?? providerIconPaths.vertex} stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
			</svg>
		</span>
		<!-- Effort badge -->
		<span class="ams-effort-badge" style="--c:{effortColor[currentEffort.id]}">
			{effortIcon[currentEffort.id]}
		</span>
		{#if !compact}
			<span class="ams-label">
				{selectedModel ? selectedModel.label.split(' ').slice(0, 2).join(' ') : 'Auto'}
				·
				{currentEffort.label}
			</span>
		{/if}
		<svg class="ams-chevron" viewBox="0 0 10 6" aria-hidden="true">
			<path d="M1 1l4 4 4-4" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round" fill="none" />
		</svg>
	</button>

	<!-- Panel -->
	{#if open}
		<div class="ams-panel" role="dialog" aria-label="Select AI model and effort">
			<!-- Effort section -->
			<section class="ams-section">
				<p class="ams-section-label">Speed</p>
				<div class="ams-effort-row">
					{#each effortLevels as level}
						<button
							type="button"
							class="ams-effort-btn"
							class:ams-effort-btn--active={settings.effort === level.id}
							on:click={() => selectEffort(level.id)}
							title={level.description}
							style="--c:{effortColor[level.id]}"
						>
							<span class="ams-effort-icon">{effortIcon[level.id]}</span>
							<span class="ams-effort-name">{level.label}</span>
						</button>
					{/each}
				</div>
			</section>

			<div class="ams-divider"></div>

			<!-- Model section -->
			<section class="ams-section">
				<p class="ams-section-label">Model</p>

				<!-- Auto option -->
				<button
					type="button"
					class="ams-model-row"
					class:ams-model-row--active={settings.modelId === 'auto'}
					on:click={() => selectModel('auto')}
				>
					<span class="ams-model-dot" style="background: #9aa0a6"></span>
					<span class="ams-model-name">Auto</span>
					<span class="ams-model-tier">default</span>
				</button>

				{#if providerGroups.length > 0}
					{#each providerGroups as group}
						<p class="ams-provider-label">{providerLabel[group.provider] ?? group.provider}</p>
						{#each group.models as model}
							<button
								type="button"
								class="ams-model-row"
								class:ams-model-row--active={settings.modelId === model.id}
								on:click={() => selectModel(model.id)}
							>
								<span
									class="ams-model-dot"
									style="background: {providerColor[model.provider] ?? '#9aa0a6'}"
								></span>
								<span class="ams-model-name">{model.label}</span>
								<span class="ams-model-tier">{model.tier}</span>
							</button>
						{/each}
					{/each}
				{:else}
					<p class="ams-empty">Effort controls tier routing.</p>
				{/if}
			</section>
		</div>
	{/if}
</div>

<style>
	.ams-wrap {
		position: relative;
		display: inline-flex;
	}

	/* ── Trigger ── */
	.ams-trigger {
		display: inline-flex;
		align-items: center;
		gap: 0.28rem;
		padding: 0.24rem 0.44rem;
		border-radius: 8px;
		border: 1px solid rgba(255, 255, 255, 0.1);
		background: rgba(255, 255, 255, 0.05);
		color: #9aa0a6;
		cursor: pointer;
		font-size: 0.72rem;
		transition:
			border-color 0.15s,
			background 0.15s,
			color 0.15s;
		white-space: nowrap;
	}
	.ams-trigger:hover,
	.ams-trigger--open {
		border-color: rgba(255, 255, 255, 0.2);
		background: rgba(255, 255, 255, 0.1);
		color: #e8eaed;
	}

	.ams-provider-icon {
		width: 14px;
		height: 14px;
		color: var(--c, #4285f4);
		flex-shrink: 0;
		display: flex;
		align-items: center;
	}
	.ams-provider-icon svg {
		width: 100%;
		height: 100%;
	}

	.ams-effort-badge {
		font-size: 0.72rem;
		color: var(--c, #fbbc04);
		line-height: 1;
	}

	.ams-label {
		font-size: 0.7rem;
		font-weight: 500;
		color: #bdc1c6;
		max-width: 120px;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.ams-chevron {
		width: 8px;
		height: 8px;
		color: #5f6368;
		flex-shrink: 0;
		transition: transform 0.15s;
	}
	.ams-trigger--open .ams-chevron {
		transform: rotate(180deg);
	}

	/* ── Panel ── */
	.ams-panel {
		position: absolute;
		bottom: calc(100% + 8px);
		left: 0;
		z-index: 1200;
		width: 230px;
		background: #1c1e23;
		border: 1px solid rgba(255, 255, 255, 0.1);
		border-radius: 12px;
		box-shadow:
			0 8px 32px rgba(0, 0, 0, 0.6),
			0 2px 8px rgba(0, 0, 0, 0.4);
		padding: 0.5rem 0;
		animation: ams-in 0.14s cubic-bezier(0.22, 1, 0.36, 1);
		overflow: hidden;
	}

	@keyframes ams-in {
		from {
			opacity: 0;
			transform: translateY(4px) scale(0.97);
		}
		to {
			opacity: 1;
			transform: translateY(0) scale(1);
		}
	}

	.ams-section {
		padding: 0.3rem 0;
	}

	.ams-section-label {
		margin: 0 0 0.3rem;
		padding: 0 0.72rem;
		font-size: 0.62rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: #5f6368;
	}

	.ams-divider {
		height: 1px;
		background: rgba(255, 255, 255, 0.07);
		margin: 0.3rem 0;
	}

	/* ── Effort row ── */
	.ams-effort-row {
		display: flex;
		gap: 0.3rem;
		padding: 0 0.72rem;
	}

	.ams-effort-btn {
		flex: 1;
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 0.22rem;
		padding: 0.42rem 0.3rem;
		border-radius: 8px;
		border: 1px solid rgba(255, 255, 255, 0.08);
		background: rgba(255, 255, 255, 0.03);
		color: #9aa0a6;
		cursor: pointer;
		transition:
			border-color 0.14s,
			background 0.14s,
			color 0.14s;
	}
	.ams-effort-btn:hover {
		border-color: rgba(255, 255, 255, 0.18);
		background: rgba(255, 255, 255, 0.08);
		color: #e8eaed;
	}
	.ams-effort-btn--active {
		border-color: var(--c, #fbbc04);
		background: color-mix(in srgb, var(--c, #fbbc04) 10%, transparent);
		color: var(--c, #fbbc04);
	}

	.ams-effort-icon {
		font-size: 0.9rem;
		line-height: 1;
	}
	.ams-effort-name {
		font-size: 0.64rem;
		font-weight: 600;
		letter-spacing: 0.02em;
	}

	/* ── Model rows ── */
	.ams-provider-label {
		margin: 0.5rem 0 0.15rem;
		padding: 0 0.72rem;
		font-size: 0.6rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: #5f6368;
	}

	.ams-model-row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		width: 100%;
		padding: 0.38rem 0.72rem;
		border: none;
		background: transparent;
		color: #9aa0a6;
		font-size: 0.74rem;
		text-align: left;
		cursor: pointer;
		transition: background 0.12s, color 0.12s;
	}
	.ams-model-row:hover {
		background: rgba(255, 255, 255, 0.06);
		color: #e8eaed;
	}
	.ams-model-row--active {
		background: rgba(255, 255, 255, 0.08);
		color: #e8eaed;
	}

	.ams-model-dot {
		width: 6px;
		height: 6px;
		border-radius: 999px;
		flex-shrink: 0;
	}

	.ams-model-name {
		flex: 1;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		font-size: 0.74rem;
	}

	.ams-model-tier {
		font-size: 0.6rem;
		color: #5f6368;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		flex-shrink: 0;
	}

	.ams-model-row--active .ams-model-tier {
		color: #9aa0a6;
	}

	.ams-empty {
		margin: 0;
		padding: 0.4rem 0.72rem;
		font-size: 0.7rem;
		color: #5f6368;
		font-style: italic;
	}
</style>
