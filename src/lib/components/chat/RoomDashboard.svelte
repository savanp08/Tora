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
	type DashboardAddItemRequest =
		| { kind: 'note'; text: string }
		| { kind: 'beacon'; text: string; beaconAt: number }
		| { kind: 'task'; title: string; details: string };

	const dispatch = createEventDispatcher<{
		close: void;
		editNote: { itemId: string; note: string };
		addItemRequest: DashboardAddItemRequest;
		aiOrganizePreview: RoomDashboardOrganizeSections;
		aiOrganizeError: { message: string };
	}>();
	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';

	const KIND_ORDER: Record<RoomDashboardItem['kind'], number> = {
		message: 0,
		note: 1,
		task: 2
	};
	const BEACON_MAX_SCHEDULE_DAYS = 15;
	const BEACON_DEFAULT_OFFSET_MINUTES = 10;
	const BEACON_TIME_STEP_MINUTES = 5;
	const BEACON_DAY_MS = 24 * 60 * 60 * 1000;
	const BEACON_QUICK_OFFSETS = [10, 30, 60] as const;

	type BeaconDayOption = {
		value: string;
		label: string;
		dateLabel: string;
	};

	let noteDraftByItemId: Record<string, string> = {};
	let isOrganizing = false;
	let nowMs = Date.now();
	let ticker: ReturnType<typeof setInterval> | null = null;
	let addMenuOpen = false;
	let addMenuRef: HTMLElement | null = null;
	let addComposerKind: DashboardAddItemKind | '' = '';
	let addComposerError = '';
	let addNoteDraft = '';
	let addBeaconDraft = '';
	let addBeaconDateDraft = '';
	let addBeaconTimeDraft = '';
	let addTaskTitleDraft = '';
	let addTaskDetailDraft = '';
	let beaconMinDateValue = '';
	let beaconMaxDateValue = '';
	let beaconDayOptions: BeaconDayOption[] = [];
	let beaconPreviewTimestamp = 0;

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
	$: beaconMinDateValue = toLocalDateInputValue(nowMs);
	$: beaconMaxDateValue = toLocalDateInputValue(nowMs + BEACON_MAX_SCHEDULE_DAYS * BEACON_DAY_MS);
	$: beaconDayOptions = buildBeaconDayOptions(beaconMinDateValue, beaconMaxDateValue);
	$: beaconPreviewTimestamp = composeBeaconTimestamp(addBeaconDateDraft, addBeaconTimeDraft);

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

	function padTwo(value: number) {
		return `${value}`.padStart(2, '0');
	}

	function toLocalDateInputValue(timestamp: number) {
		const value = new Date(timestamp);
		const year = value.getFullYear();
		const month = padTwo(value.getMonth() + 1);
		const day = padTwo(value.getDate());
		return `${year}-${month}-${day}`;
	}

	function toLocalTimeInputValue(timestamp: number) {
		const value = new Date(timestamp);
		return `${padTwo(value.getHours())}:${padTwo(value.getMinutes())}`;
	}

	function roundTimestampToStep(timestamp: number, stepMinutes: number) {
		const stepMs = Math.max(1, stepMinutes) * 60 * 1000;
		return Math.ceil(timestamp / stepMs) * stepMs;
	}

	function parseDateOnlyInput(value: string) {
		if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) {
			return 0;
		}
		const parsed = Date.parse(`${value}T00:00`);
		if (!Number.isFinite(parsed) || parsed <= 0) {
			return 0;
		}
		return Math.trunc(parsed);
	}

	function normalizeBeaconDateInput(value: string) {
		const trimmed = value.trim();
		if (!trimmed) {
			return '';
		}
		const parsed = parseDateOnlyInput(trimmed);
		if (!parsed) {
			return '';
		}
		if (beaconMinDateValue && trimmed < beaconMinDateValue) {
			return beaconMinDateValue;
		}
		if (beaconMaxDateValue && trimmed > beaconMaxDateValue) {
			return beaconMaxDateValue;
		}
		return trimmed;
	}

	function composeBeaconTimestamp(dateValue: string, timeValue: string) {
		const normalizedDate = normalizeBeaconDateInput(dateValue);
		const normalizedTime = timeValue.trim();
		if (!normalizedDate || !/^\d{2}:\d{2}$/.test(normalizedTime)) {
			return 0;
		}
		const parsed = Date.parse(`${normalizedDate}T${normalizedTime}`);
		if (!Number.isFinite(parsed) || parsed <= 0) {
			return 0;
		}
		return Math.trunc(parsed);
	}

	function setBeaconDraftFromTimestamp(timestamp: number) {
		const rounded = roundTimestampToStep(timestamp, BEACON_TIME_STEP_MINUTES);
		addBeaconDateDraft = normalizeBeaconDateInput(toLocalDateInputValue(rounded));
		addBeaconTimeDraft = toLocalTimeInputValue(rounded);
	}

	function formatBeaconDayLabel(offset: number) {
		if (offset === 0) {
			return 'Today';
		}
		if (offset === 1) {
			return 'Tomorrow';
		}
		return `Day ${offset + 1}`;
	}

	function formatBeaconDayDate(value: Date) {
		return value.toLocaleDateString([], {
			weekday: 'short',
			month: 'short',
			day: 'numeric'
		});
	}

	function buildBeaconDayOptions(minDateValue: string, maxDateValue: string) {
		const minDateTimestamp = parseDateOnlyInput(minDateValue);
		const maxDateTimestamp = parseDateOnlyInput(maxDateValue);
		if (!minDateTimestamp || !maxDateTimestamp || maxDateTimestamp < minDateTimestamp) {
			return [] as BeaconDayOption[];
		}
		const dayCount = Math.max(0, Math.round((maxDateTimestamp - minDateTimestamp) / BEACON_DAY_MS));
		const options: BeaconDayOption[] = [];
		for (let offset = 0; offset <= dayCount; offset += 1) {
			const timestamp = minDateTimestamp + offset * BEACON_DAY_MS;
			const value = toLocalDateInputValue(timestamp);
			options.push({
				value,
				label: formatBeaconDayLabel(offset),
				dateLabel: formatBeaconDayDate(new Date(timestamp))
			});
		}
		return options;
	}

	function selectBeaconDate(value: string) {
		const normalized = normalizeBeaconDateInput(value);
		if (!normalized) {
			return;
		}
		addBeaconDateDraft = normalized;
	}

	function applyBeaconQuickOffset(minutes: number) {
		const safeMinutes = Math.max(1, Math.trunc(minutes));
		setBeaconDraftFromTimestamp(Date.now() + safeMinutes * 60 * 1000);
	}

	function closeAddComposer() {
		addComposerKind = '';
		addComposerError = '';
	}

	function resetAddComposerDrafts() {
		addNoteDraft = '';
		addBeaconDraft = '';
		setBeaconDraftFromTimestamp(Date.now() + BEACON_DEFAULT_OFFSET_MINUTES * 60 * 1000);
		addTaskTitleDraft = '';
		addTaskDetailDraft = '';
	}

	function openAddComposer(kind: DashboardAddItemKind) {
		addComposerKind = kind;
		addComposerError = '';
		if (kind === 'beacon' && (!addBeaconDateDraft || !addBeaconTimeDraft)) {
			setBeaconDraftFromTimestamp(Date.now() + BEACON_DEFAULT_OFFSET_MINUTES * 60 * 1000);
		}
	}

	function submitAddComposer() {
		addComposerError = '';
		if (addComposerKind === 'note') {
			const text = addNoteDraft.trim();
			if (!text) {
				addComposerError = 'Note text is required.';
				return;
			}
			dispatch('addItemRequest', { kind: 'note', text });
			closeAddComposer();
			resetAddComposerDrafts();
			return;
		}
		if (addComposerKind === 'beacon') {
			const text = addBeaconDraft.trim();
			const beaconAt = composeBeaconTimestamp(addBeaconDateDraft, addBeaconTimeDraft);
			if (!text) {
				addComposerError = 'Beacon text is required.';
				return;
			}
			if (!addBeaconDateDraft || !addBeaconTimeDraft || !beaconAt) {
				addComposerError = 'Choose a valid day and time.';
				return;
			}
			if (addBeaconDateDraft < beaconMinDateValue || addBeaconDateDraft > beaconMaxDateValue) {
				addComposerError = `Beacon can only be scheduled within the next ${BEACON_MAX_SCHEDULE_DAYS} days.`;
				return;
			}
			if (beaconAt <= Date.now()) {
				addComposerError = 'Choose a future date and time.';
				return;
			}
			dispatch('addItemRequest', { kind: 'beacon', text, beaconAt });
			closeAddComposer();
			resetAddComposerDrafts();
			return;
		}
		if (addComposerKind === 'task') {
			const title = addTaskTitleDraft.trim();
			const details = addTaskDetailDraft.trim();
			if (!title) {
				addComposerError = 'Task title is required.';
				return;
			}
			dispatch('addItemRequest', { kind: 'task', title, details });
			closeAddComposer();
			resetAddComposerDrafts();
		}
	}

	function onAddMenuSelect(kind: DashboardAddItemKind) {
		addMenuOpen = false;
		openAddComposer(kind);
	}

	onMount(() => {
		resetAddComposerDrafts();
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
			<button
				type="button"
				class="dashboard-close-btn"
				aria-label="Close dashboard"
				title="Close dashboard"
				on:click={() => dispatch('close')}
			>
				<span aria-hidden="true">×</span>
			</button>
		</div>
	</header>

	{#if addComposerKind}
		<section class="dashboard-add-composer" aria-label="Add dashboard item">
			<header class="composer-head">
				<strong>
					{addComposerKind === 'note'
						? 'Add Note'
						: addComposerKind === 'beacon'
							? 'Schedule Beacon'
							: 'Create Task'}
				</strong>
				<button type="button" class="composer-close-btn" on:click={closeAddComposer}>Cancel</button>
			</header>

			{#if addComposerKind === 'note'}
				<label class="composer-field">
					<span>Note text</span>
					<textarea
						bind:value={addNoteDraft}
						placeholder="Write note text"
						maxlength="600"
					></textarea>
				</label>
			{:else if addComposerKind === 'beacon'}
				<label class="composer-field">
					<span>Beacon message</span>
					<textarea
						bind:value={addBeaconDraft}
						placeholder="Beacon text"
						maxlength="500"
					></textarea>
				</label>
				<div class="beacon-scheduler">
					<div class="composer-field">
						<span>Quick day picker</span>
						<div class="beacon-day-scroll" role="listbox" aria-label="Beacon day picker">
							{#each beaconDayOptions as option (option.value)}
								<button
									type="button"
									class="beacon-day-chip"
									class:is-active={addBeaconDateDraft === option.value}
									on:click={() => selectBeaconDate(option.value)}
								>
									<strong>{option.label}</strong>
									<small>{option.dateLabel}</small>
								</button>
							{/each}
						</div>
					</div>

					<div class="beacon-datetime-grid">
						<label class="composer-field">
							<span>Date</span>
							<div class="beacon-input-wrap">
								<svg viewBox="0 0 24 24" aria-hidden="true">
									<rect x="4.5" y="5.5" width="15" height="14" rx="2"></rect>
									<path d="M8 3.8v3.2M16 3.8v3.2M4.5 9.2h15"></path>
								</svg>
								<input
									class="beacon-datetime-input"
									type="date"
									bind:value={addBeaconDateDraft}
									min={beaconMinDateValue}
									max={beaconMaxDateValue}
								/>
							</div>
						</label>

						<label class="composer-field">
							<span>Time</span>
							<div class="beacon-input-wrap">
								<svg viewBox="0 0 24 24" aria-hidden="true">
									<circle cx="12" cy="12" r="8.5"></circle>
									<path d="M12 7.6V12l3 1.8"></path>
								</svg>
								<input
									class="beacon-datetime-input"
									type="time"
									bind:value={addBeaconTimeDraft}
									step={BEACON_TIME_STEP_MINUTES * 60}
								/>
							</div>
						</label>
					</div>

					<div class="beacon-quick-time">
						<span>Quick set</span>
						<div class="beacon-quick-time-actions">
							{#each BEACON_QUICK_OFFSETS as offsetMinutes (offsetMinutes)}
								<button type="button" on:click={() => applyBeaconQuickOffset(offsetMinutes)}>
									+{offsetMinutes}m
								</button>
							{/each}
						</div>
					</div>
				</div>
				{#if addBeaconDraft.trim()}
					<div class="beacon-preview">
						<div class="preview-pill">Beacon Preview</div>
						<div class="beacon-preview-time">
							<svg viewBox="0 0 24 24" aria-hidden="true">
								<circle cx="12" cy="12" r="8.5"></circle>
								<path d="M12 7.6V12l3 1.8"></path>
							</svg>
							<span>{formatDateTime(beaconPreviewTimestamp || null)}</span>
						</div>
						<p>{addBeaconDraft.trim()}</p>
					</div>
				{/if}
			{:else}
				<label class="composer-field">
					<span>Task title</span>
					<input
						type="text"
						bind:value={addTaskTitleDraft}
						placeholder="Task title"
						maxlength="120"
					/>
				</label>
				<label class="composer-field">
					<span>Task details (optional)</span>
					<textarea
						bind:value={addTaskDetailDraft}
						placeholder="Task details"
						maxlength="700"
					></textarea>
				</label>
			{/if}

			{#if addComposerError}
				<div class="composer-error">{addComposerError}</div>
			{/if}
			<div class="composer-actions">
				<button type="button" class="composer-submit-btn" on:click={submitAddComposer}>
					{addComposerKind === 'note'
						? 'Add Note'
						: addComposerKind === 'beacon'
							? 'Schedule Beacon'
							: 'Create Task'}
				</button>
			</div>
		</section>
	{/if}

	<section class="dashboard-summary-strip" aria-label="Room dashboard summary">
		<article class="summary-card">
			<span>Priority Queue</span>
			<strong>{priorityItems.length}</strong>
		</article>
		<article class="summary-card">
			<span>Pinned Context</span>
			<strong>{groupedPinnedItems.length}</strong>
		</article>
		<article class="summary-card">
			<span>Expired Beacons</span>
			<strong>{expiredItems.length}</strong>
		</article>
	</section>

	<div class="dashboard-board">
		<section class="dashboard-section section-priority">
			<div class="section-head">
				<h4>Priority</h4>
				<span class="section-count">{priorityItems.length}</span>
			</div>
			{#if priorityItems.length === 0}
				<div class="empty-state">No upcoming beacons/tasks.</div>
			{:else}
				<div class="card-list">
					{#each priorityItems as item (item.id)}
						<article class="dashboard-card kind-{item.kind}">
							<div class="card-top">
								<strong>{formatKindLabel(item.kind)}</strong>
								<time>{formatDateTime(item.beaconAt)}</time>
							</div>
							<p>{item.taskTitle || item.messageText || 'No content'}</p>
							{#if item.topic}
								<div class="topic-chip chip">Topic: {item.topic}</div>
							{/if}
							{#if item.note}
								<div class="note-chip chip">Note: {item.note}</div>
							{/if}
						</article>
					{/each}
				</div>
			{/if}
		</section>

		<section class="dashboard-section section-expired">
			<div class="section-head">
				<h4>Expired</h4>
				<span class="section-count">{expiredItems.length}</span>
			</div>
			{#if expiredItems.length === 0}
				<div class="empty-state">Nothing expired.</div>
			{:else}
				<div class="card-list">
					{#each expiredItems as item (item.id)}
						<article class="dashboard-card kind-{item.kind}">
							<div class="card-top">
								<strong>{formatKindLabel(item.kind)}</strong>
								<time>{formatDateTime(item.beaconAt)}</time>
							</div>
							<p>{item.taskTitle || item.messageText || 'No content'}</p>
							{#if item.topic}
								<div class="topic-chip chip">Topic: {item.topic}</div>
							{/if}
						</article>
					{/each}
				</div>
			{/if}
		</section>

		<section class="dashboard-section section-pinned">
			<div class="section-head">
				<h4>Pinned Items</h4>
				<span class="section-count">{groupedPinnedItems.length}</span>
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
								<article class="dashboard-card kind-message">
									<div class="card-top">
										<strong>{item.senderName || 'User'}</strong>
										<time>{formatDateTime(item.originalCreatedAt)}</time>
									</div>
									<p>{item.messageText || 'No text content'}</p>
									{#if item.topic}
										<div class="topic-chip chip">Topic: {item.topic}</div>
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
								<article class="dashboard-card kind-note">
									<div class="card-top">
										<strong>{item.senderName || 'User'}</strong>
										<time>{formatDateTime(item.originalCreatedAt)}</time>
									</div>
									<p>{item.messageText || 'No note content'}</p>
									{#if item.topic}
										<div class="topic-chip chip">Topic: {item.topic}</div>
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
								<article class="dashboard-card kind-task">
									<div class="card-top">
										<strong>{item.taskTitle || 'Task'}</strong>
										<time>{formatDateTime(item.beaconAt)}</time>
									</div>
									<p>{item.messageText || 'No task details'}</p>
									{#if item.topic}
										<div class="topic-chip chip">Topic: {item.topic}</div>
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
	</div>
</section>

	<style>
		.room-dashboard {
			--dash-bg: #f4f5f7;
			--dash-header-bg: #ffffff;
			--dash-section-bg: #ebecf0;
			--dash-group-bg: #f1f2f4;
			--dash-card-bg: #ffffff;
			--dash-input-bg: #ffffff;
			--dash-menu-bg: #ffffff;
			--dash-border: #d0d4dc;
			--dash-border-strong: #b9c0cc;
			--dash-text: #172b4d;
			--dash-muted: #5e6c84;
			--dash-soft: #6b778c;
			--dash-accent: #7a5a2f;
			--dash-accent-soft: rgba(122, 90, 47, 0.16);
			--dash-chip-bg: #f2ebe1;
			--dash-chip-text: #614627;
			--dash-focus-ring: 0 0 0 3px rgba(122, 90, 47, 0.24);
			--dash-primary-btn:
				linear-gradient(180deg, rgba(130, 92, 48, 0.98) 0%, rgba(96, 66, 35, 1) 100%);
			--dash-primary-btn-hover:
				linear-gradient(180deg, rgba(142, 104, 58, 0.99) 0%, rgba(108, 76, 43, 1) 100%);
			--dash-primary-btn-text: #f7f5ef;
			--dash-danger: #c33f5e;
			--dash-shadow-soft: 0 1px 2px rgba(9, 30, 66, 0.18);
			--dash-shadow-strong: 0 8px 18px rgba(9, 30, 66, 0.22);

			flex: 1;
			min-height: 0;
			overflow: auto;
			display: grid;
			align-content: start;
			gap: 0.72rem;
			padding: 0.8rem;
			background: var(--dash-bg);
			color: var(--dash-text);
			scrollbar-gutter: stable;
		}

		.room-dashboard.theme-dark {
			--dash-bg: #1d2125;
			--dash-header-bg: #272521;
			--dash-section-bg: #312e29;
			--dash-group-bg: #38342f;
			--dash-card-bg: #403a34;
			--dash-input-bg: #2a2722;
			--dash-menu-bg: #35312c;
			--dash-border: #4b463f;
			--dash-border-strong: #655f56;
			--dash-text: #ece6d9;
			--dash-muted: #b8b0a2;
			--dash-soft: #9e9588;
			--dash-accent: #d2a96a;
			--dash-accent-soft: rgba(210, 169, 106, 0.2);
			--dash-chip-bg: rgba(210, 169, 106, 0.16);
			--dash-chip-text: #f3dfbc;
			--dash-focus-ring: 0 0 0 3px rgba(210, 169, 106, 0.28);
			--dash-primary-btn:
				linear-gradient(180deg, rgba(152, 117, 67, 0.98) 0%, rgba(118, 86, 44, 1) 100%);
			--dash-primary-btn-hover:
				linear-gradient(180deg, rgba(168, 130, 76, 1) 0%, rgba(132, 97, 52, 1) 100%);
			--dash-primary-btn-text: #fff8ed;
			--dash-danger: #f0a3b3;
			--dash-shadow-soft: 0 1px 2px rgba(0, 0, 0, 0.34);
			--dash-shadow-strong: 0 16px 30px rgba(0, 0, 0, 0.46);
		}

		.dashboard-header {
			display: flex;
			align-items: flex-start;
			justify-content: space-between;
			gap: 0.72rem;
			padding: 0.7rem 0.76rem;
			border: 1px solid var(--dash-border);
			border-radius: 0.75rem;
			background: var(--dash-header-bg);
			box-shadow: var(--dash-shadow-soft);
		}

	.header-actions {
		display: inline-flex;
		align-items: center;
		justify-content: flex-end;
		gap: 0.42rem;
		flex-wrap: wrap;
	}

	.add-actions {
		position: relative;
	}

		.dashboard-title-wrap h3 {
			margin: 0;
			font-size: 0.95rem;
			letter-spacing: 0.01em;
			color: var(--dash-text);
		}

	.dashboard-title-wrap p {
		margin: 0.24rem 0 0;
		font-size: 0.76rem;
		line-height: 1.4;
		color: var(--dash-muted);
		max-width: 34ch;
	}

		.add-action-btn,
		.ai-organize-btn,
		.dashboard-close-btn,
		.composer-close-btn {
			border: 1px solid var(--dash-border);
			background: var(--dash-input-bg);
			color: var(--dash-text);
			border-radius: 0.55rem;
			font-size: 0.73rem;
			font-weight: 650;
			cursor: pointer;
			padding: 0.4rem 0.66rem;
			line-height: 1;
			transition:
				background 0.2s ease,
				border-color 0.2s ease,
				box-shadow 0.2s ease,
				transform 0.15s ease;
			box-shadow: var(--dash-shadow-soft);
		}

	.add-action-btn:hover,
	.ai-organize-btn:hover:not(:disabled),
	.dashboard-close-btn:hover,
	.composer-close-btn:hover {
		border-color: var(--dash-border-strong);
		background: var(--dash-menu-bg);
		transform: translateY(-1px);
	}

	.add-action-btn:focus-visible,
	.ai-organize-btn:focus-visible,
	.dashboard-close-btn:focus-visible,
	.composer-close-btn:focus-visible,
	.composer-submit-btn:focus-visible,
	.add-menu button:focus-visible,
	.composer-field input:focus,
	.composer-field textarea:focus,
	.note-editor textarea:focus {
		outline: none;
		box-shadow: var(--dash-focus-ring);
	}

	.dashboard-close-btn {
		width: 2rem;
		height: 2rem;
		padding: 0;
		font-size: 1.08rem;
		justify-content: center;
		display: inline-flex;
		align-items: center;
	}

	.dashboard-close-btn span {
		transform: translateY(-0.5px);
	}

	.ai-organize-btn {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
	}

	.ai-organize-btn:disabled {
		opacity: 0.56;
		cursor: not-allowed;
		transform: none;
	}

	.btn-spinner {
		width: 0.74rem;
		height: 0.74rem;
		border-radius: 999px;
		border: 2px solid color-mix(in srgb, var(--dash-accent) 28%, transparent);
		border-top-color: color-mix(in srgb, var(--dash-accent) 92%, black 8%);
		animation: dashboard-spin 0.82s linear infinite;
	}

		.add-menu {
			position: absolute;
			top: calc(100% + 0.45rem);
			right: 0;
			min-width: 11.4rem;
			display: grid;
			gap: 0.2rem;
			padding: 0.36rem;
			border: 1px solid var(--dash-border-strong);
			border-radius: 0.64rem;
			background: var(--dash-menu-bg);
			box-shadow: var(--dash-shadow-strong);
			z-index: 24;
		}

	.add-menu button {
		border: 0;
		background: transparent;
		color: var(--dash-text);
		font-size: 0.72rem;
		font-weight: 620;
		text-align: left;
		padding: 0.42rem 0.5rem;
		border-radius: 0.52rem;
		cursor: pointer;
		transition: background 0.2s ease;
	}

		.add-menu button:hover {
			background: var(--dash-accent-soft);
		}

		.dashboard-summary-strip {
			display: grid;
			grid-template-columns: repeat(3, minmax(0, 1fr));
			gap: 0.52rem;
		}

		.summary-card {
			display: grid;
			gap: 0.2rem;
			padding: 0.58rem 0.62rem;
			border: 1px solid var(--dash-border);
			border-radius: 0.64rem;
			background: var(--dash-header-bg);
			box-shadow: var(--dash-shadow-soft);
		}

		.summary-card span {
			font-size: 0.67rem;
			font-weight: 700;
			text-transform: uppercase;
			letter-spacing: 0.05em;
			color: var(--dash-soft);
		}

		.summary-card strong {
			font-size: 1rem;
			line-height: 1.1;
			letter-spacing: 0.01em;
			color: var(--dash-text);
		}

		.dashboard-board {
			display: grid;
			grid-template-columns: repeat(2, minmax(0, 1fr));
			gap: 0.64rem;
			align-items: start;
		}

		.dashboard-add-composer {
			display: grid;
			gap: 0.68rem;
			padding: 0.72rem;
			border-radius: 0.72rem;
			border: 1px solid var(--dash-border);
			background: var(--dash-section-bg);
			box-shadow: var(--dash-shadow-soft);
		}

	.composer-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	.composer-head strong {
		font-size: 0.79rem;
		color: var(--dash-text);
		letter-spacing: 0.01em;
	}

	.composer-close-btn {
		font-size: 0.69rem;
		padding: 0.34rem 0.56rem;
	}

	.composer-field {
		display: grid;
		gap: 0.3rem;
	}

	.composer-field span {
		font-size: 0.67rem;
		font-weight: 700;
		color: var(--dash-soft);
		text-transform: uppercase;
		letter-spacing: 0.05em;
	}

	.composer-field input,
	.composer-field textarea,
	.note-editor textarea {
		width: 100%;
		border: 1px solid var(--dash-border);
		background: var(--dash-input-bg);
		color: var(--dash-text);
		border-radius: 0.62rem;
		font-size: 0.74rem;
		padding: 0.48rem 0.56rem;
		outline: none;
		transition:
			border-color 0.2s ease,
			box-shadow 0.2s ease,
			background 0.2s ease;
	}

	.composer-field textarea {
		min-height: 4.8rem;
		resize: vertical;
	}

		.beacon-scheduler {
			display: grid;
			gap: 0.56rem;
			padding: 0.56rem;
			border: 1px solid var(--dash-border);
			border-radius: 0.6rem;
			background: color-mix(in srgb, var(--dash-card-bg) 85%, transparent);
		}

	.beacon-day-scroll {
		display: flex;
		gap: 0.42rem;
		overflow-x: auto;
		padding: 0.12rem 0.04rem 0.16rem;
		scrollbar-width: thin;
		scrollbar-color: color-mix(in srgb, var(--dash-accent) 46%, transparent) transparent;
	}

	.beacon-day-chip {
		flex: 0 0 auto;
		display: grid;
		gap: 0.12rem;
		min-width: 6.1rem;
		padding: 0.42rem 0.48rem;
		border-radius: 0.58rem;
		border: 1px solid var(--dash-border);
		background: var(--dash-input-bg);
		color: var(--dash-text);
		text-align: left;
		cursor: pointer;
		transition:
			border-color 0.16s ease,
			background 0.16s ease,
			transform 0.16s ease;
	}

	.beacon-day-chip strong {
		font-size: 0.7rem;
		font-weight: 700;
	}

	.beacon-day-chip small {
		font-size: 0.62rem;
		color: var(--dash-soft);
	}

	.beacon-day-chip:hover {
		border-color: var(--dash-border-strong);
		transform: translateY(-1px);
	}

	.beacon-day-chip.is-active {
		border-color: color-mix(in srgb, var(--dash-accent) 56%, var(--dash-border-strong));
		background: color-mix(in srgb, var(--dash-accent-soft) 45%, var(--dash-input-bg));
	}

	.beacon-datetime-grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.48rem;
	}

	.beacon-input-wrap {
		display: flex;
		align-items: center;
		gap: 0.42rem;
		border: 1px solid var(--dash-border);
		border-radius: 0.62rem;
		background: var(--dash-input-bg);
		padding: 0.24rem 0.42rem;
	}

	.beacon-input-wrap svg {
		width: 0.88rem;
		height: 0.88rem;
		stroke: var(--dash-soft);
		stroke-width: 1.85;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
		flex-shrink: 0;
	}

	.beacon-input-wrap:focus-within {
		border-color: var(--dash-border-strong);
		box-shadow: var(--dash-focus-ring);
	}

	.beacon-datetime-input {
		border: 0 !important;
		background: transparent !important;
		padding: 0.2rem 0 !important;
		border-radius: 0 !important;
		min-height: 1.9rem;
		color: var(--dash-text);
	}

	.beacon-datetime-input:focus {
		box-shadow: none !important;
	}

	.beacon-quick-time {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.4rem;
		flex-wrap: wrap;
	}

	.beacon-quick-time span {
		font-size: 0.64rem;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--dash-soft);
	}

	.beacon-quick-time-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.34rem;
		flex-wrap: wrap;
	}

	.beacon-quick-time-actions button {
		border: 1px solid var(--dash-border);
		border-radius: 999px;
		padding: 0.22rem 0.52rem;
		font-size: 0.66rem;
		font-weight: 700;
		background: var(--dash-input-bg);
		color: var(--dash-text);
		cursor: pointer;
		transition:
			background 0.16s ease,
			border-color 0.16s ease;
	}

	.beacon-quick-time-actions button:hover {
		border-color: var(--dash-border-strong);
		background: color-mix(in srgb, var(--dash-accent-soft) 38%, var(--dash-input-bg));
	}

	.beacon-day-chip:focus-visible,
	.beacon-quick-time-actions button:focus-visible {
		outline: none;
		box-shadow: var(--dash-focus-ring);
	}

	.beacon-datetime-input::-webkit-calendar-picker-indicator {
		opacity: 0.82;
		cursor: pointer;
	}

	.room-dashboard.theme-dark .beacon-datetime-input {
		color-scheme: dark;
	}

	.room-dashboard.theme-dark .beacon-datetime-input::-webkit-calendar-picker-indicator {
		filter: invert(1) brightness(1.8);
		opacity: 0.96;
	}

		.beacon-preview {
			position: relative;
			display: grid;
			gap: 0.3rem;
			padding: 0.56rem 0.6rem;
			border: 1px solid var(--dash-border);
			border-radius: 0.58rem;
			background: var(--dash-card-bg);
			overflow: hidden;
		}

	.beacon-preview::after {
		content: '';
		position: absolute;
		inset: 0;
		background: linear-gradient(
			130deg,
			color-mix(in srgb, var(--dash-accent) 18%, transparent) 0%,
			transparent 62%
		);
		pointer-events: none;
	}

	.preview-pill {
		display: inline-flex;
		width: fit-content;
		padding: 0.16rem 0.48rem;
		border-radius: 999px;
		font-size: 0.62rem;
		font-weight: 700;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--dash-chip-text);
		background: var(--dash-chip-bg);
		border: 1px solid var(--dash-border);
	}

	.beacon-preview-time {
		display: inline-flex;
		align-items: center;
		gap: 0.32rem;
		font-size: 0.67rem;
		font-weight: 700;
		color: var(--dash-soft);
	}

	.beacon-preview-time svg {
		width: 0.76rem;
		height: 0.76rem;
		stroke: currentColor;
		stroke-width: 1.85;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.beacon-preview p {
		margin: 0;
		font-size: 0.73rem;
		line-height: 1.38;
		color: var(--dash-text);
	}

	.composer-error {
		font-size: 0.68rem;
		font-weight: 700;
		color: var(--dash-danger);
	}

	.composer-actions {
		display: flex;
		justify-content: flex-end;
	}

	.composer-submit-btn {
		border: 1px solid color-mix(in srgb, var(--dash-accent) 60%, transparent);
		background: var(--dash-primary-btn);
		color: var(--dash-primary-btn-text);
		border-radius: 0.64rem;
		font-size: 0.72rem;
		font-weight: 700;
		padding: 0.44rem 0.72rem;
		cursor: pointer;
		box-shadow:
			0 10px 22px color-mix(in srgb, var(--dash-accent) 24%, transparent),
			inset 0 1px 0 rgba(255, 255, 255, 0.22);
		transition:
			transform 0.15s ease,
			filter 0.2s ease,
			box-shadow 0.2s ease;
	}

	.composer-submit-btn:hover {
		background: var(--dash-primary-btn-hover);
		transform: translateY(-1px);
		filter: saturate(1.05);
	}

		.dashboard-section {
			border: 1px solid var(--dash-border);
			border-radius: 0.72rem;
			background: var(--dash-section-bg);
			padding: 0.66rem;
			display: grid;
			gap: 0.56rem;
			box-shadow: var(--dash-shadow-soft);
		}

		.section-pinned {
			grid-column: 1 / -1;
		}

		.section-priority {
			border-top: 1px solid color-mix(in srgb, var(--dash-accent) 45%, var(--dash-border));
	}

	.section-expired {
		border-top: 1px solid color-mix(in srgb, #cb5d74 45%, var(--dash-border));
	}

	.section-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.55rem;
	}

	.section-head h4 {
		margin: 0;
		font-size: 0.82rem;
		letter-spacing: 0.02em;
		color: var(--dash-text);
	}

	.section-count {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-width: 1.72rem;
		height: 1.42rem;
		padding: 0 0.42rem;
		border-radius: 999px;
		font-size: 0.67rem;
		font-weight: 760;
		color: var(--dash-accent);
		background: var(--dash-chip-bg);
		border: 1px solid var(--dash-border);
	}

		.card-list,
		.pinned-groups {
			display: grid;
			gap: 0.52rem;
		}

		.card-list {
			grid-template-columns: repeat(auto-fill, minmax(210px, 1fr));
		}

		.pinned-groups {
			grid-template-columns: repeat(3, minmax(0, 1fr));
		}

		.group-block {
			display: grid;
			gap: 0.48rem;
			padding: 0.5rem;
			border: 1px solid var(--dash-border);
			border-radius: 0.62rem;
			background: var(--dash-group-bg);
		}

	.group-block h5 {
		margin: 0;
		font-size: 0.66rem;
		font-weight: 760;
		text-transform: uppercase;
		letter-spacing: 0.08em;
		color: var(--dash-soft);
	}

		.dashboard-card {
			border: 1px solid var(--dash-border);
			border-radius: 0.56rem;
			padding: 0.56rem;
			display: grid;
			gap: 0.38rem;
			background: var(--dash-card-bg);
			box-shadow: var(--dash-shadow-soft);
			transition:
				transform 0.16s ease,
				border-color 0.16s ease,
				box-shadow 0.16s ease;
	}

		.dashboard-card:hover {
			border-color: var(--dash-border-strong);
			transform: translateY(-1px);
			box-shadow: var(--dash-shadow-strong);
		}

		.dashboard-card.kind-task {
			border-left: 3px solid color-mix(in srgb, #a8733c 74%, transparent);
		}

		.dashboard-card.kind-note {
			border-left: 3px solid color-mix(in srgb, #5d8f67 76%, transparent);
		}

		.dashboard-card.kind-message {
			border-left: 3px solid color-mix(in srgb, #7f8796 58%, transparent);
		}

	.card-top {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.56rem;
	}

	.card-top strong {
		font-size: 0.76rem;
		color: var(--dash-text);
		font-weight: 700;
	}

	.card-top time {
		font-size: 0.66rem;
		font-weight: 620;
		color: var(--dash-soft);
	}

	.dashboard-card p {
		margin: 0;
		font-size: 0.75rem;
		line-height: 1.4;
		color: var(--dash-muted);
	}

	.dashboard-card img {
		width: 100%;
		max-height: 170px;
		object-fit: cover;
		border-radius: 0.58rem;
		border: 1px solid var(--dash-border);
	}

	.dashboard-card a {
		font-size: 0.72rem;
		font-weight: 650;
		color: var(--dash-accent);
		text-decoration: none;
	}

	.dashboard-card a:hover {
		text-decoration: underline;
	}

	.chip {
		display: inline-flex;
		width: fit-content;
		padding: 0.2rem 0.38rem;
		border-radius: 999px;
		font-size: 0.64rem;
		font-weight: 650;
		letter-spacing: 0.02em;
		background: var(--dash-chip-bg);
		border: 1px solid var(--dash-border);
		color: var(--dash-chip-text);
	}

	.note-chip {
		background: color-mix(in srgb, var(--dash-chip-bg) 60%, var(--dash-accent-soft));
	}

	.note-editor {
		display: grid;
		gap: 0.24rem;
	}

	.note-editor span {
		font-size: 0.64rem;
		font-weight: 720;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--dash-soft);
	}

	.note-editor textarea {
		min-height: 48px;
		resize: vertical;
		font-size: 0.72rem;
		line-height: 1.35;
	}

	.empty-state {
		font-size: 0.72rem;
		color: var(--dash-muted);
		padding: 0.42rem 0.32rem;
		border: 1px dashed var(--dash-border);
		border-radius: 0.66rem;
		background: color-mix(in srgb, var(--dash-group-bg) 80%, transparent);
	}

	.empty-state.subtle {
		font-size: 0.68rem;
		padding: 0.34rem 0.3rem;
	}

		@media (max-width: 900px) {
			.room-dashboard {
				padding: 0.7rem;
				gap: 0.74rem;
			}

			.dashboard-header {
				padding: 0.64rem 0.66rem;
			}

			.dashboard-title-wrap p {
				max-width: 28ch;
			}

			.dashboard-board {
				grid-template-columns: minmax(0, 1fr);
			}

			.section-pinned {
				grid-column: auto;
			}

			.pinned-groups {
				grid-template-columns: repeat(2, minmax(0, 1fr));
			}
		}

		@media (max-width: 620px) {
			.dashboard-header {
				flex-direction: column;
			align-items: stretch;
			gap: 0.62rem;
		}

		.header-actions {
			justify-content: flex-start;
		}

			.dashboard-section,
			.dashboard-add-composer {
				border-radius: 0.9rem;
				padding: 0.66rem;
			}

			.dashboard-summary-strip {
				grid-template-columns: minmax(0, 1fr);
			}

			.group-block {
				padding: 0.44rem;
			}

			.pinned-groups {
				grid-template-columns: minmax(0, 1fr);
			}

			.beacon-datetime-grid {
				grid-template-columns: minmax(0, 1fr);
			}
	}

	@keyframes dashboard-spin {
		to {
			transform: rotate(360deg);
		}
	}
</style>
