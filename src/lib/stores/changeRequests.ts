import { writable, derived, get } from 'svelte/store';

export type ChangeRequestAction =
	| 'add_task'
	| 'edit_task'
	| 'delete_task'
	| 'add_sprint'
	| 'edit_sprint'
	| 'delete_sprint'
	| 'edit_timeline'
	| 'edit_cost'
	| 'import_sheet'
	| 'edit_field_schema'
	| 'remove_member';

export type ChangeRequestStatus = 'pending' | 'approved' | 'rejected';

export type CRCommentReply = {
	id: string;
	userId: string;
	userName: string;
	text: string;
	createdAt: string;
};

export type CRComment = {
	id: string;
	userId: string;
	userName: string;
	text: string;
	createdAt: string;
	isPinned: boolean;
	isHighlighted: boolean;
	replies: CRCommentReply[];
};

export interface ChangeRequest {
	id: string;
	roomId: string;
	userId: string;
	userName: string;
	action: ChangeRequestAction;
	/** Human-readable label of what is being changed */
	targetLabel: string;
	/** The full change payload (task fields, sprint name, etc.) */
	payload: Record<string, unknown>;
	status: ChangeRequestStatus;
	/** ISO timestamp */
	createdAt: string;
	/** ISO timestamp of admin decision */
	resolvedAt?: string;
	resolvedBy?: string;
	/** Optional note from admin when resolving */
	resolveNote?: string;
	/** Discussion thread on this request */
	discussion: CRComment[];
}

// Per-room map: roomId → ChangeRequest[]
const _store = writable<Map<string, ChangeRequest[]>>(new Map());

export const changeRequestStore = {
	subscribe: _store.subscribe,

	/** Upsert a request (add or update by id) */
	upsert(req: ChangeRequest) {
		_store.update((m) => {
			const list = m.get(req.roomId) ?? [];
			const idx = list.findIndex((r) => r.id === req.id);
			if (idx >= 0) {
				list[idx] = req;
			} else {
				list.unshift(req);
			}
			return new Map(m).set(req.roomId, list);
		});
	},

	/** Remove a request by id */
	remove(roomId: string, id: string) {
		_store.update((m) => {
			const list = (m.get(roomId) ?? []).filter((r) => r.id !== id);
			return new Map(m).set(roomId, list);
		});
	},

	/** Get all requests for a room */
	forRoom(roomId: string): ChangeRequest[] {
		return get(_store).get(roomId) ?? [];
	},

	/** Resolve (approve / reject) a request locally */
	resolve(roomId: string, id: string, status: 'approved' | 'rejected', resolvedBy: string, note?: string) {
		_store.update((m) => {
			const list = m.get(roomId) ?? [];
			const idx = list.findIndex((r) => r.id === id);
			if (idx >= 0) {
				list[idx] = {
					...list[idx],
					status,
					resolvedAt: new Date().toISOString(),
					resolvedBy,
					...(note?.trim() ? { resolveNote: note.trim() } : {})
				};
			}
			return new Map(m).set(roomId, list);
		});
	},

	/** Add a discussion comment to a request */
	addComment(roomId: string, reqId: string, comment: Omit<CRComment, 'replies'>) {
		_store.update((m) => {
			const list = m.get(roomId) ?? [];
			const idx = list.findIndex((r) => r.id === reqId);
			if (idx >= 0) {
				const req = list[idx];
				list[idx] = {
					...req,
					discussion: [...req.discussion, { ...comment, replies: [] }]
				};
			}
			return new Map(m).set(roomId, list);
		});
	},

	/** Add a reply to a discussion comment */
	addReply(roomId: string, reqId: string, commentId: string, reply: CRCommentReply) {
		_store.update((m) => {
			const list = m.get(roomId) ?? [];
			const idx = list.findIndex((r) => r.id === reqId);
			if (idx >= 0) {
				const req = list[idx];
				list[idx] = {
					...req,
					discussion: req.discussion.map((c) =>
						c.id === commentId ? { ...c, replies: [...c.replies, reply] } : c
					)
				};
			}
			return new Map(m).set(roomId, list);
		});
	},

	/** Toggle pin on a discussion comment */
	togglePin(roomId: string, reqId: string, commentId: string) {
		_store.update((m) => {
			const list = m.get(roomId) ?? [];
			const idx = list.findIndex((r) => r.id === reqId);
			if (idx >= 0) {
				const req = list[idx];
				list[idx] = {
					...req,
					discussion: req.discussion.map((c) =>
						c.id === commentId ? { ...c, isPinned: !c.isPinned } : c
					)
				};
			}
			return new Map(m).set(roomId, list);
		});
	},

	/** Toggle highlight on a discussion comment */
	toggleHighlight(roomId: string, reqId: string, commentId: string) {
		_store.update((m) => {
			const list = m.get(roomId) ?? [];
			const idx = list.findIndex((r) => r.id === reqId);
			if (idx >= 0) {
				const req = list[idx];
				list[idx] = {
					...req,
					discussion: req.discussion.map((c) =>
						c.id === commentId ? { ...c, isHighlighted: !c.isHighlighted } : c
					)
				};
			}
			return new Map(m).set(roomId, list);
		});
	}
};

/** Reactive pending count for a given roomId (call inside a component with $: ) */
export function pendingCount(roomId: string) {
	return derived(_store, ($m) => ($m.get(roomId) ?? []).filter((r) => r.status === 'pending').length);
}

/** Generate a lightweight unique ID */
export function genCRId(): string {
	return `cr_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 7)}`;
}

/** Build and upsert a new pending change request; returns the request */
export function submitChangeRequest(
	roomId: string,
	userId: string,
	userName: string,
	action: ChangeRequestAction,
	targetLabel: string,
	payload: Record<string, unknown>
): ChangeRequest {
	const req: ChangeRequest = {
		id: genCRId(),
		roomId,
		userId,
		userName,
		action,
		targetLabel,
		payload,
		status: 'pending',
		createdAt: new Date().toISOString(),
		discussion: []
	};
	changeRequestStore.upsert(req);
	return req;
}

/** Parse an incoming WebSocket change_request event and upsert it */
export function handleIncomingChangeRequest(payload: unknown): ChangeRequest | null {
	if (!payload || typeof payload !== 'object') return null;
	const p = payload as Record<string, unknown>;
	const req: ChangeRequest = {
		id: String(p.id ?? ''),
		roomId: String(p.roomId ?? p.room_id ?? ''),
		userId: String(p.userId ?? p.user_id ?? ''),
		userName: String(p.userName ?? p.user_name ?? ''),
		action: (p.action ?? 'edit_task') as ChangeRequestAction,
		targetLabel: String(p.targetLabel ?? p.target_label ?? ''),
		payload: (p.payload as Record<string, unknown>) ?? {},
		status: (p.status ?? 'pending') as ChangeRequestStatus,
		createdAt: String(p.createdAt ?? p.created_at ?? new Date().toISOString()),
		resolvedAt: p.resolvedAt ? String(p.resolvedAt) : undefined,
		resolvedBy: p.resolvedBy ? String(p.resolvedBy) : undefined,
		resolveNote: p.resolveNote ? String(p.resolveNote) : undefined,
		discussion: Array.isArray(p.discussion) ? (p.discussion as CRComment[]) : []
	};
	if (!req.id || !req.roomId) return null;
	changeRequestStore.upsert(req);
	return req;
}
