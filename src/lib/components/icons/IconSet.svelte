<script lang="ts">
	type IconName =
		| 'copy'
		| 'break'
		| 'pin'
		| 'reply'
		| 'paperclip'
		| 'image'
		| 'file'
		| 'send'
		| 'edit'
		| 'trash'
		| 'more-horizontal'
		| 'theme'
		| 'chevron-down'
		| 'tree-map'
		| 'list-vertical';

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
		<line x1="6" y1="3" x2="6" y2="15"></line>
		<circle cx="18" cy="6" r="3"></circle>
		<circle cx="6" cy="18" r="3"></circle>
		<path d="M18 9a9 9 0 0 1-9 9"></path>
	{:else if name === 'pin'}
		<path
			fill="currentColor"
			stroke="none"
			d="M12 2.5c-3.87 0-7 3.13-7 7 0 5.09 6 11.51 6.25 11.78a1 1 0 0 0 1.5 0C13 21.01 19 14.59 19 9.5c0-3.87-3.13-7-7-7Zm0 9.2a2.2 2.2 0 1 1 0-4.4 2.2 2.2 0 0 1 0 4.4Z"
		></path>
	{:else if name === 'reply'}
		<path d="M9 7 4 12l5 5"></path>
		<path d="M4 12h10a6 6 0 0 1 6 6"></path>
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
	{:else if name === 'edit'}
		<path d="M12 20h9"></path>
		<path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"></path>
	{:else if name === 'trash'}
		<polyline points="3 6 5 6 21 6"></polyline>
		<path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
		<path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"></path>
		<line x1="10" y1="11" x2="10" y2="17"></line>
		<line x1="14" y1="11" x2="14" y2="17"></line>
	{:else if name === 'more-horizontal'}
		<circle cx="5" cy="12" r="1.6"></circle>
		<circle cx="12" cy="12" r="1.6"></circle>
		<circle cx="19" cy="12" r="1.6"></circle>
	{:else if name === 'theme'}
		<circle cx="12" cy="12" r="3.5"></circle>
		<path d="M12 2v2"></path>
		<path d="M12 20v2"></path>
		<path d="M4.93 4.93l1.41 1.41"></path>
		<path d="M17.66 17.66l1.41 1.41"></path>
		<path d="M2 12h2"></path>
		<path d="M20 12h2"></path>
		<path d="M4.93 19.07l1.41-1.41"></path>
		<path d="M17.66 6.34l1.41-1.41"></path>
	{:else if name === 'chevron-down'}
		<polyline points="6 9 12 15 18 9"></polyline>
	{:else if name === 'tree-map'}
		<circle cx="6" cy="6" r="2"></circle>
		<circle cx="18" cy="6" r="2"></circle>
		<circle cx="12" cy="12" r="2"></circle>
		<circle cx="7" cy="18" r="2"></circle>
		<circle cx="17" cy="18" r="2"></circle>
		<path d="M8 6h8"></path>
		<path d="M6 8v4"></path>
		<path d="M18 8v4"></path>
		<path d="M12 14v2"></path>
		<path d="M10 18h4"></path>
	{:else if name === 'list-vertical'}
		<circle cx="6" cy="6" r="2"></circle>
		<circle cx="6" cy="12" r="2"></circle>
		<circle cx="6" cy="18" r="2"></circle>
		<path d="M10 6h10"></path>
		<path d="M10 12h10"></path>
		<path d="M10 18h10"></path>
	{:else}
		<circle cx="12" cy="12" r="10"></circle>
	{/if}
</svg>
