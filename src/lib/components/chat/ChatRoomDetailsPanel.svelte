<script lang="ts">
	import { activeRoomPassword } from '$lib/store';
	import type { OnlineMember } from '$lib/types/chat';
	import { normalizeIdentifier } from '$lib/utils/chat/core';
	import { createEventDispatcher, onDestroy } from 'svelte';
	import { canvasPermissionStore } from '$lib/stores/canvasPermissions';

	export let show = false;
	export let isMobileView = false;
	export let roomId = '';
	export let roomName = 'Room';
	export let roomAdminCode = '';
	export let createdLabel = 'Unknown';
	export let expiresLabel = 'Unknown';
	export let isExtendingRoom = false;
	export let currentOnlineMembers: OnlineMember[] = [];
	export let isActiveRoomAdmin = false;
	export let currentUserId = '';
	export let formatDateTime: (timestamp: number) => string = (timestamp) =>
		new Date(timestamp).toLocaleString();
	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';

	const dispatch = createEventDispatcher<{
		close: void;
		extend: void;
		removeMember: { memberId: string };
		promoted: { token?: string; adminCode?: string };
	}>();

	let copied = false;
	let copiedTimer: ReturnType<typeof setTimeout> | null = null;
	let adminCodeCopied = false;
	let adminCodeCopyTimer: ReturnType<typeof setTimeout> | null = null;
	let promotionCode = '';
	let isPromoting = false;
	let promotionError = '';
	let promotionSuccess = '';
	$: visibleAdminCode = (roomAdminCode || '').trim().toUpperCase().slice(0, 4);
	onDestroy(() => {
		if (copiedTimer) {
			clearTimeout(copiedTimer);
		}
		if (adminCodeCopyTimer) {
			clearTimeout(adminCodeCopyTimer);
		}
	});

	function resetCopiedStateSoon() {
		if (copiedTimer) {
			clearTimeout(copiedTimer);
		}
		copiedTimer = setTimeout(() => {
			copied = false;
		}, 2000);
	}

	function copyInviteLink() {
		if (typeof window === 'undefined' || !roomId) {
			return;
		}
		const normalizedPassword = ($activeRoomPassword || '').trim().slice(0, 32);
		const inviteHash = normalizedPassword ? `#key=${encodeURIComponent(normalizedPassword)}` : '';
		const inviteUrl = `${window.location.origin}/chat/${encodeURIComponent(roomId)}${inviteHash}`;

		const fallbackCopy = () => {
			const textarea = document.createElement('textarea');
			textarea.value = inviteUrl;
			textarea.setAttribute('readonly', 'true');
			textarea.style.position = 'fixed';
			textarea.style.opacity = '0';
			textarea.style.pointerEvents = 'none';
			document.body.appendChild(textarea);
			textarea.select();
			document.execCommand('copy');
			document.body.removeChild(textarea);
		};

		if (!navigator.clipboard?.writeText) {
			fallbackCopy();
			copied = true;
			resetCopiedStateSoon();
			return;
		}

		navigator.clipboard
			.writeText(inviteUrl)
			.then(() => {
				copied = true;
				resetCopiedStateSoon();
			})
			.catch(() => {
				fallbackCopy();
				copied = true;
				resetCopiedStateSoon();
			});
	}

	function resetAdminCodeCopiedStateSoon() {
		if (adminCodeCopyTimer) {
			clearTimeout(adminCodeCopyTimer);
		}
		adminCodeCopyTimer = setTimeout(() => {
			adminCodeCopied = false;
		}, 2000);
	}

	function copyAdminCode() {
		if (!visibleAdminCode) {
			return;
		}
		const onSuccess = () => {
			adminCodeCopied = true;
			resetAdminCodeCopiedStateSoon();
		};
		const fallbackCopy = () => {
			const textarea = document.createElement('textarea');
			textarea.value = visibleAdminCode;
			textarea.setAttribute('readonly', 'true');
			textarea.style.position = 'fixed';
			textarea.style.opacity = '0';
			textarea.style.pointerEvents = 'none';
			document.body.appendChild(textarea);
			textarea.select();
			document.execCommand('copy');
			document.body.removeChild(textarea);
			onSuccess();
		};
		if (!navigator.clipboard?.writeText) {
			fallbackCopy();
			return;
		}
		navigator.clipboard.writeText(visibleAdminCode).then(onSuccess).catch(fallbackCopy);
	}

	function onPromotionInput(event: Event) {
		const target = event.currentTarget as HTMLInputElement | null;
		const next = (target?.value || '').toUpperCase().replace(/[^A-Z0-9]/g, '');
		promotionCode = next.slice(0, 4);
		promotionError = '';
		promotionSuccess = '';
	}

	async function promoteToAdmin() {
		if (!roomId || isPromoting || isActiveRoomAdmin) {
			return;
		}
		const code = promotionCode.trim().toUpperCase();
		if (code.length !== 4) {
			promotionError = 'Enter the 4-character admin code.';
			return;
		}
		const normalizedUserId = normalizeIdentifier(currentUserId);
		if (!normalizedUserId) {
			promotionError = 'Unable to identify your account. Rejoin the room and retry.';
			return;
		}

		isPromoting = true;
		promotionError = '';
		promotionSuccess = '';
		try {
			const res = await fetch(`${API_BASE}/api/rooms/${encodeURIComponent(roomId)}/promote`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					code,
					userId: normalizedUserId
				})
			});
			const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
			if (!res.ok) {
				promotionError =
					(typeof data.error === 'string' && data.error.trim()) || 'Admin promotion failed.';
				return;
			}
			const token = typeof data.token === 'string' ? data.token.trim() : '';
			const adminCode =
				typeof data.adminCode === 'string'
					? data.adminCode.trim().toUpperCase().slice(0, 4)
					: visibleAdminCode;
			promotionSuccess = 'Admin access granted.';
			dispatch('promoted', {
				token,
				adminCode
			});
		} catch {
			promotionError = 'Network error while promoting to admin.';
		} finally {
			isPromoting = false;
		}
	}

	function memberHasAdminPrivilege(member: OnlineMember) {
		if (member.isAdmin) {
			return true;
		}
		return (
			isActiveRoomAdmin &&
			normalizeIdentifier(member.id) !== '' &&
			normalizeIdentifier(member.id) === normalizeIdentifier(currentUserId)
		);
	}

	function memberHasCanvasEdit(member: OnlineMember): boolean {
		return canvasPermissionStore.hasEdit(roomId, normalizeIdentifier(member.id));
	}

	function toggleCanvasEdit(member: OnlineMember) {
		if (!isActiveRoomAdmin || !roomId) return;
		canvasPermissionStore.toggle(roomId, normalizeIdentifier(member.id));
		// Force reactivity
		canvasEditorIds = canvasPermissionStore.getEditors(roomId);
	}

	let canvasEditorIds: string[] = [];
	$: if (roomId) canvasEditorIds = canvasPermissionStore.getEditors(roomId);

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
			<h3>
				{roomName}
				{#if isActiveRoomAdmin}
					<span class="role-indicator" aria-label="Admin privilege">Admin</span>
				{/if}
			</h3>
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

			<div class="admin-access-card">
				<h4>Admin Access</h4>
				{#if isActiveRoomAdmin}
					<div class="admin-code-row">
						<span>Admin Code</span>
						<strong>{visibleAdminCode || '----'}</strong>
						<button type="button" on:click={copyAdminCode}>
							{adminCodeCopied ? 'Copied!' : 'Copy'}
						</button>
					</div>
				{:else}
					<div class="admin-promote-row">
						<input
							type="text"
							maxlength="4"
							placeholder="4-Char Code"
							value={promotionCode}
							on:input={onPromotionInput}
							style="text-transform: uppercase;"
						/>
						<button type="button" on:click={promoteToAdmin} disabled={isPromoting}>
							{isPromoting ? 'Promoting...' : 'Promote Me'}
						</button>
					</div>
					{#if promotionError}
						<p class="admin-promote-feedback error">{promotionError}</p>
					{:else if promotionSuccess}
						<p class="admin-promote-feedback success">{promotionSuccess}</p>
					{/if}
				{/if}
			</div>

			<div class="room-actions">
				<button
					type="button"
					class="copy-invite-button {copied ? 'copied' : ''}"
					on:click={copyInviteLink}
				>
					{copied ? 'Copied!' : 'Copy Invite Link'}
				</button>
				<button
					type="button"
					class="extend-room-button"
					on:click={() => dispatch('extend')}
					disabled={isExtendingRoom}
				>
					{isExtendingRoom ? 'Extending...' : 'Extend Room (24h)'}
				</button>
				<p>Manually extends this room and its messages for 24 hours (Max 15 days from creation)</p>
			</div>

			<h4 class="members-title">Members</h4>
			{#if currentOnlineMembers.length === 0}
				<div class="empty-label">No online members.</div>
			{:else}
				{#each currentOnlineMembers as member (member.id)}
					<div class="online-member">
						<span class="member-dot"></span>
						<div>
							<div class="member-name">
								<span>{member.name}</span>
								{#if memberHasAdminPrivilege(member)}
									<span class="member-role-badge" aria-label="Admin">Admin</span>
								{/if}
							</div>
							<div class="member-meta">Joined {formatDateTime(member.joinedAt)}</div>
						</div>
						{#if isActiveRoomAdmin && normalizeIdentifier(member.id) !== normalizeIdentifier(currentUserId)}
							<div class="member-admin-actions">
								<button
									type="button"
									class="member-canvas-btn {memberHasCanvasEdit(member) ? 'canvas-granted' : ''}"
									title={memberHasCanvasEdit(member) ? 'Revoke canvas edit access' : 'Grant canvas edit access'}
									on:click={() => { toggleCanvasEdit(member); canvasEditorIds = canvasPermissionStore.getEditors(roomId); }}
								>
									<svg viewBox="0 0 24 24" aria-hidden="true">
										<path d="M12 20h9M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5Z"/>
									</svg>
									{memberHasCanvasEdit(member) ? 'Canvas ✓' : 'Canvas'}
								</button>
								<button
									type="button"
									class="member-remove-button"
									on:click={() => dispatch('removeMember', { memberId: member.id })}
								>
									Remove
								</button>
							</div>
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
		background: #f1f5fa;
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
		border: 1px solid #c8d1de;
		box-shadow: 0 18px 42px rgba(15, 23, 42, 0.22);
	}

	.mobile-info-panel header {
		padding: 0.9rem 1rem;
		border-bottom: 1px solid #d2d9e4;
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.mobile-info-panel header h3 {
		margin: 0;
		font-size: 1rem;
		display: inline-flex;
		align-items: center;
		gap: 0.38rem;
		color: #1f2937;
	}

	.role-indicator {
		font-size: 0.64rem;
		font-weight: 600;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: #64748b;
		border: 1px solid #cbd5e1;
		border-radius: 999px;
		padding: 0.08rem 0.36rem;
		line-height: 1.1;
	}

	.mobile-info-panel header button {
		border: 1px solid #c4cdd9;
		background: #edf2f8;
		border-radius: 7px;
		padding: 0.32rem 0.5rem;
		cursor: pointer;
		color: #324158;
	}

	.mobile-info-content {
		padding: 0.7rem 0.85rem;
		overflow: auto;
	}

	.room-actions {
		margin-bottom: 0.9rem;
		padding: 0.75rem;
		border: 1px solid #c8d1de;
		border-radius: 10px;
		background: #e9eef6;
	}

	.room-details-card {
		margin-bottom: 0.9rem;
		padding: 0.75rem;
		border: 1px solid #c8d1de;
		border-radius: 10px;
		background: #e9eef6;
	}

	.admin-access-card {
		margin-bottom: 0.9rem;
		padding: 0.75rem;
		border: 1px solid #c8d1de;
		border-radius: 10px;
		background: #e9eef6;
	}

	.admin-access-card h4 {
		margin: 0 0 0.45rem;
		font-size: 0.86rem;
		color: #2d3d54;
	}

	.admin-code-row {
		display: flex;
		align-items: center;
		gap: 0.45rem;
		font-size: 0.78rem;
		color: #5e6d83;
	}

	.admin-code-row strong {
		font-size: 0.84rem;
		letter-spacing: 0.08em;
		color: #334155;
	}

	.admin-code-row button {
		margin-left: auto;
		border: 1px solid #c4cdd9;
		background: #f5f8fc;
		color: #324158;
		border-radius: 7px;
		padding: 0.22rem 0.46rem;
		font-size: 0.72rem;
		cursor: pointer;
	}

	.admin-promote-row {
		display: flex;
		align-items: center;
		gap: 0.45rem;
	}

	.admin-promote-row input {
		flex: 1;
		min-width: 0;
		border: 1px solid #c4cdd9;
		border-radius: 7px;
		padding: 0.34rem 0.46rem;
		font-size: 0.8rem;
		letter-spacing: 0.08em;
		background: #f8fafc;
		color: #334155;
	}

	.admin-promote-row button {
		border: 1px solid #4c5e7b;
		background: #4c5e7b;
		color: #ffffff;
		border-radius: 7px;
		padding: 0.34rem 0.52rem;
		font-size: 0.74rem;
		font-weight: 600;
		cursor: pointer;
		white-space: nowrap;
	}

	.admin-promote-row button:disabled {
		opacity: 0.7;
		cursor: not-allowed;
	}

	.admin-promote-feedback {
		margin: 0.4rem 0 0;
		font-size: 0.73rem;
	}

	.admin-promote-feedback.error {
		color: #b91c1c;
	}

	.admin-promote-feedback.success {
		color: #15803d;
	}

	.room-details-card h4 {
		margin: 0 0 0.5rem;
		font-size: 0.88rem;
		color: #2d3d54;
	}

	.room-detail-row {
		display: flex;
		justify-content: space-between;
		align-items: baseline;
		gap: 0.65rem;
		font-size: 0.8rem;
		color: #5e6d83;
	}

	.room-detail-row + .room-detail-row {
		margin-top: 0.35rem;
	}

	.room-detail-row strong {
		color: #2f3e55;
		font-weight: 600;
	}

	.members-title {
		margin: 0 0 0.35rem;
		font-size: 0.88rem;
		color: #2f3e55;
	}

	.room-actions p {
		margin: 0.45rem 0 0;
		font-size: 0.78rem;
		color: #5f6e84;
	}

	.extend-room-button {
		width: 100%;
		border: 1px solid #4c5e7b;
		background: #4c5e7b;
		color: #ffffff;
		border-radius: 8px;
		padding: 0.48rem 0.65rem;
		font-size: 0.84rem;
		font-weight: 600;
		cursor: pointer;
	}

	.copy-invite-button {
		width: 100%;
		border: 1px solid #c4cdd9;
		background: #f5f8fc;
		color: #324158;
		border-radius: 8px;
		padding: 0.48rem 0.65rem;
		font-size: 0.84rem;
		font-weight: 600;
		cursor: pointer;
		margin-bottom: 0.5rem;
	}

	.copy-invite-button.copied {
		background: #22c55e;
		border-color: #22c55e;
		color: #ffffff;
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
		color: #2c3b50;
		display: inline-flex;
		align-items: center;
		gap: 0.34rem;
	}

	.member-role-badge {
		font-size: 0.62rem;
		font-weight: 600;
		letter-spacing: 0.03em;
		text-transform: uppercase;
		color: #64748b;
		border: 1px solid #cbd5e1;
		border-radius: 999px;
		padding: 0.08rem 0.34rem;
		line-height: 1.1;
	}

	.member-meta {
		font-size: 0.75rem;
		color: #647389;
	}

	.member-remove-button {
		margin-left: auto;
		border: 1px solid #c6cfdb;
		background: #f5f8fc;
		color: #36455d;
		border-radius: 8px;
		padding: 0.22rem 0.5rem;
		font-size: 0.72rem;
		cursor: pointer;
	}

	.member-remove-button:hover {
		background: #e6edf6;
	}

	.empty-label {
		color: #607087;
		font-size: 0.84rem;
		padding: 1rem;
	}

	:global(.chat-shell.theme-dark) .room-details-panel {
		background: #101826;
		border-color: #2b3a51;
		color: #dbe7fb;
	}

	:global(.chat-shell.theme-dark) .room-details-panel header {
		border-bottom-color: #2f415d;
	}

	:global(.chat-shell.theme-dark) .room-details-panel header h3 {
		color: #f1f5ff;
	}
</style>
