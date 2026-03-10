<script lang="ts">
	import { browser } from '$app/environment';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import ChatComposer from '$lib/components/chat/ChatComposer.svelte';
	import Board from '$lib/components/chat/Board.svelte';
	import CodeCanvas from '$lib/components/canvas/CodeCanvas.svelte';
	import DiscussionModal from '$lib/components/chat/DiscussionModal.svelte';
	import FloatingActivityBox from '$lib/components/chat/FloatingActivityBox.svelte';
	import ChatRoomDetailsPanel from '$lib/components/chat/ChatRoomDetailsPanel.svelte';
	import MonochromeRoomBackground from '$lib/components/background/MonochromeRoomBackground.svelte';
	import PrivateAiChat from '$lib/components/chat/PrivateAiChat.svelte';
	import ChatRoomHeader from '$lib/components/chat/ChatRoomHeader.svelte';
	import RoomDashboard from '$lib/components/chat/RoomDashboard.svelte';
	import ChatStatusBars from '$lib/components/chat/ChatStatusBars.svelte';
	import ChatSidebar from '$lib/components/chat/ChatSidebar.svelte';
	import ProjectWorkspace from '$lib/components/workspace/ProjectWorkspace.svelte';
	import ChatUiDialog from '$lib/components/chat/ChatUiDialog.svelte';
	import ChatWindow from '$lib/components/chat/ChatWindow.svelte';
	import OnlinePanel from '$lib/components/chat/OnlinePanel.svelte';
	import { APP_LIMITS } from '$lib/config/limits';
	import { clearTaskStore, initializeTaskStoreForRoom } from '$lib/stores/tasks';
	import {
		activeRoomPassword,
		authToken,
		currentUser,
		isDarkMode,
		sessionAIEnabled,
		sessionE2EEnabled
	} from '$lib/store';
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
	import type {
		RoomDashboardItem,
		RoomDashboardOrganizeSections,
		WorkspaceModule
	} from '$lib/types/dashboard';
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
	import { formatBeaconTimestamp, parseBeaconMessagePayload } from '$lib/utils/chat/beacon';
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
	import { WebRTCManager, type CallType, type IncomingCallEvent } from '$lib/utils/chat/webrtc';
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
		applyMessageReactionsState,
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
		readSessionChatLayoutPreferences,
		readSessionRoomPreferences,
		writeSessionChatLayoutPreferences,
		writeSessionRoomPreferences
	} from '$lib/utils/sessionPreferences';
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
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';
	const CLIENT_DEBUG = (import.meta.env.VITE_CHAT_DEBUG as string | undefined) === '1';
	const TYPING_PING_INTERVAL_MS = 3000;
	const TYPING_STOP_DELAY_MS = 5000;
	const TYPING_SAFETY_TIMEOUT_MS = 7000;
	const REMOTE_TYPING_MAX_FUTURE_MS = APP_LIMITS.chat.remoteTypingMaxFutureMs;
	const DISCUSSION_MAX_REPLY_DEPTH = APP_LIMITS.chat.discussionMaxReplyDepth;
	const THEME_PREFERENCE_KEY = 'converse_theme_preference';
	const PROTECTED_ROOM_PREVIEW_TEXT = 'Protected room. Join with password to preview messages.';
	const CANVAS_SNIPPET_PAYLOAD_KIND = 'canvas_snippet_v1';
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

	type CallStreamSlot = { userId: string; stream: MediaStream };
	type CallParticipantEntry = { userId: string; name: string; isLocal: boolean };
	type CallMemberPresenceEntry = {
		userId: string;
		name: string;
		joinedAt: number;
		leftAt: number | null;
	};

	type RoomFeatureFlags = {
		aiEnabled: boolean;
		e2eEnabled: boolean;
	};
	type GlobalQuickAction =
		| 'create-room'
		| 'open-room-list'
		| 'open-chat-pane'
		| 'toggle-search'
		| 'toggle-discussion-mode'
		| 'open-board-dashboard'
		| 'open-board-draw'
		| 'open-board-code'
		| 'open-board-tasks'
		| 'mark-active-read';
	type BoardWorkspaceModule = Exclude<WorkspaceModule, 'code'>;
	type DashboardAddItemKind = 'note' | 'beacon' | 'task';
	type DashboardAddItemRequestDetail =
		| { kind: 'note'; text?: string }
		| { kind: 'beacon'; text?: string; beaconAt?: number }
		| { kind: 'task'; title?: string; details?: string };

	const CALL_SIGNAL_TYPES = new Set([
		'call_invite',
		'webrtc_offer',
		'webrtc_answer',
		'webrtc_ice',
		'call_cancel'
	]);
	const CALL_MAX_PARTICIPANTS = APP_LIMITS.calls.maxParticipants;
	const INCOMING_CALL_TIMEOUT_MS = APP_LIMITS.calls.incomingTimeoutMs;
	const EMPTY_CALL_GRACE_MS = APP_LIMITS.calls.emptyGraceMs;
	const TASK_BOARD_LIMIT_BYTES = APP_LIMITS.tasks.boardMaxBytes;
	const TASK_BOARD_LIMIT_MESSAGE = `Task Board memory limit (${Math.max(
		1,
		Math.round(TASK_BOARD_LIMIT_BYTES / (1024 * 1024))
	)}MB) reached.`;
	const DASHBOARD_STORAGE_PREFIX = 'converse:room-dashboard:v1';
	const WORKSPACE_MODULES: WorkspaceModule[] = ['dashboard', 'draw', 'code', 'tasks'];
	const BOARD_WORKSPACE_MODULES: BoardWorkspaceModule[] = ['dashboard', 'draw', 'tasks'];
	const COMPACT_NAV_BREAKPOINT = 600;
	const PANEL_COLLAPSE_BREAKPOINT = 600;

	function getCanvasPresenceColor(user: { color?: unknown } | null | undefined) {
		if (typeof user?.color === 'string') {
			const normalized = user.color.trim();
			if (normalized) {
				return normalized;
			}
		}
		return '#3b82f6';
	}

	function isTaskBoardPayloadWithinLimit(payload: ReturnType<typeof parseTaskMessagePayload>) {
		if (!payload) {
			return false;
		}
		const taskArrayJSON = JSON.stringify(payload.tasks ?? []);
		return getUTF8ByteLength(taskArrayJSON) <= TASK_BOARD_LIMIT_BYTES;
	}

	function roomDashboardStorageKey(targetRoomId: string) {
		const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
		if (!normalizedRoomID) {
			return '';
		}
		return `${DASHBOARD_STORAGE_PREFIX}:${normalizedRoomID}`;
	}

	function parseRoomDashboardItem(
		source: unknown,
		fallbackRoomId: string
	): RoomDashboardItem | null {
		if (!source || typeof source !== 'object' || Array.isArray(source)) {
			return null;
		}
		const record = source as Record<string, unknown>;
		const roomId = normalizeRoomIDValue(toStringValue(record.roomId || fallbackRoomId));
		const messageId = normalizeMessageID(toStringValue(record.messageId));
		const id = normalizeMessageID(toStringValue(record.id));
		const kindValue = toStringValue(record.kind).toLowerCase();
		const kind = kindValue === 'task' || kindValue === 'note' ? kindValue : 'message';
		const beaconData =
			record.beaconData &&
			typeof record.beaconData === 'object' &&
			!Array.isArray(record.beaconData)
				? { ...(record.beaconData as Record<string, unknown>) }
				: null;
		if (!roomId || !messageId || !id) {
			return null;
		}
		return {
			id,
			roomId,
			messageId,
			kind,
			senderId: normalizeIdentifier(toStringValue(record.senderId)),
			senderName: normalizeUsernameValue(toStringValue(record.senderName)) || 'User',
			pinnedByUserId: normalizeIdentifier(toStringValue(record.pinnedByUserId)),
			pinnedByName: normalizeUsernameValue(toStringValue(record.pinnedByName)) || 'User',
			originalCreatedAt: parseOptionalTimestamp(record.originalCreatedAt) || Date.now(),
			pinnedAt: parseOptionalTimestamp(record.pinnedAt) || Date.now(),
			messageText: toStringValue(record.messageText).trim(),
			mediaUrl: toStringValue(record.mediaUrl).trim(),
			mediaType: toStringValue(record.mediaType).trim(),
			fileName: toStringValue(record.fileName).trim(),
			note: toStringValue(record.note).trim(),
			beaconAt: parseOptionalTimestamp(record.beaconAt) || null,
			beaconLabel: toStringValue(record.beaconLabel).trim(),
			beaconData,
			taskTitle: toStringValue(record.taskTitle).trim(),
			topic: toStringValue(record.topic).trim()
		};
	}

	function readRoomDashboardItems(targetRoomId: string) {
		if (!browser) {
			return [] as RoomDashboardItem[];
		}
		const key = roomDashboardStorageKey(targetRoomId);
		if (!key) {
			return [] as RoomDashboardItem[];
		}
		try {
			const raw = window.localStorage.getItem(key);
			if (!raw) {
				return [] as RoomDashboardItem[];
			}
			const parsed = JSON.parse(raw);
			if (!Array.isArray(parsed)) {
				return [] as RoomDashboardItem[];
			}
			return parsed
				.map((entry) => parseRoomDashboardItem(entry, targetRoomId))
				.filter((entry): entry is RoomDashboardItem => Boolean(entry))
				.sort((left, right) => right.pinnedAt - left.pinnedAt);
		} catch {
			return [] as RoomDashboardItem[];
		}
	}

	function writeRoomDashboardItems(targetRoomId: string, items: RoomDashboardItem[]) {
		if (!browser) {
			return;
		}
		const key = roomDashboardStorageKey(targetRoomId);
		if (!key) {
			return;
		}
		window.localStorage.setItem(key, JSON.stringify(items));
	}

	function resolveDashboardBeaconAt(
		message: ChatMessage,
		taskPayload: ReturnType<typeof parseTaskMessagePayload>
	) {
		const source = message as Record<string, unknown>;
		const beaconPayload = parseBeaconMessagePayload(toStringValue(message.content));
		if (beaconPayload && beaconPayload.beaconAt > 0) {
			return beaconPayload.beaconAt;
		}
		const directCandidate =
			parseOptionalTimestamp(
				source.beaconAt ?? source.beacon_at ?? source.scheduledAt ?? source.scheduled_at
			) || parseOptionalTimestamp(source.deadline ?? source.dueAt ?? source.due_at);
		if (directCandidate > 0) {
			return directCandidate;
		}
		if (!taskPayload) {
			return null;
		}
		const taskTimes = (taskPayload.tasks || [])
			.map((task) => parseOptionalTimestamp(task.timestamp))
			.filter((timestamp) => timestamp > 0);
		if (taskTimes.length === 0) {
			return null;
		}
		return Math.min(...taskTimes);
	}

	function normalizeRoomFeatureFlags(aiEnabled: boolean, e2eEnabled: boolean): RoomFeatureFlags {
		const normalizedE2E = Boolean(e2eEnabled);
		const normalizedAI = normalizedE2E ? false : Boolean(aiEnabled);
		return {
			aiEnabled: normalizedAI,
			e2eEnabled: normalizedE2E
		};
	}

	function parseRoomFeatureFlags(
		source: Record<string, unknown> | null | undefined,
		fallback: Partial<RoomFeatureFlags> = {}
	): RoomFeatureFlags {
		const aiDefault = typeof fallback.aiEnabled === 'boolean' ? fallback.aiEnabled : true;
		const e2eDefault = typeof fallback.e2eEnabled === 'boolean' ? fallback.e2eEnabled : false;
		if (!source) {
			return normalizeRoomFeatureFlags(aiDefault, e2eDefault);
		}
		const rawAI = source.aiEnabled ?? source.ai_enabled ?? aiDefault;
		const rawE2E =
			source.e2eEnabled ??
			source.e2e_enabled ??
			source.e2eeEnabled ??
			source.e2ee_enabled ??
			e2eDefault;
		return normalizeRoomFeatureFlags(toBool(rawAI), toBool(rawE2E));
	}

	function syncSessionRoomPreferencesFromStorage() {
		const preferences = readSessionRoomPreferences();
		const normalized = writeSessionRoomPreferences(preferences);
		sessionAIEnabled.set(normalized.aiEnabled);
		sessionE2EEnabled.set(normalized.e2eEnabled);
		return normalized;
	}

	function syncSessionChatLayoutPreferencesFromStorage() {
		const preferences = readSessionChatLayoutPreferences();
		const normalized = writeSessionChatLayoutPreferences(preferences);
		isOnlinePanelCollapsed = normalized.onlinePanelCollapsed;
		return normalized;
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
	let selectedWorkspaceModule: WorkspaceModule | null = null;
	let visibleBoardModules: BoardWorkspaceModule[] = [];
	let activeWorkspaceModules: WorkspaceModule[] = ['dashboard'];
	let roomDashboardItems: RoomDashboardItem[] = [];
	let roomDashboardOrganizePreview: RoomDashboardOrganizeSections | null = null;
	let isDrawBoardActive = false;
	let isTaskBoardActive = false;
	let isDashboardActive = false;
	let addableWorkspaceModules: WorkspaceModule[] = [];
	let showPrivateAiChat = false;
	let isCanvasOpen = false;
	let isCanvasFullscreen = false;
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
	let isCompactNavViewport = false;
	let canCollapseRoomList = false;
	let canCollapseOnlinePanel = false;
	let isRoomListCollapsed = false;
	let isOnlinePanelCollapsed = false;
	let isOnlinePanelAutoCollapsed = false;
	let onlinePanelCollapsedBeforeAuto = false;
	let mobilePane: 'list' | 'chat' = 'chat';
	let focusMessageId = '';
	let focusConsumedForRoom = false;
	let focusRoomTracker = '';
	let lastTaskStoreRoomId = '';
	let activeRoomId = '';
	let activeFirstUnreadMessageId = '';

	let roomThreads: ChatThread[] = [];
	let totalUnreadMessages = 0;
	let unseenBoardChangeCount = 0;
	let seenDashboardItemIdsByRoom: Record<string, string[]> = {};
	let messagesByRoom: Record<string, ChatMessage[]> = {};
	let onlineByRoom: Record<string, OnlineMember[]> = {};
	let roomMetaById: Record<string, RoomMeta> = {};
	let typingUsersByRoom: Record<string, Record<string, { name: string; expiresAt: number }>> = {};
	let activeTypingUsers: string[] = [];
	let typingNamesPreview = '';
	let typingIndicatorText = '';
	let hasTypingUsers = false;
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
	let isActiveRoomEphemeral = true;
	let activeRemainingLabel = '--';
	let isRoomExpired = false;
	let serverClockOffsetMs = 0;
	let serverNowAnchorMs = 0;
	let serverNowAnchorPerfMs = 0;
	let webrtcManager: WebRTCManager | null = null;
	let activeCall = false;
	let isRinging = false;
	let incomingCall: IncomingCallEvent | null = null;
	let callType: CallType = 'audio';
	let localCallStream: MediaStream | null = null;
	let remoteCallStreams: CallStreamSlot[] = [];
	let callParticipants: CallParticipantEntry[] = [];
	let callRingingUserIds: string[] = [];
	let callRingingParticipants: CallParticipantEntry[] = [];
	let activeRemoteCallParticipantCount = 0;
	let callMemberPresenceByUserId: Record<string, Omit<CallMemberPresenceEntry, 'userId'>> = {};
	let callParticipantSnapshotIds: string[] = [];
	let showCallMembersPanel = false;
	let callMemberPresenceList: CallMemberPresenceEntry[] = [];
	let activeCallMemberPresence: CallMemberPresenceEntry[] = [];
	let departedCallMemberPresence: CallMemberPresenceEntry[] = [];
	let isMuted = false;
	let isCameraEnabled = true;
	let isCallMinimized = false;
	let callStartedAtMs = 0;
	let callDurationLabel = '00:00';
	let callDurationTicker: ReturnType<typeof setInterval> | null = null;
	let incomingCallExpireTimer: ReturnType<typeof setTimeout> | null = null;
	let callEmptyGraceTimer: ReturnType<typeof setTimeout> | null = null;
	let callHadRemoteParticipant = false;
	let webrtcContextKey = '';
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
	$: activeRoomFeatures = normalizeRoomFeatureFlags(
		activeThread?.aiEnabled ?? $sessionAIEnabled,
		activeThread?.e2eEnabled ?? $sessionE2EEnabled
	);
	$: activeRoomAllowsAI = activeRoomFeatures.aiEnabled && !activeRoomFeatures.e2eEnabled;
	$: currentMessages = activeThread?.status === 'left' ? [] : (messagesByRoom[roomId] ?? []);
	$: isDrawBoardActive = visibleBoardModules.includes('draw');
	$: isTaskBoardActive = visibleBoardModules.includes('tasks');
	$: isDashboardActive = visibleBoardModules.includes('dashboard');
	$: hasNonDashboardBoardOpen = isDrawBoardActive || isTaskBoardActive || isCanvasOpen;
	$: isOnlinePanelEffectivelyCollapsed =
		canCollapseOnlinePanel && (isOnlinePanelCollapsed || isOnlinePanelAutoCollapsed);
	$: {
		const shouldAutoCollapseOnlinePanel = canCollapseOnlinePanel && hasNonDashboardBoardOpen;
		if (shouldAutoCollapseOnlinePanel && !isOnlinePanelAutoCollapsed) {
			onlinePanelCollapsedBeforeAuto = isOnlinePanelCollapsed;
			isOnlinePanelAutoCollapsed = true;
		} else if (!shouldAutoCollapseOnlinePanel && isOnlinePanelAutoCollapsed) {
			isOnlinePanelAutoCollapsed = false;
			isOnlinePanelCollapsed = onlinePanelCollapsedBeforeAuto;
		}
	}
	$: addableWorkspaceModules = WORKSPACE_MODULES.filter(
		(module) => module !== 'dashboard' && !activeWorkspaceModules.includes(module)
	);
	$: if (selectedWorkspaceModule === 'code' && !isCanvasOpen) {
		isCanvasOpen = true;
	}
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
	$: {
		localCallStream;
		remoteCallStreams;
		currentUserId;
		currentUsername;
		currentOnlineMembers;
		activeCall;
		webrtcManager;
		callParticipants = buildCallParticipantEntries();
	}
	$: activeRemoteCallParticipantCount = callParticipants.filter(
		(participant) => !participant.isLocal
	).length;
	$: {
		callParticipants;
		callRingingUserIds;
		currentOnlineMembers;
		currentUserId;
		activeCall;
		const localUserId = normalizeIdentifier(currentUserId);
		const activeRemoteParticipantIDs = new Set(
			callParticipants
				.filter((participant) => !participant.isLocal)
				.map((participant) => normalizeIdentifier(participant.userId))
				.filter(Boolean)
		);
		const onlineMemberIDs = new Set(
			currentOnlineMembers.map((member) => normalizeIdentifier(member.id)).filter(Boolean)
		);
		const nextRingingUserIDs = callRingingUserIds.filter((userId) => {
			const normalizedUserID = normalizeIdentifier(userId);
			if (!normalizedUserID || normalizedUserID === localUserId) {
				return false;
			}
			if (!activeCall) {
				return false;
			}
			if (activeRemoteParticipantIDs.has(normalizedUserID)) {
				return false;
			}
			return onlineMemberIDs.has(normalizedUserID);
		});
		const hasChanged =
			nextRingingUserIDs.length !== callRingingUserIds.length ||
			nextRingingUserIDs.some((entry, index) => entry !== callRingingUserIds[index]);
		if (hasChanged) {
			callRingingUserIds = nextRingingUserIDs;
		}
	}
	$: callRingingParticipants = callRingingUserIds.map((userId) => ({
		userId,
		name: resolveCallUserName(userId),
		isLocal: false
	}));
	$: if (activeCall && activeRemoteCallParticipantCount > 0) {
		callHadRemoteParticipant = true;
	}
	$: callMemberPresenceList = Object.entries(callMemberPresenceByUserId)
		.map(([userId, entry]) => ({ userId, ...entry }))
		.sort((left, right) => left.joinedAt - right.joinedAt);
	$: activeCallMemberPresence = callMemberPresenceList.filter((entry) => entry.leftAt == null);
	$: departedCallMemberPresence = callMemberPresenceList
		.filter((entry) => entry.leftAt != null)
		.sort((left, right) => (right.leftAt ?? 0) - (left.leftAt ?? 0));
	$: trackCallMemberPresence(callParticipants, activeCall);
	$: if (!activeCall || !callHadRemoteParticipant || activeRemoteCallParticipantCount > 0) {
		clearCallEmptyGraceTimer();
	}
	$: if (activeCall && callHadRemoteParticipant && activeRemoteCallParticipantCount === 0) {
		scheduleEmptyCallAutoEnd();
	}
	$: if (incomingCall && !activeCall) {
		const callerID = normalizeIdentifier(incomingCall.fromUserId);
		const callerStillOnline = currentOnlineMembers.some(
			(member) => normalizeIdentifier(member.id) === callerID
		);
		if (!callerID || !callerStillOnline) {
			clearIncomingCallState();
		}
	}
	$: isActiveRoomAdmin = Boolean(activeThread?.isAdmin);
	$: isMember = resolveRoomMembership(roomId, roomThreads, roomMemberHint);
	$: canModerateBoard = isMember && !isRoomExpired && isActiveRoomAdmin;
	$: activeUnreadCount = activeThread?.unread ?? 0;
	$: totalUnreadMessages = roomThreads.reduce((total, thread) => {
		const unread = Number.isFinite(thread.unread) ? Math.max(0, Math.floor(thread.unread)) : 0;
		return total + unread;
	}, 0);
	$: {
		const normalizedRoomID = normalizeRoomIDValue(roomId);
		const seenIds = new Set(
			normalizedRoomID ? (seenDashboardItemIdsByRoom[normalizedRoomID] ?? []) : []
		);
		unseenBoardChangeCount = roomDashboardItems.filter((entry) => {
			const entryId = normalizeMessageID(entry.id);
			if (!entryId) {
				return false;
			}
			return !seenIds.has(entryId);
		}).length;
	}
	$: if (roomId && isDashboardActive) {
		markDashboardItemsSeen(roomId, roomDashboardItems);
	}
	$: activeFirstUnreadMessageId = getUnreadStartMessageId(roomId);
	$: activeLastReadTimestamp = getLastReadTimestamp(roomId);
	$: {
		typingUsersByRoom;
		activeTypingUsers = getActiveTypingUsers(roomId);
	}
	$: typingNamesPreview = formatTypingNamePreview(activeTypingUsers);
	$: typingIndicatorText = formatTypingIndicatorText(activeTypingUsers);
	$: hasTypingUsers = activeTypingUsers.length > 0;
	$: activeRoomCreatedAtMs = roomId ? (roomMetaById[roomId]?.createdAt ?? 0) : 0;
	$: activeRoomExpiresAtMs = roomId ? (roomMetaById[roomId]?.expiresAt ?? 0) : 0;
	$: isActiveRoomEphemeral = activeRoomExpiresAtMs > 0;
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
	$: if (canCollapseRoomList && isRoomListCollapsed && showLeftMenu) {
		showLeftMenu = false;
	}

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
	$: if (browser && identityReady && roomId && currentUserId) {
		ensureWebRTCManager();
	}
	$: if (browser && identityReady && roomId && roomId !== lastRoomMetaSyncRoomId) {
		lastRoomMetaSyncRoomId = roomId;
		void refreshRoomMetaFromServer(roomId);
	}
	$: if (browser && identityReady) {
		const normalizedRoomID = normalizeRoomIDValue(roomId);
		if (!normalizedRoomID) {
			lastTaskStoreRoomId = '';
			clearTaskStore();
		} else if (normalizedRoomID !== lastTaskStoreRoomId) {
			lastTaskStoreRoomId = normalizedRoomID;
			void initializeTaskStoreForRoom(normalizedRoomID, { apiBase: API_BASE });
		}
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
	$: if (browser) {
		roomThreads;
		mobilePane;
		isMobileView;
		isCompactNavViewport;
		totalUnreadMessages;
		activeUnreadCount;
		discussionBackgroundUnreadCount;
		unseenBoardChangeCount;
		publishQuickNavState();
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
		selectedWorkspaceModule = null;
		visibleBoardModules = [];
		activeWorkspaceModules = ['dashboard'];
		const initialDashboardItems = readRoomDashboardItems(roomId);
		roomDashboardItems = initialDashboardItems;
		const normalizedRoomID = normalizeRoomIDValue(roomId);
		if (normalizedRoomID && !seenDashboardItemIdsByRoom[normalizedRoomID]) {
			seenDashboardItemIdsByRoom = {
				...seenDashboardItemIdsByRoom,
				[normalizedRoomID]: initialDashboardItems
					.map((entry) => normalizeMessageID(entry.id))
					.filter((entry): entry is string => Boolean(entry))
			};
		}
		roomDashboardOrganizePreview = null;
		isCanvasOpen = false;
		isCanvasFullscreen = false;
	}
	$: if (isDiscussionOpen && !activeDiscussionTask) {
		isDiscussionOpen = false;
		discussionComments = [];
	}
	$: if (showPrivateAiChat && !activeRoomAllowsAI) {
		showPrivateAiChat = false;
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
		clearQuickNavState();
		typingController.destroy();
		clearTaskStore();
		clearAllCachePersistTimers();
		clearSidebarRefreshTimer();
		clearRoomExpiryTicker();
		clearToastTimer();
		stopCallDurationTicker();
		clearIncomingCallExpireTimer();
		clearCallEmptyGraceTimer();
		if (webrtcManager) {
			const duration = webrtcManager.endCall();
			clientLog('call-ended-on-destroy', { durationSeconds: duration });
			webrtcManager = null;
		}
	});

	onMount(() => {
		if (!browser) {
			return;
		}
		syncActiveRoomPasswordFromHash();
		syncSessionRoomPreferencesFromStorage();
		syncSessionChatLayoutPreferencesFromStorage();
		initializeTrustedDevicePreference();
		if (trustedCachingEnabled && roomId) {
			void hydrateOfflineCache(roomId);
		}
		void initializeIdentity();
		updateViewportMode();
		window.addEventListener('resize', updateViewportMode);
		window.addEventListener('converse:quick-action', onGlobalQuickAction as EventListener);
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
			window.removeEventListener('converse:quick-action', onGlobalQuickAction as EventListener);
			clearQuickNavState();
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
		const viewportWidth = window.innerWidth;
		isMobileView = viewportWidth <= 900;
		isCompactNavViewport = viewportWidth < COMPACT_NAV_BREAKPOINT;
		canCollapseRoomList = viewportWidth > PANEL_COLLAPSE_BREAKPOINT && !isMobileView;
		canCollapseOnlinePanel = viewportWidth > 1199 && viewportWidth > PANEL_COLLAPSE_BREAKPOINT;
		if (!canCollapseRoomList) {
			isRoomListCollapsed = false;
		}
		if (!canCollapseOnlinePanel) {
			isOnlinePanelAutoCollapsed = false;
		}
		if (!isMobileView) {
			mobilePane = 'chat';
		}
	}

	function markDashboardItemsSeen(targetRoomId: string, items: RoomDashboardItem[]) {
		const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
		if (!normalizedRoomID) {
			return;
		}
		const nextSeen = items
			.map((entry) => normalizeMessageID(entry.id))
			.filter((entry): entry is string => Boolean(entry));
		const prevSeen = seenDashboardItemIdsByRoom[normalizedRoomID] ?? [];
		const unchanged =
			nextSeen.length === prevSeen.length &&
			nextSeen.every((entry, index) => entry === prevSeen[index]);
		if (unchanged) {
			return;
		}
		seenDashboardItemIdsByRoom = {
			...seenDashboardItemIdsByRoom,
			[normalizedRoomID]: nextSeen
		};
	}

	function publishQuickNavState() {
		if (!browser || typeof window === 'undefined') {
			return;
		}
		window.dispatchEvent(
			new CustomEvent('converse:chat-nav-state', {
				detail: {
					isCompact: isCompactNavViewport,
					pane: isMobileView ? mobilePane : 'chat',
					totalUnread: totalUnreadMessages,
					activeUnread: activeUnreadCount,
					discussionUnread: discussionBackgroundUnreadCount,
					boardUnread: unseenBoardChangeCount
				}
			})
		);
	}

	function clearQuickNavState() {
		if (!browser || typeof window === 'undefined') {
			return;
		}
		window.dispatchEvent(new CustomEvent('converse:chat-nav-state', { detail: null }));
	}

	function onGlobalQuickAction(event: Event) {
		const customEvent = event as CustomEvent<{ action?: unknown }>;
		const action = toStringValue(customEvent.detail?.action)
			.trim()
			.toLowerCase() as GlobalQuickAction;
		if (!action) {
			return;
		}
		switch (action) {
			case 'create-room':
				void createRoomFromMenu();
				return;
			case 'open-room-list':
				showMobileRoomList();
				return;
			case 'open-chat-pane':
				if (isMobileView) {
					mobilePane = 'chat';
				}
				return;
			case 'toggle-search':
				toggleRoomSearch();
				return;
			case 'toggle-discussion-mode':
				void openLatestDiscussionFromTaskbar();
				return;
			case 'open-board-dashboard':
				if (isMobileView) {
					mobilePane = 'chat';
				}
				openWorkspaceModule('dashboard', { allowSplitCanvas: true });
				return;
			case 'open-board-draw':
				if (isMobileView) {
					mobilePane = 'chat';
				}
				openWorkspaceModule('draw', { allowSplitCanvas: true });
				return;
			case 'open-board-code':
				if (isMobileView) {
					mobilePane = 'chat';
				}
				openWorkspaceModule('code', { allowSplitCanvas: true });
				return;
			case 'open-board-tasks':
				if (isMobileView) {
					mobilePane = 'chat';
				}
				openWorkspaceModule('tasks', { allowSplitCanvas: true });
				return;
			case 'mark-active-read':
				markRoomAsRead(roomId);
				return;
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
		const rawValue = event.detail?.value || '';
		typingController.onComposerTyping(rawValue);
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

	function truncateTypingName(name: string, maxChars = 7) {
		const cleaned = name.trim();
		if (!cleaned) {
			return 'User';
		}
		if (cleaned.toLowerCase().includes('tora')) {
			return 'ToraAI';
		}
		if (cleaned.length <= maxChars) {
			return cleaned;
		}
		return `${cleaned.slice(0, maxChars)}...`;
	}

	function formatTypingNamePreview(names: string[]) {
		if (!names || names.length === 0) {
			return '';
		}
		const visible = names.slice(0, 2).map((name) => truncateTypingName(name));
		if (names.length > visible.length) {
			return `${visible.join(', ')}, ....`;
		}
		return visible.join(', ');
	}

	function formatTypingIndicatorText(names: string[]) {
		if (!names || names.length === 0) {
			return '';
		}
		const visible = names.slice(0, 2).map((name) => truncateTypingName(name));
		if (visible.length === 1) {
			return `${visible[0]} is typing...`;
		}
		if (names.length > visible.length) {
			return `${visible.join(', ')} and ${names.length - visible.length} others are typing...`;
		}
		return `${visible.join(', ')} are typing...`;
	}

	function handleTypingSignalPayload(payload: unknown) {
		if (!payload || typeof payload !== 'object') {
			return false;
		}
		const source = payload as Record<string, unknown>;
		const kind = toStringValue(source.type).toLowerCase();
		if (kind !== 'typing_start' && kind !== 'typing_stop') {
			return false;
		}
		let nestedPayload: Record<string, unknown> = {};
		if (source.payload && typeof source.payload === 'object' && !Array.isArray(source.payload)) {
			nestedPayload = source.payload as Record<string, unknown>;
		} else if (typeof source.payload === 'string') {
			try {
				const parsed = JSON.parse(source.payload);
				if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
					nestedPayload = parsed as Record<string, unknown>;
				}
			} catch {
				// ignore malformed payload; fallback to top-level fields below
			}
		}
		const targetRoomId = normalizeRoomIDValue(
			toStringValue(
				source.roomId ?? source.room_id ?? nestedPayload.roomId ?? nestedPayload.room_id ?? roomId
			)
		);
		if (!targetRoomId) {
			return true;
		}
		const participantId = normalizeIdentifier(
			toStringValue(
				nestedPayload.id ??
					nestedPayload.userId ??
					nestedPayload.user_id ??
					source.id ??
					source.userId ??
					source.user_id
			)
		);
		if (!participantId) {
			return true;
		}
		const participantName =
			normalizeUsernameValue(
				toStringValue(
					nestedPayload.name ??
						nestedPayload.username ??
						nestedPayload.userName ??
						nestedPayload.user_name ??
						source.name ??
						source.username ??
						source.userName ??
						source.user_name
				)
			) || 'User';
		const now = Date.now();
		const remoteExpiresAt = toInt(
			nestedPayload.expiresAt ?? nestedPayload.expires_at ?? source.expiresAt ?? source.expires_at
		);
		const expiresAt =
			remoteExpiresAt > now && remoteExpiresAt <= now + REMOTE_TYPING_MAX_FUTURE_MS
				? remoteExpiresAt
				: now + TYPING_SAFETY_TIMEOUT_MS;
		if (kind === 'typing_start') {
			setTypingIndicator(targetRoomId, participantId, participantName, expiresAt);
		} else {
			clearTypingIndicator(targetRoomId, participantId);
		}
		return true;
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
			const joinedFeatureFlags = parseRoomFeatureFlags(data as Record<string, unknown>, {
				aiEnabled: true,
				e2eEnabled: false
			});

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
								requiresPassword: joinedRequiresPassword,
								aiEnabled: joinedFeatureFlags.aiEnabled,
								e2eEnabled: joinedFeatureFlags.e2eEnabled
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

	function formatCallDuration(totalSeconds: number) {
		const safeSeconds = Math.max(0, Math.floor(totalSeconds));
		const hours = Math.floor(safeSeconds / 3600);
		const minutes = Math.floor((safeSeconds % 3600) / 60);
		const seconds = safeSeconds % 60;
		if (hours > 0) {
			return `${hours}h ${minutes.toString().padStart(2, '0')}m ${seconds
				.toString()
				.padStart(2, '0')}s`;
		}
		return `${minutes}m ${seconds.toString().padStart(2, '0')}s`;
	}

	function refreshCallDurationLabel() {
		if (!callStartedAtMs) {
			callDurationLabel = '00:00';
			return;
		}
		const elapsedSeconds = Math.max(0, Math.floor((Date.now() - callStartedAtMs) / 1000));
		const minutes = Math.floor(elapsedSeconds / 60);
		const seconds = elapsedSeconds % 60;
		callDurationLabel = `${minutes.toString().padStart(2, '0')}:${seconds
			.toString()
			.padStart(2, '0')}`;
	}

	function startCallDurationTicker() {
		stopCallDurationTicker();
		refreshCallDurationLabel();
		callDurationTicker = setInterval(() => {
			refreshCallDurationLabel();
		}, 1000);
	}

	function stopCallDurationTicker() {
		if (callDurationTicker) {
			clearInterval(callDurationTicker);
			callDurationTicker = null;
		}
	}

	function clearIncomingCallExpireTimer() {
		if (!incomingCallExpireTimer) {
			return;
		}
		clearTimeout(incomingCallExpireTimer);
		incomingCallExpireTimer = null;
	}

	function clearCallEmptyGraceTimer() {
		if (!callEmptyGraceTimer) {
			return;
		}
		clearTimeout(callEmptyGraceTimer);
		callEmptyGraceTimer = null;
	}

	function clearIncomingCallState() {
		incomingCall = null;
		isRinging = false;
		clearIncomingCallExpireTimer();
	}

	function scheduleIncomingCallExpiry() {
		clearIncomingCallExpireTimer();
		incomingCallExpireTimer = setTimeout(() => {
			if (activeCall || !incomingCall) {
				return;
			}
			clearIncomingCallState();
		}, INCOMING_CALL_TIMEOUT_MS);
	}

	function scheduleEmptyCallAutoEnd() {
		if (callEmptyGraceTimer) {
			return;
		}
		callEmptyGraceTimer = setTimeout(() => {
			void endCallWhenParticipantsLeave();
		}, EMPTY_CALL_GRACE_MS);
	}

	function sendCallCancelSignal(targetUserId = '') {
		if (!roomId || !isMember) {
			return;
		}
		const payload: Record<string, unknown> = {
			type: 'call_cancel',
			roomId,
			callType
		};
		const normalizedTargetUserID = normalizeIdentifier(targetUserId);
		if (normalizedTargetUserID) {
			payload.targetUserId = normalizedTargetUserID;
		}
		sendSocketPayload(payload);
	}

	function syncCallStreamsFromManager() {
		if (!webrtcManager) {
			localCallStream = null;
			remoteCallStreams = [];
			return;
		}
		localCallStream = webrtcManager.getLocalStream();
		remoteCallStreams = webrtcManager
			.getRemoteStreamEntries()
			.filter((entry) => streamHasLiveTracks(entry.stream));
	}

	function handleIncomingCallEvent(event: IncomingCallEvent) {
		const fromUserId = normalizeIdentifier(event.fromUserId);
		if (!fromUserId || fromUserId === normalizeIdentifier(currentUserId)) {
			return;
		}
		const callerStillOnline = currentOnlineMembers.some(
			(member) => normalizeIdentifier(member.id) === fromUserId
		);
		if (!callerStillOnline) {
			return;
		}
		incomingCall = event;
		callType = event.callType;
		if (activeCall) {
			return;
		}
		isRinging = true;
		scheduleIncomingCallExpiry();
	}

	function resetCallUiState() {
		activeCall = false;
		isRinging = false;
		incomingCall = null;
		clearIncomingCallExpireTimer();
		clearCallEmptyGraceTimer();
		localCallStream = null;
		remoteCallStreams = [];
		callParticipants = [];
		callRingingUserIds = [];
		callMemberPresenceByUserId = {};
		callParticipantSnapshotIds = [];
		showCallMembersPanel = false;
		isMuted = false;
		isCameraEnabled = false;
		isCallMinimized = false;
		callHadRemoteParticipant = false;
		callStartedAtMs = 0;
		callDurationLabel = '00:00';
		stopCallDurationTicker();
	}

	function ensureWebRTCManager() {
		if (!browser || !roomId || !currentUserId) {
			return;
		}
		const nextContextKey = `${normalizeRoomIDValue(roomId)}|${normalizeIdentifier(currentUserId)}`;
		if (webrtcContextKey && webrtcContextKey !== nextContextKey) {
			if (webrtcManager) {
				webrtcManager.endCall();
			}
			resetCallUiState();
		}

		if (!webrtcManager) {
			webrtcManager = new WebRTCManager({
				roomId,
				userId: currentUserId,
				userName: currentUsername,
				sendSignal: (payload) => {
					sendSocketPayload(payload);
				},
				maxParticipants: CALL_MAX_PARTICIPANTS,
				onIncomingCall: (event) => {
					handleIncomingCallEvent(event);
				},
				onRemoteStream: () => {
					syncCallStreamsFromManager();
				},
				onRemoteStreamRemoved: () => {
					syncCallStreamsFromManager();
				},
				onPeerStateChange: () => {
					syncCallStreamsFromManager();
				}
			});
		}

		webrtcManager.updateContext(roomId, currentUserId, currentUsername);
		webrtcContextKey = nextContextKey;
		syncCallStreamsFromManager();
	}

	function getCallInviteTargetUserIds() {
		if (!webrtcManager) {
			return [];
		}
		const connected = new Set(
			webrtcManager.getPeerUserIds().map((entry) => normalizeIdentifier(entry))
		);
		return currentOnlineMembers
			.map((member) => normalizeIdentifier(member.id))
			.filter((memberId) => {
				if (!memberId) {
					return false;
				}
				if (memberId === normalizeIdentifier(currentUserId)) {
					return false;
				}
				return !connected.has(memberId);
			});
	}

	function normalizeCallUserIdList(userIds: string[]) {
		const next: string[] = [];
		const seen = new Set<string>();
		for (const candidate of userIds) {
			const normalizedUserID = normalizeIdentifier(candidate);
			if (!normalizedUserID || seen.has(normalizedUserID)) {
				continue;
			}
			seen.add(normalizedUserID);
			next.push(normalizedUserID);
		}
		return next;
	}

	function setRingingUserIds(userIds: string[]) {
		callRingingUserIds = normalizeCallUserIdList(userIds);
	}

	function addRingingUserIds(userIds: string[]) {
		const merged = normalizeCallUserIdList([...callRingingUserIds, ...userIds]);
		callRingingUserIds = merged;
	}

	function removeRingingUserIds(userIds: string[]) {
		if (callRingingUserIds.length === 0) {
			return;
		}
		const removed = new Set(normalizeCallUserIdList(userIds));
		if (removed.size === 0) {
			return;
		}
		callRingingUserIds = callRingingUserIds.filter((userId) => !removed.has(userId));
	}

	function resolveCallUserName(userId: string) {
		const normalizedUserId = normalizeIdentifier(userId);
		if (!normalizedUserId) {
			return 'User';
		}
		if (normalizedUserId === normalizeIdentifier(currentUserId)) {
			return `${currentUsername} (You)`;
		}
		return (
			currentOnlineMembers.find((member) => normalizeIdentifier(member.id) === normalizedUserId)
				?.name || normalizedUserId
		);
	}

	function buildCallParticipantEntries() {
		const seen = new Set<string>();
		const entries: CallParticipantEntry[] = [];
		const localUserId = normalizeIdentifier(currentUserId);
		if (activeCall && localUserId) {
			entries.push({
				userId: localUserId,
				name: resolveCallUserName(localUserId),
				isLocal: true
			});
			seen.add(localUserId);
		}
		for (const remote of remoteCallStreams) {
			const remoteUserId = normalizeIdentifier(remote.userId);
			if (!remoteUserId || seen.has(remoteUserId) || !streamHasLiveTracks(remote.stream)) {
				continue;
			}
			entries.push({
				userId: remoteUserId,
				name: resolveCallUserName(remoteUserId),
				isLocal: false
			});
			seen.add(remoteUserId);
		}
		return entries.slice(0, CALL_MAX_PARTICIPANTS);
	}

	function trackCallMemberPresence(entries: CallParticipantEntry[], isInCall: boolean) {
		if (!isInCall) {
			if (
				Object.keys(callMemberPresenceByUserId).length > 0 ||
				callParticipantSnapshotIds.length > 0 ||
				showCallMembersPanel
			) {
				callMemberPresenceByUserId = {};
				callParticipantSnapshotIds = [];
				showCallMembersPanel = false;
			}
			return;
		}
		const now = Date.now();
		const nextIds: string[] = [];
		const nextNameById: Record<string, string> = {};
		for (const participant of entries) {
			const userId = normalizeIdentifier(participant.userId);
			if (!userId || nextNameById[userId]) {
				continue;
			}
			nextIds.push(userId);
			nextNameById[userId] = participant.name || 'User';
		}
		const prevIds = new Set(callParticipantSnapshotIds);
		const nextIdSet = new Set(nextIds);
		const nextPresenceByUserId = { ...callMemberPresenceByUserId };
		let changed = false;
		for (const userId of nextIds) {
			const existing = nextPresenceByUserId[userId];
			const nextName = nextNameById[userId];
			if (!existing) {
				nextPresenceByUserId[userId] = {
					name: nextName,
					joinedAt: now,
					leftAt: null
				};
				changed = true;
				continue;
			}
			if (existing.name !== nextName || existing.leftAt !== null) {
				nextPresenceByUserId[userId] = {
					...existing,
					name: nextName,
					leftAt: null
				};
				changed = true;
			}
		}
		for (const userId of prevIds) {
			if (nextIdSet.has(userId)) {
				continue;
			}
			const existing = nextPresenceByUserId[userId];
			if (!existing) {
				nextPresenceByUserId[userId] = {
					name: resolveCallUserName(userId),
					joinedAt: now,
					leftAt: now
				};
				changed = true;
				continue;
			}
			if (existing.leftAt == null) {
				nextPresenceByUserId[userId] = {
					...existing,
					leftAt: now
				};
				changed = true;
			}
		}
		if (changed) {
			callMemberPresenceByUserId = nextPresenceByUserId;
		}
		const snapshotUnchanged =
			nextIds.length === callParticipantSnapshotIds.length &&
			nextIds.every((userId, index) => callParticipantSnapshotIds[index] === userId);
		if (!snapshotUnchanged) {
			callParticipantSnapshotIds = nextIds;
		}
	}

	function toggleCallMembersPanel() {
		showCallMembersPanel = !showCallMembersPanel;
	}

	function formatCallMemberTime(timestampMs: number | null) {
		if (!timestampMs || !Number.isFinite(timestampMs)) {
			return '';
		}
		return new Date(timestampMs).toLocaleTimeString([], {
			hour: '2-digit',
			minute: '2-digit'
		});
	}

	function getCallNameInitials(name: string) {
		const tokens = name.trim().split(/\s+/).filter(Boolean);
		if (tokens.length === 0) {
			return 'U';
		}
		const first = tokens[0]?.[0] ?? '';
		const second = tokens.length > 1 ? (tokens[1]?.[0] ?? '') : '';
		const initials = `${first}${second}`.toUpperCase();
		return initials || 'U';
	}

	function streamHasVideoTrack(stream: MediaStream | null) {
		return Boolean(stream?.getVideoTracks().length);
	}

	function streamHasLiveTracks(stream: MediaStream | null) {
		return Boolean(stream?.getTracks().some((track) => track.readyState !== 'ended'));
	}

	function minimizeActiveCall() {
		if (!activeCall) {
			return;
		}
		showCallMembersPanel = false;
		isCallMinimized = true;
	}

	function restoreMinimizedCall() {
		if (!activeCall) {
			return;
		}
		isCallMinimized = false;
	}

	async function sendCallLogMessage(statusText: string, mode: CallType) {
		if (!roomId || !isMember) {
			return;
		}
		const outgoing: ChatMessage = {
			id: createMessageId(roomId),
			roomId,
			senderId: currentUserId,
			senderName: currentUsername,
			content: statusText.trim() || 'Missed Call',
			type: 'call_log',
			mediaType: mode,
			mediaUrl: '',
			fileName: '',
			createdAt: Date.now(),
			pending: true
		};
		upsertMessage(roomId, outgoing, false);
		sendSocketPayload(toWireMessage(outgoing));
		applyReadProgress(roomId, outgoing.id);
	}

	async function startOutgoingCall(mode: CallType) {
		if (!roomId || !isMember || isRoomExpired) {
			showErrorToast('Join an active room to start a call');
			return;
		}
		ensureWebRTCManager();
		if (!webrtcManager) {
			return;
		}

		try {
			await webrtcManager.startLocalStream(mode === 'video');
			callType = mode;
			activeCall = true;
			callHadRemoteParticipant = false;
			isCallMinimized = false;
			clearIncomingCallState();
			callStartedAtMs = Date.now();
			startCallDurationTicker();
			syncCallStreamsFromManager();
			const localAudioTracks = localCallStream?.getAudioTracks() ?? [];
			if (localAudioTracks.length === 0) {
				throw new Error('Microphone audio track is unavailable for this call.');
			}
			const hasEnabledLocalAudio = localAudioTracks.some((track) => track.enabled);
			if (!hasEnabledLocalAudio) {
				for (const track of localAudioTracks) {
					track.enabled = true;
				}
			}
			isMuted = false;
			isCameraEnabled =
				mode === 'video' &&
				Boolean(localCallStream?.getVideoTracks().some((track) => track.enabled));

			const targets = getCallInviteTargetUserIds().slice(0, webrtcManager.getAvailablePeerSlots());
			setRingingUserIds(targets);
			webrtcManager.inviteToCall(mode, targets);
			for (const targetUserId of targets) {
				await webrtcManager.connectToPeer(targetUserId, mode);
			}
		} catch (error) {
			resetCallUiState();
			showErrorToast(error instanceof Error ? error.message : 'Unable to start call');
		}
	}

	async function acceptIncomingCall() {
		if (!incomingCall) {
			return;
		}
		ensureWebRTCManager();
		if (!webrtcManager) {
			return;
		}

		try {
			await webrtcManager.startLocalStream(incomingCall.callType === 'video');
			callType = incomingCall.callType;
			activeCall = true;
			callHadRemoteParticipant = false;
			isCallMinimized = false;
			clearIncomingCallState();
			callStartedAtMs = Date.now();
			startCallDurationTicker();
			syncCallStreamsFromManager();
			const localAudioTracks = localCallStream?.getAudioTracks() ?? [];
			if (localAudioTracks.length === 0) {
				throw new Error('Microphone audio track is unavailable for this call.');
			}
			const hasEnabledLocalAudio = localAudioTracks.some((track) => track.enabled);
			if (!hasEnabledLocalAudio) {
				for (const track of localAudioTracks) {
					track.enabled = true;
				}
			}
			if (incomingCall.fromUserId) {
				await webrtcManager.connectToPeer(incomingCall.fromUserId, incomingCall.callType);
			}
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Unable to accept call');
		}
	}

	async function declineIncomingCall() {
		if (!incomingCall) {
			return;
		}
		const declinedType = incomingCall.callType;
		const declineTargetUserID = incomingCall.fromUserId;
		clearIncomingCallState();
		sendCallCancelSignal(declineTargetUserID);
		await sendCallLogMessage('Missed Call', declinedType);
	}

	async function endCallWhenParticipantsLeave() {
		clearCallEmptyGraceTimer();
		if (!activeCall || !callHadRemoteParticipant || activeRemoteCallParticipantCount > 0) {
			return;
		}
		sendCallCancelSignal();
		if (!webrtcManager) {
			resetCallUiState();
			return;
		}
		const elapsedFromManager = webrtcManager.endCall();
		const fallbackSeconds = callStartedAtMs
			? Math.max(0, Math.floor((Date.now() - callStartedAtMs) / 1000))
			: 0;
		const elapsedSeconds = Math.max(elapsedFromManager, fallbackSeconds);
		const statusText = elapsedSeconds > 0 ? formatCallDuration(elapsedSeconds) : 'Call ended';
		await sendCallLogMessage(statusText, callType);
		resetCallUiState();
	}

	async function hangUpCall() {
		sendCallCancelSignal();
		if (!webrtcManager) {
			resetCallUiState();
			return;
		}
		const elapsedFromManager = webrtcManager.endCall();
		const fallbackSeconds = callStartedAtMs
			? Math.max(0, Math.floor((Date.now() - callStartedAtMs) / 1000))
			: 0;
		const elapsedSeconds = Math.max(elapsedFromManager, fallbackSeconds);
		const statusText = !callHadRemoteParticipant
			? `Rung for ${formatCallDuration(elapsedSeconds)}`
			: elapsedSeconds > 0
				? formatCallDuration(elapsedSeconds)
				: 'Missed Call';
		await sendCallLogMessage(statusText, callType);
		resetCallUiState();
	}

	function toggleCallMute() {
		if (!webrtcManager) {
			return;
		}
		isMuted = webrtcManager.toggleMute();
	}

	function toggleCallCamera() {
		if (!webrtcManager) {
			return;
		}
		isCameraEnabled = webrtcManager.toggleCamera();
	}

	async function inviteAnotherUserToCall() {
		if (!activeCall || !webrtcManager) {
			return;
		}
		const targets = getCallInviteTargetUserIds().slice(0, webrtcManager.getAvailablePeerSlots());
		if (targets.length === 0) {
			showErrorToast('No additional room members available to invite');
			return;
		}
		addRingingUserIds(targets);
		webrtcManager.inviteToCall(callType, targets);
		for (const targetUserId of targets) {
			await webrtcManager.connectToPeer(targetUserId, callType);
		}
	}

	async function handleCallSignalingEnvelope(
		envelope: SocketEnvelope,
		targetRoomId: string,
		kind: string
	) {
		const normalizedTargetRoomId = normalizeRoomIDValue(targetRoomId || roomId);
		if (!normalizedTargetRoomId || normalizedTargetRoomId !== roomId) {
			return;
		}
		const source = envelope as Record<string, unknown>;
		const payload = resolveEnvelopePayloadRecord(envelope);
		const fromUserID = normalizeIdentifier(
			toStringValue(
				source.fromUserId ??
					source.from_user_id ??
					payload.fromUserId ??
					payload.from_user_id ??
					source.userId ??
					source.user_id
			)
		);

		if (kind === 'call_cancel') {
			if (!fromUserID || fromUserID === normalizeIdentifier(currentUserId)) {
				return;
			}
			removeRingingUserIds([fromUserID]);
			if (incomingCall && normalizeIdentifier(incomingCall.fromUserId) === fromUserID) {
				clearIncomingCallState();
			}
			if (
				activeCall &&
				activeRemoteCallParticipantCount === 0 &&
				(callHadRemoteParticipant || normalizeIdentifier(currentUserId) !== fromUserID)
			) {
				await endCallWhenParticipantsLeave();
			}
			return;
		}
		ensureWebRTCManager();
		if (!webrtcManager) {
			return;
		}
		try {
			await webrtcManager.handleSignaling(envelope as unknown as Record<string, unknown>);
			syncCallStreamsFromManager();
			if (kind === 'webrtc_answer' && fromUserID) {
				removeRingingUserIds([fromUserID]);
			}
			if (kind !== 'call_invite') {
				activeCall = true;
				isCallMinimized = false;
				if (!callStartedAtMs) {
					callStartedAtMs = Date.now();
					startCallDurationTicker();
				}
				clearIncomingCallState();
			}
		} catch (error) {
			clientLog('call-signaling-handle-error', {
				kind,
				error: error instanceof Error ? error.message : String(error)
			});
		}
	}

	function bindVideoStream(node: HTMLVideoElement, stream: MediaStream | null) {
		const attemptPlayback = () => {
			if (!node.srcObject) {
				return;
			}
			void node.play().catch(() => {
				// Playback may require gesture on some browsers.
			});
		};
		const applyStream = (nextStream: MediaStream | null) => {
			node.srcObject = nextStream;
			node.autoplay = true;
			node.playsInline = true;
			const shouldMute = node.hasAttribute('muted');
			node.muted = shouldMute;
			if (!shouldMute) {
				node.volume = 1;
			}
			attemptPlayback();
		};
		const onLoadedMetadata = () => {
			attemptPlayback();
		};
		const onCanPlay = () => {
			attemptPlayback();
		};
		node.addEventListener('loadedmetadata', onLoadedMetadata);
		node.addEventListener('canplay', onCanPlay);
		applyStream(stream);
		return {
			update(nextStream: MediaStream | null) {
				applyStream(nextStream);
			},
			destroy() {
				node.removeEventListener('loadedmetadata', onLoadedMetadata);
				node.removeEventListener('canplay', onCanPlay);
				node.pause();
				node.srcObject = null;
			}
		};
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
			const joinedFeatureFlags = parseRoomFeatureFlags(data as Record<string, unknown>, {
				aiEnabled: true,
				e2eEnabled: false
			});
			ensureRoomThread(normalizedRoomId, joinedName, 'joined');
			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === normalizedRoomId
						? {
								...thread,
								isAdmin: joinedIsAdmin,
								adminCode: joinedIsAdmin ? joinedAdminCode : '',
								requiresPassword: joinedRequiresPassword,
								aiEnabled: joinedFeatureFlags.aiEnabled,
								e2eEnabled: joinedFeatureFlags.e2eEnabled
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
			const roomFeatureFlags = parseRoomFeatureFlags(data as Record<string, unknown>, {
				aiEnabled: roomThreads.find((thread) => thread.id === normalizedRoomID)?.aiEnabled ?? true,
				e2eEnabled:
					roomThreads.find((thread) => thread.id === normalizedRoomID)?.e2eEnabled ?? false
			});
			if (createdAt > 0 || expiresAt > 0) {
				ensureRoomMeta(normalizedRoomID, createdAt, expiresAt);
			}
			roomThreads = sortThreads(
				roomThreads.map((thread) =>
					thread.id === normalizedRoomID
						? {
								...thread,
								aiEnabled: roomFeatureFlags.aiEnabled,
								e2eEnabled: roomFeatureFlags.e2eEnabled
							}
						: thread
				)
			);
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
				const roomFeatureFlags = parseRoomFeatureFlags(roomRecord, {
					aiEnabled: prev?.aiEnabled ?? true,
					e2eEnabled: prev?.e2eEnabled ?? false
				});
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
					requiresPassword: nextRequiresPassword,
					aiEnabled: roomFeatureFlags.aiEnabled,
					e2eEnabled: roomFeatureFlags.e2eEnabled
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

	function workspaceModuleLabel(module: WorkspaceModule) {
		if (module === 'draw') {
			return 'Draw';
		}
		if (module === 'code') {
			return 'Code';
		}
		if (module === 'tasks') {
			return 'Tasks';
		}
		return 'Dashboard';
	}

	function isBoardWorkspaceModule(module: WorkspaceModule): module is BoardWorkspaceModule {
		return BOARD_WORKSPACE_MODULES.includes(module as BoardWorkspaceModule);
	}

	function setVisibleBoardModulesOnOpen(
		module: BoardWorkspaceModule,
		previousSelectedModule: WorkspaceModule | null
	) {
		if (visibleBoardModules.includes(module)) {
			return;
		}
		if (visibleBoardModules.length === 0) {
			visibleBoardModules = [module];
			return;
		}
		if (visibleBoardModules.length === 1) {
			visibleBoardModules = [...visibleBoardModules, module];
			return;
		}
		const selectedBoardModule =
			previousSelectedModule && isBoardWorkspaceModule(previousSelectedModule)
				? previousSelectedModule
				: visibleBoardModules[visibleBoardModules.length - 1];
		const anchorModule =
			selectedBoardModule && selectedBoardModule !== module
				? selectedBoardModule
				: (visibleBoardModules.find((entry) => entry !== module) ?? module);
		const nextPair = [anchorModule, module].filter(
			(entry, index, collection) => collection.indexOf(entry) === index
		);
		visibleBoardModules = nextPair.slice(0, 2);
	}

	function activateWorkspaceModule(module: WorkspaceModule) {
		if (activeWorkspaceModules.includes(module)) {
			return true;
		}
		activeWorkspaceModules = [...activeWorkspaceModules, module];
		return true;
	}

	function deactivateWorkspaceModule(module: WorkspaceModule) {
		if (module !== 'dashboard') {
			activeWorkspaceModules = activeWorkspaceModules.filter((entry) => entry !== module);
		}
		if (isBoardWorkspaceModule(module)) {
			visibleBoardModules = visibleBoardModules.filter((entry) => entry !== module);
			if (selectedWorkspaceModule === module) {
				selectedWorkspaceModule =
					visibleBoardModules.length > 0
						? visibleBoardModules[visibleBoardModules.length - 1]
						: null;
			}
			return;
		}
		if (selectedWorkspaceModule === module) {
			selectedWorkspaceModule = null;
		}
		if (module === 'code') {
			isCanvasOpen = false;
			isCanvasFullscreen = false;
		}
	}

	function openWorkspaceModule(
		module: WorkspaceModule,
		options?: {
			allowSplitCanvas?: boolean;
		}
	) {
		const activated = activateWorkspaceModule(module);
		if (!activated) {
			return false;
		}
		const previousSelectedModule = selectedWorkspaceModule;
		if (isBoardWorkspaceModule(module)) {
			setVisibleBoardModulesOnOpen(module, previousSelectedModule);
			setMessageActionMode('none');
			showRoomSearch = false;
			activeReply = null;
		}
		if (module === 'code') {
			isCanvasOpen = true;
			isCanvasFullscreen = !options?.allowSplitCanvas;
		}
		selectedWorkspaceModule = module;
		return true;
	}

	function onWorkspaceModuleSelect(event: CustomEvent<{ module: WorkspaceModule }>) {
		const module = event.detail.module;
		if (selectedWorkspaceModule === module) {
			deactivateWorkspaceModule(module);
			return;
		}
		openWorkspaceModule(module);
	}

	function onWorkspaceModuleAdd(event: CustomEvent<{ module: WorkspaceModule }>) {
		const module = event.detail.module;
		const opened = openWorkspaceModule(module);
		if (!opened) {
			return;
		}
		showErrorToast(`${workspaceModuleLabel(module)} board activated`);
	}

	function onWorkspaceModuleLimit(event: CustomEvent<{ message: string }>) {
		showErrorToast(event.detail.message || 'All boards are already active for this room.');
	}

	function toggleBoardView() {
		if (isDrawBoardActive) {
			deactivateWorkspaceModule('draw');
			return;
		}
		openWorkspaceModule('draw');
	}

	function toggleTaskBoardView() {
		if (isTaskBoardActive) {
			deactivateWorkspaceModule('tasks');
			return;
		}
		openWorkspaceModule('tasks');
	}

	function toggleDashboardView() {
		if (isDashboardActive) {
			deactivateWorkspaceModule('dashboard');
			return;
		}
		openWorkspaceModule('dashboard');
	}

	function toggleCanvas() {
		if (isCanvasOpen) {
			deactivateWorkspaceModule('code');
			return;
		}
		openWorkspaceModule('code', { allowSplitCanvas: true });
	}

	function toggleCanvasFullscreen() {
		if (selectedWorkspaceModule !== 'code') {
			const opened = openWorkspaceModule('code');
			if (!opened) {
				return;
			}
			return;
		}
		if (!isCanvasOpen) {
			isCanvasOpen = true;
		}
		isCanvasFullscreen = !isCanvasFullscreen;
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

		if (handleTypingSignalPayload(payload)) {
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
		const breakFeatureFlags = parseRoomFeatureFlags(
			{
				aiEnabled: payload.aiEnabled ?? payload.ai_enabled ?? source.aiEnabled ?? source.ai_enabled,
				e2eEnabled:
					payload.e2eEnabled ??
					payload.e2e_enabled ??
					payload.e2eeEnabled ??
					payload.e2ee_enabled ??
					source.e2eEnabled ??
					source.e2e_enabled ??
					source.e2eeEnabled ??
					source.e2ee_enabled
			},
			{
				aiEnabled: roomThreads.find((thread) => thread.id === breakRoomID)?.aiEnabled ?? true,
				e2eEnabled: roomThreads.find((thread) => thread.id === breakRoomID)?.e2eEnabled ?? false
			}
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
					aiEnabled: breakFeatureFlags.aiEnabled,
					e2eEnabled: breakFeatureFlags.e2eEnabled,
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
		if (CALL_SIGNAL_TYPES.has(kind)) {
			await handleCallSignalingEnvelope(envelope, targetRoomId || roomId, kind);
			return;
		}
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
			applyMessageDiscussionState(targetRoomId, payload);
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

		if (kind === 'typing_start' || kind === 'typing_stop') {
			if (handleTypingSignalPayload(envelope)) {
				return;
			}
			if (!targetRoomId) {
				return;
			}
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
			return;
		}

		if (kind === 'message_reaction' && targetRoomId) {
			applyMessageReactions(targetRoomId, payload);
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
		addBeaconMessageToDashboard(message);
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

	function applyMessageReactions(targetRoomId: string, payload: unknown) {
		const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
		if (!normalizedRoomID) {
			return;
		}
		const next = applyMessageReactionsState(messagesByRoom, normalizedRoomID, payload);
		if (!next.changed) {
			return;
		}
		messagesByRoom = next.messagesByRoom;
		queueOfflineCachePersist(normalizedRoomID);
	}

	function applyMessageDiscussionState(targetRoomId: string, payload: unknown) {
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

	async function onCanvasSnippetSend(
		event: CustomEvent<{ snippet: string; message: string; fileName: string }>
	) {
		if (!roomId || !isMember) {
			showErrorToast('Join room before sending messages');
			return;
		}
		const snippet = (event.detail.snippet || '').replace(/\r\n/g, '\n').trim();
		if (!snippet) {
			return;
		}
		const content = (event.detail.message || '').trim();
		if (getUTF8ByteLength(content) > MESSAGE_TEXT_MAX_BYTES) {
			showErrorToast('Message exceeds the maximum length');
			return;
		}
		const fileName = (event.detail.fileName || '').trim();
		const serializedSnippetPayload = JSON.stringify({
			kind: CANVAS_SNIPPET_PAYLOAD_KIND,
			snippet,
			message: content,
			fileName
		});
		if (getUTF8ByteLength(serializedSnippetPayload) > MESSAGE_TEXT_MAX_BYTES) {
			showErrorToast('Snippet payload exceeds the maximum message size');
			return;
		}
		const encryptedSnippetPayload = await encryptMessageContent(serializedSnippetPayload);
		if (getUTF8ByteLength(encryptedSnippetPayload) > MESSAGE_TEXT_MAX_BYTES) {
			showErrorToast('Snippet is too large after encryption. Reduce snippet or message length.');
			return;
		}
		const outgoing: ChatMessage = {
			id: createMessageId(roomId),
			roomId,
			senderId: currentUserId,
			senderName: currentUsername,
			content: serializedSnippetPayload,
			type: 'text',
			mediaUrl: '',
			mediaType: '',
			fileName,
			replyToMessageId: '',
			replyToSnippet: '',
			createdAt: Date.now(),
			pending: true
		};
		upsertMessage(roomId, outgoing, false);
		sendSocketPayload(
			toWireMessage({
				...outgoing,
				content: encryptedSnippetPayload
			})
		);
		applyReadProgress(roomId, outgoing.id);
		sendTypingStop();
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
		const isBeaconMessage = payloadType === 'beacon' && payloadContent !== '';
		const isTaskMessage = payloadType === 'task' && payloadContent !== '';
		const isMediaMessage =
			payloadType !== '' &&
			payloadType !== 'task' &&
			payloadType !== 'beacon' &&
			payloadContent !== '';
		if (!text && !isMediaMessage && !isTaskMessage && !isBeaconMessage) {
			return;
		}
		if (isBeaconMessage && !isActiveRoomEphemeral) {
			showErrorToast('Beacons are available only in ephemeral rooms');
			return;
		}
		if (isTaskMessage && getUTF8ByteLength(payloadContent) > MESSAGE_TEXT_MAX_BYTES) {
			showErrorToast('Task payload is too large');
			return;
		}
		let beaconPayload: ReturnType<typeof parseBeaconMessagePayload> = null;
		if (isBeaconMessage) {
			if (getUTF8ByteLength(payloadContent) > MESSAGE_TEXT_MAX_BYTES) {
				showErrorToast('Beacon payload is too large');
				return;
			}
			beaconPayload = parseBeaconMessagePayload(payloadContent);
			if (!beaconPayload) {
				showErrorToast('Beacon data is invalid');
				return;
			}
			if (getUTF8ByteLength(beaconPayload.text) > MESSAGE_TEXT_MAX_BYTES) {
				showErrorToast('Beacon text exceeds the message limit');
				return;
			}
		}
		if (isTaskMessage) {
			const parsedTaskPayload = parseTaskMessagePayload(payloadContent);
			if (!parsedTaskPayload) {
				showErrorToast('Task data is invalid');
				return;
			}
			if (isActiveRoomEphemeral && !isTaskBoardPayloadWithinLimit(parsedTaskPayload)) {
				showErrorToast(TASK_BOARD_LIMIT_MESSAGE);
				return;
			}
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
		} else if (isBeaconMessage && beaconPayload) {
			const beaconLabel =
				beaconPayload.beaconLabel || formatBeaconTimestamp(beaconPayload.beaconAt);
			outgoing = {
				id: createMessageId(roomId),
				roomId,
				senderId: currentUserId,
				senderName: currentUsername,
				content: payloadContent,
				type: 'text',
				mediaUrl: '',
				mediaType: '',
				fileName: '',
				replyToMessageId,
				replyToSnippet,
				beaconAt: beaconPayload.beaconAt,
				beaconLabel,
				beaconData: {
					kind: 'beacon',
					text: beaconPayload.text
				},
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
		if (isBeaconMessage && addBeaconMessageToDashboard(outgoing)) {
			showErrorToast('Beacon added to dashboard');
		}
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

	async function ensureMessageInDiscussion(message: ChatMessage) {
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
				throw new Error(toStringValue(data.error) || 'Failed to link message discussion');
			}
			applyMessageDiscussionState(roomId, {
				messageId: normalizedMessageID,
				isPinned: toBool(data.isPinned ?? true),
				pinnedBy: toStringValue(data.pinnedBy ?? normalizedUserID),
				pinnedByName: toStringValue(data.pinnedByName ?? normalizedUsername)
			});
			return true;
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to link message discussion');
			return false;
		}
	}

	function persistCurrentRoomDashboardItems(nextItems: RoomDashboardItem[]) {
		if (!roomId) {
			return;
		}
		const normalizedRoomID = normalizeRoomIDValue(roomId);
		const sortedItems = [...nextItems]
			.filter((entry) => normalizeRoomIDValue(entry.roomId) === normalizedRoomID)
			.sort((left, right) => right.pinnedAt - left.pinnedAt);
		roomDashboardItems = sortedItems;
		writeRoomDashboardItems(normalizedRoomID, sortedItems);
		syncDashboardOrganizePreviewWithItems(sortedItems);
	}

	function flattenRoomDashboardOrganizePreview(sections: RoomDashboardOrganizeSections) {
		const seen = new Set<string>();
		const ordered = [...sections.priority, ...sections.pinnedItems, ...sections.expired];
		const deduped: RoomDashboardItem[] = [];
		for (const item of ordered) {
			const itemId = normalizeMessageID(item.id);
			if (!itemId || seen.has(itemId)) {
				continue;
			}
			seen.add(itemId);
			deduped.push(item);
		}
		return deduped;
	}

	function syncDashboardOrganizePreviewWithItems(sourceItems = roomDashboardItems) {
		if (!roomDashboardOrganizePreview || !roomId) {
			return;
		}
		const byId = new Map(
			sourceItems.map((entry) => [normalizeMessageID(entry.id), entry] as const)
		);
		const remap = (entries: RoomDashboardItem[]) =>
			entries
				.map((entry) => byId.get(normalizeMessageID(entry.id)) ?? entry)
				.filter((entry) => normalizeRoomIDValue(entry.roomId) === normalizeRoomIDValue(roomId));
		roomDashboardOrganizePreview = {
			priority: remap(roomDashboardOrganizePreview.priority),
			pinnedItems: remap(roomDashboardOrganizePreview.pinnedItems),
			expired: remap(roomDashboardOrganizePreview.expired)
		};
	}

	function upsertRoomDashboardItem(item: RoomDashboardItem) {
		const normalizedRoomID = normalizeRoomIDValue(roomId);
		if (!normalizedRoomID) {
			return;
		}
		roomDashboardOrganizePreview = null;
		const nextItems = [
			item,
			...roomDashboardItems.filter(
				(entry) => normalizeMessageID(entry.messageId) !== normalizeMessageID(item.messageId)
			)
		];
		persistCurrentRoomDashboardItems(nextItems);
	}

	function toLocalDateInputValue(date: Date) {
		const year = date.getFullYear();
		const month = `${date.getMonth() + 1}`.padStart(2, '0');
		const day = `${date.getDate()}`.padStart(2, '0');
		return `${year}-${month}-${day}`;
	}

	function toLocalTimeInputValue(date: Date) {
		const hours = `${date.getHours()}`.padStart(2, '0');
		const minutes = `${date.getMinutes()}`.padStart(2, '0');
		return `${hours}:${minutes}`;
	}

	function toDashboardDateTimePromptValue(timestamp: number) {
		const date = new Date(timestamp);
		return `${toLocalDateInputValue(date)} ${toLocalTimeInputValue(date)}`;
	}

	function parseDashboardDateTimePromptValue(value: string) {
		const normalized = value.trim().replace(/\s+/, 'T');
		const parsed = parseOptionalTimestamp(normalized);
		if (parsed > 0) {
			return parsed;
		}
		return parseOptionalTimestamp(value.trim());
	}

	function buildManualDashboardItem(input: {
		kind: RoomDashboardItem['kind'];
		messageText: string;
		note?: string;
		beaconAt?: number | null;
		beaconLabel?: string;
		beaconData?: Record<string, unknown> | null;
		taskTitle?: string;
		topic?: string;
	}) {
		const normalizedRoomID = normalizeRoomIDValue(roomId);
		const normalizedUserID = normalizeIdentifier(currentUserId);
		const normalizedUsername = normalizeUsernameValue(currentUsername) || 'User';
		if (!normalizedRoomID || !normalizedUserID) {
			return null;
		}
		const now = Date.now();
		const generatedId = createMessageId(normalizedRoomID);
		const beaconAt = parseOptionalTimestamp(input.beaconAt);
		const topic = toStringValue(input.topic).trim();
		return {
			id: generatedId,
			roomId: normalizedRoomID,
			messageId: generatedId,
			kind: input.kind,
			senderId: normalizedUserID,
			senderName: normalizedUsername,
			pinnedByUserId: normalizedUserID,
			pinnedByName: normalizedUsername,
			originalCreatedAt: now,
			pinnedAt: now,
			messageText: toStringValue(input.messageText).trim(),
			mediaUrl: '',
			mediaType: '',
			fileName: '',
			note: toStringValue(input.note).trim(),
			beaconAt: beaconAt > 0 ? beaconAt : null,
			beaconLabel: toStringValue(input.beaconLabel).trim(),
			beaconData:
				input.beaconData && typeof input.beaconData === 'object' && !Array.isArray(input.beaconData)
					? { ...input.beaconData }
					: null,
			taskTitle: toStringValue(input.taskTitle).trim(),
			topic: topic || undefined
		} satisfies RoomDashboardItem;
	}

	function canAddDashboardItem() {
		if (!roomId || !isMember) {
			showErrorToast('Join the room to add dashboard items.');
			return false;
		}
		if (isRoomExpired) {
			showErrorToast('Room is expired. Dashboard updates are disabled.');
			return false;
		}
		return true;
	}

	function addDashboardNoteItemDirect(noteText: string) {
		const normalizedText = noteText.trim();
		if (!normalizedText) {
			showErrorToast('Note cannot be empty.');
			return;
		}
		const dashboardItem = buildManualDashboardItem({
			kind: 'note',
			messageText: normalizedText
		});
		if (!dashboardItem) {
			showErrorToast('Unable to add note right now.');
			return;
		}
		upsertRoomDashboardItem(dashboardItem);
		showErrorToast('Note added to dashboard.');
	}

	function addDashboardBeaconItemDirect(beaconText: string, beaconAt: number) {
		const normalizedText = beaconText.trim();
		if (!normalizedText) {
			showErrorToast('Beacon text cannot be empty.');
			return;
		}
		const normalizedBeaconAt = parseOptionalTimestamp(beaconAt);
		if (normalizedBeaconAt <= Date.now()) {
			showErrorToast('Choose a future date/time for the beacon.');
			return;
		}
		const beaconLabel = formatBeaconTimestamp(normalizedBeaconAt);
		const dashboardItem = buildManualDashboardItem({
			kind: 'message',
			messageText: normalizedText,
			note: normalizedText,
			beaconAt: normalizedBeaconAt,
			beaconLabel,
			beaconData: {
				kind: 'beacon',
				text: normalizedText
			}
		});
		if (!dashboardItem) {
			showErrorToast('Unable to schedule beacon right now.');
			return;
		}
		upsertRoomDashboardItem(dashboardItem);
		showErrorToast(`Beacon scheduled for ${beaconLabel || formatDateTime(normalizedBeaconAt)}.`);
	}

	function addDashboardTaskItemDirect(taskTitle: string, taskDetail = '') {
		const normalizedTitle = taskTitle.trim();
		const normalizedDetail = taskDetail.trim();
		if (!normalizedTitle) {
			showErrorToast('Task title cannot be empty.');
			return;
		}
		const dashboardItem = buildManualDashboardItem({
			kind: 'task',
			messageText: normalizedDetail,
			taskTitle: normalizedTitle
		});
		if (!dashboardItem) {
			showErrorToast('Unable to create task right now.');
			return;
		}
		upsertRoomDashboardItem(dashboardItem);
		showErrorToast('Task added to dashboard.');
	}

	async function addDashboardNoteItem() {
		const noteRaw = await openPromptDialog({
			title: 'Add Note',
			message: 'Create a note for this room dashboard.',
			initialValue: '',
			placeholder: 'Write note',
			maxLength: 600,
			confirmLabel: 'Add Note',
			cancelLabel: 'Cancel',
			multiline: true
		});
		if (noteRaw === null) {
			return;
		}
		const noteText = noteRaw.trim();
		if (!noteText) {
			showErrorToast('Note cannot be empty.');
			return;
		}
		addDashboardNoteItemDirect(noteText);
	}

	async function addDashboardBeaconItem() {
		const beaconTextRaw = await openPromptDialog({
			title: 'Schedule Beacon',
			message: 'Add beacon text for the dashboard item.',
			initialValue: '',
			placeholder: 'Beacon text',
			maxLength: 500,
			confirmLabel: 'Next',
			cancelLabel: 'Cancel',
			multiline: true
		});
		if (beaconTextRaw === null) {
			return;
		}
		const beaconText = beaconTextRaw.trim();
		if (!beaconText) {
			showErrorToast('Beacon text cannot be empty.');
			return;
		}

		const defaultBeaconAt = Date.now() + 10 * 60 * 1000;
		const scheduleRaw = await openPromptDialog({
			title: 'Schedule Beacon',
			message: 'Set local date and time as YYYY-MM-DD HH:mm.',
			initialValue: toDashboardDateTimePromptValue(defaultBeaconAt),
			placeholder: 'YYYY-MM-DD HH:mm',
			maxLength: 32,
			confirmLabel: 'Schedule',
			cancelLabel: 'Cancel',
			multiline: false
		});
		if (scheduleRaw === null) {
			return;
		}
		const beaconAt = parseDashboardDateTimePromptValue(scheduleRaw);
		addDashboardBeaconItemDirect(beaconText, beaconAt);
	}

	async function addDashboardTaskItem() {
		const taskTitleRaw = await openPromptDialog({
			title: 'Create Task',
			message: 'Add a task title.',
			initialValue: '',
			placeholder: 'Task title',
			maxLength: APP_LIMITS.tasks.maxTitleLength,
			confirmLabel: 'Next',
			cancelLabel: 'Cancel',
			multiline: false
		});
		if (taskTitleRaw === null) {
			return;
		}
		const taskTitle = taskTitleRaw.trim();
		if (!taskTitle) {
			showErrorToast('Task title cannot be empty.');
			return;
		}

		const taskDetailRaw = await openPromptDialog({
			title: 'Create Task',
			message: 'Add task details (optional).',
			initialValue: '',
			placeholder: 'Task details',
			maxLength: 700,
			confirmLabel: 'Create Task',
			emptyConfirmLabel: 'Create without details',
			cancelLabel: 'Cancel',
			multiline: true,
			allowEmptySubmit: true
		});
		if (taskDetailRaw === null) {
			return;
		}
		const taskDetail = taskDetailRaw.trim();
		addDashboardTaskItemDirect(taskTitle, taskDetail);
	}

	function addBeaconMessageToDashboard(message: ChatMessage) {
		const normalizedActiveRoomID = normalizeRoomIDValue(roomId);
		const normalizedMessageRoomID = normalizeRoomIDValue(message.roomId);
		if (!normalizedActiveRoomID || normalizedMessageRoomID !== normalizedActiveRoomID) {
			return false;
		}
		const beaconPayload = parseBeaconMessagePayload(toStringValue(message.content));
		if (!beaconPayload) {
			return false;
		}
		const dashboardItem = buildRoomDashboardItem(message, beaconPayload.text);
		if (!dashboardItem) {
			return false;
		}
		upsertRoomDashboardItem({
			...dashboardItem,
			note: beaconPayload.text,
			beaconAt: dashboardItem.beaconAt || beaconPayload.beaconAt,
			beaconLabel:
				dashboardItem.beaconLabel ||
				beaconPayload.beaconLabel ||
				formatBeaconTimestamp(beaconPayload.beaconAt),
			beaconData: {
				...(dashboardItem.beaconData ?? {}),
				kind: 'beacon',
				text: beaconPayload.text
			}
		});
		return true;
	}

	function buildRoomDashboardItem(message: ChatMessage, note: string) {
		const normalizedRoomID = normalizeRoomIDValue(roomId);
		const normalizedMessageID = normalizeMessageID(message.id);
		const normalizedUserID = normalizeIdentifier(currentUserId);
		const normalizedUsername = normalizeUsernameValue(currentUsername) || 'User';
		if (!normalizedRoomID || !normalizedMessageID || !normalizedUserID) {
			return null;
		}
		const parsedTask = parseTaskMessagePayload(message.content || '');
		const beaconPayload = parseBeaconMessagePayload(toStringValue(message.content));
		const messageSource = message as Record<string, unknown>;
		const beaconDataSource =
			messageSource.beaconData ?? messageSource.beacon_data ?? messageSource.beacon;
		const sourceBeaconData =
			beaconDataSource && typeof beaconDataSource === 'object' && !Array.isArray(beaconDataSource)
				? { ...(beaconDataSource as Record<string, unknown>) }
				: null;
		const beaconData = beaconPayload
			? {
					...(sourceBeaconData ?? {}),
					kind: 'beacon',
					text: beaconPayload.text
				}
			: sourceBeaconData;
		const kind: RoomDashboardItem['kind'] =
			(message.type || '').toLowerCase() === 'task'
				? 'task'
				: (message.type || '').toLowerCase() === 'note'
					? 'note'
					: 'message';
		const beaconAt = resolveDashboardBeaconAt(message, parsedTask);
		const taskTitle = parsedTask?.title?.trim() || '';
		const contentText = beaconPayload ? beaconPayload.text : (message.content || '').trim();
		const normalizedNote = note.trim() || (beaconPayload ? beaconPayload.text : '');
		const beaconLabel =
			(beaconPayload?.beaconLabel || '').trim() || (beaconAt ? formatDateTime(beaconAt) : '');
		return {
			id: createMessageId(normalizedRoomID),
			roomId: normalizedRoomID,
			messageId: normalizedMessageID,
			kind,
			senderId: normalizeIdentifier(message.senderId),
			senderName: normalizeUsernameValue(message.senderName) || 'User',
			pinnedByUserId: normalizedUserID,
			pinnedByName: normalizedUsername,
			originalCreatedAt: parseOptionalTimestamp(message.createdAt) || Date.now(),
			pinnedAt: Date.now(),
			messageText: contentText,
			mediaUrl: toStringValue(message.mediaUrl).trim(),
			mediaType: toStringValue(message.mediaType).trim(),
			fileName: toStringValue(message.fileName).trim(),
			note: normalizedNote,
			beaconAt,
			beaconLabel,
			beaconData,
			taskTitle
		} satisfies RoomDashboardItem;
	}

	async function openDashboardPinPrompt(message: ChatMessage) {
		if (!roomId || !isMember) {
			return;
		}
		const noteValue = await openPromptDialog({
			title: 'Pin to Dashboard',
			message: 'Add a note for this pinned item (optional).',
			initialValue: '',
			placeholder: 'Add a note',
			maxLength: 600,
			confirmLabel: 'Pin',
			emptyConfirmLabel: 'Pin without note',
			cancelLabel: 'Cancel',
			multiline: true,
			allowEmptySubmit: true
		});
		if (noteValue === null) {
			return;
		}
		const dashboardItem = buildRoomDashboardItem(message, noteValue);
		if (!dashboardItem) {
			showErrorToast('Unable to pin item to dashboard');
			return;
		}
		upsertRoomDashboardItem(dashboardItem);
		showErrorToast('Pinned to dashboard');
	}

	function updateRoomDashboardItemNote(itemId: string, note: string) {
		const normalizedItemID = normalizeMessageID(itemId);
		if (!normalizedItemID) {
			return;
		}
		const nextItems = roomDashboardItems.map((item) =>
			normalizeMessageID(item.id) === normalizedItemID
				? {
						...item,
						note: note.trim(),
						pinnedAt: item.pinnedAt || Date.now()
					}
				: item
		);
		persistCurrentRoomDashboardItems(nextItems);
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
		if (isActiveRoomEphemeral && !isTaskBoardPayloadWithinLimit(nextPayload)) {
			showErrorToast(TASK_BOARD_LIMIT_MESSAGE);
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
		if (isActiveRoomEphemeral && !isTaskBoardPayloadWithinLimit(nextPayload)) {
			showErrorToast(TASK_BOARD_LIMIT_MESSAGE);
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
				throw new Error(toStringValue(data.error) || 'Failed to navigate discussions');
			}
			const rawMessage = data.message;
			const parsed = await parseIncomingMessageWithE2EE(rawMessage, roomId);
			if (!parsed) {
				showErrorToast(
					direction === 'previous'
						? 'No previous discussion in this room'
						: 'No next discussion in this room'
				);
				return;
			}
			mergeMessages(roomId, [parsed]);
			openDiscussionForMessage(parsed.id);
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to navigate discussions');
		}
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
		const queued = sendSocketPayload({
			type: 'message_reaction',
			roomId,
			messageId,
			emoji
		});
		if (!queued) {
			showErrorToast('Socket reconnecting. Reaction queued.');
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

	function openPrivateAiChat() {
		if (!activeRoomAllowsAI) {
			showErrorToast('AI is disabled for this room.');
			return;
		}
		showPrivateAiChat = true;
	}

	function closePrivateAiChat() {
		showPrivateAiChat = false;
	}

	function toggleRoomListCollapse() {
		if (!canCollapseRoomList) {
			return;
		}
		isRoomListCollapsed = !isRoomListCollapsed;
		showLeftMenu = false;
	}

	function toggleOnlinePanelCollapse() {
		if (!canCollapseOnlinePanel || isOnlinePanelAutoCollapsed) {
			return;
		}
		isOnlinePanelCollapsed = !isOnlinePanelCollapsed;
		writeSessionChatLayoutPreferences({
			onlinePanelCollapsed: isOnlinePanelCollapsed
		});
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
		const requestedName = normalizeRoomNameValue(action.roomName);
		if (!requestedName) {
			showErrorToast('Room name cannot be empty');
			return;
		}
		const roomMode: RoomMenuMode = action.mode;
		const sessionPreferences = syncSessionRoomPreferencesFromStorage();
		let roomPassword = normalizeRoomPasswordValue($activeRoomPassword);
		let roomAccessPassword = '';
		let shouldPromptForAccessPassword = false;

		if (roomMode === 'create') {
			const enteredRoomPassword = await openOptionalRoomPasswordDialog($activeRoomPassword);
			if (enteredRoomPassword === null) {
				return;
			}
			roomPassword = enteredRoomPassword;
			activeRoomPassword.set(roomPassword);
		}

		try {
			while (true) {
				if (roomMode === 'join' && shouldPromptForAccessPassword) {
					const enteredPassword = await openRoomAccessPasswordDialog(roomAccessPassword);
					if (enteredPassword === null) {
						return;
					}
					roomAccessPassword = enteredPassword;
				}

				const payload: Record<string, unknown> = {
					roomName: requestedName,
					username: currentUsername,
					userId: normalizeIdentifier(currentUserId),
					type: 'ephemeral',
					mode: roomMode
				};
				if (roomMode === 'create') {
					payload.aiEnabled = sessionPreferences.aiEnabled;
					payload.e2eEnabled = sessionPreferences.e2eEnabled;
				}
				if (roomMode === 'join' && roomAccessPassword) {
					payload.roomPassword = roomAccessPassword;
				}

				const res = await fetch(`${API_BASE}/api/rooms/join`, {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify(payload)
				});
				const data = await res.json().catch(() => ({}));
				const requiresPassword = toBool(
					(data as { requiresPassword?: unknown; requires_password?: unknown }).requiresPassword ??
						(data as { requiresPassword?: unknown; requires_password?: unknown }).requires_password
				);
				if (!res.ok) {
					if (roomMode === 'join' && requiresPassword) {
						if (shouldPromptForAccessPassword) {
							showErrorToast('Incorrect room password');
						}
						shouldPromptForAccessPassword = true;
						roomAccessPassword = '';
						continue;
					}
					throw new Error(
						toStringValue((data as { error?: unknown }).error) ||
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
				const nextRequiresPassword = requiresPassword;
				const nextFeatureFlags = parseRoomFeatureFlags(data as Record<string, unknown>, {
					aiEnabled: sessionPreferences.aiEnabled,
					e2eEnabled: sessionPreferences.e2eEnabled
				});
				if (roomMode === 'create') {
					const normalized = writeSessionRoomPreferences(nextFeatureFlags);
					sessionAIEnabled.set(normalized.aiEnabled);
					sessionE2EEnabled.set(normalized.e2eEnabled);
				}

				ensureRoomThread(nextRoomId, nextRoomName, 'joined');
				roomThreads = sortThreads(
					roomThreads.map((thread) =>
						thread.id === nextRoomId
							? {
									...thread,
									isAdmin: nextIsAdmin,
									adminCode: nextIsAdmin ? nextAdminCode : '',
									requiresPassword: nextRequiresPassword,
									aiEnabled: nextFeatureFlags.aiEnabled,
									e2eEnabled: nextFeatureFlags.e2eEnabled
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
				const passwordHash = buildRoomPasswordHash(
					roomMode === 'create' ? roomPassword : $activeRoomPassword
				);
				await goto(`/chat/${encodeURIComponent(nextRoomId)}?${params.toString()}${passwordHash}`);
				return;
			}
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
				const joinedFeatureFlags = parseRoomFeatureFlags(data as Record<string, unknown>, {
					aiEnabled: activeThread.aiEnabled ?? true,
					e2eEnabled: activeThread.e2eEnabled ?? false
				});
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
									requiresPassword: joinedRequiresPassword,
									aiEnabled: joinedFeatureFlags.aiEnabled,
									e2eEnabled: joinedFeatureFlags.e2eEnabled
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

	function toggleDiscussionSelectionMode() {
		const nextMode: MessageActionMode = messageActionMode === 'discussion' ? 'none' : 'discussion';
		setMessageActionMode(nextMode);
	}

	function isDiscussionAnchorMessage(message: ChatMessage) {
		const normalizedType = (message.type || '').toLowerCase();
		const normalizedContent = (message.content || '').trim();
		return (
			normalizeMessageID(message.id) !== '' &&
			normalizedType !== 'deleted' &&
			normalizedContent !== DELETED_MESSAGE_PLACEHOLDER
		);
	}

	function findLatestLocalDiscussionAnchor(targetRoomId: string) {
		const normalizedRoomID = normalizeRoomIDValue(targetRoomId);
		if (!normalizedRoomID) {
			return null as ChatMessage | null;
		}
		const roomMessages = messagesByRoom[normalizedRoomID] ?? [];
		const messageById = new Map<string, ChatMessage>();
		const activityByMessageId = new Map<string, number>();

		for (const message of roomMessages) {
			const messageId = normalizeMessageID(message.id);
			if (!messageId || !isDiscussionAnchorMessage(message)) {
				continue;
			}
			messageById.set(messageId, message);
			if (Boolean(message.isPinned)) {
				activityByMessageId.set(
					messageId,
					Math.max(
						activityByMessageId.get(messageId) ?? 0,
						parseOptionalTimestamp(message.createdAt)
					)
				);
			}
		}

		const cachePrefix = `${normalizedRoomID}::`;
		for (const [cacheKey, comments] of Object.entries(discussionCommentsCacheByTaskKey)) {
			if (!cacheKey.startsWith(cachePrefix)) {
				continue;
			}
			const messageId = normalizeMessageID(cacheKey.slice(cachePrefix.length));
			if (!messageId || !messageById.has(messageId) || !Array.isArray(comments)) {
				continue;
			}
			const latestCommentAt = comments.reduce((latest, comment) => {
				return Math.max(latest, parseOptionalTimestamp(comment.createdAt));
			}, 0);
			activityByMessageId.set(
				messageId,
				Math.max(activityByMessageId.get(messageId) ?? 0, latestCommentAt)
			);
		}

		let latestAnchor: ChatMessage | null = null;
		let latestActivityAt = 0;
		for (const [messageId, activityAt] of activityByMessageId.entries()) {
			const anchor = messageById.get(messageId);
			if (!anchor) {
				continue;
			}
			if (
				activityAt > latestActivityAt ||
				(activityAt === latestActivityAt &&
					parseOptionalTimestamp(anchor.createdAt) >
						parseOptionalTimestamp(latestAnchor?.createdAt))
			) {
				latestAnchor = anchor;
				latestActivityAt = activityAt;
			}
		}
		return latestAnchor;
	}

	async function openLatestDiscussionFromTaskbar() {
		const normalizedRoomID = normalizeRoomIDValue(roomId);
		if (!normalizedRoomID) {
			return;
		}

		if (isMobileView) {
			mobilePane = 'chat';
		}
		setMessageActionMode('none');

		const localAnchor = findLatestLocalDiscussionAnchor(normalizedRoomID);
		if (localAnchor) {
			openDiscussionForMessage(localAnchor.id);
			return;
		}

		const latestCursor = Date.now() + 365 * 24 * 60 * 60 * 1000;
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomID)}/pins/navigate?before=${encodeURIComponent(String(latestCursor))}`
			);
			const body = (await response.json().catch(() => ({}))) as Record<string, unknown>;
			if (!response.ok) {
				throw new Error(toStringValue(body.error) || 'Failed to load latest discussion');
			}
			const parsed = await parseIncomingMessageWithE2EE(body.message, normalizedRoomID);
			if (!parsed || !isDiscussionAnchorMessage(parsed)) {
				showErrorToast('No discussions in this room yet.');
				return;
			}
			mergeMessages(normalizedRoomID, [parsed]);
			openDiscussionForMessage(parsed.id);
		} catch (error) {
			showErrorToast(error instanceof Error ? error.message : 'Failed to open latest discussion');
		}
	}

	function toggleReplySelectionMode() {
		const nextMode: MessageActionMode = messageActionMode === 'reply' ? 'none' : 'reply';
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

		if (messageActionMode === 'discussion') {
			const loweredType = (message.type || '').toLowerCase();
			if (
				loweredType === 'deleted' ||
				(message.content || '').trim() === DELETED_MESSAGE_PLACEHOLDER
			) {
				showErrorToast('Deleted messages cannot be attached to a discussion');
				return;
			}
			const pinned = await ensureMessageInDiscussion(message);
			if (!pinned) {
				return;
			}
			openDiscussionForMessage(message.id);
			setMessageActionMode('none');
			return;
		}

		if (messageActionMode === 'reply') {
			const loweredType = (message.type || '').toLowerCase();
			if (
				loweredType === 'deleted' ||
				(message.content || '').trim() === DELETED_MESSAGE_PLACEHOLDER
			) {
				showErrorToast('Deleted messages cannot be replied to');
				return;
			}
			activeReply = {
				messageId: message.id,
				senderName: normalizeUsernameValue(message.senderName) || 'User',
				content: getMessagePreviewText(message).trim()
			};
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

	async function onMessageContextAction(
		event: CustomEvent<{
			messageId: string;
			action: 'reply' | 'edit' | 'delete' | 'discussion' | 'pin' | 'branch';
		}>
	) {
		if (!roomId || !isMember) {
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
		const action = event.detail.action;
		const loweredType = (message.type || '').toLowerCase();
		const isDeleted =
			loweredType === 'deleted' || (message.content || '').trim() === DELETED_MESSAGE_PLACEHOLDER;
		const isOwner = normalizeIdentifier(message.senderId) === normalizeIdentifier(currentUserId);

		if (isDeleted) {
			showErrorToast(
				action === 'reply'
					? 'Deleted messages cannot be replied to'
					: action === 'discussion'
						? 'Deleted messages cannot be used for discussions'
						: action === 'pin'
							? 'Deleted messages cannot be pinned to dashboard'
							: 'Deleted messages cannot be selected'
			);
			return;
		}

		if (action === 'reply') {
			activeReply = {
				messageId: message.id,
				senderName: normalizeUsernameValue(message.senderName) || 'User',
				content: getMessagePreviewText(message).trim()
			};
			return;
		}

		if (action === 'edit' || action === 'delete') {
			if (!isOwner) {
				showErrorToast('You can only edit/delete your own messages');
				return;
			}
		}

		if (action === 'edit') {
			if (loweredType === 'task') {
				showErrorToast('Use the checklist inside the task card to update tasks');
				return;
			}
			await onEditMessageRequest({
				detail: {
					messageId: message.id,
					content: message.content
				}
			} as CustomEvent<{ messageId: string; content: string }>);
			return;
		}

		if (action === 'delete') {
			await onDeleteMessageRequest({
				detail: {
					messageId: message.id
				}
			} as CustomEvent<{ messageId: string }>);
			return;
		}

		if (action === 'discussion') {
			const pinned = await ensureMessageInDiscussion(message);
			if (!pinned) {
				return;
			}
			openDiscussionForMessage(message.id);
			return;
		}

		if (action === 'pin') {
			await openDashboardPinPrompt(message);
			return;
		}

		if (action === 'branch') {
			await createBreakRoom(message);
		}
	}

	function onDiscussionOpen(event: CustomEvent<{ messageId: string }>) {
		const messageId = normalizeMessageID(event.detail.messageId);
		if (!messageId) {
			return;
		}
		openDiscussionForMessage(messageId);
	}

	function onDashboardItemNoteEdit(event: CustomEvent<{ itemId: string; note: string }>) {
		updateRoomDashboardItemNote(event.detail.itemId, event.detail.note);
	}

	async function onDashboardAddItemRequest(event: CustomEvent<DashboardAddItemRequestDetail>) {
		if (!canAddDashboardItem()) {
			return;
		}
		const detail = event.detail;
		const kind = detail.kind;
		if (kind === 'note') {
			const inlineText = toStringValue(detail.text).trim();
			if (inlineText) {
				addDashboardNoteItemDirect(inlineText);
				return;
			}
			await addDashboardNoteItem();
			return;
		}
		if (kind === 'beacon') {
			const inlineText = toStringValue(detail.text).trim();
			const inlineBeaconAt = parseOptionalTimestamp(detail.beaconAt);
			if (inlineText && inlineBeaconAt > 0) {
				addDashboardBeaconItemDirect(inlineText, inlineBeaconAt);
				return;
			}
			await addDashboardBeaconItem();
			return;
		}
		if (kind === 'task') {
			const inlineTitle = toStringValue(detail.title).trim();
			const inlineDetails = toStringValue(detail.details).trim();
			if (inlineTitle) {
				addDashboardTaskItemDirect(inlineTitle, inlineDetails);
				return;
			}
			await addDashboardTaskItem();
		}
	}

	function onDashboardOrganizePreview(event: CustomEvent<RoomDashboardOrganizeSections>) {
		if (!roomId) {
			return;
		}
		const preview = event.detail;
		roomDashboardOrganizePreview = preview;

		const updatesById = new Map(
			flattenRoomDashboardOrganizePreview(preview).map(
				(entry) => [normalizeMessageID(entry.id), entry] as const
			)
		);
		if (updatesById.size === 0) {
			showErrorToast('AI organize returned no dashboard updates.');
			return;
		}

		const nextItems = [...roomDashboardItems];
		const existingById = new Map(
			nextItems.map((entry, index) => [normalizeMessageID(entry.id), index] as const)
		);
		for (const [entryId, update] of updatesById.entries()) {
			if (!entryId) {
				continue;
			}
			const existingIndex = existingById.get(entryId);
			if (typeof existingIndex === 'number') {
				nextItems[existingIndex] = {
					...nextItems[existingIndex],
					note: update.note || nextItems[existingIndex].note,
					topic: update.topic || '',
					beaconAt: update.beaconAt ?? nextItems[existingIndex].beaconAt,
					beaconLabel: update.beaconLabel || nextItems[existingIndex].beaconLabel,
					messageText: update.messageText || nextItems[existingIndex].messageText,
					taskTitle: update.taskTitle || nextItems[existingIndex].taskTitle
				};
				continue;
			}
			nextItems.push(update);
		}

		persistCurrentRoomDashboardItems(nextItems);
		showErrorToast('AI organize preview ready.');
	}

	function onDashboardOrganizeError(event: CustomEvent<{ message: string }>) {
		showErrorToast(event.detail.message || 'Failed to organize dashboard.');
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
		const sessionPreferences = syncSessionRoomPreferencesFromStorage();
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
					aiEnabled: sessionPreferences.aiEnabled,
					e2eEnabled: sessionPreferences.e2eEnabled,
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
			const breakFeatureFlags = parseRoomFeatureFlags(data as Record<string, unknown>, {
				aiEnabled: sessionPreferences.aiEnabled,
				e2eEnabled: sessionPreferences.e2eEnabled
			});

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
								requiresPassword: breakRequiresPassword,
								aiEnabled: breakFeatureFlags.aiEnabled,
								e2eEnabled: breakFeatureFlags.e2eEnabled
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
	class:sidebar-collapsed={canCollapseRoomList && isRoomListCollapsed}
	class:online-collapsed={isOnlinePanelEffectivelyCollapsed}
>
	<MonochromeRoomBackground seed={roomId || 'chat-room'} />

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
			canCollapse={canCollapseRoomList}
			isCollapsed={canCollapseRoomList && isRoomListCollapsed}
			bind:chatListSearch
			on:select={onSidebarSelect}
			on:jumpOrigin={onJumpToBreakOrigin}
			on:toggleMenu={toggleLeftMenu}
			on:toggleTheme={toggleThemePreference}
			on:toggleCollapse={toggleRoomListCollapse}
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
					isDashboardView={isDashboardActive}
					isBoardView={isDrawBoardActive}
					isTaskBoardView={isTaskBoardActive}
					{isCanvasOpen}
					hasMinimizedCall={activeCall && isCallMinimized}
					minimizedCallLabel={callDurationLabel}
					minimizedCallType={callType}
					remainingLabel={activeRemainingLabel}
					on:showMobileList={showMobileRoomList}
					on:openRoomDetails={openRoomDetails}
					on:startAudioCall={() => void startOutgoingCall('audio')}
					on:startVideoCall={() => void startOutgoingCall('video')}
					on:restoreMinimizedCall={restoreMinimizedCall}
					on:toggleDashboardView={toggleDashboardView}
					on:toggleBoardView={toggleBoardView}
					on:toggleTaskBoardView={toggleTaskBoardView}
					on:toggleCanvas={toggleCanvas}
					on:toggleRoomSearch={toggleRoomSearch}
					on:renameRoom={() => void renameRoom(roomId)}
					on:toggleBreakSelectionMode={toggleBreakSelectionMode}
					on:toggleDiscussionSelectionMode={toggleDiscussionSelectionMode}
					on:toggleReplySelectionMode={toggleReplySelectionMode}
					on:toggleEditSelectionMode={toggleEditSelectionMode}
					on:toggleDeleteSelectionMode={toggleDeleteSelectionMode}
					on:markRead={() => markRoomAsRead(roomId)}
					on:clearLocal={clearCurrentRoomMessages}
					on:leaveRoom={() => void leaveCurrentRoom()}
					on:deleteRoom={() => void deleteCurrentRoomAsAdmin()}
					on:disconnect={() => void disconnectAndWipe()}
				/>

				{#if !isDrawBoardActive && !isDashboardActive && !isTaskBoardActive}
					<ChatStatusBars
						{showTrustedDevicePrompt}
						{isSelectionMode}
						{messageActionMode}
						{typingIndicatorText}
						selectedDeleteCount={selectedDeleteMessageIds.length}
						{showRoomSearch}
						bind:roomMessageSearch
						isDarkMode={$isDarkMode}
						on:trustedChoice={(event) => onTrustedDeviceChoice(event.detail.choice)}
						on:cancelSelection={cancelSelectionMode}
						on:deleteSelected={deleteSelectedMessagesBatch}
					/>
				{/if}

				{#if isRinging && incomingCall && !activeCall}
					<div class="call-inline-request" role="region" aria-label="Incoming call request">
						<div class="call-inline-copy">
							<div class="call-inline-title">
								Incoming {incomingCall.callType === 'video' ? 'Video' : 'Audio'} call
							</div>
							<div class="call-inline-subtitle">
								{resolveCallUserName(incomingCall.fromUserId)} is requesting to connect
							</div>
						</div>
						<div class="call-inline-actions">
							<button
								type="button"
								class="call-inline-btn accept"
								on:click={() => void acceptIncomingCall()}
							>
								Accept
							</button>
							<button
								type="button"
								class="call-inline-btn decline"
								on:click={() => void declineIncomingCall()}
							>
								Decline
							</button>
						</div>
					</div>
				{/if}

				{#if activeCall && callParticipants.length > 0 && !isCallMinimized}
					<div class="call-inline-presence" role="status" aria-label="Call participants">
						<span class="call-inline-presence-label">In call</span>
						<div class="call-inline-presence-list">
							{#each callParticipants as participant (participant.userId)}
								<span class="call-inline-presence-chip">{participant.name}</span>
							{/each}
						</div>
					</div>
				{/if}

				{#if activeCall && callRingingParticipants.length > 0 && !isCallMinimized}
					<div class="call-inline-presence" role="status" aria-label="Ringing participants">
						<span class="call-inline-presence-label">Ringing</span>
						<div class="call-inline-presence-list">
							{#each callRingingParticipants as participant (participant.userId)}
								<span class="call-inline-presence-chip">{participant.name}</span>
							{/each}
						</div>
					</div>
				{/if}

				{#if activeCall && !isCallMinimized}
					<div class="call-active-overlay" role="region" aria-label="Active call">
						<header class="call-active-header">
							<div class="call-active-header-meta">
								<strong>{callType === 'video' ? 'Video Call' : 'Voice Call'}</strong>
								<span>{callDurationLabel}</span>
								<div class="call-e2ee-badge" aria-label="End-to-end encrypted call">
									<svg viewBox="0 0 24 24" aria-hidden="true">
										<path d="M7.5 11V8.6a4.5 4.5 0 1 1 9 0V11"></path>
										<rect x="5.2" y="11" width="13.6" height="9.2" rx="2"></rect>
										<path d="M12 14.6v2.5"></path>
									</svg>
									<span>End-to-end encrypted</span>
								</div>
							</div>
							<div class="call-active-header-actions">
								<button
									type="button"
									class="call-active-count call-members-toggle"
									on:click={toggleCallMembersPanel}
									aria-label="Show call members"
									aria-expanded={showCallMembersPanel}
									title="Call members"
								>
									<svg viewBox="0 0 24 24" aria-hidden="true">
										<path d="M8.4 11.8a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z"></path>
										<path d="M15.8 10.6a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5Z"></path>
										<path d="M3.8 18.4a4.6 4.6 0 0 1 9.2 0"></path>
										<path d="M13 18.4a3.7 3.7 0 0 1 7.4 0"></path>
									</svg>
									<span>{activeCallMemberPresence.length}/{CALL_MAX_PARTICIPANTS}</span>
								</button>
								<button
									type="button"
									class="call-minimize-btn"
									on:click={minimizeActiveCall}
									aria-label="Minimize call"
									title="Minimize call"
								>
									<svg viewBox="0 0 24 24" aria-hidden="true">
										<path d="M6 12h12"></path>
									</svg>
								</button>
							</div>
						</header>
						{#if showCallMembersPanel}
							<div class="call-members-panel" role="dialog" aria-label="Call members panel">
								<section class="call-members-section">
									<div class="call-members-heading">
										In call ({activeCallMemberPresence.length})
									</div>
									{#if activeCallMemberPresence.length === 0}
										<div class="call-members-empty">No one is connected yet.</div>
									{:else}
										<ul class="call-members-list">
											{#each activeCallMemberPresence as member (member.userId)}
												<li class="call-members-item">
													<span class="call-participant-avatar"
														>{getCallNameInitials(member.name)}</span
													>
													<div class="call-members-item-copy">
														<span class="call-members-name">{member.name}</span>
														<span class="call-members-meta"
															>Joined {formatCallMemberTime(member.joinedAt)}</span
														>
													</div>
												</li>
											{/each}
										</ul>
									{/if}
								</section>
								<section class="call-members-section">
									<div class="call-members-heading">
										Joined and left ({departedCallMemberPresence.length})
									</div>
									{#if departedCallMemberPresence.length === 0}
										<div class="call-members-empty">No one has left this call yet.</div>
									{:else}
										<ul class="call-members-list">
											{#each departedCallMemberPresence as member (member.userId)}
												<li class="call-members-item">
													<span class="call-participant-avatar"
														>{getCallNameInitials(member.name)}</span
													>
													<div class="call-members-item-copy">
														<span class="call-members-name">{member.name}</span>
														<span class="call-members-meta"
															>Left {formatCallMemberTime(member.leftAt)}</span
														>
													</div>
												</li>
											{/each}
										</ul>
									{/if}
								</section>
							</div>
						{/if}
						<div class="call-video-grid">
							{#if localCallStream}
								<article
									class="call-video-tile local"
									class:audio-only={!streamHasVideoTrack(localCallStream)}
								>
									<video autoplay playsinline muted use:bindVideoStream={localCallStream}></video>
									{#if !streamHasVideoTrack(localCallStream)}
										<div class="call-audio-fallback" aria-hidden="true">
											<svg viewBox="0 0 24 24">
												<path d="M11 5 6 9H3v6h3l5 4V5Z"></path>
												<path d="M15 9a4 4 0 0 1 0 6"></path>
												<path d="M18 6a8 8 0 0 1 0 12"></path>
											</svg>
										</div>
									{/if}
									<span class="call-video-label">{resolveCallUserName(currentUserId)}</span>
								</article>
							{/if}
							{#each remoteCallStreams as remote (remote.userId)}
								<article
									class="call-video-tile"
									class:audio-only={!streamHasVideoTrack(remote.stream)}
								>
									<video autoplay playsinline use:bindVideoStream={remote.stream}></video>
									{#if !streamHasVideoTrack(remote.stream)}
										<div class="call-audio-fallback" aria-hidden="true">
											<svg viewBox="0 0 24 24">
												<path d="M11 5 6 9H3v6h3l5 4V5Z"></path>
												<path d="M15 9a4 4 0 0 1 0 6"></path>
												<path d="M18 6a8 8 0 0 1 0 12"></path>
											</svg>
										</div>
									{/if}
									<span class="call-video-label">{resolveCallUserName(remote.userId)}</span>
								</article>
							{/each}
						</div>
						<div class="call-active-controls">
							<button
								type="button"
								class="call-control-btn"
								class:active={isMuted}
								on:click={toggleCallMute}
								aria-label={isMuted ? 'Unmute microphone' : 'Mute microphone'}
								title={isMuted ? 'Unmute microphone' : 'Mute microphone'}
							>
								<svg viewBox="0 0 24 24" aria-hidden="true">
									{#if isMuted}
										<path d="m4.5 4.5 15 15"></path>
									{/if}
									<path
										d="M12 14.5a3.2 3.2 0 0 0 3.2-3.2V7.2A3.2 3.2 0 0 0 12 4a3.2 3.2 0 0 0-3.2 3.2v4.1a3.2 3.2 0 0 0 3.2 3.2Z"
									></path>
									<path d="M6.5 10.8a5.5 5.5 0 0 0 11 0M12 16.3V20M9.3 20h5.4"></path>
								</svg>
							</button>
							<button
								type="button"
								class="call-control-btn"
								class:active={!isCameraEnabled}
								on:click={toggleCallCamera}
								aria-label={isCameraEnabled ? 'Turn camera off' : 'Turn camera on'}
								title={isCameraEnabled ? 'Turn camera off' : 'Turn camera on'}
							>
								<svg viewBox="0 0 24 24" aria-hidden="true">
									{#if !isCameraEnabled}
										<path d="m4.5 4.5 15 15"></path>
									{/if}
									<rect x="3.5" y="6.5" width="12" height="11" rx="2"></rect>
									<path d="M15.5 10 21 7v10l-5.5-3"></path>
								</svg>
							</button>
							<button
								type="button"
								class="call-control-btn"
								on:click={() => void inviteAnotherUserToCall()}
								aria-label="Add user to call"
								title="Invite user"
							>
								<svg viewBox="0 0 24 24" aria-hidden="true">
									<path d="M12 12a3.2 3.2 0 1 0 0-6.4 3.2 3.2 0 0 0 0 6.4Z"></path>
									<path d="M6 19a6 6 0 0 1 12 0"></path>
									<path d="M19 8v4M17 10h4"></path>
								</svg>
							</button>
							<button
								type="button"
								class="call-control-btn call-control-btn-chat"
								on:click={minimizeActiveCall}
								aria-label="Open chat"
								title="Open chat"
							>
								<svg viewBox="0 0 24 24" aria-hidden="true">
									<path
										d="M4 5.8a2.8 2.8 0 0 1 2.8-2.8h10.4A2.8 2.8 0 0 1 20 5.8v7.4a2.8 2.8 0 0 1-2.8 2.8h-6l-4.2 3v-3H6.8A2.8 2.8 0 0 1 4 13.2V5.8Z"
									></path>
								</svg>
							</button>
							<button
								type="button"
								class="call-control-btn hangup"
								on:click={() => void hangUpCall()}
								aria-label="Hang up call"
								title="Hang up call"
							>
								<svg viewBox="0 0 24 24" aria-hidden="true">
									<path
										d="M6.6 10.8c1.6 3.1 3.9 5.5 7 7l2.3-2.3a1 1 0 0 1 1.1-.24c1.2.4 2.5.6 3.8.6a1 1 0 0 1 1 1V21a1 1 0 0 1-1 1C11 22 2 13 2 2a1 1 0 0 1 1-1h4.1a1 1 0 0 1 1 1c0 1.3.2 2.6.6 3.8a1 1 0 0 1-.24 1.1L6.6 10.8Z"
									></path>
								</svg>
							</button>
						</div>
					</div>
				{/if}

				<div class="chat-window-shell" class:is-expired={isRoomExpired}>
					{#if visibleBoardModules.length > 0}
						<div class="board-view-grid" class:is-split={visibleBoardModules.length > 1}>
							{#each visibleBoardModules as boardModule (boardModule)}
								<section class="board-view-pane" class:is-split={visibleBoardModules.length > 1}>
									{#if boardModule === 'draw'}
										<Board
											{roomId}
											messages={currentMessages}
											isDarkMode={$isDarkMode}
											canEdit={isMember && !isRoomExpired}
											{canModerateBoard}
											{currentUserId}
											{currentUsername}
											isEphemeralRoom={isActiveRoomEphemeral}
											on:close={() => deactivateWorkspaceModule('draw')}
											on:toastError={(event) => showErrorToast(event.detail.message)}
										/>
									{:else if boardModule === 'dashboard'}
										<RoomDashboard
											{roomId}
											items={roomDashboardItems}
											isDarkMode={$isDarkMode}
											{currentUserId}
											organizePreview={roomDashboardOrganizePreview}
											on:close={() => deactivateWorkspaceModule('dashboard')}
											on:editNote={onDashboardItemNoteEdit}
											on:addItemRequest={(event) => void onDashboardAddItemRequest(event)}
											on:aiOrganizePreview={onDashboardOrganizePreview}
											on:aiOrganizeError={onDashboardOrganizeError}
										/>
									{:else}
										<ProjectWorkspace
											{roomId}
											canEdit={isMember && !isRoomExpired}
											on:close={() => deactivateWorkspaceModule('tasks')}
										/>
									{/if}
								</section>
							{/each}
						</div>
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
							on:openDiscussion={onDiscussionOpen}
							on:reply={onReplyRequest}
							on:toggleReaction={onMessageReactionToggle}
							on:messageContextAction={onMessageContextAction}
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

				{#if !isDrawBoardActive && !isDashboardActive && !isTaskBoardActive}
					<div class="composer-typing-slot" role="status" aria-live="polite" aria-atomic="true">
						{#if hasTypingUsers}
							{#key `${roomId}:${typingIndicatorText}`}
								<div class="composer-typing-card is-visible">
									<div class="composer-typing-names">{typingNamesPreview}</div>
									<div class="composer-typing-status">{typingIndicatorText}</div>
								</div>
							{/key}
						{:else}
							<div class="composer-typing-placeholder" aria-hidden="true"></div>
						{/if}
					</div>
				{/if}
				{#if isMember && !isDrawBoardActive && !isDashboardActive && !isTaskBoardActive}
					<ChatComposer
						bind:draftMessage
						bind:attachedFile
						{roomId}
						disabled={isRoomExpired}
						isEphemeralRoom={isActiveRoomEphemeral}
						{activeReply}
						isDarkMode={$isDarkMode}
						{currentUsername}
						aiEnabled={activeRoomAllowsAI}
						mentionCandidates={currentOnlineMembers.map((member) => member.name)}
						messageLimit={MESSAGE_TEXT_MAX_BYTES}
						on:send={(event) => void sendMessage(event.detail)}
						on:typing={onComposerTyping}
						on:attach={handleComposerAttach}
						on:removeAttachment={handleComposerRemoveAttachment}
						on:openPrivateAi={openPrivateAiChat}
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
					<CodeCanvas
						{roomId}
						currentUser={canvasUser}
						isEphemeralRoom={isActiveRoomEphemeral}
						on:sendSnippet={onCanvasSnippetSend}
					/>
				</div>
			</section>
		{/if}
	</div>

	<div class="online-pane">
		<OnlinePanel
			members={currentOnlineMembers}
			isDarkMode={$isDarkMode}
			canCollapse={canCollapseOnlinePanel && !isOnlinePanelAutoCollapsed}
			isCollapsed={isOnlinePanelEffectivelyCollapsed}
			on:toggleCollapse={toggleOnlinePanelCollapse}
		/>
	</div>
</section>

{#if !isCompactNavViewport}
	<FloatingActivityBox
		activeModules={activeWorkspaceModules}
		selectedModule={selectedWorkspaceModule}
		addableModules={addableWorkspaceModules}
		isDarkMode={$isDarkMode}
		on:selectModule={onWorkspaceModuleSelect}
		on:addModule={onWorkspaceModuleAdd}
		on:limitReached={onWorkspaceModuleLimit}
		on:toggleTheme={toggleThemePreference}
	/>
{/if}

<PrivateAiChat
	open={showPrivateAiChat}
	isDarkMode={$isDarkMode}
	{roomId}
	{currentUserId}
	{currentUsername}
	on:close={closePrivateAiChat}
/>

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
