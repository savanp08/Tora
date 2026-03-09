<script lang="ts">
	import toraLogo from '$lib/assets/tora-logo.svg';
</script>

<nav class="navbar">
	<div class="brand">
		<img src={toraLogo} alt="Tora Logo" class="logo" />
		<h1>Tora</h1>
	</div>
</nav>

<style>
	:global(:root) {
		--legacy-navbar-bg: rgba(8, 12, 20, 0.72);
		--legacy-navbar-border: rgba(255, 255, 255, 0.12);
		--legacy-navbar-text: #e2e8f0;
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--legacy-navbar-bg: rgba(250, 252, 255, 0.72);
		--legacy-navbar-border: rgba(221, 231, 246, 0.92);
		--legacy-navbar-text: #111827;
	}

	.navbar {
		width: 100%;
		padding: 0.75rem 1rem;
		background: var(--legacy-navbar-bg);
		border-bottom: 1px solid var(--legacy-navbar-border);
		backdrop-filter: blur(14px) saturate(155%);
		-webkit-backdrop-filter: blur(14px) saturate(155%);
	}

	.brand {
		display: flex;
		align-items: center;
		gap: 0.75rem;
	}

	.logo {
		height: 40px;
		width: auto;
		display: block;
	}

	h1 {
		margin: 0;
		font-size: 1.1rem;
		font-weight: 700;
		letter-spacing: 0.02em;
		color: var(--legacy-navbar-text);
	}
</style>
