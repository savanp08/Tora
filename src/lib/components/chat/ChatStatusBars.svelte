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

	const dispatch = createEventDispatcher<{
		trustedChoice: { choice: 'yes' | 'no' };
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
		{#if messageActionMode === 'break'}
			Break mode active: click a message to start a new topic room.
		{:else if messageActionMode === 'edit'}
			Edit mode active: click one of your messages, then use the edit/delete buttons beside it.
		{:else if messageActionMode === 'delete'}
			Delete mode active: click one of your messages, then use the edit/delete buttons beside it.
		{/if}
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
	}

	.selection-banner.theme-dark {
		background: #101a2e;
		border-bottom-color: #2f3f5b;
		color: #c5d3ec;
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
		background: #0f1a2e;
		border-bottom-color: #2e3d58;
		color: #9fb1d0;
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
	}

	.trusted-banner.theme-dark {
		background: #0f1a2e;
		border-bottom-color: #2e3d58;
		color: #d2def2;
	}

	.trusted-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
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
		border-color: #34445f;
		background: #13203a;
		color: #d9e6ff;
	}

	.chat-search-row {
		padding: 0.65rem 0.9rem;
		background: #f2f5f9;
		border-bottom: 1px solid #d7dee8;
	}

	.chat-search-row.theme-dark {
		background: #0f1a2e;
		border-bottom-color: #2f3f5b;
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
		border-color: #314059;
		background: #111d33;
		color: #dbe7ff;
	}

	.chat-search-row.theme-dark input::placeholder {
		color: #8ea2c3;
	}
</style>
