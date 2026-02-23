<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import TaskCard from '$lib/components/chat/TaskCard.svelte';
	import type { ChatMessage } from '$lib/types/chat';
	import { normalizeIdentifier, normalizeMessageID } from '$lib/utils/chat/core';

	type CommentRow = {
		comment: ChatMessage;
		depth: number;
		parentId: string;
	};

	export let open = false;
	export let taskMessage: ChatMessage | null = null;
	export let comments: ChatMessage[] = [];
	export let isDarkMode = false;
	export let canEditTask = false;
	export let currentUserId = '';
	export let backgroundUnreadCount = 0;

	let draftComment = '';
	let replyTargetId = '';
	let previousTaskId = '';

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
	$: commentRows = buildCommentRows(comments);
	$: replyTargetMessage = commentById.get(normalizeMessageID(replyTargetId)) || null;
	$: if (taskId !== previousTaskId) {
		draftComment = '';
		replyTargetId = '';
		previousTaskId = taskId;
	}

	function closeDrawer() {
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

	function buildCommentRows(discussionComments: ChatMessage[]) {
		if (discussionComments.length === 0) {
			return [] as CommentRow[];
		}
		const childrenByParent = new Map<string, ChatMessage[]>();
		for (const entry of discussionComments) {
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
		const walk = (parentId: string, depth: number) => {
			const children = childrenByParent.get(parentId) ?? [];
			for (const child of children) {
				const childId = normalizeMessageID(child.id);
				if (!childId || seen.has(childId)) {
					continue;
				}
				seen.add(childId);
				rows.push({
					comment: child,
					depth: Math.min(depth, 6),
					parentId
				});
				walk(childId, depth + 1);
			}
		};

		walk('', 0);
		return rows;
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
				&lt;
			</button>
			<button
				type="button"
				class="nav-arrow right"
				title="Next pinned discussion"
				on:click={() => dispatch('navigateNext')}
			>
				&gt;
			</button>

			<header class="discussion-header">
				<div>
					<h3>Pinned Discussion</h3>
					<p>Threaded comments with replies</p>
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
				<div class="discussion-pinned-task">
					<TaskCard
						message={taskMessage}
						showDiscussButton={false}
						showAddTaskControl={canEditTask}
						canEditTasks={canEditTask}
						on:toggleTask={(event) => dispatch('toggleTask', event.detail)}
						on:addTask={(event) => dispatch('addTask', event.detail)}
					/>
				</div>
			{/if}

			<section class="discussion-comments" aria-label="Threaded comments">
				{#if commentRows.length === 0}
					<div class="discussion-empty">No comments yet. Start the first comment below.</div>
				{:else}
					{#each commentRows as row (row.comment.id)}
						<article
							class="comment-row {row.comment.isPinned ? 'pinned' : ''}"
							style={`--depth:${Math.min(row.depth, 6)};`}
						>
							<div class="comment-meta">
								<strong>{row.comment.senderName}</strong>
								{#if row.comment.senderId === opSenderId}
									<span class="op-badge">[OP]</span>
								{/if}
								{#if row.comment.isPinned}
									<span class="pin-badge">📌 Pinned</span>
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
		align-items: center;
		justify-content: center;
		padding: 1.1rem;
		background: rgba(10, 16, 27, 0.42);
		backdrop-filter: blur(8px);
		-webkit-backdrop-filter: blur(8px);
	}

	.discussion-modal {
		position: relative;
		width: min(920px, 100%);
		height: min(88vh, 920px);
		background: linear-gradient(180deg, #f4f8ff 0%, #e9eff8 100%);
		border: 1px solid #c9d4e5;
		border-radius: 16px;
		display: grid;
		grid-template-rows: auto auto minmax(0, 1fr) auto;
		overflow: hidden;
		box-shadow: 0 22px 50px rgba(2, 8, 23, 0.28);
	}

	.theme-dark .discussion-modal {
		background: linear-gradient(180deg, #0f1b31 0%, #0b1528 100%);
		border-color: #30435f;
		box-shadow: 0 24px 56px rgba(2, 8, 23, 0.58);
	}

	.discussion-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.7rem;
		padding: 0.88rem 1rem;
		border-bottom: 1px solid #ced8e8;
		background: rgba(255, 255, 255, 0.72);
	}

	.theme-dark .discussion-header {
		border-bottom-color: #30445f;
		background: rgba(12, 22, 39, 0.85);
	}

	.discussion-header h3 {
		margin: 0;
		font-size: 0.97rem;
		font-weight: 700;
		color: #1f2a3a;
	}

	.discussion-header p {
		margin: 0.12rem 0 0;
		font-size: 0.74rem;
		color: #60738f;
	}

	.theme-dark .discussion-header h3 {
		color: #dbe8ff;
	}

	.theme-dark .discussion-header p {
		color: #9fb8d8;
	}

	.header-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
	}

	.background-activity {
		padding: 0.24rem 0.58rem;
		border-radius: 999px;
		font-size: 0.68rem;
		font-weight: 700;
		border: 1px solid #c9d9ec;
		background: #f3f8ff;
		color: #355177;
		white-space: nowrap;
	}

	.theme-dark .background-activity {
		border-color: #3c5f89;
		background: #10243f;
		color: #bdd7ff;
	}

	.close-button {
		border: 1px solid #bfcde2;
		background: #f8fbff;
		color: #334155;
		border-radius: 8px;
		padding: 0.3rem 0.62rem;
		font-size: 0.76rem;
		font-weight: 600;
		cursor: pointer;
	}

	.theme-dark .close-button {
		border-color: #3a4f70;
		background: #10223e;
		color: #dbeafe;
	}

	.discussion-pinned-task {
		padding: 0.92rem 1rem 0.78rem;
		border-bottom: 1px solid #d2dceb;
		background: inherit;
	}

	.theme-dark .discussion-pinned-task {
		border-bottom-color: #30445f;
	}

	.discussion-comments {
		overflow-y: auto;
		padding: 0.9rem 1rem 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.62rem;
	}

	.discussion-empty {
		padding: 0.72rem;
		border-radius: 10px;
		border: 1px dashed #c7d2e5;
		font-size: 0.8rem;
		color: #4b5d79;
		background: rgba(243, 247, 255, 0.88);
	}

	.theme-dark .discussion-empty {
		border-color: #36507a;
		color: #b8cdee;
		background: rgba(15, 27, 45, 0.82);
	}

	.comment-row {
		margin-left: calc(var(--depth, 0) * 1rem);
		border: 1px solid #ccd8e9;
		border-radius: 11px;
		background: #f8fbff;
		padding: 0.58rem 0.65rem;
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
	}

	.theme-dark .comment-row {
		border-color: #36507a;
		background: #122139;
	}

	.comment-row.pinned {
		border-left: 4px solid #f59e0b;
		background: #fff9e7;
	}

	.theme-dark .comment-row.pinned {
		border-left-color: #fbbf24;
		background: #31280f;
	}

	.comment-meta {
		display: flex;
		align-items: center;
		gap: 0.42rem;
		font-size: 0.73rem;
		color: #5b6f8d;
	}

	.theme-dark .comment-meta {
		color: #a9bfdc;
	}

	.comment-meta strong {
		font-size: 0.77rem;
		color: #1f2d43;
	}

	.theme-dark .comment-meta strong {
		color: #d7e6ff;
	}

	.reply-ref {
		font-size: 0.69rem;
		color: #637a99;
	}

	.theme-dark .reply-ref {
		color: #9fb8da;
	}

	.op-badge {
		padding: 0.06rem 0.36rem;
		border-radius: 999px;
		border: 1px solid #7c93b8;
		color: #38507a;
		font-size: 0.66rem;
		font-weight: 700;
	}

	.theme-dark .op-badge {
		border-color: #7aa2db;
		color: #b8d6ff;
	}

	.pin-badge {
		color: #92400e;
		font-weight: 700;
		font-size: 0.68rem;
	}

	.theme-dark .pin-badge {
		color: #fcd34d;
	}

	.comment-row p {
		margin: 0;
		font-size: 0.82rem;
		line-height: 1.4;
		color: #223249;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.theme-dark .comment-row p {
		color: #dce9ff;
	}

	.comment-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.36rem;
	}

	.comment-actions button {
		border: 1px solid #c5d2e6;
		background: #f8fbff;
		color: #2f4464;
		border-radius: 8px;
		padding: 0.2rem 0.46rem;
		font-size: 0.68rem;
		font-weight: 700;
		cursor: pointer;
	}

	.comment-actions button.danger {
		border-color: #ef4444;
		color: #991b1b;
		background: #fff1f2;
	}

	.theme-dark .comment-actions button {
		border-color: #3d577f;
		background: #112640;
		color: #d3e5fc;
	}

	.theme-dark .comment-actions button.danger {
		border-color: #f87171;
		background: rgba(127, 29, 29, 0.28);
		color: #fecaca;
	}

	.discussion-composer {
		border-top: 1px solid #cfd9e8;
		padding: 0.76rem 1rem 0.9rem;
		display: flex;
		flex-direction: column;
		gap: 0.45rem;
		background: rgba(255, 255, 255, 0.72);
	}

	.theme-dark .discussion-composer {
		border-top-color: #2f4667;
		background: rgba(12, 22, 39, 0.86);
	}

	.reply-target {
		display: inline-flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		border: 1px solid #c7d4e6;
		background: #f4f8ff;
		border-radius: 9px;
		padding: 0.32rem 0.45rem;
		font-size: 0.72rem;
		color: #355177;
	}

	.reply-target button {
		border: 1px solid #c1d0e4;
		background: #f8fbff;
		color: #2f4a71;
		border-radius: 7px;
		padding: 0.18rem 0.42rem;
		font-size: 0.68rem;
		font-weight: 700;
		cursor: pointer;
	}

	.theme-dark .reply-target {
		border-color: #3d5f8b;
		background: #10243f;
		color: #bbd5ff;
	}

	.theme-dark .reply-target button {
		border-color: #3c5f89;
		background: #132744;
		color: #cfe2ff;
	}

	.discussion-composer textarea {
		width: 100%;
		min-height: 72px;
		max-height: 180px;
		resize: vertical;
		border: 1px solid #c5d2e5;
		background: #f8fbff;
		color: #1f2f45;
		border-radius: 10px;
		padding: 0.46rem 0.58rem;
		font-size: 0.84rem;
		line-height: 1.35;
		box-sizing: border-box;
		font-family: inherit;
	}

	.theme-dark .discussion-composer textarea {
		border-color: #3d5c85;
		background: #10213a;
		color: #deebff;
	}

	.composer-actions {
		display: flex;
		justify-content: flex-end;
	}

	.send-comment {
		border: 1px solid #0284c7;
		background: linear-gradient(180deg, #38bdf8 0%, #0284c7 100%);
		color: #ffffff;
		border-radius: 9px;
		padding: 0.34rem 0.68rem;
		font-size: 0.74rem;
		font-weight: 700;
		cursor: pointer;
	}

	.nav-arrow {
		position: absolute;
		top: 50%;
		transform: translateY(-50%);
		width: 2.15rem;
		height: 3rem;
		border: 1px solid #c8d4e7;
		background: rgba(255, 255, 255, 0.92);
		color: #2c3e5b;
		font-size: 1.12rem;
		font-weight: 700;
		border-radius: 12px;
		cursor: pointer;
		z-index: 5;
	}

	.theme-dark .nav-arrow {
		border-color: #3a5478;
		background: rgba(15, 28, 47, 0.94);
		color: #d7e8ff;
	}

	.nav-arrow.left {
		left: -1.08rem;
	}

	.nav-arrow.right {
		right: -1.08rem;
	}

	@media (max-width: 980px) {
		.discussion-shell {
			padding: 0.56rem;
		}

		.discussion-modal {
			width: 100%;
			height: min(92vh, 960px);
		}

		.nav-arrow.left {
			left: 0.44rem;
		}

		.nav-arrow.right {
			right: 0.44rem;
		}
	}

	@media (max-width: 700px) {
		.comment-row {
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
