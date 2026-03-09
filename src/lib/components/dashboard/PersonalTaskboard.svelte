<script lang="ts">
	import { onMount } from 'svelte';
	import {
		addItem,
		deleteItem,
		fetchItems,
		personalItems,
		type PersonalItem,
		updateStatus
	} from '$lib/stores/personal';

	let quickCaptureContent = '';
	let isLoading = false;
	let isCreating = false;
	let activeItemID = '';
	let errorMessage = '';

	onMount(() => {
		void loadItems();
	});

	function normalizedStatus(value: string) {
		return value.trim().toLowerCase();
	}

	function isCompleted(item: PersonalItem) {
		return normalizedStatus(item.status) === 'completed';
	}

	function nextStatus(item: PersonalItem) {
		return isCompleted(item) ? 'pending' : 'completed';
	}

	async function loadItems() {
		isLoading = true;
		errorMessage = '';
		try {
			await fetchItems();
		} catch (error) {
			errorMessage = error instanceof Error ? error.message : 'Failed to load personal items';
		} finally {
			isLoading = false;
		}
	}

	async function handleQuickCapture() {
		const content = quickCaptureContent.trim();
		if (isCreating || content === '') {
			return;
		}

		isCreating = true;
		errorMessage = '';
		try {
			await addItem('task', content);
			quickCaptureContent = '';
		} catch (error) {
			errorMessage = error instanceof Error ? error.message : 'Failed to create personal item';
		} finally {
			isCreating = false;
		}
	}

	async function handleToggleStatus(item: PersonalItem) {
		if (activeItemID) {
			return;
		}
		activeItemID = item.item_id;
		errorMessage = '';
		try {
			await updateStatus(item.item_id, nextStatus(item));
		} catch (error) {
			errorMessage = error instanceof Error ? error.message : 'Failed to update personal item status';
		} finally {
			activeItemID = '';
		}
	}

	async function handleDelete(item: PersonalItem) {
		if (activeItemID) {
			return;
		}
		activeItemID = item.item_id;
		errorMessage = '';
		try {
			await deleteItem(item.item_id);
		} catch (error) {
			errorMessage = error instanceof Error ? error.message : 'Failed to delete personal item';
		} finally {
			activeItemID = '';
		}
	}
</script>

<section class="personal-taskboard" aria-label="Personal taskboard">
	<header class="board-header">
		<div>
			<h2>Personal Taskboard</h2>
			<p>Quick capture your tasks and close the loop fast.</p>
		</div>
		<button type="button" class="refresh-btn" on:click={loadItems} disabled={isLoading || isCreating}>
			Refresh
		</button>
	</header>

	<form
		class="quick-capture"
		on:submit|preventDefault={() => {
			void handleQuickCapture();
		}}
	>
		<input
			type="text"
			bind:value={quickCaptureContent}
			placeholder="Quick capture a task..."
			autocomplete="off"
			disabled={isCreating}
		/>
		<button type="submit" disabled={isCreating || quickCaptureContent.trim() === ''}>
			{isCreating ? 'Saving...' : 'Add'}
		</button>
	</form>

	{#if errorMessage}
		<div class="error-banner">{errorMessage}</div>
	{/if}

	{#if isLoading}
		<div class="state-text">Loading personal items...</div>
	{:else if $personalItems.length === 0}
		<div class="state-text">No items yet. Capture your first task above.</div>
	{:else}
		<div class="items-grid">
			{#each $personalItems as item (item.item_id)}
				<article class="item-card {isCompleted(item) ? 'completed' : ''}">
					<label class="status-toggle">
						<input
							type="checkbox"
							checked={isCompleted(item)}
							on:change={() => {
								void handleToggleStatus(item);
							}}
							disabled={activeItemID === item.item_id}
						/>
						<span>{item.content}</span>
					</label>

					<div class="item-meta">
						<small>{item.type || 'task'}</small>
						<button
							type="button"
							class="delete-btn"
							on:click={() => {
								void handleDelete(item);
							}}
							disabled={activeItemID === item.item_id}
							aria-label="Delete personal item"
						>
							<svg viewBox="0 0 24 24" aria-hidden="true">
								<path
									d="M9 3h6l1 2h4v2H4V5h4l1-2Zm1 6h2v9h-2V9Zm4 0h2v9h-2V9ZM7 9h2v9H7V9Z"
									fill="currentColor"
								/>
							</svg>
						</button>
					</div>
				</article>
			{/each}
		</div>
	{/if}
</section>

<style>
	:global(:root) {
		--personal-board-bg:
			radial-gradient(circle at top right, rgba(166, 203, 250, 0.24), transparent 45%),
			rgba(255, 255, 255, 0.64);
		--personal-board-border: rgba(171, 196, 235, 0.56);
		--personal-board-shadow: 0 20px 48px rgba(101, 128, 174, 0.22);
		--personal-board-text: #13213f;
		--personal-board-subtle: rgba(60, 79, 113, 0.74);
		--personal-btn-border: rgba(100, 132, 188, 0.34);
		--personal-btn-bg: rgba(255, 255, 255, 0.74);
		--personal-btn-text: #122544;
		--personal-input-border: rgba(137, 167, 217, 0.5);
		--personal-input-bg: rgba(255, 255, 255, 0.7);
		--personal-input-text: #13213f;
		--personal-input-placeholder: rgba(77, 100, 139, 0.58);
		--personal-input-focus: rgba(90, 128, 196, 0.78);
		--personal-error-bg: rgba(220, 38, 38, 0.13);
		--personal-error-border: rgba(220, 38, 38, 0.35);
		--personal-error-text: #8f2235;
		--personal-state-text: rgba(56, 75, 109, 0.78);
		--personal-item-bg: rgba(255, 255, 255, 0.62);
		--personal-item-border: rgba(170, 193, 230, 0.54);
		--personal-meta-text: rgba(71, 92, 128, 0.72);
		--personal-btn-hover-bg: rgba(234, 243, 255, 0.84);
		--personal-btn-hover-border: rgba(110, 143, 203, 0.52);
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--personal-board-bg:
			radial-gradient(circle at top right, rgba(255, 255, 255, 0.05), transparent 45%),
			#0d0d12;
		--personal-board-border: rgba(255, 255, 255, 0.08);
		--personal-board-shadow: 0 20px 48px rgba(0, 0, 0, 0.36);
		--personal-board-text: #f3f4f6;
		--personal-board-subtle: rgba(229, 231, 235, 0.72);
		--personal-btn-border: rgba(255, 255, 255, 0.16);
		--personal-btn-bg: rgba(255, 255, 255, 0.08);
		--personal-btn-text: #f9fafb;
		--personal-input-border: rgba(255, 255, 255, 0.12);
		--personal-input-bg: rgba(255, 255, 255, 0.03);
		--personal-input-text: #f9fafb;
		--personal-input-placeholder: rgba(229, 231, 235, 0.56);
		--personal-input-focus: rgba(148, 163, 184, 0.75);
		--personal-error-bg: rgba(220, 38, 38, 0.2);
		--personal-error-border: rgba(248, 113, 113, 0.38);
		--personal-error-text: #fecaca;
		--personal-state-text: rgba(229, 231, 235, 0.78);
		--personal-item-bg: rgba(255, 255, 255, 0.03);
		--personal-item-border: rgba(255, 255, 255, 0.1);
		--personal-meta-text: rgba(229, 231, 235, 0.62);
		--personal-btn-hover-bg: rgba(255, 255, 255, 0.14);
		--personal-btn-hover-border: rgba(255, 255, 255, 0.25);
	}

	.personal-taskboard {
		background: var(--personal-board-bg);
		border: 1px solid var(--personal-board-border);
		border-radius: 18px;
		padding: 1rem;
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
		box-shadow: var(--personal-board-shadow);
		color: var(--personal-board-text);
		display: grid;
		gap: 0.9rem;
	}

	.board-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 0.75rem;
	}

	.board-header h2 {
		margin: 0;
		font-size: 1rem;
		letter-spacing: 0.02em;
	}

	.board-header p {
		margin: 0.2rem 0 0;
		color: var(--personal-board-subtle);
		font-size: 0.82rem;
	}

	.refresh-btn,
	.quick-capture button,
	.delete-btn {
		border: 1px solid var(--personal-btn-border);
		background: var(--personal-btn-bg);
		color: var(--personal-btn-text);
		border-radius: 10px;
		cursor: pointer;
		transition: background 0.2s ease, border-color 0.2s ease, transform 0.15s ease;
	}

	.refresh-btn {
		padding: 0.42rem 0.7rem;
		font-size: 0.78rem;
	}

	.quick-capture {
		display: flex;
		gap: 0.55rem;
	}

	.quick-capture input {
		flex: 1;
		border: 1px solid var(--personal-input-border);
		background: var(--personal-input-bg);
		color: var(--personal-input-text);
		border-radius: 12px;
		padding: 0.62rem 0.78rem;
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
	}

	.quick-capture input::placeholder {
		color: var(--personal-input-placeholder);
	}

	.quick-capture input:focus {
		outline: none;
		border-color: var(--personal-input-focus);
	}

	.quick-capture button {
		padding: 0.62rem 0.86rem;
		min-width: 70px;
	}

	.error-banner {
		color: var(--personal-error-text);
		background: var(--personal-error-bg);
		border: 1px solid var(--personal-error-border);
		padding: 0.52rem 0.66rem;
		border-radius: 10px;
		font-size: 0.82rem;
	}

	.state-text {
		color: var(--personal-state-text);
		font-size: 0.84rem;
	}

	.items-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
		gap: 0.62rem;
	}

	.item-card {
		background: var(--personal-item-bg);
		border: 1px solid var(--personal-item-border);
		border-radius: 14px;
		padding: 0.72rem;
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
		display: grid;
		gap: 0.62rem;
	}

	.status-toggle {
		display: flex;
		align-items: flex-start;
		gap: 0.6rem;
	}

	.status-toggle input {
		margin-top: 0.16rem;
		accent-color: #93c5fd;
	}

	.status-toggle span {
		font-size: 0.9rem;
		line-height: 1.35;
		word-break: break-word;
	}

	.item-card.completed .status-toggle span {
		opacity: 0.62;
		text-decoration: line-through;
	}

	.item-meta {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	.item-meta small {
		color: var(--personal-meta-text);
		font-size: 0.73rem;
		text-transform: uppercase;
		letter-spacing: 0.08em;
	}

	.delete-btn {
		width: 30px;
		height: 30px;
		padding: 0;
		display: inline-flex;
		align-items: center;
		justify-content: center;
	}

	.delete-btn svg {
		width: 16px;
		height: 16px;
	}

	.refresh-btn:hover:not(:disabled),
	.quick-capture button:hover:not(:disabled),
	.delete-btn:hover:not(:disabled) {
		background: var(--personal-btn-hover-bg);
		border-color: var(--personal-btn-hover-border);
	}

	.refresh-btn:disabled,
	.quick-capture button:disabled,
	.delete-btn:disabled {
		cursor: not-allowed;
		opacity: 0.55;
	}

	@media (max-width: 680px) {
		.quick-capture {
			flex-direction: column;
		}

		.quick-capture button {
			width: 100%;
		}
	}
</style>
