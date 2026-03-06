<script lang="ts">
	import { afterUpdate, createEventDispatcher, onDestroy, onMount } from 'svelte';
	import IconSet from '$lib/components/icons/IconSet.svelte';
	import TaskCard from '$lib/components/chat/TaskCard.svelte';
	import type { ChatMessage, MessageActionMode } from '$lib/types/chat';
	import { normalizeIdentifier } from '$lib/utils/chat/core';
	import { parseTaskMessagePayload } from '$lib/utils/chat/task';

	type ReplyPreview = {
		messageId: string;
		author: string;
		content: string;
	};

	type SnippetPayload = {
		snippet: string;
		message: string;
		fileName: string;
	};

	type MessageContextAction = 'reply' | 'edit' | 'delete' | 'pin' | 'branch';

	export let messages: ChatMessage[] = [];
	export let roomId = '';
	export let isVisible = true;
	export let currentUserId = '';
	export let roomMessageSearch = '';
	export let expandedMessages: Record<string, boolean> = {};
	export let isMember = true;
	export let isDarkMode = false;
	export let isSelectionMode = false;
	export let messageActionMode: MessageActionMode = 'none';
	export let selectedMessageId = '';
	export let deleteMultiEnabled = false;
	export let selectedDeleteMessageIds: string[] = [];
	export let focusMessageId = '';
	export let isLoadingOlder = false;
	export let hasMoreOlder = true;
	export let unreadCount = 0;
	export let lastReadTimestamp = Date.now();
	export let firstUnreadMessageId = '';

	const dispatch = createEventDispatcher<{
		toggleExpand: { messageId: string };
		joinBreakRoom: { roomId: string };
		joinRoom: void;
		messageSelect: { messageId: string };
		openPinnedDiscussion: { messageId: string };
		focusHandled: { messageId: string };
		reply: { messageId: string; senderName: string; content: string };
		editSelected: { messageId: string };
		deleteSelected: { messageId: string };
		requestOlder: void;
		readProgress: { isNearBottom: boolean; lastSeenMessageId: string };
		toggleTask: { messageId: string; taskIndex: number };
		addTask: { messageId: string; text: string };
		messageContextAction: { messageId: string; action: MessageContextAction };
	}>();

	const COLLAPSED_MESSAGE_LENGTH = 500;
	const COLLAPSED_SNIPPET_CODE_MAX_LINES = 20;
	const COLLAPSED_SNIPPET_CODE_MAX_CHARS = 1400;
	const COLLAPSED_SNIPPET_MESSAGE_MAX_LINES = 8;
	const COLLAPSED_SNIPPET_MESSAGE_MAX_CHARS = 560;
	const CANVAS_SNIPPET_PAYLOAD_KIND = 'canvas_snippet_v1';
	const MESSAGE_CONTEXT_MENU_WIDTH_PX = 186;
	const MESSAGE_CONTEXT_MENU_HEIGHT_PX = 220;
	const MESSAGE_CONTEXT_MENU_MARGIN_PX = 8;
	const MESSAGE_LONG_PRESS_DELAY_MS = 520;
	const MESSAGE_LONG_PRESS_MOVE_TOLERANCE_PX = 12;
	const MESSAGE_LONG_PRESS_CLICK_SUPPRESSION_MS = 700;
	const MESSAGE_NATIVE_CONTEXT_SUPPRESSION_MS = 1400;

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
	let previousVisibleKey = '';
	let previousRoomId = '';
	let previousIsVisible = true;
	let unreadDividerAnchorId = '';
	let unreadDividerCount = 0;
	let scrollTopByRoomId: Record<string, number> = {};
	let compactMineActionsByMessageID: Record<string, boolean> = {};
	let expandedSnippetCodeByMessageID: Record<string, boolean> = {};
	let expandedSnippetMessageByMessageID: Record<string, boolean> = {};
	let gutterMeasureFrame: number | null = null;
	let resizeMeasureHandler: (() => void) | null = null;
	let messageContextMenu = {
		open: false,
		x: 0,
		y: 0,
		messageId: ''
	};
	let messageLongPressTimer: ReturnType<typeof setTimeout> | null = null;
	let messageLongPressTouchIdentifier = -1;
	let messageLongPressMessageId = '';
	let messageLongPressStartX = 0;
	let messageLongPressStartY = 0;
	let messageLongPressLastX = 0;
	let messageLongPressLastY = 0;
	let suppressMessageClickUntil = 0;
	let suppressNativeMessageContextMenuUntil = 0;

	$: if (!focusMessageId && focusedMessageId) {
		focusedMessageId = '';
	}

	$: if (
		messageContextMenu.open &&
		!visibleMessages.some((message) => message.id === messageContextMenu.messageId)
	) {
		closeMessageContextMenu();
	}

	$: if (isSelectionMode && messageContextMenu.open) {
		closeMessageContextMenu();
	}

	$: visibleMessages = getVisibleMessages(messages, roomMessageSearch);
	$: replyCountByMessageID = buildReplyCountByMessageID(messages);
	$: safeUnreadCount = Math.max(0, Math.trunc(Number.isFinite(unreadCount) ? unreadCount : 0));
	$: unreadDividerLabel =
		unreadDividerCount === 1 ? '1 unread message' : `${unreadDividerCount} unread messages`;
	$: firstUnreadId = unreadDividerAnchorId;

	afterUpdate(() => {
		if (!viewport) {
			return;
		}
		const visibleKey = getVisibleMessageKey(visibleMessages);
		const roomChanged = roomId !== previousRoomId;
		const becameVisible = isVisible && !previousIsVisible;

		if (roomChanged || becameVisible) {
			syncUnreadDividerAnchor(true);
		} else {
			syncUnreadDividerAnchor(false);
		}

		if (!isVisible) {
			previousVisibleCount = visibleMessages.length;
			previousVisibleKey = visibleKey;
			previousRoomId = roomId;
			previousIsVisible = isVisible;
			return;
		}

		if (visibleKey !== previousVisibleKey || roomChanged || becameVisible) {
			const savedScrollTop = roomId ? scrollTopByRoomId[roomId] : undefined;
			const hasSavedScrollTop = Number.isFinite(savedScrollTop);
			const shouldRestoreSaved = hasSavedScrollTop && (roomChanged || becameVisible);
			const shouldJumpToUnread =
				!shouldRestoreSaved &&
				Boolean(firstUnreadId) &&
				!focusMessageId &&
				!roomMessageSearch.trim() &&
				(roomChanged || becameVisible);
			const shouldInitializeToBottom =
				!shouldRestoreSaved &&
				!shouldJumpToUnread &&
				previousVisibleCount === 0 &&
				!roomMessageSearch.trim();
			previousVisibleCount = visibleMessages.length;
			previousVisibleKey = visibleKey;
			if (shouldRestoreSaved) {
				viewport.scrollTop = Math.max(0, Number(savedScrollTop));
				updateScrollState(false);
			} else if (shouldJumpToUnread) {
				const target = findMessageNode(firstUnreadId);
				if (target) {
					target.scrollIntoView({ behavior: 'instant', block: 'start' });
				}
				updateScrollState(false);
			} else if (shouldInitializeToBottom) {
				scrollToBottom('instant');
			} else {
				updateScrollState(false);
			}
		}
		persistCurrentRoomScrollTop();
		previousRoomId = roomId;
		previousIsVisible = isVisible;
		tryFocusMessage();
		scheduleCompactMineActionMeasure();
	});

	onDestroy(() => {
		if (copyResetTimer) {
			clearTimeout(copyResetTimer);
		}
		clearMessageLongPressState();
		if (typeof window !== 'undefined' && clearFocusOnPointerDown) {
			window.removeEventListener('pointerdown', clearFocusOnPointerDown, true);
			clearFocusOnPointerDown = null;
		}
		if (topObserver) {
			topObserver.disconnect();
			topObserver = null;
		}
		if (typeof window !== 'undefined' && resizeMeasureHandler) {
			window.removeEventListener('resize', resizeMeasureHandler);
			resizeMeasureHandler = null;
		}
		if (typeof window !== 'undefined' && gutterMeasureFrame !== null) {
			window.cancelAnimationFrame(gutterMeasureFrame);
			gutterMeasureFrame = null;
		}
	});

		onMount(() => {
			setupTopObserver();
			if (typeof window !== 'undefined') {
				resizeMeasureHandler = () => scheduleCompactMineActionMeasure();
				window.addEventListener('resize', resizeMeasureHandler);
				window.addEventListener('keydown', onWindowKeyDown, true);
				window.addEventListener('contextmenu', onWindowContextMenuCapture, true);
				scheduleCompactMineActionMeasure();
			}
			return () => {
			if (topObserver) {
				topObserver.disconnect();
				topObserver = null;
			}
			if (typeof window !== 'undefined' && resizeMeasureHandler) {
				window.removeEventListener('resize', resizeMeasureHandler);
				resizeMeasureHandler = null;
			}
				if (typeof window !== 'undefined') {
					window.removeEventListener('keydown', onWindowKeyDown, true);
					window.removeEventListener('contextmenu', onWindowContextMenuCapture, true);
				}
			if (typeof window !== 'undefined' && gutterMeasureFrame !== null) {
				window.cancelAnimationFrame(gutterMeasureFrame);
				gutterMeasureFrame = null;
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

	function scheduleCompactMineActionMeasure() {
		if (typeof window === 'undefined') {
			return;
		}
		if (gutterMeasureFrame !== null) {
			return;
		}
		gutterMeasureFrame = window.requestAnimationFrame(() => {
			gutterMeasureFrame = null;
			measureCompactMineActions();
		});
	}

	function measureCompactMineActions() {
		if (!viewport || !isVisible) {
			if (Object.keys(compactMineActionsByMessageID).length > 0) {
				compactMineActionsByMessageID = {};
			}
			return;
		}
		const next: Record<string, boolean> = {};
		const rows = viewport.querySelectorAll<HTMLElement>('.message-row.mine');
		for (const row of rows) {
			const bubble = row.querySelector<HTMLElement>('.bubble.mine[data-message-id]');
			const messageID = bubble?.dataset.messageId || '';
			if (!bubble || !messageID) {
				continue;
			}
			const pinButton = row.querySelector<HTMLElement>('.message-gutter.mine .gutter-pin-btn');
			const actionsWrap = row.querySelector<HTMLElement>('.message-gutter.mine .gutter-actions.mine-actions');
			if (!pinButton || !actionsWrap) {
				continue;
			}
			const actionButton = actionsWrap.querySelector<HTMLElement>('.gutter-action-btn');
			const pinHeight = pinButton.getBoundingClientRect().height;
				const actionHeight = actionButton?.getBoundingClientRect().height ?? 28;
				const bubbleHeight = bubble.getBoundingClientRect().height;
				const verticalGap = 10;
				const requiredVerticalHeight = pinHeight + actionHeight + verticalGap;
				if (bubbleHeight < requiredVerticalHeight) {
					next[messageID] = true;
				}
		}
		if (!equalBooleanMap(compactMineActionsByMessageID, next)) {
			compactMineActionsByMessageID = next;
		}
	}

	function equalBooleanMap(left: Record<string, boolean>, right: Record<string, boolean>) {
		const leftKeys = Object.keys(left);
		const rightKeys = Object.keys(right);
		if (leftKeys.length !== rightKeys.length) {
			return false;
		}
		for (const key of leftKeys) {
			if (left[key] !== right[key]) {
				return false;
			}
		}
		return true;
	}

	function getVisibleMessageKey(entries: ChatMessage[]) {
		if (entries.length === 0) {
			return 'empty';
		}
		const firstId = entries[0]?.id || '';
		const lastId = entries[entries.length - 1]?.id || '';
		return `${entries.length}:${firstId}:${lastId}`;
	}

	function resolveFirstUnreadId(
		entries: ChatMessage[],
		nextUnreadCount: number,
		userId: string,
		anchorMessageId: string,
		readTimestamp: number
	) {
		const unreadTarget = Math.max(
			0,
			Math.trunc(Number.isFinite(nextUnreadCount) ? nextUnreadCount : 0)
		);
		if (unreadTarget <= 0 || entries.length === 0) {
			return '';
		}
		const normalizedUserId = normalizeIdentifier(userId || '');
		let remaining = unreadTarget;
		for (let index = entries.length - 1; index >= 0; index -= 1) {
			const entry = entries[index];
			const isOwnMessage =
				normalizedUserId !== '' && normalizeIdentifier(entry.senderId || '') === normalizedUserId;
			if (isOwnMessage) {
				continue;
			}
			remaining -= 1;
			if (remaining <= 0) {
				return entry.id;
			}
		}
		const normalizedAnchor = (anchorMessageId || '').trim();
		if (normalizedAnchor && entries.some((entry) => entry.id === normalizedAnchor)) {
			return normalizedAnchor;
		}
		return (
			entries.find((message) => message.createdAt > readTimestamp && message.senderId !== userId)
				?.id ??
			entries[0]?.id ??
			''
		);
	}

	function getLastSeenMessageId() {
		if (!viewport) {
			return '';
		}
		const viewportRect = viewport.getBoundingClientRect();
		const visibleBottom = viewportRect.bottom - 6;
		let lastSeenMessageId = '';
		const nodes = viewport.querySelectorAll<HTMLElement>('[data-message-id]');
		for (const node of nodes) {
			const nodeRect = node.getBoundingClientRect();
			if (nodeRect.top <= visibleBottom) {
				lastSeenMessageId = node.dataset.messageId || lastSeenMessageId;
			}
		}
		return lastSeenMessageId;
	}

	function updateScrollState(fromUserScroll = false) {
		if (!viewport) {
			return;
		}
		const distanceFromBottom = viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop;
		isNearBottom = distanceFromBottom < 48;
		showScrollToBottom = distanceFromBottom > Math.max(viewport.clientHeight, 300);
		dispatch('readProgress', {
			isNearBottom,
			lastSeenMessageId: getLastSeenMessageId()
		});
		if (fromUserScroll && roomId) {
			scrollTopByRoomId = {
				...scrollTopByRoomId,
				[roomId]: viewport.scrollTop
			};
		}
	}

	function onMessagesScroll() {
		clearMessageLongPressState();
		if (messageContextMenu.open) {
			closeMessageContextMenu();
		}
		updateScrollState(true);
	}

	function scrollToBottom(behavior: ScrollBehavior = 'smooth') {
		if (!viewport) {
			return;
		}
		viewport.scrollTo({ top: viewport.scrollHeight, behavior });
		updateScrollState(false);
		persistCurrentRoomScrollTop();
	}

	function persistCurrentRoomScrollTop() {
		if (!viewport || !roomId) {
			return;
		}
		scrollTopByRoomId = {
			...scrollTopByRoomId,
			[roomId]: viewport.scrollTop
		};
	}

	function resolveUnreadDividerCandidateId(unreadTotal: number) {
		return resolveFirstUnreadId(
			messages,
			unreadTotal,
			currentUserId,
			firstUnreadMessageId,
			lastReadTimestamp
		);
	}

	function syncUnreadDividerAnchor(forceRecompute: boolean) {
		if (forceRecompute) {
			unreadDividerCount = safeUnreadCount;
			unreadDividerAnchorId =
				unreadDividerCount > 0 ? resolveUnreadDividerCandidateId(unreadDividerCount) : '';
			return;
		}

		if (unreadDividerCount <= 0) {
			if (unreadDividerAnchorId) {
				unreadDividerAnchorId = '';
			}
			return;
		}

		const hasAnchor = Boolean(unreadDividerAnchorId);
		if (!hasAnchor) {
			unreadDividerAnchorId = resolveUnreadDividerCandidateId(unreadDividerCount);
			return;
		}

		const anchorStillExists = messages.some((message) => message.id === unreadDividerAnchorId);
		if (!anchorStillExists) {
			unreadDividerAnchorId = resolveUnreadDividerCandidateId(unreadDividerCount);
		}
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
		updateScrollState(false);
		persistCurrentRoomScrollTop();
	}

	function shouldShowDayStamp(input: ChatMessage[], index: number) {
		if (index <= 0) {
			return true;
		}
		const previous = input[index - 1];
		const current = input[index];
		if (!previous || !current) {
			return false;
		}
		const previousDate = new Date(previous.createdAt);
		const currentDate = new Date(current.createdAt);
		return (
			previousDate.getFullYear() !== currentDate.getFullYear() ||
			previousDate.getMonth() !== currentDate.getMonth() ||
			previousDate.getDate() !== currentDate.getDate()
		);
	}

	function formatDayStamp(timestamp: number) {
		const date = new Date(timestamp);
		const now = new Date();
		const isCurrentYear = date.getFullYear() === now.getFullYear();
		return date.toLocaleDateString([], {
			month: 'short',
			day: 'numeric',
			...(isCurrentYear ? {} : { year: 'numeric' })
		});
	}

	function getVisibleMessages(input: ChatMessage[], query: string) {
		const normalized = query.trim().toLowerCase();
		if (!normalized) {
			return input;
		}
		return input.filter((message) => {
			if (message.senderName.toLowerCase().includes(normalized)) {
				return true;
			}
			if (message.type === 'task') {
				const parsedTask = parseTaskMessagePayload(message.content || '');
				if (!parsedTask) {
					return false;
				}
				if (parsedTask.title.toLowerCase().includes(normalized)) {
					return true;
				}
				return parsedTask.tasks.some((task) => task.text.toLowerCase().includes(normalized));
			}
			const snippetPayload = getSnippetPayload(message);
			if (snippetPayload) {
				if (snippetPayload.message.toLowerCase().includes(normalized)) {
					return true;
				}
				if (snippetPayload.fileName.toLowerCase().includes(normalized)) {
					return true;
				}
				return snippetPayload.snippet.toLowerCase().includes(normalized);
			}
			return message.content.toLowerCase().includes(normalized);
		});
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
		const reported = Number.isFinite(message.branchesCreated) ? Number(message.branchesCreated) : 0;
		if (reported > 0) {
			return reported;
		}
		return message.hasBreakRoom ? 1 : 0;
	}

	function normalizeSnippetText(value: string) {
		return value.replace(/\r\n/g, '\n').trim();
	}

	function tryParseSnippetContentPayload(
		rawContent: string,
		fallbackFileName: string,
		fallbackSnippet: string,
		requireKind: boolean
	): SnippetPayload | null {
		const trimmedContent = rawContent.trim();
		if (!trimmedContent.startsWith('{') || !trimmedContent.endsWith('}')) {
			return null;
		}
		try {
			const parsed = JSON.parse(trimmedContent);
			if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
				return null;
			}
			const parsedRecord = parsed as Record<string, unknown>;
			const payloadKind =
				typeof parsedRecord.kind === 'string' ? parsedRecord.kind.trim().toLowerCase() : '';
			if (requireKind && payloadKind !== CANVAS_SNIPPET_PAYLOAD_KIND) {
				return null;
			}
			const snippet = normalizeSnippetText(
				typeof parsedRecord.snippet === 'string' ? parsedRecord.snippet : fallbackSnippet
			);
			if (!snippet) {
				return null;
			}
			return {
				snippet,
				message: typeof parsedRecord.message === 'string' ? parsedRecord.message.trim() : '',
				fileName:
					typeof parsedRecord.fileName === 'string'
						? parsedRecord.fileName.trim()
						: fallbackFileName
			};
		} catch {
			return null;
		}
	}

	function getSnippetPayload(message: ChatMessage): SnippetPayload | null {
		const messageType = (message.type || '').trim().toLowerCase();
		const replyTargetMessageId = (message.replyToMessageId || '').trim();
		const legacySnippet = normalizeSnippetText(message.replyToSnippet || '');
		const fallbackFileName = (message.fileName || '').trim();
		const rawContent = (message.content || '').trim();

		const resolveLegacyPayload = () => {
			if (!legacySnippet || replyTargetMessageId) {
				return null;
			}
			return {
				snippet: legacySnippet,
				message: rawContent,
				fileName: fallbackFileName
			};
		};

		const payloadFromContent = tryParseSnippetContentPayload(
			rawContent,
			fallbackFileName,
			legacySnippet,
			messageType !== 'snippet'
		);
		if (payloadFromContent) {
			return payloadFromContent;
		}

		if (messageType !== 'snippet') {
			return resolveLegacyPayload();
		}

		if (!rawContent) {
			if (!legacySnippet) {
				return null;
			}
			return {
				snippet: legacySnippet,
				message: '',
				fileName: fallbackFileName
			};
		}

		if (legacySnippet) {
			return {
				snippet: legacySnippet,
				message: rawContent,
				fileName: fallbackFileName
			};
		}

		const inferredSnippet = normalizeSnippetText(rawContent);
		if (!inferredSnippet) {
			return null;
		}
		return {
			snippet: inferredSnippet,
			message: '',
			fileName: fallbackFileName
		};
	}

	function buildSnippetCopyText(payload: SnippetPayload) {
		const extension = (payload.fileName.split('.').pop() || '').trim();
		const languageHint = /^[a-z0-9_+-]+$/i.test(extension) ? extension : '';
		const fenceHeader = languageHint ? `\`\`\`${languageHint}` : '```';
		const snippetBlock = `${fenceHeader}\n${payload.snippet}\n\`\`\``;
		if (!payload.message) {
			return snippetBlock;
		}
		return `${snippetBlock}\n\n${payload.message}`;
	}

	function getReplyPreview(message: ChatMessage): ReplyPreview | null {
		const messageID = (message.replyToMessageId || '').trim();
		const rawSnippet = (message.replyToSnippet || '').trim();
		if (!messageID) {
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
		const snippetPayload = getSnippetPayload(message);
		if (snippetPayload) {
			if (snippetPayload.message) {
				return truncateInlineText(snippetPayload.message, 220);
			}
			if (snippetPayload.fileName) {
				return truncateInlineText(`Code snippet: ${snippetPayload.fileName}`, 220);
			}
			return 'Code snippet';
		}
		if (message.type === 'task') {
			const parsedTask = parseTaskMessagePayload(message.content || '');
			if (parsedTask) {
				return truncateInlineText(`Task: ${parsedTask.title}`, 220);
			}
			return 'Task';
		}
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
		if (message.type === 'audio') {
			return 'Voice message';
		}
		if (message.type === 'file') {
			return getFileName(message);
		}
		if (message.type === 'call_log') {
			return 'Call log';
		}
		return 'Message';
	}

	function isCallLogMessage(message: ChatMessage) {
		return (message.type || '').trim().toLowerCase() === 'call_log';
	}

	function isMissedCallMessage(message: ChatMessage) {
		const content = (message.content || '').trim().toLowerCase();
		return content === 'missed call';
	}

	function getCallLogModeLabel(message: ChatMessage) {
		const mode = (message.mediaType || '').trim().toLowerCase();
		return mode === 'video' ? 'Video call' : 'Voice call';
	}

	function isLongMessage(content: string) {
		return content.length > COLLAPSED_MESSAGE_LENGTH;
	}

	function getLineCount(content: string) {
		if (!content) {
			return 0;
		}
		return content.split(/\r?\n/).length;
	}

	function isLongSnippetCode(content: string) {
		if (!content) {
			return false;
		}
		return (
			content.length > COLLAPSED_SNIPPET_CODE_MAX_CHARS ||
			getLineCount(content) > COLLAPSED_SNIPPET_CODE_MAX_LINES
		);
	}

	function isLongSnippetMessage(content: string) {
		if (!content) {
			return false;
		}
		return (
			content.length > COLLAPSED_SNIPPET_MESSAGE_MAX_CHARS ||
			getLineCount(content) > COLLAPSED_SNIPPET_MESSAGE_MAX_LINES
		);
	}

	function toggleSnippetCodeExpanded(messageId: string) {
		expandedSnippetCodeByMessageID = {
			...expandedSnippetCodeByMessageID,
			[messageId]: !expandedSnippetCodeByMessageID[messageId]
		};
	}

	function toggleSnippetMessageExpanded(messageId: string) {
		expandedSnippetMessageByMessageID = {
			...expandedSnippetMessageByMessageID,
			[messageId]: !expandedSnippetMessageByMessageID[messageId]
		};
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
		return (
			message.type === 'image' ||
			message.type === 'video' ||
			message.type === 'audio' ||
			message.type === 'file'
		);
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
		const snippetPayload = getSnippetPayload(message);
		const copyContent = snippetPayload ? buildSnippetCopyText(snippetPayload) : message.content;
		if (!copyContent) {
			return;
		}
		try {
			await navigator.clipboard.writeText(copyContent);
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
		if (Date.now() < suppressMessageClickUntil) {
			suppressMessageClickUntil = 0;
			return;
		}
		suppressMessageClickUntil = 0;
		if (messageContextMenu.open) {
			closeMessageContextMenu();
		}
		if (!isMember || !isSelectionMode) {
			return;
		}
		dispatch('messageSelect', { messageId: message.id });
	}

	function onDeleteCheckboxToggle(event: Event, message: ChatMessage) {
		event.stopPropagation();
		if (!isMember || !isSelectionMode) {
			return;
		}
		dispatch('messageSelect', { messageId: message.id });
	}

	function onMessageKeyDown(event: KeyboardEvent, message: ChatMessage) {
		if (event.key === 'Escape' && messageContextMenu.open) {
			event.preventDefault();
			closeMessageContextMenu();
			return;
		}
		if (!isMember || !isSelectionMode) {
			return;
		}
		if (event.key === 'Enter' || event.key === ' ') {
			event.preventDefault();
			dispatch('messageSelect', { messageId: message.id });
		}
	}

	function clearMessageLongPressState() {
		if (messageLongPressTimer) {
			clearTimeout(messageLongPressTimer);
			messageLongPressTimer = null;
		}
		messageLongPressTouchIdentifier = -1;
		messageLongPressMessageId = '';
		messageLongPressStartX = 0;
		messageLongPressStartY = 0;
		messageLongPressLastX = 0;
		messageLongPressLastY = 0;
	}

	function findTouchByIdentifier(touches: TouchList, identifier: number) {
		for (const touch of Array.from(touches)) {
			if (touch.identifier === identifier) {
				return touch;
			}
		}
		return null;
	}

	function onMessageTouchStart(event: TouchEvent, message: ChatMessage) {
		if (isSelectionMode || event.touches.length !== 1) {
			clearMessageLongPressState();
			return;
		}
		const target = event.target instanceof Element ? event.target : null;
		if (target?.closest('button, a, input, textarea, select, label')) {
			clearMessageLongPressState();
			return;
		}
		const touch = event.touches[0];
		clearMessageLongPressState();
		messageLongPressTouchIdentifier = touch.identifier;
		messageLongPressMessageId = message.id;
		messageLongPressStartX = touch.clientX;
		messageLongPressStartY = touch.clientY;
		messageLongPressLastX = touch.clientX;
		messageLongPressLastY = touch.clientY;
		suppressNativeMessageContextMenuUntil =
			Date.now() + MESSAGE_LONG_PRESS_DELAY_MS + MESSAGE_NATIVE_CONTEXT_SUPPRESSION_MS;
		messageLongPressTimer = setTimeout(() => {
			const messageId = messageLongPressMessageId;
			const clientX = messageLongPressLastX;
			const clientY = messageLongPressLastY;
			clearMessageLongPressState();
			if (!messageId) {
				return;
			}
			suppressMessageClickUntil = Date.now() + MESSAGE_LONG_PRESS_CLICK_SUPPRESSION_MS;
			suppressNativeMessageContextMenuUntil =
				Date.now() + MESSAGE_NATIVE_CONTEXT_SUPPRESSION_MS;
			openMessageContextMenuAtPosition(messageId, clientX, clientY);
		}, MESSAGE_LONG_PRESS_DELAY_MS);
	}

	function onMessageTouchMove(event: TouchEvent) {
		if (messageLongPressTouchIdentifier < 0) {
			return;
		}
		const touch = findTouchByIdentifier(event.touches, messageLongPressTouchIdentifier);
		if (!touch) {
			clearMessageLongPressState();
			return;
		}
		messageLongPressLastX = touch.clientX;
		messageLongPressLastY = touch.clientY;
		const dx = touch.clientX - messageLongPressStartX;
		const dy = touch.clientY - messageLongPressStartY;
		const movedDistance = Math.hypot(dx, dy);
		if (movedDistance > MESSAGE_LONG_PRESS_MOVE_TOLERANCE_PX) {
			clearMessageLongPressState();
		}
	}

	function onMessageTouchEnd(event: TouchEvent) {
		if (Date.now() < suppressMessageClickUntil) {
			event.preventDefault();
			event.stopPropagation();
		}
		clearMessageLongPressState();
	}

	function onMessageTouchCancel() {
		clearMessageLongPressState();
	}

	function onWindowKeyDown(event: KeyboardEvent) {
		if (event.key === 'Escape') {
			if (messageContextMenu.open) {
				closeMessageContextMenu();
			}
		}
	}

	function onWindowContextMenuCapture(event: MouseEvent) {
		if (Date.now() >= suppressNativeMessageContextMenuUntil) {
			return;
		}
		event.preventDefault();
		event.stopPropagation();
		event.stopImmediatePropagation();
	}

	function closeMessageContextMenu() {
		if (!messageContextMenu.open && !messageContextMenu.messageId) {
			return;
		}
		messageContextMenu = {
			open: false,
			x: 0,
			y: 0,
			messageId: ''
		};
	}

	function openMessageContextMenuAtPosition(messageId: string, clientX: number, clientY: number) {
		const position = clampContextMenuPosition(clientX, clientY);
		messageContextMenu = {
			open: true,
			x: position.x,
			y: position.y,
			messageId
		};
	}

	function clampContextMenuPosition(clientX: number, clientY: number) {
		if (typeof window === 'undefined') {
			return { x: clientX, y: clientY };
		}
		const minX = MESSAGE_CONTEXT_MENU_MARGIN_PX;
		const minY = MESSAGE_CONTEXT_MENU_MARGIN_PX;
		const maxX = Math.max(
			minX,
			window.innerWidth - MESSAGE_CONTEXT_MENU_WIDTH_PX - MESSAGE_CONTEXT_MENU_MARGIN_PX
		);
		const maxY = Math.max(
			minY,
			window.innerHeight - MESSAGE_CONTEXT_MENU_HEIGHT_PX - MESSAGE_CONTEXT_MENU_MARGIN_PX
		);
		return {
			x: Math.min(Math.max(clientX, minX), maxX),
			y: Math.min(Math.max(clientY, minY), maxY)
		};
	}

	function onMessageContextMenu(event: MouseEvent, message: ChatMessage) {
		event.preventDefault();
		event.stopPropagation();
		if (Date.now() < suppressNativeMessageContextMenuUntil) {
			return;
		}
		clearMessageLongPressState();
		suppressMessageClickUntil = 0;
		openMessageContextMenuAtPosition(message.id, event.clientX, event.clientY);
	}

	function getVisibleMessageById(messageID: string) {
		for (const message of visibleMessages) {
			if (message.id === messageID) {
				return message;
			}
		}
		return null;
	}

	function isContextMenuActionDisabled(
		action: MessageContextAction,
		message: ChatMessage | null,
		isMine: boolean
	) {
		if (!isMember || !message) {
			return true;
		}
		if (isDeletedMessage(message)) {
			return true;
		}
		if (action === 'reply') {
			return false;
		}
		if ((action === 'edit' || action === 'delete') && !isMine) {
			return true;
		}
		return action === 'edit' && (message.type || '').toLowerCase() === 'task';
	}

	function onMessageContextAction(action: MessageContextAction) {
		if (!messageContextMenu.open || !messageContextMenu.messageId) {
			return;
		}
		const messageId = messageContextMenu.messageId;
		closeMessageContextMenu();
		dispatch('messageContextAction', {
			messageId,
			action
		});
	}
</script>

<div
	class="messages-shell {isSelectionMode ? 'selection-mode' : ''} {isDarkMode ? 'theme-dark' : ''}"
>
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

		{#each visibleMessages as message, index (message.id)}
			{#if shouldShowDayStamp(visibleMessages, index)}
				<div class="day-stamp">
					<span>{formatDayStamp(message.createdAt)}</span>
				</div>
			{/if}
			{#if unreadDividerCount > 0 && message.id === firstUnreadId}
				<div class="unread-divider" role="separator" aria-label={unreadDividerLabel}>
					<span>{unreadDividerLabel}</span>
				</div>
			{/if}
				{@const isMine = message.senderId === currentUserId}
				{@const totalReplies = getTotalReplies(message)}
				{@const branchesCreated = getBranchesCreated(message)}
				{@const replyPreview = getReplyPreview(message)}
				{@const snippetPayload = getSnippetPayload(message)}
				{@const isMultiDeleteSelected =
					messageActionMode === 'delete' &&
					deleteMultiEnabled &&
					selectedDeleteMessageIds.includes(message.id)}
				<div
					class="message-row {isMine ? 'mine' : 'theirs'} {compactMineActionsByMessageID[message.id]
						? 'compact-gutter'
						: ''}"
				>
					{#if isMine}
						<aside class="message-gutter mine">
							{#if message.isPinned}
								<button
									type="button"
									class="gutter-pin-btn"
									title="Open pinned discussion"
									aria-label="Open pinned discussion"
									on:click|stopPropagation={() =>
										dispatch('openPinnedDiscussion', { messageId: message.id })}
								>
									<span class="gutter-pin-emoji" aria-hidden="true">📌</span>
								</button>
							{/if}
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
						{#if !isDeletedMessage(message)}
							<div class="gutter-actions mine-actions">
								<button
									type="button"
									class="gutter-action-btn"
									title="Reply"
									aria-label="Reply"
									on:click|stopPropagation={() =>
										dispatch('reply', {
											messageId: message.id,
											senderName: message.senderName,
											content: getReplyDispatchContent(message)
										})}
								>
									<IconSet name="reply" size={12} className="gutter-action-icon" />
								</button>
							</div>
						{/if}
						</aside>
					{/if}
				{#if isSelectionMode && messageActionMode === 'delete' && deleteMultiEnabled && isMine && !isDeletedMessage(message)}
					<label class="delete-select-toggle mine" title="Select message for deletion">
						<input
							type="checkbox"
							checked={isMultiDeleteSelected}
							on:change={(event) => onDeleteCheckboxToggle(event, message)}
						/>
					</label>
				{/if}
				<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
				<article
					class="bubble {isMine ? 'mine' : 'theirs'} {message.pending
						? 'pending'
						: ''} {isSelectionMode ? 'selectable' : ''}"
					class:media-bubble={isMediaBubble(message)}
					class:call-log-bubble={isCallLogMessage(message)}
					class:deleted={isDeletedMessage(message)}
					class:selected-target={selectedMessageId === message.id || isMultiDeleteSelected}
					class:focused={focusedMessageId === message.id}
					role={isSelectionMode ? 'button' : undefined}
					tabindex={isSelectionMode ? 0 : undefined}
					data-message-id={message.id}
					on:click={() => onMessageClick(message)}
					on:keydown={(event) => onMessageKeyDown(event, message)}
					on:contextmenu={(event) => onMessageContextMenu(event, message)}
					on:touchstart={(event) => onMessageTouchStart(event, message)}
					on:touchmove={onMessageTouchMove}
					on:touchend={onMessageTouchEnd}
					on:touchcancel={onMessageTouchCancel}
				>
						<div class="bubble-meta">
							<span>{message.senderName}</span>
							<div class="meta-right">
								<span class="time-meta">
									<time>{formatClock(message.createdAt)}</time>
									{#if message.isEdited && !isDeletedMessage(message)}
										<span class="edited-meta">edited {formatEditedClock(message.editedAt)}</span>
									{/if}
									{#if copiedMessageId === message.id}
										<span class="copied-tip">Copied</span>
									{/if}
									{#if message.type !== 'task'}
										<button
											type="button"
											class="copy-btn"
											title="Copy message"
											aria-label="Copy message"
											on:click|stopPropagation={() => void copyMessage(message)}
										>
											<IconSet name="copy" size={12} className="copy-icon" />
										</button>
									{/if}
								</span>
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
						class:collapsed={!snippetPayload &&
							message.type === 'text' &&
							isLongMessage(message.content) &&
							!Boolean(expandedMessages[message.id])}
						>
							{#if isDeletedMessage(message)}
								This message was deleted
							{:else if snippetPayload}
								{@const snippetCodeNeedsCollapse = isLongSnippetCode(snippetPayload.snippet)}
								{@const snippetCodeExpanded = Boolean(expandedSnippetCodeByMessageID[message.id])}
								{@const snippetMessageNeedsCollapse = isLongSnippetMessage(snippetPayload.message)}
								{@const snippetMessageExpanded = Boolean(
									expandedSnippetMessageByMessageID[message.id]
								)}
								<div class="snippet-card">
									<div class="snippet-card-header">
										<span class="snippet-card-label">Code Snippet</span>
										{#if snippetPayload.fileName}
											<span class="snippet-card-file">{snippetPayload.fileName}</span>
										{/if}
									</div>
									<pre
										class="snippet-code"
										class:collapsed={snippetCodeNeedsCollapse && !snippetCodeExpanded}
									><code>{snippetPayload.snippet}</code></pre>
									{#if snippetCodeNeedsCollapse}
										<button
											type="button"
											class="read-more-btn snippet-read-more-btn"
											on:click|stopPropagation={() => toggleSnippetCodeExpanded(message.id)}
										>
											{snippetCodeExpanded ? 'Collapse code' : 'Read more code'}
										</button>
									{/if}
									{#if snippetPayload.message}
										<div
											class="snippet-caption"
											class:collapsed={snippetMessageNeedsCollapse && !snippetMessageExpanded}
										>
											{snippetPayload.message}
										</div>
										{#if snippetMessageNeedsCollapse}
											<button
												type="button"
												class="read-more-btn snippet-read-more-btn"
												on:click|stopPropagation={() => toggleSnippetMessageExpanded(message.id)}
											>
												{snippetMessageExpanded ? 'Collapse note' : 'Read more note'}
											</button>
										{/if}
									{/if}
								</div>
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
							{:else if message.type === 'audio' && getMediaURL(message) && !mediaLoadFailedById[message.id]}
								<!-- svelte-ignore a11y_media_has_caption -->
								<audio
									src={getMediaURL(message)}
									class="audio-preview"
									controls
									preload="metadata"
									on:error={() => onMediaError(message.id)}
								></audio>
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
							{:else if message.type === 'task'}
							<TaskCard
								{message}
								showAddTaskControl={isMember}
								canEditTasks={isMember}
								on:toggleTask={(event) => dispatch('toggleTask', event.detail)}
								on:addTask={(event) => dispatch('addTask', event.detail)}
							/>
						{:else if isCallLogMessage(message)}
							<div class="call-log-entry">
								<svg
									class="call-log-icon {isMissedCallMessage(message) ? 'missed' : 'completed'}"
									viewBox="0 0 24 24"
									aria-hidden="true"
								>
									<path
										d="M6.6 10.8c1.6 3.1 3.9 5.5 7 7l2.3-2.3a1 1 0 0 1 1.1-.24c1.2.4 2.5.6 3.8.6a1 1 0 0 1 1 1V21a1 1 0 0 1-1 1C11 22 2 13 2 2a1 1 0 0 1 1-1h4.1a1 1 0 0 1 1 1c0 1.3.2 2.6.6 3.8a1 1 0 0 1-.24 1.1L6.6 10.8Z"
									/>
								</svg>
								<div class="call-log-copy">
									<div class="call-log-title">{getCallLogModeLabel(message)}</div>
									<div class="call-log-status">{message.content}</div>
								</div>
							</div>
						{:else if isCodeBlock(message.content)}
							<pre class="code-block"><code>{getCodeContent(message.content)}</code></pre>
						{:else}
							{message.content}
						{/if}
					</div>
					{#if !snippetPayload && message.type === 'text' && isLongMessage(message.content)}
						<button
							type="button"
							class="read-more-btn"
							on:click|stopPropagation={() => dispatch('toggleExpand', { messageId: message.id })}
						>
							{Boolean(expandedMessages[message.id]) ? 'Read less' : 'Read more'}
						</button>
					{/if}
				</article>
				{#if selectedMessageId === message.id &&
					(messageActionMode === 'edit' ||
						(messageActionMode === 'delete' && !deleteMultiEnabled))}
					<div class="selected-message-actions {isMine ? 'mine' : 'theirs'}">
						{#if messageActionMode === 'edit'}
							<button
								type="button"
								class="selected-action-button"
								on:click|stopPropagation={() => dispatch('editSelected', { messageId: message.id })}
							>
								Edit
							</button>
						{/if}
						<button
							type="button"
							class="selected-action-button danger"
							on:click|stopPropagation={() => dispatch('deleteSelected', { messageId: message.id })}
						>
							Delete
						</button>
					</div>
				{/if}
					{#if !isMine}
						<aside class="message-gutter theirs">
							{#if message.isPinned}
								<button
									type="button"
									class="gutter-pin-btn"
									title="Open pinned discussion"
									aria-label="Open pinned discussion"
									on:click|stopPropagation={() =>
										dispatch('openPinnedDiscussion', { messageId: message.id })}
								>
									<span class="gutter-pin-emoji" aria-hidden="true">📌</span>
								</button>
							{/if}
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
						{#if !isDeletedMessage(message)}
							<div class="gutter-actions">
								<button
									type="button"
									class="gutter-action-btn"
									title="Reply"
									aria-label="Reply"
									on:click|stopPropagation={() =>
										dispatch('reply', {
											messageId: message.id,
											senderName: message.senderName,
											content: getReplyDispatchContent(message)
										})}
								>
									<IconSet name="reply" size={12} className="gutter-action-icon" />
								</button>
							</div>
						{/if}
					</aside>
				{/if}
			</div>
		{/each}
		</div>
		{#if messageContextMenu.open}
			{@const contextMenuMessage = getVisibleMessageById(messageContextMenu.messageId)}
			{@const contextMenuIsMine = Boolean(
				contextMenuMessage &&
					normalizeIdentifier(contextMenuMessage.senderId) === normalizeIdentifier(currentUserId)
			)}
			<div
				class="message-context-menu-backdrop"
				aria-hidden="true"
				on:click={closeMessageContextMenu}
				on:contextmenu|preventDefault={closeMessageContextMenu}
			></div>
			<div
				class="message-context-menu"
				role="menu"
				tabindex="-1"
				aria-label="Message actions"
				style={`left: ${messageContextMenu.x}px; top: ${messageContextMenu.y}px;`}
			>
				<button
					type="button"
					class="message-context-menu-item"
					role="menuitem"
					disabled={isContextMenuActionDisabled('reply', contextMenuMessage, contextMenuIsMine)}
					on:click={() => onMessageContextAction('reply')}
				>
					<IconSet name="reply" size={14} />
					<span>Reply</span>
				</button>
				<button
					type="button"
					class="message-context-menu-item"
					role="menuitem"
					disabled={isContextMenuActionDisabled('edit', contextMenuMessage, contextMenuIsMine)}
					on:click={() => onMessageContextAction('edit')}
				>
					<IconSet name="edit" size={14} />
					<span>Edit</span>
				</button>
				<button
					type="button"
					class="message-context-menu-item danger"
					role="menuitem"
					disabled={isContextMenuActionDisabled('delete', contextMenuMessage, contextMenuIsMine)}
					on:click={() => onMessageContextAction('delete')}
				>
					<IconSet name="trash" size={14} />
					<span>Delete</span>
				</button>
				<button
					type="button"
					class="message-context-menu-item"
					role="menuitem"
					disabled={isContextMenuActionDisabled('pin', contextMenuMessage, contextMenuIsMine)}
					on:click={() => onMessageContextAction('pin')}
				>
					<IconSet name="pin" size={14} />
					<span>Pin</span>
				</button>
				<button
					type="button"
					class="message-context-menu-item"
					role="menuitem"
					disabled={isContextMenuActionDisabled('branch', contextMenuMessage, contextMenuIsMine)}
					on:click={() => onMessageContextAction('branch')}
				>
					<IconSet name="break" size={14} />
					<span>Create branch</span>
				</button>
			</div>
		{/if}
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

	.messages-shell.theme-dark {
		background: linear-gradient(180deg, #0c1423 0%, #0a1220 100%);
	}

	.messages {
		--meta-gutter-size: clamp(2.6rem, 7vw, 3.1rem);
		--action-icon-size: clamp(1.2rem, 2.8vw, 1.5rem);
		--action-hit-size: clamp(1.76rem, 3.7vw, 2.2rem);
		--copy-icon-size: calc(var(--action-icon-size) * 0.84);
		--copy-hit-size: calc(var(--action-hit-size) * 0.84);
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
		background: linear-gradient(180deg, #edf2f8 0%, #e4eaf2 100%);
		scrollbar-width: none;
		-ms-overflow-style: none;
	}

	.messages::-webkit-scrollbar {
		width: 0;
		height: 0;
		display: none;
	}

	.messages-shell.theme-dark .messages {
		background: linear-gradient(180deg, #0f192c 0%, #0b1525 100%);
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

	.day-stamp {
		align-self: center;
		margin: 0.18rem 0 0.05rem;
		padding: 0.15rem 0.52rem;
		border-radius: 999px;
		border: 1px solid #cdd6e3;
		background: #edf1f7;
		font-size: 0.66rem;
		font-weight: 600;
		letter-spacing: 0.01em;
		color: #5f6e85;
	}

	.messages-shell.theme-dark .day-stamp {
		border-color: #30445f;
		background: #122037;
		color: #a9bbd8;
	}

	.unread-divider {
		align-self: stretch;
		display: flex;
		align-items: center;
		gap: 0.45rem;
		margin: 0.1rem 0 0.1rem;
		color: #ef4444;
		font-size: 0.75rem;
		font-weight: 700;
		letter-spacing: 0.01em;
	}

	.unread-divider::before,
	.unread-divider::after {
		content: '';
		flex: 1;
		border-bottom: 1px solid rgba(239, 68, 68, 0.5);
	}

	.messages-shell.theme-dark .unread-divider {
		color: #f87171;
	}

	.messages-shell.theme-dark .unread-divider::before,
	.messages-shell.theme-dark .unread-divider::after {
		border-bottom-color: rgba(248, 113, 113, 0.55);
	}

	.messages-shell.theme-dark .older-history-indicator {
		color: #94a3b8;
	}

	.readonly-banner {
		margin: 0 0 0.4rem;
		padding: 0.45rem 0.65rem;
		border-radius: 8px;
		border: 1px solid #c9d2df;
		background: #e9eef6;
		color: #33445d;
		font-size: 0.78rem;
	}

	.messages-shell.theme-dark .readonly-banner {
		border-color: #334155;
		background: #111b30;
		color: #dbe7ff;
	}

	.join-footer {
		border-top: 1px solid #cbd4e1;
		background: #eef3f9;
		padding: 0.7rem;
		display: flex;
		justify-content: center;
	}

	.join-room-btn {
		border: 1px solid #4b5e7b;
		background: #4b5e7b;
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
		border: 1px solid #c5cfdd;
		border-radius: 999px;
		background: rgba(236, 242, 250, 0.95);
		color: #334158;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		box-shadow: 0 8px 18px rgba(0, 0, 0, 0.16);
		z-index: 3;
	}

	.scroll-bottom-button:hover {
		background: #dde5f0;
	}

	.empty-thread {
		color: #5e6d84;
		font-size: 0.84rem;
		padding: 1rem;
	}

	.messages-shell.theme-dark .empty-thread {
		color: #9fb0cf;
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

	.delete-select-toggle {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 1.25rem;
		min-width: 1.25rem;
		padding-top: 0.18rem;
	}

	.delete-select-toggle input {
		width: 1rem;
		height: 1rem;
		cursor: pointer;
		accent-color: #22c55e;
	}

	.message-gutter {
		flex: 0 0 var(--meta-gutter-size);
		width: var(--meta-gutter-size);
		min-height: 1rem;
		padding-top: 0.2rem;
		display: flex;
		flex-direction: column;
		gap: 0.24rem;
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
		color: #667287;
		background: rgba(244, 247, 252, 0.76);
		border: 1px solid #cfd7e3;
		border-radius: 999px;
		padding: 0.16rem 0.22rem;
	}

	.messages-shell.theme-dark .gutter-stat {
		background: rgba(15, 23, 42, 0.8);
		border-color: #334155;
		color: #bfd0ed;
	}

	.message-row.mine .gutter-stat {
		background: rgba(79, 94, 118, 0.92);
		border-color: rgba(94, 110, 136, 0.95);
		color: #e6edf8;
	}

	.gutter-pin-btn {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: var(--action-hit-size);
		height: var(--action-hit-size);
		padding: 0;
		border-radius: 8px;
		border: 1px solid #e6ccd2;
		background: rgba(255, 255, 255, 0.75);
		color: #dc2626;
		cursor: pointer;
		transition:
			transform 120ms ease,
			background 140ms ease,
			border-color 140ms ease;
	}

	.gutter-pin-btn:hover {
		background: rgba(220, 38, 38, 0.1);
		transform: translateY(-1px);
	}

	.gutter-pin-btn:focus-visible {
		outline: 2px solid rgba(220, 38, 38, 0.38);
		outline-offset: 1px;
	}

	.messages-shell.theme-dark .gutter-pin-btn {
		background: rgba(15, 23, 42, 0.84);
		border-color: #475569;
		color: #fda4af;
	}

	.message-row.mine .gutter-pin-btn {
		background: rgba(79, 94, 118, 0.94);
		border-color: rgba(105, 120, 145, 0.46);
		color: #fca5a5;
	}

	.gutter-pin-emoji {
		font-size: 0.86rem;
		line-height: 1;
	}

	.gutter-stat strong {
		font-size: 0.66rem;
		font-weight: 600;
		color: inherit;
	}

	.gutter-actions {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 0.24rem;
		width: 100%;
		opacity: 0;
		visibility: hidden;
		pointer-events: none;
		transition: opacity 140ms ease;
	}

	.message-row:hover .gutter-actions,
	.message-row:focus-within .gutter-actions {
		opacity: 1;
		visibility: visible;
		pointer-events: auto;
	}

	.message-row.compact-gutter .gutter-actions.mine-actions {
		flex-direction: row;
		width: auto;
	}

	.gutter-action-btn {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: var(--action-hit-size);
		height: var(--action-hit-size);
		border: 1px solid #c8d1de;
		border-radius: 999px;
		background: #edf2f8;
		color: #3e4b61;
		cursor: pointer;
		padding: 0;
		transition:
			transform 120ms ease,
			background 140ms ease;
	}

	.message-row.mine .gutter-action-btn {
		border-color: rgba(105, 120, 145, 0.46);
		background: rgba(79, 94, 118, 0.94);
		color: #ecf2fb;
	}

	.gutter-action-btn:hover {
		transform: translateY(-1px);
		background: #dfe7f3;
		border-color: #aebcd1;
	}

	.message-row.mine .gutter-action-btn:hover {
		background: rgba(104, 120, 145, 0.96);
		border-color: rgba(171, 188, 214, 0.62);
	}

	.gutter-action-btn:focus-visible {
		outline: 2px solid #6b7280;
		outline-offset: 1px;
	}

	.message-context-menu-backdrop {
		position: fixed;
		inset: 0;
		z-index: 30;
		background: transparent;
	}

	.message-context-menu {
		position: fixed;
		z-index: 31;
		display: flex;
		flex-direction: column;
		gap: 0.16rem;
		width: 11.6rem;
		padding: 0.35rem;
		border-radius: 10px;
		border: 1px solid #c9d3e2;
		background: #f8fbff;
		box-shadow: 0 14px 32px rgba(15, 23, 42, 0.18);
	}

	.messages-shell.theme-dark .message-context-menu {
		border-color: #31435e;
		background: #0f1a2b;
		box-shadow: 0 14px 36px rgba(2, 6, 23, 0.6);
	}

	.message-context-menu-item {
		display: inline-flex;
		align-items: center;
		gap: 0.46rem;
		width: 100%;
		padding: 0.46rem 0.5rem;
		border: 0;
		border-radius: 8px;
		background: transparent;
		color: #334155;
		font-size: 0.8rem;
		font-weight: 600;
		text-align: left;
		cursor: pointer;
	}

	.messages-shell.theme-dark .message-context-menu-item {
		color: #d6e2f5;
	}

	.message-context-menu-item:hover {
		background: #e5edf8;
	}

	.messages-shell.theme-dark .message-context-menu-item:hover {
		background: #1a2b42;
	}

	.message-context-menu-item.danger {
		color: #b91c1c;
	}

	.messages-shell.theme-dark .message-context-menu-item.danger {
		color: #fda4af;
	}

	.message-context-menu-item:disabled {
		opacity: 0.48;
		cursor: not-allowed;
	}

	.message-context-menu-item:disabled:hover {
		background: transparent;
	}

	.bubble {
		position: relative;
		max-width: min(calc(100% - var(--meta-gutter-size) - 0.6rem), 40rem);
		width: fit-content;
		border-radius: 12px;
		padding: 0.76rem 0.86rem;
		background: #f7f9fc;
		border: 1px solid #ccd5e2;
		box-shadow: 0 2px 6px rgba(15, 23, 42, 0.08);
		box-sizing: border-box;
		overflow: visible;
	}

	@media (pointer: coarse) {
		.bubble {
			-webkit-touch-callout: none;
			-webkit-user-select: none;
			user-select: none;
			touch-action: manipulation;
		}
	}

	.messages-shell.theme-dark .bubble {
		background: #101b2d;
		border-color: #2f3f59;
		color: #f1f5ff;
	}

	.selection-mode .bubble.selectable {
		cursor: pointer;
		outline: 1px dashed transparent;
	}

	.selection-mode .bubble.selectable:hover {
		outline-color: #5d6980;
	}

	.bubble.mine {
		background: #4f5f78;
		border-color: #4f5f78;
		color: #edf2fa;
	}

	.messages-shell.theme-dark .bubble.mine {
		background: #1c2c46;
		border-color: #3f5479;
		color: #e6edf8;
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

	.bubble.selected-target {
		box-shadow:
			0 0 0 2px rgba(34, 197, 94, 0.85),
			0 8px 18px rgba(0, 0, 0, 0.12);
	}

	.selected-message-actions {
		flex: 0 0 auto;
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
		padding-top: 0.25rem;
		min-width: 4.8rem;
	}

	.selected-message-actions.mine {
		align-items: flex-end;
	}

	.selected-message-actions.theirs {
		align-items: flex-start;
	}

	.selected-action-button {
		border: 1px solid #d5d5dc;
		background: #f9f9fb;
		color: #3f3f49;
		border-radius: 8px;
		font-size: 0.74rem;
		font-weight: 600;
		padding: 0.24rem 0.48rem;
		cursor: pointer;
	}

	.selected-action-button:hover {
		background: #f0f0f4;
	}

	.selected-action-button.danger {
		color: #8f2d2d;
	}

	.bubble.deleted {
		background: #e8edf4;
		border-color: #d1dae6;
		color: #67748a;
	}

	.bubble.mine.deleted {
		background: #5a6780;
		border-color: #65738f;
		color: #dde5f2;
	}

	.copy-btn {
		position: absolute;
		left: 50%;
		top: 50%;
		transform: translate(-50%, -50%);
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: var(--copy-hit-size);
		height: var(--copy-hit-size);
		border: none;
		border-radius: 999px;
		background: rgba(39, 50, 69, 0.8);
		color: #ffffff;
		opacity: 0;
		pointer-events: none;
		cursor: pointer;
		transition: opacity 140ms ease;
		padding: 0;
	}

	.message-row:hover .copy-btn,
	.message-row:focus-within .copy-btn {
		opacity: 0.9;
		pointer-events: auto;
	}

	.message-row:hover .time-meta time,
	.message-row:focus-within .time-meta time {
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
		color: #5f6d83;
		margin-bottom: 0.44rem;
	}

	.bubble.mine .bubble-meta {
		color: #dce5f2;
	}

	.messages-shell.theme-dark .bubble-meta {
		color: #aebbd2;
	}

	.messages-shell.theme-dark .bubble.mine .bubble-meta {
		color: #c9d6eb;
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

	.reply-snippet {
		width: 100%;
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		margin-bottom: 0.4rem;
		padding: 0.34rem 0.48rem;
		border-radius: 8px;
		border: 1px solid #cfd7e3;
		background: #e9eef6;
		color: #526178;
		font-size: 0.7rem;
		line-height: 1.25;
		word-break: break-word;
		text-align: left;
		cursor: pointer;
	}

	.messages-shell.theme-dark .reply-snippet {
		border-color: #3a4b67;
		background: #142036;
		color: #c5d2ea;
	}

	.bubble.mine .reply-snippet {
		border-color: rgba(201, 214, 235, 0.4);
		background: rgba(236, 243, 255, 0.14);
		color: #e5edf9;
	}

	.reply-snippet:hover {
		background: #d9e3f1;
		border-color: #b9c7da;
	}

	.messages-shell.theme-dark .reply-snippet:hover {
		background: #1a2a44;
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
		border: 1px solid #c5cfdd;
		border-radius: 999px;
		background: #eef2f8;
		color: #2e3d53;
		padding: 0.08rem 0.33rem;
		font-size: 0.68rem;
		cursor: pointer;
		transition:
			background 140ms ease,
			border-color 140ms ease,
			color 140ms ease,
			transform 140ms ease;
	}

	.messages-shell.theme-dark .break-indicator {
		border-color: #334155;
		background: #0f172a;
		color: #dbe7ff;
	}

	.break-indicator:hover {
		background: #dfe7f3;
		border-color: #aebed3;
		transform: translateY(-1px);
	}

	.messages-shell.theme-dark .break-indicator:hover {
		background: #1a263c;
		border-color: #506182;
	}

	.break-indicator-count {
		font-size: 0.74rem;
		font-weight: 700;
		line-height: 1;
		min-width: 1.2ch;
		text-align: center;
	}

	:global(.copy-icon) {
		width: var(--copy-icon-size);
		height: var(--copy-icon-size);
	}

	:global(.gutter-action-icon) {
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
		background: #dbe2ec;
	}

	.video-preview {
		max-height: 360px;
		background: #222d3f;
	}

	.audio-preview {
		display: block;
		width: 100%;
		max-width: none;
	}

	.file-link {
		color: #2f3e56;
		font-weight: 600;
		text-decoration: none;
		font-size: 0.8rem;
	}

	.messages-shell.theme-dark .file-link {
		color: #d8e2f1;
	}

	.file-link:hover {
		text-decoration: underline;
	}

	.file-card {
		display: flex;
		flex-direction: column;
		gap: 0.45rem;
		border: 1px solid #c7d0de;
		border-radius: 10px;
		background: #e8edf4;
		padding: 0.5rem 0.62rem;
		width: 100%;
		max-width: none;
		box-sizing: border-box;
	}

	.messages-shell.theme-dark .file-card {
		border-color: #3a4b66;
		background: #16243b;
	}

	.file-meta {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		color: #263246;
	}

	.messages-shell.theme-dark .file-meta {
		color: #d3deee;
	}

	.file-name {
		font-size: 0.82rem;
		font-weight: 600;
		line-height: 1.2;
		word-break: break-word;
	}

	.file-ext {
		font-size: 0.68rem;
		color: #66758b;
		margin-top: 0.1rem;
	}

	.messages-shell.theme-dark .file-ext {
		color: #9fb0c8;
	}

	.file-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.7rem;
	}

	.pdf-preview {
		width: 100%;
		height: 260px;
		border: 1px solid #c4cedd;
		border-radius: 8px;
		background: #f4f7fc;
		box-sizing: border-box;
	}

	.file-inline-preview {
		margin-bottom: 0.45rem;
	}

	.bubble-content {
		font-size: 0.93rem;
		line-height: 1.52;
		color: #1f2d42;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.messages-shell.theme-dark .bubble-content {
		color: #ccd8ea;
	}

	.bubble-content.deleted-text {
		font-style: italic;
		color: #67758b;
	}

	.messages-shell.theme-dark .bubble-content.deleted-text {
		color: #a9b8ce;
	}

	.bubble.mine .bubble-content {
		color: #eef3fb;
	}

	.messages-shell.theme-dark .bubble.mine .bubble-content {
		color: #d7deea;
	}

	.bubble.mine .bubble-content.deleted-text {
		color: #dce4f1;
	}

	.messages-shell.theme-dark .bubble.mine .bubble-content.deleted-text {
		color: #b3bfd3;
	}

	.media-caption {
		margin-top: 0.48rem;
		font-size: 0.9rem;
		line-height: 1.45;
		color: #243146;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.messages-shell.theme-dark .media-caption {
		color: #cad7ea;
	}

	.bubble.mine .media-caption {
		color: #e6edf8;
	}

	.messages-shell.theme-dark .bubble.mine .media-caption {
		color: #ced8ea;
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
		background: #1f2a3d;
		color: #e6edf8;
		font-family:
			ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
		font-size: 0.83rem;
		line-height: 1.4;
		overflow-x: auto;
		white-space: pre;
		word-break: normal;
	}

	.snippet-card {
		display: grid;
		gap: 0.52rem;
		width: 100%;
	}

	.snippet-card-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		gap: 0.45rem;
	}

	.snippet-card-label {
		font-size: 0.66rem;
		font-weight: 700;
		letter-spacing: 0.03em;
		text-transform: uppercase;
		color: #4d5f79;
	}

	.snippet-card-file {
		display: inline-flex;
		align-items: center;
		max-width: 100%;
		border: 1px solid #cad4e4;
		background: #eaf0fb;
		color: #445a78;
		border-radius: 999px;
		padding: 0.12rem 0.4rem;
		font-size: 0.66rem;
		line-height: 1.2;
		font-family:
			ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.snippet-code {
		margin: 0;
		max-height: 320px;
		padding: 0.66rem 0.72rem;
		border-radius: 10px;
		border: 1px solid #334a66;
		background: #111d30;
		color: #e4edfa;
		font-family:
			ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
		font-size: 0.82rem;
		line-height: 1.42;
		overflow: auto;
		white-space: pre;
		word-break: normal;
	}

	.snippet-code.collapsed {
		max-height: 250px;
		overflow: hidden;
		mask-image: linear-gradient(180deg, #000 74%, transparent);
		-webkit-mask-image: linear-gradient(180deg, #000 74%, transparent);
	}

	.snippet-caption {
		margin: 0;
		font-size: 0.89rem;
		line-height: 1.45;
		color: #1f2d42;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.snippet-caption.collapsed {
		max-height: 7.5rem;
		overflow: hidden;
		mask-image: linear-gradient(180deg, #000 76%, transparent);
		-webkit-mask-image: linear-gradient(180deg, #000 76%, transparent);
	}

	.snippet-read-more-btn {
		margin-top: -0.14rem;
		align-self: flex-start;
	}

	.bubble.mine .snippet-card-label {
		color: #dbe6f9;
	}

	.bubble.mine .snippet-card-file {
		border-color: rgba(224, 235, 255, 0.55);
		background: rgba(228, 238, 255, 0.16);
		color: #edf4ff;
	}

	.bubble.mine .snippet-caption {
		color: #e9f0fc;
	}

	.messages-shell.theme-dark .snippet-card-label {
		color: #cad5e8;
	}

	.messages-shell.theme-dark .snippet-card-file {
		border-color: #42546f;
		background: #1a273d;
		color: #dce7fb;
	}

	.messages-shell.theme-dark .snippet-code {
		border-color: #3a4f6d;
		background: #101b2d;
		color: #e7effd;
	}

	.messages-shell.theme-dark .snippet-caption {
		color: #d2ddf0;
	}

	.messages-shell.theme-dark .bubble.mine .snippet-card-label {
		color: #d9e4f9;
	}

	.messages-shell.theme-dark .bubble.mine .snippet-card-file {
		border-color: #586e8f;
		background: #2a3a55;
		color: #f0f6ff;
	}

	.messages-shell.theme-dark .bubble.mine .snippet-caption {
		color: #e8eefb;
	}

	.read-more-btn {
		margin-top: 0.5rem;
		border: none;
		background: transparent;
		color: #2d3d56;
		font-size: 0.78rem;
		font-weight: 600;
		padding: 0;
		cursor: pointer;
	}

	.read-more-btn:hover {
		color: #1b2f4a;
		text-decoration: underline;
	}

	.messages-shell.theme-dark .read-more-btn {
		color: #c9d5e8;
	}

	.bubble.mine .read-more-btn {
		color: #e9f0fb;
	}

	.messages-shell.theme-dark .bubble.mine .read-more-btn {
		color: #d5dff0;
	}

	.messages-shell.theme-dark {
		background: linear-gradient(180deg, #070707 0%, #0d0d0e 100%);
	}

	.messages-shell.theme-dark .messages {
		background: linear-gradient(180deg, #0a0a0b 0%, #101011 100%);
	}

	.messages-shell.theme-dark .day-stamp {
		border-color: #2f2f32;
		background: #151517;
		color: #c8c8cd;
	}

	.messages-shell.theme-dark .unread-divider {
		color: #c4c4c9;
	}

	.messages-shell.theme-dark .unread-divider::before,
	.messages-shell.theme-dark .unread-divider::after {
		border-bottom-color: rgba(190, 190, 196, 0.45);
	}

	.messages-shell.theme-dark .older-history-indicator {
		color: #9f9fa8;
	}

	.messages-shell.theme-dark .readonly-banner {
		border-color: #2c2c2f;
		background: #151517;
		color: #d2d2d8;
	}

	.messages-shell.theme-dark .empty-thread {
		color: #b0b0b8;
	}

	.messages-shell.theme-dark .gutter-stat {
		background: rgba(24, 24, 26, 0.9);
		border-color: #333336;
		color: #d0d0d7;
	}

	.messages-shell.theme-dark .gutter-pin-btn {
		background: rgba(22, 22, 24, 0.9);
		border-color: #3a3a3f;
		color: #f0f0f4;
	}

	.message-row.mine .gutter-pin-btn {
		background: rgba(34, 34, 37, 0.95);
		border-color: #444449;
		color: #f2f2f6;
	}

	.message-row.mine .gutter-stat {
		background: rgba(31, 31, 34, 0.95);
		border-color: #3e3e43;
		color: #e6e6eb;
	}

	.messages-shell.theme-dark .bubble {
		background: #171719;
		border-color: #303034;
		color: #ededf2;
	}

	.messages-shell.theme-dark .bubble.mine {
		background: #1f1f22;
		border-color: #38383d;
		color: #f2f2f6;
	}

	.messages-shell.theme-dark .bubble-meta {
		color: #bbbbc4;
	}

	.messages-shell.theme-dark .bubble.mine .bubble-meta {
		color: #d0d0d8;
	}

	.messages-shell.theme-dark .reply-snippet {
		border-color: #333338;
		background: #1a1a1d;
		color: #d5d5dc;
	}

	.messages-shell.theme-dark .reply-snippet:hover {
		background: #202024;
	}

	.messages-shell.theme-dark .bubble.mine .reply-snippet {
		border-color: #3c3c41;
		background: #242428;
		color: #ececf1;
	}

	.messages-shell.theme-dark .bubble.mine .reply-snippet:hover {
		background: #2b2b30;
	}

	.messages-shell.theme-dark .break-indicator {
		border-color: #3a3a3f;
		background: #1a1a1d;
		color: #d8d8df;
	}

	.messages-shell.theme-dark .file-link {
		color: #e1e1e8;
	}

	.messages-shell.theme-dark .file-card {
		border-color: #343439;
		background: #1a1a1d;
	}

	.messages-shell.theme-dark .file-meta {
		color: #e2e2ea;
	}

	.messages-shell.theme-dark .file-ext {
		color: #acacb6;
	}

	.messages-shell.theme-dark .bubble-content {
		color: #e6e6ec;
	}

	.messages-shell.theme-dark .bubble-content.deleted-text {
		color: #adadb6;
	}

	.messages-shell.theme-dark .bubble.mine .bubble-content {
		color: #f0f0f5;
	}

	.messages-shell.theme-dark .bubble.mine .bubble-content.deleted-text {
		color: #c8c8d0;
	}

	.messages-shell.theme-dark .media-caption {
		color: #d6d6de;
	}

	.messages-shell.theme-dark .bubble.mine .media-caption {
		color: #ececf2;
	}

	.messages-shell.theme-dark .read-more-btn {
		color: #d0d0d9;
	}

	.messages-shell.theme-dark .read-more-btn:hover {
		color: #f0f0f5;
	}

	.messages-shell.theme-dark .bubble.mine .read-more-btn {
		color: #e8e8ef;
	}

	.bubble.call-log-bubble {
		background: #e8ebf0;
		border-color: #d4dae4;
		color: #1f2937;
	}

	.messages-shell.theme-dark .bubble.call-log-bubble {
		background: #1c1d22;
		border-color: #30313a;
		color: #e5e7eb;
	}

	.call-log-entry {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
	}

	.call-log-icon {
		width: 1rem;
		height: 1rem;
		stroke: currentColor;
		stroke-width: 1.7;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.call-log-icon.missed {
		color: #d1495b;
	}

	.call-log-icon.completed {
		color: #2e9f5f;
	}

	.call-log-copy {
		display: grid;
		gap: 0.1rem;
	}

	.call-log-title {
		font-weight: 600;
		font-size: 0.78rem;
		color: #1f2937;
	}

	.call-log-status {
		font-size: 0.74rem;
		opacity: 0.85;
		color: #4b5563;
	}

	.messages-shell.theme-dark .call-log-title {
		color: #e5e7eb;
	}

	.messages-shell.theme-dark .call-log-status {
		color: #cbd5e1;
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
