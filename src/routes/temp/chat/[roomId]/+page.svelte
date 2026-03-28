<script lang="ts">
	import { browser } from '$app/environment';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import ChatComposer from '$lib/components/chat/ChatComposer.svelte';
	import ChatStatusBars from '$lib/components/chat/ChatStatusBars.svelte';
	import ChatWindow from '$lib/components/chat/ChatWindow.svelte';
	import OnlinePanel from '$lib/components/chat/OnlinePanel.svelte';
	import PrivateAiChat from '$lib/components/chat/PrivateAiChat.svelte';
	import { resolveApiBase } from '$lib/config/apiBase';
	import { activeRoomPassword, authToken, currentUser, isDarkMode } from '$lib/store';
	import type {
		ChatMessage,
		ChatThread,
		ComposerMediaPayload,
		OnlineMember,
		ReplyTarget,
		RoomMeta,
		SocketEnvelope,
		ThreadStatus
	} from '$lib/types/chat';
	import {
		createMessageId,
		formatDateTime,
		getUTF8ByteLength,
		MESSAGE_TEXT_MAX_BYTES,
		normalizeIdentifier,
		normalizeMessageID,
		normalizeRoomIDValue,
		normalizeRoomNameValue,
		normalizeUsernameValue,
		parseOptionalTimestamp,
		toBool,
		toStringValue,
		toTimestamp
	} from '$lib/utils/chat/core';
	import {
		isEnvelope,
		resolveEnvelopePayloadRecord,
		resolveEnvelopeRoomID,
		resolveEnvelopeTargetUserID
	} from '$lib/utils/chat/envelope';
	import {
		DELETED_MESSAGE_PLACEHOLDER,
		buildReplySnippet,
		getMessagePreviewText,
		parseIncomingMessage,
		parseMember,
		toWireMessage
	} from '$lib/utils/chat/messages';
	import {
		applyReadProgress as applyReadProgressState,
		getLastReadTimestamp as getLastReadTimestampState,
		getUnreadStartMessageId as getUnreadStartMessageIdState
	} from '$lib/utils/chat/readProgress';
	import {
		createThread as createThreadState,
		dedupeMembers as dedupeMembersState,
		ensureOnlineSeed as ensureOnlineSeedState,
		ensureRoomMeta as ensureRoomMetaState,
		ensureRoomThread as ensureRoomThreadState,
		markRoomAsRead as markRoomAsReadState,
		mergeMessagesState,
		removeOnlineMember as removeOnlineMemberState,
		upsertMessageState,
		upsertOnlineMember as upsertOnlineMemberState,
		applyMessageDeleteState,
		applyMessageEditState,
		applyMessageReactionsState
	} from '$lib/utils/chat/pageState';
	import {
		getRemainingHoursLabel as getRemainingHoursLabelState,
		getRoomCreatedAt as getRoomCreatedAtState,
		getRoomExpiry as getRoomExpiryState
	} from '$lib/utils/chat/roomTiming';
	import { normalizeRoomPasswordValue } from '$lib/utils/chat/security';
	import { createTypingController } from '$lib/utils/chat/typingController';
	import { decryptText, encryptText } from '$lib/utils/crypto';
	import { getOrInitIdentity } from '$lib/utils/identity';
	import { getSessionToken, setSessionToken } from '$lib/utils/sessionToken';
	import { generateUsername } from '$lib/utils/usernameGenerator';
	import { globalMessages, initGlobalSocket, sendSocketPayload, subscribeToRooms } from '$lib/ws';
	import { onDestroy, onMount } from 'svelte';

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = resolveApiBase(API_BASE_RAW);
	const TYPING_PING_INTERVAL_MS = 3000;
	const TYPING_STOP_DELAY_MS = 5000;
	const TYPING_SAFETY_TIMEOUT_MS = 7000;

	type RoomFeatureFlags = {
		aiEnabled: boolean;
		e2eEnabled: boolean;
	};

	type NoticeTone = 'info' | 'success' | 'error';
	type TempMessageContextAction = 'reply' | 'edit' | 'delete' | 'discussion' | 'pin' | 'branch';

	let roomId = '';
	let roomNameFromURL = '';
	let identityReady = false;
	let isLoadingRoom = true;
	let loadingLabel = 'Joining temp room...';
	let loadingError = '';
	let roomThreads: ChatThread[] = [];
	let messagesByRoom: Record<string, ChatMessage[]> = {};
	let onlineByRoom: Record<string, OnlineMember[]> = {};
	let roomMetaById: Record<string, RoomMeta> = {};
	let unreadAnchorByRoom: Record<string, string> = {};
	let typingUsersByRoom: Record<string, Record<string, { name: string; expiresAt: number }>> = {};
	let historyLoadingByRoom: Record<string, boolean> = {};
	let historyHasMoreByRoom: Record<string, boolean> = {};
	let expandedMessages: Record<string, boolean> = {};
	let draftMessage = '';
	let attachedFile: File | null = null;
	let activeReply: ReplyTarget | null = null;
	let showRoomSearch = false;
	let roomMessageSearch = '';
	let showPrivateAiChat = false;
	let showDetailsDrawer = false;
	let roomCode = '';
	let roomMembershipSyncing = false;
	let roomMembershipSynced = false;
	let roomExpiryTickMs = Date.now();
	let roomExpiryTicker: ReturnType<typeof setInterval> | null = null;
	let serverClockOffsetMs = 0;
	let lastInitializedRoomId = '';
	let noticeText = '';
	let noticeTone: NoticeTone = 'info';
	let noticeTimer: ReturnType<typeof setTimeout> | null = null;
	let isExtendingRoom = false;
	let isLeavingRoom = false;
	let isDeletingRoom = false;
	let lastMembershipRoomId = '';
	let currentUserId = '';
	let currentUsername = 'Guest';

	const hiddenContextActions: TempMessageContextAction[] = ['discussion', 'pin', 'branch'];

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

	$: roomId = normalizeRoomIDValue($page.params.roomId ?? '');
	$: roomNameFromURL = normalizeRoomNameValue($page.url.searchParams.get('name') || '');
	$: if (roomId && roomId !== lastInitializedRoomId) {
		initializeRoomState(roomId);
		lastInitializedRoomId = roomId;
	}
	$: activeThread = roomThreads.find((thread) => thread.id === roomId) ?? null;
	$: isMember =
		($page.url.searchParams.get('member') ?? '') === '1' || activeThread?.status === 'joined';
	$: currentMessages = messagesByRoom[roomId] ?? [];
	$: currentOnlineMembers = prioritizeOnlineMembersForViewer(
		onlineByRoom[roomId] ?? [],
		currentUserId
	);
	$: currentRoomName = activeThread?.name || roomNameFromURL || 'Temp room';
	$: activeRoomFeatures = parseRoomFeatureFlags(
		{
			aiEnabled: activeThread?.aiEnabled,
			e2eEnabled: activeThread?.e2eEnabled
		},
		{ aiEnabled: true, e2eEnabled: false }
	);
	$: activeRoomAllowsAI = activeRoomFeatures.aiEnabled && !activeRoomFeatures.e2eEnabled;
	$: activeUnreadCount = activeThread?.unread ?? 0;
	$: activeRoomCreatedAt = getRoomCreatedAtState(roomMetaById, roomId);
	$: activeRoomExpiresAt = getRoomExpiryState(roomMetaById, roomId);
	$: activeRoomRemainingLabel = getRemainingHoursLabelState(
		roomMetaById,
		roomId,
		roomExpiryTickMs,
		getApproxServerNowMs
	);
	$: activeRoomIsAdmin = Boolean(activeThread?.isAdmin);
	$: activeRoomRequiresPassword = Boolean(activeThread?.requiresPassword);
	$: typingNames = typingController.getActiveTypingUsers(roomId, currentUserId);
	$: typingIndicatorText = formatTypingIndicatorText(typingNames);
	$: typingNamesPreview = formatTypingNamesPreview(typingNames);
	$: lastReadTimestamp = getLastReadTimestampState({
		targetRoomId: roomId,
		roomThreads,
		messagesByRoom,
		currentUserId
	});
	$: firstUnreadMessageId = getUnreadStartMessageIdState({
		targetRoomId: roomId,
		roomThreads,
		messagesByRoom,
		currentUserId
	});
	$: isRoomExpired =
		activeRoomExpiresAt > 0 && getApproxServerNowMs(roomExpiryTickMs) >= activeRoomExpiresAt;
	$: if (browser && identityReady && currentUserId && currentUsername) {
		initGlobalSocket(currentUserId, currentUsername);
	}
	$: if (browser && identityReady && roomId) {
		subscribeToRooms([roomId], { force: true });
	}
	$: if (browser && identityReady && roomId && $globalMessages) {
		void handleGlobalPayload($globalMessages.payload);
	}
	$: currentUserId = $currentUser?.id || '';
	$: currentUsername = $currentUser?.username || 'Guest';
	$: if (browser && identityReady && roomId && roomId !== lastMembershipRoomId) {
		lastMembershipRoomId = roomId;
		void syncRoomMembership(roomId).then(() => refreshRoomMetaFromServer(roomId));
	}

	function initializeRoomState(nextRoomId: string) {
		roomThreads = nextRoomId
			? [
					createThreadState(
						nextRoomId,
						formatFallbackRoomName,
						formatFallbackRoomName(nextRoomId, roomNameFromURL),
						'discoverable'
					)
				]
			: [];
		messagesByRoom = nextRoomId ? { [nextRoomId]: [] } : {};
		onlineByRoom = {};
		roomMetaById = {};
		unreadAnchorByRoom = {};
		typingUsersByRoom = {};
		historyLoadingByRoom = {};
		historyHasMoreByRoom = {};
		expandedMessages = {};
		activeReply = null;
		showRoomSearch = false;
		roomMessageSearch = '';
		showDetailsDrawer = false;
		loadingError = '';
		loadingLabel = 'Joining temp room...';
		isLoadingRoom = true;
		roomCode = '';
		roomMembershipSynced = false;
		roomMembershipSyncing = false;
	}

	function formatFallbackRoomName(targetRoomId: string, preferredName = '') {
		return (
			normalizeRoomNameValue(preferredName) || normalizeRoomNameValue(targetRoomId) || 'Temp room'
		);
	}

	function parseRoomFeatureFlags(
		source: Record<string, unknown>,
		fallback: RoomFeatureFlags
	): RoomFeatureFlags {
		const rawE2E =
			source.e2eEnabled ?? source.e2e_enabled ?? source.e2eeEnabled ?? source.e2ee_enabled;
		const e2eEnabled =
			rawE2E === undefined || rawE2E === null ? fallback.e2eEnabled : toBool(rawE2E);
		const rawAI = source.aiEnabled ?? source.ai_enabled;
		const aiEnabled = e2eEnabled
			? false
			: rawAI === undefined || rawAI === null
				? fallback.aiEnabled
				: toBool(rawAI);
		return { aiEnabled, e2eEnabled };
	}

	function syncServerClock(rawValue: unknown) {
		const serverNow = parseOptionalTimestamp(rawValue);
		if (serverNow > 0) {
			serverClockOffsetMs = serverNow - Date.now();
		}
	}

	function getApproxServerNowMs(tickMs = Date.now()) {
		return tickMs + serverClockOffsetMs;
	}

	function showNotice(message: string, tone: NoticeTone = 'info') {
		noticeText = message;
		noticeTone = tone;
		if (noticeTimer) {
			clearTimeout(noticeTimer);
		}
		noticeTimer = setTimeout(() => {
			noticeText = '';
			noticeTimer = null;
		}, 3400);
	}

	function applyActiveRoomPasswordToLocation(password: string) {
		if (!browser) {
			return;
		}
		const currentURL = new URL(window.location.href);
		currentURL.hash = password ? `key=${encodeURIComponent(password)}` : '';
		window.history.replaceState(null, '', currentURL.toString());
	}

	function syncRoomPasswordFromHash() {
		if (!browser) {
			return;
		}
		const hashValue = window.location.hash.startsWith('#')
			? window.location.hash.slice(1)
			: window.location.hash;
		const params = new URLSearchParams(hashValue);
		const normalizedPassword = (params.get('key') || '').trim();
		activeRoomPassword.set(normalizedPassword);
	}

	async function encryptMessageContent(content: string) {
		return encryptText(content, normalizeRoomPasswordValue($activeRoomPassword));
	}

	async function decryptMessageContent(content: string) {
		return decryptText(content, normalizeRoomPasswordValue($activeRoomPassword));
	}

	async function decryptChatMessage(message: ChatMessage) {
		if (!message.content) {
			return message;
		}
		const nextContent = await decryptMessageContent(message.content);
		if (nextContent === message.content) {
			return message;
		}
		return {
			...message,
			content: nextContent
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

	async function requestAnonymousSession(requestedUsername: string) {
		const normalizedRequested =
			normalizeUsernameValue(requestedUsername) ||
			normalizeUsernameValue(generateUsername()) ||
			'Guest';
		try {
			const response = await fetch(`${API_BASE}/api/auth/anonymous`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ username: normalizedRequested })
			});
			const payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
			if (!response.ok) {
				return null;
			}
			const token = toStringValue(payload.token).trim();
			const userRecord = (payload.user as Record<string, unknown> | undefined) ?? {};
			const username =
				normalizeUsernameValue(toStringValue(userRecord.username)) || normalizedRequested;
			if (!token) {
				return null;
			}
			return { token, username };
		} catch {
			return null;
		}
	}

	async function ensureIdentityAndMembership() {
		const identity = getOrInitIdentity();
		let resolvedUserId = normalizeIdentifier(identity.id) || identity.id;
		let resolvedUsername =
			normalizeUsernameValue(identity.username) ||
			normalizeUsernameValue(generateUsername()) ||
			'Guest';
		let token = getSessionToken() || ($authToken ?? '');
		if (!token) {
			const anonymousSession = await requestAnonymousSession(resolvedUsername);
			if (anonymousSession) {
				token = anonymousSession.token;
				resolvedUsername = anonymousSession.username;
				setSessionToken(token);
				authToken.set(token);
			}
		} else if (!$authToken) {
			authToken.set(token);
		}

		currentUser.set({
			id: resolvedUserId,
			username: resolvedUsername
		});
		identityReady = true;
	}

	async function promptForRoomPasswordIfNeeded(payload: Record<string, unknown>) {
		const requiresPassword = toBool(payload.requiresPassword ?? payload.requires_password);
		if (!requiresPassword || !browser) {
			return false;
		}
		const currentPassword = ($activeRoomPassword || '').trim();
		const enteredPassword = window.prompt(
			'This room is password protected. Enter the room password.',
			currentPassword
		);
		if (enteredPassword === null) {
			return false;
		}
		const normalizedPassword = enteredPassword.trim().slice(0, 32);
		activeRoomPassword.set(normalizedPassword);
		applyActiveRoomPasswordToLocation(normalizedPassword);
		return normalizedPassword !== '';
	}

	async function syncRoomMembership(targetRoomId: string) {
		const normalizedRoomId = normalizeRoomIDValue(targetRoomId);
		if (!browser || !normalizedRoomId || roomMembershipSyncing) {
			return;
		}

		roomMembershipSyncing = true;
		loadingError = '';
		try {
			const userId = normalizeIdentifier(currentUserId);
			const username = normalizeUsernameValue(currentUsername) || 'Guest';
			if (!userId) {
				throw new Error('Unable to identify the current user.');
			}

			const doJoin = async () =>
				fetch(`${API_BASE}/api/rooms/join`, {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({
						roomId: normalizedRoomId,
						roomPassword: ($activeRoomPassword || '').trim(),
						username,
						userId,
						mode: 'join'
					})
				});

			let response = await doJoin();
			let payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
			if (response.status === 401 && (await promptForRoomPasswordIfNeeded(payload))) {
				response = await doJoin();
				payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
			}
			if (!response.ok) {
				throw new Error(toStringValue(payload.error) || 'Failed to join temp room');
			}

			syncServerClock(payload.serverNow ?? payload.server_now);
			const joinedRoomName =
				normalizeRoomNameValue(toStringValue(payload.roomName)) ||
				formatFallbackRoomName(normalizedRoomId, roomNameFromURL);
			const createdAt = toTimestamp(payload.createdAt);
			const expiresAt = parseOptionalTimestamp(payload.expiresAt ?? payload.expires_at);
			const nextFeatureFlags = parseRoomFeatureFlags(payload, {
				aiEnabled: true,
				e2eEnabled: false
			});
			roomCode = toStringValue(payload.roomCode ?? payload.room_code).trim();

			ensureRoomThread(normalizedRoomId, joinedRoomName, 'joined');
			roomThreads = roomThreads.map((thread) =>
				thread.id === normalizedRoomId
					? {
							...thread,
							name: joinedRoomName,
							status: 'joined',
							memberCount:
								Number(payload.memberCount ?? thread.memberCount ?? 0) || thread.memberCount,
							isAdmin: toBool(payload.isAdmin ?? payload.is_admin),
							adminCode: toStringValue(payload.adminCode ?? payload.admin_code).trim(),
							requiresPassword: toBool(payload.requiresPassword ?? payload.requires_password),
							aiEnabled: nextFeatureFlags.aiEnabled,
							e2eEnabled: nextFeatureFlags.e2eEnabled
						}
					: thread
			);
			ensureRoomMeta(normalizedRoomId, createdAt, expiresAt);
			ensureOnlineSeed(normalizedRoomId);
			roomMembershipSynced = true;
			isLoadingRoom = false;
			loadingLabel = '';
		} catch (error) {
			const message = error instanceof Error ? error.message : 'Unable to join the temp room.';
			loadingError = message;
			isLoadingRoom = false;
		} finally {
			roomMembershipSyncing = false;
		}
	}

	async function refreshRoomMetaFromServer(targetRoomId: string) {
		const normalizedRoomId = normalizeRoomIDValue(targetRoomId);
		const normalizedUserId = normalizeIdentifier(currentUserId);
		if (!browser || !normalizedRoomId || !normalizedUserId) {
			return;
		}
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomId)}?userId=${encodeURIComponent(
					normalizedUserId
				)}`
			);
			const payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
			if (!response.ok) {
				return;
			}
			syncServerClock(payload.serverNow ?? payload.server_now);
			roomCode = toStringValue(payload.roomCode ?? payload.room_code).trim();
			const nextFeatureFlags = parseRoomFeatureFlags(payload, activeRoomFeatures);
			const createdAt = toTimestamp(payload.createdAt);
			const expiresAt = parseOptionalTimestamp(payload.expiresAt ?? payload.expires_at);
			ensureRoomMeta(normalizedRoomId, createdAt, expiresAt);
			roomThreads = roomThreads.map((thread) =>
				thread.id === normalizedRoomId
					? {
							...thread,
							name: normalizeRoomNameValue(toStringValue(payload.roomName)) || thread.name,
							memberCount:
								Number(payload.memberCount ?? thread.memberCount ?? 0) || thread.memberCount,
							isAdmin: toBool(payload.isAdmin ?? payload.is_admin),
							adminCode: toStringValue(payload.adminCode ?? payload.admin_code).trim(),
							requiresPassword: toBool(payload.requiresPassword ?? payload.requires_password),
							aiEnabled: nextFeatureFlags.aiEnabled,
							e2eEnabled: nextFeatureFlags.e2eEnabled
						}
					: thread
			);
		} catch {
			// Ignore background refresh errors.
		}
	}

	function createThread(id: string, nameOverride?: string, status: ThreadStatus = 'joined') {
		return createThreadState(id, formatFallbackRoomName, nameOverride, status);
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

	function dedupeMembers(members: OnlineMember[]) {
		return dedupeMembersState(members);
	}

	function upsertOnlineMember(targetRoomId: string, member: OnlineMember) {
		onlineByRoom = upsertOnlineMemberState(onlineByRoom, targetRoomId, member);
	}

	function removeOnlineMember(targetRoomId: string, memberId: string) {
		onlineByRoom = removeOnlineMemberState(onlineByRoom, targetRoomId, memberId);
	}

	function prioritizeOnlineMembersForViewer(members: OnlineMember[], viewerId: string) {
		const normalizedViewerId = normalizeIdentifier(viewerId);
		return [...members].sort((left, right) => {
			const leftIsViewer = normalizeIdentifier(left.id) === normalizedViewerId ? 0 : 1;
			const rightIsViewer = normalizeIdentifier(right.id) === normalizedViewerId ? 0 : 1;
			if (leftIsViewer !== rightIsViewer) {
				return leftIsViewer - rightIsViewer;
			}
			return (left.joinedAt ?? 0) - (right.joinedAt ?? 0);
		});
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
				formatRoomName: formatFallbackRoomName,
				getMessagePreviewText,
				createThread
			}
		);
		messagesByRoom = next.messagesByRoom;
		roomThreads = next.roomThreads;
		if (shouldCountUnread && normalizedRoomID) {
			const nextUnread =
				next.roomThreads.find((thread) => thread.id === normalizedRoomID)?.unread ?? 0;
			if (nextUnread > previousUnread && !unreadAnchorByRoom[normalizedRoomID]) {
				const roomMessages = next.messagesByRoom[normalizedRoomID] ?? [];
				const anchorIndex = Math.max(0, roomMessages.length - nextUnread);
				const anchorId = roomMessages[anchorIndex]?.id || message.id;
				unreadAnchorByRoom = {
					...unreadAnchorByRoom,
					[normalizedRoomID]: anchorId
				};
			}
		}
	}

	function mergeMessages(targetRoomId: string, incoming: ChatMessage[]) {
		const next = mergeMessagesState(messagesByRoom, roomThreads, targetRoomId, incoming, {
			formatRoomName: formatFallbackRoomName,
			getMessagePreviewText,
			createThread
		});
		messagesByRoom = next.messagesByRoom;
		roomThreads = next.roomThreads;
	}

	function addIncomingMessage(message: ChatMessage) {
		const isOwnMessage =
			normalizeIdentifier(message.senderId) !== '' &&
			normalizeIdentifier(message.senderId) === normalizeIdentifier(currentUserId);
		upsertMessage(message.roomId, message, !isOwnMessage);
	}

	function applyMessageEdit(targetRoomId: string, payload: unknown) {
		const next = applyMessageEditState(messagesByRoom, roomThreads, targetRoomId, payload, {
			formatRoomName: formatFallbackRoomName,
			getMessagePreviewText,
			createThread
		});
		if (!next.changed) {
			return;
		}
		messagesByRoom = next.messagesByRoom;
		roomThreads = next.roomThreads;
	}

	function applyMessageDelete(targetRoomId: string, payload: unknown) {
		const next = applyMessageDeleteState(
			messagesByRoom,
			roomThreads,
			targetRoomId,
			payload,
			DELETED_MESSAGE_PLACEHOLDER,
			{
				formatRoomName: formatFallbackRoomName,
				getMessagePreviewText,
				createThread
			}
		);
		if (!next.changed) {
			return;
		}
		messagesByRoom = next.messagesByRoom;
		roomThreads = next.roomThreads;
	}

	function applyMessageReactions(targetRoomId: string, payload: unknown) {
		const normalizedRoomId = normalizeRoomIDValue(targetRoomId);
		if (!normalizedRoomId) {
			return;
		}
		const next = applyMessageReactionsState(messagesByRoom, normalizedRoomId, payload);
		if (!next.changed) {
			return;
		}
		messagesByRoom = next.messagesByRoom;
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

	function setTypingIndicator(
		targetRoomId: string,
		userId: string,
		userName: string,
		expiresAt = Date.now() + TYPING_SAFETY_TIMEOUT_MS
	) {
		typingController.setTypingIndicator(targetRoomId, userId, userName, expiresAt);
	}

	function clearTypingIndicator(targetRoomId: string, userId: string) {
		typingController.clearTypingIndicator(targetRoomId, userId);
	}

	function handleTypingSignalPayload(value: unknown) {
		if (!value || typeof value !== 'object') {
			return false;
		}
		const source = value as Record<string, unknown>;
		const kind = toStringValue(source.type).trim().toLowerCase();
		if (kind !== 'typing_start' && kind !== 'typing_stop') {
			return false;
		}
		const payload =
			source.payload && typeof source.payload === 'object' && !Array.isArray(source.payload)
				? (source.payload as Record<string, unknown>)
				: source;
		const targetRoomId = normalizeRoomIDValue(
			toStringValue(source.roomId ?? source.room_id ?? payload.roomId ?? payload.room_id)
		);
		if (!targetRoomId) {
			return true;
		}
		const participant = parseMember(payload, Date.now());
		if (!participant) {
			return true;
		}
		if (kind === 'typing_start') {
			setTypingIndicator(targetRoomId, participant.id, participant.name);
		} else {
			clearTypingIndicator(targetRoomId, participant.id);
		}
		return true;
	}

	async function handleEnvelope(envelope: SocketEnvelope) {
		const targetRoomId = resolveEnvelopeRoomID(envelope);
		const kind = toStringValue(envelope.type).trim().toLowerCase();
		const payload = resolveEnvelopePayloadRecord(envelope);

		if (kind === 'history' || kind === 'recent_messages' || kind === 'initial_messages') {
			if (Array.isArray(envelope.payload)) {
				const history = await parseIncomingMessagesWithE2EE(envelope.payload, targetRoomId);
				if (history.length > 0) {
					mergeMessages(targetRoomId || roomId, history);
				}
			}
			return;
		}

		if (kind === 'new_message') {
			const message = await parseIncomingMessageWithE2EE(envelope.payload, targetRoomId || roomId);
			if (message) {
				addIncomingMessage(message);
			}
			return;
		}

		if (kind === 'online_list' && Array.isArray(envelope.payload) && targetRoomId) {
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

		if (kind === 'typing_start' || kind === 'typing_stop') {
			handleTypingSignalPayload(envelope);
			return;
		}

		if (kind === 'message_edit' && targetRoomId) {
			const decryptedPayload =
				typeof payload.content === 'string'
					? { ...payload, content: await decryptMessageContent(payload.content) }
					: payload;
			applyMessageEdit(targetRoomId, decryptedPayload);
			return;
		}

		if (kind === 'message_delete' && targetRoomId) {
			applyMessageDelete(targetRoomId, envelope.payload);
			return;
		}

		if (kind === 'message_reaction' && targetRoomId) {
			applyMessageReactions(targetRoomId, payload);
			return;
		}

		if (kind === 'room_renamed' && targetRoomId) {
			const nextRoomName = normalizeRoomNameValue(
				toStringValue(payload.roomName ?? payload.room_name)
			);
			if (!nextRoomName) {
				return;
			}
			roomThreads = roomThreads.map((thread) =>
				thread.id === targetRoomId ? { ...thread, name: nextRoomName } : thread
			);
			return;
		}

		if (kind === 'room_extended' && targetRoomId) {
			syncServerClock(payload.serverNow ?? payload.server_now);
			const expiresAt = parseOptionalTimestamp(payload.expiresAt ?? payload.expires_at);
			if (expiresAt > 0) {
				ensureRoomMeta(targetRoomId, getRoomCreatedAtState(roomMetaById, targetRoomId), expiresAt);
			}
			return;
		}

		if (kind === 'room_deleted' || kind === 'room_expired') {
			showNotice('This temp room is no longer available.', 'error');
			await goto('/temp/login');
			return;
		}

		if (kind === 'member_removed') {
			const targetUserId = resolveEnvelopeTargetUserID(envelope);
			if (targetUserId && targetUserId === normalizeIdentifier(currentUserId)) {
				showNotice('You were removed from this room.', 'error');
				await goto('/temp/login');
				return;
			}
		}
	}

	async function handleGlobalPayload(payload: unknown) {
		if (!payload) {
			return;
		}
		if (Array.isArray(payload)) {
			const parsed = await parseIncomingMessagesWithE2EE(payload, roomId);
			if (parsed.length > 0) {
				mergeMessages(roomId, parsed);
			}
			return;
		}
		if (handleTypingSignalPayload(payload)) {
			return;
		}
		if (isEnvelope(payload)) {
			await handleEnvelope(payload);
			return;
		}
		const single = await parseIncomingMessageWithE2EE(payload, roomId);
		if (single) {
			addIncomingMessage(single);
		}
	}

	function sendTypingStop() {
		typingController.sendTypingStop();
	}

	function onComposerTyping(event: CustomEvent<{ value: string }>) {
		typingController.onComposerTyping(event.detail?.value || '');
	}

	async function sendMessage(payload?: ComposerMediaPayload) {
		if (!roomId || !isMember || isRoomExpired) {
			showNotice('Join an active room before sending messages.', 'error');
			return;
		}

		const text = (payload?.text ?? draftMessage).trim();
		if (getUTF8ByteLength(text) > MESSAGE_TEXT_MAX_BYTES) {
			showNotice('Message exceeds the size limit.', 'error');
			return;
		}
		const payloadType = (payload?.type || '').trim().toLowerCase();
		const payloadContent = payload?.content?.trim() ?? '';
		const isMediaMessage =
			payloadType !== '' &&
			payloadType !== 'task' &&
			payloadType !== 'beacon' &&
			payloadContent !== '';
		const isTaskMessage = payloadType === 'task' && payloadContent !== '';
		const isBeaconMessage = payloadType === 'beacon' && payloadContent !== '';
		if (!text && !isMediaMessage && !isTaskMessage && !isBeaconMessage) {
			return;
		}

		const replyTarget = activeReply;
		const replyToMessageId = replyTarget ? normalizeMessageID(replyTarget.messageId) : '';
		const replyToSnippet = replyToMessageId
			? buildReplySnippet(replyTarget?.senderName || '', replyTarget?.content || '')
			: '';
		const outgoing: ChatMessage = {
			id: createMessageId(roomId),
			roomId,
			senderId: currentUserId,
			senderName: currentUsername,
			content: isTaskMessage || isBeaconMessage ? payloadContent : text,
			type: payloadType || 'text',
			mediaUrl: isMediaMessage ? payloadContent : '',
			mediaType: isMediaMessage ? payloadType : '',
			fileName: payload?.fileName?.trim() ?? '',
			replyToMessageId,
			replyToSnippet,
			createdAt: Date.now(),
			pending: true
		};

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

	function onMessageReactionToggle(event: CustomEvent<{ messageId: string; emoji: string }>) {
		if (!roomId || !isMember) {
			return;
		}
		const messageId = normalizeMessageID(event.detail?.messageId || '');
		const emoji = (event.detail?.emoji || '').trim();
		if (!messageId || !emoji) {
			return;
		}
		sendSocketPayload({
			type: 'message_reaction',
			roomId,
			messageId,
			emoji
		});
	}

	async function editMessage(messageId: string, currentContent: string) {
		if (!browser || !roomId) {
			return;
		}
		const nextContentRaw = window.prompt('Edit message', currentContent);
		if (nextContentRaw === null) {
			return;
		}
		const nextContent = nextContentRaw.trim();
		if (!nextContent || nextContent === currentContent.trim()) {
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

	function deleteMessage(messageId: string) {
		if (!browser || !roomId) {
			return;
		}
		if (!window.confirm('Delete this message?')) {
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

	function onMessageContextAction(
		event: CustomEvent<{ messageId: string; action: TempMessageContextAction }>
	) {
		const messageId = normalizeMessageID(event.detail.messageId);
		const action = event.detail.action;
		if (!messageId || !roomId) {
			return;
		}
		const message = currentMessages.find((entry) => normalizeMessageID(entry.id) === messageId);
		if (!message) {
			return;
		}
		if (action === 'reply') {
			onReplyRequest(
				new CustomEvent('reply', {
					detail: {
						messageId: message.id,
						senderName: message.senderName,
						content: message.content
					}
				})
			);
			return;
		}
		if (action === 'edit') {
			void editMessage(messageId, message.content);
			return;
		}
		if (action !== 'delete') {
			return;
		}
		deleteMessage(messageId);
	}

	function toggleMessageExpanded(messageId: string) {
		expandedMessages = {
			...expandedMessages,
			[messageId]: !expandedMessages[messageId]
		};
	}

	function onChatReadProgress(
		event: CustomEvent<{ isNearBottom: boolean; lastSeenMessageId: string }>
	) {
		if (!roomId || roomMessageSearch.trim()) {
			return;
		}
		applyReadProgress(roomId, event.detail?.lastSeenMessageId || '');
	}

	async function loadOlderMessages(targetRoomId: string) {
		const normalizedRoomId = normalizeRoomIDValue(targetRoomId);
		if (!normalizedRoomId || historyLoadingByRoom[normalizedRoomId]) {
			return;
		}
		if (historyHasMoreByRoom[normalizedRoomId] === false) {
			return;
		}

		const existing = messagesByRoom[normalizedRoomId] ?? [];
		const before = existing[0]?.id || '';
		const beforeCreatedAt = existing[0]?.createdAt || 0;
		historyLoadingByRoom = {
			...historyLoadingByRoom,
			[normalizedRoomId]: true
		};
		try {
			const userIdQuery = currentUserId
				? `&userId=${encodeURIComponent(normalizeIdentifier(currentUserId))}`
				: '';
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomId)}/messages?before=${encodeURIComponent(
					before
				)}&beforeCreatedAt=${encodeURIComponent(String(beforeCreatedAt))}${userIdQuery}&limit=50`
			);
			const payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
			if (!response.ok) {
				throw new Error(toStringValue(payload.error) || 'Failed to load older messages');
			}
			const incomingPayload = Array.isArray(payload.messages) ? payload.messages : [];
			const incoming = await parseIncomingMessagesWithE2EE(incomingPayload, normalizedRoomId);
			if (incoming.length > 0) {
				mergeMessages(normalizedRoomId, incoming);
			}
			historyHasMoreByRoom = {
				...historyHasMoreByRoom,
				[normalizedRoomId]: incomingPayload.length >= 50
			};
		} catch (error) {
			showNotice(error instanceof Error ? error.message : 'Failed to load older messages', 'error');
		} finally {
			historyLoadingByRoom = {
				...historyLoadingByRoom,
				[normalizedRoomId]: false
			};
		}
	}

	function copyText(value: string, successMessage: string) {
		if (!browser || !value.trim()) {
			return;
		}
		const fallbackCopy = () => {
			const textarea = document.createElement('textarea');
			textarea.value = value;
			textarea.setAttribute('readonly', 'true');
			textarea.style.position = 'fixed';
			textarea.style.opacity = '0';
			document.body.appendChild(textarea);
			textarea.select();
			document.execCommand('copy');
			document.body.removeChild(textarea);
			showNotice(successMessage, 'success');
		};
		navigator.clipboard?.writeText(value).then(
			() => showNotice(successMessage, 'success'),
			() => fallbackCopy()
		) ?? fallbackCopy();
	}

	function copyInviteLink() {
		if (!browser || !roomId) {
			return;
		}
		const normalizedPassword = ($activeRoomPassword || '').trim();
		const inviteHash = normalizedPassword ? `#key=${encodeURIComponent(normalizedPassword)}` : '';
		copyText(
			`${window.location.origin}/temp/chat/${encodeURIComponent(roomId)}${inviteHash}`,
			'Invite link copied.'
		);
	}

	function copyRoomCode() {
		if (!roomCode) {
			return;
		}
		copyText(roomCode, 'Room code copied.');
	}

	async function renameRoom() {
		if (!browser || !roomId || !isMember) {
			return;
		}
		const nextNameRaw = window.prompt('Rename room', currentRoomName);
		if (nextNameRaw === null) {
			return;
		}
		const nextName = normalizeRoomNameValue(nextNameRaw);
		if (!nextName || nextName === currentRoomName) {
			return;
		}
		try {
			const response = await fetch(`${API_BASE}/api/rooms/rename`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					roomId,
					roomName: nextName
				})
			});
			const payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
			if (!response.ok) {
				throw new Error(toStringValue(payload.error) || 'Failed to rename room');
			}
			roomThreads = roomThreads.map((thread) =>
				thread.id === roomId ? { ...thread, name: nextName } : thread
			);
			const params = new URLSearchParams($page.url.searchParams.toString());
			params.set('name', nextName);
			await goto(`/temp/chat/${encodeURIComponent(roomId)}?${params.toString()}`, {
				replaceState: true,
				noScroll: true,
				keepFocus: true
			});
			showNotice('Room renamed.', 'success');
		} catch (error) {
			showNotice(error instanceof Error ? error.message : 'Failed to rename room', 'error');
		}
	}

	async function extendRoom() {
		if (!roomId || isExtendingRoom || isRoomExpired) {
			return;
		}
		isExtendingRoom = true;
		try {
			const response = await fetch(`${API_BASE}/api/rooms/extend`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ roomId })
			});
			const payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
			if (!response.ok) {
				throw new Error(toStringValue(payload.error) || 'Failed to extend room');
			}
			syncServerClock(payload.serverNow ?? payload.server_now);
			const expiresAt = parseOptionalTimestamp(payload.expiresAt ?? payload.expires_at);
			if (expiresAt > 0) {
				ensureRoomMeta(roomId, activeRoomCreatedAt, expiresAt);
			}
			showNotice('Room extended.', 'success');
		} catch (error) {
			showNotice(error instanceof Error ? error.message : 'Failed to extend room', 'error');
		} finally {
			isExtendingRoom = false;
		}
	}

	async function leaveRoom() {
		if (!browser || !roomId || !currentUserId || isLeavingRoom) {
			return;
		}
		if (!window.confirm('Leave this room?')) {
			return;
		}
		isLeavingRoom = true;
		try {
			const response = await fetch(`${API_BASE}/api/rooms/leave`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					roomId,
					userId: normalizeIdentifier(currentUserId)
				})
			});
			const payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
			if (!response.ok) {
				throw new Error(toStringValue(payload.error) || 'Failed to leave room');
			}
			await goto('/temp/login');
		} catch (error) {
			showNotice(error instanceof Error ? error.message : 'Failed to leave room', 'error');
		} finally {
			isLeavingRoom = false;
		}
	}

	async function deleteRoom() {
		if (!browser || !roomId || !activeRoomIsAdmin || isDeletingRoom) {
			return;
		}
		if (!window.confirm('Delete this room for everyone?')) {
			return;
		}
		isDeletingRoom = true;
		try {
			const response = await fetch(`${API_BASE}/api/rooms/delete`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					roomId,
					userId: normalizeIdentifier(currentUserId)
				})
			});
			const payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
			if (!response.ok) {
				throw new Error(toStringValue(payload.error) || 'Failed to delete room');
			}
			await goto('/temp/login');
		} catch (error) {
			showNotice(error instanceof Error ? error.message : 'Failed to delete room', 'error');
		} finally {
			isDeletingRoom = false;
		}
	}

	function handleComposerAttach(event: CustomEvent<{ file: File | null; error?: string }>) {
		if (event.detail?.error) {
			showNotice(event.detail.error, 'error');
		}
	}

	function handleComposerRemoveAttachment() {
		attachedFile = null;
	}

	function toggleRoomSearch() {
		showRoomSearch = !showRoomSearch;
		if (!showRoomSearch) {
			roomMessageSearch = '';
		}
	}

	function openPrivateAiChat() {
		if (!activeRoomAllowsAI) {
			showNotice('AI is disabled for this room.', 'error');
			return;
		}
		showPrivateAiChat = true;
	}

	function closePrivateAiChat() {
		showPrivateAiChat = false;
	}

	function formatTypingNamesPreview(names: string[]) {
		if (names.length === 0) {
			return '';
		}
		return names.slice(0, 2).join(', ');
	}

	function formatTypingIndicatorText(names: string[]) {
		if (names.length === 0) {
			return '';
		}
		if (names.length === 1) {
			return `${names[0]} is typing...`;
		}
		if (names.length === 2) {
			return `${names[0]} and ${names[1]} are typing...`;
		}
		return `${names[0]}, ${names[1]}, and ${names.length - 2} others are typing...`;
	}

	onMount(() => {
		if (!browser) {
			return;
		}
		syncRoomPasswordFromHash();
		void ensureIdentityAndMembership();
		roomExpiryTickMs = Date.now();
		roomExpiryTicker = setInterval(() => {
			roomExpiryTickMs = Date.now();
			if (identityReady && roomId) {
				void refreshRoomMetaFromServer(roomId);
			}
		}, 60000);
		return () => {
			if (roomExpiryTicker) {
				clearInterval(roomExpiryTicker);
				roomExpiryTicker = null;
			}
		};
	});

	onDestroy(() => {
		typingController.destroy();
		if (noticeTimer) {
			clearTimeout(noticeTimer);
		}
	});
</script>

<svelte:head>
	<title>{currentRoomName} · Temp Chat</title>
	<meta
		name="description"
		content="Lightweight ephemeral Tora chat with AI and presence, without boards."
	/>
</svelte:head>

<div class="temp-chat-shell" class:theme-dark={$isDarkMode}>
	<div class="temp-chat-bg" aria-hidden="true"></div>

	<section class="temp-chat-panel">
		<header class="temp-chat-header">
			<div class="temp-chat-title">
				<div class="status-dot"></div>
				<div>
					<p class="kicker">Temp room</p>
					<h1>{currentRoomName}</h1>
					<p class="meta-line">
						{currentOnlineMembers.length} online
						{#if activeUnreadCount > 0}
							· {activeUnreadCount} unread
						{/if}
						· expires in {activeRoomRemainingLabel}
					</p>
				</div>
			</div>

			<div class="temp-chat-actions">
				<button type="button" class="header-btn" on:click={toggleRoomSearch}>
					<span>{showRoomSearch ? 'Close search' : 'Search'}</span>
				</button>
				<button
					type="button"
					class="header-btn"
					on:click={openPrivateAiChat}
					disabled={!activeRoomAllowsAI}
				>
					<span>AI</span>
				</button>
				<button type="button" class="header-btn" on:click={() => void renameRoom()}>
					<span>Rename</span>
				</button>
				<button
					type="button"
					class="header-btn primary"
					on:click={() => (showDetailsDrawer = true)}
				>
					<span>Room</span>
				</button>
			</div>
		</header>

		{#if noticeText}
			<div class={`temp-chat-notice ${noticeTone}`}>{noticeText}</div>
		{/if}

		{#if loadingError}
			<div class="temp-chat-state error">
				<h2>Unable to open this temp room</h2>
				<p>{loadingError}</p>
				<div class="state-actions">
					<a href="/temp/login">Back to temp login</a>
					<a href="/">Open main flow</a>
				</div>
			</div>
		{:else}
			<ChatStatusBars
				{typingIndicatorText}
				showTrustedDevicePrompt={false}
				isSelectionMode={false}
				messageActionMode="none"
				{showRoomSearch}
				bind:roomMessageSearch
				isDarkMode={$isDarkMode}
				selectedDeleteCount={0}
				on:closeRoomSearch={() => {
					showRoomSearch = false;
					roomMessageSearch = '';
				}}
			/>

			<div class="temp-chat-layout">
				<div class="temp-chat-thread">
					{#if isLoadingRoom}
						<div class="temp-chat-state">
							<h2>{loadingLabel}</h2>
							<p>Preparing socket history, presence, and message controls.</p>
						</div>
					{:else}
						<ChatWindow
							messages={currentMessages}
							{roomId}
							{currentUserId}
							currentUserName={currentUsername}
							{roomMessageSearch}
							{expandedMessages}
							{isMember}
							isDarkMode={$isDarkMode}
							isSelectionMode={false}
							messageActionMode="none"
							selectedMessageId=""
							deleteMultiEnabled={false}
							selectedDeleteMessageIds={[]}
							focusMessageId=""
							isLoadingOlder={historyLoadingByRoom[roomId] ?? false}
							hasMoreOlder={historyHasMoreByRoom[roomId] ?? true}
							unreadCount={activeUnreadCount}
							{lastReadTimestamp}
							{firstUnreadMessageId}
							chatAuthToken={$authToken || ''}
							apiBase={API_BASE}
							{hiddenContextActions}
							on:reply={onReplyRequest}
							on:toggleReaction={onMessageReactionToggle}
							on:toggleExpand={(event) => toggleMessageExpanded(event.detail.messageId)}
							on:messageContextAction={onMessageContextAction}
							on:requestOlder={() => void loadOlderMessages(roomId)}
							on:readProgress={onChatReadProgress}
						/>
					{/if}
				</div>

				<aside class="temp-chat-sidebar">
					<div class="sidebar-card">
						<div class="sidebar-card-header">
							<h2>Room</h2>
							<button type="button" class="text-btn" on:click={copyInviteLink}>Copy invite</button>
						</div>
						<div class="room-facts">
							<div>
								<span>Created</span>
								<strong
									>{activeRoomCreatedAt > 0 ? formatDateTime(activeRoomCreatedAt) : '...'}</strong
								>
							</div>
							<div>
								<span>Expires</span>
								<strong
									>{activeRoomExpiresAt > 0 ? formatDateTime(activeRoomExpiresAt) : '...'}</strong
								>
							</div>
							<div>
								<span>Code</span>
								<strong>{roomCode || 'Loading...'}</strong>
							</div>
							<div>
								<span>Security</span>
								<strong>{activeRoomRequiresPassword ? 'Password protected' : 'Open invite'}</strong>
							</div>
						</div>
					</div>

					<div class="online-card">
						<OnlinePanel
							members={currentOnlineMembers}
							isDarkMode={$isDarkMode}
							canCollapse={false}
							isCollapsed={false}
							{currentUserId}
						/>
					</div>
				</aside>
			</div>

			{#if isMember && !isRoomExpired}
				<div class="composer-shell">
					<div class="composer-typing-slot" role="status" aria-live="polite" aria-atomic="true">
						{#if typingNamesPreview}
							<div class="typing-card">
								<div class="typing-names">{typingNamesPreview}</div>
								<div class="typing-status">{typingIndicatorText}</div>
							</div>
						{/if}
					</div>

					<ChatComposer
						bind:draftMessage
						bind:attachedFile
						{activeReply}
						isDarkMode={$isDarkMode}
						{currentUsername}
						{roomId}
						aiEnabled={activeRoomAllowsAI}
						disabled={!isMember || isRoomExpired}
						isEphemeralRoom={true}
						mentionCandidates={currentOnlineMembers.map((member) => member.name)}
						on:send={(event) => void sendMessage(event.detail)}
						on:typing={onComposerTyping}
						on:attach={handleComposerAttach}
						on:removeAttachment={handleComposerRemoveAttachment}
						on:cancelReply={clearReplyTarget}
						on:openPrivateAi={openPrivateAiChat}
						on:toastError={(event) => showNotice(event.detail.message, 'error')}
					/>
				</div>
			{:else if isRoomExpired}
				<div class="expired-banner">
					This room has expired. Extend it from the room panel to keep chatting.
				</div>
			{/if}
		{/if}
	</section>

	{#if showDetailsDrawer}
		<button
			type="button"
			class="drawer-backdrop"
			aria-label="Close room details"
			on:click={() => (showDetailsDrawer = false)}
		></button>
		<aside class="details-drawer">
			<div class="drawer-header">
				<div>
					<p class="kicker">Room details</p>
					<h2>{currentRoomName}</h2>
				</div>
				<button type="button" class="icon-close" on:click={() => (showDetailsDrawer = false)}>
					x
				</button>
			</div>

			<div class="drawer-section">
				<div class="drawer-row">
					<span>Invite link</span>
					<button type="button" class="text-btn" on:click={copyInviteLink}>Copy</button>
				</div>
				<div class="drawer-row">
					<span>Room code</span>
					<div class="drawer-inline">
						<strong>{roomCode || 'Loading...'}</strong>
						<button type="button" class="text-btn" on:click={copyRoomCode} disabled={!roomCode}>
							Copy
						</button>
					</div>
				</div>
				<div class="drawer-row">
					<span>AI</span>
					<strong>{activeRoomAllowsAI ? 'Enabled' : 'Disabled'}</strong>
				</div>
				<div class="drawer-row">
					<span>Encryption</span>
					<strong>{activeRoomFeatures.e2eEnabled ? 'Enabled' : 'Disabled'}</strong>
				</div>
				<div class="drawer-row">
					<span>Admin</span>
					<strong>{activeRoomIsAdmin ? 'Yes' : 'No'}</strong>
				</div>
				{#if activeRoomIsAdmin && activeThread?.adminCode}
					<div class="drawer-row">
						<span>Admin code</span>
						<div class="drawer-inline">
							<strong>{activeThread.adminCode}</strong>
							<button
								type="button"
								class="text-btn"
								on:click={() => copyText(activeThread?.adminCode || '', 'Admin code copied.')}
							>
								Copy
							</button>
						</div>
					</div>
				{/if}
			</div>

			<div class="drawer-actions">
				<button
					type="button"
					class="primary"
					on:click={() => void extendRoom()}
					disabled={isExtendingRoom}
				>
					{isExtendingRoom ? 'Extending...' : 'Extend room'}
				</button>
				<button
					type="button"
					class="secondary"
					on:click={() => void leaveRoom()}
					disabled={isLeavingRoom}
				>
					{isLeavingRoom ? 'Leaving...' : 'Leave room'}
				</button>
				{#if activeRoomIsAdmin}
					<button
						type="button"
						class="danger"
						on:click={() => void deleteRoom()}
						disabled={isDeletingRoom}
					>
						{isDeletingRoom ? 'Deleting...' : 'Delete room'}
					</button>
				{/if}
			</div>
		</aside>
	{/if}

	<PrivateAiChat
		open={showPrivateAiChat}
		isDarkMode={$isDarkMode}
		{currentUserId}
		{currentUsername}
		{roomId}
		on:close={closePrivateAiChat}
	/>
</div>

<style>
	.temp-chat-shell {
		position: relative;
		min-height: 100vh;
		padding: 1rem;
		background:
			radial-gradient(circle at top right, rgba(255, 185, 120, 0.18), transparent 24%),
			radial-gradient(circle at bottom left, rgba(92, 161, 255, 0.16), transparent 28%),
			linear-gradient(180deg, #f5f7fb 0%, #eef2f8 100%);
		color: #172033;
	}

	.temp-chat-shell.theme-dark {
		background:
			radial-gradient(circle at top right, rgba(255, 185, 120, 0.14), transparent 24%),
			radial-gradient(circle at bottom left, rgba(92, 161, 255, 0.12), transparent 28%),
			linear-gradient(180deg, #090d16 0%, #0f1624 100%);
		color: #edf2ff;
	}

	.temp-chat-bg {
		position: absolute;
		inset: 0;
		background-image:
			linear-gradient(rgba(255, 255, 255, 0.04) 1px, transparent 1px),
			linear-gradient(90deg, rgba(255, 255, 255, 0.04) 1px, transparent 1px);
		background-size: 32px 32px;
		mask-image: linear-gradient(180deg, rgba(0, 0, 0, 0.68), transparent);
		pointer-events: none;
	}

	.temp-chat-panel {
		position: relative;
		z-index: 1;
		display: flex;
		flex-direction: column;
		min-height: calc(100vh - 2rem);
		border-radius: 30px;
		border: 1px solid rgba(26, 36, 58, 0.08);
		background: rgba(255, 255, 255, 0.78);
		backdrop-filter: blur(18px);
		box-shadow: 0 34px 84px rgba(32, 43, 67, 0.12);
		overflow: hidden;
	}

	.theme-dark .temp-chat-panel {
		border-color: rgba(255, 255, 255, 0.08);
		background: rgba(7, 11, 19, 0.82);
		box-shadow: 0 34px 84px rgba(0, 0, 0, 0.34);
	}

	.temp-chat-header {
		display: flex;
		justify-content: space-between;
		gap: 1rem;
		padding: 1rem 1.1rem;
		border-bottom: 1px solid rgba(24, 36, 58, 0.08);
		align-items: center;
	}

	.theme-dark .temp-chat-header {
		border-bottom-color: rgba(255, 255, 255, 0.08);
	}

	.temp-chat-title {
		display: flex;
		gap: 0.85rem;
		align-items: center;
		min-width: 0;
	}

	.status-dot {
		width: 0.8rem;
		height: 0.8rem;
		border-radius: 999px;
		background: linear-gradient(135deg, #52d896 0%, #27ae60 100%);
		box-shadow: 0 0 0 0.4rem rgba(39, 174, 96, 0.12);
		flex-shrink: 0;
	}

	.kicker {
		margin: 0 0 0.18rem;
		font-size: 0.72rem;
		text-transform: uppercase;
		letter-spacing: 0.16em;
		color: #8d6641;
	}

	.theme-dark .kicker {
		color: #ffca92;
	}

	h1,
	h2 {
		margin: 0;
	}

	.temp-chat-title h1 {
		font-size: clamp(1.15rem, 2vw, 1.55rem);
		line-height: 1.08;
	}

	.meta-line {
		margin: 0.22rem 0 0;
		font-size: 0.88rem;
		color: #5d6b81;
	}

	.theme-dark .meta-line {
		color: #aebad2;
	}

	.temp-chat-actions {
		display: flex;
		flex-wrap: wrap;
		gap: 0.55rem;
		justify-content: flex-end;
	}

	.header-btn,
	.text-btn,
	.drawer-actions button {
		font: inherit;
	}

	.header-btn {
		display: inline-flex;
		align-items: center;
		gap: 0.45rem;
		min-height: 40px;
		padding: 0.68rem 0.9rem;
		border-radius: 999px;
		border: 1px solid rgba(23, 32, 51, 0.12);
		background: rgba(255, 255, 255, 0.68);
		color: inherit;
		cursor: pointer;
	}

	.theme-dark .header-btn {
		border-color: rgba(255, 255, 255, 0.1);
		background: rgba(255, 255, 255, 0.03);
	}

	.header-btn.primary {
		background: linear-gradient(135deg, #ffb35d 0%, #ff8c68 100%);
		border-color: transparent;
		color: #1f130d;
	}

	.header-btn:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.temp-chat-notice {
		margin: 0.8rem 1rem 0;
		padding: 0.8rem 0.95rem;
		border-radius: 16px;
		font-size: 0.92rem;
	}

	.temp-chat-notice.info {
		background: rgba(84, 122, 255, 0.1);
		color: #3658c7;
	}

	.temp-chat-notice.success {
		background: rgba(39, 174, 96, 0.1);
		color: #1d8a50;
	}

	.temp-chat-notice.error {
		background: rgba(220, 78, 78, 0.12);
		color: #b53838;
	}

	.theme-dark .temp-chat-notice.info {
		background: rgba(84, 122, 255, 0.16);
		color: #c7d4ff;
	}

	.theme-dark .temp-chat-notice.success {
		background: rgba(39, 174, 96, 0.14);
		color: #bce8cf;
	}

	.theme-dark .temp-chat-notice.error {
		background: rgba(220, 78, 78, 0.16);
		color: #ffd0d0;
	}

	.temp-chat-layout {
		flex: 1;
		min-height: 0;
		display: grid;
		grid-template-columns: minmax(0, 1fr) 320px;
	}

	.temp-chat-thread {
		min-height: 0;
		display: flex;
		flex-direction: column;
	}

	.temp-chat-sidebar {
		border-left: 1px solid rgba(24, 36, 58, 0.08);
		padding: 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.85rem;
	}

	.theme-dark .temp-chat-sidebar {
		border-left-color: rgba(255, 255, 255, 0.08);
	}

	.sidebar-card,
	.online-card {
		border-radius: 22px;
		border: 1px solid rgba(24, 36, 58, 0.08);
		background: rgba(255, 255, 255, 0.76);
		overflow: hidden;
	}

	.theme-dark .sidebar-card,
	.theme-dark .online-card {
		border-color: rgba(255, 255, 255, 0.08);
		background: rgba(255, 255, 255, 0.03);
	}

	.sidebar-card {
		padding: 1rem;
	}

	.sidebar-card-header,
	.drawer-header {
		display: flex;
		justify-content: space-between;
		gap: 0.8rem;
		align-items: center;
	}

	.sidebar-card-header h2,
	.drawer-header h2 {
		font-size: 1rem;
	}

	.text-btn {
		border: none;
		background: transparent;
		padding: 0;
		color: #4d73df;
		font-weight: 700;
		cursor: pointer;
	}

	.theme-dark .text-btn {
		color: #a9c1ff;
	}

	.room-facts,
	.drawer-section {
		display: flex;
		flex-direction: column;
		gap: 0.8rem;
		margin-top: 0.95rem;
	}

	.room-facts div,
	.drawer-row {
		display: flex;
		justify-content: space-between;
		gap: 1rem;
		align-items: center;
	}

	.room-facts span,
	.drawer-row span {
		font-size: 0.84rem;
		color: #66758c;
	}

	.theme-dark .room-facts span,
	.theme-dark .drawer-row span {
		color: #acb9d2;
	}

	.room-facts strong,
	.drawer-row strong {
		font-size: 0.92rem;
		text-align: right;
	}

	.composer-shell {
		padding: 0 1rem 1rem;
	}

	.composer-typing-slot {
		min-height: 2.5rem;
		display: flex;
		align-items: flex-end;
	}

	.typing-card {
		margin: 0.65rem 0 0.2rem;
		padding: 0.75rem 0.9rem;
		border-radius: 16px;
		background: rgba(84, 122, 255, 0.09);
	}

	.theme-dark .typing-card {
		background: rgba(84, 122, 255, 0.14);
	}

	.typing-names {
		font-size: 0.8rem;
		font-weight: 700;
	}

	.typing-status {
		font-size: 0.76rem;
		margin-top: 0.12rem;
		color: #65748a;
	}

	.theme-dark .typing-status {
		color: #a8b6ce;
	}

	.temp-chat-state {
		margin: auto;
		padding: 1.4rem;
		text-align: center;
	}

	.temp-chat-state h2 {
		margin-bottom: 0.35rem;
	}

	.temp-chat-state p {
		margin: 0;
		color: #67758a;
	}

	.theme-dark .temp-chat-state p {
		color: #afbad1;
	}

	.temp-chat-state.error {
		max-width: 480px;
	}

	.state-actions {
		display: flex;
		gap: 0.75rem;
		justify-content: center;
		flex-wrap: wrap;
		margin-top: 1rem;
	}

	.state-actions a {
		padding: 0.8rem 1rem;
		border-radius: 999px;
		text-decoration: none;
		font-weight: 700;
		border: 1px solid rgba(24, 36, 58, 0.12);
		color: inherit;
	}

	.expired-banner {
		margin: 0 1rem 1rem;
		padding: 0.9rem 1rem;
		border-radius: 16px;
		background: rgba(220, 78, 78, 0.12);
		color: #a63939;
		font-weight: 700;
	}

	.drawer-backdrop {
		position: fixed;
		inset: 0;
		background: rgba(6, 9, 16, 0.5);
		z-index: 20;
	}

	.details-drawer {
		position: fixed;
		top: 0;
		right: 0;
		bottom: 0;
		width: min(420px, 100%);
		padding: 1.15rem;
		background: rgba(250, 252, 255, 0.96);
		backdrop-filter: blur(18px);
		border-left: 1px solid rgba(24, 36, 58, 0.08);
		z-index: 21;
		display: flex;
		flex-direction: column;
	}

	.theme-dark .details-drawer {
		background: rgba(9, 13, 22, 0.98);
		border-left-color: rgba(255, 255, 255, 0.08);
	}

	.icon-close {
		width: 40px;
		height: 40px;
		border-radius: 999px;
		border: 1px solid rgba(24, 36, 58, 0.12);
		background: transparent;
		cursor: pointer;
	}

	.theme-dark .icon-close {
		border-color: rgba(255, 255, 255, 0.12);
		color: #edf2ff;
	}

	.drawer-inline {
		display: inline-flex;
		align-items: center;
		gap: 0.6rem;
	}

	.drawer-actions {
		display: flex;
		flex-direction: column;
		gap: 0.7rem;
		margin-top: auto;
	}

	.drawer-actions button {
		min-height: 48px;
		border-radius: 16px;
		border: 1px solid transparent;
		font-weight: 800;
		cursor: pointer;
	}

	.drawer-actions .primary {
		background: linear-gradient(135deg, #ffb35d 0%, #ff8c68 100%);
		color: #1f130d;
	}

	.drawer-actions .secondary {
		background: rgba(84, 122, 255, 0.08);
		color: inherit;
		border-color: rgba(84, 122, 255, 0.18);
	}

	.drawer-actions .danger {
		background: rgba(220, 78, 78, 0.12);
		color: #b53838;
		border-color: rgba(220, 78, 78, 0.18);
	}

	@media (max-width: 1024px) {
		.temp-chat-layout {
			grid-template-columns: 1fr;
		}

		.temp-chat-sidebar {
			border-left: none;
			border-top: 1px solid rgba(24, 36, 58, 0.08);
		}
	}

	@media (max-width: 780px) {
		.temp-chat-shell {
			padding: 0.65rem;
		}

		.temp-chat-panel {
			min-height: calc(100vh - 1.3rem);
			border-radius: 24px;
		}

		.temp-chat-header {
			flex-direction: column;
			align-items: stretch;
		}

		.temp-chat-actions {
			justify-content: stretch;
		}

		.header-btn {
			flex: 1 1 calc(50% - 0.55rem);
			justify-content: center;
		}

		.temp-chat-sidebar {
			padding: 0.85rem;
		}

		.composer-shell {
			padding: 0 0.75rem 0.75rem;
		}
	}
</style>
