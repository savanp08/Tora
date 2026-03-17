<script lang="ts">
	import { goto } from '$app/navigation';
	import ExpiryClockPicker from '$lib/components/home/ExpiryClockPicker.svelte';
	import LoginFooter from '$lib/components/home/LoginFooter.svelte';
	import OtpCodeInput from '$lib/components/home/OtpCodeInput.svelte';
	import toraLogo from '$lib/assets/tora-logo.svg';
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
	import {
		readSessionRoomPreferences,
		writeSessionRoomPreferences
	} from '$lib/utils/sessionPreferences';
	import { captureCurrentRoom } from '$lib/utils/pendingRooms';
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

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';
	const TURNSTILE_SITE_KEY_RAW = import.meta.env.VITE_TURNSTILE_SITE_KEY as string | undefined;
	const TURNSTILE_SITE_KEY = TURNSTILE_SITE_KEY_RAW?.trim() ?? '';
	const TURNSTILE_DEBUG_RAW = import.meta.env.VITE_TURNSTILE_DEBUG as string | undefined;
	const TURNSTILE_DEBUG =
		import.meta.env.DEV ||
		['1', 'true', 'yes', 'on'].includes((TURNSTILE_DEBUG_RAW ?? '').trim().toLowerCase());
	const TURNSTILE_VERIFY_TIMEOUT_MS = 12000;
	const TURNSTILE_POLL_INTERVAL_MS = 120;
	const ROOM_CODE_DIGITS = APP_LIMITS.room.codeDigits;
	const ROOM_NAME_MAX_LENGTH = APP_LIMITS.room.nameMaxLength;
	const ROOM_PASSWORD_MAX_LENGTH = APP_LIMITS.room.passwordMaxLength;
	const INCOMPLETE_CODE_MESSAGE = `Enter all ${ROOM_CODE_DIGITS} digits or set a room name.`;

	type RoomInputSource = 'name' | 'code';

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
	let lastRoomInputSource: RoomInputSource = 'name';
	let isJoining = false;
	let joinError = '';
	let roomNameInputElement: HTMLInputElement | null = null;
	let normalizedRoomName = '';
	let normalizedRoomCode = '';
	let partialRoomCode = '';
	let subtleInputError = '';
	let canCreate = false;
	let canJoinExisting = false;
	let isReviveDragActive = false;
	let isRevivingRoom = false;
	let reviveDragDepth = 0;
	let turnstileContainerElement: HTMLDivElement | null = null;
	let turnstileWidgetID = '';
	let turnstileToken = '';
	let turnstileResolve: ((token: string) => void) | null = null;
	let turnstileTimeoutHandle: ReturnType<typeof setTimeout> | null = null;

	// Cycling words for hero
	const cycleWords = ['now.', 'together.', 'with AI.', 'faster.'];
	let cycleIdx = 0;
	let cycleWord = cycleWords[0];
	let cycleVisible = true;

	// Pricing tiers
	const plans = [
		{
			name: 'Free',
			price: '$0',
			period: 'forever',
			desc: 'For individuals exploring Tora.',
			features: ['Unlimited ephemeral rooms', 'Chat, whiteboard & task tools', '@ToraAI (limited messages/day)', 'P2P voice & video calls', 'Community support'],
			cta: 'Get started',
			highlighted: false,
		},
		{
			name: 'Plus',
			price: '$10',
			period: 'per month',
			desc: 'For power users who live in Tora.',
			features: ['Everything in Free', 'Extended @ToraAI usage', 'Persistent room history (7 days)', 'Priority room creation', 'Email support'],
			cta: 'Start Plus',
			highlighted: false,
		},
		{
			name: 'Pro',
			price: '$20',
			period: 'per month',
			desc: 'For lean teams shipping fast.',
			features: ['Everything in Plus', 'Unlimited @ToraAI messages', 'Persistent history (30 days)', 'Custom AI provider keys', 'Private AI mode for all users', 'Priority support'],
			cta: 'Start Pro',
			highlighted: true,
		},
		{
			name: 'Enterprise',
			price: '$250',
			period: 'per month',
			desc: 'For organisations with compliance needs.',
			features: ['Everything in Pro', 'Self-hosted deployment', 'SAML SSO & SCIM', 'GDPR / SOC2 / ISO27001', 'SLA & dedicated support', 'Custom AI model integration'],
			cta: 'Contact us',
			highlighted: false,
		},
	];

	type ClientLogLevel = 'debug' | 'info' | 'warn' | 'error';

	function persistSessionRoomPreferences() {
		const normalized = writeSessionRoomPreferences({ aiEnabled, e2eEnabled });
		aiEnabled = normalized.aiEnabled;
		e2eEnabled = normalized.e2eEnabled;
		sessionAIEnabled.set(normalized.aiEnabled);
		sessionE2EEnabled.set(normalized.e2eEnabled);
		return normalized;
	}
	function setAiPreference(v: boolean) {
		if (e2eEnabled && v) return;
		aiEnabled = v;
		persistSessionRoomPreferences();
	}
	function setE2EPreference(v: boolean) {
		e2eEnabled = v;
		if (v) aiEnabled = false;
		persistSessionRoomPreferences();
	}
	function normalizeErrorForLog(e: unknown) {
		return e instanceof Error ? { name: e.name, message: e.message } : { message: String(e) };
	}
	function clientLog(event: string, payload?: unknown, level: ClientLogLevel = 'info') {
		if (!event.startsWith('turnstile-')) return;
		if ((level === 'debug' || level === 'info') && !TURNSTILE_DEBUG) return;
		({ debug: console.debug, info: console.info, warn: console.warn, error: console.error }[level] ?? console.info)(`[Turnstile] ${event}`, payload);
	}
	function getTurnstileHostWindow() { return window as TurnstileHostWindow; }
	function clearTurnstilePendingState() {
		if (turnstileTimeoutHandle) { clearTimeout(turnstileTimeoutHandle); turnstileTimeoutHandle = null; }
		turnstileResolve = null;
	}
	function resetTurnstile() {
		turnstileToken = ''; clearTurnstilePendingState();
		const w = getTurnstileHostWindow();
		if (turnstileWidgetID && w.turnstile?.reset) try { w.turnstile.reset(turnstileWidgetID); } catch {}
	}
	function initializeTurnstileWidget() {
		if (turnstileWidgetID) return true;
		const w = getTurnstileHostWindow();
		if (!TURNSTILE_SITE_KEY || !turnstileContainerElement || !w.turnstile?.render) return false;
		try {
			turnstileWidgetID = w.turnstile.render(turnstileContainerElement, {
				sitekey: TURNSTILE_SITE_KEY, execution: 'execute',
				callback: (token: string) => { const cw = getTurnstileHostWindow(); if (typeof cw.onTurnstileSuccess === 'function') cw.onTurnstileSuccess(token); },
				'error-callback': () => {},
				'expired-callback': () => { turnstileToken = ''; try { getTurnstileHostWindow().turnstile?.reset?.(turnstileWidgetID); } catch {} }
			});
			return turnstileWidgetID !== '';
		} catch { turnstileWidgetID = ''; return false; }
	}
	async function waitForTurnstileAPI(ms = TURNSTILE_VERIFY_TIMEOUT_MS) {
		if (getTurnstileHostWindow().turnstile?.render) return true;
		const t = Date.now();
		while (Date.now() - t < ms) { await new Promise(r => setTimeout(r, TURNSTILE_POLL_INTERVAL_MS)); if (getTurnstileHostWindow().turnstile?.render) return true; }
		return false;
	}
	async function requestTurnstileToken() {
		if (!TURNSTILE_SITE_KEY) throw new Error('Security verification is not configured');
		if (!await waitForTurnstileAPI() || !initializeTurnstileWidget() || !getTurnstileHostWindow().turnstile?.execute) throw new Error('Security verification is unavailable.');
		turnstileToken = ''; clearTurnstilePendingState();
		return new Promise<string>((resolve, reject) => {
			turnstileResolve = resolve;
			turnstileTimeoutHandle = setTimeout(() => { clearTurnstilePendingState(); reject(new Error('Security verification timed out.')); }, TURNSTILE_VERIFY_TIMEOUT_MS);
			const w = getTurnstileHostWindow();
			try { w.turnstile?.reset?.(turnstileWidgetID); w.turnstile?.execute(turnstileWidgetID); }
			catch { clearTurnstilePendingState(); reject(new Error('Failed to run security verification.')); }
		});
	}

	onMount(() => {
		const w = getTurnstileHostWindow();
		const prev = w.onTurnstileSuccess;
		w.onTurnstileSuccess = (token: string) => {
			turnstileToken = (token || '').trim();
			if (turnstileToken && turnstileResolve) { turnstileResolve(turnstileToken); clearTurnstilePendingState(); }
		};
		roomName = generateRoomName();
		const id = getOrInitIdentity();
		currentUser.set({ id: id.id, username: id.username });
		const prefs = readSessionRoomPreferences();
		aiEnabled = prefs.aiEnabled; e2eEnabled = prefs.e2eEnabled;
		sessionAIEnabled.set(prefs.aiEnabled); sessionE2EEnabled.set(prefs.e2eEnabled);
		window.addEventListener('dragenter', onWindowDragEnter);
		window.addEventListener('dragover', onWindowDragOver);
		window.addEventListener('dragleave', onWindowDragLeave);
		window.addEventListener('drop', onWindowDrop);

		// Cycle words
		const iv = setInterval(() => {
			cycleVisible = false;
			setTimeout(() => {
				cycleIdx = (cycleIdx + 1) % cycleWords.length;
				cycleWord = cycleWords[cycleIdx];
				cycleVisible = true;
			}, 350);
		}, 2800);

		return () => {
			clearInterval(iv);
			window.removeEventListener('dragenter', onWindowDragEnter);
			window.removeEventListener('dragover', onWindowDragOver);
			window.removeEventListener('dragleave', onWindowDragLeave);
			window.removeEventListener('drop', onWindowDrop);
			clearTurnstilePendingState();
			const cw = getTurnstileHostWindow();
			if (turnstileWidgetID && cw.turnstile?.remove) try { cw.turnstile.remove(turnstileWidgetID); } catch {}
			turnstileWidgetID = '';
			if (prev) cw.onTurnstileSuccess = prev; else delete cw.onTurnstileSuccess;
		};
	});

	function selectRoomNameInput() { tick().then(() => { roomNameInputElement?.focus(); roomNameInputElement?.select(); }); }
	function onRoomNameFocus() { if (lastRoomInputSource === 'code') roomName = generateRoomName(); lastRoomInputSource = 'name'; joinError = ''; selectRoomNameInput(); }
	function onRoomCodeFocus() { lastRoomInputSource = 'code'; joinError = ''; }
	function isFileDragEvent(e: DragEvent) { return !!(e.dataTransfer?.types && Array.from(e.dataTransfer.types).includes('Files')); }
	function isSupportedReviveFile(f: File) { return f.name.toLowerCase().endsWith('.tora') || f.type.toLowerCase() === 'application/json'; }
	function readFileAsText(f: File) { return new Promise<string>((res, rej) => { const r = new FileReader(); r.onload = () => res(String(r.result ?? '')); r.onerror = () => rej(new Error('Failed to read file')); r.readAsText(f); }); }
	async function reviveRoomFromArchive(file: File) {
		if (!isSupportedReviveFile(file)) { joinError = 'Unsupported file type. Use a .tora file or JSON.'; return; }
		isRevivingRoom = true; joinError = '';
		try {
			const payload = JSON.parse(await readFileAsText(file));
			const res = await fetch(`${API_BASE}/api/rooms/revive`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) });
			const data = await res.json().catch(() => ({})) as Record<string, unknown>;
			if (!res.ok) throw new Error(String(data.error || 'Failed to revive room'));
			const rid = normalizeRoomIdValue(String(data.newRoomId ?? data.roomId ?? data.new_room_id ?? ''));
			if (!rid) throw new Error('Server returned an invalid room id');
			goto(`/room/${encodeURIComponent(rid)}`);
		} catch (e: unknown) { joinError = e instanceof Error ? e.message : 'Failed to revive room'; }
		finally { isRevivingRoom = false; }
	}
	function onWindowDragEnter(e: DragEvent) { if (!isFileDragEvent(e)) return; e.preventDefault(); reviveDragDepth++; isReviveDragActive = true; }
	function onWindowDragOver(e: DragEvent) { if (!isFileDragEvent(e)) return; e.preventDefault(); if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy'; }
	function onWindowDragLeave(e: DragEvent) { if (!isFileDragEvent(e)) return; e.preventDefault(); reviveDragDepth = Math.max(0, reviveDragDepth - 1); if (!reviveDragDepth) isReviveDragActive = false; }
	async function onWindowDrop(e: DragEvent) {
		if (!isFileDragEvent(e)) return; e.preventDefault();
		reviveDragDepth = 0; isReviveDragActive = false;
		const f = e.dataTransfer?.files?.item(0);
		if (f) await reviveRoomFromArchive(f);
	}
	async function handleRoomAction(mode: JoinMode) {
		const nnrn = normalizeRoomNameInput(roomName);
		const nnrc = normalizeRoomCodeInput(roomCode);
		let reqName = nnrn, reqCode = '';
		if (lastRoomInputSource === 'code') {
			if (!nnrc) { joinError = INCOMPLETE_CODE_MESSAGE; return; }
			reqName = nnrc; reqCode = nnrc;
		} else if (!nnrn) { joinError = mode === 'create' ? 'New rooms require a room name' : `Enter a room name or a ${ROOM_CODE_DIGITS}-digit room code`; return; }
		isJoining = true; activeActionMode = mode; joinError = '';
		roomName = lastRoomInputSource === 'code' ? '' : reqName;
		roomCode = lastRoomInputSource === 'code' ? reqCode : '';
		const identity = getOrInitIdentity();
		const userIdentity = normalizeUsernameInput(guestUsername) ? updateUsername(normalizeUsernameInput(guestUsername)) : identity;
		guestUsername = userIdentity.username;
		const normPwd = (roomPassword || '').trim().slice(0, ROOM_PASSWORD_MAX_LENGTH);
		activeRoomPassword.set(normPwd);
		const prefs = persistSessionRoomPreferences();
		let tsToken = '';
		try {
			if (mode === 'create') tsToken = await requestTurnstileToken();
			const res = await fetch(`${API_BASE}/api/rooms/join`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ roomName: reqName, roomCode: reqCode, username: userIdentity.username, userId: userIdentity.id, type: 'ephemeral', mode, roomDurationHours, turnstileToken: tsToken, aiEnabled: prefs.aiEnabled, e2eEnabled: prefs.e2eEnabled }) });
			const data = await res.json();
			if (!res.ok) throw new Error(data.error || 'Failed to join room');
			currentUser.set({ id: data.userId || userIdentity.id, username: userIdentity.username });
			authToken.set(data.token); setSessionToken(data.token || '');
			const roomId = normalizeRoomIdValue(String(data.roomId || ''));
			if (!roomId) throw new Error('Server returned an invalid room id');
			const rName = lastRoomInputSource === 'code' ? reqName : data.roomName || reqName;
			captureCurrentRoom(roomId, rName);
			goto(`/chat/${roomId}?name=${encodeURIComponent(rName)}&member=1${normPwd ? `#key=${encodeURIComponent(normPwd)}` : ''}`);
		} catch (e: any) { joinError = e.message; }
		finally { if (mode === 'create') resetTurnstile(); isJoining = false; activeActionMode = ''; }
	}

	$: normalizedRoomName = normalizeRoomNameInput(roomName);
	$: normalizedRoomCode = normalizeRoomCodeInput(roomCode);
	$: partialRoomCode = sanitizeRoomCodePartial(roomCode);
	$: if (lastRoomInputSource === 'code' && partialRoomCode !== '' && roomName !== '') roomName = '';
	$: subtleInputError = lastRoomInputSource === 'code' && !normalizedRoomCode ? INCOMPLETE_CODE_MESSAGE : '';
	$: canCreate = lastRoomInputSource === 'code' ? !!normalizedRoomCode : !!normalizedRoomName;
	$: canJoinExisting = lastRoomInputSource === 'code' ? !!normalizedRoomCode : !!normalizedRoomName;

	const schemaJson = JSON.stringify({ '@context': 'https://schema.org', '@type': 'SoftwareApplication', name: 'Tora', applicationCategory: 'CommunicationApplication', operatingSystem: 'Web', description: 'Tora is an AI agentic workspace for lean teams and individuals.' });
</script>

<svelte:head>
	<title>Tora — The AI workspace for lean teams</title>
	<meta name="description" content="Tora is an AI agentic workspace with real-time chat, collaborative code canvas, whiteboard, task management, and an AI assistant that knows your whole room." />
	<meta property="og:title" content="Tora — The AI workspace for lean teams" />
	<meta name="application-name" content="Tora" />
	<link rel="preconnect" href="https://fonts.googleapis.com" />
	<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin="anonymous" />
	<link href="https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@300;400;500;600;700;800&display=swap" rel="stylesheet" />
	<script type="application/ld+json">{@html schemaJson}</script>
</svelte:head>

{#if isReviveDragActive}
	<div class="revive-overlay" role="status" aria-live="polite">
		<div class="revive-box">
			<div class="revive-icon">↓</div>
			<strong>Drop to Revive Room</strong>
			<p>Accepted: <code>.tora</code> or <code>application/json</code></p>
			{#if isRevivingRoom}<p class="revive-loading">Reviving…</p>{/if}
		</div>
	</div>
{/if}

<div class="page">

	<!-- ▸ Background atmosphere -->
	<div class="bg" aria-hidden="true">
		<div class="bg-glow-top"></div>
		<div class="bg-grid"></div>
	</div>

	<!-- ▸ Announcement banner -->
	<div class="banner">
		<a href="https://github.com/savanp08/tora" target="_blank" rel="noopener" class="banner-inner">
			<span class="banner-star">★</span>
			<span>Tora is fully open source</span>
			<span class="banner-arrow">Star us on GitHub →</span>
		</a>
	</div>

	<!-- ▸ Nav -->
	<nav class="nav">
		<div class="nav-wrap">
			<a href="/" class="nav-logo">
				<img src={toraLogo} alt="Tora" />
				<span>Tora</span>
			</a>
			<div class="nav-links">
				<a href="https://github.com/savanp08/tora" target="_blank" rel="noopener">GitHub</a>
				<a href="/home">Dashboard</a>
				<a href="#tools">Tools</a>
				<a href="#pricing">Pricing</a>
			</div>
			<button class="nav-cta" on:click={() => document.getElementById('room-entry')?.scrollIntoView({ behavior:'smooth' })}>
				Open workspace
			</button>
		</div>
	</nav>

	<!-- ▸ Hero — full viewport -->
	<section class="hero">
		<div class="hero-wrap">

			<div class="hero-eyebrow">
				<span class="eyebrow-dot"></span>
				Introducing Tora
			</div>

			<h1 class="hero-h1">
				Build, ship, and<br />
				collaborate —<br />
				<span class="hero-word" class:visible={cycleVisible}>{cycleWord}</span>
			</h1>

			<p class="hero-sub">
				Tora is an AI agentic workspace for lean teams and independent builders.
				Real-time chat, code, whiteboard, and project tools — all in one room,
				with an AI that knows your full context.
			</p>

			<div class="hero-ctas">
				<button class="cta-primary" on:click={() => document.getElementById('room-entry')?.scrollIntoView({ behavior:'smooth' })}>
					Create a free room
				</button>
				<a href="https://github.com/savanp08/tora" target="_blank" rel="noopener" class="cta-ghost">
					View on GitHub →
				</a>
			</div>

			<!-- Trusted-by strip (like Thesys) -->
			<div class="trust-strip">
				<span>Open source</span>
				<span class="trust-dot"></span>
				<span>No account required</span>
				<span class="trust-dot"></span>
				<span>E2E encrypted</span>
				<span class="trust-dot"></span>
				<span>Self-hostable</span>
			</div>
		</div>
	</section>

	<!-- ▸ Room entry card — the "demo" section like Thesys's demo screens -->
	<section class="entry-section" id="room-entry">
		<div class="entry-wrap">

			<p class="entry-label">Start a session</p>

			<div class="entry-card">
				<!-- Window chrome -->
				<div class="card-chrome">
					<span class="chrome-dot" style="background:#ff5f57"></span>
					<span class="chrome-dot" style="background:#febc2e"></span>
					<span class="chrome-dot" style="background:#28c840"></span>
					<span class="chrome-title">New Tora room</span>
				</div>

				<!-- Card body -->
				<div class="card-body">
					{#if joinError}
						<div class="err-banner">{joinError}</div>
					{/if}

					<div class="inputs-row">
						<div class="field">
							<label for="room-name-input">Room name</label>
							<input
								id="room-name-input"
								type="text"
								placeholder="e.g. Product Sprint"
								bind:value={roomName}
								bind:this={roomNameInputElement}
								maxlength={ROOM_NAME_MAX_LENGTH}
								on:focus={onRoomNameFocus}
							/>
						</div>

						<div class="or-divider">or</div>

						<div class="field" on:focusin={onRoomCodeFocus}>
							<label for="room-code-digit-0">{ROOM_CODE_DIGITS}-digit code</label>
							<OtpCodeInput idPrefix="room-code-digit" bind:value={roomCode} disabled={isJoining} />
							{#if subtleInputError}<span class="field-err">{subtleInputError}</span>{/if}
						</div>
					</div>

					<!-- Advanced toggle -->
					<button
						class="adv-btn"
						class:open={showAdvancedOptions}
						type="button"
						on:click={() => (showAdvancedOptions = !showAdvancedOptions)}
						aria-expanded={showAdvancedOptions}
					>
						Advanced options
						<svg viewBox="0 0 10 6" fill="none" class="adv-chevron">
							<path d="M1 1l4 4 4-4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
						</svg>
					</button>

					{#if showAdvancedOptions}
						<div class="adv-panel">
							<ExpiryClockPicker bind:valueHours={roomDurationHours} disabled={isJoining} />
							<div class="adv-row">
								<div class="field">
									<label for="username-input">Username (optional)</label>
									<input id="username-input" type="text" placeholder="e.g. dizzy_panda" bind:value={guestUsername} maxlength="32" />
								</div>
								<div class="field">
									<label for="room-password-input">Password (optional)</label>
									<input id="room-password-input" type="password" placeholder="Optional" bind:value={roomPassword} maxlength={ROOM_PASSWORD_MAX_LENGTH} autocomplete="off" />
								</div>
							</div>
							<div class="adv-toggles">
								<div class="field">
									<div class="tgl-label">AI Assistant</div>
									<div class="seg-ctrl" role="radiogroup">
										<button type="button" class:on={aiEnabled} on:click={() => setAiPreference(true)} disabled={e2eEnabled}>Yes</button>
										<button type="button" class:on={!aiEnabled} on:click={() => setAiPreference(false)}>No</button>
									</div>
									<small>Tiered rules apply. <button type="button" class="tgl-more" on:click={() => (showAiTierDetails = !showAiTierDetails)}>{showAiTierDetails ? 'Hide' : 'Read more'}</button></small>
									{#if showAiTierDetails}<p class="tgl-detail">On the free tier, conversational AI data may be used to improve model training.</p>{/if}
								</div>
								<div class="field">
									<div class="tgl-label">E2E encryption</div>
									<div class="seg-ctrl" role="radiogroup">
										<button type="button" class:on={e2eEnabled} on:click={() => setE2EPreference(true)}>Yes</button>
										<button type="button" class:on={!e2eEnabled} on:click={() => setE2EPreference(false)}>No</button>
									</div>
									<small>AI disabled automatically when E2E is on.</small>
								</div>
							</div>
						</div>
					{/if}

					{#if TURNSTILE_SITE_KEY}
						<div class="ts-slot" bind:this={turnstileContainerElement} aria-hidden="true"></div>
					{/if}

					<div class="card-actions">
						<button class="act-create" on:click={() => void handleRoomAction('create')} disabled={isJoining || !canCreate}>
							{#if isJoining && activeActionMode === 'create'}
								<span class="btn-spin"></span> Creating…
							{:else}
								Create room
							{/if}
						</button>
						<button class="act-join" on:click={() => void handleRoomAction('join')} disabled={isJoining || !canJoinExisting}>
							{isJoining && activeActionMode === 'join' ? 'Joining…' : 'Join existing'}
						</button>
					</div>

					<p class="card-hint">No signup required · Rooms are ephemeral by design</p>
				</div>
			</div>
		</div>
	</section>

	<!-- ▸ Tools — cinematic scenes -->
	<section class="tools-section" id="tools">

		<!-- Scene 1: Chat -->
		<div class="scene">
			<div class="scene-cards">
				<!-- Back card -->
				<div class="scard scard-back">
					<div class="scard-chrome"><span class="cdot r"></span><span class="cdot y"></span><span class="cdot g"></span><span class="ctitle">Tora — chat</span></div>
					<div class="scard-body">
						<div class="sc-msg-row sc-other"><div class="sc-av" style="background:rgba(99,102,241,0.18);color:#818cf8">J</div><div class="sc-bubble sc-other-b">Let's finalize the auth flow before we push</div></div>
						<div class="sc-msg-row sc-me"><div class="sc-bubble sc-me-b">Agreed. Should we bring ToraAI in to review?</div></div>
						<div class="sc-typing"><span></span><span></span><span></span></div>
					</div>
				</div>
				<!-- Front card -->
				<div class="scard scard-front">
					<div class="scard-chrome"><span class="cdot r"></span><span class="cdot y"></span><span class="cdot g"></span><span class="ctitle">@ToraAI</span></div>
					<div class="scard-body">
						<div class="sc-ai-header"><span class="sc-ai-pill">ToraAI</span><span class="sc-ai-ctx">Reading room context…</span></div>
						<p class="sc-ai-para">I've reviewed your auth flow. Here are <strong>3 issues</strong> I found:</p>
						<div class="sc-ai-item"><span class="sc-ai-num">01</span><span>JWT expiry not handled on the client — users get silent 401s</span></div>
						<div class="sc-ai-item"><span class="sc-ai-num">02</span><span>Refresh token stored in localStorage — move to httpOnly cookie</span></div>
						<div class="sc-ai-item"><span class="sc-ai-num">03</span><span>No rate-limiting on <code>/api/auth/login</code></span></div>
						<div class="sc-ai-actions"><button class="sc-ai-btn">Apply fixes</button><button class="sc-ai-btn sc-ai-btn-ghost">Explain more</button></div>
					</div>
				</div>
			</div>
			<div class="scene-text">
				<p class="scene-tag">Chat</p>
				<h2 class="scene-h2">Your AI agent attends<br />every conversation.</h2>
				<p class="scene-sub">Mention @ToraAI in any message and it reads your entire room history — chat, code, tasks — to give you answers that actually fit your context. Private AI mode lets you ask without the team seeing.</p>
			</div>
		</div>

		<!-- Scene 2: Code Canvas -->
		<div class="scene scene-flip">
			<div class="scene-text scene-text-right">
				<p class="scene-tag" style="color:#34d399">Code Canvas</p>
				<h2 class="scene-h2">Write, run, and ship<br />code — together.</h2>
				<p class="scene-sub">Monaco-powered editor with real-time multi-cursor sync via Yjs. Run Python, JavaScript, Go and more without leaving the room. AI sees your whole project — not just the open file.</p>
			</div>
			<div class="scene-cards scene-cards-left">
				<div class="scard scard-back scard-code-back">
					<div class="scard-chrome"><span class="cdot r"></span><span class="cdot y"></span><span class="cdot g"></span><span class="ctitle">terminal</span></div>
					<div class="scard-body sc-terminal">
						<div class="term-line"><span class="term-prompt">›</span> <span class="term-cmd">python main.py</span></div>
						<div class="term-line term-out">Running test suite...</div>
						<div class="term-line term-ok">✓ 24 tests passed</div>
						<div class="term-line term-err">✗ 1 test failed: auth_flow_test.py:44</div>
						<div class="term-cursor">▌</div>
					</div>
				</div>
				<div class="scard scard-front scard-code-front">
					<div class="scard-chrome"><span class="cdot r"></span><span class="cdot y"></span><span class="cdot g"></span><span class="ctitle">main.py</span><span class="sc-live-badge">● 2 editing</span></div>
					<div class="scard-body sc-code-body">
						<div class="cline"><span class="ln">1</span><span class="ck">async def</span> <span class="cf">authenticate</span><span class="cp">(token: </span><span class="cv">str</span><span class="cp">):</span></div>
						<div class="cline"><span class="ln">2</span><span class="ci"></span><span class="ck">try</span><span class="cp">:</span></div>
						<div class="cline"><span class="ln">3</span><span class="ci"></span><span class="ci"></span><span class="cv">payload</span> <span class="cp">= await</span> <span class="cf">verify_jwt</span><span class="cp">(token)</span></div>
						<div class="cline"><span class="ln">4</span><span class="ci"></span><span class="ci"></span><span class="ck">return</span> <span class="cf">User</span><span class="cp">.</span><span class="cf">from_payload</span><span class="cp">(payload)</span></div>
						<div class="cline"><span class="ln">5</span><span class="ci"></span><span class="ck">except</span> <span class="cv">JWTExpiredError</span><span class="cp">:</span></div>
						<div class="cline cline-ai"><span class="ln">6</span><span class="ci"></span><span class="ci"></span><span class="cp">raise</span> <span class="cf">HTTPException</span><span class="cp">(401)</span><span class="sc-ghost"> # ← AI suggestion</span></div>
					</div>
					<div class="sc-ai-suggestion-bar"><span class="sc-ai-pill" style="font-size:0.65rem">ToraAI</span> Add token refresh logic here — based on your auth_flow.py</div>
				</div>
			</div>
		</div>

		<!-- Scene 3: Whiteboard -->
		<div class="scene">
			<div class="scene-cards">
				<div class="scard scard-back scard-board-back">
					<div class="scard-chrome"><span class="cdot r"></span><span class="cdot y"></span><span class="cdot g"></span><span class="ctitle">whiteboard</span></div>
					<div class="scard-body">
						<div class="sc-prompt-bar"><span class="sc-prompt-icon">✦</span> Generate system architecture for a real-time chat app</div>
					</div>
				</div>
				<div class="scard scard-front scard-board-front">
					<div class="scard-chrome"><span class="cdot r"></span><span class="cdot y"></span><span class="cdot g"></span><span class="ctitle">whiteboard — AI generated</span></div>
					<div class="scard-body">
						<svg viewBox="0 0 420 220" xmlns="http://www.w3.org/2000/svg" style="width:100%;height:auto">
							<rect x="10" y="80" width="80" height="36" rx="6" fill="none" stroke="#f59e0b" stroke-width="1.2" opacity="0.7"/>
							<text x="50" y="102" text-anchor="middle" fill="#f59e0b" font-size="11" font-family="inherit">Client</text>
							<rect x="170" y="40" width="80" height="36" rx="6" fill="none" stroke="#6366f1" stroke-width="1.2" opacity="0.7"/>
							<text x="210" y="62" text-anchor="middle" fill="#6366f1" font-size="11" font-family="inherit">WebSocket</text>
							<rect x="170" y="120" width="80" height="36" rx="6" fill="none" stroke="#6366f1" stroke-width="1.2" opacity="0.7"/>
							<text x="210" y="142" text-anchor="middle" fill="#6366f1" font-size="11" font-family="inherit">API Server</text>
							<rect x="330" y="40" width="76" height="36" rx="6" fill="none" stroke="#34d399" stroke-width="1.2" opacity="0.7"/>
							<text x="368" y="62" text-anchor="middle" fill="#34d399" font-size="11" font-family="inherit">Redis</text>
							<rect x="330" y="120" width="76" height="36" rx="6" fill="none" stroke="#34d399" stroke-width="1.2" opacity="0.7"/>
							<text x="368" y="142" text-anchor="middle" fill="#34d399" font-size="11" font-family="inherit">ScyllaDB</text>
							<line x1="90" y1="98" x2="170" y2="58" stroke="rgba(255,255,255,0.2)" stroke-width="1"/>
							<line x1="90" y1="98" x2="170" y2="138" stroke="rgba(255,255,255,0.2)" stroke-width="1"/>
							<line x1="250" y1="58" x2="330" y2="58" stroke="rgba(255,255,255,0.2)" stroke-width="1"/>
							<line x1="250" y1="138" x2="330" y2="138" stroke="rgba(255,255,255,0.2)" stroke-width="1"/>
							<text x="210" y="205" text-anchor="middle" fill="rgba(255,255,255,0.25)" font-size="10" font-family="inherit">✦ Generated by ToraAI from your prompt</text>
						</svg>
					</div>
				</div>
			</div>
			<div class="scene-text">
				<p class="scene-tag" style="color:#f59e0b">Whiteboard</p>
				<h2 class="scene-h2">From prompt to diagram<br />in seconds.</h2>
				<p class="scene-sub">Sketch freely on a shared infinite canvas with live cursors. Or just describe what you want — ToraAI will generate the diagram, flowchart, or wireframe directly onto the board.</p>
			</div>
		</div>

		<!-- Scene 4: Task Management -->
		<div class="scene scene-flip">
			<div class="scene-text scene-text-right">
				<p class="scene-tag" style="color:#ec4899">Task Management</p>
				<h2 class="scene-h2">A full sprint plan<br />from one prompt.</h2>
				<p class="scene-sub">Room-scoped Kanban that lives next to your chat and code. Ask ToraAI to generate a sprint, break down an epic, or surface what's blocking the team — without opening another tool.</p>
			</div>
			<div class="scene-cards scene-cards-left">
				<div class="scard scard-back">
					<div class="scard-chrome"><span class="cdot r"></span><span class="cdot y"></span><span class="cdot g"></span><span class="ctitle">ToraAI — task generation</span></div>
					<div class="scard-body">
						<div class="sc-prompt-bar" style="border-color:rgba(236,72,153,0.3)"><span class="sc-prompt-icon" style="color:#ec4899">✦</span> Build a sprint for v1 auth feature</div>
						<p style="font-size:0.78rem;color:var(--text-3);margin:12px 0 6px">Generated 6 tasks:</p>
						{#each ['Design login & signup screens','Implement JWT auth backend','Add Google OAuth flow','Write auth middleware','Unit tests for token handling','QA pass + staging deploy'] as t, i}
							<div class="sc-gen-task"><span class="sc-gen-num">{i+1}</span>{t}</div>
						{/each}
					</div>
				</div>
				<div class="scard scard-front">
					<div class="scard-chrome"><span class="cdot r"></span><span class="cdot y"></span><span class="cdot g"></span><span class="ctitle">task board</span></div>
					<div class="scard-body">
						<div class="sc-kanban">
							<div class="sc-col"><div class="sc-col-h">Backlog</div><div class="sc-card-item">Add Google OAuth flow</div><div class="sc-card-item">QA pass + staging</div></div>
							<div class="sc-col"><div class="sc-col-h" style="color:#ec4899">In Progress</div><div class="sc-card-item sc-card-active" style="border-color:rgba(236,72,153,0.4)">JWT auth backend<span class="sc-card-badge" style="color:#ec4899;background:rgba(236,72,153,0.12)">AI</span></div></div>
							<div class="sc-col"><div class="sc-col-h" style="color:#34d399">Done</div><div class="sc-card-item sc-card-done">Design screens ✓</div><div class="sc-card-item sc-card-done">Auth middleware ✓</div></div>
						</div>
					</div>
				</div>
			</div>
		</div>

		<!-- Scene 5: Dashboard -->
		<div class="scene">
			<div class="scene-cards">
				<div class="scard scard-back">
					<div class="scard-chrome"><span class="cdot r"></span><span class="cdot y"></span><span class="cdot g"></span><span class="ctitle">rooms</span></div>
					<div class="scard-body">
						{#each [['v1-auth-sprint','5 members','12 tasks'],['design-review','3 members','4 tasks'],['infra-setup','2 members','7 tasks']] as [name, m, t]}
							<div class="sc-room-row"><div class="sc-room-dot"></div><div class="sc-room-info"><span class="sc-room-name">{name}</span><span class="sc-room-meta">{m} · {t}</span></div><span class="sc-room-live">live</span></div>
						{/each}
					</div>
				</div>
				<div class="scard scard-front">
					<div class="scard-chrome"><span class="cdot r"></span><span class="cdot y"></span><span class="cdot g"></span><span class="ctitle">dashboard</span></div>
					<div class="scard-body">
						<div class="sc-stats-row">
							<div class="sc-stat"><span class="sc-stat-v" style="color:#38bdf8">3</span><span class="sc-stat-l">Active rooms</span></div>
							<div class="sc-stat"><span class="sc-stat-v" style="color:#38bdf8">23</span><span class="sc-stat-l">Open tasks</span></div>
							<div class="sc-stat"><span class="sc-stat-v" style="color:#38bdf8">8</span><span class="sc-stat-l">Members online</span></div>
						</div>
						<div class="sc-notice" style="border-left-color:#38bdf8;background:rgba(56,189,248,0.05)">
							<span style="color:#38bdf8;font-weight:800">ToraAI</span> — 2 tasks in <em>v1-auth-sprint</em> are overdue and blocking the deploy.
						</div>
						<div class="sc-notice" style="border-left-color:rgba(255,255,255,0.1);margin-top:8px">
							<span style="color:var(--text-2)">Jordan</span> pinned a message in <em>design-review</em>
						</div>
					</div>
				</div>
			</div>
			<div class="scene-text">
				<p class="scene-tag" style="color:#38bdf8">Dashboard</p>
				<h2 class="scene-h2">Command centre for<br />every session.</h2>
				<p class="scene-sub">See every active room, task, and member at a glance. ToraAI surfaces blockers, summarises activity, and tells you exactly where your attention is needed — so nothing slips through.</p>
			</div>
		</div>

	</section>

	<!-- ▸ Pricing -->
	<section class="pricing-section" id="pricing">
		<div class="pricing-intro">
			<p class="section-label">Pricing</p>
			<h2 class="section-h2">Simple, transparent pricing</h2>
			<p class="section-sub">Start free. Scale when you need to. Every plan includes the full workspace — no features hidden behind paywalls at the core.</p>
		</div>

		<div class="pricing-grid">
			{#each plans as plan}
				<div class="plan-card" class:plan-featured={plan.highlighted}>
					{#if plan.highlighted}
						<div class="plan-badge">Most popular</div>
					{/if}
					<div class="plan-top">
						<p class="plan-name">{plan.name}</p>
						<div class="plan-price-row">
							<span class="plan-price">{plan.price}</span>
							<span class="plan-period">/{plan.period}</span>
						</div>
						<p class="plan-desc">{plan.desc}</p>
					</div>
					<div class="plan-divider"></div>
					<ul class="plan-features">
						{#each plan.features as f}
							<li><span class="plan-check">✓</span>{f}</li>
						{/each}
					</ul>
					<button class="plan-cta" class:plan-cta-primary={plan.highlighted}>
						{plan.cta}
					</button>
				</div>
			{/each}
		</div>
	</section>

	<!-- ▸ Footer -->
	<footer class="footer">
		<div class="footer-wrap">
			<LoginFooter />
		</div>
	</footer>

</div>

<style>
	/* ── Reset ─────────────────────────────────────── */
	:global(html), :global(body) {
		margin: 0; padding: 0;
		background: #07070c;
		-webkit-font-smoothing: antialiased;
	}

	/* ── Design tokens ─────────────────────────────── */
	:global(:root) {
		--page-bg:       #07070c;
		--surface:       #0f0f17;
		--surface-2:     #141420;
		--border:        rgba(255,255,255,0.07);
		--border-hi:     rgba(255,255,255,0.12);
		--text:          #eeeef2;
		--text-2:        #8f8fa0;
		--text-3:        #55556a;
		--accent:        #6366f1;
		--accent-dim:    rgba(99,102,241,0.15);
		--font:          'Plus Jakarta Sans', system-ui, sans-serif;
	}

	/* ── Page ──────────────────────────────────────── */
	.page {
		min-height: 100dvh;
		display: flex;
		flex-direction: column;
		background: var(--page-bg);
		color: var(--text);
		font-family: var(--font);
		overflow-x: hidden;
		position: relative;
	}

	/* ── Background ────────────────────────────────── */
	.bg {
		position: fixed;
		inset: 0;
		pointer-events: none;
		z-index: 0;
	}
	.bg-glow-top {
		position: absolute;
		top: -200px;
		left: 50%;
		transform: translateX(-50%);
		width: 900px;
		height: 700px;
		background: radial-gradient(
			ellipse at 50% 20%,
			rgba(99, 102, 241, 0.12) 0%,
			rgba(99, 102, 241, 0.04) 35%,
			transparent 65%
		);
	}
	.bg-grid {
		position: absolute;
		inset: 0;
		background-image:
			linear-gradient(rgba(255,255,255,0.028) 1px, transparent 1px),
			linear-gradient(90deg, rgba(255,255,255,0.028) 1px, transparent 1px);
		background-size: 72px 72px;
		mask-image: radial-gradient(ellipse 100% 60% at 50% 0%, black, transparent);
	}

	/* ── Banner ────────────────────────────────────── */
	.banner {
		position: relative;
		z-index: 10;
		background: rgba(255,255,255,0.03);
		border-bottom: 1px solid var(--border);
	}
	.banner-inner {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 8px;
		padding: 9px 16px;
		font-size: 0.8rem;
		color: var(--text-2);
		text-decoration: none;
		transition: color 0.15s;
	}
	.banner-inner:hover { color: var(--text); }
	.banner-star { color: #fbbf24; font-size: 0.85rem; }
	.banner-arrow { color: var(--text); font-weight: 600; }

	/* ── Nav ───────────────────────────────────────── */
	.nav {
		position: sticky;
		top: 0;
		z-index: 50;
		background: rgba(7,7,12,0.85);
		backdrop-filter: blur(20px);
		-webkit-backdrop-filter: blur(20px);
		border-bottom: 1px solid var(--border);
	}
	.nav-wrap {
		max-width: 1200px;
		margin: 0 auto;
		padding: 0 32px;
		height: 62px;
		display: flex;
		align-items: center;
		gap: 40px;
	}
	.nav-logo {
		display: flex;
		align-items: center;
		gap: 10px;
		text-decoration: none;
		flex-shrink: 0;
	}
	.nav-logo img { width: 30px; height: 30px; }
	.nav-logo span {
		font-size: 1.2rem;
		font-weight: 800;
		color: var(--text);
		letter-spacing: -0.03em;
	}
	.nav-links {
		display: flex;
		align-items: center;
		gap: 28px;
		flex: 1;
	}
	.nav-links a {
		font-size: 0.875rem;
		color: var(--text-2);
		text-decoration: none;
		font-weight: 500;
		transition: color 0.15s;
	}
	.nav-links a:hover { color: var(--text); }
	.nav-cta {
		font-family: var(--font);
		font-size: 0.825rem;
		font-weight: 700;
		padding: 9px 20px;
		border-radius: 8px;
		background: var(--text);
		color: var(--page-bg);
		border: none;
		cursor: pointer;
		letter-spacing: -0.01em;
		transition: opacity 0.15s, transform 0.15s;
		flex-shrink: 0;
	}
	.nav-cta:hover { opacity: 0.88; transform: translateY(-1px); }

	/* ── Hero ──────────────────────────────────────── */
	.hero {
		position: relative;
		z-index: 1;
		min-height: calc(100dvh - 99px);
		display: flex;
		align-items: center;
		padding: 80px 32px;
	}
	.hero-wrap {
		max-width: 900px;
		margin: 0 auto;
		width: 100%;
		display: flex;
		flex-direction: column;
		align-items: center;
		text-align: center;
		gap: 32px;
	}
	.hero-eyebrow {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		font-size: 0.8rem;
		font-weight: 600;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--text-3);
		animation: rise 0.6s ease both;
	}
	.eyebrow-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--accent);
		box-shadow: 0 0 10px rgba(99,102,241,0.8);
		animation: pulse-glow 2s ease-in-out infinite;
	}
	@keyframes pulse-glow {
		0%, 100% { box-shadow: 0 0 6px rgba(99,102,241,0.7); }
		50%       { box-shadow: 0 0 16px rgba(99,102,241,0.9); }
	}
	.hero-h1 {
		font-size: clamp(3.2rem, 7.5vw, 6.4rem);
		font-weight: 800;
		line-height: 1.06;
		letter-spacing: -0.04em;
		color: var(--text);
		margin: 0;
		animation: rise 0.6s 0.06s ease both;
	}
	.hero-word {
		display: inline-block;
		color: var(--text);
		opacity: 0;
		transform: translateY(14px);
		transition: opacity 0.35s ease, transform 0.35s ease;
		font-style: italic;
		position: relative;
	}
	.hero-word::after {
		content: '';
		position: absolute;
		bottom: 4px;
		left: 0;
		right: 0;
		height: 3px;
		border-radius: 2px;
		background: var(--accent);
		opacity: 0.7;
	}
	.hero-word.visible {
		opacity: 1;
		transform: translateY(0);
	}
	.hero-sub {
		font-size: clamp(1rem, 2vw, 1.2rem);
		line-height: 1.7;
		color: var(--text-2);
		max-width: 600px;
		margin: 0;
		font-weight: 400;
		animation: rise 0.6s 0.12s ease both;
	}
	.hero-ctas {
		display: flex;
		align-items: center;
		gap: 14px;
		flex-wrap: wrap;
		justify-content: center;
		animation: rise 0.6s 0.18s ease both;
	}
	.cta-primary {
		font-family: var(--font);
		font-size: 0.95rem;
		font-weight: 700;
		padding: 14px 30px;
		border-radius: 10px;
		background: var(--text);
		color: var(--page-bg);
		border: none;
		cursor: pointer;
		letter-spacing: -0.02em;
		transition: opacity 0.15s, transform 0.15s;
	}
	.cta-primary:hover { opacity: 0.88; transform: translateY(-1px); }
	.cta-ghost {
		font-family: var(--font);
		font-size: 0.95rem;
		font-weight: 600;
		padding: 14px 24px;
		border-radius: 10px;
		border: 1px solid var(--border-hi);
		color: var(--text-2);
		text-decoration: none;
		transition: background 0.15s, color 0.15s, border-color 0.15s;
		letter-spacing: -0.01em;
	}
	.cta-ghost:hover { background: var(--surface); color: var(--text); border-color: rgba(255,255,255,0.18); }
	.trust-strip {
		display: flex;
		align-items: center;
		gap: 14px;
		font-size: 0.78rem;
		font-weight: 500;
		color: var(--text-3);
		animation: rise 0.6s 0.24s ease both;
		flex-wrap: wrap;
		justify-content: center;
	}
	.trust-dot {
		width: 3px;
		height: 3px;
		border-radius: 50%;
		background: var(--text-3);
	}

	/* ── Room entry section ────────────────────────── */
	.entry-section {
		position: relative;
		z-index: 1;
		padding: 0 32px 100px;
	}
	.entry-wrap {
		max-width: 640px;
		margin: 0 auto;
	}
	.entry-label {
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.1em;
		text-transform: uppercase;
		color: var(--text-3);
		text-align: center;
		margin: 0 0 20px;
	}
	.entry-card {
		background: var(--surface);
		border: 1px solid var(--border-hi);
		border-radius: 18px;
		overflow: hidden;
		box-shadow:
			0 0 0 1px rgba(0,0,0,0.6),
			0 40px 80px rgba(0,0,0,0.5),
			0 0 60px rgba(99,102,241,0.04);
	}
	.card-chrome {
		display: flex;
		align-items: center;
		gap: 7px;
		padding: 13px 18px;
		background: var(--surface-2);
		border-bottom: 1px solid var(--border);
	}
	.chrome-dot {
		width: 11px;
		height: 11px;
		border-radius: 50%;
		opacity: 0.85;
	}
	.chrome-title {
		margin-left: 4px;
		font-size: 0.76rem;
		font-weight: 600;
		color: var(--text-3);
		letter-spacing: 0.02em;
	}
	.card-body {
		padding: 28px;
		display: flex;
		flex-direction: column;
		gap: 18px;
	}
	.inputs-row {
		display: flex;
		align-items: flex-start;
		gap: 12px;
	}
	.field {
		display: flex;
		flex-direction: column;
		gap: 7px;
		flex: 1;
		min-width: 0;
	}
	.field label {
		font-size: 0.72rem;
		font-weight: 700;
		color: var(--text-3);
		letter-spacing: 0.07em;
		text-transform: uppercase;
	}
	input {
		background: rgba(255,255,255,0.04);
		color: var(--text);
		border: 1px solid var(--border-hi);
		border-radius: 9px;
		padding: 11px 14px;
		font-size: 0.875rem;
		font-family: var(--font);
		font-weight: 400;
		width: 100%;
		box-sizing: border-box;
		transition: border-color 0.15s, box-shadow 0.15s;
	}
	input::placeholder { color: var(--text-3); }
	input:focus {
		outline: none;
		border-color: rgba(99,102,241,0.5);
		box-shadow: 0 0 0 3px rgba(99,102,241,0.1);
	}
	.field-err { font-size: 0.72rem; color: #f87171; }
	.or-divider {
		align-self: center;
		margin-top: 25px;
		font-size: 0.72rem;
		font-weight: 700;
		color: var(--text-3);
		flex-shrink: 0;
		letter-spacing: 0.04em;
	}
	.err-banner {
		font-size: 0.825rem;
		color: #fca5a5;
		background: rgba(239,68,68,0.08);
		border: 1px solid rgba(239,68,68,0.2);
		border-radius: 8px;
		padding: 11px 14px;
	}
	/* Advanced toggle */
	.adv-btn {
		display: inline-flex;
		align-items: center;
		gap: 7px;
		align-self: flex-start;
		background: none;
		border: 1px solid var(--border);
		border-radius: 7px;
		padding: 7px 13px;
		font-size: 0.775rem;
		font-weight: 600;
		color: var(--text-3);
		cursor: pointer;
		font-family: var(--font);
		transition: border-color 0.15s, color 0.15s;
	}
	.adv-btn:hover { border-color: var(--border-hi); color: var(--text-2); }
	.adv-chevron { width: 10px; height: 6px; transition: transform 0.2s; }
	.adv-btn.open .adv-chevron { transform: rotate(180deg); }
	.adv-panel {
		border: 1px solid var(--border);
		border-radius: 12px;
		padding: 18px;
		display: flex;
		flex-direction: column;
		gap: 16px;
		background: rgba(255,255,255,0.018);
	}
	.adv-row { display: flex; gap: 12px; }
	.adv-row .field { flex: 1 1 50%; }
	.adv-toggles { display: flex; gap: 12px; }
	.adv-toggles .field { flex: 1 1 50%; gap: 8px; }
	.tgl-label {
		font-size: 0.72rem;
		font-weight: 700;
		color: var(--text-3);
		letter-spacing: 0.07em;
		text-transform: uppercase;
	}
	.seg-ctrl {
		display: inline-flex;
		border: 1px solid var(--border-hi);
		border-radius: 999px;
		padding: 2px;
		background: rgba(255,255,255,0.02);
	}
	.seg-ctrl button {
		border: none;
		background: transparent;
		color: var(--text-3);
		font-size: 0.775rem;
		font-weight: 700;
		border-radius: 999px;
		padding: 5px 14px;
		cursor: pointer;
		font-family: var(--font);
		transition: background 0.15s, color 0.15s;
	}
	.seg-ctrl button.on { background: var(--text); color: var(--page-bg); }
	.seg-ctrl button:disabled { cursor: not-allowed; opacity: 0.35; }
	.field small, .tgl-detail {
		font-size: 0.72rem;
		color: var(--text-3);
		line-height: 1.5;
	}
	.tgl-detail { margin: 0; }
	.tgl-more {
		border: none; background: none;
		font-size: 0.72rem; font-weight: 700;
		text-decoration: underline;
		color: var(--text-2); cursor: pointer; padding: 0;
		font-family: var(--font);
	}
	.ts-slot { position: absolute; width: 0; height: 0; overflow: hidden; opacity: 0; pointer-events: none; }
	/* Card actions */
	.card-actions {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 10px;
	}
	.act-create, .act-join {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 7px;
		padding: 13px;
		border-radius: 10px;
		font-size: 0.875rem;
		font-weight: 700;
		cursor: pointer;
		font-family: var(--font);
		letter-spacing: -0.02em;
		transition: opacity 0.15s, transform 0.15s;
		border: none;
	}
	.act-create { background: var(--text); color: var(--page-bg); }
	.act-create:hover:not(:disabled) { opacity: 0.88; transform: translateY(-1px); }
	.act-join { background: transparent; color: var(--text-2); border: 1px solid var(--border-hi); }
	.act-join:hover:not(:disabled) { background: var(--surface-2); color: var(--text); }
	.act-create:disabled, .act-join:disabled { opacity: 0.35; cursor: not-allowed; transform: none; }
	.btn-spin {
		width: 13px; height: 13px;
		border: 2px solid rgba(7,7,12,0.3);
		border-top-color: var(--page-bg);
		border-radius: 50%;
		animation: spin 0.65s linear infinite;
		flex-shrink: 0;
	}
	.card-hint {
		text-align: center;
		font-size: 0.725rem;
		color: var(--text-3);
		margin: 0;
	}

	/* ── Tools — cinematic scenes ────────────────────── */
	.tools-section {
		position: relative;
		z-index: 1;
	}

	/* Each scene is full-width, tall, overflow hidden so cards can bleed */
	.scene {
		position: relative;
		min-height: 680px;
		overflow: hidden;
		border-top: 1px solid var(--border);
		display: flex;
		align-items: flex-end;
		padding: 0 0 72px 80px;
	}
	.scene-flip {
		padding: 0 80px 72px 0;
		justify-content: flex-end;
	}

	/* Bold claim text — bottom-left like Thesys */
	.scene-text {
		position: relative;
		z-index: 2;
		max-width: 480px;
		flex-shrink: 0;
	}
	.scene-text-right {
		text-align: right;
	}
	.scene-tag {
		font-size: 0.7rem;
		font-weight: 800;
		letter-spacing: 0.1em;
		text-transform: uppercase;
		color: #6366f1;
		margin: 0 0 16px;
	}
	.scene-h2 {
		font-size: clamp(2rem, 3.5vw, 3rem);
		font-weight: 800;
		letter-spacing: -0.035em;
		line-height: 1.1;
		color: var(--text);
		margin: 0 0 18px;
	}
	.scene-sub {
		font-size: 0.95rem;
		color: var(--text-2);
		line-height: 1.7;
		margin: 0;
		font-weight: 400;
	}

	/* Cards cluster — positioned to fill the right side, bleeding off */
	.scene-cards {
		position: absolute;
		top: 48px;
		right: -32px;
		width: 62%;
		height: calc(100% - 48px);
		pointer-events: none;
	}
	.scene-cards-left {
		right: auto;
		left: -32px;
	}

	/* Individual floating dark card */
	.scard {
		position: absolute;
		background: #141418;
		border: 1px solid rgba(255,255,255,0.09);
		border-radius: 14px;
		overflow: hidden;
		box-shadow: 0 24px 64px rgba(0,0,0,0.7), 0 0 0 0.5px rgba(255,255,255,0.05);
	}
	.scard-back {
		width: 58%;
		top: 0;
		left: 0;
		opacity: 0.72;
		transform: translateY(24px) scale(0.97);
		z-index: 1;
	}
	.scard-front {
		width: 70%;
		top: 60px;
		right: 0;
		z-index: 2;
	}

	/* Code canvas specific positioning */
	.scard-code-back {
		width: 52%;
		top: auto;
		bottom: 0;
		left: 8%;
		transform: none;
		opacity: 0.7;
	}
	.scard-code-front {
		width: 72%;
		top: 0;
		right: 0;
	}

	/* Board specific */
	.scard-board-back {
		width: 55%;
		top: 0;
		left: 0;
		transform: translateY(20px) scale(0.96);
	}
	.scard-board-front {
		width: 72%;
		top: 48px;
		right: 0;
	}

	/* Card chrome bar */
	.scard-chrome {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 10px 14px;
		background: rgba(255,255,255,0.025);
		border-bottom: 1px solid rgba(255,255,255,0.06);
	}
	.cdot { width: 10px; height: 10px; border-radius: 50%; opacity: 0.85; flex-shrink: 0; }
	.cdot.r { background: #ff5f57; }
	.cdot.y { background: #febc2e; }
	.cdot.g { background: #28c840; }
	.ctitle {
		margin-left: 4px;
		font-size: 0.72rem;
		font-weight: 600;
		color: rgba(255,255,255,0.3);
		letter-spacing: 0.02em;
	}
	.sc-live-badge {
		margin-left: auto;
		font-size: 0.65rem;
		font-weight: 700;
		color: #34d399;
		background: rgba(52,211,153,0.12);
		padding: 2px 8px;
		border-radius: 999px;
	}

	/* Card body */
	.scard-body {
		padding: 18px 20px;
		display: flex;
		flex-direction: column;
		gap: 10px;
	}

	/* ── Chat card internals ── */
	.sc-msg-row {
		display: flex;
		align-items: flex-end;
		gap: 8px;
	}
	.sc-me { flex-direction: row-reverse; }
	.sc-av {
		width: 26px; height: 26px;
		border-radius: 50%;
		font-size: 0.65rem; font-weight: 800;
		display: flex; align-items: center; justify-content: center;
		flex-shrink: 0;
	}
	.sc-bubble {
		max-width: 78%;
		padding: 8px 12px;
		border-radius: 10px;
		font-size: 0.78rem;
		line-height: 1.5;
	}
	.sc-other-b { background: rgba(255,255,255,0.05); color: var(--text-2); border-radius: 3px 10px 10px 10px; }
	.sc-me-b    { background: rgba(255,255,255,0.09); color: var(--text); border-radius: 10px 3px 10px 10px; }
	.sc-typing {
		display: flex; gap: 4px; align-items: center; padding: 4px 2px;
	}
	.sc-typing span {
		width: 5px; height: 5px; border-radius: 50%;
		background: rgba(255,255,255,0.25);
		animation: blink 1.2s ease-in-out infinite;
	}
	.sc-typing span:nth-child(2) { animation-delay: 0.2s; }
	.sc-typing span:nth-child(3) { animation-delay: 0.4s; }
	@keyframes blink { 0%,80%,100%{opacity:0.25} 40%{opacity:1} }
	.sc-ai-header { display: flex; align-items: center; gap: 8px; }
	.sc-ai-pill {
		display: inline-flex; align-items: center;
		font-size: 0.68rem; font-weight: 800;
		background: rgba(99,102,241,0.2); color: #a5b4fc;
		padding: 3px 9px; border-radius: 999px;
		letter-spacing: 0.03em;
	}
	.sc-ai-ctx { font-size: 0.72rem; color: var(--text-3); }
	.sc-ai-para { font-size: 0.82rem; color: var(--text-2); margin: 0; line-height: 1.5; }
	.sc-ai-para strong { color: var(--text); }
	.sc-ai-item {
		display: flex; gap: 12px; align-items: flex-start;
		font-size: 0.78rem; color: var(--text-2); line-height: 1.5;
	}
	.sc-ai-num {
		font-size: 0.65rem; font-weight: 800; color: var(--text-3);
		padding-top: 1px; flex-shrink: 0;
	}
	.sc-ai-item code { font-family: ui-monospace, monospace; font-size: 0.72rem; background: rgba(255,255,255,0.07); border-radius: 3px; padding: 1px 5px; color: #c7d2fe; }
	.sc-ai-actions { display: flex; gap: 8px; margin-top: 4px; }
	.sc-ai-btn {
		font-family: var(--font); font-size: 0.72rem; font-weight: 700;
		padding: 6px 14px; border-radius: 7px;
		background: var(--text); color: var(--page-bg);
		border: none; cursor: pointer;
	}
	.sc-ai-btn-ghost {
		background: transparent; color: var(--text-2);
		border: 1px solid rgba(255,255,255,0.1);
	}

	/* ── Code card internals ── */
	.sc-terminal { font-family: ui-monospace, monospace; font-size: 0.75rem; line-height: 1.9; }
	.term-line { display: flex; gap: 8px; }
	.term-prompt { color: rgba(255,255,255,0.3); }
	.term-cmd { color: var(--text); }
	.term-out { color: var(--text-3); padding-left: 16px; }
	.term-ok  { color: #34d399; padding-left: 16px; }
	.term-err { color: #f87171; padding-left: 16px; }
	.term-cursor { color: rgba(255,255,255,0.6); animation: blink-cur 1s step-end infinite; }
	@keyframes blink-cur { 0%,100%{opacity:1} 50%{opacity:0} }
	.sc-code-body { font-family: 'Geist Mono', ui-monospace, monospace; font-size: 0.76rem; line-height: 1.85; padding-bottom: 0; }
	.cline { display: flex; gap: 14px; }
	.cline-ai { background: rgba(52,211,153,0.06); border-radius: 3px; }
	.ln   { color: rgba(255,255,255,0.18); min-width: 14px; }
	.ci   { width: 18px; flex-shrink: 0; display: inline-block; }
	.ck   { color: #818cf8; }
	.cf   { color: #34d399; }
	.cv   { color: #f3c67e; }
	.cp   { color: rgba(255,255,255,0.55); }
	.sc-ghost { color: rgba(52,211,153,0.5); font-style: italic; }
	.sc-ai-suggestion-bar {
		border-top: 1px solid rgba(255,255,255,0.06);
		padding: 10px 20px;
		font-size: 0.73rem; color: #6ee7b7;
		background: rgba(52,211,153,0.04);
		display: flex; align-items: center; gap: 8px;
	}
	.sc-prompt-bar {
		display: flex; align-items: center; gap: 10px;
		background: rgba(255,255,255,0.04);
		border: 1px solid rgba(255,255,255,0.08);
		border-radius: 8px;
		padding: 9px 13px;
		font-size: 0.8rem; color: var(--text-2);
	}
	.sc-prompt-icon { color: #6366f1; font-size: 0.85rem; }

	/* ── Task card internals ── */
	.sc-gen-task {
		display: flex; align-items: center; gap: 10px;
		font-size: 0.76rem; color: var(--text-2); line-height: 1.4; padding: 2px 0;
	}
	.sc-gen-num {
		font-size: 0.62rem; font-weight: 800; color: var(--text-3);
		min-width: 16px;
	}
	.sc-kanban { display: grid; grid-template-columns: repeat(3,1fr); gap: 10px; }
	.sc-col { display: flex; flex-direction: column; gap: 7px; }
	.sc-col-h { font-size: 0.65rem; font-weight: 800; letter-spacing: 0.06em; text-transform: uppercase; color: var(--text-3); margin-bottom: 3px; }
	.sc-card-item {
		background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.07);
		border-radius: 7px; padding: 8px 9px;
		font-size: 0.73rem; color: var(--text-2); line-height: 1.4;
	}
	.sc-card-active { background: rgba(236,72,153,0.06); color: var(--text); display: flex; align-items: center; justify-content: space-between; }
	.sc-card-done { opacity: 0.4; text-decoration: line-through; }
	.sc-card-badge { font-size: 0.6rem; font-weight: 800; padding: 2px 5px; border-radius: 4px; }

	/* ── Dashboard card internals ── */
	.sc-stats-row { display: grid; grid-template-columns: repeat(3,1fr); gap: 10px; }
	.sc-stat { display: flex; flex-direction: column; gap: 3px; background: rgba(255,255,255,0.02); border: 1px solid rgba(255,255,255,0.06); border-radius: 8px; padding: 12px 14px; }
	.sc-stat-v { font-size: 1.6rem; font-weight: 800; letter-spacing: -0.04em; line-height: 1; }
	.sc-stat-l { font-size: 0.7rem; color: var(--text-3); font-weight: 500; }
	.sc-notice {
		border-left: 2px solid; border-radius: 0 7px 7px 0;
		padding: 10px 12px; font-size: 0.78rem;
		color: var(--text-2); line-height: 1.5;
	}
	.sc-notice em { font-style: normal; color: var(--text); }
	.sc-room-row { display: flex; align-items: center; gap: 10px; padding: 8px 0; border-bottom: 1px solid rgba(255,255,255,0.05); }
	.sc-room-row:last-child { border-bottom: none; }
	.sc-room-dot { width: 7px; height: 7px; border-radius: 50%; background: #34d399; flex-shrink: 0; }
	.sc-room-info { display: flex; flex-direction: column; gap: 2px; flex: 1; }
	.sc-room-name { font-size: 0.8rem; font-weight: 600; color: var(--text); font-family: ui-monospace, monospace; }
	.sc-room-meta { font-size: 0.7rem; color: var(--text-3); }
	.sc-room-live { font-size: 0.62rem; font-weight: 800; color: #34d399; background: rgba(52,211,153,0.1); padding: 2px 8px; border-radius: 999px; }

	/* ── Pricing section ──────────────────────────────── */
	.pricing-section {
		position: relative;
		z-index: 1;
		border-top: 1px solid var(--border);
		padding: 100px 32px 120px;
	}
	.pricing-intro {
		max-width: 640px;
		margin: 0 auto 64px;
		text-align: center;
	}
	.pricing-grid {
		max-width: 1200px;
		margin: 0 auto;
		display: grid;
		grid-template-columns: repeat(4, 1fr);
		gap: 2px;
		background: var(--border);
		border: 1px solid var(--border);
		border-radius: 20px;
		overflow: hidden;
	}
	.plan-card {
		background: var(--surface);
		padding: 36px 32px 40px;
		display: flex;
		flex-direction: column;
		gap: 0;
		position: relative;
		transition: background 0.2s;
	}
	.plan-card:hover { background: var(--surface-2); }
	.plan-featured {
		background: var(--surface-2);
		box-shadow: inset 0 0 0 1px rgba(99,102,241,0.3);
	}
	.plan-badge {
		position: absolute;
		top: -1px;
		left: 50%;
		transform: translateX(-50%);
		font-size: 0.68rem;
		font-weight: 800;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		background: var(--accent);
		color: #fff;
		padding: 4px 12px;
		border-radius: 0 0 8px 8px;
	}
	.plan-top {
		margin-bottom: 24px;
	}
	.plan-name {
		font-size: 0.75rem;
		font-weight: 800;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--text-3);
		margin: 0 0 16px;
	}
	.plan-price-row {
		display: flex;
		align-items: baseline;
		gap: 4px;
		margin-bottom: 10px;
	}
	.plan-price {
		font-size: 2.6rem;
		font-weight: 800;
		color: var(--text);
		letter-spacing: -0.04em;
		line-height: 1;
	}
	.plan-period {
		font-size: 0.8rem;
		color: var(--text-3);
		font-weight: 500;
	}
	.plan-desc {
		font-size: 0.83rem;
		color: var(--text-2);
		line-height: 1.5;
		margin: 0;
	}
	.plan-divider {
		height: 1px;
		background: var(--border);
		margin-bottom: 22px;
	}
	.plan-features {
		list-style: none;
		padding: 0;
		margin: 0 0 28px;
		display: flex;
		flex-direction: column;
		gap: 11px;
		flex: 1;
	}
	.plan-features li {
		display: flex;
		align-items: flex-start;
		gap: 9px;
		font-size: 0.83rem;
		color: var(--text-2);
		line-height: 1.45;
	}
	.plan-check {
		color: var(--text-3);
		font-weight: 800;
		font-size: 0.75rem;
		margin-top: 1px;
		flex-shrink: 0;
	}
	.plan-featured .plan-check { color: var(--accent); }
	.plan-cta {
		font-family: var(--font);
		width: 100%;
		padding: 12px;
		border-radius: 10px;
		font-size: 0.875rem;
		font-weight: 700;
		letter-spacing: -0.02em;
		cursor: pointer;
		transition: opacity 0.15s, transform 0.15s;
		background: transparent;
		border: 1px solid var(--border-hi);
		color: var(--text-2);
	}
	.plan-cta:hover { background: rgba(255,255,255,0.06); color: var(--text); transform: translateY(-1px); }
	.plan-cta-primary {
		background: var(--accent);
		border-color: var(--accent);
		color: #fff;
	}
	.plan-cta-primary:hover { opacity: 0.88; background: var(--accent); }

	/* ── Footer ────────────────────────────────────── */
	.footer {
		position: relative;
		z-index: 1;
		border-top: 1px solid var(--border);
		background: var(--surface);
		padding: 28px 0;
	}
	.footer-wrap {
		max-width: 1200px;
		margin: 0 auto;
		padding: 0 32px;
	}

	/* ── Revive overlay ────────────────────────────── */
	.revive-overlay {
		position: fixed; inset: 0; z-index: 80;
		display: flex; align-items: center; justify-content: center;
		background: rgba(7,7,12,0.8);
		backdrop-filter: blur(8px);
	}
	.revive-box {
		width: min(90vw, 26rem);
		border: 1px dashed var(--border-hi);
		border-radius: 18px;
		background: var(--surface);
		padding: 2.2rem 2.8rem;
		text-align: center;
	}
	.revive-icon { font-size: 1.8rem; color: var(--text-2); margin-bottom: 0.6rem; }
	.revive-box strong { display: block; font-size: 0.95rem; font-weight: 700; margin-bottom: 0.5rem; }
	.revive-box p { font-size: 0.82rem; color: var(--text-2); margin: 0.3rem 0 0; }
	.revive-box code { font-family: ui-monospace, monospace; font-size: 0.78rem; background: rgba(255,255,255,0.07); border-radius: 4px; padding: 0.1rem 0.3rem; color: var(--text); }
	.revive-loading { color: var(--text) !important; font-weight: 700; }

	/* ── Animations ────────────────────────────────── */
	@keyframes rise {
		from { opacity: 0; transform: translateY(22px); }
		to   { opacity: 1; transform: translateY(0); }
	}
	@keyframes spin { to { transform: rotate(360deg); } }

	/* ── Responsive ────────────────────────────────── */
	@media (max-width: 900px) {
		.pricing-grid { grid-template-columns: repeat(2, 1fr); }
		.scene {
			flex-direction: column;
			align-items: flex-start;
			padding: 320px 24px 48px;
			min-height: auto;
		}
		.scene-flip { padding: 320px 24px 48px; justify-content: flex-start; }
		.scene-text-right { text-align: left; }
		.scene-cards { width: 90%; right: 0; }
		.scene-cards-left { left: 0; right: auto; }
		.scard-back { width: 54%; }
		.scard-front { width: 80%; }
		.scard-code-front { width: 85%; }
		.scard-board-front { width: 85%; }
	}
	@media (max-width: 700px) {
		.nav-links { display: none; }
		.nav-wrap { padding: 0 20px; }
		.hero { padding: 60px 20px; min-height: auto; padding-top: 80px; }
		.entry-section { padding: 0 20px 80px; }
		.pricing-section { padding: 80px 20px 100px; }
		.pricing-intro { padding: 0 0 40px; }
		.pricing-grid { grid-template-columns: 1fr; background: none; border: none; gap: 12px; }
		.plan-card { border: 1px solid var(--border); border-radius: 16px; }
		.plan-featured { box-shadow: inset 0 0 0 1px rgba(99,102,241,0.3), 0 0 0 1px rgba(99,102,241,0.2); }
		.inputs-row { flex-wrap: wrap; }
		.or-divider { width: 100%; text-align: center; margin: 0; }
		.adv-row { flex-wrap: wrap; }
		.adv-row .field { flex-basis: 100%; }
		.adv-toggles { flex-wrap: wrap; }
		.adv-toggles .field { flex-basis: 100%; }
		.scene { padding: 280px 20px 40px; }
		.scene-flip { padding: 280px 20px 40px; }
		.sc-kanban { grid-template-columns: 1fr; }
		.sc-stats-row { grid-template-columns: repeat(3,1fr); }
	}
</style>