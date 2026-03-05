<script lang="ts">
	import { browser } from '$app/environment';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import ChatComposer from '$lib/components/chat/ChatComposer.svelte';
	import Board from '$lib/components/chat/Board.svelte';
	import CodeCanvas from '$lib/components/canvas/CodeCanvas.svelte';
	import DiscussionModal from '$lib/components/chat/DiscussionModal.svelte';
	import ChatRoomDetailsPanel from '$lib/components/chat/ChatRoomDetailsPanel.svelte';
	import ChatRoomHeader from '$lib/components/chat/ChatRoomHeader.svelte';
	import ChatStatusBars from '$lib/components/chat/ChatStatusBars.svelte';
	import ChatSidebar from '$lib/components/chat/ChatSidebar.svelte';
	import ChatUiDialog from '$lib/components/chat/ChatUiDialog.svelte';
	import ChatWindow from '$lib/components/chat/ChatWindow.svelte';
	import OnlinePanel from '$lib/components/chat/OnlinePanel.svelte';
	import { activeRoomPassword, authToken, currentUser, isDarkMode } from '$lib/store';
	import type {
		ChatMessage,
		ChatThread,
		ComposerMediaPayload,
		MessageActionMode,
		OnlineMember,
		ReplyTarget,
		RoomMenuMode,
		RoomMeta,
		SidebarRoom,
		SocketEnvelope,
		ThemePreference,
		ThreadStatus,
		UiDialogState
	} from '$lib/types/chat';
	import {
		getUTF8ByteLength,
		createMessageId,
		formatDateTime,
		formatRoomName,
		MESSAGE_TEXT_MAX_BYTES,
		normalizeEpoch,
		normalizeIdentifier,
		normalizeMessageID,
		normalizeRoomIDValue,
		normalizeRoomNameValue,
		normalizeUsernameValue,
		parseOptionalTimestamp,
		resolveRoomMembership,
		toBool,
		toInt,
		toStringValue,
		toTimestamp
	} from '$lib/utils/chat/core';
	import {
		applyReadProgress as applyReadProgressState,
		getLastReadTimestamp as getLastReadTimestampState,
		getUnreadStartMessageId as getUnreadStartMessageIdState
	} from '$lib/utils/chat/readProgress';
	import {
		buildRoomPasswordHash,
		normalizeAdminCodeValue,
		normalizeRoomAccessPasswordValue,
		normalizeRoomPasswordValue
	} from '$lib/utils/chat/security';
	import {
		addTaskItem,
		parseTaskMessagePayload,
		stringifyTaskMessagePayload,
		toggleTaskItem
	} from '$lib/utils/chat/task';
	import {
		buildDiscussionCommentMap,
		discussionCommentsEndpoint,
		readDiscussionCommentsCache,
		resolveDiscussionCommentDepth,
		roomPinsEndpoint,
		upsertDiscussionCommentList,
		writeDiscussionCommentsCache
	} from '$lib/utils/chat/discussion';
	import {
		isEnvelope,
		resolveDiscussionPinMessageID,
		resolveEnvelopePayloadRecord,
		resolveEnvelopeRoomID,
		resolveEnvelopeTargetUserID
	} from '$lib/utils/chat/envelope';
	import {
		getRemainingHoursLabel as getRemainingHoursLabelState,
		getRoomCreatedAt as getRoomCreatedAtState,
		getRoomExpiry as getRoomExpiryState
	} from '$lib/utils/chat/roomTiming';
	import { createTypingController } from '$lib/utils/chat/typingController';
	import {
		buildReplySnippet,
		DELETED_MESSAGE_PLACEHOLDER,
		getMessagePreviewText,
		parseIncomingMessage,
		parseMember,
		toWireMessage
	} from '$lib/utils/chat/messages';
	import {
		collectLocalRoomSubtreeIDs,
		filterThreadList,
		filterThreadsByStatus,
		sortThreads
	} from '$lib/utils/chat/threadList';
	import {
		applyMessageDeleteState,
		applyMessageEditState,
		createThread as createThreadState,
		dedupeMembers as dedupeMembersState,
		ensureOnlineSeed as ensureOnlineSeedState,
		ensureRoomMeta as ensureRoomMetaState,
		ensureRoomThread as ensureRoomThreadState,
		markRoomAsRead as markRoomAsReadState,
		mergeMessagesState,
		removeOnlineMember as removeOnlineMemberState,
		updateThreadPreview as updateThreadPreviewState,
		upsertMessageState,
		upsertOnlineMember as upsertOnlineMemberState
	} from '$lib/utils/chat/pageState';
	import { createChatDialogController } from '$lib/utils/chat/dialogController';
	import {
		getTrustedDevicePreference,
		isOfflineCacheSupported,
		loadEncryptedRoomMessages,
		saveEncryptedRoomMessages,
		setTrustedDevicePreference,
		wipeEncryptedRoomCache,
		type TrustedDevicePreference
	} from '$lib/utils/offlineCache';
	import { decryptText, encryptText } from '$lib/utils/crypto';
	import { getOrInitIdentity } from '$lib/utils/identity';
	import { generateUsername } from '$lib/utils/usernameGenerator';
	import { clearSessionToken, getSessionToken, setSessionToken } from '$lib/utils/sessionToken';
	import {
		closeGlobalSocket,
		globalMessages,
		initGlobalSocket,
		sendSocketPayload,
		subscribeToRooms
	} from '$lib/ws';
	import { onDestroy, onMount, tick } from 'svelte';
	import './page.css';

	const CLIENT_LOG_PREFIX = '[chat-client]';
	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';
	const CLIENT_DEBUG = (import.meta.env.VITE_CHAT_DEBUG as string | undefined) === '1';
	const TYPING_PING_INTERVAL_MS = 5000;
	const TYPING_STOP_DELAY_MS = 5000;
	const TYPING_SAFETY_TIMEOUT_MS = 7000;
	const DISCUSSION_MAX_REPLY_DEPTH = 4;
	const THEME_PREFERENCE_KEY = 'converse_theme_preference';
	const PROTECTED_ROOM_PREVIEW_TEXT = 'Protected room. Join with password to preview messages.';
	const LEGACY_ROOM_TIME_QUERY_KEYS = [
		'createdAt',
		'expiresAt',
		'serverNow',
		'created_at',
		'expires_at',
		'server_now'
	] as const;

	type CanvasPresenceUser = {
		id: string;
		name: string;
		color: string;
	};

	type WorkspaceTool = 'board' | 'canvas';

	function getCanvasPresenceColor(user: { color?: unknown } | null | undefined) {
		if (typeof user?.color === 'string') {
			const normalized = user.color.trim();
			if (normalized) {
				return normalized;
			}
		}
		return '#3b82f6';
	}

	let sidebarRefreshTimer: ReturnType<typeof setInterval> | null = null;
	let roomExpiryTicker: ReturnType<typeof setInterval> | null = null;
	$: if (browser) {
		// This component no longer controls the body class, but we might need the value locally.
		// Let's ensure it's synced from the store.
	}

	function toggleThemePreference() {
		const nextPreference = !$isDarkMode ? 'dark' : 'light';
		isDarkMode.set(!$isDarkMode);
		themePreference = nextPreference;
		if (browser) {
			window.localStorage.setItem(THEME_PREFERENCE_KEY, nextPreference);
		}
		showLeftMenu = false;
	}
	let isSelectionMode = false;
	let messageActionMode: MessageActionMode = 'none';
	let selectedActionMessageId = '';
	let chatListSearch = '';
	let roomMessageSearch = '';
	let draftMessage = '';
	let attachedFile: File | null = null;
	let showLeftMenu = false;
	let showRoomSearch = false;
	let showRoomDetails = false;
	let showBoardView = false;
	let isCanvasOpen = false;
	let isCanvasFullscreen = false;
	let lastWorkspaceTool: WorkspaceTool = 'board';
	let canvasUser: CanvasPresenceUser = { id: 'guest', name: 'Guest', color: '#3b82f6' };
	let themePreference: ThemePreference = 'system';
	let removeSystemThemeListener: (() => void) | null = null;
	let cachePersistTimers = new Map<string, ReturnType<typeof setTimeout>>();
	let showToast = false;
	let toastMessage = '';
	let toastTimer: ReturnType<typeof setTimeout> | null = null;
	let lastToastRoom = '';
	let roomMembershipSynced: Record<string, boolean> = {};
	let roomMembershipSyncing: Record<string, boolean> = {};
	let isMobileView = false;
	let mobilePane: 'list' | 'chat' = 'chat';
	let focusMessageId = '';
	let focusConsumedForRoom = false;
	let focusRoomTracker = '';
	let activeRoomId = '';
	let activeFirstUnreadMessageId = '';

	let roomThreads: ChatThread[] = [];
	let messagesByRoom: Record<string, ChatMessage[]> = {};
	let onlineByRoom: Record<string, OnlineMember[]> = {};
	let roomMetaById: Record<string, RoomMeta> = {};
	let typingUsersByRoom: Record<string, Record<string, { name: string; expiresAt: number }>> = {};
	let historyLoadingByRoom: Record<string, boolean> = {};
	let historyHasMoreByRoom: Record<string, boolean> = {};
	let offlineHydratedByRoom: Record<string, boolean> = {};
	let unreadAnchorByRoom: Record<string, string> = {};
	let trustedDevicePreference: TrustedDevicePreference = 'unset';
	let showTrustedDevicePrompt = false;
	let trustedCachingEnabled = false;
	let isExtendingRoom = false;
	let expandedMessages: Record<string, boolean> = {};
	let activeReply: ReplyTarget | null = null;
	let deleteMultiEnabled = false;
	let selectedDeleteMessageIds: string[] = [];
	let isDiscussionOpen = false;
	let activeDiscussionTaskId = '';
	let activeDiscussionTask: ChatMessage | null = null;
	let discussionComments: ChatMessage[] = [];
	let discussionCommentsCacheByTaskKey: Record<string, ChatMessage[]> = {};
	let discussionBackgroundUnreadCount = 0;
	let discussionOpenedAtMs = 0;
	let discussionTaskTracker = '';
	let identityReady = !browser;
	let roomExpiryTickMs = Date.now();
	let activeRoomRemainingMs = 0;
	let activeRoomCreatedAtMs = 0;
	let activeRoomExpiresAtMs = 0;
	let activeRemainingLabel = '--';
	let isRoomExpired = false;
	let serverClockOffsetMs = 0;
	let serverNowAnchorMs = 0;
	let serverNowAnchorPerfMs = 0;
	let uiDialog: UiDialogState = { kind: 'none' };
	const dialogController = createChatDialogController({
		getDialog: () => uiDialog,
		setDialog: (next) => {
			uiDialog = next;
		},
		normalizeRoomNameValue
	});
	const {
		closeUiDialog,
		onUiDialogConfirm,
		openConfirmDialog,
		openPromptDialog,
		openRoomActionDialog,
		updateUiPromptValue,
		updateRoomActionMode,
		updateRoomActionName
	} = dialogController;
	const typingController = createTypingController({
		getRoomId: () => roomId,
		getIsMember: () => isMember,
		getTypingUsersByRoom: () => typingUsersByRoom,
		setTypingUsersByRoom: (next) => {
			typingUsersByRoom = next;
		},
		normalizeIdentifier,
		sendSocketPayload,
		typingPingIntervalMs: TYPING_PING_INTERVAL_MS,
		typingStopDelayMs: TYPING_STOP_DELAY_MS,
		typingSafetyTimeoutMs: TYPING_SAFETY_TIMEOUT_MS
	});
	let chatWindowRef: {
		capturePrependAnchor?: () => { scrollTop: number; scrollHeight: number } | null;
		restorePrependAnchor?: (anchor: { scrollTop: number; scrollHeight: number } | null) => void;
	} | null = null;
	let lastHandledPasswordRouteSignature = '';
	let lastLegacyTimingParamCleanupSignature = '';
	let skipPasswordResetForPath = '';

	$: roomId = normalizeRoomIDValue(decodeURIComponent($page.params.roomId ?? ''));
	$: roomRouteSignature = `${$page.url.pathname}|${$page.url.search}|${$page.url.hash}`;
	$: if (browser && roomRouteSignature !== lastHandledPasswordRouteSignature) {
		lastHandledPasswordRouteSignature = roomRouteSignature;
		syncActiveRoomPasswordFromHash();
	}
	$: if (browser && roomRouteSignature !== lastLegacyTimingParamCleanupSignature) {
		lastLegacyTimingParamCleanupSignature = roomRouteSignature;
		const sanitized = new URLSearchParams($page.url.searchParams.toString());
		if (removeLegacyRoomTimeQueryParams(sanitized)) {
			const nextQuery = sanitized.toString();
			const nextURL = `${$page.url.pathname}${nextQuery ? `?${nextQuery}` : ''}${$page.url.hash}`;
			void goto(nextURL, { replaceState: true, noScroll: true, keepFocus: true });
		}
	}
	$: activeRoomId = roomId;
	$: roomNameFromURL = normalizeRoomNameValue(
		decodeURIComponent($page.url.searchParams.get('name') ?? '').trim()
	);
	$: focusMessageIdFromURL = normalizeMessageID($page.url.searchParams.get('focusMsg') ?? '');
	$: roomMemberHint = $page.url.searchParams.get('member');
	$: currentUserId = $currentUser?.id ?? 'guest';
	$: currentUsername = normalizeUsernameValue($currentUser?.username ?? 'Guest') || 'Guest';
	$: canvasUser = {
		id: currentUserId,
		name: currentUsername,
		color: getCanvasPresenceColor($currentUser as { color?: unknown } | null)
	} satisfies CanvasPresenceUser;
	$: activeThread =
		roomThreads.find((thread) => thread.id === roomId) ??
		createThread(
			roomId || 'default_room',
			roomNameFromURL || undefined,
			roomMemberHint === '1' ? 'joined' : 'discoverable'
		);
	$: currentMessages = activeThread?.status === 'left' ? [] : (messagesByRoom[roomId] ?? []);
	$: activeDiscussionTask =
		(activeDiscussionTaskId &&
			currentMessages.find(
				(message) => normalizeMessageID(message.id) === normalizeMessageID(activeDiscussionTaskId)
			)) ||
		null;
	$: if (!isDiscussionOpen) {
		discussionOpenedAtMs = 0;
	}
	$: if (isDiscussionOpen) {
		const normalizedTaskID = normalizeMessageID(activeDiscussionTaskId);
		if (normalizedTaskID && normalizedTaskID !== discussionTaskTracker) {
			discussionTaskTracker = normalizedTaskID;
			discussionOpenedAtMs = Date.now();
		}
	}
	$: discussionBackgroundUnreadCount =
		isDiscussionOpen && discussionOpenedAtMs > 0
			? discussionComments.filter((comment) => {
					if (normalizeIdentifier(comment.senderId) === normalizeIdentifier(currentUserId)) {
						return false;
					}
					return comment.createdAt > discussionOpenedAtMs;
				}).length
			: 0;
	$: currentOnlineMembers = prioritizeOnlineMembersForViewer(
		onlineByRoom[roomId] ?? [],
		currentUserId
	);
	$: isActiveRoomAdmin = Boolean(activeThread?.isAdmin);
	$: isMember = resolveRoomMembership(roomId, roomThreads, roomMemberHint);
	$: canModerateBoard = isMember && !isRoomExpired && isActiveRoomAdmin;
	$: activeUnreadCount = activeThread?.unread ?? 0;
	$: activeFirstUnreadMessageId = getUnreadStartMessageId(roomId);
	$: activeLastReadTimestamp = getLastReadTimestamp(roomId);
	$: activeTypingUsers = getActiveTypingUsers(roomId);
	$: typingIndicatorText = formatTypingIndicator(activeTypingUsers);
	$: activeRoomCreatedAtMs = roomId ? (roomMetaById[roomId]?.createdAt ?? 0) : 0;
	$: activeRoomExpiresAtMs = roomId ? (roomMetaById[roomId]?.expiresAt ?? 0) : 0;
	$: activeRoomRemainingMs =
		activeRoomExpiresAtMs > 0
			? activeRoomExpiresAtMs - getApproxServerNowMs(roomExpiryTickMs)
			: Number.POSITIVE_INFINITY;
	$: isRoomExpired = activeRoomExpiresAtMs > 0 && activeRoomRemainingMs <= 0;
	$: activeRemainingLabel = getRemainingHoursLabelState(
		roomMetaById,
		roomId,
		roomExpiryTickMs,
		getApproxServerNowMs
	);
	$: isLoadingOlderHistory = historyLoadingByRoom[roomId] ?? false;
	$: hasMoreOlderHistory = historyHasMoreByRoom[roomId] ?? true;
	$: myRooms = filterThreadsByStatus(roomThreads, 'joined');
	$: discoverableRooms = filterThreadsByStatus(roomThreads, 'discoverable');
	$: leftRooms = filterThreadsByStatus(roomThreads, 'left');
	$: filteredMyRooms = filterThreadList(myRooms, chatListSearch, messagesByRoom, roomId);
	$: filteredDiscoverableRooms = filterThreadList(
		discoverableRooms,
		chatListSearch,
		messagesByRoom,
		roomId
	);
	$: filteredLeftRooms = filterThreadList(leftRooms, chatListSearch, messagesByRoom, roomId);

	$: if (roomId) {
		const existingRoom = roomThreads.find((thread) => thread.id === roomId);
		if (existingRoom) {
			ensureRoomThread(roomId, roomNameFromURL || undefined, existingRoom.status);
			ensureOnlineSeed(roomId);
		}
	}
	$: if (browser && identityReady && roomId && isMember) {
		void syncRoomMembership(roomId);
	}
	$: if (browser && identityReady && roomId && roomId !== lastRoomMetaSyncRoomId) {
		lastRoomMetaSyncRoomId = roomId;
		void refreshRoomMetaFromServer(roomId);
	}
	$: if (browser && identityReady) {
		initGlobalSocket(currentUserId, currentUsername);
	}
	$: if (browser && identityReady && $globalMessages) {
		const payload = $globalMessages.payload;
		let handledDirectPayload = false;
		if (payload && typeof payload === 'object') {
			const source = payload as Record<string, unknown>;
			const payloadType = toStringValue(source.type).toLowerCase();
			const payloadRoomID = normalizeRoomIDValue(toStringValue(source.roomId ?? source.room_id));
			if (payloadType === 'text' && payloadRoomID) {
				void (async () => {
					const directMessage = await parseIncomingMessageWithE2EE(source, payloadRoomID);
					if (directMessage) {
						addIncomingMessage(directMessage);
					}
				})();
				handledDirectPayload = true;
			}
		}
		if (!handledDirectPayload) {
			void handleGlobalPayload(payload);
		}
	}
	$: if (browser && identityReady) {
		// Subscribe to all rooms visible in sidebar so discoverable rooms get read-only previews.
		const readableRoomIDs = roomThreads
			.filter((thread) => thread.status !== 'left')
			.map((thread) => thread.id);
		if (roomId && isMember && !readableRoomIDs.includes(roomId)) {
			readableRoomIDs.push(roomId);
		}
		subscribeToRooms(readableRoomIDs);
	}
	$: if (browser && trustedCachingEnabled && roomId && !offlineHydratedByRoom[roomId]) {
		void hydrateOfflineCache(roomId);
	}
	$: if (browser && roomId && roomId !== lastToastRoom) {
		showJoinToast(roomId);
	}
	$: if (roomId && focusRoomTracker !== roomId) {
		focusRoomTracker = roomId;
		focusConsumedForRoom = false;
		focusMessageId = '';
		activeReply = null;
		isDiscussionOpen = false;
		activeDiscussionTaskId = '';
		discussionComments = [];
		messageActionMode = 'none';
		isSelectionMode = false;
		selectedActionMessageId = '';
		isCanvasFullscreen = false;
	}
	$: if (isDiscussionOpen && !activeDiscussionTask) {
		isDiscussionOpen = false;
		discussionComments = [];
	}
	$: if (!focusConsumedForRoom && focusMessageIdFromURL) {
		focusMessageId = focusMessageIdFromURL;
		focusConsumedForRoom = true;
	}
	$: roomActionSubmitDisabled =
		uiDialog.kind === 'roomAction' ? normalizeRoomNameValue(uiDialog.roomName) === '' : false;
	$: promptSubmitDisabled =
		uiDialog.kind === 'prompt' ? !uiDialog.allowEmptySubmit && uiDialog.value.trim() === '' : false;

	onDestroy(() => {
		clientLog('component-destroy', { roomId });
		typingController.destroy();
		clearAllCachePersistTimers();
		clearSidebarRefreshTimer();
		clearRoomExpiryTicker();
		clearToastTimer();
	});

	onMount(() => {
		if (!browser) {
			return;
		}
		syncActiveRoomPasswordFromHash();
		initializeTrustedDevicePreference();
		if (trustedCachingEnabled && roomId) {
			void hydrateOfflineCache(roomId);
		}
		void initializeIdentity();
		updateViewportMode();
		window.addEventListener('resize', updateViewportMode);
		clearRoomExpiryTicker();
		roomExpiryTickMs = Date.now();
		roomExpiryTicker = setInterval(() => {
			roomExpiryTickMs = Date.now();
			if (identityReady && roomId) {
				void refreshRoomMetaFromServer(roomId);
			}
			processKnownExpiredRooms();
		}, 60000);
		return () => {
			window.removeEventListener('resize', updateViewportMode);
			clearRoomExpiryTicker();
			if (removeSystemThemeListener) {
				removeSystemThemeListener();
				removeSystemThemeListener = null;
			}
		};
	});

	function updateViewportMode() {
		if (!browser) {
			return;
		}
		isMobileView = window.innerWidth <= 900;
		if (!isMobileView) {
			mobilePane = 'chat';
		}
	}

	function syncActiveRoomPasswordFromHash() {
		if (!browser || typeof window === 'undefined') {
			return;
		}
		const pathname = window.location.pathname;
		const hash = window.location.hash || '';
		if (!pathname.startsWith('/chat/')) {
			return;
		}
		if (hash.startsWith('#key=')) {
			let decoded = '';
			try {
				decoded = decodeURIComponent(hash.slice(5));
			} catch {
				decoded = hash.slice(5);
			}
			const key = normalizeRoomPasswordValue(decoded);
			activeRoomPassword.set(key);
			skipPasswordResetForPath = pathname;
			window.history.replaceState(null, '', window.location.pathname + window.location.search);
			return;
		}
		if (skipPasswordResetForPath && skipPasswordResetForPath === pathname) {
			skipPasswordResetForPath = '';
			return;
		}
		activeRoomPassword.set('');
	}

	function removeLegacyRoomTimeQueryParams(params: URLSearchParams) {
		let changed = false;
		for (const key of LEGACY_ROOM_TIME_QUERY_KEYS) {
			if (!params.has(key)) {
				continue;
			}
			params.delete(key);
			changed = true;
		}
		return changed;
	}

	async function encryptMessageContent(content: string) {
		return encryptText(content, normalizeRoomPasswordValue($activeRoomPassword));
	}

	async function decryptMessageContent(content: string) {
		return decryptText(content, normalizeRoomPasswordValue($activeRoomPassword));
	}

	async function decryptChatMessage(message: ChatMessage): Promise<ChatMessage> {
		if (!message.content) {
			return message;
		}
		const decryptedContent = await decryptMessageContent(message.content);
		if (decryptedContent === message.content) {
			return message;
		}
		return {
			...message,
			content: decryptedContent
		};
	}

	async function parseIncomingMessageWithE2EE(
		value: unknown,
		fallbackRoomId: string
	): Promise<ChatMessage | null> {
		const parsed = parseIncomingMessage(value, fallbackRoomId, API_BASE);
		if (!parsed) {
			return null;
		}
		return decryptChatMessage(parsed);
	}

	async function parseIncomingMessagesWithE2EE(
		values: unknown[],
		fallbackRoomId: string
	): Promise<ChatMessage[]> {
		const parsed = await Promise.all(
			values.map((entry) => parseIncomingMessageWithE2EE(entry, fallbackRoomId))
		);
		return parsed.filter((entry): entry is ChatMessage => Boolean(entry));
	}

	function sendTypingStop() {
		typingController.sendTypingStop();
	}

	function onComposerTyping(event: CustomEvent<{ value: string }>) {
		typingController.onComposerTyping((event.detail?.value || '').trim());
	}

	function setTypingIndicator(
		targetRoomId: string,
		userId: string,
		userName: string,
		expiresAt: number = Date.now() + TYPING_SAFETY_TIMEOUT_MS
	) {
		typingController.setTypingIndicator(targetRoomId, userId, userName, expiresAt);
	}

	function clearTypingIndicator(targetRoomId: string, userId: string) {
		typingController.clearTypingIndicator(targetRoomId, userId);
	}

	function getActiveTypingUsers(targetRoomId: string) {
		return typingController.getActiveTypingUsers(targetRoomId, currentUserId);
	}

	function formatTypingIndicator(names: string[]) {
		if (!names || names.length === 0) {
			return '';
		}
		if (names.length === 1) {
			return `${names[0]} is typing...`;
		}
		if (names.length === 2) {
			return `${names[0]} and ${names[1]} are typing...`;
		}
		return `${names[0]} and ${names.length - 1} others are typing...`;
	}

	function initializeTrustedDevicePreference() {
		const preference = getTrustedDevicePreference();
		trustedDevicePreference = preference;
		showTrustedDevicePrompt = preference === 'unset';
		trustedCachingEnabled = preference === 'yes' && isOfflineCacheSupported();
	}

	function onTrustedDeviceChoice(choice: 'yes' | 'no') {
		setTrustedDevicePreference(choice);
		trustedDevicePreference = choice;
		showTrustedDevicePrompt = false;
		trustedCachingEnabled = choice === 'yes' && isOfflineCacheSupported();
		if (trustedCachingEnabled && roomId) {
			void hydrateOfflineCache(roomId);
		}
	}

	function clearAllCachePersistTimers() {
		for (const timer of cachePersistTimers.values()) {
			clearTimeout(timer);
		}
		cachePersistTimers = new Map<string, ReturnType<typeof setTimeout>>();
	}

	function queueOfflineCachePersist(targetRoomId: string) {
		if (!browser || !trustedCachingEnabled || !targetRoomId) {
			return;
		}
		const existing = cachePersistTimers.get(targetRoomId);
		if (existing) {
			clearTimeout(existing);
		}
		const timer = setTimeout(() => {
			void persistOfflineCache(targetRoomId);
		}, 350);
		cachePersistTimers.set(targetRoomId, timer);
	}

	async function persistOfflineCache(targetRoomId: string) {
		if (!browser || !trustedCachingEnabled || !targetRoomId) {
			return;
		}
		cachePersistTimers.delete(targetRoomId);
		const token = getSessionToken() || ($authToken ?? '');
		if (!token) {
			return;
		}
		const payload = (messagesByRoom[targetRoomId] ?? []).slice(-50);
		await saveEncryptedRoomMessages(targetRoomId, payload, token);
	}

	async function hydrateOfflineCache(targetRoomId: string) {
		if (
			!browser ||
			!trustedCachingEnabled ||
			!targetRoomId ||
			offlineHydratedByRoom[targetRoomId]
		) {
			return;
		}
		offlineHydratedByRoom = {
			...offlineHydratedByRoom,
			[targetRoomId]: true
		};
		const token = getSessionToken() || ($authToken ?? '');
		if (!token) {
			return;
		}
		const cached = await loadEncryptedRoomMessages(targetRoomId, token);
		if (!Array.isArray(cached) || cached.length === 0) {
			return;
		}
		const hydrated = await parseIncomingMessagesWithE2EE(cached, targetRoomId);
		if (hydrated.length === 0) {
			return;
		}
		mergeMessages(targetRoomId, hydrated);
	}

	async function requestAnonymousSession(requestedUsername: string) {
		const normalizedRequested =
			normalizeUsernameValue(requestedUsername) ||
			normalizeUsernameValue(generateUsername()) ||
			'Guest';
		try {
			const res = await fetch(`${API_BASE}/api/auth/anonymous`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ username: normalizedRequested })
			});
			const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
			if (!res.ok) {
				clientLog('api-auth-anonymous-failed', { status: res.status, data });
				return null;
			}

			const user = (data.user as Record<string, unknown> | undefined) ?? {};
			const token = toStringValue(data.token).trim();
			const username = normalizeUsernameValue(toStringValue(user.username)) || normalizedRequested;
			if (!token) {
				return null;
			}
			return { token, username };
		} catch (error) {
			clientLog('api-auth-anonymous-error', {
				error: error instanceof Error ? error.message : String(error)
			});
			return null;
		}
	}

	async function silentlyJoinRoomAsMember(targetRoomId: string, userId: string, username: string) {
		const normalizedRoomId = normalizeRoomIDValue(targetRoomId);
		const normalizedUserId = normalizeIdentifier(userId);
		const normalizedUsername =
			normalizeUsernameValue(username) || normalizeUsernameValue(generateUsername()) || 'Guest';
		if (!normalizedRoomId || !normalizedUserId) {
			return;
		}

		try {
			const res = await fetch(`${API_BASE}/api/rooms/join`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					roomId: normalizedRoomId,
					username: normalizedUsername,
					userId: normalizedUserId,
					mode: 'join'
				})
			});
			const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
			if (!res.ok) {
				clientLog('api-room-anonymous-join-failed', {
					roomId: normalizedRoomId,
					status: res.status,
					data
				});
				return;
			}

			syncServerClock(data.serverNow ?? data.server_now);
			const joinedRoomId = normalizeRoomIDValue(toStringValue(data.roomId)) || normalizedRoomId;
			const joinedName =
				normalizeRoomNameValue(toStringValue(data.roomName)) ||
				roomNameFromURL ||
				formatRoomName(joinedRoomId);
			const joinedCreatedAt = toTimestamp(data.createdAt);
			const joinedExpiresAt = parseOptionalTimestamp(data.expiresAt ?? data.expires_at);
			const joinedIsAdmin = toBool(data.isAdmin ?? data.is_admin);
			const joinedAdminCode = normalizeAdminCodeValue(data.adminCode ?? data.admin_code);
			const joinedRequiresPassword = toBool(
				data.requiresPassword ?? data.requires_password ?? false
			);

			ensureRoomThread(joinedRoomId, joinedName, 'joined');
			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === joinedRoomId
						? {
								...thread,
								status: 'joined',
								name: joinedName,
								isAdmin: joinedIsAdmin,
								adminCode: joinedIsAdmin ? joinedAdminCode : '',
								requiresPassword: joinedRequiresPassword
							}
						: thread
				)
			);
			markRoomMembershipSynced(joinedRoomId);
			ensureRoomMeta(joinedRoomId, joinedCreatedAt, joinedExpiresAt);
			ensureOnlineSeed(joinedRoomId);

			const params = new URLSearchParams($page.url.searchParams.toString());
			removeLegacyRoomTimeQueryParams(params);
			params.set('member', '1');
			params.set('name', joinedName);
			await goto(`/chat/${encodeURIComponent(joinedRoomId)}?${params.toString()}`, {
				replaceState: true,
				noScroll: true,
				keepFocus: true
			});
		} catch (error) {
			clientLog('api-room-anonymous-join-error', {
				roomId: normalizedRoomId,
				error: error instanceof Error ? error.message : String(error)
			});
		}
	}

	async function initializeIdentity() {
		const identity = getOrInitIdentity();
		let resolvedUserId = normalizeIdentifier(identity.id) || identity.id;
		let resolvedUsername =
			normalizeUsernameValue(identity.username) ||
			normalizeUsernameValue(generateUsername()) ||
			'Guest';
		let token = getSessionToken() || ($authToken ?? '');
		let joinedFromAnonymousSession = false;

		if (!token) {
			const anonymousSession = await requestAnonymousSession(resolvedUsername);
			if (anonymousSession) {
				token = anonymousSession.token;
				resolvedUsername = anonymousSession.username;
				setSessionToken(token);
				authToken.set(token);
				joinedFromAnonymousSession = true;
			}
		} else if (!$authToken) {
			authToken.set(token);
		}

		currentUser.set({
			id: normalizeIdentifier(resolvedUserId) || resolvedUserId,
			username: normalizeUsernameValue(resolvedUsername) || resolvedUsername
		});
		identityReady = true;
		clientLog('identity-initialized', {
			id: resolvedUserId,
			username: resolvedUsername,
			joinedFromAnonymousSession
		});

		if (joinedFromAnonymousSession && roomId) {
			await silentlyJoinRoomAsMember(roomId, resolvedUserId, resolvedUsername);
		}

		await refreshSidebarRooms(normalizeIdentifier(resolvedUserId) || resolvedUserId);
		clearSidebarRefreshTimer();
		// sidebarRefreshTimer = setInterval(() => {
		// 	void refreshSidebarRooms();
		// }, 15000);
		// increases server load and can cause jank, so leaving out for now
	}

	function clientLog(event: string, payload?: unknown) {
		if (!CLIENT_DEBUG) {
			return;
		}
		const timestamp = new Date().toISOString();
		if (payload === undefined) {
			console.log(`${CLIENT_LOG_PREFIX} ${timestamp} ${event}`);
			return;
		}
		console.log(`${CLIENT_LOG_PREFIX} ${timestamp} ${event}`, payload);
	}

	function clearSidebarRefreshTimer() {
		if (sidebarRefreshTimer) {
			clearInterval(sidebarRefreshTimer);
			sidebarRefreshTimer = null;
		}
	}

	function clearRoomExpiryTicker() {
		if (roomExpiryTicker) {
			clearInterval(roomExpiryTicker);
			roomExpiryTicker = null;
		}
	}

	function clearToastTimer() {
		if (toastTimer) {
			clearTimeout(toastTimer);
			toastTimer = null;
		}
	}

	function showJoinToast(activeRoomId: string) {
		lastToastRoom = activeRoomId;
		const activeName =
			roomThreads.find((thread) => thread.id === activeRoomId)?.name || roomNameFromURL || 'Room';
		toastMessage = `Joined Room: ${activeName}`;
		showToast = true;
		clearToastTimer();
		toastTimer = setTimeout(() => {
			showToast = false;
		}, 3000);
	}

	function showErrorToast(message: string) {
		toastMessage = message;
		showToast = true;
		clearToastTimer();
		toastTimer = setTimeout(() => {
			showToast = false;
		}, 3000);
	}

	async function openOptionalRoomPasswordDialog(initialValue = '') {
		const rawValue = await openPromptDialog({
			title: 'Room Password (E2EE)',
			message:
				'Optional. Encrypts all messages and board data. The server cannot read protected rooms.',
			initialValue: normalizeRoomPasswordValue(initialValue),
			placeholder: 'Optional password',
			maxLength: 32,
			confirmLabel: 'Continue',
			emptyConfirmLabel: 'Skip',
			cancelLabel: 'Cancel',
			multiline: false,
			allowEmptySubmit: true
		});
		if (rawValue === null) {
			return null;
		}
		return normalizeRoomPasswordValue(rawValue);
	}

	async function openRoomAccessPasswordDialog(initialValue = '') {
		const rawValue = await openPromptDialog({
			title: 'Room Access Password',
			message: 'This break room is protected. Enter the room password to join.',
			initialValue: normalizeRoomAccessPasswordValue(initialValue),
			placeholder: 'Room password',
			maxLength: 64,
			confirmLabel: 'Join',
			cancelLabel: 'Cancel',
			multiline: false
		});
		if (rawValue === null) {
			return null;
		}
		return normalizeRoomAccessPasswordValue(rawValue);
	}

	function setMessageActionMode(mode: MessageActionMode) {
		messageActionMode = mode;
		isSelectionMode = mode !== 'none';
		deleteMultiEnabled = mode === 'delete';
		selectedDeleteMessageIds = [];
		if (mode === 'none') {
			selectedActionMessageId = '';
		}
	}

	function cancelSelectionMode() {
		setMessageActionMode('none');
		selectedActionMessageId = '';
		selectedDeleteMessageIds = [];
	}

	async function deleteSelectedMessagesBatch() {
		if (!roomId || selectedDeleteMessageIds.length === 0) {
			return;
		}
		const uniqueMessageIds = Array.from(
			new Set(
				selectedDeleteMessageIds
					.map((value) => normalizeMessageID(value))
					.filter((value) => value !== '')
			)
		);
		if (uniqueMessageIds.length === 0) {
			selectedDeleteMessageIds = [];
			return;
		}

		const confirmed = await openConfirmDialog({
			title: 'Delete Selected Messages',
			message: `Delete ${uniqueMessageIds.length} selected message${
				uniqueMessageIds.length === 1 ? '' : 's'
			}? This action cannot be undone.`,
			confirmLabel: 'Delete',
			cancelLabel: 'Cancel',
			danger: true
		});
		if (!confirmed) {
			return;
		}

		const editedAt = Date.now();
		for (const messageId of uniqueMessageIds) {
			applyMessageDelete(roomId, {
				messageId,
				editedAt
			});
			sendSocketPayload({
				type: 'message_delete',
				roomId,
				messageId
			});
		}
		selectedDeleteMessageIds = [];
		selectedActionMessageId = '';
	}

	function syncServerClock(rawServerNow: unknown) {
		const parsed = parseOptionalTimestamp(rawServerNow);
		if (!parsed || parsed <= 0) {
			return;
		}
		serverClockOffsetMs = parsed - Date.now();
		if (browser && typeof performance !== 'undefined') {
			serverNowAnchorMs = parsed;
			serverNowAnchorPerfMs = performance.now();
		}
	}

	function getApproxServerNowMs(tickMs?: number) {
		// Keep `tickMs` as an optional input so callers can create a reactive dependency on the minute ticker.
		void tickMs;
		if (
			browser &&
			serverNowAnchorMs > 0 &&
			serverNowAnchorPerfMs > 0 &&
			typeof performance !== 'undefined'
		) {
			const elapsedMs = Math.max(0, performance.now() - serverNowAnchorPerfMs);
			return serverNowAnchorMs + elapsedMs;
		}
		return Date.now() + serverClockOffsetMs;
	}

	function createThread(
		id: string,
		nameOverride?: string,
		status: ThreadStatus = 'joined'
	): ChatThread {
		return createThreadState(id, formatRoomName, nameOverride, status);
	}

	function ensureRoomThread(
		targetRoomId: string,
		nameOverride?: string,
		status: ThreadStatus = 'joined'
	) {
		roomThreads = ensureRoomThreadState(
			roomThreads,
			targetRoomId,
			{ createThread },
			nameOverride,
			status
		);
	}

	function ensureRoomMeta(targetRoomId: string, createdAt: number, expiresAt = 0) {
		roomMetaById = ensureRoomMetaState(roomMetaById, targetRoomId, createdAt, expiresAt);
	}

	function ensureOnlineSeed(targetRoomId: string) {
		onlineByRoom = ensureOnlineSeedState(
			onlineByRoom,
			targetRoomId,
			currentUserId,
			currentUsername
		);
	}

	function updateThreadPreview(targetRoomId: string) {
		roomThreads = updateThreadPreviewState(roomThreads, messagesByRoom, targetRoomId, {
			formatRoomName,
			getMessagePreviewText,
			createThread
		});
	}

	function markRoomMembershipSynced(targetRoomId: string) {
		const normalizedRoomId = normalizeRoomIDValue(targetRoomId);
		if (!normalizedRoomId) {
			return;
		}
		roomMembershipSynced = {
			...roomMembershipSynced,
			[normalizedRoomId]: true
		};
	}

	async function syncRoomMembership(targetRoomId: string) {
		const normalizedRoomId = normalizeRoomIDValue(targetRoomId);
		if (!browser || !normalizedRoomId || !isMember) {
			return;
		}
		if (roomMembershipSynced[normalizedRoomId] || roomMembershipSyncing[normalizedRoomId]) {
			return;
		}

		roomMembershipSyncing = {
			...roomMembershipSyncing,
			[normalizedRoomId]: true
		};

		try {
			const payload = {
				roomId: normalizedRoomId,
				username: currentUsername,
				userId: normalizeIdentifier(currentUserId),
				mode: 'join'
			};
			clientLog('api-room-sync-request', payload);
			const res = await fetch(`${API_BASE}/api/rooms/join`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(payload)
			});
			const data = await res.json().catch(() => ({}));
			if (!res.ok) {
				clientLog('api-room-sync-failed', { roomId: normalizedRoomId, status: res.status, data });
				return;
			}
			syncServerClock(
				(data as { serverNow?: unknown; server_now?: unknown }).serverNow ??
					(data as { serverNow?: unknown; server_now?: unknown }).server_now
			);

			markRoomMembershipSynced(normalizedRoomId);
			const joinedName =
				normalizeRoomNameValue(toStringValue(data.roomName)) || formatRoomName(normalizedRoomId);
			const joinedCreatedAt = toTimestamp(data.createdAt);
			const joinedExpiresAt = parseOptionalTimestamp(data.expiresAt ?? data.expires_at);
			const joinedIsAdmin = toBool(
				(data as { isAdmin?: unknown; is_admin?: unknown }).isAdmin ??
					(data as { isAdmin?: unknown; is_admin?: unknown }).is_admin
			);
			const joinedAdminCode = normalizeAdminCodeValue(
				(data as { adminCode?: unknown; admin_code?: unknown }).adminCode ??
					(data as { adminCode?: unknown; admin_code?: unknown }).admin_code
			);
			const joinedRequiresPassword = toBool(
				(data as { requiresPassword?: unknown; requires_password?: unknown }).requiresPassword ??
					(data as { requiresPassword?: unknown; requires_password?: unknown }).requires_password
			);
			ensureRoomThread(normalizedRoomId, joinedName, 'joined');
			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === normalizedRoomId
						? {
								...thread,
								isAdmin: joinedIsAdmin,
								adminCode: joinedIsAdmin ? joinedAdminCode : '',
								requiresPassword: joinedRequiresPassword
							}
						: thread
				)
			);
			ensureRoomMeta(normalizedRoomId, joinedCreatedAt, joinedExpiresAt);
			await refreshSidebarRooms();
		} catch (error) {
			clientLog('api-room-sync-error', {
				roomId: normalizedRoomId,
				error: error instanceof Error ? error.message : String(error)
			});
		} finally {
			const nextSyncing = { ...roomMembershipSyncing };
			delete nextSyncing[normalizedRoomId];
			roomMembershipSyncing = nextSyncing;
		}
	}

	let lastRoomMetaSyncRoomId = '';
	async function refreshRoomMetaFromServer(targetRoomId: string) {
		const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
		const normalizedUserID = normalizeIdentifier(currentUserId);
		if (!browser || !identityReady || !normalizedRoomID || !normalizedUserID) {
			return;
		}
		try {
			const res = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomID)}?userId=${encodeURIComponent(normalizedUserID)}`
			);
			const data = await res.json().catch(() => ({}));
			if (!res.ok) {
				return;
			}
			syncServerClock(
				(data as { serverNow?: unknown; server_now?: unknown }).serverNow ??
					(data as { serverNow?: unknown; server_now?: unknown }).server_now
			);
			const createdAt = toTimestamp((data as { createdAt?: unknown }).createdAt);
			const expiresAt = parseOptionalTimestamp(
				(data as { expiresAt?: unknown; expires_at?: unknown }).expiresAt ??
					(data as { expiresAt?: unknown; expires_at?: unknown }).expires_at
			);
			if (createdAt > 0 || expiresAt > 0) {
				ensureRoomMeta(normalizedRoomID, createdAt, expiresAt);
			}
		} catch (error) {
			clientLog('api-room-details-error', {
				roomId: normalizedRoomID,
				error: error instanceof Error ? error.message : String(error)
			});
		}
	}

	async function refreshSidebarRooms(userIdOverride?: string) {
		const userID = normalizeIdentifier(userIdOverride || currentUserId);
		if (!browser || !userID) {
			return;
		}

		try {
			clientLog('api-sidebar-request', { userID });
			const res = await fetch(`${API_BASE}/api/rooms/sidebar?userId=${encodeURIComponent(userID)}`);
			const data = await res.json().catch(() => ({ rooms: [] }));
			if (!res.ok) {
				clientLog('api-sidebar-failed', { status: res.status, data });
				return;
			}
			syncServerClock(
				(data as { serverNow?: unknown; server_now?: unknown }).serverNow ??
					(data as { serverNow?: unknown; server_now?: unknown }).server_now
			);
			const incoming = Array.isArray(data.rooms) ? (data.rooms as SidebarRoom[]) : [];
			const existing = new Map(roomThreads.map((thread) => [thread.id, thread]));
			const nextThreads = incoming.reduce<ChatThread[]>((acc, room) => {
				const roomID = normalizeRoomIDValue(room.roomId);
				if (!roomID) {
					return acc;
				}

				const prev = existing.get(roomID);
				const roomRecord = room as unknown as Record<string, unknown>;
				const createdAt = normalizeEpoch(Number(room.createdAt ?? 0));
				const expiresAt = parseOptionalTimestamp(room.expiresAt);
				if (createdAt > 0 || expiresAt > 0) {
					ensureRoomMeta(roomID, createdAt, expiresAt);
				}

				const roomStatus: ThreadStatus =
					room.status === 'joined' ? 'joined' : room.status === 'left' ? 'left' : 'discoverable';
				const nextIsAdmin = toBool(room.isAdmin ?? prev?.isAdmin ?? false);
				const nextAdminCode = normalizeAdminCodeValue(
					room.adminCode ?? (nextIsAdmin ? prev?.adminCode : '')
				);
				const nextRequiresPassword = toBool(
					roomRecord.requiresPassword ??
						roomRecord.requires_password ??
						prev?.requiresPassword ??
						false
				);
				const shouldMaskPreview = roomStatus !== 'joined' && nextRequiresPassword;

				const next: ChatThread = {
					id: roomID,
					name:
						normalizeRoomNameValue(toStringValue(room.roomName)) ||
						prev?.name ||
						formatRoomName(roomID),
					lastMessage: shouldMaskPreview ? PROTECTED_ROOM_PREVIEW_TEXT : prev?.lastMessage || '',
					lastActivity: prev?.lastActivity || createdAt || Date.now(),
					unread: prev?.unread || 0,
					status: roomStatus,
					memberCount: typeof room.memberCount === 'number' ? room.memberCount : prev?.memberCount,
					parentRoomId: toStringValue(room.parentRoomId) || prev?.parentRoomId || undefined,
					originMessageId:
						toStringValue(room.originMessageId) || prev?.originMessageId || undefined,
					treeNumber: toInt(room.treeNumber ?? prev?.treeNumber ?? 0),
					isAdmin: nextIsAdmin,
					adminCode: nextIsAdmin ? nextAdminCode : '',
					requiresPassword: nextRequiresPassword
				};

				acc.push(next);
				return acc;
			}, []);

			const previousById = new Map(roomThreads.map((thread) => [thread.id, thread]));
			const merged = new Map<string, ChatThread>();
			for (const nextThread of nextThreads) {
				const prev = previousById.get(nextThread.id);
				merged.set(nextThread.id, {
					...prev,
					...nextThread,
					unread: prev?.unread ?? nextThread.unread,
					lastMessage: nextThread.lastMessage || prev?.lastMessage || '',
					lastActivity: Math.max(nextThread.lastActivity, prev?.lastActivity ?? 0),
					status: nextThread.status
				});
			}

			roomThreads = sortThreads([...merged.values()]);
			processKnownExpiredRooms();
		} catch (error) {
			clientLog('api-sidebar-error', {
				error: error instanceof Error ? error.message : String(error)
			});
		}
	}

	function onSidebarSelect(
		event: CustomEvent<{ id: string; isMember: boolean; status: ThreadStatus }>
	) {
		const targetRoomId = normalizeRoomIDValue(event.detail.id);
		if (!targetRoomId) {
			return;
		}
		if (event.detail.status === 'left') {
			showErrorToast('You left this room. Open one of its child rooms.');
			return;
		}
		selectRoom(targetRoomId, event.detail.isMember);
	}

	function selectRoom(targetRoomId: string, memberState: boolean, focusMsgID = '') {
		const normalizedTargetRoomId = normalizeRoomIDValue(targetRoomId);
		if (!normalizedTargetRoomId) {
			return;
		}
		sendTypingStop();
		if (normalizedTargetRoomId === roomId) {
			if (isMobileView) {
				mobilePane = 'chat';
			}
			const normalizedFocus = normalizeMessageID(focusMsgID);
			if (normalizedFocus) {
				focusMessageId = normalizedFocus;
				focusConsumedForRoom = true;
			} else {
				focusMessageId = '';
				focusConsumedForRoom = true;
			}
			return;
		}
		clientLog('select-room', { fromRoom: roomId, toRoom: normalizedTargetRoomId, memberState });
		showLeftMenu = false;
		showRoomSearch = false;
		showRoomDetails = false;
		setMessageActionMode('none');
		roomMessageSearch = '';
		if (isMobileView) {
			mobilePane = 'chat';
		}

		const selected = roomThreads.find((thread) => thread.id === normalizedTargetRoomId);
		const params = new URLSearchParams();
		if (selected?.name) {
			params.set('name', selected.name);
		}
		if (memberState) {
			params.set('member', '1');
		} else {
			params.set('member', '0');
		}
		const normalizedFocus = normalizeMessageID(focusMsgID);
		if (normalizedFocus) {
			params.set('focusMsg', normalizedFocus);
			focusMessageId = normalizedFocus;
			focusConsumedForRoom = false;
		} else {
			focusMessageId = '';
			focusConsumedForRoom = true;
		}

		const query = params.toString();
		void goto(`/chat/${encodeURIComponent(normalizedTargetRoomId)}${query ? `?${query}` : ''}`);
	}

	function showMobileRoomList() {
		if (!isMobileView) {
			return;
		}
		showRoomSearch = false;
		showRoomDetails = false;
		setMessageActionMode('none');
		mobilePane = 'list';
	}

	function toggleBoardView() {
		lastWorkspaceTool = 'board';
		showBoardView = !showBoardView;
		if (showBoardView) {
			setMessageActionMode('none');
			showRoomSearch = false;
			activeReply = null;
		}
	}

	function toggleCanvas() {
		lastWorkspaceTool = 'canvas';
		if (isCanvasOpen) {
			isCanvasOpen = false;
			isCanvasFullscreen = false;
			return;
		}
		isCanvasOpen = true;
		isCanvasFullscreen = false;
	}

	function toggleCanvasFullscreen() {
		lastWorkspaceTool = 'canvas';
		if (!isCanvasOpen) {
			isCanvasOpen = true;
		}
		isCanvasFullscreen = !isCanvasFullscreen;
	}

	function activateLastWorkspaceTool() {
		if (lastWorkspaceTool === 'canvas') {
			toggleCanvas();
			return;
		}
		toggleBoardView();
	}

	function exitCanvasFullscreen() {
		isCanvasFullscreen = false;
	}

	function onJumpToBreakOrigin(
		event: CustomEvent<{
			parentRoomId: string;
			originMessageId: string;
			fallbackRoomId: string;
			fallbackIsMember: boolean;
		}>
	) {
		const parentRoomID = normalizeRoomIDValue(event.detail.parentRoomId);
		const originMessageID = normalizeMessageID(event.detail.originMessageId);
		if (!parentRoomID || !originMessageID) {
			selectRoom(event.detail.fallbackRoomId, event.detail.fallbackIsMember);
			return;
		}

		const parentThread = roomThreads.find((thread) => thread.id === parentRoomID);
		const parentMemberState = parentThread ? parentThread.status === 'joined' : true;
		ensureRoomThread(
			parentRoomID,
			parentThread?.name || formatRoomName(parentRoomID),
			parentMemberState ? 'joined' : 'discoverable'
		);
		selectRoom(parentRoomID, parentMemberState, originMessageID);
	}

	function onFocusHandled(event: CustomEvent<{ messageId: string }>) {
		if (normalizeMessageID(event.detail.messageId) === focusMessageId) {
			focusMessageId = '';
			focusConsumedForRoom = true;
		}
	}

	async function handleGlobalPayload(payload: unknown) {
		if (Array.isArray(payload)) {
			const parsedMessages = await parseIncomingMessagesWithE2EE(payload, '');
			if (parsedMessages.length === 0) {
				return;
			}

			const byRoom = new Map<string, ChatMessage[]>();
			for (const message of parsedMessages) {
				const roomBucket = byRoom.get(message.roomId) ?? [];
				roomBucket.push(message);
				byRoom.set(message.roomId, roomBucket);
			}
			for (const [targetRoomId, history] of byRoom.entries()) {
				mergeMessages(targetRoomId, history);
			}
			return;
		}

		if (isEnvelope(payload)) {
			await handleEnvelope(payload);
			return;
		}

		const single = await parseIncomingMessageWithE2EE(payload, '');
		if (single) {
			addIncomingMessage(single);
		}
	}

	async function handleDiscussionCommentEnvelope(envelope: SocketEnvelope, targetRoomId: string) {
		const targetRoomID = normalizeRoomIDValue(targetRoomId);
		const activeRoomID = normalizeRoomIDValue(roomId);
		if (!targetRoomID || targetRoomID !== activeRoomID) {
			return;
		}
		const pinMessageID = resolveDiscussionPinMessageID(envelope);
		const activeTaskID = normalizeMessageID(activeDiscussionTaskId);
		if (!pinMessageID || !activeTaskID || pinMessageID !== activeTaskID) {
			return;
		}
		const comment = await parseIncomingMessageWithE2EE(envelope.payload, targetRoomID);
		if (!comment) {
			return;
		}
		upsertDiscussionCommentLocal(comment, pinMessageID);
	}

	function handleMessageBreakUpdatedEnvelope(envelope: SocketEnvelope, targetRoomId: string) {
		const source = envelope as Record<string, unknown>;
		const payload = resolveEnvelopePayloadRecord(envelope);
		const parentRoomID = normalizeRoomIDValue(
			toStringValue(
				payload.parentRoomId ??
					payload.parent_room_id ??
					source.parentRoomId ??
					source.parent_room_id ??
					targetRoomId
			)
		);
		const originMessageID = normalizeMessageID(
			toStringValue(
				payload.originMessageId ??
					payload.origin_message_id ??
					source.originMessageId ??
					source.origin_message_id
			)
		);
		const breakRoomID = normalizeRoomIDValue(
			toStringValue(
				payload.breakRoomId ?? payload.break_room_id ?? source.breakRoomId ?? source.break_room_id
			)
		);
		if (!parentRoomID || !originMessageID || !breakRoomID) {
			return;
		}

		const breakJoinCount = Math.max(
			0,
			toInt(
				payload.breakJoinCount ??
					payload.break_join_count ??
					source.breakJoinCount ??
					source.break_join_count
			)
		);
		const breakRoomName = normalizeRoomNameValue(
			toStringValue(
				payload.breakRoomName ??
					payload.break_room_name ??
					source.breakRoomName ??
					source.break_room_name
			)
		);
		const breakCreatedAt = parseOptionalTimestamp(
			payload.createdAt ??
				payload.created_at ??
				payload.breakCreatedAt ??
				payload.break_created_at ??
				source.createdAt ??
				source.created_at
		);
		const breakExpiresAt = parseOptionalTimestamp(
			payload.expiresAt ??
				payload.expires_at ??
				payload.breakExpiresAt ??
				payload.break_expires_at ??
				source.expiresAt ??
				source.expires_at
		);
		const breakRequiresPassword = toBool(
			payload.requiresPassword ??
				payload.requires_password ??
				source.requiresPassword ??
				source.requires_password
		);

		const roomMessages = messagesByRoom[parentRoomID] ?? [];
		let messageUpdated = false;
		const nextRoomMessages = roomMessages.map((entry) => {
			if (normalizeMessageID(entry.id) !== originMessageID) {
				return entry;
			}
			messageUpdated = true;
			return {
				...entry,
				hasBreakRoom: true,
				breakRoomId: breakRoomID,
				breakJoinCount: breakJoinCount > 0 ? breakJoinCount : (entry.breakJoinCount ?? 0),
				branchesCreated: Math.max(1, entry.branchesCreated ?? 0)
			};
		});
		if (messageUpdated) {
			messagesByRoom = {
				...messagesByRoom,
				[parentRoomID]: nextRoomMessages
			};
			queueOfflineCachePersist(parentRoomID);
		}

		const fallbackRoomName =
			breakRoomName ||
			roomThreads.find((thread) => thread.id === breakRoomID)?.name ||
			formatRoomName(breakRoomID);
		ensureRoomThread(breakRoomID, fallbackRoomName, 'discoverable');
		roomThreads = sortThreads(
			roomThreads.map((thread) => {
				if (thread.id !== breakRoomID) {
					return thread;
				}
				const nextStatus: ThreadStatus =
					thread.status === 'joined'
						? 'joined'
						: thread.status === 'left'
							? 'left'
							: 'discoverable';
				const nextRequiresPassword = breakRequiresPassword || Boolean(thread.requiresPassword);
				const shouldMaskPreview = nextStatus !== 'joined' && nextRequiresPassword;
				return {
					...thread,
					name: fallbackRoomName || thread.name,
					status: nextStatus,
					parentRoomId: parentRoomID || thread.parentRoomId,
					originMessageId: originMessageID || thread.originMessageId,
					requiresPassword: nextRequiresPassword,
					lastMessage: shouldMaskPreview ? PROTECTED_ROOM_PREVIEW_TEXT : thread.lastMessage || ''
				};
			})
		);

		if (breakCreatedAt > 0 || breakExpiresAt > 0) {
			ensureRoomMeta(breakRoomID, breakCreatedAt, breakExpiresAt);
		}
	}

	async function handleEnvelope(envelope: SocketEnvelope) {
		const targetRoomId = resolveEnvelopeRoomID(envelope);
		const kind = toStringValue(envelope.type).toLowerCase();
		const payload = resolveEnvelopePayloadRecord(envelope);
		if (kind === 'history' || kind === 'recent_messages' || kind === 'initial_messages') {
			if (Array.isArray(envelope.payload)) {
				const history = await parseIncomingMessagesWithE2EE(envelope.payload, targetRoomId);
				if (history.length > 0) {
					const grouped = new Map<string, ChatMessage[]>();
					for (const message of history) {
						const roomBucket = grouped.get(message.roomId) ?? [];
						roomBucket.push(message);
						grouped.set(message.roomId, roomBucket);
					}
					for (const [roomID, messages] of grouped.entries()) {
						mergeMessages(roomID, messages);
					}
				}
			}
			return;
		}

		if (kind === 'new_message') {
			const message = await parseIncomingMessageWithE2EE(envelope.payload, targetRoomId);
			if (message) {
				addIncomingMessage(message);
			}
			return;
		}

		if (kind === 'discussion_comment' && targetRoomId) {
			await handleDiscussionCommentEnvelope(envelope, targetRoomId);
			return;
		}

		if (kind === 'message_pin_updated' && targetRoomId) {
			applyMessagePinState(targetRoomId, payload);
			return;
		}

		if (kind === 'message_break_updated' && targetRoomId) {
			handleMessageBreakUpdatedEnvelope(envelope, targetRoomId);
			return;
		}

		if (kind === 'room_renamed' && targetRoomId) {
			const nextRoomName = normalizeRoomNameValue(
				toStringValue(
					payload.roomName ??
						payload.room_name ??
						(envelope as Record<string, unknown>).roomName ??
						(envelope as Record<string, unknown>).room_name
				)
			);
			if (!nextRoomName) {
				return;
			}
			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === targetRoomId ? { ...thread, name: nextRoomName } : thread
				)
			);
			return;
		}

		if (kind === 'room_extended' && targetRoomId) {
			syncServerClock(
				payload.serverNow ??
					payload.server_now ??
					(envelope as Record<string, unknown>).serverNow ??
					(envelope as Record<string, unknown>).server_now
			);
			const nextExpiresAt = parseOptionalTimestamp(
				payload.expiresAt ??
					payload.expires_at ??
					(envelope as Record<string, unknown>).expiresAt ??
					(envelope as Record<string, unknown>).expires_at
			);
			if (nextExpiresAt > 0) {
				ensureRoomMeta(targetRoomId, getRoomCreatedAt(targetRoomId), nextExpiresAt);
			}
			void refreshRoomMetaFromServer(targetRoomId);
			return;
		}

		if (kind === 'room_deleted' && targetRoomId) {
			const { removedCurrentRoom, removedNames } = removeRoomsFromLocalState([targetRoomId]);
			if (!removedCurrentRoom && removedNames.length === 0) {
				return;
			}
			setMessageActionMode('none');
			showRoomDetails = false;
			showRoomSearch = false;
			if (removedCurrentRoom) {
				activeReply = null;
				showErrorToast('Room deleted');
				void goto('/');
			} else if (removedNames.length > 0) {
				showErrorToast(`Room deleted: ${removedNames[0]}`);
			}
			return;
		}

		if (kind === 'member_removed' && targetRoomId) {
			const normalizedTargetUserID = resolveEnvelopeTargetUserID(envelope);
			if (!normalizedTargetUserID) {
				return;
			}
			removeOnlineMember(targetRoomId, normalizedTargetUserID);
			if (normalizedTargetUserID === normalizeIdentifier(currentUserId)) {
				setMessageActionMode('none');
				showRoomDetails = false;
				showRoomSearch = false;
				activeReply = null;
				showErrorToast('You were removed from this room');
				void goto('/');
				return;
			}

			const hasAuthoritativeMemberCount =
				payload.memberCount !== undefined ||
				payload.member_count !== undefined ||
				(envelope as Record<string, unknown>).memberCount !== undefined ||
				(envelope as Record<string, unknown>).member_count !== undefined;
			const authoritativeMemberCount = hasAuthoritativeMemberCount
				? toInt(
						payload.memberCount ??
							payload.member_count ??
							(envelope as Record<string, unknown>).memberCount ??
							(envelope as Record<string, unknown>).member_count
					)
				: -1;
			roomThreads = sortThreads(
				roomThreads.map((thread) => {
					if (thread.id !== targetRoomId) {
						return thread;
					}
					const fallbackCount =
						typeof thread.memberCount === 'number'
							? Math.max(0, thread.memberCount - 1)
							: undefined;
					const nextCount =
						authoritativeMemberCount >= 0 ? authoritativeMemberCount : fallbackCount;
					return { ...thread, memberCount: nextCount };
				})
			);
			return;
		}

		if (kind === 'room_expired') {
			const payloadRoomId = normalizeRoomIDValue(toStringValue(payload.roomId ?? payload.room_id));
			const expiredRoomId = normalizeRoomIDValue(payloadRoomId || targetRoomId);
			if (expiredRoomId) {
				void handleRoomExpired([expiredRoomId], 'server');
			}
			return;
		}

		if (kind === 'online_list' && targetRoomId && Array.isArray(envelope.payload)) {
			const members = envelope.payload
				.map((entry, index) => parseMember(entry, index))
				.filter((entry): entry is OnlineMember => Boolean(entry));
			onlineByRoom = {
				...onlineByRoom,
				[targetRoomId]: dedupeMembers(members)
			};
			return;
		}

		if (kind === 'user_joined' && targetRoomId) {
			const joined = parseMember(envelope.payload, Date.now());
			if (joined) {
				upsertOnlineMember(targetRoomId, joined);
			}
			return;
		}

		if (kind === 'user_left' && targetRoomId) {
			const leaving = parseMember(envelope.payload, Date.now());
			if (leaving) {
				removeOnlineMember(targetRoomId, leaving.id);
			}
			return;
		}

		if ((kind === 'typing_start' || kind === 'typing_stop') && targetRoomId) {
			const participant = parseMember(envelope.payload, Date.now());
			if (!participant) {
				return;
			}
			if (kind === 'typing_start') {
				setTypingIndicator(targetRoomId, participant.id, participant.name);
			} else {
				clearTypingIndicator(targetRoomId, participant.id);
			}
			return;
		}

		if (kind === 'message_edit' && targetRoomId) {
			const decryptedPayload =
				payload && typeof payload.content === 'string'
					? {
							...payload,
							content: await decryptMessageContent(payload.content)
						}
					: envelope.payload;
			applyMessageEdit(targetRoomId, decryptedPayload);
			return;
		}

		if (kind === 'message_delete' && targetRoomId) {
			applyMessageDelete(targetRoomId, envelope.payload);
		}
	}

	function removeRoomsFromLocalState(roomIDs: string[]) {
		const normalizedRoomIDs = Array.from(
			new Set(roomIDs.map((entry) => normalizeRoomIDValue(entry)).filter((entry) => entry !== ''))
		);
		if (normalizedRoomIDs.length === 0) {
			return { removedCurrentRoom: false, removedNames: [] as string[] };
		}

		const removeSet = new Set(normalizedRoomIDs);
		const removedNames = roomThreads
			.filter((thread) => removeSet.has(normalizeRoomIDValue(thread.id)))
			.map((thread) => thread.name);
		const removedCurrentRoom = removeSet.has(normalizeRoomIDValue(roomId));

		roomThreads = roomThreads.filter((thread) => !removeSet.has(normalizeRoomIDValue(thread.id)));

		const nextMessagesByRoom = { ...messagesByRoom };
		const nextOnlineByRoom = { ...onlineByRoom };
		const nextRoomMetaById = { ...roomMetaById };
		const nextTypingUsersByRoom = { ...typingUsersByRoom };
		const nextHistoryLoadingByRoom = { ...historyLoadingByRoom };
		const nextHistoryHasMoreByRoom = { ...historyHasMoreByRoom };
		const nextOfflineHydratedByRoom = { ...offlineHydratedByRoom };
		const nextUnreadAnchorByRoom = { ...unreadAnchorByRoom };
		const nextRoomMembershipSynced = { ...roomMembershipSynced };
		const nextRoomMembershipSyncing = { ...roomMembershipSyncing };
		const nextDiscussionCommentsCacheByTaskKey = { ...discussionCommentsCacheByTaskKey };

		for (const normalizedRoomID of normalizedRoomIDs) {
			delete nextMessagesByRoom[normalizedRoomID];
			delete nextOnlineByRoom[normalizedRoomID];
			delete nextRoomMetaById[normalizedRoomID];
			delete nextTypingUsersByRoom[normalizedRoomID];
			delete nextHistoryLoadingByRoom[normalizedRoomID];
			delete nextHistoryHasMoreByRoom[normalizedRoomID];
			delete nextOfflineHydratedByRoom[normalizedRoomID];
			delete nextUnreadAnchorByRoom[normalizedRoomID];
			delete nextRoomMembershipSynced[normalizedRoomID];
			delete nextRoomMembershipSyncing[normalizedRoomID];
			const cachePrefix = `${normalizedRoomID}::`;
			for (const cacheKey of Object.keys(nextDiscussionCommentsCacheByTaskKey)) {
				if (cacheKey.startsWith(cachePrefix)) {
					delete nextDiscussionCommentsCacheByTaskKey[cacheKey];
				}
			}
		}

		messagesByRoom = nextMessagesByRoom;
		onlineByRoom = nextOnlineByRoom;
		roomMetaById = nextRoomMetaById;
		typingUsersByRoom = nextTypingUsersByRoom;
		historyLoadingByRoom = nextHistoryLoadingByRoom;
		historyHasMoreByRoom = nextHistoryHasMoreByRoom;
		offlineHydratedByRoom = nextOfflineHydratedByRoom;
		unreadAnchorByRoom = nextUnreadAnchorByRoom;
		roomMembershipSynced = nextRoomMembershipSynced;
		roomMembershipSyncing = nextRoomMembershipSyncing;
		discussionCommentsCacheByTaskKey = nextDiscussionCommentsCacheByTaskKey;

		return { removedCurrentRoom, removedNames };
	}

	async function handleRoomExpired(roomIDs: string[], source: 'server' | 'timer') {
		const { removedCurrentRoom, removedNames } = removeRoomsFromLocalState(roomIDs);
		if (removedNames.length === 0 && !removedCurrentRoom) {
			return;
		}

		setMessageActionMode('none');
		showRoomDetails = false;
		showRoomSearch = false;
		if (removedCurrentRoom) {
			activeReply = null;
		}

		if (removedNames.length === 1) {
			const contextLabel = source === 'server' ? '' : ' locally';
			showErrorToast(`Room expired${contextLabel}: ${removedNames[0]}`);
		} else if (removedCurrentRoom) {
			const contextLabel = source === 'server' ? '' : ' locally';
			showErrorToast(`Room expired${contextLabel}`);
		} else {
			showErrorToast(`${removedNames.length} rooms expired and were removed`);
		}

		await refreshSidebarRooms();

		if (!removedCurrentRoom) {
			return;
		}
		const fallbackJoined = roomThreads.find((thread) => thread.status === 'joined');
		const fallbackThread = fallbackJoined ?? roomThreads.find((thread) => thread.status !== 'left');
		if (fallbackThread) {
			selectRoom(fallbackThread.id, fallbackThread.status === 'joined');
			return;
		}
		await goto('/');
	}

	function processKnownExpiredRooms() {
		if (roomThreads.length === 0) {
			return;
		}
		const now = getApproxServerNowMs(roomExpiryTickMs);
		const expiredRoomIDs = roomThreads
			.map((thread) => normalizeRoomIDValue(thread.id))
			.filter((entry) => entry !== '')
			.filter((normalizedRoomID) => {
				const expiresAt = getRoomExpiry(normalizedRoomID);
				return expiresAt > 0 && expiresAt <= now;
			});
		if (expiredRoomIDs.length === 0) {
			return;
		}
		void handleRoomExpired(expiredRoomIDs, 'timer');
	}

	function addIncomingMessage(message: ChatMessage) {
		const isOwnMessage =
			normalizeIdentifier(message.senderId) !== '' &&
			normalizeIdentifier(message.senderId) === normalizeIdentifier(currentUserId);
		const shouldCountUnread = !isOwnMessage;
		upsertMessage(message.roomId, message, shouldCountUnread);
	}

	function upsertMessage(targetRoomId: string, message: ChatMessage, shouldCountUnread: boolean) {
		const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
		const previousUnread =
			roomThreads.find((thread) => thread.id === normalizedRoomID)?.unread ?? 0;
		const next = upsertMessageState(
			messagesByRoom,
			roomThreads,
			targetRoomId,
			message,
			shouldCountUnread,
			{
				formatRoomName,
				getMessagePreviewText,
				createThread
			}
		);
		messagesByRoom = next.messagesByRoom;
		roomThreads = next.roomThreads;
		if (shouldCountUnread && normalizedRoomID) {
			const nextUnread =
				next.roomThreads.find((thread) => thread.id === normalizedRoomID)?.unread ?? 0;
			if (nextUnread > 0 && !unreadAnchorByRoom[normalizedRoomID] && nextUnread > previousUnread) {
				const roomMessages = next.messagesByRoom[normalizedRoomID] ?? [];
				const fallbackIndex = Math.max(0, roomMessages.length - nextUnread);
				const fallbackAnchor = roomMessages[fallbackIndex]?.id || message.id;
				unreadAnchorByRoom = {
					...unreadAnchorByRoom,
					[normalizedRoomID]: fallbackAnchor
				};
			}
		}
		queueOfflineCachePersist(targetRoomId);
	}

	function mergeMessages(targetRoomId: string, incoming: ChatMessage[]) {
		const next = mergeMessagesState(messagesByRoom, roomThreads, targetRoomId, incoming, {
			formatRoomName,
			getMessagePreviewText,
			createThread
		});
		messagesByRoom = next.messagesByRoom;
		roomThreads = next.roomThreads;
		if (incoming.length > 0) {
			queueOfflineCachePersist(targetRoomId);
		}
	}

	function applyMessageEdit(targetRoomId: string, payload: unknown) {
		const next = applyMessageEditState(messagesByRoom, roomThreads, targetRoomId, payload, {
			formatRoomName,
			getMessagePreviewText,
			createThread
		});
		if (!next.changed) {
			return;
		}
		messagesByRoom = next.messagesByRoom;
		roomThreads = next.roomThreads;
		queueOfflineCachePersist(targetRoomId);
	}

	function applyMessageDelete(targetRoomId: string, payload: unknown) {
		const next = applyMessageDeleteState(
			messagesByRoom,
			roomThreads,
			targetRoomId,
			payload,
			DELETED_MESSAGE_PLACEHOLDER,
			{
				formatRoomName,
				getMessagePreviewText,
				createThread
			}
		);
		if (!next.changed) {
			return;
		}
		messagesByRoom = next.messagesByRoom;
		roomThreads = next.roomThreads;
		queueOfflineCachePersist(targetRoomId);
	}

	function applyMessagePinState(targetRoomId: string, payload: unknown) {
		const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
		if (!normalizedRoomID || !payload || typeof payload !== 'object') {
			return;
		}
		const source = payload as Record<string, unknown>;
		const messageId = normalizeMessageID(
			toStringValue(source.messageId ?? source.message_id ?? source.id)
		);
		if (!messageId) {
			return;
		}
		const isPinned = toBool(source.isPinned ?? source.is_pinned ?? true);
		const pinnedBy = isPinned
			? normalizeIdentifier(toStringValue(source.pinnedBy ?? source.pinned_by))
			: '';
		const pinnedByName = isPinned
			? normalizeUsernameValue(toStringValue(source.pinnedByName ?? source.pinned_by_name))
			: '';
		const roomMessages = messagesByRoom[normalizedRoomID] ?? [];
		let changed = false;
		const nextRoomMessages = roomMessages.map((entry) => {
			if (normalizeMessageID(entry.id) !== messageId) {
				return entry;
			}
			if (
				Boolean(entry.isPinned) === isPinned &&
				(entry.pinnedBy || '') === pinnedBy &&
				(entry.pinnedByName || '') === pinnedByName
			) {
				return entry;
			}
			changed = true;
			return {
				...entry,
				isPinned,
				pinnedBy,
				pinnedByName
			};
		});
		if (!changed) {
			return;
		}
		messagesByRoom = {
			...messagesByRoom,
			[normalizedRoomID]: nextRoomMessages
		};
		queueOfflineCachePersist(normalizedRoomID);
	}

	function markRoomAsRead(targetRoomId: string) {
		const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
		roomThreads = markRoomAsReadState(roomThreads, normalizedRoomID);
		if (normalizedRoomID && unreadAnchorByRoom[normalizedRoomID]) {
			const nextUnreadAnchors = { ...unreadAnchorByRoom };
			delete nextUnreadAnchors[normalizedRoomID];
			unreadAnchorByRoom = nextUnreadAnchors;
		}
	}

	function getLastReadTimestamp(targetRoomId: string) {
		return getLastReadTimestampState({
			targetRoomId,
			roomThreads,
			messagesByRoom,
			currentUserId
		});
	}

	function getUnreadStartMessageId(targetRoomId: string) {
		return getUnreadStartMessageIdState({
			targetRoomId,
			roomThreads,
			messagesByRoom,
			currentUserId
		});
	}

	function applyReadProgress(targetRoomId: string, lastSeenMessageId: string) {
		const next = applyReadProgressState(lastSeenMessageId, {
			targetRoomId,
			roomThreads,
			messagesByRoom,
			unreadAnchorByRoom,
			currentUserId
		});
		if (!next.changed) {
			return;
		}
		roomThreads = next.roomThreads;
		unreadAnchorByRoom = next.unreadAnchorByRoom;
	}

	function onChatReadProgress(
		event: CustomEvent<{ isNearBottom: boolean; lastSeenMessageId: string }>
	) {
		if (!roomId) {
			return;
		}
		if (isMobileView && mobilePane !== 'chat') {
			return;
		}
		if (roomMessageSearch.trim()) {
			return;
		}
		applyReadProgress(roomId, event.detail?.lastSeenMessageId || '');
	}

	function upsertOnlineMember(targetRoomId: string, member: OnlineMember) {
		onlineByRoom = upsertOnlineMemberState(onlineByRoom, targetRoomId, member);
	}

	function removeOnlineMember(targetRoomId: string, memberId: string) {
		onlineByRoom = removeOnlineMemberState(onlineByRoom, targetRoomId, memberId);
	}

	function dedupeMembers(members: OnlineMember[]) {
		return dedupeMembersState(members);
	}

	function prioritizeOnlineMembersForViewer(members: OnlineMember[], viewerId: string) {
		if (!members.length) {
			return members;
		}
		const normalizedViewerId = normalizeIdentifier(viewerId);
		return [...members].sort((left, right) => {
			const leftIsViewer = normalizeIdentifier(left.id) === normalizedViewerId ? 0 : 1;
			const rightIsViewer = normalizeIdentifier(right.id) === normalizedViewerId ? 0 : 1;
			if (leftIsViewer !== rightIsViewer) {
				return leftIsViewer - rightIsViewer;
			}
			const leftJoinedAt = parseOptionalTimestamp(left.joinedAt);
			const rightJoinedAt = parseOptionalTimestamp(right.joinedAt);
			if (leftJoinedAt !== rightJoinedAt) {
				return leftJoinedAt - rightJoinedAt;
			}
			return left.name.localeCompare(right.name);
		});
	}

	async function sendMessage(payload?: ComposerMediaPayload) {
		if (!roomId || !isMember) {
			showErrorToast('Join room before sending messages');
			return;
		}

		const text = (payload?.text ?? draftMessage).trim();
		if (getUTF8ByteLength(text) > MESSAGE_TEXT_MAX_BYTES) {
			return;
		}
		const payloadType = (payload?.type || '').trim().toLowerCase();
		const payloadContent = payload?.content?.trim() ?? '';
		const isTaskMessage = payloadType === 'task' && payloadContent !== '';
		const isMediaMessage = payloadType !== '' && payloadType !== 'task' && payloadContent !== '';
		if (!text && !isMediaMessage && !isTaskMessage) {
			return;
		}
		if (isTaskMessage && getUTF8ByteLength(payloadContent) > MESSAGE_TEXT_MAX_BYTES) {
			showErrorToast('Task payload is too large');
			return;
		}
		const replyTarget = activeReply;
		const replyToMessageId = replyTarget ? normalizeMessageID(replyTarget.messageId) : '';
		const replyToSnippet = replyToMessageId
			? buildReplySnippet(replyTarget?.senderName || '', replyTarget?.content || '')
			: '';

		let outgoing: ChatMessage;
		if (isTaskMessage) {
			outgoing = {
				id: createMessageId(roomId),
				roomId,
				senderId: currentUserId,
				senderName: currentUsername,
				content: payloadContent,
				type: 'task',
				mediaUrl: '',
				mediaType: '',
				fileName: '',
				replyToMessageId,
				replyToSnippet,
				createdAt: Date.now(),
				pending: true
			};
		} else if (isMediaMessage) {
			outgoing = {
				id: createMessageId(roomId),
				roomId,
				senderId: currentUserId,
				senderName: currentUsername,
				content: text,
				type: payloadType || 'file',
				mediaUrl: payloadContent,
				mediaType: payloadType,
				fileName: payload?.fileName?.trim() ?? '',
				replyToMessageId,
				replyToSnippet,
				createdAt: Date.now(),
				pending: true
			};
		} else {
			outgoing = {
				id: createMessageId(roomId),
				roomId,
				senderId: currentUserId,
				senderName: currentUsername,
				content: text,
				type: 'text',
				mediaUrl: '',
				mediaType: '',
				fileName: '',
				replyToMessageId,
				replyToSnippet,
				createdAt: Date.now(),
				pending: true
			};
		}

		upsertMessage(roomId, outgoing, false);
		const encryptedContent = await encryptMessageContent(outgoing.content);
		sendSocketPayload(
			toWireMessage({
				...outgoing,
				content: encryptedContent
			})
		);
		applyReadProgress(roomId, outgoing.id);
		sendTypingStop();
		draftMessage = '';
		attachedFile = null;
		activeReply = null;
	}

	async function ensureMessagePinned(message: ChatMessage) {
		if (!roomId || !isMember) {
			return false;
		}
		const normalizedMessageID = normalizeMessageID(message.id);
		const normalizedUserID = normalizeIdentifier(currentUserId);
		if (!normalizedMessageID || !normalizedUserID) {
			return false;
		}
		try {
			const normalizedUsername = normalizeUsernameValue(currentUsername) || 'User';
			const res = await fetch(roomPinsEndpoint(API_BASE, roomId), {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					userId: normalizedUserID,
					username: normalizedUsername,
					messageId: normalizedMessageID
				})
			});
			const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
			if (!res.ok) {
				throw new Error(toStringValue(data.error) || 'Failed to pin message');
			}
			applyMessagePinState(roomId, {
				messageId: normalizedMessageID,
				isPinned: toBool(data.isPinned ?? true),
				pinnedBy: toStringValue(data.pinnedBy ?? normalizedUserID),
				pinnedByName: toStringValue(data.pinnedByName ?? normalizedUsername)
			});
			return true;
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to pin message');
			return false;
		}
	}

	function upsertDiscussionCommentLocal(
		comment: ChatMessage,
		pinnedMessageId = activeDiscussionTaskId
	) {
		const next = upsertDiscussionCommentList(discussionComments, comment);
		discussionComments = next;
		if (roomId && normalizeMessageID(pinnedMessageId)) {
			discussionCommentsCacheByTaskKey = writeDiscussionCommentsCache(
				discussionCommentsCacheByTaskKey,
				roomId,
				pinnedMessageId,
				next
			);
		}
	}

	async function loadDiscussionComments(pinnedMessageId: string) {
		const targetRoomID = normalizeRoomIDValue(roomId);
		if (!targetRoomID || !isMember) {
			discussionComments = [];
			return;
		}
		const normalizedPinnedMessageID = normalizeMessageID(pinnedMessageId);
		const normalizedUserID = normalizeIdentifier(currentUserId);
		if (!normalizedPinnedMessageID || !normalizedUserID) {
			discussionComments = [];
			return;
		}

		const requestURL = `${discussionCommentsEndpoint(
			API_BASE,
			targetRoomID,
			normalizedPinnedMessageID
		)}?userId=${encodeURIComponent(normalizedUserID)}&limit=50`;
		try {
			const res = await fetch(requestURL);
			const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
			if (!res.ok) {
				throw new Error(toStringValue(data.error) || 'Failed to load discussion comments');
			}
			const parsedComments = (
				await parseIncomingMessagesWithE2EE(
					Array.isArray(data.comments) ? data.comments : [],
					targetRoomID
				)
			).sort((left, right) => left.createdAt - right.createdAt);
			discussionCommentsCacheByTaskKey = writeDiscussionCommentsCache(
				discussionCommentsCacheByTaskKey,
				targetRoomID,
				normalizedPinnedMessageID,
				parsedComments
			);

			if (normalizeMessageID(activeDiscussionTaskId) !== normalizedPinnedMessageID) {
				return;
			}
			discussionComments =
				readDiscussionCommentsCache(
					discussionCommentsCacheByTaskKey,
					targetRoomID,
					normalizedPinnedMessageID
				) ?? parsedComments;
		} catch (error) {
			if (normalizeMessageID(activeDiscussionTaskId) === normalizedPinnedMessageID) {
				discussionComments =
					readDiscussionCommentsCache(
						discussionCommentsCacheByTaskKey,
						targetRoomID,
						normalizedPinnedMessageID
					) ?? [];
			}
			showErrorToast(error instanceof Error ? error.message : 'Failed to load discussion comments');
		}
	}

	function openDiscussionForMessage(messageId: string) {
		const normalizedMessageId = normalizeMessageID(messageId);
		if (!roomId || !normalizedMessageId) {
			return;
		}
		const match = (messagesByRoom[roomId] ?? []).find(
			(entry) => normalizeMessageID(entry.id) === normalizedMessageId
		);
		if (!match) {
			return;
		}
		activeDiscussionTaskId = match.id;
		isDiscussionOpen = true;
		discussionOpenedAtMs = Date.now();
		const normalizedTaskID = normalizeMessageID(match.id);
		if (!normalizedTaskID) {
			return;
		}
		const cachedComments = readDiscussionCommentsCache(
			discussionCommentsCacheByTaskKey,
			roomId,
			normalizedTaskID
		);
		if (cachedComments) {
			discussionTaskTracker = normalizedTaskID;
			discussionComments = cachedComments;
			return;
		}
		if (discussionTaskTracker !== normalizedTaskID || discussionComments.length === 0) {
			discussionTaskTracker = normalizedTaskID;
			discussionComments = [];
			void loadDiscussionComments(match.id);
		}
	}

	function closeDiscussion() {
		isDiscussionOpen = false;
		activeDiscussionTaskId = '';
		discussionOpenedAtMs = 0;
		discussionComments = [];
	}

	async function commitTaskPayloadUpdate(messageId: string, nextContent: string) {
		if (!roomId || !messageId || !nextContent) {
			return;
		}
		applyMessageEdit(roomId, {
			messageId,
			content: nextContent,
			editedAt: Date.now(),
			messageType: 'task'
		});
		const encryptedContent = await encryptMessageContent(nextContent);
		sendSocketPayload({
			type: 'message_edit',
			roomId,
			messageId,
			content: encryptedContent,
			messageType: 'task'
		});
	}

	async function onTaskToggle(event: CustomEvent<{ messageId: string; taskIndex: number }>) {
		if (!roomId || !isMember) {
			return;
		}
		const messageId = normalizeMessageID(event.detail.messageId);
		const taskIndex = Number(event.detail.taskIndex);
		if (!messageId || !Number.isInteger(taskIndex) || taskIndex < 0) {
			return;
		}

		const message = (messagesByRoom[roomId] ?? []).find(
			(entry) => normalizeMessageID(entry.id) === messageId && entry.type === 'task'
		);
		if (!message) {
			return;
		}

		const parsedPayload = parseTaskMessagePayload(message.content);
		if (!parsedPayload) {
			showErrorToast('Task data is invalid');
			return;
		}
		const nextPayload = toggleTaskItem(parsedPayload, taskIndex, currentUsername);
		if (!nextPayload) {
			return;
		}
		const nextContent = stringifyTaskMessagePayload(nextPayload);
		if (getUTF8ByteLength(nextContent) > MESSAGE_TEXT_MAX_BYTES) {
			showErrorToast('Task update is too large');
			return;
		}
		await commitTaskPayloadUpdate(messageId, nextContent);
	}

	async function onTaskAdd(event: CustomEvent<{ messageId: string; text: string }>) {
		if (!roomId || !isMember) {
			return;
		}
		const messageId = normalizeMessageID(event.detail.messageId);
		const taskText = (event.detail.text || '').trim();
		if (!messageId || !taskText) {
			return;
		}
		const message = (messagesByRoom[roomId] ?? []).find(
			(entry) => normalizeMessageID(entry.id) === messageId && entry.type === 'task'
		);
		if (!message) {
			return;
		}

		const parsedPayload = parseTaskMessagePayload(message.content);
		if (!parsedPayload) {
			showErrorToast('Task data is invalid');
			return;
		}
		const nextPayload = addTaskItem(parsedPayload, taskText, currentUsername, Date.now());
		if (!nextPayload) {
			showErrorToast('Unable to add task item');
			return;
		}
		const nextContent = stringifyTaskMessagePayload(nextPayload);
		if (getUTF8ByteLength(nextContent) > MESSAGE_TEXT_MAX_BYTES) {
			showErrorToast('Task update is too large');
			return;
		}
		await commitTaskPayloadUpdate(messageId, nextContent);
	}

	async function onDiscussionCommentSubmit(
		event: CustomEvent<{ content: string; replyToMessageId?: string }>
	) {
		if (!roomId || !isMember || !activeDiscussionTask) {
			return;
		}
		if (discussionComments.length >= 50) {
			showErrorToast('Discussion limit reached (50/50)');
			return;
		}
		const content = (event.detail.content || '').trim();
		if (!content) {
			return;
		}
		if (getUTF8ByteLength(content) > MESSAGE_TEXT_MAX_BYTES) {
			showErrorToast('Comment is too long');
			return;
		}

		const requestedReplyID = normalizeMessageID(event.detail.replyToMessageId || '');
		const allowedReplyIDs = new Set<string>(
			discussionComments.map((entry) => normalizeMessageID(entry.id))
		);
		const parentCommentId =
			requestedReplyID && allowedReplyIDs.has(requestedReplyID) ? requestedReplyID : '';
		if (parentCommentId) {
			const discussionCommentMap = buildDiscussionCommentMap(discussionComments);
			const parentDepth = resolveDiscussionCommentDepth(
				parentCommentId,
				discussionCommentMap,
				DISCUSSION_MAX_REPLY_DEPTH
			);
			if (parentDepth >= DISCUSSION_MAX_REPLY_DEPTH) {
				showErrorToast('Reply nesting limit reached (max 4 levels)');
				return;
			}
		}

		const normalizedTaskID = normalizeMessageID(activeDiscussionTask.id);
		if (!normalizedTaskID) {
			return;
		}

		const encryptedContent = await encryptMessageContent(content);
		const queued = sendSocketPayload({
			type: 'discussion_comment',
			roomId,
			pinMessageId: normalizedTaskID,
			parentCommentId,
			content: encryptedContent
		});
		if (!queued) {
			showErrorToast('Socket reconnecting. Comment queued.');
		}
	}

	async function onDiscussionCommentEditRequest(
		event: CustomEvent<{ messageId: string; content: string; skipPrompt?: boolean }>
	) {
		if (!roomId || !activeDiscussionTask || !isMember) {
			return;
		}
		const commentId = normalizeMessageID(event.detail.messageId);
		if (!commentId) {
			return;
		}
		const currentComment = discussionComments.find(
			(entry) => normalizeMessageID(entry.id) === commentId
		);
		if (!currentComment) {
			showErrorToast('Comment not found in current discussion');
			return;
		}
		if (normalizeIdentifier(currentComment.senderId) !== normalizeIdentifier(currentUserId)) {
			showErrorToast('You can only edit your own comments');
			return;
		}

		const inlineContent = (event.detail.content || '').trim();
		const currentContent = (currentComment.content || '').trim();
		let nextContent = inlineContent;
		if (!event.detail.skipPrompt) {
			const nextContentRaw = await openPromptDialog({
				title: 'Edit Comment',
				message: 'Update your discussion comment.',
				initialValue: currentContent,
				placeholder: 'Comment',
				maxLength: 2000,
				confirmLabel: 'Save',
				cancelLabel: 'Cancel',
				multiline: true
			});
			if (nextContentRaw === null) {
				return;
			}
			nextContent = nextContentRaw.trim();
		}
		if (!nextContent || nextContent === currentContent) {
			return;
		}
		if (getUTF8ByteLength(nextContent) > MESSAGE_TEXT_MAX_BYTES) {
			showErrorToast('Comment is too long');
			return;
		}

		const normalizedTaskID = normalizeMessageID(activeDiscussionTask.id);
		const normalizedUserID = normalizeIdentifier(currentUserId);
		if (!normalizedTaskID || !normalizedUserID) {
			return;
		}
		try {
			const encryptedContent = await encryptMessageContent(nextContent);
			const res = await fetch(
				`${discussionCommentsEndpoint(API_BASE, roomId, normalizedTaskID)}/${encodeURIComponent(commentId)}`,
				{
					method: 'PUT',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({
						userId: normalizedUserID,
						content: encryptedContent
					})
				}
			);
			const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
			if (!res.ok) {
				throw new Error(toStringValue(data.error) || 'Failed to edit comment');
			}
			const parsed = await parseIncomingMessageWithE2EE(data.comment, roomId);
			if (!parsed) {
				throw new Error('Comment payload is invalid');
			}
			upsertDiscussionCommentLocal(parsed, normalizedTaskID);
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to edit comment');
		}
	}

	async function onDiscussionCommentDeleteRequest(event: CustomEvent<{ messageId: string }>) {
		if (!roomId || !activeDiscussionTask || !isMember) {
			return;
		}
		const commentId = normalizeMessageID(event.detail.messageId);
		if (!commentId) {
			return;
		}
		const currentComment = discussionComments.find(
			(entry) => normalizeMessageID(entry.id) === commentId
		);
		if (!currentComment) {
			showErrorToast('Comment not found in current discussion');
			return;
		}
		if (normalizeIdentifier(currentComment.senderId) !== normalizeIdentifier(currentUserId)) {
			showErrorToast('You can only delete your own comments');
			return;
		}

		const confirmed = await openConfirmDialog({
			title: 'Delete Comment',
			message: 'This action cannot be undone.',
			confirmLabel: 'Delete',
			cancelLabel: 'Cancel',
			danger: true
		});
		if (!confirmed) {
			return;
		}

		const normalizedTaskID = normalizeMessageID(activeDiscussionTask.id);
		const normalizedUserID = normalizeIdentifier(currentUserId);
		if (!normalizedTaskID || !normalizedUserID) {
			return;
		}
		try {
			const res = await fetch(
				`${discussionCommentsEndpoint(API_BASE, roomId, normalizedTaskID)}/${encodeURIComponent(commentId)}`,
				{
					method: 'DELETE',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({
						userId: normalizedUserID
					})
				}
			);
			const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
			if (!res.ok) {
				throw new Error(toStringValue(data.error) || 'Failed to delete comment');
			}
			const parsed = await parseIncomingMessageWithE2EE(data.comment, roomId);
			if (!parsed) {
				throw new Error('Comment payload is invalid');
			}
			upsertDiscussionCommentLocal(parsed, normalizedTaskID);
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to delete comment');
		}
	}

	function onDiscussionCommentPinToggle(
		event: CustomEvent<{ messageId: string; isPinned: boolean }>
	) {
		if (!roomId || !activeDiscussionTask || !isMember) {
			return;
		}
		const commentId = normalizeMessageID(event.detail.messageId);
		const pinMessageId = normalizeMessageID(activeDiscussionTask.id);
		if (!commentId || !pinMessageId) {
			return;
		}

		const nextPinned = Boolean(event.detail.isPinned);
		const normalizedCurrentUserID = normalizeIdentifier(currentUserId);
		const normalizedCurrentUsername = normalizeUsernameValue(currentUsername) || 'User';
		const existingComment = discussionComments.find(
			(entry) => normalizeMessageID(entry.id) === commentId
		);
		if (existingComment) {
			upsertDiscussionCommentLocal({
				...existingComment,
				isPinned: nextPinned,
				pinnedBy: nextPinned ? normalizedCurrentUserID : '',
				pinnedByName: nextPinned ? normalizedCurrentUsername : ''
			});
		}

		const queued = sendSocketPayload({
			type: 'discussion_comment_pin',
			roomId,
			pinMessageId,
			commentId,
			isPinned: nextPinned
		});
		if (!queued) {
			showErrorToast('Socket reconnecting. Pin action queued.');
		}
	}

	async function navigateDiscussionPins(direction: 'previous' | 'next') {
		if (!roomId || !activeDiscussionTask) {
			return;
		}
		const anchorTimestamp = Number(activeDiscussionTask.createdAt);
		if (!Number.isFinite(anchorTimestamp) || anchorTimestamp <= 0) {
			return;
		}
		const queryParam = direction === 'previous' ? 'before' : 'after';
		try {
			const res = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(roomId)}/pins/navigate?${queryParam}=${encodeURIComponent(String(anchorTimestamp))}`
			);
			const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
			if (!res.ok) {
				throw new Error(toStringValue(data.error) || 'Failed to navigate pinned discussions');
			}
			const rawMessage = data.message;
			const parsed = await parseIncomingMessageWithE2EE(rawMessage, roomId);
			if (!parsed) {
				showErrorToast(
					direction === 'previous'
						? 'No previous pinned discussion in this room'
						: 'No next pinned discussion in this room'
				);
				return;
			}
			mergeMessages(roomId, [parsed]);
			openDiscussionForMessage(parsed.id);
		} catch (error) {
			showErrorToast(
				error instanceof Error ? error.message : 'Failed to navigate pinned discussions'
			);
		}
	}

	function onReplyRequest(event: CustomEvent<ReplyTarget>) {
		const messageId = normalizeMessageID(event.detail.messageId);
		if (!messageId) {
			return;
		}
		activeReply = {
			messageId,
			senderName: normalizeUsernameValue(event.detail.senderName) || 'User',
			content: (event.detail.content || '').trim()
		};
	}

	function clearReplyTarget() {
		activeReply = null;
	}

	function handleComposerAttach(event: CustomEvent<{ file: File | null; error?: string }>) {
		if (event.detail?.error) {
			showErrorToast(event.detail.error);
		}
	}

	function handleComposerRemoveAttachment() {
		attachedFile = null;
	}

	function toggleLeftMenu() {
		showLeftMenu = !showLeftMenu;
	}

	async function renameRoom(targetRoomId: string = roomId) {
		const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
		if (!normalizedRoomID) {
			return;
		}
		showLeftMenu = false;

		const existing = roomThreads.find((thread) => thread.id === normalizedRoomID);
		const currentName = existing?.name || formatRoomName(normalizedRoomID);
		const requested = await openPromptDialog({
			title: 'Rename Room',
			message: 'Pick a new display name for this room.',
			initialValue: currentName,
			placeholder: 'Room name',
			maxLength: 20,
			confirmLabel: 'Rename',
			cancelLabel: 'Cancel'
		});
		if (requested === null) {
			return;
		}

		const normalizedName = normalizeRoomNameValue(requested);
		if (!normalizedName) {
			showErrorToast('Room name cannot be empty');
			return;
		}
		if (normalizedName === currentName) {
			return;
		}

		try {
			const res = await fetch(`${API_BASE}/api/rooms/rename`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					roomId: normalizedRoomID,
					roomName: normalizedName
				})
			});
			const data = await res.json().catch(() => ({}));
			if (!res.ok) {
				throw new Error(data.error || 'Failed to rename room');
			}

			const savedName = normalizeRoomNameValue(toStringValue(data.roomName)) || normalizedName;
			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === normalizedRoomID ? { ...thread, name: savedName } : thread
				)
			);

			if (normalizedRoomID === roomId) {
				const params = new URLSearchParams($page.url.searchParams.toString());
				removeLegacyRoomTimeQueryParams(params);
				params.set('name', savedName);
				await goto(`/chat/${encodeURIComponent(normalizedRoomID)}?${params.toString()}`, {
					replaceState: true,
					noScroll: true,
					keepFocus: true
				});
			}

			showLeftMenu = false;
			showErrorToast('Room renamed');
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to rename room');
		}
	}

	async function createRoomFromMenu() {
		showLeftMenu = false;
		const action = await openRoomActionDialog(roomNameFromURL || '');
		if (!action) {
			return;
		}
		const roomPassword = await openOptionalRoomPasswordDialog($activeRoomPassword);
		if (roomPassword === null) {
			return;
		}
		activeRoomPassword.set(roomPassword);

		const requestedName = normalizeRoomNameValue(action.roomName);
		if (!requestedName) {
			showErrorToast('Room name cannot be empty');
			return;
		}
		const roomMode: RoomMenuMode = action.mode;

		try {
			const res = await fetch(`${API_BASE}/api/rooms/join`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					roomName: requestedName,
					username: currentUsername,
					userId: normalizeIdentifier(currentUserId),
					type: 'ephemeral',
					mode: roomMode
				})
			});
			const data = await res.json();
			if (!res.ok) {
				throw new Error(
					data.error ||
						(roomMode === 'join' ? 'Failed to join existing room' : 'Failed to create room')
				);
			}
			syncServerClock(
				(data as { serverNow?: unknown; server_now?: unknown }).serverNow ??
					(data as { serverNow?: unknown; server_now?: unknown }).server_now
			);

			const nextRoomId = normalizeRoomIDValue(toStringValue(data.roomId));
			if (!nextRoomId) {
				throw new Error('Invalid room id returned from server');
			}
			const nextRoomName =
				normalizeRoomNameValue(toStringValue(data.roomName)) || formatRoomName(nextRoomId);
			const nextCreatedAt = toTimestamp(data.createdAt);
			const nextExpiresAt = parseOptionalTimestamp(data.expiresAt ?? data.expires_at);
			const nextIsAdmin = toBool(
				(data as { isAdmin?: unknown; is_admin?: unknown }).isAdmin ??
					(data as { isAdmin?: unknown; is_admin?: unknown }).is_admin
			);
			const nextAdminCode = normalizeAdminCodeValue(
				(data as { adminCode?: unknown; admin_code?: unknown }).adminCode ??
					(data as { adminCode?: unknown; admin_code?: unknown }).admin_code
			);
			const nextRequiresPassword = toBool(
				(data as { requiresPassword?: unknown; requires_password?: unknown }).requiresPassword ??
					(data as { requiresPassword?: unknown; requires_password?: unknown }).requires_password
			);

			ensureRoomThread(nextRoomId, nextRoomName, 'joined');
			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === nextRoomId
						? {
								...thread,
								isAdmin: nextIsAdmin,
								adminCode: nextIsAdmin ? nextAdminCode : '',
								requiresPassword: nextRequiresPassword
							}
						: thread
				)
			);
			markRoomMembershipSynced(nextRoomId);
			ensureRoomMeta(nextRoomId, nextCreatedAt, nextExpiresAt);

			const params = new URLSearchParams({
				name: nextRoomName,
				member: '1'
			});
			const passwordHash = buildRoomPasswordHash(roomPassword);
			await goto(`/chat/${encodeURIComponent(nextRoomId)}?${params.toString()}${passwordHash}`);
		} catch (error) {
			showErrorToast(
				error instanceof Error
					? error.message
					: roomMode === 'join'
						? 'Failed to join existing room'
						: 'Failed to create room'
			);
		}
	}

	async function joinCurrentRoom() {
		if (!roomId) {
			return;
		}
		let roomAccessPassword = '';
		let shouldPromptForAccessPassword = Boolean(activeThread?.requiresPassword);
		try {
			while (true) {
				if (shouldPromptForAccessPassword) {
					const enteredPassword = await openRoomAccessPasswordDialog(roomAccessPassword);
					if (enteredPassword === null) {
						return;
					}
					roomAccessPassword = enteredPassword;
				}

				const res = await fetch(`${API_BASE}/api/rooms/join`, {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({
						roomId,
						roomPassword: roomAccessPassword,
						username: currentUsername,
						userId: normalizeIdentifier(currentUserId),
						mode: 'join'
					})
				});
				const data = await res.json().catch(() => ({}));
				const requiresPassword = toBool(
					(data as { requiresPassword?: unknown; requires_password?: unknown }).requiresPassword ??
						(data as { requiresPassword?: unknown; requires_password?: unknown }).requires_password
				);
				if (!res.ok) {
					if (requiresPassword) {
						if (shouldPromptForAccessPassword) {
							showErrorToast('Incorrect room password');
						}
						shouldPromptForAccessPassword = true;
						roomAccessPassword = '';
						continue;
					}
					throw new Error(
						toStringValue((data as { error?: unknown }).error) || 'Unable to join room'
					);
				}
				syncServerClock(
					(data as { serverNow?: unknown; server_now?: unknown }).serverNow ??
						(data as { serverNow?: unknown; server_now?: unknown }).server_now
				);

				const joinedName =
					normalizeRoomNameValue(toStringValue(data.roomName)) ||
					activeThread.name ||
					formatRoomName(roomId);
				const joinedCreatedAt = toTimestamp(data.createdAt);
				const joinedExpiresAt = parseOptionalTimestamp(data.expiresAt ?? data.expires_at);
				const joinedIsAdmin = toBool(
					(data as { isAdmin?: unknown; is_admin?: unknown }).isAdmin ??
						(data as { isAdmin?: unknown; is_admin?: unknown }).is_admin
				);
				const joinedAdminCode = normalizeAdminCodeValue(
					(data as { adminCode?: unknown; admin_code?: unknown }).adminCode ??
						(data as { adminCode?: unknown; admin_code?: unknown }).admin_code
				);
				const joinedRequiresPassword = requiresPassword;
				ensureRoomThread(roomId, joinedName, 'joined');
				markRoomMembershipSynced(roomId);
				ensureRoomMeta(roomId, joinedCreatedAt, joinedExpiresAt);
				roomThreads = sortThreads(
					roomThreads.map((thread) =>
						thread.id === roomId
							? {
									...thread,
									status: 'joined',
									name: joinedName,
									isAdmin: joinedIsAdmin,
									adminCode: joinedIsAdmin ? joinedAdminCode : '',
									requiresPassword: joinedRequiresPassword
								}
							: thread
					)
				);

				const params = new URLSearchParams({ name: joinedName, member: '1' });
				const passwordHash = buildRoomPasswordHash($activeRoomPassword);
				await goto(`/chat/${encodeURIComponent(roomId)}?${params.toString()}${passwordHash}`);
				return;
			}
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Unable to join room');
		}
	}

	async function extendRoomTTL(targetRoomId: string) {
		if (!browser || !targetRoomId || isExtendingRoom) {
			return;
		}
		isExtendingRoom = true;
		try {
			const res = await fetch(`${API_BASE}/api/rooms/extend`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ roomId: targetRoomId })
			});
			const data = await res.json().catch(() => ({}));
			if (!res.ok) {
				showErrorToast(data.error || 'Room has reached its 15-day limit');
				return;
			}
			syncServerClock(
				(data as { serverNow?: unknown; server_now?: unknown }).serverNow ??
					(data as { serverNow?: unknown; server_now?: unknown }).server_now
			);
			const expiresAt = parseOptionalTimestamp(data.expiresAt ?? data.expires_at);
			const expiresInSeconds = toInt(data.expiresInSeconds ?? data.expires_in_seconds);
			const createdAt = getRoomCreatedAt(targetRoomId);
			let nextExpiresAt = 0;
			if (expiresAt > 0) {
				nextExpiresAt = expiresAt;
				ensureRoomMeta(targetRoomId, createdAt, nextExpiresAt);
			} else if (expiresInSeconds > 0) {
				nextExpiresAt = getApproxServerNowMs() + expiresInSeconds * 1000;
				ensureRoomMeta(targetRoomId, createdAt, nextExpiresAt);
			}
			showErrorToast(data.message || 'Room extended for 24 hours');
		} catch {
			showErrorToast('Failed to extend room');
		} finally {
			isExtendingRoom = false;
		}
	}

	function requestRoomExtension() {
		if (!roomId) {
			return;
		}
		void extendRoomTTL(roomId);
	}

	function toggleBreakSelectionMode() {
		const nextMode: MessageActionMode = messageActionMode === 'break' ? 'none' : 'break';
		setMessageActionMode(nextMode);
	}

	function togglePinSelectionMode() {
		const nextMode: MessageActionMode = messageActionMode === 'pin' ? 'none' : 'pin';
		setMessageActionMode(nextMode);
	}

	function toggleEditSelectionMode() {
		const nextMode: MessageActionMode = messageActionMode === 'edit' ? 'none' : 'edit';
		setMessageActionMode(nextMode);
	}

	function toggleDeleteSelectionMode() {
		const nextMode: MessageActionMode = messageActionMode === 'delete' ? 'none' : 'delete';
		setMessageActionMode(nextMode);
	}

	function toggleRoomSearch() {
		showRoomSearch = !showRoomSearch;
		if (!showRoomSearch) {
			roomMessageSearch = '';
		}
	}

	function openRoomDetails() {
		showRoomDetails = true;
	}

	function closeRoomDetails() {
		showRoomDetails = false;
	}

	async function onRoomPromoted(event: CustomEvent<{ token?: string; adminCode?: string }>) {
		const nextToken = toStringValue(event.detail?.token).trim();
		if (nextToken) {
			setSessionToken(nextToken);
			authToken.set(nextToken);
		}

		const nextAdminCode = normalizeAdminCodeValue(event.detail?.adminCode);
		if (roomId) {
			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === roomId
						? {
								...thread,
								isAdmin: true,
								adminCode: nextAdminCode || thread.adminCode || ''
							}
						: thread
				)
			);
		}
		showErrorToast('Admin access granted');
		await refreshSidebarRooms();
		if (roomId) {
			await syncRoomMembership(roomId);
		}
	}

	function clearCurrentRoomMessages() {
		if (!roomId) {
			return;
		}
		messagesByRoom = { ...messagesByRoom, [roomId]: [] };
		updateThreadPreview(roomId);
		queueOfflineCachePersist(roomId);
	}

	async function disconnectAndWipe() {
		showLeftMenu = false;
		setMessageActionMode('none');
		sendTypingStop();
		unreadAnchorByRoom = {};
		closeGlobalSocket();
		clearSessionToken();
		authToken.set(null);
		currentUser.set(null);
		try {
			await wipeEncryptedRoomCache();
		} catch {
			// Best effort wipe.
		}
		await goto('/');
	}

	async function onRequestOlderHistory() {
		if (!roomId) {
			return;
		}
		await loadOlderMessages(roomId);
	}

	async function loadOlderMessages(targetRoomId: string) {
		const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
		if (!normalizedRoomID) {
			return;
		}
		if (historyLoadingByRoom[normalizedRoomID]) {
			return;
		}
		if (historyHasMoreByRoom[normalizedRoomID] === false) {
			return;
		}

		const roomMessages = messagesByRoom[normalizedRoomID] ?? [];
		const oldest = roomMessages[0];
		if (!oldest) {
			historyHasMoreByRoom = {
				...historyHasMoreByRoom,
				[normalizedRoomID]: false
			};
			return;
		}

		historyLoadingByRoom = {
			...historyLoadingByRoom,
			[normalizedRoomID]: true
		};
		const anchor = chatWindowRef?.capturePrependAnchor?.() ?? null;
		try {
			const before = encodeURIComponent(oldest.id);
			const beforeCreatedAt =
				Number.isFinite(oldest.createdAt) && oldest.createdAt > 0
					? `&beforeCreatedAt=${encodeURIComponent(String(oldest.createdAt))}`
					: '';
			const normalizedUserID = normalizeIdentifier(currentUserId);
			const userIdQuery = normalizedUserID ? `&userId=${encodeURIComponent(normalizedUserID)}` : '';
			const res = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomID)}/messages?before=${before}${beforeCreatedAt}${userIdQuery}&limit=50`
			);
			const data = await res.json().catch(() => ({}));
			if (!res.ok) {
				throw new Error(data.error || 'Failed to load older messages');
			}

			const payloadMessages = Array.isArray(data.messages) ? data.messages : [];
			const incoming = await parseIncomingMessagesWithE2EE(payloadMessages, normalizedRoomID);
			if (incoming.length > 0) {
				mergeMessages(normalizedRoomID, incoming);
				await tick();
				chatWindowRef?.restorePrependAnchor?.(anchor);
			}

			const hasMore = typeof data.hasMore === 'boolean' ? data.hasMore : incoming.length >= 50;
			historyHasMoreByRoom = {
				...historyHasMoreByRoom,
				[normalizedRoomID]: hasMore
			};
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to load older messages');
		} finally {
			historyLoadingByRoom = {
				...historyLoadingByRoom,
				[normalizedRoomID]: false
			};
		}
	}

	async function onEditMessageRequest(event: CustomEvent<{ messageId: string; content: string }>) {
		if (!roomId) {
			return;
		}
		const messageId = normalizeMessageID(event.detail.messageId);
		if (!messageId) {
			return;
		}
		const current = (event.detail.content || '').trim();
		const nextContentRaw = await openPromptDialog({
			title: 'Edit Message',
			message: 'Update your message content.',
			initialValue: current,
			placeholder: 'Message',
			maxLength: 2000,
			confirmLabel: 'Save',
			cancelLabel: 'Cancel',
			multiline: true
		});
		if (nextContentRaw === null) {
			return;
		}
		const nextContent = nextContentRaw.trim();
		if (!nextContent || nextContent === current) {
			return;
		}
		applyMessageEdit(roomId, {
			messageId,
			content: nextContent,
			editedAt: Date.now()
		});
		const encryptedContent = await encryptMessageContent(nextContent);
		sendSocketPayload({
			type: 'message_edit',
			roomId,
			messageId,
			content: encryptedContent
		});
	}

	async function onDeleteMessageRequest(event: CustomEvent<{ messageId: string }>) {
		if (!roomId) {
			return;
		}
		const messageId = normalizeMessageID(event.detail.messageId);
		if (!messageId) {
			return;
		}
		const confirmed = await openConfirmDialog({
			title: 'Delete Message',
			message: 'This action cannot be undone.',
			confirmLabel: 'Delete',
			cancelLabel: 'Cancel',
			danger: true
		});
		if (!confirmed) {
			return;
		}
		applyMessageDelete(roomId, {
			messageId,
			editedAt: Date.now()
		});
		sendSocketPayload({
			type: 'message_delete',
			roomId,
			messageId
		});
		selectedDeleteMessageIds = selectedDeleteMessageIds.filter((id) => id !== messageId);
	}

	function toggleMessageExpanded(messageId: string) {
		expandedMessages = {
			...expandedMessages,
			[messageId]: !expandedMessages[messageId]
		};
	}

	async function removeMemberFromRoom(targetUserId: string) {
		if (!roomId || !isActiveRoomAdmin) {
			return;
		}
		const normalizedTargetUserId = normalizeIdentifier(targetUserId);
		if (!normalizedTargetUserId) {
			return;
		}
		if (normalizeIdentifier(currentUserId) === normalizedTargetUserId) {
			showErrorToast('Admin cannot remove self');
			return;
		}
		const confirmed = await openConfirmDialog({
			title: 'Remove Member',
			message: 'Remove this member from the room?',
			confirmLabel: 'Remove',
			cancelLabel: 'Cancel',
			danger: true
		});
		if (!confirmed) {
			return;
		}

		try {
			const res = await fetch(`${API_BASE}/api/rooms/remove-member`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					roomId,
					actorUserId: normalizeIdentifier(currentUserId),
					targetUserId: normalizedTargetUserId
				})
			});
			const data = await res.json().catch(() => ({}));
			if (!res.ok) {
				throw new Error(data.error || 'Failed to remove member');
			}
			syncServerClock(
				(data as { serverNow?: unknown; server_now?: unknown }).serverNow ??
					(data as { serverNow?: unknown; server_now?: unknown }).server_now
			);
			removeOnlineMember(roomId, normalizedTargetUserId);
			showErrorToast(data.message || 'Member removed');
			await refreshSidebarRooms();
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to remove member');
		}
	}

	async function deleteCurrentRoomAsAdmin() {
		if (!roomId || !isActiveRoomAdmin) {
			return;
		}
		const confirmed = await openConfirmDialog({
			title: 'Delete Room',
			message: 'Delete this room and all its child rooms? This cannot be undone.',
			confirmLabel: 'Delete Room',
			cancelLabel: 'Cancel',
			danger: true
		});
		if (!confirmed) {
			return;
		}

		try {
			const res = await fetch(`${API_BASE}/api/rooms/delete`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					roomId,
					actorUserId: normalizeIdentifier(currentUserId)
				})
			});
			const data = await res.json().catch(() => ({}));
			if (!res.ok) {
				throw new Error(data.error || 'Failed to delete room');
			}
			syncServerClock(
				(data as { serverNow?: unknown; server_now?: unknown }).serverNow ??
					(data as { serverNow?: unknown; server_now?: unknown }).server_now
			);
			setMessageActionMode('none');
			showRoomDetails = false;

			const deletedRootId = roomId;
			const deleteIDs = collectLocalRoomSubtreeIDs(deletedRootId, roomThreads);
			roomThreads = roomThreads.filter((thread) => !deleteIDs.has(normalizeRoomIDValue(thread.id)));
			const nextMessages = { ...messagesByRoom };
			for (const deleteID of deleteIDs) {
				delete nextMessages[deleteID];
			}
			messagesByRoom = nextMessages;
			const nextOnline = { ...onlineByRoom };
			for (const deleteID of deleteIDs) {
				delete nextOnline[deleteID];
			}
			onlineByRoom = nextOnline;
			const nextMeta = { ...roomMetaById };
			for (const deleteID of deleteIDs) {
				delete nextMeta[deleteID];
			}
			roomMetaById = nextMeta;
			const nextUnreadAnchors = { ...unreadAnchorByRoom };
			for (const deleteID of deleteIDs) {
				delete nextUnreadAnchors[deleteID];
			}
			unreadAnchorByRoom = nextUnreadAnchors;

			await refreshSidebarRooms();
			const fallbackJoined = roomThreads.find((thread) => thread.status === 'joined');
			const fallbackThread =
				fallbackJoined ?? roomThreads.find((thread) => thread.status !== 'left');
			if (fallbackThread) {
				selectRoom(fallbackThread.id, fallbackThread.status === 'joined');
			} else {
				await goto('/');
			}
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to delete room');
		}
	}

	async function leaveCurrentRoom() {
		if (!roomId || !isMember) {
			return;
		}
		const confirmed = await openConfirmDialog({
			title: 'Leave Room',
			message: 'You can join again later if the room still exists.',
			confirmLabel: 'Leave',
			cancelLabel: 'Cancel',
			danger: false
		});
		if (!confirmed) {
			return;
		}

		try {
			const leftRoomId = roomId;
			const res = await fetch(`${API_BASE}/api/rooms/leave`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					roomId: leftRoomId,
					userId: normalizeIdentifier(currentUserId)
				})
			});
			const data = await res.json().catch(() => ({}));
			if (!res.ok) {
				throw new Error(data.error || 'Failed to leave room');
			}
			syncServerClock(
				(data as { serverNow?: unknown; server_now?: unknown }).serverNow ??
					(data as { serverNow?: unknown; server_now?: unknown }).server_now
			);
			setMessageActionMode('none');
			showRoomDetails = false;
			showRoomSearch = false;

			roomThreads = roomThreads.filter((thread) => thread.id !== leftRoomId);
			const nextMessages = { ...messagesByRoom };
			delete nextMessages[leftRoomId];
			messagesByRoom = nextMessages;
			const nextOnline = { ...onlineByRoom };
			delete nextOnline[leftRoomId];
			onlineByRoom = nextOnline;
			const nextMeta = { ...roomMetaById };
			delete nextMeta[leftRoomId];
			roomMetaById = nextMeta;
			const nextTyping = { ...typingUsersByRoom };
			delete nextTyping[leftRoomId];
			typingUsersByRoom = nextTyping;
			const nextHistoryLoading = { ...historyLoadingByRoom };
			delete nextHistoryLoading[leftRoomId];
			historyLoadingByRoom = nextHistoryLoading;
			const nextHistoryHasMore = { ...historyHasMoreByRoom };
			delete nextHistoryHasMore[leftRoomId];
			historyHasMoreByRoom = nextHistoryHasMore;
			const nextUnreadAnchors = { ...unreadAnchorByRoom };
			delete nextUnreadAnchors[leftRoomId];
			unreadAnchorByRoom = nextUnreadAnchors;

			await refreshSidebarRooms();
			showErrorToast((data as { message?: string }).message || 'Room left');

			const fallbackJoined = roomThreads.find((thread) => thread.status === 'joined');
			if (fallbackJoined) {
				selectRoom(fallbackJoined.id, true);
			} else {
				await goto('/');
			}
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to leave room');
		}
	}

	async function onMessageSelected(event: CustomEvent<{ messageId: string }>) {
		if (!isSelectionMode || !roomId) {
			return;
		}
		const message = (messagesByRoom[roomId] ?? []).find(
			(entry) => entry.id === event.detail.messageId
		);
		if (!message) {
			return;
		}
		if (messageActionMode === 'break') {
			const created = await createBreakRoom(message);
			if (created) {
				setMessageActionMode('none');
			}
			return;
		}

		if (messageActionMode === 'pin') {
			const loweredType = (message.type || '').toLowerCase();
			if (
				loweredType === 'deleted' ||
				(message.content || '').trim() === DELETED_MESSAGE_PLACEHOLDER
			) {
				showErrorToast('Deleted messages cannot be pinned');
				return;
			}
			const pinned = await ensureMessagePinned(message);
			if (!pinned) {
				return;
			}
			openDiscussionForMessage(message.id);
			setMessageActionMode('none');
			return;
		}

		if (messageActionMode === 'edit' || messageActionMode === 'delete') {
			if (normalizeIdentifier(message.senderId) !== normalizeIdentifier(currentUserId)) {
				showErrorToast('You can only edit/delete your own messages');
				return;
			}
			const loweredType = (message.type || '').toLowerCase();
			if (messageActionMode === 'edit' && loweredType === 'task') {
				showErrorToast('Use the checklist inside the task card to update tasks');
				return;
			}
			if (
				loweredType === 'deleted' ||
				(message.content || '').trim() === DELETED_MESSAGE_PLACEHOLDER
			) {
				showErrorToast('Deleted messages cannot be selected');
				return;
			}
			if (messageActionMode === 'delete' && deleteMultiEnabled) {
				const normalizedMessageID = normalizeMessageID(message.id);
				if (!normalizedMessageID) {
					return;
				}
				if (selectedDeleteMessageIds.includes(normalizedMessageID)) {
					selectedDeleteMessageIds = selectedDeleteMessageIds.filter(
						(id) => id !== normalizedMessageID
					);
				} else {
					selectedDeleteMessageIds = [...selectedDeleteMessageIds, normalizedMessageID];
				}
				return;
			}
			selectedActionMessageId = message.id;
			return;
		}
	}

	function onPinnedDiscussionOpen(event: CustomEvent<{ messageId: string }>) {
		const messageId = normalizeMessageID(event.detail.messageId);
		if (!messageId) {
			return;
		}
		openDiscussionForMessage(messageId);
	}

	async function onSelectedMessageEdit(event: CustomEvent<{ messageId: string }>) {
		if (!roomId) {
			return;
		}
		const messageId = normalizeMessageID(event.detail.messageId);
		if (!messageId) {
			return;
		}
		const message = (messagesByRoom[roomId] ?? []).find((entry) => entry.id === messageId);
		if (!message) {
			return;
		}
		await onEditMessageRequest({
			detail: {
				messageId,
				content: message.content
			}
		} as CustomEvent<{ messageId: string; content: string }>);
		selectedActionMessageId = '';
	}

	async function onSelectedMessageDelete(event: CustomEvent<{ messageId: string }>) {
		await onDeleteMessageRequest(event);
		selectedActionMessageId = '';
	}

	async function createBreakRoom(message: ChatMessage) {
		const shouldProtectBreakRoom = await openConfirmDialog({
			title: 'Break Room Password',
			message:
				'Do you want to require a room password before others can preview and join this break room?',
			confirmLabel: 'Set Password',
			cancelLabel: 'No Password'
		});

		let breakRoomAccessPassword = '';
		if (shouldProtectBreakRoom) {
			const enteredPassword = await openRoomAccessPasswordDialog('');
			if (enteredPassword === null) {
				return false;
			}
			if (!enteredPassword) {
				showErrorToast('Room password cannot be empty');
				return false;
			}
			breakRoomAccessPassword = enteredPassword;
		}

		try {
			const res = await fetch(`${API_BASE}/api/rooms/break`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					parentRoomId: roomId,
					originMessageId: message.id,
					roomPassword: breakRoomAccessPassword,
					userId: normalizeIdentifier(currentUserId),
					username: currentUsername
				})
			});
			const data = await res.json();
			if (!res.ok) {
				throw new Error(data.error || 'Failed to create break room');
			}
			syncServerClock(
				(data as { serverNow?: unknown; server_now?: unknown }).serverNow ??
					(data as { serverNow?: unknown; server_now?: unknown }).server_now
			);

			const breakRoomId = normalizeRoomIDValue(toStringValue(data.roomId));
			if (!breakRoomId) {
				throw new Error('Invalid break room id');
			}
			const breakRoomName =
				normalizeRoomNameValue(toStringValue(data.roomName)) || formatRoomName(breakRoomId);
			const breakCreatedAt = toTimestamp(data.createdAt);
			const breakExpiresAt = parseOptionalTimestamp(data.expiresAt ?? data.expires_at);
			const breakParentRoomId =
				normalizeRoomIDValue(toStringValue(data.parentRoomId ?? data.parent_room_id)) || roomId;
			const breakOriginMessageId =
				normalizeMessageID(toStringValue(data.originMessageId ?? data.origin_message_id)) ||
				message.id;
			const breakTreeNumber = toInt(data.treeNumber ?? data.tree_number);
			const breakRequiresPassword = toBool(
				(data as { requiresPassword?: unknown; requires_password?: unknown }).requiresPassword ??
					(data as { requiresPassword?: unknown; requires_password?: unknown }).requires_password ??
					Boolean(breakRoomAccessPassword)
			);

			messagesByRoom = {
				...messagesByRoom,
				[roomId]: (messagesByRoom[roomId] ?? []).map((entry) =>
					entry.id === message.id
						? {
								...entry,
								hasBreakRoom: true,
								breakRoomId,
								breakJoinCount: Math.max(1, entry.breakJoinCount ?? 0),
								branchesCreated: Math.max(1, entry.branchesCreated ?? 0)
							}
						: entry
				)
			};
			ensureRoomThread(breakRoomId, breakRoomName, 'joined');
			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === breakRoomId
						? {
								...thread,
								status: 'joined',
								parentRoomId: breakParentRoomId || undefined,
								originMessageId: breakOriginMessageId || undefined,
								treeNumber: breakTreeNumber > 0 ? breakTreeNumber : (thread.treeNumber ?? 0),
								requiresPassword: breakRequiresPassword
							}
						: thread
				)
			);
			markRoomMembershipSynced(breakRoomId);
			ensureRoomMeta(breakRoomId, breakCreatedAt, breakExpiresAt);
			const params = new URLSearchParams({
				name: breakRoomName,
				member: '1'
			});
			const passwordHash = buildRoomPasswordHash($activeRoomPassword);
			await goto(`/chat/${encodeURIComponent(breakRoomId)}?${params.toString()}${passwordHash}`);
			return true;
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to create break room');
			return false;
		}
	}

	function onJoinBreakRoom(event: CustomEvent<{ roomId: string }>) {
		const target = normalizeRoomIDValue(event.detail.roomId);
		if (!target) {
			return;
		}
		const match = roomThreads.find((thread) => thread.id === target);
		if (!match) {
			ensureRoomThread(target, formatRoomName(target), 'discoverable');
			selectRoom(target, false);
			return;
		}
		selectRoom(target, match.status === 'joined');
	}

	function getRoomCreatedAt(targetRoomId: string) {
		return getRoomCreatedAtState(roomMetaById, targetRoomId);
	}

	function getRoomExpiry(targetRoomId: string) {
		return getRoomExpiryState(roomMetaById, targetRoomId);
	}
</script>

{#if showToast}
	<div class="toast" role="status" aria-live="polite">{toastMessage}</div>
{/if}

<ChatUiDialog
	dialog={uiDialog}
	{promptSubmitDisabled}
	{roomActionSubmitDisabled}
	on:close={closeUiDialog}
	on:confirm={onUiDialogConfirm}
	on:promptInput={(event) => updateUiPromptValue(event.detail.value)}
	on:roomModeChange={(event) => updateRoomActionMode(event.detail.mode)}
	on:roomNameInput={(event) => updateRoomActionName(event.detail.value)}
/>

<section
	class="chat-shell"
	class:theme-dark={$isDarkMode}
	class:mobile-list-only={isMobileView && mobilePane === 'list'}
	class:mobile-chat-only={isMobileView && mobilePane === 'chat'}
>
	<div class="sidebar-pane">
		<ChatSidebar
			myRooms={filteredMyRooms}
			discoverableRooms={filteredDiscoverableRooms}
			leftRooms={filteredLeftRooms}
			accessibleParentRoomIds={roomThreads.map((thread) => thread.id)}
			activeRoomId={roomId}
			{isMobileView}
			{showLeftMenu}
			isDarkMode={$isDarkMode}
			{themePreference}
			bind:chatListSearch
			on:select={onSidebarSelect}
			on:jumpOrigin={onJumpToBreakOrigin}
			on:toggleMenu={toggleLeftMenu}
			on:toggleTheme={toggleThemePreference}
			on:createRoom={createRoomFromMenu}
			on:renameRoom={(event) => void renameRoom(event.detail.roomId)}
		/>
	</div>

	<div
		class="room-workspace"
		class:canvas-open={isCanvasOpen}
		class:canvas-fullscreen={isCanvasOpen && isCanvasFullscreen}
	>
		{#if !isCanvasFullscreen}
			<section class="chat-window">
				<ChatRoomHeader
					roomName={activeThread.name}
					onlineCount={currentOnlineMembers.length}
					unreadCount={activeUnreadCount}
					{isMember}
					{isActiveRoomAdmin}
					{isMobileView}
					isDarkMode={$isDarkMode}
					{messageActionMode}
					{showRoomSearch}
					isBoardView={showBoardView}
					{isCanvasOpen}
					{lastWorkspaceTool}
					remainingLabel={activeRemainingLabel}
					on:showMobileList={showMobileRoomList}
					on:openRoomDetails={openRoomDetails}
					on:activateLastWorkspaceTool={activateLastWorkspaceTool}
					on:toggleBoardView={toggleBoardView}
					on:toggleCanvas={toggleCanvas}
					on:toggleRoomSearch={toggleRoomSearch}
					on:renameRoom={() => void renameRoom(roomId)}
					on:toggleBreakSelectionMode={toggleBreakSelectionMode}
					on:togglePinSelectionMode={togglePinSelectionMode}
					on:toggleEditSelectionMode={toggleEditSelectionMode}
					on:toggleDeleteSelectionMode={toggleDeleteSelectionMode}
					on:markRead={() => markRoomAsRead(roomId)}
					on:clearLocal={clearCurrentRoomMessages}
					on:leaveRoom={() => void leaveCurrentRoom()}
					on:deleteRoom={() => void deleteCurrentRoomAsAdmin()}
					on:disconnect={() => void disconnectAndWipe()}
				/>

				{#if !showBoardView}
					<ChatStatusBars
						{typingIndicatorText}
						{showTrustedDevicePrompt}
						{isSelectionMode}
						{messageActionMode}
						selectedDeleteCount={selectedDeleteMessageIds.length}
						{showRoomSearch}
						bind:roomMessageSearch
						isDarkMode={$isDarkMode}
						on:trustedChoice={(event) => onTrustedDeviceChoice(event.detail.choice)}
						on:cancelSelection={cancelSelectionMode}
						on:deleteSelected={deleteSelectedMessagesBatch}
					/>
				{/if}

				<div class="chat-window-shell" class:is-expired={isRoomExpired}>
					{#if showBoardView}
						<Board
							{roomId}
							messages={currentMessages}
							isDarkMode={$isDarkMode}
							canEdit={isMember && !isRoomExpired}
							{canModerateBoard}
							{currentUserId}
							{currentUsername}
						/>
					{:else}
						<ChatWindow
							bind:this={chatWindowRef}
							{roomId}
							isVisible={!isMobileView || mobilePane === 'chat'}
							messages={currentMessages}
							{currentUserId}
							unreadCount={activeUnreadCount}
							firstUnreadMessageId={activeFirstUnreadMessageId}
							lastReadTimestamp={activeLastReadTimestamp}
							{roomMessageSearch}
							{expandedMessages}
							{isMember}
							{isSelectionMode}
							isDarkMode={$isDarkMode}
							{messageActionMode}
							selectedMessageId={selectedActionMessageId}
							{deleteMultiEnabled}
							{selectedDeleteMessageIds}
							{focusMessageId}
							isLoadingOlder={isLoadingOlderHistory}
							hasMoreOlder={hasMoreOlderHistory}
							on:toggleExpand={(event) => toggleMessageExpanded(event.detail.messageId)}
							on:joinBreakRoom={onJoinBreakRoom}
							on:joinRoom={() => void joinCurrentRoom()}
							on:messageSelect={onMessageSelected}
							on:openPinnedDiscussion={onPinnedDiscussionOpen}
							on:reply={onReplyRequest}
							on:editMessage={onEditMessageRequest}
							on:deleteMessage={onDeleteMessageRequest}
							on:editSelected={onSelectedMessageEdit}
							on:deleteSelected={onSelectedMessageDelete}
							on:requestOlder={onRequestOlderHistory}
							on:focusHandled={onFocusHandled}
							on:readProgress={onChatReadProgress}
							on:toggleTask={onTaskToggle}
							on:addTask={onTaskAdd}
						/>
					{/if}
				</div>

				{#if isMember && !showBoardView}
					<ChatComposer
						bind:draftMessage
						bind:attachedFile
						{roomId}
						disabled={isRoomExpired}
						{activeReply}
						isDarkMode={$isDarkMode}
						{currentUsername}
						messageLimit={MESSAGE_TEXT_MAX_BYTES}
						on:send={(event) => void sendMessage(event.detail)}
						on:typing={onComposerTyping}
						on:attach={handleComposerAttach}
						on:removeAttachment={handleComposerRemoveAttachment}
						on:cancelReply={clearReplyTarget}
					/>
				{/if}
			</section>
		{/if}

		{#if isCanvasOpen}
			<section class="canvas-pane" class:fullscreen={isCanvasFullscreen}>
				<header class="canvas-pane-header">
					<span class="canvas-pane-title">Code Canvas</span>
					<div class="canvas-pane-actions">
						{#if isCanvasFullscreen}
							<button
								type="button"
								class="canvas-pane-icon-button"
								on:click={exitCanvasFullscreen}
								title="Back to split view"
								aria-label="Back to split view"
							>
								<svg viewBox="0 0 24 24" aria-hidden="true">
									<path d="M15.5 19.5 8 12l7.5-7.5" />
								</svg>
							</button>
							<button
								type="button"
								class="canvas-pane-icon-button"
								on:click={toggleCanvas}
								title="Minimize canvas"
								aria-label="Minimize canvas"
							>
								<svg viewBox="0 0 24 24" aria-hidden="true">
									<path d="M6 12h12" />
								</svg>
							</button>
						{:else}
							<button
								type="button"
								class="canvas-pane-icon-button"
								on:click={toggleCanvasFullscreen}
								title="Fullscreen canvas"
								aria-label="Fullscreen canvas"
							>
								<svg viewBox="0 0 24 24" aria-hidden="true">
									<path d="M9 4H4v5M15 4h5v5M9 20H4v-5M20 20h-5v-5" />
								</svg>
							</button>
							<button
								type="button"
								class="canvas-pane-icon-button"
								on:click={toggleCanvas}
								title="Minimize canvas"
								aria-label="Minimize canvas"
							>
								<svg viewBox="0 0 24 24" aria-hidden="true">
									<path d="M6 12h12" />
								</svg>
							</button>
						{/if}
					</div>
				</header>
				<div class="canvas-pane-body">
					<CodeCanvas {roomId} currentUser={canvasUser} />
				</div>
			</section>
		{/if}
	</div>

	<div class="online-pane">
		<OnlinePanel members={currentOnlineMembers} isDarkMode={$isDarkMode} />
	</div>
</section>

<DiscussionModal
	open={isDiscussionOpen}
	pinnedMessage={activeDiscussionTask}
	comments={discussionComments}
	{roomId}
	isDarkMode={$isDarkMode}
	canEditTask={isMember}
	{currentUserId}
	opUserId={activeDiscussionTask?.senderId || ''}
	backgroundUnreadCount={discussionBackgroundUnreadCount}
	on:close={closeDiscussion}
	on:navigatePrevious={() => void navigateDiscussionPins('previous')}
	on:navigateNext={() => void navigateDiscussionPins('next')}
	on:toggleTask={onTaskToggle}
	on:addTask={onTaskAdd}
	on:submitComment={onDiscussionCommentSubmit}
	on:editComment={onDiscussionCommentEditRequest}
	on:deleteComment={onDiscussionCommentDeleteRequest}
	on:toggleCommentPin={onDiscussionCommentPinToggle}
/>

<ChatRoomDetailsPanel
	show={showRoomDetails}
	{isMobileView}
	{roomId}
	roomName={activeThread.name}
	roomAdminCode={activeThread.adminCode || ''}
	createdLabel={formatDateTime(activeRoomCreatedAtMs)}
	expiresLabel={formatDateTime(activeRoomExpiresAtMs)}
	{isExtendingRoom}
	{currentOnlineMembers}
	{isActiveRoomAdmin}
	{currentUserId}
	{formatDateTime}
	on:close={closeRoomDetails}
	on:extend={requestRoomExtension}
	on:removeMember={(event) => void removeMemberFromRoom(event.detail.memberId)}
	on:promoted={(event) => void onRoomPromoted(event)}
/>
