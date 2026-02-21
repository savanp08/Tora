<script lang="ts">
	import { browser } from '$app/environment';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import ChatComposer from '$lib/components/chat/ChatComposer.svelte';
	import ChatSidebar from '$lib/components/chat/ChatSidebar.svelte';
	import ChatWindow from '$lib/components/chat/ChatWindow.svelte';
	import OnlinePanel from '$lib/components/chat/OnlinePanel.svelte';
	import { authToken, currentUser } from '$lib/store';
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
	import { clearSessionToken, getSessionToken } from '$lib/utils/sessionToken';
	import { closeGlobalSocket, globalMessages, initGlobalSocket, sendSocketPayload, subscribeToRooms } from '$lib/ws';
	import { onDestroy, onMount, tick } from 'svelte';

	type ThreadStatus = 'joined' | 'discoverable' | 'left';
	type MessageActionMode = 'none' | 'break' | 'edit' | 'delete';
	type RoomMenuMode = 'create' | 'join';

	type UiDialogState =
		| { kind: 'none' }
		| {
				kind: 'confirm';
				title: string;
				message: string;
				confirmLabel: string;
				cancelLabel: string;
				danger: boolean;
		  }
		| {
				kind: 'prompt';
				title: string;
				message: string;
				value: string;
				placeholder: string;
				maxLength: number;
				confirmLabel: string;
				cancelLabel: string;
				danger: boolean;
				multiline: boolean;
		  }
		| {
				kind: 'roomAction';
				title: string;
				message: string;
				roomName: string;
				mode: RoomMenuMode;
				confirmLabel: string;
				cancelLabel: string;
		  };

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

	type ComposerMediaPayload = {
		type: 'image' | 'video' | 'file';
		content: string;
		fileName?: string;
	};

	type ChatThread = {
		id: string;
		name: string;
		lastMessage: string;
		lastActivity: number;
		unread: number;
		status: ThreadStatus;
		memberCount?: number;
		parentRoomId?: string;
		originMessageId?: string;
		treeNumber?: number;
		isAdmin?: boolean;
	};

	type OnlineMember = {
		id: string;
		name: string;
		isOnline: boolean;
		joinedAt: number;
	};

	type RoomMeta = {
		createdAt: number;
		expiresAt: number;
	};

	type SidebarRoom = {
		roomId: string;
		roomName: string;
		status: ThreadStatus;
		parentRoomId?: string;
		originMessageId?: string;
		treeNumber?: number;
		memberCount?: number;
		createdAt?: number;
		expiresAt?: number;
		isAdmin?: boolean;
	};

	type ReplyTarget = {
		messageId: string;
		senderName: string;
		content: string;
	};

	const CLIENT_LOG_PREFIX = '[chat-client]';
	const API_BASE = (import.meta.env.VITE_API_BASE as string | undefined) ?? 'http://localhost:8080';
	const CLIENT_DEBUG = (import.meta.env.VITE_CHAT_DEBUG as string | undefined) === '1';
	const TYPING_PING_INTERVAL_MS = 5000;
	const TYPING_STOP_DELAY_MS = 5000;
	const TYPING_SAFETY_TIMEOUT_MS = 7000;
	const DELETED_MESSAGE_PLACEHOLDER = 'This message was deleted';

	let sidebarRefreshTimer: ReturnType<typeof setInterval> | null = null;
	let roomExpiryTicker: ReturnType<typeof setInterval> | null = null;
	let roomMembershipSynced: Record<string, boolean> = {};
	let roomMembershipSyncing: Record<string, boolean> = {};
	let unsubscribeGlobalMessages: (() => void) | null = null;
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
	let showRoomMenu = false;
	let showRoomSearch = false;
	let showRoomDetails = false;
	let isSelectionMode = false;
	let messageActionMode: MessageActionMode = 'none';
	let selectedActionMessageId = '';
	let isMobileView = false;
	let mobilePane: 'list' | 'chat' = 'chat';
	let focusMessageId = '';
	let focusConsumedForRoom = false;
	let focusRoomTracker = '';

	let roomThreads: ChatThread[] = [];
	let messagesByRoom: Record<string, ChatMessage[]> = {};
	let onlineByRoom: Record<string, OnlineMember[]> = {};
	let roomMetaById: Record<string, RoomMeta> = {};
	let typingUsersByRoom: Record<string, Record<string, { name: string; expiresAt: number }>> = {};
	let historyLoadingByRoom: Record<string, boolean> = {};
	let historyHasMoreByRoom: Record<string, boolean> = {};
	let offlineHydratedByRoom: Record<string, boolean> = {};
	let trustedDevicePreference: TrustedDevicePreference = 'unset';
	let showTrustedDevicePrompt = false;
	let trustedCachingEnabled = false;
	let isExtendingRoom = false;
	let expandedMessages: Record<string, boolean> = {};
	let activeReply: ReplyTarget | null = null;
	let identityReady = !browser;
	let headerActionsEl: HTMLDivElement | null = null;
	let roomExpiryTickMs = Date.now();
	let serverClockOffsetMs = 0;
	let uiDialog: UiDialogState = { kind: 'none' };
	let uiDialogResolver: ((value: unknown) => void) | null = null;
	let uiDialogInputEl: HTMLInputElement | HTMLTextAreaElement | null = null;
	let chatWindowRef: {
		capturePrependAnchor?: () => { scrollTop: number; scrollHeight: number } | null;
		restorePrependAnchor?: (anchor: { scrollTop: number; scrollHeight: number } | null) => void;
	} | null = null;

	$: roomId = normalizeRoomIDValue(decodeURIComponent($page.params.roomId ?? ''));
	$: roomNameFromURL = normalizeRoomNameValue(
		decodeURIComponent($page.url.searchParams.get('name') ?? '').trim()
	);
	$: roomCreatedAtFromURL = parseTimestampParam($page.url.searchParams.get('createdAt'));
	$: roomExpiresAtFromURL = parseTimestampParam($page.url.searchParams.get('expiresAt'));
	$: serverNowFromURL = parseTimestampParam($page.url.searchParams.get('serverNow'));
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
	$: currentOnlineMembers = onlineByRoom[roomId] ?? [];
	$: isActiveRoomAdmin = Boolean(activeThread?.isAdmin);
	$: isMember = resolveRoomMembership(roomId, roomThreads, roomMemberHint);
	$: activeUnreadCount = activeThread?.unread ?? 0;
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
	$: if (serverNowFromURL > 0) {
		syncServerClock(serverNowFromURL);
	}
	$: if (browser && identityReady && roomId && isMember) {
		void syncRoomMembership(roomId);
	}
	$: if (browser && identityReady) {
		initGlobalSocket(currentUserId, currentUsername);
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
		messageActionMode = 'none';
		isSelectionMode = false;
		selectedActionMessageId = '';
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
		if (unsubscribeGlobalMessages) {
			unsubscribeGlobalMessages();
			unsubscribeGlobalMessages = null;
		}
		clearTypingStopTimer();
		clearAllTypingSafetyTimers();
		clearAllCachePersistTimers();
		clearSidebarRefreshTimer();
		clearRoomExpiryTicker();
		clearToastTimer();
	});

	onMount(() => {
		if (!browser) {
			return;
		}
		initializeTrustedDevicePreference();
		if (trustedCachingEnabled && roomId) {
			void hydrateOfflineCache(roomId);
		}
		void initializeIdentity();
		unsubscribeGlobalMessages = globalMessages.subscribe((event) => {
			if (!event) {
				return;
			}
			handleGlobalPayload(event.payload);
		});
		const onDocumentPointerDown = (event: PointerEvent) => {
			const target = event.target;
			if (!(target instanceof Node)) {
				return;
			}
			if (showRoomMenu && headerActionsEl && !headerActionsEl.contains(target)) {
				showRoomMenu = false;
			}
		};
		updateViewportMode();
		window.addEventListener('pointerdown', onDocumentPointerDown);
		window.addEventListener('resize', updateViewportMode);
		clearRoomExpiryTicker();
		roomExpiryTickMs = Date.now();
		roomExpiryTicker = setInterval(() => {
			roomExpiryTickMs = Date.now();
		}, 60000);
		return () => {
			if (unsubscribeGlobalMessages) {
				unsubscribeGlobalMessages();
				unsubscribeGlobalMessages = null;
			}
			window.removeEventListener('pointerdown', onDocumentPointerDown);
			window.removeEventListener('resize', updateViewportMode);
			clearRoomExpiryTicker();
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
		if (typingIsActive && now-typingLastPingAt < TYPING_PING_INTERVAL_MS) {
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
		if (!browser || !trustedCachingEnabled || !targetRoomId || offlineHydratedByRoom[targetRoomId]) {
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
			.map((entry) => parseIncomingMessage(entry, targetRoomId))
			.filter((entry): entry is ChatMessage => Boolean(entry));
		if (hydrated.length === 0) {
			return;
		}
		mergeMessages(targetRoomId, hydrated);
	}

	async function initializeIdentity() {
		const identity = getOrInitIdentity();
		currentUser.set({
			id: normalizeIdentifier(identity.id) || identity.id,
			username: normalizeUsernameValue(identity.username) || identity.username
		});
		identityReady = true;
		clientLog('identity-initialized', { id: identity.id, username: identity.username });
		await refreshSidebarRooms(normalizeIdentifier(identity.id) || identity.id);
		clearSidebarRefreshTimer();
		sidebarRefreshTimer = setInterval(() => {
			void refreshSidebarRooms();
		}, 15000);
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
		switch (uiDialog.kind) {
			case 'confirm':
				resolveActiveUiDialog(false);
				return;
			case 'prompt':
				resolveActiveUiDialog(null);
				return;
			case 'roomAction':
				resolveActiveUiDialog(null);
				return;
			default:
				resolveActiveUiDialog(null);
		}
	}

	function focusUiDialogInputSoon() {
		void tick().then(() => {
			uiDialogInputEl?.focus();
			if (uiDialogInputEl instanceof HTMLInputElement) {
				uiDialogInputEl.select();
			}
		});
	}

	function openConfirmDialog(config: {
		title: string;
		message: string;
		confirmLabel?: string;
		cancelLabel?: string;
		danger?: boolean;
	}) {
		resolveActiveUiDialog(false);
		uiDialog = {
			kind: 'confirm',
			title: config.title,
			message: config.message,
			confirmLabel: config.confirmLabel || 'Confirm',
			cancelLabel: config.cancelLabel || 'Cancel',
			danger: Boolean(config.danger)
		};
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
		uiDialog = {
			kind: 'prompt',
			title: config.title,
			message: config.message,
			value: config.initialValue ?? '',
			placeholder: config.placeholder ?? '',
			maxLength: Math.max(1, config.maxLength ?? 2000),
			confirmLabel: config.confirmLabel || 'Save',
			cancelLabel: config.cancelLabel || 'Cancel',
			danger: Boolean(config.danger),
			multiline: Boolean(config.multiline)
		};
		focusUiDialogInputSoon();
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
		uiDialog = {
			kind: 'roomAction',
			title: 'Open Room',
			message: 'Choose whether to create a new room or join an existing one.',
			roomName: normalizeRoomNameValue(initialName),
			mode: 'create',
			confirmLabel: 'Continue',
			cancelLabel: 'Cancel'
		};
		focusUiDialogInputSoon();
		return new Promise<{ mode: RoomMenuMode; roomName: string } | null>((resolve) => {
			uiDialogResolver = (value) => {
				if (
					value &&
					typeof value === 'object' &&
					'mode' in value &&
					'roomName' in value &&
					(typeof (value as { mode?: unknown }).mode === 'string')
				) {
					const parsed = value as { mode: RoomMenuMode; roomName: string };
					resolve(parsed);
					return;
				}
				resolve(null);
			};
		});
	}

	function onUiDialogBackdropClick() {
		closeUiDialog();
	}

	function onUiDialogConfirm() {
		if (uiDialog.kind === 'confirm') {
			resolveActiveUiDialog(true);
			return;
		}
		if (uiDialog.kind === 'prompt') {
			resolveActiveUiDialog(uiDialog.value);
			return;
		}
		if (uiDialog.kind === 'roomAction') {
			resolveActiveUiDialog({
				mode: uiDialog.mode,
				roomName: uiDialog.roomName
			});
		}
	}

	function onUiDialogKeyDown(event: KeyboardEvent) {
		if (event.key === 'Escape') {
			event.preventDefault();
			closeUiDialog();
			return;
		}
		if (event.key === 'Enter' && uiDialog.kind !== 'none') {
			if (uiDialog.kind === 'prompt' && uiDialog.multiline) {
				return;
			}
			if (
				(uiDialog.kind === 'prompt' && promptSubmitDisabled) ||
				(uiDialog.kind === 'roomAction' && roomActionSubmitDisabled)
			) {
				return;
			}
			event.preventDefault();
			onUiDialogConfirm();
		}
	}

	function updateUiPromptValue(value: string) {
		if (uiDialog.kind !== 'prompt') {
			return;
		}
		uiDialog = {
			...uiDialog,
			value: value.slice(0, uiDialog.maxLength)
		};
	}

	function updateRoomActionMode(mode: RoomMenuMode) {
		if (uiDialog.kind !== 'roomAction') {
			return;
		}
		uiDialog = {
			...uiDialog,
			mode
		};
	}

	function updateRoomActionName(value: string) {
		if (uiDialog.kind !== 'roomAction') {
			return;
		}
		uiDialog = {
			...uiDialog,
			roomName: value.slice(0, 20)
		};
	}

	function setMessageActionMode(mode: MessageActionMode) {
		messageActionMode = mode;
		isSelectionMode = mode !== 'none';
		if (mode === 'none') {
			selectedActionMessageId = '';
		}
	}

	function syncServerClock(rawServerNow: unknown) {
		const parsed = parseOptionalTimestamp(rawServerNow);
		if (!parsed || parsed <= 0) {
			return;
		}
		serverClockOffsetMs = parsed - Date.now();
	}

	function createThread(
		id: string,
		nameOverride?: string,
		status: ThreadStatus = 'joined'
	): ChatThread {
		return {
			id,
			name: nameOverride ?? formatRoomName(id),
			lastMessage: '',
			lastActivity: Date.now(),
			unread: 0,
			status,
			isAdmin: false
		};
	}

	function ensureRoomThread(
		targetRoomId: string,
		nameOverride?: string,
		status: ThreadStatus = 'joined'
	) {
		const existing = roomThreads.find((thread) => thread.id === targetRoomId);
		if (existing) {
			const nextName = nameOverride || existing.name;
			let nextStatus: ThreadStatus = existing.status;
			if (status === 'joined') {
				nextStatus = 'joined';
			} else if (existing.status !== 'joined' && existing.status !== 'left') {
				nextStatus = status;
			}
			if (nextName === existing.name && nextStatus === existing.status) {
				return;
			}

			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === targetRoomId
						? {
								...thread,
								name: nextName,
								status: nextStatus
							}
						: thread
				)
			);
			return;
		}

		roomThreads = sortThreads([createThread(targetRoomId, nameOverride, status), ...roomThreads]);
	}

	function ensureRoomMeta(targetRoomId: string, createdAt: number, expiresAt = 0) {
		if (!targetRoomId) {
			return;
		}
		const existing = roomMetaById[targetRoomId];
		const safeCreatedAt =
			Number.isFinite(createdAt) && createdAt > 0 ? createdAt : (existing?.createdAt ?? 0);
		const safeExpiresAt =
			Number.isFinite(expiresAt) && expiresAt > 0 ? expiresAt : (existing?.expiresAt ?? 0);
		if (
			existing &&
			existing.createdAt === safeCreatedAt &&
			existing.expiresAt === safeExpiresAt
		) {
			return;
		}
		roomMetaById = {
			...roomMetaById,
			[targetRoomId]: {
				createdAt: safeCreatedAt,
				expiresAt: safeExpiresAt
			}
		};
	}

	function ensureOnlineSeed(targetRoomId: string) {
		if (onlineByRoom[targetRoomId]?.length) {
			return;
		}
		onlineByRoom = {
			...onlineByRoom,
			[targetRoomId]: dedupeMembers([
				{
					id: currentUserId,
					name: currentUsername,
					isOnline: true,
					joinedAt: Date.now()
				}
			])
		};
	}

	function updateThreadPreview(targetRoomId: string) {
		const roomMessages = messagesByRoom[targetRoomId] ?? [];
		const lastMessage = roomMessages[roomMessages.length - 1];
		const fallbackName = formatRoomName(targetRoomId);
		if (!lastMessage) {
			ensureRoomThread(targetRoomId, fallbackName, 'joined');
			return;
		}

		const merged = roomThreads.some((thread) => thread.id === targetRoomId)
			? roomThreads.map((thread) =>
					thread.id === targetRoomId
						? {
								...thread,
								name: thread.name || fallbackName,
								lastMessage: getMessagePreviewText(lastMessage),
								lastActivity: lastMessage.createdAt
							}
						: thread
				)
			: [
					{
						...createThread(targetRoomId, fallbackName, 'joined'),
						lastMessage: getMessagePreviewText(lastMessage),
						lastActivity: lastMessage.createdAt
					},
					...roomThreads
				];
		roomThreads = sortThreads(merged);
	}

	function getMessagePreviewText(message: ChatMessage) {
		const content = (message.content || '').trim();
		if (message.type === 'image') {
			if (content && !isLikelyMediaURL(content)) {
				return content;
			}
			return 'Photo';
		}
		if (message.type === 'video') {
			if (content && !isLikelyMediaURL(content)) {
				return content;
			}
			return 'Video';
		}
		if (message.type === 'file') {
			if (content && !isLikelyMediaURL(content)) {
				return content;
			}
			const fileName = (message.fileName || '').trim();
			return fileName ? `File: ${fileName}` : 'Attachment';
		}
		return content;
	}

	function sortThreads(threads: ChatThread[]) {
		return [...threads].sort((a, b) => b.lastActivity - a.lastActivity);
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
			syncServerClock((data as { serverNow?: unknown; server_now?: unknown }).serverNow ?? (data as { serverNow?: unknown; server_now?: unknown }).server_now);

			markRoomMembershipSynced(normalizedRoomId);
			const joinedName =
				normalizeRoomNameValue(toStringValue(data.roomName)) || formatRoomName(normalizedRoomId);
			const joinedCreatedAt = toTimestamp(data.createdAt);
			const joinedExpiresAt = parseOptionalTimestamp(data.expiresAt ?? data.expires_at);
			const joinedIsAdmin = toBool((data as { isAdmin?: unknown; is_admin?: unknown }).isAdmin ?? (data as { isAdmin?: unknown; is_admin?: unknown }).is_admin);
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
			syncServerClock((data as { serverNow?: unknown; server_now?: unknown }).serverNow ?? (data as { serverNow?: unknown; server_now?: unknown }).server_now);
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
					room.status === 'joined'
						? 'joined'
						: room.status === 'left'
							? 'left'
							: 'discoverable';

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
					originMessageId: toStringValue(room.originMessageId) || prev?.originMessageId || undefined,
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
		} catch (error) {
			clientLog('api-sidebar-error', {
				error: error instanceof Error ? error.message : String(error)
			});
		}
	}

	function onSidebarSelect(event: CustomEvent<{ id: string; isMember: boolean; status: ThreadStatus }>) {
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
		showRoomMenu = false;
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
		params.set('serverNow', String(Date.now() + serverClockOffsetMs));

		const query = params.toString();
		void goto(`/chat/${encodeURIComponent(normalizedTargetRoomId)}${query ? `?${query}` : ''}`);
	}

	function showMobileRoomList() {
		if (!isMobileView) {
			return;
		}
		showRoomMenu = false;
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

	type SocketEnvelope = {
		type: string;
		payload: unknown;
		roomId?: unknown;
		room_id?: unknown;
	};

	function handleGlobalPayload(payload: unknown) {
		if (Array.isArray(payload)) {
			const parsedMessages = payload
				.map((entry) => parseIncomingMessage(entry, ''))
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
				if (targetRoomId === roomId) {
					markRoomAsRead(targetRoomId);
				}
			}
			return;
		}

		if (isEnvelope(payload)) {
			handleEnvelope(payload);
			return;
		}

		const single = parseIncomingMessage(payload, '');
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

	function handleEnvelope(envelope: SocketEnvelope) {
		const targetRoomId = resolveEnvelopeRoomID(envelope);
		const kind = envelope.type;
		if (kind === 'history' || kind === 'recent_messages' || kind === 'initial_messages') {
			if (Array.isArray(envelope.payload)) {
				const history = envelope.payload
					.map((entry) => parseIncomingMessage(entry, targetRoomId))
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
						if (roomID === roomId) {
							markRoomAsRead(roomID);
						}
					}
				}
			}
			return;
		}

		if (kind === 'new_message') {
			const message = parseIncomingMessage(envelope.payload, targetRoomId);
			if (message) {
				addIncomingMessage(message);
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

	function parseIncomingMessage(value: unknown, fallbackRoomId: string): ChatMessage | null {
		if (!value || typeof value !== 'object') {
			return null;
		}

		const source = value as Record<string, unknown>;
		const nextRoomId = normalizeRoomIDValue(
			toStringValue(source.roomId ?? source.room_id ?? fallbackRoomId)
		);
		if (!nextRoomId) {
			return null;
		}

		const nextType = toStringValue(source.type ?? 'text') || 'text';
		const rawText = toStringValue(source.text ?? source.content ?? source.caption ?? '');
		const rawMediaURL = toStringValue(source.mediaUrl ?? source.media_url ?? '');
		let normalizedMediaURL = toAbsoluteMediaURL(rawMediaURL);
		let nextContent = rawText;
		if (isMediaMessageType(nextType) && !normalizedMediaURL && isLikelyMediaURL(rawText)) {
			normalizedMediaURL = toAbsoluteMediaURL(rawText);
			nextContent = '';
		}
		const hasBreakRoom =
			toBool(source.hasBreakRoom ?? source.has_break_room) ||
			toStringValue(source.breakRoomId ?? source.break_room_id) !== '';
		const breakRoomId = toStringValue(source.breakRoomId ?? source.break_room_id);
		const branchCount = Math.max(toInt(source.branchesCreated ?? source.branches_created), hasBreakRoom ? 1 : 0);

		return {
			id: toStringValue(source.id) || createMessageId(nextRoomId),
			roomId: nextRoomId,
			senderId: toStringValue(source.userId ?? source.senderId ?? source.sender_id ?? 'unknown'),
			senderName:
				normalizeUsernameValue(
					toStringValue(source.username ?? source.senderName ?? source.sender_name ?? 'Unknown')
				) || 'Unknown',
			content: nextContent,
			type: nextType,
			mediaUrl:
				normalizedMediaURL ||
				(isMediaMessageType(nextType) && isLikelyMediaURL(rawText)
					? toAbsoluteMediaURL(rawText)
					: ''),
			mediaType: toStringValue(source.mediaType ?? source.media_type ?? source.type ?? nextType),
			fileName: toStringValue(source.fileName ?? source.file_name),
			isEdited: toBool(source.isEdited ?? source.is_edited),
			editedAt: parseOptionalTimestamp(source.editedAt ?? source.edited_at),
			isDeleted:
				nextType === 'deleted' ||
				toBool(source.isDeleted ?? source.is_deleted) ||
				toStringValue(source.content).trim() === DELETED_MESSAGE_PLACEHOLDER,
			replyToMessageId: normalizeMessageID(
				toStringValue(source.replyToMessageId ?? source.reply_to_message_id)
			),
			replyToSnippet: toStringValue(source.replyToSnippet ?? source.reply_to_snippet).trim(),
			totalReplies: toInt(source.totalReplies ?? source.total_replies),
			branchesCreated: branchCount,
			createdAt: toTimestamp(
				source.time ?? source.createdAt ?? source.created_at ?? source.timestamp
			),
			hasBreakRoom,
			breakRoomId,
			breakJoinCount: toInt(source.breakJoinCount ?? source.break_join_count),
			pending: false
		};
	}

	function parseMember(value: unknown, fallbackIndex: number): OnlineMember | null {
		if (!value || typeof value !== 'object') {
			return null;
		}
		const source = value as Record<string, unknown>;
		const memberId = toStringValue(
			source.id ?? source.userId ?? source.user_id ?? `member-${fallbackIndex}`
		);
		const memberName =
			toStringValue(source.name ?? source.username ?? source.userName ?? source.user_name) ||
			memberId;
		const joinedAt = toTimestamp(source.joinedAt ?? source.joined_at ?? Date.now());
		if (!memberId) {
			return null;
		}
		return { id: memberId, name: memberName, isOnline: true, joinedAt };
	}

	function addIncomingMessage(message: ChatMessage) {
		const shouldCountUnread = message.roomId !== roomId;
		upsertMessage(message.roomId, message, shouldCountUnread);
		if (message.roomId === roomId) {
			markRoomAsRead(roomId);
		}
	}

	function upsertMessage(targetRoomId: string, message: ChatMessage, shouldCountUnread: boolean) {
		const roomMessages = messagesByRoom[targetRoomId] ?? [];
		const existingIndex = roomMessages.findIndex((entry) => entry.id === message.id);

		let nextMessages: ChatMessage[];
		if (existingIndex >= 0) {
			nextMessages = [...roomMessages];
			nextMessages[existingIndex] = {
				...nextMessages[existingIndex],
				...message,
				pending: false
			};
		} else {
			nextMessages = [...roomMessages, message];
		}

		nextMessages.sort((a, b) => a.createdAt - b.createdAt);
		messagesByRoom = {
			...messagesByRoom,
			[targetRoomId]: nextMessages
		};

		updateThreadPreview(targetRoomId);
		queueOfflineCachePersist(targetRoomId);
		if (shouldCountUnread) {
			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === targetRoomId ? { ...thread, unread: thread.unread + 1 } : thread
				)
			);
		}
	}

	function mergeMessages(targetRoomId: string, incoming: ChatMessage[]) {
		if (incoming.length === 0) {
			return;
		}
		const existing = messagesByRoom[targetRoomId] ?? [];
		const merged = new Map<string, ChatMessage>();
		for (const message of existing) {
			merged.set(message.id, message);
		}
		for (const message of incoming) {
			const current = merged.get(message.id);
			merged.set(message.id, { ...current, ...message, pending: false });
		}
		const nextMessages = [...merged.values()].sort((a, b) => a.createdAt - b.createdAt);
		messagesByRoom = {
			...messagesByRoom,
			[targetRoomId]: nextMessages
		};
		updateThreadPreview(targetRoomId);
		queueOfflineCachePersist(targetRoomId);
	}

	function applyMessageEdit(targetRoomId: string, payload: unknown) {
		if (!payload || typeof payload !== 'object') {
			return;
		}
		const source = payload as Record<string, unknown>;
		const messageId = normalizeMessageID(toStringValue(source.messageId ?? source.id));
		const nextContent = toStringValue(source.content).trim();
		const editedAt = parseOptionalTimestamp(source.editedAt ?? source.edited_at ?? Date.now());
		if (!messageId || !nextContent) {
			return;
		}
		const roomMessages = messagesByRoom[targetRoomId] ?? [];
		const index = roomMessages.findIndex((entry) => entry.id === messageId);
		if (index < 0) {
			return;
		}
		const nextMessages = [...roomMessages];
		nextMessages[index] = {
			...nextMessages[index],
			content: nextContent,
			type: 'text',
			mediaUrl: '',
			mediaType: '',
			fileName: '',
			isEdited: true,
			editedAt,
			isDeleted: false,
			pending: false
		};
		messagesByRoom = {
			...messagesByRoom,
			[targetRoomId]: nextMessages
		};
		updateThreadPreview(targetRoomId);
		queueOfflineCachePersist(targetRoomId);
	}

	function applyMessageDelete(targetRoomId: string, payload: unknown) {
		if (!payload || typeof payload !== 'object') {
			return;
		}
		const source = payload as Record<string, unknown>;
		const messageId = normalizeMessageID(toStringValue(source.messageId ?? source.id));
		if (!messageId) {
			return;
		}
		const roomMessages = messagesByRoom[targetRoomId] ?? [];
		const index = roomMessages.findIndex((entry) => entry.id === messageId);
		if (index < 0) {
			return;
		}
		const nextMessages = [...roomMessages];
		nextMessages[index] = {
			...nextMessages[index],
			content: DELETED_MESSAGE_PLACEHOLDER,
			type: 'deleted',
			mediaUrl: '',
			mediaType: '',
			fileName: '',
			replyToMessageId: '',
			replyToSnippet: '',
			isEdited: false,
			editedAt: parseOptionalTimestamp(source.editedAt ?? source.edited_at),
			isDeleted: true,
			pending: false
		};
		messagesByRoom = {
			...messagesByRoom,
			[targetRoomId]: nextMessages
		};
		updateThreadPreview(targetRoomId);
		queueOfflineCachePersist(targetRoomId);
	}

	function markRoomAsRead(targetRoomId: string) {
		if (!targetRoomId) {
			return;
		}
		roomThreads = sortThreads(
			roomThreads.map((thread) => (thread.id === targetRoomId ? { ...thread, unread: 0 } : thread))
		);
	}

	function upsertOnlineMember(targetRoomId: string, member: OnlineMember) {
		const members = onlineByRoom[targetRoomId] ?? [];
		const existingIndex = members.findIndex((entry) => entry.id === member.id);
		let next: OnlineMember[];
		if (existingIndex >= 0) {
			next = [...members];
			next[existingIndex] = { ...next[existingIndex], ...member, isOnline: true };
		} else {
			next = [...members, { ...member, isOnline: true }];
		}
		onlineByRoom = {
			...onlineByRoom,
			[targetRoomId]: dedupeMembers(next)
		};
	}

	function removeOnlineMember(targetRoomId: string, memberId: string) {
		const members = onlineByRoom[targetRoomId] ?? [];
		onlineByRoom = {
			...onlineByRoom,
			[targetRoomId]: members.filter((member) => member.id !== memberId)
		};
	}

	function dedupeMembers(members: OnlineMember[]) {
		const byId = new Map<string, OnlineMember>();
		for (const member of members) {
			byId.set(member.id, member);
		}
		return [...byId.values()];
	}

	function buildReplySnippet(senderName: string, content: string) {
		const normalizedSender = normalizeUsernameValue(senderName) || 'User';
		const normalizedContent = content.trim().replace(/\s+/g, ' ');
		const base = normalizedContent ? `${normalizedSender}: ${normalizedContent}` : normalizedSender;
		if (base.length <= 140) {
			return base;
		}
		return `${base.slice(0, 137)}...`;
	}

	async function sendMessage(payload?: ComposerMediaPayload) {
		if (!roomId || !isMember) {
			showErrorToast('Join room before sending messages');
			return;
		}

		const text = draftMessage.trim();
		const mediaType = payload?.type;
		const mediaContent = payload?.content?.trim() ?? '';
		const isMediaMessage = Boolean(mediaType && mediaContent);
		if (!text && !isMediaMessage) {
			return;
		}
		const replyTarget = activeReply;
		const replyToMessageId = replyTarget ? normalizeMessageID(replyTarget.messageId) : '';
		const replyToSnippet = replyToMessageId
			? buildReplySnippet(replyTarget?.senderName || '', replyTarget?.content || '')
			: '';

		let outgoing: ChatMessage;
		if (isMediaMessage) {
			outgoing = {
				id: createMessageId(roomId),
				roomId,
				senderId: currentUserId,
				senderName: currentUsername,
				content: text,
				type: mediaType || 'file',
				mediaUrl: mediaContent,
				mediaType: mediaType,
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
		markRoomAsRead(roomId);
		sendTypingStop();
		draftMessage = '';
		attachedFile = null;
		activeReply = null;
	}

	function toWireMessage(message: ChatMessage) {
		const mediaType = isMediaMessageType(message.type) ? message.type : '';
		const mediaURL = mediaType
			? (message.mediaUrl || '').trim() || (isLikelyMediaURL(message.content) ? message.content : '')
			: '';
		const contentText =
			mediaType && mediaURL && message.content.trim() === mediaURL ? '' : message.content;

		return {
			id: message.id,
			roomId: message.roomId,
			userId: message.senderId,
			username: message.senderName,
			text: contentText,
			time: new Date(message.createdAt).toISOString(),
			senderId: message.senderId,
			senderName: message.senderName,
			content: contentText,
			type: message.type,
			mediaUrl: mediaURL,
			mediaType,
			fileName: message.fileName ?? '',
			replyToMessageId: normalizeMessageID(message.replyToMessageId ?? ''),
			replyToSnippet: (message.replyToSnippet || '').trim(),
			reply_to_message_id: normalizeMessageID(message.replyToMessageId ?? ''),
			reply_to_snippet: (message.replyToSnippet || '').trim(),
			createdAt: new Date(message.createdAt).toISOString()
		};
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
		showRoomMenu = false;
	}

	async function renameRoom(targetRoomId: string = roomId) {
		const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
		if (!normalizedRoomID) {
			return;
		}
		showLeftMenu = false;
		showRoomMenu = false;

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
			showRoomMenu = false;
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
						(roomMode === 'join'
							? 'Failed to join existing room'
							: 'Failed to create room')
				);
			}
			syncServerClock((data as { serverNow?: unknown; server_now?: unknown }).serverNow ?? (data as { serverNow?: unknown; server_now?: unknown }).server_now);

			const nextRoomId = normalizeRoomIDValue(toStringValue(data.roomId));
			if (!nextRoomId) {
				throw new Error('Invalid room id returned from server');
			}
			const nextRoomName =
				normalizeRoomNameValue(toStringValue(data.roomName)) || formatRoomName(nextRoomId);
			const nextCreatedAt = toTimestamp(data.createdAt);
			const nextExpiresAt = parseOptionalTimestamp(data.expiresAt ?? data.expires_at);
			const nextIsAdmin = toBool((data as { isAdmin?: unknown; is_admin?: unknown }).isAdmin ?? (data as { isAdmin?: unknown; is_admin?: unknown }).is_admin);

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
			params.set('serverNow', String(Date.now() + serverClockOffsetMs));
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
			syncServerClock((data as { serverNow?: unknown; server_now?: unknown }).serverNow ?? (data as { serverNow?: unknown; server_now?: unknown }).server_now);

			const joinedName =
				normalizeRoomNameValue(toStringValue(data.roomName)) ||
				activeThread.name ||
				formatRoomName(roomId);
			const joinedCreatedAt = toTimestamp(data.createdAt);
			const joinedExpiresAt = parseOptionalTimestamp(data.expiresAt ?? data.expires_at);
			const joinedIsAdmin = toBool((data as { isAdmin?: unknown; is_admin?: unknown }).isAdmin ?? (data as { isAdmin?: unknown; is_admin?: unknown }).is_admin);
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
			params.set('serverNow', String(Date.now() + serverClockOffsetMs));
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
			syncServerClock((data as { serverNow?: unknown; server_now?: unknown }).serverNow ?? (data as { serverNow?: unknown; server_now?: unknown }).server_now);
			const expiresAt = parseOptionalTimestamp(data.expiresAt ?? data.expires_at);
			const expiresInSeconds = toInt(data.expiresInSeconds ?? data.expires_in_seconds);
			const createdAt = getRoomCreatedAt(targetRoomId);
				if (expiresAt > 0) {
					ensureRoomMeta(targetRoomId, createdAt, expiresAt);
				} else if (expiresInSeconds > 0) {
					ensureRoomMeta(
						targetRoomId,
						createdAt,
						Date.now() + serverClockOffsetMs + expiresInSeconds * 1000
					);
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
		showRoomMenu = false;
	}

	function toggleEditSelectionMode() {
		const nextMode: MessageActionMode = messageActionMode === 'edit' ? 'none' : 'edit';
		setMessageActionMode(nextMode);
		showRoomMenu = false;
	}

	function toggleDeleteSelectionMode() {
		const nextMode: MessageActionMode = messageActionMode === 'delete' ? 'none' : 'delete';
		setMessageActionMode(nextMode);
		showRoomMenu = false;
	}

	function toggleRoomMenu() {
		showRoomMenu = !showRoomMenu;
		showLeftMenu = false;
	}

	function toggleRoomSearch() {
		showRoomSearch = !showRoomSearch;
		showRoomMenu = false;
		if (!showRoomSearch) {
			roomMessageSearch = '';
		}
	}

	function openRoomDetails() {
		showRoomDetails = true;
		showRoomMenu = false;
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
		showRoomMenu = false;
	}

	async function disconnectAndWipe() {
		showRoomMenu = false;
		showLeftMenu = false;
		setMessageActionMode('none');
		sendTypingStop();
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
				.map((entry: unknown) => parseIncomingMessage(entry, normalizedRoomID))
				.filter((entry: ChatMessage | null): entry is ChatMessage => Boolean(entry));
			if (incoming.length > 0) {
				mergeMessages(normalizedRoomID, incoming);
				await tick();
				chatWindowRef?.restorePrependAnchor?.(anchor);
			}

			const hasMore =
				typeof data.hasMore === 'boolean' ? data.hasMore : incoming.length >= 50;
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
			syncServerClock((data as { serverNow?: unknown; server_now?: unknown }).serverNow ?? (data as { serverNow?: unknown; server_now?: unknown }).server_now);
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
			syncServerClock((data as { serverNow?: unknown; server_now?: unknown }).serverNow ?? (data as { serverNow?: unknown; server_now?: unknown }).server_now);
			setMessageActionMode('none');
			showRoomMenu = false;
			showRoomDetails = false;

			const deletedRootId = roomId;
			const deleteIDs = collectLocalRoomSubtreeIDs(deletedRootId);
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
			syncServerClock((data as { serverNow?: unknown; server_now?: unknown }).serverNow ?? (data as { serverNow?: unknown; server_now?: unknown }).server_now);
			setMessageActionMode('none');
			showRoomMenu = false;
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

		if (messageActionMode === 'edit' || messageActionMode === 'delete') {
			if (normalizeIdentifier(message.senderId) !== normalizeIdentifier(currentUserId)) {
				showErrorToast('You can only edit/delete your own messages');
				return;
			}
			const loweredType = (message.type || '').toLowerCase();
			if (loweredType === 'deleted' || (message.content || '').trim() === DELETED_MESSAGE_PLACEHOLDER) {
				showErrorToast('Deleted messages cannot be selected');
				return;
			}
			selectedActionMessageId = message.id;
			return;
		}
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
			syncServerClock((data as { serverNow?: unknown; server_now?: unknown }).serverNow ?? (data as { serverNow?: unknown; server_now?: unknown }).server_now);

			const breakRoomId = normalizeRoomIDValue(toStringValue(data.roomId));
			if (!breakRoomId) {
				throw new Error('Invalid break room id');
			}
			const breakRoomName =
				normalizeRoomNameValue(toStringValue(data.roomName)) || formatRoomName(breakRoomId);
			const breakCreatedAt = toTimestamp(data.createdAt);
			const breakExpiresAt = parseOptionalTimestamp(data.expiresAt ?? data.expires_at);

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
			markRoomMembershipSynced(breakRoomId);
			ensureRoomMeta(breakRoomId, breakCreatedAt, breakExpiresAt);
			await refreshSidebarRooms();
			const params = new URLSearchParams({
				name: breakRoomName,
				member: '1',
				serverNow: String(Date.now() + serverClockOffsetMs)
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

	function filterThreadsByStatus(threads: ChatThread[], status: ThreadStatus) {
		return threads.filter((thread) => thread.status === status);
	}

	function filterThreadList(
		threads: ChatThread[],
		searchQuery: string,
		messageMap: Record<string, ChatMessage[]>,
		activeRoomId: string
	) {
		const query = searchQuery.trim().toLowerCase();
		if (!query) {
			return threads;
		}
		const filtered = threads.filter((thread) => {
			if (thread.name.toLowerCase().includes(query)) {
				return true;
			}
			if (thread.lastMessage.toLowerCase().includes(query)) {
				return true;
			}
			const messages = messageMap[thread.id] ?? [];
			return messages.some(
				(message) =>
					message.content.toLowerCase().includes(query) ||
					message.senderName.toLowerCase().includes(query)
			);
		});

		if (activeRoomId && !filtered.some((thread) => thread.id === activeRoomId)) {
			const active = threads.find((thread) => thread.id === activeRoomId);
			if (active) {
				return [active, ...filtered];
			}
		}
		return filtered;
	}

	function collectLocalRoomSubtreeIDs(rootRoomId: string) {
		const normalizedRoot = normalizeRoomIDValue(rootRoomId);
		const ids = new Set<string>();
		if (!normalizedRoot) {
			return ids;
		}
		ids.add(normalizedRoot);

		let changed = true;
		while (changed) {
			changed = false;
			for (const thread of roomThreads) {
				const threadRoomId = normalizeRoomIDValue(thread.id);
				const parentRoomId = normalizeRoomIDValue(thread.parentRoomId || '');
				if (!threadRoomId || !parentRoomId) {
					continue;
				}
				if (!ids.has(parentRoomId) || ids.has(threadRoomId)) {
					continue;
				}
				ids.add(threadRoomId);
				changed = true;
			}
		}
		return ids;
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

	function getRemainingHoursLabel(targetRoomId: string) {
		const expiry = getRoomExpiry(targetRoomId);
		if (!expiry) {
			return '--';
		}
		const now = roomExpiryTickMs + serverClockOffsetMs;
		const remainingMs = Math.max(0, expiry - now);
		if (remainingMs <= 0) {
			return '0m';
		}

		const totalMinutes = remainingMs / (60 * 1000);
		if (totalMinutes < 60) {
			const minutes = Math.max(1, Math.round(totalMinutes));
			return `${minutes}m`;
		}

			const totalHours = remainingMs / (60 * 60 * 1000);
			if (totalHours < 24) {
				const roundedHours = Math.round(totalHours * 10) / 10;
				const value = roundedHours.toFixed(1);
				return `${value}${roundedHours === 1 ? 'hr' : 'hrs'}`;
			}

			const totalDays = totalHours / 24;
			const roundedDays = Math.round(totalDays * 10) / 10;
			const value = roundedDays.toFixed(1);
			return `${value}${roundedDays === 1 ? 'day' : 'days'}`;
		}

	function formatDateTime(timestamp: number) {
		if (!Number.isFinite(timestamp) || timestamp <= 0) {
			return 'Unknown';
		}
		return new Date(timestamp).toLocaleString([], {
			year: 'numeric',
			month: 'short',
			day: 'numeric',
			hour: '2-digit',
			minute: '2-digit'
		});
	}

	function parseTimestampParam(value: string | null) {
		if (!value) {
			return 0;
		}
		const numeric = Number(value);
		if (!Number.isFinite(numeric) || numeric <= 0) {
			return 0;
		}
		return normalizeEpoch(numeric);
	}

	function normalizeRoomIDValue(value: string) {
		return value
			.toLowerCase()
			.trim()
			.replace(/[^a-z0-9]/g, '');
	}

	function normalizeRoomNameValue(value: string) {
		const trimmed = value.trim();
		if (!trimmed) {
			return '';
		}
		return trimmed.replace(/\s+/g, ' ').slice(0, 20);
	}

	function normalizeUsernameValue(value: string) {
		return value
			.trim()
			.replace(/[^a-zA-Z0-9\s_-]/g, '')
			.replace(/[\s-]+/g, '_')
			.replace(/_+/g, '_')
			.replace(/^_+|_+$/g, '');
	}

	function normalizeIdentifier(value: string) {
		return value
			.trim()
			.replace(/[^a-zA-Z0-9\s_-]/g, '')
			.replace(/[\s-]+/g, '_')
			.replace(/_+/g, '_')
			.replace(/^_+|_+$/g, '');
	}

	function normalizeMessageID(value: string) {
		return value.trim().replace(/[^a-zA-Z0-9_-]/g, '');
	}

	function createMessageId(targetRoomId: string) {
		if (browser && typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
			return crypto.randomUUID();
		}
		return `m${targetRoomId}${Date.now().toString(36)}${Math.floor(Math.random() * 1000000).toString(36)}`;
	}

	function formatRoomName(targetRoomId: string) {
		const trimmed = normalizeRoomIDValue(targetRoomId);
		if (!trimmed) {
			return 'Room';
		}
		return 'Room';
	}

	function toTimestamp(value: unknown) {
		if (typeof value === 'number' && Number.isFinite(value)) {
			return normalizeEpoch(value);
		}
		if (typeof value === 'string') {
			const trimmed = value.trim();
			if (!trimmed) {
				return Date.now();
			}
			const asNumber = Number(trimmed);
			if (Number.isFinite(asNumber)) {
				return normalizeEpoch(asNumber);
			}
			const parsed = Date.parse(trimmed);
			if (Number.isFinite(parsed)) {
				return parsed;
			}
		}
		if (value instanceof Date) {
			return value.getTime();
		}
		return Date.now();
	}

	function parseOptionalTimestamp(value: unknown) {
		if (value === null || value === undefined) {
			return 0;
		}
		if (typeof value === 'number') {
			if (!Number.isFinite(value) || value <= 0) {
				return 0;
			}
			return normalizeEpoch(value);
		}
		if (typeof value === 'string') {
			const trimmed = value.trim();
			if (!trimmed) {
				return 0;
			}
			const numeric = Number(trimmed);
			if (Number.isFinite(numeric) && numeric > 0) {
				return normalizeEpoch(numeric);
			}
			const parsed = Date.parse(trimmed);
			if (Number.isFinite(parsed) && parsed > 0) {
				return parsed;
			}
			return 0;
		}
		if (value instanceof Date) {
			return value.getTime();
		}
		return 0;
	}

	function normalizeEpoch(value: number) {
		if (value > 0 && value < 1_000_000_000_000) {
			return value * 1000;
		}
		return value;
	}

	function toBool(value: unknown) {
		if (typeof value === 'boolean') {
			return value;
		}
		if (typeof value === 'string') {
			const lower = value.toLowerCase();
			return lower === '1' || lower === 'true';
		}
		if (typeof value === 'number') {
			return value === 1;
		}
		return false;
	}

	function toInt(value: unknown) {
		if (typeof value === 'number' && Number.isFinite(value)) {
			return Math.trunc(value);
		}
		if (typeof value === 'string') {
			const parsed = Number(value);
			if (Number.isFinite(parsed)) {
				return Math.trunc(parsed);
			}
		}
		return 0;
	}

	function toStringValue(value: unknown) {
		if (typeof value === 'string') {
			return value;
		}
		if (typeof value === 'number' || typeof value === 'boolean') {
			return String(value);
		}
		return '';
	}

	function isMediaMessageType(value: string) {
		const normalized = value.trim().toLowerCase();
		return normalized === 'image' || normalized === 'video' || normalized === 'file';
	}

	function isLikelyMediaURL(value: string) {
		const trimmed = value.trim();
		return (
			trimmed.startsWith('http://') ||
			trimmed.startsWith('https://') ||
			trimmed.startsWith('blob:') ||
			trimmed.startsWith('data:') ||
			trimmed.startsWith('/')
		);
	}

	function toAbsoluteMediaURL(value: string) {
		const trimmed = value.trim();
		if (!trimmed) {
			return '';
		}
		if (trimmed.startsWith('blob:') || trimmed.startsWith('data:')) {
			return trimmed;
		}
		if (/^https?:\/\//i.test(trimmed)) {
			try {
				const parsed = new URL(trimmed);
				if (parsed.hostname.endsWith('.r2.cloudflarestorage.com')) {
					const pathParts = parsed.pathname.split('/').filter(Boolean);
					if (pathParts.length >= 2) {
						const objectKey = decodeIfNeeded(pathParts.slice(1).join('/'));
						return `${API_BASE}/api/upload/object/${encodeURIComponent(objectKey)}`;
					}
				}
			} catch {
				return trimmed;
			}
			return trimmed;
		}
		if (trimmed.startsWith('/')) {
			return `${API_BASE}${trimmed}`;
		}
		return `${API_BASE}/${trimmed}`;
	}

	function decodeIfNeeded(value: string) {
		try {
			return decodeURIComponent(value);
		} catch {
			return value;
		}
	}

	function resolveRoomMembership(roomID: string, threads: ChatThread[], memberHint: string | null) {
		if (!roomID) {
			return true;
		}
		if (memberHint === '0') {
			return false;
		}
		if (memberHint === '1') {
			return true;
		}
		const thread = threads.find((entry) => entry.id === roomID);
		if (!thread) {
			return false;
		}
		return thread.status === 'joined';
	}
</script>

{#if showToast}
	<div class="toast" role="status" aria-live="polite">{toastMessage}</div>
{/if}

{#if uiDialog.kind !== 'none'}
	<button
		type="button"
		class="ui-dialog-backdrop"
		aria-label="Close dialog"
		on:click={onUiDialogBackdropClick}
	></button>
	<div
		class="ui-dialog"
		role="dialog"
		aria-modal="true"
		aria-labelledby="ui-dialog-title"
		tabindex="-1"
		on:keydown={onUiDialogKeyDown}
	>
		<header class="ui-dialog-header">
			<h3 id="ui-dialog-title">{uiDialog.title}</h3>
		</header>
		<div class="ui-dialog-body">
			<p>{uiDialog.message}</p>
			{#if uiDialog.kind === 'prompt'}
				{#if uiDialog.multiline}
					<textarea
						class="ui-dialog-input ui-dialog-textarea"
						value={uiDialog.value}
						placeholder={uiDialog.placeholder}
						maxlength={uiDialog.maxLength}
						rows={5}
						bind:this={uiDialogInputEl}
						on:input={(event) =>
							updateUiPromptValue((event.currentTarget as HTMLTextAreaElement).value)}
					></textarea>
				{:else}
					<input
						class="ui-dialog-input"
						type="text"
						value={uiDialog.value}
						placeholder={uiDialog.placeholder}
						maxlength={uiDialog.maxLength}
						bind:this={uiDialogInputEl}
						on:input={(event) =>
							updateUiPromptValue((event.currentTarget as HTMLInputElement).value)}
					/>
				{/if}
			{:else if uiDialog.kind === 'roomAction'}
				<div class="ui-dialog-mode-toggle">
					<button
						type="button"
						class="ui-dialog-mode-btn {uiDialog.mode === 'create' ? 'active' : ''}"
						on:click={() => updateRoomActionMode('create')}
					>
						New
					</button>
					<button
						type="button"
						class="ui-dialog-mode-btn {uiDialog.mode === 'join' ? 'active' : ''}"
						on:click={() => updateRoomActionMode('join')}
					>
						Existing
					</button>
				</div>
				<input
					class="ui-dialog-input"
					type="text"
					value={uiDialog.roomName}
					placeholder="Room name"
					maxlength={20}
					bind:this={uiDialogInputEl}
					on:input={(event) =>
						updateRoomActionName((event.currentTarget as HTMLInputElement).value)}
				/>
			{/if}
		</div>
		<footer class="ui-dialog-actions">
			<button type="button" class="ui-dialog-btn" on:click={closeUiDialog}>
				{uiDialog.cancelLabel}
			</button>
			<button
				type="button"
				class="ui-dialog-btn primary {uiDialog.kind === 'confirm' && uiDialog.danger ? 'danger' : ''}"
				on:click={onUiDialogConfirm}
				disabled={(uiDialog.kind === 'prompt' && promptSubmitDisabled) ||
					(uiDialog.kind === 'roomAction' && roomActionSubmitDisabled)}
			>
				{uiDialog.confirmLabel}
			</button>
		</footer>
	</div>
{/if}

<section
	class="chat-shell"
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
			bind:chatListSearch
			on:select={onSidebarSelect}
			on:jumpOrigin={onJumpToBreakOrigin}
			on:toggleMenu={toggleLeftMenu}
			on:createRoom={createRoomFromMenu}
			on:renameRoom={(event) => void renameRoom(event.detail.roomId)}
		/>
	</div>

	<section class="chat-window">
		<header class="chat-header">
			{#if isMobileView}
				<button
					type="button"
					class="mobile-back-button"
					on:pointerdown|stopPropagation
					on:click|stopPropagation={showMobileRoomList}
					aria-label="Back to room list"
				>
					Rooms
				</button>
			{/if}
			<button type="button" class="room-title-button" on:click={openRoomDetails}>
				<span class="presence-dot"></span>
				<span class="title-text">
					<span class="title-main">{activeThread.name}</span>
					<span class="title-sub">
						{currentOnlineMembers.length} online
						{#if activeUnreadCount > 0}
							- {activeUnreadCount} unread
						{/if}
						{#if !isMember}
							- discoverable
						{/if}
					</span>
				</span>
			</button>

				<div class="header-actions" bind:this={headerActionsEl}>
					<button
						type="button"
						class="expiry-pill"
						on:click|stopPropagation={openRoomDetails}
						title="Remaining room lifetime"
						aria-label="Open room lifetime details"
					>
						{getRemainingHoursLabel(roomId)}
					</button>
					<button
						type="button"
						class="icon-button"
						on:click|stopPropagation={toggleRoomMenu}
						title="More options"
					>
						...
					</button>
						{#if showRoomMenu}
							<div class="room-menu">
								<button type="button" on:click|stopPropagation={toggleRoomSearch}>
								{showRoomSearch ? 'Hide search' : 'Search messages'}
							</button>
							<button type="button" on:click|stopPropagation={() => void renameRoom(roomId)}>
								Rename room
							</button>
							<button type="button" on:click|stopPropagation={toggleBreakSelectionMode}>
								{messageActionMode === 'break' ? 'Cancel Break Mode' : 'Start Break / New Topic'}
							</button>
							<button type="button" on:click|stopPropagation={toggleEditSelectionMode}>
								{messageActionMode === 'edit' ? 'Cancel Edit Mode' : 'Edit Message (Select One)'}
							</button>
							<button type="button" on:click|stopPropagation={toggleDeleteSelectionMode}>
								{messageActionMode === 'delete' ? 'Cancel Delete Mode' : 'Delete Message (Select One)'}
							</button>
							<button type="button" on:click|stopPropagation={() => markRoomAsRead(roomId)}>
								Mark read
							</button>
							<button type="button" on:click|stopPropagation={clearCurrentRoomMessages}>
								Clear local
							</button>
							{#if isMember}
								<button type="button" on:click|stopPropagation={() => void leaveCurrentRoom()}>
									Leave Room
								</button>
							{/if}
							{#if isActiveRoomAdmin}
								<button type="button" on:click|stopPropagation={() => void deleteCurrentRoomAsAdmin()}>
									Delete Room
								</button>
							{/if}
							<button type="button" on:click|stopPropagation={() => void disconnectAndWipe()}>
								Disconnect
							</button>
						</div>
					{/if}
			</div>
		</header>

		{#if typingIndicatorText}
			<div class="typing-indicator">{typingIndicatorText}</div>
		{/if}

		{#if showTrustedDevicePrompt}
			<div class="trusted-banner" role="status" aria-live="polite">
				<span>Trusted device? Enable encrypted history caching for faster loading.</span>
				<div class="trusted-actions">
					<button type="button" on:click={() => onTrustedDeviceChoice('yes')}>Yes</button>
					<button type="button" on:click={() => onTrustedDeviceChoice('no')}>No</button>
				</div>
			</div>
		{/if}

		{#if isSelectionMode}
			<div class="selection-banner">
				{#if messageActionMode === 'break'}
					Break mode active: click a message to start a new topic room.
				{:else if messageActionMode === 'edit'}
					Edit mode active: click one of your messages, then use the edit/delete buttons beside it.
				{:else if messageActionMode === 'delete'}
					Delete mode active: click one of your messages, then use the edit/delete buttons beside it.
				{/if}
			</div>
		{/if}

		{#if showRoomSearch}
			<div class="chat-search-row">
				<input type="text" bind:value={roomMessageSearch} placeholder="Search in this room" />
			</div>
		{/if}

			<ChatWindow
				bind:this={chatWindowRef}
				messages={currentMessages}
				{currentUserId}
				{roomMessageSearch}
				{expandedMessages}
				{isMember}
				{isSelectionMode}
				messageActionMode={messageActionMode}
				selectedMessageId={selectedActionMessageId}
				{focusMessageId}
				isLoadingOlder={isLoadingOlderHistory}
				hasMoreOlder={hasMoreOlderHistory}
				on:toggleExpand={(event) => toggleMessageExpanded(event.detail.messageId)}
				on:joinBreakRoom={onJoinBreakRoom}
				on:joinRoom={() => void joinCurrentRoom()}
				on:messageSelect={onMessageSelected}
				on:reply={onReplyRequest}
				on:editMessage={onEditMessageRequest}
				on:deleteMessage={onDeleteMessageRequest}
				on:editSelected={onSelectedMessageEdit}
				on:deleteSelected={onSelectedMessageDelete}
				on:requestOlder={onRequestOlderHistory}
				on:focusHandled={onFocusHandled}
			/>

		{#if isMember}
			<ChatComposer
				bind:draftMessage
				bind:attachedFile
				{activeReply}
				on:send={(event) => void sendMessage(event.detail)}
				on:typing={onComposerTyping}
				on:attach={handleComposerAttach}
				on:removeAttachment={handleComposerRemoveAttachment}
				on:cancelReply={clearReplyTarget}
			/>
		{/if}
	</section>

	<div class="online-pane">
		<OnlinePanel members={currentOnlineMembers} />
	</div>
</section>

{#if showRoomDetails}
	{#if isMobileView}
		<button
			type="button"
			class="mobile-info-backdrop"
			aria-label="Close room details"
			on:click={closeRoomDetails}
		></button>
	{/if}
	<section
		class="mobile-info-panel room-details-panel"
		class:desktop-room-panel={!isMobileView}
		role="dialog"
		aria-modal="true"
	>
		<header>
			<h3>{activeThread.name}</h3>
			<button type="button" on:click={closeRoomDetails}>Close</button>
		</header>
		<div class="mobile-info-content">
			<div class="room-details-card">
				<h4>Room Details</h4>
				<div class="room-detail-row">
					<span>Created</span>
					<strong>{formatDateTime(getRoomCreatedAt(roomId))}</strong>
				</div>
				<div class="room-detail-row">
					<span>Expires</span>
					<strong>{formatDateTime(getRoomExpiry(roomId))}</strong>
				</div>
			</div>

			<div class="room-actions">
				<button
					type="button"
					class="extend-room-button"
					on:click={requestRoomExtension}
					disabled={isExtendingRoom}
				>
					{isExtendingRoom ? 'Extending...' : 'Extend Room (24h)'}
				</button>
				<p>Manually extends this room and its messages for 24 hours.</p>
			</div>

			<h4 class="members-title">Members</h4>
			{#if currentOnlineMembers.length === 0}
				<div class="empty-label">No online members.</div>
			{:else}
				{#each currentOnlineMembers as member (member.id)}
					<div class="online-member">
						<span class="member-dot"></span>
						<div>
							<div class="member-name">{member.name}</div>
							<div class="member-meta">Joined {formatDateTime(member.joinedAt)}</div>
						</div>
						{#if isActiveRoomAdmin && normalizeIdentifier(member.id) !== normalizeIdentifier(currentUserId)}
							<button
								type="button"
								class="member-remove-button"
								on:click={() => void removeMemberFromRoom(member.id)}
							>
								Remove
							</button>
						{/if}
					</div>
				{/each}
			{/if}
		</div>
	</section>
{/if}

<style>
	.chat-shell {
		--panel-border: #d9dee8;
		--panel-shadow: 0 10px 26px rgba(15, 23, 42, 0.08);
		height: calc(100vh - 72px);
		min-height: 620px;
		display: grid;
		grid-template-columns: 330px minmax(0, 1fr) 280px;
		gap: 0.75rem;
		padding: 0.75rem;
		box-sizing: border-box;
		background:
			radial-gradient(1400px 480px at -5% -30%, rgba(80, 116, 255, 0.09) 0%, transparent 58%),
			radial-gradient(1200px 420px at 110% 0%, rgba(20, 184, 166, 0.09) 0%, transparent 55%),
			#e9edf3;
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
		background: #f8fafd;
	}

	.sidebar-pane {
		display: flex;
	}

	.chat-window {
		display: flex;
		flex-direction: column;
		overflow: hidden;
		background: linear-gradient(180deg, #fcfdff 0%, #f4f6fa 100%);
	}

	.online-pane {
		display: flex;
		background: linear-gradient(180deg, #fbfcff 0%, #f2f5fa 100%);
	}

	.chat-header {
		position: relative;
		background: #fcfcfd;
		border-bottom: 1px solid #e2e2e7;
		padding: 0.8rem 1rem;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.85rem;
	}

	.mobile-back-button {
		display: none;
		border: 1px solid #cdced4;
		background: #f8f8f9;
		color: #35353d;
		border-radius: 999px;
		padding: 0.35rem 0.65rem;
		font-size: 0.78rem;
		font-weight: 600;
		cursor: pointer;
		flex-shrink: 0;
	}

	.room-title-button {
		display: flex;
		align-items: center;
		gap: 0.55rem;
		color: #2e2e36;
		min-width: 0;
		flex: 1;
		border: none;
		background: transparent;
		padding: 0;
		margin: 0;
		text-align: left;
		cursor: pointer;
	}

	.room-title-button:focus-visible {
		outline: 2px solid #8f8f98;
		outline-offset: 4px;
		border-radius: 8px;
	}

	.presence-dot {
		width: 10px;
		height: 10px;
		border-radius: 50%;
		background: #22c55e;
	}

	.title-text {
		display: inline-flex;
		flex-direction: column;
		align-items: flex-start;
		min-width: 0;
	}

	.title-main {
		font-size: 0.98rem;
		font-weight: 700;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.title-sub {
		font-size: 0.76rem;
		color: #6d6d76;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.header-actions {
		display: flex;
		align-items: center;
		gap: 0.45rem;
		position: relative;
		cursor: default;
		flex-shrink: 0;
	}

	.expiry-pill {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-width: 3.1rem;
		height: 1.85rem;
		padding: 0 0.48rem;
		border-radius: 999px;
		border: 1px solid #d4d4da;
		background: #f5f5f7;
		color: #414149;
		font-size: 0.76rem;
		font-weight: 700;
		letter-spacing: 0.01em;
		cursor: pointer;
	}

	.expiry-pill:hover {
		background: #eeeef1;
	}

	.expiry-pill:focus-visible {
		outline: 2px solid #8f8f98;
		outline-offset: 2px;
	}

	.icon-button {
		border: 1px solid #d2d2d8;
		background: #f7f7f8;
		border-radius: 6px;
		padding: 0.35rem 0.55rem;
		font-size: 0.78rem;
		cursor: pointer;
		color: #33333b;
	}

	.room-menu {
		position: absolute;
		top: calc(100% + 6px);
		right: 0;
		background: #fcfcfd;
		border: 1px solid #dedee4;
		border-radius: 8px;
		box-shadow: 0 12px 24px rgba(0, 0, 0, 0.1);
		overflow: hidden;
		min-width: 170px;
		z-index: 100;
	}

	.room-menu button {
		width: 100%;
		border: none;
		background: #fcfcfd;
		padding: 0.55rem 0.75rem;
		text-align: left;
		font-size: 0.84rem;
		cursor: pointer;
	}

	.room-menu button:hover {
		background: #f1f1f3;
	}

	.selection-banner {
		padding: 0.45rem 0.9rem;
		background: #f1f1f3;
		border-bottom: 1px solid #dfdfe4;
		font-size: 0.8rem;
		color: #3a3a42;
	}

	.typing-indicator {
		padding: 0.35rem 0.9rem;
		border-bottom: 1px solid #e4e4e8;
		background: #fafafc;
		color: #666873;
		font-size: 0.75rem;
		line-height: 1.2;
	}

	.trusted-banner {
		padding: 0.5rem 0.9rem;
		border-bottom: 1px solid #e2e2e7;
		background: #f8f8fb;
		color: #383844;
		font-size: 0.76rem;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
	}

	.trusted-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
	}

	.trusted-actions button {
		border: 1px solid #d3d3da;
		background: #ffffff;
		color: #2f2f37;
		border-radius: 999px;
		font-size: 0.72rem;
		padding: 0.18rem 0.54rem;
		cursor: pointer;
	}

	.trusted-actions button:hover {
		background: #f2f2f6;
	}

	.chat-search-row {
		padding: 0.65rem 0.9rem;
		background: #fcfcfd;
		border-bottom: 1px solid #e3e3e8;
	}

	.chat-search-row input {
		width: 100%;
		border: 1px solid #d6d6dc;
		border-radius: 8px;
		padding: 0.55rem 0.7rem;
		font-size: 0.9rem;
		background: #fafafb;
		color: #2a2a31;
	}

	.online-member {
		display: flex;
		align-items: center;
		gap: 0.52rem;
		padding: 0.45rem 0.2rem;
	}

	.member-dot {
		width: 9px;
		height: 9px;
		border-radius: 50%;
		background: #22c55e;
	}

	.member-name {
		font-size: 0.88rem;
		color: #141414;
	}

	.member-meta {
		font-size: 0.75rem;
		color: #676767;
	}

	.member-remove-button {
		margin-left: auto;
		border: 1px solid #d6d6dc;
		background: #ffffff;
		color: #3a3a42;
		border-radius: 8px;
		padding: 0.22rem 0.5rem;
		font-size: 0.72rem;
		cursor: pointer;
	}

	.member-remove-button:hover {
		background: #f1f1f4;
	}

	.empty-label {
		color: #666666;
		font-size: 0.84rem;
		padding: 1rem;
	}

	.mobile-info-backdrop {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.45);
		border: none;
		z-index: 150;
	}

	.mobile-info-panel {
		position: fixed;
		right: 0;
		top: 0;
		height: 100vh;
		width: min(92vw, 320px);
		background: #fbfcfe;
		z-index: 160;
		box-shadow: -14px 0 30px rgba(0, 0, 0, 0.24);
		display: flex;
		flex-direction: column;
	}

	.desktop-room-panel {
		top: 84px;
		right: 18px;
		height: auto;
		width: min(34vw, 360px);
		max-height: calc(100vh - 104px);
		border-radius: 14px;
		border: 1px solid #d7dfeb;
		box-shadow: 0 18px 42px rgba(15, 23, 42, 0.22);
	}

	.mobile-info-panel header {
		padding: 0.9rem 1rem;
		border-bottom: 1px solid #dddddd;
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.mobile-info-panel header h3 {
		margin: 0;
		font-size: 1rem;
	}

	.mobile-info-panel header button {
		border: 1px solid #c9c9c9;
		background: #ffffff;
		border-radius: 7px;
		padding: 0.32rem 0.5rem;
		cursor: pointer;
		color: #111111;
	}

	.mobile-info-content {
		padding: 0.7rem 0.85rem;
		overflow: auto;
	}

	.room-actions {
		margin-bottom: 0.9rem;
		padding: 0.75rem;
		border: 1px solid #dddddf;
		border-radius: 10px;
		background: #f4f4f5;
	}

	.room-details-card {
		margin-bottom: 0.9rem;
		padding: 0.75rem;
		border: 1px solid #dddddf;
		border-radius: 10px;
		background: #f4f4f5;
	}

	.room-details-card h4 {
		margin: 0 0 0.5rem;
		font-size: 0.88rem;
		color: #111111;
	}

	.room-detail-row {
		display: flex;
		justify-content: space-between;
		align-items: baseline;
		gap: 0.65rem;
		font-size: 0.8rem;
		color: #5c5c5c;
	}

	.room-detail-row + .room-detail-row {
		margin-top: 0.35rem;
	}

	.room-detail-row strong {
		color: #111111;
		font-weight: 600;
	}

	.members-title {
		margin: 0 0 0.35rem;
		font-size: 0.88rem;
		color: #111111;
	}

	.room-actions p {
		margin: 0.45rem 0 0;
		font-size: 0.78rem;
		color: #5c5c5c;
	}

	.extend-room-button {
		width: 100%;
		border: 1px solid #111111;
		background: #111111;
		color: #ffffff;
		border-radius: 8px;
		padding: 0.48rem 0.65rem;
		font-size: 0.84rem;
		font-weight: 600;
		cursor: pointer;
	}

	.extend-room-button:disabled {
		opacity: 0.7;
		cursor: not-allowed;
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

	.ui-dialog-backdrop {
		position: fixed;
		inset: 0;
		border: none;
		background: rgba(12, 12, 16, 0.5);
		z-index: 520;
	}

	.ui-dialog {
		position: fixed;
		left: 50%;
		top: 50%;
		transform: translate(-50%, -50%);
		width: min(92vw, 460px);
		background: #fcfcfd;
		border: 1px solid #d9d9e0;
		border-radius: 14px;
		box-shadow: 0 24px 48px rgba(0, 0, 0, 0.22);
		z-index: 530;
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	.ui-dialog-header {
		padding: 0.9rem 1rem 0.45rem;
		border-bottom: 1px solid #ececf1;
	}

	.ui-dialog-header h3 {
		margin: 0;
		font-size: 1rem;
		color: #1f1f26;
	}

	.ui-dialog-body {
		padding: 0.8rem 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.65rem;
	}

	.ui-dialog-body p {
		margin: 0;
		font-size: 0.84rem;
		color: #4b4b56;
		line-height: 1.35;
	}

	.ui-dialog-input {
		width: 100%;
		border: 1px solid #d6d6dc;
		border-radius: 8px;
		padding: 0.55rem 0.65rem;
		font-size: 0.88rem;
		background: #ffffff;
		color: #17171d;
		box-sizing: border-box;
	}

	.ui-dialog-textarea {
		resize: vertical;
		min-height: 110px;
		font-family: inherit;
		line-height: 1.35;
	}

	.ui-dialog-mode-toggle {
		display: inline-flex;
		gap: 0.35rem;
	}

	.ui-dialog-mode-btn {
		border: 1px solid #d1d1d8;
		background: #f3f3f6;
		color: #393944;
		border-radius: 999px;
		padding: 0.28rem 0.74rem;
		font-size: 0.78rem;
		font-weight: 600;
		cursor: pointer;
	}

	.ui-dialog-mode-btn.active {
		background: #25252d;
		border-color: #25252d;
		color: #ffffff;
	}

	.ui-dialog-actions {
		display: flex;
		justify-content: flex-end;
		gap: 0.5rem;
		padding: 0.75rem 1rem 0.95rem;
		border-top: 1px solid #ececf1;
	}

	.ui-dialog-btn {
		border: 1px solid #d1d1d8;
		background: #f8f8fa;
		color: #34343e;
		border-radius: 8px;
		padding: 0.4rem 0.7rem;
		font-size: 0.8rem;
		font-weight: 600;
		cursor: pointer;
	}

	.ui-dialog-btn.primary {
		background: #222228;
		border-color: #222228;
		color: #ffffff;
	}

	.ui-dialog-btn.primary.danger {
		background: #8f1d1d;
		border-color: #8f1d1d;
	}

	.ui-dialog-btn:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	@media (max-width: 1199px) {
		.chat-shell {
			grid-template-columns: 290px minmax(0, 1fr);
		}

		.online-pane {
			display: none;
		}
	}

	@media (max-width: 900px) {
		.chat-shell {
			grid-template-columns: 1fr;
			height: calc(100dvh - 72px);
			min-height: 0;
			gap: 0.55rem;
			padding: 0.55rem;
		}

		.chat-header {
			padding: 0.68rem 0.75rem;
		}

		.expiry-pill {
			min-width: 2.7rem;
			height: 1.65rem;
			padding: 0 0.4rem;
			font-size: 0.71rem;
		}

		.mobile-back-button {
			display: inline-flex;
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

	}
</style>
