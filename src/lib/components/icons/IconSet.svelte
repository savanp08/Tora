<script lang="ts">
	type IconName = 'copy' | 'break' | 'paperclip' | 'image' | 'file' | 'send' | 'chevron-down';

	export let name: IconName;
	export let size = 16;
	export let title = '';
	export let className = '';
</script>

<svg
	class={className}
	width={size}
	height={size}
	viewBox="0 0 24 24"
	fill="none"
	stroke="currentColor"
	stroke-width="2"
	stroke-linecap="round"
	stroke-linejoin="round"
	aria-hidden={title ? undefined : 'true'}
	aria-label={title || undefined}
	role={title ? 'img' : 'presentation'}
>
	{#if name === 'copy'}
		<rect x="9" y="9" width="13" height="13" rx="2"></rect>
		<rect x="2" y="2" width="13" height="13" rx="2"></rect>
	{:else if name === 'break'}
		<path d="M4 4v6h6"></path>
		<path d="M20 20v-6h-6"></path>
		<path d="M10 10l10 10"></path>
	{:else if name === 'paperclip'}
		<path
			d="M21.44 11.05l-8.49 8.49a5.5 5.5 0 0 1-7.78-7.78l9.9-9.9a3.5 3.5 0 0 1 4.95 4.95l-9.2 9.2a1.5 1.5 0 0 1-2.12-2.12l8.49-8.49"
		></path>
	{:else if name === 'image'}
		<rect x="3" y="3" width="18" height="18" rx="2"></rect>
		<circle cx="8.5" cy="8.5" r="1.5"></circle>
		<path d="M21 15l-5-5L5 21"></path>
	{:else if name === 'file'}
		<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path>
		<polyline points="14 2 14 8 20 8"></polyline>
	{:else if name === 'send'}
		<path d="M22 2L11 13"></path>
		<path d="M22 2L15 22L11 13L2 9L22 2Z"></path>
	{:else if name === 'chevron-down'}
		<polyline points="6 9 12 15 18 9"></polyline>
	{:else}
		<circle cx="12" cy="12" r="10"></circle>
	{/if}
</svg>
