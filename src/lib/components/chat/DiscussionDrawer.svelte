<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import TaskCard from '$lib/components/chat/TaskCard.svelte';
	import type { ChatMessage } from '$lib/types/chat';
	import { normalizeIdentifier, normalizeMessageID } from '$lib/utils/chat/core';

	type CommentMessageRow = {
		type: 'comment';
		comment: ChatMessage;
		depth: number;
		parentId: string;
	};

	type CommentContinuationRow = {
		type: 'continuation';
		depth: number;
		parentId: string;
		hiddenCount: number;
	};

	type CommentRow = CommentMessageRow | CommentContinuationRow;

	export let open = false;
	export let taskMessage: ChatMessage | null = null;
	export let comments: ChatMessage[] = [];
	export let isDarkMode = false;
	export let canEditTask = false;
	export let currentUserId = '';
	export let backgroundUnreadCount = 0;

	let draftComment = '';
	let replyTargetId = '';
	let focusedThreadRootId = '';
	let previousTaskId = '';
	const maxInlineThreadDepth = 6;

	const dispatch = createEventDispatcher<{
		close: void;
		navigatePrevious: void;
		navigateNext: void;
		toggleTask: { messageId: string; taskIndex: number };
		addTask: { messageId: string; text: string };
		submitComment: { content: string; replyToMessageId?: string };
		editComment: { messageId: string; content: string };
		deleteComment: { messageId: string };
	}>();

	$: taskId = normalizeMessageID(taskMessage?.id || '');
	$: opSenderId = taskMessage?.senderId || '';
	$: commentById = new Map(comments.map((entry) => [normalizeMessageID(entry.id), entry]));
	$: commentRows = buildCommentRows(comments, focusedThreadRootId);
	$: replyTargetMessage = commentById.get(normalizeMessageID(replyTargetId)) || null;
	$: focusedThreadMessage = commentById.get(normalizeMessageID(focusedThreadRootId)) || null;
	$: if (taskId !== previousTaskId) {
		draftComment = '';
		replyTargetId = '';
		focusedThreadRootId = '';
		previousTaskId = taskId;
	}

	function closeDrawer() {
		replyTargetId = '';
		focusedThreadRootId = '';
		dispatch('close');
	}

	function formatCommentTime(timestamp: number) {
		if (!Number.isFinite(timestamp) || timestamp <= 0) {
			return '';
		}
		return new Date(timestamp).toLocaleString([], {
			month: 'short',
			day: 'numeric',
			hour: 'numeric',
			minute: '2-digit'
		});
	}

	function getCommentPreview(comment: ChatMessage) {
		if (comment.type === 'deleted' || comment.isDeleted) {
			return 'This message was deleted';
		}
		if (comment.type === 'image') {
			return 'Photo';
		}
		if (comment.type === 'video') {
			return 'Video';
		}
		if (comment.type === 'audio') {
			return 'Voice message';
		}
		if (comment.type === 'file') {
			return comment.fileName ? `File: ${comment.fileName}` : 'Attachment';
		}
		if (comment.type === 'task') {
			return 'Task update';
		}
		return (comment.content || '').trim() || 'Message';
	}

	function onBackdropClick(event: MouseEvent) {
		if (event.target === event.currentTarget) {
			closeDrawer();
		}
	}

	function onDialogKeyDown(event: KeyboardEvent) {
		if (event.key !== 'Escape') {
			return;
		}
		event.preventDefault();
		if (replyTargetId) {
			replyTargetId = '';
			return;
		}
		closeDrawer();
	}

	function isOwnComment(comment: ChatMessage) {
		return normalizeIdentifier(comment.senderId) === normalizeIdentifier(currentUserId);
	}

	function isOpComment(comment: ChatMessage) {
		return normalizeIdentifier(comment.senderId) === normalizeIdentifier(opSenderId);
	}

	function buildCommentRows(discussionComments: ChatMessage[], focusRootId: string) {
		if (discussionComments.length === 0) {
			return [] as CommentRow[];
		}

		const childrenByParent = new Map<string, ChatMessage[]>();
		const commentsById = new Map<string, ChatMessage>();
		for (const entry of discussionComments) {
			const entryId = normalizeMessageID(entry.id);
			if (entryId) {
				commentsById.set(entryId, entry);
			}
			const parentId = normalizeMessageID(entry.replyToMessageId || '');
			const bucket = childrenByParent.get(parentId) ?? [];
			bucket.push(entry);
			childrenByParent.set(parentId, bucket);
		}
		for (const bucket of childrenByParent.values()) {
			bucket.sort((a, b) => a.createdAt - b.createdAt);
		}

		const rows: CommentRow[] = [];
		const seen = new Set<string>();

		const countHiddenDescendants = (parentId: string, localSeen = new Set<string>()) => {
			const children = childrenByParent.get(parentId) ?? [];
			let count = 0;
			for (const child of children) {
				const childId = normalizeMessageID(child.id);
				if (!childId || localSeen.has(childId)) {
					continue;
				}
				localSeen.add(childId);
				count += 1 + countHiddenDescendants(childId, localSeen);
			}
			return count;
		};

		const walk = (parentId: string, depth: number) => {
			const children = childrenByParent.get(parentId) ?? [];
			for (const child of children) {
				const childId = normalizeMessageID(child.id);
				if (!childId || seen.has(childId)) {
					continue;
				}
				seen.add(childId);
				rows.push({
					type: 'comment',
					comment: child,
					depth: Math.min(depth, maxInlineThreadDepth),
					parentId
				});
				if (depth >= maxInlineThreadDepth) {
					const hiddenCount = countHiddenDescendants(childId);
					if (hiddenCount > 0) {
						rows.push({
							type: 'continuation',
							depth: Math.min(depth + 1, maxInlineThreadDepth),
							parentId: childId,
							hiddenCount
						});
					}
					continue;
				}
				walk(childId, depth + 1);
			}
		};

		const normalizedFocusRootId = normalizeMessageID(focusRootId);
		if (normalizedFocusRootId && commentsById.has(normalizedFocusRootId)) {
			const focusedComment = commentsById.get(normalizedFocusRootId);
			if (focusedComment) {
				rows.push({
					type: 'comment',
					comment: focusedComment,
					depth: 0,
					parentId: normalizeMessageID(focusedComment.replyToMessageId || '')
				});
				seen.add(normalizedFocusRootId);
				walk(normalizedFocusRootId, 1);
			}
			return rows;
		}

		walk('', 0);
		return rows;
	}

	function focusDeeperReplies(parentId: string) {
		const normalizedParentId = normalizeMessageID(parentId);
		if (!normalizedParentId || !commentById.has(normalizedParentId)) {
			return;
		}
		replyTargetId = '';
		focusedThreadRootId = normalizedParentId;
	}

	function clearFocusedThread() {
		focusedThreadRootId = '';
	}

	function startReply(comment: ChatMessage) {
		if (comment.type === 'deleted' || comment.isDeleted) {
			return;
		}
		replyTargetId = normalizeMessageID(comment.id);
	}

	function cancelReply() {
		replyTargetId = '';
	}

	function submitComment() {
		const content = (draftComment || '').trim();
		if (!content) {
			return;
		}
		dispatch('submitComment', {
			content,
			replyToMessageId: normalizeMessageID(replyTargetId)
		});
		draftComment = '';
		replyTargetId = '';
	}

	function onComposerKeyDown(event: KeyboardEvent) {
		if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
			event.preventDefault();
			submitComment();
			return;
		}
		if (event.key === 'Escape' && replyTargetId) {
			event.preventDefault();
			cancelReply();
		}
	}

	function requestEdit(comment: ChatMessage) {
		if (!isOwnComment(comment)) {
			return;
		}
		dispatch('editComment', {
			messageId: comment.id,
			content: comment.content || ''
		});
	}

	function requestDelete(comment: ChatMessage) {
		if (!isOwnComment(comment)) {
			return;
		}
		dispatch('deleteComment', {
			messageId: comment.id
		});
	}
</script>

{#if open}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<div
		class="discussion-shell {isDarkMode ? 'theme-dark' : ''}"
		role="presentation"
		on:click={onBackdropClick}
	>
		<div
			class="discussion-modal"
			role="dialog"
			aria-modal="true"
			aria-label="Task discussion"
			tabindex="-1"
			on:keydown={onDialogKeyDown}
		>
			<button
				type="button"
				class="nav-arrow left"
				title="Previous pinned discussion"
				on:click={() => dispatch('navigatePrevious')}
			>
				<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.1">
					<path d="m15 5-7 7 7 7"></path>
				</svg>
			</button>
			<button
				type="button"
				class="nav-arrow right"
				title="Next pinned discussion"
				on:click={() => dispatch('navigateNext')}
			>
				<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.1">
					<path d="m9 5 7 7-7 7"></path>
				</svg>
			</button>

			<header class="discussion-header">
				<div class="header-copy">
					<h3>Pinned Discussion</h3>
					<p>Deep dive thread for this task</p>
				</div>
				<div class="header-actions">
					{#if backgroundUnreadCount > 0}
						<div class="background-activity">
							{backgroundUnreadCount} new message{backgroundUnreadCount === 1 ? '' : 's'} in chat
						</div>
					{/if}
					<button type="button" class="close-button" on:click={closeDrawer}>Close</button>
				</div>
			</header>

			{#if taskMessage}
				<div class="discussion-pinned-wrap">
					<div class="discussion-pinned-task">
						<TaskCard
							message={taskMessage}
							showAddTaskControl={canEditTask}
							canEditTasks={canEditTask}
							on:toggleTask={(event) => dispatch('toggleTask', event.detail)}
							on:addTask={(event) => dispatch('addTask', event.detail)}
						/>
					</div>
				</div>
			{/if}

			<section class="discussion-comments" aria-label="Threaded comments">
				{#if focusedThreadMessage}
					<div class="thread-focus-banner">
						<span>Showing deeper replies for @{focusedThreadMessage.senderName}</span>
						<button type="button" on:click={clearFocusedThread}>Back to full thread</button>
					</div>
				{/if}
				{#if commentRows.length === 0}
					<div class="discussion-empty">No comments yet. Start the first comment below.</div>
				{:else}
					{#each commentRows as row (row.type === 'comment' ? row.comment.id : `continued-${row.parentId}`)}
						{#if row.type === 'comment'}
							<article
								class="comment-row {row.comment.isPinned ? 'pinned' : ''}"
								style={`--depth:${Math.min(row.depth, maxInlineThreadDepth)};`}
							>
								{#if row.comment.isPinned}
									<div class="pinned-op-label">📌 Pinned by OP</div>
								{/if}
								<div class="comment-meta">
									<strong>{row.comment.senderName}</strong>
									{#if isOpComment(row.comment)}
										<span class="op-badge">OP</span>
									{/if}
									<time>{formatCommentTime(row.comment.createdAt)}</time>
								</div>
								{#if normalizeMessageID(row.parentId) !== ''}
									{@const parentComment = commentById.get(normalizeMessageID(row.parentId))}
									{#if parentComment}
										<div class="reply-ref">↳ @{parentComment.senderName}</div>
									{/if}
								{/if}
								<p>{getCommentPreview(row.comment)}</p>
								<div class="comment-actions">
									<button type="button" on:click={() => startReply(row.comment)}>Reply</button>
									{#if isOwnComment(row.comment) && row.comment.type !== 'deleted' && !row.comment.isDeleted}
										<button type="button" on:click={() => requestEdit(row.comment)}>Edit</button>
										<button type="button" class="danger" on:click={() => requestDelete(row.comment)}>
											Delete
										</button>
									{/if}
								</div>
							</article>
						{:else}
							<div class="thread-continued" style={`--depth:${Math.min(row.depth, maxInlineThreadDepth)};`}>
								<span>Thread continued ({row.hiddenCount} more repl{row.hiddenCount === 1 ? 'y' : 'ies'})</span>
								<button type="button" on:click={() => focusDeeperReplies(row.parentId)}>
									View deeper replies
								</button>
							</div>
						{/if}
					{/each}
				{/if}
			</section>

			<footer class="discussion-composer">
				{#if replyTargetMessage}
					<div class="reply-target">
						<span>Replying to @{replyTargetMessage.senderName}</span>
						<button type="button" on:click={cancelReply}>Cancel</button>
					</div>
				{/if}
				<textarea
					bind:value={draftComment}
					rows="2"
					placeholder="Write a comment... (Ctrl/Cmd + Enter to send)"
					on:keydown={onComposerKeyDown}
				></textarea>
				<div class="composer-actions">
					<button type="button" class="send-comment" on:click={submitComment}>Comment</button>
				</div>
			</footer>
		</div>
	</div>
{/if}

<style>
	.discussion-shell {
		position: fixed;
		inset: 0;
		z-index: 460;
		display: flex;
		justify-content: flex-end;
		padding: 1rem;
		background: rgba(10, 16, 27, 0.42);
		backdrop-filter: blur(8px);
		-webkit-backdrop-filter: blur(8px);
	}

	.discussion-modal {
		--drawer-bg: #f5f5f5;
		--drawer-border: #d4d4d8;
		--drawer-text: #18181b;
		--drawer-muted: #71717a;
		--comment-bg: #ffffff;
		--comment-border: #e4e4e7;
		position: relative;
		width: min(760px, 100%);
		height: calc(100vh - 2rem);
		background: var(--drawer-bg);
		border: 1px solid var(--drawer-border);
		border-radius: 16px;
		display: grid;
		grid-template-rows: auto auto minmax(0, 1fr) auto;
		overflow: hidden;
		box-shadow: 0 24px 56px rgba(2, 8, 23, 0.42);
		transform: translateX(36px);
		opacity: 0;
		animation: drawer-slide-in 220ms ease forwards;
	}

	.theme-dark .discussion-modal {
		--drawer-bg: #121214;
		--drawer-border: #27272a;
		--drawer-text: #f4f4f5;
		--drawer-muted: #a1a1aa;
		--comment-bg: #18181b;
		--comment-border: #2f2f35;
		box-shadow: 0 24px 56px rgba(0, 0, 0, 0.62);
	}

	.discussion-header {
		position: sticky;
		top: 0;
		z-index: 10;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		padding: 0.88rem 1rem;
		border-bottom: 1px solid var(--drawer-border);
		backdrop-filter: blur(12px);
		background: rgba(255, 255, 255, 0.76);
	}

	.theme-dark .discussion-header {
		background: rgba(24, 24, 27, 0.7);
	}

	.header-copy h3 {
		margin: 0;
		font-size: 0.96rem;
		font-weight: 600;
		color: var(--drawer-text);
	}

	.header-copy p {
		margin: 0.12rem 0 0;
		font-size: 0.74rem;
		color: var(--drawer-muted);
	}

	.header-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
	}

	.background-activity {
		padding: 0.24rem 0.6rem;
		border-radius: 999px;
		font-size: 0.68rem;
		font-weight: 600;
		white-space: nowrap;
		background: rgba(239, 68, 68, 0.08);
		color: #ef4444;
	}

	.close-button {
		border: 1px solid var(--drawer-border);
		background: transparent;
		color: var(--drawer-muted);
		border-radius: 8px;
		padding: 0.3rem 0.62rem;
		font-size: 0.76rem;
		font-weight: 600;
		cursor: pointer;
		transition: background-color 0.16s ease;
	}

	.close-button:hover {
		background: rgba(113, 113, 122, 0.12);
	}

	.discussion-pinned-wrap {
		padding: 0.92rem 1rem 1rem;
		border-bottom: 1px solid var(--drawer-border);
	}

	.discussion-pinned-task {
		border-radius: 13px;
		box-shadow: 0 10px 30px rgba(0, 0, 0, 0.3);
	}

	.discussion-comments {
		overflow-y: auto;
		padding: 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.66rem;
	}

	.discussion-empty {
		padding: 0.8rem;
		border-radius: 10px;
		border: 1px dashed var(--drawer-border);
		font-size: 0.8rem;
		color: var(--drawer-muted);
	}

	.thread-focus-banner {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		padding: 0.44rem 0.58rem;
		border: 1px solid var(--comment-border);
		border-radius: 10px;
		background: rgba(113, 113, 122, 0.08);
		font-size: 0.72rem;
		color: var(--drawer-muted);
	}

	.thread-focus-banner button {
		border: 1px solid var(--comment-border);
		background: transparent;
		color: var(--drawer-muted);
		border-radius: 8px;
		padding: 0.22rem 0.44rem;
		font-size: 0.68rem;
		font-weight: 600;
		cursor: pointer;
	}

	.comment-row {
		margin-left: calc(var(--depth, 0) * 0.95rem);
		border: 1px solid var(--comment-border);
		border-radius: 12px;
		background: var(--comment-bg);
		padding: 0.62rem 0.68rem;
		display: flex;
		flex-direction: column;
		gap: 0.34rem;
	}

	.comment-row.pinned {
		border-left: 3px solid #f59e0b;
		background:
			linear-gradient(90deg, rgba(245, 158, 11, 0.18) 0%, rgba(245, 158, 11, 0) 72%),
			var(--comment-bg);
	}

	.pinned-op-label {
		font-size: 0.66rem;
		font-weight: 600;
		color: #f59e0b;
	}

	.comment-meta {
		display: flex;
		align-items: center;
		gap: 0.44rem;
		font-size: 0.73rem;
		color: var(--drawer-muted);
	}

	.comment-meta strong {
		font-size: 0.78rem;
		color: var(--drawer-text);
	}

	.op-badge {
		background: rgba(16, 185, 129, 0.15);
		color: #10b981;
		padding: 2px 8px;
		border-radius: 9999px;
		font-size: 0.65rem;
		font-weight: 700;
		text-transform: uppercase;
	}

	.reply-ref {
		font-size: 0.69rem;
		color: var(--drawer-muted);
	}

	.comment-row p {
		margin: 0;
		font-size: 0.82rem;
		line-height: 1.4;
		color: var(--drawer-text);
		white-space: pre-wrap;
		word-break: break-word;
	}

	.comment-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.36rem;
	}

	.comment-actions button {
		border: 1px solid var(--comment-border);
		background: transparent;
		color: var(--drawer-muted);
		border-radius: 8px;
		padding: 0.22rem 0.48rem;
		font-size: 0.68rem;
		font-weight: 600;
		cursor: pointer;
	}

	.comment-actions button:hover {
		background: rgba(113, 113, 122, 0.12);
	}

	.comment-actions button.danger {
		border-color: rgba(239, 68, 68, 0.42);
		color: #ef4444;
	}

	.thread-continued {
		margin-left: calc(var(--depth, 0) * 0.95rem);
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		border: 1px dashed var(--comment-border);
		border-radius: 11px;
		padding: 0.42rem 0.58rem;
		font-size: 0.72rem;
		color: var(--drawer-muted);
		background: rgba(113, 113, 122, 0.06);
	}

	.thread-continued button {
		border: 1px solid var(--comment-border);
		background: transparent;
		color: var(--drawer-text);
		border-radius: 8px;
		padding: 0.2rem 0.48rem;
		font-size: 0.68rem;
		font-weight: 600;
		cursor: pointer;
	}

	.discussion-composer {
		border-top: 1px solid var(--drawer-border);
		padding: 0.78rem 1rem 0.92rem;
		display: flex;
		flex-direction: column;
		gap: 0.46rem;
		background: rgba(255, 255, 255, 0.72);
	}

	.theme-dark .discussion-composer {
		background: rgba(18, 18, 20, 0.82);
	}

	.reply-target {
		display: inline-flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		border: 1px solid var(--comment-border);
		background: transparent;
		border-radius: 9px;
		padding: 0.32rem 0.46rem;
		font-size: 0.72rem;
		color: var(--drawer-muted);
	}

	.reply-target button {
		border: 1px solid var(--comment-border);
		background: transparent;
		color: inherit;
		border-radius: 7px;
		padding: 0.18rem 0.42rem;
		font-size: 0.68rem;
		font-weight: 600;
		cursor: pointer;
	}

	.discussion-composer textarea {
		width: 100%;
		min-height: 74px;
		max-height: 180px;
		resize: vertical;
		border: 1px solid var(--comment-border);
		background: transparent;
		color: var(--drawer-text);
		border-radius: 10px;
		padding: 0.48rem 0.58rem;
		font-size: 0.84rem;
		line-height: 1.35;
		box-sizing: border-box;
		font-family: inherit;
	}

	.composer-actions {
		display: flex;
		justify-content: flex-end;
	}

	.send-comment {
		border: none;
		border-radius: 9px;
		padding: 0.36rem 0.72rem;
		font-size: 0.74rem;
		font-weight: 600;
		cursor: pointer;
		background: rgba(239, 68, 68, 0.12);
		color: #ef4444;
	}

	.send-comment:hover {
		background: rgba(239, 68, 68, 0.2);
	}

	.nav-arrow {
		position: absolute;
		top: 80px;
		width: 36px;
		height: 36px;
		border-radius: 9999px;
		border: 1px solid var(--drawer-border);
		background: rgba(255, 255, 255, 0.8);
		color: var(--drawer-text);
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		z-index: 14;
		transition: background-color 0.16s ease;
	}

	.theme-dark .nav-arrow {
		background: rgba(24, 24, 27, 0.86);
	}

	.nav-arrow svg {
		width: 16px;
		height: 16px;
	}

	.nav-arrow:hover {
		background: rgba(113, 113, 122, 0.2);
	}

	.nav-arrow.left {
		left: -18px;
	}

	.nav-arrow.right {
		right: -18px;
	}

	@keyframes drawer-slide-in {
		from {
			transform: translateX(36px);
			opacity: 0;
		}
		to {
			transform: translateX(0);
			opacity: 1;
		}
	}

	@media (max-width: 980px) {
		.discussion-shell {
			padding: 0.6rem;
		}

		.discussion-modal {
			width: 100%;
			height: calc(100vh - 1.2rem);
		}

		.nav-arrow.left {
			left: 8px;
		}

		.nav-arrow.right {
			right: 8px;
		}
	}

	@media (max-width: 700px) {
		.comment-row {
			margin-left: calc(var(--depth, 0) * 0.58rem);
		}

		.thread-continued {
			margin-left: calc(var(--depth, 0) * 0.58rem);
		}

		.discussion-header {
			flex-direction: column;
			align-items: flex-start;
		}

		.header-actions {
			width: 100%;
			justify-content: space-between;
		}
	}
</style>
