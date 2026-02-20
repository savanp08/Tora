<script lang="ts">
	import { createEventDispatcher, onMount } from 'svelte';
	import IconSet from '$lib/components/icons/IconSet.svelte';

	type ThreadStatus = 'joined' | 'discoverable';

	type ChatThread = {
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
	};

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
	export let accessibleParentRoomIds: string[] = [];
	export let activeRoomId = '';
	export let showLeftMenu = false;
	export let chatListSearch = '';

	const dispatch = createEventDispatcher<{
		select: { id: string; isMember: boolean; status: ThreadStatus };
		jumpOrigin: {
			parentRoomId: string;
			originMessageId: string;
			fallbackRoomId: string;
			fallbackIsMember: boolean;
		};
		toggleMenu: void;
		createRoom: void;
		renameRoom: { roomId: string };
	}>();

	let showRelationsMap = false;
	let activeTreeIndex = 0;
	let isFullView = false;
	let streamlinedParentRoomId = '';
	let streamlinedManualRootList = false;
	let relationsPanelEl: HTMLElement | null = null;
	let relationsTriggerEl: HTMLButtonElement | null = null;
	const treeNodeWidth = 170;
	const treeNodeHeight = 48;
	const treeColumnGap = 230;
	const treeRowGap = 84;
	const treePadX = 24;
	const treePadY = 24;

	$: totalRooms = myRooms.length + discoverableRooms.length;
	$: allThreads = dedupeThreads([...myRooms, ...discoverableRooms]);
	$: threadByID = new Map(allThreads.map((thread) => [thread.id, thread]));
	$: childrenByParent = buildChildrenByParent(allThreads);
	$: rootThreads = sortSidebarThreads(
		allThreads.filter((thread) => !thread.parentRoomId || !threadByID.has(thread.parentRoomId))
	);
	$: if (streamlinedParentRoomId && !threadByID.has(streamlinedParentRoomId)) {
		streamlinedParentRoomId = '';
	}
	$: if (isFullView) {
		streamlinedManualRootList = false;
	}
	$: if (!isFullView) {
		const active = threadByID.get(activeRoomId);
		// Keep sidebar and chat route in sync: child room routes always open the child tree view.
		if (active?.parentRoomId) {
			streamlinedManualRootList = false;
		}
	}
	$: if (!isFullView && !streamlinedManualRootList) {
		const contextRoomID = getStreamlinedContextRoomID();
		if (contextRoomID !== streamlinedParentRoomId) {
			streamlinedParentRoomId = contextRoomID;
		}
	}
	$: activeStreamlinedParent = threadByID.get(streamlinedParentRoomId);
	$: descendantThreads = streamlinedParentRoomId
		? sortSidebarThreads(flattenTree(streamlinedParentRoomId).slice(1).map((row) => row.thread))
		: [];
	$: streamlinedThreads = streamlinedParentRoomId ? descendantThreads : rootThreads;
	$: accessibleParents = new Set(
		accessibleParentRoomIds.length > 0
			? accessibleParentRoomIds
			: [...myRooms.map((room) => room.id), ...discoverableRooms.map((room) => room.id)]
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
						: 'discoverable'
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
				[...children].sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }))
			);
		}
		return index;
	}

	function sortSidebarThreads(threads: ChatThread[]) {
		return [...threads].sort((a, b) => {
			if (a.status !== b.status) {
				return a.status === 'joined' ? -1 : 1;
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

		const width = treePadX*2 + maxDepth*treeColumnGap + treeNodeWidth;
		const height = treePadY*2 + (maxRowsInColumn-1)*treeRowGap + treeNodeHeight;
		return { nodes, edges, width, height };
	}

	function getTreeRootID(threadID: string) {
		let cursor = threadID;
		const seen = new Set<string>();
		while (cursor) {
			if (seen.has(cursor)) {
				break;
			}
			seen.add(cursor);
			const node = threadByID.get(cursor);
			const parentID = node?.parentRoomId || '';
			if (!parentID || !threadByID.has(parentID)) {
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

	function openRootRoomList() {
		streamlinedManualRootList = true;
		streamlinedParentRoomId = '';
	}

	function onThreadSelect(thread: ChatThread) {
		streamlinedManualRootList = false;
		selectRoom(thread);
		if (!isFullView && hasChildren(thread.id)) {
			streamlinedParentRoomId = thread.id;
		}
	}

	function getStreamlinedContextRoomID() {
		const active = threadByID.get(activeRoomId);
		if (!active) {
			return '';
		}
		const rootID = getTreeRootID(active.id);
		if (rootID && hasChildren(rootID)) {
			return rootID;
		}
		if (hasChildren(active.id)) {
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
		return Boolean(
			hasBreakOrigin(thread) &&
				thread.parentRoomId &&
				accessibleParents.has(thread.parentRoomId)
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
		return thread.status === 'joined' ? 'No messages yet' : 'Preview and join';
	}
</script>

<aside class="room-list">
	<div class="room-list-header">
		<div class="list-title">
			<h2>Chats</h2>
			<span class="thread-count">{totalRooms}</span>
		</div>
		<div class="list-actions">
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
					<button type="button" on:click={requestRenameRoom} disabled={!activeRoomId}>Rename room</button>
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
			{#if myRooms.length === 0 && discoverableRooms.length === 0}
				<div class="empty-label">No chats matched your search.</div>
			{:else}
				{#if myRooms.length > 0}
					<div class="section-label">Joined</div>
					{#each myRooms as thread (thread.id)}
						<button type="button" class={getRoomItemClasses(thread)} on:click={() => onThreadSelect(thread)}>
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<!-- svelte-ignore a11y_click_events_have_key_events -->
							<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
							<span
								class="avatar {isChildThread(thread) ? 'child-avatar' : 'original-avatar'} {canJumpToOrigin(thread)
									? 'jumpable'
									: ''}"
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
									</span>
									<span class="room-time">{formatClock(thread.lastActivity)}</span>
								</span>
								<span class="item-bottom">
									<span class="room-preview">{getThreadPreview(thread)}</span>
									{#if thread.unread > 0}
										<span class="unread">{thread.unread}</span>
									{/if}
								</span>
							</span>
						</button>
					{/each}
				{/if}

				{#if discoverableRooms.length > 0}
					<div class="section-label">Discover</div>
					{#each discoverableRooms as thread (thread.id)}
						<button type="button" class={getRoomItemClasses(thread)} on:click={() => onThreadSelect(thread)}>
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<!-- svelte-ignore a11y_click_events_have_key_events -->
							<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
							<span
								class="avatar {isChildThread(thread) ? 'child-avatar' : 'original-avatar'} {canJumpToOrigin(thread)
									? 'jumpable'
									: ''}"
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
									</span>
									<span class="room-time">{formatClock(thread.lastActivity)}</span>
								</span>
								<span class="item-bottom">
									<span class="room-preview">{getThreadPreview(thread)}</span>
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
					<button type="button" class={getRoomItemClasses(thread)} on:click={() => onThreadSelect(thread)}>
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<!-- svelte-ignore a11y_click_events_have_key_events -->
						<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
						<span
							class="avatar {isChildThread(thread) ? 'child-avatar' : 'original-avatar'} {canJumpToOrigin(thread)
								? 'jumpable'
								: ''}"
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
									<span class="status-dot {thread.status === 'joined' ? 'green' : 'orange'}"></span>
									<span class="room-name">{thread.name}</span>
								</span>
								<span class="room-time">{formatClock(thread.lastActivity)}</span>
							</span>
							<span class="item-bottom">
								<span class="room-preview">{getThreadPreview(thread)}</span>
								{#if thread.status === 'joined' && thread.unread > 0}
									<span class="unread">{thread.unread}</span>
								{/if}
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
							class="tree-node {node.id === activeRoomId ? 'active' : ''} {node.id === activeParentRoomId
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
		border-right: 1px solid #dfdfe4;
		background: #f5f5f6;
		width: 100%;
		max-width: 100%;
		height: 100%;
		min-height: 0;
		overflow-x: hidden;
	}

	.room-list-header {
		padding: 0.95rem 0.95rem 0.72rem;
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.5rem;
		min-width: 0;
		position: relative;
		border-bottom: 1px solid #e3e3e8;
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
		background: #2f3138;
		padding: 0.2rem 0.5rem;
		border-radius: 999px;
	}

	.room-list-search {
		padding: 0 1rem 0.75rem;
	}

	.room-list-search input {
		width: 100%;
		border: 1px solid #d6d6dc;
		border-radius: 8px;
		padding: 0.55rem 0.7rem;
		font-size: 0.92rem;
		background: #fafafb;
		color: #2b2b33;
	}

	.room-items {
		flex: 1;
		overflow: auto;
		overflow-x: hidden;
		display: flex;
		flex-direction: column;
		gap: 0.36rem;
		padding: 0.5rem 0.5rem;
	}

	.section-label {
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: #666666;
		padding: 0.55rem 0.9rem 0.35rem;
	}

	.room-item {
		width: 100%;
		max-width: 100%;
		min-width: 0;
		margin: 0;
		display: flex;
		gap: 0.75rem;
		padding: 0.8rem 0.85rem;
		border: 1px solid #e1e1e7;
		border-radius: 12px;
		text-align: left;
		background: #fcfcfd;
		box-shadow: 0 2px 6px rgba(0, 0, 0, 0.04);
		cursor: pointer;
		transition:
			background 140ms ease,
			color 140ms ease,
			border-color 140ms ease;
		box-sizing: border-box;
		overflow: hidden;
	}

	.room-item:hover {
		background: #f3f3f5;
		border-color: #ccccd4;
	}

	.room-item.related-child {
		border-color: rgba(245, 158, 11, 0.88);
		box-shadow: 0 0 0 1px rgba(245, 158, 11, 0.28);
	}

	.room-item.related-parent {
		border-color: rgba(34, 197, 94, 0.9);
		box-shadow: 0 0 0 1px rgba(34, 197, 94, 0.26);
	}

	.room-item.selected {
		background: #2f3138;
		border-color: #2f3138;
	}

	.room-item.discoverable.selected {
		background: #3a3d45;
		border-color: #3a3d45;
	}

	.avatar {
		width: 38px;
		height: 38px;
		border-radius: 50%;
		background: #ececef;
		color: #2f3138;
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
		background: #c8cbd2;
		color: #1f2228;
		box-shadow: inset 0 0 0 2px rgba(47, 49, 56, 0.18);
	}

	.child-avatar {
		background: #e7e9ee;
		color: #2f3138;
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

	.room-name {
		font-size: 0.92rem;
		font-weight: 600;
		color: #161616;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.room-time {
		font-size: 0.78rem;
		color: #72727c;
		white-space: nowrap;
		flex-shrink: 0;
		max-width: 4rem;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.room-preview {
		font-size: 0.82rem;
		color: #696974;
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.unread {
		min-width: 20px;
		height: 20px;
		border-radius: 999px;
		background: #2f3138;
		color: #ffffff;
		font-size: 0.75rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		font-weight: 700;
	}

	.icon-button {
		border: 1px solid #d5d5dc;
		background: #f8f8f9;
		border-radius: 6px;
		width: 2rem;
		height: 2rem;
		padding: 0;
		font-size: 0.78rem;
		cursor: pointer;
		color: #33333b;
		display: inline-flex;
		align-items: center;
		justify-content: center;
	}

	.icon-button:disabled {
		opacity: 0.55;
		cursor: not-allowed;
	}

	.map-icon-button {
		font-size: 0;
	}

	.view-icon-button {
		font-size: 0;
	}

	.view-icon-button.active {
		background: #2f3138;
		border-color: #2f3138;
		color: #ffffff;
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
		border-bottom: 1px solid #e6e6ec;
	}

	.streamlined-back {
		border: 1px solid #d6d6dc;
		background: #f8f8f9;
		border-radius: 999px;
		padding: 0.18rem 0.55rem;
		font-size: 0.74rem;
		font-weight: 600;
		display: inline-flex;
		align-items: center;
		gap: 0.25rem;
		cursor: pointer;
		color: #3a3a42;
		flex-shrink: 0;
	}

	.streamlined-parent {
		font-size: 0.76rem;
		font-weight: 600;
		color: #5d5d66;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.room-menu {
		position: absolute;
		top: calc(100% + 6px);
		right: 0;
		background: #fcfcfd;
		border: 1px solid #dedee4;
		border-radius: 8px;
		box-shadow: 0 12px 24px rgba(0, 0, 0, 0.1);
		overflow: hidden;
		min-width: 138px;
		z-index: 100;
	}

	.left-menu {
		left: 0;
		right: auto;
	}

	.room-menu button {
		width: 100%;
		border: none;
		background: #fcfcfd;
		padding: 0.55rem 0.75rem;
		text-align: left;
		font-size: 0.84rem;
		cursor: pointer;
	}

	.room-menu button:disabled {
		color: #9898a1;
		cursor: not-allowed;
	}

	.room-menu button:hover {
		background: #f1f1f3;
	}

	.room-menu button:disabled:hover {
		background: #fcfcfd;
	}

	.room-item.selected .avatar {
		background: #2b2b2b;
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
		color: #666666;
		font-size: 0.84rem;
		padding: 1rem;
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
		border: 1px solid #dadadf;
		border-radius: 14px;
		background: #fcfcfd;
		box-shadow: 0 22px 52px rgba(0, 0, 0, 0.22);
		z-index: 200;
	}

	.relations-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0.75rem 0.95rem;
		border-bottom: 1px solid #e3e3e8;
	}

	.relations-header h3 {
		margin: 0;
		font-size: 0.95rem;
	}

	.close-map-button {
		border: 1px solid #d6d6dc;
		background: #f9f9fa;
		color: #22242a;
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
		border-bottom: 1px solid #ececf0;
	}

	.tree-nav-button {
		border: 1px solid #d6d6dc;
		background: #f9f9fa;
		border-radius: 8px;
		height: 2rem;
		cursor: pointer;
	}

	.tree-meta {
		font-size: 0.82rem;
		color: #5b5b62;
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
		border: 1px solid #dedee4;
		background: #f8f8f9;
		border-radius: 10px;
		padding: 0.58rem 0.65rem;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
		text-align: left;
		cursor: pointer;
		transition: border-color 120ms ease, box-shadow 120ms ease, background 120ms ease;
	}

	.tree-node:hover {
		border-color: #c9cad2;
		box-shadow: 0 1px 10px rgba(0, 0, 0, 0.08);
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
		background: #2f3138;
		border-color: #2f3138;
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

	@media (max-width: 900px) {
		.room-list {
			border-right: none;
		}

		.room-list-header {
			padding-top: 0.85rem;
		}
	}
</style>
