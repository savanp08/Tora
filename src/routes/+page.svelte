<script lang="ts">
	import { goto } from '$app/navigation';
	import ExpiryClockPicker from '$lib/components/home/ExpiryClockPicker.svelte';
	import LoginFooter from '$lib/components/home/LoginFooter.svelte';
	import OtpCodeInput from '$lib/components/home/OtpCodeInput.svelte';
	import toraLogo from '$lib/assets/tora-logo.svg';
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
	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';
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
			clientLog('navigate-chat-room', { roomId: resolvedRoomID, roomName: resolvedRoomName });
			const roomPasswordHash = normalizedRoomPassword
				? `#key=${encodeURIComponent(normalizedRoomPassword)}`
				: '';
			goto(`/chat/${resolvedRoomID}?name=${encodeURIComponent(resolvedRoomName)}&member=1${roomPasswordHash}`);
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
		<div class="logo">
			<img src={toraLogo} alt="Tora logo" class="logo-mark" />
			<span>Tora</span>
		</div>
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
							maxlength="20"
							on:focus={onRoomNameFocus}
						/>
						<small>Used as display name (max 20 chars). Spaces are converted to underscores.</small>
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
							maxlength="32"
							pattern="[-A-Za-z0-9 _]+"
						/>
						<small>Optional. Spaces and dashes are normalized to underscores.</small>
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
	:global(:root) {
		--home-action-primary: #4f5f78;
		--home-action-primary-hover: #45546b;
		--home-action-secondary: #4f5f78;
		--home-action-secondary-hover: #45546b;
		--home-action-border: #4f5f78;
		--home-action-text: #ffffff;
		--home-action-focus: rgba(79, 95, 120, 0.35);
		--home-action-shadow: rgba(79, 95, 120, 0.28);
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--home-action-primary: #93c5fd;
		--home-action-primary-hover: #bfdbfe;
		--home-action-secondary: #7dd3fc;
		--home-action-secondary-hover: #bae6fd;
		--home-action-border: #7dd3fc;
		--home-action-text: #0f172a;
		--home-action-focus: rgba(147, 197, 253, 0.45);
		--home-action-shadow: rgba(147, 197, 253, 0.22);
	}

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
		display: flex;
		align-items: center;
		gap: 0.55rem;
		font-weight: 700;
		letter-spacing: 0.01em;
	}

	.logo-mark {
		width: 30px;
		height: 30px;
		filter: drop-shadow(0 6px 12px rgba(56, 189, 248, 0.24));
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
		transition: background 0.2s, border-color 0.2s, box-shadow 0.2s;
		color: var(--home-action-text);
		border: 1px solid var(--home-action-border);
		box-shadow: 0 6px 14px var(--home-action-shadow);
	}

	.btn-primary-action {
		background: var(--home-action-primary);
	}

	.btn-secondary-action {
		background: var(--home-action-secondary);
	}

	.btn-primary-action:disabled,
	.btn-secondary-action:disabled {
		background: var(--surface-active);
		border-color: var(--border-default);
		color: var(--text-tertiary);
		box-shadow: none;
		cursor: not-allowed;
	}

	.btn-primary-action:hover:not(:disabled) {
		background: var(--home-action-primary-hover);
		border-color: var(--home-action-primary-hover);
	}

	.btn-secondary-action:hover:not(:disabled) {
		background: var(--home-action-secondary-hover);
		border-color: var(--home-action-secondary-hover);
	}

	.btn-primary-action:focus-visible,
	.btn-secondary-action:focus-visible {
		outline: none;
		box-shadow: 0 0 0 3px var(--home-action-focus), 0 6px 14px var(--home-action-shadow);
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
