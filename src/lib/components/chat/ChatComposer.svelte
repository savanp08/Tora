<script lang="ts">
	import IconSet from '$lib/components/icons/IconSet.svelte';
	import AiDisclaimerModal from '$lib/components/chat/AiDisclaimerModal.svelte';
	import { getUTF8ByteLength, MESSAGE_TEXT_MAX_BYTES } from '$lib/utils/chat/core';
	import {
		compressMedia,
		inferMediaMessageType,
		uploadToR2,
		type MediaMessageType
	} from '$lib/utils/media';
	import type { ReplyTarget, TaskChecklistItem } from '$lib/types/chat';
	import { stringifyTaskMessagePayload } from '$lib/utils/chat/task';
	import { createEventDispatcher, onDestroy, onMount } from 'svelte';
	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';
	const AI_TERMS_STORAGE_KEY = 'hasAcceptedAITerms';
	const AI_PRIVACY_POLICY_URL = 'https://example.com/privacy-policy';
	const AI_PRIMARY_MENTION = '@ToraAI';
	const AI_MENTION_TOKENS = ['@ToraAI', '@Tora'];
	const AI_DISPLAY_NAME = 'ToraAI';
	const KLIPY_API_KEY_RAW = import.meta.env.VITE_KLIPY_API_KEY as string | undefined;
	const KLIPY_API_KEY = KLIPY_API_KEY_RAW?.trim() ?? '';
	const KLIPY_CLIENT_KEY = 'converse-web';
	const KLIPY_SEARCH_LIMIT = 24;
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

	type GifResult = {
		id: string;
		url: string;
		previewUrl: string;
		title: string;
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
	export let disabled = false;
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
	let gifPickerEl: HTMLDivElement | null = null;
	let emojiWrapEl: HTMLDivElement | null = null;
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
	let isRecording = false;
	let mediaRecorder: MediaRecorder | null = null;
	let audioChunks: Blob[] = [];
	let recordingStream: MediaStream | null = null;
	let showGifPicker = false;
	let showEmojiPicker = false;
	let gifQuery = '';
	let gifResults: GifResult[] = [];
	let gifLoading = false;
	let gifError = '';
	let gifSearchTimer: ReturnType<typeof setTimeout> | null = null;
	let gifAbortController: AbortController | null = null;
	let attachedGif: GifResult | null = null;
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
	$: hasPendingAttachment = Boolean(attachedFile || attachedGif);
	$: showSendButton =
		!isRecording && !taskDraftOpen && (hasPendingAttachment || normalizedDraftMessage.length > 0);
	$: composerDisabled = disabled || isProcessingAttachment || isRecording || taskDraftOpen;
	$: composerPlaceholder = disabled
		? 'This room has expired. Extend time to continue chatting.'
		: isRecording
			? 'Recording... Click mic to send.'
			: taskDraftOpen
				? 'Task mode active. Press send when ready.'
				: hasPendingAttachment
					? 'Add a caption (optional)'
					: 'Type a message';
	$: if (hasComposerInput && showEmojiPicker) {
		closeEmojiPicker();
	}

	const dispatch = createEventDispatcher<{
		send:
			| { type: MediaMessageType | 'task'; content: string; fileName?: string; text?: string }
			| undefined;
		attach: { file: File | null; type: 'media' | 'file'; error?: string };
		removeAttachment: void;
		cancelReply: void;
		typing: { value: string };
		openPrivateAi: void;
	}>();

	function closeGifPicker(resetQuery = false) {
		showGifPicker = false;
		if (gifSearchTimer) {
			clearTimeout(gifSearchTimer);
			gifSearchTimer = null;
		}
		gifAbortController?.abort();
		gifAbortController = null;
		gifLoading = false;
		if (resetQuery) {
			gifQuery = '';
		}
	}

	function closeEmojiPicker() {
		showEmojiPicker = false;
	}

	function emitTypingValue() {
		if (disabled) {
			return;
		}
		dispatch('typing', { value: draftMessage });
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
		if (taskDraftOpen) {
			return false;
		}
		const textToSend = (draftMessage || '').trim();
		if (attachedGif) {
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
		closeGifPicker();
		closeEmojiPicker();
	}

	function onAIButtonClick() {
		if (composerDisabled) {
			return;
		}
		closeMentionPicker();
		showAttachMenu = false;
		closeGifPicker();
		closeEmojiPicker();
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
		closeGifPicker();
		closeEmojiPicker();
		closeMentionPicker();
		if (isRecording && mediaRecorder && mediaRecorder.state !== 'inactive') {
			mediaRecorder.stop();
		}
		stopRecordingStream();
	});

	onMount(() => {
		hasAcceptedAITerms = loadHasAcceptedAITerms();
		requestAnimationFrame(() => {
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
			if (showGifPicker && gifPickerEl && !gifPickerEl.contains(target)) {
				closeGifPicker();
			}
			if (showEmojiPicker && emojiWrapEl && !emojiWrapEl.contains(target)) {
				closeEmojiPicker();
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
		if (showGifPicker) {
			closeGifPicker();
		}
		if (showEmojiPicker) {
			closeEmojiPicker();
		}
		showAttachMenu = !showAttachMenu;
	}

	function toggleEmojiPicker() {
		if (composerDisabled) {
			return;
		}
		closeMentionPicker();
		showAttachMenu = false;
		closeGifPicker();
		showEmojiPicker = !showEmojiPicker;
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

	function chooseAttachmentType(type: 'media' | 'file' | 'task' | 'gif') {
		if (disabled) {
			return;
		}
		closeMentionPicker();
		showAttachMenu = false;
		closeEmojiPicker();
		attachError = '';
		taskDraftError = '';
		if (type !== 'gif') {
			gifError = '';
		}
		if (type === 'task') {
			closeGifPicker();
			clearAttachmentPreview();
			attachedFile = null;
			attachedGif = null;
			attachedMessageType = null;
			dispatch('attach', { file: null, type: 'file' });
			taskDraftOpen = true;
			taskAddInputOpen = false;
			if (taskDraftTitle.trim() === '') {
				taskDraftTitle = 'Task';
			}
			return;
		}
		if (type === 'gif') {
			taskDraftOpen = false;
			taskAddInputOpen = false;
			clearAttachmentPreview();
			attachedFile = null;
			attachedGif = null;
			attachedMessageType = null;
			dispatch('attach', { file: null, type: 'file' });
			if (!KLIPY_API_KEY) {
				const message = 'GIF search is unavailable. Add VITE_KLIPY_API_KEY to enable it.';
				gifError = message;
				attachError = message;
				closeGifPicker();
				return;
			}
			showGifPicker = true;
			if (gifResults.length === 0) {
				void fetchTrendingGifs();
			}
			return;
		}
		closeGifPicker();
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

	function parseKlipyGifResults(payload: unknown): GifResult[] {
		const source = toRecord(payload);
		if (!source) {
			return [];
		}
		const entriesRaw = Array.isArray(source.results)
			? source.results
			: Array.isArray(source.data)
				? source.data
				: Array.isArray(source.gifs)
					? source.gifs
					: [];
		const items: GifResult[] = [];
		for (let index = 0; index < entriesRaw.length; index += 1) {
			const entry = toRecord(entriesRaw[index]);
			if (!entry) {
				continue;
			}
			const mediaFormats = toRecord(entry.media_formats);
			const images = toRecord(entry.images);
			const previewFromMediaFormats = readMediaUrl(mediaFormats, [
				'tinygif',
				'nanogif',
				'tinywebp',
				'nanowebp',
				'previewgif',
				'preview'
			]);
			const gifFromMediaFormats = readMediaUrl(mediaFormats, [
				'gif',
				'mediumgif',
				'fullgif',
				'largegif',
				'original'
			]);
			const previewFromImages = readMediaUrl(images, [
				'preview_gif',
				'fixed_width_small',
				'downsized_small',
				'preview',
				'tiny'
			]);
			const gifFromImages = readMediaUrl(images, [
				'original',
				'downsized_large',
				'downsized',
				'fixed_width',
				'gif'
			]);
			const directPreview =
				toTrimmedString(entry.preview_url) || toTrimmedString(entry.thumbnail_url);
			const directGif =
				toTrimmedString(entry.url) ||
				toTrimmedString(entry.gif_url) ||
				toTrimmedString(entry.media_url);
			const previewUrl = previewFromMediaFormats || previewFromImages || directPreview || directGif;
			const url = gifFromMediaFormats || gifFromImages || directGif || previewUrl;
			if (!url) {
				continue;
			}
			const id =
				toTrimmedString(entry.id) || toTrimmedString(entry.gif_id) || `gif_${Date.now()}_${index}`;
			const title =
				toTrimmedString(entry.content_description) ||
				toTrimmedString(entry.title) ||
				toTrimmedString(entry.alt_text) ||
				'GIF';
			items.push({
				id,
				url,
				previewUrl: previewUrl || url,
				title
			});
		}
		return items;
	}

	async function fetchKlipyGifs(query: string) {
		if (!KLIPY_API_KEY) {
			return;
		}
		gifAbortController?.abort();
		gifAbortController = new AbortController();
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
			if (query) {
				params.set('q', query);
			}
			const response = await fetch(`https://api.klipy.com${endpointPath}?${params.toString()}`, {
				signal: gifAbortController.signal
			});
			const payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
			if (!response.ok) {
				throw new Error(
					typeof payload.error === 'string'
						? payload.error
						: `GIF request failed (${response.status})`
				);
			}
			gifResults = parseKlipyGifResults(payload);
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

	async function fetchTrendingGifs() {
		await fetchKlipyGifs('');
	}

	function onGifQueryInput() {
		if (!showGifPicker || !KLIPY_API_KEY) {
			return;
		}
		if (gifSearchTimer) {
			clearTimeout(gifSearchTimer);
			gifSearchTimer = null;
		}
		const normalizedQuery = gifQuery.trim();
		gifSearchTimer = setTimeout(() => {
			void fetchKlipyGifs(normalizedQuery);
		}, 300);
	}

	function toGifFileName(gif: GifResult) {
		const normalizedTitle = (gif.title || 'gif').trim();
		const safeBaseName = normalizedTitle
			.replace(/\.[^./\\\s]+$/, '')
			.replace(/[^a-zA-Z0-9-_ ]+/g, '')
			.trim()
			.replace(/\s+/g, '-')
			.slice(0, 64);
		return `${safeBaseName || 'gif'}.gif`;
	}

	function selectGifAttachment(gif: GifResult) {
		if (!gif || !gif.url || disabled || isProcessingAttachment || isRecording) {
			return;
		}
		clearAttachmentPreview();
		attachedFile = null;
		attachedGif = gif;
		attachedMessageType = 'image';
		attachedPickerType = 'media';
		attachError = '';
		gifError = '';
		closeGifPicker();
		dispatch('attach', { file: null, type: 'media' });
	}

	function sendGifAttachment() {
		if (!attachedGif) {
			dispatch('send', undefined);
			return;
		}
		dispatch('send', {
			type: 'image',
			content: attachedGif.url,
			fileName: toGifFileName(attachedGif),
			text: draftMessage.trim()
		});
		draftMessage = '';
		attachedGif = null;
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
		showGifPicker = false;
		const target = event.currentTarget as HTMLInputElement;
		const selected = target.files?.[0] ?? null;
		target.value = '';
		if (!selected) {
			return;
		}

		const messageType = resolveMessageType(selected, pickerType);
		attachError = '';
		attachedFile = selected;
		attachedGif = null;
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
		attachedGif = null;
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
		if (taskDraftOpen) {
			submitTaskDraft();
			return;
		}
		if (attachedFile) {
			void sendAttachment();
			return;
		}
		if (attachedGif) {
			sendGifAttachment();
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

	function onComposerInput() {
		syncComposerHighlightScroll();
		emitTypingValue();
		updateMentionSuggestionsFromCaret();
	}

	function onComposerCursorActivity() {
		syncComposerHighlightScroll();
		updateMentionSuggestionsFromCaret();
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
		if (disabled || isProcessingAttachment || attachedFile || attachedGif || taskDraftOpen) {
			return;
		}
		closeEmojiPicker();

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

<footer class="composer" data-mode={isDarkMode ? 'dark' : 'light'}>
	{#if activeReply}
		<div class="reply-preview-panel">
			<div class="reply-preview-label">Replying to</div>
			<div class="reply-preview-content">{getReplyPreviewText()}</div>
			<button type="button" class="reply-preview-cancel" on:click={cancelReply}>Cancel</button>
		</div>
	{/if}
	{#if attachedFile || attachedGif}
		<div class="attachment-preview-panel">
			<div class="attachment-preview-header">
				<div class="attachment-preview-title">{getAttachmentLabel(attachedMessageType)}</div>
				<button type="button" class="preview-remove" on:click={removeAttachment}>x</button>
			</div>
			{#if attachedGif}
				<img
					src={attachedGif.previewUrl || attachedGif.url}
					alt={attachedGif.title || 'GIF'}
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
	{#if showGifPicker}
		<div class="gif-picker-panel" bind:this={gifPickerEl}>
			<div class="gif-picker-header">
				<input
					type="text"
					placeholder="Search GIFs"
					bind:value={gifQuery}
					on:input={onGifQueryInput}
				/>
				<button
					type="button"
					class="gif-picker-close"
					on:click={() => closeGifPicker()}
					aria-label="Close GIF picker"
				>
					Close
				</button>
			</div>
			{#if gifError}
				<div class="gif-picker-error">{gifError}</div>
			{:else if gifLoading}
				<div class="gif-picker-loading">Loading GIFs...</div>
			{:else if gifResults.length === 0}
				<div class="gif-picker-empty">No GIFs found. Try another search.</div>
			{:else}
				<div class="gif-grid">
					{#each gifResults as gif (gif.id)}
						<button
							type="button"
							class="gif-card"
							on:click={() => selectGifAttachment(gif)}
							title={`Attach GIF: ${gif.title}`}
							aria-label={`Attach GIF: ${gif.title}`}
						>
							<img src={gif.previewUrl} alt={gif.title || 'GIF'} loading="lazy" />
						</button>
					{/each}
				</div>
			{/if}
		</div>
	{/if}
	{#if attachError}
		<div class="attachment-error">{attachError}</div>
	{/if}
	{#if isProcessingAttachment}
		<div class="attachment-progress">Compressing &amp; Uploading...</div>
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
				disabled={disabled || isProcessingAttachment || isRecording}
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
					<button type="button" on:click={() => chooseAttachmentType('gif')}>
						<span class="gif-pill">GIF</span>
						<span>GIF</span>
					</button>
				</div>
			{/if}
		</div>
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
					<path d="M12 2.75 14.5 8.2l5.95.8-4.4 4.15 1.16 5.85L12 16.3l-5.21 2.7 1.16-5.85L3.55 9l5.95-.8Z"></path>
				</svg>
			</button>
			<div class="emoji-wrap" class:slot-hidden={hasComposerInput} bind:this={emojiWrapEl}>
				<button
					type="button"
					class="emoji-button"
					on:click={toggleEmojiPicker}
					disabled={composerDisabled || hasComposerInput}
					aria-hidden={hasComposerInput}
					aria-label="Insert emoji"
					title="Insert emoji"
				>
				<span aria-hidden="true">😊</span>
			</button>
			{#if showEmojiPicker}
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
			{/if}
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
				<div class="mention-picker" bind:this={mentionPickerEl} role="listbox" aria-label="Mention suggestions">
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
				disabled={disabled || isProcessingAttachment || hasPendingAttachment || taskDraftOpen}
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
		width: min(100%, 320px);
		max-height: 230px;
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
		font-size: 0.79rem;
		color: var(--accent-primary);
		background: var(--state-info-bg);
		border: 1px solid var(--state-info-border);
		border-radius: 8px;
		padding: 0.36rem 0.5rem;
	}

	.gif-picker-panel {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		border-radius: 12px;
		padding: 0.56rem;
		display: flex;
		flex-direction: column;
		gap: 0.46rem;
		max-height: min(54vh, 380px);
		overflow: hidden;
	}

	.gif-picker-header {
		display: flex;
		align-items: center;
		gap: 0.45rem;
	}

	.gif-picker-header input {
		flex: 1;
		min-width: 0;
		border: 1px solid var(--border-default);
		background: var(--surface-secondary);
		color: var(--text-primary);
		border-radius: 9px;
		padding: 0.34rem 0.52rem;
		font-size: 0.8rem;
	}

	.gif-picker-close {
		border: 1px solid var(--border-default);
		background: var(--surface-secondary);
		color: var(--text-secondary);
		border-radius: 8px;
		padding: 0.3rem 0.5rem;
		font-size: 0.72rem;
		cursor: pointer;
	}

	.gif-picker-loading,
	.gif-picker-empty,
	.gif-picker-error {
		font-size: 0.78rem;
		color: var(--text-secondary);
	}

	.gif-picker-error {
		color: var(--accent-danger);
	}

	.gif-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(96px, 1fr));
		gap: 0.42rem;
		overflow: auto;
		padding-right: 0.08rem;
	}

	.gif-card {
		border: 1px solid var(--border-default);
		background: var(--surface-secondary);
		border-radius: 9px;
		padding: 0;
		overflow: hidden;
		cursor: pointer;
		aspect-ratio: 1 / 1;
	}

	.gif-card img {
		display: block;
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

	@media (max-width: 640px) {
		.task-draft-card {
			width: min(100%, 100vw - 1rem);
			max-height: min(88vh, 760px);
			padding: 0.62rem;
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
		grid-template-columns: 2.2rem 0 0 minmax(0, 1fr) 2.2rem;
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
	.emoji-button,
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
	.emoji-button:disabled,
	.ai-button:disabled,
	.mic-button:disabled,
	.send-button:disabled {
		opacity: 0.7;
		cursor: not-allowed;
	}

	.attach-button:hover:not(:disabled),
	.emoji-button:hover:not(:disabled),
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

	.emoji-wrap {
		position: relative;
	}

	.emoji-button {
		font-size: 1.1rem;
		line-height: 1;
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

	.ai-button.slot-hidden,
	.emoji-wrap.slot-hidden {
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

	.emoji-wrap.slot-hidden {
		width: 0;
		overflow: hidden;
	}

	.emoji-wrap.slot-hidden .emoji-button {
		width: 0;
		height: 0;
		border: 0;
		padding: 0;
	}

	.emoji-picker {
		position: absolute;
		left: 0;
		bottom: calc(100% + 8px);
		z-index: 121;
		display: grid;
		grid-template-columns: repeat(8, minmax(0, 1fr));
		gap: 0.22rem;
		width: min(18rem, calc(100vw - 1.6rem));
		max-height: min(40vh, 220px);
		overflow: auto;
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		border-radius: 10px;
		padding: 0.38rem;
		box-shadow: var(--shadow-md);
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

	.gif-pill {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-width: 2.1rem;
		padding: 0.08rem 0.32rem;
		border-radius: 999px;
		border: 1px solid var(--border-default);
		font-size: 0.66rem;
		font-weight: 700;
		letter-spacing: 0.02em;
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
		max-height: 110px;
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
		.emoji-button,
		.ai-button,
		.mic-button,
		.send-button {
			width: 2rem;
			height: 2rem;
		}

		.composer-input-wrap textarea {
			font-size: 0.86rem;
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
