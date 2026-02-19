<script lang="ts">
	import { browser } from '$app/environment';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import ChatComposer from '$lib/components/chat/ChatComposer.svelte';
	import ChatSidebar from '$lib/components/chat/ChatSidebar.svelte';
	import ChatWindow from '$lib/components/chat/ChatWindow.svelte';
	import OnlinePanel from '$lib/components/chat/OnlinePanel.svelte';
	import { currentUser } from '$lib/store';
	import { getOrInitIdentity } from '$lib/utils/identity';
	import { globalMessages, initGlobalSocket, sendSocketPayload, subscribeToRooms } from '$lib/ws';
	import { onDestroy, onMount } from 'svelte';

	type ThreadStatus = 'joined' | 'discoverable';

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
	};

	type OnlineMember = {
		id: string;
		name: string;
		isOnline: boolean;
		joinedAt: number;
	};

	type RoomMeta = {
		createdAt: number;
	};

	type SidebarRoom = {
		roomId: string;
		roomName: string;
		status: ThreadStatus;
		parentRoomId?: string;
		originMessageId?: string;
		memberCount?: number;
		createdAt?: number;
	};

	const CLIENT_LOG_PREFIX = '[chat-client]';
	const API_BASE = (import.meta.env.VITE_API_BASE as string | undefined) ?? 'http://localhost:8080';
	const CLIENT_DEBUG = (import.meta.env.VITE_CHAT_DEBUG as string | undefined) === '1';
	const ROOM_MAX_LIFESPAN_MS = 15 * 24 * 60 * 60 * 1000;

	let sidebarRefreshTimer: ReturnType<typeof setInterval> | null = null;
	let roomMembershipSynced: Record<string, boolean> = {};
	let roomMembershipSyncing: Record<string, boolean> = {};
	let unsubscribeGlobalMessages: (() => void) | null = null;

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
	let isMobileView = false;
	let mobilePane: 'list' | 'chat' = 'chat';
	let focusMessageId = '';
	let focusConsumedForRoom = false;
	let focusRoomTracker = '';

	let roomThreads: ChatThread[] = [];
	let messagesByRoom: Record<string, ChatMessage[]> = {};
	let onlineByRoom: Record<string, OnlineMember[]> = {};
	let roomMetaById: Record<string, RoomMeta> = {};
	let isExtendingRoom = false;
	let expandedMessages: Record<string, boolean> = {};
	let identityReady = !browser;
	let headerActionsEl: HTMLDivElement | null = null;

	$: roomId = toRoomSlug(decodeURIComponent($page.params.roomId ?? ''));
	$: roomNameFromURL = toRoomSlug(
		decodeURIComponent($page.url.searchParams.get('name') ?? '').trim()
	);
	$: roomCreatedAtFromURL = parseTimestampParam($page.url.searchParams.get('createdAt'));
	$: focusMessageIdFromURL = normalizeMessageID($page.url.searchParams.get('focusMsg') ?? '');
	$: roomMemberHint = $page.url.searchParams.get('member');
	$: currentUserId = $currentUser?.id ?? 'guest';
	$: currentUsername = normalizeUsernameValue($currentUser?.username ?? 'Guest') || 'Guest';
	$: activeThread =
		roomThreads.find((thread) => thread.id === roomId) ??
		createThread(roomId || 'default_room', roomNameFromURL || undefined, 'joined');
	$: currentMessages = messagesByRoom[roomId] ?? [];
	$: currentOnlineMembers = onlineByRoom[roomId] ?? [];
	$: activeUnreadCount = activeThread?.unread ?? 0;
	$: isMember = resolveRoomMembership(roomId, roomThreads, roomMemberHint);
	$: myRooms = filterThreadsByStatus(roomThreads, 'joined');
	$: discoverableRooms = filterThreadsByStatus(roomThreads, 'discoverable');
	$: filteredMyRooms = filterThreadList(myRooms, chatListSearch, messagesByRoom, roomId);
	$: filteredDiscoverableRooms = filterThreadList(
		discoverableRooms,
		chatListSearch,
		messagesByRoom,
		roomId
	);

	$: if (roomId) {
		ensureRoomThread(roomId, roomNameFromURL || undefined, isMember ? 'joined' : 'discoverable');
		ensureOnlineSeed(roomId);
		ensureRoomMeta(roomId, roomCreatedAtFromURL);
	}
	$: if (browser && identityReady && roomId && isMember) {
		void syncRoomMembership(roomId);
	}
	$: if (browser && identityReady) {
		initGlobalSocket(currentUserId, currentUsername);
	}
	$: if (browser && identityReady) {
		const joinedRoomIDs = myRooms.map((thread) => thread.id);
		if (roomId && isMember && !joinedRoomIDs.includes(roomId)) {
			joinedRoomIDs.push(roomId);
		}
		subscribeToRooms(joinedRoomIDs);
	}
	$: if (browser && roomId && roomId !== lastToastRoom) {
		showJoinToast(roomId);
	}
	$: if (roomId && focusRoomTracker !== roomId) {
		focusRoomTracker = roomId;
		focusConsumedForRoom = false;
		focusMessageId = '';
	}
	$: if (!focusConsumedForRoom && focusMessageIdFromURL) {
		focusMessageId = focusMessageIdFromURL;
		focusConsumedForRoom = true;
	}

	onDestroy(() => {
		clientLog('component-destroy', { roomId });
		if (unsubscribeGlobalMessages) {
			unsubscribeGlobalMessages();
			unsubscribeGlobalMessages = null;
		}
		clearSidebarRefreshTimer();
		clearToastTimer();
	});

	onMount(() => {
		void initializeIdentity();
		if (!browser) {
			return;
		}
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
		return () => {
			if (unsubscribeGlobalMessages) {
				unsubscribeGlobalMessages();
				unsubscribeGlobalMessages = null;
			}
			window.removeEventListener('pointerdown', onDocumentPointerDown);
			window.removeEventListener('resize', updateViewportMode);
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

	function clearToastTimer() {
		if (toastTimer) {
			clearTimeout(toastTimer);
			toastTimer = null;
		}
	}

	function showJoinToast(activeRoomId: string) {
		lastToastRoom = activeRoomId;
		toastMessage = `Joined Room: ${activeRoomId}`;
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
			status
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
			const nextStatus: ThreadStatus = existing.status === 'joined' ? 'joined' : status;
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

	function ensureRoomMeta(targetRoomId: string, createdAt: number) {
		if (!targetRoomId || !Number.isFinite(createdAt) || createdAt <= 0) {
			return;
		}
		const existing = roomMetaById[targetRoomId];
		if (existing?.createdAt === createdAt) {
			return;
		}
		roomMetaById = {
			...roomMetaById,
			[targetRoomId]: { createdAt }
		};
	}

	function ensureOnlineSeed(targetRoomId: string) {
		if (onlineByRoom[targetRoomId]?.length) {
			return;
		}
		onlineByRoom = {
			...onlineByRoom,
			[targetRoomId]: dedupeMembers([
				{ id: currentUserId, name: currentUsername, isOnline: true, joinedAt: Date.now() }
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
		const normalizedRoomId = toRoomSlug(targetRoomId);
		if (!normalizedRoomId) {
			return;
		}
		roomMembershipSynced = {
			...roomMembershipSynced,
			[normalizedRoomId]: true
		};
	}

	async function syncRoomMembership(targetRoomId: string) {
		const normalizedRoomId = toRoomSlug(targetRoomId);
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
				roomName: normalizedRoomId,
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

			markRoomMembershipSynced(normalizedRoomId);
			const joinedName = toStringValue(data.roomName) || formatRoomName(normalizedRoomId);
			const joinedCreatedAt = toTimestamp(data.createdAt);
			ensureRoomThread(normalizedRoomId, joinedName, 'joined');
			ensureRoomMeta(normalizedRoomId, joinedCreatedAt);
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
			const incoming = Array.isArray(data.rooms) ? (data.rooms as SidebarRoom[]) : [];
			const existing = new Map(roomThreads.map((thread) => [thread.id, thread]));
			const nextThreads = incoming.reduce<ChatThread[]>((acc, room) => {
				const roomID = toRoomSlug(room.roomId);
				if (!roomID) {
					return acc;
				}

				const prev = existing.get(roomID);
				const createdAt = normalizeEpoch(Number(room.createdAt ?? 0));
				if (createdAt > 0) {
					ensureRoomMeta(roomID, createdAt);
				}

				const next: ChatThread = {
					id: roomID,
					name: toStringValue(room.roomName) || prev?.name || formatRoomName(roomID),
					lastMessage: prev?.lastMessage || '',
					lastActivity: prev?.lastActivity || createdAt || Date.now(),
					unread: prev?.unread || 0,
					status: room.status === 'discoverable' ? 'discoverable' : 'joined',
					memberCount: typeof room.memberCount === 'number' ? room.memberCount : prev?.memberCount,
					parentRoomId: toStringValue(room.parentRoomId) || prev?.parentRoomId || undefined,
					originMessageId: toStringValue(room.originMessageId) || prev?.originMessageId || undefined
				};

				acc.push(next);
				return acc;
			}, []);

			if (roomId && !nextThreads.some((thread) => thread.id === roomId)) {
				nextThreads.push(
					createThread(
						roomId,
						roomNameFromURL || formatRoomName(roomId),
						roomMemberHint === '0' ? 'discoverable' : 'joined'
					)
				);
			}

			const merged = new Map<string, ChatThread>();
			for (const existingThread of roomThreads) {
				merged.set(existingThread.id, existingThread);
			}
			for (const nextThread of nextThreads) {
				const prev = merged.get(nextThread.id);
				merged.set(nextThread.id, {
					...prev,
					...nextThread,
					unread: prev?.unread ?? nextThread.unread,
					lastMessage: nextThread.lastMessage || prev?.lastMessage || '',
					lastActivity: Math.max(nextThread.lastActivity, prev?.lastActivity ?? 0),
					status:
						prev?.status === 'joined' || nextThread.status === 'joined' ? 'joined' : 'discoverable'
				});
			}

			roomThreads = sortThreads([...merged.values()]);
		} catch (error) {
			clientLog('api-sidebar-error', {
				error: error instanceof Error ? error.message : String(error)
			});
		}
	}

	function selectRoom(targetRoomId: string, memberState: boolean, focusMsgID = '') {
		const normalizedTargetRoomId = toRoomSlug(targetRoomId);
		if (!normalizedTargetRoomId) {
			return;
		}
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
		isSelectionMode = false;
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
		showRoomMenu = false;
		showRoomSearch = false;
		showRoomDetails = false;
		isSelectionMode = false;
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
		const parentRoomID = toRoomSlug(event.detail.parentRoomId);
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
		const directRoomID = toRoomSlug(toStringValue(envelope.roomId ?? envelope.room_id));
		if (directRoomID) {
			return directRoomID;
		}
		if (envelope.payload && typeof envelope.payload === 'object') {
			const payload = envelope.payload as Record<string, unknown>;
			return toRoomSlug(toStringValue(payload.roomId ?? payload.room_id));
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
		}
	}

	function parseIncomingMessage(value: unknown, fallbackRoomId: string): ChatMessage | null {
		if (!value || typeof value !== 'object') {
			return null;
		}

		const source = value as Record<string, unknown>;
		const nextRoomId = toRoomSlug(toStringValue(source.roomId ?? source.room_id ?? fallbackRoomId));
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
			createdAt: toTimestamp(
				source.time ?? source.createdAt ?? source.created_at ?? source.timestamp
			),
			hasBreakRoom:
				toBool(source.hasBreakRoom ?? source.has_break_room) ||
				toStringValue(source.breakRoomId ?? source.break_room_id) !== '',
			breakRoomId: toStringValue(source.breakRoomId ?? source.break_room_id),
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
				createdAt: Date.now(),
				pending: true
			};
		}

		upsertMessage(roomId, outgoing, false);
		sendSocketPayload(toWireMessage(outgoing));
		markRoomAsRead(roomId);
		draftMessage = '';
		attachedFile = null;
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
			createdAt: new Date(message.createdAt).toISOString()
		};
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
		const normalizedRoomID = toRoomSlug(targetRoomId);
		if (!normalizedRoomID) {
			return;
		}
		showLeftMenu = false;
		showRoomMenu = false;

		const existing = roomThreads.find((thread) => thread.id === normalizedRoomID);
		const currentName = existing?.name || formatRoomName(normalizedRoomID);
		const requested = window.prompt('Rename room', currentName);
		if (requested === null) {
			return;
		}

		const normalizedName = toRoomSlug(requested);
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

			const savedName = toStringValue(data.roomName) || normalizedName;
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
		const input = window.prompt('Enter a room name');
		showLeftMenu = false;
		if (!input) {
			return;
		}

		const requestedName = toRoomSlug(input);
		if (!requestedName) {
			return;
		}

		try {
			const res = await fetch(`${API_BASE}/api/rooms/join`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					roomName: requestedName,
					username: currentUsername,
					userId: normalizeIdentifier(currentUserId),
					type: 'ephemeral',
					mode: 'create'
				})
			});
			const data = await res.json();
			if (!res.ok) {
				throw new Error(data.error || 'Failed to create room');
			}

			const nextRoomId = toStringValue(data.roomId) || requestedName;
			const nextRoomName = toStringValue(data.roomName) || formatRoomName(nextRoomId);
			const nextCreatedAt = toTimestamp(data.createdAt);

			ensureRoomThread(nextRoomId, nextRoomName, 'joined');
			markRoomMembershipSynced(nextRoomId);
			ensureRoomMeta(nextRoomId, nextCreatedAt);
			await refreshSidebarRooms();

			const params = new URLSearchParams({
				name: nextRoomName,
				member: '1'
			});
			if (nextCreatedAt > 0) {
				params.set('createdAt', String(nextCreatedAt));
			}
			await goto(`/chat/${encodeURIComponent(nextRoomId)}?${params.toString()}`);
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to create room');
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
					roomName: roomId,
					username: currentUsername,
					userId: normalizeIdentifier(currentUserId),
					mode: 'join'
				})
			});
			const data = await res.json();
			if (!res.ok) {
				throw new Error(data.error || 'Unable to join room');
			}

			const joinedName =
				toStringValue(data.roomName) || activeThread.name || formatRoomName(roomId);
			const joinedCreatedAt = toTimestamp(data.createdAt);
			ensureRoomThread(roomId, joinedName, 'joined');
			markRoomMembershipSynced(roomId);
			ensureRoomMeta(roomId, joinedCreatedAt);
			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === roomId ? { ...thread, status: 'joined', name: joinedName } : thread
				)
			);
			await refreshSidebarRooms();

			const params = new URLSearchParams({ name: joinedName, member: '1' });
			if (joinedCreatedAt > 0) {
				params.set('createdAt', String(joinedCreatedAt));
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

	function toggleSelectionMode() {
		isSelectionMode = !isSelectionMode;
		showRoomMenu = false;
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
		showRoomMenu = false;
	}

	function toggleMessageExpanded(messageId: string) {
		expandedMessages = {
			...expandedMessages,
			[messageId]: !expandedMessages[messageId]
		};
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
		const created = await createBreakRoom(message);
		if (created) {
			isSelectionMode = false;
		}
	}

	function buildBreakPrefixSuggestion(message: ChatMessage) {
		const textBased = toRoomSlug(message.content);
		if (textBased) {
			return textBased.slice(0, 24);
		}
		const fileBased = toRoomSlug(message.fileName || '');
		if (fileBased) {
			return fileBased.slice(0, 24);
		}
		const senderBased = toRoomSlug(message.senderName);
		if (senderBased) {
			return `${senderBased}_topic`.slice(0, 24);
		}
		return 'topic';
	}

	async function createBreakRoom(message: ChatMessage) {
		const suggestedPrefix = buildBreakPrefixSuggestion(message);
		const requested = window.prompt(
			'Child room prefix (final format: prefix_parent5_n)',
			suggestedPrefix
		);
		if (requested === null) {
			return false;
		}
		const normalizedPrefix = toRoomSlug(requested) || suggestedPrefix;

		try {
			const res = await fetch(`${API_BASE}/api/rooms/break`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					parentRoomId: roomId,
					originMessageId: message.id,
					roomName: normalizedPrefix,
					userId: normalizeIdentifier(currentUserId),
					username: currentUsername
				})
			});
			const data = await res.json();
			if (!res.ok) {
				throw new Error(data.error || 'Failed to create break room');
			}

			const breakRoomId = toRoomSlug(toStringValue(data.roomId));
			if (!breakRoomId) {
				throw new Error('Invalid break room id');
			}
			const breakRoomName = toStringValue(data.roomName) || formatRoomName(breakRoomId);
			const breakCreatedAt = toTimestamp(data.createdAt);

			messagesByRoom = {
				...messagesByRoom,
				[roomId]: (messagesByRoom[roomId] ?? []).map((entry) =>
					entry.id === message.id
						? {
								...entry,
								hasBreakRoom: true,
								breakRoomId,
								breakJoinCount: Math.max(1, entry.breakJoinCount ?? 0)
							}
						: entry
				)
			};
			ensureRoomThread(breakRoomId, breakRoomName, 'joined');
			markRoomMembershipSynced(breakRoomId);
			ensureRoomMeta(breakRoomId, breakCreatedAt);
			await refreshSidebarRooms();
			await goto(
				`/chat/${encodeURIComponent(breakRoomId)}?name=${encodeURIComponent(breakRoomName)}&member=1`
			);
			return true;
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to create break room');
			return false;
		}
	}

	function onJoinBreakRoom(event: CustomEvent<{ roomId: string }>) {
		const target = toRoomSlug(event.detail.roomId);
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

	function getRoomCreatedAt(targetRoomId: string) {
		return roomMetaById[targetRoomId]?.createdAt ?? 0;
	}

	function getRoomExpiry(targetRoomId: string) {
		const createdAt = getRoomCreatedAt(targetRoomId);
		if (!createdAt) {
			return 0;
		}
		return createdAt + ROOM_MAX_LIFESPAN_MS;
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

	function toRoomSlug(value: string) {
		const normalized = value.toLowerCase().trim();
		if (!normalized) {
			return '';
		}
		return normalized
			.replace(/[^a-z0-9\s_-]/g, '')
			.replace(/[\s-]+/g, '_')
			.replace(/_+/g, '_')
			.replace(/^_+|_+$/g, '');
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
		return `${targetRoomId}_${Date.now()}_${Math.floor(Math.random() * 1000000)}`;
	}

	function formatRoomName(targetRoomId: string) {
		return targetRoomId
			.split(/[_-]/)
			.filter(Boolean)
			.map((part) => part.charAt(0).toUpperCase() + part.slice(1))
			.join(' ');
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
			return true;
		}
		return thread.status === 'joined';
	}
</script>

{#if showToast}
	<div class="toast" role="status" aria-live="polite">{toastMessage}</div>
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
			accessibleParentRoomIds={roomThreads.map((thread) => thread.id)}
			activeRoomId={roomId}
			{showLeftMenu}
			bind:chatListSearch
			on:select={(event) => selectRoom(event.detail.id, event.detail.isMember)}
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
						<button type="button" on:click|stopPropagation={toggleSelectionMode}>
							{isSelectionMode ? 'Cancel Break Mode' : 'Start Break / New Topic'}
						</button>
						<button type="button" on:click|stopPropagation={() => markRoomAsRead(roomId)}>
							Mark read
						</button>
						<button type="button" on:click|stopPropagation={clearCurrentRoomMessages}>
							Clear local
						</button>
					</div>
				{/if}
			</div>
		</header>

		{#if isSelectionMode}
			<div class="selection-banner">
				Break mode active: click a message to start a new topic room.
			</div>
		{/if}

		{#if showRoomSearch}
			<div class="chat-search-row">
				<input type="text" bind:value={roomMessageSearch} placeholder="Search in this room" />
			</div>
		{/if}

		<ChatWindow
			messages={currentMessages}
			{currentUserId}
			{roomMessageSearch}
			{expandedMessages}
			{isMember}
			{isSelectionMode}
			{focusMessageId}
			on:toggleExpand={(event) => toggleMessageExpanded(event.detail.messageId)}
			on:joinBreakRoom={onJoinBreakRoom}
			on:joinRoom={() => void joinCurrentRoom()}
			on:messageSelect={onMessageSelected}
			on:focusHandled={onFocusHandled}
		/>

		{#if isMember}
			<ChatComposer
				bind:draftMessage
				bind:attachedFile
				on:send={(event) => void sendMessage(event.detail)}
				on:attach={handleComposerAttach}
				on:removeAttachment={handleComposerRemoveAttachment}
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
					</div>
				{/each}
			{/if}
		</div>
	</section>
{/if}

<style>
	.chat-shell {
		height: calc(100vh - 72px);
		min-height: 620px;
		display: grid;
		grid-template-columns: 330px minmax(0, 1fr) 280px;
		border-top: 1px solid #dcdce1;
		background: #ececef;
		overflow: hidden;
	}

	.sidebar-pane,
	.chat-window,
	.online-pane {
		min-height: 0;
	}

	.chat-window {
		display: flex;
		flex-direction: column;
		min-width: 0;
		overflow: hidden;
		background: #f5f5f6;
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
		}

		.chat-header {
			padding: 0.68rem 0.75rem;
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
