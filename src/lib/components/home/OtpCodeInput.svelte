<script lang="ts">
	import { APP_LIMITS } from '$lib/config/limits';
	import { sanitizeRoomCodePartial } from '$lib/utils/homeJoin';

	export let value = '';
	export let disabled = false;
	export let idPrefix = 'otp-digit';

	const CODE_LENGTH = APP_LIMITS.room.codeDigits;
	let digits = Array.from({ length: CODE_LENGTH }, () => '');
	let rootEl: HTMLDivElement | null = null;

	$: {
		const normalized = sanitizeRoomCodePartial(value);
		const nextDigits = Array.from({ length: CODE_LENGTH }, (_, index) => normalized[index] || '');
		if (nextDigits.join('') !== digits.join('')) {
			digits = nextDigits;
		}
	}

	function applyAndEmit(nextDigits: string[]) {
		digits = nextDigits;
		value = digits.join('');
	}

	function focusDigit(index: number) {
		if (index < 0 || index >= CODE_LENGTH || !rootEl) {
			return;
		}
		const nextInput = rootEl.querySelector<HTMLInputElement>(`#${idPrefix}-${index}`);
		if (!nextInput) {
			return;
		}
		nextInput.focus();
		nextInput.select();
	}

	function fillDigitsFrom(startIndex: number, rawValue: string) {
		const normalized = sanitizeRoomCodePartial(rawValue);
		const nextDigits = [...digits];
		let cursor = startIndex;
		for (const char of normalized) {
			if (cursor >= CODE_LENGTH) {
				break;
			}
			nextDigits[cursor] = char;
			cursor += 1;
		}
		return { nextDigits, cursor };
	}

	function onDigitInput(index: number, event: Event) {
		const input = event.currentTarget as HTMLInputElement;
		const entered = sanitizeRoomCodePartial(input.value);
		if (!entered) {
			const nextDigits = [...digits];
			nextDigits[index] = '';
			applyAndEmit(nextDigits);
			return;
		}

		const { nextDigits, cursor } = fillDigitsFrom(index, entered);
		applyAndEmit(nextDigits);
		if (cursor < CODE_LENGTH) {
			focusDigit(cursor);
			return;
		}
		input.blur();
	}

	function onDigitKeyDown(index: number, event: KeyboardEvent) {
		if (event.key === 'Backspace') {
			event.preventDefault();
			const nextDigits = [...digits];
			if (nextDigits[index]) {
				nextDigits[index] = '';
				applyAndEmit(nextDigits);
				return;
			}
			if (index > 0) {
				nextDigits[index - 1] = '';
				applyAndEmit(nextDigits);
				focusDigit(index - 1);
			}
			return;
		}
		if (event.key === 'ArrowLeft') {
			event.preventDefault();
			focusDigit(index - 1);
			return;
		}
		if (event.key === 'ArrowRight') {
			event.preventDefault();
			focusDigit(index + 1);
			return;
		}
		if (event.key === ' ') {
			event.preventDefault();
		}
	}

	function onDigitPaste(index: number, event: ClipboardEvent) {
		event.preventDefault();
		const pasted = event.clipboardData?.getData('text') || '';
		if (!pasted) {
			return;
		}
		const { nextDigits, cursor } = fillDigitsFrom(index, pasted);
		applyAndEmit(nextDigits);
		if (cursor < CODE_LENGTH) {
			focusDigit(cursor);
		}
	}
</script>

<div
	class="otp-row"
	role="group"
	aria-label={`${CODE_LENGTH}-digit room code`}
	style={`--otp-columns:${CODE_LENGTH};`}
	bind:this={rootEl}
>
	{#each digits as digit, index (index)}
		<input
			type="text"
			class="otp-digit"
			id={`${idPrefix}-${index}`}
			data-otp-digit={index}
			inputmode="numeric"
			pattern="[0-9]*"
			maxlength="1"
			value={digit}
			{disabled}
			on:input={(event) => onDigitInput(index, event)}
			on:keydown={(event) => onDigitKeyDown(index, event)}
			on:paste={(event) => onDigitPaste(index, event)}
		/>
	{/each}
</div>

<style>
	.otp-row {
		display: flex;
		flex-wrap: nowrap;
		align-items: center;
		gap: 0.35rem;
		max-width: 100%;
	}

	.otp-digit {
		width: 2.2rem;
		height: 2.35rem;
		padding: 0;
		text-align: center;
		border: 1px solid #d4d8e0;
		border-radius: 8px;
		font-size: 0.96rem;
		font-weight: 700;
		line-height: 1;
		font-variant-numeric: tabular-nums;
		background: #ffffff;
		color: #111827;
		flex: 0 0 auto;
	}

	.otp-digit:focus {
		outline: none;
		border-color: #16a34a;
		box-shadow: 0 0 0 2px rgba(22, 163, 74, 0.2);
	}

	.otp-digit:disabled {
		background: #f3f4f6;
		color: #9ca3af;
	}

	@media (max-width: 420px) {
		.otp-row {
			display: grid;
			grid-template-columns: repeat(var(--otp-columns), minmax(0, 1fr));
			gap: 0.32rem;
		}

		.otp-digit {
			width: 100%;
		}
	}
</style>
