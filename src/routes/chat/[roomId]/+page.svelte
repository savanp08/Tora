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
	import { onDestroy, onMount } from 'svelte';

	type ConnectionState = 'idle' | 'connecting' | 'open' | 'closed' | 'error';
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
		memberCount?: number;
		createdAt?: number;
	};

	const CLIENT_LOG_PREFIX = '[chat-client]';
	const API_BASE = (import.meta.env.VITE_API_BASE as string | undefined) ?? 'http://localhost:8080';
	const WS_BASE = (import.meta.env.VITE_WS_BASE as string | undefined) ?? 'ws://localhost:8080';
	const CLIENT_DEBUG = (import.meta.env.VITE_CHAT_DEBUG as string | undefined) === '1';
	const ROOM_MAX_LIFESPAN_MS = 15 * 24 * 60 * 60 * 1000;

	let ws: WebSocket | null = null;
	let wsRoomId = '';
	let wsState: ConnectionState = 'idle';
	let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
	let sidebarRefreshTimer: ReturnType<typeof setInterval> | null = null;
	let roomMembershipSynced: Record<string, boolean> = {};
	let roomMembershipSyncing: Record<string, boolean> = {};
	let reconnectAttempts = 0;

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

	let roomThreads: ChatThread[] = [];
	let messagesByRoom: Record<string, ChatMessage[]> = {};
	let onlineByRoom: Record<string, OnlineMember[]> = {};
	let roomMetaById: Record<string, RoomMeta> = {};
	let pendingOutgoingByRoom: Record<string, ChatMessage[]> = {};
	let isExtendingRoom = false;
	let expandedMessages: Record<string, boolean> = {};
	let identityReady = !browser;

	$: roomId = toRoomSlug(decodeURIComponent($page.params.roomId ?? ''));
	$: roomNameFromURL = toRoomSlug(
		decodeURIComponent($page.url.searchParams.get('name') ?? '').trim()
	);
	$: roomCreatedAtFromURL = parseTimestampParam($page.url.searchParams.get('createdAt'));
	$: roomMemberHint = $page.url.searchParams.get('member');
	$: currentUserId = $currentUser?.id ?? 'guest';
	$: currentUsername = normalizeUsernameValue($currentUser?.username ?? 'Guest') || 'Guest';
	$: activeThread =
		roomThreads.find((thread) => thread.id === roomId) ??
		createThread(roomId || 'default_room', roomNameFromURL || undefined, 'joined');
	$: currentMessages = messagesByRoom[roomId] ?? [];
	$: currentOnlineMembers = onlineByRoom[roomId] ?? [];
	$: activeUnreadCount = activeThread?.unread ?? 0;
	$: connectionLabel = getConnectionLabel(wsState);
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
	$: if (browser && identityReady && roomId && roomId !== wsRoomId) {
		connectToRoom(roomId);
	}
	$: if (browser && roomId && roomId !== lastToastRoom) {
		showJoinToast(roomId);
	}

	onDestroy(() => {
		clientLog('component-destroy', { roomId, wsRoomId, wsState });
		closeSocket();
		clearReconnectTimer();
		clearSidebarRefreshTimer();
		clearToastTimer();
	});

	onMount(() => {
		void initializeIdentity();
	});

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

	function clearReconnectTimer() {
		if (reconnectTimer) {
			clearTimeout(reconnectTimer);
			reconnectTimer = null;
		}
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
								lastMessage: lastMessage.content,
								lastActivity: lastMessage.createdAt
							}
						: thread
				)
			: [
					{
						...createThread(targetRoomId, fallbackName, 'joined'),
						lastMessage: lastMessage.content,
						lastActivity: lastMessage.createdAt
					},
					...roomThreads
				];
		roomThreads = sortThreads(merged);
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
					parentRoomId: toStringValue(room.parentRoomId) || prev?.parentRoomId || undefined
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

	function selectRoom(targetRoomId: string, memberState: boolean) {
		if (!targetRoomId || targetRoomId === roomId) {
			return;
		}
		clientLog('select-room', { fromRoom: roomId, toRoom: targetRoomId, memberState });
		showLeftMenu = false;
		showRoomMenu = false;
		showRoomSearch = false;
		showRoomDetails = false;
		isSelectionMode = false;
		roomMessageSearch = '';

		const selected = roomThreads.find((thread) => thread.id === targetRoomId);
		const params = new URLSearchParams();
		if (selected?.name) {
			params.set('name', selected.name);
		}
		if (memberState) {
			params.set('member', '1');
		} else {
			params.set('member', '0');
		}
		const createdAt = getRoomCreatedAt(targetRoomId);
		if (createdAt > 0) {
			params.set('createdAt', String(createdAt));
		}

		const query = params.toString();
		void goto(`/chat/${encodeURIComponent(targetRoomId)}${query ? `?${query}` : ''}`);
	}

	function connectToRoom(targetRoomId: string) {
		const normalizedRoomId = toRoomSlug(targetRoomId);
		if (!browser || !normalizedRoomId) {
			return;
		}

		clearReconnectTimer();
		closeSocket();
		wsState = 'connecting';
		wsRoomId = normalizedRoomId;

		try {
			const wsURL = new URL(`${WS_BASE}/ws/${encodeURIComponent(normalizedRoomId)}`);
			wsURL.searchParams.set('userId', normalizeIdentifier(currentUserId) || 'guest');
			wsURL.searchParams.set('username', currentUsername);
			const nextSocket = new WebSocket(wsURL.toString());
			ws = nextSocket;

			nextSocket.onopen = () => {
				if (ws !== nextSocket) {
					return;
				}
				wsState = 'open';
				reconnectAttempts = 0;
				markRoomAsRead(normalizedRoomId);
				flushPendingOutgoing(normalizedRoomId);
			};

			nextSocket.onmessage = (event: MessageEvent) => {
				if (ws !== nextSocket || typeof event.data !== 'string') {
					return;
				}
				handleSocketPayload(event.data, normalizedRoomId);
			};

			nextSocket.onerror = () => {
				if (ws !== nextSocket) {
					return;
				}
				wsState = 'error';
			};

			nextSocket.onclose = () => {
				if (ws !== nextSocket) {
					return;
				}
				wsState = 'closed';
				if (roomId === normalizedRoomId) {
					scheduleReconnect(normalizedRoomId);
				}
			};
		} catch (error) {
			clientLog('socket-connection-failed', {
				error: error instanceof Error ? error.message : String(error)
			});
			wsState = 'error';
			scheduleReconnect(targetRoomId);
		}
	}

	function scheduleReconnect(targetRoomId: string) {
		clearReconnectTimer();
		reconnectAttempts = Math.min(reconnectAttempts + 1, 5);
		const delay = Math.min(1000 * 2 ** (reconnectAttempts - 1), 9000);
		reconnectTimer = setTimeout(() => {
			if (roomId === targetRoomId) {
				connectToRoom(targetRoomId);
			}
		}, delay);
	}

	function closeSocket() {
		if (!ws) {
			wsRoomId = '';
			wsState = 'idle';
			return;
		}

		const activeSocket = ws;
		ws = null;
		activeSocket.onopen = null;
		activeSocket.onmessage = null;
		activeSocket.onclose = null;
		activeSocket.onerror = null;
		if (
			activeSocket.readyState === WebSocket.OPEN ||
			activeSocket.readyState === WebSocket.CONNECTING
		) {
			activeSocket.close();
		}
		wsRoomId = '';
		wsState = 'idle';
	}

	function handleSocketPayload(raw: string, targetRoomId: string) {
		let parsed: unknown;
		try {
			parsed = JSON.parse(raw);
		} catch {
			return;
		}

		if (Array.isArray(parsed)) {
			const history = parsed
				.map((entry) => parseIncomingMessage(entry, targetRoomId))
				.filter((entry): entry is ChatMessage => Boolean(entry));
			mergeMessages(targetRoomId, history);
			markRoomAsRead(targetRoomId);
			return;
		}

		if (isEnvelope(parsed)) {
			handleEnvelope(parsed, targetRoomId);
			return;
		}

		const single = parseIncomingMessage(parsed, targetRoomId);
		if (single) {
			addIncomingMessage(single);
		}
	}

	function isEnvelope(value: unknown): value is { type: string; payload: unknown } {
		return Boolean(
			value &&
			typeof value === 'object' &&
			'type' in value &&
			'payload' in value &&
			typeof (value as { type?: unknown }).type === 'string'
		);
	}

	function handleEnvelope(envelope: { type: string; payload: unknown }, targetRoomId: string) {
		const kind = envelope.type;
		if (kind === 'history' || kind === 'recent_messages' || kind === 'initial_messages') {
			if (Array.isArray(envelope.payload)) {
				const history = envelope.payload
					.map((entry) => parseIncomingMessage(entry, targetRoomId))
					.filter((entry): entry is ChatMessage => Boolean(entry));
				mergeMessages(targetRoomId, history);
				markRoomAsRead(targetRoomId);
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

		if (kind === 'online_list' && Array.isArray(envelope.payload)) {
			const members = envelope.payload
				.map((entry, index) => parseMember(entry, index))
				.filter((entry): entry is OnlineMember => Boolean(entry));
			onlineByRoom = {
				...onlineByRoom,
				[targetRoomId]: dedupeMembers(members)
			};
			return;
		}

		if (kind === 'user_joined') {
			const joined = parseMember(envelope.payload, Date.now());
			if (joined) {
				upsertOnlineMember(targetRoomId, joined);
			}
			return;
		}

		if (kind === 'user_left') {
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
		const rawMediaURL = toStringValue(source.mediaUrl ?? source.media_url ?? '');
		const normalizedMediaURL = toAbsoluteMediaURL(rawMediaURL);
		let nextContent = toStringValue(source.text ?? source.content ?? '') || normalizedMediaURL;
		if (isMediaMessageType(nextType)) {
			nextContent = toAbsoluteMediaURL(nextContent);
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
			mediaUrl: normalizedMediaURL || (isMediaMessageType(nextType) ? nextContent : ''),
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

	function queueOutgoing(message: ChatMessage) {
		const currentQueue = pendingOutgoingByRoom[message.roomId] ?? [];
		pendingOutgoingByRoom = {
			...pendingOutgoingByRoom,
			[message.roomId]: [...currentQueue, message]
		};
	}

	function flushPendingOutgoing(targetRoomId: string) {
		const roomQueue = pendingOutgoingByRoom[targetRoomId] ?? [];
		if (roomQueue.length === 0 || !ws || ws.readyState !== WebSocket.OPEN) {
			return;
		}
		for (const queued of roomQueue) {
			ws.send(JSON.stringify(toWireMessage(queued)));
		}
		pendingOutgoingByRoom = {
			...pendingOutgoingByRoom,
			[targetRoomId]: []
		};
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

		const nextType = isMediaMessage ? mediaType : 'text';
		const nextContent = isMediaMessage ? mediaContent : text;

		const nextMessage: ChatMessage = {
			id: createMessageId(roomId),
			roomId,
			senderId: currentUserId,
			senderName: currentUsername,
			content: nextContent,
			type: nextType || 'text',
			mediaUrl: isMediaMessage ? mediaContent : '',
			mediaType: isMediaMessage ? mediaType : '',
			fileName: payload?.fileName?.trim() ?? '',
			createdAt: Date.now(),
			pending: true
		};

		upsertMessage(roomId, nextMessage, false);
		markRoomAsRead(roomId);
		draftMessage = '';
		attachedFile = null;

		if (ws && ws.readyState === WebSocket.OPEN) {
			ws.send(JSON.stringify(toWireMessage(nextMessage)));
		} else {
			queueOutgoing(nextMessage);
		}
	}

	function toWireMessage(message: ChatMessage) {
		const mediaType =
			message.type === 'image' || message.type === 'video' || message.type === 'file'
				? message.type
				: '';
		const mediaURL = mediaType ? message.content : '';

		return {
			id: message.id,
			roomId: message.roomId,
			userId: message.senderId,
			username: message.senderName,
			text: message.content,
			time: new Date(message.createdAt).toISOString(),
			senderId: message.senderId,
			senderName: message.senderName,
			content: message.content,
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
		await createBreakRoom(message);
		isSelectionMode = false;
	}

	async function createBreakRoom(message: ChatMessage) {
		try {
			const res = await fetch(`${API_BASE}/api/rooms/break`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					parentRoomId: roomId,
					originMessageId: message.id,
					roomName: message.content,
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
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to create break room');
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

	function onChatHeaderKeyDown(event: KeyboardEvent) {
		if (event.key === 'Enter' || event.key === ' ') {
			event.preventDefault();
			openRoomDetails();
		}
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

	function getConnectionLabel(state: ConnectionState) {
		if (state === 'open') {
			return 'Live';
		}
		if (state === 'connecting') {
			return 'Connecting';
		}
		if (state === 'error') {
			return 'Error';
		}
		if (state === 'closed') {
			return 'Offline';
		}
		return 'Idle';
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

<section class="chat-shell">
	<ChatSidebar
		myRooms={filteredMyRooms}
		discoverableRooms={filteredDiscoverableRooms}
		activeRoomId={roomId}
		{showLeftMenu}
		bind:chatListSearch
		on:select={(event) => selectRoom(event.detail.id, event.detail.isMember)}
		on:toggleMenu={toggleLeftMenu}
		on:createRoom={createRoomFromMenu}
	/>

	<section class="chat-window">
		<header
			class="chat-header"
			role="button"
			tabindex="0"
			on:click={openRoomDetails}
			on:keydown={onChatHeaderKeyDown}
		>
			<div class="room-title-button">
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
			</div>

			<div class="header-actions">
				<span class="connection {wsState}">{connectionLabel}</span>
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
			on:toggleExpand={(event) => toggleMessageExpanded(event.detail.messageId)}
			on:joinBreakRoom={onJoinBreakRoom}
			on:joinRoom={() => void joinCurrentRoom()}
			on:messageSelect={onMessageSelected}
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

	<OnlinePanel members={currentOnlineMembers} />
</section>

{#if showRoomDetails}
	<button
		type="button"
		class="mobile-info-backdrop"
		aria-label="Close room details"
		on:click={closeRoomDetails}
	></button>
	<section class="mobile-info-panel room-details-panel" role="dialog" aria-modal="true">
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
				<p>Manually extends this room for 24 hours (up to 15 days total).</p>
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
		grid-template-columns: 320px minmax(0, 1fr) 270px;
		border-top: 1px solid #d9dee4;
		background: #f3f5f7;
	}

	.chat-window {
		display: flex;
		flex-direction: column;
		min-width: 0;
		background: #efeae2;
	}

	.chat-header {
		position: relative;
		background: #f6f8fa;
		border-bottom: 1px solid #d9dee4;
		padding: 0.8rem 1rem;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 1rem;
		cursor: pointer;
	}

	.chat-header:focus-visible {
		outline: 2px solid #22c55e;
		outline-offset: -2px;
	}

	.room-title-button {
		display: flex;
		align-items: center;
		gap: 0.55rem;
		color: #0f172a;
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
	}

	.title-main {
		font-size: 0.98rem;
		font-weight: 700;
	}

	.title-sub {
		font-size: 0.76rem;
		color: #64748b;
	}

	.header-actions {
		display: flex;
		align-items: center;
		gap: 0.45rem;
		position: relative;
		cursor: default;
	}

	.connection {
		font-size: 0.74rem;
		font-weight: 700;
		padding: 0.2rem 0.5rem;
		border-radius: 999px;
		background: #dbe5f1;
		color: #1e293b;
	}

	.connection.open {
		background: #dcfce7;
		color: #166534;
	}

	.connection.connecting {
		background: #fef9c3;
		color: #854d0e;
	}

	.connection.error,
	.connection.closed {
		background: #fee2e2;
		color: #b91c1c;
	}

	.icon-button {
		border: 1px solid #cdd7e1;
		background: #ffffff;
		border-radius: 6px;
		padding: 0.35rem 0.55rem;
		font-size: 0.78rem;
		cursor: pointer;
	}

	.room-menu {
		position: absolute;
		top: calc(100% + 6px);
		right: 0;
		background: #ffffff;
		border: 1px solid #d8e0e9;
		border-radius: 8px;
		box-shadow: 0 8px 20px rgba(15, 23, 42, 0.12);
		overflow: hidden;
		min-width: 170px;
		z-index: 100;
	}

	.room-menu button {
		width: 100%;
		border: none;
		background: #ffffff;
		padding: 0.55rem 0.75rem;
		text-align: left;
		font-size: 0.84rem;
		cursor: pointer;
	}

	.room-menu button:hover {
		background: #f3f6fa;
	}

	.selection-banner {
		padding: 0.45rem 0.9rem;
		background: #fff8e1;
		border-bottom: 1px solid #f8ddb2;
		font-size: 0.8rem;
		color: #7c4a03;
	}

	.chat-search-row {
		padding: 0.65rem 0.9rem;
		background: #f6f8fa;
		border-bottom: 1px solid #d9dee4;
	}

	.chat-search-row input {
		width: 100%;
		border: 1px solid #cfd8e3;
		border-radius: 8px;
		padding: 0.55rem 0.7rem;
		font-size: 0.9rem;
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
		color: #172132;
	}

	.member-meta {
		font-size: 0.75rem;
		color: #64748b;
	}

	.empty-label {
		color: #64748b;
		font-size: 0.84rem;
		padding: 1rem;
	}

	.mobile-info-backdrop {
		position: fixed;
		inset: 0;
		background: rgba(15, 23, 42, 0.35);
		border: none;
		z-index: 150;
	}

	.mobile-info-panel {
		position: fixed;
		right: 0;
		top: 0;
		height: 100vh;
		width: min(92vw, 320px);
		background: #ffffff;
		z-index: 160;
		box-shadow: -14px 0 30px rgba(15, 23, 42, 0.2);
		display: flex;
		flex-direction: column;
	}

	.mobile-info-panel header {
		padding: 0.9rem 1rem;
		border-bottom: 1px solid #e8edf3;
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.mobile-info-panel header h3 {
		margin: 0;
		font-size: 1rem;
	}

	.mobile-info-panel header button {
		border: 1px solid #d4dce6;
		background: #ffffff;
		border-radius: 7px;
		padding: 0.32rem 0.5rem;
		cursor: pointer;
	}

	.mobile-info-content {
		padding: 0.7rem 0.85rem;
		overflow: auto;
	}

	.room-actions {
		margin-bottom: 0.9rem;
		padding: 0.75rem;
		border: 1px solid #e2e8f0;
		border-radius: 10px;
		background: #f8fafc;
	}

	.room-details-card {
		margin-bottom: 0.9rem;
		padding: 0.75rem;
		border: 1px solid #e2e8f0;
		border-radius: 10px;
		background: #f8fafc;
	}

	.room-details-card h4 {
		margin: 0 0 0.5rem;
		font-size: 0.88rem;
		color: #0f172a;
	}

	.room-detail-row {
		display: flex;
		justify-content: space-between;
		align-items: baseline;
		gap: 0.65rem;
		font-size: 0.8rem;
		color: #475569;
	}

	.room-detail-row + .room-detail-row {
		margin-top: 0.35rem;
	}

	.room-detail-row strong {
		color: #0f172a;
		font-weight: 600;
	}

	.members-title {
		margin: 0 0 0.35rem;
		font-size: 0.88rem;
		color: #0f172a;
	}

	.room-actions p {
		margin: 0.45rem 0 0;
		font-size: 0.78rem;
		color: #475569;
	}

	.extend-room-button {
		width: 100%;
		border: 1px solid #15803d;
		background: #16a34a;
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
		background: #1f2937;
		color: #ffffff;
		padding: 0.65rem 1rem;
		border-radius: 999px;
		font-size: 0.87rem;
		font-weight: 600;
		box-shadow: 0 12px 24px rgba(0, 0, 0, 0.22);
		z-index: 500;
		pointer-events: none;
	}

	@media (max-width: 1199px) {
		.chat-shell {
			grid-template-columns: 290px minmax(0, 1fr);
		}
	}

	@media (max-width: 900px) {
		.chat-shell {
			grid-template-columns: 1fr;
			grid-template-rows: minmax(220px, 36%) minmax(0, 64%);
			height: calc(100vh - 72px);
		}

		.chat-window {
			min-height: 0;
		}
	}
</style>
