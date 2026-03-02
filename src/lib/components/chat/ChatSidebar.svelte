<script lang="ts">
	import { createEventDispatcher, onMount } from 'svelte';
	import IconSet from '$lib/components/icons/IconSet.svelte';
	import type { ChatThread, ThemePreference, ThreadStatus } from '$lib/types/chat';

	type TreeRow = {
		thread: ChatThread;
		depth: number;
	};

	type TreeNodeLayout = {
		id: string;
		thread: ChatThread;
		x: number;
		y: number;
		width: number;
		height: number;
		depth: number;
	};

	type TreeEdgeLayout = {
		id: string;
		path: string;
	};

	type TreeLayout = {
		nodes: TreeNodeLayout[];
		edges: TreeEdgeLayout[];
		width: number;
		height: number;
	};

	export let myRooms: ChatThread[] = [];
	export let discoverableRooms: ChatThread[] = [];
	export let leftRooms: ChatThread[] = [];
	export let accessibleParentRoomIds: string[] = [];
	export let activeRoomId = '';
	export let showLeftMenu = false;
	export let chatListSearch = '';
	export let isMobileView = false;
	export let isDarkMode = false;
	export let themePreference: ThemePreference = 'system';

	const dispatch = createEventDispatcher<{
		select: { id: string; isMember: boolean; status: ThreadStatus };
		jumpOrigin: {
			parentRoomId: string;
			originMessageId: string;
			fallbackRoomId: string;
			fallbackIsMember: boolean;
		};
		toggleMenu: void;
		toggleTheme: void;
		createRoom: void;
		renameRoom: { roomId: string };
	}>();

	let showRelationsMap = false;
	let activeTreeIndex = 0;
	let isFullView = false;
	let streamlinedParentRoomId = '';
	let streamlinedManualRootList = false;
	let previousActiveRoomId = '';
	let relationsPanelEl: HTMLElement | null = null;
	let relationsTriggerEl: HTMLButtonElement | null = null;
	const treeNodeWidth = 170;
	const treeNodeHeight = 48;
	const treeColumnGap = 230;
	const treeRowGap = 84;
	const treePadX = 24;
	const treePadY = 24;

	$: totalRooms = myRooms.length + discoverableRooms.length + leftRooms.length;
	$: allThreads = dedupeThreads([...myRooms, ...discoverableRooms, ...leftRooms]);
	$: threadByID = new Map(allThreads.map((thread) => [thread.id, thread]));
	$: childrenByParent = buildChildrenByParent(allThreads);
	$: subtreeUnreadByThreadID = buildSubtreeUnreadByThreadID(allThreads, childrenByParent);
	$: rootThreads = sortSidebarThreads(
		allThreads.filter((thread) => !thread.parentRoomId || !threadByID.has(thread.parentRoomId))
	);
	$: if (streamlinedParentRoomId && !threadByID.has(streamlinedParentRoomId)) {
		streamlinedParentRoomId = '';
	}
	$: if (isFullView) {
		streamlinedManualRootList = false;
	}
	$: {
		const nextActiveRoomId = activeRoomId || '';
		const activeRoomChanged = nextActiveRoomId !== previousActiveRoomId;
		if (activeRoomChanged) {
			const active = threadByID.get(nextActiveRoomId);
			// Auto-open the corresponding child tree only on actual room switch.
			// This prevents background refreshes from overriding manual "Back to roots" browsing.
			if (!isFullView && active?.parentRoomId) {
				streamlinedManualRootList = false;
			}
			if (!isFullView && !streamlinedManualRootList) {
				streamlinedParentRoomId = getStreamlinedContextRoomID(
					nextActiveRoomId,
					threadByID,
					childrenByParent
				);
			}
			previousActiveRoomId = nextActiveRoomId;
		}
	}
	$: activeStreamlinedParent = threadByID.get(streamlinedParentRoomId);
	$: descendantThreads = streamlinedParentRoomId
		? sortSidebarThreads(
				flattenTree(streamlinedParentRoomId)
					.slice(1)
					.map((row) => row.thread)
			)
		: [];
	$: streamlinedThreads = (() => {
		if (!streamlinedParentRoomId) {
			return rootThreads;
		}
		const parentThread = threadByID.get(streamlinedParentRoomId);
		if (!parentThread) {
			return descendantThreads;
		}
		return [parentThread, ...descendantThreads];
	})();
	$: accessibleParents = new Set(
		accessibleParentRoomIds.length > 0
			? accessibleParentRoomIds
			: [
					...myRooms.map((room) => room.id),
					...discoverableRooms.map((room) => room.id),
					...leftRooms.map((room) => room.id)
				]
	);
	$: relationRootIds = allThreads
		.filter((thread) => !thread.parentRoomId || !threadByID.has(thread.parentRoomId))
		.map((thread) => thread.id);
	$: if (relationRootIds.length === 0) {
		activeTreeIndex = 0;
	} else if (activeTreeIndex >= relationRootIds.length || activeTreeIndex < 0) {
		activeTreeIndex = 0;
	}
	$: currentTreeRootId = relationRootIds.length > 0 ? relationRootIds[activeTreeIndex] : '';
	$: currentTreeRows = currentTreeRootId ? flattenTree(currentTreeRootId) : [];
	$: activeThread = threadByID.get(activeRoomId);
	$: activeParentRoomId = activeThread?.parentRoomId || '';
	$: highlightedChildIds = new Set(
		allThreads
			.filter((thread) => thread.parentRoomId && thread.parentRoomId === activeRoomId)
			.map((thread) => thread.id)
	);
	$: treeLayout = buildTreeLayout(currentTreeRows);
	$: treeNodes = treeLayout.nodes;
	$: treeEdges = treeLayout.edges;

	onMount(() => {
		const onDocumentPointerDown = (event: PointerEvent) => {
			if (!showRelationsMap) {
				return;
			}
			const target = event.target;
			if (!(target instanceof Node)) {
				return;
			}
			if (relationsPanelEl?.contains(target)) {
				return;
			}
			if (relationsTriggerEl?.contains(target)) {
				return;
			}
			showRelationsMap = false;
		};

		window.addEventListener('pointerdown', onDocumentPointerDown);
		return () => {
			window.removeEventListener('pointerdown', onDocumentPointerDown);
		};
	});

	function dedupeThreads(threads: ChatThread[]) {
		const byID = new Map<string, ChatThread>();
		for (const thread of threads) {
			const existing = byID.get(thread.id);
			if (!existing) {
				byID.set(thread.id, thread);
				continue;
			}
			byID.set(thread.id, {
				...existing,
				...thread,
				status:
					existing.status === 'joined' || thread.status === 'joined'
						? 'joined'
						: existing.status === 'discoverable' || thread.status === 'discoverable'
							? 'discoverable'
							: 'left'
			});
		}
		return [...byID.values()];
	}

	function buildChildrenByParent(threads: ChatThread[]) {
		const index = new Map<string, ChatThread[]>();
		for (const thread of threads) {
			if (!thread.parentRoomId) {
				continue;
			}
			const existing = index.get(thread.parentRoomId) ?? [];
			existing.push(thread);
			index.set(thread.parentRoomId, existing);
		}
		for (const [parentID, children] of index) {
			index.set(
				parentID,
				[...children].sort((a, b) =>
					a.name.localeCompare(b.name, undefined, { sensitivity: 'base' })
				)
			);
		}
		return index;
	}

	function normalizeUnread(value: number | undefined) {
		if (!Number.isFinite(value)) {
			return 0;
		}
		return Math.max(0, Math.floor(value ?? 0));
	}

	function buildSubtreeUnreadByThreadID(
		threads: ChatThread[],
		childIndex: Map<string, ChatThread[]>
	) {
		const unreadByID = new Map(threads.map((thread) => [thread.id, normalizeUnread(thread.unread)]));
		const totals = new Map<string, number>();
		const walk = (threadID: string, seen: Set<string>) => {
			if (totals.has(threadID)) {
				return totals.get(threadID) ?? 0;
			}
			if (seen.has(threadID)) {
				return unreadByID.get(threadID) ?? 0;
			}
			const nextSeen = new Set(seen);
			nextSeen.add(threadID);
			let total = unreadByID.get(threadID) ?? 0;
			const children = childIndex.get(threadID) ?? [];
			for (const child of children) {
				total += walk(child.id, nextSeen);
			}
			totals.set(threadID, total);
			return total;
		};
		for (const thread of threads) {
			walk(thread.id, new Set<string>());
		}
		return totals;
	}

	function sortSidebarThreads(threads: ChatThread[]) {
		const statusRank = (status: ThreadStatus) => {
			if (status === 'joined') {
				return 0;
			}
			if (status === 'left') {
				return 1;
			}
			return 2;
		};
		return [...threads].sort((a, b) => {
			const rankDelta = statusRank(a.status) - statusRank(b.status);
			if (rankDelta !== 0) {
				return rankDelta;
			}
			if (a.lastActivity !== b.lastActivity) {
				return b.lastActivity - a.lastActivity;
			}
			return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });
		});
	}

	function flattenTree(rootID: string) {
		const rows: TreeRow[] = [];
		const walk = (threadID: string, depth: number) => {
			const node = threadByID.get(threadID);
			if (!node) {
				return;
			}
			rows.push({ thread: node, depth });
			const children = childrenByParent.get(threadID) ?? [];
			for (const child of children) {
				walk(child.id, depth + 1);
			}
		};
		walk(rootID, 0);
		return rows;
	}

	function buildTreeLayout(rows: TreeRow[]): TreeLayout {
		if (rows.length === 0) {
			return { nodes: [], edges: [], width: treeNodeWidth + treePadX * 2, height: 180 };
		}

		const rowsByDepth = new Map<number, TreeRow[]>();
		let maxDepth = 0;
		for (const row of rows) {
			const bucket = rowsByDepth.get(row.depth) ?? [];
			bucket.push(row);
			rowsByDepth.set(row.depth, bucket);
			if (row.depth > maxDepth) {
				maxDepth = row.depth;
			}
		}

		let maxRowsInColumn = 1;
		for (const bucket of rowsByDepth.values()) {
			if (bucket.length > maxRowsInColumn) {
				maxRowsInColumn = bucket.length;
			}
		}

		const nodes: TreeNodeLayout[] = [];
		const nodeByID = new Map<string, TreeNodeLayout>();
		for (const [depth, bucket] of rowsByDepth.entries()) {
			for (let index = 0; index < bucket.length; index++) {
				const row = bucket[index];
				const x = treePadX + depth * treeColumnGap;
				const y = treePadY + index * treeRowGap;
				const node: TreeNodeLayout = {
					id: row.thread.id,
					thread: row.thread,
					x,
					y,
					width: treeNodeWidth,
					height: treeNodeHeight,
					depth
				};
				nodes.push(node);
				nodeByID.set(row.thread.id, node);
			}
		}

		const edges: TreeEdgeLayout[] = [];
		for (const node of nodes) {
			const parentID = node.thread.parentRoomId;
			if (!parentID) {
				continue;
			}
			const parent = nodeByID.get(parentID);
			if (!parent) {
				continue;
			}
			const fromX = parent.x + parent.width;
			const fromY = parent.y + parent.height / 2;
			const toX = node.x;
			const toY = node.y + node.height / 2;
			const c1x = fromX + 34;
			const c2x = toX - 34;
			const path = `M ${fromX} ${fromY} C ${c1x} ${fromY}, ${c2x} ${toY}, ${toX} ${toY}`;
			edges.push({ id: `${parent.id}->${node.id}`, path });
		}

		const width = treePadX * 2 + maxDepth * treeColumnGap + treeNodeWidth;
		const height = treePadY * 2 + (maxRowsInColumn - 1) * treeRowGap + treeNodeHeight;
		return { nodes, edges, width, height };
	}

	function getTreeRootID(threadID: string, threadIndex: Map<string, ChatThread> = threadByID) {
		let cursor = threadID;
		const seen = new Set<string>();
		while (cursor) {
			if (seen.has(cursor)) {
				break;
			}
			seen.add(cursor);
			const node = threadIndex.get(cursor);
			const parentID = node?.parentRoomId || '';
			if (!parentID || !threadIndex.has(parentID)) {
				break;
			}
			cursor = parentID;
		}
		return cursor;
	}

	function openRelationsMap() {
		if (showLeftMenu) {
			dispatch('toggleMenu');
		}
		showRelationsMap = true;
		const rootID = activeRoomId ? getTreeRootID(activeRoomId) : relationRootIds[0];
		const nextIndex = relationRootIds.findIndex((id) => id === rootID);
		activeTreeIndex = nextIndex >= 0 ? nextIndex : 0;
	}

	function closeRelationsMap() {
		showRelationsMap = false;
	}

	function toggleFullView() {
		isFullView = !isFullView;
		if (isFullView) {
			streamlinedParentRoomId = '';
			streamlinedManualRootList = false;
		}
	}

	function requestRenameRoom() {
		if (!activeRoomId) {
			return;
		}
		if (showLeftMenu) {
			dispatch('toggleMenu');
		}
		dispatch('renameRoom', { roomId: activeRoomId });
		showRelationsMap = false;
	}

	function gotoPrevTree() {
		if (relationRootIds.length <= 1) {
			return;
		}
		activeTreeIndex = (activeTreeIndex - 1 + relationRootIds.length) % relationRootIds.length;
	}

	function gotoNextTree() {
		if (relationRootIds.length <= 1) {
			return;
		}
		activeTreeIndex = (activeTreeIndex + 1) % relationRootIds.length;
	}

	function formatClock(timestamp: number) {
		const safe = Number.isFinite(timestamp) ? timestamp : Date.now();
		return new Date(safe).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
	}

	function selectRoom(thread: ChatThread) {
		dispatch('select', {
			id: thread.id,
			isMember: thread.status === 'joined',
			status: thread.status
		});
	}

	function hasChildren(threadID: string) {
		return (childrenByParent.get(threadID) ?? []).length > 0;
	}

	function getChildCount(threadID: string) {
		return (childrenByParent.get(threadID) ?? []).length;
	}

	function openRootRoomList() {
		streamlinedManualRootList = true;
		streamlinedParentRoomId = '';
	}

	function onThreadSelect(thread: ChatThread) {
		if (thread.status === 'left') {
			if (hasChildren(thread.id)) {
				streamlinedManualRootList = false;
				streamlinedParentRoomId = thread.id;
			}
			return;
		}
		if (!isFullView && isMobileView && hasChildren(thread.id)) {
			const isAlreadyBrowsingThisParent = streamlinedParentRoomId === thread.id;
			if (!isAlreadyBrowsingThisParent) {
				streamlinedManualRootList = false;
				streamlinedParentRoomId = thread.id;
				return;
			}
		}
		streamlinedManualRootList = false;
		selectRoom(thread);
		if (!isFullView) {
			streamlinedParentRoomId = getStreamlinedContextRoomID(
				thread.id,
				threadByID,
				childrenByParent
			);
		}
	}

	function getStreamlinedContextRoomID(
		currentActiveID: string,
		threadIndex: Map<string, ChatThread>,
		childIndex: Map<string, ChatThread[]>
	) {
		const active = threadIndex.get(currentActiveID);
		if (!active) {
			return '';
		}
		const rootID = getTreeRootID(active.id, threadIndex);
		const hasChildThreads = (threadID: string) => (childIndex.get(threadID) ?? []).length > 0;
		if (rootID && hasChildThreads(rootID)) {
			return rootID;
		}
		if (hasChildThreads(active.id)) {
			return active.id;
		}
		return '';
	}

	function hasBreakOrigin(thread: ChatThread) {
		return Boolean(thread.parentRoomId && thread.originMessageId);
	}

	function isChildThread(thread: ChatThread) {
		return Boolean(thread.parentRoomId);
	}

	function canJumpToOrigin(thread: ChatThread) {
		const parentRoom = thread.parentRoomId ? threadByID.get(thread.parentRoomId) : null;
		return Boolean(
			hasBreakOrigin(thread) &&
			thread.parentRoomId &&
			accessibleParents.has(thread.parentRoomId) &&
			parentRoom?.status !== 'left'
		);
	}

	function jumpToOrigin(thread: ChatThread) {
		if (!canJumpToOrigin(thread)) {
			return;
		}
		dispatch('jumpOrigin', {
			parentRoomId: thread.parentRoomId || '',
			originMessageId: thread.originMessageId || '',
			fallbackRoomId: thread.id,
			fallbackIsMember: thread.status === 'joined'
		});
	}

	function onAvatarKeyDown(event: KeyboardEvent, thread: ChatThread) {
		if (!canJumpToOrigin(thread)) {
			return;
		}
		if (event.key === 'Enter' || event.key === ' ') {
			event.preventDefault();
			jumpToOrigin(thread);
		}
	}

	function getRoomItemClasses(thread: ChatThread) {
		const classes = ['room-item'];
		if (thread.status === 'discoverable') {
			classes.push('discoverable');
		}
		if (thread.status === 'left') {
			classes.push('left');
		}
		if (thread.id === activeRoomId) {
			classes.push('selected');
		}
		if (highlightedChildIds.has(thread.id)) {
			classes.push('related-child');
		}
		if (activeParentRoomId && thread.id === activeParentRoomId) {
			classes.push('related-parent');
		}
		return classes.join(' ');
	}

	function openRoomFromTree(thread: ChatThread) {
		selectRoom(thread);
		closeRelationsMap();
	}

	function getTreeAvatarLabel(thread: ChatThread) {
		const number = Number.isFinite(thread.treeNumber) ? Number(thread.treeNumber) : 0;
		if (number > 0) {
			return String(number);
		}
		return '#';
	}

	function getThreadPreview(thread: ChatThread) {
		const preview = (thread.lastMessage || '').trim();
		if (preview) {
			return preview;
		}
		if (thread.status === 'left') {
			return hasChildren(thread.id) ? 'Left room - browse child rooms' : 'You left this room';
		}
		return thread.status === 'joined' ? 'No messages yet' : 'Preview and join';
	}

	function getUnreadBadgeCount(thread: ChatThread) {
		const directUnread = normalizeUnread(thread.unread);
		const isStreamlinedRootList = !isFullView && !streamlinedParentRoomId;
		if (!isStreamlinedRootList) {
			return directUnread;
		}
		return subtreeUnreadByThreadID.get(thread.id) ?? directUnread;
	}
</script>

<aside class="room-list {isDarkMode ? 'theme-dark' : ''}">
	<div class="room-list-header">
		<div class="list-title">
			<h2>Chats</h2>
			<span class="thread-count">{totalRooms}</span>
		</div>
		<div class="list-actions">
			<button
				type="button"
				class="icon-button theme-icon-button {isDarkMode ? 'active' : ''}"
				on:click={() => dispatch('toggleTheme')}
				title={isDarkMode
					? 'Switch to light mode'
					: themePreference === 'system'
						? 'Switch to dark mode (system mode active)'
						: 'Switch to dark mode'}
				aria-label={isDarkMode ? 'Switch to light mode' : 'Switch to dark mode'}
			>
				<IconSet name="theme" size={14} />
			</button>
			<button
				type="button"
				class="icon-button map-icon-button"
				on:click={openRelationsMap}
				title="View room relations map"
				aria-label="View room relations map"
				bind:this={relationsTriggerEl}
			>
				<IconSet name="tree-map" size={14} />
			</button>

			<button
				type="button"
				class="icon-button view-icon-button {isFullView ? 'active' : ''}"
				on:click={toggleFullView}
				title={isFullView ? 'Switch to streamlined view' : 'Switch to full view'}
				aria-label={isFullView ? 'Switch to streamlined view' : 'Switch to full view'}
			>
				<IconSet name="list-vertical" size={14} />
			</button>
			<button
				type="button"
				class="icon-button menu-icon-button"
				on:click={() => dispatch('toggleMenu')}
				title="Room options"
			>
				...
			</button>
			{#if showLeftMenu}
				<div class="room-menu left-menu">
					<button type="button" on:click={() => dispatch('createRoom')}>New room</button>
					<button type="button" on:click={requestRenameRoom} disabled={!activeRoomId}
						>Rename room</button
					>
					<button type="button" on:click={openRelationsMap}>Relations map</button>
					<button type="button" on:click={toggleFullView}>
						{isFullView ? 'Streamlined view' : 'Full view'}
					</button>
				</div>
			{/if}
		</div>
	</div>
	<div class="room-list-search">
		<input type="text" bind:value={chatListSearch} placeholder="Search names or messages" />
	</div>
	<div class="room-items">
		{#if isFullView}
			{#if myRooms.length === 0 && discoverableRooms.length === 0 && leftRooms.length === 0}
				<div class="empty-label">No chats matched your search.</div>
			{:else}
				{#if myRooms.length > 0}
					<div class="section-label">Joined</div>
					{#each myRooms as thread (thread.id)}
						<button
							type="button"
							class={getRoomItemClasses(thread)}
							on:click={() => onThreadSelect(thread)}
						>
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<!-- svelte-ignore a11y_click_events_have_key_events -->
							<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
							<span
								class="avatar {isChildThread(thread)
									? 'child-avatar'
									: 'original-avatar'} {canJumpToOrigin(thread) ? 'jumpable' : ''}"
								role={canJumpToOrigin(thread) ? 'button' : undefined}
								tabindex={canJumpToOrigin(thread) ? 0 : undefined}
								title={canJumpToOrigin(thread) ? 'Open origin message context' : thread.name}
								on:click|stopPropagation={() => jumpToOrigin(thread)}
								on:keydown={(event) => onAvatarKeyDown(event, thread)}
							>
								{getTreeAvatarLabel(thread)}
							</span>
							<span class="item-main">
								<span class="item-top">
									<span class="room-name-wrap">
										<span class="status-dot green"></span>
										<span class="room-name">{thread.name}</span>
										{#if thread.requiresPassword}
											<span class="room-lock" title="Password protected room">🔒</span>
										{/if}
									</span>
									<span class="room-time">{formatClock(thread.lastActivity)}</span>
								</span>
								<span class="item-bottom">
									<span class="room-preview">{getThreadPreview(thread)}</span>
									<span class="badges">
										{#if hasChildren(thread.id)}
											<span
												class="branch-badge"
												title={`${getChildCount(thread.id)} breakaway room(s)`}
											>
												<svg
													width="12"
													height="12"
													viewBox="0 0 24 24"
													fill="none"
													stroke="currentColor"
													stroke-width="2.5"
													stroke-linecap="round"
													stroke-linejoin="round"
													aria-hidden="true"
												>
													<line x1="6" y1="3" x2="6" y2="15"></line>
													<circle cx="18" cy="6" r="3"></circle>
													<circle cx="6" cy="18" r="3"></circle>
													<path d="M18 9a9 9 0 0 1-9 9"></path>
												</svg>
												{getChildCount(thread.id)}
											</span>
										{/if}
										{#if getUnreadBadgeCount(thread) > 0}
											<span class="unread">{getUnreadBadgeCount(thread)}</span>
										{/if}
									</span>
								</span>
							</span>
						</button>
					{/each}
				{/if}

				{#if discoverableRooms.length > 0}
					<div class="section-label">Discover</div>
					{#each discoverableRooms as thread (thread.id)}
						<button
							type="button"
							class={getRoomItemClasses(thread)}
							on:click={() => onThreadSelect(thread)}
						>
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<!-- svelte-ignore a11y_click_events_have_key_events -->
							<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
							<span
								class="avatar {isChildThread(thread)
									? 'child-avatar'
									: 'original-avatar'} {canJumpToOrigin(thread) ? 'jumpable' : ''}"
								role={canJumpToOrigin(thread) ? 'button' : undefined}
								tabindex={canJumpToOrigin(thread) ? 0 : undefined}
								title={canJumpToOrigin(thread) ? 'Open origin message context' : thread.name}
								on:click|stopPropagation={() => jumpToOrigin(thread)}
								on:keydown={(event) => onAvatarKeyDown(event, thread)}
							>
								{getTreeAvatarLabel(thread)}
							</span>
							<span class="item-main">
								<span class="item-top">
									<span class="room-name-wrap">
										<span class="status-dot orange"></span>
										<span class="room-name">{thread.name}</span>
										{#if thread.requiresPassword}
											<span class="room-lock" title="Password protected room">🔒</span>
										{/if}
									</span>
									<span class="room-time">{formatClock(thread.lastActivity)}</span>
								</span>
								<span class="item-bottom">
									<span class="room-preview">{getThreadPreview(thread)}</span>
									<span class="badges">
										{#if hasChildren(thread.id)}
											<span
												class="branch-badge"
												title={`${getChildCount(thread.id)} breakaway room(s)`}
											>
												<svg
													width="12"
													height="12"
													viewBox="0 0 24 24"
													fill="none"
													stroke="currentColor"
													stroke-width="2.5"
													stroke-linecap="round"
													stroke-linejoin="round"
													aria-hidden="true"
												>
													<line x1="6" y1="3" x2="6" y2="15"></line>
													<circle cx="18" cy="6" r="3"></circle>
													<circle cx="6" cy="18" r="3"></circle>
													<path d="M18 9a9 9 0 0 1-9 9"></path>
												</svg>
												{getChildCount(thread.id)}
											</span>
										{/if}
										{#if getUnreadBadgeCount(thread) > 0}
											<span class="unread">{getUnreadBadgeCount(thread)}</span>
										{/if}
									</span>
								</span>
							</span>
						</button>
					{/each}
				{/if}

				{#if leftRooms.length > 0}
					<div class="section-label">Left (Child Nav)</div>
					{#each leftRooms as thread (thread.id)}
						<button
							type="button"
							class={getRoomItemClasses(thread)}
							on:click={() => onThreadSelect(thread)}
						>
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<!-- svelte-ignore a11y_click_events_have_key_events -->
							<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
							<span
								class="avatar {isChildThread(thread)
									? 'child-avatar'
									: 'original-avatar'} {canJumpToOrigin(thread) ? 'jumpable' : ''}"
								role={canJumpToOrigin(thread) ? 'button' : undefined}
								tabindex={canJumpToOrigin(thread) ? 0 : undefined}
								title={canJumpToOrigin(thread) ? 'Open origin message context' : thread.name}
								on:click|stopPropagation={() => jumpToOrigin(thread)}
								on:keydown={(event) => onAvatarKeyDown(event, thread)}
							>
								{getTreeAvatarLabel(thread)}
							</span>
							<span class="item-main">
								<span class="item-top">
									<span class="room-name-wrap">
										<span class="status-dot gray"></span>
										<span class="room-name">{thread.name}</span>
										{#if thread.requiresPassword}
											<span class="room-lock" title="Password protected room">🔒</span>
										{/if}
									</span>
									<span class="room-time">{formatClock(thread.lastActivity)}</span>
								</span>
								<span class="item-bottom">
									<span class="room-preview">{getThreadPreview(thread)}</span>
									<span class="badges">
										{#if hasChildren(thread.id)}
											<span
												class="branch-badge"
												title={`${getChildCount(thread.id)} breakaway room(s)`}
											>
												<svg
													width="12"
													height="12"
													viewBox="0 0 24 24"
													fill="none"
													stroke="currentColor"
													stroke-width="2.5"
													stroke-linecap="round"
													stroke-linejoin="round"
													aria-hidden="true"
												>
													<line x1="6" y1="3" x2="6" y2="15"></line>
													<circle cx="18" cy="6" r="3"></circle>
													<circle cx="6" cy="18" r="3"></circle>
													<path d="M18 9a9 9 0 0 1-9 9"></path>
												</svg>
												{getChildCount(thread.id)}
											</span>
										{/if}
										{#if getUnreadBadgeCount(thread) > 0}
											<span class="unread">{getUnreadBadgeCount(thread)}</span>
										{/if}
									</span>
								</span>
							</span>
						</button>
					{/each}
				{/if}
			{/if}
		{:else}
			{#if streamlinedParentRoomId}
				<div class="streamlined-context">
					<button type="button" class="streamlined-back" on:click={openRootRoomList}>
						<span aria-hidden="true">&larr;</span>
						<span>Back</span>
					</button>
					<span class="streamlined-parent" title={activeStreamlinedParent?.name || ''}>
						{activeStreamlinedParent?.name || 'Room'}
					</span>
				</div>
			{/if}
			{#if streamlinedThreads.length === 0}
				<div class="empty-label">
					{streamlinedParentRoomId ? 'No child rooms yet.' : 'No chats matched your search.'}
				</div>
			{:else}
				{#each streamlinedThreads as thread (thread.id)}
					<button
						type="button"
						class={getRoomItemClasses(thread)}
						on:click={() => onThreadSelect(thread)}
					>
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<!-- svelte-ignore a11y_click_events_have_key_events -->
						<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
						<span
							class="avatar {isChildThread(thread)
								? 'child-avatar'
								: 'original-avatar'} {canJumpToOrigin(thread) ? 'jumpable' : ''}"
							role={canJumpToOrigin(thread) ? 'button' : undefined}
							tabindex={canJumpToOrigin(thread) ? 0 : undefined}
							title={canJumpToOrigin(thread) ? 'Open origin message context' : thread.name}
							on:click|stopPropagation={() => jumpToOrigin(thread)}
							on:keydown={(event) => onAvatarKeyDown(event, thread)}
						>
							{getTreeAvatarLabel(thread)}
						</span>
						<span class="item-main">
							<span class="item-top">
								<span class="room-name-wrap">
									<span
										class="status-dot {thread.status === 'joined'
											? 'green'
											: thread.status === 'left'
												? 'gray'
												: 'orange'}"
									></span>
									<span class="room-name">{thread.name}</span>
									{#if thread.requiresPassword}
										<span class="room-lock" title="Password protected room">🔒</span>
									{/if}
								</span>
								<span class="room-time">{formatClock(thread.lastActivity)}</span>
							</span>
							<span class="item-bottom">
								<span class="room-preview">{getThreadPreview(thread)}</span>
								<span class="badges">
									{#if hasChildren(thread.id)}
										<span class="branch-badge" title={`${getChildCount(thread.id)} breakaway room(s)`}>
											<svg
												width="12"
												height="12"
												viewBox="0 0 24 24"
												fill="none"
												stroke="currentColor"
												stroke-width="2.5"
												stroke-linecap="round"
												stroke-linejoin="round"
												aria-hidden="true"
											>
												<line x1="6" y1="3" x2="6" y2="15"></line>
												<circle cx="18" cy="6" r="3"></circle>
												<circle cx="6" cy="18" r="3"></circle>
												<path d="M18 9a9 9 0 0 1-9 9"></path>
											</svg>
											{getChildCount(thread.id)}
										</span>
									{/if}
									{#if getUnreadBadgeCount(thread) > 0}
										<span class="unread">{getUnreadBadgeCount(thread)}</span>
									{/if}
								</span>
							</span>
						</span>
					</button>
				{/each}
			{/if}
		{/if}
	</div>
</aside>

{#if showRelationsMap}
	<button
		type="button"
		class="relations-backdrop"
		aria-label="Close relations map"
		on:click={closeRelationsMap}
	></button>
	<section class="relations-modal" role="dialog" aria-modal="true" bind:this={relationsPanelEl}>
		<header class="relations-header">
			<h3>Relations Map</h3>
			<button type="button" class="close-map-button" on:click={closeRelationsMap}>x</button>
		</header>
		<div class="relations-nav">
			<button type="button" class="tree-nav-button" on:click={gotoPrevTree}>&lt;</button>
			<div class="tree-meta">
				{#if relationRootIds.length === 0}
					No relation trees
				{:else}
					Tree {activeTreeIndex + 1} / {relationRootIds.length}
				{/if}
			</div>
			<button type="button" class="tree-nav-button" on:click={gotoNextTree}>&gt;</button>
		</div>
		{#if currentTreeRows.length === 0}
			<div class="empty-label">Create a break room to build your first tree.</div>
		{:else}
			<div class="relations-tree-viewport">
				<div
					class="relations-tree-canvas"
					style="width: {treeLayout.width}px; height: {treeLayout.height}px;"
				>
					<svg
						class="tree-lines"
						viewBox={`0 0 ${treeLayout.width} ${treeLayout.height}`}
						preserveAspectRatio="none"
					>
						{#each treeEdges as edge (edge.id)}
							<path d={edge.path} class="tree-link"></path>
						{/each}
					</svg>
					{#each treeNodes as node (node.id)}
						<button
							type="button"
							class="tree-node {node.id === activeRoomId ? 'active' : ''} {node.id ===
							activeParentRoomId
								? 'is-parent'
								: ''} {node.thread.parentRoomId ? 'is-child' : 'is-root'}"
							style="left: {node.x}px; top: {node.y}px;"
							on:click={() => openRoomFromTree(node.thread)}
						>
							<span class="tree-name">{node.thread.name}</span>
							<span class="tree-state {node.thread.status}"></span>
						</button>
					{/each}
				</div>
			</div>
		{/if}
	</section>
{/if}

<style>
	.room-list {
		display: flex;
		flex-direction: column;
		border-right: none;
		background: linear-gradient(180deg, #eef3f9 0%, #e4eaf3 100%);
		width: 100%;
		max-width: 100%;
		height: 100%;
		min-height: 0;
		overflow: hidden;
	}

	.room-list.theme-dark {
		background: linear-gradient(180deg, #0f1729 0%, #0b1323 100%);
		color: #dbe5f8;
	}

	.room-list-header {
		padding: 0.95rem 0.95rem 0.72rem;
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.5rem;
		min-width: 0;
		position: relative;
		border-bottom: 1px solid #d4dce8;
	}

	.room-list.theme-dark .room-list-header {
		border-bottom-color: #253049;
	}

	.list-title {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-width: 0;
		flex: 1;
	}

	.list-actions {
		position: relative;
		display: inline-flex;
		gap: 0.35rem;
		align-items: center;
		flex-shrink: 0;
	}

	.room-list-header h2 {
		margin: 0;
		font-size: 1.05rem;
	}

	.thread-count {
		font-size: 0.85rem;
		font-weight: 700;
		color: #ffffff;
		background: #556683;
		padding: 0.2rem 0.5rem;
		border-radius: 999px;
	}

	.room-list.theme-dark .thread-count {
		background: #1f293d;
		color: #cdd9f0;
	}

	.room-list-search {
		padding: 0 1rem 0.75rem;
	}

	.room-list-search input {
		width: 100%;
		border: 1px solid #c8d1de;
		border-radius: 8px;
		padding: 0.55rem 0.7rem;
		font-size: 0.92rem;
		background: #edf2f8;
		color: #29374d;
	}

	.room-list.theme-dark .room-list-search input {
		border-color: #2c3852;
		background: #111b2f;
		color: #dbe7ff;
	}

	.room-items {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
		display: flex;
		flex-direction: column;
		gap: 0.36rem;
		padding: 0.5rem 0.5rem;
		-webkit-overflow-scrolling: touch;
		overscroll-behavior: contain;
		scrollbar-width: none;
		-ms-overflow-style: none;
	}

	.room-items::-webkit-scrollbar {
		width: 0;
		height: 0;
		display: none;
	}

	.section-label {
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: #627186;
		padding: 0.55rem 0.9rem 0.35rem;
		flex: 0 0 auto;
	}

	.room-list.theme-dark .section-label {
		color: #93a4c4;
	}

	.room-item {
		width: 100%;
		max-width: 100%;
		min-width: 0;
		margin: 0;
		flex: 0 0 auto;
		display: flex;
		gap: 0.75rem;
		padding: 0.8rem 0.85rem;
		border: 1px solid #ccd5e1;
		border-radius: 12px;
		text-align: left;
		background: #f3f6fa;
		box-shadow: 0 2px 6px rgba(15, 23, 42, 0.08);
		cursor: pointer;
		transition:
			background 140ms ease,
			color 140ms ease,
			border-color 140ms ease,
			box-shadow 140ms ease,
			transform 140ms ease;
		box-sizing: border-box;
		overflow: hidden;
	}

	.room-list.theme-dark .room-item {
		border-color: #2b3851;
		background: #121d33;
		box-shadow: 0 3px 10px rgba(2, 8, 23, 0.38);
	}

	.room-item:hover {
		background: #dee7f2;
		border-color: #aebed1;
		transform: translateY(-1px);
		box-shadow: 0 8px 20px rgba(15, 23, 42, 0.12);
	}

	.room-list.theme-dark .room-item:hover {
		background: #20304d;
		border-color: #4b5f82;
		box-shadow: 0 8px 20px rgba(2, 8, 23, 0.5);
	}

	.room-item.related-child {
		border-color: rgba(245, 158, 11, 0.88);
		box-shadow: 0 0 0 1px rgba(245, 158, 11, 0.28);
	}

	.room-list.theme-dark .room-item.related-child {
		border-color: rgba(245, 158, 11, 0.95);
		box-shadow:
			0 0 0 1px rgba(245, 158, 11, 0.55),
			0 6px 14px rgba(2, 8, 23, 0.4);
	}

	.room-item.related-parent {
		border-color: rgba(34, 197, 94, 0.9);
		box-shadow: 0 0 0 1px rgba(34, 197, 94, 0.26);
	}

	.room-list.theme-dark .room-item.related-parent {
		border-color: rgba(34, 197, 94, 0.95);
		box-shadow:
			0 0 0 1px rgba(34, 197, 94, 0.5),
			0 6px 14px rgba(2, 8, 23, 0.4);
	}

	.room-item.selected {
		background: #4a5d7a;
		border-color: #3f526e;
		box-shadow: 0 10px 22px rgba(63, 82, 110, 0.34);
	}

	.room-list.theme-dark .room-item.selected {
		background: #26344a;
		border-color: #3e5474;
		box-shadow: 0 10px 22px rgba(8, 13, 24, 0.58);
	}

	.room-list.theme-dark .room-item.related-child.selected {
		border-color: rgba(245, 158, 11, 0.96);
		box-shadow:
			0 0 0 1px rgba(245, 158, 11, 0.62),
			0 8px 16px rgba(2, 8, 23, 0.45);
	}

	.room-list.theme-dark .room-item.related-parent.selected {
		border-color: rgba(34, 197, 94, 0.96);
		box-shadow:
			0 0 0 1px rgba(34, 197, 94, 0.58),
			0 8px 16px rgba(2, 8, 23, 0.45);
	}

	.room-item.discoverable.selected {
		background: #5d6b84;
		border-color: #5d6b84;
	}

	.room-item.left {
		border-style: dashed;
	}

	.room-item.left.selected {
		background: #646f84;
		border-color: #646f84;
	}

	.avatar {
		width: 38px;
		height: 38px;
		border-radius: 50%;
		background: #e2e8f1;
		color: #334158;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		font-weight: 700;
		font-size: 0.78rem;
		line-height: 1;
		font-variant-numeric: tabular-nums;
		flex-shrink: 0;
	}

	.original-avatar {
		background: #c6d0df;
		color: #263449;
		box-shadow: inset 0 0 0 2px rgba(80, 95, 120, 0.24);
	}

	.child-avatar {
		background: #dfe7f2;
		color: #334158;
		box-shadow: inset 0 0 0 2px rgba(245, 158, 11, 0.58);
	}

	.child-avatar:hover {
		box-shadow: inset 0 0 0 2px #f59e0b;
	}

	.jumpable {
		cursor: pointer;
	}

	.jumpable:hover {
		box-shadow: inset 0 0 0 2px #f59e0b;
	}

	.item-main {
		min-width: 0;
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 0.35rem;
	}

	.item-top,
	.item-bottom {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.6rem;
		min-width: 0;
	}

	.room-name-wrap {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		min-width: 0;
		flex: 1;
	}

	.status-dot {
		width: 8px;
		height: 8px;
		border-radius: 999px;
		flex-shrink: 0;
		border: 1px solid rgba(0, 0, 0, 0.22);
	}

	.status-dot.green {
		background: #22c55e;
	}

	.status-dot.orange {
		background: #f59e0b;
	}

	.status-dot.gray {
		background: #9ca3af;
	}

	.room-name {
		font-size: 0.92rem;
		font-weight: 600;
		color: #1f2d42;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.room-lock {
		font-size: 0.74rem;
		line-height: 1;
		opacity: 0.9;
		color: #a16207;
		flex-shrink: 0;
	}

	.room-list.theme-dark .room-name {
		color: #e6eefc;
	}

	.room-list.theme-dark .room-lock {
		color: #fbbf24;
	}

	.room-time {
		font-size: 0.78rem;
		color: #627087;
		white-space: nowrap;
		flex-shrink: 0;
		max-width: 4rem;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.room-list.theme-dark .room-time {
		color: #9eb0d0;
	}

	.room-preview {
		font-size: 0.82rem;
		color: #5b697f;
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.room-list.theme-dark .room-preview {
		color: #a9b8d4;
	}

	.badges {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		flex-shrink: 0;
	}

	.branch-badge {
		display: inline-flex;
		align-items: center;
		gap: 0.25rem;
		padding: 0.15rem 0.45rem;
		background: #e2e8f1;
		color: #5b697f;
		border-radius: 6px;
		font-size: 0.72rem;
		font-weight: 700;
	}

	.room-list.theme-dark .branch-badge {
		background: #1e293b;
		color: #94a3b8;
	}

	.room-item.selected .branch-badge {
		background: #44546e;
		color: #cbd5e1;
	}

	.room-list.theme-dark .room-item.selected .branch-badge {
		background: #334155;
		color: #cbd5e1;
	}

	.unread {
		min-width: 22px;
		height: 22px;
		padding: 0 0.38rem;
		border-radius: 999px;
		background: #ef4444;
		color: #ffffff;
		font-size: 0.72rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		font-weight: 700;
		flex-shrink: 0;
	}

	.room-list.theme-dark .unread {
		background: #fb7185;
		color: #220b11;
	}

	.room-item.selected .unread {
		background: #f87171;
		color: #240d12;
	}

	.room-list.theme-dark .room-item.selected .unread {
		background: #fca5a5;
		color: #1f0b10;
	}

	.icon-button {
		border: 1px solid #c7d0de;
		background: #edf2f8;
		border-radius: 6px;
		width: 2rem;
		height: 2rem;
		padding: 0;
		font-size: 0.78rem;
		cursor: pointer;
		color: #324057;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		transition:
			background 140ms ease,
			border-color 140ms ease,
			color 140ms ease,
			transform 140ms ease;
	}

	.room-list.theme-dark .icon-button {
		border-color: #2b3853;
		background: #111b2f;
		color: #d6e1f6;
	}

	.icon-button:disabled {
		opacity: 0.55;
		cursor: not-allowed;
	}

	.icon-button:hover:not(:disabled) {
		background: #dfe8f4;
		border-color: #aebfd4;
		transform: translateY(-1px);
	}

	.room-list.theme-dark .icon-button:hover:not(:disabled) {
		background: #22324f;
		border-color: #41587d;
	}

	.map-icon-button {
		font-size: 0;
	}

	.theme-icon-button {
		font-size: 0;
	}

	.theme-icon-button.active {
		background: #53627c;
		border-color: #53627c;
		color: #ffffff;
	}

	.room-list.theme-dark .theme-icon-button.active {
		background: #1f304c;
		border-color: #35507d;
		color: #dbeafe;
	}

	.view-icon-button {
		font-size: 0;
	}

	.view-icon-button.active {
		background: #53627c;
		border-color: #53627c;
		color: #ffffff;
	}

	.room-list.theme-dark .view-icon-button.active {
		background: #1f304c;
		border-color: #35507d;
		color: #dbeafe;
	}

	.menu-icon-button {
		font-size: 0.8rem;
	}

	.streamlined-context {
		display: flex;
		align-items: center;
		justify-content: flex-start;
		gap: 0.5rem;
		padding: 0.2rem 0.35rem 0.35rem;
		margin-bottom: 0.2rem;
		border-bottom: 1px solid #d5dce7;
		flex: 0 0 auto;
	}

	.streamlined-back {
		border: 1px solid #c7d0de;
		background: #edf2f8;
		border-radius: 999px;
		padding: 0.18rem 0.55rem;
		font-size: 0.74rem;
		font-weight: 600;
		display: inline-flex;
		align-items: center;
		gap: 0.25rem;
		cursor: pointer;
		color: #39485f;
		flex-shrink: 0;
	}

	.streamlined-parent {
		font-size: 0.76rem;
		font-weight: 600;
		color: #5c6b81;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.room-menu {
		position: absolute;
		top: calc(100% + 6px);
		right: 0;
		background: #f4f7fb;
		border: 1px solid #cad3df;
		border-radius: 8px;
		box-shadow: 0 12px 24px rgba(0, 0, 0, 0.1);
		overflow: hidden;
		min-width: 138px;
		z-index: 100;
	}

	.room-list.theme-dark .room-menu {
		background: #111b2f;
		border-color: #2d3b57;
		box-shadow: 0 12px 24px rgba(2, 8, 23, 0.45);
	}

	.left-menu {
		left: 0;
		right: auto;
	}

	.room-menu button {
		width: 100%;
		border: none;
		background: #f4f7fb;
		padding: 0.55rem 0.75rem;
		text-align: left;
		font-size: 0.84rem;
		cursor: pointer;
	}

	.room-list.theme-dark .room-menu button {
		background: #111b2f;
		color: #dbe7ff;
	}

	.room-menu button:disabled {
		color: #9898a1;
		cursor: not-allowed;
	}

	.room-menu button:hover {
		background: #e7edf5;
	}

	.room-list.theme-dark .room-menu button:hover {
		background: #1b2a45;
	}

	.room-menu button:disabled:hover {
		background: #f4f7fb;
	}

	.room-item.selected .avatar {
		background: #44546e;
		color: #ffffff;
	}

	.room-item.selected .room-name,
	.room-item.selected .room-time,
	.room-item.selected .room-preview {
		color: #f3f3f3;
	}

	.room-item.selected .status-dot {
		border-color: rgba(255, 255, 255, 0.45);
	}

	.empty-label {
		color: #607087;
		font-size: 0.84rem;
		padding: 1rem;
		flex: 0 0 auto;
	}

	.room-list.theme-dark .empty-label {
		color: #9fb0ce;
	}

	.relations-backdrop {
		position: fixed;
		inset: 0;
		border: none;
		background: rgba(10, 10, 11, 0.33);
		z-index: 190;
	}

	.relations-modal {
		position: fixed;
		left: 50%;
		top: 50%;
		transform: translate(-50%, -50%);
		width: min(540px, calc(100vw - 2rem));
		max-height: min(640px, calc(100vh - 2rem));
		overflow: hidden;
		display: flex;
		flex-direction: column;
		border: 1px solid #ccd5e2;
		border-radius: 14px;
		background: #f4f7fb;
		box-shadow: 0 22px 52px rgba(0, 0, 0, 0.22);
		z-index: 200;
	}

	.relations-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0.75rem 0.95rem;
		border-bottom: 1px solid #d5dce7;
	}

	.relations-header h3 {
		margin: 0;
		font-size: 0.95rem;
	}

	.close-map-button {
		border: 1px solid #c8d1de;
		background: #edf2f8;
		color: #2f3d54;
		border-radius: 7px;
		padding: 0.24rem 0.52rem;
		cursor: pointer;
	}

	.relations-nav {
		display: grid;
		grid-template-columns: 2.2rem 1fr 2.2rem;
		align-items: center;
		gap: 0.5rem;
		padding: 0.6rem 0.8rem;
		border-bottom: 1px solid #dfe5ee;
	}

	.tree-nav-button {
		border: 1px solid #c8d1de;
		background: #edf2f8;
		border-radius: 8px;
		height: 2rem;
		cursor: pointer;
	}

	.tree-meta {
		font-size: 0.82rem;
		color: #5c6b81;
		text-align: center;
	}

	.relations-tree-viewport {
		padding: 0.7rem 0.8rem 0.95rem;
		overflow: auto;
		flex: 1;
		min-height: 0;
	}

	.relations-tree-canvas {
		position: relative;
	}

	.tree-lines {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		overflow: visible;
	}

	.tree-link {
		fill: none;
		stroke: rgba(120, 125, 136, 0.6);
		stroke-width: 2;
		stroke-linecap: round;
	}

	.tree-node {
		position: absolute;
		width: 170px;
		height: 48px;
		border: 1px solid #ccd5e2;
		background: #edf2f8;
		border-radius: 10px;
		padding: 0.58rem 0.65rem;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
		text-align: left;
		cursor: pointer;
		transition:
			border-color 120ms ease,
			box-shadow 120ms ease,
			background 120ms ease;
	}

	.tree-node:hover {
		border-color: #b9c5d6;
		box-shadow: 0 1px 10px rgba(15, 23, 42, 0.12);
	}

	.tree-node.is-root {
		border-left: 3px solid #4b5563;
	}

	.tree-node.is-child {
		border-left: 3px solid #f59e0b;
	}

	.tree-node.is-parent {
		border-left: 3px solid #22c55e;
	}

	.tree-node.active {
		background: #53627c;
		border-color: #53627c;
		color: #ffffff;
	}

	.tree-name {
		font-size: 0.86rem;
		font-weight: 600;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		max-width: 130px;
	}

	.tree-state {
		width: 8px;
		height: 8px;
		border-radius: 999px;
		flex-shrink: 0;
	}

	.tree-state.joined {
		background: #22c55e;
	}

	.tree-state.discoverable {
		background: #f59e0b;
	}

	.tree-state.left {
		background: #9ca3af;
	}

	.room-list.theme-dark {
		background: linear-gradient(180deg, #0b0b0d 0%, #121214 100%);
		color: #ececf2;
	}

	.room-list.theme-dark .room-list-header {
		border-bottom-color: #2b2b30;
	}

	.room-list.theme-dark .thread-count {
		background: #2a2a2f;
		color: #ececf2;
	}

	.room-list.theme-dark .room-list-search input {
		border-color: #37373c;
		background: #17171a;
		color: #f0f0f5;
	}

	.room-list.theme-dark .section-label {
		color: #a9a9b2;
	}

	.room-list.theme-dark .room-item {
		border-color: #333338;
		background: #17171a;
		box-shadow: 0 3px 10px rgba(0, 0, 0, 0.38);
	}

	.room-list.theme-dark .room-item:hover {
		background: #202024;
		border-color: #404046;
	}

	.room-list.theme-dark .room-item.selected {
		background: #25252a;
		border-color: #45454b;
	}

	.room-list.theme-dark .room-item.related-child {
		border-color: rgba(245, 158, 11, 0.95);
		box-shadow:
			0 0 0 1px rgba(245, 158, 11, 0.55),
			0 6px 14px rgba(0, 0, 0, 0.4);
	}

	.room-list.theme-dark .room-item.related-parent {
		border-color: rgba(34, 197, 94, 0.95);
		box-shadow:
			0 0 0 1px rgba(34, 197, 94, 0.5),
			0 6px 14px rgba(0, 0, 0, 0.4);
	}

	.room-list.theme-dark .room-name {
		color: #f0f0f5;
	}

	.room-list.theme-dark .room-time {
		color: #b0b0b8;
	}

	.room-list.theme-dark .room-preview {
		color: #b8b8c0;
	}

	.room-list.theme-dark .status-dot.green {
		background: #22c55e;
	}

	.room-list.theme-dark .status-dot.orange {
		background: #f59e0b;
	}

	.room-list.theme-dark .status-dot.gray {
		background: #8f8f98;
	}

	.room-list.theme-dark .branch-badge {
		background: #27272c;
		color: #c0c0c8;
	}

	.room-list.theme-dark .room-item.selected .branch-badge {
		background: #333338;
		color: #e0e0e7;
	}

	.room-list.theme-dark .unread {
		background: #cfcfd8;
		color: #161618;
	}

	.room-list.theme-dark .room-item.selected .unread {
		background: #e1e1e9;
		color: #131314;
	}

	.room-list.theme-dark .icon-button {
		border-color: #35353a;
		background: #17171a;
		color: #ececf2;
	}

	.room-list.theme-dark .theme-icon-button.active,
	.room-list.theme-dark .view-icon-button.active {
		background: #2b2b30;
		border-color: #44444a;
		color: #f2f2f6;
	}

	.room-list.theme-dark .streamlined-back {
		border-color: #36363c;
		background: #19191c;
		color: #e7e7ed;
	}

	.room-list.theme-dark .streamlined-parent {
		color: #b3b3bc;
	}

	.room-list.theme-dark .room-menu {
		background: #17171a;
		border-color: #333338;
		box-shadow: 0 12px 24px rgba(0, 0, 0, 0.45);
	}

	.room-list.theme-dark .room-menu button {
		background: #17171a;
		color: #ececf2;
	}

	.room-list.theme-dark .room-menu button:hover {
		background: #222226;
	}

	.room-list.theme-dark .empty-label {
		color: #afafb8;
	}

	@media (max-width: 900px) {
		.room-list {
			border-right: none;
		}

		.room-list-header {
			padding-top: 0.85rem;
		}
	}
</style>
