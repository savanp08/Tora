<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import type { OnlineMember } from '$lib/types/chat';

	export let members: OnlineMember[] = [];
	export let isDarkMode = false;
	export let canCollapse = false;
	export let isCollapsed = false;

	const dispatch = createEventDispatcher<{
		toggleCollapse: void;
	}>();
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
						<span class="member-dot"></span>
						<span class="member-name">{member.name}</span>
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
		gap: 0.62rem;
		padding: 0.72rem 0.75rem;
		border: 1px solid #dde3ec;
		border-radius: 12px;
		background: #ffffff;
		box-shadow: 0 1px 5px rgba(15, 23, 42, 0.05);
		white-space: nowrap;
	}

	.online-panel.theme-dark .online-member {
		border-color: #333338;
		background: #18181b;
		box-shadow: 0 3px 10px rgba(0, 0, 0, 0.36);
	}

	.member-dot {
		width: 10px;
		height: 10px;
		border-radius: 50%;
		flex-shrink: 0;
		box-shadow:
			0 0 0 2px rgba(255, 255, 255, 0.92),
			0 0 0 3px rgba(17, 24, 39, 0.18);
		background: #22c55e;
	}

	.online-panel.theme-dark .member-dot {
		background: #22c55e;
	}

	.member-name {
		font-size: 0.88rem;
		font-weight: 600;
		color: #141d2a;
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.online-panel.theme-dark .member-name {
		color: #f0f0f5;
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
