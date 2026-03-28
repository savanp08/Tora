<script lang="ts">
	import { goto } from '$app/navigation';
	import ExpiryClockPicker from '$lib/components/home/ExpiryClockPicker.svelte';
	import OtpCodeInput from '$lib/components/home/OtpCodeInput.svelte';
	import MonochromeRoomBackground from '$lib/components/background/MonochromeRoomBackground.svelte';
	import toraLogo from '$lib/assets/tora-logo.svg';
	import { resolveApiBase } from '$lib/config/apiBase';
	import { APP_LIMITS } from '$lib/config/limits';
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
		sanitizeRoomCodePartial,
		normalizeUsernameInput,
		type JoinMode
	} from '$lib/utils/homeJoin';
	import { generateRoomName } from '$lib/utils/nameGenerator';
	import { captureCurrentRoom } from '$lib/utils/pendingRooms';
	import {
		readSessionRoomPreferences,
		writeSessionRoomPreferences
	} from '$lib/utils/sessionPreferences';
	import { setSessionToken } from '$lib/utils/sessionToken';
	import { onMount, tick } from 'svelte';

	type TurnstileApi = {
		render: (container: HTMLElement, options: Record<string, unknown>) => string;
		execute: (widgetId?: string) => void;
		reset: (widgetId?: string) => void;
		remove: (widgetId?: string) => void;
	};

	type TurnstileHostWindow = Window & {
		turnstile?: TurnstileApi;
		onTurnstileSuccess?: (token: string) => void;
	};

	type RoomInputSource = 'name' | 'code';

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = resolveApiBase(API_BASE_RAW);
	const TURNSTILE_SITE_KEY_RAW = import.meta.env.VITE_TURNSTILE_SITE_KEY as string | undefined;
	const TURNSTILE_SITE_KEY = TURNSTILE_SITE_KEY_RAW?.trim() ?? '';
	const TURNSTILE_VERIFY_TIMEOUT_MS = 12000;
	const TURNSTILE_POLL_INTERVAL_MS = 120;
	const TURNSTILE_PREWARM_MAX_AGE_MS = 90_000;
	const ROOM_CODE_DIGITS = APP_LIMITS.room.codeDigits;
	const ROOM_NAME_MAX_LENGTH = APP_LIMITS.room.nameMaxLength;
	const ROOM_PASSWORD_MAX_LENGTH = APP_LIMITS.room.passwordMaxLength;
	const INCOMPLETE_CODE_MESSAGE = `Enter all ${ROOM_CODE_DIGITS} digits or set a room name.`;

	let roomName = '';
	let roomCode = '';
	let guestUsername = '';
	let roomPassword = '';
	let roomDurationHours = 24;
	let aiEnabled = true;
	let e2eEnabled = false;
	let isJoining = false;
	let joinError = '';
	let lastRoomInputSource: RoomInputSource = 'name';
	let roomNameInputElement: HTMLInputElement | null = null;
	let normalizedRoomName = '';
	let normalizedRoomCode = '';
	let partialRoomCode = '';
	let canCreate = false;
	let canJoinExisting = false;
	let subtleInputError = '';

	let turnstileContainerElement: HTMLDivElement | null = null;
	let turnstileWidgetID = '';
	let turnstileResolve: ((token: string) => void) | null = null;
	let turnstileTimeoutHandle: ReturnType<typeof setTimeout> | null = null;
	let warmTurnstileTokenValue = '';
	let warmTurnstileTokenAtMs = 0;
	let warmTurnstilePromise: Promise<string> | null = null;

	function persistSessionRoomPreferences() {
		const normalized = writeSessionRoomPreferences({ aiEnabled, e2eEnabled });
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
		if (nextValue) {
			aiEnabled = false;
		}
		persistSessionRoomPreferences();
	}

	function getTurnstileHostWindow() {
		return window as TurnstileHostWindow;
	}

	function clearTurnstilePendingState() {
		if (turnstileTimeoutHandle) {
			clearTimeout(turnstileTimeoutHandle);
			turnstileTimeoutHandle = null;
		}
		turnstileResolve = null;
	}

	function initializeTurnstileWidget() {
		if (turnstileWidgetID) {
			return true;
		}
		const hostWindow = getTurnstileHostWindow();
		if (!TURNSTILE_SITE_KEY || !turnstileContainerElement || !hostWindow.turnstile?.render) {
			return false;
		}
		try {
			turnstileWidgetID = hostWindow.turnstile.render(turnstileContainerElement, {
				sitekey: TURNSTILE_SITE_KEY,
				execution: 'execute',
				callback: (token: string) => {
					const callbackWindow = getTurnstileHostWindow();
					callbackWindow.onTurnstileSuccess?.(token);
				},
				'error-callback': () => {},
				'expired-callback': () => {
					warmTurnstileTokenValue = '';
					warmTurnstileTokenAtMs = 0;
					try {
						hostWindow.turnstile?.reset?.(turnstileWidgetID);
					} catch {}
				}
			});
			return turnstileWidgetID !== '';
		} catch {
			turnstileWidgetID = '';
			return false;
		}
	}

	async function waitForTurnstileAPI(timeoutMs = TURNSTILE_VERIFY_TIMEOUT_MS) {
		if (getTurnstileHostWindow().turnstile?.render) {
			return true;
		}
		const startedAt = Date.now();
		while (Date.now() - startedAt < timeoutMs) {
			await new Promise((resolve) => setTimeout(resolve, TURNSTILE_POLL_INTERVAL_MS));
			if (getTurnstileHostWindow().turnstile?.render) {
				return true;
			}
		}
		return false;
	}

	async function requestTurnstileToken() {
		if (!TURNSTILE_SITE_KEY) {
			throw new Error('Security verification is not configured');
		}
		if (!(await waitForTurnstileAPI()) || !initializeTurnstileWidget()) {
			throw new Error('Security verification is unavailable.');
		}
		clearTurnstilePendingState();
		return new Promise<string>((resolve, reject) => {
			turnstileResolve = resolve;
			turnstileTimeoutHandle = setTimeout(() => {
				clearTurnstilePendingState();
				reject(new Error('Security verification timed out.'));
			}, TURNSTILE_VERIFY_TIMEOUT_MS);
			try {
				const hostWindow = getTurnstileHostWindow();
				hostWindow.turnstile?.reset?.(turnstileWidgetID);
				hostWindow.turnstile?.execute(turnstileWidgetID);
			} catch {
				clearTurnstilePendingState();
				reject(new Error('Failed to run security verification.'));
			}
		});
	}

	function hasFreshWarmTurnstileToken() {
		return (
			warmTurnstileTokenValue !== '' &&
			Date.now() - warmTurnstileTokenAtMs < TURNSTILE_PREWARM_MAX_AGE_MS
		);
	}

	function warmTurnstileToken(force = false) {
		if (!TURNSTILE_SITE_KEY) {
			return Promise.reject(new Error('Security verification is not configured'));
		}
		if (!force && hasFreshWarmTurnstileToken()) {
			return Promise.resolve(warmTurnstileTokenValue);
		}
		if (!force && warmTurnstilePromise) {
			return warmTurnstilePromise;
		}
		warmTurnstilePromise = requestTurnstileToken()
			.then((token) => {
				warmTurnstileTokenValue = token.trim();
				warmTurnstileTokenAtMs = Date.now();
				return warmTurnstileTokenValue;
			})
			.finally(() => {
				warmTurnstilePromise = null;
			});
		return warmTurnstilePromise;
	}

	function invalidateWarmTurnstileToken() {
		warmTurnstileTokenValue = '';
		warmTurnstileTokenAtMs = 0;
	}

	function selectRoomNameInput() {
		tick().then(() => {
			roomNameInputElement?.focus();
			roomNameInputElement?.select();
		});
	}

	function onRoomNameFocus() {
		if (lastRoomInputSource === 'code') {
			roomName = generateRoomName();
		}
		lastRoomInputSource = 'name';
		joinError = '';
		selectRoomNameInput();
	}

	function onRoomCodeFocus() {
		lastRoomInputSource = 'code';
		joinError = '';
	}

	async function handleRoomAction(mode: JoinMode) {
		const requestedRoomName =
			lastRoomInputSource === 'code'
				? normalizeRoomCodeInput(roomCode)
				: normalizeRoomNameInput(roomName);
		const requestedRoomCode =
			lastRoomInputSource === 'code' ? normalizeRoomCodeInput(roomCode) : '';
		if (lastRoomInputSource === 'code' && !requestedRoomCode) {
			joinError = INCOMPLETE_CODE_MESSAGE;
			return;
		}
		if (lastRoomInputSource === 'name' && !requestedRoomName) {
			joinError =
				mode === 'create'
					? 'New rooms require a room name'
					: `Enter a room name or a ${ROOM_CODE_DIGITS}-digit room code`;
			return;
		}

		isJoining = true;
		joinError = '';
		roomName = lastRoomInputSource === 'code' ? '' : requestedRoomName;
		roomCode = lastRoomInputSource === 'code' ? requestedRoomCode : '';

		const identity = getOrInitIdentity();
		const requestedUsername = normalizeUsernameInput(guestUsername);
		const userIdentity = requestedUsername ? updateUsername(requestedUsername) : identity;
		const normalizedPassword = (roomPassword || '').trim().slice(0, ROOM_PASSWORD_MAX_LENGTH);
		activeRoomPassword.set(normalizedPassword);

		const preferences = persistSessionRoomPreferences();
		let turnstileToken = '';
		try {
			if (mode === 'create') {
				turnstileToken = hasFreshWarmTurnstileToken()
					? warmTurnstileTokenValue
					: await warmTurnstileToken(true);
				invalidateWarmTurnstileToken();
				void warmTurnstileToken().catch(() => {});
			}

			const response = await fetch(`${API_BASE}/api/rooms/join`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					roomName: requestedRoomName,
					roomCode: requestedRoomCode,
					roomPassword: normalizedPassword,
					username: userIdentity.username,
					userId: userIdentity.id,
					type: 'ephemeral',
					mode,
					roomDurationHours,
					turnstileToken,
					aiEnabled: preferences.aiEnabled,
					e2eEnabled: preferences.e2eEnabled
				})
			});
			const data = (await response.json().catch(() => ({}))) as Record<string, unknown>;
			if (!response.ok) {
				throw new Error(toStringValue(data.error) || 'Failed to join room');
			}

			const nextRoomId = normalizeRoomIdValue(toStringValue(data.roomId));
			if (!nextRoomId) {
				throw new Error('Server returned an invalid room id');
			}
			const nextRoomName =
				lastRoomInputSource === 'code'
					? requestedRoomName
					: normalizeRoomNameInput(toStringValue(data.roomName)) || requestedRoomName;

			currentUser.set({
				id: toStringValue(data.userId) || userIdentity.id,
				username: userIdentity.username
			});
			authToken.set(toStringValue(data.token));
			setSessionToken(toStringValue(data.token));
			captureCurrentRoom(nextRoomId, nextRoomName);

			const passwordHash = normalizedPassword
				? `#key=${encodeURIComponent(normalizedPassword)}`
				: '';
			await goto(
				`/temp/chat/${encodeURIComponent(nextRoomId)}?name=${encodeURIComponent(nextRoomName)}&member=1${passwordHash}`
			);
		} catch (error) {
			joinError =
				error instanceof Error ? error.message : 'Unable to create or join the temp room.';
		} finally {
			isJoining = false;
		}
	}

	function toStringValue(value: unknown) {
		if (typeof value === 'string') {
			return value;
		}
		if (typeof value === 'number' || typeof value === 'boolean') {
			return String(value);
		}
		return '';
	}

	onMount(() => {
		const hostWindow = getTurnstileHostWindow();
		const previousHandler = hostWindow.onTurnstileSuccess;
		hostWindow.onTurnstileSuccess = (token: string) => {
			const normalized = (token || '').trim();
			if (!normalized) {
				return;
			}
			if (turnstileResolve) {
				turnstileResolve(normalized);
				clearTurnstilePendingState();
			}
		};

		roomName = generateRoomName();
		const identity = getOrInitIdentity();
		currentUser.set({ id: identity.id, username: identity.username });
		const sessionPreferences = readSessionRoomPreferences();
		aiEnabled = sessionPreferences.aiEnabled;
		e2eEnabled = sessionPreferences.e2eEnabled;
		sessionAIEnabled.set(sessionPreferences.aiEnabled);
		sessionE2EEnabled.set(sessionPreferences.e2eEnabled);

		const warmTimer = setTimeout(() => {
			void warmTurnstileToken().catch(() => {});
		}, 400);

		return () => {
			clearTimeout(warmTimer);
			clearTurnstilePendingState();
			const callbackWindow = getTurnstileHostWindow();
			if (turnstileWidgetID && callbackWindow.turnstile?.remove) {
				try {
					callbackWindow.turnstile.remove(turnstileWidgetID);
				} catch {}
			}
			turnstileWidgetID = '';
			if (previousHandler) {
				callbackWindow.onTurnstileSuccess = previousHandler;
			} else {
				delete callbackWindow.onTurnstileSuccess;
			}
		};
	});

	$: normalizedRoomName = normalizeRoomNameInput(roomName);
	$: normalizedRoomCode = normalizeRoomCodeInput(roomCode);
	$: partialRoomCode = sanitizeRoomCodePartial(roomCode);
	$: if (lastRoomInputSource === 'code' && partialRoomCode !== '' && roomName !== '') {
		roomName = '';
	}
	$: subtleInputError =
		lastRoomInputSource === 'code' && !normalizedRoomCode ? INCOMPLETE_CODE_MESSAGE : '';
	$: canCreate = lastRoomInputSource === 'code' ? !!normalizedRoomCode : !!normalizedRoomName;
	$: canJoinExisting = lastRoomInputSource === 'code' ? !!normalizedRoomCode : !!normalizedRoomName;
	$: if (!isJoining && canCreate && TURNSTILE_SITE_KEY && !hasFreshWarmTurnstileToken()) {
		void warmTurnstileToken().catch(() => {});
	}
</script>

<svelte:head>
	<title>Tora Temp Chat</title>
	<meta
		name="description"
		content="Create or join a lightweight temporary Tora chat room without boards."
	/>
</svelte:head>

<main class="temp-login-shell">
	<MonochromeRoomBackground seed="tora-temp-login" />
	<section class="temp-login-card">
		<div class="temp-login-brand">
			<img src={toraLogo} alt="Tora" />
			<div>
				<p class="eyebrow">Temp chat</p>
				<h1>Fast ephemeral rooms without boards.</h1>
			</div>
		</div>

		<p class="intro">
			This flow keeps chat, AI, media, and room controls, but skips dashboard, task board,
			drawboard, and canvas loading.
		</p>

		<div class="field-grid">
			<label class="field">
				<span>Room name</span>
				<input
					type="text"
					maxlength={ROOM_NAME_MAX_LENGTH}
					bind:value={roomName}
					bind:this={roomNameInputElement}
					on:focus={onRoomNameFocus}
					placeholder="launch-standup"
					disabled={isJoining}
				/>
			</label>

			<label class="field">
				<span>{ROOM_CODE_DIGITS}-digit room code</span>
				<div class="otp-wrap" on:focusin={onRoomCodeFocus}>
					<OtpCodeInput
						idPrefix="temp-room-code-digit"
						bind:value={roomCode}
						disabled={isJoining}
					/>
				</div>
			</label>

			<label class="field">
				<span>Your name</span>
				<input
					type="text"
					maxlength="32"
					bind:value={guestUsername}
					placeholder="Guest"
					disabled={isJoining}
				/>
			</label>

			<label class="field">
				<span>Password</span>
				<input
					type="password"
					maxlength={ROOM_PASSWORD_MAX_LENGTH}
					bind:value={roomPassword}
					placeholder="Optional"
					disabled={isJoining}
				/>
			</label>
		</div>

		<div class="controls-row">
			<div class="control-card">
				<span class="control-label">Duration</span>
				<ExpiryClockPicker bind:valueHours={roomDurationHours} disabled={isJoining} />
			</div>
			<div class="control-card">
				<span class="control-label">AI</span>
				<div class="toggle-row">
					<button
						type="button"
						class:active={aiEnabled}
						disabled={isJoining || e2eEnabled}
						on:click={() => setAiPreference(true)}
					>
						On
					</button>
					<button
						type="button"
						class:active={!aiEnabled}
						disabled={isJoining}
						on:click={() => setAiPreference(false)}
					>
						Off
					</button>
				</div>
			</div>
			<div class="control-card">
				<span class="control-label">E2EE</span>
				<div class="toggle-row">
					<button
						type="button"
						class:active={e2eEnabled}
						disabled={isJoining}
						on:click={() => setE2EPreference(true)}
					>
						On
					</button>
					<button
						type="button"
						class:active={!e2eEnabled}
						disabled={isJoining}
						on:click={() => setE2EPreference(false)}
					>
						Off
					</button>
				</div>
			</div>
		</div>

		{#if subtleInputError}
			<p class="hint warning">{subtleInputError}</p>
		{:else}
			<p class="hint">
				Create goes to the lighter `/temp/chat/*` room shell. Existing `/chat/*` stays untouched.
			</p>
		{/if}

		{#if joinError}
			<p class="error">{joinError}</p>
		{/if}

		<div class="actions">
			<button
				type="button"
				class="primary"
				disabled={!canCreate || isJoining}
				on:click={() => void handleRoomAction('create')}
			>
				{isJoining ? 'Working...' : 'Create temp room'}
			</button>
			<button
				type="button"
				class="secondary"
				disabled={!canJoinExisting || isJoining}
				on:click={() => void handleRoomAction('join')}
			>
				Join existing
			</button>
			<a class="ghost-link" href="/"> Back to main flow </a>
		</div>

		<div class="turnstile-host" bind:this={turnstileContainerElement} aria-hidden="true"></div>
	</section>
</main>

<style>
	.temp-login-shell {
		position: relative;
		min-height: 100vh;
		display: grid;
		place-items: center;
		padding: 2rem 1.25rem;
		background:
			radial-gradient(circle at top left, rgba(255, 188, 117, 0.22), transparent 32%),
			radial-gradient(circle at bottom right, rgba(98, 144, 255, 0.18), transparent 28%),
			linear-gradient(180deg, #090c13 0%, #111724 100%);
	}

	.temp-login-card {
		position: relative;
		z-index: 1;
		width: min(760px, 100%);
		padding: 1.4rem;
		border-radius: 28px;
		border: 1px solid rgba(255, 255, 255, 0.08);
		background: rgba(8, 11, 18, 0.86);
		backdrop-filter: blur(18px);
		box-shadow: 0 30px 80px rgba(0, 0, 0, 0.34);
		color: #f6f8fc;
	}

	.temp-login-brand {
		display: flex;
		align-items: center;
		gap: 1rem;
	}

	.temp-login-brand img {
		width: 58px;
		height: 58px;
		padding: 0.9rem;
		border-radius: 18px;
		background: linear-gradient(135deg, rgba(255, 255, 255, 0.14), rgba(255, 255, 255, 0.04));
	}

	.eyebrow {
		margin: 0 0 0.2rem;
		font-size: 0.78rem;
		letter-spacing: 0.18em;
		text-transform: uppercase;
		color: #ffcc8a;
	}

	h1 {
		margin: 0;
		font-size: clamp(1.8rem, 4vw, 2.7rem);
		line-height: 1.02;
	}

	.intro {
		margin: 1rem 0 1.2rem;
		color: rgba(235, 239, 249, 0.78);
		line-height: 1.6;
	}

	.field-grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.95rem;
	}

	.field {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}

	.field span,
	.control-label {
		font-size: 0.82rem;
		font-weight: 700;
		letter-spacing: 0.02em;
		color: rgba(225, 231, 245, 0.85);
	}

	.field input,
	.otp-wrap {
		min-height: 52px;
		border-radius: 16px;
		border: 1px solid rgba(255, 255, 255, 0.12);
		background: rgba(13, 17, 27, 0.92);
		color: #f5f7fb;
	}

	.field input {
		padding: 0.9rem 1rem;
		font: inherit;
	}

	.field input:focus {
		outline: 2px solid rgba(255, 188, 117, 0.4);
		outline-offset: 2px;
	}

	.otp-wrap {
		display: flex;
		align-items: center;
		padding: 0.55rem 0.7rem;
	}

	.controls-row {
		display: grid;
		grid-template-columns: 1.4fr 1fr 1fr;
		gap: 0.95rem;
		margin-top: 1rem;
	}

	.control-card {
		padding: 0.9rem 1rem;
		border-radius: 18px;
		border: 1px solid rgba(255, 255, 255, 0.1);
		background: rgba(14, 19, 30, 0.86);
		display: flex;
		flex-direction: column;
		gap: 0.7rem;
	}

	.toggle-row {
		display: flex;
		gap: 0.5rem;
	}

	.toggle-row button {
		flex: 1;
		min-height: 40px;
		border-radius: 999px;
		border: 1px solid rgba(255, 255, 255, 0.12);
		background: rgba(255, 255, 255, 0.03);
		color: #dce4f6;
		font: inherit;
		font-weight: 700;
		cursor: pointer;
	}

	.toggle-row button.active {
		border-color: rgba(255, 188, 117, 0.72);
		background: rgba(255, 188, 117, 0.18);
		color: #fff4e6;
	}

	.toggle-row button:disabled {
		cursor: not-allowed;
		opacity: 0.55;
	}

	.hint {
		margin: 1rem 0 0;
		color: rgba(215, 223, 240, 0.72);
		font-size: 0.92rem;
	}

	.hint.warning,
	.error {
		color: #ffb9b9;
	}

	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: 0.75rem;
		align-items: center;
		margin-top: 1.2rem;
	}

	.actions button,
	.ghost-link {
		min-height: 50px;
		padding: 0.9rem 1.15rem;
		border-radius: 16px;
		font: inherit;
		font-weight: 700;
		text-decoration: none;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
	}

	.primary {
		border: none;
		background: linear-gradient(135deg, #ffb45e 0%, #ff8d69 100%);
		color: #16110c;
		box-shadow: 0 16px 34px rgba(255, 141, 105, 0.22);
	}

	.secondary {
		border: 1px solid rgba(255, 255, 255, 0.14);
		background: rgba(255, 255, 255, 0.04);
		color: #f6f8fc;
	}

	.ghost-link {
		color: rgba(226, 233, 247, 0.82);
		border: 1px dashed rgba(255, 255, 255, 0.14);
	}

	.actions button:disabled {
		cursor: not-allowed;
		opacity: 0.58;
	}

	.turnstile-host {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		opacity: 0;
		pointer-events: none;
	}

	@media (max-width: 760px) {
		.field-grid,
		.controls-row {
			grid-template-columns: 1fr;
		}

		.temp-login-card {
			padding: 1.1rem;
			border-radius: 22px;
		}

		.actions {
			flex-direction: column;
			align-items: stretch;
		}

		.actions button,
		.ghost-link {
			width: 100%;
		}
	}
</style>
