<script lang="ts">
	import { browser } from '$app/environment';
	import { createEventDispatcher } from 'svelte';
	import toraLogo from '$lib/assets/tora-logo.svg';
	import { normalizeUsernameInput } from '$lib/utils/homeJoin';
	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = (() => {
		const configured = API_BASE_RAW?.trim();
		if (configured) {
			return configured;
		}
		if (!browser) {
			return 'http://localhost:8080';
		}
		const protocol = window.location.protocol === 'https:' ? 'https:' : 'http:';
		const host = window.location.hostname;
		if (host === 'localhost' || host === '127.0.0.1') {
			return `${protocol}//${host}:8080`;
		}
		return window.location.origin;
	})();

	export let isOpen: boolean = false;
	let isRegisterMode: boolean = false;

	let email = '';
	let password = '';
	let username = '';
	let error = '';
	let isLoading = false;
	const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

	const dispatch = createEventDispatcher();

	$: normalizedEmail = email.trim().toLowerCase();
	$: normalizedPassword = password.trim();
	$: normalizedUsername = normalizeUsernameInput(username).toLowerCase().slice(0, 32);
	$: hasValidEmail = emailRegex.test(normalizedEmail);
	$: hasPassword = normalizedPassword.length > 0;
	$: hasSignupUsername = normalizedUsername.length > 0;
	$: canSubmit = hasValidEmail && hasPassword && (!isRegisterMode || hasSignupUsername);
	$: authSubtitle = isRegisterMode
		? 'Set your handle and start secure, disappearing conversations in seconds.'
		: 'Sign in to mint a fresh session token and jump straight into your rooms.';
	$: switchPrompt = isRegisterMode
		? 'Prefer quick access with just email + password?'
		: 'Want to lock in a custom handle for this session?';
	$: submitLabel = isLoading
		? 'Processing...'
		: isRegisterMode
			? 'Create Account'
			: 'Start Session';

	function clientLog(_event: string, _payload?: unknown) {
		// Auth modal debug logs intentionally disabled.
	}

	function close() {
		dispatch('close');
		error = '';
	}

	async function handleAuth() {
		if (!canSubmit) {
			error = isRegisterMode
				? 'Use a valid email, password, and username to continue'
				: 'Use a valid email and password to continue';
			return;
		}

		isLoading = true;
		error = '';

		const endpoint = isRegisterMode ? '/api/auth/signup' : '/api/auth/login';
		const payload: Record<string, string> = {
			email: normalizedEmail,
			password: normalizedPassword
		};
		if (isRegisterMode) {
			payload.username = normalizedUsername;
		}

		try {
			clientLog('api-auth-request', {
				mode: isRegisterMode ? 'signup' : 'login',
				endpoint,
				email: payload.email,
				username: payload.username
			});
			const res = await fetch(`${API_BASE}${endpoint}`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				credentials: 'include',
				body: JSON.stringify(payload)
			});

			const data = await res.json().catch(() => ({}));
			clientLog('api-auth-response', { endpoint, status: res.status, ok: res.ok, data });

			if (!res.ok) throw new Error(data.error || data.message || 'Authentication failed');

			dispatch('success', { user: data.user, token: data.token });
			close();
		} catch (e: any) {
			clientLog('api-auth-error', { endpoint, error: e?.message ?? String(e) });
			error = e.message;
		} finally {
			isLoading = false;
		}
	}

	function handleGoogleLogin() {
		if (!browser || isLoading) {
			clientLog('google-oauth-click-ignored', {
				reason: !browser ? 'not-in-browser' : 'request-in-flight',
				isLoading
			});
			return;
		}
		const target = `${API_BASE}/api/auth/google`;
		clientLog('google-oauth-redirect-start', { target });
		window.location.href = target;
	}
</script>

{#if isOpen}
	<div
		class="modal-backdrop"
		role="button"
		tabindex="0"
		aria-label="Close auth modal"
		on:click|self={close}
		on:keydown={(event) => {
			if (event.key === 'Escape' || event.key === 'Enter' || event.key === ' ') {
				event.preventDefault();
				close();
			}
		}}
	>
		<div class="auth-modal">
			<div class="auth-brand">
				<img src={toraLogo} alt="Tora logo" class="brand-logo" />
				<h2>Welcome to Tora</h2>
			</div>
			<p class="auth-subtitle">{authSubtitle}</p>

			{#if error}
				<div class="error-banner">{error}</div>
			{/if}

			<div class="form-group">
				<label for="email">Email</label>
				<input
					type="email"
					id="email"
					bind:value={email}
					placeholder="you@example.com"
					autocomplete="email"
					inputmode="email"
					maxlength="254"
					required
				/>
				<small class="input-helper">Required. Sent lowercase after trimming spaces.</small>
				{#if normalizedEmail !== '' && !hasValidEmail}
					<small class="input-helper invalid">Use a valid address like you@example.com.</small>
				{/if}
			</div>

			{#if isRegisterMode}
				<div class="form-group">
					<label for="username">Username</label>
					<input
						type="text"
						id="username"
						bind:value={username}
						placeholder="CoolUser123"
						autocomplete="username"
						maxlength="32"
						pattern="[A-Za-z0-9 _-]+"
						required={isRegisterMode}
					/>
					<small class="input-helper">
						Required for signup. Must be unique. Saved lowercase with underscores.
					</small>
					{#if username.trim() !== '' && normalizedUsername !== username.trim().toLowerCase()}
						<small class="input-helper">Will be normalized to {normalizedUsername}.</small>
					{/if}
					{#if username.trim() !== '' && !hasSignupUsername}
						<small class="input-helper invalid">
							Username must include at least one letter or number.
						</small>
					{/if}
				</div>
			{/if}

			<div class="form-group">
				<label for="password">Password</label>
				<input
					type="password"
					id="password"
					bind:value={password}
					placeholder="Enter your password"
					autocomplete={isRegisterMode ? 'new-password' : 'current-password'}
					minlength="1"
					maxlength="128"
					required
				/>
				<small class="input-helper">Required. Current backend validates presence.</small>
			</div>

			<button class="btn-primary" on:click={handleAuth} disabled={isLoading || !canSubmit}>
				{submitLabel}
			</button>

			<div class="divider">OR</div>

			<button class="btn-google" type="button" on:click={handleGoogleLogin} disabled={isLoading}>
				Continue with Google
			</button>
			<small class="input-helper muted">Google login uses your configured OAuth redirect.</small>

			<p class="switch-mode">
				{switchPrompt}
				<button
					class="link-btn"
					on:click={() => {
						isRegisterMode = !isRegisterMode;
						error = '';
					}}
				>
					{isRegisterMode ? 'Switch to Log In' : 'Switch to Sign Up'}
				</button>
			</p>
		</div>
	</div>
{/if}

<style>
	.modal-backdrop {
		position: fixed;
		inset: 0;
		background:
			radial-gradient(circle at 20% 20%, rgba(0, 240, 255, 0.15), transparent 40%),
			radial-gradient(circle at 80% 10%, rgba(112, 0, 255, 0.18), transparent 35%),
			rgba(3, 8, 20, 0.72);
		display: flex;
		justify-content: center;
		align-items: center;
		z-index: 1000;
	}

	.auth-modal {
		position: fixed;
		top: 50%;
		left: 50%;
		transform: translate(-50%, -50%);
		background: rgba(25, 25, 30, 0.65);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
		border: 1px solid rgba(255, 255, 255, 0.1);
		box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5);
		padding: 2.5rem;
		border-radius: 16px;
		width: min(420px, calc(100vw - 2rem));
		max-width: 100%;
		color: white;
		text-align: center;
	}

	.auth-modal h2 {
		margin: 0;
		font-size: 1.6rem;
	}

	.auth-brand {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 0.7rem;
	}

	.brand-logo {
		width: 34px;
		height: 34px;
	}

	.auth-subtitle {
		margin: 0.8rem 0 1.2rem;
		color: rgba(255, 255, 255, 0.82);
		font-size: 0.94rem;
		line-height: 1.45;
	}

	.form-group {
		margin-bottom: 1rem;
		text-align: left;
	}

	.form-group label {
		display: block;
		margin-bottom: 0.5rem;
		font-weight: 600;
		color: rgba(255, 255, 255, 0.88);
	}

	.auth-modal input {
		width: 100%;
		padding: 12px 16px;
		margin: 10px 0;
		background: rgba(0, 0, 0, 0.2);
		border: 1px solid rgba(255, 255, 255, 0.1);
		border-radius: 8px;
		color: white;
		font-size: 1rem;
		transition: all 0.2s ease;
	}

	.input-helper {
		display: block;
		margin-top: 0.25rem;
		font-size: 0.78rem;
		color: rgba(255, 255, 255, 0.72);
		line-height: 1.35;
	}

	.input-helper.invalid {
		color: #fca5a5;
	}

	.input-helper.muted {
		margin-top: 0.6rem;
		text-align: center;
	}

	.auth-modal input::placeholder {
		color: rgba(255, 255, 255, 0.6);
	}

	.auth-modal input:focus {
		outline: none;
		border-color: #00f0ff;
		background: rgba(0, 0, 0, 0.4);
	}

	.auth-modal button {
		width: 100%;
		padding: 12px;
		margin-top: 15px;
		background: linear-gradient(135deg, #00f0ff, #7000ff);
		border: none;
		border-radius: 8px;
		color: white;
		font-weight: 600;
		font-size: 1rem;
		cursor: pointer;
		transition:
			opacity 0.2s ease,
			transform 0.1s ease;
	}

	.auth-modal button:hover:not(:disabled) {
		opacity: 0.9;
	}

	.auth-modal button:active:not(:disabled) {
		transform: scale(0.98);
	}

	.auth-modal button:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	.btn-google {
		background: rgba(255, 255, 255, 0.08) !important;
		border: 1px solid rgba(255, 255, 255, 0.2) !important;
		color: rgba(255, 255, 255, 0.78) !important;
	}

	.error-banner {
		background: rgba(248, 113, 113, 0.15);
		color: #fecaca;
		padding: 0.65rem;
		margin-bottom: 1rem;
		border: 1px solid rgba(248, 113, 113, 0.3);
		border-radius: 8px;
		text-align: left;
	}

	.divider {
		text-align: center;
		margin: 1rem 0;
		color: rgba(255, 255, 255, 0.7);
		font-size: 0.9rem;
	}

	.switch-mode {
		text-align: center;
		margin-top: 1rem;
		font-size: 0.9rem;
		color: rgba(255, 255, 255, 0.85);
	}

	.switch-mode .link-btn {
		width: auto;
		margin-top: 0;
		padding: 0;
		background: none;
		border: none;
		color: #67e8f9;
		text-decoration: underline;
		cursor: pointer;
		font-size: 0.9rem;
		font-weight: 600;
	}
</style>
