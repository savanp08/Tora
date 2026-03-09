import { derived, writable } from 'svelte/store';

export type WorkspaceContext = {
	type: 'personal' | 'room';
	id: string;
	name: string;
};

const defaultContext: WorkspaceContext = {
	type: 'personal',
	id: 'personal',
	name: 'Personal Taskboard'
};

export const activeContext = writable<WorkspaceContext>(defaultContext);

export function setContext(type: WorkspaceContext['type'], id: string, name: string) {
	const normalizedID = id.trim() || (type === 'personal' ? 'personal' : '');
	const normalizedName = name.trim() || (type === 'personal' ? 'Personal Taskboard' : 'Workspace');
	if (!normalizedID) {
		return;
	}
	activeContext.set({
		type,
		id: normalizedID,
		name: normalizedName
	});
}

export const activeContextID = derived(activeContext, ($activeContext) => $activeContext.id);

