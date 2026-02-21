<script lang="ts">
	type OnlineMember = {
		id: string;
		name: string;
		isOnline: boolean;
		joinedAt: number;
	};

	export let members: OnlineMember[] = [];
	export let isDarkMode = false;
</script>

<aside class="online-panel {isDarkMode ? 'theme-dark' : ''}">
	<div class="online-header">
		<h3>Online</h3>
		<span>{members.length}</span>
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
		background: linear-gradient(180deg, #0f1729 0%, #0b1323 100%);
	}

	.online-header {
		padding: 0.95rem 0.95rem 0.8rem;
		display: flex;
		justify-content: space-between;
		align-items: center;
		border-bottom: 1px solid #e3e8f0;
	}

	.online-panel.theme-dark .online-header {
		border-bottom-color: #253049;
	}

	.online-header h3 {
		margin: 0;
		font-size: 1rem;
		color: #1c2533;
	}

	.online-panel.theme-dark .online-header h3 {
		color: #dbe8ff;
	}

	.online-header span {
		font-size: 0.78rem;
		color: #f8fbff;
		font-weight: 700;
		background: #2f3138;
		padding: 0.2rem 0.5rem;
		border-radius: 999px;
	}

	.online-panel.theme-dark .online-header span {
		background: #1f2c43;
		color: #d9e6ff;
	}

	.online-list {
		flex: 1;
		overflow: auto;
		padding: 0.65rem 0.6rem 0.8rem;
		display: flex;
		flex-direction: column;
		gap: 0.38rem;
	}

	.online-member {
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
		border-color: #2c3853;
		background: #111d33;
		box-shadow: 0 3px 10px rgba(2, 8, 23, 0.36);
	}

	.member-dot {
		width: 10px;
		height: 10px;
		border-radius: 50%;
		flex-shrink: 0;
		box-shadow: 0 0 0 2px rgba(255, 255, 255, 0.92), 0 0 0 3px rgba(17, 24, 39, 0.18);
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
		color: #e4eeff;
	}

	.empty-label {
		color: #6b7280;
		font-size: 0.83rem;
		padding: 1rem 0.9rem;
		border: 1px dashed #d8dee9;
		border-radius: 12px;
		background: #f9fbff;
	}

	.online-panel.theme-dark .empty-label {
		color: #9fb0ce;
		border-color: #30405e;
		background: #111d33;
	}

	@media (max-width: 1199px) {
		.online-panel {
			display: none;
		}
	}
</style>
