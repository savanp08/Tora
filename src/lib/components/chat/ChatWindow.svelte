<script lang="ts">
	import { afterUpdate, createEventDispatcher, onDestroy, onMount } from 'svelte';
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
		isEdited?: boolean;
		editedAt?: number;
		isDeleted?: boolean;
		replyToMessageId?: string;
		replyToSnippet?: string;
		totalReplies?: number;
		branchesCreated?: number;
		createdAt: number;
		hasBreakRoom?: boolean;
		breakRoomId?: string;
		breakJoinCount?: number;
		pending?: boolean;
	};

	type ReplyPreview = {
		messageId: string;
		author: string;
		content: string;
	};

	export let messages: ChatMessage[] = [];
	export let currentUserId = '';
	export let roomMessageSearch = '';
	export let expandedMessages: Record<string, boolean> = {};
	export let isMember = true;
	export let isSelectionMode = false;
	export let focusMessageId = '';
	export let isLoadingOlder = false;
	export let hasMoreOlder = true;

	const dispatch = createEventDispatcher<{
		toggleExpand: { messageId: string };
		joinBreakRoom: { roomId: string };
		joinRoom: void;
		messageSelect: { messageId: string };
		focusHandled: { messageId: string };
		reply: { messageId: string; senderName: string; content: string };
		editMessage: { messageId: string; content: string };
		deleteMessage: { messageId: string };
		requestOlder: void;
	}>();

	const COLLAPSED_MESSAGE_LENGTH = 500;

	let viewport: HTMLDivElement | null = null;
	let previousVisibleCount = 0;
	let copiedMessageId = '';
	let copyResetTimer: ReturnType<typeof setTimeout> | null = null;
	let mediaLoadFailedById: Record<string, boolean> = {};
	let focusedMessageId = '';
	let clearFocusOnPointerDown: ((event: PointerEvent) => void) | null = null;
	let isNearBottom = true;
	let showScrollToBottom = false;
	let topSentinel: HTMLDivElement | null = null;
	let topObserver: IntersectionObserver | null = null;
	let olderRequestPending = false;

	$: if (!focusMessageId && focusedMessageId) {
		focusedMessageId = '';
	}

	$: visibleMessages = getVisibleMessages(messages, roomMessageSearch);
	$: replyCountByMessageID = buildReplyCountByMessageID(messages);

	afterUpdate(() => {
		if (!viewport) {
			return;
		}
		if (visibleMessages.length !== previousVisibleCount) {
			const shouldStickToBottom = previousVisibleCount === 0 || isNearBottom;
			previousVisibleCount = visibleMessages.length;
			if (shouldStickToBottom) {
				scrollToBottom('instant');
			} else {
				updateScrollState();
			}
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
		if (topObserver) {
			topObserver.disconnect();
			topObserver = null;
		}
	});

	onMount(() => {
		setupTopObserver();
		return () => {
			if (topObserver) {
				topObserver.disconnect();
				topObserver = null;
			}
		};
	});

	$: if (viewport && topSentinel) {
		setupTopObserver();
	}

	$: if (!isLoadingOlder) {
		olderRequestPending = false;
	}

	function tryFocusMessage() {
		if (!focusMessageId) {
			return;
		}
		const focused = focusMessageInViewport(focusMessageId);
		if (!focused) {
			return;
		}
		dispatch('focusHandled', { messageId: focusMessageId });
	}

	function focusMessageInViewport(messageID: string) {
		const target = findMessageNode(messageID);
		if (!target) {
			return false;
		}
		target.scrollIntoView({ behavior: 'smooth', block: 'center' });
		updateScrollState();
		focusedMessageId = messageID;
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
		return true;
	}

	function findMessageNode(messageID: string) {
		if (!viewport || !messageID) {
			return null;
		}
		const nodes = viewport.querySelectorAll<HTMLElement>('[data-message-id]');
		for (const node of nodes) {
			if (node.dataset.messageId === messageID) {
				return node;
			}
		}
		return null;
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

	function updateScrollState() {
		if (!viewport) {
			return;
		}
		const distanceFromBottom = viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop;
		isNearBottom = distanceFromBottom < 96;
		showScrollToBottom = distanceFromBottom > Math.max(viewport.clientHeight, 300);
	}

	function onMessagesScroll() {
		updateScrollState();
	}

	function scrollToBottom(behavior: ScrollBehavior = 'smooth') {
		if (!viewport) {
			return;
		}
		viewport.scrollTo({ top: viewport.scrollHeight, behavior });
		updateScrollState();
	}

	function setupTopObserver() {
		if (typeof IntersectionObserver === 'undefined' || !viewport || !topSentinel) {
			return;
		}
		if (topObserver) {
			topObserver.disconnect();
		}
		topObserver = new IntersectionObserver(
			(entries) => {
				for (const entry of entries) {
					if (!entry.isIntersecting) {
						continue;
					}
					maybeRequestOlder();
				}
			},
			{
				root: viewport,
				threshold: 0.01
			}
		);
		topObserver.observe(topSentinel);
	}

	function maybeRequestOlder() {
		if (olderRequestPending || isLoadingOlder || !hasMoreOlder) {
			return;
		}
		if (visibleMessages.length === 0) {
			return;
		}
		olderRequestPending = true;
		dispatch('requestOlder');
	}

	type PrependAnchor = {
		scrollTop: number;
		scrollHeight: number;
	};

	export function capturePrependAnchor(): PrependAnchor | null {
		if (!viewport) {
			return null;
		}
		return {
			scrollTop: viewport.scrollTop,
			scrollHeight: viewport.scrollHeight
		};
	}

	export function restorePrependAnchor(anchor: PrependAnchor | null) {
		if (!viewport || !anchor) {
			return;
		}
		const nextScrollHeight = viewport.scrollHeight;
		const delta = nextScrollHeight - anchor.scrollHeight;
		viewport.scrollTop = anchor.scrollTop + delta;
		updateScrollState();
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

	function buildReplyCountByMessageID(input: ChatMessage[]) {
		const counts: Record<string, number> = {};
		for (const message of input) {
			const targetID = (message.replyToMessageId || '').trim();
			if (!targetID) {
				continue;
			}
			counts[targetID] = (counts[targetID] ?? 0) + 1;
		}
		return counts;
	}

	function getTotalReplies(message: ChatMessage) {
		const serverTotal = Number.isFinite(message.totalReplies) ? Number(message.totalReplies) : 0;
		const visibleTotal = replyCountByMessageID[message.id] ?? 0;
		return Math.max(serverTotal, visibleTotal);
	}

	function getBranchesCreated(message: ChatMessage) {
		const reported = Number.isFinite(message.branchesCreated)
			? Number(message.branchesCreated)
			: 0;
		if (reported > 0) {
			return reported;
		}
		return message.hasBreakRoom ? 1 : 0;
	}

	function getReplyPreview(message: ChatMessage): ReplyPreview | null {
		const messageID = (message.replyToMessageId || '').trim();
		const rawSnippet = (message.replyToSnippet || '').trim();
		if (!messageID && !rawSnippet) {
			return null;
		}
		if (!rawSnippet) {
			return {
				messageId: messageID,
				author: 'Original',
				content: 'Preview unavailable'
			};
		}

		const separatorIndex = rawSnippet.indexOf(':');
		if (separatorIndex <= 0) {
			return {
				messageId: messageID,
				author: 'Original',
				content: truncateInlineText(rawSnippet, 260)
			};
		}

		const author = rawSnippet.slice(0, separatorIndex).trim() || 'Original';
		const content = rawSnippet.slice(separatorIndex + 1).trim();
		return {
			messageId: messageID,
			author,
			content: truncateInlineText(content || 'Message', 260)
		};
	}

	function jumpToReplyTarget(message: ChatMessage) {
		const targetID = (message.replyToMessageId || '').trim();
		if (!targetID) {
			return;
		}
		focusMessageInViewport(targetID);
	}

	function truncateInlineText(value: string, maxLength: number) {
		if (value.length <= maxLength) {
			return value;
		}
		return `${value.slice(0, maxLength - 3)}...`;
	}

	function getReplyDispatchContent(message: ChatMessage) {
		const textContent = (message.content || '').trim();
		if (textContent) {
			return truncateInlineText(textContent, 220);
		}
		if (message.type === 'image') {
			return 'Image';
		}
		if (message.type === 'video') {
			return 'Video';
		}
		if (message.type === 'file') {
			return getFileName(message);
		}
		return 'Message';
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

	function formatEditedClock(timestamp: number | undefined) {
		if (!Number.isFinite(timestamp) || !timestamp) {
			return '';
		}
		const safe = Number(timestamp);
		return new Date(safe).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
	}

	function isDeletedMessage(message: ChatMessage) {
		if (message.isDeleted) {
			return true;
		}
		if ((message.type || '').toLowerCase() === 'deleted') {
			return true;
		}
		return (message.content || '').trim() === 'This message was deleted';
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

	function onEditMessage(message: ChatMessage) {
		dispatch('editMessage', {
			messageId: message.id,
			content: message.content
		});
	}

	function onDeleteMessage(message: ChatMessage) {
		dispatch('deleteMessage', {
			messageId: message.id
		});
	}
</script>

<div class="messages-shell {isSelectionMode ? 'selection-mode' : ''}">
	<div class="messages" bind:this={viewport} on:scroll={onMessagesScroll}>
		<div class="top-sentinel" bind:this={topSentinel} aria-hidden="true"></div>
		{#if isLoadingOlder}
			<div class="older-history-indicator">Loading older messages...</div>
		{/if}
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
			{@const isMine = message.senderId === currentUserId}
			{@const totalReplies = getTotalReplies(message)}
			{@const branchesCreated = getBranchesCreated(message)}
			{@const replyPreview = getReplyPreview(message)}
			<div class="message-row {isMine ? 'mine' : 'theirs'}">
				{#if isMine}
					<aside class="message-gutter">
						{#if totalReplies > 1}
							<div class="gutter-stat" title={`${totalReplies} replies`}>
								<IconSet name="reply" size={10} className="gutter-icon" />
								<strong>{totalReplies}</strong>
							</div>
						{/if}
						{#if branchesCreated > 1}
							<div class="gutter-stat" title={`${branchesCreated} branches`}>
								<IconSet name="break" size={10} className="gutter-icon" />
								<strong>{branchesCreated}</strong>
							</div>
						{/if}
					</aside>
				{/if}
				<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
				<article
					class="bubble {isMine ? 'mine' : 'theirs'} {message.pending ? 'pending' : ''} {isSelectionMode
						? 'selectable'
						: ''}"
					class:media-bubble={isMediaBubble(message)}
					class:deleted={isDeletedMessage(message)}
					class:focused={focusedMessageId === message.id}
					role={isSelectionMode ? 'button' : undefined}
					tabindex={isSelectionMode ? 0 : undefined}
					data-message-id={message.id}
					on:click={() => onMessageClick(message)}
					on:keydown={(event) => onMessageKeyDown(event, message)}
				>
					<div class="bubble-meta">
						<span>{message.senderName}</span>
						<div class="meta-right">
							<span class="time-meta">
								<time>{formatClock(message.createdAt)}</time>
								{#if message.isEdited && !isDeletedMessage(message)}
									<span class="edited-meta">(edited at {formatEditedClock(message.editedAt)})</span>
								{/if}
								{#if copiedMessageId === message.id}
									<span class="copied-tip">Copied</span>
								{/if}
								<button
									type="button"
									class="copy-btn"
									title="Copy message"
									aria-label="Copy message"
									on:click|stopPropagation={() => void copyMessage(message)}
								>
									<IconSet name="copy" size={12} className="copy-icon" />
								</button>
							</span>
							{#if isMine && !isDeletedMessage(message)}
								<button
									type="button"
									class="message-action-btn"
									title="Edit message"
									aria-label="Edit message"
									on:click|stopPropagation={() => onEditMessage(message)}
								>
									<IconSet name="edit" size={12} className="message-action-icon" />
								</button>
								<button
									type="button"
									class="message-action-btn danger"
									title="Delete message"
									aria-label="Delete message"
									on:click|stopPropagation={() => onDeleteMessage(message)}
								>
									<IconSet name="trash" size={12} className="message-action-icon" />
								</button>
							{/if}
							{#if !isDeletedMessage(message)}
							<button
								type="button"
								class="reply-edge-btn {isMine ? 'mine' : 'theirs'}"
								title="Reply"
								aria-label="Reply"
								on:click|stopPropagation={() =>
									dispatch('reply', {
										messageId: message.id,
										senderName: message.senderName,
										content: getReplyDispatchContent(message)
									})}
							>
								<IconSet name="reply" size={12} className="reply-edge-icon" />
							</button>
							{/if}
							{#if message.hasBreakRoom && message.breakRoomId}
								<button
									type="button"
									class="break-indicator"
									title={`Join break room (${formatBreakCount(message.breakJoinCount)} joined)`}
									aria-label={`Join break room (${formatBreakCount(message.breakJoinCount)} joined)`}
									on:click|stopPropagation={() =>
										dispatch('joinBreakRoom', { roomId: message.breakRoomId || '' })}
								>
									<IconSet name="break" size={12} className="break-indicator-icon" />
									<span class="break-indicator-count">{formatBreakCount(message.breakJoinCount)}</span>
								</button>
							{/if}
						</div>
					</div>
					{#if replyPreview}
						<button
							type="button"
							class="reply-snippet"
							title="Jump to original message"
							aria-label="Jump to original message"
							on:click|stopPropagation={() => jumpToReplyTarget(message)}
						>
							<span class="reply-snippet-author">{replyPreview.author}</span>
							<span class="reply-snippet-content">{replyPreview.content}</span>
						</button>
					{/if}
					<div
						class="bubble-content"
						class:deleted-text={isDeletedMessage(message)}
						class:collapsed={message.type === 'text' &&
							isLongMessage(message.content) &&
							!isMessageExpanded(message.id)}
					>
						{#if isDeletedMessage(message)}
							This message was deleted
						{:else if message.type === 'image' && getMediaURL(message) && !mediaLoadFailedById[message.id]}
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
				{#if !isMine}
					<aside class="message-gutter">
						{#if totalReplies > 1}
							<div class="gutter-stat" title={`${totalReplies} replies`}>
								<IconSet name="reply" size={10} className="gutter-icon" />
								<strong>{totalReplies}</strong>
							</div>
						{/if}
						{#if branchesCreated > 1}
							<div class="gutter-stat" title={`${branchesCreated} branches`}>
								<IconSet name="break" size={10} className="gutter-icon" />
								<strong>{branchesCreated}</strong>
							</div>
						{/if}
					</aside>
				{/if}
			</div>
		{/each}
	</div>
	{#if showScrollToBottom}
		<button
			type="button"
			class="scroll-bottom-button"
			on:click={() => scrollToBottom('smooth')}
			aria-label="Scroll to latest message"
			title="Scroll to latest"
		>
			<IconSet name="chevron-down" size={20} />
		</button>
	{/if}

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
		position: relative;
	}

	.messages {
		--meta-gutter-size: clamp(2.6rem, 7vw, 3.1rem);
		--action-icon-size: clamp(1.2rem, 2.8vw, 1.5rem);
		--action-hit-size: clamp(1.76rem, 3.7vw, 2.2rem);
		--counter-icon-size: clamp(1rem, 2.4vw, 1.25rem);
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
		background: linear-gradient(180deg, #f7f7f8 0%, #f1f1f3 100%);
	}

	.top-sentinel {
		height: 1px;
		width: 100%;
	}

	.older-history-indicator {
		align-self: center;
		margin: 0.12rem 0 0.15rem;
		font-size: 0.72rem;
		color: #71717a;
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

	.scroll-bottom-button {
		position: absolute;
		right: 1rem;
		bottom: 1rem;
		width: 2.4rem;
		height: 2.4rem;
		border: 1px solid #d0d0d7;
		border-radius: 999px;
		background: rgba(250, 250, 251, 0.95);
		color: #2f2f37;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		box-shadow: 0 8px 18px rgba(0, 0, 0, 0.16);
		z-index: 3;
	}

	.scroll-bottom-button:hover {
		background: #efeff2;
	}

	.empty-thread {
		color: #666666;
		font-size: 0.84rem;
		padding: 1rem;
	}

	.message-row {
		display: flex;
		align-items: flex-start;
		gap: 0.5rem;
		width: 100%;
	}

	.message-row.mine {
		justify-content: flex-end;
	}

	.message-row.theirs {
		justify-content: flex-start;
	}

	.message-gutter {
		flex: 0 0 var(--meta-gutter-size);
		width: var(--meta-gutter-size);
		min-height: 1rem;
		padding-top: 0.2rem;
		display: flex;
		flex-direction: column;
		gap: 0.2rem;
		align-items: center;
	}

	.gutter-stat {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 0.22rem;
		width: 100%;
		font-size: 0.66rem;
		line-height: 1.2;
		color: #7a7a82;
		background: rgba(255, 255, 255, 0.62);
		border: 1px solid #dfdfe5;
		border-radius: 999px;
		padding: 0.16rem 0.22rem;
	}

	.message-row.mine .gutter-stat {
		background: rgba(47, 49, 56, 0.92);
		border-color: #4a4c55;
		color: #d6d7dd;
	}

	.gutter-stat strong {
		font-size: 0.66rem;
		font-weight: 600;
		color: inherit;
	}

	.bubble {
		position: relative;
		max-width: min(calc(100% - var(--meta-gutter-size) - 0.6rem), 40rem);
		width: fit-content;
		border-radius: 12px;
		padding: 0.76rem 0.86rem;
		background: #ffffff;
		border: 1px solid #d9d9de;
		box-shadow: 0 2px 6px rgba(0, 0, 0, 0.05);
		box-sizing: border-box;
		overflow: visible;
	}

	.selection-mode .bubble.selectable {
		cursor: pointer;
		outline: 1px dashed transparent;
	}

	.selection-mode .bubble.selectable:hover {
		outline-color: #4a4a54;
	}

	.bubble.mine {
		background: #2f3138;
		border-color: #2f3138;
		color: #f3f5f8;
	}

	.bubble.media-bubble {
		width: min(calc(100% - var(--meta-gutter-size) - 0.6rem), 42rem);
		max-width: min(calc(100% - var(--meta-gutter-size) - 0.6rem), 42rem);
		min-width: 0;
	}

	.bubble.pending {
		opacity: 0.65;
	}

	.bubble.focused {
		box-shadow:
			0 0 0 2px rgba(245, 158, 11, 0.95),
			0 8px 18px rgba(0, 0, 0, 0.14);
	}

	.bubble.deleted {
		background: #f5f5f7;
		border-color: #e1e1e7;
		color: #6f6f79;
	}

	.bubble.mine.deleted {
		background: #3a3c45;
		border-color: #4a4d57;
		color: #c6c8d1;
	}

	.copy-btn {
		position: absolute;
		left: 50%;
		top: 50%;
		transform: translate(-50%, -50%);
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: var(--action-hit-size);
		height: var(--action-hit-size);
		border: none;
		border-radius: 999px;
		background: rgba(19, 19, 24, 0.85);
		color: #ffffff;
		opacity: 0;
		pointer-events: none;
		cursor: pointer;
		transition: opacity 140ms ease;
		padding: 0;
	}

	.time-meta:hover .copy-btn,
	.time-meta:focus-within .copy-btn {
		opacity: 0.9;
		pointer-events: auto;
	}

	.time-meta:hover time,
	.time-meta:focus-within time {
		opacity: 0.16;
	}

	.copy-btn:hover {
		opacity: 1;
	}

	.copied-tip {
		position: absolute;
		left: calc(100% + 0.25rem);
		top: 50%;
		transform: translateY(-50%);
		white-space: nowrap;
		font-size: 0.68rem;
		color: inherit;
		opacity: 0.85;
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

	.time-meta {
		position: relative;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-width: 3rem;
	}

	.time-meta time {
		transition: opacity 120ms ease;
	}

	.edited-meta {
		margin-left: 0.2rem;
		font-size: 0.68rem;
		opacity: 0.78;
	}

	.message-action-btn {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: var(--action-hit-size);
		height: var(--action-hit-size);
		border: 1px solid #d5d5dc;
		border-radius: 999px;
		background: #f8f8fb;
		color: #45454f;
		opacity: 0;
		pointer-events: none;
		cursor: pointer;
		padding: 0;
		transition: opacity 140ms ease;
	}

	.bubble.mine .message-action-btn {
		border-color: rgba(255, 255, 255, 0.2);
		background: rgba(255, 255, 255, 0.12);
		color: #e6e7ec;
	}

	.message-action-btn.danger {
		color: #8f2d2d;
	}

	.bubble.mine .message-action-btn.danger {
		color: #ffd4d4;
	}

	.message-row:hover .message-action-btn,
	.message-row:focus-within .message-action-btn,
	.message-action-btn:hover,
	.message-action-btn:focus-visible {
		opacity: 0.86;
		pointer-events: auto;
	}

	.message-action-btn:hover {
		opacity: 1;
	}

	.reply-edge-btn {
		position: absolute;
		top: 0.55rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: var(--action-hit-size);
		height: var(--action-hit-size);
		border: 1px solid #d7d7dd;
		border-radius: 999px;
		background: #fdfdfe;
		color: #44444d;
		opacity: 0;
		pointer-events: none;
		cursor: pointer;
		transition: opacity 140ms ease;
		padding: 0;
		z-index: 1;
	}

	.reply-edge-btn.mine {
		left: calc(-1 * var(--meta-gutter-size) + (var(--meta-gutter-size) - var(--action-hit-size)) / 2);
	}

	.reply-edge-btn.theirs {
		right: calc(-1 * var(--meta-gutter-size) + (var(--meta-gutter-size) - var(--action-hit-size)) / 2);
	}

	.message-row:hover .reply-edge-btn,
	.message-row:focus-within .reply-edge-btn,
	.reply-edge-btn:hover,
	.reply-edge-btn:focus-visible {
		opacity: 0.82;
		pointer-events: auto;
	}

	.reply-edge-btn:hover {
		opacity: 1;
	}

	.reply-snippet {
		width: 100%;
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		margin-bottom: 0.4rem;
		padding: 0.34rem 0.48rem;
		border-radius: 8px;
		border: 1px solid #dfdfe5;
		background: #f2f2f4;
		color: #4f4f58;
		font-size: 0.7rem;
		line-height: 1.25;
		word-break: break-word;
		text-align: left;
		cursor: pointer;
	}

	.bubble.mine .reply-snippet {
		border-color: rgba(255, 255, 255, 0.22);
		background: rgba(255, 255, 255, 0.12);
		color: #d9d9df;
	}

	.reply-snippet:hover {
		background: #ececf0;
	}

	.bubble.mine .reply-snippet:hover {
		background: rgba(255, 255, 255, 0.18);
	}

	.reply-snippet-author {
		font-size: 0.68rem;
		font-weight: 700;
		letter-spacing: 0.01em;
		opacity: 0.9;
		margin-bottom: 0.1rem;
	}

	.reply-snippet-content {
		display: -webkit-box;
		line-clamp: 3;
		-webkit-line-clamp: 3;
		-webkit-box-orient: vertical;
		overflow: hidden;
		font-size: 0.74rem;
		line-height: 1.3;
		opacity: 0.95;
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

	.break-indicator-count {
		font-size: 0.74rem;
		font-weight: 700;
		line-height: 1;
		min-width: 1.2ch;
		text-align: center;
	}

	:global(.copy-icon),
	:global(.reply-edge-icon) {
		width: var(--action-icon-size);
		height: var(--action-icon-size);
	}

	:global(.gutter-icon) {
		width: var(--counter-icon-size);
		height: var(--counter-icon-size);
	}

	:global(.break-indicator-icon) {
		width: var(--counter-icon-size);
		height: var(--counter-icon-size);
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

	.bubble-content.deleted-text {
		font-style: italic;
		color: #6d6d76;
	}

	.bubble.mine .bubble-content {
		color: #f2f2f2;
	}

	.bubble.mine .bubble-content.deleted-text {
		color: #d3d4dc;
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
			--meta-gutter-size: clamp(2.45rem, 12vw, 2.9rem);
			padding: 0.82rem 0.68rem;
		}

		.bubble {
			max-width: min(calc(100% - var(--meta-gutter-size) - 0.45rem), 36rem);
			padding: 0.68rem 0.72rem;
		}

		.bubble.media-bubble {
			width: min(calc(100% - var(--meta-gutter-size) - 0.45rem), 36rem);
			max-width: min(calc(100% - var(--meta-gutter-size) - 0.45rem), 36rem);
		}

		.gutter-stat {
			padding: 0.1rem 0.14rem;
		}

		.reply-edge-btn {
			position: static;
			opacity: 0.72;
			pointer-events: auto;
		}

		.message-action-btn {
			opacity: 0.74;
			pointer-events: auto;
		}

		.reply-edge-btn.mine,
		.reply-edge-btn.theirs {
			left: auto;
			right: auto;
		}

		.time-meta .copy-btn,
		.time-meta .copied-tip {
			display: none;
		}

		.video-preview {
			max-height: 300px;
		}

		.pdf-preview {
			height: 220px;
		}

		.scroll-bottom-button {
			right: 0.8rem;
			bottom: 0.8rem;
		}
	}
</style>
