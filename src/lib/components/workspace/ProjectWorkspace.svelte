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
	import SheetsTool from '$lib/components/workspace/SheetsTool.svelte';
	import CalendarView from '$lib/components/workspace/CalendarView.svelte';
	import WorkloadView from '$lib/components/workspace/WorkloadView.svelte';
	import ActivityFeedPanel from './ActivityFeedPanel.svelte';
	import ChangeRequestPanel from './ChangeRequestPanel.svelte';
	import { changeRequestStore, pendingCount, handleIncomingChangeRequest } from '$lib/stores/changeRequests';
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
	import { initializeFieldSchemasForRoom, fieldSchemaStore } from '$lib/stores/fieldSchema';
	import type { ProjectTimeline } from '$lib/types/timeline';
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
		icon: string;
	};

	type SidebarMode = 'activity' | 'tools';
	type TaskBoardViewMode = 'table' | 'kanban' | 'support';
	type TaskBoardCanvasView = 'table' | 'kanban' | 'support';
	type ProgressSubView = 'gantt' | 'calendar';
	type TeamSubView = 'people' | 'workload';
	type ToolsTab = 'cost' | 'people' | 'sheets';
	type MobileWorkspacePane = 'sidebar' | 'board';

	const WORKSPACE_TABS: WorkspaceTabMeta[] = [
		{
			key: 'overview',
			label: 'Overview',
			icon: 'M3 3h7v7H3zM14 3h7v4h-7zM14 10h7v11h-7zM3 13h7v8H3z'
		},
		{
			key: 'tasks',
			label: 'Board',
			icon: 'M8 7h11M8 12h11M8 17h11M4.5 7h.01M4.5 12h.01M4.5 17h.01'
		},
		{
			key: 'progress',
			label: 'Timeline',
			icon: 'M5 18.5h14M7.5 16V9.5M12 16V6.5M16.5 16v-4.2'
		},
		{
			key: 'tora_ai',
			label: 'Tora AI',
			icon: 'M12 4.2 13.7 8l3.8 1.5-3.8 1.5L12 14.8 10.3 11 6.5 9.5 10.3 8 12 4.2Z M18.5 13.5l.9 2.2 2.1.8-2.1.9-.9 2.1-.8-2.1-2.2-.9 2.2-.8z'
		}
	];

	const COST_ICON =
		'M12 1v22M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6';
	const TEAM_ICON =
		'M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75';
	const SHEETS_ICON =
		'M3 3h18v18H3zM3 9h18M3 15h18M9 3v18M15 3v18';
	const AI_ICON =
		'M12 4.2 13.7 8l3.8 1.5-3.8 1.5L12 14.8 10.3 11 6.5 9.5 10.3 8 12 4.2Z M18.5 13.5l.9 2.2 2.1.8-2.1.9-.9 2.1-.8-2.1-2.2-.9 2.2-.8z';
	const TEMPLATE_ICON =
		'M12 2.9a2 2 0 0 1 2 2V6h1.1a2 2 0 0 1 1.5.7l.8.9 1.5-.3a2 2 0 0 1 2.3 2.3l-.3 1.5.9.8a2 2 0 0 1 .7 1.5V15a2 2 0 0 1-.7 1.5l-.9.8.3 1.5a2 2 0 0 1-2.3 2.3l-1.5-.3-.8.9a2 2 0 0 1-1.5.7H14v1.1a2 2 0 0 1-4 0V22h-1.1a2 2 0 0 1-1.5-.7l-.8-.9-1.5.3a2 2 0 0 1-2.3-2.3l.3-1.5-.9-.8A2 2 0 0 1 2 15v-1.1a2 2 0 0 1 .7-1.5l.9-.8-.3-1.5a2 2 0 0 1 2.3-2.3l1.5.3.8-.9A2 2 0 0 1 8.9 6H10V4.9a2 2 0 0 1 2-2Z M9 12h6M9 16h6M9 8h6';
	const SETTINGS_ICON =
		'M9.8 8.2 8.4 5.9l1.4-1.4 2.3 1.4a5.7 5.7 0 0 1 1.8 0l2.3-1.4 1.4 1.4-1.4 2.3c.2.6.3 1.2.3 1.8s-.1 1.2-.3 1.8l1.4 2.3-1.4 1.4-2.3-1.4a5.7 5.7 0 0 1-1.8 0l-2.3 1.4-1.4-1.4 1.4-2.3a5.7 5.7 0 0 1 0-3.6ZM12 14.2a2.2 2.2 0 1 0 0-4.4 2.2 2.2 0 0 0 0 4.4Z';
	const BELL_ICON =
		'M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9M13.73 21a2 2 0 0 1-3.46 0';

	let lastWorkspaceRoomID = '';
	let lastTemplateSocketSignature = '';
	let workspaceLoadToken = 0;
	let clearingTaskboard = false;
	let templatePickerOpen = false;
	let rightPanelMode: SidebarMode = 'activity';
	let toolsTab: ToolsTab = 'cost';
	let toolsSidebarPinned = false;
	let taskBoardViewMode: TaskBoardViewMode = 'table';
	let taskBoardCanvasView: TaskBoardCanvasView = 'table';
	let progressSubView: ProgressSubView = 'gantt';
	let teamSubView: TeamSubView = 'people';
	let pendingTaskEditID = '';
	let isCompactWorkspaceLayout = false;
	let mobileWorkspacePane: MobileWorkspacePane = 'board';
	let sidebarCollapsed = false;
	let notificationOpen = false;
	let settingsOpen = false;
	let changeRequestPanelOpen = false;

	$: sessionUserID = ($currentUser?.id || '').trim();
	$: sessionUserName = ($currentUser?.username || '').trim();
	$: currentUserIsAdmin = onlineMembers.find((m) => m.id === sessionUserID)?.isAdmin ?? false;
	$: crPendingStore = pendingCount(normalizedWorkspaceRoomID);
	$: crPendingCount = $crPendingStore;
	$: normalizedWorkspaceRoomID = normalizeRoomIDValue(roomId);
	$: mobileSidebarTitle = resolveMobileSidebarTitle(rightPanelMode);
	$: visibleWorkspaceTabs = aiEnabled
		? WORKSPACE_TABS
		: WORKSPACE_TABS.filter((tab) => tab.key !== 'tora_ai');
	$: mainNavTabs = visibleWorkspaceTabs.filter((tab) => tab.key !== 'tora_ai');
	$: aiTab = visibleWorkspaceTabs.find((tab) => tab.key === 'tora_ai') ?? null;
	$: if (!aiEnabled && $activeProjectTab === 'tora_ai') {
		activeProjectTab.set('overview');
	}
$: taskBoardCanvasView = taskBoardViewMode as TaskBoardCanvasView;
	$: {
		if (toolsSidebarPinned) {
			rightPanelMode = 'tools';
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
		templatePickerOpen = false;
		progressSubView = 'gantt';
		teamSubView = 'people';
		toolsTab = 'cost';
		mobileWorkspacePane = 'board';
		notificationOpen = false;
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

	$: { const _gm = $globalMessages as Record<string, unknown> | null; if (_gm?.['type'] === 'change_request' && _gm?.['payload']) { handleIncomingChangeRequest(_gm['payload']); } }

	$: timeline = $projectTimeline;
	$: sprints = timeline?.sprints ?? [];
	$: allTasks = sprints.flatMap((sprint) => sprint.tasks);
	$: totalTasks = allTasks.length;
	$: completedTasks = allTasks.filter((task) => task.status === 'done').length;
	$: inProgressTasks = allTasks.filter((task) => task.status === 'in_progress').length;
	$: completionRate = totalTasks > 0 ? Math.round((completedTasks / totalTasks) * 100) : 0;
	$: roomFieldSchemas = [...$fieldSchemaStore];
	$: timelineDateMap = buildTimelineDateMap($projectTimeline);
	$: calendarWorkloadTasks = [...$taskStore].map((task) => {
		if (task.dueDate || task.startDate) return task;
		const tl = timelineDateMap.get(task.id);
		if (!tl) return task;
		return {
			...task,
			dueDate: tl.endDate > 0 ? tl.endDate : undefined,
			startDate: tl.startDate > 0 ? tl.startDate : undefined
		};
	});

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

	function toggleMobileWorkspacePane() {
		if (!isCompactWorkspaceLayout) {
			return;
		}
		mobileWorkspacePane = mobileWorkspacePane === 'board' ? 'sidebar' : 'board';
	}

	function resolveMobileSidebarTitle(mode: SidebarMode) {
		switch (mode) {
			case 'tools':
				return 'Tools';
			default:
				return 'Project';
		}
	}

	function activateTab(tab: ProjectTab) {
		toolsSidebarPinned = false;
		showMobileBoardPane();
		activeProjectTab.set(tab);
	}

	function setTaskBoardView(nextView: TaskBoardViewMode) {
		taskBoardViewMode = nextView;
		toolsSidebarPinned = false;
		showMobileBoardPane();
		if (get(activeProjectTab) !== 'tasks') {
			activeProjectTab.set('tasks');
		}
	}

	function openToolsTab(nextTab: ToolsTab) {
		toolsTab = nextTab;
		toolsSidebarPinned = true;
		rightPanelMode = 'tools';
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

	function buildTimelineDateMap(timeline: ProjectTimeline | null): Map<string, { startDate: number; endDate: number }> {
		const map = new Map<string, { startDate: number; endDate: number }>();
		if (!timeline) return map;
		for (const sprint of timeline.sprints) {
			for (const task of sprint.tasks) {
				if (!task.id) continue;
				const sd = task.start_date ? new Date(task.start_date).getTime() : 0;
				const ed = task.end_date ? new Date(task.end_date).getTime() : 0;
				if (sd > 0 || ed > 0) map.set(task.id, { startDate: sd, endDate: ed });
			}
		}
		return map;
	}

	async function clearTaskboard() {
		if ($isProjectNew || !$projectTimeline) return;
		const shouldReset = window.confirm(
			'Clear this project and return to setup? This removes all tasks for this room.'
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
				title: 'Cleared project board',
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
			timelineError.set(error instanceof Error ? error.message : 'Failed to clear project board');
		} finally {
			clearingTaskboard = false;
		}
	}
</script>

<section class="workspace-shell" aria-label="Project workspace">
	<div
		class="workspace-frame"
		class:is-compact-layout={isCompactWorkspaceLayout}
		class:show-mobile-sidebar={isCompactWorkspaceLayout && mobileWorkspacePane === 'sidebar'}
		class:show-mobile-board={isCompactWorkspaceLayout && mobileWorkspacePane === 'board'}
		style:--ws-sidebar-w={sidebarCollapsed ? '48px' : '220px'}
	>
		<!-- ── Jira-style left sidebar ─────────────────────────── -->
		<nav
			class="project-sidebar"
			class:is-collapsed={sidebarCollapsed}
			aria-label="Project navigation"
		>
			<!-- Sidebar header: project identity + collapse toggle -->
			<div class="sidebar-header">
				<div class="project-identity">
					<div class="project-avatar" aria-hidden="true">P</div>
					{#if !sidebarCollapsed}
						<span class="project-name">Project</span>
					{/if}
				</div>
				<button
					type="button"
					class="sidebar-collapse-btn"
					on:click={() => (sidebarCollapsed = !sidebarCollapsed)}
					title={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
					aria-label={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						{#if sidebarCollapsed}
							<path d="m9 6 6 6-6 6"></path>
						{:else}
							<path d="M15 6 9 12l6 6"></path>
						{/if}
					</svg>
				</button>
			</div>

			<!-- Scrollable nav body -->
			<div class="sidebar-scroll">
				<!-- Main navigation tabs -->
				{#each mainNavTabs as tab (tab.key)}
					<button
						type="button"
						class="snav-btn"
						class:is-active={$activeProjectTab === tab.key && rightPanelMode === 'activity'}
						on:click={() => activateTab(tab.key)}
						title={tab.label}
						aria-label={tab.label}
						aria-current={$activeProjectTab === tab.key && rightPanelMode === 'activity'
							? 'page'
							: undefined}
					>
						<svg viewBox="0 0 24 24" aria-hidden="true">
							<path d={tab.icon}></path>
						</svg>
						{#if !sidebarCollapsed}<span>{tab.label}</span>{/if}
					</button>
				{/each}

				<!-- Tools section -->
				{#if !sidebarCollapsed}
					<div class="snav-section-label">Tools</div>
				{:else}
					<div class="snav-divider" aria-hidden="true"></div>
				{/if}

				<button
					type="button"
					class="snav-btn"
					class:is-active={rightPanelMode === 'tools' && toolsTab === 'cost'}
					on:click={() => openToolsTab('cost')}
					title="Cost"
					aria-label="Cost"
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d={COST_ICON}></path>
					</svg>
					{#if !sidebarCollapsed}<span>Cost</span>{/if}
				</button>

				<button
					type="button"
					class="snav-btn"
					class:is-active={rightPanelMode === 'tools' && toolsTab === 'people'}
					on:click={() => openToolsTab('people')}
					title="Team"
					aria-label="Team"
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d={TEAM_ICON}></path>
					</svg>
					{#if !sidebarCollapsed}<span>Team</span>{/if}
				</button>

				<button
					type="button"
					class="snav-btn"
					class:is-active={rightPanelMode === 'tools' && toolsTab === 'sheets'}
					on:click={() => openToolsTab('sheets')}
					title="Sheets"
					aria-label="Sheets"
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d={SHEETS_ICON}></path>
					</svg>
					{#if !sidebarCollapsed}<span>Sheets</span>{/if}
				</button>

				{#if aiTab}
					<button
						type="button"
						class="snav-btn snav-ai"
						class:is-active={$activeProjectTab === aiTab.key}
						on:click={() => activateTab(aiTab.key)}
						title={aiTab.label}
						aria-label={aiTab.label}
						aria-current={$activeProjectTab === aiTab.key ? 'page' : undefined}
					>
						<svg viewBox="0 0 24 24" aria-hidden="true">
							<path d={AI_ICON}></path>
						</svg>
						{#if !sidebarCollapsed}<span>{aiTab.label}</span>{/if}
					</button>
				{/if}
			</div>

			<!-- Sidebar footer: settings + clear -->
			<div class="sidebar-footer">
				<button
					type="button"
					class="snav-btn"
					class:is-active={settingsOpen}
					on:click={() => (settingsOpen = !settingsOpen)}
					title="Settings"
					aria-label="Settings"
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d={SETTINGS_ICON}></path>
					</svg>
					{#if !sidebarCollapsed}<span>Settings</span>{/if}
				</button>

				<button
					type="button"
					class="snav-btn snav-danger"
					on:click={() => void clearTaskboard()}
					disabled={clearingTaskboard || $isProjectNew || !$projectTimeline}
					title="Clear Project"
					aria-label="Clear Project"
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d="M5 6h14M9 6V4.8a1.3 1.3 0 0 1 1.3-1.3h3.4A1.3 1.3 0 0 1 15 4.8V6"></path>
						<path d="m8 6 .7 12.2a1.6 1.6 0 0 0 1.6 1.5h3.4a1.6 1.6 0 0 0 1.6-1.5L16 6"></path>
						<path d="M10.2 10.1v6.1M13.8 10.1v6.1"></path>
					</svg>
					{#if !sidebarCollapsed}<span>Clear</span>{/if}
				</button>
			</div>
		</nav>

		<!-- ── Main workspace content ──────────────────────────── -->
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
						<h2>Project</h2>
						<p>{totalTasks} total · {inProgressTasks} in progress · {completionRate}% done</p>
					</div>
				</div>
				<div class="workspace-header-actions">
					<button
						type="button"
						class="header-icon-btn"
						class:is-active={templatePickerOpen}
						on:click={openTemplatePicker}
						title="Change starter template"
						aria-label="Change starter template"
					>
						<svg viewBox="0 0 24 24" aria-hidden="true">
							<path d={TEMPLATE_ICON}></path>
						</svg>
					</button>

					<!-- Notification bell (activity feed) -->
					<button
						type="button"
						class="header-icon-btn notif-btn"
						class:is-active={notificationOpen}
						on:click={() => (notificationOpen = !notificationOpen)}
						title="Activity"
						aria-label="Activity notifications"
						aria-expanded={notificationOpen}
					>
						<svg viewBox="0 0 24 24" aria-hidden="true">
							<path d={BELL_ICON}></path>
						</svg>
					</button>

					<!-- Change request bell: visible only to admins -->
					{#if currentUserIsAdmin}
						<button
							type="button"
							class="header-icon-btn cr-notif-btn"
							class:is-active={changeRequestPanelOpen}
							on:click={() => (changeRequestPanelOpen = !changeRequestPanelOpen)}
							title="Change requests"
							aria-label="Change requests"
							aria-expanded={changeRequestPanelOpen}
						>
							<svg viewBox="0 0 24 24" aria-hidden="true">
								<path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2M9 5a2 2 0 0 0 2 2h2a2 2 0 0 0 2-2M9 5a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2m-6 9l2 2 4-4" />
							</svg>
							{#if crPendingCount > 0}
								<span class="cr-header-badge" aria-label="{crPendingCount} pending requests">{crPendingCount}</span>
							{/if}
						</button>
					{/if}

					<div class="header-sep" aria-hidden="true"></div>
					<button
						type="button"
						class="close-btn"
						on:click={() => dispatch('close')}
						title="Close project"
						aria-label="Close project"
					>
						<svg viewBox="0 0 24 24" aria-hidden="true">
							<path d="M18 6 6 18M6 6l12 12"></path>
						</svg>
					</button>
				</div>
			</header>

			{#if $activeProjectTab === 'tasks' && rightPanelMode !== 'tools'}
				<div class="task-view-bar" role="toolbar" aria-label="Board view">
					<button
						type="button"
						class="tvb-btn"
						class:is-active={taskBoardViewMode === 'table'}
						on:click={() => setTaskBoardView('table')}
						title="Grid table"
					>
						<svg viewBox="0 0 24 24" aria-hidden="true"
							><path d="M3 3h7v7H3zM13 3h8v7h-8zM3 13h7v8H3zM13 13h8v8h-8z"></path></svg
						>
						<span>Table</span>
					</button>
					<button
						type="button"
						class="tvb-btn"
						class:is-active={taskBoardViewMode === 'kanban'}
						on:click={() => setTaskBoardView('kanban')}
						title="Kanban board"
					>
						<svg viewBox="0 0 24 24" aria-hidden="true"
							><path
								d="M9 3H5a2 2 0 0 0-2 2v4m6-6h10a2 2 0 0 1 2 2v4M9 3v18m0 0h10a2 2 0 0 0 2-2V9M9 21H5a2 2 0 0 1-2-2V9m0 0h18"
							></path></svg
						>
						<span>Kanban</span>
					</button>
					<button
						type="button"
						class="tvb-btn"
						class:is-active={taskBoardViewMode === 'support'}
						on:click={() => setTaskBoardView('support')}
						title="Support tickets"
					>
						<svg viewBox="0 0 24 24" aria-hidden="true"
							><path
								d="M15 5v2M15 11v2M15 17v2M5 5h14a2 2 0 0 1 2 2v3a2 2 0 0 0 0 4v3a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-3a2 2 0 0 0 0-4V7a2 2 0 0 1 2-2z"
							></path></svg
						>
						<span>Support</span>
					</button>
				</div>
			{:else if $activeProjectTab === 'progress' && rightPanelMode !== 'tools'}
				<div class="task-view-bar" role="toolbar" aria-label="Timeline view">
					<button
						type="button"
						class="tvb-btn"
						class:is-active={progressSubView === 'gantt'}
						on:click={() => (progressSubView = 'gantt')}
						title="Gantt chart"
					>
						<svg viewBox="0 0 24 24" aria-hidden="true"
							><path d="M5 18.5h14M7.5 16V9.5M12 16V6.5M16.5 16v-4.2"></path></svg
						>
						<span>Gantt</span>
					</button>
					<button
						type="button"
						class="tvb-btn"
						class:is-active={progressSubView === 'calendar'}
						on:click={() => (progressSubView = 'calendar')}
						title="Calendar view"
					>
						<svg viewBox="0 0 24 24" aria-hidden="true"
							><rect x="3" y="4" width="18" height="18" rx="2"></rect><path
								d="M16 2v4M8 2v4M3 10h18"
							></path></svg
						>
						<span>Calendar</span>
					</button>
				</div>
			{/if}

			<main class="workspace-canvas">
				{#if $timelineLoading && !$projectTimeline && !$isProjectNew}
					<div class="canvas-loading">
						<span class="loading-spinner" aria-hidden="true"></span>
						<p>Loading project…</p>
					</div>
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
							<div class="tool-sub-view-wrap">
								<div class="tool-sub-view-bar" role="toolbar" aria-label="Team view">
									<button
										type="button"
										class="tvb-btn"
										class:is-active={teamSubView === 'people'}
										on:click={() => (teamSubView = 'people')}
									>
										<svg viewBox="0 0 24 24" aria-hidden="true"
											><path
												d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75"
											></path></svg
										>
										<span>Team</span>
									</button>
									<button
										type="button"
										class="tvb-btn"
										class:is-active={teamSubView === 'workload'}
										on:click={() => (teamSubView = 'workload')}
									>
										<svg viewBox="0 0 24 24" aria-hidden="true"
											><path d="M3 3h18v18H3zM3 9h18M3 15h18M9 3v18M15 3v18"></path></svg
										>
										<span>Workload</span>
									</button>
								</div>
								<div class="tool-sub-view-body">
									{#if teamSubView === 'people'}
										<PeopleManagement
											{roomId}
											{onlineMembers}
											{canEdit}
											on:requestTaskEdit={(event) => requestTaskEdit(event.detail?.taskId ?? '')}
										/>
									{:else}
										<WorkloadView
											tasks={calendarWorkloadTasks}
											fieldSchemas={roomFieldSchemas}
											{onlineMembers}
										/>
									{/if}
								</div>
							</div>
						{:else}
							<SheetsTool {canEdit} {roomId} isAdmin={currentUserIsAdmin} {sessionUserID} {sessionUserName} />
						{/if}
					</div>
				{:else if $activeProjectTab === 'overview'}
					<TimelineBoard />
				{:else if $activeProjectTab === 'tasks'}
					<TaskBoard
						{roomId}
						{canEdit}
						isAdmin={currentUserIsAdmin}
						{sessionUserID}
						{sessionUserName}
						{onlineMembers}
						boardView={taskBoardCanvasView}
						externalEditTaskId={pendingTaskEditID}
						onExternalEditHandled={handleTaskEditBridgeClear}
					/>
				{:else if $activeProjectTab === 'progress'}
					{#if progressSubView === 'gantt'}
						<ProgressGanttTab {onlineMembers} isAdmin={currentUserIsAdmin} {sessionUserID} {sessionUserName} {roomId} />
					{:else}
						<CalendarView
							tasks={calendarWorkloadTasks}
							fieldSchemas={roomFieldSchemas}
							{onlineMembers}
						/>
					{/if}
				{:else if aiEnabled && $activeProjectTab === 'tora_ai'}
					<ToraAIPanel {roomId} contextKey="taskboard" {onlineMembers} />
				{:else}
					<TimelineBoard />
				{/if}
			</main>
		</div>
	</div>

	<!-- ── Notification (Activity) overlay panel ──────────────── -->
	{#if notificationOpen}
		<div
			class="notif-backdrop"
			role="presentation"
			tabindex="-1"
			on:click={() => (notificationOpen = false)}
			on:keydown={(e) => {
				if (e.key === 'Escape') notificationOpen = false;
			}}
		>
			<div
				class="notif-panel"
				role="dialog"
				aria-modal="true"
				aria-label="Activity feed"
				tabindex="0"
				on:click|stopPropagation
				on:keydown|stopPropagation
			>
				<header class="notif-panel-header">
					<h3>Activity</h3>
					<button
						type="button"
						class="notif-close-btn"
						on:click={() => (notificationOpen = false)}
						aria-label="Close activity panel"
					>
						<svg viewBox="0 0 24 24" aria-hidden="true">
							<path d="M18 6 6 18M6 6l12 12"></path>
						</svg>
					</button>
				</header>
				<div class="notif-panel-body">
					<ActivityFeedPanel />
				</div>
			</div>
		</div>
	{/if}

	<!-- ── Change Request Panel (admin only) ─────────────────── -->
	{#if currentUserIsAdmin}
		<ChangeRequestPanel
			open={changeRequestPanelOpen}
			roomId={normalizedWorkspaceRoomID}
			isAdmin={currentUserIsAdmin}
			sessionUserID={sessionUserID}
			sessionUserName={sessionUserName}
			on:close={() => (changeRequestPanelOpen = false)}
		/>
	{/if}

	<!-- ── Template picker modal ──────────────────────────────── -->
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
		--ws-sidebar-w: 220px;
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

	/* ── Main frame: left sidebar + content ────────────────────── */
	.workspace-frame {
		height: 100%;
		min-height: 0;
		min-width: 0;
		display: grid;
		grid-template-columns: var(--ws-sidebar-w) minmax(0, 1fr);
		background: var(--ws-surface);
		transition: grid-template-columns 0.18s ease;
	}

	/* ── Project sidebar (Jira-style left panel) ────────────────── */
	.project-sidebar {
		min-height: 0;
		display: flex;
		flex-direction: column;
		border-right: 1px solid var(--ws-border);
		background: linear-gradient(180deg, var(--ws-surface) 0%, var(--ws-surface-soft) 100%);
		overflow: hidden;
	}

	/* Sidebar header */
	.sidebar-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.4rem;
		padding: 0.75rem 0.6rem 0.6rem;
		border-bottom: 1px solid color-mix(in srgb, var(--ws-border) 70%, transparent);
		flex-shrink: 0;
	}

	.project-identity {
		display: flex;
		align-items: center;
		gap: 0.55rem;
		min-width: 0;
		overflow: hidden;
	}

	.project-avatar {
		width: 28px;
		height: 28px;
		border-radius: 8px;
		background: var(--ws-accent);
		color: #fff;
		display: grid;
		place-items: center;
		font-size: 0.75rem;
		font-weight: 800;
		flex-shrink: 0;
	}

	.project-name {
		font-size: 0.82rem;
		font-weight: 700;
		color: var(--ws-text);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.sidebar-collapse-btn {
		width: 26px;
		height: 26px;
		border-radius: 7px;
		border: 1px solid transparent;
		background: transparent;
		color: var(--ws-muted);
		display: grid;
		place-items: center;
		cursor: pointer;
		flex-shrink: 0;
		transition:
			background 0.15s ease,
			color 0.15s ease,
			border-color 0.15s ease;
	}

	.sidebar-collapse-btn:hover {
		background: color-mix(in srgb, var(--ws-border) 50%, transparent);
		border-color: var(--ws-border);
		color: var(--ws-text);
	}

	.sidebar-collapse-btn svg {
		width: 0.8rem;
		height: 0.8rem;
		stroke: currentColor;
		fill: none;
		stroke-width: 2.5;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	/* When collapsed: center everything */
	.project-sidebar.is-collapsed .sidebar-header {
		justify-content: center;
		padding: 0.75rem 0.5rem 0.6rem;
	}

	.project-sidebar.is-collapsed .project-identity {
		justify-content: center;
	}

	/* Sidebar scrollable nav */
	.sidebar-scroll {
		flex: 1;
		min-height: 0;
		overflow: hidden auto;
		padding: 0.5rem 0.5rem;
		display: flex;
		flex-direction: column;
		gap: 0.18rem;
	}

	/* Nav buttons */
	.snav-btn {
		width: 100%;
		display: flex;
		align-items: center;
		gap: 0.6rem;
		padding: 0.45rem 0.55rem;
		border: 1px solid transparent;
		border-radius: 8px;
		background: transparent;
		color: var(--ws-muted);
		font-size: 0.79rem;
		font-weight: 500;
		text-align: left;
		cursor: pointer;
		white-space: nowrap;
		overflow: hidden;
		transition:
			background 0.15s ease,
			color 0.15s ease,
			border-color 0.15s ease;
	}

	.project-sidebar.is-collapsed .snav-btn {
		justify-content: center;
		padding: 0.48rem;
	}

	.snav-btn svg {
		width: 0.96rem;
		height: 0.96rem;
		stroke: currentColor;
		fill: none;
		stroke-width: 1.9;
		stroke-linecap: round;
		stroke-linejoin: round;
		flex-shrink: 0;
	}

	.snav-btn span {
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.snav-btn:hover:not(:disabled) {
		color: var(--ws-text);
		background: color-mix(in srgb, var(--ws-border) 45%, transparent);
	}

	.snav-btn.is-active {
		color: var(--ws-accent);
		background: color-mix(in srgb, var(--ws-accent-soft) 75%, transparent);
		border-color: color-mix(in srgb, var(--ws-accent) 28%, transparent);
		font-weight: 600;
	}

	.snav-btn.snav-ai.is-active {
		color: #7c3aed;
		background: rgba(124, 58, 237, 0.1);
		border-color: rgba(124, 58, 237, 0.25);
	}

	.snav-btn.snav-danger {
		color: var(--ws-danger);
	}

	.snav-btn.snav-danger:hover:not(:disabled) {
		background: color-mix(in srgb, var(--ws-danger-soft) 80%, transparent);
		color: var(--ws-danger);
	}

	.snav-btn.snav-danger:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}

	/* Section label + divider */
	.snav-section-label {
		font-size: 0.66rem;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.07em;
		color: var(--ws-muted);
		padding: 0.55rem 0.55rem 0.22rem;
		opacity: 0.75;
	}

	.snav-divider {
		height: 1px;
		background: color-mix(in srgb, var(--ws-border) 70%, transparent);
		margin: 0.35rem 0.55rem;
	}

	/* Sidebar footer */
	.sidebar-footer {
		flex-shrink: 0;
		padding: 0.5rem;
		border-top: 1px solid color-mix(in srgb, var(--ws-border) 70%, transparent);
		display: flex;
		flex-direction: column;
		gap: 0.18rem;
	}

	/* ── Workspace content (header + canvas) ─────────────────── */
	.workspace-content {
		min-height: 0;
		display: grid;
		grid-template-rows: auto auto minmax(0, 1fr);
	}

	.workspace-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.9rem;
		padding: 0.68rem 1rem;
		border-bottom: 1px solid var(--ws-border);
		background: color-mix(in srgb, var(--ws-surface) 80%, var(--ws-surface-soft));
		flex-shrink: 0;
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
		margin: 0.18rem 0 0;
		font-size: 0.76rem;
		color: var(--ws-muted);
	}

	.workspace-header-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.38rem;
	}

	.header-sep {
		width: 1px;
		height: 1.4rem;
		background: var(--ws-border);
		margin: 0 0.1rem;
		flex-shrink: 0;
	}

	/* Header icon buttons */
	.header-icon-btn {
		width: 2rem;
		height: 2rem;
		border-radius: 9px;
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

	.header-icon-btn svg {
		width: 0.88rem;
		height: 0.88rem;
		stroke: currentColor;
		stroke-width: 1.8;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.header-icon-btn:hover {
		color: var(--ws-text);
		border-color: color-mix(in srgb, var(--ws-accent) 42%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 58%, var(--ws-surface));
	}

	.header-icon-btn.is-active {
		color: var(--ws-accent);
		border-color: color-mix(in srgb, var(--ws-accent) 62%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 78%, var(--ws-surface));
	}

	.notif-btn.is-active {
		color: #d97706;
		border-color: rgba(217, 119, 6, 0.4);
		background: rgba(217, 119, 6, 0.1);
	}

	.cr-notif-btn {
		position: relative;
	}

	.cr-notif-btn.is-active {
		color: #6366f1;
		border-color: rgba(99, 102, 241, 0.4);
		background: rgba(99, 102, 241, 0.1);
	}

	.cr-header-badge {
		position: absolute;
		top: -3px;
		right: -3px;
		min-width: 14px;
		height: 14px;
		padding: 0 3px;
		border-radius: 7px;
		background: #ef4444;
		color: #fff;
		font-size: 0.58rem;
		font-weight: 800;
		display: flex;
		align-items: center;
		justify-content: center;
		pointer-events: none;
	}

	.close-btn {
		width: 2rem;
		height: 2rem;
		border-radius: 9px;
		border: 1px solid color-mix(in srgb, var(--ws-danger) 35%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-danger-soft) 45%, transparent);
		color: var(--ws-danger);
		display: grid;
		place-items: center;
		cursor: pointer;
		flex-shrink: 0;
		transition:
			background 0.2s ease,
			border-color 0.2s ease,
			color 0.2s ease;
	}

	.close-btn svg {
		width: 0.88rem;
		height: 0.88rem;
		stroke: currentColor;
		stroke-width: 2;
		fill: none;
		stroke-linecap: round;
	}

	.close-btn:hover {
		background: color-mix(in srgb, var(--ws-danger-soft) 100%, transparent);
		border-color: color-mix(in srgb, var(--ws-danger) 70%, var(--ws-border));
	}

	.workspace-mobile-pane-btn {
		height: 2rem;
		border-radius: 9px;
		border: 1px solid var(--ws-border);
		background: var(--ws-surface);
		color: var(--ws-muted);
		display: inline-flex;
		align-items: center;
		gap: 0.38rem;
		padding: 0 0.65rem 0 0.52rem;
		font-size: 0.75rem;
		font-weight: 600;
		cursor: pointer;
		transition:
			border-color 0.2s ease,
			background 0.2s ease,
			color 0.2s ease;
	}

	.workspace-mobile-pane-btn svg {
		width: 0.88rem;
		height: 0.88rem;
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

	/* ── Task view bar ────────────────────────────────────────── */
	.task-view-bar {
		display: flex;
		align-items: center;
		gap: 0.22rem;
		padding: 0.36rem 0.72rem;
		border-bottom: 1px solid var(--ws-border);
		background: var(--ws-bg);
		flex-shrink: 0;
	}

	.tvb-btn {
		display: inline-flex;
		align-items: center;
		gap: 0.32rem;
		padding: 0.26rem 0.58rem;
		border: 1px solid transparent;
		border-radius: 7px;
		background: transparent;
		color: var(--ws-muted);
		font-size: 0.75rem;
		font-weight: 500;
		cursor: pointer;
		transition:
			background 0.15s ease,
			color 0.15s ease,
			border-color 0.15s ease;
	}

	.tvb-btn svg {
		width: 0.86rem;
		height: 0.86rem;
		stroke: currentColor;
		fill: none;
		stroke-width: 2;
		stroke-linecap: round;
		stroke-linejoin: round;
		flex-shrink: 0;
	}

	.tvb-btn:hover {
		color: var(--ws-text);
		background: color-mix(in srgb, var(--ws-border) 40%, transparent);
	}

	.tvb-btn.is-active {
		color: var(--ws-accent);
		border-color: color-mix(in srgb, var(--ws-accent) 50%, transparent);
		background: color-mix(in srgb, var(--ws-accent-soft) 70%, transparent);
		font-weight: 600;
	}

	/* ── Canvas ──────────────────────────────────────────────── */
	.workspace-canvas {
		min-width: 0;
		min-height: 0;
		overflow: hidden;
		background: var(--ws-bg);
	}

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

	.workspace-tool-canvas {
		height: 100%;
		min-height: 0;
		padding: 0.72rem;
		overflow: hidden;
	}

	.tool-sub-view-wrap {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr);
	}

	.tool-sub-view-bar {
		display: flex;
		align-items: center;
		gap: 0.22rem;
		padding: 0.36rem 0.72rem;
		border-bottom: 1px solid var(--ws-border);
		background: var(--ws-bg);
		flex-shrink: 0;
	}

	.tool-sub-view-body {
		min-height: 0;
		overflow: hidden auto;
		padding: 0.72rem;
	}

	/* ── Notification (Activity) overlay ─────────────────────── */
	.notif-backdrop {
		position: absolute;
		inset: 0;
		z-index: 20;
		background: rgba(10, 12, 18, 0.2);
		backdrop-filter: blur(2px);
	}

	.notif-panel {
		position: absolute;
		top: 0;
		right: 0;
		width: min(340px, 100%);
		height: 100%;
		background: var(--ws-surface);
		border-left: 1px solid var(--ws-border);
		display: flex;
		flex-direction: column;
		box-shadow: -8px 0 32px rgba(0, 0, 0, 0.14);
		animation: slideInRight 0.18s ease;
	}

	@keyframes slideInRight {
		from {
			transform: translateX(100%);
			opacity: 0;
		}
		to {
			transform: translateX(0);
			opacity: 1;
		}
	}

	.notif-panel-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0.72rem 1rem;
		border-bottom: 1px solid var(--ws-border);
		flex-shrink: 0;
	}

	.notif-panel-header h3 {
		margin: 0;
		font-size: 0.88rem;
		font-weight: 700;
	}

	.notif-close-btn {
		width: 1.8rem;
		height: 1.8rem;
		border-radius: 7px;
		border: 1px solid var(--ws-border);
		background: transparent;
		color: var(--ws-muted);
		display: grid;
		place-items: center;
		cursor: pointer;
		transition:
			background 0.15s ease,
			color 0.15s ease;
	}

	.notif-close-btn svg {
		width: 0.8rem;
		height: 0.8rem;
		stroke: currentColor;
		fill: none;
		stroke-width: 2.2;
		stroke-linecap: round;
	}

	.notif-close-btn:hover {
		background: color-mix(in srgb, var(--ws-border) 50%, transparent);
		color: var(--ws-text);
	}

	.notif-panel-body {
		flex: 1;
		min-height: 0;
		overflow: hidden auto;
	}

	/* ── Template picker modal ────────────────────────────────── */
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

	/* ── Compact / mobile layout ─────────────────────────────── */
	@media (max-width: 1100px) {
		.project-sidebar {
			display: none;
		}

		.workspace-frame.show-mobile-sidebar .project-sidebar {
			display: flex;
			position: absolute;
			top: 0;
			left: 0;
			height: 100%;
			z-index: 15;
			width: 220px;
		}

		.workspace-frame {
			grid-template-columns: minmax(0, 1fr);
		}
	}

	@media (max-width: 760px) {
		.workspace-header {
			flex-direction: column;
			align-items: flex-start;
			padding: 0.58rem 0.72rem;
		}

		.workspace-header-primary {
			width: 100%;
		}

		.workspace-header-actions {
			width: 100%;
			justify-content: flex-end;
		}
	}
</style>
