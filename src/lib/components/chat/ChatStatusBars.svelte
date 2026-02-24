<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import type { MessageActionMode } from '$lib/types/chat';

	export let typingIndicatorText = '';
	export let showTrustedDevicePrompt = false;
	export let isSelectionMode = false;
	export let messageActionMode: MessageActionMode = 'none';
	export let showRoomSearch = false;
	export let roomMessageSearch = '';
	export let isDarkMode = false;
	export let selectedDeleteCount = 0;

	const dispatch = createEventDispatcher<{
		trustedChoice: { choice: 'yes' | 'no' };
		cancelSelection: void;
		deleteSelected: void;
	}>();
</script>

{#if typingIndicatorText}
	<div class="typing-indicator {isDarkMode ? 'theme-dark' : ''}">{typingIndicatorText}</div>
{/if}

{#if showTrustedDevicePrompt}
	<div class="trusted-banner {isDarkMode ? 'theme-dark' : ''}" role="status" aria-live="polite">
		<span>Trusted device? Enable encrypted history caching for faster loading.</span>
		<div class="trusted-actions">
			<button type="button" on:click={() => dispatch('trustedChoice', { choice: 'yes' })}
				>Yes</button
			>
			<button type="button" on:click={() => dispatch('trustedChoice', { choice: 'no' })}>No</button>
		</div>
	</div>
{/if}

{#if isSelectionMode}
	<div class="selection-banner {isDarkMode ? 'theme-dark' : ''}">
		<span class="selection-copy">
			{#if messageActionMode === 'break'}
				Break mode active: click a message to start a new topic room.
			{:else if messageActionMode === 'pin'}
				Pin mode active: click any message to pin it and open a discussion.
			{:else if messageActionMode === 'edit'}
				Edit mode active: click one of your messages, then use the action buttons.
			{:else if messageActionMode === 'delete'}
				Delete mode active: choose message(s) to delete.
			{/if}
		</span>
		<div class="selection-controls">
			{#if messageActionMode === 'delete'}
				<span class="selected-count">{selectedDeleteCount} selected</span>
				<button
					type="button"
					class="selection-cta danger"
					disabled={selectedDeleteCount <= 0}
					on:click={() => dispatch('deleteSelected')}
				>
					Delete selected
				</button>
			{/if}
			<button
				type="button"
				class="selection-cta cancel"
				on:click={() => dispatch('cancelSelection')}
			>
				Cancel
			</button>
		</div>
	</div>
{/if}

{#if showRoomSearch}
	<div class="chat-search-row {isDarkMode ? 'theme-dark' : ''}">
		<input type="text" bind:value={roomMessageSearch} placeholder="Search in this room" />
	</div>
{/if}

<style>
	.selection-banner {
		padding: 0.45rem 0.9rem;
		background: #e8edf4;
		border-bottom: 1px solid #d4dce7;
		font-size: 0.8rem;
		color: #3e4d63;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
		flex-wrap: wrap;
	}

	.selection-banner.theme-dark {
		background: #111113;
		border-bottom-color: #2c2c31;
		color: #d1d1d8;
	}

	.selection-copy {
		flex: 1 1 14rem;
		min-width: 0;
	}

	.selection-controls {
		display: inline-flex;
		align-items: center;
		gap: 0.45rem;
		flex-wrap: wrap;
	}

	.selected-count {
		font-size: 0.72rem;
		opacity: 0.8;
	}

	.selection-cta {
		border: 1px solid #c6cfdb;
		background: #f7f9fc;
		color: #2f3d54;
		border-radius: 999px;
		font-size: 0.72rem;
		font-weight: 600;
		padding: 0.18rem 0.56rem;
		cursor: pointer;
	}

	.selection-cta:hover {
		background: #e8edf4;
	}

	.selection-cta.danger {
		border-color: rgba(220, 38, 38, 0.5);
		color: #b91c1c;
	}

	.selection-cta:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.selection-cta.cancel {
		font-size: 1.08rem;
		padding: 0.27rem 0.84rem;
	}

	.selection-banner.theme-dark .selection-cta {
		border-color: #3a3a40;
		background: #1a1a1e;
		color: #efeff4;
	}

	.selection-banner.theme-dark .selection-cta.danger {
		border-color: rgba(248, 113, 113, 0.45);
		color: #fca5a5;
	}

	.typing-indicator {
		padding: 0.35rem 0.9rem;
		border-bottom: 1px solid #d9e0ea;
		background: #f2f5f9;
		color: #67758a;
		font-size: 0.75rem;
		line-height: 1.2;
	}

	.typing-indicator.theme-dark {
		background: #0f0f11;
		border-bottom-color: #2a2a2f;
		color: #adadb6;
	}

	.trusted-banner {
		padding: 0.5rem 0.9rem;
		border-bottom: 1px solid #d6dde8;
		background: #ecf1f7;
		color: #3b4a60;
		font-size: 0.76rem;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
		flex-wrap: wrap;
	}

	.trusted-banner.theme-dark {
		background: #101012;
		border-bottom-color: #2b2b30;
		color: #d6d6dd;
	}

	.trusted-banner > span {
		flex: 1 1 14rem;
		min-width: 0;
	}

	.trusted-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		margin-left: auto;
		flex-shrink: 0;
	}

	.trusted-actions button {
		border: 1px solid #c6cfdc;
		background: #f7f9fc;
		color: #2f3d54;
		border-radius: 999px;
		font-size: 0.72rem;
		padding: 0.18rem 0.54rem;
		cursor: pointer;
	}

	.trusted-actions button:hover {
		background: #e8edf4;
	}

	.trusted-banner.theme-dark .trusted-actions button {
		border-color: #3a3a40;
		background: #1a1a1e;
		color: #efeff4;
	}

	.chat-search-row {
		padding: 0.65rem 0.9rem;
		background: #f2f5f9;
		border-bottom: 1px solid #d7dee8;
	}

	.chat-search-row.theme-dark {
		background: #101012;
		border-bottom-color: #2b2b30;
	}

	.chat-search-row input {
		width: 100%;
		border: 1px solid #c7cfdb;
		border-radius: 8px;
		padding: 0.55rem 0.7rem;
		font-size: 0.9rem;
		background: #edf2f8;
		color: #2b394f;
	}

	.chat-search-row input::placeholder {
		color: #6a7890;
	}

	.chat-search-row.theme-dark input {
		border-color: #38383d;
		background: #18181b;
		color: #efeff4;
	}

	.chat-search-row.theme-dark input::placeholder {
		color: #9898a1;
	}
</style>
