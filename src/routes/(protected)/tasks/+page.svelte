<svelte:head>
	<title>Tasks | Converse</title>
</svelte:head>

<main class="tasks-shell">
	<section class="card">
		<h1>Tasks</h1>
		<p>Cross-room task management view will be wired here.</p>
	</section>
</main>

<style>
	.tasks-shell {
		min-height: 100dvh;
		padding: 5.6rem 1.2rem 1.4rem;
		background: #0d0d12;
		color: #f2f6ff;
	}

	.card {
		max-width: 920px;
		margin: 0 auto;
		border-radius: 14px;
		padding: 1rem;
		background: rgba(255, 255, 255, 0.03);
		border: 1px solid rgba(255, 255, 255, 0.08);
	}

	h1 {
		margin: 0;
		font-size: 1.25rem;
	}

	p {
		margin: 0.45rem 0 0;
		color: rgba(201, 211, 226, 0.84);
	}
</style>
