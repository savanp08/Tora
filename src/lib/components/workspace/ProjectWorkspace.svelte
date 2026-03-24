<script lang="ts">
	import { createEventDispatcher, onMount } from 'svelte';
	import { get } from 'svelte/store';
	import ProjectOnboarding from '$lib/components/workspace/ProjectOnboarding.svelte';
	import ProgressGanttTab from '$lib/components/workspace/ProgressGanttTab.svelte';
	import TaskBoard from '$lib/components/workspace/TaskBoard.svelte';
	import ToraAIPanel from '$lib/components/workspace/ToraAIPanel.svelte';
	import TimelineBoard from '$lib/components/workspace/TimelineBoard.svelte';
	import CostManagement from '$lib/components/workspace/CostManagement.svelte';
	import PeopleManagement from '$lib/components/workspace/PeopleManagement.svelte';
	import IntakeForms from '$lib/components/workspace/IntakeForms.svelte';
	import SheetsTool from '$lib/components/workspace/SheetsTool.svelte';
	import TableBoard from '$lib/components/workspace/TableBoard.svelte';
	import ActivityFeedPanel from './ActivityFeedPanel.svelte';
	import { resolveApiBase } from '$lib/config/apiBase';
	import { currentUser } from '$lib/store';
	import type { OnlineMember } from '$lib/types/chat';

	const dispatch = createEventDispatcher<{ close: void }>();
	import {
		activeProjectTab,
		initializeProjectTimelineForRoom,
		isProjectNew,
		projectTimeline,
		setProjectTimeline,
		timelineError,
		timelineLoading,
		type ProjectTab
	} from '$lib/stores/timeline';
	import { addBoardActivity, setBoardActivityRoom } from '$lib/stores/boardActivity';
	import { initializeFieldSchemasForRoom } from '$lib/stores/fieldSchema';
	import { initializeTaskStoreForRoom, taskStore } from '$lib/stores/tasks';
	import { normalizeRoomIDValue } from '$lib/utils/chat/core';
	import { globalMessages, sendSocketPayload } from '$lib/ws';
	import { buildBoardActivitySocketPayload, buildTaskSocketPayload } from '$lib/ws/client';

	export let roomId = '';
	export let canEdit = true;
	export let aiEnabled = true;
	export let onlineMembers: OnlineMember[] = [];

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = resolveApiBase(API_BASE_RAW);
	const COMPACT_WORKSPACE_BREAKPOINT = 1100;

	type WorkspaceTabMeta = {
		key: ProjectTab;
		label: string;
		// SVG path(s) — stroke-based icons, viewBox 0 0 24 24
		icon: string;
	};

	type SidebarMode = 'activity' | 'task_management' | 'ai' | 'tools' | 'forms';
	type TaskBoardViewMode = 'table' | 'tabulator' | 'kanban' | 'support' | 'calendar' | 'workload';
	type TaskBoardCanvasView = 'table' | 'kanban' | 'support' | 'calendar' | 'workload';
	type ToolsTab = 'cost' | 'people' | 'sheets';
	type MobileWorkspacePane = 'sidebar' | 'board';

	// Activity bar icons (left rail — icon-only buttons)
	const WORKSPACE_TABS: WorkspaceTabMeta[] = [
		{
			key: 'overview',
			label: 'Overview',
			// dashboard grid
			icon: 'M3 3h7v7H3zM14 3h7v4h-7zM14 10h7v11h-7zM3 13h7v8H3z'
		},
		{
			key: 'tasks',
			label: 'Tasks',
			// kanban columns
			icon: 'M8 7h11M8 12h11M8 17h11M4.5 7h.01M4.5 12h.01M4.5 17h.01'
		},
		{
			key: 'progress',
			label: 'Progress / Gantt',
			// bar chart ascending
			icon: 'M5 18.5h14M7.5 16V9.5M12 16V6.5M16.5 16v-4.2'
		},
		{
			key: 'tora_ai',
			label: 'Tora AI',
			// sparkle
			icon: 'M12 4.2 13.7 8l3.8 1.5-3.8 1.5L12 14.8 10.3 11 6.5 9.5 10.3 8 12 4.2Z M18.5 13.5l.9 2.2 2.1.8-2.1.9-.9 2.1-.8-2.1-2.2-.9 2.2-.8z'
		}
	];

	const TOOLS_ICON_PATH =
		'M9.8 8.2 8.4 5.9l1.4-1.4 2.3 1.4a5.7 5.7 0 0 1 1.8 0l2.3-1.4 1.4 1.4-1.4 2.3c.2.6.3 1.2.3 1.8s-.1 1.2-.3 1.8l1.4 2.3-1.4 1.4-2.3-1.4a5.7 5.7 0 0 1-1.8 0l-2.3 1.4-1.4-1.4 1.4-2.3a5.7 5.7 0 0 1 0-3.6ZM12 14.2a2.2 2.2 0 1 0 0-4.4 2.2 2.2 0 0 0 0 4.4Z';
	const FORMS_ICON_PATH =
		'M4 6.5h10.2a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-9a2 2 0 0 1 2-2Zm2.2 3.1h6.2M6.2 12.2h6.2M6.2 14.8h4.1m8.1-8.5 3.6 3.4-5.6 5.4-2.3.3.4-2.2 3.9-3.9Z';
	const AI_HEADER_ICON_PATH =
		'M12 4.2 13.7 8l3.8 1.5-3.8 1.5L12 14.8 10.3 11 6.5 9.5 10.3 8 12 4.2Z M18.5 13.5l.9 2.2 2.1.8-2.1.9-.9 2.1-.8-2.1-2.2-.9 2.2-.8z';
	const TEMPLATE_HEADER_ICON_PATH =
		'M12 2.9a2 2 0 0 1 2 2V6h1.1a2 2 0 0 1 1.5.7l.8.9 1.5-.3a2 2 0 0 1 2.3 2.3l-.3 1.5.9.8a2 2 0 0 1 .7 1.5V15a2 2 0 0 1-.7 1.5l-.9.8.3 1.5a2 2 0 0 1-2.3 2.3l-1.5-.3-.8.9a2 2 0 0 1-1.5.7H14v1.1a2 2 0 0 1-4 0V22h-1.1a2 2 0 0 1-1.5-.7l-.8-.9-1.5.3a2 2 0 0 1-2.3-2.3l.3-1.5-.9-.8A2 2 0 0 1 2 15v-1.1a2 2 0 0 1 .7-1.5l.9-.8-.3-1.5a2 2 0 0 1 2.3-2.3l1.5.3.8-.9A2 2 0 0 1 8.9 6H10V4.9a2 2 0 0 1 2-2Z M9 12h6M9 16h6M9 8h6';

	let lastWorkspaceRoomID = '';
	let lastTemplateSocketSignature = '';
	let workspaceLoadToken = 0;
	let clearingTaskboard = false;
	let templatePickerOpen = false;
	let rightPanelMode: SidebarMode = 'activity';
	let sidebarAIVisible = false;
	let toolsTab: ToolsTab = 'cost';
	let toolsSidebarPinned = false;
	let formsSidebarPinned = false;
	let taskBoardViewMode: TaskBoardViewMode = 'table';
	let taskBoardCanvasView: TaskBoardCanvasView = 'table';
	let lastNonAITab: Exclude<ProjectTab, 'tora_ai'> = 'overview';
	let pendingTaskEditID = '';
	let isCompactWorkspaceLayout = false;
	let mobileWorkspacePane: MobileWorkspacePane = 'board';

	$: sessionUserID = ($currentUser?.id || '').trim();
	$: normalizedWorkspaceRoomID = normalizeRoomIDValue(roomId);
	$: mobileSidebarTitle = resolveMobileSidebarTitle(rightPanelMode);
	$: visibleWorkspaceTabs = aiEnabled
		? WORKSPACE_TABS
		: WORKSPACE_TABS.filter((tab) => tab.key !== 'tora_ai');
	$: leadingWorkspaceTabs = visibleWorkspaceTabs.filter((tab) => tab.key !== 'tora_ai');
	$: trailingAITab = visibleWorkspaceTabs.find((tab) => tab.key === 'tora_ai') ?? null;
	$: if (!aiEnabled && $activeProjectTab === 'tora_ai') {
		activeProjectTab.set('overview');
	}
	$: if (!aiEnabled) {
		sidebarAIVisible = false;
	}
	$: if ($activeProjectTab !== 'tora_ai') {
		lastNonAITab = $activeProjectTab as Exclude<ProjectTab, 'tora_ai'>;
	}
	$: taskBoardCanvasView = taskBoardViewMode === 'tabulator' ? 'table' : taskBoardViewMode;
	$: {
		if (formsSidebarPinned) {
			rightPanelMode = 'forms';
		} else if (toolsSidebarPinned) {
			rightPanelMode = 'tools';
		} else if (sidebarAIVisible && aiEnabled && $activeProjectTab !== 'tora_ai') {
			rightPanelMode = 'ai';
		} else if ($activeProjectTab === 'tasks' || $activeProjectTab === 'table') {
			rightPanelMode = 'task_management';
		} else {
			rightPanelMode = 'activity';
		}
	}
	$: if (normalizedWorkspaceRoomID !== lastWorkspaceRoomID) {
		lastWorkspaceRoomID = normalizedWorkspaceRoomID;
		lastTemplateSocketSignature = '';
		activeProjectTab.set('overview');
		taskBoardViewMode = 'table';
		toolsSidebarPinned = false;
		formsSidebarPinned = false;
		sidebarAIVisible = false;
		templatePickerOpen = false;
		toolsTab = 'cost';
		mobileWorkspacePane = 'board';
		setBoardActivityRoom(normalizedWorkspaceRoomID);
		void hydrateWorkspaceForRoom(normalizedWorkspaceRoomID);
	}
	$: latestTemplateSocketEvent = extractTemplateSocketEvent($globalMessages?.payload);
	$: if (
		latestTemplateSocketEvent &&
		latestTemplateSocketEvent.roomId === normalizedWorkspaceRoomID &&
		latestTemplateSocketEvent.signature !== lastTemplateSocketSignature
	) {
		lastTemplateSocketSignature = latestTemplateSocketEvent.signature;
		void refreshWorkspaceAfterTemplateApply(latestTemplateSocketEvent.templateId === 'blank');
	}

	$: timeline = $projectTimeline;
	$: sprints = timeline?.sprints ?? [];
	$: allTasks = sprints.flatMap((sprint) => sprint.tasks);
	$: totalTasks = allTasks.length;
	$: completedTasks = allTasks.filter((task) => task.status === 'done').length;
	$: inProgressTasks = allTasks.filter((task) => task.status === 'in_progress').length;
	$: todoCount = allTasks.filter((task) => task.status === 'todo').length;
	$: completionRate = totalTasks > 0 ? Math.round((completedTasks / totalTasks) * 100) : 0;

	onMount(() => {
		syncCompactWorkspaceLayout();
		window.addEventListener('resize', syncCompactWorkspaceLayout);
		return () => {
			window.removeEventListener('resize', syncCompactWorkspaceLayout);
		};
	});

	function syncCompactWorkspaceLayout() {
		const nextCompactLayout = window.innerWidth <= COMPACT_WORKSPACE_BREAKPOINT;
		isCompactWorkspaceLayout = nextCompactLayout;
		if (!nextCompactLayout) {
			mobileWorkspacePane = 'board';
		}
	}

	function showMobileBoardPane() {
		if (isCompactWorkspaceLayout) {
			mobileWorkspacePane = 'board';
		}
	}

	function showMobileSidebarPane() {
		if (isCompactWorkspaceLayout) {
			mobileWorkspacePane = 'sidebar';
		}
	}

	function toggleMobileWorkspacePane() {
		if (!isCompactWorkspaceLayout) {
			return;
		}
		mobileWorkspacePane = mobileWorkspacePane === 'board' ? 'sidebar' : 'board';
	}

	function resolveMobileSidebarTitle(mode: SidebarMode) {
		switch (mode) {
			case 'forms':
				return 'Forms';
			case 'tools':
				return 'Tools';
			case 'task_management':
				return 'Task Views';
			case 'ai':
				return 'Tora AI';
			default:
				return 'Activity';
		}
	}

	function activateTab(tab: ProjectTab) {
		toolsSidebarPinned = false;
		formsSidebarPinned = false;
		showMobileBoardPane();
		if (tab === 'tora_ai') {
			sidebarAIVisible = false;
			activeProjectTab.set('tora_ai');
			return;
		}
		if (tab === 'table') {
			taskBoardViewMode = 'tabulator';
			activeProjectTab.set('tasks');
			return;
		}
		activeProjectTab.set(tab);
	}

	function setTaskBoardView(nextView: TaskBoardViewMode) {
		taskBoardViewMode = nextView;
		toolsSidebarPinned = false;
		formsSidebarPinned = false;
		sidebarAIVisible = false;
		showMobileBoardPane();
		if (get(activeProjectTab) !== 'tasks') {
			activeProjectTab.set('tasks');
		}
	}

	function toggleToolsSidebar() {
		formsSidebarPinned = false;
		if (toolsSidebarPinned) {
			toolsSidebarPinned = false;
			showMobileBoardPane();
			return;
		}
		toolsSidebarPinned = true;
		sidebarAIVisible = false;
		rightPanelMode = 'tools';
		showMobileBoardPane();
	}

	function openToolsTab(nextTab: ToolsTab) {
		toolsTab = nextTab;
		toolsSidebarPinned = true;
		formsSidebarPinned = false;
		sidebarAIVisible = false;
		rightPanelMode = 'tools';
		showMobileBoardPane();
	}

	function toggleFormsSidebar() {
		toolsSidebarPinned = false;
		sidebarAIVisible = false;
		if (formsSidebarPinned) {
			formsSidebarPinned = false;
			showMobileBoardPane();
			return;
		}
		formsSidebarPinned = true;
		rightPanelMode = 'forms';
		showMobileBoardPane();
	}

	function toggleHeaderAISidebar() {
		if (!aiEnabled) {
			return;
		}
		toolsSidebarPinned = false;
		formsSidebarPinned = false;
		if ($activeProjectTab === 'tora_ai') {
			activeProjectTab.set(lastNonAITab || 'overview');
			sidebarAIVisible = true;
			showMobileSidebarPane();
			return;
		}
		sidebarAIVisible = !sidebarAIVisible;
		if (sidebarAIVisible) {
			showMobileSidebarPane();
			return;
		}
		showMobileBoardPane();
	}

	function createBlankWorkspaceTimeline() {
		const today = new Date();
		const dateText = today.toISOString().slice(0, 10);
		return {
			project_name: 'Blank Workspace',
			total_progress: 0,
			sprints: [
				{
					id: 'sprint-backlog',
					name: 'Backlog',
					start_date: dateText,
					end_date: dateText,
					tasks: []
				}
			]
		};
	}

	function openTemplatePicker() {
		templatePickerOpen = true;
	}

	function closeTemplatePicker() {
		templatePickerOpen = false;
	}

	function requestTaskEdit(taskID: string) {
		const normalizedTaskID = taskID.trim();
		if (!normalizedTaskID) {
			return;
		}
		toolsSidebarPinned = false;
		formsSidebarPinned = false;
		sidebarAIVisible = false;
		taskBoardViewMode = 'table';
		pendingTaskEditID = normalizedTaskID;
		activeProjectTab.set('tasks');
		showMobileBoardPane();
	}

	function handleTaskEditBridgeClear(taskID: string) {
		if (pendingTaskEditID === taskID) {
			pendingTaskEditID = '';
		}
	}

	function toWorkspaceRecord(value: unknown): Record<string, unknown> | null {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return null;
		}
		return value as Record<string, unknown>;
	}

	function extractTemplateSocketEvent(rawPayload: unknown) {
		const source = toWorkspaceRecord(rawPayload);
		if (!source) {
			return null;
		}
		const payload = toWorkspaceRecord(source.payload);
		const type = String(source.type ?? '')
			.trim()
			.toLowerCase();
		if (type !== 'template_applied') {
			return null;
		}
		const roomId = normalizeRoomIDValue(
			String(source.roomId ?? source.room_id ?? payload?.roomId ?? payload?.room_id ?? '')
		);
		if (!roomId) {
			return null;
		}
		const templateId = String(source.template_id ?? payload?.template_id ?? '')
			.trim()
			.toLowerCase();
		const appliedAt = String(source.applied_at ?? payload?.applied_at ?? '').trim();
		return {
			roomId,
			templateId,
			signature: `${roomId}:${templateId}:${appliedAt || 'no-timestamp'}`
		};
	}

	async function refreshWorkspaceAfterTemplateApply(blank: boolean) {
		if (!normalizedWorkspaceRoomID) {
			if (blank) {
				setProjectTimeline(createBlankWorkspaceTimeline());
				activeProjectTab.set('overview');
			}
			return;
		}

		if (blank) {
			await Promise.all([
				initializeTaskStoreForRoom(normalizedWorkspaceRoomID, { apiBase: API_BASE }),
				initializeFieldSchemasForRoom(normalizedWorkspaceRoomID, { apiBase: API_BASE })
			]);
			setProjectTimeline(createBlankWorkspaceTimeline());
			activeProjectTab.set('overview');
			return;
		}

		await Promise.all([
			initializeProjectTimelineForRoom(normalizedWorkspaceRoomID, { apiBase: API_BASE }),
			initializeTaskStoreForRoom(normalizedWorkspaceRoomID, { apiBase: API_BASE }),
			initializeFieldSchemasForRoom(normalizedWorkspaceRoomID, { apiBase: API_BASE })
		]);
		activeProjectTab.set('overview');
	}

	async function handleTemplateApplied(
		event: CustomEvent<{
			templateId: string;
			templateName: string;
			blank: boolean;
			fieldsCreated: number;
			tasksCreated: number;
			automationRulesCreated: number;
		}>
	) {
		const detail = event.detail;
		templatePickerOpen = false;
		await refreshWorkspaceAfterTemplateApply(Boolean(detail?.blank));
	}

	async function parseWorkspaceError(response: Response) {
		const payload = (await response.json().catch(() => null)) as {
			error?: string;
			message?: string;
		} | null;
		return payload?.error?.trim() || payload?.message?.trim() || `HTTP ${response.status}`;
	}

	async function hydrateWorkspaceForRoom(normalizedRoomID: string) {
		workspaceLoadToken += 1;
		const loadToken = workspaceLoadToken;

		if (!normalizedRoomID) {
			setBoardActivityRoom('');
			setProjectTimeline(null);
			isProjectNew.set(true);
			timelineError.set('');
			return;
		}

		await Promise.all([
			initializeProjectTimelineForRoom(normalizedRoomID, { apiBase: API_BASE }),
			initializeTaskStoreForRoom(normalizedRoomID, { apiBase: API_BASE }),
			initializeFieldSchemasForRoom(normalizedRoomID, { apiBase: API_BASE })
		]);

		if (loadToken !== workspaceLoadToken) return;
	}

	async function clearTaskboard() {
		if ($isProjectNew || !$projectTimeline) return;
		const shouldReset = window.confirm(
			'Clear this taskboard and return to setup? This removes all tasks for this room.'
		);
		if (!shouldReset) return;

		if (!normalizedWorkspaceRoomID) {
			setProjectTimeline(null);
			timelineError.set('');
			return;
		}

		clearingTaskboard = true;
		timelineError.set('');
		const existingTasks = get(taskStore).filter(
			(task) => normalizeRoomIDValue(task.roomId) === normalizedWorkspaceRoomID
		);
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedWorkspaceRoomID)}/tasks`,
				{
					method: 'DELETE',
					credentials: 'include',
					headers: sessionUserID ? { 'X-User-Id': sessionUserID } : undefined
				}
			);
			if (!response.ok) throw new Error(await parseWorkspaceError(response));

			for (const task of existingTasks) {
				sendSocketPayload(buildTaskSocketPayload('task_delete', normalizedWorkspaceRoomID, task));
			}

			const clearEvent = addBoardActivity({
				type: 'board_cleared',
				title: 'Cleared task board',
				subtitle: 'Removed all tasks in this room',
				actor: sessionUserID || 'Unknown'
			});
			sendSocketPayload(buildBoardActivitySocketPayload(normalizedWorkspaceRoomID, clearEvent));

			await Promise.all([
				initializeProjectTimelineForRoom(normalizedWorkspaceRoomID, { apiBase: API_BASE }),
				initializeTaskStoreForRoom(normalizedWorkspaceRoomID, { apiBase: API_BASE }),
				initializeFieldSchemasForRoom(normalizedWorkspaceRoomID, { apiBase: API_BASE })
			]);

			activeProjectTab.set('overview');
		} catch (error) {
			timelineError.set(error instanceof Error ? error.message : 'Failed to clear room taskboard');
		} finally {
			clearingTaskboard = false;
		}
	}
</script>

<section class="workspace-shell" aria-label="Project workspace">
	<!-- ── Close button ────────────────────────────────────────────────── -->
	<button
		type="button"
		class="close-btn"
		on:click={() => dispatch('close')}
		title="Close task board"
		aria-label="Close task board"
	>
		<svg viewBox="0 0 24 24" aria-hidden="true">
			<path d="M18 6 6 18M6 6l12 12"></path>
		</svg>
	</button>

	<div
		class="workspace-frame"
		class:is-compact-layout={isCompactWorkspaceLayout}
		class:show-mobile-sidebar={isCompactWorkspaceLayout && mobileWorkspacePane === 'sidebar'}
		class:show-mobile-board={isCompactWorkspaceLayout && mobileWorkspacePane === 'board'}
	>
		<nav class="activity-bar" aria-label="Workspace navigation">
			{#each leadingWorkspaceTabs as tab (tab.key)}
				<button
					type="button"
					class="act-btn"
					class:is-active={$activeProjectTab === tab.key}
					on:click={() => activateTab(tab.key)}
					title={tab.label}
					aria-label={tab.label}
					aria-current={$activeProjectTab === tab.key ? 'page' : undefined}
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d={tab.icon}></path>
					</svg>
				</button>
			{/each}

			<button
				type="button"
				class="act-btn"
				class:is-active={rightPanelMode === 'forms'}
				on:click={toggleFormsSidebar}
				title="Forms"
				aria-label="Forms"
				aria-pressed={rightPanelMode === 'forms'}
			>
				<svg viewBox="0 0 24 24" aria-hidden="true">
					<path d={FORMS_ICON_PATH}></path>
				</svg>
			</button>

			<button
				type="button"
				class="act-btn"
				class:is-active={rightPanelMode === 'tools'}
				on:click={toggleToolsSidebar}
				title="Tools"
				aria-label="Tools"
				aria-pressed={rightPanelMode === 'tools'}
			>
				<svg viewBox="0 0 24 24" aria-hidden="true">
					<path d={TOOLS_ICON_PATH}></path>
				</svg>
			</button>

			{#if trailingAITab}
				<button
					type="button"
					class="act-btn"
					class:is-active={$activeProjectTab === trailingAITab.key}
					on:click={() => activateTab(trailingAITab.key)}
					title={trailingAITab.label}
					aria-label={trailingAITab.label}
					aria-current={$activeProjectTab === trailingAITab.key ? 'page' : undefined}
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d={trailingAITab.icon}></path>
					</svg>
				</button>
			{/if}

			<span class="act-spacer" aria-hidden="true"></span>

			<div class="workspace-actions">
				<button
					type="button"
					class="act-btn act-clear"
					on:click={() => void clearTaskboard()}
					disabled={clearingTaskboard || $isProjectNew || !$projectTimeline}
					title="Clear Taskboard"
					aria-label="Clear Taskboard"
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d="M5 6h14M9 6V4.8a1.3 1.3 0 0 1 1.3-1.3h3.4A1.3 1.3 0 0 1 15 4.8V6"></path>
						<path d="m8 6 .7 12.2a1.6 1.6 0 0 0 1.6 1.5h3.4a1.6 1.6 0 0 0 1.6-1.5L16 6"></path>
						<path d="M10.2 10.1v6.1M13.8 10.1v6.1"></path>
					</svg>
				</button>
			</div>
		</nav>

		<div class="workspace-content">
			<header class="workspace-header">
				<div class="workspace-header-primary">
					{#if isCompactWorkspaceLayout}
						<button
							type="button"
							class="workspace-mobile-pane-btn"
							on:click={toggleMobileWorkspacePane}
							aria-label={mobileWorkspacePane === 'board'
								? `Show ${mobileSidebarTitle} sidebar`
								: 'Back to board'}
						>
							<svg viewBox="0 0 24 24" aria-hidden="true">
								{#if mobileWorkspacePane === 'board'}
									<path d="M15 6 9 12l6 6"></path>
								{:else}
									<path d="m9 6 6 6-6 6"></path>
								{/if}
							</svg>
							<span>{mobileWorkspacePane === 'board' ? mobileSidebarTitle : 'Board'}</span>
						</button>
					{/if}
					<div class="workspace-header-copy">
						<h2>Workspace</h2>
						<p>{totalTasks} total · {inProgressTasks} in progress · {completionRate}% done</p>
					</div>
				</div>
				<div class="workspace-header-actions">
					<button
						type="button"
						class="header-ai-btn"
						class:is-active={templatePickerOpen}
						on:click={openTemplatePicker}
						title="Change starter template"
						aria-label="Change starter template"
					>
						<svg viewBox="0 0 24 24" aria-hidden="true">
							<path d={TEMPLATE_HEADER_ICON_PATH}></path>
						</svg>
					</button>
					{#if aiEnabled}
						<button
							type="button"
							class="header-ai-btn"
							class:is-active={rightPanelMode === 'ai'}
							on:click={toggleHeaderAISidebar}
							title="Open Tora AI in sidebar"
							aria-label="Open Tora AI in sidebar"
						>
							<svg viewBox="0 0 24 24" aria-hidden="true">
								<path d={AI_HEADER_ICON_PATH}></path>
							</svg>
						</button>
					{/if}
				</div>
			</header>

			<div class="workspace-main">
				<aside class="activity-feed-sidebar">
					{#if rightPanelMode === 'forms'}
						<section class="resource-sidebar" aria-label="Forms">
							<header class="task-sidebar-head">
								<h3>Forms</h3>
								<p>Build intake forms and review submissions.</p>
							</header>
							<p class="task-sidebar-note">
								Public form links can create tasks directly in this room.
							</p>
						</section>
					{:else if rightPanelMode === 'tools'}
						<section class="resource-sidebar" aria-label="Workspace tools">
							<nav class="resource-tab-nav tools-tab-nav" aria-label="Tools tabs">
								<button
									type="button"
									class="resource-tab-btn"
									class:is-active={toolsTab === 'cost'}
									on:click={() => openToolsTab('cost')}
									aria-current={toolsTab === 'cost' ? 'page' : undefined}
								>
									Cost
								</button>
								<button
									type="button"
									class="resource-tab-btn"
									class:is-active={toolsTab === 'people'}
									on:click={() => openToolsTab('people')}
									aria-current={toolsTab === 'people' ? 'page' : undefined}
								>
									Team
								</button>
								<button
									type="button"
									class="resource-tab-btn"
									class:is-active={toolsTab === 'sheets'}
									on:click={() => openToolsTab('sheets')}
									aria-current={toolsTab === 'sheets' ? 'page' : undefined}
								>
									Sheets
								</button>
							</nav>
							<div class="resource-tab-panel">
								<p class="task-sidebar-note">Tools render in the main board area.</p>
							</div>
						</section>
					{:else if rightPanelMode === 'task_management'}
						<section class="task-sidebar" aria-label="Task management options">
							<header class="task-sidebar-head">
								<h3>Task Management</h3>
								<p>Select how the task board renders.</p>
							</header>
							<nav class="resource-tab-nav task-view-nav" aria-label="Task view options">
								<button
									type="button"
									class="resource-tab-btn"
									class:is-active={taskBoardViewMode === 'table'}
									on:click={() => setTaskBoardView('table')}
									aria-current={taskBoardViewMode === 'table' ? 'page' : undefined}
								>
									Grid Table
								</button>
								<button
									type="button"
									class="resource-tab-btn"
									class:is-active={taskBoardViewMode === 'tabulator'}
									on:click={() => setTaskBoardView('tabulator')}
									aria-current={taskBoardViewMode === 'tabulator' ? 'page' : undefined}
								>
									Tabulator Table
								</button>
								<button
									type="button"
									class="resource-tab-btn"
									class:is-active={taskBoardViewMode === 'kanban'}
									on:click={() => setTaskBoardView('kanban')}
									aria-current={taskBoardViewMode === 'kanban' ? 'page' : undefined}
								>
									Kanban
								</button>
								<button
									type="button"
									class="resource-tab-btn"
									class:is-active={taskBoardViewMode === 'support'}
									on:click={() => setTaskBoardView('support')}
									aria-current={taskBoardViewMode === 'support' ? 'page' : undefined}
								>
									Support Ticket
								</button>
								<button
									type="button"
									class="resource-tab-btn"
									class:is-active={taskBoardViewMode === 'calendar'}
									on:click={() => setTaskBoardView('calendar')}
									aria-current={taskBoardViewMode === 'calendar' ? 'page' : undefined}
								>
									Calendar
								</button>
								<button
									type="button"
									class="resource-tab-btn"
									class:is-active={taskBoardViewMode === 'workload'}
									on:click={() => setTaskBoardView('workload')}
									aria-current={taskBoardViewMode === 'workload' ? 'page' : undefined}
								>
									Workload
								</button>
							</nav>
							<p class="task-sidebar-note">
								{#if taskBoardViewMode === 'table'}
									Grid table view keeps the current detailed task table layout.
								{:else if taskBoardViewMode === 'tabulator'}
									Tabulator table keeps the spreadsheet-style task data grid.
								{:else if taskBoardViewMode === 'kanban'}
									Kanban view shows swimlanes for To Do, Working on it, and Done.
								{:else if taskBoardViewMode === 'support'}
									Support view is focused on support-ticket intake and tracking.
								{:else if taskBoardViewMode === 'calendar'}
									Calendar view groups tasks by due date and highlights undated work.
								{:else}
									Workload view maps assignees against task date ranges.
								{/if}
							</p>
						</section>
					{:else if rightPanelMode === 'ai' && aiEnabled && $activeProjectTab !== 'tora_ai'}
						<div class="sidebar-panel">
							<ToraAIPanel {roomId} contextKey="taskboard-sidebar" {onlineMembers} />
						</div>
					{:else}
						<div class="sidebar-panel">
							<ActivityFeedPanel />
						</div>
					{/if}
				</aside>

				<main class="workspace-canvas">
					{#if $timelineLoading && !$projectTimeline && !$isProjectNew}
						<div class="canvas-loading">
							<span class="loading-spinner" aria-hidden="true"></span>
							<p>Loading workspace…</p>
						</div>
					{:else if rightPanelMode === 'forms'}
						<IntakeForms
							{roomId}
							{canEdit}
							on:requestTaskEdit={(event) => requestTaskEdit(event.detail?.taskId ?? '')}
						/>
					{:else if $isProjectNew || !$projectTimeline}
						<ProjectOnboarding {roomId} {aiEnabled} on:templateApplied={handleTemplateApplied} />
					{:else if rightPanelMode === 'tools'}
						<div class="workspace-tool-canvas">
							{#if toolsTab === 'cost'}
								<CostManagement
									{canEdit}
									on:requestTaskEdit={(event) => requestTaskEdit(event.detail?.taskId ?? '')}
								/>
							{:else if toolsTab === 'people'}
								<PeopleManagement
									{onlineMembers}
									{canEdit}
									on:requestTaskEdit={(event) => requestTaskEdit(event.detail?.taskId ?? '')}
								/>
							{:else}
								<SheetsTool {canEdit} {roomId} />
							{/if}
						</div>
					{:else if $activeProjectTab === 'overview'}
						<TimelineBoard />
					{:else if $activeProjectTab === 'tasks' || $activeProjectTab === 'table'}
						{#if taskBoardViewMode === 'tabulator'}
							<TableBoard {onlineMembers} />
						{:else}
							<TaskBoard
								{roomId}
								{canEdit}
								{onlineMembers}
								boardView={taskBoardCanvasView}
								externalEditTaskId={pendingTaskEditID}
								onExternalEditHandled={handleTaskEditBridgeClear}
							/>
						{/if}
					{:else if $activeProjectTab === 'progress'}
						<ProgressGanttTab {onlineMembers} />
					{:else if aiEnabled && $activeProjectTab === 'tora_ai'}
						<!-- tora_ai -->
						<ToraAIPanel {roomId} contextKey="taskboard" {onlineMembers} />
					{:else}
						<TimelineBoard />
					{/if}
				</main>
			</div>
		</div>
	</div>

	{#if templatePickerOpen}
		<div
			class="template-modal-backdrop"
			role="presentation"
			tabindex="-1"
			on:click={closeTemplatePicker}
			on:keydown={(event) => {
				if (event.key === 'Escape' || event.key === 'Enter' || event.key === ' ') {
					event.preventDefault();
					closeTemplatePicker();
				}
			}}
		>
			<div
				class="template-modal-card"
				role="dialog"
				aria-modal="true"
				aria-label="Change template"
				tabindex="0"
				on:click|stopPropagation
				on:keydown|stopPropagation
			>
				<ProjectOnboarding
					{roomId}
					{aiEnabled}
					templatePickerOnly={true}
					isModal={true}
					on:close={closeTemplatePicker}
					on:templateApplied={handleTemplateApplied}
				/>
			</div>
		</div>
	{/if}
</section>

<style>
	:global(:root) {
		--ws-bg: #f2f6fc;
		--ws-surface: #ffffff;
		--ws-surface-soft: #f8fbff;
		--ws-border: #d5e0f0;
		--ws-text: #12223f;
		--ws-muted: #5c7196;
		--ws-accent: #2563eb;
		--ws-accent-soft: rgba(37, 99, 235, 0.1);
		--ws-danger: #dc2626;
		--ws-danger-soft: rgba(220, 38, 38, 0.1);
		--ws-feed-w: 250px;
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--ws-bg: #161617;
		--ws-surface: #1b1b1d;
		--ws-surface-soft: #232326;
		--ws-border: #34343a;
		--ws-text: #eff2f7;
		--ws-muted: #a3abb8;
		--ws-accent: #ceced5;
		--ws-accent-soft: rgba(206, 206, 213, 0.16);
		--ws-danger: #f87171;
		--ws-danger-soft: rgba(248, 113, 113, 0.14);
	}

	.workspace-shell {
		height: 100%;
		width: 100%;
		min-height: 0;
		min-width: 0;
		background: var(--ws-bg);
		color: var(--ws-text);
		overflow: hidden;
		position: relative;
	}

	.workspace-frame {
		height: 100%;
		min-height: 0;
		min-width: 0;
		display: grid;
		grid-template-columns: 56px minmax(0, 1fr);
		background: var(--ws-surface);
	}

	.close-btn {
		position: absolute;
		top: 0.75rem;
		right: 0.75rem;
		z-index: 20;
		width: 32px;
		height: 32px;
		border-radius: 10px;
		border: 1px solid color-mix(in srgb, var(--ws-danger) 45%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-danger-soft) 72%, transparent);
		color: var(--ws-danger);
		display: grid;
		place-items: center;
		cursor: pointer;
		transition:
			background 0.2s ease,
			border-color 0.2s ease,
			color 0.2s ease;
	}

	.close-btn svg {
		width: 0.92rem;
		height: 0.92rem;
		stroke: currentColor;
		stroke-width: 2;
		fill: none;
		stroke-linecap: round;
	}

	.close-btn:hover {
		background: color-mix(in srgb, var(--ws-danger-soft) 100%, transparent);
		border-color: color-mix(in srgb, var(--ws-danger) 70%, var(--ws-border));
	}

	.activity-bar {
		min-height: 0;
		display: flex;
		flex-direction: column;
		align-items: stretch;
		gap: 0.42rem;
		padding: 0.62rem 0.36rem;
		border-right: 1px solid var(--ws-border);
		background: linear-gradient(180deg, var(--ws-surface) 0%, var(--ws-surface-soft) 100%);
	}

	.act-btn {
		width: 100%;
		min-height: 38px;
		border-radius: 10px;
		border: 1px solid var(--ws-border);
		background: var(--ws-surface);
		color: var(--ws-muted);
		display: grid;
		place-items: center;
		cursor: pointer;
		transition:
			border-color 0.2s ease,
			background 0.2s ease,
			color 0.2s ease,
			transform 0.15s ease;
	}

	.act-btn:hover:not(:disabled) {
		color: var(--ws-text);
		border-color: color-mix(in srgb, var(--ws-accent) 40%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 42%, var(--ws-surface));
		transform: translateY(-1px);
	}

	.act-btn.is-active {
		color: var(--ws-accent);
		border-color: color-mix(in srgb, var(--ws-accent) 60%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 72%, var(--ws-surface));
		box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--ws-accent) 20%, transparent);
	}

	.act-btn svg {
		width: 0.96rem;
		height: 0.96rem;
		stroke: currentColor;
		stroke-width: 1.9;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
		flex-shrink: 0;
	}

	.act-spacer {
		flex: 1;
	}

	.workspace-actions {
		display: flex;
		flex-direction: column;
		gap: 0.55rem;
	}

	.act-clear {
		border-color: color-mix(in srgb, var(--ws-danger) 40%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-danger-soft) 78%, var(--ws-surface));
		color: var(--ws-danger);
	}

	.act-clear:hover:not(:disabled) {
		background: color-mix(in srgb, var(--ws-danger-soft) 100%, var(--ws-surface));
		border-color: color-mix(in srgb, var(--ws-danger) 66%, var(--ws-border));
		color: var(--ws-danger);
	}

	.act-clear:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.workspace-content {
		min-height: 0;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr);
	}

	.workspace-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.9rem;
		padding: 0.72rem 1rem;
		border-bottom: 1px solid var(--ws-border);
		background: color-mix(in srgb, var(--ws-surface) 80%, var(--ws-surface-soft));
	}

	.workspace-header-primary {
		display: flex;
		align-items: center;
		gap: 0.72rem;
		min-width: 0;
	}

	.workspace-header-copy h2 {
		margin: 0;
		font-size: 0.95rem;
		font-weight: 700;
	}

	.workspace-header-copy p {
		margin: 0.2rem 0 0;
		font-size: 0.78rem;
		color: var(--ws-muted);
	}

	.workspace-header-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.42rem;
	}

	.workspace-mobile-pane-btn {
		height: 2.05rem;
		border-radius: 10px;
		border: 1px solid var(--ws-border);
		background: var(--ws-surface);
		color: var(--ws-muted);
		display: inline-flex;
		align-items: center;
		gap: 0.38rem;
		padding: 0 0.7rem 0 0.58rem;
		font-size: 0.76rem;
		font-weight: 600;
		cursor: pointer;
		transition:
			border-color 0.2s ease,
			background 0.2s ease,
			color 0.2s ease;
	}

	.workspace-mobile-pane-btn svg {
		width: 0.92rem;
		height: 0.92rem;
		stroke: currentColor;
		stroke-width: 1.9;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
		flex-shrink: 0;
	}

	.workspace-mobile-pane-btn:hover {
		color: var(--ws-text);
		border-color: color-mix(in srgb, var(--ws-accent) 42%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 58%, var(--ws-surface));
	}

	.header-ai-btn {
		width: 2.05rem;
		height: 2.05rem;
		border-radius: 10px;
		border: 1px solid var(--ws-border);
		background: var(--ws-surface);
		color: var(--ws-muted);
		display: grid;
		place-items: center;
		cursor: pointer;
		transition:
			border-color 0.2s ease,
			background 0.2s ease,
			color 0.2s ease;
	}

	.header-ai-btn svg {
		width: 0.92rem;
		height: 0.92rem;
		stroke: currentColor;
		stroke-width: 1.8;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.header-ai-btn:hover {
		color: var(--ws-text);
		border-color: color-mix(in srgb, var(--ws-accent) 42%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 58%, var(--ws-surface));
	}

	.header-ai-btn.is-active {
		color: var(--ws-accent);
		border-color: color-mix(in srgb, var(--ws-accent) 62%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 78%, var(--ws-surface));
	}

	.template-modal-backdrop {
		position: absolute;
		inset: 0;
		z-index: 30;
		padding: 1.1rem;
		background: rgba(10, 12, 18, 0.46);
		backdrop-filter: blur(12px);
		display: grid;
		place-items: center;
	}

	.template-modal-card {
		width: min(980px, 100%);
		max-height: min(88vh, 860px);
		overflow: auto;
		border-radius: 24px;
		border: 1px solid color-mix(in srgb, var(--ws-border) 86%, transparent);
		background: var(--ws-surface);
		box-shadow: 0 30px 80px rgba(0, 0, 0, 0.28);
	}

	.workspace-main {
		min-height: 0;
		display: grid;
		grid-template-columns: var(--ws-feed-w) minmax(0, 1fr);
	}

	.activity-feed-sidebar {
		min-height: 0;
		overflow: hidden;
		border-right: 1px solid var(--ws-border);
		background: var(--workspace-taskboard-bg, var(--ws-bg));
	}

	.sidebar-panel {
		height: 100%;
		min-height: 0;
	}

	.resource-sidebar {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr);
	}

	.resource-tab-nav {
		display: flex;
		flex-direction: column;
		gap: 0.42rem;
		padding: 0.68rem 0.72rem 0.5rem;
		border-bottom: 1px solid color-mix(in srgb, var(--ws-border) 80%, transparent);
	}

	.resource-tab-btn {
		border: 1px solid var(--ws-border);
		background: var(--ws-surface);
		color: var(--ws-muted);
		border-radius: 10px;
		padding: 0.42rem 0.58rem;
		font-size: 0.76rem;
		font-weight: 600;
		text-align: left;
		cursor: pointer;
		transition:
			border-color 0.2s ease,
			background 0.2s ease,
			color 0.2s ease;
	}

	.resource-tab-btn:hover {
		color: var(--ws-text);
		border-color: color-mix(in srgb, var(--ws-accent) 40%, var(--ws-border));
	}

	.resource-tab-btn.is-active {
		color: var(--ws-accent);
		border-color: color-mix(in srgb, var(--ws-accent) 60%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 80%, var(--ws-surface));
	}

	.resource-tab-panel {
		min-height: 0;
		overflow: hidden;
		padding: 0.68rem 0.72rem 0.72rem;
	}

	.task-sidebar {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto auto minmax(0, 1fr);
		gap: 0.65rem;
		padding: 0.75rem 0.72rem;
	}

	.task-sidebar-head h3 {
		margin: 0;
		font-size: 0.9rem;
	}

	.task-sidebar-head p {
		margin: 0.24rem 0 0;
		font-size: 0.74rem;
		color: var(--ws-muted);
	}

	.task-sidebar-note {
		margin: 0;
		font-size: 0.74rem;
		line-height: 1.45;
		color: var(--ws-muted);
	}

	.workspace-tool-canvas {
		height: 100%;
		min-height: 0;
		padding: 0.72rem;
		overflow: hidden;
	}

	.workspace-canvas {
		min-width: 0;
		min-height: 0;
		overflow: hidden;
		background: var(--ws-bg);
	}

	/* On small screens let the canvas scroll so every tab is reachable */
	@media (max-width: 640px) {
		.workspace-canvas {
			overflow-y: auto;
		}
	}

	.canvas-loading {
		height: 100%;
		display: grid;
		place-items: center;
		gap: 0.7rem;
		color: var(--ws-muted);
		font-size: 0.96rem;
	}

	.canvas-loading p {
		margin: 0;
	}

	.loading-spinner {
		display: block;
		width: 1.55rem;
		height: 1.55rem;
		border-radius: 50%;
		border: 2px solid color-mix(in srgb, var(--ws-accent) 20%, var(--ws-border));
		border-top-color: var(--ws-accent);
		animation: spin 0.8s linear infinite;
	}

	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}

	@media (max-width: 1100px) {
		.workspace-main {
			grid-template-columns: minmax(0, 1fr);
		}

		.activity-feed-sidebar {
			display: none;
			border-right: none;
		}

		.workspace-frame.show-mobile-sidebar .activity-feed-sidebar {
			display: block;
		}

		.workspace-frame.show-mobile-sidebar .workspace-canvas {
			display: none;
		}
	}

	@media (max-width: 760px) {
		.workspace-frame {
			grid-template-columns: 50px minmax(0, 1fr);
		}

		.activity-bar {
			padding: 0.58rem 0.28rem;
			gap: 0.36rem;
		}

		.act-btn {
			min-height: 34px;
			border-radius: 9px;
		}

		.act-btn svg {
			width: 0.9rem;
			height: 0.9rem;
		}

		.workspace-header {
			flex-direction: column;
			align-items: flex-start;
			padding: 0.62rem 0.72rem;
		}

		.workspace-header-primary {
			width: 100%;
			align-items: flex-start;
		}

		.workspace-header-actions {
			width: 100%;
			justify-content: flex-end;
		}
	}
</style>
