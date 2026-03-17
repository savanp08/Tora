<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import type { OnlineMember } from '$lib/types/chat';
	import { currentUser } from '$lib/store';
	import { taskStore, upsertTaskStoreEntry, type Task } from '$lib/stores/tasks';
	import { normalizeRoomIDValue } from '$lib/utils/chat/core';
	import { sendSocketPayload } from '$lib/ws';
	import { buildTaskSocketPayload } from '$lib/ws/client';

	export let onlineMembers: OnlineMember[] = [];
	export let canEdit = true;

	const dispatch = createEventDispatcher<{
		requestTaskEdit: { taskId: string };
	}>();

	const OVERLOAD_THRESHOLD = 20;
	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';

	type WorkloadUser = {
		key: string;
		id: string;
		name: string;
		isOnline: boolean;
		taskCount: number;
		totalEffortScore: number;
		tasks: Task[];
	};

	let expandedUserKey = '';
	let reassignError = '';
	let savingTaskIds = new Set<string>();

	$: sessionUserID = ($currentUser?.id || '').trim();
	$: sessionUsername = ($currentUser?.username || '').trim();
	$: tasks = [...$taskStore];
	$: presenceByKey = buildPresenceMap(onlineMembers);
	$: workloadUsers = buildWorkloadUsers();
	$: workloadByKey = new Map(workloadUsers.map((entry) => [entry.key, entry]));
	$: overloadedUsers = workloadUsers.filter((user) => user.totalEffortScore > OVERLOAD_THRESHOLD);

	function normalizeUserKey(value: string) {
		const trimmed = value.trim().toLowerCase();
		return trimmed || 'unassigned';
	}

	function buildPresenceMap(members: OnlineMember[]) {
		const map = new Map<string, OnlineMember>();
		for (const member of members) {
			const key = normalizeUserKey(member.id);
			if (!key || map.has(key)) {
				continue;
			}
			map.set(key, member);
		}
		return map;
	}

	function parseDescriptionMetadata(description: string) {
		const trimmed = description.trim();
		if (!trimmed) {
			return [] as Array<{ key: string; value: string }>;
		}
		const metadataMatch = trimmed.match(/\[([^\]]+)\]\s*$/);
		if (!metadataMatch) {
			return [] as Array<{ key: string; value: string }>;
		}
		const entries: Array<{ key: string; value: string }> = [];
		for (const section of metadataMatch[1].split('|')) {
			const [rawLabel, ...rawValueParts] = section.split(':');
			const key = rawLabel?.trim().toLowerCase();
			const value = rawValueParts.join(':').trim();
			if (!key || !value) {
				continue;
			}
			entries.push({ key, value });
		}
		return entries;
	}

	function parseEffortScore(task: Task) {
		const entries = parseDescriptionMetadata(task.description || '');
		const effortRaw = entries.find((entry) => entry.key === 'effort')?.value ?? '';
		const parsed = Number(effortRaw.replace(/[^\d.\-]/g, ''));
		if (Number.isFinite(parsed) && parsed > 0) {
			return parsed;
		}
		return 0;
	}

	function displayNameFromIdentifier(value: string) {
		const trimmed = value.trim();
		if (!trimmed) {
			return 'Unassigned';
		}
		return trimmed.replace(/[_-]+/g, ' ').replace(/\b\w/g, (char) => char.toUpperCase());
	}

	function workloadSort(left: WorkloadUser, right: WorkloadUser) {
		return (
			right.totalEffortScore - left.totalEffortScore ||
			right.taskCount - left.taskCount ||
			Number(right.isOnline) - Number(left.isOnline) ||
			left.name.localeCompare(right.name, undefined, { sensitivity: 'base' })
		);
	}

	function buildWorkloadUsers() {
		const grouped = new Map<string, WorkloadUser>();

		for (const member of onlineMembers) {
			const key = normalizeUserKey(member.id);
			if (grouped.has(key)) {
				continue;
			}
			grouped.set(key, {
				key,
				id: member.id,
				name: member.name.trim() || displayNameFromIdentifier(member.id),
				isOnline: Boolean(member.isOnline),
				taskCount: 0,
				totalEffortScore: 0,
				tasks: []
			});
		}

		for (const task of tasks) {
			const key = normalizeUserKey(task.assigneeId || '');
			const member = presenceByKey.get(key);
			const existing = grouped.get(key);
			const row: WorkloadUser =
				existing ?? {
					key,
					id: task.assigneeId || '',
					name: member?.name.trim() || displayNameFromIdentifier(task.assigneeId || ''),
					isOnline: Boolean(member?.isOnline),
					taskCount: 0,
					totalEffortScore: 0,
					tasks: []
				};

			row.taskCount += 1;
			row.totalEffortScore += parseEffortScore(task);
			row.tasks.push(task);
			grouped.set(key, row);
		}

		return [...grouped.values()].sort(workloadSort);
	}

	function workloadFillPercent(totalEffortScore: number) {
		if (totalEffortScore <= 0) {
			return 0;
		}
		return Math.min(100, (totalEffortScore / OVERLOAD_THRESHOLD) * 100);
	}

	function avatarLabel(name: string) {
		const words = name
			.trim()
			.split(/\s+/)
			.filter(Boolean)
			.slice(0, 2);
		if (words.length === 0) {
			return 'U';
		}
		return words.map((word) => word[0]?.toUpperCase() ?? '').join('');
	}

	function withSessionUserHeaders(headers: Record<string, string> = {}) {
		if (!sessionUserID) {
			if (!sessionUsername) {
				return headers;
			}
			return {
				...headers,
				'X-User-Name': sessionUsername
			};
		}
		return {
			...headers,
			'X-User-Id': sessionUserID,
			'X-User-Name': sessionUsername
		};
	}

	async function parseResponseError(response: Response) {
		const payload = (await response.json().catch(() => null)) as
			| {
					error?: string;
					message?: string;
			  }
			| null;
		return payload?.error?.trim() || payload?.message?.trim() || `HTTP ${response.status}`;
	}

	function setTaskSaving(taskID: string, nextState: boolean) {
		const normalized = taskID.trim();
		if (!normalized) {
			return;
		}
		const next = new Set(savingTaskIds);
		if (nextState) {
			next.add(normalized);
		} else {
			next.delete(normalized);
		}
		savingTaskIds = next;
	}

	function resolveLowerWorkloadCandidates(sourceUser: WorkloadUser) {
		const lower = workloadUsers.filter(
			(candidate) =>
				candidate.key !== sourceUser.key &&
				candidate.key !== 'unassigned' &&
				candidate.totalEffortScore < sourceUser.totalEffortScore
		);
		if (lower.length > 0) {
			return lower;
		}
		return workloadUsers.filter(
			(candidate) => candidate.key !== sourceUser.key && candidate.key !== 'unassigned'
		);
	}

	function toggleUserExpansion(userKey: string) {
		expandedUserKey = expandedUserKey === userKey ? '' : userKey;
		reassignError = '';
	}

	function openTaskEditor(taskID: string) {
		const normalized = taskID.trim();
		if (!normalized) {
			return;
		}
		dispatch('requestTaskEdit', { taskId: normalized });
	}

	async function persistTaskReassign(task: Task, nextAssigneeID: string) {
		const normalizedRoomID = normalizeRoomIDValue(task.roomId);
		if (!normalizedRoomID) {
			throw new Error('Task room id is missing.');
		}
		const response = await fetch(
			`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomID)}/tasks/${encodeURIComponent(task.id)}`,
			{
				method: 'PUT',
				headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
				credentials: 'include',
				body: JSON.stringify({ assignee_id: nextAssigneeID })
			}
		);
		if (!response.ok) {
			throw new Error(await parseResponseError(response));
		}
		const payload = await response.json().catch(() => null);
		const updatedTask = upsertTaskStoreEntry(payload, normalizedRoomID);
		if (!updatedTask) {
			throw new Error('Failed to parse updated task payload.');
		}
		sendSocketPayload(buildTaskSocketPayload('task_update', normalizedRoomID, updatedTask));
	}

	async function reassignTask(task: Task, sourceUser: WorkloadUser, targetKey: string) {
		const normalizedTargetKey = normalizeUserKey(targetKey);
		const currentOwnerKey = normalizeUserKey(task.assigneeId || '');
		if (!normalizedTargetKey || normalizedTargetKey === currentOwnerKey) {
			return;
		}

		const isLowerWorkloadTarget = workloadByKey.get(normalizedTargetKey)?.totalEffortScore ?? 0;
		if (normalizedTargetKey !== 'unassigned' && isLowerWorkloadTarget >= sourceUser.totalEffortScore) {
			reassignError = 'Pick a teammate with lower current workload for this quick reassign tool.';
			return;
		}

		const target = workloadByKey.get(normalizedTargetKey);
		const nextAssigneeID = normalizedTargetKey === 'unassigned' ? '' : target?.id?.trim() || '';
		setTaskSaving(task.id, true);
		reassignError = '';
		try {
			await persistTaskReassign(task, nextAssigneeID);
		} catch (error) {
			reassignError = error instanceof Error ? error.message : 'Failed to reassign task.';
		} finally {
			setTaskSaving(task.id, false);
		}
	}
</script>

<section class="people-panel" aria-label="People management">
	<header class="people-header">
		<h3>People Management</h3>
		<p>{workloadUsers.length} contributors · {overloadedUsers.length} overloaded</p>
	</header>

	{#if reassignError}
		<p class="people-error" role="status">{reassignError}</p>
	{/if}

	{#if workloadUsers.length === 0}
		<p class="people-empty">No assignees available yet.</p>
	{:else}
		<div class="people-list" role="list">
			{#each workloadUsers as user (user.key)}
				<article class="person-card" class:is-overloaded={user.totalEffortScore > OVERLOAD_THRESHOLD} role="listitem">
					<div class="person-head">
						<div class="person-identity">
							<div class="avatar">{avatarLabel(user.name)}</div>
							<div>
								<strong>{user.name}</strong>
								<div class="presence" class:is-online={user.isOnline}>
									<span class="presence-dot"></span>
									{user.isOnline ? 'Online' : 'Offline'}
								</div>
							</div>
						</div>
						<div class="person-metrics">
							<span>{user.taskCount} task{user.taskCount === 1 ? '' : 's'}</span>
							<span>{user.totalEffortScore.toFixed(1)} effort</span>
						</div>
					</div>

					<div class="workload-track" role="presentation">
						<div
							class="workload-fill"
							class:is-overloaded={user.totalEffortScore > OVERLOAD_THRESHOLD}
							style={`width:${workloadFillPercent(user.totalEffortScore)}%`}
						></div>
					</div>

					{#if user.totalEffortScore > OVERLOAD_THRESHOLD && user.tasks.length > 0}
						<button type="button" class="reassign-toggle" on:click={() => toggleUserExpansion(user.key)}>
							{expandedUserKey === user.key ? 'Hide Reassign Tools' : 'Reassign from overloaded user'}
						</button>

						{#if expandedUserKey === user.key}
							<div class="reassign-panel">
								{#each user.tasks as task (task.id)}
									<div class="reassign-row">
										<button
											type="button"
											class="task-open"
											on:click={() => openTaskEditor(task.id)}
										>
											{task.title}
										</button>
										<select
											value={normalizeUserKey(task.assigneeId || '')}
											on:change={(event) =>
												void reassignTask(
													task,
													user,
													(event.currentTarget as HTMLSelectElement).value
												)}
											disabled={!canEdit || savingTaskIds.has(task.id)}
										>
											<option value={user.key}>{user.name} (current)</option>
											{#each resolveLowerWorkloadCandidates(user) as candidate (candidate.key)}
												<option value={candidate.key}>
													{candidate.name} · {candidate.totalEffortScore.toFixed(1)} effort
												</option>
											{/each}
											<option value="unassigned">Unassigned</option>
										</select>
									</div>
								{/each}
							</div>
						{/if}
					{/if}
				</article>
			{/each}
		</div>
	{/if}
</section>

<style>
	.people-panel {
		height: 100%;
		min-height: 0;
		overflow: auto;
		display: grid;
		grid-template-rows: auto auto minmax(0, 1fr);
		gap: 0.7rem;
		padding-right: 0.2rem;
	}

	.people-header h3 {
		margin: 0;
		font-size: 0.9rem;
	}

	.people-header p {
		margin: 0.24rem 0 0;
		font-size: 0.74rem;
		color: var(--ws-muted);
	}

	.people-error {
		margin: 0;
		font-size: 0.72rem;
		color: var(--ws-danger);
	}

	.people-empty {
		margin: 0;
		font-size: 0.74rem;
		color: var(--ws-muted);
	}

	.people-list {
		display: grid;
		gap: 0.58rem;
	}

	.person-card {
		border: 1px solid color-mix(in srgb, var(--ws-border) 90%, transparent);
		border-radius: 12px;
		padding: 0.62rem;
		background: color-mix(in srgb, var(--ws-surface) 90%, var(--ws-surface-soft));
		display: grid;
		gap: 0.52rem;
	}

	.person-card.is-overloaded {
		border-color: color-mix(in srgb, #ef4444 55%, var(--ws-border));
	}

	.person-head {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.6rem;
	}

	.person-identity {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-width: 0;
	}

	.avatar {
		width: 1.9rem;
		height: 1.9rem;
		border-radius: 999px;
		display: grid;
		place-items: center;
		font-size: 0.72rem;
		font-weight: 700;
		background: color-mix(in srgb, var(--ws-accent) 24%, var(--ws-surface));
		color: var(--ws-text);
		flex-shrink: 0;
	}

	.person-identity strong {
		display: block;
		font-size: 0.76rem;
		line-height: 1.2;
	}

	.presence {
		display: inline-flex;
		align-items: center;
		gap: 0.28rem;
		font-size: 0.66rem;
		color: var(--ws-muted);
	}

	.presence-dot {
		width: 0.42rem;
		height: 0.42rem;
		border-radius: 999px;
		background: color-mix(in srgb, var(--ws-muted) 60%, transparent);
	}

	.presence.is-online {
		color: color-mix(in srgb, #22c55e 82%, var(--ws-text));
	}

	.presence.is-online .presence-dot {
		background: #22c55e;
	}

	.person-metrics {
		display: grid;
		justify-items: end;
		gap: 0.16rem;
		font-size: 0.68rem;
		color: var(--ws-muted);
	}

	.workload-track {
		height: 9px;
		border-radius: 999px;
		overflow: hidden;
		background: color-mix(in srgb, var(--ws-border) 82%, transparent);
	}

	.workload-fill {
		height: 100%;
		border-radius: inherit;
		background: linear-gradient(
			90deg,
			color-mix(in srgb, #22c55e 86%, #a3e635),
			color-mix(in srgb, #22c55e 64%, #16a34a)
		);
	}

	.workload-fill.is-overloaded {
		background: linear-gradient(
			90deg,
			color-mix(in srgb, #ef4444 88%, #f97316),
			color-mix(in srgb, #dc2626 82%, #ef4444)
		);
	}

	.reassign-toggle {
		justify-self: start;
		border: 1px solid color-mix(in srgb, var(--ws-border) 90%, transparent);
		background: color-mix(in srgb, var(--ws-surface) 96%, transparent);
		color: var(--ws-text);
		font-size: 0.7rem;
		font-weight: 600;
		border-radius: 8px;
		padding: 0.34rem 0.54rem;
		cursor: pointer;
	}

	.reassign-panel {
		display: grid;
		gap: 0.38rem;
		border-top: 1px dashed color-mix(in srgb, var(--ws-border) 88%, transparent);
		padding-top: 0.5rem;
	}

	.reassign-row {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(0, 180px);
		gap: 0.42rem;
		align-items: center;
	}

	.task-open {
		border: none;
		background: transparent;
		text-align: left;
		font-size: 0.71rem;
		color: var(--ws-text);
		cursor: pointer;
		padding: 0;
	}

	.task-open:hover {
		text-decoration: underline;
	}

	.reassign-row select {
		width: 100%;
		border: 1px solid color-mix(in srgb, var(--ws-border) 90%, transparent);
		background: var(--ws-surface);
		color: var(--ws-text);
		border-radius: 8px;
		padding: 0.32rem 0.4rem;
		font-size: 0.69rem;
	}

	.reassign-row select:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	@media (max-width: 760px) {
		.reassign-row {
			grid-template-columns: minmax(0, 1fr);
		}

		.person-head {
			align-items: flex-start;
			flex-direction: column;
		}

		.person-metrics {
			justify-items: start;
		}
	}
</style>
