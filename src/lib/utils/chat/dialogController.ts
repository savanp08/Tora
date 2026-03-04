import type { RoomMenuMode, UiDialogState } from '$lib/types/chat';
import {
	buildConfirmDialog,
	buildPromptDialog,
	buildRoomActionDialog,
	resolveCloseDialogValue,
	resolveConfirmDialogValue,
	updatePromptDialogValue,
	updateRoomActionDialogMode,
	updateRoomActionDialogName
} from '$lib/utils/chat/dialogState';

type DialogControllerParams = {
	getDialog: () => UiDialogState;
	setDialog: (next: UiDialogState) => void;
	normalizeRoomNameValue: (value: string) => string;
};

export function createChatDialogController({
	getDialog,
	setDialog,
	normalizeRoomNameValue
}: DialogControllerParams) {
	let dialogResolver: ((value: unknown) => void) | null = null;

	function resolveActiveUiDialog(value: unknown) {
		const resolver = dialogResolver;
		dialogResolver = null;
		setDialog({ kind: 'none' });
		if (resolver) {
			resolver(value);
		}
	}

	function closeUiDialog() {
		resolveActiveUiDialog(resolveCloseDialogValue(getDialog()));
	}

	function onUiDialogConfirm() {
		resolveActiveUiDialog(resolveConfirmDialogValue(getDialog()));
	}

	function openConfirmDialog(config: {
		title: string;
		message: string;
		confirmLabel?: string;
		cancelLabel?: string;
		danger?: boolean;
	}) {
		resolveActiveUiDialog(false);
		setDialog(buildConfirmDialog(config));
		return new Promise<boolean>((resolve) => {
			dialogResolver = (value) => resolve(Boolean(value));
		});
	}

	function openPromptDialog(config: {
		title: string;
		message: string;
		initialValue?: string;
		placeholder?: string;
		maxLength?: number;
		confirmLabel?: string;
		emptyConfirmLabel?: string;
		cancelLabel?: string;
		danger?: boolean;
		multiline?: boolean;
		allowEmptySubmit?: boolean;
	}) {
		resolveActiveUiDialog(null);
		setDialog(buildPromptDialog(config));
		return new Promise<string | null>((resolve) => {
			dialogResolver = (value) => {
				if (typeof value === 'string') {
					resolve(value);
					return;
				}
				resolve(null);
			};
		});
	}

	function openRoomActionDialog(initialName = '') {
		resolveActiveUiDialog(null);
		setDialog(buildRoomActionDialog(initialName, normalizeRoomNameValue));
		return new Promise<{ mode: RoomMenuMode; roomName: string } | null>((resolve) => {
			dialogResolver = (value) => {
				if (
					value &&
					typeof value === 'object' &&
					'mode' in value &&
					'roomName' in value &&
					typeof (value as { mode?: unknown }).mode === 'string'
				) {
					const parsed = value as { mode: RoomMenuMode; roomName: string };
					resolve(parsed);
					return;
				}
				resolve(null);
			};
		});
	}

	function updateUiPromptValue(value: string) {
		setDialog(updatePromptDialogValue(getDialog(), value));
	}

	function updateRoomActionMode(mode: RoomMenuMode) {
		setDialog(updateRoomActionDialogMode(getDialog(), mode));
	}

	function updateRoomActionName(value: string) {
		setDialog(updateRoomActionDialogName(getDialog(), value));
	}

	return {
		closeUiDialog,
		onUiDialogConfirm,
		openConfirmDialog,
		openPromptDialog,
		openRoomActionDialog,
		updateUiPromptValue,
		updateRoomActionMode,
		updateRoomActionName
	};
}
