<script lang="ts">
	import { browser } from '$app/environment';
	import type { ChatMessage } from '$lib/types/chat';
	import {
		createMessageId,
		normalizeIdentifier,
		normalizeMessageID,
		normalizeRoomIDValue,
		parseOptionalTimestamp,
		toInt,
		toStringValue
	} from '$lib/utils/chat/core';
	import { inferMediaMessageType, uploadToR2, type MediaMessageType } from '$lib/utils/media';
	import { globalMessages, sendSocketPayload } from '$lib/ws';
	import { onDestroy, onMount } from 'svelte';

	const API_BASE = (import.meta.env.VITE_API_BASE as string | undefined) ?? 'http://localhost:8080';
	const BOARD_WIDTH = 3840;
	const BOARD_HEIGHT = 2560;
	const MIN_ZOOM = 0.08;
	const MAX_ZOOM = 4;
	const DOUBLE_TAP_MS = 340;
	const TAP_MOVE_TOLERANCE = 8;
	const DRAW_PROGRESS_THROTTLE_MS = 90;
	const HISTORY_LIMIT = 80;
	const BRUSH_WIDTH_PRESETS = [1.5, 3, 5, 8] as const;
	const FABRIC_VITE_ID_URL = '/@id/fabric';
	const FABRIC_CDN_URL = 'https://cdn.jsdelivr.net/npm/fabric@6.5.3/+esm';
	const DEFAULT_RECT_WIDTH = 180;
	const DEFAULT_RECT_HEIGHT = 110;
	const DEFAULT_CIRCLE_DIAMETER = 120;
	const DEFAULT_LINE_LENGTH = 190;
	const MIN_SHAPE_WIDTH = 96;
	const MIN_SHAPE_HEIGHT = 72;
	const DEFAULT_MESSAGE_CARD_WIDTH = 340;
	const DEFAULT_MEDIA_CARD_WIDTH = 360;
	const MAX_IMAGE_PREVIEW_HEIGHT = 460;
	const MAX_VIDEO_PREVIEW_HEIGHT = 360;
	const LOCAL_ACTION_LIMIT = 180;
	const DUSTER_STRIPE_WIDTH = BOARD_WIDTH * 0.01;
	const DUSTER_HANDLE_WIDTH = 56;
	const DUSTER_HANDLE_HEIGHT = 34;
	const DUSTER_HANDLE_PADDING = 8;
	const BOARD_STORAGE_LIMIT_BYTES = 10 * 1024 * 1024;
	const UTF8_ENCODER = new TextEncoder();

	type ToolMode = 'select' | 'draw' | 'eraser' | 'duster';
	type ShapeKind = 'line' | 'arrow' | 'rect' | 'circle';
	type BoardEventType =
		| 'board_draw_start'
		| 'board_draw_progress'
		| 'board_element_add'
		| 'board_element_move'
		| 'board_element_delete';

	type DusterScreenMetrics = {
		left: number;
		top: number;
		width: number;
		height: number;
		handleLeft: number;
		handleTop: number;
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
	};

	type LocalBoardAction = {
		kind: 'add' | 'move' | 'delete';
		elementId: string;
		before?: BoardElementWire;
		after?: BoardElementWire;
	};

	export let roomId = '';
	export let messages: ChatMessage[] = [];
	export let isDarkMode = false;
	export let canEdit = true;
	export let canModerateBoard = false;
	export let currentUserId = '';
	export let currentUsername = '';

	let boardContainerEl: HTMLDivElement | null = null;
	let canvasEl: HTMLCanvasElement | null = null;
	let mediaInputEl: HTMLInputElement | null = null;
	let insertWrapEl: HTMLDivElement | null = null;
	let widthMenuWrapEl: HTMLDivElement | null = null;
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
	let showWidthMenu = false;
	let pendingShapeKind: ShapeKind | null = null;
	let pendingInsertElementId = '';
	let isInsertOperationActive = false;
	let insertionHintLabel = '';
	let showToolbarMore = false;
	let showBoardDetails = false;
	let canModerateBoardActions = false;
	let canManageAllBoardElements = false;
	let canUndoLocalAction = false;
	let canRedoLocalAction = false;
	let boardElementCount = 0;
	let boardApproxBytes = 0;
	let boardStorageUsagePercent = 0;
	let boardRemainingBytes = BOARD_STORAGE_LIMIT_BYTES;
	let boardZoomLevel = 1;
	let dusterCenterX = BOARD_WIDTH / 2;
	let dusterIsDragging = false;
	let dusterPointerId: number | null = null;
	let viewportRenderTick = 0;
	let dusterScreenMetrics: DusterScreenMetrics = {
		left: -9999,
		top: 0,
		width: 0,
		height: 0,
		handleLeft: -9999,
		handleTop: DUSTER_HANDLE_PADDING
	};
	let pendingTapGesture:
		| {
				startX: number;
				startY: number;
				moved: boolean;
				emptyTarget: boolean;
				boardPoint: { x: number; y: number };
			}
		| null = null;
	let lastEmptyTapAt = 0;
	let isPanning = false;
	let panLastX = 0;
	let panLastY = 0;
	let isDrawingGesture = false;
	let lastDrawProgressAt = 0;
	let drawingProgressPoint: { x: number; y: number } | null = null;

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
	let removeWindowKeyListeners: (() => void) | null = null;
	let removeWindowPointerListener: (() => void) | null = null;
	let boardPermissionRefreshKey = '';

	$: normalizedRoomId = normalizeRoomIDValue(roomId);
	$: normalizedCurrentUserID = normalizeIdentifier(currentUserId);
	$: normalizedCurrentUsername = (currentUsername || '').trim();
	$: isInsertOperationActive = Boolean(pendingShapeKind || pendingInsertElementId);
	$: insertionHintLabel = pendingShapeKind
		? pendingInsertElementId
			? 'Resize or move shape, then click once to place it'
			: `Click board to place ${pendingShapeKind}`
		: '';
	$: filteredMessages = (messages ?? [])
		.filter((entry) => normalizeMessageID(entry.id) !== '')
		.sort((left, right) => right.createdAt - left.createdAt)
		.filter((entry) => {
			const snippet = extractMessageSnippet(entry).toLowerCase();
			return messageSearch.trim() ? snippet.includes(messageSearch.trim().toLowerCase()) : true;
		})
		.slice(0, 120);

	$: if (boardReady) {
		updateBoardVisualTheme();
	}

	$: if (boardReady && normalizedRoomId && normalizedRoomId !== initializedRoomId) {
		void loadBoard(normalizedRoomId);
	}
	$: canModerateBoardActions = canEdit;
	$: canManageAllBoardElements = canEdit && canModerateBoard;
	$: if (!canModerateBoardActions && (activeTool === 'eraser' || activeTool === 'duster')) {
		applyToolMode('select');
	}
	$: canUndoLocalAction = localUndoStack.length > 0;
	$: canRedoLocalAction = localRedoStack.length > 0;
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
		BOARD_STORAGE_LIMIT_BYTES > 0
			? Math.min(100, (boardApproxBytes / BOARD_STORAGE_LIMIT_BYTES) * 100)
			: 0;
	$: boardRemainingBytes = Math.max(0, BOARD_STORAGE_LIMIT_BYTES - boardApproxBytes);
	$: boardPermissionRefreshKey = `${canEdit ? 1 : 0}:${canManageAllBoardElements ? 1 : 0}:${normalizedCurrentUserID}`;
	$: if (boardReady) {
		void boardPermissionRefreshKey;
		applyBoardObjectPermissions();
	}

	onMount(() => {
		if (!browser) {
			return;
		}

		registerWindowGuards();
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
		if (removeWindowKeyListeners) {
			removeWindowKeyListeners();
			removeWindowKeyListeners = null;
		}
		if (removeWindowPointerListener) {
			removeWindowPointerListener();
			removeWindowPointerListener = null;
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
		boardRemainingBytes = BOARD_STORAGE_LIMIT_BYTES;
		boardZoomLevel = 1;
	}

	function registerWindowGuards() {
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key === 'Escape' && canCancelCurrentOperation) {
				event.preventDefault();
				cancelCurrentOperation();
				return;
			}
			if ((event.key === 'Delete' || event.key === 'Backspace') && canModerateBoardActions) {
				const activeObject = fabricCanvas?.getActiveObject?.();
				if (activeObject) {
					event.preventDefault();
					removeBoardObject(activeObject, true);
				}
			}
		};
		const onPointerDown = (event: PointerEvent) => {
			if (dusterIsDragging) {
				return;
			}
			const target = event.target;
			if (target instanceof Node) {
				if (insertWrapEl && insertWrapEl.contains(target)) {
					return;
				}
				if (widthMenuWrapEl && widthMenuWrapEl.contains(target)) {
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
			showBoardDetails = false;
			showToolbarMore = false;
		};
		const onPointerMove = (event: PointerEvent) => {
			if (!dusterIsDragging) {
				return;
			}
			if (dusterPointerId !== null && event.pointerId !== dusterPointerId) {
				return;
			}
			event.preventDefault();
			moveDusterToClientX(event.clientX, true);
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

	async function initializeBoard() {
		if (!canvasEl || !boardContainerEl) {
			return;
		}

		boardError = '';
		try {
			fabricPackage = (await import(
				/* @vite-ignore */ FABRIC_VITE_ID_URL
			)) as Record<string, unknown>;
		} catch (primaryError) {
			try {
				fabricPackage = (await import(
					/* @vite-ignore */ FABRIC_CDN_URL
				)) as Record<string, unknown>;
			} catch (fallbackError) {
				const primaryMessage = primaryError instanceof Error ? primaryError.message : String(primaryError);
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
		fabricCanvas.renderOnAddRemove = true;
		ensureBoardBoundsObject();
		updateBoardVisualTheme();
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

	function updateBoardVisualTheme() {
		if (!fabricCanvas || !boardBoundsRect) {
			return;
		}
		boardBoundsRect.set?.({
			fill: isDarkMode ? '#101316' : '#ffffff',
			stroke: isDarkMode ? '#2f3640' : '#cfd8e3'
		});
		fabricCanvas.backgroundColor = isDarkMode ? '#080b0f' : '#edf2f8';
		fabricCanvas.requestRenderAll?.();
		applyToolMode(activeTool, false);
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
		viewport[4] = Math.min(0, Math.max(minTranslateX, toNumber(viewport[4], 0)));
		viewport[5] = Math.min(0, Math.max(minTranslateY, toNumber(viewport[5], 0)));
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
			const delta = nativeEvent.deltaY;
			let zoom = fabricCanvas.getZoom?.() ?? 1;
			const intensity = Math.max(0.5, Math.min(2, Math.abs(delta) / 80));
			const step = delta > 0 ? 0.84 : 1.19;
			zoom *= step ** intensity;
			zoom = clampZoom(zoom);
			const pointer = {
				x: nativeEvent.offsetX,
				y: nativeEvent.offsetY
			};
			fabricCanvas.zoomToPoint?.(pointer, zoom);
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
				target !== boardBoundsRect &&
				canMutateBoardObject(target)
			) {
				removeBoardObject(target, true);
				return;
			}

			if (canEdit && activeTool === 'draw') {
				isDrawingGesture = true;
				drawingProgressPoint = getBoardPointFromClientPosition(clientPoint.x, clientPoint.y);
				sendBoardEnvelope('board_draw_start', {
					x: drawingProgressPoint.x,
					y: drawingProgressPoint.y
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

			if (!canEdit || activeTool !== 'draw' || !isDrawingGesture) {
				return;
			}
			const now = Date.now();
			if (now - lastDrawProgressAt < DRAW_PROGRESS_THROTTLE_MS) {
				return;
			}
			lastDrawProgressAt = now;
			drawingProgressPoint = getBoardPointFromClientPosition(clientPoint.x, clientPoint.y);
			sendBoardEnvelope('board_draw_progress', {
				x: drawingProgressPoint.x,
				y: drawingProgressPoint.y
			});
		});

		fabricCanvas.on('mouse:up', () => {
			isPanning = false;
			isDrawingGesture = false;
			drawingProgressPoint = null;
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
			if (!canMutateBoardObject(target)) {
				applyObjectPermission(target);
				fabricCanvas.requestRenderAll?.();
				return;
			}
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
		});

		fabricCanvas.on('object:scaling', (event: any) => {
			const target = event?.target as FabricObjectLike | null;
			if (!target || target === boardBoundsRect) {
				return;
			}
			enforceMinimumObjectSize(target);
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
		if ((mode === 'eraser' || mode === 'duster') && !canModerateBoardActions) {
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
				fabricCanvas.freeDrawingBrush.color = isDarkMode ? '#e5e7eb' : '#111827';
				fabricCanvas.freeDrawingBrush.width = drawBrushWidth;
			}
		}
		if (resetSelection) {
			showInsertMenu = false;
			showWidthMenu = false;
		}
		if (resetSelection && mode !== 'eraser') {
			fabricCanvas.discardActiveObject?.();
			fabricCanvas.requestRenderAll?.();
		}
	}

	function toggleWidthMenu() {
		showWidthMenu = !showWidthMenu;
		if (showWidthMenu) {
			showInsertMenu = false;
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
		showWidthMenu = false;
		showBoardDetails = false;
		showInsertMenu = !showInsertMenu;
	}

	function toggleToolbarMore() {
		showToolbarMore = !showToolbarMore;
		if (!showToolbarMore) {
			showBoardDetails = false;
		}
	}

	function toggleBoardDetails() {
		showBoardDetails = !showBoardDetails;
		if (showBoardDetails) {
			showInsertMenu = false;
			showWidthMenu = false;
		}
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
		const shapeObject = createShapeObjectAtPoint(pendingShapeKind, point);
		if (!shapeObject) {
			return;
		}
		const identity = ensureObjectIdentity(shapeObject, pendingShapeKind);
		shapeObject.set?.({
			pendingCommit: true
		});
		pendingInsertElementId = identity.elementId;
		fabricCanvas.add(shapeObject);
		applyObjectPermission(shapeObject);
		fabricCanvas.setActiveObject?.(shapeObject);
		fabricCanvas.requestRenderAll?.();
		captureHistorySnapshot();
	}

	function commitPendingShapeInsert() {
		if (!fabricCanvas || !pendingInsertElementId) {
			pendingShapeKind = null;
			pendingInsertElementId = '';
			return;
		}
		const pendingObject = findObjectByElementId(pendingInsertElementId);
		if (!pendingObject) {
			pendingShapeKind = null;
			pendingInsertElementId = '';
			return;
		}
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
	}

	function createShapeObjectAtPoint(kind: ShapeKind, point: { x: number; y: number }): FabricObjectLike | null {
		if (!fabricCanvas) {
			return null;
		}
		const sharedStyle = {
			stroke: isDarkMode ? '#f3f4f6' : '#111827',
			strokeWidth: 2,
			fill: 'transparent'
		};

		if (kind === 'rect') {
			const RectClass = getFabricClass('Rect');
			const width = DEFAULT_RECT_WIDTH;
			const height = DEFAULT_RECT_HEIGHT;
			return RectClass
				? (new RectClass({
						...sharedStyle,
						left: clampBoardX(point.x, width),
						top: clampBoardY(point.y, height),
						width,
						height,
						rx: 10,
						ry: 10
					}) as FabricObjectLike)
				: null;
		}
		if (kind === 'circle') {
			const CircleClass = getFabricClass('Circle');
			const diameter = DEFAULT_CIRCLE_DIAMETER;
			return CircleClass
				? (new CircleClass({
						...sharedStyle,
						left: clampBoardX(point.x, diameter),
						top: clampBoardY(point.y, diameter),
						radius: diameter / 2
					}) as FabricObjectLike)
				: null;
		}
		const LineClass = getFabricClass('Line');
		if (!LineClass) {
			return null;
		}
		const x1 = clampBoardX(point.x, DEFAULT_LINE_LENGTH);
		const y1 = clampBoardY(point.y, MIN_SHAPE_HEIGHT);
		return new LineClass([x1, y1, x1 + DEFAULT_LINE_LENGTH, y1], {
			stroke: isDarkMode ? '#f3f4f6' : '#111827',
			strokeWidth: 3
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
			toStringValue(record.createdByUserId ?? record.created_by_user_id ?? record.senderId ?? record.sender_id)
		);
	}

	function canMutateOwner(ownerUserID: string) {
		if (!canEdit) {
			return false;
		}
		if (canManageAllBoardElements) {
			return true;
		}
		const normalizedOwner = normalizeIdentifier(ownerUserID);
		return normalizedOwner !== '' && normalizedOwner === normalizedCurrentUserID;
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
		const canMutate = canMutateBoardObject(object);
		object.set?.({
			selectable: canMutate,
			evented: canMutate,
			hasControls: canMutate,
			lockMovementX: !canMutate,
			lockMovementY: !canMutate,
			lockScalingX: !canMutate,
			lockScalingY: !canMutate,
			lockRotation: !canMutate
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

	function clampDusterCenterX(x: number) {
		const halfStripe = DUSTER_STRIPE_WIDTH / 2;
		return Math.max(halfStripe, Math.min(BOARD_WIDTH - halfStripe, x));
	}

	function markViewportForRender() {
		viewportRenderTick = Date.now();
	}

	function resolveDusterScreenMetrics(_tick: number, centerX: number): DusterScreenMetrics {
		void _tick;
		const fallbackTop = DUSTER_HANDLE_PADDING;
		if (!fabricCanvas || !boardContainerEl) {
			return {
				left: -9999,
				top: 0,
				width: 0,
				height: 0,
				handleLeft: -9999,
				handleTop: fallbackTop
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
		const containerHeight = Math.max(1, boardContainerEl.clientHeight || 1);
		const handleTop = Math.max(
			DUSTER_HANDLE_PADDING,
			Math.min(containerHeight - DUSTER_HANDLE_HEIGHT - DUSTER_HANDLE_PADDING, top + DUSTER_HANDLE_PADDING)
		);
		return {
			left,
			top,
			width: stripeWidthPx,
			height,
			handleLeft: left + stripeWidthPx / 2,
			handleTop
		};
	}

	function stopDusterDrag() {
		dusterIsDragging = false;
		dusterPointerId = null;
	}

	function moveDusterToBoardX(boardX: number, sweep = false) {
		const nextX = clampDusterCenterX(boardX);
		if (Math.abs(nextX - dusterCenterX) < 0.01 && !sweep) {
			return;
		}
		dusterCenterX = nextX;
		markViewportForRender();
		if (sweep) {
			sweepBoardAtDuster(nextX);
		}
	}

	function moveDusterToClientX(clientX: number, sweep = false) {
		if (!boardContainerEl) {
			return;
		}
		const rect = boardContainerEl.getBoundingClientRect();
		const anchorY = rect.top + Math.max(8, Math.min(rect.height - 8, rect.height * 0.35));
		const point = getBoardPointFromClientPosition(clientX, anchorY);
		moveDusterToBoardX(point.x, sweep);
	}

	function sweepBoardAtDuster(centerX: number) {
		if (!fabricCanvas || !canModerateBoardActions) {
			return;
		}
		const stripeLeft = centerX - DUSTER_STRIPE_WIDTH / 2;
		const stripeRight = centerX + DUSTER_STRIPE_WIDTH / 2;
		const objects = [...(fabricCanvas.getObjects?.() ?? [])];
		for (const object of objects) {
			if (object === boardBoundsRect) {
				continue;
			}
			const candidate = object as FabricObjectLike;
			if (isPendingObject(candidate)) {
				continue;
			}
			const element = boardObjectToElement(candidate);
			if (!element) {
				continue;
			}
			const elementLeft = element.x;
			const elementRight = element.x + Math.max(1, element.width);
			if (elementRight < stripeLeft || elementLeft > stripeRight) {
				continue;
			}
			removeBoardObject(candidate, true);
		}
	}

	function onDusterHandlePointerDown(event: PointerEvent) {
		if (!canModerateBoardActions || activeTool !== 'duster') {
			return;
		}
		event.preventDefault();
		event.stopPropagation();
		contextMenuOpen = false;
		showInsertMenu = false;
		showWidthMenu = false;
		messagePickerOpen = false;
		dusterIsDragging = true;
		dusterPointerId = event.pointerId;
		moveDusterToClientX(event.clientX, true);
	}

	function isPendingObject(object: FabricObjectLike | null) {
		if (!object) {
			return false;
		}
		const objectElementId = normalizeMessageID(toStringValue((object as Record<string, unknown>).elementId));
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
	}

	function cancelCurrentOperation() {
		cancelPendingOperation(true);
		if (activeTool !== 'select') {
			applyToolMode('select');
		}
		stopDusterDrag();
		showInsertMenu = false;
		showWidthMenu = false;
		contextMenuOpen = false;
		messagePickerOpen = false;
		showBoardDetails = false;
		showToolbarMore = false;
		pendingTapGesture = null;
	}

	function ensureObjectIdentity(object: FabricObjectLike, fallbackType = 'shape') {
		const currentElementId = normalizeMessageID(
			toStringValue((object as Record<string, unknown>).elementId)
		);
		const nextElementId = currentElementId || createMessageId(normalizedRoomId || 'board');
		const currentType = toStringValue((object as Record<string, unknown>).elementType).trim().toLowerCase();
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
			createdAt:
				parseOptionalTimestamp((object as Record<string, unknown>).createdAt) || Date.now()
		});
		return {
			elementId: nextElementId,
			elementType: nextType,
			createdByUserId: nextOwnerUserID,
			createdByName: nextOwnerName
		};
	}

	function emitBoardElementAdd(object: FabricObjectLike) {
		const element = boardObjectToElement(object);
		if (!element) {
			return;
		}
		sendBoardEnvelope('board_element_add', element);
	}

	function emitBoardElementMove(object: FabricObjectLike) {
		const element = boardObjectToElement(object);
		if (!element) {
			return;
		}
		const scaleX = toNumber((object as Record<string, unknown>).scaleX, 1);
		const scaleY = toNumber((object as Record<string, unknown>).scaleY, 1);
		sendBoardEnvelope('board_element_move', {
			elementId: element.elementId,
			x: element.x,
			y: element.y,
			width: element.width,
			height: element.height,
			scaleX,
			scaleY,
			zIndex: element.zIndex
		});
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

	function sendBoardEnvelope(type: BoardEventType, payload: Record<string, unknown>) {
		if (!normalizedRoomId || !canEdit) {
			return;
		}
		sendSocketPayload({
			type,
			roomId: normalizedRoomId,
			payload
		});
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
		const zIndex = toInt(fabricCanvas?.getObjects?.().indexOf(object as any) ?? 0);
		const createdAt =
			parseOptionalTimestamp((object as Record<string, unknown>).createdAt) || Date.now();

		let content = toStringValue((object as Record<string, unknown>).content);
		if (!content && elementType === 'stroke') {
			const strokePath = ((object as Record<string, unknown>).path as unknown[]) ?? [];
			content = serializePathCommands(strokePath);
		}
		if (!content && (elementType === 'line' || elementType === 'arrow')) {
			content = JSON.stringify({
				x1: toNumber((object as Record<string, unknown>).x1, left),
				y1: toNumber((object as Record<string, unknown>).y1, top),
				x2: toNumber((object as Record<string, unknown>).x2, left + width),
				y2: toNumber((object as Record<string, unknown>).y2, top + height)
			});
		}

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
		try {
			const res = await fetch(`${API_BASE}/api/rooms/${encodeURIComponent(normalizedTargetRoomId)}/board`);
			if (!res.ok) {
				throw new Error(`Load failed (${res.status})`);
			}
			const payload = (await res.json()) as unknown;
			const elements = Array.isArray(payload) ? payload : [];
			beginRemoteApply();
			clearBoardElements();
			for (const rawElement of elements) {
				const parsed = parseBoardElementRecord(rawElement);
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

	function clearBoardElements() {
		if (!fabricCanvas) {
			return;
		}
		const objects = [...(fabricCanvas.getObjects?.() ?? [])];
		for (const object of objects) {
			if (object === boardBoundsRect) {
				continue;
			}
			fabricCanvas.remove(object);
		}
		refreshBoardStats();
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
				record.createdByUserId ??
					record.created_by_user_id ??
					record.senderId ??
					record.sender_id
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

	async function createFabricObjectFromElement(element: BoardElementWire): Promise<FabricObjectLike | null> {
		const { elementType } = element;
		const strokeColor = isDarkMode ? '#f3f4f6' : '#111827';
		const fillColor = isDarkMode ? 'rgba(148, 163, 184, 0.16)' : 'rgba(30, 64, 175, 0.08)';

		if (elementType === 'stroke' && element.content) {
			const PathClass = getFabricClass('Path');
			if (!PathClass) {
				return null;
			}
			try {
				return new PathClass(element.content, {
					left: element.x,
					top: element.y,
					stroke: strokeColor,
					fill: '',
					strokeWidth: 2
				}) as FabricObjectLike;
			} catch {
				return null;
			}
		}

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
						stroke: strokeColor,
						strokeWidth: 2,
						fill: fillColor
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
				stroke: strokeColor,
				strokeWidth: 2,
				fill: fillColor
			}) as FabricObjectLike;
		}

		if (elementType === 'line' || elementType === 'arrow') {
			const LineClass = getFabricClass('Line');
			if (!LineClass) {
				return null;
			}
			const linePoints = parseLinePoints(element.content, element);
			return new LineClass(linePoints, {
				stroke: strokeColor,
				strokeWidth: elementType === 'arrow' ? 4 : 3
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
			const media = parseBoardMediaContent(element.content);
			const mediaObject = createMediaCardObject(media, element.x, element.y, element.width, element.height);
			if (mediaObject) {
				return mediaObject;
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
				name: raw.split('/').pop() ?? 'File',
				kind: 'file',
				mimeType: '',
				sizeBytes: 0
			};
		}
		try {
			const parsed = JSON.parse(raw) as Record<string, unknown>;
			const url = toStringValue(parsed.url).trim();
			if (!url) {
				return null;
			}
			return {
				url,
				name: toStringValue(parsed.name) || 'Attachment',
				kind: normalizeMediaKind(toStringValue(parsed.kind)),
				mimeType: toStringValue(parsed.mimeType ?? parsed.mime_type),
				sizeBytes: Math.max(0, toNumber(parsed.sizeBytes ?? parsed.size_bytes, 0))
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

	function createMediaCardObject(
		media: BoardMediaContent | null,
		left: number,
		top: number,
		explicitWidth = 0,
		explicitHeight = 0
	): FabricObjectLike | null {
		const TextboxClass = getFabricClass('Textbox') ?? getFabricClass('Text');
		if (!TextboxClass) {
			return null;
		}
		const baseWidth = Math.max(220, explicitWidth || getBoardCardWidth('media'));
		const nameLine = media?.name || media?.url || 'Attachment';
		const hostLine = media?.url ? safeHostFromURL(media.url) : '';
		const sizeLine = media?.sizeBytes ? formatFileSize(media.sizeBytes) : '';
		const title =
			media?.kind === 'video'
				? 'Video'
				: media?.kind === 'audio'
					? 'Audio'
					: media?.kind === 'image'
						? 'Image'
						: 'File';
		const cardText = [title, nameLine, hostLine, sizeLine, media?.url || '']
			.filter((entry) => entry !== '')
			.join('\n');
		const object = new TextboxClass(cardText, {
			left: clampBoardX(left, baseWidth),
			top: clampBoardY(top, explicitHeight > 0 ? explicitHeight : MIN_SHAPE_HEIGHT),
			width: baseWidth,
			fontSize: 13,
			lineHeight: 1.32,
			fill: isDarkMode ? '#e2e8f0' : '#1f2937',
			backgroundColor: isDarkMode ? '#172032' : '#ecf2fb',
			padding: 10
		}) as FabricObjectLike;
		if (explicitHeight > 0) {
			const rawHeight = Math.max(1, toNumber((object as Record<string, unknown>).height, explicitHeight));
			object.set?.({
				scaleY: explicitHeight / rawHeight
			});
		}
		return object;
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
				toNumber((object as Record<string, unknown>).width, loadedImage.naturalWidth || loadedImage.width || 1)
			);
			const rawHeight = Math.max(
				1,
				toNumber((object as Record<string, unknown>).height, loadedImage.naturalHeight || loadedImage.height || 1)
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
			const candidateId = normalizeMessageID(toStringValue((object as Record<string, unknown>).elementId));
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
		if (envelope.type === 'board_element_add') {
			const parsedElement = parseBoardElementRecord(envelope.payload);
			if (!parsedElement) {
				return;
			}
			void applyIncomingAdd(parsedElement);
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

	function parseBoardErrorPayload(value: unknown):
		| {
				roomId: string;
				code: string;
				message: string;
				elementId: string;
			}
		| null {
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
			type !== 'board_draw_progress' &&
			type !== 'board_element_add' &&
			type !== 'board_element_move' &&
			type !== 'board_element_delete'
		) {
			return null;
		}
		const roomIdFromEnvelope = normalizeRoomIDValue(
			toStringValue(record.roomId ?? record.room_id)
		);
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
			type === 'board_element_add' || type === 'board_element_move' || type === 'board_element_delete'
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
		return {
			elementId,
			x: toNumber(source.x, 0),
			y: toNumber(source.y, 0),
			scaleX: toNumber(source.scaleX ?? source.scale_x, 1),
			scaleY: toNumber(source.scaleY ?? source.scale_y, 1)
		};
	}

	function removeBoardObject(object: FabricObjectLike, emitDelete: boolean) {
		if (!fabricCanvas || object === boardBoundsRect) {
			return;
		}
		const elementId = normalizeMessageID(toStringValue((object as Record<string, unknown>).elementId));
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

	function recordLocalAction(action: LocalBoardAction) {
		if (isApplyingRemoteEvent || isRestoringHistory || isApplyingLocalAction) {
			return;
		}
		localUndoStack = [...localUndoStack, action].slice(-LOCAL_ACTION_LIMIT);
		localRedoStack = [];
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
			return;
		}
		const objects = fabricCanvas.getObjects?.() ?? [];
		boardElementCount = objects.filter((object: unknown) => object && object !== boardBoundsRect).length;
		const serialized = serializedSnapshot || serializeBoardSnapshot();
		boardApproxBytes = serialized ? UTF8_ENCODER.encode(serialized).length : 0;
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
			if (!canModerateBoardActions) {
				return;
			}
			event.preventDefault();
			contextMenuOpen = false;
			moveDusterToBoardX(boardPoint.x, true);
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
				return;
			}
			const target = tryResolveFabricTargetFromPointer(event);
			if (target && isPendingObject(target)) {
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

	function onBoardPointerMove(event: PointerEvent) {
		if (activeTool === 'duster') {
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
	}

	function openContextMenuAt(clientX: number, clientY: number, boardPoint: { x: number; y: number }) {
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
		isUploadingMedia = true;
		boardError = '';
		try {
			const uploaded = await uploadToR2(file, normalizedRoomId);
			const mediaPayload: BoardMediaContent = {
				url: uploaded.fileUrl,
				name: file.name || 'attachment',
				kind: inferMediaMessageType(file),
				mimeType: file.type || 'application/octet-stream',
				sizeBytes: file.size
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
		const snippet = extractMessageSnippet(message);
		insertMessageLikeObject(snippet, contextMenuPoint);
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

	function extractMessageSnippet(message: ChatMessage) {
		if (!message) {
			return '';
		}
		const text = toStringValue(message.content).trim();
		if (text) {
			return text.length > 240 ? `${text.slice(0, 237)}...` : text;
		}
		if (toStringValue(message.mediaUrl).trim()) {
			return `[${toStringValue(message.type) || 'media'}] ${toStringValue(message.mediaUrl)}`;
		}
		return `(message ${normalizeMessageID(message.id) || 'unknown'})`;
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
		<div class="board-toolbar">
			<button
				type="button"
				class="tool-icon-button"
				class:active={activeTool === 'draw'}
				on:click={() => toggleToolMode('draw')}
				title="Free draw"
				aria-label="Free draw"
			>
			<svg class="tool-icon" viewBox="0 0 24 24" aria-hidden="true">
				<path
					d="M4 16.8V20h3.2l9.4-9.4-3.2-3.2L4 16.8Zm14.7-8.7a.9.9 0 0 0 0-1.3l-1.5-1.5a.9.9 0 0 0-1.3 0l-1.2 1.2 3.2 3.2 1.2-1.2Z"
				/>
			</svg>
		</button>
		<div class="brush-width-wrap" bind:this={widthMenuWrapEl}>
			<button
				type="button"
				class="line-width-toggle"
				on:click={toggleWidthMenu}
				aria-haspopup="true"
				aria-expanded={showWidthMenu}
				title="Brush width"
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
			<button
				type="button"
				class="tool-icon-button"
				class:active={activeTool === 'eraser'}
				on:click={() => toggleToolMode('eraser')}
				title="Eraser"
				aria-label="Eraser"
				disabled={!canModerateBoardActions}
		>
			<svg class="tool-icon" viewBox="0 0 24 24" aria-hidden="true">
				<path
					d="M3.6 14.5 11.9 6a1.7 1.7 0 0 1 2.4 0l6.1 6.1a1.7 1.7 0 0 1 0 2.4l-4.1 4.1a1.7 1.7 0 0 1-1.2.5H8.8a1.7 1.7 0 0 1-1.2-.5l-4-4a1.7 1.7 0 0 1 0-2.4Zm7.7-6.8L5.2 13.8l3.5 3.5h3.1l5.2-5.2-5.7-5.4Z"
				/>
			</svg>
		</button>
			<button
				type="button"
				class="clear-tool-button"
				class:active={activeTool === 'duster'}
				on:click={() => toggleToolMode('duster')}
				title="Clear board duster"
				aria-label="Clear board duster"
				disabled={!canModerateBoardActions}
		>
			<svg class="tool-icon" viewBox="0 0 24 24" aria-hidden="true">
				<path d="M4 7.5h16v3H4z" />
				<path d="M7 11.5h10v7H7z" fill="none" stroke="currentColor" stroke-width="1.8" />
			</svg>
			<span>Clear</span>
		</button>
			<div class="insert-wrap" bind:this={insertWrapEl}>
			<button
				type="button"
				class="insert-toggle"
				class:active={showInsertMenu}
				on:click={toggleInsertMenu}
				aria-haspopup="true"
				aria-expanded={showInsertMenu}
				title="Insert shape"
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
						title="Line"
					>
						<svg class="tool-icon" viewBox="0 0 24 24" aria-hidden="true">
							<line x1="4" y1="18" x2="20" y2="6" stroke="currentColor" stroke-width="2.3" />
						</svg>
					</button>
					<button
						type="button"
						class="shape-icon-button"
						on:click={() => beginShapeInsert('arrow')}
						title="Arrow"
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
						title="Rect"
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
						on:click={() => beginShapeInsert('circle')}
						title="Circle"
					>
						<svg class="tool-icon" viewBox="0 0 24 24" aria-hidden="true">
							<circle cx="12" cy="12" r="6.5" fill="none" stroke="currentColor" stroke-width="2" />
						</svg>
					</button>
					</div>
				{/if}
			</div>
			<div class="board-details-wrap" bind:this={boardDetailsWrapEl}>
				<button
					type="button"
					class="details-toggle-button"
					class:active={showBoardDetails}
					on:click={toggleBoardDetails}
					title="Board details"
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
								{formatStorageBytes(boardApproxBytes)} / {formatStorageBytes(BOARD_STORAGE_LIMIT_BYTES)}
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
							<strong>{canManageAllBoardElements ? 'Admin full access' : 'Owner-only edits'}</strong>
						</div>
						<div class="board-detail-note">Drag empty board to pan. Double-tap empty space to attach.</div>
					</div>
				{/if}
			</div>
			<button
				type="button"
				class="toolbar-more-toggle"
				class:active={showToolbarMore}
				on:click={toggleToolbarMore}
				title="More board actions"
				aria-label="More board actions"
				aria-expanded={showToolbarMore}
			>
				{showToolbarMore ? 'Hide' : 'More'}
			</button>
			<div class="toolbar-overflow" class:open={showToolbarMore}>
				<button type="button" on:click={undo} disabled={!canModerateBoardActions || !canUndoLocalAction}>
					Undo
				</button>
				<button
					type="button"
					on:click={redo}
					disabled={!canModerateBoardActions || !canRedoLocalAction}
				>
					Redo
				</button>
				<button
					type="button"
					class="cancel-operation-button"
					disabled={!canCancelCurrentOperation}
					title="Cancel current operation"
					aria-label="Cancel current operation"
					on:click={cancelCurrentOperation}
				>
					×
				</button>
				{#if insertionHintLabel}
					<span class="insert-operation-hint">{insertionHintLabel}</span>
				{/if}
			</div>
		</div>

	<div
		class="board-canvas-shell"
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
		{#if activeTool === 'duster' && canModerateBoardActions}
			<div class="board-duster-layer" aria-hidden="true">
				<div
					class="board-duster-stripe"
					style={`left:${dusterScreenMetrics.left}px;top:${dusterScreenMetrics.top}px;width:${dusterScreenMetrics.width}px;height:${dusterScreenMetrics.height}px;`}
				></div>
				<button
					type="button"
					class="board-duster-handle"
					style={`left:${dusterScreenMetrics.handleLeft}px;top:${dusterScreenMetrics.handleTop}px;width:${DUSTER_HANDLE_WIDTH}px;height:${DUSTER_HANDLE_HEIGHT}px;`}
					on:pointerdown={onDusterHandlePointerDown}
					title="Drag to clear elements"
					aria-label="Drag duster handle to clear board elements"
				>
					<span>Drag</span>
				</button>
			</div>
		{/if}

		{#if boardLoading}
			<div class="board-overlay">Loading board...</div>
		{/if}
		{#if boardError}
			<div class="board-overlay error">{boardError}</div>
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
							<button type="button" class="message-picker-item" on:click={() => insertRoomMessage(message)}>
								<span class="author">{message.senderName || 'Guest'}</span>
								<span class="snippet">{extractMessageSnippet(message)}</span>
							</button>
						{/each}
					{/if}
				</div>
			</div>
		</div>
	{/if}

	<input
		bind:this={mediaInputEl}
		type="file"
		accept="image/*,video/*,audio/*,.pdf,.doc,.docx,.txt"
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
		border-radius: 10px;
		border: 1px solid var(--border-subtle);
		background: var(--bg-secondary);
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

	.insert-wrap {
		position: relative;
		display: inline-flex;
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
		z-index: 25;
		display: grid;
		grid-template-columns: repeat(4, minmax(0, 1fr));
		gap: 0.35rem;
		padding: 0.4rem;
		border-radius: 9px;
		border: 1px solid var(--border-subtle);
		background: var(--bg-secondary);
		box-shadow: 0 12px 24px rgba(0, 0, 0, 0.2);
	}

	.shape-icon-button {
		width: 34px;
		height: 34px;
		padding: 0;
		display: inline-flex;
		align-items: center;
		justify-content: center;
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

	.toolbar-more-toggle {
		display: none;
		min-width: 58px;
		height: 34px;
		padding: 0 0.6rem;
		border-radius: 8px;
		font-size: 0.78rem;
		font-weight: 700;
		line-height: 1;
		align-items: center;
		justify-content: center;
	}

	.toolbar-overflow {
		display: inline-flex;
		align-items: center;
		gap: 0.45rem;
		flex-wrap: wrap;
		margin-left: auto;
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

	.insert-operation-hint {
		font-size: 0.72rem;
		color: #fca5a5;
		border: 1px solid rgba(239, 68, 68, 0.35);
		background: rgba(127, 29, 29, 0.2);
		border-radius: 999px;
		padding: 0.2rem 0.52rem;
		white-space: nowrap;
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

	.board-canvas-shell :global(canvas) {
		touch-action: none;
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

	.board-duster-handle {
		position: absolute;
		transform: translateX(-50%);
		width: 56px;
		height: 34px;
		border-radius: 999px;
		border: 1px solid rgba(248, 113, 113, 0.85);
		background: rgba(127, 29, 29, 0.92);
		color: #fee2e2;
		font-size: 0.68rem;
		font-weight: 700;
		letter-spacing: 0.02em;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		pointer-events: auto;
		touch-action: none;
		cursor: ew-resize;
		box-shadow: 0 6px 18px rgba(15, 23, 42, 0.45);
	}

	.board-duster-handle:hover {
		background: rgba(153, 27, 27, 0.96);
	}

	.board-duster-handle::after {
		content: '';
		position: absolute;
		top: 100%;
		left: 50%;
		transform: translateX(-50%);
		width: 2px;
		height: 14px;
		background: rgba(248, 113, 113, 0.82);
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
		gap: 0.4rem;
		overflow: auto;
		padding-right: 0.15rem;
	}

	.message-picker-item {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
		text-align: left;
		border: 1px solid var(--border-subtle);
		background: var(--bg-primary);
		border-radius: 8px;
		padding: 0.56rem 0.65rem;
		cursor: pointer;
	}

	.message-picker-item:hover {
		background: var(--bg-tertiary);
	}

	.author {
		font-size: 0.77rem;
		font-weight: 700;
		color: var(--text-muted);
	}

	.snippet {
		font-size: 0.84rem;
		color: var(--text-main);
	}

	.empty-state {
		font-size: 0.82rem;
		color: var(--text-muted);
		padding: 0.65rem 0.2rem;
	}

	.hidden-input {
		display: none;
	}

	@media (max-width: 1200px) {
		.board-root {
			padding: 0.45rem;
		}

		.toolbar-more-toggle {
			display: inline-flex;
			margin-left: auto;
		}

		.toolbar-overflow {
			display: none;
			width: 100%;
			order: 20;
			margin-left: 0;
		}

		.toolbar-overflow.open {
			display: flex;
		}

		.toolbar-overflow {
			justify-content: flex-start;
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

	}
</style>
