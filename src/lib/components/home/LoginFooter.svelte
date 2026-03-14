<script lang="ts">
	import { captureCurrentRoom } from '$lib/utils/pendingRooms';

	function handleLoginClick() {
		if (typeof window === 'undefined') {
			return;
		}

		const pathname = window.location.pathname || '';
		if (!pathname.startsWith('/chat/')) {
			return;
		}

		const pathSuffix = pathname.split('/chat/')[1] || '';
		const roomId = decodeURIComponent(pathSuffix.split('/')[0] || '').trim();
		if (!roomId) {
			return;
		}

		const roomName = (document.title || '').trim() || roomId;
		captureCurrentRoom(roomId, roomName);
	}
</script>

<footer class="global-footer" aria-label="Site footer">
	<div class="footer-content">
		<span class="footer-brand">Product by monokenos</span>
		<div class="footer-links">
			<a class="footer-link footer-link-login" href="/login" on:click={handleLoginClick}>Log in</a>
			<a
				class="footer-link"
				href="https://portfolio.monokenos.com/contact"
				target="_blank"
				rel="noreferrer"
			>
				Contact
			</a>
		</div>
	</div>
</footer>

<style>
	.global-footer {
		margin-top: 1rem;
		padding-top: 0.95rem;
		border-top: 1px solid #dbe2ee;
	}

	.footer-content {
		display: flex;
		gap: 0.6rem 1rem;
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		font-size: 0.82rem;
		line-height: 1.2;
	}

	.footer-links {
		display: flex;
		gap: 0.55rem;
		align-items: center;
		flex-wrap: wrap;
	}

	.footer-brand {
		color: #4b5563;
		font-weight: 600;
		letter-spacing: 0.01em;
	}

	.footer-link {
		color: #0f766e;
		font-weight: 700;
		text-decoration: none;
		padding: 0.2rem 0.55rem;
		border-radius: 999px;
		background: #ecfeff;
		border: 1px solid #bae6fd;
		transition: background 120ms ease, border-color 120ms ease, color 120ms ease;
	}

	.footer-link-login {
		background: #f5f3ff;
		border-color: #ddd6fe;
		color: #5b21b6;
	}

	.footer-link:hover {
		background: #cffafe;
		border-color: #67e8f9;
		color: #155e75;
	}

	.footer-link-login:hover {
		background: #ede9fe;
		border-color: #c4b5fd;
		color: #4c1d95;
	}

	.footer-link:focus-visible {
		outline: 2px solid #0891b2;
		outline-offset: 2px;
	}

	@media (max-width: 540px) {
		.footer-content {
			font-size: 0.78rem;
		}
	}
</style>
