export type CallType = 'audio' | 'video';

export type SignalingMessageType =
	| 'call_invite'
	| 'webrtc_offer'
	| 'webrtc_answer'
	| 'webrtc_ice'
	| 'call_log';

export type IncomingCallEvent = {
	fromUserId: string;
	fromUserName: string;
	callType: CallType;
	targetUserId: string;
};

type SignalCallback = (payload: Record<string, unknown>) => void;

type WebRTCManagerOptions = {
	roomId: string;
	userId: string;
	userName: string;
	sendSignal: SignalCallback;
	onIncomingCall?: (event: IncomingCallEvent) => void;
	onRemoteStream?: (userId: string, stream: MediaStream) => void;
	onRemoteStreamRemoved?: (userId: string) => void;
	maxParticipants?: number;
};

const WEBRTC_STUN_CONFIG: RTCConfiguration = {
	iceServers: [{ urls: ['stun:stun.l.google.com:19302'] }]
};

const MAX_PARTICIPANTS_DEFAULT = 5;

function toStringValue(input: unknown) {
	if (typeof input === 'string') {
		return input.trim();
	}
	if (typeof input === 'number' || typeof input === 'boolean') {
		return String(input).trim();
	}
	return '';
}

function normalizeIdentifier(input: string) {
	return input
		.trim()
		.replace(/[^a-zA-Z0-9\s_-]/g, '')
		.replace(/[\s-]+/g, '_')
		.replace(/_+/g, '_')
		.replace(/^_+|_+$/g, '');
}

function normalizeRoomId(input: string) {
	return input
		.toLowerCase()
		.trim()
		.replace(/[^a-z0-9]/g, '');
}

function toRecord(input: unknown): Record<string, unknown> {
	if (!input || typeof input !== 'object' || Array.isArray(input)) {
		return {};
	}
	return input as Record<string, unknown>;
}

function normalizeCallType(input: string, fallback: CallType = 'audio'): CallType {
	return input.toLowerCase() === 'video' ? 'video' : fallback === 'video' ? 'video' : 'audio';
}

function isTransientSignalingType(input: string) {
	switch (input.toLowerCase()) {
		case 'call_invite':
		case 'webrtc_offer':
		case 'webrtc_answer':
		case 'webrtc_ice':
			return true;
		default:
			return false;
	}
}

export class WebRTCManager extends EventTarget {
	private roomId: string;
	private userId: string;
	private userName: string;
	private sendSignal: SignalCallback;
	private onIncomingCall?: (event: IncomingCallEvent) => void;
	private onRemoteStream?: (userId: string, stream: MediaStream) => void;
	private onRemoteStreamRemoved?: (userId: string) => void;
	private maxParticipants: number;

	private localStream: MediaStream | null = null;
	private peerConnections = new Map<string, RTCPeerConnection>();
	private remoteStreams = new Map<string, MediaStream>();
	private callStartedAt = 0;
	private currentCallType: CallType = 'audio';

	constructor(options: WebRTCManagerOptions) {
		super();
		this.roomId = normalizeRoomId(options.roomId);
		this.userId = normalizeIdentifier(options.userId);
		this.userName = toStringValue(options.userName) || 'User';
		this.sendSignal = options.sendSignal;
		this.onIncomingCall = options.onIncomingCall;
		this.onRemoteStream = options.onRemoteStream;
		this.onRemoteStreamRemoved = options.onRemoteStreamRemoved;
		const requestedMax = Number.isFinite(options.maxParticipants)
			? Math.max(2, Math.trunc(Number(options.maxParticipants)))
			: MAX_PARTICIPANTS_DEFAULT;
		this.maxParticipants = requestedMax;
	}

	public updateContext(roomId: string, userId: string, userName: string) {
		this.roomId = normalizeRoomId(roomId);
		this.userId = normalizeIdentifier(userId);
		this.userName = toStringValue(userName) || this.userName || 'User';
	}

	public getLocalStream() {
		return this.localStream;
	}

	public getRemoteStreamEntries() {
		return Array.from(this.remoteStreams.entries()).map(([userId, stream]) => ({ userId, stream }));
	}

	public getPeerUserIds() {
		return Array.from(this.peerConnections.keys());
	}

	public getAvailablePeerSlots() {
		return Math.max(0, this.maxParticipants-1 - this.peerConnections.size);
	}

	public async startLocalStream(video: boolean) {
		const wantsVideo = Boolean(video);
		if (this.localStream) {
			const hasVideoTrack = this.localStream.getVideoTracks().length > 0;
			if (!wantsVideo || hasVideoTrack) {
				this.currentCallType = wantsVideo ? 'video' : 'audio';
				if (!this.callStartedAt) {
					this.callStartedAt = Date.now();
				}
				return this.localStream;
			}
			this.stopLocalStreamTracks();
			this.localStream = null;
		}

		const stream = await navigator.mediaDevices.getUserMedia({
			audio: true,
			video: wantsVideo
		});
		this.localStream = stream;
		this.currentCallType = wantsVideo ? 'video' : 'audio';
		if (!this.callStartedAt) {
			this.callStartedAt = Date.now();
		}
		this.attachLocalTracksToExistingPeers();
		this.dispatchEvent(
			new CustomEvent('local-stream', {
				detail: { stream }
			})
		);
		return stream;
	}

	public async createPeerConnection(targetUserId: string, isInitiator: boolean) {
		const normalizedTarget = normalizeIdentifier(targetUserId);
		if (!normalizedTarget || normalizedTarget === this.userId) {
			throw new Error('invalid target user for peer connection');
		}
		const existing = this.peerConnections.get(normalizedTarget);
		if (existing) {
			return existing;
		}
		if (this.peerConnections.size >= this.maxParticipants - 1) {
			throw new Error('call is limited to 5 participants');
		}

		const connection = new RTCPeerConnection(WEBRTC_STUN_CONFIG);
		this.peerConnections.set(normalizedTarget, connection);
		this.attachLocalTracks(connection);

		connection.onicecandidate = (event) => {
			if (!event.candidate) {
				return;
			}
			this.sendSignal({
				type: 'webrtc_ice',
				roomId: this.roomId,
				targetUserId: normalizedTarget,
				fromUserId: this.userId,
				fromUserName: this.userName,
				callType: this.currentCallType,
				payload: {
					candidate: event.candidate.toJSON(),
					targetUserId: normalizedTarget,
					fromUserId: this.userId,
					fromUserName: this.userName,
					callType: this.currentCallType
				}
			});
		};

		connection.ontrack = (event) => {
			const remoteStream = event.streams[0] ?? new MediaStream([event.track]);
			this.remoteStreams.set(normalizedTarget, remoteStream);
			this.onRemoteStream?.(normalizedTarget, remoteStream);
			this.dispatchEvent(
				new CustomEvent('remote-stream', {
					detail: { userId: normalizedTarget, stream: remoteStream }
				})
			);
		};

		connection.onconnectionstatechange = () => {
			if (
				connection.connectionState === 'failed' ||
				connection.connectionState === 'closed' ||
				connection.connectionState === 'disconnected'
			) {
				this.cleanupPeerConnection(normalizedTarget);
			}
		};

		if (isInitiator) {
			const offer = await connection.createOffer();
			await connection.setLocalDescription(offer);
			this.sendSignal({
				type: 'webrtc_offer',
				roomId: this.roomId,
				targetUserId: normalizedTarget,
				fromUserId: this.userId,
				fromUserName: this.userName,
				callType: this.currentCallType,
				payload: {
					offer: connection.localDescription,
					targetUserId: normalizedTarget,
					fromUserId: this.userId,
					fromUserName: this.userName,
					callType: this.currentCallType
				}
			});
		}

		return connection;
	}

	public inviteToCall(callType: CallType, targetUserIds: string[] = []) {
		this.currentCallType = callType;
		const normalizedTargets = Array.from(
			new Set(
				targetUserIds
					.map((entry) => normalizeIdentifier(entry))
					.filter((entry) => entry && entry !== this.userId)
			)
		);
		if (normalizedTargets.length === 0) {
			this.sendSignal({
				type: 'call_invite',
				roomId: this.roomId,
				fromUserId: this.userId,
				fromUserName: this.userName,
				callType,
				payload: {
					fromUserId: this.userId,
					fromUserName: this.userName,
					callType
				}
			});
			return;
		}
		for (const targetUserId of normalizedTargets) {
			this.sendSignal({
				type: 'call_invite',
				roomId: this.roomId,
				targetUserId,
				fromUserId: this.userId,
				fromUserName: this.userName,
				callType,
				payload: {
					targetUserId,
					fromUserId: this.userId,
					fromUserName: this.userName,
					callType
				}
			});
		}
	}

	public async connectToPeer(targetUserId: string, callType: CallType = this.currentCallType) {
		if (!this.localStream) {
			await this.startLocalStream(callType === 'video');
		}
		await this.createPeerConnection(targetUserId, true);
	}

	public async handleSignaling(message: unknown) {
		const source = toRecord(message);
		const payload = toRecord(source.payload);
		const eventType = toStringValue(source.type).toLowerCase();
		if (!isTransientSignalingType(eventType)) {
			return;
		}

		const messageRoomId = normalizeRoomId(
			toStringValue(source.roomId ?? source.room_id) ||
				toStringValue(payload.roomId ?? payload.room_id)
		);
		if (messageRoomId && this.roomId && messageRoomId !== this.roomId) {
			return;
		}

		const targetUserId = normalizeIdentifier(
			toStringValue(
				source.targetUserId ??
					source.target_user_id ??
					source.targetUser ??
					source.target_user ??
					payload.targetUserId ??
					payload.target_user_id ??
					payload.targetUser ??
					payload.target_user
			)
		);
		if (targetUserId && targetUserId !== this.userId) {
			return;
		}

		const fromUserId = normalizeIdentifier(
			toStringValue(
				source.fromUserId ??
					source.from_user_id ??
					source.userId ??
					source.user_id ??
					source.senderId ??
					source.sender_id ??
					payload.fromUserId ??
					payload.from_user_id ??
					payload.userId ??
					payload.user_id ??
					payload.senderId ??
					payload.sender_id
			)
		);
		if (fromUserId && fromUserId === this.userId) {
			return;
		}

		const fromUserName =
			toStringValue(source.fromUserName ?? source.from_user_name ?? payload.fromUserName ?? payload.from_user_name) ||
			'User';
		const callType = normalizeCallType(
			toStringValue(source.callType ?? source.call_type ?? payload.callType ?? payload.call_type),
			this.currentCallType
		);

		if (eventType === 'call_invite') {
			if (!fromUserId) {
				return;
			}
			const incomingEvent: IncomingCallEvent = {
				fromUserId,
				fromUserName,
				callType,
				targetUserId
			};
			this.onIncomingCall?.(incomingEvent);
			this.dispatchEvent(new CustomEvent('incoming-call', { detail: incomingEvent }));
			return;
		}

		if (eventType === 'webrtc_offer') {
			if (!fromUserId) {
				return;
			}
			if (!this.localStream) {
				await this.startLocalStream(callType === 'video');
			}
			const offer = toRecord(payload.offer || source.offer);
			const sdpType = toStringValue(offer.type || 'offer');
			const sdp = toStringValue(offer.sdp);
			if (!sdpType || !sdp) {
				return;
			}

			const offerConnection = await this.createPeerConnection(fromUserId, false);
			await offerConnection.setRemoteDescription(
				new RTCSessionDescription({
					type: sdpType as RTCSdpType,
					sdp
				})
			);
			const answer = await offerConnection.createAnswer();
			await offerConnection.setLocalDescription(answer);
			this.sendSignal({
				type: 'webrtc_answer',
				roomId: this.roomId,
				targetUserId: fromUserId,
				fromUserId: this.userId,
				fromUserName: this.userName,
				callType: this.currentCallType,
				payload: {
					answer: offerConnection.localDescription,
					targetUserId: fromUserId,
					fromUserId: this.userId,
					fromUserName: this.userName,
					callType: this.currentCallType
				}
			});
			return;
		}

		if (eventType === 'webrtc_answer') {
			if (!fromUserId) {
				return;
			}
			const answerConnection = this.peerConnections.get(fromUserId);
			if (!answerConnection) {
				return;
			}
			const answer = toRecord(payload.answer || source.answer);
			const sdpType = toStringValue(answer.type || 'answer');
			const sdp = toStringValue(answer.sdp);
			if (!sdpType || !sdp) {
				return;
			}
			await answerConnection.setRemoteDescription(
				new RTCSessionDescription({
					type: sdpType as RTCSdpType,
					sdp
				})
			);
			return;
		}

		if (eventType === 'webrtc_ice') {
			if (!fromUserId) {
				return;
			}
			const candidatePayload = toRecord(payload.candidate || source.candidate);
			const candidateValue = toStringValue(candidatePayload.candidate);
			if (!candidateValue) {
				return;
			}
			const connection =
				this.peerConnections.get(fromUserId) ?? (await this.createPeerConnection(fromUserId, false));
			await connection.addIceCandidate(
				new RTCIceCandidate({
					candidate: candidateValue,
					sdpMid: toStringValue(candidatePayload.sdpMid) || null,
					sdpMLineIndex:
						typeof candidatePayload.sdpMLineIndex === 'number'
							? candidatePayload.sdpMLineIndex
							: null,
					usernameFragment: toStringValue(candidatePayload.usernameFragment) || null
				})
			);
		}
	}

	public toggleMute() {
		if (!this.localStream) {
			return false;
		}
		const tracks = this.localStream.getAudioTracks();
		if (tracks.length === 0) {
			return false;
		}
		const shouldEnable = tracks.every((track) => !track.enabled);
		for (const track of tracks) {
			track.enabled = shouldEnable;
		}
		return tracks.every((track) => !track.enabled);
	}

	public toggleCamera() {
		if (!this.localStream) {
			return false;
		}
		const tracks = this.localStream.getVideoTracks();
		if (tracks.length === 0) {
			return false;
		}
		const shouldEnable = tracks.every((track) => !track.enabled);
		for (const track of tracks) {
			track.enabled = shouldEnable;
		}
		return tracks.some((track) => track.enabled);
	}

	public endCall() {
		const durationSeconds = this.callStartedAt
			? Math.max(0, Math.floor((Date.now() - this.callStartedAt) / 1000))
			: 0;
		this.callStartedAt = 0;

		for (const targetUserId of this.peerConnections.keys()) {
			this.cleanupPeerConnection(targetUserId);
		}
		this.peerConnections.clear();

		for (const targetUserId of this.remoteStreams.keys()) {
			this.remoteStreams.delete(targetUserId);
			this.onRemoteStreamRemoved?.(targetUserId);
			this.dispatchEvent(
				new CustomEvent('remote-stream-removed', {
					detail: { userId: targetUserId }
				})
			);
		}

		this.stopLocalStreamTracks();
		this.localStream = null;
		this.currentCallType = 'audio';
		return durationSeconds;
	}

	public dispose() {
		this.endCall();
	}

	private cleanupPeerConnection(targetUserId: string) {
		const connection = this.peerConnections.get(targetUserId);
		if (connection) {
			connection.onicecandidate = null;
			connection.ontrack = null;
			connection.onconnectionstatechange = null;
			connection.close();
		}
		this.peerConnections.delete(targetUserId);
		if (this.remoteStreams.has(targetUserId)) {
			this.remoteStreams.delete(targetUserId);
			this.onRemoteStreamRemoved?.(targetUserId);
			this.dispatchEvent(
				new CustomEvent('remote-stream-removed', {
					detail: { userId: targetUserId }
				})
			);
		}
	}

	private stopLocalStreamTracks() {
		if (!this.localStream) {
			return;
		}
		for (const track of this.localStream.getTracks()) {
			track.stop();
		}
	}

	private attachLocalTracks(connection: RTCPeerConnection) {
		if (!this.localStream) {
			return;
		}
		for (const track of this.localStream.getTracks()) {
			const hasSender = connection
				.getSenders()
				.some((sender) => sender.track && sender.track.id === track.id);
			if (!hasSender) {
				connection.addTrack(track, this.localStream);
			}
		}
	}

	private attachLocalTracksToExistingPeers() {
		for (const connection of this.peerConnections.values()) {
			this.attachLocalTracks(connection);
		}
	}
}
