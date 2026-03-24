<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import type { OnlineMember } from '$lib/types/chat';
	import { normalizeIdentifier } from '$lib/utils/chat/core';
	import { resolveSenderNameColor } from '$lib/utils/chat/senderNameColors';

	export let members: OnlineMember[] = [];
	export let isDarkMode = false;
	export let canCollapse = false;
	export let isCollapsed = false;
	export let currentUserId = '';

	const dispatch = createEventDispatcher<{
		toggleCollapse: void;
	}>();

	function buildMemberInitials(name: string) {
		const normalized = name.trim();
		if (!normalized) {
			return '?';
		}
		const parts = normalized.split(/\s+/).slice(0, 2);
		return parts.map((part) => part.charAt(0).toUpperCase()).join('');
	}

	function resolveMemberAvatarColor(member: OnlineMember) {
		const normalizedCurrentUserID = normalizeIdentifier(currentUserId || '');
		const normalizedMemberID = normalizeIdentifier(member.id || '');
		const isOwn =
			Boolean(normalizedCurrentUserID) &&
			Boolean(normalizedMemberID) &&
			normalizedCurrentUserID === normalizedMemberID;
		return resolveSenderNameColor({
			senderId: member.id || '',
			senderName: member.name || '',
			isOwnMessage: isOwn,
			isDarkMode
		});
	}
</script>

<aside class="online-panel {isDarkMode ? 'theme-dark' : ''} {isCollapsed ? 'is-collapsed' : ''}">
	{#if isCollapsed}
		<div class="online-activity-bar">
			{#if canCollapse}
				<button
					type="button"
					class="online-activity-toggle"
					on:click={() => dispatch('toggleCollapse')}
					title="Expand online list"
					aria-label="Expand online list"
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d="M9 6l6 6-6 6"></path>
					</svg>
				</button>
			{/if}
			<span class="online-activity-label">Online</span>
			<span class="online-activity-count">{members.length}</span>
		</div>
	{:else}
		<div class="online-header">
			<div class="online-header-title">
				<h3>Online</h3>
				<span>{members.length}</span>
			</div>
			{#if canCollapse}
				<button
					type="button"
					class="online-collapse-button"
					on:click={() => dispatch('toggleCollapse')}
					title="Collapse online list"
					aria-label="Collapse online list"
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d="M15 6l-6 6 6 6"></path>
					</svg>
				</button>
			{/if}
		</div>
		<div class="online-list">
			{#if members.length === 0}
				<div class="empty-label">No online members.</div>
			{:else}
				{#each members as member (member.id)}
					<div class="online-member">
						<div
							class="member-avatar-wrap"
							style={`--member-avatar-color:${resolveMemberAvatarColor(member)};`}
						>
							<span class="member-avatar">{buildMemberInitials(member.name)}</span>
							<span class="member-status-dot" aria-hidden="true"></span>
						</div>
						<div class="member-copy">
							<span class="member-name">{member.name}</span>
							<span class="member-state-label">Active now</span>
						</div>
					</div>
				{/each}
			{/if}
		</div>
	{/if}
</aside>

<style>
	.online-panel {
		background: linear-gradient(180deg, #fcfdff 0%, #f3f6fb 100%);
		display: flex;
		flex-direction: column;
		width: 100%;
		min-height: 0;
	}

	.online-panel.theme-dark {
		background: linear-gradient(180deg, #0b0b0d 0%, #121214 100%);
	}

	.online-panel.is-collapsed {
		align-items: stretch;
	}

	.online-activity-bar {
		flex: 1;
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 0.8rem;
		padding: 0.8rem 0.25rem;
	}

	.online-activity-toggle {
		width: 2rem;
		height: 2rem;
		padding: 0;
		border: 1px solid #c7d0de;
		border-radius: 6px;
		background: #edf2f8;
		color: #324057;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		transition:
			background 140ms ease,
			border-color 140ms ease,
			color 140ms ease,
			transform 140ms ease;
	}

	.online-panel.theme-dark .online-activity-toggle {
		border-color: #2b3853;
		background: #111b2f;
		color: #d6e1f6;
	}

	.online-activity-toggle:hover {
		background: #dfe8f4;
		border-color: #aebfd4;
		transform: translateY(-1px);
	}

	.online-panel.theme-dark .online-activity-toggle:hover {
		background: #22324f;
		border-color: #41587d;
	}

	.online-activity-toggle svg {
		width: 13px;
		height: 13px;
		stroke: currentColor;
		stroke-width: 2;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.online-activity-label {
		writing-mode: vertical-rl;
		transform: rotate(180deg);
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: #627186;
		user-select: none;
	}

	.online-panel.theme-dark .online-activity-label {
		color: #93a4c4;
	}

	.online-activity-count {
		font-size: 0.72rem;
		color: #f8fbff;
		font-weight: 700;
		background: #2f3138;
		padding: 0.18rem 0.46rem;
		border-radius: 999px;
	}

	.online-panel.theme-dark .online-activity-count {
		background: #222226;
		color: #ececf2;
	}

	.online-header {
		padding: 0.95rem 0.95rem 0.8rem;
		display: flex;
		justify-content: space-between;
		align-items: center;
		border-bottom: 1px solid #e3e8f0;
	}

	.online-panel.theme-dark .online-header {
		border-bottom-color: #2b2b30;
	}

	.online-header h3 {
		margin: 0;
		font-size: 1rem;
		color: #1c2533;
	}

	.online-panel.theme-dark .online-header h3 {
		color: #f0f0f5;
	}

	.online-header-title {
		display: inline-flex;
		align-items: center;
		gap: 0.58rem;
		min-width: 0;
	}

	.online-header-title span {
		font-size: 0.78rem;
		color: #f8fbff;
		font-weight: 700;
		background: #2f3138;
		padding: 0.2rem 0.5rem;
		border-radius: 999px;
	}

	.online-panel.theme-dark .online-header-title span {
		background: #222226;
		color: #ececf2;
	}

	.online-collapse-button {
		width: 2rem;
		height: 2rem;
		padding: 0;
		border: 1px solid #c7d0de;
		border-radius: 6px;
		background: #edf2f8;
		color: #324057;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		transition:
			background 140ms ease,
			border-color 140ms ease,
			color 140ms ease,
			transform 140ms ease;
	}

	.online-panel.theme-dark .online-collapse-button {
		border-color: #2b3853;
		background: #111b2f;
		color: #d6e1f6;
	}

	.online-collapse-button:hover {
		background: #dfe8f4;
		border-color: #aebfd4;
		transform: translateY(-1px);
	}

	.online-panel.theme-dark .online-collapse-button:hover {
		background: #22324f;
		border-color: #41587d;
	}

	.online-collapse-button svg {
		width: 13px;
		height: 13px;
		stroke: currentColor;
		stroke-width: 2;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.online-list {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
		padding: 0.65rem 0.6rem 0.8rem;
		display: flex;
		flex-direction: column;
		gap: 0.38rem;
		-webkit-overflow-scrolling: touch;
		overscroll-behavior: contain;
		scrollbar-width: none;
		-ms-overflow-style: none;
	}

	.online-list::-webkit-scrollbar {
		width: 0;
		height: 0;
		display: none;
	}

	.online-member {
		flex: 0 0 auto;
		display: flex;
		align-items: center;
		gap: 0.68rem;
		padding: 0.64rem 0.68rem;
		border: 1px solid #dae2f0;
		border-radius: 14px;
		background:
			linear-gradient(135deg, rgba(255, 255, 255, 0.97), rgba(245, 250, 255, 0.94)),
			#ffffff;
		box-shadow:
			0 8px 18px rgba(25, 44, 74, 0.08),
			inset 0 1px 0 rgba(255, 255, 255, 0.72);
		white-space: nowrap;
		transition:
			transform 160ms ease,
			box-shadow 160ms ease,
			border-color 160ms ease;
	}

	.online-panel.theme-dark .online-member {
		border-color: rgba(159, 182, 221, 0.25);
		background:
			linear-gradient(132deg, rgba(20, 25, 36, 0.94), rgba(27, 34, 48, 0.92)),
			#171a22;
		box-shadow:
			0 10px 20px rgba(0, 0, 0, 0.36),
			inset 0 1px 0 rgba(255, 255, 255, 0.04);
	}

	.online-member:hover {
		transform: translateY(-1px);
		box-shadow:
			0 14px 24px rgba(25, 44, 74, 0.12),
			inset 0 1px 0 rgba(255, 255, 255, 0.72);
		border-color: #c9d8ed;
	}

	.online-panel.theme-dark .online-member:hover {
		box-shadow:
			0 14px 30px rgba(0, 0, 0, 0.44),
			inset 0 1px 0 rgba(255, 255, 255, 0.08);
		border-color: rgba(159, 182, 221, 0.34);
	}

	.member-avatar-wrap {
		position: relative;
		width: 2rem;
		height: 2rem;
		flex: 0 0 auto;
	}

	.member-avatar {
		width: 2rem;
		height: 2rem;
		border-radius: 999px;
		display: grid;
		place-items: center;
		background: var(--member-avatar-color, #64748b);
		color: #f8fbff;
		font-size: 0.66rem;
		font-weight: 800;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		box-shadow: 0 8px 14px rgba(10, 15, 26, 0.2);
	}

	.member-status-dot {
		position: absolute;
		left: -0.08rem;
		bottom: -0.08rem;
		width: 0.62rem;
		height: 0.62rem;
		border-radius: 999px;
		background: #22c55e;
		border: 2px solid rgba(255, 255, 255, 0.96);
		box-shadow: 0 0 0 1px rgba(15, 23, 42, 0.14);
	}

	.online-panel.theme-dark .member-status-dot {
		border-color: #151b27;
		box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.18);
	}

	.member-copy {
		min-width: 0;
		display: grid;
		gap: 0.1rem;
	}

	.member-name {
		font-size: 0.84rem;
		font-weight: 600;
		color: #141d2a;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.online-panel.theme-dark .member-name {
		color: #f0f0f5;
	}

	.member-state-label {
		font-size: 0.66rem;
		color: #5c6d88;
		font-weight: 600;
	}

	.online-panel.theme-dark .member-state-label {
		color: #9bb0d2;
	}

	.empty-label {
		flex: 0 0 auto;
		color: #6b7280;
		font-size: 0.83rem;
		padding: 1rem 0.9rem;
		border: 1px dashed #d8dee9;
		border-radius: 12px;
		background: #f9fbff;
	}

	.online-panel.theme-dark .empty-label {
		color: #aeaeb7;
		border-color: #35353a;
		background: #18181b;
	}

	@media (max-width: 1199px) {
		.online-panel {
			display: none;
		}
	}
</style>
