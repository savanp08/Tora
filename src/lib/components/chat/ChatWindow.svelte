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

	const dispatch = createEventDispatcher<{
		toggleExpand: { messageId: string };
		joinBreakRoom: { roomId: string };
		joinRoom: void;
		messageSelect: { messageId: string };
	}>();

	const COLLAPSED_MESSAGE_LENGTH = 500;

	let viewport: HTMLDivElement | null = null;
	let previousVisibleCount = 0;
	let copiedMessageId = '';
	let copyResetTimer: ReturnType<typeof setTimeout> | null = null;
	let mediaLoadFailedById: Record<string, boolean> = {};

	$: visibleMessages = getVisibleMessages(messages, roomMessageSearch);

	afterUpdate(() => {
		if (!viewport) {
			return;
		}
		if (visibleMessages.length !== previousVisibleCount) {
			previousVisibleCount = visibleMessages.length;
			viewport.scrollTop = viewport.scrollHeight;
		}
	});

	onDestroy(() => {
		if (copyResetTimer) {
			clearTimeout(copyResetTimer);
		}
	});

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
				role={isSelectionMode ? 'button' : undefined}
				tabindex={isSelectionMode ? 0 : undefined}
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
					{:else if message.type === 'video' && getMediaURL(message) && !mediaLoadFailedById[message.id]}
						<!-- svelte-ignore a11y_media_has_caption -->
						<video
							src={getMediaURL(message)}
							class="media-preview video-preview"
							controls
							preload="metadata"
							on:error={() => onMediaError(message.id)}
						></video>
					{:else if (message.type === 'file' || mediaLoadFailedById[message.id]) && getMediaURL(message)}
						{#if isPDFMessage(message)}
							<iframe
								class="pdf-preview"
								src={getMediaURL(message)}
								title={getFileName(message)}
								loading="lazy"
							></iframe>
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
	}

	.messages {
		height: 100%;
		overflow: auto;
		padding: 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.72rem;
	}

	.readonly-banner {
		margin: 0 0 0.4rem;
		padding: 0.45rem 0.65rem;
		border-radius: 8px;
		border: 1px solid #f8ddb2;
		background: #fff8e1;
		color: #7c4a03;
		font-size: 0.78rem;
	}

	.join-footer {
		border-top: 1px solid #d9dee4;
		background: #f6f8fa;
		padding: 0.7rem;
		display: flex;
		justify-content: center;
	}

	.join-room-btn {
		border: 1px solid #15803d;
		background: #16a34a;
		color: #ffffff;
		border-radius: 8px;
		padding: 0.55rem 0.9rem;
		font-weight: 600;
		cursor: pointer;
	}

	.empty-thread {
		color: #64748b;
		font-size: 0.84rem;
		padding: 1rem;
	}

	.bubble {
		position: relative;
		max-width: min(75%, 540px);
		border-radius: 12px;
		padding: 0.58rem 0.7rem;
		background: #ffffff;
		box-shadow: 0 1px 2px rgba(15, 23, 42, 0.08);
	}

	.selection-mode .bubble.selectable {
		cursor: pointer;
		outline: 1px dashed transparent;
	}

	.selection-mode .bubble.selectable:hover {
		outline-color: #16a34a;
	}

	.bubble.mine {
		align-self: flex-end;
		background: #dcf8c6;
	}

	.bubble.pending {
		opacity: 0.65;
	}

	.copy-btn {
		position: absolute;
		top: 0.35rem;
		right: 0.35rem;
		border: none;
		background: rgba(255, 255, 255, 0.85);
		color: #1e293b;
		border-radius: 6px;
		padding: 0.2rem;
		opacity: 0.55;
		transform: scale(1);
		transition:
			opacity 120ms ease,
			transform 120ms ease;
		cursor: pointer;
	}

	.bubble:hover .copy-btn {
		opacity: 1;
		transform: scale(1.2);
	}

	.copied-tip {
		position: absolute;
		top: -0.7rem;
		right: 1.8rem;
		font-size: 0.68rem;
		background: #0f172a;
		color: #ffffff;
		padding: 0.15rem 0.36rem;
		border-radius: 999px;
	}

	.bubble-meta {
		display: flex;
		justify-content: space-between;
		gap: 0.75rem;
		font-size: 0.72rem;
		color: #5b6472;
		margin-bottom: 0.28rem;
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
		border: 1px solid #d8e0e9;
		border-radius: 999px;
		background: #ffffff;
		color: #0f172a;
		padding: 0.08rem 0.33rem;
		font-size: 0.68rem;
		cursor: pointer;
	}

	.media-preview {
		display: block;
		max-width: min(100%, 360px);
		border-radius: 8px;
		border: 1px solid #d8e0e9;
	}

	.image-preview {
		height: auto;
	}

	.video-preview {
		max-height: 320px;
		background: #020617;
	}

	.file-link {
		color: #1d4ed8;
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
		border: 1px solid #d8e0e9;
		border-radius: 10px;
		background: #f8fafc;
		padding: 0.5rem 0.62rem;
		max-width: 360px;
	}

	.file-meta {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		color: #0f172a;
	}

	.file-name {
		font-size: 0.82rem;
		font-weight: 600;
		line-height: 1.2;
		word-break: break-word;
	}

	.file-ext {
		font-size: 0.68rem;
		color: #64748b;
		margin-top: 0.1rem;
	}

	.file-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.7rem;
	}

	.pdf-preview {
		width: min(100%, 360px);
		height: 260px;
		border: 1px solid #d8e0e9;
		border-radius: 8px;
		background: #ffffff;
	}

	.bubble-content {
		font-size: 0.89rem;
		line-height: 1.35;
		color: #142032;
		white-space: pre-wrap;
		word-break: break-word;
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
		background: #0f172a;
		color: #e2e8f0;
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
		margin-top: 0.35rem;
		border: none;
		background: transparent;
		color: #1d4ed8;
		font-size: 0.78rem;
		font-weight: 600;
		padding: 0;
		cursor: pointer;
	}
</style>
