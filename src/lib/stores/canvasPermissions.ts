import { writable, get } from 'svelte/store';

// Per-room map: roomId → Set of userId strings who have canvas edit access
const _store = writable<Map<string, Set<string>>>(new Map());

export const canvasPermissionStore = {
	subscribe: _store.subscribe,

	grant(roomId: string, userId: string) {
		_store.update((m) => {
			const editors = new Set(m.get(roomId) ?? []);
			editors.add(userId.trim());
			return new Map(m).set(roomId, editors);
		});
	},

	revoke(roomId: string, userId: string) {
		_store.update((m) => {
			const editors = new Set(m.get(roomId) ?? []);
			editors.delete(userId.trim());
			return new Map(m).set(roomId, editors);
		});
	},

	hasEdit(roomId: string, userId: string): boolean {
		return get(_store).get(roomId)?.has(userId.trim()) ?? false;
	},

	getEditors(roomId: string): string[] {
		return Array.from(get(_store).get(roomId) ?? []);
	},

	toggle(roomId: string, userId: string) {
		if (canvasPermissionStore.hasEdit(roomId, userId)) {
			canvasPermissionStore.revoke(roomId, userId);
		} else {
			canvasPermissionStore.grant(roomId, userId);
		}
	}
};
