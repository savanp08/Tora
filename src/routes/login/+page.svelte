<script lang="ts">
	import { browser } from '$app/environment';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import MonochromeRoomBackground from '$lib/components/background/MonochromeRoomBackground.svelte';
	import { login } from '$lib/stores/auth';

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';

	let email = '';
	let password = '';
	let username = '';
	let isRegisterMode = false;
	let isSubmitting = false;
	let authError = '';

	$: normalizedEmail = email.trim().toLowerCase();
	$: normalizedUsername = normalizeAccountUsername(username);
	$: canSubmit = normalizedEmail.length > 0 && password.trim().length > 0 && (!isRegisterMode || normalizedUsername.length > 0);
	$: requestedRedirect = resolveSafeRedirect($page.url.searchParams.get('redirect'));

	function resolveSafeRedirect(rawPath: string | null) {
		if (!rawPath) {
			return '';
		}
		const trimmed = rawPath.trim();
		if (!trimmed.startsWith('/') || trimmed.startsWith('//')) {
			return '';
		}
		return trimmed;
	}

	function normalizeAccountUsername(value: string) {
		return value
			.trim()
			.toLowerCase()
			.replace(/[^a-z0-9\s_-]/g, '')
			.replace(/[\s-]+/g, '_')
			.replace(/_+/g, '_')
			.replace(/^_+|_+$/g, '')
			.slice(0, 32);
	}

	function buildFallbackName(sourceEmail: string) {
		const prefix = sourceEmail.split('@')[0]?.trim() || 'user';
		return prefix
			.split(/[._-]+/)
			.filter(Boolean)
			.map((part) => part.slice(0, 1).toUpperCase() + part.slice(1))
			.join(' ');
	}

	async function handleAuth(event: SubmitEvent) {
		event.preventDefault();
		authError = '';
		if (isSubmitting || !canSubmit) {
			return;
		}
		isSubmitting = true;

		const endpoint = isRegisterMode ? '/api/auth/signup' : '/api/auth/login';
		const payload: Record<string, string> = {
			email: normalizedEmail,
			password
		};
		if (isRegisterMode) {
			payload.username = normalizedUsername;
		}

		try {
			const response = await fetch(`${API_BASE}${endpoint}`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				credentials: 'include',
				body: JSON.stringify(payload)
			});
			const data = (await response.json().catch(() => null)) as
				| {
						error?: string;
						token?: string;
						user?: {
							id?: string;
							email?: string;
							username?: string;
							fullName?: string;
							avatarUrl?: string;
						};
				  }
				| null;

			if (!response.ok || !data?.token || !data.user?.id) {
				authError = data?.error?.trim() || 'Unable to authenticate. Please retry.';
				return;
			}

			const accountEmail = data.user.email?.trim() || normalizedEmail;
			const displayName =
				data.user.username?.trim() ||
				data.user.fullName?.trim() ||
				buildFallbackName(accountEmail) ||
				'User';

			login(data.token, {
				id: data.user.id.trim(),
				email: accountEmail,
				name: displayName,
				avatarUrl: data.user.avatarUrl?.trim() || '',
				role: 'member'
			});

			const postAuthRedirect = requestedRedirect || '/dashboard';
			await goto(postAuthRedirect);
			if (browser) {
				const expected = new URL(postAuthRedirect, window.location.origin);
				if (window.location.pathname !== expected.pathname || window.location.search !== expected.search) {
					window.location.assign(postAuthRedirect);
				}
			}
		} catch {
			authError = 'Unable to reach the authentication service.';
		} finally {
			isSubmitting = false;
		}
	}

	function handleGoogleLogin() {
		if (!browser || isSubmitting) {
			return;
		}
		window.location.href = `${API_BASE}/api/auth/google`;
	}
</script>

<svelte:head>
	<title>Login | Converse</title>
</svelte:head>

<main class="login-shell">
	<MonochromeRoomBackground seed="tora-persistent-login" />

	<section class="login-card">
		<header class="card-head">
			<h1>{isRegisterMode ? 'Create your persistent account' : 'Welcome Back'}</h1>
			<p>
				{isRegisterMode
					? 'Pick a unique username to keep your persistent identity across sessions.'
					: 'Sign in to access your persistent workspace dashboard.'}
			</p>
		</header>

		{#if authError}
			<div class="error-msg">{authError}</div>
		{/if}

		<form on:submit={handleAuth}>
			<div class="field-group login-field">
				<label for="email">Email</label>
				<input id="email" type="email" bind:value={email} autocomplete="email" required />
			</div>

			{#if isRegisterMode}
				<div class="field-group login-field">
					<label for="username">Username</label>
					<input
						id="username"
						type="text"
						bind:value={username}
						autocomplete="username"
						maxlength="32"
						required={isRegisterMode}
					/>
					<small>Unique, lowercase handle. Spaces and dashes become underscores.</small>
					{#if username.trim() !== '' && normalizedUsername !== username.trim().toLowerCase()}
						<small>Will be saved as {normalizedUsername || '(invalid username)'}</small>
					{/if}
				</div>
			{/if}

			<div class="field-group login-field">
				<label for="password">Password</label>
				<input
					id="password"
					type="password"
					bind:value={password}
					autocomplete={isRegisterMode ? 'new-password' : 'current-password'}
					required
				/>
			</div>

			<button type="submit" class="btn-primary-action login-field" disabled={isSubmitting || !canSubmit}>
				{#if isSubmitting}
					{isRegisterMode ? 'Creating account...' : 'Signing in...'}
				{:else}
					{isRegisterMode ? 'Create Account' : 'Sign In'}
				{/if}
			</button>
		</form>

		<div class="separator"><span>or</span></div>

		<button type="button" class="google-btn login-field" on:click={handleGoogleLogin} disabled={isSubmitting}>
			<svg viewBox="0 0 24 24" aria-hidden="true">
				<path
					fill="#4285F4"
					d="M23.49 12.27c0-.79-.07-1.54-.21-2.27H12v4.3h6.44a5.5 5.5 0 0 1-2.39 3.61v3h3.87c2.26-2.08 3.57-5.14 3.57-8.64Z"
				/>
				<path
					fill="#34A853"
					d="M12 24c3.24 0 5.96-1.08 7.95-2.93l-3.87-3c-1.08.72-2.46 1.15-4.08 1.15-3.13 0-5.78-2.11-6.72-4.95H1.28v3.09A12 12 0 0 0 12 24Z"
				/>
				<path
					fill="#FBBC05"
					d="M5.28 14.27a7.19 7.19 0 0 1 0-4.54V6.64H1.28a12 12 0 0 0 0 10.72l4-3.09Z"
				/>
				<path
					fill="#EA4335"
					d="M12 4.77c1.76 0 3.34.61 4.58 1.8l3.44-3.44C17.95 1.18 15.24 0 12 0A12 12 0 0 0 1.28 6.64l4 3.09c.94-2.84 3.59-4.96 6.72-4.96Z"
				/>
			</svg>
			Continue with Google
		</button>

		<p class="switch-row">
			{isRegisterMode ? 'Already have an account?' : 'Need an account?'}
			<button
				type="button"
				class="switch-btn"
				on:click={() => {
					isRegisterMode = !isRegisterMode;
					authError = '';
				}}
			>
				{isRegisterMode ? 'Sign in instead' : 'Create one'}
			</button>
		</p>
	</section>
</main>

<style>
	.login-shell {
		position: relative;
		isolation: isolate;
		display: flex;
		align-items: center;
		justify-content: center;
		min-height: 100dvh;
		padding: 1rem;
		background: var(--bg-primary);
		overflow: hidden;
	}

	.login-shell :global(.monochrome-room-background) {
		opacity: 0;
		transition: opacity 0.2s ease;
	}

	:global(:root[data-theme='dark']) .login-shell :global(.monochrome-room-background),
	:global(.theme-dark) .login-shell :global(.monochrome-room-background) {
		opacity: 1;
	}

	.login-card {
		position: relative;
		z-index: 1;
		width: min(100%, 420px);
		padding: 2rem;
		border-radius: 12px;
		background: var(--surface-primary);
		border: 1px solid var(--border-subtle);
		box-shadow: var(--shadow-lg);
	}

	.card-head {
		text-align: center;
		margin-bottom: 1rem;
	}

	.card-head h1 {
		margin: 0;
		color: var(--text-primary);
	}

	.card-head p {
		margin: 0.45rem 0 0;
		color: var(--text-secondary);
		font-size: 0.94rem;
	}

	form {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}

	.field-group {
		display: flex;
		flex-direction: column;
		gap: 0.36rem;
	}

	.field-group label {
		font-size: 0.85rem;
		font-weight: 600;
		color: var(--text-secondary);
	}

	.field-group small {
		font-size: 0.74rem;
		color: var(--text-tertiary);
	}

	.field-group input {
		background: var(--surface-primary);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: 6px;
		padding: 10px;
		font-size: 0.95rem;
	}

	.field-group input:focus {
		outline: none;
		border-color: var(--border-focus);
		box-shadow: 0 0 0 3px var(--interactive-focus);
	}

	.btn-primary-action,
	.google-btn {
		height: 42px;
		padding: 0.55rem 0.9rem;
		border-radius: 6px;
		font-size: 0.92rem;
		font-weight: 700;
		cursor: pointer;
		transition:
			background 0.2s,
			border-color 0.2s,
			box-shadow 0.2s,
			opacity 0.2s;
	}

	.login-field {
		width: 100%;
		max-width: 300px;
		margin-inline: auto;
	}

	.btn-primary-action {
		margin-top: 0.3rem;
		color: var(--home-action-text, #ffffff);
		background: var(--home-action-primary, #4f5f78);
		border: 1px solid var(--home-action-border, #4f5f78);
		box-shadow: 0 6px 14px var(--home-action-shadow, rgba(79, 95, 120, 0.28));
	}

	.btn-primary-action:hover:not(:disabled) {
		background: var(--home-action-primary-hover, #45546b);
		border-color: var(--home-action-primary-hover, #45546b);
	}

	.google-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 0.55rem;
		width: 100%;
		color: var(--text-primary);
		background: var(--surface-secondary);
		border: 1px solid var(--border-default);
	}

	.google-btn svg {
		width: 18px;
		height: 18px;
	}

	.google-btn:hover:not(:disabled) {
		background: var(--surface-hover);
		border-color: var(--border-strong);
	}

	.btn-primary-action:disabled,
	.google-btn:disabled {
		opacity: 0.7;
		cursor: not-allowed;
	}

	.separator {
		display: grid;
		place-items: center;
		margin: 0.85rem 0 0.5rem;
	}

	.separator span {
		font-size: 0.72rem;
		font-weight: 700;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.12em;
	}

	.switch-row {
		margin: 0.9rem 0 0;
		display: flex;
		justify-content: center;
		align-items: center;
		gap: 0.35rem;
		font-size: 0.84rem;
		color: var(--text-secondary);
	}

	.switch-btn {
		border: none;
		background: transparent;
		padding: 0;
		font-size: 0.84rem;
		font-weight: 700;
		color: var(--text-link);
		cursor: pointer;
		text-decoration: underline;
	}

	.error-msg {
		color: var(--accent-danger);
		background: var(--state-danger-bg);
		border: 1px solid var(--state-danger-border);
		padding: 10px;
		border-radius: 4px;
		margin-bottom: 15px;
		font-size: 0.85rem;
	}

	@media (max-width: 640px) {
		.login-card {
			padding: 1.15rem;
		}
	}
</style>
