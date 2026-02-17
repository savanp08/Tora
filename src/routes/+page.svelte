<script lang="ts">
	import { goto } from '$app/navigation';
	import AuthModal from '$lib/components/home/AuthModal.svelte';
	import { currentUser, authToken } from '$lib/store';
	import { getOrInitIdentity, updateUsername } from '$lib/utils/identity';
	import { generateRoomName } from '$lib/utils/nameGenerator';
	import { onMount } from 'svelte';
	const API_BASE = (import.meta.env.VITE_API_BASE as string | undefined) ?? 'http://localhost:8080';
	const CLIENT_LOG_PREFIX = '[home-client]';
	type JoinMode = 'create' | 'join';

	let roomName = '';
	let guestUsername = '';
	let selectedMode: JoinMode = 'create';

	let isModalOpen = false;
	let isJoining = false;
	let joinError = '';

	function clientLog(event: string, payload?: unknown) {
		const timestamp = new Date().toISOString();
		if (payload === undefined) {
			console.log(`${CLIENT_LOG_PREFIX} ${timestamp} ${event}`);
			return;
		}
		console.log(`${CLIENT_LOG_PREFIX} ${timestamp} ${event}`, payload);
	}

	onMount(() => {
		roomName = generateRoomName();
		const identity = getOrInitIdentity();
		currentUser.set({ id: identity.id, username: identity.username });
	});

	function onRoomNameFocus(event: FocusEvent) {
		const input = event.currentTarget as HTMLInputElement | null;
		input?.select();
	}

	function normalizeRoomNameInput(value: string) {
		return value
			.trim()
			.toLowerCase()
			.replace(/[^a-z0-9\s_-]/g, '')
			.replace(/[\s-]+/g, '_')
			.replace(/_+/g, '_')
			.replace(/^_+|_+$/g, '');
	}

	function normalizeUsernameInput(value: string) {
		return value
			.trim()
			.replace(/[^a-zA-Z0-9\s_-]/g, '')
			.replace(/[\s-]+/g, '_')
			.replace(/_+/g, '_')
			.replace(/^_+|_+$/g, '');
	}

	function setMode(mode: JoinMode) {
		selectedMode = mode;
	}

	async function handleRoomAction() {
		const normalizedRoomName = normalizeRoomNameInput(roomName);
		if (!normalizedRoomName) {
			joinError = 'Room name cannot be empty';
			return;
		}

		isJoining = true;
		joinError = '';
		roomName = normalizedRoomName;

		const identity = getOrInitIdentity();
		const requestedUsername = normalizeUsernameInput(guestUsername);
		const userIdentity = requestedUsername ? updateUsername(requestedUsername) : identity;
		const userToJoin = userIdentity.username;
		guestUsername = userToJoin;

		try {
			clientLog('api-rooms-join-request', {
				roomName: normalizedRoomName,
				userToJoin,
				mode: selectedMode
			});
			const res = await fetch(`${API_BASE}/api/rooms/join`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					roomName: normalizedRoomName,
					username: userToJoin,
					userId: userIdentity.id,
					type: 'ephemeral',
					mode: selectedMode
				})
			});

			const data = await res.json();
			clientLog('api-rooms-join-response', { status: res.status, ok: res.ok, data });

			if (!res.ok) throw new Error(data.error || 'Failed to join room');

			currentUser.set({ id: data.userId || userIdentity.id, username: userToJoin });
			authToken.set(data.token);

			const resolvedRoomName = data.roomName || normalizedRoomName;
			const createdAt = Number(data.createdAt);
			const createdAtQuery =
				Number.isFinite(createdAt) && createdAt > 0 ? `&createdAt=${createdAt}` : '';
			clientLog('navigate-chat-room', { roomId: data.roomId, roomName: resolvedRoomName });
			goto(
				`/chat/${data.roomId}?name=${encodeURIComponent(resolvedRoomName)}&member=1${createdAtQuery}`
			);
		} catch (e: any) {
			clientLog('api-rooms-join-error', { error: e?.message ?? String(e) });
			joinError = e.message;
		} finally {
			isJoining = false;
		}
	}

	function onAuthSuccess(event: CustomEvent) {
		const { user, token } = event.detail;
		clientLog('auth-success', { userId: user?.id, username: user?.username });

		currentUser.set(user);
		authToken.set(token);

		alert(`Welcome back, ${user.username}!`);
	}
</script>

<div class="container">
	<header>
		<div class="logo">Ephemeral<b>Chat</b></div>
		<button class="btn-login" on:click={() => (isModalOpen = true)}> Log In / Sign Up </button>
	</header>

	<main>
		<div class="hero-box">
			<h1>Disappearing chats. <br />Instant connections.</h1>
			<p>Create a room. Share the link. It vanishes when you leave.</p>

			{#if joinError}
				<div class="error-msg">{joinError}</div>
			{/if}

			<div class="join-form">
				<input
					type="text"
					placeholder="Enter Room Name (e.g. 'LunchPlan')"
					bind:value={roomName}
					on:focus={onRoomNameFocus}
				/>

				<input type="text" placeholder="Username (Optional)" bind:value={guestUsername} />

				<div class="action-row">
					<button
						class="btn-primary-action"
						class:selected={selectedMode === 'create'}
						on:click={() => setMode('create')}
						disabled={isJoining}
					>
						New
					</button>
					<button
						class="btn-secondary-action"
						class:selected={selectedMode === 'join'}
						on:click={() => setMode('join')}
						disabled={isJoining}
					>
						Existing
					</button>
				</div>
				<button
					class="btn-submit-action"
					on:click={handleRoomAction}
					disabled={isJoining || !roomName}
				>
					{isJoining ? 'Working...' : 'Join'}
				</button>
			</div>

			<p class="hint">No signup required for ephemeral rooms.</p>
		</div>
	</main>

	<AuthModal
		isOpen={isModalOpen}
		on:close={() => (isModalOpen = false)}
		on:success={onAuthSuccess}
	/>
</div>

<style>
	:global(body) {
		margin: 0;
		font-family: sans-serif;
		background: #f4f4f4;
	}

	.container {
		max-width: 800px;
		margin: 0 auto;
		padding: 20px;
		height: 100vh;
		display: flex;
		flex-direction: column;
	}

	header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 60px;
	}
	.logo {
		font-size: 1.5rem;
	}
	.btn-login {
		padding: 8px 16px;
		background: transparent;
		border: 2px solid #333;
		cursor: pointer;
		border-radius: 4px;
		font-weight: bold;
	}

	main {
		flex: 1;
		display: flex;
		justify-content: center;
		align-items: center;
	}

	.hero-box {
		text-align: center;
		background: white;
		padding: 40px;
		border-radius: 12px;
		box-shadow: 0 10px 25px rgba(0, 0, 0, 0.05);
		width: 100%;
		max-width: 500px;
	}

	h1 {
		margin-top: 0;
		color: #222;
	}
	p {
		color: #666;
		margin-bottom: 30px;
	}

	.join-form {
		display: flex;
		flex-direction: column;
		gap: 10px;
	}
	.action-row {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 10px;
	}
	input {
		padding: 12px;
		border: 1px solid #ddd;
		border-radius: 6px;
		font-size: 1rem;
	}
	button {
		padding: 12px;
		color: white;
		border: none;
		border-radius: 6px;
		font-size: 1rem;
		font-weight: bold;
		cursor: pointer;
		transition: background 0.2s;
	}
	button:disabled {
		background: #ccc;
		cursor: not-allowed;
	}
	button:hover:not(:disabled) {
		background: #0056b3;
	}
	.btn-primary-action {
		background: #ffffff;
		border: 1px solid #cbd5e1;
		color: #1f2937;
	}
	.btn-primary-action:hover:not(:disabled) {
		background: #f1f5f9;
	}
	.btn-secondary-action {
		background: #ffffff;
		border: 1px solid #cbd5e1;
		color: #1f2937;
	}
	.btn-secondary-action:hover:not(:disabled) {
		background: #f1f5f9;
	}
	.btn-primary-action.selected,
	.btn-secondary-action.selected {
		border-color: #16a34a;
		box-shadow: 0 0 0 2px rgba(22, 163, 74, 0.18);
		background: #ecfdf3;
	}
	.btn-submit-action {
		background: #16a34a;
	}
	.btn-submit-action:hover:not(:disabled) {
		background: #15803d;
	}

	.error-msg {
		color: #d9534f;
		background: #f9d6d5;
		padding: 10px;
		border-radius: 4px;
		margin-bottom: 15px;
	}
	.hint {
		font-size: 0.8rem;
		color: #999;
		margin-top: 20px;
	}
</style>
