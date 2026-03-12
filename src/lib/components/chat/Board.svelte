<script lang="ts">
	import { browser } from '$app/environment';
	import type { ChatMessage } from '$lib/types/chat';
	import { APP_LIMITS } from '$lib/config/limits';
	import { activeRoomPassword } from '$lib/store';
	import {
		createMessageId,
		normalizeIdentifier,
		normalizeMessageID,
		normalizeRoomIDValue,
		parseOptionalTimestamp,
		toInt,
		toStringValue
	} from '$lib/utils/chat/core';
	import { decryptText, encryptText } from '$lib/utils/crypto';
	import { parseTaskMessagePayload } from '$lib/utils/chat/task';
	import { inferMediaMessageType, uploadToR2, type MediaMessageType } from '$lib/utils/media';
	import { globalMessages, sendSocketPayload } from '$lib/ws';
	import imageCompression from 'browser-image-compression';
	import { createEventDispatcher, onDestroy, onMount } from 'svelte';

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';
	const BOARD_WIDTH = 3840;
	const BOARD_HEIGHT = 2560;
	const MIN_ZOOM = 0.04;
	const MAX_ZOOM = APP_LIMITS.board.maxZoom;
	const DOUBLE_TAP_MS = 340;
	const TAP_MOVE_TOLERANCE = 8;
	const BOARD_UPDATE_STACK_FLUSH_MS = 1000;
	const BOARD_EVENT_BATCH_TYPE = 'board_event_batch';
	const REMOTE_CURSOR_STALE_MS = APP_LIMITS.board.remoteCursorStaleMs;
	const HISTORY_LIMIT = APP_LIMITS.board.historyLimit;
	const BRUSH_WIDTH_PRESETS = [1.5, 3, 5, 8] as const;
	const BOARD_COLOR_PRESETS = [
		'#111827',
		'#2563eb',
		'#0ea5e9',
		'#16a34a',
		'#d97706',
		'#dc2626',
		'#7c3aed',
		'#f8fafc'
	] as const;
	const FABRIC_VITE_ID_URL = '/@id/fabric';
	const FABRIC_CDN_URL = 'https://cdn.jsdelivr.net/npm/fabric@6.5.3/+esm';
	const DEFAULT_RECT_WIDTH = 180;
	const DEFAULT_RECT_HEIGHT = 110;
	const DEFAULT_CIRCLE_DIAMETER = 120;
	const DEFAULT_ELLIPSE_WIDTH = 180;
	const DEFAULT_ELLIPSE_HEIGHT = 116;
	const DEFAULT_TRIANGLE_WIDTH = 190;
	const DEFAULT_TRIANGLE_HEIGHT = 150;
	const DEFAULT_LINE_LENGTH = 190;
	const MIN_SHAPE_WIDTH = 96;
	const MIN_SHAPE_HEIGHT = 72;
	const MIN_SHAPE_POINTER_DELTA = 4;
	const DEFAULT_MESSAGE_CARD_WIDTH = 340;
	const DEFAULT_MEDIA_CARD_WIDTH = 360;
	const MAX_IMAGE_PREVIEW_HEIGHT = APP_LIMITS.board.maxImagePreviewHeight;
	const MAX_VIDEO_PREVIEW_HEIGHT = APP_LIMITS.board.maxVideoPreviewHeight;
	const LOCAL_ACTION_LIMIT = APP_LIMITS.board.localActionLimit;
	const LOCAL_ACTION_STORAGE_PREFIX = 'converse_board_local_actions_v1';
	const DUSTER_STRIPE_WIDTH = BOARD_WIDTH * 0.01;
	const MINIMAP_WIDTH = 200;
	const MINIMAP_HEIGHT = 150;
	const BOARD_STORAGE_LIMIT_BYTES = APP_LIMITS.board.maxStorageBytes;
	const EPHEMERAL_DRAW_BOARD_LIMIT_BYTES = APP_LIMITS.board.ephemeralMaxStorageBytes;
	const DRAW_BOARD_MEMORY_LIMIT_MESSAGE = `Draw Board memory limit (${Math.max(
		1,
		Math.round(EPHEMERAL_DRAW_BOARD_LIMIT_BYTES / (1024 * 1024))
	)}MB) reached.`;
	const DRAW_BOARD_LIMIT_TOAST_COOLDOWN_MS = APP_LIMITS.board.drawLimitToastCooldownMs;
	const RICH_MESSAGE_SCHEMA = 'rich_message_v1';
	const BOARD_STROKE_SCHEMA = 'board_stroke_v1';
	const BOARD_TEXT_BOX_SCHEMA = 'board_text_box_v1';
	const BOARD_SHAPE_STYLE_SCHEMA = 'board_shape_style_v1';
	const UTF8_ENCODER = new TextEncoder();
	const THEME_ADAPTIVE_LIGHT_INK = '#111827';
	const THEME_ADAPTIVE_DARK_INK = '#f8fafc';

	type ToolMode = 'select' | 'draw' | 'eraser' | 'duster';
	type ShapeKind = 'line' | 'arrow' | 'rect' | 'circle' | 'ellipse' | 'triangle';
	type BoardEventType =
		| 'board_draw_start'
		| 'board_cursor_move'
		| 'board_clear'
		| 'board_element_add'
		| 'board_element_move'
		| 'board_element_delete';
	type StackedBoardEventType = 'board_element_add' | 'board_element_move' | 'board_element_delete';

	type PendingBoardUpdate = {
		roomId: string;
		type: StackedBoardEventType;
		payload: Record<string, unknown>;
	};

	type DusterScreenMetrics = {
		left: number;
		top: number;
		width: number;
		height: number;
	};

	type FabricObjectLike = Record<string, unknown> & {
		set?: (args: Record<string, unknown>) => void;
		setCoords?: () => void;
	};

	type BoardElementWire = {
		elementId: string;
		elementType: string;
		x: number;
		y: number;
		width: number;
		height: number;
		content: string;
		zIndex: number;
		createdByUserId: string;
		createdByName: string;
		createdAt: number;
	};

	type BoardMediaContent = {
		url: string;
		name: string;
		kind: MediaMessageType;
		mimeType: string;
		sizeBytes: number;
		caption: string;
		senderName: string;
		sentAt: number;
	};

	type LocalBoardAction = {
		kind: 'add' | 'move' | 'delete';
		elementId: string;
		before?: BoardElementWire;
		after?: BoardElementWire;
	};

	type BoardCursorWire = {
		userId: string;
		name: string;
		x: number;
		y: number;
		updatedAt: number;
		color: string;
	};

	type BoardMessageCardPayload = {
		schema: string;
		messageId: string;
		senderId: string;
		senderName: string;
		content: string;
		type: string;
		mediaUrl: string;
		mediaType: string;
		fileName: string;
		createdAt: number;
		replyToSnippet: string;
	};

	export let roomId = '';
	export let messages: ChatMessage[] = [];
	export let isDarkMode = false;
	export let canEdit = true;
	export let canModerateBoard = false;
	export let currentUserId = '';
	export let currentUsername = '';
	export let isEphemeralRoom = true;

	const dispatch = createEventDispatcher<{
		close: void;
		toastError: { message: string };
	}>();

	let boardContainerEl: HTMLDivElement | null = null;
	let boardToolbarEl: HTMLDivElement | null = null;
	let toolbarPrimaryEl: HTMLDivElement | null = null;
	let toolbarSecondaryEl: HTMLDivElement | null = null;
	let canvasEl: HTMLCanvasElement | null = null;
	let minimapEl: HTMLCanvasElement | null = null;
	let mediaInputEl: HTMLInputElement | null = null;
	let insertWrapEl: HTMLDivElement | null = null;
	let widthMenuWrapEl: HTMLDivElement | null = null;
	let colorMenuWrapEl: HTMLDivElement | null = null;
	let contextMenuEl: HTMLDivElement | null = null;
	let boardDetailsWrapEl: HTMLDivElement | null = null;

	let fabricPackage: Record<string, unknown> | null = null;
	let fabricCanvas: any = null;
	let boardBoundsRect: any = null;
	let boardReady = false;
	let boardLoading = false;
	let boardError = '';
	let initializedRoomId = '';

	let activeTool: ToolMode = 'select';
	let showInsertMenu = false;
	let contextMenuOpen = false;
	let contextMenuX = 0;
	let contextMenuY = 0;
	let contextMenuPoint = { x: BOARD_WIDTH / 2, y: BOARD_HEIGHT / 2 };
	let messagePickerOpen = false;
	let messageSearch = '';
	let isUploadingMedia = false;
	let drawBrushWidth = 2.5;
	let boardInkColor = '#111827';
	let boardInkColorCustomized = false;
	let showWidthMenu = false;
	let showColorMenu = false;
	let pendingShapeKind: ShapeKind | null = null;
	let pendingInsertElementId = '';
	let pendingShapeAnchorPoint: { x: number; y: number } | null = null;
	let pendingShapePointerMoved = false;
	let isInsertOperationActive = false;
	let insertionHintLabel = '';
	let isWidthControlVisible = false;
	let shouldUseToolbarMenu = false;
	let isToolbarExpanded = false;
	let showBoardDetails = false;
	let canModerateBoardActions = false;
	let canManageAllBoardElements = false;
	let canUndoLocalAction = false;
	let canRedoLocalAction = false;
	let boardElementCount = 0;
	let boardApproxBytes = 0;
	let effectiveBoardStorageLimitBytes = BOARD_STORAGE_LIMIT_BYTES;
	let boardStorageUsagePercent = 0;
	let boardRemainingBytes = effectiveBoardStorageLimitBytes;
	let latestSerializedBoardSnapshot = '';
	let latestSerializedBoardSnapshotBytes = 0;
	let lastDrawBoardLimitToastAt = 0;
	let boardZoomLevel = 1;
	let dusterCenterX = BOARD_WIDTH / 2;
	let dusterIsDragging = false;
	let dusterPointerId: number | null = null;
	let viewportRenderTick = 0;
	let dusterScreenMetrics: DusterScreenMetrics = {
		left: -9999,
		top: 0,
		width: 0,
		height: 0
	};
	let pendingTapGesture: {
		startX: number;
		startY: number;
		moved: boolean;
		emptyTarget: boolean;
		boardPoint: { x: number; y: number };
	} | null = null;
	let lastEmptyTapAt = 0;
	let isPanning = false;
	let panLastX = 0;
	let panLastY = 0;
	let pendingBoardUpdates: PendingBoardUpdate[] = [];
	let boardUpdateFlushInterval: ReturnType<typeof setInterval> | null = null;
	let remoteCursors: BoardCursorWire[] = [];
	let remoteCursorByUserId = new Map<string, BoardCursorWire>();
	let zControlVisible = false;
	let zControlLeft = -9999;
	let zControlTop = -9999;
	let minimapRenderInProgress = false;

	let isApplyingRemoteEvent = false;
	let remoteApplyDepth = 0;
	let isApplyingLocalAction = false;
	let isRestoringHistory = false;
	let historyStack: string[] = [];
	let historyCursor = -1;
	let localUndoStack: LocalBoardAction[] = [];
	let localRedoStack: LocalBoardAction[] = [];
	let pendingTransformSnapshotByElementId = new Map<string, BoardElementWire>();

	let removeMessageSubscription: (() => void) | null = null;
	let resizeObserver: ResizeObserver | null = null;
	let toolbarResizeObserver: ResizeObserver | null = null;
	let toolbarOverflowMeasureRAF = 0;
	let removeWindowKeyListeners: (() => void) | null = null;
	let removeWindowPointerListener: (() => void) | null = null;
	let removeWindowResizeListener: (() => void) | null = null;
	let boardPermissionRefreshKey = '';
	let lastAppliedBoardTheme = '';
	let boardThemeRefreshToken = 0;
	let selectionCycleKey = '';
	let selectionCycleCursor = 0;

	$: normalizedRoomId = normalizeRoomIDValue(roomId);
	$: normalizedCurrentUserID = normalizeIdentifier(currentUserId);
	$: normalizedCurrentUsername = (currentUsername || '').trim();
	$: if (!boardInkColorCustomized) {
		boardInkColor = isDarkMode ? '#e5e7eb' : '#111827';
	}
	$: isInsertOperationActive = Boolean(pendingShapeKind || pendingInsertElementId);
	$: insertionHintLabel = pendingShapeKind
		? pendingInsertElementId
			? isLineShapeKind(pendingShapeKind)
				? 'Move mouse to set the endpoint, then click once to place. Hold Shift for angle snap.'
				: 'Move mouse to size the shape, then click once to place. Hold Shift for equal ratio.'
			: `Click board to set the start point for ${describeShapeKind(pendingShapeKind)}`
		: '';
	$: filteredMessages = (messages ?? [])
		.filter((entry) => normalizeMessageID(entry.id) !== '')
		.sort((left, right) => right.createdAt - left.createdAt)
		.filter((entry) => {
			const searchText = buildMessageSearchText(entry).toLowerCase();
			return messageSearch.trim() ? searchText.includes(messageSearch.trim().toLowerCase()) : true;
		})
		.slice(0, 120);

	$: boardThemeKey = isDarkMode ? 'dark' : 'light';
	$: if (boardReady) {
		const didThemeChange = lastAppliedBoardTheme !== '' && lastAppliedBoardTheme !== boardThemeKey;
		updateBoardVisualTheme(isDarkMode);
		if (didThemeChange) {
			void rebuildBoardObjectsForTheme();
		}
		lastAppliedBoardTheme = boardThemeKey;
	}

	$: if (boardReady && normalizedRoomId && normalizedRoomId !== initializedRoomId) {
		flushPendingBoardUpdates();
		void loadBoard(normalizedRoomId);
	}
	$: openToolbarHintText = showInsertMenu
		? 'Insert menu is open. Choose a shape.'
		: showColorMenu
			? 'Color menu is open. Pick an ink color.'
			: showWidthMenu
				? 'Brush width menu is open. Choose a stroke size.'
				: showBoardDetails
					? 'Board details are open.'
					: '';
	$: canModerateBoardActions = canEdit;
	$: canManageAllBoardElements = canEdit && canModerateBoard;
	$: isWidthControlVisible = activeTool === 'draw';
	$: if (!isWidthControlVisible && showWidthMenu) {
		showWidthMenu = false;
	}
	$: if (
		(!canModerateBoardActions && activeTool === 'eraser') ||
		(!canManageAllBoardElements && activeTool === 'duster')
	) {
		applyToolMode('select');
	}
	$: canUndoLocalAction = localUndoStack.length > 0;
	$: canRedoLocalAction = localRedoStack.length > 0;
	$: effectiveBoardStorageLimitBytes = isEphemeralRoom
		? EPHEMERAL_DRAW_BOARD_LIMIT_BYTES
		: BOARD_STORAGE_LIMIT_BYTES;
	$: canCancelCurrentOperation =
		isInsertOperationActive ||
		activeTool !== 'select' ||
		showInsertMenu ||
		showWidthMenu ||
		contextMenuOpen ||
		messagePickerOpen ||
		showBoardDetails;
	$: dusterScreenMetrics = resolveDusterScreenMetrics(viewportRenderTick, dusterCenterX);
	$: boardStorageUsagePercent =
		effectiveBoardStorageLimitBytes > 0
			? Math.min(100, (boardApproxBytes / effectiveBoardStorageLimitBytes) * 100)
			: 0;
	$: boardRemainingBytes = Math.max(0, effectiveBoardStorageLimitBytes - boardApproxBytes);
	$: boardPermissionRefreshKey = `${canEdit ? 1 : 0}:${canManageAllBoardElements ? 1 : 0}:${normalizedCurrentUserID}`;
	$: if (boardReady) {
		void boardPermissionRefreshKey;
		applyBoardObjectPermissions();
	}
	$: if (browser && boardToolbarEl && toolbarPrimaryEl && toolbarSecondaryEl) {
		void isWidthControlVisible;
		scheduleToolbarOverflowCheck();
	}

	onMount(() => {
		if (!browser) {
			return;
		}

		startBoardUpdateFlushLoop();
		registerWindowGuards();
		registerToolbarLayoutGuards();
		void initializeBoard();

		removeMessageSubscription = globalMessages.subscribe((event) => {
			if (!event || !boardReady || !normalizedRoomId) {
				return;
			}
			handleIncomingSocketPayload(event.payload);
		});

		return () => {
			cleanupBoard();
		};
	});

	onDestroy(() => {
		cleanupBoard();
	});

	function cleanupBoard() {
		flushPendingBoardUpdates();
		stopBoardUpdateFlushLoop();
		pendingTapGesture = null;
		cancelPendingOperation(false);
		stopDusterDrag();
		if (removeMessageSubscription) {
			removeMessageSubscription();
			removeMessageSubscription = null;
		}
		if (resizeObserver) {
			resizeObserver.disconnect();
			resizeObserver = null;
		}
		if (toolbarResizeObserver) {
			toolbarResizeObserver.disconnect();
			toolbarResizeObserver = null;
		}
		if (toolbarOverflowMeasureRAF) {
			cancelAnimationFrame(toolbarOverflowMeasureRAF);
			toolbarOverflowMeasureRAF = 0;
		}
		if (removeWindowKeyListeners) {
			removeWindowKeyListeners();
			removeWindowKeyListeners = null;
		}
		if (removeWindowPointerListener) {
			removeWindowPointerListener();
			removeWindowPointerListener = null;
		}
		if (removeWindowResizeListener) {
			removeWindowResizeListener();
			removeWindowResizeListener = null;
		}
		if (fabricCanvas) {
			fabricCanvas.dispose();
			fabricCanvas = null;
		}
		fabricPackage = null;
		boardBoundsRect = null;
		boardReady = false;
		initializedRoomId = '';
		boardElementCount = 0;
		boardApproxBytes = 0;
		boardStorageUsagePercent = 0;
		boardRemainingBytes = effectiveBoardStorageLimitBytes;
		latestSerializedBoardSnapshot = '';
		latestSerializedBoardSnapshotBytes = 0;
		boardZoomLevel = 1;
		remoteCursorByUserId = new Map<string, BoardCursorWire>();
		remoteCursors = [];
		zControlVisible = false;
		zControlLeft = -9999;
		zControlTop = -9999;
		minimapRenderInProgress = false;
		lastAppliedBoardTheme = '';
		boardThemeRefreshToken = 0;
	}

	function registerWindowGuards() {
		const isEditableDOMTarget = (target: EventTarget | null) => {
			if (!(target instanceof HTMLElement)) {
				return false;
			}
			const tagName = target.tagName.toLowerCase();
			return tagName === 'input' || tagName === 'textarea' || target.isContentEditable;
		};

		const isFabricTextEditingActive = () => {
			const activeObject = fabricCanvas?.getActiveObject?.() as Record<string, unknown> | null;
			if (!activeObject) {
				return false;
			}
			if (Boolean(activeObject.isEditing)) {
				return true;
			}
			const hiddenTextarea =
				(activeObject.hiddenTextarea as HTMLTextAreaElement | undefined) ?? null;
			return Boolean(hiddenTextarea && document.activeElement === hiddenTextarea);
		};

		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key === 'Escape' && canCancelCurrentOperation) {
				event.preventDefault();
				cancelCurrentOperation();
				return;
			}
			if ((event.key === 'Delete' || event.key === 'Backspace') && canEdit) {
				if (isEditableDOMTarget(event.target) || isFabricTextEditingActive()) {
					return;
				}
				const activeObject = fabricCanvas?.getActiveObject?.();
				if (activeObject && canMutateBoardObject(activeObject as FabricObjectLike)) {
					event.preventDefault();
					removeBoardObject(activeObject as FabricObjectLike, true);
				}
			}
		};
		const onPointerDown = (event: PointerEvent) => {
			if (dusterIsDragging) {
				return;
			}
			const target = event.target;
			if (target instanceof HTMLElement && target.closest('.mobile-expand-btn')) {
				return;
			}
			if (target instanceof Node) {
				if (insertWrapEl && insertWrapEl.contains(target)) {
					return;
				}
				if (widthMenuWrapEl && widthMenuWrapEl.contains(target)) {
					return;
				}
				if (colorMenuWrapEl && colorMenuWrapEl.contains(target)) {
					return;
				}
				if (contextMenuEl && contextMenuEl.contains(target)) {
					return;
				}
				if (boardDetailsWrapEl && boardDetailsWrapEl.contains(target)) {
					return;
				}
			}
			contextMenuOpen = false;
			showInsertMenu = false;
			showWidthMenu = false;
			showColorMenu = false;
			showBoardDetails = false;
			isToolbarExpanded = false;
		};
		const onPointerMove = (event: PointerEvent) => {
			if (!dusterIsDragging) {
				return;
			}
			if (dusterPointerId !== null && event.pointerId !== dusterPointerId) {
				return;
			}
			event.preventDefault();
			moveDusterToClientX(event.clientX);
			clearElementsTouchingDusterStripe();
		};
		const onPointerUp = (event: PointerEvent) => {
			if (!dusterIsDragging) {
				return;
			}
			if (dusterPointerId !== null && event.pointerId !== dusterPointerId) {
				return;
			}
			event.preventDefault();
			stopDusterDrag();
		};

		window.addEventListener('keydown', onKeyDown);
		window.addEventListener('pointerdown', onPointerDown);
		window.addEventListener('pointermove', onPointerMove, { passive: false });
		window.addEventListener('pointerup', onPointerUp, { passive: false });
		window.addEventListener('pointercancel', onPointerUp, { passive: false });

		removeWindowKeyListeners = () => {
			window.removeEventListener('keydown', onKeyDown);
		};
		removeWindowPointerListener = () => {
			window.removeEventListener('pointerdown', onPointerDown);
			window.removeEventListener('pointermove', onPointerMove);
			window.removeEventListener('pointerup', onPointerUp);
			window.removeEventListener('pointercancel', onPointerUp);
		};
	}

	function registerToolbarLayoutGuards() {
		if (!browser || typeof window === 'undefined') {
			return;
		}
		const onResize = () => {
			scheduleToolbarOverflowCheck();
		};
		window.addEventListener('resize', onResize);
		removeWindowResizeListener = () => {
			window.removeEventListener('resize', onResize);
		};
		if (typeof ResizeObserver !== 'undefined' && boardToolbarEl) {
			toolbarResizeObserver?.disconnect();
			toolbarResizeObserver = new ResizeObserver(() => {
				scheduleToolbarOverflowCheck();
			});
			toolbarResizeObserver.observe(boardToolbarEl);
		}
		scheduleToolbarOverflowCheck();
	}

	function scheduleToolbarOverflowCheck() {
		if (!browser || typeof window === 'undefined') {
			return;
		}
		if (!boardToolbarEl || !toolbarPrimaryEl || !toolbarSecondaryEl) {
			return;
		}
		if (toolbarOverflowMeasureRAF) {
			cancelAnimationFrame(toolbarOverflowMeasureRAF);
		}
		toolbarOverflowMeasureRAF = window.requestAnimationFrame(() => {
			toolbarOverflowMeasureRAF = 0;
			syncToolbarOverflowState();
		});
	}

	function syncToolbarOverflowState() {
		if (!browser || typeof window === 'undefined') {
			return;
		}
		if (!boardToolbarEl || !toolbarPrimaryEl || !toolbarSecondaryEl) {
			return;
		}
		const isCompactScreen = window.matchMedia('(max-width: 768px)').matches;
		if (!isCompactScreen) {
			shouldUseToolbarMenu = false;
			isToolbarExpanded = false;
			return;
		}
		const toolbarWidth = boardToolbarEl.clientWidth;
		const collapseBreakpoint = 560;
		const shouldCollapse = toolbarWidth > 0 && toolbarWidth <= collapseBreakpoint;
		shouldUseToolbarMenu = shouldCollapse;
		if (!shouldCollapse) {
			isToolbarExpanded = false;
		}
	}

	function closeBoardView() {
		cancelPendingOperation(false);
		showInsertMenu = false;
		showWidthMenu = false;
		showColorMenu = false;
		showBoardDetails = false;
		contextMenuOpen = false;
		messagePickerOpen = false;
		isToolbarExpanded = false;
		dispatch('close');
	}

	async function initializeBoard() {
		if (!canvasEl || !boardContainerEl) {
			return;
		}

		boardError = '';
		try {
			fabricPackage = (await import(/* @vite-ignore */ FABRIC_VITE_ID_URL)) as Record<
				string,
				unknown
			>;
		} catch (primaryError) {
			try {
				fabricPackage = (await import(/* @vite-ignore */ FABRIC_CDN_URL)) as Record<
					string,
					unknown
				>;
			} catch (fallbackError) {
				const primaryMessage =
					primaryError instanceof Error ? primaryError.message : String(primaryError);
				const fallbackMessage =
					fallbackError instanceof Error ? fallbackError.message : String(fallbackError);
				boardError = `Failed to load board renderer. Install fabric locally or check network. (${primaryMessage}; ${fallbackMessage})`;
				return;
			}
		}
		if (!fabricPackage) {
			boardError =
				'Failed to load board renderer. Install fabric locally or check network and retry.';
			return;
		}

		const CanvasClass = getFabricClass('Canvas');
		if (!CanvasClass) {
			boardError = 'Board renderer is unavailable';
			return;
		}

		const initialWidth = Math.max(480, boardContainerEl.clientWidth || 1024);
		const initialHeight = Math.max(320, boardContainerEl.clientHeight || 640);

		fabricCanvas = new CanvasClass(canvasEl, {
			width: initialWidth,
			height: initialHeight,
			preserveObjectStacking: true,
			selection: true
		});
		fabricCanvas.perPixelTargetFind = true;
		fabricCanvas.targetFindTolerance = 6;
		fabricCanvas.renderOnAddRemove = true;
		ensureBoardBoundsObject();
		updateBoardVisualTheme(isDarkMode);
		attachFabricListeners();
		syncCanvasViewportSize(false);
		captureHistorySnapshot();

		if (normalizedRoomId) {
			await loadBoard(normalizedRoomId);
		}

		resizeObserver = new ResizeObserver(() => {
			if (!fabricCanvas) {
				return;
			}
			syncCanvasViewportSize(true);
		});
		resizeObserver.observe(boardContainerEl);
		boardReady = true;
	}

	function getFabricClass(className: string): any {
		if (!fabricPackage) {
			return null;
		}
		return (
			(fabricPackage[className] as any) ??
			((fabricPackage.fabric as Record<string, unknown> | undefined)?.[className] as any) ??
			null
		);
	}

	function ensureBoardBoundsObject() {
		if (!fabricCanvas) {
			return;
		}
		const RectClass = getFabricClass('Rect');
		if (!RectClass) {
			return;
		}
		if (boardBoundsRect) {
			fabricCanvas.remove(boardBoundsRect);
			boardBoundsRect = null;
		}
		boardBoundsRect = new RectClass({
			left: 0,
			top: 0,
			width: BOARD_WIDTH,
			height: BOARD_HEIGHT,
			stroke: '#d2d9e5',
			strokeWidth: 2,
			fill: '#ffffff',
			selectable: false,
			evented: false,
			hoverCursor: 'default',
			excludeFromExport: true
		});
		fabricCanvas.add(boardBoundsRect);
		boardBoundsRect.sendToBack?.();
	}

	function updateBoardVisualTheme(darkModeEnabled: boolean) {
		if (!fabricCanvas || !boardBoundsRect) {
			return;
		}
		boardBoundsRect.set?.({
			fill: darkModeEnabled ? '#101316' : '#ffffff',
			stroke: darkModeEnabled ? '#2f3640' : '#cfd8e3'
		});
		fabricCanvas.backgroundColor = darkModeEnabled ? '#080b0f' : '#edf2f8';
		fabricCanvas.requestRenderAll?.();
		applyToolMode(activeTool, false);
	}

	async function rebuildBoardObjectsForTheme() {
		if (!fabricCanvas || boardLoading) {
			return;
		}
		const refreshToken = ++boardThemeRefreshToken;
		const objects = fabricCanvas.getObjects?.() ?? [];
		const elements = objects
			.filter((object: unknown) => object && object !== boardBoundsRect)
			.map((object: unknown) => boardObjectToElement(object as FabricObjectLike))
			.filter((element: BoardElementWire | null): element is BoardElementWire => Boolean(element))
			.sort((left: BoardElementWire, right: BoardElementWire) => left.zIndex - right.zIndex);
		const activeObject = fabricCanvas.getActiveObject?.() as FabricObjectLike | null;
		const activeElementId = normalizeMessageID(
			toStringValue((activeObject as Record<string, unknown> | null)?.elementId)
		);
		beginRemoteApply();
		try {
			fabricCanvas.clear();
			ensureBoardBoundsObject();
			updateBoardVisualTheme(isDarkMode);
			zControlVisible = false;
			for (const element of elements) {
				if (!fabricCanvas || refreshToken !== boardThemeRefreshToken) {
					return;
				}
				await addOrReplaceElementOnCanvas(element);
			}
			if (activeElementId) {
				const restoredActiveObject = findObjectByElementId(activeElementId);
				if (restoredActiveObject) {
					fabricCanvas.setActiveObject?.(restoredActiveObject as any);
				}
			}
			updateSelectionControlsPosition();
			refreshBoardStats();
			fabricCanvas.requestRenderAll?.();
		} finally {
			endRemoteApply();
		}
	}

	function syncCanvasViewportSize(preserveViewport = true) {
		if (!fabricCanvas || !boardContainerEl) {
			return;
		}
		const width = Math.max(360, boardContainerEl.clientWidth || 1024);
		const height = Math.max(300, boardContainerEl.clientHeight || 640);
		fabricCanvas.setDimensions?.({ width, height });
		if (!preserveViewport) {
			const viewport = [1, 0, 0, 1, 0, 0];
			fabricCanvas.setViewportTransform?.(viewport);
			fabricCanvas.setZoom?.(1);
		}
		clampViewportTransform();
		fabricCanvas.requestRenderAll?.();
		markViewportForRender();
	}

	function clampViewportTransform() {
		if (!fabricCanvas || !boardContainerEl) {
			return;
		}
		const viewport = fabricCanvas.viewportTransform ?? [1, 0, 0, 1, 0, 0];
		const zoom = clampZoom(toNumber(viewport[0], 1));
		boardZoomLevel = zoom;
		viewport[0] = zoom;
		viewport[3] = zoom;
		const viewportWidth = Math.max(1, boardContainerEl.clientWidth || 1);
		const viewportHeight = Math.max(1, boardContainerEl.clientHeight || 1);
		const scaledBoardWidth = BOARD_WIDTH * zoom;
		const scaledBoardHeight = BOARD_HEIGHT * zoom;
		const minTranslateX = Math.min(0, viewportWidth - scaledBoardWidth);
		const minTranslateY = Math.min(0, viewportHeight - scaledBoardHeight);
		if (scaledBoardWidth <= viewportWidth) {
			viewport[4] = (viewportWidth - scaledBoardWidth) / 2;
		} else {
			viewport[4] = Math.min(0, Math.max(minTranslateX, toNumber(viewport[4], 0)));
		}
		if (scaledBoardHeight <= viewportHeight) {
			viewport[5] = (viewportHeight - scaledBoardHeight) / 2;
		} else {
			viewport[5] = Math.min(0, Math.max(minTranslateY, toNumber(viewport[5], 0)));
		}
		fabricCanvas.setViewportTransform?.(viewport);
		markViewportForRender();
	}

	function clampZoom(value: number) {
		return Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, value));
	}

	function attachFabricListeners() {
		if (!fabricCanvas) {
			return;
		}

		fabricCanvas.on('mouse:wheel', (event: any) => {
			const nativeEvent = event?.e as WheelEvent | undefined;
			if (!nativeEvent) {
				return;
			}

			// If Ctrl or Cmd is held (or trackpad pinch), ZOOM
			if (nativeEvent.ctrlKey || nativeEvent.metaKey) {
				const delta = nativeEvent.deltaY;
				let zoom = fabricCanvas.getZoom?.() ?? 1;
				const intensity = Math.max(0.5, Math.min(2, Math.abs(delta) / 80));
				const step = delta > 0 ? 0.88 : 1.12; // Smooth zoom step
				zoom *= step ** intensity;
				zoom = clampZoom(zoom);
				const pointer = {
					x: nativeEvent.offsetX,
					y: nativeEvent.offsetY
				};
				fabricCanvas.zoomToPoint?.(pointer, zoom);
			} else {
				// Otherwise, PAN (Scroll)
				const viewport = fabricCanvas.viewportTransform;
				if (viewport) {
					// Subtract delta to move the canvas in the direction of the scroll
					viewport[4] -= nativeEvent.deltaX;
					viewport[5] -= nativeEvent.deltaY;
					fabricCanvas.setViewportTransform?.(viewport);
				}
			}
			clampViewportTransform();
			fabricCanvas.requestRenderAll?.();
			nativeEvent.preventDefault();
			nativeEvent.stopPropagation();
		});

		fabricCanvas.on('mouse:down', (event: any) => {
			const nativeEvent = event?.e as Event | undefined;
			const target = event?.target as FabricObjectLike | null;
			if (!nativeEvent) {
				return;
			}
			const clientPoint = getNativeClientPoint(nativeEvent);
			if (!clientPoint) {
				return;
			}
			if (canEdit && normalizedCurrentUserID) {
				const boardPoint = getBoardPointFromClientPosition(clientPoint.x, clientPoint.y);
				sendBoardEnvelope('board_cursor_move', {
					x: boardPoint.x,
					y: boardPoint.y,
					userId: normalizedCurrentUserID,
					name: normalizedCurrentUsername || 'Guest'
				});
			}
			if (activeTool === 'duster') {
				return;
			}
			if (
				canEdit &&
				target &&
				target !== boardBoundsRect &&
				!isPendingObject(target) &&
				canMutateBoardObject(target)
			) {
				const beforeSnapshot = boardObjectToElement(target);
				if (beforeSnapshot) {
					pendingTransformSnapshotByElementId.set(
						beforeSnapshot.elementId,
						cloneBoardElement(beforeSnapshot)
					);
				}
			}

			if (
				canModerateBoardActions &&
				activeTool === 'eraser' &&
				target &&
				target !== boardBoundsRect
			) {
				if (canMutateBoardObject(target)) {
					removeBoardObject(target, true);
				} else {
					target.set?.({ opacity: 0.5 });
					setTimeout(() => {
						target.set?.({ opacity: 1 });
						fabricCanvas.requestRenderAll();
					}, 150);
				}
				return;
			}

			if (canEdit && activeTool === 'draw') {
				const drawStartPoint = getBoardPointFromClientPosition(clientPoint.x, clientPoint.y);
				sendBoardEnvelope('board_draw_start', {
					x: drawStartPoint.x,
					y: drawStartPoint.y
				});
				return;
			}

			const isEmptyBoardTarget = !target || target === boardBoundsRect;
			if (!isInsertOperationActive && isEmptyBoardTarget) {
				isPanning = true;
				panLastX = clientPoint.x;
				panLastY = clientPoint.y;
				fabricCanvas.selection = false;
			}
		});

		fabricCanvas.on('mouse:move', (event: any) => {
			const nativeEvent = event?.e as Event | undefined;
			if (!nativeEvent) {
				return;
			}
			const clientPoint = getNativeClientPoint(nativeEvent);
			if (!clientPoint) {
				return;
			}
			if (isPanning) {
				const viewport = fabricCanvas.viewportTransform;
				if (!viewport) {
					return;
				}
				viewport[4] += clientPoint.x - panLastX;
				viewport[5] += clientPoint.y - panLastY;
				panLastX = clientPoint.x;
				panLastY = clientPoint.y;
				clampViewportTransform();
				fabricCanvas.requestRenderAll?.();
				return;
			}
		});

		fabricCanvas.on('mouse:up', () => {
			isPanning = false;
			fabricCanvas.selection = true;
		});

		fabricCanvas.on('path:created', (event: any) => {
			if (!canEdit || isApplyingRemoteEvent || isRestoringHistory) {
				return;
			}
			const pathObject = event?.path as FabricObjectLike | null;
			if (!pathObject) {
				return;
			}
			ensureObjectIdentity(pathObject, 'stroke');
			applyObjectPermission(pathObject);
			emitBoardElementAdd(pathObject);
			const addedElement = boardObjectToElement(pathObject);
			if (addedElement) {
				recordLocalAction({
					kind: 'add',
					elementId: addedElement.elementId,
					after: cloneBoardElement(addedElement)
				});
			}
			captureHistorySnapshot();
		});

		fabricCanvas.on('object:modified', (event: any) => {
			if (!canEdit || isApplyingRemoteEvent || isRestoringHistory) {
				return;
			}
			const target = event?.target as FabricObjectLike | null;
			if (!target || target === boardBoundsRect) {
				return;
			}
			if ((target as Record<string, unknown>).type === 'activeSelection') {
				// Ensure the group doesn't contain unauthorized items before broadcasting moves
				enforceSelectionPermissions();
				const objects = (target as any).getObjects?.() || [];
				for (const obj of objects) {
					ensureObjectIdentity(obj as FabricObjectLike);
					emitBoardElementMove(obj as FabricObjectLike);
				}
				captureHistorySnapshot();
			} else {
				if (!canMutateBoardObject(target)) {
					applyObjectPermission(target);
					fabricCanvas.discardActiveObject?.();
					fabricCanvas.requestRenderAll?.();
					return;
				}
				// ... (keep the rest of your single object move/history tracking logic here)
				if (isPendingObject(target)) {
					captureHistorySnapshot();
					return;
				}
				const afterElement = boardObjectToElement(target);
				if (!afterElement) {
					return;
				}
				const beforeElement = pendingTransformSnapshotByElementId.get(afterElement.elementId);
				discardPendingTransformForElement(afterElement.elementId);
				ensureObjectIdentity(target);
				emitBoardElementMove(target);
				if (
					beforeElement &&
					!elementsEquivalent(beforeElement, afterElement) &&
					!isApplyingLocalAction
				) {
					recordLocalAction({
						kind: 'move',
						elementId: afterElement.elementId,
						before: cloneBoardElement(beforeElement),
						after: cloneBoardElement(afterElement)
					});
				}
				captureHistorySnapshot();
			}
		});

		fabricCanvas.on('object:scaling', (event: any) => {
			const target = event?.target as FabricObjectLike | null;
			if (!target || target === boardBoundsRect) {
				return;
			}
			enforceMinimumObjectSize(target);
			updateSelectionControlsPosition();
		});

		fabricCanvas.on('selection:created', (event: any) => {
			enforceSelectionPermissions(event);
			updateSelectionControlsPosition();
		});
		fabricCanvas.on('selection:updated', (event: any) => {
			enforceSelectionPermissions(event);
			updateSelectionControlsPosition();
		});
		fabricCanvas.on('selection:cleared', () => {
			zControlVisible = false;
		});
		fabricCanvas.on('object:moving', () => {
			updateSelectionControlsPosition();
		});
		fabricCanvas.on('object:modified', () => {
			updateSelectionControlsPosition();
		});
		fabricCanvas.on('after:render', () => {
			updateSelectionControlsPosition();
			pruneStaleRemoteCursors();
			updateMinimap();
		});
	}

	function getNativeClientPoint(event: Event): { x: number; y: number } | null {
		const maybeMouseEvent = event as MouseEvent;
		if (
			typeof maybeMouseEvent.clientX === 'number' &&
			typeof maybeMouseEvent.clientY === 'number'
		) {
			return { x: maybeMouseEvent.clientX, y: maybeMouseEvent.clientY };
		}
		const maybeTouchEvent = event as TouchEvent;
		const touch = maybeTouchEvent.touches?.[0] ?? maybeTouchEvent.changedTouches?.[0];
		if (touch) {
			return { x: touch.clientX, y: touch.clientY };
		}
		return null;
	}

	function getBoardPointFromClientPosition(clientX: number, clientY: number) {
		if (!canvasEl || !fabricCanvas) {
			return { x: 0, y: 0 };
		}
		const rect = canvasEl.getBoundingClientRect();
		const zoom = fabricCanvas.getZoom?.() ?? 1;
		const viewport = fabricCanvas.viewportTransform ?? [zoom, 0, 0, zoom, 0, 0];
		return {
			x: (clientX - rect.left - viewport[4]) / zoom,
			y: (clientY - rect.top - viewport[5]) / zoom
		};
	}

	function getViewportMetrics() {
		const viewport = fabricCanvas?.viewportTransform ?? [1, 0, 0, 1, 0, 0];
		const zoom = clampZoom(toNumber(viewport[0], 1));
		const translateX = toNumber(viewport[4], 0);
		const translateY = toNumber(viewport[5], 0);
		return { zoom, translateX, translateY };
	}

	function getCursorScreenPosition(cursor: BoardCursorWire) {
		const { zoom, translateX, translateY } = getViewportMetrics();
		return {
			left: translateX + cursor.x * zoom,
			top: translateY + cursor.y * zoom
		};
	}

	function updateSelectionControlsPosition() {
		if (!fabricCanvas || !boardContainerEl) {
			zControlVisible = false;
			return;
		}
		const activeObject = fabricCanvas.getActiveObject?.() as FabricObjectLike | null;
		if (!activeObject || activeObject === boardBoundsRect || !canMutateBoardObject(activeObject)) {
			zControlVisible = false;
			return;
		}
		const bounds = (activeObject as any).getBoundingRect?.();
		if (!bounds) {
			zControlVisible = false;
			return;
		}
		const containerWidth = Math.max(1, boardContainerEl.clientWidth || 1);
		const containerHeight = Math.max(1, boardContainerEl.clientHeight || 1);
		const controlWidth = Math.min(240, Math.max(160, containerWidth * 0.62));
		const controlHeight = 44;
		const leftNearObject = toNumber(bounds.left, 0) + toNumber(bounds.width, 0) + 8;
		const topAboveObject = toNumber(bounds.top, 0) - controlHeight - 8;
		const topBelowObject = toNumber(bounds.top, 0) + toNumber(bounds.height, 0) + 8;
		const resolvedTop = topAboveObject >= 8 ? topAboveObject : topBelowObject;
		zControlLeft = Math.max(6, Math.min(containerWidth - controlWidth - 6, leftNearObject));
		zControlTop = Math.max(8, Math.min(containerHeight - controlHeight - 6, resolvedTop));
		zControlVisible = true;
	}

	function bringSelectedObjectForward() {
		if (!fabricCanvas) {
			return;
		}
		const activeObject = fabricCanvas.getActiveObject?.() as FabricObjectLike | null;
		if (!activeObject || activeObject === boardBoundsRect || !canMutateBoardObject(activeObject)) {
			return;
		}
		const objects = fabricCanvas.getObjects?.() ?? [];
		const currentIndex = toInt(objects.indexOf(activeObject as any));
		if (currentIndex < 0 || currentIndex >= objects.length - 1) {
			return;
		}
		fabricCanvas.moveTo?.(activeObject as any, currentIndex + 1);
		emitBoardElementMove(activeObject);
		fabricCanvas.requestRenderAll?.();
		captureHistorySnapshot();
		updateSelectionControlsPosition();
	}

	function sendSelectedObjectBackward() {
		if (!fabricCanvas) {
			return;
		}
		const activeObject = fabricCanvas.getActiveObject?.() as FabricObjectLike | null;
		if (!activeObject || activeObject === boardBoundsRect || !canMutateBoardObject(activeObject)) {
			return;
		}
		const objects = fabricCanvas.getObjects?.() ?? [];
		const currentIndex = toInt(objects.indexOf(activeObject as any));
		const minIndex = boardBoundsRect ? 1 : 0;
		if (currentIndex <= minIndex) {
			return;
		}
		fabricCanvas.moveTo?.(activeObject as any, currentIndex - 1);
		emitBoardElementMove(activeObject);
		fabricCanvas.requestRenderAll?.();
		captureHistorySnapshot();
		updateSelectionControlsPosition();
	}

	function cursorColorFromUserID(userId: string) {
		const normalized = normalizeIdentifier(userId);
		if (!normalized) {
			return '#06b6d4';
		}
		let hash = 0;
		for (let index = 0; index < normalized.length; index += 1) {
			hash = (hash * 31 + normalized.charCodeAt(index)) >>> 0;
		}
		const palette = ['#22c55e', '#0ea5e9', '#f97316', '#f59e0b', '#ef4444', '#a855f7'];
		return palette[hash % palette.length];
	}

	function upsertRemoteCursor(payload: { userId: string; name: string; x: number; y: number }) {
		const userId = normalizeIdentifier(payload.userId);
		if (!userId || userId === normalizedCurrentUserID) {
			return;
		}
		remoteCursorByUserId.set(userId, {
			userId,
			name: payload.name.trim() || 'Guest',
			x: payload.x,
			y: payload.y,
			updatedAt: Date.now(),
			color: cursorColorFromUserID(userId)
		});
		remoteCursors = [...remoteCursorByUserId.values()];
	}

	function pruneStaleRemoteCursors() {
		if (remoteCursorByUserId.size === 0) {
			return;
		}
		const expirationThreshold = Date.now() - REMOTE_CURSOR_STALE_MS;
		let changed = false;
		for (const [userId, cursor] of remoteCursorByUserId.entries()) {
			if (cursor.updatedAt >= expirationThreshold) {
				continue;
			}
			remoteCursorByUserId.delete(userId);
			changed = true;
		}
		if (changed) {
			remoteCursors = [...remoteCursorByUserId.values()];
		}
	}

	function updateMinimap() {
		if (!fabricCanvas || !minimapEl || !boardContainerEl || minimapRenderInProgress) {
			return;
		}
		const ctx = minimapEl.getContext('2d');
		if (!ctx) {
			return;
		}
		minimapRenderInProgress = true;
		const scale = Math.min(MINIMAP_WIDTH / BOARD_WIDTH, MINIMAP_HEIGHT / BOARD_HEIGHT);
		const drawWidth = BOARD_WIDTH * scale;
		const drawHeight = BOARD_HEIGHT * scale;
		const offsetX = (MINIMAP_WIDTH - drawWidth) / 2;
		const offsetY = (MINIMAP_HEIGHT - drawHeight) / 2;
		ctx.clearRect(0, 0, MINIMAP_WIDTH, MINIMAP_HEIGHT);
		ctx.fillStyle = isDarkMode ? '#0b1220' : '#f8fafc';
		ctx.fillRect(0, 0, MINIMAP_WIDTH, MINIMAP_HEIGHT);

		const mainCanvasEl = document.querySelector('.upper-canvas') as HTMLCanvasElement | null;
		if (!mainCanvasEl) {
			drawMinimapViewport(ctx, offsetX, offsetY, scale);
			minimapRenderInProgress = false;
			return;
		}

		ctx.drawImage(
			mainCanvasEl,
			0,
			0,
			mainCanvasEl.width,
			mainCanvasEl.height,
			offsetX,
			offsetY,
			drawWidth,
			drawHeight
		);
		drawMinimapViewport(ctx, offsetX, offsetY, scale);
		minimapRenderInProgress = false;
	}

	function drawMinimapViewport(
		ctx: CanvasRenderingContext2D,
		offsetX: number,
		offsetY: number,
		scale: number
	) {
		const { zoom, translateX, translateY } = getViewportMetrics();
		const viewportWidth = Math.max(1, boardContainerEl?.clientWidth ?? 1);
		const viewportHeight = Math.max(1, boardContainerEl?.clientHeight ?? 1);
		const viewLeft = -translateX / zoom;
		const viewTop = -translateY / zoom;
		const viewWidth = viewportWidth / zoom;
		const viewHeight = viewportHeight / zoom;
		ctx.fillStyle = 'rgba(239, 68, 68, 0.22)';
		ctx.strokeStyle = 'rgba(239, 68, 68, 0.92)';
		ctx.lineWidth = 1.2;
		ctx.fillRect(
			offsetX + viewLeft * scale,
			offsetY + viewTop * scale,
			viewWidth * scale,
			viewHeight * scale
		);
		ctx.strokeRect(
			offsetX + viewLeft * scale,
			offsetY + viewTop * scale,
			viewWidth * scale,
			viewHeight * scale
		);
	}

	function enforceMinimumObjectSize(object: FabricObjectLike) {
		const rawWidth = toNumber((object as Record<string, unknown>).width, 0);
		const rawHeight = toNumber((object as Record<string, unknown>).height, 0);
		const currentScaleX = toNumber((object as Record<string, unknown>).scaleX, 1);
		const currentScaleY = toNumber((object as Record<string, unknown>).scaleY, 1);
		const actualWidth = rawWidth * Math.abs(currentScaleX || 1);
		const actualHeight = rawHeight * Math.abs(currentScaleY || 1);
		const nextPatch: Record<string, unknown> = {};

		if (rawWidth > 0 && actualWidth < MIN_SHAPE_WIDTH) {
			const direction = currentScaleX < 0 ? -1 : 1;
			nextPatch.scaleX = direction * (MIN_SHAPE_WIDTH / rawWidth);
		}
		if (rawHeight > 0 && actualHeight < MIN_SHAPE_HEIGHT) {
			const direction = currentScaleY < 0 ? -1 : 1;
			nextPatch.scaleY = direction * (MIN_SHAPE_HEIGHT / rawHeight);
		}
		if (Object.keys(nextPatch).length > 0) {
			object.set?.(nextPatch);
			object.setCoords?.();
		}
	}

	function toggleToolMode(mode: ToolMode) {
		if (mode !== 'select' && activeTool === mode) {
			applyToolMode('select');
			return;
		}
		applyToolMode(mode);
	}

	function applyToolMode(mode: ToolMode, resetSelection = true) {
		if (
			(mode === 'eraser' && !canModerateBoardActions) ||
			(mode === 'duster' && !canManageAllBoardElements)
		) {
			mode = 'select';
		}
		if (mode !== 'select' && isInsertOperationActive) {
			cancelPendingOperation(true);
		}
		if (mode !== 'duster') {
			stopDusterDrag();
		}
		activeTool = mode;
		if (!fabricCanvas) {
			return;
		}
		fabricCanvas.isDrawingMode = mode === 'draw' && canEdit;
		if (mode === 'draw' && canEdit) {
			const PencilBrushClass = getFabricClass('PencilBrush');
			if (PencilBrushClass && !fabricCanvas.freeDrawingBrush) {
				fabricCanvas.freeDrawingBrush = new PencilBrushClass(fabricCanvas);
			}
			if (fabricCanvas.freeDrawingBrush) {
				fabricCanvas.freeDrawingBrush.color = resolveThemeAwareInkColor(boardInkColor);
				fabricCanvas.freeDrawingBrush.width = drawBrushWidth;
			}
		}
		if (resetSelection) {
			showInsertMenu = false;
			showWidthMenu = false;
			showColorMenu = false;
		}
		if (resetSelection && mode !== 'eraser') {
			fabricCanvas.discardActiveObject?.();
			fabricCanvas.requestRenderAll?.();
		}
	}

	function toggleWidthMenu() {
		if (!isWidthControlVisible) {
			return;
		}
		showWidthMenu = !showWidthMenu;
		if (showWidthMenu) {
			showColorMenu = false;
			showInsertMenu = false;
			showBoardDetails = false;
		}
	}

	function normalizeColorHex(value: string) {
		const normalized = normalizeHexColorValue(value);
		if (normalized) {
			return normalized;
		}
		return boardInkColor;
	}

	function normalizeHexColorValue(value: unknown) {
		const trimmed = toStringValue(value).trim().toLowerCase();
		if (/^#[0-9a-f]{6}$/i.test(trimmed)) {
			return trimmed;
		}
		const shortHexMatch = trimmed.match(/^#([0-9a-f]{3})$/i);
		if (shortHexMatch) {
			const shortHex = shortHexMatch[1];
			return `#${shortHex
				.split('')
				.map((part) => `${part}${part}`)
				.join('')}`;
		}
		return '';
	}

	function resolveThemeAwareInkColor(color: string) {
		const normalizedHex = normalizeHexColorValue(color);
		if (!normalizedHex) {
			return toStringValue(color).trim();
		}
		const red = Number.parseInt(normalizedHex.slice(1, 3), 16);
		const green = Number.parseInt(normalizedHex.slice(3, 5), 16);
		const blue = Number.parseInt(normalizedHex.slice(5, 7), 16);
		const channelSpread = Math.max(red, green, blue) - Math.min(red, green, blue);
		const brightness = (red + green + blue) / 3;
		const isNearNeutral = channelSpread <= 16;
		const isNearWhite = brightness >= 215;
		const isNearBlack = brightness <= 40;
		if (!isNearNeutral || (!isNearWhite && !isNearBlack)) {
			return normalizedHex;
		}
		return isDarkMode ? THEME_ADAPTIVE_DARK_INK : THEME_ADAPTIVE_LIGHT_INK;
	}

	function setBoardInkColor(value: string) {
		const nextColor = normalizeColorHex(value);
		boardInkColor = nextColor;
		boardInkColorCustomized = true;
		if (fabricCanvas?.freeDrawingBrush) {
			fabricCanvas.freeDrawingBrush.color = resolveThemeAwareInkColor(nextColor);
		}
	}

	function toggleColorMenu() {
		if (!canEdit) {
			return;
		}
		showColorMenu = !showColorMenu;
		if (showColorMenu) {
			showInsertMenu = false;
			showWidthMenu = false;
			showBoardDetails = false;
		}
	}

	function setDrawWidthPreset(width: number) {
		drawBrushWidth = width;
		showWidthMenu = false;
		if (fabricCanvas?.freeDrawingBrush) {
			fabricCanvas.freeDrawingBrush.width = drawBrushWidth;
		}
		if (activeTool === 'draw') {
			applyToolMode('draw', false);
		}
	}

	function toggleInsertMenu() {
		if (!canEdit) {
			return;
		}
		applyToolMode('select');
		contextMenuOpen = false;
		showColorMenu = false;
		showWidthMenu = false;
		showBoardDetails = false;
		showInsertMenu = !showInsertMenu;
	}

	function toggleBoardDetails() {
		showBoardDetails = !showBoardDetails;
		if (showBoardDetails) {
			showColorMenu = false;
			showInsertMenu = false;
			showWidthMenu = false;
		}
	}

	function getBoardViewportCenter() {
		if (!boardContainerEl || !fabricCanvas) {
			return { x: BOARD_WIDTH / 2, y: BOARD_HEIGHT / 2 };
		}
		const rect = boardContainerEl.getBoundingClientRect();
		return getBoardPointFromClientPosition(rect.left + rect.width / 2, rect.top + rect.height / 2);
	}

	function insertTextBox() {
		if (!fabricCanvas || !canEdit) {
			return;
		}
		const TextboxClass = getFabricClass('Textbox') ?? getFabricClass('Text');
		if (!TextboxClass) {
			return;
		}
		const width = 240;
		const height = 72;
		const point = getBoardViewportCenter();
		const textValue = 'Text';
		const textBox = new TextboxClass(textValue, {
			left: clampBoardX(point.x, width),
			top: clampBoardY(point.y, height),
			width,
			height,
			fontSize: 18,
			lineHeight: 1.28,
			fill: resolveThemeAwareInkColor(boardInkColor),
			backgroundColor: 'transparent',
			padding: 8
		}) as FabricObjectLike;
		ensureObjectIdentity(textBox, 'text_box');
		textBox.set?.({
			content: textValue
		});
		fabricCanvas.add(textBox);
		applyObjectPermission(textBox);
		fabricCanvas.setActiveObject?.(textBox);
		fabricCanvas.requestRenderAll?.();
		emitBoardElementAdd(textBox);
		const addedElement = boardObjectToElement(textBox);
		if (addedElement && !isApplyingLocalAction) {
			recordLocalAction({
				kind: 'add',
				elementId: addedElement.elementId,
				after: cloneBoardElement(addedElement)
			});
		}
		captureHistorySnapshot();
	}

	function insertStickyNote() {
		if (!fabricCanvas || !canEdit) {
			return;
		}
		const TextboxClass = getFabricClass('Textbox') ?? getFabricClass('Text');
		if (!TextboxClass) {
			return;
		}
		const noteSize = 150;
		const point = getBoardViewportCenter();
		const stickyNote = new TextboxClass('Type here...', {
			left: clampBoardX(point.x, noteSize),
			top: clampBoardY(point.y, noteSize),
			width: noteSize,
			height: noteSize,
			fontSize: 16,
			lineHeight: 1.2,
			fill: '#1f2937',
			backgroundColor: '#fef08a',
			padding: 12
		}) as FabricObjectLike;
		ensureObjectIdentity(stickyNote, 'sticky_note');
		stickyNote.set?.({
			content: 'Type here...'
		});
		fabricCanvas.add(stickyNote);
		applyObjectPermission(stickyNote);
		fabricCanvas.setActiveObject?.(stickyNote);
		fabricCanvas.requestRenderAll?.();
		emitBoardElementAdd(stickyNote);
		const addedElement = boardObjectToElement(stickyNote);
		if (addedElement && !isApplyingLocalAction) {
			recordLocalAction({
				kind: 'add',
				elementId: addedElement.elementId,
				after: cloneBoardElement(addedElement)
			});
		}
		captureHistorySnapshot();
	}

	function exportBoardAsPNG() {
		if (!fabricCanvas || !browser) {
			return;
		}
		const dataURL = fabricCanvas.toDataURL?.({
			format: 'png',
			quality: 0.8,
			multiplier: 1
		});
		if (typeof dataURL !== 'string' || dataURL === '') {
			return;
		}
		const anchor = document.createElement('a');
		anchor.href = dataURL;
		anchor.download = 'board_export.png';
		anchor.style.display = 'none';
		document.body.appendChild(anchor);
		anchor.click();
		anchor.remove();
	}

	function describeShapeKind(kind: ShapeKind) {
		if (kind === 'rect') {
			return 'rectangle';
		}
		if (kind === 'circle') {
			return 'circle';
		}
		if (kind === 'ellipse') {
			return 'ellipse';
		}
		if (kind === 'triangle') {
			return 'triangle';
		}
		if (kind === 'arrow') {
			return 'arrow';
		}
		return 'line';
	}

	function isLineShapeKind(kind: ShapeKind | null): kind is 'line' | 'arrow' {
		return kind === 'line' || kind === 'arrow';
	}

	function clampBoardPoint(point: { x: number; y: number }) {
		return {
			x: Math.max(0, Math.min(BOARD_WIDTH, point.x)),
			y: Math.max(0, Math.min(BOARD_HEIGHT, point.y))
		};
	}

	function resolveSnappedLineEndpoint(
		anchor: { x: number; y: number },
		endpoint: { x: number; y: number }
	) {
		const deltaX = endpoint.x - anchor.x;
		const deltaY = endpoint.y - anchor.y;
		const length = Math.hypot(deltaX, deltaY);
		if (length < 0.01) {
			return endpoint;
		}
		const angle = Math.atan2(deltaY, deltaX);
		const snapStep = Math.PI / 4;
		const snappedAngle = Math.round(angle / snapStep) * snapStep;
		return clampBoardPoint({
			x: anchor.x + Math.cos(snappedAngle) * length,
			y: anchor.y + Math.sin(snappedAngle) * length
		});
	}

	function beginShapeInsert(kind: ShapeKind) {
		if (!fabricCanvas || !canEdit) {
			return;
		}
		applyToolMode('select');
		cancelPendingOperation(false);
		pendingShapeKind = kind;
		showInsertMenu = false;
	}

	function placePendingShapeAt(point: { x: number; y: number }) {
		if (!fabricCanvas || !canEdit || !pendingShapeKind) {
			return;
		}
		const anchor = clampBoardPoint(point);
		const shapeObject = createShapeObjectAtPoint(pendingShapeKind, anchor);
		if (!shapeObject) {
			return;
		}
		const identity = ensureObjectIdentity(shapeObject, pendingShapeKind);
		shapeObject.set?.({
			pendingCommit: true
		});
		pendingInsertElementId = identity.elementId;
		pendingShapeAnchorPoint = anchor;
		pendingShapePointerMoved = false;
		fabricCanvas.add(shapeObject);
		applyObjectPermission(shapeObject);
		fabricCanvas.setActiveObject?.(shapeObject);
		updatePendingShapeGeometry(anchor);
		fabricCanvas.requestRenderAll?.();
		captureHistorySnapshot();
	}

	function updatePendingShapeGeometry(point: { x: number; y: number }, lockConstraint = false) {
		if (!fabricCanvas || !pendingInsertElementId || !pendingShapeKind || !pendingShapeAnchorPoint) {
			return;
		}
		const pendingObject = getPendingInsertObject();
		if (!pendingObject) {
			return;
		}
		const anchor = pendingShapeAnchorPoint;
		let endPoint = clampBoardPoint(point);

		if (isLineShapeKind(pendingShapeKind)) {
			if (lockConstraint) {
				endPoint = resolveSnappedLineEndpoint(anchor, endPoint);
			}
			pendingObject.set?.({
				x1: anchor.x,
				y1: anchor.y,
				x2: endPoint.x,
				y2: endPoint.y
			});
			pendingObject.setCoords?.();
			fabricCanvas.requestRenderAll?.();
			return;
		}

		const deltaX = endPoint.x - anchor.x;
		const deltaY = endPoint.y - anchor.y;
		let drawWidth = Math.max(2, Math.abs(deltaX));
		let drawHeight = Math.max(2, Math.abs(deltaY));
		if (lockConstraint || pendingShapeKind === 'circle') {
			const fixedSize = Math.max(drawWidth, drawHeight);
			drawWidth = fixedSize;
			drawHeight = fixedSize;
		}

		const resolvedEndX = anchor.x + (deltaX >= 0 ? drawWidth : -drawWidth);
		const resolvedEndY = anchor.y + (deltaY >= 0 ? drawHeight : -drawHeight);
		const left = clampBoardX(Math.min(anchor.x, resolvedEndX), drawWidth);
		const top = clampBoardY(Math.min(anchor.y, resolvedEndY), drawHeight);

		if (pendingShapeKind === 'rect') {
			pendingObject.set?.({
				left,
				top,
				width: drawWidth,
				height: drawHeight,
				scaleX: 1,
				scaleY: 1
			});
		} else if (pendingShapeKind === 'triangle') {
			pendingObject.set?.({
				left,
				top,
				width: drawWidth,
				height: drawHeight,
				scaleX: 1,
				scaleY: 1
			});
		} else if (pendingShapeKind === 'ellipse') {
			pendingObject.set?.({
				left,
				top,
				rx: Math.max(1, drawWidth / 2),
				ry: Math.max(1, drawHeight / 2),
				scaleX: 1,
				scaleY: 1
			});
		} else if (pendingShapeKind === 'circle') {
			const diameter = Math.max(drawWidth, drawHeight);
			pendingObject.set?.({
				left: clampBoardX(left, diameter),
				top: clampBoardY(top, diameter),
				radius: Math.max(1, diameter / 2),
				scaleX: 1,
				scaleY: 1
			});
		}
		pendingObject.setCoords?.();
		fabricCanvas.requestRenderAll?.();
	}

	function updatePendingShapeFromPointer(event: PointerEvent) {
		if (!pendingInsertElementId || !pendingShapeAnchorPoint) {
			return;
		}
		const boardPoint = getBoardPointFromClientPosition(event.clientX, event.clientY);
		const deltaX = Math.abs(boardPoint.x - pendingShapeAnchorPoint.x);
		const deltaY = Math.abs(boardPoint.y - pendingShapeAnchorPoint.y);
		if (deltaX >= MIN_SHAPE_POINTER_DELTA || deltaY >= MIN_SHAPE_POINTER_DELTA) {
			pendingShapePointerMoved = true;
		}
		updatePendingShapeGeometry(boardPoint, event.shiftKey);
	}

	function ensurePendingShapeHasMinimumFootprint() {
		if (!pendingShapeKind || !pendingShapeAnchorPoint || pendingShapePointerMoved) {
			return;
		}
		const anchor = pendingShapeAnchorPoint;
		if (isLineShapeKind(pendingShapeKind)) {
			updatePendingShapeGeometry({ x: anchor.x + DEFAULT_LINE_LENGTH, y: anchor.y });
			return;
		}
		if (pendingShapeKind === 'rect') {
			updatePendingShapeGeometry({
				x: anchor.x + DEFAULT_RECT_WIDTH,
				y: anchor.y + DEFAULT_RECT_HEIGHT
			});
			return;
		}
		if (pendingShapeKind === 'circle') {
			updatePendingShapeGeometry({
				x: anchor.x + DEFAULT_CIRCLE_DIAMETER,
				y: anchor.y + DEFAULT_CIRCLE_DIAMETER
			});
			return;
		}
		if (pendingShapeKind === 'ellipse') {
			updatePendingShapeGeometry({
				x: anchor.x + DEFAULT_ELLIPSE_WIDTH,
				y: anchor.y + DEFAULT_ELLIPSE_HEIGHT
			});
			return;
		}
		if (pendingShapeKind === 'triangle') {
			updatePendingShapeGeometry({
				x: anchor.x + DEFAULT_TRIANGLE_WIDTH,
				y: anchor.y + DEFAULT_TRIANGLE_HEIGHT
			});
		}
	}

	function commitPendingShapeInsert() {
		if (!fabricCanvas || !pendingInsertElementId) {
			pendingShapeKind = null;
			pendingInsertElementId = '';
			pendingShapeAnchorPoint = null;
			pendingShapePointerMoved = false;
			return;
		}
		const pendingObject = findObjectByElementId(pendingInsertElementId);
		if (!pendingObject) {
			pendingShapeKind = null;
			pendingInsertElementId = '';
			pendingShapeAnchorPoint = null;
			pendingShapePointerMoved = false;
			return;
		}
		ensurePendingShapeHasMinimumFootprint();
		pendingObject.set?.({
			pendingCommit: false
		});
		emitBoardElementAdd(pendingObject);
		const addedElement = boardObjectToElement(pendingObject);
		if (addedElement && !isApplyingLocalAction) {
			recordLocalAction({
				kind: 'add',
				elementId: addedElement.elementId,
				after: cloneBoardElement(addedElement)
			});
		}
		captureHistorySnapshot();
		pendingShapeKind = null;
		pendingInsertElementId = '';
		pendingShapeAnchorPoint = null;
		pendingShapePointerMoved = false;
	}

	function createShapeObjectAtPoint(
		kind: ShapeKind,
		point: { x: number; y: number }
	): FabricObjectLike | null {
		if (!fabricCanvas) {
			return null;
		}
		const sharedStyle = {
			stroke: resolveThemeAwareInkColor(boardInkColor),
			strokeWidth: 2,
			fill: 'transparent'
		};

		if (kind === 'rect') {
			const RectClass = getFabricClass('Rect');
			return RectClass
				? (new RectClass({
						...sharedStyle,
						left: clampBoardX(point.x, 2),
						top: clampBoardY(point.y, 2),
						width: 2,
						height: 2,
						rx: 10,
						ry: 10
					}) as FabricObjectLike)
				: null;
		}
		if (kind === 'circle') {
			const CircleClass = getFabricClass('Circle');
			return CircleClass
				? (new CircleClass({
						...sharedStyle,
						left: clampBoardX(point.x, 2),
						top: clampBoardY(point.y, 2),
						radius: 1
					}) as FabricObjectLike)
				: null;
		}
		if (kind === 'ellipse') {
			const EllipseClass = getFabricClass('Ellipse');
			return EllipseClass
				? (new EllipseClass({
						...sharedStyle,
						left: clampBoardX(point.x, 2),
						top: clampBoardY(point.y, 2),
						rx: 1,
						ry: 1
					}) as FabricObjectLike)
				: null;
		}
		if (kind === 'triangle') {
			const TriangleClass = getFabricClass('Triangle');
			return TriangleClass
				? (new TriangleClass({
						...sharedStyle,
						left: clampBoardX(point.x, 2),
						top: clampBoardY(point.y, 2),
						width: 2,
						height: 2
					}) as FabricObjectLike)
				: null;
		}
		const LineClass = getFabricClass('Line');
		if (!LineClass) {
			return null;
		}
		const anchor = clampBoardPoint(point);
		return new LineClass([anchor.x, anchor.y, anchor.x + 1, anchor.y + 1], {
			stroke: resolveThemeAwareInkColor(boardInkColor),
			strokeWidth: kind === 'arrow' ? 4 : 3
		}) as FabricObjectLike;
	}

	function clampBoardX(x: number, objectWidth = 0) {
		return Math.max(0, Math.min(BOARD_WIDTH - Math.max(0, objectWidth), x));
	}

	function clampBoardY(y: number, objectHeight = 0) {
		return Math.max(0, Math.min(BOARD_HEIGHT - Math.max(0, objectHeight), y));
	}

	function getObjectOwnerUserID(object: FabricObjectLike | null) {
		if (!object) {
			return '';
		}
		const record = object as Record<string, unknown>;
		return normalizeIdentifier(
			toStringValue(
				record.createdByUserId ?? record.created_by_user_id ?? record.senderId ?? record.sender_id
			)
		);
	}

	function canMutateOwner(ownerUserID: string) {
		if (!canEdit) return false;
		if (canManageAllBoardElements) return true; // Admins can delete anything
		const normalizedOwner = normalizeIdentifier(ownerUserID);
		const currentUser = normalizeIdentifier(currentUserId);
		return normalizedOwner !== '' && currentUser !== '' && normalizedOwner === currentUser;
	}

	function canMutateBoardObject(object: FabricObjectLike | null) {
		if (!object || object === boardBoundsRect) {
			return false;
		}
		if (isPendingObject(object)) {
			return canEdit;
		}
		return canMutateOwner(getObjectOwnerUserID(object));
	}

	function applyObjectPermission(object: FabricObjectLike | null) {
		if (!object || object === boardBoundsRect) {
			return;
		}
		const elementType = toStringValue((object as Record<string, unknown>).elementType)
			.trim()
			.toLowerCase();
		const usePreciseHitTest =
			elementType === 'stroke' ||
			elementType === 'line' ||
			elementType === 'arrow' ||
			elementType === 'text_box';
		const canMutate = canMutateBoardObject(object);
		object.set?.({
			selectable: canMutate,
			evented: canMutate,
			hasControls: canMutate,
			lockMovementX: !canMutate,
			lockMovementY: !canMutate,
			lockScalingX: !canMutate,
			lockScalingY: !canMutate,
			lockRotation: !canMutate,
			perPixelTargetFind: usePreciseHitTest,
			padding: usePreciseHitTest ? 4 : 1
		});
		object.setCoords?.();
	}

	function applyBoardObjectPermissions() {
		if (!fabricCanvas) {
			return;
		}
		const objects = fabricCanvas.getObjects?.() ?? [];
		for (const object of objects) {
			if (object === boardBoundsRect) {
				continue;
			}
			applyObjectPermission(object as FabricObjectLike);
		}
		const activeObject = fabricCanvas.getActiveObject?.();
		if (activeObject && !canMutateBoardObject(activeObject as FabricObjectLike)) {
			fabricCanvas.discardActiveObject?.();
		}
		fabricCanvas.requestRenderAll?.();
	}

	function enforceSelectionPermissions(event?: any) {
		void event;
		if (!fabricCanvas || canManageAllBoardElements) return;

		const activeSelection = fabricCanvas.getActiveObject?.();
		if (!activeSelection) return;

		// If it's a multi-selection group
		if ((activeSelection as Record<string, unknown>).type === 'activeSelection') {
			const objects = (activeSelection as any).getObjects?.() || [];
			let selectionChanged = false;

			for (const obj of objects) {
				if (!canMutateBoardObject(obj as FabricObjectLike)) {
					(activeSelection as any).removeWithUpdate?.(obj);
					selectionChanged = true;
				}
			}

			const selectionSize =
				typeof (activeSelection as any).size === 'function'
					? Number((activeSelection as any).size())
					: ((activeSelection as any).getObjects?.() || []).length;

			// If the group is now empty, discard it entirely
			if (selectionSize === 0) {
				fabricCanvas.discardActiveObject?.();
			}

			if (selectionChanged) {
				fabricCanvas.requestRenderAll?.();
			}
		}
		// If it's a single object that somehow got selected
		else {
			if (!canMutateBoardObject(activeSelection as FabricObjectLike)) {
				fabricCanvas.discardActiveObject?.();
				fabricCanvas.requestRenderAll?.();
			}
		}
	}

	function clampDusterCenterX(x: number) {
		const halfStripe = DUSTER_STRIPE_WIDTH / 2;
		return Math.max(halfStripe, Math.min(BOARD_WIDTH - halfStripe, x));
	}

	function markViewportForRender() {
		viewportRenderTick = Date.now();
	}

	function resolveDusterScreenMetrics(_tick: number, centerX: number): DusterScreenMetrics {
		void _tick;
		if (!fabricCanvas || !boardContainerEl) {
			return {
				left: -9999,
				top: 0,
				width: 0,
				height: 0
			};
		}
		const viewport = fabricCanvas.viewportTransform ?? [1, 0, 0, 1, 0, 0];
		const zoom = clampZoom(toNumber(viewport[0], 1));
		const translateX = toNumber(viewport[4], 0);
		const translateY = toNumber(viewport[5], 0);
		const stripeWidthPx = Math.max(10, DUSTER_STRIPE_WIDTH * zoom);
		const stripeLeftBoard = clampDusterCenterX(centerX) - DUSTER_STRIPE_WIDTH / 2;
		const left = translateX + stripeLeftBoard * zoom;
		const top = translateY;
		const height = BOARD_HEIGHT * zoom;
		return {
			left,
			top,
			width: stripeWidthPx,
			height
		};
	}

	function stopDusterDrag() {
		const pointerId = dusterPointerId;
		dusterIsDragging = false;
		dusterPointerId = null;
		if (!boardContainerEl || pointerId === null) {
			return;
		}
		try {
			if (boardContainerEl.hasPointerCapture?.(pointerId)) {
				boardContainerEl.releasePointerCapture(pointerId);
			}
		} catch {
			// Ignore failed pointer release when capture has already ended.
		}
	}

	function moveDusterToBoardX(boardX: number) {
		const nextX = clampDusterCenterX(boardX);
		if (Math.abs(nextX - dusterCenterX) < 0.01) {
			return;
		}
		dusterCenterX = nextX;
		markViewportForRender();
	}

	function moveDusterToClientX(clientX: number) {
		if (!boardContainerEl) {
			return;
		}
		const rect = boardContainerEl.getBoundingClientRect();
		const anchorY = rect.top + Math.max(8, Math.min(rect.height - 8, rect.height * 0.35));
		const point = getBoardPointFromClientPosition(clientX, anchorY);
		moveDusterToBoardX(point.x);
	}

	function clearElementsTouchingDusterStripe() {
		if (!fabricCanvas || !canManageAllBoardElements) {
			return;
		}
		const stripeCenterX = clampDusterCenterX(dusterCenterX);
		const stripeHalfWidth = DUSTER_STRIPE_WIDTH / 2;
		const stripeLeft = stripeCenterX - stripeHalfWidth;
		const stripeRight = stripeCenterX + stripeHalfWidth;
		const objects = [...(fabricCanvas.getObjects?.() ?? [])] as FabricObjectLike[];
		for (const object of objects) {
			if (!object || object === boardBoundsRect || isPendingObject(object)) {
				continue;
			}
			if (!canMutateBoardObject(object)) {
				continue;
			}
			const element = boardObjectToElement(object);
			if (!element) {
				continue;
			}
			const objectLeft = element.x;
			const objectRight = element.x + Math.max(1, element.width);
			if (objectRight < stripeLeft || objectLeft > stripeRight) {
				continue;
			}
			removeBoardObject(object, true);
		}
	}

	function isPendingObject(object: FabricObjectLike | null) {
		if (!object) {
			return false;
		}
		const objectElementId = normalizeMessageID(
			toStringValue((object as Record<string, unknown>).elementId)
		);
		if (pendingInsertElementId && objectElementId === pendingInsertElementId) {
			return true;
		}
		return Boolean((object as Record<string, unknown>).pendingCommit);
	}

	function getPendingInsertObject() {
		if (!pendingInsertElementId) {
			return null;
		}
		return findObjectByElementId(pendingInsertElementId);
	}

	function cancelPendingOperation(captureSnapshot = true) {
		if (pendingInsertElementId && fabricCanvas) {
			const pendingObject = getPendingInsertObject();
			if (pendingObject) {
				fabricCanvas.remove(pendingObject as any);
				fabricCanvas.discardActiveObject?.();
				fabricCanvas.requestRenderAll?.();
				if (captureSnapshot) {
					captureHistorySnapshot();
				}
			}
		}
		pendingInsertElementId = '';
		pendingShapeKind = null;
		pendingShapeAnchorPoint = null;
		pendingShapePointerMoved = false;
	}

	function cancelCurrentOperation() {
		cancelPendingOperation(true);
		if (activeTool !== 'select') {
			applyToolMode('select');
		}
		stopDusterDrag();
		showInsertMenu = false;
		showWidthMenu = false;
		showColorMenu = false;
		contextMenuOpen = false;
		messagePickerOpen = false;
		showBoardDetails = false;
		pendingTapGesture = null;
	}

	function ensureObjectIdentity(object: FabricObjectLike, fallbackType = 'shape') {
		const currentElementId = normalizeMessageID(
			toStringValue((object as Record<string, unknown>).elementId)
		);
		const nextElementId = currentElementId || createMessageId(normalizedRoomId || 'board');
		const currentType = toStringValue((object as Record<string, unknown>).elementType)
			.trim()
			.toLowerCase();
		const nextType = currentType || fallbackType;
		const currentOwnerUserID = normalizeIdentifier(
			toStringValue(
				(object as Record<string, unknown>).createdByUserId ??
					(object as Record<string, unknown>).created_by_user_id ??
					(object as Record<string, unknown>).senderId ??
					(object as Record<string, unknown>).sender_id
			)
		);
		const nextOwnerUserID = currentOwnerUserID || (currentElementId ? '' : normalizedCurrentUserID);
		const currentOwnerName = toStringValue(
			(object as Record<string, unknown>).createdByName ??
				(object as Record<string, unknown>).created_by_name
		).trim();
		const nextOwnerName = currentOwnerName || (nextOwnerUserID ? normalizedCurrentUsername : '');
		object.set?.({
			elementId: nextElementId,
			elementType: nextType,
			createdByUserId: nextOwnerUserID,
			createdByName: nextOwnerName,
			createdAt: parseOptionalTimestamp((object as Record<string, unknown>).createdAt) || Date.now()
		});
		return {
			elementId: nextElementId,
			elementType: nextType,
			createdByUserId: nextOwnerUserID,
			createdByName: nextOwnerName
		};
	}

	function emitBoardElementAdd(object: FabricObjectLike) {
		void emitBoardElementAddEncrypted(object);
	}

	async function emitBoardElementAddEncrypted(object: FabricObjectLike) {
		const element = boardObjectToElement(object);
		if (!element) {
			return;
		}
		const payload: Record<string, unknown> = { ...element };
		if (element.content) {
			payload.content = await encryptBoardContentField(element.content);
		}
		sendBoardEnvelope('board_element_add', payload);
	}

	function emitBoardElementMove(object: FabricObjectLike) {
		void emitBoardElementMoveEncrypted(object);
	}

	async function emitBoardElementMoveEncrypted(object: FabricObjectLike) {
		const element = boardObjectToElement(object);
		if (!element) {
			return;
		}
		const scaleX = toNumber((object as Record<string, unknown>).scaleX, 1);
		const scaleY = toNumber((object as Record<string, unknown>).scaleY, 1);
		const payload: Record<string, unknown> = {
			elementId: element.elementId,
			x: element.x,
			y: element.y,
			width: element.width,
			height: element.height,
			scaleX,
			scaleY,
			zIndex: element.zIndex
		};
		if (element.content) {
			payload.content = await encryptBoardContentField(element.content);
		}
		sendBoardEnvelope('board_element_move', payload);
	}

	function emitBoardElementDelete(elementId: string) {
		const normalizedElementId = normalizeMessageID(elementId);
		if (!normalizedElementId || !canModerateBoardActions) {
			return;
		}
		sendBoardEnvelope('board_element_delete', {
			elementId: normalizedElementId
		});
	}

	function isStackedBoardEventType(type: BoardEventType): type is StackedBoardEventType {
		return type === 'board_element_add' || type === 'board_element_move' || type === 'board_element_delete';
	}

	function enqueuePendingBoardUpdate(type: StackedBoardEventType, payload: Record<string, unknown>) {
		if (!normalizedRoomId) {
			return;
		}
		pendingBoardUpdates = [
			...pendingBoardUpdates,
			{
				roomId: normalizedRoomId,
				type,
				payload: { ...payload }
			}
		];
	}

	function notifyDrawBoardMemoryLimitReached() {
		const now = Date.now();
		if (now - lastDrawBoardLimitToastAt < DRAW_BOARD_LIMIT_TOAST_COOLDOWN_MS) {
			return;
		}
		lastDrawBoardLimitToastAt = now;
		dispatch('toastError', { message: DRAW_BOARD_MEMORY_LIMIT_MESSAGE });
	}

	function canBroadcastBoardUpdates() {
		if (!isEphemeralRoom) {
			return true;
		}
		if (!fabricCanvas) {
			return true;
		}
		if (!latestSerializedBoardSnapshot) {
			refreshBoardStats();
		}
		if (latestSerializedBoardSnapshotBytes <= EPHEMERAL_DRAW_BOARD_LIMIT_BYTES) {
			return true;
		}
		pendingBoardUpdates = [];
		notifyDrawBoardMemoryLimitReached();
		return false;
	}

	function flushPendingBoardUpdates() {
		if (pendingBoardUpdates.length === 0) {
			return;
		}
		if (!canBroadcastBoardUpdates()) {
			return;
		}

		const updatesToFlush = pendingBoardUpdates.slice();
		pendingBoardUpdates = [];

		const updatesByRoom = new Map<string, PendingBoardUpdate[]>();
		for (const update of updatesToFlush) {
			const roomKey = normalizeRoomIDValue(update.roomId);
			if (!roomKey) {
				continue;
			}
			const roomUpdates = updatesByRoom.get(roomKey) ?? [];
			roomUpdates.push(update);
			updatesByRoom.set(roomKey, roomUpdates);
		}

		for (const [roomId, roomUpdates] of updatesByRoom.entries()) {
			if (roomUpdates.length === 0) {
				continue;
			}
			sendSocketPayload({
				type: BOARD_EVENT_BATCH_TYPE,
				roomId,
				payload: roomUpdates.map((update) => ({
					type: update.type,
					payload: update.payload
				}))
			});
		}
	}

	function startBoardUpdateFlushLoop() {
		if (!browser || boardUpdateFlushInterval) {
			return;
		}
		boardUpdateFlushInterval = setInterval(() => {
			flushPendingBoardUpdates();
		}, BOARD_UPDATE_STACK_FLUSH_MS);
	}

	function stopBoardUpdateFlushLoop() {
		if (!boardUpdateFlushInterval) {
			return;
		}
		clearInterval(boardUpdateFlushInterval);
		boardUpdateFlushInterval = null;
	}

	function sendBoardEnvelope(type: BoardEventType, payload: Record<string, unknown>) {
		if (!normalizedRoomId || !canEdit) {
			return;
		}
		if (!canBroadcastBoardUpdates()) {
			return;
		}
		if (isStackedBoardEventType(type)) {
			enqueuePendingBoardUpdate(type, payload);
			return;
		}
		if (type === 'board_clear') {
			flushPendingBoardUpdates();
		}
		sendSocketPayload({
			type,
			roomId: normalizedRoomId,
			payload
		});
	}

	function normalizeBoardPasswordValue(value: string) {
		return (value || '').trim().slice(0, 32);
	}

	async function encryptBoardContentField(content: string) {
		return encryptText(content, normalizeBoardPasswordValue($activeRoomPassword));
	}

	async function decryptBoardContentField(content: string) {
		return decryptText(content, normalizeBoardPasswordValue($activeRoomPassword));
	}

	function boardObjectToElement(object: FabricObjectLike): BoardElementWire | null {
		const { elementId, elementType, createdByUserId, createdByName } = ensureObjectIdentity(object);
		const left = toNumber((object as Record<string, unknown>).left, 0);
		const top = toNumber((object as Record<string, unknown>).top, 0);
		const scaleX = toNumber((object as Record<string, unknown>).scaleX, 1);
		const scaleY = toNumber((object as Record<string, unknown>).scaleY, 1);
		const rawWidth = toNumber((object as Record<string, unknown>).width, 0);
		const rawHeight = toNumber((object as Record<string, unknown>).height, 0);
		const width = Math.max(1, rawWidth * Math.abs(scaleX || 1));
		const height = Math.max(1, rawHeight * Math.abs(scaleY || 1));
		const absoluteIndex = toInt(fabricCanvas?.getObjects?.().indexOf(object as any) ?? 0);
		const zIndexOffset = boardBoundsRect ? 1 : 0;
		const zIndex = Math.max(0, absoluteIndex - zIndexOffset);
		const createdAt =
			parseOptionalTimestamp((object as Record<string, unknown>).createdAt) || Date.now();

		const content = buildElementContent(object, elementType, left, top, width, height);

		return {
			elementId,
			elementType,
			x: left,
			y: top,
			width,
			height,
			content,
			zIndex,
			createdByUserId,
			createdByName,
			createdAt
		};
	}

	function toRecord(value: unknown): Record<string, unknown> | null {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return null;
		}
		return value as Record<string, unknown>;
	}

	function parseContentRecord(content: string): Record<string, unknown> | null {
		const trimmed = toStringValue(content).trim();
		if (!trimmed.startsWith('{') || !trimmed.endsWith('}')) {
			return null;
		}
		try {
			return toRecord(JSON.parse(trimmed));
		} catch {
			return null;
		}
	}

	function normalizeOptionalColor(value: unknown) {
		const normalized = toStringValue(value).trim();
		return normalized || '';
	}

	function normalizeOptionalPositiveNumber(value: unknown) {
		const parsed = toNumber(value, 0);
		return parsed > 0 ? parsed : 0;
	}

	function buildElementContent(
		object: FabricObjectLike,
		elementType: string,
		left: number,
		top: number,
		width: number,
		height: number
	) {
		const objectRecord = object as Record<string, unknown>;
		let baseContent = toStringValue(objectRecord.content);
		if (!baseContent) {
			baseContent = toStringValue(objectRecord.text);
		}

		if (elementType === 'text_box') {
			return JSON.stringify({
				schema: BOARD_TEXT_BOX_SCHEMA,
				text: toStringValue(objectRecord.text),
				fill: normalizeOptionalColor(objectRecord.fill),
				fontSize: normalizeOptionalPositiveNumber(objectRecord.fontSize),
				lineHeight: normalizeOptionalPositiveNumber(objectRecord.lineHeight)
			});
		}

		if (elementType === 'stroke') {
			const strokePath = (objectRecord.path as unknown[]) ?? [];
			const path = serializePathCommands(strokePath);
			if (!path) {
				return baseContent;
			}
			return JSON.stringify({
				schema: BOARD_STROKE_SCHEMA,
				path,
				stroke: normalizeOptionalColor(objectRecord.stroke),
				fill: normalizeOptionalColor(objectRecord.fill),
				strokeWidth: normalizeOptionalPositiveNumber(objectRecord.strokeWidth)
			});
		}

		if (elementType === 'line' || elementType === 'arrow') {
			return JSON.stringify({
				x1: toNumber(objectRecord.x1, left),
				y1: toNumber(objectRecord.y1, top),
				x2: toNumber(objectRecord.x2, left + width),
				y2: toNumber(objectRecord.y2, top + height),
				stroke: normalizeOptionalColor(objectRecord.stroke),
				strokeWidth: normalizeOptionalPositiveNumber(objectRecord.strokeWidth)
			});
		}

		if (
			elementType === 'rect' ||
			elementType === 'shape' ||
			elementType === 'circle' ||
			elementType === 'ellipse' ||
			elementType === 'triangle'
		) {
			return JSON.stringify({
				schema: BOARD_SHAPE_STYLE_SCHEMA,
				stroke: normalizeOptionalColor(objectRecord.stroke),
				fill: normalizeOptionalColor(objectRecord.fill),
				strokeWidth: normalizeOptionalPositiveNumber(objectRecord.strokeWidth)
			});
		}

		return baseContent;
	}

	function serializePathCommands(pathCommands: unknown[]) {
		if (!Array.isArray(pathCommands) || pathCommands.length === 0) {
			return '';
		}
		return pathCommands
			.map((command) => {
				if (!Array.isArray(command) || command.length === 0) {
					return '';
				}
				return command.map((part) => toStringValue(part)).join(' ');
			})
			.filter((entry) => entry !== '')
			.join(' ');
	}

	async function loadBoard(targetRoomId: string) {
		const normalizedTargetRoomId = normalizeRoomIDValue(targetRoomId);
		if (!normalizedTargetRoomId || !fabricCanvas) {
			return;
		}
		boardLoading = true;
		boardError = '';
		restoreLocalActionHistory(normalizedTargetRoomId);
		try {
			const res = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedTargetRoomId)}/board`
			);
			if (!res.ok) {
				throw new Error(`Load failed (${res.status})`);
			}
			const payload = (await res.json()) as unknown;
			const elements = Array.isArray(payload) ? payload : [];
			beginRemoteApply();
			clearBoardElements(false);
			for (const rawElement of elements) {
				const parsed = await parseBoardElementRecordDecrypted(rawElement);
				if (!parsed) {
					continue;
				}
				await addOrReplaceElementOnCanvas(parsed);
			}
			initializedRoomId = normalizedTargetRoomId;
			captureHistorySnapshot(true);
			fabricCanvas.requestRenderAll?.();
		} catch (error) {
			boardError = error instanceof Error ? error.message : 'Failed to load board';
		} finally {
			endRemoteApply();
			boardLoading = false;
		}
	}

	function clearBoardElements(resetLocalActions = true) {
		clearBoardWithBounds(false, resetLocalActions);
	}

	function clearBoardWithBounds(emitClearEvent: boolean, resetLocalActions = true) {
		if (!fabricCanvas) {
			return;
		}
		fabricCanvas.clear();
		ensureBoardBoundsObject();
		updateBoardVisualTheme(isDarkMode);
		fabricCanvas.discardActiveObject?.();
		pendingInsertElementId = '';
		pendingShapeKind = null;
		pendingShapeAnchorPoint = null;
		pendingShapePointerMoved = false;
		pendingTransformSnapshotByElementId.clear();
		if (resetLocalActions) {
			localUndoStack = [];
			localRedoStack = [];
			persistLocalActionHistory();
		}
		zControlVisible = false;
		refreshBoardStats();
		captureHistorySnapshot(true);
		fabricCanvas.requestRenderAll?.();
		if (emitClearEvent) {
			sendBoardEnvelope('board_clear', {});
		}
	}

	function parseBoardElementRecord(value: unknown): BoardElementWire | null {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return null;
		}
		const record = value as Record<string, unknown>;
		const elementId = normalizeMessageID(
			toStringValue(record.elementId ?? record.element_id ?? record.id)
		);
		const elementType = toStringValue(record.elementType ?? record.element_type ?? record.type)
			.trim()
			.toLowerCase();
		if (!elementId || !elementType) {
			return null;
		}
		const x = toNumber(record.x, 0);
		const y = toNumber(record.y, 0);
		const width = Math.max(1, toNumber(record.width, 120));
		const height = Math.max(1, toNumber(record.height, 80));
		const content = toStringValue(record.content);
		const zIndex = toInt(record.zIndex ?? record.z_index);
		const createdByUserId = normalizeIdentifier(
			toStringValue(
				record.createdByUserId ?? record.created_by_user_id ?? record.senderId ?? record.sender_id
			)
		);
		const createdByName = toStringValue(
			record.createdByName ?? record.created_by_name ?? record.senderName ?? record.sender_name
		).trim();
		const createdAt = parseOptionalTimestamp(record.createdAt ?? record.created_at) || Date.now();
		return {
			elementId,
			elementType,
			x,
			y,
			width,
			height,
			content,
			zIndex,
			createdByUserId,
			createdByName,
			createdAt
		};
	}

	async function parseBoardElementRecordDecrypted(
		value: unknown
	): Promise<BoardElementWire | null> {
		const parsed = parseBoardElementRecord(value);
		if (!parsed || !parsed.content) {
			return parsed;
		}
		const decryptedContent = await decryptBoardContentField(parsed.content);
		if (decryptedContent === parsed.content) {
			return parsed;
		}
		return {
			...parsed,
			content: decryptedContent
		};
	}

	async function addOrReplaceElementOnCanvas(element: BoardElementWire) {
		if (!fabricCanvas) {
			return;
		}
		const existingObject = findObjectByElementId(element.elementId);
		if (existingObject) {
			fabricCanvas.remove(existingObject);
		}
		const nextObject = await createFabricObjectFromElement(element);
		if (!nextObject) {
			return;
		}
		nextObject.set?.({
			elementId: element.elementId,
			elementType: element.elementType,
			content: element.content,
			createdByUserId: element.createdByUserId,
			createdByName: element.createdByName,
			createdAt: element.createdAt
		});
		applyObjectPermission(nextObject);
		fabricCanvas.add(nextObject);
		const totalObjects = toInt(fabricCanvas.getObjects?.().length ?? 1);
		const minIndex = boardBoundsRect ? 1 : 0;
		const targetIndex = Math.max(minIndex, Math.min(totalObjects - 1, element.zIndex + minIndex));
		fabricCanvas.moveTo?.(nextObject, targetIndex);
	}

	function parseStrokeContent(content: string) {
		const record = parseContentRecord(content);
		if (!record) {
			return null;
		}
		const schema = toStringValue(record.schema).trim().toLowerCase();
		const hasSchema = schema !== '';
		if (hasSchema && schema !== BOARD_STROKE_SCHEMA) {
			return null;
		}
		const path = toStringValue(record.path);
		if (!path) {
			return null;
		}
		const strokeWidth = normalizeOptionalPositiveNumber(record.strokeWidth ?? record.stroke_width);
		return {
			path,
			stroke: normalizeOptionalColor(record.stroke),
			fill: normalizeOptionalColor(record.fill),
			strokeWidth
		};
	}

	function parseShapeStyleContent(content: string) {
		const record = parseContentRecord(content);
		if (!record) {
			return null;
		}
		const schema = toStringValue(record.schema).trim().toLowerCase();
		const hasSchema = schema !== '';
		if (hasSchema && schema !== BOARD_SHAPE_STYLE_SCHEMA) {
			return null;
		}
		const hasStyleKeys =
			Object.prototype.hasOwnProperty.call(record, 'stroke') ||
			Object.prototype.hasOwnProperty.call(record, 'fill') ||
			Object.prototype.hasOwnProperty.call(record, 'strokeWidth') ||
			Object.prototype.hasOwnProperty.call(record, 'stroke_width');
		if (!hasSchema && !hasStyleKeys) {
			return null;
		}
		const strokeWidth = normalizeOptionalPositiveNumber(record.strokeWidth ?? record.stroke_width);
		return {
			stroke: normalizeOptionalColor(record.stroke),
			fill: normalizeOptionalColor(record.fill),
			strokeWidth
		};
	}

	function parseTextBoxContent(content: string) {
		const record = parseContentRecord(content);
		if (!record) {
			return null;
		}
		const schema = toStringValue(record.schema).trim().toLowerCase();
		const hasSchema = schema !== '';
		if (hasSchema && schema !== BOARD_TEXT_BOX_SCHEMA) {
			return null;
		}
		const hasTextKeys =
			Object.prototype.hasOwnProperty.call(record, 'text') ||
			Object.prototype.hasOwnProperty.call(record, 'fill') ||
			Object.prototype.hasOwnProperty.call(record, 'fontSize') ||
			Object.prototype.hasOwnProperty.call(record, 'font_size') ||
			Object.prototype.hasOwnProperty.call(record, 'lineHeight') ||
			Object.prototype.hasOwnProperty.call(record, 'line_height');
		if (!hasSchema && !hasTextKeys) {
			return null;
		}
		const text = toStringValue(record.text);
		return {
			text,
			fill: normalizeOptionalColor(record.fill),
			fontSize: normalizeOptionalPositiveNumber(record.fontSize ?? record.font_size),
			lineHeight: normalizeOptionalPositiveNumber(record.lineHeight ?? record.line_height)
		};
	}

	function parseLineContent(content: string) {
		const record = parseContentRecord(content);
		if (!record) {
			return null;
		}
		const hasLineCoordinates =
			Object.prototype.hasOwnProperty.call(record, 'x1') ||
			Object.prototype.hasOwnProperty.call(record, 'y1') ||
			Object.prototype.hasOwnProperty.call(record, 'x2') ||
			Object.prototype.hasOwnProperty.call(record, 'y2');
		if (!hasLineCoordinates) {
			return null;
		}
		const strokeWidth = normalizeOptionalPositiveNumber(record.strokeWidth ?? record.stroke_width);
		return {
			x1: toNumber(record.x1, 0),
			y1: toNumber(record.y1, 0),
			x2: toNumber(record.x2, 0),
			y2: toNumber(record.y2, 0),
			stroke: normalizeOptionalColor(record.stroke),
			strokeWidth
		};
	}

	async function createFabricObjectFromElement(
		element: BoardElementWire
	): Promise<FabricObjectLike | null> {
		const { elementType } = element;
		const fallbackStrokeColor = isDarkMode ? '#f3f4f6' : '#111827';
		const fallbackFillColor = isDarkMode ? 'rgba(148, 163, 184, 0.16)' : 'rgba(30, 64, 175, 0.08)';
		const fallbackTextColor = isDarkMode ? '#f3f4f6' : '#111827';

		if (elementType === 'stroke' && element.content) {
			const PathClass = getFabricClass('Path');
			if (!PathClass) {
				return null;
			}
			const strokeContent = parseStrokeContent(element.content);
			const pathData = strokeContent?.path || element.content;
			const strokeColor = resolveThemeAwareInkColor(strokeContent?.stroke || fallbackStrokeColor);
			const fillColor = resolveThemeAwareInkColor(strokeContent?.fill || '');
			const strokeWidth = strokeContent?.strokeWidth || 2;
			try {
				return new PathClass(pathData, {
					left: element.x,
					top: element.y,
					stroke: strokeColor,
					fill: fillColor,
					strokeWidth
				}) as FabricObjectLike;
			} catch {
				return null;
			}
		}

		const shapeStyle = parseShapeStyleContent(element.content);
		const shapeStrokeColor = resolveThemeAwareInkColor(shapeStyle?.stroke || fallbackStrokeColor);
		const shapeFillColor = resolveThemeAwareInkColor(shapeStyle?.fill || fallbackFillColor);
		const shapeStrokeWidth = shapeStyle?.strokeWidth || 2;

		if (elementType === 'rect' || elementType === 'shape') {
			const RectClass = getFabricClass('Rect');
			return RectClass
				? (new RectClass({
						left: element.x,
						top: element.y,
						width: element.width,
						height: element.height,
						rx: 10,
						ry: 10,
						stroke: shapeStrokeColor,
						strokeWidth: shapeStrokeWidth,
						fill: shapeFillColor
					}) as FabricObjectLike)
				: null;
		}

		if (elementType === 'circle') {
			const CircleClass = getFabricClass('Circle');
			if (!CircleClass) {
				return null;
			}
			return new CircleClass({
				left: element.x,
				top: element.y,
				radius: Math.max(element.width, element.height) / 2,
				stroke: shapeStrokeColor,
				strokeWidth: shapeStrokeWidth,
				fill: shapeFillColor
			}) as FabricObjectLike;
		}

		if (elementType === 'ellipse') {
			const EllipseClass = getFabricClass('Ellipse');
			if (!EllipseClass) {
				return null;
			}
			return new EllipseClass({
				left: element.x,
				top: element.y,
				rx: Math.max(1, element.width / 2),
				ry: Math.max(1, element.height / 2),
				stroke: shapeStrokeColor,
				strokeWidth: shapeStrokeWidth,
				fill: shapeFillColor
			}) as FabricObjectLike;
		}

		if (elementType === 'triangle') {
			const TriangleClass = getFabricClass('Triangle');
			if (!TriangleClass) {
				return null;
			}
			return new TriangleClass({
				left: element.x,
				top: element.y,
				width: element.width,
				height: element.height,
				stroke: shapeStrokeColor,
				strokeWidth: shapeStrokeWidth,
				fill: shapeFillColor
			}) as FabricObjectLike;
		}

		if (elementType === 'line' || elementType === 'arrow') {
			const LineClass = getFabricClass('Line');
			if (!LineClass) {
				return null;
			}
			const lineContent = parseLineContent(element.content);
			const linePoints = lineContent
				? [lineContent.x1, lineContent.y1, lineContent.x2, lineContent.y2]
				: parseLinePoints(element.content, element);
			const lineStrokeColor = resolveThemeAwareInkColor(lineContent?.stroke || fallbackStrokeColor);
			const lineStrokeWidth = lineContent?.strokeWidth || (elementType === 'arrow' ? 4 : 3);
			return new LineClass(linePoints, {
				stroke: lineStrokeColor,
				strokeWidth: lineStrokeWidth
			}) as FabricObjectLike;
		}

		if (elementType === 'image') {
			const parsedMedia = parseBoardMediaContent(element.content);
			if (parsedMedia?.url) {
				const imageObject = await createImageObjectFromMedia(
					parsedMedia,
					element.x,
					element.y,
					element.width,
					element.height
				);
				if (imageObject) {
					return imageObject;
				}
			}
		}

		if (
			elementType === 'image' ||
			elementType === 'video' ||
			elementType === 'audio' ||
			elementType === 'file' ||
			elementType === 'media'
		) {
			const media = parseBoardMediaContent(element.content) ?? {
				url: '',
				name: 'Attachment',
				kind: 'file',
				mimeType: '',
				sizeBytes: 0,
				caption: '',
				senderName: '',
				sentAt: 0
			};
			const mediaObject = createMediaCardObject(
				media,
				element.x,
				element.y,
				element.width,
				element.height
			);
			if (mediaObject) {
				return mediaObject;
			}
		}

		if (elementType === 'text_box') {
			const textBoxContent = parseTextBoxContent(element.content);
			const textValue = textBoxContent ? textBoxContent.text : element.content || 'Text';
			const textColor = resolveThemeAwareInkColor(textBoxContent?.fill || fallbackTextColor);
			const textFontSize = textBoxContent?.fontSize || 18;
			const textLineHeight = textBoxContent?.lineHeight || 1.28;
			const TextboxClass = getFabricClass('Textbox') ?? getFabricClass('Text');
			if (!TextboxClass) {
				return null;
			}
			return new TextboxClass(textValue, {
				left: clampBoardX(element.x, Math.max(140, element.width)),
				top: clampBoardY(element.y, Math.max(48, element.height)),
				width: Math.max(140, element.width),
				height: Math.max(48, element.height),
				fontSize: textFontSize,
				lineHeight: textLineHeight,
				fill: textColor,
				backgroundColor: 'transparent',
				padding: 8
			}) as FabricObjectLike;
		}

		if (elementType === 'sticky_note') {
			return createStickyNoteObject(
				element.content || 'Type here...',
				element.x,
				element.y,
				Math.max(120, element.width),
				Math.max(120, element.height)
			);
		}

		if (elementType === 'message') {
			const richMessage = parseRichMessageCardPayload(element.content);
			if (richMessage) {
				return await createRichMessageObjectFromPayload(
					richMessage,
					element.x,
					element.y,
					Math.max(220, element.width)
				);
			}
		}

		return createMessageCardObject(
			element.content || `Pinned message (${element.elementId.slice(0, 6)})`,
			element.x,
			element.y,
			Math.max(150, element.width)
		);
	}

	function parseBoardMediaContent(rawContent: string): BoardMediaContent | null {
		const raw = toStringValue(rawContent).trim();
		if (!raw) {
			return null;
		}
		if (!raw.startsWith('{')) {
			return {
				url: raw,
				name: inferFileNameFromURL(raw) || raw.split('/').pop() || 'File',
				kind: 'file',
				mimeType: '',
				sizeBytes: 0,
				caption: '',
				senderName: '',
				sentAt: 0
			};
		}
		try {
			const parsed = JSON.parse(raw) as Record<string, unknown>;
			const url = toStringValue(parsed.url).trim();
			if (!url) {
				return null;
			}
			const rawCaption = toStringValue(parsed.caption ?? parsed.text ?? parsed.content).trim();
			const normalizedCaption = rawCaption && rawCaption !== url ? rawCaption : '';
			return {
				url,
				name: toStringValue(parsed.name).trim() || inferFileNameFromURL(url) || 'Attachment',
				kind: normalizeMediaKind(toStringValue(parsed.kind)),
				mimeType: toStringValue(parsed.mimeType ?? parsed.mime_type),
				sizeBytes: Math.max(0, toNumber(parsed.sizeBytes ?? parsed.size_bytes, 0)),
				caption: normalizedCaption,
				senderName: toStringValue(parsed.senderName ?? parsed.sender_name).trim(),
				sentAt: parseOptionalTimestamp(
					parsed.sentAt ?? parsed.sent_at ?? parsed.createdAt ?? parsed.created_at
				)
			};
		} catch {
			return null;
		}
	}

	function normalizeMediaKind(rawKind: string): MediaMessageType {
		const normalized = rawKind.trim().toLowerCase();
		if (normalized === 'image' || normalized === 'video' || normalized === 'audio') {
			return normalized;
		}
		return 'file';
	}

	function getBoardCardWidth(type: 'message' | 'media' = 'message') {
		return type === 'media' ? DEFAULT_MEDIA_CARD_WIDTH : DEFAULT_MESSAGE_CARD_WIDTH;
	}

	function createStickyNoteObject(
		content: string,
		left: number,
		top: number,
		explicitWidth = 150,
		explicitHeight = 150
	): FabricObjectLike | null {
		const TextboxClass = getFabricClass('Textbox') ?? getFabricClass('Text');
		if (!TextboxClass) {
			return null;
		}
		const width = Math.max(120, explicitWidth);
		const height = Math.max(120, explicitHeight);
		const text = toStringValue(content).trim() || 'Type here...';
		return new TextboxClass(text, {
			left: clampBoardX(left, width),
			top: clampBoardY(top, height),
			width,
			height,
			fontSize: 16,
			lineHeight: 1.2,
			fill: '#1f2937',
			backgroundColor: '#fef08a',
			padding: 12
		}) as FabricObjectLike;
	}

	function createMessageCardObject(
		content: string,
		left: number,
		top: number,
		explicitWidth = 0
	): FabricObjectLike | null {
		const TextboxClass = getFabricClass('Textbox') ?? getFabricClass('Text');
		if (!TextboxClass) {
			return null;
		}
		const cardWidth = Math.max(170, explicitWidth || getBoardCardWidth('message'));
		return new TextboxClass(content || '(empty)', {
			left: clampBoardX(left, cardWidth),
			top: clampBoardY(top, MIN_SHAPE_HEIGHT),
			width: cardWidth,
			fontSize: 14,
			lineHeight: 1.32,
			fill: isDarkMode ? '#f3f4f6' : '#111827',
			backgroundColor: isDarkMode ? '#1f2937' : '#fef9c3',
			padding: 10
		}) as FabricObjectLike;
	}

	function createRichMessageCardPayload(message: ChatMessage): BoardMessageCardPayload {
		return {
			schema: RICH_MESSAGE_SCHEMA,
			messageId: normalizeMessageID(message.id) || createMessageId(normalizedRoomId || 'board'),
			senderId: normalizeIdentifier(message.senderId),
			senderName: toStringValue(message.senderName).trim() || 'Guest',
			content: toStringValue(message.content),
			type: toStringValue(message.type).trim().toLowerCase() || 'text',
			mediaUrl: toStringValue(message.mediaUrl).trim(),
			mediaType: toStringValue(message.mediaType).trim(),
			fileName: toStringValue(message.fileName).trim(),
			createdAt:
				Number.isFinite(message.createdAt) && message.createdAt > 0
					? message.createdAt
					: Date.now(),
			replyToSnippet: toStringValue(message.replyToSnippet).trim()
		};
	}

	function parseRichMessageCardPayload(rawContent: string): BoardMessageCardPayload | null {
		const raw = toStringValue(rawContent).trim();
		if (!raw || !raw.startsWith('{')) {
			return null;
		}
		try {
			const parsed = JSON.parse(raw) as Record<string, unknown>;
			const schema = toStringValue(parsed.schema).trim();
			if (schema !== RICH_MESSAGE_SCHEMA) {
				return null;
			}
			return {
				schema,
				messageId:
					normalizeMessageID(toStringValue(parsed.messageId ?? parsed.message_id)) ||
					createMessageId(normalizedRoomId || 'board'),
				senderId: normalizeIdentifier(toStringValue(parsed.senderId ?? parsed.sender_id)),
				senderName: toStringValue(parsed.senderName ?? parsed.sender_name).trim() || 'Guest',
				content: toStringValue(parsed.content),
				type: toStringValue(parsed.type).trim().toLowerCase() || 'text',
				mediaUrl: toStringValue(parsed.mediaUrl ?? parsed.media_url).trim(),
				mediaType: toStringValue(parsed.mediaType ?? parsed.media_type).trim(),
				fileName: toStringValue(parsed.fileName ?? parsed.file_name).trim(),
				createdAt: parseOptionalTimestamp(parsed.createdAt ?? parsed.created_at) || Date.now(),
				replyToSnippet: toStringValue(parsed.replyToSnippet ?? parsed.reply_to_snippet).trim()
			};
		} catch {
			return null;
		}
	}

	function richMessageHasImage(payload: BoardMessageCardPayload) {
		const mediaURL = toStringValue(payload.mediaUrl).trim();
		if (!mediaURL) {
			return false;
		}
		if (payload.type === 'image') {
			return true;
		}
		if (toStringValue(payload.mediaType).toLowerCase().startsWith('image/')) {
			return true;
		}
		return /\.(png|jpe?g|gif|webp|avif|bmp|svg)(\?|#|$)/i.test(mediaURL);
	}

	function toChatMessageFromRichPayload(payload: BoardMessageCardPayload): ChatMessage {
		return {
			id: payload.messageId,
			roomId: normalizedRoomId,
			senderId: payload.senderId,
			senderName: payload.senderName,
			content: payload.content,
			type: payload.type,
			mediaUrl: payload.mediaUrl,
			mediaType: payload.mediaType,
			fileName: payload.fileName,
			replyToSnippet: payload.replyToSnippet,
			createdAt: payload.createdAt
		};
	}

	async function createRichMessageObject(
		message: ChatMessage,
		left: number,
		top: number,
		explicitWidth = 0
	): Promise<FabricObjectLike | null> {
		const payload = createRichMessageCardPayload(message);
		return createRichMessageObjectFromPayload(payload, left, top, explicitWidth);
	}

	async function createRichMessageObjectFromPayload(
		payload: BoardMessageCardPayload,
		left: number,
		top: number,
		explicitWidth = 0
	): Promise<FabricObjectLike | null> {
		const RectClass = getFabricClass('Rect');
		const GroupClass = getFabricClass('Group');
		const TextClass = getFabricClass('Text');
		const TextboxClass = getFabricClass('Textbox') ?? TextClass;
		if (!RectClass || !GroupClass || !TextClass || !TextboxClass) {
			return createMessageCardObject(
				buildMessageCardBody(toChatMessageFromRichPayload(payload)),
				left,
				top,
				explicitWidth || getBoardCardWidth('message')
			);
		}

		const cardWidth = Math.max(260, Math.min(420, explicitWidth || getBoardCardWidth('message')));
		const padding = 14;
		const maxContentWidth = cardWidth - padding * 2;
		const senderText = new TextClass(payload.senderName || 'Guest', {
			left: padding,
			top: padding,
			fontSize: 12,
			fontWeight: '700',
			fill: isDarkMode ? '#e2e8f0' : '#111827'
		});
		const timestampText = new TextClass(formatBoardMessageDateTime(payload.createdAt), {
			left: padding,
			top: padding + 15,
			fontSize: 10,
			fill: isDarkMode ? '#94a3b8' : '#475569'
		});
		const groupChildren: any[] = [senderText, timestampText];
		const messageLikePayload = toChatMessageFromRichPayload(payload);
		const parsedTask =
			payload.type === 'task'
				? parseTaskMessagePayload(toStringValue(messageLikePayload.content))
				: null;
		let contentBottom = padding + 34;
		if (parsedTask) {
			const taskTitle = new TextboxClass(parsedTask.title || 'Task', {
				left: padding,
				top: contentBottom,
				width: maxContentWidth,
				fontSize: 13,
				fontWeight: '700',
				lineHeight: 1.3,
				fill: isDarkMode ? '#f8fafc' : '#0f172a'
			});
			groupChildren.push(taskTitle);
			contentBottom += Math.max(20, toNumber((taskTitle as any).height, 20)) + 3;

			const completedCount = parsedTask.tasks.filter((task) => task.completed).length;
			const progressLabel = new TextClass(`${completedCount}/${parsedTask.tasks.length} done`, {
				left: padding,
				top: contentBottom,
				fontSize: 10,
				fill: isDarkMode ? '#94a3b8' : '#64748b'
			});
			groupChildren.push(progressLabel);
			contentBottom += 18;

			const checkboxSize = 14;
			const visibleTasks = parsedTask.tasks.slice(0, 8);
			if (visibleTasks.length === 0) {
				const emptyState = new TextClass('No checklist items', {
					left: padding,
					top: contentBottom + 2,
					fontSize: 12,
					fill: isDarkMode ? '#94a3b8' : '#64748b'
				});
				groupChildren.push(emptyState);
				contentBottom += 22;
			} else {
				for (const task of visibleTasks) {
					const rowTop = contentBottom;
					const isDone = Boolean(task.completed);
					const checkbox = new RectClass({
						left: padding,
						top: rowTop + 1,
						width: checkboxSize,
						height: checkboxSize,
						rx: 3,
						ry: 3,
						fill: isDone ? '#22c55e' : isDarkMode ? '#0f172a' : '#ffffff',
						stroke: isDone ? '#22c55e' : isDarkMode ? '#475569' : '#94a3b8',
						strokeWidth: 1
					});
					groupChildren.push(checkbox);

					if (isDone) {
						const tick = new TextClass('✓', {
							left: padding + 3,
							top: rowTop - 0.5,
							fontSize: 12,
							fontWeight: '700',
							fill: '#ffffff'
						});
						groupChildren.push(tick);
					}

					const taskText = new TextboxClass(task.text || 'Untitled task', {
						left: padding + checkboxSize + 8,
						top: rowTop,
						width: maxContentWidth - checkboxSize - 8,
						fontSize: 13,
						lineHeight: 1.25,
						fill: isDone
							? isDarkMode
								? '#94a3b8'
								: '#64748b'
							: isDarkMode
								? '#e2e8f0'
								: '#1e293b',
						textDecoration: isDone ? 'line-through' : ''
					});
					groupChildren.push(taskText);
					const rowHeight = Math.max(18, toNumber((taskText as any).height, 18));
					contentBottom = rowTop + rowHeight + 7;
				}
			}

			const remainingCount = Math.max(0, parsedTask.tasks.length - visibleTasks.length);
			if (remainingCount > 0) {
				const moreText = new TextClass(`+${remainingCount} more`, {
					left: padding,
					top: contentBottom + 1,
					fontSize: 11,
					fill: isDarkMode ? '#94a3b8' : '#64748b'
				});
				groupChildren.push(moreText);
				contentBottom += 20;
			}
		} else {
			const bodyText = buildMessageCardBody(messageLikePayload);
			const bodyObject = new TextboxClass(bodyText || '(empty)', {
				left: padding,
				top: contentBottom,
				width: maxContentWidth,
				fontSize: 14,
				lineHeight: 1.34,
				fill: isDarkMode ? '#f8fafc' : '#0f172a'
			});
			groupChildren.push(bodyObject);
			contentBottom += Math.max(22, toNumber((bodyObject as any).height, 22));
		}
		if (richMessageHasImage(payload) && payload.mediaUrl) {
			const ImageClass = getFabricClass('Image') ?? getFabricClass('FabricImage');
			if (ImageClass) {
				try {
					const imageEl = await loadBrowserImage(payload.mediaUrl);
					const imageObject = new ImageClass(imageEl, {
						left: padding,
						top: contentBottom + 10
					});
					const rawWidth = Math.max(
						1,
						toNumber((imageObject as any).width, imageEl.naturalWidth || imageEl.width || 1)
					);
					const rawHeight = Math.max(
						1,
						toNumber((imageObject as any).height, imageEl.naturalHeight || imageEl.height || 1)
					);
					const maxImageWidth = Math.min(300, maxContentWidth);
					const imageScale = Math.min(maxImageWidth / rawWidth, 1);
					const targetWidth = Math.max(80, rawWidth * imageScale);
					const targetHeight = Math.max(60, rawHeight * imageScale);
					(imageObject as any).set?.({
						scaleX: targetWidth / rawWidth,
						scaleY: targetHeight / rawHeight
					});
					groupChildren.push(imageObject);
					contentBottom += 10 + targetHeight;
				} catch {
					// Keep message card render even if media fetch fails.
				}
			}
		}
		const cardHeight = Math.max(100, contentBottom + padding);
		const background = new RectClass({
			left: 0,
			top: 0,
			width: cardWidth,
			height: cardHeight,
			rx: 14,
			ry: 14,
			fill: isDarkMode ? '#111827' : '#f8fafc',
			stroke: isDarkMode ? '#334155' : '#cbd5e1',
			strokeWidth: 1.2
		});
		groupChildren.unshift(background);
		return new GroupClass(groupChildren, {
			left: clampBoardX(left, cardWidth),
			top: clampBoardY(top, cardHeight)
		}) as FabricObjectLike;
	}

	function createMediaCardObject(
		media: BoardMediaContent,
		left: number,
		top: number,
		width: number,
		height: number
	): FabricObjectLike | null {
		const GroupClass = getFabricClass('Group');
		const RectClass = getFabricClass('Rect');
		const TextClass = getFabricClass('Textbox');
		const PathClass = getFabricClass('Path');
		if (!GroupClass || !RectClass || !TextClass || !PathClass) return null;

		const cardWidth = Math.max(160, width || 220);
		const cardHeight = Math.max(60, height || 80);

		// Background card
		const bg = new RectClass({
			left: 0,
			top: 0,
			width: cardWidth,
			height: cardHeight,
			fill: isDarkMode ? '#1e293b' : '#ffffff',
			stroke: isDarkMode ? '#475569' : '#cbd5e1',
			strokeWidth: 1,
			rx: 8,
			ry: 8
		});

		// File Icon SVG Path (Generic Document Icon)
		const iconPath =
			'M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-6-6zm-1 1.5L18.5 9H13V3.5zM6 20V4h5v7h7v9H6z';
		const icon = new PathClass(iconPath, {
			left: 12,
			top: cardHeight / 2 - 12,
			fill: isDarkMode ? '#94a3b8' : '#64748b',
			scaleX: 1.2,
			scaleY: 1.2
		});

		// Filename Text
		const fileName = new TextClass(media.name || 'Attachment', {
			left: 48,
			top: cardHeight / 2 - 14,
			width: cardWidth - 60,
			fontSize: 14,
			fontWeight: 'bold',
			fill: isDarkMode ? '#f1f5f9' : '#0f172a',
			splitByGrapheme: true
		});

		// File Size / Type Text
		const metaText = new TextClass(`${formatFileSize(media.sizeBytes)} • ${media.kind}`, {
			left: 48,
			top: cardHeight / 2 + 4,
			width: cardWidth - 60,
			fontSize: 11,
			fill: isDarkMode ? '#64748b' : '#94a3b8'
		});

		const group = new GroupClass([bg, icon, fileName, metaText], {
			left: clampBoardX(left, cardWidth),
			top: clampBoardY(top, cardHeight)
		});
		return group as FabricObjectLike;
	}

	async function createImageObjectFromMedia(
		media: BoardMediaContent,
		left: number,
		top: number,
		explicitWidth = 0,
		explicitHeight = 0
	): Promise<FabricObjectLike | null> {
		const ImageClass = getFabricClass('Image') ?? getFabricClass('FabricImage');
		if (!ImageClass || !browser) {
			return null;
		}
		try {
			const loadedImage = await loadBrowserImage(media.url);
			const object = new ImageClass(loadedImage, {
				left,
				top
			}) as FabricObjectLike;
			const rawWidth = Math.max(
				1,
				toNumber(
					(object as Record<string, unknown>).width,
					loadedImage.naturalWidth || loadedImage.width || 1
				)
			);
			const rawHeight = Math.max(
				1,
				toNumber(
					(object as Record<string, unknown>).height,
					loadedImage.naturalHeight || loadedImage.height || 1
				)
			);
			let targetWidth = explicitWidth;
			let targetHeight = explicitHeight;
			if (targetWidth <= 0 || targetHeight <= 0) {
				const maxWidth = Math.max(240, Math.min(getBoardCardWidth('media') + 80, rawWidth));
				const maxHeight = Math.max(200, Math.min(MAX_IMAGE_PREVIEW_HEIGHT, rawHeight));
				const fitScale = Math.min(maxWidth / rawWidth, maxHeight / rawHeight, 1);
				targetWidth = Math.max(MIN_SHAPE_WIDTH, rawWidth * fitScale);
				targetHeight = Math.max(MIN_SHAPE_HEIGHT, rawHeight * fitScale);
			}
			object.set?.({
				left: clampBoardX(left, targetWidth),
				top: clampBoardY(top, targetHeight),
				scaleX: targetWidth / rawWidth,
				scaleY: targetHeight / rawHeight
			});
			object.setCoords?.();
			return object;
		} catch {
			return null;
		}
	}

	function loadBrowserImage(url: string): Promise<HTMLImageElement> {
		return new Promise((resolve, reject) => {
			if (!browser) {
				reject(new Error('Browser-only image API unavailable'));
				return;
			}
			const image = new Image();
			image.crossOrigin = 'anonymous';
			image.onload = () => resolve(image);
			image.onerror = () => reject(new Error('Image load failed'));
			image.src = url;
		});
	}

	function safeHostFromURL(url: string) {
		try {
			return new URL(url).hostname;
		} catch {
			return '';
		}
	}

	function inferFileNameFromURL(rawURL: string) {
		try {
			const parsed = new URL(rawURL);
			const pathName = parsed.pathname || '';
			const rawSegment = pathName.split('/').pop() || '';
			if (!rawSegment) {
				return '';
			}
			const decoded = decodeURIComponent(rawSegment).trim();
			return decoded || '';
		} catch {
			return '';
		}
	}

	function formatFileSize(sizeBytes: number) {
		if (sizeBytes <= 0) {
			return '';
		}
		if (sizeBytes < 1024) {
			return `${sizeBytes} B`;
		}
		if (sizeBytes < 1024 * 1024) {
			return `${(sizeBytes / 1024).toFixed(1)} KB`;
		}
		return `${(sizeBytes / (1024 * 1024)).toFixed(1)} MB`;
	}

	function parseLinePoints(content: string, fallback: BoardElementWire) {
		if (content) {
			try {
				const parsed = JSON.parse(content) as Record<string, unknown>;
				const x1 = toNumber(parsed.x1, fallback.x);
				const y1 = toNumber(parsed.y1, fallback.y);
				const x2 = toNumber(parsed.x2, fallback.x + fallback.width);
				const y2 = toNumber(parsed.y2, fallback.y + fallback.height);
				return [x1, y1, x2, y2];
			} catch {
				// fall through to defaults
			}
		}
		return [fallback.x, fallback.y, fallback.x + fallback.width, fallback.y + fallback.height];
	}

	function findObjectByElementId(elementId: string): FabricObjectLike | null {
		if (!fabricCanvas) {
			return null;
		}
		const normalizedElementId = normalizeMessageID(elementId);
		if (!normalizedElementId) {
			return null;
		}
		const objects = fabricCanvas.getObjects?.() ?? [];
		for (const object of objects) {
			if (object === boardBoundsRect) {
				continue;
			}
			const candidateId = normalizeMessageID(
				toStringValue((object as Record<string, unknown>).elementId)
			);
			if (candidateId && candidateId === normalizedElementId) {
				return object as FabricObjectLike;
			}
		}
		return null;
	}

	function handleIncomingSocketPayload(rawPayload: unknown) {
		const boardErrorEvent = parseBoardErrorPayload(rawPayload);
		if (boardErrorEvent && boardErrorEvent.roomId === normalizedRoomId) {
			handleBoardErrorEvent(boardErrorEvent);
			return;
		}
		const envelope = parseBoardEnvelope(rawPayload);
		if (!envelope || envelope.roomId !== normalizedRoomId) {
			return;
		}
		if (envelope.type === 'board_cursor_move') {
			const cursorMove = parseBoardCursorMoveRecord(envelope.payload);
			if (!cursorMove) {
				return;
			}
			upsertRemoteCursor(cursorMove);
			return;
		}
		if (envelope.type === 'board_clear') {
			beginRemoteApply();
			try {
				clearBoardWithBounds(false);
				refreshBoardStats();
			} finally {
				endRemoteApply();
			}
			return;
		}
		if (envelope.type === 'board_element_add') {
			void applyIncomingAddPayload(envelope.payload);
			return;
		}
		if (envelope.type === 'board_element_move') {
			const movement = parseBoardMovementRecord(envelope.payload);
			if (!movement) {
				return;
			}
			const target = findObjectByElementId(movement.elementId);
			if (!target) {
				return;
			}
			beginRemoteApply();
			try {
				target.set?.({
					left: movement.x,
					top: movement.y,
					scaleX: movement.scaleX > 0 ? movement.scaleX : 1,
					scaleY: movement.scaleY > 0 ? movement.scaleY : 1
				});
				target.setCoords?.();
				if (movement.zIndex >= 0) {
					const objects = fabricCanvas.getObjects?.() ?? [];
					const minIndex = boardBoundsRect ? 1 : 0;
					const maxIndex = Math.max(minIndex, objects.length - 1);
					const targetIndex = Math.max(minIndex, Math.min(maxIndex, movement.zIndex + minIndex));
					fabricCanvas.moveTo?.(target as any, targetIndex);
				}
				fabricCanvas.requestRenderAll?.();
			} finally {
				endRemoteApply();
			}
			return;
		}
		if (envelope.type === 'board_element_delete') {
			const movement = parseBoardMovementRecord(envelope.payload);
			if (!movement) {
				return;
			}
			const target = findObjectByElementId(movement.elementId);
			if (!target) {
				return;
			}
			beginRemoteApply();
			try {
				fabricCanvas.remove(target as any);
				fabricCanvas.requestRenderAll?.();
				refreshBoardStats();
			} finally {
				endRemoteApply();
			}
		}
	}

	function parseBoardErrorPayload(value: unknown): {
		roomId: string;
		code: string;
		message: string;
		elementId: string;
	} | null {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return null;
		}
		const record = value as Record<string, unknown>;
		const type = toStringValue(record.type).trim().toLowerCase();
		if (type !== 'board_error') {
			return null;
		}
		const roomId = normalizeRoomIDValue(toStringValue(record.roomId ?? record.room_id));
		if (!roomId) {
			return null;
		}
		const payload =
			record.payload && typeof record.payload === 'object' && !Array.isArray(record.payload)
				? (record.payload as Record<string, unknown>)
				: {};
		return {
			roomId,
			code: toStringValue(payload.code).trim().toLowerCase(),
			message: toStringValue(payload.message).trim(),
			elementId: normalizeMessageID(toStringValue(payload.elementId ?? payload.element_id))
		};
	}

	function handleBoardErrorEvent(event: {
		roomId: string;
		code: string;
		message: string;
		elementId: string;
	}) {
		if (event.message) {
			boardError = event.message;
		}
		if (
			event.code === 'board_permission_denied' ||
			event.code === 'board_permission_check_failed'
		) {
			return;
		}
		const targetElementId = normalizeMessageID(event.elementId);
		if (!targetElementId) {
			return;
		}
		const existingObject = findObjectByElementId(targetElementId);
		if (existingObject && fabricCanvas) {
			fabricCanvas.remove(existingObject as any);
			fabricCanvas.discardActiveObject?.();
			fabricCanvas.requestRenderAll?.();
			refreshBoardStats();
		}
		pruneLocalActionsForElement(targetElementId);
		if (pendingInsertElementId && pendingInsertElementId === targetElementId) {
			pendingInsertElementId = '';
			pendingShapeKind = null;
			pendingShapeAnchorPoint = null;
			pendingShapePointerMoved = false;
		}
	}

	async function applyIncomingAdd(element: BoardElementWire) {
		beginRemoteApply();
		try {
			await addOrReplaceElementOnCanvas(element);
			fabricCanvas?.requestRenderAll?.();
			refreshBoardStats();
		} finally {
			endRemoteApply();
		}
	}

	async function applyIncomingAddPayload(rawElement: unknown) {
		const parsedElement = await parseBoardElementRecordDecrypted(rawElement);
		if (!parsedElement) {
			return;
		}
		await applyIncomingAdd(parsedElement);
	}

	function beginRemoteApply() {
		remoteApplyDepth += 1;
		isApplyingRemoteEvent = true;
	}

	function endRemoteApply() {
		remoteApplyDepth = Math.max(0, remoteApplyDepth - 1);
		isApplyingRemoteEvent = remoteApplyDepth > 0;
	}

	function parseBoardEnvelope(value: unknown): {
		type: BoardEventType;
		roomId: string;
		payload: unknown;
	} | null {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return null;
		}
		const record = value as Record<string, unknown>;
		const type = toStringValue(record.type).trim().toLowerCase() as BoardEventType;
		if (
			type !== 'board_draw_start' &&
			type !== 'board_cursor_move' &&
			type !== 'board_clear' &&
			type !== 'board_element_add' &&
			type !== 'board_element_move' &&
			type !== 'board_element_delete'
		) {
			return null;
		}
		const roomIdFromEnvelope = normalizeRoomIDValue(toStringValue(record.roomId ?? record.room_id));
		const payloadRecord = record.payload;
		const roomIdFromPayload =
			payloadRecord && typeof payloadRecord === 'object' && !Array.isArray(payloadRecord)
				? normalizeRoomIDValue(
						toStringValue(
							(payloadRecord as Record<string, unknown>).roomId ??
								(payloadRecord as Record<string, unknown>).room_id
						)
					)
				: '';
		const resolvedRoomId = roomIdFromEnvelope || roomIdFromPayload;
		if (!resolvedRoomId) {
			return null;
		}
		const resolvedPayload =
			type === 'board_cursor_move' ||
			type === 'board_clear' ||
			type === 'board_element_add' ||
			type === 'board_element_move' ||
			type === 'board_element_delete'
				? payloadRecord
				: record;
		return {
			type,
			roomId: resolvedRoomId,
			payload: resolvedPayload
		};
	}

	function parseBoardMovementRecord(value: unknown): {
		elementId: string;
		x: number;
		y: number;
		scaleX: number;
		scaleY: number;
		zIndex: number;
	} | null {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return null;
		}
		const record = value as Record<string, unknown>;
		const nestedPayload =
			record.payload && typeof record.payload === 'object' && !Array.isArray(record.payload)
				? (record.payload as Record<string, unknown>)
				: null;
		const source = nestedPayload ?? record;
		const elementId = normalizeMessageID(
			toStringValue(source.elementId ?? source.element_id ?? source.id)
		);
		if (!elementId) {
			return null;
		}
		const hasZIndex =
			Object.prototype.hasOwnProperty.call(source, 'zIndex') ||
			Object.prototype.hasOwnProperty.call(source, 'z_index');
		return {
			elementId,
			x: toNumber(source.x, 0),
			y: toNumber(source.y, 0),
			scaleX: toNumber(source.scaleX ?? source.scale_x, 1),
			scaleY: toNumber(source.scaleY ?? source.scale_y, 1),
			zIndex: hasZIndex ? toInt(source.zIndex ?? source.z_index) : -1
		};
	}

	function parseBoardCursorMoveRecord(value: unknown): {
		userId: string;
		name: string;
		x: number;
		y: number;
	} | null {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return null;
		}
		const record = value as Record<string, unknown>;
		const nestedPayload =
			record.payload && typeof record.payload === 'object' && !Array.isArray(record.payload)
				? (record.payload as Record<string, unknown>)
				: null;
		const source = nestedPayload ?? record;
		const userId = normalizeIdentifier(toStringValue(source.userId ?? source.user_id));
		if (!userId) {
			return null;
		}
		return {
			userId,
			name: toStringValue(source.name).trim() || 'Guest',
			x: toNumber(source.x, 0),
			y: toNumber(source.y, 0)
		};
	}

	function removeBoardObject(object: FabricObjectLike, emitDelete: boolean) {
		if (!fabricCanvas || object === boardBoundsRect) {
			return;
		}
		const elementId = normalizeMessageID(
			toStringValue((object as Record<string, unknown>).elementId)
		);
		const wasPendingInsert = Boolean(elementId && elementId === pendingInsertElementId);
		if (emitDelete && !wasPendingInsert && !canMutateBoardObject(object)) {
			return;
		}
		const beforeElement = boardObjectToElement(object);
		fabricCanvas.remove(object as any);
		fabricCanvas.discardActiveObject?.();
		fabricCanvas.requestRenderAll?.();
		if (wasPendingInsert) {
			pendingInsertElementId = '';
			pendingShapeKind = null;
			pendingShapeAnchorPoint = null;
			pendingShapePointerMoved = false;
		}
		if (emitDelete && elementId && !wasPendingInsert) {
			emitBoardElementDelete(elementId);
			if (!isApplyingLocalAction && beforeElement) {
				recordLocalAction({
					kind: 'delete',
					elementId,
					before: cloneBoardElement(beforeElement)
				});
			}
		}
		if (elementId) {
			discardPendingTransformForElement(elementId);
		}
		captureHistorySnapshot();
	}

	function cloneBoardElement(element: BoardElementWire): BoardElementWire {
		return {
			elementId: element.elementId,
			elementType: element.elementType,
			x: element.x,
			y: element.y,
			width: element.width,
			height: element.height,
			content: element.content,
			zIndex: element.zIndex,
			createdByUserId: element.createdByUserId,
			createdByName: element.createdByName,
			createdAt: element.createdAt
		};
	}

	function elementsEquivalent(left: BoardElementWire, right: BoardElementWire) {
		return (
			left.elementId === right.elementId &&
			left.elementType === right.elementType &&
			Math.abs(left.x - right.x) < 0.01 &&
			Math.abs(left.y - right.y) < 0.01 &&
			Math.abs(left.width - right.width) < 0.01 &&
			Math.abs(left.height - right.height) < 0.01 &&
			left.zIndex === right.zIndex &&
			left.createdByUserId === right.createdByUserId &&
			left.createdByName === right.createdByName &&
			left.content === right.content
		);
	}

	function getLocalActionHistoryStorageKey(targetRoomId = normalizedRoomId) {
		const normalizedTargetRoomId = normalizeRoomIDValue(targetRoomId);
		if (!normalizedTargetRoomId) {
			return '';
		}
		const actorId = normalizedCurrentUserID || 'guest';
		return `${LOCAL_ACTION_STORAGE_PREFIX}:${normalizedTargetRoomId}:${actorId}`;
	}

	function parsePersistedLocalAction(value: unknown): LocalBoardAction | null {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return null;
		}
		const record = value as Record<string, unknown>;
		const kind = toStringValue(record.kind).trim().toLowerCase();
		if (kind !== 'add' && kind !== 'move' && kind !== 'delete') {
			return null;
		}
		const elementId = normalizeMessageID(toStringValue(record.elementId ?? record.element_id));
		if (!elementId) {
			return null;
		}
		const before = parseBoardElementRecord(record.before) ?? undefined;
		const after = parseBoardElementRecord(record.after) ?? undefined;
		if (kind === 'add' && !after) {
			return null;
		}
		if (kind === 'delete' && !before) {
			return null;
		}
		if (kind === 'move' && !before && !after) {
			return null;
		}
		return {
			kind,
			elementId,
			before,
			after
		};
	}

	function persistLocalActionHistory(targetRoomId = normalizedRoomId) {
		if (!browser || typeof window === 'undefined') {
			return;
		}
		const storageKey = getLocalActionHistoryStorageKey(targetRoomId);
		if (!storageKey) {
			return;
		}
		try {
			if (localUndoStack.length === 0 && localRedoStack.length === 0) {
				window.sessionStorage.removeItem(storageKey);
				return;
			}
			window.sessionStorage.setItem(
				storageKey,
				JSON.stringify({
					undo: localUndoStack.slice(-LOCAL_ACTION_LIMIT),
					redo: localRedoStack.slice(-LOCAL_ACTION_LIMIT)
				})
			);
		} catch {
			// Ignore local storage quota or availability failures.
		}
	}

	function restoreLocalActionHistory(targetRoomId = normalizedRoomId) {
		if (!browser || typeof window === 'undefined') {
			return;
		}
		const storageKey = getLocalActionHistoryStorageKey(targetRoomId);
		if (!storageKey) {
			localUndoStack = [];
			localRedoStack = [];
			return;
		}
		const raw = window.sessionStorage.getItem(storageKey);
		if (!raw) {
			localUndoStack = [];
			localRedoStack = [];
			return;
		}
		try {
			const parsed = JSON.parse(raw) as Record<string, unknown>;
			const undoRaw = Array.isArray(parsed.undo) ? parsed.undo : [];
			const redoRaw = Array.isArray(parsed.redo) ? parsed.redo : [];
			localUndoStack = undoRaw
				.map((entry) => parsePersistedLocalAction(entry))
				.filter((entry): entry is LocalBoardAction => Boolean(entry))
				.slice(-LOCAL_ACTION_LIMIT);
			localRedoStack = redoRaw
				.map((entry) => parsePersistedLocalAction(entry))
				.filter((entry): entry is LocalBoardAction => Boolean(entry))
				.slice(-LOCAL_ACTION_LIMIT);
			persistLocalActionHistory(targetRoomId);
		} catch {
			localUndoStack = [];
			localRedoStack = [];
			window.sessionStorage.removeItem(storageKey);
		}
	}

	function recordLocalAction(action: LocalBoardAction) {
		if (isApplyingRemoteEvent || isRestoringHistory || isApplyingLocalAction) {
			return;
		}
		localUndoStack = [...localUndoStack, action].slice(-LOCAL_ACTION_LIMIT);
		localRedoStack = [];
		persistLocalActionHistory();
	}

	function discardPendingTransformForElement(elementId: string) {
		if (!elementId) {
			return;
		}
		pendingTransformSnapshotByElementId.delete(elementId);
	}

	function pruneLocalActionsForElement(elementId: string) {
		if (!elementId) {
			return;
		}
		localUndoStack = localUndoStack.filter((entry) => entry.elementId !== elementId);
		localRedoStack = localRedoStack.filter((entry) => entry.elementId !== elementId);
		discardPendingTransformForElement(elementId);
		persistLocalActionHistory();
	}

	function serializeBoardSnapshot() {
		if (!fabricCanvas) {
			return '';
		}
		return JSON.stringify(
			fabricCanvas.toJSON?.([
				'elementId',
				'elementType',
				'content',
				'createdAt',
				'createdByUserId',
				'createdByName'
			]) ?? {}
		);
	}

	function refreshBoardStats(serializedSnapshot = '') {
		if (!fabricCanvas) {
			boardElementCount = 0;
			boardApproxBytes = 0;
			latestSerializedBoardSnapshot = '';
			latestSerializedBoardSnapshotBytes = 0;
			return;
		}
		const objects = fabricCanvas.getObjects?.() ?? [];
		boardElementCount = objects.filter(
			(object: unknown) => object && object !== boardBoundsRect
		).length;
		const serialized = serializedSnapshot || serializeBoardSnapshot();
		boardApproxBytes = serialized ? UTF8_ENCODER.encode(serialized).length : 0;
		latestSerializedBoardSnapshot = serialized;
		latestSerializedBoardSnapshotBytes = serialized ? new Blob([serialized]).size : 0;
	}

	function captureHistorySnapshot(force = false) {
		if (!fabricCanvas || isApplyingRemoteEvent || isRestoringHistory) {
			return;
		}
		const serialized = serializeBoardSnapshot();
		if (!serialized) {
			return;
		}
		refreshBoardStats(serialized);
		if (!force && historyCursor >= 0 && historyStack[historyCursor] === serialized) {
			return;
		}
		historyStack = historyStack.slice(0, historyCursor + 1);
		historyStack.push(serialized);
		if (historyStack.length > HISTORY_LIMIT) {
			historyStack = historyStack.slice(historyStack.length - HISTORY_LIMIT);
		}
		historyCursor = historyStack.length - 1;
	}

	async function undo() {
		if (
			!fabricCanvas ||
			localUndoStack.length === 0 ||
			isApplyingLocalAction ||
			!canModerateBoardActions
		) {
			return;
		}
		const action = localUndoStack[localUndoStack.length - 1];
		localUndoStack = localUndoStack.slice(0, -1);
		isApplyingLocalAction = true;
		try {
			await applyLocalAction(action, 'undo');
			localRedoStack = [...localRedoStack, action].slice(-LOCAL_ACTION_LIMIT);
			persistLocalActionHistory();
			captureHistorySnapshot();
		} finally {
			isApplyingLocalAction = false;
		}
	}

	async function redo() {
		if (
			!fabricCanvas ||
			localRedoStack.length === 0 ||
			isApplyingLocalAction ||
			!canModerateBoardActions
		) {
			return;
		}
		const action = localRedoStack[localRedoStack.length - 1];
		localRedoStack = localRedoStack.slice(0, -1);
		isApplyingLocalAction = true;
		try {
			await applyLocalAction(action, 'redo');
			localUndoStack = [...localUndoStack, action].slice(-LOCAL_ACTION_LIMIT);
			persistLocalActionHistory();
			captureHistorySnapshot();
		} finally {
			isApplyingLocalAction = false;
		}
	}

	async function applyLocalAction(action: LocalBoardAction, direction: 'undo' | 'redo') {
		if (!fabricCanvas || !action || !action.elementId) {
			return;
		}
		if (action.kind === 'add') {
			if (direction === 'undo') {
				const object = findObjectByElementId(action.elementId);
				if (object) {
					removeBoardObject(object, true);
				} else {
					emitBoardElementDelete(action.elementId);
				}
				return;
			}
			if (action.after) {
				await addOrReplaceElementOnCanvas(action.after);
				const nextObject = findObjectByElementId(action.elementId);
				if (nextObject) {
					emitBoardElementAdd(nextObject);
				}
			}
			return;
		}
		if (action.kind === 'delete') {
			if (direction === 'undo') {
				if (action.before) {
					await addOrReplaceElementOnCanvas(action.before);
					const restoredObject = findObjectByElementId(action.elementId);
					if (restoredObject) {
						emitBoardElementAdd(restoredObject);
					}
				}
				return;
			}
			const object = findObjectByElementId(action.elementId);
			if (object) {
				removeBoardObject(object, true);
			} else {
				emitBoardElementDelete(action.elementId);
			}
			return;
		}
		if (action.kind === 'move') {
			const targetState = direction === 'undo' ? action.before : action.after;
			if (!targetState) {
				return;
			}
			await addOrReplaceElementOnCanvas(targetState);
			const movedObject = findObjectByElementId(action.elementId);
			if (!movedObject) {
				return;
			}
			emitBoardElementMove(movedObject);
		}
	}

	function onBoardPointerDown(event: PointerEvent) {
		if (event.button !== 0 || !boardContainerEl || !canvasEl || !fabricCanvas) {
			return;
		}
		const candidateTarget = event.target as Node | null;
		if (candidateTarget && boardContainerEl && !boardContainerEl.contains(candidateTarget)) {
			return;
		}
		const boardPoint = getBoardPointFromClientPosition(event.clientX, event.clientY);
		contextMenuPoint = boardPoint;
		if (!canEdit) {
			return;
		}
		if (activeTool === 'duster') {
			if (!canManageAllBoardElements) {
				return;
			}
			event.preventDefault();
			event.stopPropagation();
			contextMenuOpen = false;
			showInsertMenu = false;
			showWidthMenu = false;
			showColorMenu = false;
			showBoardDetails = false;
			messagePickerOpen = false;
			pendingTapGesture = null;
			moveDusterToBoardX(boardPoint.x);
			dusterIsDragging = true;
			dusterPointerId = event.pointerId;
			try {
				boardContainerEl.setPointerCapture?.(event.pointerId);
			} catch {
				// Ignore capture failure; window pointer listeners still keep the sweep active.
			}
			clearElementsTouchingDusterStripe();
			return;
		}

		if (!event.altKey) {
			resetSelectionCycleState();
		}
		if (
			activeTool === 'select' &&
			event.altKey &&
			!pendingShapeKind &&
			!pendingInsertElementId &&
			cycleSelectionFromPointer(event)
		) {
			event.preventDefault();
			contextMenuOpen = false;
			return;
		}

		if (pendingShapeKind && !pendingInsertElementId) {
			event.preventDefault();
			contextMenuOpen = false;
			placePendingShapeAt(boardPoint);
			return;
		}

		if (pendingInsertElementId) {
			const pendingObject = getPendingInsertObject();
			if (!pendingObject) {
				pendingInsertElementId = '';
				pendingShapeKind = null;
				pendingShapeAnchorPoint = null;
				pendingShapePointerMoved = false;
				return;
			}
			event.preventDefault();
			commitPendingShapeInsert();
			return;
		}

		const target = tryResolveFabricTargetFromPointer(event);
		pendingTapGesture = {
			startX: event.clientX,
			startY: event.clientY,
			moved: false,
			emptyTarget: !target || target === boardBoundsRect,
			boardPoint
		};
	}

	function tryResolveFabricTargetFromPointer(event: PointerEvent): FabricObjectLike | null {
		if (!fabricCanvas) {
			return null;
		}
		try {
			const target = fabricCanvas.findTarget?.(event as unknown as MouseEvent, false);
			return (target as FabricObjectLike | null) ?? null;
		} catch {
			return null;
		}
	}

	function resetSelectionCycleState() {
		selectionCycleKey = '';
		selectionCycleCursor = 0;
	}

	function getCycleElementID(object: FabricObjectLike, fallbackIndex = 0) {
		const normalizedElementID = normalizeMessageID(
			toStringValue((object as Record<string, unknown>).elementId)
		);
		if (normalizedElementID) {
			return normalizedElementID;
		}
		return `anon_${fallbackIndex}`;
	}

	function collectPointerTargetStack(event: PointerEvent) {
		if (!fabricCanvas) {
			return [] as FabricObjectLike[];
		}
		const mutedTargets: Array<{
			object: FabricObjectLike;
			evented: unknown;
		}> = [];
		const seenIDs = new Set<string>();
		const resolvedTargets: FabricObjectLike[] = [];

		try {
			for (let depth = 0; depth < 28; depth += 1) {
				const target = tryResolveFabricTargetFromPointer(event);
				if (!target || target === boardBoundsRect) {
					break;
				}
				const targetID = getCycleElementID(target, depth);
				if (seenIDs.has(targetID)) {
					break;
				}
				seenIDs.add(targetID);
				if (canMutateBoardObject(target)) {
					resolvedTargets.push(target);
				}
				mutedTargets.push({
					object: target,
					evented: (target as Record<string, unknown>).evented
				});
				target.set?.({
					evented: false
				});
				target.setCoords?.();
			}
		} finally {
			for (const muted of mutedTargets) {
				muted.object.set?.({
					evented: muted.evented
				});
				muted.object.setCoords?.();
			}
		}

		return resolvedTargets;
	}

	function cycleSelectionFromPointer(event: PointerEvent) {
		if (!fabricCanvas) {
			return false;
		}
		const targetStack = collectPointerTargetStack(event);
		if (targetStack.length === 0) {
			resetSelectionCycleState();
			return false;
		}
		const roundedX = Math.round(event.clientX / 8);
		const roundedY = Math.round(event.clientY / 8);
		const stackKey = targetStack.map((target, index) => getCycleElementID(target, index)).join('|');
		const cycleKey = `${roundedX}:${roundedY}:${stackKey}`;
		if (selectionCycleKey !== cycleKey) {
			selectionCycleKey = cycleKey;
			selectionCycleCursor = 0;
		} else {
			selectionCycleCursor = (selectionCycleCursor + 1) % targetStack.length;
		}
		const targetObject = targetStack[selectionCycleCursor];
		if (!targetObject) {
			return false;
		}
		fabricCanvas.setActiveObject?.(targetObject as any);
		fabricCanvas.requestRenderAll?.();
		updateSelectionControlsPosition();
		return true;
	}

	function onBoardPointerMove(event: PointerEvent) {
		if (activeTool === 'duster') {
			if (!canManageAllBoardElements) {
				return;
			}
			const boardPoint = getBoardPointFromClientPosition(event.clientX, event.clientY);
			moveDusterToBoardX(boardPoint.x);
			if (dusterIsDragging && (dusterPointerId === null || event.pointerId === dusterPointerId)) {
				event.preventDefault();
				clearElementsTouchingDusterStripe();
			}
			return;
		}
		if (pendingInsertElementId) {
			updatePendingShapeFromPointer(event);
			return;
		}
		if (!pendingTapGesture) {
			return;
		}
		const deltaX = Math.abs(event.clientX - pendingTapGesture.startX);
		const deltaY = Math.abs(event.clientY - pendingTapGesture.startY);
		if (deltaX >= TAP_MOVE_TOLERANCE || deltaY >= TAP_MOVE_TOLERANCE) {
			pendingTapGesture.moved = true;
		}
	}

	function onBoardPointerUp(event: PointerEvent) {
		if (activeTool === 'duster') {
			if (dusterIsDragging && (dusterPointerId === null || event.pointerId === dusterPointerId)) {
				event.preventDefault();
				stopDusterDrag();
			}
			return;
		}
		if (!pendingTapGesture) {
			return;
		}
		const gesture = pendingTapGesture;
		pendingTapGesture = null;
		if (gesture.moved || !gesture.emptyTarget || !canEdit || isInsertOperationActive) {
			return;
		}
		if (fabricCanvas?.getActiveObject?.()) {
			return;
		}
		const now = Date.now();
		if (now - lastEmptyTapAt <= DOUBLE_TAP_MS) {
			lastEmptyTapAt = 0;
			openContextMenuAt(event.clientX, event.clientY, gesture.boardPoint);
			return;
		}
		lastEmptyTapAt = now;
	}

	function onBoardPointerCancel() {
		pendingTapGesture = null;
		stopDusterDrag();
	}

	function openContextMenuAt(
		clientX: number,
		clientY: number,
		boardPoint: { x: number; y: number }
	) {
		if (!boardContainerEl) {
			return;
		}
		contextMenuPoint = boardPoint;
		const rect = boardContainerEl.getBoundingClientRect();
		const menuWidth = 210;
		const menuHeight = 92;
		const offsetX = clientX - rect.left;
		const offsetY = clientY - rect.top;
		contextMenuX = Math.max(0, Math.min(rect.width - menuWidth, offsetX));
		contextMenuY = Math.max(0, Math.min(rect.height - menuHeight, offsetY));
		contextMenuOpen = true;
		showInsertMenu = false;
	}

	function openMediaPicker() {
		contextMenuOpen = false;
		mediaInputEl?.click();
	}

	async function onMediaFileSelected(event: Event) {
		if (!canEdit || !normalizedRoomId) {
			return;
		}
		const input = event.currentTarget as HTMLInputElement | null;
		const file = input?.files?.[0] ?? null;
		if (!file) {
			return;
		}
		if (isEphemeralRoom && file.type.startsWith('image/')) {
			dispatch('toastError', { message: 'Image uploads are disabled in ephemeral rooms.' });
			if (input) {
				input.value = '';
			}
			return;
		}
		isUploadingMedia = true;
		boardError = '';
		try {
			let fileForUpload = file;
			if (file.type.startsWith('image/')) {
				const options = {
					maxSizeMB: 1,
					maxWidthOrHeight: 1200,
					useWebWorker: true
				};
				const compressed = await imageCompression(file, options);
				fileForUpload =
					compressed instanceof File
						? compressed
						: new File([compressed], file.name, {
								type: compressed.type || file.type || 'image/jpeg',
								lastModified: file.lastModified
							});
			}
			const uploaded = await uploadToR2(fileForUpload, normalizedRoomId);
			const mediaPayload: BoardMediaContent = {
				url: uploaded.fileUrl,
				name: fileForUpload.name || file.name || 'attachment',
				kind: inferMediaMessageType(fileForUpload),
				mimeType: fileForUpload.type || 'application/octet-stream',
				sizeBytes: fileForUpload.size,
				caption: '',
				senderName: '',
				sentAt: 0
			};
			await insertMediaObject(mediaPayload, contextMenuPoint);
		} catch (error) {
			boardError = error instanceof Error ? error.message : 'Failed to upload media';
		} finally {
			isUploadingMedia = false;
			if (input) {
				input.value = '';
			}
		}
	}

	function openMessagePicker() {
		contextMenuOpen = false;
		messagePickerOpen = true;
	}

	function insertRoomMessage(message: ChatMessage) {
		messagePickerOpen = false;
		void insertRichMessageObject(message, contextMenuPoint);
	}

	async function insertRichMessageObject(message: ChatMessage, point: { x: number; y: number }) {
		if (!fabricCanvas || !canEdit) {
			return;
		}
		const object = await createRichMessageObject(message, point.x, point.y);
		if (!object) {
			return;
		}
		const richPayload = createRichMessageCardPayload(message);
		ensureObjectIdentity(object, 'message');
		object.set?.({
			content: JSON.stringify(richPayload)
		});
		fabricCanvas.add(object);
		applyObjectPermission(object);
		fabricCanvas.setActiveObject?.(object);
		fabricCanvas.requestRenderAll?.();
		emitBoardElementAdd(object);
		const addedElement = boardObjectToElement(object);
		if (addedElement && !isApplyingLocalAction) {
			recordLocalAction({
				kind: 'add',
				elementId: addedElement.elementId,
				after: cloneBoardElement(addedElement)
			});
		}
		captureHistorySnapshot();
	}

	async function insertMediaObject(media: BoardMediaContent, point: { x: number; y: number }) {
		if (!fabricCanvas || !canEdit) {
			return;
		}
		let object: FabricObjectLike | null = null;
		if (media.kind === 'image') {
			object = await createImageObjectFromMedia(media, point.x, point.y);
		}
		if (!object) {
			const width = getBoardCardWidth('media');
			const height =
				media.kind === 'video'
					? MAX_VIDEO_PREVIEW_HEIGHT * 0.45
					: media.kind === 'audio'
						? 140
						: 160;
			object = createMediaCardObject(media, point.x, point.y, width, height);
		}
		if (!object) {
			boardError = 'Unable to render selected media on board';
			return;
		}
		ensureObjectIdentity(object, media.kind === 'image' ? 'image' : media.kind);
		object.set?.({
			content: JSON.stringify(media)
		});
		fabricCanvas.add(object);
		applyObjectPermission(object);
		fabricCanvas.setActiveObject?.(object);
		fabricCanvas.requestRenderAll?.();
		emitBoardElementAdd(object);
		const addedElement = boardObjectToElement(object);
		if (addedElement && !isApplyingLocalAction) {
			recordLocalAction({
				kind: 'add',
				elementId: addedElement.elementId,
				after: cloneBoardElement(addedElement)
			});
		}
		captureHistorySnapshot();
	}

	function insertMessageLikeObject(content: string, point: { x: number; y: number }) {
		if (!fabricCanvas || !canEdit) {
			return;
		}
		const object = createMessageCardObject(content, point.x, point.y);
		if (!object) {
			return;
		}
		ensureObjectIdentity(object, 'message');
		object.set?.({
			content
		});
		fabricCanvas.add(object);
		applyObjectPermission(object);
		fabricCanvas.setActiveObject?.(object);
		fabricCanvas.requestRenderAll?.();
		emitBoardElementAdd(object);
		const addedElement = boardObjectToElement(object);
		if (addedElement && !isApplyingLocalAction) {
			recordLocalAction({
				kind: 'add',
				elementId: addedElement.elementId,
				after: cloneBoardElement(addedElement)
			});
		}
		captureHistorySnapshot();
	}

	function buildMessageSearchText(message: ChatMessage) {
		return [
			toStringValue(message.senderName),
			formatBoardMessageDateTime(message.createdAt),
			extractMessageSnippet(message),
			toStringValue(message.fileName),
			toStringValue(message.mediaUrl)
		]
			.join(' ')
			.toLowerCase();
	}

	function buildBoardMediaPayloadFromMessage(message: ChatMessage): BoardMediaContent | null {
		const normalizedType = toStringValue(message.type).trim().toLowerCase();
		if (
			normalizedType !== 'image' &&
			normalizedType !== 'video' &&
			normalizedType !== 'audio' &&
			normalizedType !== 'file'
		) {
			return null;
		}
		const mediaURL =
			toStringValue(message.mediaUrl).trim() ||
			(toStringValue(message.content).trim().startsWith('http')
				? toStringValue(message.content).trim()
				: '');
		if (!mediaURL) {
			return null;
		}
		const rawContent = toStringValue(message.content).trim();
		const caption = rawContent && rawContent !== mediaURL ? rawContent : '';
		const senderName = toStringValue(message.senderName).trim() || 'Guest';
		const sentAt =
			Number.isFinite(message.createdAt) && message.createdAt > 0 ? message.createdAt : 0;
		return {
			url: mediaURL,
			name:
				toStringValue(message.fileName).trim() ||
				inferFileNameFromURL(mediaURL) ||
				`${resolveMessageTypeLabel(message)} attachment`,
			kind: normalizeMediaKind(normalizedType),
			mimeType: toStringValue(message.mediaType).trim(),
			sizeBytes: 0,
			caption,
			senderName,
			sentAt
		};
	}

	function formatBoardMessageDateTime(timestamp: number) {
		const safe = Number.isFinite(timestamp) && timestamp > 0 ? timestamp : Date.now();
		return new Date(safe).toLocaleString([], {
			year: 'numeric',
			month: 'short',
			day: '2-digit',
			hour: '2-digit',
			minute: '2-digit'
		});
	}

	function messageAvatarInitial(name: string) {
		const trimmed = toStringValue(name).trim();
		return trimmed ? trimmed.slice(0, 1).toUpperCase() : 'G';
	}

	function resolveMessagePreviewImageURL(message: ChatMessage) {
		const mediaURL = toStringValue(message.mediaUrl).trim();
		if (!mediaURL) {
			return '';
		}
		const messageType = toStringValue(message.type).trim().toLowerCase();
		const mediaType = toStringValue(message.mediaType).trim().toLowerCase();
		if (messageType === 'image' || mediaType.startsWith('image/')) {
			return mediaURL;
		}
		return /\.(png|jpe?g|gif|webp|avif|bmp|svg)(\?|#|$)/i.test(mediaURL) ? mediaURL : '';
	}

	function truncatePickerSnippet(value: string, max = 220) {
		if (value.length <= max) {
			return value;
		}
		return `${value.slice(0, max - 3)}...`;
	}

	function resolveMessageTypeLabel(message: ChatMessage) {
		const normalizedType = toStringValue(message.type).trim().toLowerCase();
		if (normalizedType === 'task') {
			return 'Task';
		}
		if (normalizedType === 'image') {
			return 'Image';
		}
		if (normalizedType === 'video') {
			return 'Video';
		}
		if (normalizedType === 'audio') {
			return 'Audio';
		}
		if (normalizedType === 'file') {
			return 'File';
		}
		if (normalizedType === 'deleted') {
			return 'Deleted';
		}
		return 'Message';
	}

	function extractMessageSnippet(message: ChatMessage) {
		if (!message) {
			return '';
		}

		const normalizedType = toStringValue(message.type).trim().toLowerCase();
		if (normalizedType === 'task') {
			const parsedTask = parseTaskMessagePayload(toStringValue(message.content));
			if (!parsedTask) {
				return 'Task';
			}
			const taskCount = parsedTask.tasks.length;
			if (taskCount <= 0) {
				return truncatePickerSnippet(`Task: ${parsedTask.title}`);
			}
			return truncatePickerSnippet(`Task: ${parsedTask.title} (${taskCount})`);
		}

		const mediaURL = toStringValue(message.mediaUrl).trim();
		if (
			normalizedType === 'image' ||
			normalizedType === 'video' ||
			normalizedType === 'audio' ||
			normalizedType === 'file'
		) {
			const fileName = toStringValue(message.fileName).trim();
			const caption = toStringValue(message.content).trim();
			const preferredText = fileName || (caption && caption !== mediaURL ? caption : mediaURL);
			return truncatePickerSnippet(
				`${resolveMessageTypeLabel(message)}: ${preferredText || 'Attachment'}`
			);
		}

		if (normalizedType === 'deleted') {
			return 'This message was deleted';
		}

		const text = toStringValue(message.content).trim();
		if (text) {
			return truncatePickerSnippet(text.replace(/\s+/g, ' '));
		}
		if (mediaURL) {
			return truncatePickerSnippet(mediaURL);
		}
		return `(message ${normalizeMessageID(message.id) || 'unknown'})`;
	}

	function buildTaskMessageCardBody(message: ChatMessage) {
		const parsedTask = parseTaskMessagePayload(toStringValue(message.content));
		if (!parsedTask) {
			return 'Task';
		}
		const lines = [`Task: ${parsedTask.title || 'Task'}`];
		const visibleTasks = parsedTask.tasks.slice(0, 8);
		for (const task of visibleTasks) {
			lines.push(`${task.completed ? '✓' : '○'} ${task.text}`);
		}
		const remainingCount = Math.max(0, parsedTask.tasks.length - visibleTasks.length);
		if (remainingCount > 0) {
			lines.push(`+${remainingCount} more task(s)`);
		}
		return lines.join('\n');
	}

	function buildMessageCardBody(message: ChatMessage) {
		const normalizedType = toStringValue(message.type).trim().toLowerCase();
		if (normalizedType === 'task') {
			return buildTaskMessageCardBody(message);
		}

		const content = toStringValue(message.content).trim();
		const mediaURL = toStringValue(message.mediaUrl).trim();

		if (
			normalizedType === 'image' ||
			normalizedType === 'video' ||
			normalizedType === 'audio' ||
			normalizedType === 'file'
		) {
			const lines = [`${resolveMessageTypeLabel(message)} attachment`];
			if (content && content !== mediaURL) {
				lines.push(content);
			}
			return lines.join('\n');
		}

		if (normalizedType === 'deleted') {
			return 'This message was deleted';
		}
		if (content) {
			return content;
		}
		if (mediaURL) {
			return mediaURL;
		}
		return 'Message';
	}

	function buildMessageCardText(message: ChatMessage) {
		const author = toStringValue(message.senderName).trim() || 'Guest';
		const sentAt = formatBoardMessageDateTime(message.createdAt);
		const replySnippet = toStringValue(message.replyToSnippet).trim();
		const body = buildMessageCardBody(message);
		const lines = ['Message Card', `From: ${author}`, `Sent: ${sentAt}`];
		if (replySnippet) {
			lines.push(`Reply: ${replySnippet}`);
		}
		lines.push('', body);
		return lines.join('\n').trim();
	}

	function formatStorageBytes(value: number) {
		if (!Number.isFinite(value) || value <= 0) {
			return '0 B';
		}
		if (value < 1024) {
			return `${Math.round(value)} B`;
		}
		if (value < 1024 * 1024) {
			return `${(value / 1024).toFixed(1)} KB`;
		}
		return `${(value / (1024 * 1024)).toFixed(2)} MB`;
	}

	function formatUsagePercent(value: number) {
		if (!Number.isFinite(value) || value <= 0) {
			return '0.0%';
		}
		return `${Math.min(100, value).toFixed(1)}%`;
	}

	function toNumber(value: unknown, fallback: number) {
		if (typeof value === 'number' && Number.isFinite(value)) {
			return value;
		}
		const parsed = Number(value);
		return Number.isFinite(parsed) ? parsed : fallback;
	}
</script>

	<section class="board-root">
		<div class="board-toolbar" bind:this={boardToolbarEl}>
			{#if openToolbarHintText}
				<div class="toolbar-open-hint" role="status" aria-live="polite">{openToolbarHintText}</div>
			{/if}
			<button
				type="button"
				class="tool-icon-button board-close-button"
				on:click={closeBoardView}
				title="Close board"
				aria-label="Close board"
			>
				<span aria-hidden="true">×</span>
			</button>
			<div class="toolbar-primary-group" bind:this={toolbarPrimaryEl}>
				<button
					type="button"
					class="tool-icon-button"
					class:active={activeTool === 'draw'}
					on:click={() => toggleToolMode('draw')}
				title="Free draw"
			>
				<svg class="tool-icon" viewBox="0 0 24 24">
					<path
						d="M4 16.8V20h3.2l9.4-9.4-3.2-3.2L4 16.8Zm14.7-8.7a.9.9 0 0 0 0-1.3l-1.5-1.5a.9.9 0 0 0-1.3 0l-1.2 1.2 3.2 3.2 1.2-1.2Z"
					/>
				</svg>
			</button>
			<button
				type="button"
				class="tool-icon-button"
				on:click={insertTextBox}
				title="Insert text box"
				aria-label="Insert text box"
				disabled={!canEdit}
			>
				<svg class="tool-icon" viewBox="0 0 24 24" aria-hidden="true">
					<path d="M5 6h14v2H13v10h-2V8H5z" />
				</svg>
			</button>
			<div class="color-menu-wrap" bind:this={colorMenuWrapEl}>
				<button
					type="button"
					class="color-menu-toggle"
					on:click={toggleColorMenu}
					aria-haspopup="true"
					aria-expanded={showColorMenu}
					title={showColorMenu ? undefined : 'Ink color'}
					disabled={!canEdit}
				>
					<span class="color-swatch" style={`background:${boardInkColor};`}></span>
					<span class="color-label">Color</span>
				</button>
				{#if showColorMenu}
					<div class="color-menu-popover">
						<div class="color-preset-grid" role="listbox" aria-label="Color presets">
							{#each BOARD_COLOR_PRESETS as presetColor}
								<button
									type="button"
									class="color-preset-button"
									class:active={boardInkColor === presetColor}
									style={`--swatch:${presetColor};`}
									on:click={() => setBoardInkColor(presetColor)}
									aria-label={`Set ink color ${presetColor}`}
								></button>
							{/each}
						</div>
						<label class="color-picker-row">
							<span>Custom</span>
							<input
								type="color"
								value={boardInkColor}
								on:input={(event) => {
									const input = event.currentTarget as HTMLInputElement | null;
									if (!input) return;
									setBoardInkColor(input.value);
								}}
							/>
						</label>
					</div>
				{/if}
			</div>
			<button
				type="button"
				class="tool-icon-button"
				on:click={undo}
				disabled={!canModerateBoardActions || !canUndoLocalAction}
				title="Undo"
			>
				<svg
					class="tool-icon"
					viewBox="0 0 24 24"
					fill="none"
					stroke="currentColor"
					stroke-width="2"
				>
					<path d="M3 7v6h6" />
					<path d="M3 13a9 9 0 0 1 15-6.7L21 9" />
				</svg>
			</button>
			{#if shouldUseToolbarMenu}
				<button
					type="button"
					class="tool-icon-button mobile-expand-btn"
					class:active={isToolbarExpanded}
					on:click={() => (isToolbarExpanded = !isToolbarExpanded)}
					title={isToolbarExpanded ? 'Hide extra tools' : 'Show extra tools'}
					aria-label={isToolbarExpanded ? 'Hide extra tools' : 'Show extra tools'}
					aria-expanded={isToolbarExpanded}
				>
					<svg
						class="tool-icon"
						viewBox="0 0 24 24"
						fill="none"
						stroke="currentColor"
						stroke-width="2"
					>
						{#if isToolbarExpanded}
							<path d="m6 14 6-6 6 6" />
						{:else}
							<path d="m6 10 6 6 6-6" />
						{/if}
					</svg>
				</button>
				<button
				type="button"
				class="tool-icon-button"
				on:click={redo}
				disabled={!canModerateBoardActions || !canRedoLocalAction}
				title="Redo"
			>
				<svg
					class="tool-icon"
					viewBox="0 0 24 24"
					fill="none"
					stroke="currentColor"
					stroke-width="2"
				>
					<path d="M21 7v6h-6" />
					<path d="M21 13A9 9 0 0 0 6 6.3L3 9" />
				</svg>
			</button>
			{/if}
				<div class="insert-wrap" bind:this={insertWrapEl}>
				<button
					type="button"
					class="insert-toggle"
					class:active={showInsertMenu}
					on:click={toggleInsertMenu}
					aria-haspopup="true"
					aria-expanded={showInsertMenu}
					title={showInsertMenu ? undefined : 'Insert shape'}
				>
					<svg class="tool-icon" viewBox="0 0 24 24" aria-hidden="true">
						<path d="M11 5h2v14h-2z" />
						<path d="M5 11h14v2H5z" />
					</svg>
					<span>Insert</span>
				</button>
				{#if showInsertMenu}
					<div class="insert-menu">
						<button
							type="button"
							class="shape-icon-button"
							on:click={() => beginShapeInsert('line')}
							aria-label="Insert line"
						>
							<svg class="tool-icon" viewBox="0 0 24 24" aria-hidden="true">
								<line x1="4" y1="18" x2="20" y2="6" stroke="currentColor" stroke-width="2.3" />
							</svg>
						</button>
						<button
							type="button"
							class="shape-icon-button"
							on:click={() => beginShapeInsert('arrow')}
							aria-label="Insert arrow"
						>
							<svg class="tool-icon" viewBox="0 0 24 24" aria-hidden="true">
								<path d="M4 12h13" stroke="currentColor" stroke-width="2.3" fill="none" />
								<path d="m13 7 6 5-6 5" fill="currentColor" />
							</svg>
						</button>
						<button
							type="button"
							class="shape-icon-button"
							on:click={() => beginShapeInsert('rect')}
							aria-label="Insert rectangle"
						>
							<svg class="tool-icon" viewBox="0 0 24 24" aria-hidden="true">
								<rect
									x="5"
									y="7"
									width="14"
									height="10"
									rx="2"
									fill="none"
									stroke="currentColor"
									stroke-width="2"
								/>
							</svg>
						</button>
						<button
							type="button"
							class="shape-icon-button"
							on:click={() => beginShapeInsert('ellipse')}
							aria-label="Insert ellipse"
						>
							<svg class="tool-icon" viewBox="0 0 24 24" aria-hidden="true">
								<ellipse
									cx="12"
									cy="12"
									rx="7.5"
									ry="5.2"
									fill="none"
									stroke="currentColor"
									stroke-width="2"
								/>
							</svg>
						</button>
						<button
							type="button"
							class="shape-icon-button"
							on:click={() => beginShapeInsert('circle')}
							aria-label="Insert circle"
						>
							<svg class="tool-icon" viewBox="0 0 24 24" aria-hidden="true">
								<circle
									cx="12"
									cy="12"
									r="6.5"
									fill="none"
									stroke="currentColor"
									stroke-width="2"
								/>
							</svg>
						</button>
						<button
							type="button"
							class="shape-icon-button"
							on:click={() => beginShapeInsert('triangle')}
							aria-label="Insert triangle"
						>
							<svg class="tool-icon" viewBox="0 0 24 24" aria-hidden="true">
								<path d="M12 5.5 19 18H5z" fill="none" stroke="currentColor" stroke-width="2" />
							</svg>
						</button>
					</div>
				{/if}
			</div>
		</div>

		<div
			class="toolbar-secondary-group"
			class:expanded={isToolbarExpanded}
			class:menu-mode={shouldUseToolbarMenu}
			bind:this={toolbarSecondaryEl}
		>
			

			{#if isWidthControlVisible}
				<div class="brush-width-wrap" bind:this={widthMenuWrapEl}>
					<button
						type="button"
						class="line-width-toggle"
						on:click={toggleWidthMenu}
						aria-haspopup="true"
						aria-expanded={showWidthMenu}
						title={showWidthMenu ? undefined : 'Brush width'}
					>
						<svg class="brush-width-icon" viewBox="0 0 24 24" aria-hidden="true">
							<line
								x1="4"
								y1="12"
								x2="20"
								y2="12"
								stroke="currentColor"
								stroke-linecap="round"
								stroke-width={Math.max(2, Math.min(8, drawBrushWidth))}
							/>
						</svg>
						<span class="brush-width-text">{drawBrushWidth.toFixed(1)}px</span>
					</button>
					{#if showWidthMenu}
						<div class="brush-width-menu">
							{#each BRUSH_WIDTH_PRESETS as width}
								<button
									type="button"
									class="brush-width-option"
									class:active={Math.abs(drawBrushWidth - width) < 0.01}
									on:click={() => setDrawWidthPreset(width)}
								>
									<span class="brush-width-sample" style={`height:${Math.max(2, width)}px;`}></span>
									<span>{width.toFixed(1)}px</span>
								</button>
							{/each}
						</div>
					{/if}
				</div>
			{/if}

		

			<button
				type="button"
				class="clear-tool-button"
				class:active={activeTool === 'duster'}
				on:click={() => toggleToolMode('duster')}
				title="Clear board duster"
				aria-label="Clear board duster"
				disabled={!canManageAllBoardElements}
			>
				<svg class="tool-icon" viewBox="0 0 24 24" aria-hidden="true">
					<path d="M4 7.5h16v3H4z" />
					<path d="M7 11.5h10v7H7z" fill="none" stroke="currentColor" stroke-width="1.8" />
				</svg>
				<span>Clear</span>
			</button>
			<button
				type="button"
				class="cancel-operation-button"
				disabled={!canCancelCurrentOperation}
				on:click={cancelCurrentOperation}
				title="Cancel current operation"
			>
				×
			</button>

			<div class="board-details-wrap" bind:this={boardDetailsWrapEl}>
				<button
					type="button"
					class="details-toggle-button"
					class:active={showBoardDetails}
					on:click={toggleBoardDetails}
					title={showBoardDetails ? undefined : 'Board details'}
					aria-label="Board details"
				>
					i
				</button>
				{#if showBoardDetails}
					<div class="board-details-popover">
						<div class="board-detail-row">
							<span>Plane</span>
							<strong>{BOARD_WIDTH}×{BOARD_HEIGHT}px</strong>
						</div>
						<div class="board-detail-row">
							<span>Elements</span>
							<strong>{boardElementCount}</strong>
						</div>
						<div class="board-detail-row">
							<span>Used</span>
							<strong>
								{formatStorageBytes(boardApproxBytes)} / {formatStorageBytes(
									effectiveBoardStorageLimitBytes
								)}
							</strong>
						</div>
						<div class="board-detail-row">
							<span>Remaining</span>
							<strong>{formatStorageBytes(boardRemainingBytes)}</strong>
						</div>
						<div class="board-detail-row">
							<span>Usage</span>
							<strong>{formatUsagePercent(boardStorageUsagePercent)}</strong>
						</div>
						<div class="board-detail-row">
							<span>Zoom</span>
							<strong>{Math.round(boardZoomLevel * 100)}%</strong>
						</div>
						<div class="board-detail-row">
							<span>Access</span>
							<strong>{canManageAllBoardElements ? 'Admin full access' : 'Owner-only edits'}</strong
							>
						</div>
						<div class="board-detail-note">
							Drag empty board to pan. Double-tap empty space to attach. Hold Alt and click to cycle
							overlapping elements. In Clear mode, click and move to erase touched items instantly.
						</div>
					</div>
				{/if}
			</div>
		</div>
	</div>

	<div
		class="board-canvas-shell"
		class:inserting-shape={isInsertOperationActive}
		bind:this={boardContainerEl}
		role="region"
		aria-label="Spatial board canvas"
		on:pointerdown={onBoardPointerDown}
		on:pointermove={onBoardPointerMove}
		on:pointerup={onBoardPointerUp}
		on:pointercancel={onBoardPointerCancel}
		on:contextmenu|preventDefault
	>
		<canvas bind:this={canvasEl}></canvas>
		{#if remoteCursors.length > 0}
			<div class="board-cursor-layer" aria-hidden="true">
				{#each remoteCursors as cursor (cursor.userId)}
					<div
						class="board-remote-cursor"
						style={`left:${getCursorScreenPosition(cursor).left}px;top:${getCursorScreenPosition(cursor).top}px;--cursor-color:${cursor.color};`}
					>
						<span class="cursor-dot"></span>
						<span class="cursor-name">{cursor.name}</span>
					</div>
				{/each}
			</div>
		{/if}
		{#if zControlVisible}
			<div class="board-z-controls" style={`left:${zControlLeft}px;top:${zControlTop}px;`}>
				<button type="button" on:click={bringSelectedObjectForward}>Bring Forward</button>
				<button type="button" on:click={sendSelectedObjectBackward}>Send Backward</button>
			</div>
		{/if}
		{#if activeTool === 'duster' && canManageAllBoardElements}
			<div class="board-duster-layer" aria-hidden="true">
				<div
					class="board-duster-stripe"
					style={`left:${dusterScreenMetrics.left}px;top:${dusterScreenMetrics.top}px;width:${dusterScreenMetrics.width}px;height:${dusterScreenMetrics.height}px;`}
				></div>
			</div>
		{/if}

		{#if boardLoading}
			<div class="board-overlay">Loading board...</div>
		{/if}
		{#if boardError}
			<div class="board-overlay error">{boardError}</div>
		{/if}
		{#if insertionHintLabel}
			<div class="board-insert-hint">{insertionHintLabel}</div>
		{/if}
		{#if contextMenuOpen}
			<div
				class="board-context-menu"
				bind:this={contextMenuEl}
				style={`left:${contextMenuX}px; top:${contextMenuY}px;`}
			>
				<button type="button" on:click={openMediaPicker}>Insert Media</button>
				<button type="button" on:click={openMessagePicker}>Insert Message from Room</button>
			</div>
		{/if}
	</div>
	<canvas
		id="minimap"
		bind:this={minimapEl}
		class="board-minimap"
		width={MINIMAP_WIDTH}
		height={MINIMAP_HEIGHT}
		aria-label="Board minimap"
	></canvas>

	{#if messagePickerOpen}
		<div
			class="board-modal-backdrop"
			role="button"
			tabindex="0"
			aria-label="Close message picker"
			on:pointerdown={() => (messagePickerOpen = false)}
			on:keydown={(event) => {
				if (event.key === 'Enter' || event.key === ' ') {
					event.preventDefault();
					messagePickerOpen = false;
				}
			}}
		>
			<div
				class="board-modal"
				role="dialog"
				aria-label="Select room message"
				tabindex="-1"
				on:pointerdown|stopPropagation
			>
				<div class="board-modal-header">
					<h3>Insert Message from Room</h3>
					<button type="button" on:click={() => (messagePickerOpen = false)}>Close</button>
				</div>
				<input
					type="search"
					bind:value={messageSearch}
					placeholder="Search messages"
					autocomplete="off"
				/>
				<div class="message-picker-list">
					{#if filteredMessages.length === 0}
						<div class="empty-state">No messages available</div>
					{:else}
						{#each filteredMessages as message (message.id)}
							<div class="message-picker-item">
								<div class="message-picker-preview">
									<div class="message-picker-avatar">
										{messageAvatarInitial(message.senderName || 'Guest')}
									</div>
									<div class="message-picker-bubble">
										<div class="message-picker-bubble-header">
											<strong>{message.senderName || 'Guest'}</strong>
											<time>{formatBoardMessageDateTime(message.createdAt)}</time>
										</div>
										<p>{extractMessageSnippet(message)}</p>
										{#if resolveMessagePreviewImageURL(message)}
											<img
												src={resolveMessagePreviewImageURL(message)}
												alt="Message attachment preview"
												loading="lazy"
											/>
										{/if}
									</div>
								</div>
								<div class="message-picker-actions">
									<button type="button" on:click={() => insertRoomMessage(message)}>
										Pin to Board
									</button>
								</div>
							</div>
						{/each}
					{/if}
				</div>
			</div>
		</div>
	{/if}

	<input
		bind:this={mediaInputEl}
		type="file"
		accept={isEphemeralRoom ? 'video/*,audio/*,.pdf,.doc,.docx,.txt' : 'image/*,video/*,audio/*,.pdf,.doc,.docx,.txt'}
		class="hidden-input"
		on:change={onMediaFileSelected}
		disabled={isUploadingMedia}
	/>
</section>

<style>
	.board-root {
		display: flex;
		flex-direction: column;
		gap: 0.6rem;
		flex: 1;
		min-height: 0;
		padding: 0.7rem;
		background: var(--bg-primary);
	}

	.board-toolbar {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.45rem;
		padding: 0.55rem;
		padding-right: 2.8rem;
		border-radius: 10px;
		border: 1px solid var(--border-subtle);
		background: var(--bg-secondary);
		overflow: visible;
		position: relative;
	}

	.toolbar-open-hint {
		flex: 1 0 100%;
		order: -1;
		display: inline-flex;
		align-items: center;
		padding: 0.34rem 2.6rem 0.34rem 0.5rem;
		border-radius: 8px;
		border: 1px solid color-mix(in srgb, #38bdf8 42%, transparent);
		background: color-mix(in srgb, var(--bg-secondary) 82%, #38bdf8 18%);
		color: var(--text-main);
		font-size: 0.72rem;
		font-weight: 600;
		line-height: 1.25;
		pointer-events: none;
	}

	.toolbar-primary-group {
		display: flex;
		align-items: center;
		gap: 0.45rem;
		flex-wrap: wrap;
		min-width: 0;
	}

	.toolbar-secondary-group {
		display: flex;
		align-items: center;
		gap: 0.45rem;
		flex-wrap: wrap;
		min-width: 0;
	}

	.mobile-expand-btn {
		display: none;
	}

	.board-toolbar button {
		border: 1px solid var(--border-subtle);
		background: var(--bg-tertiary);
		color: var(--text-main);
		border-radius: 7px;
		padding: 0.35rem 0.62rem;
		font-size: 0.8rem;
		font-weight: 600;
		cursor: pointer;
	}

	.board-toolbar button:hover:not(:disabled) {
		background: color-mix(in srgb, var(--bg-tertiary) 80%, white 20%);
	}

	.board-toolbar button:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}

	.board-toolbar button.active {
		border-color: #22c55e;
		background: rgba(34, 197, 94, 0.16);
		color: #86efac;
	}

	.tool-icon {
		width: 14px;
		height: 14px;
		display: block;
		fill: currentColor;
		stroke: currentColor;
	}

	.tool-icon-button {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 34px;
		height: 34px;
		padding: 0;
	}

	.board-close-button {
		border-color: #f87171 !important;
		background: rgba(239, 68, 68, 0.9) !important;
		color: #fff !important;
		font-size: 1.05rem;
		font-weight: 800;
		position: absolute;
		top: 0.55rem;
		right: 0.55rem;
		z-index: 8;
	}

	.board-close-button:hover {
		background: rgba(220, 38, 38, 0.98) !important;
	}

	.mobile-expand-btn.active {
		border-color: #38bdf8;
		background: rgba(56, 189, 248, 0.2);
		color: #bae6fd;
	}

	.clear-tool-button {
		display: inline-flex;
		align-items: center;
		gap: 0.32rem;
		padding: 0.35rem 0.52rem;
	}

	.brush-width-wrap {
		position: relative;
		display: inline-flex;
	}

	.line-width-toggle {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		padding: 0.24rem 0.45rem;
		border-radius: 7px;
		border: 1px solid var(--border-subtle);
		background: var(--bg-tertiary);
		color: var(--text-main);
	}

	.brush-width-icon {
		width: 13px;
		height: 13px;
		color: var(--text-muted);
	}

	.brush-width-text {
		font-size: 0.74rem;
		color: var(--text-muted);
		min-width: 2.8rem;
	}

	.brush-width-menu {
		position: absolute;
		top: 50%;
		left: calc(100% + 8px);
		transform: translateY(-50%);
		z-index: 27;
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
		padding: 0.4rem;
		border-radius: 10px;
		border: 1px solid var(--border-subtle);
		background: var(--bg-secondary);
		box-shadow: 0 12px 24px rgba(0, 0, 0, 0.2);
	}

	.brush-width-option {
		min-width: 98px;
		display: inline-flex;
		align-items: center;
		gap: 0.45rem;
	}

	.brush-width-option.active {
		border-color: #22c55e;
		background: rgba(34, 197, 94, 0.16);
		color: #86efac;
	}

	.brush-width-sample {
		display: inline-block;
		width: 26px;
		background: currentColor;
		border-radius: 999px;
		opacity: 0.95;
	}

	.color-menu-wrap {
		position: relative;
		display: inline-flex;
		align-items: center;
	}

	.color-menu-toggle {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		padding: 0.24rem 0.45rem;
	}

	.color-swatch {
		width: 14px;
		height: 14px;
		border-radius: 4px;
		border: 1px solid rgba(148, 163, 184, 0.72);
		box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.2);
	}

	.color-label {
		font-size: 0.74rem;
		color: var(--text-muted);
	}

	.color-menu-popover {
		position: absolute;
		top: calc(100% + 6px);
		left: 0;
		z-index: 36;
		display: flex;
		flex-direction: column;
		gap: 0.42rem;
		padding: 0.45rem;
		border-radius: 10px;
		border: 1px solid var(--border-subtle);
		background: var(--bg-secondary);
		box-shadow: 0 14px 24px rgba(0, 0, 0, 0.22);
		min-width: 170px;
	}

	.color-preset-grid {
		display: grid;
		grid-template-columns: repeat(4, minmax(0, 1fr));
		gap: 0.35rem;
	}

	.board-toolbar .color-preset-grid .color-preset-button {
		width: 28px;
		height: 28px;
		min-width: 28px;
		min-height: 28px;
		padding: 0 !important;
		border-radius: 6px;
		background: var(--swatch, #111827);
		border: 1px solid rgba(148, 163, 184, 0.75);
		color: transparent;
	}

	.board-toolbar .color-preset-grid .color-preset-button:hover:not(:disabled),
	.board-toolbar .color-preset-grid .color-preset-button:focus-visible {
		background: var(--swatch, #111827);
	}

	.board-toolbar .color-preset-grid .color-preset-button.active {
		background: var(--swatch, #111827);
		border-color: rgba(148, 163, 184, 0.92);
		outline: 2px solid #38bdf8;
		outline-offset: 1px;
	}

	.color-picker-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		font-size: 0.72rem;
		color: var(--text-muted);
	}

	.color-picker-row input[type='color'] {
		width: 34px;
		height: 26px;
		border: 1px solid var(--border-subtle);
		border-radius: 6px;
		padding: 0;
		background: transparent;
	}

	.insert-wrap {
		position: relative;
		display: inline-flex;
		align-items: center;
	}

	.insert-toggle {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
	}

	.insert-menu {
		position: absolute;
		top: calc(100% + 6px);
		left: 0;
		z-index: 35;
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.4rem;
		padding: 0.45rem;
		border-radius: 9px;
		border: 1px solid var(--border-subtle);
		background: var(--bg-secondary);
		box-shadow: 0 12px 24px rgba(0, 0, 0, 0.2);
		min-width: max-content;
	}

	.shape-icon-button {
		width: 36px;
		height: 36px;
		min-width: 36px;
		min-height: 36px;
		padding: 0 !important;
		margin: 0;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		line-height: 1;
	}

	.board-details-wrap {
		position: relative;
		display: inline-flex;
	}

	.details-toggle-button {
		width: 32px;
		height: 32px;
		padding: 0;
		border-radius: 999px;
		font-size: 0.9rem;
		font-weight: 700;
		display: inline-flex;
		align-items: center;
		justify-content: center;
	}

	.board-details-popover {
		position: absolute;
		top: calc(100% + 7px);
		right: 0;
		z-index: 28;
		min-width: 240px;
		display: flex;
		flex-direction: column;
		gap: 0.35rem;
		padding: 0.55rem;
		border-radius: 10px;
		border: 1px solid var(--border-subtle);
		background: var(--bg-secondary);
		box-shadow: 0 16px 28px rgba(0, 0, 0, 0.26);
	}

	.board-detail-note {
		margin-top: 0.15rem;
		font-size: 0.72rem;
		line-height: 1.35;
		color: var(--text-muted);
		padding-top: 0.35rem;
		border-top: 1px solid var(--border-subtle);
	}

	.board-detail-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
		font-size: 0.76rem;
		color: var(--text-muted);
	}

	.board-detail-row strong {
		color: var(--text-main);
		font-size: 0.78rem;
		font-weight: 700;
		text-align: right;
	}

	.cancel-operation-button {
		width: 34px;
		height: 34px;
		padding: 0;
		border-radius: 999px;
		border: 1px solid #ef4444;
		background: rgba(239, 68, 68, 0.9);
		color: #fff;
		font-size: 1.1rem;
		line-height: 1;
		font-weight: 700;
		display: inline-flex;
		align-items: center;
		justify-content: center;
	}

	.cancel-operation-button:hover:not(:disabled) {
		background: rgba(220, 38, 38, 0.98);
	}

	.board-canvas-shell {
		position: relative;
		flex: 1;
		min-height: 300px;
		overflow: hidden;
		border-radius: 12px;
		border: 1px solid var(--border-subtle);
		background: var(--bg-secondary);
	}

	.board-canvas-shell.inserting-shape {
		cursor: crosshair;
	}

	.board-canvas-shell :global(canvas) {
		touch-action: none;
	}

	.board-cursor-layer {
		position: absolute;
		inset: 0;
		z-index: 16;
		pointer-events: none;
	}

	.board-remote-cursor {
		position: absolute;
		transform: translate(-4px, -4px);
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
	}

	.cursor-dot {
		width: 8px;
		height: 8px;
		border-radius: 50%;
		background: var(--cursor-color, #06b6d4);
		box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.78);
	}

	.cursor-name {
		font-size: 0.64rem;
		font-weight: 700;
		padding: 0.08rem 0.35rem;
		border-radius: 999px;
		background: color-mix(in srgb, var(--cursor-color, #06b6d4) 70%, #0f172a 30%);
		color: #f8fafc;
		white-space: nowrap;
	}

	.board-z-controls {
		position: absolute;
		z-index: 22;
		display: inline-flex;
		gap: 0.3rem;
		flex-wrap: wrap;
		padding: 0.3rem;
		max-width: min(240px, calc(100% - 12px));
		border-radius: 10px;
		border: 1px solid rgba(148, 163, 184, 0.45);
		background: rgba(15, 23, 42, 0.9);
		backdrop-filter: blur(6px);
	}

	.board-z-controls button {
		border: 1px solid rgba(148, 163, 184, 0.55);
		background: rgba(30, 41, 59, 0.95);
		color: #e2e8f0;
		border-radius: 7px;
		padding: 0.22rem 0.46rem;
		font-size: 0.7rem;
		font-weight: 600;
	}

	.board-z-controls button:hover {
		background: rgba(51, 65, 85, 0.95);
	}

	.board-duster-layer {
		position: absolute;
		inset: 0;
		z-index: 18;
		pointer-events: none;
	}

	.board-duster-stripe {
		position: absolute;
		border-left: 1px dashed rgba(248, 113, 113, 0.7);
		border-right: 1px dashed rgba(248, 113, 113, 0.7);
		background: linear-gradient(
			180deg,
			rgba(248, 113, 113, 0.24) 0%,
			rgba(239, 68, 68, 0.2) 45%,
			rgba(220, 38, 38, 0.24) 100%
		);
		box-shadow: inset 0 0 0 1px rgba(248, 113, 113, 0.3);
	}

	.board-overlay {
		position: absolute;
		top: 0.8rem;
		left: 50%;
		transform: translateX(-50%);
		background: rgba(15, 23, 42, 0.85);
		color: #f8fafc;
		border-radius: 999px;
		padding: 0.32rem 0.7rem;
		font-size: 0.76rem;
		font-weight: 600;
		pointer-events: none;
	}

	.board-overlay.error {
		background: rgba(220, 38, 38, 0.85);
	}

	.board-insert-hint {
		position: absolute;
		top: 0.8rem;
		right: 0.8rem;
		max-width: min(340px, calc(100% - 1.6rem));
		z-index: 24;
		padding: 0.4rem 0.6rem;
		border-radius: 10px;
		border: 1px solid color-mix(in srgb, #38bdf8 45%, transparent);
		background: color-mix(in srgb, #0f172a 88%, #38bdf8 12%);
		color: #e2e8f0;
		font-size: 0.72rem;
		line-height: 1.3;
		font-weight: 600;
		pointer-events: none;
	}

	.board-context-menu {
		position: absolute;
		z-index: 30;
		display: flex;
		flex-direction: column;
		min-width: 200px;
		border: 1px solid var(--border-subtle);
		border-radius: 10px;
		overflow: hidden;
		background: var(--bg-secondary);
		box-shadow: 0 16px 30px rgba(0, 0, 0, 0.26);
	}

	.board-context-menu button {
		border: none;
		text-align: left;
		padding: 0.55rem 0.75rem;
		font-size: 0.84rem;
		background: transparent;
		color: var(--text-main);
		cursor: pointer;
	}

	.board-context-menu button:hover {
		background: var(--bg-tertiary);
	}

	.board-modal-backdrop {
		position: absolute;
		inset: 0;
		z-index: 40;
		display: flex;
		align-items: center;
		justify-content: center;
		background: rgba(3, 7, 18, 0.55);
		backdrop-filter: blur(4px);
	}

	.board-modal {
		width: min(720px, 92vw);
		max-height: 80vh;
		display: flex;
		flex-direction: column;
		gap: 0.55rem;
		padding: 0.8rem;
		border-radius: 12px;
		border: 1px solid var(--border-subtle);
		background: var(--bg-secondary);
	}

	.board-modal-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
	}

	.board-modal-header h3 {
		margin: 0;
		font-size: 1rem;
		color: var(--text-main);
	}

	.board-modal-header button {
		border: 1px solid var(--border-subtle);
		background: var(--bg-tertiary);
		color: var(--text-main);
		border-radius: 7px;
		padding: 0.28rem 0.58rem;
		cursor: pointer;
	}

	.board-modal input[type='search'] {
		border: 1px solid var(--border-subtle);
		background: var(--bg-primary);
		color: var(--text-main);
		border-radius: 8px;
		padding: 0.55rem 0.65rem;
	}

	.message-picker-list {
		display: flex;
		flex-direction: column;
		gap: 0.55rem;
		overflow: auto;
		padding-right: 0.15rem;
	}

	.message-picker-item {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.7rem;
		border: 1px solid var(--border-subtle);
		background: var(--bg-primary);
		border-radius: 10px;
		padding: 0.62rem;
	}

	.message-picker-item:hover {
		background: var(--bg-tertiary);
	}

	.message-picker-preview {
		display: flex;
		align-items: flex-start;
		gap: 0.58rem;
		min-width: 0;
		flex: 1;
	}

	.message-picker-avatar {
		width: 28px;
		height: 28px;
		flex: 0 0 28px;
		border-radius: 999px;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		font-size: 0.74rem;
		font-weight: 700;
		color: #f8fafc;
		background: linear-gradient(145deg, #0ea5e9 0%, #2563eb 100%);
	}

	.message-picker-bubble {
		min-width: 0;
		flex: 1;
		border-radius: 10px;
		padding: 0.5rem 0.58rem;
		background: color-mix(in srgb, var(--bg-secondary) 85%, #0f172a 15%);
		border: 1px solid color-mix(in srgb, var(--border-subtle) 78%, transparent 22%);
	}

	.message-picker-bubble-header {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.55rem;
		margin-bottom: 0.26rem;
	}

	.message-picker-bubble-header strong {
		font-size: 0.76rem;
		color: var(--text-main);
	}

	.message-picker-bubble-header time {
		font-size: 0.68rem;
		color: var(--text-muted);
		white-space: nowrap;
	}

	.message-picker-bubble p {
		margin: 0;
		font-size: 0.82rem;
		line-height: 1.36;
		color: var(--text-main);
		white-space: pre-wrap;
		word-break: break-word;
	}

	.message-picker-bubble img {
		margin-top: 0.42rem;
		display: block;
		max-width: min(260px, 100%);
		max-height: 170px;
		border-radius: 8px;
		object-fit: cover;
		border: 1px solid color-mix(in srgb, var(--border-subtle) 70%, transparent 30%);
	}

	.message-picker-actions {
		display: inline-flex;
		align-items: center;
		justify-content: center;
	}

	.message-picker-actions button {
		border: 1px solid rgba(34, 197, 94, 0.55);
		background: rgba(2, 243, 90, 0.636);
		color: #f9f9f9;
		border-radius: 8px;
		padding: 0.38rem 0.58rem;
		font-size: 0.75rem;
		font-weight: 700;
		white-space: nowrap;
	}

	.message-picker-actions button:hover {
		background: rgba(34, 197, 94, 0.28);
	}

	.empty-state {
		font-size: 0.82rem;
		color: var(--text-muted);
		padding: 0.65rem 0.2rem;
	}

	.hidden-input {
		display: none;
	}

	.board-minimap {
		position: fixed;
		right: 1rem;
		bottom: 1rem;
		z-index: 45;
		width: 200px;
		height: 150px;
		border-radius: 10px;
		border: 1px solid rgba(148, 163, 184, 0.55);
		background: rgba(15, 23, 42, 0.9);
		box-shadow: 0 14px 26px rgba(15, 23, 42, 0.35);
	}

	@media (max-width: 1200px) {
		.board-root {
			padding: 0.45rem;
		}

		.brush-width-menu {
			top: calc(100% + 6px);
			left: 0;
			transform: none;
			flex-direction: row;
			flex-wrap: wrap;
		}

		.board-details-popover {
			right: 0;
			min-width: min(250px, 90vw);
		}

		.board-minimap {
			right: 0.65rem;
			bottom: 0.65rem;
			width: 160px;
			height: 120px;
		}
	}

	@media (max-width: 768px) {
		.board-toolbar {
			flex-direction: row;
			flex-wrap: wrap;
			align-items: flex-start;
			justify-content: flex-start;
			gap: 0.3rem;
			padding: 0.42rem;
			padding-right: 2.5rem;
			width: 100%;
			max-width: 92vw;
			box-sizing: border-box;
		}

		.toolbar-open-hint {
			padding: 0.28rem 2.3rem 0.28rem 0.44rem;
			font-size: 0.68rem;
		}

		.toolbar-primary-group {
			width: auto;
			flex: 0 1 auto;
			min-width: 0;
			justify-content: flex-start;
			gap: 0.3rem;
			flex-wrap: wrap;
			overflow: visible;
		}

		.mobile-expand-btn {
			display: inline-flex;
			margin-left: auto;
			order: 60;
		}

		.toolbar-secondary-group {
			display: flex;
			width: auto;
			flex: 1 1 auto;
			min-width: 0;
			gap: 0.3rem;
			flex-wrap: wrap;
		}

		.toolbar-secondary-group.menu-mode {
			display: none;
			flex: 1 0 100%;
			width: 100%;
			border-top: 1px solid var(--border-subtle);
			padding-top: 0.42rem;
			margin-top: 0.1rem;
			flex-wrap: wrap;
		}

		.toolbar-secondary-group.menu-mode.expanded {
			display: flex;
		}

		.board-toolbar button {
			padding: 0.28rem 0.46rem;
			font-size: 0.74rem;
		}

		.tool-icon-button {
			width: 30px;
			height: 30px;
		}

		.board-close-button {
			top: 0.42rem;
			right: 0.42rem;
		}

		.insert-toggle {
			gap: 0.25rem;
			padding: 0.28rem 0.46rem;
		}

		.insert-menu {
			left: 50%;
			right: auto;
			transform: translateX(-50%);
			gap: 0.32rem;
			padding: 0.38rem;
		}

		.insert-menu .shape-icon-button {
			width: 32px;
			height: 32px;
			min-width: 32px;
			min-height: 32px;
			padding: 0 !important;
		}

		.clear-tool-button {
			gap: 0.24rem;
			padding: 0.28rem 0.44rem;
		}

		.line-width-toggle {
			gap: 0.26rem;
			padding: 0.22rem 0.38rem;
		}

		.color-menu-toggle {
			gap: 0.22rem;
			padding: 0.22rem 0.36rem;
		}

		.color-label {
			display: none;
		}

		.color-menu-popover {
			left: 0;
			right: auto;
			transform: none;
			min-width: 158px;
			max-width: calc(100vw - 1.2rem);
		}

		.brush-width-text {
			min-width: 2.4rem;
			font-size: 0.69rem;
		}

		.board-z-controls {
			gap: 0.22rem;
			padding: 0.24rem;
			max-width: calc(100% - 10px);
		}

		.board-z-controls button {
			font-size: 0.62rem;
			padding: 0.18rem 0.34rem;
		}
	}
</style>
