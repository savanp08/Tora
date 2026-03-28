<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import type { OnlineMember } from '$lib/types/chat';
	import { resolveApiBase } from '$lib/config/apiBase';
	import { currentUser } from '$lib/store';
	import { taskStore, upsertTaskStoreEntry, type Task } from '$lib/stores/tasks';
	import { normalizeRoomIDValue } from '$lib/utils/chat/core';
	import { sendSocketPayload } from '$lib/ws';
	import { buildTaskSocketPayload } from '$lib/ws/client';

	export let onlineMembers: OnlineMember[] = [];
	export let canEdit = true;
	export let isAdmin = false;
	export let sessionUserID = '';
	export let sessionUserName = '';
	export let roomId = '';

	const dispatch = createEventDispatcher<{
		requestTaskEdit: { taskId: string };
	}>();

	const OVERLOAD_THRESHOLD = 20;
	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = resolveApiBase(API_BASE_RAW);

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
	let settingsPanelOpen = false;
	let adminPermissionRequired = false;
	// Per-member privilege toggles: memberId -> { aiAccept, fullEdit }
	type MemberPrivileges = { aiAccept: boolean; fullEdit: boolean };
	let memberPrivileges: Record<string, MemberPrivileges> = {};

	function getPrivileges(memberId: string): MemberPrivileges {
		return memberPrivileges[memberId] ?? { aiAccept: false, fullEdit: false };
	}
	function setPrivilege(memberId: string, key: keyof MemberPrivileges, value: boolean) {
		memberPrivileges = {
			...memberPrivileges,
			[memberId]: { ...getPrivileges(memberId), [key]: value }
		};
	}
	let inviteInput = '';
	let inviteEmails: string[] = [];
	let inviteSending = false;
	let inviteSuccess = '';
	let inviteError = '';

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
		const customFieldEffort = parseCustomFieldEffort(task.customFields);
		if (customFieldEffort > 0) {
			return customFieldEffort;
		}
		const entries = parseDescriptionMetadata(task.description || '');
		const effortRaw = entries.find((entry) => entry.key === 'effort')?.value ?? '';
		return parsePositiveNumber(effortRaw);
	}

	function parsePositiveNumber(value: unknown) {
		if (typeof value === 'number' && Number.isFinite(value) && value > 0) {
			return value;
		}
		if (typeof value === 'string') {
			const parsed = Number(value.replace(/[^\d.\-]/g, ''));
			if (Number.isFinite(parsed) && parsed > 0) {
				return parsed;
			}
		}
		return 0;
	}

	function parseCustomFieldEffort(fields: Task['customFields']) {
		if (!fields || typeof fields !== 'object') {
			return 0;
		}
		const record = fields as Record<string, unknown>;
		const effortFieldCandidates = [
			'effort',
			'effort_score',
			'effortScore',
			'story_points',
			'storyPoints',
			'estimate',
			'estimated_effort'
		];
		for (const key of effortFieldCandidates) {
			const parsed = parsePositiveNumber(record[key]);
			if (parsed > 0) {
				return parsed;
			}
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

	function taskEffortScore(task: Task) {
		return parseEffortScore(task);
	}

	function sortTasksByEffortDesc(taskList: Task[]) {
		return [...taskList].sort(
			(left, right) =>
				taskEffortScore(right) - taskEffortScore(left) ||
				right.updatedAt - left.updatedAt ||
				left.title.localeCompare(right.title, undefined, { sensitivity: 'base' })
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

	function normalizeEmail(raw: string) {
		return raw.trim().toLowerCase();
	}

	function isValidEmail(email: string) {
		return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email);
	}

	function addInviteEmail() {
		const emails = inviteInput.split(/[,;\s]+/).map(normalizeEmail).filter(Boolean);
		const toAdd = emails.filter((e) => isValidEmail(e) && !inviteEmails.includes(e));
		if (toAdd.length > 0) {
			inviteEmails = [...inviteEmails, ...toAdd];
		}
		inviteInput = '';
		inviteError = '';
	}

	function removeInviteEmail(email: string) {
		inviteEmails = inviteEmails.filter((e) => e !== email);
	}

	function handleInviteKeydown(event: KeyboardEvent) {
		if (event.key === 'Enter' || event.key === ',' || event.key === ' ') {
			event.preventDefault();
			addInviteEmail();
		}
	}

	async function sendInvites() {
		if (inviteEmails.length === 0) {
			inviteError = 'Add at least one email address.';
			return;
		}
		if (!roomId) {
			inviteError = 'No project room ID available.';
			return;
		}
		inviteSending = true;
		inviteError = '';
		inviteSuccess = '';
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(roomId)}/invite`,
				{
					method: 'POST',
					headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
					credentials: 'include',
					body: JSON.stringify({ emails: inviteEmails })
				}
			);
			if (!response.ok) {
				const msg = await parseResponseError(response);
				throw new Error(msg);
			}
			inviteSuccess = `Invites sent to ${inviteEmails.length} address${inviteEmails.length === 1 ? '' : 'es'}.`;
			inviteEmails = [];
		} catch (error) {
			inviteError = error instanceof Error ? error.message : 'Failed to send invites.';
		} finally {
			inviteSending = false;
		}
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
		<div>
			<h3>Team</h3>
			<p>{workloadUsers.length} contributors · {overloadedUsers.length} overloaded</p>
		</div>
		<button
			type="button"
			class="settings-toggle-btn"
			class:is-active={settingsPanelOpen}
			on:click={() => (settingsPanelOpen = !settingsPanelOpen)}
			title="Team settings"
			aria-label="Team settings"
			aria-expanded={settingsPanelOpen}
		>
			<svg viewBox="0 0 24 24" aria-hidden="true"
				><path
					d="M9.8 8.2 8.4 5.9l1.4-1.4 2.3 1.4a5.7 5.7 0 0 1 1.8 0l2.3-1.4 1.4 1.4-1.4 2.3c.2.6.3 1.2.3 1.8s-.1 1.2-.3 1.8l1.4 2.3-1.4 1.4-2.3-1.4a5.7 5.7 0 0 1-1.8 0l-2.3 1.4-1.4-1.4 1.4-2.3a5.7 5.7 0 0 1 0-3.6ZM12 14.2a2.2 2.2 0 1 0 0-4.4 2.2 2.2 0 0 0 0 4.4Z"
				></path></svg
			>
		</button>
	</header>

	{#if settingsPanelOpen}
		<div class="settings-panel">
			<div class="settings-panel-title">Team Settings</div>
			<label class="settings-toggle-row">
				<div class="settings-toggle-info">
					<strong>Admin permission for transfers</strong>
					<small>When enabled, only admins can reassign tasks between team members.</small>
				</div>
				<div
					class="toggle-switch"
					class:is-on={adminPermissionRequired}
					role="switch"
					aria-checked={adminPermissionRequired}
					tabindex="0"
					on:click={() => {
						if (canEdit) adminPermissionRequired = !adminPermissionRequired;
					}}
					on:keydown={(e) => {
						if ((e.key === ' ' || e.key === 'Enter') && canEdit) {
							adminPermissionRequired = !adminPermissionRequired;
						}
					}}
					title={canEdit
						? adminPermissionRequired
							? 'Disable admin requirement'
							: 'Enable admin requirement'
						: 'Only admins can change this setting'}
				>
					<span class="toggle-knob"></span>
				</div>
			</label>
			{#if !canEdit && adminPermissionRequired}
				<p class="settings-notice">Task transfers require admin permission on this project.</p>
			{/if}
		</div>
	{/if}

	<!-- ── Email invite section ────────────────────────────── -->
	{#if isAdmin}
	<div class="invite-section">
		<div class="invite-header">
			<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"></path><polyline points="22,6 12,13 2,6"></polyline></svg>
			<h4>Invite to Project</h4>
		</div>

		<div class="invite-input-row">
			<input
				type="email"
				multiple
				class="invite-input"
				bind:value={inviteInput}
				on:keydown={handleInviteKeydown}
				on:blur={addInviteEmail}
				placeholder="email@example.com"
				aria-label="Enter email to invite"
				disabled={!canEdit}
			/>
			<button
				type="button"
				class="invite-add-btn"
				on:click={addInviteEmail}
				disabled={!canEdit || !inviteInput.trim()}
			>
				Add
			</button>
		</div>

		{#if inviteEmails.length > 0}
			<div class="invite-chips" role="list" aria-label="Emails to invite">
				{#each inviteEmails as email (email)}
					<div class="invite-chip" role="listitem">
						<span>{email}</span>
						<button
							type="button"
							class="chip-remove"
							on:click={() => removeInviteEmail(email)}
							aria-label="Remove {email}"
						>
							<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12"></path></svg>
						</button>
					</div>
				{/each}
			</div>
			<button
				type="button"
				class="invite-send-btn"
				on:click={() => void sendInvites()}
				disabled={inviteSending || !canEdit}
			>
				{#if inviteSending}
					Sending…
				{:else}
					Send {inviteEmails.length} Invite{inviteEmails.length === 1 ? '' : 's'}
				{/if}
			</button>
		{/if}

		{#if inviteSuccess}
			<p class="invite-success" role="status">{inviteSuccess}</p>
		{/if}
		{#if inviteError}
			<p class="invite-error" role="alert">{inviteError}</p>
		{/if}

		{#if !canEdit}
			<p class="invite-locked">Only admins can send invites.</p>
		{/if}
	</div>
	{/if}

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
						{@const transferAllowed = canEdit || !adminPermissionRequired}
						<button
							type="button"
							class="reassign-toggle"
							class:is-locked={!transferAllowed}
							disabled={!transferAllowed}
							title={!transferAllowed
								? 'Admin permission required to transfer tasks'
								: undefined}
							on:click={() => toggleUserExpansion(user.key)}
						>
							{#if !transferAllowed}
								<svg viewBox="0 0 24 24" aria-hidden="true" style="width:0.75rem;height:0.75rem;stroke:currentColor;fill:none;stroke-width:2;stroke-linecap:round"><path d="M19 11H5M12 18V12M17 7V5a2 2 0 0 0-4 0v2M7 11V7a5 5 0 0 1 10 0v4"></path></svg>
								Transfer requires admin
							{:else}
								{expandedUserKey === user.key ? 'Hide Reassign Tools' : 'Reassign from overloaded user'}
							{/if}
						</button>

						{#if expandedUserKey === user.key}
							{@const lowerWorkloadCandidates = resolveLowerWorkloadCandidates(user)}
							<div class="reassign-panel">
								{#each sortTasksByEffortDesc(user.tasks) as task (task.id)}
									<div class="reassign-row">
										<button
											type="button"
											class="task-open"
											on:click={() => openTaskEditor(task.id)}
										>
											{task.title}
											<small>{taskEffortScore(task).toFixed(1)} effort</small>
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
											{#each lowerWorkloadCandidates as candidate (candidate.key)}
												<option value={candidate.key}>
													{candidate.name} · {candidate.totalEffortScore.toFixed(1)} effort
												</option>
											{/each}
											{#if lowerWorkloadCandidates.length === 0}
												<option value={user.key} disabled>No lower-workload teammates available</option>
											{/if}
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
		grid-template-rows: auto;
		align-content: start;
		gap: 0.7rem;
		padding-right: 0.2rem;
	}

	.people-header {
		display: flex;
		justify-content: space-between;
		align-items: flex-start;
		gap: 0.5rem;
	}

	.people-header h3 {
		margin: 0;
		font-size: 0.9rem;
	}

	.people-header p {
		margin: 0.22rem 0 0;
		font-size: 0.74rem;
		color: var(--ws-muted);
	}

	.settings-toggle-btn {
		width: 2rem;
		height: 2rem;
		border-radius: 9px;
		border: 1px solid var(--ws-border);
		background: var(--ws-surface);
		color: var(--ws-muted);
		display: grid;
		place-items: center;
		cursor: pointer;
		flex-shrink: 0;
		transition:
			background 0.15s ease,
			color 0.15s ease,
			border-color 0.15s ease;
	}

	.settings-toggle-btn svg {
		width: 0.88rem;
		height: 0.88rem;
		stroke: currentColor;
		fill: none;
		stroke-width: 1.8;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.settings-toggle-btn:hover {
		color: var(--ws-text);
		border-color: color-mix(in srgb, var(--ws-accent) 40%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 55%, var(--ws-surface));
	}

	.settings-toggle-btn.is-active {
		color: var(--ws-accent);
		border-color: color-mix(in srgb, var(--ws-accent) 60%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 75%, var(--ws-surface));
	}

	/* Settings panel */
	.settings-panel {
		border: 1px solid color-mix(in srgb, var(--ws-border) 90%, transparent);
		border-radius: 11px;
		padding: 0.62rem;
		background: color-mix(in srgb, var(--ws-surface) 88%, var(--ws-surface-soft));
		display: grid;
		gap: 0.48rem;
	}

	.settings-panel-title {
		font-size: 0.72rem;
		font-weight: 700;
		color: var(--ws-muted);
		text-transform: uppercase;
		letter-spacing: 0.06em;
	}

	.settings-toggle-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.6rem;
		cursor: pointer;
	}

	.settings-toggle-info strong {
		display: block;
		font-size: 0.76rem;
	}

	.settings-toggle-info small {
		font-size: 0.66rem;
		color: var(--ws-muted);
		line-height: 1.4;
	}

	/* Toggle switch */
	.toggle-switch {
		width: 2.2rem;
		height: 1.25rem;
		border-radius: 999px;
		background: color-mix(in srgb, var(--ws-border) 100%, transparent);
		position: relative;
		cursor: pointer;
		flex-shrink: 0;
		transition: background 0.2s ease;
		outline: none;
	}

	.toggle-switch:focus-visible {
		box-shadow: 0 0 0 2px var(--ws-accent);
	}

	.toggle-switch.is-on {
		background: var(--ws-accent);
	}

	.toggle-knob {
		position: absolute;
		top: 0.15rem;
		left: 0.15rem;
		width: 0.95rem;
		height: 0.95rem;
		border-radius: 999px;
		background: #fff;
		box-shadow: 0 1px 3px rgba(0, 0, 0, 0.2);
		transition: transform 0.2s ease;
	}

	.toggle-switch.is-on .toggle-knob {
		transform: translateX(0.95rem);
	}

	.settings-notice {
		margin: 0;
		font-size: 0.69rem;
		color: var(--ws-danger);
		line-height: 1.4;
	}

	.member-priv-row {
		display: flex;
		align-items: center;
		gap: 0.6rem;
		padding: 0.35rem 0;
		border-top: 1px solid color-mix(in srgb, var(--ws-border, #3a3a52) 50%, transparent);
		flex-wrap: wrap;
	}

	.member-priv-name {
		font-size: 0.72rem;
		font-weight: 600;
		color: var(--ws-text, #e2e2f0);
		flex: 1;
		min-width: 80px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.priv-toggle-label {
		display: flex;
		align-items: center;
		gap: 0.3rem;
		font-size: 0.66rem;
		color: var(--ws-muted, #8888a8);
		cursor: pointer;
		user-select: none;
	}

	.toggle-sm {
		width: 28px;
		height: 16px;
	}

	.toggle-sm .toggle-knob {
		width: 12px;
		height: 12px;
		top: 2px;
		left: 2px;
	}

	.toggle-sm.is-on .toggle-knob {
		transform: translateX(12px);
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
		display: inline-flex;
		align-items: center;
		gap: 0.28rem;
		border: 1px solid color-mix(in srgb, var(--ws-border) 90%, transparent);
		background: color-mix(in srgb, var(--ws-surface) 96%, transparent);
		color: var(--ws-text);
		font-size: 0.7rem;
		font-weight: 600;
		border-radius: 8px;
		padding: 0.34rem 0.54rem;
		cursor: pointer;
	}

	.reassign-toggle.is-locked {
		opacity: 0.65;
		cursor: not-allowed;
		color: var(--ws-muted);
	}

	.reassign-toggle:disabled {
		cursor: not-allowed;
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
		display: grid;
		gap: 0.16rem;
	}

	.task-open:hover {
		text-decoration: underline;
	}

	.task-open small {
		font-size: 0.64rem;
		color: var(--ws-muted);
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

	/* ── Email invite ────────────────────────────────────── */
	.invite-section {
		border: 1px solid color-mix(in srgb, var(--ws-border) 90%, transparent);
		border-radius: 12px;
		padding: 0.65rem 0.68rem;
		background: color-mix(in srgb, var(--ws-surface) 88%, var(--ws-surface-soft));
		display: grid;
		gap: 0.48rem;
	}

	.invite-header {
		display: flex;
		align-items: center;
		gap: 0.4rem;
	}

	.invite-header svg {
		width: 0.88rem;
		height: 0.88rem;
		stroke: var(--ws-muted);
		fill: none;
		stroke-width: 1.8;
		stroke-linecap: round;
		stroke-linejoin: round;
		flex-shrink: 0;
	}

	.invite-header h4 {
		margin: 0;
		font-size: 0.78rem;
		font-weight: 700;
	}

	.invite-input-row {
		display: flex;
		gap: 0.38rem;
	}

	.invite-input {
		flex: 1;
		min-width: 0;
		border: 1px solid var(--ws-border);
		border-radius: 8px;
		padding: 0.38rem 0.52rem;
		background: var(--ws-surface);
		color: var(--ws-text);
		font-size: 0.76rem;
		outline: none;
		transition: border-color 0.15s ease;
	}

	.invite-input:focus {
		border-color: color-mix(in srgb, var(--ws-accent) 60%, var(--ws-border));
	}

	.invite-input:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.invite-add-btn {
		padding: 0.38rem 0.7rem;
		border: 1px solid var(--ws-border);
		border-radius: 8px;
		background: var(--ws-surface);
		color: var(--ws-muted);
		font-size: 0.74rem;
		font-weight: 600;
		cursor: pointer;
		white-space: nowrap;
		transition:
			background 0.15s ease,
			color 0.15s ease,
			border-color 0.15s ease;
	}

	.invite-add-btn:hover:not(:disabled) {
		color: var(--ws-accent);
		border-color: color-mix(in srgb, var(--ws-accent) 50%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 65%, var(--ws-surface));
	}

	.invite-add-btn:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}

	.invite-chips {
		display: flex;
		flex-wrap: wrap;
		gap: 0.3rem;
	}

	.invite-chip {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
		padding: 0.22rem 0.4rem 0.22rem 0.52rem;
		border: 1px solid color-mix(in srgb, var(--ws-accent) 35%, var(--ws-border));
		border-radius: 999px;
		background: color-mix(in srgb, var(--ws-accent-soft) 60%, var(--ws-surface));
		font-size: 0.72rem;
		color: var(--ws-text);
	}

	.chip-remove {
		width: 1rem;
		height: 1rem;
		border-radius: 999px;
		border: none;
		background: color-mix(in srgb, var(--ws-border) 60%, transparent);
		color: var(--ws-muted);
		display: grid;
		place-items: center;
		cursor: pointer;
		flex-shrink: 0;
		padding: 0;
		transition: background 0.12s ease;
	}

	.chip-remove:hover {
		background: color-mix(in srgb, var(--ws-danger-soft) 80%, transparent);
		color: var(--ws-danger);
	}

	.chip-remove svg {
		width: 0.5rem;
		height: 0.5rem;
		stroke: currentColor;
		fill: none;
		stroke-width: 2.5;
		stroke-linecap: round;
	}

	.invite-send-btn {
		width: 100%;
		padding: 0.44rem 0.72rem;
		border: 1px solid color-mix(in srgb, var(--ws-accent) 55%, var(--ws-border));
		border-radius: 9px;
		background: color-mix(in srgb, var(--ws-accent-soft) 80%, var(--ws-surface));
		color: var(--ws-accent);
		font-size: 0.76rem;
		font-weight: 700;
		cursor: pointer;
		transition:
			background 0.15s ease,
			border-color 0.15s ease;
	}

	.invite-send-btn:hover:not(:disabled) {
		background: color-mix(in srgb, var(--ws-accent-soft) 100%, var(--ws-surface));
		border-color: color-mix(in srgb, var(--ws-accent) 75%, var(--ws-border));
	}

	.invite-send-btn:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.invite-success {
		margin: 0;
		font-size: 0.7rem;
		color: #16a34a;
	}

	.invite-error {
		margin: 0;
		font-size: 0.7rem;
		color: var(--ws-danger);
	}

	.invite-locked {
		margin: 0;
		font-size: 0.7rem;
		color: var(--ws-muted);
		font-style: italic;
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
