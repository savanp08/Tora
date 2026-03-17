<script lang="ts">
	import type { OnlineMember } from '$lib/types/chat';
	import { taskStore } from '$lib/stores/tasks';

	export let onlineMembers: OnlineMember[] = [];

	type MemberWorkload = {
		id: string;
		name: string;
		isOnline: boolean;
		total: number;
		inProgress: number;
		todo: number;
		done: number;
	};

	$: membersByKey = buildMemberMap(onlineMembers);
	$: workload = buildWorkloadRows();

	function normalizeKey(value: string) {
		return value.trim().toLowerCase();
	}

	function buildMemberMap(members: OnlineMember[]) {
		const map = new Map<string, OnlineMember>();
		for (const member of members) {
			const key = normalizeKey(member.id);
			if (!key || map.has(key)) {
				continue;
			}
			map.set(key, member);
		}
		return map;
	}

	function displayNameFromID(rawId: string) {
		const trimmed = rawId.trim();
		if (!trimmed) {
			return 'Unassigned';
		}
		return trimmed.replace(/[_-]+/g, ' ');
	}

	function buildWorkloadRows(): MemberWorkload[] {
		const rows = new Map<string, MemberWorkload>();

		for (const member of onlineMembers) {
			const key = normalizeKey(member.id);
			if (!key) {
				continue;
			}
			rows.set(key, {
				id: member.id,
				name: member.name.trim() || displayNameFromID(member.id),
				isOnline: Boolean(member.isOnline),
				total: 0,
				inProgress: 0,
				todo: 0,
				done: 0
			});
		}

		for (const task of $taskStore) {
			const fallbackID = task.assigneeId.trim() || 'unassigned';
			const key = normalizeKey(fallbackID);
			if (!key) {
				continue;
			}
			const memberFromPresence = membersByKey.get(key);
			const row =
				rows.get(key) ??
				({
					id: fallbackID,
					name: memberFromPresence?.name.trim() || displayNameFromID(fallbackID),
					isOnline: Boolean(memberFromPresence?.isOnline),
					total: 0,
					inProgress: 0,
					todo: 0,
					done: 0
				} as MemberWorkload);

			row.total += 1;
			const status = task.status.trim().toLowerCase();
			if (status === 'done' || status === 'completed') {
				row.done += 1;
			} else if (status === 'in_progress') {
				row.inProgress += 1;
			} else {
				row.todo += 1;
			}
			rows.set(key, row);
		}

		return [...rows.values()].sort(
			(left, right) =>
				right.total - left.total ||
				Number(right.isOnline) - Number(left.isOnline) ||
				left.name.localeCompare(right.name, undefined, { sensitivity: 'base' })
		);
	}
</script>

<section class="people-panel" aria-label="People management">
	<header class="people-head">
		<h3>Team Workload</h3>
		<p>{workload.length} contributor{workload.length === 1 ? '' : 's'}</p>
	</header>

	{#if workload.length === 0}
		<div class="people-empty">No assignees yet. Assign tasks to start tracking people load.</div>
	{:else}
		<div class="people-list" role="list">
			{#each workload as member (member.id)}
				<article class="people-row" role="listitem">
					<div class="people-row-head">
						<strong>{member.name}</strong>
						<span class:is-online={member.isOnline}>
							{member.isOnline ? 'Online' : 'Offline'}
						</span>
					</div>
					<div class="people-row-stats">
						<span>Total {member.total}</span>
						<span>Doing {member.inProgress}</span>
						<span>To Do {member.todo}</span>
						<span>Done {member.done}</span>
					</div>
				</article>
			{/each}
		</div>
	{/if}
</section>

<style>
	.people-panel {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr);
		gap: 0.62rem;
	}

	.people-head h3 {
		margin: 0;
		font-size: 0.85rem;
	}

	.people-head p {
		margin: 0.2rem 0 0;
		font-size: 0.72rem;
		color: var(--ws-muted);
	}

	.people-empty {
		border: 1px dashed color-mix(in srgb, var(--ws-border) 90%, transparent);
		border-radius: 12px;
		padding: 0.78rem;
		font-size: 0.74rem;
		color: var(--ws-muted);
		background: color-mix(in srgb, var(--ws-surface) 90%, var(--ws-surface-soft));
	}

	.people-list {
		min-height: 0;
		overflow: auto;
		display: grid;
		gap: 0.5rem;
		padding-right: 0.2rem;
	}

	.people-row {
		border: 1px solid color-mix(in srgb, var(--ws-border) 90%, transparent);
		border-radius: 12px;
		padding: 0.56rem;
		background: color-mix(in srgb, var(--ws-surface) 88%, var(--ws-surface-soft));
		display: grid;
		gap: 0.4rem;
	}

	.people-row-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	.people-row-head strong {
		font-size: 0.8rem;
	}

	.people-row-head span {
		font-size: 0.68rem;
		padding: 0.16rem 0.46rem;
		border-radius: 999px;
		background: color-mix(in srgb, var(--ws-border) 75%, transparent);
		color: var(--ws-muted);
	}

	.people-row-head span.is-online {
		background: color-mix(in srgb, #22c55e 20%, transparent);
		color: color-mix(in srgb, #22c55e 85%, var(--ws-text));
	}

	.people-row-stats {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.3rem;
	}

	.people-row-stats span {
		font-size: 0.71rem;
		color: var(--ws-muted);
	}
</style>
