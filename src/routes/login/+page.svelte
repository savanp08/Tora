<script lang="ts">
	import { browser } from '$app/environment';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { onMount } from 'svelte';
	import MonochromeRoomBackground from '$lib/components/background/MonochromeRoomBackground.svelte';
	import { login } from '$lib/stores/auth';

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';

	let email = '';
	let password = '';
	let username = '';
	let isRegisterMode = false;
	let isSubmitting = false;
	let authError = '';
	let authNotice = '';
	let isForgotFlowActive = false;
	let forgotStep: 'request' | 'verify' = 'request';
	let forgotEmail = '';
	let forgotOtp = '';
	let forgotNewPassword = '';
	let forgotInfo = '';
	let forgotError = '';
	let isForgotSubmitting = false;
	let forgotDebugOtp = '';

	$: normalizedEmail = email.trim().toLowerCase();
	$: normalizedUsername = normalizeAccountUsername(username);
	$: canSubmit = normalizedEmail.length > 0 && password.trim().length > 0 && (!isRegisterMode || normalizedUsername.length > 0);
	$: normalizedForgotEmail = forgotEmail.trim().toLowerCase();
	$: normalizedForgotOtp = forgotOtp.replace(/\D+/g, '').slice(0, 6);
	$: canForgotRequest = normalizedForgotEmail.length > 0;
	$: canForgotVerify = normalizedForgotEmail.length > 0 && normalizedForgotOtp.length === 6;
	$: requestedRedirect = resolveSafeRedirect($page.url.searchParams.get('redirect'));

	type OAuthFragmentPayload = {
		token: string;
		user: {
			id: string;
			email: string;
			username: string;
			fullName: string;
			avatarUrl: string;
		};
	};

	type OAuthParamsSource = 'hash' | 'search';

	function oauthDebugLog(_event: string, _payload?: unknown) {
		// Login/OAuth debug logs intentionally disabled.
	}

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

	function parseOAuthPayloadFromParams(
		params: URLSearchParams,
		source: OAuthParamsSource
	): OAuthFragmentPayload | null {
		const token = params.get('oauth_token')?.trim() || '';
		const userID = params.get('oauth_user_id')?.trim() || '';
		const email = params.get('oauth_email')?.trim().toLowerCase() || '';
		if (!token || !userID || !email) {
			oauthDebugLog('OAuth payload source is missing required fields.', {
				source,
				hasToken: token.length > 0,
				hasUserId: userID.length > 0,
				hasEmail: email.length > 0
			});
			return null;
		}
		oauthDebugLog('OAuth payload parsed successfully.', {
			source,
			hasToken: token.length > 0,
			userId: userID,
			email
		});
		return {
			token,
			user: {
				id: userID,
				email,
				username: params.get('oauth_username')?.trim() || '',
				fullName: params.get('oauth_full_name')?.trim() || '',
				avatarUrl: params.get('oauth_avatar_url')?.trim() || ''
			}
		};
	}

	function parseOAuthLocationPayload(rawHash: string, rawSearch: string): OAuthFragmentPayload | null {
		const trimmedHash = rawHash.trim();
		if (trimmedHash) {
			oauthDebugLog('OAuth fragment found. Attempting to parse payload.', {
				hashLength: trimmedHash.length
			});
			const fragment = trimmedHash.startsWith('#') ? trimmedHash.slice(1) : trimmedHash;
			const payload = parseOAuthPayloadFromParams(new URLSearchParams(fragment), 'hash');
			if (payload) {
				return payload;
			}
		}

		oauthDebugLog('No valid OAuth payload found in hash. Checking URL search params as fallback.', {
			search: rawSearch
		});
		const search = rawSearch.startsWith('?') ? rawSearch.slice(1) : rawSearch;
		if (!search.trim()) {
			oauthDebugLog('No OAuth payload found in URL search params fallback.');
			return null;
		}
		return parseOAuthPayloadFromParams(new URLSearchParams(search), 'search');
	}

	async function trySessionCookieRedirect() {
		if (!browser) {
			return;
		}
		oauthDebugLog('No OAuth payload found. Probing backend auth session before staying on login page.');
		try {
			const response = await fetch(`${API_BASE}/api/dashboard/rooms`, {
				method: 'GET',
				credentials: 'include'
			});
			oauthDebugLog('Backend auth session probe completed.', {
				status: response.status,
				ok: response.ok
			});
			if (!response.ok) {
				return;
			}
				const postAuthRedirect = requestedRedirect || '/dashboard';
				oauthDebugLog('Backend session exists. Redirecting to dashboard.', {
					postAuthRedirect
				});
				await goto(postAuthRedirect);
				const expected = new URL(postAuthRedirect, window.location.origin);
				if (window.location.pathname !== expected.pathname || window.location.search !== expected.search) {
					window.location.assign(postAuthRedirect);
				}
		} catch (error: unknown) {
			oauthDebugLog('Backend auth session probe failed.', { error });
		}
	}

	onMount(() => {
		if (!browser) {
			return;
		}
		const rawHash = window.location.hash;
		const rawSearch = window.location.search;
		oauthDebugLog('Login page mounted. Checking for OAuth callback payload.', {
			pathname: window.location.pathname,
			search: rawSearch,
			href: window.location.href,
			hashLength: rawHash.length
		});

		const hashParams = new URLSearchParams(rawHash.startsWith('#') ? rawHash.substring(1) : rawHash);
		const oauthToken = hashParams.get('oauth_token')?.trim() || '';
		const oauthUserID = hashParams.get('oauth_user_id')?.trim() || '';
		const oauthUsername = hashParams.get('oauth_username')?.trim() || '';
		const oauthEmail = hashParams.get('oauth_email')?.trim().toLowerCase() || '';
		const oauthAvatarURL = hashParams.get('oauth_avatar_url')?.trim() || '';
		const oauthFullName = hashParams.get('oauth_full_name')?.trim() || '';
		if (oauthToken) {
			oauthDebugLog('Raw hash OAuth payload detected in onMount. Applying immediate login.', {
				hasToken: oauthToken.length > 0,
				hasUserId: oauthUserID.length > 0,
				hasEmail: oauthEmail.length > 0
			});

			const fallbackEmail = oauthEmail || 'oauth-user@local.invalid';
			const displayName =
				oauthUsername ||
				oauthFullName ||
				buildFallbackName(fallbackEmail) ||
				'User';
			login(oauthToken, {
				id: oauthUserID || `oauth-${Date.now()}`,
				email: fallbackEmail,
				name: displayName,
				avatarUrl: oauthAvatarURL,
				role: 'member'
			});

			window.history.replaceState(null, '', window.location.pathname + window.location.search);
			const postAuthRedirect = '/dashboard';
			void goto(postAuthRedirect).then(() => {
				const expected = new URL(postAuthRedirect, window.location.origin);
				if (window.location.pathname !== expected.pathname || window.location.search !== expected.search) {
					window.location.assign(postAuthRedirect);
				}
			});
			return;
		}

		const payload = parseOAuthLocationPayload(rawHash, rawSearch);
		if (!payload) {
			oauthDebugLog('No valid OAuth payload found. Staying on login page.');
			void trySessionCookieRedirect();
			return;
		}

		const displayName =
			payload.user.username ||
			payload.user.fullName ||
			buildFallbackName(payload.user.email) ||
			'User';
		oauthDebugLog('Applying OAuth login payload to client auth store.', {
			userId: payload.user.id,
			email: payload.user.email,
			displayName
		});
		login(payload.token, {
			id: payload.user.id,
			email: payload.user.email,
			name: displayName,
			avatarUrl: payload.user.avatarUrl,
			role: 'member'
		});

		oauthDebugLog('OAuth payload applied. Clearing URL hash and redirecting to dashboard.');
		window.history.replaceState(null, '', window.location.pathname + window.location.search);
		const postAuthRedirect = requestedRedirect || '/dashboard';
		oauthDebugLog('Attempting client navigation after OAuth login.', {
			redirectTarget: postAuthRedirect,
			requestedRedirect
		});
		void goto(postAuthRedirect).then(() => {
			oauthDebugLog('Client navigation completed after OAuth login.', {
				currentPath: window.location.pathname,
				currentSearch: window.location.search
			});
			const expected = new URL(postAuthRedirect, window.location.origin);
			if (window.location.pathname !== expected.pathname || window.location.search !== expected.search) {
				oauthDebugLog('Current URL still does not match expected target. Forcing full-page navigation.', {
					expectedPath: expected.pathname,
					expectedSearch: expected.search,
					actualPath: window.location.pathname,
					actualSearch: window.location.search
				});
				window.location.assign(postAuthRedirect);
			}
		}).catch((error: unknown) => {
			oauthDebugLog('Client navigation after OAuth login failed.', {
				redirectTarget: postAuthRedirect,
				error
			});
		});
	});

	async function handleAuth(event: SubmitEvent) {
		event.preventDefault();
		authError = '';
		authNotice = '';
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

	function openForgotPasswordFlow() {
		if (isSubmitting || isRegisterMode) {
			return;
		}
		isForgotFlowActive = true;
		forgotStep = 'request';
		forgotEmail = normalizedEmail || email.trim();
		forgotOtp = '';
		forgotNewPassword = '';
		forgotInfo = '';
		forgotError = '';
		forgotDebugOtp = '';
		authError = '';
		authNotice = '';
	}

	function closeForgotPasswordFlow() {
		isForgotFlowActive = false;
		forgotStep = 'request';
		forgotOtp = '';
		forgotNewPassword = '';
		forgotInfo = '';
		forgotError = '';
		forgotDebugOtp = '';
		isForgotSubmitting = false;
	}

	async function handleForgotPasswordRequest(event: SubmitEvent) {
		event.preventDefault();
		forgotError = '';
		forgotInfo = '';
		forgotDebugOtp = '';

		if (isForgotSubmitting || !canForgotRequest) {
			return;
		}
		isForgotSubmitting = true;

		try {
			const response = await fetch(`${API_BASE}/api/auth/forgot-password/request`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				credentials: 'include',
				body: JSON.stringify({ email: normalizedForgotEmail })
			});
			const data = (await response.json().catch(() => null)) as
				| {
						error?: string;
						message?: string;
						debugOtp?: string;
				  }
				| null;

			if (!response.ok) {
				forgotError = data?.error?.trim() || 'Unable to request OTP right now.';
				return;
			}

			forgotStep = 'verify';
			forgotInfo = data?.message?.trim() || 'OTP sent. Enter the code to verify.';
			forgotDebugOtp = data?.debugOtp?.trim() || '';
		} catch {
			forgotError = 'Unable to reach the authentication service.';
		} finally {
			isForgotSubmitting = false;
		}
	}

	async function handleForgotPasswordVerify(event: SubmitEvent) {
		event.preventDefault();
		forgotError = '';
		forgotInfo = '';

		if (isForgotSubmitting || !canForgotVerify) {
			return;
		}
		isForgotSubmitting = true;

		try {
			const response = await fetch(`${API_BASE}/api/auth/forgot-password/verify`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				credentials: 'include',
				body: JSON.stringify({
					email: normalizedForgotEmail,
					otp: normalizedForgotOtp,
					newPassword: forgotNewPassword
				})
			});
			const data = (await response.json().catch(() => null)) as
				| {
						error?: string;
						message?: string;
						passwordUpdated?: boolean;
				  }
				| null;

			if (!response.ok) {
				forgotError = data?.error?.trim() || 'Unable to verify OTP.';
				return;
			}

			closeForgotPasswordFlow();
			authNotice = data?.message?.trim() || 'Password reset verification complete.';
		} catch {
			forgotError = 'Unable to reach the authentication service.';
		} finally {
			isForgotSubmitting = false;
		}
	}

	function handleGoogleLogin() {
		if (!browser || isSubmitting) {
			oauthDebugLog('Google login click ignored.', {
				isBrowser: browser,
				isSubmitting
			});
			return;
		}
		const target = `${API_BASE}/api/auth/google`;
		oauthDebugLog('Redirecting browser to backend Google login endpoint.', { target });
		window.location.href = target;
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
		{#if authNotice}
			<div class="notice-msg">{authNotice}</div>
		{/if}

		{#if isForgotFlowActive}
			<section class="forgot-shell login-field">
				<header>
					<h2>Reset Password</h2>
					<p>Enter OTP and optionally set a new password.</p>
				</header>

				{#if forgotError}
					<div class="error-msg">{forgotError}</div>
				{/if}
				{#if forgotInfo}
					<div class="notice-msg">{forgotInfo}</div>
				{/if}
				{#if forgotDebugOtp}
					<div class="debug-msg">Dev OTP: {forgotDebugOtp}</div>
				{/if}

				{#if forgotStep === 'request'}
					<form on:submit={handleForgotPasswordRequest}>
						<div class="field-group">
							<label for="forgot-email">Email</label>
							<input
								id="forgot-email"
								type="email"
								bind:value={forgotEmail}
								autocomplete="email"
								required
							/>
						</div>
						<button
							type="submit"
							class="btn-primary-action"
							disabled={isForgotSubmitting || !canForgotRequest}
						>
							{isForgotSubmitting ? 'Sending OTP...' : 'Send OTP'}
						</button>
					</form>
				{:else}
					<form on:submit={handleForgotPasswordVerify}>
						<div class="field-group">
							<label for="forgot-email-verify">Email</label>
							<input
								id="forgot-email-verify"
								type="email"
								bind:value={forgotEmail}
								autocomplete="email"
								required
							/>
						</div>
						<div class="field-group">
							<label for="forgot-otp">OTP</label>
							<input
								id="forgot-otp"
								type="text"
								inputmode="numeric"
								pattern="[0-9]*"
								maxlength="6"
								value={normalizedForgotOtp}
								on:input={(event) => {
									forgotOtp = (event.currentTarget as HTMLInputElement).value;
								}}
								placeholder="6-digit OTP"
								required
							/>
						</div>
						<div class="field-group">
							<label for="forgot-new-password">New password (optional)</label>
							<input
								id="forgot-new-password"
								type="password"
								bind:value={forgotNewPassword}
								autocomplete="new-password"
								placeholder="Leave blank to keep current password"
							/>
						</div>
						<button
							type="submit"
							class="btn-primary-action"
							disabled={isForgotSubmitting || !canForgotVerify}
						>
							{isForgotSubmitting ? 'Verifying...' : 'Verify OTP'}
						</button>
					</form>
				{/if}

				<div class="forgot-actions">
					{#if forgotStep === 'verify'}
						<button
							type="button"
							class="switch-btn"
							on:click={() => {
								forgotStep = 'request';
								forgotError = '';
								forgotInfo = '';
								forgotDebugOtp = '';
							}}
						>
							Request new OTP
						</button>
					{/if}
					<button
						type="button"
						class="switch-btn"
						on:click={closeForgotPasswordFlow}
						disabled={isForgotSubmitting}
					>
						Back to sign in
					</button>
				</div>
			</section>
		{:else}
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
					{#if !isRegisterMode}
						<div class="forgot-row">
							<button type="button" class="switch-btn forgot-btn" on:click={openForgotPasswordFlow}>
								Forgot password?
							</button>
						</div>
					{/if}
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
						authNotice = '';
					}}
				>
					{isRegisterMode ? 'Sign in instead' : 'Create one'}
				</button>
			</p>
		{/if}
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

	.login-shell :global(.mrb-host),
	.login-shell :global(.monochrome-room-background) {
		opacity: 0.72;
		transition: opacity 0.3s ease;
	}

	:global(:root[data-theme='dark']) .login-shell :global(.mrb-host),
	:global(.theme-dark) .login-shell :global(.mrb-host),
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

	.forgot-shell {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
		padding: 0.85rem;
		border-radius: 10px;
		border: 1px solid var(--border-subtle);
		background: var(--surface-secondary);
	}

	.forgot-shell .btn-primary-action {
		width: 100%;
		margin-top: 0.2rem;
	}

	.forgot-shell header h2 {
		margin: 0;
		font-size: 1rem;
		color: var(--text-primary);
	}

	.forgot-shell header p {
		margin: 0.3rem 0 0;
		font-size: 0.84rem;
		color: var(--text-secondary);
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

	.forgot-row {
		display: flex;
		justify-content: flex-end;
	}

	.forgot-btn {
		font-size: 0.75rem;
	}

	.forgot-actions {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.7rem;
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

	.notice-msg {
		color: var(--accent-primary);
		background: var(--state-info-bg);
		border: 1px solid var(--state-info-border);
		padding: 10px;
		border-radius: 4px;
		margin-bottom: 15px;
		font-size: 0.85rem;
	}

	.debug-msg {
		color: var(--text-secondary);
		background: var(--surface-hover);
		border: 1px dashed var(--border-default);
		padding: 10px;
		border-radius: 4px;
		font-size: 0.8rem;
		margin-bottom: 8px;
	}

	.forgot-shell .error-msg,
	.forgot-shell .notice-msg {
		margin-bottom: 0;
	}

	@media (max-width: 640px) {
		.login-card {
			padding: 1.15rem;
		}
	}
</style>
