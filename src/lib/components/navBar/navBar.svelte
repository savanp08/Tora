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
	.navbar {
		width: 100%;
		padding: 0.75rem 1rem;
		background: rgba(8, 12, 20, 0.72);
		border-bottom: 1px solid rgba(255, 255, 255, 0.08);
		backdrop-filter: blur(10px);
		-webkit-backdrop-filter: blur(10px);
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
		color: #e2e8f0;
	}
</style>
