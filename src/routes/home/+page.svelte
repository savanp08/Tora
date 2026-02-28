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
		<h1 class="headline">Welcome to Converse</h1>
		<p class="sub-headline">
			The modern, secure, and real-time chat application for seamless collaboration.
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
	.home-container {
		font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;
		max-width: 1200px;
		margin: 0 auto;
		padding: 4rem 2rem;
		color: #333;
	}

	.hero-section {
		text-align: center;
		margin-bottom: 4rem;
	}

	.headline {
		font-size: 3.5rem;
		font-weight: 700;
		margin-bottom: 1rem;
		background: linear-gradient(45deg, #2563eb, #1e40af);
		-webkit-background-clip: text;
		-webkit-text-fill-color: transparent;
	}

	.sub-headline {
		font-size: 1.25rem;
		color: #4b5563;
		max-width: 600px;
		margin: 0 auto 2rem;
	}

	.cta-button {
		background-color: #2563eb;
		color: white;
		border: none;
		padding: 0.75rem 1.5rem;
		font-size: 1rem;
		font-weight: 600;
		border-radius: 0.5rem;
		cursor: pointer;
		transition: background-color 0.2s;
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
		color: #334155;
	}

	.launch-password-field input {
		width: 100%;
		border: 1px solid #cbd5e1;
		border-radius: 0.5rem;
		padding: 0.55rem 0.7rem;
		font-size: 0.92rem;
	}

	.launch-password-field small {
		font-size: 0.78rem;
		color: #64748b;
		text-align: left;
	}

	.cta-button:hover {
		background-color: #1e40af;
	}

	.features-grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
		gap: 2rem;
	}

	.feature-card {
		background-color: #ffffff;
		border-radius: 0.75rem;
		padding: 2rem;
		box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1), 0 2px 4px -1px rgba(0, 0, 0, 0.06);
		transition: transform 0.2s, box-shadow 0.2s;
	}

	.feature-card:hover {
		transform: translateY(-5px);
		box-shadow: 0 10px 15px -3px rgba(0, 0, 0, 0.1), 0 4px 6px -2px rgba(0, 0, 0, 0.05);
	}

	.feature-title {
		font-size: 1.25rem;
		font-weight: 600;
		margin-bottom: 0.75rem;
		color: #1f2937;
	}

	.feature-description {
		font-size: 1rem;
		color: #4b5563;
	}
</style>
