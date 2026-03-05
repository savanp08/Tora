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
	export let isBoardView = false;
	export let isCanvasOpen = false;
	export let lastWorkspaceTool: 'board' | 'canvas' = 'board';

	const dispatch = createEventDispatcher<{
		showMobileList: void;
		openRoomDetails: void;
		startAudioCall: void;
		startVideoCall: void;
		activateLastWorkspaceTool: void;
		toggleBoardView: void;
		toggleCanvas: void;
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
			| 'activateLastWorkspaceTool'
			| 'toggleBoardView'
			| 'toggleCanvas'
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

	function isQuickToolActive() {
		return lastWorkspaceTool === 'canvas' ? isCanvasOpen : isBoardView;
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
			<svg class="mobile-back-icon" viewBox="0 0 24 24" aria-hidden="true">
				<path d="M15.5 19.5 8 12l7.5-7.5" />
			</svg>
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
			class="icon-button call-trigger"
			on:click|stopPropagation={() => dispatch('startAudioCall')}
			title="Start voice call"
			aria-label="Start voice call"
		>
			<svg viewBox="0 0 24 24" aria-hidden="true">
				<path
					d="M6.6 10.8c1.6 3.1 3.9 5.5 7 7l2.3-2.3a1 1 0 0 1 1.1-.24c1.2.4 2.5.6 3.8.6a1 1 0 0 1 1 1V21a1 1 0 0 1-1 1C11 22 2 13 2 2a1 1 0 0 1 1-1h4.1a1 1 0 0 1 1 1c0 1.3.2 2.6.6 3.8a1 1 0 0 1-.24 1.1L6.6 10.8Z"
				/>
			</svg>
		</button>
		<button
			type="button"
			class="icon-button call-trigger"
			on:click|stopPropagation={() => dispatch('startVideoCall')}
			title="Start video call"
			aria-label="Start video call"
		>
			<svg viewBox="0 0 24 24" aria-hidden="true">
				<rect x="3.5" y="6.5" width="12" height="11" rx="2"></rect>
				<path d="M15.5 10 21 7v10l-5.5-3"></path>
			</svg>
		</button>
		<button
			type="button"
			class="workspace-quick-button"
			class:active={isQuickToolActive()}
			on:click|stopPropagation={() => dispatch('activateLastWorkspaceTool')}
			title={lastWorkspaceTool === 'canvas'
				? isCanvasOpen
					? 'Hide code canvas'
					: 'Show code canvas'
				: isBoardView
					? 'Switch to chat view'
					: 'Switch to board view'}
			aria-label={lastWorkspaceTool === 'canvas'
				? isCanvasOpen
					? 'Hide code canvas'
					: 'Show code canvas'
				: isBoardView
					? 'Switch to chat view'
					: 'Switch to board view'}
		>
			{#if lastWorkspaceTool === 'canvas'}
				<svg class="workspace-tool-icon" viewBox="0 0 24 24" aria-hidden="true">
					<path
						d="M5.5 6.5h13a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2h-4.5L9 21v-3.5H5.5a2 2 0 0 1-2-2v-7a2 2 0 0 1 2-2Z"
					/>
					<path d="M8.5 10.5h7M8.5 14h4.5" />
				</svg>
			{:else}
				<svg class="board-toggle-icon" viewBox="0 0 24 24" aria-hidden="true">
					<rect x="4.5" y="4.5" width="15" height="15" rx="2" ry="2" fill="none" />
					<path d="M9.5 4.5v15M14.5 4.5v15M4.5 9.5h15M4.5 14.5h15" />
				</svg>
			{/if}
		</button>
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
				<div class="room-menu-tools" role="group" aria-label="Workspace tools">
					<button
						type="button"
						class="room-menu-tool-button"
						class:active={isBoardView}
						on:click|stopPropagation={() => closeMenuThen('toggleBoardView')}
					>
						<svg class="room-menu-tool-icon" viewBox="0 0 24 24" aria-hidden="true">
							<rect x="4.5" y="4.5" width="15" height="15" rx="2" ry="2" fill="none" />
							<path d="M9.5 4.5v15M14.5 4.5v15M4.5 9.5h15M4.5 14.5h15" />
						</svg>
						<span>{isBoardView ? 'Close board' : 'Open board'}</span>
					</button>
					<button
						type="button"
						class="room-menu-tool-button"
						class:active={isCanvasOpen}
						on:click|stopPropagation={() => closeMenuThen('toggleCanvas')}
					>
						<svg class="room-menu-tool-icon" viewBox="0 0 24 24" aria-hidden="true">
							<path
								d="M5.5 6.5h13a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2h-4.5L9 21v-3.5H5.5a2 2 0 0 1-2-2v-7a2 2 0 0 1 2-2Z"
							/>
							<path d="M8.5 10.5h7M8.5 14h4.5" />
						</svg>
						<span>{isCanvasOpen ? 'Close canvas' : 'Open canvas'}</span>
					</button>
				</div>
				<div class="room-menu-divider"></div>
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
					{messageActionMode === 'edit' ? 'Cancel Edit Mode' : 'Edit Message'}
				</button>
				<button
					type="button"
					on:click|stopPropagation={() => closeMenuThen('toggleDeleteSelectionMode')}
				>
					{messageActionMode === 'delete' ? 'Cancel Delete Mode' : 'Delete Messages'}
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
		width: 1.95rem;
		height: 1.95rem;
		padding: 0;
		cursor: pointer;
		flex-shrink: 0;
		align-items: center;
		justify-content: center;
	}

	.mobile-back-icon {
		width: 0.95rem;
		height: 0.95rem;
		stroke: currentColor;
		stroke-width: 2.25;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
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

	.call-trigger {
		width: 1.95rem;
		height: 1.85rem;
		padding: 0;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		line-height: 0;
	}

	.call-trigger svg {
		width: 0.9rem;
		height: 0.9rem;
		stroke: currentColor;
		stroke-width: 1.7;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.workspace-quick-button {
		border: 1px solid #c7d0dc;
		background: #ebf1f8;
		border-radius: 6px;
		width: 1.95rem;
		height: 1.85rem;
		padding: 0;
		cursor: pointer;
		color: #314057;
		line-height: 0;
		display: inline-flex;
		align-items: center;
		justify-content: center;
	}

	.workspace-tool-icon,
	.board-toggle-icon,
	.room-menu-tool-icon {
		width: 0.92rem;
		height: 0.92rem;
		stroke: currentColor;
		stroke-width: 1.7;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.workspace-quick-button.active {
		border-color: #0f9d58;
		background: #dcfce7;
		color: #166534;
	}

	.theme-dark .workspace-quick-button {
		border-color: #3a3a40;
		background: #18181b;
		color: #eeeef4;
	}

	.theme-dark .workspace-quick-button.active {
		border-color: #22c55e;
		background: #14532d;
		color: #dcfce7;
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

	.room-menu-divider {
		height: 1px;
		background: #d7dfea;
		margin: 0.1rem 0;
	}

	.theme-dark .room-menu {
		background: #161619;
		border-color: #34343a;
		box-shadow: 0 14px 28px rgba(0, 0, 0, 0.5);
	}

	.theme-dark .room-menu-divider {
		background: #2e2e35;
	}

	.room-menu-tools {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.45rem;
		padding: 0.6rem 0.75rem 0.5rem;
	}

	.room-menu-tool-button {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		gap: 0.35rem;
		border: 1px solid #cad3df;
		background: #edf3f9;
		border-radius: 0.7rem;
		padding: 0.5rem 0.45rem;
		color: #314057;
		font-size: 0.75rem;
		font-weight: 700;
		line-height: 1.1;
	}

	.room-menu-tool-button.active {
		border-color: #0f9d58;
		background: #dcfce7;
		color: #166534;
	}

	.theme-dark .room-menu-tool-button {
		border-color: #3a3a40;
		background: #1b1b20;
		color: #eeeef4;
	}

	.theme-dark .room-menu-tool-button.active {
		border-color: #22c55e;
		background: #14532d;
		color: #dcfce7;
	}

	.room-menu button:not(.room-menu-tool-button) {
		width: 100%;
		border: none;
		background: #f3f7fc;
		padding: 0.55rem 0.75rem;
		text-align: left;
		font-size: 0.84rem;
		cursor: pointer;
	}

	.theme-dark .room-menu button:not(.room-menu-tool-button) {
		background: #161619;
		color: #e9e9ef;
	}

	.room-menu button:not(.room-menu-tool-button):hover {
		background: #e6edf6;
	}

	.theme-dark .room-menu button:not(.room-menu-tool-button):hover {
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
			width: 1.75rem;
			height: 1.75rem;
		}

		.workspace-quick-button {
			width: 1.75rem;
			height: 1.65rem;
		}
	}
</style>
