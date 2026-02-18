<script lang="ts">
	import { afterUpdate, createEventDispatcher, onDestroy } from 'svelte';
	import IconSet from '$lib/components/icons/IconSet.svelte';

	type ChatMessage = {
		id: string;
		roomId: string;
		senderId: string;
		senderName: string;
		content: string;
		type: string;
		mediaUrl?: string;
		mediaType?: string;
		fileName?: string;
		createdAt: number;
		hasBreakRoom?: boolean;
		breakRoomId?: string;
		breakJoinCount?: number;
		pending?: boolean;
	};

	export let messages: ChatMessage[] = [];
	export let currentUserId = '';
	export let roomMessageSearch = '';
	export let expandedMessages: Record<string, boolean> = {};
	export let isMember = true;
	export let isSelectionMode = false;
	export let focusMessageId = '';

	const dispatch = createEventDispatcher<{
		toggleExpand: { messageId: string };
		joinBreakRoom: { roomId: string };
		joinRoom: void;
		messageSelect: { messageId: string };
		focusHandled: { messageId: string };
	}>();

	const COLLAPSED_MESSAGE_LENGTH = 500;

	let viewport: HTMLDivElement | null = null;
	let previousVisibleCount = 0;
	let copiedMessageId = '';
	let copyResetTimer: ReturnType<typeof setTimeout> | null = null;
	let mediaLoadFailedById: Record<string, boolean> = {};
	let focusedMessageId = '';
	let clearFocusOnPointerDown: ((event: PointerEvent) => void) | null = null;

	$: if (!focusMessageId && focusedMessageId) {
		focusedMessageId = '';
	}

	$: visibleMessages = getVisibleMessages(messages, roomMessageSearch);

	afterUpdate(() => {
		if (!viewport) {
			return;
		}
		if (visibleMessages.length !== previousVisibleCount) {
			previousVisibleCount = visibleMessages.length;
			viewport.scrollTop = viewport.scrollHeight;
		}
		tryFocusMessage();
	});

	onDestroy(() => {
		if (copyResetTimer) {
			clearTimeout(copyResetTimer);
		}
		if (typeof window !== 'undefined' && clearFocusOnPointerDown) {
			window.removeEventListener('pointerdown', clearFocusOnPointerDown, true);
			clearFocusOnPointerDown = null;
		}
	});

	function tryFocusMessage() {
		if (!viewport || !focusMessageId) {
			return;
		}
		const nodes = viewport.querySelectorAll<HTMLElement>('[data-message-id]');
		let target: HTMLElement | null = null;
		for (const node of nodes) {
			if (node.dataset.messageId === focusMessageId) {
				target = node;
				break;
			}
		}
		if (!target) {
			return;
		}
		target.scrollIntoView({ behavior: 'smooth', block: 'center' });
		focusedMessageId = focusMessageId;
		dispatch('focusHandled', { messageId: focusMessageId });
		if (typeof window !== 'undefined') {
			if (clearFocusOnPointerDown) {
				window.removeEventListener('pointerdown', clearFocusOnPointerDown, true);
				clearFocusOnPointerDown = null;
			}
			clearFocusOnPointerDown = () => {
				clearFocusedMessage();
			};
			window.addEventListener('pointerdown', clearFocusOnPointerDown, true);
		}
	}

	function clearFocusedMessage() {
		if (focusedMessageId) {
			focusedMessageId = '';
		}
		if (typeof window !== 'undefined' && clearFocusOnPointerDown) {
			window.removeEventListener('pointerdown', clearFocusOnPointerDown, true);
			clearFocusOnPointerDown = null;
		}
	}

	function getVisibleMessages(input: ChatMessage[], query: string) {
		const normalized = query.trim().toLowerCase();
		if (!normalized) {
			return input;
		}
		return input.filter(
			(message) =>
				message.content.toLowerCase().includes(normalized) ||
				message.senderName.toLowerCase().includes(normalized)
		);
	}

	function isLongMessage(content: string) {
		return content.length > COLLAPSED_MESSAGE_LENGTH;
	}

	function isMessageExpanded(messageId: string) {
		return Boolean(expandedMessages[messageId]);
	}

	function isCodeBlock(content: string) {
		const trimmed = content.trim();
		return trimmed.startsWith('```') && trimmed.endsWith('```') && trimmed.length >= 6;
	}

	function getCodeContent(content: string) {
		const trimmed = content.trim();
		const withoutOpening = trimmed.replace(/^```[^\n]*\n?/, '');
		return withoutOpening.replace(/```$/, '');
	}

	function formatClock(timestamp: number) {
		const safe = Number.isFinite(timestamp) ? timestamp : Date.now();
		return new Date(safe).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
	}

	function formatBreakCount(count: number | undefined) {
		const safeCount = Number.isFinite(count) ? Number(count) : 0;
		if (safeCount > 999) {
			return `${(safeCount / 1000).toFixed(1).replace(/\.0$/, '')}k`;
		}
		if (safeCount > 99) {
			return '99+';
		}
		if (safeCount <= 0) {
			return '1';
		}
		return String(safeCount);
	}

	function getMediaURL(message: ChatMessage) {
		return (message.mediaUrl || message.content || '').trim();
	}

	function isMediaBubble(message: ChatMessage) {
		return message.type === 'image' || message.type === 'video' || message.type === 'file';
	}

	function isLikelyURL(value: string) {
		const trimmed = value.trim();
		return (
			trimmed.startsWith('http://') ||
			trimmed.startsWith('https://') ||
			trimmed.startsWith('blob:') ||
			trimmed.startsWith('data:') ||
			trimmed.startsWith('/')
		);
	}

	function getMediaCaption(message: ChatMessage) {
		const content = (message.content || '').trim();
		if (!content) {
			return '';
		}
		const mediaURL = getMediaURL(message);
		if (mediaURL && content === mediaURL) {
			return '';
		}
		if (!mediaURL && isLikelyURL(content)) {
			return '';
		}
		return content;
	}

	function getFileName(message: ChatMessage) {
		const provided = (message.fileName || '').trim();
		if (provided) {
			return provided;
		}
		const mediaURL = getMediaURL(message);
		if (!mediaURL) {
			return 'Attachment';
		}
		try {
			const parsed = new URL(mediaURL);
			const base = parsed.pathname.split('/').pop() || '';
			return safeDecode(base) || 'Attachment';
		} catch {
			const base = mediaURL.split('/').pop() || '';
			return safeDecode(base) || 'Attachment';
		}
	}

	function safeDecode(value: string) {
		try {
			return decodeURIComponent(value);
		} catch {
			return value;
		}
	}

	function getFileExtension(message: ChatMessage) {
		const name = getFileName(message);
		const dot = name.lastIndexOf('.');
		if (dot <= 0 || dot === name.length - 1) {
			return '';
		}
		return name.slice(dot + 1).toLowerCase();
	}

	function isPDFMessage(message: ChatMessage) {
		const ext = getFileExtension(message);
		const mediaType = (message.mediaType || '').toLowerCase();
		return ext === 'pdf' || mediaType.includes('pdf');
	}

	function isImageFileMessage(message: ChatMessage) {
		const ext = getFileExtension(message);
		const mediaType = (message.mediaType || '').toLowerCase();
		return ['jpg', 'jpeg', 'png', 'gif', 'webp'].includes(ext) || mediaType.startsWith('image/');
	}

	function isVideoFileMessage(message: ChatMessage) {
		const ext = getFileExtension(message);
		const mediaType = (message.mediaType || '').toLowerCase();
		return ['mp4', 'webm', 'mov', 'm4v', 'ogg'].includes(ext) || mediaType.startsWith('video/');
	}

	function onMediaError(messageID: string) {
		mediaLoadFailedById = {
			...mediaLoadFailedById,
			[messageID]: true
		};
	}

	async function copyMessage(message: ChatMessage) {
		if (!message.content) {
			return;
		}
		try {
			await navigator.clipboard.writeText(message.content);
			copiedMessageId = message.id;
			if (copyResetTimer) {
				clearTimeout(copyResetTimer);
			}
			copyResetTimer = setTimeout(() => {
				copiedMessageId = '';
			}, 1200);
		} catch {
			copiedMessageId = '';
		}
	}

	function onMessageClick(message: ChatMessage) {
		if (!isMember || !isSelectionMode) {
			return;
		}
		dispatch('messageSelect', { messageId: message.id });
	}

	function onMessageKeyDown(event: KeyboardEvent, message: ChatMessage) {
		if (!isMember || !isSelectionMode) {
			return;
		}
		if (event.key === 'Enter' || event.key === ' ') {
			event.preventDefault();
			dispatch('messageSelect', { messageId: message.id });
		}
	}
</script>

<div class="messages-shell {isSelectionMode ? 'selection-mode' : ''}">
	<div class="messages" bind:this={viewport}>
		{#if !isMember}
			<div class="readonly-banner">Read-only preview. Join this room to post messages.</div>
		{/if}

		{#if visibleMessages.length === 0}
			<div class="empty-thread">
				{#if roomMessageSearch.trim()}
					No messages matched your room search.
				{:else}
					No messages yet. Send the first one.
				{/if}
			</div>
		{/if}

		{#each visibleMessages as message (message.id)}
			<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
			<article
				class="bubble {message.senderId === currentUserId ? 'mine' : 'theirs'} {message.pending
					? 'pending'
					: ''} {isSelectionMode ? 'selectable' : ''}"
				class:media-bubble={isMediaBubble(message)}
				class:focused={focusedMessageId === message.id}
				role={isSelectionMode ? 'button' : undefined}
				tabindex={isSelectionMode ? 0 : undefined}
				data-message-id={message.id}
				on:click={() => onMessageClick(message)}
				on:keydown={(event) => onMessageKeyDown(event, message)}
			>
				<button
					type="button"
					class="copy-btn"
					title="Copy message"
					on:click|stopPropagation={() => void copyMessage(message)}
				>
					<IconSet name="copy" size={14} />
				</button>
				{#if copiedMessageId === message.id}
					<div class="copied-tip">Copied!</div>
				{/if}

				<div class="bubble-meta">
					<span>{message.senderName}</span>
					<div class="meta-right">
						<time>{formatClock(message.createdAt)}</time>
						{#if message.hasBreakRoom && message.breakRoomId}
							<button
								type="button"
								class="break-indicator"
								title="Join break room"
								on:click|stopPropagation={() =>
									dispatch('joinBreakRoom', { roomId: message.breakRoomId || '' })}
							>
								<IconSet name="break" size={12} />
								<span>Join Thread ({formatBreakCount(message.breakJoinCount)} joined)</span>
							</button>
						{/if}
					</div>
				</div>
				<div
					class="bubble-content"
					class:collapsed={message.type === 'text' &&
						isLongMessage(message.content) &&
						!isMessageExpanded(message.id)}
				>
					{#if message.type === 'image' && getMediaURL(message) && !mediaLoadFailedById[message.id]}
						<img
							src={getMediaURL(message)}
							alt={getFileName(message)}
							class="media-preview image-preview"
							loading="lazy"
							on:error={() => onMediaError(message.id)}
						/>
						{#if getMediaCaption(message)}
							<div class="media-caption">{getMediaCaption(message)}</div>
						{/if}
					{:else if message.type === 'video' && getMediaURL(message) && !mediaLoadFailedById[message.id]}
						<!-- svelte-ignore a11y_media_has_caption -->
						<video
							src={getMediaURL(message)}
							class="media-preview video-preview"
							controls
							preload="metadata"
							on:error={() => onMediaError(message.id)}
						></video>
						{#if getMediaCaption(message)}
							<div class="media-caption">{getMediaCaption(message)}</div>
						{/if}
					{:else if (message.type === 'file' || mediaLoadFailedById[message.id]) && getMediaURL(message)}
						{#if isPDFMessage(message)}
							<iframe
								class="pdf-preview"
								src={getMediaURL(message)}
								title={getFileName(message)}
								loading="lazy"
							></iframe>
						{/if}
						{#if isImageFileMessage(message) && !mediaLoadFailedById[message.id]}
							<img
								src={getMediaURL(message)}
								alt={getFileName(message)}
								class="media-preview image-preview file-inline-preview"
								loading="lazy"
								on:error={() => onMediaError(message.id)}
							/>
						{:else if isVideoFileMessage(message) && !mediaLoadFailedById[message.id]}
							<!-- svelte-ignore a11y_media_has_caption -->
							<video
								src={getMediaURL(message)}
								class="media-preview video-preview file-inline-preview"
								controls
								preload="metadata"
								on:error={() => onMediaError(message.id)}
							></video>
						{/if}
						<div class="file-card">
							<div class="file-meta">
								<IconSet name="file" size={16} />
								<div>
									<div class="file-name">{getFileName(message)}</div>
									<div class="file-ext">{getFileExtension(message).toUpperCase() || 'FILE'}</div>
								</div>
							</div>
							<div class="file-actions">
								<a href={getMediaURL(message)} target="_blank" rel="noreferrer" class="file-link"
									>Open</a
								>
								<a
									href={getMediaURL(message)}
									target="_blank"
									rel="noreferrer"
									download
									class="file-link"
								>
									Download
								</a>
							</div>
						</div>
						{#if getMediaCaption(message)}
							<div class="media-caption">{getMediaCaption(message)}</div>
						{/if}
					{:else if isCodeBlock(message.content)}
						<pre class="code-block"><code>{getCodeContent(message.content)}</code></pre>
					{:else}
						{message.content}
					{/if}
				</div>
				{#if message.type === 'text' && isLongMessage(message.content)}
					<button
						type="button"
						class="read-more-btn"
						on:click|stopPropagation={() => dispatch('toggleExpand', { messageId: message.id })}
					>
						{isMessageExpanded(message.id) ? 'Read less' : 'Read more'}
					</button>
				{/if}
			</article>
		{/each}
	</div>

	{#if !isMember}
		<div class="join-footer">
			<button type="button" class="join-room-btn" on:click={() => dispatch('joinRoom')}>
				Join Room
			</button>
		</div>
	{/if}
</div>

<style>
	.messages-shell {
		flex: 1;
		min-height: 0;
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	.messages {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.9rem;
		overflow-x: hidden;
		width: 100%;
		box-sizing: border-box;
		background: linear-gradient(180deg, #f7f8fa 0%, #f1f4f8 100%);
	}

	.readonly-banner {
		margin: 0 0 0.4rem;
		padding: 0.45rem 0.65rem;
		border-radius: 8px;
		border: 1px solid #dadada;
		background: #f3f3f3;
		color: #202020;
		font-size: 0.78rem;
	}

	.join-footer {
		border-top: 1px solid #dadada;
		background: #ffffff;
		padding: 0.7rem;
		display: flex;
		justify-content: center;
	}

	.join-room-btn {
		border: 1px solid #111111;
		background: #111111;
		color: #ffffff;
		border-radius: 8px;
		padding: 0.55rem 0.9rem;
		font-weight: 600;
		cursor: pointer;
	}

	.empty-thread {
		color: #666666;
		font-size: 0.84rem;
		padding: 1rem;
	}

	.bubble {
		position: relative;
		max-width: min(76%, 40rem);
		border-radius: 12px;
		padding: 0.76rem 0.86rem;
		background: #ffffff;
		border: 1px solid #d7dce4;
		box-shadow: 0 2px 6px rgba(15, 23, 42, 0.05);
		box-sizing: border-box;
		overflow: visible;
	}

	.selection-mode .bubble.selectable {
		cursor: pointer;
		outline: 1px dashed transparent;
	}

	.selection-mode .bubble.selectable:hover {
		outline-color: #334155;
	}

	.bubble.mine {
		align-self: flex-end;
		background: #273341;
		border-color: #273341;
		color: #f3f5f8;
	}

	.bubble.media-bubble {
		width: min(100%, 42rem);
		max-width: min(100%, 42rem);
		min-width: 0;
	}

	.bubble.pending {
		opacity: 0.65;
	}

	.bubble.focused {
		box-shadow:
			0 0 0 2px rgba(245, 158, 11, 0.95),
			0 8px 18px rgba(15, 23, 42, 0.14);
	}

	.copy-btn {
		position: absolute;
		top: 0.35rem;
		right: 0.35rem;
		border: 1px solid #cfcfcf;
		background: rgba(255, 255, 255, 0.92);
		color: #1e1e1e;
		border-radius: 6px;
		padding: 0.2rem;
		opacity: 0.82;
		transform: scale(1);
		transition:
			opacity 120ms ease,
			transform 120ms ease;
		cursor: pointer;
	}

	.bubble:hover .copy-btn {
		opacity: 1;
		transform: scale(1.14);
	}

	.bubble.mine .copy-btn {
		border-color: #454545;
		background: rgba(17, 17, 17, 0.85);
		color: #f7f7f7;
	}

	.copied-tip {
		position: absolute;
		top: -0.7rem;
		right: 1.8rem;
		font-size: 0.68rem;
		background: #111111;
		color: #ffffff;
		padding: 0.15rem 0.36rem;
		border-radius: 999px;
	}

	.bubble-meta {
		display: flex;
		justify-content: space-between;
		gap: 0.75rem;
		font-size: 0.74rem;
		color: #5e5e5e;
		margin-bottom: 0.44rem;
	}

	.bubble.mine .bubble-meta {
		color: #d8d8d8;
	}

	.meta-right {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
	}

	.break-indicator {
		display: inline-flex;
		align-items: center;
		gap: 0.2rem;
		border: 1px solid #cfcfcf;
		border-radius: 999px;
		background: #ffffff;
		color: #111111;
		padding: 0.08rem 0.33rem;
		font-size: 0.68rem;
		cursor: pointer;
	}

	.media-preview {
		display: block;
		width: 100%;
		max-width: none;
		border-radius: 8px;
		border: 1px solid #d1d1d1;
		box-sizing: border-box;
	}

	.image-preview {
		height: auto;
		max-height: 460px;
		object-fit: contain;
		background: #f0f0f0;
	}

	.video-preview {
		max-height: 360px;
		background: #111111;
	}

	.file-link {
		color: #111111;
		font-weight: 600;
		text-decoration: none;
		font-size: 0.8rem;
	}

	.file-link:hover {
		text-decoration: underline;
	}

	.file-card {
		display: flex;
		flex-direction: column;
		gap: 0.45rem;
		border: 1px solid #d1d1d1;
		border-radius: 10px;
		background: #f4f4f4;
		padding: 0.5rem 0.62rem;
		width: 100%;
		max-width: none;
		box-sizing: border-box;
	}

	.file-meta {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		color: #141414;
	}

	.file-name {
		font-size: 0.82rem;
		font-weight: 600;
		line-height: 1.2;
		word-break: break-word;
	}

	.file-ext {
		font-size: 0.68rem;
		color: #666666;
		margin-top: 0.1rem;
	}

	.file-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.7rem;
	}

	.pdf-preview {
		width: 100%;
		height: 260px;
		border: 1px solid #d0d0d0;
		border-radius: 8px;
		background: #ffffff;
		box-sizing: border-box;
	}

	.file-inline-preview {
		margin-bottom: 0.45rem;
	}

	.bubble-content {
		font-size: 0.93rem;
		line-height: 1.52;
		color: #161616;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.bubble.mine .bubble-content {
		color: #f2f2f2;
	}

	.media-caption {
		margin-top: 0.48rem;
		font-size: 0.9rem;
		line-height: 1.45;
		color: #181818;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.bubble.mine .media-caption {
		color: #e6e6e6;
	}

	.bubble-content.collapsed {
		max-height: 300px;
		overflow: hidden;
		mask-image: linear-gradient(180deg, #000 70%, transparent);
		-webkit-mask-image: linear-gradient(180deg, #000 70%, transparent);
	}

	.code-block {
		margin: 0;
		padding: 0.65rem 0.72rem;
		border-radius: 8px;
		background: #0f0f0f;
		color: #e9e9e9;
		font-family:
			ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
		font-size: 0.83rem;
		line-height: 1.4;
		overflow-x: auto;
		white-space: pre;
		word-break: normal;
	}

	.read-more-btn {
		margin-top: 0.5rem;
		border: none;
		background: transparent;
		color: #161616;
		font-size: 0.78rem;
		font-weight: 600;
		padding: 0;
		cursor: pointer;
	}

	.bubble.mine .read-more-btn {
		color: #f2f2f2;
	}

	@media (max-width: 900px) {
		.messages {
			padding: 0.82rem 0.68rem;
		}

		.bubble {
			max-width: min(96%, 36rem);
			padding: 0.68rem 0.72rem;
		}

		.bubble.media-bubble {
			width: min(100%, 36rem);
			max-width: min(100%, 36rem);
		}

		.video-preview {
			max-height: 300px;
		}

		.pdf-preview {
			height: 220px;
		}
	}
</style>
