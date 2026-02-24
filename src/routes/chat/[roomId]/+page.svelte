<script lang="ts">
	import { browser } from '$app/environment';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import ChatComposer from '$lib/components/chat/ChatComposer.svelte';
	import DiscussionModal from '$lib/components/chat/DiscussionModal.svelte';
	import ChatRoomDetailsPanel from '$lib/components/chat/ChatRoomDetailsPanel.svelte';
	import ChatRoomHeader from '$lib/components/chat/ChatRoomHeader.svelte';
	import ChatStatusBars from '$lib/components/chat/ChatStatusBars.svelte';
	import ChatSidebar from '$lib/components/chat/ChatSidebar.svelte';
	import ChatUiDialog from '$lib/components/chat/ChatUiDialog.svelte';
	import ChatWindow from '$lib/components/chat/ChatWindow.svelte';
	import OnlinePanel from '$lib/components/chat/OnlinePanel.svelte';
	import { authToken, currentUser } from '$lib/store';
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
		parseTimestampParam,
		resolveRoomMembership,
		toBool,
		toInt,
		toStringValue,
		toTimestamp
	} from '$lib/utils/chat/core';
	import {
		addTaskItem,
		parseTaskMessagePayload,
		stringifyTaskMessagePayload,
		toggleTaskItem
	} from '$lib/utils/chat/task';
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
	import {
		buildConfirmDialog,
		buildPromptDialog,
		buildRoomActionDialog,
		resolveCloseDialogValue,
		resolveConfirmDialogValue,
		updatePromptDialogValue,
		updateRoomActionDialogMode,
		updateRoomActionDialogName
	} from '$lib/utils/chat/dialogState';
	import {
		getTrustedDevicePreference,
		isOfflineCacheSupported,
		loadEncryptedRoomMessages,
		saveEncryptedRoomMessages,
		setTrustedDevicePreference,
		wipeEncryptedRoomCache,
		type TrustedDevicePreference
	} from '$lib/utils/offlineCache';
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

	const CLIENT_LOG_PREFIX = '[chat-client]';
	const API_BASE = (import.meta.env.VITE_API_BASE as string | undefined) ?? 'http://localhost:8080';
	const CLIENT_DEBUG = (import.meta.env.VITE_CHAT_DEBUG as string | undefined) === '1';
	const TYPING_PING_INTERVAL_MS = 5000;
	const TYPING_STOP_DELAY_MS = 5000;
	const TYPING_SAFETY_TIMEOUT_MS = 7000;
	const DISCUSSION_MAX_REPLY_DEPTH = 4;
	const THEME_PREFERENCE_KEY = 'converse_theme_preference';

	let sidebarRefreshTimer: ReturnType<typeof setInterval> | null = null;
	let roomExpiryTicker: ReturnType<typeof setInterval> | null = null;
	let systemThemeMediaQuery: MediaQueryList | null = null;
	let removeSystemThemeListener: (() => void) | null = null;
	let roomMembershipSynced: Record<string, boolean> = {};
	let roomMembershipSyncing: Record<string, boolean> = {};
	let typingStopTimer: ReturnType<typeof setTimeout> | null = null;
	let typingLastPingAt = 0;
	let typingIsActive = false;
	let typingSafetyTimers = new Map<string, ReturnType<typeof setTimeout>>();
	let cachePersistTimers = new Map<string, ReturnType<typeof setTimeout>>();

	let toastMessage = '';
	let showToast = false;
	let toastTimer: ReturnType<typeof setTimeout> | null = null;
	let lastToastRoom = '';

	let chatListSearch = '';
	let roomMessageSearch = '';
	let draftMessage = '';
	let attachedFile: File | null = null;
	let showLeftMenu = false;
	let showRoomSearch = false;
	let showRoomDetails = false;
	let themePreference: ThemePreference = 'system';
	let isDarkMode = false;
	let isSelectionMode = false;
	let messageActionMode: MessageActionMode = 'none';
	let selectedActionMessageId = '';
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
	let discussionBackgroundUnreadCount = 0;
	let discussionOpenedAtMs = 0;
	let discussionTaskTracker = '';
	let identityReady = !browser;
	let roomExpiryTickMs = Date.now();
	let serverClockOffsetMs = 0;
	let serverNowAnchorMs = 0;
	let serverNowAnchorPerfMs = 0;
	let uiDialog: UiDialogState = { kind: 'none' };
	let uiDialogResolver: ((value: unknown) => void) | null = null;
	let chatWindowRef: {
		capturePrependAnchor?: () => { scrollTop: number; scrollHeight: number } | null;
		restorePrependAnchor?: (anchor: { scrollTop: number; scrollHeight: number } | null) => void;
	} | null = null;

	$: roomId = normalizeRoomIDValue(decodeURIComponent($page.params.roomId ?? ''));
	$: activeRoomId = roomId;
	$: roomNameFromURL = normalizeRoomNameValue(
		decodeURIComponent($page.url.searchParams.get('name') ?? '').trim()
	);
	$: roomCreatedAtFromURL = parseTimestampParam($page.url.searchParams.get('createdAt'));
	$: roomExpiresAtFromURL = parseTimestampParam($page.url.searchParams.get('expiresAt'));
	$: focusMessageIdFromURL = normalizeMessageID($page.url.searchParams.get('focusMsg') ?? '');
	$: roomMemberHint = $page.url.searchParams.get('member');
	$: currentUserId = $currentUser?.id ?? 'guest';
	$: currentUsername = normalizeUsernameValue($currentUser?.username ?? 'Guest') || 'Guest';
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
		discussionTaskTracker = '';
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
					if (
						normalizeIdentifier(comment.senderId) === normalizeIdentifier(currentUserId)
					) {
						return false;
					}
					return comment.createdAt > discussionOpenedAtMs;
				}).length
			: 0;
	$: currentOnlineMembers = onlineByRoom[roomId] ?? [];
	$: isActiveRoomAdmin = Boolean(activeThread?.isAdmin);
	$: isMember = resolveRoomMembership(roomId, roomThreads, roomMemberHint);
	$: activeUnreadCount = activeThread?.unread ?? 0;
	$: activeFirstUnreadMessageId = getUnreadStartMessageId(roomId);
	$: activeLastReadTimestamp = getLastReadTimestamp(roomId);
	$: activeTypingUsers = getActiveTypingUsers(roomId);
	$: typingIndicatorText = formatTypingIndicator(activeTypingUsers);
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
		ensureRoomMeta(roomId, roomCreatedAtFromURL, roomExpiresAtFromURL);
	}
	$: if (browser && identityReady && roomId && isMember) {
		void syncRoomMembership(roomId);
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
				const directMessage = parseIncomingMessage(source, payloadRoomID, API_BASE);
				if (directMessage) {
					addIncomingMessage(directMessage);
				}
				handledDirectPayload = true;
			}
		}
		if (!handledDirectPayload) {
			handleGlobalPayload(payload);
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
	$: promptSubmitDisabled = uiDialog.kind === 'prompt' ? uiDialog.value.trim() === '' : false;

	onDestroy(() => {
		clientLog('component-destroy', { roomId });
		clearTypingStopTimer();
		clearAllTypingSafetyTimers();
		clearAllCachePersistTimers();
		clearSidebarRefreshTimer();
		clearRoomExpiryTicker();
		clearToastTimer();
		if (removeSystemThemeListener) {
			removeSystemThemeListener();
			removeSystemThemeListener = null;
		}
		systemThemeMediaQuery = null;
	});

	onMount(() => {
		if (!browser) {
			return;
		}
		initializeThemePreference();
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

	$: if (browser) {
		document.body.classList.toggle('theme-dark', isDarkMode);
		document.body.dataset.theme = isDarkMode ? 'dark' : 'light';
	}

	function initializeThemePreference() {
		if (!browser) {
			return;
		}
		systemThemeMediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
		registerSystemThemeListener();
		const saved = window.localStorage.getItem(THEME_PREFERENCE_KEY);
		if (saved === 'dark' || saved === 'light' || saved === 'system') {
			applyThemePreference(saved, false);
			return;
		}
		applyThemePreference('system', false);
	}

	function registerSystemThemeListener() {
		if (!systemThemeMediaQuery || removeSystemThemeListener) {
			return;
		}
		const onSystemThemeChange = () => {
			if (themePreference !== 'system') {
				return;
			}
			isDarkMode = Boolean(systemThemeMediaQuery?.matches);
		};
		if (typeof systemThemeMediaQuery.addEventListener === 'function') {
			systemThemeMediaQuery.addEventListener('change', onSystemThemeChange);
			removeSystemThemeListener = () => {
				systemThemeMediaQuery?.removeEventListener('change', onSystemThemeChange);
			};
			return;
		}
		systemThemeMediaQuery.addListener(onSystemThemeChange);
		removeSystemThemeListener = () => {
			systemThemeMediaQuery?.removeListener(onSystemThemeChange);
		};
	}

	function resolveDarkMode(preference: ThemePreference) {
		if (preference === 'dark') {
			return true;
		}
		if (preference === 'light') {
			return false;
		}
		return Boolean(systemThemeMediaQuery?.matches);
	}

	function applyThemePreference(preference: ThemePreference, persist = true) {
		themePreference = preference;
		isDarkMode = resolveDarkMode(preference);
		if (browser && persist) {
			window.localStorage.setItem(THEME_PREFERENCE_KEY, preference);
		}
	}

	function toggleThemePreference() {
		// Toggle dark/light while system remains the default for first-time users.
		const next: ThemePreference = isDarkMode ? 'light' : 'dark';
		applyThemePreference(next);
		showLeftMenu = false;
	}

	function updateViewportMode() {
		if (!browser) {
			return;
		}
		isMobileView = window.innerWidth <= 900;
		if (!isMobileView) {
			mobilePane = 'chat';
		}
	}

	function clearTypingStopTimer() {
		if (typingStopTimer) {
			clearTimeout(typingStopTimer);
			typingStopTimer = null;
		}
	}

	function sendTypingStart() {
		if (!roomId || !isMember) {
			return;
		}
		const now = Date.now();
		if (typingIsActive && now - typingLastPingAt < TYPING_PING_INTERVAL_MS) {
			return;
		}
		typingIsActive = true;
		typingLastPingAt = now;
		sendSocketPayload({
			type: 'typing_start',
			roomId
		});
	}

	function sendTypingStop() {
		if (!typingIsActive || !roomId || !isMember) {
			clearTypingStopTimer();
			typingIsActive = false;
			return;
		}
		typingIsActive = false;
		typingLastPingAt = 0;
		clearTypingStopTimer();
		sendSocketPayload({
			type: 'typing_stop',
			roomId
		});
	}

	function scheduleTypingStop() {
		clearTypingStopTimer();
		typingStopTimer = setTimeout(() => {
			sendTypingStop();
		}, TYPING_STOP_DELAY_MS);
	}

	function onComposerTyping(event: CustomEvent<{ value: string }>) {
		const value = (event.detail?.value || '').trim();
		if (!value) {
			sendTypingStop();
			return;
		}
		sendTypingStart();
		scheduleTypingStop();
	}

	function clearAllTypingSafetyTimers() {
		for (const timer of typingSafetyTimers.values()) {
			clearTimeout(timer);
		}
		typingSafetyTimers = new Map<string, ReturnType<typeof setTimeout>>();
	}

	function typingTimerKey(targetRoomId: string, userId: string) {
		return `${targetRoomId}:${userId}`;
	}

	function setTypingIndicator(
		targetRoomId: string,
		userId: string,
		userName: string,
		expiresAt: number = Date.now() + TYPING_SAFETY_TIMEOUT_MS
	) {
		if (!targetRoomId || !userId) {
			return;
		}
		const roomIndicators = typingUsersByRoom[targetRoomId] ?? {};
		typingUsersByRoom = {
			...typingUsersByRoom,
			[targetRoomId]: {
				...roomIndicators,
				[userId]: {
					name: userName || 'User',
					expiresAt
				}
			}
		};

		const key = typingTimerKey(targetRoomId, userId);
		const existing = typingSafetyTimers.get(key);
		if (existing) {
			clearTimeout(existing);
		}
		const timer = setTimeout(() => {
			clearTypingIndicator(targetRoomId, userId);
		}, TYPING_SAFETY_TIMEOUT_MS);
		typingSafetyTimers.set(key, timer);
	}

	function clearTypingIndicator(targetRoomId: string, userId: string) {
		if (!targetRoomId || !userId) {
			return;
		}
		const roomIndicators = typingUsersByRoom[targetRoomId];
		if (!roomIndicators || !roomIndicators[userId]) {
			return;
		}

		const nextRoomIndicators = { ...roomIndicators };
		delete nextRoomIndicators[userId];
		const nextTypingByRoom = { ...typingUsersByRoom };
		if (Object.keys(nextRoomIndicators).length === 0) {
			delete nextTypingByRoom[targetRoomId];
		} else {
			nextTypingByRoom[targetRoomId] = nextRoomIndicators;
		}
		typingUsersByRoom = nextTypingByRoom;

		const key = typingTimerKey(targetRoomId, userId);
		const existing = typingSafetyTimers.get(key);
		if (existing) {
			clearTimeout(existing);
			typingSafetyTimers.delete(key);
		}
	}

	function getActiveTypingUsers(targetRoomId: string) {
		if (!targetRoomId) {
			return [];
		}
		const roomIndicators = typingUsersByRoom[targetRoomId] ?? {};
		const now = Date.now();
		const active = Object.entries(roomIndicators)
			.filter(([userId, entry]) => {
				if (!entry || entry.expiresAt <= now) {
					clearTypingIndicator(targetRoomId, userId);
					return false;
				}
				return normalizeIdentifier(userId) !== normalizeIdentifier(currentUserId);
			})
			.map(([, entry]) => entry.name);
		return active;
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
		const hydrated = cached
			.map((entry) => parseIncomingMessage(entry, targetRoomId, API_BASE))
			.filter((entry): entry is ChatMessage => Boolean(entry));
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

			ensureRoomThread(joinedRoomId, joinedName, 'joined');
			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === joinedRoomId
						? { ...thread, status: 'joined', name: joinedName, isAdmin: joinedIsAdmin }
						: thread
				)
			);
			markRoomMembershipSynced(joinedRoomId);
			ensureRoomMeta(joinedRoomId, joinedCreatedAt, joinedExpiresAt);
			ensureOnlineSeed(joinedRoomId);

			const params = new URLSearchParams($page.url.searchParams.toString());
			params.set('member', '1');
			params.set('name', joinedName);
			if (joinedCreatedAt > 0) {
				params.set('createdAt', String(joinedCreatedAt));
			}
			if (joinedExpiresAt > 0) {
				params.set('expiresAt', String(joinedExpiresAt));
			}
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

	function resolveActiveUiDialog(value: unknown) {
		const resolver = uiDialogResolver;
		uiDialogResolver = null;
		uiDialog = { kind: 'none' };
		if (resolver) {
			resolver(value);
		}
	}

	function closeUiDialog() {
		resolveActiveUiDialog(resolveCloseDialogValue(uiDialog));
	}

	function openConfirmDialog(config: {
		title: string;
		message: string;
		confirmLabel?: string;
		cancelLabel?: string;
		danger?: boolean;
	}) {
		resolveActiveUiDialog(false);
		uiDialog = buildConfirmDialog(config);
		return new Promise<boolean>((resolve) => {
			uiDialogResolver = (value) => resolve(Boolean(value));
		});
	}

	function openPromptDialog(config: {
		title: string;
		message: string;
		initialValue?: string;
		placeholder?: string;
		maxLength?: number;
		confirmLabel?: string;
		cancelLabel?: string;
		danger?: boolean;
		multiline?: boolean;
	}) {
		resolveActiveUiDialog(null);
		uiDialog = buildPromptDialog(config);
		return new Promise<string | null>((resolve) => {
			uiDialogResolver = (value) => {
				if (typeof value === 'string') {
					resolve(value);
					return;
				}
				resolve(null);
			};
		});
	}

	function openRoomActionDialog(initialName = '') {
		resolveActiveUiDialog(null);
		uiDialog = buildRoomActionDialog(initialName, normalizeRoomNameValue);
		return new Promise<{ mode: RoomMenuMode; roomName: string } | null>((resolve) => {
			uiDialogResolver = (value) => {
				if (
					value &&
					typeof value === 'object' &&
					'mode' in value &&
					'roomName' in value &&
					typeof (value as { mode?: unknown }).mode === 'string'
				) {
					const parsed = value as { mode: RoomMenuMode; roomName: string };
					resolve(parsed);
					return;
				}
				resolve(null);
			};
		});
	}

	function onUiDialogConfirm() {
		resolveActiveUiDialog(resolveConfirmDialogValue(uiDialog));
	}

	function updateUiPromptValue(value: string) {
		uiDialog = updatePromptDialogValue(uiDialog, value);
	}

	function updateRoomActionMode(mode: RoomMenuMode) {
		uiDialog = updateRoomActionDialogMode(uiDialog, mode);
	}

	function updateRoomActionName(value: string) {
		uiDialog = updateRoomActionDialogName(uiDialog, value);
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
		const knownExpiry = roomMetaById[normalizedRoomId]?.expiresAt ?? 0;
		if (
			(roomMembershipSynced[normalizedRoomId] && knownExpiry > 0) ||
			roomMembershipSyncing[normalizedRoomId]
		) {
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
			ensureRoomThread(normalizedRoomId, joinedName, 'joined');
			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === normalizedRoomId ? { ...thread, isAdmin: joinedIsAdmin } : thread
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
				const createdAt = normalizeEpoch(Number(room.createdAt ?? 0));
				const expiresAt = parseOptionalTimestamp(room.expiresAt);
				if (createdAt > 0 || expiresAt > 0) {
					ensureRoomMeta(roomID, createdAt, expiresAt);
				}

				const roomStatus: ThreadStatus =
					room.status === 'joined' ? 'joined' : room.status === 'left' ? 'left' : 'discoverable';

				const next: ChatThread = {
					id: roomID,
					name:
						normalizeRoomNameValue(toStringValue(room.roomName)) ||
						prev?.name ||
						formatRoomName(roomID),
					lastMessage: prev?.lastMessage || '',
					lastActivity: prev?.lastActivity || createdAt || Date.now(),
					unread: prev?.unread || 0,
					status: roomStatus,
					memberCount: typeof room.memberCount === 'number' ? room.memberCount : prev?.memberCount,
					parentRoomId: toStringValue(room.parentRoomId) || prev?.parentRoomId || undefined,
					originMessageId:
						toStringValue(room.originMessageId) || prev?.originMessageId || undefined,
					treeNumber: toInt(room.treeNumber ?? prev?.treeNumber ?? 0),
					isAdmin: toBool(room.isAdmin ?? prev?.isAdmin ?? false)
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
		const createdAt = getRoomCreatedAt(normalizedTargetRoomId);
		if (createdAt > 0) {
			params.set('createdAt', String(createdAt));
		}
		const expiresAt = roomMetaById[normalizedTargetRoomId]?.expiresAt ?? 0;
		if (expiresAt > 0) {
			params.set('expiresAt', String(expiresAt));
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

	function handleGlobalPayload(payload: unknown) {
		if (Array.isArray(payload)) {
			const parsedMessages = payload
				.map((entry) => parseIncomingMessage(entry, '', API_BASE))
				.filter((entry): entry is ChatMessage => Boolean(entry));
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
			handleEnvelope(payload);
			return;
		}

		const single = parseIncomingMessage(payload, '', API_BASE);
		if (single) {
			addIncomingMessage(single);
		}
	}

	function isEnvelope(value: unknown): value is SocketEnvelope {
		return Boolean(
			value &&
			typeof value === 'object' &&
			'type' in value &&
			'payload' in value &&
			typeof (value as { type?: unknown }).type === 'string'
		);
	}

	function resolveEnvelopeRoomID(envelope: SocketEnvelope) {
		const directRoomID = normalizeRoomIDValue(toStringValue(envelope.roomId ?? envelope.room_id));
		if (directRoomID) {
			return directRoomID;
		}
		if (envelope.payload && typeof envelope.payload === 'object') {
			const payload = envelope.payload as Record<string, unknown>;
			return normalizeRoomIDValue(toStringValue(payload.roomId ?? payload.room_id));
		}
		return '';
	}

	function resolveDiscussionPinMessageID(envelope: SocketEnvelope) {
		const source = envelope as Record<string, unknown>;
		const directPinID = normalizeMessageID(
			toStringValue(source.pinMessageId ?? source.pin_message_id)
		);
		if (directPinID) {
			return directPinID;
		}
		if (envelope.payload && typeof envelope.payload === 'object') {
			const payload = envelope.payload as Record<string, unknown>;
			const payloadPinID = normalizeMessageID(
				toStringValue(
					payload.pinMessageId ??
						payload.pin_message_id ??
						payload.replyToMessageId ??
						payload.reply_to_message_id
				)
			);
			if (payloadPinID) {
				return payloadPinID;
			}
		}
		return '';
	}

	function handleDiscussionCommentEnvelope(envelope: SocketEnvelope, targetRoomId: string) {
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
		const comment = parseIncomingMessage(envelope.payload, targetRoomID, API_BASE);
		if (!comment) {
			return;
		}
		upsertDiscussionCommentLocal(comment);
	}

	function handleEnvelope(envelope: SocketEnvelope) {
		const targetRoomId = resolveEnvelopeRoomID(envelope);
		const kind = envelope.type;
		if (kind === 'history' || kind === 'recent_messages' || kind === 'initial_messages') {
			if (Array.isArray(envelope.payload)) {
				const history = envelope.payload
					.map((entry) => parseIncomingMessage(entry, targetRoomId, API_BASE))
					.filter((entry): entry is ChatMessage => Boolean(entry));
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
			const message = parseIncomingMessage(envelope.payload, targetRoomId, API_BASE);
			if (message) {
				addIncomingMessage(message);
			}
			return;
		}

		if (kind === 'discussion_comment' && targetRoomId) {
			handleDiscussionCommentEnvelope(envelope, targetRoomId);
			return;
		}

		if (kind === 'room_expired') {
			const payloadRoomId =
				envelope.payload && typeof envelope.payload === 'object'
					? normalizeRoomIDValue(
							toStringValue(
								(envelope.payload as Record<string, unknown>).roomId ??
									(envelope.payload as Record<string, unknown>).room_id
							)
						)
					: '';
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
			applyMessageEdit(targetRoomId, envelope.payload);
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
		const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
		if (!normalizedRoomID) {
			return Date.now();
		}
		const roomMessages = messagesByRoom[normalizedRoomID] ?? [];
		if (roomMessages.length === 0) {
			return Date.now();
		}
		const unread = roomThreads.find((thread) => thread.id === normalizedRoomID)?.unread ?? 0;
		if (unread <= 0) {
			return roomMessages[roomMessages.length - 1]?.createdAt ?? Date.now();
		}
		const anchorMessageID = getUnreadStartMessageId(normalizedRoomID);
		if (anchorMessageID) {
			const anchorIndex = roomMessages.findIndex(
				(message) => normalizeMessageID(message.id) === normalizeMessageID(anchorMessageID)
			);
			if (anchorIndex <= 0) {
				return 0;
			}
			return roomMessages[anchorIndex - 1]?.createdAt ?? 0;
		}
		const firstUnreadIndex = Math.max(0, roomMessages.length - unread);
		if (firstUnreadIndex <= 0) {
			return 0;
		}
		return roomMessages[firstUnreadIndex - 1]?.createdAt ?? 0;
	}

	function findUnreadAnchorFromTail(
		roomMessages: ChatMessage[],
		unreadCount: number,
		normalizedCurrentUserID: string
	) {
		if (!Array.isArray(roomMessages) || roomMessages.length === 0 || unreadCount <= 0) {
			return '';
		}
		let remainingUnread = unreadCount;
		for (let index = roomMessages.length - 1; index >= 0; index -= 1) {
			const candidate = roomMessages[index];
			const isOwnMessage =
				normalizedCurrentUserID !== '' &&
				normalizeIdentifier(candidate.senderId) === normalizedCurrentUserID;
			if (isOwnMessage) {
				continue;
			}
			remainingUnread -= 1;
			if (remainingUnread <= 0) {
				return candidate.id;
			}
		}
		return roomMessages[0]?.id ?? '';
	}

	function getUnreadStartMessageId(targetRoomId: string) {
		const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
		if (!normalizedRoomID) {
			return '';
		}
		const unread = roomThreads.find((thread) => thread.id === normalizedRoomID)?.unread ?? 0;
		if (unread <= 0) {
			return '';
		}
		const roomMessages = messagesByRoom[normalizedRoomID] ?? [];
		if (roomMessages.length === 0) {
			return '';
		}
		const normalizedCurrentUserID = normalizeIdentifier(currentUserId);
		return findUnreadAnchorFromTail(roomMessages, unread, normalizedCurrentUserID);
	}

	function applyReadProgress(targetRoomId: string, lastSeenMessageId: string) {
		const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
		if (!normalizedRoomID) {
			return;
		}
		const thread = roomThreads.find((entry) => entry.id === normalizedRoomID);
		const unread = thread?.unread ?? 0;
		if (unread <= 0) {
			return;
		}

		const roomMessages = messagesByRoom[normalizedRoomID] ?? [];
		if (roomMessages.length === 0) {
			return;
		}

		const anchorMessageID = getUnreadStartMessageId(normalizedRoomID);
		if (!anchorMessageID) {
			return;
		}
		const anchorIndex = roomMessages.findIndex(
			(message) => normalizeMessageID(message.id) === normalizeMessageID(anchorMessageID)
		);
		if (anchorIndex < 0) {
			return;
		}

		const seenIndex = roomMessages.findIndex(
			(message) => normalizeMessageID(message.id) === normalizeMessageID(lastSeenMessageId)
		);
		if (seenIndex < anchorIndex) {
			return;
		}
		const normalizedCurrentUserID = normalizeIdentifier(currentUserId);
		let seenUnreadCount = 0;
		for (let index = anchorIndex; index <= seenIndex; index += 1) {
			const candidate = roomMessages[index];
			const isOwnMessage =
				normalizedCurrentUserID !== '' &&
				normalizeIdentifier(candidate.senderId) === normalizedCurrentUserID;
			if (!isOwnMessage) {
				seenUnreadCount += 1;
			}
		}
		const seenCount = Math.min(unread, seenUnreadCount);
		if (seenCount <= 0) {
			return;
		}

		const nextUnread = Math.max(0, unread - seenCount);
		roomThreads = sortThreads(
			roomThreads.map((entry) =>
				entry.id === normalizedRoomID ? { ...entry, unread: nextUnread } : entry
			)
		);

		if (nextUnread <= 0) {
			const nextUnreadAnchors = { ...unreadAnchorByRoom };
			delete nextUnreadAnchors[normalizedRoomID];
			unreadAnchorByRoom = nextUnreadAnchors;
			return;
		}

		const nextAnchorMessageId = findUnreadAnchorFromTail(
			roomMessages,
			nextUnread,
			normalizedCurrentUserID
		);
		if (nextAnchorMessageId) {
			unreadAnchorByRoom = {
				...unreadAnchorByRoom,
				[normalizedRoomID]: nextAnchorMessageId
			};
		}
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
		sendSocketPayload(toWireMessage(outgoing));
		applyReadProgress(roomId, outgoing.id);
		sendTypingStop();
		draftMessage = '';
		attachedFile = null;
		activeReply = null;
	}

	function roomPinsEndpoint() {
		return `${API_BASE}/api/rooms/${encodeURIComponent(roomId)}/pins`;
	}

	function discussionCommentsEndpoint(pinnedMessageId: string) {
		const normalizedPinnedMessageID = normalizeMessageID(pinnedMessageId);
		return `${API_BASE}/api/rooms/${encodeURIComponent(roomId)}/pins/${encodeURIComponent(normalizedPinnedMessageID)}/discussion/comments`;
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
			const res = await fetch(roomPinsEndpoint(), {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					userId: normalizedUserID,
					messageId: normalizedMessageID
				})
			});
			const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
			if (!res.ok) {
				throw new Error(toStringValue(data.error) || 'Failed to pin message');
			}
			const normalizedTargetID = normalizeMessageID(message.id);
			if (normalizedTargetID) {
				const nextMessages = (messagesByRoom[roomId] ?? []).map((entry) => {
					if (normalizeMessageID(entry.id) !== normalizedTargetID) {
						return entry;
					}
					return {
						...entry,
						isPinned: true
					};
				});
				messagesByRoom = {
					...messagesByRoom,
					[roomId]: nextMessages
				};
			}
			return true;
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to pin message');
			return false;
		}
	}

	function upsertDiscussionCommentLocal(comment: ChatMessage) {
		const normalizedID = normalizeMessageID(comment.id);
		if (!normalizedID) {
			return;
		}
		const next = [
			...discussionComments.filter((entry) => normalizeMessageID(entry.id) !== normalizedID),
			comment
		].sort((left, right) => left.createdAt - right.createdAt);
		discussionComments = next;
	}

	async function loadDiscussionComments(pinnedMessageId: string) {
		if (!roomId || !isMember) {
			discussionComments = [];
			return;
		}
		const normalizedPinnedMessageID = normalizeMessageID(pinnedMessageId);
		const normalizedUserID = normalizeIdentifier(currentUserId);
		if (!normalizedPinnedMessageID || !normalizedUserID) {
			discussionComments = [];
			return;
		}

		const requestURL = `${discussionCommentsEndpoint(normalizedPinnedMessageID)}?userId=${encodeURIComponent(normalizedUserID)}&limit=50`;
		try {
			const res = await fetch(requestURL);
			const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
			if (!res.ok) {
				throw new Error(toStringValue(data.error) || 'Failed to load discussion comments');
			}
			const parsedComments = (Array.isArray(data.comments) ? data.comments : [])
				.map((entry) => parseIncomingMessage(entry, roomId, API_BASE))
				.filter((entry): entry is ChatMessage => Boolean(entry))
				.sort((left, right) => left.createdAt - right.createdAt);

			if (normalizeMessageID(activeDiscussionTaskId) !== normalizedPinnedMessageID) {
				return;
			}
			discussionComments = parsedComments;
		} catch (error) {
			if (normalizeMessageID(activeDiscussionTaskId) === normalizedPinnedMessageID) {
				discussionComments = [];
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
		discussionComments = [];
		void loadDiscussionComments(match.id);
	}

	function closeDiscussion() {
		isDiscussionOpen = false;
		activeDiscussionTaskId = '';
		discussionOpenedAtMs = 0;
		discussionComments = [];
	}

	function commitTaskPayloadUpdate(messageId: string, nextContent: string) {
		if (!roomId || !messageId || !nextContent) {
			return;
		}
		applyMessageEdit(roomId, {
			messageId,
			content: nextContent,
			editedAt: Date.now(),
			messageType: 'task'
		});
		sendSocketPayload({
			type: 'message_edit',
			roomId,
			messageId,
			content: nextContent,
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
		commitTaskPayloadUpdate(messageId, nextContent);
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
		commitTaskPayloadUpdate(messageId, nextContent);
	}

	function buildDiscussionCommentMap(items: ChatMessage[]) {
		const map = new Map<string, ChatMessage>();
		for (const item of items) {
			const normalizedId = normalizeMessageID(item.id);
			if (!normalizedId) {
				continue;
			}
			map.set(normalizedId, item);
		}
		return map;
	}

	function resolveDiscussionCommentDepth(commentId: string, commentMap: Map<string, ChatMessage>) {
		let depth = 1;
		let currentId = normalizeMessageID(commentId);
		const seen = new Set<string>();
		while (currentId && commentMap.has(currentId) && depth <= DISCUSSION_MAX_REPLY_DEPTH + 2) {
			if (seen.has(currentId)) {
				break;
			}
			seen.add(currentId);
			const parentId = normalizeMessageID(commentMap.get(currentId)?.replyToMessageId || '');
			if (!parentId || !commentMap.has(parentId)) {
				break;
			}
			depth += 1;
			currentId = parentId;
		}
		return depth;
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
		const allowedReplyIDs = new Set<string>(discussionComments.map((entry) => normalizeMessageID(entry.id)));
		const parentCommentId =
			requestedReplyID && allowedReplyIDs.has(requestedReplyID) ? requestedReplyID : '';
		if (parentCommentId) {
			const discussionCommentMap = buildDiscussionCommentMap(discussionComments);
			const parentDepth = resolveDiscussionCommentDepth(parentCommentId, discussionCommentMap);
			if (parentDepth >= DISCUSSION_MAX_REPLY_DEPTH) {
				showErrorToast('Reply nesting limit reached (max 4 levels)');
				return;
			}
		}

		const normalizedTaskID = normalizeMessageID(activeDiscussionTask.id);
		if (!normalizedTaskID) {
			return;
		}

		const queued = sendSocketPayload({
			type: 'discussion_comment',
			roomId,
			pinMessageId: normalizedTaskID,
			parentCommentId,
			content
		});
		if (!queued) {
			showErrorToast('Socket reconnecting. Comment queued.');
		}
	}

	async function onDiscussionCommentEditRequest(
		event: CustomEvent<{ messageId: string; content: string }>
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

		const nextContentRaw = await openPromptDialog({
			title: 'Edit Comment',
			message: 'Update your discussion comment.',
			initialValue: (event.detail.content || '').trim(),
			placeholder: 'Comment',
			maxLength: 2000,
			confirmLabel: 'Save',
			cancelLabel: 'Cancel',
			multiline: true
		});
		if (nextContentRaw === null) {
			return;
		}
		const nextContent = nextContentRaw.trim();
		if (!nextContent || nextContent === (event.detail.content || '').trim()) {
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
			const res = await fetch(
				`${discussionCommentsEndpoint(normalizedTaskID)}/${encodeURIComponent(commentId)}`,
				{
					method: 'PUT',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({
						userId: normalizedUserID,
						content: nextContent
					})
				}
			);
			const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
			if (!res.ok) {
				throw new Error(toStringValue(data.error) || 'Failed to edit comment');
			}
			const parsed = parseIncomingMessage(data.comment, roomId, API_BASE);
			if (!parsed) {
				throw new Error('Comment payload is invalid');
			}
			upsertDiscussionCommentLocal(parsed);
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
				`${discussionCommentsEndpoint(normalizedTaskID)}/${encodeURIComponent(commentId)}`,
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
			const parsed = parseIncomingMessage(data.comment, roomId, API_BASE);
			if (!parsed) {
				throw new Error('Comment payload is invalid');
			}
			upsertDiscussionCommentLocal(parsed);
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
			const parsed = parseIncomingMessage(rawMessage, roomId, API_BASE);
			if (!parsed) {
				showErrorToast(
					direction === 'previous'
						? 'No previous pinned discussion in this room'
						: 'No next pinned discussion in this room'
				);
				return;
			}
			mergeMessages(roomId, [parsed]);
			activeDiscussionTaskId = parsed.id;
			isDiscussionOpen = true;
			discussionComments = [];
			void loadDiscussionComments(parsed.id);
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

			ensureRoomThread(nextRoomId, nextRoomName, 'joined');
			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === nextRoomId ? { ...thread, isAdmin: nextIsAdmin } : thread
				)
			);
			markRoomMembershipSynced(nextRoomId);
			ensureRoomMeta(nextRoomId, nextCreatedAt, nextExpiresAt);
			await refreshSidebarRooms();

			const params = new URLSearchParams({
				name: nextRoomName,
				member: '1'
			});
			if (nextCreatedAt > 0) {
				params.set('createdAt', String(nextCreatedAt));
			}
			if (nextExpiresAt > 0) {
				params.set('expiresAt', String(nextExpiresAt));
			}
			await goto(`/chat/${encodeURIComponent(nextRoomId)}?${params.toString()}`);
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
		try {
			const res = await fetch(`${API_BASE}/api/rooms/join`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					roomId,
					username: currentUsername,
					userId: normalizeIdentifier(currentUserId),
					mode: 'join'
				})
			});
			const data = await res.json();
			if (!res.ok) {
				throw new Error(data.error || 'Unable to join room');
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
			ensureRoomThread(roomId, joinedName, 'joined');
			markRoomMembershipSynced(roomId);
			ensureRoomMeta(roomId, joinedCreatedAt, joinedExpiresAt);
			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === roomId
						? { ...thread, status: 'joined', name: joinedName, isAdmin: joinedIsAdmin }
						: thread
				)
			);
			await refreshSidebarRooms();

			const params = new URLSearchParams({ name: joinedName, member: '1' });
			if (joinedCreatedAt > 0) {
				params.set('createdAt', String(joinedCreatedAt));
			}
			if (joinedExpiresAt > 0) {
				params.set('expiresAt', String(joinedExpiresAt));
			}
			await goto(`/chat/${encodeURIComponent(roomId)}?${params.toString()}`);
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
			if (expiresAt > 0) {
				ensureRoomMeta(targetRoomId, createdAt, expiresAt);
			} else if (expiresInSeconds > 0) {
				ensureRoomMeta(targetRoomId, createdAt, getApproxServerNowMs() + expiresInSeconds * 1000);
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
			const res = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomID)}/messages?before=${before}${beforeCreatedAt}&limit=50`
			);
			const data = await res.json().catch(() => ({}));
			if (!res.ok) {
				throw new Error(data.error || 'Failed to load older messages');
			}

			const payloadMessages = Array.isArray(data.messages) ? data.messages : [];
			const incoming = payloadMessages
				.map((entry: unknown) => parseIncomingMessage(entry, normalizedRoomID, API_BASE))
				.filter((entry: ChatMessage | null): entry is ChatMessage => Boolean(entry));
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
		sendSocketPayload({
			type: 'message_edit',
			roomId,
			messageId,
			content: nextContent
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
		try {
			const res = await fetch(`${API_BASE}/api/rooms/break`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					parentRoomId: roomId,
					originMessageId: message.id,
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
								treeNumber: breakTreeNumber > 0 ? breakTreeNumber : (thread.treeNumber ?? 0)
							}
						: thread
				)
			);
			markRoomMembershipSynced(breakRoomId);
			ensureRoomMeta(breakRoomId, breakCreatedAt, breakExpiresAt);
			await refreshSidebarRooms();
			const params = new URLSearchParams({
				name: breakRoomName,
				member: '1'
			});
			if (breakCreatedAt > 0) {
				params.set('createdAt', String(breakCreatedAt));
			}
			if (breakExpiresAt > 0) {
				params.set('expiresAt', String(breakExpiresAt));
			}
			await goto(`/chat/${encodeURIComponent(breakRoomId)}?${params.toString()}`);
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
		return roomMetaById[targetRoomId]?.createdAt ?? 0;
	}

	function getRoomExpiry(targetRoomId: string) {
		const meta = roomMetaById[targetRoomId];
		if (!meta) {
			return 0;
		}
		if (meta.expiresAt > 0) {
			return meta.expiresAt;
		}
		return 0;
	}

	function getRemainingHoursLabel(targetRoomId: string, tickMs: number) {
		const expiry = getRoomExpiry(targetRoomId);
		if (!expiry) {
			return '--';
		}

		const now = getApproxServerNowMs(tickMs);
		const remainingMs = expiry - now;
		if (remainingMs <= 0) {
			return 'Expired';
		}

		if (remainingMs < 60 * 60 * 1000) {
			return `${Math.ceil(remainingMs / 60000)}m`;
		}

		if (remainingMs < 24 * 60 * 60 * 1000) {
			const hours = Math.floor(remainingMs / 3600000);
			const minutes = Math.floor((remainingMs % 3600000) / 60000);
			return `${hours}h ${minutes}m`;
		}

		const days = Math.floor(remainingMs / 86400000);
		const hours = Math.floor((remainingMs % 86400000) / 3600000);
		return `${days}d ${hours}h`;
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
	class:theme-dark={isDarkMode}
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
			{isDarkMode}
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

	<section class="chat-window">
		<ChatRoomHeader
			roomName={activeThread.name}
			onlineCount={currentOnlineMembers.length}
			unreadCount={activeUnreadCount}
			{isMember}
			{isActiveRoomAdmin}
			{isMobileView}
			{isDarkMode}
			{messageActionMode}
			{showRoomSearch}
			remainingLabel={getRemainingHoursLabel(roomId, roomExpiryTickMs)}
			on:showMobileList={showMobileRoomList}
			on:openRoomDetails={openRoomDetails}
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

		<ChatStatusBars
			{typingIndicatorText}
			{showTrustedDevicePrompt}
			{isSelectionMode}
			{messageActionMode}
			selectedDeleteCount={selectedDeleteMessageIds.length}
			{showRoomSearch}
			bind:roomMessageSearch
			{isDarkMode}
			on:trustedChoice={(event) => onTrustedDeviceChoice(event.detail.choice)}
			on:cancelSelection={cancelSelectionMode}
			on:deleteSelected={deleteSelectedMessagesBatch}
		/>

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
				{isDarkMode}
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

		{#if isMember}
			<ChatComposer
				bind:draftMessage
				bind:attachedFile
				{activeReply}
				{isDarkMode}
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

	<div class="online-pane">
		<OnlinePanel members={currentOnlineMembers} {isDarkMode} />
	</div>
</section>

<DiscussionModal
	open={isDiscussionOpen}
	pinnedMessage={activeDiscussionTask}
	comments={discussionComments}
	{roomId}
	{isDarkMode}
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
	createdLabel={formatDateTime(getRoomCreatedAt(roomId))}
	expiresLabel={formatDateTime(getRoomExpiry(roomId))}
	{isExtendingRoom}
	{currentOnlineMembers}
	{isActiveRoomAdmin}
	{currentUserId}
	{formatDateTime}
	on:close={closeRoomDetails}
	on:extend={requestRoomExtension}
	on:removeMember={(event) => void removeMemberFromRoom(event.detail.memberId)}
/>

<style>
	.chat-shell {
		--panel-border: #cbd4e1;
		--panel-shadow: 0 10px 24px rgba(15, 23, 42, 0.1);
		height: calc(98vh);
		min-height: 620px;
		width: 100%;
		max-width: 100%;
		display: grid;
		grid-template-columns: 330px minmax(0, 1fr) 280px;
		gap: 0.75rem;
		padding: 0.75rem;
		box-sizing: border-box;
		background:
			radial-gradient(1400px 480px at -5% -30%, rgba(80, 116, 255, 0.06) 0%, transparent 58%),
			radial-gradient(1200px 420px at 110% 0%, rgba(20, 184, 166, 0.06) 0%, transparent 55%),
			#dde4ee;
		overflow: hidden;
	}

	.sidebar-pane,
	.chat-window,
	.online-pane {
		min-height: 0;
		min-width: 0;
		border: 1px solid var(--panel-border);
		border-radius: 16px;
		box-shadow: var(--panel-shadow);
		overflow: hidden;
		background: #f1f5fa;
	}

	.sidebar-pane {
		display: flex;
		align-self: center;
		height: 93%;
	}

	.chat-window {
		display: flex;
		flex-direction: column;
		overflow: hidden;
		background: linear-gradient(180deg, #f3f6fa 0%, #e9edf4 100%);
	}

	.online-pane {
		display: flex;
		align-self: center;
		height: 85%;
		background: linear-gradient(180deg, #f1f5fa 0%, #e7ecf4 100%);
	}

	.chat-shell.theme-dark {
		--panel-border: #2b2b2f;
		--panel-shadow: 0 12px 28px rgba(0, 0, 0, 0.45);
		background:
			radial-gradient(1300px 460px at -10% -35%, rgba(255, 255, 255, 0.07) 0%, transparent 58%),
			radial-gradient(1100px 400px at 110% 0%, rgba(255, 255, 255, 0.05) 0%, transparent 55%),
			#070708;
	}

	.chat-shell.theme-dark .sidebar-pane,
	.chat-shell.theme-dark .chat-window,
	.chat-shell.theme-dark .online-pane {
		background: #101012;
		border-color: var(--panel-border);
	}

	.toast {
		position: fixed;
		top: 0.8rem;
		left: 50%;
		transform: translateX(-50%);
		background: #111111;
		color: #ffffff;
		padding: 0.65rem 1rem;
		border-radius: 999px;
		font-size: 0.87rem;
		font-weight: 600;
		box-shadow: 0 12px 24px rgba(0, 0, 0, 0.2);
		z-index: 500;
		pointer-events: none;
	}

	@media (max-width: 1199px) {
		.chat-shell {
			grid-template-columns: 290px minmax(0, 1fr);
		}

		.sidebar-pane {
			align-self: stretch;
			height: 100%;
		}

		.online-pane {
			display: none;
		}
	}

	@media (max-height: 860px) {
		.sidebar-pane {
			align-self: stretch;
			height: 100%;
		}

		.online-pane {
			align-self: stretch;
			height: 100%;
		}
	}

	@media (max-width: 900px) {
		.chat-shell {
			grid-template-columns: 1fr;
			height: calc(98vh);
			min-height: 0;
			gap: 0.55rem;
			padding: 0.55rem;
		}

		.chat-shell.mobile-chat-only .sidebar-pane {
			display: none;
		}

		.chat-shell.mobile-list-only .chat-window {
			display: none;
		}

		.sidebar-pane,
		.chat-window {
			height: 100%;
		}

		.sidebar-pane {
			align-self: stretch;
			height: 100%;
		}
	}
</style>
