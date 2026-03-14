<script lang="ts">
	import { browser } from '$app/environment';
	import { goto } from '$app/navigation';
	import { authState } from '$lib/stores/auth';
	import { getOrInitIdentity } from '$lib/utils/identity';
	import { normalizeRoomIdValue, normalizeRoomNameInput } from '$lib/utils/homeJoin';
	import { get } from 'svelte/store';
	import { onMount } from 'svelte';

	type RoomSource = 'ephemeral' | 'persistent' | 'mixed';
	type RoomStatus = 'joined' | 'discoverable' | 'left';

	type DashboardRoom = {
		room_id?: string;
		room_name?: string;
		role?: string;
		last_accessed?: string;
	};

	type SidebarRoom = {
		roomId?: string;
		roomName?: string;
		status?: string;
		createdAt?: number;
	};

	type SidebarRoomsResponse = {
		rooms?: SidebarRoom[];
	};

	type RoomListItem = {
		id: string;
		name: string;
		status: RoomStatus;
		source: RoomSource;
		role: string;
		lastActivity: number;
	};

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = (() => {
		const configured = API_BASE_RAW?.trim();
		if (configured) {
			return configured;
		}
		if (!browser) {
			return 'http://localhost:8080';
		}
		const protocol = window.location.protocol === 'https:' ? 'https:' : 'http:';
		const host = window.location.hostname;
		if (host === 'localhost' || host === '127.0.0.1') {
			return `${protocol}//${host}:8080`;
		}
		return window.location.origin;
	})();

	let rooms: RoomListItem[] = [];
	let selectedRoomId = '';
	let isLoading = false;
	let loadError = '';

	$: selectedRoom = rooms.find((room) => room.id === selectedRoomId) ?? null;
	$: if (
		rooms.length > 0 &&
		(selectedRoomId === '' || !rooms.some((room) => room.id === selectedRoomId))
	) {
		selectedRoomId = rooms[0].id;
	}

	function buildAuthHeaders() {
		const headers: Record<string, string> = {};
		if (!browser) {
			return headers;
		}
		const fromStore = get(authState).token?.trim() || '';
		const fromStorage = window.localStorage.getItem('converse.auth.token')?.trim() || '';
		const token = fromStore || fromStorage;
		if (token) {
			headers.Authorization = `Bearer ${token}`;
		}
		return headers;
	}

	function resolveUserId() {
		const accountUser = get(authState).user;
		const identity = getOrInitIdentity();
		return (accountUser?.id || identity.id || '').trim();
	}

	function normalizeStatus(raw: string): RoomStatus {
		const normalized = raw.trim().toLowerCase();
		if (normalized === 'discoverable') {
			return 'discoverable';
		}
		if (normalized === 'left') {
			return 'left';
		}
		return 'joined';
	}

	function statusRank(status: RoomStatus) {
		if (status === 'joined') {
			return 0;
		}
		if (status === 'discoverable') {
			return 1;
		}
		return 2;
	}

	function formatLastActivity(timestamp: number) {
		if (!Number.isFinite(timestamp) || timestamp <= 0) {
			return 'No activity yet';
		}
		return new Intl.DateTimeFormat('en-US', {
			month: 'short',
			day: 'numeric',
			hour: 'numeric',
			minute: '2-digit'
		}).format(timestamp);
	}

	async function fetchDashboardRooms() {
		const response = await fetch(`${API_BASE}/api/dashboard/rooms`, {
			method: 'GET',
			headers: buildAuthHeaders(),
			credentials: 'include'
		});
		if (!response.ok) {
			return [] as DashboardRoom[];
		}
		const payload = (await response.json().catch(() => [])) as unknown;
		return Array.isArray(payload) ? (payload as DashboardRoom[]) : [];
	}

	async function fetchSidebarRooms(userId: string) {
		if (!userId) {
			return [] as SidebarRoom[];
		}
		const response = await fetch(
			`${API_BASE}/api/rooms/sidebar?userId=${encodeURIComponent(userId)}`,
			{
				method: 'GET',
				credentials: 'include'
			}
		);
		if (!response.ok) {
			return [] as SidebarRoom[];
		}
		const payload = (await response.json().catch(() => null)) as SidebarRoomsResponse | null;
		return Array.isArray(payload?.rooms) ? payload.rooms : [];
	}

	function mergeRooms(dashboardRooms: DashboardRoom[], sidebarRooms: SidebarRoom[]) {
		const map = new Map<string, RoomListItem>();

		for (const room of dashboardRooms) {
			const roomId = normalizeRoomIdValue(room.room_id || '');
			if (!roomId) {
				continue;
			}
			const roomName = normalizeRoomNameInput(room.room_name || '') || roomId;
			const lastAccessed = Date.parse((room.last_accessed || '').trim());
			map.set(roomId, {
				id: roomId,
				name: roomName,
				status: 'joined',
				source: 'persistent',
				role: (room.role || '').trim() || 'member',
				lastActivity: Number.isFinite(lastAccessed) ? lastAccessed : 0
			});
		}

		for (const room of sidebarRooms) {
			const roomId = normalizeRoomIdValue(room.roomId || '');
			if (!roomId) {
				continue;
			}
			const roomName = normalizeRoomNameInput(room.roomName || '') || roomId;
			const createdAtMs =
				Number.isFinite(room.createdAt) && Number(room.createdAt) > 0
					? Number(room.createdAt) * 1000
					: 0;
			const existing = map.get(roomId);
			if (!existing) {
				map.set(roomId, {
					id: roomId,
					name: roomName,
					status: normalizeStatus(room.status || ''),
					source: 'ephemeral',
					role: 'member',
					lastActivity: createdAtMs
				});
				continue;
			}
			map.set(roomId, {
				...existing,
				name: existing.name || roomName,
				status:
					existing.status === 'joined' ? 'joined' : normalizeStatus(room.status || existing.status),
				source: existing.source === 'persistent' ? 'mixed' : 'ephemeral',
				lastActivity: Math.max(existing.lastActivity, createdAtMs)
			});
		}

		return [...map.values()].sort((left, right) => {
			const rankDiff = statusRank(left.status) - statusRank(right.status);
			if (rankDiff !== 0) {
				return rankDiff;
			}
			if (left.lastActivity !== right.lastActivity) {
				return right.lastActivity - left.lastActivity;
			}
			return left.name.localeCompare(right.name, undefined, { sensitivity: 'base' });
		});
	}

	async function loadRooms() {
		isLoading = true;
		loadError = '';
		try {
			const userId = resolveUserId();
			const [dashboardRooms, sidebarRooms] = await Promise.all([
				fetchDashboardRooms(),
				fetchSidebarRooms(userId)
			]);
			rooms = mergeRooms(dashboardRooms, sidebarRooms);
			if (rooms.length === 0) {
				selectedRoomId = '';
			} else if (!rooms.some((room) => room.id === selectedRoomId)) {
				selectedRoomId = rooms[0].id;
			}
		} catch (error) {
			loadError = error instanceof Error ? error.message : 'Failed to load rooms.';
		} finally {
			isLoading = false;
		}
	}

	function selectRoom(roomId: string) {
		selectedRoomId = roomId;
	}

	function openSelectedRoom() {
		if (!selectedRoom) {
			return;
		}
		void goto(
			`/chat/${encodeURIComponent(selectedRoom.id)}?name=${encodeURIComponent(
				selectedRoom.name
			)}&member=1`
		);
	}

	onMount(() => {
		void loadRooms();
	});
</script>

<svelte:head>
	<title>Rooms | Converse</title>
</svelte:head>

<main class="rooms-shell">
	<section class="chat-shell">
		<aside class="room-list-panel">
			<header class="room-list-header">
				<div>
					<h1>Room List</h1>
					<p>Ephemeral chats show here. Persistent/project chats join this list when created.</p>
				</div>
				<button type="button" class="refresh-btn" on:click={loadRooms} disabled={isLoading}>
					{isLoading ? '...' : 'Refresh'}
				</button>
			</header>

			{#if loadError}
				<div class="state-box error">{loadError}</div>
			{/if}

			{#if isLoading}
				<div class="state-box">Loading rooms...</div>
			{:else if rooms.length === 0}
				<div class="state-box">No rooms yet.</div>
			{:else}
				<div class="room-list">
					{#each rooms as room (room.id)}
						<button
							type="button"
							class="room-row"
							class:active={room.id === selectedRoomId}
							on:click={() => selectRoom(room.id)}
						>
							<div class="room-row-head">
								<strong>{room.name}</strong>
								<span class="room-chip {room.source}">
									{room.source === 'mixed' ? 'ephemeral + persistent' : room.source}
								</span>
							</div>
							<div class="room-row-meta">
								<span>{room.role || room.status}</span>
								<span>{formatLastActivity(room.lastActivity)}</span>
							</div>
						</button>
					{/each}
				</div>
			{/if}
		</aside>

		<section class="chat-window-panel">
			<header class="chat-window-header">
				<h2>Chat Window</h2>
				{#if selectedRoom}
					<button type="button" class="open-btn" on:click={openSelectedRoom}> Open Room </button>
				{/if}
			</header>

			{#if selectedRoom}
				<div class="chat-window-content">
					<div class="chat-room-title">{selectedRoom.name}</div>
					<div class="chat-room-subtitle">
						Selected from room list. Open to continue in the full chat experience.
					</div>

					<div class="fake-message-row mine">
						<div class="fake-message">No active message stream in this launcher view.</div>
					</div>
					<div class="fake-message-row">
						<div class="fake-message">
							Use <strong>Open Room</strong> to jump into live room chat.
						</div>
					</div>
				</div>
			{:else}
				<div class="chat-window-empty">
					<p>Select a room from the list to preview it here.</p>
				</div>
			{/if}
		</section>
	</section>
</main>

<style>
	:global(:root) {
		--rooms-shell-bg:
			radial-gradient(circle at 12% -8%, rgba(157, 196, 248, 0.2), transparent 36%),
			radial-gradient(circle at 90% 12%, rgba(188, 212, 248, 0.2), transparent 34%), #f3f7ff;
		--rooms-panel-bg: rgba(255, 255, 255, 0.62);
		--rooms-panel-border: rgba(175, 198, 232, 0.5);
		--rooms-panel-shadow: 0 18px 44px rgba(93, 120, 168, 0.2);
		--rooms-text: #13203b;
		--rooms-muted: rgba(61, 80, 114, 0.76);
		--rooms-btn-bg: rgba(255, 255, 255, 0.72);
		--rooms-btn-border: rgba(98, 129, 182, 0.36);
		--rooms-btn-text: #12305d;
		--rooms-state-bg: rgba(255, 255, 255, 0.48);
		--rooms-state-border: rgba(149, 178, 222, 0.5);
		--rooms-state-text: rgba(66, 84, 119, 0.8);
		--rooms-error-bg: rgba(220, 38, 38, 0.13);
		--rooms-error-border: rgba(220, 38, 38, 0.36);
		--rooms-error-text: #8f2336;
		--rooms-row-bg: rgba(255, 255, 255, 0.56);
		--rooms-row-border: rgba(165, 189, 228, 0.55);
		--rooms-row-active-bg: rgba(214, 229, 251, 0.9);
		--rooms-row-active-border: rgba(102, 141, 206, 0.64);
		--rooms-chip-ephemeral-bg: rgba(16, 185, 129, 0.16);
		--rooms-chip-ephemeral-text: #0f766e;
		--rooms-chip-persistent-bg: rgba(59, 130, 246, 0.16);
		--rooms-chip-persistent-text: #1d4ed8;
		--rooms-chip-mixed-bg: rgba(168, 85, 247, 0.16);
		--rooms-chip-mixed-text: #7e22ce;
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--rooms-shell-bg:
			radial-gradient(circle at 12% -8%, rgba(255, 255, 255, 0.08), transparent 36%),
			radial-gradient(circle at 90% 12%, rgba(255, 255, 255, 0.05), transparent 34%), #0d0d12;
		--rooms-panel-bg: rgba(255, 255, 255, 0.03);
		--rooms-panel-border: rgba(255, 255, 255, 0.1);
		--rooms-panel-shadow: 0 18px 44px rgba(0, 0, 0, 0.36);
		--rooms-text: #f3f7ff;
		--rooms-muted: rgba(205, 214, 234, 0.74);
		--rooms-btn-bg: rgba(255, 255, 255, 0.08);
		--rooms-btn-border: rgba(255, 255, 255, 0.18);
		--rooms-btn-text: #edf4ff;
		--rooms-state-bg: rgba(255, 255, 255, 0.04);
		--rooms-state-border: rgba(255, 255, 255, 0.15);
		--rooms-state-text: rgba(199, 207, 227, 0.8);
		--rooms-error-bg: rgba(220, 38, 38, 0.2);
		--rooms-error-border: rgba(248, 113, 113, 0.35);
		--rooms-error-text: #ffd6df;
		--rooms-row-bg: rgba(255, 255, 255, 0.04);
		--rooms-row-border: rgba(255, 255, 255, 0.12);
		--rooms-row-active-bg: rgba(255, 255, 255, 0.1);
		--rooms-row-active-border: rgba(174, 198, 244, 0.65);
		--rooms-chip-ephemeral-bg: rgba(16, 185, 129, 0.2);
		--rooms-chip-ephemeral-text: #6ee7b7;
		--rooms-chip-persistent-bg: rgba(59, 130, 246, 0.22);
		--rooms-chip-persistent-text: #93c5fd;
		--rooms-chip-mixed-bg: rgba(168, 85, 247, 0.22);
		--rooms-chip-mixed-text: #d8b4fe;
	}

	.rooms-shell {
		height: 100dvh;
		box-sizing: border-box;
		padding: 5.6rem 1.2rem 1.3rem;
		background: var(--rooms-shell-bg);
		color: var(--rooms-text);
		overflow: hidden;
	}

	.chat-shell {
		height: 100%;
		display: grid;
		grid-template-columns: 300px minmax(0, 1fr);
		gap: 0.95rem;
		min-height: 0;
		overflow: hidden;
	}

	.room-list-panel,
	.chat-window-panel {
		min-height: 0;
		border-radius: 16px;
		border: 1px solid var(--rooms-panel-border);
		background: var(--rooms-panel-bg);
		box-shadow: var(--rooms-panel-shadow);
		backdrop-filter: blur(14px);
		-webkit-backdrop-filter: blur(14px);
	}

	.room-list-panel {
		padding: 0.9rem;
		display: grid;
		grid-template-rows: auto auto 1fr;
		gap: 0.78rem;
		overflow: auto;
	}

	.room-list-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 0.6rem;
	}

	.room-list-header h1 {
		margin: 0;
		font-size: 1.03rem;
		letter-spacing: 0.03em;
	}

	.room-list-header p {
		margin: 0.32rem 0 0;
		font-size: 0.75rem;
		color: var(--rooms-muted);
	}

	.refresh-btn {
		border-radius: 9px;
		border: 1px solid var(--rooms-btn-border);
		background: var(--rooms-btn-bg);
		color: var(--rooms-btn-text);
		padding: 0.28rem 0.5rem;
		font-size: 0.67rem;
		cursor: pointer;
	}

	.refresh-btn:disabled {
		opacity: 0.62;
		cursor: not-allowed;
	}

	.state-box {
		border: 1px solid var(--rooms-state-border);
		background: var(--rooms-state-bg);
		color: var(--rooms-state-text);
		border-radius: 11px;
		padding: 0.6rem;
		font-size: 0.78rem;
	}

	.state-box.error {
		border-color: var(--rooms-error-border);
		background: var(--rooms-error-bg);
		color: var(--rooms-error-text);
	}

	.room-list {
		display: grid;
		gap: 0.46rem;
		overflow-y: auto;
	}

	.room-row {
		border-radius: 11px;
		border: 1px solid var(--rooms-row-border);
		background: var(--rooms-row-bg);
		padding: 0.56rem 0.62rem;
		display: grid;
		gap: 0.3rem;
		text-align: left;
		color: var(--rooms-text);
		cursor: pointer;
	}

	.room-row.active {
		background: var(--rooms-row-active-bg);
		border-color: var(--rooms-row-active-border);
	}

	.room-row-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.42rem;
	}

	.room-row-head strong {
		font-size: 0.79rem;
		line-height: 1.2;
	}

	.room-chip {
		border-radius: 999px;
		padding: 0.12rem 0.42rem;
		font-size: 0.63rem;
		font-weight: 700;
		white-space: nowrap;
	}

	.room-chip.ephemeral {
		background: var(--rooms-chip-ephemeral-bg);
		color: var(--rooms-chip-ephemeral-text);
	}

	.room-chip.persistent {
		background: var(--rooms-chip-persistent-bg);
		color: var(--rooms-chip-persistent-text);
	}

	.room-chip.mixed {
		background: var(--rooms-chip-mixed-bg);
		color: var(--rooms-chip-mixed-text);
	}

	.room-row-meta {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		font-size: 0.67rem;
		color: var(--rooms-muted);
	}

	.chat-window-panel {
		display: grid;
		grid-template-rows: auto 1fr;
		overflow: hidden;
	}

	.chat-window-header {
		padding: 0.86rem 0.98rem;
		border-bottom: 1px solid var(--rooms-panel-border);
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
	}

	.chat-window-header h2 {
		margin: 0;
		font-size: 0.98rem;
		letter-spacing: 0.03em;
	}

	.open-btn {
		border-radius: 10px;
		border: 1px solid rgba(37, 99, 235, 0.55);
		background: linear-gradient(135deg, #3b82f6, #2563eb);
		color: #f8fbff;
		padding: 0.42rem 0.62rem;
		font-size: 0.72rem;
		font-weight: 700;
		cursor: pointer;
	}

	.chat-window-content {
		padding: 1rem;
		display: grid;
		grid-template-rows: auto auto 1fr;
		gap: 0.62rem;
		overflow: auto;
	}

	.chat-room-title {
		font-size: 1rem;
		font-weight: 700;
	}

	.chat-room-subtitle {
		font-size: 0.78rem;
		color: var(--rooms-muted);
	}

	.fake-message-row {
		display: flex;
	}

	.fake-message-row.mine {
		justify-content: flex-end;
	}

	.fake-message {
		max-width: 82%;
		border-radius: 12px;
		border: 1px solid var(--rooms-row-border);
		background: var(--rooms-row-bg);
		padding: 0.55rem 0.66rem;
		font-size: 0.78rem;
		line-height: 1.35;
		color: var(--rooms-text);
	}

	.chat-window-empty {
		padding: 1rem;
		display: flex;
		align-items: center;
		justify-content: center;
		color: var(--rooms-muted);
		font-size: 0.82rem;
	}

	@media (max-width: 980px) {
		.rooms-shell {
			padding: 5.3rem 0.8rem 0.9rem;
		}

		.chat-shell {
			grid-template-columns: 1fr;
		}

		.room-list-panel,
		.chat-window-panel {
			overflow: visible;
		}
	}
</style>
