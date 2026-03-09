import { error, redirect } from '@sveltejs/kit';
import type { PageServerLoad } from './$types';

const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';

type DashboardRoom = {
	room_id: string;
	room_name: string;
	role: string;
	last_accessed: string;
};

function normalizeDashboardRooms(payload: unknown): DashboardRoom[] {
	if (!Array.isArray(payload)) {
		return [];
	}

	const rooms: DashboardRoom[] = [];
	for (const entry of payload) {
		if (!entry || typeof entry !== 'object') {
			continue;
		}
		const row = entry as Record<string, unknown>;
		const roomID = typeof row.room_id === 'string' ? row.room_id.trim() : '';
		if (!roomID) {
			continue;
		}
		const roomName = typeof row.room_name === 'string' ? row.room_name.trim() : '';
		const role = typeof row.role === 'string' ? row.role.trim() : '';
		const lastAccessed = typeof row.last_accessed === 'string' ? row.last_accessed.trim() : '';

		rooms.push({
			room_id: roomID,
			room_name: roomName,
			role,
			last_accessed: lastAccessed
		});
	}

	return rooms;
}

export const load: PageServerLoad = async ({ fetch, cookies, parent }) => {
	const parentData = await parent();
	const jwtToken = cookies.get('tora_auth')?.trim();
	if (!jwtToken) {
		throw redirect(303, '/login');
	}

	const response = await fetch(`${API_BASE}/api/dashboard/rooms`, {
		method: 'GET',
		headers: {
			cookie: `tora_auth=${encodeURIComponent(jwtToken)}`
		}
	});
	if (response.status === 401 || response.status === 403) {
		throw redirect(303, '/login');
	}
	if (!response.ok) {
		throw error(response.status, 'Failed to load dashboard rooms');
	}

	const payload = (await response.json().catch(() => null)) as unknown;
	const rooms = normalizeDashboardRooms(payload);

	return {
		user: parentData.user,
		rooms
	};
};
