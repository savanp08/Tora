<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { resolveApiBase } from '$lib/config/apiBase';

	type IntakeFieldType = 'text' | 'textarea' | 'number' | 'email' | 'select' | 'checkbox';

	type IntakeFormField = {
		field_id: string;
		label: string;
		field_type: IntakeFieldType;
		required: boolean;
		options?: string[];
	};

	type PublicIntakeForm = {
		form_id: string;
		title: string;
		description?: string;
		fields: IntakeFormField[];
	};

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = resolveApiBase(API_BASE_RAW);

	let loading = true;
	let loadingError = '';
	let submitError = '';
	let submitSuccess = false;
	let submitting = false;
	let form: PublicIntakeForm | null = null;
	let values: Record<string, unknown> = {};
	let submitterEmail = '';
	let fieldErrors: Record<string, string> = {};
	let lastLoadedFormId = '';

	$: formId = ($page.params.formId || '').trim();
	$: if (formId && formId !== lastLoadedFormId) {
		lastLoadedFormId = formId;
		void loadForm();
	}

	onMount(() => {
		if (formId) {
			void loadForm();
		}
	});

	async function parseError(response: Response) {
		const payload = (await response.json().catch(() => null)) as
			| {
					error?: string;
					message?: string;
					field_errors?: Record<string, string>;
			  }
			| null;
		return {
			message: payload?.error?.trim() || payload?.message?.trim() || `HTTP ${response.status}`,
			fieldErrors: payload?.field_errors || {}
		};
	}

	async function loadForm() {
		if (!formId) {
			loading = false;
			loadingError = 'This form is not available.';
			return;
		}
		loading = true;
		loadingError = '';
		submitError = '';
		submitSuccess = false;
		fieldErrors = {};
		values = {};
		submitterEmail = '';

		try {
			const response = await fetch(`${API_BASE}/api/f/${encodeURIComponent(formId)}`, {
				method: 'GET',
				credentials: 'include'
			});
			if (!response.ok) {
				throw new Error((await parseError(response)).message);
			}
			const payload = (await response.json().catch(() => null)) as PublicIntakeForm | null;
			if (!payload || !Array.isArray(payload.fields)) {
				throw new Error('This form is not available.');
			}
			form = payload;
			const nextValues: Record<string, unknown> = {};
			for (const field of payload.fields) {
				nextValues[field.field_id] = field.field_type === 'checkbox' ? false : '';
			}
			values = nextValues;
		} catch (error) {
			form = null;
			loadingError = error instanceof Error ? error.message : 'This form is not available.';
		} finally {
			loading = false;
		}
	}

	function updateFieldValue(fieldId: string, nextValue: unknown) {
		values = {
			...values,
			[fieldId]: nextValue
		};
		const nextFieldErrors = { ...fieldErrors };
		delete nextFieldErrors[fieldId];
		fieldErrors = nextFieldErrors;
	}

	function validateLocalFields() {
		if (!form) {
			return { valid: false, errors: { form: 'This form is not available.' } };
		}
		const errors: Record<string, string> = {};
		for (const field of form.fields) {
			const current = values[field.field_id];
			if (field.required) {
				if (field.field_type === 'checkbox') {
					if (current !== true) {
						errors[field.field_id] = 'Please check this field.';
					}
					continue;
				}
				if (typeof current !== 'string' || current.trim() === '') {
					errors[field.field_id] = 'This field is required.';
					continue;
				}
			}
			if (field.field_type === 'email' && typeof current === 'string' && current.trim() !== '') {
				const emailPattern = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
				if (!emailPattern.test(current.trim())) {
					errors[field.field_id] = 'Please enter a valid email.';
				}
			}
		}
		if (submitterEmail.trim() !== '') {
			const emailPattern = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
			if (!emailPattern.test(submitterEmail.trim())) {
				errors.submitter_email = 'Please enter a valid email.';
			}
		}
		return { valid: Object.keys(errors).length === 0, errors };
	}

	async function submitForm() {
		if (!form || submitting || submitSuccess) {
			return;
		}
		const validation = validateLocalFields();
		if (!validation.valid) {
			fieldErrors = validation.errors;
			submitError = 'Please correct the highlighted fields.';
			return;
		}

		submitting = true;
		submitError = '';
		fieldErrors = {};
		try {
			const response = await fetch(`${API_BASE}/api/f/${encodeURIComponent(form.form_id)}`, {
				method: 'POST',
				credentials: 'include',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					fields: values,
					submitter_email: submitterEmail.trim()
				})
			});
			if (!response.ok) {
				const parsedError = await parseError(response);
				fieldErrors = parsedError.fieldErrors;
				throw new Error(parsedError.message);
			}
			submitSuccess = true;
		} catch (error) {
			submitError = error instanceof Error ? error.message : 'Failed to submit the form.';
		} finally {
			submitting = false;
		}
	}
</script>

<section class="public-form-shell">
	<div class="public-form-card">
		{#if loading}
			<div class="state">Loading form...</div>
		{:else if loadingError || !form}
			<div class="state error">This form is not available.</div>
		{:else if submitSuccess}
			<div class="state success">Your submission has been received.</div>
		{:else}
			<header>
				<h1>{form.title}</h1>
				{#if form.description}
					<p>{form.description}</p>
				{/if}
			</header>

			<form class="public-form" on:submit|preventDefault={() => void submitForm()}>
				{#each form.fields as field (field.field_id)}
					<label class="field-wrap" class:has-error={Boolean(fieldErrors[field.field_id])}>
						<span>
							{field.label}
							{#if field.required}
								<sup>*</sup>
							{/if}
						</span>

						{#if field.field_type === 'textarea'}
							<textarea
								value={typeof values[field.field_id] === 'string'
									? (values[field.field_id] as string)
									: ''}
								on:input={(event) =>
									updateFieldValue(field.field_id, (event.currentTarget as HTMLTextAreaElement).value)}
							></textarea>
						{:else if field.field_type === 'number'}
							<input
								type="number"
								value={typeof values[field.field_id] === 'number'
									? String(values[field.field_id])
									: typeof values[field.field_id] === 'string'
										? (values[field.field_id] as string)
										: ''}
								on:input={(event) =>
									updateFieldValue(field.field_id, (event.currentTarget as HTMLInputElement).value)}
							/>
						{:else if field.field_type === 'email'}
							<input
								type="email"
								value={typeof values[field.field_id] === 'string'
									? (values[field.field_id] as string)
									: ''}
								on:input={(event) =>
									updateFieldValue(field.field_id, (event.currentTarget as HTMLInputElement).value)}
							/>
						{:else if field.field_type === 'select'}
							<select
								value={typeof values[field.field_id] === 'string'
									? (values[field.field_id] as string)
									: ''}
								on:change={(event) =>
									updateFieldValue(field.field_id, (event.currentTarget as HTMLSelectElement).value)}
							>
								<option value="">Select an option</option>
								{#each field.options || [] as option (option)}
									<option value={option}>{option}</option>
								{/each}
							</select>
						{:else if field.field_type === 'checkbox'}
							<div class="checkbox-wrap">
								<input
									type="checkbox"
									checked={Boolean(values[field.field_id])}
									on:change={(event) =>
										updateFieldValue(field.field_id, (event.currentTarget as HTMLInputElement).checked)}
								/>
								<span>Yes</span>
							</div>
						{:else}
							<input
								type="text"
								value={typeof values[field.field_id] === 'string'
									? (values[field.field_id] as string)
									: ''}
								on:input={(event) =>
									updateFieldValue(field.field_id, (event.currentTarget as HTMLInputElement).value)}
							/>
						{/if}

						{#if fieldErrors[field.field_id]}
							<small>{fieldErrors[field.field_id]}</small>
						{/if}
					</label>
				{/each}

				<label class="field-wrap" class:has-error={Boolean(fieldErrors.submitter_email)}>
					<span>Your email (optional)</span>
					<input type="email" bind:value={submitterEmail} />
					{#if fieldErrors.submitter_email}
						<small>{fieldErrors.submitter_email}</small>
					{/if}
				</label>

				{#if submitError}
					<div class="submit-error">{submitError}</div>
				{/if}

				<button type="submit" disabled={submitting}>
					{submitting ? 'Submitting...' : 'Submit'}
				</button>
			</form>
		{/if}
	</div>
</section>

<style>
	.public-form-shell {
		min-height: 100vh;
		display: grid;
		place-items: center;
		padding: 1.25rem 0.85rem;
		background:
			radial-gradient(circle at 0% 0%, rgba(15, 23, 42, 0.08), transparent 32%),
			radial-gradient(circle at 100% 0%, rgba(15, 23, 42, 0.05), transparent 36%),
			#f4f5f7;
	}

	.public-form-card {
		width: min(640px, 100%);
		border: 1px solid rgba(15, 23, 42, 0.14);
		border-radius: 14px;
		background: #ffffff;
		padding: 1.05rem;
		box-shadow: 0 16px 34px rgba(15, 23, 42, 0.1);
	}

	header h1 {
		margin: 0;
		font-size: 1.28rem;
		line-height: 1.2;
	}

	header p {
		margin: 0.36rem 0 0;
		color: #4b5563;
		font-size: 0.9rem;
		line-height: 1.35;
	}

	.public-form {
		margin-top: 0.95rem;
		display: grid;
		gap: 0.74rem;
	}

	.field-wrap {
		display: grid;
		gap: 0.28rem;
	}

	.field-wrap > span {
		font-size: 0.8rem;
		font-weight: 700;
		color: #111827;
	}

	.field-wrap > span sup {
		color: #dc2626;
		font-size: 0.7rem;
	}

	input,
	select,
	textarea {
		border: 1px solid #cbd5e1;
		border-radius: 9px;
		background: #ffffff;
		color: #0f172a;
		padding: 0.54rem 0.6rem;
		font-size: 0.88rem;
		font-family: inherit;
	}

	textarea {
		min-height: 5.6rem;
		resize: vertical;
	}

	.checkbox-wrap {
		display: inline-flex;
		align-items: center;
		gap: 0.38rem;
	}

	.checkbox-wrap span {
		font-size: 0.84rem;
		color: #334155;
	}

	.field-wrap.has-error input,
	.field-wrap.has-error select,
	.field-wrap.has-error textarea {
		border-color: #ef4444;
		background: rgba(239, 68, 68, 0.05);
	}

	.field-wrap small {
		color: #b91c1c;
		font-size: 0.73rem;
	}

	.submit-error {
		padding: 0.55rem 0.62rem;
		border-radius: 9px;
		border: 1px solid rgba(239, 68, 68, 0.34);
		background: rgba(239, 68, 68, 0.08);
		color: #991b1b;
		font-size: 0.78rem;
	}

	button[type='submit'] {
		height: 2.2rem;
		border: 1px solid #334155;
		border-radius: 10px;
		background: #0f172a;
		color: #f8fafc;
		font-size: 0.82rem;
		font-weight: 700;
		cursor: pointer;
	}

	button[type='submit']:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	.state {
		padding: 1.25rem 0.7rem;
		text-align: center;
		font-size: 0.92rem;
		color: #334155;
	}

	.state.error {
		color: #991b1b;
	}

	.state.success {
		color: #166534;
	}
</style>
