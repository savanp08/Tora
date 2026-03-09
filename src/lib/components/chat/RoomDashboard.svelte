<script lang="ts">
	import { createEventDispatcher, onDestroy, onMount } from 'svelte';
	import type {
		RoomDashboardItem,
		RoomDashboardOrganizePayload,
		RoomDashboardOrganizeSections
	} from '$lib/types/dashboard';

	export let roomId = '';
	export let items: RoomDashboardItem[] = [];
	export let isDarkMode = false;
	export let currentUserId = '';
	export let organizePreview: RoomDashboardOrganizeSections | null = null;

	type DashboardAddItemKind = 'note' | 'beacon' | 'task';

	const dispatch = createEventDispatcher<{
		editNote: { itemId: string; note: string };
		addItemRequest: { kind: DashboardAddItemKind };
		aiOrganizePreview: RoomDashboardOrganizeSections;
		aiOrganizeError: { message: string };
	}>();
	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';

	const KIND_ORDER: Record<RoomDashboardItem['kind'], number> = {
		message: 0,
		note: 1,
		task: 2
	};

	let noteDraftByItemId: Record<string, string> = {};
	let isOrganizing = false;
	let nowMs = Date.now();
	let ticker: ReturnType<typeof setInterval> | null = null;
	let addMenuOpen = false;
	let addMenuRef: HTMLElement | null = null;

	$: scopedItems = items.filter((item) => item.roomId === roomId);
	$: scheduledItemsComputed = scopedItems
		.filter((item) => Number.isFinite(item.beaconAt) && Number(item.beaconAt) > 0)
		.map((item) => ({ ...item, beaconAt: Number(item.beaconAt) }));
	$: priorityItemsComputed = [...scheduledItemsComputed]
		.filter((item) => item.beaconAt >= nowMs)
		.sort((left, right) => left.beaconAt - right.beaconAt);
	$: expiredItemsComputed = [...scheduledItemsComputed]
		.filter((item) => item.beaconAt < nowMs)
		.sort((left, right) => right.beaconAt - left.beaconAt);
	$: groupedPinnedItemsComputed = [...scopedItems].sort((left, right) => {
		const kindDelta = KIND_ORDER[left.kind] - KIND_ORDER[right.kind];
		if (kindDelta !== 0) {
			return kindDelta;
		}
		return right.pinnedAt - left.pinnedAt;
	});
	$: priorityItems = organizePreview?.priority ?? priorityItemsComputed;
	$: expiredItems = organizePreview?.expired ?? expiredItemsComputed;
	$: groupedPinnedItems = organizePreview?.pinnedItems ?? groupedPinnedItemsComputed;

	onMount(() => {
		document.addEventListener('pointerdown', onDocumentPointerDown);
		ticker = setInterval(() => {
			nowMs = Date.now();
		}, 30000);
		return () => {
			document.removeEventListener('pointerdown', onDocumentPointerDown);
			if (ticker) {
				clearInterval(ticker);
				ticker = null;
			}
		};
	});

	onDestroy(() => {
		if (ticker) {
			clearInterval(ticker);
			ticker = null;
		}
	});

	function formatDateTime(timestamp: number | null) {
		if (!timestamp || !Number.isFinite(timestamp) || timestamp <= 0) {
			return 'Not scheduled';
		}
		return new Date(timestamp).toLocaleString([], {
			month: 'short',
			day: 'numeric',
			hour: '2-digit',
			minute: '2-digit'
		});
	}

	function formatKindLabel(kind: RoomDashboardItem['kind']) {
		if (kind === 'task') {
			return 'Task';
		}
		if (kind === 'note') {
			return 'Note';
		}
		return 'Message';
	}

	function isImageItem(item: RoomDashboardItem) {
		return (item.mediaType || '').toLowerCase().startsWith('image/');
	}

	function resolveDraftNote(item: RoomDashboardItem) {
		return noteDraftByItemId[item.id] ?? item.note ?? '';
	}

	function onNoteInput(itemId: string, value: string) {
		noteDraftByItemId = {
			...noteDraftByItemId,
			[itemId]: value
		};
	}

	function saveInlineNote(item: RoomDashboardItem) {
		const draft = (noteDraftByItemId[item.id] ?? item.note ?? '').trim();
		if (draft === (item.note || '').trim()) {
			return;
		}
		dispatch('editNote', {
			itemId: item.id,
			note: draft
		});
	}

	function onNoteKeydown(event: KeyboardEvent, item: RoomDashboardItem) {
		if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
			event.preventDefault();
			saveInlineNote(item);
		}
	}

	function sectionItems(kind: RoomDashboardItem['kind']) {
		return groupedPinnedItems.filter((item) => item.kind === kind);
	}

	function parseTimestampValue(value: unknown): number | null {
		if (typeof value === 'number' && Number.isFinite(value) && value > 0) {
			return Math.trunc(value);
		}
		if (typeof value === 'string') {
			const numeric = Number(value.trim());
			if (Number.isFinite(numeric) && numeric > 0) {
				return Math.trunc(numeric);
			}
		}
		return null;
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

	function normalizeKind(value: unknown): RoomDashboardItem['kind'] {
		const normalized = toStringValue(value).trim().toLowerCase();
		if (normalized === 'task' || normalized === 'note') {
			return normalized;
		}
		return 'message';
	}

	function parseOrganizedDashboardItem(source: unknown): RoomDashboardItem | null {
		if (!source || typeof source !== 'object' || Array.isArray(source)) {
			return null;
		}
		const record = source as Record<string, unknown>;
		const id = toStringValue(record.id).trim();
		const messageId = toStringValue(record.messageId).trim() || id;
		const itemRoomId = toStringValue(record.roomId).trim() || roomId;
		if (!id || !messageId || !itemRoomId) {
			return null;
		}
		return {
			id,
			roomId: itemRoomId,
			messageId,
			kind: normalizeKind(record.kind),
			senderId: toStringValue(record.senderId).trim(),
			senderName: toStringValue(record.senderName).trim() || 'User',
			pinnedByUserId: toStringValue(record.pinnedByUserId).trim(),
			pinnedByName: toStringValue(record.pinnedByName).trim() || 'User',
			originalCreatedAt: parseTimestampValue(record.originalCreatedAt) || Date.now(),
			pinnedAt: parseTimestampValue(record.pinnedAt) || Date.now(),
			messageText: toStringValue(record.messageText).trim(),
			mediaUrl: toStringValue(record.mediaUrl).trim(),
			mediaType: toStringValue(record.mediaType).trim(),
			fileName: toStringValue(record.fileName).trim(),
			note: toStringValue(record.note).trim(),
			beaconAt: parseTimestampValue(record.beaconAt),
			beaconLabel: toStringValue(record.beaconLabel).trim(),
			beaconData:
				record.beaconData && typeof record.beaconData === 'object' && !Array.isArray(record.beaconData)
					? { ...(record.beaconData as Record<string, unknown>) }
					: null,
			taskTitle: toStringValue(record.taskTitle).trim(),
			topic: toStringValue(record.topic).trim()
		};
	}

	function parseOrganizedSection(value: unknown) {
		if (!Array.isArray(value)) {
			return [] as RoomDashboardItem[];
		}
		return value
			.map((entry) => parseOrganizedDashboardItem(entry))
			.filter((entry): entry is RoomDashboardItem => Boolean(entry))
			.filter((entry) => entry.roomId === roomId);
	}

	function parseOrganizeResponse(payload: unknown): RoomDashboardOrganizeSections | null {
		if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
			return null;
		}
		const record = payload as Record<string, unknown>;
		return {
			priority: parseOrganizedSection(record.priority),
			pinnedItems: parseOrganizedSection(record.pinnedItems),
			expired: parseOrganizedSection(record.expired)
		};
	}

	async function onAIOrganizeClick() {
		if (isOrganizing || !roomId) {
			return;
		}
		if (!currentUserId.trim()) {
			dispatch('aiOrganizeError', { message: 'User context is missing. Rejoin room and try again.' });
			return;
		}
		const payload: RoomDashboardOrganizePayload = {
			items: scopedItems
		};
		if (payload.items.length === 0) {
			dispatch('aiOrganizeError', { message: 'No dashboard items to organize yet.' });
			return;
		}

		isOrganizing = true;
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(roomId)}/ai-organize`,
				{
					method: 'POST',
					headers: {
						'Content-Type': 'application/json',
						'X-User-Id': currentUserId || ''
					},
					body: JSON.stringify(payload)
				}
			);
			const body = (await response
				.json()
				.catch(() => ({}))) as Record<string, unknown>;
			if (!response.ok) {
				const message = toStringValue(body.error).trim() || 'Failed to organize dashboard';
				throw new Error(message);
			}
			const parsed = parseOrganizeResponse(body);
			if (!parsed) {
				throw new Error('AI organize returned an invalid response');
			}
			dispatch('aiOrganizePreview', parsed);
		} catch (error) {
			dispatch('aiOrganizeError', {
				message: error instanceof Error ? error.message : 'Failed to organize dashboard'
			});
		} finally {
			isOrganizing = false;
		}
	}

	function toggleAddMenu() {
		addMenuOpen = !addMenuOpen;
	}

	function onDocumentPointerDown(event: PointerEvent) {
		if (!addMenuOpen || !addMenuRef) {
			return;
		}
		const target = event.target;
		if (target instanceof Node && addMenuRef.contains(target)) {
			return;
		}
		addMenuOpen = false;
	}

	function onAddMenuKeydown(event: KeyboardEvent) {
		if (event.key === 'Escape' && addMenuOpen) {
			event.preventDefault();
			addMenuOpen = false;
		}
	}

	function onAddMenuSelect(kind: DashboardAddItemKind) {
		addMenuOpen = false;
		dispatch('addItemRequest', { kind });
	}
</script>

<section class="room-dashboard {isDarkMode ? 'theme-dark' : ''}">
	<header class="dashboard-header">
		<div class="dashboard-title-wrap">
			<h3>Room Dashboard</h3>
			<p>Snapshot of priorities, pinned context, and expired beacons.</p>
		</div>
		<div class="header-actions">
			<div class="add-actions" bind:this={addMenuRef}>
				<button
					type="button"
					class="add-action-btn"
					aria-haspopup="menu"
					aria-expanded={addMenuOpen}
					on:click={toggleAddMenu}
					on:keydown={onAddMenuKeydown}
				>
					+ Add
				</button>
				{#if addMenuOpen}
					<div class="add-menu" role="menu" aria-label="Add item to room dashboard">
						<button type="button" role="menuitem" on:click={() => onAddMenuSelect('note')}>
							Note
						</button>
						<button type="button" role="menuitem" on:click={() => onAddMenuSelect('beacon')}>
							Schedule Beacon
						</button>
						<button type="button" role="menuitem" on:click={() => onAddMenuSelect('task')}>
							Create Task
						</button>
					</div>
				{/if}
			</div>
			<button
				type="button"
				class="ai-organize-btn"
				disabled={isOrganizing || scopedItems.length === 0}
				aria-busy={isOrganizing}
				on:click={onAIOrganizeClick}
			>
				{#if isOrganizing}
					<span class="btn-spinner" aria-hidden="true"></span>
					Organizing...
				{:else}
					AI Organize
				{/if}
			</button>
		</div>
	</header>

	<section class="dashboard-section">
		<div class="section-head">
			<h4>Priority</h4>
			<span>{priorityItems.length}</span>
		</div>
		{#if priorityItems.length === 0}
			<div class="empty-state">No upcoming beacons/tasks.</div>
		{:else}
			<div class="card-list">
				{#each priorityItems as item (item.id)}
					<article class="dashboard-card">
						<div class="card-top">
							<strong>{formatKindLabel(item.kind)}</strong>
							<time>{formatDateTime(item.beaconAt)}</time>
						</div>
						<p>{item.taskTitle || item.messageText || 'No content'}</p>
						{#if item.topic}
							<div class="topic-chip">Topic: {item.topic}</div>
						{/if}
						{#if item.note}
							<div class="note-chip">Note: {item.note}</div>
						{/if}
					</article>
				{/each}
			</div>
		{/if}
	</section>

	<section class="dashboard-section">
		<div class="section-head">
			<h4>Pinned Items</h4>
			<span>{groupedPinnedItems.length}</span>
		</div>
		{#if groupedPinnedItems.length === 0}
			<div class="empty-state">No pinned items yet.</div>
		{:else}
			<div class="pinned-groups">
				<div class="group-block">
					<h5>Messages</h5>
					{#if sectionItems('message').length === 0}
						<div class="empty-state subtle">No pinned messages.</div>
					{:else}
						{#each sectionItems('message') as item (item.id)}
							<article class="dashboard-card">
								<div class="card-top">
									<strong>{item.senderName || 'User'}</strong>
									<time>{formatDateTime(item.originalCreatedAt)}</time>
								</div>
								<p>{item.messageText || 'No text content'}</p>
								{#if item.topic}
									<div class="topic-chip">Topic: {item.topic}</div>
								{/if}
								{#if item.mediaUrl}
									{#if isImageItem(item)}
										<img src={item.mediaUrl} alt={item.fileName || 'Pinned preview'} />
									{:else}
										<a href={item.mediaUrl} target="_blank" rel="noreferrer">
											Open attachment{item.fileName ? ` (${item.fileName})` : ''}
										</a>
									{/if}
								{/if}
								<label class="note-editor">
									<span>Attached note</span>
									<textarea
										value={resolveDraftNote(item)}
										on:input={(event) =>
											onNoteInput(item.id, (event.currentTarget as HTMLTextAreaElement).value)}
										on:blur={() => saveInlineNote(item)}
										on:keydown={(event) => onNoteKeydown(event, item)}
										placeholder="Add note (optional)"
									></textarea>
								</label>
							</article>
						{/each}
					{/if}
				</div>
				<div class="group-block">
					<h5>Notes</h5>
					{#if sectionItems('note').length === 0}
						<div class="empty-state subtle">No pinned notes.</div>
					{:else}
						{#each sectionItems('note') as item (item.id)}
							<article class="dashboard-card">
								<div class="card-top">
									<strong>{item.senderName || 'User'}</strong>
									<time>{formatDateTime(item.originalCreatedAt)}</time>
								</div>
								<p>{item.messageText || 'No note content'}</p>
								{#if item.topic}
									<div class="topic-chip">Topic: {item.topic}</div>
								{/if}
								<label class="note-editor">
									<span>Attached note</span>
									<textarea
										value={resolveDraftNote(item)}
										on:input={(event) =>
											onNoteInput(item.id, (event.currentTarget as HTMLTextAreaElement).value)}
										on:blur={() => saveInlineNote(item)}
										on:keydown={(event) => onNoteKeydown(event, item)}
										placeholder="Add note (optional)"
									></textarea>
								</label>
							</article>
						{/each}
					{/if}
				</div>
				<div class="group-block">
					<h5>Tasks</h5>
					{#if sectionItems('task').length === 0}
						<div class="empty-state subtle">No pinned tasks.</div>
					{:else}
						{#each sectionItems('task') as item (item.id)}
							<article class="dashboard-card">
								<div class="card-top">
									<strong>{item.taskTitle || 'Task'}</strong>
									<time>{formatDateTime(item.beaconAt)}</time>
								</div>
								<p>{item.messageText || 'No task details'}</p>
								{#if item.topic}
									<div class="topic-chip">Topic: {item.topic}</div>
								{/if}
								<label class="note-editor">
									<span>Attached note</span>
									<textarea
										value={resolveDraftNote(item)}
										on:input={(event) =>
											onNoteInput(item.id, (event.currentTarget as HTMLTextAreaElement).value)}
										on:blur={() => saveInlineNote(item)}
										on:keydown={(event) => onNoteKeydown(event, item)}
										placeholder="Add note (optional)"
									></textarea>
								</label>
							</article>
						{/each}
					{/if}
				</div>
			</div>
		{/if}
	</section>

	<section class="dashboard-section expired">
		<div class="section-head">
			<h4>Expired</h4>
			<span>{expiredItems.length}</span>
		</div>
		{#if expiredItems.length === 0}
			<div class="empty-state">Nothing expired.</div>
		{:else}
			<div class="card-list">
				{#each expiredItems as item (item.id)}
					<article class="dashboard-card">
						<div class="card-top">
							<strong>{formatKindLabel(item.kind)}</strong>
							<time>{formatDateTime(item.beaconAt)}</time>
						</div>
						<p>{item.taskTitle || item.messageText || 'No content'}</p>
						{#if item.topic}
							<div class="topic-chip">Topic: {item.topic}</div>
						{/if}
					</article>
				{/each}
			</div>
		{/if}
	</section>
</section>

<style>
	.room-dashboard {
		flex: 1;
		min-height: 0;
		overflow: auto;
		display: grid;
		align-content: start;
		gap: 0.7rem;
		padding: 0.75rem;
		background: linear-gradient(180deg, #f8fbff 0%, #eef3fa 100%);
	}

	.room-dashboard.theme-dark {
		background: linear-gradient(180deg, #111620 0%, #0b1220 100%);
	}

	.dashboard-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 0.8rem;
	}

	.header-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
	}

	.add-actions {
		position: relative;
	}

	.dashboard-title-wrap h3 {
		margin: 0;
		font-size: 0.96rem;
		color: #1f2f47;
	}

	.dashboard-title-wrap p {
		margin: 0.2rem 0 0;
		font-size: 0.76rem;
		color: #5e6f89;
	}

	.room-dashboard.theme-dark .dashboard-title-wrap h3 {
		color: #e8edf8;
	}

	.room-dashboard.theme-dark .dashboard-title-wrap p {
		color: #a4b6d2;
	}

	.add-action-btn {
		border: 1px solid #b8c8dd;
		background: rgba(245, 250, 255, 0.9);
		color: #20395a;
		border-radius: 0.55rem;
		font-size: 0.73rem;
		font-weight: 700;
		padding: 0.36rem 0.66rem;
		cursor: pointer;
	}

	.add-menu {
		position: absolute;
		top: calc(100% + 0.3rem);
		right: 0;
		min-width: 11rem;
		display: grid;
		gap: 0.18rem;
		padding: 0.28rem;
		border: 1px solid rgba(177, 196, 220, 0.9);
		border-radius: 0.62rem;
		background: rgba(250, 253, 255, 0.98);
		box-shadow: 0 12px 30px rgba(16, 36, 66, 0.14);
		z-index: 20;
	}

	.add-menu button {
		border: 0;
		background: transparent;
		color: #2e4465;
		font-size: 0.72rem;
		font-weight: 600;
		text-align: left;
		padding: 0.36rem 0.44rem;
		border-radius: 0.42rem;
		cursor: pointer;
	}

	.add-menu button:hover,
	.add-menu button:focus-visible {
		background: rgba(223, 235, 251, 0.92);
		outline: none;
	}

	.room-dashboard.theme-dark .add-action-btn {
		border-color: rgba(89, 115, 151, 0.88);
		background: rgba(16, 27, 45, 0.9);
		color: #dce9fb;
	}

	.room-dashboard.theme-dark .add-menu {
		border-color: rgba(67, 92, 126, 0.9);
		background: rgba(12, 22, 38, 0.97);
		box-shadow: 0 14px 34px rgba(0, 0, 0, 0.48);
	}

	.room-dashboard.theme-dark .add-menu button {
		color: #d3e2f8;
	}

	.room-dashboard.theme-dark .add-menu button:hover,
	.room-dashboard.theme-dark .add-menu button:focus-visible {
		background: rgba(39, 58, 88, 0.9);
	}

	.ai-organize-btn {
		border: 1px solid #b8c8dd;
		background: rgba(245, 250, 255, 0.9);
		color: #243a5a;
		border-radius: 0.55rem;
		font-size: 0.73rem;
		font-weight: 700;
		padding: 0.36rem 0.62rem;
		cursor: pointer;
		display: inline-flex;
		align-items: center;
		gap: 0.38rem;
	}

	.ai-organize-btn:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	.btn-spinner {
		width: 0.72rem;
		height: 0.72rem;
		border-radius: 999px;
		border: 2px solid rgba(58, 90, 129, 0.25);
		border-top-color: rgba(58, 90, 129, 0.95);
		animation: dashboard-spin 0.8s linear infinite;
	}

	.dashboard-section {
		border: 1px solid rgba(179, 195, 216, 0.78);
		border-radius: 0.8rem;
		background: rgba(250, 253, 255, 0.92);
		padding: 0.62rem;
		display: grid;
		gap: 0.58rem;
	}

	.dashboard-section.expired {
		margin-top: 0.3rem;
	}

	.room-dashboard.theme-dark .dashboard-section {
		border-color: rgba(62, 82, 111, 0.85);
		background: rgba(12, 20, 35, 0.88);
	}

	.section-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}

	.section-head h4 {
		margin: 0;
		font-size: 0.8rem;
		color: #2d3f5d;
	}

	.section-head span {
		font-size: 0.68rem;
		font-weight: 700;
		color: #4f668a;
	}

	.room-dashboard.theme-dark .section-head h4 {
		color: #dce8fa;
	}

	.room-dashboard.theme-dark .section-head span {
		color: #9bb6de;
	}

	.card-list,
	.pinned-groups {
		display: grid;
		gap: 0.5rem;
	}

	.group-block {
		display: grid;
		gap: 0.45rem;
	}

	.group-block h5 {
		margin: 0;
		font-size: 0.72rem;
		text-transform: uppercase;
		letter-spacing: 0.03em;
		color: #4d6080;
	}

	.room-dashboard.theme-dark .group-block h5 {
		color: #a6bcdd;
	}

	.dashboard-card {
		border: 1px solid rgba(187, 201, 220, 0.86);
		border-radius: 0.65rem;
		padding: 0.5rem;
		display: grid;
		gap: 0.34rem;
		background: rgba(255, 255, 255, 0.88);
	}

	.room-dashboard.theme-dark .dashboard-card {
		border-color: rgba(72, 90, 118, 0.88);
		background: rgba(18, 27, 43, 0.86);
	}

	.card-top {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	.card-top strong {
		font-size: 0.76rem;
		color: #314564;
	}

	.card-top time {
		font-size: 0.66rem;
		color: #6a7f9f;
	}

	.room-dashboard.theme-dark .card-top strong {
		color: #d9e6fb;
	}

	.room-dashboard.theme-dark .card-top time {
		color: #9ab2d8;
	}

	.dashboard-card p {
		margin: 0;
		font-size: 0.76rem;
		color: #354b6d;
		line-height: 1.36;
	}

	.room-dashboard.theme-dark .dashboard-card p {
		color: #d7e5fb;
	}

	.dashboard-card img {
		width: 100%;
		max-height: 170px;
		object-fit: cover;
		border-radius: 0.5rem;
		border: 1px solid rgba(188, 203, 222, 0.85);
	}

	.note-chip {
		font-size: 0.68rem;
		color: #415777;
		background: rgba(233, 241, 250, 0.95);
		border-radius: 0.42rem;
		padding: 0.26rem 0.34rem;
	}

	.topic-chip {
		font-size: 0.66rem;
		color: #3d5f87;
		background: rgba(228, 238, 252, 0.95);
		border-radius: 0.42rem;
		padding: 0.22rem 0.34rem;
	}

	.room-dashboard.theme-dark .topic-chip {
		color: #bdd4f4;
		background: rgba(31, 45, 71, 0.92);
	}

	.note-editor {
		display: grid;
		gap: 0.2rem;
	}

	.note-editor span {
		font-size: 0.65rem;
		font-weight: 700;
		color: #577195;
	}

	.note-editor textarea {
		width: 100%;
		min-height: 44px;
		border: 1px solid rgba(173, 190, 213, 0.85);
		border-radius: 0.44rem;
		padding: 0.34rem;
		font-size: 0.72rem;
		background: rgba(252, 254, 255, 0.96);
		color: #2a405f;
		resize: vertical;
	}

	.empty-state {
		font-size: 0.73rem;
		color: #667b99;
		padding: 0.3rem 0.1rem;
	}

	.empty-state.subtle {
		font-size: 0.69rem;
	}

	.room-dashboard.theme-dark .empty-state {
		color: #a3bcdd;
	}

	@media (max-width: 900px) {
		.room-dashboard {
			padding: 0.62rem;
		}
	}

	@keyframes dashboard-spin {
		to {
			transform: rotate(360deg);
		}
	}
</style>
