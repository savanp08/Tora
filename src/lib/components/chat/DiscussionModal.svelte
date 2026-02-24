<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import TaskCard from '$lib/components/chat/TaskCard.svelte';
	import type { ChatMessage } from '$lib/types/chat';
	import { normalizeIdentifier, normalizeMessageID } from '$lib/utils/chat/core';

	type ThreadReplyRow = {
		comment: ChatMessage;
		depth: number;
		directReplyCount: number;
		repliesExpanded: boolean;
		remainingDirectReplies: number;
		canReply: boolean;
	};

	type ThreadEntry = {
		parent: ChatMessage;
		directReplyCount: number;
		repliesExpanded: boolean;
		remainingDirectReplies: number;
		canReply: boolean;
		replies: ThreadReplyRow[];
	};

	export let open = false;
	export let pinnedMessage: ChatMessage | null = null;
	export let comments: ChatMessage[] = [];
	export let roomId = '';
	export let isDarkMode = false;
	export let canEditTask = false;
	export let currentUserId = '';
	export let opUserId = '';
	export let backgroundUnreadCount = 0;

	let draftComment = '';
	let replyTargetId = '';
	let previousPinnedMessageId = '';
	let previousNotesStorageKey = '';
	let expandedRepliesByParent: Record<string, boolean> = {};
	let visibleRepliesByParent: Record<string, number> = {};
	let notes: string[] = [];
	let noteDraft = '';
	const MAX_REPLY_DEPTH = 4;
	const REPLIES_PAGE_SIZE = 5;
	const MAX_NOTES = 5;
	const NOTES_STORAGE_PREFIX = 'converse:pinned-notes:v1';

	const dispatch = createEventDispatcher<{
		close: void;
		navigatePrevious: void;
		navigateNext: void;
		toggleTask: { messageId: string; taskIndex: number };
		addTask: { messageId: string; text: string };
		submitComment: { content: string; replyToMessageId?: string };
		editComment: { messageId: string; content: string };
		deleteComment: { messageId: string };
		toggleCommentPin: { messageId: string; isPinned: boolean };
	}>();

	$: normalizedPinnedMessageId = normalizeMessageID(pinnedMessage?.id || '');
	$: normalizedOpUserId = normalizeIdentifier(opUserId || pinnedMessage?.senderId || '');
	$: commentById = new Map(comments.map((entry) => [normalizeMessageID(entry.id), entry]));
	$: childrenByParent = buildChildrenByParent(comments);
	$: commentDepthById = buildCommentDepthById(commentById);
	$: parentComments = buildParentComments(comments, commentById, normalizedOpUserId);
	$: threadEntries = parentComments.map((parent) =>
		buildThreadEntry(
			parent,
			childrenByParent,
			commentDepthById,
			expandedRepliesByParent,
			visibleRepliesByParent
		)
	);
	$: replyTargetMessage = commentById.get(normalizeMessageID(replyTargetId)) ?? null;
	$: isLimitReached = comments.length >= 50;
	$: notesLimitReached = notes.length >= MAX_NOTES;
	$: activeNotesStorageKey = buildNotesStorageKey(
		roomId,
		normalizedPinnedMessageId,
		normalizeIdentifier(currentUserId)
	);

	$: if (normalizedPinnedMessageId !== previousPinnedMessageId) {
		draftComment = '';
		replyTargetId = '';
		expandedRepliesByParent = {};
		visibleRepliesByParent = {};
		notes = loadStoredNotes(activeNotesStorageKey);
		noteDraft = '';
		previousPinnedMessageId = normalizedPinnedMessageId;
		previousNotesStorageKey = activeNotesStorageKey;
	}

	$: if (activeNotesStorageKey !== previousNotesStorageKey) {
		notes = loadStoredNotes(activeNotesStorageKey);
		previousNotesStorageKey = activeNotesStorageKey;
	}

	$: if (replyTargetId) {
		const target = commentById.get(normalizeMessageID(replyTargetId));
		if (!target || !canReplyToComment(target, commentDepthById)) {
			replyTargetId = '';
		}
	}

	$: persistStoredNotes(activeNotesStorageKey, notes);

	function closeModal() {
		dispatch('close');
	}

	function onBackdropClick(event: MouseEvent) {
		if (event.target === event.currentTarget) {
			closeModal();
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
		closeModal();
	}

	function buildChildrenByParent(items: ChatMessage[]) {
		const children = new Map<string, ChatMessage[]>();
		for (const item of items) {
			const parentId = normalizeMessageID(item.replyToMessageId || '');
			const bucket = children.get(parentId) ?? [];
			bucket.push(item);
			children.set(parentId, bucket);
		}
		for (const bucket of children.values()) {
			bucket.sort((left, right) => left.createdAt - right.createdAt);
		}
		return children;
	}

	function resolvePinPriority(comment: ChatMessage, normalizedOpId: string) {
		if (!comment.isPinned) {
			return 3;
		}
		const normalizedPinnedBy = normalizeIdentifier(comment.pinnedBy || '');
		if (normalizedOpId && normalizedPinnedBy === normalizedOpId) {
			return 1;
		}
		return 2;
	}

	function buildParentComments(
		items: ChatMessage[],
		commentMap: Map<string, ChatMessage>,
		normalizedOpId: string
	) {
		return [...items]
			.filter((item) => {
				const parentId = normalizeMessageID(item.replyToMessageId || '');
				return !parentId || !commentMap.has(parentId);
			})
				.sort((left, right) => {
					const leftPriority = resolvePinPriority(left, normalizedOpId);
					const rightPriority = resolvePinPriority(right, normalizedOpId);
					if (leftPriority !== rightPriority) {
						return leftPriority - rightPriority;
					}
					return left.createdAt - right.createdAt;
				});
	}

	function buildCommentDepthById(commentMap: Map<string, ChatMessage>) {
		const depthById = new Map<string, number>();
		const pending = new Set<string>();
		const computeDepth = (commentId: string): number => {
			const normalizedId = normalizeMessageID(commentId);
			if (!normalizedId) {
				return 1;
			}
			const cached = depthById.get(normalizedId);
			if (cached) {
				return cached;
			}
			if (pending.has(normalizedId)) {
				return 1;
			}
			const comment = commentMap.get(normalizedId);
			if (!comment) {
				return 1;
			}
			pending.add(normalizedId);
			const parentId = normalizeMessageID(comment.replyToMessageId || '');
			let depth = 1;
			if (parentId && commentMap.has(parentId)) {
				depth = Math.min(MAX_REPLY_DEPTH, computeDepth(parentId) + 1);
			}
			pending.delete(normalizedId);
			depthById.set(normalizedId, depth);
			return depth;
		};
		for (const commentId of commentMap.keys()) {
			computeDepth(commentId);
		}
		return depthById;
	}

	function getCommentDepth(comment: ChatMessage, depthById: Map<string, number>) {
		const commentId = normalizeMessageID(comment.id);
		return Math.max(1, Math.min(MAX_REPLY_DEPTH, depthById.get(commentId) ?? 1));
	}

	function canReplyToComment(comment: ChatMessage, depthById: Map<string, number>) {
		if (comment.type === 'deleted' || comment.isDeleted) {
			return false;
		}
		return getCommentDepth(comment, depthById) < MAX_REPLY_DEPTH;
	}

	function getVisibleReplyCount(
		parentId: string,
		totalReplies: number,
		visibleState: Record<string, number> = visibleRepliesByParent
	) {
		const normalizedParentId = normalizeMessageID(parentId);
		if (!normalizedParentId || totalReplies <= 0) {
			return 0;
		}
		const saved = visibleState[normalizedParentId];
		if (!Number.isFinite(saved) || saved <= 0) {
			return Math.min(REPLIES_PAGE_SIZE, totalReplies);
		}
		return Math.min(totalReplies, Math.max(1, Math.trunc(saved)));
	}

	function buildThreadEntry(
		parent: ChatMessage,
		children: Map<string, ChatMessage[]>,
		depthById: Map<string, number>,
		expandedState: Record<string, boolean>,
		visibleState: Record<string, number>
	): ThreadEntry {
		const parentId = normalizeMessageID(parent.id);
		const directReplies = parentId ? children.get(parentId) ?? [] : [];
		const repliesExpanded = isRepliesExpanded(parentId, expandedState);
		const visibleDirectReplies =
			repliesExpanded && directReplies.length > 0
				? Math.max(1, getVisibleReplyCount(parentId, directReplies.length, visibleState))
				: 0;
		const replies = repliesExpanded
			? buildReplyRows(
					parentId,
					children,
					depthById,
					2,
					visibleDirectReplies,
					expandedState,
					visibleState
				)
			: [];
		return {
			parent,
			directReplyCount: directReplies.length,
			repliesExpanded,
			remainingDirectReplies: repliesExpanded
				? Math.max(0, directReplies.length - visibleDirectReplies)
				: directReplies.length,
			canReply: canReplyToComment(parent, depthById),
			replies
		};
	}

	function buildReplyRows(
		parentId: string,
		children: Map<string, ChatMessage[]>,
		depthById: Map<string, number>,
		depth: number,
		parentVisibleCount: number,
		expandedState: Record<string, boolean>,
		visibleState: Record<string, number>
	): ThreadReplyRow[] {
		if (depth > MAX_REPLY_DEPTH) {
			return [];
		}
		const directReplies = children.get(parentId) ?? [];
		const visibleCount = Math.max(0, Math.min(parentVisibleCount, directReplies.length));
		const visibleReplies = directReplies.slice(0, visibleCount);
		const rows: ThreadReplyRow[] = [];
		for (const reply of visibleReplies) {
			const replyId = normalizeMessageID(reply.id);
			const replyDepth = getCommentDepth(reply, depthById);
			const childReplies = replyDepth < MAX_REPLY_DEPTH ? children.get(replyId) ?? [] : [];
			const repliesExpanded =
				replyDepth < MAX_REPLY_DEPTH && isRepliesExpanded(replyId, expandedState);
			const visibleDirectReplies =
				repliesExpanded && childReplies.length > 0
					? Math.max(1, getVisibleReplyCount(replyId, childReplies.length, visibleState))
					: 0;
			rows.push({
				comment: reply,
				depth: Math.min(depth, MAX_REPLY_DEPTH),
				directReplyCount: childReplies.length,
				repliesExpanded,
				remainingDirectReplies: repliesExpanded
					? Math.max(0, childReplies.length - visibleDirectReplies)
					: childReplies.length,
				canReply: canReplyToComment(reply, depthById)
			});
			if (replyId && childReplies.length > 0 && repliesExpanded && replyDepth < MAX_REPLY_DEPTH) {
				rows.push(
					...buildReplyRows(
						replyId,
						children,
						depthById,
						replyDepth + 1,
						visibleDirectReplies,
						expandedState,
						visibleState
					)
				);
			}
		}
		return rows;
	}

	function isOwnComment(comment: ChatMessage) {
		return normalizeIdentifier(comment.senderId) === normalizeIdentifier(currentUserId);
	}

	function isPinnedByOP(comment: ChatMessage) {
		if (!comment.isPinned) {
			return false;
		}
		const normalizedPinnedBy = normalizeIdentifier(comment.pinnedBy || '');
		return Boolean(normalizedOpUserId && normalizedPinnedBy === normalizedOpUserId);
	}

	function pinnedByLabel(comment: ChatMessage) {
		return comment.pinnedByName?.trim() || 'User';
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

	function formatReplyCountLabel(count: number) {
		const safeCount = Math.max(0, Math.trunc(count));
		return safeCount === 1 ? '1 reply' : `${safeCount} replies`;
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

	function startReply(comment: ChatMessage) {
		if (!canReplyToComment(comment, commentDepthById)) {
			return;
		}
		replyTargetId = normalizeMessageID(comment.id);
	}

	function cancelReply() {
		replyTargetId = '';
	}

	function submitComment() {
		if (isLimitReached) {
			return;
		}
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

	function toggleCommentPin(comment: ChatMessage) {
		dispatch('toggleCommentPin', {
			messageId: comment.id,
			isPinned: !Boolean(comment.isPinned)
		});
	}

	function toggleReplies(parentCommentId: string) {
		const normalizedId = normalizeMessageID(parentCommentId);
		if (!normalizedId) {
			return;
		}
		const nextExpanded = !expandedRepliesByParent[normalizedId];
		expandedRepliesByParent = {
			...expandedRepliesByParent,
			[normalizedId]: nextExpanded
		};
		if (nextExpanded) {
			const totalReplies = (childrenByParent.get(normalizedId) ?? []).length;
			if (totalReplies <= 0) {
				return;
			}
			const nextVisibleCount = getVisibleReplyCount(normalizedId, totalReplies);
			visibleRepliesByParent = {
				...visibleRepliesByParent,
				[normalizedId]: Math.max(1, nextVisibleCount)
			};
		}
	}

	function isRepliesExpanded(
		parentCommentId: string,
		expandedState: Record<string, boolean> = expandedRepliesByParent
	) {
		return Boolean(expandedState[normalizeMessageID(parentCommentId)]);
	}

	function showMoreReplies(parentCommentId: string) {
		const normalizedId = normalizeMessageID(parentCommentId);
		if (!normalizedId) {
			return;
		}
		const totalReplies = (childrenByParent.get(normalizedId) ?? []).length;
		if (totalReplies <= 0) {
			return;
		}
		const currentVisible = getVisibleReplyCount(normalizedId, totalReplies);
		const nextVisible = Math.min(totalReplies, currentVisible + REPLIES_PAGE_SIZE);
		expandedRepliesByParent = {
			...expandedRepliesByParent,
			[normalizedId]: true
		};
		visibleRepliesByParent = {
			...visibleRepliesByParent,
			[normalizedId]: nextVisible
		};
	}

	function addNote() {
		if (notesLimitReached) {
			return;
		}
		const value = noteDraft.trim();
		if (!value) {
			return;
		}
		notes = [...notes, value].slice(0, MAX_NOTES);
		noteDraft = '';
	}

	function onNoteKeyDown(event: KeyboardEvent) {
		if (event.key === 'Enter') {
			event.preventDefault();
			addNote();
		}
	}

	function buildNotesStorageKey(targetRoomId: string, pinMessageId: string, userId: string) {
		const normalizedRoomId = (targetRoomId || '').trim();
		const normalizedPinMessageId = normalizeMessageID(pinMessageId);
		const normalizedUserId = normalizeIdentifier(userId);
		if (!normalizedRoomId || !normalizedPinMessageId || !normalizedUserId) {
			return '';
		}
		return `${NOTES_STORAGE_PREFIX}:${normalizedRoomId}:${normalizedPinMessageId}:${normalizedUserId}`;
	}

	function loadStoredNotes(storageKey: string) {
		if (!storageKey || typeof window === 'undefined') {
			return [];
		}
		try {
			const raw = window.localStorage.getItem(storageKey);
			if (!raw) {
				return [];
			}
			const parsed = JSON.parse(raw);
			if (!Array.isArray(parsed)) {
				return [];
			}
			return parsed
				.map((entry) => (typeof entry === 'string' ? entry.trim() : ''))
				.filter((entry) => Boolean(entry))
				.slice(0, MAX_NOTES);
		} catch {
			return [];
		}
	}

	function persistStoredNotes(storageKey: string, nextNotes: string[]) {
		if (!storageKey || typeof window === 'undefined') {
			return;
		}
		try {
			window.localStorage.setItem(storageKey, JSON.stringify(nextNotes.slice(0, MAX_NOTES)));
		} catch {
			// ignore quota/serialization errors
		}
	}

	function formatPinnedTimestamp(timestamp: number) {
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

	function pinnedContentLabel(message: ChatMessage) {
		if (message.type === 'image') {
			return 'Pinned image';
		}
		if (message.type === 'video') {
			return 'Pinned video';
		}
		if (message.type === 'audio') {
			return 'Pinned audio';
		}
		if (message.type === 'file') {
			return message.fileName ? `Pinned file: ${message.fileName}` : 'Pinned file';
		}
		return 'Pinned message';
	}
</script>

{#if open}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<div class="discussion-overlay {isDarkMode ? 'theme-dark' : ''}" role="presentation" on:click={onBackdropClick}>
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

		<div
			class="discussion-modal"
			role="dialog"
			aria-modal="true"
			aria-label="Pinned discussion"
			tabindex="-1"
			on:keydown={onDialogKeyDown}
		>
			<header class="modal-header-grid">
				<section class="context-column">
					<div class="column-title-row">
						<h3>Pinned Message</h3>
						{#if backgroundUnreadCount > 0}
							<span class="chat-unread-pill">
								{backgroundUnreadCount} new in chat
							</span>
						{/if}
					</div>
					{#if pinnedMessage}
						{#if pinnedMessage.type === 'task'}
							<TaskCard
								message={pinnedMessage}
								showAddTaskControl={canEditTask}
								canEditTasks={canEditTask}
								on:toggleTask={(event) => dispatch('toggleTask', event.detail)}
								on:addTask={(event) => dispatch('addTask', event.detail)}
							/>
						{:else}
							<div class="pinned-message-block">
								<div class="pinned-meta">
									<strong>{pinnedMessage.senderName}</strong>
									<time>{formatPinnedTimestamp(pinnedMessage.createdAt)}</time>
								</div>
								<div class="pinned-label">{pinnedContentLabel(pinnedMessage)}</div>
								<p>{(pinnedMessage.content || '').trim() || 'No message body'}</p>
								{#if pinnedMessage.mediaUrl}
									<a href={pinnedMessage.mediaUrl} target="_blank" rel="noreferrer">Open attachment</a>
								{/if}
							</div>
						{/if}
					{:else}
						<div class="empty-pinned-message">No pinned message selected.</div>
					{/if}
				</section>

				<section class="notes-column">
					<div class="column-title-row">
						<h3>Quick Notes</h3>
						<button type="button" class="close-button" on:click={closeModal}>Close</button>
					</div>
					<div class="notes-list" aria-live="polite">
						{#if notes.length === 0}
							<div class="empty-notes">Capture highlights from this discussion.</div>
						{:else}
							{#each notes as note, index (note + index)}
								<div class="note-item">{note}</div>
							{/each}
						{/if}
					</div>
					<div class="notes-input-wrap">
						{#if notesLimitReached}
							<div class="notes-limit">Max 5 notes reached.</div>
						{/if}
						<input
							type="text"
							bind:value={noteDraft}
							placeholder={notesLimitReached ? 'Max 5 notes reached.' : 'Add a quick note'}
							disabled={notesLimitReached}
							on:keydown={onNoteKeyDown}
						/>
						<button type="button" on:click={addNote} disabled={notesLimitReached}>Add</button>
					</div>
				</section>
			</header>

			<section class="discussion-comments" aria-label="Threaded comments">
				{#if threadEntries.length === 0}
					<div class="discussion-empty">No comments yet. Start the first comment below.</div>
				{:else}
					{#each threadEntries as thread (thread.parent.id)}
						<article class="comment-card parent">
							{#if thread.parent.isPinned}
								{#if isPinnedByOP(thread.parent)}
									<div class="pin-badge op">📌 Pinned by OP ({pinnedByLabel(thread.parent)})</div>
								{:else}
									<div class="pin-badge user">📌 Pinned by ({pinnedByLabel(thread.parent)})</div>
								{/if}
							{/if}
							<div class="comment-layout">
								<div class="avatar">{thread.parent.senderName?.slice(0, 1) || 'U'}</div>
								<div class="comment-body">
									<div class="comment-top-row">
										<div class="identity-row">
											<strong>{thread.parent.senderName}</strong>
											<time>{formatCommentTime(thread.parent.createdAt)}</time>
										</div>
										<button
											type="button"
											class="pin-action"
											on:click={() => toggleCommentPin(thread.parent)}
											title={thread.parent.isPinned ? 'Unpin comment' : 'Pin comment'}
										>
											📌
										</button>
									</div>
									<p>{getCommentPreview(thread.parent)}</p>
									<div class="comment-actions">
										{#if thread.canReply}
											<button type="button" on:click={() => startReply(thread.parent)}>Reply</button>
										{/if}
										{#if isOwnComment(thread.parent) && thread.parent.type !== 'deleted' && !thread.parent.isDeleted}
											<button type="button" on:click={() => requestEdit(thread.parent)}>Edit</button>
											<button type="button" class="danger" on:click={() => requestDelete(thread.parent)}>
												Delete
											</button>
										{/if}
									</div>
								</div>
							</div>
						</article>

						{#if thread.directReplyCount > 0}
							<button type="button" class="show-replies" on:click={() => toggleReplies(thread.parent.id)}>
								{thread.repliesExpanded
									? '↳ Hide replies'
									: `↳ Show ${formatReplyCountLabel(thread.directReplyCount)}`}
							</button>
							{#if thread.repliesExpanded && thread.remainingDirectReplies > 0}
								<button
									type="button"
									class="show-more-replies"
									on:click={() => showMoreReplies(thread.parent.id)}
								>
									Show more replies ({thread.remainingDirectReplies} left)
								</button>
							{/if}
						{/if}

						{#if thread.repliesExpanded}
							{#each thread.replies as reply (reply.comment.id)}
								<article class="comment-card child" style={`--depth:${Math.max(1, reply.depth - 1)};`}>
									{#if reply.comment.isPinned}
										{#if isPinnedByOP(reply.comment)}
											<div class="pin-badge op">📌 Pinned by OP ({pinnedByLabel(reply.comment)})</div>
										{:else}
											<div class="pin-badge user">📌 Pinned by ({pinnedByLabel(reply.comment)})</div>
										{/if}
									{/if}
									<div class="comment-layout">
										<div class="avatar">{reply.comment.senderName?.slice(0, 1) || 'U'}</div>
										<div class="comment-body">
											<div class="comment-top-row">
												<div class="identity-row">
													<strong>{reply.comment.senderName}</strong>
													<time>{formatCommentTime(reply.comment.createdAt)}</time>
												</div>
												<button
													type="button"
													class="pin-action"
													on:click={() => toggleCommentPin(reply.comment)}
													title={reply.comment.isPinned ? 'Unpin comment' : 'Pin comment'}
												>
													📌
												</button>
											</div>
											<p>{getCommentPreview(reply.comment)}</p>
											<div class="comment-actions">
												{#if reply.canReply}
													<button type="button" on:click={() => startReply(reply.comment)}>Reply</button>
												{/if}
												{#if isOwnComment(reply.comment) && reply.comment.type !== 'deleted' && !reply.comment.isDeleted}
													<button type="button" on:click={() => requestEdit(reply.comment)}>Edit</button>
													<button type="button" class="danger" on:click={() => requestDelete(reply.comment)}>
														Delete
													</button>
												{/if}
											</div>
										</div>
									</div>
								</article>
								{#if reply.directReplyCount > 0}
									<button
										type="button"
										class="show-replies nested"
										style={`--depth:${Math.max(1, Math.min(reply.depth - 1, MAX_REPLY_DEPTH - 1))};`}
										on:click={() => toggleReplies(reply.comment.id)}
									>
										{reply.repliesExpanded
											? '↳ Hide replies'
											: `↳ Show ${formatReplyCountLabel(reply.directReplyCount)}`}
									</button>
									{#if reply.repliesExpanded && reply.remainingDirectReplies > 0}
										<button
											type="button"
											class="show-more-replies nested"
											style={`--depth:${Math.max(1, Math.min(reply.depth - 1, MAX_REPLY_DEPTH - 1))};`}
											on:click={() => showMoreReplies(reply.comment.id)}
										>
											Show more replies ({reply.remainingDirectReplies} left)
										</button>
									{/if}
								{/if}
							{/each}
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
					placeholder={isLimitReached ? 'Discussion limit reached (50/50)' : 'Write a comment... (Ctrl/Cmd + Enter to send)'}
					disabled={isLimitReached}
					on:keydown={onComposerKeyDown}
				></textarea>
				<div class="composer-actions">
					<button type="button" class="send-comment" on:click={submitComment} disabled={isLimitReached}>
						Comment
					</button>
				</div>
			</footer>
		</div>
	</div>
{/if}

<style>
	.discussion-overlay {
		position: fixed;
		inset: 0;
		z-index: 1000;
		backdrop-filter: blur(12px);
		-webkit-backdrop-filter: blur(12px);
		background: rgba(0, 0, 0, 0.6);
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 2rem;
	}

	.discussion-modal {
		max-width: 1200px;
		width: 100%;
		height: 90vh;
		background: #0f172a;
		border: 1px solid #1e293b;
		border-radius: 16px;
		display: flex;
		flex-direction: column;
		overflow: hidden;
		color: #e2e8f0;
		position: relative;
	}

	.modal-header-grid {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 1.5rem;
		padding: 1.5rem;
		border-bottom: 1px solid #1e293b;
		background: #0b1120;
	}

	.context-column,
	.notes-column {
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}

	.column-title-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.65rem;
	}

	.column-title-row h3 {
		margin: 0;
		font-size: 0.95rem;
		font-weight: 700;
	}

	.chat-unread-pill {
		padding: 0.22rem 0.56rem;
		border-radius: 999px;
		background: rgba(248, 113, 113, 0.15);
		color: #fda4af;
		font-size: 0.68rem;
		font-weight: 700;
		white-space: nowrap;
	}

	.close-button {
		border: 1px solid #334155;
		background: rgba(15, 23, 42, 0.75);
		color: #cbd5e1;
		border-radius: 8px;
		padding: 0.32rem 0.62rem;
		font-size: 0.76rem;
		font-weight: 600;
		cursor: pointer;
	}

	.close-button:hover {
		background: rgba(51, 65, 85, 0.5);
	}

	.pinned-message-block {
		border: 1px solid #334155;
		border-radius: 12px;
		padding: 0.8rem;
		background: rgba(15, 23, 42, 0.74);
		display: flex;
		flex-direction: column;
		gap: 0.42rem;
	}

	.pinned-meta {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		font-size: 0.74rem;
		color: #94a3b8;
	}

	.pinned-meta strong {
		color: #e2e8f0;
		font-size: 0.78rem;
	}

	.pinned-label {
		font-size: 0.7rem;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: #38bdf8;
	}

	.pinned-message-block p {
		margin: 0;
		font-size: 0.85rem;
		line-height: 1.45;
		color: #e2e8f0;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.pinned-message-block a {
		font-size: 0.75rem;
		color: #38bdf8;
	}

	.empty-pinned-message,
	.empty-notes {
		border: 1px dashed #334155;
		border-radius: 10px;
		padding: 0.72rem;
		font-size: 0.8rem;
		color: #94a3b8;
	}

	.notes-list {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
		min-height: 0;
		overflow: auto;
		padding-right: 0.1rem;
		max-height: 14rem;
	}

	.note-item {
		border: 1px solid rgba(253, 224, 71, 0.36);
		background: rgba(254, 249, 195, 0.16);
		color: #fde68a;
		border-radius: 10px;
		padding: 0.5rem 0.62rem;
		font-size: 0.8rem;
		line-height: 1.35;
		word-break: break-word;
	}

	.notes-input-wrap {
		display: grid;
		grid-template-columns: 1fr auto;
		gap: 0.42rem;
		align-items: center;
	}

	.notes-input-wrap input {
		border: 1px solid #334155;
		background: rgba(15, 23, 42, 0.75);
		color: #e2e8f0;
		border-radius: 8px;
		padding: 0.42rem 0.52rem;
		font-size: 0.78rem;
		min-width: 0;
	}

	.notes-input-wrap button {
		border: 1px solid #334155;
		background: rgba(15, 23, 42, 0.75);
		color: #cbd5e1;
		border-radius: 8px;
		padding: 0.34rem 0.58rem;
		font-size: 0.72rem;
		font-weight: 600;
		cursor: pointer;
	}

	.notes-input-wrap button:disabled,
	.notes-input-wrap input:disabled {
		opacity: 0.55;
		cursor: not-allowed;
	}

	.notes-limit {
		grid-column: 1 / -1;
		font-size: 0.72rem;
		color: #fca5a5;
	}

	.discussion-comments {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 1rem 1.5rem;
		display: flex;
		flex-direction: column;
		gap: 0.72rem;
	}

	.discussion-empty {
		border: 1px dashed #334155;
		border-radius: 10px;
		padding: 0.82rem;
		font-size: 0.82rem;
		color: #94a3b8;
	}

	.comment-card {
		border: 1px solid #334155;
		border-radius: 12px;
		background: rgba(15, 23, 42, 0.72);
		padding: 0.62rem 0.72rem;
		display: flex;
		flex-direction: column;
		gap: 0.4rem;
		position: relative;
		z-index: 1;
	}

	.comment-card.child {
		border-left: 2px solid #334155;
		margin-left: calc(var(--depth, 1) * 1.25rem);
		padding-left: 1rem;
	}

	.comment-layout {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr);
		gap: 0.62rem;
		align-items: flex-start;
	}

	.avatar {
		width: 32px;
		height: 32px;
		border-radius: 999px;
		background: #1e293b;
		color: #cbd5e1;
		font-size: 0.78rem;
		font-weight: 700;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		flex-shrink: 0;
	}

	.comment-body {
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
	}

	.comment-top-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
	}

	.identity-row {
		display: inline-flex;
		align-items: center;
		gap: 0.42rem;
		min-width: 0;
	}

	.identity-row strong {
		font-size: 0.8rem;
		font-weight: 700;
		color: #e2e8f0;
	}

	.identity-row time {
		font-size: 0.71rem;
		color: #94a3b8;
	}

	.pin-action {
		border: 1px solid #334155;
		background: rgba(15, 23, 42, 0.8);
		color: #94a3b8;
		border-radius: 8px;
		padding: 0.16rem 0.38rem;
		font-size: 0.75rem;
		cursor: pointer;
		opacity: 0;
		transition: opacity 0.2s ease;
	}

	.comment-card:hover .pin-action {
		opacity: 1;
	}

	.comment-body p {
		margin: 0;
		font-size: 0.82rem;
		line-height: 1.42;
		color: #e2e8f0;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.pin-badge {
		width: fit-content;
		font-size: 0.66rem;
		font-weight: 700;
		border-radius: 999px;
		padding: 0.15rem 0.5rem;
	}

	.pin-badge.op {
		background: rgba(245, 158, 11, 0.18);
		color: #fbbf24;
		box-shadow: 0 0 12px rgba(251, 191, 36, 0.2);
	}

	.pin-badge.user {
		background: rgba(148, 163, 184, 0.2);
		color: #cbd5e1;
	}

	.comment-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.34rem;
		position: relative;
		z-index: 3;
	}

	.comment-actions button {
		border: 1px solid #334155;
		background: transparent;
		color: #94a3b8;
		border-radius: 8px;
		padding: 0.2rem 0.5rem;
		font-size: 0.68rem;
		font-weight: 600;
		cursor: pointer;
	}

	.comment-actions button:hover {
		background: rgba(51, 65, 85, 0.45);
	}

	.comment-actions button.danger {
		border-color: rgba(239, 68, 68, 0.42);
		color: #f87171;
	}

	.show-replies {
		align-self: flex-start;
		margin: 0.08rem 0 0.15rem 0.25rem;
		border: none;
		background: transparent;
		color: #93c5fd;
		font-size: 0.74rem;
		font-weight: 600;
		cursor: pointer;
		position: relative;
		z-index: 4;
		pointer-events: auto;
	}

	.show-replies.nested,
	.show-more-replies.nested {
		margin-left: calc(var(--depth, 1) * 1.25rem + 0.25rem);
	}

	.show-more-replies {
		align-self: flex-start;
		margin: 0.02rem 0 0.15rem 0.25rem;
		border: none;
		background: transparent;
		color: #7dd3fc;
		font-size: 0.72rem;
		font-weight: 600;
		cursor: pointer;
		position: relative;
		z-index: 4;
		pointer-events: auto;
	}

	.discussion-composer {
		border-top: 1px solid #1e293b;
		padding: 0.85rem 1.5rem 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.48rem;
		background: #0b1120;
	}

	.reply-target {
		display: inline-flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		border: 1px solid #334155;
		border-radius: 9px;
		padding: 0.3rem 0.48rem;
		font-size: 0.72rem;
		color: #94a3b8;
	}

	.reply-target button {
		border: 1px solid #334155;
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
		border: 1px solid #334155;
		background: rgba(15, 23, 42, 0.75);
		color: #e2e8f0;
		border-radius: 10px;
		padding: 0.48rem 0.58rem;
		font-size: 0.84rem;
		line-height: 1.35;
		box-sizing: border-box;
		font-family: inherit;
	}

	.discussion-composer textarea:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	.composer-actions {
		display: flex;
		justify-content: flex-end;
	}

	.send-comment {
		border: 1px solid #334155;
		border-radius: 9px;
		padding: 0.36rem 0.78rem;
		font-size: 0.74rem;
		font-weight: 700;
		cursor: pointer;
		background: rgba(14, 165, 233, 0.16);
		color: #7dd3fc;
	}

	.send-comment:disabled {
		opacity: 0.55;
		cursor: not-allowed;
	}

	.nav-arrow {
		position: fixed;
		top: 50%;
		transform: translateY(-50%);
		width: 48px;
		height: 48px;
		border-radius: 50%;
		border: 1px solid rgba(255, 255, 255, 0.18);
		background: rgba(255, 255, 255, 0.1);
		backdrop-filter: blur(4px);
		-webkit-backdrop-filter: blur(4px);
		color: #f8fafc;
		font-size: 1.2rem;
		font-weight: 700;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		z-index: 1001;
	}

	.nav-arrow.left {
		left: 1rem;
	}

	.nav-arrow.right {
		right: 1rem;
	}

	@media (max-width: 980px) {
		.discussion-overlay {
			padding: 1rem;
		}

		.discussion-modal {
			height: 94vh;
		}

		.modal-header-grid {
			grid-template-columns: 1fr;
		}
	}

	@media (max-width: 700px) {
		.discussion-overlay {
			padding: 0.6rem;
		}

		.discussion-comments,
		.discussion-composer {
			padding-left: 0.8rem;
			padding-right: 0.8rem;
		}

		.comment-card.child {
			margin-left: calc(var(--depth, 1) * 0.8rem);
		}

		.nav-arrow.left {
			left: 0.35rem;
		}

		.nav-arrow.right {
			right: 0.35rem;
		}
	}
</style>
