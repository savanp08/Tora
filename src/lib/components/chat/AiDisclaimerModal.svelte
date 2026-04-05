<script lang="ts">
	import { createEventDispatcher } from 'svelte';

	export let open = false;
	export let isDarkMode = false;
	export let privacyPolicyUrl = 'https://example.com/privacy-policy';

	const dispatch = createEventDispatcher<{
		cancel: void;
		agree: void;
	}>();

	function onBackdropClick(event: MouseEvent) {
		if (event.target === event.currentTarget) {
			dispatch('cancel');
		}
	}
</script>

{#if open}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<div
		class="ai-disclaimer-overlay {isDarkMode ? 'theme-dark' : ''}"
		role="presentation"
		on:click={onBackdropClick}
	>
		<div class="ai-disclaimer-card" role="dialog" aria-modal="true" aria-label="AI terms notice">
			<h3>AI Features</h3>
			<p>
				AI can help you draft, summarize, brainstorm, and answer questions in context. Responses may
				not always be accurate, so please review important details before relying on them.
			</p>
			<p>
				By continuing, you agree to use these features in line with our
				<a href={privacyPolicyUrl} target="_blank" rel="noreferrer noopener">Privacy Policy</a>.
			</p>
			<div class="ai-disclaimer-actions">
				<button type="button" class="cancel" on:click={() => dispatch('cancel')}>Cancel</button>
				<button type="button" class="agree" on:click={() => dispatch('agree')}>Continue</button>
			</div>
		</div>
	</div>
{/if}

<style>
	.ai-disclaimer-overlay {
		position: fixed;
		inset: 0;
		z-index: 1100;
		background: rgba(11, 15, 20, 0.48);
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 1rem;
	}

	.ai-disclaimer-overlay.theme-dark {
		background: rgba(2, 6, 14, 0.62);
	}

	.ai-disclaimer-card {
		width: min(460px, 100%);
		border-radius: 14px;
		border: 1px solid rgba(122, 132, 146, 0.38);
		background: #ffffff;
		color: #1f2937;
		padding: 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.72rem;
		box-shadow: 0 22px 44px rgba(15, 23, 42, 0.22);
	}

	.theme-dark .ai-disclaimer-card {
		background: #1b1f26;
		color: #e5e7eb;
		border-color: rgba(111, 120, 134, 0.48);
		box-shadow: 0 22px 44px rgba(3, 7, 16, 0.42);
	}

	h3 {
		margin: 0;
		font-size: 1.02rem;
		font-weight: 700;
	}

	p {
		margin: 0;
		line-height: 1.5;
		font-size: 0.92rem;
	}

	a {
		width: fit-content;
		font-size: 0.88rem;
		color: #2563eb;
		text-decoration: none;
		font-weight: 600;
	}

	a:hover {
		text-decoration: underline;
	}

	.theme-dark a {
		color: #93c5fd;
	}

	.ai-disclaimer-actions {
		display: flex;
		justify-content: flex-end;
		gap: 0.56rem;
	}

	.ai-disclaimer-actions button {
		border-radius: 10px;
		border: 1px solid transparent;
		padding: 0.42rem 0.76rem;
		font-size: 0.85rem;
		font-weight: 600;
		cursor: pointer;
	}

	.ai-disclaimer-actions .cancel {
		background: #f3f4f6;
		border-color: #d1d5db;
		color: #374151;
	}

	.ai-disclaimer-actions .agree {
		background: #111827;
		color: #f9fafb;
	}

	.theme-dark .ai-disclaimer-actions .cancel {
		background: #2b3039;
		border-color: #3f4652;
		color: #d1d5db;
	}

	.theme-dark .ai-disclaimer-actions .agree {
		background: #e5e7eb;
		color: #111827;
	}
</style>
