<script lang="ts">
	type OnlineMember = {
		id: string;
		name: string;
		isOnline: boolean;
		joinedAt: number;
	};

	export let members: OnlineMember[] = [];
</script>

<aside class="online-panel">
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
		border-left: 1px solid #dddddd;
		background: #ffffff;
		display: flex;
		flex-direction: column;
	}

	.online-header {
		padding: 1rem;
		display: flex;
		justify-content: space-between;
		align-items: center;
		border-bottom: 1px solid #eeeeee;
	}

	.online-header h3 {
		margin: 0;
		font-size: 0.95rem;
	}

	.online-header span {
		font-size: 0.82rem;
		color: #2a2a2a;
		font-weight: 700;
	}

	.online-list {
		flex: 1;
		overflow: auto;
		padding: 0.75rem;
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

	.empty-label {
		color: #666666;
		font-size: 0.84rem;
		padding: 1rem;
	}

	@media (max-width: 1199px) {
		.online-panel {
			display: none;
		}
	}
</style>
