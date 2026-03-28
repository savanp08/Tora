<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import {
		changeRequestStore,
		genCRId,
		type ChangeRequest,
		type ChangeRequestStatus,
		type CRComment,
		type CRCommentReply
	} from '$lib/stores/changeRequests';

	export let open = false;
	export let roomId = '';
	export let isAdmin = false;
	export let sessionUserID = '';
	export let sessionUserName = '';

	const dispatch = createEventDispatcher<{
		approve: { req: ChangeRequest };
		reject: { req: ChangeRequest };
		close: void;
	}>();

	type FilterTab = 'pending' | 'resolved';
	let filterTab: FilterTab = 'pending';

	let previewOpenId = '';
	let discussionOpenId = '';
	let noteOpenId = '';
	let noteText = '';
	let commentDraft: Record<string, string> = {};
	let replyDraft: Record<string, string> = {};
	let replyTargetId: Record<string, string> = {};

	$: allRequests = $changeRequestStore.get(roomId) ?? [];
	$: pending = allRequests.filter((r) => r.status === 'pending');
	$: resolved = allRequests.filter((r) => r.status !== 'pending');
	$: shown = filterTab === 'pending' ? pending : resolved;

	const ACTION_LABEL: Record<string, string> = {
		add_task: 'Add task',
		edit_task: 'Edit task',
		delete_task: 'Delete task',
		add_sprint: 'Add sprint',
		edit_sprint: 'Edit sprint',
		delete_sprint: 'Delete sprint',
		edit_timeline: 'Edit timeline',
		edit_cost: 'Edit cost / budget',
		import_sheet: 'Import spreadsheet',
		edit_field_schema: 'Edit custom field',
		remove_member: 'Remove member'
	};

	const STATUS_COLOR: Record<ChangeRequestStatus, string> = {
		pending: '#f59e0b',
		approved: '#10b981',
		rejected: '#ef4444'
	};

	function timeAgo(iso: string): string {
		const ms = Date.now() - new Date(iso).getTime();
		const m = Math.floor(ms / 60000);
		if (m < 1) return 'just now';
		if (m < 60) return `${m}m ago`;
		const h = Math.floor(m / 60);
		if (h < 24) return `${h}h ago`;
		return `${Math.floor(h / 24)}d ago`;
	}

	function approve(req: ChangeRequest) {
		changeRequestStore.resolve(roomId, req.id, 'approved', sessionUserName);
		dispatch('approve', { req });
		noteOpenId = '';
	}

	function reject(req: ChangeRequest) {
		changeRequestStore.resolve(roomId, req.id, 'rejected', sessionUserName);
		dispatch('reject', { req });
		noteOpenId = '';
	}

	function submitWithNote(req: ChangeRequest, status: 'approved' | 'rejected') {
		changeRequestStore.resolve(roomId, req.id, status, sessionUserName, noteText.trim() || undefined);
		dispatch(status === 'approved' ? 'approve' : 'reject', { req });
		noteOpenId = '';
		noteText = '';
	}

	function close() { dispatch('close'); }
	function handleKeydown(e: KeyboardEvent) { if (e.key === 'Escape') close(); }

	function togglePreview(id: string) { previewOpenId = previewOpenId === id ? '' : id; }
	function toggleDiscussion(id: string) {
		discussionOpenId = discussionOpenId === id ? '' : id;
		noteOpenId = '';
	}
	function toggleNote(id: string) {
		noteOpenId = noteOpenId === id ? '' : id;
		noteText = '';
		discussionOpenId = '';
	}

	function addComment(req: ChangeRequest) {
		const text = (commentDraft[req.id] || '').trim();
		if (!text) return;
		const comment: Omit<CRComment, 'replies'> = {
			id: genCRId(), userId: sessionUserID, userName: sessionUserName,
			text, createdAt: new Date().toISOString(), isPinned: false, isHighlighted: false
		};
		changeRequestStore.addComment(roomId, req.id, comment);
		commentDraft = { ...commentDraft, [req.id]: '' };
	}

	function addReply(req: ChangeRequest, commentId: string) {
		const key = `${req.id}:${commentId}`;
		const text = (replyDraft[key] || '').trim();
		if (!text) return;
		const reply: CRCommentReply = {
			id: genCRId(), userId: sessionUserID, userName: sessionUserName,
			text, createdAt: new Date().toISOString()
		};
		changeRequestStore.addReply(roomId, req.id, commentId, reply);
		replyDraft = { ...replyDraft, [key]: '' };
		replyTargetId = { ...replyTargetId, [req.id]: '' };
	}

	function togglePin(req: ChangeRequest, commentId: string) {
		changeRequestStore.togglePin(roomId, req.id, commentId);
	}
	function toggleHighlight(req: ChangeRequest, commentId: string) {
		changeRequestStore.toggleHighlight(roomId, req.id, commentId);
	}

	function pinnedFirst(comments: CRComment[]) {
		return [...comments].sort((a, b) => (b.isPinned ? 1 : 0) - (a.isPinned ? 1 : 0));
	}

	function getPreviewRows(req: ChangeRequest): Array<{ label: string; before: string; after: string }> {
		const p = req.payload;
		const rows: Array<{ label: string; before: string; after: string }> = [];
		if (req.action === 'add_sprint') {
			const tasks = Array.isArray(p.tasks) ? (p.tasks as Array<Record<string, unknown>>) : [];
			rows.push({ label: 'Sprint name', before: '—', after: String(p.sprintName ?? p.name ?? '—') });
			rows.push({ label: 'Tasks', before: '—', after: tasks.length > 0 ? tasks.map(t => String(t.title ?? '')).filter(Boolean).join(', ') : '(none)' });
		} else if (req.action === 'edit_task') {
			const fieldLabelMap: Record<string, string> = {
				title: 'Title', status: 'Status', assigneeId: 'Assignee',
				budget: 'Budget', spent: 'Cost', dueDate: 'Due date', startDate: 'Start date'
			};
			if (p.field) {
				const label = fieldLabelMap[String(p.field)] ?? String(p.field);
				rows.push({ label, before: String(p.before ?? p.currentValue ?? '—'), after: String(p.value ?? p.after ?? '—') });
			} else {
				const fields = ['title', 'status', 'assigneeId', 'budget', 'spent', 'dueDate', 'startDate'];
				for (const f of fields) {
					if (p[f] !== undefined) {
						rows.push({ label: fieldLabelMap[f] ?? f, before: String(p[`before_${f}`] ?? '—'), after: String(p[f]) });
					}
				}
			}
		} else if (req.action === 'delete_task' || req.action === 'delete_sprint') {
			rows.push({ label: 'Name', before: String(p.taskTitle ?? p.sprintName ?? req.targetLabel), after: '(deleted)' });
		} else if (req.action === 'edit_sprint') {
			rows.push({ label: 'Sprint name', before: String(p.before ?? '—'), after: String(p.name ?? p.after ?? '—') });
		} else if (req.action === 'edit_cost') {
			rows.push({ label: 'Method', before: String(p.before ?? '—'), after: String(p.method ?? p.after ?? '—') });
		} else if (req.action === 'import_sheet') {
			rows.push({ label: 'File', before: '—', after: String(p.fileName ?? '—') });
		} else {
			for (const [k, v] of Object.entries(p).filter(([k2]) => k2 !== 'note').slice(0, 6)) {
				rows.push({ label: k, before: '—', after: String(v) });
			}
		}
		return rows.filter(r => r.after && r.after !== 'undefined');
	}

	function getSprintTasks(req: ChangeRequest) {
		if (req.action !== 'add_sprint') return [];
		const tasks = req.payload.tasks;
		return Array.isArray(tasks) ? (tasks as Array<Record<string, unknown>>) : [];
	}
</script>

<svelte:window on:keydown={handleKeydown} />

{#if open}
	<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
	<div class="crp-backdrop" on:click={close}></div>
	<div class="crp-panel" role="dialog" aria-modal="true" aria-label="Change requests">
		<header class="crp-header">
			<div class="crp-title">
				<svg viewBox="0 0 24 24" aria-hidden="true">
					<path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2M9 5a2 2 0 0 0 2 2h2a2 2 0 0 0 2-2M9 5a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2" />
				</svg>
				<span>Change Requests</span>
				{#if pending.length > 0}<span class="crp-badge">{pending.length}</span>{/if}
			</div>
			<button type="button" class="crp-close" on:click={close} aria-label="Close">
				<svg viewBox="0 0 24 24"><path d="M18 6 6 18M6 6l12 12" /></svg>
			</button>
		</header>

		<div class="crp-tabs" role="tablist">
			<button type="button" role="tab" class="crp-tab" class:is-active={filterTab === 'pending'} on:click={() => (filterTab = 'pending')}>
				Pending {#if pending.length > 0}<span class="crp-tab-count">{pending.length}</span>{/if}
			</button>
			<button type="button" role="tab" class="crp-tab" class:is-active={filterTab === 'resolved'} on:click={() => (filterTab = 'resolved')}>
				Resolved
			</button>
		</div>

		<div class="crp-list">
			{#if shown.length === 0}
				<div class="crp-empty">{filterTab === 'pending' ? 'No pending requests' : 'No resolved requests yet'}</div>
			{:else}
				{#each shown as req (req.id)}
					{@const previewRows = getPreviewRows(req)}
					{@const sprintTasks = getSprintTasks(req)}
					{@const isDiscOpen = discussionOpenId === req.id}
					{@const isNoteOpen = noteOpenId === req.id}
					{@const isPreviewOpen = previewOpenId === req.id}
					<div class="crp-item" class:is-resolved={req.status !== 'pending'}>
						<div class="crp-item-top">
							<span class="crp-action-badge crp-action-{req.action.replace(/_/g, '-')}">{ACTION_LABEL[req.action] ?? req.action}</span>
							<span class="crp-status-dot" style:background={STATUS_COLOR[req.status]} title={req.status}></span>
							<span class="crp-time">{timeAgo(req.createdAt)}</span>
						</div>

						<div class="crp-item-who">
							<svg viewBox="0 0 24 24"><path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8z" /></svg>
							<strong>{req.userName}</strong>
							{#if req.targetLabel}<span class="crp-target">· {req.targetLabel}</span>{/if}
						</div>

						{#if req.payload.note}
							<div class="crp-note">
								<svg viewBox="0 0 24 24"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
								"{req.payload.note}"
							</div>
						{/if}

						{#if req.status !== 'pending'}
							<div class="crp-resolved-meta" class:is-approved={req.status === 'approved'} class:is-rejected={req.status === 'rejected'}>
								<svg viewBox="0 0 24 24">
									{#if req.status === 'approved'}<path d="m6 12 4 4 8-8"/>
									{:else}<path d="M18 6 6 18M6 6l12 12"/>{/if}
								</svg>
								<span>{req.status === 'approved' ? 'Approved' : 'Rejected'}{req.resolvedBy ? ` by ${req.resolvedBy}` : ''}</span>
								{#if req.resolvedAt}<span class="crp-resolved-time">{timeAgo(req.resolvedAt)}</span>{/if}
							</div>
							{#if req.resolveNote}<div class="crp-resolve-note">"{req.resolveNote}"</div>{/if}
						{/if}

						<!-- Quick action icons: admin sees approve/reject/note/preview/discuss -->
						{#if isAdmin && req.status === 'pending'}
							<div class="crp-quick-actions">
								<button type="button" class="crp-qa crp-qa-approve" title="Approve" on:click={() => approve(req)}>
									<svg viewBox="0 0 24 24"><path d="m6 12 4 4 8-8"/></svg>
								</button>
								<button type="button" class="crp-qa crp-qa-reject" title="Reject" on:click={() => reject(req)}>
									<svg viewBox="0 0 24 24"><path d="M18 6 6 18M6 6l12 12"/></svg>
								</button>
								<button type="button" class="crp-qa crp-qa-note" class:is-active={isNoteOpen} title="Resolve with note" on:click={() => toggleNote(req.id)}>
									<svg viewBox="0 0 24 24"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
								</button>
								<div class="crp-qa-sep"></div>
								<button type="button" class="crp-qa crp-qa-preview" class:is-active={isPreviewOpen} title="Preview change on board" on:click={() => togglePreview(req.id)}>
									<svg viewBox="0 0 24 24"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
								</button>
								<button type="button" class="crp-qa crp-qa-discuss" class:is-active={isDiscOpen} title="Discussion" on:click={() => toggleDiscussion(req.id)}>
									<svg viewBox="0 0 24 24"><path d="M17 8h2a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2h-2v4l-4-4H9a1.994 1.994 0 0 1-1.414-.586m0 0L11 14H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v2"/></svg>
									{#if req.discussion.length > 0}<span class="crp-disc-count">{req.discussion.length}</span>{/if}
								</button>
							</div>
						{:else if req.userId === sessionUserID || isAdmin}
							<div class="crp-quick-actions">
								<div style="flex:1"></div>
								<button type="button" class="crp-qa crp-qa-discuss" class:is-active={isDiscOpen} title="Discussion" on:click={() => toggleDiscussion(req.id)}>
									<svg viewBox="0 0 24 24"><path d="M17 8h2a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2h-2v4l-4-4H9a1.994 1.994 0 0 1-1.414-.586m0 0L11 14H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v2"/></svg>
									{#if req.discussion.length > 0}<span class="crp-disc-count">{req.discussion.length}</span>{/if}
								</button>
							</div>
						{/if}

						<!-- Resolve with note panel -->
						{#if isNoteOpen && isAdmin && req.status === 'pending'}
							<div class="crp-note-panel">
								<label class="crp-note-label">
									Add a note <span>(optional)</span>
									<textarea class="crp-note-input" bind:value={noteText} placeholder="Explain your decision…" rows="3" maxlength="500"></textarea>
								</label>
								<div class="crp-note-actions">
									<button type="button" class="crp-note-btn crp-note-approve" on:click={() => submitWithNote(req, 'approved')}>
										<svg viewBox="0 0 24 24"><path d="m6 12 4 4 8-8"/></svg> Approve
									</button>
									<button type="button" class="crp-note-btn crp-note-reject" on:click={() => submitWithNote(req, 'rejected')}>
										<svg viewBox="0 0 24 24"><path d="M18 6 6 18M6 6l12 12"/></svg> Reject
									</button>
									<button type="button" class="crp-note-cancel" on:click={() => { noteOpenId = ''; noteText = ''; }}>Cancel</button>
								</div>
							</div>
						{/if}

						<!-- Preview panel -->
						{#if isPreviewOpen}
							<div class="crp-preview-panel">
								<div class="crp-preview-title">Preview · how this change looks if applied</div>
								{#if req.action === 'add_sprint'}
									<div class="crp-sprint-preview">
										<div class="crp-sprint-preview-name">
											<svg viewBox="0 0 24 24"><path d="M13 2 3 14h9l-1 8 10-12h-9l1-8z"/></svg>
											{req.payload.sprintName ?? req.payload.name ?? req.targetLabel}
										</div>
										{#if sprintTasks.length > 0}
											<div class="crp-sprint-tasks">
												{#each sprintTasks as t}
													<div class="crp-sprint-task">
														<span class="crp-task-dot status-{String(t.status ?? 'todo')}"></span>
														<span class="crp-task-name">{t.title ?? '(unnamed)'}</span>
														{#if t.assigneeId}<span class="crp-task-meta">{t.assigneeId}</span>{/if}
													</div>
												{/each}
											</div>
										{:else}
											<div class="crp-sprint-no-tasks">No tasks specified</div>
										{/if}
									</div>
								{:else if previewRows.length > 0}
									<table class="crp-diff-table">
										<thead><tr><th>Field</th><th>Before</th><th>After</th></tr></thead>
										<tbody>
											{#each previewRows as row}
												<tr>
													<td class="crp-diff-field">{row.label}</td>
													<td class="crp-diff-before">{row.before}</td>
													<td class="crp-diff-after">{row.after}</td>
												</tr>
											{/each}
										</tbody>
									</table>
								{:else}
									<div class="crp-preview-empty">No preview available</div>
								{/if}
							</div>
						{/if}

						<!-- Discussion panel -->
						{#if isDiscOpen}
							<div class="crp-disc-panel">
								<div class="crp-disc-title">
									Discussion
									{#if req.discussion.length > 0}<span class="crp-disc-badge">{req.discussion.length}</span>{/if}
								</div>

								{#if req.discussion.length === 0}
									<div class="crp-disc-empty">No comments yet. Start the conversation.</div>
								{:else}
									<div class="crp-disc-list">
										{#each pinnedFirst(req.discussion) as comment (comment.id)}
											<div class="crp-comment" class:is-pinned={comment.isPinned} class:is-highlighted={comment.isHighlighted}>
												<div class="crp-comment-header">
													<span class="crp-comment-avatar">{comment.userName.slice(0,1).toUpperCase()}</span>
													<strong class="crp-comment-name">{comment.userName}</strong>
													<span class="crp-comment-time">{timeAgo(comment.createdAt)}</span>
													{#if comment.isPinned}<span class="crp-pin-badge">📌</span>{/if}
													<div class="crp-comment-actions">
														<button type="button" class="crp-cmt-btn" class:is-active={comment.isPinned} title={comment.isPinned ? 'Unpin' : 'Pin'} on:click={() => togglePin(req, comment.id)}>
															<svg viewBox="0 0 24 24"><path d="m15 4-3 3-4 1-1 3 5 5 3-1 1-4 3-3-4-4zm-9 9L3 16m3 3 3-3"/></svg>
														</button>
														<button type="button" class="crp-cmt-btn" class:is-active={comment.isHighlighted} title={comment.isHighlighted ? 'Remove highlight' : 'Highlight'} on:click={() => toggleHighlight(req, comment.id)}>
															<svg viewBox="0 0 24 24"><path d="M12 2l3.09 6.26L22 9.27l-5 4.87 1.18 6.88L12 17.77l-6.18 3.25L7 14.14 2 9.27l6.91-1.01L12 2z"/></svg>
														</button>
														<button type="button" class="crp-cmt-btn" title="Reply" on:click={() => { replyTargetId = { ...replyTargetId, [req.id]: replyTargetId[req.id] === comment.id ? '' : comment.id }; }}>
															<svg viewBox="0 0 24 24"><path d="m9 17-5-5 5-5M4 12h10.5a3.5 3.5 0 0 1 0 7H13"/></svg>
														</button>
													</div>
												</div>
												<div class="crp-comment-body">{comment.text}</div>

												{#if comment.replies.length > 0}
													<div class="crp-replies">
														{#each comment.replies as reply (reply.id)}
															<div class="crp-reply">
																<span class="crp-reply-avatar">{reply.userName.slice(0,1).toUpperCase()}</span>
																<div class="crp-reply-content">
																	<span class="crp-reply-name">{reply.userName}</span>
																	<span class="crp-reply-time">{timeAgo(reply.createdAt)}</span>
																	<div class="crp-reply-text">{reply.text}</div>
																</div>
															</div>
														{/each}
													</div>
												{/if}

												{#if replyTargetId[req.id] === comment.id}
													<div class="crp-reply-compose">
														<textarea class="crp-reply-input" placeholder="Write a reply…" rows="2"
															bind:value={replyDraft[`${req.id}:${comment.id}`]}
															on:keydown={(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); addReply(req, comment.id); }}}
														></textarea>
														<div class="crp-reply-btns">
															<button type="button" class="crp-reply-send" on:click={() => addReply(req, comment.id)}>Reply</button>
															<button type="button" class="crp-reply-cancel" on:click={() => { replyTargetId = { ...replyTargetId, [req.id]: '' }; }}>Cancel</button>
														</div>
													</div>
												{/if}
											</div>
										{/each}
									</div>
								{/if}

								<div class="crp-comment-compose">
									<textarea class="crp-comment-input" placeholder="Add a comment…" rows="2"
										bind:value={commentDraft[req.id]}
										on:keydown={(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); addComment(req); }}}
									></textarea>
									<button type="button" class="crp-comment-send" disabled={!(commentDraft[req.id] || '').trim()} on:click={() => addComment(req)}>
										Comment
									</button>
								</div>
							</div>
						{/if}
					</div>
				{/each}
			{/if}
		</div>
	</div>
{/if}

<style>
	.crp-backdrop { position: fixed; inset: 0; background: rgba(0,0,0,.35); z-index: 1100; backdrop-filter: blur(1px); }

	.crp-panel {
		position: fixed; top: 0; right: 0; bottom: 0; z-index: 1101;
		width: min(420px, 100vw);
		background: var(--ws-surface, #1e1e2e);
		border-left: 1px solid color-mix(in srgb, var(--ws-border, #3a3a52) 80%, transparent);
		display: flex; flex-direction: column;
		box-shadow: -8px 0 40px rgba(0,0,0,.3);
		animation: crp-slide-in .2s ease;
	}
	@keyframes crp-slide-in { from { transform: translateX(100%); } to { transform: translateX(0); } }

	.crp-header { display: flex; align-items: center; justify-content: space-between; padding: .85rem 1rem; border-bottom: 1px solid color-mix(in srgb, var(--ws-border,#3a3a52) 60%, transparent); flex-shrink: 0; }
	.crp-title { display: flex; align-items: center; gap: .5rem; font-size: .88rem; font-weight: 700; color: var(--ws-text,#e2e2f0); }
	.crp-title svg { width: 16px; height: 16px; stroke: #6366f1; fill: none; stroke-width: 2; stroke-linecap: round; stroke-linejoin: round; }
	.crp-badge { min-width: 18px; height: 18px; padding: 0 5px; border-radius: 9px; background: #ef4444; color: #fff; font-size: .65rem; font-weight: 700; display: flex; align-items: center; justify-content: center; }
	.crp-close { width: 28px; height: 28px; border-radius: 7px; border: none; background: transparent; color: var(--ws-muted,#8888a8); cursor: pointer; display: flex; align-items: center; justify-content: center; }
	.crp-close:hover { background: color-mix(in srgb, var(--ws-surface,#1e1e2e) 50%, var(--ws-border,#3a3a52) 50%); color: var(--ws-text,#e2e2f0); }
	.crp-close svg { width: 14px; height: 14px; stroke: currentColor; fill: none; stroke-width: 2; stroke-linecap: round; }

	.crp-tabs { display: flex; padding: .5rem 1rem 0; border-bottom: 1px solid color-mix(in srgb, var(--ws-border,#3a3a52) 60%, transparent); flex-shrink: 0; }
	.crp-tab { padding: .38rem .7rem; border: none; background: transparent; color: var(--ws-muted,#8888a8); font-size: .72rem; font-weight: 600; cursor: pointer; border-bottom: 2px solid transparent; margin-bottom: -1px; display: flex; align-items: center; gap: .35rem; transition: color .13s; }
	.crp-tab.is-active { color: var(--ws-text,#e2e2f0); border-bottom-color: #6366f1; }
	.crp-tab-count { min-width: 16px; height: 16px; padding: 0 4px; border-radius: 8px; background: #f59e0b; color: #000; font-size: .6rem; font-weight: 800; display: flex; align-items: center; justify-content: center; }

	.crp-list { flex: 1; overflow-y: auto; padding: .7rem; display: flex; flex-direction: column; gap: .55rem; }
	.crp-empty { padding: 2rem 1rem; text-align: center; font-size: .78rem; color: var(--ws-muted,#8888a8); }

	/* ── Card ─────────────────────────────────────────────────────────────── */
	.crp-item { background: color-mix(in srgb, var(--ws-surface,#1e1e2e) 70%, var(--ws-border,#3a3a52) 30%); border: 1px solid color-mix(in srgb, var(--ws-border,#3a3a52) 70%, transparent); border-radius: 11px; padding: .65rem .75rem; display: flex; flex-direction: column; gap: .38rem; }
	.crp-item:not(.is-resolved) { border-left: 3px solid #f59e0b; }
	.crp-item.is-resolved { opacity: .72; border-left: 3px solid transparent; }

	.crp-item-top { display: flex; align-items: center; gap: .4rem; }
	.crp-action-badge { font-size: .65rem; font-weight: 700; padding: .15rem .45rem; border-radius: 5px; background: color-mix(in srgb, #6366f1 15%, transparent); color: #818cf8; border: 1px solid color-mix(in srgb, #6366f1 30%, transparent); }
	.crp-action-badge.crp-action-delete-task,
	.crp-action-badge.crp-action-delete-sprint { background: color-mix(in srgb, #ef4444 15%, transparent); color: #f87171; border-color: color-mix(in srgb, #ef4444 30%, transparent); }
	.crp-action-badge.crp-action-add-sprint,
	.crp-action-badge.crp-action-add-task { background: color-mix(in srgb, #10b981 15%, transparent); color: #34d399; border-color: color-mix(in srgb, #10b981 30%, transparent); }
	.crp-status-dot { width: 7px; height: 7px; border-radius: 50%; flex-shrink: 0; }
	.crp-time { font-size: .66rem; color: var(--ws-muted,#8888a8); margin-left: auto; }

	.crp-item-who { display: flex; align-items: center; gap: .3rem; font-size: .74rem; color: var(--ws-text,#e2e2f0); }
	.crp-item-who svg { width: 12px; height: 12px; stroke: var(--ws-muted,#8888a8); fill: none; stroke-width: 2; stroke-linecap: round; stroke-linejoin: round; flex-shrink: 0; }
	.crp-target { color: var(--ws-muted,#8888a8); font-size: .7rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 140px; }

	.crp-note { display: flex; align-items: flex-start; gap: .3rem; font-size: .7rem; color: var(--ws-text,#e2e2f0); font-style: italic; background: color-mix(in srgb, var(--ws-surface,#1e1e2e) 50%, #000 50%); border-radius: 6px; padding: .3rem .5rem; }
	.crp-note svg { width: 11px; height: 11px; stroke: var(--ws-muted,#8888a8); fill: none; stroke-width: 2; stroke-linecap: round; flex-shrink: 0; margin-top: 1px; }

	.crp-resolved-meta { display: flex; align-items: center; gap: .35rem; font-size: .7rem; padding: .25rem .5rem; border-radius: 6px; font-weight: 600; }
	.crp-resolved-meta.is-approved { background: color-mix(in srgb, #10b981 12%, transparent); color: #34d399; }
	.crp-resolved-meta.is-rejected { background: color-mix(in srgb, #ef4444 12%, transparent); color: #f87171; }
	.crp-resolved-meta svg { width: 12px; height: 12px; stroke: currentColor; fill: none; stroke-width: 2.5; stroke-linecap: round; }
	.crp-resolved-time { margin-left: auto; font-weight: 400; color: var(--ws-muted,#8888a8); }
	.crp-resolve-note { font-size: .68rem; font-style: italic; color: var(--ws-muted,#8888a8); padding: .2rem .45rem; border-left: 2px solid color-mix(in srgb, var(--ws-border,#3a3a52) 80%, transparent); margin-left: .3rem; }

	/* ── Quick actions ────────────────────────────────────────────────────── */
	.crp-quick-actions { display: flex; align-items: center; gap: .3rem; padding-top: .25rem; border-top: 1px solid color-mix(in srgb, var(--ws-border,#3a3a52) 50%, transparent); margin-top: .1rem; }
	.crp-qa { width: 28px; height: 28px; border-radius: 7px; border: 1px solid transparent; cursor: pointer; display: flex; align-items: center; justify-content: center; background: transparent; position: relative; transition: background .12s, color .12s; }
	.crp-qa svg { width: 13px; height: 13px; stroke: currentColor; fill: none; stroke-width: 2; stroke-linecap: round; stroke-linejoin: round; }
	.crp-qa-approve { color: #10b981; }
	.crp-qa-approve:hover { background: color-mix(in srgb, #10b981 18%, transparent); border-color: color-mix(in srgb, #10b981 35%, transparent); }
	.crp-qa-reject { color: #ef4444; }
	.crp-qa-reject:hover { background: color-mix(in srgb, #ef4444 18%, transparent); border-color: color-mix(in srgb, #ef4444 35%, transparent); }
	.crp-qa-note { color: var(--ws-muted,#8888a8); }
	.crp-qa-note:hover, .crp-qa-note.is-active { color: #f59e0b; background: color-mix(in srgb, #f59e0b 16%, transparent); border-color: color-mix(in srgb, #f59e0b 30%, transparent); }
	.crp-qa-preview { color: var(--ws-muted,#8888a8); }
	.crp-qa-preview:hover, .crp-qa-preview.is-active { color: #6366f1; background: color-mix(in srgb, #6366f1 16%, transparent); border-color: color-mix(in srgb, #6366f1 30%, transparent); }
	.crp-qa-discuss { color: var(--ws-muted,#8888a8); position: relative; }
	.crp-qa-discuss:hover, .crp-qa-discuss.is-active { color: #818cf8; background: color-mix(in srgb, #6366f1 16%, transparent); border-color: color-mix(in srgb, #6366f1 30%, transparent); }
	.crp-disc-count { position: absolute; top: -4px; right: -4px; min-width: 14px; height: 14px; padding: 0 3px; border-radius: 7px; background: #6366f1; color: #fff; font-size: .55rem; font-weight: 800; display: flex; align-items: center; justify-content: center; }
	.crp-qa-sep { width: 1px; height: 16px; background: color-mix(in srgb, var(--ws-border,#3a3a52) 60%, transparent); margin: 0 .1rem; }

	/* ── Note panel ────────────────────────────────────────────────────────── */
	.crp-note-panel { background: color-mix(in srgb, var(--ws-surface,#1e1e2e) 60%, #000 40%); border: 1px solid color-mix(in srgb, var(--ws-border,#3a3a52) 70%, transparent); border-radius: 8px; padding: .65rem .7rem; display: flex; flex-direction: column; gap: .45rem; }
	.crp-note-label { font-size: .7rem; font-weight: 600; color: var(--ws-text,#e2e2f0); display: flex; flex-direction: column; gap: .3rem; }
	.crp-note-label span { color: var(--ws-muted,#8888a8); font-weight: 400; }
	.crp-note-input { width: 100%; box-sizing: border-box; resize: vertical; background: color-mix(in srgb, var(--ws-surface,#1e1e2e) 70%, #000 30%); border: 1px solid color-mix(in srgb, var(--ws-border,#3a3a52) 80%, transparent); border-radius: 6px; color: var(--ws-text,#e2e2f0); font-size: .72rem; padding: .4rem .55rem; outline: none; font-family: inherit; line-height: 1.5; }
	.crp-note-input:focus { border-color: color-mix(in srgb, #6366f1 60%, var(--ws-border,#3a3a52)); }
	.crp-note-actions { display: flex; gap: .4rem; align-items: center; }
	.crp-note-btn { display: flex; align-items: center; gap: .3rem; height: 1.72rem; padding: 0 .7rem; border-radius: 6px; font-size: .68rem; font-weight: 700; cursor: pointer; border: 1px solid transparent; transition: background .12s; }
	.crp-note-btn svg { width: 11px; height: 11px; stroke: currentColor; fill: none; stroke-width: 2.5; stroke-linecap: round; }
	.crp-note-approve { background: color-mix(in srgb, #10b981 18%, transparent); color: #10b981; border-color: color-mix(in srgb, #10b981 35%, transparent); }
	.crp-note-approve:hover { background: color-mix(in srgb, #10b981 30%, transparent); }
	.crp-note-reject { background: color-mix(in srgb, #ef4444 15%, transparent); color: #ef4444; border-color: color-mix(in srgb, #ef4444 30%, transparent); }
	.crp-note-reject:hover { background: color-mix(in srgb, #ef4444 28%, transparent); }
	.crp-note-cancel { margin-left: auto; background: transparent; border: none; color: var(--ws-muted,#8888a8); cursor: pointer; font-size: .68rem; padding: 0 .3rem; }
	.crp-note-cancel:hover { color: var(--ws-text,#e2e2f0); }

	/* ── Preview panel ─────────────────────────────────────────────────────── */
	.crp-preview-panel { background: color-mix(in srgb, var(--ws-surface,#1e1e2e) 55%, #000 45%); border: 1px solid color-mix(in srgb, var(--ws-border,#3a3a52) 70%, transparent); border-radius: 8px; padding: .65rem .7rem; display: flex; flex-direction: column; gap: .5rem; }
	.crp-preview-title { font-size: .66rem; font-weight: 700; letter-spacing: .06em; text-transform: uppercase; color: var(--ws-muted,#8888a8); }
	.crp-diff-table { width: 100%; border-collapse: collapse; font-size: .68rem; }
	.crp-diff-table th { text-align: left; padding: .2rem .35rem; color: var(--ws-muted,#8888a8); font-weight: 600; font-size: .62rem; letter-spacing: .05em; border-bottom: 1px solid color-mix(in srgb, var(--ws-border,#3a3a52) 60%, transparent); }
	.crp-diff-table td { padding: .28rem .35rem; vertical-align: middle; }
	.crp-diff-field { color: var(--ws-muted,#8888a8); font-weight: 600; min-width: 70px; }
	.crp-diff-before { color: #f87171; text-decoration: line-through; opacity: .7; }
	.crp-diff-after { color: #34d399; font-weight: 600; }
	.crp-sprint-preview { display: flex; flex-direction: column; gap: .4rem; border: 1px solid color-mix(in srgb, #6366f1 30%, transparent); border-radius: 8px; padding: .5rem .6rem; background: color-mix(in srgb, #6366f1 6%, transparent); }
	.crp-sprint-preview-name { display: flex; align-items: center; gap: .35rem; font-size: .78rem; font-weight: 700; color: var(--ws-text,#e2e2f0); }
	.crp-sprint-preview-name svg { width: 13px; height: 13px; stroke: #818cf8; fill: none; stroke-width: 2; stroke-linecap: round; }
	.crp-sprint-tasks { display: flex; flex-direction: column; gap: .22rem; }
	.crp-sprint-task { display: flex; align-items: center; gap: .4rem; font-size: .68rem; }
	.crp-task-dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; background: color-mix(in srgb, var(--ws-muted,#8888a8) 40%, transparent); }
	.crp-task-dot.status-done { background: #10b981; }
	.crp-task-dot.status-in_progress { background: #f59e0b; }
	.crp-task-dot.status-todo { background: #6366f1; }
	.crp-task-name { color: var(--ws-text,#e2e2f0); flex: 1; }
	.crp-task-meta { color: var(--ws-muted,#8888a8); font-size: .62rem; }
	.crp-sprint-no-tasks { font-size: .66rem; color: var(--ws-muted,#8888a8); font-style: italic; }
	.crp-preview-empty { font-size: .68rem; color: var(--ws-muted,#8888a8); font-style: italic; }

	/* ── Discussion panel ──────────────────────────────────────────────────── */
	.crp-disc-panel { background: color-mix(in srgb, var(--ws-surface,#1e1e2e) 55%, #000 45%); border: 1px solid color-mix(in srgb, var(--ws-border,#3a3a52) 70%, transparent); border-radius: 8px; padding: .6rem .7rem; display: flex; flex-direction: column; gap: .5rem; }
	.crp-disc-title { display: flex; align-items: center; gap: .4rem; font-size: .66rem; font-weight: 700; letter-spacing: .06em; text-transform: uppercase; color: var(--ws-muted,#8888a8); }
	.crp-disc-badge { min-width: 16px; height: 16px; padding: 0 4px; border-radius: 8px; background: #6366f1; color: #fff; font-size: .55rem; font-weight: 800; display: flex; align-items: center; justify-content: center; }
	.crp-disc-empty { font-size: .68rem; color: var(--ws-muted,#8888a8); font-style: italic; padding: .2rem 0; }
	.crp-disc-list { display: flex; flex-direction: column; gap: .5rem; max-height: 260px; overflow-y: auto; }

	.crp-comment { display: flex; flex-direction: column; gap: .28rem; padding: .4rem .5rem; border-radius: 7px; border: 1px solid color-mix(in srgb, var(--ws-border,#3a3a52) 50%, transparent); background: color-mix(in srgb, var(--ws-surface,#1e1e2e) 80%, transparent); }
	.crp-comment.is-pinned { border-color: color-mix(in srgb, #f59e0b 35%, transparent); background: color-mix(in srgb, #f59e0b 6%, transparent); }
	.crp-comment.is-highlighted { border-color: color-mix(in srgb, #6366f1 35%, transparent); background: color-mix(in srgb, #6366f1 8%, transparent); }
	.crp-comment-header { display: flex; align-items: center; gap: .3rem; }
	.crp-comment-avatar { width: 20px; height: 20px; border-radius: 50%; background: #6366f1; color: #fff; font-size: .58rem; font-weight: 800; display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
	.crp-comment-name { font-size: .7rem; font-weight: 700; color: var(--ws-text,#e2e2f0); }
	.crp-comment-time { font-size: .62rem; color: var(--ws-muted,#8888a8); }
	.crp-pin-badge { font-size: .65rem; }
	.crp-comment-actions { display: flex; align-items: center; gap: .15rem; margin-left: auto; }
	.crp-cmt-btn { width: 22px; height: 22px; border-radius: 5px; border: none; background: transparent; cursor: pointer; color: var(--ws-muted,#8888a8); display: flex; align-items: center; justify-content: center; transition: color .12s, background .12s; }
	.crp-cmt-btn:hover, .crp-cmt-btn.is-active { color: var(--ws-text,#e2e2f0); background: color-mix(in srgb, var(--ws-border,#3a3a52) 50%, transparent); }
	.crp-cmt-btn svg { width: 11px; height: 11px; stroke: currentColor; fill: none; stroke-width: 2; stroke-linecap: round; }
	.crp-comment-body { font-size: .72rem; color: var(--ws-text,#e2e2f0); line-height: 1.5; white-space: pre-wrap; word-break: break-word; }

	.crp-replies { display: flex; flex-direction: column; gap: .2rem; margin-left: .7rem; padding-left: .5rem; border-left: 2px solid color-mix(in srgb, var(--ws-border,#3a3a52) 60%, transparent); }
	.crp-reply { display: flex; align-items: flex-start; gap: .3rem; }
	.crp-reply-avatar { width: 16px; height: 16px; border-radius: 50%; background: #4f52d4; color: #fff; font-size: .5rem; font-weight: 800; display: flex; align-items: center; justify-content: center; flex-shrink: 0; margin-top: 1px; }
	.crp-reply-content { display: flex; flex-direction: column; gap: .1rem; }
	.crp-reply-name { font-size: .65rem; font-weight: 700; color: var(--ws-text,#e2e2f0); }
	.crp-reply-time { font-size: .58rem; color: var(--ws-muted,#8888a8); margin-left: .25rem; }
	.crp-reply-text { font-size: .68rem; color: var(--ws-text,#e2e2f0); line-height: 1.4; white-space: pre-wrap; word-break: break-word; }

	.crp-reply-compose { display: flex; flex-direction: column; gap: .3rem; margin-left: .7rem; margin-top: .15rem; }
	.crp-reply-input { width: 100%; box-sizing: border-box; resize: none; background: color-mix(in srgb, var(--ws-surface,#1e1e2e) 70%, #000 30%); border: 1px solid color-mix(in srgb, var(--ws-border,#3a3a52) 80%, transparent); border-radius: 6px; color: var(--ws-text,#e2e2f0); font-size: .68rem; padding: .3rem .45rem; outline: none; font-family: inherit; line-height: 1.4; }
	.crp-reply-input:focus { border-color: color-mix(in srgb, #6366f1 60%, var(--ws-border,#3a3a52)); }
	.crp-reply-btns { display: flex; gap: .3rem; }
	.crp-reply-send { height: 1.6rem; padding: 0 .65rem; border-radius: 6px; font-size: .65rem; font-weight: 700; cursor: pointer; background: #6366f1; color: #fff; border: none; transition: background .12s; }
	.crp-reply-send:hover { background: #4f52d4; }
	.crp-reply-cancel { background: transparent; border: none; color: var(--ws-muted,#8888a8); cursor: pointer; font-size: .65rem; }
	.crp-reply-cancel:hover { color: var(--ws-text,#e2e2f0); }

	.crp-comment-compose { display: flex; flex-direction: column; gap: .3rem; padding-top: .35rem; border-top: 1px solid color-mix(in srgb, var(--ws-border,#3a3a52) 50%, transparent); }
	.crp-comment-input { width: 100%; box-sizing: border-box; resize: none; background: color-mix(in srgb, var(--ws-surface,#1e1e2e) 70%, #000 30%); border: 1px solid color-mix(in srgb, var(--ws-border,#3a3a52) 80%, transparent); border-radius: 6px; color: var(--ws-text,#e2e2f0); font-size: .7rem; padding: .38rem .5rem; outline: none; font-family: inherit; line-height: 1.5; }
	.crp-comment-input:focus { border-color: color-mix(in srgb, #6366f1 60%, var(--ws-border,#3a3a52)); }
	.crp-comment-send { align-self: flex-end; height: 1.72rem; padding: 0 .8rem; border-radius: 7px; font-size: .68rem; font-weight: 700; cursor: pointer; background: #6366f1; color: #fff; border: none; transition: background .12s, opacity .12s; }
	.crp-comment-send:hover:not(:disabled) { background: #4f52d4; }
	.crp-comment-send:disabled { opacity: .45; cursor: not-allowed; }
</style>
