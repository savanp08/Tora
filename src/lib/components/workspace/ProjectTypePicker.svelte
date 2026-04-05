<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import { PROJECT_TYPE_CONFIGS, type ProjectType } from '$lib/stores/projectType';

	export let value: ProjectType = 'software';
	export let variant: 'default' | 'template' = 'default';

	const dispatch = createEventDispatcher<{ select: { projectType: ProjectType } }>();
	const projectTypes = Object.values(PROJECT_TYPE_CONFIGS);

	function selectProjectType(projectType: ProjectType) {
		value = projectType;
		dispatch('select', { projectType });
	}
</script>

<div
	class="project-type-grid"
	class:is-template={variant === 'template'}
	aria-label="Project type options"
>
	{#each projectTypes as projectType}
		<button
			type="button"
			class:selected={projectType.type === value}
			class="project-type-card"
			class:is-template={variant === 'template'}
			on:click={() => selectProjectType(projectType.type)}
		>
			{#if variant === 'template'}
				<div class="project-type-card-top">
					<span class="project-type-pill">{projectType.groupTermPlural}</span>
					<span class="project-type-meta">{projectType.taskTermPlural}</span>
				</div>
				<div class="project-type-card-main">
					<div class="project-type-icon" aria-hidden="true">{projectType.icon}</div>
					<div class="project-type-copy">
						<strong>{projectType.displayName}</strong>
						<p>{projectType.description}</p>
					</div>
				</div>
			{:else}
				<div class="project-type-icon" aria-hidden="true">{projectType.icon}</div>
				<div class="project-type-copy">
					<strong>{projectType.displayName}</strong>
					<p>{projectType.description}</p>
				</div>
			{/if}
		</button>
	{/each}
</div>

<style>
	.project-type-grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
		gap: 0.85rem;
	}

	.project-type-grid.is-template {
		grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
	}

	.project-type-card {
		display: flex;
		align-items: flex-start;
		gap: 0.9rem;
		padding: 0.95rem 1rem;
		border: 1px solid rgba(148, 163, 184, 0.35);
		border-radius: 1rem;
		background: rgba(255, 255, 255, 0.86);
		color: inherit;
		text-align: left;
		transition:
			border-color 0.18s ease,
			transform 0.18s ease,
			box-shadow 0.18s ease;
	}

	.project-type-card.is-template {
		display: grid;
		gap: 0.62rem;
		padding: 0.95rem;
		border-radius: 16px;
		background: var(--po-surface, rgba(255, 255, 255, 0.86));
		border-color: var(--po-border, rgba(148, 163, 184, 0.35));
		color: var(--po-text, inherit);
	}

	.project-type-card:hover {
		transform: translateY(-1px);
		border-color: rgba(37, 99, 235, 0.35);
		box-shadow: 0 12px 24px rgba(15, 23, 42, 0.08);
	}

	.project-type-card.is-template:hover {
		border-color: var(--po-border-strong, rgba(37, 99, 235, 0.35));
		background: color-mix(
			in srgb,
			var(--po-accent-soft, rgba(37, 99, 235, 0.1)) 45%,
			var(--po-surface, rgba(255, 255, 255, 0.86))
		);
		box-shadow: none;
	}

	.project-type-card.selected {
		border-color: #2563eb;
		box-shadow: 0 0 0 1px rgba(37, 99, 235, 0.22);
		background: rgba(239, 246, 255, 0.96);
	}

	.project-type-card.is-template.selected {
		border-color: color-mix(
			in srgb,
			var(--po-accent, #2563eb) 72%,
			var(--po-border, rgba(148, 163, 184, 0.35))
		);
		background: color-mix(
			in srgb,
			var(--po-accent-soft, rgba(37, 99, 235, 0.1)) 78%,
			var(--po-surface, rgba(255, 255, 255, 0.86))
		);
		box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--po-accent, #2563eb) 20%, transparent);
	}

	.project-type-card-top {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		flex-wrap: wrap;
	}

	.project-type-card-main {
		display: flex;
		align-items: flex-start;
		gap: 0.9rem;
	}

	.project-type-icon {
		font-size: 1.5rem;
		line-height: 1;
	}

	.project-type-pill {
		display: inline-flex;
		align-items: center;
		border-radius: 999px;
		padding: 0.18rem 0.52rem;
		font-size: 0.64rem;
		font-weight: 700;
		letter-spacing: 0.03em;
		text-transform: uppercase;
		border: 1px solid
			color-mix(in srgb, var(--po-accent, #2563eb) 34%, var(--po-border, rgba(148, 163, 184, 0.35)));
		background: color-mix(
			in srgb,
			var(--po-accent-soft, rgba(37, 99, 235, 0.1)) 66%,
			var(--po-surface, rgba(255, 255, 255, 0.86))
		);
		color: inherit;
	}

	.project-type-meta {
		font-size: 0.72rem;
		color: var(--po-muted, rgba(71, 85, 105, 0.96));
	}

	.project-type-copy {
		display: grid;
		gap: 0.25rem;
	}

	.project-type-copy strong {
		font-size: 0.96rem;
	}

	.project-type-copy p {
		margin: 0;
		font-size: 0.84rem;
		line-height: 1.4;
		color: rgba(71, 85, 105, 0.96);
	}

	:global(:root[data-theme='dark']) .project-type-card,
	:global(.theme-dark) .project-type-card {
		background: rgba(24, 28, 39, 0.94);
		border-color: rgba(148, 163, 184, 0.2);
	}

	:global(:root[data-theme='dark']) .project-type-card.is-template,
	:global(.theme-dark) .project-type-card.is-template {
		background: var(--po-surface, rgba(24, 28, 39, 0.94));
		border-color: var(--po-border, rgba(148, 163, 184, 0.2));
	}

	:global(:root[data-theme='dark']) .project-type-card.selected,
	:global(.theme-dark) .project-type-card.selected {
		background: rgba(29, 78, 216, 0.16);
		border-color: rgba(96, 165, 250, 0.72);
	}

	:global(:root[data-theme='dark']) .project-type-card.is-template.selected,
	:global(.theme-dark) .project-type-card.is-template.selected {
		background: color-mix(
			in srgb,
			var(--po-accent-soft, rgba(180, 190, 207, 0.18)) 78%,
			var(--po-surface, rgba(24, 28, 39, 0.94))
		);
		border-color: color-mix(
			in srgb,
			var(--po-accent, rgba(96, 165, 250, 0.72)) 72%,
			var(--po-border, rgba(148, 163, 184, 0.2))
		);
	}

	:global(:root[data-theme='dark']) .project-type-copy p,
	:global(.theme-dark) .project-type-copy p {
		color: rgba(191, 219, 254, 0.82);
	}

	:global(:root[data-theme='dark']) .project-type-meta,
	:global(.theme-dark) .project-type-meta {
		color: rgba(191, 219, 254, 0.82);
	}
</style>
