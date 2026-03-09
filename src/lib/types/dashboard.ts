export type WorkspaceModule = 'dashboard' | 'draw' | 'code' | 'tasks';

export type DashboardItemKind = 'message' | 'note' | 'task';

export type RoomDashboardItem = {
	id: string;
	roomId: string;
	messageId: string;
	kind: DashboardItemKind;
	senderId: string;
	senderName: string;
	pinnedByUserId: string;
	pinnedByName: string;
	originalCreatedAt: number;
	pinnedAt: number;
	messageText: string;
	mediaUrl: string;
	mediaType: string;
	fileName: string;
	note: string;
	beaconAt: number | null;
	beaconLabel: string;
	beaconData: Record<string, unknown> | null;
	taskTitle: string;
	topic?: string;
};

export type RoomDashboardOrganizePayload = {
	items: RoomDashboardItem[];
};

export type RoomDashboardOrganizeSections = {
	priority: RoomDashboardItem[];
	pinnedItems: RoomDashboardItem[];
	expired: RoomDashboardItem[];
};
