export type ThreadStatus = 'joined' | 'discoverable' | 'left';
export type MessageActionMode = 'none' | 'break' | 'edit' | 'delete' | 'pin';
export type RoomMenuMode = 'create' | 'join';
export type ThemePreference = 'system' | 'light' | 'dark';

export type UiDialogState =
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

export type ChatMessage = {
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
	isPinned?: boolean;
	pinnedBy?: string;
	pinnedByName?: string;
	pending?: boolean;
};

export type TaskChecklistItem = {
	text: string;
	completed: boolean;
	completedBy: string;
	timestamp: number;
	createdBy: string;
	createdAt: number;
};

export type TaskMessagePayload = {
	title: string;
	tasks: TaskChecklistItem[];
};

export type ComposerMediaPayload = {
	type: 'image' | 'video' | 'file' | 'audio' | 'task';
	content: string;
	fileName?: string;
	text?: string;
};

export type ChatThread = {
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
	adminCode?: string;
};

export type OnlineMember = {
	id: string;
	name: string;
	isOnline: boolean;
	joinedAt: number;
	isAdmin?: boolean;
};

export type RoomMeta = {
	createdAt: number;
	expiresAt: number;
};

export type SidebarRoom = {
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
	adminCode?: string;
};

export type ReplyTarget = {
	messageId: string;
	senderName: string;
	content: string;
};

export type SocketEnvelope = {
	type: string;
	payload: unknown;
	roomId?: unknown;
	room_id?: unknown;
};
