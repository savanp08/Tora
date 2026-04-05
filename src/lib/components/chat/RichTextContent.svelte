<script lang="ts">
	import { renderRichTextHtml } from '$lib/utils/chat/richText';

	export let text = '';
	export let className = '';
	let html = '';

	$: html = renderRichTextHtml(text);
</script>

<div class={`rich-text-content ${className}`.trim()}>
	{@html html}
</div>

<style>
	.rich-text-content {
		color: inherit;
		font: inherit;
		line-height: 1.6;
		min-width: 0;
	}

	.rich-text-content :global(*:first-child) {
		margin-top: 0;
	}

	.rich-text-content :global(*:last-child) {
		margin-bottom: 0;
	}

	.rich-text-content :global(p),
	.rich-text-content :global(ul),
	.rich-text-content :global(ol),
	.rich-text-content :global(blockquote),
	.rich-text-content :global(pre),
	.rich-text-content :global(hr),
	.rich-text-content :global(h1),
	.rich-text-content :global(h2),
	.rich-text-content :global(h3),
	.rich-text-content :global(h4),
	.rich-text-content :global(h5),
	.rich-text-content :global(h6) {
		margin: 0.8rem 0 0;
	}

	.rich-text-content :global(h1),
	.rich-text-content :global(h2),
	.rich-text-content :global(h3),
	.rich-text-content :global(h4),
	.rich-text-content :global(h5),
	.rich-text-content :global(h6) {
		font: inherit;
		font-weight: 700;
		line-height: 1.35;
	}

	.rich-text-content :global(h1) {
		font-size: 1.16em;
	}

	.rich-text-content :global(h2) {
		font-size: 1.1em;
	}

	.rich-text-content :global(h3),
	.rich-text-content :global(h4),
	.rich-text-content :global(h5),
	.rich-text-content :global(h6) {
		font-size: 1.02em;
	}

	.rich-text-content :global(ul),
	.rich-text-content :global(ol) {
		padding-left: 1.3rem;
	}

	.rich-text-content :global(li + li) {
		margin-top: 0.25rem;
	}

	.rich-text-content :global(blockquote) {
		padding-left: 0.9rem;
		border-left: 3px solid rgba(148, 163, 184, 0.42);
		opacity: 0.94;
	}

	.rich-text-content :global(code) {
		font-family:
			'SFMono-Regular', 'SF Mono', 'JetBrains Mono', 'Cascadia Code', 'Fira Code',
			'Source Code Pro', monospace;
		font-size: 0.95em;
		padding: 0.12rem 0.34rem;
		border-radius: 0.42rem;
		background: rgba(15, 23, 42, 0.08);
		border: 1px solid rgba(15, 23, 42, 0.12);
	}

	.rich-text-content :global(pre) {
		padding: 0.9rem 1rem;
		border-radius: 0.9rem;
		overflow-x: auto;
		background: rgba(15, 23, 42, 0.08);
		border: 1px solid rgba(15, 23, 42, 0.12);
	}

	.rich-text-content :global(pre code) {
		display: block;
		padding: 0;
		border: 0;
		background: transparent;
		font-size: 0.92em;
		line-height: 1.55;
	}

	.rich-text-content :global(.ai-rich-text-code-language) {
		margin-bottom: 0.55rem;
		font-size: 0.72em;
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		opacity: 0.7;
	}

	.rich-text-content :global(a) {
		color: inherit;
		text-decoration: underline;
		text-decoration-thickness: 0.08em;
		text-underline-offset: 0.16em;
	}

	.rich-text-content :global(hr) {
		border: 0;
		border-top: 1px solid rgba(148, 163, 184, 0.3);
	}
</style>
