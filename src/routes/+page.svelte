<script lang="ts">
	import { goto } from '$app/navigation';
	import ExpiryClockPicker from '$lib/components/home/ExpiryClockPicker.svelte';
	import LoginFooter from '$lib/components/home/LoginFooter.svelte';
	import OtpCodeInput from '$lib/components/home/OtpCodeInput.svelte';
	import { activeRoomPassword, authToken, currentUser } from '$lib/store';
	import { getOrInitIdentity, updateUsername } from '$lib/utils/identity';
	import {
		normalizeRoomCodeInput,
		normalizeRoomIdValue,
		normalizeRoomNameInput,
		normalizeUsernameInput,
		type JoinMode
	} from '$lib/utils/homeJoin';
	import { generateRoomName } from '$lib/utils/nameGenerator';
	import { setSessionToken } from '$lib/utils/sessionToken';
	import { onMount } from 'svelte';
	const API_BASE = (import.meta.env.VITE_API_BASE as string | undefined) ?? 'http://localhost:8080';
	const CLIENT_LOG_PREFIX = '[home-client]';

	let roomName = '';
	let roomCode = '';
	let guestUsername = '';
	let roomPassword = '';
	let roomDurationHours = 24;
	let activeActionMode: JoinMode | '' = '';
	let isJoining = false;
	let joinError = '';

	function clientLog(event: string, payload?: unknown) {
		const timestamp = new Date().toISOString();
		if (payload === undefined) {
			console.log(`${CLIENT_LOG_PREFIX} ${timestamp} ${event}`);
			return;
		}
		console.log(`${CLIENT_LOG_PREFIX} ${timestamp} ${event}`, payload);
	}

	onMount(() => {
		roomName = generateRoomName();
		const identity = getOrInitIdentity();
		currentUser.set({ id: identity.id, username: identity.username });
	});

	function onRoomNameFocus(event: FocusEvent) {
		const input = event.currentTarget as HTMLInputElement | null;
		input?.select();
	}

	async function handleRoomAction(mode: JoinMode) {
		const normalizedRoomName = normalizeRoomNameInput(roomName);
		const normalizedRoomCode = normalizeRoomCodeInput(roomCode);
		if (mode === 'create' && !normalizedRoomName) {
			joinError = 'New rooms require a room name';
			return;
		}
		if (mode === 'join' && !normalizedRoomName && !normalizedRoomCode) {
			joinError = 'Enter a room name or a 6-digit room code';
			return;
		}

		isJoining = true;
		activeActionMode = mode;
		joinError = '';
		roomName = normalizedRoomName;
		roomCode = normalizedRoomCode;

		const identity = getOrInitIdentity();
		const requestedUsername = normalizeUsernameInput(guestUsername);
		const userIdentity = requestedUsername ? updateUsername(requestedUsername) : identity;
		const userToJoin = userIdentity.username;
		guestUsername = userToJoin;
		const normalizedRoomPassword = (roomPassword || '').trim().slice(0, 32);
		activeRoomPassword.set(normalizedRoomPassword);

		try {
			clientLog('api-rooms-join-request', {
				roomName: normalizedRoomName,
				roomCode: normalizedRoomCode,
				userToJoin,
				mode,
				roomDurationHours
			});
			const res = await fetch(`${API_BASE}/api/rooms/join`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					roomName: normalizedRoomName,
					roomCode: normalizedRoomCode,
					username: userToJoin,
					userId: userIdentity.id,
					type: 'ephemeral',
					mode,
					roomDurationHours
				})
			});

			const data = await res.json();
			clientLog('api-rooms-join-response', { status: res.status, ok: res.ok, data });

			if (!res.ok) throw new Error(data.error || 'Failed to join room');

			currentUser.set({ id: data.userId || userIdentity.id, username: userToJoin });
			authToken.set(data.token);
			setSessionToken(data.token || '');

			const resolvedRoomID = normalizeRoomIdValue(String(data.roomId || ''));
			if (!resolvedRoomID) {
				throw new Error('Server returned an invalid room id');
			}
			const resolvedRoomName = data.roomName || normalizedRoomName;
			const createdAt = Number(data.createdAt);
			const createdAtQuery =
				Number.isFinite(createdAt) && createdAt > 0 ? `&createdAt=${createdAt}` : '';
			const expiresAt = Number(data.expiresAt ?? data.expires_at);
			const expiresAtQuery =
				Number.isFinite(expiresAt) && expiresAt > 0 ? `&expiresAt=${expiresAt}` : '';
			const serverNow = Number(data.serverNow ?? data.server_now);
			const serverNowQuery =
				Number.isFinite(serverNow) && serverNow > 0 ? `&serverNow=${serverNow}` : '';
			clientLog('navigate-chat-room', { roomId: resolvedRoomID, roomName: resolvedRoomName });
			const roomPasswordHash = normalizedRoomPassword
				? `#key=${encodeURIComponent(normalizedRoomPassword)}`
				: '';
			goto(
				`/chat/${resolvedRoomID}?name=${encodeURIComponent(resolvedRoomName)}&member=1${createdAtQuery}${expiresAtQuery}${serverNowQuery}${roomPasswordHash}`
			);
		} catch (e: any) {
			clientLog('api-rooms-join-error', { error: e?.message ?? String(e) });
			joinError = e.message;
		} finally {
			isJoining = false;
			activeActionMode = '';
		}
	}

	$: canCreate = normalizeRoomNameInput(roomName) !== '';
	$: canJoinExisting =
		normalizeRoomNameInput(roomName) !== '' || normalizeRoomCodeInput(roomCode) !== '';
</script>

<div class="container">
	<header>
		<div class="logo">Ephemeral<b>Chat</b></div>
	</header>

	<main>
		<div class="hero-box">
			<h1>Disappearing chats. <br />Instant connections.</h1>
			<p>Create a room. Share the link. It vanishes when you leave.</p>

			{#if joinError}
				<div class="error-msg">{joinError}</div>
			{/if}

			<div class="join-form">
				<div class="room-inputs-row">
					<div class="field-group room-name-group">
						<label for="room-name-input">Room name</label>
						<input
							id="room-name-input"
							type="text"
							placeholder="e.g. Product Sprint"
							bind:value={roomName}
							on:focus={onRoomNameFocus}
						/>
						<small>Used as display name (max 20 chars).</small>
					</div>
					<div class="or-divider" aria-hidden="true">or</div>
					<div class="field-group room-code-group">
						<label for="room-code-digit-0">6-digit code</label>
						<OtpCodeInput idPrefix="room-code-digit" bind:value={roomCode} disabled={isJoining} />
						<small>For quick join when someone shares a code.</small>
					</div>
				</div>

				<div class="field-group">
					<ExpiryClockPicker bind:valueHours={roomDurationHours} disabled={isJoining} />
				</div>

				<div class="identity-inputs-row">
					<div class="field-group">
						<label for="username-input">Username (optional)</label>
						<input
							id="username-input"
							type="text"
							placeholder="e.g. dizzy_panda"
							bind:value={guestUsername}
						/>
					</div>

					<div class="field-group">
						<label for="room-password-input">Room Password (optional)</label>
						<input
							id="room-password-input"
							type="password"
							placeholder="Optional password"
							bind:value={roomPassword}
							maxlength="32"
							autocomplete="off"
						/>
						<small>
							Private, Secured
						</small>
					</div>
				</div>

				<div class="action-row">
					<button
						class="btn-primary-action"
						on:click={() => void handleRoomAction('create')}
						disabled={isJoining || !canCreate}
					>
						{isJoining && activeActionMode === 'create' ? 'Creating...' : 'New'}
					</button>
					<button
						class="btn-secondary-action"
						on:click={() => void handleRoomAction('join')}
						disabled={isJoining || !canJoinExisting}
					>
						{isJoining && activeActionMode === 'join' ? 'Joining...' : 'Existing'}
					</button>
				</div>
			</div>

			<p class="hint">No signup required for ephemeral rooms.</p>
		</div>
	</main>
	<LoginFooter />
</div>

<style>
	:global(body) {
		margin: 0;
		font-family: sans-serif;
		background: var(--bg-primary);
	}

	.container {
		
		margin: 0 auto;
		padding: 16px clamp(12px, 3vw, 24px) 20px;
		min-height: 100dvh;
		height: auto;
		display: flex;
		flex-direction: column;
		overflow-y: auto;
		overflow-x: hidden;
	}

	header {
		display: flex;
		justify-content: flex-start;
		align-items: center;
		margin-bottom: 28px;
	}

	.logo {
		font-size: 1.5rem;
		color: var(--text-primary);
	}

	main {
		flex: 1;
		display: flex;
		justify-content: center;
		align-items: center;
		padding: 6px 0 14px;
	}

	.hero-box {
		text-align: center;
		background: var(--surface-primary);
		padding: 40px;
		border-radius: 12px;
		box-shadow: var(--shadow-lg);
		width: 100%;
		max-width: 500px;
		border: 1px solid var(--border-subtle);
	}

	h1 {
		margin-top: 0;
		color: var(--text-primary);
	}
	p {
		color: var(--text-secondary);
		margin-bottom: 30px;
	}

	.join-form {
		display: flex;
		flex-direction: column;
		gap: 12px;
	}

	.room-inputs-row {
		display: flex;
		align-items: stretch;
		gap: 10px;
		flex-wrap: nowrap;
	}

	.identity-inputs-row {
		display: flex;
		align-items: flex-start;
		gap: 10px;
		flex-wrap: nowrap;
	}

	.identity-inputs-row .field-group {
		flex: 1 1 50%;
	}

	.field-group {
		display: flex;
		flex-direction: column;
		gap: 6px;
		flex: 1;
		text-align: left;
		min-width: 0;
	}

	.field-group label {
		font-size: 0.82rem;
		font-weight: 600;
		color: var(--text-secondary);
	}

	.field-group small {
		font-size: 0.75rem;
		color: var(--text-tertiary);
	}

	.or-divider {
		align-self: center;
		font-size: 0.85rem;
		font-weight: 700;
		color: var(--text-tertiary);
		padding: 0 2px;
	}
	.action-row {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 10px;
	}
	input {
		background: var(--surface-primary);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: 6px;
		padding: 10px;
		font-size: 0.95rem;
	}

	input:focus {
		outline: none;
		border-color: var(--border-focus);
		box-shadow: 0 0 0 3px var(--interactive-focus);
	}

	.btn-primary-action,
	.btn-secondary-action {
		padding: 10px;
		border-radius: 6px;
		font-size: 0.95rem;
		font-weight: bold;
		cursor: pointer;
		transition: background 0.2s;
		background: var(--surface-secondary);
		border: 1px solid var(--border-default);
		color: var(--text-primary);
	}

	.btn-primary-action:disabled,
	.btn-secondary-action:disabled {
		background: var(--surface-active);
		color: var(--text-tertiary);
		cursor: not-allowed;
	}
	.btn-primary-action:hover:not(:disabled),
	.btn-secondary-action:hover:not(:disabled) {
		background: var(--surface-hover);
	}

	.error-msg {
		color: var(--accent-danger);
		background: var(--state-danger-bg);
		border: 1px solid var(--state-danger-border);
		padding: 10px;
		border-radius: 4px;
		margin-bottom: 15px;
	}
	.hint {
		font-size: 0.8rem;
		color: var(--text-tertiary);
		margin-top: 20px;
	}

	@media (max-width: 760px) {
		.container {
			min-height: 100svh;
		}

		main {
			align-items: flex-start;
		}

		.hero-box {
			padding: 26px 18px;
		}

		.room-inputs-row {
			flex-wrap: wrap;
		}

		.room-name-group,
		.room-code-group {
			flex-basis: 100%;
		}

		.identity-inputs-row {
			flex-wrap: wrap;
		}

		.identity-inputs-row .field-group {
			flex-basis: 100%;
		}

		.or-divider {
			width: 100%;
			text-align: center;
		}
	}
</style>
