import type { RoomMenuMode, UiDialogState } from '$lib/types/chat';

type ConfirmDialogConfig = {
	title: string;
	message: string;
	confirmLabel?: string;
	cancelLabel?: string;
	danger?: boolean;
};

type PromptDialogConfig = {
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
};

export function buildConfirmDialog(config: ConfirmDialogConfig): UiDialogState {
	return {
		kind: 'confirm',
		title: config.title,
		message: config.message,
		confirmLabel: config.confirmLabel || 'Confirm',
		cancelLabel: config.cancelLabel || 'Cancel',
		danger: Boolean(config.danger)
	};
}

export function buildPromptDialog(config: PromptDialogConfig): UiDialogState {
	return {
		kind: 'prompt',
		title: config.title,
		message: config.message,
		value: config.initialValue ?? '',
		placeholder: config.placeholder ?? '',
		maxLength: Math.max(1, config.maxLength ?? 2000),
		confirmLabel: config.confirmLabel || 'Save',
		emptyConfirmLabel: config.emptyConfirmLabel || config.confirmLabel || 'Save',
		cancelLabel: config.cancelLabel || 'Cancel',
		danger: Boolean(config.danger),
		multiline: Boolean(config.multiline),
		allowEmptySubmit: Boolean(config.allowEmptySubmit)
	};
}

export function buildRoomActionDialog(
	initialName: string,
	normalizeRoomNameValue: (value: string) => string
): UiDialogState {
	return {
		kind: 'roomAction',
		title: 'Open Room',
		message: 'Choose whether to create a new room or join an existing one.',
		roomName: normalizeRoomNameValue(initialName),
		mode: 'create',
		confirmLabel: 'Continue',
		cancelLabel: 'Cancel'
	};
}

export function resolveCloseDialogValue(dialog: UiDialogState): unknown {
	switch (dialog.kind) {
		case 'confirm':
			return false;
		case 'prompt':
			return null;
		case 'roomAction':
			return null;
		default:
			return null;
	}
}

export function resolveConfirmDialogValue(dialog: UiDialogState): unknown {
	if (dialog.kind === 'confirm') {
		return true;
	}
	if (dialog.kind === 'prompt') {
		return dialog.value;
	}
	if (dialog.kind === 'roomAction') {
		return {
			mode: dialog.mode,
			roomName: dialog.roomName
		};
	}
	return null;
}

export function updatePromptDialogValue(dialog: UiDialogState, value: string): UiDialogState {
	if (dialog.kind !== 'prompt') {
		return dialog;
	}
	return {
		...dialog,
		value: value.slice(0, dialog.maxLength)
	};
}

export function updateRoomActionDialogMode(
	dialog: UiDialogState,
	mode: RoomMenuMode
): UiDialogState {
	if (dialog.kind !== 'roomAction') {
		return dialog;
	}
	return {
		...dialog,
		mode
	};
}

export function updateRoomActionDialogName(dialog: UiDialogState, value: string): UiDialogState {
	if (dialog.kind !== 'roomAction') {
		return dialog;
	}
	return {
		...dialog,
		roomName: value.slice(0, 20)
	};
}
