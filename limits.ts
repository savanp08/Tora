// Centralized app limits.
// Update values here to tune both frontend and backend behavior.
export type AppLimits = {
	ai: {
		// Sliding window size (seconds) for private AI request throttling.
		windowSeconds: number;
		// Max private AI requests per user within `windowSeconds`.
		perUser: number;
		// Max private AI requests per room within `windowSeconds`.
		perRoom: number;
		// Max private AI requests per IP within `windowSeconds`.
		perIP: number;
		// Max private AI requests per device id within `windowSeconds`.
		perDeviceId: number;
		// Private AI cap per user in a rolling 1-hour window.
		privateUserPerHour: number;
		// Private AI cap per user in a rolling 24-hour window.
		privateUserPerDay: number;
		// Private AI cap per user in a rolling 7-day window.
		privateUserPerWeek: number;
		// Private AI cap per user in a rolling 30-day window.
		privateUserPerMonth: number;
		// Private AI cap per room in a rolling 1-hour window.
		privateRoomPerHour: number;
		// Private AI cap per room in a rolling 24-hour window.
		privateRoomPerDay: number;
		// Private AI cap per room in a rolling 7-day window.
		privateRoomPerWeek: number;
		// Private AI cap per room in a rolling 30-day window.
		privateRoomPerMonth: number;
		// Private AI cap per IP in a rolling 1-hour window.
		privateIpPerHour: number;
		// Private AI cap per IP in a rolling 24-hour window.
		privateIpPerDay: number;
		// Private AI cap per IP in a rolling 7-day window.
		privateIpPerWeek: number;
		// Private AI cap per IP in a rolling 30-day window.
		privateIpPerMonth: number;
		// Private AI cap per device in a rolling 1-hour window.
		privateDevicePerHour: number;
		// Private AI cap per device in a rolling 24-hour window.
		privateDevicePerDay: number;
		// Private AI cap per device in a rolling 7-day window.
		privateDevicePerWeek: number;
		// Private AI cap per device in a rolling 30-day window.
		privateDevicePerMonth: number;
		// Number of recent messages kept in AI context windows.
		contextMessageLimit: number;
		// Max request body size (bytes) accepted by AI organize endpoint.
		organizeMaxRequestBytes: number;
		// Max dashboard items accepted in a single AI organize request.
		organizeMaxItems: number;
		// Max note length the AI organize pipeline accepts/returns.
		organizeNoteMaxLength: number;
		// Max topic label length the AI organize pipeline accepts/returns.
		organizeTopicMaxLength: number;
		// Max text/content length per normalized item for AI organize.
		organizeTextMaxLength: number;
		// Backend timeout (ms) for one AI organize request.
		organizeRequestTimeoutMs: number;
	};
	chat: {
		// Max text payload size (bytes) for a chat message body.
		messageTextMaxBytes: number;
		// Allowed future skew (ms) for remote typing timestamps.
		remoteTypingMaxFutureMs: number;
		// Maximum depth for threaded discussion replies.
		discussionMaxReplyDepth: number;
		// Page size when fetching discussion replies.
		discussionRepliesPageSize: number;
		// Max attached notes per discussion/pin record.
		discussionMaxNotes: number;
		// Max chars for compact discussion preview text.
		discussionCommentPreviewMaxLength: number;
		// Max lines rendered in compact discussion preview.
		discussionCommentPreviewMaxLines: number;
		// Threshold where long message body is collapsed in UI.
		collapsedMessageLength: number;
		// Max code lines shown in collapsed snippet preview.
		collapsedSnippetCodeMaxLines: number;
		// Max code chars shown in collapsed snippet preview.
		collapsedSnippetCodeMaxChars: number;
		// Max message lines shown in collapsed snippet preview.
		collapsedSnippetMessageMaxLines: number;
		// Max message chars shown in collapsed snippet preview.
		collapsedSnippetMessageMaxChars: number;
	};
	calls: {
		// Maximum simultaneous participants in one call room.
		maxParticipants: number;
		// Incoming call auto-timeout (ms) if unanswered.
		incomingTimeoutMs: number;
		// Grace period (ms) before ending an empty call.
		emptyGraceMs: number;
	};
	board: {
		// Absolute room board storage cap (bytes) for persisted rooms.
		maxStorageBytes: number;
		// Stricter board storage cap (bytes) for ephemeral rooms.
		ephemeralMaxStorageBytes: number;
		// Minimum gap between board memory-limit toasts.
		drawLimitToastCooldownMs: number;
		// Max number of board history entries kept client-side.
		historyLimit: number;
		// Max queued local board actions before backpressure.
		localActionLimit: number;
		// Cursor broadcast throttle (ms) for board presence.
		cursorMoveThrottleMs: number;
		// When to treat a remote cursor as stale (ms).
		remoteCursorStaleMs: number;
		// Max board zoom level.
		maxZoom: number;
		// Max preview height (px) for image elements.
		maxImagePreviewHeight: number;
		// Max preview height (px) for video elements.
		maxVideoPreviewHeight: number;
	};
	codeCanvas: {
		// Max simultaneously open Monaco editors/files.
		maxFileEditors: number;
		// Yjs/code-canvas document memory cap (bytes).
		memoryLimitBytes: number;
		// Minimum gap between code-canvas memory-limit alerts.
		yDocLimitAlertCooldownMs: number;
	};
	tasks: {
		// Max task title characters.
		maxTitleLength: number;
		// Max task body/detail characters.
		maxTaskTextLength: number;
		// Max task items allowed on task board.
		maxItems: number;
		// Max task board serialized size (bytes).
		boardMaxBytes: number;
	};
	composer: {
		// Max visual lines for composer before it starts scrolling.
		maxVisibleLines: number;
		// Max media-search results for quick insert.
		klipySearchLimit: number;
		// Min ad/sticker width.
		klipyAdMinWidth: number;
		// Max ad/sticker width.
		klipyAdMaxWidth: number;
		// Min ad/sticker height.
		klipyAdMinHeight: number;
		// Max ad/sticker height.
		klipyAdMaxHeight: number;
	};
	media: {
		// Max upload size (bytes) for video payloads.
		maxVideoBytes: number;
		// Target max compressed image size in MB.
		imageCompressionMaxSizeMB: number;
		// Max long-edge dimension for compressed images.
		imageCompressionMaxWidthOrHeight: number;
	};
	workspace: {
		// Max active non-dashboard modules at once.
		maxActiveNonDashboardModules: number;
	};
	ws: {
		// Max queued websocket messages per client before dropping.
		maxQueuedMessages: number;
		// Max raw websocket frame/message size (bytes).
		maxMessageSize: number;
		// Max text characters accepted in ws text payloads.
		maxTextChars: number;
		// Max media URL length accepted over ws.
		maxMediaURLLength: number;
		// Max uploaded/displayed filename length.
		maxFileNameLength: number;
		// Global websocket connection cap across the server.
		maxGlobalConnections: number;
		// Max websocket connections per source IP.
		maxConnectionsPerIP: number;
		// Max websocket connections per room.
		maxConnectionsPerRoom: number;
		// WebSocket connect attempts per user in a rolling 1-hour window.
		connectUserPerHour: number;
		// WebSocket connect attempts per user in a rolling 24-hour window.
		connectUserPerDay: number;
		// WebSocket connect attempts per user in a rolling 7-day window.
		connectUserPerWeek: number;
		// WebSocket connect attempts per user in a rolling 30-day window.
		connectUserPerMonth: number;
		// WebSocket connect attempts per IP in a rolling 1-hour window.
		connectIpPerHour: number;
		// WebSocket connect attempts per IP in a rolling 24-hour window.
		connectIpPerDay: number;
		// WebSocket connect attempts per IP in a rolling 7-day window.
		connectIpPerWeek: number;
		// WebSocket connect attempts per IP in a rolling 30-day window.
		connectIpPerMonth: number;
		// WebSocket connect attempts per device in a rolling 1-hour window.
		connectDevicePerHour: number;
		// WebSocket connect attempts per device in a rolling 24-hour window.
		connectDevicePerDay: number;
		// WebSocket connect attempts per device in a rolling 7-day window.
		connectDevicePerWeek: number;
		// WebSocket connect attempts per device in a rolling 30-day window.
		connectDevicePerMonth: number;
	};
	upload: {
		// General upload file size cap (bytes).
		maxFileBytes: number;
		// Image upload size cap (bytes).
		maxImageBytes: number;
		// Multipart request size cap (bytes).
		maxMultipartBytes: number;
		// Max length for text form-field inputs in multipart requests.
		maxFormFieldLength: number;
		// Generate-upload-url calls per user in a rolling 1-hour window.
		generateUrlUserPerHour: number;
		// Generate-upload-url calls per user in a rolling 24-hour window.
		generateUrlUserPerDay: number;
		// Generate-upload-url calls per user in a rolling 7-day window.
		generateUrlUserPerWeek: number;
		// Generate-upload-url calls per user in a rolling 30-day window.
		generateUrlUserPerMonth: number;
		// Generate-upload-url calls per IP in a rolling 1-hour window.
		generateUrlIpPerHour: number;
		// Generate-upload-url calls per IP in a rolling 24-hour window.
		generateUrlIpPerDay: number;
		// Generate-upload-url calls per IP in a rolling 7-day window.
		generateUrlIpPerWeek: number;
		// Generate-upload-url calls per IP in a rolling 30-day window.
		generateUrlIpPerMonth: number;
		// Generate-upload-url calls per device in a rolling 1-hour window.
		generateUrlDevicePerHour: number;
		// Generate-upload-url calls per device in a rolling 24-hour window.
		generateUrlDevicePerDay: number;
		// Generate-upload-url calls per device in a rolling 7-day window.
		generateUrlDevicePerWeek: number;
		// Generate-upload-url calls per device in a rolling 30-day window.
		generateUrlDevicePerMonth: number;
		// Proxy upload calls per user in a rolling 1-hour window.
		proxyUserPerHour: number;
		// Proxy upload calls per user in a rolling 24-hour window.
		proxyUserPerDay: number;
		// Proxy upload calls per user in a rolling 7-day window.
		proxyUserPerWeek: number;
		// Proxy upload calls per user in a rolling 30-day window.
		proxyUserPerMonth: number;
		// Proxy upload calls per IP in a rolling 1-hour window.
		proxyIpPerHour: number;
		// Proxy upload calls per IP in a rolling 24-hour window.
		proxyIpPerDay: number;
		// Proxy upload calls per IP in a rolling 7-day window.
		proxyIpPerWeek: number;
		// Proxy upload calls per IP in a rolling 30-day window.
		proxyIpPerMonth: number;
		// Proxy upload calls per device in a rolling 1-hour window.
		proxyDevicePerHour: number;
		// Proxy upload calls per device in a rolling 24-hour window.
		proxyDevicePerDay: number;
		// Proxy upload calls per device in a rolling 7-day window.
		proxyDevicePerWeek: number;
		// Proxy upload calls per device in a rolling 30-day window.
		proxyDevicePerMonth: number;
	};
	room: {
		// Required digit count for numeric room codes.
		codeDigits: number;
		// Max room name length.
		nameMaxLength: number;
		// Max room password length.
		passwordMaxLength: number;
		// Max descendant breakout rooms from a root room.
		maxDescendants: number;
		// Max room lifetime extension window (hours).
		maxDurationHours: number;
	};
	users: {
		// Max members that can join one room.
		maxRoomMembers: number;
	};
};

export const LIMITS: AppLimits = {
	ai: {
		// 24 hour throttle window.
		windowSeconds: 86400,
		// Per-user private AI call cap in the window.
		perUser: 120,
		// Per-room private AI call cap in the window.
		perRoom: 500,
		// Per-IP private AI call cap in the window.
		perIP: 220,
		// Per-device private AI call cap in the window.
		perDeviceId: 180,
		// Private AI per-user hourly cap.
		privateUserPerHour: 24,
		// Private AI per-user daily cap.
		privateUserPerDay: 120,
		// Private AI per-user weekly cap.
		privateUserPerWeek: 600,
		// Private AI per-user monthly cap.
		privateUserPerMonth: 1800,
		// Private AI per-room hourly cap.
		privateRoomPerHour: 80,
		// Private AI per-room daily cap.
		privateRoomPerDay: 500,
		// Private AI per-room weekly cap.
		privateRoomPerWeek: 2500,
		// Private AI per-room monthly cap.
		privateRoomPerMonth: 7000,
		// Private AI per-IP hourly cap.
		privateIpPerHour: 40,
		// Private AI per-IP daily cap.
		privateIpPerDay: 220,
		// Private AI per-IP weekly cap.
		privateIpPerWeek: 1000,
		// Private AI per-IP monthly cap.
		privateIpPerMonth: 3000,
		// Private AI per-device hourly cap.
		privateDevicePerHour: 30,
		// Private AI per-device daily cap.
		privateDevicePerDay: 180,
		// Private AI per-device weekly cap.
		privateDevicePerWeek: 800,
		// Private AI per-device monthly cap.
		privateDevicePerMonth: 2400,
		// Keep last 50 messages in AI context.
		contextMessageLimit: 50,
		// AI organize request body cap = 2MB.
		organizeMaxRequestBytes: 2097152,
		// AI organize request max item count.
		organizeMaxItems: 500,
		// AI organize max note length.
		organizeNoteMaxLength: 1200,
		// AI organize max topic length.
		organizeTopicMaxLength: 180,
		// AI organize max text/content length.
		organizeTextMaxLength: 3000,
		// AI organize backend timeout = 30s.
		organizeRequestTimeoutMs: 30000
	},
	chat: {
		// Max text payload size per message.
		messageTextMaxBytes: 4000,
		// Allow 2 min future skew on typing events.
		remoteTypingMaxFutureMs: 120000,
		// Max nested discussion reply depth.
		discussionMaxReplyDepth: 4,
		// Paginate discussion replies by 5.
		discussionRepliesPageSize: 5,
		// Max notes attached to one discussion item.
		discussionMaxNotes: 5,
		// Compact preview char cap.
		discussionCommentPreviewMaxLength: 360,
		// Compact preview line cap.
		discussionCommentPreviewMaxLines: 7,
		// Collapse message body after this length.
		collapsedMessageLength: 500,
		// Collapsed code snippet preview line cap.
		collapsedSnippetCodeMaxLines: 20,
		// Collapsed code snippet preview char cap.
		collapsedSnippetCodeMaxChars: 1400,
		// Collapsed message snippet preview line cap.
		collapsedSnippetMessageMaxLines: 8,
		// Collapsed message snippet preview char cap.
		collapsedSnippetMessageMaxChars: 560
	},
	calls: {
		// Audio/video room participant cap.
		maxParticipants: 5,
		// Ring timeout.
		incomingTimeoutMs: 30000,
		// End call shortly after everyone leaves.
		emptyGraceMs: 4500
	},
	board: {
		// Persistent board storage cap.
		maxStorageBytes: 10485760,
		// Ephemeral board storage cap.
		ephemeralMaxStorageBytes: 1048576,
		// Avoid toast spam for draw memory limit warnings.
		drawLimitToastCooldownMs: 2500,
		// Board undo/redo history cap.
		historyLimit: 80,
		// Local action queue cap.
		localActionLimit: 180,
		// Throttle board cursor messages.
		cursorMoveThrottleMs: 1000,
		// Mark remote cursor stale after inactivity.
		remoteCursorStaleMs: 8000,
		// Max board zoom.
		maxZoom: 4,
		// Image preview max height.
		maxImagePreviewHeight: 460,
		// Video preview max height.
		maxVideoPreviewHeight: 360
	},
	codeCanvas: {
		// Max open editors/files at once.
		maxFileEditors: 3,
		// Yjs document cap for code canvas.
		memoryLimitBytes: 2097152,
		// Avoid alert spam when cap is hit.
		yDocLimitAlertCooldownMs: 2500
	},
	tasks: {
		// Task title length cap.
		maxTitleLength: 120,
		// Task description length cap.
		maxTaskTextLength: 280,
		// Total tasks cap.
		maxItems: 200,
		// Serialized task-board payload cap.
		boardMaxBytes: 1048576
	},
	composer: {
		// Input box visual height cap in lines.
		maxVisibleLines: 3,
		// Search result cap in media picker.
		klipySearchLimit: 24,
		// Sticker/ad min width.
		klipyAdMinWidth: 50,
		// Sticker/ad max width.
		klipyAdMaxWidth: 150,
		// Sticker/ad min height.
		klipyAdMinHeight: 50,
		// Sticker/ad max height.
		klipyAdMaxHeight: 150
	},
	media: {
		// Max direct video upload size.
		maxVideoBytes: 52428800,
		// Target compressed image size.
		imageCompressionMaxSizeMB: 1,
		// Max long edge after compression.
		imageCompressionMaxWidthOrHeight: 1920
	},
	workspace: {
		// Keep max 2 active boards besides dashboard.
		maxActiveNonDashboardModules: 2
	},
	ws: {
		// Backpressure queue cap per ws client.
		maxQueuedMessages: 500,
		// Max frame/message size accepted on ws.
		maxMessageSize: 65536,
		// Max text chars in ws message content.
		maxTextChars: 4000,
		// Max media URL chars in ws payload.
		maxMediaURLLength: 4096,
		// Max filename chars in ws payload.
		maxFileNameLength: 180,
		// Global connection cap.
		maxGlobalConnections: 60000,
		// Per-IP connection cap.
		maxConnectionsPerIP: 2000,
		// Per-room connection cap.
		maxConnectionsPerRoom: 6,
		// Per-user websocket connect attempts/hour.
		connectUserPerHour: 120,
		// Per-user websocket connect attempts/day.
		connectUserPerDay: 1000,
		// Per-user websocket connect attempts/week.
		connectUserPerWeek: 4000,
		// Per-user websocket connect attempts/month.
		connectUserPerMonth: 12000,
		// Per-IP websocket connect attempts/hour.
		connectIpPerHour: 180,
		// Per-IP websocket connect attempts/day.
		connectIpPerDay: 1200,
		// Per-IP websocket connect attempts/week.
		connectIpPerWeek: 5000,
		// Per-IP websocket connect attempts/month.
		connectIpPerMonth: 15000,
		// Per-device websocket connect attempts/hour.
		connectDevicePerHour: 120,
		// Per-device websocket connect attempts/day.
		connectDevicePerDay: 900,
		// Per-device websocket connect attempts/week.
		connectDevicePerWeek: 3600,
		// Per-device websocket connect attempts/month.
		connectDevicePerMonth: 10000
	},
	upload: {
		// Generic upload max bytes.
		maxFileBytes: 5242880,
		// Max image upload bytes.
		maxImageBytes: 5242880,
		// Max multipart request bytes.
		maxMultipartBytes: 6291456,
		// Max text-field size in multipart payload.
		maxFormFieldLength: 1024,
		// Generate URL requests per-user/hour.
		generateUrlUserPerHour: 25,
		// Generate URL requests per-user/day.
		generateUrlUserPerDay: 120,
		// Generate URL requests per-user/week.
		generateUrlUserPerWeek: 500,
		// Generate URL requests per-user/month.
		generateUrlUserPerMonth: 1500,
		// Generate URL requests per-IP/hour.
		generateUrlIpPerHour: 30,
		// Generate URL requests per-IP/day.
		generateUrlIpPerDay: 150,
		// Generate URL requests per-IP/week.
		generateUrlIpPerWeek: 600,
		// Generate URL requests per-IP/month.
		generateUrlIpPerMonth: 1800,
		// Generate URL requests per-device/hour.
		generateUrlDevicePerHour: 20,
		// Generate URL requests per-device/day.
		generateUrlDevicePerDay: 100,
		// Generate URL requests per-device/week.
		generateUrlDevicePerWeek: 450,
		// Generate URL requests per-device/month.
		generateUrlDevicePerMonth: 1400,
		// Proxy uploads per-user/hour.
		proxyUserPerHour: 15,
		// Proxy uploads per-user/day.
		proxyUserPerDay: 70,
		// Proxy uploads per-user/week.
		proxyUserPerWeek: 300,
		// Proxy uploads per-user/month.
		proxyUserPerMonth: 900,
		// Proxy uploads per-IP/hour.
		proxyIpPerHour: 20,
		// Proxy uploads per-IP/day.
		proxyIpPerDay: 90,
		// Proxy uploads per-IP/week.
		proxyIpPerWeek: 350,
		// Proxy uploads per-IP/month.
		proxyIpPerMonth: 1000,
		// Proxy uploads per-device/hour.
		proxyDevicePerHour: 12,
		// Proxy uploads per-device/day.
		proxyDevicePerDay: 60,
		// Proxy uploads per-device/week.
		proxyDevicePerWeek: 250,
		// Proxy uploads per-device/month.
		proxyDevicePerMonth: 750
	},
	room: {
		// Room numeric code length.
		codeDigits: 6,
		// Max room name length.
		nameMaxLength: 20,
		// Max room password length.
		passwordMaxLength: 64,
		// Max breakout descendants from root room.
		maxDescendants: 6,
		// Max room extension horizon (hours).
		maxDurationHours: 360
	},
	users: {
		// Max members inside one room.
		maxRoomMembers: 1200
	}
} as const;

// Backwards-compatible alias for backend AI parsing code.
export const AI_LIMITS = LIMITS.ai;
