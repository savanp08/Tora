<script lang="ts">
	import type { OnlineMember } from '$lib/types/chat';
	import { normalizeIdentifier } from '$lib/utils/chat/core';
	import { createEventDispatcher } from 'svelte';

	export let show = false;
	export let isMobileView = false;
	export let roomName = 'Room';
	export let createdLabel = 'Unknown';
	export let expiresLabel = 'Unknown';
	export let isExtendingRoom = false;
	export let currentOnlineMembers: OnlineMember[] = [];
	export let isActiveRoomAdmin = false;
	export let currentUserId = '';
	export let formatDateTime: (timestamp: number) => string = (timestamp) =>
		new Date(timestamp).toLocaleString();

	const dispatch = createEventDispatcher<{
		close: void;
		extend: void;
		removeMember: { memberId: string };
	}>();
</script>

{#if show}
	{#if isMobileView}
		<button
			type="button"
			class="mobile-info-backdrop"
			aria-label="Close room details"
			on:click={() => dispatch('close')}
		></button>
	{/if}
	<section
		class="mobile-info-panel room-details-panel"
		class:desktop-room-panel={!isMobileView}
		role="dialog"
		aria-modal="true"
	>
		<header>
			<h3>{roomName}</h3>
			<button type="button" on:click={() => dispatch('close')}>Close</button>
		</header>
		<div class="mobile-info-content">
			<div class="room-details-card">
				<h4>Room Details</h4>
				<div class="room-detail-row">
					<span>Created</span>
					<strong>{createdLabel}</strong>
				</div>
				<div class="room-detail-row">
					<span>Expires</span>
					<strong>{expiresLabel}</strong>
				</div>
			</div>

			<div class="room-actions">
				<button
					type="button"
					class="extend-room-button"
					on:click={() => dispatch('extend')}
					disabled={isExtendingRoom}
				>
					{isExtendingRoom ? 'Extending...' : 'Extend Room (24h)'}
				</button>
				<p>Manually extends this room and its messages for 24 hours (Max 14 extensions).</p>
			</div>

			<h4 class="members-title">Members</h4>
			{#if currentOnlineMembers.length === 0}
				<div class="empty-label">No online members.</div>
			{:else}
				{#each currentOnlineMembers as member (member.id)}
					<div class="online-member">
						<span class="member-dot"></span>
						<div>
							<div class="member-name">{member.name}</div>
							<div class="member-meta">Joined {formatDateTime(member.joinedAt)}</div>
						</div>
						{#if isActiveRoomAdmin &&
							normalizeIdentifier(member.id) !== normalizeIdentifier(currentUserId)}
							<button
								type="button"
								class="member-remove-button"
								on:click={() => dispatch('removeMember', { memberId: member.id })}
							>
								Remove
							</button>
						{/if}
					</div>
				{/each}
			{/if}
		</div>
	</section>
{/if}

<style>
	.mobile-info-backdrop {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.45);
		border: none;
		z-index: 150;
	}

	.mobile-info-panel {
		position: fixed;
		right: 0;
		top: 0;
		height: 100vh;
		width: min(92vw, 320px);
		background: #fbfcfe;
		z-index: 160;
		box-shadow: -14px 0 30px rgba(0, 0, 0, 0.24);
		display: flex;
		flex-direction: column;
	}

	.desktop-room-panel {
		top: 84px;
		right: 18px;
		height: auto;
		width: min(34vw, 360px);
		max-height: calc(100vh - 104px);
		border-radius: 14px;
		border: 1px solid #d7dfeb;
		box-shadow: 0 18px 42px rgba(15, 23, 42, 0.22);
	}

	.mobile-info-panel header {
		padding: 0.9rem 1rem;
		border-bottom: 1px solid #dddddd;
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.mobile-info-panel header h3 {
		margin: 0;
		font-size: 1rem;
	}

	.mobile-info-panel header button {
		border: 1px solid #c9c9c9;
		background: #ffffff;
		border-radius: 7px;
		padding: 0.32rem 0.5rem;
		cursor: pointer;
		color: #111111;
	}

	.mobile-info-content {
		padding: 0.7rem 0.85rem;
		overflow: auto;
	}

	.room-actions {
		margin-bottom: 0.9rem;
		padding: 0.75rem;
		border: 1px solid #dddddf;
		border-radius: 10px;
		background: #f4f4f5;
	}

	.room-details-card {
		margin-bottom: 0.9rem;
		padding: 0.75rem;
		border: 1px solid #dddddf;
		border-radius: 10px;
		background: #f4f4f5;
	}

	.room-details-card h4 {
		margin: 0 0 0.5rem;
		font-size: 0.88rem;
		color: #111111;
	}

	.room-detail-row {
		display: flex;
		justify-content: space-between;
		align-items: baseline;
		gap: 0.65rem;
		font-size: 0.8rem;
		color: #5c5c5c;
	}

	.room-detail-row + .room-detail-row {
		margin-top: 0.35rem;
	}

	.room-detail-row strong {
		color: #111111;
		font-weight: 600;
	}

	.members-title {
		margin: 0 0 0.35rem;
		font-size: 0.88rem;
		color: #111111;
	}

	.room-actions p {
		margin: 0.45rem 0 0;
		font-size: 0.78rem;
		color: #5c5c5c;
	}

	.extend-room-button {
		width: 100%;
		border: 1px solid #111111;
		background: #111111;
		color: #ffffff;
		border-radius: 8px;
		padding: 0.48rem 0.65rem;
		font-size: 0.84rem;
		font-weight: 600;
		cursor: pointer;
	}

	.extend-room-button:disabled {
		opacity: 0.7;
		cursor: not-allowed;
	}

	.online-member {
		display: flex;
		align-items: center;
		gap: 0.52rem;
		padding: 0.45rem 0.2rem;
	}

	.member-dot {
		width: 9px;
		height: 9px;
		border-radius: 50%;
		background: #22c55e;
	}

	.member-name {
		font-size: 0.88rem;
		color: #141414;
	}

	.member-meta {
		font-size: 0.75rem;
		color: #676767;
	}

	.member-remove-button {
		margin-left: auto;
		border: 1px solid #d6d6dc;
		background: #ffffff;
		color: #3a3a42;
		border-radius: 8px;
		padding: 0.22rem 0.5rem;
		font-size: 0.72rem;
		cursor: pointer;
	}

	.member-remove-button:hover {
		background: #f1f1f4;
	}

	.empty-label {
		color: #666666;
		font-size: 0.84rem;
		padding: 1rem;
	}
</style>
