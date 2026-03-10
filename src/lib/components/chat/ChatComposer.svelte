<script lang="ts">
	import IconSet from '$lib/components/icons/IconSet.svelte';
	import AiDisclaimerModal from '$lib/components/chat/AiDisclaimerModal.svelte';
	import { APP_LIMITS } from '$lib/config/limits';
	import { getUTF8ByteLength, MESSAGE_TEXT_MAX_BYTES } from '$lib/utils/chat/core';
	import {
		compressMedia,
		inferMediaMessageType,
		uploadToR2,
		type MediaMessageType
	} from '$lib/utils/media';
	import type { ComposerMediaPayload, ReplyTarget, TaskChecklistItem } from '$lib/types/chat';
	import { stringifyTaskMessagePayload } from '$lib/utils/chat/task';
	import { buildBeaconMessagePayload, formatBeaconTimestamp } from '$lib/utils/chat/beacon';
	import { createEventDispatcher, onDestroy, onMount } from 'svelte';
	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';
	const AI_TERMS_STORAGE_KEY = 'hasAcceptedAITerms';
	const AI_PRIVACY_POLICY_URL = 'https://example.com/privacy-policy';
	const AI_PRIMARY_MENTION = '@ToraAI';
	const AI_MENTION_TOKENS = ['@ToraAI', '@Tora'];
	const AI_DISPLAY_NAME = 'ToraAI';
	const KLIPY_API_KEY_RAW = import.meta.env.VITE_KLIPY_API_KEY as string | undefined;
	const KLIPY_API_KEY = KLIPY_API_KEY_RAW?.trim() ?? '';
	const KLIPY_API_BASE = 'https://api.klipy.com';
	const KLIPY_CLIENT_KEY = 'converse-web';
	const KLIPY_SEARCH_LIMIT = APP_LIMITS.composer.klipySearchLimit;
	const KLIPY_DEFAULT_LOCALE = 'us';
	const KLIPY_CONTENT_FILTER = 'medium';
	const KLIPY_AD_MIN_WIDTH = String(APP_LIMITS.composer.klipyAdMinWidth);
	const KLIPY_AD_MAX_WIDTH = String(APP_LIMITS.composer.klipyAdMaxWidth);
	const KLIPY_AD_MIN_HEIGHT = String(APP_LIMITS.composer.klipyAdMinHeight);
	const KLIPY_AD_MAX_HEIGHT = String(APP_LIMITS.composer.klipyAdMaxHeight);
	const KLIPY_AD_INSERT_POSITIONS = [1, 4] as const;
	const COMPOSER_MAX_VISIBLE_LINES = APP_LIMITS.composer.maxVisibleLines;
	const COMMON_EMOJIS = [
		'😊',
		'😀',
		'😁',
		'😂',
		'🤣',
		'😍',
		'🥳',
		'😎',
		'🤔',
		'😴',
		'😅',
		'😭',
		'😡',
		'🙏',
		'👏',
		'🙌',
		'👍',
		'👎',
		'👌',
		'🤝',
		'🔥',
		'✨',
		'💯',
		'🚀',
		'🎉',
		'❤️',
		'💙',
		'💚',
		'💛',
		'👀',
		'✅',
		'🤖'
	];

	type MediaAssetKind = 'gif' | 'sticker' | 'meme';
	type MediaPickerTab = 'emoji' | 'gif' | 'sticker' | 'meme';

	type MediaAssetResult = {
		id: string;
		url: string;
		previewUrl: string;
		title: string;
		kind: MediaAssetKind;
		isAd?: boolean;
		adContent?: string;
	};

	type MentionOption = {
		id: string;
		label: string;
		insertValue: string;
		isAI?: boolean;
	};

	type ComposerTextSegment = {
		value: string;
		isMention: boolean;
	};

	type PendingAIAction = 'send' | 'open-private-ai' | null;

	const COMPOSER_MENTION_TOKEN_PATTERN = /(^|[^A-Za-z0-9_])(@[A-Za-z0-9_.-]{1,32})/g;

	export let draftMessage = '';
	export let attachedFile: File | null = null;
	export let activeReply: ReplyTarget | null = null;
	export let isDarkMode = false;
	export let messageLimit = MESSAGE_TEXT_MAX_BYTES;
	export let currentUsername = 'You';
	export let roomId = '';
	export let aiEnabled = true;
	export let disabled = false;
	export let isEphemeralRoom = false;
	export let mentionCandidates: string[] = [];

	let mediaInput: HTMLInputElement | null = null;
	let fileInput: HTMLInputElement | null = null;
	let showAttachMenu = false;
	let attachError = '';
	let isProcessingAttachment = false;
	let attachedMessageType: MediaMessageType | null = null;
	let attachedPickerType: 'media' | 'file' = 'file';
	let attachmentPreviewURL = '';
	let attachWrapEl: HTMLDivElement | null = null;
	let mediaPickerEl: HTMLDivElement | null = null;
	let mediaPickerWrapEl: HTMLDivElement | null = null;
	let mentionPickerEl: HTMLDivElement | null = null;
	let composerTextareaEl: HTMLTextAreaElement | null = null;
	let composerHighlightEl: HTMLDivElement | null = null;
	let normalizedDraftMessage = '';
	let draftMessageBytes = 0;
	let composerMentionSegments: ComposerTextSegment[] = [];
	let taskDraftOpen = false;
	let taskDraftTitle = '';
	let taskDraftItems: TaskChecklistItem[] = [];
	let taskNewItemText = '';
	let taskAddInputOpen = false;
	let taskDraftError = '';
	let beaconDraftOpen = false;
	let beaconDraftDate = '';
	let beaconDraftTime = '';
	let beaconDraftText = '';
	let beaconDraftError = '';
	let isRecording = false;
	let mediaRecorder: MediaRecorder | null = null;
	let audioChunks: Blob[] = [];
	let recordingStream: MediaStream | null = null;
	let showMediaPicker = false;
	let activeMediaTab: MediaPickerTab = 'emoji';
	let mediaQuery = '';
	let gifResults: MediaAssetResult[] = [];
	let stickerResults: MediaAssetResult[] = [];
	let memeResults: MediaAssetResult[] = [];
	let gifLoading = false;
	let stickerLoading = false;
	let memeLoading = false;
	let gifError = '';
	let stickerError = '';
	let memeError = '';
	let mediaSearchTimer: ReturnType<typeof setTimeout> | null = null;
	let mediaAbortController: AbortController | null = null;
	let attachedMediaAsset: MediaAssetResult | null = null;
	let hasAcceptedAITerms = false;
	let showAIDisclaimerModal = false;
	let pendingAIAction: PendingAIAction = null;
	let showMentionPicker = false;
	let mentionOptions: MentionOption[] = [];
	let mentionActiveIndex = 0;
	let mentionReplaceStart = 0;
	let mentionReplaceEnd = 0;

	$: normalizedDraftMessage = draftMessage.trim();
	$: hasComposerInput = draftMessage.length > 0;
	$: composerMentionSegments = splitComposerTextByMention(draftMessage);
	$: draftMessageBytes = getUTF8ByteLength(normalizedDraftMessage);
	$: isOverMessageLimit = draftMessageBytes > messageLimit;
	$: overLimitBy = Math.max(0, draftMessageBytes - messageLimit);
	$: taskDraftReady = taskDraftOpen && taskDraftTitle.trim() !== '' && taskDraftItems.length > 0;
	$: beaconDraftReady =
		beaconDraftOpen &&
		beaconDraftText.trim() !== '' &&
		beaconDraftDate.trim() !== '' &&
		beaconDraftTime.trim() !== '';
	$: beaconDraftDateTimeValue =
		beaconDraftDate && beaconDraftTime ? new Date(`${beaconDraftDate}T${beaconDraftTime}`) : null;
	$: beaconDraftTimestamp =
		beaconDraftDateTimeValue instanceof Date && !Number.isNaN(beaconDraftDateTimeValue.getTime())
			? beaconDraftDateTimeValue.getTime()
			: 0;
	$: beaconDraftLabel = beaconDraftTimestamp > 0 ? formatBeaconTimestamp(beaconDraftTimestamp) : '';
	$: hasPendingAttachment = Boolean(attachedFile || attachedMediaAsset);
	$: activeMediaResults =
		activeMediaTab === 'gif'
			? gifResults
			: activeMediaTab === 'sticker'
				? stickerResults
				: activeMediaTab === 'meme'
					? memeResults
					: [];
	$: activeMediaLoading =
		activeMediaTab === 'gif'
			? gifLoading
			: activeMediaTab === 'sticker'
				? stickerLoading
				: activeMediaTab === 'meme'
					? memeLoading
					: false;
	$: activeMediaError =
		activeMediaTab === 'gif'
			? gifError
			: activeMediaTab === 'sticker'
				? stickerError
				: activeMediaTab === 'meme'
					? memeError
					: '';
	$: activeMediaSearchPlaceholder =
		activeMediaTab === 'gif'
			? 'Search GIFs'
			: activeMediaTab === 'sticker'
				? 'Search stickers'
				: 'Search memes';
	$: showSendButton =
		!isRecording && !taskDraftOpen && (hasPendingAttachment || normalizedDraftMessage.length > 0);
	$: composerDisabled =
		disabled || isProcessingAttachment || isRecording || taskDraftOpen || beaconDraftOpen;
	$: composerPlaceholder = disabled
		? 'This room has expired. Extend time to continue chatting.'
		: isRecording
			? 'Recording... Click mic to send.'
			: taskDraftOpen
				? 'Task mode active. Press send when ready.'
				: hasPendingAttachment
					? 'Add a caption (optional)'
					: 'Type a message';

	const dispatch = createEventDispatcher<{
		send: ComposerMediaPayload | undefined;
		attach: { file: File | null; type: 'media' | 'file'; error?: string };
		removeAttachment: void;
		cancelReply: void;
		typing: { value: string };
		openPrivateAi: void;
	}>();

	function closeMediaPicker(resetQuery = false) {
		showMediaPicker = false;
		if (mediaSearchTimer) {
			clearTimeout(mediaSearchTimer);
			mediaSearchTimer = null;
		}
		mediaAbortController?.abort();
		mediaAbortController = null;
		gifLoading = false;
		stickerLoading = false;
		memeLoading = false;
		if (resetQuery) {
			mediaQuery = '';
		}
	}

	function emitTypingValue(nextValue: string = draftMessage) {
		if (disabled) {
			return;
		}
		dispatch('typing', { value: nextValue });
	}

	function loadHasAcceptedAITerms() {
		if (typeof window === 'undefined') {
			return false;
		}
		const raw = window.localStorage.getItem(AI_TERMS_STORAGE_KEY);
		const normalized = (raw || '').trim().toLowerCase();
		return normalized === 'true' || normalized === '1' || normalized === 'yes';
	}

	function persistHasAcceptedAITerms(value: boolean) {
		if (typeof window === 'undefined') {
			return;
		}
		window.localStorage.setItem(AI_TERMS_STORAGE_KEY, value ? 'true' : 'false');
	}

	function closeMentionPicker() {
		showMentionPicker = false;
		mentionOptions = [];
		mentionActiveIndex = 0;
	}

	function splitComposerTextByMention(value: string): ComposerTextSegment[] {
		const source = value || '';
		if (!source) {
			return [];
		}
		const segments: ComposerTextSegment[] = [];
		COMPOSER_MENTION_TOKEN_PATTERN.lastIndex = 0;
		let cursor = 0;
		let match = COMPOSER_MENTION_TOKEN_PATTERN.exec(source);
		while (match) {
			const matchIndex = match.index ?? -1;
			const fullValue = match[0] || '';
			const prefix = match[1] || '';
			const mention = match[2] || '';
			if (matchIndex >= 0 && fullValue) {
				if (matchIndex > cursor) {
					segments.push({ value: source.slice(cursor, matchIndex), isMention: false });
				}
				if (prefix) {
					segments.push({ value: prefix, isMention: false });
				}
				if (mention) {
					segments.push({ value: mention, isMention: true });
				}
				cursor = matchIndex + fullValue.length;
			}
			match = COMPOSER_MENTION_TOKEN_PATTERN.exec(source);
		}
		if (cursor < source.length) {
			segments.push({ value: source.slice(cursor), isMention: false });
		}
		if (segments.length === 0) {
			return [{ value: source, isMention: false }];
		}
		return segments;
	}

	function syncComposerHighlightScroll() {
		if (!composerTextareaEl || !composerHighlightEl) {
			return;
		}
		composerHighlightEl.scrollTop = composerTextareaEl.scrollTop;
		composerHighlightEl.scrollLeft = composerTextareaEl.scrollLeft;
	}

	function parsePixel(value: string) {
		const parsed = Number.parseFloat(value);
		return Number.isFinite(parsed) ? parsed : 0;
	}

	function resizeComposerTextarea() {
		if (!composerTextareaEl || typeof window === 'undefined') {
			return;
		}
		const styles = window.getComputedStyle(composerTextareaEl);
		const lineHeight = parsePixel(styles.lineHeight) || 19;
		const verticalPadding = parsePixel(styles.paddingTop) + parsePixel(styles.paddingBottom);
		const verticalBorder = parsePixel(styles.borderTopWidth) + parsePixel(styles.borderBottomWidth);
		const minHeight = lineHeight + verticalPadding + verticalBorder;
		const maxHeight = lineHeight * COMPOSER_MAX_VISIBLE_LINES + verticalPadding + verticalBorder;

		composerTextareaEl.style.height = 'auto';
		const nextHeight = Math.max(minHeight, Math.min(composerTextareaEl.scrollHeight, maxHeight));
		composerTextareaEl.style.height = `${nextHeight}px`;
		composerTextareaEl.style.overflowY =
			composerTextareaEl.scrollHeight > maxHeight ? 'auto' : 'hidden';
		syncComposerHighlightScroll();
	}

	function textUsesAI(text: string) {
		for (const token of AI_MENTION_TOKENS) {
			if (text.includes(token)) {
				return true;
			}
		}
		return false;
	}

	function normalizeMentionCandidateValues() {
		const seen = new Set<string>();
		const values: string[] = [];
		for (const candidate of mentionCandidates) {
			const name = (candidate || '').trim();
			if (!name) {
				continue;
			}
			const key = name.toLowerCase();
			if (seen.has(key)) {
				continue;
			}
			seen.add(key);
			values.push(name);
		}
		return values;
	}

	function buildMentionOptions(query: string) {
		const normalizedQuery = query.toLowerCase();
		const options: MentionOption[] = [];
		if (aiEnabled) {
			const aiMatches =
				normalizedQuery === '' ||
				AI_DISPLAY_NAME.toLowerCase().includes(normalizedQuery) ||
				'tora'.includes(normalizedQuery);
			if (aiMatches) {
				options.push({
					id: 'ai_tora',
					label: AI_DISPLAY_NAME,
					insertValue: AI_PRIMARY_MENTION,
					isAI: true
				});
			}
		}

		for (const name of normalizeMentionCandidateValues()) {
			if (name.toLowerCase() === AI_DISPLAY_NAME.toLowerCase()) {
				continue;
			}
			if (normalizedQuery !== '' && !name.toLowerCase().includes(normalizedQuery)) {
				continue;
			}
			options.push({
				id: `user_${name.toLowerCase()}`,
				label: name,
				insertValue: `@${name}`
			});
		}

		return options.slice(0, 8);
	}

	function updateMentionSuggestionsFromCaret() {
		if (!composerTextareaEl) {
			closeMentionPicker();
			return;
		}
		const value = draftMessage || '';
		const caret = composerTextareaEl.selectionStart ?? value.length;
		const beforeCaret = value.slice(0, caret);
		const match = beforeCaret.match(/(?:^|\s)@([A-Za-z0-9_.-]{0,32})$/);
		if (!match) {
			closeMentionPicker();
			return;
		}

		const atIndex = beforeCaret.lastIndexOf('@');
		if (atIndex < 0) {
			closeMentionPicker();
			return;
		}
		const query = match[1] || '';
		const options = buildMentionOptions(query);
		if (options.length === 0) {
			closeMentionPicker();
			return;
		}

		showMentionPicker = true;
		mentionOptions = options;
		mentionReplaceStart = atIndex;
		mentionReplaceEnd = caret;
		mentionActiveIndex = Math.max(0, Math.min(mentionActiveIndex, options.length - 1));
	}

	function selectMentionOption(option: MentionOption) {
		if (!option || !composerTextareaEl) {
			closeMentionPicker();
			return;
		}
		const value = draftMessage || '';
		const replacement = `${option.insertValue} `;
		const nextValue =
			value.slice(0, mentionReplaceStart) + replacement + value.slice(mentionReplaceEnd);
		draftMessage = nextValue;
		const nextCursor = mentionReplaceStart + replacement.length;
		requestAnimationFrame(() => {
			if (!composerTextareaEl) {
				return;
			}
			composerTextareaEl.focus();
			composerTextareaEl.setSelectionRange(nextCursor, nextCursor);
			syncComposerHighlightScroll();
		});
		closeMentionPicker();
		emitTypingValue();
	}

	function requiresAITermsForCurrentSend() {
		if (!aiEnabled) {
			return false;
		}
		if (taskDraftOpen) {
			return false;
		}
		const textToSend = (draftMessage || '').trim();
		if (attachedMediaAsset) {
			return textUsesAI(textToSend);
		}
		if (attachedFile) {
			return false;
		}
		return textUsesAI(textToSend);
	}

	function requestAITermsAcceptance(nextAction: Exclude<PendingAIAction, null>) {
		pendingAIAction = nextAction;
		showAIDisclaimerModal = true;
		showAttachMenu = false;
		closeMediaPicker();
	}

	function onAIButtonClick() {
		if (composerDisabled || !aiEnabled) {
			return;
		}
		closeMentionPicker();
		showAttachMenu = false;
		closeMediaPicker();
		if (!hasAcceptedAITerms) {
			requestAITermsAcceptance('open-private-ai');
			return;
		}
		dispatch('openPrivateAi');
	}

	function onAIDisclaimerCancel() {
		showAIDisclaimerModal = false;
		pendingAIAction = null;
	}

	function onAIDisclaimerAgree() {
		hasAcceptedAITerms = true;
		persistHasAcceptedAITerms(true);
		showAIDisclaimerModal = false;
		const action = pendingAIAction;
		pendingAIAction = null;
		if (action === 'open-private-ai') {
			dispatch('openPrivateAi');
			return;
		}
		if (action === 'send') {
			onSend();
		}
	}

	onDestroy(() => {
		clearAttachmentPreview();
		closeMediaPicker();
		closeMentionPicker();
		clearBeaconDraft();
		if (isRecording && mediaRecorder && mediaRecorder.state !== 'inactive') {
			mediaRecorder.stop();
		}
		stopRecordingStream();
	});

	onMount(() => {
		hasAcceptedAITerms = loadHasAcceptedAITerms();
		requestAnimationFrame(() => {
			resizeComposerTextarea();
			syncComposerHighlightScroll();
		});

		const onDocumentPointerDown = (event: PointerEvent) => {
			const target = event.target;
			if (!(target instanceof Node)) {
				return;
			}
			if (showAttachMenu && attachWrapEl && !attachWrapEl.contains(target)) {
				showAttachMenu = false;
			}
			if (
				showMediaPicker &&
				(!mediaPickerWrapEl || !mediaPickerWrapEl.contains(target)) &&
				(!mediaPickerEl || !mediaPickerEl.contains(target))
			) {
				closeMediaPicker();
			}
			if (showMentionPicker && mentionPickerEl && !mentionPickerEl.contains(target)) {
				closeMentionPicker();
			}
		};

		window.addEventListener('pointerdown', onDocumentPointerDown);
		return () => {
			window.removeEventListener('pointerdown', onDocumentPointerDown);
		};
	});

	function toggleAttachMenu() {
		if (disabled) {
			return;
		}
		closeMentionPicker();
		if (showMediaPicker) {
			closeMediaPicker();
		}
		showAttachMenu = !showAttachMenu;
	}

	function fetchActiveMediaTab(query = '') {
		if (activeMediaTab === 'gif') {
			return fetchKlipyGifs(query);
		}
		if (activeMediaTab === 'sticker') {
			return fetchKlipyStickers(query);
		}
		if (activeMediaTab === 'meme') {
			return fetchKlipyMemes(query);
		}
		return Promise.resolve();
	}

	function getMediaResultsByTab(tab: MediaPickerTab) {
		if (tab === 'gif') {
			return gifResults;
		}
		if (tab === 'sticker') {
			return stickerResults;
		}
		if (tab === 'meme') {
			return memeResults;
		}
		return [];
	}

	function openMediaPicker(tab: MediaPickerTab = activeMediaTab) {
		if (composerDisabled) {
			return;
		}
		closeMentionPicker();
		showAttachMenu = false;
		activeMediaTab = tab;
		showMediaPicker = true;
		if (tab !== 'emoji' && getMediaResultsByTab(tab).length === 0) {
			void fetchActiveMediaTab();
		}
	}

	function toggleMediaPicker() {
		if (showMediaPicker) {
			closeMediaPicker();
			return;
		}
		openMediaPicker(activeMediaTab);
	}

	function switchMediaTab(tab: MediaPickerTab) {
		if (tab === activeMediaTab) {
			return;
		}
		activeMediaTab = tab;
		mediaQuery = '';
		if (tab !== 'emoji' && getMediaResultsByTab(tab).length === 0) {
			void fetchActiveMediaTab('');
		}
	}

	function insertEmoji(emoji: string) {
		if (composerDisabled) {
			return;
		}
		const normalizedEmoji = (emoji || '').trim();
		if (!normalizedEmoji) {
			return;
		}
		const currentValue = draftMessage || '';
		if (!composerTextareaEl) {
			draftMessage = `${currentValue}${normalizedEmoji}`;
			emitTypingValue();
			return;
		}

		const selectionStart = composerTextareaEl.selectionStart ?? currentValue.length;
		const selectionEnd = composerTextareaEl.selectionEnd ?? currentValue.length;
		draftMessage =
			currentValue.slice(0, selectionStart) + normalizedEmoji + currentValue.slice(selectionEnd);

		const nextCaretPosition = selectionStart + normalizedEmoji.length;
		requestAnimationFrame(() => {
			if (!composerTextareaEl) {
				return;
			}
			composerTextareaEl.focus();
			composerTextareaEl.setSelectionRange(nextCaretPosition, nextCaretPosition);
		});
		emitTypingValue();
	}

	function chooseAttachmentType(type: 'media' | 'file' | 'task' | 'beacon') {
		if (disabled) {
			return;
		}
		if (type === 'beacon' && !isEphemeralRoom) {
			return;
		}
		closeMentionPicker();
		showAttachMenu = false;
		closeMediaPicker();
		attachError = '';
		taskDraftError = '';
		gifError = '';
		stickerError = '';
		memeError = '';
		if (type === 'task') {
			clearAttachmentPreview();
			attachedFile = null;
			attachedMediaAsset = null;
			attachedMessageType = null;
			dispatch('attach', { file: null, type: 'file' });
			clearBeaconDraft();
			taskDraftOpen = true;
			taskAddInputOpen = false;
			if (taskDraftTitle.trim() === '') {
				taskDraftTitle = 'Task';
			}
			return;
		}
		if (type === 'beacon') {
			clearAttachmentPreview();
			attachedFile = null;
			attachedMediaAsset = null;
			attachedMessageType = null;
			dispatch('attach', { file: null, type: 'file' });
			clearTaskDraft();
			openBeaconDraft();
			return;
		}
		taskDraftOpen = false;
		taskAddInputOpen = false;
		if (type === 'media') {
			mediaInput?.click();
			return;
		}
		fileInput?.click();
	}

	function resolveMessageType(file: File, pickerType: 'media' | 'file'): MediaMessageType {
		if (pickerType === 'file') {
			if (file.type.startsWith('image/')) {
				return 'image';
			}
			if (file.type.startsWith('video/')) {
				return 'video';
			}
			if (file.type.startsWith('audio/')) {
				return 'audio';
			}
			return 'file';
		}
		return inferMediaMessageType(file);
	}

	function toRecord(value: unknown): Record<string, unknown> | null {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return null;
		}
		return value as Record<string, unknown>;
	}

	function toTrimmedString(value: unknown) {
		return typeof value === 'string' ? value.trim() : '';
	}

	function toScalarString(value: unknown) {
		if (typeof value === 'string') {
			return value.trim();
		}
		if (typeof value === 'number' || typeof value === 'bigint') {
			return String(value);
		}
		return '';
	}

	function getKlipyLocale() {
		if (typeof navigator === 'undefined') {
			return KLIPY_DEFAULT_LOCALE;
		}
		const language = (navigator.language || '').trim().toLowerCase();
		if (!language) {
			return KLIPY_DEFAULT_LOCALE;
		}
		const parts = language.split('-').filter(Boolean);
		const countryCode = (parts[1] || parts[0] || KLIPY_DEFAULT_LOCALE).slice(0, 2);
		return countryCode || KLIPY_DEFAULT_LOCALE;
	}

	function readMediaUrl(formats: Record<string, unknown> | null, keys: string[]) {
		if (!formats) {
			return '';
		}
		for (const key of keys) {
			const entry = formats[key];
			if (!entry) {
				continue;
			}
			if (typeof entry === 'string') {
				const direct = entry.trim();
				if (direct) {
					return direct;
				}
				continue;
			}
			const record = toRecord(entry);
			const url = toTrimmedString(record?.url);
			if (url) {
				return url;
			}
		}
		return '';
	}

	function readKlipyFileUrl(
		fileRecord: Record<string, unknown> | null,
		preferredFormats: string[]
	) {
		if (!fileRecord) {
			return '';
		}
		const variantKeys = ['hd', 'md', 'sd', 'sm', 'xs', 'tiny', 'preview', 'original'];
		for (const variantKey of variantKeys) {
			const variant = toRecord(fileRecord[variantKey]);
			if (!variant) {
				continue;
			}
			const directVariantUrl = toTrimmedString(variant.url);
			if (directVariantUrl) {
				return directVariantUrl;
			}
			for (const format of preferredFormats) {
				const formatRecord = toRecord(variant[format]);
				const formatUrl = toTrimmedString(formatRecord?.url);
				if (formatUrl) {
					return formatUrl;
				}
			}
		}

		for (const format of preferredFormats) {
			const formatRecord = toRecord(fileRecord[format]);
			const formatUrl = toTrimmedString(formatRecord?.url);
			if (formatUrl) {
				return formatUrl;
			}
		}
		return '';
	}

	function applyKlipyAdParams(params: URLSearchParams) {
		params.set('ad-min-width', KLIPY_AD_MIN_WIDTH);
		params.set('ad-max-width', KLIPY_AD_MAX_WIDTH);
		params.set('ad-min-height', KLIPY_AD_MIN_HEIGHT);
		params.set('ad-max-height', KLIPY_AD_MAX_HEIGHT);
	}

	function placeKlipyAds(items: MediaAssetResult[]) {
		const adItems = items.filter((asset) => asset.isAd && (asset.adContent || '').trim() !== '');
		if (adItems.length === 0) {
			return items;
		}
		const mediaItems = items.filter((asset) => !asset.isAd);
		if (mediaItems.length === 0) {
			return adItems;
		}
		const orderedItems = [...mediaItems];
		for (let index = 0; index < adItems.length; index += 1) {
			const targetIndex = KLIPY_AD_INSERT_POSITIONS[index] ?? orderedItems.length;
			const boundedIndex = Math.max(0, Math.min(targetIndex, orderedItems.length));
			orderedItems.splice(boundedIndex, 0, adItems[index]);
		}
		return orderedItems;
	}

	function parseKlipyMediaResults(payload: unknown, kind: MediaAssetKind): MediaAssetResult[] {
		const source = toRecord(payload);
		if (!source) {
			return [];
		}
		const nestedData = toRecord(source.data);
		const entriesRaw = Array.isArray(source.results)
			? source.results
			: Array.isArray(source.data)
				? source.data
				: Array.isArray(nestedData?.data)
					? nestedData.data
					: Array.isArray(nestedData?.results)
						? nestedData.results
						: Array.isArray(nestedData?.items)
							? nestedData.items
							: Array.isArray(source.items)
								? source.items
								: Array.isArray(source.gifs)
									? source.gifs
									: Array.isArray(source.stickers)
										? source.stickers
										: Array.isArray(source.memes)
											? source.memes
											: [];
		const items: MediaAssetResult[] = [];
		for (let index = 0; index < entriesRaw.length; index += 1) {
			const entry = toRecord(entriesRaw[index]);
			if (!entry) {
				continue;
			}
			const entryType = toTrimmedString(entry.type).toLowerCase();
			if (entryType === 'ad') {
				const adContent = toTrimmedString(entry.content);
				if (!adContent) {
					continue;
				}
				items.push({
					id: `klipy_ad_${Date.now()}_${index}`,
					url: '',
					previewUrl: '',
					title: 'Advertisement',
					kind,
					isAd: true,
					adContent
				});
				continue;
			}
			const mediaFormats = toRecord(entry.media_formats) ?? toRecord(entry.mediaFormats);
			const images = toRecord(entry.images) ?? toRecord(entry.image);
			const fileRecord = toRecord(entry.file);
			const previewFromMediaFormats = readMediaUrl(mediaFormats, [
				'tinygif',
				'nanogif',
				'tinywebp',
				'nanowebp',
				'previewgif',
				'previewwebp',
				'tinypng',
				'thumbnail',
				'preview'
			]);
			const mediaFromMediaFormats = readMediaUrl(mediaFormats, [
				'gif',
				'mediumgif',
				'fullgif',
				'largegif',
				'webp',
				'mediumwebp',
				'largewebp',
				'png',
				'jpg',
				'original'
			]);
			const previewFromImages = readMediaUrl(images, [
				'preview_gif',
				'preview_webp',
				'thumbnail',
				'thumb',
				'fixed_width_small',
				'downsized_small',
				'preview',
				'tiny'
			]);
			const mediaFromImages = readMediaUrl(images, [
				'original',
				'original_webp',
				'downsized_large',
				'downsized',
				'fixed_width',
				'image',
				'webp',
				'gif'
			]);
			const previewFromFile = readKlipyFileUrl(fileRecord, ['webp', 'jpg', 'png', 'gif']);
			const mediaFromFile = readKlipyFileUrl(fileRecord, ['gif', 'webp', 'jpg', 'png']);
			const directPreview =
				toTrimmedString(entry.preview_url) ||
				toTrimmedString(entry.thumbnail_url) ||
				toTrimmedString(entry.thumb_url);
			const directMedia =
				toTrimmedString(entry.url) ||
				toTrimmedString(entry.gif_url) ||
				toTrimmedString(entry.image_url) ||
				toTrimmedString(entry.webp_url) ||
				toTrimmedString(entry.media_url);
			const previewUrl =
				previewFromMediaFormats ||
				previewFromImages ||
				previewFromFile ||
				directPreview ||
				directMedia;
			const url =
				mediaFromMediaFormats || mediaFromImages || mediaFromFile || directMedia || previewUrl;
			if (!url) {
				continue;
			}
			const id =
				toScalarString(entry.id) ||
				toScalarString(entry.gif_id) ||
				toTrimmedString(entry.slug) ||
				`${kind}_${Date.now()}_${index}`;
			const fallbackTitle = kind === 'gif' ? 'GIF' : kind === 'sticker' ? 'Sticker' : 'Meme';
			const title =
				toTrimmedString(entry.content_description) ||
				toTrimmedString(entry.title) ||
				toTrimmedString(entry.alt_text) ||
				fallbackTitle;
			items.push({
				id,
				url,
				previewUrl: previewUrl || url,
				title,
				kind
			});
		}
		return placeKlipyAds(items);
	}

	async function fetchKlipyGifs(query: string) {
		if (!KLIPY_API_KEY) {
			gifError = 'GIF search is unavailable. Add VITE_KLIPY_API_KEY to enable it.';
			gifResults = [];
			return;
		}
		mediaAbortController?.abort();
		mediaAbortController = new AbortController();
		gifLoading = true;
		gifError = '';
		try {
			const endpointPath = query ? '/v2/search' : '/v2/featured';
			const params = new URLSearchParams({
				key: KLIPY_API_KEY,
				client_key: KLIPY_CLIENT_KEY,
				limit: String(KLIPY_SEARCH_LIMIT),
				media_filter: 'tinygif,gif',
				contentfilter: 'medium'
			});
			applyKlipyAdParams(params);
			if (query) {
				params.set('q', query);
			}
			const response = await fetch(`${KLIPY_API_BASE}${endpointPath}?${params.toString()}`, {
				signal: mediaAbortController.signal
			});
			const payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
			if (!response.ok) {
				throw new Error(
					typeof payload.error === 'string'
						? payload.error
						: `GIF request failed (${response.status})`
				);
			}
			gifResults = parseKlipyMediaResults(payload, 'gif');
		} catch (error) {
			const isAbortError =
				typeof error === 'object' &&
				error !== null &&
				'name' in error &&
				(error as { name?: string }).name === 'AbortError';
			if (isAbortError) {
				return;
			}
			gifError = error instanceof Error ? error.message : 'Failed to load GIFs.';
			gifResults = [];
		} finally {
			gifLoading = false;
		}
	}

	async function fetchKlipyStickers(query: string) {
		if (!KLIPY_API_KEY) {
			stickerError = 'Sticker search is unavailable. Add VITE_KLIPY_API_KEY to enable it.';
			stickerResults = [];
			return;
		}
		mediaAbortController?.abort();
		mediaAbortController = new AbortController();
		stickerLoading = true;
		stickerError = '';
		try {
			const endpointPath = query ? 'search' : 'trending';
			const params = new URLSearchParams({
				page: '1',
				per_page: String(KLIPY_SEARCH_LIMIT),
				customer_id: KLIPY_CLIENT_KEY,
				locale: getKlipyLocale(),
				content_filter: KLIPY_CONTENT_FILTER
			});
			applyKlipyAdParams(params);
			if (query) {
				params.set('q', query);
			}
			const response = await fetch(
				`${KLIPY_API_BASE}/api/v1/${encodeURIComponent(KLIPY_API_KEY)}/stickers/${endpointPath}?${params.toString()}`,
				{
					signal: mediaAbortController.signal,
					headers: {
						'Content-Type': 'application/json'
					}
				}
			);
			const payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
			if (!response.ok) {
				throw new Error(
					typeof payload.error === 'string'
						? payload.error
						: `Sticker request failed (${response.status})`
				);
			}
			stickerResults = parseKlipyMediaResults(payload, 'sticker');
		} catch (error) {
			const isAbortError =
				typeof error === 'object' &&
				error !== null &&
				'name' in error &&
				(error as { name?: string }).name === 'AbortError';
			if (isAbortError) {
				return;
			}
			stickerError = error instanceof Error ? error.message : 'Failed to load stickers.';
			stickerResults = [];
		} finally {
			stickerLoading = false;
		}
	}

	async function fetchKlipyMemes(query: string) {
		if (!KLIPY_API_KEY) {
			memeError = 'Meme search is unavailable. Add VITE_KLIPY_API_KEY to enable it.';
			memeResults = [];
			return;
		}
		mediaAbortController?.abort();
		mediaAbortController = new AbortController();
		memeLoading = true;
		memeError = '';
		try {
			const endpointPath = query ? 'search' : 'trending';
			const params = new URLSearchParams({
				page: '1',
				per_page: String(KLIPY_SEARCH_LIMIT),
				customer_id: KLIPY_CLIENT_KEY,
				locale: getKlipyLocale()
			});
			applyKlipyAdParams(params);
			if (query) {
				params.set('q', query);
			}
			const response = await fetch(
				`${KLIPY_API_BASE}/api/v1/${encodeURIComponent(KLIPY_API_KEY)}/static-memes/${endpointPath}?${params.toString()}`,
				{
					signal: mediaAbortController.signal,
					headers: {
						'Content-Type': 'application/json'
					}
				}
			);
			const payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
			if (!response.ok) {
				throw new Error(
					typeof payload.error === 'string'
						? payload.error
						: `Meme request failed (${response.status})`
				);
			}
			memeResults = parseKlipyMediaResults(payload, 'meme');
		} catch (error) {
			const isAbortError =
				typeof error === 'object' &&
				error !== null &&
				'name' in error &&
				(error as { name?: string }).name === 'AbortError';
			if (isAbortError) {
				return;
			}
			memeError = error instanceof Error ? error.message : 'Failed to load memes.';
			memeResults = [];
		} finally {
			memeLoading = false;
		}
	}

	function onMediaQueryInput() {
		if (!showMediaPicker || !KLIPY_API_KEY || activeMediaTab === 'emoji') {
			return;
		}
		if (mediaSearchTimer) {
			clearTimeout(mediaSearchTimer);
			mediaSearchTimer = null;
		}
		const normalizedQuery = mediaQuery.trim();
		mediaSearchTimer = setTimeout(() => {
			void fetchActiveMediaTab(normalizedQuery);
		}, 300);
	}

	function toMediaAssetFileName(asset: MediaAssetResult) {
		const normalizedTitle = (asset.title || asset.kind).trim();
		const safeBaseName = normalizedTitle
			.replace(/\.[^./\\\s]+$/, '')
			.replace(/[^a-zA-Z0-9-_ ]+/g, '')
			.trim()
			.replace(/\s+/g, '-')
			.slice(0, 64);
		let extension = asset.kind === 'gif' ? 'gif' : asset.kind === 'sticker' ? 'webp' : 'png';
		try {
			const assetUrl = new URL(asset.url);
			const match = assetUrl.pathname.match(/\.([A-Za-z0-9]{2,5})$/);
			if (match && match[1]) {
				extension = match[1].toLowerCase();
			}
		} catch {
			// ignore invalid URL parsing and use fallback extension
		}
		const baseName = safeBaseName || asset.kind;
		if (asset.kind === 'sticker') {
			const stickerBaseName = baseName.toLowerCase().startsWith('sticker-')
				? baseName
				: `sticker-${baseName}`;
			return `${stickerBaseName}.${extension}`;
		}
		return `${baseName}.${extension}`;
	}

	function selectMediaAssetAttachment(asset: MediaAssetResult) {
		if (!asset || !asset.url || disabled || isProcessingAttachment || isRecording) {
			return;
		}
		clearAttachmentPreview();
		attachedFile = null;
		attachedMediaAsset = asset;
		attachedMessageType = 'image';
		attachedPickerType = 'media';
		attachError = '';
		gifError = '';
		stickerError = '';
		memeError = '';
		closeMediaPicker();
		dispatch('attach', { file: null, type: 'media' });
	}

	function sendMediaAssetAttachment() {
		if (!attachedMediaAsset) {
			dispatch('send', undefined);
			return;
		}
		dispatch('send', {
			type: 'image',
			content: attachedMediaAsset.url,
			fileName: toMediaAssetFileName(attachedMediaAsset),
			text: draftMessage.trim()
		});
		draftMessage = '';
		attachedMediaAsset = null;
		attachedMessageType = null;
		dispatch('attach', { file: null, type: 'media' });
	}

	function clearAttachmentPreview() {
		if (attachmentPreviewURL) {
			URL.revokeObjectURL(attachmentPreviewURL);
			attachmentPreviewURL = '';
		}
	}

	function setAttachmentPreview(file: File, messageType: MediaMessageType) {
		clearAttachmentPreview();
		if (messageType === 'image' || messageType === 'video') {
			attachmentPreviewURL = URL.createObjectURL(file);
		}
	}

	async function onFilePicked(event: Event, pickerType: 'media' | 'file') {
		if (disabled) {
			return;
		}
		closeMediaPicker();
		const target = event.currentTarget as HTMLInputElement;
		const selected = target.files?.[0] ?? null;
		target.value = '';
		if (!selected) {
			return;
		}

		const messageType = resolveMessageType(selected, pickerType);
		attachError = '';
		attachedFile = selected;
		attachedMediaAsset = null;
		attachedMessageType = messageType;
		attachedPickerType = pickerType;
		setAttachmentPreview(selected, messageType);
		dispatch('attach', { file: selected, type: pickerType });
	}

	async function sendAttachment() {
		if (!attachedFile || !attachedMessageType) {
			dispatch('send', undefined);
			return;
		}

		isProcessingAttachment = true;
		attachError = '';
		try {
			const compressed = await compressMedia(attachedFile);
			const uploaded = await uploadToR2(compressed, roomId);
			dispatch('send', {
				type: attachedMessageType,
				content: uploaded.fileUrl,
				fileName: attachedFile.name
			});
			clearAttachmentPreview();
			attachedFile = null;
			attachedMessageType = null;
			dispatch('attach', { file: null, type: attachedPickerType });
		} catch (error) {
			const message = error instanceof Error ? error.message : 'Attachment failed';
			attachError = message;
			dispatch('attach', { file: attachedFile, type: attachedPickerType, error: message });
		} finally {
			isProcessingAttachment = false;
		}
	}

	function removeAttachment() {
		clearAttachmentPreview();
		attachedFile = null;
		attachedMediaAsset = null;
		attachedMessageType = null;
		attachError = '';
		dispatch('removeAttachment');
	}

	function cancelReply() {
		dispatch('cancelReply');
	}

	function proceedSend() {
		if (disabled || isProcessingAttachment || isOverMessageLimit || isRecording) {
			return;
		}
		if (beaconDraftOpen) {
			submitBeaconDraft();
			return;
		}
		if (taskDraftOpen) {
			submitTaskDraft();
			return;
		}
		if (attachedFile) {
			void sendAttachment();
			return;
		}
		if (attachedMediaAsset) {
			sendMediaAssetAttachment();
			return;
		}
		dispatch('send', undefined);
	}

	function onSend() {
		closeMentionPicker();
		if (!hasAcceptedAITerms && requiresAITermsForCurrentSend()) {
			requestAITermsAcceptance('send');
			return;
		}
		proceedSend();
	}

	function onComposerKeyDown(event: KeyboardEvent) {
		if (disabled) {
			return;
		}
		if (showMentionPicker && mentionOptions.length > 0) {
			if (event.key === 'ArrowDown') {
				event.preventDefault();
				mentionActiveIndex = (mentionActiveIndex + 1) % mentionOptions.length;
				return;
			}
			if (event.key === 'ArrowUp') {
				event.preventDefault();
				mentionActiveIndex =
					(mentionActiveIndex - 1 + mentionOptions.length) % mentionOptions.length;
				return;
			}
			if ((event.key === 'Enter' && !event.shiftKey) || event.key === 'Tab') {
				event.preventDefault();
				const selected = mentionOptions[mentionActiveIndex] ?? mentionOptions[0];
				if (selected) {
					selectMentionOption(selected);
				}
				return;
			}
		}
		if (event.key === 'Escape' && showMentionPicker) {
			event.preventDefault();
			closeMentionPicker();
			return;
		}
		if (event.key === 'Enter' && !event.shiftKey) {
			event.preventDefault();
			onSend();
		}
	}

	function onComposerInput(event: Event) {
		resizeComposerTextarea();
		syncComposerHighlightScroll();
		const nextValue =
			event.currentTarget instanceof HTMLTextAreaElement ? event.currentTarget.value : draftMessage;
		emitTypingValue(nextValue);
		updateMentionSuggestionsFromCaret();
	}

	function onComposerCursorActivity() {
		syncComposerHighlightScroll();
		updateMentionSuggestionsFromCaret();
	}

	$: if (composerTextareaEl) {
		draftMessage;
		resizeComposerTextarea();
	}

	function stopRecordingStream() {
		if (!recordingStream) {
			return;
		}
		for (const track of recordingStream.getTracks()) {
			track.stop();
		}
		recordingStream = null;
	}

	function toAbsoluteUploadURL(value: string) {
		const trimmed = (value || '').trim();
		if (!trimmed) {
			return '';
		}
		if (
			/^https?:\/\//i.test(trimmed) ||
			trimmed.startsWith('blob:') ||
			trimmed.startsWith('data:')
		) {
			return trimmed;
		}
		if (trimmed.startsWith('/')) {
			return `${API_BASE}${trimmed}`;
		}
		return `${API_BASE}/${trimmed}`;
	}

	async function uploadRecordedAudio(audioBlob: Blob) {
		const payload = new FormData();
		const fileName = `voice-message-${Date.now()}.webm`;
		payload.append('file', audioBlob, fileName);
		const roomIdParam = roomId ? `?roomId=${encodeURIComponent(roomId)}` : '';
		const res = await fetch(`${API_BASE}/api/upload${roomIdParam}`, {
			method: 'POST',
			body: payload
		});
		const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
		const rawFileURL = typeof data.fileUrl === 'string' ? data.fileUrl : '';
		const uploadedURL = toAbsoluteUploadURL(rawFileURL);
		if (!res.ok || !uploadedURL) {
			throw new Error(
				typeof data.error === 'string' ? data.error : `Voice upload failed (${res.status})`
			);
		}
		return { uploadedURL, fileName };
	}

	async function handleRecordingStop() {
		const hasAudio = audioChunks.some((chunk) => chunk.size > 0);
		if (!hasAudio) {
			audioChunks = [];
			mediaRecorder = null;
			return;
		}

		isProcessingAttachment = true;
		attachError = '';
		try {
			const audioBlob = new Blob(audioChunks, { type: 'audio/webm' });
			const { uploadedURL, fileName } = await uploadRecordedAudio(audioBlob);
			dispatch('send', {
				type: 'audio',
				content: uploadedURL,
				text: 'Voice message',
				fileName
			});
			draftMessage = '';
		} catch (error) {
			attachError = error instanceof Error ? error.message : 'Voice recording failed';
		} finally {
			audioChunks = [];
			mediaRecorder = null;
			isProcessingAttachment = false;
		}
	}

	async function toggleRecording() {
		if (
			disabled ||
			isProcessingAttachment ||
			attachedFile ||
			attachedMediaAsset ||
			taskDraftOpen ||
			beaconDraftOpen
		) {
			return;
		}
		closeMediaPicker();

		if (!isRecording) {
			if (typeof navigator === 'undefined' || !navigator.mediaDevices?.getUserMedia) {
				attachError = 'Microphone is not available in this browser.';
				return;
			}
			if (typeof MediaRecorder === 'undefined') {
				attachError = 'Media recording is not supported in this browser.';
				return;
			}

			try {
				attachError = '';
				audioChunks = [];
				recordingStream = await navigator.mediaDevices.getUserMedia({ audio: true });
				const recorder = new MediaRecorder(recordingStream);
				recorder.ondataavailable = (event: BlobEvent) => {
					if (event.data && event.data.size > 0) {
						audioChunks = [...audioChunks, event.data];
					}
				};
				recorder.onstop = () => {
					void handleRecordingStop();
				};
				mediaRecorder = recorder;
				recorder.start();
				isRecording = true;
			} catch (error) {
				stopRecordingStream();
				mediaRecorder = null;
				isRecording = false;
				attachError =
					error instanceof Error ? error.message : 'Unable to access microphone for recording.';
			}
			return;
		}

		isRecording = false;
		if (mediaRecorder && mediaRecorder.state !== 'inactive') {
			mediaRecorder.stop();
		}
		stopRecordingStream();
	}

	function getAttachmentLabel(type: MediaMessageType | null) {
		if (type === 'image') {
			return 'Image ready to send';
		}
		if (type === 'video') {
			return 'Video ready to send';
		}
		if (type === 'file') {
			return 'File ready to send';
		}
		return 'Attachment ready to send';
	}

	function getReplyPreviewText() {
		if (!activeReply) {
			return '';
		}
		const normalized = `${activeReply.senderName}: ${activeReply.content}`.trim();
		if (normalized.length <= 120) {
			return normalized;
		}
		return `${normalized.slice(0, 117)}...`;
	}

	function clearTaskDraft() {
		taskDraftOpen = false;
		taskDraftTitle = '';
		taskDraftItems = [];
		taskNewItemText = '';
		taskAddInputOpen = false;
		taskDraftError = '';
	}

	function clearBeaconDraft() {
		beaconDraftOpen = false;
		beaconDraftDate = '';
		beaconDraftTime = '';
		beaconDraftText = '';
		beaconDraftError = '';
	}

	function toLocalDateInputValue(date: Date) {
		const year = date.getFullYear();
		const month = `${date.getMonth() + 1}`.padStart(2, '0');
		const day = `${date.getDate()}`.padStart(2, '0');
		return `${year}-${month}-${day}`;
	}

	function toLocalTimeInputValue(date: Date) {
		const hours = `${date.getHours()}`.padStart(2, '0');
		const minutes = `${date.getMinutes()}`.padStart(2, '0');
		return `${hours}:${minutes}`;
	}

	function openBeaconDraft() {
		const defaultDate = new Date(Date.now() + 10 * 60 * 1000);
		beaconDraftOpen = true;
		beaconDraftDate = toLocalDateInputValue(defaultDate);
		beaconDraftTime = toLocalTimeInputValue(defaultDate);
		beaconDraftText = draftMessage.trim();
		beaconDraftError = '';
	}

	function submitBeaconDraft() {
		const normalizedText = beaconDraftText.trim();
		if (!normalizedText) {
			beaconDraftError = 'Enter beacon text before sending.';
			return;
		}
		if (!beaconDraftDate || !beaconDraftTime) {
			beaconDraftError = 'Select both date and time.';
			return;
		}
		const composedDateTime = new Date(`${beaconDraftDate}T${beaconDraftTime}`);
		const beaconAt = composedDateTime.getTime();
		if (!Number.isFinite(beaconAt) || beaconAt <= 0) {
			beaconDraftError = 'Invalid beacon date/time.';
			return;
		}
		const beaconLabel = formatBeaconTimestamp(beaconAt);
		dispatch('send', {
			type: 'beacon',
			content: buildBeaconMessagePayload({
				text: normalizedText,
				beaconAt,
				beaconLabel
			})
		});
		draftMessage = '';
		clearBeaconDraft();
	}

	function onBeaconDraftBackdropClick(event: MouseEvent) {
		if (event.target === event.currentTarget) {
			clearBeaconDraft();
		}
	}

	function openTaskDraftAddInput() {
		taskAddInputOpen = true;
		taskDraftError = '';
	}

	function cancelTaskDraftAddInput() {
		taskAddInputOpen = false;
		taskNewItemText = '';
	}

	function addTaskDraftItem() {
		const text = (taskNewItemText || '').trim();
		if (!text) {
			return;
		}
		taskDraftItems = [
			...taskDraftItems,
			{
				text,
				completed: false,
				completedBy: '',
				timestamp: 0,
				createdBy: (currentUsername || 'You').trim() || 'You',
				createdAt: Date.now()
			}
		];
		taskNewItemText = '';
		taskAddInputOpen = false;
		taskDraftError = '';
	}

	function removeTaskDraftItem(index: number) {
		if (index < 0 || index >= taskDraftItems.length) {
			return;
		}
		taskDraftItems = taskDraftItems.filter((_, itemIndex) => itemIndex !== index);
	}

	function onTaskDraftItemKeyDown(event: KeyboardEvent) {
		if (event.key === 'Enter') {
			event.preventDefault();
			addTaskDraftItem();
			return;
		}
		if (event.key === 'Escape') {
			event.preventDefault();
			cancelTaskDraftAddInput();
		}
	}

	function submitTaskDraft() {
		const title = taskDraftTitle.trim();
		if (!title) {
			taskDraftError = 'Add a title for this task card.';
			return;
		}
		if (taskDraftItems.length === 0) {
			taskDraftError = 'Add at least one task item.';
			return;
		}
		const content = stringifyTaskMessagePayload({
			title,
			tasks: taskDraftItems
		});
		dispatch('send', {
			type: 'task',
			content
		});
		clearTaskDraft();
	}

	function formatTaskMeta(timestamp: number) {
		if (!Number.isFinite(timestamp) || timestamp <= 0) {
			return '';
		}
		return new Date(timestamp).toLocaleString([], {
			month: 'short',
			day: 'numeric',
			hour: 'numeric',
			minute: '2-digit'
		});
	}

	function onTaskDraftBackdropClick(event: MouseEvent) {
		if (event.target === event.currentTarget) {
			clearTaskDraft();
		}
	}
</script>

<AiDisclaimerModal
	open={showAIDisclaimerModal}
	{isDarkMode}
	privacyPolicyUrl={AI_PRIVACY_POLICY_URL}
	on:cancel={onAIDisclaimerCancel}
	on:agree={onAIDisclaimerAgree}
/>

{#if taskDraftOpen}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<div
		class="task-draft-shell"
		data-mode={isDarkMode ? 'dark' : 'light'}
		role="presentation"
		on:click={onTaskDraftBackdropClick}
	>
		<section class="task-draft-card" role="group" aria-label="Task preview card">
			<div class="task-draft-header">
				<div class="task-draft-kicker">Task Preview</div>
				<button type="button" class="task-draft-close" on:click={clearTaskDraft}>Cancel</button>
			</div>
			<input type="text" class="task-draft-title" bind:value={taskDraftTitle} placeholder="Title" />
			<div class="task-draft-list">
				{#if taskDraftItems.length === 0}
					<div class="task-draft-empty">No tasks yet. Add your first item.</div>
				{:else}
					{#each taskDraftItems as task, index}
						<div class="task-draft-item">
							<input type="checkbox" checked={task.completed} disabled />
							<div class="task-draft-item-content">
								<div class="task-draft-item-name">{task.text}</div>
								<div class="task-draft-meta-line">
									<span>{task.createdBy}</span>
									<span aria-hidden="true">•</span>
									<span>{formatTaskMeta(task.createdAt)}</span>
									<span aria-hidden="true">•</span>
									<span class="task-draft-meta-finished">open</span>
								</div>
							</div>
							<button
								type="button"
								class="task-draft-remove"
								on:click={() => removeTaskDraftItem(index)}
								aria-label="Remove task item"
								title="Remove"
							>
								×
							</button>
						</div>
					{/each}
				{/if}
			</div>
			{#if taskAddInputOpen}
				<div class="task-draft-add-row">
					<input type="checkbox" disabled aria-hidden="true" />
					<input
						type="text"
						bind:value={taskNewItemText}
						placeholder="Task name"
						on:keydown={onTaskDraftItemKeyDown}
					/>
					<div class="task-draft-add-actions">
						<button type="button" class="add-row-action confirm" on:click={addTaskDraftItem}>
							Add
						</button>
						<button type="button" class="add-row-action" on:click={cancelTaskDraftAddInput}>
							Cancel
						</button>
					</div>
				</div>
			{:else}
				<button type="button" class="task-draft-add-trigger" on:click={openTaskDraftAddInput}>
					<span class="plus-pill">+</span>
					<span>Add Task</span>
				</button>
			{/if}
			{#if taskDraftError}
				<div class="task-draft-error">{taskDraftError}</div>
			{/if}
			<div class="task-draft-footer">
				<button type="button" class="task-draft-footer-btn ghost" on:click={clearTaskDraft}>
					Cancel
				</button>
				<button type="button" class="task-draft-footer-btn submit" on:click={submitTaskDraft}>
					Create Task
				</button>
			</div>
		</section>
	</div>
{/if}

{#if beaconDraftOpen}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<div
		class="beacon-draft-shell"
		data-mode={isDarkMode ? 'dark' : 'light'}
		role="presentation"
		on:click={onBeaconDraftBackdropClick}
	>
		<section class="beacon-draft-card" role="group" aria-label="Beacon composer">
			<div class="beacon-draft-header">
				<div class="beacon-draft-kicker">Beacon</div>
				<button type="button" class="beacon-draft-close" on:click={clearBeaconDraft}>Cancel</button>
			</div>
			<div class="beacon-draft-schedule-row">
				<label>
					<span>Date</span>
					<input type="date" bind:value={beaconDraftDate} />
				</label>
				<label>
					<span>Time</span>
					<input type="time" bind:value={beaconDraftTime} />
				</label>
			</div>
			<label class="beacon-draft-text-wrap">
				<span>Message</span>
				<textarea
					rows="3"
					bind:value={beaconDraftText}
					placeholder="What should this beacon remind the room about?"
				></textarea>
			</label>
			<div class="beacon-preview-card">
				<div class="beacon-preview-meta">
					<IconSet name="beacon" size={14} />
					<span>{beaconDraftLabel || 'Set date and time'}</span>
				</div>
				<div class="beacon-preview-text">
					{beaconDraftText.trim() || 'Your beacon preview appears here.'}
				</div>
			</div>
			{#if beaconDraftError}
				<div class="beacon-draft-error">{beaconDraftError}</div>
			{/if}
			<div class="beacon-draft-footer">
				<button type="button" class="beacon-draft-footer-btn ghost" on:click={clearBeaconDraft}>
					Cancel
				</button>
				<button type="button" class="beacon-draft-footer-btn submit" on:click={submitBeaconDraft}>
					Send Beacon
				</button>
			</div>
		</section>
	</div>
{/if}

<footer class="composer" data-mode={isDarkMode ? 'dark' : 'light'}>
	{#if activeReply}
		<div class="reply-preview-panel">
			<div class="reply-preview-label">Replying to</div>
			<div class="reply-preview-content">{getReplyPreviewText()}</div>
			<button type="button" class="reply-preview-cancel" on:click={cancelReply}>Cancel</button>
		</div>
	{/if}
	{#if attachedFile || attachedMediaAsset}
		<div class="attachment-preview-panel">
			<div class="attachment-preview-header">
				<div class="attachment-preview-title">{getAttachmentLabel(attachedMessageType)}</div>
				<button type="button" class="preview-remove" on:click={removeAttachment}>x</button>
			</div>
			{#if attachedMediaAsset}
				<img
					src={attachedMediaAsset.previewUrl || attachedMediaAsset.url}
					alt={attachedMediaAsset.title || 'Media'}
					class="attachment-preview-image"
				/>
			{:else if attachedMessageType === 'image' && attachmentPreviewURL && attachedFile}
				<img src={attachmentPreviewURL} alt={attachedFile.name} class="attachment-preview-image" />
			{:else if attachedMessageType === 'video' && attachmentPreviewURL && attachedFile}
				<!-- svelte-ignore a11y_media_has_caption -->
				<video
					src={attachmentPreviewURL}
					class="attachment-preview-video"
					controls
					preload="metadata"
				></video>
			{:else if attachedFile}
				<div class="attachment-preview-file">
					<IconSet name="file" size={18} />
					<span>{attachedFile.name}</span>
				</div>
			{/if}
		</div>
	{/if}
	{#if showMediaPicker}
		<div class="media-picker-panel" bind:this={mediaPickerEl}>
			<div class="media-picker-tabs" role="tablist" aria-label="Media picker tabs">
				<button
					type="button"
					class="media-picker-tab {activeMediaTab === 'emoji' ? 'active' : ''}"
					role="tab"
					aria-selected={activeMediaTab === 'emoji'}
					on:click={() => switchMediaTab('emoji')}
				>
					Emoji
				</button>
				<button
					type="button"
					class="media-picker-tab {activeMediaTab === 'gif' ? 'active' : ''}"
					role="tab"
					aria-selected={activeMediaTab === 'gif'}
					on:click={() => switchMediaTab('gif')}
				>
					GIFs
				</button>
				<button
					type="button"
					class="media-picker-tab {activeMediaTab === 'sticker' ? 'active' : ''}"
					role="tab"
					aria-selected={activeMediaTab === 'sticker'}
					on:click={() => switchMediaTab('sticker')}
				>
					Stickers
				</button>
				<button
					type="button"
					class="media-picker-tab {activeMediaTab === 'meme' ? 'active' : ''}"
					role="tab"
					aria-selected={activeMediaTab === 'meme'}
					on:click={() => switchMediaTab('meme')}
				>
					Memes
				</button>
			</div>
			<div class="media-picker-header">
				{#if activeMediaTab !== 'emoji'}
					<input
						type="text"
						placeholder={activeMediaSearchPlaceholder}
						bind:value={mediaQuery}
						on:input={onMediaQueryInput}
					/>
				{/if}
				<button
					type="button"
					class="media-picker-close"
					on:click={() => closeMediaPicker()}
					aria-label="Close media picker"
				>
					Close
				</button>
			</div>
			{#if activeMediaTab === 'emoji'}
				<div class="emoji-picker" role="dialog" aria-label="Emoji picker">
					{#each COMMON_EMOJIS as emoji}
						<button
							type="button"
							class="emoji-option"
							on:click={() => insertEmoji(emoji)}
							aria-label={`Insert ${emoji}`}
						>
							{emoji}
						</button>
					{/each}
				</div>
			{:else if activeMediaError}
				<div class="media-picker-error">{activeMediaError}</div>
			{:else if activeMediaLoading}
				<div class="media-picker-loading">Loading...</div>
			{:else if activeMediaResults.length === 0}
				<div class="media-picker-empty">No results found. Try another search.</div>
			{:else}
				<div class="media-grid">
					{#each activeMediaResults as asset (asset.id)}
						{#if asset.isAd}
							<div class="media-card ad-card" aria-label="Advertisement">
								<div class="media-ad-slot">
									{@html asset.adContent ?? ''}
								</div>
							</div>
						{:else}
							<button
								type="button"
								class="media-card"
								on:click={() => selectMediaAssetAttachment(asset)}
								title={`Attach ${asset.title}`}
								aria-label={`Attach ${asset.title}`}
							>
								<img src={asset.previewUrl} alt={asset.title || 'Media'} loading="lazy" />
							</button>
						{/if}
					{/each}
				</div>
			{/if}
		</div>
	{/if}
	{#if attachError}
		<div class="attachment-error">{attachError}</div>
	{/if}
	{#if isProcessingAttachment}
		<div class="attachment-progress" role="status" aria-label="Uploading attachment">
			<span class="attachment-progress-bar"></span>
		</div>
	{/if}
	<div class="composer-row" class:typing-active={hasComposerInput}>
		<input
			bind:this={mediaInput}
			type="file"
			class="hidden-file-input"
			accept="image/*,video/*"
			on:change={(event) => void onFilePicked(event, 'media')}
		/>
		<input
			bind:this={fileInput}
			type="file"
			class="hidden-file-input"
			accept="*"
			on:change={(event) => void onFilePicked(event, 'file')}
		/>

		<div class="attach-wrap" bind:this={attachWrapEl}>
			<button
				type="button"
				class="attach-button"
				on:click={toggleAttachMenu}
				disabled={disabled || isProcessingAttachment || isRecording || beaconDraftOpen}
				aria-label="Attach"
				title="Attach"
			>
				<IconSet name="paperclip" size={14} />
			</button>
			{#if showAttachMenu}
				<div class="attach-menu">
					<button type="button" on:click={() => chooseAttachmentType('media')}>
						<IconSet name="image" size={14} />
						<span>Media</span>
					</button>
					<button type="button" on:click={() => chooseAttachmentType('file')}>
						<IconSet name="file" size={14} />
						<span>File</span>
					</button>
					<button type="button" on:click={() => chooseAttachmentType('task')}>
						<IconSet name="list-vertical" size={14} />
						<span>Task</span>
					</button>
					{#if isEphemeralRoom}
						<button type="button" on:click={() => chooseAttachmentType('beacon')}>
							<IconSet name="beacon" size={14} />
							<span>Beacon</span>
						</button>
					{/if}
				</div>
			{/if}
		</div>
		{#if aiEnabled}
			<button
				type="button"
				class="ai-button"
				class:slot-hidden={hasComposerInput}
				on:click={onAIButtonClick}
				disabled={composerDisabled || hasComposerInput}
				aria-hidden={hasComposerInput}
				aria-label="Ask AI Privately"
				title="Ask AI Privately"
			>
				<svg viewBox="0 0 24 24" aria-hidden="true">
					<path
						d="M12 2.75 14.5 8.2l5.95.8-4.4 4.15 1.16 5.85L12 16.3l-5.21 2.7 1.16-5.85L3.55 9l5.95-.8Z"
					></path>
				</svg>
			</button>
		{/if}
		<div class="media-picker-wrap" bind:this={mediaPickerWrapEl}>
			<button
				type="button"
				class="media-picker-button"
				on:click={toggleMediaPicker}
				disabled={composerDisabled}
				aria-label="Open emoji, GIF, sticker, and meme picker"
				title="Emoji, GIFs, stickers, memes"
			>
				<svg viewBox="0 0 24 24" aria-hidden="true">
					<rect x="3.5" y="4.5" width="14" height="14" rx="3"></rect>
					<circle cx="8.8" cy="9.8" r="1"></circle>
					<circle cx="12.2" cy="9.8" r="1"></circle>
					<path d="M7.8 13.1c1.1 1 3 1 4.1 0"></path>
					<path d="M18 6.3v2.1"></path>
					<path d="M16.95 7.35h2.1"></path>
				</svg>
			</button>
		</div>

		<div class="composer-input-wrap">
			<div class="composer-input-highlight" bind:this={composerHighlightEl} aria-hidden="true">
				<div class="composer-input-highlight-content">
					{#if composerMentionSegments.length === 0}
						<span> </span>
					{:else}
						{#each composerMentionSegments as segment, segmentIndex (`${segmentIndex}-${segment.value}-${segment.isMention ? 'mention' : 'text'}`)}
							{#if segment.isMention}
								<span class="composer-mention-token">{segment.value}</span>
							{:else}
								{segment.value}
							{/if}
						{/each}
					{/if}
				</div>
			</div>
			<textarea
				bind:this={composerTextareaEl}
				bind:value={draftMessage}
				rows="1"
				placeholder={composerPlaceholder}
				on:input={onComposerInput}
				on:scroll={syncComposerHighlightScroll}
				on:keydown={onComposerKeyDown}
				on:click={onComposerCursorActivity}
				on:keyup={onComposerCursorActivity}
				disabled={composerDisabled}
				autocomplete="off"
			></textarea>
			{#if showMentionPicker && mentionOptions.length > 0}
				<div
					class="mention-picker"
					bind:this={mentionPickerEl}
					role="listbox"
					aria-label="Mention suggestions"
				>
					{#each mentionOptions as option, index (option.id)}
						<button
							type="button"
							class="mention-option {index === mentionActiveIndex ? 'active' : ''}"
							role="option"
							aria-selected={index === mentionActiveIndex}
							on:mousedown|preventDefault
							on:click={() => selectMentionOption(option)}
						>
							<span class="mention-option-label">@{option.label}</span>
							{#if option.isAI}
								<span class="mention-option-pill">AI</span>
							{/if}
						</button>
					{/each}
				</div>
			{/if}
		</div>
		{#if showSendButton}
			<button
				type="button"
				class="send-button"
				on:click={onSend}
				disabled={disabled ||
					isProcessingAttachment ||
					isOverMessageLimit ||
					isRecording ||
					(taskDraftOpen && !taskDraftReady)}
				aria-label={hasPendingAttachment
					? 'Send attachment'
					: taskDraftOpen
						? 'Send task'
						: 'Send message'}
				title={isOverMessageLimit
					? `Message is too long (${draftMessageBytes}/${messageLimit})`
					: hasPendingAttachment
						? 'Send attachment'
						: taskDraftOpen
							? 'Send task card'
							: 'Send message'}
			>
				<IconSet name="send" size={15} />
			</button>
		{:else}
			<button
				type="button"
				class="mic-button {isRecording ? 'recording' : ''}"
				on:click={toggleRecording}
				disabled={disabled ||
					isProcessingAttachment ||
					hasPendingAttachment ||
					taskDraftOpen ||
					beaconDraftOpen}
				aria-label={isRecording ? 'Stop recording and send voice message' : 'Record voice message'}
				title={isRecording ? 'Stop recording and send voice message' : 'Record voice message'}
			>
				<svg
					width="14"
					height="14"
					viewBox="0 0 24 24"
					fill="none"
					stroke="currentColor"
					stroke-width="2"
					stroke-linecap="round"
					stroke-linejoin="round"
					aria-hidden="true"
				>
					<rect x="9" y="2" width="6" height="12" rx="3"></rect>
					<path d="M5 10a7 7 0 0 0 14 0"></path>
					<line x1="12" y1="17" x2="12" y2="22"></line>
					<line x1="8" y1="22" x2="16" y2="22"></line>
				</svg>
			</button>
		{/if}
	</div>
	{#if isOverMessageLimit}
		<div class="composer-limit-hint" role="status" aria-live="polite">
			Message is too long by {overLimitBy}. Max {messageLimit}.
		</div>
	{/if}
</footer>

<style>
	.composer {
		position: relative;
		border-top: 1px solid #cfd6de;
		background: linear-gradient(180deg, #f6f7f9 0%, #edf0f3 100%);
		padding: 0.72rem 0.78rem 0.82rem;
		display: flex;
		flex-direction: column;
		gap: 0.48rem;
		flex-shrink: 0;
		box-shadow: 0 -12px 24px rgba(15, 23, 42, 0.09);
		backdrop-filter: blur(8px);
	}

	.composer[data-mode='dark'] {
		border-top-color: #343a43;
		background: linear-gradient(180deg, #1c2026 0%, #171b21 100%);
		box-shadow: 0 -14px 26px rgba(2, 8, 23, 0.3);
	}

	.composer::before {
		content: '';
		position: absolute;
		left: 0;
		right: 0;
		top: 0;
		height: 1px;
		background: rgba(121, 130, 143, 0.4);
		opacity: 1;
	}

	.composer[data-mode='dark']::before {
		background: rgba(126, 136, 149, 0.34);
	}

	.reply-preview-panel {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		border-radius: 10px;
		padding: 0.56rem 0.62rem;
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		grid-template-rows: auto auto;
		column-gap: 0.5rem;
		row-gap: 0.18rem;
		align-items: center;
	}

	.reply-preview-label {
		grid-column: 1;
		font-size: 0.7rem;
		font-weight: 700;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--text-secondary);
	}

	.reply-preview-content {
		grid-column: 1;
		font-size: 0.8rem;
		color: var(--text-primary);
		line-height: 1.28;
		word-break: break-word;
	}

	.reply-preview-cancel {
		grid-column: 2;
		grid-row: 1 / span 2;
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		border-radius: 8px;
		padding: 0.28rem 0.52rem;
		font-size: 0.72rem;
		cursor: pointer;
		color: var(--text-secondary);
	}

	.attachment-preview-panel {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		border-radius: 12px;
		padding: 0.55rem;
		display: flex;
		flex-direction: column;
		gap: 0.45rem;
	}

	.attachment-preview-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	.attachment-preview-title {
		font-size: 0.78rem;
		font-weight: 600;
		color: var(--text-primary);
	}

	.preview-remove {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		border-radius: 6px;
		width: 24px;
		height: 24px;
		cursor: pointer;
		color: var(--text-secondary);
	}

	.attachment-preview-image,
	.attachment-preview-video {
		display: block;
		width: min(100%, 260px);
		max-height: 180px;
		border: 1px solid var(--border-default);
		border-radius: 8px;
		background: var(--bg-tertiary);
		object-fit: cover;
	}

	.attachment-preview-file {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		color: var(--text-secondary);
		font-size: 0.84rem;
		padding: 0.35rem 0.15rem;
	}

	.attachment-error {
		font-size: 0.79rem;
		color: var(--accent-danger);
		background: var(--state-danger-bg);
		border: 1px solid var(--state-danger-border);
		border-radius: 8px;
		padding: 0.36rem 0.5rem;
	}

	.attachment-progress {
		width: 100px;
		height: 4px;
		border-radius: 999px;
		background: var(--state-info-bg);
		border: 1px solid var(--state-info-border);
		overflow: hidden;
		position: relative;
	}

	.attachment-progress-bar {
		position: absolute;
		top: 0;
		left: -42%;
		display: block;
		width: 42%;
		height: 100%;
		border-radius: 999px;
		background: var(--accent-primary);
		animation: attachment-progress-slide 1s ease-in-out infinite;
	}

	@keyframes attachment-progress-slide {
		0% {
			left: -42%;
		}
		100% {
			left: 100%;
		}
	}

	.media-picker-panel {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		border-radius: 12px;
		padding: 0.56rem;
		display: flex;
		flex-direction: column;
		gap: 0.46rem;
		max-height: min(54vh, 380px);
		overflow: hidden;
		min-height: 0;
	}

	.media-picker-tabs {
		display: flex;
		align-items: center;
		gap: 0.36rem;
		flex-wrap: wrap;
	}

	.media-picker-tab {
		border: 1px solid var(--border-default);
		background: var(--surface-secondary);
		color: var(--text-secondary);
		border-radius: 8px;
		padding: 0.25rem 0.56rem;
		font-size: 0.72rem;
		font-weight: 600;
		cursor: pointer;
	}

	.media-picker-tab.active {
		border-color: var(--accent-primary);
		background: var(--state-info-bg);
		color: var(--accent-primary);
	}

	.media-picker-header {
		display: flex;
		align-items: center;
		gap: 0.45rem;
	}

	.media-picker-header input {
		flex: 1;
		min-width: 0;
		border: 1px solid var(--border-default);
		background: var(--surface-secondary);
		color: var(--text-primary);
		border-radius: 9px;
		padding: 0.34rem 0.52rem;
		font-size: 0.8rem;
	}

	.media-picker-close {
		border: 1px solid var(--border-default);
		background: var(--surface-secondary);
		color: var(--text-secondary);
		border-radius: 8px;
		padding: 0.3rem 0.5rem;
		font-size: 0.72rem;
		cursor: pointer;
	}

	.media-picker-loading,
	.media-picker-empty,
	.media-picker-error {
		font-size: 0.78rem;
		color: var(--text-secondary);
	}

	.media-picker-error {
		color: var(--accent-danger);
	}

	.media-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(96px, 1fr));
		grid-auto-rows: minmax(96px, auto);
		grid-auto-flow: row;
		align-content: start;
		flex: 1 1 auto;
		overflow-y: auto;
		overflow-x: hidden;
		max-height: 100%;
		gap: 0.42rem;
		padding-right: 0.08rem;
		-webkit-overflow-scrolling: touch;
	}

	.media-card {
		position: relative;
		display: block;
		border: 1px solid var(--border-default);
		background: var(--surface-secondary);
		border-radius: 9px;
		padding: 0;
		overflow: hidden;
		cursor: pointer;
		aspect-ratio: 1 / 1;
		min-height: 96px;
		height: 100%;
	}

	.media-card img {
		display: block;
		position: relative;
		inset: auto;
		width: 100%;
		height: 100%;
		object-fit: cover;
	}

	.media-card.ad-card {
		cursor: default;
	}

	.media-ad-slot {
		width: 100%;
		height: 100%;
		display: flex;
		align-items: center;
		justify-content: center;
		overflow: hidden;
	}

	.media-ad-slot :global(*) {
		max-width: 100%;
		max-height: 100%;
		box-sizing: border-box;
	}

	.media-ad-slot :global(iframe),
	.media-ad-slot :global(img),
	.media-ad-slot :global(video),
	.media-ad-slot :global(canvas) {
		border: 0;
		width: 100%;
		height: 100%;
		object-fit: cover;
	}

	.task-draft-shell {
		position: fixed;
		inset: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 1.2rem;
		background: var(--overlay-soft);
		backdrop-filter: blur(8px);
		-webkit-backdrop-filter: blur(8px);
		z-index: 520;
	}

	.task-draft-card {
		width: min(100%, 54rem);
		max-height: min(92vh, 820px);
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		border-radius: 14px;
		padding: 0.72rem 0.76rem;
		display: flex;
		flex-direction: column;
		gap: 0.56rem;
		overflow: auto;
		box-shadow: var(--shadow-lg);
	}

	.task-draft-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.4rem;
	}

	.task-draft-kicker {
		font-size: 0.68rem;
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--text-secondary);
	}

	.task-draft-close {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		color: var(--text-secondary);
		border-radius: 9px;
		padding: 0.24rem 0.56rem;
		font-size: 0.72rem;
		font-weight: 700;
		cursor: pointer;
	}

	.task-draft-title {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		color: var(--text-primary);
		border-radius: 10px;
		padding: 0.52rem 0.64rem;
		font-size: 0.95rem;
		font-weight: 700;
	}

	.task-draft-title:focus {
		outline: none;
		border-color: var(--border-focus);
		box-shadow: 0 0 0 2px var(--interactive-focus);
	}

	.task-draft-list {
		display: flex;
		flex-direction: column;
		gap: 0.45rem;
	}

	.task-draft-empty {
		border: 1px dashed var(--border-default);
		background: var(--surface-secondary);
		border-radius: 10px;
		padding: 0.5rem 0.6rem;
		font-size: 0.78rem;
		color: var(--text-secondary);
	}

	.task-draft-item {
		display: grid;
		grid-template-columns: 1rem minmax(0, 1fr) auto;
		gap: 0.48rem;
		align-items: center;
		padding: 0.5rem 0.56rem;
		border: 1px solid var(--border-default);
		border-radius: 10px;
		background: var(--surface-primary);
	}

	.task-draft-item input[type='checkbox'] {
		width: 0.95rem;
		height: 0.95rem;
		accent-color: var(--accent-success);
	}

	.task-draft-item-content {
		display: flex;
		flex-direction: column;
		gap: 0.16rem;
		min-width: 0;
	}

	.task-draft-item-name {
		font-size: 0.82rem;
		color: var(--text-primary);
		word-break: break-word;
		font-weight: 600;
	}

	.task-draft-meta-line {
		display: inline-flex;
		align-items: center;
		gap: 0.28rem;
		font-size: 0.67rem;
		color: var(--text-secondary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.task-draft-meta-finished {
		color: var(--text-tertiary);
	}

	.task-draft-remove {
		border: 1px solid var(--border-default);
		background: var(--surface-secondary);
		color: var(--text-secondary);
		border-radius: 8px;
		width: 1.55rem;
		height: 1.55rem;
		cursor: pointer;
		font-size: 1rem;
		line-height: 1;
	}

	.task-draft-add-trigger {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		border: 1.5px solid var(--accent-success);
		background: var(--state-success-bg);
		color: var(--accent-success);
		border-radius: 10px;
		padding: 0.38rem 0.66rem;
		font-size: 0.79rem;
		font-weight: 700;
		cursor: pointer;
	}

	.plus-pill {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 1.1rem;
		height: 1.1rem;
		border-radius: 6px;
		border: 1px solid var(--state-success-border);
		background: var(--surface-primary);
		font-size: 0.9rem;
		line-height: 1;
	}

	.task-draft-add-row {
		display: grid;
		grid-template-columns: 1rem minmax(0, 1fr) auto;
		gap: 0.34rem;
		align-items: center;
	}

	.task-draft-add-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.34rem;
		flex-wrap: wrap;
	}

	.task-draft-add-row input[type='checkbox'] {
		width: 0.95rem;
		height: 0.95rem;
		accent-color: var(--accent-success);
	}

	.task-draft-add-row input[type='text'] {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		color: var(--text-primary);
		border-radius: 9px;
		padding: 0.36rem 0.52rem;
		font-size: 0.79rem;
		min-width: 0;
	}

	.add-row-action {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		color: var(--text-secondary);
		border-radius: 9px;
		padding: 0.31rem 0.56rem;
		font-size: 0.73rem;
		font-weight: 700;
		cursor: pointer;
		white-space: nowrap;
		min-width: 3.5rem;
	}

	.add-row-action.confirm {
		border-color: var(--accent-success);
		background: var(--state-success-bg);
		color: var(--accent-success);
	}

	.task-draft-footer {
		position: sticky;
		bottom: -0.72rem;
		margin-top: 0.2rem;
		margin-inline: -0.76rem;
		padding: 0.58rem 0.76rem 0.74rem;
		display: flex;
		justify-content: flex-end;
		gap: 0.45rem;
		background: linear-gradient(180deg, var(--surface-primary) 0%, var(--surface-secondary) 30%);
		border-top: 1px solid var(--border-default);
	}

	.task-draft-footer-btn {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		color: var(--text-secondary);
		border-radius: 10px;
		padding: 0.43rem 0.78rem;
		font-size: 0.78rem;
		font-weight: 700;
		cursor: pointer;
	}

	.task-draft-footer-btn.submit {
		border-color: var(--accent-success);
		background: var(--state-success-bg);
		color: var(--accent-success);
	}

	.task-draft-footer-btn.ghost {
		background: var(--surface-secondary);
	}

	.task-draft-error {
		font-size: 0.74rem;
		color: var(--accent-danger);
		background: var(--state-danger-bg);
		border: 1px solid var(--state-danger-border);
		border-radius: 8px;
		padding: 0.32rem 0.48rem;
	}

	.beacon-draft-shell {
		position: fixed;
		inset: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 1.1rem;
		background: var(--overlay-soft);
		backdrop-filter: blur(8px);
		-webkit-backdrop-filter: blur(8px);
		z-index: 525;
	}

	.beacon-draft-card {
		width: min(100%, 32rem);
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		border-radius: 14px;
		padding: 0.85rem;
		display: flex;
		flex-direction: column;
		gap: 0.62rem;
		box-shadow: var(--shadow-lg);
	}

	.beacon-draft-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.4rem;
	}

	.beacon-draft-kicker {
		font-size: 0.69rem;
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--text-secondary);
	}

	.beacon-draft-close {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		color: var(--text-secondary);
		border-radius: 9px;
		padding: 0.22rem 0.56rem;
		font-size: 0.72rem;
		font-weight: 700;
		cursor: pointer;
	}

	.beacon-draft-schedule-row {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.52rem;
	}

	.beacon-draft-schedule-row label,
	.beacon-draft-text-wrap {
		display: flex;
		flex-direction: column;
		gap: 0.24rem;
	}

	.beacon-draft-schedule-row span,
	.beacon-draft-text-wrap span {
		font-size: 0.73rem;
		font-weight: 700;
		color: var(--text-secondary);
	}

	.beacon-draft-schedule-row input,
	.beacon-draft-text-wrap textarea {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		color: var(--text-primary);
		border-radius: 10px;
		padding: 0.45rem 0.55rem;
		font-size: 0.82rem;
	}

	.beacon-draft-text-wrap textarea {
		resize: vertical;
		min-height: 4.7rem;
		max-height: 10.5rem;
	}

	.beacon-draft-schedule-row input:focus,
	.beacon-draft-text-wrap textarea:focus {
		outline: none;
		border-color: var(--border-focus);
		box-shadow: 0 0 0 2px var(--interactive-focus);
	}

	.beacon-preview-card {
		border: 1px solid var(--border-default);
		background: var(--surface-secondary);
		border-radius: 11px;
		padding: 0.52rem 0.6rem;
		display: flex;
		flex-direction: column;
		gap: 0.34rem;
	}

	.beacon-preview-meta {
		display: inline-flex;
		align-items: center;
		gap: 0.34rem;
		font-size: 0.74rem;
		font-weight: 700;
		color: var(--text-secondary);
	}

	.beacon-preview-text {
		font-size: 0.82rem;
		color: var(--text-primary);
		word-break: break-word;
	}

	.beacon-draft-error {
		font-size: 0.74rem;
		color: var(--accent-danger);
		background: var(--state-danger-bg);
		border: 1px solid var(--state-danger-border);
		border-radius: 8px;
		padding: 0.32rem 0.48rem;
	}

	.beacon-draft-footer {
		display: flex;
		justify-content: flex-end;
		gap: 0.44rem;
	}

	.beacon-draft-footer-btn {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		color: var(--text-secondary);
		border-radius: 10px;
		padding: 0.42rem 0.78rem;
		font-size: 0.78rem;
		font-weight: 700;
		cursor: pointer;
	}

	.beacon-draft-footer-btn.submit {
		border-color: var(--state-info-border);
		background: var(--state-info-bg);
		color: var(--accent-info);
	}

	.beacon-draft-footer-btn.ghost {
		background: var(--surface-secondary);
	}

	@media (max-width: 640px) {
		.task-draft-card {
			width: min(100%, 100vw - 1rem);
			max-height: min(88vh, 760px);
			padding: 0.62rem;
		}

		.beacon-draft-card {
			width: min(100%, 100vw - 1rem);
			padding: 0.65rem;
		}

		.beacon-draft-schedule-row {
			grid-template-columns: 1fr;
		}

		.task-draft-footer {
			bottom: -0.62rem;
			margin-inline: -0.62rem;
			padding-inline: 0.62rem;
		}

		.task-draft-add-row {
			grid-template-columns: 1rem minmax(0, 1fr);
		}

		.task-draft-add-actions {
			grid-column: 1 / -1;
			justify-self: start;
		}
	}

	.composer-limit-hint {
		font-size: 0.74rem;
		line-height: 1.2;
		color: var(--accent-danger);
		opacity: 0.92;
		padding: 0 0.2rem;
	}

	.composer-row {
		display: grid;
		grid-template-columns: 2.2rem 2.2rem 2.2rem minmax(0, 1fr) 2.2rem;
		gap: 0.42rem;
		align-items: center;
		border: 1px solid #cfd6df;
		background: #f8f9fb;
		border-radius: 16px;
		padding: 0.32rem 0.34rem;
		box-shadow:
			0 7px 18px rgba(15, 23, 42, 0.07),
			inset 0 1px 0 rgba(255, 255, 255, 0.95);
		transition:
			border-color 140ms ease,
			box-shadow 140ms ease,
			background 140ms ease;
	}

	.composer-row.typing-active {
		grid-template-columns: 2.2rem 0 2.2rem minmax(0, 1fr) 2.2rem;
	}

	.composer[data-mode='dark'] .composer-row {
		border-color: #3c434d;
		background: #242a32;
		box-shadow:
			0 8px 18px rgba(2, 8, 23, 0.24),
			inset 0 1px 0 rgba(148, 163, 184, 0.08);
	}

	.composer-row:focus-within {
		border-color: #a1acb8;
		box-shadow:
			0 10px 22px rgba(15, 23, 42, 0.1),
			0 0 0 2px rgba(127, 138, 151, 0.2);
	}

	.composer[data-mode='dark'] .composer-row:focus-within {
		border-color: #656f7c;
		box-shadow:
			0 10px 22px rgba(2, 8, 23, 0.32),
			0 0 0 2px rgba(127, 138, 151, 0.2);
	}

	.hidden-file-input {
		display: none;
	}

	.attach-wrap {
		position: relative;
	}

	.attach-button,
	.media-picker-button,
	.ai-button,
	.mic-button,
	.send-button {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		border: 1px solid var(--border-default);
		background: var(--surface-secondary);
		border-radius: 10px;
		width: 2.1rem;
		height: 2.1rem;
		cursor: pointer;
		color: var(--text-secondary);
		padding: 0;
		transition:
			background 140ms ease,
			border-color 140ms ease,
			transform 140ms ease,
			color 140ms ease;
	}

	.attach-button:disabled,
	.media-picker-button:disabled,
	.ai-button:disabled,
	.mic-button:disabled,
	.send-button:disabled {
		opacity: 0.7;
		cursor: not-allowed;
	}

	.attach-button:hover:not(:disabled),
	.media-picker-button:hover:not(:disabled),
	.ai-button:hover:not(:disabled),
	.mic-button:hover:not(:disabled),
	.send-button:hover:not(:disabled) {
		background: var(--surface-hover);
		border-color: var(--border-strong);
		transform: translateY(-1px);
	}

	.mic-button.recording {
		border-color: var(--accent-danger);
		background: var(--accent-danger);
		color: var(--text-inverse);
		animation: mic-pulse 1.1s ease-in-out infinite;
	}

	.send-button {
		background: var(--accent-primary);
		border-color: var(--accent-primary);
		color: var(--text-inverse);
	}

	.send-button:hover:not(:disabled) {
		background: var(--accent-primary-hover);
		border-color: var(--accent-primary-hover);
	}

	.media-picker-wrap {
		position: relative;
	}

	.media-picker-button svg {
		width: 1rem;
		height: 1rem;
		stroke: currentColor;
		fill: none;
		stroke-width: 1.7;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.ai-button svg {
		width: 1rem;
		height: 1rem;
		stroke: currentColor;
		fill: none;
		stroke-width: 1.9;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.ai-button.slot-hidden {
		visibility: hidden;
		pointer-events: none;
	}

	.ai-button.slot-hidden {
		width: 0;
		height: 0;
		border: 0;
		padding: 0;
	}

	.ai-button.slot-hidden svg {
		display: none;
	}

	.emoji-picker {
		display: grid;
		grid-template-columns: repeat(8, minmax(0, 1fr));
		gap: 0.22rem;
		max-height: min(42vh, 240px);
		overflow-y: auto;
		overflow-x: hidden;
		padding-right: 0.08rem;
	}

	.emoji-option {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 1.86rem;
		height: 1.86rem;
		border: none;
		background: transparent;
		border-radius: 8px;
		font-size: 1.16rem;
		line-height: 1;
		cursor: pointer;
	}

	.emoji-option:hover {
		background: var(--surface-hover);
	}

	.attach-menu {
		position: absolute;
		left: 0;
		bottom: calc(100% + 8px);
		background: var(--surface-primary);
		border: 1px solid var(--border-default);
		border-radius: 10px;
		box-shadow: var(--shadow-md);
		padding: 0.3rem;
		z-index: 120;
		min-width: 132px;
	}

	.attach-menu button {
		width: 100%;
		display: flex;
		align-items: center;
		gap: 0.4rem;
		border: none;
		background: transparent;
		padding: 0.45rem 0.55rem;
		cursor: pointer;
		border-radius: 7px;
		font-size: 0.84rem;
		color: var(--text-primary);
	}

	.attach-menu button:hover {
		background: var(--surface-hover);
	}

	.composer-input-wrap {
		position: relative;
		min-width: 0;
	}

	.composer-input-highlight {
		position: absolute;
		inset: 0;
		z-index: 0;
		pointer-events: none;
		overflow: auto;
		scrollbar-width: none;
	}

	.composer-input-highlight::-webkit-scrollbar {
		display: none;
	}

	.composer-input-highlight-content {
		min-height: 100%;
		padding: 0.44rem 0.56rem;
		font-size: 0.9rem;
		line-height: 1.32;
		font-family: inherit;
		box-sizing: border-box;
		white-space: pre-wrap;
		word-break: break-word;
		overflow-wrap: anywhere;
		color: var(--text-primary);
	}

	.composer-mention-token {
		color: #2563eb;
		font-weight: 600;
		text-decoration: none;
	}

	.composer[data-mode='dark'] .composer-mention-token {
		color: #9bc2ff;
	}

	.composer-input-wrap textarea {
		position: relative;
		z-index: 1;
		width: 100%;
		min-width: 0;
		resize: none;
		min-height: 2.1rem;
		max-height: none;
		border: 1px solid transparent;
		border-radius: 10px;
		padding: 0.44rem 0.56rem;
		font-size: 0.9rem;
		line-height: 1.32;
		font-family: inherit;
		background: transparent;
		color: transparent;
		-webkit-text-fill-color: transparent;
		caret-color: var(--text-primary);
		box-sizing: border-box;
		overflow-y: hidden;
	}

	.composer-input-wrap textarea:focus {
		outline: none;
		border-color: #aab3be;
		background: transparent;
	}

	.composer[data-mode='dark'] .composer-input-wrap textarea:focus {
		border-color: #737d89;
		background: transparent;
	}

	.composer-input-wrap textarea::placeholder {
		color: var(--text-placeholder);
	}

	.mention-picker {
		position: absolute;
		left: 0;
		right: 0;
		bottom: calc(100% + 8px);
		z-index: 118;
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		border-radius: 10px;
		box-shadow: var(--shadow-md);
		padding: 0.24rem;
		display: flex;
		flex-direction: column;
		gap: 0.14rem;
		max-height: min(220px, 38vh);
		overflow: auto;
	}

	.mention-option {
		display: flex;
		align-items: center;
		justify-content: space-between;
		width: 100%;
		border: none;
		background: transparent;
		border-radius: 8px;
		padding: 0.4rem 0.5rem;
		font-size: 0.82rem;
		color: var(--text-primary);
		cursor: pointer;
	}

	.mention-option.active,
	.mention-option:hover {
		background: var(--surface-hover);
	}

	.mention-option-label {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.mention-option-pill {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-width: 1.6rem;
		padding: 0.06rem 0.34rem;
		border-radius: 999px;
		border: 1px solid var(--border-default);
		font-size: 0.64rem;
		font-weight: 700;
		letter-spacing: 0.03em;
		color: var(--text-secondary);
	}

	@media (max-width: 700px) {
		.composer {
			padding: 0.56rem 0.58rem 0.62rem;
		}

		.composer-row {
			gap: 0.34rem;
		}

		.attach-button,
		.media-picker-button,
		.ai-button,
		.mic-button,
		.send-button {
			width: 2rem;
			height: 2rem;
		}

		.composer-input-wrap textarea {
			font-size: 0.86rem;
		}

		.media-picker-panel {
			max-height: min(48vh, 320px);
		}

		.media-card {
			min-height: 84px;
		}
	}

	@keyframes mic-pulse {
		0% {
			box-shadow: 0 0 0 0 var(--state-danger-border);
		}
		70% {
			box-shadow: 0 0 0 9px transparent;
		}
		100% {
			box-shadow: 0 0 0 0 transparent;
		}
	}
</style>
