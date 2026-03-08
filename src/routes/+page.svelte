<script lang="ts">
	import { goto } from '$app/navigation';
	import ExpiryClockPicker from '$lib/components/home/ExpiryClockPicker.svelte';
	import LoginFooter from '$lib/components/home/LoginFooter.svelte';
	import OtpCodeInput from '$lib/components/home/OtpCodeInput.svelte';
	import toraLogo from '$lib/assets/tora-logo.svg';
	import {
		activeRoomPassword,
		authToken,
		currentUser,
		sessionAIEnabled,
		sessionE2EEnabled
	} from '$lib/store';
	import { getOrInitIdentity, updateUsername } from '$lib/utils/identity';
	import {
		normalizeRoomCodeInput,
		normalizeRoomIdValue,
		normalizeRoomNameInput,
		normalizeUsernameInput,
		type JoinMode
	} from '$lib/utils/homeJoin';
	import { generateRoomName } from '$lib/utils/nameGenerator';
	import {
		readSessionRoomPreferences,
		writeSessionRoomPreferences
	} from '$lib/utils/sessionPreferences';
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
	let aiEnabled = true;
	let e2eEnabled = false;
	let showAdvancedOptions = false;
	let showAiTierDetails = false;
	let activeActionMode: JoinMode | '' = '';
	let isJoining = false;
	let joinError = '';

	function persistSessionRoomPreferences() {
		const normalized = writeSessionRoomPreferences({
			aiEnabled,
			e2eEnabled
		});
		aiEnabled = normalized.aiEnabled;
		e2eEnabled = normalized.e2eEnabled;
		sessionAIEnabled.set(normalized.aiEnabled);
		sessionE2EEnabled.set(normalized.e2eEnabled);
		return normalized;
	}

	function setAiPreference(nextValue: boolean) {
		if (e2eEnabled && nextValue) {
			return;
		}
		aiEnabled = nextValue;
		persistSessionRoomPreferences();
	}

	function setE2EPreference(nextValue: boolean) {
		e2eEnabled = nextValue;
		if (e2eEnabled) {
			aiEnabled = false;
		}
		persistSessionRoomPreferences();
	}

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
		const preferences = readSessionRoomPreferences();
		aiEnabled = preferences.aiEnabled;
		e2eEnabled = preferences.e2eEnabled;
		sessionAIEnabled.set(preferences.aiEnabled);
		sessionE2EEnabled.set(preferences.e2eEnabled);
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
		const sessionPreferences = persistSessionRoomPreferences();

		try {
			clientLog('api-rooms-join-request', {
				roomName: normalizedRoomName,
				roomCode: normalizedRoomCode,
				userToJoin,
				mode,
				roomDurationHours,
				aiEnabled: sessionPreferences.aiEnabled,
				e2eEnabled: sessionPreferences.e2eEnabled
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
					roomDurationHours,
					aiEnabled: sessionPreferences.aiEnabled,
					e2eEnabled: sessionPreferences.e2eEnabled
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
			goto(
				`/chat/${resolvedRoomID}?name=${encodeURIComponent(resolvedRoomName)}&member=1${roomPasswordHash}`
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
			<div class="logo">
				<div class="logo-mark-wrap">
					<img src={toraLogo} alt="Tora logo" class="logo-mark" />
				</div>
				<span>Tora</span>
			</div>
		</header>

	<main>
		<div class="hero-box">
			<h1>Disappearing chats. <br />Instant connections.</h1>
			<p>Create a room. Share the link. Live the moment.</p>

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

					<button
						type="button"
						class="advanced-toggle"
						class:is-open={showAdvancedOptions}
						on:click={() => (showAdvancedOptions = !showAdvancedOptions)}
						aria-expanded={showAdvancedOptions}
					>
						<span class="advanced-toggle-label">
							{showAdvancedOptions ? 'Hide advanced options' : 'Advanced options'}
						</span>
						<span class="advanced-toggle-icon" aria-hidden="true"></span>
					</button>

				{#if showAdvancedOptions}
					<div class="advanced-panel">
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
								<small>Private, Secured</small>
							</div>
						</div>

						<div class="options-row">
							<div class="field-group option-group">
								<div class="option-label">AI Assistant</div>
								<div class="choice-toggle" role="radiogroup" aria-label="AI assistant setting">
									<button
										type="button"
										class:active={aiEnabled}
										on:click={() => setAiPreference(true)}
										disabled={e2eEnabled}
									>
										Yes
									</button>
									<button
										type="button"
										class:active={!aiEnabled}
										on:click={() => setAiPreference(false)}
									>
										No
									</button>
								</div>
								<small>Applies by default to rooms and branches you create in this session.</small>
								<div class="tiered-note">
									Tiered rules apply.
									<button
										type="button"
										class="tiered-readmore"
										on:click={() => (showAiTierDetails = !showAiTierDetails)}
									>
										{showAiTierDetails ? 'Hide' : 'Read more'}
									</button>
								</div>
								{#if showAiTierDetails}
									<small class="tiered-detail">
										Standard tier rules: on the free tier, conversational AI data may be used to
										improve model training.
									</small>
								{/if}
							</div>

							<div class="field-group option-group">
								<div class="option-label">End-to-end encryption</div>
								<div
									class="choice-toggle"
									role="radiogroup"
									aria-label="End-to-end encryption setting"
								>
									<button
										type="button"
										class:active={e2eEnabled}
										on:click={() => setE2EPreference(true)}
									>
										Yes
									</button>
									<button
										type="button"
										class:active={!e2eEnabled}
										on:click={() => setE2EPreference(false)}
									>
										No
									</button>
								</div>
								<small>
									When enabled, new joiners cannot view messages sent before they joined. AI is
									disabled automatically.
								</small>
							</div>
						</div>
					</div>
				{/if}

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

	:global(html),
	:global(body) {
		margin: 0;
		min-height: 100%;
		font-family: sans-serif;
		background: var(--bg-primary);
	}

	.container {
		margin: 0 auto;
		width: 100%;
		
		min-height: 100svh;
		min-height: 100dvh;
		height: auto;
		display: flex;
		flex-direction: column;
		background: var(--bg-primary);
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

	.logo-mark-wrap {
		position: relative;
		width: 34px;
		height: 34px;
		display: inline-flex;
		align-items: center;
		justify-content: center;
	}

	.logo-mark {
		width: 100%;
		height: 100%;
		filter: drop-shadow(0 7px 14px rgba(56, 189, 248, 0.24));
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

	.advanced-toggle {
		align-self: center;
		width: fit-content;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		gap: 8px;
		border: 1px solid var(--border-default);
		background: var(--surface-secondary);
		color: var(--text-secondary);
		border-radius: 8px;
		font-size: 0.82rem;
		font-weight: 600;
		padding: 9px 14px;
		cursor: pointer;
		text-align: center;
		transition:
			border-color 0.2s,
			background 0.2s;
	}

	.advanced-toggle-icon {
		width: 8px;
		height: 8px;
		border-right: 2px solid currentColor;
		border-bottom: 2px solid currentColor;
		transform: rotate(45deg) translateY(-1px);
		transition: transform 0.2s ease;
	}

	.advanced-toggle.is-open .advanced-toggle-icon {
		transform: rotate(-135deg) translateY(-1px);
	}

	.advanced-toggle:hover {
		border-color: var(--border-focus);
		background: var(--surface-active);
	}

	.advanced-panel {
		border: 1px solid var(--border-subtle);
		border-radius: 10px;
		padding: 12px;
		display: flex;
		flex-direction: column;
		gap: 12px;
		background: var(--surface-secondary);
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

	.options-row {
		display: flex;
		align-items: flex-start;
		gap: 10px;
	}

	.option-group {
		flex: 1 1 50%;
		gap: 8px;
	}

	.choice-toggle {
		display: inline-flex;
		width: fit-content;
		border: 1px solid var(--border-default);
		border-radius: 999px;
		padding: 2px;
		background: var(--surface-primary);
	}

	.choice-toggle button {
		border: none;
		background: transparent;
		color: var(--text-secondary);
		font-size: 0.79rem;
		font-weight: 600;
		border-radius: 999px;
		padding: 5px 12px;
		cursor: pointer;
	}

	.choice-toggle button.active {
		background: var(--home-action-primary);
		color: var(--home-action-text);
	}

	.choice-toggle button:disabled {
		cursor: not-allowed;
		opacity: 0.55;
	}

	.tiered-note {
		font-size: 0.75rem;
		color: var(--text-tertiary);
		display: inline-flex;
		align-items: center;
		gap: 6px;
	}

	.tiered-readmore {
		border: none;
		background: transparent;
		color: var(--text-secondary);
		font-size: 0.75rem;
		font-weight: 600;
		text-decoration: underline;
		cursor: pointer;
		padding: 0;
	}

	.tiered-detail {
		line-height: 1.4;
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

	.option-label {
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
		transition:
			background 0.2s,
			border-color 0.2s,
			box-shadow 0.2s;
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
		box-shadow:
			0 0 0 3px var(--home-action-focus),
			0 6px 14px var(--home-action-shadow);
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

		.options-row {
			flex-wrap: wrap;
		}

		.option-group {
			flex-basis: 100%;
		}

		.or-divider {
			width: 100%;
			text-align: center;
		}
	}
</style>
