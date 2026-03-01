<script lang="ts">
	import { goto } from '$app/navigation';
	let roomPassword = '';

	function navigateToApp() {
		const normalizedPassword = (roomPassword || '').trim().slice(0, 32);
		const hash = normalizedPassword ? `#key=${encodeURIComponent(normalizedPassword)}` : '';
		goto(`/${hash}`);
	}
</script>

<div class="home-container">
	<header class="hero-section">
		<h1 class="headline">Welcome to Tora</h1>
		<p class="sub-headline">
			A secure, real-time chat space with disappearing rooms and premium collaboration flow.
		</p>
		<div class="launch-password-field">
			<label for="launch-password">Room Password (E2EE)</label>
			<input
				id="launch-password"
				type="password"
				bind:value={roomPassword}
				maxlength="32"
				placeholder="Optional password"
				autocomplete="off"
			/>
			<small>
				Optional. Encrypts all messages and board data. The server cannot read protected rooms.
			</small>
		</div>
		<button class="cta-button" on:click={navigateToApp}>Get Started</button>
	</header>

	<main class="features-grid">
		<div class="feature-card">
			<h3 class="feature-title">Ephemeral & Persistent Chat</h3>
			<p class="feature-description">
				Create temporary, auto-expiring rooms for quick huddles or persistent rooms for long-term
				projects.
			</p>
		</div>
		<div class="feature-card">
			<h3 class="feature-title">Breakout Discussions</h3>
			<p class="feature-description">
				Branch off from any message into a separate, linked "breakout" room to keep conversations
				focused and organized.
			</p>
		</div>
		<div class="feature-card">
			<h3 class="feature-title">Integrated Task Management</h3>
			<p class="feature-description">
				Turn talk into action. Create and manage shared task lists directly within your chat to
				stay on top of your work.
			</p>
		</div>
		<div class="feature-card">
			<h3 class="feature-title">Secure & Private</h3>
			<p class="feature-description">
				Your conversations are your own. We prioritize your privacy with end-to-end encryption.
			</p>
		</div>
		<div class="feature-card">
			<h3 class="feature-title">Easy Access</h3>
			<p class="feature-description">
				No complicated sign-ups. Join rooms instantly with a simple name or a 6-digit code.
			</p>
		</div>
		<div class="feature-card">
			<h3 class="feature-title">Real-time Collaboration</h3>
			<p class="feature-description">
				Experience fluid, real-time communication that’s perfect for team collaboration and staying
				in sync.
			</p>
		</div>
	</main>
</div>

<style>
	:global(body) {
		margin: 0;
		min-height: 100vh;
		font-family: 'Space Grotesk', 'Sora', 'Avenir Next', sans-serif;
		background:
			radial-gradient(circle at 18% 18%, rgba(0, 240, 255, 0.18), transparent 42%),
			radial-gradient(circle at 78% 0%, rgba(112, 0, 255, 0.22), transparent 38%),
			linear-gradient(150deg, #060910 0%, #0b1324 52%, #10192f 100%);
		color: #f8fafc;
	}

	.home-container {
		max-width: 1200px;
		margin: 0 auto;
		padding: 4rem 1.25rem;
		color: #f8fafc;
	}

	.hero-section {
		text-align: center;
		margin-bottom: 4rem;
		background: rgba(15, 23, 42, 0.6);
		border: 1px solid rgba(148, 163, 184, 0.24);
		box-shadow: 0 24px 70px rgba(2, 6, 23, 0.55);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
		border-radius: 24px;
		padding: clamp(1.5rem, 4vw, 3.25rem);
		animation: rise-in 0.65s ease both;
	}

	.headline {
		font-size: clamp(2.1rem, 5vw, 3.8rem);
		font-weight: 700;
		margin-bottom: 1rem;
		letter-spacing: 0.01em;
		background: linear-gradient(135deg, #00f0ff, #7c3aed 70%);
		background-clip: text;
		-webkit-background-clip: text;
		color: transparent;
		-webkit-text-fill-color: transparent;
	}

	.sub-headline {
		font-size: 1.25rem;
		color: #dbeafe;
		max-width: 600px;
		margin: 0 auto 2rem;
	}

	.cta-button {
		background: linear-gradient(135deg, #00f0ff, #7000ff);
		color: white;
		border: none;
		padding: 0.8rem 1.8rem;
		font-size: 1rem;
		font-weight: 600;
		border-radius: 0.65rem;
		cursor: pointer;
		transition: transform 0.15s ease, box-shadow 0.2s ease, opacity 0.2s ease;
		box-shadow: 0 12px 26px rgba(56, 189, 248, 0.26);
	}

	.launch-password-field {
		max-width: 440px;
		margin: 0 auto 1rem;
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 0.35rem;
	}

	.launch-password-field label {
		font-size: 0.85rem;
		font-weight: 600;
		color: #e2e8f0;
	}

	.launch-password-field input {
		width: 100%;
		border: 1px solid rgba(148, 163, 184, 0.45);
		border-radius: 0.65rem;
		padding: 0.62rem 0.74rem;
		font-size: 0.92rem;
		background: rgba(15, 23, 42, 0.45);
		color: #f8fafc;
		transition: border-color 0.2s ease, box-shadow 0.2s ease;
	}

	.launch-password-field input:focus {
		outline: none;
		border-color: #00f0ff;
		box-shadow: 0 0 0 3px rgba(0, 240, 255, 0.2);
	}

	.launch-password-field small {
		font-size: 0.78rem;
		color: #cbd5e1;
		text-align: left;
	}

	.cta-button:hover {
		opacity: 0.93;
		transform: translateY(-1px);
	}

	.features-grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
		gap: 2rem;
	}

	.feature-card {
		background: rgba(15, 23, 42, 0.52);
		border: 1px solid rgba(148, 163, 184, 0.2);
		backdrop-filter: blur(12px);
		-webkit-backdrop-filter: blur(12px);
		border-radius: 0.95rem;
		padding: 2rem;
		box-shadow: 0 14px 32px rgba(2, 6, 23, 0.3);
		transition: transform 0.25s ease, box-shadow 0.25s ease, border-color 0.25s ease;
		animation: rise-in 0.6s ease both;
	}

	.feature-card:hover {
		transform: translateY(-6px);
		box-shadow: 0 18px 42px rgba(15, 23, 42, 0.5);
		border-color: rgba(0, 240, 255, 0.35);
	}

	.feature-title {
		font-size: 1.25rem;
		font-weight: 600;
		margin-bottom: 0.75rem;
		color: #f8fafc;
	}

	.feature-description {
		font-size: 1rem;
		color: #cbd5e1;
	}

	.feature-card:nth-child(2) {
		animation-delay: 0.05s;
	}

	.feature-card:nth-child(3) {
		animation-delay: 0.1s;
	}

	.feature-card:nth-child(4) {
		animation-delay: 0.15s;
	}

	.feature-card:nth-child(5) {
		animation-delay: 0.2s;
	}

	.feature-card:nth-child(6) {
		animation-delay: 0.25s;
	}

	@keyframes rise-in {
		from {
			opacity: 0;
			transform: translateY(12px);
		}
		to {
			opacity: 1;
			transform: translateY(0);
		}
	}

	@media (max-width: 640px) {
		.home-container {
			padding: 2.4rem 1rem;
		}

		.hero-section {
			margin-bottom: 2.2rem;
		}

		.sub-headline {
			font-size: 1.02rem;
		}

		.features-grid {
			grid-template-columns: 1fr;
			gap: 1rem;
		}

		.feature-card {
			padding: 1.3rem;
		}
	}
</style>
