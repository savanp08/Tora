<script lang="ts">
	import { createEventDispatcher } from 'svelte';

	type ThreadStatus = 'joined' | 'discoverable';

	type ChatThread = {
		id: string;
		name: string;
		lastMessage: string;
		lastActivity: number;
		unread: number;
		status: ThreadStatus;
		memberCount?: number;
		parentRoomId?: string;
		originMessageId?: string;
	};

	export let myRooms: ChatThread[] = [];
	export let discoverableRooms: ChatThread[] = [];
	export let activeRoomId = '';
	export let showLeftMenu = false;
	export let chatListSearch = '';

	const dispatch = createEventDispatcher<{
		select: { id: string; isMember: boolean; status: ThreadStatus };
		jumpOrigin: {
			parentRoomId: string;
			originMessageId: string;
			fallbackRoomId: string;
			fallbackIsMember: boolean;
		};
		toggleMenu: void;
		createRoom: void;
	}>();

	$: totalRooms = myRooms.length + discoverableRooms.length;

	function formatClock(timestamp: number) {
		const safe = Number.isFinite(timestamp) ? timestamp : Date.now();
		return new Date(safe).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
	}

	function selectRoom(thread: ChatThread) {
		dispatch('select', {
			id: thread.id,
			isMember: thread.status === 'joined',
			status: thread.status
		});
	}

	function hasBreakOrigin(thread: ChatThread) {
		return Boolean(thread.parentRoomId && thread.originMessageId);
	}

	function jumpToOrigin(thread: ChatThread) {
		if (!hasBreakOrigin(thread)) {
			return;
		}
		dispatch('jumpOrigin', {
			parentRoomId: thread.parentRoomId || '',
			originMessageId: thread.originMessageId || '',
			fallbackRoomId: thread.id,
			fallbackIsMember: thread.status === 'joined'
		});
	}

	function onAvatarKeyDown(event: KeyboardEvent, thread: ChatThread) {
		if (!hasBreakOrigin(thread)) {
			return;
		}
		if (event.key === 'Enter' || event.key === ' ') {
			event.preventDefault();
			jumpToOrigin(thread);
		}
	}
</script>

<aside class="room-list">
	<div class="room-list-header">
		<div class="list-title">
			<h2>Chats</h2>
			<span class="thread-count">{totalRooms}</span>
		</div>
		<div class="list-actions">
			<button
				type="button"
				class="icon-button"
				on:click={() => dispatch('toggleMenu')}
				title="Room options"
			>
				...
			</button>
			{#if showLeftMenu}
				<div class="room-menu left-menu">
					<button type="button" on:click={() => dispatch('createRoom')}>New room</button>
				</div>
			{/if}
		</div>
	</div>
	<div class="room-list-search">
		<input type="text" bind:value={chatListSearch} placeholder="Search names or messages" />
	</div>
	<div class="room-items">
		{#if myRooms.length === 0 && discoverableRooms.length === 0}
			<div class="empty-label">No chats matched your search.</div>
		{:else}
			{#if myRooms.length > 0}
				<div class="section-label">Joined</div>
				{#each myRooms as thread (thread.id)}
					<button
						type="button"
						class="room-item {thread.id === activeRoomId ? 'selected' : ''}"
						on:click={() => selectRoom(thread)}
					>
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<!-- svelte-ignore a11y_click_events_have_key_events -->
						<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
						<span
							class="avatar {hasBreakOrigin(thread) ? 'origin-avatar' : ''}"
							role={hasBreakOrigin(thread) ? 'button' : undefined}
							tabindex={hasBreakOrigin(thread) ? 0 : undefined}
							title={hasBreakOrigin(thread) ? 'Open origin message context' : thread.name}
							on:click|stopPropagation={() => jumpToOrigin(thread)}
							on:keydown={(event) => onAvatarKeyDown(event, thread)}
						>
							{thread.name.charAt(0).toUpperCase()}
						</span>
						<span class="item-main">
							<span class="item-top">
								<span class="room-name-wrap">
									<span class="status-dot green"></span>
									<span class="room-name">{thread.name}</span>
								</span>
								<span class="room-time">{formatClock(thread.lastActivity)}</span>
							</span>
							<span class="item-bottom">
								<span class="room-preview">{thread.lastMessage || 'No messages yet'}</span>
								{#if thread.unread > 0}
									<span class="unread">{thread.unread}</span>
								{/if}
							</span>
						</span>
					</button>
				{/each}
			{/if}

			{#if discoverableRooms.length > 0}
				<div class="section-label">Discover</div>
				{#each discoverableRooms as thread (thread.id)}
					<button
						type="button"
						class="room-item discoverable {thread.id === activeRoomId ? 'selected' : ''}"
						on:click={() => selectRoom(thread)}
					>
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<!-- svelte-ignore a11y_click_events_have_key_events -->
						<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
						<span
							class="avatar {hasBreakOrigin(thread) ? 'origin-avatar' : ''}"
							role={hasBreakOrigin(thread) ? 'button' : undefined}
							tabindex={hasBreakOrigin(thread) ? 0 : undefined}
							title={hasBreakOrigin(thread) ? 'Open origin message context' : thread.name}
							on:click|stopPropagation={() => jumpToOrigin(thread)}
							on:keydown={(event) => onAvatarKeyDown(event, thread)}
						>
							{thread.name.charAt(0).toUpperCase()}
						</span>
						<span class="item-main">
							<span class="item-top">
								<span class="room-name-wrap">
									<span class="status-dot orange"></span>
									<span class="room-name">{thread.name}</span>
								</span>
								<span class="room-time">{formatClock(thread.lastActivity)}</span>
							</span>
							<span class="item-bottom">
								<span class="room-preview">{thread.lastMessage || 'Preview and join'}</span>
							</span>
						</span>
					</button>
				{/each}
			{/if}
		{/if}
	</div>
</aside>

<style>
	.room-list {
		display: flex;
		flex-direction: column;
		border-right: 1px solid #d6dde7;
		background: #f6f8fb;
		height: 100%;
		min-height: 0;
	}

	.room-list-header {
		padding: 0.95rem 0.95rem 0.72rem;
		display: flex;
		justify-content: space-between;
		align-items: center;
		position: relative;
		border-bottom: 1px solid #dfe5ee;
	}

	.list-title {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}

	.list-actions {
		position: relative;
	}

	.room-list-header h2 {
		margin: 0;
		font-size: 1.05rem;
	}

	.thread-count {
		font-size: 0.85rem;
		font-weight: 700;
		color: #ffffff;
		background: #2a3442;
		padding: 0.2rem 0.5rem;
		border-radius: 999px;
	}

	.room-list-search {
		padding: 0 1rem 0.75rem;
	}

	.room-list-search input {
		width: 100%;
		border: 1px solid #ccd5e2;
		border-radius: 8px;
		padding: 0.55rem 0.7rem;
		font-size: 0.92rem;
		background: #f9fbfd;
		color: #1f2937;
	}

	.room-items {
		flex: 1;
		overflow: auto;
		display: flex;
		flex-direction: column;
		gap: 0.36rem;
		padding: 0.5rem 0;
	}

	.section-label {
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: #666666;
		padding: 0.55rem 0.9rem 0.35rem;
	}

	.room-item {
		width: 90%;
		margin: 0 auto;
		display: flex;
		gap: 0.75rem;
		padding: 0.8rem 0.85rem;
		border: 1px solid #dbe2ec;
		border-radius: 12px;
		text-align: left;
		background: #fbfcfe;
		box-shadow: 0 2px 6px rgba(15, 23, 42, 0.04);
		cursor: pointer;
		transition:
			background 140ms ease,
			color 140ms ease,
			border-color 140ms ease;
	}

	.room-item:hover {
		background: #f2f5fa;
		border-color: #c6d2e2;
	}

	.room-item.selected {
		background: #2a3442;
		border-color: #2a3442;
	}

	.room-item.discoverable.selected {
		background: #39475a;
		border-color: #39475a;
	}

	.avatar {
		width: 38px;
		height: 38px;
		border-radius: 50%;
		background: #e6ebf2;
		color: #243245;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		font-weight: 700;
		flex-shrink: 0;
	}

	.origin-avatar {
		cursor: pointer;
		box-shadow: inset 0 0 0 2px rgba(39, 51, 65, 0.18);
	}

	.origin-avatar:hover {
		box-shadow: inset 0 0 0 2px #f59e0b;
	}

	.item-main {
		min-width: 0;
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 0.35rem;
	}

	.item-top,
	.item-bottom {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.6rem;
	}

	.room-name-wrap {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		min-width: 0;
	}

	.status-dot {
		width: 8px;
		height: 8px;
		border-radius: 999px;
		flex-shrink: 0;
		border: 1px solid rgba(0, 0, 0, 0.22);
	}

	.status-dot.green {
		background: #22c55e;
	}

	.status-dot.orange {
		background: #f59e0b;
	}

	.room-name {
		font-size: 0.92rem;
		font-weight: 600;
		color: #161616;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.room-time {
		font-size: 0.78rem;
		color: #6a7688;
		white-space: nowrap;
	}

	.room-preview {
		font-size: 0.82rem;
		color: #627082;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.unread {
		min-width: 20px;
		height: 20px;
		border-radius: 999px;
		background: #273341;
		color: #ffffff;
		font-size: 0.75rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		font-weight: 700;
	}

	.icon-button {
		border: 1px solid #ccd5e2;
		background: #f7f9fc;
		border-radius: 6px;
		padding: 0.35rem 0.55rem;
		font-size: 0.78rem;
		cursor: pointer;
		color: #2d3748;
	}

	.room-menu {
		position: absolute;
		top: calc(100% + 6px);
		right: 0;
		background: #fbfcfe;
		border: 1px solid #d2dae6;
		border-radius: 8px;
		box-shadow: 0 12px 24px rgba(15, 23, 42, 0.12);
		overflow: hidden;
		min-width: 138px;
		z-index: 100;
	}

	.left-menu {
		left: 0;
		right: auto;
	}

	.room-menu button {
		width: 100%;
		border: none;
		background: #fbfcfe;
		padding: 0.55rem 0.75rem;
		text-align: left;
		font-size: 0.84rem;
		cursor: pointer;
	}

	.room-menu button:hover {
		background: #eef2f7;
	}

	.room-item.selected .avatar {
		background: #2b2b2b;
		color: #ffffff;
	}

	.room-item.selected .room-name,
	.room-item.selected .room-time,
	.room-item.selected .room-preview {
		color: #f3f3f3;
	}

	.room-item.selected .status-dot {
		border-color: rgba(255, 255, 255, 0.45);
	}

	.empty-label {
		color: #666666;
		font-size: 0.84rem;
		padding: 1rem;
	}

	@media (max-width: 900px) {
		.room-list {
			border-right: none;
		}

		.room-list-header {
			padding-top: 0.85rem;
		}
	}
</style>
