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
	};

	export let myRooms: ChatThread[] = [];
	export let discoverableRooms: ChatThread[] = [];
	export let activeRoomId = '';
	export let showLeftMenu = false;
	export let chatListSearch = '';

	const dispatch = createEventDispatcher<{
		select: { id: string; isMember: boolean; status: ThreadStatus };
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
						<span class="avatar">{thread.name.charAt(0).toUpperCase()}</span>
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
						<span class="avatar">{thread.name.charAt(0).toUpperCase()}</span>
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
		border-right: 1px solid #d9dee4;
		background: #ffffff;
	}

	.room-list-header {
		padding: 1rem 1rem 0.75rem;
		display: flex;
		justify-content: space-between;
		align-items: center;
		position: relative;
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
		color: #166534;
		background: #dcfce7;
		padding: 0.2rem 0.5rem;
		border-radius: 999px;
	}

	.room-list-search {
		padding: 0 1rem 0.75rem;
	}

	.room-list-search input {
		width: 100%;
		border: 1px solid #cfd8e3;
		border-radius: 8px;
		padding: 0.55rem 0.7rem;
		font-size: 0.92rem;
	}

	.room-items {
		flex: 1;
		overflow: auto;
	}

	.section-label {
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: #64748b;
		padding: 0.55rem 0.9rem 0.35rem;
	}

	.room-item {
		width: 100%;
		display: flex;
		gap: 0.75rem;
		padding: 0.78rem 0.9rem;
		border: none;
		border-top: 1px solid #f1f3f6;
		text-align: left;
		background: transparent;
		cursor: pointer;
	}

	.room-item:hover {
		background: #f8fafc;
	}

	.room-item.selected {
		background: #e8f5ec;
	}

	.room-item.discoverable.selected {
		background: #fff3de;
	}

	.avatar {
		width: 38px;
		height: 38px;
		border-radius: 50%;
		background: #dde7f4;
		color: #1e293b;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		font-weight: 700;
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
		color: #162136;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.room-time {
		font-size: 0.78rem;
		color: #607188;
		white-space: nowrap;
	}

	.room-preview {
		font-size: 0.82rem;
		color: #546479;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.unread {
		min-width: 20px;
		height: 20px;
		border-radius: 999px;
		background: #16a34a;
		color: #ffffff;
		font-size: 0.75rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		font-weight: 700;
	}

	.icon-button {
		border: 1px solid #cdd7e1;
		background: #ffffff;
		border-radius: 6px;
		padding: 0.35rem 0.55rem;
		font-size: 0.78rem;
		cursor: pointer;
	}

	.room-menu {
		position: absolute;
		top: calc(100% + 6px);
		right: 0;
		background: #ffffff;
		border: 1px solid #d8e0e9;
		border-radius: 8px;
		box-shadow: 0 8px 20px rgba(15, 23, 42, 0.12);
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
		background: #ffffff;
		padding: 0.55rem 0.75rem;
		text-align: left;
		font-size: 0.84rem;
		cursor: pointer;
	}

	.empty-label {
		color: #64748b;
		font-size: 0.84rem;
		padding: 1rem;
	}
</style>
