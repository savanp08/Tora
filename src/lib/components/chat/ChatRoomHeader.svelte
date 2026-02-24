<script lang="ts">
	import { createEventDispatcher, onMount } from 'svelte';
	import type { MessageActionMode } from '$lib/types/chat';

	export let roomName = 'Room';
	export let onlineCount = 0;
	export let unreadCount = 0;
	export let isMember = true;
	export let isActiveRoomAdmin = false;
	export let isMobileView = false;
	export let isDarkMode = false;
	export let messageActionMode: MessageActionMode = 'none';
	export let showRoomSearch = false;
	export let remainingLabel = '--';

	const dispatch = createEventDispatcher<{
		showMobileList: void;
		openRoomDetails: void;
		toggleRoomSearch: void;
		renameRoom: void;
		toggleBreakSelectionMode: void;
		togglePinSelectionMode: void;
		toggleEditSelectionMode: void;
		toggleDeleteSelectionMode: void;
		markRead: void;
		clearLocal: void;
		leaveRoom: void;
		deleteRoom: void;
		disconnect: void;
	}>();

	let showRoomMenu = false;
	let headerActionsEl: HTMLDivElement | null = null;

	onMount(() => {
		const onDocumentPointerDown = (event: PointerEvent) => {
			const target = event.target;
			if (!(target instanceof Node)) {
				return;
			}
			if (showRoomMenu && headerActionsEl && !headerActionsEl.contains(target)) {
				showRoomMenu = false;
			}
		};
		window.addEventListener('pointerdown', onDocumentPointerDown);
		return () => {
			window.removeEventListener('pointerdown', onDocumentPointerDown);
		};
	});

	function closeMenuThen(
		eventName:
			| 'toggleRoomSearch'
			| 'renameRoom'
			| 'toggleBreakSelectionMode'
			| 'togglePinSelectionMode'
			| 'toggleEditSelectionMode'
			| 'toggleDeleteSelectionMode'
			| 'markRead'
			| 'clearLocal'
			| 'leaveRoom'
			| 'deleteRoom'
			| 'disconnect'
	) {
		showRoomMenu = false;
		dispatch(eventName);
	}
</script>

<header class="chat-header {isDarkMode ? 'theme-dark' : ''}">
	{#if isMobileView}
		<button
			type="button"
			class="mobile-back-button"
			on:pointerdown|stopPropagation
			on:click|stopPropagation={() => dispatch('showMobileList')}
			aria-label="Back to room list"
		>
			Rooms
		</button>
	{/if}
	<button type="button" class="room-title-button" on:click={() => dispatch('openRoomDetails')}>
		<span class="presence-dot"></span>
		<span class="title-text">
			<span class="title-main">{roomName}</span>
			<span class="title-sub">
				{onlineCount} online
				{#if unreadCount > 0}
					- {unreadCount} unread
				{/if}
				{#if !isMember}
					- discoverable
				{/if}
			</span>
		</span>
	</button>

	<div class="header-actions" bind:this={headerActionsEl}>
		<button
			type="button"
			class="expiry-pill"
			on:click|stopPropagation={() => dispatch('openRoomDetails')}
			title="Remaining room lifetime"
			aria-label="Open room lifetime details"
		>
			{remainingLabel}
		</button>
		<button
			type="button"
			class="icon-button"
			on:click|stopPropagation={() => (showRoomMenu = !showRoomMenu)}
			title="More options"
		>
			...
		</button>
		{#if showRoomMenu}
			<div class="room-menu">
				<button type="button" on:click|stopPropagation={() => closeMenuThen('toggleRoomSearch')}>
					{showRoomSearch ? 'Hide search' : 'Search messages'}
				</button>
				<button type="button" on:click|stopPropagation={() => closeMenuThen('renameRoom')}>
					Rename room
				</button>
				<button
					type="button"
					on:click|stopPropagation={() => closeMenuThen('toggleBreakSelectionMode')}
				>
					{messageActionMode === 'break' ? 'Cancel Break Mode' : 'Start Break / New Topic'}
				</button>
				<button
					type="button"
					on:click|stopPropagation={() => closeMenuThen('togglePinSelectionMode')}
				>
					{messageActionMode === 'pin' ? 'Cancel Pin Mode' : '📌 Pin Message'}
				</button>
				<button
					type="button"
					on:click|stopPropagation={() => closeMenuThen('toggleEditSelectionMode')}
				>
					{messageActionMode === 'edit' ? 'Cancel Edit Mode' : 'Edit Message (Select One)'}
				</button>
				<button
					type="button"
					on:click|stopPropagation={() => closeMenuThen('toggleDeleteSelectionMode')}
				>
					{messageActionMode === 'delete' ? 'Cancel Delete Mode' : 'Delete Message (Select One)'}
				</button>
				<button type="button" on:click|stopPropagation={() => closeMenuThen('markRead')}>
					Mark read
				</button>
				<button type="button" on:click|stopPropagation={() => closeMenuThen('clearLocal')}>
					Clear local
				</button>
				{#if isMember}
					<button type="button" on:click|stopPropagation={() => closeMenuThen('leaveRoom')}>
						Leave Room
					</button>
				{/if}
				{#if isActiveRoomAdmin}
					<button type="button" on:click|stopPropagation={() => closeMenuThen('deleteRoom')}>
						Delete Room
					</button>
				{/if}
				<button type="button" on:click|stopPropagation={() => closeMenuThen('disconnect')}>
					Disconnect
				</button>
			</div>
		{/if}
	</div>
</header>

<style>
	.chat-header {
		position: relative;
		background: #f1f5fa;
		border-bottom: 1px solid #d4dce8;
		padding: 0.8rem 1rem;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.85rem;
	}

	.chat-header.theme-dark {
		background: #101012;
		border-bottom-color: #2b2b30;
	}

	.mobile-back-button {
		display: none;
		border: 1px solid #c6cfdb;
		background: #ebf1f8;
		color: #3b4a60;
		border-radius: 999px;
		padding: 0.35rem 0.65rem;
		font-size: 0.78rem;
		font-weight: 600;
		cursor: pointer;
		flex-shrink: 0;
	}

	.theme-dark .mobile-back-button {
		border-color: #3a3a40;
		background: #18181b;
		color: #ececf2;
	}

	.room-title-button {
		display: flex;
		align-items: center;
		gap: 0.55rem;
		color: #253246;
		min-width: 0;
		flex: 1;
		border: none;
		background: transparent;
		padding: 0;
		margin: 0;
		text-align: left;
		cursor: pointer;
	}

	.theme-dark .room-title-button {
		color: #f1f1f6;
	}

	.room-title-button:focus-visible {
		outline: 2px solid #8f8f98;
		outline-offset: 4px;
		border-radius: 8px;
	}

	.presence-dot {
		width: 10px;
		height: 10px;
		border-radius: 50%;
		background: #22c55e;
	}

	.theme-dark .presence-dot {
		background: #22c55e;
	}

	.title-text {
		display: inline-flex;
		flex-direction: column;
		align-items: flex-start;
		min-width: 0;
	}

	.title-main {
		font-size: 0.98rem;
		font-weight: 700;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.title-sub {
		font-size: 0.76rem;
		color: #66758c;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.theme-dark .title-sub {
		color: #ababb4;
	}

	.header-actions {
		display: flex;
		align-items: center;
		gap: 0.45rem;
		position: relative;
		cursor: default;
		flex-shrink: 0;
	}

	.expiry-pill {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-width: 3.1rem;
		height: 1.85rem;
		padding: 0 0.48rem;
		border-radius: 999px;
		border: 1px solid #c7d0dc;
		background: #e8edf4;
		color: #3a4a62;
		font-size: 0.76rem;
		font-weight: 700;
		letter-spacing: 0.01em;
		cursor: pointer;
	}

	.theme-dark .expiry-pill {
		border-color: #3a3a40;
		background: #18181b;
		color: #eeeef4;
	}

	.expiry-pill:hover {
		background: #dce4ef;
	}

	.theme-dark .expiry-pill:hover {
		background: #222227;
	}

	.expiry-pill:focus-visible {
		outline: 2px solid #8f8f98;
		outline-offset: 2px;
	}

	.icon-button {
		border: 1px solid #c7d0dc;
		background: #ebf1f8;
		border-radius: 6px;
		padding: 0.35rem 0.55rem;
		font-size: 0.78rem;
		cursor: pointer;
		color: #314057;
	}

	.theme-dark .icon-button {
		border-color: #3a3a40;
		background: #18181b;
		color: #eeeef4;
	}

	.room-menu {
		position: absolute;
		top: calc(100% + 6px);
		right: 0;
		background: #f3f7fc;
		border: 1px solid #cad3df;
		border-radius: 8px;
		box-shadow: 0 12px 24px rgba(0, 0, 0, 0.1);
		overflow: hidden;
		min-width: 170px;
		z-index: 100;
	}

	.theme-dark .room-menu {
		background: #161619;
		border-color: #34343a;
		box-shadow: 0 14px 28px rgba(0, 0, 0, 0.5);
	}

	.room-menu button {
		width: 100%;
		border: none;
		background: #f3f7fc;
		padding: 0.55rem 0.75rem;
		text-align: left;
		font-size: 0.84rem;
		cursor: pointer;
	}

	.theme-dark .room-menu button {
		background: #161619;
		color: #e9e9ef;
	}

	.room-menu button:hover {
		background: #e6edf6;
	}

	.theme-dark .room-menu button:hover {
		background: #222226;
	}

	@media (max-width: 900px) {
		.chat-header {
			padding: 0.68rem 0.75rem;
		}

		.expiry-pill {
			min-width: 2.7rem;
			height: 1.65rem;
			padding: 0 0.4rem;
			font-size: 0.71rem;
		}

		.mobile-back-button {
			display: inline-flex;
		}
	}
</style>
