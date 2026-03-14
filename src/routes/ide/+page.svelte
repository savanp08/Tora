<script lang="ts">
	import { browser } from '$app/environment';
	import { onMount } from 'svelte';
	import CodeCanvas from '$lib/components/canvas/CodeCanvas.svelte';
	import FreeDrawBoard from '$lib/components/ide/FreeDrawBoard.svelte';

	type WorkspaceMode = 'ide' | 'draw';

	const ideSessionId = `ide-local-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
	const ideUser = {
		id: 'ide-guest',
		name: 'IDE Guest',
		color: '#3b82f6'
	};

	let workspaceMode: WorkspaceMode = 'ide';

	onMount(() => {
		if (!browser) {
			return;
		}
		document.body.classList.add('ide-lab-mode');
		return () => {
			document.body.classList.remove('ide-lab-mode');
		};
	});
</script>

<section class="ide-lab">
	<div class="ide-main">
		<header class="ide-toolbar">
			<div class="mode-toggle">
				<button
					type="button"
					class="mode-btn"
					class:is-active={workspaceMode === 'ide'}
					on:click={() => (workspaceMode = 'ide')}
				>
					IDE
				</button>
				<button
					type="button"
					class="mode-btn"
					class:is-active={workspaceMode === 'draw'}
					on:click={() => (workspaceMode = 'draw')}
				>
					Free Draw
				</button>
			</div>
			<p class="mode-note">
				Local-only session. No backend save or sync.
			</p>
		</header>

		<div class="ide-stage">
			{#if workspaceMode === 'ide'}
				<CodeCanvas
					roomId={ideSessionId}
					currentUser={ideUser}
					isEphemeralRoom={true}
					remoteSyncEnabled={false}
					initialTerminalHeight={320}
				/>
			{:else}
				<FreeDrawBoard />
			{/if}
		</div>
	</div>

	<aside class="ide-ad-rail" aria-label="Sponsored panel placeholder">
		<div class="ad-card">
			<h2>Ad Slot</h2>
			<p>Reserved area for sponsor modules or promo cards.</p>
		</div>
		<div class="ad-card muted">
			<h3>Secondary Slot</h3>
			<p>Keep this rail for contextual ads or announcements.</p>
		</div>
	</aside>
</section>

<style>
	.ide-lab {
		height: 100vh;
		display: grid;
		grid-template-columns: minmax(0, 1fr) 300px;
		overflow: hidden;
		background:
			radial-gradient(circle at top left, rgba(34, 197, 94, 0.15), transparent 45%),
			radial-gradient(circle at 82% 14%, rgba(59, 130, 246, 0.18), transparent 42%),
			#0b1220;
	}

	.ide-main {
		min-width: 0;
		min-height: 0;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr);
		gap: 0.65rem;
		padding: 0.7rem;
	}

	.ide-toolbar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.8rem;
		padding: 0.62rem 0.72rem;
		border: 1px solid rgba(148, 163, 184, 0.3);
		background: rgba(15, 23, 42, 0.8);
		border-radius: 0.85rem;
		color: #e2e8f0;
	}

	.mode-toggle {
		display: inline-flex;
		gap: 0.4rem;
	}

	.mode-btn {
		border: 1px solid rgba(148, 163, 184, 0.42);
		background: rgba(30, 41, 59, 0.86);
		color: #e2e8f0;
		padding: 0.42rem 0.66rem;
		border-radius: 0.52rem;
		font-size: 0.78rem;
		font-weight: 600;
		cursor: pointer;
	}

	.mode-btn:hover {
		border-color: rgba(191, 219, 254, 0.7);
		background: rgba(51, 65, 85, 0.92);
	}

	.mode-btn.is-active {
		border-color: rgba(34, 197, 94, 0.75);
		background: rgba(22, 163, 74, 0.24);
	}

	.mode-note {
		margin: 0;
		font-size: 0.78rem;
		color: #cbd5e1;
	}

	.ide-stage {
		min-width: 0;
		min-height: 0;
		border: 1px solid rgba(148, 163, 184, 0.3);
		border-radius: 0.92rem;
		overflow: hidden;
		background: rgba(2, 6, 23, 0.76);
	}

	.ide-stage :global(.canvas-shell) {
		height: 100%;
	}

	.ide-ad-rail {
		min-height: 0;
		padding: 0.72rem 0.72rem 0.72rem 0;
		display: flex;
		flex-direction: column;
		gap: 0.7rem;
	}

	.ad-card {
		border-radius: 0.86rem;
		border: 1px dashed rgba(148, 163, 184, 0.46);
		background: rgba(15, 23, 42, 0.66);
		padding: 0.9rem;
		color: #e2e8f0;
	}

	.ad-card h2,
	.ad-card h3 {
		margin: 0 0 0.45rem;
		font-size: 0.95rem;
	}

	.ad-card p {
		margin: 0;
		font-size: 0.78rem;
		line-height: 1.45;
		color: #cbd5e1;
	}

	.ad-card.muted {
		margin-top: auto;
	}

	:global(body.ide-lab-mode) {
		overflow: hidden;
	}

	@media (max-width: 1180px) {
		.ide-lab {
			grid-template-columns: minmax(0, 1fr) 240px;
		}
	}

	@media (max-width: 900px) {
		.ide-lab {
			grid-template-columns: minmax(0, 1fr);
			grid-template-rows: minmax(0, 1fr) auto;
			height: 100svh;
		}

		.ide-ad-rail {
			padding: 0 0.7rem 0.7rem;
			flex-direction: row;
			overflow-x: auto;
		}

		.ad-card {
			min-width: 220px;
		}

		.ide-toolbar {
			flex-direction: column;
			align-items: flex-start;
		}
	}
</style>
